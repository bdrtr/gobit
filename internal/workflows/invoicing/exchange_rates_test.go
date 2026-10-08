package invoicing_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A stacked row taken back in parts never asks a rate for more than it has
// left (ADR 0432): the units' tax is shared by what each rate charged, a rate
// that would be over-drawn gives its excess to the rates with room, and the
// part that takes the row's last units takes what each rate has left.

// newStackedHarness builds the flow over an order of two bottles at 99.95
// taxed 10% and then 5% compound: 19 990 + 1 999 + 1 099 = 23 088. Half of
// the tax, 1 549, splits 999.5 : 549.5 over the two rates, so each half takes
// the rounding unit to the first rate, and two halves would take 1 000 from a
// rate that charged 999.5 twice.
func newStackedHarness(t *testing.T, acts ...map[string]any) *amendHarness {
	t.Helper()

	h := newAmendHarness(t, acts...)
	h.orders.order = fakeOrder{
		OrderID: "order_1", CurrencyCode: "TRY", Subtotal: 19990, TaxTotal: 3098, ShippingTotal: 3000,
		Total: 26088,
		Items: []fakeItem{{
			LineID: "li_bottle", Title: "Bottle", Quantity: 2, UnitPrice: 9995, Subtotal: 19990,
			TaxRateBps: 1000, TaxTotal: 3098, Total: 23088, TaxComponents: []fakeItemTax{
				{RateID: "txr_ten", RateBps: 1000, TaxableAmount: 19990, TaxAmount: 1999},
				{RateID: "txr_five", RateBps: 500, Compound: true, TaxableAmount: 21989, TaxAmount: 1099},
			},
		}},
	}
	h.invoices.rows = []amendedRow{
		{LineID: "invl_bottle", Position: 1, Total: 23088, TaxTotal: 3098, TaxRateBps: 1000,
			LeftTotal: 23088, LeftTax: 3098, Components: []amendedComponent{
				{RateID: "txr_ten", RateBps: 1000, TaxableAmount: 19990, TaxAmount: 1999, LeftTax: 1999},
				{RateID: "txr_five", RateBps: 500, Compound: true, TaxableAmount: 21989, TaxAmount: 1099, LeftTax: 1099},
			}},
		{LineID: "invl_carriage", Position: 2, Total: 3000, LeftTotal: 3000},
	}

	return h
}

// oneBottleBack is the return of one bottle, and coatSent a coat sent for it.
var (
	oneBottleBack = []map[string]any{{"line_id": "li_bottle", "quantity": 1}}
	coatSent      = map[string]any{
		"variant_id": "var_coat", "title": "Coat", "quantity": 1, "unit_price": 20000,
		"total": 20200, "tax_total": 200, "tax_rate_bps": 100,
	}
)

// givenBackPerRate sums what the refunds sent gave back under each rate of
// the bottle's row.
func (h *amendHarness) givenBackPerRate(t *testing.T) [2]int64 {
	t.Helper()

	var sums [2]int64
	for i := range h.invoices.sent {
		doc := &h.invoices.sent[i]
		if doc.Kind != "refund" || doc.canceled {
			continue
		}
		for _, line := range doc.Lines {
			if line.AmendsLineID != "invl_bottle" {
				continue
			}
			require.Len(t, line.TaxComponents, 2)
			sums[0] += line.TaxComponents[0].TaxAmount
			sums[1] += line.TaxComponents[1].TaxAmount
		}
	}

	return sums
}

// TestTwoExchangesOfAStackedRowAreBothDocumented: one bottle exchanged and
// then the other, in either order, both documented, the rates given back
// exactly what they charged.
func TestTwoExchangesOfAStackedRowAreBothDocumented(t *testing.T) {
	t.Parallel()

	for name, order := range map[string][2]string{"A then B": {"exch_a", "exch_b"}, "B then A": {"exch_b", "exch_a"}} {
		h := newStackedHarness(t, exchangeAct("exch_a", true, oneBottleBack, coatSent),
			exchangeAct("exch_b", true, oneBottleBack, coatSent))
		_, err := h.documentExchange(order[0])
		require.NoError(t, err, name)
		_, err = h.documentExchange(order[1])
		require.NoError(t, err, "%s: the second half fits the row's total and tax", name)
		assert.Equal(t, [2]int64{1999, 1099}, h.givenBackPerRate(t), "%s: each rate gave back what it charged", name)
		first, second := h.invoices.sent[0].Lines[0], h.invoices.sent[2].Lines[0]
		assert.Equal(t, int64(1000), first.TaxComponents[0].TaxAmount, "%s: the tie goes to the first rate", name)
		assert.Equal(t, int64(999), second.TaxComponents[0].TaxAmount, "%s: the last units take what is left", name)
		assert.Equal(t, [2]int64{11544, 1549}, [2]int64{second.Total, second.TaxTotal}, name)
	}
}

// TestAReturnThenAnExchangeOfAStackedRowAreBothDocumented: a return's refund
// of one bottle documented first, by its amount, and the exchange of the
// other after it.
func TestAReturnThenAnExchangeOfAStackedRowAreBothDocumented(t *testing.T) {
	t.Parallel()

	h := newStackedHarness(t, act("return_refunded", "ref_1", 11544, true, oneBottleBack[0]),
		exchangeAct("exch_b", true, oneBottleBack, coatSent))
	_, err := h.amend("return_refunded", "ref_1")
	require.NoError(t, err)
	_, err = h.documentExchange("exch_b")
	require.NoError(t, err, "the exchange's units fit what the row has left")
	assert.Equal(t, [2]int64{1999, 1099}, h.givenBackPerRate(t))
}

// TestARefundsRateBasesAreSharedByUnits: one bottle back prints each rate's
// base as the units' share, ⌊19 990 / 2⌋ and ⌊21 989 / 2⌋, which is what a
// bottle sent again is priced at; the share of the amount would print 10 995.
func TestARefundsRateBasesAreSharedByUnits(t *testing.T) {
	t.Parallel()

	h := newStackedHarness(t, exchangeAct("exch_a", true, oneBottleBack, coatSent))
	_, err := h.documentExchange("exch_a")
	require.NoError(t, err)
	line := h.invoices.sent[0].Lines[0]
	require.Len(t, line.TaxComponents, 2)
	assert.Equal(t, []int64{9995, 10994},
		[]int64{line.TaxComponents[0].TaxableAmount, line.TaxComponents[1].TaxableAmount})
}

// TestTheLastUnitsTakeWhatTheRowHasLeft: a bottle's return refunded by its
// amount gives back ⌊11 544 × 3 099 / 23 088⌋ = 1 549 of a tax of 3 099, so
// the row keeps 1 550; the exchange of the other bottle takes the row's last
// 11 544 and with it the 1 550 and each rate's rest, base included, where the
// units' share alone would leave a unit of tax on the row for ever.
func TestTheLastUnitsTakeWhatTheRowHasLeft(t *testing.T) {
	t.Parallel()

	h := newStackedHarness(t, act("return_refunded", "ref_1", 11544, true, oneBottleBack[0]),
		exchangeAct("exch_b", true, oneBottleBack, coatSent))
	h.orders.order.Items[0].TaxTotal, h.orders.order.Items[0].TaxComponents[1].TaxAmount = 3099, 1100
	h.invoices.rows[0].TaxTotal, h.invoices.rows[0].LeftTax = 3099, 3099
	h.invoices.rows[0].Components[1].TaxAmount, h.invoices.rows[0].Components[1].LeftTax = 1100, 1100
	_, err := h.amend("return_refunded", "ref_1")
	require.NoError(t, err)
	_, err = h.documentExchange("exch_b")
	require.NoError(t, err)

	last := h.invoices.sent[1].Lines[0]
	assert.Equal(t, [2]int64{11544, 1550}, [2]int64{last.Total, last.TaxTotal}, "the row's last tax")
	assert.Equal(t, [2]int64{1999, 1100}, h.givenBackPerRate(t), "each rate gave back all it charged")
	assert.Equal(t, []int64{9995, 10995},
		[]int64{last.TaxComponents[0].TaxableAmount, last.TaxComponents[1].TaxableAmount},
		"each base's rest: 19 990 - 9 995 and 21 989 - 10 994")
}
