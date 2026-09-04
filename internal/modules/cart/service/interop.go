package service

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// This file is the CROSS-MODULE surface of the cart module (ADR 0001, ADR 0006).
//
// internal/workflows/cart needs the cart service while it runs the cart flows,
// but neither can that package import this module nor can this module import
// that package. The solution is the same as the interop.go in the
// region/pricing/customer modules: to publish a surface that uses only
// PRIMITIVE and stdlib types. The consumer defines its own narrow interface,
// this type satisfies it STRUCTURALLY, and it is resolved from the container by
// name.
//
// Composite data (the cart's snapshot, the computed totals) travels as JSON.
// The field names are declared EXPLICITLY here; they MUST be exactly the same
// as the schema on the consumer's side and the match can only be proven by an
// integration test (see internal/e2e).

// CodeInteropTotalsInvalid reports that an unparseable totals body arrived.
const CodeInteropTotalsInvalid = "e2e_kopru_totals_invalid"

// CodeInteropMetadataInvalid reports that an unparseable metadata body
// arrived.
//
// The code is SHARED by the cart and the line surfaces: on both of them the
// malformation is the same thing (a body that is not a JSON object) and the
// correction the caller will make is the same as well. Separate codes would
// offer the client a distinction it cannot tell apart.
const CodeInteropMetadataInvalid = "cart_interop_metadata_invalid"

// Interop translates the cart service into the cross-module PRIMITIVE surface.
//
// It makes no decisions: it translates only the signature and the JSON schema.
// All the business rules stay on [Service]; adding a rule here would mean the
// same rule diverging in two places.
//
// It is registered in the container under the name "cart.interop" and the cart
// flows resolve it with the narrow interface they define themselves (ADR 0006).
type Interop struct {
	svc *Service
}

// NewInterop sets up the cross-module surface for the given service.
func NewInterop(svc *Service) *Interop { return &Interop{svc: svc} }

// interopSnapshot is the JSON schema of the cart shape that goes into the
// calculation.
//
// The field names must be exactly the same as the schema on the consumer's side
// and the match can only be proven by an integration test: because this module
// cannot import the workflow package, the compiler cannot check the match.
type interopSnapshot struct {
	ID              string                  `json:"id"`
	RegionID        string                  `json:"region_id"`
	CustomerID      string                  `json:"customer_id"`
	CurrencyCode    string                  `json:"currency_code"`
	Revision        int64                   `json:"revision"`
	Completed       bool                    `json:"completed"`
	Items           []interopItem           `json:"items"`
	ShippingMethods []interopShippingMethod `json:"shipping_methods"`
}

// interopItem is the JSON schema of a cart line.
type interopItem struct {
	ID        string `json:"id"`
	VariantID string `json:"variant_id"`
	Quantity  int64  `json:"quantity"`
}

// interopShippingMethod is the JSON schema of a shipping method.
type interopShippingMethod struct {
	ID     string `json:"id"`
	Amount int64  `json:"amount"`
}

// interopTotals is the JSON schema of the computed cart totals.
type interopTotals struct {
	Revision      int64               `json:"revision"`
	Subtotal      int64               `json:"subtotal"`
	DiscountTotal int64               `json:"discount_total"`
	TaxTotal      int64               `json:"tax_total"`
	ShippingTotal int64               `json:"shipping_total"`
	Total         int64               `json:"total"`
	Lines         []interopLineTotals `json:"lines"`
}

// interopLineTotals is the JSON schema of the amounts computed per line.
type interopLineTotals struct {
	LineItemID    string `json:"line_item_id"`
	UnitPrice     int64  `json:"unit_price"`
	Subtotal      int64  `json:"subtotal"`
	DiscountTotal int64  `json:"discount_total"`
	TaxTotal      int64  `json:"tax_total"`
	Total         int64  `json:"total"`
}

// OpenCart opens a new cart and returns its identifier.
//
// Both regionID and currencyCode are THE CALLER'S DERIVATION, not a choice: the
// region is resolved from the customer's country and the currency from that
// region, and this surface cannot ask for either of them itself (the cart module
// does not call region, ADR 0006). The only caller is the create_cart workflow
// and the storefront end now goes through it too — that is, EVERY path that
// opens a cart goes through the same derivation, none of them takes the region
// from the client.
//
// metadata is the cart's free-form extra data and must be a JSON OBJECT; it may
// be left empty. A malformed body is errors.Invalid and the cart IS NOT OPENED;
// the rationale is the same as the one in the [Interop.AddCartLineItem] godoc.
func (i *Interop) OpenCart(
	ctx context.Context,
	regionID, currencyCode, customerID, email string,
	metadata json.RawMessage,
) (string, error) {
	extra, err := decodeInteropMetadata(metadata)
	if err != nil {
		return "", err
	}

	cart, err := i.svc.CreateCart(ctx, CreateCartInput{
		RegionID:     regionID,
		CustomerID:   customerID,
		Email:        email,
		CurrencyCode: currencyCode,
		Metadata:     extra,
	})
	if err != nil {
		return "", err
	}
	return cart.ID, nil
}

// CartSnapshotJSON returns the cart shape that goes into the calculation in a
// single read.
//
// The read is done with [Service.GetCart]; because that method runs its four
// queries over a single snapshot, the lines, the shipping methods and the
// revision belong to the SAME moment — that is the consistency the schema wants.
func (i *Interop) CartSnapshotJSON(ctx context.Context, cartID string) (json.RawMessage, error) {
	detail, err := i.svc.GetCart(ctx, cartID)
	if err != nil {
		return nil, err
	}

	snapshot := interopSnapshot{
		ID:              detail.ID,
		RegionID:        detail.RegionID,
		CustomerID:      detail.CustomerID,
		CurrencyCode:    detail.CurrencyCode,
		Revision:        detail.Revision,
		Completed:       detail.Completed(),
		Items:           make([]interopItem, 0, len(detail.Items)),
		ShippingMethods: make([]interopShippingMethod, 0, len(detail.ShippingMethods)),
	}
	for i := range detail.Items {
		snapshot.Items = append(snapshot.Items, interopItem{
			ID:        detail.Items[i].ID,
			VariantID: detail.Items[i].VariantID,
			Quantity:  detail.Items[i].Quantity,
		})
	}
	for i := range detail.ShippingMethods {
		snapshot.ShippingMethods = append(snapshot.ShippingMethods, interopShippingMethod{
			ID:     detail.ShippingMethods[i].ID,
			Amount: detail.ShippingMethods[i].Amount,
		})
	}

	return json.Marshal(snapshot)
}

// AddCartLineItem adds a line to the cart and returns the line's identifier.
//
// metadata is the line's free-form extra data and must be a JSON OBJECT; it may
// be left empty. A malformed body is errors.Invalid and the line IS NOT
// WRITTEN: throwing it away silently would leave a field the client thinks it
// sent but which is nowhere to be found.
func (i *Interop) AddCartLineItem(
	ctx context.Context,
	cartID, variantID, title string,
	quantity, unitPrice int64,
	metadata json.RawMessage,
) (string, error) {
	extra, err := decodeInteropMetadata(metadata)
	if err != nil {
		return "", err
	}

	line, err := i.svc.AddLineItem(ctx, cartID, AddLineItemInput{
		VariantID: variantID,
		Title:     title,
		Quantity:  quantity,
		UnitPrice: unitPrice,
		Metadata:  extra,
	})
	if err != nil {
		return "", err
	}
	return line.ID, nil
}

// decodeInteropMetadata parses the free-form extra data; an empty body returns
// nil.
//
// "JSON null" counts as nil too: on this surface there is no difference between
// a client's intent to clear the field explicitly and its not sending the field
// at all — the record (the cart or the line) is being opened NEW, there is no
// value to be deleted.
func decodeInteropMetadata(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	var extra map[string]any
	if err := json.Unmarshal(raw, &extra); err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, CodeInteropMetadataInvalid,
			"metadata must be a JSON object")
	}
	return extra, nil
}

// SetCartLineItemQuantity writes the line's quantity as an absolute value.
func (i *Interop) SetCartLineItemQuantity(ctx context.Context, cartID, lineItemID string, quantity int64) error {
	_, err := i.svc.UpdateLineItemQuantity(ctx, cartID, lineItemID, quantity)
	return err
}

// RemoveLineItem removes the line from the cart.
func (i *Interop) RemoveLineItem(ctx context.Context, cartID, lineItemID string) error {
	return i.svc.RemoveLineItem(ctx, cartID, lineItemID)
}

// SetCartTotalsJSON writes the computed totals onto the cart.
func (i *Interop) SetCartTotalsJSON(ctx context.Context, cartID string, totals json.RawMessage) error {
	var incoming interopTotals
	if err := json.Unmarshal(totals, &incoming); err != nil {
		return errors.Wrap(err, errors.KindInvalid, CodeInteropTotalsInvalid,
			"the cart totals could not be parsed: %s", cartID)
	}

	lines := make([]LineTotals, 0, len(incoming.Lines))
	for i := range incoming.Lines {
		lines = append(lines, LineTotals{
			LineItemID:    incoming.Lines[i].LineItemID,
			UnitPrice:     incoming.Lines[i].UnitPrice,
			Subtotal:      incoming.Lines[i].Subtotal,
			DiscountTotal: incoming.Lines[i].DiscountTotal,
			TaxTotal:      incoming.Lines[i].TaxTotal,
			Total:         incoming.Lines[i].Total,
		})
	}

	return i.svc.SetTotals(ctx, cartID, Totals{
		Revision:      incoming.Revision,
		Subtotal:      incoming.Subtotal,
		DiscountTotal: incoming.DiscountTotal,
		TaxTotal:      incoming.TaxTotal,
		ShippingTotal: incoming.ShippingTotal,
		Total:         incoming.Total,
		Lines:         lines,
	})
}

// MarkCompleted marks the cart as completed.
//
// It is the LAST step of the order completion saga: from this point on the cart
// IS IMMUTABLE (every write method returns errors.Conflict). Marking an already
// completed cart a second time DOES NOT return an error; the saga may rerun a
// step.
func (i *Interop) MarkCompleted(ctx context.Context, cartID string) error {
	_, err := i.svc.MarkCompleted(ctx, cartID)
	return err
}

// interopCartTotals is the JSON schema of the cart's written totals.
//
// The field names must be exactly the same as the schema on the consumer's
// side; because this module cannot import the workflow package the compiler
// cannot check the match, and the match is only proven by an integration test.
type interopCartTotals struct {
	CurrencyCode  string `json:"currency_code"`
	Subtotal      int64  `json:"subtotal"`
	DiscountTotal int64  `json:"discount_total"`
	TaxTotal      int64  `json:"tax_total"`
	ShippingTotal int64  `json:"shipping_total"`
	Total         int64  `json:"total"`
	Completed     bool   `json:"completed"`
}

// CartTotalsJSON returns the cart's WRITTEN totals.
//
// The saga opens the payment collection with the cart's grand total; it does NOT
// compute the amount ITSELF. The calculation is the calculate_totals flow's job
// and its result is stored on the cart (see [Service.SetTotals]). This way the
// payment is opened with the amount as of the moment the calculation was made,
// and no arithmetic diverging in two places comes about.
func (i *Interop) CartTotalsJSON(ctx context.Context, cartID string) (json.RawMessage, error) {
	detail, err := i.svc.GetCart(ctx, cartID)
	if err != nil {
		return nil, err
	}
	return json.Marshal(interopCartTotals{
		CurrencyCode:  detail.CurrencyCode,
		Subtotal:      detail.Subtotal,
		DiscountTotal: detail.DiscountTotal,
		TaxTotal:      detail.TaxTotal,
		ShippingTotal: detail.ShippingTotal,
		Total:         detail.Total,
		Completed:     detail.Completed(),
	})
}
