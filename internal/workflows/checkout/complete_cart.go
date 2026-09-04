package checkout

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
)

// Workflow and step names. The names are written into the execution record and
// are the only thing an operator sees on an execution that needs manual
// intervention; changing them makes past records harder to read.
const (
	// WorkflowName is the saga's name in the engine.
	WorkflowName = "complete_cart"
	// StepReserveInventory is the name of the stock reservation step.
	StepReserveInventory = "reserve_inventory"
	// StepCreateOrder is the name of the order opening step.
	StepCreateOrder = "create_order"
	// StepAuthorizePayment is the name of the payment authorization step.
	StepAuthorizePayment = "authorize_payment"
	// StepCapturePayment is the name of the capture step.
	StepCapturePayment = "capture_payment"
	// StepClearCart is the name of the cart closing step.
	StepClearCart = "clear_cart"
)

// IdempotencyKeyPrefix is the part of the execution key that comes before the
// cart id.
//
// Since the key is unique by the (workflow name, key) pair, the prefix is not
// technically necessary; it exists for readability: an operator looking at a
// row in pgstore sees what the key names without consulting another table.
const IdempotencyKeyPrefix = "complete_cart:"

// CompensationTimeout is the time budget given to a single compensation call.
//
// The budget is PER STEP (see [workflow.WithCompensationTimeout]) and holds the
// same value as the engine's default; stating it EXPLICITLY here is meant to
// make visible that compensation performs real network calls (a cancellation at
// the payment provider, releasing a reservation in the database) and that this
// duration is a decision.
const CompensationTimeout = 30 * time.Second

// SagaTimeout is the time budget given to the WHOLE saga.
//
// The budget is generous because the chain spans three modules and a payment
// provider; it is nevertheless FINITE: a flow detached from the caller's
// cancellation (see [sagaContext]) cannot hold the right to run indefinitely,
// otherwise a hung external call would hold a goroutine and reserved stock
// forever.
const SagaTimeout = 2 * time.Minute

// ExecutionLease is the longest a complete_cart execution can LEGITIMATELY
// take; a record that stays "running" longer than that has been abandoned
// (see [workflow.WithLease]).
//
// # Why it is needed
//
// The record opens as "running" and closes by moving to a terminal state. If
// the process dies before it can write that transition — a deploy, an OOM, a
// pod eviction — the record stays running forever and that cart can never be
// paid again. Measured: an execution that had crashed three days earlier still
// said "still running".
//
// This is the direct consequence of the difference between the shutdown budget
// and the saga budget: SHUTDOWN_TIMEOUT defaults to 15 seconds, [SagaTimeout]
// is two minutes. So an ordinary deploy can cut an in-flight payment in half.
//
// # Why it is this generous
//
// The theoretical upper bound is [SagaTimeout] + step count x
// [CompensationTimeout], that is 2m + 5x30s = 4.5 minutes. The lease was chosen
// at more than twice that, because the two mistakes do NOT cost the SAME:
// deciding late makes the customer wait, deciding early releases the key of a
// saga that is STILL RUNNING and starts a second saga for the same cart — that
// is, the stock gets reserved twice.
const ExecutionLease = 10 * time.Minute

// CompleteCartInput is the input of the order completion request.
type CompleteCartInput struct {
	// CartID is the cart to be completed; it is REQUIRED.
	CartID string
	// LocationID is the stock location the inventory will be reserved from; it
	// is OPTIONAL.
	//
	// # IF SET: every line is reserved from that location
	//
	// The flow makes no choice and asks no module. The path is kept because a
	// declared location is not a preference but an INSTRUCTION: an
	// administrative order that must leave a particular warehouse, or a
	// single-warehouse installation, wants the choice not to be made at all.
	//
	// # IF EMPTY: the location is chosen PER LINE
	//
	// For each line the inventory module is first asked for the locations that
	// can reserve enough of that item ([Inventory.LocationsWithStock]), then the
	// fulfillment module puts the candidates in PREFERENCE ORDER
	// ([Fulfillment.RankLocations]) and the reservation is opened at the first
	// warehouse in that order that works. The lines of one order can therefore
	// be reserved from DIFFERENT warehouses; [planLine], which carries their ids
	// per line, already made that possible.
	//
	// The division of labor answers, one by one, the two reasons the old godoc
	// of this field listed: (1) which warehouse to ship from is a FULFILLMENT
	// decision and this package does not make it, it ASKS the fulfillment
	// module; (2) the inventory module's surface LISTS locations, so we are not
	// forced to pick "the first location" by looking at an ordering accident —
	// the list that comes back is a fact, not a preference order.
	//
	// How the fulfillment module builds its preference (eliminating warehouses
	// that do not serve the target region, the operator's priority order) is not
	// this package's knowledge; the only context passing through here is the
	// order's region.
	//
	// If no location has enough stock the flow reports it the SAME way as the
	// case where the reservation blows up (errors.Conflict,
	// [CodeReservationFailed]): what the caller sees is again "the order cannot
	// be placed", and the reservations taken until then are released.
	LocationID string
	// PaymentProviderID is the provider the payment will be opened at; it is
	// REQUIRED.
	//
	// There is NO default: which provider the money is captured from is the
	// customer's choice, and a default would silently pull the money from
	// another provider in a miswired installation.
	PaymentProviderID string
	// PaymentData is the free-form JSON object passed to the provider as is; it
	// is optional (card token, return address, test behavior keys).
	//
	// It is NOT written into the execution record; for the rationale see
	// [checkoutPlan].
	PaymentData json.RawMessage
	// Email is the order's contact address; it is optional.
	//
	// The cart's own email cannot be used here: the cart module's cross-module
	// surface does not publish it. On a guest order the email is the only way to
	// follow the order, so it must be asked for at the checkout step and given
	// here.
	Email string
	// ExpectedTotal is the total the caller had the customer CONFIRM (minor
	// unit); it is optional and zero means "do not check".
	//
	// If given, it is compared with the computed amount and a difference
	// produces errors.Conflict. The check is necessary because the totals are
	// RECOMPUTED at the start of checkout: a price change in the catalog could
	// otherwise lead to an amount different from the one the customer saw being
	// charged silently.
	ExpectedTotal int64
}

// CompleteCartResult holds the fields of the completed order that concern the
// caller.
//
// The type is the output of the saga's LAST step and is written into the
// execution record as JSON; a second call with the same key reads this body
// back from the record and returns it. Changing the field names means old
// records can no longer be read.
type CompleteCartResult struct {
	// CartID is the cart the order was born from.
	CartID string `json:"cart_id"`
	// OrderID is the id of the created order.
	OrderID string `json:"order_id"`
	// PaymentCollectionID is the id of the payment collection.
	PaymentCollectionID string `json:"payment_collection_id"`
	// PaymentSessionID is the id of the payment session.
	PaymentSessionID string `json:"payment_session_id"`
	// PaymentID is the id of the capture.
	PaymentID string `json:"payment_id"`
	// CurrencyCode is the currency that was captured.
	CurrencyCode string `json:"currency_code"`
	// Amount is the captured amount (minor unit).
	Amount int64 `json:"amount"`
	// ReservationIDs are the reservations allocated to the order.
	ReservationIDs []string `json:"reservation_ids"`
	// PaymentTotalsRecorded reports whether what was collected was written onto
	// the order.
	//
	// FALSE does not mean the money did not move — it means the order does not
	// SAY it moved, and will read as unpaid until a human repairs it. See the
	// Warnings field for what went wrong.
	PaymentTotalsRecorded bool `json:"payment_totals_recorded"`
	// CartCompleted reports whether the cart was stamped completed.
	CartCompleted bool `json:"cart_completed"`
	// ReservationsConfirmed reports whether the reservations were
	// confirmed.
	ReservationsConfirmed bool `json:"reservations_confirmed"`
	// Warnings are the faults that do NOT DROP the order but ask for a manual
	// repair.
	//
	// Only the step after the pivot (clear_cart) fills it: once the money has
	// been taken, failing to stamp the cart or to confirm the reservation is not
	// a reason to roll the flow back (see the package comment). If the field is
	// set the order is VALID, but a human must look at it.
	Warnings []string `json:"warnings,omitempty"`
}

// CompleteCart turns the cart into an order.
//
// The flow first does the preparation (the totals are recomputed, the snapshot
// is read, the headers and the stock items are resolved), then runs the
// five-step saga: reserve_inventory -> create_order -> authorize_payment ->
// capture_payment -> clear_cart. If a step blows up, the steps that succeeded
// until then are compensated in REVERSE ORDER; the details and the pivot rule
// are in the package comment.
//
// The execution is bound to an idempotency key derived from the cart id: a
// second call for the same cart does NOT RUN the steps again. If one is still
// running, or has been compensated, it returns errors.Conflict.
//
// # A second call stops at the preparation in a REAL installation
//
// The engine's "return the output of the completed execution" path (replay) is
// practically UNREACHABLE in this flow and the godoc does not hide it: the
// preparation runs BEFORE the engine's idempotency check and its first act is
// to recompute the totals, whereas a successful execution stamps the cart
// completed. With the real cart module the answer to a second call is therefore
// not "the same result" but [CodeCartCompleted]. The replay path can only be
// seen in the case where the cart could not be stamped (where clear_cart left a
// warning). Moving the check ahead of the preparation is possible, but it means
// binding the engine's key before the totals are recomputed, and that is a
// larger design decision (plan Phase 7+).
//
// # The saga is detached from the caller's CANCELLATION
//
// The preparation runs with the caller's context — it leaves no side effect, so
// there is no point in going on working for a client that has given up. The
// saga, on the other hand, is detached with [sagaContext]: once the first
// reservation has been taken, the client dropping does NOT STOP the flow. The
// reason lies in the pivot — the engine checks the context before every step,
// and a cancellation arriving during the capture would skip clear_cart
// entirely: the money would be taken, the order open, the cart locked and the
// stock left "active". Because the idempotency key would be burned as well,
// that cart could never be tried again. Work left half done is more expensive
// than the cost of finishing it.
//
// The class of the returned error is preserved so that the caller can branch on
// it: insufficient stock and a changed cart are errors.Conflict, a variant
// without a price or without stock is errors.Invalid, a cart that cannot be
// found is errors.NotFound. If the compensation itself blows up the error is
// errors.Internal and it requires MANUAL INTERVENTION.
//
// # Steps are NOT RETRIED
//
// The engine's default is kept (see [workflow.NoRetry]): if inventory.Reserve
// is called twice it produces two reservations, and repeating payment.Capture
// is, at a real provider, a second attempt to move money. COMPENSATION, by
// contrast, is retried; the price of a failed compensation is manual
// intervention, and insisting through a transient fault pays off.
func (w *Workflows) CompleteCart(ctx context.Context, in CompleteCartInput) (CompleteCartResult, error) {
	if err := in.normalize(); err != nil {
		return CompleteCartResult{}, err
	}

	plan, err := w.prepare(ctx, in)
	if err != nil {
		return CompleteCartResult{}, err
	}

	wf := workflow.Workflow{Name: WorkflowName, Steps: w.sagaSteps(plan)}

	sctx, cancel := sagaContext(ctx)
	defer cancel()

	out, err := workflow.RunInto[CompleteCartResult](sctx, w.executor, wf, plan,
		append([]workflow.RunOption{
			workflow.WithIdempotencyKey(IdempotencyKeyPrefix + plan.CartID),
		}, RecoveryOptions()...)...,
	)
	if err != nil {
		return CompleteCartResult{}, err
	}

	w.log.InfoContext(ctx, "cart turned into an order",
		"cart_id", out.CartID, "order_id", out.OrderID, "payment_id", out.PaymentID,
		"amount", out.Amount, "currency_code", out.CurrencyCode,
		"warnings", len(out.Warnings))
	return out, nil
}

// sagaSteps builds the saga's step sequence.
//
// It is the SINGLE source and that is deliberate: writing the same sequence a
// second time would open the door for the recovery path
// ([Workflows.RecoveryWorkflow]) to drift silently from the live path. In
// recovery the engine compares step NAMES against the ones in the record, so a
// drift turns not into a fault but into a REFUSAL: a half-finished saga can
// never be recovered (see workflow.Recoverer).
func (w *Workflows) sagaSteps(plan *checkoutPlan) []workflow.Step {
	return []workflow.Step{
		&reserveInventoryStep{w: w, plan: plan},
		&createOrderStep{w: w, plan: plan},
		&authorizePaymentStep{w: w, plan: plan},
		&capturePaymentStep{w: w, plan: plan},
		&clearCartStep{w: w, plan: plan},
	}
}

// RecoveryOptions returns the saga's lease and compensation policy.
//
// It is the SINGLE source: both the live path ([Workflows.CompleteCart]) and
// the recovery path (workflow.Recoverer) use the same list. Their drifting
// apart would produce a silent class of bug — the compensation an operator runs
// by hand would work with a DIFFERENT budget than the one the engine runs
// itself, and the same provider call would time out on one path and not on the
// other.
//
// The lease ([ExecutionLease]) has to be here: recovery refuses a call without
// a lease, because without one a still-running saga cannot be told apart from
// an abandoned record.
func RecoveryOptions() []workflow.RunOption {
	return []workflow.RunOption{
		workflow.WithLease(ExecutionLease),
		workflow.WithCompensationRetry(compensationRetry()),
		workflow.WithCompensationTimeout(CompensationTimeout),
	}
}

// RecoveryWorkflow rebuilds the definition of a half-finished execution FROM
// THE RECORD'S INPUT.
//
// Recovery wants a workflow definition that carries compensation functions
// (workflow.Recoverer), but when the process died the plan that built that
// definition went with it. The plan sits in the engine's record as JSON; here
// it is decoded back and the same steps are built in the same order.
//
// # The payment data does not come back, and it must not
//
// checkoutPlan.PaymentData is NOT written into the record (`json:"-"`), so it
// is empty in the rebuilt plan. Compensation does not use it — it is the input
// of the authorization, not of the rollback — and keeping it out of the record
// is a security decision: payment details must not sit in the execution record.
//
// # An empty plan is REFUSED
//
// Decoding the JSON is not enough; `{}` decodes too. A chain built from a plan
// without a cart id would call its compensations without an id and would
// produce a recovery that says "I left nothing behind".
func (w *Workflows) RecoveryWorkflow(input json.RawMessage) (workflow.Workflow, error) {
	var plan checkoutPlan
	if err := json.Unmarshal(input, &plan); err != nil {
		return workflow.Workflow{}, errors.Wrap(err, errors.KindInvalid, CodeInvalidInput,
			"the plan in the execution record could not be decoded")
	}
	if plan.CartID == "" {
		return workflow.Workflow{}, errors.Invalid(CodeInvalidInput,
			"the plan in the execution record has no cart id; the compensation chain cannot be built")
	}

	return workflow.Workflow{Name: WorkflowName, Steps: w.sagaSteps(&plan)}, nil
}

// sagaContext detaches the saga from the caller's CANCELLATION and binds it to
// its own time budget.
//
// The engine checks the context BEFORE every step and does not start a new step
// on a dead context; that is the right behavior in a flow that leaves no side
// effect, but here there is a PIVOT. The capture is the flow's slowest external
// call, and a cancellation arriving exactly then (the client dropped, the
// gateway timed out) would skip the last step — the stamping of the cart and
// the confirmation of the reservations — entirely. The result is close to the
// state the package calls "must never happen": the money taken, the order
// "pending", the cart locked, the stock "active", and because the execution is
// compensation_failed the same cart can never be tried again.
//
// The context's VALUES are preserved (context.WithoutCancel): trace ids and
// logger fields must stay visible through the rest of the flow. The budget is
// [SagaTimeout], not the caller's budget, because the very purpose of the
// detachment is to withstand that one running out.
func sagaContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), SagaTimeout)
}

// compensationRetry is the retry policy of the compensation chain.
//
// The exponential backoff is kept short: compensation SHARES the per-step
// [CompensationTimeout] budget with its attempts, and long waits would spend
// that budget on waiting rather than on work. Permanent errors
// (errors.Conflict, errors.Invalid) are not retried by the engine anyway, so
// the policy only applies to transient faults.
func compensationRetry() workflow.RetryPolicy {
	return workflow.RetryPolicy{
		MaxAttempts: 3,
		Backoff:     200 * time.Millisecond,
		Multiplier:  2,
		MaxBackoff:  2 * time.Second,
	}
}

// normalize validates the input and trims whitespace.
//
// Trimming is done only on the email; ids are not trimmed, they are rejected
// (see [requireID]).
func (in *CompleteCartInput) normalize() error {
	if err := requireID("cart_id", in.CartID, MaxCartIDLen); err != nil {
		return err
	}
	// The location is OPTIONAL (see [CompleteCartInput.LocationID]) and an empty
	// value means "you pick"; when it is set the check applies unchanged.
	// Accepting an id with whitespace or an excessively long one would blow the
	// error up one module away from its cause — in the inventory module's input
	// validation.
	if in.LocationID != "" {
		if err := requireID("location_id", in.LocationID, maxIDLen); err != nil {
			return err
		}
	}
	if err := requireID("payment_provider_id", in.PaymentProviderID, maxIDLen); err != nil {
		return err
	}
	if in.ExpectedTotal < 0 {
		return errors.Invalid(CodeInvalidInput,
			"expected_total cannot be negative: %d", in.ExpectedTotal)
	}
	if in.ExpectedTotal > MaxTotal {
		return errors.Invalid(CodeInvalidInput,
			"expected_total can be at most %d: %d", MaxTotal, in.ExpectedTotal)
	}

	in.Email = strings.TrimSpace(in.Email)
	if len(in.Email) > maxIDLen {
		return errors.Invalid(CodeInvalidInput,
			"email can be at most %d bytes: %d", maxIDLen, len(in.Email))
	}
	return nil
}
