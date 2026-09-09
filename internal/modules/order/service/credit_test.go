package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestACreditLowersWhatIsOwedAndNotWhatWasSold is the whole decision in one
// assertion.
//
// The order's total is the cart's snapshot and it is pinned to the order's lines
// by a CHECK constraint. A concession agreed after the sale changes what the
// customer has left to pay, not what they bought.
func TestACreditLowersWhatIsOwedAndNotWhatWasSold(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	credit, err := e.svc.CreateCreditLine(ctx, order.ID, service.CreateCreditLineInput{
		Amount: 1000, Reason: "goodwill",
	})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(credit.ID, models.CreditLineIDPrefix))

	detail, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)

	assert.Equal(t, order.Total, detail.Total, "the order's total does NOT move")
	assert.Equal(t, int64(1000), detail.CreditedTotal)
	assert.Equal(t, order.Total-1000, detail.Summary.Outstanding(detail.Total, detail.CreditedTotal),
		"what changes is the amount left to collect")
}

// TestACreditCannotWriteOffMoreThanTheOrderIsWorth is the ceiling.
//
// A credit past the total is a data-entry error rather than a concession: it
// would make the shop owe more than the sale was ever worth, and no verb in this
// module could settle the difference.
func TestACreditCannotWriteOffMoreThanTheOrderIsWorth(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	_, err = e.svc.CreateCreditLine(ctx, order.ID, service.CreateCreditLineInput{
		Amount: order.Total, Reason: "the whole sale",
	})
	require.NoError(t, err, "writing off exactly the total is legitimate")

	_, err = e.svc.CreateCreditLine(ctx, order.ID, service.CreateCreditLineInput{
		Amount: 1, Reason: "one more",
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, service.CodeCreditExceedsOrder, errors.CodeOf(err))
	assert.Contains(t, err.Error(), "more than the order is worth")
}

// TestTheCeilingCountsWhatWasAlreadyCredited keeps it a running rule rather than
// a per-credit one.
func TestTheCeilingCountsWhatWasAlreadyCredited(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	for range 3 {
		_, err = e.svc.CreateCreditLine(ctx, order.ID, service.CreateCreditLineInput{
			Amount: 2000, Reason: "partial",
		})
		require.NoError(t, err)
	}

	_, err = e.svc.CreateCreditLine(ctx, order.ID, service.CreateCreditLineInput{
		Amount: 2000, Reason: "one too many",
	})
	require.Error(t, err, "3 x 2000 = 6000 against a total of 6100 leaves room for 100, not 2000")

	detail, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(6000), detail.CreditedTotal, "the refused credit was not written")
}

// TestACreditGrantedAfterPaymentMakesTheShopOwe is why the ceiling is the TOTAL
// and not the outstanding amount.
//
// A concession made after the customer paid is legitimate; it turns the
// outstanding amount negative, which is this module's word for "the shop owes
// the customer" and is exactly what a refund then settles.
func TestACreditGrantedAfterPaymentMakesTheShopOwe(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	_, err = e.store.SetSummaryTotals(ctx, order.ID, order.Total, 0)
	require.NoError(t, err)

	_, err = e.svc.CreateCreditLine(ctx, order.ID, service.CreateCreditLineInput{
		Amount: 1000, Reason: "late goodwill",
	})
	require.NoError(t, err, "a credit after payment is legitimate")

	detail, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(-1000),
		detail.Summary.Outstanding(detail.Total, detail.CreditedTotal),
		"the shop now owes the customer, which is what a refund settles")
}

// TestACreditWithoutAReasonIsRefused keeps an unexplained concession out.
//
// A credit with no reason is a number nobody can answer a question about six
// months later.
func TestACreditWithoutAReasonIsRefused(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	_, err = e.svc.CreateCreditLine(ctx, order.ID, service.CreateCreditLineInput{
		Amount: 100, Reason: "   ",
	})

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
}

// TestANegativeCreditIsRefused keeps "credit" the word for one act.
//
// Charging a customer more after the sale is a different verb with a different
// authorization; it would have to reach the payment module rather than this
// table.
func TestANegativeCreditIsRefused(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	for _, amount := range []int64{0, -100} {
		_, err = e.svc.CreateCreditLine(ctx, order.ID, service.CreateCreditLineInput{
			Amount: amount, Reason: "wrong direction",
		})
		require.Error(t, err, "amount %d", amount)
		assert.True(t, errors.IsInvalid(err))
	}
}

// TestTheCreditsAreListedOldestFirst is the order a support screen reads them
// in: what happened, in the sequence it happened.
func TestTheCreditsAreListedOldestFirst(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	for _, reason := range []string{"first", "second", "third"} {
		_, err = e.svc.CreateCreditLine(ctx, order.ID, service.CreateCreditLineInput{
			Amount: 100, Reason: reason,
		})
		require.NoError(t, err)
	}

	credits, err := e.svc.ListCreditLines(ctx, order.ID)
	require.NoError(t, err)
	require.Len(t, credits, 3)
	assert.Equal(t, "first", credits[0].Reason)
	assert.Equal(t, "third", credits[2].Reason)
}
