package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	corepage "github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/product/models"
)

// The filter keys the Query providers understand.
//
// The keys are a contract: if a provider sees a filter it does not recognize it
// returns errors.Invalid (ADR 0004), so the names have to live in a single
// place.
const (
	filterIDs          = "ids"
	filterID           = "id"
	filterProductID    = "product_id"
	filterProductIDs   = "product_ids"
	filterStatus       = "status"
	filterHandle       = "handle"
	filterCollectionID = "collection_id"

	// filterCategoryID and filterTagID are the taxonomy filters of the product
	// provider, and they are spelled THE WAY THE STOREFRONT SPELLS THEM.
	//
	// The storefront reads the same two words off the query string (see
	// api/store.go, "category_id" and "tag_id") and hands them to the same
	// repository fields. Had the read layer picked a second spelling for the
	// same concept — "category" here, "category_id" there — the two surfaces
	// would answer the same question under two names, and the first consumer
	// that spoke both (the panel and the shop are one installation) would have
	// to keep a translation table nothing verifies.
	filterCategoryID = "category_id"
	filterTagID      = "tag_id"

	// filterParentID walks the category tree one level at a time; it is the
	// same word the storefront's category endpoint reads (see api/store.go).
	filterParentID = "parent_id"

	// filterPublicOnly is the category provider's opt-in narrowing to the set a
	// SHOPPER may see; see [categoryProvider.List] for why it is opt-in.
	//
	// The name follows [repository.CategoryFilter].PublicOnly and
	// [ListCategoriesOptions].PublicOnly rather than inventing a third word for
	// the flag the two of them already carry.
	filterPublicOnly = "public_only"

	// FilterSalesChannelIDs is the sales channel filter of the variant provider.
	//
	// Unlike its siblings it is EXPORTED, and the difference comes from the
	// consumer: it is passed from OUTSIDE this module, from the cart flow (see
	// service/provider.go, "The sales channel filter"). That flow CANNOT import
	// this package (ADR 0006) and repeats the key on its own side as a string;
	// the constant being exported is so that a test can bind the two sides
	// together (see internal/arch).
	//
	// The price of the drift is obvious but ACCEPTABLE: the provider returns
	// errors.Invalid for a filter it does not recognize, that is, adding a line
	// to the cart breaks entirely. It is not silent — and it is still better for
	// the fault to be seen in a test rather than in production.
	FilterSalesChannelIDs = "sales_channel_ids"
)

// The keys under which the price and stock records are written in the store
// response.
const (
	keyPriceSet  = "price_set"
	keyInventory = "inventory_item"
)

// codeProviderNotFound is the Query layer's "the provider of this entity is not
// registered in the container" error code.
//
// The code is REPEATED HERE because its counterpart in core/query is unexported
// and the only portable bond between packages is the error code (see
// core/errors: "the code is part of the contract"). If its value changes, this
// module silently becomes less forgiving — the storefront returns an error
// instead of returning without prices; that is better than silently becoming
// more permissive.
const codeProviderNotFound = "query_provider_not_found"

// StoreListOptions is the criteria of the storefront (store) product listing.
//
// There is NO status filter: the storefront shows only published products, and
// letting the client change that would leak draft products.
type StoreListOptions struct {
	CollectionID *string
	// CategoryID and TagID are the two filters a storefront can actually call:
	// the ids come from the vocabulary endpoints, which is what those exist for.
	CategoryID *string
	TagID      *string
	Search     *string
	// SalesChannelIDs are the sales channels the request is bound to.
	//
	// The value comes from the request's IDENTITY (the channels of the
	// publishable key), NOT from the query string; for the rationale see
	// api/store.go.
	//
	// nil and an EMPTY BUT NON-nil slice say DIFFERENT things:
	//
	//   - nil: the request carries no sales channel id at all (store
	//     authentication is not wired up in this setup). The filter is NOT
	//     APPLIED.
	//   - empty slice: there is an identity but it has no channels. The filter
	//     IS APPLIED and only products with no assignment are visible.
	//
	// The second case does not occur in practice — auth rejects a publishable
	// key with no channels anyway — but the defensive behavior is this: treating
	// an identity with no channels as "no filtering" would open the catalog of
	// ALL channels to that identity. Applying the empty set to the rule itself
	// (no assignment matches, the unassigned ones remain) opens no separate code
	// path and never errs in the direction of leaking.
	SalesChannelIDs []string
	Limit           int
	Offset          int
	// After is the opaque position from a previous page's NextCursor; the zero
	// value is the first page. See [ListProductsOptions.After].
	After corepage.Cursor
	// SkipCount, when true, means the total count query is never run at all and
	// the Count field of the result comes back nil.
	//
	// Its meaning, its rationale and its measurement are in
	// [ListProductsOptions.SkipCount]; they are NOT REPEATED here. The reason
	// the field is present in the storefront criteria as well is this: the side
	// that MAKES the decision is the client (the "with_count" parameter in REST,
	// selecting or not selecting the "count" field in GraphQL) and the storefront
	// service itself cannot pick a default — had it picked one, there would be a
	// second definition of the same rule.
	SkipCount bool
}

// StoreProduct is a product prepared for the storefront.
type StoreProduct struct {
	models.Product
	// Variants SHADOWS the Variants field of the embedded product: only this
	// field shows up in JSON and the variants are enriched with price/stock
	// information.
	Variants []StoreVariant `json:"variants"`
}

// StoreVariant is a variant enriched with price and stock information.
//
// The PriceSet and InventoryItem fields are the records of OTHER MODULES and
// this module does not know their schema: they are carried exactly as they came
// from the Query layer (as loosely typed records). Not regaining type safety
// here is deliberate; interpreting the fields would mean copying the
// pricing/inventory schema into this module (the accepted price of ADR 0004).
type StoreVariant struct {
	models.Variant
	PriceSet      query.Record `json:"price_set,omitempty"`
	InventoryItem query.Record `json:"inventory_item,omitempty"`
}

// enrichment holds the additions a single variant gets from other modules.
type enrichment struct {
	priceSet  query.Record
	inventory query.Record
}

// ListStoreProducts lists the published products with PRICE and STOCK
// information.
//
// The price is pricing's and the stock is inventory's data; neither module is
// IMPORTED. The data is resolved through links over the variant ids and
// gathered with the batch provider calls of the Query layer (ADR 0004).
//
// The number of queries is INDEPENDENT of the number of products or variants: a
// fixed number of queries is made for the catalog, and one link resolution plus
// one provider call per expansion for the enrichment. There is no N+1.
//
// # The sales channel filter
//
// The rule is this: a product with NO channel assignment is visible in ALL
// channels, a product that HAS one is visible ONLY in the channels it is
// assigned to. It is backwards compatible (today's catalog does not empty out
// overnight) but the filtering really works: the moment a product is assigned to
// channel A it becomes invisible in channel B.
//
// The strict alternative — "an unassigned product is hidden" — was deliberately
// NOT IMPLEMENTED. It is a forward-looking decision and the day it is applied it
// empties every existing catalog in one go; if it is to be chosen, a migration
// (assigning all products to a default channel) has to come first.
//
// The filter is applied IN THE DATABASE through [repository.Store]; for why we
// cannot do the paging on the Go side see repository/saleschannel.go.
//
// # The total count is optional
//
// The same filter binds the count too, and that count walks the WHOLE set
// regardless of the page size: when measured it is 99% of the SQL of a
// storefront request (see [ListProductsOptions.SkipCount]). It can be turned off
// with [StoreListOptions.SkipCount]; while it is off the Count field of the
// result comes back nil and that means "not counted", NOT "zero records".
func (s *Service) ListStoreProducts(ctx context.Context, opts StoreListOptions) (ListResult[StoreProduct], error) {
	published := models.StatusPublished
	result, err := s.ListProducts(ctx, ListProductsOptions{
		Status:          &published,
		CollectionID:    opts.CollectionID,
		CategoryID:      opts.CategoryID,
		TagID:           opts.TagID,
		Search:          opts.Search,
		SalesChannelIDs: opts.SalesChannelIDs,
		Limit:           opts.Limit,
		Offset:          opts.Offset,
		After:           opts.After,
		WithRelations:   true,
		SkipCount:       opts.SkipCount,
	})
	if err != nil {
		return ListResult[StoreProduct]{}, err
	}

	items, err := s.toStoreProducts(ctx, result.Items)
	if err != nil {
		return ListResult[StoreProduct]{}, err
	}
	return ListResult[StoreProduct]{
		Items:      items,
		Count:      result.Count,
		Offset:     result.Offset,
		Limit:      result.Limit,
		NextCursor: result.NextCursor,
	}, nil
}

// GetStoreProduct returns a single product for the storefront with price and
// stock information.
//
// Either an id or a handle is accepted: storefront addresses carry the handle,
// internal calls the id. A product that is not published returns NOT FOUND —
// giving away the existence of a draft product with an error like "unauthorized"
// is a leak as well.
//
// salesChannelIDs carries the SAME meaning as in the listing (see
// [StoreListOptions.SalesChannelIDs]) and the single-record endpoint is subject
// to the SAME filter: showing a product that is hidden in the list through the
// single endpoint would make the hiding entirely pointless — because storefront
// addresses carry the handle, this is exactly the endpoint that is guessable.
//
// An invisible product returns the SAME error (NotFound) as an unpublished one:
// giving away the existence of a product sold in another channel with a
// different error kind would pierce the hiding itself.
func (s *Service) GetStoreProduct(
	ctx context.Context,
	idOrHandle string,
	salesChannelIDs []string,
) (StoreProduct, error) {
	if _, err := requireID("id", idOrHandle); err != nil {
		return StoreProduct{}, err
	}

	var (
		product models.Product
		err     error
	)
	if strings.HasPrefix(idOrHandle, prefixProduct) {
		product, err = s.GetProduct(ctx, idOrHandle)
	} else {
		product, err = s.GetProductByHandle(ctx, idOrHandle)
	}
	if err != nil {
		return StoreProduct{}, err
	}
	if product.Status != models.StatusPublished {
		return StoreProduct{}, errors.NotFound(codeNotFound, "the product was not found: %s", idOrHandle)
	}

	// nil means "the request carries no channel id"; in that case the query
	// would return true anyway, so the round trip is not made for nothing.
	if salesChannelIDs != nil {
		visible, err := s.repo.ProductVisibleInSalesChannels(ctx, product.ID, salesChannelIDs)
		if err != nil {
			return StoreProduct{}, err
		}
		if !visible {
			return StoreProduct{}, errors.NotFound(codeNotFound, "the product was not found: %s", idOrHandle)
		}
	}

	items, err := s.toStoreProducts(ctx, []models.Product{product})
	if err != nil {
		return StoreProduct{}, err
	}
	return items[0], nil
}

// StoreProductsByIDs returns the storefront products BY ID, IN THE REQUESTED
// ORDER.
//
// It is meant for external consumers such as search: they supply the relevance
// order from outside (the "product.interop" surface looks at this method, see
// interop.go).
//
// # The visibility rule is the SAME as the list's
//
// The rule is NOT REWRITTEN here; a visibility rule expressed in two places
// means that when one of them changes the storefront and search drift apart and
// search becomes a BYPASS of the channel filtering. Therefore:
//
//   - The publication status is filtered the same way as in the single
//     storefront endpoint (only "published"; see [Service.GetStoreProduct]).
//   - Channel visibility is asked with the repository call the single endpoint
//     uses (ProductVisibleInSalesChannels) — that is, with the SAME template as
//     the SQL of the listing (see repository/saleschannel.go).
//
// The difference between nil and an empty slice for salesChannelIDs carries the
// same meaning as in the listing (see [StoreListOptions.SalesChannelIDs]).
//
// Visibility is a SINGLE batch query ([repository.Store.VisibleProductIDs]), the
// number of ids is bounded by [MaxLimit] and the query uses the primary key
// prefix of the link table. The query is generated from salesChannelVisibleTemplate
// in saleschannel.go, the rule's ONLY definition — that is, being batched does
// not write the rule a second time.
//
// # Order and ids that are not found
//
// The response preserves the id order of the request. An id that is unknown,
// deleted, unpublished or not visible in the request's channels is SILENTLY
// skipped — all of them are valid answers to the caller's question "do you have
// this id", and returning an error would mean search falling over entirely
// because one product was deleted. It gives away no information in the leaking
// direction either: a product in another channel and a product that never
// existed are INDISTINGUISHABLE to the caller (the same rationale as the single
// storefront endpoint returning NotFound for both).
//
// A repeated id appears ONCE in the response; it keeps the position of its first
// occurrence.
func (s *Service) StoreProductsByIDs(ctx context.Context, ids, salesChannelIDs []string) ([]StoreProduct, error) {
	wanted, err := uniqueIDs("ids", ids)
	if err != nil {
		return nil, err
	}
	if len(wanted) == 0 {
		return []StoreProduct{}, nil
	}
	// A request above the limit is NOT TRUNCATED, it is rejected: silent
	// truncation silently shortens the search result and the caller can never
	// see it. An explicit error forces it to paginate.
	if len(wanted) > MaxLimit {
		return nil, invalid("ids can carry at most %d ids (given: %d)", MaxLimit, len(wanted))
	}

	found, err := s.repo.ListProductsByIDs(ctx, wanted)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]models.Product, len(found))
	for i := range found {
		byID[found[i].ID] = found[i]
	}

	// Visibility is asked in a SINGLE query. Asking per id means as many round
	// trips as there are search results, and in an architecture that structurally
	// keeps N+1 out (see core/query) it would bring it back at the hottest
	// endpoint.
	//
	// nil means "the request carries no channel id"; the filter is not applied
	// and the query is not made at all.
	var visibleIDs map[string]struct{}
	if salesChannelIDs != nil {
		visibleIDs, err = s.repo.VisibleProductIDs(ctx, wanted, salesChannelIDs)
		if err != nil {
			return nil, err
		}
	}

	visible := make([]models.Product, 0, len(wanted))
	for _, id := range wanted {
		product, ok := byID[id]
		if !ok || product.Status != models.StatusPublished {
			continue
		}
		if visibleIDs != nil {
			if _, ok := visibleIDs[id]; !ok {
				continue
			}
		}
		visible = append(visible, product)
	}
	if len(visible) == 0 {
		return []StoreProduct{}, nil
	}

	if err := s.attachRelations(ctx, visible); err != nil {
		return nil, err
	}
	return s.toStoreProducts(ctx, visible)
}

// toStoreProducts converts the products into the storefront shape and enriches
// the variants.
func (s *Service) toStoreProducts(ctx context.Context, products []models.Product) ([]StoreProduct, error) {
	variantIDs := make([]string, 0, len(products))
	for i := range products {
		variants := products[i].Variants
		for j := range variants {
			variantIDs = append(variantIDs, variants[j].ID)
		}
	}

	extras, err := s.enrichVariants(ctx, variantIDs)
	if err != nil {
		return nil, err
	}

	out := make([]StoreProduct, 0, len(products))
	for i := range products {
		p := products[i]
		variants := make([]StoreVariant, 0, len(p.Variants))
		for j := range p.Variants {
			variant := p.Variants[j]
			extra := extras[variant.ID]
			variants = append(variants, StoreVariant{
				Variant:       variant,
				PriceSet:      extra.priceSet,
				InventoryItem: extra.inventory,
			})
		}
		// The variant slice of the embedded product is emptied: carrying the
		// same data in two places leaves the door open for one of them to be
		// updated and the other forgotten.
		p.Variants = nil
		out = append(out, StoreProduct{Product: p, Variants: variants})
	}
	return out, nil
}

// enrichVariants gathers the price and stock records of the variants with a
// SINGLE graph call.
//
// The Query layer resolves it like this: the roots from the variant provider
// (one query), one link resolution per expansion and a single FetchByIDs to the
// provider of the target module. That is, whatever the number of variants, the
// number of round trips is constant.
//
// # Behavior against a missing module
//
// If pricing or inventory is not registered in this setup, Query returns
// "provider not found" (codeProviderNotFound). ONLY in that case does the
// listing NOT FAIL: the catalog comes back without prices/stock and the
// situation is logged as a warning. The rationale is modularity itself — the
// product module has to be deployable on its own; besides, a missing price is
// better than showing a wrong one (the field is not written at all).
//
// The fallback is narrowed by the CODE, not by the error KIND. Looking at the
// kind (KindNotFound) was too broad: a NotFound produced by a registered
// provider inside itself (query_provider_failed) passes through that gate too,
// and a genuine fault would turn into a storefront page that returns 200 without
// prices — the DoD of Phase 4 would be violated without leaving any trace beyond
// a single log line.
func (s *Service) enrichVariants(ctx context.Context, variantIDs []string) (map[string]enrichment, error) {
	out := make(map[string]enrichment, len(variantIDs))
	if len(variantIDs) == 0 {
		return out, nil
	}
	if s.graph == nil {
		s.log.DebugContext(ctx, "the query layer is not registered; the storefront returns without price/stock")
		return out, nil
	}

	records, err := s.graph.Graph(ctx, query.GraphSpec{
		Entity:  EntityVariant,
		Fields:  []string{filterID},
		Filters: map[string]any{filterIDs: variantIDs},
		Limit:   len(variantIDs),
		Expand: []query.Expansion{
			{Link: LinkVariantPriceSet, As: keyPriceSet},
			{Link: LinkVariantInventory, As: keyInventory},
		},
	})
	if err != nil {
		if errors.CodeOf(err) == codeProviderNotFound {
			s.log.WarnContext(ctx, "the price/stock provider is not registered; the storefront returns without that information",
				"error", err)
			return out, nil
		}
		return nil, errors.Wrap(err, errors.KindOf(err), codeQueryFailed,
			"the price/stock information of the variants could not be read (%d variants)", len(variantIDs))
	}

	for _, rec := range records {
		id, ok := rec[filterID].(string)
		if !ok || id == "" {
			continue
		}
		out[id] = enrichment{
			priceSet:  asRecord(rec[keyPriceSet]),
			inventory: asRecord(rec[keyInventory]),
		}
	}
	return out, nil
}

// asRecord converts an expansion result into a record; if there is no match it
// returns nil.
//
// Both types are accepted: the core writes a query.Record, but a provider or a
// fake implementation may return a plain map[string]any and the type assertion
// would fail silently in that case and swallow the price.
func asRecord(v any) query.Record {
	switch t := v.(type) {
	case query.Record:
		return t
	case map[string]any:
		return t
	default:
		return nil
	}
}
