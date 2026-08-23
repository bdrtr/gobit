package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/modules/pricing/models"
	"github.com/bdrtr/gobit/internal/modules/pricing/repository/pricingdb"
)

// CreatePriceList yeni bir fiyat listesi oluşturur.
func (r *Repo) CreatePriceList(ctx context.Context, list models.PriceList, now time.Time) (models.PriceList, error) {
	if err := r.ready(); err != nil {
		return models.PriceList{}, err
	}

	row, err := r.q.InsertPriceList(ctx, pricingdb.InsertPriceListParams{
		ID:          list.ID,
		Title:       list.Title,
		Description: list.Description,
		Type:        string(list.Type),
		Status:      string(list.Status),
		StartsAt:    fromTimePtr(list.StartsAt),
		EndsAt:      fromTimePtr(list.EndsAt),
		CreatedAt:   fromTime(now),
	})
	if err != nil {
		return models.PriceList{}, wrapDB(err, "fiyat listesi oluşturulamadı")
	}
	return toPriceList(row), nil
}

// GetPriceList kimliğe göre listeyi döner; yoksa errors.NotFound.
func (r *Repo) GetPriceList(ctx context.Context, id string) (models.PriceList, error) {
	if err := r.ready(); err != nil {
		return models.PriceList{}, err
	}

	row, err := r.q.GetPriceList(ctx, id)
	if err != nil {
		return models.PriceList{}, notFoundOr(err, CodePriceListNotFound, "fiyat listesi bulunamadı: %s", id)
	}
	return toPriceList(row), nil
}

// ListPriceLists sayfalanmış liste kümesini ve TOPLAM kayıt sayısını döner.
func (r *Repo) ListPriceLists(ctx context.Context, limit, offset int32) ([]models.PriceList, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	rows, err := r.q.ListPriceLists(ctx, pricingdb.ListPriceListsParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, 0, wrapDB(err, "fiyat listeleri alınamadı")
	}

	total, err := r.q.CountPriceLists(ctx)
	if err != nil {
		return nil, 0, wrapDB(err, "fiyat listesi sayısı alınamadı")
	}

	lists := make([]models.PriceList, 0, len(rows))
	for i := range rows {
		lists = append(lists, toPriceList(rows[i]))
	}
	return lists, total, nil
}

// UpdatePriceList listenin tüm güncellenebilir alanlarını yazar;
// yoksa errors.NotFound.
func (r *Repo) UpdatePriceList(ctx context.Context, list models.PriceList, now time.Time) (models.PriceList, error) {
	if err := r.ready(); err != nil {
		return models.PriceList{}, err
	}

	row, err := r.q.UpdatePriceList(ctx, pricingdb.UpdatePriceListParams{
		ID:          list.ID,
		Title:       list.Title,
		Description: list.Description,
		Type:        string(list.Type),
		Status:      string(list.Status),
		StartsAt:    fromTimePtr(list.StartsAt),
		EndsAt:      fromTimePtr(list.EndsAt),
		UpdatedAt:   fromTime(now),
	})
	if err != nil {
		return models.PriceList{}, notFoundOr(err, CodePriceListNotFound,
			"fiyat listesi bulunamadı: %s", list.ID)
	}
	return toPriceList(row), nil
}

// DeletePriceList listeyi soft delete ile siler; yoksa errors.NotFound.
//
// Listeye bağlı fiyatlar SİLİNMEZ ama hesaplamada elenir: aday sorgusundaki
// LEFT JOIN silinmiş listeyi görmez ve servis, listesi kaybolmuş fiyatı hesaba
// katmaz. Fiyatların kalması bilinçlidir — liste yanlışlıkla silinirse geri
// yüklemek tek satırlık bir işlemdir.
func (r *Repo) DeletePriceList(ctx context.Context, id string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	if _, err := r.q.SoftDeletePriceList(ctx, pricingdb.SoftDeletePriceListParams{
		ID:        id,
		DeletedAt: fromTime(now),
	}); err != nil {
		return notFoundOr(err, CodePriceListNotFound, "fiyat listesi bulunamadı: %s", id)
	}
	return nil
}

// toPriceList üretilen satırı domain modeline çevirir.
func toPriceList(row pricingdb.PriceList) models.PriceList {
	return models.PriceList{
		ID:          row.ID,
		Title:       row.Title,
		Description: row.Description,
		Type:        models.PriceListType(row.Type),
		Status:      models.PriceListStatus(row.Status),
		StartsAt:    toTimePtr(row.StartsAt),
		EndsAt:      toTimePtr(row.EndsAt),
		CreatedAt:   toTime(row.CreatedAt),
		UpdatedAt:   toTime(row.UpdatedAt),
	}
}
