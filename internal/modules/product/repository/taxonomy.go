package repository

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository/productdb"
)

// CreateCollection writes a new collection.
func (r *Repo) CreateCollection(ctx context.Context, c models.Collection) (models.Collection, error) {
	meta, err := fromMetadata(c.Metadata)
	if err != nil {
		return models.Collection{}, err
	}

	row, err := r.q.CreateCollection(ctx, productdb.CreateCollectionParams{
		ID:       c.ID,
		Title:    c.Title,
		Handle:   c.Handle,
		Metadata: meta,
	})
	if err != nil {
		return models.Collection{}, wrapDB(err, "could not create collection (%s)", c.Handle)
	}
	return toCollection(row)
}

// GetCollection returns the collection by id.
func (r *Repo) GetCollection(ctx context.Context, id string) (models.Collection, error) {
	row, err := r.q.GetCollection(ctx, id)
	if err != nil {
		return models.Collection{}, wrapDB(err, "collection not found: %s", id)
	}
	return toCollection(row)
}

// ListCollections returns the collections paginated.
func (r *Repo) ListCollections(ctx context.Context, limit, offset int) ([]models.Collection, error) {
	rows, err := r.q.ListCollections(ctx, productdb.ListCollectionsParams{
		Lim: toInt32(limit),
		Off: toInt32(offset),
	})
	if err != nil {
		return nil, wrapDB(err, "could not list collections")
	}

	out := make([]models.Collection, 0, len(rows))
	for i := range rows {
		c, err := toCollection(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// CountCollections returns the total number of collections.
func (r *Repo) CountCollections(ctx context.Context) (int, error) {
	n, err := r.q.CountCollections(ctx)
	if err != nil {
		return 0, wrapDB(err, "could not read collection count")
	}
	return int(n), nil
}

// CreateCategory writes a new category.
func (r *Repo) CreateCategory(ctx context.Context, c models.Category) (models.Category, error) {
	row, err := r.q.CreateCategory(ctx, productdb.CreateCategoryParams{
		ID:          c.ID,
		Name:        c.Name,
		Handle:      c.Handle,
		Description: c.Description,
		ParentID:    c.ParentID,
		IsActive:    c.IsActive,
		IsInternal:  c.IsInternal,
		Rank:        c.Rank,
	})
	if err != nil {
		return models.Category{}, wrapDB(err, "could not create category (%s)", c.Handle)
	}
	return toCategory(row), nil
}

// GetCategory returns the category by id.
func (r *Repo) GetCategory(ctx context.Context, id string) (models.Category, error) {
	row, err := r.q.GetCategory(ctx, id)
	if err != nil {
		return models.Category{}, wrapDB(err, "category not found: %s", id)
	}
	return toCategory(row), nil
}

// ListCategories returns the categories paginated; if parentID is given only
// the children of that node are listed.
func (r *Repo) ListCategories(ctx context.Context, parentID *string, limit, offset int) ([]models.Category, error) {
	rows, err := r.q.ListCategories(ctx, productdb.ListCategoriesParams{
		ParentID: parentID,
		Lim:      toInt32(limit),
		Off:      toInt32(offset),
	})
	if err != nil {
		return nil, wrapDB(err, "could not list categories")
	}

	out := make([]models.Category, 0, len(rows))
	for i := range rows {
		out = append(out, toCategory(rows[i]))
	}
	return out, nil
}

// CountCategories returns the total number of categories.
func (r *Repo) CountCategories(ctx context.Context, parentID *string) (int, error) {
	n, err := r.q.CountCategories(ctx, parentID)
	if err != nil {
		return 0, wrapDB(err, "could not read category count")
	}
	return int(n), nil
}

// CreateTag writes a new tag.
func (r *Repo) CreateTag(ctx context.Context, t models.Tag) (models.Tag, error) {
	row, err := r.q.CreateTag(ctx, productdb.CreateTagParams{ID: t.ID, Value: t.Value})
	if err != nil {
		return models.Tag{}, wrapDB(err, "could not create tag (%s)", t.Value)
	}
	return toTag(row), nil
}

// GetTagByValue returns the tag by value.
func (r *Repo) GetTagByValue(ctx context.Context, value string) (models.Tag, error) {
	row, err := r.q.GetTagByValue(ctx, value)
	if err != nil {
		return models.Tag{}, wrapDB(err, "tag not found: %s", value)
	}
	return toTag(row), nil
}

// ListTags returns the tags paginated.
func (r *Repo) ListTags(ctx context.Context, limit, offset int) ([]models.Tag, error) {
	rows, err := r.q.ListTags(ctx, productdb.ListTagsParams{
		Lim: toInt32(limit),
		Off: toInt32(offset),
	})
	if err != nil {
		return nil, wrapDB(err, "could not list tags")
	}

	out := make([]models.Tag, 0, len(rows))
	for i := range rows {
		out = append(out, toTag(rows[i]))
	}
	return out, nil
}

// CountTags returns the total number of tags.
func (r *Repo) CountTags(ctx context.Context) (int, error) {
	n, err := r.q.CountTags(ctx)
	if err != nil {
		return 0, wrapDB(err, "could not read tag count")
	}
	return int(n), nil
}

// SetProductTags REPLACES the product's tags with the given set.
//
// First the existing bindings are deleted, then the new ones are written; the
// caller must wrap this inside a transaction (InTx), otherwise the product can
// look untagged in between.
func (r *Repo) SetProductTags(ctx context.Context, productID string, tagIDs []string) error {
	if err := r.q.DeleteProductTags(ctx, productID); err != nil {
		return wrapDB(err, "could not clear the product's tags: %s", productID)
	}
	for _, tagID := range tagIDs {
		err := r.q.AddProductTag(ctx, productdb.AddProductTagParams{ProductID: productID, TagID: tagID})
		if err != nil {
			return wrapDB(err, "could not bind tag to product (%s -> %s)", productID, tagID)
		}
	}
	return nil
}

// SetProductCategories replaces the product's categories with the given set.
func (r *Repo) SetProductCategories(ctx context.Context, productID string, categoryIDs []string) error {
	if err := r.q.DeleteProductCategories(ctx, productID); err != nil {
		return wrapDB(err, "could not clear the product's categories: %s", productID)
	}
	for _, categoryID := range categoryIDs {
		err := r.q.AddProductCategory(ctx, productdb.AddProductCategoryParams{
			ProductID:  productID,
			CategoryID: categoryID,
		})
		if err != nil {
			return wrapDB(err, "could not bind category to product (%s -> %s)", productID, categoryID)
		}
	}
	return nil
}

// ListTagsByProductIDs returns the tags of the given products in a SINGLE
// query.
func (r *Repo) ListTagsByProductIDs(ctx context.Context, productIDs []string) (map[string][]models.Tag, error) {
	if len(productIDs) == 0 {
		return map[string][]models.Tag{}, nil
	}
	rows, err := r.q.ListTagsByProductIDs(ctx, productIDs)
	if err != nil {
		return nil, wrapDB(err, "could not read the products' tags (%d products)", len(productIDs))
	}

	out := make(map[string][]models.Tag, len(productIDs))
	for _, row := range rows {
		out[row.ProductID] = append(out[row.ProductID], models.Tag{ID: row.ID, Value: row.Value})
	}
	return out, nil
}

// ListCategoriesByProductIDs returns the categories of the given products in a
// SINGLE query.
func (r *Repo) ListCategoriesByProductIDs(ctx context.Context, productIDs []string) (map[string][]models.Category, error) {
	if len(productIDs) == 0 {
		return map[string][]models.Category{}, nil
	}
	rows, err := r.q.ListCategoriesByProductIDs(ctx, productIDs)
	if err != nil {
		return nil, wrapDB(err, "could not read the products' categories (%d products)", len(productIDs))
	}

	out := make(map[string][]models.Category, len(productIDs))
	for _, row := range rows {
		out[row.ProductID] = append(out[row.ProductID], models.Category{
			ID:       row.ID,
			Name:     row.Name,
			Handle:   row.Handle,
			ParentID: row.ParentID,
			Rank:     row.Rank,
		})
	}
	return out, nil
}
