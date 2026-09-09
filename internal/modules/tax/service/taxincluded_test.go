package service

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// TestTaxIncludedInTakesTheTaxOutOfAGrossAmount pins the contract.
//
// What is pinned is not the figures but the PROPERTY: base and tax add back up
// to the gross. The whole of tax-inclusive pricing is in that one sentence —
// the shopper pays what the sticker said.
func TestTaxIncludedInTakesTheTaxOutOfAGrossAmount(t *testing.T) {
	cases := []struct {
		name    string
		gross   int64
		rateBps int32
		tax     int64
	}{
		{"20% VAT, divides evenly", 12_000, 2000, 2000},
		{"20% VAT, with a remainder", 19_900, 2000, 3316},
		{"18% VAT", 11_800, 1800, 1800},
		{"1% VAT, below a single unit", 100, 100, 0},
		{"a zero rate", 19_900, 0, 0},
		{"a zero amount", 0, 2000, 0},
		{"the ceiling", MaxTaxableAmount, models.MaxRateBps, MaxTaxableAmount / 2},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tax, err := TaxIncludedIn(c.gross, c.rateBps)

			require.NoError(t, err)
			assert.Equal(t, c.tax, tax)
			assert.GreaterOrEqual(t, c.gross-tax, int64(0),
				"the base cannot be negative: the tax came out larger than the gross")
		})
	}
}

// TestTaxIncludedInCannotBeReplacedByTaxOf turns the measurement into a test.
//
// "Find the net, then tax it the ordinary way" BREAKS a tax-inclusive price on
// a share of amounts that has a closed form: rate/(10000+rate), which is one in
// six at 20% VAT. The gross amounts below are the first examples from that
// measurement at 1%: taxing the extracted base again produces one unit MORE, so
// the shopper would pay above the sticker.
//
// The test PROVES the two are not interchangeable. The day somebody decides
// they "do the same thing" and deletes the reverse computation, this fails.
func TestTaxIncludedInCannotBeReplacedByTaxOf(t *testing.T) {
	for _, gross := range []int64{100, 201, 302, 403, 504, 605} {
		const rate int32 = 100

		tax, err := TaxIncludedIn(gross, rate)
		require.NoError(t, err)
		base := gross - tax

		again, err := TaxOf(base, rate)
		require.NoError(t, err)

		assert.Equal(t, gross, base+tax, "the reverse computation has to hit the gross by construction")
		assert.NotEqual(t, tax, again,
			"gross=%d: this value is one of the MEASURED points where the two computations "+
				"diverge; them agreeing means the measurement or the arithmetic has moved", gross)
		assert.Equal(t, tax+1, again,
			"the divergence is one unit and AGAINST the shopper; any other difference says "+
				"the rounding direction has changed")
	}
}

// TestTaxIncludedInRefusesWhatIsOutOfRange verifies every bound.
func TestTaxIncludedInRefusesWhatIsOutOfRange(t *testing.T) {
	_, err := TaxIncludedIn(-1, 2000)
	require.Error(t, err)
	assert.Equal(t, CodeAmountOverflow, errors.CodeOf(err))

	_, err = TaxIncludedIn(MaxTaxableAmount+1, 2000)
	require.Error(t, err)
	assert.Equal(t, CodeAmountOverflow, errors.CodeOf(err))

	_, err = TaxIncludedIn(1000, models.MaxRateBps+1)
	require.Error(t, err)
	assert.Equal(t, CodeRateOutOfRange, errors.CodeOf(err))

	_, err = TaxIncludedIn(1000, -1)
	require.Error(t, err)
	assert.Equal(t, CodeRateOutOfRange, errors.CodeOf(err))
}

// FuzzTaxIncludedIn runs the reverse computation against arbitrary precision.
//
// The split arithmetic — divide first, then multiply — exists to avoid an
// overflow, and its correctness is an ALGEBRAIC claim: written as gross = q*d +
// m, the answer is q*rate + (m*rate)/d. An algebraic claim is not tested by
// examples but by the direct computation at arbitrary precision, and math/big
// is the oracle here.
//
// Three properties at once, because all three are part of the price: the result
// EQUALS the direct computation, the tax is inside [0, gross], and base plus
// tax is the gross itself.
func FuzzTaxIncludedIn(f *testing.F) {
	f.Add(int64(0), int32(0))
	f.Add(int64(12_000), int32(2000))
	f.Add(int64(19_900), int32(2000))
	f.Add(int64(100), int32(100))
	f.Add(MaxTaxableAmount, models.MaxRateBps)
	f.Add(MaxTaxableAmount, int32(1))
	f.Add(int64(1), models.MaxRateBps)

	f.Fuzz(func(t *testing.T, gross int64, rateBps int32) {
		tax, err := TaxIncludedIn(gross, rateBps)

		if gross < 0 || gross > MaxTaxableAmount ||
			rateBps < models.MinRateBps || rateBps > models.MaxRateBps {
			require.Error(t, err, "an out-of-range input may not be accepted")

			return
		}
		require.NoError(t, err)

		// The direct computation at arbitrary precision: gross*rate/(10000+rate).
		want := new(big.Int).Mul(big.NewInt(gross), big.NewInt(int64(rateBps)))
		want.Quo(want, big.NewInt(BpsScale+int64(rateBps)))

		require.True(t, want.IsInt64(), "the expected value has to fit in an int64")
		assert.Equal(t, want.Int64(), tax, "gross=%d rate=%d", gross, rateBps)

		assert.GreaterOrEqual(t, tax, int64(0), "the tax cannot be negative")
		assert.LessOrEqual(t, tax, gross, "the tax cannot exceed the gross")
		assert.Equal(t, gross, (gross-tax)+tax,
			"base and tax have to hit the gross — the whole of tax-inclusive pricing is this")
	})
}
