package service

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// This file is the CROSS-MODULE surface of the order module (ADR 0001,
// ADR 0006).
//
// The complete_cart saga under internal/workflows opens the order through this
// module and cancels it in compensation, but neither can that package import
// this module nor this module that package. The solution is the same as the
// interop.go in the cart/region/pricing modules: publishing a surface that uses
// only PRIMITIVE and stdlib types. The consumer defines its own narrow
// interface, this type satisfies it STRUCTURALLY, and it is resolved from the
// container under the name "order.interop".
//
// The counterpart on the consumer side is this (the workflow defines it in its
// own package):
//
//	type OrderPlacer interface {
//	    PlaceOrderJSON(ctx context.Context, snapshot json.RawMessage) (string, error)
//	    CancelOrder(ctx context.Context, orderID, reason string) error
//	}
//
// [Interop.CompleteOrder] is DELIBERATELY absent from that interface: a
// completed order cannot be canceled, so the moment the saga called it, it
// would make its own compensation impossible (the rationale is written on the
// consumer side too).
//
// The SECOND consumer of the surface is the NOTIFICATION side that subscribes
// to the "order.placed" event, and it only READS. The payload of the event is
// deliberately narrow and CARRIES NO personal data (rationale: the block on
// [EventFieldTotal]); the subscriber cannot get the e-mail it needs for
// delivery out of the event, it has to read the order with the order_id it
// holds. It too defines its own narrow interface in its own package:
//
//	type OrderContactReader interface {
//	    OrderContactJSON(ctx context.Context, orderID string) (json.RawMessage, error)
//	}
//
// Composite data (the cart's snapshot) travels as JSON. The field names are
// declared EXPLICITLY below; they MUST be exactly the same as the schema on the
// consumer side and the match can only be proven by an integration test — the
// compiler cannot check the match because this module cannot import the
// workflow package.

// CodeInteropSnapshotInvalid reports that an unparseable snapshot body arrived.
const CodeInteropSnapshotInvalid = "order_interop_snapshot_invalid"

// Interop turns the order service into the cross-module PRIMITIVE surface.
//
// It makes no decision: it only translates the signature and the JSON schema.
// All the business rules stay on [Service]; adding a rule here would mean the
// same rule diverging in two places.
//
// It is registered in the container under the name "order.interop".
type Interop struct {
	svc *Service
}

// NewInterop sets up the cross-module surface for the given service.
func NewInterop(svc *Service) *Interop { return &Interop{svc: svc} }

// interopSnapshot is the JSON schema of the cart snapshot that will turn into
// an order.
//
// # Schema
//
//	{
//	  "cart_id":         "cart_01H…",   // optional; documents the origin only
//	  "region_id":       "reg_01H…",    // REQUIRED
//	  "customer_id":     "cus_01H…",    // optional; empty means a guest order
//	  "email":           "a@b.com",     // optional
//	  "currency_code":   "TRY",         // REQUIRED, ISO 4217
//	  "idempotency_key": "wf_01H…",     // optional; MUST BE FILLED in the saga
//	  "subtotal":        3000,          // minor unit INTEGER
//	  "discount_total":  0,
//	  "tax_total":       600,
//	  "shipping_total":  2500,
//	  "total":           6100,
//	  "metadata":        {"channel": "web"},
//	  "items": [                        // AT LEAST ONE line
//	    {
//	      "variant_id":     "variant_01H…",
//	      "title":          "Red T-Shirt",
//	      "quantity":       3,
//	      "unit_price":     1000,
//	      "subtotal":       3000,
//	      "discount_total": 0,
//	      "tax_total":      600,
//	      "total":          3600,
//	      "metadata":       {}
//	    }
//	  ]
//	}
//
// # Who builds the snapshot
//
// The complete_cart workflow. The cart's own snapshot (the "cart.interop"
// surface of the cart module) carries the line IDENTIFIERS and the quantities,
// while the computed amounts are carried by the output of calculate_totals; the
// side that merges the two and produces this schema is the workflow. The order
// module knows neither the cart nor the pricing.
//
// # Unknown fields ARE IGNORED
//
// The parsing DOES NOT USE DisallowUnknownFields. The reason is deliberate: the
// consumer must be able to pass through a wider snapshot it holds (e.g. the
// cart's revision, the shipping methods) as it is, and those fields are of no
// use to the order. Strict parsing would make changing this module mandatory
// whenever a new field is added on the consumer side and would lock the two
// packages to each other without a compile-time dependency. MISSING fields, on
// the other hand, are not ignored: the absence of the required fields returns
// errors.Invalid from the validation of [Service.CreateOrder].
type interopSnapshot struct {
	CartID         string             `json:"cart_id"`
	RegionID       string             `json:"region_id"`
	CustomerID     string             `json:"customer_id"`
	Email          string             `json:"email"`
	CurrencyCode   string             `json:"currency_code"`
	IdempotencyKey string             `json:"idempotency_key"`
	Subtotal       int64              `json:"subtotal"`
	DiscountTotal  int64              `json:"discount_total"`
	TaxTotal       int64              `json:"tax_total"`
	ShippingTotal  int64              `json:"shipping_total"`
	Total          int64              `json:"total"`
	Metadata       map[string]any     `json:"metadata"`
	Items          []interopOrderItem `json:"items"`
}

// interopOrderItem is the JSON schema of one order line.
type interopOrderItem struct {
	VariantID     string         `json:"variant_id"`
	Title         string         `json:"title"`
	Quantity      int64          `json:"quantity"`
	UnitPrice     int64          `json:"unit_price"`
	Subtotal      int64          `json:"subtotal"`
	DiscountTotal int64          `json:"discount_total"`
	TaxTotal      int64          `json:"tax_total"`
	Total         int64          `json:"total"`
	Metadata      map[string]any `json:"metadata"`
}

// PlaceOrderJSON opens an order from the cart snapshot and returns its
// identifier.
//
// The schema is defined in the [interopSnapshot] documentation. When the
// "idempotency_key" in the snapshot is filled the call is IDEMPOTENT: a second
// call with the same key does not open a new order, it returns the identifier
// of the existing one. Because the saga may retry a step (plan Section 2.6),
// filling this field is complete_cart's responsibility.
func (i *Interop) PlaceOrderJSON(ctx context.Context, snapshot json.RawMessage) (string, error) {
	var incoming interopSnapshot
	if err := json.Unmarshal(snapshot, &incoming); err != nil {
		return "", errors.Wrap(err, errors.KindInvalid, CodeInteropSnapshotInvalid,
			"order snapshot could not be parsed")
	}

	items := make([]CreateOrderItemInput, 0, len(incoming.Items))
	for k := range incoming.Items {
		items = append(items, CreateOrderItemInput{
			VariantID:     incoming.Items[k].VariantID,
			Title:         incoming.Items[k].Title,
			Quantity:      incoming.Items[k].Quantity,
			UnitPrice:     incoming.Items[k].UnitPrice,
			Subtotal:      incoming.Items[k].Subtotal,
			DiscountTotal: incoming.Items[k].DiscountTotal,
			TaxTotal:      incoming.Items[k].TaxTotal,
			Total:         incoming.Items[k].Total,
			Metadata:      incoming.Items[k].Metadata,
		})
	}

	order, err := i.svc.CreateOrder(ctx, CreateOrderInput{
		RegionID:       incoming.RegionID,
		CustomerID:     incoming.CustomerID,
		Email:          incoming.Email,
		CurrencyCode:   incoming.CurrencyCode,
		CartID:         incoming.CartID,
		IdempotencyKey: incoming.IdempotencyKey,
		Subtotal:       incoming.Subtotal,
		DiscountTotal:  incoming.DiscountTotal,
		TaxTotal:       incoming.TaxTotal,
		ShippingTotal:  incoming.ShippingTotal,
		Total:          incoming.Total,
		Items:          items,
		Metadata:       incoming.Metadata,
	})
	if err != nil {
		return "", err
	}
	return order.ID, nil
}

// CancelOrder cancels the order; it IS THE SAGA COMPENSATION and it IS
// IDEMPOTENT.
//
// An already canceled order DOES NOT return an error on the second call.
// Canceling a completed order, on the other hand, returns errors.Conflict; for
// the rationale see [Service.CancelOrder].
func (i *Interop) CancelOrder(ctx context.Context, orderID, reason string) error {
	return i.svc.CancelOrder(ctx, orderID, reason)
}

// SetOrderSummaryTotals records on the order how much of it was collected and
// how much was refunded.
//
// # Why it is on this surface at all
//
// The order module cannot ask the payment module and must not (Principle
// 2.1/2.4). The side that knows the outcome of a collection is the checkout
// flow, which holds both identifiers, and this is how it hands the answer back.
//
// # The write is a MERGE, so the caller need not care about order
//
// For each field the LARGER of the recorded and the reported value is kept, so
// a late or repeated report cannot shrink a total; the reasoning is in
// [Service.SetOrderSummaryTotals]. That is what makes this safe to call from a
// place that may run twice — a retried saga step, or one day a subscriber fed
// by an at-least-once bus.
//
// # It reports nothing back
//
// The written summary is not returned. The caller is a flow that has already
// decided what happened; handing it the row would invite a second decision made
// from a value it did not compute.
func (i *Interop) SetOrderSummaryTotals(
	ctx context.Context, orderID string, paidTotal, refundedTotal int64,
) error {
	_, err := i.svc.SetOrderSummaryTotals(ctx, orderID, SummaryTotalsInput{
		PaidTotal:     paidTotal,
		RefundedTotal: refundedTotal,
	})

	return err
}

// ReturnDetailJSON returns a return with its lines and their variants.
//
// The schema is documented on [Service.ReturnDetailJSON]. The consumer is the
// return flow, which needs the variant of every line coming back in order to
// put its stock back.
func (i *Interop) ReturnDetailJSON(ctx context.Context, returnID string) (json.RawMessage, error) {
	return i.svc.ReturnDetailJSON(ctx, returnID)
}

// ReceiveReturn stamps the return as received at the given stock location.
//
// It is the RECORD half of receiving a return: it says the goods arrived and
// where. Putting the stock back is the flow's half, because it reaches the
// inventory module and this one does not know it.
//
// A second call is a no-op that KEEPS THE FIRST MOMENT, so a flow that is
// retried does not make the record claim the goods arrived twice.
func (i *Interop) ReceiveReturn(ctx context.Context, returnID, locationID string) error {
	_, err := i.svc.ReceiveReturn(ctx, returnID, locationID)

	return err
}

// CompleteOrder stamps the order as completed.
//
// It is NOT idempotent: a second call returns errors.Conflict (for the
// rationale see [Service.CompleteOrder]). Because it is a forward step, the
// saga's idempotency key already prevents the repetition.
func (i *Interop) CompleteOrder(ctx context.Context, orderID string) error {
	_, err := i.svc.CompleteOrder(ctx, orderID)
	return err
}

// interopContact is the JSON schema of the fields that the subscriber which
// will send the notification reads from the order.
//
// # Schema
//
//	{
//	  "order_id":      "order_01H…",
//	  "display_id":    "1042",       // STRING without decimals
//	  "email":         "a@b.com",    // may be EMPTY
//	  "currency_code": "TRY",
//	  "total":         "6100",       // minor unit, STRING without decimals
//	  "item_count":    "2"           // STRING without decimals
//	}
//
// # Why ALL values are strings
//
// The field names and the types are EXACTLY the same as the payload of the
// "order.placed" event (see [EventFieldOrderID] and what follows). The
// subscriber uses the two sources side by side: it takes order_id from the
// event and reads the rest from here. Had the two used different types — a
// string in the event, an integer here — the subscriber would have to parse the
// same field in two separate forms and the amount would pass through float64 on
// one side (plan Section 8: NEVER float). A single form binds the subscriber to
// a single reading rule.
//
// # Why only these fields
//
// This is the smallest set needed for the template to be fillable: to whom
// (email), which order (order_id, display_id) and how much (total,
// currency_code, item_count). Returning the whole order — the lines, the
// address, the summary — would turn the surface into a wide contract that could
// never be narrowed again: a field that enters a contract can never be taken
// out again, even if it has no consumer.
type interopContact struct {
	OrderID      string `json:"order_id"`
	DisplayID    string `json:"display_id"`
	Email        string `json:"email"`
	CurrencyCode string `json:"currency_code"`
	Total        string `json:"total"`
	ItemCount    string `json:"item_count"`
}

// OrderContactJSON returns the fields of the order needed for the notification.
//
// The schema is defined in the [interopContact] documentation and ALL of its
// values are strings. When the order does not exist (or has been soft deleted)
// errors.NotFound is returned; when the identifier is empty, errors.Invalid.
//
// # An order without an e-mail IS NOT AN ERROR
//
// Email is optional (see [CreateOrderInput.Email]): an order opened by
// administration may have no address. On such an order the field is returned as
// an EMPTY STRING, the call SUCCEEDS and the "email" key is still in the body.
//
// The alternative — returning an error on an addressless order — would be wrong
// in two places. First, this surface is a READ: it reports what the record is,
// it does not decide what the record should be; an addressless order is a valid
// record. Second, and the real one, is how the consumer would handle the error:
// for the subscriber "there is no address to send to" is a PERMANENT condition
// and must be skipped, whereas returning an error would make it
// indistinguishable from a fault that is to be retried — the subscriber would
// either retry every addressless order forever or silently swallow the real
// faults too. An empty string is a definite answer that can be told apart with
// a single check.
//
// The read is done with [Service.GetOrder]: the line count and the order header
// come from the SAME snapshot, so item_count is always the line count of the
// order the returned amount belongs to.
func (i *Interop) OrderContactJSON(ctx context.Context, orderID string) (json.RawMessage, error) {
	detail, err := i.svc.GetOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	return json.Marshal(interopContact{
		OrderID:      detail.ID,
		DisplayID:    strconv.FormatInt(detail.DisplayID, 10),
		Email:        detail.Email,
		CurrencyCode: detail.CurrencyCode,
		Total:        strconv.FormatInt(detail.Total, 10),
		ItemCount:    strconv.Itoa(len(detail.Items)),
	})
}
