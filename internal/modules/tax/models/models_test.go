package models_test

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// TestNewIDFormatAndOrder verifies the format of the ids and that they are
// TIME-ORDERED.
//
// Sortability is not decoration: the tie-breaking rule in the tax calculation
// ("the rate with the smaller id wins") rests on exactly that order, and were
// the ids not sortable the rule would mean "a random one wins".
func TestNewIDFormatAndOrder(t *testing.T) {
	base := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)

	var ids []string
	for i := range 50 {
		ids = append(ids, models.NewTaxRateID(base.Add(time.Duration(i)*time.Millisecond)))
	}

	for _, id := range ids {
		assert.True(t, strings.HasPrefix(id, models.TaxRateIDPrefix), "prefix: %s", id)
		assert.Len(t, id, len(models.TaxRateIDPrefix)+models.IDBodyLength())
	}

	assert.True(t, sort.StringsAreSorted(ids),
		"ids produced with increasing time must increase lexicographically too")

	seen := map[string]bool{}
	for _, id := range ids {
		require.False(t, seen[id], "the id repeated: %s", id)
		seen[id] = true
	}
}

// TestNewIDPrefixesDiffer verifies that every record carries its own prefix.
func TestNewIDPrefixesDiffer(t *testing.T) {
	now := time.Now()

	assert.True(t, strings.HasPrefix(models.NewTaxRegionID(now), "taxreg_"))
	assert.True(t, strings.HasPrefix(models.NewTaxRateID(now), "taxrate_"))
	assert.True(t, strings.HasPrefix(models.NewTaxRateRuleID(now), "taxrule_"))

	// The rate and region prefixes MUST NOT BE A PREFIX of one another; were
	// they, the prefix check would accept an id of the wrong kind.
	assert.False(t, strings.HasPrefix(models.TaxRateIDPrefix, models.TaxRegionIDPrefix))
	assert.False(t, strings.HasPrefix(models.TaxRegionIDPrefix, models.TaxRateIDPrefix))
	assert.False(t, strings.HasPrefix(models.TaxRateRuleIDPrefix, models.TaxRateIDPrefix))
}

// TestNewIDBefore1970IsClampedToTheFloor verifies that a negative timestamp
// does not break the ordering.
func TestNewIDBefore1970IsClampedToTheFloor(t *testing.T) {
	older := models.NewTaxRateID(time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC))
	newer := models.NewTaxRateID(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	assert.Less(t, older, newer, "the clamped old stamp must still stay smaller")
}

// TestRuleReferenceValidity verifies the defined reference kinds.
func TestRuleReferenceValidity(t *testing.T) {
	for _, ref := range []models.RuleReference{
		models.ReferenceProduct, models.ReferenceProductType, models.ReferenceShippingOption,
	} {
		assert.True(t, ref.Valid(), "reference: %s", ref)
		assert.Positive(t, ref.Specificity())
	}

	for _, ref := range []models.RuleReference{"", "variant", "PRODUCT"} {
		assert.False(t, ref.Valid(), "reference: %q", ref)
		assert.Zero(t, ref.Specificity())
	}
}

// TestRuleReferenceSpecificityOrder verifies that the product rule beats the
// product type.
func TestRuleReferenceSpecificityOrder(t *testing.T) {
	assert.Greater(t, models.ReferenceProduct.Specificity(), models.ReferenceProductType.Specificity(),
		"a rule written for a single product is MORE SPECIFIC than one written for the type")
	assert.Equal(t, models.ReferenceProductType.Specificity(), models.ReferenceShippingOption.Specificity(),
		"a shipping rule does not compete with items; its degree is taken to be the product type's")
}

// TestTaxRegionHierarchyHelpers verifies the root and province distinction.
func TestTaxRegionHierarchyHelpers(t *testing.T) {
	root := models.TaxRegion{ID: "taxreg_1", CountryCode: "TR"}
	assert.True(t, root.IsRoot())
	assert.Empty(t, root.Province())
	assert.Empty(t, root.Parent())

	province := "34"
	parent := "taxreg_1"
	child := models.TaxRegion{ID: "taxreg_2", CountryCode: "TR", ProvinceCode: &province, ParentID: &parent}
	assert.False(t, child.IsRoot())
	assert.Equal(t, "34", child.Province())
	assert.Equal(t, "taxreg_1", child.Parent())
}

// TestTaxRatePercentProducesNoFloat verifies that the percentage representation
// comes back as two INTEGERS.
func TestTaxRatePercentProducesNoFloat(t *testing.T) {
	tests := map[int32][2]int32{
		0:      {0, 0},
		100:    {1, 0},
		2000:   {20, 0},
		2050:   {20, 50},
		10_000: {100, 0},
	}

	for bps, want := range tests {
		percent, remainder := models.TaxRate{RateBps: bps}.RatePercent()
		assert.Equal(t, want[0], percent, "bps: %d", bps)
		assert.Equal(t, want[1], remainder, "bps: %d", bps)
	}
}

// TestTaxRatePatchKeepsUntouchedFields verifies that the partial update is a
// pure transformation.
func TestTaxRatePatchKeepsUntouchedFields(t *testing.T) {
	code := "VAT20"
	original := models.TaxRate{
		ID: "taxrate_1", Name: "VAT", Code: &code, RateBps: 2000, IsDefault: true,
		Metadata: map[string]any{"a": "b"},
	}

	newName := "VAT Updated"
	patched := original.Patched(models.TaxRatePatch{Name: &newName})

	assert.Equal(t, "VAT Updated", patched.Name)
	assert.Equal(t, "VAT20", patched.RateCode())
	assert.Equal(t, int32(2000), patched.RateBps)
	assert.True(t, patched.IsDefault)
	assert.Equal(t, "VAT", original.Name, "the receiver MUST NOT CHANGE")
}

// TestTaxRatePatchCodeRemoval verifies that the empty string deletes the code.
func TestTaxRatePatchCodeRemoval(t *testing.T) {
	code := "VAT20"
	original := models.TaxRate{Code: &code}

	empty := ""
	assert.Nil(t, original.Patched(models.TaxRatePatch{Code: &empty}).Code)

	updated := "VAT18"
	assert.Equal(t, "VAT18", original.Patched(models.TaxRatePatch{Code: &updated}).RateCode())
	assert.Equal(t, "VAT20", original.RateCode(), "the receiver MUST NOT CHANGE")
}

// TestTaxRatePatchEmpty verifies that an empty patch is recognized.
func TestTaxRatePatchEmpty(t *testing.T) {
	assert.True(t, models.TaxRatePatch{}.Empty())

	name := "x"
	assert.False(t, models.TaxRatePatch{Name: &name}.Empty())
	assert.False(t, models.TaxRatePatch{Metadata: map[string]any{}}.Empty())
}

// TestRateBoundsMatchTheMigration verifies that the constants are the same as
// the database CHECK.
//
// Were the two to diverge, the service could not write a value it had accepted
// and the error would surface as a constraint violation AFTER validation had
// passed.
func TestRateBoundsMatchTheMigration(t *testing.T) {
	assert.Equal(t, int32(0), models.MinRateBps)
	assert.Equal(t, int32(10_000), models.MaxRateBps, "migration: rate_bps <= 10000")
	// The constant is not the basis point SCALE but the number of basis points
	// in one percent; the scale (10000) is service.BpsScale and the two stand
	// under separate names.
	assert.Equal(t, int32(100), models.BpsPerPercent)
	assert.Equal(t, 2, models.CountryCodeLength)
	assert.Equal(t, 10, models.MaxProvinceCodeLength, "migration: province_code at most 10 characters")
}
