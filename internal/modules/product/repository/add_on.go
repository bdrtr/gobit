package repository

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/product/repository/productdb"
)

// ListProductAddOns returns the variants a product's lines may carry as
// add-ons, in the operator's order (ADR 0228).
func (r *Repo) ListProductAddOns(ctx context.Context, productID string) ([]string, error) {
	ids, err := r.q.ListProductAddOns(ctx, productID)
	if err != nil {
		return nil, wrapDB(err, "could not read the product's add-ons: %s", productID)
	}
	return ids, nil
}

// ReplaceProductAddOns writes a product's add-on list: the old one is deleted
// and the new one written with the position as the rank. It has to run inside a
// transaction, or a reader between the two statements sees an empty list.
func (r *Repo) ReplaceProductAddOns(ctx context.Context, productID string, variantIDs []string) error {
	if err := r.q.DeleteProductAddOns(ctx, productID); err != nil {
		return wrapDB(err, "could not clear the product's add-ons: %s", productID)
	}
	if len(variantIDs) == 0 {
		return nil
	}
	if err := r.q.InsertProductAddOns(ctx, productdb.InsertProductAddOnsParams{
		ProductID:  productID,
		VariantIds: variantIDs,
	}); err != nil {
		return wrapDB(err, "could not write the product's add-ons: %s", productID)
	}
	return nil
}
