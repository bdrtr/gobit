package repository

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corepage "github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/review/models"
	"github.com/bdrtr/gobit/internal/modules/review/repository/reviewdb"
)

// wrapDB turns a driver error into the module's typed error.
//
// pgx.ErrNoRows becomes NOT FOUND and everything else becomes an internal
// fault: a missing row is an answer, a broken connection is not.
func wrapDB(err error, code, message string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return coreerrors.NotFound(code, "%s", message)
	}

	return coreerrors.Wrap(err, coreerrors.KindInternal, code, "%s", message)
}

// cursorBounds returns the keyset parameters, each null when the cursor names
// no position.
func cursorBounds(c corepage.Cursor) (at pgtype.Timestamptz, id *string) {
	if !c.Time.IsZero() {
		at = pgtype.Timestamptz{Time: c.Time, Valid: true}
	}
	if c.ID != "" {
		value := c.ID
		id = &value
	}

	return at, id
}

// toReview turns a review row into the domain model.
//
// moderated_at is nullable and an unmoderated review carries SQL NULL there.
// The zero Time is what the model uses for "not yet", and reading .Time off an
// invalid Timestamptz gives exactly that — but it is written out rather than
// relied on, because the two are only the same by the driver's convention and a
// reader should not have to know it.
func toReview(row reviewdb.Review) models.Review {
	out := models.Review{
		ID:             row.ID,
		ProductID:      row.ProductID,
		Rating:         row.Rating,
		Title:          row.Title,
		Body:           row.Body,
		AuthorName:     row.AuthorName,
		Status:         models.Status(row.Status),
		ModerationNote: row.ModerationNote,
		CreatedAt:      row.CreatedAt.Time,
		UpdatedAt:      row.UpdatedAt.Time,
	}
	if row.ModeratedAt.Valid {
		out.ModeratedAt = row.ModeratedAt.Time
	}

	return out
}

// toReviews converts a page of rows.
func toReviews(rows []reviewdb.Review) []models.Review {
	out := make([]models.Review, 0, len(rows))
	for i := range rows {
		out = append(out, toReview(rows[i]))
	}

	return out
}
