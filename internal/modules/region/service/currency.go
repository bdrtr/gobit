package service

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/region/models"
)

// GetCurrency ISO 4217 koduna göre para birimi döner; yoksa errors.NotFound.
//
// Kod BÜYÜK harfe normalleştirilir: "try" ile "TRY" aynı kaydı bulur. Biçimsel
// olarak geçersiz bir kod errors.Invalid döner ve veritabanına hiç gidilmez.
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

// ListCurrencies sayfalanmış para birimi listesini döner.
//
// Para birimi listesi REFERANS VERİDİR ve tohum ile yüklenir; modülün yazma
// yüzeyi yoktur (bkz. models.Currency).
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
