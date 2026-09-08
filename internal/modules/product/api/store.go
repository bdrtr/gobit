package api

import (
	"net/http"
	"slices"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// paramSalesChannelID is the name of the sales channel path parameter.
//
// It is spelled exactly as the admin surface already spells the concept in
// DELETE /admin/v1/products/{id}/sales-channels/{sales_channel_id}, so one
// concept keeps one spelling on both surfaces and a reader who has seen it once
// does not have to learn a second name for it (ADR 0044).
const paramSalesChannelID = "sales_channel_id"

// The three channel-scoped storefront catalog paths.
//
// # Why the channel is IN THE PATH (ADR 0044)
//
// These three bodies vary with the caller, and until this decision they varied
// through the "x-publishable-api-key" HEADER: the same URL returned one
// storefront's catalog to one key and another storefront's to another. Nothing
// in this repository emits Vary for that header, so a shared cache in front of
// the origin had NO cache key it could see — it would either have to be
// configured to vary by a custom header (that is, store one identical body per
// key and empty the lot on a key rotation) or serve one storefront's catalog to
// another. Moving the channel into the URL replaces that dilemma: the body
// becomes a function of the channel alone, two keys authorized for the same
// channel receive byte-identical bodies, and the cache key is the URL every
// shared cache already keys on with nothing configured.
//
// The segment is LITERAL ("sales-channels/{id}") rather than a bare parameter
// under the prefix, because "/store/v1/{sales_channel_id}/products" would put a
// wildcard where "carts", "customers", "orders" and "collections" already sit.
//
// Nothing had to be added to make the id fit in a URL: a channel id is the
// "sc_" prefix plus unpadded Crockford Base32, and every character of that
// alphabet — and the underscore — is unreserved under RFC 3986.
//
// # Why they are constants and the admin paths are not
//
// Each of these three is written in TWO places, [Handler.Routes] and
// [Describe], and the pair has to stay identical or the document describes an
// endpoint that does not exist. That was survivable while the path was a short
// literal; a forty-character pattern with two placeholders in it is not, and
// the OpenAPI build only notices a drift as a description that matches no route
// (see openapi.Doc.UnmatchedDescriptions), which names the description rather
// than the typo. The tests still spell the paths out by hand, which is what
// keeps the constant honest about what it is serving.
//
// They are whole literals rather than a shared prefix plus a suffix on purpose:
// the storefront route audit in internal/arch resolves a route's path from a
// string literal or from a constant whose value IS one, and a value built by
// concatenation reads back as unknown — the route would silently drop out of
// that audit's population.
const (
	// pathStoreProducts is the storefront product listing.
	pathStoreProducts = "/store/v1/sales-channels/{sales_channel_id}/products"
	// pathStoreProduct is the single storefront product, by id or by handle.
	pathStoreProduct = "/store/v1/sales-channels/{sales_channel_id}/products/{id}"
	// pathStoreOptionValues is the storefront option vocabulary.
	pathStoreOptionValues = "/store/v1/sales-channels/{sales_channel_id}/option-values"
)

// codeChannelNotAuthorized reports that the key does not hold the sales channel
// the path names.
const codeChannelNotAuthorized = "product_sales_channel_not_authorized"

// storeListProducts GET /store/v1/sales-channels/{sales_channel_id}/products
//
// This is the heart of Phase 4: the storefront listing returns products
// together with their PRICE and STOCK information. Both are the data of other
// modules and arrive here through links, over the Query layer; the product
// module does not import them.
//
// The listing is also filtered by the SALES CHANNEL the path names, narrowed to
// what the request's key holds; for the rule see [storeChannelScope].
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
	// The scope is resolved FIRST and the refusal is returned before any
	// parameter is parsed: a request for a channel the key does not hold must
	// not be able to tell a bad cursor from a good one, and answering 422 to it
	// would say the channel itself was acceptable.
	channels, err := storeChannelScope(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

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
	order, err := sortParam(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	// The cursor is decoded against the listing name of THE ORDER ASKED FOR, so
	// a cursor minted under the other order is refused here rather than serving
	// a page out of a key space it does not describe.
	after, err := afterParam(r, service.ProductListingFor(order), offset)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	result, err := h.svc.ListStoreProducts(r.Context(), service.StoreListOptions{
		Order:           order,
		CollectionID:    stringParam(r, "collection_id"),
		CategoryID:      stringParam(r, "category_id"),
		TagID:           stringParam(r, "tag_id"),
		Search:          stringParam(r, "q"),
		SalesChannelIDs: channels,
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

// storeGetProduct GET /store/v1/sales-channels/{sales_channel_id}/products/{id}
//
// The trailing path segment may be a product id or a handle: storefront
// addresses carry a handle, admin flows carry an id.
//
// The single endpoint is subject to the SAME sales channel filter as the
// listing; for the reasoning see service.Service.GetStoreProduct.
func (h *Handler) storeGetProduct(w http.ResponseWriter, r *http.Request) {
	channels, err := storeChannelScope(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	product, err := h.svc.GetStoreProduct(r.Context(), id, channels)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, product)
}

// storeChannelScope resolves the sales channels a catalog read is scoped to:
// the ONE channel the path names, NARROWED by what the request's identity
// holds.
//
// # The claim is in the path, the evidence is in the key
//
// A value the client states is a CLAIM, not a fact (ADR 0008), and the whole
// safety of ADR 0044 is that this function only ever NARROWS. The path selects
// a channel; the key decides whether that selection is honored. A path naming a
// channel the key does not hold is REFUSED rather than served, so the segment
// can never widen an answer — the client picks WHICH of its channels to read,
// never WHETHER it may read one.
//
// Where there is no identity at all — the deployment that never wired store
// authentication up, which [graph.SalesChannelIDsFromContext] returns nil for on
// purpose — there is no set to intersect against and the path value stands
// alone. That is NARROWER than the behavior it replaces (nil meant "do not
// filter", that is, every channel's catalog) and never wider, which is why the
// nil case is allowed to pass rather than being made an error: a product module
// deployed on its own still serves a storefront, and it now serves the channel
// asked for instead of everything.
//
// # Why the refusal is 403 and not 404
//
// A 404 is what a HIDDEN PRODUCT gets, and it is 404 precisely so that the key's
// owner cannot enumerate another storefront's handles one at a time. No such
// leak exists here, because this function never consults the channel table: it
// compares the segment against the key's own set and nothing else. The answer
// is therefore the same whether the named channel exists, belongs to another
// merchant, or was never created — "I know who you are and this is not yours",
// which is what a 403 means in this repository (see corehttp.RequireScope).
//
// # Why the query string is still dead
//
// r.URL.Query() is still never consulted for a channel here, and
// "?sales_channel_id=" remains ignored. The path segment is a NEW input that
// carries no prior promise; the query parameter carries one made in three
// godocs and pinned by two tests, and giving it a live meaning would mean
// inverting a test whose failure mode is a catalog leak.
//
// The identity side of the rule is NOT reimplemented here: it stays in
// [graph.SalesChannelIDsFromContext], the single place both read surfaces can
// reach — the GraphQL resolvers have no *http.Request in hand, only a context,
// and a second copy of the derivation is how one surface gets fixed and the
// other forgotten.
func storeChannelScope(r *http.Request) ([]string, error) {
	channelID, err := pathParam(r, paramSalesChannelID)
	if err != nil {
		return nil, err
	}

	// nil is "no identity in this deployment"; an EMPTY BUT NON-nil slice is an
	// identity that holds no channel, and that one holds nothing to narrow to.
	// Collapsing the two would let a channelless key read whatever channel it
	// names.
	held := graph.SalesChannelIDsFromContext(r.Context())
	if held != nil && !slices.Contains(held, channelID) {
		return nil, coreerrors.Forbidden(codeChannelNotAuthorized,
			"this key is not bound to the %q sales channel", channelID)
	}

	return []string{channelID}, nil
}

// The storefront's vocabulary endpoints.
//
// # Why they exist
//
// The listing takes a collection id, a category id and a tag id, and a
// storefront has none of them: it has the WORD a shopper clicked. Until these
// endpoints existed there was no public way to turn "t-shirts" into an id, so
// every filter the catalog supports was unusable from outside — a capability
// with a caller that could not be written.
//
// # Why three of them are not sales-channel scoped
//
// A product is hidden per channel because a product is what is sold; the
// taxonomy is one tree for the whole installation. What DOES hide a category is
// the merchant's own switch: the storefront listing passes PublicOnly, so a
// category that is switched off (is_active) or exists for operators
// (is_internal) is not listed. Those two flags have been in the schema since
// the first migration and nothing read them until now.
//
// Collections and tags carry no such flag, so there is nothing to hide behind:
// a collection exists or it does not.
//
// # Why their URLs did not move and the catalog's did
//
// ADR 0044 put the sales channel into the path of the reads whose BODY varies
// by channel, so that a shared cache has a key it can see. These three do not
// vary by channel — the same bytes go to every storefront — so a channel
// segment on them would be a cache key with nothing behind it and a parameter
// the handler would have to ignore. The storefront's URLs are therefore no
// longer uniform, and the rule that decides which shape an endpoint has is
// exactly this one: the catalog listing, the single product and the option
// vocabulary carry the segment BECAUSE their answers differ per channel;
// collections, categories and tags do not carry it because their answers do
// not.

// storeListCollections GET /store/v1/collections
func (h *Handler) storeListCollections(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := paging(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}

	result, err := h.svc.ListCollections(r.Context(), limit, offset)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	writeList(w, r, result)
}

// storeListCategories GET /store/v1/categories
//
// parent_id walks the tree one level at a time, which is what a navigation menu
// asks for. Without it the whole tree comes back flat and the client has to
// rebuild the hierarchy from parent_id fields it was not going to read.
func (h *Handler) storeListCategories(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := paging(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}

	result, err := h.svc.ListCategories(r.Context(), service.ListCategoriesOptions{
		ParentID:   stringParam(r, "parent_id"),
		PublicOnly: true,
		Limit:      limit,
		Offset:     offset,
	})
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	writeList(w, r, result)
}

// storeListOptionValues
// GET /store/v1/sales-channels/{sales_channel_id}/option-values
//
// The fourth vocabulary endpoint, and the only one that returns TEXT rather
// than ids: an option value belongs to exactly one product, so its id is
// useless as a catalog filter. See service.Service.ListOptionValues.
//
// It is scoped by the sales channel for the same reason the listing is — the
// values come from products, so an unscoped vocabulary would name what the
// listing hides. That is also why it is the ONE vocabulary endpoint that moved
// under the channel segment while collections, categories and tags did not: a
// body that varies by channel needs the channel in its cache key, and a body
// that does not, does not.
func (h *Handler) storeListOptionValues(w http.ResponseWriter, r *http.Request) {
	channels, err := storeChannelScope(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}

	limit, offset, err := paging(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}

	result, err := h.svc.ListOptionValues(r.Context(), service.ListOptionValuesOptions{
		SalesChannelIDs: channels,
		PublicOnly:      true,
		Limit:           limit,
		Offset:          offset,
	})
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	writeList(w, r, result)
}

// storeListTags GET /store/v1/tags
func (h *Handler) storeListTags(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := paging(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}

	result, err := h.svc.ListTags(r.Context(), limit, offset)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	writeList(w, r, result)
}
