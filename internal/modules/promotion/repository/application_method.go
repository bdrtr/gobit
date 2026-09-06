package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository/promotiondb"
)

// SetApplicationMethod promosyonun uygulama yöntemini yazar; varsa ÜZERİNE
// YAZAR.
//
// Promosyon yoksa ya da silinmişse errors.NotFound döner.
//
// Yerine koyma tek ifadedir (upsert): "önce sil sonra ekle" iki ifade arasında
// yöntemsiz bir promosyon bırakır ve o aralıkta koşan bir hesap indirim
// üretmezdi.
//
// Promosyon PAYLAŞIMLI kilit altında okunur ve yöntem AYNI işlemde yazılır
// (bkz. [requireLivePromotion]). Upsert'ün TEK ifade olması yetmez: tek ifade
// olan şey yazmanın kendisidir, promosyonun canlı olduğu bilgisi değil. Foreign
// key de yetmez — yumuşak silme satırı yerinde bıraktığı için FK, silinmiş bir
// promosyonun altına yazılan yöntemi GEÇİRİR.
//
// Koşulu upsert'ün İÇİNE koymak (INSERT ... SELECT) reddedildi: çakışma dalı
// (ON CONFLICT DO UPDATE) promosyon tablosunu göremez ve o dalda koşul yeniden
// yazılamazdı; iki dalın farklı garantileri olurdu.
func (r *Repo) SetApplicationMethod(
	ctx context.Context,
	m models.ApplicationMethod,
	now time.Time,
) (models.ApplicationMethod, error) {
	var out models.ApplicationMethod

	err := r.inTx(ctx, func(q *promotiondb.Queries) error {
		if txErr := requireLivePromotion(ctx, q, m.PromotionID); txErr != nil {
			return txErr
		}

		row, txErr := q.UpsertApplicationMethod(ctx, promotiondb.UpsertApplicationMethodParams{
			ID:           m.ID,
			PromotionID:  m.PromotionID,
			Type:         string(m.Type),
			TargetType:   string(m.TargetType),
			Allocation:   string(m.Allocation),
			Value:        m.Value,
			MaxQuantity:  copyInt64(m.MaxQuantity),
			CurrencyCode: nilIfEmpty(m.CurrencyCode),
			CreatedAt:    fromTime(now),
		})
		if txErr != nil {
			return wrapDB(txErr, "uygulama yöntemi yazılamadı: %s", m.PromotionID)
		}
		out = toApplicationMethod(row)
		return nil
	})
	if err != nil {
		return models.ApplicationMethod{}, err
	}
	return out, nil
}

// GetApplicationMethod promosyonun uygulama yöntemini döner; yoksa
// errors.NotFound.
func (r *Repo) GetApplicationMethod(ctx context.Context, promotionID string) (models.ApplicationMethod, error) {
	if err := r.ready(); err != nil {
		return models.ApplicationMethod{}, err
	}

	row, err := r.q.GetApplicationMethod(ctx, promotionID)
	if err != nil {
		return models.ApplicationMethod{}, notFoundOr(err, CodeApplicationMethodNotFound,
			"promosyonun uygulama yöntemi yok: %s", promotionID)
	}
	return toApplicationMethod(row), nil
}

// DeleteApplicationMethod yöntemi soft delete ile siler; yoksa errors.NotFound.
//
// Yöntemsiz kalan promosyon HATA DEĞİLDİR: indirim üretmez ve hesapta atlanır.
// Bu, bir promosyonu silmeden geçici olarak etkisizleştirmenin yoludur.
func (r *Repo) DeleteApplicationMethod(ctx context.Context, promotionID string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	if _, err := r.q.SoftDeleteApplicationMethod(ctx, promotiondb.SoftDeleteApplicationMethodParams{
		PromotionID: promotionID,
		DeletedAt:   fromTime(now),
	}); err != nil {
		return notFoundOr(err, CodeApplicationMethodNotFound,
			"promosyonun uygulama yöntemi yok: %s", promotionID)
	}
	return nil
}

// toApplicationMethod üretilen satırı domain modeline çevirir.
func toApplicationMethod(row promotiondb.PromotionApplicationMethod) models.ApplicationMethod {
	return models.ApplicationMethod{
		ID:           row.ID,
		PromotionID:  row.PromotionID,
		Type:         models.ApplicationMethodType(row.Type),
		TargetType:   models.ApplicationTargetType(row.TargetType),
		Allocation:   models.Allocation(row.Allocation),
		Value:        row.Value,
		MaxQuantity:  copyInt64(row.MaxQuantity),
		CurrencyCode: deref(row.CurrencyCode),
		CreatedAt:    toTime(row.CreatedAt),
		UpdatedAt:    toTime(row.UpdatedAt),
	}
}
