// Package repository is the settings module's data access layer.
//
// It holds one record and therefore has one read and one write. The shape
// follows the other modules': the service speaks in domain models, this package
// speaks to sqlc, and the driver's errors are turned into typed ones here.
package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/settings/models"
	"github.com/bdrtr/gobit/internal/modules/settings/repository/settingsdb"
)

// Error codes this layer produces.
const (
	// codeProfileNotFound reports that the shop has not said who it is yet.
	codeProfileNotFound = "settings_store_profile_not_found"
	// codeQueryFailed reports a query that did not run.
	codeQueryFailed = "settings_query_failed"
)

// Repository reads and writes the store profile.
type Repository struct {
	q *settingsdb.Queries
}

// New builds a repository over the given pool.
func New(pool *pgxpool.Pool) *Repository {
	return &Repository{q: settingsdb.New(pool)}
}

// GetProfile returns the shop's profile; NotFound when it has not been written.
//
// The absence is a real answer rather than a zero value: a caller that received
// an empty profile would print an empty name, and the one thing a document must
// never carry is a seller who does not exist.
func (r *Repository) GetProfile(ctx context.Context) (models.StoreProfile, error) {
	row, err := r.q.GetStoreProfile(ctx, models.ProfileID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.StoreProfile{}, coreerrors.NotFound(codeProfileNotFound,
				"the shop has not said who it is yet; write the store profile first")
		}

		return models.StoreProfile{}, coreerrors.Wrap(err, coreerrors.KindInternal,
			codeQueryFailed, "the store profile could not be read")
	}

	return toProfile(row), nil
}

// SetProfile writes the profile, replacing what was there.
func (r *Repository) SetProfile(
	ctx context.Context, profile models.StoreProfile,
) (models.StoreProfile, error) {
	row, err := r.q.UpsertStoreProfile(ctx, settingsdb.UpsertStoreProfileParams{
		ID:          models.ProfileID,
		LegalName:   profile.LegalName,
		TaxNumber:   profile.TaxNumber,
		TaxOffice:   profile.TaxOffice,
		Email:       profile.Email,
		Address:     profile.Address,
		CountryCode: profile.CountryCode,
	})
	if err != nil {
		return models.StoreProfile{}, classify(err)
	}

	return toProfile(row), nil
}

// toProfile converts the generated row into the domain model.
func toProfile(row settingsdb.StoreProfile) models.StoreProfile {
	return models.StoreProfile{
		LegalName:   row.LegalName,
		TaxNumber:   row.TaxNumber,
		TaxOffice:   row.TaxOffice,
		Email:       row.Email,
		Address:     row.Address,
		CountryCode: row.CountryCode,
		CreatedAt:   row.CreatedAt.Time.UTC(),
		UpdatedAt:   row.UpdatedAt.Time.UTC(),
	}
}
