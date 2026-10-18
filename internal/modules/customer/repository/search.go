package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/repository/customerdb"
)

// searchCustomers is ListCustomers for a filter carrying a search
// (ADR 0456): the same narrowings, the same order and the same count, over
// the customers the search finds.
func (r *Repo) searchCustomers(
	ctx context.Context,
	filter models.CustomerFilter,
	limit, offset int64,
	afterAt pgtype.Timestamptz,
	afterID *string,
) ([]models.Customer, int64, error) {
	rows, err := r.q.SearchCustomers(ctx, customerdb.SearchCustomersParams{
		Q:          *filter.Search,
		Email:      filter.Email,
		HasAccount: filter.HasAccount,
		GroupID:    filter.GroupID,
		Lim:        toInt32(limit),
		Off:        toInt32(offset),
		AfterAt:    afterAt,
		AfterID:    afterID,
	})
	if err != nil {
		return nil, 0, wrapDB(err, "the customer list could not be searched")
	}

	total, err := r.q.CountSearchedCustomers(ctx, customerdb.CountSearchedCustomersParams{
		Q:          *filter.Search,
		Email:      filter.Email,
		HasAccount: filter.HasAccount,
		GroupID:    filter.GroupID,
	})
	if err != nil {
		return nil, 0, wrapDB(err, "the customers searched could not be counted")
	}

	customers, err := toCustomers(rows)
	if err != nil {
		return nil, 0, err
	}

	return customers, total, nil
}
