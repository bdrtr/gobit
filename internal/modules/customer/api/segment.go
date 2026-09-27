package api

import (
	"context"
	"encoding/json"
	"net/http"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

// codePreviewNotWired reports a handler built without the segment flow.
const codePreviewNotWired = "customer_segment_preview_not_wired"

// SegmentPreview is the segment flow the preview endpoint runs on (ADR 0217).
//
// The flow is a workflow this package cannot import; the module resolves it
// from the container on first use (see the module's segmentPreview).
type SegmentPreview interface {
	// PreviewSegmentJSON counts the customers a normalized rule would take in:
	// {"members": 12, "customers": 480}.
	PreviewSegmentJSON(ctx context.Context, rule json.RawMessage) (json.RawMessage, error)
}

// WithPreview binds the flow the preview endpoint runs on and returns the
// handler.
func (h *Handler) WithPreview(preview SegmentPreview) *Handler {
	h.preview = preview

	return h
}

// segmentRuleDTO is a segment's rule in a request and a response.
type segmentRuleDTO struct {
	// CurrencyCode is the currency net_spend is summed in; only a rule with a
	// net_spend condition carries one.
	CurrencyCode string `json:"currency_code,omitempty"`
	// WindowDays is how many days back order_count and net_spend read; zero or
	// absent reads the whole history. Only a rule with one of them carries it.
	WindowDays int32 `json:"window_days,omitempty"`
	// Conditions are ANDed.
	Conditions []segmentConditionDTO `json:"conditions"`
}

// segmentConditionDTO compares one attribute of a customer with a value.
type segmentConditionDTO struct {
	// Attribute is has_account, account_age_days, country_code, order_count or
	// net_spend.
	Attribute string `json:"attribute"`
	// Operator is eq, ne, gt, gte, lt, lte, in or nin, as the attribute takes.
	Operator string `json:"operator"`
	// Value is a whole number, a boolean for has_account, or a country code for
	// country_code with eq or ne.
	Value json.RawMessage `json:"value,omitempty"`
	// Values are the country codes of an in or nin condition.
	Values []string `json:"values,omitempty"`
}

// segmentPreviewDTO is how many customers a rule would take in.
type segmentPreviewDTO struct {
	// Members is how many live customers satisfy the rule now.
	Members int `json:"members"`
	// Customers is how many live customers were read.
	Customers int `json:"customers"`
}

// toSegmentRuleDTO converts a stored rule for the response; nil stays nil.
func toSegmentRuleDTO(rule *models.SegmentRule) *segmentRuleDTO {
	if rule == nil {
		return nil
	}
	out := &segmentRuleDTO{CurrencyCode: rule.CurrencyCode, WindowDays: rule.WindowDays}
	for _, c := range rule.Conditions {
		out.Conditions = append(out.Conditions, segmentConditionDTO{
			Attribute: c.Attribute, Operator: c.Operator, Value: c.Value, Values: c.Values,
		})
	}
	return out
}

// toSegmentRule converts a request's rule.
func (r *segmentRuleDTO) toSegmentRule() models.SegmentRule {
	out := models.SegmentRule{CurrencyCode: r.CurrencyCode, WindowDays: r.WindowDays}
	for _, c := range r.Conditions {
		out.Conditions = append(out.Conditions, models.SegmentCondition{
			Attribute: c.Attribute, Operator: c.Operator, Value: c.Value, Values: c.Values,
		})
	}
	return out
}

// adminSetGroupSegment gives a group the rule that decides its members
// (PUT /admin/v1/customer-groups/{id}/segment, ADR 0217).
func (h *Handler) adminSetGroupSegment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req segmentRuleDTO
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	group, err := h.svc.SetGroupSegment(ctx, pathParam(r, paramID), req.toSegmentRule())
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toGroupDTO(group))
}

// adminClearGroupSegment hands a segment back to the operator
// (DELETE /admin/v1/customer-groups/{id}/segment).
func (h *Handler) adminClearGroupSegment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	group, err := h.svc.ClearGroupSegment(ctx, pathParam(r, paramID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toGroupDTO(group))
}

// adminPreviewSegment counts the customers a rule would take in, writing
// nothing (POST /admin/v1/customer-segments/preview).
func (h *Handler) adminPreviewSegment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req segmentRuleDTO
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	rule, err := service.NormalizeSegmentRule(req.toSegmentRule())
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	if h.preview == nil {
		corehttp.WriteError(ctx, w, coreerrors.Internal(codePreviewNotWired,
			"the segment preview is not wired to the segment flow"))
		return
	}
	body, err := json.Marshal(rule)
	if err != nil {
		corehttp.WriteError(ctx, w, coreerrors.Wrap(err, coreerrors.KindInternal, codePreviewNotWired,
			"the rule could not be encoded"))
		return
	}
	raw, err := h.preview.PreviewSegmentJSON(ctx, body)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	var out segmentPreviewDTO
	if err := json.Unmarshal(raw, &out); err != nil {
		corehttp.WriteError(ctx, w, coreerrors.Wrap(err, coreerrors.KindInternal, codePreviewNotWired,
			"the segment flow's preview could not be read"))
		return
	}
	writeItem(w, r, http.StatusOK, out)
}
