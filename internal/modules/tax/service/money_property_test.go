package service

import (
	"fmt"
	"math"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/bdrtr/gobit/core/errors"
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

// drawStack draws a stack of one to maxStackDepth rates, the base first and
// never compound, the others compound or not. The rates together are drawn
// well under a whole line, within a few basis points of it, or over it, each
// as often as the others, since the check's boundary is where a stack is
// admitted wrongly.
func drawStack(t *rapid.T) []models.TaxRate {
	depth := rapid.IntRange(1, maxStackDepth).Draw(t, "depth")
	budget := rapid.OneOf(rapid.Int32Range(0, 9_989), rapid.Int32Range(9_990, 10_010),
		rapid.Int32Range(10_011, 12_000)).Draw(t, "rates together")
	stack := make([]models.TaxRate, 0, depth)
	for i := range depth {
		rate := budget
		if i < depth-1 {
			rate = rapid.Int32Range(0, budget).Draw(t, "rate")
		}
		budget -= rate
		stack = append(stack, models.TaxRate{
			ID: fmt.Sprintf("txr_%d", i), RateBps: min(rate, models.MaxRateBps),
			Compound: i > 0 && rapid.Bool().Draw(t, "compound"),
		})
	}

	return stack
}

// tableOf is a rate table whose one region's default rate starts the stack.
func tableOf(stack []models.TaxRate) rateTable {
	table := rateTable{
		chain:    []string{"txreg_1"},
		fallback: map[string]models.TaxRate{"txreg_1": stack[0]},
		standing: map[string]models.TaxRate{},
	}
	for i := 1; i < len(stack); i++ {
		table.standing[stack[i-1].ID] = stack[i]
	}

	return table
}

// TestAStackTheCheckAdmitsNeverTakesMoreThanTheLine is ADR 0249 on stacked
// rates: assertStackWithinBase admits a stack on a notional line with every
// component rounded up, and the property is the claim that check stands for.
// On any line the admitted stack takes no more than the line, each component
// is its own base times its rate rounded down, computed in arbitrary precision,
// with a compound component's base the line plus what the components before it
// took, and the components add up to the tax. A compound base past the taxable
// ceiling is refused.
func TestAStackTheCheckAdmitsNeverTakesMoreThanTheLine(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		stack := drawStack(t)
		if assertStackWithinBase(stack) != nil {
			t.Skip("the stack takes more than a line; the service refuses to write it")
		}
		amount := rapid.OneOf(rapid.Just(int64(0)), rapid.Int64Range(1, 1_000),
			rapid.Int64Range(0, MaxTaxableAmount), rapid.Int64Range(MaxTaxableAmount-3, MaxTaxableAmount)).Draw(t, "line")

		// A compound component's base is the line plus what came before it,
		// and a base past the taxable ceiling is refused rather than taxed.
		var prior int64
		pastCeiling := false
		for i := range stack {
			base := amount
			if stack[i].Compound {
				base += prior
			}
			pastCeiling = pastCeiling || base > MaxTaxableAmount
			step := new(big.Int).Mul(big.NewInt(base), big.NewInt(int64(stack[i].RateBps)))
			prior += step.Quo(step, big.NewInt(BpsScale)).Int64()
		}

		got, err := tableOf(stack).applyTo(nil, "line_1", amount, false)
		if pastCeiling {
			require.Error(t, err)
			require.Equal(t, CodeAmountOverflow, errors.CodeOf(err))
			return
		}
		require.NoError(t, err)

		require.LessOrEqual(t, got.TaxAmount, amount, "the stack takes no more than the line")
		require.Equal(t, amount, got.TaxableAmount)
		require.Equal(t, stack[0].ID, got.RateID)
		if len(stack) == 1 {
			require.Empty(t, got.Components)
			return
		}
		require.Len(t, got.Components, len(stack))
		var taken int64
		for i, component := range got.Components {
			base := amount
			if stack[i].Compound {
				base += taken
			}
			want := new(big.Int).Mul(big.NewInt(base), big.NewInt(int64(stack[i].RateBps)))
			want.Quo(want, big.NewInt(BpsScale))
			require.Equal(t, base, component.TaxableAmount, "component %d", i)
			require.Equal(t, want.Int64(), component.TaxAmount, "component %d", i)
			taken += component.TaxAmount
		}
		require.Equal(t, taken, got.TaxAmount, "the components add up to the tax")
	})
}

// TestAnIncludedTaxIsTakenOutOfOneRateOnly holds the inclusive side: one rate
// splits the line into base and tax exactly, and a stack is refused, since the
// reverse computation is defined for one rate (ADR 0086).
func TestAnIncludedTaxIsTakenOutOfOneRateOnly(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		stack := drawStack(t)
		amount := rapid.Int64Range(0, MaxTaxableAmount).Draw(t, "gross")

		got, err := tableOf(stack).applyTo(nil, "line_1", amount, true)

		if len(stack) > 1 {
			require.Error(t, err)
			return
		}
		require.NoError(t, err)
		require.Equal(t, amount, got.TaxableAmount+got.TaxAmount, "base and tax make the gross")
		require.GreaterOrEqual(t, got.TaxAmount, int64(0))
	})
}

// TestTheStackCheckRoundsUpForAStackThatPassesTheLineByAFraction is the stack
// the property's draws do not reach, computed rather than found (ADR 0080's
// rule for what a generator cannot find). Three rates of 3, 303 and 9406 basis
// points, the last two compound, take a line's whole notional base exactly
// when each component is rounded down, and one unit more when rounded up; on a
// real line of 10^12 they take 54 units more than the line. The check rounds
// up, so it refuses the stack.
func TestTheStackCheckRoundsUpForAStackThatPassesTheLineByAFraction(t *testing.T) {
	stack := []models.TaxRate{
		{ID: "txr_0", RateBps: 3},
		{ID: "txr_1", RateBps: 303, Compound: true},
		{ID: "txr_2", RateBps: 9406, Compound: true},
	}

	err := assertStackWithinBase(stack)
	require.Error(t, err)
	require.Equal(t, CodeStackExceedsBase, errors.CodeOf(err))

	got, err := tableOf(stack).applyTo(nil, "line_1", 1_000_000_000_000, false)
	require.NoError(t, err)
	require.Equal(t, int64(1_000_000_000_054), got.TaxAmount, "why the check refuses it")
}
