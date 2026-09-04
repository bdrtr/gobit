package cart

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// Error codes for the shipping flow.
const (
	// CodeShippingUnavailable reports that the shipping surface is not wired,
	// so no option can be priced.
	CodeShippingUnavailable = "cart_workflow_shipping_unavailable"
	// CodeShippingOptionUnknown reports that the requested option is not among
	// the ones eligible for this cart.
	CodeShippingOptionUnknown = "cart_workflow_shipping_option_unknown"
	// CodeShippingQuoteFailed reports that the options could not be quoted.
	CodeShippingQuoteFailed = "cart_workflow_shipping_quote_failed"
)

// quoteRequest is the request schema of the fulfillment module's option
// listing. The producing side documents it; it is repeated here because this
// package cannot import that module (ADR 0006).
type quoteRequest struct {
	RegionID     string `json:"region_id"`
	CurrencyCode string `json:"currency_code"`
	CountryCode  string `json:"country_code"`
	Subtotal     int64  `json:"subtotal"`
	ItemCount    int64  `json:"item_count"`
	TotalWeight  int64  `json:"total_weight"`
}

// quotedOption is one option as the fulfillment module priced it.
type quotedOption struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Amount       int64  `json:"amount"`
	CurrencyCode string `json:"currency_code"`
	IsReturn     bool   `json:"is_return"`
	AdminOnly    bool   `json:"admin_only"`
}

// quoteResponse is the response schema of the option listing.
type quoteResponse struct {
	Options []quotedOption `json:"options"`
}

// AddQuotedShippingMethod attaches a shipping option to the cart at the price
// the FULFILLMENT MODULE quotes for it, and returns the method's id.
//
// # Why this flow exists at all
//
// The cart service can write a shipping method with any amount, and until this
// flow existed the storefront endpoint passed the amount straight out of the
// request body. A shopper could post a real option id with an amount of zero
// and the order was created — and captured — at that price. The quote engine
// that produces the right number was fully built and nothing asked it.
//
// The cart module cannot ask it: a module does not know another module
// (Principle 2.1/2.4). Deciding a price from facts that live in two modules is
// this layer's job, and the module's own package doc already made that argument
// for the LINE price:
//
//	"Had the method stayed on the surface, a handler bound to it would SILENTLY
//	skip both the pricing and the ceiling."
//
// The same sentence is true of the shipping price. This flow is that sentence
// applied to the second place it was always true.
//
// # The facts are the CART's, never the caller's
//
// Everything the quote is computed from — region, currency, country, subtotal,
// item count — is read from the cart's own record. The caller supplies exactly
// two things: WHICH option, and the free-form data blob that is carried and
// never read. There is no field a caller can send that changes the price.
//
// That is also why the call reaches fulfillment through the interop surface
// rather than the storefront endpoint: interop marks the facts TRUSTED, which
// is what lets rule-bound options ("free shipping over 500") be quoted at all.
// The HTTP endpoint cannot quote them, because it cannot verify the facts.
//
// # It fails CLOSED
//
// If the fulfillment surface is not wired, the method is NOT ADDED. The
// reasoning is [Workflows.applyTaxes]'s in reverse: a missing tax surface has a
// correct fallback — the region's rate — and the answer stays a real number. A
// missing shipping surface has no fallback at all. The only other source for a
// shipping price is the caller, and taking it from there is the defect this
// flow exists to close, so degrading to it would be worse than refusing.
func (w *Workflows) AddQuotedShippingMethod(
	ctx context.Context, cartID, shippingOptionID string, data json.RawMessage,
) (string, error) {
	if cartID == "" {
		return "", errors.Invalid(CodeInvalidInput, "the cart id is required")
	}
	if shippingOptionID == "" {
		// The option id is REQUIRED here even though the cart service treats it
		// as optional. A method without an option cannot be quoted, and a
		// method that cannot be quoted is one whose price came from the caller.
		return "", errors.Invalid(CodeInvalidInput,
			"the shipping option id is required; the price is quoted from the option and there "+
				"is no other place it could come from")
	}
	if w.shipping == nil {
		return "", errors.Internal(CodeShippingUnavailable,
			"the fulfillment surface (%q) is not wired, so no shipping option can be priced; "+
				"the method is not added rather than written at a price the caller chose",
			ServiceFulfillment)
	}

	snap, err := w.snapshot(ctx, cartID)
	if err != nil {
		return "", err
	}

	option, err := w.quoteOption(ctx, snap, shippingOptionID)
	if err != nil {
		return "", err
	}

	// The currency is checked rather than trusted. A quote in another currency
	// would be added to the cart's shipping total as a bare integer, and the
	// arithmetic would look sound while the money was wrong.
	if option.CurrencyCode != "" && option.CurrencyCode != snap.CurrencyCode {
		return "", errors.Invalid(CodeShippingOptionUnknown,
			"the shipping option %q is priced in %s and the cart is in %s",
			shippingOptionID, option.CurrencyCode, snap.CurrencyCode)
	}

	return w.carts.AddShippingMethod(ctx, cartID, option.Name, option.ID, option.Amount, data)
}

// quoteOption asks the fulfillment module to price the cart's options and
// returns the requested one.
//
// A requested option that is NOT in the answer is refused rather than looked up
// some other way. The listing is not merely a catalog: it applies the
// eligibility rules for THIS cart, so "not in the list" and "not allowed for
// this cart" are the same fact, and reaching past it to fetch the option by id
// would price something the rules had already excluded.
func (w *Workflows) quoteOption(
	ctx context.Context, snap Snapshot, shippingOptionID string,
) (quotedOption, error) {
	request, err := w.quoteRequestFor(ctx, snap)
	if err != nil {
		return quotedOption{}, err
	}

	body, err := json.Marshal(request)
	if err != nil {
		return quotedOption{}, errors.Wrap(err, errors.KindInternal, CodeShippingQuoteFailed,
			"the shipping quote request could not be built")
	}

	raw, err := w.shipping.ListOptionsJSON(ctx, body)
	if err != nil {
		// NOT swallowed, and this is the opposite choice from the discount and
		// tax surfaces. There, a failure has a defined fallback. Here the only
		// fallback is the caller's number.
		return quotedOption{}, errors.Wrap(err, errors.KindOf(err), CodeShippingQuoteFailed,
			"the shipping options could not be quoted for cart %s", snap.ID)
	}

	var answer quoteResponse
	if err := json.Unmarshal(raw, &answer); err != nil {
		return quotedOption{}, errors.Wrap(err, errors.KindInternal, CodeShippingQuoteFailed,
			"the shipping quote answer could not be parsed for cart %s", snap.ID)
	}

	for i := range answer.Options {
		if answer.Options[i].ID != shippingOptionID {
			continue
		}
		// An admin-only or return option that reached the listing is still not
		// something a shopper may choose. The request never asks for them, so
		// this is a second lock on the same door rather than the only one.
		if answer.Options[i].AdminOnly || answer.Options[i].IsReturn {
			break
		}

		return answer.Options[i], nil
	}

	return quotedOption{}, errors.Invalid(CodeShippingOptionUnknown,
		"the shipping option %q is not available for cart %s", shippingOptionID, snap.ID)
}

// quoteRequestFor builds the quote request from the cart's own facts.
func (w *Workflows) quoteRequestFor(ctx context.Context, snap Snapshot) (quoteRequest, error) {
	lines, err := w.lineSubtotals(ctx, snap)
	if err != nil {
		return quoteRequest{}, err
	}

	// The DISCOUNTS ARE APPLIED before the subtotal is read, in the same order
	// [Workflows.computeTotals] applies them, so the two cannot disagree about
	// what the basket is worth.
	//
	// The subtotal is therefore taken AFTER the discount, and that is the
	// decision rather than an accident of ordering. The rules reading it are
	// thresholds — "free shipping over 500" — and answering them from the
	// pre-discount number would spend the same discount twice: once off the
	// goods, and again off the delivery of a basket the shopper is not actually
	// paying that much for.
	if err := w.applyDiscounts(ctx, snap, lines); err != nil {
		return quoteRequest{}, err
	}

	var subtotal int64
	for i := range lines {
		next, err := addAmount(subtotal, lines[i].Subtotal-lines[i].DiscountTotal)
		if err != nil {
			return quoteRequest{}, err
		}
		subtotal = next
	}

	var itemCount int64
	for i := range snap.Items {
		next, err := addAmount(itemCount, snap.Items[i].Quantity)
		if err != nil {
			return quoteRequest{}, err
		}
		itemCount = next
	}

	// The country is resolved from the REGION, exactly as the tax computation
	// resolves it, so the two cannot disagree about where the cart is. An
	// unresolved country is left empty rather than guessed: the fulfillment
	// module then drops the options bound to a country, which errs toward
	// offering too few rather than pricing for the wrong place.
	countryCode, _, err := w.countryForRegion(ctx, snap.RegionID)
	if err != nil {
		return quoteRequest{}, err
	}

	return quoteRequest{
		RegionID:     snap.RegionID,
		CurrencyCode: snap.CurrencyCode,
		CountryCode:  countryCode,
		Subtotal:     subtotal,
		ItemCount:    itemCount,
		// The cart does not carry weight. Sending zero is honest — it is what
		// this installation knows — and a weight-banded option prices at its
		// lowest band rather than at a number nobody measured. When the cart
		// grows a weight, it goes here and nothing else changes.
		TotalWeight: 0,
	}, nil
}
