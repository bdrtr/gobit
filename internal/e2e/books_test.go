//go:build integration

package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ordermodels "github.com/bdrtr/gobit/internal/modules/order/models"
	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	paymentmodels "github.com/bdrtr/gobit/internal/modules/payment/models"
	paymentsvc "github.com/bdrtr/gobit/internal/modules/payment/service"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// TestTheBooksCloseForAReturnedOrder is ADR 0189's claim on the production
// wiring, across the four records that make it: the payment journal (0186),
// the refund's cause (0187), the order journal (0188) and the revenue given
// back read from that cause.
//
// An order is placed and paid, one unit comes back and part of the money is
// refunded through the return. On the two journals together the order owes
// nothing: its placement debits receivable and its capture credits it, the
// refund debits it and the return it names credits it. The sales it recorded
// less what the return gave back is what the shop kept.
func TestTheBooksCloseForAReturnedOrder(t *testing.T) {
	ctx := t.Context()
	from := time.Now().UTC().Add(-time.Second)

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Books Product", map[string]int64{
		taxedCurrency: happyUnitPrice,
	}, happyInitialStock)
	cartID, _ := prepareCart(ctx, t, customerID, variantID, happyQuantity)
	placed, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID: cartID, LocationID: stockLocationID, PaymentProviderID: paymentmanual.ID,
		PaymentData: paymentBehavior(t, paymentmanual.OutcomeAuthorize), Email: email,
		ExpectedTotal: happyTotal,
	})
	require.NoError(t, err)
	order, err := orderSvc.GetOrder(ctx, placed.OrderID)
	require.NoError(t, err)

	requested := storefrontRequest(t, http.MethodPost, "/store/v1/orders/"+placed.OrderID+"/returns",
		`{"reason":"books","lines":[{"order_line_item_id":"`+order.Items[0].ID+`","quantity":1}]}`)
	require.Equal(t, http.StatusCreated, requested.Code, requested.Body.String())
	var opened afterSalesRecordResponse
	require.NoError(t, json.Unmarshal(requested.Body.Bytes(), &opened))
	returnID := opened.Data.ID

	received, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+placed.OrderID+"/returns/"+returnID+"/receive",
		map[string]any{"location_id": stockLocationID})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, received.Code, received.Body.String())
	refunded, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+placed.OrderID+"/returns/"+returnID+"/refund",
		map[string]any{"amount": returnRefundAmount, "reason": "books"})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, refunded.Code, refunded.Body.String())

	window := time.Now().UTC().Add(time.Minute)
	payments, err := paymentSvc.Journal(ctx, paymentsvc.JournalQuery{From: from, To: window})
	require.NoError(t, err)
	orders, err := orderSvc.Journal(ctx, ordersvc.JournalQuery{From: from, To: window})
	require.NoError(t, err)

	var receivable, givenBack, sales int64
	var orderKinds []ordermodels.JournalKind
	for _, entry := range orders.Entries {
		if entry.OrderID != placed.OrderID {
			continue
		}
		orderKinds = append(orderKinds, entry.Kind)
		for _, line := range entry.Lines {
			switch line.Account {
			case ordermodels.AccountReceivable:
				receivable += line.Debit - line.Credit
			case ordermodels.AccountSalesReturns:
				givenBack += line.Debit
			case ordermodels.AccountSales:
				sales += line.Credit
			}
		}
	}
	var paymentKinds []paymentmodels.JournalKind
	for _, entry := range payments.Entries {
		if entry.CollectionID != placed.PaymentCollectionID {
			continue
		}
		paymentKinds = append(paymentKinds, entry.Kind)
		for _, line := range entry.Lines {
			if line.Account == paymentmodels.AccountReceivable {
				receivable += line.Debit - line.Credit
			}
		}
	}

	assert.Equal(t, []ordermodels.JournalKind{ordermodels.JournalOrderPlaced, ordermodels.JournalReturnRefunded},
		orderKinds)
	assert.Equal(t, []paymentmodels.JournalKind{paymentmodels.JournalCapture, paymentmodels.JournalRefund},
		paymentKinds)
	assert.Zero(t, receivable, "the order owes nothing on the two journals together")
	assert.Equal(t, returnRefundAmount, givenBack, "the return gave back exactly what its refund sent")
	assert.Equal(t, order.Subtotal, sales)
}
