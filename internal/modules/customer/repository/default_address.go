package repository

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// DefaultShippingAddresses returns the default shipping address of each of the
// given customers that has one, in one read (ADR 0304).
func (r *Repo) DefaultShippingAddresses(ctx context.Context, customerIDs []string) ([]models.CustomerAddress, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	rows, err := r.q.ListDefaultShippingAddresses(ctx, customerIDs)
	if err != nil {
		return nil, wrapDB(err, "the customers' default shipping addresses could not be read")
	}

	out := make([]models.CustomerAddress, 0, len(rows))
	for i := range rows {
		out = append(out, toAddress(rows[i]))
	}

	return out, nil
}
