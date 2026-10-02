package repository

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/region/models"
	"github.com/bdrtr/gobit/internal/modules/region/repository/regiondb"
)

// GetCurrency returns the currency by its code; errors.NotFound if there is none.
func (r *Repo) GetCurrency(ctx context.Context, code string) (models.Currency, error) {
	if err := r.ready(); err != nil {
		return models.Currency{}, err
	}

	row, err := r.q.GetCurrency(ctx, code)
	if err != nil {
		return models.Currency{}, notFoundOr(err, CodeCurrencyNotFound, "currency not found: %s", code)
	}
	return toCurrency(row), nil
}

// ListCurrencies returns a paginated list of currencies and the TOTAL record count.
func (r *Repo) ListCurrencies(ctx context.Context, limit, offset int32) ([]models.Currency, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	rows, err := r.q.ListCurrencies(ctx, regiondb.ListCurrenciesParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, 0, wrapDB(err, "the currency list could not be read")
	}

	total, err := r.q.CountCurrencies(ctx)
	if err != nil {
		return nil, 0, wrapDB(err, "the currency count could not be read")
	}

	currencies := make([]models.Currency, 0, len(rows))
	for i := range rows {
		currencies = append(currencies, toCurrency(rows[i]))
	}
	return currencies, total, nil
}

// GetCurrenciesByCodes returns the currencies matching the given codes in a
// SINGLE query. No record is returned for a code that is not found; that is not
// an error.
func (r *Repo) GetCurrenciesByCodes(ctx context.Context, codes []string) ([]models.Currency, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(codes) == 0 {
		return []models.Currency{}, nil
	}

	rows, err := r.q.GetCurrenciesByCodes(ctx, codes)
	if err != nil {
		return nil, wrapDB(err, "the currencies could not be read")
	}

	currencies := make([]models.Currency, 0, len(rows))
	for i := range rows {
		currencies = append(currencies, toCurrency(rows[i]))
	}
	return currencies, nil
}
