package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/region/models"
)

// CodeRegionRevised refuses a correction of a region when another writer
// changed it since the caller read it (ADR 0362).
const CodeRegionRevised = "region_revised"

// ReviseRegion writes the region's name, whether taxes are computed for it
// and its tax rate only while they are the ones the caller read, and refuses
// with [CodeRegionRevised] when another writer changed them since (ADR 0362).
// The terms are checked as the update checks them; the currency is not
// touched.
func (s *Service) ReviseRegion(ctx context.Context, id string, read, next models.RegionTerms) (models.Region, error) {
	if err := s.ready(); err != nil {
		return models.Region{}, err
	}
	if err := requireRegionID(id); err != nil {
		return models.Region{}, err
	}
	name, err := normalizeName(next.Name)
	if err != nil {
		return models.Region{}, err
	}
	next.Name = name
	if err := validateTaxRate(next.TaxRate); err != nil {
		return models.Region{}, err
	}

	region, revised, err := s.repo.ReviseRegion(ctx, id, read, next, s.clock())
	if err != nil || revised {
		return region, err
	}

	// Nothing was written: the region is gone, or it was revised since.
	if _, err := s.repo.GetRegion(ctx, id); err != nil {
		return models.Region{}, err
	}

	return models.Region{}, errors.Conflict(CodeRegionRevised,
		"region %s was revised since it was read; draw the list again", id)
}
