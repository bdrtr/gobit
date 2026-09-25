package service

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/core/errors"
)

// trialRequest is the JSON schema of the [Interop.TrialDiscountsJSON] request.
//
//	{
//	  "entries": [
//	    {"reference": "cart_…", "request": { …the ComputeDiscountsJSON request… }}
//	  ]
//	}
//
// Each entry's "request" is exactly what [Interop.ComputeDiscountsJSON] takes and is
// decoded by the same decoder, so a purchase reaches a trial in the shape a cart
// reaches a computation. Its "codes" are ignored: the promotion under trial is
// priced as automatic.
type trialRequest struct {
	Entries []trialRequestEntry `json:"entries"`
}

// trialRequestEntry is one purchase in the request.
type trialRequestEntry struct {
	Reference string          `json:"reference"`
	Request   json.RawMessage `json:"request"`
}

// trialResponse is the JSON schema of the [Interop.TrialDiscountsJSON] response.
//
//	{
//	  "assumptions": ["active", "automatic", "no_usage_limit", "no_campaign"],
//	  "entries": [
//	    {"reference": "cart_…", "already_applied": false, "skipped": "",
//	     "items": [{"id": "li_1", "amount": 1000}], "items_discount_total": 1000}
//	  ]
//	}
//
// "entries" are in the request's order, one per request entry. "items" is empty for
// an entry the promotion was already redeemed on; otherwise it carries one record per
// line in the request's order. "skipped" is a [SkipReason] or empty.
type trialResponse struct {
	Assumptions []string             `json:"assumptions"`
	Entries     []trialResponseEntry `json:"entries"`
}

// trialResponseEntry is one purchase in the response.
type trialResponseEntry struct {
	Reference          string                `json:"reference"`
	AlreadyApplied     bool                  `json:"already_applied"`
	Skipped            string                `json:"skipped"`
	Items              []interopLineDiscount `json:"items"`
	ItemsDiscountTotal int64                 `json:"items_discount_total"`
}

// TrialDiscountsJSON prices one promotion against past purchases as if it were
// published; it WRITES NOTHING (ADR 0176).
//
// The schema is in the [trialRequest] and [trialResponse] godocs, and what the
// trial sets aside is in [Service.TrialDiscounts]'s.
//
// The counterpart on the consumer side:
//
//	type PromotionTrial interface {
//	    TrialDiscountsJSON(ctx context.Context, promotionID string, request json.RawMessage) (json.RawMessage, error)
//	}
func (i *Interop) TrialDiscountsJSON(
	ctx context.Context, promotionID string, request json.RawMessage,
) (json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(request))
	dec.DisallowUnknownFields()

	var req trialRequest
	if err := dec.Decode(&req); err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, CodeInteropRequestInvalid,
			"the trial request could not be parsed")
	}

	entries := make([]TrialEntry, 0, len(req.Entries))
	for idx := range req.Entries {
		in, err := decodeInteropRequest(req.Entries[idx].Request)
		if err != nil {
			return nil, err
		}
		entries = append(entries, TrialEntry{Reference: req.Entries[idx].Reference, Input: in})
	}

	outcomes, err := i.svc.TrialDiscounts(ctx, promotionID, entries)
	if err != nil {
		return nil, err
	}

	response := trialResponse{
		Assumptions: TrialAssumptions,
		Entries:     make([]trialResponseEntry, 0, len(outcomes)),
	}
	for idx := range outcomes {
		entry := trialResponseEntry{
			Reference:          outcomes[idx].Reference,
			AlreadyApplied:     outcomes[idx].AlreadyApplied,
			Skipped:            string(outcomes[idx].Skipped),
			Items:              make([]interopLineDiscount, 0, len(outcomes[idx].Result.Items)),
			ItemsDiscountTotal: outcomes[idx].Result.ItemsDiscountTotal,
		}
		for _, line := range outcomes[idx].Result.Items {
			entry.Items = append(entry.Items, interopLineDiscount(line))
		}
		response.Entries = append(response.Entries, entry)
	}

	payload, err := json.Marshal(response)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeInteropResponseInvalid,
			"the trial result could not be converted to JSON")
	}

	return payload, nil
}
