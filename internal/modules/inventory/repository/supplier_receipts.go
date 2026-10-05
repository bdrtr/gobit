package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/repository/inventorydb"
)

// codeSupplierReceiptNotFound reports a receipt that does not exist.
const codeSupplierReceiptNotFound = "inventory_supplier_receipt_not_found"

// CreateSupplierReceipt records an expected supplier receipt (ADR 0399).
func (r *Repository) CreateSupplierReceipt(ctx context.Context, rec models.SupplierReceipt) (models.SupplierReceipt, error) {
	row, err := r.queries(ctx).CreateSupplierReceipt(ctx, inventorydb.CreateSupplierReceiptParams{
		ID:              rec.ID,
		InventoryItemID: rec.InventoryItemID,
		LocationID:      rec.LocationID,
		Quantity:        rec.Quantity,
		ExpectedAt:      pgtype.Timestamptz{Time: rec.ExpectedAt, Valid: true},
		Reference:       nullString(rec.Reference),
	})
	if err != nil {
		return models.SupplierReceipt{}, classify(err, codeQueryFailed, "the supplier receipt could not be recorded")
	}

	return toSupplierReceipt(row), nil
}

// GetSupplierReceipt returns the receipt without locking it, or NotFound.
func (r *Repository) GetSupplierReceipt(ctx context.Context, id string) (models.SupplierReceipt, error) {
	row, err := r.queries(ctx).GetSupplierReceipt(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.SupplierReceipt{}, coreerrors.NotFound(codeSupplierReceiptNotFound,
			"there is no supplier receipt %s", id)
	}
	if err != nil {
		return models.SupplierReceipt{}, classify(err, codeQueryFailed, "the supplier receipt could not be read")
	}

	return toSupplierReceipt(row), nil
}

// LockSupplierReceipt locks the receipt for the transaction and returns it; it
// is refused outside a transaction, as every Lock method is.
func (r *Repository) LockSupplierReceipt(ctx context.Context, id string) (models.SupplierReceipt, error) {
	if err := requireTx(ctx, "LockSupplierReceipt"); err != nil {
		return models.SupplierReceipt{}, err
	}
	row, err := r.queries(ctx).LockSupplierReceipt(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.SupplierReceipt{}, coreerrors.NotFound(codeSupplierReceiptNotFound,
			"there is no supplier receipt %s", id)
	}
	if err != nil {
		return models.SupplierReceipt{}, classify(err, codeQueryFailed, "the supplier receipt could not be locked")
	}

	return toSupplierReceipt(row), nil
}

// ReceiveSupplierReceipt closes an expected receipt with the count and the
// moment of the supplier_receipt movement naming it, and returns how many rows
// changed.
func (r *Repository) ReceiveSupplierReceipt(ctx context.Context, id string) (int64, error) {
	affected, err := r.queries(ctx).ReceiveSupplierReceipt(ctx, id)
	if err != nil {
		return 0, classify(err, codeQueryFailed, "the supplier receipt could not be closed as received")
	}

	return affected, nil
}

// CancelSupplierReceipt closes an expected receipt as canceled and returns how
// many rows changed.
func (r *Repository) CancelSupplierReceipt(ctx context.Context, id string) (int64, error) {
	affected, err := r.queries(ctx).CancelSupplierReceipt(ctx, id)
	if err != nil {
		return 0, classify(err, codeQueryFailed, "the supplier receipt could not be canceled")
	}

	return affected, nil
}

// ListSupplierReceipts pages an item's receipts by expected moment; the total
// comes from a separate query, as every listing's here does.
func (r *Repository) ListSupplierReceipts(
	ctx context.Context, filter models.SupplierReceiptFilter,
) ([]models.SupplierReceipt, int64, error) {
	status := nullString(filter.Status.String())
	rows, err := r.queries(ctx).ListSupplierReceipts(ctx, inventorydb.ListSupplierReceiptsParams{
		InventoryItemID: filter.InventoryItemID,
		Status:          status,
		RowLimit:        filter.Limit,
		RowOffset:       filter.Offset,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "the supplier receipts could not be listed")
	}
	total, err := r.queries(ctx).CountSupplierReceipts(ctx, inventorydb.CountSupplierReceiptsParams{
		InventoryItemID: filter.InventoryItemID,
		Status:          status,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "the supplier receipts could not be counted")
	}

	return toSupplierReceipts(rows), total, nil
}

// CountExpectedSupplierReceipts returns how many of the item's receipts are
// still expected.
func (r *Repository) CountExpectedSupplierReceipts(ctx context.Context, itemID string) (int64, error) {
	count, err := r.queries(ctx).CountExpectedSupplierReceipts(ctx, itemID)
	if err != nil {
		return 0, classify(err, codeQueryFailed, "the expected supplier receipts could not be counted")
	}

	return count, nil
}

// CountExpectedSupplierReceiptsAtLocation returns how many receipts the
// location still expects.
func (r *Repository) CountExpectedSupplierReceiptsAtLocation(ctx context.Context, locationID string) (int64, error) {
	count, err := r.queries(ctx).CountExpectedSupplierReceiptsAtLocation(ctx, locationID)
	if err != nil {
		return 0, classify(err, codeQueryFailed, "the supplier receipts the location expects could not be counted")
	}

	return count, nil
}

// ExpectedSupplierReceiptsOfItems returns the items' receipts still expected
// and not yet due, by item and expected moment.
func (r *Repository) ExpectedSupplierReceiptsOfItems(ctx context.Context, itemIDs []string) ([]models.SupplierReceipt, error) {
	rows, err := r.queries(ctx).ExpectedSupplierReceiptsOfItems(ctx, itemIDs)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the expected supplier receipts could not be read")
	}

	return toSupplierReceipts(rows), nil
}

// WaitingBackordersOfItems returns the items' waiting claims in queue order,
// unlocked.
func (r *Repository) WaitingBackordersOfItems(ctx context.Context, itemIDs []string) ([]models.Backorder, error) {
	rows, err := r.queries(ctx).WaitingBackordersOfItems(ctx, itemIDs)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the waiting backorders could not be read")
	}

	return toBackorders(rows), nil
}

// toSupplierReceipts converts the generated rows.
func toSupplierReceipts(rows []inventorydb.InventorySupplierReceipt) []models.SupplierReceipt {
	out := make([]models.SupplierReceipt, 0, len(rows))
	for i := range rows {
		out = append(out, toSupplierReceipt(rows[i]))
	}

	return out
}

// toSupplierReceipt converts the generated row into the domain model.
func toSupplierReceipt(row inventorydb.InventorySupplierReceipt) models.SupplierReceipt {
	var received int64
	if row.ReceivedQuantity != nil {
		received = *row.ReceivedQuantity
	}

	return models.SupplierReceipt{
		ID:               row.ID,
		InventoryItemID:  row.InventoryItemID,
		LocationID:       row.LocationID,
		Quantity:         row.Quantity,
		ExpectedAt:       timeValue(row.ExpectedAt),
		Reference:        stringValue(row.Reference),
		Status:           models.SupplierReceiptStatus(row.Status),
		ReceivedQuantity: received,
		ReceivedAt:       timePointer(row.ReceivedAt),
		CanceledAt:       timePointer(row.CanceledAt),
		CreatedAt:        timeValue(row.CreatedAt),
		UpdatedAt:        timeValue(row.UpdatedAt),
	}
}
