package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// listProviders returns the identifiers of the registered shipping providers.
//
// It is bound ONLY to the admin surface: which carriers the store works with is
// the store's operational information and is not shown to the customer (this is
// the difference from the payment providers — there the customer has to know
// which payment method to choose).
func (h *Handler) listProviders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	writeList(ctx, w, h.svc.ProviderIDs(ctx))
}

// --- shipping profiles -------------------------------------------------------

// createProfileRequest is the body of POST /admin/v1/shipping-profiles.
type createProfileRequest struct {
	Name     string         `json:"name"`
	Type     string         `json:"type"`
	Metadata map[string]any `json:"metadata"`
}

// createProfile creates a new shipping profile.
func (h *Handler) createProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createProfileRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	profile, err := h.svc.CreateShippingProfile(ctx, service.CreateProfileInput{
		Name:     body.Name,
		Type:     body.Type,
		Metadata: body.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toProfileDTO(profile)})
}

// listProfiles returns the shipping profiles page by page.
func (h *Handler) listProfiles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	in := service.ListProfilesInput{Page: page}
	if raw := r.URL.Query().Get("type"); raw != "" {
		in.Type = &raw
	}

	profiles, count, err := h.svc.ListShippingProfiles(ctx, in)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]profileDTO, 0, len(profiles))
	for i := range profiles {
		data = append(data, toProfileDTO(profiles[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:   data,
		Count:  count,
		Offset: page.Offset,
		Limit:  page.Limit,
	})
}

// getProfile returns the profile by its identifier.
func (h *Handler) getProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	profile, err := h.svc.GetShippingProfile(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toProfileDTO(profile)})
}

// updateProfileRequest is the body of PATCH /admin/v1/shipping-profiles/{id}.
//
// The fields are POINTERS: the distinction between "not sent" and "sent empty"
// is preserved; a field that is not sent does not change.
type updateProfileRequest struct {
	Name     *string        `json:"name"`
	Type     *string        `json:"type"`
	Metadata map[string]any `json:"metadata"`
}

// updateProfile updates the given fields of the profile.
func (h *Handler) updateProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body updateProfileRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	profile, err := h.svc.UpdateShippingProfile(ctx, chi.URLParam(r, "id"), service.UpdateProfileInput{
		Name:     body.Name,
		Type:     body.Type,
		Metadata: body.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toProfileDTO(profile)})
}

// deleteProfile soft deletes the profile.
func (h *Handler) deleteProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.DeleteShippingProfile(ctx, chi.URLParam(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// --- shipping options --------------------------------------------------------

// createOptionRequest is the body of POST /admin/v1/shipping-options.
type createOptionRequest struct {
	Name              string `json:"name"`
	ProviderID        string `json:"provider_id"`
	ShippingProfileID string `json:"shipping_profile_id"`
	PriceType         string `json:"price_type"`
	// Amount is an INTEGER in minor units and is meaningful only on "flat"
	// options.
	Amount       int64          `json:"amount"`
	CurrencyCode string         `json:"currency_code"`
	RegionID     string         `json:"region_id"`
	IsReturn     bool           `json:"is_return"`
	AdminOnly    bool           `json:"admin_only"`
	Data         map[string]any `json:"data"`
	Metadata     map[string]any `json:"metadata"`
}

// createOption creates a new shipping option.
func (h *Handler) createOption(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createOptionRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	option, err := h.svc.CreateShippingOption(ctx, service.CreateOptionInput{
		Name:              body.Name,
		ProviderID:        body.ProviderID,
		ShippingProfileID: body.ShippingProfileID,
		PriceType:         body.PriceType,
		Amount:            body.Amount,
		CurrencyCode:      body.CurrencyCode,
		RegionID:          body.RegionID,
		IsReturn:          body.IsReturn,
		AdminOnly:         body.AdminOnly,
		Data:              body.Data,
		Metadata:          body.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toOptionDTO(option)})
}

// listOptions returns the shipping options page by page.
func (h *Handler) listOptions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	in := service.ListOptionsAdminInput{Page: page}
	if raw := r.URL.Query().Get("region_id"); raw != "" {
		in.RegionID = &raw
	}
	if raw := r.URL.Query().Get("shipping_profile_id"); raw != "" {
		in.ProfileID = &raw
	}
	if raw := r.URL.Query().Get("provider_id"); raw != "" {
		in.ProviderID = &raw
	}
	if raw := r.URL.Query().Get("price_type"); raw != "" {
		in.PriceType = &raw
	}

	options, count, err := h.svc.ListShippingOptions(ctx, in)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]optionDTO, 0, len(options))
	for i := range options {
		data = append(data, toOptionDTO(options[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:   data,
		Count:  count,
		Offset: page.Offset,
		Limit:  page.Limit,
	})
}

// getOption returns the option together with its rules.
func (h *Handler) getOption(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	option, err := h.svc.GetShippingOption(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toOptionDTO(option)})
}

// updateOptionRequest is the body of PATCH /admin/v1/shipping-options/{id}.
//
// provider_id and shipping_profile_id are ABSENT HERE; the rationale is in the
// [service.UpdateOptionInput] documentation.
type updateOptionRequest struct {
	Name      *string        `json:"name"`
	PriceType *string        `json:"price_type"`
	Amount    *int64         `json:"amount"`
	RegionID  *string        `json:"region_id"`
	IsReturn  *bool          `json:"is_return"`
	AdminOnly *bool          `json:"admin_only"`
	Data      map[string]any `json:"data"`
	Metadata  map[string]any `json:"metadata"`
}

// updateOption updates the given fields of the option.
func (h *Handler) updateOption(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body updateOptionRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	option, err := h.svc.UpdateShippingOption(ctx, chi.URLParam(r, "id"), service.UpdateOptionInput{
		Name:      body.Name,
		PriceType: body.PriceType,
		Amount:    body.Amount,
		RegionID:  body.RegionID,
		IsReturn:  body.IsReturn,
		AdminOnly: body.AdminOnly,
		Data:      body.Data,
		Metadata:  body.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toOptionDTO(option)})
}

// deleteOption soft deletes the option.
func (h *Handler) deleteOption(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.DeleteShippingOption(ctx, chi.URLParam(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// --- shipping option rules ---------------------------------------------------

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

// --- eligibility listing -----------------------------------------------------

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

// --- fulfillments ------------------------------------------------------------

// createFulfillmentRequest is the body of POST /admin/v1/fulfillments.
type createFulfillmentRequest struct {
	Reference        string `json:"reference"`
	ShippingOptionID string `json:"shipping_option_id"`
	// IdempotencyKey is required: a second request with the same key does NOT
	// open a new fulfillment, it returns the existing one.
	IdempotencyKey string                 `json:"idempotency_key"`
	Items          []fulfillmentItemInput `json:"items"`
	Data           map[string]any         `json:"data"`
	Metadata       map[string]any         `json:"metadata"`
}

// fulfillmentItemInput is the body of a fulfillment item.
type fulfillmentItemInput struct {
	LineItemID string `json:"line_item_id"`
	// Quantity is a pointer: the distinction between "not sent" and "sent as
	// zero" is preserved and both are rejected, but with a different message.
	Quantity *int64 `json:"quantity"`
}

// createFulfillment opens a fulfillment at the provider.
func (h *Handler) createFulfillment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createFulfillmentRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	items := make([]service.FulfillmentItemInput, 0, len(body.Items))
	for i, item := range body.Items {
		if item.Quantity == nil {
			corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest,
				"the quantity field of item %d is required", i+1))
			return
		}
		items = append(items, service.FulfillmentItemInput{
			LineItemID: item.LineItemID,
			Quantity:   *item.Quantity,
		})
	}

	ful, err := h.svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
		Reference:        body.Reference,
		ShippingOptionID: body.ShippingOptionID,
		IdempotencyKey:   body.IdempotencyKey,
		Items:            items,
		Data:             body.Data,
		Metadata:         body.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toFulfillmentDTO(ful)})
}

// listFulfillments returns the fulfillments page by page.
func (h *Handler) listFulfillments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	in := service.ListFulfillmentsInput{Page: page}
	if raw := r.URL.Query().Get("reference"); raw != "" {
		in.Reference = &raw
	}
	if raw := r.URL.Query().Get("status"); raw != "" {
		in.Status = &raw
	}

	list, count, err := h.svc.ListFulfillments(ctx, in)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]fulfillmentDTO, 0, len(list))
	for i := range list {
		data = append(data, toFulfillmentDTO(list[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:   data,
		Count:  count,
		Offset: page.Offset,
		Limit:  page.Limit,
	})
}

// getFulfillment returns the fulfillment together with its items.
func (h *Handler) getFulfillment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	ful, err := h.svc.GetFulfillment(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toFulfillmentDTO(ful)})
}

// cancelFulfillment cancels the fulfillment and returns its CURRENT state.
//
// Cancellation is IDEMPOTENT: a second call returns 200 as well. The response
// having a body is deliberate — the caller has to be able to see from the
// status field that the cancellation was really written.
func (h *Handler) cancelFulfillment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")

	if err := h.svc.CancelFulfillment(ctx, id); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	h.writeFulfillment(w, r, id)
}

// shipRequest is the body of POST /admin/v1/fulfillments/{id}/ship.
type shipRequest struct {
	TrackingNumber string `json:"tracking_number"`
	TrackingURL    string `json:"tracking_url"`
}

// shipFulfillment marks the fulfillment as handed to the carrier.
//
// The body is OPTIONAL: shipping can be reported without tracking information
// as well (some carriers provide the number later).
func (h *Handler) shipFulfillment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body shipRequest
	if err := decodeOptionalBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	ful, err := h.svc.MarkShipped(ctx, chi.URLParam(r, "id"), body.TrackingNumber, body.TrackingURL)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toFulfillmentDTO(ful)})
}

// deliverFulfillment marks the fulfillment as delivered.
func (h *Handler) deliverFulfillment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	ful, err := h.svc.MarkDelivered(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toFulfillmentDTO(ful)})
}

// writeFulfillment reads the fulfillment and writes it with the single
// envelope.
//
// It exists to return the current record after an operation that has no body
// (cancellation); if the read fails, that error is written.
func (h *Handler) writeFulfillment(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()

	ful, err := h.svc.GetFulfillment(ctx, id)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toFulfillmentDTO(ful)})
}

// --- location shipping policy ------------------------------------------------

// setLocationRequest is the body of
// PUT /admin/v1/shipping-locations/{location_id}.
//
// The route is PUT, not PATCH, and that is deliberate: the body is not a
// CORRECTION but the WHOLE policy of the location. A field left out does not
// mean "do not change it"; if the region list is not given, the location's
// bindings are DELETED and the location comes to serve all regions. PATCH would
// promise "change what I sent, leave the rest alone" and that promise could not
// be kept for this body — an empty slice and a missing field cannot be told
// apart as they pass through JSON.
type setLocationRequest struct {
	// Priority is the preference order; a smaller value comes first, negative
	// values are allowed.
	Priority int64 `json:"priority"`
	// RegionIDs are the shipping regions the location serves. If EMPTY, the
	// location serves ALL regions.
	RegionIDs []string `json:"region_ids"`
}

// setLocation writes or overwrites the shipping policy of a location.
func (h *Handler) setLocation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body setLocationRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	loc, err := h.svc.SetShippingLocation(ctx, service.SetShippingLocationInput{
		LocationID: chi.URLParam(r, "location_id"),
		Priority:   body.Priority,
		RegionIDs:  body.RegionIDs,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toLocationDTO(loc)})
}

// getLocation returns the policy of the location.
func (h *Handler) getLocation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	loc, err := h.svc.GetShippingLocation(ctx, chi.URLParam(r, "location_id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toLocationDTO(loc)})
}

// listLocations returns the written policies in priority order, page by page.
func (h *Handler) listLocations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	locations, count, err := h.svc.ListShippingLocations(ctx, page)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]locationDTO, 0, len(locations))
	for i := range locations {
		data = append(data, toLocationDTO(locations[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:   data,
		Count:  count,
		Offset: page.Offset,
		Limit:  page.Limit,
	})
}

// deleteLocation deletes the policy and returns the location to the DEFAULT.
//
// Deleting does not close the location: a location without a record is
// considered to be at priority zero and to serve all regions. Removing a
// location from candidacy is not within the shipping module's authority — the
// candidate list is produced by an inventory fact.
func (h *Handler) deleteLocation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.DeleteShippingLocation(ctx, chi.URLParam(r, "location_id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}
