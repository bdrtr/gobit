package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/modules/pricing/models"
	"github.com/bdrtr/gobit/internal/modules/pricing/repository/pricingdb"
)

// CreatePriceSet verilen kimlikle boş bir price set oluşturur.
func (r *Repo) CreatePriceSet(ctx context.Context, id string, now time.Time) (models.PriceSet, error) {
	if err := r.ready(); err != nil {
		return models.PriceSet{}, err
	}

	row, err := r.q.InsertPriceSet(ctx, pricingdb.InsertPriceSetParams{
		ID:        id,
		CreatedAt: fromTime(now),
	})
	if err != nil {
		return models.PriceSet{}, wrapDB(err, "price set oluşturulamadı")
	}
	return toPriceSet(row), nil
}

// GetPriceSet kimliğe göre price set döner; yoksa errors.NotFound.
func (r *Repo) GetPriceSet(ctx context.Context, id string) (models.PriceSet, error) {
	if err := r.ready(); err != nil {
		return models.PriceSet{}, err
	}

	row, err := r.q.GetPriceSet(ctx, id)
	if err != nil {
		return models.PriceSet{}, notFoundOr(err, CodePriceSetNotFound, "price set bulunamadı: %s", id)
	}
	return toPriceSet(row), nil
}

// ListPriceSets sayfalanmış price set listesini ve TOPLAM kayıt sayısını döner.
//
// Toplam sayı sayfa uzunluğundan bağımsızdır; API zarfındaki "count" alanı
// istemcinin kaç sayfa olduğunu bilmesini sağlar.
func (r *Repo) ListPriceSets(ctx context.Context, limit, offset int32) ([]models.PriceSet, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	rows, err := r.q.ListPriceSets(ctx, pricingdb.ListPriceSetsParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, 0, wrapDB(err, "price set listesi alınamadı")
	}

	total, err := r.q.CountPriceSets(ctx)
	if err != nil {
		return nil, 0, wrapDB(err, "price set sayısı alınamadı")
	}

	sets := make([]models.PriceSet, 0, len(rows))
	for _, row := range rows {
		sets = append(sets, toPriceSet(row))
	}
	return sets, total, nil
}

// GetPriceSetsByIDs verilen kimliklere karşılık gelen price set'leri TEK
// sorguda döner. Bulunamayan kimlik için kayıt dönmez; bu bir hata değildir
// (Query katmanının FetchByIDs sözleşmesi, ADR 0004).
func (r *Repo) GetPriceSetsByIDs(ctx context.Context, ids []string) ([]models.PriceSet, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []models.PriceSet{}, nil
	}

	rows, err := r.q.GetPriceSetsByIDs(ctx, ids)
	if err != nil {
		return nil, wrapDB(err, "price set'ler alınamadı")
	}

	sets := make([]models.PriceSet, 0, len(rows))
	for _, row := range rows {
		sets = append(sets, toPriceSet(row))
	}
	return sets, nil
}

// DeletePriceSet price set'i soft delete ile siler; yoksa errors.NotFound.
//
// Fiyatlar da aynı işlemde silinir: kabı silip fiyatlarını canlı bırakmak,
// silinmiş bir kabın fiyatlarının listelerde görünmesi demekti.
func (r *Repo) DeletePriceSet(ctx context.Context, id string, now time.Time) error {
	return r.inTx(ctx, func(q *pricingdb.Queries) error {
		if _, err := q.SoftDeletePriceSet(ctx, pricingdb.SoftDeletePriceSetParams{
			ID:        id,
			DeletedAt: fromTime(now),
		}); err != nil {
			return notFoundOr(err, CodePriceSetNotFound, "price set bulunamadı: %s", id)
		}

		if err := q.SoftDeletePricesBySet(ctx, pricingdb.SoftDeletePricesBySetParams{
			PriceSetID: id,
			DeletedAt:  fromTime(now),
		}); err != nil {
			return wrapDB(err, "price set fiyatları silinemedi: %s", id)
		}
		return nil
	})
}

// toPriceSet üretilen satırı domain modeline çevirir.
func toPriceSet(row pricingdb.PriceSet) models.PriceSet {
	return models.PriceSet{
		ID:        row.ID,
		CreatedAt: toTime(row.CreatedAt),
		UpdatedAt: toTime(row.UpdatedAt),
		DeletedAt: toTimePtr(row.DeletedAt),
	}
}
