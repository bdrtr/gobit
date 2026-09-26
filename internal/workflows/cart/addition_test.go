package cart

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// stubOrders records the one question this package asks the order module.
type stubOrders struct {
	calls  int
	asked  []string
	answer error
}

// CheckAddition records the question and gives the scripted answer.
func (s *stubOrders) CheckAddition(_ context.Context, orderID, customerID, currencyCode string) error {
	s.calls++
	s.asked = []string{orderID, customerID, currencyCode}

	return s.answer
}

// TestACartOpenedToAddAsksTheOrderModuleFirst is the order of events: the
// question carries the cart's customer and the currency derived from the
// country, and only a yes opens the cart, with the order written on it
// (ADR 0192).
func TestACartOpenedToAddAsksTheOrderModuleFirst(t *testing.T) {
	h := newHarness(t)
	orders := &stubOrders{}
	h.wf.orders = orders
	seen := recordOpenCart(h.carts, testCartID)

	out, err := h.wf.CreateCart(context.Background(), CreateCartInput{
		CountryCode: "TR", CustomerID: testCustomerID, AddsToOrderID: " order_PARENT ",
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"order_PARENT", testCustomerID, testCurrency}, orders.asked)
	assert.Equal(t, "order_PARENT", h.carts.openedAddsTo)
	assert.Equal(t, "order_PARENT", out.AddsToOrderID)
	assert.NotEmpty(t, *seen)
}

// TestARefusedAdditionOpensNoCart passes the order module's refusal through as
// it is, and writes nothing.
func TestARefusedAdditionOpensNoCart(t *testing.T) {
	h := newHarness(t)
	h.wf.orders = &stubOrders{answer: errors.Conflict("order_addition_parent_not_pending", "canceled")}
	seen := recordOpenCart(h.carts, testCartID)

	_, err := h.wf.CreateCart(context.Background(), CreateCartInput{
		CountryCode: "TR", CustomerID: testCustomerID, AddsToOrderID: "order_PARENT",
	})

	require.Error(t, err)
	assert.Equal(t, "order_addition_parent_not_pending", errors.CodeOf(err))
	assert.Empty(t, *seen, "no cart is opened for an order that may not be added to")
}

// TestACartThatAddsToNothingAsksNothing keeps every other cart off the order
// module, so an installation without one opens carts as before.
func TestACartThatAddsToNothingAsksNothing(t *testing.T) {
	h := newHarness(t)
	orders := &stubOrders{}
	h.wf.orders = orders
	recordOpenCart(h.carts, testCartID)

	_, err := h.wf.CreateCart(context.Background(), CreateCartInput{
		CountryCode: "TR", CustomerID: testCustomerID,
	})
	require.NoError(t, err)

	assert.Zero(t, orders.calls)
	assert.Empty(t, h.carts.openedAddsTo)
}

// TestWithoutTheOrderModuleNoCartAdds fails closed: nothing else can say the
// order may be added to.
func TestWithoutTheOrderModuleNoCartAdds(t *testing.T) {
	h := newHarness(t)
	seen := recordOpenCart(h.carts, testCartID)

	_, err := h.wf.CreateCart(context.Background(), CreateCartInput{
		CountryCode: "TR", CustomerID: testCustomerID, AddsToOrderID: "order_PARENT",
	})

	require.Error(t, err)
	assert.Equal(t, CodeAdditionsUnavailable, errors.CodeOf(err))
	assert.Empty(t, *seen)
}
