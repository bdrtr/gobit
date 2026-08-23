package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/modules/region/models"
	"github.com/bdrtr/gobit/internal/modules/region/repository/regiondb"
)

// CreateRegion yeni bir bölge yazar ve yazılan satırı döner.
//
// Tanımsız bir para birimi verilirse foreign key ihlali errors.Invalid'e
// çevrilir (bkz. wrapDB): var olup olmadığını önce SELECT ile denetlemek
// yarışa açık olurdu — denetimle yazma arasında para birimi silinebilirdi.
func (r *Repo) CreateRegion(ctx context.Context, region models.Region, now time.Time) (models.Region, error) {
	if err := r.ready(); err != nil {
		return models.Region{}, err
	}

	row, err := r.q.InsertRegion(ctx, regiondb.InsertRegionParams{
		ID:             region.ID,
		Name:           region.Name,
		CurrencyCode:   region.CurrencyCode,
		AutomaticTaxes: region.AutomaticTaxes,
		TaxRate:        region.TaxRate,
		CreatedAt:      fromTime(now),
	})
	if err != nil {
		return models.Region{}, wrapDB(err, "bölge oluşturulamadı")
	}
	return toRegion(row), nil
}

// GetRegion kimliğe göre bölge döner; yoksa errors.NotFound.
func (r *Repo) GetRegion(ctx context.Context, id string) (models.Region, error) {
	if err := r.ready(); err != nil {
		return models.Region{}, err
	}

	row, err := r.q.GetRegion(ctx, id)
	if err != nil {
		return models.Region{}, notFoundOr(err, CodeRegionNotFound, "bölge bulunamadı: %s", id)
	}
	return toRegion(row), nil
}

// ListRegions sayfalanmış bölge listesini ve TOPLAM kayıt sayısını döner.
//
// Toplam sayı sayfa uzunluğundan bağımsızdır; API zarfındaki "count" alanı
// istemcinin kaç sayfa olduğunu bilmesini sağlar.
func (r *Repo) ListRegions(ctx context.Context, limit, offset int32) ([]models.Region, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	rows, err := r.q.ListRegions(ctx, regiondb.ListRegionsParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, 0, wrapDB(err, "bölge listesi alınamadı")
	}

	total, err := r.q.CountRegions(ctx)
	if err != nil {
		return nil, 0, wrapDB(err, "bölge sayısı alınamadı")
	}

	regions := make([]models.Region, 0, len(rows))
	for i := range rows {
		regions = append(regions, toRegion(rows[i]))
	}
	return regions, total, nil
}

// GetRegionsByIDs verilen kimliklere karşılık gelen bölgeleri TEK sorguda
// döner. Bulunamayan kimlik için kayıt dönmez; bu bir hata değildir (Query
// katmanının FetchByIDs sözleşmesi, ADR 0004).
func (r *Repo) GetRegionsByIDs(ctx context.Context, ids []string) ([]models.Region, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []models.Region{}, nil
	}

	rows, err := r.q.GetRegionsByIDs(ctx, ids)
	if err != nil {
		return nil, wrapDB(err, "bölgeler alınamadı")
	}

	regions := make([]models.Region, 0, len(rows))
	for i := range rows {
		regions = append(regions, toRegion(rows[i]))
	}
	return regions, nil
}

// UpdateRegion yamayı KİLİT ALTINDA okunan satırın üstüne uygular.
//
// Oku-değiştir-yaz döngüsü tek işlemde ve satır kilidiyle yürür: kilitsiz iki
// eşzamanlı kısmi güncellemeden ikincisi, birincinin yazdığı alanı eski
// değeriyle geri yazardı (lost update). Yamanın kendisi saf bir dönüşümdür
// ([models.Region.Patched]) ve veritabanı olmadan sınanabilir.
func (r *Repo) UpdateRegion(
	ctx context.Context,
	id string,
	patch models.RegionPatch,
	now time.Time,
) (models.Region, error) {
	var updated models.Region

	err := r.inTx(ctx, func(q *regiondb.Queries) error {
		current, err := q.GetRegionForUpdate(ctx, id)
		if err != nil {
			return notFoundOr(err, CodeRegionNotFound, "bölge bulunamadı: %s", id)
		}

		next := toRegion(current).Patched(patch)
		row, err := q.UpdateRegion(ctx, regiondb.UpdateRegionParams{
			ID:             id,
			Name:           next.Name,
			CurrencyCode:   next.CurrencyCode,
			AutomaticTaxes: next.AutomaticTaxes,
			TaxRate:        next.TaxRate,
			UpdatedAt:      fromTime(now),
		})
		if err != nil {
			return wrapDB(err, "bölge güncellenemedi: %s", id)
		}
		updated = toRegion(row)
		return nil
	})
	if err != nil {
		return models.Region{}, err
	}
	return updated, nil
}

// DeleteRegion bölgeyi soft delete ile siler ve ülkelerini SERBEST BIRAKIR.
//
// İki adım tek işlemdedir ve sıra kilit sırasının aynısıdır: önce bölge, sonra
// ülkeler. Ülkeler serbest bırakılmasaydı ölü bir bölgeye bağlı kalır, başka
// hiçbir bölgeye eklenemez ve ResolveRegionForCountry onlar için kalıcı olarak
// "bulunamadı" dönerdi.
func (r *Repo) DeleteRegion(ctx context.Context, id string, now time.Time) error {
	return r.inTx(ctx, func(q *regiondb.Queries) error {
		if _, err := q.SoftDeleteRegion(ctx, regiondb.SoftDeleteRegionParams{
			ID:        id,
			DeletedAt: fromTime(now),
		}); err != nil {
			return notFoundOr(err, CodeRegionNotFound, "bölge bulunamadı: %s", id)
		}

		if err := q.ClearRegionCountries(ctx, regiondb.ClearRegionCountriesParams{
			RegionID:  id,
			UpdatedAt: fromTime(now),
		}); err != nil {
			return wrapDB(err, "bölgenin ülkeleri serbest bırakılamadı: %s", id)
		}
		return nil
	})
}

// GetRegionByCountry ülke koduna karşılık gelen bölgeyi TEK sorguda döner.
//
// Ülke tanımsızsa, hiçbir bölgeye bağlı değilse ya da bağlı olduğu bölge
// silinmişse errors.NotFound döner; üç durumu birbirinden ayırmak servisin
// işidir ve YALNIZCA hata yolunda yapılır (bkz. service.ResolveRegionForCountry).
func (r *Repo) GetRegionByCountry(ctx context.Context, countryCode string) (models.Region, error) {
	if err := r.ready(); err != nil {
		return models.Region{}, err
	}

	row, err := r.q.GetRegionByCountry(ctx, countryCode)
	if err != nil {
		return models.Region{}, notFoundOr(err, CodeRegionNotFound,
			"%s ülkesi için bölge bulunamadı", countryCode)
	}
	return toRegion(row), nil
}
