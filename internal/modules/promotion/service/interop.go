package service

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// This file is the CROSS-MODULE surface of the promotion module (ADR 0001, ADR 0006).
//
// The cart flow and the order completion saga under internal/workflows CANNOT import
// this module. The solution is the same as the interop.go of the
// region/cart/payment/order/inventory modules: publish a surface that uses only
// PRIMITIVE and stdlib types. The consumer defines its own narrow interface, this
// type satisfies it STRUCTURALLY, and it is resolved from the container under the
// name "promotion.interop".
//
// The reason is Go's structural conformance rule: because the consumer cannot import
// promotion, it cannot name a type such as [ComputeInput] in its signature; the
// moment it names one, that becomes ANOTHER type defined in its own package and the
// concrete service does not satisfy the consumer's interface.
//
// Composite data (the cart context and the computed discounts) travels as JSON and
// the schema is declared EXPLICITLY below. It MUST be exactly the same as the schema
// on the consumer side, and conformance can only be proven by an integration test:
// because this module cannot import the workflow package, the compiler cannot check
// the match.

// Interop error codes.
const (
	// CodeInteropRequestInvalid reports that a request body arrived that cannot be
	// parsed.
	CodeInteropRequestInvalid = "promotion_interop_request_invalid"
	// CodeInteropResponseInvalid reports that the result could not be converted to
	// JSON.
	CodeInteropResponseInvalid = "promotion_interop_response_invalid"
)

// Interop turns the promotion service into a PRIMITIVE cross-module surface.
//
// It makes no decisions: it only translates the signature and the JSON schema. All
// business rules stay on [Service]; adding a rule here would mean the same rule
// drifting apart in two places.
type Interop struct {
	svc *Service
}

// NewInterop sets up the cross-module surface for the given service.
func NewInterop(svc *Service) *Interop { return &Interop{svc: svc} }

// interopRequest is the JSON schema of the [Interop.ComputeDiscountsJSON] request.
//
// The field names must be exactly the same as the schema on the consumer side. All
// amounts are INTEGER minor units (plan Section 8).
//
//	{
//	  "currency_code": "TRY",
//	  "context": {"region_id": "reg_1", "customer_group_id": "vip"},
//	  "items": [
//	    {"id": "li_1", "amount": 25000, "quantity": 2,
//	     "attributes": {"product_category_id": "cat_1"}}
//	  ],
//	  "shipping_methods": [{"id": "sm_1", "amount": 4990, "attributes": {}}],
//	  "codes": ["SUMMER20"],
//	  "at": "2026-08-24T10:00:00Z"
//	}
//
// "context", "attributes" and "codes" may be left empty. If "at" is left empty, "now"
// is used; it is expected in RFC 3339 format.
type interopRequest struct {
	CurrencyCode    string                   `json:"currency_code"`
	Context         map[string]string        `json:"context"`
	Items           []interopRequestItem     `json:"items"`
	ShippingMethods []interopRequestShipping `json:"shipping_methods"`
	Codes           []string                 `json:"codes"`
	At              string                   `json:"at"`
}

// interopRequestItem is the schema of a single cart line in the request.
type interopRequestItem struct {
	ID         string            `json:"id"`
	Amount     int64             `json:"amount"`
	Quantity   int64             `json:"quantity"`
	Attributes map[string]string `json:"attributes"`
}

// interopRequestShipping is the schema of a single shipping method in the request.
type interopRequestShipping struct {
	ID         string            `json:"id"`
	Amount     int64             `json:"amount"`
	Attributes map[string]string `json:"attributes"`
}

// interopResponse is the JSON schema of the [Interop.ComputeDiscountsJSON] response.
//
//	{
//	  "currency_code": "TRY",
//	  "items": [{"id": "li_1", "amount": 5000}],
//	  "shipping_methods": [{"id": "sm_1", "amount": 0}],
//	  "items_discount_total": 5000,
//	  "shipping_discount_total": 0,
//	  "discount_total": 5000,
//	  "applied": [
//	    {"promotion_id": "promo_…", "code": "SUMMER20",
//	     "is_automatic": false, "amount": 5000}
//	  ],
//	  "unmatched_codes": []
//	}
//
// Invariants (the consumer may rely on these):
//
//   - "items" and "shipping_methods" carry one record for EVERY line in the request
//     and are in the SAME order as the request; the ones whose discount is zero are
//     present too.
//   - The discount of each line DOES NOT EXCEED that line's amount.
//   - discount_total = items_discount_total + shipping_discount_total
//     = Σ items[i].amount + Σ shipping_methods[i].amount
//     = Σ applied[i].amount
type interopResponse struct {
	CurrencyCode          string                `json:"currency_code"`
	Items                 []interopLineDiscount `json:"items"`
	ShippingMethods       []interopLineDiscount `json:"shipping_methods"`
	ItemsDiscountTotal    int64                 `json:"items_discount_total"`
	ShippingDiscountTotal int64                 `json:"shipping_discount_total"`
	DiscountTotal         int64                 `json:"discount_total"`
	Applied               []interopApplied      `json:"applied"`
	UnmatchedCodes        []string              `json:"unmatched_codes"`
}

// interopLineDiscount is the schema of a single line discount in the response.
type interopLineDiscount struct {
	ID     string `json:"id"`
	Amount int64  `json:"amount"`
}

// interopApplied is the schema of a single applied promotion in the response.
type interopApplied struct {
	PromotionID string `json:"promotion_id"`
	Code        string `json:"code"`
	IsAutomatic bool   `json:"is_automatic"`
	Amount      int64  `json:"amount"`
}

// ComputeDiscountsJSON computes the discounts for a cart context; it WRITES NOTHING.
//
// The schema is defined in the [interopRequest] and [interopResponse] godocs. The
// rules of the computation (elimination, order, non-compounding, upper bounds,
// rounding, budget) are in the [Service.ComputeDiscounts] godoc and this surface does
// NOT change them.
//
// Numbers are decoded straight into int64 fields rather than through json.Number: the
// schema declares every amount as an integer, and an amount that went through a
// float64 would be silently corrupted at the cent level (plan Section 8). Unknown
// fields are REJECTED — a field that is silently ignored means that a line the
// consumer believed it had sent never entered the computation at all.
//
// The counterpart on the consumer side:
//
//	type DiscountCalculator interface {
//	    ComputeDiscountsJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error)
//	}
func (i *Interop) ComputeDiscountsJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error) {
	in, err := decodeInteropRequest(request)
	if err != nil {
		return nil, err
	}

	result, err := i.svc.ComputeDiscounts(ctx, in)
	if err != nil {
		return nil, err
	}

	response := interopResponse{
		CurrencyCode:          result.CurrencyCode,
		Items:                 make([]interopLineDiscount, 0, len(result.Items)),
		ShippingMethods:       make([]interopLineDiscount, 0, len(result.ShippingMethods)),
		ItemsDiscountTotal:    result.ItemsDiscountTotal,
		ShippingDiscountTotal: result.ShippingDiscountTotal,
		DiscountTotal:         result.DiscountTotal,
		Applied:               make([]interopApplied, 0, len(result.Applied)),
		UnmatchedCodes:        result.UnmatchedCodes,
	}
	for idx := range result.Items {
		response.Items = append(response.Items, interopLineDiscount{
			ID:     result.Items[idx].ID,
			Amount: result.Items[idx].Amount,
		})
	}
	for idx := range result.ShippingMethods {
		response.ShippingMethods = append(response.ShippingMethods, interopLineDiscount{
			ID:     result.ShippingMethods[idx].ID,
			Amount: result.ShippingMethods[idx].Amount,
		})
	}
	for idx := range result.Applied {
		response.Applied = append(response.Applied, interopApplied{
			PromotionID: result.Applied[idx].PromotionID,
			Code:        result.Applied[idx].Code,
			IsAutomatic: result.Applied[idx].IsAutomatic,
			Amount:      result.Applied[idx].Amount,
		})
	}

	payload, err := json.Marshal(response)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeInteropResponseInvalid,
			"the discount result could not be converted to JSON")
	}
	return payload, nil
}

// RedeemPromotion uses the coupon for a reference and returns the identifier of the
// redemption record.
//
// It is IDEMPOTENT: a second call with the same reference writes no new record and
// does not increment the counters, it returns the identifier of the existing record
// (see [Service.RedeemPromotion]).
//
// At least one of promotionID or code must be set; if both are set they must point at
// the same promotion.
//
// If the promotion is not PUBLISHED, if its campaign's window does not contain the
// moment of use, or if a counter bound would be exceeded, it returns errors.Conflict
// and nothing is written; the full list of reasons is in the
// [Service.RedeemPromotion] godoc. The caller is NOT EXPECTED to do a status check —
// this call is the referee.
//
// The counterpart on the consumer side:
//
//	type PromotionRedeemer interface {
//	    RedeemPromotion(ctx context.Context, promotionID, code, reference, currencyCode string, amount int64) (string, error)
//	}
func (i *Interop) RedeemPromotion(
	ctx context.Context,
	promotionID, code, reference, currencyCode string,
	amount int64,
) (string, error) {
	redemption, err := i.svc.RedeemPromotion(ctx, RedeemInput{
		PromotionID:  promotionID,
		Code:         code,
		Reference:    reference,
		Amount:       amount,
		CurrencyCode: currencyCode,
	})
	if err != nil {
		return "", err
	}
	return redemption.ID, nil
}

// ReleasePromotion releases a use; this is the SAGA COMPENSATION and it is
// IDEMPOTENT.
//
// If it is called twice the second call DOES NOT error and the counters are not
// decremented a second time. If no use was ever written it does not error either. The
// returned bool reports whether anything was reversed BY THIS CALL.
//
// An unknown promotion identifier/code returns errors.NotFound; the compensation does
// not silently swallow a record that does not exist.
//
// The counterpart on the consumer side:
//
//	type PromotionReleaser interface {
//	    ReleasePromotion(ctx context.Context, promotionID, code, reference string) (bool, error)
//	}
func (i *Interop) ReleasePromotion(ctx context.Context, promotionID, code, reference string) (bool, error) {
	return i.svc.ReleasePromotion(ctx, ReleaseInput{
		PromotionID: promotionID,
		Code:        code,
		Reference:   reference,
	})
}

// decodeInteropRequest turns the raw JSON body into the computation input.
func decodeInteropRequest(raw json.RawMessage) (ComputeInput, error) {
	if len(raw) == 0 {
		return ComputeInput{}, errors.Invalid(CodeInteropRequestInvalid,
			"the discount request cannot be empty")
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()

	var req interopRequest
	if err := dec.Decode(&req); err != nil {
		return ComputeInput{}, errors.Wrap(err, errors.KindInvalid, CodeInteropRequestInvalid,
			"the discount request could not be parsed")
	}

	at, err := parseInteropTime(req.At)
	if err != nil {
		return ComputeInput{}, err
	}

	items := make([]ComputeItem, 0, len(req.Items))
	for idx := range req.Items {
		items = append(items, ComputeItem{
			ID:         req.Items[idx].ID,
			Amount:     req.Items[idx].Amount,
			Quantity:   req.Items[idx].Quantity,
			Attributes: req.Items[idx].Attributes,
		})
	}
	shipping := make([]ComputeShippingMethod, 0, len(req.ShippingMethods))
	for idx := range req.ShippingMethods {
		shipping = append(shipping, ComputeShippingMethod{
			ID:         req.ShippingMethods[idx].ID,
			Amount:     req.ShippingMethods[idx].Amount,
			Attributes: req.ShippingMethods[idx].Attributes,
		})
	}

	return ComputeInput{
		CurrencyCode:    req.CurrencyCode,
		Context:         req.Context,
		Items:           items,
		ShippingMethods: shipping,
		Codes:           req.Codes,
		At:              at,
	}, nil
}

// parseInteropTime parses an RFC 3339 formatted moment; an empty string returns the
// zero time.
//
// The zero time means "now" and [Service.ComputeDiscounts] fills it in from its own
// clock. A stamp that cannot be parsed does not silently fall back to "now": if the
// consumer asked for a backdated computation and the stamp is broken, a computation
// made with today's campaigns is the wrong answer.
func parseInteropTime(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, errors.Wrap(err, errors.KindInvalid, CodeInteropRequestInvalid,
			"the computation moment must be in RFC 3339 format, %q was given", raw)
	}
	return parsed.UTC(), nil
}
