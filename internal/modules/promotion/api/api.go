// Package api is the promotion module's HTTP surface.
//
// There are two namespaces (plan Section 8): /admin/v1 for administration,
// /store/v1 for the customer. The write surface is ONLY on the admin side; the
// store side has a single coupon validation endpoint.
//
// # What the store surface does NOT LEAK
//
// The body that goes to the customer does not contain the promotion's STATUS,
// the usage counter, the campaign budget, the metadata or the RULE CONDITIONS.
// When a code is not valid, no reason is given either: a draft, inactive,
// expired, budget-exhausted and nonexistent code all return the SAME 404
// (rationale: service.Service.LookupStoreCoupon).
//
// Handlers do NOT CHOOSE the status code: the service returns a typed error and
// corehttp.WriteError turns it into a status code (plan Section 2.7). This
// keeps error classification in a single place.
//
// # Scopes
//
// Every admin endpoint asks for a scope, and the vocabulary consists of two
// entries:
//
//   - [ScopeRead] — opens the READ (GET, HEAD) endpoints under /admin/v1:
//     campaigns, promotions, rules and redemption records can be read.
//   - [ScopeWrite] — opens the WRITE (POST, PUT, PATCH, DELETE) endpoints
//     under /admin/v1: besides CRUD, the redeem/release endpoints and the
//     discount computation (compute) endpoint belong here too.
//
// corehttp.ScopeAdmin is a SUPERSCOPE and satisfies both; it does not need to
// be listed separately, corehttp.Principal.HasScope already does that.
//
// The /store/v1 coupon validation endpoint asks for NO scope: the storefront
// surface's identity is the publishable key, and that key by definition
// CARRIES no scope.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/promotion/service"
)

// maxBodyBytes is the maximum size of a single request body.
//
// A discount computation can carry hundreds of lines in one request, so the
// bound is generous; but it is not unbounded — an unbounded body is the
// cheapest way to exhaust memory with a single request.
const maxBodyBytes int64 = 1 << 20 // 1 MiB

// codeInvalidBody is the error code returned when a request body cannot be
// parsed.
const codeInvalidBody = "promotion_invalid_body"

// API holds promotion's HTTP handlers.
type API struct {
	svc *service.Service
	// trial is the flow the trial endpoint runs on; see [API.WithTrial].
	trial PromotionTrial
}

// New builds an API that runs on the given service.
func New(svc *service.Service) *API {
	return &API{svc: svc}
}

// Scope vocabulary: the scopes promotion's admin endpoints ask for.
//
// The vocabulary DELIBERATELY consists of the read/write split. Defining a
// separate scope per resource ("campaigns:write", "redemptions:write" …) would
// grow the list but would not make possible any new decision that can be made
// today: an identity that can write a promotion has to be able to write the
// campaign as well, because the budget is kept on the campaign. The split is
// added when it is genuinely needed; added now, it would only give a false
// sense of precision.
const (
	// ScopeRead is the scope the READ endpoints of promotion's admin surface
	// ask for.
	ScopeRead = "promotion:read"
	// ScopeWrite is the scope the WRITE endpoints of promotion's admin
	// surface ask for.
	ScopeWrite = "promotion:write"
)

// Routes binds promotion's admin and store routes to the router.
//
// Routes are registered with full paths, NOT with chi's Route/Mount helpers:
// several modules share the /admin/v1 prefix, and mounting the same prefix
// twice would panic in chi. Registering full paths writes them side by side
// into the same tree.
//
// # PROTECTION
//
// There are two layers, and both are needed:
//
//  1. IDENTITY — with corehttp.RequireAdmin, on the side that builds the
//     router.
//  2. SCOPE — HERE, endpoint by endpoint, with corehttp.RequireScope: read
//     endpoints ask for [ScopeRead], write endpoints for [ScopeWrite].
//
// Without the second layer authentication would stand in for authorization,
// and an admin user whose scopes were EMPTIED could create a promotion and
// write themselves a 100% discount — a direct loss of money.
//
// POST /admin/v1/promotions/compute only COMPUTES and writes nothing; it still
// asks for [ScopeWrite]. The vocabulary is defined by the method ("POST →
// write") and has no exceptions: a "POST that is really a read" distinction
// turns the vocabulary into something argued endpoint by endpoint, and the next
// loosening arrives silently. The place that verifies the computation really
// writes nothing is the service layer.
func (a *API) Routes(r chi.Router) {
	read := r.With(corehttp.RequireScope(ScopeRead))
	write := r.With(corehttp.RequireScope(ScopeWrite))

	write.Post("/admin/v1/campaigns", a.createCampaign)
	read.Get("/admin/v1/campaigns", a.listCampaigns)
	read.Get("/admin/v1/campaigns/{id}", a.getCampaign)
	write.Put("/admin/v1/campaigns/{id}", a.updateCampaign)
	write.Delete("/admin/v1/campaigns/{id}", a.deleteCampaign)

	write.Post("/admin/v1/promotions", a.createPromotion)
	read.Get("/admin/v1/promotions", a.listPromotions)
	read.Get("/admin/v1/promotions/{id}", a.getPromotion)
	write.Put("/admin/v1/promotions/{id}", a.updatePromotion)
	write.Delete("/admin/v1/promotions/{id}", a.deletePromotion)

	write.Put("/admin/v1/promotions/{id}/application-method", a.setApplicationMethod)
	write.Delete("/admin/v1/promotions/{id}/application-method", a.deleteApplicationMethod)

	read.Get("/admin/v1/promotions/{id}/rules", a.listPromotionRules)
	write.Post("/admin/v1/promotions/{id}/rules", a.createPromotionRule)
	write.Delete("/admin/v1/promotion-rules/{id}", a.deletePromotionRule)

	read.Get("/admin/v1/promotions/{id}/redemptions", a.listRedemptions)
	write.Post("/admin/v1/promotions/{id}/redeem", a.redeemPromotion)
	write.Post("/admin/v1/promotions/{id}/release", a.releasePromotion)

	write.Post("/admin/v1/promotions/compute", a.computeDiscounts)
	// The trial's report is orders, so it asks for the order module's read
	// privilege too (ADR 0176).
	read.With(corehttp.RequireScope(orderReadScope)).Get(pathTrial, a.trialPromotion)

	// The store endpoint is UNCHANGED: the publishable key carries no scope.
	r.Get("/store/v1/promotions/{code}", a.storeGetPromotion)
}

// itemEnvelope is the envelope of single-record responses (plan Section 8).
type itemEnvelope struct {
	// Data is the body of the single record.
	Data any `json:"data"`
}

// listEnvelope is the envelope of list responses (plan Section 8).
type listEnvelope struct {
	// Data is the records on the current page.
	Data any `json:"data"`
	// Count is the TOTAL number of records matching the filter.
	Count int64 `json:"count"`
	// Offset is the applied skip count.
	Offset int32 `json:"offset"`
	// Limit is the applied page size.
	Limit int32 `json:"limit"`
}

// writeItem writes a single-record response with its envelope.
func writeItem(w http.ResponseWriter, r *http.Request, status int, data any) {
	corehttp.WriteJSON(r.Context(), w, status, itemEnvelope{Data: data})
}

// writeItems writes an unpaginated list with its envelope.
//
// On unpaginated endpoints (a promotion's rules) the envelope's numeric fields
// are filled with the record count: the shape of the envelope the client sees
// does not change from endpoint to endpoint.
//
// Limit EQUALS the number of records returned and is NOT CLAMPED to
// [service.MaxLimit]. Were it clamped, the response for a promotion with 250
// rules would say "count=250, limit=100"; the client would take the page size
// to be 100, enter a paging loop and read the same records again. There are no
// pages here — the single page is all the records.
func writeItems[T any](w http.ResponseWriter, r *http.Request, items []T) {
	if items == nil {
		items = []T{}
	}
	count := int64(len(items))
	corehttp.WriteJSON(r.Context(), w, http.StatusOK, listEnvelope{
		Data:   items,
		Count:  count,
		Offset: 0,
		Limit:  clampCount(count),
	})
}

// clampCount fits the record count into the envelope's int32 limit field.
//
// It only fits it into the int32 RANGE; it applies no page size bound (see
// writeItems). The lower bound is checked too: count is the result of a len()
// and cannot be negative, but the presence of the check proves LOCALLY that the
// conversion to int32 is safe for every input.
func clampCount(count int64) int32 {
	if count < 0 {
		return 0
	}
	if count > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(count)
}

// writePage writes a service page with the list envelope.
func writePage[S any, T any](w http.ResponseWriter, r *http.Request, page service.Page[S], convert func(S) T) {
	items := make([]T, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, convert(item))
	}
	corehttp.WriteJSON(r.Context(), w, http.StatusOK, listEnvelope{
		Data:   items,
		Count:  page.Count,
		Offset: page.Offset,
		Limit:  page.Limit,
	})
}

// decodeBody decodes the request body into the destination.
//
// Unknown fields are REJECTED: a silently ignored field means a rule the client
// believes it sent is never written. The body size is bounded too; if the
// bound is exceeded it comes back as a parse error.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	reader := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(reader)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return coreerrors.Invalid(codeInvalidBody, "request body cannot be empty")
		}
		return coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidBody,
			"request body could not be parsed")
	}

	// A single JSON document is expected; were a second document following it
	// silently ignored, the client would believe what it sent had been
	// processed.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return coreerrors.Invalid(codeInvalidBody, "request body has to be a single JSON document")
	}
	return nil
}

// pathID reads a path parameter.
func pathID(r *http.Request, name string) string {
	return chi.URLParam(r, name)
}

// pageParams reads the paging parameters from the query string.
//
// A missing parameter returns zero and the service applies the default; a value
// that CANNOT BE CONVERTED TO A NUMBER returns an error — silently falling back
// to zero would hand the client the first page instead of the page it asked
// for.
func pageParams(r *http.Request) (limit, offset int32, err error) {
	limit, err = intParam(r, "limit")
	if err != nil {
		return 0, 0, err
	}
	offset, err = intParam(r, "offset")
	if err != nil {
		return 0, 0, err
	}
	return limit, offset, nil
}

// intParam reads a single numeric query parameter; it returns zero when the
// parameter is absent.
func intParam(r *http.Request, name string) (int32, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return 0, coreerrors.Invalid(codeInvalidBody,
			"the %q parameter has to be an integer, %q was given", name, raw)
	}
	return int32(value), nil
}

// stringParam returns a single string query parameter as a pointer; nil when
// it is absent.
//
// Returning a pointer is deliberate: the distinction between "no filter was
// given" and "filter by an empty value" is preserved.
func stringParam(r *http.Request, name string) *string {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil
	}
	return &raw
}
