package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// CodeTaxRateRevised refuses a correction of a rate when another writer
// changed it since the caller read it (ADR 0378).
const CodeTaxRateRevised = "tax_rate_revised"

// ReviseTaxRate writes a rate's name and rate only while they are the ones
// the caller read, and refuses with [CodeTaxRateRevised] when another writer
// changed them since (ADR 0378). The terms are checked as the update checks
// them; the code, the default and the stack are not touched.
func (s *Service) ReviseTaxRate(ctx context.Context, id string, read, next models.TaxRateTerms) (models.TaxRate, error) {
	if err := s.ready(); err != nil {
		return models.TaxRate{}, err
	}
	if err := requireID(id, models.TaxRateIDPrefix, "tax rate id"); err != nil {
		return models.TaxRate{}, err
	}
	name, err := normalizeName(next.Name)
	if err != nil {
		return models.TaxRate{}, err
	}
	next.Name = name
	if err := validateRateBps(next.RateBps); err != nil {
		return models.TaxRate{}, err
	}

	rate, revised, err := s.repo.ReviseTaxRate(ctx, id, read, next, s.clock())
	if err != nil || revised {
		return rate, err
	}

	// Nothing was written: the rate is gone, or it was revised since.
	if _, err := s.repo.GetTaxRate(ctx, id); err != nil {
		return models.TaxRate{}, err
	}

	return models.TaxRate{}, errors.Conflict(CodeTaxRateRevised,
		"tax rate %s was revised since it was read; draw the list again", id)
}
