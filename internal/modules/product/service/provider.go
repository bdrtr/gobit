package service

import (
	"context"
	"math"
	"slices"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
)

// This file is the READ SURFACE the module opens onto the Query layer (ADR
// 0004).
//
// The providers are registered in the container under the names "product.query"
// and "variant.query" (the third one, "category.query", lives in
// category_provider.go). Query resolves them by name; the core does not know
// this module, and this module is visible to the core only by satisfying the
// signature.
//
// The reason product and variant are separate entities is identity: the price
// and stock links are made with the VARIANT id, not with the product id. Had
// there been a single "product" entity, the links would fall onto the "id"
// field of the product records and nothing would match.
//
// The category entity is separate for a different reason and it is written out
// where it lives: it is a VOCABULARY, read to turn a word into an id, not a
// second view of a product.

// providerUnlimited is the limit that goes to the query when Limit 0
// (unlimited) is given.
//
// A genuinely unlimited query could pull the whole catalog into memory with a
// single request; this constant both stands for unlimited and fits safely into
// an int32 query parameter.
const providerUnlimited = math.MaxInt32

// productProvider offers the product records to the Query layer.
type productProvider struct {
	repo repository.Store
}

// variantProvider offers the variant records to the Query layer.
type variantProvider struct {
	repo repository.Store
}

// That the providers satisfy the core contract is pinned at compile time.
var (
	_ query.Provider = (*productProvider)(nil)
	_ query.Provider = (*variantProvider)(nil)
)

// NewProductProvider builds the Query provider of the "product" entity.
func NewProductProvider(repo repository.Store) query.Provider {
	return &productProvider{repo: repo}
}

// NewVariantProvider builds the Query provider of the "variant" entity.
func NewVariantProvider(repo repository.Store) query.Provider {
	return &variantProvider{repo: repo}
}

// Entity returns the name of the entity the provider offers.
func (p *productProvider) Entity() string { return EntityProduct }

// List returns the product records.
//
// Supported filters: status, handle, collection_id, q, category_id, tag_id and
// id/ids. An unrecognized filter returns errors.Invalid (ADR 0004): ignoring it
// silently would leave the client believing that an unfiltered list — one it
// thinks it has filtered — is the right answer.
//
// # The free-text search
//
// "q" was the last filter of the storefront's set this surface could not
// answer, and it is spelled the storefront's way (see [filterSearch]). The
// predicate is NOT written here: the term becomes
// [repository.ProductFilter].Search and the shared filter body turns it into
// title ILIKE '%' || $4::text || '%' for the listing AND for the count (see
// repository/saleschannel.go). One definition, two queries, no copy in Go.
//
// The consumer was already waiting. The panel reaches the catalog only through
// Query (ADR 0011), so while this case did not exist a shopper could search the
// shop and the operator maintaining that same catalog could only page through
// it — the screen carried no search box because the read layer answered no such
// filter (internal/adminui/catalog.go).
//
// # An empty or whitespace-only term builds NO filter
//
// The value is trimmed first, and when nothing survives the trim no filter is
// built at all: the caller gets the page it would have got had it not sent the
// key. That is the "matches everything" direction of the two, and it is picked
// deliberately.
//
// Handing the value on as it came is what the two wrong answers look like, and
// they point in OPPOSITE directions. An empty string reaches SQL as
// ILIKE '%%' and matches every row: a caller that believes it searched is shown
// the whole catalog. A run of spaces reaches SQL as ILIKE '%   %' and matches
// only the titles carrying that run, which is to say nothing: a caller that
// searched for nothing is shown an empty shop. Neither answer says a word about
// what happened, and no caller can tell which of the two it got.
//
// Between them, "no filter" is the answer this module ALREADY gives to the same
// input everywhere else: REST counts an empty parameter as not given
// (stringParam in api/api.go), GraphQL trims the argument down to nil
// (trimmedPointer in graph/resolver.go), and TestEmptyTextArgumentBuildsNoFilter
// in graph/schema_test.go holds EVERY text argument of the storefront listing to
// that rule — including, deliberately, the ones added after it was written. A
// read layer that answered "nothing" to a search box an operator had just
// cleared, while the shop answered "the catalog", would be one question with two
// answers.
//
// Trimming is not only about the empty case: " shirt " would reach SQL as
// '% shirt %' and miss the product whose title merely ENDS in "shirt". What
// travels is the trimmed term, exactly as on the other two surfaces.
//
// # The taxonomy filters
//
// category_id and tag_id become EXISTS subqueries in the repository, not joins
// (see [repository.ProductFilter] and repository/saleschannel.go): a product
// that sits in three categories is still one row. Written as a join it would
// come back once per membership, the page would hold fewer products than its
// limit promises and a count would count MEMBERSHIPS. That is a property of the
// SQL, so it is proven against a real database rather than against the fake
// (see product_integration_test.go).
//
// The two names are the storefront's own (see [filterCategoryID]): before this
// existed the shop could narrow the catalog by category and the read layer could
// not, so an operator's panel — which reaches the catalog only through Query —
// had no way to ask the question its customers were already asking.
//
// # Why the taxonomy filters cannot be COMBINED with id/ids
//
// Given an id filter, [productProvider.fetch] takes the batch read and applies
// the remaining criteria IN MEMORY. That works for status, handle and
// collection_id because each of them is a scalar column on [models.Product] —
// the re-check reads the record it already holds. Membership is not on the
// record: [repository.Store.ListProductsByIDs] returns the product rows, and
// Categories/Tags are opt-in relation slices filled only by the SERVICE
// ([Service.ListProducts] with WithRelations), which this provider does not go
// through — it holds the repository directly. On that path the slices are
// ALWAYS nil.
//
// So the combination is refused with errors.Invalid, and two other answers were
// rejected to get there:
//
//   - Re-check the nil slices anyway. It compiles, it looks like the three
//     lines above it, and it matches NOTHING: every id + category_id query comes
//     back empty and says nothing about why. That is the silent wrong answer
//     this repository bans, dressed as a filter.
//   - Fetch the memberships and re-check honestly. It is affordable —
//     [repository.Store.ListCategoriesByProductIDs] is a bulk read and the id
//     branch pages AFTER filtering, so unlike the variant provider's channel
//     case there is no short-page hazard. It was still rejected: it writes the
//     membership predicate a SECOND time, in Go, beside the SQL EXISTS. A
//     category is a TREE, and the day the SQL grows to match a category's
//     descendants — the obvious next request for a shop menu — the Go copy goes
//     on answering about direct membership, and the same filter gives two
//     answers depending on which path the caller happened to take. On top of
//     that the combination has no caller today: the panel's only id-filtered
//     spec reads ONE product by id.
//
// The refusal does not look at the data: id + category_id fails the same way
// whether or not that product is in that category. A refusal that fired only
// when the answer would have been empty would be a filter that works sometimes,
// and the caller could not tell which time it got.
//
// # Why the SEARCH cannot be combined with id/ids either
//
// Same answer, DIFFERENT reason, and the difference has to be written down
// because the obvious objection to it is correct: unlike a category membership,
// the title IS a scalar column on [models.Product], so [productProvider.fetch]
// could re-check it in memory beside status, handle and collection_id. Nothing
// structural stops it.
//
// What stops it is that the re-check would be a SECOND definition of a
// predicate whose first definition is in SQL — the hazard the taxonomy refusal
// was written for — and here the two cannot be made to agree by construction.
// ILIKE folds case the way the CLUSTER folds it: the rule comes from the CTYPE
// the data directory was created with, which is not a constant a Go process can
// read. The repository already knows this and probes for it at startup
// ([github.com/bdrtr/gobit/core/db.CaseFolding], core/db/casefold.go, ADR
// 0015). Measured on two PostgreSQL 16 clusters and on Go, uppercase in the
// title against lowercase in the term:
//
//	pair                                CTYPE C   CTYPE C.UTF-8   Go ToLower+Contains
//	"SHIRT" / "shirt"                   true      true            true
//	U+00C7 + "OCUK" / U+00E7 + "ocuk"   FALSE     true            true
//	U+0130 + "NCE" / "ince"             FALSE     true            true
//
// So on a C cluster the id path would hand back rows that the same term does
// NOT return without the id filter, and on a C.UTF-8 one it would not: whether
// the Go copy is right would depend on how somebody ran initdb. Those are not
// hypothetical clusters — deploy/docker-compose.yml has carried the C.UTF-8
// setting only since the search defect ADR 0015 records, and a locale is fixed
// at initdb time, so every data directory created before that fix still folds
// ASCII only. A predicate whose two implementations cannot be made to agree is
// refused rather than half-answered.
//
// Three answers were rejected to get here:
//
//   - Re-check with strings.Contains alone. Case-SENSITIVE beside a
//     case-INSENSITIVE SQL predicate: "shirt" finds the shirt on every path but
//     the id one. It is the cheapest way to write the two-answers fault and it
//     looks exactly like the three re-checks above it, which is what makes it
//     the likely mistake rather than an unlikely one.
//   - Re-check with strings.ToLower on both sides — what the fake store does
//     (memstore_test.go). It agrees with SQL for ASCII, and that is precisely
//     what makes it dangerous: every fixture in this package is ASCII, so the
//     divergence is invisible to the tests here and shows up in a catalog with
//     non-ASCII titles on a C-locale cluster, the deployment ADR 0015 was
//     written about.
//   - Push the ids into the query as one more predicate so that ONE engine
//     answers the whole question. This is the only version with no second
//     definition, and it is not a provider-sized change: it edits the shared
//     filter body and both queries built on it, that is, the storefront's
//     hottest path. If the combination ever gets a caller, that is the road —
//     this paragraph is the note it leaves behind.
//
// It has no caller today: the panel's only id-filtered spec reads ONE product
// by id.
//
// The refusal is decided AFTER the term is normalized, and that order is part
// of the decision: whitespace is not a criterion (see above), so an id filter
// arriving beside a search box the operator has just cleared is ANSWERED, not
// refused. Refusing it would turn an empty box into an error page. Unlike the
// value, the DATA is never consulted — id + q fails whether or not that
// product's title matches.
//
// # What the cost is, measured
//
// The structural half was always checkable: there is NO index on title. The
// module's schema creates a unique partial index on handle, one on status, one
// on collection_id and the listing's own (created_at DESC, id DESC), and it
// creates no trigram, no full-text and no expression index anywhere
// (migrations/000001_product_init.up.sql). The pattern also carries a LEADING
// wildcard, which no B-tree can serve even if one existed on title.
//
// The obvious conclusion from those two facts — "the search is a sequential
// scan, therefore it is slow" — is HALF WRONG, and the wrong half is the half
// that would decide what to do about it. Measured on 52,004 real products
// (docs/catalog-search-cost.md): the listing's cost does not follow the term,
// it follows how far down the ordering the page's last match sits. A term
// matching almost the whole catalog is answered from the (created_at DESC,
// id DESC) index in 0.03 ms, because the scan stops once 25 rows have passed
// the filter and the 25th is 29 rows in. A term matching ONE product costs
// 9.1 ms and reads all 730 pages of the table, because there is nothing to stop
// early for. The rare search is the expensive one — the opposite of what a
// missing index is usually read to mean, and the reason "is the search slow"
// has no answer that does not name WHICH search.
//
// The count behaves differently again, and in the caller's favor: with the
// storefront's sales channel filter on, a term matching one product takes the
// channel-filtered count from about 74 ms DOWN to about 13 ms, because the ILIKE is
// evaluated before the per-row visibility subquery and removes 52,003 of its
// invocations. This surface never runs that query (it applies no channel and
// asks for no count), but the same filter body serves it, so the number belongs
// next to the filter rather than only next to the count.
//
// The written boundary is a slope, not a row count: 0.18 microseconds per
// catalog row, linear from 10,000 rows upward, holding while the table stays in
// memory and the rows stay as narrow as the rig's. The measurement says what
// would end that, and it says it in one place instead of being guessed at in
// several: this repository has already carried a godoc claiming an index was
// used, and measurement proved it wrong.
//
// # The condition every figure above was taken under, added 2026-09-06
//
// They were all measured on the surviving rig, and that cluster was created
// with a collation under which the database's own case folding does not work —
// it fails the probe this repository runs at startup, so an upper-case letter
// carrying a diacritic does not match its lower-case form there at all. Every
// ILIKE figure above is therefore OPTIMISTIC, and by a measured amount rather
// than a guessed one: the same bare comparison costs about 8.7 ms on that
// cluster against about 14.5 ms on one that folds correctly, a factor of 1.66.
// The SHAPE of the finding survives — a rare term is still the expensive one,
// and the slope is still linear — but the absolute milliseconds belong to a
// cluster no deployment should have.
//
// The reason this was not simply re-measured and overwritten is worth stating,
// because it is the whole argument for the rig being rebuildable: fixing that
// cluster's collation needs a new data directory, which would have deleted the
// only copy of the catalog these numbers were taken on. The rig can now be
// rebuilt from this repository, so the next person to touch this block can
// stand it up on a correctly folding cluster and replace the figures rather
// than inheriting them.
//
// # The sales channel filter is NOT APPLIED here
//
// This surface is a CROSS-MODULE read and there is no customer request behind
// it: the cart or the order making the Query call does not carry the channels of
// a publishable key. Filtering by an identity that does not exist would either
// hide everything or mean picking a made-up channel set.
//
// The known limit of this is the following: channel scope is the rule of the
// STOREFRONT SURFACE (see [Service.ListStoreProducts]), and a module reading
// from this provider sees the products that have a channel assignment as well.
// What is right today is this — the title of a product added to a cart has to
// stay resolvable even if that product is later moved to another channel. If
// scoping by channel becomes necessary, the right way is not to put a silent
// default into this provider, but for the caller to pass its channel set
// EXPLICITLY as a filter.
func (p *productProvider) List(ctx context.Context, opts query.ListOptions) ([]query.Record, error) {
	filter := repository.ProductFilter{Limit: providerLimit(opts.Limit), Offset: opts.Offset}
	var ids []string

	for key, raw := range opts.Filters {
		switch key {
		case filterStatus:
			value, err := stringFilter(key, raw)
			if err != nil {
				return nil, err
			}
			filter.Status = &value
		case filterHandle:
			value, err := stringFilter(key, raw)
			if err != nil {
				return nil, err
			}
			filter.Handle = &value
		case filterCollectionID:
			value, err := stringFilter(key, raw)
			if err != nil {
				return nil, err
			}
			filter.CollectionID = &value
		case filterSearch:
			// The only filter whose value is NORMALIZED rather than carried
			// through: a term that is empty after trimming leaves Search nil,
			// which is the same state as "the key was never sent". See
			// [searchFilter].
			value, err := searchFilter(key, raw)
			if err != nil {
				return nil, err
			}
			filter.Search = value
		case filterCategoryID:
			value, err := stringFilter(key, raw)
			if err != nil {
				return nil, err
			}
			filter.CategoryID = &value
		case filterTagID:
			value, err := stringFilter(key, raw)
			if err != nil {
				return nil, err
			}
			filter.TagID = &value
		case filterID, filterIDs:
			values, err := stringsFilter(key, raw)
			if err != nil {
				return nil, err
			}
			ids = append(ids, values...)
		default:
			return nil, unsupportedFilter(EntityProduct, key)
		}
	}

	// The check is made after the whole map has been walked, not inside the
	// case: which key the map hands over first is random, so a refusal raised
	// mid-walk would name category_id on one run and tag_id on the next for the
	// same request. The order here is fixed, and a request that carries both
	// always reports the same one.
	//
	// The search joins the same list and joins it LAST, so that a request which
	// already used to be refused keeps being refused by the same name. It is
	// read off filter.Search rather than off the map, which is what makes a
	// whitespace term — normalized away above — no longer part of the request.
	if len(ids) > 0 {
		switch {
		case filter.CategoryID != nil:
			return nil, taxonomyWithIDs(filterCategoryID)
		case filter.TagID != nil:
			return nil, taxonomyWithIDs(filterTagID)
		case filter.Search != nil:
			return nil, searchWithIDs()
		}
	}

	products, err := p.fetch(ctx, ids, filter)
	if err != nil {
		return nil, err
	}
	return records(products, productRecord, opts.Fields, EntityProduct)
}

// fetch reads by id if an id filter was given, and by the criteria if not.
//
// CategoryID, TagID and Search cannot reach the id branch: [productProvider.List]
// refuses those combinations before calling here. The reasons are written out
// there and they are NOT the same reason — the taxonomy membership is not on
// the record this branch holds, while the title is on it but its match rule
// belongs to the database. If a future filter is added, the question to ask of
// it is in two parts: can the criterion be answered from a [models.Product] as
// [repository.Store.ListProductsByIDs] returns it, and would answering it here
// give the SAME verdict the SQL gives? A criterion that fails either half must
// be refused, not skipped.
func (p *productProvider) fetch(ctx context.Context, ids []string, filter repository.ProductFilter) ([]models.Product, error) {
	if len(ids) == 0 {
		return p.repo.ListProducts(ctx, filter)
	}

	products, err := p.repo.ListProductsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	// The id list is already a narrow set; the remaining criteria are applied in
	// memory so that the two separate query paths give consistent results.
	out := make([]models.Product, 0, len(products))
	for i := range products {
		product := &products[i]
		if filter.Status != nil && product.Status.String() != *filter.Status {
			continue
		}
		if filter.Handle != nil && product.Handle != *filter.Handle {
			continue
		}
		if filter.CollectionID != nil && (product.CollectionID == nil || *product.CollectionID != *filter.CollectionID) {
			continue
		}
		out = append(out, *product)
	}
	return page(out, filter.Limit, filter.Offset), nil
}

// FetchByIDs returns the product records of the given ids in a SINGLE query.
func (p *productProvider) FetchByIDs(ctx context.Context, ids, fields []string) ([]query.Record, error) {
	products, err := p.repo.ListProductsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return records(products, productRecord, fields, EntityProduct)
}

// Entity returns the name of the entity the provider offers.
func (v *variantProvider) Entity() string { return EntityVariant }

// List returns the variant records.
//
// Supported filters: product_id, product_ids, id/ids and
// [FilterSalesChannelIDs].
//
// # The sales channel filter
//
// Unlike the product provider ([productProvider.List], "The sales channel filter
// is NOT APPLIED here") this provider does apply the channel scope — but only
// when the caller asks for it EXPLICITLY. There is no silent default and the
// difference matters: not every caller reading from this surface has a customer
// request behind it, and picking a made-up channel set would either hide
// everything or filter nothing at all.
//
// The consumer of the filter is the cart WRITE path: the workflow adding a line
// reads the variant from here and passes the channels coming from the
// AUTHENTICATED identity of the request as a filter (see
// internal/workflows/cart). The rule is not rewritten here; the repository is
// asked with the very SQL template the storefront listing uses (see
// repository/saleschannel.go).
//
// The nil versus empty slice distinction is preserved in the caller: if the key
// is NOT given at all no filter is applied, if an empty array is given it IS
// APPLIED and only the variants of the products with no assignment are returned
// — the same meaning as on the read surface (see
// [StoreListOptions.SalesChannelIDs]).
//
// # Why only together with an id
//
// If the channel filter is given WITHOUT id/ids, errors.Invalid is returned.
// There are two reasons and the second one is technical:
//
//   - The question of this surface is "is this variant within my scope", not
//     "list the variants within my scope". The second one has no consumer today,
//     and a capability with no consumer is a surface whose correctness is tested
//     nowhere.
//   - The id-less paths do the pagination IN THE DATABASE (ListVariants reads
//     with a LIMIT). Had the filter been applied on the Go side on that path, the
//     page would come back short, and silently so — that is exactly why the
//     filter of the product listing went into SQL (see
//     repository/saleschannel.go). Rather than opening a surface that paginates
//     wrongly, rejecting the unwanted combination is preferable.
func (v *variantProvider) List(ctx context.Context, opts query.ListOptions) ([]query.Record, error) {
	var (
		ids        []string
		productIDs []string
		channels   []string
		scoped     bool
	)

	for key, raw := range opts.Filters {
		switch key {
		case filterProductID, filterProductIDs:
			values, err := stringsFilter(key, raw)
			if err != nil {
				return nil, err
			}
			productIDs = append(productIDs, values...)
		case filterID, filterIDs:
			values, err := stringsFilter(key, raw)
			if err != nil {
				return nil, err
			}
			ids = append(ids, values...)
		case FilterSalesChannelIDs:
			values, err := stringsFilter(key, raw)
			if err != nil {
				return nil, err
			}
			// An empty array is a DECISION too ("an identity with no channel"), so
			// its presence is carried in a separate flag; looking at the length of the
			// slice would collapse two different cases into one.
			channels, scoped = values, true
		default:
			return nil, unsupportedFilter(EntityVariant, key)
		}
	}

	if scoped && len(ids) == 0 {
		return nil, errors.Invalid(codeInvalidInput,
			"filter %q can only be used together with %q or %q",
			FilterSalesChannelIDs, filterID, filterIDs).
			WithDetails(filterDetails(EntityVariant, FilterSalesChannelIDs))
	}

	variants, err := v.fetch(ctx, ids, productIDs, channels, scoped, opts)
	if err != nil {
		return nil, err
	}
	return records(variants, variantRecord, opts.Fields, EntityVariant)
}

// fetch reads the variants by the narrowest criterion.
//
// If scoped is true the channel scope IS APPLIED, and that is possible only on
// the id branch ([variantProvider.List] rejects the other combinations).
func (v *variantProvider) fetch(
	ctx context.Context,
	ids, productIDs, channels []string,
	scoped bool,
	opts query.ListOptions,
) ([]models.Variant, error) {
	limit := providerLimit(opts.Limit)

	switch {
	case len(ids) > 0:
		if scoped {
			// The scope is applied ON the ids, before the rows are read: never
			// fetching the record of an invisible variant is both cheaper and less
			// open to accidents than fetching it and throwing it away afterwards.
			visible, err := v.repo.VisibleVariantIDs(ctx, ids, channels)
			if err != nil {
				return nil, err
			}
			ids = slices.DeleteFunc(slices.Clone(ids), func(id string) bool {
				_, ok := visible[id]
				return !ok
			})
			if len(ids) == 0 {
				return []models.Variant{}, nil
			}
		}

		variants, err := v.repo.ListVariantsByIDs(ctx, ids)
		if err != nil {
			return nil, err
		}
		if len(productIDs) > 0 {
			variants = slices.DeleteFunc(variants, func(variant models.Variant) bool {
				return !slices.Contains(productIDs, variant.ProductID)
			})
		}
		return page(variants, limit, opts.Offset), nil

	case len(productIDs) > 1:
		variants, err := v.repo.ListVariantsByProductIDs(ctx, productIDs)
		if err != nil {
			return nil, err
		}
		return page(variants, limit, opts.Offset), nil

	case len(productIDs) == 1:
		return v.repo.ListVariants(ctx, repository.VariantFilter{
			ProductID: &productIDs[0],
			Limit:     limit,
			Offset:    opts.Offset,
		})

	default:
		return v.repo.ListVariants(ctx, repository.VariantFilter{Limit: limit, Offset: opts.Offset})
	}
}

// FetchByIDs returns the variant records of the given ids in a SINGLE query.
func (v *variantProvider) FetchByIDs(ctx context.Context, ids, fields []string) ([]query.Record, error) {
	variants, err := v.repo.ListVariantsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return records(variants, variantRecord, fields, EntityVariant)
}

// The record keys that MORE THAN ONE of this module's entities offers.
//
// Only these two are named, and the rest of the keys stay literals where the
// record is built. That is deliberate: a key belongs to ONE entity's contract,
// and a shared constant for "handle" would suggest that a product's handle and a
// category's handle are the same promise — they can move apart, and the order
// module writes the same rule for its own line fields. The timestamps are the
// exception because every entity here carries them and the pair is now written
// three times; a typo in one copy would give that one entity a differently
// spelled key, and nothing but the consumer would ever notice.
const (
	fieldCreatedAt = "created_at"
	fieldUpdatedAt = "updated_at"
)

// productRecord turns a product into a Query record.
//
// The keys are the same as the JSON field names: if the same data appeared under
// two different names on two surfaces, the one writing the query and the one
// reading the response would have to use different dictionaries.
func productRecord(p models.Product) query.Record {
	return query.Record{
		"id":             p.ID,
		"handle":         p.Handle,
		"title":          p.Title,
		"subtitle":       deref(p.Subtitle),
		"description":    deref(p.Description),
		"thumbnail":      deref(p.Thumbnail),
		"status":         p.Status.String(),
		"is_giftcard":    p.IsGiftcard,
		"discountable":   p.Discountable,
		"weight":         derefInt32(p.Weight),
		"collection_id":  deref(p.CollectionID),
		"material":       deref(p.Material),
		"origin_country": deref(p.OriginCountry),
		"metadata":       p.Metadata,
		fieldCreatedAt:   p.CreatedAt,
		fieldUpdatedAt:   p.UpdatedAt,
	}
}

// variantRecord turns a variant into a Query record.
func variantRecord(v models.Variant) query.Record {
	return query.Record{
		"id":               v.ID,
		"product_id":       v.ProductID,
		"title":            v.Title,
		"sku":              deref(v.SKU),
		"barcode":          deref(v.Barcode),
		"ean":              deref(v.EAN),
		"upc":              deref(v.UPC),
		"manage_inventory": v.ManageInventory,
		"allow_backorder":  v.AllowBackorder,
		"weight":           derefInt32(v.Weight),
		"rank":             v.Rank,
		"metadata":         v.Metadata,
		fieldCreatedAt:     v.CreatedAt,
		fieldUpdatedAt:     v.UpdatedAt,
	}
}

// records turns the models into records and selects the requested fields.
func records[T any](items []T, toRecord func(T) query.Record, fields []string, entity string) ([]query.Record, error) {
	out := make([]query.Record, 0, len(items))
	for i := range items {
		rec, err := project(toRecord(items[i]), fields, entity)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

// project selects the requested fields from a record.
//
// If the field list is empty the record is returned as is. A field the provider
// does not recognize produces errors.Invalid (ADR 0004): skipping a missing
// field silently would mean presenting a record that does not carry the data the
// caller expects as a valid one.
func project(rec query.Record, fields []string, entity string) (query.Record, error) {
	if len(fields) == 0 {
		return rec, nil
	}
	out := make(query.Record, len(fields))
	for _, field := range fields {
		value, ok := rec[field]
		if !ok {
			return nil, errors.Invalid(codeInvalidInput,
				"entity %q does not offer the field %q", entity, field).
				WithDetails(map[string]any{"entity": entity, "field": field})
		}
		out[field] = value
	}
	return out, nil
}

// page applies limit/offset to an in-memory slice.
func page[T any](items []T, limit, offset int) []T {
	if offset >= len(items) {
		return []T{}
	}
	items = items[offset:]
	if limit > 0 && limit < len(items) {
		items = items[:limit]
	}
	return items
}

// providerLimit turns Query's "0 = unlimited" contract into a query limit.
func providerLimit(limit int) int {
	if limit <= 0 {
		return providerUnlimited
	}
	return limit
}

// stringFilter turns a filter value into a single string.
func stringFilter(key string, raw any) (string, error) {
	value, ok := raw.(string)
	if !ok {
		return "", errors.Invalid(codeInvalidInput,
			"filter %q has to be a string, %T given", key, raw)
	}
	return value, nil
}

// searchFilter turns the free-text filter value into a search term.
//
// It returns nil when nothing survives the trim, and nil means the criterion is
// NOT APPLIED — the same state the filter struct is in when the key was never
// sent. The whole argument for that choice, and for the two silent answers it
// keeps out, is in [productProvider.List] under "An empty or whitespace-only
// term builds NO filter"; it is not repeated here, because a rule written twice
// is a rule that gets fixed once.
//
// The returned pointer addresses the TRIMMED copy and never the caller's value:
// handing the raw string on would push " shirt " into the ILIKE pattern, and a
// term that is trimmed for the emptiness test but not for the query would be
// the worst of both — the check would pass and the match would still be made
// against the padding.
func searchFilter(key string, raw any) (*string, error) {
	value, err := stringFilter(key, raw)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, nil
	}
	return &trimmed, nil
}

// boolFilter turns a filter value into a boolean.
//
// ONLY a real bool is accepted. The strings "true" and "false" were left out on
// purpose: a filter that reads two spellings of the same value teaches two
// client dialects, and the first value outside the pair ("yes", "1", "") would
// then need a rule of its own — one that could only be guessed at. The callers
// of this surface build their filters in Go, and a definition arriving as JSON
// carries a JSON boolean, which unmarshals to bool anyway.
func boolFilter(key string, raw any) (bool, error) {
	value, ok := raw.(bool)
	if !ok {
		return false, errors.Invalid(codeInvalidInput,
			"filter %q has to be a boolean, %T given", key, raw)
	}
	return value, nil
}

// stringsFilter turns a filter value into a string slice.
//
// A single string is accepted too: "id" and "ids" use the same path and the
// caller does not have to wrap a single id in a slice. The []any form is for the
// filters coming from JSON.
func stringsFilter(key string, raw any) ([]string, error) {
	switch v := raw.(type) {
	case string:
		return []string{v}, nil
	case []string:
		return v, nil
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			value, ok := item.(string)
			if !ok {
				return nil, errors.Invalid(codeInvalidInput,
					"the values of filter %q have to be strings, %T given", key, item)
			}
			out = append(out, value)
		}
		return out, nil
	default:
		return nil, errors.Invalid(codeInvalidInput,
			"filter %q has to be a string or a string slice, %T given", key, raw)
	}
}

// taxonomyWithIDs builds the typed error of the refused combination.
//
// It is a REFUSAL, not a failure: the caller asked something this path cannot
// answer correctly, and the answer says which filter to drop. The details carry
// the taxonomy filter rather than "id", because that is the one the caller has
// to take out — an id filter with no taxonomy filter is a perfectly good
// request, and the panel makes it on every product page.
func taxonomyWithIDs(key string) error {
	return errors.Invalid(codeInvalidInput,
		"filter %q cannot be combined with %q or %q: on the id path the category and tag "+
			"membership is not read, so the filter could not be applied", key, filterID, filterIDs).
		WithDetails(filterDetails(EntityProduct, key))
}

// searchWithIDs builds the typed error of the refused q + id combination.
//
// It is a SEPARATE constructor from [taxonomyWithIDs] although the two produce
// the same error kind and the same details shape, and the reason is the
// MESSAGE: the taxonomy refusal says the membership is not read on the id path,
// which is true of a category and false of a title. Reusing that sentence here
// would hand the caller an explanation that does not describe its request, and
// an explanation that does not fit is worse than none — the reader goes looking
// for a membership that has nothing to do with what it asked.
func searchWithIDs() error {
	return errors.Invalid(codeInvalidInput,
		"filter %q cannot be combined with %q or %q: the title match is defined by the "+
			"database's ILIKE, and re-checking it in Go on the id path would answer with a "+
			"different case rule", filterSearch, filterID, filterIDs).
		WithDetails(filterDetails(EntityProduct, filterSearch))
}

// unsupportedFilter builds the typed error for an unrecognized filter.
func unsupportedFilter(entity, key string) error {
	return errors.Invalid(codeInvalidInput,
		"the %q provider does not support the filter %q", entity, key).
		WithDetails(filterDetails(entity, key))
}

// filterDetails builds the structured details of a filter error.
//
// The shape lives in ONE place because the client reads the error not from the
// MESSAGE but from these fields: had one of the keys been spelled differently at
// one call site, the same error class would show up with two different bodies
// and the reading side would have to recognize both.
func filterDetails(entity, key string) map[string]any {
	return map[string]any{"entity": entity, "filter": key}
}

// deref turns a pointer into a value; returns an empty string if it is nil.
//
// That the record holds an empty string instead of nil is deliberate: once it is
// written to JSON, the difference between "subtitle": null and "subtitle": "" is
// of no concern to the consumer, but a nil pointer produces surprises in type
// assertions.
func deref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// derefInt32 turns a pointer into a value; returns zero if it is nil.
func derefInt32(v *int32) int32 {
	if v == nil {
		return 0
	}
	return *v
}
