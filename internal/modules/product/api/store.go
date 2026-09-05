package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// storeListProducts GET /store/v1/products
//
// This is the heart of Phase 4: the storefront listing returns products
// together with their PRICE and STOCK information. Both are the data of other
// modules and arrive here through links, over the Query layer; the product
// module does not import them.
//
// The listing is also filtered by the SALES CHANNEL of the request; for where
// the channels are read from see [salesChannelIDs].
//
// # with_count
//
// The total counter is optional and its DEFAULT IS TRUE: a request that does
// not give the parameter gets today's response byte for byte. A request that
// says "with_count=false" DOES NOT run the count query at all and its envelope
// carries no "count" field (see [listEnvelope]).
//
// Why a parameter is needed: independently of the page size, the counter walks
// the whole set the sales channel filter is applied to. Measured on gobit_load
// (52,004 products, 52,000 channel assignments, LIMIT 20, median) — what is
// measured is the SERVICE CALL (service.ListProducts), not the whole endpoint:
//
//	counting (today's default)        67.00 ms
//	not counting (with_count=false)    0.65 ms
//
// The rest of the endpoint — the price and stock enrichment of the variants —
// is independent of the counter and with_count=false DOES NOT SKIP it; when
// measured, both legs take 0.1-0.2 ms over an index, so the countless endpoint
// is ~1 ms, not 0.65. The ratio does not change: on a large catalog nearly all
// of the request's SQL is the counter and its cost grows WITH THE CATALOG, not
// with the page size. The number is something the client needs once on the first
// page; on every following page the same number is computed again.
//
// The value is neither IGNORED nor interpreted: "with_count=abc" returns a
// typed validation error (see [boolParam]). Silently falling back to the
// default would be the client thinking it had turned the counter off while it
// kept paying the cost.
func (h *Handler) storeListProducts(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := paging(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	withCount, err := boolParam(r, "with_count", true)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	after, err := afterParam(r, service.ProductListing, offset)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	result, err := h.svc.ListStoreProducts(r.Context(), service.StoreListOptions{
		CollectionID:    stringParam(r, "collection_id"),
		Search:          stringParam(r, "q"),
		SalesChannelIDs: salesChannelIDs(r),
		Limit:           limit,
		Offset:          offset,
		After:           after,
		SkipCount:       !withCount,
	})
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeList(w, r, result)
}

// storeGetProduct GET /store/v1/products/{id}
//
// The path segment may be a product id or a handle: storefront addresses carry
// a handle ("/store/v1/products/tisort"), admin flows carry an id.
//
// The single endpoint is subject to the SAME sales channel filter as the
// listing; for the reasoning see service.Service.GetStoreProduct.
func (h *Handler) storeGetProduct(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	product, err := h.svc.GetStoreProduct(r.Context(), id, salesChannelIDs(r))
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, product)
}

// salesChannelIDs reads the sales channels the request is bound to FROM THE
// AUTHENTICATED IDENTITY.
//
// The channel MUST NOT be a value the client declares in the query string, and
// that is why r.URL.Query() is never looked at here: had "?sales_channel_id=..."
// been accepted, a client arriving with any publishable key it happened to hold
// could read ANOTHER channel's catalog — that is, the filter would stop being
// an authorization and turn into a display preference. The identity is put in
// place by the core's corehttp.RequireStore middleware; the channel list comes
// from the key's record.
//
// The rule ITSELF is not here but in [graph.SalesChannelIDsFromContext], and
// the reason is the second read surface: the GraphQL resolvers have no
// *http.Request in hand, only a context. Had the rule been written in two
// places, the day one was fixed and the other forgotten there would be a
// catalog leak on one of the surfaces — including the difference between a nil
// and an EMPTY slice on return (for the meaning see that documentation; the two
// say different things).
func salesChannelIDs(r *http.Request) []string {
	return graph.SalesChannelIDsFromContext(r.Context())
}
