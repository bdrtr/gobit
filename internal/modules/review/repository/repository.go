// Package repository is the review module's data access layer.
//
// # Conversion stays here
//
// pgtype and the generated row types DO NOT LEAVE this package: the service
// speaks in domain models. The boundary is what keeps a database detail — a
// nullable column, a numeric type — from becoming a fact the whole module has
// to know about.
//
// # The storefront's reads have their own methods
//
// [Repository.ListApproved] and [Repository.Summarize] do not take a status and
// cannot be given one: their SQL carries the literal. A single listing method
// with a status parameter would put the module's whole guarantee behind one
// correctly-set argument, and the caller that forgot it would publish every
// review ever submitted while every test still passed.
package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/review/models"
	"github.com/bdrtr/gobit/internal/modules/review/repository/reviewdb"
)

// Error codes.
const (
	codeQueryFailed = "review_query_failed"
	codeNotFound    = "review_not_found"
	codeConflict    = "review_conflict"
)

// Repository reads and writes the review module's table.
type Repository struct {
	pool *pgxpool.Pool
}

// New builds a repository over the given pool.
func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// queries returns the query set bound to the pool.
//
// There is no transaction plumbing here, unlike the invoice module's
// repository, and its absence is deliberate rather than unfinished: every write
// this module makes is a SINGLE statement — one INSERT for a submission, one
// conditional UPDATE for a moderation — so there is no pair of statements that
// would have to commit or roll back together. A WithTx that nothing needed
// would be a mechanism whose correctness nobody could check.
func (r *Repository) queries() *reviewdb.Queries { return reviewdb.New(r.pool) }

// Create writes a submitted review.
func (r *Repository) Create(ctx context.Context, in models.Review) (models.Review, error) {
	row, err := r.queries().CreateReview(ctx, reviewdb.CreateReviewParams{
		ID:         in.ID,
		ProductID:  in.ProductID,
		Rating:     in.Rating,
		Title:      in.Title,
		Body:       in.Body,
		AuthorName: in.AuthorName,
		Status:     in.Status.String(),
	})
	if err != nil {
		return models.Review{}, wrapDB(err, codeQueryFailed, "the review could not be written")
	}

	return toReview(row), nil
}

// Get returns one review whatever its status.
//
// It is the ADMIN read and the service's own pre-check before a moderation; the
// storefront never reaches it, because a shopper holding a review id must not
// be able to read a review that was rejected or is still waiting.
func (r *Repository) Get(ctx context.Context, id string) (models.Review, error) {
	row, err := r.queries().GetReview(ctx, id)
	if err != nil {
		return models.Review{}, wrapDB(err, codeNotFound, "the review could not be read")
	}

	return toReview(row), nil
}

// Moderate moves the review and returns the row it wrote.
//
// The move is decided by the DATABASE: the update carries the status the caller
// believed the review was in, so two operators acting at the same moment cannot
// both win. A move that matched no row comes back as a conflict rather than as
// "not found", because the review does exist — it simply is not where the
// caller thought.
func (r *Repository) Moderate(
	ctx context.Context, id string, from, to models.Status, note string,
) (models.Review, error) {
	row, err := r.queries().ModerateReview(ctx, reviewdb.ModerateReviewParams{
		ID:             id,
		CurrentStatus:  from.String(),
		NextStatus:     to.String(),
		ModerationNote: note,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Review{}, coreerrors.Conflict(codeConflict,
				"review %s is no longer in status %q, so it cannot be moved to %q", id, from, to)
		}

		return models.Review{}, wrapDB(err, codeQueryFailed, "the review could not be moved")
	}

	return toReview(row), nil
}

// List pages the reviews for the ADMIN surface and returns the matching count.
func (r *Repository) List(
	ctx context.Context, filter models.Filter,
) ([]models.Review, int64, error) {
	// The cursor arrives as SQL NULL when it names no position; the COALESCE
	// sentinels in the query turn that into "start at the top". A zero TIME sent
	// instead would make the first page come back empty with no error anywhere.
	afterAt, afterID := cursorBounds(filter.After)

	rows, err := r.queries().ListReviews(ctx, reviewdb.ListReviewsParams{
		Status:    filter.Status,
		ProductID: filter.ProductID,
		AfterAt:   afterAt,
		AfterID:   afterID,
		RowLimit:  filter.Limit,
		RowOffset: filter.Offset,
	})
	if err != nil {
		return nil, 0, wrapDB(err, codeQueryFailed, "the reviews could not be listed")
	}

	total, err := r.queries().CountReviews(ctx, reviewdb.CountReviewsParams{
		Status:    filter.Status,
		ProductID: filter.ProductID,
	})
	if err != nil {
		return nil, 0, wrapDB(err, codeQueryFailed, "the reviews could not be counted")
	}

	return toReviews(rows), total, nil
}

// ListApproved pages the reviews of one product that a STOREFRONT may see.
//
// The status is not a parameter here and cannot become one: the query carries
// the literal. See the package doc for why the two listings are separate.
func (r *Repository) ListApproved(
	ctx context.Context, productID string, filter models.Filter,
) ([]models.Review, int64, error) {
	afterAt, afterID := cursorBounds(filter.After)

	rows, err := r.queries().ListApprovedReviews(ctx, reviewdb.ListApprovedReviewsParams{
		ProductID: productID,
		AfterAt:   afterAt,
		AfterID:   afterID,
		RowLimit:  filter.Limit,
		RowOffset: filter.Offset,
	})
	if err != nil {
		return nil, 0, wrapDB(err, codeQueryFailed, "the reviews could not be listed")
	}

	total, err := r.queries().CountApprovedReviews(ctx, productID)
	if err != nil {
		return nil, 0, wrapDB(err, codeQueryFailed, "the reviews could not be counted")
	}

	return toReviews(rows), total, nil
}

// Summarize returns the count and the average of a product's approved reviews.
func (r *Repository) Summarize(ctx context.Context, productID string) (models.Summary, error) {
	row, err := r.queries().SummarizeApprovedReviews(ctx, productID)
	if err != nil {
		return models.Summary{}, wrapDB(err, codeQueryFailed,
			"the review summary could not be read")
	}

	return models.Summary{
		ProductID:         productID,
		Count:             row.ReviewCount,
		AverageHundredths: row.AverageHundredths,
	}, nil
}
