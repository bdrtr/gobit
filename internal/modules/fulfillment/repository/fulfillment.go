package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/repository/fulfillmentdb"
)

// This file is the access to FULFILLMENTS and their items. The access to the
// shipping catalog is in shipping.go.

// InsertFulfillmentIfAbsent writes the fulfillment only if the idempotency key
// has not been used yet.
//
// The second return value is whether the row was written; a conflict IS NOT AN
// ERROR. The losing side waits until the winner's transaction ends and then
// reads the completed row with [Repository.FulfillmentByIdempotencyKey].
func (r *Repository) InsertFulfillmentIfAbsent(
	ctx context.Context,
	ful models.Fulfillment,
) (models.Fulfillment, bool, error) {
	meta, err := fromJSONMap(ful.Metadata)
	if err != nil {
		return models.Fulfillment{}, false, err
	}

	row, err := r.queries(ctx).InsertFulfillmentIfAbsent(ctx, fulfillmentdb.InsertFulfillmentIfAbsentParams{
		ID:               ful.ID,
		Reference:        ful.Reference,
		ShippingOptionID: ful.ShippingOptionID,
		ProviderID:       ful.ProviderID,
		Status:           ful.Status.String(),
		IdempotencyKey:   ful.IdempotencyKey,
		Data:             fromJSONRaw(ful.Data),
		Metadata:         meta,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Fulfillment{}, false, nil
		}
		return models.Fulfillment{}, false, classify(err, codeQueryFailed, "could not create fulfillment")
	}
	created, err := toFulfillment(row)
	if err != nil {
		return models.Fulfillment{}, false, err
	}
	return created, true, nil
}

// GetFulfillment returns the fulfillment by its identifier; NotFound if there is
// none. Items ARE NOT FILLED IN; they are read with
// [Repository.ListFulfillmentItems].
func (r *Repository) GetFulfillment(ctx context.Context, id string) (models.Fulfillment, error) {
	row, err := r.queries(ctx).GetFulfillment(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Fulfillment{}, fulfillmentNotFound(id)
		}
		return models.Fulfillment{}, classify(err, codeQueryFailed, "could not read fulfillment")
	}
	return toFulfillment(row)
}

// FulfillmentByIdempotencyKey returns the fulfillment created with the same key;
// NotFound if there is none.
func (r *Repository) FulfillmentByIdempotencyKey(
	ctx context.Context,
	key string,
) (models.Fulfillment, error) {
	row, err := r.queries(ctx).GetFulfillmentByIdempotencyKey(ctx, key)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Fulfillment{}, errors.NotFound(codeFulfillmentNotFound,
				"there is no fulfillment created with this idempotency key: %s", key)
		}
		return models.Fulfillment{}, classify(err, codeQueryFailed, "could not read fulfillment")
	}
	return toFulfillment(row)
}

// LockFulfillment locks the fulfillment for the duration of the transaction and
// returns its current state. Status transitions are only made under this lock.
func (r *Repository) LockFulfillment(ctx context.Context, id string) (models.Fulfillment, error) {
	if err := requireTx(ctx, "LockFulfillment"); err != nil {
		return models.Fulfillment{}, err
	}
	row, err := r.queries(ctx).LockFulfillment(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Fulfillment{}, fulfillmentNotFound(id)
		}
		return models.Fulfillment{}, classify(err, codeQueryFailed, "could not lock fulfillment")
	}
	return toFulfillment(row)
}

// ListFulfillments returns the fulfillments filtered and paginated.
// The second return value is the count of ALL rows matching the filter.
func (r *Repository) ListFulfillments(
	ctx context.Context,
	filter models.FulfillmentFilter,
) ([]models.Fulfillment, int64, error) {
	rows, err := r.queries(ctx).ListFulfillments(ctx, fulfillmentdb.ListFulfillmentsParams{
		Reference: filter.Reference,
		Status:    filter.Status,
		RowLimit:  filter.Limit,
		RowOffset: filter.Offset,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "could not list fulfillments")
	}

	total, err := r.queries(ctx).CountFulfillments(ctx, fulfillmentdb.CountFulfillmentsParams{
		Reference: filter.Reference,
		Status:    filter.Status,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "could not count fulfillments")
	}

	out := make([]models.Fulfillment, 0, len(rows))
	for i := range rows {
		ful, convErr := toFulfillment(rows[i])
		if convErr != nil {
			return nil, 0, convErr
		}
		out = append(out, ful)
	}
	return out, total, nil
}

// UpdateFulfillmentProviderResult writes the provider's response into the
// fulfillment row.
func (r *Repository) UpdateFulfillmentProviderResult(
	ctx context.Context,
	id, externalID string,
	status models.FulfillmentStatus,
	trackingNumber, trackingURL string,
	data []byte,
	shippedAt, deliveredAt, canceledAt *time.Time,
) (models.Fulfillment, error) {
	row, err := r.queries(ctx).UpdateFulfillmentProviderResult(ctx,
		fulfillmentdb.UpdateFulfillmentProviderResultParams{
			ID:             id,
			ExternalID:     externalID,
			Status:         status.String(),
			TrackingNumber: trackingNumber,
			TrackingUrl:    trackingURL,
			Data:           fromJSONRaw(data),
			ShippedAt:      fromTimePtr(shippedAt),
			DeliveredAt:    fromTimePtr(deliveredAt),
			CanceledAt:     fromTimePtr(canceledAt),
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Fulfillment{}, fulfillmentNotFound(id)
		}
		return models.Fulfillment{}, classify(err, codeQueryFailed, "could not update fulfillment")
	}
	return toFulfillment(row)
}

// UpdateFulfillmentStatus writes the fulfillment's status, tracking information
// and timestamps with ABSOLUTE values.
func (r *Repository) UpdateFulfillmentStatus(
	ctx context.Context,
	id string,
	status models.FulfillmentStatus,
	trackingNumber, trackingURL string,
	shippedAt, deliveredAt, canceledAt *time.Time,
) (models.Fulfillment, error) {
	row, err := r.queries(ctx).UpdateFulfillmentStatus(ctx, fulfillmentdb.UpdateFulfillmentStatusParams{
		ID:             id,
		Status:         status.String(),
		TrackingNumber: trackingNumber,
		TrackingUrl:    trackingURL,
		ShippedAt:      fromTimePtr(shippedAt),
		DeliveredAt:    fromTimePtr(deliveredAt),
		CanceledAt:     fromTimePtr(canceledAt),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Fulfillment{}, fulfillmentNotFound(id)
		}
		return models.Fulfillment{}, classify(err, codeQueryFailed, "could not update fulfillment status")
	}
	return toFulfillment(row)
}

// --- fulfillment items -------------------------------------------------------

// CreateFulfillmentItem adds an item to the fulfillment.
func (r *Repository) CreateFulfillmentItem(
	ctx context.Context,
	item models.FulfillmentItem,
) (models.FulfillmentItem, error) {
	row, err := r.queries(ctx).CreateFulfillmentItem(ctx, fulfillmentdb.CreateFulfillmentItemParams{
		ID:            item.ID,
		FulfillmentID: item.FulfillmentID,
		LineItemID:    item.LineItemID,
		Quantity:      item.Quantity,
	})
	if err != nil {
		return models.FulfillmentItem{}, classify(err, codeQueryFailed, "could not add fulfillment item")
	}
	return toItem(row), nil
}

// ListFulfillmentItems returns the items of a fulfillment.
func (r *Repository) ListFulfillmentItems(
	ctx context.Context,
	fulfillmentID string,
) ([]models.FulfillmentItem, error) {
	rows, err := r.queries(ctx).ListFulfillmentItems(ctx, fulfillmentID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not list fulfillment items")
	}
	out := make([]models.FulfillmentItem, 0, len(rows))
	for i := range rows {
		out = append(out, toItem(rows[i]))
	}
	return out, nil
}

// FulfillmentItemsByFulfillments returns the items for MORE THAN ONE fulfillment
// in a SINGLE query (no N+1).
func (r *Repository) FulfillmentItemsByFulfillments(
	ctx context.Context,
	fulfillmentIDs []string,
) ([]models.FulfillmentItem, error) {
	if len(fulfillmentIDs) == 0 {
		return []models.FulfillmentItem{}, nil
	}
	rows, err := r.queries(ctx).ListFulfillmentItemsByFulfillments(ctx, fulfillmentIDs)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not list fulfillment items")
	}
	out := make([]models.FulfillmentItem, 0, len(rows))
	for i := range rows {
		out = append(out, toItem(rows[i]))
	}
	return out, nil
}

// --- the manual provider's ledger --------------------------------------------

// InsertManualShipmentIfAbsent writes the provider shipment only if the
// idempotency key has not been used yet.
//
// The second return value is whether the row was written; a conflict IS NOT AN
// ERROR.
func (r *Repository) InsertManualShipmentIfAbsent(
	ctx context.Context,
	shipment models.ManualShipment,
) (models.ManualShipment, bool, error) {
	row, err := r.queries(ctx).InsertManualShipmentIfAbsent(ctx,
		fulfillmentdb.InsertManualShipmentIfAbsentParams{
			ID:             shipment.ID,
			IdempotencyKey: shipment.IdempotencyKey,
			Reference:      shipment.Reference,
			OptionID:       shipment.OptionID,
			Status:         shipment.Status.String(),
			TrackingNumber: shipment.TrackingNumber,
			TrackingUrl:    shipment.TrackingURL,
			Data:           fromJSONRaw(shipment.Data),
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ManualShipment{}, false, nil
		}
		return models.ManualShipment{}, false, classify(err, codeQueryFailed,
			"could not create manual provider shipment")
	}
	return toManualShipment(row), true, nil
}

// ManualShipment returns the provider shipment by its identifier; NotFound if
// there is none.
func (r *Repository) ManualShipment(ctx context.Context, id string) (models.ManualShipment, error) {
	row, err := r.queries(ctx).GetManualShipment(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ManualShipment{}, manualShipmentNotFound(id)
		}
		return models.ManualShipment{}, classify(err, codeQueryFailed,
			"could not read manual provider shipment")
	}
	return toManualShipment(row), nil
}

// ManualShipmentByIdempotencyKey returns the provider shipment by its key;
// NotFound if there is none.
func (r *Repository) ManualShipmentByIdempotencyKey(
	ctx context.Context,
	key string,
) (models.ManualShipment, error) {
	row, err := r.queries(ctx).GetManualShipmentByIdempotencyKey(ctx, key)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ManualShipment{}, errors.NotFound(codeManualShipmentNotFound,
				"there is no provider shipment opened with this idempotency key: %s", key)
		}
		return models.ManualShipment{}, classify(err, codeQueryFailed,
			"could not read manual provider shipment")
	}
	return toManualShipment(row), nil
}

// LockManualShipment locks the provider shipment for the duration of the
// transaction and returns its current state.
func (r *Repository) LockManualShipment(ctx context.Context, id string) (models.ManualShipment, error) {
	if err := requireTx(ctx, "LockManualShipment"); err != nil {
		return models.ManualShipment{}, err
	}
	row, err := r.queries(ctx).LockManualShipment(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ManualShipment{}, manualShipmentNotFound(id)
		}
		return models.ManualShipment{}, classify(err, codeQueryFailed,
			"could not lock manual provider shipment")
	}
	return toManualShipment(row), nil
}

// UpdateManualShipmentState writes the status and the tracking information in
// the provider ledger with ABSOLUTE values.
func (r *Repository) UpdateManualShipmentState(
	ctx context.Context,
	id string,
	status models.FulfillmentStatus,
	trackingNumber, trackingURL string,
) (models.ManualShipment, error) {
	row, err := r.queries(ctx).UpdateManualShipmentState(ctx,
		fulfillmentdb.UpdateManualShipmentStateParams{
			ID:             id,
			Status:         status.String(),
			TrackingNumber: trackingNumber,
			TrackingUrl:    trackingURL,
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ManualShipment{}, manualShipmentNotFound(id)
		}
		return models.ManualShipment{}, classify(err, codeQueryFailed,
			"could not update manual provider shipment")
	}
	return toManualShipment(row), nil
}
