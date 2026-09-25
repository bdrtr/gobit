package repository

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository/productdb"
)

// ListProductRelations returns a product's relations: for every kind that has
// any, the related ids in the operator's order (ADR 0180).
func (r *Repo) ListProductRelations(ctx context.Context, productID string) (map[models.RelationType][]string, error) {
	rows, err := r.q.ListProductRelations(ctx, productID)
	if err != nil {
		return nil, wrapDB(err, "could not read the product's relations: %s", productID)
	}

	out := make(map[models.RelationType][]string)
	for _, row := range rows {
		kind := models.RelationType(row.Type)
		out[kind] = append(out[kind], row.RelatedProductID)
	}

	return out, nil
}

// ListProductRelationsOfType returns one kind of a product's relations in the
// operator's order.
func (r *Repo) ListProductRelationsOfType(
	ctx context.Context, productID string, kind models.RelationType,
) ([]string, error) {
	ids, err := r.q.ListProductRelationsOfType(ctx, productdb.ListProductRelationsOfTypeParams{
		ProductID: productID,
		Type:      string(kind),
	})
	if err != nil {
		return nil, wrapDB(err, "could not read the product's %s relations: %s", kind, productID)
	}

	return ids, nil
}

// ReplaceProductRelations writes one kind's list: the old one is deleted and
// the new one written with the position as the rank. It has to run inside a
// transaction, or a reader between the two statements sees an empty list.
func (r *Repo) ReplaceProductRelations(
	ctx context.Context, productID string, kind models.RelationType, relatedIDs []string,
) error {
	if err := r.q.DeleteProductRelationsOfType(ctx, productdb.DeleteProductRelationsOfTypeParams{
		ProductID: productID,
		Type:      string(kind),
	}); err != nil {
		return wrapDB(err, "could not clear the product's %s relations: %s", kind, productID)
	}
	if len(relatedIDs) == 0 {
		return nil
	}
	if err := r.q.InsertProductRelations(ctx, productdb.InsertProductRelationsParams{
		ProductID:  productID,
		Type:       string(kind),
		RelatedIds: relatedIDs,
	}); err != nil {
		return wrapDB(err, "could not write the product's %s relations: %s", kind, productID)
	}

	return nil
}
