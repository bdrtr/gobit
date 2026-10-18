package repository

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/repository/orderdb"
)

// This file keeps an order's transfers (ADR 0450). Which transfer may be
// written, answered or accepted is the service's; the statements are
// queries/order_transfers.sql.

// codeTransferNotFound is the service's code for a transfer that does not
// exist, repeated here for the reason codeOrderNotFound is.
const codeTransferNotFound = "order_transfer_not_found"

// InsertTransfer writes a transfer and returns it as stored. A request is
// written waiting; anything else is written closed, at the database's now.
func (r *Repository) InsertTransfer(ctx context.Context, t models.OrderTransfer) (models.OrderTransfer, error) {
	row, err := r.queries(ctx).InsertOrderTransfer(ctx, orderdb.InsertOrderTransferParams{
		ID:             t.ID,
		OrderID:        t.OrderID,
		Status:         string(t.Status),
		RequestedBy:    string(t.RequestedBy),
		FromCustomerID: nullString(t.FromCustomerID),
		ToCustomerID:   t.ToCustomerID,
		FromEmail:      nullString(t.FromEmail),
		TokenHash:      t.TokenHash,
		ExpiresAt:      fromTimePtr(t.ExpiresAt),
	})
	if err != nil {
		return models.OrderTransfer{}, classify(err, codeQueryFailed, "could not write the order's transfer")
	}

	return toTransfer(row), nil
}

// GetTransfer returns the transfer; NotFound when there is none.
func (r *Repository) GetTransfer(ctx context.Context, id string) (models.OrderTransfer, error) {
	row, err := r.queries(ctx).GetOrderTransfer(ctx, id)
	if err != nil {
		return models.OrderTransfer{}, transferReadError(err, id)
	}

	return toTransfer(row), nil
}

// LockTransfer locks the transfer for the rest of the transaction and returns
// it; it is taken after the order's lock.
func (r *Repository) LockTransfer(ctx context.Context, id string) (models.OrderTransfer, error) {
	if err := requireTx(ctx, "LockTransfer"); err != nil {
		return models.OrderTransfer{}, err
	}
	row, err := r.queries(ctx).LockOrderTransfer(ctx, id)
	if err != nil {
		return models.OrderTransfer{}, transferReadError(err, id)
	}

	return toTransfer(row), nil
}

// RequestedTransfers returns the order's transfers still marked waiting,
// newest first, expired ones included.
func (r *Repository) RequestedTransfers(ctx context.Context, orderID string) ([]models.OrderTransfer, error) {
	rows, err := r.queries(ctx).RequestedOrderTransfers(ctx, orderID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not read the order's waiting transfers")
	}

	return toTransfers(rows), nil
}

// CloseTransfer moves a waiting transfer to status; Conflict when it no longer
// waits, which under the order's lock means a direct write moved it.
func (r *Repository) CloseTransfer(
	ctx context.Context, id string, status models.TransferStatus,
) (models.OrderTransfer, error) {
	row, err := r.queries(ctx).CloseOrderTransfer(ctx, orderdb.CloseOrderTransferParams{
		Status: string(status),
		ID:     id,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.OrderTransfer{}, errors.Conflict(codeStateChanged,
				"the transfer %s no longer waits", id)
		}
		return models.OrderTransfer{}, classify(err, codeQueryFailed, "could not close the transfer")
	}

	return toTransfer(row), nil
}

// ListTransfers returns a page of the order's transfers, newest first, and how
// many it has.
func (r *Repository) ListTransfers(
	ctx context.Context, orderID string, limit, offset int32,
) ([]models.OrderTransfer, int64, error) {
	rows, err := r.queries(ctx).ListOrderTransfers(ctx, orderdb.ListOrderTransfersParams{
		OrderID:     orderID,
		LimitCount:  limit,
		OffsetCount: offset,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "could not list the order's transfers")
	}
	count, err := r.queries(ctx).CountOrderTransfers(ctx, orderID)
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "could not count the order's transfers")
	}

	return toTransfers(rows), count, nil
}

// MoveOrderCustomer hands the order and the orders that add to it to the
// receiver while each still belongs to fromCustomerID, and returns the ids it
// moved.
func (r *Repository) MoveOrderCustomer(
	ctx context.Context, orderID, fromCustomerID, toCustomerID, toEmail string,
) ([]string, error) {
	moved, err := r.queries(ctx).MoveOrderCustomer(ctx, orderdb.MoveOrderCustomerParams{
		OrderID:        orderID,
		FromCustomerID: nullString(fromCustomerID),
		ToCustomerID:   nullString(toCustomerID),
		ToEmail:        nullString(toEmail),
	})
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not move the order to its new customer")
	}

	return moved, nil
}

// EraseTransferEmails empties the address a transfer asked on the given
// orders and wherever the person was the holder asked, and returns the rows
// written.
func (r *Repository) EraseTransferEmails(
	ctx context.Context, orderIDs []string, customerID, email string,
) (int64, error) {
	written, err := r.queries(ctx).EraseOrderTransferEmails(ctx, orderdb.EraseOrderTransferEmailsParams{
		OrderIds:   orderIDs,
		CustomerID: nullString(customerID),
		Email:      nullString(email),
	})
	if err != nil {
		return 0, classify(err, codeQueryFailed, "could not erase the transfers' addresses")
	}

	return written, nil
}

// TransfersForDisclosure returns the transfers of the given orders and every
// transfer naming the person.
func (r *Repository) TransfersForDisclosure(
	ctx context.Context, orderIDs []string, customerID, email string,
) ([]models.OrderTransfer, error) {
	rows, err := r.queries(ctx).OrderTransfersForDisclosure(ctx, orderdb.OrderTransfersForDisclosureParams{
		OrderIds:   orderIDs,
		CustomerID: nullString(customerID),
		Email:      nullString(email),
	})
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not read the person's transfers")
	}

	return toTransfers(rows), nil
}

// transferReadError classifies a failed read of one transfer.
func transferReadError(err error, id string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.NotFound(codeTransferNotFound, "order transfer not found: %s", id)
	}

	return classify(err, codeQueryFailed, "could not read the transfer")
}

// toTransfer converts a database row into the domain model.
func toTransfer(row orderdb.OrderTransfer) models.OrderTransfer {
	return models.OrderTransfer{
		ID:             row.ID,
		OrderID:        row.OrderID,
		Status:         models.TransferStatus(row.Status),
		RequestedBy:    models.TransferRequester(row.RequestedBy),
		FromCustomerID: stringValue(row.FromCustomerID),
		ToCustomerID:   row.ToCustomerID,
		FromEmail:      stringValue(row.FromEmail),
		TokenHash:      row.TokenHash,
		ExpiresAt:      toTimePtr(row.ExpiresAt),
		ClosedAt:       toTimePtr(row.ClosedAt),
		CreatedAt:      toTime(row.CreatedAt),
	}
}

// toTransfers converts rows into domain models.
func toTransfers(rows []orderdb.OrderTransfer) []models.OrderTransfer {
	out := make([]models.OrderTransfer, 0, len(rows))
	for i := range rows {
		out = append(out, toTransfer(rows[i]))
	}

	return out
}
