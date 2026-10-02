//go:build integration

// The tests in this file need a real PostgreSQL instance (and therefore
// Docker); they are separated behind the `integration` tag so `make test` stays
// fast. To run them: make test-integration
//
// The unit tests prove the service's DECISIONS against a fake repository. The
// tests here prove the GROUND those decisions stand on: that the migration can
// be rolled back, that the SEED DATA really loads, that the constraints are
// enforced, and that the rule "a country belongs to at most one region" holds
// under two concurrent requests too. The last can only be tried here, with real
// goroutines on real row locks.
package region_test

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/region"
	"github.com/bdrtr/gobit/internal/modules/region/models"
	"github.com/bdrtr/gobit/internal/modules/region/repository"
	"github.com/bdrtr/gobit/internal/modules/region/service"
	"github.com/bdrtr/gobit/internal/testdb"
)

const postgresImage = "postgres:16-alpine"

// moduleTables are the tables the module owns; the migration tests use this
// list.
var moduleTables = []string{"currency", "region", "country"}

// seededCountryCount is the number of alpha-2 codes officially assigned in ISO
// 3166-1.
//
// The constant is deliberate: if the seed file is accidentally truncated, or a
// row drops out while being copied, the count shows it in the test at once. A
// loose claim such as "greater than zero" could not catch that.
const seededCountryCount = 249

// seededCurrencyCount is the number of currencies the seed loads.
const seededCurrencyCount = 41

var (
	// testPool is the pool every test shares.
	testPool *db.Pool
	// testDSN is the connection string the migration calls use.
	testDSN string
)

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres brings up one Postgres container and runs every test against
// it. It is a separate function because os.Exit skips defers.
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
		fmt.Fprintf(os.Stderr, "the connection string could not be read: %v\n", err)
		return 1
	}

	cfg := db.DefaultConfig(testDSN)
	// The concurrency test runs dozens of goroutines at once; every transaction
	// holds a connection, so the pool is opened wider than the default.
	cfg.MaxConns = 24
	testPool, err = db.New(ctx, cfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "the connection pool could not be opened: %v\n", err)
		return 1
	}
	defer testPool.Close()

	if err := db.Migrate(ctx, testDSN, region.New(nil).Migrations(), region.ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "the migration could not be applied: %v\n", err)
		return 1
	}

	return m.Run()
}

// newService builds a service that runs on the real repository.
func newService(t *testing.T) *service.Service {
	t.Helper()

	return service.New(repository.New(testPool.Pool()), service.Options{})
}

// newRegion creates a region whose name is unique to the test.
func newRegion(ctx context.Context, t *testing.T, svc *service.Service, currency string) models.Region {
	t.Helper()

	created, err := svc.CreateRegion(ctx, service.CreateRegionInput{
		Name:           t.Name() + " " + currency,
		CurrencyCode:   currency,
		AutomaticTaxes: true,
		TaxRate:        2000,
	})
	require.NoError(t, err)
	return created
}

// countOf runs a single-column count query.
func countOf(ctx context.Context, t *testing.T, sql string, args ...any) int64 {
	t.Helper()

	var count int64
	require.NoError(t, testPool.Pool().QueryRow(ctx, sql, args...).Scan(&count))
	return count
}

// TestTheMigrationCanBeRolledBack verifies that the migrations can be applied
// and rolled back and that the seed loads again (plan Section 8).
//
// The rollback runs on the module's REAL state: region rows are left WHERE
// THEY ARE. The condition is deliberate: the module deletes only softly, so
// even an operator who deleted every region through the API leaves the row in
// the table, and it keeps holding the foreign key to the seeded currency.
// Sweeping the rows with raw SQL (DELETE FROM region) would set up a state the
// module's API cannot reach and take out exactly the trigger of the fault: the
// test would stay green even if the seed's down did not skip the currencies in
// use.
//
// The two states are tried SEPARATELY: a live region and a soft-deleted one.
// The second is its own case because a "deleted" region is still a row and
// holds the foreign key as tightly as a live one; one run for both would leave
// unclear which kept the down standing.
//
// Each runs in a database of its own. In the one this package shares the
// rollback would drop every other test's regions with the schema (D141).
func TestTheMigrationCanBeRolledBack(t *testing.T) {
	ctx := context.Background()

	t.Run("with a live region in place", func(t *testing.T) {
		dsn, pool := migratedDatabase(ctx, t)
		svc := service.New(repository.New(pool.Pool()), service.Options{})
		created := newRegion(ctx, t, svc, "TRY")
		_, err := svc.AddCountryToRegion(ctx, created.ID, "TR")
		require.NoError(t, err)
		require.Equal(t, int64(1), countIn(ctx, t, pool,
			`SELECT count(*) FROM region WHERE id = $1 AND deleted_at IS NULL`, created.ID),
			"the rollback has to run while a LIVE region is in place")

		rollBackAndReapply(ctx, t, dsn, pool)
	})

	t.Run("with a soft-deleted region in place", func(t *testing.T) {
		dsn, pool := migratedDatabase(ctx, t)
		svc := service.New(repository.New(pool.Pool()), service.Options{})
		created := newRegion(ctx, t, svc, "USD")
		require.NoError(t, svc.DeleteRegion(ctx, created.ID))
		require.Equal(t, int64(1), countIn(ctx, t, pool,
			`SELECT count(*) FROM region WHERE id = $1 AND deleted_at IS NOT NULL`, created.ID),
			"a soft delete LEAVES the row in the table, so the foreign key still holds")

		rollBackAndReapply(ctx, t, dsn, pool)
	})
}

// migratedDatabase is a database of the test's own with the module's
// migrations applied, and a pool on it.
func migratedDatabase(ctx context.Context, t *testing.T) (string, *db.Pool) {
	t.Helper()

	dsn := testdb.New(t, testDSN, "region_migration")
	require.NoError(t, db.Migrate(ctx, dsn, region.New(nil).Migrations(), region.ModuleName))
	pool, err := db.New(ctx, db.DefaultConfig(dsn), nil)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return dsn, pool
}

// countIn runs a count query on the given database.
func countIn(ctx context.Context, t *testing.T, pool *db.Pool, sql string, args ...any) int64 {
	t.Helper()

	var count int64
	require.NoError(t, pool.Pool().QueryRow(ctx, sql, args...).Scan(&count))

	return count
}

// rollBackAndReapply takes the module's migrations down to zero, applies them
// again and checks that the version ledger stayed clean.
//
// The claim that matters is dirty=false: a down that fails leaves
// golang-migrate's ledger "dirty", and since cmd/server migrates each module at
// every start, the module would never come up again. The table and seed counts
// show the down really did its work rather than skipping it.
func rollBackAndReapply(ctx context.Context, t *testing.T, dsn string, pool *db.Pool) {
	t.Helper()

	src := region.New(nil).Migrations()

	require.NoError(t, db.MigrateDown(ctx, dsn, src, region.ModuleName, 0))
	for _, table := range moduleTables {
		assert.False(t, testdb.TableExists(t, dsn, table), "%s must not remain after the rollback", table)
	}

	require.NoError(t, db.Migrate(ctx, dsn, src, region.ModuleName))
	for _, table := range moduleTables {
		assert.True(t, testdb.TableExists(t, dsn, table), "%s must be applied again", table)
	}

	version, dirty, err := db.Version(ctx, dsn, region.ModuleName)
	require.NoError(t, err)
	assert.False(t, dirty, "no migration may be left half-applied")
	assert.Equal(t, uint(3), version,
		"the schema (1), the seed (2) and dropping deleted_at from the reference tables (3) are separate versions")

	assert.Equal(t, int64(seededCountryCount), countIn(ctx, t, pool, `SELECT count(*) FROM country`),
		"the country seed has to be applied again")
	assert.Equal(t, int64(seededCurrencyCount), countIn(ctx, t, pool, `SELECT count(*) FROM currency`),
		"the currency seed has to be applied again")
	assert.Zero(t, countIn(ctx, t, pool, `SELECT count(*) FROM region`),
		"the schema was dropped and rebuilt, so no region may remain")
}

// TestTheSeedIsLoaded verifies that the reference data arrives with the
// migration.
//
// The seed is a precondition for the module being usable at all: a missing
// country means no cart can be opened for a customer in that country.
func TestTheSeedIsLoaded(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	assert.Equal(t, int64(seededCountryCount), countOf(ctx, t, `SELECT count(*) FROM country`))
	assert.Equal(t, int64(seededCurrencyCount), countOf(ctx, t, `SELECT count(*) FROM currency`))

	// The number of decimal digits is the reason this module exists; all three
	// classes must be in the seed.
	digits := map[string]int32{"TRY": 2, "USD": 2, "EUR": 2, "GBP": 2, "JPY": 0, "KWD": 3}
	for code, want := range digits {
		currency, err := svc.GetCurrency(ctx, code)
		require.NoError(t, err, "%s must be in the seed", code)
		assert.Equal(t, want, currency.DecimalDigits, "%s decimal digits", code)
		assert.NotEmpty(t, currency.Symbol, "%s must have a symbol", code)
		assert.NotEmpty(t, currency.Name, "%s must have a name", code)
	}

	// The factor used with integer division: 1999 minor units give a different
	// major unit depending on the currency.
	jpy, err := svc.GetCurrency(ctx, "JPY")
	require.NoError(t, err)
	try, err := svc.GetCurrency(ctx, "TRY")
	require.NoError(t, err)
	kwd, err := svc.GetCurrency(ctx, "KWD")
	require.NoError(t, err)
	assert.Equal(t, int64(1999), 1999/jpy.MinorUnitFactor())
	assert.Equal(t, int64(19), 1999/try.MinorUnitFactor())
	assert.Equal(t, int64(1), 1999/kwd.MinorUnitFactor())

	// Country names are ISO's English short names and the codes are UPPER case.
	countries, err := svc.ListCountries(ctx, service.ListCountriesInput{Limit: service.MaxLimit})
	require.NoError(t, err)
	require.NotEmpty(t, countries.Items)
	assert.Equal(t, int64(seededCountryCount), countries.Count)

	assert.Zero(t, countOf(ctx, t, `SELECT count(*) FROM country WHERE iso_2 <> upper(iso_2)`),
		"every country code must be UPPER case")
	assert.Zero(t, countOf(ctx, t, `SELECT count(*) FROM currency WHERE code <> upper(code)`),
		"every currency code must be UPPER case")
	// TR's ISO short name carries one letter outside ASCII, U+00FC. It is
	// written as an escape: the query the database receives is byte for byte
	// the same, and this file stays ASCII.
	assert.Equal(t, int64(1), countOf(ctx, t, "SELECT count(*) FROM country WHERE iso_2 = 'TR' AND name = 'T\u00fcrkiye'"))
}

// TestTheSeedCanBeAppliedAgain runs the seed file ITSELF a second time and
// verifies that it is idempotent.
//
// The file itself is read: SQL copied into the test would not see a change
// made to the file and would carry no evidence.
//
// Two claims are tried at once: running it again must not BLOW UP on a primary
// key violation (otherwise a redeploy would leave the migration dirty), and a
// value the operator corrected must not be OVERWRITTEN.
func TestTheSeedCanBeAppliedAgain(t *testing.T) {
	ctx := context.Background()

	seed, err := fs.ReadFile(region.New(nil).Migrations(), "000002_region_seed.up.sql")
	require.NoError(t, err, "the seed file must be embedded")

	_, err = testPool.Pool().Exec(ctx, `UPDATE currency SET symbol = 'X' WHERE code = 'TRY'`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, restoreErr := testPool.Pool().Exec(ctx,
			`UPDATE currency SET symbol = '₺' WHERE code = 'TRY'`)
		require.NoError(t, restoreErr)
	})

	countriesBefore := countOf(ctx, t, `SELECT count(*) FROM country`)

	_, err = testPool.Pool().Exec(ctx, string(seed))
	require.NoError(t, err, "the seed must be applicable a second time")

	assert.Equal(t, countriesBefore, countOf(ctx, t, `SELECT count(*) FROM country`),
		"the second application must not DUPLICATE rows")

	var symbol string
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT symbol FROM currency WHERE code = 'TRY'`).Scan(&symbol))
	assert.Equal(t, "X", symbol, "the value the operator corrected must not be OVERWRITTEN")
}

// TestARegionUpdateReadsUnderTheLock verifies deterministically that a partial
// update does its read under the row lock.
//
// The setup is the same as [TestCountryAssignmentReadsUnderTheLock] and
// produces a LOST UPDATE: the update starts while a rival transaction holds the
// row lock, and waits; the rival transaction changes the tax rate and commits.
// Because the read is done UNDER the lock, the waiting update reads the row's
// NEW state and changes only its own field. With an unlocked read the update
// would have read the row in its OLD state, and on writing it would have put
// the rival transaction's rate back to the old value — with no error returned.
func TestARegionUpdateReadsUnderTheLock(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	reg := newRegion(ctx, t, svc, "TRY")
	require.Equal(t, int32(2000), reg.TaxRate)

	conn, err := testPool.Pool().Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	var locked string
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT id FROM region WHERE id = $1 FOR UPDATE`, reg.ID).Scan(&locked))

	result := make(chan error, 1)
	go func() {
		newName := "Updated Under The Lock"
		_, updErr := svc.UpdateRegion(ctx, reg.ID, service.UpdateRegionInput{Name: &newName})
		result <- updErr
	}()

	requireLockWaiter(ctx, t)

	_, err = tx.Exec(ctx,
		`UPDATE region SET tax_rate = 500, updated_at = now() WHERE id = $1`, reg.ID)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	select {
	case updErr := <-result:
		require.NoError(t, updErr)
	case <-time.After(15 * time.Second):
		t.Fatal("the waiting update did not finish in time")
	}

	updated, err := svc.GetRegion(ctx, reg.ID)
	require.NoError(t, err)
	assert.Equal(t, "Updated Under The Lock", updated.Name, "the patch's field must be written")
	assert.Equal(t, int32(500), updated.TaxRate,
		"the rate the rival transaction wrote must not be OVERWRITTEN (lost update)")
}

// TestNoCrossModuleForeignKeys verifies that ALL the foreign keys in the
// module's tables go to the module's own tables again (Principle 2.2).
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

	var n int
	for rows.Next() {
		var name, src, tgt string
		require.NoError(t, rows.Scan(&name, &src, &tgt))
		assert.Contains(t, owned, tgt,
			"the %s constraint references outside the module (%s -> %s)", name, src, tgt)
		n++
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, 2, n, "the region->currency and country->region links must be in place")
}

// TestTheRegionLifecycle verifies creating, reading, partially updating and
// soft-deleting a region end to end.
func TestTheRegionLifecycle(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	// The name carries a letter outside ASCII (U+00FC), written as an escape so
	// this file stays ASCII; the value stored is the same.
	created, err := svc.CreateRegion(ctx, service.CreateRegionInput{
		Name: "T\u00fcrkiye", CurrencyCode: "try", AutomaticTaxes: true, TaxRate: 2000,
	})
	require.NoError(t, err)
	assert.Equal(t, "TRY", created.CurrencyCode, "the code must be stored in UPPER case")
	assert.False(t, created.CreatedAt.IsZero(), "created_at must come from the database")
	assert.Equal(t, "UTC", created.CreatedAt.Location().String(), "the time must be UTC")

	got, err := svc.GetRegion(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, int32(2000), got.TaxRate)

	// Partial update: only the rate changes.
	rate := int32(1000)
	updated, err := svc.UpdateRegion(ctx, created.ID, service.UpdateRegionInput{TaxRate: &rate})
	require.NoError(t, err)
	assert.Equal(t, int32(1000), updated.TaxRate)
	assert.Equal(t, "T\u00fcrkiye", updated.Name, "a field that was not given must not change")
	assert.Equal(t, "TRY", updated.CurrencyCode, "a field that was not given must not change")
	assert.True(t, updated.UpdatedAt.After(created.UpdatedAt) || updated.UpdatedAt.Equal(created.UpdatedAt))

	require.NoError(t, svc.DeleteRegion(ctx, created.ID))

	_, err = svc.GetRegion(ctx, created.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err), "a soft-deleted region must not be readable")

	page, err := svc.ListRegions(ctx, service.MaxLimit, 0)
	require.NoError(t, err)
	for _, item := range page.Items {
		assert.NotEqual(t, created.ID, item.ID, "a soft-deleted region must not appear in the list")
	}

	// The row is still there; the delete is SOFT.
	assert.Equal(t, int64(1), countOf(ctx, t,
		`SELECT count(*) FROM region WHERE id = $1 AND deleted_at IS NOT NULL`, created.ID))
}

// TestAnUnknownCurrencyIsRejected verifies that a foreign key violation is
// turned into a meaningful typed error.
//
// Without the classification this case, which the client can correct, would
// show up as a 500 and the real cause would stay only in the log.
func TestAnUnknownCurrencyIsRejected(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	_, err := svc.CreateRegion(ctx, service.CreateRegionInput{Name: "X", CurrencyCode: "XBT"})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, repository.CodeUnknownCurrency, errors.CodeOf(err))
}

// TestTheDatabaseConstraintsAreEnforced verifies that the constraints are a
// second gate even when the service's validation is bypassed.
func TestTheDatabaseConstraintsAreEnforced(t *testing.T) {
	ctx := context.Background()

	// An out-of-range tax rate (the service already filters it out; the claim
	// here is that the CHECK really was created).
	_, err := testPool.Pool().Exec(ctx,
		`INSERT INTO region (id, name, currency_code, tax_rate) VALUES ('reg_check', 'X', 'TRY', 10001)`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "region_tax_rate_check")

	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO region (id, name, currency_code) VALUES ('reg_check', '', 'TRY')`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "region_name_check")

	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO currency (code, symbol, name) VALUES ('try', '₺', 'X')`)
	require.Error(t, err, "a lower-case code must not be accepted")
	assert.Contains(t, err.Error(), "currency_code_check")

	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO currency (code, symbol, name, decimal_digits) VALUES ('ZZZ', 'Z', 'X', 5)`)
	require.Error(t, err, "more than five digits must not be accepted")
	assert.Contains(t, err.Error(), "currency_digits_check")

	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO country (iso_2, name) VALUES ('TRX', 'X')`)
	require.Error(t, err, "a three-letter country code must not be accepted")
	assert.Contains(t, err.Error(), "country_iso_2_check")
}

// TestACountryBelongsToAtMostOneRegion verifies on the real database that a
// country can belong to at most one region.
func TestACountryBelongsToAtMostOneRegion(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	first := newRegion(ctx, t, svc, "TRY")
	second := newRegion(ctx, t, svc, "USD")

	country, err := svc.AddCountryToRegion(ctx, first.ID, "cy")
	require.NoError(t, err)
	require.NotNil(t, country.RegionID)
	assert.Equal(t, first.ID, *country.RegionID)
	assert.Equal(t, "CY", country.Code)

	_, err = svc.AddCountryToRegion(ctx, second.ID, "CY")
	require.Error(t, err, "the same country must not be added to a second region")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))

	// Adding it to the same region again is idempotent.
	again, err := svc.AddCountryToRegion(ctx, first.ID, "CY")
	require.NoError(t, err)
	assert.Equal(t, first.ID, *again.RegionID)

	// A missing country and a missing region each return not found.
	_, err = svc.AddCountryToRegion(ctx, first.ID, "ZZ")
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))

	_, err = svc.AddCountryToRegion(ctx, "reg_MISSING", "MT")
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// lockWaiterCount returns how many sessions are waiting (blocked) on a lock.
func lockWaiterCount(ctx context.Context, t *testing.T) int64 {
	t.Helper()

	return countOf(ctx, t,
		`SELECT count(*) FROM pg_stat_activity
         WHERE datname = current_database()
           AND wait_event_type = 'Lock'
           AND pid <> pg_backend_pid()`)
}

// requireLockWaiter verifies that a request really is waiting on a lock.
//
// It looks at the WAIT STATE rather than sleeping: a fixed sleep would either
// wake early on a slow machine and make the test flaky, or add idle waiting to
// every run.
func requireLockWaiter(ctx context.Context, t *testing.T) {
	t.Helper()

	require.Eventually(t, func() bool {
		return lockWaiterCount(ctx, t) > 0
	}, 10*time.Second, 10*time.Millisecond, "the request should have been waiting on the row lock")
}

// TestCountryAssignmentReadsUnderTheLock verifies deterministically that the
// country row is locked AT THE MOMENT IT IS READ.
//
// THIS IS THE REAL PROOF OF THE CONCURRENCY CLAIM. The setup produces the
// losing side of the race without leaving it to timing:
//
//  1. A rival transaction locks the country row with FOR UPDATE (the country
//     belongs to no region yet).
//  2. The service tries to add the same country to another region and WAITS.
//  3. The rival transaction puts the country into the first region and commits.
//  4. The waiting request wakes up.
//
// In a correct implementation the read in step 4 is done UNDER the lock, so the
// row's CURRENT state is seen and errors.Conflict is returned. Had the read been
// unlocked, the request would have read region_id as EMPTY in step 2; by the
// time it woke its decision would already have been made, its UPDATE would
// re-evaluate the WHERE clause and succeed, and the country would SILENTLY move
// to the second region — with no error returned.
func TestCountryAssignmentReadsUnderTheLock(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	first := newRegion(ctx, t, svc, "TRY")
	second := newRegion(ctx, t, svc, "USD")

	const countryCode = "IS"

	conn, err := testPool.Pool().Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	var locked string
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT iso_2 FROM country WHERE iso_2 = $1 FOR UPDATE`, countryCode).Scan(&locked))

	result := make(chan error, 1)
	go func() {
		_, addErr := svc.AddCountryToRegion(ctx, second.ID, countryCode)
		result <- addErr
	}()

	requireLockWaiter(ctx, t)

	_, err = tx.Exec(ctx,
		`UPDATE country SET region_id = $2, updated_at = now() WHERE iso_2 = $1`, countryCode, first.ID)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	select {
	case addErr := <-result:
		require.Error(t, addErr,
			"on waking, the waiting request must see the row's CURRENT state and return a conflict")
		assert.Equal(t, errors.KindConflict, errors.KindOf(addErr))
	case <-time.After(15 * time.Second):
		t.Fatal("the waiting request did not finish in time")
	}

	resolved, err := svc.ResolveRegionForCountry(ctx, countryCode)
	require.NoError(t, err)
	assert.Equal(t, first.ID, resolved.ID, "the country must stay in the rival transaction's region")
}

// TestACountryCannotJoinARegionBeingDeleted verifies deterministically that the
// SHARED lock on the region row does its job.
//
// The region is the first step of the lock order, and deliberately so: adding
// a country must not see a region that is being deleted at that moment as
// LIVE. The setup:
//
//  1. A rival transaction locks the region row.
//  2. Adding a country starts and WAITS on the region lock.
//  3. The rival transaction soft-deletes the region and commits.
//  4. The waiting request wakes up; once the FOR SHARE lock is taken, the WHERE
//     clause (deleted_at IS NULL) is evaluated AGAIN and the row looks "absent".
//
// Had the region been read without a lock, the request would have seen it live
// in step 2, never woken to the delete, and tied the country to a DELETED
// region: the country would be locked into a dead region, could not be added to
// any other region, and ResolveRegionForCountry would return the inconsistency
// code for it for good.
func TestACountryCannotJoinARegionBeingDeleted(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	reg := newRegion(ctx, t, svc, "TRY")

	const countryCode = "FI"

	conn, err := testPool.Pool().Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	var locked string
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT id FROM region WHERE id = $1 FOR UPDATE`, reg.ID).Scan(&locked))

	result := make(chan error, 1)
	go func() {
		_, addErr := svc.AddCountryToRegion(ctx, reg.ID, countryCode)
		result <- addErr
	}()

	requireLockWaiter(ctx, t)

	_, err = tx.Exec(ctx,
		`UPDATE region SET deleted_at = now(), updated_at = now() WHERE id = $1`, reg.ID)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	select {
	case addErr := <-result:
		require.Error(t, addErr, "a country must not be added to a deleted region")
		assert.Equal(t, errors.KindNotFound, errors.KindOf(addErr))
	case <-time.After(15 * time.Second):
		t.Fatal("the waiting request did not finish in time")
	}

	assert.Zero(t, countOf(ctx, t,
		`SELECT count(*) FROM country WHERE iso_2 = $1 AND region_id IS NOT NULL`, countryCode),
		"the country must not be tied to the deleted region")
}

// TestConcurrentCountryAssignmentHasOneWinner verifies that of the requests
// trying to add the same country to different regions at the same time, ONLY
// ONE wins.
//
// This test shows that the rule holds end to end but DOES NOT PROVE THAT THE
// LOCK EXISTS: if the requests happened to run one after another, an
// implementation without the lock would pass too. The real proof of the lock is
// in [TestCountryAssignmentReadsUnderTheLock]; together the two cover both the
// rule and its mechanism.
func TestConcurrentCountryAssignmentHasOneWinner(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	const regionCount = 8
	regions := make([]models.Region, 0, regionCount)
	for i := range regionCount {
		regions = append(regions, newRegion(ctx, t, svc, []string{"TRY", "USD", "EUR", "JPY"}[i%4]))
	}

	const countryCode = "MT"
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		winners   []string
		conflicts int
		otherErrs []error
	)

	wg.Add(regionCount)
	for _, reg := range regions {
		go func(regionID string) {
			defer wg.Done()

			_, err := svc.AddCountryToRegion(ctx, regionID, countryCode)

			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				winners = append(winners, regionID)
			case errors.IsConflict(err):
				conflicts++
			default:
				otherErrs = append(otherErrs, err)
			}
		}(reg.ID)
	}
	wg.Wait()

	assert.Empty(t, otherErrs, "unexpected error: %v", otherErrs)
	require.Len(t, winners, 1, "exactly one region must win the race")
	assert.Equal(t, regionCount-1, conflicts, "every loser must get a conflict")

	// The final state in the database must match the winner.
	resolved, err := svc.ResolveRegionForCountry(ctx, countryCode)
	require.NoError(t, err)
	assert.Equal(t, winners[0], resolved.ID)
}

// TestResolveRegionForCountry verifies the resolution from country to region
// and its three failure cases on the real database.
func TestResolveRegionForCountry(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	reg := newRegion(ctx, t, svc, "EUR")

	_, err := svc.AddCountryToRegion(ctx, reg.ID, "PT")
	require.NoError(t, err)

	resolved, err := svc.ResolveRegionForCountry(ctx, "pt")
	require.NoError(t, err)
	assert.Equal(t, reg.ID, resolved.ID)
	assert.Equal(t, "EUR", resolved.CurrencyCode, "the cart takes its currency from here")

	_, err = svc.ResolveRegionForCountry(ctx, "ZZ")
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
	assert.Equal(t, repository.CodeCountryNotFound, errors.CodeOf(err))

	_, err = svc.ResolveRegionForCountry(ctx, "AQ")
	require.Error(t, err, "no region may be found for a country tied to no region")
	assert.Equal(t, service.CodeCountryUnassigned, errors.CodeOf(err))

	_, err = svc.ResolveRegionForCountry(ctx, "PRT")
	require.Error(t, err, "an alpha-3 code must not be accepted")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestDeletingARegionReleasesItsCountries verifies that the delete and the
// release of the countries happen in ONE operation.
//
// Had they not been released, the country would stay tied to a dead region,
// could not be added to any other region because of the foreign key, and no
// cart could ever be opened for a customer in that country.
func TestDeletingARegionReleasesItsCountries(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	first := newRegion(ctx, t, svc, "TRY")
	second := newRegion(ctx, t, svc, "USD")

	for _, code := range []string{"AL", "AD"} {
		_, err := svc.AddCountryToRegion(ctx, first.ID, code)
		require.NoError(t, err)
	}

	require.NoError(t, svc.DeleteRegion(ctx, first.ID))

	assert.Zero(t, countOf(ctx, t, `SELECT count(*) FROM country WHERE region_id = $1`, first.ID),
		"no country may stay tied to the deleted region")

	country, err := svc.AddCountryToRegion(ctx, second.ID, "AL")
	require.NoError(t, err, "a released country must be addable to another region")
	require.NotNil(t, country.RegionID)
	assert.Equal(t, second.ID, *country.RegionID)
}

// blockedRequestCount returns how many requests the given session is
// BLOCKING.
//
// It is [lockWaiterCount] narrowed to a known blocker, and the narrowing is
// mandatory for the one test in this file that uses it: the test below makes a
// claim about a MOMENT — "what does an observer outside see while the delete
// has not finished yet" — and if that moment is picked wrongly the test stays
// green having measured nothing. The condition "somebody in the database is
// waiting on a lock" is also met by another session's wait; in that case the
// assertion runs before the delete has even run its first statement, and of
// course it holds. pg_blocking_pids says the wait comes from OUR transaction,
// so the condition is met only once the delete has really reached its second
// statement.
func blockedRequestCount(ctx context.Context, t *testing.T, blockerPID int32) int64 {
	t.Helper()

	return countOf(ctx, t,
		`SELECT count(*) FROM pg_stat_activity
         WHERE datname = current_database()
           AND wait_event_type = 'Lock'
           AND $1 = ANY(pg_blocking_pids(pid))`, blockerPID)
}

// requireBlockedRequest verifies that the given session really is holding a
// request up.
//
// It looks at the WAIT STATE rather than sleeping; the reason is the same as
// [requireLockWaiter]'s.
func requireBlockedRequest(ctx context.Context, t *testing.T, blockerPID int32) {
	t.Helper()

	require.Eventually(t, func() bool {
		return blockedRequestCount(ctx, t, blockerPID) > 0
	}, 10*time.Second, 10*time.Millisecond, "the request should have been waiting on this session's lock")
}

// TestDeletingARegionMakesBothWritesInOneTransaction verifies deterministically
// that the delete's TWO writes become visible to the outside AT ONE MOMENT.
//
// [TestDeletingARegionReleasesItsCountries] shows that both writes HAPPEN, not
// that they happen TOGETHER. The gap was found by measuring: with the
// transaction frame of repository.DeleteRegion removed and the two writes
// turned into two separate autocommit statements, ALL of the module's
// integration tests stayed green (2026-09-06). So the frame was there but no
// test held it in place; this test closes that gap.
//
// The setup produces the intermediate state without leaving it to timing:
//
//  1. A rival transaction locks the region's ONLY country with FOR UPDATE.
//  2. The delete starts: it soft-deletes the region, then goes to release the
//     countries and WAITS on the country lock.
//  3. A THIRD read (from the pool, in its own autocommit) sees only committed
//     data.
//  4. The rival transaction lets go of the lock and the delete completes.
//
// Step 3 carries the proof: in a correct implementation the region STILL LOOKS
// LIVE, because the first write has not been committed. Had the two writes
// been in separate transactions, at that moment the region would look deleted
// while its country still looked tied to it — exactly the state the delete
// exists to prevent: a country tied to a dead region.
func TestDeletingARegionMakesBothWritesInOneTransaction(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	reg := newRegion(ctx, t, svc, "TRY")

	const countryCode = "LV"
	_, err := svc.AddCountryToRegion(ctx, reg.ID, countryCode)
	require.NoError(t, err)

	conn, err := testPool.Pool().Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	var blockerPID int32
	require.NoError(t, tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID))

	var locked string
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT iso_2 FROM country WHERE iso_2 = $1 FOR UPDATE`, countryCode).Scan(&locked))

	result := make(chan error, 1)
	go func() {
		result <- svc.DeleteRegion(ctx, reg.ID)
	}()

	requireBlockedRequest(ctx, t, blockerPID)

	assert.Equal(t, int64(1), countOf(ctx, t,
		`SELECT count(*) FROM region WHERE id = $1 AND deleted_at IS NULL`, reg.ID),
		"the region must not look deleted before the second write has finished")
	assert.Equal(t, int64(1), countOf(ctx, t,
		`SELECT count(*) FROM country WHERE iso_2 = $1 AND region_id = $2`, countryCode, reg.ID),
		"the country must not have been released yet either; there is no intermediate state")

	require.NoError(t, tx.Rollback(ctx))

	select {
	case delErr := <-result:
		require.NoError(t, delErr, "once the lock is released the delete must complete")
	case <-time.After(15 * time.Second):
		t.Fatal("the waiting delete did not finish in time")
	}

	assert.Zero(t, countOf(ctx, t,
		`SELECT count(*) FROM region WHERE id = $1 AND deleted_at IS NULL`, reg.ID),
		"once the delete has committed the region must not stay live")
	assert.Zero(t, countOf(ctx, t,
		`SELECT count(*) FROM country WHERE region_id = $1`, reg.ID),
		"the country must be released at the same moment")
}

// TestACountryIsRemovedFromARegion verifies the path that removes a country and
// the refusal of a call made with the wrong region.
func TestACountryIsRemovedFromARegion(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	first := newRegion(ctx, t, svc, "TRY")
	second := newRegion(ctx, t, svc, "USD")

	_, err := svc.AddCountryToRegion(ctx, first.ID, "BG")
	require.NoError(t, err)

	err = svc.RemoveCountryFromRegion(ctx, second.ID, "BG")
	require.Error(t, err, "another region's country must not be removable")
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
	assert.Equal(t, int64(1), countOf(ctx, t,
		`SELECT count(*) FROM country WHERE iso_2 = 'BG' AND region_id = $1`, first.ID),
		"a failed removal must not break the link")

	require.NoError(t, svc.RemoveCountryFromRegion(ctx, first.ID, "bg"))
	assert.Zero(t, countOf(ctx, t, `SELECT count(*) FROM country WHERE iso_2 = 'BG' AND region_id IS NOT NULL`))
}

// TestTheCountryListFiltersByRegion verifies the country list's region filter
// with the real query.
func TestTheCountryListFiltersByRegion(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	reg := newRegion(ctx, t, svc, "TRY")

	for _, code := range []string{"GE", "AM"} {
		_, err := svc.AddCountryToRegion(ctx, reg.ID, code)
		require.NoError(t, err)
	}

	page, err := svc.ListCountries(ctx, service.ListCountriesInput{RegionID: &reg.ID})
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	assert.Equal(t, int64(2), page.Count)
	assert.Equal(t, "AM", page.Items[0].Code, "the countries must come back ordered by code")
	assert.Equal(t, "GE", page.Items[1].Code)

	all, err := svc.ListCountries(ctx, service.ListCountriesInput{Limit: 5})
	require.NoError(t, err)
	assert.Len(t, all.Items, 5, "the page size must be applied")
	assert.Equal(t, int64(seededCountryCount), all.Count, "the total count does not depend on the page size")
}

// TestTheStorefrontRegionCarriesItsCurrencyAndCountries verifies that the
// storefront view carries the currency and the countries in a single call.
func TestTheStorefrontRegionCarriesItsCurrencyAndCountries(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	reg := newRegion(ctx, t, svc, "JPY")
	_, err := svc.AddCountryToRegion(ctx, reg.ID, "SG")
	require.NoError(t, err)

	item, err := svc.GetStoreRegion(ctx, reg.ID)
	require.NoError(t, err)
	require.NotNil(t, item.Currency)
	assert.Equal(t, "JPY", item.Currency.Code)
	assert.Equal(t, int32(0), item.Currency.DecimalDigits, "JPY has no decimals")
	assert.Equal(t, int64(1), item.Currency.MinorUnitFactor())
	require.Len(t, item.Countries, 1)
	assert.Equal(t, "SG", item.Countries[0].Code)
}

// TestTheInteropSurfaceWorksOnRealData verifies that the narrow surface between
// modules returns the expected values on the real database.
//
// This surface is the one door cart will use in Phase 5, order in Phase 6 and
// tax in Phase 7; a mismatch shows not at compile time but at resolution time
// (ADR 0001), so trying it with real data is MANDATORY.
func TestTheInteropSurfaceWorksOnRealData(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	reg := newRegion(ctx, t, svc, "KWD")
	_, err := svc.AddCountryToRegion(ctx, reg.ID, "KW")
	require.NoError(t, err)

	id, err := svc.RegionIDForCountry(ctx, "kw")
	require.NoError(t, err)
	assert.Equal(t, reg.ID, id)

	code, digits, err := svc.RegionCurrency(ctx, reg.ID)
	require.NoError(t, err)
	assert.Equal(t, "KWD", code)
	assert.Equal(t, int32(3), digits, "KWD has three decimal digits")

	rate, automatic, err := svc.RegionTax(ctx, reg.ID)
	require.NoError(t, err)
	assert.Equal(t, int32(2000), rate)
	assert.True(t, automatic)

	// The tax computed in integer arithmetic: for 19.990 KWD (19990 fils),
	// 19990 * 2000 / 10000 = 3998 fils.
	assert.Equal(t, int64(3998), 19990*int64(rate)/int64(models.MaxTaxRate))

	digits, err = svc.CurrencyDecimalDigits(ctx, "jpy")
	require.NoError(t, err)
	assert.Zero(t, digits)
}

// TestTheQueryProviderReadsInBulk verifies that the provider reads in bulk with
// the real queries and returns the records with their currency and countries.
func TestTheQueryProviderReadsInBulk(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	provider := service.NewQueryProvider(svc)

	first := newRegion(ctx, t, svc, "TRY")
	second := newRegion(ctx, t, svc, "JPY")
	_, err := svc.AddCountryToRegion(ctx, first.ID, "AZ")
	require.NoError(t, err)
	_, err = svc.AddCountryToRegion(ctx, second.ID, "TH")
	require.NoError(t, err)

	records, err := provider.FetchByIDs(ctx, []string{first.ID, second.ID}, nil)
	require.NoError(t, err)
	require.Len(t, records, 2)

	byID := map[string]query.Record{}
	for _, record := range records {
		id, ok := record[query.IDField].(string)
		require.True(t, ok)
		byID[id] = record
	}

	jp, ok := byID[second.ID]["currency"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "JPY", jp["code"])
	assert.Equal(t, int32(0), jp["decimal_digits"])

	countries, ok := byID[first.ID]["countries"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, countries, 1)
	assert.Equal(t, "AZ", countries[0]["code"])

	// Field selection: sub-records that were not asked for do not come back at
	// all.
	narrow, err := provider.FetchByIDs(ctx, []string{first.ID}, []string{"currency_code"})
	require.NoError(t, err)
	require.Len(t, narrow, 1)
	assert.NotContains(t, narrow[0], "currency")
	assert.NotContains(t, narrow[0], "countries")
	assert.Equal(t, "TRY", narrow[0]["currency_code"])
}

// TestTheModuleRegistrationResolves verifies that the names the module
// registers in the container really resolve and satisfy the expected
// interfaces.
//
// This was ADR 0001's price: there is no compile-time link between provider and
// consumer, and a mismatch shows only at resolution time. This test brings that
// moment forward.
func TestTheModuleRegistrationResolves(t *testing.T) {
	ctx := context.Background()
	c := container.New(nil)
	require.NoError(t, c.Provide("core.db", testPool))

	mod := region.New(nil)
	require.NoError(t, mod.Register(ctx, c))

	svc, err := container.Resolve[*service.Service](c, "region.service")
	require.NoError(t, err, "the service must resolve by its constant name")
	require.NotNil(t, svc)
	assert.Equal(t, "region.service", region.ServiceName,
		"if the service name changes, the consumer modules cannot find it")

	// The name is worked out BY HAND with ADR 0004's rule: the provider is
	// looked up under the name "<entity>.query". Using the constant would turn
	// the test into a tautology.
	provider, err := container.Resolve[query.Provider](c, "region"+query.ProviderSuffix)
	require.NoError(t, err, "the Query provider must resolve by its name (ADR 0004)")
	assert.Equal(t, "region", provider.Entity(),
		"the registration name's prefix must be the same as Entity()")

	// The NARROW interface a consumer module (cart, in Phase 5) will write is
	// resolved here; it matches by signature alone, WITHOUT importing region
	// (ADR 0001).
	type regionReader interface {
		RegionIDForCountry(ctx context.Context, countryCode string) (string, error)
		RegionCurrency(ctx context.Context, regionID string) (string, int32, error)
		RegionTax(ctx context.Context, regionID string) (int32, bool, error)
	}
	reader, err := container.Resolve[regionReader](c, region.ServiceName)
	require.NoError(t, err, "the service must satisfy the narrow consumer interface")

	reg := newRegion(ctx, t, svc, "TRY")
	_, err = svc.AddCountryToRegion(ctx, reg.ID, "MD")
	require.NoError(t, err)

	id, err := reader.RegionIDForCountry(ctx, "MD")
	require.NoError(t, err)
	assert.Equal(t, reg.ID, id)

	// The real proof: the core's Query layer must be able to find the provider
	// and fetch the data by the entity name alone, without knowing the module
	// at all.
	records, err := query.New(nil, c, nil).Graph(ctx, query.GraphSpec{
		Entity:  "region",
		Filters: map[string]any{"id": reg.ID},
	})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, reg.ID, records[0][query.IDField])
	assert.Equal(t, "TRY", records[0]["currency_code"])
}
