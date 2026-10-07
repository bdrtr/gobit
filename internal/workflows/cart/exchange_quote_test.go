package cart

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

const testExchangeChannel = "sc_store"

// TestAnExchangeQuotePricesAsTheOrdersCartWould prices each variant at its
// quantity in the order's region, channel and customer, and taxes it through
// the tax module as a cart line is (ADR 0432).
func TestAnExchangeQuotePricesAsTheOrdersCartWould(t *testing.T) {
	t.Parallel()

	h := newModuleHarness(t)
	h.customers.groups = map[string][]string{testCustomerID: {"cgrp_vip"}}

	quote, err := h.wf.QuoteExchangeLines(context.Background(), ExchangeQuoteInput{
		RegionID: testRegionID, SalesChannelID: testExchangeChannel, CustomerID: testCustomerID,
		Lines: []ExchangeQuoteLine{{VariantID: testVariantA, Quantity: 3}, {VariantID: testVariantB, Quantity: 1}},
	})

	require.NoError(t, err)
	require.Len(t, h.prices.requests, 1, "one bulk request")
	req := h.prices.requests[0]
	assert.Equal(t, map[string]string{
		attrRegionID: testRegionID, AttrSalesChannelID: testExchangeChannel,
		AttrCustomerID: testCustomerID, AttrCustomerGroupID: "cgrp_vip",
	}, req.Attributes, "the price context of a cart the order's customer opened in its channel")
	require.Len(t, req.Items, 2)
	assert.Equal(t, int32(3), req.Items[0].Quantity, "a line is priced at its own quantity")

	require.Len(t, h.taxes.requests, 1, "the tax module taxes the lines, as it does a cart's")
	assert.Equal(t, "TR", h.taxes.requests[0].CountryCode)
	assert.Equal(t, testCurrency, quote.CurrencyCode)
	assert.Equal(t, TaxSourceTax, quote.TaxSource)
	assert.False(t, quote.PricesIncludeTax)
	require.Len(t, quote.Lines, 2)
	assert.Equal(t, LineTotals{
		LineItemID: "line-0", UnitPrice: 1000, Subtotal: 3000, TaxTotal: 600, TaxRateBps: 2000, Total: 3600,
		PriceID: "price_of_" + testPriceSetA,
	}, withoutListOf(quote.Lines[0]))
	assert.Equal(t, int64(250+50), quote.Lines[1].Total)
}

// withoutListOf leaves out the list pair, which the pricing fake names.
func withoutListOf(line LineTotals) LineTotals {
	line.PriceListID, line.PriceListType = nil, ""
	return line
}

// TestAnOperatorsPriceIsTaxedAsTheVariantWouldBe keeps the price list out of a
// line the operator priced and the tax in it.
func TestAnOperatorsPriceIsTaxedAsTheVariantWouldBe(t *testing.T) {
	t.Parallel()

	h := newModuleHarness(t)
	price := int64(850)

	quote, err := h.wf.QuoteExchangeLines(context.Background(), ExchangeQuoteInput{
		RegionID: testRegionID, CustomerID: testCustomerID,
		Lines: []ExchangeQuoteLine{
			{VariantID: testVariantA, Quantity: 2, UnitPrice: &price},
			{VariantID: testVariantB, Quantity: 1},
		},
	})

	require.NoError(t, err)
	require.Len(t, h.prices.requests, 1)
	require.Len(t, h.prices.requests[0].Items, 1, "only the line the operator did not price is priced")
	assert.Equal(t, testPriceSetB, h.prices.requests[0].Items[0].PriceSetID)
	require.Len(t, quote.Lines, 2)
	assert.Equal(t, int64(850), quote.Lines[0].UnitPrice)
	assert.Equal(t, int64(1700), quote.Lines[0].Subtotal)
	assert.Equal(t, int64(340), quote.Lines[0].TaxTotal, "an operator's price is still taxed")
	assert.Equal(t, int64(2040), quote.Lines[0].Total)
	assert.Equal(t, int64(250), quote.Lines[1].UnitPrice, "the list prices the other line")
}

// TestAnExchangeQuoteTaxesAGiftCardAsACartDoes reads the products' facts, so a
// gift card is sent to no tax module and carries none (ADR 0247).
func TestAnExchangeQuoteTaxesAGiftCardAsACartDoes(t *testing.T) {
	t.Parallel()

	h := newModuleHarness(t)
	installProductCatalog(h, map[string]productFacts{
		testProductA: {IsGiftcard: true},
		testProductB: {Discountable: true},
	})

	quote, err := h.wf.QuoteExchangeLines(context.Background(), ExchangeQuoteInput{
		RegionID: testRegionID,
		Lines:    []ExchangeQuoteLine{{VariantID: testVariantA, Quantity: 1}, {VariantID: testVariantB, Quantity: 2}},
	})

	require.NoError(t, err)
	require.Len(t, quote.Lines, 2)
	assert.Zero(t, quote.Lines[0].TaxTotal, "a gift card carries no tax")
	assert.Equal(t, int64(100), quote.Lines[1].TaxTotal)
	require.Len(t, h.taxes.requests, 1)
	assert.Len(t, h.taxes.requests[0].Items, 1, "the card is not sent to the tax module")
}

// TestAnExchangeQuoteWithoutTheTaxModuleTakesTheRegionsRate falls back as the
// totals do.
func TestAnExchangeQuoteWithoutTheTaxModuleTakesTheRegionsRate(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.regions.rateBps = 1800

	quote, err := h.wf.QuoteExchangeLines(context.Background(), ExchangeQuoteInput{
		RegionID: testRegionID, Lines: []ExchangeQuoteLine{{VariantID: testVariantA, Quantity: 1}},
	})

	require.NoError(t, err)
	assert.Equal(t, TaxSourceRegion, quote.TaxSource)
	assert.Equal(t, int64(180), quote.Lines[0].TaxTotal)
	assert.Equal(t, int32(1800), quote.Lines[0].TaxRateBps)
}

// TestAnExchangeQuoteIsRefusedWhatACartWouldRefuse checks the request before
// anything is asked.
func TestAnExchangeQuoteIsRefusedWhatACartWouldRefuse(t *testing.T) {
	t.Parallel()

	negative := int64(-1)
	for name, in := range map[string]ExchangeQuoteInput{
		"no region":         {Lines: []ExchangeQuoteLine{{VariantID: testVariantA, Quantity: 1}}},
		"no line":           {RegionID: testRegionID},
		"no variant":        {RegionID: testRegionID, Lines: []ExchangeQuoteLine{{Quantity: 1}}},
		"no unit":           {RegionID: testRegionID, Lines: []ExchangeQuoteLine{{VariantID: testVariantA}}},
		"a negative price":  {RegionID: testRegionID, Lines: []ExchangeQuoteLine{{VariantID: testVariantA, Quantity: 1, UnitPrice: &negative}}},
		"too many quantity": {RegionID: testRegionID, Lines: []ExchangeQuoteLine{{VariantID: testVariantA, Quantity: MaxQuantity + 1}}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)

			_, err := h.wf.QuoteExchangeLines(context.Background(), in)

			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "%v", err)
			assert.Empty(t, h.prices.requests)
		})
	}

	h := newHarness(t)
	h.regions.currencyErr = errors.NotFound("region_not_found", "no such region")
	_, err := h.wf.QuoteExchangeLines(context.Background(), ExchangeQuoteInput{
		RegionID: "reg_gone", Lines: []ExchangeQuoteLine{{VariantID: testVariantA, Quantity: 1}},
	})
	require.Error(t, err)
	assert.Equal(t, CodeQuoteRegionUnknown, errors.CodeOf(err))
}

// TestTheExchangeQuoteCrossesAsJSON reads the request strictly and answers the
// quote's schema.
func TestTheExchangeQuoteCrossesAsJSON(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	surface := NewInterop(h.wf)

	raw, err := surface.QuoteExchangeLinesJSON(context.Background(), json.RawMessage(
		`{"region_id":"`+testRegionID+`","lines":[{"variant_id":"`+testVariantA+`","quantity":2}]}`))
	require.NoError(t, err)
	var quote struct {
		CurrencyCode string `json:"currency_code"`
		Lines        []struct {
			UnitPrice int64 `json:"unit_price"`
			Total     int64 `json:"total"`
		} `json:"lines"`
	}
	require.NoError(t, json.Unmarshal(raw, &quote))
	assert.Equal(t, testCurrency, quote.CurrencyCode)
	require.Len(t, quote.Lines, 1)
	assert.Equal(t, int64(1000), quote.Lines[0].UnitPrice)
	assert.Equal(t, int64(2400), quote.Lines[0].Total)

	_, err = surface.QuoteExchangeLinesJSON(context.Background(), json.RawMessage(
		`{"region_id":"`+testRegionID+`","currency_code":"EUR","lines":[{"variant_id":"`+testVariantA+`","quantity":2}]}`))
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a field the schema does not know refuses the request")
	assert.Equal(t, CodeInvalidInput, errors.CodeOf(err))
}
