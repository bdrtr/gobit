package api

import (
	"net/http"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/tax/service"
)

// createRegion is the POST /admin/v1/tax-regions handler.
func (a *API) createRegion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createTaxRegionRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	region, err := a.svc.CreateTaxRegion(ctx, toCreateTaxRegionInput(body))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toTaxRegionDTO(region))
}

// listRegions is the GET /admin/v1/tax-regions handler.
//
// The "country_code" query parameter narrows the list to a single country;
// without it every region comes back.
func (a *API) listRegions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, offset, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	page, err := a.svc.ListTaxRegions(ctx, r.URL.Query().Get("country_code"), limit, offset)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writePage(w, r, page, toTaxRegionDTO)
}

// getRegion is the GET /admin/v1/tax-regions/{id} handler.
func (a *API) getRegion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	region, err := a.svc.GetTaxRegion(ctx, pathParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toTaxRegionDTO(region))
}

// deleteRegion is the DELETE /admin/v1/tax-regions/{id} handler.
//
// The delete covers the TREE: the child regions, their rates and those rates'
// rules are soft-deleted too (see service.Service.DeleteTaxRegion). The
// response has no body; returning a dump of the deleted tree would mean
// producing, on every call, a list the client does not need.
func (a *API) deleteRegion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := a.svc.DeleteTaxRegion(ctx, pathParam(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// listRegionRates is the GET /admin/v1/tax-regions/{id}/tax-rates handler.
func (a *API) listRegionRates(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rates, err := a.svc.ListTaxRates(ctx, pathParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeAll(w, r, rates, toTaxRateDTO)
}

// createRate is the POST /admin/v1/tax-rates handler.
func (a *API) createRate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createTaxRateRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	rate, err := a.svc.CreateTaxRate(ctx, toCreateTaxRateInput(body))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toTaxRateDTO(rate))
}

// listRates is the GET /admin/v1/tax-rates handler.
//
// The "tax_region_id" query parameter is REQUIRED: rates always belong to a
// region, and a list of rates without a region is a table that does not say
// which geography it belongs to. If it is missing errors.Invalid is returned.
func (a *API) listRates(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	regionID := r.URL.Query().Get("tax_region_id")
	if regionID == "" {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidBody,
			"the %q query parameter is required", "tax_region_id"))
		return
	}

	rates, err := a.svc.ListTaxRates(ctx, regionID)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeAll(w, r, rates, toTaxRateDTO)
}

// getRate is the GET /admin/v1/tax-rates/{id} handler.
func (a *API) getRate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rate, err := a.svc.GetTaxRate(ctx, pathParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toTaxRateDTO(rate))
}

// updateRate is the PUT /admin/v1/tax-rates/{id} handler.
//
// Although the method is PUT, the semantics are PARTIAL: a field not given does
// not change. This is a deliberate simplification — offering PATCH as a
// separate method would mean two body shapes and two validation paths, while a
// PUT that is not partial would silently reset a rate somebody forgot to send.
func (a *API) updateRate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body updateTaxRateRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	rate, err := a.svc.UpdateTaxRate(ctx, pathParam(r, "id"), toUpdateTaxRateInput(body))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toTaxRateDTO(rate))
}

// deleteRate is the DELETE /admin/v1/tax-rates/{id} handler.
func (a *API) deleteRate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := a.svc.DeleteTaxRate(ctx, pathParam(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// createRule is the POST /admin/v1/tax-rates/{id}/rules handler.
//
// The rate id is taken FROM THE PATH: the rule is a sub-resource of the rate,
// and were the id carried a second time in the body, the path and the body
// could contradict each other.
func (a *API) createRule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createTaxRateRuleRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	rule, err := a.svc.CreateRateRule(ctx, service.CreateRateRuleInput{
		TaxRateID:   pathParam(r, "id"),
		Reference:   body.Reference,
		ReferenceID: body.ReferenceID,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toTaxRateRuleDTO(rule))
}

// listRules is the GET /admin/v1/tax-rates/{id}/rules handler.
func (a *API) listRules(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rules, err := a.svc.ListRateRules(ctx, pathParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeAll(w, r, rules, toTaxRateRuleDTO)
}

// deleteRule is the DELETE /admin/v1/tax-rates/{id}/rules/{ruleID} handler.
//
// The rate id in the path only states where the resource lives; the delete is
// done by rule id. Whether the two agree is not checked separately — deleting
// another rate's rule through this path only means the path was written
// nonsensically, and the outcome still deletes the right record.
func (a *API) deleteRule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := a.svc.DeleteRateRule(ctx, pathParam(r, "ruleID")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}
