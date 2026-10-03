// Package api is the pricing module's HTTP surface.
//
// There are two namespaces (plan Section 8): /admin/v1 for administration,
// /store/v1 for the customer. The price write surface is ONLY on the admin
// side; the store side has a single read endpoint, because the price that
// reaches the customer normally comes from product's store listing, through
// the Query layer (ADR 0004).
//
// Handlers do NOT CHOOSE the status code: the service returns a typed error and
// corehttp.WriteError turns it into a status code (plan Section 2.7). This
// keeps error classification in a single place.
//
// # Scopes
//
// The /admin/v1 endpoints ask for a scope, and the vocabulary splits in two:
// GET endpoints ask for [ScopeRead], POST/PUT/PATCH/DELETE endpoints for
// [ScopeWrite] (see [API.Routes]). corehttp.ScopeAdmin is a SUPERSCOPE and
// satisfies both on its own.
//
// The vocabulary has NO EXCEPTIONS, and that is deliberate: a scope that can be
// read off the method means being able to say what an endpoint opens without
// looking at its handler. The right way not to tie a side-effect-free
// computation to the write scope is not to open an exception in the
// vocabulary but to MOVE the endpoint to a read method; the price calculation
// endpoint is the example (GET /admin/v1/price-sets/{id}/calculate, see
// [API.calculatePrice]).
//
// NO scope is ADDED to the /store/v1 endpoint: the storefront surface's
// identity is the publishable key, and that key by definition CARRIES no
// scope.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
	"github.com/bdrtr/gobit/internal/modules/pricing/service"
)

// maxBodyBytes is the maximum size of a single request body.
//
// A bulk price write (SetPrices) can carry thousands of rows in one request, so
// the bound is generous; but it is not unbounded — an unbounded body is the
// cheapest way to exhaust memory with a single request.
const maxBodyBytes int64 = 1 << 20 // 1 MiB

// codeInvalidBody is the error code returned when a request body cannot be
// parsed.
const codeInvalidBody = "pricing_invalid_body"

// Scope vocabulary: the scopes pricing's admin endpoints ask for.
//
// The vocabulary has the SAME shape in every module and DELIBERATELY consists
// of two entries: read and write. Defining a separate scope per resource
// ("price-lists:write", "price-rules:read" …) would grow the list but would
// not make possible any new decision that can be made today; the split is
// added when it is genuinely needed.
const (
	// ScopeRead is the scope the READ endpoints of pricing's admin surface ask
	// for.
	//
	// It is enough to read price sets, price lists and price rules; it opens no
	// write endpoint. Fully privileged identities do not need it granted
	// separately: a caller carrying corehttp.ScopeAdmin satisfies this one too
	// (see corehttp.Principal.HasScope).
	ScopeRead = "pricing:read"

	// ScopeWrite is the scope the WRITE endpoints of pricing's admin surface
	// ask for.
	//
	// An identity that can write prices can drop the whole catalog to a single
	// minor unit in one request; that is why it matters that integrations which
	// only REPORT prices can make do with [ScopeRead].
	ScopeWrite = "pricing:write"
)

// API holds pricing's HTTP handlers.
type API struct {
	svc *service.Service
	// trial is the flow the price list trial runs on; see [API.WithTrial].
	trial PriceListTrial
}

// New builds an API that runs on the given service.
func New(svc *service.Service) *API {
	return &API{svc: svc}
}

// Routes binds pricing's admin and store routes to the router.
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
//  1. IDENTITY — the /admin/v1 endpoints are guarded by corehttp.RequireAdmin.
//     That middleware is mounted not in this module but on the side that
//     builds the router (see corehttp.APIGuards).
//  2. SCOPE — the endpoints are marked HERE, endpoint by endpoint, with
//     corehttp.RequireScope: GET endpoints ask for [ScopeRead], POST/PUT/DELETE
//     endpoints for [ScopeWrite].
//
// Without the second layer authentication would stand in for authorization: an
// admin user whose scopes were emptied could sign in and change every price
// with POST /admin/v1/price-sets/{id}/prices.
//
// NO scope is ADDED to the store endpoint: /store/v1's identity is the
// publishable key, and that key by definition CARRIES no scope.
func (a *API) Routes(r chi.Router) {
	read := r.With(corehttp.RequireScope(ScopeRead))
	write := r.With(corehttp.RequireScope(ScopeWrite))

	write.Post("/admin/v1/price-sets", a.createPriceSet)
	read.Get("/admin/v1/price-sets", a.listPriceSets)
	read.Get("/admin/v1/price-sets/{id}", a.getPriceSet)
	write.Delete("/admin/v1/price-sets/{id}", a.deletePriceSet)
	read.Get("/admin/v1/price-sets/{id}/prices", a.listPrices)
	write.Post("/admin/v1/price-sets/{id}/prices", a.setPrices)

	// The calculation endpoint WRITES nothing and is therefore a GET; it takes
	// its context from the query string (see [API.calculatePrice]). It used to
	// be a POST, and because the vocabulary looks at the method it asked for
	// [ScopeWrite]: an integration that only REPORTS prices had to run, just to
	// get a calculation done, with an identity that could change the whole
	// catalog in a single request. The fix was NOT to open a "this POST is
	// really a read" exception in the vocabulary: once an exception is opened,
	// understanding what an endpoint opens would take reading its handler. The
	// endpoint was moved so that its method states its intent.
	read.Get("/admin/v1/price-sets/{id}/calculate", a.calculatePrice)
	// What the set charged over a window, from the price history (ADR 0167).
	read.Get(pathAdminPriceHistory, a.priceHistory)

	write.Post("/admin/v1/price-lists", a.createPriceList)
	read.Get("/admin/v1/price-lists", a.listPriceLists)
	read.Get("/admin/v1/price-lists/{id}", a.getPriceList)
	write.Put("/admin/v1/price-lists/{id}", a.updatePriceList)
	write.Delete("/admin/v1/price-lists/{id}", a.deletePriceList)
	// What a list would do to the orders of a period (ADR 0220); its answer is
	// orders, so it asks for the order module's read privilege too.
	read.With(corehttp.RequireScope(orderReadScope)).Get(pathListTrial, a.trialPriceList)

	read.Get("/admin/v1/prices/{price_id}/rules", a.listPriceRules)
	write.Post("/admin/v1/prices/{price_id}/rules", a.createPriceRule)
	write.Delete("/admin/v1/price-rules/{id}", a.deletePriceRule)

	r.Get("/store/v1/price-sets/{id}", a.storeGetPriceSet)
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

// writeItems writes an unpaged list in its envelope.
//
// On unpaged endpoints (a container's prices, a price's rules) the envelope's
// numeric fields are filled with the record count: the client's envelope shape
// does not change from endpoint to endpoint.
//
// Limit EQUALS the number of records returned and is NOT CLIPPED to
// [service.MaxLimit]. Were it clipped, the response for a container with 250
// prices would say "count=250, limit=100"; the client would take the page size
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
// It fits it into the int32 RANGE only; it applies no page size bound (see
// writeItems). The lower bound is checked too: count is the result of a len()
// and cannot be negative, but the presence of the check proves LOCALLY that the
// conversion to int32 is safe for every input; a change far from the caller
// cannot silently produce a wraparound.
func clampCount(count int64) int32 {
	if count < 0 {
		return 0
	}
	if count > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(count)
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

// decodeBody decodes the request body into the destination.
//
// Unknown fields are REJECTED: a silently ignored field means a price the
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

// The calculation endpoint's query parameters.
//
// The reserved names are the SAME as the JSON field names of the endpoint's
// POST form; they were not renamed when it moved. A new set of names would have
// loaded a name change on top of the method change onto the client and gained
// nothing in return. Their form matches the module's other query parameters
// too (limit, offset): plain snake_case.
const (
	// paramCurrencyCode is the requested currency (ISO 4217).
	paramCurrencyCode = "currency_code"
	// paramQuantity is the quantity the calculation is made for.
	paramQuantity = "quantity"
	// paramAt is the moment of the calculation (RFC 3339).
	paramAt = "at"
	// paramAttrPrefix is the query prefix of the rule context fields.
	paramAttrPrefix = "attr_"
)

// calculateQuery reads the calculation context from the query string.
//
// An UNRECOGNIZED parameter is an error; this is the GET counterpart of the
// strictness of [decodeBody] in the endpoint's POST form. Were they silently
// ignored, a client writing "?qty=10" would read the price for a single unit
// while believing it had asked for the price of 10 — a failure that returns no
// error and only gives a WRONG answer.
//
// Giving the same parameter twice is an error too: net/url keeps the second
// value, but url.Values.Get returns only the first, and a silent choice means
// calculating in a context OTHER than the one the client sent. The rule context
// is a map as well; a field cannot have two values.
func calculateQuery(r *http.Request) (service.CalculateParams, error) {
	attributes := map[string]string{}
	for name, values := range r.URL.Query() {
		if len(values) > 1 {
			return service.CalculateParams{}, coreerrors.Invalid(codeInvalidBody,
				"the %q parameter was given more than once", name)
		}
		switch {
		case name == paramCurrencyCode, name == paramQuantity, name == paramAt:
			// Reserved names; their values are read one by one below.
		case strings.HasPrefix(name, paramAttrPrefix):
			attributes[strings.TrimPrefix(name, paramAttrPrefix)] = values[0]
		default:
			return service.CalculateParams{}, coreerrors.Invalid(codeInvalidBody,
				"the %q parameter is not recognized; the rule context is given with the %q prefix",
				name, paramAttrPrefix)
		}
	}

	quantity, err := intParam(r, paramQuantity)
	if err != nil {
		return service.CalculateParams{}, err
	}
	at, err := timeParam(r, paramAt)
	if err != nil {
		return service.CalculateParams{}, err
	}

	// NO validation is done here: the service decides on the validity of the
	// currency, the quantity bounds and the defaults (see toPriceInputs in
	// dto.go).
	return service.CalculateParams{
		CurrencyCode: r.URL.Query().Get(paramCurrencyCode),
		Quantity:     quantity,
		Attributes:   attributes,
		At:           at,
	}, nil
}

// timeParam reads a single time query parameter; it returns the zero time when
// the parameter is absent.
//
// For the service the zero time means "now". A timestamp that cannot be parsed
// does NOT silently FALL BACK to "now": answering a price asked for a past or
// future moment with today's campaigns is the most expensive form of silent
// wrongness.
func timeParam(r *http.Request, name string) (time.Time, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return time.Time{}, nil
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidBody,
			"the %q parameter has to be in RFC 3339 form, %q was given", name, raw)
	}
	return value.UTC(), nil
}

// listTypeOrNil returns the price list type as a string pointer; nil when it is
// empty.
//
// A base price has no list type, and showing null rather than an empty string
// in JSON keeps "no list" apart from "a list whose type is empty".
func listTypeOrNil(t models.PriceListType) *string {
	if t == "" {
		return nil
	}
	value := string(t)
	return &value
}
