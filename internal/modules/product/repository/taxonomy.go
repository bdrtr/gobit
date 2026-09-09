package repository

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/core/errors"
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

// SoftDeleteCollection deletes the collection (stamps deleted_at).
//
// A record that is already deleted or that never existed returns
// errors.NotFound, for the reason [Repo.SoftDeleteProduct] gives.
func (r *Repo) SoftDeleteCollection(ctx context.Context, id string) error {
	n, err := r.q.SoftDeleteCollection(ctx, id)
	if err != nil {
		return wrapDB(err, "could not delete collection: %s", id)
	}
	if n == 0 {
		return notFound("collection", id)
	}
	return nil
}

// ClearCollectionProducts releases the products bound to the collection and
// returns how many were released.
//
// The count is returned rather than dropped because it is what the caller logs:
// deleting a collection can silently change many products, and a number nobody
// can see is a change nobody can audit.
func (r *Repo) ClearCollectionProducts(ctx context.Context, collectionID string) (int, error) {
	n, err := r.q.ClearCollectionProducts(ctx, &collectionID)
	if err != nil {
		return 0, wrapDB(err, "could not release the collection's products: %s", collectionID)
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

// UpdateCategory writes the changed fields and returns the category as stored.
//
// # What a statement matching NO ROW means here
//
// Two things, and the caller has to have ruled out the first: the id names no
// live category, or the reparent was REFUSED. The refusal is the statement's
// own guard — it walks up from the new parent and will not let a category
// become its own descendant, and it also refuses an ancestry too deep to
// verify. [github.com/bdrtr/gobit/internal/modules/product/service.Service.UpdateCategory]
// resolves the id before calling, so what arrives here is the second, and it is
// reported as an INVALID request rather than as a missing record.
//
// # The statement's guard is not enough on its own, and this is what was
// measured
//
// The guard lives IN the statement because a read followed by a write has a
// window. That closes the window for two writes to the SAME row, and not for
// two writes to different ones: under READ COMMITTED each UPDATE's recursive
// walk reads a snapshot taken when that statement started, so "move A under B"
// and "move B under A" running together each see a tree with no ring, each
// touch a different row, take no lock from one another, and close the ring
// between them. It was reproduced — one run in three on a laptop, and on CI
// (D46).
//
// So a reparent takes an advisory lock first and the two halves are one
// decision: the lock makes the second mover WAIT, and the statement's guard is
// what then refuses it, because after the wait its walk sees what the first
// one committed.
func (r *Repo) UpdateCategory(ctx context.Context, id string, in UpdateCategory) (models.Category, error) {
	if in.ParentID == nil {
		// Nothing that can close a ring: a rename, a rank, a flag, or clearing
		// the parent — which makes a root and can only ever REMOVE an edge.
		// Serializing those would make every category edit queue behind every
		// other one for a race they cannot take part in.
		return r.updateCategoryRow(ctx, id, in)
	}

	var out models.Category
	err := r.inTx(ctx, func(ctx context.Context, repo *Repo) error {
		if _, err := repo.db.Exec(ctx, advisoryLockSQL, categoryReparentLockKey); err != nil {
			return wrapDB(err, "could not take the category reparent lock")
		}

		var updateErr error
		out, updateErr = repo.updateCategoryRow(ctx, id, in)

		return updateErr
	})
	if err != nil {
		return models.Category{}, err
	}

	return out, nil
}

// advisoryLockSQL takes the transaction-lifetime advisory lock.
//
// It is not generated through sqlc and runs on the transaction handle directly:
// the query touches none of this module's tables and carries no schema
// information — it is a concurrency primitive.
const advisoryLockSQL = `SELECT pg_advisory_xact_lock($1)`

// categoryReparentLockClass is the CLASS number of the reparent lock, written
// into the upper 32 bits of the key.
//
// # Why one key for the whole tree
//
// A cycle is a property of a PATH, not of a pair, so locking the two rows a
// move names is not enough: a third move elsewhere on the path can close the
// ring with them. The tree is what has to be serialized, and there is one tree.
// The cost is that two reparents never run at the same time, which is the
// right trade for an operator action that happens by hand.
//
// # The key space is shared by the whole database
//
// The upper 32 bits are a CLASS number, the convention the order module's
// spending lock introduced (class 1); this is class 3, because 2 is the job
// scheduler's and 0 is golang-migrate's. Without the class, two unrelated locks
// that happened to pick the same number would hold each other up, and neither
// side would have any way to notice — which is what happened on the way in
// (D47), and what internal/arch/advisory_lock_test.go now refuses.
const categoryReparentLockClass int64 = 3

// categoryReparentLockKey is the key every category reparent serializes on.
//
// The lower 32 bits are zero because there is ONE tree: unlike the spending
// lock, which keys on a customer, there is nothing here to tell two locks
// apart.
const categoryReparentLockKey int64 = categoryReparentLockClass << 32

// updateCategoryRow runs the update statement itself.
func (r *Repo) updateCategoryRow(
	ctx context.Context, id string, in UpdateCategory,
) (models.Category, error) {
	row, err := r.q.UpdateCategory(ctx, productdb.UpdateCategoryParams{
		ID:          id,
		Name:        in.Name,
		Handle:      in.Handle,
		Description: in.Description,
		ClearParent: in.ClearParent,
		ParentID:    in.ParentID,
		IsActive:    in.IsActive,
		IsInternal:  in.IsInternal,
		Rank:        in.Rank,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Category{}, errors.Wrap(err, errors.KindInvalid, codeCategoryCycle,
			"the category (%s) could not be moved: the new parent is the category itself "+
				"or one of its descendants, or the tree above it is too deep to verify", id)
	}
	if err != nil {
		return models.Category{}, wrapDB(err, "could not update category (%s)", id)
	}
	return toCategory(row), nil
}

// UpdateCategory is the set of fields a category update may change.
//
// Every pointer is "leave it alone" when nil. ClearParent is a bool rather than
// a third state on ParentID because nil already means "do not touch", and
// "make this a root" needs a way to say itself.
type UpdateCategory struct {
	Name        *string
	Handle      *string
	Description *string
	ParentID    *string
	ClearParent bool
	IsActive    *bool
	IsInternal  *bool
	Rank        *int32
}

// GetCategory returns the category by id.
func (r *Repo) GetCategory(ctx context.Context, id string) (models.Category, error) {
	row, err := r.q.GetCategory(ctx, id)
	if err != nil {
		return models.Category{}, wrapDB(err, "category not found: %s", id)
	}
	return toCategory(row), nil
}

// ListCategoriesByIDs returns the categories of the given ids in a SINGLE
// query.
//
// It is the batch counterpart of [Repo.GetCategory] and it exists for the read
// layer's category provider: an expansion, or a filter naming several ids, would
// otherwise turn into one round trip per id — the N+1 the Query layer is built
// to keep out (ADR 0004).
//
// An id that is not found produces NO row and that is not an error: the caller
// asked "which of these do you have", and a deleted category is a valid answer
// of "not this one". The SQL applies the same deleted_at predicate as every
// other read, so a soft-deleted category cannot come back through this door
// while it is hidden at the others.
//
// The FLAGS ARE NOT applied here. is_active and is_internal narrow a LISTING
// (see [CategoryFilter].PublicOnly); this method answers about named ids and
// the caller decides what to do with a switched-off one — the provider does
// exactly that.
func (r *Repo) ListCategoriesByIDs(ctx context.Context, ids []string) ([]models.Category, error) {
	if len(ids) == 0 {
		return []models.Category{}, nil
	}

	rows, err := r.q.ListCategoriesByIDs(ctx, ids)
	if err != nil {
		return nil, wrapDB(err, "could not read the categories (%d ids)", len(ids))
	}

	out := make([]models.Category, 0, len(rows))
	for i := range rows {
		out = append(out, toCategory(rows[i]))
	}
	return out, nil
}

// CategoryFilter is the criteria of a category listing.
//
// It is a struct rather than three parameters for the reason ProductFilter is:
// the listing and the COUNT have to apply the same predicate, and two argument
// lists that must stay in step are two places to forget one.
type CategoryFilter struct {
	// ParentID lists only the children of that node.
	ParentID *string
	// PublicOnly hides the categories the merchant has switched off (is_active)
	// or marked as operator-only (is_internal).
	//
	// It is false for the admin surface ON PURPOSE: a merchant who cannot see a
	// switched-off category has no way to switch it back on.
	PublicOnly bool
	Limit      int
	Offset     int
}

// ListCategories returns the categories paginated; if ParentID is given only
// the children of that node are listed.
func (r *Repo) ListCategories(ctx context.Context, f CategoryFilter) ([]models.Category, error) {
	rows, err := r.q.ListCategories(ctx, productdb.ListCategoriesParams{
		ParentID:   f.ParentID,
		PublicOnly: f.PublicOnly,
		Lim:        toInt32(f.Limit),
		Off:        toInt32(f.Offset),
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

// CountCategories returns the total number of categories matching the filter.
func (r *Repo) CountCategories(ctx context.Context, f CategoryFilter) (int, error) {
	n, err := r.q.CountCategories(ctx, productdb.CountCategoriesParams{
		ParentID:   f.ParentID,
		PublicOnly: f.PublicOnly,
	})
	if err != nil {
		return 0, wrapDB(err, "could not read category count")
	}
	return int(n), nil
}

// CountChildCategories returns the number of live children of the category.
func (r *Repo) CountChildCategories(ctx context.Context, id string) (int, error) {
	n, err := r.q.CountChildCategories(ctx, &id)
	if err != nil {
		return 0, wrapDB(err, "could not read the category's child count: %s", id)
	}
	return int(n), nil
}

// SoftDeleteCategory deletes the category (stamps deleted_at).
//
// The child check is NOT here: it belongs to the service, which is where the
// two statements are ordered inside one transaction.
func (r *Repo) SoftDeleteCategory(ctx context.Context, id string) error {
	n, err := r.q.SoftDeleteCategory(ctx, id)
	if err != nil {
		return wrapDB(err, "could not delete category: %s", id)
	}
	if n == 0 {
		return notFound("category", id)
	}
	return nil
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

// SoftDeleteTag deletes the tag (stamps deleted_at).
//
// The bindings in product_tag_map stay; see the SQL for why.
func (r *Repo) SoftDeleteTag(ctx context.Context, id string) error {
	n, err := r.q.SoftDeleteTag(ctx, id)
	if err != nil {
		return wrapDB(err, "could not delete tag: %s", id)
	}
	if n == 0 {
		return notFound("tag", id)
	}
	return nil
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

// CreateProductType writes a new product type.
func (r *Repo) CreateProductType(
	ctx context.Context, t models.ProductType,
) (models.ProductType, error) {
	meta, err := fromMetadata(t.Metadata)
	if err != nil {
		return models.ProductType{}, err
	}

	row, err := r.q.CreateProductType(ctx, productdb.CreateProductTypeParams{
		ID:       t.ID,
		Value:    t.Value,
		Handle:   t.Handle,
		Metadata: meta,
	})
	if err != nil {
		return models.ProductType{}, wrapDB(err, "could not create product type (%s)", t.Handle)
	}

	return toProductType(row)
}

// GetProductType returns the product type by id.
func (r *Repo) GetProductType(ctx context.Context, id string) (models.ProductType, error) {
	row, err := r.q.GetProductType(ctx, id)
	if err != nil {
		return models.ProductType{}, wrapDB(err, "product type not found: %s", id)
	}

	return toProductType(row)
}

// ListProductTypes returns the product types paginated.
func (r *Repo) ListProductTypes(
	ctx context.Context, limit, offset int,
) ([]models.ProductType, error) {
	rows, err := r.q.ListProductTypes(ctx, productdb.ListProductTypesParams{
		Lim: toInt32(limit),
		Off: toInt32(offset),
	})
	if err != nil {
		return nil, wrapDB(err, "could not list product types")
	}

	out := make([]models.ProductType, 0, len(rows))
	for i := range rows {
		t, convErr := toProductType(rows[i])
		if convErr != nil {
			return nil, convErr
		}
		out = append(out, t)
	}

	return out, nil
}

// CountProductTypes returns the total number of product types.
func (r *Repo) CountProductTypes(ctx context.Context) (int, error) {
	n, err := r.q.CountProductTypes(ctx)
	if err != nil {
		return 0, wrapDB(err, "could not read product type count")
	}

	return int(n), nil
}

// SoftDeleteProductType deletes the type (stamps deleted_at).
//
// A record that is already deleted or that never existed returns
// errors.NotFound, for the reason [Repo.SoftDeleteProduct] gives.
func (r *Repo) SoftDeleteProductType(ctx context.Context, id string) error {
	n, err := r.q.SoftDeleteProductType(ctx, id)
	if err != nil {
		return wrapDB(err, "could not delete product type: %s", id)
	}
	if n == 0 {
		return notFound("product type", id)
	}

	return nil
}

// ClearProductTypeProducts releases the products bound to the type and returns
// how many were released.
//
// The count is returned for the reason [Repo.ClearCollectionProducts] gives,
// and it matters more here: a product's type decides which tax rate rule
// matches it, so releasing one silently changes what a shop charges.
func (r *Repo) ClearProductTypeProducts(ctx context.Context, typeID string) (int, error) {
	n, err := r.q.ClearProductTypeProducts(ctx, &typeID)
	if err != nil {
		return 0, wrapDB(err, "could not release the type's products: %s", typeID)
	}

	return int(n), nil
}
