package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/repository/fulfillmentdb"
)

// Bu dosya GÖNDERİLERİN ve kalemlerinin erişimidir. Kargo kataloğunun erişimi
// shipping.go'dadır.

// InsertFulfillmentIfAbsent gönderiyi yalnızca idempotency anahtarı henüz
// kullanılmamışsa yazar.
//
// İkinci dönüş değeri satırın yazılıp yazılmadığıdır; çakışma HATA DEĞİLDİR.
// Kaybeden taraf, kazananın işlemi bitene kadar bekler ve sonra
// [Repository.FulfillmentByIdempotencyKey] ile tamamlanmış satırı okur.
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
		return models.Fulfillment{}, false, classify(err, codeQueryFailed, "gönderi oluşturulamadı")
	}
	created, err := toFulfillment(row)
	if err != nil {
		return models.Fulfillment{}, false, err
	}
	return created, true, nil
}

// GetFulfillment gönderiyi kimliğiyle döner; yoksa NotFound.
// Kalemler DOLDURULMAZ; onlar [Repository.ListFulfillmentItems] ile okunur.
func (r *Repository) GetFulfillment(ctx context.Context, id string) (models.Fulfillment, error) {
	row, err := r.queries(ctx).GetFulfillment(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Fulfillment{}, fulfillmentNotFound(id)
		}
		return models.Fulfillment{}, classify(err, codeQueryFailed, "gönderi okunamadı")
	}
	return toFulfillment(row)
}

// FulfillmentByIdempotencyKey aynı anahtarla oluşturulmuş gönderiyi döner;
// yoksa NotFound.
func (r *Repository) FulfillmentByIdempotencyKey(
	ctx context.Context,
	key string,
) (models.Fulfillment, error) {
	row, err := r.queries(ctx).GetFulfillmentByIdempotencyKey(ctx, key)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Fulfillment{}, errors.NotFound(codeFulfillmentNotFound,
				"bu idempotency anahtarıyla oluşturulmuş bir gönderi yok: %s", key)
		}
		return models.Fulfillment{}, classify(err, codeQueryFailed, "gönderi okunamadı")
	}
	return toFulfillment(row)
}

// LockFulfillment gönderiyi işlem boyunca kilitler ve güncel hâlini döner.
// Durum geçişleri yalnızca bu kilit altında yapılır.
func (r *Repository) LockFulfillment(ctx context.Context, id string) (models.Fulfillment, error) {
	if err := requireTx(ctx, "LockFulfillment"); err != nil {
		return models.Fulfillment{}, err
	}
	row, err := r.queries(ctx).LockFulfillment(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Fulfillment{}, fulfillmentNotFound(id)
		}
		return models.Fulfillment{}, classify(err, codeQueryFailed, "gönderi kilitlenemedi")
	}
	return toFulfillment(row)
}

// ListFulfillments gönderileri süzerek ve sayfalayarak döner.
// İkinci dönüş değeri süzgece uyan TÜM satırların sayısıdır.
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
		return nil, 0, classify(err, codeQueryFailed, "gönderiler listelenemedi")
	}

	total, err := r.queries(ctx).CountFulfillments(ctx, fulfillmentdb.CountFulfillmentsParams{
		Reference: filter.Reference,
		Status:    filter.Status,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "gönderiler sayılamadı")
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

// UpdateFulfillmentProviderResult sağlayıcının yanıtını gönderi satırına yazar.
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
		return models.Fulfillment{}, classify(err, codeQueryFailed, "gönderi güncellenemedi")
	}
	return toFulfillment(row)
}

// UpdateFulfillmentStatus gönderinin durumunu, takip bilgisini ve zaman
// damgalarını MUTLAK değerlerle yazar.
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
		return models.Fulfillment{}, classify(err, codeQueryFailed, "gönderi durumu güncellenemedi")
	}
	return toFulfillment(row)
}

// --- gönderi kalemleri -------------------------------------------------------

// CreateFulfillmentItem gönderiye bir kalem ekler.
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
		return models.FulfillmentItem{}, classify(err, codeQueryFailed, "gönderi kalemi eklenemedi")
	}
	return toItem(row), nil
}

// ListFulfillmentItems bir gönderinin kalemlerini döner.
func (r *Repository) ListFulfillmentItems(
	ctx context.Context,
	fulfillmentID string,
) ([]models.FulfillmentItem, error) {
	rows, err := r.queries(ctx).ListFulfillmentItems(ctx, fulfillmentID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "gönderi kalemleri listelenemedi")
	}
	out := make([]models.FulfillmentItem, 0, len(rows))
	for i := range rows {
		out = append(out, toItem(rows[i]))
	}
	return out, nil
}

// FulfillmentItemsByFulfillments kalemleri BİRDEN ÇOK gönderi için TEK sorguda
// döner (N+1 yok).
func (r *Repository) FulfillmentItemsByFulfillments(
	ctx context.Context,
	fulfillmentIDs []string,
) ([]models.FulfillmentItem, error) {
	if len(fulfillmentIDs) == 0 {
		return []models.FulfillmentItem{}, nil
	}
	rows, err := r.queries(ctx).ListFulfillmentItemsByFulfillments(ctx, fulfillmentIDs)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "gönderi kalemleri listelenemedi")
	}
	out := make([]models.FulfillmentItem, 0, len(rows))
	for i := range rows {
		out = append(out, toItem(rows[i]))
	}
	return out, nil
}

// --- manuel sağlayıcının defteri ---------------------------------------------

// InsertManualShipmentIfAbsent sağlayıcı gönderisini yalnızca idempotency
// anahtarı henüz kullanılmamışsa yazar.
//
// İkinci dönüş değeri satırın yazılıp yazılmadığıdır; çakışma HATA DEĞİLDİR.
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
			"manuel sağlayıcı gönderisi oluşturulamadı")
	}
	return toManualShipment(row), true, nil
}

// ManualShipment sağlayıcı gönderisini kimliğiyle döner; yoksa NotFound.
func (r *Repository) ManualShipment(ctx context.Context, id string) (models.ManualShipment, error) {
	row, err := r.queries(ctx).GetManualShipment(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ManualShipment{}, manualShipmentNotFound(id)
		}
		return models.ManualShipment{}, classify(err, codeQueryFailed,
			"manuel sağlayıcı gönderisi okunamadı")
	}
	return toManualShipment(row), nil
}

// ManualShipmentByIdempotencyKey sağlayıcı gönderisini anahtarıyla döner;
// yoksa NotFound.
func (r *Repository) ManualShipmentByIdempotencyKey(
	ctx context.Context,
	key string,
) (models.ManualShipment, error) {
	row, err := r.queries(ctx).GetManualShipmentByIdempotencyKey(ctx, key)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ManualShipment{}, errors.NotFound(codeManualShipmentNotFound,
				"bu idempotency anahtarıyla açılmış bir sağlayıcı gönderisi yok: %s", key)
		}
		return models.ManualShipment{}, classify(err, codeQueryFailed,
			"manuel sağlayıcı gönderisi okunamadı")
	}
	return toManualShipment(row), nil
}

// LockManualShipment sağlayıcı gönderisini işlem boyunca kilitler ve güncel
// hâlini döner.
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
			"manuel sağlayıcı gönderisi kilitlenemedi")
	}
	return toManualShipment(row), nil
}

// UpdateManualShipmentState sağlayıcı defterindeki durumu ve takip bilgisini
// MUTLAK değerlerle yazar.
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
			"manuel sağlayıcı gönderisi güncellenemedi")
	}
	return toManualShipment(row), nil
}
