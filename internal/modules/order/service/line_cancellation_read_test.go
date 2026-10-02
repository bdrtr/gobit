package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestAWriteOffSentTwiceWritesOffOnce is ADR 0341: a cancellation carrying
// how many of the line's units were spoken for when the line was read goes
// through while that holds, and the same one sent again, the count having
// moved, is refused by what the line has now; one carrying no count is
// judged by the ceiling alone, as before.
func TestAWriteOffSentTwiceWritesOffOnce(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, lineID := returnedOrder(t, e)
	none := int64(0)

	in := service.CancelOrderLineInput{OrderLineItemID: lineID, Quantity: 1, Reason: "out of stock", ReadSpokenFor: &none}
	_, err := e.svc.CancelOrderLine(ctx, order.ID, in)
	require.NoError(t, err)
	_, err = e.svc.CancelOrderLine(ctx, order.ID, in)
	require.Error(t, err)
	assert.Equal(t, service.CodeLineMoved, errors.CodeOf(err), "the same form sent twice: %v", err)
	assert.Contains(t, err.Error(), "has 1 units asked back or written off now, not 0")

	one := int64(1)
	in.ReadSpokenFor = &one
	_, err = e.svc.CancelOrderLine(ctx, order.ID, in)
	require.NoError(t, err, "read after the first, it goes through")
	in.ReadSpokenFor = nil
	_, err = e.svc.CancelOrderLine(ctx, order.ID, in)
	require.NoError(t, err, "no count read, the ceiling alone judges")
	_, err = e.svc.CancelOrderLine(ctx, order.ID, in)
	assert.Equal(t, service.CodeCancelQuantityExceeded, errors.CodeOf(err), "and refuses past the line: %v", err)
}
