package service_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// The constants used in the test data. The region, customer and variant
// identifiers belong to OTHER modules; this module does not validate their
// existence (Principle 2.2).
const (
	testRegionID   = "reg_TEST"
	testCustomerID = "cus_TEST"
	testVariantID  = "variant_TEST"
	testCurrency   = "TRY"
)

// env carries the service and the fakes set up for one test.
type env struct {
	svc   *service.Service
	store *fakeStore
	bus   *fakeBus
}

// newEnv produces a service built with fake dependencies.
func newEnv(t *testing.T) env {
	t.Helper()

	store := newFakeStore()
	bus := newFakeBus()

	svc, err := service.New(service.Options{Repo: store, Events: bus})
	require.NoError(t, err)

	return env{svc: svc, store: store, bus: bus}
}

// validInput produces a consistent order input.
//
// The numbers are deliberately "realistic": 3 x 1000 = 3000 subtotal, 20% tax
// 600, shipping 2500 -> total 6100. The tests exercise the rules one by one by
// breaking this base; every test building its own input would make it invisible
// WHICH field was CHANGED.
func validInput() service.CreateOrderInput {
	return service.CreateOrderInput{
		RegionID:      testRegionID,
		CustomerID:    testCustomerID,
		Email:         "Customer@Example.COM",
		CurrencyCode:  "try",
		CartID:        "cart_TEST",
		Subtotal:      3000,
		DiscountTotal: 0,
		TaxTotal:      600,
		ShippingTotal: 2500,
		Total:         6100,
		Metadata:      map[string]any{"channel": "web"},
		Items: []service.CreateOrderItemInput{
			{
				VariantID: testVariantID,
				Title:     "Red T-Shirt",
				Quantity:  3,
				UnitPrice: 1000,
				Subtotal:  3000,
				TaxTotal:  600,
				Total:     3600,
			},
		},
	}
}

// TestCreateOrderWritesTheOrderItsLinesAndItsSummary validates the happy path.
func TestCreateOrderWritesTheOrderItsLinesAndItsSummary(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(order.ID, models.OrderIDPrefix))
	assert.Equal(t, models.OrderPending, order.Status)
	assert.Equal(t, "TRY", order.CurrencyCode, "the currency has to be folded to upper case")
	assert.Equal(t, "customer@example.com", order.Email, "the e-mail has to be folded to lower case")
	assert.Equal(t, int64(6100), order.Total)

	// The number is produced by the STORE; the service does not produce it.
	assert.Equal(t, int64(1), order.DisplayID)

	detail, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1)
	assert.Equal(t, testVariantID, detail.Items[0].VariantID)
	assert.Equal(t, int64(3600), detail.Items[0].Total)
	assert.True(t, strings.HasPrefix(detail.Items[0].ID, models.LineItemIDPrefix))

	// The summary is born together with the order and ZEROED.
	assert.Equal(t, order.ID, detail.Summary.OrderID)
	assert.Equal(t, int64(0), detail.Summary.PaidTotal)
	assert.Equal(t, int64(6100), detail.Summary.Outstanding(detail.Total))
}

// TestCreateOrderSecondOrderTakesTheNextNumber validates that the number is
// INCREASING.
func TestCreateOrderSecondOrderTakesTheNextNumber(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	first, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	second, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	assert.Equal(t, int64(1), first.DisplayID)
	assert.Equal(t, int64(2), second.DisplayID)
	assert.NotEqual(t, first.ID, second.ID)
}

// TestCreateOrderRejectsInconsistentInput exercises every layer of the total
// validation one by one.
//
// Every row breaks ONE single field: which rule catches which input becomes
// readable this way. Because the whole input is derived from the valid base, if
// a check is removed ONLY its own row falls.
func TestCreateOrderRejectsInconsistentInput(t *testing.T) {
	cases := map[string]struct {
		corrupt  func(in *service.CreateOrderInput)
		code     string
		contains string
	}{
		"the identity of the order total does not hold": {
			corrupt:  func(in *service.CreateOrderInput) { in.Total = 6099 },
			code:     service.CodeTotalsInconsistent,
			contains: "the order total is inconsistent",
		},
		"the discount exceeds the subtotal but the identity holds": {
			// subtotal=3000, discount=4000, tax=600, shipping=2500 -> total=2100.
			// The identity HOLDS; the only thing that catches it is the discount
			// bound.
			corrupt: func(in *service.CreateOrderInput) {
				in.DiscountTotal = 4000
				in.Total = 2100
			},
			code:     service.CodeTotalsInconsistent,
			contains: "the discount cannot exceed the subtotal",
		},
		"the line subtotal does not match the product with the quantity": {
			corrupt: func(in *service.CreateOrderInput) {
				in.Items[0].Quantity = 2
			},
			code:     service.CodeTotalsInconsistent,
			contains: "the line subtotal is inconsistent",
		},
		"the identity of the line total does not hold": {
			corrupt: func(in *service.CreateOrderInput) {
				in.Items[0].Total = 3599
			},
			code:     service.CodeTotalsInconsistent,
			contains: "the line total is inconsistent",
		},
		"the line discount exceeds the subtotal": {
			corrupt: func(in *service.CreateOrderInput) {
				in.Items[0].DiscountTotal = 4000
				in.Items[0].Total = -400 + 600 // subtotal - discount + tax
			},
			code:     service.CodeTotalsInconsistent,
			contains: "cannot exceed the subtotal",
		},
		"the order subtotal is not equal to the sum of the lines": {
			// The counterpart of a computation that "forgets" to send the lines:
			// the single line adds up to 1000 but the order claims 3000.
			corrupt: func(in *service.CreateOrderInput) {
				in.Items[0].Quantity = 1
				in.Items[0].Subtotal = 1000
				in.Items[0].TaxTotal = 600
				in.Items[0].Total = 1600
			},
			code:     service.CodeTotalsInconsistent,
			contains: "the sum of the line subtotals",
		},
		"an order without lines": {
			corrupt:  func(in *service.CreateOrderInput) { in.Items = nil },
			code:     service.CodeOrderEmpty,
			contains: "at least one line",
		},
		"a negative total": {
			corrupt:  func(in *service.CreateOrderInput) { in.Total = -1; in.Subtotal = -1 },
			code:     service.CodeInvalidInput,
			contains: "cannot be negative",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			e := newEnv(t)

			in := validInput()
			tc.corrupt(&in)

			_, err := e.svc.CreateOrder(ctx, in)

			require.Error(t, err, "an inconsistent input must not be accepted")
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
			assert.Equal(t, tc.code, errors.CodeOf(err))
			assert.Contains(t, err.Error(), tc.contains)

			// A rejected request must write NOTHING.
			listed, listErr := e.svc.ListOrders(ctx, service.ListOrdersInput{})
			orders := listed.Items
			count := listed.Count
			require.NoError(t, listErr)
			assert.Zero(t, count)
			assert.Empty(t, orders)
			assert.Empty(t, e.bus.events(), "a rejected request must not publish an event")
		})
	}
}

// TestCreateOrderValidatesTheRequiredFields shows that the identifier and code
// fields are validated.
func TestCreateOrderValidatesTheRequiredFields(t *testing.T) {
	cases := map[string]func(in *service.CreateOrderInput){
		"an empty region":                func(in *service.CreateOrderInput) { in.RegionID = "" },
		"an empty currency":              func(in *service.CreateOrderInput) { in.CurrencyCode = "" },
		"a currency that is not letters": func(in *service.CreateOrderInput) { in.CurrencyCode = "TR1" },
		"a malformed e-mail":             func(in *service.CreateOrderInput) { in.Email = "customer" },
		"an empty line variant":          func(in *service.CreateOrderInput) { in.Items[0].VariantID = "" },
		"an empty line title":            func(in *service.CreateOrderInput) { in.Items[0].Title = "" },
		"a zero quantity": func(in *service.CreateOrderInput) {
			in.Items[0].Quantity = 0
			in.Items[0].Subtotal = 0
			in.Items[0].TaxTotal = 0
			in.Items[0].Total = 0
			in.Subtotal, in.TaxTotal, in.Total = 0, 0, 2500
		},
		"a customer id with whitespace": func(in *service.CreateOrderInput) { in.CustomerID = " cus_1" },
	}

	for name, corrupt := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			e := newEnv(t)

			in := validInput()
			corrupt(&in)

			_, err := e.svc.CreateOrder(ctx, in)

			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
		})
	}
}

// TestCreateOrderWritesTheRegionAndTheCustomerIntoTheirColumns validates that
// the region and the customer of the order stand IN THEIR OWN COLUMNS.
//
// That is the only place of the relation: the order is not written into a link
// table as well and this claim is the guard of that decision — if a second copy
// were added, the column and the link could diverge.
func TestCreateOrderWritesTheRegionAndTheCustomerIntoTheirColumns(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	assert.Equal(t, testRegionID, order.RegionID)
	assert.Equal(t, testCustomerID, order.CustomerID)
}

// TestCreateOrderGuestOrderIsOpenedWithoutACustomer validates that an order
// given without a customer id is opened as a GUEST order.
func TestCreateOrderGuestOrderIsOpenedWithoutACustomer(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	in := validInput()
	in.CustomerID = ""

	order, err := e.svc.CreateOrder(ctx, in)
	require.NoError(t, err)

	assert.True(t, order.Guest())
	assert.Empty(t, order.CustomerID)
	assert.Equal(t, testRegionID, order.RegionID)
}

// TestCreateOrderRollsBackAnOrderWithoutANumber validates that if the store does
// not give a usable number the order does not stay written.
//
// An order without a number is an order the customer will not be able to find
// anywhere.
func TestCreateOrderRollsBackAnOrderWithoutANumber(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	brokenNumber := int64(0)
	e.store.forceDisplayID = &brokenNumber

	_, err := e.svc.CreateOrder(ctx, validInput())

	require.Error(t, err)
	assert.Equal(t, errors.KindInternal, errors.KindOf(err))
	assert.Equal(t, service.CodeDisplayIDInvalid, errors.CodeOf(err))

	listed, listErr := e.svc.ListOrders(ctx, service.ListOrdersInput{})
	count := listed.Count
	require.NoError(t, listErr)
	assert.Zero(t, count, "an order without a number has to be rolled back")
	assert.Empty(t, e.bus.events())
}

// TestCreateOrderWritesNothingWhenALineCannotBeWritten validates that the order
// and its lines are written in a SINGLE transaction.
func TestCreateOrderWritesNothingWhenALineCannotBeWritten(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.store.failCreateLineItem = errors.Internal("store_failed", "the line could not be written")

	_, err := e.svc.CreateOrder(ctx, validInput())

	require.Error(t, err)
	listed, listErr := e.svc.ListOrders(ctx, service.ListOrdersInput{})
	count := listed.Count
	require.NoError(t, listErr)
	assert.Zero(t, count, "when the line cannot be written the order must not be written either")
	assert.Empty(t, e.bus.events())
}

// TestCreateOrderIdempotencyKeyBlocksASecondOrder validates that a second call
// made with the same key returns the EXISTING order.
func TestCreateOrderIdempotencyKeyBlocksASecondOrder(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	in := validInput()
	in.IdempotencyKey = "wf_STEP_1"

	first, err := e.svc.CreateOrder(ctx, in)
	require.NoError(t, err)

	second, err := e.svc.CreateOrder(ctx, in)
	require.NoError(t, err, "a second call with the same key must not return an error")

	assert.Equal(t, first.ID, second.ID)
	assert.Equal(t, first.DisplayID, second.DisplayID)

	listed, listErr := e.svc.ListOrders(ctx, service.ListOrdersInput{})
	count := listed.Count
	require.NoError(t, listErr)
	assert.Equal(t, int64(1), count, "the second call must not open a new order")
	assert.Len(t, e.bus.events(), 1, "the second call must not publish a second event")
}

// TestCreateOrderConcurrentIdempotentCallAnswersTheRaceLoserToo validates that
// the call returning with a database uniqueness violation also returns the
// existing order.
//
// The scenario is the RACE the cheap path (look up first) cannot catch: neither
// call finds the key, both attempt to write and the index rejects the second
// one. Because the race depends on timing, it is set up deterministically with
// the hook of the fake store.
func TestCreateOrderConcurrentIdempotentCallAnswersTheRaceLoserToo(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	in := validInput()
	in.IdempotencyKey = "wf_STEP_1"

	// The hook itself makes the rival call: our call sets off without finding
	// the key, and just as it is about to write the rival's record comes into
	// being and the index rejects us.
	var rival models.Order
	e.store.hookCreateOrder = func() {
		var rivalErr error
		rival, rivalErr = e.svc.CreateOrder(ctx, in)
		require.NoError(t, rivalErr)
	}

	result, err := e.svc.CreateOrder(ctx, in)

	require.NoError(t, err, "the call that loses the race has to return the existing order, not an error")
	assert.Equal(t, rival.ID, result.ID)

	listed, listErr := e.svc.ListOrders(ctx, service.ListOrdersInput{})
	count := listed.Count
	require.NoError(t, listErr)
	assert.Equal(t, int64(1), count)
}

// TestCreateOrderCallWithoutAKeyOpensANewOrderEveryTime shows that the
// idempotency key is OPTIONAL and that it gives no protection when it is not
// given.
func TestCreateOrderCallWithoutAKeyOpensANewOrderEveryTime(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	first, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	second, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	assert.NotEqual(t, first.ID, second.ID)
	listed, listErr := e.svc.ListOrders(ctx, service.ListOrdersInput{})
	count := listed.Count
	require.NoError(t, listErr)
	assert.Equal(t, int64(2), count)
}

// TestCreateOrderPublishesThePlacedEvent validates the DoD requirement: when an
// order is created "order.placed" is published and its payload carries the
// necessary fields.
func TestCreateOrderPublishesThePlacedEvent(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	events := e.bus.events()
	require.Len(t, events, 1)
	assert.Equal(t, service.EventOrderPlaced, events[0].Name)

	data := events[0].Data
	assert.Equal(t, order.ID, data[service.EventFieldOrderID])
	assert.Equal(t, "1", data[service.EventFieldDisplayID])
	assert.Equal(t, "6100", data[service.EventFieldTotal])
	assert.Equal(t, order.CurrencyCode, data[service.EventFieldCurrencyCode])
	assert.Equal(t, order.CustomerID, data[service.EventFieldCustomerID])
	assert.Equal(t, order.RegionID, data[service.EventFieldRegionID])
	assert.Equal(t, models.OrderPending.String(), data[service.EventFieldStatus])
	assert.Equal(t, "1", data[service.EventFieldItemCount])

	// The e-mail IS NOT PUT into the event: events are written to a durable
	// stream and personal data would spread there needlessly.
	assert.NotContains(t, data, "email")
}

// TestOrderPlacedPayloadDoesNotChangeTypeThroughJSON validates that the event
// payload changes neither TYPE nor VALUE when it goes through the production
// data bus.
//
// The Redis Streams backend in production writes Data with json.Marshal and
// decodes it into a map[string]any when reading (see core/eventbus redis.go).
// Because JSON has a single number type, every field put in as an int64 would
// reach the subscriber as a float64: a subscriber written according to the
// contract would work in development (InMemory) and fall over in production, and
// amounts above 2^53 would be rounded silently — that is, money would travel
// over a float (plan Section 8: float NEVER).
//
// The test imitates that conversion EXACTLY and asks for two things: every value
// HAS TO STAY a string and the amount has to be readable back EXACTLY.
func TestOrderPlacedPayloadDoesNotChangeTypeThroughJSON(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	// An amount above 2^53: a path that passes through a float64 rounds here.
	const largeAmount int64 = 9_007_199_254_740_993

	in := validInput()
	in.ShippingTotal = largeAmount - 3600
	in.Total = largeAmount
	order, err := e.svc.CreateOrder(ctx, in)
	require.NoError(t, err)

	events := e.bus.events()
	require.Len(t, events, 1)

	raw, err := json.Marshal(events[0].Data)
	require.NoError(t, err)
	var delivered map[string]any
	require.NoError(t, json.Unmarshal(raw, &delivered))

	for key, value := range delivered {
		assert.IsType(t, "", value,
			"the %q field has to stay a string once it goes through the data bus", key)
	}

	rawAmount, ok := delivered[service.EventFieldTotal].(string)
	require.True(t, ok, "the amount has to be carried as a string")
	readBack, err := strconv.ParseInt(rawAmount, 10, 64)
	require.NoError(t, err)
	assert.Equal(t, order.Total, readBack, "the amount has to be readable back without rounding")
	assert.Equal(t, largeAmount, readBack)
}

// TestCreateOrderKeepsTheOrderWhenTheEventPublishingFails validates that a
// publishing failure does not drop the order.
//
// The decision is deliberate: the order is a RECORD, whereas the event is an
// announcement. Rolling back an order whose payment was taken because of a
// one-second unavailability of the data bus would be a more expensive loss than
// the thing it tries to protect.
func TestCreateOrderKeepsTheOrderWhenTheEventPublishingFails(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.bus.failErr = errors.Unavailable("eventbus_publish_failed", "the data bus is unreachable")

	order, err := e.svc.CreateOrder(ctx, validInput())

	require.NoError(t, err, "an event publishing failure must not drop the order")
	assert.NotEmpty(t, order.ID)

	readBack, getErr := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, getErr, "the order has to be written")
	assert.Equal(t, order.ID, readBack.ID)
}

// TestCancelOrderIsIdempotent validates that the saga compensation can be called
// a second time (a DoD requirement).
func TestCancelOrderIsIdempotent(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	require.NoError(t, e.svc.CancelOrder(ctx, order.ID, "the payment was declined"))
	require.NoError(t, e.svc.CancelOrder(ctx, order.ID, "a repeat of the compensation"),
		"the second cancellation must not return an error")

	canceled, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, models.OrderCanceled, canceled.Status)
	require.NotNil(t, canceled.CanceledAt)
	assert.Equal(t, "the payment was declined", canceled.CancelReason,
		"the reason of the first cancellation has to be preserved; the cancellation really happened there")
}

// TestCancelOrderTakesTheLock validates that the cancellation takes the row lock
// of the order.
//
// The lock is a CONCURRENCY contract: in a lockless "read the status, write the
// status" flow a concurrent cancellation and completion would overwrite each
// other. In a real database the violation only shows up under a race; here it is
// read directly.
func TestCancelOrderTakesTheLock(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	require.NoError(t, e.svc.CancelOrder(ctx, order.ID, ""))

	assert.Contains(t, e.store.lockedOrders, order.ID,
		"the cancellation has to take the lock of the order")
}

// TestCancelOrderConflictsOnACompletedOrder validates that a completed order
// cannot be canceled.
//
// The payment of a completed order has been collected; a "canceled" stamp would
// turn a collected amount into an amount that is not tied to any order.
func TestCancelOrderConflictsOnACompletedOrder(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	_, err = e.svc.CompleteOrder(ctx, order.ID)
	require.NoError(t, err)

	err = e.svc.CancelOrder(ctx, order.ID, "given up on")

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeNotPending, errors.CodeOf(err))
	assert.Contains(t, err.Error(), "return/exchange")

	current, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, models.OrderCompleted, current.Status, "the status must not change")
}

// TestAPaidOrderCannotBeCanceled is the defect this guard closes, and it lives
// entirely inside "pending".
//
// The checkout saga never completes the order it places — nothing calls
// CompleteOrder except an admin endpoint — so a fully paid order whose stock
// was deducted and whose cart was closed sits at 'pending'. The admin cancel
// endpoint would stamp it canceled: nothing refunded, nothing restocked, the
// money simply no longer belonging to an order.
func TestAPaidOrderCannotBeCanceled(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	_, err = e.svc.SetOrderSummaryTotals(ctx, order.ID,
		service.SummaryTotalsInput{PaidTotal: 12_000})
	require.NoError(t, err)

	err = e.svc.CancelOrder(ctx, order.ID, "the customer changed their mind")

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeNotPending, errors.CodeOf(err))
	assert.Contains(t, err.Error(), "return/refund",
		"the message has to name the path that IS correct")

	current, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, models.OrderPending, current.Status,
		"a paid order must keep its status; a canceled stamp is what detaches the money")
}

// TestAnUnpaidOrderIsStillCancelable keeps the guard from swallowing the case
// it must not touch.
//
// CancelOrder is the create_order step's Compensate. Compensation is skipped
// once a capture has been attempted, and the summary is written after the
// capture — so a compensating saga always arrives with a zero total, and the
// rollback has to keep working.
func TestAnUnpaidOrderIsStillCancelable(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	require.NoError(t, e.svc.CancelOrder(ctx, order.ID, "the payment was declined"))

	current, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, models.OrderCanceled, current.Status)
}

// TestARefundedOrderIsStillNotCancelable holds the edge the merge semantics
// create.
//
// PaidTotal never shrinks — a refund is recorded alongside it, not subtracted
// from it — so an order that was paid and fully refunded still carries a
// collected amount. It must stay uncancelable: the money moved twice and both
// movements belong to this order.
func TestARefundedOrderIsStillNotCancelable(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	_, err = e.svc.SetOrderSummaryTotals(ctx, order.ID,
		service.SummaryTotalsInput{PaidTotal: 12_000, RefundedTotal: 12_000})
	require.NoError(t, err)

	err = e.svc.CancelOrder(ctx, order.ID, "fully refunded")

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
}

// TestCancelOrderNotFoundOnAMissingOrder validates that a missing order returns
// NotFound.
func TestCancelOrderNotFoundOnAMissingOrder(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	err := e.svc.CancelOrder(ctx, "order_MISSING", "")

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestCompleteOrderConflictsOnTheSecondCall validates that completing is NOT
// idempotent.
//
// Unlike the cancellation, completing is not a compensation but a forward step.
// Counting it silently as a success would make a flow in which the same order is
// closed twice invisible.
func TestCompleteOrderConflictsOnTheSecondCall(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	completed, err := e.svc.CompleteOrder(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, models.OrderCompleted, completed.Status)
	require.NotNil(t, completed.CompletedAt)

	_, err = e.svc.CompleteOrder(ctx, order.ID)

	require.Error(t, err, "the second completion has to return an error")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeNotPending, errors.CodeOf(err))
}

// TestCompleteOrderConflictsOnACanceledOrder validates that a cancellation is
// TERMINAL.
func TestCompleteOrderConflictsOnACanceledOrder(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	require.NoError(t, e.svc.CancelOrder(ctx, order.ID, "out of stock"))

	_, err = e.svc.CompleteOrder(ctx, order.ID)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
}

// TestArchiveOrderOnlyAcceptsACompletedOrder validates the precondition of
// archiving.
func TestArchiveOrderOnlyAcceptsACompletedOrder(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	_, err = e.svc.ArchiveOrder(ctx, order.ID)
	require.Error(t, err, "an order that is not completed must not be archivable")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeNotCompleted, errors.CodeOf(err))

	completed, err := e.svc.CompleteOrder(ctx, order.ID)
	require.NoError(t, err)

	archived, err := e.svc.ArchiveOrder(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, models.OrderArchived, archived.Status)
	require.NotNil(t, archived.CompletedAt)
	assert.Equal(t, *completed.CompletedAt, *archived.CompletedAt,
		"archiving must not touch the completion stamp")
}

// TestGetOrderByDisplayIDReadsByTheNumber validates the entry gate of the
// support flow.
func TestGetOrderByDisplayIDReadsByTheNumber(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	detail, err := e.svc.GetOrderByDisplayID(ctx, order.DisplayID)
	require.NoError(t, err)
	assert.Equal(t, order.ID, detail.ID)
	require.Len(t, detail.Items, 1)

	_, err = e.svc.GetOrderByDisplayID(ctx, 0)
	require.Error(t, err, "a zero number has to be invalid")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	_, err = e.svc.GetOrderByDisplayID(ctx, 9999)
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestListOrdersFiltersAndPaginates validates the criteria of the listing.
func TestListOrdersFiltersAndPaginates(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	guest := validInput()
	guest.CustomerID = ""
	_, err := e.svc.CreateOrder(ctx, guest)
	require.NoError(t, err)

	registered, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	require.NoError(t, e.svc.CancelOrder(ctx, registered.ID, "test"))

	customer := testCustomerID
	listed, err := e.svc.ListOrders(ctx, service.ListOrdersInput{CustomerID: &customer})
	orders := listed.Items
	count := listed.Count
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
	require.Len(t, orders, 1)
	assert.Equal(t, registered.ID, orders[0].ID)

	canceled := models.OrderCanceled
	listed, err = e.svc.ListOrders(ctx, service.ListOrdersInput{Status: &canceled})
	count = listed.Count
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	listed, err = e.svc.ListOrders(ctx, service.ListOrdersInput{
		Status: func() *models.OrderStatus { s := models.OrderStatus("shipped"); return &s }(),
	})
	require.Error(t, err, "an undefined status has to be rejected")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	listed, err = e.svc.ListOrders(ctx, service.ListOrdersInput{
		Page: service.Page{Limit: service.MaxLimit + 1},
	})
	require.Error(t, err, "the page ceiling cannot be exceeded")
}

// TestSetOrderSummaryTotalsIsIdempotentAndBounded validates the rules of the
// summary write.
func TestSetOrderSummaryTotalsIsIdempotentAndBounded(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	summary, err := e.svc.SetOrderSummaryTotals(ctx, order.ID,
		service.SummaryTotalsInput{PaidTotal: 6100})
	require.NoError(t, err)
	assert.Equal(t, int64(6100), summary.PaidTotal)
	assert.Equal(t, int64(0), summary.Outstanding(order.Total))

	// Writing the same value a second time is harmless: an absolute write makes
	// repeated payment events idempotent.
	repeat, err := e.svc.SetOrderSummaryTotals(ctx, order.ID,
		service.SummaryTotalsInput{PaidTotal: 6100})
	require.NoError(t, err)
	assert.Equal(t, int64(6100), repeat.PaidTotal)

	// Overcollection IS NOT REJECTED; the outstanding amount shows as negative.
	overpaid, err := e.svc.SetOrderSummaryTotals(ctx, order.ID,
		service.SummaryTotalsInput{PaidTotal: 6500})
	require.NoError(t, err)
	assert.Equal(t, int64(-400), overpaid.Outstanding(order.Total))

	// An amount that was not collected cannot be refunded.
	_, err = e.svc.SetOrderSummaryTotals(ctx, order.ID,
		service.SummaryTotalsInput{PaidTotal: 100, RefundedTotal: 200})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, service.CodeSummaryInvalid, errors.CodeOf(err))
}

// TestSetOrderSummaryTotalsALateReportDoesNotEraseTheRefund validates that the
// summary write is ORDER INDEPENDENT.
//
// Payment events are delivered at least once and THERE IS NO ORDERING GUARANTEE.
// On an overwriting endpoint the reprocessing of a late collection event would
// zero out a refund recorded after it: the call would return without an error,
// the recorded money would disappear and the
// order_summaries_refund_within_paid constraint would not fire either because it
// is a RANGE check.
func TestSetOrderSummaryTotalsALateReportDoesNotEraseTheRefund(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	_, err = e.svc.SetOrderSummaryTotals(ctx, order.ID,
		service.SummaryTotalsInput{PaidTotal: 6100})
	require.NoError(t, err)

	_, err = e.svc.SetOrderSummaryTotals(ctx, order.ID,
		service.SummaryTotalsInput{PaidTotal: 6100, RefundedTotal: 1000})
	require.NoError(t, err)

	// A late collection event is being reprocessed: it does not know the refund.
	late, err := e.svc.SetOrderSummaryTotals(ctx, order.ID,
		service.SummaryTotalsInput{PaidTotal: 6100, RefundedTotal: 0})
	require.NoError(t, err, "a late delivery has to be ignored, not an error")
	assert.Equal(t, int64(1000), late.RefundedTotal,
		"a recorded refund cannot be erased by a late report")
	assert.Equal(t, int64(6100), late.PaidTotal)

	readBack, err := e.svc.GetOrderSummary(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1000), readBack.RefundedTotal)

	// The path that writes money takes the lock of the order too; it is subject
	// to the same concurrency discipline as the rest of the module.
	assert.Contains(t, e.store.lockedOrders, order.ID,
		"the summary write has to take the lock of the order")
}

// TestSetOrderSummaryTotalsNotFoundOnAMissingOrder shows that the summary write
// validates the existence OF THE ORDER.
//
// The check looks at the order, not at the summary row: in a lockless write a
// missing order would look like "the summary was not found" and which record the
// error points at would be lost.
func TestSetOrderSummaryTotalsNotFoundOnAMissingOrder(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	_, err := e.svc.SetOrderSummaryTotals(ctx, "order_MISSING",
		service.SummaryTotalsInput{PaidTotal: 100})

	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "got: %v", err)
	assert.Contains(t, e.store.lockedOrders, "order_MISSING",
		"the existence check has to be done with the lock of the order")
}

// TestNewRejectsASetupWithAMissingDependency validates that the service cannot
// be built with a missing dependency.
//
// The event bus in particular: had it been optional, in an installation where
// registering it was forgotten the order would be written silently but
// "order.placed" would never be published.
func TestNewRejectsASetupWithAMissingDependency(t *testing.T) {
	cases := map[string]service.Options{
		"no store":    {Events: newFakeBus()},
		"no data bus": {Repo: newFakeStore()},
	}

	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := service.New(opts)

			require.Error(t, err)
			assert.Equal(t, service.CodeNotReady, errors.CodeOf(err))
		})
	}
}
