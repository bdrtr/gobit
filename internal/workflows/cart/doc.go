// Package cart is the cross-module orchestration of the cart flows (plan Phase 5).
//
// It offers four flows: [Workflows.CreateCart], [Workflows.AddLineItem],
// [Workflows.UpdateLineItem] and [Workflows.CalculateTotals]. All four touch more
// than one module and, per plan Section 2.5, live HERE and not in a module service:
// the shape of the cart is cart's data, the price is pricing's, the currency is
// region's, the discount is promotion's and the tax is tax's; none of them can know
// on its own what a cart holds.
//
// # Access to the modules
//
// This package imports NO package under internal/modules (ADR 0006). Every surface
// it needs is defined here as a NARROW interface ([Carts], [Prices], [Regions],
// [Customers], [Discounts], [Taxes], [Links], [Catalog]) and the concrete service is
// resolved from the container BY NAME (see [FromContainer]). The rule is audited by
// TestWorkflowsDoNotImportModules in internal/arch.
//
// Six of the eight surfaces are MANDATORY; [Discounts] and [Taxes] are optional and
// when they are not registered the calculation runs down a degraded path (see
// "Discount" and "Tax contract").
//
// The signatures of the interfaces use only primitive and stdlib types. The reason
// is Go's structural conformance rule: a consumer that cannot name a module's type
// gets a DIFFERENT type the moment it redefines that type in its own package, and
// then the concrete service no longer satisfies the interface. For the same reason
// structural data (the current shape of the cart and the computed totals) crosses
// the boundary as JSON; see [Carts].
//
// # Why none of the flows is a saga
//
// A saga wins when a flow leaves side effects in MORE THAN ONE module that have to
// be undone: the compensation chain rolls back together the writes that cannot be
// wrapped in a single database transaction (see the internal/core/workflow package
// comment). None of these four flows is like that — they all READ from MANY modules
// but WRITE to only ONE module (cart):
//
//   - CreateCart: reads from region and customer, writes to cart once.
//   - AddLineItem / UpdateLineItem: read from catalog, link and pricing, write to
//     cart.
//   - CalculateTotals: reads from cart, link, pricing, promotion, tax and region,
//     writes to cart.
//
// If the single write fails there is nothing to roll back; the step never happened.
// The only path that does two writes is adding/updating a line (first the line, then
// the totals) and a failure of the second write NEEDS NO COMPENSATION: the state
// that remains is the STALE TOTALS state that the cart model explicitly recognizes,
// and such a cart is already refused from becoming an order (see MarkCompleted in
// the cart module). Rolling the line back would mean DELETING the customer's intent
// because of a transient pricing failure.
//
// This is why the core/workflow Executor is NOT USED in this round and the
// "core.workflow" name is not resolved. Wrapping a single-step job that has no
// compensation into the engine pays the cost of the execution record and the
// compensation machinery but buys no guarantee in return; the only thing the engine
// offers, "undo in reverse order", is the empty set here.
//
// The complete_cart of Phase 6 WILL be a REAL saga: the reservation (inventory), the
// order (order) and the payment (payment) leave side effects in THREE SEPARATE
// modules and when the payment fails the first two must be rolled back. This package
// leaves it the following ground: [Deps] and [FromContainer] are extended just as
// they are, and [Workflows.CalculateTotals] becomes the body of the saga's first
// step — the Compensate of that step may stay empty, because writing totals is
// idempotent and staleness is already a visible state.
//
// # Tax contract
//
// Tax is computed by the tax module ([Taxes.CalculateTaxJSON]); in Phase 5 this job
// temporarily lived in region, and region's godoc had already flagged the handover.
// The three decisions of the contract are UNAFFECTED by the handover:
//
//  1. BASE: tax is computed over the POST-DISCOUNT line subtotal and SHIPPING does
//     not enter the base. Tax follows the amount actually paid; taxing the
//     pre-discount amount would mean taxing money that was never collected from the
//     customer. Shipping is left out because whether shipping is taxed or not varies
//     by jurisdiction; the tax module makes this optional with ShippingInput.Taxable
//     and this flow does NOT turn that option on. Assuming a rule that does not
//     exist, "it is the same as goods", is a silent guess.
//  2. PER LINE: tax is computed separately for each line, and the cart's tax is the
//     SUM of the line taxes. Taxing the cart base in one go would give a result a few
//     minor units different because of rounding; per-line computation was chosen
//     because (a) on an invoice the tax of every line must be explainable one by one,
//     and (b) the tax module may apply DIFFERENT per-line rates by product class and
//     on that day the definition of the base must not change.
//  3. ROUNDING: basis point arithmetic is INTEGER and division rounds DOWN
//     (base x rate / 10000). This is acceptable: the error is smaller than one minor
//     unit per line and is always IN THE CUSTOMER'S FAVOR. Round-half-up was not
//     chosen, because it overcharges the customer and leaves the question "where did
//     the excess come from" to reconciliation; a floating point rate is, per plan
//     Section 8, never even considered.
//
// # Where the tax COUNTRY comes from
//
// The tax module wants a country, while the cart holds a REGION. The country is read
// from the region's record in the Query layer and is used if the region is bound to
// a SINGLE country. The rejected alternative (the cart's shipping address) and why a
// multi-country region counts as "unresolved" are in the
// [Workflows.countryForRegion] godoc.
//
// # The tax SOURCE is visible in the result
//
// Who computed the tax is reported in the [Totals.TaxSource] field and it takes three
// values: [TaxSourceTax], [TaxSourceTaxUnconfigured], [TaxSourceRegion]. If the tax
// surface is not registered, or the country cannot be resolved, the calculation FALLS
// BACK to region's rate (the Phase 5 path) — not to zero. The whole ladder and the
// "why not zero" rationale are in the [Workflows.applyTaxes] godoc. In short: missing
// tax comes silently out of the merchant's own pocket, whereas a missing discount is
// an excess the customer sees; the risks of the two directions are not symmetric.
//
// # Discount
//
// The discount is computed by the promotion module
// ([Discounts.ComputeDiscountsJSON]) and the result arrives PER LINE ITEM: line
// discounts are written to [LineTotals.DiscountTotal] and their sum to the
// [Totals.DiscountTotal] field. The computation is SIDE-EFFECT FREE — the call that
// actually spends the coupon is promotion's RedeemPromotion method and that is the
// order's job; this is why the [Discounts] surface does not know it at all.
//
// If the promotion surface is NOT registered the discount stays zero and the
// storefront keeps working; the rationale is in the [Workflows.applyDiscounts]
// godoc.
//
// # Coupon codes: AUTOMATIC promotions ONLY
//
// There is NO coupon field on the cart and [Workflows.CalculateTotals] takes NO
// coupon code; only AUTOMATIC promotions enter the calculation.
//
// The rejected alternative was to give the codes to CalculateTotals as an optional
// parameter. It was rejected for two reasons:
//
//   - The totals calculation must be reproducible from the cart's OWN state. The
//     flow is called from three places (directly, after adding a line and after
//     updating a line) and if the code were passed to only one of them, the discount
//     WRITTEN to the cart would appear and disappear depending on which entry point
//     was used last. The coupon silently dropping when the customer increases a
//     quantity by one is the unavoidable consequence of that design.
//   - The code is NOT PERSISTENT. Because the cart cannot store it, the order could
//     be created with a total different from the discount seen on the cart; the saga
//     of Phase 6 uses the cart's WRITTEN total.
//
// When a coupon field is added to the cart module the place it will be wired into is
// clear and it is three points: a field carrying the codes is added to the [Snapshot]
// schema, that field is passed into the request's "codes" array inside
// [Workflows.discountRequestFor], and at order time promotion's RedeemPromotion is
// called. In this round the first two points are empty and the third is outside this
// package.
//
// # Customer segment prices
//
// The customer group is NOT put into the price context (beyond "region_id").
// pricing's rule context carries a SINGLE value per attribute; for a customer who is
// a member of more than one group it is ambiguous which group would be written, and
// silently picking one would tie the price to map iteration order. The selection rule
// ("the best price the customer is entitled to") is pricing's decision. The same gap
// exists in the discount context as well and is left unfilled for the same reason
// (see [Workflows.discountRequestFor]).
//
// # Who satisfies the [Carts] surface
//
// This surface is satisfied not by the cart module's SERVICE but by its cross-module
// interop type, and it is registered in the container under the name [ServiceCart].
// The distinction is mandatory: the signatures of the service use cart's own models
// and service types, and this package cannot name those types (ADR 0006) —
// structural conformance can only be established through a surface with primitive
// signatures. The same pattern exists in the region, pricing, customer, promotion and
// tax modules too.
//
// The conformance is NOT CHECKED by the compiler; a wrongly registered type gives a
// typed mismatch error on the [FromContainer] call and the error writes which method
// is missing (ADR 0001, ADR 0002). The conformance of the field names, on the other
// hand, can only be proven by an integration test (see internal/e2e).
package cart
