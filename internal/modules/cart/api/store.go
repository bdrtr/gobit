package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// createCartRequest is the body of POST /store/v1/carts.
//
// # currency_code and region_id ARE NOT HERE
//
// Both used to be in this body and both were the SERVER'S data; the class is the
// same as that of unit_price, which was removed by the same measure
// ([addLineItemRequest]).
//
//   - currency_code. The currency is the region's data: in the region schema it
//     is a SINGLE column per region (region.currency_code, an FK to the currency
//     table). A region cannot have two currencies, therefore the cart's currency
//     is not a CHOICE but a DERIVATION. The cost was not cosmetic — the currency
//     PICKS THE PRICE: the line item pricing flow reads the unit price out of the
//     variant's price set "in the cart's currency". A client that wrote USD into
//     a cart it opened in the TRY region was paying the price on the operator's
//     USD list.
//   - region_id. The region picks the tax RATE and it carries the currency too,
//     but the real defect is this: region_id is NOT what the customer wants to
//     express. The customer picks a COUNTRY (or their browser says it); the
//     region is that country's counterpart on the server and the mapping is set
//     up by the operator. Making the client write an INTERNAL ENTITY ID is a
//     softer form of the class "taking the server's data from the client". On top
//     of that a flow that already does the derivation existed (create_cart,
//     which resolves both the region and the currency from the country code) and
//     this endpoint SKIPPED it.
//
// The body REJECTS an unknown field ([decodeBody]), that is, an old client
// sending "region_id" or "currency_code" gets a 422. Silently ignoring them was
// not chosen: the client would believe it had sent them while the server opened
// the cart in another region — and that cart would be priced with another tax
// rate, from another price list.
//
// # On the ADMIN surface the same field is LEGITIMATE
//
// The currency appearing in the body is not a defect everywhere; the question is
// "is this value the caller's own data". The currency_code in the body of
// POST /admin/v1/regions DEFINES the region — there the operator writes not a
// copy but the ORIGINAL and there is no source to copy from. Here, on the other
// hand, the same field was a value the server already knew being repeated by the
// client. On cart's own admin surface the question never arises: /admin/v1/carts
// is READ ONLY and there is no admin endpoint that opens a cart.
type createCartRequest struct {
	// CountryCode is the customer's country (ISO 3166-1 alpha-2) and it is
	// MANDATORY; the cart's region and currency are derived from it.
	//
	// Its validation is not done HERE, it is done in the flow: an empty code gets
	// a 422, a malformed code gets a 422 as well (region's validation), and a
	// valid country that has no region gets a 404. A second check in the handler
	// would mean the same rule drifting apart in two places.
	CountryCode string `json:"country_code"`
	// CustomerID left empty means the cart belongs to a guest.
	//
	// The field is an OWNERSHIP CLAIM and today it asks for no proof at all; its
	// boundary is in the package documentation's section "What the model DOES NOT
	// COVER: customer_id".
	//
	// LEAVING the field EMPTY is a decision as well and its cost is written
	// there: the b2b spending limit is never applied to an order born of a cart
	// without a customer.
	CustomerID string `json:"customer_id"`
	Email      string `json:"email"`
	// Metadata is the cart's free-form extra data (campaign source, storefront
	// session).
	//
	// Unlike the region and the currency, this really is the CLIENT'S
	// information and it enters no computation; that is why it stays in the body
	// and is carried over to the flow as it is. The decision is the same one made
	// for the line item's metadata (see [addLineItemRequest]).
	Metadata map[string]any `json:"metadata"`
}

// storeCreateCart opens a new cart; the SERVER derives the region and the
// currency.
//
// The cart is opened not by this handler but by [CartOpening]: the flow resolves
// the region from the customer's country and the currency from the region,
// verifies a registered customer and writes the cart. If the flow cannot be
// resolved the cart is NOT opened at all (see [Handler.opening]).
//
// For the response the cart is READ BACK; the reasoning is in the [Handler.cart]
// godoc.
func (h *Handler) storeCreateCart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createCartRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	flow, err := h.opening()
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	metadata, err := encodeMetadata(body.Metadata)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	id, err := flow.OpenCartForCountry(ctx, body.CountryCode, body.CustomerID, body.Email, metadata)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	cart, err := h.cart(ctx, id)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toCartDTO(cart)})
}

// cart READS the opened cart BACK and returns it.
//
// # Why it is read back
//
// The flow gives back only the cart's id and that is deliberate: its surface is
// limited to primitive types (ADR 0006), it cannot carry the cart's rich record.
// BUILDING the record out of the few fields the flow returns was not an option
// either — the timestamps, the revision and the total fields are the cart's own
// data and a copy produced by hand would come out different from the real one on
// the very first read.
//
// If the cart cannot be found the error is Internal: a record written a moment
// ago being unreadable is not something the client can fix.
func (h *Handler) cart(ctx context.Context, cartID string) (models.Cart, error) {
	detail, err := h.svc.GetCart(ctx, cartID)
	if err != nil {
		if coreerrors.IsNotFound(err) {
			return models.Cart{}, coreerrors.Wrap(err, coreerrors.KindInternal, codeCartMissing,
				"the cart was opened but could not be read: %s", cartID)
		}
		return models.Cart{}, err
	}
	return detail.Cart, nil
}

// storeGetCart returns the cart with its children.
func (h *Handler) storeGetCart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	detail, err := h.svc.GetCart(ctx, cartID(r))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toCartDetailDTO(detail)})
}

// updateCartRequest is the body of POST /store/v1/carts/{id}.
type updateCartRequest struct {
	// Email is a pointer: an email NOT SENT in the body at all and an email meant
	// to be cleared are separate intents. Had the two been reduced to a single
	// empty string, every request that only wanted to hand the cart over to a
	// customer would silently delete the cart's email.
	Email *string `json:"email"`
	// CustomerID is the customer that takes over the guest cart; if it is left
	// empty the cart's customer is not touched.
	CustomerID string `json:"customer_id"`
}

// storeUpdateCart updates the cart's email and/or its customer.
//
// It exists in order to collect an email at the payment step and to hand a guest
// cart over to a customer who signs in. The endpoint is POST rather than PATCH:
// chi's routing does not branch on the body anyway and the other writes on the
// customer side are POST as well.
func (h *Handler) storeUpdateCart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body updateCartRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	cart, err := h.svc.UpdateCart(ctx, cartID(r), service.UpdateCartInput{
		Email:      body.Email,
		CustomerID: body.CustomerID,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toCartDTO(cart)})
}

// storeDeleteCart soft deletes the cart.
func (h *Handler) storeDeleteCart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.DeleteCart(ctx, cartID(r)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// addLineItemRequest is the body of POST /store/v1/carts/{id}/line-items.
//
// # THE PRICE AND THE TITLE ARE NOT HERE
//
// Both used to be in this body and both are the SERVER'S information:
//
//   - unit_price. The field's godoc said "it is optional; the final price is
//     written by the calculate_totals workflow", but that workflow was wired in
//     no setup; that is, the amount the client sent was the FINAL amount. The
//     storefront's identity is the publishable key and it sits in the browser,
//     therefore this was a "write your own price" endpoint everyone could reach.
//     The price now comes from the pricing module over [LinePricing].
//   - title. The line item's name is the CATALOG'S data; taking it from the
//     client meant a text with no relation to the product's real name showing up
//     in the cart and therefore in the order, on the invoice and on the picking
//     list. The title is now read by the flow from the Query layer (see the
//     flow's snapshot/catalog side).
//
// The body REJECTS an unknown field ([decodeBody]), that is, an old client does
// not silently fall back to the old behavior: a request sending "unit_price"
// gets a 422. Being breaking is deliberate — silent acceptance would bring the
// removed failure back.
type addLineItemRequest struct {
	VariantID string `json:"variant_id"`
	// Quantity is a pointer so that a quantity that is not sent and a quantity of
	// zero are told apart. A quantity of zero is invalid anyway, but the two
	// cases deserve DIFFERENT messages.
	Quantity *int64 `json:"quantity"`
	// Metadata is the line item's free-form extra data (gift note,
	// personalization).
	//
	// Unlike the price, this really is the CLIENT'S information and it enters no
	// computation; that is why it stays in the body and is carried over to the
	// flow as it is.
	Metadata map[string]any `json:"metadata"`
}

// storeAddLineItem adds a line item to the cart; the SERVER decides the price.
//
// The line item is written not by this handler but by [LinePricing]: the flow
// takes the variant's title from the catalog and its price from the pricing
// module, adds the line item to the cart and recomputes the cart's totals. If
// the pricer cannot be resolved the line item is NOT added at all
// (see [Handler.pricing]).
func (h *Handler) storeAddLineItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body addLineItemRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	if body.Quantity == nil {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest, "quantity is mandatory"))
		return
	}
	flow, err := h.pricing()
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	metadata, err := encodeMetadata(body.Metadata)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	id := cartID(r)
	lineID, err := flow.AddPricedLineItem(ctx, id, body.VariantID, *body.Quantity, metadata)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	item, err := h.lineItem(ctx, id, lineID)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toLineItemDTO(item)})
}

// updateLineItemRequest is the line item update body.
type updateLineItemRequest struct {
	// Quantity is a pointer; see [addLineItemRequest].
	Quantity *int64 `json:"quantity"`
}

// storeUpdateLineItem writes the line item's quantity and REPRICES the line
// item.
//
// The path that writes the quantity goes through the flow as well, because the
// quantity CAN CHANGE the price: pricing picks the unit price according to the
// quantity range (3 units and 5 units are different tiers). Had it been written
// straight to the service, the line item would stay with the new quantity but
// with the OLD tier's price and the cart's total would go stale.
//
// # A quantity of zero REMOVES the line item and returns 204
//
// A quantity of zero used to be a 422; the flow's translation of intent applies
// now (on every cart interface, taking the quantity selector down to zero means
// "remove this", see UpdateLineItem in the flow). In that case the endpoint
// returns a bodyless 204: presenting the record of a removed line item in the
// response would mean handing the client a resource that no longer exists.
func (h *Handler) storeUpdateLineItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body updateLineItemRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	if body.Quantity == nil {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest, "quantity is mandatory"))
		return
	}
	flow, err := h.pricing()
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	id, lineID := cartID(r), chi.URLParam(r, paramLineItemID)
	removed, err := flow.SetLineItemQuantity(ctx, id, lineID, *body.Quantity)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	if removed {
		corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
		return
	}

	item, err := h.lineItem(ctx, id, lineID)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toLineItemDTO(item)})
}

// lineItem READS the written line item out of the cart and returns it.
//
// # Why it is read back
//
// The flow gives back only the line item's id and that is deliberate: its
// surface is limited to primitive types, it cannot carry the cart's rich record.
// But that is not the real reason for the read: the line item's AMOUNTS (unit
// price, subtotal, discount, tax) are written in the computation pass that runs
// AFTER the line item is written. Presenting the momentary value returned by the
// flow would mean showing the customer a number different from the amount in the
// cart — and that at exactly the endpoint where we fixed the source of the price.
//
// If the line item cannot be found the error is Internal: a record written a
// moment ago being unreadable is not something the client can fix.
func (h *Handler) lineItem(ctx context.Context, cartID, lineID string) (models.LineItem, error) {
	detail, err := h.svc.GetCart(ctx, cartID)
	if err != nil {
		return models.LineItem{}, err
	}
	for i := range detail.Items {
		if detail.Items[i].ID == lineID {
			return detail.Items[i], nil
		}
	}
	return models.LineItem{}, coreerrors.Internal(codeLineItemMissing,
		"the line item was written but could not be found in the cart: %s (%s)", lineID, cartID)
}

// encodeMetadata converts the free-form extra data into the JSON the flow
// carries; an empty map returns nil.
//
// The conversion being necessary comes from the flow surface only being able to
// use primitive and stdlib types (ADR 0006): map[string]any is not this
// package's type, but its appearing in the flow's signature would mean binding
// the two ends of the boundary to the same Go type. An encoding failure is
// errors.Invalid — the body comes from the client and the only case that cannot
// be encoded is a value that cannot be turned into JSON.
func encodeMetadata(metadata map[string]any) (json.RawMessage, error) {
	if len(metadata) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"metadata could not be converted into JSON")
	}
	return raw, nil
}

// storeRemoveLineItem removes the line item from the cart.
func (h *Handler) storeRemoveLineItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.RemoveLineItem(ctx, cartID(r), chi.URLParam(r, paramLineItemID)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// completeCartRequest is the body of POST /store/v1/carts/{id}/complete.
//
// # Which field is HERE and why, which one is NOT and why
//
// Every field is a question of authority: what is put into the body is what the
// customer can decide.
//
//   - payment_provider_id IS THERE. Which provider the payment is made with is
//     the customer's choice and the server cannot have a default for it. It
//     raises no authority problem: an unknown name opens no capture, it brings
//     the flow down.
//   - payment_data IS THERE. It is free-form data passed to the provider as it
//     is (card token, return address); by definition it is the client's
//     information.
//   - expected_total IS THERE and is MANDATORY; the reasoning is in the field's
//     godoc.
//   - email IS NOT THERE. The cart's contact address is already held on the cart
//     and opening a second channel here would allow the order to be bound to an
//     address OTHER than the one visible on the cart. The handler reads it from
//     its own service — the flow's constraint of "the cart module's surface does
//     not publish the email" does not hold at this endpoint, because the owner of
//     the cart is this module.
//   - location_id IS NOT THERE. Which warehouse things ship out of is a shipping
//     decision and the flow makes it by asking the stock + shipping modules per
//     line item; letting the customer pick a warehouse would both leak the stock
//     topology and leave where the order ships from up to them.
type completeCartRequest struct {
	PaymentProviderID string `json:"payment_provider_id"`
	// PaymentData is passed to the provider as it is; it is optional.
	PaymentData json.RawMessage `json:"payment_data"`
	// ExpectedTotal is the grand total the customer APPROVED (minor unit); it is
	// MANDATORY.
	//
	// # Why it is mandatory
	//
	// The computation is REFRESHED at the beginning of the completion flow: a
	// price change in the catalog or an expiring promotion can separate the
	// amount the customer saw from the amount that will be charged. If the field
	// is sent, a difference produces errors.Conflict and NO side effect is
	// applied — the check runs before the saga's first step.
	//
	// It is a pointer and its absence is a 422: had it been left optional, every
	// client that forgot the field would silently switch the protection off. The
	// error class that recurs in this repository is exactly this — the rule is
	// defined, the place where it is applied does not exist.
	//
	// Zero means "do not compare" and is legitimate only for a cart that really
	// holds zero. It is NOT a security hole: a client that skips the comparison
	// still pays the amount the server computed, it merely loses the chance to
	// make its customer aware of the price change.
	ExpectedTotal *int64 `json:"expected_total"`
}

// completeCartFlowRequest is the schema of the JSON sent to the completion flow.
//
// The field names have to be EXACTLY the same as the schema on the flow side;
// because this module cannot import internal/workflows (ADR 0006), the fit is
// not checked by the compiler and can only be proven with an integration test
// (see internal/e2e).
type completeCartFlowRequest struct {
	CartID            string          `json:"cart_id"`
	PaymentProviderID string          `json:"payment_provider_id"`
	PaymentData       json.RawMessage `json:"payment_data,omitempty"`
	Email             string          `json:"email,omitempty"`
	ExpectedTotal     int64           `json:"expected_total"`
}

// completeCartFlowResult is the schema of the JSON returned from the completion
// flow.
type completeCartFlowResult struct {
	OrderID      string `json:"order_id"`
	CartID       string `json:"cart_id"`
	CurrencyCode string `json:"currency_code"`
	Amount       int64  `json:"amount"`
}

// completeCartDTO is the completed cart's outward representation.
//
// The response carries the order's ID and the captured amount, nothing else: the
// payment session and reservation ids are internal structure, and the warnings
// are the operator's business (the flow logs them). The order's detail is read
// with GET /store/v1/orders/{id}.
type completeCartDTO struct {
	OrderID      string `json:"order_id"`
	CartID       string `json:"cart_id"`
	CurrencyCode string `json:"currency_code"`
	Total        int64  `json:"total"`
}

// storeCompleteCart turns the cart into an order.
//
// # Why this endpoint is in THIS module
//
// The HTTP owner of the flows is the module (the pattern of ADR 0001): the owner
// of the cart's endpoints is the cart module, therefore the endpoint that closes
// the cart is here as well. The composition root only SETS the flow UP and
// leaves it to the container; no handler code goes there. The module does not
// know the concrete flow, it talks over the [CartCompletion] interface.
//
// # Why /complete
//
// The path is under the cart and carries a VERB: the endpoint creates no
// resource, it changes the cart's state (and a by-product of it is an order).
// The alternative would have been POST /store/v1/orders, but order creation is
// the order module's surface and that module does not know the cart; putting the
// endpoint there would move the information that produces the order from the
// cart to a place that owns none of it.
//
// # Why 200 and not 201
//
// The created resource (the order) is not THIS path's resource and this endpoint
// cannot give it an address; 201 is correct where it can show the address with a
// "Location" header. The response carries the order's id, the client reads it
// from the order endpoint.
//
// # The second call
//
// Because the cart is stamped as completed, the second call gets
// errors.Conflict; the flow's idempotency key also prevents a second execution
// on the same cart. That is why a client whose network drops repeating the
// request does not produce a second order.
func (h *Handler) storeCompleteCart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body completeCartRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	if body.ExpectedTotal == nil {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest,
			"expected_total is mandatory; the total approved by the customer has to be declared"))
		return
	}
	flow, err := h.checkout()
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	// The cart's contact address is read from OUR OWN service; it is not taken
	// from the client.
	id := cartID(r)
	detail, err := h.svc.GetCart(ctx, id)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	request, err := json.Marshal(completeCartFlowRequest{
		CartID:            id,
		PaymentProviderID: body.PaymentProviderID,
		PaymentData:       body.PaymentData,
		Email:             detail.Email,
		ExpectedTotal:     *body.ExpectedTotal,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, coreerrors.Wrap(err, coreerrors.KindInternal, codeInvalidRequest,
			"the order completion request could not be encoded"))
		return
	}

	response, err := flow.CompleteCartJSON(ctx, request)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	var result completeCartFlowResult
	if err := json.Unmarshal(response, &result); err != nil {
		corehttp.WriteError(ctx, w, coreerrors.Wrap(err, coreerrors.KindInternal, codeFlowResultInvalid,
			"the order completion result could not be decoded: %s", id))
		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: completeCartDTO{
		OrderID:      result.OrderID,
		CartID:       result.CartID,
		CurrencyCode: result.CurrencyCode,
		Total:        result.Amount,
	}})
}

// storeSetShippingAddress writes the cart's shipping address.
func (h *Handler) storeSetShippingAddress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body addressRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	addr, err := h.svc.SetShippingAddress(ctx, cartID(r), body.toInput())
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toAddressDTO(addr)})
}

// storeSetBillingAddress writes the cart's billing address.
func (h *Handler) storeSetBillingAddress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body addressRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	addr, err := h.svc.SetBillingAddress(ctx, cartID(r), body.toInput())
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toAddressDTO(addr)})
}

// addShippingMethodRequest is the shipping method creation body.
type addShippingMethodRequest struct {
	Name             string         `json:"name"`
	ShippingOptionID string         `json:"shipping_option_id"`
	Amount           int64          `json:"amount"`
	Data             map[string]any `json:"data"`
}

// storeAddShippingMethod adds a shipping method to the cart.
func (h *Handler) storeAddShippingMethod(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body addShippingMethodRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	method, err := h.svc.AddShippingMethod(ctx, cartID(r), service.AddShippingMethodInput{
		Name:             body.Name,
		ShippingOptionID: body.ShippingOptionID,
		Amount:           body.Amount,
		Data:             body.Data,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toShippingMethodDTO(method)})
}

// storeRemoveShippingMethod removes the shipping method from the cart.
func (h *Handler) storeRemoveShippingMethod(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.RemoveShippingMethod(ctx, cartID(r), chi.URLParam(r, paramMethodID)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}
