package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// This file holds the rule endpoints of a shipping option, under
// /admin/v1/shipping-options/{id}/rules. A rule is a CHILD record with routes
// of its own: it is added and deleted one by one and never travels in the
// option's update body, which is why it is kept apart from the option CRUD.

// createRuleRequest is the body of
// POST /admin/v1/shipping-options/{id}/rules.
type createRuleRequest struct {
	Attribute string   `json:"attribute"`
	Operator  string   `json:"operator"`
	Values    []string `json:"values"`
}

// createRule adds a rule to an option.
func (h *Handler) createRule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createRuleRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	rule, err := h.svc.CreateShippingOptionRule(ctx, chi.URLParam(r, "id"), service.CreateRuleInput{
		Attribute: body.Attribute,
		Operator:  body.Operator,
		Values:    body.Values,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toRuleDTO(rule)})
}

// listRules returns the rules of an option.
func (h *Handler) listRules(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rules, err := h.svc.ListShippingOptionRules(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]ruleDTO, 0, len(rules))
	for i := range rules {
		data = append(data, toRuleDTO(rules[i]))
	}
	writeList(ctx, w, data)
}

// deleteRule soft deletes the rule.
func (h *Handler) deleteRule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.DeleteShippingOptionRule(ctx, chi.URLParam(r, "rule_id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}
