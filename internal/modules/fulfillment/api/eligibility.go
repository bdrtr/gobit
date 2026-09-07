package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// This file holds the eligibility listing: which shipping options are open for
// a cart context, and at what rate. It is the one surface here with TWO faces —
// the store endpoint and the admin preview — and what separates them is a TRUST
// decision: whether admin_only options are shown and whether the cart facts in
// the query are believed. The two handlers and the query parser they share are
// kept in one file so that decision can be read in a single place.

// listStoreEligibleOptions is the GET /store/v1/shipping-options endpoint.
//
// admin_only options are NEVER returned and the client cannot ask for them: the
// flag is not read from a query parameter, it is passed as a constant false.
// Had it been read, a single parameter coming from the storefront would open
// the admin-only options.
//
// # Cart facts CANNOT BE VERIFIED HERE
//
// subtotal, item_count and total_weight come from query parameters; the cart is
// the cart module's data and this module can neither compute nor verify it
// (Principle 2.1). So all three are the CLIENT'S CLAIM: sending
// "?subtotal=50000" with an empty cart is allowed.
//
// The endpoint therefore goes to the service with TrustedFacts=false, and that
// flag removes from the list every option that has a rule DEPENDING on these
// three facts (rationale: [service.Service.ListShippingOptionsFor]). The
// endpoint thereby stops being a "rule oracle": a made-up subtotal no longer
// opens an option that is closed to anyone.
//
// The remaining two limits are EXPLICITLY accepted:
//
//   - The price is a PRESENTATION. The rate of a "calculated" option is
//     computed with the weight the client reported; the real rate has to be
//     determined again at the payment step with the cart's real facts.
//   - The free rule context ([service.ListOptionsInput.Attributes]) is NEVER
//     read from this endpoint. The consequence: options bound to a field such
//     as "customer_group_id" cannot be listed on the HTTP eligibility endpoints
//     (the admin endpoint included). Reading it would mean letting the customer
//     declare their own group; the owner of the real value is not this module
//     and the path that carries the context is [service.Interop].
func (h *Handler) listStoreEligibleOptions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	in, err := parseEligibilityQuery(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	in.IncludeAdminOnly = false
	in.TrustedFacts = false

	quoted, err := h.svc.ListShippingOptionsFor(ctx, in)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]storeOptionDTO, 0, len(quoted))
	for i := range quoted {
		data = append(data, toStoreOptionDTO(quoted[i]))
	}
	writeList(ctx, w, data)
}

// listAdminEligibleOptions is the GET /admin/v1/shipping-options/eligible
// endpoint.
//
// The admin surface sees admin_only options TOO; that is the distinction.
//
// Cart facts are considered TRUSTED here (TrustedFacts=true) and rule-bound
// options are listed. The rationale: this endpoint is a PREVIEW tool with which
// the administrator tries out "which options come up in this context"; since
// the administrator can already read the whole catalog and its rules, making up
// a context opens nothing new to them. The same assumption does NOT hold on the
// store endpoint (see [Handler.listStoreEligibleOptions]).
func (h *Handler) listAdminEligibleOptions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	in, err := parseEligibilityQuery(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	in.IncludeAdminOnly = true
	in.TrustedFacts = true

	quoted, err := h.svc.ListShippingOptionsFor(ctx, in)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]quotedOptionDTO, 0, len(quoted))
	for i := range quoted {
		data = append(data, toQuotedOptionDTO(quoted[i]))
	}
	writeList(ctx, w, data)
}

// parseEligibilityQuery parses the query parameters of the eligibility listing.
//
// IncludeAdminOnly and TrustedFacts are NOT READ HERE: both are a TRUST
// decision and their values are fixed according to which surface the handler
// belongs to (see [Handler.listStoreEligibleOptions]). Had they been read from
// the query, a single parameter coming from the storefront would open both
// doors.
func parseEligibilityQuery(r *http.Request) (service.ListOptionsInput, error) {
	query := r.URL.Query()

	subtotal, err := parseInt64Param(r, "subtotal")
	if err != nil {
		return service.ListOptionsInput{}, err
	}
	itemCount, err := parseInt64Param(r, "item_count")
	if err != nil {
		return service.ListOptionsInput{}, err
	}
	totalWeight, err := parseInt64Param(r, "total_weight")
	if err != nil {
		return service.ListOptionsInput{}, err
	}
	isReturn, err := parseBoolParam(r, "is_return")
	if err != nil {
		return service.ListOptionsInput{}, err
	}

	return service.ListOptionsInput{
		RegionID:     query.Get("region_id"),
		CurrencyCode: query.Get("currency_code"),
		CountryCode:  query.Get("country_code"),
		// The profile identifier is a REPEATABLE parameter: a cart may contain
		// products bound to several profiles and all of them have to be asked
		// at once.
		ShippingProfileIDs: query["shipping_profile_id"],
		Subtotal:           subtotal,
		ItemCount:          itemCount,
		TotalWeight:        totalWeight,
		IsReturn:           isReturn,
	}, nil
}
