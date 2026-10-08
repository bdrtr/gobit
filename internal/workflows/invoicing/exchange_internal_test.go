package invoicing

import (
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// TestAStackedRowTakenBackInPartsNeverOverdrawsARate is ADR 0432's refund on a
// two-rate stack at any price: any sequence of exchanges by units and returns
// by amount that fits the row's total and tax is documented without a
// refusal, every part's rates add up to its tax, and what the parts give back
// under each rate never exceeds what the rate charged.
func TestAStackedRowTakenBackInPartsNeverOverdrawsARate(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		quantity := rapid.Int64Range(2, 6).Draw(t, "units")
		price := rapid.Int64Range(100, 50_000).Draw(t, "unit price")
		base := price * quantity
		first := base * int64(rapid.Int32Range(100, 2_000).Draw(t, "first rate")) / 10_000
		second := (base + first) * int64(rapid.Int32Range(100, 1_000).Draw(t, "second rate")) / 10_000
		row := saleRow{
			LineID: "invl_1", Position: 1, Quantity: quantity, Description: "Bottle", OrderLineID: "li_1",
			Total: base + first + second, TaxTotal: first + second, TaxRateBps: 1000,
			LeftTotal: base + first + second, LeftTax: first + second,
			Components: []saleRowComponent{
				{RateID: "r1", RateBps: 1000, TaxableAmount: base, TaxAmount: first, LeftTax: first},
				{RateID: "r2", RateBps: 500, Compound: true, TaxableAmount: base + first, TaxAmount: second, LeftTax: second},
			},
		}

		for range rapid.IntRange(1, 6).Draw(t, "acts") {
			var line documentLine
			if rapid.Bool().Draw(t, "an exchange") {
				units := rapid.Int64Range(1, quantity).Draw(t, "units back")
				if mulDiv(row.Total, units, quantity, false) > row.LeftTotal ||
					mulDiv(row.TaxTotal, units, quantity, false) > row.LeftTax {
					continue
				}
				var err error
				line, err = returnedUnitsLine(&row, units, false)
				require.NoError(t, err, "units that fit the row's total and tax are given back")
			} else {
				if row.LeftTotal == 0 {
					continue
				}
				amount := rapid.Int64Range(1, row.LeftTotal).Draw(t, "amount back")
				line = amendingLine(rowPart{row: &row, amount: amount}, false)
			}

			var taxes int64
			for i, component := range line.TaxComponents {
				require.GreaterOrEqual(t, component.TaxAmount, int64(0))
				require.LessOrEqual(t, component.TaxAmount, component.TaxableAmount)
				require.LessOrEqual(t, component.TaxAmount, row.Components[i].LeftTax, "rate %d over-drawn", i+1)
				taxes += component.TaxAmount
				row.Components[i].LeftTax -= component.TaxAmount
			}
			require.Equal(t, line.TaxTotal, taxes, "the rates add up to the part's tax")
			require.LessOrEqual(t, line.Total, row.LeftTotal)
			row.LeftTotal -= line.Total
			row.LeftTax -= line.TaxTotal
		}
	})
}

// TestAnOverdrawnRatesExcessGoesToTheMostRoom: three rates that charged 100
// each share 150 as 50 apiece; the second has 40 left, so its 10 go to the
// rate with the most room, the first, and on a tie to the earlier rate.
func TestAnOverdrawnRatesExcessGoesToTheMostRoom(t *testing.T) {
	t.Parallel()

	row := func(left ...int64) *saleRow {
		r := &saleRow{Position: 1}
		for _, l := range left {
			r.Components = append(r.Components, saleRowComponent{TaxableAmount: 1000, TaxAmount: 100, LeftTax: l})
		}
		return r
	}
	shares, err := rateShares(row(100, 40, 60), 150)
	require.NoError(t, err)
	require.Equal(t, []int64{60, 40, 50}, shares, "the first rate has 50 of room, the third 10")

	shares, err = rateShares(row(70, 40, 70), 150)
	require.NoError(t, err)
	require.Equal(t, []int64{60, 40, 50}, shares, "a tie of room goes to the earlier rate")

	_, err = rateShares(row(50, 40, 50), 150)
	require.Error(t, err, "140 of room cannot take 150")
}

// TestARowWithMoreTaxThanTotalLeftKeepsTheUnitsShare: a row an admin refund
// gave back off its ratio (2 000 with 400 tax of 3 000 with 1 500) has 1 000
// left with 1 100 tax; the unit that takes the 1 000 cannot take the 1 100,
// which would leave the part a negative net, and takes its share, 500.
func TestARowWithMoreTaxThanTotalLeftKeepsTheUnitsShare(t *testing.T) {
	t.Parallel()

	row := saleRow{
		LineID: "invl_1", Position: 1, Quantity: 3, Description: "Bottle", OrderLineID: "li_1",
		Total: 3000, TaxTotal: 1500, TaxRateBps: 10_000, LeftTotal: 1000, LeftTax: 1100,
	}
	line, err := returnedUnitsLine(&row, 1, false)
	require.NoError(t, err)
	require.Equal(t, [2]int64{1000, 500}, [2]int64{line.Total, line.TaxTotal})
	require.Equal(t, int64(500), line.UnitPrice, "the part's net is not negative")
}
