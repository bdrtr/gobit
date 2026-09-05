//go:build integration

// The tests in this file need a real PostgreSQL instance (and therefore
// Docker); they are separated behind the `integration` tag so that `make test`
// stays fast. To run them: make test-integration
//
// The unit tests prove the service's DECISIONS against a fake store. The tests
// here prove the GROUND those decisions rest on: that the migration can be
// rolled back WITH DATA PRESENT, that the constraints really are enforced, that
// the provider's state lives outside the process, and that the idempotency
// claim holds at the database level. In particular, the claim that "two Creates
// with the same key produce one shipment" can only be exercised here, over real
// goroutines and a real unique index.
package fulfillment_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/link"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/fulfillment"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/manual"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/repository"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

const postgresImage = "postgres:16-alpine"

// moduleTables are the tables the module owns; the migration tests use this
// list.
var moduleTables = []string{
	"shipping_profiles", "shipping_options", "shipping_option_rules",
	"fulfillments", "fulfillment_items", "fulfillment_manual_shipments",
	"shipping_locations", "shipping_location_regions",
}

// Constants used in the test data. The reference belongs to ANOTHER module (to
// the order); this module does not verify its existence (Principle 2.2).
const (
	testReference = "order_TEST"
	testCurrency  = "TRY"
	testRegion    = "reg_TEST"
)

var (
	// testPool is the pool all the tests share.
	testPool *db.Pool
	// testDSN is the connection address for the migration calls.
	testDSN string
)

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres brings up a single Postgres container and runs all the tests
// on it. It is a separate function because os.Exit skips defers.
func runWithPostgres(m *testing.M) int {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_test"),
		tcpostgres.WithUsername("gobit"),
		tcpostgres.WithPassword("gobit"),
		tcpostgres.BasicWaitStrategies(),
	)
	defer func() {
		if termErr := testcontainers.TerminateContainer(ctr); termErr != nil {
			fmt.Fprintf(os.Stderr, "the postgres container could not be stopped: %v\n", termErr)
		}
	}()
	if err != nil {
		fmt.Fprintf(os.Stderr, "the postgres container could not be started: %v\n", err)
		return 1
	}

	testDSN, err = ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "the connection address could not be obtained: %v\n", err)
		return 1
	}

	cfg := db.DefaultConfig(testDSN)
	// The concurrency tests run dozens of goroutines at once; because every
	// transaction holds a connection, the pool is opened wider than the default.
	cfg.MaxConns = 24
	testPool, err = db.New(ctx, cfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "the connection pool could not be opened: %v\n", err)
		return 1
	}
	defer testPool.Close()

	if err := db.Migrate(ctx, testDSN, fulfillment.New().Migrations(), fulfillment.ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "the migration could not be applied: %v\n", err)
		return 1
	}

	return m.Run()
}

// newService sets up a service running on a real store and on the REAL manual
// provider.
func newService(t *testing.T) (*service.Service, *manual.Provider) {
	t.Helper()

	repo := repository.New(testPool.Pool())
	prov := manual.New(repo, nil)
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(prov))

	svc, err := service.New(service.Options{Store: repo, Providers: registry})
	require.NoError(t, err)
	return svc, prov
}

// countingProvider wraps the real provider and COUNTS THE CALLS.
//
// The claim that "one shipment is produced" can only be exercised DEFINITIVELY
// this way: because the manual provider is idempotent in itself, telling apart
// the work a second call does by looking at the row count in the ledger is not
// enough — what really has to be measured is HOW MANY TIMES THE PROVIDER WAS
// VISITED. At a real carrier every call means a label.
type countingProvider struct {
	inner *manual.Provider

	mu     sync.Mutex
	quote  int
	create int
	cancel int
}

// That the decorator satisfies the core's contract is verified at compile time.
var _ coreprovider.FulfillmentProvider = (*countingProvider)(nil)

// ID returns the wrapped provider's id; the options are opened under the same
// name.
func (s *countingProvider) ID() string { return s.inner.ID() }

// Quote counts the call and forwards it.
func (s *countingProvider) Quote(
	ctx context.Context,
	in coreprovider.QuoteInput,
) (coreprovider.ShippingQuote, error) {
	s.mu.Lock()
	s.quote++
	s.mu.Unlock()
	return s.inner.Quote(ctx, in)
}

// Create counts the call and forwards it.
func (s *countingProvider) Create(
	ctx context.Context,
	in coreprovider.CreateFulfillmentInput,
) (coreprovider.Fulfillment, error) {
	s.mu.Lock()
	s.create++
	s.mu.Unlock()
	return s.inner.Create(ctx, in)
}

// Cancel counts the call and forwards it.
func (s *countingProvider) Cancel(ctx context.Context, fulfillmentID string) error {
	s.mu.Lock()
	s.cancel++
	s.mu.Unlock()
	return s.inner.Cancel(ctx, fulfillmentID)
}

// counts returns the number of calls made to the provider.
func (s *countingProvider) counts() (quote, create, cancel int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.quote, s.create, s.cancel
}

// newCountingService sets up a service on a provider that counts the calls.
func newCountingService(t *testing.T) (*service.Service, *countingProvider) {
	t.Helper()

	repo := repository.New(testPool.Pool())
	counting := &countingProvider{inner: manual.New(repo, nil)}
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(counting))

	svc, err := service.New(service.Options{Store: repo, Providers: registry})
	require.NoError(t, err)
	return svc, counting
}

// newProfile opens a shipping profile with a unique name for the test.
func newProfile(ctx context.Context, t *testing.T, svc *service.Service) models.ShippingProfile {
	t.Helper()

	profile, err := svc.CreateShippingProfile(ctx, service.CreateProfileInput{
		Name: "profile-" + models.NewShippingProfileID(),
	})
	require.NoError(t, err)
	return profile
}

// newOption opens a flat-rate shipping option for the test.
func newOption(
	ctx context.Context,
	t *testing.T,
	svc *service.Service,
	profileID string,
	amount int64,
) models.ShippingOption {
	t.Helper()

	option, err := svc.CreateShippingOption(ctx, service.CreateOptionInput{
		Name:              "option-" + models.NewShippingOptionID(),
		ProviderID:        manual.ID,
		ShippingProfileID: profileID,
		Amount:            amount,
		CurrencyCode:      testCurrency,
		RegionID:          testRegion,
	})
	require.NoError(t, err)
	return option
}

// tableExists reports whether the table is present in the database.
func tableExists(ctx context.Context, t *testing.T, table string) bool {
	t.Helper()

	var exists bool
	err := testPool.Pool().QueryRow(ctx,
		`SELECT EXISTS (
             SELECT 1 FROM pg_class c
             JOIN pg_namespace n ON n.oid = c.relnamespace
             WHERE c.relname = $1 AND c.relkind = 'r' AND n.nspname = current_schema()
         )`, table).Scan(&exists)
	require.NoError(t, err)
	return exists
}

// TestMigrationRollsBackWithDataPresent verifies that the migration can be
// applied to and rolled back from a FULL schema.
//
// The gate in internal/arch only runs up -> down -> up on an EMPTY database and
// cannot catch data-dependent rollback failures. The test here first writes the
// COMPLETE graph made of a profile, an option, a rule, a shipment, an item and
// the provider's ledger; a down file that gets the foreign key order wrong only
// falls over that way.
func TestMigrationRollsBackWithDataPresent(t *testing.T) {
	ctx := context.Background()
	src := fulfillment.New().Migrations()
	svc, _ := newService(t)

	profile := newProfile(ctx, t, svc)
	option := newOption(ctx, t, svc, profile.ID, 2_500)
	_, err := svc.CreateShippingOptionRule(ctx, option.ID, service.CreateRuleInput{
		Attribute: service.AttrSubtotal,
		Operator:  "gte",
		Values:    []string{"50000"},
	})
	require.NoError(t, err)

	_, err = svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
		Reference:        testReference,
		ShippingOptionID: option.ID,
		IdempotencyKey:   "migration-" + option.ID,
		Items:            []service.FulfillmentItemInput{{LineItemID: "line_1", Quantity: 2}},
	})
	require.NoError(t, err)

	// The warehouse policy is written too, and it is written TOGETHER WITH ITS
	// REGION BINDING: there is an in-module foreign key between the two tables,
	// and that the rollback drops them in the right ORDER can only be exercised
	// with full tables. With empty tables a down in the wrong order would pass
	// as well.
	_, err = svc.SetShippingLocation(ctx, service.SetShippingLocationInput{
		LocationID: "sloc_migration",
		Priority:   -1,
		RegionIDs:  []string{testRegion},
	})
	require.NoError(t, err)

	for _, table := range moduleTables {
		require.True(t, tableExists(ctx, t, table), "%s must exist at the start", table)
	}

	require.NoError(t, db.MigrateDown(ctx, testDSN, src, fulfillment.ModuleName, 0),
		"down failed — this means the module can NEVER be migrated again")
	for _, table := range moduleTables {
		assert.False(t, tableExists(ctx, t, table), "%s must not remain after the rollback", table)
	}

	require.NoError(t, db.Migrate(ctx, testDSN, src, fulfillment.ModuleName))
	for _, table := range moduleTables {
		assert.True(t, tableExists(ctx, t, table), "%s must be applied again", table)
	}

	version, dirty, err := db.Version(ctx, testDSN, fulfillment.ModuleName)
	require.NoError(t, err)
	assert.False(t, dirty, "there must be no half-finished migration")
	assert.Equal(t, uint(2), version,
		"the version is the NUMBER of migrations in the module; when a new file is added "+
			"this goes up too. Were it held constant, an unapplied migration would "+
			"silently go unnoticed")
}

// TestNoCrossModuleForeignKeys verifies that ALL the foreign keys in the
// module's tables go to the module's own tables again (Principle 2.2).
//
// In particular fulfillments.reference is an order id, shipping_options.region_id
// is a region id and fulfillment_items.line_item_id is an order line id; none of
// the three CAN be a foreign key.
func TestNoCrossModuleForeignKeys(t *testing.T) {
	ctx := context.Background()

	rows, err := testPool.Pool().Query(ctx,
		`SELECT c.conname, src.relname, tgt.relname
         FROM pg_constraint c
         JOIN pg_class src ON src.oid = c.conrelid
         JOIN pg_class tgt ON tgt.oid = c.confrelid
         WHERE c.contype = 'f' AND src.relname = ANY($1)`, moduleTables)
	require.NoError(t, err)
	defer rows.Close()

	owned := make(map[string]struct{}, len(moduleTables))
	for _, table := range moduleTables {
		owned[table] = struct{}{}
	}

	var count int
	for rows.Next() {
		var name, src, tgt string
		require.NoError(t, rows.Scan(&name, &src, &tgt))
		assert.Contains(t, owned, tgt,
			"the %s constraint references outside the module (%s -> %s)", name, src, tgt)
		count++
	}
	require.NoError(t, rows.Err())
	assert.Positive(t, count, "in-module foreign keys must be used")
}

// TestCatalogCRUD verifies that a profile, an option and a rule can be managed
// end to end on the real schema.
func TestCatalogCRUD(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	profile := newProfile(ctx, t, svc)
	assert.Equal(t, models.ProfileDefault, profile.Type)

	option := newOption(ctx, t, svc, profile.ID, 2_500)
	rule, err := svc.CreateShippingOptionRule(ctx, option.ID, service.CreateRuleInput{
		Attribute: service.AttrSubtotal,
		Operator:  "gte",
		Values:    []string{"50000"},
	})
	require.NoError(t, err)

	loaded, err := svc.GetShippingOption(ctx, option.ID)
	require.NoError(t, err)
	require.Len(t, loaded.Rules, 1)
	assert.Equal(t, rule.ID, loaded.Rules[0].ID)

	newName := "updated-" + option.ID
	newAmount := int64(1_750)
	updated, err := svc.UpdateShippingOption(ctx, option.ID, service.UpdateOptionInput{
		Name:   &newName,
		Amount: &newAmount,
	})
	require.NoError(t, err)
	assert.Equal(t, newName, updated.Name)
	assert.Equal(t, newAmount, updated.Amount)
	assert.Equal(t, manual.ID, updated.ProviderID, "the provider must not change")

	// A profile with a standing option cannot be deleted; the rule keeps the
	// order flow from being left without a shipping option.
	err = svc.DeleteShippingProfile(ctx, profile.ID)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "the error must be errors.Conflict: %v", err)

	require.NoError(t, svc.DeleteShippingOptionRule(ctx, rule.ID))
	require.NoError(t, svc.DeleteShippingOption(ctx, option.ID))
	require.NoError(t, svc.DeleteShippingProfile(ctx, profile.ID))

	_, err = svc.GetShippingOption(ctx, option.ID)
	assert.True(t, errors.IsNotFound(err), "the deleted option must not be readable: %v", err)
}

// TestCalculatedOptionRejectsAmount verifies that the constraint in the schema
// works as the LAST LINE OF DEFENSE.
//
// The service already rejects this; the claim here is that an intervention made
// directly over SQL is stopped as well.
func TestCalculatedOptionRejectsAmount(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	profile := newProfile(ctx, t, svc)

	option, err := svc.CreateShippingOption(ctx, service.CreateOptionInput{
		Name:              "calculated-" + models.NewShippingOptionID(),
		ProviderID:        manual.ID,
		ShippingProfileID: profile.ID,
		PriceType:         "calculated",
		CurrencyCode:      testCurrency,
	})
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx,
		`UPDATE shipping_options SET amount = 500 WHERE id = $1`, option.ID)
	require.Error(t, err, "an amount must not be writable to a calculated option")
}

// TestEndToEndShipmentFlow runs the full flow Phase 7 asks for with the REAL
// provider: eligibility -> open shipment -> hand to carrier -> deliver.
//
// At every step both the module's record and the PROVIDER'S ledger are
// inspected; a fault where the two drift apart can only be seen by looking at
// both sides at once.
func TestEndToEndShipmentFlow(t *testing.T) {
	ctx := context.Background()
	svc, prov := newService(t)

	profile := newProfile(ctx, t, svc)
	option, err := svc.CreateShippingOption(ctx, service.CreateOptionInput{
		Name:              "calculated-" + models.NewShippingOptionID(),
		ProviderID:        manual.ID,
		ShippingProfileID: profile.ID,
		PriceType:         "calculated",
		CurrencyCode:      testCurrency,
		RegionID:          testRegion,
		Data: map[string]any{
			manual.DataKeyBaseAmount:        1_000,
			manual.DataKeyPerKilogramAmount: 500,
			manual.DataKeyTrackingNumber:    "TK-E2E",
		},
	})
	require.NoError(t, err)

	options, err := svc.ListShippingOptionsFor(ctx, service.ListOptionsInput{
		RegionID:           testRegion,
		CurrencyCode:       testCurrency,
		ShippingProfileIDs: []string{profile.ID},
		TotalWeight:        1_200,
	})
	require.NoError(t, err)
	require.Len(t, options, 1)
	// 1000 base + 500 x ⌈1200/1000⌉ = 1000 + 1000.
	assert.Equal(t, int64(2_000), options[0].Amount, "the fee must come from the provider's formula")

	ful, err := svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
		Reference:        testReference,
		ShippingOptionID: option.ID,
		IdempotencyKey:   "e2e-" + option.ID,
		Items:            []service.FulfillmentItemInput{{LineItemID: "line_1", Quantity: 2}},
	})
	require.NoError(t, err)
	assert.Equal(t, models.StatusPending, ful.Status)
	require.NotEmpty(t, ful.ExternalID, "the provider's id must be written")
	assert.Equal(t, "TK-E2E", ful.TrackingNumber)
	require.Len(t, ful.Items, 1)

	providerRecord, err := prov.GetShipment(ctx, ful.ExternalID)
	require.NoError(t, err)
	assert.Equal(t, ful.ID, providerRecord.Reference,
		"the provider must keep the SHIPMENT'S id for reconciliation")

	shipped, err := svc.MarkShipped(ctx, ful.ID, "TK-E2E", "https://carrier.example/TK-E2E")
	require.NoError(t, err)
	assert.Equal(t, models.StatusShipped, shipped.Status)
	require.NotNil(t, shipped.ShippedAt)

	delivered, err := svc.MarkDelivered(ctx, ful.ID)
	require.NoError(t, err)
	assert.Equal(t, models.StatusDelivered, delivered.Status)
	require.NotNil(t, delivered.DeliveredAt)

	// A delivered shipment CANNOT be canceled; the remedy is a refund.
	err = svc.CancelFulfillment(ctx, ful.ID)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "the error must be errors.Conflict: %v", err)
}

// TestConcurrentCreatesProduceOneShipment exercises the idempotency claim with
// a REAL unique index and real goroutines.
//
// The fake store in the unit test imitates the race; the test here proves the
// same claim over ON CONFLICT DO NOTHING and a row lock. What is measured is HOW
// MANY TIMES the provider was visited: at a real carrier every call means a
// label.
func TestConcurrentCreatesProduceOneShipment(t *testing.T) {
	ctx := context.Background()
	svc, counting := newCountingService(t)

	profile := newProfile(ctx, t, svc)
	option := newOption(ctx, t, svc, profile.ID, 2_500)
	key := "race-" + option.ID

	const concurrency = 8
	ids := make([]string, concurrency)
	errs := make([]error, concurrency)

	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	done.Add(concurrency)

	for i := range concurrency {
		go func() {
			defer done.Done()
			start.Wait()
			ful, err := svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
				Reference:        testReference,
				ShippingOptionID: option.ID,
				IdempotencyKey:   key,
			})
			ids[i], errs[i] = ful.ID, err
		}()
	}
	start.Done()
	done.Wait()

	for i, err := range errs {
		require.NoErrorf(t, err, "call %d returned an error", i)
	}
	for i := 1; i < concurrency; i++ {
		assert.Equal(t, ids[0], ids[i], "all the calls must return the same shipment")
	}

	_, create, _ := counting.counts()
	assert.Equal(t, 1, create, "the provider must be visited EXACTLY once")

	var rowCount int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT COUNT(*) FROM fulfillments WHERE idempotency_key = $1 AND deleted_at IS NULL`,
		key).Scan(&rowCount))
	assert.EqualValues(t, 1, rowCount, "the unique index must allow a single row")
}

// TestCancelCanBeCalledTwice verifies the saga compensation's condition over a
// real database.
func TestCancelCanBeCalledTwice(t *testing.T) {
	ctx := context.Background()
	svc, counting := newCountingService(t)

	profile := newProfile(ctx, t, svc)
	option := newOption(ctx, t, svc, profile.ID, 2_500)
	ful, err := svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
		Reference:        testReference,
		ShippingOptionID: option.ID,
		IdempotencyKey:   "cancel-" + option.ID,
	})
	require.NoError(t, err)

	require.NoError(t, svc.CancelFulfillment(ctx, ful.ID))
	require.NoError(t, svc.CancelFulfillment(ctx, ful.ID), "the second cancel must not return an error")

	_, _, cancel := counting.counts()
	assert.Equal(t, 1, cancel, "the provider must be visited only once")

	loaded, err := svc.GetFulfillment(ctx, ful.ID)
	require.NoError(t, err)
	assert.Equal(t, models.StatusCanceled, loaded.Status)
	require.NotNil(t, loaded.CanceledAt)
}

// TestConcurrentCancelMakesOneProviderCall verifies that the row lock really
// works.
//
// Without the lock, more than one goroutine would see the shipment as "pending"
// and they would all go to the provider.
func TestConcurrentCancelMakesOneProviderCall(t *testing.T) {
	ctx := context.Background()
	svc, counting := newCountingService(t)

	profile := newProfile(ctx, t, svc)
	option := newOption(ctx, t, svc, profile.ID, 2_500)
	ful, err := svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
		Reference:        testReference,
		ShippingOptionID: option.ID,
		IdempotencyKey:   "concurrent-cancel-" + option.ID,
	})
	require.NoError(t, err)

	const concurrency = 8
	errs := make([]error, concurrency)

	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	done.Add(concurrency)

	for i := range concurrency {
		go func() {
			defer done.Done()
			start.Wait()
			errs[i] = svc.CancelFulfillment(ctx, ful.ID)
		}()
	}
	start.Done()
	done.Wait()

	for i, err := range errs {
		require.NoErrorf(t, err, "cancel %d returned an error", i)
	}
	_, _, cancel := counting.counts()
	assert.Equal(t, 1, cancel, "EXACTLY one cancel must reach the provider")
}

// TestProviderLedgerOutlivesTheProcess verifies that the manual provider's
// state lives in the database.
//
// Were it held in memory, a NEW provider instance (the equivalent of a process
// restart) could not find the shipment and the saga compensation could never
// run.
func TestProviderLedgerOutlivesTheProcess(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	profile := newProfile(ctx, t, svc)
	option := newOption(ctx, t, svc, profile.ID, 2_500)
	ful, err := svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
		Reference:        testReference,
		ShippingOptionID: option.ID,
		IdempotencyKey:   "durable-" + option.ID,
	})
	require.NoError(t, err)

	// A new provider instance: the equivalent of the process restarting.
	freshProv := manual.New(repository.New(testPool.Pool()), nil)
	stored, err := freshProv.GetShipment(ctx, ful.ExternalID)
	require.NoError(t, err, "the shipment must be readable from a new provider instance")
	assert.Equal(t, models.StatusPending, stored.Status)

	require.NoError(t, freshProv.Cancel(ctx, ful.ExternalID),
		"the compensation must work after the process restarts too")
}

// TestModuleRegistersItsContainerSurfaces verifies that every name the module
// declares really can be resolved.
//
// As ADR 0001/0006 requires, consumers resolve these names with THEIR OWN
// narrow interfaces; forgetting to register a name is only seen at run time.
func TestModuleRegistersItsContainerSurfaces(t *testing.T) {
	ctx := context.Background()

	c := container.New(nil)
	require.NoError(t, c.Provide("core.db", testPool))
	// The module declares its link definitions in Register (ADR 0005), so it
	// needs the link service as well. Handing it a real one rather than a stub
	// is what makes the definition's SCHEMA part of what this test covers.
	require.NoError(t, c.Provide("core.link", link.New(testPool, nil)))

	mod := fulfillment.New()
	require.NoError(t, mod.Register(ctx, c))

	svc, err := container.Resolve[*service.Service](c, fulfillment.ServiceName)
	require.NoError(t, err)
	assert.NotNil(t, svc)

	interop, err := container.Resolve[*service.Interop](c, fulfillment.InteropName)
	require.NoError(t, err)
	assert.NotNil(t, interop)

	providers, err := container.Resolve[*service.ProviderRegistry](c, fulfillment.ProvidersName)
	require.NoError(t, err)
	assert.Equal(t, []string{manual.ID}, providers.IDs())

	qp, err := container.Resolve[query.Provider](c, fulfillment.ProviderName)
	require.NoError(t, err)
	assert.Equal(t, service.EntityName, qp.Entity())
	assert.Equal(t, "shipping_option.query", fulfillment.ProviderName)
}

// TestInteropSurfaceWorksEndToEnd verifies that the cross-module primitive
// surface works over a real database.
//
// This is the surface the saga will see; that the JSON schema and the
// idempotency hold together is only visible here.
func TestInteropSurfaceWorksEndToEnd(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	interop := service.NewInterop(svc)

	profile := newProfile(ctx, t, svc)
	option := newOption(ctx, t, svc, profile.ID, 2_500)

	request := fmt.Sprintf(
		`{"region_id":%q,"currency_code":%q,"shipping_profile_ids":[%q],"subtotal":50000}`,
		testRegion, testCurrency, profile.ID)
	response, err := interop.ListOptionsJSON(ctx, []byte(request))
	require.NoError(t, err)
	assert.Contains(t, string(response), option.ID)
	assert.Contains(t, string(response), `"amount":2500`)

	first, err := interop.CreateFulfillment(ctx, testReference, option.ID, "interop-"+option.ID)
	require.NoError(t, err)
	second, err := interop.CreateFulfillment(ctx, testReference, option.ID, "interop-"+option.ID)
	require.NoError(t, err)
	assert.Equal(t, first, second, "the same key must produce a single shipment")

	require.NoError(t, interop.CancelFulfillment(ctx, first))
	require.NoError(t, interop.CancelFulfillment(ctx, first), "the compensation must be callable twice")

	status, err := interop.FulfillmentStatus(ctx, first)
	require.NoError(t, err)
	assert.Equal(t, "canceled", status)
}

// TestProfileNameCannotBeReused verifies that the unique index really is
// enforced.
func TestProfileNameCannotBeReused(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	name := "unique-" + models.NewShippingProfileID()
	_, err := svc.CreateShippingProfile(ctx, service.CreateProfileInput{Name: name})
	require.NoError(t, err)

	_, err = svc.CreateShippingProfile(ctx, service.CreateProfileInput{Name: name})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "the error must be errors.Conflict: %v", err)
}

// TestQueryProviderWorksOnTheRealSchema verifies ADR 0004's read surface with
// real data.
func TestQueryProviderWorksOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	provider := service.NewQueryProvider(svc)

	profile := newProfile(ctx, t, svc)
	option := newOption(ctx, t, svc, profile.ID, 2_500)

	records, err := provider.FetchByIDs(ctx, []string{option.ID}, []string{"id", "amount", "provider_id"})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, option.ID, records[0]["id"])
	assert.Equal(t, int64(2_500), records[0]["amount"])
	assert.Equal(t, manual.ID, records[0]["provider_id"])

	filtered, err := provider.List(ctx, query.ListOptions{
		Filters: map[string]any{"shipping_profile_id": profile.ID},
		Fields:  []string{"id"},
	})
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	assert.Equal(t, option.ID, filtered[0]["id"])
}

// TestSameLineItemCannotAppearTwiceInAShipment verifies that the unique index
// works as the last line of defense.
func TestSameLineItemCannotAppearTwiceInAShipment(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	profile := newProfile(ctx, t, svc)
	option := newOption(ctx, t, svc, profile.ID, 2_500)
	ful, err := svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
		Reference:        testReference,
		ShippingOptionID: option.ID,
		IdempotencyKey:   "item-" + option.ID,
		Items:            []service.FulfillmentItemInput{{LineItemID: "line_1", Quantity: 1}},
	})
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO fulfillment_items (id, fulfillment_id, line_item_id, quantity)
         VALUES ($1, $2, 'line_1', 1)`, models.NewFulfillmentItemID(), ful.ID)
	require.Error(t, err, "the same order line must not be writable twice")
}

// TestProfileDeleteWaitsForAnOpenOptionWrite verifies that the check-then-write
// race is closed on REAL Postgres.
//
// Regression: DeleteShippingProfile used to read and count the profile WITHOUT A
// LOCK and then soft-delete it. Because a soft delete updates a non-key column
// it takes FOR NO KEY UPDATE, and that lock does NOT conflict with the FOR KEY
// SHARE an option INSERT takes for the foreign key. The result: while an open
// INSERT transaction existed the delete completed without waiting, leaving a
// LIVE option bound to a deleted profile.
//
// The test sets that interleaving up EXACTLY: the option row is written in an
// open transaction (not committed yet) — that is, the profile row carries only
// the FK's FOR KEY SHARE lock — and the delete is called. BEFORE the fix the
// delete completed without error and the profile would be NotFound; with the fix
// the delete tries to take the row with FOR UPDATE, WAITS, and the context times
// out.
func TestProfileDeleteWaitsForAnOpenOptionWrite(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	profile := newProfile(ctx, t, svc)

	// A: the option INSERT is held in an open transaction.
	tx, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			t.Logf("the transaction could not be rolled back: %v", rbErr)
		}
	}()

	optionID := models.NewShippingOptionID()
	_, err = tx.Exec(ctx,
		`INSERT INTO shipping_options
             (id, name, provider_id, shipping_profile_id, price_type, amount, currency_code, region_id)
         VALUES ($1, 'race', $2, $3, 'flat', 2500, $4, $5)`,
		optionID, manual.ID, profile.ID, testCurrency, testRegion)
	require.NoError(t, err)

	// B: the administrator trying to delete the profile at the same time.
	deleteCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	deleteErr := svc.DeleteShippingProfile(deleteCtx, profile.ID)
	require.Error(t, deleteErr,
		"the profile delete must not complete while an option write is open (it waits on the lock)")

	require.NoError(t, tx.Commit(ctx))

	// The real claim: the profile is STILL live. Before the fix this was NotFound
	// and a live option bound to a deleted profile was left behind.
	loaded, err := svc.GetShippingProfile(ctx, profile.ID)
	require.NoError(t, err, "the profile must not have been deleted")
	assert.Nil(t, loaded.DeletedAt)

	// And the delete is now rejected for the right reason.
	err = svc.DeleteShippingProfile(ctx, profile.ID)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "the error must be errors.Conflict: %v", err)
	assert.Equal(t, service.CodeProfileInUse, errors.CodeOf(err))
}

// TestOptionOfDeletedProfileIsHiddenInTheStorefront verifies the eligibility
// query's second line of defense.
//
// In the normal flow such a row can no longer come about (the lock above), but a
// maintenance script running SQL directly can produce one. An option of a profile
// whose shipping rule has vanished must not stand in the storefront.
func TestOptionOfDeletedProfileIsHiddenInTheStorefront(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	profile := newProfile(ctx, t, svc)
	option := newOption(ctx, t, svc, profile.ID, 2_500)

	before, err := svc.ListShippingOptionsFor(ctx, service.ListOptionsInput{
		RegionID:           testRegion,
		CurrencyCode:       testCurrency,
		ShippingProfileIDs: []string{profile.ID},
	})
	require.NoError(t, err)
	require.Len(t, before, 1)
	assert.Equal(t, option.ID, before[0].Option.ID)

	// The service does not produce this state; direct SQL does.
	_, err = testPool.Pool().Exec(ctx,
		`UPDATE shipping_profiles SET deleted_at = now() WHERE id = $1`, profile.ID)
	require.NoError(t, err)

	after, err := svc.ListShippingOptionsFor(ctx, service.ListOptionsInput{
		RegionID:           testRegion,
		CurrencyCode:       testCurrency,
		ShippingProfileIDs: []string{profile.ID},
	})
	require.NoError(t, err)
	assert.Empty(t, after, "an option whose profile is deleted must not enter the eligibility list")
}

// TestItemQuantityUpperBoundIsEnforcedInTheSchema verifies that the
// money/quantity rule holds INDEPENDENTLY OF THE APPLICATION LAYER too.
//
// The service already enforces the same bound; the constraint here is the last
// line of defense and stops a maintenance script running SQL directly as well.
func TestItemQuantityUpperBoundIsEnforcedInTheSchema(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	profile := newProfile(ctx, t, svc)
	option := newOption(ctx, t, svc, profile.ID, 2_500)
	ful, err := svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
		Reference:        testReference,
		ShippingOptionID: option.ID,
		IdempotencyKey:   "quantity-" + option.ID,
	})
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO fulfillment_items (id, fulfillment_id, line_item_id, quantity)
         VALUES ($1, $2, 'line_1', $3)`,
		models.NewFulfillmentItemID(), ful.ID, models.MaxQuantity+1)
	require.Error(t, err, "a quantity above the upper bound must not be writable to the schema")

	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO fulfillment_items (id, fulfillment_id, line_item_id, quantity)
         VALUES ($1, $2, 'line_2', $3)`,
		models.NewFulfillmentItemID(), ful.ID, models.MaxQuantity)
	require.NoError(t, err, "a quantity at the bound must be writable")
}

// TestOptionWithShipmentsCannotBeHardDeleted verifies why the soft delete is
// mandatory.
//
// ON DELETE RESTRICT protects the record of an option that has a history; this
// is why the service offers only a soft delete.
func TestOptionWithShipmentsCannotBeHardDeleted(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	profile := newProfile(ctx, t, svc)
	option := newOption(ctx, t, svc, profile.ID, 2_500)
	_, err := svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
		Reference:        testReference,
		ShippingOptionID: option.ID,
		IdempotencyKey:   "restrict-" + option.ID,
	})
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx, `DELETE FROM shipping_options WHERE id = $1`, option.ID)
	require.Error(t, err, "an option that has shipments must not be hard deleted")

	require.NoError(t, svc.DeleteShippingOption(ctx, option.ID),
		"the soft delete must always be possible")
}
