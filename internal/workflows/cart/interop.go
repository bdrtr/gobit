package cart

import (
	"context"
	"encoding/json"
	"time"
)

// InteropName is the name of the cart workflows in the container (ADR 0001/0006).
//
// This package declares the name but the COMPOSITION ROOT performs the
// registration (internal/app): the workflows resolve their own dependencies from the
// container as well and can only be built AFTER every module has Registered — had
// they been built inside some module's Register they would have been looking for
// services that do not exist yet.
//
// The consumer is the cart MODULE: it owns the storefront's cart-opening and line
// item endpoints and resolves the flow under this name (see CartFlowsName in the
// cart module). Because the module cannot import this package, the name is
// repeated there as a STRING; the price of that repetition is the accepted price
// of isolation, and a typo does not stay silent — if the name does not resolve,
// the cart-opening and line-item endpoints fail CLOSED.
const InteropName = "workflows.cart.interop"

// Interop turns the cart workflows into a PRIMITIVE cross-module surface.
//
// # Why a separate type
//
// [Workflows]'s signatures use this package's own types ([AddLineItemInput],
// [Totals] …) and no module can NAME those types: modules do not import
// internal/workflows (ADR 0006, in both directions). For a narrow interface
// declared on the consumer side to be satisfied structurally, the signatures must
// consist of PRIMITIVE and stdlib types only; this type performs exactly that
// translation and nothing else. It is the same pattern as the modules' interop.go
// files.
//
// # Why not ALL of the workflows
//
// The surface carries the workflows that ARE another module's HTTP endpoints —
// the storefront's cart writes, and the promotion trial the promotion module's
// admin endpoint asks for (ADR 0176) — and no others. [Workflows.CalculateTotals]
// is here ONLY inside [Interop.RepriceAfter], the step a write takes after itself
// (ADR 0173); it is still not a capability a client can ask for, because running
// the computation at the moment the client asks would tie the amount to the
// client's timing rather than to a change.
//
// The rule cuts both ways and the second direction went wrong for a while. The
// cart module's two coupon endpoints resolve the cart API's CartPromotions from this
// surface, and the surface carried neither method — so `POST
// /store/v1/carts/{id}/promotions` and its DELETE sibling failed at resolution
// with `container_type_mismatch`, on the first request, cached by a sync.Once for
// the life of the process. Startup was green, the route was described, and the
// module's fail-closed nil guard passed because the WRAPPER was present; what was
// absent was one layer deeper (gap D73).
//
// The count used to be written into this paragraph as "the three workflows". It is
// not written as a number any more: a number here is a second place to update, and
// it was already wrong by one before the coupon methods were missing.
//
// [Workflows.CreateCart] stayed OUT for a while on that same ground: the
// cart-opening endpoint was wired to the cart module's own service. That wiring
// meant the endpoint took the region FROM THE CLIENT and skipped the workflow's
// derivation of the region from the country; the surface's third method
// ([Interop.OpenCartForCountry]) was added precisely to close that skip.
type Interop struct {
	w *Workflows
}

// NewInterop builds the cross-module surface for the given workflows.
func NewInterop(w *Workflows) *Interop { return &Interop{w: w} }

// OpenCartForCountry resolves the region from the country code, opens the cart and
// returns the cart's ID.
//
// The CALLER DOES NOT SUPPLY the region and the currency: both are derived from
// countryCode by this workflow (see [Workflows.CreateCart]). The surface having no
// region parameter is deliberate and is the reason this method exists — had there
// been a parameter, nothing would have stood in the way of the caller filling it
// in, whereas what the customer expresses is a COUNTRY and the region is its
// counterpart on the server. The same gap is the absence of the price parameter in
// [Interop.AddPricedLineItem].
//
// If customerID is left empty the cart belongs to a GUEST. addsToOrderID, when
// given, opens the cart to add to that order (ADR 0192). metadata is the free
// JSON object to attach to the cart; it may be left empty.
//
// Only the ID is returned: the cart itself is a record richer than this surface
// can carry, and the caller can already read it from its own service. The same
// choice is made in [Interop.AddPricedLineItem].
func (i *Interop) OpenCartForCountry(
	ctx context.Context,
	countryCode, customerID, email, addsToOrderID string,
	metadata json.RawMessage,
) (string, error) {
	result, err := i.w.CreateCart(ctx, CreateCartInput{
		CountryCode:   countryCode,
		CustomerID:    customerID,
		Email:         email,
		AddsToOrderID: addsToOrderID,
		Metadata:      metadata,
	})
	if err != nil {
		return "", err
	}
	return result.CartID, nil
}

// AddPricedLineItem adds a line to the cart and returns the line's ID.
//
// The CALLER DOES NOT SUPPLY the unit price: the price is determined by this
// workflow from the variant's price set and the cart's currency (see
// [Workflows.AddLineItem]). The surface having no price parameter is deliberate
// and is the core rationale of this change — had there been a parameter, nothing
// would have stood in the way of the caller filling it in.
//
// metadata is the free JSON object to attach to the line; it may be left empty.
//
// After the line is added the cart totals are RECOMPUTED. If the computation blows
// up, the line stays written and the error is returned with the
// [CodeTotalsAfterChange] code; the caller MUST NOT REPEAT the request (the line
// would be added a second time).
func (i *Interop) AddPricedLineItem(
	ctx context.Context,
	cartID, variantID string,
	quantity int64,
	metadata json.RawMessage,
) (string, error) {
	result, err := i.w.AddLineItem(ctx, AddLineItemInput{
		CartID:    cartID,
		VariantID: variantID,
		Quantity:  quantity,
		Metadata:  metadata,
	})
	if err != nil {
		return "", err
	}
	return result.LineItemID, nil
}

// AddQuotedShippingMethod attaches a shipping option to the cart at the price
// the fulfillment module quotes, and returns the method's id.
//
// The caller supplies WHICH option and nothing about the price; the rationale
// is in the [Workflows.AddQuotedShippingMethod] godoc.
func (i *Interop) AddQuotedShippingMethod(
	ctx context.Context,
	cartID, shippingOptionID string,
	data json.RawMessage,
) (string, error) {
	return i.w.AddQuotedShippingMethod(ctx, cartID, shippingOptionID, data)
}

// SetLineItemQuantity writes the line's quantity as an ABSOLUTE value and
// recomputes the totals; it reports whether the line was removed.
//
// A quantity of zero REMOVES the line and the first returned value says so; the
// rationale is in the [Workflows.UpdateLineItem] godoc. A negative quantity is
// rejected.
//
// When the quantity changes the price can change as well (pricing picks the price
// by quantity range), which is why the recomputation is not a convenience but a
// NECESSITY: writing the quantity without running the computation would leave the
// line at the old tier's price.
func (i *Interop) SetLineItemQuantity(
	ctx context.Context,
	cartID, lineItemID string,
	quantity int64,
) (bool, error) {
	result, err := i.w.UpdateLineItem(ctx, UpdateLineItemInput{
		CartID:     cartID,
		LineItemID: lineItemID,
		Quantity:   quantity,
	})
	if err != nil {
		return false, err
	}
	return result.Removed, nil
}

// ApplyPromotionCode writes a coupon code onto the cart and reprices it.
//
// It is the storefront's `POST /store/v1/carts/{id}/promotions`, and it delegates
// without deciding anything: the order — ask the promotion module, then write, then
// reprice — is [Workflows.ApplyPromotionCode]'s, and a surface that re-stated it
// would be a second copy of a rule that already has one place.
func (i *Interop) ApplyPromotionCode(ctx context.Context, cartID, code string) error {
	return i.w.ApplyPromotionCode(ctx, cartID, code)
}

// RemovePromotionCode takes a coupon code off the cart and reprices it.
//
// It is the storefront's `DELETE /store/v1/carts/{id}/promotions/{code}`. A cart
// that was not holding the code answers NotFound, which is the workflow's decision
// and not this surface's.
func (i *Interop) RemovePromotionCode(ctx context.Context, cartID, code string) error {
	return i.w.RemovePromotionCode(ctx, cartID, code)
}

// TrialPromotionJSON prices a promotion against the orders placed in [from, to) as
// if it had been published then, and returns the [TrialReport] as JSON; it writes
// nothing (ADR 0176).
//
// Its consumer is the promotion module's admin endpoint, which owns the question
// but cannot read an order or build a purchase the way a cart is built; both are
// this package's. The report is JSON because it is composite and the promotion
// module cannot name its type (ADR 0006); the schema is the json tags of
// [TrialReport].
func (i *Interop) TrialPromotionJSON(
	ctx context.Context, promotionID string, from, to time.Time,
) (json.RawMessage, error) {
	report, err := i.w.TrialPromotion(ctx, promotionID, from, to)
	if err != nil {
		return nil, err
	}

	return json.Marshal(report)
}

// RepriceAfter runs a write the cart module makes on its own and then recomputes
// the cart's totals.
//
// Six storefront writes change the cart through the module's service rather than
// through a flow — the e-mail and the customer, the two addresses, removing a line
// or a shipping method, and a merge — and each of them leaves the totals stale.
// The write is handed IN rather than made before the call, so the pairing is this
// method's and not the caller's discipline: a caller that reaches the flow gets
// the repricing, and one that cannot reach it never writes (ADR 0173).
//
// A repricing that fails after the write does not take the write back, which is
// [Workflows.AddLineItem]'s choice; the error says the totals are stale. It is not
// an endpoint: a client cannot reprice a cart it has not changed.
func (i *Interop) RepriceAfter(ctx context.Context, cartID string, change func() error) error {
	if err := change(); err != nil {
		return err
	}
	if _, err := i.w.CalculateTotals(ctx, cartID); err != nil {
		return totalsAfterChange(err, cartID, "the cart was changed")
	}

	return nil
}
