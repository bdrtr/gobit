package repository

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/order/repository/orderdb"
)

// CustomerBoughtAnyOf reports whether the customer has an order that was not
// canceled with a line of one of the variants (ADR 0372).
func (r *Repository) CustomerBoughtAnyOf(ctx context.Context, customerID string, variantIDs []string) (bool, error) {
	bought, err := r.queries(ctx).CustomerBoughtAnyOf(ctx, orderdb.CustomerBoughtAnyOfParams{
		CustomerID: customerID,
		VariantIds: variantIDs,
	})
	if err != nil {
		return false, classify(err, codeQueryFailed, "could not read whether the customer bought the variants")
	}

	return bought, nil
}
