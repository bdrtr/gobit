package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
	"github.com/bdrtr/gobit/internal/modules/tax/repository/taxdb"
)

// CreateTaxRate bir bölgeye oran ekler.
//
// Bölge yoksa foreign key ihlali oluşur ve errors.Invalid dönülür; oranın
// yetim kalması yapısal olarak imkânsızdır. Bölgede zaten bir varsayılan oran
// varsa kısmi benzersiz indeks ihlali oluşur ve errors.Conflict dönülür.
func (r *Repo) CreateTaxRate(ctx context.Context, rate models.TaxRate, now time.Time) (models.TaxRate, error) {
	if err := r.ready(); err != nil {
		return models.TaxRate{}, err
	}

	metadata, err := fromJSONMap(rate.Metadata)
	if err != nil {
		return models.TaxRate{}, err
	}

	row, err := r.q.InsertTaxRate(ctx, taxdb.InsertTaxRateParams{
		ID:          rate.ID,
		TaxRegionID: rate.TaxRegionID,
		Name:        rate.Name,
		Code:        optionalText(rate.RateCode()),
		RateBps:     rate.RateBps,
		IsDefault:   rate.IsDefault,
		Metadata:    metadata,
		CreatedAt:   fromTime(now),
	})
	if err != nil {
		return models.TaxRate{}, wrapDB(err, "vergi oranı eklenemedi: %s", rate.TaxRegionID)
	}
	return toTaxRate(row)
}

// GetTaxRate kimliğe göre oranı döner; yoksa errors.NotFound.
func (r *Repo) GetTaxRate(ctx context.Context, id string) (models.TaxRate, error) {
	if err := r.ready(); err != nil {
		return models.TaxRate{}, err
	}

	row, err := r.q.GetTaxRate(ctx, id)
	if err != nil {
		return models.TaxRate{}, notFoundOr(err, CodeTaxRateNotFound, "vergi oranı bulunamadı: %s", id)
	}
	return toTaxRate(row)
}

// ListTaxRates bir bölgenin canlı oranlarını döner; varsayılan oran BAŞTADIR.
func (r *Repo) ListTaxRates(ctx context.Context, regionID string) ([]models.TaxRate, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	rows, err := r.q.ListTaxRatesByRegion(ctx, regionID)
	if err != nil {
		return nil, wrapDB(err, "vergi oranları alınamadı: %s", regionID)
	}
	return toTaxRates(rows)
}

// ListTaxRatesByRegions birden çok bölgenin oranlarını TEK sorguda döner.
//
// Hesap yolunun okuma biçimidir: bölge zinciri (eyalet + ülke) tek turda
// okunur, bölge başına ayrı sorgu yapılmaz.
func (r *Repo) ListTaxRatesByRegions(ctx context.Context, regionIDs []string) ([]models.TaxRate, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(regionIDs) == 0 {
		return []models.TaxRate{}, nil
	}

	rows, err := r.q.ListTaxRatesByRegions(ctx, regionIDs)
	if err != nil {
		return nil, wrapDB(err, "vergi oranları alınamadı")
	}
	return toTaxRates(rows)
}

// UpdateTaxRate oranın verilen alanlarını KİLİT ALTINDA günceller.
//
// Yama, kilitli okunan satırın üstüne uygulanır: kilitsiz iki eşzamanlı
// güncelleme birbirinin alanını geri alabilirdi (lost update). Kilit ayrıca
// "bu oranın kuralı var mı" denetimini de güvenilir kılar — denetim ile yazma
// arasına giren bir kural ekleme, kurallı bir oranı varsayılan yapabilirdi.
func (r *Repo) UpdateTaxRate(
	ctx context.Context,
	id string,
	patch models.TaxRatePatch,
	now time.Time,
) (models.TaxRate, error) {
	if err := r.ready(); err != nil {
		return models.TaxRate{}, err
	}

	var out models.TaxRate
	err := r.inTx(ctx, func(q *taxdb.Queries) error {
		row, err := q.GetTaxRateForUpdate(ctx, id)
		if err != nil {
			return notFoundOr(err, CodeTaxRateNotFound, "vergi oranı bulunamadı: %s", id)
		}
		current, err := toTaxRate(row)
		if err != nil {
			return err
		}

		updated := current.Patched(patch)
		if updated.IsDefault && !current.IsDefault {
			// Varsayılan yapılan bir oranın kuralı olamaz: "kuralsız oran her
			// şeye uygulanır" ile "kurallı oran yalnızca eşleşene uygulanır"
			// aynı satırda birleşseydi oranın kapsamı okunamaz hâle gelirdi.
			count, countErr := q.CountTaxRateRulesByRate(ctx, id)
			if countErr != nil {
				return wrapDB(countErr, "vergi oranının kuralları sayılamadı: %s", id)
			}
			if count > 0 {
				return errors.Conflict(CodeConstraintViolation,
					"%s oranının %d kuralı var; kurallı bir oran varsayılan yapılamaz", id, count)
			}
		}

		metadata, err := fromJSONMap(updated.Metadata)
		if err != nil {
			return err
		}

		written, err := q.UpdateTaxRate(ctx, taxdb.UpdateTaxRateParams{
			ID:        id,
			Name:      updated.Name,
			Code:      optionalText(updated.RateCode()),
			RateBps:   updated.RateBps,
			IsDefault: updated.IsDefault,
			Metadata:  metadata,
			UpdatedAt: fromTime(now),
		})
		if err != nil {
			return wrapDB(err, "vergi oranı güncellenemedi: %s", id)
		}

		out, err = toTaxRate(written)
		return err
	})
	if err != nil {
		return models.TaxRate{}, err
	}
	return out, nil
}

// DeleteTaxRate oranı ve kurallarını TEK işlemde yumuşak siler.
//
// Kurallar da silinir: silinmiş bir orana bağlı canlı kural hiçbir hesaba
// girmez ama aynı referansa yazılmak istenen yeni bir kuralın benzersizlik
// indeksiyle çakışırdı.
func (r *Repo) DeleteTaxRate(ctx context.Context, id string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	return r.inTx(ctx, func(q *taxdb.Queries) error {
		if _, err := q.SoftDeleteTaxRate(ctx, taxdb.SoftDeleteTaxRateParams{
			ID:        id,
			DeletedAt: fromTime(now),
		}); err != nil {
			return notFoundOr(err, CodeTaxRateNotFound, "vergi oranı bulunamadı: %s", id)
		}

		if err := q.SoftDeleteTaxRateRulesByRates(ctx, taxdb.SoftDeleteTaxRateRulesByRatesParams{
			RateIds:   []string{id},
			DeletedAt: fromTime(now),
		}); err != nil {
			return wrapDB(err, "vergi oranının kuralları silinemedi: %s", id)
		}
		return nil
	})
}

// toTaxRate üretilen satırı domain modeline çevirir.
func toTaxRate(row taxdb.TaxRate) (models.TaxRate, error) {
	metadata, err := toJSONMap(row.Metadata)
	if err != nil {
		return models.TaxRate{}, err
	}
	return models.TaxRate{
		ID:          row.ID,
		TaxRegionID: row.TaxRegionID,
		Name:        row.Name,
		Code:        row.Code,
		RateBps:     row.RateBps,
		IsDefault:   row.IsDefault,
		Metadata:    metadata,
		CreatedAt:   toTime(row.CreatedAt),
		UpdatedAt:   toTime(row.UpdatedAt),
		DeletedAt:   toTimePtr(row.DeletedAt),
	}, nil
}

// toTaxRates satır dilimini domain modellerine çevirir.
func toTaxRates(rows []taxdb.TaxRate) ([]models.TaxRate, error) {
	out := make([]models.TaxRate, 0, len(rows))
	for i := range rows {
		rate, err := toTaxRate(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, rate)
	}
	return out, nil
}
