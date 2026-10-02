package service

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/region/models"
)

// GetCurrency returns the currency for an ISO 4217 code; errors.NotFound if
// there is none.
//
// The code is normalized to UPPER case: "try" and "TRY" find the same record. A
// formally invalid code returns errors.Invalid and the database is never
// reached.
func (s *Service) GetCurrency(ctx context.Context, code string) (models.Currency, error) {
	if err := s.ready(); err != nil {
		return models.Currency{}, err
	}
	normalized, err := NormalizeCurrencyCode(code)
	if err != nil {
		return models.Currency{}, err
	}
	return s.repo.GetCurrency(ctx, normalized)
}

// ListCurrencies returns the paginated currency list.
//
// The currency list is REFERENCE DATA and is loaded by the seed; the module has
// no write surface for it (see models.Currency).
func (s *Service) ListCurrencies(ctx context.Context, limit, offset int32) (Page[models.Currency], error) {
	if err := s.ready(); err != nil {
		return Page[models.Currency]{}, err
	}
	limit, offset, err := normalizePaging(limit, offset)
	if err != nil {
		return Page[models.Currency]{}, err
	}

	currencies, total, err := s.repo.ListCurrencies(ctx, limit, offset)
	if err != nil {
		return Page[models.Currency]{}, err
	}
	return Page[models.Currency]{Items: currencies, Count: total, Limit: limit, Offset: offset}, nil
}
