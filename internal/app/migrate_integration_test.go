//go:build integration

// This file needs a real PostgreSQL and is only compiled with
// `-tags=integration` (`make test-integration`), so `make test` stays fast and
// Docker-free.
package app

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/internal/modules/cart"
	"github.com/bdrtr/gobit/internal/modules/region"
)

// postgresImage is the PostgreSQL image these tests run against; it matches the
// one internal/e2e and internal/smoke use, because a migration's behavior must
// not differ between runs.
const postgresImage = "postgres:16-alpine"

// migrateDSN starts a PostgreSQL for the calling test and returns its address.
//
// One container PER TEST rather than one shared: every scenario here asserts on
// a version LEDGER, and a ledger another scenario rolled back or dirtied is not
// a fixture, it is a race. The container is the cheapest boundary that gives
// each test its own.
func migrateDSN(t *testing.T) string {
	t.Helper()

	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_migrate"),
		tcpostgres.WithUsername("gobit"),
		tcpostgres.WithPassword("gobit"),
		tcpostgres.BasicWaitStrategies(),
	)
	t.Cleanup(func() {
		if termErr := testcontainers.TerminateContainer(ctr); termErr != nil {
			t.Logf("the postgres container could not be stopped: %v", termErr)
		}
	})
	require.NoError(t, err, "the postgres container could not be started")

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	return dsn
}

// cartSources is the one-owner source list the rollback scenarios work on.
//
// The real collection ([migrationSources]) is exercised by the unit tests; what
// these scenarios need is a schema they can destroy, and cart's is a real one
// taken from the shipped module rather than a fixture written for the test.
func cartSources() []migrationSource {
	mod := cart.New()

	return []migrationSource{{owner: mod.Name(), src: mod.Migrations()}}
}

// twoOwnerSources is region + cart, and both properties matter.
//
// region has TWO migrations, which is what makes a -steps value other than 1
// observable: with a single-migration owner, "roll back 1" and "roll back N"
// land on the same ledger row and the flag can be ignored without any test
// noticing (measured — passing the default instead of the flag survived the
// whole suite). And having two OWNERS is what makes "roll back the one that was
// named" observable: with a one-element list, picking the first element is
// indistinguishable from matching by name.
func twoOwnerSources() []migrationSource {
	regionMod := region.New(slog.New(slog.DiscardHandler))
	cartMod := cart.New()

	return []migrationSource{
		{owner: regionMod.Name(), src: regionMod.Migrations()},
		{owner: cartMod.Name(), src: cartMod.Migrations()},
	}
}

// tableExists reports whether a table is present in the public schema.
func tableExists(t *testing.T, dsn, name string) bool {
	t.Helper()

	pool, err := db.New(t.Context(), db.DefaultConfig(dsn), nil)
	require.NoError(t, err)
	defer pool.Close()

	var exists bool
	require.NoError(t, pool.Pool().QueryRow(t.Context(),
		"SELECT EXISTS (SELECT 1 FROM information_schema.tables "+
			"WHERE table_schema = 'public' AND table_name = $1)", name).Scan(&exists))

	return exists
}

// TestStatusReportsEveryOwnerOnAFreshDatabase is the first thing an operator
// does, against the state they are most likely to do it in.
//
// It also NAILS DOWN the side effect the command's own godoc admits: reading a
// version through golang-migrate's driver CREATES the missing version table.
// The assertion is here rather than in the prose alone, because a documented
// side effect that stops happening (or starts happening for more tables) is a
// documentation rot this repository pays for repeatedly.
func TestStatusReportsEveryOwnerOnAFreshDatabase(t *testing.T) {
	dsn := migrateDSN(t)

	require.False(t, tableExists(t, dsn, "cart"+migrationsTableSuffix),
		"the fresh database already carries a version table; the measurement below "+
			"would prove nothing")

	sources, err := migrationSources(t.Context(), migrateConfig(), Options{})
	require.NoError(t, err)

	var out bytes.Buffer
	require.NoError(t, migrateStatus(t.Context(), &out, dsn, sources),
		"a fresh database is not an error state")

	report := out.String()
	for _, source := range sources {
		assert.Contains(t, report, source.owner, "the report omits the owner %q", source.owner)
	}
	assert.Contains(t, report, stateNone,
		"every owner is at version 0 here; the report must say so in words, not only as a 0")
	assert.NotContains(t, report, stateDirty)

	assert.True(t, tableExists(t, dsn, "cart"+migrationsTableSuffix),
		"the documented side effect is gone: the status read no longer creates the version "+
			"table. Either the footer that warns operators about it is now a lie, or the "+
			"version is being read some other way")
}

// TestStatusReportsTheVersionAfterAMigration checks the number is read, not
// assumed.
func TestStatusReportsTheVersionAfterAMigration(t *testing.T) {
	dsn := migrateDSN(t)
	sources := cartSources()

	require.NoError(t, db.Migrate(t.Context(), dsn, sources[0].src, sources[0].owner))

	version, dirty, err := db.Version(t.Context(), dsn, sources[0].owner)
	require.NoError(t, err)
	require.False(t, dirty)
	require.Positive(t, version)

	var out bytes.Buffer
	require.NoError(t, migrateStatus(t.Context(), &out, dsn, sources))

	assert.Regexp(t, `cart\s+`+versionPattern(version)+`\s+`+stateApplied, out.String(),
		"the table must carry the version the ledger actually holds")
}

// versionPattern renders a version for a regular expression.
func versionPattern(version uint) string {
	return strings.TrimSpace(ownerState{version: version}.versionText())
}

// TestDownWithoutConfirmationChangesNOTHING is the guard's whole point,
// measured on the ledger rather than on the message.
//
// Asserting the refusal text would prove only that a sentence was printed. What
// has to be true is that the DATABASE is untouched, so the version is read back
// afterwards through the same call the command uses.
func TestDownWithoutConfirmationChangesNOTHING(t *testing.T) {
	dsn := migrateDSN(t)
	sources := cartSources()

	require.NoError(t, db.Migrate(t.Context(), dsn, sources[0].src, sources[0].owner))
	before, err := readOwnerState(t.Context(), dsn, "cart")
	require.NoError(t, err)
	require.Positive(t, before.version)

	var out bytes.Buffer
	err = migrateDown(t.Context(), &out, dsn, sources, []string{"cart"}, "dev")

	require.Error(t, err, "an unconfirmed rollback must not report success")
	assert.Contains(t, err.Error(), "-"+flagConfirm+" cart",
		"the error must carry the confirmation that would authorize it")
	assert.Contains(t, out.String(), "REFUSED")

	after, err := readOwnerState(t.Context(), dsn, "cart")
	require.NoError(t, err)
	assert.Equal(t, before, after,
		"the ledger MOVED without a confirmation; the guard is decoration")
	assert.True(t, tableExists(t, dsn, "carts"),
		"the schema was rolled back without a confirmation")
}

// TestAConfirmationForAnotherOwnerIsNotAConfirmation is the case a bare -yes
// flag could not catch.
//
// It is the shape of the mistake the confirmation was designed against: a
// command line copied out of a runbook, its owner changed in one place and not
// the other. The flag matching the WRONG owner has to be as inert as no flag at
// all.
func TestAConfirmationForAnotherOwnerIsNotAConfirmation(t *testing.T) {
	dsn := migrateDSN(t)
	sources := cartSources()

	require.NoError(t, db.Migrate(t.Context(), dsn, sources[0].src, sources[0].owner))
	before, err := readOwnerState(t.Context(), dsn, "cart")
	require.NoError(t, err)

	var out bytes.Buffer
	err = migrateDown(t.Context(), &out, dsn, sources,
		[]string{"cart", "-" + flagConfirm, "order"}, "dev")

	require.Error(t, err)

	after, err := readOwnerState(t.Context(), dsn, "cart")
	require.NoError(t, err)
	assert.Equal(t, before, after,
		"a confirmation naming ANOTHER owner authorized this one's rollback")
}

// TestAConfirmedRollbackMovesTheLedgerAndReportsWhatItReads is the working path.
//
// The closing message is checked against the version read BACK out of the
// database, not against the step count that was asked for: golang-migrate may
// legitimately move fewer steps than requested, and a report built from the
// request would be a number the operator trusts and the schema does not have.
func TestAConfirmedRollbackMovesTheLedgerAndReportsWhatItReads(t *testing.T) {
	dsn := migrateDSN(t)
	sources := cartSources()

	require.NoError(t, db.Migrate(t.Context(), dsn, sources[0].src, sources[0].owner))
	before, err := readOwnerState(t.Context(), dsn, "cart")
	require.NoError(t, err)
	require.Positive(t, before.version, "there must be something to roll back")

	var out bytes.Buffer
	require.NoError(t, migrateDown(t.Context(), &out, dsn, sources,
		[]string{"cart", "-" + flagSteps, "1", "-" + flagConfirm, "cart"}, "dev"))

	after, err := readOwnerState(t.Context(), dsn, "cart")
	require.NoError(t, err)
	assert.Less(t, after.version, before.version, "the confirmed rollback did not move the ledger")
	assert.False(t, after.dirty)

	assert.Contains(t, out.String(), "is now at version "+after.versionText(),
		"the closing line must report the version the LEDGER holds; it says %q", out.String())
}

// TestStepsFlagReachesTheRollback proves the -steps value is the one that runs.
//
// The knob could exist end to end — parsed, validated, printed in the receipt —
// and still change nothing, because the receipt is written from the FLAG while
// the rollback could be called with a constant. This repository has paid for
// that exact shape twice (the pool's MaxConns, the idempotency budget): the
// operator reads the number they asked for while a different number is in
// force. It needs an owner with more than one migration to be visible at all,
// which is why region is used here rather than cart.
func TestStepsFlagReachesTheRollback(t *testing.T) {
	dsn := migrateDSN(t)
	sources := twoOwnerSources()

	require.NoError(t, db.Migrate(t.Context(), dsn, sources[0].src, sources[0].owner))
	before, err := readOwnerState(t.Context(), dsn, "region")
	require.NoError(t, err)
	require.Equal(t, uint(2), before.version, "region must have two migrations for this to prove anything")

	var out bytes.Buffer
	require.NoError(t, migrateDown(t.Context(), &out, dsn, sources,
		[]string{"region", "-" + flagSteps, "2", "-" + flagConfirm, "region"}, "dev"))

	after, err := readOwnerState(t.Context(), dsn, "region")
	require.NoError(t, err)
	assert.Equal(t, uint(0), after.version,
		"two steps back from version 2 is zero; a run that quietly used the default would stop at 1")
}

// TestDownRollsBackTheOwnerThatWasNAMED proves the named owner is the one that
// moves, and that nobody else does.
//
// The catastrophic case this repository names is a source applied under the
// wrong owner. With a single-element source list, "find the named owner" and
// "take the first element" are the same code path, so the whole matching step
// is untested by construction — measured: returning sources[0] unconditionally
// passed every shipped scenario, while dropping ANOTHER module's schema and
// handing the operator a clean receipt for the one they asked about.
func TestDownRollsBackTheOwnerThatWasNAMED(t *testing.T) {
	dsn := migrateDSN(t)
	sources := twoOwnerSources()

	for _, src := range sources {
		require.NoError(t, db.Migrate(t.Context(), dsn, src.src, src.owner))
	}
	require.True(t, tableExists(t, dsn, "region"), "region's schema must be up before we roll cart back")

	var out bytes.Buffer
	require.NoError(t, migrateDown(t.Context(), &out, dsn, sources,
		[]string{"cart", "-" + flagSteps, "1", "-" + flagConfirm, "cart"}, "dev"))

	regionState, err := readOwnerState(t.Context(), dsn, "region")
	require.NoError(t, err)
	assert.Equal(t, uint(2), regionState.version, "the owner that was NOT named must not move")
	assert.True(t, tableExists(t, dsn, "region"),
		"another owner's schema was dropped by a rollback naming cart; receipt was %q", out.String())

	cartState, err := readOwnerState(t.Context(), dsn, "cart")
	require.NoError(t, err)
	assert.Equal(t, uint(0), cartState.version, "the owner that WAS named must have moved")
}

// TestRollingBackToZeroLeavesNothingToRollBack covers the far end.
//
// Two things are proved at once: the whole schema really goes (the module's
// table is gone, not merely its ledger entry), and a second rollback over an
// empty ledger is reported as the no-op it is rather than as a failure — the
// outcome db.MigrateDown's godoc calls normal.
func TestRollingBackToZeroLeavesNothingToRollBack(t *testing.T) {
	dsn := migrateDSN(t)
	sources := cartSources()

	require.NoError(t, db.Migrate(t.Context(), dsn, sources[0].src, sources[0].owner))
	before, err := readOwnerState(t.Context(), dsn, "cart")
	require.NoError(t, err)
	require.True(t, tableExists(t, dsn, "carts"))

	var out bytes.Buffer
	require.NoError(t, migrateDown(t.Context(), &out, dsn, sources,
		[]string{"cart", "-" + flagSteps, versionPattern(before.version), "-" + flagConfirm, "cart"}, "dev"))

	after, err := readOwnerState(t.Context(), dsn, "cart")
	require.NoError(t, err)
	assert.Zero(t, after.version)
	assert.False(t, tableExists(t, dsn, "carts"),
		"the ledger went to 0 but the module's table is still there; the rollback moved the "+
			"version without undoing the schema")

	out.Reset()
	require.NoError(t, migrateDown(t.Context(), &out, dsn, sources,
		[]string{"cart", "-" + flagConfirm, "cart"}, "dev"),
		"a rollback with nothing to roll back is the documented normal outcome")
	assert.Contains(t, out.String(), "nothing to roll back")
}

// TestADirtyLedgerIsRefusedEvenWithAConfirmation is the decision this surface
// had to make, proved on a ledger that is really dirty.
//
// # Why it is refused rather than attempted
//
// Dirty means some of the current version's .up.sql ran and some did not. The
// matching .down.sql was written for the state where ALL of it ran, so running
// it is a guess the command cannot check. The refusal is unconditional: there
// is no confirmation an operator can type that tells the command what the
// half-applied schema contains.
//
// # Why the check is ours and not golang-migrate's
//
// It was MEASURED that golang-migrate refuses a dirty ledger too — the raw
// db.MigrateDown call below returns an error. Our check is still load-bearing
// for two reasons the assertions pin: it fires BEFORE anything is attempted, so
// the failure cannot be confused with one that started; and it names the table
// an operator has to repair, which the driver's own error does not.
func TestADirtyLedgerIsRefusedEvenWithAConfirmation(t *testing.T) {
	dsn := migrateDSN(t)
	sources := cartSources()

	require.NoError(t, db.Migrate(t.Context(), dsn, sources[0].src, sources[0].owner))
	before, err := readOwnerState(t.Context(), dsn, "cart")
	require.NoError(t, err)

	pool, err := db.New(t.Context(), db.DefaultConfig(dsn), nil)
	require.NoError(t, err)
	_, err = pool.Pool().Exec(t.Context(),
		"UPDATE cart"+migrationsTableSuffix+" SET dirty = true")
	require.NoError(t, err)
	pool.Close()

	dirtied, err := readOwnerState(t.Context(), dsn, "cart")
	require.NoError(t, err)
	require.True(t, dirtied.dirty, "the ledger could not be dirtied; the scenario is blind")

	// The measurement the godoc above quotes.
	rawErr := db.MigrateDown(t.Context(), dsn, sources[0].src, "cart", 1)
	t.Logf("golang-migrate's own answer to a dirty ledger: %v", rawErr)

	var out bytes.Buffer
	err = migrateDown(t.Context(), &out, dsn, sources,
		[]string{"cart", "-" + flagConfirm, "cart"}, "dev")

	require.Error(t, err, "a dirty ledger was rolled back on a confirmation")
	assert.Contains(t, err.Error(), migrationsTableSuffix,
		"the refusal must name the table an operator has to repair; without it the "+
			"refusal is a dead end")
	assert.Contains(t, out.String(), "Nothing was changed")

	after, err := readOwnerState(t.Context(), dsn, "cart")
	require.NoError(t, err)
	assert.Equal(t, before.version, after.version, "the dirty ledger's version moved")
	assert.True(t, after.dirty)
}

// TestStatusFailsWhenAnOwnerIsDirtyButStillPrintsTheTable checks the two halves
// of the diagnosis.
//
// The table is what a human came for and is printed either way; the non-zero
// exit is what a pre-deploy script reads. Printing without failing would let a
// pipeline roll forward onto a schema stranded between two versions; failing
// without printing would leave the operator with an exit code and no idea which
// owner it was about.
func TestStatusFailsWhenAnOwnerIsDirtyButStillPrintsTheTable(t *testing.T) {
	dsn := migrateDSN(t)
	sources := cartSources()

	require.NoError(t, db.Migrate(t.Context(), dsn, sources[0].src, sources[0].owner))

	pool, err := db.New(t.Context(), db.DefaultConfig(dsn), nil)
	require.NoError(t, err)
	_, err = pool.Pool().Exec(t.Context(), "UPDATE cart"+migrationsTableSuffix+" SET dirty = true")
	require.NoError(t, err)
	pool.Close()

	var out bytes.Buffer
	err = migrateStatus(t.Context(), &out, dsn, sources)

	require.Error(t, err, "a dirty owner must make the status command FAIL, not just print")
	assert.Contains(t, err.Error(), "cart")
	assert.Contains(t, out.String(), stateDirty,
		"the table must still be printed; the exit code alone does not say which owner")
}
