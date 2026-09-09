package invoicing_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAStackedOrderLinePrintsEveryRate is the hop that used to lose the
// breakdown IN SILENCE.
//
// This decode ignores unknown fields on purpose — the flow must be able to read
// a wider order than it needs — so before the field existed the order's
// components were dropped here without a word and the document printed the
// stack's base rate as though it were the whole story. Nothing would have
// reported it: the totals all still add up.
func TestAStackedOrderLinePrintsEveryRate(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	// 5% + 8% compound on 2000: 100 and 168, adding to the 268 the row carries.
	h.orders.order.TaxTotal = 268
	h.orders.order.Total = 2000 + 268 + 2500
	h.orders.order.Items[0].TaxRateBps = 500
	h.orders.order.Items[0].TaxTotal = 268
	h.orders.order.Items[0].Total = 2268
	h.orders.order.Items[0].TaxComponents = []fakeItemTax{
		{RateID: "txr_base", RateBps: 500, TaxableAmount: 2000, TaxAmount: 100},
		{RateID: "txr_top", RateBps: 800, Compound: true, TaxableAmount: 2100, TaxAmount: 168},
	}

	_, err := h.flow.IssueForOrder(context.Background(), validIssue())
	require.NoError(t, err)

	document := h.invoices.lastDocument(t)
	require.NotEmpty(t, document.Lines)

	require.Len(t, document.Lines[0].TaxComponents, 2,
		"both rates the order recorded must reach the document")
	assert.Equal(t, int32(500), document.Lines[0].TaxRateBps,
		"the row still carries the stack's base")

	assert.Equal(t, lineTax{
		RateID: "txr_base", RateBps: 500, Compound: false,
		TaxableAmount: 2000, TaxAmount: 100,
	}, document.Lines[0].TaxComponents[0])
	assert.Equal(t, lineTax{
		RateID: "txr_top", RateBps: 800, Compound: true,
		TaxableAmount: 2100, TaxAmount: 168,
	}, document.Lines[0].TaxComponents[1])

	assert.Equal(t, document.Lines[0].TaxTotal,
		document.Lines[0].TaxComponents[0].TaxAmount+
			document.Lines[0].TaxComponents[1].TaxAmount)
}

// TestASingleRateOrderLinePrintsNoBreakdown keeps the body the size it was for
// the row every shop mostly sells.
func TestASingleRateOrderLinePrintsNoBreakdown(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	_, err := h.flow.IssueForOrder(context.Background(), validIssue())
	require.NoError(t, err)

	document := h.invoices.lastDocument(t)
	require.NotEmpty(t, document.Lines)
	assert.Empty(t, document.Lines[0].TaxComponents)
}

// TestTheCarriageRowCarriesNoBreakdown states the obvious so a later change has
// to face it: carriage is not taxed today, so it has no rate and nothing to
// break down.
func TestTheCarriageRowCarriesNoBreakdown(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.orders.order.Items[0].TaxComponents = []fakeItemTax{
		{RateID: "txr_base", RateBps: 1000, TaxableAmount: 2000, TaxAmount: 200},
		{RateID: "txr_top", RateBps: 1000, TaxableAmount: 2000, TaxAmount: 200},
	}

	_, err := h.flow.IssueForOrder(context.Background(), validIssue())
	require.NoError(t, err)

	document := h.invoices.lastDocument(t)
	require.Len(t, document.Lines, 2, "the carriage becomes a row of its own")
	assert.Empty(t, document.Lines[1].TaxComponents)
	assert.Zero(t, document.Lines[1].TaxRateBps)
}
