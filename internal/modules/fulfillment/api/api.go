// Package api is the HTTP surface of the fulfillment module.
//
// There are two surfaces and their authority differs:
//
//   - /admin/v1 — MANAGES the shipping catalog, the LOCATION SELECTION POLICY
//     and the fulfillments: profile, option and rule CRUD, writing/reading/
//     deleting a location policy, opening a fulfillment, canceling it, handing
//     it to the carrier and reporting delivery.
//   - /store/v1 — the ONLY surface the customer sees: the eligible shipping
//     options and their rates for a cart context. Opening a fulfillment,
//     canceling one or changing its status is NOT TRIGGERED from the store
//     side; the order workflows and the admin surface do that. Letting the
//     customer open a fulfillment from their own browser would mean printing a
//     shipping label for a cart whose order never came into being.
//
// # What NEVER LEAKS into the store surface
//
// The storefront response carries only the fields the customer needs to see:
// identity, name, rate, currency and price type. The three things left out are
// deliberate:
//
//   - admin_only options are NEVER listed; the filter sits in SQL and the row
//     is never read on the store path (see service.ListShippingOptionsFor).
//   - provider_id and the provider's raw data ("data") are NOT WRITTEN: which
//     carrier the store works with, and that carrier's internal response, are
//     the store's operational information.
//   - shipping_profile_id and metadata are NOT WRITTEN: both are internal
//     structure of the catalog and the customer does not need them to make a
//     choice.
//
// Handlers do NOT CHOOSE the status code: the service returns a core/errors
// typed error and corehttp.WriteError writes the code matching its kind (plan
// Section 8).
//
// # Authority
//
// Every admin endpoint demands a scope and the vocabulary consists of two
// entries:
//
//   - [ScopeRead] — opens the READ (GET, HEAD) endpoints under /admin/v1: the
//     provider list, profiles, options, rules, the eligibility listing, the
//     location shipping policies and the fulfillments can be read.
//   - [ScopeWrite] — opens the WRITE (POST, PUT, PATCH, DELETE) endpoints under
//     /admin/v1: besides catalog CRUD, writing/deleting a location policy,
//     opening a fulfillment, canceling it, handing it to the carrier and
//     reporting delivery also belong here.
//
// corehttp.ScopeAdmin is the SUPERIOR SCOPE and satisfies both; it does not
// need to be listed separately, corehttp.Principal.HasScope already does that.
//
// The /store/v1 eligible-option endpoint DEMANDS no scope: the identity of the
// store surface is the publishable key and that key by definition CARRIES no
// scope.
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

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// Route paths. Module routes are registered with their FULL PATH; a prefix such
// as "/admin/v1" is NOT MOUNTED, because the first module that mounts it owns
// that whole subtree and would collide with the other modules using the same
// prefix.
const (
	pathAdminProviders = "/admin/v1/fulfillment-providers"

	pathAdminProfiles = "/admin/v1/shipping-profiles"
	pathAdminProfile  = "/admin/v1/shipping-profiles/{id}"

	pathAdminOptions = "/admin/v1/shipping-options"
	// pathAdminEligible is the admin surface's eligibility listing and it
	// includes admin_only options TOO. The static segment matches before the
	// "{id}" path; chi prefers a constant segment over a parameter.
	pathAdminEligible    = "/admin/v1/shipping-options/eligible"
	pathAdminOption      = "/admin/v1/shipping-options/{id}"
	pathAdminOptionRules = "/admin/v1/shipping-options/{id}/rules"
	pathAdminOptionRule  = "/admin/v1/shipping-options/{id}/rules/{rule_id}"

	// pathAdminLocations is the LOCATION SELECTION POLICY: which location
	// serves which shipping region and in what order it is preferred. The
	// locations themselves live on the inventory module's endpoints
	// (/admin/v1/stock-locations); the record here is only that location's
	// shipping attribute.
	pathAdminLocations = "/admin/v1/shipping-locations"
	pathAdminLocation  = "/admin/v1/shipping-locations/{location_id}"

	pathAdminFulfillments = "/admin/v1/fulfillments"
	pathAdminFulfillment  = "/admin/v1/fulfillments/{id}"
	pathAdminCancel       = "/admin/v1/fulfillments/{id}/cancel"
	pathAdminShip         = "/admin/v1/fulfillments/{id}/ship"
	pathAdminDeliver      = "/admin/v1/fulfillments/{id}/deliver"

	pathStoreOptions = "/store/v1/shipping-options"
)

// maxBodyBytes is the upper limit for the request body. Without a limit a
// single request could exhaust the server's memory.
const maxBodyBytes int64 = 1 << 20 // 1 MiB

// codeInvalidRequest is the error code returned when the body or a parameter
// cannot be parsed.
const codeInvalidRequest = "fulfillment_invalid_request"

// Fulfillments is the surface the handlers need from the service.
//
// Keeping it narrow simplifies the tests: HTTP behavior can be verified with a
// few lines of fake, without a real database.
type Fulfillments interface {
	// ProviderIDs returns the registered provider identifiers.
	ProviderIDs(ctx context.Context) []string

	// CreateShippingProfile creates a new shipping profile.
	CreateShippingProfile(ctx context.Context, in service.CreateProfileInput) (models.ShippingProfile, error)
	// GetShippingProfile returns the profile by its identifier.
	GetShippingProfile(ctx context.Context, id string) (models.ShippingProfile, error)
	// ListShippingProfiles pages through the profiles.
	ListShippingProfiles(ctx context.Context, in service.ListProfilesInput) ([]models.ShippingProfile, int64, error)
	// UpdateShippingProfile updates the given fields of the profile.
	UpdateShippingProfile(ctx context.Context, id string, in service.UpdateProfileInput) (models.ShippingProfile, error)
	// DeleteShippingProfile soft deletes the profile.
	DeleteShippingProfile(ctx context.Context, id string) error

	// CreateShippingOption creates a new shipping option.
	CreateShippingOption(ctx context.Context, in service.CreateOptionInput) (models.ShippingOption, error)
	// GetShippingOption returns the option together with its rules.
	GetShippingOption(ctx context.Context, id string) (models.ShippingOption, error)
	// ListShippingOptions pages through the options.
	ListShippingOptions(ctx context.Context, in service.ListOptionsAdminInput) ([]models.ShippingOption, int64, error)
	// UpdateShippingOption updates the given fields of the option.
	UpdateShippingOption(ctx context.Context, id string, in service.UpdateOptionInput) (models.ShippingOption, error)
	// DeleteShippingOption soft deletes the option.
	DeleteShippingOption(ctx context.Context, id string) error

	// CreateShippingOptionRule adds a rule to an option.
	CreateShippingOptionRule(ctx context.Context, optionID string, in service.CreateRuleInput) (models.ShippingOptionRule, error)
	// ListShippingOptionRules returns the rules of an option.
	ListShippingOptionRules(ctx context.Context, optionID string) ([]models.ShippingOptionRule, error)
	// DeleteShippingOptionRule soft deletes the rule.
	DeleteShippingOptionRule(ctx context.Context, ruleID string) error

	// SetShippingLocation writes or overwrites the shipping policy of a
	// location.
	SetShippingLocation(ctx context.Context, in service.SetShippingLocationInput) (models.ShippingLocation, error)
	// GetShippingLocation returns the location's policy with its regions.
	GetShippingLocation(ctx context.Context, locationID string) (models.ShippingLocation, error)
	// ListShippingLocations pages through the policies in priority order.
	ListShippingLocations(ctx context.Context, page service.Page) ([]models.ShippingLocation, int64, error)
	// DeleteShippingLocation deletes the policy and returns the location to the
	// default.
	DeleteShippingLocation(ctx context.Context, locationID string) error

	// ListShippingOptionsFor returns the eligible options for a cart context.
	ListShippingOptionsFor(ctx context.Context, in service.ListOptionsInput) ([]service.QuotedOption, error)

	// CreateFulfillment opens a fulfillment at the provider.
	CreateFulfillment(ctx context.Context, in service.CreateFulfillmentInput) (models.Fulfillment, error)
	// GetFulfillment returns the fulfillment with its items.
	GetFulfillment(ctx context.Context, id string) (models.Fulfillment, error)
	// ListFulfillments pages through the fulfillments.
	ListFulfillments(ctx context.Context, in service.ListFulfillmentsInput) ([]models.Fulfillment, int64, error)
	// CancelFulfillment cancels the fulfillment (saga compensation).
	CancelFulfillment(ctx context.Context, id string) error
	// MarkShipped marks the fulfillment as handed to the carrier.
	MarkShipped(ctx context.Context, id, trackingNumber, trackingURL string) (models.Fulfillment, error)
	// MarkDelivered marks the fulfillment as delivered.
	MarkDelivered(ctx context.Context, id string) (models.Fulfillment, error)
}

// Handler is the HTTP handler set of the fulfillment module.
type Handler struct {
	svc Fulfillments
}

// New builds the handler set running on the given service.
func New(svc Fulfillments) *Handler { return &Handler{svc: svc} }

// Scope vocabulary: the scopes the fulfillment admin endpoints demand.
//
// The vocabulary DELIBERATELY consists of nothing more than a read/write split.
// Separating "writes the catalog" from "runs the fulfillment" looks plausible
// but does not enable any decision that could be made today: the identity that
// runs a fulfillment must also be able to decide which option the fulfillment
// is opened on. The split gets added when it is genuinely needed; added now it
// would only give a false sense of precision.
//
// # The location policy endpoint has a WIDER REACH and still got no third scope
//
// A single write to [pathAdminLocation] can stop the order path: binding a
// non-existent region identifier to a location is writing a rule that
// eliminates that location on every cart, and in a single-location setup the
// result is that every checkout gets a 409 even though the catalog is full.
// None of the other write endpoints in this module can do that.
//
// Even so, a third scope (e.g. "fulfillment:policy") was not added, and the
// reason is not that this endpoint is harmless but the VOCABULARY itself: the
// project's scope vocabulary derives from a single rule (<module>:read /
// <module>:write, with "admin" as the superior scope) and hundreds of admin
// endpoints are checked with that rule. A name special to a single endpoint
// would make the rule unlearnable and unauditable; the gain would be limited,
// because an identity carrying fulfillment:write is already an admin identity.
//
// The cost of the decision was REDUCED, not removed: an order that falls
// because of elimination carries the shipping module's OWN error code in the
// response body (service.CodeNoServiceableLocation), so the operator finds the
// place to look FROM THE CODE. This is the PRECONDITION of the decision: for a
// fault whose cause is invisible, saying "it can be undone from an admin
// endpoint" would be an empty promise.
//
// The LIMIT of that visibility must be written down too: the MESSAGE in the
// body is the same for all three elimination faults (the transport layer writes
// the outermost message). The dump that says which regions the candidates are
// actually bound to lives in the server log and in the workflow record, not in
// the body.
const (
	// ScopeRead is the scope the READ endpoints of the fulfillment admin
	// surface demand.
	ScopeRead = "fulfillment:read"
	// ScopeWrite is the scope the WRITE endpoints of the fulfillment admin
	// surface demand.
	ScopeWrite = "fulfillment:write"
)

// Routes binds the module's admin and store routes to the router.
//
// # PROTECTION
//
// There are two layers and both are necessary:
//
//  1. IDENTITY — with corehttp.RequireAdmin, on the side that builds the
//     router.
//  2. SCOPE — HERE, endpoint by endpoint with corehttp.RequireScope: read
//     endpoints demand [ScopeRead], write endpoints demand [ScopeWrite].
//
// Without the second layer authentication would stand in for authorization, and
// an admin user whose scopes had been EMPTIED could open a fulfillment and
// print a shipping label, cancel an opened fulfillment, or close an order that
// was never shipped as "delivered". All three reach the outside world — the
// carrier and the customer — and cost money to undo.
func (h *Handler) Routes(r chi.Router) {
	read := r.With(corehttp.RequireScope(ScopeRead))
	write := r.With(corehttp.RequireScope(ScopeWrite))

	read.Get(pathAdminProviders, h.listProviders)

	write.Post(pathAdminProfiles, h.createProfile)
	read.Get(pathAdminProfiles, h.listProfiles)
	read.Get(pathAdminProfile, h.getProfile)
	write.Patch(pathAdminProfile, h.updateProfile)
	write.Delete(pathAdminProfile, h.deleteProfile)

	write.Post(pathAdminOptions, h.createOption)
	read.Get(pathAdminOptions, h.listOptions)
	// The eligibility listing is bound BEFORE the option read; that is for
	// readability, chi prefers the constant segment regardless of order.
	read.Get(pathAdminEligible, h.listAdminEligibleOptions)
	read.Get(pathAdminOption, h.getOption)
	write.Patch(pathAdminOption, h.updateOption)
	write.Delete(pathAdminOption, h.deleteOption)

	write.Post(pathAdminOptionRules, h.createRule)
	read.Get(pathAdminOptionRules, h.listRules)
	write.Delete(pathAdminOptionRule, h.deleteRule)

	read.Get(pathAdminLocations, h.listLocations)
	read.Get(pathAdminLocation, h.getLocation)
	write.Put(pathAdminLocation, h.setLocation)
	write.Delete(pathAdminLocation, h.deleteLocation)

	write.Post(pathAdminFulfillments, h.createFulfillment)
	read.Get(pathAdminFulfillments, h.listFulfillments)
	read.Get(pathAdminFulfillment, h.getFulfillment)
	write.Post(pathAdminCancel, h.cancelFulfillment)
	write.Post(pathAdminShip, h.shipFulfillment)
	write.Post(pathAdminDeliver, h.deliverFulfillment)

	// The store endpoint DOES NOT CHANGE: a publishable key carries no scope.
	r.Get(pathStoreOptions, h.listStoreEligibleOptions)
}

// --- envelopes and DTOs ------------------------------------------------------

// singleEnvelope is the envelope of single responses (plan Section 8).
type singleEnvelope struct {
	// Data is the body of the response.
	Data any `json:"data"`
}

// listEnvelope is the envelope of list responses (plan Section 8).
type listEnvelope struct {
	// Data is the set of records on the page.
	Data any `json:"data"`
	// Count is the number of ALL records matching the filter; not the number of
	// rows on the page.
	Count int64 `json:"count"`
	// Offset is the number of skipped records.
	Offset int64 `json:"offset"`
	// Limit is the requested page size.
	Limit int64 `json:"limit"`
}

// locationDTO is the representation of a location's SHIPPING POLICY.
//
// The location's name and address are ABSENT and will stay absent: they are the
// inventory module's data and are read from under /admin/v1/stock-locations.
// Joining the two lists is the admin surface's job; copying them here would
// make the same information have two sources of truth in two modules.
type locationDTO struct {
	LocationID string `json:"location_id"`
	Priority   int64  `json:"priority"`
	// RegionIDs being EMPTY means the location serves ALL regions — not none of
	// them. The field CARRIES no omitempty and that is deliberate: dropping the
	// "regions" key from the response would make the client read it as "no
	// information", whereas an empty array states the rule itself.
	RegionIDs []string  `json:"region_ids"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// toLocationDTO converts the policy into its response representation.
func toLocationDTO(loc models.ShippingLocation) locationDTO {
	regions := loc.RegionIDs
	if regions == nil {
		regions = []string{}
	}
	return locationDTO{
		LocationID: loc.LocationID,
		Priority:   loc.Priority,
		RegionIDs:  regions,
		CreatedAt:  loc.CreatedAt,
		UpdatedAt:  loc.UpdatedAt,
	}
}

// profileDTO is the external representation of a shipping profile.
type profileDTO struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Type      string         `json:"type"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// optionDTO is the ADMIN representation of a shipping option.
//
// The provider configuration ("data") is visible here: the administrator needs
// it to be able to edit the option. The store representation is separate
// ([storeOptionDTO]) and does not carry this field.
type optionDTO struct {
	ID                string         `json:"id"`
	Name              string         `json:"name"`
	ProviderID        string         `json:"provider_id"`
	ShippingProfileID string         `json:"shipping_profile_id"`
	PriceType         string         `json:"price_type"`
	Amount            int64          `json:"amount"`
	CurrencyCode      string         `json:"currency_code"`
	RegionID          string         `json:"region_id"`
	IsReturn          bool           `json:"is_return"`
	AdminOnly         bool           `json:"admin_only"`
	Data              map[string]any `json:"data,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
	Rules             []ruleDTO      `json:"rules,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

// ruleDTO is the external representation of a shipping option rule.
type ruleDTO struct {
	ID               string    `json:"id"`
	ShippingOptionID string    `json:"shipping_option_id"`
	Attribute        string    `json:"attribute"`
	Operator         string    `json:"operator"`
	Values           []string  `json:"values"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// quotedOptionDTO is the ADMIN surface's representation of a quoted option.
type quotedOptionDTO struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Amount            int64  `json:"amount"`
	CurrencyCode      string `json:"currency_code"`
	PriceType         string `json:"price_type"`
	ProviderID        string `json:"provider_id"`
	ShippingProfileID string `json:"shipping_profile_id"`
	IsReturn          bool   `json:"is_return"`
	AdminOnly         bool   `json:"admin_only"`
}

// storeOptionDTO is the STORE surface's representation of a quoted option.
//
// The field list is deliberately SHORT; what is left out and why is written in
// the package documentation. The struct being separate from [quotedOptionDTO]
// structurally prevents a field added to the admin representation from
// accidentally leaking into the storefront.
type storeOptionDTO struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Amount       int64  `json:"amount"`
	CurrencyCode string `json:"currency_code"`
	PriceType    string `json:"price_type"`
}

// fulfillmentDTO is the external representation of a fulfillment.
type fulfillmentDTO struct {
	ID               string               `json:"id"`
	Reference        string               `json:"reference"`
	ShippingOptionID string               `json:"shipping_option_id"`
	ProviderID       string               `json:"provider_id"`
	ExternalID       string               `json:"external_id"`
	Status           string               `json:"status"`
	TrackingNumber   string               `json:"tracking_number,omitempty"`
	TrackingURL      string               `json:"tracking_url,omitempty"`
	ShippedAt        *time.Time           `json:"shipped_at,omitempty"`
	DeliveredAt      *time.Time           `json:"delivered_at,omitempty"`
	CanceledAt       *time.Time           `json:"canceled_at,omitempty"`
	Data             json.RawMessage      `json:"data,omitempty"`
	Metadata         map[string]any       `json:"metadata,omitempty"`
	Items            []fulfillmentItemDTO `json:"items"`
	CreatedAt        time.Time            `json:"created_at"`
	UpdatedAt        time.Time            `json:"updated_at"`
}

// fulfillmentItemDTO is the external representation of a fulfillment item.
type fulfillmentItemDTO struct {
	ID         string `json:"id"`
	LineItemID string `json:"line_item_id"`
	Quantity   int64  `json:"quantity"`
}

// toProfileDTO converts the model into its external representation.
func toProfileDTO(profile models.ShippingProfile) profileDTO {
	return profileDTO{
		ID:        profile.ID,
		Name:      profile.Name,
		Type:      profile.Type.String(),
		Metadata:  profile.Metadata,
		CreatedAt: profile.CreatedAt,
		UpdatedAt: profile.UpdatedAt,
	}
}

// toOptionDTO converts the model into the admin representation.
func toOptionDTO(option models.ShippingOption) optionDTO {
	rules := make([]ruleDTO, 0, len(option.Rules))
	for i := range option.Rules {
		rules = append(rules, toRuleDTO(option.Rules[i]))
	}
	if len(rules) == 0 {
		// Left nil so that omitempty works: writing "rules": [] on an option
		// without rules would be confused with the list response in which the
		// rules were never read at all.
		rules = nil
	}

	return optionDTO{
		ID:                option.ID,
		Name:              option.Name,
		ProviderID:        option.ProviderID,
		ShippingProfileID: option.ShippingProfileID,
		PriceType:         option.PriceType.String(),
		Amount:            option.Amount,
		CurrencyCode:      option.CurrencyCode,
		RegionID:          option.RegionID,
		IsReturn:          option.IsReturn,
		AdminOnly:         option.AdminOnly,
		Data:              option.Data,
		Metadata:          option.Metadata,
		Rules:             rules,
		CreatedAt:         option.CreatedAt,
		UpdatedAt:         option.UpdatedAt,
	}
}

// toRuleDTO converts the model into its external representation.
func toRuleDTO(rule models.ShippingOptionRule) ruleDTO {
	return ruleDTO{
		ID:               rule.ID,
		ShippingOptionID: rule.ShippingOptionID,
		Attribute:        rule.Attribute,
		Operator:         rule.Operator.String(),
		Values:           rule.Values,
		CreatedAt:        rule.CreatedAt,
		UpdatedAt:        rule.UpdatedAt,
	}
}

// toQuotedOptionDTO converts the quoted option into the admin representation.
func toQuotedOptionDTO(quoted service.QuotedOption) quotedOptionDTO {
	return quotedOptionDTO{
		ID:                quoted.Option.ID,
		Name:              quoted.Option.Name,
		Amount:            quoted.Amount,
		CurrencyCode:      quoted.CurrencyCode,
		PriceType:         quoted.Option.PriceType.String(),
		ProviderID:        quoted.Option.ProviderID,
		ShippingProfileID: quoted.Option.ShippingProfileID,
		IsReturn:          quoted.Option.IsReturn,
		AdminOnly:         quoted.Option.AdminOnly,
	}
}

// toStoreOptionDTO converts the quoted option into the STORE representation.
func toStoreOptionDTO(quoted service.QuotedOption) storeOptionDTO {
	return storeOptionDTO{
		ID:           quoted.Option.ID,
		Name:         quoted.Option.Name,
		Amount:       quoted.Amount,
		CurrencyCode: quoted.CurrencyCode,
		PriceType:    quoted.Option.PriceType.String(),
	}
}

// toFulfillmentDTO converts the model into its external representation.
func toFulfillmentDTO(ful models.Fulfillment) fulfillmentDTO {
	items := make([]fulfillmentItemDTO, 0, len(ful.Items))
	for i := range ful.Items {
		items = append(items, fulfillmentItemDTO{
			ID:         ful.Items[i].ID,
			LineItemID: ful.Items[i].LineItemID,
			Quantity:   ful.Items[i].Quantity,
		})
	}

	return fulfillmentDTO{
		ID:               ful.ID,
		Reference:        ful.Reference,
		ShippingOptionID: ful.ShippingOptionID,
		ProviderID:       ful.ProviderID,
		ExternalID:       ful.ExternalID,
		Status:           ful.Status.String(),
		TrackingNumber:   ful.TrackingNumber,
		TrackingURL:      ful.TrackingURL,
		ShippedAt:        ful.ShippedAt,
		DeliveredAt:      ful.DeliveredAt,
		CanceledAt:       ful.CanceledAt,
		Data:             ful.Data,
		Metadata:         ful.Metadata,
		Items:            items,
		CreatedAt:        ful.CreatedAt,
		UpdatedAt:        ful.UpdatedAt,
	}
}

// --- helpers -----------------------------------------------------------------

// decodeBody decodes the request body.
//
// The body size is limited and UNKNOWN FIELDS are rejected: a silently
// swallowed field means a setting the client believes it sent but that is never
// applied.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return coreerrors.Invalid(codeInvalidRequest, "request body cannot be empty")
		}
		return coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"request body could not be parsed")
	}
	// More than a single JSON value having been sent is a client error as well.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return coreerrors.Invalid(codeInvalidRequest,
			"request body has to be a single JSON object")
	}
	return nil
}

// decodeOptionalBody decodes the body but does NOT treat an EMPTY body as an
// error.
//
// It is for endpoints whose body is optional (e.g. a ship notification without
// tracking information). The emptiness check does NOT LOOK at Content-Length:
// on a request arriving with chunked encoding the length is -1, and a check
// that looked at the length would silently ignore a body that WAS actually
// SENT — the client would only see on the shipping screen that the tracking
// number it believed it had sent was never written. Emptiness is recognized
// from the first decode returning io.EOF.
func decodeOptionalBody(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"request body could not be parsed")
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return coreerrors.Invalid(codeInvalidRequest,
			"request body has to be a single JSON object")
	}
	return nil
}

// parsePage parses the limit/offset query parameters.
func parsePage(r *http.Request) (service.Page, error) {
	limit, err := parseInt64Param(r, "limit")
	if err != nil {
		return service.Page{}, err
	}
	offset, err := parseInt64Param(r, "offset")
	if err != nil {
		return service.Page{}, err
	}
	page := service.Page{Limit: limit, Offset: offset}
	if page.Limit == 0 {
		// The default is made visible here too, so that the limit field in the
		// response shows the limit that is actually applied.
		page.Limit = service.DefaultLimit
	}
	return page, nil
}

// parseInt64Param converts a query parameter into an integer; returns 0 if it
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

// parseBoolParam converts a query parameter into a boolean; returns false if it
// is absent.
func parseBoolParam(r *http.Request, name string) (bool, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return false, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"%s has to be a boolean: %q", name, raw)
	}
	return value, nil
}

// writeList writes a slice with the list envelope.
//
// On endpoints that are not paged (such as the rules of an option) count is the
// row count and equals limit: the envelope has the same shape everywhere, so
// the client does not have to learn two different response formats.
func writeList[T any](ctx context.Context, w http.ResponseWriter, items []T) {
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:   items,
		Count:  int64(len(items)),
		Offset: 0,
		Limit:  int64(len(items)),
	})
}
