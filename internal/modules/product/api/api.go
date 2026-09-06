// Package api is the product module's HTTP surface.
//
// The layer is THIN: it decodes the body, reads the query parameters, calls the
// service and wraps the response in an envelope. There is NO business rule here
// and the HTTP status code is NOT PICKED BY HAND — the code is derived by
// corehttp.WriteError from the class of the typed error the service returned
// (plan Section 8).
//
// Response envelope: list endpoints return {"data": [...], "count": N,
// "offset": N, "limit": N}, single endpoints return {"data": {...}}.
//
// # Scopes
//
// The /admin/v1 endpoints ask for a scope and the dictionary splits in two: GET
// endpoints [ScopeRead], POST/PUT/PATCH/DELETE endpoints [ScopeWrite] (see
// [Handler.Routes]). corehttp.ScopeAdmin is the SUPER SCOPE and satisfies both
// on its own.
//
// NO scope is ADDED to the /store/v1 endpoints: the identity of the store
// surface is the publishable key and that key by definition CARRIES NO scope.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	corepage "github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// maxBodyBytes is the upper bound of a single request body.
//
// Without a limit a single client could exhaust the server's memory while the
// body was being decoded. 1 MiB is more than enough for a product that arrives
// together with its image list and its variants.
const maxBodyBytes = 1 << 20

// Error codes; a client can look these up with errors.CodeOf.
const (
	codeBadJSON  = "product_bad_json"
	codeBadParam = "product_bad_query_param"
)

// Handler carries the module's HTTP handlers.
type Handler struct {
	svc Catalog
	// graphql is the storefront's GraphQL read endpoint (see [graph.NewHandler]).
	//
	// It is a FIELD of the handler, not built per request: the gqlgen server
	// parses the schema once and carries the parsed-query cache inside itself;
	// rebuilding it on every request would throw both away.
	graphql http.Handler
}

// New builds a handler with the given service and GraphQL limits.
//
// The GraphQL endpoint is built HERE so that the module's whole HTTP surface is
// wired from a single place (see [Handler.Routes]); otherwise the list of
// endpoints would be split across two files.
//
// graphOpts may be the ZERO VALUE and then gives the package defaults; it does
// NOT mean "unlimited" (see [graph.Options]). The limits are not interpreted in
// the api layer, they are passed through as they are: picking a default here
// would be a second definition of the same rule.
//
// svc may be nil (documentation generation does this): the gqlgen server does
// not touch the service while it is being built, it only calls it once a
// request arrives.
func New(svc Catalog, graphOpts graph.Options) *Handler {
	return &Handler{svc: svc, graphql: graph.NewHandler(svc, graphOpts)}
}

// That the concrete service satisfies the surface the api layer expects is
// pinned at compile time: a signature drift shows up in the build, not in a
// test.
var _ Catalog = (*service.Service)(nil)

// listEnvelope is the envelope of list responses (plan Section 8).
type listEnvelope struct {
	Data any `json:"data"`
	// Count is the TOTAL number of records matching the filter and it DROPS
	// out of the body entirely when it WAS NOT COUNTED (omitempty + pointer).
	//
	// The only endpoint where counting can be turned off today is the
	// storefront listing (see [Handler.storeListProducts]); every other list
	// always counts and the field is always written in those — that is, the
	// bytes of the default response DO NOT CHANGE.
	//
	// # Why not 0, why not null, why ABSENT
	//
	// 0 is a LIE: it cannot be told apart from the sentence "no matching
	// record" and the client computes zero pages. A JSON null changes the TYPE
	// of the field (integer → integer|null) and in JavaScript it is silently
	// coerced to a number — `null / 20` is zero, `undefined / 20` is NaN; that
	// is, the missing field gives the wrong answer LOUDLY, null gives it
	// silently.
	//
	// The real reason is not that one, it is SYMMETRY: on the GraphQL surface
	// of the same catalog "count" is already absent from the response unless
	// the client selects it. Dropping the field does not invent a new shape, it
	// brings the two read surfaces to the same sentence — the counter is there
	// AS LONG AS IT IS ASKED FOR.
	//
	// That the field can drop is written in the OpenAPI document too: the
	// response schema of the storefront listing DOES NOT PUT "count" among the
	// required fields (see openapi.Doc.ListOptionalCount).
	Count  *int `json:"count,omitempty"`
	Offset int  `json:"offset"`
	Limit  int  `json:"limit"`
	// NextCursor is the opaque position to send back as "after" for the next
	// page; it is ABSENT when this page is the last one.
	//
	// Its absence is the end-of-listing signal, which is what a client walking
	// forward needs and what offset alone cannot give without a count.
	NextCursor string `json:"next_cursor,omitempty"`
}

// itemEnvelope is the envelope of single responses.
type itemEnvelope struct {
	Data any `json:"data"`
}

// writeList writes a paginated result wrapped in the envelope.
//
// An empty result becomes an EMPTY ARRAY in JSON, not null: the client being
// able to treat the "data" field as an array every time is better than it
// having to check for null in every response.
func writeList[T any](w http.ResponseWriter, r *http.Request, res service.ListResult[T]) {
	items := res.Items
	if items == nil {
		items = []T{}
	}
	corehttp.WriteJSON(r.Context(), w, http.StatusOK, listEnvelope{
		Data:       items,
		Count:      res.Count,
		Offset:     res.Offset,
		Limit:      res.Limit,
		NextCursor: res.NextCursor,
	})
}

// writeItem writes a single record wrapped in the envelope.
func writeItem(w http.ResponseWriter, r *http.Request, status int, v any) {
	corehttp.WriteJSON(r.Context(), w, status, itemEnvelope{Data: v})
}

// decode decodes the request body.
//
// An unknown field is REJECTED: a client that writes "titel" learns what it did
// right away instead of silently creating a product with no title. If the body
// exceeds the limit a typed validation error is returned as well; it is not a
// server error.
func decode[T any](w http.ResponseWriter, r *http.Request) (T, error) {
	var out T

	// The response writer is handed over so the server can terminate the
	// request cleanly when the limit is exceeded; otherwise the connection
	// would be left with a half-read body.
	body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(&out); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return out, coreerrors.Wrap(err, coreerrors.KindInvalid, codeBadJSON,
				"request body is too large (at most %d bytes)", maxBodyBytes)
		}
		if errors.Is(err, io.EOF) {
			return out, coreerrors.Invalid(codeBadJSON, "request body is empty")
		}
		return out, coreerrors.Wrap(err, coreerrors.KindInvalid, codeBadJSON,
			"request body could not be decoded: %v", err)
	}

	// If more than a single JSON value was sent the request is ambiguous; the
	// second body being silently ignored means the sender does not know which
	// record was written.
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return out, coreerrors.Invalid(codeBadJSON, "request body has to be a single JSON object")
	}
	return out, nil
}

// pathParam reads a path parameter and returns a typed error when it is empty.
func pathParam(r *http.Request, name string) (string, error) {
	value := strings.TrimSpace(chi.URLParam(r, name))
	if value == "" {
		return "", coreerrors.Invalid(codeBadParam, "the %s path parameter is required", name)
	}
	return value, nil
}

// intParam reads a query parameter as an integer; returns the fallback when it
// is absent.
func intParam(r *http.Request, name string, fallback int) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, coreerrors.Wrap(err, coreerrors.KindInvalid, codeBadParam,
			"the %s parameter has to be an integer (given: %q)", name, raw)
	}
	return value, nil
}

// stringParam reads a query parameter; returns nil when it was not given.
//
// An empty string counts as "not given" as well: building a filter with
// "?handle=" that matches no product at all is not what the client intends.
func stringParam(r *http.Request, name string) *string {
	value := strings.TrimSpace(r.URL.Query().Get(name))
	if value == "" {
		return nil
	}
	return &value
}

// boolParam reads a query parameter as a boolean value; returns the fallback
// when it is absent.
//
// The default is written AT THE CALL SITE, not here — the same shape as
// [intParam]. A constant false default stopped being enough: the default of the
// "with_count" parameter is TRUE (see [Handler.storeListProducts]) and burying
// the default inside the function would be a rule that IS NOT WRITTEN in the
// signature the caller sees.
func boolParam(r *http.Request, name string, fallback bool) (bool, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, coreerrors.Wrap(err, coreerrors.KindInvalid, codeBadParam,
			"the %s parameter has to be a boolean value (given: %q)", name, raw)
	}
	return value, nil
}

// sortParam reads the listing order.
//
// An absent value is the default and not an error: the parameter is additive
// and every client that predates it keeps working unchanged. A value that is
// not in the set IS refused, for the reason [boolParam] refuses a
// non-boolean — a client that asked for an order it did not get cannot tell
// that from a catalog which happens to look that way, so the mistake has to
// arrive as an error rather than as data.
//
// The set is closed and lives in the models package beside the values
// themselves; the API does not carry a second copy of it.
func sortParam(r *http.Request) (models.ProductOrder, error) {
	raw := r.URL.Query().Get("sort")
	if raw == "" {
		return models.ProductOrderNewest, nil
	}

	order := models.ProductOrder(raw)
	if !order.Valid() {
		return "", coreerrors.Invalid(codeBadParam,
			"the sort parameter has to be one of newest, oldest (given: %q)", raw)
	}

	return order, nil
}

// afterParam reads the cursor of the page being asked for.
//
// # Why an offset alongside it is REFUSED
//
// A cursor and an offset each name a position, and honoring both would serve
// the page that is N rows past the cursor — a position neither parameter asked
// for and no client meant. Refusing is what makes a client that is migrating
// find out at the first request instead of through quietly skipped rows.
func afterParam(r *http.Request, listing string, offset int) (corepage.Cursor, error) {
	raw := r.URL.Query().Get("after")
	if raw == "" {
		return corepage.Cursor{}, nil
	}
	if offset != 0 {
		return corepage.Cursor{}, coreerrors.Invalid(codeBadParam,
			"\"after\" and \"offset\" name two different positions; send one of them")
	}

	return corepage.Decode(listing, raw)
}

// paging reads the pagination parameters.
func paging(r *http.Request) (limit, offset int, err error) {
	limit, err = intParam(r, "limit", 0)
	if err != nil {
		return 0, 0, err
	}
	offset, err = intParam(r, "offset", 0)
	if err != nil {
		return 0, 0, err
	}
	return limit, offset, nil
}
