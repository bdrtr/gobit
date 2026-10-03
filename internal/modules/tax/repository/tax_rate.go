package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
	"github.com/bdrtr/gobit/internal/modules/tax/repository/taxdb"
)

// CreateTaxRate adds a rate to a region.
//
// If the region does not exist a foreign key violation occurs and
// errors.Invalid is returned; an orphaned rate is structurally impossible. If
// the region already has a default rate a partial unique index violation
// occurs and errors.Conflict is returned.
func (r *Repo) CreateTaxRate(ctx context.Context, rate models.TaxRate, now time.Time) (models.TaxRate, error) {
	if err := r.ready(); err != nil {
		return models.TaxRate{}, err
	}

	metadata, err := fromJSONMap(rate.Metadata)
	if err != nil {
		return models.TaxRate{}, err
	}

	row, err := r.queries(ctx).InsertTaxRate(ctx, taxdb.InsertTaxRateParams{
		ID:          rate.ID,
		TaxRegionID: rate.TaxRegionID,
		Name:        rate.Name,
		Code:        optionalText(rate.RateCode()),
		RateBps:     rate.RateBps,
		IsDefault:   rate.IsDefault,
		StacksOnID:  rate.StacksOnID,
		Compound:    rate.Compound,
		Metadata:    metadata,
		CreatedAt:   fromTime(now),
	})
	if err != nil {
		return models.TaxRate{}, wrapDB(err, "the tax rate could not be inserted: %s", rate.TaxRegionID)
	}
	return toTaxRate(row)
}

// GetTaxRate returns the rate by id; errors.NotFound if there is none.
func (r *Repo) GetTaxRate(ctx context.Context, id string) (models.TaxRate, error) {
	if err := r.ready(); err != nil {
		return models.TaxRate{}, err
	}

	row, err := r.queries(ctx).GetTaxRate(ctx, id)
	if err != nil {
		return models.TaxRate{}, notFoundOr(err, CodeTaxRateNotFound, "tax rate not found: %s", id)
	}
	return toTaxRate(row)
}

// ListTaxRates returns a region's live rates; the default rate comes FIRST.
func (r *Repo) ListTaxRates(ctx context.Context, regionID string) ([]models.TaxRate, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	rows, err := r.queries(ctx).ListTaxRatesByRegion(ctx, regionID)
	if err != nil {
		return nil, wrapDB(err, "the tax rates could not be read: %s", regionID)
	}
	return toTaxRates(rows)
}

// ListTaxRatesByRegions returns the rates of several regions in a SINGLE query.
//
// It is the calculation path's way of reading: the region chain (province +
// country) is read in one round trip, with no separate query per region.
func (r *Repo) ListTaxRatesByRegions(ctx context.Context, regionIDs []string) ([]models.TaxRate, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(regionIDs) == 0 {
		return []models.TaxRate{}, nil
	}

	rows, err := r.queries(ctx).ListTaxRatesByRegions(ctx, regionIDs)
	if err != nil {
		return nil, wrapDB(err, "the tax rates could not be read")
	}
	return toTaxRates(rows)
}

// UpdateTaxRate updates the given fields of the rate UNDER A LOCK.
//
// The patch is applied on top of the row read under the lock: two concurrent
// updates without a lock could undo each other's field (lost update). The lock
// also makes the "does this rate have rules" check reliable — a rule insert
// slipping between the check and the write could make a ruled rate the
// default.
func (r *Repo) UpdateTaxRate(
	ctx context.Context,
	id string,
	patch models.TaxRatePatch,
	now time.Time,
) (models.TaxRate, error) {
	if err := r.ready(); err != nil {
		return models.TaxRate{}, err
	}

	var out models.TaxRate
	err := r.WithTx(ctx, func(ctx context.Context) error {
		q := r.queries(ctx)

		row, err := q.GetTaxRateForUpdate(ctx, id)
		if err != nil {
			return notFoundOr(err, CodeTaxRateNotFound, "tax rate not found: %s", id)
		}
		current, err := toTaxRate(row)
		if err != nil {
			return err
		}

		updated := current.Patched(patch)
		if updated.IsDefault && !current.IsDefault {
			// A rate made the default cannot have rules: had "a rate without
			// rules applies to everything" and "a ruled rate applies only to what
			// it matches" been combined in the same row, the rate's scope would
			// become unreadable.
			count, countErr := q.CountTaxRateRulesByRate(ctx, id)
			if countErr != nil {
				return wrapDB(countErr, "the tax rate's rules could not be counted: %s", id)
			}
			if count > 0 {
				return errors.Conflict(CodeConstraintViolation,
					"the rate %s has %d rules; a ruled rate cannot be made the default", id, count)
			}
		}

		metadata, err := fromJSONMap(updated.Metadata)
		if err != nil {
			return err
		}

		written, err := q.UpdateTaxRate(ctx, taxdb.UpdateTaxRateParams{
			ID:         id,
			Name:       updated.Name,
			Code:       optionalText(updated.RateCode()),
			RateBps:    updated.RateBps,
			IsDefault:  updated.IsDefault,
			StacksOnID: updated.StacksOnID,
			Compound:   updated.Compound,
			Metadata:   metadata,
			UpdatedAt:  fromTime(now),
		})
		if err != nil {
			return wrapDB(err, "the tax rate could not be updated: %s", id)
		}

		out, err = toTaxRate(written)
		return err
	})
	if err != nil {
		return models.TaxRate{}, err
	}
	return out, nil
}

// DeleteTaxRate soft-deletes the rate and its rules in a SINGLE transaction.
//
// The rules are deleted too: a live rule bound to a deleted rate enters no
// calculation, but it would collide in the uniqueness index with a new rule
// meant to be written for the same reference.
func (r *Repo) DeleteTaxRate(ctx context.Context, id string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	return r.WithTx(ctx, func(ctx context.Context) error {
		q := r.queries(ctx)

		if _, err := q.SoftDeleteTaxRate(ctx, taxdb.SoftDeleteTaxRateParams{
			ID:        id,
			DeletedAt: fromTime(now),
		}); err != nil {
			return notFoundOr(err, CodeTaxRateNotFound, "tax rate not found: %s", id)
		}

		if err := q.SoftDeleteTaxRateRulesByRates(ctx, taxdb.SoftDeleteTaxRateRulesByRatesParams{
			RateIds:   []string{id},
			DeletedAt: fromTime(now),
		}); err != nil {
			return wrapDB(err, "the tax rate's rules could not be deleted: %s", id)
		}
		return nil
	})
}

// toTaxRate turns a generated row into the domain model.
func toTaxRate(row taxdb.TaxRate) (models.TaxRate, error) {
	metadata, err := toJSONMap(row.Metadata)
	if err != nil {
		return models.TaxRate{}, err
	}
	return models.TaxRate{
		ID:          row.ID,
		TaxRegionID: row.TaxRegionID,
		Name:        row.Name,
		Code:        row.Code,
		RateBps:     row.RateBps,
		IsDefault:   row.IsDefault,
		StacksOnID:  row.StacksOnID,
		Compound:    row.Compound,
		Metadata:    metadata,
		CreatedAt:   toTime(row.CreatedAt),
		UpdatedAt:   toTime(row.UpdatedAt),
		DeletedAt:   toTimePtr(row.DeletedAt),
	}, nil
}

// toTaxRates turns a slice of rows into domain models.
func toTaxRates(rows []taxdb.TaxRate) ([]models.TaxRate, error) {
	out := make([]models.TaxRate, 0, len(rows))
	for i := range rows {
		rate, err := toTaxRate(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, rate)
	}
	return out, nil
}
