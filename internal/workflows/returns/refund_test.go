package returns

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

const testCollectionID = "pcol_1"

// refundHarness builds a harness whose return has already been received and
// whose order is bound to a collection.
func refundHarness(t *testing.T) *harness {
	t.Helper()

	h := newHarness(t)
	h.orders.detail.Status = statusReceived
	h.links.links[testOrderID] = []string{testCollectionID}
	h.payments.refunded = 1200
	h.payments.captured = 6100
	h.payments.totalRefund = 1200

	return h
}

// TestRefundingSendsTheMoneyBackAndTellsTheOrder is the half ADR 0022 left
// open.
//
// Refunds were made through the payment module's own admin API, which has no
// order-side caller, so a refund never reached the order's summary. The B2B
// spending window subtracts that figure, which is why a refunded B2B order
// never returned the employee's budget.
func TestRefundingSendsTheMoneyBackAndTellsTheOrder(t *testing.T) {
	h := refundHarness(t)

	out, err := h.wf.RefundReturn(context.Background(), testReturnID, 1200, "damaged")
	require.NoError(t, err)

	require.Len(t, h.payments.refundCalls, 1)
	assert.Equal(t, refundCall{collectionID: testCollectionID, amount: 1200, reason: "damaged"},
		h.payments.refundCalls[0])

	assert.Equal(t, int64(1200), out.RefundedAmount)
	assert.True(t, out.SummaryRecorded)
	assert.Equal(t, 1, h.orders.summaryCalls)
	assert.Equal(t, testOrderID, h.orders.summaryOrder)
	assert.Equal(t, int64(1200), h.orders.summaryRefunded)
}

// TestTheRecordedFigureIsTheCOLLECTIONsRunningTotal is why the amount is read
// back rather than added.
//
// The order's summary is the LIFETIME refunded total, not the size of this
// refund. Reporting what this call sent would be wrong the moment a second
// refund exists — and it is also what makes the write safe to repeat, because
// a total merges where a delta would double.
func TestTheRecordedFigureIsTheCOLLECTIONsRunningTotal(t *testing.T) {
	h := refundHarness(t)
	// This call sends 1200 back, but the collection has 3000 refunded in total
	// because an earlier refund already stood against it.
	h.payments.refunded = 1200
	h.payments.totalRefund = 3000

	out, err := h.wf.RefundReturn(context.Background(), testReturnID, 1200, "")
	require.NoError(t, err)

	assert.Equal(t, int64(1200), out.RefundedAmount, "the result reports what THIS call sent")
	assert.Equal(t, int64(3000), h.orders.summaryRefunded,
		"the order is told the LIFETIME total, not this refund")
}

// TestAnUnreceivedReturnIsNotRefunded keeps money from going back for goods
// nobody has seen.
//
// A shop that wants to pay before inspection can receive first and refund
// immediately — the same two facts in the same order — but that is the shop's
// decision to make explicitly rather than the framework's to assume.
func TestAnUnreceivedReturnIsNotRefunded(t *testing.T) {
	h := refundHarness(t)
	h.orders.detail.Status = "requested"

	_, err := h.wf.RefundReturn(context.Background(), testReturnID, 1200, "")

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindConflict, coreerrors.KindOf(err))
	assert.Empty(t, h.payments.refundCalls)
}

// TestAnOrderWithNoCollectionCannotBeRefunded says which thing is missing.
func TestAnOrderWithNoCollectionCannotBeRefunded(t *testing.T) {
	h := refundHarness(t)
	delete(h.links.links, testOrderID)

	_, err := h.wf.RefundReturn(context.Background(), testReturnID, 1200, "")

	require.Error(t, err)
	assert.Equal(t, CodeNoPayment, coreerrors.CodeOf(err))
	assert.Empty(t, h.payments.refundCalls)
}

// TestAZeroAmountRefundsWhatIsLeft carries the "give the money back" case
// through untouched.
func TestAZeroAmountRefundsWhatIsLeft(t *testing.T) {
	h := refundHarness(t)

	_, err := h.wf.RefundReturn(context.Background(), testReturnID, 0, "")
	require.NoError(t, err)

	require.Len(t, h.payments.refundCalls, 1)
	assert.Zero(t, h.payments.refundCalls[0].amount,
		"zero has to reach the payment module, which is where it means 'everything left'")
}

// TestMoneyThatLEFTIsRecordedEvenWhenTheRefundFailedPartWay keeps a partial
// refund from being reported as nothing.
//
// A caller told only "it failed" would record nothing and would be right to
// retry the whole amount — sending part of it a second time.
func TestMoneyThatLEFTIsRecordedEvenWhenTheRefundFailedPartWay(t *testing.T) {
	h := refundHarness(t)
	h.payments.refunded = 700
	h.payments.totalRefund = 700
	h.payments.refundErr = errors.New("the provider refused the second capture")

	out, err := h.wf.RefundReturn(context.Background(), testReturnID, 1200, "")
	require.NoError(t, err, "money that moved must not be reported as an outright failure")

	assert.Equal(t, int64(700), out.RefundedAmount)
	assert.NotEmpty(t, out.Warnings)
	assert.True(t, out.SummaryRecorded, "the order still has to learn about the part that left")
	assert.Equal(t, int64(700), h.orders.summaryRefunded)
}

// TestARefundThatMovedNOTHINGIsAnError keeps the other side of that line.
func TestARefundThatMovedNOTHINGIsAnError(t *testing.T) {
	h := refundHarness(t)
	h.payments.refunded = 0
	h.payments.refundErr = errors.New("the provider is unreachable")

	_, err := h.wf.RefundReturn(context.Background(), testReturnID, 1200, "")

	require.Error(t, err)
	assert.Equal(t, CodeRefundFailed, coreerrors.CodeOf(err))
	assert.Equal(t, 0, h.orders.summaryCalls, "nothing may be recorded when nothing moved")
}

// TestAnUnrecordedRefundIsREPORTEDNotHidden is the risk the ordering accepts.
//
// The refund reaches a provider and the summary write is local, so the local
// write goes last — otherwise the order would record money a provider then
// refused to send. The opposite risk is money sent and not recorded, and it is
// reported rather than swallowed.
func TestAnUnrecordedRefundIsREPORTEDNotHidden(t *testing.T) {
	h := refundHarness(t)
	h.orders.summaryErr = errors.New("the order module is unreachable")

	out, err := h.wf.RefundReturn(context.Background(), testReturnID, 1200, "")
	require.NoError(t, err, "the money left; the call did not fail")

	assert.Equal(t, int64(1200), out.RefundedAmount)
	assert.False(t, out.SummaryRecorded)
	assert.NotEmpty(t, out.Warnings)
}
