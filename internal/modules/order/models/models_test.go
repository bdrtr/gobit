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

// TestExchangeStatusHasNoCompletedValue is a vocabulary test, and it is here
// because the vocabulary is the promise.
//
// "completed" was a defined status with a stamp column beside it and no writer
// anywhere, so the type advertised a state the framework could not reach.
// Completing an exchange needs goods shipped out against an existing order and,
// on a positive difference, money collected against one; there is no capability
// for the first and the order-to-payment link's one-to-one cardinality forbids
// the second. Both halves are recorded in the source, not inferred here.
func TestExchangeStatusHasNoCompletedValue(t *testing.T) {
	assert.True(t, models.ExchangeRequested.Valid())
	assert.True(t, models.ExchangeCanceled.Valid())
	assert.False(t, models.ExchangeStatus("completed").Valid(),
		"an exchange cannot be completed, so the status must not be accepted")
	assert.False(t, models.ExchangeStatus("").Valid())
}

// TestTheExchangeCancelTable is the transition table read as a table.
//
// The noop entry is the one worth a test: it is what keeps a second click from
// moving the moment the record was actually withdrawn.
func TestTheExchangeCancelTable(t *testing.T) {
	assert.Equal(t, models.AfterSalesProceed, models.ExchangeRequested.CancelAction())
	assert.Equal(t, models.AfterSalesNoop, models.ExchangeCanceled.CancelAction())
	assert.Equal(t, models.AfterSalesConflict, models.ExchangeStatus("completed").CancelAction(),
		"a status this type does not define may not proceed")
}

// TestOrderSummaryOutstanding verifies the computation of the outstanding
// amount.
//
// The value being able to be NEGATIVE is deliberate: overcollection is a real
// phenomenon and clamping it to zero would make it invisible.
func TestOrderSummaryOutstanding(t *testing.T) {
	const orderTotal int64 = 6100

	assert.Equal(t, orderTotal,
		models.OrderSummary{}.Outstanding(orderTotal),
		"with no payment at all the whole amount stays outstanding")
	assert.Equal(t, int64(0),
		models.OrderSummary{PaidTotal: 6100}.Outstanding(orderTotal))
	assert.Equal(t, int64(1000),
		models.OrderSummary{PaidTotal: 6100, RefundedTotal: 1000}.Outstanding(orderTotal),
		"a refunded amount becomes a debt again")
	assert.Equal(t, int64(-400),
		models.OrderSummary{PaidTotal: 6500}.Outstanding(orderTotal),
		"overcollection must show as a negative outstanding amount")
}
