package graph

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// The two reads that vary by sales channel (ADR 0226). Each is the
// channel-scoped storefront REST read's own service call, and each takes the
// channels from the request's identity, as [queryResolver.Products] does.

// ProductFacets is GET /store/v1/sales-channels/{sales_channel_id}/product-facets.
//
// The text filters are trimmed and an empty one is not given, by the rule the
// listing's resolver writes down; the listing's page, order, inStock and price
// are not arguments at all, since service.Service.StoreFacets counts the whole
// filtered catalog and refuses the two enriched filters.
func (r *queryResolver) ProductFacets(
	ctx context.Context,
	q, collectionID, categoryID, tagID, optionValue *string,
	variantIDs []string,
	attributes []service.AttributeCriterion,
) ([]service.Facet, error) {
	return r.svc.StoreFacets(ctx, service.StoreListOptions{
		CollectionID:    trimmedPointer(collectionID),
		CategoryID:      trimmedPointer(categoryID),
		TagID:           trimmedPointer(tagID),
		OptionValue:     trimmedPointer(optionValue),
		VariantIDs:      variantIDs,
		Attributes:      attributes,
		Search:          trimmedPointer(q),
		SalesChannelIDs: SalesChannelIDsFromContext(ctx),
	})
}

// OptionValues is GET /store/v1/sales-channels/{sales_channel_id}/option-values: the
// published products' values only.
func (r *queryResolver) OptionValues(ctx context.Context, limit, offset *int) (*OptionValuePairList, error) {
	page, err := r.svc.ListOptionValues(ctx, service.ListOptionValuesOptions{
		SalesChannelIDs: SalesChannelIDsFromContext(ctx),
		PublicOnly:      true,
		Limit:           intValue(limit),
		Offset:          intValue(offset),
	})
	if err != nil {
		return nil, err
	}
	return &page, nil
}
