package graph

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// The catalog's vocabulary (ADR 0225). Each root query is the storefront REST
// read's own service call with the same arguments, so the two surfaces cannot
// start answering the same question differently. None is scoped to the
// request's sales channels: neither surface scopes them (see api/store.go).

// Collections is GET /store/v1/collections.
func (r *queryResolver) Collections(ctx context.Context, limit, offset *int) (*CollectionList, error) {
	page, err := r.svc.ListCollections(ctx, intValue(limit), intValue(offset))
	if err != nil {
		return nil, err
	}
	return &page, nil
}

// Categories is GET /store/v1/categories: the public categories only, one
// level at a time when parentId is given.
func (r *queryResolver) Categories(ctx context.Context, parentID *string, limit, offset *int) (*CategoryList, error) {
	page, err := r.svc.ListCategories(ctx, service.ListCategoriesOptions{
		ParentID:   parentID,
		PublicOnly: true,
		Limit:      intValue(limit),
		Offset:     intValue(offset),
	})
	if err != nil {
		return nil, err
	}
	return &page, nil
}

// Tags is GET /store/v1/tags.
func (r *queryResolver) Tags(ctx context.Context, limit, offset *int) (*TagList, error) {
	page, err := r.svc.ListTags(ctx, intValue(limit), intValue(offset))
	if err != nil {
		return nil, err
	}
	return &page, nil
}

// ProductAttributes is GET /store/v1/product-attributes (ADR 0219).
func (r *queryResolver) ProductAttributes(ctx context.Context) ([]models.Attribute, error) {
	return r.svc.ListAttributes(ctx)
}
