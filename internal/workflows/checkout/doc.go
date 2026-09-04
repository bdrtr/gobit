// Package checkout is the complete_cart saga that turns a cart into an order
// (plan Phase 6).
//
// It offers a single workflow: [Workflows.CompleteCart]. The workflow consists
// of five steps and is executed by core/workflow's saga engine:
//
//	reserve_inventory -> create_order -> authorize_payment -> capture_payment -> clear_cart
//
// If a step blows up, the engine calls the Compensate of the steps that had
// SUCCEEDED up to that moment in REVERSE ORDER: the payment is canceled, the
// order is canceled, the reservations are released. That is Phase 6's DoD.
//
// # Why this is a REAL saga
//
// Phase 5's cart workflows READ from many modules but WROTE to a single one,
// and were therefore not sagas (see the internal/workflows/cart package
// comment). This workflow is structurally different from them: it leaves behind
// side effects that must be undone in ALL THREE of the inventory, order and
// payment modules. Because the three modules own separate tables (separate
// services, later on), they cannot be wrapped in a single database transaction;
// what stands in for the distributed transaction is the compensation chain.
//
// # Reaching the modules
//
// This package imports NO package under internal/modules (ADR 0006). Every
// surface it needs is defined here as a NARROW interface ([Carts],
// [Inventory], [Fulfillment], [Orders], [Payments], [Links], [Catalog]) and the
// concrete service is resolved from the container BY NAME (see
// [FromContainer]). The rule is enforced by TestWorkflowsDoNotImportModules in
// internal/arch.
//
// The interface signatures use only primitive and stdlib types; the reason is
// Go's structural conformance rule (ADR 0001). Composite data (the cart's
// momentary shape, the order's view) crosses the boundary as JSON.
//
// The interfaces are DELIBERATELY narrow: methods that exist on the module
// surface but that this workflow does not use (inventory.AvailableQuantity,
// payment.Refund, order.CompleteOrder, fulfillment.CreateFulfillment …) are NOT
// written here. The rationales live in the godoc of the interfaces concerned.
//
// # The internal/workflows/cart dependency
//
// This package DOES import internal/workflows/cart, and that falls outside
// ADR 0006's prohibition: the prohibition is about internal/modules, not about
// a sibling orchestration package. The dependency is unavoidable and it has two
// reasons:
//
//  1. The order needs PER-LINE amounts (unit price, subtotal, tax) and the cart
//     module's primitive surface ("cart.interop") publishes only the id, the
//     variant and the quantity of the lines — it does not publish the amounts.
//     The ONLY place that produces line amounts is the calculate_totals
//     workflow; the order module's interop document also hands this joining
//     explicitly to the workflow.
//  2. The cart module's MarkCompleted rejects a cart with STALE totals
//     (totals_revision ≠ revision). If the calculation is not refreshed at the
//     start of checkout, the saga's LAST step would fall over after the money
//     had already been taken.
//
// # The calculation happens BEFORE the saga
//
// [Workflows.CompleteCart] first does the preparation (calculation, snapshot,
// title, inventory item resolution), then starts the saga. That the preparation
// is NOT a step is deliberate: none of it leaves behind a side effect to be
// undone (writing totals is idempotent and staleness is a visible state
// anyway), whereas being a saga step would load each of them with a pointless
// compensation and execution-record cost. Besides, an error found during the
// preparation (a variant without a price, a product without an inventory item)
// returns without ANY side effect having been applied.
//
// # Step-by-step decisions
//
// reserve_inventory — for every line of the cart the location is determined
// first, then the stock is reserved; its compensation is ReleaseReservation and
// it is IDEMPOTENT. The step is composite in itself: if one line blows up it
// releases the reservations taken up to that moment ITSELF, because the engine
// does not compensate a step that blew up on its single attempt (see the
// core/workflow package comment). If its own cleanup blows up too, the error is
// wrapped with [workflow.ErrUncompensated] and the execution is written
// compensation_failed. For how the location is determined see "Location".
//
// create_order — the order is opened from the view built during the
// preparation; its compensation is CancelOrder and it is IDEMPOTENT. The
// execution id is placed on the view as the idempotency key, so that a second
// call within the same execution does not open a new order.
//
// authorize_payment — the collection is opened, the session is opened and
// authorized; its compensation is Cancel and it releases the hold.
//
// capture_payment — the capture is performed and the amount is VERIFIED against
// Collection. It has NO compensation (see "The point of no return"). The call
// returning an error does not mean "the money did not move"; the error path is
// investigated (see "Ambiguous capture").
//
// clear_cart — the cart is marked completed and the reservations are confirmed.
// It has no compensation; it is both the last step and ConfirmReservation
// cannot be undone.
//
// # Location: the stock FACT and the fulfillment DECISION live in separate places
//
// [CompleteCartInput.LocationID] is OPTIONAL. If it is set the workflow makes
// no choice at all and every line of the cart is reserved from that location —
// a declared location is not a preference but an instruction. If it is empty
// the location is determined PER LINE and the question splits in two:
//
//  1. "In which warehouses can this quantity of this item be reserved" is a
//     STOCK FACT; [Inventory.LocationsWithStock] gives the answer and it
//     carries no order of preference.
//  2. "In which order should these candidates be tried" is a FULFILLMENT
//     DECISION; [Fulfillment.RankLocations] gives the answer. The fulfillment
//     module drops the warehouses that do not serve the destination region and
//     lines the rest up in the operator's priority order; the only context
//     passing through here is the order's region. The order is asked ONCE per
//     line, not after every exhausted candidate.
//
// Gathering the two into a single module would have made the stock query depend
// on fulfillment policy, or fulfillment policy depend on the stock schema.
// Having this package make the choice would have been possible without breaking
// ADR 0006, but it would still have been wrong: a cart workflow has nothing to
// say about warehouse policy.
//
// The consequence is this: the lines of one order may be reserved from
// DIFFERENT warehouses. Compensation is not affected by that, because
// reservations are released PER ID and which warehouse they were taken from
// does not change the release. A line for which no location holds enough stock
// is reported through the SAME path as the case where the reservation blows up
// (errors.Conflict, [CodeReservationFailed]) and the reservations taken up to
// that moment are released by the step's OWN cleanup — in a multi-warehouse
// cart this is a situation that arises more easily than in a single-warehouse
// one.
//
// # THE FULL PAYMENT RULE
//
// If the amount the authorization HELD does not cover the collection's amount
// (authorized < amount), authorize_payment counts as FAILED and the saga is
// rolled back. The rule is a single line and it looks at the NUMBER
// [Payments.Authorize] returns, not at the provider's status string: when the
// provider authorizes PARTIALLY the status is still "authorized", and a saga
// that looks only at the status would confirm an unpaid order. The same measure
// is applied after the capture as well: the collection is re-read and
// captured >= amount is verified.
//
// # The point of no return (the pivot)
//
// capture_payment is the saga's PIVOT step: once the money has been taken there
// is NO automatic way back. A refund is not the compensation of the capture but
// a SEPARATE workflow (plan Phase 7+) and it touches the customer, the order
// and accounting separately; hiding it silently inside a compensation step
// would mean that where the saga says "rolled back" it actually creates a
// movement of money.
//
// This has three consequences and all three are deliberate:
//
//   - capture_payment's Compensate returns an error if the capture ACTUALLY
//     HAPPENED (errors.Conflict). The engine writes the execution
//     compensation_failed and that is a MANUAL INTERVENTION signal. Returning
//     nil would mean "rolled back" and that record would be a lie.
//   - The compensations of the steps BEFORE the capture DO NOT RUN if the
//     capture happened: the order is not canceled, the stock is not released,
//     the hold is not released (see Workflows.skipAfterCapture). Rolling back an
//     order whose money has been taken would cost the customer both their money
//     and their order; releasing the stock would mean selling the goods of a
//     standing order a second time. Canceling the hold is meaningless anyway:
//     the capture closes the hold (see CapturePayment in the payment module).
//     Every skip is logged as ERROR and the execution is still written
//     compensation_failed.
//   - The pivot guard looks not at the SUCCESS of the capture but at whether it
//     was ATTEMPTED (see Workflows.skipAfterCapture). A guard that looked at
//     success would switch itself off exactly where the guard is needed most —
//     see "Ambiguous capture".
//   - Because clear_cart runs AFTER the pivot it DOES NOT RETURN module
//     failures as errors.
//     If the cart stamp or the reservation confirmation falls over, the event is
//     logged as ERROR, written into the [CompleteCartResult.Warnings] field and
//     the workflow ends SUCCESSFULLY. The alternative was to cancel an order
//     that has been paid for and release the stock. The remaining inconsistency
//     (a cart left open, a reservation left "active") is visible and can be
//     repaired by hand; it is not an unrefunded capture.
//
// # Ambiguous capture: an error DOES NOT MEAN "the money did not move"
//
// The classic distributed-transaction problem lives here: the provider takes
// the money and the response is lost (a network timeout). Capture returns an
// error, no capture id is left behind, and a pivot guard that looked at the ID
// would switch itself off — the saga would cancel the order, release the stock,
// and the customer would lose both their money and their order.
//
// That is why the error path is INVESTIGATED: the collection is re-read and the
// saga is rolled back only when the collection SEES no capture at all.
// Otherwise (if the read blew up too, or if a capture is visible) NO rollback is
// performed; the error carries [workflow.ErrUncompensated], the execution is
// written compensation_failed and the correction is made by hand.
//
// # REMAINING RISK: the collection is the LOCAL ledger, not the provider itself
//
// The investigation above NARROWS the risk, it does not ELIMINATE it, and that
// limit has to be written down here explicitly.
//
// The payment module makes the provider call INSIDE its OWN database
// transaction (internal/modules/payment/service/capture.go). If a write or the
// commit of the module blows up AFTER the provider has taken the money, the
// transaction is rolled back: the money is gone but there is NO trace of it in
// the collection. The saga reads that collection, says "no capture visible" and
// performs a FULL ROLLBACK — that is, exactly the failure this section tries to
// prevent is still possible one layer below.
//
// There are TWO places where the response can be lost and the investigation
// closes only the first:
//
//	provider  <--(1)-->  payment module  <--(2)-->  the payment module's commit
//
// (1) is closed: a capture the module HAS RECORDED is seen by the saga.
// (2) is OPEN: the module cannot report to the saga a capture it could not
// record.
//
// The only correct way to close (2) is to ASK the provider — that is,
// reconciliation: a periodic comparison against the provider's own ledger.
//
// THAT IS NOW BUILT: internal/jobs/paymentrecon runs hourly, lists the sessions
// this installation still holds as authorized, asks each provider what it did
// with them, and reports every disagreement with both sides and the external
// id (ADR 0020). It does NOT close the hole — the money is still gone and the
// saga has still rolled the order back — it makes the hole VISIBLE within the
// hour instead of at the next audit. And it closes only this half: a session
// whose row was never committed at all leaves nothing to list, so it is still
// caught operationally, from the provider's dashboard.
//
// A secondary improvement is to move the provider call OUTSIDE the module's
// transaction and to write it in two phases with a "capturing" intermediate
// state; that narrows the window but still does not close it (this time the
// transaction that writes the intermediate state can blow up).
//
// The options were weighed and the decision is deliberate:
//
//   - Counting every error as "not captured" (the old behavior) is the CHEAPEST
//     code but produces the most expensive failure: the destruction of an order
//     that has been paid for. Rejected.
//   - QUERYING the session/collection on the error path adds one network call
//     and is paid only on the error path; the happy path does not change.
//     CHOSEN.
//   - Carrying the workflow FORWARD when the capture is proven (counting the
//     capture as successful and closing the cart) looks tempting, but we do not
//     have a capture id: saying "successful" without a trace to write to the
//     order and to accounting would mean presenting an unverifiable payment as
//     verified. Reconciling a lost response is separate work, and it landed as
//     a REPORT rather than a workflow: see internal/jobs/paymentrecon and ADR
//     0020 for why nothing about it writes.
//
// The decision is asymmetric because the costs are asymmetric: the cost of
// rolling back by mistake is a refund, accounting work and customer contact;
// the cost of NOT rolling back by mistake is a pending order, reserved stock and
// a hold left on the card — all of them visible and repairable.
//
// # The saga detaches from the caller's CANCELLATION
//
// The preparation runs with the caller's context; the saga, though, detaches
// from it and binds itself to its own time budget ([SagaTimeout]). The reason
// is once again in the pivot: the engine checks the context before every step,
// and a cancellation arriving during the capture would skip clear_cart entirely
// — the money has been taken, the order stays "pending", the cart stays locked,
// the stock stays "active", and because the idempotency key is burned that cart
// could never be tried again. Work left half done is more expensive than the
// cost of finishing it.
//
// # Idempotency
//
// The execution is bound to a key derived from the cart id
// ([IdempotencyKeyPrefix] + cart_id). A second call made for the same cart DOES
// NOT RE-RUN the steps: if one is in flight or has failed it returns
// errors.Conflict (see [workflow.Executor]).
//
// The engine's "return the output of the completed execution" path (replay) is
// in practice UNREACHABLE in this workflow: the preparation runs BEFORE the
// engine's check and its first act is to refresh the calculation, whereas a
// successful execution stamps the cart completed and no calculation can be made
// on a completed cart. In a real setup the answer to a second call is therefore
// not "the same result" but [CodeCartCompleted]; the replay path can only be
// seen in the case where the cart could not be stamped (where clear_cart left a
// warning). It is a deviation in the harmless direction — both guards prevent
// the same thing: no second order is born from the same cart.
//
// That the SAME cart cannot be retried after a failed attempt is the accepted
// cost: the engine defines the key as "the result of one attempt", not as a
// right to endless repetition. Starting a fresh attempt after a declined
// payment (producing a new key) is not this workflow's decision but that of the
// endpoint calling it, and it belongs to plan Phase 7+.
//
// The protection is not single-layered either: a successful execution stamps
// the cart completed, no calculation can be made on a completed cart, and
// MarkCompleted does not succeed a second time. So even if the key were lost,
// no second order would be born from the same cart.
//
// # This package DOES NOT publish the order.placed event
//
// The order module publishes the event ITSELF: after its service's CreateOrder
// method writes the order it puts the "order.placed" event on the bus (see
// events.go in the order module, EventOrderPlaced). Publishing it here as well
// would produce a DUPLICATE EVENT and the subscribers (notification,
// accounting, search index) would process the same order twice. That is why
// this package never resolves the name "core.eventbus" and publishes no event
// at all.
//
// For the same reason the compensation publishes no event either: canceling the
// order is the order module's own decision and announcing it belongs there too.
//
// That the order module REALLY does publish the event can only be proven by an
// integration test running with the real modules — because this package cannot
// import that module, neither the compiler nor a unit test can see that link
// (ADR 0006's accepted cost).
//
// # Retry policy
//
// The steps are NOT RETRIED (the engine's default). The reason is the
// idempotency of the steps: if inventory.Reserve is called twice it produces TWO
// reservations, and repeating payment.Capture is, against a real payment
// provider, another attempt at moving money. If the engine repeats a step on its
// own decision it compensates it on a best-effort basis, but best effort is not
// enough here.
//
// COMPENSATION, on the other hand, is retried (see
// [workflow.WithCompensationRetry]): the cost of a failed Invoke is the rollback
// of the execution, the cost of a failed Compensate is manual intervention.
// Insisting on the compensation through a transient failure therefore pays off.
package checkout
