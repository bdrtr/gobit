package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bdrtr/gobit/internal/modules/settings/models"
	"github.com/bdrtr/gobit/internal/modules/settings/repository/settingsdb"
)

// ReviseProfile writes the profile while it is still the one the caller read,
// named by the moment it was last written, or, read as never written, while
// there still is none; and reports whether it did (ADR 0336).
func (r *Repository) ReviseProfile(
	ctx context.Context, readUpdatedAt *time.Time, profile models.StoreProfile,
) (models.StoreProfile, bool, error) {
	var row settingsdb.StoreProfile
	var err error
	if readUpdatedAt == nil {
		row, err = r.q.CreateStoreProfile(ctx, settingsdb.CreateStoreProfileParams{
			ID: models.ProfileID, LegalName: profile.LegalName, TaxNumber: profile.TaxNumber,
			TaxOffice: profile.TaxOffice, Email: profile.Email, Address: profile.Address,
			CountryCode: profile.CountryCode,
		})
	} else {
		row, err = r.q.ReviseStoreProfile(ctx, settingsdb.ReviseStoreProfileParams{
			ID: models.ProfileID, LegalName: profile.LegalName, TaxNumber: profile.TaxNumber,
			TaxOffice: profile.TaxOffice, Email: profile.Email, Address: profile.Address,
			CountryCode: profile.CountryCode, ReadUpdatedAt: pgtype.Timestamptz{Time: *readUpdatedAt, Valid: true},
		})
	}
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return models.StoreProfile{}, false, nil
	case err != nil:
		return models.StoreProfile{}, false, classify(err)
	}

	return toProfile(row), true, nil
}
