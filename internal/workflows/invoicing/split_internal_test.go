package invoicing

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// TestApportionGivesTheRemainderToTheLargestRemainders: the shares round
// down, the units left over go to the largest remainders, and a tie goes to
// the earlier position.
func TestApportionGivesTheRemainderToTheLargestRemainders(t *testing.T) {
	t.Parallel()

	shares, err := apportion(10, []int64{1, 1, 1})
	require.NoError(t, err)
	assert.Equal(t, []int64{4, 3, 3}, shares, "three equal remainders: the first takes the unit")

	shares, err = apportion(100, []int64{1, 2, 7})
	require.NoError(t, err)
	assert.Equal(t, []int64{10, 20, 70}, shares)

	shares, err = apportion(7, []int64{5, 3, 2})
	require.NoError(t, err)
	assert.Equal(t, []int64{4, 2, 1}, shares, "3.5 2.1 1.4: the half goes to the first")

	shares, err = apportion(5, []int64{0, 3, 3})
	require.NoError(t, err)
	assert.Equal(t, []int64{0, 3, 2}, shares)
}

// TestApportionCarriesWhat64BitsCannot: amount times weight past 2^63 still
// shares exactly.
func TestApportionCarriesWhat64BitsCannot(t *testing.T) {
	t.Parallel()

	large := int64(math.MaxInt64 / 2)
	shares, err := apportion(large, []int64{large, large})
	require.NoError(t, err)
	assert.Equal(t, large, shares[0]+shares[1])
	assert.Equal(t, []int64{large/2 + 1, large / 2}, shares)
}

// TestApportionRefusesWhatItCannotShare: weights adding to zero share nothing
// but nothing, and a negative amount or weight is a fault.
func TestApportionRefusesWhatItCannotShare(t *testing.T) {
	t.Parallel()

	shares, err := apportion(0, []int64{0, 0})
	require.NoError(t, err)
	assert.Equal(t, []int64{0, 0}, shares)

	_, err = apportion(1, []int64{0, 0})
	require.Error(t, err, "no division by zero")
	_, err = apportion(-1, []int64{1})
	require.Error(t, err)
	_, err = apportion(1, []int64{-1, 2})
	require.Error(t, err)
}

// TestAPartOfAStackedRowPrintsItsRatesOnTheirBase: a part of a row taxed 5%
// and 8% compound takes the row's ratio of tax rounded down, shares it over the
// rates by the largest remainder, and prints each rate on its base's part
// rounded up.
func TestAPartOfAStackedRowPrintsItsRatesOnTheirBase(t *testing.T) {
	t.Parallel()

	row := saleRow{
		LineID: "invl_stacked", Total: 2268, TaxTotal: 268, TaxRateBps: 500, LeftTotal: 2268, LeftTax: 268,
		Description: "Stacked", Quantity: 1,
		Components: []saleRowComponent{
			{RateBps: 500, TaxableAmount: 2000, TaxAmount: 100, LeftTax: 100},
			{RateBps: 800, Compound: true, TaxableAmount: 2100, TaxAmount: 168, LeftTax: 168},
		},
	}
	line := amendingLine(rowPart{row: &row, amount: 1000}, false)
	assert.Equal(t, int64(118), line.TaxTotal, "1000 x 268 / 2268 = 118.16")
	assert.Equal(t, int64(882), line.Subtotal)
	require.Len(t, line.TaxComponents, 2)
	assert.Equal(t, documentLineTax{RateBps: 500, TaxableAmount: 882, TaxAmount: 44}, line.TaxComponents[0],
		"2000 x 1000 / 2268 = 881.8 rounds up; 118 x 100 / 268 = 44.03")
	assert.Equal(t, documentLineTax{RateBps: 800, Compound: true, TaxableAmount: 926, TaxAmount: 74},
		line.TaxComponents[1], "2100 x 1000 / 2268 = 925.9 rounds up; 73.97 takes the remainder")
}

// TestTheRoundingOfARowIsNotCollectedByItsLastPart: a row of 1180 with 180 tax
// given back as 6, 1173 and 1 rounds each part on the running total, so the
// last unit takes the one unit of tax it is owed and not the 2 the parts
// before it left behind.
func TestTheRoundingOfARowIsNotCollectedByItsLastPart(t *testing.T) {
	t.Parallel()

	row := saleRow{LineID: "invl_1", Total: 1180, TaxTotal: 180, TaxRateBps: 1800, LeftTotal: 1180, LeftTax: 180}
	var taxes []int64
	for _, piece := range []int64{6, 1173, 1} {
		line := amendingLine(rowPart{row: &row, amount: piece}, false)
		require.GreaterOrEqual(t, line.Subtotal, int64(0), "piece %d", piece)
		taxes = append(taxes, line.TaxTotal)
		row.LeftTotal -= piece
		row.LeftTax -= line.TaxTotal
	}
	assert.Equal(t, []int64{0, 179, 1}, taxes)
	assert.Zero(t, row.LeftTax, "the row gave back exactly its tax")

	// A refund issued past the flow gave back 1179 and no tax; the unit left
	// takes at most itself.
	given := saleRow{LineID: "invl_2", Total: 1180, TaxTotal: 180, TaxRateBps: 1800, LeftTotal: 1, LeftTax: 180}
	line := amendingLine(rowPart{row: &given, amount: 1}, false)
	assert.Equal(t, int64(1), line.TaxTotal, "a part's tax is within the part")
	assert.Zero(t, line.Subtotal)

	// A refund past the flow gave back half the row and none of its tax; the
	// half that empties the row takes all the tax it has left.
	half := saleRow{LineID: "invl_3", Total: 1180, TaxTotal: 180, TaxRateBps: 1800, LeftTotal: 590, LeftTax: 180}
	line = amendingLine(rowPart{row: &half, amount: 590}, false)
	assert.Equal(t, int64(180), line.TaxTotal, "the part that empties a row takes the tax it has left")
}

// TestAFreeLineReturnDoesNotDivideByZero: a returned line that cost nothing
// carries nothing, and the refund falls on the carriage.
func TestAFreeLineReturnDoesNotDivideByZero(t *testing.T) {
	t.Parallel()

	rows := []saleRow{
		{LineID: "invl_free", Total: 0, Quantity: 1, OrderLineID: "li_free", Description: "Sample"},
		{LineID: "invl_carriage", Total: 3000, LeftTotal: 3000, Quantity: 1, Carriage: true},
	}
	act := actOfOrder{Kind: actReturnRefunded, Amount: 500, Documentable: true}
	act.Returned = append(act.Returned, struct {
		LineID   string `json:"line_id"`
		Quantity int64  `json:"quantity"`
	}{LineID: "li_free", Quantity: 1})

	parts, err := splitAct(act, rows, nil)
	require.NoError(t, err)
	require.Len(t, parts, 1)
	assert.Equal(t, "invl_carriage", parts[0].row.LineID)
	assert.Equal(t, int64(500), parts[0].amount)
}

// TestTheSplitHoldsItsIdentities is the property ADR 0406 rests on, over any
// rows and any amount they can carry: the parts add up to the amount, none is
// more than its row has left, each row's rates add up to its tax on bases at
// least their tax, and a row given back in any number of parts gives back
// exactly the tax it charged, under each of its rates.
func TestTheSplitHoldsItsIdentities(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		count := rapid.IntRange(1, 5).Draw(t, "rows")
		rows := make([]saleRow, count)
		var left int64
		for i := range rows {
			total := rapid.Int64Range(0, 1_000_000).Draw(t, "total")
			taxed := rapid.Int64Range(0, total).Draw(t, "tax")
			rows[i] = saleRow{LineID: "row", Total: total, TaxTotal: taxed, LeftTotal: total, LeftTax: taxed}
			if rapid.Bool().Draw(t, "stacked") && taxed > 0 {
				base := rapid.Int64Range(0, taxed).Draw(t, "base tax")
				rows[i].Components = []saleRowComponent{
					{RateBps: 500, TaxableAmount: total - taxed, TaxAmount: base, LeftTax: base},
					{RateBps: 800, Compound: true, TaxableAmount: total - taxed + base,
						TaxAmount: taxed - base, LeftTax: taxed - base},
				}
			}
			left += total
		}
		amount := rapid.Int64Range(1, max(left, 1)).Draw(t, "amount")
		if left == 0 {
			return
		}
		tier := make([]int, count)
		for i := range tier {
			tier[i] = i
		}

		parts, rest, err := fillTier(amount, rows, tier, func(i int) int64 { return rows[i].LeftTotal })
		require.NoError(t, err)
		require.Zero(t, rest)
		var sum int64
		for _, part := range parts {
			sum += part.amount
			require.LessOrEqual(t, part.amount, part.row.LeftTotal)
			line := amendingLine(part, false)
			holdsItsRates(t, line)
		}
		require.Equal(t, amount, sum)

		// One row given back in pieces, each piece's tax taken off what it has
		// left, as the invoice module's sums would.
		row := rows[rapid.IntRange(0, count-1).Draw(t, "given back")]
		givenTax, givenRates := int64(0), make([]int64, len(row.Components))
		for row.LeftTotal > 0 {
			piece := rapid.Int64Range(1, row.LeftTotal).Draw(t, "piece")
			line := amendingLine(rowPart{row: &row, amount: piece}, rapid.Bool().Draw(t, "inclusive"))
			holdsItsRates(t, line)
			require.LessOrEqual(t, line.TaxTotal, row.LeftTax)
			row.LeftTotal -= piece
			row.LeftTax -= line.TaxTotal
			givenTax += line.TaxTotal
			for k := range line.TaxComponents {
				row.Components[k].LeftTax -= line.TaxComponents[k].TaxAmount
				require.GreaterOrEqual(t, row.Components[k].LeftTax, int64(0))
				givenRates[k] += line.TaxComponents[k].TaxAmount
			}
		}
		require.Equal(t, row.TaxTotal, givenTax, "a row given back whole gives back its tax")
		for k := range row.Components {
			require.Equal(t, row.Components[k].TaxAmount, givenRates[k])
		}
	})
}

// holdsItsRates checks a printed row's rates against its tax, and that the
// row moves money one way: no part takes more tax than its own amount.
func holdsItsRates(t *rapid.T, line documentLine) {
	require.GreaterOrEqual(t, line.TaxTotal, int64(0))
	require.LessOrEqual(t, line.TaxTotal, line.Total, "a part's tax is within the part")
	require.GreaterOrEqual(t, line.Subtotal, int64(0))
	require.GreaterOrEqual(t, line.UnitPrice, int64(0))
	if len(line.TaxComponents) == 0 {
		return
	}
	var sum int64
	for _, component := range line.TaxComponents {
		require.GreaterOrEqual(t, component.TaxableAmount, component.TaxAmount)
		require.LessOrEqual(t, component.TaxAmount, line.TaxTotal, "a rate's tax is within the part's")
		sum += component.TaxAmount
	}
	require.Equal(t, line.TaxTotal, sum)
	require.Equal(t, line.Total, line.Subtotal+line.TaxTotal)
}
