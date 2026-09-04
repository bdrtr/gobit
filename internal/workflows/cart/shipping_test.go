package cart

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
)

// shippingHarness wires a harness whose cart holds one line and whose
// fulfillment surface quotes one option.
func shippingHarness(t *testing.T) *harness {
	t.Helper()

	h := newHarness(t)
	h.carts.snapshotFn = func(_ context.Context, _ string) (json.RawMessage, error) {
		return json.Marshal(Snapshot{
			ID:           testCartID,
			RegionID:     testRegionID,
			CurrencyCode: testCurrency,
			Items: []SnapshotItem{
				{ID: "cli_1", VariantID: testVariantA, Quantity: 2},
			},
		})
	}
	h.carts.addShippingFn = func(
		_ context.Context, _, _, _ string, _ int64, _ json.RawMessage,
	) (string, error) {
		return "csm_1", nil
	}
	h.shipping.options = []quotedOption{
		{ID: "so_1", Name: "Standard", Amount: 2500, CurrencyCode: testCurrency},
	}

	return h
}

// TestTheQuotedAmountIsWrittenNotTheCallersIs the reason this flow exists.
//
// Nothing a caller can send reaches the amount: the flow takes the option id
// and writes whatever the fulfillment module priced it at.
func TestTheQuotedAmountIsWrittenNotTheCallers(t *testing.T) {
	h := shippingHarness(t)

	var gotName, gotOption string
	var gotAmount int64
	h.carts.addShippingFn = func(
		_ context.Context, _, name, optionID string, amount int64, _ json.RawMessage,
	) (string, error) {
		gotName, gotOption, gotAmount = name, optionID, amount

		return "csm_1", nil
	}

	id, err := h.wf.AddQuotedShippingMethod(context.Background(), testCartID, "so_1", nil)
	require.NoError(t, err)

	assert.Equal(t, "csm_1", id)
	assert.Equal(t, int64(2500), gotAmount, "the amount has to be the QUOTED one")
	assert.Equal(t, "so_1", gotOption)
	assert.Equal(t, "Standard", gotName, "the name is the option's, not the caller's")
}

// TestTheQuoteIsAskedWithTheCARTsFacts proves the request is built from the
// record rather than from anything a caller supplied.
//
// A rule-bound option ("free shipping over 500") reads these numbers, so a
// caller able to influence them would be able to influence the price without
// ever naming it.
func TestTheQuoteIsAskedWithTheCARTsFacts(t *testing.T) {
	h := shippingHarness(t)

	_, err := h.wf.AddQuotedShippingMethod(context.Background(), testCartID, "so_1", nil)
	require.NoError(t, err)

	require.Equal(t, 1, h.shipping.calls)
	got := h.shipping.gotRequest
	assert.Equal(t, testRegionID, got.RegionID)
	assert.Equal(t, testCurrency, got.CurrencyCode)
	assert.Equal(t, "TR", got.CountryCode, "the country comes from the REGION, as the tax does")
	assert.Equal(t, int64(2), got.ItemCount)
	// Two units at the stub's 1000 each.
	assert.Equal(t, int64(2000), got.Subtotal)
}

// TestTheQuoteSubtotalIsTakenAFTERTheDiscount pins the basis of the threshold
// rules.
//
// "Free shipping over 500" reads this number. Answering it from the
// PRE-discount subtotal would grant free delivery on a basket the shopper is
// not paying that much for — the discount would be spent twice, once off the
// goods and once off the shipping.
func TestTheQuoteSubtotalIsTakenAFTERTheDiscount(t *testing.T) {
	h := newHarnessWith(t, &stubDiscounts{perLine: map[string]int64{"cli_1": 750}}, nil)
	h.carts.snapshotFn = func(_ context.Context, _ string) (json.RawMessage, error) {
		return json.Marshal(Snapshot{
			ID:           testCartID,
			RegionID:     testRegionID,
			CurrencyCode: testCurrency,
			Items:        []SnapshotItem{{ID: "cli_1", VariantID: testVariantA, Quantity: 2}},
		})
	}
	h.carts.addShippingFn = func(
		_ context.Context, _, _, _ string, _ int64, _ json.RawMessage,
	) (string, error) {
		return "csm_1", nil
	}
	h.shipping.options = []quotedOption{
		{ID: "so_1", Name: "Standard", Amount: 2500, CurrencyCode: testCurrency},
	}

	_, err := h.wf.AddQuotedShippingMethod(context.Background(), testCartID, "so_1", nil)
	require.NoError(t, err)

	// Two units at 1000, less a 750 discount on the line.
	assert.Equal(t, int64(1250), h.shipping.gotRequest.Subtotal,
		"the quote must be asked with the amount the shopper actually pays")
}

// TestAnOptionOutsideTheQuoteIsRefused holds the eligibility rule.
//
// The listing is not a catalog: it applies this cart's rules. "Not in the list"
// and "not allowed for this cart" are the same fact, so an option that is
// missing must not be fetched some other way.
func TestAnOptionOutsideTheQuoteIsRefused(t *testing.T) {
	h := shippingHarness(t)

	var written bool
	h.carts.addShippingFn = func(
		_ context.Context, _, _, _ string, _ int64, _ json.RawMessage,
	) (string, error) {
		written = true

		return "csm_1", nil
	}

	_, err := h.wf.AddQuotedShippingMethod(context.Background(), testCartID, "so_not_offered", nil)

	require.Error(t, err)
	assert.Equal(t, CodeShippingOptionUnknown, coreerrors.CodeOf(err))
	assert.False(t, written, "an option that was not quoted must not be written")
}

// TestAnAdminOnlyOrReturnOptionIsRefusedOnTheStorefrontPath keeps an option
// that reached the listing from being chosen by a shopper.
func TestAnAdminOnlyOrReturnOptionIsRefusedOnTheStorefrontPath(t *testing.T) {
	for _, option := range []quotedOption{
		{ID: "so_1", Name: "Ops only", Amount: 0, CurrencyCode: testCurrency, AdminOnly: true},
		{ID: "so_1", Name: "Return label", Amount: 0, CurrencyCode: testCurrency, IsReturn: true},
	} {
		h := shippingHarness(t)
		h.shipping.options = []quotedOption{option}

		_, err := h.wf.AddQuotedShippingMethod(context.Background(), testCartID, "so_1", nil)

		require.Error(t, err)
		assert.Equal(t, CodeShippingOptionUnknown, coreerrors.CodeOf(err))
	}
}

// TestAQuoteInAnotherCurrencyIsRefused stops a number that is sound as
// arithmetic and wrong as money.
func TestAQuoteInAnotherCurrencyIsRefused(t *testing.T) {
	h := shippingHarness(t)
	h.shipping.options = []quotedOption{
		{ID: "so_1", Name: "Standard", Amount: 2500, CurrencyCode: "EUR"},
	}

	_, err := h.wf.AddQuotedShippingMethod(context.Background(), testCartID, "so_1", nil)

	require.Error(t, err)
	assert.Equal(t, CodeShippingOptionUnknown, coreerrors.CodeOf(err))
}

// TestWithoutTheShippingSurfaceNothingIsWritten pins the fail-closed branch.
//
// This is the OPPOSITE of the tax surface's degradation, and the difference is
// that a missing tax surface has a correct fallback while a missing shipping
// surface has none: the only other source for the price is the caller.
func TestWithoutTheShippingSurfaceNothingIsWritten(t *testing.T) {
	h := shippingHarness(t)
	h.wf.shipping = nil

	var written bool
	h.carts.addShippingFn = func(
		_ context.Context, _, _, _ string, _ int64, _ json.RawMessage,
	) (string, error) {
		written = true

		return "csm_1", nil
	}

	_, err := h.wf.AddQuotedShippingMethod(context.Background(), testCartID, "so_1", nil)

	require.Error(t, err)
	assert.Equal(t, CodeShippingUnavailable, coreerrors.CodeOf(err))
	assert.False(t, written)
}

// TestAFailedQuoteIsNotSwallowed keeps an unreachable fulfillment module from
// becoming a free delivery.
func TestAFailedQuoteIsNotSwallowed(t *testing.T) {
	h := shippingHarness(t)
	h.shipping.err = errors.New("connection refused")

	var written bool
	h.carts.addShippingFn = func(
		_ context.Context, _, _, _ string, _ int64, _ json.RawMessage,
	) (string, error) {
		written = true

		return "csm_1", nil
	}

	_, err := h.wf.AddQuotedShippingMethod(context.Background(), testCartID, "so_1", nil)

	require.Error(t, err)
	assert.Equal(t, CodeShippingQuoteFailed, coreerrors.CodeOf(err))
	assert.False(t, written)
}

// TestAMethodWithoutAnOptionIsRefused closes the other way in.
//
// The cart service accepts a method with no option id, and such a method could
// only carry a price the caller chose. This flow is where that door is shut.
func TestAMethodWithoutAnOptionIsRefused(t *testing.T) {
	h := shippingHarness(t)

	_, err := h.wf.AddQuotedShippingMethod(context.Background(), testCartID, "", nil)

	require.Error(t, err)
	assert.Equal(t, CodeInvalidInput, coreerrors.CodeOf(err))
	assert.Equal(t, 0, h.shipping.calls)
}

// TestTheFreeFormDataIsCarriedUntouched keeps the caller's blob from being read
// or dropped.
func TestTheFreeFormDataIsCarriedUntouched(t *testing.T) {
	h := shippingHarness(t)

	var gotData json.RawMessage
	h.carts.addShippingFn = func(
		_ context.Context, _, _, _ string, _ int64, data json.RawMessage,
	) (string, error) {
		gotData = data

		return "csm_1", nil
	}

	blob := json.RawMessage(`{"note":"leave at the door"}`)
	_, err := h.wf.AddQuotedShippingMethod(context.Background(), testCartID, "so_1", blob)
	require.NoError(t, err)

	assert.JSONEq(t, string(blob), string(gotData))
}
