package service_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// gonderiAc test için bir gönderi oluşturur.
func (k testKurulum) gonderiAc(t *testing.T, secenekID, anahtar string) models.Fulfillment {
	t.Helper()
	ful, err := k.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference:        "order_1",
		ShippingOptionID: secenekID,
		IdempotencyKey:   anahtar,
	})
	require.NoError(t, err)
	return ful
}

// hazirSecenek profil ve seçenek açıp seçeneğin kimliğini döner.
func hazirSecenek(t *testing.T, kurulum testKurulum) string {
	t.Helper()
	profilID := kurulum.profilAc(t, "varsayilan")
	return kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Standart kargo",
		ShippingProfileID: profilID,
		Amount:            2_000,
	})
}

// TestGonderiSaglayiciyaKendiKimliginiVerir sağlayıcıya geçilen Reference'ın
// GÖNDERİNİN kimliği olduğunu kanıtlar.
//
// Çekirdek sözleşmesi (internal/core/provider) Reference'ı "mutabakatta iki
// sistemi eşleştiren alan" diye tanımlar. Sipariş kimliği verilseydi, aynı
// siparişin iki gönderisi sağlayıcı tarafında ayırt edilemezdi.
func TestGonderiSaglayiciyaKendiKimliginiVerir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	secenekID := hazirSecenek(t, kurulum)

	ful := kurulum.gonderiAc(t, secenekID, "anahtar-1")

	girdi := kurulum.provider.sonCreateGirdisi()
	assert.Equal(t, ful.ID, girdi.Reference, "sağlayıcıya gönderinin kendi kimliği verilmeli")
	assert.NotEqual(t, "order_1", girdi.Reference, "sipariş kimliği verilmemeli")
	assert.Equal(t, secenekID, girdi.OptionID)
	assert.Equal(t, "anahtar-1", girdi.IdempotencyKey)

	assert.Equal(t, "dis_anahtar-1", ful.ExternalID, "sağlayıcının kimliği kayda yazılmalı")
	assert.Equal(t, models.StatusPending, ful.Status)
	assert.Equal(t, "TK-anahtar-1", ful.TrackingNumber)
	assert.Nil(t, ful.ShippedAt, "bekleyen gönderinin sevk damgası olmamalı")
}

// TestSecenegiYapilandirmasiSaglayiciyaGider seçeneğin Data alanının gönderi
// açılırken de sağlayıcıya iletildiğini kanıtlar.
//
// İletilmeseydi sağlayıcı hangi hesapla (sözleşme numarası, taşıyıcı ayarı)
// etiket basacağını bilemezdi; fiyat sorgusunda çalışan yapılandırmanın
// gönderi açılışında kaybolması sessiz bir tutarsızlık olurdu.
//
// Çakışmada İSTEĞİN verisi kazanır: yapılandırma mağazanın sabit ayarıdır,
// istek ise o gönderiye özgüdür ve daha belirgindir.
func TestSecenegiYapilandirmasiSaglayiciyaGider(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	profilID := kurulum.profilAc(t, "varsayilan")
	secenekID := kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Standart kargo",
		ShippingProfileID: profilID,
		Amount:            2_000,
		Data: map[string]any{
			"sozlesme_no": "SZ-42",
			"sube":        "merkez",
		},
	})

	_, err := kurulum.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference:        "order_1",
		ShippingOptionID: secenekID,
		IdempotencyKey:   "anahtar-1",
		Data:             map[string]any{"sube": "kadikoy", "adres": "..."},
	})
	require.NoError(t, err)

	girdi := kurulum.provider.sonCreateGirdisi()
	assert.Equal(t, "SZ-42", girdi.Data["sozlesme_no"], "seçeneğin yapılandırması iletilmeli")
	assert.Equal(t, "...", girdi.Data["adres"], "isteğin verisi iletilmeli")
	assert.Equal(t, "kadikoy", girdi.Data["sube"], "çakışmada isteğin verisi kazanmalı")

	secenek, err := kurulum.svc.GetShippingOption(context.Background(), secenekID)
	require.NoError(t, err)
	assert.Equal(t, "merkez", secenek.Data["sube"],
		"birleştirme seçeneğin yapılandırmasını DEĞİŞTİRMEMELİ")
}

// TestAyniAnahtarIkinciGonderiUretmez idempotency şartını kanıtlar.
//
// Çekirdek sözleşmesinin şartı budur: anahtarsız bir tekrar İKİNCİ BİR KARGO
// ETİKETİ demek olurdu. İkinci çağrının sağlayıcıya HİÇ gitmediği de sınanır.
func TestAyniAnahtarIkinciGonderiUretmez(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	secenekID := hazirSecenek(t, kurulum)

	ilk := kurulum.gonderiAc(t, secenekID, "anahtar-1")
	ikinci := kurulum.gonderiAc(t, secenekID, "anahtar-1")

	assert.Equal(t, ilk.ID, ikinci.ID, "aynı anahtar aynı gönderiyi dönmeli")
	assert.Equal(t, ilk.ExternalID, ikinci.ExternalID)

	_, create, _ := kurulum.provider.cagriSayilari()
	assert.Equal(t, 1, create, "ikinci çağrı sağlayıcıya GİTMEMELİ")
}

// TestAyniAnahtarFarkliGonderiIcinKullanilamaz idempotency'nin "aynı isteği
// tekrarlamak" demek olduğunu kanıtlar.
//
// Sessizce kabul edilseydi, çağıranın açtığını sandığı gönderi hiç açılmamış
// olurdu.
func TestAyniAnahtarFarkliGonderiIcinKullanilamaz(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	secenekID := hazirSecenek(t, kurulum)
	kurulum.gonderiAc(t, secenekID, "anahtar-1")

	_, err := kurulum.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference:        "order_2",
		ShippingOptionID: secenekID,
		IdempotencyKey:   "anahtar-1",
	})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "hata errors.Conflict olmalı: %v", err)
	assert.Equal(t, service.CodeIdempotencyMismatch, errors.CodeOf(err))
}

// TestAyniAnahtarFarkliSecenekIcinKullanilamaz karşılaştırmanın İKİNCİ
// yarısını sınar: referans aynı, kargo SEÇENEĞİ farklı.
//
// Regresyon: yalnızca referansı değiştiren bir test, koşuldaki seçenek
// karşılaştırmasını silen bir mutasyonu YAKALAYAMIYORDU; "aynı anahtar farklı
// seçenekle kullanılamaz" iddiasının hiçbir kanıtı yoktu. Sessizce kabul
// edilseydi, hızlı kargo isteyen çağıran standart kargoyla açılmış bir gönderi
// alır ve müşteri yanlış hizmeti öderdi.
func TestAyniAnahtarFarkliSecenekIcinKullanilamaz(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	ilkSecenek := hazirSecenek(t, kurulum)
	profilID := kurulum.profilAc(t, "hizli")
	ikinciSecenek := kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Hızlı kargo",
		ShippingProfileID: profilID,
		Amount:            9_000,
	})

	ilk := kurulum.gonderiAc(t, ilkSecenek, "anahtar-1")

	_, err := kurulum.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		// Referans AYNI; değişen tek şey kargo seçeneğidir.
		Reference:        ilk.Reference,
		ShippingOptionID: ikinciSecenek,
		IdempotencyKey:   "anahtar-1",
	})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "hata errors.Conflict olmalı: %v", err)
	assert.Equal(t, service.CodeIdempotencyMismatch, errors.CodeOf(err))
}

// TestAyniAnahtarFarkliKalemListesiIcinKullanilamaz tekrar dalının KALEM
// LİSTESİNİ de karşılaştırdığını kanıtlar.
//
// Regresyon: yalnızca referans ve seçenek karşılaştırılıyordu. Farklı bir
// kalem dökümüyle gelen ikinci istek hata dönmüyor, mevcut gönderiyi KENDİ
// kalemleriyle döndürüyordu; çağıran (örn. dökümü düzelttiğini sanan bir
// yönetim isteği) yazıldığını sanıyor, oysa hiçbir şey yazılmamış oluyordu.
func TestAyniAnahtarFarkliKalemListesiIcinKullanilamaz(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	secenekID := hazirSecenek(t, kurulum)

	ilk, err := kurulum.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference:        "order_1",
		ShippingOptionID: secenekID,
		IdempotencyKey:   "anahtar-1",
		Items: []service.FulfillmentItemInput{
			{LineItemID: "li_1", Quantity: 1},
		},
	})
	require.NoError(t, err)
	require.Len(t, ilk.Items, 1)

	_, err = kurulum.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference:        "order_1",
		ShippingOptionID: secenekID,
		IdempotencyKey:   "anahtar-1",
		Items: []service.FulfillmentItemInput{
			{LineItemID: "li_9", Quantity: 7},
		},
	})
	require.Error(t, err, "farklı kalem listesi sessizce yutulmamalı")
	assert.True(t, errors.IsConflict(err), "hata errors.Conflict olmalı: %v", err)
	assert.Equal(t, service.CodeIdempotencyMismatch, errors.CodeOf(err))

	// Kayıt DEĞİŞMEMİŞ olmalı: reddedilen istek hiçbir şey yazmaz.
	guncel, err := kurulum.svc.GetFulfillment(context.Background(), ilk.ID)
	require.NoError(t, err)
	require.Len(t, guncel.Items, 1)
	assert.Equal(t, "li_1", guncel.Items[0].LineItemID)
}

// TestAyniKalemKumesiFarkliSiradaTekrardir karşılaştırmanın KÜME
// olduğunu kanıtlar.
//
// Sıra farkı bir fark değildir: aynı kalemleri başka sırada gönderen bir
// tekrar, gerçek bir tekrardır ve Conflict dönmemelidir.
func TestAyniKalemKumesiFarkliSiradaTekrardir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	secenekID := hazirSecenek(t, kurulum)

	ilk, err := kurulum.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference:        "order_1",
		ShippingOptionID: secenekID,
		IdempotencyKey:   "anahtar-1",
		Items: []service.FulfillmentItemInput{
			{LineItemID: "li_1", Quantity: 2},
			{LineItemID: "li_2", Quantity: 3},
		},
	})
	require.NoError(t, err)

	ikinci, err := kurulum.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference:        "order_1",
		ShippingOptionID: secenekID,
		IdempotencyKey:   "anahtar-1",
		Items: []service.FulfillmentItemInput{
			{LineItemID: "li_2", Quantity: 3},
			{LineItemID: "li_1", Quantity: 2},
		},
	})
	require.NoError(t, err, "aynı kalem kümesi başka sırada da tekrardır")
	assert.Equal(t, ilk.ID, ikinci.ID)
}

// TestEszamanliIkiCreateTekGonderiUretir yarışın tek noktada çözüldüğünü
// kanıtlar.
//
// Sahte depo benzersiz anahtar kısıtını taklit eder; aynı anahtarla koşan
// goroutine'lerden yalnızca biri satır yazabilmelidir.
func TestEszamanliIkiCreateTekGonderiUretir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	secenekID := hazirSecenek(t, kurulum)

	const eszamanli = 8
	kimlikler := make([]string, eszamanli)
	hatalar := make([]error, eszamanli)

	var basla sync.WaitGroup
	var bitti sync.WaitGroup
	basla.Add(1)
	bitti.Add(eszamanli)

	for i := range eszamanli {
		go func() {
			defer bitti.Done()
			basla.Wait()
			ful, err := kurulum.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
				Reference:        "order_1",
				ShippingOptionID: secenekID,
				IdempotencyKey:   "anahtar-yaris",
			})
			kimlikler[i], hatalar[i] = ful.ID, err
		}()
	}
	basla.Done()
	bitti.Wait()

	for i, err := range hatalar {
		require.NoErrorf(t, err, "%d. çağrı hata döndü", i)
	}
	for i := 1; i < eszamanli; i++ {
		assert.Equal(t, kimlikler[0], kimlikler[i], "tüm çağrılar aynı gönderiyi dönmeli")
	}

	_, create, _ := kurulum.provider.cagriSayilari()
	assert.Equal(t, 1, create, "sağlayıcıya TAM OLARAK bir kez gidilmeli")
}

// TestGonderiKalemleriYazilir kalemlerin gönderiyle birlikte kaydedildiğini
// kanıtlar.
func TestGonderiKalemleriYazilir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	secenekID := hazirSecenek(t, kurulum)

	ful, err := kurulum.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference:        "order_1",
		ShippingOptionID: secenekID,
		IdempotencyKey:   "anahtar-1",
		Items: []service.FulfillmentItemInput{
			{LineItemID: "line_1", Quantity: 2},
			{LineItemID: "line_2", Quantity: 1},
		},
	})
	require.NoError(t, err)
	require.Len(t, ful.Items, 2)

	okunan, err := kurulum.svc.GetFulfillment(context.Background(), ful.ID)
	require.NoError(t, err)
	require.Len(t, okunan.Items, 2, "kalemler okuma yolunda da dönmeli")

	toplam := int64(0)
	for _, item := range okunan.Items {
		toplam += item.Quantity
	}
	assert.Equal(t, int64(3), toplam)
}

// TestKalemYazmaHatasiGonderiyiGeriAlir işlem sınırının gerçekten atomik
// olduğunu kanıtlar.
//
// Kalem yazma patladığında gönderi satırının da geri alınması gerekir; aksi
// hâlde sağlayıcıda etiket basılmış ama kalemleri boş bir gönderi kalırdı.
func TestKalemYazmaHatasiGonderiyiGeriAlir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	secenekID := hazirSecenek(t, kurulum)
	kurulum.store.failCreateItem = errors.Internal("test_kalem_yazilamadi", "kalem yazılamadı")

	_, err := kurulum.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference:        "order_1",
		ShippingOptionID: secenekID,
		IdempotencyKey:   "anahtar-1",
		Items:            []service.FulfillmentItemInput{{LineItemID: "line_1", Quantity: 1}},
	})
	require.Error(t, err)

	list, count, err := kurulum.svc.ListFulfillments(context.Background(), service.ListFulfillmentsInput{})
	require.NoError(t, err)
	assert.Zero(t, count, "başarısız işlemden geriye gönderi kalmamalı")
	assert.Empty(t, list)
}

// TestSaglayiciHatasiGonderiBirakmaz sağlayıcı patladığında satırın geri
// alındığını kanıtlar.
//
// Kalsaydı, sağlayıcıda karşılığı olmayan bir gönderi kaydı kalır ve iptal
// akışı hiçbir zaman kapatamayacağı bir satırla uğraşırdı.
func TestSaglayiciHatasiGonderiBirakmaz(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	secenekID := hazirSecenek(t, kurulum)
	kurulum.provider.createErr = errors.Unavailable("test_saglayici_dustu", "kargo firmasına ulaşılamadı")

	_, err := kurulum.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference:        "order_1",
		ShippingOptionID: secenekID,
		IdempotencyKey:   "anahtar-1",
	})
	require.Error(t, err)

	_, count, err := kurulum.svc.ListFulfillments(context.Background(), service.ListFulfillmentsInput{})
	require.NoError(t, err)
	assert.Zero(t, count)
}

// TestSaglayiciBosKimlikDonerseSozlesmeIhlali sağlayıcının kimlik vermemesinin
// sessizce kabul edilmediğini kanıtlar.
//
// Kabul edilseydi, mutabakatta iki sistemi eşleştirecek alan boş kalırdı.
func TestSaglayiciBosKimlikDonerseSozlesmeIhlali(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	secenekID := hazirSecenek(t, kurulum)
	kurulum.provider.createErr = nil
	kurulum.provider.createStatus = coreprovider.FulfillmentPending

	bosSaglayici := &bosKimlikSaglayici{}
	kayit := service.NewProviderRegistry()
	require.NoError(t, kayit.Register(bosSaglayici))
	svc, err := service.New(service.Options{Store: kurulum.store, Providers: kayit})
	require.NoError(t, err)

	_, err = svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference:        "order_1",
		ShippingOptionID: secenekID,
		IdempotencyKey:   "anahtar-1",
	})
	require.Error(t, err)
	assert.Equal(t, service.CodeProviderContract, errors.CodeOf(err))
}

// bosKimlikSaglayici boş kimlik dönen bir sağlayıcıdır; sözleşme ihlalini
// sınamak içindir.
type bosKimlikSaglayici struct{}

// ID sahte sağlayıcının kimliğini döner.
func (p *bosKimlikSaglayici) ID() string { return "sahte" }

// Quote sıfır ücret döner; bu sağlayıcı yalnızca Create dalını sınar.
func (p *bosKimlikSaglayici) Quote(
	_ context.Context,
	in coreprovider.QuoteInput,
) (coreprovider.ShippingQuote, error) {
	return coreprovider.ShippingQuote{OptionID: in.OptionID, CurrencyCode: in.CurrencyCode}, nil
}

// Create BOŞ kimlikli bir gönderi döner.
func (p *bosKimlikSaglayici) Create(
	_ context.Context,
	_ coreprovider.CreateFulfillmentInput,
) (coreprovider.Fulfillment, error) {
	return coreprovider.Fulfillment{Status: coreprovider.FulfillmentPending}, nil
}

// Cancel hiçbir şey yapmaz.
func (p *bosKimlikSaglayici) Cancel(_ context.Context, _ string) error { return nil }

// TestIptalIdempotenttir saga telafisinin şartını kanıtlar.
//
// İkinci iptal hata VERMEMELİ, sağlayıcıya İKİNCİ KEZ gitmemeli ve gönderi
// satırına yeniden YAZMAMALIDIR.
func TestIptalIdempotenttir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	secenekID := hazirSecenek(t, kurulum)
	ful := kurulum.gonderiAc(t, secenekID, "anahtar-1")

	require.NoError(t, kurulum.svc.CancelFulfillment(context.Background(), ful.ID))
	yazmaSayisi := kurulum.store.gonderiYazmaSayisi()

	require.NoError(t, kurulum.svc.CancelFulfillment(context.Background(), ful.ID),
		"ikinci iptal hata dönmemeli")

	_, _, cancel := kurulum.provider.cagriSayilari()
	assert.Equal(t, 1, cancel, "sağlayıcıya yalnızca bir kez gidilmeli")
	assert.Equal(t, yazmaSayisi, kurulum.store.gonderiYazmaSayisi(),
		"ikinci iptal satıra yazmamalı")

	okunan, err := kurulum.svc.GetFulfillment(context.Background(), ful.ID)
	require.NoError(t, err)
	assert.Equal(t, models.StatusCanceled, okunan.Status)
	require.NotNil(t, okunan.CanceledAt, "iptal anı yazılmalı")
	assert.Equal(t, testAn, *okunan.CanceledAt)
}

// TestIptalBilinmeyenKimlikteNotFound idempotentliğin "her şeyi sessizce yut"
// demek OLMADIĞINI kanıtlar.
//
// İki kez iptal edilen gerçek bir gönderi ile hiç var olmamış bir kimlik
// farklı durumlardır; ikincisi çağıran tarafta bir hatadır.
func TestIptalBilinmeyenKimlikteNotFound(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	err := kurulum.svc.CancelFulfillment(context.Background(), "ful_YOKBOYLEBIRSEY")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "hata errors.NotFound olmalı: %v", err)
}

// TestTeslimEdilmisGonderiIptalEdilemez Faz 7'nin açıkça sorduğu kararı
// kanıtlar.
//
// Teslim geri alınamayan fiziksel bir olgudur; "iptal" fiziksel dünya hakkında
// yalan olurdu ve çaresi İADEDİR. Kural, payment'ta tahsil edilmiş bir oturumun
// iptal edilemeyip iade edilmesiyle aynıdır.
func TestTeslimEdilmisGonderiIptalEdilemez(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	secenekID := hazirSecenek(t, kurulum)
	ful := kurulum.gonderiAc(t, secenekID, "anahtar-1")

	_, err := kurulum.svc.MarkShipped(context.Background(), ful.ID, "TK-1", "")
	require.NoError(t, err)
	_, err = kurulum.svc.MarkDelivered(context.Background(), ful.ID)
	require.NoError(t, err)

	err = kurulum.svc.CancelFulfillment(context.Background(), ful.ID)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "hata errors.Conflict olmalı: %v", err)
	assert.Equal(t, service.CodeInvalidTransition, errors.CodeOf(err))

	_, _, cancel := kurulum.provider.cagriSayilari()
	assert.Zero(t, cancel, "reddedilen iptal sağlayıcıya gitmemeli")
}

// TestKargodakiGonderiIptalEdilebilir kararın diğer yarısını kanıtlar.
//
// Yoldaki paket taşıyıcı tarafından geri çağrılabilir; kapatmak operatörü
// sistemin dışında çalışmaya zorlardı.
func TestKargodakiGonderiIptalEdilebilir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	secenekID := hazirSecenek(t, kurulum)
	ful := kurulum.gonderiAc(t, secenekID, "anahtar-1")

	_, err := kurulum.svc.MarkShipped(context.Background(), ful.ID, "TK-1", "")
	require.NoError(t, err)

	require.NoError(t, kurulum.svc.CancelFulfillment(context.Background(), ful.ID))

	okunan, err := kurulum.svc.GetFulfillment(context.Background(), ful.ID)
	require.NoError(t, err)
	assert.Equal(t, models.StatusCanceled, okunan.Status)
	require.NotNil(t, okunan.ShippedAt, "sevk anı KORUNMALI; iptal geçmişi silmez")
}

// TestIptalSaglayiciyaGonderiKimligiyleGider sağlayıcıya modülün kendi
// kimliğinin DEĞİL, sağlayıcının kimliğinin verildiğini kanıtlar.
//
// Modülün kimliği verilseydi sağlayıcı hiçbir zaman doğru kaydı bulamazdı.
func TestIptalSaglayiciyaGonderiKimligiyleGider(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	secenekID := hazirSecenek(t, kurulum)
	ful := kurulum.gonderiAc(t, secenekID, "anahtar-1")

	require.NoError(t, kurulum.svc.CancelFulfillment(context.Background(), ful.ID))

	kurulum.provider.mu.Lock()
	iptalEdilen := append([]string(nil), kurulum.provider.canceledIDs...)
	kurulum.provider.mu.Unlock()

	require.Len(t, iptalEdilen, 1)
	assert.Equal(t, ful.ExternalID, iptalEdilen[0])
	assert.NotEqual(t, ful.ID, iptalEdilen[0])
}

// TestSaglayiciIptalHatasiKaydiDegistirmez telafinin yarıda kalmadığını
// kanıtlar.
//
// Sağlayıcı iptal edemezken kaydı "canceled" yazmak, saga'ya geri alındığını
// söylerken gerçekte etiketi açık bırakmak demek olurdu.
func TestSaglayiciIptalHatasiKaydiDegistirmez(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	secenekID := hazirSecenek(t, kurulum)
	ful := kurulum.gonderiAc(t, secenekID, "anahtar-1")
	kurulum.provider.cancelErr = errors.Unavailable("test_iptal_edilemedi", "kargo firmasına ulaşılamadı")

	err := kurulum.svc.CancelFulfillment(context.Background(), ful.ID)
	require.Error(t, err)

	okunan, err := kurulum.svc.GetFulfillment(context.Background(), ful.ID)
	require.NoError(t, err)
	assert.Equal(t, models.StatusPending, okunan.Status, "durum değişmemeli")
	assert.Nil(t, okunan.CanceledAt)
}

// TestDurumGecisleri durum makinesinin servis üzerindeki karşılığını sınar.
func TestDurumGecisleri(t *testing.T) {
	t.Parallel()

	t.Run("bekleyen gönderi teslim edilemez", func(t *testing.T) {
		t.Parallel()

		kurulum := yeniKurulum(t)
		ful := kurulum.gonderiAc(t, hazirSecenek(t, kurulum), "anahtar-1")

		_, err := kurulum.svc.MarkDelivered(context.Background(), ful.ID)
		require.Error(t, err)
		assert.True(t, errors.IsConflict(err), "hata errors.Conflict olmalı: %v", err)
	})

	t.Run("iptal edilmiş gönderi kargoya verilemez", func(t *testing.T) {
		t.Parallel()

		kurulum := yeniKurulum(t)
		ful := kurulum.gonderiAc(t, hazirSecenek(t, kurulum), "anahtar-1")
		require.NoError(t, kurulum.svc.CancelFulfillment(context.Background(), ful.ID))

		_, err := kurulum.svc.MarkShipped(context.Background(), ful.ID, "", "")
		require.Error(t, err)
		assert.True(t, errors.IsConflict(err), "hata errors.Conflict olmalı: %v", err)
	})

	t.Run("teslim edilmiş gönderi kargoya verilemez", func(t *testing.T) {
		t.Parallel()

		kurulum := yeniKurulum(t)
		ful := kurulum.gonderiAc(t, hazirSecenek(t, kurulum), "anahtar-1")
		_, err := kurulum.svc.MarkShipped(context.Background(), ful.ID, "TK-1", "")
		require.NoError(t, err)
		_, err = kurulum.svc.MarkDelivered(context.Background(), ful.ID)
		require.NoError(t, err)

		_, err = kurulum.svc.MarkShipped(context.Background(), ful.ID, "TK-2", "")
		require.Error(t, err)
		assert.True(t, errors.IsConflict(err), "hata errors.Conflict olmalı: %v", err)
	})
}

// TestKargoyaVermeIdempotenttir aynı takip numarasıyla ikinci çağrının hata
// vermediğini, FARKLI numarayla çakıştığını kanıtlar.
func TestKargoyaVermeIdempotenttir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	ful := kurulum.gonderiAc(t, hazirSecenek(t, kurulum), "anahtar-1")

	ilk, err := kurulum.svc.MarkShipped(context.Background(), ful.ID, "TK-1", "https://kargo/1")
	require.NoError(t, err)
	assert.Equal(t, models.StatusShipped, ilk.Status)
	require.NotNil(t, ilk.ShippedAt)
	assert.Equal(t, testAn, *ilk.ShippedAt)

	ikinci, err := kurulum.svc.MarkShipped(context.Background(), ful.ID, "TK-1", "")
	require.NoError(t, err, "aynı numarayla ikinci çağrı hata vermemeli")
	assert.Equal(t, ilk.ShippedAt, ikinci.ShippedAt, "sevk anı değişmemeli")

	bos, err := kurulum.svc.MarkShipped(context.Background(), ful.ID, "", "")
	require.NoError(t, err, "numarasız tekrar da hata vermemeli")
	assert.Equal(t, "TK-1", bos.TrackingNumber)

	_, err = kurulum.svc.MarkShipped(context.Background(), ful.ID, "TK-2", "")
	require.Error(t, err, "farklı numara yeni bir istektir")
	assert.True(t, errors.IsConflict(err), "hata errors.Conflict olmalı: %v", err)
}

// TestTeslimIdempotenttir ikinci teslim bildiriminin hata vermediğini ve
// teslim anını DEĞİŞTİRMEDİĞİNİ kanıtlar.
func TestTeslimIdempotenttir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	ful := kurulum.gonderiAc(t, hazirSecenek(t, kurulum), "anahtar-1")
	_, err := kurulum.svc.MarkShipped(context.Background(), ful.ID, "TK-1", "")
	require.NoError(t, err)

	ilk, err := kurulum.svc.MarkDelivered(context.Background(), ful.ID)
	require.NoError(t, err)
	require.NotNil(t, ilk.DeliveredAt)

	yazmaSayisi := kurulum.store.gonderiYazmaSayisi()
	ikinci, err := kurulum.svc.MarkDelivered(context.Background(), ful.ID)
	require.NoError(t, err)
	assert.Equal(t, ilk.DeliveredAt, ikinci.DeliveredAt)
	assert.Equal(t, yazmaSayisi, kurulum.store.gonderiYazmaSayisi(),
		"idempotent dal satıra yazmamalı")
}

// TestDurumDegistirenAkislarKilitAlir eşzamanlılık sözleşmesini kanıtlar.
//
// Kilit alınmasaydı, aynı gönderiyi aynı anda iptal eden iki çağrı sağlayıcıya
// İKİ KEZ giderdi. Sahte depo kilidi yalnızca işlem içinde verir; kilit
// alınmadığında test derlenir ama iddia düşer.
func TestDurumDegistirenAkislarKilitAlir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	ful := kurulum.gonderiAc(t, hazirSecenek(t, kurulum), "anahtar-1")
	// Kurulum da kilit alır (seçenek oluşturma profili paylaşımlı kilitler);
	// sınanan şey aşağıdaki iki akışın kilitleridir.
	kurulum.store.kilitleriSifirla()

	_, err := kurulum.svc.MarkShipped(context.Background(), ful.ID, "TK-1", "")
	require.NoError(t, err)
	_, err = kurulum.svc.MarkDelivered(context.Background(), ful.ID)
	require.NoError(t, err)

	assert.Equal(t, []string{"fulfillment", "fulfillment"}, kurulum.store.kilitSirasi(),
		"durum değiştiren her akış gönderi satırını kilitlemeli")
}

// TestGonderiGirdisiDogrulanir geçersiz girdinin errors.Invalid ile
// reddedildiğini kanıtlar.
func TestGonderiGirdisiDogrulanir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	secenekID := hazirSecenek(t, kurulum)

	durumlar := []struct {
		ad    string
		girdi service.CreateFulfillmentInput
	}{
		{"referans yok", service.CreateFulfillmentInput{
			ShippingOptionID: secenekID, IdempotencyKey: "a",
		}},
		{"seçenek yok", service.CreateFulfillmentInput{
			Reference: "order_1", IdempotencyKey: "a",
		}},
		{"seçenek öneki yanlış", service.CreateFulfillmentInput{
			Reference: "order_1", ShippingOptionID: "sprof_XYZ", IdempotencyKey: "a",
		}},
		{"anahtar yok", service.CreateFulfillmentInput{
			Reference: "order_1", ShippingOptionID: secenekID,
		}},
		{"kalem adedi sıfır", service.CreateFulfillmentInput{
			Reference: "order_1", ShippingOptionID: secenekID, IdempotencyKey: "a",
			Items: []service.FulfillmentItemInput{{LineItemID: "line_1", Quantity: 0}},
		}},
		{"aynı satır iki kez", service.CreateFulfillmentInput{
			Reference: "order_1", ShippingOptionID: secenekID, IdempotencyKey: "a",
			Items: []service.FulfillmentItemInput{
				{LineItemID: "line_1", Quantity: 1},
				{LineItemID: "line_1", Quantity: 2},
			},
		}},
	}

	for _, durum := range durumlar {
		t.Run(durum.ad, func(t *testing.T) {
			t.Parallel()

			_, err := kurulum.svc.CreateFulfillment(context.Background(), durum.girdi)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "hata errors.Invalid olmalı: %v", err)
		})
	}
}

// TestGonderiListelemesiKalemleriIliistirir liste yolunun kalemleri toplu
// getirdiğini kanıtlar.
func TestGonderiListelemesiKalemleriIliistirir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	secenekID := hazirSecenek(t, kurulum)

	for i, anahtar := range []string{"anahtar-1", "anahtar-2"} {
		_, err := kurulum.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
			Reference:        "order_1",
			ShippingOptionID: secenekID,
			IdempotencyKey:   anahtar,
			Items: []service.FulfillmentItemInput{
				{LineItemID: "line_" + anahtar, Quantity: int64(i + 1)},
			},
		})
		require.NoError(t, err)
	}

	list, count, err := kurulum.svc.ListFulfillments(context.Background(), service.ListFulfillmentsInput{})
	require.NoError(t, err)
	require.EqualValues(t, 2, count)
	require.Len(t, list, 2)
	for _, ful := range list {
		assert.Len(t, ful.Items, 1, "her gönderinin kalemi iliştirilmeli")
	}
}
