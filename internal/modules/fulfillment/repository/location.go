package repository

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/repository/fulfillmentdb"
)

// Bu dosya depo seçim POLİTİKASININ erişimidir: hangi depo hangi bölgeye
// hizmet eder ve hangi sırayla tercih edilir.
//
// İki tablo birlikte tek bir kavramı taşır (shipping_locations ve
// shipping_location_regions) ve bu paketten dışarı TEK model olarak çıkar:
// çağıranın iki tablo gördüğü bir yüzey, bölge bağlarını ayrı yönetme
// sorumluluğunu da ona devrederdi.
//
// Silme burada YUMUŞAK DEĞİLDİR; gerekçesi migration'ın başındadır. Bu yüzden
// hiçbir sorguda deleted_at süzgeci yoktur.

// UpsertShippingLocation deponun ÖNCELİĞİNİ yazar ya da üzerine yazar.
//
// Bölge bağlarına DOKUNMAZ: onları [Repository.ReplaceShippingLocationRegions]
// yazar. İkisi birlikte tek bir yazma sayılır ve çağıran onları AYNI işlemde
// çağırmalıdır (bkz. service katmanı); ayrı çağrılmaları, aradaki bir okumanın
// depoyu yeni önceliğiyle ama eski bölgeleriyle görmesi demektir.
func (r *Repository) UpsertShippingLocation(
	ctx context.Context,
	locationID string,
	priority int64,
) (models.ShippingLocation, error) {
	row, err := r.queries(ctx).UpsertShippingLocation(ctx, fulfillmentdb.UpsertShippingLocationParams{
		LocationID: locationID,
		Priority:   priority,
	})
	if err != nil {
		return models.ShippingLocation{}, classify(err, codeQueryFailed, "depo politikası yazılamadı")
	}
	return toShippingLocation(row), nil
}

// ReplaceShippingLocationRegions deponun bölge bağlarını TOPTAN yazar.
//
// Boş dilim geçerli bir girdidir ve "tüm bölgelere hizmet et" demektir; bağlar
// silinir, yerine hiçbir şey yazılmaz.
//
// Yalnızca [Repository.WithTx] içinde çağrılmalıdır: iki deyimden oluşur (sil,
// yaz) ve işlemsiz çağrıldığında aradaki bir okuma depoyu BÖLGESİZ görür —
// yani onu, kapsamı daralmış sanılan bir anda TÜM bölgelere açık bulur.
func (r *Repository) ReplaceShippingLocationRegions(
	ctx context.Context,
	locationID string,
	regionIDs []string,
) error {
	if _, ok := txFromContext(ctx); !ok {
		return errors.Internal(codeTxRequired,
			"bölge bağları işlem dışında yazılamaz: %s", locationID)
	}

	q := r.queries(ctx)
	if err := q.DeleteShippingLocationRegions(ctx, locationID); err != nil {
		return classify(err, codeQueryFailed, "depo bölge bağları silinemedi")
	}
	if len(regionIDs) == 0 {
		return nil
	}

	err := q.InsertShippingLocationRegions(ctx, fulfillmentdb.InsertShippingLocationRegionsParams{
		LocationID: locationID,
		RegionIds:  regionIDs,
	})
	if err != nil {
		return classify(err, codeQueryFailed, "depo bölge bağları yazılamadı")
	}
	return nil
}

// GetShippingLocation deponun politikasını bölgeleriyle döner; yoksa NotFound.
//
// Okuma TEK deyimdir. İki ayrı SELECT (önce satır, sonra bağlar) yırtık bir
// kayıt üretirdi: işlem dışında yapılan iki okuma iki ayrı anlık görüntüden
// gelir ve aralarına giren bir yazma, deponun YENİ önceliğiyle ESKİ bölgelerini
// yan yana gösterirdi. Yazma yolu bu yırtığı işlemle kapatıyor; okuma yolu tek
// deyimle kapatır.
func (r *Repository) GetShippingLocation(
	ctx context.Context,
	locationID string,
) (models.ShippingLocation, error) {
	row, err := r.queries(ctx).GetShippingLocation(ctx, locationID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ShippingLocation{}, locationNotFound(locationID)
		}
		return models.ShippingLocation{}, classify(err, codeQueryFailed, "depo politikası okunamadı")
	}
	return models.ShippingLocation{
		LocationID: row.LocationID,
		Priority:   row.Priority,
		RegionIDs:  row.RegionIds,
		CreatedAt:  toTime(row.CreatedAt),
		UpdatedAt:  toTime(row.UpdatedAt),
	}, nil
}

// ListShippingLocations politikaları bağlarıyla birlikte sayfalar; ikinci değer
// TÜM satırların sayısıdır.
//
// Bağlar sayfa sorgusunun İÇİNDE toplanır: depo başına ikinci bir sorgu (N+1)
// yapılmaz ve tekil okumadaki yırtılma kapısı burada da kapalı kalır.
func (r *Repository) ListShippingLocations(
	ctx context.Context,
	filter models.LocationFilter,
) ([]models.ShippingLocation, int64, error) {
	q := r.queries(ctx)

	rows, err := q.ListShippingLocations(ctx, fulfillmentdb.ListShippingLocationsParams{
		RowLimit:  filter.Limit,
		RowOffset: filter.Offset,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "depo politikaları listelenemedi")
	}

	total, err := q.CountShippingLocations(ctx)
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "depo politikaları sayılamadı")
	}

	out := make([]models.ShippingLocation, 0, len(rows))
	for _, row := range rows {
		out = append(out, models.ShippingLocation{
			LocationID: row.LocationID,
			Priority:   row.Priority,
			RegionIDs:  row.RegionIds,
			CreatedAt:  toTime(row.CreatedAt),
			UpdatedAt:  toTime(row.UpdatedAt),
		})
	}
	return out, total, nil
}

// DeleteShippingLocation politikayı KALICI olarak siler; bölge bağları
// birlikte düşer. Kayıt yoksa NotFound.
//
// Silinen satır sayısı denetlenir çünkü DELETE, olmayan bir satır için de
// hatasız döner: denetim olmasaydı yanlış bir kimlikle yapılan silme başarılı
// görünürdü.
func (r *Repository) DeleteShippingLocation(ctx context.Context, locationID string) error {
	affected, err := r.queries(ctx).DeleteShippingLocation(ctx, locationID)
	if err != nil {
		return classify(err, codeQueryFailed, "depo politikası silinemedi")
	}
	if affected == 0 {
		return locationNotFound(locationID)
	}
	return nil
}

// LocationPolicies aday depoların seçim anında kararı etkileyen olgularını
// TEK sorguda döner.
//
// Dönen dilim YALNIZCA politikası OLAN adayları içerir ve aday listesinden
// KISA olabilir. Eksik olan aday bir hata değildir: politikası olmayan depo
// varsayılan sayılır ve bu ayrımı çağıran yapar.
//
// Hedef bölge PARAMETRE DEĞİLDİR: eşleştirmeyi sorgu değil servis katmanındaki
// saf fonksiyon yapar. Bölge SQL'e verilseydi kural veritabanına taşınır ve
// gerçek bir Postgres olmadan sınanamaz olurdu; ayrıca elenen adayların hangi
// bölgelere bağlı olduğu geri dönmez, yani hata mesajı sebebi yazamazdı.
//
// Aday listesi boşsa hiç sorgu yapılmaz.
func (r *Repository) LocationPolicies(
	ctx context.Context,
	locationIDs []string,
) ([]models.LocationPolicy, error) {
	if len(locationIDs) == 0 {
		return nil, nil
	}

	rows, err := r.queries(ctx).ShippingLocationPolicies(ctx, locationIDs)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "depo politikaları okunamadı")
	}

	out := make([]models.LocationPolicy, 0, len(rows))
	for _, row := range rows {
		out = append(out, models.LocationPolicy{
			LocationID: row.LocationID,
			Priority:   row.Priority,
			RegionIDs:  row.RegionIds,
		})
	}
	return out, nil
}

// toShippingLocation öncelik yazımının döndürdüğü satırı modele çevirir.
//
// Bölge alanı BOŞ kalır ve bu, "tüm bölgelere hizmet ediyor" anlamına GELMEZ —
// bu yoldan dönen kayıt eksiktir. Çağıranın onu doğrudan kullanmaması gerekir:
// öncelik yazımının tek çağıranı, aynı işlem içinde bağları hemen ardından
// YAZAN ve sonucu GetShippingLocation ile yeniden OKUYAN servistir.
func toShippingLocation(row fulfillmentdb.ShippingLocation) models.ShippingLocation {
	return models.ShippingLocation{
		LocationID: row.LocationID,
		Priority:   row.Priority,
		CreatedAt:  toTime(row.CreatedAt),
		UpdatedAt:  toTime(row.UpdatedAt),
	}
}
