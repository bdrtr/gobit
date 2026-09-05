// Package rig rebuilds the catalog this repository's performance sentences are
// measured on.
//
// # What was wrong
//
// The 52,000-product catalog every timing figure in this repository leans on
// existed as live rows in ONE Docker volume and in nothing else: no seed file,
// no seed target, no seed program. `docker compose down -v` was the whole
// distance between a measured claim and unfalsifiable prose. The repository's
// own rule — a performance sentence is never written unmeasured — therefore
// depended on a volume nobody had promised to keep.
//
// This package is the exit. It builds the catalog from generate_series in a
// single transaction, deterministically, so a rebuilt rig is the SAME rig: the
// same ids, the same handles, the same titles, the same amounts and the same
// four created_at values as the one measured on 2026-09-03.
//
// # Why bulk SQL and not the module services
//
// The obvious shape — call the product module's service once per product, as
// the fixtures in internal/e2e do — was rejected on arithmetic. The original
// rig was built with generate_series and took seconds, and so does this: the
// full catalog is MEASURED at 13.6 s of inserts plus the vacuum below. The same
// catalog through the service layer is 52,004 CreateProduct calls plus 54,000
// CreateVariant, 54,000 CreatePriceSet and 54,000 CreateInventoryItem calls,
// each its own round trip and its own transaction; over HTTP it is worse again
// by the whole guard stack, and the honest estimate for that shape is hours. A
// rebuild nobody will wait for is a rebuild nobody runs, and a rig nobody
// rebuilds is the state this package exists to end.
//
// The price of that decision is written down rather than hidden: these
// statements name other modules' tables directly, so they do NOT go through
// validation, they publish NO events, and they write NO audit rows. Three
// consequences follow and all three are deliberate:
//
//   - The search index stays EMPTY. plugins/searchpg fills itself from
//     "product.created" and "product.updated" events, and a bulk INSERT
//     publishes none. Its own recovery path pages through the read layer 100
//     ids at a time, which is 520 OFFSET pages over this catalog at the offset
//     cost internal/core/page has already measured. The searchpg figures keep
//     resting on their own separate fixture; see [Seed] for what this package
//     does not build.
//   - The rows are not validated on the way in. The schema still is: every
//     CHECK, every unique index and every foreign key of the modules'
//     migrations runs, because the statements go into the very tables those
//     migrations created.
//   - This package is a DEVELOPMENT tool and belongs to no module. It is not
//     reachable from any HTTP surface and nothing in a running server calls it.
//
// # Why the schema is not this package's business
//
// Nothing here creates a table. The caller is expected to have brought the
// installation up first, which is what makes the drift the old rig suffered
// impossible: gobit_load was measured five order migrations, one payment
// migration and one product migration BEHIND the repository, and missing the
// invoice, job, outbox and audit schemas entirely. A rig whose schema is
// applied by the same code the server applies cannot fall behind it.
//
// Three of the tables filled here — link_product_variant_price_set,
// link_product_variant_inventory and link_product_sales_channel — appear in NO
// migration at all. core/link creates them at run time from a Define call
// (ADR 0005) during the product module's bootstrap, which is the second reason
// the caller has to boot the installation before seeding and the reason a plain
// `psql -f seed.sql` could never have worked: the first link INSERT would hit a
// table that does not exist yet.
package rig

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
)

// The error codes of this package.
const (
	codeInvalidSpec = "rig_invalid_spec"
	codeSeedFailed  = "rig_seed_failed"
	codeResetFailed = "rig_reset_failed"
	codeCountFailed = "rig_count_failed"
)

// The rig's documented default shape, as measured on the surviving rig.
//
// The two families are not decoration; they are what makes the catalog exercise
// two different read paths at once. The single-variant family is the bulk that
// makes the storefront's count query walk 52,004 rows, and the multi-variant
// family is the only part of it where a product carries more than one variant
// and more than one currency — the enrichment leg the storefront pays per page.
const (
	// DefaultSingleVariantProducts is how many single-variant products the rig
	// carries (family B: prod_B1 ... prod_B50000).
	DefaultSingleVariantProducts = 50_000
	// DefaultMultiVariantProducts is how many two-variant products the rig
	// carries (family L: prod_L1 ... prod_L2000).
	//
	// Each of them carries two variants and each variant two prices, so the
	// family contributes 4,000 variants and 8,000 prices.
	DefaultMultiVariantProducts = 2_000
	// DefaultCategories is how many categories the products are spread over.
	//
	// The surviving rig has NONE: product_category and product_category_map are
	// both empty in it, which is why the category_id filter the product
	// provider grew on 2026-09-05 has no fixture and cannot be measured at all.
	// A rebuild that reproduced that emptiness would reproduce the gap, so the
	// taxonomy is built here — twenty categories over the default catalog is
	// 2,600 products each, selective enough for the filter to be worth
	// measuring and coarse enough that a category page is not a single row.
	DefaultCategories = 20
	// DefaultTags is how many tags the products are spread over; the reasoning
	// is [DefaultCategories]'s, for the tag_id filter.
	DefaultTags = 20
)

// Spec is the shape of the catalog to build.
//
// Every field is a COUNT rather than a switch, so the same mechanism builds the
// rig a measurement needs and the twenty-product catalog a developer wants
// while waiting. [DefaultSpec] carries the surviving rig's own numbers.
type Spec struct {
	// SingleVariantProducts is the size of family B: products carrying one
	// variant, one price set, one TRY price and one stock level.
	SingleVariantProducts int
	// MultiVariantProducts is the size of family L: products carrying two
	// variants, and two prices (TRY and USD) per variant.
	MultiVariantProducts int
	// Categories is how many categories the generated products are spread over.
	// Zero builds no taxonomy at all.
	Categories int
	// Tags is how many tags the generated products are spread over. Zero builds
	// none.
	Tags int
	// SalesChannelID is the channel every GENERATED product is assigned to.
	//
	// It is REQUIRED, and that is not tidiness. The storefront's visibility rule
	// counts a product with no assignment as visible in every channel, so a
	// catalog seeded without a channel would take the wrong branch of the rule
	// on every read and the count query's subquery would probe an empty table —
	// a rig that looks right and measures a different plan. The four hand-made
	// products are the deliberate exception (see [Seed]).
	SalesChannelID string
}

// DefaultSpec returns the shape of the rig this repository's figures were
// measured on: 52,004 products, 54,000 variants, 58,000 prices.
//
// The channel is left empty on purpose. It is an identity rather than a shape,
// it differs per installation, and the caller has to mint or find one; see
// [Spec.SalesChannelID].
func DefaultSpec() Spec {
	return Spec{
		SingleVariantProducts: DefaultSingleVariantProducts,
		MultiVariantProducts:  DefaultMultiVariantProducts,
		Categories:            DefaultCategories,
		Tags:                  DefaultTags,
	}
}

// validate rejects a spec that would build a catalog nobody asked for.
//
// A negative count is refused rather than clamped to zero: generate_series over
// a negative bound returns no rows, so clamping would silently build an empty
// family and report success for a run that did nothing — the exact
// indistinguishability between "checked nothing" and "passed" this repository
// keeps paying for.
func (s Spec) validate() error {
	for _, field := range []struct {
		name  string
		value int
	}{
		{"SingleVariantProducts", s.SingleVariantProducts},
		{"MultiVariantProducts", s.MultiVariantProducts},
		{"Categories", s.Categories},
		{"Tags", s.Tags},
	} {
		if field.value < 0 {
			return errors.Invalid(codeInvalidSpec,
				"%s cannot be negative, got %d", field.name, field.value)
		}
	}

	if s.SingleVariantProducts == 0 && s.MultiVariantProducts == 0 {
		return errors.Invalid(codeInvalidSpec,
			"both product families are zero, so there would be nothing to build")
	}

	if strings.TrimSpace(s.SalesChannelID) == "" {
		return errors.Invalid(codeInvalidSpec,
			"SalesChannelID is required: a catalog seeded without a channel assignment is "+
				"visible in EVERY storefront and measures a different plan than the rig")
	}

	// The price of family B's last product is SingleVariantProducts * 100 minor
	// units and the price CHECK constraint caps an amount at 10^12. The bound
	// is stated here rather than left to the database, because a constraint
	// violation 40,000 rows into a transaction reports the row and not the
	// reason.
	const maxSingleVariantProducts = 10_000_000_000
	if s.SingleVariantProducts > maxSingleVariantProducts {
		return errors.Invalid(codeInvalidSpec,
			"SingleVariantProducts is capped at %d: the generated price is the product's "+
				"number times 100 and the price table refuses an amount above 10^12",
			maxSingleVariantProducts)
	}

	return nil
}

// TableCount is one table's row count.
type TableCount struct {
	// Table is the table's name.
	Table string
	// Rows is how many rows it holds, deleted ones included: the count is a
	// physical fact about the rig, not a query the storefront would make.
	Rows int64
}

// Counts is what the database holds, table by table, in the order of
// [countedTables].
type Counts []TableCount

// Of returns one table's row count, and -1 when the table is not one this
// package counts.
//
// The miss is a NEGATIVE number rather than a zero: zero is a legitimate answer
// (product_image really is empty in the rig) and a caller asserting "the
// catalog is not empty" against a mistyped table name would otherwise read the
// typo as an empty catalog and fail for the wrong reason.
func (c Counts) Of(table string) int64 {
	for _, entry := range c {
		if entry.Table == table {
			return entry.Rows
		}
	}

	return -1
}

// String renders the counts as one line per table.
func (c Counts) String() string {
	width := 0
	for _, entry := range c {
		width = max(width, len(entry.Table))
	}

	var b strings.Builder
	for _, entry := range c {
		fmt.Fprintf(&b, "  %-*s %d\n", width+2, entry.Table, entry.Rows)
	}

	return b.String()
}

// countedTables are the tables [Count] reports, in the order it reports them.
//
// The list is the ANSWER to "did the rebuild produce the rig", so it holds the
// nine tables that carry the load plus the taxonomy this package adds. The
// count SQL is generated from this list, so a table added here is counted with
// no second edit; there is no hand-written column list that could drift away
// from it.
var countedTables = []string{
	"product",
	"product_variant",
	"price_set",
	"price",
	"inventory_items",
	"inventory_levels",
	"link_product_sales_channel",
	"link_product_variant_price_set",
	"link_product_variant_inventory",
	"product_category",
	"product_category_map",
	"product_tag",
	"product_tag_map",
}

// ProductTable is the table a caller asserts a non-empty catalog against; it is
// exported so the assertion does not have to repeat a string this package
// already owns.
const ProductTable = "product"

// Seed builds the catalog and returns what the database holds afterwards.
//
// # It is idempotent, and that is structural rather than careful
//
// Every id it writes is derived from the row's number, and every statement ends
// in ON CONFLICT DO NOTHING against the PRIMARY KEY. A second run therefore
// inserts nothing and changes nothing: the ids collide, the rows are already
// the same values, and the counts come back identical. A seeder that silently
// doubled a catalog on its second run would be a measurement rig that lies, and
// the guard against that is the id, not a check.
//
// The conflict target is spelled out — ON CONFLICT (id) rather than a bare ON
// CONFLICT DO NOTHING. The bare form would also swallow a collision on
// product's unique HANDLE index, which is the one collision worth hearing
// about: it means the database already holds a different product occupying the
// rig's handle, and silently skipping that row would leave a rig short of one
// product with no sound made.
//
// # What it builds
//
// One stock location, two generated families, four hand-made products, and the
// taxonomy the spec asks for. Family B is prod_B<n> / buyuk-<n> with one
// variant, one price set, one TRY price of n*100 and 100 units of stock; family
// L is prod_L<n> / urun-<n> with two variants, each carrying a TRY and a USD
// price of n*100 + k*10 + c. Every generated product is assigned to
// [Spec.SalesChannelID].
//
// The four hand-made products carry NO variant and NO channel assignment, and
// both absences are the point. No assignment exercises the "a product with no
// channel at all is visible in EVERY storefront" branch of the visibility rule
// — the branch nothing else in the rig covers. Three of them carry Turkish
// diacritics, which is how the rig answers a question about the CLUSTER rather
// than about the catalog: on a database initialized with --locale=C they can
// never be matched by a lowercase ILIKE, and the surviving rig is exactly such
// a cluster (its own startup case-folding probe fails on it). A rebuild on a
// correctly initialized cluster matches them; one on the broken locale does
// not, so the rows are a probe an operator can run.
//
// # What it does NOT build, and why
//
//   - No search index. See this package's godoc.
//   - No product images, options or option values, and no collection. An image
//     is bound to an upload owned by the file module, and building one from SQL
//     would fabricate a file that does not exist on disk. The surviving rig has
//     none of these either.
//   - No orders, no payment sessions and no workflow executions. Three more
//     rigs stand behind this repository's figures — a 52,000-order fixture, a
//     52,000-execution fixture and a 200,000-session fixture — and all three are
//     already gone. This mechanism is shaped to grow them (another family, more
//     counts) but seeding them is not this change, and saying so is better than
//     leaving a reader to assume the catalog rebuild covered them.
//   - No API key and no sales channel. The plaintext of a publishable key
//     exists only in the return value of the call that mints it, so a key can
//     be minted but never restored; that is an identity decision and it belongs
//     to the caller, which has the auth service. It is also why a pg_dump of
//     the surviving rig would restore a storefront nobody can authenticate
//     against.
//
// # VACUUM ANALYZE is part of the build, and ANALYZE alone is not enough
//
// The statistics are refreshed before the call returns, because without them
// the planner picks a different shape and the rebuilt rig measures something
// the original did not. The surviving rig carries a manual ANALYZE from the day
// it was built (pg_stat_user_tables records it on all nine large tables), and
// reproducing the rows without reproducing the statistics would reproduce the
// catalog and not the measurement.
//
// The VACUUM half was found by measurement, on the first full rebuild, and it
// is the difference between a rig and a lookalike. An INDEX ONLY scan is only
// "only" where the visibility map says a page is all-visible, and nothing but a
// vacuum sets that bit; a freshly inserted table has it set nowhere. The
// storefront's count query is what this decides, because its subquery probes
// the channel link once per product. Measured on a full rebuild, same rows,
// same statement:
//
//	ANALYZE alone     Heap Fetches: 52,000   Buffers: shared hit=208,742
//	VACUUM (ANALYZE)  Heap Fetches: 0        Buffers: shared hit=156,743
//
// # The acceptance test of a rebuild
//
// The second line above is not merely better, it is the surviving rig's own
// recorded plan to the buffer: the count query's godoc in the product module
// records "Buffers: shared hit=156,743 (156,013 of that the subquery's)" and a
// "SubPlan 1 -> Index Only Scan ... (loops=52,004)". A rebuilt rig reproduces
// all four numbers exactly — 52,004 rows, 52,004 loops, Heap Fetches 0, 156,743
// shared hits of which 156,013 the subquery's — which is how a rebuild is
// CHECKED. The check is a plan and not a stopwatch on purpose: a timing
// assertion goes red on a slow machine and teaches the next reader to ignore
// it.
//
// Left un-vacuumed the same rebuild reports a third more buffer traffic than
// the catalog it is supposed to be, and every reader comparing a fresh
// measurement against a godoc would be comparing two different databases
// without being told.
func Seed(ctx context.Context, pool *db.Pool, spec Spec) (Counts, error) {
	if err := spec.validate(); err != nil {
		return nil, err
	}

	raw := pool.Pool()
	if raw == nil {
		return nil, errors.Unavailable(codeSeedFailed, "the database pool is not open")
	}

	// The whole catalog goes in ONE transaction. A rig that was half built is
	// worse than no rig: the counts would look plausible, the links would be
	// missing for the tail of the catalog, and the query plans measured on it
	// would be measured on a shape nobody described.
	tx, err := raw.Begin(ctx)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindUnavailable, codeSeedFailed,
			"the seed transaction could not be started")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, step := range seedSteps(spec) {
		if _, err := tx.Exec(ctx, step.sql, step.args...); err != nil {
			return nil, errors.Wrap(err, errors.KindOf(err), codeSeedFailed,
				"the seed step %q failed", step.name)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, errors.Wrap(err, errors.KindUnavailable, codeSeedFailed,
			"the seeded catalog could not be committed")
	}

	// It runs AFTER the commit and it HAS to: VACUUM is refused inside a
	// transaction block, and an ANALYZE inside one would sample rows no other
	// session can see yet and describe the table as it was before the seed.
	if _, err := raw.Exec(ctx, vacuumSQL()); err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), codeSeedFailed,
			"the seeded tables could not be vacuumed and analyzed; the rows are committed "+
				"but the planner statistics are stale and the visibility map is unset, so "+
				"every plan measured now is measured on the wrong shape")
	}

	return Count(ctx, pool)
}

// Reset deletes the rows [Seed] writes and nothing else.
//
// It is the destructive half of the pair and the caller is expected to have
// asked for it explicitly; this package does no confirming of its own, because
// the confirmation belongs where the operator is (see internal/app/seed.go).
//
// # It is SLOW, and the reason is in the modules' indexes
//
// Measured on the full rig: 4 m 31 s to delete what 13.6 s put there. The cost
// is not the rows, it is the foreign keys. Deleting one product makes
// PostgreSQL look for referencing rows in product_variant, and the index that
// would answer that question — product_variant_product_idx — is PARTIAL
// (WHERE deleted_at IS NULL), which a referential-integrity check will not use.
// So the check falls to a sequential scan, once per deleted parent: 52,000
// scans of a 54,000-row table. The same shape repeats for price against
// price_set (price_set_id_idx is partial) and for inventory_levels against
// inventory_items. Measured separately, the product delete alone is 81.8 s of
// it, and deleting the children by hand first does not rescue it — the parent
// delete still scans, and the total came out worse.
//
// Two remedies were rejected. Creating a non-partial index for the duration of
// the reset would mean a development tool mutating an installation's schema and
// leaving one behind if it crashed. TRUNCATE would be fast and would delete the
// installation's real catalog along with the rig's. What is left is to say the
// number out loud: a reset is a deliberate, rare act, the SEED is the operation
// that had to be fast, and it is.
//
// # Why the rows are matched by PATTERN and not by prefix
//
// The obvious spelling — delete every id starting with "prod_" — would delete
// THE ENTIRE CATALOG, the installation's real products included, because
// "prod_" is the product module's own id prefix and so are "pset_", "invitem_",
// "pcat_" and "ptag_". The rig's ids are the prefix followed by a family letter
// and DIGITS ONLY, while a generated id is 26 Crockford Base32 characters, so
// the anchored patterns separate the two.
//
// The patterns bound the digit run at twelve, and the bound is what makes the
// separation EXACT rather than merely improbable. Without it the two id shapes
// overlap in principle — a generated id whose 26 characters happened to be all
// digits would match — and "astronomically unlikely" is not the standard a
// statement that deletes a catalog should be held to. With it no generated id
// can match at any length, whatever its random half turns out to be, while the
// rig's own numbers (capped by [Spec.validate] at eleven digits) all fit.
//
// The letters help too, and it is worth knowing why rather than assuming it: a
// generated id can never begin with L at all, because Crockford Base32 omits
// I, L, O and U, and it cannot begin with B until the top five bits of its
// 48-bit millisecond timestamp reach eleven, which is somewhere around the year
// 5000.
//
// A full table scan per statement is the cost of that precision and it is
// accepted: a reset is a rare, deliberate act, and the alternative — a prefix
// that is fast and occasionally deletes a customer's catalog — is not a trade.
//
// The four hand-made products are matched by their literal ids. Sales channels
// and API keys are NOT touched: they are identities, and a command whose job is
// rows must not quietly revoke a key someone is using.
func Reset(ctx context.Context, pool *db.Pool) (Counts, error) {
	raw := pool.Pool()
	if raw == nil {
		return nil, errors.Unavailable(codeResetFailed, "the database pool is not open")
	}

	tx, err := raw.Begin(ctx)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindUnavailable, codeResetFailed,
			"the reset transaction could not be started")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, step := range resetSteps() {
		if _, err := tx.Exec(ctx, step.sql, step.args...); err != nil {
			return nil, errors.Wrap(err, errors.KindOf(err), codeResetFailed,
				"the reset step %q failed", step.name)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, errors.Wrap(err, errors.KindUnavailable, codeResetFailed,
			"the reset could not be committed")
	}

	return Count(ctx, pool)
}

// Count reports the row count of every table in [countedTables].
//
// It is exported because it is the only honest way to say what a run produced:
// what is reported is what the DATABASE holds afterwards, never the number that
// was requested. The two differ whenever a run was a no-op on an already seeded
// catalog, and a report built from the request would then describe work that
// did not happen.
func Count(ctx context.Context, pool *db.Pool) (Counts, error) {
	raw := pool.Pool()
	if raw == nil {
		return nil, errors.Unavailable(codeCountFailed, "the database pool is not open")
	}

	// One statement rather than one per table: the counts are read as a single
	// snapshot, so two of them cannot come from two different moments and
	// describe a catalog that never existed.
	targets := make([]string, 0, len(countedTables))
	values := make([]int64, len(countedTables))
	scan := make([]any, len(countedTables))
	for i, table := range countedTables {
		targets = append(targets, fmt.Sprintf("(SELECT count(*) FROM %s)", table))
		scan[i] = &values[i]
	}

	query := "SELECT " + strings.Join(targets, ", ")
	if err := raw.QueryRow(ctx, query).Scan(scan...); err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), codeCountFailed,
			"the rig's tables could not be counted")
	}

	counts := make(Counts, 0, len(countedTables))
	for i, table := range countedTables {
		counts = append(counts, TableCount{Table: table, Rows: values[i]})
	}

	return counts, nil
}

// DatabaseName returns the name of the database the pool is connected to.
//
// It is asked of the SERVER rather than parsed out of the DSN, and that is the
// whole point of it: the name is used as the confirmation a destructive run has
// to repeat, so it must be a fact about where the rows really are. A DSN can be
// overridden by PGDATABASE, by a connection service file or by a keyword form
// this repository does not parse, and a guard comparing the operator's word
// against a string nobody connected with would be guarding the wrong database.
func DatabaseName(ctx context.Context, pool *db.Pool) (string, error) {
	raw := pool.Pool()
	if raw == nil {
		return "", errors.Unavailable(codeCountFailed, "the database pool is not open")
	}

	var name string
	if err := raw.QueryRow(ctx, "SELECT current_database()").Scan(&name); err != nil {
		return "", errors.Wrap(err, errors.KindOf(err), codeCountFailed,
			"the database name could not be read")
	}

	return name, nil
}

// vacuumSQL builds the VACUUM (ANALYZE) statement over the tables the seed
// writes.
//
// It is generated from [countedTables] so that a table added to the rig is
// vacuumed, analyzed and counted from a single edit; a second hand-written list
// is the shape that goes stale without a sound.
func vacuumSQL() string {
	return "VACUUM (ANALYZE) " + strings.Join(countedTables, ", ")
}

// step is one statement of a seed or a reset, with the name that goes into the
// error message when it fails.
//
// The name exists because the statements are long and generated: a pgx error
// carries the SQL, not the intent, and "the seed step \"family B prices\"
// failed" is the difference between a diagnosis and a wall of text.
type step struct {
	name string
	sql  string
	args []any
}

// The rig's four created_at values.
//
// The surviving rig carries exactly four distinct product timestamps and these
// are them, to the microsecond. They are CONSTANTS rather than now() for a
// reason that is not neatness: the storefront's listing orders by (created_at
// DESC, id DESC), so the timestamps decide the page boundaries. Seeded with
// now(), every rebuild would produce a different deep-paging cursor and the
// 34.71 ms figure core/page records for "~50,000 rows in" would be measured
// over a different set of rows each time.
//
// The two hand-made batches are LATER than both families, so the four
// variant-less products sit at the top of the storefront's first page — which
// is where they were when the rig was measured.
var (
	familyLCreatedAt  = time.Date(2026, 9, 3, 8, 48, 39, 528698000, time.UTC)
	familyBCreatedAt  = time.Date(2026, 9, 3, 8, 51, 1, 878231000, time.UTC)
	freeProductTime   = time.Date(2026, 9, 3, 9, 4, 7, 301398000, time.UTC)
	diacriticRowsTime = time.Date(2026, 9, 3, 10, 19, 19, 295193000, time.UTC)
)
