package service

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// TestALineSentInPartSharesItsStackByWhatEachRateCharged prices units of a
// stacked line: the item's tax is the line's shared by units, and the rates
// share that tax by what each charged, the largest remainder first, so the
// components add up to the item's tax (ADR 0432).
func TestALineSentInPartSharesItsStackByWhatEachRateCharged(t *testing.T) {
	line := models.OrderLineItem{
		ID: "oli_STACK", Quantity: 7, UnitPrice: 1000, Subtotal: 7000, TaxRateBps: 500,
		TaxTotal: 913, Total: 7913,
		TaxComponents: []models.OrderLineTax{
			{RateID: "txr_base", RateBps: 500, TaxableAmount: 7000, TaxAmount: 350},
			{RateID: "txr_top", RateBps: 800, Compound: true, TaxableAmount: 7350, TaxAmount: 563},
		},
	}

	price := linePrice(line, 0, 3)

	// floor(7913x3/7) = 3391, floor(913x3/7) = 391; 391 over 350:563 is
	// 149.89 and 241.10, the remainder unit to the larger fraction.
	assert.Equal(t, int64(3391), price.Total)
	assert.Equal(t, int64(391), price.TaxTotal)
	assert.Equal(t, []models.ReplacementItemTax{
		{RateID: "txr_base", RateBps: 500, TaxableAmount: 3000, TaxAmount: 150},
		{RateID: "txr_top", RateBps: 800, Compound: true, TaxableAmount: 3150, TaxAmount: 241},
	}, price.TaxComponents)
	assert.Equal(t, models.PricedByLine, price.PricedBy)
	assert.Equal(t, int32(500), price.TaxRateBps)
}

// TestTheGoodsBackAreWorthTheSameShareTheGoodsSentAre values a return by the
// rounding linePrice uses, line by line.
func TestTheGoodsBackAreWorthTheSameShareTheGoodsSentAre(t *testing.T) {
	lines := []models.OrderLineItem{
		{ID: "oli_A", Quantity: 3, Total: 3361},
		{ID: "oli_B", Quantity: 2, Total: 1415},
	}
	items := []models.ReturnItem{
		{OrderLineItemID: "oli_A", Quantity: 2},
		{OrderLineItemID: "oli_B", Quantity: 1},
	}

	assert.Equal(t, int64(2240+707), returnedWorth(lines, items))
	assert.Equal(t, linePrice(lines[0], 0, 2).Total+linePrice(lines[1], 0, 1).Total, returnedWorth(lines, items))
}

// TestUnitsSentInPiecesAddUpToTheirShare prices each item of a line as the
// units after those sent before it, so a line whose total leaves a remainder
// of two over its quantity still nets to the returned units' worth when its
// units go one replacement at a time (ADR 0432).
func TestUnitsSentInPiecesAddUpToTheirShare(t *testing.T) {
	line := models.OrderLineItem{
		ID: "oli_SPLIT", Quantity: 3, UnitPrice: 1000, Subtotal: 3000, DiscountTotal: 198,
		TaxRateBps: 2000, TaxTotal: 560, Total: 3362,
	}

	first, second := linePrice(line, 0, 1), linePrice(line, 1, 1)

	assert.Equal(t, int64(1120), first.Total, "floor(3362/3)")
	assert.Equal(t, int64(1121), second.Total, "floor(3362x2/3) - floor(3362/3)")
	assert.Equal(t, int64(186), first.TaxTotal)
	assert.Equal(t, int64(187), second.TaxTotal, "floor(560x2/3) - floor(560/3)")
	worth := returnedWorth([]models.OrderLineItem{line}, []models.ReturnItem{{OrderLineItemID: line.ID, Quantity: 2}})
	assert.Equal(t, worth, first.Total+second.Total, "two units in two pieces are worth two units")
	assert.Equal(t, linePrice(line, 0, 2).Total, first.Total+second.Total)
}

// TestAStackSentInPiecesGivesEachRateItsShareOfTheWhole shares a stacked
// line's tax over its rates cumulatively, so the pieces' rates add up to the
// rates of the units sent at once.
func TestAStackSentInPiecesGivesEachRateItsShareOfTheWhole(t *testing.T) {
	line := models.OrderLineItem{
		ID: "oli_STACK", Quantity: 7, UnitPrice: 1000, Subtotal: 7000, TaxRateBps: 500,
		TaxTotal: 913, Total: 7913,
		TaxComponents: []models.OrderLineTax{
			{RateID: "txr_base", RateBps: 500, TaxableAmount: 7000, TaxAmount: 350},
			{RateID: "txr_top", RateBps: 800, Compound: true, TaxableAmount: 7354, TaxAmount: 563},
		},
	}

	pieces := []*models.ReplacementPrice{linePrice(line, 0, 2), linePrice(line, 2, 3), linePrice(line, 5, 2)}

	var total, tax, base, top, topTaxable int64
	for _, p := range pieces {
		total += p.Total
		tax += p.TaxTotal
		base += p.TaxComponents[0].TaxAmount
		top += p.TaxComponents[1].TaxAmount
		topTaxable += p.TaxComponents[1].TaxableAmount
		assert.Equal(t, p.TaxTotal, p.TaxComponents[0].TaxAmount+p.TaxComponents[1].TaxAmount,
			"each piece's rates add up to its tax")
	}
	assert.Equal(t, line.Total, total, "all seven units are the line")
	assert.Equal(t, line.TaxTotal, tax)
	assert.Equal(t, int64(350), base, "each rate gives back what it charged")
	assert.Equal(t, int64(563), top)
	assert.Equal(t, int64(7354), topTaxable, "the bases add up too, where a share per piece would lose a unit")
}

// TestARateNeverGoesBelowNothing shares the item's own tax when the rates'
// cumulative shares would take one back: on a stack charging 1, 3 and 3 over
// six units, the tax for four units, 4, splits 0/2/2 while the tax for three,
// 3, split 1/1/1, so the fourth unit's first rate would be -1.
func TestARateNeverGoesBelowNothing(t *testing.T) {
	line := models.OrderLineItem{
		ID: "oli_PARADOX", Quantity: 6, UnitPrice: 100, Subtotal: 600, TaxRateBps: 10, TaxTotal: 7, Total: 607,
		TaxComponents: []models.OrderLineTax{
			{RateID: "txr_a", RateBps: 10, TaxableAmount: 600, TaxAmount: 1},
			{RateID: "txr_b", RateBps: 50, Compound: true, TaxableAmount: 601, TaxAmount: 3},
			{RateID: "txr_c", RateBps: 50, Compound: true, TaxableAmount: 604, TaxAmount: 3},
		},
	}

	price := linePrice(line, 3, 1)

	assert.Equal(t, int64(1), price.TaxTotal, "floor(7x4/6) - floor(7x3/6)")
	var sum int64
	for _, c := range price.TaxComponents {
		assert.GreaterOrEqual(t, c.TaxAmount, int64(0), "%s", c.RateID)
		sum += c.TaxAmount
	}
	assert.Equal(t, price.TaxTotal, sum, "the rates add up to the item's tax")
	assert.Equal(t, int64(1), price.TaxComponents[1].TaxAmount, "the item's own tax by the largest remainder")
}

// TestAShareIsTakenIn128Bits does not overflow at the largest total a line can
// carry, and shares nothing outside a part of its whole.
func TestAShareIsTakenIn128Bits(t *testing.T) {
	assert.Equal(t, models.MaxTotal/3*2, shareOf(models.MaxTotal, 2, 3), "the largest total a line carries")
	assert.Equal(t, int64(math.MaxInt64)/7*6+(int64(math.MaxInt64)%7*6)/7, shareOf(math.MaxInt64, 6, 7),
		"a product past int64 is still divided exactly")
	assert.Zero(t, shareOf(100, 4, 3), "more units than the whole")
	assert.Zero(t, shareOf(100, 1, 0), "no whole")
	assert.Zero(t, shareOf(-100, 1, 2), "a negative amount")
}

// TestApportionGivesTheRemainderToTheLargestFractions shares by the largest
// remainder, the earlier position winning a tie, and never past the weights.
func TestApportionGivesTheRemainderToTheLargestFractions(t *testing.T) {
	assert.Equal(t, []int64{1, 1, 1}, apportion(3, []int64{5, 5, 5}))
	assert.Equal(t, []int64{2, 1, 1}, apportion(4, []int64{5, 5, 5}), "the earlier wins a tie")
	assert.Equal(t, []int64{0, 3}, apportion(3, []int64{1, 9}))
	assert.Equal(t, []int64{1, 9}, apportion(15, []int64{1, 9}), "never past the weights")
	assert.Equal(t, []int64{0, 0}, apportion(5, []int64{0, 0}))
}
