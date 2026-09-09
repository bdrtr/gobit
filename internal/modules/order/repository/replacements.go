package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/repository/orderdb"
)

// CreateReplacement writes a replacement record.
func (r *Repository) CreateReplacement(
	ctx context.Context, in models.Replacement,
) (models.Replacement, error) {
	row, err := r.queries(ctx).CreateOrderReplacement(ctx, orderdb.CreateOrderReplacementParams{
		ID:               in.ID,
		OrderClaimID:     in.ClaimID,
		ShippingOptionID: in.ShippingOptionID,
		LocationID:       in.LocationID,
		Note:             nullString(in.Note),
	})
	if err != nil {
		return models.Replacement{}, classify(err, codeQueryFailed,
			"could not write the replacement of claim %s", in.ClaimID)
	}

	return toReplacement(row), nil
}

// GetReplacement reads a replacement by id.
func (r *Repository) GetReplacement(
	ctx context.Context, id string,
) (models.Replacement, error) {
	row, err := r.queries(ctx).GetOrderReplacement(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Replacement{}, coreerrors.NotFound(codeReplacementNotFound,
				"replacement record not found: %s", id)
		}

		return models.Replacement{}, classify(err, codeQueryFailed,
			"could not read the replacement record")
	}

	return toReplacement(row), nil
}

// LockReplacement reads a replacement and holds its row until the transaction
// ends.
//
// The transition reads the status and writes the next one; without the lock two
// withdrawals could each read 'requested' and both write a moment.
func (r *Repository) LockReplacement(
	ctx context.Context, id string,
) (models.Replacement, error) {
	row, err := r.queries(ctx).GetOrderReplacementForUpdate(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Replacement{}, coreerrors.NotFound(codeReplacementNotFound,
				"replacement record not found: %s", id)
		}

		return models.Replacement{}, classify(err, codeQueryFailed,
			"could not lock the replacement record")
	}

	return toReplacement(row), nil
}

// CancelReplacement withdraws the request.
func (r *Repository) CancelReplacement(
	ctx context.Context, id string,
) (models.Replacement, error) {
	row, err := r.queries(ctx).CancelOrderReplacement(ctx, id)
	if err != nil {
		return models.Replacement{}, classify(err, codeQueryFailed,
			"the replacement could not be canceled: %s", id)
	}

	return toReplacement(row), nil
}

// ListReplacementsByClaim returns a claim's replacements, newest first.
func (r *Repository) ListReplacementsByClaim(
	ctx context.Context, claimID string,
) ([]models.Replacement, error) {
	rows, err := r.queries(ctx).ListOrderReplacementsByClaim(ctx, claimID)
	if err != nil {
		return nil, classify(err, codeQueryFailed,
			"could not list the replacements of claim %s", claimID)
	}

	return toReplacements(rows), nil
}

// CreateReplacementItem writes one line of a replacement.
func (r *Repository) CreateReplacementItem(
	ctx context.Context, in models.ReplacementItem,
) (models.ReplacementItem, error) {
	row, err := r.queries(ctx).CreateOrderReplacementItem(ctx,
		orderdb.CreateOrderReplacementItemParams{
			ID:                 in.ID,
			OrderReplacementID: in.ReplacementID,
			OrderLineItemID:    in.OrderLineItemID,
			Quantity:           in.Quantity,
		})
	if err != nil {
		return models.ReplacementItem{}, classify(err, codeQueryFailed,
			"could not write the replacement line %s", in.OrderLineItemID)
	}

	return toReplacementItem(row), nil
}

// ListReplacementItems returns a replacement's lines in the order they were
// written.
func (r *Repository) ListReplacementItems(
	ctx context.Context, replacementID string,
) ([]models.ReplacementItem, error) {
	rows, err := r.queries(ctx).ListOrderReplacementItems(ctx, replacementID)
	if err != nil {
		return nil, classify(err, codeQueryFailed,
			"could not list the lines of replacement %s", replacementID)
	}

	out := make([]models.ReplacementItem, 0, len(rows))
	for i := range rows {
		out = append(out, toReplacementItem(rows[i]))
	}

	return out, nil
}

// ReplacedQuantities reports how many units of each given order line have
// already been promised across the order's live replacements.
//
// A line that has never been replaced is ABSENT from the map rather than
// present with a zero, for the reason [Repository.ReturnedQuantities] gives:
// the query returns rows, not a census.
func (r *Repository) ReplacedQuantities(
	ctx context.Context, lineItemIDs []string,
) (map[string]int64, error) {
	out := make(map[string]int64, len(lineItemIDs))
	if len(lineItemIDs) == 0 {
		return out, nil
	}

	rows, err := r.queries(ctx).SumReplacedQuantities(ctx, lineItemIDs)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not sum the replaced quantities")
	}

	for i := range rows {
		out[rows[i].OrderLineItemID] = rows[i].Replaced
	}

	return out, nil
}

// toReplacement converts a row into the domain model.
func toReplacement(row orderdb.OrderReplacement) models.Replacement {
	return models.Replacement{
		ID:               row.ID,
		ClaimID:          row.OrderClaimID,
		Status:           models.ReplacementStatus(row.Status),
		ShippingOptionID: row.ShippingOptionID,
		LocationID:       row.LocationID,
		Note:             stringValue(row.Note),
		CanceledAt:       toTimePtr(row.CanceledAt),
		CreatedAt:        toTime(row.CreatedAt),
		UpdatedAt:        toTime(row.UpdatedAt),
	}
}

// toReplacements converts a row slice into domain models.
func toReplacements(rows []orderdb.OrderReplacement) []models.Replacement {
	out := make([]models.Replacement, 0, len(rows))
	for i := range rows {
		out = append(out, toReplacement(rows[i]))
	}

	return out
}

// toReplacementItem converts a row into the domain model.
func toReplacementItem(row orderdb.OrderReplacementItem) models.ReplacementItem {
	return models.ReplacementItem{
		ID:              row.ID,
		ReplacementID:   row.OrderReplacementID,
		OrderLineItemID: row.OrderLineItemID,
		Quantity:        row.Quantity,
		CreatedAt:       toTime(row.CreatedAt),
		UpdatedAt:       toTime(row.UpdatedAt),
	}
}
