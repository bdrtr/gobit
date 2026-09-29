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
		OrderClaimID:     nullString(in.ClaimID),
		OrderExchangeID:  nullString(in.ExchangeID),
		ShippingOptionID: in.ShippingOptionID,
		LocationID:       in.LocationID,
		Note:             nullString(in.Note),
	})
	if err != nil {
		return models.Replacement{}, classify(err, codeQueryFailed,
			"could not write the replacement of %s %s", in.Source(), in.SourceID())
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

// DispatchReplacement records that the goods left, in the parcel named.
func (r *Repository) DispatchReplacement(
	ctx context.Context, id, fulfillmentID string,
) (models.Replacement, error) {
	row, err := r.queries(ctx).DispatchOrderReplacement(ctx,
		orderdb.DispatchOrderReplacementParams{ID: id, FulfillmentID: fulfillmentID})
	if err != nil {
		return models.Replacement{}, classify(err, codeQueryFailed,
			"the replacement could not be marked as dispatched: %s", id)
	}

	return toReplacement(row), nil
}

// ListReplacementsByClaim returns a claim's replacements, newest first.
func (r *Repository) ListReplacementsByClaim(
	ctx context.Context, claimID string,
) ([]models.Replacement, error) {
	rows, err := r.queries(ctx).ListOrderReplacementsByClaim(ctx, nullString(claimID))
	if err != nil {
		return nil, classify(err, codeQueryFailed,
			"could not list the replacements of claim %s", claimID)
	}

	return toReplacements(rows), nil
}

// ListReplacementsByOrder returns every replacement the order's claims and
// exchanges promised, oldest first, at most limit of them.
func (r *Repository) ListReplacementsByOrder(
	ctx context.Context, orderID string, limit int64,
) ([]models.Replacement, error) {
	rows, err := r.queries(ctx).ListOrderReplacementsByOrder(ctx, orderdb.ListOrderReplacementsByOrderParams{
		OrderID: orderID, RowLimit: limit,
	})
	if err != nil {
		return nil, classify(err, codeQueryFailed,
			"could not list the replacements of order %s", orderID)
	}

	return toReplacements(rows), nil
}

// ListReplacementsByExchange returns an exchange's replacements, newest first.
func (r *Repository) ListReplacementsByExchange(
	ctx context.Context, exchangeID string,
) ([]models.Replacement, error) {
	rows, err := r.queries(ctx).ListOrderReplacementsByExchange(ctx, nullString(exchangeID))
	if err != nil {
		return nil, classify(err, codeQueryFailed,
			"could not list the replacements of exchange %s", exchangeID)
	}

	return toReplacements(rows), nil
}

// ReplacementsByFulfillment returns the replacements a parcel carries, at most
// one in practice (ADR 0239).
func (r *Repository) ReplacementsByFulfillment(
	ctx context.Context, fulfillmentID string,
) ([]models.Replacement, error) {
	rows, err := r.queries(ctx).GetOrderReplacementByFulfillment(ctx, fulfillmentID)
	if err != nil {
		return nil, classify(err, codeQueryFailed,
			"could not find the replacement parcel %s carries", fulfillmentID)
	}

	return toReplacements(rows), nil
}

// RecallReplacement sends a dispatched replacement back to 'requested'.
func (r *Repository) RecallReplacement(ctx context.Context, id string) (models.Replacement, error) {
	row, err := r.queries(ctx).RecallOrderReplacement(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Replacement{}, coreerrors.Conflict(codeStateChanged,
				"replacement %s is no longer dispatched", id)
		}

		return models.Replacement{}, classify(err, codeQueryFailed, "could not recall replacement %s", id)
	}

	return toReplacement(row), nil
}

// ClearReplacementReservations forgets every promise a replacement's lines and
// parts held.
func (r *Repository) ClearReplacementReservations(ctx context.Context, replacementID string) error {
	if err := r.queries(ctx).ClearOrderReplacementItemReservations(ctx, replacementID); err != nil {
		return classify(err, codeQueryFailed,
			"could not clear the promises of replacement %s", replacementID)
	}
	if err := r.queries(ctx).ClearOrderReplacementItemPartReservations(ctx, replacementID); err != nil {
		return classify(err, codeQueryFailed,
			"could not clear the part promises of replacement %s", replacementID)
	}

	return nil
}

// CreateReplacementItem writes one line of a replacement.
func (r *Repository) CreateReplacementItem(
	ctx context.Context, in models.ReplacementItem,
) (models.ReplacementItem, error) {
	row, err := r.queries(ctx).CreateOrderReplacementItem(ctx,
		orderdb.CreateOrderReplacementItemParams{
			ID:                 in.ID,
			OrderReplacementID: in.ReplacementID,
			OrderLineItemID:    nullString(in.OrderLineItemID),
			VariantID:          nullString(in.VariantID),
			Quantity:           in.Quantity,
		})
	if err != nil {
		return models.ReplacementItem{}, classify(err, codeQueryFailed,
			"could not write the replacement line %s", in.Names())
	}

	out := toReplacementItem(row)
	if len(in.Parts) == 0 {
		return out, nil
	}

	variantIDs := make([]string, 0, len(in.Parts))
	quantities := make([]int64, 0, len(in.Parts))
	for _, p := range in.Parts {
		variantIDs = append(variantIDs, p.VariantID)
		quantities = append(quantities, p.Quantity)
	}
	if err := r.queries(ctx).CreateOrderReplacementItemParts(ctx,
		orderdb.CreateOrderReplacementItemPartsParams{
			ItemID: in.ID, VariantIds: variantIDs, Quantities: quantities,
		}); err != nil {
		return models.ReplacementItem{}, classify(err, codeQueryFailed,
			"could not write the parts of replacement line %s", in.Names())
	}
	out.Parts = make([]models.ReplacementItemPart, 0, len(in.Parts))
	for _, p := range in.Parts {
		out.Parts = append(out.Parts, models.ReplacementItemPart{VariantID: p.VariantID, Quantity: p.Quantity})
	}

	return out, nil
}

// SetReplacementItemReservation writes the promise a line's units are held
// under.
func (r *Repository) SetReplacementItemReservation(
	ctx context.Context, itemID, reservationID string,
) (models.ReplacementItem, error) {
	row, err := r.queries(ctx).SetOrderReplacementItemReservation(ctx,
		orderdb.SetOrderReplacementItemReservationParams{
			ID: itemID, ReservationID: reservationID,
		})
	if err != nil {
		return models.ReplacementItem{}, classify(err, codeQueryFailed,
			"the reservation of replacement line %s could not be written", itemID)
	}

	return toReplacementItem(row), nil
}

// SetReplacementItemPartReservation writes the promise one part's units are
// held under (ADR 0238).
func (r *Repository) SetReplacementItemPartReservation(
	ctx context.Context, itemID, variantID, reservationID string,
) error {
	n, err := r.queries(ctx).SetOrderReplacementItemPartReservation(ctx,
		orderdb.SetOrderReplacementItemPartReservationParams{
			ItemID: itemID, VariantID: variantID, ReservationID: reservationID,
		})
	if err != nil {
		return classify(err, codeQueryFailed,
			"the reservation of part %s of replacement line %s could not be written", variantID, itemID)
	}
	if n == 0 {
		return coreerrors.NotFound(codeReplacementNotFound,
			"replacement line %s has no part %s", itemID, variantID)
	}

	return nil
}

// ListReplacementItems returns a replacement's lines in the order they were
// written, each with its parts in their rank.
func (r *Repository) ListReplacementItems(
	ctx context.Context, replacementID string,
) ([]models.ReplacementItem, error) {
	rows, err := r.queries(ctx).ListOrderReplacementItems(ctx, replacementID)
	if err != nil {
		return nil, classify(err, codeQueryFailed,
			"could not list the lines of replacement %s", replacementID)
	}
	partRows, err := r.queries(ctx).ListOrderReplacementItemParts(ctx, replacementID)
	if err != nil {
		return nil, classify(err, codeQueryFailed,
			"could not list the parts of replacement %s", replacementID)
	}

	parts := make(map[string][]models.ReplacementItemPart, len(rows))
	for _, p := range partRows {
		parts[p.OrderReplacementItemID] = append(parts[p.OrderReplacementItemID], models.ReplacementItemPart{
			VariantID:     p.VariantID,
			Quantity:      p.Quantity,
			ReservationID: stringValue(p.ReservationID),
		})
	}

	out := make([]models.ReplacementItem, 0, len(rows))
	for i := range rows {
		item := toReplacementItem(rows[i])
		item.Parts = parts[item.ID]
		out = append(out, item)
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
		// The query excludes variant-shaped rows, so every row here names a line.
		// The pointer is dereferenced through the same helper the rest of this
		// package uses rather than with a bare *: a nil here would be a query that
		// stopped filtering, and a panic is a worse way to learn that than a zero.
		out[stringValue(rows[i].OrderLineItemID)] = rows[i].Replaced
	}

	return out, nil
}

// toReplacement converts a row into the domain model.
func toReplacement(row orderdb.OrderReplacement) models.Replacement {
	return models.Replacement{
		ID:               row.ID,
		ClaimID:          stringValue(row.OrderClaimID),
		ExchangeID:       stringValue(row.OrderExchangeID),
		Status:           models.ReplacementStatus(row.Status),
		ShippingOptionID: row.ShippingOptionID,
		LocationID:       row.LocationID,
		Note:             stringValue(row.Note),
		FulfillmentID:    stringValue(row.FulfillmentID),
		Recalls:          int(row.Recalls),
		CanceledAt:       toTimePtr(row.CanceledAt),
		DispatchedAt:     toTimePtr(row.DispatchedAt),
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
		OrderLineItemID: stringValue(row.OrderLineItemID),
		VariantID:       stringValue(row.VariantID),
		Quantity:        row.Quantity,
		ReservationID:   stringValue(row.ReservationID),
		CreatedAt:       toTime(row.CreatedAt),
		UpdatedAt:       toTime(row.UpdatedAt),
	}
}
