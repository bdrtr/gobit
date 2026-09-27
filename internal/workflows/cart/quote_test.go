package cart

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// TestAQuoteIsTheCartsOwnPriceForOne is ADR 0216: the region's currency, the
// rule context a cart of the customer carries, quantity one, and one bulk
// request for the variants that have a price set.
func TestAQuoteIsTheCartsOwnPriceForOne(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.customers.groups = map[string][]string{testCustomerID: {"cgrp_vip", "cgrp_retail"}}

	currency, prices, err := h.wf.QuoteUnitPrices(context.Background(), testRegionID, testCustomerID,
		[]string{testVariantA, testVariantB, "variant_unpriced"})

	require.NoError(t, err)
	assert.Equal(t, testCurrency, currency)
	assert.Equal(t, map[string]int64{testVariantA: 1000, testVariantB: 250}, prices,
		"a variant with no price set is absent")
	require.Len(t, h.prices.requests, 1, "one bulk request")
	req := h.prices.requests[0]
	assert.Equal(t, testCurrency, req.CurrencyCode)
	assert.Equal(t, map[string]string{
		attrRegionID: testRegionID, AttrCustomerID: testCustomerID, attrCustomerGroupID: "cgrp_vip",
	}, req.Attributes, "the cart's rule context, head group included")
	for _, item := range req.Items {
		assert.Equal(t, int32(1), item.Quantity)
	}
}

// TestAVariantWithNoPriceInTheCurrencyIsAbsent: pricing flags it unpriced and
// the others are answered.
func TestAVariantWithNoPriceInTheCurrencyIsAbsent(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	delete(h.prices.amounts, testPriceSetB)

	_, prices, err := h.wf.QuoteUnitPrices(context.Background(), testRegionID, testCustomerID,
		[]string{testVariantA, testVariantB})

	require.NoError(t, err)
	assert.Equal(t, map[string]int64{testVariantA: 1000}, prices)
}

// TestAQuoteNeedsARegionThatExists: the currency is the region's.
func TestAQuoteNeedsARegionThatExists(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	_, _, err := h.wf.QuoteUnitPrices(context.Background(), "", testCustomerID, []string{testVariantA})
	assert.True(t, errors.IsInvalid(err))

	h.regions.currencyErr = errors.NotFound("region_not_found", "no such region")
	_, _, err = h.wf.QuoteUnitPrices(context.Background(), "reg_gone", testCustomerID, []string{testVariantA})
	assert.True(t, errors.IsNotFound(err), "%v", err)
	assert.Equal(t, CodeQuoteRegionUnknown, errors.CodeOf(err))

	h.regions.currencyErr = errors.Unavailable("region_read_failed", "the database is away")
	_, _, err = h.wf.QuoteUnitPrices(context.Background(), "reg_1", testCustomerID, []string{testVariantA})
	require.Error(t, err)
	assert.NotEqual(t, CodeQuoteRegionUnknown, errors.CodeOf(err), "a region not read is not a region that is gone")
}

// TestAVariantBoundToTwoPriceSetsIsAbsent: the cart refuses such a line as
// ambiguous (CodeVariantPriceSetAmbiguous); asked about as one of many, it has
// no price to compare, and the others are answered.
func TestAVariantBoundToTwoPriceSetsIsAbsent(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.links.links[testVariantB] = []string{testPriceSetB, testPriceSetA}

	_, prices, err := h.wf.QuoteUnitPrices(context.Background(), testRegionID, testCustomerID,
		[]string{testVariantA, testVariantB})

	require.NoError(t, err)
	assert.Equal(t, map[string]int64{testVariantA: 1000}, prices)
	require.Len(t, h.prices.requests, 1)
	assert.Len(t, h.prices.requests[0].Items, 1, "only the variant with one price set is asked")
}
