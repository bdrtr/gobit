package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// These tests are about the DECISION and the SENTENCE, which is what a fake
// store can prove: whether an order is judged settled, what the report keeps,
// and what it says about what it kept. Whether the columns really are NULL in
// the database is the integration test's half (erasure_integration_test.go),
// and a fake that agreed with a broken statement would agree with it silently.

// refundableInput produces an order of exactly 12000 minor units.
//
// The amount is spelled out rather than derived from [validInput] because the
// tests below pay it and refund it in full, and a reader checking the arithmetic
// of a refund has to be able to see the number the refund is equal to:
// 9000 subtotal + 1800 tax + 1200 shipping = 12000.
func refundableInput() service.CreateOrderInput {
	in := validInput()
	in.Subtotal = 9000
	in.TaxTotal = 1800
	in.ShippingTotal = 1200
	in.Total = 12000
	in.Items[0].UnitPrice = 3000
	in.Items[0].Subtotal = 9000
	in.Items[0].TaxTotal = 1800
	in.Items[0].Total = 10800

	return in
}

// TestEraseForgetsAnOrderThatWasREFUNDEDInFull is the regression the settlement
// predicate was written wrongly for.
//
// paid_total NEVER SHRINKS: the summary merges it with GREATEST so that
// unordered, at-least-once payment events converge, and a refund is recorded by
// growing refunded_total beside it. Read as total - (paid - refunded) — the
// difference a payment screen wants — this fully refunded order owes its whole
// 12000 for ever, so the module answered RETAINED for it on every sweep for
// ever: the person who had been given all of their money back was the one
// person this module could never forget, and the report kept telling the
// controller the sale was still moving after it had finished.
func TestEraseForgetsAnOrderThatWasREFUNDEDInFull(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	env := newEnv(t)

	in := refundableInput()
	ord, err := env.svc.CreateOrder(ctx, in)
	require.NoError(t, err)

	_, err = env.svc.SetOrderSummaryTotals(ctx, ord.ID,
		service.SummaryTotalsInput{PaidTotal: 12000, RefundedTotal: 12000})
	require.NoError(t, err)
	_, err = env.svc.CompleteOrder(ctx, ord.ID)
	require.NoError(t, err)

	result, err := env.svc.Erase(ctx, personaldata.Subject{CustomerID: testCustomerID})
	require.NoError(t, err)

	assert.Equal(t, personaldata.Anonymized, result.Outcome,
		"the money has stopped moving on this order and nothing is owed in either direction; "+
			"retaining it refuses to forget a refunded buyer for ever: %s", result.Why)
	assert.Positive(t, result.Rows, "the order row had to be rewritten")
	assert.NotContains(t, result.Why, "outstanding",
		"nothing is outstanding on an order that was paid and refunded in full")
}

// TestEraseStillHoldsAnOrderWhoseMoneyIsMoving guards the other direction: the
// fix must not turn the refusal off.
func TestEraseStillHoldsAnOrderWhoseMoneyIsMoving(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	cases := []struct {
		name           string
		paid, refunded int64
	}{
		{name: "nothing was ever collected"},
		{name: "a PART of it was collected and given back", paid: 5000, refunded: 5000},
		{name: "MORE than the sale was collected and is owed back", paid: 13000},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := newEnv(t)
			ord, err := env.svc.CreateOrder(ctx, refundableInput())
			require.NoError(t, err)

			if tc.paid > 0 {
				_, err = env.svc.SetOrderSummaryTotals(ctx, ord.ID,
					service.SummaryTotalsInput{PaidTotal: tc.paid, RefundedTotal: tc.refunded})
				require.NoError(t, err)
			}
			_, err = env.svc.CompleteOrder(ctx, ord.ID)
			require.NoError(t, err)

			result, err := env.svc.Erase(ctx, personaldata.Subject{CustomerID: testCustomerID})
			require.NoError(t, err)

			assert.Equal(t, personaldata.Retained, result.Outcome,
				"money is still owed in one direction or the other: %s", result.Why)
			assert.Contains(t, result.Why, "money is still outstanding on the order")
		})
	}
}

// TestTheRetainedSentenceSeparatesTheTwoKindsOfKeptColumn checks the half of the
// report that is a STATEMENT MADE TO A PERSON.
//
// A retained answer keeps every declared column, and the columns have two
// different futures: eleven of them are nulled the day the order settles, and
// the rest — the free-form ones, orders.customer_id and
// order_addresses.country_code — are never rewritten by any settlement. A
// sentence that says "everything listed stays until it settles" promises the
// person that their metadata blobs and the operator's notes disappear on a day
// that will never come.
func TestTheRetainedSentenceSeparatesTheTwoKindsOfKeptColumn(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	env := newEnv(t)

	// A pending order: nothing about it has been performed yet, so it is
	// retained on the plainest fact there is.
	_, err := env.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	result, err := env.svc.Erase(ctx, personaldata.Subject{CustomerID: testCustomerID})
	require.NoError(t, err)

	require.Equal(t, personaldata.Retained, result.Outcome)
	require.NotEmpty(t, result.Why)

	assert.Contains(t, result.Kept, "orders.email",
		"the e-mail is kept while the order is performed and nulled when it settles")
	assert.Contains(t, result.Kept, "orders.metadata",
		"a free-form column is kept whatever happens to the order")

	assert.NotContains(t, result.Why, "everything listed stays until it settles",
		"the free-form columns are NOT rewritten when the order settles, and saying they are is a "+
			"promise made to the data subject that nothing will ever keep")
	assert.Contains(t, result.Why, "stay until it settles",
		"the columns the erasure does null have to be named as the ones that go")
	assert.Contains(t, result.Why, "free-form",
		"the clause that says the rest stays for ever is the one that must not be dropped")
	assert.Contains(t, result.Why, "orders.customer_id",
		"the two columns the anonymization keeps on purpose are named, not implied")
	assert.Contains(t, result.Why, "order_addresses.country_code")
}

// TestTheDeclarationNamesTheIdempotencyKey pins one column the declaration used
// to leave out on a false premise.
//
// The exclusion said the key was "derived from the workflow run, not from the
// buyer". It is not derived from anything by this module: the caller puts it in
// the interop snapshot, the module checks only that it is non-empty, unpadded
// and short enough, and stores the text. That is a column whose content the
// embedder controls, which is what [personaldata.Open] means — and an embedder is
// free to build the key out of the buyer's e-mail.
func TestTheDeclarationNamesTheIdempotencyKey(t *testing.T) {
	t.Parallel()

	var declared *personaldata.Holding
	for _, holding := range service.PersonalDataHoldings() {
		if holding.Table == "orders" && holding.Column == "idempotency_key" {
			found := holding
			declared = &found
		}
	}

	require.NotNil(t, declared,
		"orders.idempotency_key holds whatever the caller typed and the declaration has to say so")
	assert.Equal(t, personaldata.Open, declared.Kind,
		"gobit does not build this value and does not read what is in it")
	assert.NotEmpty(t, declared.Why)

	// It is kept rather than erased, and the report has to admit that: nulling
	// it would take the handle a retried saga step finds the order by out of
	// orders_idempotency_key_uniq.
	ctx := context.Background()
	env := newEnv(t)

	in := validInput()
	in.IdempotencyKey = "wf_ERASURE"
	ord, err := env.svc.CreateOrder(ctx, in)
	require.NoError(t, err)

	_, err = env.svc.CompleteOrder(ctx, ord.ID)
	require.NoError(t, err)
	_, err = env.svc.SetOrderSummaryTotals(ctx, ord.ID,
		service.SummaryTotalsInput{PaidTotal: ord.Total})
	require.NoError(t, err)

	result, err := env.svc.Erase(ctx, personaldata.Subject{CustomerID: testCustomerID})
	require.NoError(t, err)

	require.Equal(t, personaldata.Anonymized, result.Outcome, result.Why)
	assert.Contains(t, result.Kept, "orders.idempotency_key")

	kept, err := env.svc.GetOrder(ctx, ord.ID)
	require.NoError(t, err)
	assert.Equal(t, "wf_ERASURE", kept.IdempotencyKey,
		"the replay handle survives; a retry that opened a SECOND order for a person who asked to "+
			"be forgotten would be worse than keeping it")
	assert.Equal(t, models.OrderCompleted, kept.Status, "the sale still stands")
}
