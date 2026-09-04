package cart

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// recordOpenCart scripts the fake cart service so that it records the arguments
// of the cart that gets opened.
func recordOpenCart(carts *stubCarts, cartID string) *[]string {
	seen := &[]string{}
	carts.openCartFn = func(
		_ context.Context, regionID, currencyCode, customerID, email string, _ json.RawMessage,
	) (string, error) {
		*seen = []string{regionID, currencyCode, customerID, email}
		return cartID, nil
	}
	return seen
}

// TestCreateCartGuestSkipsCustomerModule verifies that a guest cart is opened
// WITHOUT touching the customer module at all.
func TestCreateCartGuestSkipsCustomerModule(t *testing.T) {
	h := newHarness(t)
	seen := recordOpenCart(h.carts, testCartID)

	out, err := h.wf.CreateCart(context.Background(), CreateCartInput{CountryCode: "TR"})
	require.NoError(t, err)

	assert.Equal(t, CreateCartResult{
		CartID:       testCartID,
		RegionID:     testRegionID,
		CurrencyCode: testCurrency,
		Guest:        true,
	}, out)
	assert.Equal(t, []string{testRegionID, testCurrency, "", ""}, *seen)
	assert.Zero(t, h.customers.calls, "the guest flow must not depend on the customer service")
}

// TestCreateCartGuestCarriesGivenEmail verifies that a guest cart is opened with
// the e-mail that was given.
func TestCreateCartGuestCarriesGivenEmail(t *testing.T) {
	h := newHarness(t)
	seen := recordOpenCart(h.carts, testCartID)

	out, err := h.wf.CreateCart(context.Background(), CreateCartInput{
		CountryCode: "TR",
		Email:       " misafir@example.com ",
	})
	require.NoError(t, err)

	assert.Equal(t, "misafir@example.com", out.Email)
	assert.Equal(t, "misafir@example.com", (*seen)[3])
	assert.True(t, out.Guest)
}

// TestCreateCartRegisteredCustomerUsesStoredEmail verifies that the cart of a
// registered customer is linked to that customer and that the e-mail comes from
// the customer record.
func TestCreateCartRegisteredCustomerUsesStoredEmail(t *testing.T) {
	h := newHarness(t)
	seen := recordOpenCart(h.carts, testCartID)

	out, err := h.wf.CreateCart(context.Background(), CreateCartInput{
		CountryCode: "TR",
		CustomerID:  testCustomerID,
	})
	require.NoError(t, err)

	assert.False(t, out.Guest)
	assert.Equal(t, testCustomerID, out.CustomerID)
	assert.Equal(t, "registered@example.com", out.Email)
	assert.Equal(t, []string{testRegionID, testCurrency, testCustomerID, "registered@example.com"}, *seen)
}

// TestCreateCartKeepsGivenEmailOverCustomerRecord verifies that the caller's
// e-mail is preserved.
//
// The cart's address is the address that this order will be shipped to;
// OVERWRITING it with the current address in the customer ledger would mean
// silently throwing away the address the customer deliberately entered for this
// order.
func TestCreateCartKeepsGivenEmailOverCustomerRecord(t *testing.T) {
	h := newHarness(t)
	recordOpenCart(h.carts, testCartID)

	out, err := h.wf.CreateCart(context.Background(), CreateCartInput{
		CountryCode: "TR",
		CustomerID:  testCustomerID,
		Email:       "this-order@example.com",
	})
	require.NoError(t, err)

	assert.Equal(t, "this-order@example.com", out.Email)
}

// TestCreateCartRejectsUnknownCustomer verifies that no cart is opened for a
// customer that does not exist.
func TestCreateCartRejectsUnknownCustomer(t *testing.T) {
	h := newHarness(t)
	opened := false
	h.carts.openCartFn = func(_ context.Context, _, _, _, _ string, _ json.RawMessage) (string, error) {
		opened = true
		return testCartID, nil
	}

	_, err := h.wf.CreateCart(context.Background(), CreateCartInput{
		CountryCode: "TR",
		CustomerID:  "cust_missing",
	})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))
	assert.False(t, opened, "if validation fails the cart must never be opened")
}

// TestCreateCartRejectsUnknownCountry verifies that no cart is opened in a
// country that has no region.
func TestCreateCartRejectsUnknownCountry(t *testing.T) {
	h := newHarness(t)

	_, err := h.wf.CreateCart(context.Background(), CreateCartInput{CountryCode: "ZZ"})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))
}

// TestCreateCartRejectsInvalidInput verifies that malformed input reaches no
// module at all.
func TestCreateCartRejectsInvalidInput(t *testing.T) {
	tests := map[string]CreateCartInput{
		"country empty":                {CountryCode: "   "},
		"customer contains whitespace": {CountryCode: "TR", CustomerID: " cust_1"},
	}

	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)

			_, err := h.wf.CreateCart(context.Background(), in)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err))
			assert.Equal(t, CodeInvalidInput, errors.CodeOf(err))
			assert.Zero(t, h.customers.calls)
		})
	}
}

// TestCreateCartCarriesMetadataUnchanged verifies that the free-form data
// attached to the cart passes through the workflow UNMODIFIED.
//
// The field is in a class of its own, apart from the region and the currency: it
// really is the caller's own data and there is nothing to derive it from. The
// workflow reading or re-encoding it would be the only place that could silently
// change the body the storefront sent; the assertion is precisely that this does
// not happen. The same decision was taken for line item metadata as well.
func TestCreateCartCarriesMetadataUnchanged(t *testing.T) {
	h := newHarness(t)

	want := json.RawMessage(`{"source":"storefront"}`)
	var got json.RawMessage
	h.carts.openCartFn = func(
		_ context.Context, _, _, _, _ string, metadata json.RawMessage,
	) (string, error) {
		got = metadata
		return testCartID, nil
	}

	_, err := h.wf.CreateCart(context.Background(), CreateCartInput{
		CountryCode: "TR",
		Metadata:    want,
	})
	require.NoError(t, err)

	assert.JSONEq(t, string(want), string(got),
		"metadata must reach the cart as it was given")
}

// TestOpenCartForCountryDerivesRegion verifies that the cross-module surface
// resolves the region FROM THE COUNTRY and hands out nothing but the identifier.
//
// The surface HAS NO region parameter, and that is the core justification for
// wiring the cart-opening endpoint through the workflow: if there were such a
// parameter, nothing would stop the caller (that is, the storefront endpoint)
// from filling it with a value that came from the client.
func TestOpenCartForCountryDerivesRegion(t *testing.T) {
	h := newHarness(t)
	seen := recordOpenCart(h.carts, testCartID)

	cartID, err := NewInterop(h.wf).OpenCartForCountry(
		context.Background(), "TR", "", "misafir@example.com", nil)
	require.NoError(t, err)

	assert.Equal(t, testCartID, cartID)
	assert.Equal(t, []string{testRegionID, testCurrency, "", "misafir@example.com"}, *seen,
		"the region and the currency must be derived FROM THE COUNTRY")
}

// TestOpenCartForCountryOpensNoCartForUnknownCountry verifies that the surface
// does not swallow the error.
//
// The assertion is not merely that the identifier comes back empty: the cart must
// NOT be opened at all. Opening a cart "somehow" in a country that has no region
// would come out at the same door as falling back to a default region.
func TestOpenCartForCountryOpensNoCartForUnknownCountry(t *testing.T) {
	h := newHarness(t)
	opened := false
	h.carts.openCartFn = func(_ context.Context, _, _, _, _ string, _ json.RawMessage) (string, error) {
		opened = true
		return testCartID, nil
	}

	cartID, err := NewInterop(h.wf).OpenCartForCountry(context.Background(), "ZZ", "", "", nil)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))
	assert.Empty(t, cartID)
	assert.False(t, opened, "no cart must be WRITTEN in a country that has no region")
}
