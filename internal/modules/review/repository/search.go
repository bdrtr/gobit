package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bdrtr/gobit/internal/modules/review/models"
	"github.com/bdrtr/gobit/internal/modules/review/repository/reviewdb"
)

// search is List for a filter carrying a search (ADR 0456): the same
// narrowings, the same order and the same count, over the reviews the search
// finds.
func (r *Repository) search(
	ctx context.Context, filter models.Filter, afterAt pgtype.Timestamptz, afterID *string,
) ([]models.Review, int64, error) {
	rows, err := r.queries().SearchReviews(ctx, reviewdb.SearchReviewsParams{
		Q:           *filter.Search,
		Status:      filter.Status,
		ProductID:   filter.ProductID,
		Suggested:   filter.Suggested,
		Unsuggested: filter.Unsuggested,
		AfterAt:     afterAt,
		AfterID:     afterID,
		RowLimit:    filter.Limit,
		RowOffset:   filter.Offset,
	})
	if err != nil {
		return nil, 0, wrapDB(err, codeQueryFailed, "the reviews could not be searched")
	}

	total, err := r.queries().CountSearchedReviews(ctx, reviewdb.CountSearchedReviewsParams{
		Q:           *filter.Search,
		Status:      filter.Status,
		ProductID:   filter.ProductID,
		Suggested:   filter.Suggested,
		Unsuggested: filter.Unsuggested,
	})
	if err != nil {
		return nil, 0, wrapDB(err, codeQueryFailed, "the reviews searched could not be counted")
	}

	return toReviews(rows), total, nil
}
