package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/region/models"
	"github.com/bdrtr/gobit/internal/modules/region/repository/regiondb"
)

// AssignCountry ülkeyi bölgeye bağlar ve güncel ülkeyi döner.
//
// "Bir ülke en fazla bir bölgeye ait olabilir" kuralı BURADA korunur:
//
//   - Bölge satırı önce PAYLAŞIMLI kilitlenir (kilit sırasının ilk adımı);
//     böylece silinmekte olan bir bölgeye ülke eklenemez.
//   - Ülke satırı sonra TEKİL kilitlenir. Aynı ülkeyi iki farklı bölgeye
//     eklemeye çalışan iki istekten ikincisi burada bekler; beklemesi bitince
//     satırın GÜNCEL sürümünü okur, ülkenin alındığını görür ve
//     errors.Conflict döner. Kilit olmasaydı ikisi de boş bir region_id görür
//     ve ikincisi birincinin yazdığını sessizce ezerdi.
//
// Çağrı IDEMPOTENTTİR: ülke zaten aynı bölgedeyse yazma yapılmadan mevcut kayıt
// döner. Tekrar edilen bir yönetim isteğinin çakışma hatası vermesi anlamsız
// olurdu — istenen durum zaten sağlanmıştır.
func (r *Repo) AssignCountry(
	ctx context.Context,
	regionID, countryCode string,
	now time.Time,
) (models.Country, error) {
	var assigned models.Country

	err := r.inTx(ctx, func(q *regiondb.Queries) error {
		if _, err := q.GetRegionForShare(ctx, regionID); err != nil {
			return notFoundOr(err, CodeRegionNotFound, "bölge bulunamadı: %s", regionID)
		}

		current, err := q.GetCountryForUpdate(ctx, countryCode)
		if err != nil {
			return notFoundOr(err, CodeCountryNotFound, "ülke bulunamadı: %s", countryCode)
		}

		if current.RegionID != nil {
			if *current.RegionID == regionID {
				assigned = toCountry(current)
				return nil
			}
			return errors.Conflict(CodeCountryTaken,
				"%s ülkesi zaten %s bölgesine ait; bir ülke en fazla bir bölgeye ait olabilir",
				countryCode, *current.RegionID)
		}

		row, err := q.SetCountryRegion(ctx, regiondb.SetCountryRegionParams{
			Iso2:      countryCode,
			RegionID:  regionID,
			UpdatedAt: fromTime(now),
		})
		if err != nil {
			return wrapDB(err, "ülke bölgeye eklenemedi: %s", countryCode)
		}
		assigned = toCountry(row)
		return nil
	})
	if err != nil {
		return models.Country{}, err
	}
	return assigned, nil
}

// UnassignCountry ülkeyi verilen bölgeden ayırır.
//
// Ülke o bölgeye ait değilse errors.NotFound döner: silme isteğinin hedefi
// "bölgedeki ülke" kaydıdır ve o kayıt yoktur. Sessizce başarılı dönmek,
// yanlış bölge kimliğiyle yapılan bir çağrının başarılı sanılması demekti.
func (r *Repo) UnassignCountry(ctx context.Context, regionID, countryCode string, now time.Time) error {
	return r.inTx(ctx, func(q *regiondb.Queries) error {
		current, err := q.GetCountryForUpdate(ctx, countryCode)
		if err != nil {
			return notFoundOr(err, CodeCountryNotFound, "ülke bulunamadı: %s", countryCode)
		}
		if current.RegionID == nil || *current.RegionID != regionID {
			return errors.NotFound(CodeCountryNotInRegion,
				"%s ülkesi %s bölgesine ait değil", countryCode, regionID)
		}

		if _, err := q.ClearCountryRegion(ctx, regiondb.ClearCountryRegionParams{
			Iso2:      countryCode,
			RegionID:  regionID,
			UpdatedAt: fromTime(now),
		}); err != nil {
			return notFoundOr(err, CodeCountryNotInRegion,
				"%s ülkesi %s bölgesinden çıkarılamadı", countryCode, regionID)
		}
		return nil
	})
}

// GetCountry koda göre ülke döner; yoksa errors.NotFound.
func (r *Repo) GetCountry(ctx context.Context, code string) (models.Country, error) {
	if err := r.ready(); err != nil {
		return models.Country{}, err
	}

	row, err := r.q.GetCountry(ctx, code)
	if err != nil {
		return models.Country{}, notFoundOr(err, CodeCountryNotFound, "ülke bulunamadı: %s", code)
	}
	return toCountry(row), nil
}

// ListCountries sayfalanmış ülke listesini ve TOPLAM kayıt sayısını döner.
//
// regionID nil ise süzgeç uygulanmaz; dolu ise yalnızca o bölgenin ülkeleri
// döner.
func (r *Repo) ListCountries(
	ctx context.Context,
	regionID *string,
	limit, offset int32,
) ([]models.Country, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	rows, err := r.q.ListCountries(ctx, regiondb.ListCountriesParams{
		RegionID: regionID,
		Lim:      limit,
		Off:      offset,
	})
	if err != nil {
		return nil, 0, wrapDB(err, "ülke listesi alınamadı")
	}

	total, err := r.q.CountCountries(ctx, regionID)
	if err != nil {
		return nil, 0, wrapDB(err, "ülke sayısı alınamadı")
	}

	countries := make([]models.Country, 0, len(rows))
	for i := range rows {
		countries = append(countries, toCountry(rows[i]))
	}
	return countries, total, nil
}

// ListCountriesByRegions birden çok bölgenin ülkelerini TEK sorguda, bölge
// kimliğine göre gruplanmış olarak döner.
//
// Query sağlayıcısı bölgeleri ülkeleriyle döndürür; bölge başına ayrı sorgu
// N+1 demek olurdu (ADR 0004).
func (r *Repo) ListCountriesByRegions(
	ctx context.Context,
	regionIDs []string,
) (map[string][]models.Country, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(regionIDs) == 0 {
		return map[string][]models.Country{}, nil
	}

	rows, err := r.q.ListCountriesByRegions(ctx, regionIDs)
	if err != nil {
		return nil, wrapDB(err, "bölgelerin ülkeleri alınamadı")
	}

	byRegion := make(map[string][]models.Country, len(regionIDs))
	for i := range rows {
		regionID := rows[i].RegionID
		if regionID == nil {
			// Sorgu region_id = ANY(...) süzdüğü için NULL satır dönemez;
			// yine de dönse kimliksiz bir gruba yazmak sessiz bir hata olurdu.
			continue
		}
		byRegion[*regionID] = append(byRegion[*regionID], toCountry(rows[i]))
	}
	return byRegion, nil
}
