package models_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/b2b/models"
)

// TestHarcamaPenceresiTakvimeGoreHesaplanir modülün bir sonraki adıma
// bıraktığı sözleşmeyi sabitler.
//
// Pencere KAYDIN AÇILIŞINA göre değil TAKVİME göre başlar: aylık limit, şirket
// ayın 20'sinde açılmış olsa bile ayın 1'inde sıfırlanır. Muhasebe dönemleri
// takvimle yürür ve kayan bir ay hiçbir mali raporla örtüşmezdi.
func TestHarcamaPenceresiTakvimeGoreHesaplanir(t *testing.T) {
	t.Parallel()

	simdi := time.Date(2026, time.March, 17, 9, 30, 45, 0, time.UTC)

	aylik := models.ResetMonthly.WindowStart(simdi)
	require.NotNil(t, aylik)
	assert.Equal(t, time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC), *aylik)

	yillik := models.ResetYearly.WindowStart(simdi)
	require.NotNil(t, yillik)
	assert.Equal(t, time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC), *yillik)

	assert.Nil(t, models.ResetNever.WindowStart(simdi),
		"sıfırlama yoksa pencere de yoktur; limit tüm geçmişe uygulanır")
}

// TestHarcamaPenceresiYerelSaatiDegilUTCyiKullanir aynı şirketin iki farklı
// ülkedeki çalışanı için ayın AYNI anda başladığını doğrular.
//
// Yerel saat kullanılsaydı, aynı limit iki çalışan için farklı anlarda
// sıfırlanır ve şirket toplamı hiçbir zaman tek bir döneme oturmazdı.
func TestHarcamaPenceresiYerelSaatiDegilUTCyiKullanir(t *testing.T) {
	t.Parallel()

	// UTC'ye göre 1 Nisan 00:30, ama UTC-3'te hâlâ 31 Mart.
	konum := time.FixedZone("UTC-3", -3*60*60)
	simdi := time.Date(2026, time.April, 1, 0, 30, 0, 0, time.UTC).In(konum)

	baslangic := models.ResetMonthly.WindowStart(simdi)
	require.NotNil(t, baslangic)
	assert.Equal(t, time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC), *baslangic,
		"pencere UTC takvimine göre hesaplanmalı")
}

// TestTanimsizPeriyotPencereAcmaz enum dışında bir değerin en güvenli sonuca
// düştüğünü doğrular.
//
// "Sınırsız pencere" dönmek, limiti sessizce genişletmek olurdu; değer zaten
// veritabanına giremez (CHECK) ama davranış yine de belirli olmalıdır.
func TestTanimsizPeriyotPencereAcmaz(t *testing.T) {
	t.Parallel()

	var haftalik models.SpendingResetPeriod = "weekly"
	assert.False(t, haftalik.Valid())
	assert.Nil(t, haftalik.WindowStart(time.Now()))

	assert.True(t, models.ResetMonthly.Valid())
	assert.True(t, models.ResetYearly.Valid())
	assert.True(t, models.ResetNever.Valid())
}

// TestSifirLimitSinirsizdanFarklidir modelin en kolay karıştırılan ayrımını
// sabitler: nil "sınırsız", 0 ise "hiç harcayamaz".
func TestSifirLimitSinirsizdanFarklidir(t *testing.T) {
	t.Parallel()

	sifir := int64(0)
	assert.True(t, models.CompanyEmployee{SpendingLimit: &sifir}.HasSpendingLimit())
	assert.False(t, models.CompanyEmployee{}.HasSpendingLimit())
}

// TestNormalizasyonSaklamaBicimineCevirir e-postanın küçük, kodların BÜYÜK
// harfe indiğini doğrular.
func TestNormalizasyonSaklamaBicimineCevirir(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "muhasebe@acme.example", models.NormalizeEmail("  Muhasebe@Acme.Example "))
	assert.Equal(t, "TR", models.NormalizeCountryCode(" tr "))
	assert.Equal(t, "TRY", models.NormalizeCurrencyCode("try"))
}

// TestKimliklerOnekliVeZamanSiralidir plan Bölüm 8'in kimlik kuralını
// doğrular: önek türü söyler, gövde oluşturma sırasını taşır.
func TestKimliklerOnekliVeZamanSiralidir(t *testing.T) {
	t.Parallel()

	once := time.Date(2026, time.March, 17, 9, 0, 0, 0, time.UTC)
	sonra := once.Add(time.Second)

	ilk := models.NewCompanyID(once)
	ikinci := models.NewCompanyID(sonra)

	assert.True(t, strings.HasPrefix(ilk, models.CompanyIDPrefix))
	assert.Len(t, strings.TrimPrefix(ilk, models.CompanyIDPrefix), models.IDBodyLength())
	assert.Less(t, ilk, ikinci, "kimlikler sözlüksel sırada da zaman sıralı olmalı")

	calisan := models.NewEmployeeID(once)
	assert.True(t, strings.HasPrefix(calisan, models.EmployeeIDPrefix))
	assert.NotEqual(t, models.CompanyIDPrefix, models.EmployeeIDPrefix,
		"iki tür kimlik tek bakışta ayrılmalı")
}
