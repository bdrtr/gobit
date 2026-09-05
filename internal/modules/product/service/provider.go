package service

import (
	"context"
	"math"
	"slices"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
)

// This file is the READ SURFACE the module opens onto the Query layer (ADR
// 0004).
//
// The providers are registered in the container under the names "product.query"
// and "variant.query". Query resolves them by name; the core does not know this
// module, and this module is visible to the core only by satisfying the
// signature.
//
// The reason two separate entities are offered is identity: the price and stock
// links are made with the VARIANT id, not with the product id. Had there been a
// single "product" entity, the links would fall onto the "id" field of the
// product records and nothing would match.

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
// Supported filters: status, handle, collection_id, id/ids. An unrecognized
// filter returns errors.Invalid (ADR 0004): ignoring it silently would leave the
// client believing that an unfiltered list — one it thinks it has filtered — is
// the right answer.
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

	products, err := p.fetch(ctx, ids, filter)
	if err != nil {
		return nil, err
	}
	return records(products, productRecord, opts.Fields, EntityProduct)
}

// fetch reads by id if an id filter was given, and by the criteria if not.
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
		"created_at":     p.CreatedAt,
		"updated_at":     p.UpdatedAt,
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
		"created_at":       v.CreatedAt,
		"updated_at":       v.UpdatedAt,
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
