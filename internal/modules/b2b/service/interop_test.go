package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/b2b/models"
)

// kuralGovdesi yüzeyin döndüğü gövdenin testlerdeki karşılığıdır.
//
// Tip AYRI tanımlanır ve interopSpendingRule yeniden kullanılmaz: sınanan şey
// tam olarak ALAN ADLARIDIR ve üretim tipini kullanan bir test, adlar değişse
// bile geçerdi. Tüketici (order modülü) bu adları kendi paketinde ayrıca yazar
// ve iki tarafı derleyici birbirine bağlayamaz.
type kuralGovdesi struct {
	Limited       bool   `json:"limited"`
	SpendingLimit int64  `json:"spending_limit"`
	CurrencyCode  string `json:"currency_code"`
	WindowStart   string `json:"window_start"`
}

// kuraliCoz yüzeyi çağırır ve gövdeyi çözer.
func kuraliCoz(t *testing.T, svc *Service, customerID string) kuralGovdesi {
	t.Helper()

	payload, err := NewInterop(svc).SpendingLimitJSON(t.Context(), customerID)
	require.NoError(t, err)

	var kural kuralGovdesi
	require.NoError(t, json.Unmarshal(payload, &kural))
	return kural
}

// calisanEkle şirkete verilen limitle bir çalışan ekler.
func calisanEkle(t *testing.T, svc *Service, companyID, customerID string, limit *int64) {
	t.Helper()

	_, err := svc.CreateEmployee(t.Context(), EmployeeInput{
		CompanyID:     companyID,
		CustomerID:    customerID,
		SpendingLimit: limit,
	})
	require.NoError(t, err)
}

// TestKuralLimitiVePencereyiYayimlar limitli çalışanın kuralını doğrular.
//
// Şirketin periyodu aylıktır ve sabit saat ayın 17'sidir; pencere bu yüzden
// ayın 1'inde 00:00 UTC'de başlamalıdır. Pencerenin TAKVİME göre olduğu
// (şirketin açılış gününe göre kaymadığı) yalnızca burada, dönen dizede
// görülür.
func TestKuralLimitiVePencereyiYayimlar(t *testing.T) {
	svc, _, _ := yeniServis(t)
	company := yeniSirket(t, svc)
	limit := int64(500_000)
	calisanEkle(t, svc, company.ID, "cust_01", &limit)

	kural := kuraliCoz(t, svc, "cust_01")

	assert.True(t, kural.Limited)
	assert.Equal(t, int64(500_000), kural.SpendingLimit)
	assert.Equal(t, "TRY", kural.CurrencyCode, "limit ŞİRKETİN para biriminde ifade edilir")
	assert.Equal(t, "2026-03-01T00:00:00Z", kural.WindowStart)
}

// TestKuralYillikPencereyiYayimlar yıllık periyodun 1 Ocak'ta başladığını
// doğrular.
func TestKuralYillikPencereyiYayimlar(t *testing.T) {
	svc, _, _ := yeniServis(t)
	girdi := gecerliSirket()
	girdi.SpendingLimitResetPeriod = string(models.ResetYearly)
	company, err := svc.CreateCompany(t.Context(), girdi)
	require.NoError(t, err)

	limit := int64(10)
	calisanEkle(t, svc, company.ID, "cust_01", &limit)

	assert.Equal(t, "2026-01-01T00:00:00Z", kuraliCoz(t, svc, "cust_01").WindowStart)
}

// TestKuralPencerisizPeriyottaBosDoner "never" periyodunun karşılığını
// doğrular.
//
// Pencere yoksa alan BOŞ dizedir. Sıfır zaman damgası göndermek, tüketicinin
// "0001-01-01'den beri" ile "pencere yok"u ayırt etmesini beklemek olurdu ve
// ilki bir tarih gibi görünüp sessizce yanlış bir aralık üretebilirdi.
func TestKuralPencerisizPeriyottaBosDoner(t *testing.T) {
	svc, _, _ := yeniServis(t)
	girdi := gecerliSirket()
	girdi.SpendingLimitResetPeriod = string(models.ResetNever)
	company, err := svc.CreateCompany(t.Context(), girdi)
	require.NoError(t, err)

	limit := int64(10)
	calisanEkle(t, svc, company.ID, "cust_01", &limit)

	kural := kuraliCoz(t, svc, "cust_01")
	assert.True(t, kural.Limited)
	assert.Empty(t, kural.WindowStart)
}

// TestKuralSinirsizCalisandaLimitsizDoner nil limitin "kural yok"a çözüldüğünü
// doğrular.
//
// nil SINIRSIZ demektir; limitli bir kural olarak yayımlansaydı tüketici onu
// bir tavan sanar ve sınırsız çalışan sıfır limitliye dönerdi.
func TestKuralSinirsizCalisandaLimitsizDoner(t *testing.T) {
	svc, _, _ := yeniServis(t)
	company := yeniSirket(t, svc)
	calisanEkle(t, svc, company.ID, "cust_01", nil)

	assert.False(t, kuraliCoz(t, svc, "cust_01").Limited)
}

// TestKuralSifirLimitiKORUR 0 ile nil ayrımının sınırda kaybolmadığını
// doğrular.
//
// Sıfır limitli çalışan SINIRLIDIR ve hiç harcayamaz. İkisini tek cevaba
// indirmek, "limiti sıfırladım" diyen şirkete sınırsız bir çalışan verirdi.
func TestKuralSifirLimitiKORUR(t *testing.T) {
	svc, _, _ := yeniServis(t)
	company := yeniSirket(t, svc)
	sifir := int64(0)
	calisanEkle(t, svc, company.ID, "cust_01", &sifir)

	kural := kuraliCoz(t, svc, "cust_01")
	assert.True(t, kural.Limited)
	assert.Zero(t, kural.SpendingLimit)
}

// TestKuralCalisanOlmayanMusteride HATA DÖNMEZ.
//
// Kurulumun çoğunluğu B2C'dir ve tüketici bu yüzeyi HER sipariş için çağırır;
// "bu müşteri bir şirketin çalışanı değil" onun için normal yoldur. Hata
// dönmek, tüketiciyi "kural yok" ile "kuralı öğrenemedik" arasında ayrım
// yapamaz hâle getirirdi.
func TestKuralCalisanOlmayanMusteride(t *testing.T) {
	svc, _, _ := yeniServis(t)

	kural := kuraliCoz(t, svc, "cust_BAGSIZ")
	assert.False(t, kural.Limited)
}

// TestKuralTaninmayanKimlikteLimitsizDoner customer id bile olmayan bir
// dizede hata dönmediğini doğrular.
//
// Böyle bir kimlik çalışan olarak BAĞLANAMAZ (CreateEmployee önek denetimi
// yapar), yani "limiti yok" cevabı tahmin değil kanıtlanabilir bir olgudur.
// Hata dönmek, b2b'nin kimlik biçimi hakkındaki görüşünü her siparişin önüne
// koymak olurdu.
func TestKuralTaninmayanKimlikteLimitsizDoner(t *testing.T) {
	svc, _, _ := yeniServis(t)

	for _, kimlik := range []string{"", "cus_ESKI_ONEK", "  "} {
		payload, err := NewInterop(svc).SpendingLimitJSON(t.Context(), kimlik)
		require.NoError(t, err, "kimlik: %q", kimlik)

		var kural kuralGovdesi
		require.NoError(t, json.Unmarshal(payload, &kural))
		assert.False(t, kural.Limited, "kimlik: %q", kimlik)
	}
}

// TestKuralOkumaArizasiniGIZLEMEZ altyapı hatasının yutulmadığını doğrular.
//
// Bağ katmanı okunamadığında limitin ne olduğu BİLİNMEZ. "limitsiz" dönmek,
// link servisinin her arızasında harcama limitini sessizce kaldırmak olurdu;
// tüketici bu yüzden hatayı görmeli ve siparişi reddetmelidir.
func TestKuralOkumaArizasiniGIZLEMEZ(t *testing.T) {
	svc, _, links := yeniServis(t)
	links.failListByTo = errors.Internal("link_down", "bağ katmanı yanıt vermiyor")

	_, err := NewInterop(svc).SpendingLimitJSON(t.Context(), "cust_01")

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInternal))
}

// TestKuralSilinmisSirketteLimitsizDoner yumuşak silinmiş şirketin kuralının
// yayımlanmadığını doğrular.
//
// Şirket silindiğinde çalışanları da silinir ve bağları kaldırılır; geride
// kalmış bir bağ bile kuralı geri getirmemelidir. Aksi hâlde kapanmış bir
// şirketin limiti, var olmayan bir bütçeye karşı uygulanmaya devam ederdi.
func TestKuralSilinmisSirketteLimitsizDoner(t *testing.T) {
	svc, _, _ := yeniServis(t)
	company := yeniSirket(t, svc)
	limit := int64(500)
	calisanEkle(t, svc, company.ID, "cust_01", &limit)

	require.NoError(t, svc.DeleteCompany(t.Context(), company.ID))

	assert.False(t, kuraliCoz(t, svc, "cust_01").Limited)
}

// TestKuralPenceresiUTCdir dönen zamanın saat dilimi taşımadığını doğrular.
//
// Yerel bir saat dilimi, aynı şirketin iki ülkedeki çalışanı için ayın farklı
// anlarda başlaması demek olurdu; dize bu yüzden "Z" ile biter.
func TestKuralPenceresiUTCdir(t *testing.T) {
	svc, _, _ := yeniServis(t)
	company := yeniSirket(t, svc)
	limit := int64(1)
	calisanEkle(t, svc, company.ID, "cust_01", &limit)

	an, err := time.Parse(time.RFC3339, kuraliCoz(t, svc, "cust_01").WindowStart)
	require.NoError(t, err)
	assert.Equal(t, time.UTC, an.Location())
}
