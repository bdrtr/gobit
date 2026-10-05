package cart

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// AddLineItemInput is the input of the line to be added to the cart.
type AddLineItemInput struct {
	// CartID is the cart the line will be added to; it is MANDATORY.
	CartID string
	// VariantID is the product variant to be added; it is MANDATORY.
	VariantID string
	// Quantity is the quantity to be added; it must be POSITIVE.
	//
	// The value is not the ABSOLUTE quantity but the quantity TO BE ADDED: if
	// the same variant with the same properties is already in the cart no new
	// line is opened, the quantity of the existing line INCREASES by this much.
	Quantity int64
	// Metadata is the free-form JSON object to attach to the line; it is
	// OPTIONAL.
	//
	// The flow does not read it and does not take it into account; it only
	// carries it to the cart module. The field holds the storefront's per-line
	// intent (a gift note, personalization) and, since this flow is the only
	// path that opens a line, it has no other carrier.
	//
	// It IS NOT WRITTEN on a merge: if the same variant with the same properties
	// is already in the cart the cart module only increases the quantity and
	// preserves the existing line's metadata (see AddLineItem in the cart
	// service).
	Metadata json.RawMessage
	// Properties are what the shopper wrote on the line (ADR 0223): the same
	// variant with other properties is another line, and they reach the order.
	Properties map[string]string
	// AddOns are the add-on lines opened with this one (ADR 0229): each a
	// variant the line's product accepts (ADR 0228), with the shopper's words
	// on it. The flow prices each as it prices the line.
	AddOns []AddOnRequest
}

// AddOnRequest is one add-on of an added line (ADR 0229).
type AddOnRequest struct {
	VariantID  string            `json:"variant_id"`
	Properties map[string]string `json:"properties,omitempty"`
}

// pricedAddOn is one add-on on the wire to the cart module, as this flow
// decided its title and price.
type pricedAddOn struct {
	VariantID  string            `json:"variant_id"`
	Title      string            `json:"title"`
	UnitPrice  int64             `json:"unit_price"`
	Properties map[string]string `json:"properties,omitempty"`
}

// AddLineItemResult is the result of the added line and of the recalculated
// totals.
type AddLineItemResult struct {
	// LineItemID is the id of the line that was added (or whose quantity was
	// increased).
	LineItemID string
	// VariantID is the variant the line points at.
	VariantID string
	// Title is the line's title copied from the catalog.
	Title string
	// UnitPrice is the unit price written WHILE the line was being opened.
	//
	// It is not the final price: the calculation round that runs after the line
	// is opened selects the price again according to the LAST quantity in the
	// cart and writes that one to the line. The two diverge only when there is
	// a merge (see [Workflows.AddLineItem]).
	UnitPrice int64
	// Totals are the cart totals after the line was added.
	Totals Totals
}

// AddLineItem finds the variant's price, adds the line and recalculates the
// totals.
//
// The order: the cart's currency is read -> the variant's title is taken from
// the catalog -> the variant's price set is found over the link -> the unit
// price is calculated from pricing -> the line is written ->
// [Workflows.CalculateTotals] runs.
//
// # A variant with no price
//
// It is rejected (errors.Invalid); the rationale is in the
// [Workflows.priceSetsFor] godoc. If the price set exists but there is no valid
// price in the cart's currency the error is again errors.Invalid and the
// message writes the currency.
//
// # The line ceiling
//
// The cart module refuses a line past its ceiling (cart service MaxLineItems,
// ADR 0227) where it decides whether the line is new, under the cart's lock; the
// flow does not ask a snapshot, and a refused add has read the catalog and
// pricing first.
//
// # Merging and the price tier
//
// If the same variant with the same properties is already in the cart the cart
// module does not open a new line, it increases the quantity. In that case the unit price calculated
// HERE belongs to the quantity being added and may not belong to the merged
// quantity — pricing selects the price according to the quantity range, that
// is, once 3 + 2 merge the line may move into the "5+" tier. The difference is
// immaterial because the calculation round that runs right after the line is
// written reprices ALL the lines with the CURRENT quantity in the cart; the
// value here is only the line's opening value and is never the amount shown to
// the customer.
//
// # If the totals calculation blows up
//
// The line HAS BEEN WRITTEN and is not taken back. The error is returned
// wrapped with the [CodeTotalsAfterChange] code; the cart stays in the "stale
// totals" state the cart model recognizes, and that cart becoming an order is
// separately rejected. Deleting the line would mean destroying the customer's
// request because of a temporary pricing/region fault (see the package comment,
// "Why none of the flows is a saga").
func (w *Workflows) AddLineItem(ctx context.Context, in AddLineItemInput) (AddLineItemResult, error) {
	if err := requireID("cart_id", in.CartID); err != nil {
		return AddLineItemResult{}, err
	}
	if err := requireID("variant_id", in.VariantID); err != nil {
		return AddLineItemResult{}, err
	}
	quantity, err := quantity32(in.Quantity)
	if err != nil {
		return AddLineItemResult{}, err
	}

	snap, err := w.snapshot(ctx, in.CartID)
	if err != nil {
		return AddLineItemResult{}, err
	}
	if snap.Completed {
		return AddLineItemResult{}, errors.Conflict(CodeCartCompleted,
			"no line can be added to a completed cart: %s", in.CartID)
	}
	title, err := w.variantTitle(ctx, in.VariantID)
	if err != nil {
		return AddLineItemResult{}, err
	}
	// The add-ons are checked against the line's product before anything is
	// priced, which also bounds what is read: each is on the product's list,
	// and none twice.
	if err := w.checkAddOns(ctx, in.VariantID, in.AddOns); err != nil {
		return AddLineItemResult{}, err
	}
	variants := []string{in.VariantID}
	for _, addOn := range in.AddOns {
		variants = append(variants, addOn.VariantID)
	}
	priceSets, err := w.priceSetsFor(ctx, variants)
	if err != nil {
		return AddLineItemResult{}, err
	}

	// The list is discarded here: these two callers are PRICING, and a price set
	// is chosen by the merchant-ranked head alone (ADR 0049). "Any of my groups"
	// is a discount question — two price sets both matching would be two prices
	// with nothing deciding between them.
	attributes, _, contextErr := w.ruleContext(ctx, snap)
	if contextErr != nil {
		// The base price is a worse answer than the segment or contract price
		// and a far better one than no cart; see ruleContext.
		w.log.WarnContext(ctx, "the customer's groups or company could not be read; pricing without them",
			"error", contextErr, "customer_id", snap.CustomerID)
	}

	unitPrice, err := w.prices.CalculateAmount(ctx, priceSets[in.VariantID], snap.CurrencyCode, quantity,
		attributes)
	if err != nil {
		if errors.IsNotFound(err) {
			return AddLineItemResult{}, errors.Wrap(err, errors.KindInvalid, CodePriceUnavailable,
				"variant %s has no price in currency %s at quantity %d",
				in.VariantID, snap.CurrencyCode, in.Quantity)
		}
		return AddLineItemResult{}, err
	}
	if err := checkAmount("unit_price", unitPrice, MaxAmount); err != nil {
		return AddLineItemResult{}, err
	}
	addOns, err := w.priceAddOns(ctx, in.AddOns, priceSets, snap.CurrencyCode, quantity, attributes)
	if err != nil {
		return AddLineItemResult{}, err
	}

	lineID, err := w.carts.AddCartLineItem(ctx, in.CartID, in.VariantID, title, in.Quantity, unitPrice,
		in.Metadata, in.Properties, addOns)
	if err != nil {
		return AddLineItemResult{}, err
	}

	totals, err := w.CalculateTotals(ctx, in.CartID)
	if err != nil {
		return AddLineItemResult{}, totalsAfterChange(err, in.CartID, "line added")
	}

	return AddLineItemResult{
		LineItemID: lineID,
		VariantID:  in.VariantID,
		Title:      title,
		UnitPrice:  unitPrice,
		Totals:     totals,
	}, nil
}

// checkAddOns holds the add-ons of an added line to the list its product
// accepts (ADR 0228, ADR 0229), each named once. The product is read through the
// channel the request is scoped to, as the line's variant is.
func (w *Workflows) checkAddOns(ctx context.Context, variantID string, addOns []AddOnRequest) error {
	if len(addOns) == 0 {
		return nil
	}
	products, err := w.productIDsFor(ctx, []string{variantID})
	if err != nil {
		return err
	}
	productID, ok := products[variantID]
	if !ok {
		return errors.NotFound(CodeVariantUnknown, "variant %s is not in the catalog", variantID)
	}
	return w.productTakes(ctx, productID, addOns)
}

// productTakes holds add-ons to the list productID accepts (ADR 0228), each
// named once; a list that cannot be read takes none of them.
func (w *Workflows) productTakes(ctx context.Context, productID string, addOns []AddOnRequest) error {
	records, err := w.catalog.Graph(ctx, query.GraphSpec{
		Entity:  EntityProduct,
		Fields:  []string{query.IDField, FieldAddOnVariantIDs},
		Filters: map[string]any{FilterIDs: []string{productID}},
		Limit:   1,
	})
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), CodeCatalogReadFailed,
			"could not read the add-ons product %s accepts", productID)
	}
	var accepted []string
	if len(records) > 0 {
		accepted = stringList(records[0][FieldAddOnVariantIDs])
	}
	seen := make(map[string]bool, len(addOns))
	for _, addOn := range addOns {
		if seen[addOn.VariantID] {
			return errors.Invalid(CodeAddOnNotAccepted,
				"the add-on %s is named twice on one line", addOn.VariantID)
		}
		seen[addOn.VariantID] = true
		if !slices.Contains(accepted, addOn.VariantID) {
			return errors.Invalid(CodeAddOnNotAccepted,
				"variant %s is not an add-on the lines of product %s take", addOn.VariantID, productID)
		}
	}
	return nil
}

// priceAddOns prices each add-on as the line is priced — its own price set, at
// the line's quantity, in the cart's currency, under the same rule context —
// and reads its title from the catalog, and returns them as the cart module
// takes them; none is nil.
func (w *Workflows) priceAddOns(
	ctx context.Context,
	addOns []AddOnRequest,
	priceSets map[string]string,
	currency string,
	quantity int32,
	attributes map[string]string,
) (json.RawMessage, error) {
	if len(addOns) == 0 {
		return nil, nil
	}
	priced := make([]pricedAddOn, 0, len(addOns))
	for _, addOn := range addOns {
		title, err := w.variantTitle(ctx, addOn.VariantID)
		if err != nil {
			return nil, err
		}
		amount, err := w.prices.CalculateAmount(ctx, priceSets[addOn.VariantID], currency, quantity, attributes)
		if err != nil {
			if errors.IsNotFound(err) {
				return nil, errors.Wrap(err, errors.KindInvalid, CodePriceUnavailable,
					"add-on %s has no price in currency %s at quantity %d", addOn.VariantID, currency, quantity)
			}
			return nil, err
		}
		if err := checkAmount("add_on unit_price", amount, MaxAmount); err != nil {
			return nil, err
		}
		priced = append(priced, pricedAddOn{
			VariantID: addOn.VariantID, Title: title, UnitPrice: amount, Properties: addOn.Properties,
		})
	}
	raw, err := json.Marshal(priced)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeAddOnNotAccepted, "the add-ons could not be encoded")
	}
	return raw, nil
}

// decodeAddOnRequests reads the add-ons an add-line call carries; a field this
// schema does not know refuses it.
func decodeAddOnRequests(raw json.RawMessage) ([]AddOnRequest, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var out []AddOnRequest
	if err := decoder.Decode(&out); err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, CodeAddOnNotAccepted,
			"the add-ons must be a JSON array of variant_id and properties")
	}
	return out, nil
}

// totalsAfterChange wraps the error of the calculation that blew up AFTER the
// cart CHANGED.
//
// The wrapping is there so that the caller can tell two states apart: the
// request was rejected (the cart did not change) versus the request was applied
// but the amount could not be calculated. In the second case repeating the
// request would add the line a SECOND TIME; the correct behavior is only to run
// the calculation again. The error's KIND is preserved so that the layer
// translating it into a status code writes the right one.
func totalsAfterChange(err error, cartID, what string) error {
	return errors.Wrap(err, errors.KindOf(err), CodeTotalsAfterChange,
		"%s (%s) but the totals could not be calculated; the cart's totals are stale, the calculation has to be run again",
		what, cartID)
}
