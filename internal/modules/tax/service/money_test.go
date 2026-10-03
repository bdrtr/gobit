package service

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// TestTaxOfBasisPointArithmetic checks the basis point arithmetic and the
// rounding DIRECTION.
func TestTaxOfBasisPointArithmetic(t *testing.T) {
	tests := []struct {
		name    string
		base    int64
		rateBps int32
		want    int64
	}{
		{"zero rate", 100_000, 0, 0},
		{"zero base", 0, 2000, 0},
		{"exact division", 10_000, 2000, 2000},
		{"one hundred percent gives the base", 12_345, 10_000, 12_345},
		{"one basis point", 10_000, 1, 1},
		{"below one basis point becomes zero", 9_999, 1, 0},
		{"a fraction goes down", 1_999, 1800, 359},
		{"an exact half goes down", 5, 5000, 2},
		{"a large base with a remainder", 123_456_789, 1850, 22_839_505},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := TaxOf(tt.base, tt.rateBps)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestTaxOfDividingFirstDoesNotChangeTheResult proves that the "divide first"
// optimization is mathematically equivalent, by COMPARING it with the direct
// product on bases that do not overflow.
//
// The direct product is safe only here: the chosen bases are small and
// base × rate fits into int64. In the real implementation the base can go up
// to 10^18, so that path cannot be used — which is exactly why the test
// exists.
func TestTaxOfDividingFirstDoesNotChangeTheResult(t *testing.T) {
	bases := []int64{0, 1, 7, 99, 100, 4_999, 10_000, 10_001, 123_456, 999_999_999}
	rates := []int32{0, 1, 18, 100, 725, 1800, 2000, 9_999, 10_000}

	for _, base := range bases {
		for _, rate := range rates {
			got, err := TaxOf(base, rate)
			require.NoError(t, err)
			assert.Equal(t, base*int64(rate)/BpsScale, got,
				"base=%d rate=%d", base, rate)
		}
	}
}

// TestTaxOfABaseAtTheCeilingDoesNotOverflow checks that the largest base does
// not overflow even at the largest rate.
//
// The direct base × rate calculation would produce 10^22 here and exceed
// int64; the test staying green is the proof that the division was moved
// first.
func TestTaxOfABaseAtTheCeilingDoesNotOverflow(t *testing.T) {
	got, err := TaxOf(MaxTaxableAmount, models.MaxRateBps)
	require.NoError(t, err)
	assert.Equal(t, MaxTaxableAmount, got)
	assert.Positive(t, got, "an overflowing product would produce a negative value or zero")
}

// TestTaxOfRejectsOutOfContractInput checks how an out-of-bounds base and rate
// are classified.
func TestTaxOfRejectsOutOfContractInput(t *testing.T) {
	t.Run("a negative base is an internal error", func(t *testing.T) {
		_, err := TaxOf(-1, 2000)
		require.Error(t, err)
		assert.True(t, errors.HasKind(err, errors.KindInternal),
			"a negative base is the code's error, not the caller's")
	})

	t.Run("a base above the ceiling is a client error", func(t *testing.T) {
		_, err := TaxOf(MaxTaxableAmount+1, 2000)
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err))
		assert.Equal(t, CodeAmountOverflow, errors.CodeOf(err))
	})

	t.Run("rate out of range", func(t *testing.T) {
		for _, rate := range []int32{-1, models.MaxRateBps + 1, math.MaxInt32} {
			_, err := TaxOf(1000, rate)
			require.Error(t, err, "rate=%d", rate)
			assert.Equal(t, CodeRateOutOfRange, errors.CodeOf(err))
		}
	})
}

// TestAddAmountRejectsOverflow checks that the addition does not wrap silently.
func TestAddAmountRejectsOverflow(t *testing.T) {
	sum, err := addAmount(MaxTaxableAmount-1, 1)
	require.NoError(t, err)
	assert.Equal(t, MaxTaxableAmount, sum)

	_, err = addAmount(MaxTaxableAmount, 1)
	require.Error(t, err)
	assert.Equal(t, CodeAmountOverflow, errors.CodeOf(err))

	_, err = addAmount(math.MaxInt64, math.MaxInt64)
	require.Error(t, err, "at the int64 ceiling an error is expected, not a wrap")

	_, err = addAmount(-1, 1)
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInternal))
}

// TestCheckTaxableAmountBounds checks the base check.
func TestCheckTaxableAmountBounds(t *testing.T) {
	require.NoError(t, checkTaxableAmount("base", 0))
	require.NoError(t, checkTaxableAmount("base", MaxTaxableAmount))

	err := checkTaxableAmount("base", -1)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a negative base must not be silently pulled up to zero")

	err = checkTaxableAmount("base", MaxTaxableAmount+1)
	require.Error(t, err)
	assert.Equal(t, CodeAmountOverflow, errors.CodeOf(err))
}
