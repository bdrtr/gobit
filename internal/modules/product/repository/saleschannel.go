package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository/productdb"
)

// This file holds the SALES CHANNEL filter of the product listing and the two
// queries that carry that filter (list + count).
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
//
// The rule was once written with TWO subqueries ("has no assignment at all OR
// has an assignment in the requested channel") and the comment here claimed
// that one index probe was done per candidate row. THE CLAIM WAS WRONG and its
// wrongness only showed at real volume: when the planner sees two independent
// EXISTS it turns both of them into a HASH, that is, it scans the whole link
// table twice BEFORE returning the first row. It was measured — 52,000 products
// and 52,000 channel assignments, the storefront's LIMIT 20 list query:
//
//	two EXISTS (old)           26.8 ms
//	single bool_or (today's)    0.8 ms
//
// The cost was growing not with the page size but with the CATALOG size, and on
// the storefront's hottest endpoint at that. A single correlated subquery makes
// the planner do an index probe per row and it can really stop at the LIMIT.
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
// And NO TEST can catch this — that was measured too. The channel array arrives
// here from Go as a []string, so a NULL element CANNOT BE PRODUCED today; when
// "IS TRUE" is deleted, all thirteen tests of the integration package keep
// passing (every other piece that was deleted — COALESCE's default, the admin
// branch, bool_or, the correlation, the direction of the equality — drops at
// least one test). That is, these two words lean on the caller's CURRENT type
// choice, and if that choice changes the rule silently loosens. That is the
// reason they are written; if they are to be removed, this line must be read
// first.

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
    OR COALESCE((
      SELECT bool_or(scl.to_id = ANY(%[2]s::text[]) IS TRUE)
      FROM ` + SalesChannelLinkTable + ` scl
      WHERE scl.from_id = %[1]s
    ), true)
  )`

// salesChannelVisible produces the visibility condition for the given product
// expression and channel parameter.
//
// It is called ONLY with CONSTANTS from inside the package; no string given by
// the caller enters here, the channel ids go to the SQL as parameters.
func salesChannelVisible(productExpr, channelsParam string) string {
	return fmt.Sprintf(salesChannelVisibleTemplate, productExpr, channelsParam)
}

// productFilterSQL is the SHARED filter body of the product listing and
// counting queries.
//
// Parameter order: $1 status, $2 collection_id, $3 handle, $4 search,
// $5 sales_channel_ids. The pagination parameters ($6, $7) exist only in the
// list query; that is why the count query can use the same body without
// changing it at all.
const productFilterSQL = `WHERE deleted_at IS NULL
  AND ($1::text IS NULL OR status = $1::text)
  AND ($2::text IS NULL OR collection_id = $2::text)
  AND ($3::text IS NULL OR handle = $3::text)
  AND ($4::text IS NULL OR title ILIKE '%' || $4::text || '%')
  AND `

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

// listProductsSQL reads the products matching the criteria, paginated.
//
// The order (created_at DESC, id DESC) is fixed; the second key prevents two
// records created in the same millisecond from changing places between pages.
// That same pair is what a cursor carries, which is why this listing could take
// one without changing its order at all.
//
// # Why the cursor bound is a sentinel and not a branch
//
// The obvious way to make the bound optional is
// "$9 IS NULL OR (created_at, id) < (...)". It measures perfectly and then
// degrades in production: Postgres plans the statement per call for its first
// five executions and folds the OR away, so a test sees an Index Cond; on the
// sixth it switches to a GENERIC plan, the OR survives into a Filter, and the
// seek becomes a full index walk. Measured on 52,000 rows at the deep end:
// 50,001 rows removed by filter, 4.3 ms instead of 0.065 ms — and nothing in
// the code changed at that moment, which is what makes it worth writing down.
//
// COALESCE leaves no OR to survive. The comparison stays a ROW against the
// index under both plans, and an absent cursor means "start at the top",
// because every real row sorts below infinity.
var listProductsSQL = `SELECT ` + productColumns + ` FROM product
` + productFilterSQL + salesChannelVisible("product.id", "$5") + `
  AND (created_at, id) < (COALESCE($8::timestamptz, 'infinity'::timestamptz), COALESCE($9::text, ''))
ORDER BY created_at DESC, id DESC
LIMIT $6::int OFFSET $7::int`

// countProductsSQL reads the TOTAL number of products matching the criteria.
//
// # This query IS EXPENSIVE and its shape MUST NOT BE CHANGED
//
// Because it has no LIMIT the planner cannot stop early: it walks the whole
// product table and does one index probe into the link table per row. It was
// measured on gobit_load (52,004 products, 52,000 channel assignments):
//
//	Aggregate (actual 70.655 ms)
//	  -> Seq Scan on product (rows=52,004)
//	       Filter: ... AND COALESCE((SubPlan 1), true)
//	       SubPlan 1 -> Index Only Scan ... (loops=52,004)
//	  Buffers: shared hit=156,743 (156,013 of that the subquery's)
//
// Two alternative shapes counting the same set were measured and BOTH WERE
// REJECTED: "two EXISTS" 43-54 ms, "GROUP BY + hash join" 33-45 ms. They are
// faster in the unfiltered case, but hashing the whole link table lays down a
// fixed ~30 ms floor; on a SELECTIVE filter (a "q" that matches a single
// product) today's shape is 13.8 ms and the hash shape 30.0 ms — that is, the
// trade changes direction. On top of that the list query IS OBLIGED to take
// this shape (the "The index, and why a SINGLE subquery" heading above) and the
// template is SHARED between the two; splitting the shape would create a second
// definition of the visibility rule.
//
// The count is O(catalog) and no SQL shape makes it sublinear. That is why the
// solution was sought not here but IN THE CALLER: the count is no longer run at
// all when it is not wanted (see service.ListProductsOptions.SkipCount).
var countProductsSQL = `SELECT count(*) FROM product
` + productFilterSQL + salesChannelVisible("product.id", "$5")

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
	afterAt, afterID := f.After.SQLBounds()

	rows, err := r.db.Query(ctx, listProductsSQL,
		f.Status, f.CollectionID, f.Handle, f.Search, f.SalesChannelIDs,
		toInt32(f.Limit), toInt32(f.Offset), afterAt, afterID)
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
	var n int64
	err := r.db.QueryRow(ctx, countProductsSQL,
		f.Status, f.CollectionID, f.Handle, f.Search, f.SalesChannelIDs).Scan(&n)
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
