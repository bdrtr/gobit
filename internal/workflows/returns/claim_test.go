package returns

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
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
	assert.Equal(t, int64(800), h.payments.refundCalls[0].amount,
		"a zero amount means the CLAIM's own figure, not the whole collection")
	assert.Equal(t, int64(800), out.RefundedAmount)
	assert.True(t, out.SummaryRecorded)
	assert.Equal(t, 1, h.orders.completeCalls)
}

// TestAZeroAmountMeansTheCLAIMsFigure is the difference from a return refund.
//
// On a return, zero means "everything the collection holds" — the customer is
// getting their order back. A claim carries what was AGREED, and defaulting to
// the whole collection would turn "settle this claim" into "refund the order".
func TestAZeroAmountMeansTheCLAIMsFigure(t *testing.T) {
	h := claimHarness(t)
	h.orders.claim.RefundAmount = 250
	h.payments.refunded = 250

	_, err := h.wf.SettleClaim(context.Background(), testClaimID, 0, "")
	require.NoError(t, err)

	require.Len(t, h.payments.refundCalls, 1)
	assert.Equal(t, int64(250), h.payments.refundCalls[0].amount)
}

// TestAReplacementClaimIsREFUSEDNotStamped keeps a settlement from being
// recorded when nothing was sent.
//
// Shipping goods against an existing order is not a capability this framework
// has. Marking the claim complete would say the customer got something.
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
	assert.Empty(t, h.payments.refundCalls)
}

// TestTheClaimIsStampedLAST holds the ordering.
//
// The money has moved either way. A stamp that could not be written leaves a
// claim an operator can settle again — visible — while stamping first would
// leave one that looks settled with nothing sent.
func TestTheClaimIsStampedLAST(t *testing.T) {
	h := claimHarness(t)
	h.orders.completeErr = coreerrors.Internal("order_down", "the order module is unreachable")

	out, err := h.wf.SettleClaim(context.Background(), testClaimID, 0, "")
	require.NoError(t, err, "the money left; the call did not fail")

	assert.Equal(t, int64(800), out.RefundedAmount)
	assert.NotEmpty(t, out.Warnings)
}
