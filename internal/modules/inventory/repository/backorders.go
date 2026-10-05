package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/repository/inventorydb"
)

// codeBackorderNotFound reports a line with no claim on the item.
const codeBackorderNotFound = "inventory_backorder_not_found"

// CreateBackorder records a claim (ADR 0392) and reports true, or writes nothing
// and reports false when the line already has a claim on the item: the unique
// index answers ON CONFLICT DO NOTHING, which returns no row.
func (r *Repository) CreateBackorder(ctx context.Context, b models.Backorder) (models.Backorder, bool, error) {
	row, err := r.queries(ctx).CreateBackorder(ctx, inventorydb.CreateBackorderParams{
		ID:              b.ID,
		InventoryItemID: b.InventoryItemID,
		OrderID:         b.OrderID,
		OrderLineItemID: b.OrderLineItemID,
		Quantity:        b.Quantity,
		LocationIds:     nonNilStrings(b.LocationIDs),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Backorder{}, false, nil
	}
	if err != nil {
		return models.Backorder{}, false, classify(err, codeQueryFailed, "the backorder could not be recorded")
	}

	return toBackorder(row), true, nil
}

// GetBackorderOfLine returns the line's claim on the item, or NotFound.
func (r *Repository) GetBackorderOfLine(ctx context.Context, orderLineItemID, itemID string) (models.Backorder, error) {
	row, err := r.queries(ctx).GetBackorderOfLine(ctx, inventorydb.GetBackorderOfLineParams{
		OrderLineItemID: orderLineItemID,
		InventoryItemID: itemID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Backorder{}, coreerrors.NotFound(codeBackorderNotFound,
			"line %s has no backorder on item %s", orderLineItemID, itemID)
	}
	if err != nil {
		return models.Backorder{}, classify(err, codeQueryFailed, "the backorder could not be read")
	}

	return toBackorder(row), nil
}

// LockBackordersOfLine locks the line's claims in queue order; it is refused
// outside a transaction, as every Lock method is.
func (r *Repository) LockBackordersOfLine(ctx context.Context, orderLineItemID string) ([]models.Backorder, error) {
	if err := requireTx(ctx, "LockBackordersOfLine"); err != nil {
		return nil, err
	}
	rows, err := r.queries(ctx).LockBackordersOfLine(ctx, orderLineItemID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the backorders of the line could not be locked")
	}

	return toBackorders(rows), nil
}

// LockWaitingBackordersAt locks the item's waiting claims that name the location
// and owe no more than available, in queue order.
func (r *Repository) LockWaitingBackordersAt(
	ctx context.Context, itemID, locationID string, available int64,
) ([]models.Backorder, error) {
	if err := requireTx(ctx, "LockWaitingBackordersAt"); err != nil {
		return nil, err
	}
	rows, err := r.queries(ctx).LockWaitingBackordersAt(ctx, inventorydb.LockWaitingBackordersAtParams{
		InventoryItemID: itemID,
		LocationID:      locationID,
		Available:       available,
	})
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the waiting backorders could not be locked")
	}

	return toBackorders(rows), nil
}

// FillBackorder marks a waiting claim filled and returns how many rows changed.
func (r *Repository) FillBackorder(ctx context.Context, id, reservationID, locationID string) (int64, error) {
	affected, err := r.queries(ctx).FillBackorder(ctx, inventorydb.FillBackorderParams{
		ID:            id,
		ReservationID: reservationID,
		LocationID:    locationID,
	})
	if err != nil {
		return 0, classify(err, codeQueryFailed, "the backorder could not be marked filled")
	}

	return affected, nil
}

// WithdrawBackorder writes a claim's withdrawn units and status.
func (r *Repository) WithdrawBackorder(
	ctx context.Context, id string, withdrawn int64, status models.BackorderStatus,
) (models.Backorder, error) {
	row, err := r.queries(ctx).WithdrawBackorder(ctx, inventorydb.WithdrawBackorderParams{
		ID:                id,
		WithdrawnQuantity: withdrawn,
		Status:            status.String(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Backorder{}, coreerrors.NotFound(codeBackorderNotFound, "there is no backorder %s", id)
	}
	if err != nil {
		return models.Backorder{}, classify(err, codeQueryFailed, "the backorder could not be withdrawn")
	}

	return toBackorder(row), nil
}

// CountWaitingBackorders returns how many of the item's claims still wait.
func (r *Repository) CountWaitingBackorders(ctx context.Context, itemID string) (int64, error) {
	count, err := r.queries(ctx).CountWaitingBackorders(ctx, itemID)
	if err != nil {
		return 0, classify(err, codeQueryFailed, "the waiting backorders could not be counted")
	}

	return count, nil
}

// ListBackorders pages an item's claims in queue order; the total comes from a
// separate query, as every listing's here does.
func (r *Repository) ListBackorders(
	ctx context.Context, filter models.BackorderFilter,
) ([]models.Backorder, int64, error) {
	status := nullString(filter.Status.String())
	rows, err := r.queries(ctx).ListBackorders(ctx, inventorydb.ListBackordersParams{
		InventoryItemID: filter.InventoryItemID,
		Status:          status,
		RowLimit:        filter.Limit,
		RowOffset:       filter.Offset,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "the backorders could not be listed")
	}
	total, err := r.queries(ctx).CountBackorders(ctx, inventorydb.CountBackordersParams{
		InventoryItemID: filter.InventoryItemID,
		Status:          status,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "the backorders could not be counted")
	}

	return toBackorders(rows), total, nil
}

// OpenStockLocationIDs lists every open location.
func (r *Repository) OpenStockLocationIDs(ctx context.Context) ([]string, error) {
	ids, err := r.queries(ctx).OpenStockLocationIDs(ctx)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the open locations could not be listed")
	}

	return ids, nil
}

// toBackorders converts the generated rows.
func toBackorders(rows []inventorydb.InventoryBackorder) []models.Backorder {
	out := make([]models.Backorder, 0, len(rows))
	for i := range rows {
		out = append(out, toBackorder(rows[i]))
	}

	return out
}

// toBackorder converts the generated row into the domain model.
func toBackorder(row inventorydb.InventoryBackorder) models.Backorder {
	var seq int64
	if row.Seq != nil {
		seq = *row.Seq
	}

	return models.Backorder{
		ID:                row.ID,
		InventoryItemID:   row.InventoryItemID,
		OrderID:           row.OrderID,
		OrderLineItemID:   row.OrderLineItemID,
		Quantity:          row.Quantity,
		WithdrawnQuantity: row.WithdrawnQuantity,
		LocationIDs:       nonNilStrings(row.LocationIds),
		Status:            models.BackorderStatus(row.Status),
		ReservationID:     stringValue(row.ReservationID),
		FilledLocationID:  stringValue(row.FilledLocationID),
		Seq:               seq,
		CreatedAt:         timeValue(row.CreatedAt),
		UpdatedAt:         timeValue(row.UpdatedAt),
	}
}

// nonNilStrings turns nil into an empty slice: the column is NOT NULL, and an
// empty array is the claim the checkout could not rank.
func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}

	return in
}
