package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/eventbus"
	"github.com/bdrtr/gobit/internal/core/eventbus/outbox"
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

// ReceiveReturn stamps the return as received at the given location.
func (r *Repository) ReceiveReturn(
	ctx context.Context, id, locationID string,
) (models.Return, error) {
	row, err := r.queries(ctx).ReceiveOrderReturn(ctx, orderdb.ReceiveOrderReturnParams{
		ID:                 id,
		ReceivedLocationID: locationID,
	})
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

// LockClaim locks the claim row until the end of the transaction.
func (r *Repository) LockClaim(ctx context.Context, id string) (models.Claim, error) {
	row, err := r.queries(ctx).LockOrderClaim(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Claim{}, coreerrors.NotFound(codeClaimNotFound,
				"claim record not found: %s", id)
		}

		return models.Claim{}, classify(err, codeQueryFailed, "could not lock the claim record")
	}

	return toClaim(row)
}

// CompleteClaim records that the claim was settled.
func (r *Repository) CompleteClaim(ctx context.Context, id string) (models.Claim, error) {
	row, err := r.queries(ctx).CompleteOrderClaim(ctx, id)
	if err != nil {
		return models.Claim{}, classify(err, codeQueryFailed, "could not complete the claim record")
	}

	return toClaim(row)
}

// CancelClaim withdraws the claim.
func (r *Repository) CancelClaim(ctx context.Context, id string) (models.Claim, error) {
	row, err := r.queries(ctx).CancelOrderClaim(ctx, id)
	if err != nil {
		return models.Claim{}, classify(err, codeQueryFailed, "could not cancel the claim record")
	}

	return toClaim(row)
}

// WriteOutboxEvent records an event inside the CURRENT transaction.
//
// # Why the module writes a core-owned table
//
// The outbox row has to commit with the order, and only this side is inside the
// order's transaction: every module keeps its transaction under its own
// unexported context key, so the core cannot see it. The core owns the table
// and the writing rule; this method is the hand that reaches into the
// transaction, and it does nothing else.
//
// # Outside a transaction it REFUSES
//
// An outbox row written outside one is an event promised for work that may
// never commit — the exact fault the outbox exists to prevent, with the
// appearance of preventing it.
func (r *Repository) WriteOutboxEvent(
	ctx context.Context, id, name string, data map[string]any,
) error {
	tx, inTx := txFromContext(ctx)
	if !inTx {
		return coreerrors.Internal(codeQueryFailed,
			"an outbox event may only be written inside a transaction (%s); outside one it "+
				"promises an event for work that may never commit", name)
	}

	return outbox.Write(ctx, tx, eventbus.Event{ID: id, Name: name, Data: data})
}
