package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// CreateTaxRateInput is the write input of a new tax rate.
type CreateTaxRateInput struct {
	// TaxRegionID is the region the rate is added to; it is required.
	TaxRegionID string
	// Name is the rate's display name (e.g. "KDV"); it is required.
	Name string
	// Code is the reconciliation code for external systems; it may be left empty.
	Code string
	// RateBps is the rate (basis points; 2000 = 20%).
	RateBps int32
	// IsDefault is whether this is the region's default rate.
	IsDefault bool
	// StacksOnID is the rate this rate will stand ON TOP OF; it may be left
	// empty.
	//
	// If it is filled the rate is never selected: it is reached by expanding
	// the selected rate. That is why it CANNOT be the default and CANNOT carry
	// rules (ADR 0095).
	StacksOnID string
	// Compound is whether the rate is computed ON TOP OF the tax of the ones
	// below it; it cannot be true while StacksOnID is empty.
	Compound bool
	// Metadata is free-form metadata.
	Metadata map[string]any
}

// CreateTaxRate adds a tax rate to a region.
//
// # A second default rate
//
// It is refused (errors.Conflict, code [CodeDefaultExists]). The service checks
// this by reading first; the last line of defense is the partial unique index
// in the database (tax_rate_default_uniq). Two concurrent requests can pass the
// "read first, then write" check together, and from then on which rate applies
// would be left to row order.
//
// # If there is no region
//
// errors.NotFound is returned and no row is written. The check is made twice:
// here with a readable error, and in the database with a foreign key; the
// second only covers an intervention made directly with SQL.
//
// # The check and the write are in the SAME transaction
//
// The region check and the rate write run in a single transaction, and the
// region row is read with a SHARED lock (Repository.LockTaxRegion). The
// situation before this frame was added was MEASURED: the two calls were
// separately auto-committed statements, and a [Service.DeleteTaxRegion]
// slipping into the gap between them completed after the check, and the rate
// was written anyway. A foreign key does NOT CATCH this — the delete is SOFT,
// the region row stays in place — and what was left behind was a LIVE rate
// bound to a deleted region. That rate enters no calculation but stays in the
// ledger; repository.DeleteTaxRegion's transaction exists precisely so that
// this row never comes about, and the gap on the service side was going around
// it.
//
// The default rate check ([Service.assertNoDefaultRate]) is INSIDE the
// transaction too, but it does NOT GUARANTEE uniqueness: the shared lock does
// not separate two concurrent rate inserts (nor is it meant to), and two
// requests can pass the check together. The last line of defense is still the
// partial unique index; the check's job here is to produce a readable error for
// the ordinary caller, not for the loser of the race.
func (s *Service) CreateTaxRate(ctx context.Context, in CreateTaxRateInput) (models.TaxRate, error) {
	if err := s.ready(); err != nil {
		return models.TaxRate{}, err
	}
	if err := requireID(in.TaxRegionID, models.TaxRegionIDPrefix, "tax region id"); err != nil {
		return models.TaxRate{}, err
	}

	name, err := normalizeName(in.Name)
	if err != nil {
		return models.TaxRate{}, err
	}
	code, err := normalizeCode(in.Code)
	if err != nil {
		return models.TaxRate{}, err
	}
	if err := validateRateBps(in.RateBps); err != nil {
		return models.TaxRate{}, err
	}

	var created models.TaxRate
	txErr := s.repo.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.repo.LockTaxRegion(ctx, in.TaxRegionID); err != nil {
			return err
		}
		if in.IsDefault {
			if err := s.assertNoDefaultRate(ctx, in.TaxRegionID); err != nil {
				return err
			}
		}
		if err := s.assertStackable(ctx, in.TaxRegionID, in.StacksOnID, in.IsDefault,
			in.RateBps, in.Compound, ""); err != nil {
			return err
		}

		now := s.clock()
		rate := models.TaxRate{
			ID:          models.NewTaxRateID(now),
			TaxRegionID: in.TaxRegionID,
			Name:        name,
			RateBps:     in.RateBps,
			IsDefault:   in.IsDefault,
			Compound:    in.Compound,
			Metadata:    in.Metadata,
		}
		if code != "" {
			rate.Code = &code
		}
		if in.StacksOnID != "" {
			base := in.StacksOnID
			rate.StacksOnID = &base
		}

		var err error
		created, err = s.repo.CreateTaxRate(ctx, rate, now)
		return err
	})
	if txErr != nil {
		return models.TaxRate{}, txErr
	}
	return created, nil
}

// assertNoDefaultRate verifies that the region has no default rate yet.
func (s *Service) assertNoDefaultRate(ctx context.Context, regionID string) error {
	existing, err := s.repo.ListTaxRates(ctx, regionID)
	if err != nil {
		return err
	}
	for i := range existing {
		if existing[i].IsDefault {
			return errors.Conflict(CodeDefaultExists,
				"region %s already has a default rate: %s", regionID, existing[i].ID)
		}
	}
	return nil
}

// GetTaxRate returns the rate by id; errors.NotFound if there is none.
func (s *Service) GetTaxRate(ctx context.Context, id string) (models.TaxRate, error) {
	if err := s.ready(); err != nil {
		return models.TaxRate{}, err
	}
	if err := requireID(id, models.TaxRateIDPrefix, "tax rate id"); err != nil {
		return models.TaxRate{}, err
	}
	return s.repo.GetTaxRate(ctx, id)
}

// ListTaxRates returns a region's rates; the default rate comes FIRST.
//
// There is NO paging, and that is deliberate: the number of rates in a region
// is a manageable list (standard, reduced, exempt …) and all of it should show
// in a single response. Paging would mean an administrator overlooking a rate
// on the second page.
func (s *Service) ListTaxRates(ctx context.Context, regionID string) ([]models.TaxRate, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := requireID(regionID, models.TaxRegionIDPrefix, "tax region id"); err != nil {
		return nil, err
	}
	if _, err := s.repo.GetTaxRegion(ctx, regionID); err != nil {
		return nil, err
	}
	return s.repo.ListTaxRates(ctx, regionID)
}

// UpdateTaxRateInput is the PARTIAL update input of a rate.
//
// A nil field means "leave it alone". Had a full body been required, a client
// that forgot to send rate_bps in its body would silently reset the rate.
type UpdateTaxRateInput struct {
	// Name is the new name; if nil the name does not change.
	Name *string
	// Code is the new reconciliation code; if nil the code does not change. To
	// REMOVE the code, a pointer to an empty string is given.
	Code *string
	// RateBps is the new rate (basis points); if nil the rate does not change.
	RateBps *int32
	// IsDefault is the default flag; if nil it does not change.
	IsDefault *bool
	// Metadata is the new metadata; if nil the metadata does not change.
	Metadata map[string]any
}

// UpdateTaxRate updates the given fields of the rate.
//
// If no field is given errors.Invalid is returned: an empty patch is the most
// likely sign that the client misspelled the name of the field it believes it
// sent, and returning success silently would hide that mistake.
//
// Making a rate the DEFAULT depends on two extra conditions, and both are
// checked in the repository layer, UNDER the row LOCK: the region must have no
// other default rate (partial unique index), and the rate must have no rules
// at all. The checks have to be under the lock — otherwise a rule insert
// slipping in between could make a ruled rate the default.
func (s *Service) UpdateTaxRate(ctx context.Context, id string, in UpdateTaxRateInput) (models.TaxRate, error) {
	if err := s.ready(); err != nil {
		return models.TaxRate{}, err
	}
	if err := requireID(id, models.TaxRateIDPrefix, "tax rate id"); err != nil {
		return models.TaxRate{}, err
	}

	patch, err := buildRatePatch(in)
	if err != nil {
		return models.TaxRate{}, err
	}
	if patch.Empty() {
		return models.TaxRate{}, errors.Invalid(CodeInvalidInput, "no field was given to update")
	}
	return s.repo.UpdateTaxRate(ctx, id, patch, s.clock())
}

// buildRatePatch validates the update input and turns it into a patch.
//
// Validation is applied only to the fields that are FILLED: the current value
// of a field that is not touched must not fail the update, even if it violates
// a rule that is not valid today.
func buildRatePatch(in UpdateTaxRateInput) (models.TaxRatePatch, error) {
	var patch models.TaxRatePatch

	if in.Name != nil {
		name, err := normalizeName(*in.Name)
		if err != nil {
			return models.TaxRatePatch{}, err
		}
		patch.Name = &name
	}
	if in.Code != nil {
		code, err := normalizeCode(*in.Code)
		if err != nil {
			return models.TaxRatePatch{}, err
		}
		patch.Code = &code
	}
	if in.RateBps != nil {
		if err := validateRateBps(*in.RateBps); err != nil {
			return models.TaxRatePatch{}, err
		}
		rate := *in.RateBps
		patch.RateBps = &rate
	}
	if in.IsDefault != nil {
		isDefault := *in.IsDefault
		patch.IsDefault = &isDefault
	}
	patch.Metadata = in.Metadata
	return patch, nil
}

// DeleteTaxRate soft-deletes the rate and its rules; errors.NotFound if there
// is none.
func (s *Service) DeleteTaxRate(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(id, models.TaxRateIDPrefix, "tax rate id"); err != nil {
		return err
	}
	return s.repo.DeleteTaxRate(ctx, id, s.clock())
}
