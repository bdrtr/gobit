//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// This file runs the write-off chain end to end, and it exists because its
// absence was the hiding place.
//
// # What only this file can show
//
// Four records meet here and no unit test can see more than one of them:
//
//   - ADR 0134 puts back the stock of units written off, as
//     `min(canceled, bought − in a live parcel)`;
//   - ADR 0135 refuses a parcel holding more than the order owes, computed from
//     the same middle term;
//   - ADR 0139 releases what a CANCELED parcel was holding, as the difference
//     between that window evaluated twice;
//   - ADR 0140 writes the binding that lets any of them find a parcel at all.
//
// Every one of those was green against fakes, and the middle term was zero in
// production for every parcel that had anything to contribute: the flow-opened
// parcel carries no items and the item-carrying parcel carried no binding. Three
// records describing a subtraction that never subtracted (D75, D76). The fakes
// could not show it, because a fake is written to the mechanism rather than to
// what the endpoints can actually produce.
//
// The cancellation flow is also the only one in this repository that is driven
// entirely by the BUS. Nothing resolves it, nothing calls it; if it is not
// subscribed it does nothing and no request fails. That is precisely why it was
// the one flow this ground did not wire, and why nothing noticed (ADR 0141).
//
// # Why the numbers are what they are
//
// Three units bought and TWO in the parcel, so every figure in the scenario is
// distinct. Had the parcel held all three, "what the write-off could reach" and
// "what the parcel released" would both be zero and three, and a flow that put
// everything back at the first step would look correct.

// The hand-computed figures of the write-off scenario, derived by hand rather
// than recomputed from the production formula — that would be making the same
// mistake twice.
const (
	// cancelUnitPrice is one unit, and the tax rate is the shared 20%.
	cancelUnitPrice int64 = 20_000
	// cancelQuantity is what the customer buys.
	cancelQuantity int64 = 3
	// cancelTotal is 60_000 + 12_000 tax.
	cancelTotal int64 = 72_000
	// cancelInitialStock is what the shelf holds before the sale.
	cancelInitialStock int64 = 10
	// cancelStockAfterSale is 10 − 3: the checkout CONFIRMS the reservation, so
	// the units are deducted rather than held.
	cancelStockAfterSale int64 = cancelInitialStock - cancelQuantity
	// cancelInParcel is how many of the three go into a parcel.
	cancelInParcel int64 = 2
	// cancelStockAfterWriteOff is 7 + 1: the write-off can only reach the one
	// unit that is NOT in the parcel, because the other two are in a box the
	// warehouse is picking.
	cancelStockAfterWriteOff int64 = cancelStockAfterSale + (cancelQuantity - cancelInParcel)
)

// TestAWriteOffAndThenACanceledParcelPutEveryUnitBack is the whole chain.
func TestAWriteOffAndThenACanceledParcelPutEveryUnitBack(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, inventoryItemID := newStockedVariant(ctx, t, "E2E Canceled Product",
		map[string]int64{taxedCurrency: cancelUnitPrice}, cancelInitialStock)

	cartID, _ := prepareCart(ctx, t, customerID, variantID, cancelQuantity)

	placed, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: paymentmanual.ID,
		PaymentData:       paymentBehavior(t, paymentmanual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     cancelTotal,
	})
	require.NoError(t, err, "the fixture order could not be placed")
	require.Equal(t, cancelStockAfterSale, stockLevel(ctx, t, inventoryItemID).StockedQuantity,
		"precondition: the checkout CONFIRMS the reservation, so the three units are "+
			"deducted from the shelf rather than held against it")

	order, err := orderSvc.GetOrder(ctx, placed.OrderID)
	require.NoError(t, err)
	require.Len(t, order.Items, 1, "precondition: the fixture order has a single line")
	lineID := order.Items[0].ID

	// --- 1) two of the three units go into a parcel ---
	//
	// Through the ADMIN endpoint, which is the only one that takes an item
	// breakdown. Until ADR 0140 a parcel opened this way was bound to no order,
	// so everything below answered zero about it.
	profileID := newShippingProfile(ctx, t, "E2E Cancel Profile")
	optionID := newShippingOption(ctx, t, profileID, "E2E Cancel Shipping", shippingOptionFee, false)

	opened, err := adminRequestWithBody(http.MethodPost, "/admin/v1/fulfillments", map[string]any{
		"reference":          placed.OrderID,
		"shipping_option_id": optionID,
		"idempotency_key":    "e2e-cancel-" + placed.OrderID,
		"items": []map[string]any{
			{"line_item_id": lineID, "quantity": cancelInParcel},
		},
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, opened.Code,
		"the parcel must open; body: %s", opened.Body.String())

	var parcel struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(opened.Body.Bytes(), &parcel),
		"the parcel could not be decoded; body: %s", opened.Body.String())
	require.NotEmpty(t, parcel.Data.ID)

	assert.Equal(t, cancelStockAfterSale, stockLevel(ctx, t, inventoryItemID).StockedQuantity,
		"opening a parcel moves no stock: the units left the shelf when the order was "+
			"placed, and putting them in a box is not a second deduction")

	// --- 2) the operator writes off ALL THREE units ---
	//
	// Two of them are in the parcel, so only one can come back. The other two are
	// goods the warehouse is picking, and putting them on the shelf would sell
	// them twice.
	canceled, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+placed.OrderID+"/line-cancellations", map[string]any{
			"order_line_item_id": lineID,
			"quantity":           cancelQuantity,
			"reason":             "the customer changed their mind",
		})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, canceled.Code,
		"the write-off must be recorded; body: %s", canceled.Body.String())

	// The flow is driven by the bus and the in-memory backend hands each handler
	// its own goroutine, so the effect is awaited rather than assumed.
	requireStockEventually(ctx, t, inventoryItemID, cancelStockAfterWriteOff,
		"the write-off must put back exactly the unit that is NOT in a parcel. "+
			"Putting back all three would mean the shelf is credited with goods that "+
			"are in a box and about to ship; putting back none would mean the "+
			"cancellation flow is not subscribed at all")

	// --- 3) the shop resolves the rest the way ADR 0135 told it to ---
	//
	// That record declined to withdraw somebody's shipment on the framework's own
	// authority and named this as the shop's move. Until ADR 0139 it flipped a
	// status and told nobody, and the two units it had been holding were left in
	// no parcel, owed to no customer and on no shelf.
	dropped, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/fulfillments/"+parcel.Data.ID+"/cancel", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, dropped.Code,
		"the parcel must be cancelable; body: %s", dropped.Body.String())

	requireStockEventually(ctx, t, inventoryItemID, cancelInitialStock,
		"every canceled unit has to be back once the parcel that held the rest is "+
			"gone. A shelf that stops at eight is the defect this chain was built to "+
			"close: two units that are neither sold, nor shipped, nor stock")
}

// TestCancelingAParcelWhoseLineWasNeverWrittenOffAddsNoStock separates the two
// meanings a canceled parcel has.
//
// Its units become DISPATCHABLE again — a new parcel may hold them — but they are
// still sold. Crediting them to the shelf would sell the same goods twice, and
// this runs the case through the real endpoints because the arithmetic that
// decides it is the same one the test above depends on.
func TestCancelingAParcelWhoseLineWasNeverWrittenOffAddsNoStock(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, inventoryItemID := newStockedVariant(ctx, t, "E2E Uncanceled Product",
		map[string]int64{taxedCurrency: cancelUnitPrice}, cancelInitialStock)

	cartID, _ := prepareCart(ctx, t, customerID, variantID, cancelQuantity)

	placed, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: paymentmanual.ID,
		PaymentData:       paymentBehavior(t, paymentmanual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     cancelTotal,
	})
	require.NoError(t, err)

	order, err := orderSvc.GetOrder(ctx, placed.OrderID)
	require.NoError(t, err)
	lineID := order.Items[0].ID

	profileID := newShippingProfile(ctx, t, "E2E Uncanceled Profile")
	optionID := newShippingOption(ctx, t, profileID, "E2E Uncanceled Shipping", shippingOptionFee, false)

	opened, err := adminRequestWithBody(http.MethodPost, "/admin/v1/fulfillments", map[string]any{
		"reference":          placed.OrderID,
		"shipping_option_id": optionID,
		"idempotency_key":    "e2e-uncanceled-" + placed.OrderID,
		"items": []map[string]any{
			{"line_item_id": lineID, "quantity": cancelInParcel},
		},
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, opened.Code, "body: %s", opened.Body.String())

	var parcel struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(opened.Body.Bytes(), &parcel))

	dropped, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/fulfillments/"+parcel.Data.ID+"/cancel", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, dropped.Code, "body: %s", dropped.Body.String())

	// Never rather than Eventually: the claim is that nothing happens, and a poll
	// that passes on the first read would pass before the handler ran.
	//
	// The condition does NOT assert, for the reason written out over
	// [requireStockEventually] — and this one is the proof that the reason is real
	// rather than theoretical. The first version called the shared stockLevel
	// helper here too. It passed when run alone and brought the whole e2e package
	// down when run in the suite: the goroutine outlives the assertion, the test's
	// context is canceled under it, the read fails, and the require inside it is a
	// FailNow on a test that has finished.
	require.Never(t, func() bool {
		levels, err := inventorySvc.ListInventoryLevels(ctx, inventoryItemID)
		if err != nil || len(levels) != 1 {
			// Nothing can be concluded from a read that did not happen, and
			// "true" here would report a stock change that was never observed.
			return false
		}

		return levels[0].StockedQuantity != cancelStockAfterSale
	}, 2*time.Second, 100*time.Millisecond,
		"nobody wrote these units off, so they are still owed to the customer; "+
			"crediting them to the shelf would sell the same goods twice")
}

// requireStockEventually waits for the shelf to reach the expected quantity.
//
// The cancellation flow is driven by the bus, and the in-memory backend gives
// each handler its own goroutine on purpose — a handler is not the publisher's
// work. So the effect is AWAITED. The failure message carries the last value
// read, because "it never got there" and "it went somewhere else" are different
// faults and the second one is the interesting one.
func requireStockEventually(
	ctx context.Context, t *testing.T, inventoryItemID string, want int64, why string,
) {
	t.Helper()

	// The poll does NOT assert, and the message is built after it. Two drafts of
	// this helper got that wrong in two different ways; both are written out below
	// because each produced a diagnostic that pointed at the wrong mechanism.
	//
	// Testify runs the condition in its own goroutine, and a require inside it
	// calls t.FailNow, which is runtime.Goexit on a goroutine that is not the
	// test's: the tick dies silently, no value is ever recorded, and the timeout
	// reports whatever the variable was initialized to. The first draft used the
	// shared stockLevel helper — which asserts — and reported "last read: 0" for a
	// shelf that was not empty, which would have sent the next reader looking for
	// a stock fault instead of a wiring one.
	//
	// So the read is plain, its error is CARRIED, and both are reported.
	var (
		last    atomic.Int64
		lastErr atomic.Value
	)
	reached := assert.Eventually(t, func() bool {
		levels, err := inventorySvc.ListInventoryLevels(ctx, inventoryItemID)
		if err != nil {
			lastErr.Store(err.Error())

			return false
		}
		if len(levels) != 1 {
			lastErr.Store(fmt.Sprintf("the item is leveled at %d locations, not one", len(levels)))

			return false
		}
		last.Store(levels[0].StockedQuantity)

		return levels[0].StockedQuantity == want
	}, 5*time.Second, 50*time.Millisecond)

	// The message is built AFTER the poll, not passed into it.
	//
	// Eventuallyf takes its arguments like any call: they are evaluated before the
	// poll starts, so a live counter handed in there reports the value it had at
	// zero seconds. The second draft of this helper did exactly that and printed
	// "last read: 0" for a shelf holding seven — a diagnostic that accuses the
	// wrong mechanism is worse than none.
	if !reached {
		t.Fatalf("the shelf never reached %d (last read: %d, last error: %v). %s",
			want, last.Load(), lastErr.Load(), why)
	}
}
