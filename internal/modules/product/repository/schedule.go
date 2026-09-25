package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository/productdb"
)

// ScheduleProductPublication sets the moment a DRAFT is to be published (ADR
// 0177). A product that is missing or is not a draft matches no row and comes
// back as not found; the service reads the product first and tells the two apart.
func (r *Repo) ScheduleProductPublication(ctx context.Context, id string, at time.Time) (models.Product, error) {
	row, err := r.q.ScheduleProductPublication(ctx, productdb.ScheduleProductPublicationParams{
		ID:        id,
		PublishAt: pgtype.Timestamptz{Time: at, Valid: true},
	})
	if err != nil {
		return models.Product{}, wrapDB(err, "could not schedule the product: %s", id)
	}

	return toProduct(row)
}

// CancelProductPublication takes the schedule off a product.
func (r *Repo) CancelProductPublication(ctx context.Context, id string) (models.Product, error) {
	row, err := r.q.CancelProductPublication(ctx, id)
	if err != nil {
		return models.Product{}, wrapDB(err, "could not take the schedule off the product: %s", id)
	}

	return toProduct(row)
}

// PublishDueProducts publishes at most limit drafts whose moment is at or before
// due, and returns their ids.
func (r *Repo) PublishDueProducts(ctx context.Context, due time.Time, limit int64) ([]string, error) {
	ids, err := r.q.PublishDueProducts(ctx, productdb.PublishDueProductsParams{
		Due:      pgtype.Timestamptz{Time: due, Valid: true},
		RowLimit: limit,
	})
	if err != nil {
		return nil, wrapDB(err, "could not publish the scheduled products")
	}

	return ids, nil
}
