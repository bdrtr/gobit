package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/eventbus/outbox"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/repository/orderdb"
)

// CreateReturn opens a new return record.
func (r *Repository) CreateReturn(ctx context.Context, ret models.Return) (models.Return, error) {
	meta, err := fromJSONMap(ret.Metadata)
	if err != nil {
		return models.Return{}, err
	}

	row, err := r.queries(ctx).CreateOrderReturn(ctx, orderdb.CreateOrderReturnParams{
		ID:           ret.ID,
		OrderID:      ret.OrderID,
		Status:       ret.Status.String(),
		RefundAmount: ret.RefundAmount,
		Reason:       nullString(ret.Reason),
		Note:         nullString(ret.Note),
		Metadata:     meta,
	})
	if err != nil {
		return models.Return{}, classify(err, codeQueryFailed, "could not create the return record")
	}
	return toReturn(row)
}

// GetReturn returns the return record by its identifier; NotFound if there is
// none.
func (r *Repository) GetReturn(ctx context.Context, id string) (models.Return, error) {
	row, err := r.queries(ctx).GetOrderReturn(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Return{}, coreerrors.NotFound(codeReturnNotFound, "return record not found: %s", id)
		}
		return models.Return{}, classify(err, codeQueryFailed, "could not read the return record")
	}
	return toReturn(row)
}

// ListReturns pages the order's return records; the second value is the total
// count.
func (r *Repository) ListReturns(ctx context.Context, filter models.ChildFilter) ([]models.Return, int64, error) {
	rows, err := r.queries(ctx).ListOrderReturns(ctx, orderdb.ListOrderReturnsParams{
		OrderID:   filter.OrderID,
		RowLimit:  filter.Limit,
		RowOffset: filter.Offset,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "could not list the return records")
	}
	total, err := r.queries(ctx).CountOrderReturns(ctx, filter.OrderID)
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "could not count the return records")
	}
	items, err := toReturns(rows)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// CreateExchange opens a new exchange record.
func (r *Repository) CreateExchange(ctx context.Context, exchange models.Exchange) (models.Exchange, error) {
	meta, err := fromJSONMap(exchange.Metadata)
	if err != nil {
		return models.Exchange{}, err
	}

	row, err := r.queries(ctx).CreateOrderExchange(ctx, orderdb.CreateOrderExchangeParams{
		ID:            exchange.ID,
		OrderID:       exchange.OrderID,
		Status:        exchange.Status.String(),
		DifferenceDue: exchange.DifferenceDue,
		Note:          nullString(exchange.Note),
		Metadata:      meta,
	})
	if err != nil {
		return models.Exchange{}, classify(err, codeQueryFailed, "could not create the exchange record")
	}
	return toExchange(row)
}

// GetExchange returns the exchange record by its identifier; NotFound if there
// is none.
func (r *Repository) GetExchange(ctx context.Context, id string) (models.Exchange, error) {
	row, err := r.queries(ctx).GetOrderExchange(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Exchange{}, coreerrors.NotFound(codeExchangeNotFound, "exchange record not found: %s", id)
		}
		return models.Exchange{}, classify(err, codeQueryFailed, "could not read the exchange record")
	}
	return toExchange(row)
}

// ListExchanges pages the order's exchange records; the second value is the
// total count.
func (r *Repository) ListExchanges(ctx context.Context, filter models.ChildFilter) ([]models.Exchange, int64, error) {
	rows, err := r.queries(ctx).ListOrderExchanges(ctx, orderdb.ListOrderExchangesParams{
		OrderID:   filter.OrderID,
		RowLimit:  filter.Limit,
		RowOffset: filter.Offset,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "could not list the exchange records")
	}
	total, err := r.queries(ctx).CountOrderExchanges(ctx, filter.OrderID)
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "could not count the exchange records")
	}
	items, err := toExchanges(rows)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// CreateClaim opens a new claim record.
func (r *Repository) CreateClaim(ctx context.Context, claim models.Claim) (models.Claim, error) {
	meta, err := fromJSONMap(claim.Metadata)
	if err != nil {
		return models.Claim{}, err
	}

	row, err := r.queries(ctx).CreateOrderClaim(ctx, orderdb.CreateOrderClaimParams{
		ID:           claim.ID,
		OrderID:      claim.OrderID,
		ClaimType:    claim.Type.String(),
		Status:       claim.Status.String(),
		RefundAmount: claim.RefundAmount,
		Reason:       nullString(claim.Reason),
		Note:         nullString(claim.Note),
		Metadata:     meta,
	})
	if err != nil {
		return models.Claim{}, classify(err, codeQueryFailed, "could not create the claim record")
	}
	return toClaim(row)
}

// GetClaim returns the claim record by its identifier; NotFound if there is
// none.
func (r *Repository) GetClaim(ctx context.Context, id string) (models.Claim, error) {
	row, err := r.queries(ctx).GetOrderClaim(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Claim{}, coreerrors.NotFound(codeClaimNotFound, "claim record not found: %s", id)
		}
		return models.Claim{}, classify(err, codeQueryFailed, "could not read the claim record")
	}
	return toClaim(row)
}

// ListClaims pages the order's claim records; the second value is the total
// count.
func (r *Repository) ListClaims(ctx context.Context, filter models.ChildFilter) ([]models.Claim, int64, error) {
	rows, err := r.queries(ctx).ListOrderClaims(ctx, orderdb.ListOrderClaimsParams{
		OrderID:   filter.OrderID,
		RowLimit:  filter.Limit,
		RowOffset: filter.Offset,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "could not list the claim records")
	}
	total, err := r.queries(ctx).CountOrderClaims(ctx, filter.OrderID)
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "could not count the claim records")
	}
	items, err := toClaims(rows)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

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

// LockExchange locks the exchange row until the end of the transaction.
//
// It may only be called inside [Repository.WithTx], for the reason
// [Repository.LockReturn] states.
func (r *Repository) LockExchange(ctx context.Context, id string) (models.Exchange, error) {
	row, err := r.queries(ctx).LockOrderExchange(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Exchange{}, coreerrors.NotFound(codeExchangeNotFound,
				"exchange record not found: %s", id)
		}

		return models.Exchange{}, classify(err, codeQueryFailed, "could not lock the exchange record")
	}

	return toExchange(row)
}

// CancelExchange withdraws the exchange request.
func (r *Repository) CancelExchange(ctx context.Context, id string) (models.Exchange, error) {
	row, err := r.queries(ctx).CancelOrderExchange(ctx, id)
	if err != nil {
		return models.Exchange{}, classify(err, codeQueryFailed, "could not cancel the exchange record")
	}

	return toExchange(row)
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

// --- credit lines ------------------------------------------------------------

// CreateCreditLine records an amount that lowers what the order owes.
func (r *Repository) CreateCreditLine(
	ctx context.Context, credit models.OrderCreditLine,
) (models.OrderCreditLine, error) {
	row, err := r.queries(ctx).CreateOrderCreditLine(ctx, orderdb.CreateOrderCreditLineParams{
		ID:      credit.ID,
		OrderID: credit.OrderID,
		Amount:  credit.Amount,
		Reason:  credit.Reason,
		Note:    credit.Note,
	})
	if err != nil {
		return models.OrderCreditLine{}, classify(err, codeQueryFailed,
			"could not create the order credit line")
	}

	return toCreditLine(row), nil
}

// ListCreditLines returns the order's credit lines, oldest first.
func (r *Repository) ListCreditLines(
	ctx context.Context, orderID string,
) ([]models.OrderCreditLine, error) {
	rows, err := r.queries(ctx).ListOrderCreditLines(ctx, orderID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not read the order credit lines")
	}

	out := make([]models.OrderCreditLine, 0, len(rows))
	for i := range rows {
		out = append(out, toCreditLine(rows[i]))
	}

	return out, nil
}

// CreditedTotal returns the sum of the order's credit lines.
//
// It is READ rather than stored, for the reason migration 000001 gives about the
// outstanding amount: a running total in a second place is a number that can go
// stale, and this one is the sum of rows the database can add up itself.
func (r *Repository) CreditedTotal(ctx context.Context, orderID string) (int64, error) {
	total, err := r.queries(ctx).SumOrderCreditLines(ctx, orderID)
	if err != nil {
		return 0, classify(err, codeQueryFailed, "could not read the order's credited total")
	}

	return total, nil
}

// --- line cancellations ------------------------------------------------------

// CreateLineCancellation records units of a line that will not be delivered.
func (r *Repository) CreateLineCancellation(
	ctx context.Context, cancellation models.OrderLineCancellation,
) (models.OrderLineCancellation, error) {
	row, err := r.queries(ctx).CreateOrderLineCancellation(ctx,
		orderdb.CreateOrderLineCancellationParams{
			ID:              cancellation.ID,
			OrderLineItemID: cancellation.OrderLineItemID,
			Quantity:        cancellation.Quantity,
			Reason:          cancellation.Reason,
			Note:            cancellation.Note,
		})
	if err != nil {
		return models.OrderLineCancellation{}, classify(err, codeQueryFailed,
			"could not create the order line cancellation")
	}

	return toLineCancellation(row), nil
}

// ListLineCancellations returns the order's line cancellations, oldest first.
func (r *Repository) ListLineCancellations(
	ctx context.Context, orderID string,
) ([]models.OrderLineCancellation, error) {
	rows, err := r.queries(ctx).ListOrderLineCancellations(ctx, orderID)
	if err != nil {
		return nil, classify(err, codeQueryFailed,
			"could not read the order line cancellations")
	}

	out := make([]models.OrderLineCancellation, 0, len(rows))
	for i := range rows {
		out = append(out, toLineCancellation(rows[i]))
	}

	return out, nil
}

// CanceledQuantities reports how many units of each given order line have
// already been written off.
//
// The shape is [Repository.ReturnedQuantities]'s and so is the reason: a line
// nobody canceled is ABSENT rather than zero, because the query returns rows
// and not a census.
func (r *Repository) CanceledQuantities(
	ctx context.Context, lineItemIDs []string,
) (map[string]int64, error) {
	out := make(map[string]int64, len(lineItemIDs))
	if len(lineItemIDs) == 0 {
		return out, nil
	}

	rows, err := r.queries(ctx).SumCanceledQuantities(ctx, lineItemIDs)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not sum the canceled quantities")
	}

	for i := range rows {
		out[rows[i].OrderLineItemID] = rows[i].Canceled
	}

	return out, nil
}

// --- claim evidence ----------------------------------------------------------

// CreateClaimEvidence binds a file to the claim.
func (r *Repository) CreateClaimEvidence(
	ctx context.Context, evidence models.ClaimEvidence,
) (models.ClaimEvidence, error) {
	row, err := r.queries(ctx).CreateOrderClaimEvidence(ctx,
		orderdb.CreateOrderClaimEvidenceParams{
			ID:           evidence.ID,
			OrderClaimID: evidence.OrderClaimID,
			UploadID:     evidence.UploadID,
			Caption:      evidence.Caption,
		})
	if err != nil {
		return models.ClaimEvidence{}, classify(err, codeQueryFailed,
			"could not attach the claim evidence")
	}

	return toClaimEvidence(row), nil
}

// ListClaimEvidence returns the claim's evidence, oldest first.
func (r *Repository) ListClaimEvidence(
	ctx context.Context, claimID string,
) ([]models.ClaimEvidence, error) {
	rows, err := r.queries(ctx).ListOrderClaimEvidence(ctx, claimID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not read the claim evidence")
	}

	out := make([]models.ClaimEvidence, 0, len(rows))
	for i := range rows {
		out = append(out, toClaimEvidence(rows[i]))
	}

	return out, nil
}

// DeleteClaimEvidence detaches a file from its claim.
//
// It is a HARD delete, and it is the one write in this module that is: the row
// is a BINDING rather than a record of something that happened. Detaching a file
// attached by mistake should leave no trace, and the file itself is untouched —
// it belongs to the file module, which knows nothing about this table.
func (r *Repository) DeleteClaimEvidence(ctx context.Context, id string) error {
	n, err := r.queries(ctx).DeleteOrderClaimEvidence(ctx, id)
	if err != nil {
		return classify(err, codeQueryFailed, "could not detach the claim evidence")
	}
	if n == 0 {
		return coreerrors.NotFound("order_claim_evidence_not_found",
			"the claim evidence was not found: %s", id)
	}

	return nil
}
