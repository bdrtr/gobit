package repository

// This file is English because ADR 0012 makes language a property of the FILE
// and every new file is English; repository.go beside it stays Turkish.

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/repository/inventorydb"
)

// AppendMovement records one change to the physical count.
//
// # It REFUSES to run outside a transaction, and that is the whole gate
//
// ADR 0068 keeps stocked_quantity authoritative and lets the ledger EXPLAIN it,
// which leaves exactly one way for the two to disagree: one of them being
// written without the other. The service writes the pair inside one
// [Repository.WithTx], and this check is what makes that structural rather than
// a convention — a movement appended on the pool would commit while the level
// update it explains rolled back, and the ledger would report units that never
// moved.
//
// It is the same refusal the Lock methods make, for a related reason: there too
// the call is meaningless outside a transaction, and there too being silently
// meaningless is worse than failing.
func (r *Repository) AppendMovement(ctx context.Context, mv models.Movement) (models.Movement, error) {
	if err := requireTx(ctx, "AppendMovement"); err != nil {
		return models.Movement{}, err
	}

	row, err := r.queries(ctx).AppendMovement(ctx, inventorydb.AppendMovementParams{
		ID:              mv.ID,
		InventoryItemID: mv.InventoryItemID,
		LocationID:      mv.LocationID,
		ReservationID:   nullString(mv.ReservationID),
		Reason:          mv.Reason.String(),
		Delta:           mv.Delta,
		StockedAfter:    mv.StockedAfter,
	})
	if err != nil {
		return models.Movement{}, classify(err, codeQueryFailed, "the stock movement could not be recorded")
	}

	return toMovement(row), nil
}

// ListMovements pages one item's movements, newest first.
//
// It takes no lock and needs none: the ledger is append-only, so a row a reader
// sees will not change under it, and a row arriving during the walk lands at the
// TOP of the order — above a keyset position that is already below it — which
// is precisely why the paging is keyset rather than offset.
func (r *Repository) ListMovements(ctx context.Context, filter models.MovementFilter) ([]models.Movement, error) {
	// The cursor reaches SQL as NULL when it names no position, and the
	// COALESCE sentinels in the query turn that into "start at the top".
	// Sending a zero TIME instead would compare every row against year one and
	// bring back an empty first page with no error anywhere.
	afterAt := pgtype.Timestamptz{}
	if !filter.After.Time.IsZero() {
		afterAt = pgtype.Timestamptz{Time: filter.After.Time, Valid: true}
	}

	rows, err := r.queries(ctx).ListMovementsForItem(ctx, inventorydb.ListMovementsForItemParams{
		InventoryItemID: filter.InventoryItemID,
		LocationID:      nullString(filter.LocationID),
		AfterAt:         afterAt,
		AfterID:         nullString(filter.After.ID),
		RowLimit:        filter.Limit,
	})
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the stock movements could not be listed")
	}

	out := make([]models.Movement, 0, len(rows))
	for i := range rows {
		out = append(out, toMovement(rows[i]))
	}

	return out, nil
}

// toMovement turns the generated row into the module's model.
func toMovement(row inventorydb.InventoryMovement) models.Movement {
	return models.Movement{
		ID:              row.ID,
		InventoryItemID: row.InventoryItemID,
		LocationID:      row.LocationID,
		ReservationID:   stringValue(row.ReservationID),
		Reason:          models.MovementReason(row.Reason),
		Delta:           row.Delta,
		StockedAfter:    row.StockedAfter,
		CreatedAt:       timeValue(row.CreatedAt),
	}
}
