package returns

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/core/errors"
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
// # Only the REFUND kind, and the other kind is sent somewhere else
//
// A claim is settled either with money or with goods. This verb does the first
// and [Workflows.DispatchReplacement] does the second, so a claim to be settled
// with goods is refused here and told where to go — rather than being quietly
// marked complete while nothing was sent.
//
// The refusal used to say the framework could not ship a replacement at all.
// That was true until the record of WHAT to send existed (ADR 0089) and the
// flow that sends it (ADR 0090); what is left is that money and goods are two
// different verbs, which is a distinction rather than a limit.
//
// # A claim is refunded once
//
// The amount asked is the ceiling of the refunds naming the claim (ADR 0433),
// so a refund already naming it refuses the next. A claim still requested that
// a refund already names is one whose stamp failed after its money left, or
// one that met another settle: it is recorded settled and nothing moves,
// which is what the settle that made the refund would have done.
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
			"claim %s is settled with a %s, not with money; a replacement is sent by "+
				"dispatching the record of what to send, and sending it settles the claim",
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

	// The amount asked is the ceiling as well (ADR 0433): a claim keeps a figure
	// typed above its own, and a refund that already names the claim refuses
	// the next, which holds two settles that meet, or one whose stamp failed,
	// to one refund.
	refunded, err := w.payments.RefundCollection(ctx, collectionID, amount, amount, reason, claimID)
	if err != nil && refunded == 0 {
		if errors.CodeOf(err) == codeRefundExceedsCause {
			return w.settleRefundedClaim(ctx, detail, collectionID, err)
		}
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
	// that could not be written leaves a claim that still looks open — which is
	// visible, and settling it again is refused because a refund names it
	// (ADR 0433) — while stamping first would leave one that looks settled with
	// nothing sent.
	if err := w.orders.CompleteClaim(ctx, claimID); err != nil {
		w.log.ErrorContext(ctx,
			"the claim could not be stamped settled; the money LEFT and the claim still looks open",
			"claim_id", claimID, "order_id", detail.OrderID, "refunded", refunded, "error", err)
		result.Warnings = append(result.Warnings, "the claim was not stamped settled: "+err.Error())
	}

	return result, nil
}

// settleRefundedClaim records as settled a claim still requested that a refund
// already names (ADR 0433). Its money left with that refund, so nothing moves:
// the order's summary is brought up to the collection and the claim is
// stamped, which the settle that made the refund would have done had its stamp
// been written. A stamp that cannot be written answers
// [CodeClaimRefunded] and leaves the claim as it was.
func (w *Workflows) settleRefundedClaim(
	ctx context.Context, detail claimDetail, collectionID string, refused error,
) (SettleClaimResult, error) {
	result := SettleClaimResult{ClaimID: detail.ClaimID, OrderID: detail.OrderID}

	refundResult := RefundResult{ReturnID: detail.ClaimID, OrderID: detail.OrderID}
	w.recordRefund(ctx, collectionID, &refundResult)
	result.SummaryRecorded = refundResult.SummaryRecorded

	if err := w.orders.CompleteClaim(ctx, detail.ClaimID); err != nil {
		return SettleClaimResult{}, errors.Wrap(err, errors.KindOf(err), CodeClaimRefunded,
			"a refund already names claim %s, so nothing was refunded again, and the claim could "+
				"not be recorded settled", detail.ClaimID)
	}
	w.log.WarnContext(ctx, "a refund already named the claim; it was recorded settled and nothing moved",
		"claim_id", detail.ClaimID, "order_id", detail.OrderID, "collection_id", collectionID,
		"refunds", messageOf(refused))

	result.Warnings = append(result.Warnings,
		"a refund already named the claim, so nothing was refunded again and the claim is recorded "+
			"settled with what was given back: "+messageOf(refused))
	result.Warnings = append(result.Warnings, refundResult.Warnings...)

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
