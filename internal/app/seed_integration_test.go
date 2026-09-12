//go:build integration

package app

import (
	"bytes"
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/db"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/rig"
)

// rigDatabase is the database [migrateDSN]'s container serves, and therefore
// the word `gobit seed -reset` demands back from the operator.
//
// It is spelled out here rather than parsed from the DSN on purpose: what the
// confirmation compares against is asked of the SERVER, and a test that derived
// its expectation from the same connection string the pool was built from could
// not tell a correct answer from an echo.
const rigDatabase = "gobit_migrate"

// The rig this file builds. It is the smallest catalog that still has both
// product families, a taxonomy and the four hand-made rows; the measured rig is
// 52,004 products and would take the whole test budget to no extra purpose.
const (
	smallSingleVariantProducts = 6
	smallMultiVariantProducts  = 2
	smallCategories            = 2
	smallTags                  = 2
	// smallSkewedCategorySize is how many products each skewed category holds
	// here.
	//
	// It is above zero although the rig's default is zero, because the reset is
	// what this file proves and the two skewed categories are the only rows in
	// the generator that a pattern cannot reach: they are deleted by their
	// literal ids, and a spec that never built them would leave that branch
	// asserted by nothing.
	smallSkewedCategorySize = 2
	// handMadeProductCount is what the generator adds on top of the two
	// families: the free product and the three Turkish-diacritic rows.
	handMadeProductCount = 4
	// seedChannel is the storefront the products are assigned to.
	seedChannel = "rig-test"
)

// seedEnv points the binary at a database of its own.
//
// The seed command loads the SERVER's configuration, which is the property the
// whole design rests on — run inside the running container it is already
// pointed at the installation it is meant to rebuild — so the test configures
// it the same way.
func seedEnv(t *testing.T, dsn string) {
	t.Helper()

	t.Setenv("APP_ENV", "development")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("JWT_SECRET", "seed-integration-test-secret-32-bytes-long")
	t.Setenv("LOG_LEVEL", "warn")
}

// smallSpec is the argument list every run below shares.
func smallSpec() []string {
	return []string{
		seedCommand,
		"-" + flagProducts, strconv.Itoa(smallSingleVariantProducts),
		"-" + flagMulti, strconv.Itoa(smallMultiVariantProducts),
		"-" + flagCategories, strconv.Itoa(smallCategories),
		"-" + flagTags, strconv.Itoa(smallTags),
		"-" + flagSkew, strconv.Itoa(smallSkewedCategorySize),
		"-" + flagChannel, seedChannel,
	}
}

// seedSmallRig builds the catalog and returns the command's report.
func seedSmallRig(t *testing.T, dsn string) string {
	t.Helper()

	seedEnv(t, dsn)

	var out bytes.Buffer
	require.NoError(t, Main(smallSpec(), &out, Options{}),
		"the small rig could not be built, so nothing below is measuring a reset")

	return out.String()
}

// rigPool opens a pool of the test's own, outside the application.
func rigPool(t *testing.T, dsn string) *db.Pool {
	t.Helper()

	pool, err := db.New(context.Background(), db.DefaultConfig(dsn), nil)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return pool
}

// countRig reports what the database holds, table by table.
func countRig(t *testing.T, pool *db.Pool) rig.Counts {
	t.Helper()

	counts, err := rig.Count(context.Background(), pool)
	require.NoError(t, err)

	return counts
}

// TestTheConfirmationIsComparedWithTheDatabaseTheConnectionReached is the query
// the whole safety of `gobit seed -reset` rests on.
//
// The confirmation is ONE string comparison, and the right-hand side of it is
// this. A DSN can be overridden by PGDATABASE, by a connection service file or
// by a keyword form this repository does not parse, so the name is asked of the
// server rather than read off the string the pool was built from. If it came
// back wrong the guard would not merely be weaker — it would be guarding a
// different database than the one the deletions run in, which is the exact
// failure the confirmation exists to make impossible.
func TestTheConfirmationIsComparedWithTheDatabaseTheConnectionReached(t *testing.T) {
	dsn := migrateDSN(t)
	pool := rigPool(t, dsn)

	name, err := rig.DatabaseName(context.Background(), pool)
	require.NoError(t, err)
	assert.Equal(t, rigDatabase, name,
		"the name the operator must repeat has to be the database the rows are really in")
}

// TestASeedNamesTheDatabaseItIsAboutToWriteTo is the operator's first line of
// defense, and it is printed before anything is written.
//
// Somebody about to rebuild a rig has usually just exported a DSN. The report
// says which installation answered, so a run pointed at the wrong one is
// visible in its own output rather than in a catalog somebody finds later.
func TestASeedNamesTheDatabaseItIsAboutToWriteTo(t *testing.T) {
	dsn := migrateDSN(t)

	report := seedSmallRig(t, dsn)

	assert.Contains(t, report, "database \""+rigDatabase+"\"",
		"the report has to name the database, and name the real one")
	assert.Contains(t, report, seedChannel, "and the storefront the catalog was assigned to")
}

// TestAResetWithNoConfirmationDeletesNothing is the refusal proved against rows
// that exist.
//
// The unit tests around [resetPlanText] prove the WORDING; they cannot prove
// that the command stopped. This one seeds a catalog, asks for a reset with the
// flag forgotten, and then counts: a refusal that printed the plan and deleted
// anyway would satisfy every assertion made about its output.
func TestAResetWithNoConfirmationDeletesNothing(t *testing.T) {
	dsn := migrateDSN(t)
	seedSmallRig(t, dsn)

	pool := rigPool(t, dsn)
	before := countRig(t, pool)
	require.Positive(t, before.Of("product"),
		"the rig is empty before the reset was even attempted; a later count of zero "+
			"would prove nothing")

	var out bytes.Buffer
	err := Main(append(smallSpec(), "-"+flagReset), &out, Options{})

	require.Error(t, err, "a destructive run must not proceed on a forgotten flag")
	assert.True(t, coreerrors.IsInvalid(err), "error: %v", err)
	assert.Equal(t, codeResetRefused, coreerrors.CodeOf(err),
		"the refusal has its own code because \"nothing was touched\" and \"it broke "+
			"halfway\" call for opposite next steps")
	assert.Contains(t, out.String(), "no confirmation given")

	assert.Equal(t, before, countRig(t, pool),
		"the catalog changed under a run that reported it had changed nothing")
}

// TestAResetConfirmedForAnotherDatabaseDeletesNothing is the same refusal in the
// case that actually happens.
//
// A forgotten flag is a typo. This one is an operator who believes the
// connection goes somewhere else — the state in which a wipe lands on the wrong
// installation — and it is the reason the confirmation is compared with a name
// the server reported rather than accepted as a yes/no.
func TestAResetConfirmedForAnotherDatabaseDeletesNothing(t *testing.T) {
	dsn := migrateDSN(t)
	seedSmallRig(t, dsn)

	pool := rigPool(t, dsn)
	before := countRig(t, pool)

	var out bytes.Buffer
	err := Main(append(smallSpec(), "-"+flagReset, "-"+flagConfirm, "gobit_somewhere_else"),
		&out, Options{})

	require.Error(t, err)
	assert.Equal(t, codeResetRefused, coreerrors.CodeOf(err), "error: %v", err)
	assert.Contains(t, out.String(),
		"the confirmation names \"gobit_somewhere_else\", but this connection is to \""+
			rigDatabase+"\"",
		"the refusal has to say which of the two names is the connection's")

	assert.Equal(t, before, countRig(t, pool),
		"rows were deleted in a database the operator did not name")
}

// TestAResetNamingThisDatabaseGoesAhead is the other half, and it is the half
// that would be missed by a guard that simply never passed.
//
// A confirmation compared against the wrong value fails in two directions: it
// lets a wipe through against a database the operator did not name, or it
// refuses every correct attempt and makes the Makefile's documented way to
// rebuild the rig unusable. The refusals above cover the first. This covers the
// second, by typing the word the server reported and watching the rows go.
//
// The rebuild is asked for a SMALLER catalog than the one already there, and
// that is what makes the deletion observable at all. A seed writes deterministic
// ids and skips the ones it finds (ON CONFLICT DO NOTHING), so re-seeding the
// same spec over an unreset rig produces exactly the counts a real reset would
// — measured, and it is how this test first passed against a build whose reset
// had been removed. Shrinking the spec leaves the tail of the old catalog with
// nothing to recreate it.
func TestAResetNamingThisDatabaseGoesAhead(t *testing.T) {
	dsn := migrateDSN(t)
	seedSmallRig(t, dsn)

	pool := rigPool(t, dsn)
	require.True(t, productExists(t, pool, "prod_B"+strconv.Itoa(smallSingleVariantProducts)),
		"the rig this reset is supposed to delete was not built")

	const shrunkTo = 3

	var out bytes.Buffer
	rebuild := append(smallSpec(), "-"+flagReset, "-"+flagConfirm, rigDatabase)
	rebuild = append(rebuild, "-"+flagProducts, strconv.Itoa(shrunkTo))
	require.NoError(t, Main(rebuild, &out, Options{}),
		"the reset was refused although the confirmation named this very database; "+
			"the sanctioned way to rebuild the rig would be unusable")

	report := out.String()
	assert.Contains(t, report, "the rig's rows were deleted",
		"a run that deleted is required to say so; the operator's only other evidence "+
			"is counting rows by hand")
	assert.NotContains(t, report, "REFUSED")

	assert.False(t, productExists(t, pool, "prod_B"+strconv.Itoa(smallSingleVariantProducts)),
		"a product the rebuilt catalog is too small to contain is still there, so the "+
			"confirmed reset deleted nothing and the seed wrote on top of the old rig")
	assert.Equal(t, int64(shrunkTo+smallMultiVariantProducts+handMadeProductCount),
		countRig(t, pool).Of("product"),
		"the rebuild has to leave exactly the catalog that was asked for; anything "+
			"more is the previous rig still standing underneath it")
}

// TestTheTwoSkewedCategoriesHoldTheSameCountAndNotTheSameProducts is the whole
// claim the skew makes, and neither half of it survives without a database.
//
// The rig's taxonomy is uniform by construction, and the measurement that
// decided how the product filter is written found its case at the other end:
// a category holding a fraction of a percent of the catalog. It also found that
// the SIZE is not what decides the cost — two categories of the same size gave
// two different plans, and what separated them was where their members sat in
// the listing order. So the pair is the fixture, not either one of them, and
// the pair is only a pair while the two sets are the same size and different
// sets.
//
// The failure this guards is a simplification that looks harmless: picking the
// spread category with a plain LIMIT, or by a numeric run of ids. Both produce
// two categories of the right size, both pass every count, and both make the
// second category a copy of the first — after which the rig would carry two
// samples of one shape and a reader would take one plan for the law.
// It rebuilds from EMPTY rather than seeding on top, and that is not tidiness.
// The skewed memberships are positional — the head of the listing, then every
// stride-th row of it — so a seed over a catalog of a different size adds a
// second set of rows beside the first and the category ends up holding neither
// shape. That hazard is written on the generator; here it would simply make the
// assertions read a mixture.
func TestTheTwoSkewedCategoriesHoldTheSameCountAndNotTheSameProducts(t *testing.T) {
	dsn := migrateDSN(t)
	seedEnv(t, dsn)

	var out bytes.Buffer
	rebuild := append(smallSpec(), "-"+flagReset, "-"+flagConfirm, rigDatabase)
	require.NoError(t, Main(rebuild, &out, Options{}),
		"the rig could not be rebuilt, so the memberships below would describe whatever "+
			"catalog the previous test left behind")

	pool := rigPool(t, dsn)

	adjacent := categoryMembers(t, pool, rig.AdjacentSkewCategoryID)
	spread := categoryMembers(t, pool, rig.SpreadSkewCategoryID)

	require.Len(t, adjacent, smallSkewedCategorySize,
		"the adjacent skew category does not hold the size it was asked for")
	require.Len(t, spread, smallSkewedCategorySize,
		"the spread skew category does not hold the size it was asked for; two categories "+
			"of different sizes cannot answer the question the pair exists for")

	assert.NotEqual(t, adjacent, spread,
		"the two skewed categories hold the SAME products, so the rig carries one shape "+
			"twice. The stride that spreads the second one across the listing has stopped "+
			"striding, and the case the measurement found — same size, two plans — is back "+
			"to being unreproducible")

	for _, id := range append(append([]string{}, adjacent...), spread...) {
		assert.Regexp(t, `^prod_(B|L)[0-9]+$`, id,
			"a skewed category holds %q, which is not a product this rig generated. The "+
				"members are picked out of the listing, so a loosened pattern would draw the "+
				"installation's own catalog into a measurement fixture", id)
	}
}

// categoryMembers returns the products of one category, in the listing's order.
func categoryMembers(t *testing.T, pool *db.Pool, categoryID string) []string {
	t.Helper()

	rows, err := pool.Pool().Query(context.Background(),
		`SELECT m.product_id
FROM product_category_map m
JOIN product p ON p.id = m.product_id
WHERE m.category_id = $1
ORDER BY p.created_at DESC, p.id DESC`, categoryID)
	require.NoError(t, err)
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	require.NoError(t, rows.Err())

	return ids
}

// productExists reports whether the catalog holds a product with that id.
func productExists(t *testing.T, pool *db.Pool, id string) bool {
	t.Helper()

	var exists bool
	require.NoError(t, pool.Pool().QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM product WHERE id = $1)`, id).Scan(&exists))

	return exists
}

// TestTheResetEmptiesTheRigAndLeavesTheRestOfTheCatalogStanding is the
// statement no unit test can make.
//
// [rig.Reset] deletes by PATTERN because the obvious spelling — every id
// starting with prod_ — is the module's own prefix and would take the
// installation's real catalog with it. That claim is about SQL meeting real
// rows, and the difference between the two spellings is invisible until a
// database is asked. So a product with a generated id is put next to the rig's,
// and it has to be there afterwards.
//
// The identities are the second half: a reset deletes rows, and revoking a key
// a storefront is authenticating with would be a different and much worse act.
func TestTheResetEmptiesTheRigAndLeavesTheRestOfTheCatalogStanding(t *testing.T) {
	ctx := context.Background()
	dsn := migrateDSN(t)
	seedSmallRig(t, dsn)

	pool := rigPool(t, dsn)

	// A product of the shape the application mints: the prefix the rig shares,
	// then 26 Crockford Base32 characters instead of digits.
	const realProductID = "prod_01JQZ8ZK9YQ3W6M7X2VBCDEFGH"
	_, err := pool.Pool().Exec(ctx,
		`INSERT INTO product (id, handle, title) VALUES ($1, 'shop-own-product', 'A real product')`,
		realProductID)
	require.NoError(t, err)

	before := countRig(t, pool)
	require.Positive(t, before.Of("price_set"))
	require.Positive(t, before.Of("link_product_sales_channel"))

	channels, keys := identityCounts(t, pool)
	require.Positive(t, channels)
	require.Positive(t, keys)

	after, err := rig.Reset(ctx, pool)
	require.NoError(t, err)

	// The counts come back from the same call, and they are the DATABASE's
	// answer rather than the request's: a reset that reported what it meant to
	// delete would say the same thing whether or not the statements matched
	// anything.
	assert.Equal(t, int64(1), after.Of("product"),
		"the rig's products had to go and the installation's own had to stay; a count "+
			"of 0 means a real catalog was just deleted, and anything above 1 means "+
			"the reset missed the rows it was written for")
	for _, table := range []string{
		"price_set", "inventory_items", "product_category", "product_tag",
		"link_product_sales_channel", "link_product_variant_price_set",
		"link_product_variant_inventory",
	} {
		assert.Equal(t, int64(0), after.Of(table),
			"%s still holds rig rows, which the next seed would then write alongside "+
				"rather than replace", table)
	}
	assert.Equal(t, int64(0), after.Of("product_variant"),
		"the variants hang off the deleted products by ON DELETE CASCADE; if they "+
			"survive, the cascade the reset relies on instead of a second hand-written "+
			"model of the foreign keys is gone")

	assert.Equal(t, after, countRig(t, pool),
		"the counts the reset reported are not what the database holds")

	channelsAfter, keysAfter := identityCounts(t, pool)
	assert.Equal(t, channels, channelsAfter,
		"a sales channel was deleted; the reset promises to touch rows and not identities")
	assert.Equal(t, keys, keysAfter,
		"a publishable key was revoked by a command whose job is rows, and no storefront "+
			"using it could authenticate again")

	assert.True(t, productExists(t, pool, realProductID),
		"the installation's own product is gone: the reset matched by prefix rather "+
			"than by the rig's digits-only id shape, and a customer's catalog would "+
			"have been deleted by a development tool")
}

// identityCounts reports the two things a reset must not touch.
func identityCounts(t *testing.T, pool *db.Pool) (channels, keys int64) {
	t.Helper()

	require.NoError(t, pool.Pool().QueryRow(context.Background(),
		`SELECT (SELECT count(*) FROM sales_channel), (SELECT count(*) FROM api_key)`).
		Scan(&channels, &keys))

	return channels, keys
}
