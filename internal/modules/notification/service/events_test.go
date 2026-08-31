package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/eventbus"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/modules/notification/models"
	"github.com/bdrtr/gobit/internal/modules/notification/service"
)

// testSiparisGovdesi "order.interop" yüzeyinin döndüğü gövdedir.
//
// TÜM değerler dizedir; yüzeyin sözleşmesi budur ve gövdeyi burada elle yazmak
// bilinçlidir (bkz. [fakeContacts]).
const testSiparisGovdesi = `{
	"order_id":      "order_01H",
	"display_id":    "1042",
	"email":         "musteri@example.com",
	"currency_code": "TRY",
	"total":         "6100",
	"item_count":    "2"
}`

// olaySiparisi "order.placed" olayının yükünü üretir.
//
// Yükte E-POSTA YOKTUR ve bu testin dayanağıdır: abone adresi olaydan değil
// kayıttan okumak ZORUNDADIR.
func olaySiparisi(orderID string) eventbus.Event {
	return eventbus.Event{
		Name: service.EventOrderPlaced,
		Data: map[string]any{
			"order_id":      orderID,
			"display_id":    "1042",
			"status":        "pending",
			"currency_code": "TRY",
			"total":         "6100",
			"item_count":    "2",
		},
	}
}

// olayKurulumu sahte iletişim yüzeyiyle bir servis üretir.
func olayKurulumu(t *testing.T, contacts *fakeContacts) (*service.Service, *fakeStore, *fakeProvider) {
	t.Helper()

	store := newFakeStore()
	prov := newFakeProvider("test")
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(prov))

	svc, err := yeniServis(store, registry, prov.ID(), contacts)
	require.NoError(t, err)

	return svc, store, prov
}

// TestOrderPlacedEpostayiOLAYDANDEGILKAYITTANOkur abonenin adresi siparişten
// okuduğunu doğrular.
//
// Olay yükü kişisel veri TAŞIMAZ (olaylar Redis'e yazılır ve orada kalıcıdır);
// abone bu yüzden olaydan yalnızca sipariş kimliğini alır ve gerisini
// "order.interop" üzerinden okur. Test bunu iki yönden sabitler: okuma
// GERÇEKTEN yapılır ve sağlayıcıya giden adres o okumadan gelir.
func TestOrderPlacedEpostayiOLAYDANDEGILKAYITTANOkur(t *testing.T) {
	contacts := &fakeContacts{govde: testSiparisGovdesi}
	svc, store, prov := olayKurulumu(t, contacts)

	require.NoError(t, svc.OrderPlaced(context.Background(), olaySiparisi("order_01H")))

	require.Equal(t, 1, contacts.cagri, "sipariş kaydı okunmalı")
	assert.Equal(t, "order_01H", contacts.istenen, "okuma, olaydaki kimlikle yapılmalı")

	require.Equal(t, 1, prov.cagriSayisi())
	gonderilen := prov.sonBildirim()
	assert.Equal(t, "musteri@example.com", gonderilen.To, "adres KAYITTAN gelmeli")
	assert.Equal(t, coreprovider.ChannelEmail, gonderilen.Channel)
	assert.Equal(t, service.TemplateOrderPlaced, gonderilen.Template)
	assert.Equal(t, map[string]string{
		"order_id":      "order_01H",
		"display_id":    "1042",
		"currency_code": "TRY",
		"total":         "6100",
		"item_count":    "2",
	}, gonderilen.Data, "şablon verisi siparişten gelmeli ve TÜM değerleri dize olmalı")

	kayitlar := store.tumKayitlar()
	require.Len(t, kayitlar, 1)
	assert.Equal(t, "order_01H", kayitlar[0].Reference)
	assert.Equal(t, service.TemplateOrderPlaced, kayitlar[0].Template)
}

// TestOrderPlacedSablonAdiOlayAdiylaAynidir şablon ile olay adının
// ayrışmadığını doğrular.
//
// İkisi ayrışırsa "hangi olay hangi şablonu tetikliyor" sorusu ancak koda
// bakılarak yanıtlanır; üstelik şablon adı idempotency anahtarının yarısıdır ve
// değişmesi, TÜM siparişler için bildirimin ikinci kez gönderilebilir olması
// demektir.
func TestOrderPlacedSablonAdiOlayAdiylaAynidir(t *testing.T) {
	assert.Equal(t, service.EventOrderPlaced, service.TemplateOrderPlaced)
	assert.Equal(t, "order.placed", service.EventOrderPlaced,
		"olay adı order modülünün yayımladığı adla aynı olmalı")
}

// TestOrderPlacedAyniOlayIkiKezIslenirseTekBildirimGonderilir olay veri
// yolunun sıra ve tekillik garantisi VERMEDİĞİ gerçeğine karşı korumayı
// doğrular.
//
// İşleyici idempotent olmak zorundadır ve tekillik koda değil, teslim
// günlüğündeki (şablon, referans) benzersizliğine dayanır.
func TestOrderPlacedAyniOlayIkiKezIslenirseTekBildirimGonderilir(t *testing.T) {
	contacts := &fakeContacts{govde: testSiparisGovdesi}
	svc, store, prov := olayKurulumu(t, contacts)
	ctx := context.Background()
	olay := olaySiparisi("order_01H")

	require.NoError(t, svc.OrderPlaced(ctx, olay))
	require.NoError(t, svc.OrderPlaced(ctx, olay))

	assert.Equal(t, 1, prov.cagriSayisi(), "müşteri ikinci bir onay e-postası ALMAMALI")
	assert.Len(t, store.tumKayitlar(), 1)
}

// TestOrderPlacedEpostasizSiparisiAtlar adressiz siparişin hata değil,
// atlanmış bir kayıt ürettiğini doğrular.
func TestOrderPlacedEpostasizSiparisiAtlar(t *testing.T) {
	contacts := &fakeContacts{govde: `{"order_id":"order_01H","display_id":"7","email":"",` +
		`"currency_code":"TRY","total":"100","item_count":"1"}`}
	svc, store, prov := olayKurulumu(t, contacts)

	require.NoError(t, svc.OrderPlaced(context.Background(), olaySiparisi("order_01H")))

	assert.Equal(t, 0, prov.cagriSayisi())
	kayitlar := store.tumKayitlar()
	require.Len(t, kayitlar, 1)
	assert.Equal(t, models.DeliverySkipped, kayitlar[0].Status)
}

// TestOrderPlacedBozukOlayYukunuReddeder olay sözleşmesinin ihlalinin sessiz
// kalmadığını doğrular.
//
// Sayısal bir order_id, Redis backend'inde float64 olarak gelirdi; sessizce
// devam etmek, hiç var olmayan bir sipariş için bildirim denemesi üretirdi.
func TestOrderPlacedBozukOlayYukunuReddeder(t *testing.T) {
	tests := map[string]map[string]any{
		"alan yok":     {"display_id": "1042"},
		"dize değil":   {"order_id": 42},
		"boş kimlik":   {"order_id": "   "},
		"nil değerli":  {"order_id": nil},
		"yanlış tipli": {"order_id": []string{"order_01H"}},
	}

	for ad, veri := range tests {
		t.Run(ad, func(t *testing.T) {
			contacts := &fakeContacts{govde: testSiparisGovdesi}
			svc, store, prov := olayKurulumu(t, contacts)

			err := svc.OrderPlaced(context.Background(),
				eventbus.Event{Name: service.EventOrderPlaced, Data: veri})

			require.Error(t, err)
			assert.Equal(t, service.CodeEventInvalid, errors.CodeOf(err))
			assert.Equal(t, 0, contacts.cagri, "bozuk yükle sipariş okunmamalı")
			assert.Equal(t, 0, prov.cagriSayisi())
			assert.Empty(t, store.tumKayitlar())
		})
	}
}

// TestOrderPlacedSiparisOkunamazsaHataDoner okuma hatasının yutulmadığını
// doğrular.
//
// Hata dönmek yeniden teslim İSTEMEK değildir (bkz. events.go dosya belgesi);
// veri yolu onu ERROR seviyesinde loglar ve bildirimin gitmediği görünür olur.
func TestOrderPlacedSiparisOkunamazsaHataDoner(t *testing.T) {
	contacts := &fakeContacts{err: errors.NotFound("order_not_found", "sipariş yok")}
	svc, store, prov := olayKurulumu(t, contacts)

	err := svc.OrderPlaced(context.Background(), olaySiparisi("order_YOK"))

	require.Error(t, err)
	assert.Equal(t, service.CodeContactUnavailable, errors.CodeOf(err))
	assert.True(t, errors.IsNotFound(err), "hata SINIFI korunmalı: %v", err)
	assert.Equal(t, 0, prov.cagriSayisi())
	assert.Empty(t, store.tumKayitlar(), "okunamayan sipariş idempotency anahtarını tüketmemeli")
}

// TestOrderPlacedBozukYanitiReddeder yüzeyin şeması değiştiğinde sessizce boş
// bir şablon gönderilmediğini doğrular.
func TestOrderPlacedBozukYanitiReddeder(t *testing.T) {
	tests := map[string]string{
		"JSON değil":      `{bozuk`,
		"kimliksiz gövde": `{"email":"a@b.com"}`,
	}

	for ad, govde := range tests {
		t.Run(ad, func(t *testing.T) {
			contacts := &fakeContacts{govde: govde}
			svc, _, prov := olayKurulumu(t, contacts)

			err := svc.OrderPlaced(context.Background(), olaySiparisi("order_01H"))

			require.Error(t, err)
			assert.Equal(t, service.CodeContactInvalid, errors.CodeOf(err))
			assert.Equal(t, 0, prov.cagriSayisi(), "eksik gövdeyle şablon gönderilmemeli")
		})
	}
}

// TestOrderPlacedTaninmayanAlanlariYokSayar yüzeye eklenen yeni bir alanın
// bildirimi düşürmediğini doğrular.
//
// Katı çözümleme, order'a eklenen her alanın TÜM sipariş bildirimlerini
// kırması demekti.
func TestOrderPlacedTaninmayanAlanlariYokSayar(t *testing.T) {
	contacts := &fakeContacts{govde: `{"order_id":"order_01H","display_id":"7",` +
		`"email":"a@b.com","currency_code":"TRY","total":"100","item_count":"1",` +
		`"yeni_alan":"gelecekte eklendi"}`}
	svc, _, prov := olayKurulumu(t, contacts)

	require.NoError(t, svc.OrderPlaced(context.Background(), olaySiparisi("order_01H")))

	assert.Equal(t, 1, prov.cagriSayisi())
}
