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
	"github.com/bdrtr/gobit/core/eventbus"
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

	svc, err := service.New(service.Options{
		Store: repo, Providers: registry,
		// A parcel cannot be opened without a bound on what the order still owes
		// (ADR 0135), and these scenarios are about the SQL rather than the bound —
		// so it answers generously and the dispatch tests next door answer for it.
		DispatchBound: generousBound{},
	})
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

	svc, err := service.New(service.Options{
		Store: repo, Providers: registry,
		// A parcel cannot be opened without a bound on what the order still owes
		// (ADR 0135), and these scenarios are about the SQL rather than the bound —
		// so it answers generously and the dispatch tests next door answer for it.
		DispatchBound: generousBound{},
	})
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
	assert.Equal(t, uint(4), version,
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
		`SELECT COUNT(*) FROM fulfillments WHERE idempotency_key = $1`,
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
	// Since ADR 0139 the module PUBLISHES — a canceled parcel releases units a
	// write-off counted as gone, and a flow has to hear it — so the bus is a hard
	// dependency and the module does not register without one.
	require.NoError(t, c.Provide("core.eventbus", eventbus.NewInMemory(nil)))

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

// --- the dropped soft-delete column (D18) --------------------------------

// TestTheFulfillmentTableHasNoSoftDeleteColumn pins the removal made by
// migration 000003.
//
// The column stood from the first migration and NOTHING ever wrote it, while
// every read carried "deleted_at IS NULL" — a predicate that had never once
// been false (docs/gaps.md D18). A shipment is the record of something that
// happened; it is retired by the 'canceled' status, which a CHECK refuses to
// accept without its moment.
func TestTheFulfillmentTableHasNoSoftDeleteColumn(t *testing.T) {
	ctx := context.Background()

	var exists bool
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT EXISTS (
             SELECT 1 FROM information_schema.columns
             WHERE table_name = 'fulfillments' AND column_name = 'deleted_at')`,
	).Scan(&exists))
	assert.False(t, exists,
		"fulfillments.deleted_at is back; while it exists the uniqueness rule reads "+
			"'unique among LIVING rows' and a second live shipment can be written "+
			"against the same idempotency key by stamping the first")
}

// TestTheIdempotencyGuardSurvivedTheDroppedColumn is the reason migration
// 000003 rebuilds its indexes, and it is the most expensive thing that could
// have gone wrong here.
//
// PostgreSQL drops any index whose PREDICATE names a dropped column — silently,
// with no notice and no error. fulfillments_idempotency_uniq was partial on
// deleted_at and it is the SINGLE POINT of the race that stops a retried saga
// step from producing A SECOND SHIPPING LABEL. Measured on a real PostgreSQL 16
// before the migration was written: on a probe table the DROP COLUMN took the
// unique index with it and the duplicate key that had been impossible one
// statement earlier was accepted, with the schema looking untouched.
//
// The test does not stop at "the index is listed". It writes the second row and
// requires the database to refuse it: an index can exist under the right name
// and cover the wrong columns, and only the write says which.
func TestTheIdempotencyGuardSurvivedTheDroppedColumn(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	profile := newProfile(ctx, t, svc)
	option := newOption(ctx, t, svc, profile.ID, 1_000)
	key := "idem-" + models.NewFulfillmentID()

	var definition string
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT indexdef FROM pg_indexes
         WHERE tablename = 'fulfillments' AND indexname = 'fulfillments_idempotency_uniq'`,
	).Scan(&definition), "the idempotency index is gone")
	assert.Contains(t, definition, "UNIQUE")
	assert.NotContains(t, definition, "deleted_at")

	insert := `INSERT INTO fulfillments (id, reference, shipping_option_id, provider_id, idempotency_key)
               VALUES ($1, $2, $3, 'manual', $4)`
	_, err := testPool.Pool().Exec(ctx, insert,
		models.NewFulfillmentID(), testReference, option.ID, key)
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx, insert,
		models.NewFulfillmentID(), testReference, option.ID, key)
	require.Error(t, err,
		"a second shipment was written under the same idempotency key: the unique "+
			"index is not there, and a retried saga step can print a second label")
	assert.Contains(t, err.Error(), "fulfillments_idempotency_uniq")
}

// --- a parcel that came back, and events that arrive in the wrong order ------

// TestTheReturnedStampConstraintIsAFullMirror exercises what migration 000004
// actually added to the schema, in both directions.
//
// The three stamp constraints 000001 wrote are one-directional and could not
// have been anything else: shipped_at SURVIVES into 'delivered', so requiring
// the status whenever the moment is present would be false for every delivered
// shipment. 'returned' is terminal, nothing follows it, and the mirror is
// therefore expressible — which means it has to be exercised in the direction
// the others cannot have, or the difference between the two shapes is a claim
// in a comment.
//
// It is written with raw SQL on purpose. The service can no longer produce
// either violation, so going through it would prove the service and say nothing
// about the schema; a maintenance script and a partial restore reach the table
// without the service, and the constraint is what stands there.
func TestTheReturnedStampConstraintIsAFullMirror(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	profile := newProfile(ctx, t, svc)
	option := newOption(ctx, t, svc, profile.ID, 1_000)

	// Both rows satisfy EVERY OTHER constraint on the table, and that is what
	// makes the assertion about the constraint NAME meaningful: a row that also
	// trips fulfillments_shipped_stamp would fail either way, and the test would
	// pass while saying nothing about the mirror.
	insert := `INSERT INTO fulfillments (id, reference, shipping_option_id, provider_id,
                                         idempotency_key, status, shipped_at, returned_at)
               VALUES ($1, $2, $3, 'manual', $4, $5, $6, $7)`
	now := time.Now().UTC()

	_, err := testPool.Pool().Exec(ctx, insert,
		models.NewFulfillmentID(), testReference, option.ID,
		"mirror-a-"+models.NewFulfillmentID(), "returned", now, nil)
	require.Error(t, err,
		"the 'returned' status was written WITHOUT its moment; a returned parcel whose "+
			"return has no date cannot be reconciled against the carrier")
	assert.Contains(t, err.Error(), "fulfillments_returned_stamp")

	_, err = testPool.Pool().Exec(ctx, insert,
		models.NewFulfillmentID(), testReference, option.ID,
		"mirror-b-"+models.NewFulfillmentID(), "shipped", now, now)
	require.Error(t, err,
		"returned_at was written WITHOUT the status; the row carries the evidence that "+
			"the parcel came back while every status filter still counts it as in transit. "+
			"This is the direction the three one-directional stamp constraints CANNOT have, "+
			"and it is the whole reason this one is written as a mirror")
	assert.Contains(t, err.Error(), "fulfillments_returned_stamp")
}

// TestTheStatusCheckAcceptsReturnedAndNothingElse proves the CHECK was REPLACED
// rather than added to.
//
// A second CHECK naming the same column would have been ANDed with the first,
// so 'returned' would still have been refused while the new constraint looked
// like it worked. The negative half is asserted in the same test because a
// widened vocabulary that accepts anything is not a vocabulary.
func TestTheStatusCheckAcceptsReturnedAndNothingElse(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	profile := newProfile(ctx, t, svc)
	option := newOption(ctx, t, svc, profile.ID, 1_000)

	insert := `INSERT INTO fulfillments (id, reference, shipping_option_id, provider_id,
                                         idempotency_key, status, shipped_at, returned_at)
               VALUES ($1, $2, $3, 'manual', $4, $5, now(), $6)`

	_, err := testPool.Pool().Exec(ctx, insert,
		models.NewFulfillmentID(), testReference, option.ID,
		"check-ok-"+models.NewFulfillmentID(), "returned", time.Now().UTC())
	require.NoError(t, err,
		"'returned' is refused; migration 000004 added a CHECK next to the old one "+
			"instead of replacing it, and the two are ANDed")

	_, err = testPool.Pool().Exec(ctx, insert,
		models.NewFulfillmentID(), testReference, option.ID,
		"check-bad-"+models.NewFulfillmentID(), "iade", nil)
	require.Error(t, err, "the status vocabulary must stay closed")
	assert.Contains(t, err.Error(), "fulfillments_status_valid")
}

// TestOutOfOrderCarrierEventsSurviveTheRealSchema replays a reordered carrier
// sequence against a real PostgreSQL.
//
// The unit test proves the SERVICE's decisions against a fake store. What can
// only be proved here is that the row the decisions produce is a row the
// database accepts: a delivered shipment whose shipped_at is NULL passes
// fulfillments_shipped_stamp only because that constraint is one-directional,
// and if it were ever tightened to a mirror this state would become unwritable
// while every unit test still passed.
func TestOutOfOrderCarrierEventsSurviveTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	profile := newProfile(ctx, t, svc)
	option := newOption(ctx, t, svc, profile.ID, 1_000)

	ful, err := svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
		Reference:        testReference,
		ShippingOptionID: option.ID,
		IdempotencyKey:   "ooo-" + models.NewFulfillmentID(),
	})
	require.NoError(t, err)

	// The delivery message wins the race, and it is repeated.
	for range 2 {
		delivered, deliverErr := svc.MarkDelivered(ctx, ful.ID)
		require.NoError(t, deliverErr, "a repeated delivery must be absorbed")
		assert.Equal(t, models.StatusDelivered, delivered.Status)
		assert.Nil(t, delivered.ShippedAt,
			"the dispatch moment was never reported and must not be invented")
	}

	// The collection message arrives afterwards, twice, carrying the number the
	// provider issued when the shipment was opened.
	for range 2 {
		late, shipErr := svc.MarkShipped(ctx, ful.ID, ful.TrackingNumber, "")
		require.NoError(t, shipErr,
			"a collection reported after the delivery is a late message, not a contradiction; "+
				"refusing it leaves the carrier retrying it forever")
		assert.Equal(t, models.StatusDelivered, late.Status, "the status must not move backwards")
	}

	// And the row really is on disk in that shape.
	var status string
	var shippedAt, deliveredAt *time.Time
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT status, shipped_at, delivered_at FROM fulfillments WHERE id = $1`, ful.ID,
	).Scan(&status, &shippedAt, &deliveredAt))
	assert.Equal(t, "delivered", status)
	assert.Nil(t, shippedAt, "shipped_at stays null; the moment is unknown, not zero")
	assert.NotNil(t, deliveredAt)
}

// TestAParcelComesBackThroughTheRealSchema drives the "iade" transition end to
// end against a real database.
//
// The service, the query and the constraint have to agree about which of the
// four stamps a return writes; a mismatch between them shows up nowhere else,
// because UpdateFulfillmentStatus passes all four absolutely and the fake store
// would happily accept a combination the CHECK refuses.
func TestAParcelComesBackThroughTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	profile := newProfile(ctx, t, svc)
	option := newOption(ctx, t, svc, profile.ID, 1_000)

	ful, err := svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
		Reference:        testReference,
		ShippingOptionID: option.ID,
		IdempotencyKey:   "iade-" + models.NewFulfillmentID(),
	})
	require.NoError(t, err)

	_, err = svc.MarkShipped(ctx, ful.ID, ful.TrackingNumber, "")
	require.NoError(t, err)

	returned, err := svc.MarkReturned(ctx, ful.ID)
	require.NoError(t, err)
	assert.Equal(t, models.StatusReturned, returned.Status)
	require.NotNil(t, returned.ReturnedAt)

	var status string
	var returnedAt, canceledAt, deliveredAt *time.Time
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT status, returned_at, canceled_at, delivered_at FROM fulfillments WHERE id = $1`, ful.ID,
	).Scan(&status, &returnedAt, &canceledAt, &deliveredAt))
	assert.Equal(t, "returned", status)
	assert.NotNil(t, returnedAt, "the moment reached the column, not just the model")
	assert.Nil(t, canceledAt, "a parcel that came back was not canceled by us")
	assert.Nil(t, deliveredAt, "and it never reached the recipient")

	// The listing filter has to accept the new value, or a returned parcel is
	// findable only by reading every page.
	list, total, err := svc.ListFulfillments(ctx, service.ListFulfillmentsInput{
		Status: ptr("returned"),
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, total, int64(1))
	found := false
	for i := range list {
		if list[i].ID == ful.ID {
			found = true
		}
	}
	assert.True(t, found, "the returned shipment must be reachable through the status filter")
}

// ptr returns a pointer to the given value; the listing filters take pointers so
// that "not given" and "given empty" stay distinguishable.
func ptr[T any](value T) *T { return &value }

// TestTheProfileTotalIsTheCountOfTheFilterNotOfThePage proves the claim the
// listing query's own godoc makes: the total in the pagination envelope comes
// from a SEPARATE count and survives a page that returns no rows at all.
//
// The rejected alternative was a window function returned alongside the rows.
// It is wrong for a reason no fake store can show: on an out-of-range page
// Postgres evaluates the window ZERO times, so the total comes back as 0. The
// admin screen paging through profiles would then be told, the moment it walked
// one page too far, that the store has no shipping profiles — and the operator's
// reasonable next move is to create the "missing" default profile, whose name
// then collides with the one that was there all along.
//
// The second half of the claim is that the two queries agree. They repeat the
// same WHERE clause in two separate SQL strings and nothing in the type system
// ties them together; a filter added to one and forgotten in the other is
// invisible on the first page (the rows are right) and only shows up as a count
// that never matches what the operator can see.
func TestTheProfileTotalIsTheCountOfTheFilterNotOfThePage(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	// A profile of ANOTHER type is opened FIRST, and the order matters: it is
	// the row that makes a dropped filter visible. Without a profile the filter
	// has to exclude, both queries could ignore the type entirely and every
	// number below would still agree with itself.
	other := newProfile(ctx, t, svc)
	require.Equal(t, models.ProfileDefault, other.Type)

	// Other tests in this file open profiles of the default type as well; the
	// count of gift-card profiles is therefore read BEFORE the writes rather
	// than assumed to be zero.
	giftCard := "gift_card"
	_, before, err := svc.ListShippingProfiles(ctx, service.ListProfilesInput{Type: &giftCard})
	require.NoError(t, err)

	const written = 3
	for range written {
		_, createErr := svc.CreateShippingProfile(ctx, service.CreateProfileInput{
			Name: "gift-" + models.NewShippingProfileID(),
			Type: giftCard,
		})
		require.NoError(t, createErr)
	}

	// A page that fits none of them: the rows are empty, the total is not.
	rows, total, err := svc.ListShippingProfiles(ctx, service.ListProfilesInput{
		Type: &giftCard,
		Page: service.Page{Limit: 2, Offset: before + written + 10},
	})
	require.NoError(t, err)
	assert.Empty(t, rows, "the page is past the end; no row can be on it")
	assert.Equal(t, before+written, total,
		"the total is the count of the FILTER, not of the page; a window function "+
			"returned with the rows would report 0 here and the store would look empty")

	// And the filter really is applied by BOTH queries: every row that comes
	// back is a gift-card profile, and the total counts only those.
	rows, total, err = svc.ListShippingProfiles(ctx, service.ListProfilesInput{
		Type: &giftCard,
		Page: service.Page{Limit: service.MaxLimit},
	})
	require.NoError(t, err)
	assert.Equal(t, before+written, total)
	assert.Len(t, rows, int(before+written),
		"the count and the listing must agree; they repeat the same WHERE clause in two places")
	for i := range rows {
		assert.Equal(t, models.ProfileGiftCard, rows[i].Type,
			"the listing must not return a profile of another type")
	}

	// And the same question asked WITHOUT the filter has to give a bigger
	// answer, or "the two agree" would be satisfied by both of them ignoring
	// the filter together.
	_, all, err := svc.ListShippingProfiles(ctx, service.ListProfilesInput{})
	require.NoError(t, err)
	assert.Greater(t, all, total,
		"the unfiltered count must exceed the filtered one; a default-type "+
			"profile was opened above that the gift-card filter has to exclude")
}

// TestASoftDeletedProfileCannotBeUpdatedBackIntoTheCatalog proves that the
// update statement carries its own deleted_at guard, independently of the
// service's read-before-write.
//
// The service refuses this at a higher altitude: UpdateShippingProfile reads the
// profile first and that read filters the soft-deleted rows out. The test
// therefore drives the REPOSITORY directly, because that guard is the one that
// disappears silently. A caller that already holds the row — a bulk importer, a
// backfill, a future handler that skips the read because it "just fetched it" —
// reaches the statement with nothing in front of it, and without the predicate
// the UPDATE would succeed against a deleted row and RETURNING would hand back a
// perfectly healthy-looking profile that no listing will ever show again.
//
// Only a database can witness this: the guard is a WHERE clause, and a fake
// store's map has no notion of a row it is refusing to touch.
func TestASoftDeletedProfileCannotBeUpdatedBackIntoTheCatalog(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	repo := repository.New(testPool.Pool())

	profile := newProfile(ctx, t, svc)
	require.NoError(t, svc.DeleteShippingProfile(ctx, profile.ID))

	profile.Name = "resurrected-" + models.NewShippingProfileID()
	_, err := repo.UpdateShippingProfile(ctx, profile)
	require.Error(t, err,
		"the UPDATE has to refuse a soft-deleted row on its own; the service's "+
			"read-before-write is not the only thing standing here")
	assert.True(t, errors.IsNotFound(err),
		"a deleted profile is ABSENT, not merely un-writable: %v", err)

	// The row is still deleted and still carries its original name — the
	// statement did not half-apply before deciding it had matched nothing.
	var name string
	var deletedAt *time.Time
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT name, deleted_at FROM shipping_profiles WHERE id = $1`, profile.ID,
	).Scan(&name, &deletedAt))
	assert.NotNil(t, deletedAt, "the profile must have stayed deleted")
	assert.NotEqual(t, profile.Name, name, "the refused update must not have written the new name")
}

// TestASoftDeletedRuleIsNotReadableByItsIdentifier proves that reading a rule by
// id honors the soft delete.
//
// A shipping option rule is an ELIMINATION: "subtotal >= 50000" is what closes
// free shipping to a small cart. Deleting one is how an operator OPENS an option
// back up, and a read path that still finds the deleted row would report the
// store's own catalog as more restricted than it is — an admin screen showing a
// rule that no longer governs anything, on an option the customer can already
// see.
//
// This claim can only be made against a database, for the same reason as the
// profile update above: the guard is a WHERE clause on a column the deletion
// merely stamps, and the row is still physically there.
//
// FINDING, recorded here rather than hidden: nothing in this repository calls
// Repository.GetShippingOptionRule. It is declared on the service's Store
// interface, implemented, and generated by sqlc, and no service method reaches
// it — the rule surface reads through ListShippingOptionRules and deletes by id
// without a read. The test is written because the method is part of the Store
// CONTRACT and an implementation that leaked deleted rows would be a real defect
// the moment anything wires it up; it is not written to suggest the method is
// load-bearing today.
func TestASoftDeletedRuleIsNotReadableByItsIdentifier(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	repo := repository.New(testPool.Pool())

	profile := newProfile(ctx, t, svc)
	option := newOption(ctx, t, svc, profile.ID, 1_000)

	rule, err := svc.CreateShippingOptionRule(ctx, option.ID, service.CreateRuleInput{
		Attribute: "subtotal",
		Operator:  "gte",
		Values:    []string{"50000"},
	})
	require.NoError(t, err)

	// While it is alive the read finds it and carries every field, so the
	// refusal below is about the deletion and not about the query being broken.
	found, err := repo.GetShippingOptionRule(ctx, rule.ID)
	require.NoError(t, err)
	assert.Equal(t, option.ID, found.ShippingOptionID)
	assert.Equal(t, "subtotal", found.Attribute)
	assert.Equal(t, models.OpGte, found.Operator)
	assert.Equal(t, []string{"50000"}, found.Values)

	require.NoError(t, svc.DeleteShippingOptionRule(ctx, rule.ID))

	_, err = repo.GetShippingOptionRule(ctx, rule.ID)
	require.Error(t, err, "a deleted rule must not be readable by its identifier")
	assert.True(t, errors.IsNotFound(err),
		"a deleted rule is ABSENT; reporting it would show the operator an "+
			"elimination that no longer governs anything: %v", err)

	// The row really is still on disk — the refusal comes from the predicate,
	// not from the row having been removed, which is what makes the predicate
	// the only thing standing between a deleted rule and a reader.
	var deletedAt *time.Time
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT deleted_at FROM shipping_option_rules WHERE id = $1`, rule.ID,
	).Scan(&deletedAt))
	assert.NotNil(t, deletedAt, "the deletion is a stamp; the row is still there")
}

// TestARepeatedKeyReturnsTheCanceledShipment pins what the module really does,
// and it is the fact a FLOW-level decision rests on.
//
// # Why this is asserted here rather than assumed
//
// internal/workflows/fulfilling refuses to report a canceled shipment as an
// open one, and the whole refusal rests on this: an idempotency key OUTLIVES
// the shipment it opened, so repeating it resolves to the canceled parcel
// instead of opening a new one. That was read out of the code — migration
// 000003 made the key unique over EVERY shipment rather than the live ones, and
// the create's repeated-key branch compares the reference, the option and the
// item set and never looks at the status. Reading is not running, and a fake
// that answered differently would have made the flow's test agree with itself.
//
// # What it does NOT say
//
// It does not say the module is wrong. The contract is "the same key returns
// the same shipment" and it says nothing about status; returning what the key
// produced is idempotency working. What was wrong was a CALLER treating that
// answer as "a parcel is open", and that is where the fix went.
func TestARepeatedKeyReturnsTheCanceledShipment(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	profile := newProfile(ctx, t, svc)
	option := newOption(ctx, t, svc, profile.ID, 2_500)
	key := "canceled-key-" + option.ID

	opened, err := svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
		Reference:        testReference,
		ShippingOptionID: option.ID,
		IdempotencyKey:   key,
	})
	require.NoError(t, err)

	require.NoError(t, svc.CancelFulfillment(ctx, opened.ID))

	repeated, err := svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
		Reference:        testReference,
		ShippingOptionID: option.ID,
		IdempotencyKey:   key,
	})
	require.NoError(t, err,
		"a repeated key must not fail; the key names a shipment and that shipment still exists")

	assert.Equal(t, opened.ID, repeated.ID,
		"the key resolves to the SHIPMENT it opened, canceled or not — this is what makes "+
			"the caller-side refusal necessary")

	current, err := svc.GetFulfillment(ctx, repeated.ID)
	require.NoError(t, err)
	assert.Equal(t, models.StatusCanceled, current.Status,
		"the shipment the key names is canceled, and nothing about the repeated create "+
			"revived it")

	var rows int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT COUNT(*) FROM fulfillments WHERE idempotency_key = $1`, key).Scan(&rows))
	assert.EqualValues(t, 1, rows,
		"the canceled shipment still holds the key: migration 000003 made it unique over "+
			"every shipment rather than the live ones")
}

// generousBound answers that every line asked about owes a thousand units.
//
// The module fails CLOSED when it cannot read what an order still owes, which is
// what makes a missing bound loud — and these scenarios would otherwise all be
// tests of that one refusal.
type generousBound struct{}

// DispatchableQuantities answers generously for whatever it is asked about.
func (generousBound) DispatchableQuantities(
	_ context.Context, _ string, lineItemIDs []string,
) (map[string]int64, error) {
	out := make(map[string]int64, len(lineItemIDs))
	for _, id := range lineItemIDs {
		out[id] = 1000
	}

	return out, nil
}
