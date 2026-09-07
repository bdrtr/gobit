package searchpg

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// The endpoints the plugin opens and the scopes they require.
const (
	// SearchPath is the storefront search endpoint.
	SearchPath = "/store/v1/search"
	// ReindexPath is the full reindex endpoint.
	ReindexPath = "/admin/v1/search/reindex"
	// ScopeWrite is the scope the reindex endpoint requires.
	//
	// It has the SAME shape as the modules' ("<module>:write"). No read scope was
	// defined: this module's only admin endpoint is one that WRITES, and a scope
	// name that is never handed out is a name nobody knows the purpose of on the
	// day it is first deployed (the same reasoning as product's api/routes.go).
	ScopeWrite = ModuleName + ":write"
)

// The names of the query parameters.
const (
	paramQuery  = "q"
	paramLimit  = "limit"
	paramOffset = "offset"
)

// The limits of a search request.
const (
	// defaultLimit is how many records come back when no limit is given.
	defaultLimit = 20
	// maxLimit is the most records one request can return.
	//
	// The value is the SAME as the catalog's bulk-read limit (product
	// service.MaxLimit) and it is repeated by hand. Were it exceeded, the request
	// would be refused by the CATALOG rather than here and the user would see
	// another module's error message instead of search's own limit.
	maxLimit = 100
	// maxQueryBytes is the byte limit of the query text. An unbounded text
	// would force websearch_to_tsquery to parse megabytes of input in a single
	// request; no query that fits in a search box comes near this limit.
	maxQueryBytes = 256
)

// The error codes.
const (
	codeQueryMissing  = "searchpg_query_missing"
	codeQueryTooLong  = "searchpg_query_too_long"
	codeBadQueryParam = "searchpg_bad_query_param"
)

// listEnvelope is the envelope of the list responses (plan Section 8).
//
// Count is the number of records IN THIS RESPONSE, not the total number of
// matches in the index, and that is deliberate: the channel filter is applied by
// the catalog while the records are read, so the real total could only be known
// after EVERY match had been read and filtered. Writing the raw match count from
// the index would be worse — the number would be a lie that counted products the
// client can never see.
//
// The visible consequence: a page may come back with FEWER records than the
// limit even though more matches exist. A client should page "until an empty
// page arrives".
type listEnvelope struct {
	Data   []json.RawMessage `json:"data"`
	Count  int               `json:"count"`
	Offset int               `json:"offset"`
	Limit  int               `json:"limit"`
}

// singleEnvelope is the envelope of the single-object responses.
type singleEnvelope struct {
	Data any `json:"data"`
}

// search is GET /store/v1/search.
//
// The flow: RELEVANCE-ORDERED ids are found in the index, and the records are
// read from the catalog's "product.interop" surface IN THE SAME ORDER. Every
// record in the response has exactly the shape the storefront's product endpoint
// writes; the plugin does not reshape it.
//
// The publication status and the sales channel filter are applied by the catalog
// (see the package documentation). That is why even a stale row left in the
// index cannot LEAK a product from somebody else's channel: an id that does not
// pass the filter never enters the response.
func (m *searchModule) search(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	query, err := searchQuery(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	limit, offset, err := paging(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	ids, err := m.index.Search(ctx, query, limit, offset)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	products, err := m.catalog.products(ctx, ids, channels(r))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:   products,
		Count:  len(products),
		Offset: offset,
		Limit:  limit,
	})
}

// reindexEndpoint is POST /admin/v1/search/reindex.
//
// It indexes the whole catalog from scratch and runs SYNCHRONOUSLY: the response
// comes back with the counts when the work is done. Pushing it to the background
// (202 Accepted) would tell the caller only "it started", and because there is
// nowhere for them to see the outcome, a failed round would disappear silently.
// The price is a long request on a large catalog; this endpoint is an operations
// tool and is not on the customer's path.
//
// # A known limit: the server's write timeout
//
// WRITE_TIMEOUT (30s by default) can be exhausted on a large enough catalog. In
// that case the RESPONSE does not reach the client but the WORK completes: the
// timeout cuts the connection's write deadline, it does not stop the handler —
// the index is still built and the caller simply does not see the counts. When
// the scale reaches here, the right step is not to push the endpoint into the
// background (there is nowhere to see the outcome) but to split the round into
// page ranges the operator runs.
func (m *searchModule) reindexEndpoint(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	result, err := m.reindex(ctx)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: result})
}

// searchQuery reads the search text from the request.
//
// An empty query is REFUSED. Had it meant "return the whole catalog", search
// would be a second copy of the listing endpoint and an empty search box would
// pull the entire catalog by accident; the way to list the catalog is already
// GET /store/v1/products.
func searchQuery(r *http.Request) (string, error) {
	query := strings.TrimSpace(r.URL.Query().Get(paramQuery))
	if query == "" {
		return "", coreerrors.Invalid(codeQueryMissing,
			"the %s parameter is required and cannot be empty", paramQuery)
	}
	if len(query) > maxQueryBytes {
		return "", coreerrors.Invalid(codeQueryTooLong,
			"the %s parameter can be at most %d bytes (%d given)",
			paramQuery, maxQueryBytes, len(query))
	}

	return query, nil
}

// paging reads the limit and offset parameters.
//
// A limit above the maximum is NOT clamped, it is refused: a limit clamped
// silently makes a client receive fewer records than it asked for and never
// notice.
func paging(r *http.Request) (limit, offset int, err error) {
	limit, err = intParam(r, paramLimit, defaultLimit)
	if err != nil {
		return 0, 0, err
	}
	offset, err = intParam(r, paramOffset, 0)
	if err != nil {
		return 0, 0, err
	}

	switch {
	case limit < 1:
		return 0, 0, coreerrors.Invalid(codeBadQueryParam,
			"%s has to be at least 1 (%d given)", paramLimit, limit)
	case limit > maxLimit:
		return 0, 0, coreerrors.Invalid(codeBadQueryParam,
			"%s can be at most %d (%d given)", paramLimit, maxLimit, limit)
	case offset < 0:
		return 0, 0, coreerrors.Invalid(codeBadQueryParam,
			"%s cannot be negative (%d given)", paramOffset, offset)
	}

	return limit, offset, nil
}

// intParam reads a query parameter as an integer; it returns the default
// when the parameter is absent.
func intParam(r *http.Request, name string, fallback int) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, coreerrors.Wrap(err, coreerrors.KindInvalid, codeBadQueryParam,
			"the %s parameter has to be an integer (%q given)", name, raw)
	}

	return value, nil
}

// channels reads the sales channels the request is bound to FROM THE VERIFIED
// IDENTITY.
//
// The query string is NOT consulted at all, and that is a security decision:
// had "?sales_channel_id=" been accepted, a client holding any publishable key
// could search another channel's catalog. The identity is placed by the core's
// corehttp.RequireStore middleware.
//
// The difference between nil and an empty slice MEANS something and is defined
// by the catalog; the mapping here is EXACTLY the one in the product module's
// storefront endpoint (api/store.go salesChannelIDs):
//
//   - No identity gives nil: store authentication is not wired in this
//     installation and no filter is applied. Otherwise search would silently
//     return nothing on an installation without auth.
//   - With an identity nil is NEVER returned: an identity with no channel means
//     an EMPTY SET, not "no filtering".
func channels(r *http.Request) []string {
	principal, ok := corehttp.PrincipalFromContext(r.Context())
	if !ok {
		return nil
	}
	if principal.SalesChannelIDs == nil {
		return []string{}
	}

	return principal.SalesChannelIDs
}
