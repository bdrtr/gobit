package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository/productdb"
)

// SetProductSchedule replaces a product's schedule: the moment it is published
// and the moment it is archived, either of which may be nil (ADR 0177, ADR 0179).
// The service decides which product may carry which moment; the constraints
// refuse a pair that breaks the same rules.
func (r *Repo) SetProductSchedule(
	ctx context.Context, id string, publishAt, archiveAt *time.Time,
) (models.Product, error) {
	row, err := r.q.SetProductSchedule(ctx, productdb.SetProductScheduleParams{
		ID:        id,
		PublishAt: moment(publishAt),
		ArchiveAt: moment(archiveAt),
	})
	if err != nil {
		return models.Product{}, wrapDB(err, "could not schedule the product: %s", id)
	}

	return toProduct(row)
}

// ClearProductSchedule takes the whole schedule off a product.
func (r *Repo) ClearProductSchedule(ctx context.Context, id string) (models.Product, error) {
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

// ArchiveDueProducts archives at most limit products whose moment to leave is at
// or before due, and returns their ids.
func (r *Repo) ArchiveDueProducts(ctx context.Context, due time.Time, limit int64) ([]string, error) {
	ids, err := r.q.ArchiveDueProducts(ctx, productdb.ArchiveDueProductsParams{
		Due:      pgtype.Timestamptz{Time: due, Valid: true},
		RowLimit: limit,
	})
	if err != nil {
		return nil, wrapDB(err, "could not archive the scheduled products")
	}

	return ids, nil
}

// moment turns an optional moment into the column's value.
func moment(at *time.Time) pgtype.Timestamptz {
	if at == nil {
		return pgtype.Timestamptz{}
	}

	return pgtype.Timestamptz{Time: *at, Valid: true}
}
