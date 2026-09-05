package cart

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
)

// This file is the DISCOUNT leg of the cart calculation (plan Phase 7).
//
// In Phase 5 the discount was ALWAYS ZERO and the field was left with a
// "promotion will take this over" note; the takeover happens here. The promotion
// module does the calculation, this package only translates the shape of the cart
// into its contract, VALIDATES the result and writes it onto the lines.

// attrVariantID is the line item attribute that discount rules may look at.
//
// The cart line's variant is the only catalog fact this workflow KNOWS about the
// line item. Product and category IDs could be rule targets too, but the cart does
// not carry them; reading them from the catalog would mean an extra round trip per
// line, and no rule asks for them today. The day they are added the place is
// known: the attribute map inside [Workflows.discountRequestFor].
const attrVariantID = "variant_id"

// discountRequest is the JSON schema of the discount request that goes to the
// promotion module.
//
// The field names MUST be EXACTLY the same as promotion's interop schema: the
// other side REJECTS unknown fields, and because the two packages cannot import
// each other the compiler cannot see the match (the accepted price of ADR 0006).
// The match can only be proven by an integration test.
//
// All amounts are INTEGER minor units (plan Section 8).
type discountRequest struct {
	// CurrencyCode is the cart's currency; filtering out fixed-amount
	// promotions rests on it.
	CurrencyCode string `json:"currency_code"`
	// Context holds the fields that context rules will look at.
	Context map[string]string `json:"context"`
	// Items are the cart's lines and they go in the cart's ORDER.
	Items []discountRequestItem `json:"items"`
	// ShippingMethods is ALWAYS EMPTY; the rationale is in the
	// [Workflows.discountRequestFor] godoc.
	ShippingMethods []discountRequestShipping `json:"shipping_methods"`
	// Codes are the coupon codes to apply and are ALWAYS EMPTY in this phase; the
	// rationale is under the "Coupon codes" heading in the package comment.
	Codes []string `json:"codes"`
	// At is the instant of the calculation; it is left empty and promotion uses
	// "now".
	//
	// A cart calculation ALWAYS belongs to now: a backdated calculation would
	// show a campaign that ended today as live in the cart.
	At string `json:"at"`
}

// discountRequestItem is the schema of a single cart line in the request.
type discountRequestItem struct {
	// ID is the cart line's ID; the discount comes back under the same ID.
	ID string `json:"id"`
	// Amount is the line's PRE-DISCOUNT subtotal (unit x quantity).
	Amount int64 `json:"amount"`
	// Quantity is the quantity on the line; it determines how many units a "fixed
	// amount per unit" discount applies to.
	Quantity int64 `json:"quantity"`
	// Attributes are the line attributes that target rules will look at.
	Attributes map[string]string `json:"attributes"`
}

// discountRequestShipping is the schema of a single shipping method in the
// request.
//
// The type exists only so that the SCHEMA is COMPLETE; this package never sends a
// shipping method.
type discountRequestShipping struct {
	// ID is the shipping method's ID.
	ID string `json:"id"`
	// Amount is the shipping amount (minor units).
	Amount int64 `json:"amount"`
	// Attributes are the attributes that target rules will look at.
	Attributes map[string]string `json:"attributes"`
}

// discountResponse is the JSON schema of the discount result returned by the
// promotion module.
//
// Unknown fields are SILENTLY SKIPPED (the opposite of the request): when
// promotion grows its schema, this package must not have to be updated in the same
// round. The silence is only for UNRECOGNIZED fields — the invariants that the
// recognized fields carry are VALIDATED one by one inside
// [Workflows.applyDiscounts].
type discountResponse struct {
	// CurrencyCode is the currency of the calculation (UPPERCASE).
	CurrencyCode string `json:"currency_code"`
	// Items are the per-item discounts; it carries one record for EVERY item in
	// the request and is in the SAME order as the request.
	Items []discountLine `json:"items"`
	// ShippingMethods are the per-shipping-method discounts; because this package
	// sends no shipping, it is expected to be EMPTY.
	ShippingMethods []discountLine `json:"shipping_methods"`
	// ItemsDiscountTotal is the total discount falling on the items.
	ItemsDiscountTotal int64 `json:"items_discount_total"`
	// ShippingDiscountTotal is the total discount falling on shipping; zero is
	// expected.
	ShippingDiscountTotal int64 `json:"shipping_discount_total"`
	// DiscountTotal is the total discount.
	DiscountTotal int64 `json:"discount_total"`
}

// discountLine is the schema of a single line discount in the response.
type discountLine struct {
	// ID is the line the discount belongs to.
	ID string `json:"id"`
	// Amount is the TOTAL discount falling on the line (minor units).
	Amount int64 `json:"amount"`
}

// applyDiscounts takes the lines' discount from the promotion module and WRITES it
// onto the lines.
//
// The lines' subtotals must already have been calculated; tax, on the other hand,
// has NOT YET been calculated. The order is the contract itself: the tax base is
// POST-DISCOUNT (see the package comment, "Tax contract"), and if the discount
// were not known before tax the base would stay wrong.
//
// # If promotion is NOT registered
//
// The discount stays ZERO and the calculation continues. The same pattern exists
// in the product module's storefront listing (if there is no price/stock provider
// the catalog comes back without prices): modularity itself demands it — the cart
// must work in a deployment that does not install promotion. The direction is safe
// too; a missing discount OVERCHARGES the customer, and the customer sees that and
// says so. The reverse direction (a missing tax) would silently come out of the
// merchant's own pocket, and that is why tax does not fall back to zero (see
// [Workflows.applyTaxes]).
//
// The presence of the surface is logged once at STARTUP ([FromContainer]); no
// per-round warning is produced here.
//
// # The returned result is VALIDATED
//
// promotion's godoc promises three invariants: a response line for every request
// line IN THE SAME ORDER, a line discount that does not exceed the line amount,
// and the identity of the totals. Validating something that was promised may look
// unnecessary, but the compiler does not check the other side of the boundary, and
// if a BROKEN discount passes silently the result is a cart that trips the cart
// module's totals check or, worse, does not trip it and shows the customer a wrong
// amount. A contract violation is an errors.Internal: there is nothing the caller
// can fix.
func (w *Workflows) applyDiscounts(ctx context.Context, snap Snapshot, lines []LineTotals) error {
	if w.discounts == nil {
		return nil
	}
	if len(lines) != len(snap.Items) {
		return errors.Internal(CodeDiscountInvalid,
			"line count does not match the snapshot: %d calculated, %d lines (%s)",
			len(lines), len(snap.Items), snap.ID)
	}

	payload, err := json.Marshal(w.discountRequestFor(snap, lines))
	if err != nil {
		return errors.Wrap(err, errors.KindInternal, CodeDiscountFailed,
			"discount request could not be encoded to JSON: %s", snap.ID)
	}

	raw, err := w.discounts.ComputeDiscountsJSON(ctx, payload)
	if err != nil {
		// The class is PRESERVED: promotion's Invalid is a contract mismatch, and
		// had it been turned into Internal a fixable wiring error would look like
		// a server failure.
		return errors.Wrap(err, errors.KindOf(err), CodeDiscountFailed,
			"cart discount could not be calculated: %s (%d lines)", snap.ID, len(lines))
	}

	var resp discountResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return errors.Wrap(err, errors.KindInternal, CodeDiscountInvalid,
			"discount result could not be decoded: %s", snap.ID)
	}
	return applyDiscountResponse(snap, lines, resp)
}

// discountRequestFor translates the shape of the cart into promotion's request
// schema.
//
// # Shipping methods are NOT SENT
//
// promotion can apply a discount to shipping too, but the [Totals] schema has NO
// field to carry a shipping discount: the cart's discount is the sum of the line
// discounts, and the cart module applies the "a discount cannot exceed the
// subtotal" rule against the subtotal (which excludes shipping). Folding the
// shipping discount into the cart discount would push the discount above the
// subtotal on a cart with cheap goods but expensive shipping, and cart would reject
// the WHOLE calculation. Subtracting it from [Totals.ShippingTotal] would instead
// hide the discount somewhere invisible — the customer could not read on any line
// why shipping got cheaper.
//
// That is why shipping is NOT DRAWN INTO the calculation; the shipping discount is
// opened up the day [Totals] gains a "shipping_discount_total" field, and the place
// it will be wired to is this empty slice in the request.
//
// # Only the region is put into the context
//
// The customer group is NOT put into the context; the rationale is the same as for
// the price context (see the package comment, "Customer segment prices"): the cart
// does not know the customer's groups, and silently picking one would tie the
// discount to map iteration order. The group context is added here the day the
// customer surface publishes the group list.
func (w *Workflows) discountRequestFor(snap Snapshot, lines []LineTotals) discountRequest {
	items := make([]discountRequestItem, 0, len(lines))
	for i := range lines {
		items = append(items, discountRequestItem{
			ID:         lines[i].LineItemID,
			Amount:     lines[i].Subtotal,
			Quantity:   snap.Items[i].Quantity,
			Attributes: map[string]string{attrVariantID: snap.Items[i].VariantID},
		})
	}

	return discountRequest{
		CurrencyCode:    snap.CurrencyCode,
		Context:         map[string]string{attrRegionID: snap.RegionID},
		Items:           items,
		ShippingMethods: []discountRequestShipping{},
		Codes:           []string{},
	}
}

// applyDiscountResponse VALIDATES the response and writes it onto the lines.
//
// All of the validation stands in one place so that a forgotten rule can be seen
// by eye. The write is done after ALL of the validation passes: half-written lines
// would leave the caller with an inconsistent slice even when an error is
// returned.
func applyDiscountResponse(snap Snapshot, lines []LineTotals, resp discountResponse) error {
	if !strings.EqualFold(resp.CurrencyCode, snap.CurrencyCode) {
		return errors.Internal(CodeDiscountInvalid,
			"discount was calculated in a different currency: cart %q, result %q (%s)",
			snap.CurrencyCode, resp.CurrencyCode, snap.ID)
	}
	if len(resp.Items) != len(lines) {
		return errors.Internal(CodeDiscountInvalid,
			"discount result returned a wrong record count: %d lines, %d records (%s)",
			len(lines), len(resp.Items), snap.ID)
	}
	if resp.ShippingDiscountTotal != 0 || len(resp.ShippingMethods) != 0 {
		return errors.Internal(CodeDiscountInvalid,
			"a shipping discount came back although no shipping method was sent: %d (%s)",
			resp.ShippingDiscountTotal, snap.ID)
	}

	var sum int64
	for i := range resp.Items {
		line := resp.Items[i]
		if line.ID != lines[i].LineItemID {
			return errors.Internal(CodeDiscountInvalid,
				"discount result did not preserve the request order: record %d is %q, expected %q (%s)",
				i, line.ID, lines[i].LineItemID, snap.ID)
		}
		if line.Amount < 0 || line.Amount > lines[i].Subtotal {
			return errors.Internal(CodeDiscountInvalid,
				"line discount must be in the range [0, %d]: %q -> %d (%s)",
				lines[i].Subtotal, line.ID, line.Amount, snap.ID)
		}

		var err error
		if sum, err = addAmount(sum, line.Amount); err != nil {
			return err
		}
	}

	// The cart discount is Σ of the line discounts. promotion reports the same
	// identity with its own total as well; the two diverging means the discount
	// written onto the lines differs from the one written onto the cart, and cart's
	// totals check would only catch that at write time.
	if sum != resp.ItemsDiscountTotal || resp.DiscountTotal != resp.ItemsDiscountTotal {
		return errors.Internal(CodeDiscountInvalid,
			"discount total does not match the line discounts: Σ=%d, items total=%d, grand total=%d (%s)",
			sum, resp.ItemsDiscountTotal, resp.DiscountTotal, snap.ID)
	}

	for i := range resp.Items {
		lines[i].DiscountTotal = resp.Items[i].Amount
	}
	return nil
}
