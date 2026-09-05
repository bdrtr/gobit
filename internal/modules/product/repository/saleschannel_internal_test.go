package repository

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file pins the CONSTRUCTION of the product listing's filter body.
//
// It exists because of what the file header of saleschannel.go paid for speed:
// the statement used to be one fixed string with a fixed parameter numbering,
// and now it is assembled per request. Two failure modes came with that and
// neither of them is an error at run time.
//
// A criterion that stops writing its clause returns MORE products than it
// should — a storefront that shows another channel's catalog, or a category
// page that lists the whole shop. Nothing raises, the page just fills.
//
// A numbering that drifts between the body and the arguments sends the right
// values to the wrong placeholders. pgx does not object: every criterion here
// is text, so a category id lands in the handle comparison and the listing
// quietly returns nothing.
//
// Both are silent, so both are asserted here rather than left to the
// integration tests, which can only see the rows and not the reason.

// allCriteria is every optional criterion of [ProductFilter], with a value to
// set it to and the fragment its clause must contain.
//
// The clause fragments are matched rather than the whole clause because the
// whole clause carries a placeholder whose NUMBER depends on which other
// criteria are set — which is the property the numbering tests exercise
// separately.
var allCriteria = []struct {
	name     string
	set      func(*ProductFilter)
	fragment string
}{
	{"status", func(f *ProductFilter) { v := "published"; f.Status = &v }, "AND status = "},
	{"collection", func(f *ProductFilter) { v := "pcol_1"; f.CollectionID = &v }, "AND collection_id = "},
	{"handle", func(f *ProductFilter) { v := "a-handle"; f.Handle = &v }, "AND handle = "},
	{"search", func(f *ProductFilter) { v := "shirt"; f.Search = &v }, "AND title ILIKE "},
	{"category", func(f *ProductFilter) { v := "pcat_1"; f.CategoryID = &v }, "FROM product_category_map"},
	{"tag", func(f *ProductFilter) { v := "ptag_1"; f.TagID = &v }, "FROM product_tag_map"},
	{"sales channel", func(f *ProductFilter) { f.SalesChannelIDs = []string{"sc_1"} }, "SELECT bool_or(scl.to_id"},
}

// filterWith produces a filter carrying exactly the criteria whose bit is set
// in mask.
func filterWith(mask int) ProductFilter {
	var f ProductFilter
	for i, c := range allCriteria {
		if mask&(1<<i) != 0 {
			c.set(&f)
		}
	}
	return f
}

// TestEachCriterionAloneWritesOnlyItsOwnClause is the "each filter alone" half
// of the claim.
//
// The assertion that matters is the NEGATIVE one: a filter carrying only a
// category must not carry a status comparison, because the old shape wrote all
// seven clauses always and the whole point of the change is that it no longer
// does. Asserting only that the wanted clause is present would pass on the old
// shape too.
func TestEachCriterionAloneWritesOnlyItsOwnClause(t *testing.T) {
	for i, want := range allCriteria {
		t.Run(want.name, func(t *testing.T) {
			body, args := productFilterSQL(filterWith(1 << i))

			assert.Contains(t, body, want.fragment)
			assert.Len(t, args, 1, "one criterion must consume exactly one parameter")

			for j, other := range allCriteria {
				if i == j {
					continue
				}
				assert.NotContains(t, body, other.fragment,
					"the %s clause was written for a filter that only carries %s",
					other.name, want.name)
			}
		})
	}
}

// TestNoCriterionLeavesOnlyTheSoftDeleteGuard is the "none at all" half.
//
// The soft delete guard is not a criterion and is never optional: a listing
// that stopped writing it would return deleted products to the storefront.
func TestNoCriterionLeavesOnlyTheSoftDeleteGuard(t *testing.T) {
	body, args := productFilterSQL(ProductFilter{})

	assert.Equal(t, "WHERE deleted_at IS NULL", body)
	assert.Empty(t, args)
}

// TestSeveralCriteriaTogetherWriteAllOfTheirClauses is the "several together"
// half.
//
// Every one of the 128 combinations is walked rather than a couple of hand
// picked ones, because the failure this guards against is a single "if" losing
// its clause, and which one it is cannot be guessed in advance.
func TestSeveralCriteriaTogetherWriteAllOfTheirClauses(t *testing.T) {
	for mask := range 1 << len(allCriteria) {
		body, args := productFilterSQL(filterWith(mask))

		wanted := 0
		for i, c := range allCriteria {
			if mask&(1<<i) != 0 {
				wanted++
				assert.Contains(t, body, c.fragment, "mask %d lost the %s clause", mask, c.name)
			} else {
				assert.NotContains(t, body, c.fragment, "mask %d wrote the absent %s clause", mask, c.name)
			}
		}
		assert.Len(t, args, wanted, "mask %d: one parameter per criterion given", mask)
	}
}

// TestEveryPlaceholderHasExactlyOneArgument is the numbering claim.
//
// A body carrying $3 while args holds two values is not an error pgx reports as
// one; it is "expected 2 arguments, got 3" only if the count is short, and a
// SKIPPED number in the middle is not caught at all. Walking $1..$n and
// demanding each appears exactly once catches both a gap and a duplicate.
func TestEveryPlaceholderHasExactlyOneArgument(t *testing.T) {
	for mask := range 1 << len(allCriteria) {
		body, args := productFilterSQL(filterWith(mask))

		for n := 1; n <= len(args); n++ {
			assert.Equal(t, 1, strings.Count(body, "$"+strconv.Itoa(n)),
				"mask %d: $%d must appear exactly once", mask, n)
		}
		assert.NotContains(t, body, "$"+strconv.Itoa(len(args)+1),
			"mask %d: the body reaches past its own arguments", mask)
	}
}

// TestTheListAndTheCountAgreeOnTheNumbering is the reason the body and the
// arguments are returned as one pair.
//
// The two statements SHARE the body. If they were ever built from different
// filters, or if one of them renumbered, the count would answer a question the
// page did not ask and the pagination envelope would send the client after
// pages that never fill — the exact fault the sales channel filter was moved
// into the database to prevent.
func TestTheListAndTheCountAgreeOnTheNumbering(t *testing.T) {
	for mask := range 1 << len(allCriteria) {
		f := filterWith(mask)
		f.Limit = 20

		listSQL, listArgs := listProductsSQL(f)
		countSQL, countArgs := countProductsSQL(f)

		require.GreaterOrEqual(t, len(listArgs), len(countArgs))
		assert.Equal(t, countArgs, listArgs[:len(countArgs)],
			"mask %d: the listing must hand the shared body the same arguments", mask)

		body, _ := productFilterSQL(f)
		assert.Contains(t, listSQL, body, "mask %d: the listing must carry the shared body", mask)
		assert.Contains(t, countSQL, body, "mask %d: the count must carry the shared body", mask)
	}
}

// TestTheListNumbersItsOwnParametersAfterTheBody pins that limit, offset and
// the two cursor halves come after whatever the filter used.
//
// They are numbered from len(args) and a literal would be wrong the moment a
// criterion is added or dropped; four criteria here push them from $1-$4 to
// $5-$8, which is the drift a fixed number would not survive.
func TestTheListNumbersItsOwnParametersAfterTheBody(t *testing.T) {
	f := filterWith(0)
	f.Limit = 20
	listSQL, listArgs := listProductsSQL(f)
	assert.Len(t, listArgs, 4)
	assert.Contains(t, listSQL, "LIMIT $1::int OFFSET $2::int")
	assert.Contains(t, listSQL, "COALESCE($3::timestamptz")
	assert.Contains(t, listSQL, "COALESCE($4::text, '')")

	full := filterWith(1<<len(allCriteria) - 1)
	full.Limit = 20
	fullSQL, fullArgs := listProductsSQL(full)
	assert.Len(t, fullArgs, 11)
	assert.Contains(t, fullSQL, "LIMIT $8::int OFFSET $9::int")
	assert.Contains(t, fullSQL, "COALESCE($10::timestamptz")
	assert.Contains(t, fullSQL, "COALESCE($11::text, '')")
}

// TestNilnessAndNotEmptinessDecides pins the one semantic the rewrite could
// have changed without anything failing.
//
// Under the old shape a non-nil pointer to "" produced "title ILIKE '%%'",
// which matches every row, and a non-nil EMPTY channel slice produced a filter
// that keeps only the products with no assignment at all — see
// [ProductFilter.SalesChannelIDs] for why that distinction is load-bearing. A
// builder that tested for an empty value instead of a nil one would drop both
// clauses and open the catalog, silently.
func TestNilnessAndNotEmptinessDecides(t *testing.T) {
	empty := ""
	body, args := productFilterSQL(ProductFilter{Search: &empty, SalesChannelIDs: []string{}})

	assert.Contains(t, body, "AND title ILIKE ")
	assert.Contains(t, body, "SELECT bool_or(scl.to_id")
	assert.Len(t, args, 2)

	bare, bareArgs := productFilterSQL(ProductFilter{})
	assert.NotContains(t, bare, "AND title ILIKE ")
	assert.NotContains(t, bare, "SELECT bool_or(scl.to_id")
	assert.Empty(t, bareArgs)
}

// TestTheChannelClauseInTheListingHasNoNullBranch pins the split between
// [salesChannelVisibleTemplate] and [salesChannelAssignedTemplate].
//
// The listing writes the channel clause only when the request carried channels,
// so the "$n IS NULL" branch there is dead text — and leaving it in is what
// kept the planner from ordering the cheap criteria first. The three queries
// that are HANDED ids still need it, because their callers really do pass nil
// and expect true, so the branch must survive in one place and not in the
// other.
func TestTheChannelClauseInTheListingHasNoNullBranch(t *testing.T) {
	body, _ := productFilterSQL(ProductFilter{SalesChannelIDs: []string{"sc_1"}})
	assert.NotContains(t, body, "IS NULL\n    OR ")

	assert.Contains(t, productVisibleSQL, "IS NULL")
	assert.Contains(t, visibleProductIDsSQL, "IS NULL\n    OR ")
	assert.Contains(t, visibleVariantIDsSQL, "IS NULL\n    OR ")
}

// TestTheBuiltStatementStillReadsAsSQL is the answer to the one cost of this
// shape that has no structural fix.
//
// A statement assembled at run time cannot be grepped, and a reader who wants
// to know what the storefront actually sends can no longer find out by opening
// a file. This test is where that reader looks: the exact text produced for the
// storefront's category page, spelled out, so the statement is readable without
// running the program and so changing it is a deliberate act rather than a side
// effect of touching the builder.
//
// It is deliberately brittle. If it fails because the SQL genuinely changed,
// the fix is to paste the new text in AFTER reading it — not to loosen the
// assertion.
func TestTheBuiltStatementStillReadsAsSQL(t *testing.T) {
	category := "pcat_7"
	f := ProductFilter{CategoryID: &category, SalesChannelIDs: []string{"sc_1"}, Limit: 20}

	listSQL, args := listProductsSQL(f)
	assert.Len(t, args, 6)
	assert.Equal(t, `SELECT id, handle, title, subtitle, description, thumbnail,
	status, is_giftcard, discountable, weight, length, height, width,
	material, origin_country, collection_id, metadata,
	created_at, updated_at, deleted_at FROM product
WHERE deleted_at IS NULL
  AND EXISTS (
    SELECT 1 FROM product_category_map
    WHERE product_category_map.product_id = product.id
      AND product_category_map.category_id = $1::text
  )
  AND COALESCE((
      SELECT bool_or(scl.to_id = ANY($2::text[]) IS TRUE)
      FROM link_product_sales_channel scl
      WHERE scl.from_id = product.id
    ), true)
  AND (created_at, id) < (COALESCE($5::timestamptz, 'infinity'::timestamptz), COALESCE($6::text, ''))
ORDER BY created_at DESC, id DESC
LIMIT $3::int OFFSET $4::int`, listSQL)
}
