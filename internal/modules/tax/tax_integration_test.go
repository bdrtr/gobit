//go:build integration

// The tests in this file need a real PostgreSQL instance (and therefore
// Docker); they carry the `integration` tag so that `make test` stays fast.
// To run them: make test-integration
//
// The unit tests prove the service's DECISIONS against a fake repository (rate
// selection, rounding direction, overflow, error classification). The tests
// here prove the GROUND those decisions stand on: that the migration rolls back
// WITH DATA IN PLACE, that the partial unique indexes really refuse a second
// root region and a second default rate, that the composite foreign key stops a
// province-country mismatch, and that two concurrent requests cannot break the
// rule together. The last of those can only be tried here, against real
// constraints and real row locks — a fake repository cannot disagree with a
// rule it wrote itself.
package tax_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
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
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/tax"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
	"github.com/bdrtr/gobit/internal/modules/tax/repository"
	"github.com/bdrtr/gobit/internal/modules/tax/service"
)

const postgresImage = "postgres:16-alpine"

// moduleTables are the tables the module owns; the migration tests walk this
// list.
var moduleTables = []string{
	"tax_region", "tax_rate", "tax_rate_rule", "tax_class", "tax_class_member",
}

var (
	// testPool is the pool every test shares.
	testPool *db.Pool
	// testDSN is the connection string the migration calls take.
	testDSN string
	// countryCounter hands out a UNIQUE country code per test.
	//
	// It is mandatory: a country has at most one root tax region and every
	// test shares one database. Two tests using a fixed code would collide on
	// each other's constraint, and which of them actually tried the rule would
	// stop being knowable.
	countryCounter atomic.Int64
)

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres brings up a single Postgres container and runs every test on
// it. It is a separate function because os.Exit skips defers.
func runWithPostgres(m *testing.M) int {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_tax_test"),
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
		fmt.Fprintf(os.Stderr, "the connection string could not be read: %v\n", err)
		return 1
	}

	cfg := db.DefaultConfig(testDSN)
	// The concurrency tests run dozens of goroutines at once and every
	// transaction holds a connection, so the pool opens wider than the default.
	cfg.MaxConns = 24
	testPool, err = db.New(ctx, cfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "the connection pool could not be opened: %v\n", err)
		return 1
	}
	defer testPool.Close()

	if err := db.Migrate(ctx, testDSN, tax.New(nil).Migrations(), tax.ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "the migrations could not be applied: %v\n", err)
		return 1
	}

	return m.Run()
}

// newService builds a service on top of the real repository.
func newService(t *testing.T) *service.Service {
	t.Helper()

	return service.New(repository.New(testPool.Pool()), service.Options{})
}

// uniqueCountry produces a country code no other test in this run uses.
//
// The code does NOT have to be defined in ISO 3166-1: this module validates
// only the SHAPE, and the country list is the region module's data (tax cannot
// import it, ADR 0001).
func uniqueCountry(t *testing.T) string {
	t.Helper()

	n := countryCounter.Add(1)
	const letters = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	code := string(letters[(n/26)%26]) + string(letters[n%26])
	require.Len(t, code, 2)
	return code
}

// newRootRegion opens a root tax region for a unique country.
func newRootRegion(ctx context.Context, t *testing.T, svc *service.Service) models.TaxRegion {
	t.Helper()

	region, err := svc.CreateTaxRegion(ctx, service.CreateTaxRegionInput{
		CountryCode: uniqueCountry(t),
	})
	require.NoError(t, err)
	return region
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

// countOf runs a single-column counting query.
func countOf(ctx context.Context, t *testing.T, sql string, args ...any) int64 {
	t.Helper()

	var count int64
	require.NoError(t, testPool.Pool().QueryRow(ctx, sql, args...).Scan(&count))
	return count
}

// TestMigrationsRollBackWithDataInPlace proves the migration can be rolled back
// WITH DATA PRESENT (plan section 8).
//
// The rollback runs against the module's REAL state: region, rate and rule rows
// are left WHERE THEY ARE. That condition is deliberate — the module's only
// delete path is a SOFT delete, so even an operator who removes every record
// through the API leaves the rows in the tables, still holding the in-module
// foreign keys (rule -> rate -> region, province -> root). Sweeping the rows
// with raw SQL would build a precondition UNREACHABLE through the module's own
// API and would take the trigger of the fault out of the test.
//
// TestMigrationsCanReallyBeRolledBack in internal/arch runs the same round trip
// on an EMPTY schema; a data-dependent rollback failure is caught only here.
func TestMigrationsRollBackWithDataInPlace(t *testing.T) {
	ctx := context.Background()

	for _, table := range moduleTables {
		require.True(t, tableExists(ctx, t, table), "%s must exist to begin with", table)
	}

	svc := newService(t)
	root := newRootRegion(ctx, t, svc)
	province, err := svc.CreateTaxRegion(ctx, service.CreateTaxRegionInput{
		CountryCode: root.CountryCode, ProvinceCode: "34", ParentID: root.ID,
	})
	require.NoError(t, err)

	rate, err := svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: province.ID, Name: "Reduced", RateBps: 100,
	})
	require.NoError(t, err)
	_, err = svc.CreateRateRule(ctx, service.CreateRateRuleInput{
		TaxRateID: rate.ID, Reference: "product", ReferenceID: "prod_1",
	})
	require.NoError(t, err)

	// A soft-deleted record is left behind too: the row STAYS in the table and
	// holds its foreign key as tightly as a live one.
	doomed, err := svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: root.ID, Name: "Doomed", RateBps: 500,
	})
	require.NoError(t, err)
	require.NoError(t, svc.DeleteTaxRate(ctx, doomed.ID))
	require.Equal(t, int64(1), countOf(ctx, t,
		`SELECT count(*) FROM tax_rate WHERE id = $1 AND deleted_at IS NOT NULL`, doomed.ID),
		"a soft delete LEAVES the row in the table")

	src := tax.New(nil).Migrations()

	require.NoError(t, db.MigrateDown(ctx, testDSN, src, tax.ModuleName, 0),
		"down failed — which means the module can never be migrated again")
	for _, table := range moduleTables {
		assert.False(t, tableExists(ctx, t, table), "%s must not survive the rollback", table)
	}

	require.NoError(t, db.Migrate(ctx, testDSN, src, tax.ModuleName))
	for _, table := range moduleTables {
		assert.True(t, tableExists(ctx, t, table), "%s must be applied again", table)
	}

	version, dirty, err := db.Version(ctx, testDSN, tax.ModuleName)
	require.NoError(t, err)
	assert.False(t, dirty, "no migration may be left half-applied")
	// The head version is raised BY HAND when a migration joins the module. It
	// is not derived from the files: derived, it would agree with itself and
	// the sentence "the head was applied" would stop saying anything.
	assert.Equal(t, uint(4), version)
	assert.Zero(t, countOf(ctx, t, `SELECT count(*) FROM tax_region`),
		"the schema was dropped and rebuilt, so no region may remain")
}

// TestNoCrossModuleForeignKey proves Principle 2.2 on the REAL schema.
//
// internal/arch audits the same rule by scanning the SQL text; this test shows
// the constraints are really built in the database and that their targets stay
// inside the module.
func TestNoCrossModuleForeignKey(t *testing.T) {
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

	var found int
	for rows.Next() {
		var name, src, tgt string
		require.NoError(t, rows.Scan(&name, &src, &tgt))
		assert.Contains(t, owned, tgt,
			"the %s constraint points outside the module (%s -> %s)", name, src, tgt)
		found++
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, 5, found,
		"region->region (province), rate->region, rate->rate (stack), rule->rate "+
			"and membership->class must all be built")
}

// TestASecondRootRegionIsRefused proves the partial unique index works.
func TestASecondRootRegionIsRefused(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	root := newRootRegion(ctx, t, svc)

	_, err := svc.CreateTaxRegion(ctx, service.CreateTaxRegionInput{CountryCode: root.CountryCode})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
	assert.Equal(t, service.CodeRootExists, errors.CodeOf(err))

	// After a deleted root a new one must be openable; otherwise a delete would
	// leave the country permanently unconfigurable (the partial index filters
	// on deleted_at IS NULL).
	require.NoError(t, svc.DeleteTaxRegion(ctx, root.ID))
	_, err = svc.CreateTaxRegion(ctx, service.CreateTaxRegionInput{CountryCode: root.CountryCode})
	require.NoError(t, err)
}

// TestConcurrentRootRegionsLeaveOneWinner proves that two requests which pass
// the service check together still collide in the database.
//
// The service reads before it writes, and two concurrent requests can pass that
// check together; the last defense is the partial unique index, and it can only
// be tried against a real database.
func TestConcurrentRootRegionsLeaveOneWinner(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	country := uniqueCountry(t)

	const requestCount = 8
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		winners   []string
		codes     []string
		otherErrs []error
	)

	wg.Add(requestCount)
	for range requestCount {
		go func() {
			defer wg.Done()

			region, err := svc.CreateTaxRegion(ctx, service.CreateTaxRegionInput{CountryCode: country})

			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				winners = append(winners, region.ID)
			case errors.IsConflict(err):
				codes = append(codes, errors.CodeOf(err))
			default:
				otherErrs = append(otherErrs, err)
			}
		}()
	}
	wg.Wait()

	assert.Empty(t, otherErrs, "unexpected error: %v", otherErrs)
	require.Len(t, winners, 1, "exactly one request must win the race")
	assert.Len(t, codes, requestCount-1, "every loser must get a conflict")
	for _, code := range codes {
		// Whichever way a losing request falls (the read-first check or the
		// unique index) it must get the same code; for the detail see
		// TestTheLosingRootRaceGetsTheSameCode.
		assert.Equal(t, service.CodeRootExists, code)
	}
	assert.Equal(t, int64(1), countOf(ctx, t,
		`SELECT count(*) FROM tax_region WHERE country_code = $1 AND parent_id IS NULL AND deleted_at IS NULL`,
		country))
}

// TestTheLosingRootRaceGetsTheSameCode proves that a request which passes the
// read-first check gets the SAME error code when it hits the database index.
//
// The race is tied to a LOCK rather than to timing: the rival row is written in
// an open transaction and is NOT committed. The service's read cannot see it
// (read committed), but its INSERT hits it on the unique index and waits until
// that transaction ends. The losing end of the race is therefore tried for
// certain on every run; TestConcurrentRootRegionsLeaveOneWinner asserts the same
// code but cannot guarantee which way its losers fell.
func TestTheLosingRootRaceGetsTheSameCode(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	country := uniqueCountry(t)

	tx, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `INSERT INTO tax_region (id, country_code) VALUES ($1, $2)`,
		models.NewTaxRegionID(time.Now()), country)
	require.NoError(t, err)

	result := make(chan error, 1)
	go func() {
		_, createErr := svc.CreateTaxRegion(ctx, service.CreateTaxRegionInput{CountryCode: country})
		result <- createErr
	}()

	// The commit must not happen before the request is seen WAITING on the
	// index: an early commit would drop the request onto the read-first check
	// and the race path would never be tried.
	require.Eventually(t, func() bool {
		var waiting int64
		scanErr := testPool.Pool().QueryRow(ctx,
			`SELECT count(*) FROM pg_stat_activity
             WHERE wait_event_type = 'Lock' AND query ILIKE '%INSERT INTO tax_region%'`).
			Scan(&waiting)
		return scanErr == nil && waiting > 0
	}, 10*time.Second, 20*time.Millisecond,
		"the concurrent request should have waited on the unique index")

	require.NoError(t, tx.Commit(ctx))

	err = <-result
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
	assert.Equal(t, service.CodeRootExists, errors.CodeOf(err),
		"the request that loses the race must get the SAME code as the one the check stopped")
}

// TestProviderIDConstraint proves provider_id is kept trimmed and bounded in
// the database as well.
//
// The service applies the same rule first, with a readable error; this test
// shows the constraint also holds against DIRECT SQL — the application layer is
// not the last defense.
func TestProviderIDConstraint(t *testing.T) {
	ctx := context.Background()

	for name, value := range map[string]string{
		"leading space":  " local",
		"trailing space": "local ",
		"over the limit": strings.Repeat("a", 256),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := testPool.Pool().Exec(ctx,
				`INSERT INTO tax_region (id, country_code, provider_id) VALUES ($1, $2, $3)`,
				models.NewTaxRegionID(time.Now()), uniqueCountry(t), value)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "tax_region_provider_id_check")
		})
	}

	// The value at the limit and the empty value are allowed: the constraint
	// refuses only the untrimmed and the one that EXCEEDS the limit.
	_, err := testPool.Pool().Exec(ctx,
		`INSERT INTO tax_region (id, country_code, provider_id) VALUES ($1, $2, $3)`,
		models.NewTaxRegionID(time.Now()), uniqueCountry(t), strings.Repeat("a", 255))
	require.NoError(t, err)
}

// TestAProvinceInheritsTheCountrysProvider proves inheritance works over the
// REAL region query.
//
// The unit test proves the rule against a fake repository; what is proved here
// is that the chain arrives from SQL ordered from most specific to most
// general — reverse that order and the province's empty provider_id OVERWRITES
// the country's, sending the calculation to the wrong authority again.
func TestAProvinceInheritsTheCountrysProvider(t *testing.T) {
	ctx := context.Background()

	repo := repository.New(testPool.Pool())
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(service.NewLocalProvider(repo)))
	require.NoError(t, registry.Register(&fakeProvider{id: "avalara"}))
	svc := service.New(repo, service.Options{Providers: registry})

	root, err := svc.CreateTaxRegion(ctx, service.CreateTaxRegionInput{
		CountryCode: uniqueCountry(t), ProviderID: "avalara",
	})
	require.NoError(t, err)

	// The province is opened for ONE exception and its provider is left empty;
	// it has a default rate of its own, so a calculation falling to the local
	// provider would find 725.
	province, err := svc.CreateTaxRegion(ctx, service.CreateTaxRegionInput{
		CountryCode: root.CountryCode, ProvinceCode: "CA", ParentID: root.ID,
	})
	require.NoError(t, err)
	_, err = svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: province.ID, Name: "Province", RateBps: 725, IsDefault: true,
	})
	require.NoError(t, err)

	result, err := svc.CalculateTax(ctx, service.CalculateTaxInput{
		CountryCode:  root.CountryCode,
		ProvinceCode: "CA",
		Items:        []service.TaxableItem{{ID: "li_1", Amount: 10_000}},
	})
	require.NoError(t, err)

	assert.Equal(t, "avalara", result.ProviderID,
		"the province's empty provider_id must not DROP the country's external authority")
	require.Len(t, result.Items, 1)
	assert.Equal(t, int64(999), result.Items[0].TaxAmount, "the external provider must do the maths")
}

// fakeProvider is an external tax provider that returns a fixed result.
//
// It returns an amount the local calculation CANNOT produce: the amount in the
// result is the proof of who did the maths.
type fakeProvider struct {
	id string
}

// ID returns the provider's identifier.
func (p *fakeProvider) ID() string { return p.id }

// Calculate writes a fixed tax onto every line.
func (p *fakeProvider) Calculate(
	_ context.Context,
	in service.ProviderInput,
) (service.ProviderResult, error) {
	out := service.ProviderResult{
		Items:    make([]service.ProviderItemTax, 0, len(in.Items)),
		Shipping: service.ProviderItemTax{ID: service.ShippingLineID},
	}
	for i := range in.Items {
		out.Items = append(out.Items, service.ProviderItemTax{
			ID: in.Items[i].ID, RateBps: 1000, TaxAmount: 999,
		})
	}
	return out, nil
}

// TestAProvinceCannotChangeTheRootsCountry proves the composite foreign key.
//
// The service makes the same check first, with a readable error; this test
// shows the constraint also holds against DIRECT SQL.
func TestAProvinceCannotChangeTheRootsCountry(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	root := newRootRegion(ctx, t, svc)
	otherCountry := uniqueCountry(t)

	_, err := testPool.Pool().Exec(ctx,
		`INSERT INTO tax_region (id, country_code, province_code, parent_id)
         VALUES ($1, $2, 'XX', $3)`,
		models.NewTaxRegionID(root.CreatedAt), otherCountry, root.ID)
	require.Error(t, err, "a province in a country other than its root must not be writable")
	assert.Contains(t, err.Error(), "tax_region_parent_fk")
}

// TestAHalfHierarchyIsRefusedByTheDatabase proves the CHECK constraint.
func TestAHalfHierarchyIsRefusedByTheDatabase(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	root := newRootRegion(ctx, t, svc)

	t.Run("province without a parent", func(t *testing.T) {
		_, err := testPool.Pool().Exec(ctx,
			`INSERT INTO tax_region (id, country_code, province_code) VALUES ($1, $2, 'XX')`,
			models.NewTaxRegionID(root.CreatedAt), uniqueCountry(t))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "tax_region_hierarchy_check")
	})

	t.Run("root carrying a province code", func(t *testing.T) {
		_, err := testPool.Pool().Exec(ctx,
			`INSERT INTO tax_region (id, country_code, parent_id) VALUES ($1, $2, $3)`,
			models.NewTaxRegionID(root.CreatedAt), root.CountryCode, root.ID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "tax_region_hierarchy_check")
	})
}

// TestASecondDefaultRateIsRefused proves the one-default-per-region rule holds
// in the database too.
func TestASecondDefaultRateIsRefused(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	root := newRootRegion(ctx, t, svc)

	first, err := svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: root.ID, Name: "VAT", RateBps: 2000, IsDefault: true,
	})
	require.NoError(t, err)

	_, err = svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: root.ID, Name: "Second", RateBps: 1000, IsDefault: true,
	})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
	assert.Equal(t, service.CodeDefaultExists, errors.CodeOf(err))

	// Direct SQL must be refused as well: the service check is not the last
	// defense.
	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO tax_rate (id, tax_region_id, name, rate_bps, is_default)
         VALUES ($1, $2, 'Raw', 1000, TRUE)`,
		models.NewTaxRateID(root.CreatedAt), root.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tax_rate_default_uniq")

	// After the default is deleted a new one must be writable.
	require.NoError(t, svc.DeleteTaxRate(ctx, first.ID))
	_, err = svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: root.ID, Name: "New", RateBps: 1800, IsDefault: true,
	})
	require.NoError(t, err)
}

// TestRateRangeConstraint proves the rate_bps CHECK.
func TestRateRangeConstraint(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	root := newRootRegion(ctx, t, svc)

	for _, bps := range []int32{-1, 10_001} {
		_, err := testPool.Pool().Exec(ctx,
			`INSERT INTO tax_rate (id, tax_region_id, name, rate_bps) VALUES ($1, $2, 'Raw', $3)`,
			models.NewTaxRateID(root.CreatedAt), root.ID, bps)
		require.Error(t, err, "rate: %d", bps)
		assert.Contains(t, err.Error(), "tax_rate_bps_check")
	}
}

// TestADefaultRateCannotCarryARule proves the scope rule holds under the real
// lock.
func TestADefaultRateCannotCarryARule(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	root := newRootRegion(ctx, t, svc)

	fallback, err := svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: root.ID, Name: "VAT", RateBps: 2000, IsDefault: true,
	})
	require.NoError(t, err)

	_, err = svc.CreateRateRule(ctx, service.CreateRateRuleInput{
		TaxRateID: fallback.ID, Reference: "product", ReferenceID: "prod_1",
	})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))

	// The other direction: a rate that carries a rule CANNOT be made the
	// default.
	ruled, err := svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: root.ID, Name: "Reduced", RateBps: 100,
	})
	require.NoError(t, err)
	_, err = svc.CreateRateRule(ctx, service.CreateRateRuleInput{
		TaxRateID: ruled.ID, Reference: "product", ReferenceID: "prod_1",
	})
	require.NoError(t, err)

	yes := true
	_, err = svc.UpdateTaxRate(ctx, ruled.ID, service.UpdateTaxRateInput{IsDefault: &yes})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
}

// TestRuleUniqueness proves the same reference cannot be written twice.
func TestRuleUniqueness(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	root := newRootRegion(ctx, t, svc)
	rate, err := svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: root.ID, Name: "Reduced", RateBps: 100,
	})
	require.NoError(t, err)

	in := service.CreateRateRuleInput{
		TaxRateID: rate.ID, Reference: "product", ReferenceID: "prod_1",
	}
	rule, err := svc.CreateRateRule(ctx, in)
	require.NoError(t, err)

	_, err = svc.CreateRateRule(ctx, in)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))

	// A different reference KIND with the same identifier is allowed: product
	// and product type are separate namespaces.
	_, err = svc.CreateRateRule(ctx, service.CreateRateRuleInput{
		TaxRateID: rate.ID, Reference: "product_type", ReferenceID: "prod_1",
	})
	require.NoError(t, err)

	// A deleted rule's reference must be writable again.
	require.NoError(t, svc.DeleteRateRule(ctx, rule.ID))
	_, err = svc.CreateRateRule(ctx, in)
	require.NoError(t, err)
}

// TestCalculationOnTheRealSchema proves the tax calculation works end to end on
// a real database.
//
// The unit tests prove the same branches against a fake repository; what is
// proved here is that the QUERIES are right: the region chain resolving in
// order, rates and rules read in bulk, and the soft-delete filter.
func TestCalculationOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	root := newRootRegion(ctx, t, svc)
	province, err := svc.CreateTaxRegion(ctx, service.CreateTaxRegionInput{
		CountryCode: root.CountryCode, ProvinceCode: "34", ParentID: root.ID,
	})
	require.NoError(t, err)

	_, err = svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: root.ID, Name: "VAT", RateBps: 2000, IsDefault: true,
	})
	require.NoError(t, err)

	reduced, err := svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: province.ID, Name: "Book", RateBps: 100,
	})
	require.NoError(t, err)
	_, err = svc.CreateRateRule(ctx, service.CreateRateRuleInput{
		TaxRateID: reduced.ID, Reference: "product", ReferenceID: "prod_book",
	})
	require.NoError(t, err)

	in := service.CalculateTaxInput{
		CountryCode:  root.CountryCode,
		ProvinceCode: "34",
		Items: []service.TaxableItem{
			{ID: "li_book", ProductID: "prod_book", Amount: 10_000},
			{ID: "li_other", ProductID: "prod_other", Amount: 1_999},
		},
		Shipping: service.ShippingInput{OptionID: "sopt_1", Amount: 2_500},
	}

	result, err := svc.CalculateTax(ctx, in)
	require.NoError(t, err)

	require.True(t, result.RegionFound)
	assert.Equal(t, province.ID, result.RegionID, "the most specific region must be returned")
	assert.Equal(t, service.LocalProviderID, result.ProviderID)
	require.Len(t, result.Items, 2)

	assert.Equal(t, int32(100), result.Items[0].RateBps, "the province's rule must match")
	assert.Equal(t, int64(100), result.Items[0].TaxAmount)
	assert.Equal(t, int32(2000), result.Items[1].RateBps, "an unmatched line falls to the country")
	assert.Equal(t, int64(399), result.Items[1].TaxAmount, "1999 × %%20 = 399.8 -> 399 (DOWN)")
	assert.Equal(t, int64(0), result.Shipping.TaxAmount, "shipping is not taxed unless asked for")
	assert.Equal(t, int64(499), result.TaxTotal)

	// When shipping is asked for explicitly it falls to the default rate.
	in.Shipping.Taxable = true
	result, err = svc.CalculateTax(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, int64(500), result.Shipping.TaxAmount, "2500 × %%20")
	assert.Equal(t, int64(999), result.TaxTotal)

	// With the province deleted the chain falls to one link and the book falls
	// to the country's rate too.
	require.NoError(t, svc.DeleteTaxRegion(ctx, province.ID))
	result, err = svc.CalculateTax(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, root.ID, result.RegionID)
	assert.Equal(t, int32(2000), result.Items[0].RateBps,
		"a deleted province's rate must NOT enter the calculation")
}

// TestAnUnconfiguredCountryReturnsZero proves a country with no region produces
// zero rather than an error on the real database as well.
func TestAnUnconfiguredCountryReturnsZero(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	result, err := svc.CalculateTax(ctx, service.CalculateTaxInput{
		CountryCode: uniqueCountry(t),
		Items:       []service.TaxableItem{{ID: "li_1", Amount: 10_000}},
	})
	require.NoError(t, err)
	assert.False(t, result.RegionFound)
	assert.Equal(t, int64(0), result.TaxTotal)
}

// TestDeletingARegionCoversItsTree proves the delete REALLY covers sub-regions,
// rates and rules.
func TestDeletingARegionCoversItsTree(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	root := newRootRegion(ctx, t, svc)
	province, err := svc.CreateTaxRegion(ctx, service.CreateTaxRegionInput{
		CountryCode: root.CountryCode, ProvinceCode: "35", ParentID: root.ID,
	})
	require.NoError(t, err)
	rate, err := svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: province.ID, Name: "Reduced", RateBps: 100,
	})
	require.NoError(t, err)
	_, err = svc.CreateRateRule(ctx, service.CreateRateRuleInput{
		TaxRateID: rate.ID, Reference: "product", ReferenceID: "prod_1",
	})
	require.NoError(t, err)

	require.NoError(t, svc.DeleteTaxRegion(ctx, root.ID))

	assert.Zero(t, countOf(ctx, t,
		`SELECT count(*) FROM tax_region WHERE country_code = $1 AND deleted_at IS NULL`,
		root.CountryCode), "root and province must be deleted together")
	assert.Zero(t, countOf(ctx, t,
		`SELECT count(*) FROM tax_rate WHERE id = $1 AND deleted_at IS NULL`, rate.ID),
		"the sub-region's rate must be deleted too")
	assert.Zero(t, countOf(ctx, t,
		`SELECT count(*) FROM tax_rate_rule WHERE tax_rate_id = $1 AND deleted_at IS NULL`, rate.ID),
		"the rate's rules must be deleted too")

	// The deleted rows STAY in the table: a soft delete does not destroy a
	// record.
	assert.Equal(t, int64(2), countOf(ctx, t,
		`SELECT count(*) FROM tax_region WHERE country_code = $1`, root.CountryCode))
}

// TestTheInteropSurfaceWorksOnRealData proves the cross-module surface against
// a real database.
func TestTheInteropSurfaceWorksOnRealData(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	interop := service.NewInterop(svc)

	root := newRootRegion(ctx, t, svc)
	_, err := svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: root.ID, Name: "VAT", RateBps: 1800, IsDefault: true,
	})
	require.NoError(t, err)

	rate, found, err := interop.RateForCountry(ctx, root.CountryCode)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, int32(1800), rate)

	raw, err := interop.CalculateTaxJSON(ctx, []byte(
		`{"country_code":"`+root.CountryCode+`","items":[{"id":"li_1","amount":10000}]}`))
	require.NoError(t, err)
	assert.JSONEq(t,
		`{"region_id":"`+root.ID+`","region_found":true,"provider_id":"local",
		  "prices_include_tax":false,"tax_total":1800,
		  "items":[{"id":"li_1","rate_id":"`+defaultRateID(ctx, t, root.ID)+`","rate_bps":1800,
		            "taxable_amount":10000,"tax_amount":1800}],
		  "shipping":{"id":"_shipping","rate_id":"","rate_bps":0,"taxable_amount":0,"tax_amount":0}}`,
		string(raw))
}

// defaultRateID returns the identifier of a region's default rate.
func defaultRateID(ctx context.Context, t *testing.T, regionID string) string {
	t.Helper()

	var id string
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT id FROM tax_rate WHERE tax_region_id = $1 AND is_default AND deleted_at IS NULL`,
		regionID).Scan(&id))
	return id
}

// TestTheModuleRegistrationResolves proves the names the module registers in
// the container really resolve and satisfy the expected interfaces.
//
// This was the price of ADR 0001: there is no compile-time bond between
// provider and consumer, so a mismatch shows up only at resolution time. This
// test brings that moment forward.
func TestTheModuleRegistrationResolves(t *testing.T) {
	ctx := context.Background()
	c := container.New(nil)
	require.NoError(t, c.Provide("core.db", testPool))

	mod := tax.New(nil)
	require.NoError(t, mod.Register(ctx, c))

	svc, err := container.Resolve[*service.Service](c, "tax.service")
	require.NoError(t, err, "the service must resolve under its fixed name")
	require.NotNil(t, svc)
	assert.Equal(t, "tax.service", tax.ServiceName,
		"if the service name changes, consumer modules cannot find it")

	// The NARROW interface the cart workflow (internal/workflows/cart) will
	// write is resolved here; it matches by signature alone, without importing
	// tax (ADR 0001/0006). json.RawMessage must be used EXACTLY: it shares an
	// underlying type with []byte but it is a named type, and the container's
	// type check looks for signature EQUALITY. Writing "[]byte" on the consumer
	// side means a mismatch error at resolution time.
	type taxCalculator interface {
		CalculateTaxJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error)
		RateForCountry(ctx context.Context, countryCode string) (int32, bool, error)
	}
	calculator, err := container.Resolve[taxCalculator](c, tax.InteropName)
	require.NoError(t, err, "the narrow consumer interface must satisfy the interop surface")

	registry, err := container.Resolve[*service.ProviderRegistry](c, tax.ProvidersName)
	require.NoError(t, err, "the provider registry must resolve under its name")
	assert.Equal(t, []string{service.LocalProviderID}, registry.IDs())

	// The name is computed BY HAND from ADR 0004's rule: a provider is looked
	// up as "<entity>.query". Using the constant would turn the test into a
	// tautology.
	provider, err := container.Resolve[query.Provider](c, "tax_region"+query.ProviderSuffix)
	require.NoError(t, err, "the Query provider must resolve under its name (ADR 0004)")
	assert.Equal(t, "tax_region", provider.Entity(),
		"the prefix of the registered name must equal Entity()")

	root := newRootRegion(ctx, t, svc)
	_, err = svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: root.ID, Name: "VAT", RateBps: 2000, IsDefault: true,
	})
	require.NoError(t, err)

	rate, found, err := calculator.RateForCountry(ctx, root.CountryCode)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, int32(2000), rate)

	// The real proof: the core Query layer must find the provider by entity
	// name alone, knowing nothing about the module, and pull the data.
	records, err := query.New(nil, c, nil).Graph(ctx, query.GraphSpec{
		Entity:  "tax_region",
		Filters: map[string]any{"id": root.ID},
	})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, root.ID, records[0][query.IDField])
	assert.Equal(t, root.CountryCode, records[0]["country_code"])

	rates, ok := records[0]["rates"].([]map[string]any)
	require.True(t, ok, "the rates must come back with the record: %#v", records[0]["rates"])
	require.Len(t, rates, 1)
	assert.Equal(t, int32(2000), rates[0]["rate_bps"])
}

// lockWaiterCount returns how many requests are waiting on a row lock.
func lockWaiterCount(ctx context.Context, t *testing.T) int64 {
	t.Helper()

	return countOf(ctx, t,
		`SELECT count(*) FROM pg_stat_activity
         WHERE datname = current_database()
           AND wait_event_type = 'Lock'
           AND pid <> pg_backend_pid()`)
}

// requireLockWaiter proves a request is really waiting on a lock.
//
// It watches the WAIT STATE rather than sleeping: a fixed sleep would either
// wake early on a slow machine and make the test flaky, or add idle waiting to
// every run.
func requireLockWaiter(ctx context.Context, t *testing.T) {
	t.Helper()

	require.Eventually(t, func() bool {
		return lockWaiterCount(ctx, t) > 0
	}, 10*time.Second, 10*time.Millisecond, "the request should have waited on a row lock")
}

// lockingTx opens a transaction that holds an EXCLUSIVE lock on the given
// region row and returns it; the caller either commits it or rolls it back with
// the returned release.
func lockingTx(
	ctx context.Context, t *testing.T, regionID string,
) (tx pgx.Tx, release func()) {
	t.Helper()

	conn, err := testPool.Pool().Acquire(ctx)
	require.NoError(t, err)

	tx, err = conn.Begin(ctx)
	if err != nil {
		conn.Release()
		require.NoError(t, err)
	}

	var locked string
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT id FROM tax_region WHERE id = $1 FOR UPDATE`, regionID).Scan(&locked))

	return tx, func() {
		_ = tx.Rollback(ctx)
		conn.Release()
	}
}

// TestATransactionRollsBackBothWrites proves two SEPARATE repository calls can
// join one transaction and are rolled back together.
//
// THIS IS THE REAL PROOF OF D6. While the repository transaction lived INSIDE
// that package, behind a private helper taking only `func(q *taxdb.Queries)
// error`, this test COULD NOT BE WRITTEN: only the repository could produce the
// handle, it could not hand it out, and two repository calls necessarily ran in
// two separate transactions. The transaction now travels in the context and the
// service opens the frame.
//
// The test shows three things at once, and all three are needed:
//
//   - Two different repository methods (writing a region, writing a rate) run
//     in the SAME transaction.
//   - A read INSIDE the transaction SEES writes that are not committed yet —
//     that is, the context really carries the transaction; on two separate
//     connections the read would see neither and the test would still look
//     "green".
//   - When an error is returned BOTH rows are rolled back; no trace is left in
//     the tables.
func TestATransactionRollsBackBothWrites(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	repo := repository.New(testPool.Pool())

	root := newRootRegion(ctx, t, svc)

	now := time.Now().UTC()
	provinceCode := "77"
	province := models.TaxRegion{
		ID:           models.NewTaxRegionID(now),
		CountryCode:  root.CountryCode,
		ProvinceCode: &provinceCode,
		ParentID:     &root.ID,
	}
	rate := models.TaxRate{
		ID:          models.NewTaxRateID(now),
		TaxRegionID: province.ID,
		Name:        "VAT",
		RateBps:     2000,
		IsDefault:   true,
	}

	const deliberateCode = "deliberate_failure"
	err := repo.WithTx(ctx, func(ctx context.Context) error {
		if _, txErr := repo.CreateTaxRegion(ctx, province, now); txErr != nil {
			return txErr
		}
		// The rate points at the province written in the same transaction: the
		// foreign key holds only if the two are in ONE transaction; in separate
		// ones the second write could not reference an uncommitted row.
		if _, txErr := repo.CreateTaxRate(ctx, rate, now); txErr != nil {
			return txErr
		}

		read, txErr := repo.GetTaxRegion(ctx, province.ID)
		if txErr != nil {
			return txErr
		}
		require.Equal(t, province.ID, read.ID,
			"a read inside the transaction must see the row that transaction wrote")

		return errors.Internal(deliberateCode, "a deliberate error, to roll the transaction back")
	})

	require.Error(t, err)
	assert.Equal(t, deliberateCode, errors.CodeOf(err), "the error must pass upward as it is")

	assert.Zero(t, countOf(ctx, t,
		`SELECT count(*) FROM tax_region WHERE id = $1`, province.ID),
		"the first write must be rolled back")
	assert.Zero(t, countOf(ctx, t,
		`SELECT count(*) FROM tax_rate WHERE id = $1`, rate.ID),
		"the second write must be rolled back")
}

// TestWithoutATransactionTwoWritesLeaveHalfState shows the claim above HAS
// TEETH.
//
// Without this control test [TestATransactionRollsBackBothWrites] could not
// tell "the rollback works" from "the write never happens". Here the same two
// writes are made WITH NO TRANSACTION FRAME; the second hits a database
// constraint and THE FIRST STAYS WHERE IT IS. That is what the service did
// before the frame was added.
func TestWithoutATransactionTwoWritesLeaveHalfState(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	repo := repository.New(testPool.Pool())

	root := newRootRegion(ctx, t, svc)

	now := time.Now().UTC()
	provinceCode := "78"
	province := models.TaxRegion{
		ID:           models.NewTaxRegionID(now),
		CountryCode:  root.CountryCode,
		ProvinceCode: &provinceCode,
		ParentID:     &root.ID,
	}
	_, err := repo.CreateTaxRegion(ctx, province, now)
	require.NoError(t, err)

	// The second write hits tax_rate_bps_check: a rate cannot exceed 100%.
	_, err = repo.CreateTaxRate(ctx, models.TaxRate{
		ID:          models.NewTaxRateID(now),
		TaxRegionID: province.ID,
		Name:        "Invalid",
		RateBps:     models.MaxRateBps + 1,
	}, now)
	require.Error(t, err)

	assert.Equal(t, int64(1), countOf(ctx, t,
		`SELECT count(*) FROM tax_region WHERE id = $1`, province.ID),
		"a write outside a transaction is not rolled back; half state is exactly this")
}

// TestTheLockCannotBeTakenOutsideATransaction forbids using the lock without a
// transaction.
//
// A FOR SHARE lock is released when its transaction ends: a lock taken outside
// one protects nothing while it is BELIEVED to protect something. Falling
// silently back to an unlocked read would close the rule the two tests below
// guard, and nobody would notice.
func TestTheLockCannotBeTakenOutsideATransaction(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	repo := repository.New(testPool.Pool())

	root := newRootRegion(ctx, t, svc)

	_, err := repo.LockTaxRegion(ctx, root.ID)
	require.Error(t, err, "the lock must not be available outside a transaction")
	assert.Equal(t, repository.CodeTxRequired, errors.CodeOf(err))
	assert.Equal(t, errors.KindInternal, errors.KindOf(err),
		"this is a programming fault, not client input")

	// The same call works INSIDE a transaction.
	require.NoError(t, repo.WithTx(ctx, func(ctx context.Context) error {
		locked, txErr := repo.LockTaxRegion(ctx, root.ID)
		if txErr != nil {
			return txErr
		}
		assert.Equal(t, root.ID, locked.ID)
		return nil
	}))
}

// TestAProvinceCannotJoinARootBeingDeleted proves deterministically that the
// parent check happens in the SAME transaction as the write and UNDER the lock.
//
// The setup produces the losing side of the race without leaving it to timing:
//
//  1. A rival transaction takes an EXCLUSIVE lock on the root region row.
//  2. The province insert starts and WAITS on the root's lock.
//  3. The rival transaction soft-deletes the root and commits.
//  4. The waiting request wakes; after the FOR SHARE lock is taken the WHERE
//     clause (deleted_at IS NULL) is re-evaluated and the row looks "gone".
//
// Had the check been unlocked and in a separate transaction — the state before
// the frame was added — the request would read the root LIVE at step 2, never
// wait, and attach the province to a DELETED root. The foreign key does not
// catch this: the delete is soft and the root row stays in place. The result is
// not merely an orphan row — ResolveTaxRegions matches the province row ON ITS
// OWN, so every cart in that province would keep being taxed from a region the
// operator believes they deleted; even after a new root is opened for the
// country, because the chain walks from most specific to most general and the
// province comes first.
func TestAProvinceCannotJoinARootBeingDeleted(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	root := newRootRegion(ctx, t, svc)

	tx, release := lockingTx(ctx, t, root.ID)
	defer release()

	result := make(chan error, 1)
	go func() {
		_, err := svc.CreateTaxRegion(ctx, service.CreateTaxRegionInput{
			CountryCode: root.CountryCode, ProvinceCode: "34", ParentID: root.ID,
		})
		result <- err
	}()

	requireLockWaiter(ctx, t)

	_, err := tx.Exec(ctx,
		`UPDATE tax_region SET deleted_at = now(), updated_at = now() WHERE id = $1`, root.ID)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	select {
	case createErr := <-result:
		require.Error(t, createErr, "a province must not join a deleted root")
		assert.Equal(t, errors.KindNotFound, errors.KindOf(createErr))
	case <-time.After(15 * time.Second):
		t.Fatal("the waiting request did not finish in time")
	}

	assert.Zero(t, countOf(ctx, t,
		`SELECT count(*) FROM tax_region WHERE parent_id = $1`, root.ID),
		"the province row must never be written")
}

// TestARateCannotJoinARegionBeingDeleted proves the rate insert path takes the
// same protection.
//
// The setup is the same as [TestAProvinceCannotJoinARootBeingDeleted]; what it
// proves is different. The godoc on repository.DeleteTaxRegion said the lock
// "stops the race with a concurrent rate insert into the same region", because
// "the rate insert reads the region under a shared lock too". IT WAS MEASURED:
// there was not a single FOR SHARE query in the module, and the rate insert read
// the region unlocked and in a SEPARATE transaction. The sentence is true today,
// and this test binds it.
func TestARateCannotJoinARegionBeingDeleted(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	root := newRootRegion(ctx, t, svc)

	tx, release := lockingTx(ctx, t, root.ID)
	defer release()

	result := make(chan error, 1)
	go func() {
		_, err := svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
			TaxRegionID: root.ID, Name: "VAT", RateBps: 2000, IsDefault: true,
		})
		result <- err
	}()

	requireLockWaiter(ctx, t)

	_, err := tx.Exec(ctx,
		`UPDATE tax_region SET deleted_at = now(), updated_at = now() WHERE id = $1`, root.ID)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	select {
	case createErr := <-result:
		require.Error(t, createErr, "a rate must not join a deleted region")
		assert.Equal(t, errors.KindNotFound, errors.KindOf(createErr))
	case <-time.After(15 * time.Second):
		t.Fatal("the waiting request did not finish in time")
	}

	assert.Zero(t, countOf(ctx, t,
		`SELECT count(*) FROM tax_rate WHERE tax_region_id = $1`, root.ID),
		"the rate row must never be written")
}
