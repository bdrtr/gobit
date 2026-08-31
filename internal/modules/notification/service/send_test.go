package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/modules/notification/models"
	"github.com/bdrtr/gobit/internal/modules/notification/service"
)

// testGirdi tipik bir sipariş onayı bildirimidir.
func testGirdi() service.NotifyInput {
	return service.NotifyInput{
		Template:  service.TemplateOrderPlaced,
		Channel:   coreprovider.ChannelEmail,
		Reference: "order_TEST",
		To:        "musteri@example.com",
		Data:      map[string]string{"order_id": "order_TEST", "total": "6100"},
	}
}

// kurulum sahte depo, kayıt ve sağlayıcıyla çalışan bir servis üretir.
func kurulum(t *testing.T) (*service.Service, *fakeStore, *fakeProvider) {
	t.Helper()

	store := newFakeStore()
	prov := newFakeProvider("test")
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(prov))

	svc, err := yeniServis(store, registry, prov.ID(), &fakeContacts{})
	require.NoError(t, err)

	return svc, store, prov
}

// TestNotifyGonderirVeGunlugeYazar başarılı yolun hem sağlayıcıya gittiğini hem
// de günlüğe "sent" yazıldığını doğrular.
//
// İkisi birlikte doğrulanır çünkü ayrı ayrı doğru olup birlikte yanlış
// olabilirler: gönderilen ama kaydedilmeyen bir bildirim, bir sonraki olayda
// İKİNCİ KEZ gönderilirdi.
func TestNotifyGonderirVeGunlugeYazar(t *testing.T) {
	svc, store, prov := kurulum(t)

	require.NoError(t, svc.Notify(context.Background(), testGirdi()))

	require.Equal(t, 1, prov.cagriSayisi())
	gonderilen := prov.sonBildirim()
	assert.Equal(t, "musteri@example.com", gonderilen.To, "adres sağlayıcıya BOZULMADAN gitmeli")
	assert.Equal(t, service.TemplateOrderPlaced, gonderilen.Template)
	assert.Equal(t, coreprovider.ChannelEmail, gonderilen.Channel)
	assert.Equal(t, "6100", gonderilen.Data["total"], "şablon verisi dize olarak taşınmalı")

	kayitlar := store.tumKayitlar()
	require.Len(t, kayitlar, 1)
	assert.Equal(t, models.DeliverySent, kayitlar[0].Status)
	assert.Empty(t, kayitlar[0].Error)
	assert.Equal(t, "order_TEST", kayitlar[0].Reference)
	assert.Equal(t, "test", kayitlar[0].ProviderID, "kayıt hangi sağlayıcının denendiğini yazmalı")
}

// TestNotifyAyniSablonVeReferansiIkinciKezGONDERMEZ idempotency'nin tek
// dayanağını doğrular.
//
// Olay veri yolu bugün yeniden teslim yapmaz, ama bir olayın elle yeniden
// yayımlanması mümkündür; o hâlde müşteri aynı sipariş için ikinci bir onay
// e-postası almamalıdır. Sağlayıcının çağrı SAYISI bunun tek kanıtıdır: kayıt
// sayısına bakmak yetmezdi, çünkü ikinci gönderim kayıt açmadan da yapılabilirdi.
func TestNotifyAyniSablonVeReferansiIkinciKezGONDERMEZ(t *testing.T) {
	svc, store, prov := kurulum(t)
	ctx := context.Background()

	require.NoError(t, svc.Notify(ctx, testGirdi()))
	require.NoError(t, svc.Notify(ctx, testGirdi()), "ikinci çağrı HATA DEĞİL, sessiz atlama olmalı")

	assert.Equal(t, 1, prov.cagriSayisi(), "sağlayıcıya YALNIZCA bir kez gidilmeli")
	assert.Len(t, store.tumKayitlar(), 1, "günlükte tek kayıt olmalı")
}

// TestNotifyFarkliReferansAyriGonderimdir benzersizliğin ÇİFT üzerinde
// olduğunu, yalnızca şablon üzerinde olmadığını doğrular.
//
// Yalnızca şablona bakan bir kural, ilk siparişten sonraki hiçbir siparişin
// onay almaması demekti.
func TestNotifyFarkliReferansAyriGonderimdir(t *testing.T) {
	svc, _, prov := kurulum(t)
	ctx := context.Background()

	ilk := testGirdi()
	ikinci := testGirdi()
	ikinci.Reference = "order_DIGER"

	require.NoError(t, svc.Notify(ctx, ilk))
	require.NoError(t, svc.Notify(ctx, ikinci))

	assert.Equal(t, 2, prov.cagriSayisi())
}

// TestNotifySaglayiciHatasindaBasarisizYazar sağlayıcı hatasının hem günlüğe
// yazıldığını hem de çağırana döndüğünü doğrular.
//
// Hata YUTULSAYDI, gitmeyen bildirim yalnızca tabloya bakan birine görünürdü;
// dönen hata olay veri yolu tarafından ERROR seviyesinde loglanır.
func TestNotifySaglayiciHatasindaBasarisizYazar(t *testing.T) {
	svc, store, prov := kurulum(t)
	prov.err = errors.Unavailable("smtp_down", "sağlayıcıya ulaşılamadı")

	err := svc.Notify(context.Background(), testGirdi())

	require.Error(t, err)
	assert.Equal(t, service.CodeSendFailed, errors.CodeOf(err))

	kayitlar := store.tumKayitlar()
	require.Len(t, kayitlar, 1)
	assert.Equal(t, models.DeliveryFailed, kayitlar[0].Status)
	assert.Contains(t, kayitlar[0].Error, "sağlayıcıya ulaşılamadı",
		"teşhis için sağlayıcının mesajı kayda yazılmalı")
}

// TestNotifyBasarisizKayittanSonraYENIDENGONDERMEZ başarısız bir denemenin
// ikinci kez tetiklenmesinin de atlandığını doğrular.
//
// "Başarısızsa tekrar dene" sezgisel olarak doğru görünür ama yanlıştır:
// çekirdek sözleşmesi hata dönmenin bildirimin GİTMEDİĞİ anlamına gelmediğini
// söyler (zaman aşımına uğrayan istek karşı tarafta işlenmiş olabilir), yani
// otomatik tekrar mükerrer e-posta üretebilir.
func TestNotifyBasarisizKayittanSonraYENIDENGONDERMEZ(t *testing.T) {
	svc, store, prov := kurulum(t)
	ctx := context.Background()

	prov.err = errors.Unavailable("smtp_down", "sağlayıcıya ulaşılamadı")
	require.Error(t, svc.Notify(ctx, testGirdi()))

	prov.err = nil
	require.NoError(t, svc.Notify(ctx, testGirdi()), "ikinci çağrı atlanmalı, hata dönmemeli")

	assert.Equal(t, 1, prov.cagriSayisi(),
		"sağlayıcıya yalnızca İLK denemede gidilmeli; başarısız kayıt yeniden gönderim açmaz")
	kayitlar := store.tumKayitlar()
	require.Len(t, kayitlar, 1)
	assert.Equal(t, models.DeliveryFailed, kayitlar[0].Status, "kayıt başarısız kalmalı")
}

// TestNotifyAdressizGonderimiAtlar adressiz bildirimin hata DEĞİL, atlanan bir
// kayıt ürettiğini doğrular.
//
// Çağıran bir olay işleyicisidir ve onun için "adres yok" KALICI bir durumdur;
// hata dönmek onu yeniden denenecek bir arızadan ayırt edilemez hâle getirirdi.
func TestNotifyAdressizGonderimiAtlar(t *testing.T) {
	svc, store, prov := kurulum(t)

	girdi := testGirdi()
	girdi.To = ""

	require.NoError(t, svc.Notify(context.Background(), girdi))

	assert.Equal(t, 0, prov.cagriSayisi(), "adres yoksa sağlayıcıya HİÇ gidilmemeli")
	kayitlar := store.tumKayitlar()
	require.Len(t, kayitlar, 1, "atlama da günlüğe yazılmalı; yoksa 'neden gitmedi' cevapsız kalır")
	assert.Equal(t, models.DeliverySkipped, kayitlar[0].Status)
	assert.NotContains(t, kayitlar[0].Error, "@", "açıklama adres taşımamalı")
}

// TestNotifyBilinmeyenSaglayiciKayitACMAZ sağlayıcının kayıttan ÖNCE
// çözüldüğünü doğrular.
//
// Sıra ters olsaydı, yanlış yapılandırılmış bir kurulum idempotency anahtarını
// tüketir ve yapılandırma düzeltildikten sonra o bildirim bir daha HİÇ
// gönderilemezdi.
func TestNotifyBilinmeyenSaglayiciKayitACMAZ(t *testing.T) {
	store := newFakeStore()
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(newFakeProvider("log")))

	svc, err := yeniServis(store, registry, "sendgrid", &fakeContacts{})
	require.NoError(t, err)

	err = svc.Notify(context.Background(), testGirdi())

	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "hata: %v", err)
	assert.Equal(t, 0, store.claimSayisi, "kayıt hiç denenmemeli")
	assert.Empty(t, store.tumKayitlar())
}

// TestNotifyGecersizGirdiyiReddeder zorunlu alanların doğrulandığını
// doğrular. Referanssız bir kayıt, idempotency anahtarının yarısı olmadan
// yazılırdı.
func TestNotifyGecersizGirdiyiReddeder(t *testing.T) {
	svc, store, _ := kurulum(t)

	tests := map[string]func(in *service.NotifyInput){
		"şablonsuz":  func(in *service.NotifyInput) { in.Template = "  " },
		"kanalsız":   func(in *service.NotifyInput) { in.Channel = "" },
		"referanssz": func(in *service.NotifyInput) { in.Reference = "" },
	}

	for ad, boz := range tests {
		t.Run(ad, func(t *testing.T) {
			girdi := testGirdi()
			boz(&girdi)

			err := svc.Notify(context.Background(), girdi)

			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "hata: %v", err)
		})
	}

	assert.Empty(t, store.tumKayitlar(), "geçersiz girdi günlüğe hiç dokunmamalı")
}

// TestNotifySonucYazilamazsaGonderimiBASARILISayar sonucun yazılamadığı
// durumda gönderim hatasının UYDURULMADIĞINI doğrular.
//
// Bu noktada sağlayıcıya çoktan gidilmiştir; hata dönmek, gönderilmiş bir
// bildirimi "başarısız" gibi göstermek olurdu. Kaydın 'pending' kalması,
// gerçek durumu ("gönderildi ama sonucu yazılamadı") anlatan tek işarettir.
func TestNotifySonucYazilamazsaGonderimiBASARILISayar(t *testing.T) {
	svc, store, prov := kurulum(t)
	store.finishErr = errors.Unavailable("db_down", "veritabanı yok")

	err := svc.Notify(context.Background(), testGirdi())

	require.NoError(t, err, "gönderim gerçekleşti; yazma hatası onu başarısız yapmaz")
	assert.Equal(t, 1, prov.cagriSayisi())

	kayitlar := store.tumKayitlar()
	require.Len(t, kayitlar, 1)
	assert.Equal(t, models.DeliveryPending, kayitlar[0].Status,
		"sonucu yazılamayan kayıt 'pending' kalmalı; arızanın izi budur")
}

// TestNotifyKayitAcilamazsaGONDERMEZ kaydı yazılamayan bir bildirimin
// sağlayıcıya HİÇ gitmediğini doğrular.
//
// Kayıtsız gönderim, mükerrer bildirimi durduran tek korumayı devre dışı
// bırakırdı: bir sonraki tetikleme aynı e-postayı ikinci kez gönderirdi.
func TestNotifyKayitAcilamazsaGONDERMEZ(t *testing.T) {
	svc, store, prov := kurulum(t)
	store.claimErr = errors.Unavailable("db_down", "veritabanı yok")

	err := svc.Notify(context.Background(), testGirdi())

	require.Error(t, err)
	assert.Equal(t, 0, prov.cagriSayisi(), "kayıt açılamadıysa sağlayıcıya gidilmemeli")
}
