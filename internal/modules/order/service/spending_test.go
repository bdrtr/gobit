package service_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// pencereBasi harcama penceresinin testlerdeki başlangıcıdır.
//
// Sabit bir an seçilir: pencerenin NEREDE başladığı b2b modülünün kararıdır ve
// burada sınanan şey, verilen pencerenin UYGULANIP uygulanmadığıdır.
var pencereBasi = time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)

// limitKurali verilen limitle bir harcama kuralı gövdesi üretir.
func limitKurali(limit int64, currency string, window *time.Time) json.RawMessage {
	kural := map[string]any{
		"limited":        true,
		"spending_limit": limit,
		"currency_code":  currency,
		"window_start":   "",
	}
	if window != nil {
		kural["window_start"] = window.Format(time.RFC3339)
	}
	payload, err := json.Marshal(kural)
	if err != nil {
		panic(err)
	}
	return payload
}

// limitliOrtam harcama kuralı bağlı bir servis kurar.
func limitliOrtam(t *testing.T, payload json.RawMessage) (ortam, *fakeSpendingPolicy) {
	t.Helper()

	store := newFakeStore()
	bus := newFakeBus()
	policy := &fakeSpendingPolicy{payload: payload}

	svc, err := service.New(service.Options{
		Repo: store, Events: bus, Spending: policy,
	})
	require.NoError(t, err)

	return ortam{svc: svc, store: store, bus: bus}, policy
}

// gecmisSiparis müşterinin geçmiş harcamasını depoya yazar.
func gecmisSiparis(t *testing.T, o ortam, id string, total int64, placedAt time.Time) {
	t.Helper()

	o.store.seedOrder(models.Order{
		ID:           id,
		Status:       models.OrderPending,
		RegionID:     testRegionID,
		CustomerID:   testCustomerID,
		CurrencyCode: testCurrency,
		Subtotal:     total,
		Total:        total,
		PlacedAt:     placedAt,
	})
}

// TestHarcamaLimitiAsilinsaSiparisAcilmaz kuralın GERÇEKTEN uygulandığını
// sabitler.
//
// Sipariş 6100 tutarındadır; dönem içinde 5000 harcanmış ve limit 10000'dir.
// 5000 + 6100 > 10000 olduğu için sipariş açılmamalı ve depoda İZ kalmamalıdır:
// bu akışın çağıranı (complete_cart saga'sı) siparişi ödemeden ÖNCE açar, yani
// buradaki ret ödemenin hiç denenmemesi demektir.
func TestHarcamaLimitiAsilinsaSiparisAcilmaz(t *testing.T) {
	o, policy := limitliOrtam(t, limitKurali(10_000, testCurrency, &pencereBasi))
	gecmisSiparis(t, o, "order_GECMIS", 5000, pencereBasi.Add(time.Hour))

	_, err := o.svc.CreateOrder(context.Background(), gecerliGirdi())

	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "limit aşımı bir çakışmadır, sunucu arızası değil")
	assert.Equal(t, service.CodeSpendingLimitExceeded, errors.CodeOf(err))
	assert.Equal(t, []string{testCustomerID}, policy.calls())

	// Yazma GERİ ALINDI: geriye yalnızca geçmiş sipariş kalır.
	kayitlar, _, listErr := o.svc.ListOrders(context.Background(), service.ListOrdersInput{})
	require.NoError(t, listErr)
	require.Len(t, kayitlar, 1)
	assert.Equal(t, "order_GECMIS", kayitlar[0].ID)

	// Olay da yayımlanmadı: açılmamış bir siparişin duyurusu olmaz.
	assert.Empty(t, o.bus.events())
}

// TestHarcamaLimitiAltindaSiparisGecer sınırın ALTINDA kalan bir siparişin
// engellenmediğini sabitler.
//
// Toplam tam olarak limite EŞİTTİR (3900 + 6100 = 10000): limit harcanabilecek
// TAVANDIR, aşılmadığı sürece geçer. Eşitliği ayrıca sınamak, "büyüktür" ile
// "büyük eşittir" arasındaki tek karakterlik kaymayı yakalayan tek testtir.
func TestHarcamaLimitiAltindaSiparisGecer(t *testing.T) {
	o, _ := limitliOrtam(t, limitKurali(10_000, testCurrency, &pencereBasi))
	gecmisSiparis(t, o, "order_GECMIS", 3900, pencereBasi.Add(time.Hour))

	created, err := o.svc.CreateOrder(context.Background(), gecerliGirdi())

	require.NoError(t, err)
	assert.Equal(t, int64(6100), created.Total)
	assert.Equal(t, 1, o.store.spendingLockCount(), "kural uygulanırken kilit alınmalı")
	assert.Equal(t, 1, o.store.spendingSumCount())
}

// TestHarcamaLimitiYoksaHicOkunmaz sınırsız çalışanda ek maliyet olmadığını
// sabitler.
//
// "limited": false gövdesi, hem b2b'de limiti nil olan çalışanın hem de hiçbir
// şirkete bağlı olmayan müşterinin cevabıdır. İkisinde de ne kilit alınır ne de
// toplam okunur; aksi hâlde B2C bir kurulum her siparişte iki gereksiz sorgu
// öderdi.
func TestHarcamaLimitiYoksaHicOkunmaz(t *testing.T) {
	o, policy := limitliOrtam(t, json.RawMessage(`{"limited":false}`))

	_, err := o.svc.CreateOrder(context.Background(), gecerliGirdi())

	require.NoError(t, err)
	assert.Equal(t, []string{testCustomerID}, policy.calls(), "kural yine de SORULUR")
	assert.Equal(t, 0, o.store.spendingLockCount())
	assert.Equal(t, 0, o.store.spendingSumCount())
}

// TestHarcamaKuraliKuruluDegilseDavranisDegismez b2b modülü olmayan kurulumu
// sabitler.
//
// [service.Options.Spending] nil bırakıldığında sipariş açma yolu, alan hiç
// eklenmemiş gibi davranmalıdır: kural SORULMAZ bile. Saf B2C kurulumun
// bugünkü davranışını koruyan tek test budur.
func TestHarcamaKuraliKuruluDegilseDavranisDegismez(t *testing.T) {
	o := yeniOrtam(t)
	// Limitin uygulansa REDDEDECEĞİ kadar geçmiş harcama: kural kurulu
	// olmadığında bu satırların hiçbir etkisi olmamalıdır.
	gecmisSiparis(t, o, "order_GECMIS", 1_000_000, pencereBasi.Add(time.Hour))

	created, err := o.svc.CreateOrder(context.Background(), gecerliGirdi())

	require.NoError(t, err)
	assert.Equal(t, int64(6100), created.Total)
	assert.Equal(t, 0, o.store.spendingLockCount())
	assert.Equal(t, 0, o.store.spendingSumCount())
}

// TestPencereDisindakiHarcamaSayilmaz dönemin GERÇEKTEN uygulandığını
// sabitler.
//
// Pencereden bir saniye ÖNCE verilmiş 50000'lik bir sipariş, limiti 10000 olan
// çalışanı engellememelidir: harcama limiti dönem başına sıfırlanır ve geçmiş
// dönemin harcaması bu dönemin bütçesini yakmaz.
func TestPencereDisindakiHarcamaSayilmaz(t *testing.T) {
	o, _ := limitliOrtam(t, limitKurali(10_000, testCurrency, &pencereBasi))
	gecmisSiparis(t, o, "order_ONCEKI_DONEM", 50_000, pencereBasi.Add(-time.Second))

	created, err := o.svc.CreateOrder(context.Background(), gecerliGirdi())

	require.NoError(t, err)
	assert.Equal(t, int64(6100), created.Total)
}

// TestPencereBasindakiSiparisSayilir pencerenin alt ucunun DÂHİL olduğunu
// sabitler.
//
// Tam pencerenin başlangıcında verilmiş bir sipariş İÇERİDEDİR. Bir saniyelik
// bu fark, ayın ilk anına denk gelen siparişin hangi döneme yazılacağını
// belirler ve iki uçtan birini seçmek zorunludur; seçilen uç alt uçtur.
func TestPencereBasindakiSiparisSayilir(t *testing.T) {
	o, _ := limitliOrtam(t, limitKurali(10_000, testCurrency, &pencereBasi))
	gecmisSiparis(t, o, "order_TAM_SINIR", 5000, pencereBasi)

	_, err := o.svc.CreateOrder(context.Background(), gecerliGirdi())

	require.Error(t, err)
	assert.Equal(t, service.CodeSpendingLimitExceeded, errors.CodeOf(err))
}

// TestPencereYoksaTumGecmisSayilir "never" periyodunun karşılığını sabitler.
//
// window_start boş geldiğinde pencere yoktur ve çalışanın TÜM geçmişi toplanır;
// yıllar önceki bir sipariş bile limiti doldurur. Boş alanı "pencere şimdi
// başlıyor" saymak, hiç sıfırlanmaması istenen bir limiti her çağrıda
// sıfırlardı.
func TestPencereYoksaTumGecmisSayilir(t *testing.T) {
	o, _ := limitliOrtam(t, limitKurali(10_000, testCurrency, nil))
	gecmisSiparis(t, o, "order_COK_ESKI", 5000, time.Date(2019, time.March, 3, 0, 0, 0, 0, time.UTC))

	_, err := o.svc.CreateOrder(context.Background(), gecerliGirdi())

	require.Error(t, err)
	assert.Equal(t, service.CodeSpendingLimitExceeded, errors.CodeOf(err))
}

// TestIptalEdilmisSiparisHarcamadanDusulur iptalin bütçeyi geri verdiğini
// sabitler.
//
// İptal "bu alışveriş olmadı" demektir; iptal edilmiş bir siparişin bütçeyi
// dönem sonuna kadar tutması, satılmamış bir malı harcama saymak olurdu.
func TestIptalEdilmisSiparisHarcamadanDusulur(t *testing.T) {
	o, _ := limitliOrtam(t, limitKurali(10_000, testCurrency, &pencereBasi))
	o.store.seedOrder(models.Order{
		ID:           "order_IPTAL",
		Status:       models.OrderCanceled,
		RegionID:     testRegionID,
		CustomerID:   testCustomerID,
		CurrencyCode: testCurrency,
		Subtotal:     9000,
		Total:        9000,
		PlacedAt:     pencereBasi.Add(time.Hour),
	})

	created, err := o.svc.CreateOrder(context.Background(), gecerliGirdi())

	require.NoError(t, err)
	assert.Equal(t, int64(6100), created.Total)
}

// TestIadeEdilenTutarHarcamadanDusulur iadenin bütçeyi geri verdiğini
// sabitler.
//
// 9000'lik siparişin 8000'i iade edilmişse harcama 1000'dir; 1000 + 6100
// limitin altındadır ve sipariş geçmelidir. Düşülmeseydi, tamamı iade edilmiş
// bir sipariş çalışanın bütçesini dönem sonuna kadar kilitlerdi.
func TestIadeEdilenTutarHarcamadanDusulur(t *testing.T) {
	o, _ := limitliOrtam(t, limitKurali(10_000, testCurrency, &pencereBasi))
	gecmisSiparis(t, o, "order_IADELI", 9000, pencereBasi.Add(time.Hour))
	o.store.seedRefund("order_IADELI", 8000)

	created, err := o.svc.CreateOrder(context.Background(), gecerliGirdi())

	require.NoError(t, err)
	assert.Equal(t, int64(6100), created.Total)
}

// TestKuralinParaBirimiTeklestirilir küçük harf gelen bir kodun limiti
// uygulanamaz kılmadığını sabitler.
//
// Sipariş tarafında para birimi zaten büyük harfe indirilir; kural tarafı
// tekleştirilmeseydi "try" ile "TRY" farklı para birimi sanılır ve her sipariş
// para birimi uyuşmazlığıyla reddedilirdi — yani limit, uygulanmak yerine
// alışverişi tümüyle durdururdu.
func TestKuralinParaBirimiTeklestirilir(t *testing.T) {
	o, _ := limitliOrtam(t, limitKurali(10_000, "try", &pencereBasi))

	created, err := o.svc.CreateOrder(context.Background(), gecerliGirdi())

	require.NoError(t, err)
	assert.Equal(t, int64(6100), created.Total)
}

// TestFarkliParaBirimiSiparisiReddedilir çevrim yapılmadığını ve kuralın
// ATLANMADIĞINI sabitler.
//
// Şirketin limiti TRY, sipariş USD ise iki tutar karşılaştırılamaz. Sessizce
// geçirmek, limiti dolmuş bir çalışana başka para birimli bir bölgeden
// sınırsız alışveriş kapısı açardı; çevirmek ise olmayan bir kura dayanırdı.
func TestFarkliParaBirimiSiparisiReddedilir(t *testing.T) {
	o, _ := limitliOrtam(t, limitKurali(10_000, "USD", &pencereBasi))

	_, err := o.svc.CreateOrder(context.Background(), gecerliGirdi())

	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
	assert.Equal(t, service.CodeSpendingCurrencyMismatch, errors.CodeOf(err))
	// Ret, hiçbir yan etki bırakmadan gelmeli: müşteri kilidi bile alınmaz.
	assert.Equal(t, 0, o.store.spendingLockCount())
}

// TestKuralOkunamazsaSiparisAcilmaz "kural yok" ile "kuralı öğrenemedik"
// ayrımını sabitler.
//
// Sağlayıcı hata döndüğünde limitin ne olduğu BİLİNMEZ. Siparişi geçirmek,
// limiti sağlayıcının her arızasında sessizce kaldırmak olurdu.
func TestKuralOkunamazsaSiparisAcilmaz(t *testing.T) {
	o, policy := limitliOrtam(t, nil)
	policy.err = errors.Internal("b2b_down", "b2b servisi yanıt vermiyor")

	_, err := o.svc.CreateOrder(context.Background(), gecerliGirdi())

	require.Error(t, err)
	assert.Equal(t, service.CodeSpendingPolicyUnavailable, errors.CodeOf(err))
	assert.Empty(t, o.bus.events())
}

// TestBozukKuralGovdesiReddedilir sözleşmeyi çiğneyen bir gövdenin sessizce
// "limitsiz"e düşmediğini sabitler.
//
// Çözülemeyen ya da anlamsız bir gövde errors.Internal'dır: çağıranın
// düzeltebileceği bir şey yoktur, sağlayıcı sözleşmeyi çiğnemiştir. "limited"
// alanını okuyamayan bir çözümleyicinin varsayılan false'a düşmesi, bozuk bir
// kurulumda limiti tamamen kaldırırdı.
func TestBozukKuralGovdesiReddedilir(t *testing.T) {
	for ad, govde := range map[string]json.RawMessage{
		"cozulemeyen":     json.RawMessage(`{"limited":`),
		"negatif limit":   json.RawMessage(`{"limited":true,"spending_limit":-1,"currency_code":"TRY"}`),
		"parasiz limit":   json.RawMessage(`{"limited":true,"spending_limit":100,"currency_code":""}`),
		"bozuk pencere":   json.RawMessage(`{"limited":true,"spending_limit":100,"currency_code":"TRY","window_start":"dun"}`),
		"gecersiz birim":  json.RawMessage(`{"limited":true,"spending_limit":100,"currency_code":"TRYY"}`),
		"eksik para kodu": json.RawMessage(`{"limited":true,"spending_limit":100}`),
	} {
		t.Run(ad, func(t *testing.T) {
			o, _ := limitliOrtam(t, govde)

			_, err := o.svc.CreateOrder(context.Background(), gecerliGirdi())

			require.Error(t, err)
			assert.Equal(t, service.CodeSpendingPolicyInvalid, errors.CodeOf(err),
				"bozuk kural sessizce limitsize düşmemeli")
		})
	}
}

// TestBosKuralGovdesiReddedilir hiç gövde döndürmeyen sağlayıcıyı sabitler.
//
// Boş gövde "limit yok" DEĞİLDİR: sağlayıcı bir cevap vermemiştir ve
// verilmemiş bir cevabı sınırsıza saymak, kuralı en sessiz biçimde kaldırırdı.
func TestBosKuralGovdesiReddedilir(t *testing.T) {
	o, policy := limitliOrtam(t, nil)
	policy.empty = true

	_, err := o.svc.CreateOrder(context.Background(), gecerliGirdi())

	require.Error(t, err)
	assert.Equal(t, service.CodeSpendingPolicyInvalid, errors.CodeOf(err))
}

// TestMisafirSiparisindeKuralSorulmaz müşterisiz siparişte sağlayıcının hiç
// çağrılmadığını sabitler.
//
// Harcama limiti ÇALIŞANA bağlıdır ve çalışanın kimliği bir müşteri kaydıdır;
// müşterisi olmayan bir siparişte sorulacak bir kural yoktur. Boş kimlikle
// sormak, sağlayıcıyı her misafir siparişinde boşuna meşgul ederdi.
func TestMisafirSiparisindeKuralSorulmaz(t *testing.T) {
	o, policy := limitliOrtam(t, limitKurali(1, testCurrency, &pencereBasi))
	girdi := gecerliGirdi()
	girdi.CustomerID = ""

	_, err := o.svc.CreateOrder(context.Background(), girdi)

	require.NoError(t, err)
	assert.Empty(t, policy.calls())
}

// TestIdempotentTekrarHarcamayiIkinciKezSaymaz aynı anahtarla gelen ikinci
// çağrının bütçeyi iki kez yakmadığını sabitler.
//
// Saga bir adımı yeniden deneyebilir. Tekrar çağrı ucuz yolda mevcut siparişi
// bulur ve kurala hiç girmez; girseydi, ilk çağrının yazdığı sipariş kendi
// tekrarını limite takılan bir istek hâline getirirdi.
func TestIdempotentTekrarHarcamayiIkinciKezSaymaz(t *testing.T) {
	o, policy := limitliOrtam(t, limitKurali(10_000, testCurrency, &pencereBasi))
	girdi := gecerliGirdi()
	girdi.IdempotencyKey = "wf_TEKRAR"

	ilk, err := o.svc.CreateOrder(context.Background(), girdi)
	require.NoError(t, err)

	ikinci, err := o.svc.CreateOrder(context.Background(), girdi)
	require.NoError(t, err)

	assert.Equal(t, ilk.ID, ikinci.ID)
	assert.Len(t, policy.calls(), 1, "tekrar çağrı kurala hiç girmez")
	assert.Equal(t, 1, o.store.spendingSumCount())
}

// TestHarcamaKilidiSiparisIsleminde alınan kilidin YAZMA işleminin içinde
// olduğunu sabitler.
//
// Sahte depo, kilidi işlem dışında almaya kalkan bir servisi hata ile
// reddeder; testin geçmesi kilidin işlem içinde alındığının kanıtıdır. Kilit
// işlemin dışında alınsaydı hemen serbest kalır ve "önce oku sonra yaz"
// yarışını hiç kapatmazdı.
func TestHarcamaKilidiSiparisIsleminde(t *testing.T) {
	o, _ := limitliOrtam(t, limitKurali(1_000_000, testCurrency, &pencereBasi))

	_, err := o.svc.CreateOrder(context.Background(), gecerliGirdi())

	require.NoError(t, err)
	assert.Equal(t, 1, o.store.spendingLockCount())
}

// TestSifirLimitliCalisanHicHarcayamaz 0 ile nil ayrımının uçtan uca
// korunduğunu sabitler.
//
// b2b tarafında nil "sınırsız", 0 ise gerçek bir sıfır limittir. İkisi burada
// da ayrı davranmalıdır: sıfır limitli çalışanın hiçbir siparişi geçmez.
func TestSifirLimitliCalisanHicHarcayamaz(t *testing.T) {
	o, _ := limitliOrtam(t, limitKurali(0, testCurrency, &pencereBasi))

	_, err := o.svc.CreateOrder(context.Background(), gecerliGirdi())

	require.Error(t, err)
	assert.Equal(t, service.CodeSpendingLimitExceeded, errors.CodeOf(err))
	assert.Contains(t, fmt.Sprint(err), "limit 0")
}
