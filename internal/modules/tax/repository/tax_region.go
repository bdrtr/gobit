package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
	"github.com/bdrtr/gobit/internal/modules/tax/repository/taxdb"
)

// CreateTaxRegion writes a new tax region.
//
// If a second ROOT region is written for the same country a partial unique
// index violation occurs and errors.Conflict is returned. If a province
// region's country differs from its parent's country a composite foreign key
// violation occurs and errors.Invalid is returned; both are the last line of
// defense BEHIND the service's checks.
func (r *Repo) CreateTaxRegion(ctx context.Context, region models.TaxRegion, now time.Time) (models.TaxRegion, error) {
	if err := r.ready(); err != nil {
		return models.TaxRegion{}, err
	}

	metadata, err := fromJSONMap(region.Metadata)
	if err != nil {
		return models.TaxRegion{}, err
	}

	row, err := r.queries(ctx).InsertTaxRegion(ctx, taxdb.InsertTaxRegionParams{
		ID:           region.ID,
		CountryCode:  region.CountryCode,
		ProvinceCode: region.ProvinceCode,
		ParentID:     region.ParentID,
		ProviderID:   region.ProviderID,

		PricesIncludeTax: region.PricesIncludeTax,

		Metadata:  metadata,
		CreatedAt: fromTime(now),
	})
	if err != nil {
		return models.TaxRegion{}, wrapDB(err, "the tax region could not be inserted: %s/%s",
			region.CountryCode, region.Province())
	}
	return toTaxRegion(row)
}

// GetTaxRegion returns the region by id; errors.NotFound if there is none.
func (r *Repo) GetTaxRegion(ctx context.Context, id string) (models.TaxRegion, error) {
	if err := r.ready(); err != nil {
		return models.TaxRegion{}, err
	}

	row, err := r.queries(ctx).GetTaxRegion(ctx, id)
	if err != nil {
		return models.TaxRegion{}, notFoundOr(err, CodeTaxRegionNotFound,
			"tax region not found: %s", id)
	}
	return toTaxRegion(row)
}

// LockTaxRegion reads the region with a SHARED lock; the lock is held until the
// end of the transaction.
//
// It can be called ONLY inside [Repo.WithTx], and returns an error when called
// outside it: a FOR SHARE lock is released when the transaction ends, so a lock
// without a transaction protects nothing but is believed to protect something.
//
// Every flow that BINDS something to a region uses it — adding a province
// region and adding a rate. Both make the "is the region live" check and then
// write, and the check and the write have to be in the SAME transaction. The
// cost of a check made with the lock-free [Repo.GetTaxRegion] has been
// measured: a [Repo.DeleteTaxRegion] slipping in completes after the check,
// the write succeeds anyway, and a LIVE row bound to a deleted region is left
// behind. A foreign key cannot catch this, because the delete is SOFT: the row
// stays in place.
//
// The lock is SHARED: there is no reason for two concurrent rate inserts into
// the same region to wait for each other; the only flow that has to wait is
// the delete, and it takes an EXCLUSIVE lock.
func (r *Repo) LockTaxRegion(ctx context.Context, id string) (models.TaxRegion, error) {
	if err := r.ready(); err != nil {
		return models.TaxRegion{}, err
	}
	if err := requireTx(ctx, "LockTaxRegion"); err != nil {
		return models.TaxRegion{}, err
	}

	row, err := r.queries(ctx).GetTaxRegionForShare(ctx, id)
	if err != nil {
		return models.TaxRegion{}, notFoundOr(err, CodeTaxRegionNotFound,
			"tax region not found: %s", id)
	}
	return toTaxRegion(row)
}

// LockTaxRegionForWrite reads the region with an EXCLUSIVE lock held until the
// end of the transaction, and like [Repo.LockTaxRegion] it can be called only
// inside [Repo.WithTx].
//
// A change to a rate's value takes it (gap D237): a stack's cap is a sum over
// its members, and two writers raising two members under shared locks would
// each pass the check against the other's old value. The exclusive lock makes
// them wait for each other, and for a stack being built on the region.
func (r *Repo) LockTaxRegionForWrite(ctx context.Context, id string) (models.TaxRegion, error) {
	if err := r.ready(); err != nil {
		return models.TaxRegion{}, err
	}
	if err := requireTx(ctx, "LockTaxRegionForWrite"); err != nil {
		return models.TaxRegion{}, err
	}

	row, err := r.queries(ctx).GetTaxRegionForUpdate(ctx, id)
	if err != nil {
		return models.TaxRegion{}, notFoundOr(err, CodeTaxRegionNotFound,
			"tax region not found: %s", id)
	}
	return toTaxRegion(row)
}

// GetTaxRegionsByIDs returns the regions matching the given ids in a SINGLE
// round trip; no record comes back for an id that is not found.
func (r *Repo) GetTaxRegionsByIDs(ctx context.Context, ids []string) ([]models.TaxRegion, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []models.TaxRegion{}, nil
	}

	rows, err := r.queries(ctx).GetTaxRegionsByIDs(ctx, ids)
	if err != nil {
		return nil, wrapDB(err, "the tax regions could not be read")
	}
	return toTaxRegions(rows)
}

// ListTaxRegions returns the paged list of regions and the TOTAL count.
//
// If countryCode is empty no filter is applied. The total count is taken with a
// separate query: a page's worth of rows cannot tell the total number of
// records, and the API envelope (plan Section 8) has to carry the "count"
// field.
func (r *Repo) ListTaxRegions(
	ctx context.Context,
	countryCode string,
	limit, offset int32,
) ([]models.TaxRegion, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	rows, err := r.queries(ctx).ListTaxRegions(ctx, taxdb.ListTaxRegionsParams{
		Limit:       limit,
		Offset:      offset,
		CountryCode: countryCode,
	})
	if err != nil {
		return nil, 0, wrapDB(err, "the tax regions could not be listed")
	}

	total, err := r.queries(ctx).CountTaxRegions(ctx, countryCode)
	if err != nil {
		return nil, 0, wrapDB(err, "the tax regions could not be counted")
	}

	regions, err := toTaxRegions(rows)
	if err != nil {
		return nil, 0, err
	}
	return regions, total, nil
}

// ResolveTaxRegions returns the country's root and (if given) its province
// region.
//
// The order is PROVINCE FIRST, country after; the calculation chain walks from
// the most SPECIFIC to the general (see service.CalculateTax). If there is no
// region at all an EMPTY slice comes back, and that is NOT an error: a country
// whose tax is not configured has to produce zero tax, not an error (the
// reasoning is in the service/calculate.go godoc).
func (r *Repo) ResolveTaxRegions(ctx context.Context, countryCode, provinceCode string) ([]models.TaxRegion, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	rows, err := r.queries(ctx).ResolveTaxRegions(ctx, taxdb.ResolveTaxRegionsParams{
		CountryCode:  countryCode,
		ProvinceCode: provinceCode,
	})
	if err != nil {
		return nil, wrapDB(err, "the tax region could not be resolved: %s/%s", countryCode, provinceCode)
	}
	return toTaxRegions(rows)
}

// DeleteTaxRegion soft-deletes the region, its child regions, their rates and
// those rates' rules in a SINGLE transaction.
//
// The tree is deleted because without the country root a province region
// becomes unreachable: the resolution path always starts from the country. An
// orphaned province record enters no calculation, but a new root opened for the
// same country could not take its place — the province uniqueness would stay
// bound to the old (undeleted) row.
func (r *Repo) DeleteTaxRegion(ctx context.Context, id string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	return r.WithTx(ctx, func(ctx context.Context) error {
		q := r.queries(ctx)

		// The lock prevents a race with a concurrent flow adding a rate or a
		// province to the same region: those flows read the region with a
		// SHARED lock through [Repo.LockTaxRegion], and FOR SHARE conflicts with
		// FOR UPDATE. Without the lock the soft delete would be invisible to
		// them — a foreign key looks at the row's EXISTENCE, not at its
		// deleted_at, so a rate written to a deleted region trips no
		// constraint.
		if _, err := q.GetTaxRegionForUpdate(ctx, id); err != nil {
			return notFoundOr(err, CodeTaxRegionNotFound, "tax region not found: %s", id)
		}

		regionIDs, err := q.SoftDeleteTaxRegionTree(ctx, taxdb.SoftDeleteTaxRegionTreeParams{
			ID:        id,
			DeletedAt: fromTime(now),
		})
		if err != nil {
			return wrapDB(err, "the tax region could not be deleted: %s", id)
		}
		if len(regionIDs) == 0 {
			// Since the locked read saw the row, this cannot be reached; still,
			// returning success silently would mean a region believed deleted
			// although it was not.
			return errors.NotFound(CodeTaxRegionNotFound, "tax region not found: %s", id)
		}

		rateIDs, err := q.SoftDeleteTaxRatesByRegions(ctx, taxdb.SoftDeleteTaxRatesByRegionsParams{
			RegionIds: regionIDs,
			DeletedAt: fromTime(now),
		})
		if err != nil {
			return wrapDB(err, "the region's tax rates could not be deleted: %s", id)
		}
		if len(rateIDs) == 0 {
			return nil
		}

		if err := q.SoftDeleteTaxRateRulesByRates(ctx, taxdb.SoftDeleteTaxRateRulesByRatesParams{
			RateIds:   rateIDs,
			DeletedAt: fromTime(now),
		}); err != nil {
			return wrapDB(err, "the region's tax rules could not be deleted: %s", id)
		}
		return nil
	})
}

// toTaxRegion turns a generated row into the domain model.
func toTaxRegion(row taxdb.TaxRegion) (models.TaxRegion, error) {
	metadata, err := toJSONMap(row.Metadata)
	if err != nil {
		return models.TaxRegion{}, err
	}
	return models.TaxRegion{
		ID:           row.ID,
		CountryCode:  row.CountryCode,
		ProvinceCode: row.ProvinceCode,
		ParentID:     row.ParentID,
		ProviderID:   row.ProviderID,

		PricesIncludeTax: row.PricesIncludeTax,

		Metadata:  metadata,
		CreatedAt: toTime(row.CreatedAt),
		UpdatedAt: toTime(row.UpdatedAt),
		DeletedAt: toTimePtr(row.DeletedAt),
	}, nil
}

// toTaxRegions turns a slice of rows into domain models.
func toTaxRegions(rows []taxdb.TaxRegion) ([]models.TaxRegion, error) {
	out := make([]models.TaxRegion, 0, len(rows))
	for i := range rows {
		region, err := toTaxRegion(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, region)
	}
	return out, nil
}
