package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// createPriceSet creates a new price set (POST /admin/v1/price-sets).
//
// The prices in the body are written in the same request; if one of them is
// invalid, none is written and the container is not created either.
func (a *API) createPriceSet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req createPriceSetRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	set, err := a.svc.CreatePriceSet(ctx, toPriceInputs(req.Prices))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	prices, err := a.svc.ListPrices(ctx, set.ID)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toPriceSetDTO(set, prices))
}

// listPriceSets lists price sets page by page (GET /admin/v1/price-sets).
//
// The list response carries NO prices: fetching every price of a page full of
// containers would produce a large body the caller almost never uses. Prices
// are read from the single-record endpoint or from the Query layer.
func (a *API) listPriceSets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, offset, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	page, err := a.svc.ListPriceSets(ctx, limit, offset)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writePage(w, r, page, toPriceSetSummaryDTO)
}

// getPriceSet returns a single price set with its prices
// (GET /admin/v1/price-sets/{id}).
func (a *API) getPriceSet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := pathID(r, "id")

	set, err := a.svc.GetPriceSet(ctx, id)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	prices, err := a.svc.ListPrices(ctx, id)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toPriceSetDTO(set, prices))
}

// deletePriceSet soft-deletes the price set and its prices
// (DELETE /admin/v1/price-sets/{id}).
func (a *API) deletePriceSet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := a.svc.DeletePriceSet(ctx, pathID(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// listPrices returns a container's prices
// (GET /admin/v1/price-sets/{id}/prices).
func (a *API) listPrices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	prices, err := a.svc.ListPrices(ctx, pathID(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItems(w, r, toPriceDTOs(prices))
}

// setPrices REPLACES a container's prices in bulk
// (POST /admin/v1/price-sets/{id}/prices).
//
// The operation is a replacement: prices absent from the body are deleted. The
// write is atomic.
func (a *API) setPrices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req setPricesRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	prices, err := a.svc.SetPrices(ctx, pathID(r, "id"), toPriceInputs(req.Prices))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItems(w, r, toPriceDTOs(prices))
}

// calculatePrice selects a container's valid price in the given context
// (GET /admin/v1/price-sets/{id}/calculate).
//
// It is a GET, not a POST. The endpoint used to be a POST, and the reason was
// written down as "the context is a structured body; flattened into a query
// string, nested values are lost". That reason has no counterpart in the
// types: service.CalculateParams is flat and the rule context is a
// map[string]string — there is no nested value to lose. The cost, on the other
// hand, was concrete: because the scope vocabulary looks at the method (see
// [API.Routes]), this endpoint, which writes nothing, asked for [ScopeWrite],
// and integrations that only read prices — price comparison, export — had to
// run with an identity that CAN WRITE prices.
//
// Query shape:
//
//	?currency_code=TRY&quantity=10&at=2026-06-15T12:00:00Z&attr_region_id=reg_1
//
// The rule context is carried not as a single structured value but as
// separate parameters with the [paramAttrPrefix] PREFIX: the field names are
// the model's own snake_case names, and the prefix is enough to tell them apart
// from the reserved parameters ([paramCurrencyCode], [paramQuantity],
// [paramAt]). Two alternatives were ruled out: a JSON object embedded in the
// query ("attributes={...}") and the "attributes[region_id]" form. Both make
// the URL unreadable by hand and bring a second parser into the HTTP layer,
// and in return there is no nested structure for them to parse.
//
// The timestamp is RFC 3339, and the "+" in a time zone offset has to be
// percent-encoded in the query string ("%2B"); otherwise net/url decodes it as
// a space. The "Z" form never runs into this trap.
func (a *API) calculatePrice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	params, err := calculateQuery(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	calculated, err := a.svc.CalculatePrice(ctx, pathID(r, "id"), params)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toCalculatedPriceDTO(calculated))
}

// createPriceList creates a new price list (POST /admin/v1/price-lists).
func (a *API) createPriceList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req priceListRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	list, err := a.svc.CreatePriceList(ctx, toPriceListInput(req))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toPriceListDTO(list))
}

// listPriceLists returns price lists page by page
// (GET /admin/v1/price-lists).
func (a *API) listPriceLists(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, offset, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	page, err := a.svc.ListPriceLists(ctx, limit, offset)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writePage(w, r, page, toPriceListDTO)
}

// getPriceList returns a single price list (GET /admin/v1/price-lists/{id}).
func (a *API) getPriceList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	list, err := a.svc.GetPriceList(ctx, pathID(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toPriceListDTO(list))
}

// updatePriceList writes every field of the price list
// (PUT /admin/v1/price-lists/{id}).
//
// It is a PUT because it is not a partial update: fields absent from the body
// are reset.
func (a *API) updatePriceList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req priceListRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	list, err := a.svc.UpdatePriceList(ctx, pathID(r, "id"), toPriceListInput(req))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toPriceListDTO(list))
}

// deletePriceList soft-deletes the price list
// (DELETE /admin/v1/price-lists/{id}).
func (a *API) deletePriceList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := a.svc.DeletePriceList(ctx, pathID(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// listPriceRules returns a price's rules
// (GET /admin/v1/prices/{price_id}/rules).
func (a *API) listPriceRules(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rules, err := a.svc.ListPriceRules(ctx, pathID(r, "price_id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItems(w, r, toPriceRuleDTOs(rules))
}

// createPriceRule adds a rule to a price
// (POST /admin/v1/prices/{price_id}/rules).
func (a *API) createPriceRule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req ruleRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	rules := toRuleInputs([]ruleRequest{req})
	rule, err := a.svc.CreatePriceRule(ctx, pathID(r, "price_id"), rules[0])
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toPriceRuleDTO(rule))
}

// deletePriceRule soft-deletes the rule
// (DELETE /admin/v1/price-rules/{id}).
func (a *API) deletePriceRule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := a.svc.DeletePriceRule(ctx, pathID(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}
