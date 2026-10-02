// Package api is the region module's HTTP surface.
//
// There are two namespaces (plan Section 8): /admin/v1 for administration,
// /store/v1 for the customer.
//
// # Why the write surface is on regions only
//
// On the admin side full CRUD is open to regions ONLY. Currency and country are
// REFERENCE DATA: both are seeded by a migration (see 000002_region_seed) and
// are copies of external standards (ISO 4217 / ISO 3166-1). Opening a write
// surface to them would make the standard "correctable" by hand; a single
// currency entered with the wrong number of decimal digits would show every
// amount in that currency at the wrong scale. That is why the currency and
// country endpoints are READS; the one thing that changes is which region a
// country belongs to, and that is a subresource of the region.
//
// # Scopes
//
// The endpoints under /admin/v1 ask for a scope SEPARATELY from identity:
//
//   - [ScopeRead] ("region:read") — opens the GET endpoints.
//   - [ScopeWrite] ("region:write") — opens the POST, PUT and DELETE endpoints.
//
// corehttp.ScopeAdmin ("admin") is a SUPERSCOPE and satisfies both; a fully
// privileged identity does not need to be granted them separately.
//
// The /store/v1 endpoints ask for NO scope: the storefront surface's identity
// is the publishable key, and that key by definition carries no scope.
//
// Handlers do NOT CHOOSE the status code: the service returns a typed error and
// corehttp.WriteError turns it into a status code (plan Section 2.7). This
// keeps error classification in a single place.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/region/service"
)

// Route paths. Module routes are registered with the FULL PATH; a prefix such
// as "/admin/v1" is NOT MOUNTED, because the first module to mount it would own
// that whole subtree and collide with every other module using the same
// prefix.
const (
	pathAdminRegions         = "/admin/v1/regions"
	pathAdminRegion          = "/admin/v1/regions/{id}"
	pathAdminRegionCountries = "/admin/v1/regions/{id}/countries"
	pathAdminRegionCountry   = "/admin/v1/regions/{id}/countries/{code}"
	pathAdminCountries       = "/admin/v1/countries"
	pathAdminCurrencies      = "/admin/v1/currencies"
	pathAdminCurrency        = "/admin/v1/currencies/{code}"

	pathStoreRegions = "/store/v1/regions"
	pathStoreRegion  = "/store/v1/regions/{id}"
)

// maxBodyBytes is the maximum size of a single request body.
//
// Region bodies are small (name, currency, rate), so the bound is tight. An
// unbounded body is the cheapest way to exhaust memory with a single request.
const maxBodyBytes int64 = 64 << 10 // 64 KiB

// codeInvalidBody is the error code returned when a request body or parameter
// cannot be parsed.
const codeInvalidBody = "region_invalid_body"

// API holds region's HTTP handlers.
type API struct {
	svc *service.Service
}

// New builds an API that runs on the given service.
func New(svc *service.Service) *API {
	return &API{svc: svc}
}

// Scope vocabulary: the scopes region's admin endpoints ask for.
//
// The names follow the same pattern in ALL modules ("<module>:read" /
// "<module>:write"). Every module coining its own word would mean the person
// granting scopes memorizes a separate vocabulary per module; and a mistake made
// in a vocabulary nobody memorized always falls the same way — too much is
// granted.
const (
	// ScopeRead is the scope the READ endpoints of region's admin surface ask
	// for.
	//
	// It is enough to read regions, countries and currencies; it opens no write
	// endpoint. Fully privileged identities do not need it granted separately:
	// a caller carrying corehttp.ScopeAdmin satisfies this one too (see
	// corehttp.Principal.HasScope).
	ScopeRead = "region:read"

	// ScopeWrite is the scope the WRITE endpoints of region's admin surface ask
	// for.
	//
	// It opens the endpoints that create, update and delete a region and that
	// change the region-country link. These endpoints decide the tax and
	// currency choice: moving a country to another region changes the currency
	// and tax rate of every order that comes from that country.
	ScopeWrite = "region:write"
)

// Routes binds region's admin and store routes to the router.
//
// # PROTECTION
//
// The admin endpoints have two layers, and both are needed:
//
//  1. IDENTITY — corehttp.RequireAdmin. It is mounted NOT in this module but on
//     the side that builds the router (see corehttp.APIGuards).
//  2. SCOPE — HERE, endpoint by endpoint, with corehttp.RequireScope: read
//     endpoints ask for [ScopeRead], write endpoints for [ScopeWrite].
//
// Without the second layer authentication would stand in for authorization:
// an admin user whose scopes were deliberately emptied is still a valid
// identity and could delete regions with DELETE /admin/v1/regions/{id}.
//
// The currency and country endpoints are already READS (see the package doc);
// there is no route of theirs to bind a write scope to.
//
// The store endpoints get NO scope: the storefront surface's identity is the
// publishable key, and that key by definition carries NO scope.
func (a *API) Routes(r chi.Router) {
	read := r.With(corehttp.RequireScope(ScopeRead))
	write := r.With(corehttp.RequireScope(ScopeWrite))

	write.Post(pathAdminRegions, a.createRegion)
	read.Get(pathAdminRegions, a.listRegions)
	read.Get(pathAdminRegion, a.getRegion)
	write.Put(pathAdminRegion, a.updateRegion)
	write.Delete(pathAdminRegion, a.deleteRegion)

	write.Post(pathAdminRegionCountries, a.addCountry)
	read.Get(pathAdminRegionCountries, a.listRegionCountries)
	write.Delete(pathAdminRegionCountry, a.removeCountry)

	read.Get(pathAdminCountries, a.listCountries)
	read.Get(pathAdminCurrencies, a.listCurrencies)
	read.Get(pathAdminCurrency, a.getCurrency)

	r.Get(pathStoreRegions, a.storeListRegions)
	r.Get(pathStoreRegion, a.storeGetRegion)
}

// itemEnvelope is the envelope of single-record responses (plan Section 8).
type itemEnvelope struct {
	// Data is the body of the single record.
	Data any `json:"data"`
}

// listEnvelope is the envelope of list responses (plan Section 8).
type listEnvelope struct {
	// Data are the records on the current page.
	Data any `json:"data"`
	// Count is the TOTAL number of records matching the filter.
	Count int64 `json:"count"`
	// Offset is the number of skipped records that was applied.
	Offset int32 `json:"offset"`
	// Limit is the page size that was applied.
	Limit int32 `json:"limit"`
}

// writeItem writes a single-record response with its envelope.
func writeItem(w http.ResponseWriter, r *http.Request, status int, data any) {
	corehttp.WriteJSON(r.Context(), w, status, itemEnvelope{Data: data})
}

// writePage writes the service page with the list envelope.
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
// Unknown fields are REJECTED: a silently ignored field means a tax rate the
// client believes it sent is never written. The body size is bounded too; if
// the bound is exceeded it comes back as a parse error.
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

// pathParam reads a path parameter.
func pathParam(r *http.Request, name string) string {
	return chi.URLParam(r, name)
}

// pageParams reads the paging parameters from the query string.
//
// A missing parameter returns zero and the service applies its default; a value
// that CANNOT BE CONVERTED to a number returns an error instead — silently
// falling back to zero would have made the client get the first page rather
// than the page it asked for.
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

// intParam reads a single numeric query parameter; returns zero if it is
// absent.
func intParam(r *http.Request, name string) (int32, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return 0, coreerrors.Invalid(codeInvalidBody,
			"the %q parameter has to be an integer, %q given", name, raw)
	}
	return int32(value), nil
}

// optionalParam reads a query parameter as a pointer; nil if it is absent.
//
// The distinction between the empty string and "not given at all" is kept: an
// empty region_id filter is an ID the client sent empty by mistake, and the
// service's validation has to reject it rather than let it silently turn into
// "no filter".
func optionalParam(r *http.Request, name string) *string {
	if !r.URL.Query().Has(name) {
		return nil
	}
	value := r.URL.Query().Get(name)
	return &value
}
