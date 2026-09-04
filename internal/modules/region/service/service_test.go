package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/region/models"
)

// testClock testlerin sabit zaman kaynağıdır; zamana bağlı alanlar
// belirlenimci olsun diye kullanılır.
var testClock = time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

// newTestService sahte depo üzerinde çalışan bir servis ve deposunu döner.
func newTestService(t *testing.T) (*Service, *memRepo) {
	t.Helper()

	repo := newMemRepo()
	svc := New(repo, Options{Now: func() time.Time { return testClock }})
	return svc, repo
}

// newRegion test için bir bölge oluşturur.
func newRegion(t *testing.T, svc *Service, currency string) models.Region {
	t.Helper()

	region, err := svc.CreateRegion(context.Background(), CreateRegionInput{
		Name:           "Test " + currency,
		CurrencyCode:   currency,
		AutomaticTaxes: true,
		TaxRate:        2000,
	})
	require.NoError(t, err)
	return region
}

// TestCreateRegionNormalizesAndValidates bölge oluşturmanın normalleştirme ve
// doğrulama kurallarını kanıtlar.
func TestCreateRegionNormalizesAndValidates(t *testing.T) {
	ctx := context.Background()

	t.Run("para birimi büyük harfe çevrilir", func(t *testing.T) {
		svc, _ := newTestService(t)

		region, err := svc.CreateRegion(ctx, CreateRegionInput{
			Name: "  Türkiye  ", CurrencyCode: " try ", TaxRate: 2000,
		})
		require.NoError(t, err)
		assert.Equal(t, "TRY", region.CurrencyCode, "kod BÜYÜK harf saklanmalı")
		assert.Equal(t, "Türkiye", region.Name, "ad kırpılmalı")
		assert.True(t, strings.HasPrefix(region.ID, models.RegionIDPrefix),
			"kimlik %q önekiyle başlamalı, %q üretildi", models.RegionIDPrefix, region.ID)
		assert.Equal(t, testClock, region.CreatedAt)
	})

	t.Run("geçersiz para birimi kodu reddedilir", func(t *testing.T) {
		svc, repo := newTestService(t)

		for _, code := range []string{"", "TR", "TRYX", "TR1", "T RY", "₺₺₺"} {
			_, err := svc.CreateRegion(ctx, CreateRegionInput{Name: "X", CurrencyCode: code})
			require.Error(t, err, "%q kabul edilmemeli", code)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "kod: %q", code)
		}
		assert.Zero(t, repo.callCount("CreateRegion"),
			"biçimsel olarak geçersiz kod için veritabanına hiç gidilmemeli")
	})

	t.Run("tanımsız para birimi reddedilir", func(t *testing.T) {
		svc, _ := newTestService(t)

		// Biçimsel olarak geçerli ama referans tablosunda yok.
		_, err := svc.CreateRegion(ctx, CreateRegionInput{Name: "X", CurrencyCode: "XYZ"})
		require.Error(t, err)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	})

	t.Run("boş ad reddedilir", func(t *testing.T) {
		svc, _ := newTestService(t)

		_, err := svc.CreateRegion(ctx, CreateRegionInput{Name: "   ", CurrencyCode: "TRY"})
		require.Error(t, err)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	})

	t.Run("aralık dışı vergi oranı reddedilir", func(t *testing.T) {
		svc, _ := newTestService(t)

		for _, rate := range []int32{-1, models.MaxTaxRate + 1} {
			_, err := svc.CreateRegion(ctx, CreateRegionInput{
				Name: "X", CurrencyCode: "TRY", TaxRate: rate,
			})
			require.Error(t, err, "oran: %d", rate)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "oran: %d", rate)
		}
	})

	t.Run("sınır değerdeki vergi oranları kabul edilir", func(t *testing.T) {
		svc, _ := newTestService(t)

		for _, rate := range []int32{models.MinTaxRate, models.MaxTaxRate} {
			region, err := svc.CreateRegion(ctx, CreateRegionInput{
				Name: "X", CurrencyCode: "TRY", TaxRate: rate,
			})
			require.NoError(t, err, "oran: %d", rate)
			assert.Equal(t, rate, region.TaxRate)
		}
	})
}

// TestGetRegionRejectsForeignID yanlış türde bir kimliğin "bulunamadı" değil,
// doğrulama hatası döndüğünü kanıtlar.
//
// Önekli kimliklerin varlık sebebi budur: bir customer idnin bölge yerine
// geçmesi, sessiz bir 404 değil ne olduğu belli bir 422 olmalıdır.
func TestGetRegionRejectsForeignID(t *testing.T) {
	svc, repo := newTestService(t)

	_, err := svc.GetRegion(context.Background(), "cust_01ABCDEF")

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Zero(t, repo.callCount("GetRegion"), "yanlış önekli kimlik için depoya gidilmemeli")
}

// TestUpdateRegionIsPartial kısmi güncellemenin yalnızca verilen alanları
// değiştirdiğini kanıtlar.
func TestUpdateRegionIsPartial(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	region := newRegion(t, svc, "TRY")

	name := "Yeni Ad"
	updated, err := svc.UpdateRegion(ctx, region.ID, UpdateRegionInput{Name: &name})
	require.NoError(t, err)

	assert.Equal(t, "Yeni Ad", updated.Name)
	assert.Equal(t, region.CurrencyCode, updated.CurrencyCode, "verilmeyen para birimi değişmemeli")
	assert.Equal(t, region.TaxRate, updated.TaxRate, "verilmeyen vergi oranı değişmemeli")
	assert.Equal(t, region.AutomaticTaxes, updated.AutomaticTaxes, "verilmeyen bayrak değişmemeli")
}

// TestUpdateRegionZeroValuesAreWritten sıfır değerli bir yamanın "dokunma"
// sayılmadığını kanıtlar.
//
// İşaretçi kullanmanın tek sebebi budur: false ve 0 geçerli değerlerdir ve
// alanın verilip verilmediğinden ayırt edilmelidir.
func TestUpdateRegionZeroValuesAreWritten(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	region := newRegion(t, svc, "TRY")
	require.True(t, region.AutomaticTaxes)
	require.Equal(t, int32(2000), region.TaxRate)

	automatic := false
	rate := int32(0)
	updated, err := svc.UpdateRegion(ctx, region.ID, UpdateRegionInput{
		AutomaticTaxes: &automatic,
		TaxRate:        &rate,
	})
	require.NoError(t, err)

	assert.False(t, updated.AutomaticTaxes, "false yazılmalı, 'dokunma' sayılmamalı")
	assert.Zero(t, updated.TaxRate, "0 yazılmalı, 'dokunma' sayılmamalı")
	assert.Equal(t, region.Name, updated.Name)
}

// TestUpdateRegionRejectsEmptyPatch boş bir yamanın sessizce başarılı
// dönmediğini kanıtlar.
func TestUpdateRegionRejectsEmptyPatch(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService(t)
	region := newRegion(t, svc, "TRY")
	repo.resetCalls()

	_, err := svc.UpdateRegion(ctx, region.ID, UpdateRegionInput{})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Zero(t, repo.callCount("UpdateRegion"), "boş yama için depoya gidilmemeli")
}

// TestUpdateRegionValidatesCurrency yamadaki para biriminin de
// normalleştirilip doğrulandığını kanıtlar.
func TestUpdateRegionValidatesCurrency(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	region := newRegion(t, svc, "TRY")

	bad := "tryx"
	_, err := svc.UpdateRegion(ctx, region.ID, UpdateRegionInput{CurrencyCode: &bad})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	good := "usd"
	updated, err := svc.UpdateRegion(ctx, region.ID, UpdateRegionInput{CurrencyCode: &good})
	require.NoError(t, err)
	assert.Equal(t, "USD", updated.CurrencyCode, "yamadaki kod da BÜYÜK harfe çevrilmeli")
}

// TestAddCountryToRegionUniqueness bir ülkenin en fazla bir bölgeye ait
// olabileceğini kanıtlar.
func TestAddCountryToRegionUniqueness(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	first := newRegion(t, svc, "TRY")
	second := newRegion(t, svc, "USD")

	country, err := svc.AddCountryToRegion(ctx, first.ID, "tr")
	require.NoError(t, err)
	assert.Equal(t, "TR", country.Code, "ülke kodu BÜYÜK harfe çevrilmeli")
	require.NotNil(t, country.RegionID)
	assert.Equal(t, first.ID, *country.RegionID)

	_, err = svc.AddCountryToRegion(ctx, second.ID, "TR")
	require.Error(t, err, "aynı ülke ikinci bir bölgeye eklenememeli")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))

	// İlk bölgedeki bağ bozulmamış olmalı.
	resolved, err := svc.ResolveRegionForCountry(ctx, "TR")
	require.NoError(t, err)
	assert.Equal(t, first.ID, resolved.ID)
}

// TestAddCountryToRegionIsIdempotent aynı bölgeye tekrar ekleme isteğinin hata
// üretmediğini kanıtlar.
func TestAddCountryToRegionIsIdempotent(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	region := newRegion(t, svc, "TRY")

	_, err := svc.AddCountryToRegion(ctx, region.ID, "TR")
	require.NoError(t, err)

	again, err := svc.AddCountryToRegion(ctx, region.ID, "TR")
	require.NoError(t, err, "tekrarlanan yönetim isteği hata üretmemeli")
	require.NotNil(t, again.RegionID)
	assert.Equal(t, region.ID, *again.RegionID)
}

// TestAddCountryToRegionValidatesInput geçersiz kimlik ve ülke kodunun depoya
// hiç gitmeden reddedildiğini kanıtlar.
func TestAddCountryToRegionValidatesInput(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService(t)
	region := newRegion(t, svc, "TRY")
	repo.resetCalls()

	_, err := svc.AddCountryToRegion(ctx, "prod_01", "TR")
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	for _, code := range []string{"", "T", "TUR", "T1"} {
		_, err = svc.AddCountryToRegion(ctx, region.ID, code)
		require.Error(t, err, "%q kabul edilmemeli", code)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "kod: %q", code)
	}
	assert.Zero(t, repo.callCount("AssignCountry"), "geçersiz girdi için depoya gidilmemeli")
}

// TestRemoveCountryFromRegion ülkenin bölgeden çıkarılmasını ve yanlış bölgeyle
// yapılan çağrının reddini kanıtlar.
func TestRemoveCountryFromRegion(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	first := newRegion(t, svc, "TRY")
	second := newRegion(t, svc, "USD")

	_, err := svc.AddCountryToRegion(ctx, first.ID, "TR")
	require.NoError(t, err)

	err = svc.RemoveCountryFromRegion(ctx, second.ID, "TR")
	require.Error(t, err, "başka bölgenin ülkesi çıkarılamamalı")
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))

	require.NoError(t, svc.RemoveCountryFromRegion(ctx, first.ID, "tr"))

	_, err = svc.ResolveRegionForCountry(ctx, "TR")
	require.Error(t, err)
	assert.Equal(t, CodeCountryUnassigned, errors.CodeOf(err))
}

// TestResolveRegionForCountry çözümün mutlu yolunu ve üç ayrı başarısızlık
// durumunu kanıtlar.
//
// Üçü de errors.NotFound döner ama KODLARI farklıdır; çağıran hangi
// düzeltmenin gerektiğini kodundan bilir.
func TestResolveRegionForCountry(t *testing.T) {
	ctx := context.Background()

	t.Run("ülkeden bölgeye tek sorguda gidilir", func(t *testing.T) {
		svc, repo := newTestService(t)
		region := newRegion(t, svc, "TRY")
		_, err := svc.AddCountryToRegion(ctx, region.ID, "TR")
		require.NoError(t, err)
		repo.resetCalls()

		resolved, err := svc.ResolveRegionForCountry(ctx, "tr")
		require.NoError(t, err)
		assert.Equal(t, region.ID, resolved.ID)
		assert.Equal(t, "TRY", resolved.CurrencyCode)
		assert.Equal(t, 1, repo.callCount("GetRegionByCountry"))
		assert.Zero(t, repo.callCount("GetCountry"), "mutlu yolda ikinci sorgu yapılmamalı")
	})

	t.Run("tanımsız ülke kodu doğrulamada elenir", func(t *testing.T) {
		svc, repo := newTestService(t)

		_, err := svc.ResolveRegionForCountry(ctx, "TURKIYE")
		require.Error(t, err)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
		assert.Zero(t, repo.callCount("GetRegionByCountry"))
	})

	t.Run("bilinmeyen ülke bulunamadı döner", func(t *testing.T) {
		svc, _ := newTestService(t)

		_, err := svc.ResolveRegionForCountry(ctx, "ZZ")
		require.Error(t, err)
		assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
		assert.Equal(t, "country_not_found", errors.CodeOf(err))
	})

	t.Run("bölgesiz ülke ayrı bir kodla döner", func(t *testing.T) {
		svc, _ := newTestService(t)

		_, err := svc.ResolveRegionForCountry(ctx, "DE")
		require.Error(t, err)
		assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
		assert.Equal(t, CodeCountryUnassigned, errors.CodeOf(err))
	})

	t.Run("bölgesi silinmiş ülke tutarsızlık kodu döner", func(t *testing.T) {
		svc, repo := newTestService(t)
		region := newRegion(t, svc, "TRY")
		_, err := svc.AddCountryToRegion(ctx, region.ID, "TR")
		require.NoError(t, err)

		// Bölgeyi ülkeleri serbest BIRAKMADAN sil: gerçek depoda oluşmayan,
		// ama servisin ayırt edebilmesi gereken tutarsız durum.
		repo.mu.Lock()
		stale := repo.regions[region.ID]
		deleted := testClock
		stale.DeletedAt = &deleted
		repo.regions[region.ID] = stale
		repo.mu.Unlock()

		_, err = svc.ResolveRegionForCountry(ctx, "TR")
		require.Error(t, err)
		assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
		assert.Equal(t, CodeCountryRegionMissing, errors.CodeOf(err))
	})
}

// TestDeleteRegionReleasesCountries silinen bölgenin ülkelerinin serbest
// kaldığını kanıtlar.
//
// Serbest bırakılmasaydı ülke ölü bir bölgeye bağlı kalır, başka hiçbir bölgeye
// eklenemez ve o ülkedeki müşteri için sepet açılamazdı.
func TestDeleteRegionReleasesCountries(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	first := newRegion(t, svc, "TRY")
	second := newRegion(t, svc, "USD")

	_, err := svc.AddCountryToRegion(ctx, first.ID, "TR")
	require.NoError(t, err)

	require.NoError(t, svc.DeleteRegion(ctx, first.ID))

	_, err = svc.GetRegion(ctx, first.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err), "silinen bölge okunamamalı")

	// Ülke artık serbesttir ve başka bir bölgeye eklenebilir.
	country, err := svc.AddCountryToRegion(ctx, second.ID, "TR")
	require.NoError(t, err, "serbest kalan ülke başka bölgeye eklenebilmeli")
	require.NotNil(t, country.RegionID)
	assert.Equal(t, second.ID, *country.RegionID)
}

// TestListCountriesValidatesRegionFilter bölge süzgecinin doğrulandığını
// kanıtlar.
//
// Doğrulanmasaydı yanlış türde bir kimlik boş liste döndürür ve istemci
// bölgenin ülkesi olmadığını sanırdı.
func TestListCountriesValidatesRegionFilter(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService(t)

	for _, id := range []string{"", "prod_01"} {
		filter := id
		_, err := svc.ListCountries(ctx, ListCountriesInput{RegionID: &filter})
		require.Error(t, err, "kimlik: %q", id)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "kimlik: %q", id)
	}
	assert.Zero(t, repo.callCount("ListCountries"))
}

// TestPagingIsNormalized sayfalama sınırlarının uygulandığını ve UYGULANAN
// değerin geri bildirildiğini kanıtlar.
func TestPagingIsNormalized(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	newRegion(t, svc, "TRY")

	page, err := svc.ListRegions(ctx, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, DefaultLimit, page.Limit, "limit verilmezse varsayılan uygulanmalı")

	page, err = svc.ListRegions(ctx, MaxLimit+1000, 0)
	require.NoError(t, err)
	assert.Equal(t, MaxLimit, page.Limit, "limit azami değerle kırpılmalı")

	_, err = svc.ListRegions(ctx, 10, -1)
	require.Error(t, err, "negatif offset reddedilmeli")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestListCurrenciesReturnsSeededSet para birimi listesinin ondalık basamak
// bilgisiyle döndüğünü kanıtlar.
func TestListCurrenciesReturnsSeededSet(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)

	page, err := svc.ListCurrencies(ctx, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(4), page.Count)

	digits := map[string]int32{}
	for _, currency := range page.Items {
		digits[currency.Code] = currency.DecimalDigits
	}
	assert.Equal(t, int32(2), digits["TRY"])
	assert.Equal(t, int32(0), digits["JPY"], "JPY ondalıksızdır")
	assert.Equal(t, int32(3), digits["KWD"], "KWD üç basamaklıdır")
}

// TestGetCurrencyNormalizesCode para birimi okumasının kodu normalleştirdiğini
// kanıtlar.
func TestGetCurrencyNormalizesCode(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)

	currency, err := svc.GetCurrency(ctx, " jpy ")
	require.NoError(t, err)
	assert.Equal(t, "JPY", currency.Code)
	assert.Zero(t, currency.DecimalDigits)

	_, err = svc.GetCurrency(ctx, "JP")
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	_, err = svc.GetCurrency(ctx, "XYZ")
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestUnconfiguredServiceReturnsTypedError deposuz bir servisin panik değil
// tipli hata döndürdüğünü kanıtlar.
func TestUnconfiguredServiceReturnsTypedError(t *testing.T) {
	ctx := context.Background()
	svc := New(nil, Options{})

	_, err := svc.CreateRegion(ctx, CreateRegionInput{Name: "X", CurrencyCode: "TRY"})
	require.Error(t, err)
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err))

	_, err = svc.ResolveRegionForCountry(ctx, "TR")
	require.Error(t, err)
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err))
}
