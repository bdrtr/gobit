package rig

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// This file pins the parts of the skewed taxonomy that hold WITHOUT a database.
//
// What a database adds — that the two categories really come back with the same
// count and different members — is asserted where a database is,
// [TestTheTwoSkewedCategoriesHoldTheSameCountAndNotTheSameProducts] in
// internal/app. What is here is the arithmetic and the refusals, because those
// are the parts that decide the SHAPE of the fixture and are wrong in ways a
// row count cannot see.

// skewSpec is a valid spec of the given family sizes and skew.
func skewSpec(single, multi, skew int) Spec {
	return Spec{
		SingleVariantProducts: single,
		MultiVariantProducts:  multi,
		Categories:            DefaultCategories,
		Tags:                  DefaultTags,
		SkewedCategorySize:    skew,
		SalesChannelID:        "sc_test",
	}
}

// TestTheDefaultRigCarriesNoSkew is the promise [DefaultSpec] makes.
//
// Every performance sentence in this repository was measured on a catalog whose
// taxonomy is perfectly uniform. The skew is worth having and it is worth
// having as an ADDITION: a default that quietly wrote two more categories and
// their memberships would make each of those sentences describe a catalog
// nobody measured, which is the drift this package exists to end.
func TestTheDefaultRigCarriesNoSkew(t *testing.T) {
	t.Parallel()

	assert.Zero(t, DefaultSpec().SkewedCategorySize,
		"the default rig grew a skewed taxonomy; the figures recorded against it were "+
			"taken on a catalog that had none")

	spec := DefaultSpec()
	spec.SalesChannelID = "sc_test"
	assert.Empty(t, skewSteps(spec), "a spec asking for no skew still wrote statements")

	for _, step := range seedSteps(spec) {
		assert.NotContains(t, step.sql, AdjacentSkewCategoryID,
			"the %q step names a skewed category the spec did not ask for", step.name)
		assert.NotContains(t, step.sql, SpreadSkewCategoryID,
			"the %q step names a skewed category the spec did not ask for", step.name)
	}
}

// TestTheSkewBuildsBothCategoriesOrNeither is what makes the fixture a PAIR.
//
// The measurement that motivated the skew found two categories of nearly the
// same size (26 and 27 products)
// producing two different plans, and concluded that the cost is not a property
// of the size at all. A rig carrying only the cheap one, or only the expensive
// one, would therefore reproduce a number and hide the finding.
func TestTheSkewBuildsBothCategoriesOrNeither(t *testing.T) {
	t.Parallel()

	steps := skewSteps(skewSpec(100, 10, 5))
	require.Len(t, steps, 3,
		"the skew is three statements: the two categories, and one map for each of them")

	var text strings.Builder
	for _, step := range steps {
		text.WriteString(step.sql)
		for _, arg := range step.args {
			if list, isList := arg.([]string); isList {
				text.WriteString(strings.Join(list, " "))
			}
			if single, isString := arg.(string); isString {
				text.WriteString(single)
			}
		}
	}

	assert.Contains(t, text.String(), AdjacentSkewCategoryID)
	assert.Contains(t, text.String(), SpreadSkewCategoryID,
		"only one of the two skewed categories is built, so the rig carries one shape "+
			"where the finding needs two")
}

// TestTheSpreadCategoryStridesAcrossTheWholeCatalog pins the arithmetic that
// makes the second category a different SET rather than a second copy.
//
// The stride is how far apart its members stand, and it has to walk the entire
// listing in exactly as many steps as the category has members. Too small and
// the spread category collapses onto the head of the listing, which is where
// the adjacent one already is; the two would then hold nearly the same
// products and every count would still pass.
func TestTheSpreadCategoryStridesAcrossTheWholeCatalog(t *testing.T) {
	t.Parallel()

	for _, expected := range []struct {
		single, multi, skew int
		stride              int
	}{
		{single: 100, multi: 0, skew: 10, stride: 10},
		{single: 50_000, multi: 2_000, skew: 26, stride: 2_000},
		{single: 10, multi: 0, skew: 3, stride: 3},
		// The degenerate end: a category holding the whole catalog strides by
		// one, which is the honest answer to "spread over every row" rather
		// than a division by zero.
		{single: 8, multi: 2, skew: 10, stride: 1},
	} {
		steps := skewSteps(skewSpec(expected.single, expected.multi, expected.skew))
		require.Len(t, steps, 3)

		spread := steps[2]
		require.Equal(t, "spread skew category map", spread.name,
			"the statement order changed and this assertion is reading the wrong one")
		require.Len(t, spread.args, 5)
		assert.Equal(t, expected.stride, spread.args[3],
			"%d products over a skewed category of %d strides by %v and not by %d",
			expected.single+expected.multi, expected.skew, spread.args[3], expected.stride)
		assert.Equal(t, expected.skew, spread.args[4],
			"the spread category is capped at a size other than the one asked for")
	}
}

// TestTheSkewedMembersArePickedByTheListingOrder is the trap this fixture is
// one line away from at all times.
//
// "Adjacent" reads as a run of consecutive numbers, and a run of consecutive
// numbers is not adjacent here: the listing orders by created_at then by id,
// the ids are text, and prod_B1 to prod_B26 are scattered through that order
// with prod_B89 and prod_B899 standing between two of them. Picked that way the
// adjacent category would be a spread one under another name.
func TestTheSkewedMembersArePickedByTheListingOrder(t *testing.T) {
	t.Parallel()

	steps := skewSteps(skewSpec(100, 10, 5))
	require.Len(t, steps, 3)

	for _, step := range steps[1:] {
		assert.Contains(t, step.sql, "ORDER BY created_at DESC, id DESC",
			"the %q step does not read the listing's own order, so the position it calls "+
				"adjacent is a position in some other ordering", step.name)
		assert.Contains(t, step.args, generatedProductPattern,
			"the %q step does not restrict itself to the products this package generated; "+
				"the installation's own catalog would be drawn into the fixture", step.name)
	}

	assert.Contains(t, steps[2].sql, "row_number()",
		"the spread category no longer numbers the listing, so it cannot take every "+
			"stride-th row of it and has become a second copy of the adjacent one")
}

// TestASkewLargerThanTheCatalogIsRefused is the refusal that keeps the pair a
// pair.
//
// A size the catalog cannot fill would not fail: the adjacent category would
// take everything there is and the spread one would take everything there is,
// and the two would be identical. Refusing says so; a LIMIT would have made the
// rig quietly stop being a comparison.
func TestASkewLargerThanTheCatalogIsRefused(t *testing.T) {
	t.Parallel()

	err := skewSpec(6, 2, 9).validate()
	require.Error(t, err, "a skewed category larger than the catalog was accepted")
	assert.True(t, errors.IsInvalid(err), "error: %v", err)
	assert.Contains(t, err.Error(), "SkewedCategorySize")

	require.NoError(t, skewSpec(6, 2, 8).validate(),
		"a skewed category the size of the catalog is legal: it is the degenerate end of "+
			"the stride, not a mistake")
}

// TestANegativeSkewIsRefusedRatherThanClamped follows the reasoning already
// written on the other counts: a negative bound makes generate_series return no
// rows, so clamping would report success for a run that built nothing.
func TestANegativeSkewIsRefusedRatherThanClamped(t *testing.T) {
	t.Parallel()

	err := skewSpec(10, 2, -1).validate()
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "error: %v", err)
	assert.Contains(t, err.Error(), "SkewedCategorySize")
}

// TestTheResetDeletesTheSkewedCategoriesByTheirOwnIds is the half of the reset
// no pattern covers.
//
// Everything else the rig writes carries a number, and the reset separates a
// rig id from a real one by requiring digits. The two skewed categories carry
// no number, so a reset that only ran the numbered pattern would leave them
// standing — and the next seed would then add a second set of memberships to
// categories that already held one.
func TestTheResetDeletesTheSkewedCategoriesByTheirOwnIds(t *testing.T) {
	t.Parallel()

	var deleted []string
	for _, step := range resetSteps() {
		if !strings.Contains(step.sql, "product_category ") {
			continue
		}
		for _, arg := range step.args {
			if list, isList := arg.([]string); isList {
				deleted = append(deleted, list...)
			}
		}
	}

	assert.Contains(t, deleted, AdjacentSkewCategoryID,
		"the reset leaves %q behind, so a rebuild seeds a second set of memberships into "+
			"a category that already holds one", AdjacentSkewCategoryID)
	assert.Contains(t, deleted, SpreadSkewCategoryID,
		"the reset leaves %q behind", SpreadSkewCategoryID)
}

// TestTheResetAndTheSkewSeparateRigIdsTheSameWay is why the pattern is a
// constant.
//
// The reset deletes by it and the skew picks its members by it, and the two
// have opposite failure modes on the same edit: loosened, the reset starts
// deleting an installation's own catalog and the skew starts counting it into a
// measurement. Written twice, only one of them would be corrected.
func TestTheResetAndTheSkewSeparateRigIdsTheSameWay(t *testing.T) {
	t.Parallel()

	var reset, skew int
	for _, step := range resetSteps() {
		for _, arg := range step.args {
			if text, isText := arg.(string); isText && text == generatedProductPattern {
				reset++
			}
		}
	}
	for _, step := range skewSteps(skewSpec(100, 10, 5)) {
		for _, arg := range step.args {
			if text, isText := arg.(string); isText && text == generatedProductPattern {
				skew++
			}
		}
	}

	assert.Positive(t, reset, "the reset no longer uses the shared id pattern")
	assert.Positive(t, skew, "the skew no longer uses the shared id pattern")
}
