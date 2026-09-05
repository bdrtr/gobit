package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
	"github.com/bdrtr/gobit/internal/modules/tax/repository/taxdb"
)

// CreateTaxRegion yeni bir vergi bölgesi yazar.
//
// Aynı ülkeye ikinci bir KÖK bölge yazılırsa kısmi benzersiz indeks ihlali
// oluşur ve errors.Conflict dönülür. Eyalet bölgesinin ülkesi ebeveyninin
// ülkesinden farklıysa bileşik foreign key ihlali oluşur ve errors.Invalid
// dönülür; ikisi de servis denetimlerinin ARDINDAKİ son savunmadır.
func (r *Repo) CreateTaxRegion(ctx context.Context, region models.TaxRegion, now time.Time) (models.TaxRegion, error) {
	if err := r.ready(); err != nil {
		return models.TaxRegion{}, err
	}

	metadata, err := fromJSONMap(region.Metadata)
	if err != nil {
		return models.TaxRegion{}, err
	}

	row, err := r.queries(ctx).InsertTaxRegion(ctx, taxdb.InsertTaxRegionParams{
		ID:           region.ID,
		CountryCode:  region.CountryCode,
		ProvinceCode: region.ProvinceCode,
		ParentID:     region.ParentID,
		ProviderID:   region.ProviderID,
		Metadata:     metadata,
		CreatedAt:    fromTime(now),
	})
	if err != nil {
		return models.TaxRegion{}, wrapDB(err, "vergi bölgesi eklenemedi: %s/%s",
			region.CountryCode, region.Province())
	}
	return toTaxRegion(row)
}

// GetTaxRegion kimliğe göre bölge döner; yoksa errors.NotFound.
func (r *Repo) GetTaxRegion(ctx context.Context, id string) (models.TaxRegion, error) {
	if err := r.ready(); err != nil {
		return models.TaxRegion{}, err
	}

	row, err := r.queries(ctx).GetTaxRegion(ctx, id)
	if err != nil {
		return models.TaxRegion{}, notFoundOr(err, CodeTaxRegionNotFound,
			"vergi bölgesi bulunamadı: %s", id)
	}
	return toTaxRegion(row)
}

// LockTaxRegion bölgeyi PAYLAŞIMLI kilitle okur; kilit işlem sonuna kadar
// tutulur.
//
// YALNIZCA [Repo.WithTx] içinde çağrılabilir ve dışarıda çağrılırsa hata
// döner: FOR SHARE kilidi işlem bitince serbest kalır, yani işlemsiz bir kilit
// hiçbir şeyi korumaz ama koruduğu sanılır.
//
// Bölgeye bir şey BAĞLAYAN her akış bunu kullanır — eyalet bölgesi ekleme ve
// oran ekleme. İkisi de "bölge canlı mı" denetimini yapıp ardından yazar ve
// denetim ile yazma AYNI işlemde olmalıdır. Kilitsiz [Repo.GetTaxRegion] ile
// yapılan denetimin bedeli ölçülmüştür: araya giren bir
// [Repo.DeleteTaxRegion] denetimden sonra tamamlanır, yazma yine de başarılı
// olur ve silinmiş bir bölgeye bağlı CANLI bir satır kalır. Foreign key bunu
// yakalayamaz çünkü silme YUMUŞAKTIR: satır yerinde durur.
//
// Kilit PAYLAŞIMLIDIR: aynı bölgeye eşzamanlı iki oran eklemenin birbirini
// beklemesi için sebep yoktur, beklemesi gereken tek akış silmedir ve o TEKİL
// kilit alır.
func (r *Repo) LockTaxRegion(ctx context.Context, id string) (models.TaxRegion, error) {
	if err := r.ready(); err != nil {
		return models.TaxRegion{}, err
	}
	if err := requireTx(ctx, "LockTaxRegion"); err != nil {
		return models.TaxRegion{}, err
	}

	row, err := r.queries(ctx).GetTaxRegionForShare(ctx, id)
	if err != nil {
		return models.TaxRegion{}, notFoundOr(err, CodeTaxRegionNotFound,
			"vergi bölgesi bulunamadı: %s", id)
	}
	return toTaxRegion(row)
}

// GetTaxRegionsByIDs verilen kimliklere karşılık gelen bölgeleri TEK turda
// döner; bulunamayan kimlik için kayıt dönmez.
func (r *Repo) GetTaxRegionsByIDs(ctx context.Context, ids []string) ([]models.TaxRegion, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []models.TaxRegion{}, nil
	}

	rows, err := r.queries(ctx).GetTaxRegionsByIDs(ctx, ids)
	if err != nil {
		return nil, wrapDB(err, "vergi bölgeleri alınamadı")
	}
	return toTaxRegions(rows)
}

// ListTaxRegions sayfalanmış bölge listesini ve TOPLAM sayıyı döner.
//
// countryCode boşsa süzgeç uygulanmaz. Toplam sayı ayrı bir sorguyla alınır:
// sayfa boyu kadar satırla toplam kayıt sayısı bilinemez ve API zarfı
// (plan Bölüm 8) "count" alanını taşımak zorundadır.
func (r *Repo) ListTaxRegions(
	ctx context.Context,
	countryCode string,
	limit, offset int32,
) ([]models.TaxRegion, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	rows, err := r.queries(ctx).ListTaxRegions(ctx, taxdb.ListTaxRegionsParams{
		Limit:       limit,
		Offset:      offset,
		CountryCode: countryCode,
	})
	if err != nil {
		return nil, 0, wrapDB(err, "vergi bölgeleri listelenemedi")
	}

	total, err := r.queries(ctx).CountTaxRegions(ctx, countryCode)
	if err != nil {
		return nil, 0, wrapDB(err, "vergi bölgeleri sayılamadı")
	}

	regions, err := toTaxRegions(rows)
	if err != nil {
		return nil, 0, err
	}
	return regions, total, nil
}

// ResolveTaxRegions ülkenin kökünü ve (verilmişse) eyalet bölgesini döner.
//
// Sıra EYALET ÖNCE, ülke sonradır; hesap zinciri en ÖZELDEN genele yürür
// (bkz. service.CalculateTax). Hiç bölge yoksa BOŞ dilim döner ve bu bir hata
// DEĞİLDİR: vergisi yapılandırılmamış bir ülke, hata değil sıfır vergi
// üretmelidir (gerekçe service/calculate.go godoc'unda).
func (r *Repo) ResolveTaxRegions(ctx context.Context, countryCode, provinceCode string) ([]models.TaxRegion, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	rows, err := r.queries(ctx).ResolveTaxRegions(ctx, taxdb.ResolveTaxRegionsParams{
		CountryCode:  countryCode,
		ProvinceCode: provinceCode,
	})
	if err != nil {
		return nil, wrapDB(err, "vergi bölgesi çözülemedi: %s/%s", countryCode, provinceCode)
	}
	return toTaxRegions(rows)
}

// DeleteTaxRegion bölgeyi, alt bölgelerini, onların oranlarını ve o oranların
// kurallarını TEK işlemde yumuşak siler.
//
// Ağaç silinir çünkü ülke kökü olmadan eyalet bölgesi bulunamaz hâle gelir:
// çözüm yolu daima ülkeden başlar. Yetim kalan bir eyalet kaydı hiçbir hesaba
// girmez ama aynı ülkeye açılan yeni bir kök onun yerini alamaz — eyalet
// benzersizliği eski (silinmemiş) satıra bağlı kalırdı.
func (r *Repo) DeleteTaxRegion(ctx context.Context, id string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	return r.WithTx(ctx, func(ctx context.Context) error {
		q := r.queries(ctx)

		// Kilit, aynı bölgeye eşzamanlı bir oran ya da eyalet ekleme akışıyla
		// yarışı engeller: o akışlar da bölgeyi [Repo.LockTaxRegion] ile
		// PAYLAŞIMLI kilitle okur ve FOR SHARE ile FOR UPDATE çakışır. Kilit
		// olmadan yumuşak silme onlara görünmezdi — foreign key satırın
		// VARLIĞINA bakar, deleted_at'ine değil, yani silinmiş bir bölgeye
		// yazılan oran hiçbir kısıta takılmaz.
		if _, err := q.GetTaxRegionForUpdate(ctx, id); err != nil {
			return notFoundOr(err, CodeTaxRegionNotFound, "vergi bölgesi bulunamadı: %s", id)
		}

		regionIDs, err := q.SoftDeleteTaxRegionTree(ctx, taxdb.SoftDeleteTaxRegionTreeParams{
			ID:        id,
			DeletedAt: fromTime(now),
		})
		if err != nil {
			return wrapDB(err, "vergi bölgesi silinemedi: %s", id)
		}
		if len(regionIDs) == 0 {
			// Kilitli okuma satırı gördüğüne göre buraya düşülemez; yine de
			// sessizce başarılı dönmek, silinmediği hâlde silindi sanılan bir
			// bölge demek olurdu.
			return errors.NotFound(CodeTaxRegionNotFound, "vergi bölgesi bulunamadı: %s", id)
		}

		rateIDs, err := q.SoftDeleteTaxRatesByRegions(ctx, taxdb.SoftDeleteTaxRatesByRegionsParams{
			RegionIds: regionIDs,
			DeletedAt: fromTime(now),
		})
		if err != nil {
			return wrapDB(err, "bölgenin vergi oranları silinemedi: %s", id)
		}
		if len(rateIDs) == 0 {
			return nil
		}

		if err := q.SoftDeleteTaxRateRulesByRates(ctx, taxdb.SoftDeleteTaxRateRulesByRatesParams{
			RateIds:   rateIDs,
			DeletedAt: fromTime(now),
		}); err != nil {
			return wrapDB(err, "bölgenin vergi kuralları silinemedi: %s", id)
		}
		return nil
	})
}

// toTaxRegion üretilen satırı domain modeline çevirir.
func toTaxRegion(row taxdb.TaxRegion) (models.TaxRegion, error) {
	metadata, err := toJSONMap(row.Metadata)
	if err != nil {
		return models.TaxRegion{}, err
	}
	return models.TaxRegion{
		ID:           row.ID,
		CountryCode:  row.CountryCode,
		ProvinceCode: row.ProvinceCode,
		ParentID:     row.ParentID,
		ProviderID:   row.ProviderID,
		Metadata:     metadata,
		CreatedAt:    toTime(row.CreatedAt),
		UpdatedAt:    toTime(row.UpdatedAt),
		DeletedAt:    toTimePtr(row.DeletedAt),
	}, nil
}

// toTaxRegions satır dilimini domain modellerine çevirir.
func toTaxRegions(rows []taxdb.TaxRegion) ([]models.TaxRegion, error) {
	out := make([]models.TaxRegion, 0, len(rows))
	for i := range rows {
		region, err := toTaxRegion(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, region)
	}
	return out, nil
}
