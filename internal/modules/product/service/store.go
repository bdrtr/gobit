package service

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"

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

	// filterCategoryTreeID is a category with every category below it, the
	// storefront's word since ADR 0261, and the read layer's since ADR 0282.
	filterCategoryTreeID = "category_tree_id"

	// filterSearch is the product provider's free-text search over the title,
	// and it carries the storefront's spelling for the same reason its two
	// neighbors do: "q".
	//
	// Both storefront surfaces already read that exact word — REST off the
	// query string (api/store.go, stringParam(r, "q")) and GraphQL as the
	// argument of the same name (graph/resolver.go) — and both hand it to
	// [StoreListOptions].Search, which becomes [repository.ProductFilter].Search
	// and finally the ILIKE of the shared filter body. A single letter is not a
	// self-explanatory name and "search" or "title" would read better here; what
	// they would cost is a THIRD name for one concept, so that the panel and the
	// shop — one installation, one operator, one URL that gets copied from one
	// to the other — would need a translation table that nothing verifies. The
	// name in use wins over the better name.
	filterSearch = "q"

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
	// keyInventoryStock is where the per-warehouse breakdown lands, and it is a
	// key of its OWN so that the published record never carries it.
	keyInventoryStock = "inventory_stock"
	// keyInventoryRestock is where the restock forecast lands, on the one more
	// graph call only the published variants with nothing to sell make (ADR
	// 0399).
	keyInventoryRestock = "inventory_restock"
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
	// Order is the listing order; the zero value is newest first.
	Order        models.ProductOrder
	CollectionID *string
	// CategoryID and TagID are the two filters a storefront can actually call:
	// the ids come from the vocabulary endpoints, which is what those exist for.
	CategoryID *string
	TagID      *string
	// CategoryTreeID narrows the catalog to a category's products and its
	// subcategories' (ADR 0261); see [ListProductsOptions.CategoryTreeID].
	CategoryTreeID *string
	// OptionValue narrows the catalog to the products offering one option
	// value ("red"), matched on the FOLDED form (ADR 0039). The value comes
	// from the option vocabulary endpoint, which is the third of the four
	// vocabularies and the only one that returns text.
	//
	// It is a value without an option title beside it, and that is a narrower
	// promise than it looks. The vocabulary hands back (title, value) PAIRS, so
	// a client can show "Color: red" and "Size: red" apart; this filter answers
	// "offers the value red on ANY axis". Adding the title would need a second
	// decision -- whether two axes named differently are one axis is a
	// merchant's data question rather than a rule this framework can pick --
	// and ADR 0039 folds the VALUE and nothing else.
	OptionValue *string
	Search      *string
	// VariantIDs narrows the catalog to the products that own one of these
	// variants, so a list of variants kept elsewhere -- a customer's wishlist
	// (ADR 0190) -- is shown in one read (ADR 0191). nil applies no filter.
	//
	// A product comes back whole, with all its variants, and once however
	// many of its variants were named. A variant that was deleted, or whose
	// product is not published or not in the channel, names nothing, and its
	// product is left out as any other product would be. At most [MaxLimit]
	// ids are taken, so one page holds every product they name.
	VariantIDs []string
	// Attributes keep the products whose value of each named attribute
	// matches (ADR 0219); see [AttributeCriterion].
	Attributes []AttributeCriterion
	// InStock narrows the catalog by ADR 0040's definition: true keeps the
	// products with at least one sellable variant, false keeps the ones with
	// none.
	//
	// Both directions are offered because both are a shopper's question ("hide
	// what I cannot buy" and, for a merchant's own tooling, "what is dark").
	// nil is neither and applies no filter at all.
	//
	// It cannot be a SQL clause: two of its three inputs are this module's
	// columns and the third is inventory's number, which arrives with the
	// enrichment. What that costs the page is in [Service.ListStoreProducts].
	InStock *bool
	// Price narrows the catalog to the products with a BASE price inside a
	// bracket, in the request's currency, at quantity one (ADR 0041).
	//
	// It rides exactly the same machinery as InStock and for the same reason:
	// the amounts belong to pricing and its Query provider takes one filter,
	// "id", so there is no predicate to push down.
	Price *PriceBracket
	// SalesChannelIDs are the sales channels the read is scoped to.
	//
	// It is NEVER read from the query string, and the two read surfaces fill it
	// from two different places:
	//
	//   - The REST catalog reads take the ONE channel their path names,
	//     narrowed by the channels the request's publishable key holds, so this
	//     slice arrives with exactly one element. A path naming a channel the
	//     key does not hold never reaches the service at all — it is refused in
	//     the handler (ADR 0044, see api.storeChannelScope).
	//   - The GraphQL resolvers take the whole set from the identity, because
	//     that transport did not move and has no path to take a channel from.
	//
	// The service does not need to know which of the two it was handed: the
	// filter is a set either way, and putting the difference here would be a
	// second definition of a rule that belongs at the edge.
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
	//
	// Neither case can arrive from the REST catalog reads any more: those always
	// send one channel, and the channelless identity is refused before the call.
	// Both stay in the contract because GraphQL still produces them and because a
	// caller inside this module may hand over either.
	SalesChannelIDs []string
	// RegionID is the shopper's region, which the storefront knows and the
	// checkout ranks warehouses by (ADR 0422). With it, the stock badge, the
	// in-stock filter and the restock date count only the warehouses the
	// checkout would rank for the region, among the ones the channels ship
	// from; empty counts as before. It is a narrowing the client chooses, not a
	// gate: an unknown id counts the warehouses bound to no region, which is
	// what the checkout ranks for it.
	RegionID string
	Limit    int
	Offset   int
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
	// InStock is ADR 0040's answer for the product: true when AT LEAST ONE of
	// its variants is in stock, false when none is and false when it has no
	// variants at all.
	//
	// It is the BADGE, and it is written on every storefront body rather than
	// behind a parameter. A storefront that renders a product card needs it
	// whether or not it also filters by it, and the inputs are already fetched
	// -- the answer is arithmetic over records this endpoint reads anyway, so
	// making it optional would buy nothing and would leave two callers to
	// re-derive one definition.
	//
	// A product read through an endpoint that does NOT enrich (there is none
	// today; every path into this type goes through [Service.enrichStoreProducts])
	// would carry false here, which is the same direction the definition errs
	// in everywhere else: never claiming stock it cannot see.
	InStock bool `json:"in_stock"`
}

// StoreVariant is a variant enriched with price and stock information.
//
// The PriceSet and InventoryItem fields are the records of OTHER MODULES and
// this module does not know their schema: they are carried exactly as they came
// from the Query layer (as loosely typed records). Not regaining type safety
// here is deliberate; interpreting the fields would mean copying the
// pricing/inventory schema into this module (the accepted price of ADR 0004).
// The exceptions to that sentence are [StoreVariant.InStock], which ADR 0040
// makes deliberately by reading a single published field name out of
// inventory's record, and [StoreVariant.RestockExpectedAt], which ADR 0399 makes
// by reading the same field and the restock forecast. What that costs, and why
// nothing may quietly add another, is written where the field names are -- see
// catalogfilter.go.
type StoreVariant struct {
	models.Variant
	PriceSet      query.Record `json:"price_set,omitempty"`
	InventoryItem query.Record `json:"inventory_item,omitempty"`
	// InStock is ADR 0040's answer for this variant: not counted, OR sellable
	// past zero, OR carrying a positive available quantity.
	//
	// It is computed rather than stored. A stored copy is stale the moment
	// stock moves, and keeping it fresh would mean the catalog subscribing to
	// inventory's events for a value it can work out on read.
	InStock bool `json:"in_stock"`
	// RestockExpectedAt is, for a counted variant with nothing to sell at the
	// request's warehouses, the first moment inventory expects units on sale
	// there again, after the backordered lines waiting for them take theirs
	// (ADR 0399); absent when no supplier receipt is expected to leave any.
	//
	// It is an estimate, not a promise: the checkout does not read it and no
	// order line carries it. A bundle carries none.
	RestockExpectedAt *time.Time `json:"restock_expected_at,omitempty"`
}

// locationsServingChannels is the set of warehouses the request's sales
// channels ship from (ADR 0092), nil when they narrow nothing. A read naming
// the shopper's region narrows it once more, to the warehouses the checkout
// ranks for the region ([Service.narrowToRegion], ADR 0422).
//
// # Why a failure does NOT fail the read
//
// The narrowing makes a badge more honest; it is not what keeps the shop
// correct. That is the checkout, which resolves the same binding and REFUSES an
// order it cannot serve from the channel's warehouses. So a link service that
// cannot be reached degrades the badge to the unnarrowed total — with a line in
// the log — instead of taking the catalog down for a display concern.
//
// The reverse choice is the checkout's, and the two are deliberately different:
// there the same failure fails the order, because there the answer decides
// where goods come from.
func (s *Service) locationsServingChannels(
	ctx context.Context, salesChannelIDs []string,
) map[string]bool {
	if len(salesChannelIDs) == 0 || s.links == nil {
		return nil
	}

	bound, err := s.links.ListManyByTo(ctx, LinkStockLocationSalesChannel, salesChannelIDs)
	if err != nil {
		s.log.ErrorContext(ctx,
			"the warehouses serving the request's sales channels could not be read; the "+
				"stock badge counts EVERY warehouse for this response",
			"sales_channel_ids", salesChannelIDs, "error", err)

		return nil
	}

	served := map[string]bool{}
	for _, locationIDs := range bound {
		for _, locationID := range locationIDs {
			served[locationID] = true
		}
	}
	// A channel bound to no warehouse is one nobody has configured, not one
	// that ships from nowhere: it narrows nothing, as the checkout reads it.
	// nil is that answer; a non-nil set counts its members and nothing else
	// ([unitsAvailable]), so an empty one would count nothing at all.
	if len(served) == 0 {
		return nil
	}

	return served
}

// enrichment holds the additions a single variant gets from other modules.
type enrichment struct {
	priceSet  query.Record
	inventory query.Record
	// sellableByLocation is the stock of this variant broken down by warehouse,
	// and it is NOT part of [StoreVariant]: it is read to decide the badge and
	// goes no further. It is nil unless the read is narrowed to a sales
	// channel's warehouses (ADR 0092).
	sellableByLocation map[string]int64
}

// sellableByLocation reads the breakdown out of its own expansion record.
//
// A missing record and a record without the field both answer nil, and the
// badge reads nil as "nothing sellable here" — see [sellableAt] for why that
// direction is the safe one.
func sellableByLocation(record query.Record) map[string]int64 {
	if record == nil {
		return nil
	}
	byLocation, ok := record[foreignAvailableByLocation].(map[string]int64)
	if !ok {
		return nil
	}

	return byLocation
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
//
// # Two of the filters cannot be a WHERE clause, and the page is scanned for them
//
// [StoreListOptions.InStock] and [StoreListOptions.Price] are answered over data
// that belongs to inventory and to pricing, and neither can enter this module's
// SQL: Principle 2.2 forbids the join, and neither provider takes a predicate
// this module could push down (inventory's takes "sku" and
// "requires_shipping", pricing's takes "id"). So the answer exists only AFTER a
// product has been enriched, and enrichment happens after LIMIT has cut the
// page.
//
// Filtering the cut page is what this module refuses to do, and it refuses it in
// writing twice already: repository/saleschannel.go put the sales channel filter
// into the database because "a filtering done on the Go side fills the page
// short, and the total count would show the unfiltered set", and
// [variantProvider.List] rejects a combination rather than "opening a surface
// that paginates wrongly". A short page is not merely ugly -- with offset paging
// an EMPTY page in the middle of the catalog is indistinguishable from the end
// of it, and a client that stops there loses every product beyond.
//
// So when either filter is given the listing SCANS instead: it walks the catalog
// in the listing order in chunks, enriches each chunk with the one batch Graph
// call it would have made anyway, keeps what matches, and stops at a full page.
// See [Service.scanStoreProducts] for what that costs and what it refuses.
func (s *Service) ListStoreProducts(ctx context.Context, opts StoreListOptions) (ListResult[StoreProduct], error) {
	// The bracket is normalized and judged HERE rather than at either edge, so
	// that the REST query string and the GraphQL input object compare the same
	// amounts and refuse the same requests; see [PriceBracket.Validate] and
	// [PriceBracket.normalized].
	if opts.Price != nil {
		bracket := opts.Price.normalized()
		if err := bracket.Validate(); err != nil {
			return ListResult[StoreProduct]{}, err
		}

		opts.Price = &bracket
	}

	if keep := opts.enrichedFilter(); keep != nil {
		return s.scanStoreProducts(ctx, opts, keep)
	}

	published := models.StatusPublished
	result, err := s.ListProducts(ctx, ListProductsOptions{
		Order:           opts.Order,
		Status:          &published,
		CollectionID:    opts.CollectionID,
		CategoryID:      opts.CategoryID,
		CategoryTreeID:  opts.CategoryTreeID,
		TagID:           opts.TagID,
		OptionValue:     opts.OptionValue,
		VariantIDs:      opts.VariantIDs,
		Attributes:      opts.Attributes,
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

	items, err := s.toStoreProducts(ctx, result.Items, opts.SalesChannelIDs, opts.RegionID)
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

// enrichedFilter is a criterion that can only be answered once a product has
// been enriched with the other modules' records.
type enrichedFilter func(StoreProduct) bool

// enrichedFilter gathers the criteria of a request that the database cannot
// answer, and returns nil when there are none.
//
// Returning nil rather than a function that always says yes is what keeps the
// unfiltered listing on the path it has always taken: one list query, one count
// and one enrichment Graph call, with the same bytes in the response as before
// this existed -- plus the one restock call ADR 0399 adds when a variant on the
// page has nothing to sell.
//
// The criteria are ANDed, and the aggregation to the product is an OR over its
// variants for both of them. That is the same any-variant rule ADR 0040 and ADR
// 0041 each state, and stating it once here would still leave two decisions
// saying it -- so a product with a cheap sold-out variant and an expensive
// available one matches "in stock" and matches "under 100" and is therefore
// returned by a request asking for both, even though no single variant satisfies
// the pair. Narrowing that to one variant is a THIRD decision and neither record
// takes it.
func (o StoreListOptions) enrichedFilter() enrichedFilter {
	var tests []enrichedFilter

	if o.InStock != nil {
		wanted := *o.InStock
		tests = append(tests, func(p StoreProduct) bool { return p.InStock == wanted })
	}

	if o.Price != nil {
		bracket := *o.Price
		tests = append(tests, func(p StoreProduct) bool {
			for i := range p.Variants {
				if bracket.matchesVariant(p.Variants[i].PriceSet) {
					return true
				}
			}

			return false
		})
	}

	if len(tests) == 0 {
		return nil
	}

	return func(p StoreProduct) bool {
		for _, test := range tests {
			if !test(p) {
				return false
			}
		}

		return true
	}
}

// The two numbers that bound the scan.
const (
	// storeScanChunk is how many products one round of the scan reads.
	//
	// It is deliberately far larger than a page. The cost of a round is almost
	// entirely round trips -- the listing query plus its bulk relation reads,
	// and ONE batch enrichment Graph call for the whole chunk regardless of its
	// size --
	// so a chunk of a hundred costs about what a chunk of twenty costs and
	// buys five times the ground. It is [MaxLimit] because the listing will
	// not fetch more than that in one call anyway.
	storeScanChunk = MaxLimit
	// storeScanMaxRows is the number of catalog rows one request will walk
	// before it stops and hands back a SHORT page with a cursor.
	//
	// There has to be a bound: "in stock" can be false for every product in a
	// catalog, and without one, a single request would enrich the whole of it
	// looking for a twentieth match. Five chunks is the ceiling, so the worst
	// request makes five list rounds and five enrichment Graph calls rather
	// than one, plus the one restock call for the page it returns (ADR 0399).
	//
	// Stopping is SAFE in a way that stopping in the middle of a WHERE clause
	// would not be: the cursor handed back names the last row examined, so the
	// client's next request resumes exactly there and no product is skipped or
	// returned twice. A short page with a cursor is "keep going", and the
	// listing's contract already says the catalog is exhausted when
	// next_cursor is ABSENT -- not when a page comes back short.
	storeScanMaxRows = 5 * storeScanChunk
)

// scanStoreProducts assembles a page for a request carrying a filter the
// database cannot answer.
//
// # What it guarantees
//
// A full page whenever the catalog holds one within the scan budget; no product
// skipped and none returned twice across pages; and a next_cursor that is empty
// only when the SCAN REACHED THE END of the catalog. The client's stop condition
// is unchanged.
//
// # What it refuses, and why an offset is not one of the things it can honor
//
// A non-zero offset. An offset counts rows of the set the DATABASE returns, and
// the set the client sees here is a subset of that chosen after the fact -- so
// "skip 40" would skip forty catalog rows rather than forty matches, and the
// second page would silently begin somewhere the first page had already shown.
// The refusal is a typed validation error naming the cursor, because the cursor
// is the parameter that does work here: it is a POSITION rather than a count,
// and a position survives a filter that removes rows.
//
// # What it costs
//
// Up to [storeScanMaxRows] products read and enriched for one page, against
// exactly one page's worth on the unfiltered path. The count is NOT produced --
// counting the matches means enriching the whole catalog, which is the one thing
// the budget exists to prevent -- so the result's Count comes back nil and the
// envelope carries no "count" field. That is the same "not counted" the
// with_count parameter already produces, and the handler refuses an explicit
// with_count=true beside these filters rather than letting the number go missing
// without a word.
//
// The restock dates (ADR 0399) are asked once, for the page returned, after the
// filter: a product the scan read and dropped is never dated.
func (s *Service) scanStoreProducts(
	ctx context.Context,
	opts StoreListOptions,
	keep enrichedFilter,
) (ListResult[StoreProduct], error) {
	limit, offset, err := normalizePaging(opts.Limit, opts.Offset)
	if err != nil {
		return ListResult[StoreProduct]{}, err
	}
	if offset > 0 {
		return ListResult[StoreProduct]{}, invalid(
			"the offset cannot be combined with the in_stock or price filters; " +
				"page with the cursor from next_cursor instead")
	}

	order, err := normalizeProductOrder(opts.Order)
	if err != nil {
		return ListResult[StoreProduct]{}, err
	}
	listing := ProductListingFor(order)

	published := models.StatusPublished
	chunk := max(limit, storeScanChunk)
	after := opts.After
	out := make([]StoreProduct, 0, limit)
	scanned := 0
	// What the chunks read, kept so the page is dated once, after the filter:
	// a product the filter drops is never asked for a date.
	read := storeRead{extras: map[string]enrichment{}}
	finish := func(next string) ListResult[StoreProduct] {
		s.restockDates(ctx, out, read)

		return storeScanResult(out, limit, next)
	}

	for {
		page, err := s.ListProducts(ctx, ListProductsOptions{
			Order:           order,
			Status:          &published,
			CollectionID:    opts.CollectionID,
			CategoryID:      opts.CategoryID,
			CategoryTreeID:  opts.CategoryTreeID,
			TagID:           opts.TagID,
			OptionValue:     opts.OptionValue,
			VariantIDs:      opts.VariantIDs,
			Attributes:      opts.Attributes,
			Search:          opts.Search,
			SalesChannelIDs: opts.SalesChannelIDs,
			Limit:           chunk,
			After:           after,
			WithRelations:   true,
			// The count of the unfiltered set is not merely useless here, it
			// is WRONG: it would report how many products the WHERE clause
			// keeps, under a filter that removes more of them.
			SkipCount: true,
		})
		if err != nil {
			return ListResult[StoreProduct]{}, err
		}

		enriched, chunkRead, err := s.enrichStoreProducts(ctx, page.Items, opts.SalesChannelIDs, opts.RegionID)
		if err != nil {
			return ListResult[StoreProduct]{}, err
		}
		read.merge(chunkRead)

		for i := range enriched {
			scanned++
			if !keep(enriched[i]) {
				continue
			}

			out = append(out, enriched[i])
			if len(out) < limit {
				continue
			}

			// The page is full. The next one resumes AFTER this row rather
			// than after the chunk, so the matches this chunk still holds are
			// not lost.
			next := ""
			if i < len(enriched)-1 || page.NextCursor != "" {
				next = corepage.Encode(listing,
					corepage.Cursor{Time: enriched[i].CreatedAt, ID: enriched[i].ID})
			}

			return finish(next), nil
		}

		// An unfilled chunk means the listing itself is exhausted: there is no
		// row left to resume from and the catalog has been walked to its end.
		if page.NextCursor == "" {
			return finish(""), nil
		}

		last := page.Items[len(page.Items)-1]
		after = corepage.Cursor{Time: last.CreatedAt, ID: last.ID}

		if scanned >= storeScanMaxRows {
			return finish(corepage.Encode(listing, after)), nil
		}
	}
}

// storeScanResult wraps what the scan gathered.
//
// Offset is written as zero rather than echoed, which is what the scan accepts
// in the first place, and Count is left nil: see [Service.scanStoreProducts].
func storeScanResult(items []StoreProduct, limit int, nextCursor string) ListResult[StoreProduct] {
	return ListResult[StoreProduct]{
		Items:      items,
		Offset:     0,
		Limit:      limit,
		NextCursor: nextCursor,
	}
}

// GetStoreProduct returns a single product for the storefront with price and
// stock information.
//
// Either an id or a handle is accepted: storefront addresses carry the handle,
// internal calls the id. A product that is not published returns NOT FOUND —
// giving away the existence of a draft product with an error like "unauthorized"
// is a leak as well.
//
// regionID narrows the badge as [StoreListOptions.RegionID] does.
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
	regionID string,
) (StoreProduct, error) {
	product, err := s.visibleStoreProduct(ctx, idOrHandle, salesChannelIDs)
	if err != nil {
		return StoreProduct{}, err
	}

	items, err := s.toStoreProducts(ctx, []models.Product{product}, salesChannelIDs, regionID)
	if err != nil {
		return StoreProduct{}, err
	}
	return items[0], nil
}

// visibleStoreProduct resolves a product by id or handle and answers NotFound
// unless the storefront may show it: published, and visible in the request's
// channels.
//
// It is the single endpoint's rule, taken out so the related-products endpoint
// asks the same question about the product it starts from (ADR 0180) rather than
// a second copy of it.
func (s *Service) visibleStoreProduct(
	ctx context.Context, idOrHandle string, salesChannelIDs []string,
) (models.Product, error) {
	if _, err := requireID("id", idOrHandle); err != nil {
		return models.Product{}, err
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
		return models.Product{}, err
	}
	if product.Status != models.StatusPublished {
		return models.Product{}, errors.NotFound(codeNotFound, "the product was not found: %s", idOrHandle)
	}

	// nil means "the request carries no channel id"; in that case the query
	// would return true anyway, so the round trip is not made for nothing.
	if salesChannelIDs != nil {
		visible, err := s.repo.ProductVisibleInSalesChannels(ctx, product.ID, salesChannelIDs)
		if err != nil {
			return models.Product{}, err
		}
		if !visible {
			return models.Product{}, errors.NotFound(codeNotFound, "the product was not found: %s", idOrHandle)
		}
	}

	return product, nil
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
	return s.storeProductsByIDs(ctx, ids, salesChannelIDs, true)
}

// storeProductsByIDs is [Service.StoreProductsByIDs], with the restock dates
// only when dated: the badge read publishes none of them.
func (s *Service) storeProductsByIDs(
	ctx context.Context, ids, salesChannelIDs []string, dated bool,
) ([]StoreProduct, error) {
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
	// The by-id reads name no region: search, related products, add-ons and
	// the badge read count the channel's warehouses (ADR 0422).
	if dated {
		return s.toStoreProducts(ctx, visible, salesChannelIDs, "")
	}
	items, _, err := s.enrichStoreProducts(ctx, visible, salesChannelIDs, "")

	return items, err
}

// storeRead is what one enrichment read beside the products it returned: the
// other modules' records of each variant and the warehouses the read counted.
// The restock dates are decided from it ([Service.restockDates]).
type storeRead struct {
	extras map[string]enrichment
	// served is the set the badge counted: nil counts every warehouse, any
	// other set its members alone ([unitsAvailable]).
	served map[string]bool
	// narrowed says the read was narrowed to the shopper's region (ADR 0422):
	// it named one AND fulfillment judged it. A read that named a region the
	// ranking could not judge is not narrowed, and counts the channel's answer
	// for its badge and its restock dates alike.
	narrowed bool
	// region, channel and judged are, for a narrowed read, the region, the set
	// the narrowing started from and what it decided, so a restock date at a
	// warehouse the breakdown did not name is judged by the same rule
	// ([Service.restockCounted]).
	region  string
	channel map[string]bool
	judged  map[string]bool
}

// merge takes a later chunk's read into the scan's: the extras accumulate, and
// so do the counted sets and the region's judgments of the chunks.
func (r *storeRead) merge(chunk storeRead) {
	maps.Copy(r.extras, chunk.extras)
	r.narrowed, r.region, r.channel = chunk.narrowed, chunk.region, chunk.channel
	if chunk.served == nil {
		r.served = nil

		return
	}
	if r.served == nil {
		r.served = map[string]bool{}
	}
	maps.Copy(r.served, chunk.served)
	if chunk.judged != nil {
		if r.judged == nil {
			r.judged = map[string]bool{}
		}
		maps.Copy(r.judged, chunk.judged)
	}
}

// toStoreProducts converts the products into the storefront shape, enriches
// the variants and dates the ones with nothing to sell: it is the path of a
// caller that publishes every product it is handed.
func (s *Service) toStoreProducts(
	ctx context.Context, products []models.Product, salesChannelIDs []string, regionID string,
) ([]StoreProduct, error) {
	items, read, err := s.enrichStoreProducts(ctx, products, salesChannelIDs, regionID)
	if err != nil {
		return nil, err
	}
	s.restockDates(ctx, items, read)

	return items, nil
}

// enrichStoreProducts converts the products into the storefront shape and
// enriches the variants, without the restock dates: the scan dates only the
// products its filter keeps, and the badge read ([Service.VariantsInStock])
// dates none.
func (s *Service) enrichStoreProducts(
	ctx context.Context, products []models.Product, salesChannelIDs []string, regionID string,
) ([]StoreProduct, storeRead, error) {
	variantIDs := make([]string, 0, len(products))
	var componentIDs []string
	for i := range products {
		variants := products[i].Variants
		for j := range variants {
			variantIDs = append(variantIDs, variants[j].ID)
			for _, c := range variants[j].BundleComponents {
				if !slices.Contains(componentIDs, c.VariantID) {
					componentIDs = append(componentIDs, c.VariantID)
				}
			}
		}
	}

	// The warehouses the read may count, resolved ONCE for the whole page: the
	// set belongs to the request's channels, not to a variant.
	channel := s.locationsServingChannels(ctx, salesChannelIDs)
	region := trimmedRegion(regionID)

	// A bundle's components ride on the same graph call as the page's variants
	// (ADR 0235): their stock records are what the bundle's badge reads. A read
	// naming a region needs the breakdown even when no channel binds
	// warehouses: the region is judged per warehouse (ADR 0422).
	extras, err := s.enrichVariants(ctx, append(slices.Clone(variantIDs), componentIDs...),
		channel != nil || region != "")
	if err != nil {
		return nil, storeRead{}, err
	}
	read := storeRead{extras: extras, served: channel}
	if region != "" {
		if served, judged, ok := s.narrowToRegion(ctx, region, extras, channel); ok {
			read.narrowed = true
			read.served, read.region, read.channel, read.judged = served, region, channel, judged
		}
	}
	served := read.served
	parts, err := s.bundleParts(ctx, componentIDs)
	if err != nil {
		return nil, storeRead{}, err
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
				// The badge is computed HERE, on the one path every storefront
				// body takes, rather than in the handler that happens to need
				// it: a definition that lives in one endpoint is the state gap
				// A17 was filed against.
				InStock: variantBadge(variant, extras, parts, served),
			})
		}
		// The variant slice of the embedded product is emptied: carrying the
		// same data in two places leaves the door open for one of them to be
		// updated and the other forgotten.
		p.Variants = nil
		out = append(out, StoreProduct{
			Product:  p,
			Variants: variants,
			InStock:  productInStock(variants),
		})
	}
	return out, read, nil
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
func (s *Service) enrichVariants(
	ctx context.Context, variantIDs []string, narrowed bool,
) (map[string]enrichment, error) {
	out := make(map[string]enrichment, len(variantIDs))
	if len(variantIDs) == 0 {
		return out, nil
	}
	if s.graph == nil {
		s.log.DebugContext(ctx, "the query layer is not registered; the storefront returns without price/stock")
		return out, nil
	}

	expansions := []query.Expansion{
		{Link: LinkVariantPriceSet, As: keyPriceSet},
		// It names no fields, so it is answered with inventory's default set,
		// which carries no per-warehouse field (ADR 0093, D258): this record is
		// the one [StoreVariant.InventoryItem] publishes.
		{Link: LinkVariantInventory, As: keyInventory},
	}
	if narrowed {
		// A SECOND expansion over the same link, asking for one field. The
		// Query layer allows it as long as the output keys differ, and the
		// separation is the point: the breakdown lands under a key of its own
		// and never enters the record the storefront PUBLISHES
		// ([StoreVariant.InventoryItem]). A shop's warehouse topology is not a
		// shopper's business, and keeping it out is cheaper to prove than
		// remembering to delete it.
		//
		// It costs one more provider call and one more query, and only while a
		// sales channel or the shopper's region narrows the read.
		expansions = append(expansions, query.Expansion{
			Link:   LinkVariantInventory,
			As:     keyInventoryStock,
			Fields: []string{foreignAvailableByLocation},
		})
	}

	records, err := s.graph.Graph(ctx, query.GraphSpec{
		Entity:  EntityVariant,
		Fields:  []string{filterID},
		Filters: map[string]any{filterIDs: variantIDs},
		Limit:   len(variantIDs),
		Expand:  expansions,
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
			priceSet:           asRecord(rec[keyPriceSet]),
			inventory:          asRecord(rec[keyInventory]),
			sellableByLocation: sellableByLocation(asRecord(rec[keyInventoryStock])),
		}
	}
	return out, nil
}

// restockDates writes [StoreVariant.RestockExpectedAt] on the products a
// caller publishes (ADR 0399).
//
// Only a counted variant that is not a bundle, has an inventory record and has
// nothing to sell at the request's warehouses asks -- a backorder-permitting
// one too, since its badge says in stock while nothing is on the shelf. A
// bundle's badge is read from its parts (ADR 0235), so a record of its own,
// which a link landing beside the composition can leave (ADR 0234), dates
// nothing. They ask together, in ONE more graph call, and products where every
// variant has something to sell make none.
//
// The answer is the earliest moment over the warehouses the read counts, the
// channel's when it is narrowed and every one when it is not, as
// [unitsAvailable] decides; a read naming a region counts the ones the
// checkout ranks for it ([Service.restockCounted], ADR 0422). A failed call logs and leaves the dates absent: the
// date is a display concern, and the catalog does not fail for it.
func (s *Service) restockDates(ctx context.Context, items []StoreProduct, read storeRead) {
	var asking []string
	for i := range items {
		for j := range items[i].Variants {
			variant := &items[i].Variants[j]
			extra := read.extras[variant.ID]
			if !variant.ManageInventory || len(variant.BundleComponents) > 0 || extra.inventory == nil ||
				unitsAvailable(extra, read.served) > 0 || slices.Contains(asking, variant.ID) {
				continue
			}
			asking = append(asking, variant.ID)
		}
	}
	if len(asking) == 0 || s.graph == nil {
		return
	}

	records, err := s.graph.Graph(ctx, query.GraphSpec{
		Entity:  EntityVariant,
		Fields:  []string{filterID},
		Filters: map[string]any{filterIDs: asking},
		Limit:   len(asking),
		Expand: []query.Expansion{{
			Link: LinkVariantInventory, As: keyInventoryRestock,
			Fields: []string{foreignRestockByLocation},
		}},
	})
	if err != nil {
		s.log.ErrorContext(ctx, "the restock dates could not be read; the storefront answers without them",
			"variants", len(asking), "error", err)
		return
	}

	forecasts := make(map[string]map[string]time.Time, len(records))
	for _, rec := range records {
		id, ok := rec[filterID].(string)
		if !ok || id == "" || !slices.Contains(asking, id) {
			continue
		}
		forecasts[id] = restockByLocation(asRecord(rec[keyInventoryRestock]))
	}
	counted := s.restockCounted(ctx, read, forecasts)

	dates := make(map[string]*time.Time, len(forecasts))
	for id, byLocation := range forecasts {
		if at := earliestRestock(byLocation, counted); at != nil {
			dates[id] = at
		}
	}
	for i := range items {
		for j := range items[i].Variants {
			if at, ok := dates[items[i].Variants[j].ID]; ok {
				items[i].Variants[j].RestockExpectedAt = at
			}
		}
	}
}

// restockByLocation reads the forecast out of its own expansion record; a
// missing record or field answers nil, which is no date.
func restockByLocation(record query.Record) map[string]time.Time {
	if record == nil {
		return nil
	}
	byLocation, ok := record[foreignRestockByLocation].(map[string]time.Time)
	if !ok {
		return nil
	}

	return byLocation
}

// earliestRestock is the first moment over the warehouses the read counts:
// the served ones when the read is narrowed, every one when it is not (nil).
func earliestRestock(byLocation map[string]time.Time, served map[string]bool) *time.Time {
	var earliest *time.Time
	for locationID, at := range byLocation {
		if served != nil && !served[locationID] {
			continue
		}
		if earliest == nil || at.Before(*earliest) {
			at := at.UTC()
			earliest = &at
		}
	}

	return earliest
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

// VariantsInStock answers, for each variant, the storefront's in-stock badge in
// the given sales channels (ADR 0215).
//
// It is [Service.StoreProductsByIDs] read by variant rather than a rule of its
// own: the variants' products go through the storefront's one path, so a
// variant whose product is unpublished or invisible in the channels is absent,
// and the badge is the one a shopper sees. The products are read [MaxLimit] at
// a time, without the restock dates the badge does not need (ADR 0399).
func (s *Service) VariantsInStock(ctx context.Context, variantIDs, salesChannelIDs []string) (map[string]bool, error) {
	wanted, err := uniqueIDs("variant_ids", variantIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(wanted))
	if len(wanted) == 0 {
		return out, nil
	}
	variants, err := s.repo.ListVariantsByIDs(ctx, wanted)
	if err != nil {
		return nil, err
	}
	asked := make(map[string]bool, len(wanted))
	for _, id := range wanted {
		asked[id] = true
	}
	var productIDs []string
	seen := map[string]bool{}
	for i := range variants {
		if !seen[variants[i].ProductID] {
			seen[variants[i].ProductID] = true
			productIDs = append(productIDs, variants[i].ProductID)
		}
	}
	for start := 0; start < len(productIDs); start += MaxLimit {
		products, err := s.storeProductsByIDs(ctx, productIDs[start:min(start+MaxLimit, len(productIDs))],
			salesChannelIDs, false)
		if err != nil {
			return nil, err
		}
		for i := range products {
			for j := range products[i].Variants {
				if variant := products[i].Variants[j]; asked[variant.ID] {
					out[variant.ID] = variant.InStock
				}
			}
		}
	}

	return out, nil
}
