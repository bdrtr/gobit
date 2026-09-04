package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/repository/orderdb"
)

// LockReturn locks the return row until the end of the transaction and returns
// its current form.
//
// It may only be called inside [Repository.WithTx]. Every transition reads the
// status and writes the next one, and without the lock two operators clicking
// the same button at the same moment would both read "requested" and both
// write a timestamp.
func (r *Repository) LockReturn(ctx context.Context, id string) (models.Return, error) {
	row, err := r.queries(ctx).LockOrderReturn(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Return{}, coreerrors.NotFound(codeReturnNotFound,
				"return record not found: %s", id)
		}

		return models.Return{}, classify(err, codeQueryFailed, "could not lock the return record")
	}

	return toReturn(row)
}

// ReceiveReturn stamps the return as received.
func (r *Repository) ReceiveReturn(ctx context.Context, id string) (models.Return, error) {
	row, err := r.queries(ctx).ReceiveOrderReturn(ctx, id)
	if err != nil {
		return models.Return{}, classify(err, codeQueryFailed, "could not receive the return record")
	}

	return toReturn(row)
}

// CancelReturn withdraws the return request.
func (r *Repository) CancelReturn(ctx context.Context, id string) (models.Return, error) {
	row, err := r.queries(ctx).CancelOrderReturn(ctx, id)
	if err != nil {
		return models.Return{}, classify(err, codeQueryFailed, "could not cancel the return record")
	}

	return toReturn(row)
}

// CreateReturnItem writes one line of a return.
func (r *Repository) CreateReturnItem(
	ctx context.Context, item models.ReturnItem,
) (models.ReturnItem, error) {
	row, err := r.queries(ctx).CreateOrderReturnItem(ctx, orderdb.CreateOrderReturnItemParams{
		ID:              item.ID,
		OrderReturnID:   item.ReturnID,
		OrderLineItemID: item.OrderLineItemID,
		Quantity:        item.Quantity,
		RefundAmount:    item.RefundAmount,
	})
	if err != nil {
		return models.ReturnItem{}, classify(err, codeQueryFailed, "could not write the return line")
	}

	return toReturnItem(row), nil
}

// ListReturnItems returns a return's lines.
func (r *Repository) ListReturnItems(ctx context.Context, returnID string) ([]models.ReturnItem, error) {
	rows, err := r.queries(ctx).ListOrderReturnItems(ctx, returnID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not list the return lines")
	}

	out := make([]models.ReturnItem, 0, len(rows))
	for i := range rows {
		out = append(out, toReturnItem(rows[i]))
	}

	return out, nil
}

// ReturnedQuantities reports how many units of each given order line have
// already been asked back across the order's live returns.
//
// A line that has never been returned is ABSENT from the map rather than
// present with a zero. The caller reads it with the zero value anyway, and an
// absent key is the honest answer: the query returns rows, not a census.
func (r *Repository) ReturnedQuantities(
	ctx context.Context, lineItemIDs []string,
) (map[string]int64, error) {
	out := make(map[string]int64, len(lineItemIDs))
	if len(lineItemIDs) == 0 {
		return out, nil
	}

	rows, err := r.queries(ctx).SumReturnedQuantities(ctx, lineItemIDs)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not sum the returned quantities")
	}

	for i := range rows {
		out[rows[i].OrderLineItemID] = rows[i].Returned
	}

	return out, nil
}

// toReturnItem converts a row into the domain model.
func toReturnItem(row orderdb.OrderReturnItem) models.ReturnItem {
	return models.ReturnItem{
		ID:              row.ID,
		ReturnID:        row.OrderReturnID,
		OrderLineItemID: row.OrderLineItemID,
		Quantity:        row.Quantity,
		RefundAmount:    row.RefundAmount,
		CreatedAt:       toTime(row.CreatedAt),
		UpdatedAt:       toTime(row.UpdatedAt),
	}
}
