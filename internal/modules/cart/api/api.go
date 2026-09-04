// Package api is the HTTP surface of the cart module.
//
// There are two surfaces: the customer side (/store/v1/carts …) builds and
// changes the cart, the admin side (/admin/v1/carts) is READ ONLY. The cart is
// not changed from the admin panel; the only party that changes the cart is the
// customer and order corrections are the job of the order module in Phase 6.
//
// # Surfaces not opened to HTTP
//
// [service.Service.SetTotals] and [service.Service.MarkCompleted] DELIBERATELY
// get no route. Both are a workflow surface: calculate_totals, which computes
// the totals, and complete_cart, which closes the cart, resolve them from the
// container and call them (ADR 0006). Had they been opened to HTTP, a client
// could write the cart's amount itself or close the cart without paying.
//
// # Endpoints delegated to a flow
//
// Every WRITING endpoint of the storefront does its job NOT WITH ITS OWN
// SERVICE but with a FLOW resolved by name from the container: opening a cart
// ([CartOpening]), adding a line item and updating a line item's quantity
// ([LinePricing]), completing the cart ([CartCompletion]). The reason in a
// single sentence is this — these endpoints working correctly depends on data
// the cart module DOES NOT KNOW (region, currency, catalog title, price, tax,
// stock, payment) and that data only comes together in a cross-module flow.
//
// The module being the HTTP owner of the flows is a deliberate decision: the
// URLs stay under the cart, no handler code enters the composition root and the
// module knows not the concrete flow but only the narrow interface it defines
// itself (the pattern of ADR 0001; the same one is used in the order module's
// b2b spending rule too).
//
// # This package no longer resolves ANY MODULE surface
//
// The cart-opening endpoint worked differently for a while: it took the region
// from the body and resolved the region module's surface by name in order to
// read the currency from that region as well. It had two defects. First,
// region_id is NOT what the customer expresses — the customer picks a COUNTRY
// and the region is its counterpart on the server; second, while a flow that
// already does the derivation (create_cart) existed, the endpoint SKIPPED it.
// Both were closed by delegating to the flow, and this package's only tie to
// region fell away with them: the party that knows the region is the flow now.
//
// # Scopes
//
// The endpoints under /admin/v1 ask for a scope SEPARATELY from identity:
//
//   - [ScopeRead] ("cart:read") — opens the GET endpoints.
//   - [ScopeWrite] ("cart:write") — would open the write endpoints; because
//     cart's admin surface is read only, it is bound to no route today.
//
// corehttp.ScopeAdmin ("admin") is the SUPERIOR SCOPE and satisfies both of
// them; it does not have to be granted separately to a fully privileged
// identity.
//
// The /store/v1 endpoints ask for NO scope: the store surface's identity is the
// publishable key and that key by definition carries no scope.
//
// # OWNERSHIP on storefront carts
//
// No endpoint under /store/v1/carts/{id} verifies that the requester is the
// owner of that cart. This is NOT AN OVERSIGHT but a chosen model: the cart's id
// is the CAPABILITY itself (a "capability URL"). The id is produced from a
// 48-bit timestamp + 80 bits of cryptographic randomness
// (see models.NewCartID); it is unguessable, therefore KNOWING it means
// carrying the right of access to the cart.
//
// The model is common in headless commerce and here it also arises out of
// necessity: the store surface's only identity today is the publishable key and
// that key is NOT A SECRET — it sits in the browser, its only job is to bind the
// request to a sales channel (see corehttp.RequireStore). There is no customer
// SESSION, that is, there is no subject to ask "is this cart yours" either. The
// same declaration is written in the order module too (order/api storeGetOrder)
// and real authorization is the job of Phase 8.
//
// The model has RULES that are not free, and this package obeys them:
//
//   - There is NO LIST endpoint on the storefront side. A list endpoint would
//     turn knowing a single id into reading ALL carts; listing is only under
//     /admin/v1 and asks for [ScopeRead].
//   - The cart id has to be carried like a SECRET. Its leaking into a log, into
//     the Referer header or into third-party scripts is the leaking of the
//     access itself.
//
// # What the model DOES NOT COVER: customer_id
//
// The capability URL says "I can reach the id I hold"; it DOES NOT say "I am
// this customer". Yet the bodies of POST /store/v1/carts and
// POST /store/v1/carts/{id} take a customer_id and ask for no proof at all. The
// service guards only ONE boundary: a cart that has a customer cannot be handed
// over to another customer (service.CodeCustomerMismatch). The remaining two
// doors are open — the caller can write somebody else's customer id into the new
// cart it opens, and it can hand a GUEST cart whose id it knows over to any
// customer it likes.
//
// The consequence is not cosmetic: the cart's customer determines the order's
// owner and the b2b spending limit is deducted from THAT customer's company
// window (see the order module's spending rule). That is, the claim can consume
// somebody else's limit window. It was measured on a real pair: the checkout a
// stranger completed was deducted from the target's window and AFTERWARDS that
// customer's own checkout got a 409 — that is, the claim is not merely an escape
// but a way to BURN the spending right of an employee whose name is known.
//
// The third door goes underneath both of the others and is the cheapest one: NOT
// SENDING the field AT ALL. A cart without a customer belongs to a guest, on a
// guest order the spending rule is not even ASKED and the limit is never
// applied. That is, the only thing an employee who has hit their limit has to do
// is leave a field out of the body. Saying "declare your identity" does not
// close it either: POST /store/v1/customers opens a fresh guest record bound to
// no company with the publishable key.
//
// Unguessability DOES NOT CLOSE this, because the thing being guarded is not a
// capability in the caller's hand but a claim made ABOUT SOMEBODY ELSE. The only
// correct closure is a customer session (Phase 8): customer_id stops being taken
// from the body and is read from the verified identity. That mechanism DOES NOT
// EXIST today and this package does not try to invent it; the decision taken is
// that the hole is WRITTEN DOWN — an unwritten security model is a security
// model that does not exist.
//
// Where the responsibility sits is settled in ADR 0008: verifying the identity
// is the job not of the framework but of the EMBEDDING APPLICATION, and under
// which condition the spending limit is applied (only on a checkout that
// DECLARES its customer) is written in the order module's spendingRuleFor godoc
// and in the README's B2B section.
//
// Handlers do NOT PICK the status code: the service returns its core/errors
// typed error and corehttp.WriteError writes the code matching its kind
// (plan Section 8).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// maxBodyBytes is the upper bound for the request body. Without a bound a
// single request could exhaust the server's memory.
const maxBodyBytes int64 = 1 << 20 // 1 MiB

// codeInvalidRequest is the error code returned when the body/parameter cannot
// be parsed.
const codeInvalidRequest = "cart_invalid_request"

// codeFlowUnavailable reports that a flow is not bound to the handler.
//
// There is nothing the client can fix; the code names a SETUP failure and is in
// the Internal kind for that reason.
const codeFlowUnavailable = "cart_workflow_unavailable"

// codeLineItemMissing reports that the written line item could not be read back
// right afterwards.
const codeLineItemMissing = "cart_line_item_missing"

// codeCartMissing reports that the opened cart could not be read back right
// afterwards.
//
// It is a code SEPARATE from [codeLineItemMissing] because the record that went
// missing is different and so is the place the operator will look: one looks for
// the cart, the other for the line item. Had the two been reduced to a single
// code, the diagnosis could not be made without reading the log.
const codeCartMissing = "cart_missing_after_create"

// codeFlowResultInvalid reports that the body returned from the flow could not
// be decoded.
//
// The two ends of the contract cannot import each other (ADR 0006), that is,
// when a field name drifts the compiler stays silent; this code breaks that
// silence at run time.
const codeFlowResultInvalid = "cart_workflow_result_invalid"

// URL parameter names.
const (
	paramCartID     = "id"
	paramLineItemID = "line_item_id"
	paramMethodID   = "shipping_method_id"
)

// Carts is the surface the handlers need from the service.
//
// Keeping it narrow simplifies the tests: the HTTP behavior can be verified with
// a fake of a few hundred lines, without a real database. There is NO SetTotals
// and no MarkCompleted on the surface; both are the workflow surface that is not
// opened to HTTP.
//
// CreateCart is NOT there either and the reason is a different one: the
// cart-opening endpoint calls not the service but the [CartOpening] flow (the
// region is derived from the country there) and [Carts.GetCart] is already
// enough to turn the written cart into a response. Keeping the method on the
// surface would leave open a door through which the handler could SKIP the flow
// and write the cart directly.
//
// AddLineItem is absent FOR THE SAME REASON and that is not a gap: the endpoint
// that adds a line item calls the [LinePricing] flow, because the SERVER decides
// the price and the flow applies the CEILING on the cart's line count
// (MaxLineItems inside workflows/cart). Had the method stayed on the surface, a
// handler bound to it would SILENTLY skip both the pricing and the ceiling; the
// service method itself is still there, the flow calls it.
type Carts interface {
	// GetCart returns the cart with its children.
	GetCart(ctx context.Context, cartID string) (models.CartDetail, error)
	// UpdateCart updates the cart's email and customer fields.
	UpdateCart(ctx context.Context, cartID string, in service.UpdateCartInput) (models.Cart, error)
	// ListCarts pages the carts.
	ListCarts(ctx context.Context, in service.ListCartsInput) ([]models.Cart, int64, error)
	// DeleteCart soft deletes the cart.
	DeleteCart(ctx context.Context, cartID string) error

	// UpdateLineItemQuantity writes the line item's quantity.
	UpdateLineItemQuantity(ctx context.Context, cartID, lineID string, quantity int64) (models.LineItem, error)
	// RemoveLineItem removes the line item.
	RemoveLineItem(ctx context.Context, cartID, lineID string) error

	// SetShippingAddress writes the cart's shipping address.
	SetShippingAddress(ctx context.Context, cartID string, in service.AddressInput) (models.CartAddress, error)
	// SetBillingAddress writes the cart's billing address.
	SetBillingAddress(ctx context.Context, cartID string, in service.AddressInput) (models.CartAddress, error)

	// AddShippingMethod adds a shipping method to the cart.
	AddShippingMethod(ctx context.Context, cartID string, in service.AddShippingMethodInput) (models.ShippingMethod, error)
	// RemoveShippingMethod removes the shipping method.
	RemoveShippingMethod(ctx context.Context, cartID, methodID string) error
}

// CartOpening is the surface used by this package of the flow that OPENS the
// cart (ADR 0001/0006).
//
// # Why it is defined here
//
// The concrete flow is in internal/workflows/cart and this module CANNOT import
// it (ADR 0006 holds in both directions). The pattern is the same as
// [LinePricing]'s: the interface is defined on the CONSUMER side and only with
// primitive types, the concrete type satisfies it STRUCTURALLY and it is
// resolved from the container BY NAME.
//
// # Why there is NO REGION parameter
//
// The whole surface exists for this gap and the gap is the same as the price gap
// in [LinePricing]. The cart's region is derived from the customer's COUNTRY:
// the customer picks a country (or their browser says it) and the region is its
// counterpart on the server. Putting a "regionID" parameter here would mean
// making the client write an INTERNAL ENTITY ID and leaving the cart's tax rate
// to its choice — and because the currency was read from that same region, that
// was at one point exactly what happened.
type CartOpening interface {
	// OpenCartForCountry opens the cart and returns the cart's ID.
	//
	// The region and the currency are decided ON THE SERVER: both are resolved
	// from countryCode by the flow. If customerID is left empty the cart belongs
	// to a guest; metadata is the free-form JSON object the caller attaches to
	// the cart and it can be left empty.
	//
	// If the country has no region it returns errors.NotFound, if the code is
	// malformed errors.Invalid; both are the region module's error and pass
	// through as they are.
	OpenCartForCountry(
		ctx context.Context,
		countryCode, customerID, email string,
		metadata json.RawMessage,
	) (cartID string, err error)
}

// LinePricing is the surface used by this package of the flow that decides the
// line item's price ON THE SERVER (ADR 0001/0006).
//
// # Why it is defined here
//
// The concrete flow is in internal/workflows/cart and this module CANNOT import
// it (ADR 0006 holds in both directions). This interface, defined on the
// consumer side, is satisfied STRUCTURALLY by the concrete type resolved from
// the container BY NAME; the fit is checked not by the compiler but by the first
// resolution attempt.
//
// # Why there is NO price parameter
//
// The whole surface exists for this gap. The price is decided by the flow out of
// the variant's price set and the cart's currency; putting a "unitPrice"
// parameter here would mean rebuilding the removed failure one layer down.
type LinePricing interface {
	// AddPricedLineItem adds a line item to the cart and returns the line item's
	// id.
	//
	// The price and the title are decided ON THE SERVER: the price comes from
	// pricing, the title from the catalog. metadata is the free-form JSON object
	// the caller attaches to the line item and it can be left empty.
	AddPricedLineItem(
		ctx context.Context,
		cartID, variantID string,
		quantity int64,
		metadata json.RawMessage,
	) (lineItemID string, err error)

	// SetLineItemQuantity writes the line item's quantity as an ABSOLUTE value,
	// recomputes the totals and reports whether the line item was REMOVED.
	//
	// A zero quantity removes the line item; a negative quantity is rejected.
	SetLineItemQuantity(
		ctx context.Context,
		cartID, lineItemID string,
		quantity int64,
	) (removed bool, err error)
}

// CartCompletion is the surface used by this package of the flow that turns the
// cart into an order (ADR 0001/0006).
//
// The signature is JSON because both the flow's input and its output are
// COMPOSITE and the two sides cannot name each other's types; the schema is
// written in one place, in the [completeCartFlowRequest] and
// [completeCartFlowResult] types.
type CartCompletion interface {
	// CompleteCartJSON turns the cart into an order: stock is reserved, the order
	// is opened, the payment is authorized and captured and the cart is closed.
	CompleteCartJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error)
}

// Flows is the set of surfaces the handler needs from the flows.
//
// All three are MANDATORY and their absence produces an error at run time
// (see [Handler.opening], [Handler.pricing] and [Handler.checkout]); not binding
// the routes at all was not an option, because the flows are set up AFTER the
// modules and may not be registered yet when Routes is called.
type Flows struct {
	// Opening is the cart-opening flow.
	Opening CartOpening
	// Pricing is the line item pricing flow.
	Pricing LinePricing
	// Checkout is the cart-completion flow.
	Checkout CartCompletion
}

// Handler is the cart module's set of HTTP handlers.
type Handler struct {
	svc   Carts
	flows Flows
}

// New produces the set of handlers working on the given service and flows.
//
// There used to be a third parameter as well (the region surface) and the cart's
// currency was read from it; today that derivation is done by the cart-opening
// FLOW, that is, the only outside party the handler knows is [Flows].
func New(svc Carts, flows Flows) *Handler {
	return &Handler{svc: svc, flows: flows}
}

// opening returns the cart-opening flow; if it is not bound it returns an ERROR.
//
// # Why it fails CLOSED
//
// The reasoning runs in the same direction as [Handler.pricing]'s: if the flow
// is missing, the correct answer is NOT "a cart without a region" or "the region
// the client says". The cart's region picks the tax rate and the currency
// derived from it picks which price list is applied; dropping either to a
// default would reopen exactly the privilege door that was closed. If the flow
// cannot be resolved, the cart is NOT OPENED AT ALL.
func (h *Handler) opening() (CartOpening, error) {
	if h.flows.Opening == nil {
		return nil, coreerrors.Internal(codeFlowUnavailable,
			"the cart-opening flow is not bound; a cart cannot be opened without the server deriving the region")
	}
	return h.flows.Opening, nil
}

// pricing returns the line item pricing flow; if it is not bound it returns an
// ERROR.
//
// # Why it fails CLOSED
//
// This is the OPPOSITE of the order module's spending limit rule and the
// difference is deliberate. There, if the provider is missing the correct answer
// is "no limit": in a store where b2b is not set up there is no such concept as
// a spending limit and carrying on without the rule is the setup's own decision.
// Here, however, if the provider is missing the correct answer is NOT "no
// price" — writing a line item without a price (neither with the amount the
// client gave, nor with zero) would be silently selling goods for free. The only
// correct outcome of a missing pricer is the line item NOT BEING ADDED AT ALL.
func (h *Handler) pricing() (LinePricing, error) {
	if h.flows.Pricing == nil {
		return nil, coreerrors.Internal(codeFlowUnavailable,
			"the line pricing flow is not bound; a line item cannot be added without the server deciding the price")
	}
	return h.flows.Pricing, nil
}

// checkout returns the cart-completion flow; if it is not bound it returns an
// ERROR.
//
// The reasoning runs in the same direction as [Handler.pricing]'s but is even
// plainer: if the flow is missing there is no order, no payment and no stock
// reservation either, and there can be no shortcut called "consider the cart
// completed".
func (h *Handler) checkout() (CartCompletion, error) {
	if h.flows.Checkout == nil {
		return nil, coreerrors.Internal(codeFlowUnavailable,
			"the cart-completion flow is not bound; the cart cannot be turned into an order")
	}
	return h.flows.Checkout, nil
}

// --- envelopes and DTOs ------------------------------------------------------

// singleEnvelope is the envelope of single responses (plan Section 8).
type singleEnvelope struct {
	// Data is the response's body.
	Data any `json:"data"`
}

// listEnvelope is the envelope of list responses (plan Section 8).
type listEnvelope struct {
	// Data is the records on the page.
	Data any `json:"data"`
	// Count is the number of ALL records matching the filter; not the number of
	// rows on the page.
	Count int64 `json:"count"`
	// Offset is the number of skipped records.
	Offset int64 `json:"offset"`
	// Limit is the requested page size.
	Limit int64 `json:"limit"`
}

// cartDTO is the cart's outward representation.
//
// TotalsStale is a derived field and is presented TOGETHER with the totals: a
// stale amount being taken for a correct one would be the most expensive mistake
// this API could produce.
type cartDTO struct {
	ID            string         `json:"id"`
	RegionID      string         `json:"region_id"`
	CustomerID    string         `json:"customer_id,omitempty"`
	Email         string         `json:"email,omitempty"`
	CurrencyCode  string         `json:"currency_code"`
	Subtotal      int64          `json:"subtotal"`
	DiscountTotal int64          `json:"discount_total"`
	TaxTotal      int64          `json:"tax_total"`
	ShippingTotal int64          `json:"shipping_total"`
	Total         int64          `json:"total"`
	TotalsStale   bool           `json:"totals_stale"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	CompletedAt   *time.Time     `json:"completed_at,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// cartDetailDTO is the cart's outward representation together with its children.
type cartDetailDTO struct {
	cartDTO
	Items           []lineItemDTO       `json:"items"`
	ShippingAddress *addressDTO         `json:"shipping_address,omitempty"`
	BillingAddress  *addressDTO         `json:"billing_address,omitempty"`
	ShippingMethods []shippingMethodDTO `json:"shipping_methods"`
}

// lineItemDTO is the cart line item's outward representation.
type lineItemDTO struct {
	ID            string         `json:"id"`
	CartID        string         `json:"cart_id"`
	VariantID     string         `json:"variant_id"`
	Title         string         `json:"title"`
	Quantity      int64          `json:"quantity"`
	UnitPrice     int64          `json:"unit_price"`
	Subtotal      int64          `json:"subtotal"`
	DiscountTotal int64          `json:"discount_total"`
	TaxTotal      int64          `json:"tax_total"`
	Total         int64          `json:"total"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// addressDTO is the cart address's outward representation.
type addressDTO struct {
	ID              string         `json:"id"`
	CartID          string         `json:"cart_id"`
	Type            string         `json:"type"`
	SourceAddressID string         `json:"source_address_id,omitempty"`
	FirstName       string         `json:"first_name,omitempty"`
	LastName        string         `json:"last_name,omitempty"`
	Company         string         `json:"company,omitempty"`
	Address1        string         `json:"address_1,omitempty"`
	Address2        string         `json:"address_2,omitempty"`
	City            string         `json:"city,omitempty"`
	Province        string         `json:"province,omitempty"`
	PostalCode      string         `json:"postal_code,omitempty"`
	CountryCode     string         `json:"country_code,omitempty"`
	Phone           string         `json:"phone,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

// shippingMethodDTO is the shipping method's outward representation.
type shippingMethodDTO struct {
	ID               string         `json:"id"`
	CartID           string         `json:"cart_id"`
	Name             string         `json:"name"`
	ShippingOptionID string         `json:"shipping_option_id,omitempty"`
	Amount           int64          `json:"amount"`
	Data             map[string]any `json:"data,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

// toCartDTO converts the model into the outward representation.
func toCartDTO(cart models.Cart) cartDTO {
	return cartDTO{
		ID:            cart.ID,
		RegionID:      cart.RegionID,
		CustomerID:    cart.CustomerID,
		Email:         cart.Email,
		CurrencyCode:  cart.CurrencyCode,
		Subtotal:      cart.Subtotal,
		DiscountTotal: cart.DiscountTotal,
		TaxTotal:      cart.TaxTotal,
		ShippingTotal: cart.ShippingTotal,
		Total:         cart.Total,
		TotalsStale:   cart.TotalsStale(),
		Metadata:      cart.Metadata,
		CompletedAt:   cart.CompletedAt,
		CreatedAt:     cart.CreatedAt,
		UpdatedAt:     cart.UpdatedAt,
	}
}

// toCartDetailDTO converts the cart with its children into the outward
// representation.
func toCartDetailDTO(detail models.CartDetail) cartDetailDTO {
	out := cartDetailDTO{
		cartDTO:         toCartDTO(detail.Cart),
		Items:           make([]lineItemDTO, 0, len(detail.Items)),
		ShippingMethods: make([]shippingMethodDTO, 0, len(detail.ShippingMethods)),
	}
	// The loops are walked by index: the line item and method structs are large
	// and copying them by value would carry a few hundred bytes needlessly on
	// every turn.
	for i := range detail.Items {
		out.Items = append(out.Items, toLineItemDTO(detail.Items[i]))
	}
	for i := range detail.ShippingMethods {
		out.ShippingMethods = append(out.ShippingMethods, toShippingMethodDTO(detail.ShippingMethods[i]))
	}
	if detail.ShippingAddress != nil {
		addr := toAddressDTO(*detail.ShippingAddress)
		out.ShippingAddress = &addr
	}
	if detail.BillingAddress != nil {
		addr := toAddressDTO(*detail.BillingAddress)
		out.BillingAddress = &addr
	}
	return out
}

// toLineItemDTO converts the model into the outward representation.
func toLineItemDTO(item models.LineItem) lineItemDTO {
	return lineItemDTO{
		ID:            item.ID,
		CartID:        item.CartID,
		VariantID:     item.VariantID,
		Title:         item.Title,
		Quantity:      item.Quantity,
		UnitPrice:     item.UnitPrice,
		Subtotal:      item.Subtotal,
		DiscountTotal: item.DiscountTotal,
		TaxTotal:      item.TaxTotal,
		Total:         item.Total,
		Metadata:      item.Metadata,
		CreatedAt:     item.CreatedAt,
		UpdatedAt:     item.UpdatedAt,
	}
}

// toAddressDTO converts the model into the outward representation.
func toAddressDTO(addr models.CartAddress) addressDTO {
	return addressDTO{
		ID:              addr.ID,
		CartID:          addr.CartID,
		Type:            addr.Type.String(),
		SourceAddressID: addr.SourceAddressID,
		FirstName:       addr.FirstName,
		LastName:        addr.LastName,
		Company:         addr.Company,
		Address1:        addr.Address1,
		Address2:        addr.Address2,
		City:            addr.City,
		Province:        addr.Province,
		PostalCode:      addr.PostalCode,
		CountryCode:     addr.CountryCode,
		Phone:           addr.Phone,
		Metadata:        addr.Metadata,
		CreatedAt:       addr.CreatedAt,
		UpdatedAt:       addr.UpdatedAt,
	}
}

// toShippingMethodDTO converts the model into the outward representation.
func toShippingMethodDTO(method models.ShippingMethod) shippingMethodDTO {
	return shippingMethodDTO{
		ID:               method.ID,
		CartID:           method.CartID,
		Name:             method.Name,
		ShippingOptionID: method.ShippingOptionID,
		Amount:           method.Amount,
		Data:             method.Data,
		CreatedAt:        method.CreatedAt,
		UpdatedAt:        method.UpdatedAt,
	}
}

// --- helpers -----------------------------------------------------------------

// decodeBody decodes the request body.
//
// The body size is limited and UNKNOWN FIELDS are rejected: a silently swallowed
// field means a setting the client believes it sent but that is never applied.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return coreerrors.Invalid(codeInvalidRequest, "request body cannot be empty")
		}
		return coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"request body could not be parsed")
	}
	// More than a single JSON value having been sent is a client error as well.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return coreerrors.Invalid(codeInvalidRequest,
			"request body has to be a single JSON object")
	}
	return nil
}

// parsePage decodes the limit/offset query parameters.
func parsePage(r *http.Request) (service.Page, error) {
	limit, err := parseInt64Param(r, "limit")
	if err != nil {
		return service.Page{}, err
	}
	offset, err := parseInt64Param(r, "offset")
	if err != nil {
		return service.Page{}, err
	}
	page := service.Page{Limit: limit, Offset: offset}
	if page.Limit == 0 {
		// So that the response's limit field really shows the bound that is
		// applied, the default is made visible here as well.
		page.Limit = service.DefaultLimit
	}
	return page, nil
}

// parseInt64Param converts a query parameter into an integer; if it is absent it
// returns 0.
func parseInt64Param(r *http.Request, name string) (int64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"%s has to be an integer: %q", name, raw)
	}
	return value, nil
}

// addressRequest is the common body of the shipping and billing endpoints.
type addressRequest struct {
	SourceAddressID string         `json:"source_address_id"`
	FirstName       string         `json:"first_name"`
	LastName        string         `json:"last_name"`
	Company         string         `json:"company"`
	Address1        string         `json:"address_1"`
	Address2        string         `json:"address_2"`
	City            string         `json:"city"`
	Province        string         `json:"province"`
	PostalCode      string         `json:"postal_code"`
	CountryCode     string         `json:"country_code"`
	Phone           string         `json:"phone"`
	Metadata        map[string]any `json:"metadata"`
}

// toInput converts the body into the service input.
func (b addressRequest) toInput() service.AddressInput {
	return service.AddressInput{
		SourceAddressID: b.SourceAddressID,
		FirstName:       b.FirstName,
		LastName:        b.LastName,
		Company:         b.Company,
		Address1:        b.Address1,
		Address2:        b.Address2,
		City:            b.City,
		Province:        b.Province,
		PostalCode:      b.PostalCode,
		CountryCode:     b.CountryCode,
		Phone:           b.Phone,
		Metadata:        b.Metadata,
	}
}

// cartID reads the cart id from the request.
func cartID(r *http.Request) string {
	return chi.URLParam(r, paramCartID)
}
