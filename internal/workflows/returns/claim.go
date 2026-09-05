package returns

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// claimDetail is the schema of the order module's claim read.
type claimDetail struct {
	ClaimID      string `json:"claim_id"`
	OrderID      string `json:"order_id"`
	Status       string `json:"status"`
	ClaimType    string `json:"claim_type"`
	RefundAmount int64  `json:"refund_amount"`
}

// Claim settlement kinds, as the order module names them.
const (
	claimTypeRefund  = "refund"
	claimTypeReplace = "replace"
	statusRequested  = "requested"
)

// SettleClaimResult reports what settling a claim did.
type SettleClaimResult struct {
	// ClaimID and OrderID locate the claim.
	ClaimID string
	OrderID string
	// RefundedAmount is what went back (minor unit).
	RefundedAmount int64
	// SummaryRecorded reports whether the order was told.
	SummaryRecorded bool
	// Warnings are the faults that did not stop the settlement.
	Warnings []string
}

// SettleClaim settles a damage or shortage claim by refunding it.
//
// # Only the REFUND kind, and the other kind is refused rather than ignored
//
// A claim is settled either with money or with a replacement. This flow does
// the first. The second needs goods shipped out against an existing order —
// there is no capability for that anywhere in the framework — so a claim of
// that kind is REFUSED here with a message that says so, instead of being
// quietly marked complete while nothing was sent.
//
// # Why it is not a return
//
// A claim is about goods that arrived damaged or short. Nothing comes back, so
// nothing is restocked; the only movement is money. That is why this shares the
// refund half with [Workflows.RefundReturn] and none of the receiving half.
func (w *Workflows) SettleClaim(
	ctx context.Context, claimID string, amount int64, reason string,
) (SettleClaimResult, error) {
	if claimID == "" {
		return SettleClaimResult{}, errors.Invalid(CodeInvalidInput, "the claim id is required")
	}
	if amount < 0 {
		return SettleClaimResult{}, errors.Invalid(CodeInvalidInput,
			"the refunded amount cannot be negative: %d", amount)
	}

	detail, err := w.readClaim(ctx, claimID)
	if err != nil {
		return SettleClaimResult{}, err
	}
	if detail.ClaimType != claimTypeRefund {
		return SettleClaimResult{}, errors.Conflict(CodeInvalidInput,
			"claim %s is settled with a %s, not with money; shipping a replacement against an "+
				"existing order is not something this framework can do yet",
			claimID, detail.ClaimType)
	}
	if detail.Status != statusRequested {
		return SettleClaimResult{}, errors.Conflict(CodeInvalidInput,
			"claim %s is in status %q and can no longer be settled", claimID, detail.Status)
	}

	collectionID, err := w.collectionOf(ctx, detail.OrderID)
	if err != nil {
		return SettleClaimResult{}, err
	}

	// A zero amount means the claim's OWN figure rather than "everything the
	// collection holds": a claim carries what was agreed, and defaulting to the
	// whole collection would turn "settle this claim" into "refund the order".
	if amount == 0 {
		amount = detail.RefundAmount
	}
	if amount == 0 {
		return SettleClaimResult{}, errors.Invalid(CodeInvalidInput,
			"claim %s names no amount, so there is nothing to refund; give one explicitly",
			claimID)
	}

	refunded, err := w.payments.RefundCollection(ctx, collectionID, amount, reason)
	if err != nil && refunded == 0 {
		return SettleClaimResult{}, errors.Wrap(err, errors.KindOf(err), CodeRefundFailed,
			"the refund for claim %s could not be made", claimID)
	}

	result := SettleClaimResult{
		ClaimID:        detail.ClaimID,
		OrderID:        detail.OrderID,
		RefundedAmount: refunded,
	}
	if err != nil {
		w.log.ErrorContext(ctx, "the claim refund was made only in part; a human has to finish it",
			"claim_id", claimID, "collection_id", collectionID, "refunded", refunded, "error", err)
		result.Warnings = append(result.Warnings, "the refund was made only in part: "+err.Error())
	}

	refundResult := RefundResult{ReturnID: claimID, OrderID: detail.OrderID}
	w.recordRefund(ctx, collectionID, &refundResult)
	result.SummaryRecorded = refundResult.SummaryRecorded
	result.Warnings = append(result.Warnings, refundResult.Warnings...)

	// The claim is stamped LAST. The money has moved either way, and a stamp
	// that could not be written leaves a claim an operator can settle again —
	// which is visible — while stamping first would leave one that looks
	// settled with nothing sent.
	if err := w.orders.CompleteClaim(ctx, claimID); err != nil {
		w.log.ErrorContext(ctx,
			"the claim could not be stamped settled; the money LEFT and the claim still looks open",
			"claim_id", claimID, "order_id", detail.OrderID, "refunded", refunded, "error", err)
		result.Warnings = append(result.Warnings, "the claim was not stamped settled: "+err.Error())
	}

	return result, nil
}

// readClaim reads the claim.
func (w *Workflows) readClaim(ctx context.Context, claimID string) (claimDetail, error) {
	raw, err := w.orders.ClaimDetailJSON(ctx, claimID)
	if err != nil {
		return claimDetail{}, errors.Wrap(err, errors.KindOf(err), CodeReturnUnreadable,
			"claim %s could not be read", claimID)
	}

	var detail claimDetail
	if err := json.Unmarshal(raw, &detail); err != nil {
		return claimDetail{}, errors.Wrap(err, errors.KindInternal, CodeReturnUnreadable,
			"the answer for claim %s could not be parsed", claimID)
	}

	return detail, nil
}
