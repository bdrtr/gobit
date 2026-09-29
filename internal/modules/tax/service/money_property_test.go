package service

import (
	"math"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// amountsNear draws the int64s money arithmetic meets at its edges as well as
// in the middle: zero, below zero, just above it, around the ceiling, inside
// it and anywhere, each class as often as the others (ADR 0249).
func amountsNear(ceiling int64) *rapid.Generator[int64] {
	return rapid.OneOf(
		rapid.Just(int64(0)),
		rapid.Int64Range(math.MinInt64, -1),
		rapid.Int64Range(1, 3),
		rapid.Int64Range(ceiling-3, ceiling+3),
		rapid.Int64Range(0, ceiling),
		rapid.Int64(),
	)
}

// rates draws every rate the contract admits and the two just outside it.
func rates() *rapid.Generator[int32] {
	return rapid.Int32Range(models.MinRateBps-2, models.MaxRateBps+2)
}

// TestTaxOfIsTheFlooredProduct holds the tax to base x rate / 10000 rounded
// down, computed in arbitrary precision, for every base up to the taxable
// ceiling; anything outside the contract is refused.
func TestTaxOfIsTheFlooredProduct(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		base := amountsNear(MaxTaxableAmount).Draw(t, "base")
		rate := rates().Draw(t, "rate_bps")

		got, err := TaxOf(base, rate)

		if base < 0 || base > MaxTaxableAmount || rate < models.MinRateBps || rate > models.MaxRateBps {
			require.Error(t, err)
			return
		}
		require.NoError(t, err)
		want := new(big.Int).Mul(big.NewInt(base), big.NewInt(int64(rate)))
		want.Quo(want, big.NewInt(BpsScale))
		require.Equal(t, want.Int64(), got)
	})
}

// TestTaxingTheExtractedNetIsNeverLessAndAtMostAUnitMore is ADR 0086's
// measured claim as a property: taking the tax out of a gross and taxing what
// is left the ordinary way gives the tax that was taken out, or one minor unit
// more — never less, never two more. It is why the extraction is its own
// arithmetic.
func TestTaxingTheExtractedNetIsNeverLessAndAtMostAUnitMore(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		gross := rapid.Int64Range(0, MaxTaxableAmount).Draw(t, "gross")
		rate := rapid.Int32Range(models.MinRateBps, models.MaxRateBps).Draw(t, "rate_bps")

		included, err := TaxIncludedIn(gross, rate)
		require.NoError(t, err)
		retaxed, err := TaxOf(gross-included, rate)
		require.NoError(t, err)

		require.GreaterOrEqual(t, retaxed-included, int64(0))
		require.LessOrEqual(t, retaxed-included, int64(1))
	})
}
