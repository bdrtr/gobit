package repository

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/region/models"
	"github.com/bdrtr/gobit/internal/modules/region/repository/regiondb"
)

// GetCurrency koda göre para birimi döner; yoksa errors.NotFound.
func (r *Repo) GetCurrency(ctx context.Context, code string) (models.Currency, error) {
	if err := r.ready(); err != nil {
		return models.Currency{}, err
	}

	row, err := r.q.GetCurrency(ctx, code)
	if err != nil {
		return models.Currency{}, notFoundOr(err, CodeCurrencyNotFound, "para birimi bulunamadı: %s", code)
	}
	return toCurrency(row), nil
}

// ListCurrencies sayfalanmış para birimi listesini ve TOPLAM kayıt sayısını döner.
func (r *Repo) ListCurrencies(ctx context.Context, limit, offset int32) ([]models.Currency, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	rows, err := r.q.ListCurrencies(ctx, regiondb.ListCurrenciesParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, 0, wrapDB(err, "para birimi listesi alınamadı")
	}

	total, err := r.q.CountCurrencies(ctx)
	if err != nil {
		return nil, 0, wrapDB(err, "para birimi sayısı alınamadı")
	}

	currencies := make([]models.Currency, 0, len(rows))
	for i := range rows {
		currencies = append(currencies, toCurrency(rows[i]))
	}
	return currencies, total, nil
}

// GetCurrenciesByCodes verilen kodlara karşılık gelen para birimlerini TEK
// sorguda döner. Bulunamayan kod için kayıt dönmez; bu bir hata değildir.
func (r *Repo) GetCurrenciesByCodes(ctx context.Context, codes []string) ([]models.Currency, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(codes) == 0 {
		return []models.Currency{}, nil
	}

	rows, err := r.q.GetCurrenciesByCodes(ctx, codes)
	if err != nil {
		return nil, wrapDB(err, "para birimleri alınamadı")
	}

	currencies := make([]models.Currency, 0, len(rows))
	for i := range rows {
		currencies = append(currencies, toCurrency(rows[i]))
	}
	return currencies, nil
}
