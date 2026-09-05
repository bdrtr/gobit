package repository

import (
	"context"

	corepage "github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository/productdb"
)

// ProductFilter holds the criteria of the product listing.
//
// A nil field means "do not apply this criterion"; the distinction between the
// zero value and "not given" is kept with a pointer (an empty string is not a
// valid handle, but in the search text an empty string would match everything).
type ProductFilter struct {
	Status       *string
	CollectionID *string
	Handle       *string
	Search       *string
	// SalesChannelIDs are the sales channels the request is bound to.
	//
	// Here nil and an EMPTY BUT NON-nil slice say DIFFERENT things, and the
	// distinction is kept not with a pointer but with the slice itself (the
	// same distinction exists in ProductPatch too): nil means "the request
	// carries no channel id, do not filter"; an empty slice means "there is an
	// identity but it has no channel at all" and the filter is applied. Had
	// the two been treated as one, an identity with no channel would read the
	// catalog of ALL channels.
	//
	// For the rule itself and for why it is applied in the database see
	// saleschannel.go.
	SalesChannelIDs []string
	Limit           int
	Offset          int
	// After is the keyset position the page starts below; the zero value is the
	// first page.
	//
	// It is applied TOGETHER with Offset rather than instead of it, so the query
	// keeps a single shape. The API refuses the two at once, because a cursor
	// and an offset name two different positions and honoring both would serve a
	// page that neither of them asked for.
	After corepage.Cursor
}

// ProductPatch is a partial update of a product.
//
// A nil field IS NOT CHANGED. This is the direct counterpart of the PATCH
// contract: a field that is not present in the body is preserved.
type ProductPatch struct {
	Handle        *string
	Title         *string
	Subtitle      *string
	Description   *string
	Thumbnail     *string
	Status        *string
	Discountable  *bool
	Weight        *int32
	Length        *int32
	Height        *int32
	Width         *int32
	Material      *string
	OriginCountry *string
	CollectionID  *string
	Metadata      map[string]any
}

// CreateProduct writes a new product row and returns it as it was written.
//
// The timestamps are produced by the database; the returned record is
// therefore not what the caller sent but what is STORED.
func (r *Repo) CreateProduct(ctx context.Context, p models.Product) (models.Product, error) {
	meta, err := fromMetadata(p.Metadata)
	if err != nil {
		return models.Product{}, err
	}

	row, err := r.q.CreateProduct(ctx, productdb.CreateProductParams{
		ID:            p.ID,
		Handle:        p.Handle,
		Title:         p.Title,
		Subtitle:      p.Subtitle,
		Description:   p.Description,
		Thumbnail:     p.Thumbnail,
		Status:        string(p.Status),
		IsGiftcard:    p.IsGiftcard,
		Discountable:  p.Discountable,
		Weight:        p.Weight,
		Length:        p.Length,
		Height:        p.Height,
		Width:         p.Width,
		Material:      p.Material,
		OriginCountry: p.OriginCountry,
		CollectionID:  p.CollectionID,
		Metadata:      meta,
	})
	if err != nil {
		return models.Product{}, wrapDB(err, "could not create product (%s)", p.Handle)
	}
	return toProduct(row)
}

// GetProduct returns the product by id; a deleted record counts as not found.
func (r *Repo) GetProduct(ctx context.Context, id string) (models.Product, error) {
	row, err := r.q.GetProduct(ctx, id)
	if err != nil {
		return models.Product{}, wrapDB(err, "product not found: %s", id)
	}
	return toProduct(row)
}

// GetProductForUpdate reads the product WITH A ROW LOCK; a deleted record
// counts as not found.
//
// It is meaningful ONLY inside [Store.InTx]: the lock is released at the end of
// the transaction, and in a call without a transaction the row is freed as soon
// as the read finishes.
//
// The reason the existence check is done with a lock is soft delete: the
// foreign key on product_variant still sees the row of a deleted product, so a
// delete slipping in between the check and the INSERT would leave a variant
// whose owner is deleted. The lock puts the two operations in order.
func (r *Repo) GetProductForUpdate(ctx context.Context, id string) (models.Product, error) {
	row, err := r.q.GetProductForUpdate(ctx, id)
	if err != nil {
		return models.Product{}, wrapDB(err, "product not found: %s", id)
	}
	return toProduct(row)
}

// GetProductByHandle returns the product by handle.
func (r *Repo) GetProductByHandle(ctx context.Context, handle string) (models.Product, error) {
	row, err := r.q.GetProductByHandle(ctx, handle)
	if err != nil {
		return models.Product{}, wrapDB(err, "product not found (handle: %s)", handle)
	}
	return toProduct(row)
}

// ListProductsByIDs returns the products of the given ids in a SINGLE query.
//
// The Query provider's FetchByIDs uses this; for an id that is not found no
// record is returned and that is not an error (ADR 0004).
func (r *Repo) ListProductsByIDs(ctx context.Context, ids []string) ([]models.Product, error) {
	if len(ids) == 0 {
		return []models.Product{}, nil
	}
	rows, err := r.q.ListProductsByIDs(ctx, ids)
	if err != nil {
		return nil, wrapDB(err, "could not read products by id (%d ids)", len(ids))
	}
	return toProducts(rows)
}

// UpdateProduct updates the product partially and returns its current state.
func (r *Repo) UpdateProduct(ctx context.Context, id string, patch ProductPatch) (models.Product, error) {
	meta, err := patchMetadata(patch.Metadata)
	if err != nil {
		return models.Product{}, err
	}

	row, err := r.q.UpdateProduct(ctx, productdb.UpdateProductParams{
		ID:            id,
		Handle:        patch.Handle,
		Title:         patch.Title,
		Subtitle:      patch.Subtitle,
		Description:   patch.Description,
		Thumbnail:     patch.Thumbnail,
		Status:        patch.Status,
		Discountable:  patch.Discountable,
		Weight:        patch.Weight,
		Length:        patch.Length,
		Height:        patch.Height,
		Width:         patch.Width,
		Material:      patch.Material,
		OriginCountry: patch.OriginCountry,
		CollectionID:  patch.CollectionID,
		Metadata:      meta,
	})
	if err != nil {
		return models.Product{}, wrapDB(err, "could not update product: %s", id)
	}
	return toProduct(row)
}

// SoftDeleteProduct deletes the product (stamps deleted_at).
//
// A record that is already deleted or that never existed returns
// errors.NotFound: having the delete call silently look successful would hide
// that the client is working with the wrong id.
func (r *Repo) SoftDeleteProduct(ctx context.Context, id string) error {
	n, err := r.q.SoftDeleteProduct(ctx, id)
	if err != nil {
		return wrapDB(err, "could not delete product: %s", id)
	}
	if n == 0 {
		return notFound("product", id)
	}
	return nil
}

// SoftDeleteProductChildren deletes the product's variants, options and
// images.
//
// Why the delete is not left to the database CASCADE: CASCADE REALLY deletes
// the row, whereas the delete here is SOFT and leaves a trace of the record.
// The order matters as well; that is why the caller wraps it in a single
// transaction (InTx).
func (r *Repo) SoftDeleteProductChildren(ctx context.Context, productID string) error {
	if _, err := r.q.SoftDeleteVariantsByProduct(ctx, productID); err != nil {
		return wrapDB(err, "could not delete the product's variants: %s", productID)
	}
	if _, err := r.q.SoftDeleteOptionsByProduct(ctx, productID); err != nil {
		return wrapDB(err, "could not delete the product's options: %s", productID)
	}
	if err := r.q.DeleteImagesByProduct(ctx, productID); err != nil {
		return wrapDB(err, "could not delete the product's images: %s", productID)
	}
	return nil
}

// ListVariantIDsByProduct returns the product's variant ids that are not
// deleted.
func (r *Repo) ListVariantIDsByProduct(ctx context.Context, productID string) ([]string, error) {
	ids, err := r.q.ListVariantIDsByProduct(ctx, productID)
	if err != nil {
		return nil, wrapDB(err, "could not read the product's variant ids: %s", productID)
	}
	return ids, nil
}

// CreateImage adds an image to the product.
func (r *Repo) CreateImage(ctx context.Context, img models.Image) (models.Image, error) {
	meta, err := fromMetadata(img.Metadata)
	if err != nil {
		return models.Image{}, err
	}

	row, err := r.q.CreateImage(ctx, productdb.CreateImageParams{
		ID:        img.ID,
		ProductID: img.ProductID,
		Url:       img.URL,
		Rank:      img.Rank,
		Metadata:  meta,
	})
	if err != nil {
		return models.Image{}, wrapDB(err, "could not add product image: %s", img.ProductID)
	}
	return toImage(row)
}

// ListImagesByProductIDs returns the images of the given products in a SINGLE
// query.
func (r *Repo) ListImagesByProductIDs(ctx context.Context, productIDs []string) (map[string][]models.Image, error) {
	if len(productIDs) == 0 {
		return map[string][]models.Image{}, nil
	}
	rows, err := r.q.ListImagesByProductIDs(ctx, productIDs)
	if err != nil {
		return nil, wrapDB(err, "could not read product images (%d products)", len(productIDs))
	}

	out := make(map[string][]models.Image, len(productIDs))
	for i := range rows {
		img, err := toImage(rows[i])
		if err != nil {
			return nil, err
		}
		out[rows[i].ProductID] = append(out[rows[i].ProductID], img)
	}
	return out, nil
}

// DeleteImagesByProduct deletes the product's images.
func (r *Repo) DeleteImagesByProduct(ctx context.Context, productID string) error {
	if err := r.q.DeleteImagesByProduct(ctx, productID); err != nil {
		return wrapDB(err, "could not delete the product's images: %s", productID)
	}
	return nil
}
