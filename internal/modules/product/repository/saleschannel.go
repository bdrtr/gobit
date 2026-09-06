package repository

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository/productdb"
)

// This file holds the SALES CHANNEL filter of the product listing and the two
// queries that carry that filter (list + count).
//
// # Where the numbers in this file come from
//
// Every millisecond below was re-measured on 2026-09-06 and the ones that had
// changed were replaced in place, with the old figure struck rather than
// deleted. A struck line is how this file shows it is CHECKED rather than
// accumulated: a number nobody can find the argument against is a number
// nobody re-measures.
//
// The bench: container gobit-pg-utf8, PostgreSQL 16.14 (postgres:16-alpine),
// database gobit_orexists created C / C.UTF-8. That database PASSES both
// halves of the case-folding probe in core/db/casefold.go, which matters
// because one clause here is an ILIKE; the older container the historic
// figures came from fails it, and a cluster that folds ASCII only walks the
// same rows faster. The catalog is the rig `gobit seed` rebuilds with its
// default shape — 52,004 products, 52,000 channel assignments, 20 categories
// holding 2,600 products each and 20 tags holding 2,600 each — VACUUM
// (ANALYZE)d by the seeder itself.
//
// Two categories are NOT in that rig and were added by hand, because the
// default shape cannot produce the case that decides half of this file:
// internal/rig/catalog.go maps product n to category (n-1) mod 20, so every
// category holds exactly 5.0% of the catalog and a SELECTIVE category does not
// exist. The hand-made ones are pcat_TINY (26 products that are adjacent in
// the listing order) and pcat_SPREAD (27 products, every 2000th row of the
// listing order). Any figure quoted against them says so, and a reader must
// not repeat them as properties of the rig.
//
// Each figure is the MEDIAN of 9 to 11 warmed EXPLAIN (ANALYZE, BUFFERS)
// executions of a PREPAREd statement with plan_cache_mode left at auto, which
// is what pgx gets. The count figures spread 2-10% run to run; the
// sub-millisecond list figures have a noise floor near ±0.02 ms and should be
// read as "about 0.1 ms", not to three digits.
//
// # Why the filter is in the database
//
// The rule has two halves: a product that has NO assignment is visible in all
// channels, a product that HAS an assignment is visible only in the channels it
// is assigned to. The first half cannot be derived EFFICIENTLY with the
// ListManyByTo surface core/link offers — "the products assigned to these
// channels" arrives in a single query, but "the products with no assignment at
// all" can only be found by pulling all the bindings and taking the complement.
// That has two costs and the second one is lethal: as the catalog grows the
// whole link table enters memory and — the real issue — because LIMIT and
// OFFSET are applied in the database, a filtering done on the Go side fills the
// page short, and the total count would show the unfiltered set. That is, the
// listing would silently be paginated WRONG.
//
// That is why the filter enters the product's OWN query, as an
// EXISTS/NOT EXISTS condition against the link table; pagination and the count
// work over the filtered set.
//
// # This is NOT a cross-module foreign key
//
// The first person reading it rightly asks "is another module's table being
// touched here". No: [SalesChannelLinkTable] is not auth's but the table of the
// link PRODUCT declares (see service.LinkProductSalesChannel) and there is
// nothing inside it beyond two free id strings. The query sees none of auth's
// tables, no REFERENCES is added, and if auth changed its schema this condition
// would not be affected — the binding Principle 2.2 forbids is exactly this one
// and it is not established here.
//
// # Why the queries are hand-written
//
// sqlc reads the schema from the module's migration directory; the link table's
// schema, however, is set up by core/link at run time (link.Define), so it IS
// NOT in that directory and sqlc generation rejects it with "relation does not
// exist". Leaving a fake copy to introduce the schema to sqlc would mean
// defining core/link's schema a second time inside product; a copy can silently
// drift. That is why these two queries are written by hand and — more
// importantly — the sqlc counterparts HAVE BEEN DELETED: the filter has a
// single definition ([productFilterSQL]). Had two definitions remained, a
// filter added one day would be written into one and not into the other, and
// the storefront and the admin listing would SILENTLY return different sets.
//
// # The index, and why a SINGLE subquery
//
// No extra index IS NEEDED and product could not add one anyway (the schema is
// core/link's). For a ManyToMany link core/link sets up PRIMARY KEY (from_id,
// to_id) and a lookup index on to_id (see core/link registry.go ddl). The
// subquery starts with from_id, that is, it uses the prefix of the primary key.
// RE-CONFIRMED 2026-09-06: every plan taken for this file shows
// "Index Only Scan using link_product_sales_channel_pkey" with
// "Index Cond: (from_id = product.id)" and "Heap Fetches: 0".
//
// The rule was once written with TWO subqueries ("has no assignment at all OR
// has an assignment in the requested channel") and the comment here claimed
// that one index probe was done per candidate row. THE CLAIM WAS WRONG and its
// wrongness only showed at real volume: when the planner sees two independent
// EXISTS it turns both of them into a HASH, that is, it scans the whole link
// table twice BEFORE returning the first row. It was measured — 52,000 products
// and 52,000 channel assignments, the storefront's LIMIT 20 list query:
//
//	two EXISTS (old)           26.8 ms      <- struck 2026-09-06
//	single bool_or (today's)    0.8 ms      <- struck 2026-09-06
//
// RE-MEASURED 2026-09-06, same query, same catalog size, on the bench the file
// header describes:
//
//	two EXISTS (old)           21.4 ms   1,077 buffers
//	single bool_or (today's)    0.12 ms      68 buffers
//
// The old pair is struck and not deleted because the direction it reports is
// right and the MECHANISM it describes reproduces verbatim: the plan of the old
// shape is "Index Scan using product_created_at_idx" with
// "Filter: ((NOT (hashed SubPlan 2)) OR (hashed SubPlan 4))", and both sublinks
// are a full "Seq Scan on link_product_sales_channel" over 52,000 rows, 535
// buffers each, spent before the first row leaves the plan. What did not
// reproduce is either magnitude, and the second one is off by nearly 7x. The
// likeliest reading is that the two halves were not read off the same clock —
// the 0.8 ms has the shape of a client round trip and the 26.8 ms the shape of
// an EXPLAIN. Whichever pair a writer prefers, both halves have to come from
// one clock; that is how a 0.8 ms survived next to a 0.12 ms query.
//
// The cost was growing not with the page size but with the CATALOG size, and on
// the storefront's hottest endpoint at that. A single correlated subquery lets
// the planner do an index probe per row and really stop at the LIMIT.
//
// # IS TRUE is not decoration
//
// Without the "IS TRUE" inside bool_or the rule CHANGES: when the channel array
// carries a NULL element, "to_id = ANY(...)" returns NULL for that row, bool_or
// swallows the NULL, and COALESCE takes it for "has no assignment at all" and
// makes the product VISIBLE. That is, the version written short falls OPEN: a
// product assigned to a channel becomes visible to a request that does not ask
// for that channel. It was measured over eight scenarios; with "IS TRUE" it is
// identical to the old rule, without it the two diverge in two of them.
//
// And NO BEHAVIOR TEST can catch this — that was measured too. The channel
// array arrives here from Go as a []string, so a NULL element CANNOT BE
// PRODUCED today; when "IS TRUE" is deleted, the integration package stays
// green (every other piece that was deleted — COALESCE's default, the admin
// branch, bool_or, the correlation, the direction of the equality — drops at
// least one test). That is, these two words lean on the caller's CURRENT type
// choice, and if that choice changes the rule silently loosens. That is the
// reason they are written; if they are to be removed, this line must be read
// first.
//
// Two things in that paragraph moved and both are worth the line:
//
//	all thirteen tests of the integration package   <- struck 2026-09-06
//
// RE-MEASURED 2026-09-06 by deleting "IS TRUE" and running the package: 51
// top-level tests (62 counting subtests), 0 failures. The count was stale, the
// claim was not — a bigger suite catches it no better than the old one, which
// is the point.
//
// What DID change is that one test now fails, and a reader must not take it for
// protection. TestTheBuiltStatementStillReadsAsSQL asserts the exact text of
// the built statement, so deleting these two words breaks it — on the TEXT, not
// on the rule. It catches this edit and would catch no other way of loosening
// the same rule, and it fails saying "the SQL changed" rather than "a product
// became visible in the wrong channel". It is a tripwire on the string, not a
// test of the behavior, and the sentence above still holds for the behavior.
//
// # An absent criterion writes NO clause, and what that cost
//
// Until 2026-09-06 every criterion of the listing was written as
// "($n IS NULL OR <clause>)", so the statement was one fixed string whatever
// the request asked for. It read well and it was wrong at the one place it
// mattered. The OR is not decoration the planner can ignore: a sublink under a
// disjunction CANNOT be pulled up into a semi-join, so the product table stays
// the outer relation of every plan and the taxonomy map can only ever be a
// subplan hanging off a full scan of product. Measured, category filter, the
// SAME statement and the SAME data with only the category id changed:
//
//	pcat_TINY (26 adjacent)  Filter: (deleted_at IS NULL) AND (hashed SubPlan 2)
//	                                 AND COALESCE((SubPlan 3), true)
//	                         12.5 ms       812 buffers
//	pcat_SPREAD (27 spread)  Filter: (deleted_at IS NULL) AND COALESCE((SubPlan 3), true)
//	                                 AND (hashed SubPlan 2)
//	                        163.5 ms   156,746 buffers, channel subplan loops=52,004
//
// That is the finding, and it is worse than a slow number: the OR form's cost
// is not a figure, it is a coin the planner flips from statistics. Both plans
// are legal, both come from one statement, and which one is chosen decides
// whether the correlated channel subquery runs 27 times or 52,004 times. When
// it flips the wrong way a storefront category page becomes a 163 ms count and
// a 53 ms first page.
//
// Dropping the OR removes the choice. The bare EXISTS is pulled up, and
// product_category_map becomes the DRIVING relation: "Nested Loop" over
// "Bitmap Heap Scan on product_category_map" (27 rows) probing "product_pkey",
// with the channel subplan at loops=27. Medians, the bench in the file header,
// "+ch" meaning the sales channel filter is also applied:
//
//	                                  OR-wrapped          conditional     ratio
//	no criterion, count +ch       78.0 ms 156,743 buf   76.3 ms 156,743     1.0
//	no criterion, list  +ch        0.10 ms      68 buf   0.08 ms      68     1.0
//	no criterion, count admin      3.5 ms       43 buf   3.5 ms       43     1.0
//	category 2,600 (5%), count    13.2 ms    1,117 buf   8.0 ms    1,117     1.6
//	category 2,600 (5%), count+ch 20.2 ms    8,918 buf  10.3 ms   18,588     1.9
//	category 2,600 (5%), list      0.90 ms     473 buf   0.38 ms   1,272     2.4
//	category 2,600 (5%), list +ch  1.29 ms     534 buf   0.93 ms   2,458     1.4
//	category 26 adjacent, count+ch 12.5 ms     812 buf   0.16 ms     186      80
//	category 26 adjacent, list +ch  0.70 ms    911 buf   0.16 ms     186     4.4
//	category 27 spread, count      11.4 ms     733 buf   0.13 ms     111      85
//	category 27 spread, count +ch 163.5 ms 156,746 buf   0.28 ms     193     586
//	category 27 spread, list        4.9 ms   8,029 buf   0.15 ms     111      34
//	category 27 spread, list +ch   52.7 ms 122,033 buf   0.28 ms     193     188
//
// The tag filter mirrors the category filter at every point; only the index
// name in the plan differs (product_tag_map_tag_idx). The status, collection,
// handle and "q" criteria are INDISTINGUISHABLE between the two forms — the
// count with status='published' is 3.85 ms against 3.58 ms, the count with a
// "q" matching one product 20.4 ms against 20.7 ms, with a "q" matching almost
// everything 93.5 ms against 92.7 ms. They were made conditional anyway,
// because a rule with an exception in it is a rule the next reader has to
// re-derive; and the shape of the built statement is what keeps the planner
// from having to assume all seven criteria are live at once.
//
// Note the top three rows. This change is not a trade: at the request shape the
// storefront serves most often — no taxonomy criterion at all — the two forms
// produce the same plan, the same buffer counts and the same milliseconds.
// Nothing was given up to buy the rest of the table.
//
// # The generic plan, which is what this actually cost
//
// The OR form was SAFE from PostgreSQL's generic-plan trap, and for the same
// reason it was slow: with seven criteria that might all be live its generic
// plan is so expensive that plan_cache_mode=auto never adopts it, so the
// statement is re-planned from scratch on every call and the planner always
// sees the literal. A conditionally built statement is cheap enough that its
// generic plan CAN be adopted, and a generic plan cannot see which category was
// asked for. That is the one thing this change genuinely put at risk, so it was
// measured rather than reasoned about. pg_prepared_statements after 30
// executions of each statement, plan_cache_mode=auto:
//
//	OR-wrapped count                      0 generic / 30 custom
//	OR-wrapped list                       0 generic / 30 custom
//	built count, category                25 generic /  5 custom
//	built count, category + channel      25 generic /  5 custom
//	built count, no criterion            30 generic /  0 custom
//	built list, category                  0 generic / 30 custom
//	built list, category + channel        0 generic / 30 custom
//	built list, no criterion              0 generic / 30 custom
//	built list, no criterion, OR cursor   0 generic / 30 custom
//
// The COUNT does flip, and the flip costs nothing: 9.8 ms generic against
// 10.3 ms custom at the 5% category, 7.8 against 8.0 without the channel. At
// the scattered small category a FORCED generic plan is 0.30 ms for the count
// and 0.31 ms for the list — still 550x and 170x better than the OR form.
//
// The LIST does not flip, on any of the four shapes tried, including the
// simplest one. That is not luck: auto adopts the generic plan only when it is
// no dearer than the average custom plan, and the list's custom plans are far
// cheaper because the LIMIT can stop early once the planner knows the constant.
// Forced, the list's generic plan is 10.7 ms against 0.93 ms — so the risk is
// real and the reason it does not fire is a cost comparison, not a guarantee.
// If a future catalog ever makes those custom plans dear enough to lose that
// comparison, the symptom is a storefront list that got 10x slower with no code
// change, and the one query that names it is
// `SELECT name, generic_plans, custom_plans FROM pg_prepared_statements`.
//
// # What the built statement costs, honestly
//
// Three prices were paid and none of them is zero.
//
// THE PARAMETER NUMBERS STOPPED BEING FIXED. "$5 is the category" was a fact a
// reader could hold in their head; now the numbering depends on which criteria
// the request carried. Worse, the body is SHARED between the listing and the
// count, so the two statements must agree on it. The answer is structural
// rather than disciplinary: [productFilterSQL] returns the body AND the
// arguments it stands for as one pair, so there is no way to hold a body
// without the arguments that match it, and [listProductsSQL] appends its own
// four parameters after the body's, numbering them from len(args). A test pins
// that the two statements are handed the same numbering for the same filter.
//
// PREPARED-STATEMENT REUSE IS DIFFERENT, not worse. pgx's default mode is
// cache_statement: one named server-side statement per DISTINCT SQL TEXT, in an
// LRU of 512 per connection. The old shape produced exactly two texts and
// therefore two entries, forever. The new one produces one text per criterion
// COMBINATION actually asked for; [ProductFilter] has seven optional criteria,
// so 128 texts is the ceiling and the cache holds it four times over. What is
// paid is one Parse the first time a connection sees a shape — measured
// Planning Time for these statements is 0.08 to 0.21 ms — against the 4 ms to
// 163 ms per request the table above returns on the taxonomy paths.
//
// GREPPABILITY IS GONE and there is no clever answer to it. `grep "title
// ILIKE"` still lands on the clause, but no grep will ever print the STATEMENT
// again, and reading a query out of a builder is worse than reading it out of a
// string. Two things push back. Every clause is still a named constant with the
// SQL written out in it, so the pieces read as SQL. And a test asserts the
// EXACT text the builder produces for a representative filter, so the statement
// is still readable without running the program, and a change to it has to be
// made deliberately.
//
// # What would change the answer
//
// Two things, and both are checkable. If the LIST statements began adopting
// generic plans (the pg_prepared_statements query above), the storefront's
// hottest query would go from about 0.9 ms to about 10.7 ms at a 5% category
// while the OR form stayed at 1.3 ms, and the OR form would win that one row of
// the table. And if a deployment's categories really were uniform at 5% — the
// rig's own shape, and the shape under which none of this was ever visible —
// the whole change buys between 1.4x and 2.4x rather than between 34x and 586x.
// It is the SKEW that pays for this, and the rig has none.

// SalesChannelLinkTable is the link table where the product ↔ sales channel
// binding is kept.
//
// The name derives from core/link's contract ("link_" + link name) but IS
// WRITTEN BY HAND HERE: the link name is in the service package and the
// repository cannot import it (service already imports the repository). Because
// a constant repeated by hand can silently drift, a test in the service package
// binds the two names to each other.
const SalesChannelLinkTable = "link_product_sales_channel"

// salesChannelVisibleTemplate is the SQL counterpart of the sales channel
// visibility rule.
//
// %[1]s is the expression giving the product's id ("product.id" in the list
// query, "$1" in the single query), and %[2]s is the parameter carrying the
// channel ids. Being a template makes the rule stand in ONE place: the same
// text is used both in the paginated list and in the single visibility check,
// so the two cannot drift apart.
//
// The three branches cover these three cases in order:
//
//  1. If the parameter is NULL the request carries no sales channel id and the
//     filter is not applied at all (the admin listing goes through this
//     branch).
//  2. If the product has NO assignment at all it is visible in every channel
//     (backward compatibility: the existing catalog does not empty out
//     overnight).
//  3. If it does have an assignment it is visible only if it matches one of the
//     requested channels.
//
// An empty but non-NULL array never satisfies the third branch; on a request
// with no channel only the products without an assignment remain. This is
// deliberate: the definition of the match does not change, there is simply no
// channel left to match.
const salesChannelVisibleTemplate = `(
    %[2]s::text[] IS NULL
    OR ` + salesChannelAssignedTemplate + `
  )`

// salesChannelAssignedTemplate is the MATCHING half of the visibility rule, on
// its own: the two branches that decide whether a product with an assignment is
// sold in these channels, without the branch that asks whether a channel was
// named at all.
//
// It stands apart because the listing and the count no longer need that third
// branch. They write the channel clause only when the request carried channels
// (see [productFilterSQL]), so "$n IS NULL" there is a question the statement
// already knows the answer to — and leaving it in is what let the planner
// evaluate this correlated subquery over the whole catalog (the file header's
// "An absent criterion writes NO clause"). The three queries that ask about ids
// they were handed keep the full [salesChannelVisibleTemplate], because their
// callers really can pass nil and expect true.
//
// Splitting it this way is what keeps the rule SINGLE. The alternative was a
// second copy of the bool_or for the listing, and a second copy is how the
// storefront and the cart end up disagreeing about what is for sale.
const salesChannelAssignedTemplate = `COALESCE((
      SELECT bool_or(scl.to_id = ANY(%[2]s::text[]) IS TRUE)
      FROM ` + SalesChannelLinkTable + ` scl
      WHERE scl.from_id = %[1]s
    ), true)`

// salesChannelVisible produces the visibility condition for the given product
// expression and channel parameter.
//
// It is called ONLY with CONSTANTS from inside the package; no string given by
// the caller enters here, the channel ids go to the SQL as parameters.
func salesChannelVisible(productExpr, channelsParam string) string {
	return fmt.Sprintf(salesChannelVisibleTemplate, productExpr, channelsParam)
}

// salesChannelAssigned produces the matching condition WITHOUT the "no channel
// was named" branch.
//
// The same argument about constants applies as in [salesChannelVisible]: the
// two format arguments are expressions this package writes, never caller input.
func salesChannelAssigned(productExpr, channelsParam string) string {
	return fmt.Sprintf(salesChannelAssignedTemplate, productExpr, channelsParam)
}

// The clauses [productFilterSQL] can write, each with ONE placeholder for the
// value it compares against.
//
// They are constants rather than lines inside the builder for the reason the
// file header admits under "GREPPABILITY IS GONE": the statement can no longer
// be read out of a single string, so at least every clause of it has to be
// readable as SQL, in one place, with a name.
//
// # Why the taxonomy filters are EXISTS and not joins
//
// A product may sit in several categories and carry several tags, so a join
// multiplies the row: a product in three categories would come back three times,
// the page would hold fewer products than its limit says, and the count would be
// a number of MEMBERSHIPS rather than of products. EXISTS asks the only question
// being asked — is there at least one — and stops at the first match.
//
// Both map tables are keyed (product_id, x) with a second index on x alone.
// This comment used to conclude from that "so the subquery is an index probe per
// row: the same shape the sales channel filter already has". THAT WAS WRONG, and
// wrong twice. It was never one probe per row: with the category id known the
// planner hashes the whole subquery once per STATEMENT — "hashed SubPlan" over a
// single "Bitmap Index Scan on product_category_map_category_idx", 4 buffers,
// loops=1 — and probes nothing per row. And the shape is no longer the sales
// channel filter's at all: since the OR came off, this EXISTS is pulled up into
// a join and the map table DRIVES the plan ("Nested Loop" over
// "Bitmap Heap Scan on product_category_map" into "product_pkey"), which is the
// whole reason the taxonomy rows of the file header's table moved by 34x to
// 586x. The sales channel clause cannot be pulled up — bool_or under a COALESCE
// is not a semi-join — and stays a correlated subplan.
const (
	statusFilterSQL     = "\n  AND status = %s::text"
	collectionFilterSQL = "\n  AND collection_id = %s::text"
	handleFilterSQL     = "\n  AND handle = %s::text"
	searchFilterSQL     = "\n  AND title ILIKE '%%' || %s::text || '%%'"

	categoryFilterSQL = `
  AND EXISTS (
    SELECT 1 FROM product_category_map
    WHERE product_category_map.product_id = product.id
      AND product_category_map.category_id = %s::text
  )`

	tagFilterSQL = `
  AND EXISTS (
    SELECT 1 FROM product_tag_map
    WHERE product_tag_map.product_id = product.id
      AND product_tag_map.tag_id = %s::text
  )`
)

// productFilterSQL builds the SHARED filter body of the product listing and
// counting queries, together with the arguments its placeholders stand for.
//
// A criterion the request did not carry writes NO CLAUSE and consumes NO
// PARAMETER. The file header's "An absent criterion writes NO clause" is the
// measurement that bought this shape and the three prices it was bought at; the
// mechanics are here.
//
// # Why the body and the arguments come back together
//
// Because they cannot be kept in step any other way. The numbering is now a
// function of which criteria were given, and the SAME body is handed to the
// listing and to the count. Returning the SQL alone would leave every caller to
// rebuild the argument list in the same order by hand, and the day one of them
// got it wrong the failure would not be an error — pgx would send a category id
// where a handle was expected and the listing would quietly return nothing. The
// pair makes that unrepresentable: there is no body without its arguments.
//
// # Why nil-ness and not emptiness decides
//
// Every criterion is tested with "!= nil", never for an empty string or an
// empty slice, and that is the OLD behavior preserved exactly. Under the OR
// form a non-nil pointer to "" produced "title ILIKE '%%'", which matches every
// row, and a non-nil empty channel slice produced a filter that keeps only the
// products with no assignment at all ([ProductFilter.SalesChannelIDs] documents
// why that distinction is load-bearing). Deciding on emptiness here would have
// changed both, silently, in a commit whose subject line is about speed.
func productFilterSQL(f ProductFilter) (body string, args []any) {
	var clauses strings.Builder
	args = make([]any, 0, 7)

	// param records an argument and returns the placeholder that stands for
	// it. The number is the position in args, so the two cannot drift.
	param := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}

	clauses.WriteString("WHERE deleted_at IS NULL")

	if f.Status != nil {
		fmt.Fprintf(&clauses, statusFilterSQL, param(f.Status))
	}
	if f.CollectionID != nil {
		fmt.Fprintf(&clauses, collectionFilterSQL, param(f.CollectionID))
	}
	if f.Handle != nil {
		fmt.Fprintf(&clauses, handleFilterSQL, param(f.Handle))
	}
	if f.Search != nil {
		fmt.Fprintf(&clauses, searchFilterSQL, param(f.Search))
	}
	if f.CategoryID != nil {
		fmt.Fprintf(&clauses, categoryFilterSQL, param(f.CategoryID))
	}
	if f.TagID != nil {
		fmt.Fprintf(&clauses, tagFilterSQL, param(f.TagID))
	}
	if f.SalesChannelIDs != nil {
		clauses.WriteString("\n  AND " + salesChannelAssigned("product.id", param(f.SalesChannelIDs)))
	}

	return clauses.String(), args
}

// productColumns lists the columns of the product row IN THE ORDER of
// [productdb.Product]'s fields.
//
// # Why not "SELECT *"
//
// Rows are resolved by position (pgx.RowToStructByPos) and "SELECT *" leaves
// the column order to the TABLE's physical layout. If that layout shifts one
// day — inserting a column in between is enough — the mapping shifts, and the
// shift is SILENT: in this table handle and title are adjacent and both are
// text; subtitle/description/thumbnail are three text; weight/length/height/
// width are four integer. Two neighboring columns of the same type changing
// places produces no error at all, it merely swaps every product's title with
// its handle. Had the NUMBER of columns changed we would get an error; the real
// danger is not the number but the ORDER.
//
// The named list closes that road: a column inserted in between does not enter
// here and therefore shifts nothing, while a column that is deleted or renamed
// drops the query noisily.
//
// The single remaining risk is the field order sqlc generates drifting from
// this list; the product package's integration test pins that down by writing a
// DISTINGUISHABLE value into every field and reading it back.
const productColumns = `id, handle, title, subtitle, description, thumbnail,
	status, is_giftcard, discountable, weight, length, height, width,
	material, origin_country, collection_id, metadata,
	created_at, updated_at, deleted_at`

// keysetSeek is the ordering half of the listing: the comparison the cursor
// rides and the ORDER BY it has to agree with.
//
// The two are returned TOGETHER on purpose. They are one decision written in
// two places, and a seek written for one direction beside an ORDER BY written
// for the other does not fail — it returns a page that is neither, which reads
// as missing rows rather than as an error.
//
// # Both orders ride ONE index
//
// `product_created_at_idx` is declared `(created_at DESC, id DESC)` and a
// b-tree is readable in either direction, so the older-first order is the same
// index walked backwards. Neither order costs a migration and neither degrades
// to a sort of the whole table.
//
// # The sentinel flips with the direction
//
// The absent cursor means "start at the top", and which end that is depends on
// the direction: newest-first starts below +infinity, oldest-first starts above
// -infinity. The id sentinel stays the empty string in both, because the row
// comparison is already decided by the timestamp against an infinity — no real
// row ties with one.
//
// The form is a ROW COMPARISON against a COALESCE sentinel rather than an OR
// branch, and the reason is measured in the [corepage] godoc: the branch folds
// away for the first five executions and survives into a Filter on the sixth,
// when the statement goes generic.
func keysetSeek(order models.ProductOrder, afterAtParam, afterIDParam string) (seek, orderBy string) {
	if order == models.ProductOrderOldest {
		return "  AND (created_at, id) > (COALESCE(" + afterAtParam + "::timestamptz, '-infinity'::timestamptz), COALESCE(" + afterIDParam + "::text, ''))",
			"ORDER BY created_at ASC, id ASC"
	}

	return "  AND (created_at, id) < (COALESCE(" + afterAtParam + "::timestamptz, 'infinity'::timestamptz), COALESCE(" + afterIDParam + "::text, ''))",
		"ORDER BY created_at DESC, id DESC"
}

// listProductsSQL builds the paginated listing for the given criteria and the
// arguments it takes.
//
// The ordering KEY (created_at, id) is fixed and the DIRECTION is not: the
// second key prevents two records created in the same millisecond from changing
// places between pages, and [keysetSeek] reads that one key from either end.
// That same pair is what a cursor carries, which is why this listing could take
// one without changing its key at all — and why the order it was minted under
// has to travel with it (see service.ProductListingFor).
//
// Its four own parameters — limit, offset and the two halves of the cursor —
// are numbered AFTER whatever [productFilterSQL] used, which is the whole
// reason they are computed from len(args) rather than written as literals.
//
// # Why the cursor bound is a sentinel and not a branch
//
// The obvious way to make the bound optional is
// "$n IS NULL OR (created_at, id) < (...)". It measures perfectly under a
// custom plan and collapses under a generic one: the OR survives into a Filter
// and the seek becomes a full index walk. RE-MEASURED 2026-09-06 at the deep
// end of the rig, cursor at offset 50,000 of 52,004, plan_cache_mode =
// force_generic_plan:
//
//	rejected OR bound   Rows Removed by Filter: 50001   2.8 ms   432 buffers
//	COALESCE sentinel   Index Cond: (ROW(created_at, id) < ROW(...))
//	                                                    0.036 ms   4 buffers
//
// "50,001 rows removed" is EXACT, and so is the mechanism. Two things in the
// old note were not:
//
//	4.3 ms instead of 0.065 ms   <- struck 2026-09-06; measured 2.8 ms vs 0.036 ms
//
// and, more importantly, the claim that on the sixth execution the statement
// "switches to a GENERIC plan". It does not, and that was measured rather than
// assumed: after 30 executions under plan_cache_mode=auto, FOUR list shapes —
// including the simplest one and including a variant carrying this very OR
// bound — all report 0 generic plans and 30 custom ones (the table in the file
// header, under "The generic plan, which is what this actually cost"). Postgres
// adopts a generic plan only when it is no dearer than the average custom one,
// and for a statement whose LIMIT can stop early it never is.
//
// So the sentinel guards against a mode this database is NOT choosing today. It
// is kept anyway, and the reason is not superstition: what protects the OR
// bound is a cost comparison over statistics, not a property of the statement,
// and it is free to change under a different catalog with nothing in this
// repository changing. The sentinel costs nothing to keep — 0.028 ms against
// 0.027 ms for the OR bound under a custom plan — and it is the only form that
// keeps its Index Cond under BOTH plan modes.
func listProductsSQL(f ProductFilter) (query string, args []any) {
	body, args := productFilterSQL(f)
	afterAt, afterID := f.After.SQLBounds()

	n := len(args)
	limitParam := "$" + strconv.Itoa(n+1)
	offsetParam := "$" + strconv.Itoa(n+2)
	afterAtParam := "$" + strconv.Itoa(n+3)
	afterIDParam := "$" + strconv.Itoa(n+4)
	args = append(args, toInt32(f.Limit), toInt32(f.Offset), afterAt, afterID)

	seek, orderBy := keysetSeek(f.Order, afterAtParam, afterIDParam)

	return `SELECT ` + productColumns + ` FROM product
` + body + `
` + seek + `
` + orderBy + `
LIMIT ` + limitParam + `::int OFFSET ` + offsetParam + `::int`, args
}

// countProductsSQL builds the TOTAL count for the given criteria and the
// arguments it takes.
//
// It is [productFilterSQL]'s body with a count(*) in front and nothing else,
// which is what makes "the count and the listing filter the same set" a
// property of the code rather than a promise in a comment.
//
// # This query IS EXPENSIVE when no criterion narrows it
//
// With no criterion the planner cannot stop early: it walks the whole product
// table and does one index probe into the link table per row. Measured on
// gobit_load (52,004 products, 52,000 channel assignments):
//
//	Aggregate (actual 70.655 ms)
//	  -> Seq Scan on product (rows=52,004)
//	       Filter: ... AND COALESCE((SubPlan 1), true)
//	       SubPlan 1 -> Index Only Scan ... (loops=52,004)
//	  Buffers: shared hit=156,743 (156,013 of that the subquery's)
//
// RE-CONFIRMED 2026-09-06 on the rebuilt rig: rows=52,004, loops=52,004,
// Heap Fetches 0, "Buffers: shared hit=156743" of which 156,013 the subplan's —
// every structural number to the digit, and the conditionally built statement
// produces the identical plan and the identical buffer counts. The millisecond
// did not reproduce and is not struck, because it is a machine figure rather
// than a wrong one: the Aggregate's actual time was 75.2 ms and Execution Time
// ran 72.9-78.5 ms in one session and 75.7-82.1 ms in another. 4.1 ms of that
// is JIT compilation, which the old note does not mention and which this
// statement crosses the cost threshold for. A defensible form is "about 75 ms
// on this bench, of which 4 ms is JIT".
//
// Two alternative shapes counting the same set were measured and BOTH WERE
// REJECTED. RE-MEASURED 2026-09-06, medians of 9, the channel filter applied,
// "selective" meaning a q that matches ONE product:
//
//	                        no criterion        selective q
//	correlated (today's)   74.6 ms (72.9-78.5)  20.3 ms
//	two EXISTS             49.5 ms (47.0-55.4)  20.8 ms
//	GROUP BY + hash join   45.5 ms (42.3-48.3)  39.0 ms
//
// against the recorded "two EXISTS 43-54 ms, GROUP BY + hash join 33-45 ms" and
// "on a selective filter today's shape is 13.8 ms and the hash shape 30.0 ms".
// The two EXISTS band RE-CONFIRMS. The other three do not:
//
//	GROUP BY + hash join 33-45 ms   <- struck; measured 42-48, median on the old ceiling
//	a fixed ~30 ms floor            <- struck; measured ~39 ms
//	13.8 ms vs 30.0 ms selective    <- struck; measured 20.3 ms vs 39.0 ms
//
// The ARGUMENT is not withdrawn, because the argument was never the absolute
// numbers: it is that the trade CHANGES DIRECTION, and it does. Hashing the
// whole link table lays down a floor the filter cannot get under — the hash
// shape costs 45.5 ms counting everything and still 39.0 ms counting one — so
// it is 1.6x faster than today's shape unfiltered and 1.9x slower the moment a
// criterion is selective. The old text implied a 2x win unfiltered and a 30 ms
// floor; both are overstated, and the direction change survives both
// corrections. On top of that the list query IS OBLIGED to take this shape (the
// "The index, and why a SINGLE subquery" heading above) and the body is SHARED
// between the two; splitting the shape would create a second definition of the
// visibility rule.
//
// The count is O(catalog) IN THE UNFILTERED CASE and no SQL shape makes that
// sublinear, which is why the solution was sought not here but IN THE CALLER:
// the count is no longer run at all when it is not wanted (see
// service.ListProductsOptions.SkipCount). What is no longer true is the old
// heading's "its shape MUST NOT BE CHANGED" read as "every count is O(catalog)".
// With a category or a tag given, the map table now drives the plan and the
// count reads 111 buffers instead of 156,743 (the file header's table).
func countProductsSQL(f ProductFilter) (query string, args []any) {
	body, args := productFilterSQL(f)
	return "SELECT count(*) FROM product\n" + body, args
}

// visibleProductIDsSQL returns, out of the given ids, the ones that are VISIBLE
// in the channels.
//
// The reason it is asked in bulk is concrete: search brings tens of ids at a
// time, and asking for visibility per id means as many round trips as there are
// results. This store lives in an architecture that structurally keeps N+1 out
// (see core/query), and bringing it back on the search path would mean setting
// up the most expensive access pattern at the hottest endpoint.
//
// The rule comes from [salesChannelVisibleTemplate], that is, it is the SAME
// definition as the single query; had a second copy been made, then when one of
// them changed, search and the storefront would silently drift apart.
var visibleProductIDsSQL = `SELECT id FROM product
WHERE id = ANY($1::text[]) AND deleted_at IS NULL AND ` +
	salesChannelVisible("product.id", "$2")

// visibleVariantIDsSQL returns, out of the given VARIANT ids, the ones that are
// visible in the channels.
//
// # Visibility is a property not of the variant but of the PRODUCT
//
// The channel assignment is made to the product ([SalesChannelLinkTable]
// carries a product id) and the variant inherits it. That is why the variant's
// product_id column is given to the template as the product expression; the
// rule is not rewritten, the same [salesChannelVisibleTemplate] is instantiated
// with a second expression. There IS NO separate assignment for a variant and
// there must not be: there is no such notion as one variant of the same product
// being sold in one storefront and another in a different one — the storefront
// shows the product, the variant is one of its options.
//
// # Why it exists
//
// The channel scope was once applied ONLY on the read surface: the list, the
// count, the single endpoint and the bulk read all went through the template
// here, but the WRITE path that adds a line to the cart read the variant by id,
// without a filter. That is, a client arriving with channel B's publishable key
// could buy a variant sold only in channel A simply by writing its id into the
// request body. This query binds that path's question ("is this variant visible
// in my channels") to the same definition; rewriting the rule on the Go side —
// pulling the bindings of the variant's product and looking at the intersection
// — would be a second definition, and the day it drifted, the storefront would
// hide a product while the cart kept selling it.
//
// # Why in bulk
//
// The rationale is the same as [visibleProductIDsSQL]'s and is not repeated.
var visibleVariantIDsSQL = `SELECT v.id FROM product_variant v
WHERE v.id = ANY($1::text[]) AND v.deleted_at IS NULL AND ` +
	salesChannelVisible("v.product_id", "$2")

// productVisibleSQL asks whether a single product is visible in the given
// channels.
//
// The single storefront endpoint uses this query. Rewriting the rule on the Go
// side was possible (reading the product's bindings and looking at the
// intersection) but then the same rule would be expressed in two separate
// places, and when one of them changed the list and the single endpoint would
// drift apart — the single endpoint showing a product the list hides makes the
// hiding completely meaningless.
var productVisibleSQL = `SELECT ` + salesChannelVisible("$1", "$2")

// ListProducts returns the products matching the criteria, paginated.
//
// Rows are resolved BY POSITION (pgx.RowToStructByPos). Resolving by name would
// not work here: the field name is CollectionID, the column name is
// collection_id and pgx wants a tag to match the two; sqlc generation writes no
// tag. The price of resolving by position is the dependence on the order, and
// that dependence is written out explicitly in [productColumns] and pinned down
// by a test.
func (r *Repo) ListProducts(ctx context.Context, f ProductFilter) ([]models.Product, error) {
	query, args := listProductsSQL(f)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, wrapDB(err, "could not list products")
	}
	// CollectRows closes the rows and folds rows.Err() into the result too; if
	// there are no rows at all it returns an empty slice (not nil), so that in
	// JSON it becomes "[]" and not "null".
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[productdb.Product])
	if err != nil {
		return nil, wrapDB(err, "could not list products")
	}
	return toProducts(list)
}

// CountProducts returns the TOTAL number of products matching the criteria.
//
// The number is the source of the pagination envelope ("count") and is
// INDEPENDENT of limit/offset; only this way can the client know how many pages
// there are. The sales channel filter is applied here as well: had the count
// shown the unfiltered set, the storefront client would ask for pages that
// never fill.
func (r *Repo) CountProducts(ctx context.Context, f ProductFilter) (int, error) {
	query, args := countProductsSQL(f)

	var n int64
	err := r.db.QueryRow(ctx, query, args...).Scan(&n)
	if err != nil {
		return 0, wrapDB(err, "could not read product count")
	}
	return int(n), nil
}

// ProductVisibleInSalesChannels reports whether the product is visible in the
// given sales channels.
//
// If salesChannelIDs is nil the result is always true (the request carries no
// channel id); the query still goes to the database, because the only place
// that gives the verdict must be [salesChannelVisibleTemplate]. If the caller
// wants to avoid the needless round trip, it short-circuits the nil case
// itself.
func (r *Repo) ProductVisibleInSalesChannels(
	ctx context.Context,
	productID string,
	salesChannelIDs []string,
) (bool, error) {
	var visible bool
	if err := r.db.QueryRow(ctx, productVisibleSQL, productID, salesChannelIDs).Scan(&visible); err != nil {
		return false, wrapDB(err, "could not read the product's sales channel visibility: %s", productID)
	}
	return visible, nil
}

// VisibleProductIDs returns, out of the given ids, the ones visible in the
// channels in a SINGLE query.
//
// The result is returned as a set because the caller's only need is the
// membership question; had a slice been returned every caller would build its
// own map, and the ordering would look like a meaning the slice does not carry
// — the order of the request is in the caller's hands.
func (r *Repo) VisibleProductIDs(
	ctx context.Context,
	productIDs []string,
	salesChannelIDs []string,
) (map[string]struct{}, error) {
	return r.visibleIDs(ctx, visibleProductIDsSQL, productIDs, salesChannelIDs, "product")
}

// VisibleVariantIDs returns, out of the given variant ids, the ones visible in
// the channels in a SINGLE query.
//
// The flow that adds a line to the cart asks this path for the variant's
// channel scope over the Query layer (see service/provider.go). The rule itself
// and why the product is asked about rather than the variant are in the
// documentation of [visibleVariantIDsSQL].
//
// The result being a set, and an empty input not making a round trip, are for
// the same reasons as in [Repo.VisibleProductIDs].
func (r *Repo) VisibleVariantIDs(
	ctx context.Context,
	variantIDs []string,
	salesChannelIDs []string,
) (map[string]struct{}, error) {
	return r.visibleIDs(ctx, visibleVariantIDsSQL, variantIDs, salesChannelIDs, "variant")
}

// visibleIDs runs a bulk visibility query and turns the returned ids into a
// MEMBERSHIP set.
//
// The two bulk queries ([visibleProductIDsSQL] and [visibleVariantIDsSQL])
// differ only in which table they select from; their sharing the body makes a
// silent drift — one of them forgetting rows.Err() one day, or making a
// needless round trip on empty input — impossible. The rule is already in a
// single template; this makes the path that CALLS the rule single as well.
//
// kind is the entity name that goes into the error message ("product",
// "variant"); deriving the message from the query would tie the text the
// operator sees to the shape of the SQL.
func (r *Repo) visibleIDs(
	ctx context.Context,
	sql string,
	ids []string,
	salesChannelIDs []string,
	kind string,
) (map[string]struct{}, error) {
	if len(ids) == 0 {
		return map[string]struct{}{}, nil
	}

	rows, err := r.db.Query(ctx, sql, ids, salesChannelIDs)
	if err != nil {
		return nil, wrapDB(err, "could not read %s visibility (%d ids)", kind, len(ids))
	}

	found, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, wrapDB(err, "could not read %s visibility (%d ids)", kind, len(ids))
	}

	visible := make(map[string]struct{}, len(found))
	for _, id := range found {
		visible[id] = struct{}{}
	}

	return visible, nil
}
