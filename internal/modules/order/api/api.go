// Package api is the HTTP surface of the order module.
//
// There are two surfaces: the admin side (/admin/v1/orders …) reads the order
// and applies status transitions, the customer side (/store/v1/orders/{id})
// ONLY READS.
//
// # Scopes
//
// The admin endpoints ASK FOR a scope and the scope is enforced endpoint by
// endpoint (see [Handler.Routes]):
//
//   - [ScopeRead] ("order:read") — opens the GET endpoints under /admin/v1:
//     the order list and a single order, return/exchange/claim records.
//   - [ScopeWrite] ("order:write") — opens the POST endpoints under /admin/v1:
//     cancel, complete, archive and creating after-sales records.
//
// corehttp.ScopeAdmin ("admin") is the SUPER SCOPE; on its own it satisfies
// both of them (see corehttp.Principal.HasScope).
//
// No scope IS ADDED to the storefront endpoint: the identity of /store/v1 is
// the publishable key and that key by definition carries no scope.
//
// # Surfaces not opened to HTTP
//
// [service.Service.CreateOrder] DELIBERATELY gets no route. An order is a
// record whose amounts are supplied from outside: had it been opened to HTTP, a
// client could have written an order with a total it determined itself — with a
// total of zero, for example. The validation layers only ensure that the input
// is consistent WITHIN ITSELF, not that the amounts correspond to the REAL
// prices; the only thing that provides that guarantee is the complete_cart
// workflow, which builds the snapshot from the cart and from pricing (ADR
// 0006). This is why an order is opened only through "order.interop".
//
// [service.Service.SetOrderSummaryTotals] gets no route for the same reason:
// the side that knows the amount paid is the payment flow, not the client.
//
// Creating an order by hand from the admin side (draft order) is the work of
// later phases and when it arrives it has to arrive with its own validation
// chain.
//
// Handlers DO NOT CHOOSE the status code: the service returns its core/errors
// typed error and corehttp.WriteError writes the code matching its kind (plan
// Section 8).
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

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	corepage "github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// maxBodyBytes is the upper limit for the request body. Without a limit a
// single request could exhaust the server's memory.
const maxBodyBytes int64 = 1 << 20 // 1 MiB

// codeInvalidRequest is the error code returned when the body or a parameter
// cannot be parsed.
const codeInvalidRequest = "order_invalid_request"

// codeFlowUnavailable reports that a flow the endpoint needs is not bound.
const codeFlowUnavailable = "order_workflow_unavailable"

// codeOrderPaymentUnbound reports that no payment collection is bound to the
// order.
//
// It is SEPARATE from the order's own not-found code on purpose: a client that
// cannot tell "there is no such order" from "this order has no payment" would
// treat a checkout that died mid-flight as a missing order.
const codeOrderPaymentUnbound = "order_payment_unbound"

// URL parameter names.
const (
	// paramOrderID is the URL parameter name of the order id.
	paramOrderID = "id"
	// paramReturnID is the URL parameter name of the return record id.
	paramReturnID = "returnId"
	// paramExchangeID is the URL parameter name of the exchange record id.
	paramExchangeID = "exchangeId"
	// paramClaimID is the URL parameter name of the claim record id.
	paramClaimID = "claimId"
	// paramEvidenceID is the claim evidence in the path.
	paramEvidenceID = "evidenceId"
	// paramFulfillmentID is the parcel an addition joins (ADR 0197).
	paramFulfillmentID = "fulfillmentId"
	// paramShippingMethodID is the delivery a change applies to (ADR 0199).
	paramShippingMethodID = "shippingMethodId"
)

// Orders is the surface the handlers need from the service.
//
// Keeping it narrow simplifies the tests: HTTP behavior can be verified with a
// fake of a few hundred lines, without a real database. CreateOrder and
// SetOrderSummaryTotals are NOT on the surface; both of them are the workflow
// surface that is not opened to HTTP (see the package documentation).
type Orders interface {
	// GetOrder returns the order with its line items and summary.
	GetOrder(ctx context.Context, orderID string) (models.OrderDetail, error)
	// ListOrders pages the orders.
	ListOrders(ctx context.Context, in service.ListOrdersInput) (service.OrderPage, error)
	// CancelPlacedOrder cancels an order the checkout placed and writes off
	// what was not delivered; it is idempotent (ADR 0285).
	CancelPlacedOrder(ctx context.Context, orderID, reason string) error
	// CompleteOrder completes the order.
	CompleteOrder(ctx context.Context, orderID string) (models.Order, error)
	// Timeline is everything that happened to the order, newest first.
	Timeline(ctx context.Context, orderID string) ([]service.TimelineEntry, error)
	// StorefrontTimeline returns the moments a CUSTOMER may see on their order.
	StorefrontTimeline(ctx context.Context, orderID string) ([]service.TimelineEntry, error)
	// OrderAsOf reads the order as it stood at a past moment (ADR 0171).
	OrderAsOf(ctx context.Context, orderID string, at time.Time) (models.OrderAsOf, error)
	// Journal derives the module's books over a window (ADR 0188).
	Journal(ctx context.Context, q service.JournalQuery) (service.Journal, error)
	// AttachClaimEvidence binds a file to the claim.
	AttachClaimEvidence(ctx context.Context, claimID string, in service.AttachClaimEvidenceInput) (models.ClaimEvidence, error)
	// ListClaimEvidence returns the claim's evidence, oldest first.
	ListClaimEvidence(ctx context.Context, claimID string) ([]models.ClaimEvidence, error)
	// DetachClaimEvidence removes a file from its claim.
	DetachClaimEvidence(ctx context.Context, evidenceID string) error
	// CreateCreditLine writes off part of what the order owes.
	CreateCreditLine(ctx context.Context, orderID string, in service.CreateCreditLineInput) (models.OrderCreditLine, error)
	// ListCreditLines returns the order's credit lines, oldest first.
	ListCreditLines(ctx context.Context, orderID string) ([]models.OrderCreditLine, error)
	// CancelOrderLine writes off units of one line of a live order.
	CancelOrderLine(
		ctx context.Context, orderID string, in service.CancelOrderLineInput,
	) (models.OrderLineCancellation, error)
	// ListLineCancellations returns the order's line cancellations, oldest first.
	ListLineCancellations(
		ctx context.Context, orderID string,
	) ([]models.OrderLineCancellation, error)
	// PaymentOf returns the LIVE payment collection bound to the order; the
	// second value reports whether one is bound at all.
	PaymentOf(ctx context.Context, orderID string) (service.OrderPayment, bool, error)
	// ArchiveOrder archives a completed order.
	ArchiveOrder(ctx context.Context, orderID string) (models.Order, error)

	// CreateReturnRecord opens a return record on the order and answers it
	// with its lines and what its units were sold for, from the write itself
	// (ADR 0433).
	CreateReturnRecord(ctx context.Context, in service.CreateReturnInput) (service.ReturnRecord, error)
	// GetReturn returns the return record by its id.
	GetReturn(ctx context.Context, returnID string) (models.Return, error)
	// ListReturns pages the order's return records.
	ListReturns(ctx context.Context, orderID string, page service.Page) ([]models.Return, int64, error)
	// CancelReturn withdraws the return request.
	//
	// It is on this surface because it is the only thing that RELEASES the
	// units a request is holding against the order's lines; what that costs
	// while nothing calls it is in [Handler.adminCancelReturn].
	CancelReturn(ctx context.Context, returnID string) (models.Return, error)
	// ReturnsWithLines fills the returns' lines and says what each one's
	// units were sold for, in two reads however many are given (ADR 0433).
	ReturnsWithLines(ctx context.Context, returns []models.Return) ([]service.ReturnRecord, error)

	// CreateExchange opens an exchange record on the order.
	CreateExchange(ctx context.Context, in service.CreateExchangeInput) (models.Exchange, error)
	// GetExchange returns the exchange record by its id.
	GetExchange(ctx context.Context, exchangeID string) (models.Exchange, error)
	// ListExchanges pages the order's exchange records.
	ListExchanges(ctx context.Context, orderID string, page service.Page) ([]models.Exchange, int64, error)
	// CancelExchange withdraws the exchange request.
	CancelExchange(ctx context.Context, exchangeID string) (models.Exchange, error)
	// ListReplacementsOfExchange returns an exchange's replacements, newest
	// first.
	ListReplacementsOfExchange(
		ctx context.Context, exchangeID string,
	) ([]models.Replacement, error)

	// CreateClaim opens a claim record on the order.
	CreateClaim(ctx context.Context, in service.CreateClaimInput) (models.Claim, error)

	// CreateReplacement records what a claim will send; it sends nothing.
	CreateReplacement(
		ctx context.Context, in service.CreateReplacementInput,
	) (service.ReplacementRecord, error)
	// GetReplacement returns a replacement with its lines.
	GetReplacement(ctx context.Context, id string) (service.ReplacementRecord, error)
	// ListReplacementsOfClaim returns a claim's replacements, newest first.
	ListReplacementsOfClaim(ctx context.Context, claimID string) ([]models.Replacement, error)
	// CancelReplacement withdraws a request that has not been acted on.
	CancelReplacement(ctx context.Context, id string) (models.Replacement, error)
	// GetClaim returns the claim record by its id.
	GetClaim(ctx context.Context, claimID string) (models.Claim, error)
	// ListClaims pages the order's claim records.
	ListClaims(ctx context.Context, orderID string, page service.Page) ([]models.Claim, int64, error)
	// CancelClaim withdraws the claim.
	//
	// It is the claim's SECOND transition; the first, settling, is not here
	// because it moves money and therefore goes through a flow (see
	// [ReturnReceiving.SettleClaim]).
	CancelClaim(ctx context.Context, claimID string) (models.Claim, error)

	// PlacedMargins returns the margin each given order's goods were placed at
	// (ADR 0401); an order with no line that is not a gift card has no entry.
	PlacedMargins(ctx context.Context, orderIDs []string) (map[string]models.PlacedMargin, error)
}

// ReturnReceiving is the surface used by this package of the flow that RECEIVES
// a return (ADR 0001/0006).
//
// # Why the endpoint does not call the service directly
//
// Receiving a return has two halves: the record says the goods arrived, and the
// stock goes back. The second reaches the inventory module, which this one does
// not know, so it belongs to a flow. Had the endpoint been bound to the service
// method it would have stamped the record and SILENTLY skipped the restock —
// the same shape of defect the cart module names about its line price.
type ReturnReceiving interface {
	// ReceiveReturn records that the goods arrived at the location and puts
	// their stock back.
	//
	// warnings is non-empty when the record is right and the warehouse count is
	// not; every entry needs a human.
	ReceiveReturn(ctx context.Context, returnID, locationID string) (
		restockedLines int, restockedUnits int64, warnings []string, err error,
	)

	// FundExchangeDifference records WHICH payment collection answers an
	// exchange's difference.
	//
	// It goes through a flow because deciding it needs both modules: the
	// exchange says what it owes and the payment module says what a collection
	// holds, and this one may not ask the second (ADR 0006).
	FundExchangeDifference(ctx context.Context, exchangeID, collectionID string) error

	// RefundExchangeDifference sends a funded exchange's money back and takes
	// the request back with it.
	//
	// It is the EXIT from a funded exchange, which refuses the ordinary
	// withdrawal: without it a record whose goods turn out to be unsendable
	// would sit funded for ever and its order could never be forgotten.
	RefundExchangeDifference(ctx context.Context, exchangeID, reason string) error

	// RefundReturn sends money back for a received return and records it on the
	// order.
	//
	// summaryRecorded being false does not mean the money stayed: it means the
	// ORDER does not say it left, and an operator has to be shown that.
	RefundReturn(ctx context.Context, returnID string, amount int64, reason string) (
		refunded int64, summaryRecorded bool, warnings []string, err error,
	)

	// SettleClaim settles a damage or shortage claim by refunding it.
	//
	// A claim settled with a REPLACEMENT is refused: money and goods are two
	// different verbs, and the second one is DispatchReplacement.
	SettleClaim(ctx context.Context, claimID string, amount int64, reason string) (
		refunded int64, summaryRecorded bool, warnings []string, err error,
	)

	// DispatchReplacement sends what a claim promised: it sets the units
	// aside, opens a parcel, takes the units out of the count and records all
	// three.
	//
	// alreadySent being true means the goods had already gone and nothing
	// moved this time.
	DispatchReplacement(ctx context.Context, replacementID string) (
		fulfillmentID string, sentUnits int64, alreadySent bool, err error,
	)

	// WithdrawReplacement takes back a replacement that has not left and
	// gives back the units its lines set aside (ADR 0237). The record alone
	// cannot: the units belong to the inventory module.
	WithdrawReplacement(ctx context.Context, replacementID string) error
}

// Handler is the HTTP handler set of the order module.
type Handler struct {
	svc        Orders
	receiving  ReturnReceiving
	invoicing  Invoicing
	fulfilling Fulfilling
	// identity proves the customer the storefront's own orders route names
	// (ADR 0367); nil refuses that route.
	identity corehttp.Identity
}

// New produces the handler set that runs over the given service and flow.
//
// receiving and invoicing may be nil; the endpoints that need them then FAIL
// CLOSED rather than falling back to something else. For receiving the fallback
// would stamp a return as received and put no stock back; for invoicing there
// is nothing to fall back TO — a document cannot be produced without the flow
// that assembles it, and pretending otherwise would answer a caller who is
// waiting for a legal document.
func New(
	svc Orders, receiving ReturnReceiving, invoicing Invoicing, fulfilling Fulfilling,
) *Handler {
	return &Handler{svc: svc, receiving: receiving, invoicing: invoicing, fulfilling: fulfilling}
}

// returnReceiving returns the flow; if it is not bound it returns an ERROR.
//
// # Why it fails CLOSED
//
// The same reasoning the cart module gives about its line price. If the flow is
// missing, the correct answer is NOT "record the receipt and skip the stock":
// the goods would be in the warehouse and the count would say they are not,
// with a record claiming the receipt succeeded. The only correct outcome of a
// missing flow is the return NOT BEING RECEIVED AT ALL.
func (h *Handler) returnReceiving() (ReturnReceiving, error) {
	if h.receiving == nil {
		return nil, coreerrors.Internal(codeFlowUnavailable,
			"the return receiving flow is not bound; a return cannot be received without the "+
				"stock going back")
	}

	return h.receiving, nil
}

// fulfillingFlow returns the flow; if it is not bound it returns an ERROR.
//
// It fails CLOSED and the reason is specific to this one: a shipment opened
// without the flow would be a real parcel bound to nothing, and nothing could
// afterwards say which order it belonged to. That is worse than not shipping.
func (h *Handler) fulfillingFlow() (Fulfilling, error) {
	if h.fulfilling == nil {
		return nil, coreerrors.Internal(codeFlowUnavailable,
			"the fulfilling flow is not bound; a shipment cannot be opened for an order "+
				"without it, and one opened another way would be bound to nothing")
	}

	return h.fulfilling, nil
}

// invoicingFlow returns the flow; if it is not bound it returns an ERROR.
//
// It fails CLOSED for a plainer reason than the receiving flow does: there is
// no second path to a document. What the endpoint must not do is answer 200
// with nothing, which is what a nil check placed further in would produce.
func (h *Handler) invoicingFlow() (Invoicing, error) {
	if h.invoicing == nil {
		return nil, coreerrors.Internal(codeFlowUnavailable,
			"the invoicing flow is not bound; an order cannot be invoiced without it")
	}

	return h.invoicing, nil
}

// --- envelopes and DTOs ------------------------------------------------------

// singleEnvelope is the envelope of single-record responses (plan Section 8).
type singleEnvelope struct {
	// Data is the body of the response.
	Data any `json:"data"`
}

// listEnvelope is the envelope of list responses (plan Section 8).
type listEnvelope struct {
	// Data holds the records on the page.
	Data any `json:"data"`
	// Count is the number of ALL records matching the filter; not the number of
	// rows on the page.
	Count int64 `json:"count"`
	// Offset is the number of skipped records.
	Offset int64 `json:"offset"`
	// Limit is the requested page size.
	Limit int64 `json:"limit"`
	// NextCursor is the opaque position to send back as "after" for the next
	// page; it is ABSENT when this page is the last one.
	//
	// Its absence is the end-of-listing signal, which is what a client walking
	// forward needs and what offset alone cannot give without a count.
	NextCursor string `json:"next_cursor,omitempty"`
}

// orderDTO is the external representation of the order.
type orderDTO struct {
	ID           string `json:"id"`
	DisplayID    int64  `json:"display_id"`
	Status       string `json:"status"`
	RegionID     string `json:"region_id"`
	CustomerID   string `json:"customer_id,omitempty"`
	Email        string `json:"email,omitempty"`
	CurrencyCode string `json:"currency_code"`
	CartID       string `json:"cart_id,omitempty"`
	// AddsToOrderID is the order this one adds to; absent on an order that
	// adds to nothing (ADR 0192). The amounts beside it are this order's own.
	AddsToOrderID string `json:"adds_to_order_id,omitempty"`
	Subtotal      int64  `json:"subtotal"`
	DiscountTotal int64  `json:"discount_total"`
	TaxTotal      int64  `json:"tax_total"`
	ShippingTotal int64  `json:"shipping_total"`
	Total         int64  `json:"total"`
	// PricesIncludeTax says the order was sold in a market whose prices
	// include their tax: a line's unit_price is the sticker and its subtotal is
	// what is left of it once its tax_total is taken out (ADR 0246).
	PricesIncludeTax bool           `json:"prices_include_tax"`
	Metadata         map[string]any `json:"metadata,omitempty"`
	PlacedAt         time.Time      `json:"placed_at"`
	CompletedAt      *time.Time     `json:"completed_at,omitempty"`
	CanceledAt       *time.Time     `json:"canceled_at,omitempty"`
	ArchivedAt       *time.Time     `json:"archived_at,omitempty"`
	CancelReason     string         `json:"cancel_reason,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

// orderDetailDTO is the external representation of the order with its line
// items and summary.
type orderDetailDTO struct {
	orderDTO
	Items   []lineItemDTO `json:"items"`
	Summary summaryDTO    `json:"summary"`
	// ShippingMethods are the deliveries the order was sold (ADR 0198); an
	// empty array for an order placed before they were kept or shipping
	// nothing. A service's name is not about the person, so both surfaces
	// carry it.
	ShippingMethods []shippingMethodDTO `json:"shipping_methods"`
}

// shippingMethodDTO is one delivery the order was sold, and what it was
// changed to since.
type shippingMethodDTO struct {
	// ID names the method a delivery change applies to (ADR 0199).
	ID               string `json:"id"`
	ShippingOptionID string `json:"shipping_option_id,omitempty"`
	Name             string `json:"name"`
	Amount           int64  `json:"amount"`
	// Changes are the services the delivery was put on after the sale, oldest
	// first; the last one is the delivery the order is on now (ADR 0199).
	Changes []deliveryChangeDTO `json:"changes"`
}

// deliveryChangeDTO is one change to a delivery.
type deliveryChangeDTO struct {
	ID               string    `json:"id"`
	ShippingOptionID string    `json:"shipping_option_id"`
	Name             string    `json:"name"`
	Amount           int64     `json:"amount"`
	Difference       int64     `json:"difference"`
	CreditLineID     string    `json:"credit_line_id,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	// PaymentCollectionID is the collection that paid a dearer change
	// (ADR 0200).
	PaymentCollectionID string `json:"payment_collection_id,omitempty"`
}

// adminOrderDetailDTO is the order as the operator reads it: the storefront's
// record, where it went and whom it was billed to (ADR 0193), and what its
// goods cost the shop (ADR 0401).
//
// The addresses are the admin surface's alone. The storefront reads an order
// by its id with a key that names the shop rather than the shopper (ADR 0008),
// and a home address is more than that read should hand anyone holding the id.
type adminOrderDetailDTO struct {
	orderDetailDTO
	// ShippingAddress and BillingAddress are absent when the order recorded
	// none, as a download has neither. After an erasure they hold what the
	// erasure kept: the country and the free metadata.
	ShippingAddress *orderAddressDTO `json:"shipping_address,omitempty"`
	BillingAddress  *orderAddressDTO `json:"billing_address,omitempty"`
	// PlacedBy is the operator who placed the order through the admin cart
	// surface or the panel; absent on a shopper's order (ADR 0298). It is the
	// admin surface's alone, as the addresses are: a shopper reading the order
	// would read the operator's identity.
	PlacedBy string `json:"placed_by,omitempty"`
	// SalesChannelID is the sales channel the order's cart was opened in;
	// absent when it named none (ADR 0410). It is the shop's partition of its
	// sales, so the admin surface carries it and the storefront's record does
	// not.
	SalesChannelID string `json:"sales_channel_id,omitempty"`
	// Items SHADOWS the storefront record's lines with the admin's, which
	// carry what each unit cost the shop (ADR 0401); encoding/json writes only
	// this one.
	Items []adminLineItemDTO `json:"items"`
	// PlacedMargin is what the order's goods earned when it was placed
	// (ADR 0401); absent on an order with no line that is not a gift card.
	PlacedMargin *placedMarginDTO `json:"placed_margin,omitempty"`
}

// adminLineItemDTO is an order line as the operator reads it: the storefront's
// line and what one unit of it cost the shop.
//
// The cost is the admin surface's alone. The storefront's order and its lines
// are [orderDetailDTO] and [lineItemDTO], which carry none, and the shop's cost
// is not a shopper's business.
type adminLineItemDTO struct {
	lineItemDTO
	// UnitCost is what one unit cost the shop in the order's currency, net of
	// tax, copied at the sale (ADR 0401); absent when the line kept none.
	UnitCost *int64 `json:"unit_cost,omitempty"`
}

// placedMarginDTO is an order's margin at placement, over its lines that did
// not sell gift cards (ADR 0401).
type placedMarginDTO struct {
	// CurrencyCode is the order's; every amount here is in its minor units.
	CurrencyCode string `json:"currency_code"`
	// Sales is the lines' subtotal less their discount, net of tax.
	Sales int64 `json:"sales"`
	// Cost is the lines' unit cost times their quantity; absent when a line
	// kept no cost or the sum passes an order total's bound.
	Cost *int64 `json:"cost,omitempty"`
	// Margin is sales less cost, negative for goods sold at a loss; absent
	// when cost is.
	Margin *int64 `json:"margin,omitempty"`
	// LinesWithoutCost counts the lines that kept no cost.
	LinesWithoutCost int64 `json:"lines_without_cost"`
}

// adminOrderRowDTO is one row of the admin order list: the order and its
// placed margin (ADR 0401). The storefront's list of a customer's orders is
// [orderDTO] alone.
type adminOrderRowDTO struct {
	orderDTO
	// SalesChannelID is the sales channel the order's cart was opened in;
	// absent when it named none (ADR 0410).
	SalesChannelID string `json:"sales_channel_id,omitempty"`
	// PlacedMargin is absent on an order with no line that is not a gift card.
	PlacedMargin *placedMarginDTO `json:"placed_margin,omitempty"`
}

// orderAddressDTO is one address the order was placed with, as the cart
// carried it into the order.
type orderAddressDTO struct {
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
	Company   string `json:"company,omitempty"`
	Address1  string `json:"address_1,omitempty"`
	Address2  string `json:"address_2,omitempty"`
	City      string `json:"city,omitempty"`
	// Province is the unit under the country, an il in Turkey. The district a
	// domestic carrier prices on has no field of its own (ADR 0067).
	Province    string         `json:"province,omitempty"`
	PostalCode  string         `json:"postal_code,omitempty"`
	CountryCode string         `json:"country_code,omitempty"`
	Phone       string         `json:"phone,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// lineItemDTO is the external representation of an order line item.
type lineItemDTO struct {
	ID        string `json:"id"`
	OrderID   string `json:"order_id"`
	VariantID string `json:"variant_id"`
	Title     string `json:"title"`
	// ProductTitle is the title of the variant's product, copied with the
	// variant's at the sale (ADR 0365); absent on a line written before.
	ProductTitle  string `json:"product_title,omitempty"`
	Quantity      int64  `json:"quantity"`
	UnitPrice     int64  `json:"unit_price"`
	Subtotal      int64  `json:"subtotal"`
	DiscountTotal int64  `json:"discount_total"`
	TaxTotal      int64  `json:"tax_total"`
	// TaxRateBps is the rate the line's tax was computed at, in BASIS POINTS
	// (2000 = 20%).
	//
	// It is published because it cannot be recomputed: the tax is rounded down
	// per line, so the amount alone maps back to a range of rates. Anything
	// that prints an invoice needs the rate the customer was charged under.
	TaxRateBps int32 `json:"tax_rate_bps"`
	// TaxComponents is the per-rate breakdown when a STACK taxed the line, base
	// first; it is absent when a single rate applied and TaxRateBps says it all.
	//
	// It is published for the same reason the rate is: a line taxed at 5% + 8%
	// carries "5%" — a rate really applied, on an amount really recorded — and
	// anything that prints a document has to state the other one too.
	TaxComponents []lineTaxDTO   `json:"tax_components,omitempty"`
	Total         int64          `json:"total"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	// PriceOrigin is the price the line was charged — the price row and, for a
	// list price, the list and its type (ADR 0168). It is absent when unknown:
	// every line sold before the order kept it.
	PriceOrigin *linePriceOriginDTO `json:"price_origin,omitempty"`
	// IsGiftcard says the line sold gift cards (ADR 0211).
	IsGiftcard bool `json:"is_giftcard"`
	// Properties are what the shopper wrote on the line — an engraving, a gift
	// message — as the cart line carried them (ADR 0223).
	Properties map[string]string `json:"properties,omitempty"`
	// ParentLineItemID is the line of this order the line is an add-on of, the
	// ring an engraving was sold for (ADR 0229); absent on a line of its own.
	ParentLineItemID *string `json:"parent_line_item_id,omitempty"`
	// Components are what one unit of the line held when it was sold, for a
	// line that sold a bundle (ADR 0235); absent on any other line.
	Components []lineComponentDTO `json:"components,omitempty"`
	CreatedAt  time.Time          `json:"created_at"`
	UpdatedAt  time.Time          `json:"updated_at"`
}

// lineComponentDTO is one variant a bundle line's unit held, and how many.
type lineComponentDTO struct {
	VariantID string `json:"variant_id"`
	Quantity  int64  `json:"quantity"`
}

// linePriceOriginDTO is which of a variant's prices a line was charged.
type linePriceOriginDTO struct {
	// PriceID is pricing's price row; pricing's price history reads it back
	// after the row itself is replaced.
	PriceID string `json:"price_id"`
	// PriceListID and PriceListType name its list — "sale" or "override" — and
	// are null for a base price.
	PriceListID   *string `json:"price_list_id"`
	PriceListType *string `json:"price_list_type"`
}

// lineTaxDTO is the external representation of one rate inside a line's tax
// stack.
//
// The position is not published: the list is already in stack order, base
// first, and a second way to say the same thing is a second thing that can
// disagree.
type lineTaxDTO struct {
	RateID        string `json:"rate_id"`
	RateBps       int32  `json:"rate_bps"`
	Compound      bool   `json:"compound"`
	TaxableAmount int64  `json:"taxable_amount"`
	TaxAmount     int64  `json:"tax_amount"`
}

// summaryDTO is the external representation of the order's payment/refund
// summary.
//
// Outstanding is a DERIVED field and it is presented TOGETHER with the amounts:
// having the client compute the outstanding amount itself meant the same
// formula being written in two places and one of them being wrong. The value
// can be NEGATIVE (overcollection).
type summaryDTO struct {
	ID            string `json:"id"`
	OrderID       string `json:"order_id"`
	PaidTotal     int64  `json:"paid_total"`
	RefundedTotal int64  `json:"refunded_total"`
	// CreditedTotal is the sum of the order's credit lines: what was written
	// off without changing what was sold.
	CreditedTotal int64     `json:"credited_total"`
	Outstanding   int64     `json:"outstanding"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// returnDTO is the external representation of a return record.
type returnDTO struct {
	ID           string         `json:"id"`
	OrderID      string         `json:"order_id"`
	Status       string         `json:"status"`
	RefundAmount int64          `json:"refund_amount"`
	Reason       string         `json:"reason,omitempty"`
	Note         string         `json:"note,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	// Lines are the order lines coming back, an empty list on a return that
	// names none (ADR 0433).
	//
	// Lines and SoldFor are absent only from the answer to a withdrawal whose
	// lines could not be read after it was written, and Warnings says so: the
	// withdrawal stands, and an error would invite repeating it.
	Lines *[]returnLineDTO `json:"lines,omitempty"`
	// SoldFor is what the return's units were sold for: each line's total
	// shared by the units coming back, rounded down. The return's refunds add
	// up to at most this figure; what they gave back is the payment module's
	// to say, and is not copied here (ADR 0119, ADR 0433).
	SoldFor    *int64     `json:"sold_for,omitempty"`
	ReceivedAt *time.Time `json:"received_at,omitempty"`
	CanceledAt *time.Time `json:"canceled_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	// Warnings are what the answer could not read; they need a human.
	Warnings []string `json:"warnings,omitempty"`
}

// returnLineDTO is one order line a return names.
type returnLineDTO struct {
	OrderLineItemID string `json:"order_line_item_id"`
	Quantity        int64  `json:"quantity"`
	// RefundAmount is the part of the planned refund the line was opened with.
	RefundAmount int64 `json:"refund_amount"`
}

// exchangeDTO is the external representation of an exchange record.
type exchangeDTO struct {
	ID            string         `json:"id"`
	OrderID       string         `json:"order_id"`
	Status        string         `json:"status"`
	DifferenceDue int64          `json:"difference_due"`
	Note          string         `json:"note,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	// ReturnID is the return whose goods the exchange takes back; absent on an
	// exchange that names none. With it difference_due is derived: what the
	// exchange's live replacements send, priced, less what the return takes
	// back (ADR 0432).
	ReturnID string `json:"return_id,omitempty"`
	// PaymentCollectionID is the collection that answers the difference, and
	// FundedAt is when it was named. Both are empty until the difference is
	// funded, and the database holds them to each other in both directions.
	//
	// The identifier is published and the AMOUNT is not: the figure belongs to
	// the payment module and a copy of it here would be a claim a route this
	// module never hears about can invalidate (ADR 0119). A client that wants
	// the numbers reads the collection.
	PaymentCollectionID string     `json:"payment_collection_id,omitempty"`
	FundedAt            *time.Time `json:"funded_at,omitempty"`
	// CompletedAt and CanceledAt are the two moments the status can name.
	//
	// CompletedAt was missing until ADR 0117 while the status it dates was
	// not: ADR 0114 brought 'completed' back and this surface published the
	// word without the moment. The endpoint table matched, because it is
	// derived from THIS type rather than from the record.
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	CanceledAt  *time.Time `json:"canceled_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// claimDTO is the external representation of a claim record.
type claimDTO struct {
	ID           string         `json:"id"`
	OrderID      string         `json:"order_id"`
	Type         string         `json:"type"`
	Status       string         `json:"status"`
	RefundAmount int64          `json:"refund_amount"`
	Reason       string         `json:"reason,omitempty"`
	Note         string         `json:"note,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	CompletedAt  *time.Time     `json:"completed_at,omitempty"`
	CanceledAt   *time.Time     `json:"canceled_at,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// toOrderDTO converts the model to the external representation.
func toOrderDTO(order models.Order) orderDTO {
	return orderDTO{
		ID:               order.ID,
		DisplayID:        order.DisplayID,
		Status:           order.Status.String(),
		RegionID:         order.RegionID,
		CustomerID:       order.CustomerID,
		Email:            order.Email,
		CurrencyCode:     order.CurrencyCode,
		CartID:           order.CartID,
		AddsToOrderID:    order.AddsToOrderID,
		Subtotal:         order.Subtotal,
		DiscountTotal:    order.DiscountTotal,
		TaxTotal:         order.TaxTotal,
		ShippingTotal:    order.ShippingTotal,
		Total:            order.Total,
		PricesIncludeTax: order.PricesIncludeTax,
		Metadata:         order.Metadata,
		PlacedAt:         order.PlacedAt,
		CompletedAt:      order.CompletedAt,
		CanceledAt:       order.CanceledAt,
		ArchivedAt:       order.ArchivedAt,
		CancelReason:     order.CancelReason,
		CreatedAt:        order.CreatedAt,
		UpdatedAt:        order.UpdatedAt,
	}
}

// toOrderDetailDTO converts the order with its line items and summary to the
// external representation.
func toOrderDetailDTO(detail models.OrderDetail) orderDetailDTO {
	out := orderDetailDTO{
		orderDTO:        toOrderDTO(detail.Order),
		Items:           make([]lineItemDTO, 0, len(detail.Items)),
		Summary:         toSummaryDTO(detail.Summary, detail.Total, detail.CreditedTotal),
		ShippingMethods: make([]shippingMethodDTO, 0, len(detail.ShippingMethods)),
	}
	for _, method := range detail.ShippingMethods {
		dto := shippingMethodDTO{
			ID: method.ID, ShippingOptionID: method.ShippingOptionID, Name: method.Name,
			Amount: method.Amount, Changes: []deliveryChangeDTO{},
		}
		for i := range detail.DeliveryChanges {
			change := &detail.DeliveryChanges[i]
			if change.ShippingMethodID != method.ID {
				continue
			}
			dto.Changes = append(dto.Changes, deliveryChangeDTO{
				ID: change.ID, ShippingOptionID: change.ShippingOptionID, Name: change.Name,
				Amount: change.Amount, Difference: change.Difference,
				CreditLineID: change.CreditLineID, CreatedAt: change.CreatedAt,
				PaymentCollectionID: change.PaymentCollectionID,
			})
		}
		out.ShippingMethods = append(out.ShippingMethods, dto)
	}
	// The loop is walked by index: the line item struct is large and copying it
	// by value would carry a few hundred bytes for nothing on every turn.
	for i := range detail.Items {
		out.Items = append(out.Items, toLineItemDTO(detail.Items[i]))
	}
	return out
}

// toAdminOrderDetailDTO is [toOrderDetailDTO] with the order's addresses, its
// lines' costs and its placed margin; margins is what
// [Orders.PlacedMargins] answered for it.
func toAdminOrderDetailDTO(detail models.OrderDetail, margins map[string]models.PlacedMargin) adminOrderDetailDTO {
	out := adminOrderDetailDTO{
		orderDetailDTO:  toOrderDetailDTO(detail),
		ShippingAddress: toOrderAddressDTO(detail.ShippingAddress),
		BillingAddress:  toOrderAddressDTO(detail.BillingAddress),
		PlacedBy:        detail.PlacedBy,
		SalesChannelID:  detail.SalesChannelID,
		Items:           make([]adminLineItemDTO, 0, len(detail.Items)),
		PlacedMargin:    toPlacedMarginDTO(detail.Order, margins),
	}
	for i := range detail.Items {
		out.Items = append(out.Items, adminLineItemDTO{
			lineItemDTO: toLineItemDTO(detail.Items[i]),
			UnitCost:    detail.Items[i].UnitCost,
		})
	}
	return out
}

// toPlacedMarginDTO is the order's placed margin; nil when it has none.
func toPlacedMarginDTO(order models.Order, margins map[string]models.PlacedMargin) *placedMarginDTO {
	margin, ok := margins[order.ID]
	if !ok {
		return nil
	}
	return &placedMarginDTO{
		CurrencyCode:     order.CurrencyCode,
		Sales:            margin.Sales,
		Cost:             margin.Cost,
		Margin:           margin.Margin,
		LinesWithoutCost: margin.LinesWithoutCost,
	}
}

// toOrderAddressDTO converts one address; nil when the order recorded none.
func toOrderAddressDTO(address *models.OrderAddress) *orderAddressDTO {
	if address == nil {
		return nil
	}

	return &orderAddressDTO{
		FirstName:   address.FirstName,
		LastName:    address.LastName,
		Company:     address.Company,
		Address1:    address.Address1,
		Address2:    address.Address2,
		City:        address.City,
		Province:    address.Province,
		PostalCode:  address.PostalCode,
		CountryCode: address.CountryCode,
		Phone:       address.Phone,
		Metadata:    address.Metadata,
	}
}

// toLineItemDTO converts the model to the external representation.
func toLineItemDTO(item models.OrderLineItem) lineItemDTO {
	return lineItemDTO{
		ID:               item.ID,
		OrderID:          item.OrderID,
		VariantID:        item.VariantID,
		Title:            item.Title,
		ProductTitle:     item.ProductTitle,
		Quantity:         item.Quantity,
		UnitPrice:        item.UnitPrice,
		Subtotal:         item.Subtotal,
		DiscountTotal:    item.DiscountTotal,
		TaxTotal:         item.TaxTotal,
		TaxRateBps:       item.TaxRateBps,
		TaxComponents:    toLineTaxDTOs(item.TaxComponents),
		Total:            item.Total,
		Metadata:         item.Metadata,
		PriceOrigin:      toLinePriceOriginDTO(item.PriceOrigin),
		IsGiftcard:       item.IsGiftcard,
		Properties:       item.Properties,
		ParentLineItemID: item.ParentLineItemID,
		Components:       toLineComponentDTOs(item.Components),
		CreatedAt:        item.CreatedAt,
		UpdatedAt:        item.UpdatedAt,
	}
}

// toLineComponentDTOs converts a bundle line's components; none is nil.
func toLineComponentDTOs(components []models.OrderLineComponent) []lineComponentDTO {
	if len(components) == 0 {
		return nil
	}
	out := make([]lineComponentDTO, 0, len(components))
	for _, c := range components {
		out = append(out, lineComponentDTO{VariantID: c.VariantID, Quantity: c.Quantity})
	}
	return out
}

// toLinePriceOriginDTO converts a line's price origin; nil when unknown.
func toLinePriceOriginDTO(origin *models.LinePriceOrigin) *linePriceOriginDTO {
	if origin == nil {
		return nil
	}
	out := &linePriceOriginDTO{PriceID: origin.PriceID}
	if origin.PriceListID != "" {
		listID, listType := origin.PriceListID, origin.PriceListType
		out.PriceListID, out.PriceListType = &listID, &listType
	}

	return out
}

// toLineTaxDTOs converts a line's tax breakdown to the external representation.
func toLineTaxDTOs(components []models.OrderLineTax) []lineTaxDTO {
	if len(components) == 0 {
		return nil
	}

	out := make([]lineTaxDTO, 0, len(components))
	for i := range components {
		out = append(out, lineTaxDTO{
			RateID:        components[i].RateID,
			RateBps:       components[i].RateBps,
			Compound:      components[i].Compound,
			TaxableAmount: components[i].TaxableAmount,
			TaxAmount:     components[i].TaxAmount,
		})
	}
	return out
}

// toSummaryDTO converts the summary to the external representation.
//
// The outstanding amount is computed from the order total AND the credited
// total, because a concession lowers what is owed without lowering what was
// sold (ADR 0105). Both are parameters because the summary stores neither.
func toSummaryDTO(summary models.OrderSummary, orderTotal, creditedTotal int64) summaryDTO {
	return summaryDTO{
		ID:            summary.ID,
		OrderID:       summary.OrderID,
		PaidTotal:     summary.PaidTotal,
		RefundedTotal: summary.RefundedTotal,
		CreditedTotal: creditedTotal,
		Outstanding:   summary.Outstanding(orderTotal, creditedTotal),
		CreatedAt:     summary.CreatedAt,
		UpdatedAt:     summary.UpdatedAt,
	}
}

// toReturnDTO converts the record to the external representation.
func toReturnDTO(ret *service.ReturnRecord) returnDTO {
	lines := make([]returnLineDTO, 0, len(ret.Items))
	for i := range ret.Items {
		lines = append(lines, returnLineDTO{
			OrderLineItemID: ret.Items[i].OrderLineItemID,
			Quantity:        ret.Items[i].Quantity,
			RefundAmount:    ret.Items[i].RefundAmount,
		})
	}

	out := toReturnHeadDTO(&ret.Return)
	soldFor := ret.SoldFor
	out.Lines, out.SoldFor = &lines, &soldFor

	return out
}

// toReturnHeadDTO converts the return's own row, without its lines and their
// worth.
func toReturnHeadDTO(ret *models.Return) returnDTO {
	return returnDTO{
		ID:           ret.ID,
		OrderID:      ret.OrderID,
		Status:       ret.Status.String(),
		RefundAmount: ret.RefundAmount,
		Reason:       ret.Reason,
		Note:         ret.Note,
		Metadata:     ret.Metadata,
		ReceivedAt:   ret.ReceivedAt,
		CanceledAt:   ret.CanceledAt,
		CreatedAt:    ret.CreatedAt,
		UpdatedAt:    ret.UpdatedAt,
	}
}

// returnDTOs reads the returns' lines and what their units were sold for, and
// converts them (ADR 0433).
func (h *Handler) returnDTOs(ctx context.Context, returns []models.Return) ([]returnDTO, error) {
	records, err := h.svc.ReturnsWithLines(ctx, returns)
	if err != nil {
		return nil, err
	}

	out := make([]returnDTO, 0, len(records))
	for i := range records {
		out = append(out, toReturnDTO(&records[i]))
	}

	return out, nil
}

// writeReturn answers a read with one return record, its lines and what its
// units were sold for.
func (h *Handler) writeReturn(ctx context.Context, w http.ResponseWriter, status int, ret models.Return) {
	data, err := h.returnDTOs(ctx, []models.Return{ret})
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, status, singleEnvelope{Data: data[0]})
}

// writeWrittenReturn answers a write that committed with the record, its lines
// and what its units were sold for. When the lines cannot be read after the
// write, the record is answered without them and with a warning, and the
// failure is logged: the write stands, and an error would invite repeating
// it.
func (h *Handler) writeWrittenReturn(ctx context.Context, w http.ResponseWriter, status int, ret models.Return) {
	data, err := h.returnDTOs(ctx, []models.Return{ret})
	if err != nil {
		corehttp.LoggerFromContext(ctx).ErrorContext(ctx,
			"the return was written and its lines could not be read for the answer",
			"return_id", ret.ID, "order_id", ret.OrderID, "error", err)
		head := toReturnHeadDTO(&ret)
		head.Warnings = []string{"The return was written; its lines and what they were sold for " +
			"could not be read for this answer. Read the return again."}
		corehttp.WriteJSON(ctx, w, status, singleEnvelope{Data: head})

		return
	}

	corehttp.WriteJSON(ctx, w, status, singleEnvelope{Data: data[0]})
}

// toExchangeDTO converts the model to the external representation.
func toExchangeDTO(exchange models.Exchange) exchangeDTO {
	return exchangeDTO{
		ID:                  exchange.ID,
		OrderID:             exchange.OrderID,
		Status:              exchange.Status.String(),
		DifferenceDue:       exchange.DifferenceDue,
		Note:                exchange.Note,
		Metadata:            exchange.Metadata,
		ReturnID:            exchange.ReturnID,
		PaymentCollectionID: exchange.PaymentCollectionID,
		FundedAt:            exchange.FundedAt,
		CompletedAt:         exchange.CompletedAt,
		CanceledAt:          exchange.CanceledAt,
		CreatedAt:           exchange.CreatedAt,
		UpdatedAt:           exchange.UpdatedAt,
	}
}

// toClaimDTO converts the model to the external representation.
func toClaimDTO(claim models.Claim) claimDTO {
	return claimDTO{
		ID:           claim.ID,
		OrderID:      claim.OrderID,
		Type:         claim.Type.String(),
		Status:       claim.Status.String(),
		RefundAmount: claim.RefundAmount,
		Reason:       claim.Reason,
		Note:         claim.Note,
		Metadata:     claim.Metadata,
		CompletedAt:  claim.CompletedAt,
		CanceledAt:   claim.CanceledAt,
		CreatedAt:    claim.CreatedAt,
		UpdatedAt:    claim.UpdatedAt,
	}
}

// --- helpers -----------------------------------------------------------------

// decodeBody decodes the request body; the body is MANDATORY.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	return decodeJSON(w, r, dst, false)
}

// decodeOptionalBody decodes requests whose body MAY BE LEFT EMPTY.
//
// On the cancel endpoint the body only carries an optional reason; counting an
// empty body as an error would have made canceling without a reason impossible.
// If a body was sent, the whole strictness of [decodeJSON] (including the
// rejection of unknown fields) applies.
func decodeOptionalBody(w http.ResponseWriter, r *http.Request, dst any) error {
	return decodeJSON(w, r, dst, true)
}

// decodeJSON decodes the request body.
//
// The body size is limited and UNRECOGNIZED FIELDS are rejected: a silently
// swallowed field means a setting the client believes it sent but which is
// never applied.
//
// If allowEmpty is true then sending no body at all is valid and dst stays at
// its zero value. The emptiness check is done by looking NOT at Content-Length
// but at the io.EOF of the decoding: in a chunked request the length is -1 and
// a check that looked at the length would misclassify those requests.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any, allowEmpty bool) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			if allowEmpty {
				return nil
			}
			return coreerrors.Invalid(codeInvalidRequest, "the request body cannot be empty")
		}
		return coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"the request body could not be parsed")
	}
	// If more than a single JSON value was sent, that too is a client error.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return coreerrors.Invalid(codeInvalidRequest,
			"the request body has to be a single JSON object")
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
	// "after" and "offset" name two different positions; honoring both would
	// serve the page N rows past the cursor, which neither of them asked for.
	raw := r.URL.Query().Get("after")
	if raw != "" && offset != 0 {
		return service.Page{}, coreerrors.Invalid(codeInvalidRequest,
			`"after" and "offset" name two different positions; send one of them`)
	}

	after, err := corepage.Decode(service.OrderListing, raw)
	if err != nil {
		return service.Page{}, err
	}

	page := service.Page{Limit: limit, Offset: offset, After: after}
	if page.Limit == 0 {
		// So that the limit field in the response really shows the limit that
		// is applied, the default is made visible here as well.
		page.Limit = service.DefaultLimit
	}
	return page, nil
}

// parseInt64Param converts a query parameter to an integer; returns 0 when it
// is absent.
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

// orderID reads the order id from the request.
func orderID(r *http.Request) string {
	return chi.URLParam(r, paramOrderID)
}
