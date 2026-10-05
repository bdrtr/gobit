package service

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// PlacedMargins returns, for each given order, the margin its goods were placed
// at (ADR 0401): the net sales of its lines that are not gift cards, and, when
// every such line kept a cost, that cost and the difference. An order with no
// such line has no entry.
//
// It is read from the lines as they were sold, so a cost changed in the
// catalog afterwards moves no order; a cancellation, a return, a credit, a
// provider's fee and a carrier's cost do not move it either. Both figures are at
// most [models.MaxTotal], so the difference fits an int64, and a negative one is
// kept: goods sold at a loss.
func (s *Service) PlacedMargins(ctx context.Context, orderIDs []string) (map[string]models.PlacedMargin, error) {
	out := make(map[string]models.PlacedMargin, len(orderIDs))
	if len(orderIDs) == 0 {
		return out, nil
	}

	margins, err := s.store.PlacedMargins(ctx, orderIDs)
	if err != nil {
		return nil, err
	}
	for _, m := range margins {
		if m.Cost != nil {
			margin := m.Sales - *m.Cost
			m.Margin = &margin
		}
		out[m.OrderID] = m
	}
	return out, nil
}
