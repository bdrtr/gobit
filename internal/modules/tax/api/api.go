// Package api is the tax module's HTTP surface.
//
// # Why only /admin/v1
//
// Tax is not opened directly to the CUSTOMER. The only thing the storefront
// sees is the cart's computed tax line, and that comes through the cart flow
// (internal/workflows/cart, the "tax.interop" surface). Opening a "calculate
// the tax" endpoint to the customer would do two things at once: it would
// expose the store's rate configuration, and it would open the door to a
// mismatch between the cart total and a separately computed tax.
//
// Handlers do NOT CHOOSE the status code: the service returns a typed error and
// corehttp.WriteError turns it into a status code (plan Section 2.7). This
// keeps error classification in a single place.
//
// # Scopes
//
// Every admin endpoint asks for a scope, and the vocabulary has two entries:
//
//   - [ScopeRead] — opens the READ (GET, HEAD) endpoints under /admin/v1: tax
//     regions, rates, rate rules, tax classes and their products can be read.
//   - [ScopeWrite] — opens the WRITE (POST, PUT, PATCH, DELETE) endpoints
//     under /admin/v1: creating, updating and deleting regions/rates/rules,
//     tax classes and class memberships.
//
// corehttp.ScopeAdmin is a SUPERSCOPE and satisfies both; it does not have to
// be listed separately, corehttp.Principal.HasScope already does that.
//
// The scope check comes AFTER IDENTITY: with no identity the answer is 401,
// with an identity whose scopes fall short it is 403. corehttp.RequireAdmin,
// which establishes the identity, is not mounted in this module but on the side
// that builds the router (corehttp.APIGuards).
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
	"github.com/bdrtr/gobit/internal/modules/tax/service"
)

// Route paths. Module routes are registered with the FULL PATH; a prefix such
// as "/admin/v1" is NOT MOUNTED, because the first module to mount it would own
// that whole subtree and collide with the other modules using the same prefix.
const (
	pathAdminRegions     = "/admin/v1/tax-regions"
	pathAdminRegion      = "/admin/v1/tax-regions/{id}"
	pathAdminRegionRates = "/admin/v1/tax-regions/{id}/tax-rates"

	pathAdminRates     = "/admin/v1/tax-rates"
	pathAdminRate      = "/admin/v1/tax-rates/{id}"
	pathAdminRateRules = "/admin/v1/tax-rates/{id}/rules"
	pathAdminRateRule  = "/admin/v1/tax-rates/{id}/rules/{ruleID}"
)

// maxBodyBytes is the maximum size of a single request body.
//
// Tax bodies are small (a country code, a name, a rate, metadata), so the bound
// is tight. An unbounded body is the cheapest way to exhaust memory with a
// single request.
const maxBodyBytes int64 = 64 << 10 // 64 KiB

// codeInvalidBody is the error code returned when a request body or parameter
// cannot be parsed.
const codeInvalidBody = "tax_invalid_body"

// API holds tax's HTTP handlers.
type API struct {
	svc *service.Service
}

// New builds an API that runs on the given service.
func New(svc *service.Service) *API {
	return &API{svc: svc}
}

// The scope vocabulary: the scopes tax's admin endpoints ask for.
//
// The vocabulary is DELIBERATELY just a read/write split. Defining a separate
// scope per resource ("tax_rates:write", "tax_regions:write" …) would grow the
// list but would not make possible any new decision that can be made today: an
// identity that can write a rate has to be able to write the region as well,
// because a rate is meaningless without a region. The split is added when it is
// really needed; added in advance, it would only give a false sense of
// precision.
const (
	// ScopeRead is the scope the READ endpoints of tax's admin surface ask for.
	ScopeRead = "tax:read"
	// ScopeWrite is the scope the WRITE endpoints of tax's admin surface ask
	// for.
	ScopeWrite = "tax:write"
)

// Routes binds tax's admin routes to the router.
//
// # PROTECTION
//
// There are two layers, and both are needed:
//
//  1. IDENTITY — with corehttp.RequireAdmin, on the side that builds the
//     router.
//  2. SCOPE — HERE, endpoint by endpoint, with corehttp.RequireScope: the read
//     endpoints ask for [ScopeRead], the write endpoints for [ScopeWrite].
//
// Without the second layer authentication would stand in for authorization,
// and an admin user whose scopes were EMPTIED (the user auth says "can reach no
// protected endpoint") could delete the whole tax catalog. Tax configuration is
// data that can be made wrong silently: a deleted rate does not show the error
// at once, it only closes the following orders with too little tax.
func (a *API) Routes(r chi.Router) {
	read := r.With(corehttp.RequireScope(ScopeRead))
	write := r.With(corehttp.RequireScope(ScopeWrite))

	write.Post(pathAdminRegions, a.createRegion)
	read.Get(pathAdminRegions, a.listRegions)
	read.Get(pathAdminRegion, a.getRegion)
	write.Delete(pathAdminRegion, a.deleteRegion)
	read.Get(pathAdminRegionRates, a.listRegionRates)

	write.Post(pathAdminRates, a.createRate)
	read.Get(pathAdminRates, a.listRates)
	read.Get(pathAdminRate, a.getRate)
	write.Put(pathAdminRate, a.updateRate)
	write.Delete(pathAdminRate, a.deleteRate)

	write.Post(pathAdminRateRules, a.createRule)
	read.Get(pathAdminRateRules, a.listRules)
	write.Delete(pathAdminRateRule, a.deleteRule)

	// Tax class: the set of products a merchant taxes THE SAME WAY. The
	// classification is this module's own data, so its endpoints are here too.
	write.Post(pathAdminClasses, a.createClass)
	read.Get(pathAdminClasses, a.listClasses)
	read.Get(pathAdminClass, a.getClass)
	write.Delete(pathAdminClass, a.deleteClass)
	read.Get(pathAdminClassProducts, a.listClassProducts)
	write.Post(pathAdminClassProducts, a.addClassProduct)
	write.Delete(pathAdminClassProduct, a.removeClassProduct)
}

// itemEnvelope is the envelope of single-record responses (plan Section 8).
type itemEnvelope struct {
	// Data is the body of the single record.
	Data any `json:"data"`
}

// listEnvelope is the envelope of list responses (plan Section 8).
type listEnvelope struct {
	// Data holds the records on the current page.
	Data any `json:"data"`
	// Count is the TOTAL number of records matching the filter.
	Count int64 `json:"count"`
	// Offset is the number of records skipped.
	Offset int32 `json:"offset"`
	// Limit is the page size applied.
	Limit int32 `json:"limit"`
}

// writeItem writes a single-record response in its envelope.
func writeItem(w http.ResponseWriter, r *http.Request, status int, data any) {
	corehttp.WriteJSON(r.Context(), w, status, itemEnvelope{Data: data})
}

// writePage writes a service page in the list envelope.
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

// writeAll writes an unpaged list in its envelope.
//
// Count is the number of records returned and equals Limit: because the list is
// not paged there is no distinction between "total" and "on the page". The
// envelope's shape stays uniform all the same — the client does not have to
// write two separate decoders for paged and unpaged lists.
func writeAll[S any, T any](w http.ResponseWriter, r *http.Request, items []S, convert func(S) T) {
	out := make([]T, 0, len(items))
	for _, item := range items {
		out = append(out, convert(item))
	}
	// The length is fitted into int32: although these endpoints are not paged,
	// the returned list is always below the service's bounds, and the clamp only
	// makes it impossible for the conversion to wrap SILENTLY.
	count := len(out)
	limit := int32(math.MaxInt32)
	if count < math.MaxInt32 {
		limit = int32(count)
	}
	corehttp.WriteJSON(r.Context(), w, http.StatusOK, listEnvelope{
		Data:   out,
		Count:  int64(count),
		Offset: 0,
		Limit:  limit,
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
