package rig

import "time"

// The identifiers and the literal text of the rig.
//
// These strings are DATA, not prose: they are copied character for character
// out of the catalog measured on 2026-09-03 so that a rebuilt rig diffs clean
// against the surviving one, row for row. The titles in particular are load
// bearing — the storefront's "q" filter is `title ILIKE '%q%'`, and the 13.8 ms
// figure recorded for a selective filter was measured against a q that matched
// exactly one of these titles. Renaming them into English would leave every
// structural claim intact and quietly invalidate that one.
const (
	// stockLocationID is the single warehouse every generated stock level sits
	// at. Its id is referenced by 54,000 inventory_levels rows in the surviving
	// rig, which is why it is reproduced rather than generated.
	stockLocationID = "sloc_L1"
	// stockLocationName is that warehouse's name in the rig.
	stockLocationName = "Ana Depo"
)

// handMadeProduct is one of the four products that were placed in the rig by
// hand rather than generated.
type handMadeProduct struct {
	id     string
	handle string
	title  string
	// createdAt is written per row because these four arrived in two separate
	// batches and the rig's product table carries exactly four distinct
	// timestamps; collapsing them into one would change the storefront's first
	// page.
	createdAt time.Time
}

// handMadeProducts are the four variant-less products of the rig.
//
// # Why they carry no variant and no channel
//
// No variant makes them the only products in the catalog whose enrichment leg
// returns nothing, and no channel assignment puts them on the "visible in EVERY
// storefront" branch of the sales-channel rule — the branch the 52,000 assigned
// products never take.
//
// # Why the diacritics are written as escapes
//
// The three Turkish titles are the rig's probe for the CLUSTER, not for the
// catalog: on a database initialized with --locale=C the Turkish capitals do
// not fold to lower case at all, so a lowercase ILIKE can never match them —
// which is exactly the state the surviving rig is in, and the state its own
// startup case-folding probe reports. They have to be here for a rebuild to be
// able to answer that question about a new cluster.
//
// They are spelled with \u escapes because the language gate in internal/arch
// reads the RAW BYTES of a file for Turkish letters, and a new file is required
// to be English. The alternative was to add this file to that gate's data
// exemption list, which would mean editing the gate to fit the code — the wrong
// direction, and the exemption list is not this change's to edit. The escape
// keeps the bytes English and the DATA exactly what it has to be.
var handMadeProducts = []handMadeProduct{
	{id: "prod_FREE", handle: "serbest", title: "Serbest", createdAt: freeProductTime},
	// The three escaped titles read, in order: C-cedilla + "anta"; "G" +
	// O-diaeresis + "MLEK"; dotted-capital-I + "pek " + S-cedilla + "al".
	{id: "prod_TR1", handle: "canta", title: "\u00c7anta", createdAt: diacriticRowsTime},
	{id: "prod_TR2", handle: "gomlek", title: "G\u00d6MLEK", createdAt: diacriticRowsTime},
	{id: "prod_TR3", handle: "ipek", title: "\u0130pek \u015eal", createdAt: diacriticRowsTime},
}

// stockedQuantity is how many units every generated stock level carries.
//
// A hundred, and nothing reserved, so the sellable quantity equals the stocked
// quantity everywhere: the rig is a READ fixture and a checkout run against it
// must not be able to exhaust a variant and change what the next measurement
// sees.
//
// That last sentence is not a worry, it is a measurement. A rebuilt catalog was
// compared against the surviving rig column by column: the product, variant and
// price tables come back with IDENTICAL checksums over every row, and
// inventory_levels differs in exactly two rows out of 54,000 — invlvl_B2 and
// invlvl_B3 sit at 99 because two checkouts were run against the rig months
// after it was built. The stock is the one part of a read fixture that ordinary
// use can move, and the rig it moved away from was the one the figures were
// taken on.
const stockedQuantity = 100

// seedSteps builds the statement ledger for a spec.
//
// Each statement inserts a whole family in ONE round trip out of
// generate_series, which is what makes the rebuild take seconds rather than the
// hours a call-per-product seeder would take. The order is the order the
// foreign keys require: the warehouse before the levels that stand in it, the
// price sets before their prices, the products before their variants, the
// categories before the rows that map into them.
func seedSteps(spec Spec) []step {
	steps := []step{
		{
			name: "stock location",
			sql: `INSERT INTO stock_locations (id, name, created_at, updated_at)
VALUES ($1, $2, $3, $3)
ON CONFLICT (id) DO NOTHING`,
			args: []any{stockLocationID, stockLocationName, familyLCreatedAt},
		},
	}

	steps = append(steps, taxonomySteps(spec)...)
	steps = append(steps, singleVariantSteps(spec)...)
	steps = append(steps, multiVariantSteps(spec)...)

	return append(steps, handMadeStep())
}

// taxonomySteps creates the categories and the tags the generated products are
// spread over.
//
// The maps themselves are written by the family steps, because the mapping is a
// property of the product's number: product n lands in category (n-1) mod C.
// The spread is deterministic, so the same n is in the same category on every
// rebuild and a filter measured once can be measured again.
func taxonomySteps(spec Spec) []step {
	var steps []step

	if spec.Categories > 0 {
		steps = append(steps, step{
			name: "categories",
			sql: `INSERT INTO product_category (id, name, handle, rank, created_at, updated_at)
SELECT 'pcat_' || n, 'Category ' || n, 'category-' || n, n, $2, $2
FROM generate_series(1, $1::int) AS n
ON CONFLICT (id) DO NOTHING`,
			args: []any{spec.Categories, familyLCreatedAt},
		})
	}

	if spec.Tags > 0 {
		steps = append(steps, step{
			name: "tags",
			sql: `INSERT INTO product_tag (id, value, created_at, updated_at)
SELECT 'ptag_' || n, 'tag-' || n, $2, $2
FROM generate_series(1, $1::int) AS n
ON CONFLICT (id) DO NOTHING`,
			args: []any{spec.Tags, familyLCreatedAt},
		})
	}

	return steps
}

// singleVariantSteps builds family B: prod_B<n>, one variant, one TRY price of
// n*100 minor units, one stock level.
//
// It is the bulk of the catalog and therefore the family that decides what the
// storefront's count query costs: the count has no LIMIT, so it walks every
// product row and probes the channel link once per row.
func singleVariantSteps(spec Spec) []step {
	count := spec.SingleVariantProducts
	if count == 0 {
		return nil
	}

	when := familyBCreatedAt
	steps := []step{
		{
			name: "family B products",
			sql: `INSERT INTO product (id, handle, title, status, created_at, updated_at)
SELECT 'prod_B' || n, 'buyuk-' || n, 'Buyuk Urun ' || n, 'published', $2, $2
FROM generate_series(1, $1::int) AS n
ON CONFLICT (id) DO NOTHING`,
			args: []any{count, when},
		},
		{
			name: "family B variants",
			sql: `INSERT INTO product_variant (id, product_id, title, sku, rank, created_at, updated_at)
SELECT 'var_B' || n, 'prod_B' || n, 'tek', 'SKU-B-' || n, 1, $2, $2
FROM generate_series(1, $1::int) AS n
ON CONFLICT (id) DO NOTHING`,
			args: []any{count, when},
		},
		{
			name: "family B price sets",
			sql: `INSERT INTO price_set (id, created_at, updated_at)
SELECT 'pset_B' || n, $2, $2
FROM generate_series(1, $1::int) AS n
ON CONFLICT (id) DO NOTHING`,
			args: []any{count, when},
		},
		{
			name: "family B prices",
			sql: `INSERT INTO price (id, price_set_id, currency_code, amount, min_quantity, created_at, updated_at)
SELECT 'price_B' || n, 'pset_B' || n, 'TRY', n::bigint * 100, 1, $2, $2
FROM generate_series(1, $1::int) AS n
ON CONFLICT (id) DO NOTHING`,
			args: []any{count, when},
		},
		{
			name: "family B inventory items",
			sql: `INSERT INTO inventory_items (id, sku, requires_shipping, created_at, updated_at)
SELECT 'invitem_B' || n, 'SKU-B-' || n, true, $2, $2
FROM generate_series(1, $1::int) AS n
ON CONFLICT (id) DO NOTHING`,
			args: []any{count, when},
		},
		{
			name: "family B stock levels",
			sql: `INSERT INTO inventory_levels
    (id, inventory_item_id, location_id, stocked_quantity, reserved_quantity, created_at, updated_at)
SELECT 'invlvl_B' || n, 'invitem_B' || n, $3, $4::bigint, 0, $2, $2
FROM generate_series(1, $1::int) AS n
ON CONFLICT (id) DO NOTHING`,
			args: []any{count, when, stockLocationID, stockedQuantity},
		},
		{
			name: "family B variant to price set links",
			sql: `INSERT INTO link_product_variant_price_set (from_id, to_id, created_at)
SELECT 'var_B' || n, 'pset_B' || n, $2
FROM generate_series(1, $1::int) AS n
ON CONFLICT (from_id, to_id) DO NOTHING`,
			args: []any{count, when},
		},
		{
			name: "family B variant to inventory links",
			sql: `INSERT INTO link_product_variant_inventory (from_id, to_id, created_at)
SELECT 'var_B' || n, 'invitem_B' || n, $2
FROM generate_series(1, $1::int) AS n
ON CONFLICT (from_id, to_id) DO NOTHING`,
			args: []any{count, when},
		},
		{
			name: "family B sales channel assignments",
			sql: `INSERT INTO link_product_sales_channel (from_id, to_id, created_at)
SELECT 'prod_B' || n, $3, $2
FROM generate_series(1, $1::int) AS n
ON CONFLICT (from_id, to_id) DO NOTHING`,
			args: []any{count, when, spec.SalesChannelID},
		},
	}

	return append(steps, mapSteps("family B", "'prod_B' || n", spec, count, when)...)
}

// multiVariantSteps builds family L: prod_L<n> with two variants ("250g" and
// "1kg") and two currencies per variant.
//
// The amount is n*100 + k*10 + c, so a single number says which product, which
// variant and which currency a row belongs to — var_L7_1 is 711 TRY and 712
// USD, var_L7_2 is 721 and 722. That encoding is why a wrong join in a read
// path shows up as an amount that reads wrong rather than as an amount that
// merely differs.
func multiVariantSteps(spec Spec) []step {
	count := spec.MultiVariantProducts
	if count == 0 {
		return nil
	}

	when := familyLCreatedAt
	steps := []step{
		{
			name: "family L products",
			sql: `INSERT INTO product (id, handle, title, status, created_at, updated_at)
SELECT 'prod_L' || n, 'urun-' || n, 'Urun ' || n, 'published', $2, $2
FROM generate_series(1, $1::int) AS n
ON CONFLICT (id) DO NOTHING`,
			args: []any{count, when},
		},
		{
			name: "family L variants",
			sql: `INSERT INTO product_variant (id, product_id, title, sku, rank, created_at, updated_at)
SELECT 'var_L' || n || '_' || k, 'prod_L' || n,
       CASE k WHEN 1 THEN '250g' ELSE '1kg' END,
       'SKU-' || n || '-' || k, k, $2, $2
FROM generate_series(1, $1::int) AS n, generate_series(1, 2) AS k
ON CONFLICT (id) DO NOTHING`,
			args: []any{count, when},
		},
		{
			name: "family L price sets",
			sql: `INSERT INTO price_set (id, created_at, updated_at)
SELECT 'pset_L' || n || '_' || k, $2, $2
FROM generate_series(1, $1::int) AS n, generate_series(1, 2) AS k
ON CONFLICT (id) DO NOTHING`,
			args: []any{count, when},
		},
		{
			name: "family L prices",
			sql: `INSERT INTO price (id, price_set_id, currency_code, amount, min_quantity, created_at, updated_at)
SELECT 'price_L' || n || '_' || k || '_' || c, 'pset_L' || n || '_' || k,
       CASE c WHEN 1 THEN 'TRY' ELSE 'USD' END,
       n::bigint * 100 + k * 10 + c, 1, $2, $2
FROM generate_series(1, $1::int) AS n, generate_series(1, 2) AS k, generate_series(1, 2) AS c
ON CONFLICT (id) DO NOTHING`,
			args: []any{count, when},
		},
		{
			name: "family L inventory items",
			sql: `INSERT INTO inventory_items (id, sku, requires_shipping, created_at, updated_at)
SELECT 'invitem_L' || n || '_' || k, 'SKU-' || n || '-' || k, true, $2, $2
FROM generate_series(1, $1::int) AS n, generate_series(1, 2) AS k
ON CONFLICT (id) DO NOTHING`,
			args: []any{count, when},
		},
		{
			name: "family L stock levels",
			sql: `INSERT INTO inventory_levels
    (id, inventory_item_id, location_id, stocked_quantity, reserved_quantity, created_at, updated_at)
SELECT 'invlvl_L' || n || '_' || k, 'invitem_L' || n || '_' || k, $3, $4::bigint, 0, $2, $2
FROM generate_series(1, $1::int) AS n, generate_series(1, 2) AS k
ON CONFLICT (id) DO NOTHING`,
			args: []any{count, when, stockLocationID, stockedQuantity},
		},
		{
			name: "family L variant to price set links",
			sql: `INSERT INTO link_product_variant_price_set (from_id, to_id, created_at)
SELECT 'var_L' || n || '_' || k, 'pset_L' || n || '_' || k, $2
FROM generate_series(1, $1::int) AS n, generate_series(1, 2) AS k
ON CONFLICT (from_id, to_id) DO NOTHING`,
			args: []any{count, when},
		},
		{
			name: "family L variant to inventory links",
			sql: `INSERT INTO link_product_variant_inventory (from_id, to_id, created_at)
SELECT 'var_L' || n || '_' || k, 'invitem_L' || n || '_' || k, $2
FROM generate_series(1, $1::int) AS n, generate_series(1, 2) AS k
ON CONFLICT (from_id, to_id) DO NOTHING`,
			args: []any{count, when},
		},
		{
			name: "family L sales channel assignments",
			sql: `INSERT INTO link_product_sales_channel (from_id, to_id, created_at)
SELECT 'prod_L' || n, $3, $2
FROM generate_series(1, $1::int) AS n
ON CONFLICT (from_id, to_id) DO NOTHING`,
			args: []any{count, when, spec.SalesChannelID},
		},
	}

	return append(steps, mapSteps("family L", "'prod_L' || n", spec, count, when)...)
}

// mapSteps writes one family's category and tag memberships.
//
// productExpression is the SQL fragment producing the family's product id and
// it is a LITERAL from this file, never a value from outside: the two callers
// pass their own family prefix and nothing here is reachable from a request, so
// no caller-supplied text can enter the statement.
func mapSteps(family, productExpression string, spec Spec, count int, when time.Time) []step {
	var steps []step

	if spec.Categories > 0 {
		steps = append(steps, step{
			name: family + " category map",
			sql: "INSERT INTO product_category_map (product_id, category_id, created_at)\n" +
				"SELECT " + productExpression + ", 'pcat_' || ((n - 1) % $2::int + 1), $3\n" +
				"FROM generate_series(1, $1::int) AS n\n" +
				"ON CONFLICT (product_id, category_id) DO NOTHING",
			args: []any{count, spec.Categories, when},
		})
	}

	if spec.Tags > 0 {
		steps = append(steps, step{
			name: family + " tag map",
			sql: "INSERT INTO product_tag_map (product_id, tag_id, created_at)\n" +
				"SELECT " + productExpression + ", 'ptag_' || ((n - 1) % $2::int + 1), $3\n" +
				"FROM generate_series(1, $1::int) AS n\n" +
				"ON CONFLICT (product_id, tag_id) DO NOTHING",
			args: []any{count, spec.Tags, when},
		})
	}

	return steps
}

// handMadeStep writes the four variant-less products in one statement.
//
// The four columns arrive as four parallel arrays through unnest rather than as
// four VALUES rows built by string concatenation: the values then travel as
// BOUND PARAMETERS, which is the same discipline every other statement here
// keeps and the reason none of this file's text is assembled out of data.
func handMadeStep() step {
	ids := make([]string, 0, len(handMadeProducts))
	handles := make([]string, 0, len(handMadeProducts))
	titles := make([]string, 0, len(handMadeProducts))
	times := make([]time.Time, 0, len(handMadeProducts))
	for _, product := range handMadeProducts {
		ids = append(ids, product.id)
		handles = append(handles, product.handle)
		titles = append(titles, product.title)
		times = append(times, product.createdAt)
	}

	return step{
		name: "hand-made products",
		sql: `INSERT INTO product (id, handle, title, status, created_at, updated_at)
SELECT id, handle, title, 'published', created_at, created_at
FROM unnest($1::text[], $2::text[], $3::text[], $4::timestamptz[])
    AS handmade(id, handle, title, created_at)
ON CONFLICT (id) DO NOTHING`,
		args: []any{ids, handles, titles, times},
	}
}

// resetSteps deletes exactly what [Seed] writes.
//
// The patterns are anchored and end in digits, which is what separates a rig id
// from a real one; the argument for that, and for what a bare prefix would have
// deleted, is on [Reset].
//
// Five tables are absent from this list and their absence is load bearing:
// product_variant, price, inventory_levels, product_category_map and
// product_tag_map all hang off a row deleted here by an ON DELETE CASCADE
// declared in the modules' own migrations. Deleting them by hand as well would
// mean this file carrying a second, hand-maintained model of those foreign
// keys, and the day a cascade changed the two models would disagree in silence.
func resetSteps() []step {
	ids := make([]string, 0, len(handMadeProducts))
	for _, product := range handMadeProducts {
		ids = append(ids, product.id)
	}

	return []step{
		{
			name: "sales channel assignments",
			sql:  `DELETE FROM link_product_sales_channel WHERE from_id ~ '^prod_(B|L)[0-9]{1,12}$'`,
		},
		{
			name: "variant to price set links",
			sql: `DELETE FROM link_product_variant_price_set
WHERE from_id ~ '^var_(B[0-9]{1,12}|L[0-9]{1,12}_[0-9]{1,12})$'`,
		},
		{
			name: "variant to inventory links",
			sql: `DELETE FROM link_product_variant_inventory
WHERE from_id ~ '^var_(B[0-9]{1,12}|L[0-9]{1,12}_[0-9]{1,12})$'`,
		},
		{
			name: "products",
			sql:  `DELETE FROM product WHERE id ~ '^prod_(B|L)[0-9]{1,12}$' OR id = ANY($1::text[])`,
			args: []any{ids},
		},
		{
			name: "price sets",
			sql:  `DELETE FROM price_set WHERE id ~ '^pset_(B[0-9]{1,12}|L[0-9]{1,12}_[0-9]{1,12})$'`,
		},
		{
			name: "inventory items",
			sql:  `DELETE FROM inventory_items WHERE id ~ '^invitem_(B[0-9]{1,12}|L[0-9]{1,12}_[0-9]{1,12})$'`,
		},
		{
			name: "stock location",
			sql:  `DELETE FROM stock_locations WHERE id = $1`,
			args: []any{stockLocationID},
		},
		{
			name: "categories",
			sql:  `DELETE FROM product_category WHERE id ~ '^pcat_[0-9]{1,12}$'`,
		},
		{
			name: "tags",
			sql:  `DELETE FROM product_tag WHERE id ~ '^ptag_[0-9]{1,12}$'`,
		},
	}
}
