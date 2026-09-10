package models_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// crockford is the alphabet permitted in an identifier body (Crockford Base32).
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// TestKimlikBicimi verifies that the produced identifiers keep the prefix +
// 26-character body format.
//
// The format is a CONTRACT: an order identifier travels in the log, in the
// support record and in the saga's snapshot; the prefix disappearing or the
// body shortening (uniqueness weakening) must not pass silently.
func TestKimlikBicimi(t *testing.T) {
	cases := map[string]struct {
		gen    func() string
		prefix string
	}{
		"order":    {gen: models.NewOrderID, prefix: models.OrderIDPrefix},
		"line":     {gen: models.NewLineItemID, prefix: models.LineItemIDPrefix},
		"summary":  {gen: models.NewSummaryID, prefix: models.SummaryIDPrefix},
		"return":   {gen: models.NewReturnID, prefix: models.ReturnIDPrefix},
		"exchange": {gen: models.NewExchangeID, prefix: models.ExchangeIDPrefix},
		"claim":    {gen: models.NewClaimID, prefix: models.ClaimIDPrefix},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			id := tc.gen()

			body, ok := strings.CutPrefix(id, tc.prefix)
			require.True(t, ok, "identifier %q must start with the prefix %q", id, tc.prefix)
			assert.Len(t, body, models.IDBodyLen,
				"the body must be %d characters: %q", models.IDBodyLen, id)
			for _, r := range body {
				assert.Contains(t, crockford, string(r),
					"the body must contain only the Crockford Base32 alphabet: %q", id)
			}
		})
	}
}

// TestKimliklerZamanaGoreSiralanir verifies that the identifier itself carries
// the creation order.
//
// Sortability is not idle decoration: under a primary key scan the records
// stand in natural order and B-tree insertions happen at the end. Had the
// identifier been completely random, every insertion would fall into the middle
// of the index.
//
// The resolution is MILLISECONDS: the order of two identifiers produced in the
// same millisecond is left to the random body and is not guaranteed. That is
// why the test waits between rounds; the assertion is not "every identifier is
// greater than the previous one" but "identifiers produced in different
// milliseconds keep the time order".
func TestKimliklerZamanaGoreSiralanir(t *testing.T) {
	const (
		rounds = 12
		wait   = 2 * time.Millisecond
	)

	previous := models.NewOrderID()
	for range rounds {
		time.Sleep(wait)
		next := models.NewOrderID()
		assert.Less(t, previous, next,
			"an identifier produced one millisecond later must sort greater")
		previous = next
	}
}

// TestKimliklerTekildir verifies that identifiers produced in the same
// millisecond do not collide.
func TestKimliklerTekildir(t *testing.T) {
	const count = 1000

	seen := make(map[string]struct{}, count)
	for range count {
		id := models.NewOrderID()
		_, duplicate := seen[id]
		require.False(t, duplicate, "the id repeated: %s", id)
		seen[id] = struct{}{}
	}
}

// TestValidDisplayID verifies the validity threshold of the order number.
//
// A zero or negative number means "an order with no number"; the customer finds
// it nowhere. The service applies this criterion AFTER the order is written and
// rolls back an order that does not satisfy it.
func TestValidDisplayID(t *testing.T) {
	assert.False(t, models.ValidDisplayID(0), "a zero number has to be invalid")
	assert.False(t, models.ValidDisplayID(-1), "a negative number must be invalid")
	assert.True(t, models.ValidDisplayID(models.MinDisplayID))
	assert.True(t, models.ValidDisplayID(1042))
}

// TestOrderTotalsIdentity verifies that the order totals identity and the
// discount bound can be read from the model.
func TestOrderTotalsIdentity(t *testing.T) {
	consistent := models.Order{Subtotal: 3000, DiscountTotal: 500, TaxTotal: 600, ShippingTotal: 2500, Total: 5600}
	assert.True(t, consistent.TotalsConsistent())
	assert.True(t, consistent.DiscountWithinSubtotal())

	inconsistent := consistent
	inconsistent.Total = 5599
	assert.False(t, inconsistent.TotalsConsistent())

	// The identity HOLDS but the discount exceeds the subtotal: the two checks
	// being separate is for exactly this case.
	excessDiscount := models.Order{Subtotal: 1000, DiscountTotal: 3000, ShippingTotal: 2500, Total: 500}
	assert.True(t, excessDiscount.TotalsConsistent(), "the identity holds in this case")
	assert.False(t, excessDiscount.DiscountWithinSubtotal(), "the discount bound must be violated")
}

// TestOrderDurumYardimcilari verifies that the status-based helpers answer
// correctly.
func TestOrderDurumYardimcilari(t *testing.T) {
	assert.True(t, models.Order{Status: models.OrderCanceled}.Canceled())
	assert.False(t, models.Order{Status: models.OrderPending}.Canceled())

	assert.True(t, models.Order{Status: models.OrderCompleted}.Completed())
	assert.True(t, models.Order{Status: models.OrderArchived}.Completed(),
		"an archived order is completed too")
	assert.False(t, models.Order{Status: models.OrderPending}.Completed())

	assert.True(t, models.Order{}.Guest())
	assert.False(t, models.Order{CustomerID: "cus_1"}.Guest())
}

// TestOrderStatusValid verifies that undefined statuses are rejected.
func TestOrderStatusValid(t *testing.T) {
	for _, status := range []models.OrderStatus{
		models.OrderPending, models.OrderCompleted, models.OrderArchived, models.OrderCanceled,
	} {
		assert.True(t, status.Valid(), "%q must be defined", status)
	}
	assert.False(t, models.OrderStatus("shipped").Valid())
	assert.False(t, models.OrderStatus("").Valid())
}

// TestExchangeStatusCarriesTheCompletionAgain is a vocabulary test, and it is
// here because the vocabulary is the promise.
//
// Until ADR 0114 this test asserted the OPPOSITE, and it was right to: "completed"
// was a defined status with a stamp beside it and no writer anywhere, so the type
// advertised a state the framework could not reach. What changed is not the
// reading but the capability — goods can now leave against an existing order
// (ADR 0090), which was one of the two things migration 000008 said completing
// would need.
//
// The other, moving money against an existing order, is still missing. So the
// word is back and its BOUND is elsewhere: an exchange that owes nothing may be
// completed, and the database refuses the rest
// (order_exchanges_completed_owes_nothing). A vocabulary test is the wrong place
// for that bound — a status set says which words exist, not which records may
// wear them.
func TestExchangeStatusCarriesTheCompletionAgain(t *testing.T) {
	assert.True(t, models.ExchangeRequested.Valid())
	assert.True(t, models.ExchangeCompleted.Valid())
	assert.True(t, models.ExchangeCanceled.Valid())
	assert.False(t, models.ExchangeStatus("shipped").Valid(),
		"a word nothing writes is a state the type must not advertise")
	assert.False(t, models.ExchangeStatus("").Valid())
}

// TestOnlyAnExchangeThatOwesNothingIsSettleable is the bound, read from the
// model.
//
// The sign does not matter and that is the point: a positive difference is money
// to collect and a negative one is money to pay back, and this framework can move
// neither against an existing order.
func TestOnlyAnExchangeThatOwesNothingIsSettleable(t *testing.T) {
	assert.True(t, models.Exchange{DifferenceDue: 0}.OwesNothing())
	assert.False(t, models.Exchange{DifferenceDue: 1}.OwesNothing())
	assert.False(t, models.Exchange{DifferenceDue: -1}.OwesNothing(),
		"money owed TO the customer is money all the same")
}

// TestTheExchangeCancelTable is the transition table read as a table.
//
// The noop entry is the one worth a test: it is what keeps a second click from
// moving the moment the record was actually withdrawn.
func TestTheExchangeCancelTable(t *testing.T) {
	assert.Equal(t, models.AfterSalesProceed, models.ExchangeRequested.CancelAction())
	assert.Equal(t, models.AfterSalesNoop, models.ExchangeCanceled.CancelAction())
	assert.Equal(t, models.AfterSalesConflict, models.ExchangeCompleted.CancelAction(),
		"an exchange that was met is not un-met by withdrawing the request")
	assert.Equal(t, models.AfterSalesConflict, models.ExchangeStatus("shipped").CancelAction(),
		"a status this type does not define may not proceed")
}

// TestTheExchangeCompleteTable is the transition that came back.
func TestTheExchangeCompleteTable(t *testing.T) {
	assert.Equal(t, models.AfterSalesProceed, models.ExchangeRequested.CompleteAction())
	assert.Equal(t, models.AfterSalesNoop, models.ExchangeCompleted.CompleteAction(),
		"the FIRST settlement keeps its moment")
	assert.Equal(t, models.AfterSalesConflict, models.ExchangeCanceled.CompleteAction(),
		"a withdrawn request has no goods to answer")
	assert.Equal(t, models.AfterSalesConflict, models.ExchangeStatus("shipped").CompleteAction())
}

// TestOrderSummaryOutstanding verifies the computation of the outstanding
// amount.
//
// The value being able to be NEGATIVE is deliberate: overcollection is a real
// phenomenon and clamping it to zero would make it invisible.
func TestOrderSummaryOutstanding(t *testing.T) {
	const orderTotal int64 = 6100

	assert.Equal(t, orderTotal,
		models.OrderSummary{}.Outstanding(orderTotal, 0),
		"with no payment at all the whole amount stays outstanding")
	assert.Equal(t, int64(0),
		models.OrderSummary{PaidTotal: 6100}.Outstanding(orderTotal, 0))
	assert.Equal(t, int64(1000),
		models.OrderSummary{PaidTotal: 6100, RefundedTotal: 1000}.Outstanding(orderTotal, 0),
		"a refunded amount becomes a debt again")
	assert.Equal(t, int64(-400),
		models.OrderSummary{PaidTotal: 6500}.Outstanding(orderTotal, 0),
		"overcollection must show as a negative outstanding amount")

	// A credit lowers what is OWED, and it does it the same way a payment does:
	// by standing between the order's total and the amount left to collect. The
	// order's own total is not in this arithmetic at all — that is the point of
	// ADR 0105.
	assert.Equal(t, int64(4100),
		models.OrderSummary{}.Outstanding(orderTotal, 2000),
		"a credit lowers what is outstanding without touching what was sold")
	assert.Equal(t, int64(-2000),
		models.OrderSummary{PaidTotal: 6100}.Outstanding(orderTotal, 2000),
		"a credit granted after payment makes the shop owe the customer, which is "+
			"what a refund then settles")
}
