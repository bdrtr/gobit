package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/payment/api"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// yeniRouter sahte servis üzerinde çalışan bir router kurar.
func yeniRouter(svc *fakePayments) chi.Router {
	r := chi.NewRouter()
	api.New(svc).Routes(r)
	return r
}

// yonetici testlerin varsayılan kimliğidir: tam yetkili bir yönetim
// kullanıcısı.
//
// Router burada DOĞRUDAN kuruluyor, yani corehttp.RequireAdmin zincirde yok ve
// context'e kimliği koyan kimse yok. Yönetim uçları artık
// corehttp.RequireScope ile korunduğu için kimliksiz istek 401 döner ve
// testlerin asıl doğruladığı davranışa (zarf, status eşlemesi, gövde çözümü)
// hiç sıra gelmezdi. Bu yüzden kimlik testin kendisi tarafından eklenir;
// testlerin NE doğruladığı değişmez, yalnızca eksik olan kimlik tamamlanır.
func yonetici() corehttp.Principal {
	return corehttp.Principal{
		ID:     "user_test",
		Kind:   "user",
		Scopes: []string{corehttp.ScopeAdmin},
	}
}

// istek verilen isteği tam yetkili bir kimlikle router'a uygular ve yanıtı
// döner.
func istek(t *testing.T, r chi.Router, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	return kimlikliIstek(t, r, method, path, body, yonetici())
}

// kimlikliIstek verilen isteği belirtilen kimlikle uygular ve yanıtı döner.
//
// Yetki denetimini sınayan testler için ayrıdır: [istek] her zaman tam yetkili
// çağırır, burada dar yetkili bir kimlik verilebilir.
func kimlikliIstek(
	t *testing.T, r chi.Router, method, path, body string, kimlik corehttp.Principal,
) *httptest.ResponseRecorder {
	t.Helper()

	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), kimlik))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// govde yanıt gövdesini haritaya çevirir.
func govde(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

// hataKodu hata zarfındaki kodu döner.
//
// Hata gövdesi tek bir "error" anahtarı altında toplanır (bkz.
// corehttp.ErrorResponse); testler bu şekli doğrudan okur ki zarfın
// değişmesi sessiz kalmasın.
func hataKodu(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	hata, ok := govde(t, rec)["error"].(map[string]any)
	require.True(t, ok, "hata zarfı bekleniyordu: %s", rec.Body.String())
	kod, ok := hata["code"].(string)
	require.True(t, ok, "kod metin olmalı: %s", rec.Body.String())
	return kod
}

// TestKoleksiyonOlusturma201VeZarfDoner mutlu yolu doğrular.
func TestKoleksiyonOlusturma201VeZarfDoner(t *testing.T) {
	svc := &fakePayments{collection: models.PaymentCollection{
		ID: "paycol_1", Reference: "cart_1", Amount: 1000,
		CurrencyCode: "TRY", Status: models.CollectionNotPaid,
	}}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodPost, "/admin/v1/payment-collections",
		`{"reference":"cart_1","amount":1000,"currency_code":"TRY"}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	data, ok := govde(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "paycol_1", data["id"])
	assert.InDelta(t, 1000, data["amount"], 0)
	assert.Equal(t, "not_paid", data["status"])
	assert.Equal(t, int64(1000), svc.sonCollectionInput.Amount)
}

// TestKoleksiyonOlusturmaTutarZorunlu eksik alanın 422 ürettiğini doğrular.
//
// İşaretçi kullanılması bilinçlidir: alanı hiç göndermeyen bir istemci sıfır
// tutar göndermiş sayılsaydı, hata mesajı "tutar pozitif olmalı" olur ve
// istemci alanı gönderdiğini sanırdı.
func TestKoleksiyonOlusturmaTutarZorunlu(t *testing.T) {
	r := yeniRouter(&fakePayments{})

	rec := istek(t, r, http.MethodPost, "/admin/v1/payment-collections",
		`{"reference":"cart_1","currency_code":"TRY"}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestBilinmeyenAlanReddedilir sessizce yutulan bir alanın olmadığını
// doğrular; yutulan alan, istemcinin uygulandığını sandığı bir ayardır.
func TestBilinmeyenAlanReddedilir(t *testing.T) {
	r := yeniRouter(&fakePayments{})

	rec := istek(t, r, http.MethodPost, "/admin/v1/payment-collections",
		`{"reference":"cart_1","amount":1,"currency_code":"TRY","typo":true}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestBosGovdeReddedilir gövdesi zorunlu uçlarda boş isteğin reddedildiğini
// doğrular.
func TestBosGovdeReddedilir(t *testing.T) {
	r := yeniRouter(&fakePayments{})

	rec := istek(t, r, http.MethodPost, "/admin/v1/payment-collections", "")

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestHataSiniflariStatusKodunaEslenir handler'ın status kodu SEÇMEDİĞİNİ,
// servisin hata sınıfının eşlendiğini doğrular (plan Bölüm 8).
func TestHataSiniflariStatusKodunaEslenir(t *testing.T) {
	tests := map[string]struct {
		err    error
		status int
	}{
		"not found": {notFound(), http.StatusNotFound},
		"invalid": {
			errors.Invalid("payment_invalid_input", "tutar pozitif olmalı"),
			http.StatusUnprocessableEntity,
		},
		"conflict": {
			errors.Conflict("payment_invalid_transition", "geçersiz geçiş"),
			http.StatusConflict,
		},
		"unavailable": {
			errors.Unavailable("payment_provider_down", "sağlayıcıya ulaşılamadı"),
			http.StatusServiceUnavailable,
		},
	}

	for ad, tt := range tests {
		t.Run(ad, func(t *testing.T) {
			r := yeniRouter(&fakePayments{err: tt.err})

			rec := istek(t, r, http.MethodGet, "/admin/v1/payment-collections/paycol_1", "")

			assert.Equal(t, tt.status, rec.Code, rec.Body.String())
			assert.Equal(t, errors.CodeOf(tt.err), hataKodu(t, rec))
		})
	}
}

// TestReddedilenYetkilendirme409Doner ödeme reddinin sunucu hatası olarak
// raporlanmadığını doğrular.
//
// 500 dönmek, entegrasyonu yazanın sorunu kendi tarafında araması demek
// olurdu; reddin sebebi sunucuda değil, karttadır.
func TestReddedilenYetkilendirme409Doner(t *testing.T) {
	r := yeniRouter(&fakePayments{
		err: errors.Conflict("payment_authorization_declined", "ödeme reddedildi"),
	})

	rec := istek(t, r, http.MethodPost, "/admin/v1/payment-sessions/payses_1/authorize", "")

	assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	assert.Equal(t, "payment_authorization_declined", hataKodu(t, rec))
}

// TestListeZarfiTutarli liste zarfının plan Bölüm 8'deki şekle uyduğunu
// doğrular.
func TestListeZarfiTutarli(t *testing.T) {
	svc := &fakePayments{
		collections: []models.PaymentCollection{{ID: "paycol_1", Status: models.CollectionNotPaid}},
		count:       7,
	}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodGet, "/admin/v1/payment-collections?limit=1&offset=2", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := govde(t, rec)
	assert.InDelta(t, 7, body["count"], 0, "count SÜZGECİN sayısıdır, sayfanın değil")
	assert.InDelta(t, 2, body["offset"], 0)
	assert.InDelta(t, 1, body["limit"], 0)
	assert.Len(t, body["data"], 1)
	assert.Equal(t, int64(1), svc.sonListInput.Page.Limit)
	assert.Equal(t, int64(2), svc.sonListInput.Page.Offset)
}

// TestSuzgecParametreleriServiseGecer sorgu parametrelerinin servise
// ulaştığını doğrular.
func TestSuzgecParametreleriServiseGecer(t *testing.T) {
	svc := &fakePayments{}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodGet,
		"/admin/v1/payment-collections?reference=cart_1&status=captured", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotNil(t, svc.sonListInput.Reference)
	assert.Equal(t, "cart_1", *svc.sonListInput.Reference)
	require.NotNil(t, svc.sonListInput.Status)
	assert.Equal(t, "captured", *svc.sonListInput.Status)
}

// TestGecersizSayfalamaParametresi422 tam sayı olmayan limitin reddedildiğini
// doğrular.
func TestGecersizSayfalamaParametresi422(t *testing.T) {
	r := yeniRouter(&fakePayments{})

	rec := istek(t, r, http.MethodGet, "/admin/v1/payment-collections?limit=abc", "")

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestOturumAcma201VeVeriGecisi gövdedeki serbest verinin servise BOZULMADAN
// ulaştığını doğrular.
//
// Sayının json.Number olarak çözülmesi kritiktir: float64'e dönen bir tam sayı
// yeniden kodlanırken üstel gösterime kayabilir ve sağlayıcı tarafında tam sayı
// olarak okunamaz (plan Bölüm 8). Sağlayıcı davranışını yönlendiren anahtar
// bilinçli olarak YÖNETİM ucundan gönderilir; mağaza ucu onu kabul etmez.
func TestOturumAcma201VeVeriGecisi(t *testing.T) {
	svc := &fakePayments{session: models.PaymentSession{
		ID: "payses_1", ProviderID: "manual", Status: models.SessionPending, Amount: 1000,
	}}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodPost, "/admin/v1/payment-collections/paycol_1/payment-sessions",
		`{"provider_id":"manual","idempotency_key":"key-1","data":{"manual_authorized_amount":1000000000000}}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, "key-1", svc.sonCreateSession.IdempotencyKey)

	deger, ok := svc.sonCreateSession.Data["manual_authorized_amount"].(json.Number)
	require.True(t, ok, "sayı json.Number olarak çözülmeli, %T geldi",
		svc.sonCreateSession.Data["manual_authorized_amount"])
	assert.Equal(t, "1000000000000", deger.String())
}

// TestMagazaOturumuTutarKabulEtmez müşterinin ödeyeceği tutarı KENDİSİNİN
// belirleyemediğini doğrular.
//
// Bulgunun tam senaryosu buydu: {"amount":1} ile açılan bir oturum,
// 50.000'lik bir koleksiyondan 1 birim tahsil edilmesine ve siparişin ödenmiş
// görünmesine yol açıyordu. Mağaza gövdesinde alan HİÇ YOKTUR; tanınmayan alan
// olarak reddedilir.
func TestMagazaOturumuTutarKabulEtmez(t *testing.T) {
	svc := &fakePayments{session: models.PaymentSession{ID: "payses_1", Amount: 1000}}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodPost, "/store/v1/payment-collections/paycol_1/payment-sessions",
		`{"provider_id":"manual","idempotency_key":"key-1","amount":1}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Zero(t, svc.sonCreateSession.IdempotencyKey, "istek servise HİÇ ulaşmamalı")
}

// TestMagazaOturumuKalanTutarinTamaminiKapar mağaza ucunun servise tutar
// GEÇMEDİĞİNİ doğrular; sıfır, "koleksiyonun kalanının tamamı" demektir.
func TestMagazaOturumuKalanTutarinTamaminiKapar(t *testing.T) {
	svc := &fakePayments{session: models.PaymentSession{ID: "payses_1", Amount: 1000}}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodPost, "/store/v1/payment-collections/paycol_1/payment-sessions",
		`{"provider_id":"manual","idempotency_key":"key-1"}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Zero(t, svc.sonCreateSession.Amount, "tutar istemciden alınmamalı")
	assert.Equal(t, "key-1", svc.sonCreateSession.IdempotencyKey)
}

// TestMagazaOturumuSaglayiciDavranisAnahtarlariniReddeder müşterinin kendi
// ödemesinin SONUCUNU yazamadığını doğrular.
//
// Anahtarlar oturumla birlikte saklanır ve yetkilendirmenin sonucunu belirler;
// mağaza ucundan geçselerdi müşteri 1 birim bloke ettirip siparişi ödenmiş
// gösterebilirdi. Sessizce süzmek yerine REDDEDİLİR: yutulan bir alan,
// istemcinin gönderdiğini sandığı ama uygulanmayan bir ayardır.
func TestMagazaOturumuSaglayiciDavranisAnahtarlariniReddeder(t *testing.T) {
	govdeler := map[string]string{
		"kismi yetkilendirme": `{"provider_id":"manual","idempotency_key":"k",` +
			`"data":{"manual_authorized_amount":1}}`,
		"sonuc": `{"provider_id":"manual","idempotency_key":"k","data":{"manual_outcome":"authorize"}}`,
		"ret sebebi": `{"provider_id":"manual","idempotency_key":"k",` +
			`"data":{"manual_decline_reason":"x"}}`,
	}

	for ad, govde := range govdeler {
		t.Run(ad, func(t *testing.T) {
			svc := &fakePayments{session: models.PaymentSession{ID: "payses_1"}}
			r := yeniRouter(svc)

			rec := istek(t, r, http.MethodPost,
				"/store/v1/payment-collections/paycol_1/payment-sessions", govde)

			assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
			assert.Empty(t, svc.sonCreateSession.Data, "istek servise HİÇ ulaşmamalı")
		})
	}
}

// TestMagazaOturumuSaglayiciyaOzguVeriyiGecirir beyaz listeye değil kara
// listeye dayanan kuralın MEŞRU veriyi engellemediğini doğrular; kart tokenı
// gibi alanlar sağlayıcıya ulaşmak zorundadır.
func TestMagazaOturumuSaglayiciyaOzguVeriyiGecirir(t *testing.T) {
	svc := &fakePayments{session: models.PaymentSession{ID: "payses_1"}}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodPost, "/store/v1/payment-collections/paycol_1/payment-sessions",
		`{"provider_id":"manual","idempotency_key":"key-1","data":{"card_token":"tok_1"}}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, "tok_1", svc.sonCreateSession.Data["card_token"])
}

// TestOturumAcmaBozukData422 nesne olmayan bir data alanının reddedildiğini
// doğrular.
func TestOturumAcmaBozukData422(t *testing.T) {
	r := yeniRouter(&fakePayments{})

	rec := istek(t, r, http.MethodPost, "/admin/v1/payment-collections/paycol_1/payment-sessions",
		`{"provider_id":"manual","idempotency_key":"key-1","data":[1,2]}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestTahsilatGovdesiIstegeBagli gövdesiz bir tahsilat isteğinin "tamamı"
// anlamına geldiğini doğrular.
//
// Boş gövdeyi hata saymak, en yaygın çağrıyı gereksiz bir JSON nesnesi yazmaya
// zorlardı.
func TestTahsilatGovdesiIstegeBagli(t *testing.T) {
	svc := &fakePayments{payment: models.Payment{ID: "pay_1", Amount: 1000}}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodPost, "/admin/v1/payment-sessions/payses_1/capture", "")

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Zero(t, svc.sonCaptureAmount, "gövdesiz istek sıfır tutar demektir")
}

// TestTahsilatTutarliGovde açık tutarın servise geçtiğini doğrular.
func TestTahsilatTutarliGovde(t *testing.T) {
	svc := &fakePayments{payment: models.Payment{ID: "pay_1", Amount: 400}}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodPost, "/admin/v1/payment-sessions/payses_1/capture", `{"amount":400}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, int64(400), svc.sonCaptureAmount)
}

// TestIptal204Doner telafi ucunun gövdesiz başarı döndüğünü doğrular.
func TestIptal204Doner(t *testing.T) {
	svc := &fakePayments{}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodPost, "/admin/v1/payment-sessions/payses_1/cancel", "")

	assert.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	assert.True(t, svc.cancelCagrisi)
	assert.Empty(t, rec.Body.String())
}

// TestIadeIstegi201VeArgumanlar iade gövdesinin servise geçtiğini doğrular.
func TestIadeIstegi201VeArgumanlar(t *testing.T) {
	svc := &fakePayments{refund: models.Refund{ID: "refund_1", Amount: 250, Reason: "müşteri talebi"}}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodPost, "/admin/v1/payments/pay_1/refunds",
		`{"amount":250,"reason":"müşteri talebi"}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, int64(250), svc.sonRefundAmount)
	assert.Equal(t, "müşteri talebi", svc.sonRefundReason)
}

// TestSaglayiciListesi vitrinin ve yönetimin aynı listeyi gördüğünü doğrular.
func TestSaglayiciListesi(t *testing.T) {
	svc := &fakePayments{providerIDs: []string{"manual"}}
	r := yeniRouter(svc)

	for _, path := range []string{"/admin/v1/payment-providers", "/store/v1/payment-providers"} {
		rec := istek(t, r, http.MethodGet, path, "")

		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		body := govde(t, rec)
		assert.Equal(t, []any{"manual"}, body["data"])
		assert.InDelta(t, 1, body["count"], 0)
	}
}

// TestMagazaYuzeyiTahsilatAcmaz müşterinin kendi tarayıcısından PARA HAREKETİ
// tetikleyemediğini doğrular.
//
// Mağaza tarafında tahsilat ucu olsaydı, siparişi hiç oluşmamış bir sepetten
// para çekilebilirdi; yetkilendirme, tahsilat ve iade sipariş tamamlama
// workflow'una aittir.
//
// İptal bu listede DEĞİLDİR ve olmamalıdır: para hareket ettirmez, müşterinin
// KENDİ açtığı rezervasyonu bırakır (bkz. TestMagazaYuzeyiOturumIptaliniAcar).
func TestMagazaYuzeyiTahsilatAcmaz(t *testing.T) {
	r := yeniRouter(&fakePayments{})

	for _, path := range []string{
		"/store/v1/payment-sessions/payses_1/authorize",
		"/store/v1/payment-sessions/payses_1/capture",
		"/store/v1/payments/pay_1/refunds",
	} {
		rec := istek(t, r, http.MethodPost, path, "")

		assert.Equal(t, http.StatusNotFound, rec.Code, "%s mağazaya açılmamalı", path)
	}
}

// TestMagazaYuzeyiOturumIptaliniAcar müşterinin kendi ödeme oturumunu
// bırakabildiğini doğrular.
//
// Regresyon: çift tahsilatı engelleyen rezervasyon koleksiyon düzeyinde
// tutulur — açık bir oturum koleksiyonun kalan tutarını kapatır. Vitrinde
// bırakma yolu olmadığında "kredi kartı" seçip sonra "havale"ye dönmek isteyen
// müşteri, bir YÖNETİCİ oturumu elle iptal edene kadar kilitli kalıyordu.
//
// İptalin para hareketi olmaması, onu authorize/capture/refund'dan ayıran
// şeydir; o üçü mağazaya kapalı kalır.
func TestMagazaYuzeyiOturumIptaliniAcar(t *testing.T) {
	fake := &fakePayments{}
	r := yeniRouter(fake)

	rec := istek(t, r, http.MethodPost, "/store/v1/payment-sessions/payses_1/cancel", "")

	assert.Equal(t, http.StatusNoContent, rec.Code,
		"müşteri kendi rezervasyonunu bırakabilmeli; aksi hâlde ödeme yöntemi değiştirilemez")
}

// TestOturumDTOsuRetSebebiniTasir teşhis alanının yanıtta göründüğünü
// doğrular; alanı gizlemek, entegrasyonu yazanın reddin sebebini hiç
// görememesi demek olurdu.
func TestOturumDTOsuRetSebebiniTasir(t *testing.T) {
	svc := &fakePayments{session: models.PaymentSession{
		ID: "payses_1", Status: models.SessionFailed, DeclineReason: "yetersiz bakiye",
	}}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodGet, "/admin/v1/payment-sessions/payses_1", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, ok := govde(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "failed", data["status"])
	assert.Equal(t, "yetersiz bakiye", data["decline_reason"])
}

// darYetkili yalnızca [api.ScopeRead] taşıyan bir yönetim kimliği döner.
func darYetkili() corehttp.Principal {
	return corehttp.Principal{
		ID:     "user_dar",
		Kind:   "user",
		Scopes: []string{api.ScopeRead},
	}
}

// TestDarYetkiliKimlikYazmaUcunda403Alir okuma yetkisinin yazmaya
// yetmediğini doğrular.
//
// Buradaki uç PARA ÇIKARIR. Yetki denetimi olmasaydı kimlik doğrulama tek
// başına yetkilendirme yerine geçerdi: yalnızca rapor okusun diye verilmiş bir
// kimlik kasadan iade yapabilirdi.
func TestDarYetkiliKimlikYazmaUcunda403Alir(t *testing.T) {
	svc := &fakePayments{}
	r := yeniRouter(svc)

	rec := kimlikliIstek(t, r, http.MethodPost, "/admin/v1/payments/pay_1/refunds",
		`{"amount":500,"reason":"müşteri iadesi"}`, darYetkili())

	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, corehttp.CodeForbidden, hataKodu(t, rec))
	assert.Zero(t, svc.sonRefundAmount, "yetki yetmiyorsa servise hiç gidilmemeli")
}

// TestDarYetkiliKimlikOkumaUcundaGecer aynı kimliğin okuma ucunda geçtiğini
// doğrular.
//
// Çift eşlik eden test budur: 403 dönen bir uç, yetki haritasının fazla dar
// olmasından da kaynaklanabilirdi. Aynı kimliğin okumada geçmesi, reddin
// yetki AYRIMINDAN geldiğini gösterir.
func TestDarYetkiliKimlikOkumaUcundaGecer(t *testing.T) {
	svc := &fakePayments{
		collections: []models.PaymentCollection{{ID: "pcol_1", Amount: 1000, CurrencyCode: "TRY"}},
		count:       1,
	}
	r := yeniRouter(svc)

	rec := kimlikliIstek(t, r, http.MethodGet, "/admin/v1/payment-collections", "", darYetkili())

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, float64(1), govde(t, rec)["count"])
}

// TestYetkisizKimlikYonetimUcunuAcamaz yetkileri BOŞ bırakılmış bir yönetim
// kullanıcısının hiçbir yönetim ucuna erişemediğini doğrular.
//
// Kimlik geçerlidir — giriş yapabilir, kim olduğu bilinir — ama yetkisi
// yoktur. Bu ayrım olmasaydı yetki listesi boş bırakılan bir kullanıcı
// "hiçbir şeye erişemez" sanılırken tahsilat tetikleyebilirdi.
func TestYetkisizKimlikYonetimUcunuAcamaz(t *testing.T) {
	yetkisiz := corehttp.Principal{ID: "user_bos", Kind: "user", Scopes: []string{}}

	for _, tc := range []struct {
		ad     string
		method string
		yol    string
	}{
		{ad: "okuma", method: http.MethodGet, yol: "/admin/v1/payment-collections"},
		{ad: "yazma", method: http.MethodPost, yol: "/admin/v1/payment-sessions/pses_1/capture"},
	} {
		t.Run(tc.ad, func(t *testing.T) {
			svc := &fakePayments{}
			r := yeniRouter(svc)

			rec := kimlikliIstek(t, r, tc.method, tc.yol, "", yetkisiz)

			assert.Equal(t, http.StatusForbidden, rec.Code)
			assert.Zero(t, svc.sonCaptureAmount, "yetkisiz kimlik için servise hiç gidilmemeli")
		})
	}
}

// TestKimliksizIstek401Alir kimliği hiç olmayan isteğin 403 DEĞİL 401
// aldığını doğrular.
//
// Ayrım istemci için anlamlıdır: 401 "kim olduğunu söyle" (kimlikle tekrar
// dene), 403 "kim olduğunu biliyorum ama yetkin yok" (tekrar denemenin
// anlamı yok) demektir.
func TestKimliksizIstek401Alir(t *testing.T) {
	r := yeniRouter(&fakePayments{})

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/payment-collections", strings.NewReader(""))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestMagazaUclariYetkiIstemez mağaza uçlarının yetki taşımayan bir kimlikle
// çalıştığını doğrular.
//
// /store/v1'in kimliği publishable anahtardır ve o anahtar tanımı gereği yetki
// TAŞIMAZ. Yönetim yetkisi eklerken mağaza ucunu da kapatmak tüm vitrini
// düşürürdü; bu risk gerçektir, çünkü sağlayıcı listesi ve koleksiyon okuma
// uçları iki yüzeyde AYNI handler'ı paylaşır.
func TestMagazaUclariYetkiIstemez(t *testing.T) {
	svc := &fakePayments{
		providerIDs: []string{"manual"},
		collection:  models.PaymentCollection{ID: "pcol_1", Amount: 1000, CurrencyCode: "TRY"},
	}
	magaza := corehttp.Principal{ID: "pk_1", Kind: "api_key", Scopes: []string{}}
	r := yeniRouter(svc)

	for _, yol := range []string{
		"/store/v1/payment-providers",
		"/store/v1/payment-collections/pcol_1",
	} {
		t.Run(yol, func(t *testing.T) {
			rec := kimlikliIstek(t, r, http.MethodGet, yol, "", magaza)

			assert.Equal(t, http.StatusOK, rec.Code)
		})
	}
}

// --- okuma yüzeyi ------------------------------------------------------------
//
// Aşağıdaki dört uç (koleksiyonun oturumları, koleksiyonun tahsilatları,
// tahsilatın kendisi, tahsilatın iadeleri) operatörün PARA KAYITLARINA tek
// bakış yüzeyidir. Yazma uçlarından farkları, hata verdiklerinde kimsenin
// haberi olmamasıdır: yanlış kaydı okuyan bir uç da 200 döner, boş yerine null
// dönen bir liste de. Bu yüzden her biri ayrı ayrı sınanır.

// TestOkumaUclariKaydinKimliginiYoldanAlir okuma uçlarının URL'deki kaydı
// sorduğunu doğrular.
//
// Bir okuma handler'ının tek işi budur: yoldan gelen kimliği servise geçirmek.
// Kimlik kaybolsa (boş dizeye düşse) ya da başka bir yol parçasından okunsa uç
// yine 200 döner ve zarf yine doğru görünür — operatör, sorduğu tahsilatın
// yanıtını aldığını sanır. Para kayıtlarında bu, bir iadeyi başka bir
// tahsilatın altında aramak demektir.
func TestOkumaUclariKaydinKimliginiYoldanAlir(t *testing.T) {
	tests := map[string]struct {
		path    string
		beklHam string
		sorulan func(*fakePayments) string
	}{
		"koleksiyonun oturumları": {
			path:    "/admin/v1/payment-collections/paycol_9/payment-sessions",
			beklHam: "paycol_9",
			sorulan: func(f *fakePayments) string { return f.sonOturumListesiKimligi },
		},
		"koleksiyonun tahsilatları": {
			path:    "/admin/v1/payment-collections/paycol_9/payments",
			beklHam: "paycol_9",
			sorulan: func(f *fakePayments) string { return f.sonTahsilatListesiKimligi },
		},
		"tahsilatın kendisi": {
			path:    "/admin/v1/payments/pay_9",
			beklHam: "pay_9",
			sorulan: func(f *fakePayments) string { return f.sonTahsilatKimligi },
		},
		"tahsilatın iadeleri": {
			path:    "/admin/v1/payments/pay_9/refunds",
			beklHam: "pay_9",
			sorulan: func(f *fakePayments) string { return f.sonIadeListesiKimligi },
		},
	}

	for ad, tt := range tests {
		t.Run(ad, func(t *testing.T) {
			svc := &fakePayments{}
			r := yeniRouter(svc)

			rec := istek(t, r, http.MethodGet, tt.path, "")

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Equal(t, tt.beklHam, tt.sorulan(svc),
				"uç, yoldaki kaydı değil başka bir kimliği sordu")
		})
	}
}

// TestBosOkumaListeleriBosDiziDoner kaydı olmayan bir listenin null DEĞİL boş
// dizi döndüğünü doğrular.
//
// Go'da nil bir dilim JSON'da null olarak kodlanır. Zarfın "data" alanı null
// dönerse istemci tarafındaki her döngü ya patlar ya da sessizce atlanır; daha
// kötüsü, "bu tahsilatın iadesi yok" ile "iade listesi alınamadı" ayırt
// edilemez hâle gelir. Operatörün mutabakat sorusu tam olarak budur ve iki
// yanıt farklı şeyler demektir.
func TestBosOkumaListeleriBosDiziDoner(t *testing.T) {
	paths := map[string]string{
		"koleksiyonun oturumları":   "/admin/v1/payment-collections/paycol_1/payment-sessions",
		"koleksiyonun tahsilatları": "/admin/v1/payment-collections/paycol_1/payments",
		"tahsilatın iadeleri":       "/admin/v1/payments/pay_1/refunds",
	}

	for ad, path := range paths {
		t.Run(ad, func(t *testing.T) {
			r := yeniRouter(&fakePayments{})

			rec := istek(t, r, http.MethodGet, path, "")

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			body := govde(t, rec)
			assert.Equal(t, []any{}, body["data"],
				"kayıt yokken liste null değil boş dizi olmalı: %s", rec.Body.String())
			assert.InDelta(t, 0, body["count"], 0)
		})
	}
}

// TestOkumaUclariServisinHataSinifiniKorur okuma uçlarının da status kodu
// SEÇMEDİĞİNİ doğrular.
//
// Olmayan bir koleksiyonun oturumları sorulduğunda 404 dönmelidir; 500 dönmek,
// kimliği yanlış yazan operatöre "sunucu bozuldu" demek ve onu kendi hatasını
// aramak yerine bir olay kaydı açmaya göndermek olurdu. Yazma uçları için
// zaten sınanan bu kural (bkz. TestHataSiniflariStatusKodunaEslenir) okuma
// uçlarında ayrıca doğrulanır: dördü de hatayı kendi elleriyle yazmaya kalksa
// diğer testlerin hiçbiri kırılmazdı.
func TestOkumaUclariServisinHataSinifiniKorur(t *testing.T) {
	paths := map[string]string{
		"koleksiyonun oturumları":   "/admin/v1/payment-collections/paycol_yok/payment-sessions",
		"koleksiyonun tahsilatları": "/admin/v1/payment-collections/paycol_yok/payments",
		"tahsilatın kendisi":        "/admin/v1/payments/pay_yok",
		"tahsilatın iadeleri":       "/admin/v1/payments/pay_yok/refunds",
	}

	for ad, path := range paths {
		t.Run(ad, func(t *testing.T) {
			r := yeniRouter(&fakePayments{err: notFound()})

			rec := istek(t, r, http.MethodGet, path, "")

			assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
			assert.Equal(t, "payment_collection_not_found", hataKodu(t, rec),
				"hata kodu servisten geldiği gibi taşınmalı")
		})
	}
}

// TestIadeListesiMutabakatIcinTutariVeSebebiTasir yapılmış bir iadenin
// listede TAM olarak göründüğünü doğrular.
//
// Bu uç, operatörün "bu tahsilattan ne kadar geri gitti ve neden" sorusuna
// verilen tek yanıttır. Tutarı düşen bir DTO, kısmi iadeleri toplamı tutmayan
// bir tabloya çevirir; sebebi düşen bir DTO ise iki iadeyi birbirinden ayırt
// edilemez kılar. İkisi de sessizdir: yanıt yine 200 ve yine dolu bir liste
// olur. Zarfın "count" alanı da sayfanın değil dönen satırların sayısıdır ve
// sayfalanmayan uçlarda istemcinin gördüğü tek sayıdır.
func TestIadeListesiMutabakatIcinTutariVeSebebiTasir(t *testing.T) {
	svc := &fakePayments{refunds: []models.Refund{
		{ID: "refund_2", PaymentID: "pay_1", Amount: 250, Reason: "müşteri talebi"},
		{ID: "refund_1", PaymentID: "pay_1", Amount: 750, Reason: "hasarlı ürün"},
	}}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodGet, "/admin/v1/payments/pay_1/refunds", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := govde(t, rec)
	assert.InDelta(t, 2, body["count"], 0, "count dönen satır sayısı olmalı")

	data, ok := body["data"].([]any)
	require.True(t, ok, "iade listesi bekleniyordu: %s", rec.Body.String())
	require.Len(t, data, 2)

	ilk, ok := data[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "refund_2", ilk["id"])
	assert.Equal(t, "pay_1", ilk["payment_id"], "iade hangi tahsilata ait olduğunu taşımalı")
	assert.InDelta(t, 250, ilk["amount"], 0, "iade tutarı yanıtta görünmeli")
	assert.Equal(t, "müşteri talebi", ilk["reason"], "iadenin sebebi yanıtta görünmeli")

	ikinci, ok := data[1].(map[string]any)
	require.True(t, ok)
	assert.InDelta(t, 750, ikinci["amount"], 0)
	assert.Equal(t, "hasarlı ürün", ikinci["reason"])
}

// TestTahsilatDetayiIadeEdilenTutariGosterir tahsilatın tekil okumasının
// iade edilmiş tutarı taşıdığını doğrular.
//
// "Bu tahsilattan ne kadarı hâlâ bizde" sorusunun yanıtı tek bir alandır ve
// listeden değil, buradan okunur. Alan düşerse tahsilat tam tutarıyla
// görünmeye devam eder — yani tamamı iade edilmiş bir tahsilat, hiç iade
// edilmemiş bir tahsilattan ayırt edilemez. Yanıtın tekil zarfla (liste
// zarfıyla değil) döndüğü de burada sabitlenir; iki şekil arasında geçiş
// istemcinin okumasını sessizce boşa çıkarırdı.
func TestTahsilatDetayiIadeEdilenTutariGosterir(t *testing.T) {
	svc := &fakePayments{payment: models.Payment{
		ID: "pay_1", PaymentSessionID: "payses_1", PaymentCollectionID: "paycol_1",
		Amount: 1000, CurrencyCode: "TRY", RefundedAmount: 400,
	}}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodGet, "/admin/v1/payments/pay_1", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, ok := govde(t, rec)["data"].(map[string]any)
	require.True(t, ok, "tekil zarf bekleniyordu: %s", rec.Body.String())
	assert.Equal(t, "pay_1", data["id"])
	assert.InDelta(t, 1000, data["amount"], 0)
	assert.InDelta(t, 400, data["refunded_amount"], 0,
		"iade edilen tutar görünmezse tamamı iade edilmiş tahsilat dokunulmamış görünür")
	assert.Equal(t, "paycol_1", data["payment_collection_id"])
}

// TestOturumListesiKoleksiyonunOturumlariniDoner koleksiyonun oturum
// listesinin satırları taşıdığını doğrular.
//
// Koleksiyonda birden çok oturum olması normaldir: reddedilen ya da bırakılan
// her deneme bir satır bırakır ve "müşteri neden ödeyemedi" sorusu ancak bu
// listeye bakılarak yanıtlanır. Liste yalnızca sonuncuyu ya da yalnızca
// açık olanı gösterirse o soru cevapsız kalır.
func TestOturumListesiKoleksiyonunOturumlariniDoner(t *testing.T) {
	svc := &fakePayments{sessions: []models.PaymentSession{
		{ID: "payses_2", PaymentCollectionID: "paycol_1", Status: models.SessionPending, Amount: 1000},
		{
			ID: "payses_1", PaymentCollectionID: "paycol_1", Status: models.SessionFailed,
			Amount: 1000, DeclineReason: "yetersiz bakiye",
		},
	}}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodGet,
		"/admin/v1/payment-collections/paycol_1/payment-sessions", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := govde(t, rec)
	assert.InDelta(t, 2, body["count"], 0)

	data, ok := body["data"].([]any)
	require.True(t, ok, "oturum listesi bekleniyordu: %s", rec.Body.String())
	require.Len(t, data, 2, "başarısız deneme de listede kalmalı")

	basarisiz, ok := data[1].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "failed", basarisiz["status"])
	assert.Equal(t, "yetersiz bakiye", basarisiz["decline_reason"],
		"reddin sebebi listede de görünmeli; teşhis tek tek oturum okumaya kalmamalı")
}

// TestTahsilatListesiKoleksiyonunTahsilatlariniDoner koleksiyonun tahsilat
// listesinin satırları taşıdığını doğrular.
//
// Kısmi tahsilat birden çok satır üretir ve koleksiyonun captured_amount
// alanı bunların TOPLAMIDIR. Toplam ile satırlar ayrışırsa bu ancak listeye
// bakılarak görülür; tek bir toplam sayı hangi tahsilatın eksik yazıldığını
// söylemez.
func TestTahsilatListesiKoleksiyonunTahsilatlariniDoner(t *testing.T) {
	svc := &fakePayments{payments: []models.Payment{
		{ID: "pay_2", PaymentCollectionID: "paycol_1", Amount: 400, CurrencyCode: "TRY"},
		{ID: "pay_1", PaymentCollectionID: "paycol_1", Amount: 600, CurrencyCode: "TRY"},
	}}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodGet, "/admin/v1/payment-collections/paycol_1/payments", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := govde(t, rec)
	assert.InDelta(t, 2, body["count"], 0)

	data, ok := body["data"].([]any)
	require.True(t, ok, "tahsilat listesi bekleniyordu: %s", rec.Body.String())
	require.Len(t, data, 2, "kısmi tahsilatların HEPSİ listede olmalı")

	var toplam float64
	for i := range data {
		satir, satirOK := data[i].(map[string]any)
		require.True(t, satirOK)
		tutar, tutarOK := satir["amount"].(float64)
		require.True(t, tutarOK, "tutar alanı olmalı: %s", rec.Body.String())
		toplam += tutar
	}
	assert.InDelta(t, 1000, toplam, 0,
		"satırların toplamı koleksiyonun tahsil edilen tutarını vermeli")
}
