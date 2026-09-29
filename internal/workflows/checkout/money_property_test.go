package checkout

import (
	"math"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// amountsNear draws the int64s money arithmetic meets at its edges as well as
// in the middle: around zero, around the ceiling, inside it and anywhere
// (ADR 0249).
//
// Each class is drawn as often as the others, so zero against a negative, the
// pair a shortcut for zero can let past, turns up in one draw of about
// twenty-five rather than by luck.
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

// TestMulAmountIsTheProductOrARefusal holds the multiplication to its
// own words: two factors that are not negative and whose product is within
// MaxTotal give the product, and anything else is refused (ADR 0249).
func TestMulAmountIsTheProductOrARefusal(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		price := amountsNear(MaxAmount).Draw(t, "unit_price")
		quantity := amountsNear(MaxQuantity).Draw(t, "quantity")

		got, err := mulAmount(price, quantity)

		want := new(big.Int).Mul(big.NewInt(price), big.NewInt(quantity))
		if price < 0 || quantity < 0 || want.Cmp(big.NewInt(MaxTotal)) > 0 {
			require.Error(t, err)
			return
		}
		require.NoError(t, err)
		require.Equal(t, want.Int64(), got)
	})
}

// TestAddAmountIsTheSumOrARefusal is the same for the addition.
func TestAddAmountIsTheSumOrARefusal(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := amountsNear(MaxTotal).Draw(t, "a")
		b := amountsNear(MaxTotal).Draw(t, "b")

		got, err := addAmount(a, b)

		want := new(big.Int).Add(big.NewInt(a), big.NewInt(b))
		if a < 0 || b < 0 || want.Cmp(big.NewInt(MaxTotal)) > 0 {
			require.Error(t, err)
			return
		}
		require.NoError(t, err)
		require.Equal(t, want.Int64(), got)
	})
}
