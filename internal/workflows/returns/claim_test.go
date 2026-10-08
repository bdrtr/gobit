package returns

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

const testClaimID = "claim_1"

// claimHarness builds a harness with an open refund claim on a paid order.
func claimHarness(t *testing.T) *harness {
	t.Helper()

	h := newHarness(t)
	h.orders.claim = claimDetail{
		ClaimID:      testClaimID,
		OrderID:      testOrderID,
		Status:       statusRequested,
		ClaimType:    claimTypeRefund,
		RefundAmount: 800,
	}
	h.links.links[testOrderID] = []string{testCollectionID}
	h.payments.refunded = 800
	h.payments.captured = 6100
	h.payments.totalRefund = 800

	return h
}

// TestSettlingAClaimRefundsAndStampsIt is what the claim record could not do.
//
// Claims could be opened, read and listed and nothing acted on them; the module
// deferred acting to phases that never came.
func TestSettlingAClaimRefundsAndStampsIt(t *testing.T) {
	h := claimHarness(t)

	out, err := h.wf.SettleClaim(context.Background(), testClaimID, 0, "arrived broken")
	require.NoError(t, err)

	require.Len(t, h.payments.refundCalls, 1)
	assert.Equal(t, testClaimID, h.payments.refundCalls[0].reference,
		"the refund names the claim that caused it (ADR 0187)")
	assert.Equal(t, int64(800), h.payments.refundCalls[0].amount,
		"a zero amount means the CLAIM's own figure, not the whole collection")
	assert.Equal(t, int64(800), h.payments.refundCalls[0].ceiling,
		"and what it asks is its ceiling (ADR 0433)")
	assert.Equal(t, int64(800), out.RefundedAmount)
	assert.True(t, out.SummaryRecorded)
	assert.Equal(t, 1, h.orders.completeCalls)
}

// TestAZeroAmountMeansTheCLAIMsFigure is the difference from a return refund.
//
// On a return, zero means what the returned units were sold for, less what its
// refunds gave back (ADR 0433). A claim names no goods and carries what was
// AGREED, and defaulting to the whole collection would turn "settle this
// claim" into "refund the order".
func TestAZeroAmountMeansTheCLAIMsFigure(t *testing.T) {
	h := claimHarness(t)
	h.orders.claim.RefundAmount = 250
	h.payments.refunded = 250

	_, err := h.wf.SettleClaim(context.Background(), testClaimID, 0, "")
	require.NoError(t, err)

	require.Len(t, h.payments.refundCalls, 1)
	assert.Equal(t, int64(250), h.payments.refundCalls[0].amount)
}

// TestAClaimIsHeldToWhatItsSettleAsks: the amount asked is the ceiling, so a
// figure typed above the claim's own still pays (ADR 0433).
func TestAClaimIsHeldToWhatItsSettleAsks(t *testing.T) {
	h := claimHarness(t)
	h.payments.refunded = 31_000

	_, err := h.wf.SettleClaim(context.Background(), testClaimID, 31_000, "")
	require.NoError(t, err)

	require.Len(t, h.payments.refundCalls, 1)
	assert.Equal(t, int64(31_000), h.payments.refundCalls[0].amount)
	assert.Equal(t, int64(31_000), h.payments.refundCalls[0].ceiling,
		"the claim's own 800 is not its ceiling")
	assert.Equal(t, 1, h.orders.completeCalls)
}

// TestAClaimARefundAlreadyNamesIsRecordedSettled: two settles that meet, or
// one after a stamp that failed, pay once (ADR 0433); the one the payment
// module refuses moves nothing and records the claim settled.
func TestAClaimARefundAlreadyNamesIsRecordedSettled(t *testing.T) {
	h := claimHarness(t)
	h.payments.refunded = 0
	h.payments.refundErr = coreerrors.Conflict(codeRefundExceedsCause,
		"the refunds naming claim_1 gave back 800 of the 800 it may give back, so nothing is left")

	out, err := h.wf.SettleClaim(context.Background(), testClaimID, 0, "")

	require.NoError(t, err)
	assert.Zero(t, out.RefundedAmount, "nothing moved")
	assert.Equal(t, 1, h.orders.completeCalls, "the claim is recorded settled")
	assert.Equal(t, testClaimID, h.orders.completedID)
	assert.Equal(t, 1, h.orders.summaryCalls, "the order's summary is brought up to the collection")
	require.NotEmpty(t, out.Warnings)
	assert.Contains(t, out.Warnings[0], "gave back 800 of the 800", "with what was given back")
}

// TestAClaimARefundNamesThatCannotBeStampedIsTheClaims: the record that could
// not be written leaves the claim as it was, under the claim's own code.
func TestAClaimARefundNamesThatCannotBeStampedIsTheClaims(t *testing.T) {
	h := claimHarness(t)
	h.payments.refunded = 0
	h.payments.refundErr = coreerrors.Conflict(codeRefundExceedsCause, "spent")
	h.orders.completeErr = coreerrors.Conflict("order_claim_invalid_transition", "claim claim_1 was withdrawn")

	_, err := h.wf.SettleClaim(context.Background(), testClaimID, 0, "")

	require.Error(t, err)
	assert.True(t, coreerrors.IsConflict(err))
	assert.Equal(t, CodeClaimRefunded, coreerrors.CodeOf(err))
}

// TestARetryAfterAFailedStampSettlesTheClaim: the money left and the stamp
// failed, so the claim still looks open; settling it again refunds nothing,
// because a refund already names it, and records it settled (ADR 0433).
func TestARetryAfterAFailedStampSettlesTheClaim(t *testing.T) {
	h := claimHarness(t)
	h.payments.given = map[string]int64{}
	h.orders.completeErr = coreerrors.Internal("order_down", "the order module is unreachable")

	first, err := h.wf.SettleClaim(context.Background(), testClaimID, 0, "arrived broken")
	require.NoError(t, err, "the money left; the call did not fail")
	assert.Equal(t, int64(800), first.RefundedAmount)
	assert.NotEmpty(t, first.Warnings, "the stamp failed")

	h.orders.completeErr = nil
	again, err := h.wf.SettleClaim(context.Background(), testClaimID, 0, "arrived broken")

	require.NoError(t, err, "the retry records the claim settled")
	assert.Zero(t, again.RefundedAmount, "nothing moved the second time")
	assert.Equal(t, int64(800), h.payments.given[testClaimID], "no second refund was made")
	assert.Equal(t, 2, h.orders.completeCalls)
	assert.Equal(t, testClaimID, h.orders.completedID)
	assert.NotEmpty(t, again.Warnings, "the answer says nothing moved")
}

// TestAReplacementClaimIsREFUSEDNotStamped keeps a settlement from being
// recorded when nothing was sent.
//
// Money and goods are two different verbs: this one refunds, and
// [Workflows.DispatchReplacement] sends. Letting the refund verb stamp a claim
// to be settled with goods would say the customer got something.
func TestAReplacementClaimIsREFUSEDNotStamped(t *testing.T) {
	h := claimHarness(t)
	h.orders.claim.ClaimType = claimTypeReplace

	_, err := h.wf.SettleClaim(context.Background(), testClaimID, 0, "")

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindConflict, coreerrors.KindOf(err))
	assert.Contains(t, err.Error(), "replacement")
	assert.Empty(t, h.payments.refundCalls)
	assert.Equal(t, 0, h.orders.completeCalls, "nothing may be stamped when nothing was sent")
}

// TestAClaimWithNoAmountIsRefused stops a settlement that would move nothing.
func TestAClaimWithNoAmountIsRefused(t *testing.T) {
	h := claimHarness(t)
	h.orders.claim.RefundAmount = 0

	_, err := h.wf.SettleClaim(context.Background(), testClaimID, 0, "")

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindInvalid, coreerrors.KindOf(err))
	assert.Empty(t, h.payments.refundCalls)
}

// TestASettledClaimIsNotSettledAgain keeps money from going out twice.
func TestASettledClaimIsNotSettledAgain(t *testing.T) {
	h := claimHarness(t)
	h.orders.claim.Status = "completed"

	_, err := h.wf.SettleClaim(context.Background(), testClaimID, 0, "")

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindConflict, coreerrors.KindOf(err))
	assert.Equal(t, CodeInvalidInput, coreerrors.CodeOf(err),
		"a settled claim is refused by its status, which is what the settle route's text says")
	assert.Empty(t, h.payments.refundCalls)
}

// TestTheClaimIsStampedLAST holds the ordering.
//
// The money has moved either way. A stamp that could not be written leaves a
// claim that still looks open — visible, and a second settle is refused
// because a refund names it — while stamping first would leave one that looks
// settled with nothing sent.
func TestTheClaimIsStampedLAST(t *testing.T) {
	h := claimHarness(t)
	h.orders.completeErr = coreerrors.Internal("order_down", "the order module is unreachable")

	out, err := h.wf.SettleClaim(context.Background(), testClaimID, 0, "")
	require.NoError(t, err, "the money left; the call did not fail")

	assert.Equal(t, int64(800), out.RefundedAmount)
	assert.NotEmpty(t, out.Warnings)
}
