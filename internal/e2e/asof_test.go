//go:build integration

package e2e

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	fulfillmentmodels "github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	ordermodels "github.com/bdrtr/gobit/internal/modules/order/models"
	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
	fulfillingwf "github.com/bdrtr/gobit/internal/workflows/fulfilling"
)

// TestAnOrderReadNowIsTheLiveOrder is the cross-check that holds a reading at a
// moment to the records (ADR 0171): read at the present, the derived order has
// to agree with the order, the payment collection and the parcel as they are
// recorded — status, money, canceled units and the parcel's status. A
// derivation that disagreed with today would disagree with any past, and only
// this lane has all three modules' rows.
//
// It then reads the same order between its two refunds, where the money has to
// be what had moved by then and nothing after.
func TestAnOrderReadNowIsTheLiveOrder(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E As Of Product", map[string]int64{
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

	first, err := paymentSvc.RefundCollection(ctx, placed.PaymentCollectionID, 1_000, "first part", "")
	require.NoError(t, err)
	second, err := paymentSvc.RefundCollection(ctx, placed.PaymentCollectionID, 500, "second part", "")
	require.NoError(t, err)

	detail, err := orderSvc.GetOrder(ctx, placed.OrderID)
	require.NoError(t, err)
	_, err = orderSvc.CancelOrderLine(ctx, placed.OrderID, ordersvc.CancelOrderLineInput{
		OrderLineItemID: detail.Items[0].ID, Quantity: 1, Reason: "out of stock",
	})
	require.NoError(t, err)
	_, err = orderSvc.CreateCreditLine(ctx, placed.OrderID, ordersvc.CreateCreditLineInput{
		Amount: 300, Reason: "goodwill",
	})
	require.NoError(t, err)

	profileID := newShippingProfile(ctx, t, "E2E As Of Profile")
	optionID := newShippingOption(ctx, t, profileID, "E2E As Of Shipping", shippingOptionFee, false)
	flow, err := fulfillingwf.FromContainer(ctr)
	require.NoError(t, err)
	opened, err := flow.OpenForOrder(ctx, placed.OrderID, optionID, "e2e-asof-"+placed.OrderID)
	require.NoError(t, err)
	shipped, err := shippingSvc.MarkShipped(ctx, opened.FulfillmentID, "ASOF-"+opened.FulfillmentID, "")
	require.NoError(t, err)

	// Now, against the live records.
	live, err := orderSvc.GetOrder(ctx, placed.OrderID)
	require.NoError(t, err)
	collection, err := paymentSvc.GetPaymentCollection(ctx, placed.PaymentCollectionID)
	require.NoError(t, err)

	now, err := orderSvc.OrderAsOf(ctx, placed.OrderID, time.Now())
	require.NoError(t, err)

	require.NotNil(t, now.Status)
	assert.Equal(t, live.Status, *now.Status)
	assert.Equal(t, collection.CapturedAmount, now.Money.Captured,
		"the captures summed from the movements are the collection's captured total")
	assert.Equal(t, collection.RefundedAmount, now.Money.Refunded,
		"the refunds summed from the movements are the collection's refunded total")
	assert.Equal(t, int64(300), now.Money.Credited)
	assert.Equal(t, live.Summary.Outstanding(live.Total, now.Money.Credited), now.Money.Outstanding,
		"what was owed now is what the live order says is owed")
	require.Len(t, now.Lines, 1)
	assert.Equal(t, int64(1), now.Lines[0].Canceled)
	require.Len(t, now.Shipments, 1)
	assert.Equal(t, opened.FulfillmentID, now.Shipments[0].ID)
	assert.Equal(t, string(fulfillmentmodels.StatusShipped), now.Shipments[0].Status)
	assert.Equal(t, string(shipped.Status), now.Shipments[0].Status)
	assert.Equal(t, ordermodels.ContactHeld, now.Contact)

	// Between the two refunds: the first had moved and the second had not.
	between := second[0].CreatedAt.Add(-time.Microsecond)
	require.True(t, between.After(first[0].CreatedAt) || between.Equal(first[0].CreatedAt),
		"the two refunds were stamped at distinct microseconds")

	then, err := orderSvc.OrderAsOf(ctx, placed.OrderID, between)
	require.NoError(t, err)

	assert.Equal(t, collection.CapturedAmount, then.Money.Captured)
	assert.Equal(t, int64(1_000), then.Money.Refunded, "only the first refund had moved")
	assert.Zero(t, then.Money.Credited, "the credit came later")
	assert.Zero(t, then.Lines[0].Canceled, "so did the cancellation")
	assert.Empty(t, then.Shipments, "and the parcel")
}
