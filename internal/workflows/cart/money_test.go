package cart

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// TestTaxOfRoundingAndLimits pins down the contract of the basis point arithmetic.
func TestTaxOfRoundingAndLimits(t *testing.T) {
	tests := map[string]struct {
		base    int64
		rateBps int32
		want    int64
	}{
		"divides evenly":       {base: 1000, rateBps: 2000, want: 200},
		"rounds down":          {base: 101, rateBps: 1850, want: 18},
		"one short stays zero": {base: 5, rateBps: 1000, want: 0},
		"zero rate":            {base: 999_999, rateBps: 0, want: 0},
		"zero base":            {base: 0, rateBps: 2000, want: 0},
		"full rate":            {base: 12_345, rateBps: MaxTaxRateBps, want: 12_345},
		"largest base":         {base: MaxTotal, rateBps: MaxTaxRateBps, want: MaxTotal},
		"largest base partial": {base: MaxTotal, rateBps: 1, want: MaxTotal / 10_000},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := taxOf(tc.base, tc.rateBps)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestTaxOfDoesNotOverflowOnLargeBase proves that the division is done BEFORE the
// multiplication.
//
// Had base x rate been computed directly, 10^18 x 10^4 would overflow an int64 and
// the result would silently come out negative. The test asks for exactly that value.
func TestTaxOfDoesNotOverflowOnLargeBase(t *testing.T) {
	got, err := taxOf(MaxTotal, 2000)
	require.NoError(t, err)

	assert.Positive(t, got, "an overflowing product would have produced a negative result")
	assert.Equal(t, MaxTotal/10_000*2000, got)
}

// TestTaxOfRejectsRateOutsideTheContract verifies that a rate outside the range is
// not computed.
func TestTaxOfRejectsRateOutsideTheContract(t *testing.T) {
	for _, rate := range []int32{-1, MaxTaxRateBps + 1} {
		_, err := taxOf(1000, rate)
		require.Error(t, err)
		assert.Equal(t, CodeTaxRateInvalid, errors.CodeOf(err))
	}
}

// TestTaxOfRejectsBaseOutsideTheLimits verifies that a base above the ceiling or a
// negative one is rejected.
func TestTaxOfRejectsBaseOutsideTheLimits(t *testing.T) {
	_, err := taxOf(MaxTotal+1, 100)
	require.Error(t, err)
	assert.Equal(t, CodeAmountOverflow, errors.CodeOf(err))

	_, err = taxOf(-1, 100)
	require.Error(t, err)
	assert.Equal(t, CodeAmountOverflow, errors.CodeOf(err))
}

// TestAddAmountCatchesOverflow verifies that the addition returns an error when it
// exceeds the limit.
func TestAddAmountCatchesOverflow(t *testing.T) {
	sum, err := addAmount(MaxTotal-1, 1)
	require.NoError(t, err)
	assert.Equal(t, MaxTotal, sum)

	_, err = addAmount(MaxTotal, 1)
	require.Error(t, err)
	assert.Equal(t, CodeAmountOverflow, errors.CodeOf(err))

	_, err = addAmount(-1, 1)
	require.Error(t, err)
}

// TestMulAmountCatchesOverflow verifies that the product returns an error when it
// exceeds the limit.
func TestMulAmountCatchesOverflow(t *testing.T) {
	product, err := mulAmount(MaxAmount, MaxQuantity)
	require.NoError(t, err)
	assert.Equal(t, MaxTotal, product)

	_, err = mulAmount(MaxAmount, MaxQuantity+1)
	require.Error(t, err)
	assert.Equal(t, CodeAmountOverflow, errors.CodeOf(err))

	zero, err := mulAmount(0, MaxQuantity)
	require.NoError(t, err)
	assert.Zero(t, zero)
}

// TestQuantity32EnforcesTheLimits verifies that the quantity conversion returns an
// error outside the limits.
func TestQuantity32EnforcesTheLimits(t *testing.T) {
	got, err := quantity32(MaxQuantity)
	require.NoError(t, err)
	assert.Equal(t, int32(MaxQuantity), got)

	for _, quantity := range []int64{0, -1, MaxQuantity + 1, 1 << 40} {
		_, err := quantity32(quantity)
		require.Error(t, err, "the quantity %d must not be accepted", quantity)
		assert.True(t, errors.IsInvalid(err))
	}
}

// TestRequireIDRejectsAnIDWithWhitespace verifies that the ID is rejected rather
// than trimmed.
func TestRequireIDRejectsAnIDWithWhitespace(t *testing.T) {
	require.NoError(t, requireID("cart_id", "cart_1"))

	for _, value := range []string{"", " cart_1", "cart_1 ", "cart_1\n"} {
		err := requireID("cart_id", value)
		require.Error(t, err, "%q must not be accepted", value)
		assert.True(t, errors.IsInvalid(err))
	}
}
