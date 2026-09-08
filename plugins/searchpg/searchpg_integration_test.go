//go:build integration

// The tests in this file need a real PostgreSQL instance (and therefore Docker);
// they are separated behind the `integration` tag so that `make test` stays
// fast. To run them: make test-integration
//
// None of the claims here can be proved with a fake: the tsvector match, the
// relevance ranking, websearch_to_tsquery NOT turning broken input into a syntax
// error, the sweep deleting only the stale rows, and the migration really being
// reversible are visible only by asking the server itself.
//
// The catalog is FAKE here too and has to be: the plugin cannot import any module
// (internal/arch TestPluginsDoNotImportModules) and the ban covers the test files
// too. So this file says "the index and the endpoints work against a real
// database"; it does NOT say "product's JSON schema is the same as this fake" —
// that bond can only be proved in an end-to-end installation.
package searchpg

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/link"
	"github.com/bdrtr/gobit/core/query"
)

const postgresImage = "postgres:16-alpine"

var (
	// testPool is the pool every test shares.
	testPool *db.Pool
	// testDSN is the shared database's address.
	testDSN string
)

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres brings up a single Postgres container, applies the plugin's
// schema and runs every test against it.
func runWithPostgres(m *testing.M) int {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_searchpg"),
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
		fmt.Fprintf(os.Stderr, "the connection address could not be read: %v\n", err)
		return 1
	}

	// The schema is applied the SAME way as in production: the module's
	// Migrations() and the module name. Writing CREATE TABLE by hand would leave
	// the migration itself untested.
	if err = db.Migrate(ctx, testDSN, migrationsRoot, ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "the searchpg schema could not be applied: %v\n", err)
		return 1
	}

	testPool, err = db.New(ctx, db.DefaultConfig(testDSN), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "the connection pool could not be opened: %v\n", err)
		return 1
	}
	defer testPool.Close()

	return m.Run()
}

// realIndex returns a store over an empty index table.
func realIndex(t *testing.T) *index {
	t.Helper()

	_, err := testPool.Pool().Exec(t.Context(), "TRUNCATE searchpg_product")
	require.NoError(t, err)

	return newIndex(testPool.Pool())
}

// write verilen belgeleri indekse yazar.
func write(t *testing.T, i *index, documents ...document) {
	t.Helper()

	require.NoError(t, i.Upsert(t.Context(), documents))
}

// TestTheIndexWritesSearchesAndDeletes verifies the index's basic cycle against
// real SQL.
//
// The test data is ASCII and that is deliberate: PostgreSQL's lower-casing
// depends on the database's ctype setting, and on a cluster created with the C
// locale non-ASCII letters are NOT folded. Making the test depend on the
// container's locale would give false confidence about the plugin's behavior.
func TestTheIndexWritesSearchesAndDeletes(t *testing.T) {
	i := realIndex(t)

	write(t, i,
		document{productID: "prod_1", title: "Blue shirt", keywords: "blue-shirt SKU-1", body: "Cotton summer"},
		document{productID: "prod_2", title: "Black trousers", keywords: "black-trousers SKU-2", body: "Denim trousers"},
	)

	ids, err := i.Search(t.Context(), "shirt", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"prod_1"}, ids)

	// The SKU has to be searchable: pasting a product code has to find the product.
	ids, err = i.Search(t.Context(), "SKU-2", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"prod_2"}, ids)

	// Case insensitivity is what the 'simple' dictionary gives.
	ids, err = i.Search(t.Context(), "SHIRT", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"prod_1"}, ids)

	silinen, err := i.Delete(t.Context(), "prod_1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), silinen)

	ids, err = i.Search(t.Context(), "shirt", 10, 0)
	require.NoError(t, err)
	assert.Empty(t, ids, "a deleted product must not appear in search")
}

// TestRelevanceRankingUsesTheWeights verifies that a word appearing in the title
// comes BEFORE one appearing in the description.
//
// Without the weights the ordering would be close to random and the claim
// "relevance order" would be empty: a product with a long description would come
// before the product whose exact name was searched for.
func TestRelevanceRankingUsesTheWeights(t *testing.T) {
	i := realIndex(t)

	write(t, i,
		document{productID: "prod_title", title: "Sneaker", keywords: "", body: "Comfortable footwear"},
		document{productID: "prod_body", title: "Boot", keywords: "", body: "As comfortable as a sneaker"},
		document{productID: "prod_keywords", title: "Slipper", keywords: "sneaker-like", body: ""},
	)

	ids, err := i.Search(t.Context(), "sneaker", 10, 0)
	require.NoError(t, err)
	require.Len(t, ids, 3, "all three records have to match")
	assert.Equal(t, "prod_title", ids[0], "the title match (A) has to be first")
	assert.Equal(t, "prod_body", ids[2], "the description match (C) has to be last")
}

// TestRankingPutsWeightAheadOfProximity verifies that on a multi-word query
// FIELD WEIGHT beats word proximity.
//
// Both fixtures carry the words "blue" and "shirt"; the difference is where.
// prod_title carries both IN ITS TITLE (A) but with four words between them,
// while prod_keywords carries them side by side in the KEYWORD field (B). The
// order therefore TELLS THE RANKING FUNCTIONS APART, and that is why this test
// exists:
//
//	ts_rank    -> prod_title (0,915) > prod_keywords (0,396)
//	ts_rank_cd -> prod_keywords (0,4)  > prod_title (0,2)
//
// [searchSQL] uses ts_rank: ts_rank_cd spent ~12 µs on EVERY matching document
// and pushed a query with 52 thousand matches to 663 ms (the measurement is
// documented on [searchSQL]). This test fixes that decision in behavior — it
// breaks here if the ranking goes back to ts_rank_cd — and at the same time it
// writes down what was given up: proximity is no longer a signal, weight is
// always ahead.
func TestRankingPutsWeightAheadOfProximity(t *testing.T) {
	i := realIndex(t)

	write(t, i,
		document{productID: "prod_title", title: "Blue jacket trousers shoes and shirt"},
		document{productID: "prod_keywords", title: "Slipper", keywords: "blue shirt"},
	)

	ids, err := i.Search(t.Context(), "blue shirt", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"prod_title", "prod_keywords"}, ids,
		"the product matching in the title (A) has to come before the one matching side by side in the keywords field (B)")
}

// TestDescriptionLengthDoesNotChangeRanking verifies that a product with a long
// description does NOT FALL BEHIND one with a short description and the same
// title match.
//
// [searchSQL] calls ts_rank WITHOUT a normalization argument, so document length
// never enters the score. Adding the argument is a one-character change and it
// quietly installs a different relevance model: ts_rank(..., 2) divides the score
// by the document length and drops the longer-described of the two products below
// from 0.608 to 0.043 — a seller who writes their catalog in detail would fall
// behind in search for the same product. Both fixtures match ONLY in the title,
// so without normalization the scores are exactly equal and the order is
// decided by product_id.
func TestDescriptionLengthDoesNotChangeRanking(t *testing.T) {
	i := realIndex(t)

	write(t, i,
		document{
			productID: "prod_a_long",
			title:     "Sneaker",
			body: "the description of this item is long and carries many words " +
				"it is written out in detail in the catalog",
		},
		document{productID: "prod_b_short", title: "Sneaker"},
	)

	ids, err := i.Search(t.Context(), "sneaker", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"prod_a_long", "prod_b_short"}, ids,
		"with an equal title match the order has to be decided by product_id, not document length")
}

// TestExclusionKeepsTheRanking verifies that on an "exclude with -" query the
// ordering stays BY RELEVANCE.
//
// ts_rank gives EVERY document 0 on a query carrying a negation (the measurement
// is documented on [searchSQL]). Had the ranking used the raw query, the scores
// would level out and the order would fall to product_id, that is, to indexing
// order. The fixture is built to make that VISIBLE: the relevance order (title
// first) is the REVERSE of the product_id order, so the test fails when the score
// collapses.
//
//	querytree ile        prod_b_title 0,6079 > prod_a_body 0,1216
//	without querytree    both 0.0000 -> the order is product_id
//
// prod_c_blue shows that the filtering still works: while the ranking uses the
// positive part, the exclusion stays in the WHERE.
func TestExclusionKeepsTheRanking(t *testing.T) {
	i := realIndex(t)

	write(t, i,
		document{productID: "prod_a_body", title: "Slipper", body: "this item is worn with a shirt"},
		document{productID: "prod_b_title", title: "Shirt"},
		document{productID: "prod_c_blue", title: "Blue shirt"},
	)

	ids, err := i.Search(t.Context(), "shirt -blue", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"prod_b_title", "prod_a_body"}, ids,
		"even on a query with an exclusion the title (A) match has to come before the description (C) match")
}

// TestAQueryOfOnlyExclusionsWorks verifies that a query made only of exclusions
// produces NO error and still filters.
//
// On that query the ranking expression gets 'T' from querytree — there is no
// positive signal to rank by — and the order falls to product_id. The test writes
// that limit down IN BEHAVIOUR: the result set is right, its order is the
// indexing order, and that is a known limit (see [searchSQL]). And had turning
// the text 'T' into a tsquery produced a syntax error, a shopper typing "-blue"
// into the search box would get a 500; that is this test's second job.
func TestAQueryOfOnlyExclusionsWorks(t *testing.T) {
	i := realIndex(t)

	write(t, i,
		document{productID: "prod_1", title: "Shirt"},
		document{productID: "prod_2", title: "Blue shirt"},
		document{productID: "prod_3", title: "Terlik"},
	)

	ids, err := i.Search(t.Context(), "-blue", 10, 0)
	require.NoError(t, err, "an exclusion query must produce no error")
	assert.Equal(t, []string{"prod_1", "prod_3"}, ids,
		"the product containing blue has to be filtered out and the rest come in product_id order")
}

// TestTheRankingExpressionIsComputedOncePerQuery verifies through the PLAN that
// the ranking query stays a SCALAR SUBQUERY.
//
// The decision is for SPEED and does NOT change the result: with the subquery
// removed and the expression inlined, every document gets the same score, so no
// test that exercises the ordering can see it (verified by mutation: the change
// removing the subquery passed every test in the package). The only place it is
// visible is the plan: a subquery becomes an InitPlan and is computed once per
// query, while an inline expression is reparsed per row on the generic plan
// (25.4 ms against 46.7 ms on 52 thousand matches; the measurement is documented
// on [searchSQL]).
func TestTheRankingExpressionIsComputedOncePerQuery(t *testing.T) {
	i := realIndex(t)
	write(t, i, document{productID: "prod_1", title: "Shirt"})

	rows, err := testPool.Pool().Query(t.Context(), "EXPLAIN (COSTS OFF) "+searchSQL, "shirt", 10, 0)
	require.NoError(t, err)
	defer rows.Close()

	var plan strings.Builder
	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		plan.WriteString(line)
		plan.WriteString("\n")
	}
	require.NoError(t, rows.Err())

	assert.Contains(t, plan.String(), "InitPlan",
		"the ranking query has to be an InitPlan; inlined it is computed per row on the generic plan:\n%s", plan.String())
}

// TestPagingIsDeterministic verifies that equally relevant records do not SHIFT
// between pages.
//
// Without the second sort key the same query could show one product on two pages
// or show a product on none.
func TestPagingIsDeterministic(t *testing.T) {
	i := realIndex(t)

	var documents []document
	for n := range 5 {
		documents = append(documents, document{
			productID: "prod_" + strconv.Itoa(n),
			title:     "Ayni title",
		})
	}
	write(t, i, documents...)

	ilk, err := i.Search(t.Context(), "ayni", 2, 0)
	require.NoError(t, err)
	ikinci, err := i.Search(t.Context(), "ayni", 2, 2)
	require.NoError(t, err)
	ucuncu, err := i.Search(t.Context(), "ayni", 2, 4)
	require.NoError(t, err)

	tum := append(append(append([]string{}, ilk...), ikinci...), ucuncu...)
	assert.Equal(t, []string{"prod_0", "prod_1", "prod_2", "prod_3", "prod_4"}, tum,
		"the pages have to give every record with no overlap and no gap")
}

// TestABrokenQueryDoesNotProduceA500 verifies that a user's text does not bring
// the query down.
//
// Had to_tsquery been chosen, these inputs would give a syntax error and produce
// 500s depending on what somebody typed into the search box;
// websearch_to_tsquery treats all of them as text.
func TestABrokenQueryDoesNotProduceA500(t *testing.T) {
	i := realIndex(t)
	write(t, i, document{productID: "prod_1", title: "Blue shirt"})

	for _, query := range []string{"& &", "!", "a | | b", "((", "'", `"`, "-"} {
		ids, err := i.Search(t.Context(), query, 10, 0)
		require.NoError(t, err, "the query %q must produce no error", query)
		assert.Empty(t, ids, "a meaningless query must return no match: %q", query)
	}

	// The syntax websearch brings has to work too: a quote means an exact phrase.
	ids, err := i.Search(t.Context(), `"blue shirt"`, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"prod_1"}, ids)
}

// TestUpsertUpdatesTheSameID verifies that a second write does NOT open a new
// row; the subscriber being idempotent rests on it.
func TestUpsertUpdatesTheSameID(t *testing.T) {
	i := realIndex(t)

	write(t, i, document{productID: "prod_1", title: "Eski title"})
	write(t, i, document{productID: "prod_1", title: "Yeni title"})

	var rowCount int
	require.NoError(t, testPool.Pool().
		QueryRow(t.Context(), "SELECT count(*) FROM searchpg_product").Scan(&rowCount))
	assert.Equal(t, 1, rowCount, "there has to be one row per product")

	ids, err := i.Search(t.Context(), "yeni", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"prod_1"}, ids)

	ids, err = i.Search(t.Context(), "eski", 10, 0)
	require.NoError(t, err)
	assert.Empty(t, ids, "an updated document must not match its old text")
}

// TestTheSweepDeletesOnlyStaleRows verifies that the sweep does not touch the
// rows written after the round.
//
// The threshold comes from the database clock; had the application clock been
// used, the drift between the two could make fresh rows look stale.
func TestTheSweepDeletesOnlyStaleRows(t *testing.T) {
	i := realIndex(t)

	write(t, i, document{productID: "prod_bayat", title: "Eski"})

	threshold, err := i.Now(t.Context())
	require.NoError(t, err)

	write(t, i, document{productID: "prod_taze", title: "Yeni"})

	silinen, err := i.Sweep(t.Context(), threshold)
	require.NoError(t, err)
	assert.Equal(t, int64(1), silinen)

	ids, err := i.Search(t.Context(), "eski", 10, 0)
	require.NoError(t, err)
	assert.Empty(t, ids, "a row older than the threshold has to have been deleted")

	ids, err = i.Search(t.Context(), "yeni", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"prod_taze"}, ids, "a row refreshed during the round has to STAY")
}

// TestTheSchemaCreatesAGINIndex verifies that the migration really produces a
// GIN index.
//
// "CREATE INDEX IF NOT EXISTS" produces a NOTICE rather than an error and skips
// when another relation of the same name exists; that is, the index may quietly
// never have been created. In that case every search falls to a full table scan
// and that is invisible in every test until the catalog grows (the same reasoning
// as core/link's verifySchema).
func TestTheSchemaCreatesAGINIndex(t *testing.T) {
	var definition string
	err := testPool.Pool().QueryRow(t.Context(), `
		SELECT indexdef FROM pg_indexes
		WHERE tablename = 'searchpg_product' AND indexname = 'searchpg_product_document_idx'`).
		Scan(&definition)

	require.NoError(t, err, "the document index has to have been created")
	assert.Contains(t, definition, "USING gin", "the document index has to be a GIN one")
}

// TestTheSearchEndpointRunsAgainstARealIndex verifies the storefront endpoint end
// to end: a real index gives the ids and the catalog gives the records.
func TestTheSearchEndpointRunsAgainstARealIndex(t *testing.T) {
	i := realIndex(t)
	k := newFakeCatalog()
	k.addProduct("prod_1", "Blue shirt", "Cotton")
	k.addProduct("prod_2", "Black trousers", "Denim")
	write(t, i,
		document{productID: "prod_1", title: "Blue shirt", body: "Cotton"},
		document{productID: "prod_2", title: "Black trousers", body: "Denim"},
	)

	m := testModule(i, k)
	rec := request(m, http.MethodGet, searchURL(testChannel, "?q=shirt"), storePrincipal(testChannel))

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"id":"prod_1"`)
	assert.NotContains(t, rec.Body.String(), `"id":"prod_2"`)
}

// TestReindexingRefreshesARealDatabase verifies that a full round both writes and
// drops the stale rows.
//
// This is the only repair path when the plugin is fitted to an existing
// installation or when an event is missed.
func TestReindexingRefreshesARealDatabase(t *testing.T) {
	i := realIndex(t)

	// A stale row for a product that is no longer published (the catalog does not
	// return it).
	write(t, i, document{productID: "prod_withdrawn", title: "Withdrawn item"})

	k := newFakeCatalog()
	graph := &fakeGraph{}
	for n := range 3 {
		id := "prod_" + strconv.Itoa(n)
		graph.ids = append(graph.ids, id)
		k.addProduct(id, "Item "+strconv.Itoa(n), "a description")
	}

	m := testModule(i, k)
	m.graph = graph

	result, err := m.reindex(t.Context())
	require.NoError(t, err)

	assert.Equal(t, 3, result.Indexed)
	assert.Equal(t, int64(1), result.Removed, "an unpublished product has to be swept")
	assert.Equal(t, 1, result.Pages)

	ids, err := i.Search(t.Context(), "item", 10, 0)
	require.NoError(t, err)
	assert.Len(t, ids, 3)

	ids, err = i.Search(t.Context(), "withdrawn", 10, 0)
	require.NoError(t, err)
	assert.Empty(t, ids, "a stale row must not appear in search")
}

// TestTheEventStreamWritesToARealIndex verifies the subscriber -> catalog ->
// index path against a real table.
func TestTheEventStreamWritesToARealIndex(t *testing.T) {
	i := realIndex(t)
	k := newFakeCatalog()
	k.addProduct("prod_1", "Blue shirt", "Cotton summer")
	m := testModule(i, k)

	require.NoError(t, m.productWritten(t.Context(), event(eventProductCreated, "prod_1")))
	ids, err := i.Search(t.Context(), "shirt", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"prod_1"}, ids)

	require.NoError(t, m.productDeleted(t.Context(), event(eventProductDeleted, "prod_1")))
	ids, err = i.Search(t.Context(), "shirt", 10, 0)
	require.NoError(t, err)
	assert.Empty(t, ids, "a deletion event has to remove it from the index")
}

// TestRegisterResolvesFromTheCore verifies that the module asks only for core
// services and BRINGS STARTUP DOWN when one is missing.
func TestRegisterResolvesFromTheCore(t *testing.T) {
	log := slog.New(slog.DiscardHandler)

	t.Run("a missing core service", func(t *testing.T) {
		c := container.New(log)
		t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

		m := newSearchModule(c, log)
		err := m.Register(t.Context(), c)

		require.Error(t, err, "registration has to fail while core.db is absent")
		assert.Equal(t, codeSetupFailed, coreerrors.CodeOf(err))
	})

	t.Run("tam kurulum", func(t *testing.T) {
		c := container.New(log)
		t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

		links := link.New(testPool, nil)
		require.NoError(t, c.Provide(svcDB, testPool))
		require.NoError(t, c.Provide(svcQuery, query.New(links, c, nil)))

		m := newSearchModule(c, log)
		require.NoError(t, m.Register(t.Context(), c))

		// The endpoints are bound only AFTER the registration.
		desenler := chiPatterns(testRouter(m))
		assert.Contains(t, desenler, "GET "+SearchPath)
		assert.Contains(t, desenler, "POST "+ReindexPath)
	})
}

// TestTheMigrationCanBeRolledBackAndReapplied verifies that the schema survives
// an up -> down -> up cycle.
//
// internal/arch's gate of the same name scans only under internal/modules; a
// migration that cannot be rolled back leaves golang-migrate's version ledger
// "dirty", and from that point on the server does NOT COME UP again.
//
// The test is placed LAST: it drops the table and creates it again, so any test
// running after it would start with an empty table.
func TestTheMigrationCanBeRolledBackAndReapplied(t *testing.T) {
	ctx := t.Context()

	require.NoError(t, db.MigrateDown(ctx, testDSN, migrationsRoot, ModuleName, 0),
		"the schema has to be reversible")

	var remaining int
	require.NoError(t, testPool.Pool().QueryRow(ctx, `
		SELECT count(*) FROM pg_tables WHERE tablename = 'searchpg_product'`).Scan(&remaining))
	assert.Zero(t, remaining, "the rollback has to drop the table")

	require.NoError(t, db.Migrate(ctx, testDSN, migrationsRoot, ModuleName),
		"the schema has to be reappliable")

	_, err := newIndex(testPool.Pool()).Search(ctx, "shirt", 10, 0)
	assert.NoError(t, err, "the reapplied schema has to be usable")
}
