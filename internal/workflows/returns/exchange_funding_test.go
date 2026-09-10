package returns

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// fundingOf builds the order module's answer for a funding decision.
func fundingOf(difference int64, currency, collectionID string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{
		"exchange_id":           "exch_1",
		"order_id":              testOrderID,
		"status":                "requested",
		"difference_due":        difference,
		"currency_code":         currency,
		"payment_collection_id": collectionID,
	})

	return raw
}

// TestAShortCaptureDoesNotFundAnything refuses a collection that is opened for
// the right amount and has not taken it.
//
// The comparison in the code is an equality and this test does not prove that
// it has to be: with the amount pinned to the difference and the payment module
// capping a capture at the amount, what is held can never exceed it, so a floor
// would refuse exactly the same rows. Measured, not assumed — turning it into a
// floor breaks nothing. What the equality and the amount check really do is
// answer two different questions, and the test below covers the other one.
func TestAShortCaptureDoesNotFundAnything(t *testing.T) {
	h := newHarness(t)
	h.orders.funding = fundingOf(1000, "TRY", "")
	h.payments.amount = 1000
	h.payments.captured = 400

	err := h.wf.FundExchangeDifference(context.Background(), "exch_1", "paycol_1")

	require.Error(t, err)
	assert.Equal(t, CodeDifferenceNotHeld, coreerrors.CodeOf(err))
	assert.Equal(t, 0, h.orders.fundCalls, "nothing is recorded when the money is not there")
}

// TestARefundedCollectionDoesNotFundAnything keeps the measure a BALANCE.
//
// A collection captured in full and refunded in full holds nothing. A rule
// reading the capture alone would call that funded, and the record would say a
// balance was handled after the money went back.
func TestARefundedCollectionDoesNotFundAnything(t *testing.T) {
	h := newHarness(t)
	h.orders.funding = fundingOf(1000, "TRY", "")
	h.payments.amount = 1000
	h.payments.captured = 1000
	h.payments.totalRefund = 1000

	err := h.wf.FundExchangeDifference(context.Background(), "exch_1", "paycol_1")

	require.Error(t, err)
	assert.Equal(t, CodeDifferenceNotHeld, coreerrors.CodeOf(err))
	assert.Equal(t, 0, h.orders.fundCalls)
}

// TestANegativeDifferenceIsNotFunded is the sign, refused before any arithmetic
// can be fooled by it.
func TestANegativeDifferenceIsNotFunded(t *testing.T) {
	h := newHarness(t)
	h.orders.funding = fundingOf(-500, "TRY", "")

	err := h.wf.FundExchangeDifference(context.Background(), "exch_1", "paycol_1")

	require.Error(t, err)
	assert.Equal(t, CodeInvalidInput, coreerrors.CodeOf(err))
	assert.Equal(t, 0, h.orders.fundCalls,
		"money owed TO the customer leaves by a refund, which is a different act")
}

// TestACollectionInAnotherCurrencyDoesNotFund is the check that cannot be made
// from the amounts alone.
//
// Every number lines up and the money is still the wrong money. The exchange's
// row has no currency of its own, so the comparison is against the ORDER's.
func TestACollectionInAnotherCurrencyDoesNotFund(t *testing.T) {
	h := newHarness(t)
	h.orders.funding = fundingOf(1000, "TRY", "")
	h.payments.amount = 1000
	h.payments.captured = 1000
	h.payments.currency = "USD"

	err := h.wf.FundExchangeDifference(context.Background(), "exch_1", "paycol_1")

	require.Error(t, err)
	assert.Equal(t, CodeDifferenceNotHeld, coreerrors.CodeOf(err))
	assert.Equal(t, 0, h.orders.fundCalls)
}

// TestACollectionOpenedForMoreThanTheDifferenceIsRefused keeps the ceiling
// structural.
//
// The payment module caps a capture at the collection's amount and that amount
// is written once, so a collection opened for exactly the difference can never
// take more than the exchange owed. One opened for more can.
func TestACollectionOpenedForMoreThanTheDifferenceIsRefused(t *testing.T) {
	h := newHarness(t)
	h.orders.funding = fundingOf(1000, "TRY", "")
	h.payments.amount = 5000
	h.payments.captured = 1000

	err := h.wf.FundExchangeDifference(context.Background(), "exch_1", "paycol_1")

	require.Error(t, err)
	assert.Equal(t, CodeDifferenceNotHeld, coreerrors.CodeOf(err))
}

// TestAHeldDifferenceIsRecorded is the path that works.
func TestAHeldDifferenceIsRecorded(t *testing.T) {
	h := newHarness(t)
	h.orders.funding = fundingOf(1000, "TRY", "")
	h.payments.amount = 1000
	h.payments.captured = 1000

	err := h.wf.FundExchangeDifference(context.Background(), "exch_1", "paycol_1")

	require.NoError(t, err)
	assert.Equal(t, 1, h.orders.fundCalls)
	assert.Equal(t, "exch_1", h.orders.fundedExchange)
	assert.Equal(t, "paycol_1", h.orders.fundedCollection,
		"the record keeps the collection's identifier and never its amount")
}

// TestTheExitSendsTheMoneyBackBeforeItWithdraws pins the order of the two
// halves.
//
// The reverse order would take the request back while the money was still with
// the shop, and the record that named the collection would be gone.
func TestTheExitSendsTheMoneyBackBeforeItWithdraws(t *testing.T) {
	h := newHarness(t)
	h.orders.funding = fundingOf(1000, "TRY", "paycol_1")
	h.payments.amount = 1000
	h.payments.captured = 1000

	err := h.wf.RefundExchangeDifference(context.Background(), "exch_1", "out of stock")

	require.NoError(t, err)
	require.Len(t, h.payments.refundCalls, 1, "everything held goes back")
	assert.Equal(t, int64(1000), h.payments.refundCalls[0].amount)
	assert.Equal(t, 1, h.orders.withdrawCalls)
	assert.Equal(t, "exch_1", h.orders.withdrawnExchange)
}

// TestAFailedRefundLeavesTheExchangeFunded keeps the failure honest.
//
// The record must not say the request was taken back while the money is still
// with the shop. Reporting the failure and leaving the record where it was is
// the direction an operator can act on.
func TestAFailedRefundLeavesTheExchangeFunded(t *testing.T) {
	h := newHarness(t)
	h.orders.funding = fundingOf(1000, "TRY", "paycol_1")
	h.payments.amount = 1000
	h.payments.captured = 1000
	h.payments.refundErr = coreerrors.Conflict("payment_refund_failed", "the provider refused")

	err := h.wf.RefundExchangeDifference(context.Background(), "exch_1", "")

	require.Error(t, err)
	assert.Equal(t, 0, h.orders.withdrawCalls,
		"the request is not taken back while the money is still held")
}

// TestARepeatedExitIsSafe is why the exit is one call rather than two.
//
// A collection with nothing left to refund is skipped rather than refused, so a
// retry after a crash between the halves finishes the job instead of failing on
// the half already done.
func TestARepeatedExitIsSafe(t *testing.T) {
	h := newHarness(t)
	h.orders.funding = fundingOf(1000, "TRY", "paycol_1")
	h.payments.amount = 1000
	h.payments.captured = 1000
	h.payments.totalRefund = 1000

	err := h.wf.RefundExchangeDifference(context.Background(), "exch_1", "")

	require.NoError(t, err)
	assert.Empty(t, h.payments.refundCalls, "there was nothing left to send back")
	assert.Equal(t, 1, h.orders.withdrawCalls, "the record still follows the money")
}
