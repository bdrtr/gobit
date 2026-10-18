package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/repository/orderdb"
)

// searchOrders is ListOrders for a filter carrying a search (ADR 0456): the
// same narrowings, the same order and the same count, over the orders the
// search finds. The narrowings arrive already converted by ListOrders.
func (r *Repository) searchOrders(
	ctx context.Context,
	filter models.OrderFilter,
	status, paymentStatus *string,
	afterAt pgtype.Timestamptz,
	afterID *string,
) ([]models.Order, int64, error) {
	rows, err := r.queries(ctx).SearchOrders(ctx, orderdb.SearchOrdersParams{
		DisplayID:        filter.Search.DisplayID,
		Q:                filter.Search.Text,
		CustomerID:       filter.CustomerID,
		RegionID:         filter.RegionID,
		Status:           status,
		AddsToOrderID:    filter.AddsToOrderID,
		AwaitingPayment:  filter.AwaitingPayment,
		PlacedByOperator: filter.PlacedByOperator,
		SalesChannelID:   filter.SalesChannelID,
		PaymentStatus:    paymentStatus,
		RowLimit:         filter.Limit,
		RowOffset:        filter.Offset,
		AfterAt:          afterAt,
		AfterID:          afterID,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "could not search the orders")
	}

	// The count carries the SAME narrowings as the page; a field added to one
	// and forgotten on the other would report a page out of the wrong total.
	total, err := r.queries(ctx).CountSearchedOrders(ctx, orderdb.CountSearchedOrdersParams{
		DisplayID:        filter.Search.DisplayID,
		Q:                filter.Search.Text,
		CustomerID:       filter.CustomerID,
		RegionID:         filter.RegionID,
		Status:           status,
		AddsToOrderID:    filter.AddsToOrderID,
		AwaitingPayment:  filter.AwaitingPayment,
		PlacedByOperator: filter.PlacedByOperator,
		SalesChannelID:   filter.SalesChannelID,
		PaymentStatus:    paymentStatus,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "could not count the orders searched")
	}

	orders, err := toOrders(rows)
	if err != nil {
		return nil, 0, err
	}

	return orders, total, nil
}
