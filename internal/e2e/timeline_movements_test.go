//go:build integration

package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// TestTheTimelineTellsEveryMovement walks ADR 0170 across the modules: two
// partial refunds, a canceled unit and a credit on one real order, and the
// timeline has to name each with what it moved.
//
// The refunds are the defect this closes (D126). The timeline used to read the
// payment module's first capture and last refund with the lifetime totals, so
// the two refunds below were one refund of 1,500 dated at the second. Only this
// lane reaches the payment module's rows through the order_payment link, which
// is where the movements cross.
func TestTheTimelineTellsEveryMovement(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Timeline Movements Product", map[string]int64{
		taxedCurrency: shippingUnitPrice,
	}, shippingStock)

	cartID, totals := prepareCart(ctx, t, customerID, variantID, 2)
	placed, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: paymentmanual.ID,
		PaymentData:       paymentBehavior(t, paymentmanual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     totals.Total,
	})
	require.NoError(t, err)

	first, err := paymentSvc.RefundCollection(ctx, placed.PaymentCollectionID, 1_000, "first part")
	require.NoError(t, err)
	second, err := paymentSvc.RefundCollection(ctx, placed.PaymentCollectionID, 500, "second part")
	require.NoError(t, err)
	require.Len(t, first, 1)
	require.Len(t, second, 1)

	detail, err := orderSvc.GetOrder(ctx, placed.OrderID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1)
	canceled, err := orderSvc.CancelOrderLine(ctx, placed.OrderID, ordersvc.CancelOrderLineInput{
		OrderLineItemID: detail.Items[0].ID, Quantity: 1, Reason: "out of stock",
	})
	require.NoError(t, err)
	credit, err := orderSvc.CreateCreditLine(ctx, placed.OrderID, ordersvc.CreateCreditLineInput{
		Amount: 300, Reason: "goodwill",
	})
	require.NoError(t, err)

	entries, err := orderSvc.Timeline(ctx, placed.OrderID)
	require.NoError(t, err)

	captures := entriesOfKind(entries, ordersvc.KindPaymentCaptured)
	require.Len(t, captures, 1)
	assert.Equal(t, totals.Total, captures[0].Amount)
	assert.Equal(t, ordersvc.ClockApplication, captures[0].Clock)

	refunds := entriesOfKind(entries, ordersvc.KindPaymentRefunded)
	require.Len(t, refunds, 2, "each refund is its own entry; one entry carrying their sum is D126")
	byRef := map[string]int64{}
	for i := range refunds {
		byRef[refunds[i].RefID] = refunds[i].Amount
		assert.Equal(t, ordersvc.ClockDatabase, refunds[i].Clock)
	}
	assert.Equal(t, map[string]int64{first[0].ID: 1_000, second[0].ID: 500}, byRef,
		"each refund carries what it moved, under its own record")

	cancellations := entriesOfKind(entries, ordersvc.KindOrderLineCanceled)
	require.Len(t, cancellations, 1)
	assert.Equal(t, canceled.ID, cancellations[0].RefID)
	assert.Equal(t, int64(1), cancellations[0].Quantity)
	assert.Equal(t, detail.Items[0].ID, cancellations[0].Detail)

	credits := entriesOfKind(entries, ordersvc.KindOrderCredited)
	require.Len(t, credits, 1)
	assert.Equal(t, credit.ID, credits[0].RefID)
	assert.Equal(t, int64(300), credits[0].Amount)

	// The customer sees the goods and none of the money.
	visible, err := orderSvc.StorefrontTimeline(ctx, placed.OrderID)
	require.NoError(t, err)
	assert.Len(t, entriesOfKind(visible, ordersvc.KindOrderLineCanceled), 1)
	for i := range visible {
		assert.Zero(t, visible[i].Amount, "no moment the customer sees carries a figure (%s)", visible[i].Kind)
		assert.NotEqual(t, ordersvc.KindOrderCredited, visible[i].Kind)
		assert.NotEqual(t, ordersvc.KindPaymentRefunded, visible[i].Kind)
	}
}

// entriesOfKind returns the timeline entries of one kind.
func entriesOfKind(entries []ordersvc.TimelineEntry, kind string) []ordersvc.TimelineEntry {
	var out []ordersvc.TimelineEntry
	for i := range entries {
		if entries[i].Kind == kind {
			out = append(out, entries[i])
		}
	}

	return out
}
