package models_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/region/models"
)

// TestMinorUnitFactor proves that the decimal digit count is correctly turned
// into the division factor.
//
// This whole table comes from real currencies and it tests the module's reason
// for existing: since amounts are stored as minor unit INTEGERS, a presentation
// layer assuming a fixed factor of 100 would show yen amounts a hundred times
// too small and dinar amounts ten times too large.
func TestMinorUnitFactor(t *testing.T) {
	cases := []struct {
		code   string
		digits int32
		factor int64
		// amount is the minor unit amount, major is its whole part.
		amount int64
		major  int64
	}{
		{code: "JPY", digits: 0, factor: 1, amount: 1999, major: 1999},
		{code: "TRY", digits: 2, factor: 100, amount: 1999, major: 19},
		{code: "USD", digits: 2, factor: 100, amount: 100, major: 1},
		{code: "KWD", digits: 3, factor: 1000, amount: 1999, major: 1},
		{code: "UYW", digits: 4, factor: 10_000, amount: 19_999, major: 1},
	}

	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			currency := models.Currency{Code: tc.code, DecimalDigits: tc.digits}

			assert.Equal(t, tc.factor, currency.MinorUnitFactor())
			assert.Equal(t, tc.major, tc.amount/currency.MinorUnitFactor())
		})
	}
}

// TestMinorUnitFactorOutOfRangeIsSafe proves that an out of range digit count
// returns ONE, not zero.
//
// Had it returned zero, division by zero would arise in the caller; returning
// one distorts the scale at worst but does not bring the process down.
func TestMinorUnitFactorOutOfRangeIsSafe(t *testing.T) {
	for _, digits := range []int32{-1, models.MaxDecimalDigits + 1, 100} {
		currency := models.Currency{Code: "XXX", DecimalDigits: digits}

		assert.Equal(t, int64(1), currency.MinorUnitFactor(), "digits: %d", digits)
	}
}

// TestTaxRatePercent proves that a basis point is split into an integer
// percentage and a remainder.
//
// The percentage does not come back as a float: 2050 basis points is "20% and
// 50 basis points" and it is carried as two integers (plan Section 8).
func TestTaxRatePercent(t *testing.T) {
	cases := []struct {
		rate      int32
		percent   int32
		remainder int32
	}{
		{rate: 0, percent: 0, remainder: 0},
		{rate: 1, percent: 0, remainder: 1},
		{rate: 800, percent: 8, remainder: 0},
		{rate: 2000, percent: 20, remainder: 0},
		{rate: 2050, percent: 20, remainder: 50},
		{rate: models.MaxTaxRate, percent: 100, remainder: 0},
	}

	for _, tc := range cases {
		region := models.Region{TaxRate: tc.rate}

		percent, remainder := region.TaxRatePercent()
		assert.Equal(t, tc.percent, percent, "rate: %d", tc.rate)
		assert.Equal(t, tc.remainder, remainder, "rate: %d", tc.rate)
	}
}

// TestRegionPatchedAppliesOnlyGivenFields proves that the patch applies only
// the filled fields and DOES NOT MODIFY the receiver.
func TestRegionPatchedAppliesOnlyGivenFields(t *testing.T) {
	original := models.Region{
		ID:             "reg_1",
		Name:           "Turkey",
		CurrencyCode:   "TRY",
		AutomaticTaxes: true,
		TaxRate:        2000,
	}

	name := "New"
	patched := original.Patched(models.RegionPatch{Name: &name})

	assert.Equal(t, "New", patched.Name)
	assert.Equal(t, "TRY", patched.CurrencyCode, "a field that was not given must not change")
	assert.True(t, patched.AutomaticTaxes)
	assert.Equal(t, int32(2000), patched.TaxRate)
	assert.Equal(t, "Turkey", original.Name, "the receiver must not change")
}

// TestRegionPatchedWritesZeroValues proves that a patch with a zero value does
// not count as "do not touch".
//
// This is the only reason for using a pointer: false and 0 are valid values.
func TestRegionPatchedWritesZeroValues(t *testing.T) {
	original := models.Region{Name: "X", CurrencyCode: "TRY", AutomaticTaxes: true, TaxRate: 2000}

	automatic := false
	rate := int32(0)
	patched := original.Patched(models.RegionPatch{AutomaticTaxes: &automatic, TaxRate: &rate})

	assert.False(t, patched.AutomaticTaxes)
	assert.Zero(t, patched.TaxRate)
}

// TestRegionPatchEmpty proves that an empty patch is recognized.
func TestRegionPatchEmpty(t *testing.T) {
	assert.True(t, models.RegionPatch{}.Empty())

	name := "X"
	code := "TRY"
	automatic := false
	rate := int32(0)
	assert.False(t, models.RegionPatch{Name: &name}.Empty())
	assert.False(t, models.RegionPatch{CurrencyCode: &code}.Empty())
	assert.False(t, models.RegionPatch{AutomaticTaxes: &automatic}.Empty(),
		"false is a value, it is not 'not given'")
	assert.False(t, models.RegionPatch{TaxRate: &rate}.Empty(),
		"0 is a value, it is not 'not given'")
}

// TestNewRegionIDIsPrefixedAndSortable proves that the id is prefixed, of the
// right length, unique and TIME ORDERED.
//
// The sortability claim is not an empty one: because "ORDER BY id" yields
// creation order, listing queries do not sort by a separate time column.
func TestNewRegionIDIsPrefixedAndSortable(t *testing.T) {
	base := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

	first := models.NewRegionID(base)
	second := models.NewRegionID(base.Add(time.Millisecond))

	require.True(t, strings.HasPrefix(first, models.RegionIDPrefix))
	assert.Len(t, strings.TrimPrefix(first, models.RegionIDPrefix), models.IDBodyLength())
	assert.Less(t, first, second, "an id produced later has to come later lexicographically too")

	seen := map[string]struct{}{}
	for range 1000 {
		id := models.NewRegionID(base)
		_, dup := seen[id]
		require.False(t, dup, "ids produced in the same millisecond must not repeat: %s", id)
		seen[id] = struct{}{}
	}
}

// TestNewIDClampsPreEpochTime proves that a timestamp before 1970 does not
// break the ordering.
func TestNewIDClampsPreEpochTime(t *testing.T) {
	old := models.NewRegionID(time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC))
	epoch := models.NewRegionID(time.Unix(0, 0))

	assert.Len(t, strings.TrimPrefix(old, models.RegionIDPrefix), models.IDBodyLength())
	// Both are clamped to the floor; since the time part is the same they carry
	// a common start over the length of the stamp prefix.
	const stampPrefixLen = 9 // the Base32 equivalent of the 48-bit stamp
	assert.Equal(t,
		strings.TrimPrefix(epoch, models.RegionIDPrefix)[:stampPrefixLen],
		strings.TrimPrefix(old, models.RegionIDPrefix)[:stampPrefixLen],
	)
}
