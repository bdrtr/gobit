package repository

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// AddressesOfCustomers returns the living addresses of the given customers in
// one read, each customer's in the order they were written (ADR 0304, ADR
// 0308).
func (r *Repo) AddressesOfCustomers(ctx context.Context, customerIDs []string) ([]models.CustomerAddress, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	rows, err := r.q.ListAddressesOfCustomers(ctx, customerIDs)
	if err != nil {
		return nil, wrapDB(err, "the customers' addresses could not be read")
	}

	out := make([]models.CustomerAddress, 0, len(rows))
	for i := range rows {
		out = append(out, toAddress(rows[i]))
	}

	return out, nil
}
