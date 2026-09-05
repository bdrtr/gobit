package models

// This file is the STATE MACHINE of the after-sales records.
//
// The transitions stand here as pure, database-free functions and the service
// only turns the result into a typed error, exactly as the payment module does
// for its sessions. The split is what makes the rule READABLE AS A TABLE
// instead of having to be dug out of ifs spread across three service methods —
// and there are three record types here, so a table is worth three times as
// much.
//
// Until these existed the records could not move at all: every one was born
// "requested" and stayed there, because the module had no UPDATE for them. The
// skeleton said so in writing and deferred the transitions to "the next
// phases".

// AfterSalesAction is the outcome of a request made against an after-sales
// record in its current status.
//
// It is shared by returns, exchanges and claims because the three answer the
// same question with the same three answers; giving each its own type would
// triple the vocabulary without adding a distinction.
type AfterSalesAction int

// After-sales actions.
const (
	// AfterSalesProceed means the transition is valid and has to be written.
	AfterSalesProceed AfterSalesAction = iota
	// AfterSalesNoop means the record IS ALREADY in the target state; the
	// caller succeeds without a second write.
	//
	// It is not the same as Proceed and the difference is the whole reason
	// this type exists: writing again would move the timestamp of a thing that
	// happened earlier, so the record would claim the goods came back at the
	// moment somebody clicked twice.
	AfterSalesNoop
	// AfterSalesConflict means the transition is not valid from here.
	AfterSalesConflict
)

// String returns the action's readable name.
func (a AfterSalesAction) String() string {
	switch a {
	case AfterSalesProceed:
		return "proceed"
	case AfterSalesNoop:
		return "noop"
	case AfterSalesConflict:
		return "conflict"
	default:
		return "unknown"
	}
}

// ReceiveAction returns the outcome of receiving the returned goods in this
// status.
//
// Transition table:
//
//	requested -> proceed   (the goods arrived; received_at is stamped)
//	received  -> noop      (already received; the FIRST arrival keeps its moment)
//	canceled  -> conflict  (a withdrawn request cannot be received; a new one is opened)
func (s ReturnStatus) ReceiveAction() AfterSalesAction {
	switch s {
	case ReturnRequested:
		return AfterSalesProceed
	case ReturnReceived:
		return AfterSalesNoop
	case ReturnCanceled:
		return AfterSalesConflict
	default:
		return AfterSalesConflict
	}
}

// CancelAction returns the outcome of canceling the return in this status.
//
// Transition table:
//
//	requested -> proceed   (the customer or the operator withdraws the request)
//	received  -> conflict  (the goods are HERE; withdrawing the request would
//	                        leave stock in the warehouse that belongs to nobody)
//	canceled  -> noop      (already withdrawn)
//
// The received -> conflict entry is the one that carries weight. A return whose
// goods arrived is a physical fact, and the record is the only thing that says
// where those goods came from; canceling it would not un-receive them.
func (s ReturnStatus) CancelAction() AfterSalesAction {
	switch s {
	case ReturnRequested:
		return AfterSalesProceed
	case ReturnCanceled:
		return AfterSalesNoop
	case ReturnReceived:
		return AfterSalesConflict
	default:
		return AfterSalesConflict
	}
}

// CancelAction returns the outcome of withdrawing the exchange in this status.
//
// Transition table:
//
//	requested -> proceed
//	canceled  -> noop      (already withdrawn; the FIRST withdrawal keeps its
//	                        moment, for the reason AfterSalesNoop exists)
//
// # Why there is no CompleteAction beside it
//
// The exchange is the one after-sales record with two statuses instead of
// three, and the missing transition is missing on purpose. It carried a table
// here until 2026-09-06 — requested -> proceed, completed -> noop — and the
// table was unreachable: no query wrote the status, so nothing ever called it,
// and the transition it described could not have been honored if it had. See
// [ExchangeStatus] for what completing would require and why the framework
// cannot do it.
//
// A dead table is worse than an absent one here, because this file exists to be
// READ AS THE RULE. An entry saying "requested -> proceed" is a promise that
// completing works, made by the file whose whole purpose is to be the answer.
func (s ExchangeStatus) CancelAction() AfterSalesAction {
	switch s {
	case ExchangeRequested:
		return AfterSalesProceed
	case ExchangeCanceled:
		return AfterSalesNoop
	default:
		return AfterSalesConflict
	}
}

// CompleteAction returns the outcome of completing the claim in this status.
//
// Transition table:
//
//	requested -> proceed
//	completed -> noop
//	canceled  -> conflict
func (s ClaimStatus) CompleteAction() AfterSalesAction {
	switch s {
	case ClaimRequested:
		return AfterSalesProceed
	case ClaimCompleted:
		return AfterSalesNoop
	case ClaimCanceled:
		return AfterSalesConflict
	default:
		return AfterSalesConflict
	}
}

// CancelAction returns the outcome of canceling the claim in this status.
//
// Transition table:
//
//	requested -> proceed
//	completed -> conflict  (the claim was met — money refunded or goods
//	                        replaced — and un-meeting it is a new record)
//	canceled  -> noop
func (s ClaimStatus) CancelAction() AfterSalesAction {
	switch s {
	case ClaimRequested:
		return AfterSalesProceed
	case ClaimCanceled:
		return AfterSalesNoop
	case ClaimCompleted:
		return AfterSalesConflict
	default:
		return AfterSalesConflict
	}
}
