package checkout

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// TestSpendingLimitExceededDoesNotChargePayment pins down that money is NOT
// touched at all in a cart that trips the limit.
//
// This package does NOT ENFORCE the spending limit; the rule lives in the order
// module because the spend the limit is compared against is its data, and the
// check only closes against races inside the transaction that writes the order
// (see service/spending.go in the order module). What is pinned down here is the
// CONSEQUENCE of that rejection on the saga, and that is the real guarantee:
// because the create_order step comes BEFORE authorize_payment, a cart that trips
// the limit does not even OPEN a payment collection.
//
// The test depends on this order being preserved. If the order of the steps
// changes and payment is opened before the order, this goes red — and it should:
// charging the money of a purchase that is going to be rejected and refunding it
// afterwards is an irreversible mistake in a design where the refund is NOT this
// flow's compensation.
func TestSpendingLimitExceededDoesNotChargePayment(t *testing.T) {
	h := newHarness(t)
	h.orders.placeFn = func(context.Context, json.RawMessage) (string, error) {
		return "", errors.Conflict("order_spending_limit_exceeded",
			"spending limit exceeded: spend within period 5000, order 6100, limit 10000 (TRY)")
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())

	require.Error(t, err)
	// The CLASS is preserved (the engine wraps the step error with its own code
	// but carries the class over): the caller meets a cart whose payment was
	// declined and a cart that tripped the limit in the same branch — "the order
	// cannot be placed" — and both are 409s.
	assert.True(t, errors.IsConflict(err),
		"exceeding the limit is a conflict: the client can shrink the cart and retry")
	assert.Contains(t, err.Error(), "spending limit exceeded",
		"the module's reason MUST REACH the caller: otherwise the customer cannot learn why they were rejected")

	calls := h.rec.snapshot()

	// MONEY WAS NOT TOUCHED AT ALL.
	assert.Equal(t, 0, h.rec.count("payment:collection"))
	assert.Equal(t, 0, h.rec.count("payment:session"))
	assert.Equal(t, 0, h.rec.count("payment:authorize"))
	assert.Equal(t, 0, h.rec.count("payment:capture"))

	// Since no order was opened, there is no order to cancel either.
	assert.Empty(t, h.orders.canceled)

	// The reserved stock was RELEASED: the goods of a rejected cart must not come
	// off the shelf.
	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineA))
	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineB))
	assert.Equal(t, 0, h.rec.count("inventory:confirm:res_"+testLineA))

	// The cart stayed OPEN: the customer must be able to remove a line and retry.
	assert.Equal(t, 0, h.rec.count("cart:complete"))

	// The order attempt came BEFORE payment; this is the ordering the test rests
	// on.
	assert.Less(t, indexOf(calls, "order:place"), len(calls))
	assert.Equal(t, -1, indexOf(calls, "payment:authorize"))
}
