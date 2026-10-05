package repository

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/repository/orderdb"
)

// PlacedMargins reads the parts of the given orders' placed margins in one
// statement (ADR 0401); what is summed is queries/margin.sql's. The cost is
// summed as numeric and comes back only when it fits [models.MaxTotal], so it
// fits an int64 here. Margin is left to the caller. An order with no line that
// is not a gift card has no entry.
func (r *Repository) PlacedMargins(ctx context.Context, orderIDs []string) ([]models.PlacedMargin, error) {
	if len(orderIDs) == 0 {
		return nil, nil
	}

	rows, err := r.queries(ctx).PlacedMarginsOfOrders(ctx, orderdb.PlacedMarginsOfOrdersParams{
		CostLimit: models.MaxTotal,
		OrderIds:  orderIDs,
	})
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not read the margins of %d orders", len(orderIDs))
	}

	out := make([]models.PlacedMargin, 0, len(rows))
	for _, row := range rows {
		margin := models.PlacedMargin{
			OrderID: row.OrderID, Sales: row.Sales, LinesWithoutCost: row.LinesWithoutCost,
		}
		if row.Cost.Valid {
			cost, err := row.Cost.Int64Value()
			if err != nil || !cost.Valid {
				return nil, errors.Internal(codeQueryFailed,
					"the cost of order %s did not read as a whole amount: %v", row.OrderID, err)
			}
			margin.Cost = &cost.Int64
		}
		out = append(out, margin)
	}
	return out, nil
}
