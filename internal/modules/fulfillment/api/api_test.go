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

	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/api"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// adminKimlik testlerin varsayılan çağıranıdır: tam yetkili yönetici.
//
// Yönetim uçları corehttp.RequireScope ile korunuyor ve o middleware
// context'te kimlik YOKSA 401 döner. Bu testler router'ı doğrudan kuruyor,
// yani zincirde kimliği yerleştiren corehttp.RequireAdmin yok; kimliği bu
// yüzden testin kendisi koyar. Eklenen tek şey KİMLİKTİR — testlerin
// doğruladığı davranış (durum kodları, zarflar, sızıntı sınamaları) değişmedi.
var adminKimlik = corehttp.Principal{
	ID:     "usr_test",
	Kind:   "user",
	Scopes: []string{corehttp.ScopeAdmin},
}

// okumaKimligi yalnızca [api.ScopeRead] taşıyan dar yetkili çağırandır.
var okumaKimligi = corehttp.Principal{
	ID:     "usr_dar",
	Kind:   "user",
	Scopes: []string{api.ScopeRead},
}

// yeniRouter sahte servis üzerinde çalışan bir router kurar.
func yeniRouter(svc *fakeFulfillments) chi.Router {
	r := chi.NewRouter()
	api.New(svc).Routes(r)
	return r
}

// istek verilen isteği tam yetkili kimlikle router'a uygular ve yanıtı döner.
func istek(t *testing.T, r chi.Router, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	return istekKimlikle(t, r, adminKimlik, method, path, body)
}

// istekKimlikle verilen isteği verilen kimlikle router'a uygular.
func istekKimlikle(t *testing.T, r chi.Router, kimlik corehttp.Principal, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(body))
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
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	return out
}

// hataKodu hata zarfındaki kodu döner.
//
// Hata gövdesi tek bir "error" anahtarı altında toplanır (bkz.
// corehttp.ErrorResponse); testler bu şekli doğrudan okur ki zarfın değişmesi
// sessiz kalmasın.
func hataKodu(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	hata, ok := govde(t, rec)["error"].(map[string]any)
	require.True(t, ok, "hata zarfı bekleniyordu: %s", rec.Body.String())
	kod, ok := hata["code"].(string)
	require.True(t, ok, "kod metin olmalı: %s", rec.Body.String())
	return kod
}

// ornekQuoted mağaza ve yönetim yanıtlarında kullanılan örnek seçenektir.
//
// Bilinçli olarak SIZMAMASI gereken her alanı doludur: sağlayıcı, profil,
// yapılandırma ve üstveri.
func ornekQuoted() []service.QuotedOption {
	return []service.QuotedOption{{
		Option: models.ShippingOption{
			ID:                "sopt_1",
			Name:              "Standart kargo",
			ProviderID:        "gizli-kargo-firmasi",
			ShippingProfileID: "sprof_1",
			PriceType:         models.PriceFlat,
			Amount:            2_500,
			CurrencyCode:      "TRY",
			RegionID:          "reg_tr",
			Data:              map[string]any{"sozlesme_no": "GIZLI-42"},
			Metadata:          map[string]any{"ic_not": "depo B"},
		},
		Amount:       2_500,
		CurrencyCode: "TRY",
		ProviderData: json.RawMessage(`{"saglayici_ic_verisi":"GIZLI"}`),
	}}
}

// TestMagazaYanitiSaglayiciVerisiniSizdirmaz Faz 7'nin açık şartını kanıtlar.
//
// Vitrin yanıtında sağlayıcı kimliği, sağlayıcının ham verisi, seçeneğin
// yapılandırması, üstverisi ve profil kimliği HİÇ görünmemelidir.
func TestMagazaYanitiSaglayiciVerisiniSizdirmaz(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{quoted: ornekQuoted()}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodGet, "/store/v1/shipping-options?currency_code=TRY", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	ham := rec.Body.String()
	for _, sizinti := range []string{
		"gizli-kargo-firmasi", "provider_id",
		"saglayici_ic_verisi", "sozlesme_no", "ic_not",
		"shipping_profile_id", "admin_only", "region_id", "metadata",
	} {
		assert.NotContains(t, ham, sizinti, "mağaza yanıtına %q sızmamalı", sizinti)
	}

	data, ok := govde(t, rec)["data"].([]any)
	require.True(t, ok, rec.Body.String())
	require.Len(t, data, 1)

	secenek, ok := data[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "sopt_1", secenek["id"])
	assert.Equal(t, "Standart kargo", secenek["name"])
	assert.EqualValues(t, 2_500, secenek["amount"])
	assert.Equal(t, "TRY", secenek["currency_code"])
	assert.Equal(t, "flat", secenek["price_type"])
	assert.Len(t, secenek, 5, "mağaza gösterimi yalnızca beş alan taşımalı")
}

// TestMagazaUcuAdminOnlyIsteyemez bayrağın sorgu parametresinden
// OKUNMADIĞINI kanıtlar.
//
// Okunsaydı, vitrinden gelen tek bir parametre yönetime özel seçenekleri
// açardı.
func TestMagazaUcuAdminOnlyIsteyemez(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{quoted: ornekQuoted()}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodGet,
		"/store/v1/shipping-options?currency_code=TRY&include_admin_only=true&admin_only=true", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.False(t, svc.sonListeInput.IncludeAdminOnly,
		"mağaza ucu admin_only seçenekleri asla istememeli")
}

// TestMagazaUcuSepetOlgularinaGuvenmez ikinci güven kararını sabitler.
//
// Regresyon: kural motorunun baktığı sayısal olgular (subtotal, item_count,
// total_weight) doğrudan sorgu parametresinden alınıyor ve servise GÜVENİLİR
// diye veriliyordu. Boş sepetle "?subtotal=50000" gönderen bir müşteri,
// kendisine kapalı olan ücretsiz kargo seçeneğini ve ücretini görüyordu.
// Bayrak sorgudan OKUNMAZ ve mağaza ucunda sabit olarak false'tur.
func TestMagazaUcuSepetOlgularinaGuvenmez(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{quoted: ornekQuoted()}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodGet,
		"/store/v1/shipping-options?currency_code=TRY&subtotal=50000&trusted_facts=true", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.False(t, svc.sonListeInput.TrustedFacts,
		"mağaza ucu sepet olgularını asla güvenilir bildirmemeli")
	assert.Equal(t, int64(50_000), svc.sonListeInput.Subtotal,
		"olgu yine de iletilmeli; güvenilmemesi iletilmemesi demek değildir")
}

// TestYonetimUygunlukUcuOlgulariGuvenilirBildirir ayrımın diğer yarısıdır.
//
// Yönetim ucu bir ÖNİZLEME aracıdır: yönetici zaten tüm kataloğu ve
// kurallarını okuyabildiği için bağlamı uydurması ona yeni bir şey açmaz.
func TestYonetimUygunlukUcuOlgulariGuvenilirBildirir(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{quoted: ornekQuoted()}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodGet,
		"/admin/v1/shipping-options/eligible?currency_code=TRY&subtotal=50000", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.True(t, svc.sonListeInput.TrustedFacts)
}

// TestYonetimUygunlukUcuAdminOnlyIster ayrımın diğer yarısını kanıtlar.
func TestYonetimUygunlukUcuAdminOnlyIster(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{quoted: ornekQuoted()}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodGet,
		"/admin/v1/shipping-options/eligible?currency_code=TRY", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.True(t, svc.sonListeInput.IncludeAdminOnly)

	assert.Contains(t, rec.Body.String(), "provider_id",
		"yönetim gösterimi sağlayıcıyı taşımalı")
	assert.NotContains(t, rec.Body.String(), "saglayici_ic_verisi",
		"sağlayıcının ham verisi yönetim listesinde de taşınmaz")
}

// TestUygunlukSorgusuCozulur sorgu parametrelerinin servise doğru
// aktarıldığını kanıtlar.
//
// Profil kimliği TEKRARLANABİLİR bir parametredir; bir sepette birden çok
// profile bağlı ürün bulunabilir.
func TestUygunlukSorgusuCozulur(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodGet,
		"/store/v1/shipping-options?region_id=reg_tr&currency_code=TRY&country_code=TR"+
			"&shipping_profile_id=sprof_1&shipping_profile_id=sprof_2"+
			"&subtotal=50000&item_count=3&total_weight=1500&is_return=true", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	in := svc.sonListeInput
	assert.Equal(t, "reg_tr", in.RegionID)
	assert.Equal(t, "TRY", in.CurrencyCode)
	assert.Equal(t, "TR", in.CountryCode)
	assert.Equal(t, []string{"sprof_1", "sprof_2"}, in.ShippingProfileIDs)
	assert.Equal(t, int64(50_000), in.Subtotal)
	assert.Equal(t, int64(3), in.ItemCount)
	assert.Equal(t, int64(1_500), in.TotalWeight)
	assert.True(t, in.IsReturn)
}

// TestBozukSorguParametresi422Doner sayı olmayan bir parametrenin istemci
// hatası sayıldığını kanıtlar.
func TestBozukSorguParametresi422Doner(t *testing.T) {
	t.Parallel()

	r := yeniRouter(&fakeFulfillments{})

	rec := istek(t, r, http.MethodGet, "/store/v1/shipping-options?subtotal=abc", "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())

	rec = istek(t, r, http.MethodGet, "/store/v1/shipping-options?is_return=belki", "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestSecenekOlusturma201VeZarfDoner mutlu yolu ve gövde çevirisini doğrular.
func TestSecenekOlusturma201VeZarfDoner(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{option: models.ShippingOption{
		ID: "sopt_1", Name: "Standart kargo", ProviderID: "manual",
		ShippingProfileID: "sprof_1", PriceType: models.PriceFlat,
		Amount: 2_500, CurrencyCode: "TRY",
	}}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodPost, "/admin/v1/shipping-options",
		`{"name":"Standart kargo","provider_id":"manual","shipping_profile_id":"sprof_1",`+
			`"price_type":"flat","amount":2500,"currency_code":"TRY"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	assert.Equal(t, "Standart kargo", svc.sonOptionInput.Name)
	assert.Equal(t, "manual", svc.sonOptionInput.ProviderID)
	assert.Equal(t, int64(2_500), svc.sonOptionInput.Amount)

	data, ok := govde(t, rec)["data"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	assert.Equal(t, "sopt_1", data["id"])
}

// TestTaninmayanGovdeAlaniReddedilir sessizce yutulan bir ayarın olmadığını
// kanıtlar.
func TestTaninmayanGovdeAlaniReddedilir(t *testing.T) {
	t.Parallel()

	r := yeniRouter(&fakeFulfillments{})

	rec := istek(t, r, http.MethodPost, "/admin/v1/shipping-options",
		`{"name":"Kargo","currency_code":"TRY","bilinmeyen":1}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestSecenekGuncellemesindeSaglayiciAlaniYok güncelleme gövdesinin
// sağlayıcıyı ve profili kabul ETMEDİĞİNİ kanıtlar.
//
// Kabul etseydi, o seçenekle açılmış gönderilerin hangi sağlayıcıda olduğu
// geçmişe dönük yanıltıcı hâle gelirdi.
func TestSecenekGuncellemesindeSaglayiciAlaniYok(t *testing.T) {
	t.Parallel()

	r := yeniRouter(&fakeFulfillments{})

	rec := istek(t, r, http.MethodPatch, "/admin/v1/shipping-options/sopt_1",
		`{"provider_id":"baska-firma"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())

	rec = istek(t, r, http.MethodPatch, "/admin/v1/shipping-options/sopt_1",
		`{"shipping_profile_id":"sprof_2"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestGuncellemeIsaretcileriVerilmeyenAlaniDegistirmez PATCH semantiğini
// kanıtlar.
func TestGuncellemeIsaretcileriVerilmeyenAlaniDegistirmez(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodPatch, "/admin/v1/shipping-options/sopt_1", `{"name":"Yeni ad"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	require.NotNil(t, svc.sonUpdateOption.Name)
	assert.Equal(t, "Yeni ad", *svc.sonUpdateOption.Name)
	assert.Nil(t, svc.sonUpdateOption.Amount, "verilmeyen alan nil kalmalı")
	assert.Nil(t, svc.sonUpdateOption.AdminOnly)
}

// TestGonderiOlusturmaKalemleriCevirir kalem gövdesinin servise aktarıldığını
// ve adetsiz kalemin reddedildiğini kanıtlar.
func TestGonderiOlusturmaKalemleriCevirir(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{fulfillment: models.Fulfillment{
		ID: "ful_1", Reference: "order_1", ShippingOptionID: "sopt_1",
		ProviderID: "manual", Status: models.StatusPending,
	}}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodPost, "/admin/v1/fulfillments",
		`{"reference":"order_1","shipping_option_id":"sopt_1","idempotency_key":"a",`+
			`"items":[{"line_item_id":"line_1","quantity":2}]}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Len(t, svc.sonCreateInput.Items, 1)
	assert.Equal(t, int64(2), svc.sonCreateInput.Items[0].Quantity)

	rec = istek(t, r, http.MethodPost, "/admin/v1/fulfillments",
		`{"reference":"order_1","shipping_option_id":"sopt_1","idempotency_key":"a",`+
			`"items":[{"line_item_id":"line_1"}]}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code,
		"adetsiz kalem reddedilmeli: %s", rec.Body.String())
}

// TestGonderiYanitiKalemleriTasir liste alanının daima dizi olduğunu kanıtlar.
func TestGonderiYanitiKalemleriTasir(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{fulfillment: models.Fulfillment{
		ID: "ful_1", Reference: "order_1", ShippingOptionID: "sopt_1",
		ProviderID: "manual", Status: models.StatusPending,
	}}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodGet, "/admin/v1/fulfillments/ful_1", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	data, ok := govde(t, rec)["data"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	items, ok := data["items"].([]any)
	require.True(t, ok, "kalemler daima dizi olmalı: %s", rec.Body.String())
	assert.Empty(t, items)
}

// TestIptalGuncelKaydiDoner iptalin gövdeli yanıt verdiğini kanıtlar.
//
// Çağıran, iptalin gerçekten yazıldığını durum alanından görebilmelidir.
func TestIptalGuncelKaydiDoner(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{fulfillment: models.Fulfillment{
		ID: "ful_1", Reference: "order_1", ShippingOptionID: "sopt_1",
		ProviderID: "manual", Status: models.StatusCanceled,
	}}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodPost, "/admin/v1/fulfillments/ful_1/cancel", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "ful_1", svc.sonIptalEdilen)

	data, ok := govde(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "canceled", data["status"])
}

// TestKargoyaVermeGovdesiIstegeBagli takip numarası olmadan da sevk
// bildirilebildiğini kanıtlar.
//
// Bazı taşıyıcılar numarayı sonradan verir; gövdeyi zorunlu kılmak o akışı
// imkânsız hâle getirirdi.
func TestKargoyaVermeGovdesiIstegeBagli(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{fulfillment: models.Fulfillment{
		ID: "ful_1", Reference: "order_1", ShippingOptionID: "sopt_1",
		ProviderID: "manual", Status: models.StatusShipped,
	}}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodPost, "/admin/v1/fulfillments/ful_1/ship", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, [2]string{"", ""}, svc.sonShipTracking)

	rec = istek(t, r, http.MethodPost, "/admin/v1/fulfillments/ful_1/ship",
		`{"tracking_number":"TK-1","tracking_url":"https://kargo/1"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, [2]string{"TK-1", "https://kargo/1"}, svc.sonShipTracking)
}

// TestUzunlugBilinmeyenGovdeYokSayilmaz chunked kodlamayla gelen bir gövdenin
// okunduğunu kanıtlar.
//
// Content-Length'e bakan bir kontrol, uzunluğu -1 olan (chunked) bir istekte
// gerçekten gönderilmiş takip numarasını sessizce yok sayardı; istemci bunu
// ancak kargo ekranında görürdü.
func TestUzunlugBilinmeyenGovdeYokSayilmaz(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{fulfillment: models.Fulfillment{
		ID: "ful_1", Reference: "order_1", ShippingOptionID: "sopt_1",
		ProviderID: "manual", Status: models.StatusShipped,
	}}
	r := yeniRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/admin/v1/fulfillments/ful_1/ship",
		strings.NewReader(`{"tracking_number":"TK-9"}`))
	req.Header.Set("Content-Type", "application/json")
	// Yönetim ucu artık yetki istiyor; kimlik context'e konmazsa istek
	// gövdeye hiç bakılmadan 401 ile döner ve testin sınadığı chunked okuma
	// yolu hiç çalışmazdı. Eklenen tek şey kimliktir.
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), adminKimlik))
	// httptest gövde uzunluğunu okuyucudan çıkarır; chunked isteği taklit
	// etmek için BİLİNMEYEN uzunluk elle konur.
	req.ContentLength = -1

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, [2]string{"TK-9", ""}, svc.sonShipTracking,
		"uzunluğu bilinmeyen gövde de okunmalı")
}

// TestHataSiniflariStatusKoduneCevrilir handler'ın status seçmediğini,
// çevirinin corehttp'de yapıldığını kanıtlar (plan Bölüm 8).
func TestHataSiniflariStatusKoduneCevrilir(t *testing.T) {
	t.Parallel()

	t.Run("bulunamadı 404", func(t *testing.T) {
		t.Parallel()

		r := yeniRouter(&fakeFulfillments{err: notFoundHatasi()})
		rec := istek(t, r, http.MethodGet, "/admin/v1/fulfillments/ful_1", "")
		assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
		assert.Equal(t, "fulfillment_not_found", hataKodu(t, rec))
	})

	t.Run("çakışma 409", func(t *testing.T) {
		t.Parallel()

		r := yeniRouter(&fakeFulfillments{err: conflictHatasi()})
		rec := istek(t, r, http.MethodPost, "/admin/v1/fulfillments/ful_1/cancel", "")
		assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		assert.Equal(t, service.CodeInvalidTransition, hataKodu(t, rec))
	})
}

// TestSilmeleri204Doner gövdesiz silme yanıtlarını doğrular.
func TestSilmeleri204Doner(t *testing.T) {
	t.Parallel()

	r := yeniRouter(&fakeFulfillments{})

	for _, yol := range []string{
		"/admin/v1/shipping-profiles/sprof_1",
		"/admin/v1/shipping-options/sopt_1",
		"/admin/v1/shipping-options/sopt_1/rules/sorule_1",
	} {
		rec := istek(t, r, http.MethodDelete, yol, "")
		assert.Equal(t, http.StatusNoContent, rec.Code, yol)
		assert.Empty(t, rec.Body.String(), yol)
	}
}

// TestKuralOlusturmaGovdesiCevrilir kural gövdesinin servise aktarıldığını
// kanıtlar.
func TestKuralOlusturmaGovdesiCevrilir(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{rule: models.ShippingOptionRule{
		ID: "sorule_1", ShippingOptionID: "sopt_1",
		Attribute: "subtotal", Operator: models.OpGte, Values: []string{"50000"},
	}}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodPost, "/admin/v1/shipping-options/sopt_1/rules",
		`{"attribute":"subtotal","operator":"gte","values":["50000"]}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, "subtotal", svc.sonRuleInput.Attribute)
	assert.Equal(t, []string{"50000"}, svc.sonRuleInput.Values)
}

// TestSaglayiciListesiYalnizcaYonetimde kargo firmalarının mağaza yüzeyine
// açılmadığını kanıtlar.
//
// payment'tan farkı budur: müşteri hangi ödeme yolunu seçeceğini bilmek
// zorundadır, ama hangi kargo firmasıyla çalışıldığı mağazanın operasyonel
// bilgisidir.
func TestSaglayiciListesiYalnizcaYonetimde(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{providerIDs: []string{"manual"}}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodGet, "/admin/v1/fulfillment-providers", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = istek(t, r, http.MethodGet, "/store/v1/fulfillment-providers", "")
	assert.Equal(t, http.StatusNotFound, rec.Code, "mağaza yüzeyinde böyle bir uç olmamalı")
}

// TestListeZarfiTutarli sayfalama zarfının alanlarını doğrular (plan Bölüm 8).
func TestListeZarfiTutarli(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{
		options: []models.ShippingOption{{ID: "sopt_1", Name: "Kargo", CurrencyCode: "TRY"}},
		count:   42,
	}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodGet, "/admin/v1/shipping-options?limit=10&offset=20", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	body := govde(t, rec)
	assert.EqualValues(t, 42, body["count"], "count süzgece uyan TÜM kayıtların sayısı olmalı")
	assert.EqualValues(t, 20, body["offset"])
	assert.EqualValues(t, 10, body["limit"])
	assert.Contains(t, body, "data")
}

// TestDarYetkiYazmaUcunuAcmaz yalnızca okuma yetkisi taşıyan bir kimliğin
// yönetim yazma uçlarında 403 aldığını kanıtlar.
//
// Kimlik doğrulama tek başına yetmez: yetkileri boşaltılmış ya da yalnızca
// okumaya yetkili bir yönetim kullanıcısı, yetki zorlaması olmadan gönderi
// açıp kargo etiketi bastırabilir, açılmış bir gönderiyi iptal edebilir ya da
// hiç gönderilmemiş bir siparişi "teslim edildi" diye kapatabilirdi. 401
// değil 403 beklenir — kimlik bilinmektedir, eksik olan yetkidir.
func TestDarYetkiYazmaUcunuAcmaz(t *testing.T) {
	t.Parallel()

	// Sahte servis her çağrıya BAŞARIYLA yanıt verir; 403 gelmesi tek
	// başına middleware'in isteği handler'a hiç ulaştırmadığını gösterir.
	svc := &fakeFulfillments{}
	r := yeniRouter(svc)

	for _, durum := range []struct {
		ad     string
		method string
		path   string
		body   string
	}{
		{"profil oluştur", http.MethodPost, "/admin/v1/shipping-profiles", `{"name":"Varsayılan","type":"default"}`},
		{"profil güncelle", http.MethodPatch, "/admin/v1/shipping-profiles/sprof_1", `{"name":"Yeni"}`},
		{"profil sil", http.MethodDelete, "/admin/v1/shipping-profiles/sprof_1", ``},
		{"seçenek oluştur", http.MethodPost, "/admin/v1/shipping-options", `{"name":"Kargo"}`},
		{"seçenek güncelle", http.MethodPatch, "/admin/v1/shipping-options/sopt_1", `{"name":"Kargo 2"}`},
		{"seçenek sil", http.MethodDelete, "/admin/v1/shipping-options/sopt_1", ``},
		{"kural ekle", http.MethodPost, "/admin/v1/shipping-options/sopt_1/rules", `{"attribute":"subtotal","operator":"gte","value":"1000"}`},
		{"kural sil", http.MethodDelete, "/admin/v1/shipping-options/sopt_1/rules/sorule_1", ``},
		{"gönderi aç", http.MethodPost, "/admin/v1/fulfillments", `{"reference":"order_1","shipping_option_id":"sopt_1"}`},
		{"gönderi iptal", http.MethodPost, "/admin/v1/fulfillments/ful_1/cancel", ``},
		{"kargoya ver", http.MethodPost, "/admin/v1/fulfillments/ful_1/ship", `{"tracking_number":"TK-9"}`},
		{"teslim bildir", http.MethodPost, "/admin/v1/fulfillments/ful_1/deliver", ``},
	} {
		rec := istekKimlikle(t, r, okumaKimligi, durum.method, durum.path, durum.body)
		assert.Equal(t, http.StatusForbidden, rec.Code, "durum: %s", durum.ad)
		assert.Equal(t, corehttp.CodeForbidden, hataKodu(t, rec), "durum: %s", durum.ad)
	}

	assert.Empty(t, svc.sonIptalEdilen, "istek servise hiç ulaşmamalı")
	assert.Equal(t, [2]string{}, svc.sonShipTracking, "istek servise hiç ulaşmamalı")
}

// TestDarYetkiOkumaUcundaGecer aynı dar kimliğin okuma uçlarından geçtiğini
// kanıtlar.
//
// Bu testin çifti [TestDarYetkiYazmaUcunuAcmaz]'dır: yetki haritası her yazma
// ucunu kapatırken okuma uçlarını da kapatsaydı, 403 sonuçları haritanın
// doğruluğunu değil yalnızca aşırı kısıtlayıcılığını kanıtlardı.
func TestDarYetkiOkumaUcundaGecer(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{providerIDs: []string{"manual"}, quoted: ornekQuoted()}
	r := yeniRouter(svc)

	for _, path := range []string{
		"/admin/v1/fulfillment-providers",
		"/admin/v1/shipping-profiles",
		"/admin/v1/shipping-profiles/sprof_1",
		"/admin/v1/shipping-options",
		"/admin/v1/shipping-options/eligible?currency_code=TRY",
		"/admin/v1/shipping-options/sopt_1",
		"/admin/v1/shipping-options/sopt_1/rules",
		"/admin/v1/fulfillments",
		"/admin/v1/fulfillments/ful_1",
	} {
		rec := istekKimlikle(t, r, okumaKimligi, http.MethodGet, path, "")
		assert.Equal(t, http.StatusOK, rec.Code, "yol: %s — gövde: %s", path, rec.Body.String())
	}
}

// TestMagazaUcuYetkiIstemez mağaza ucunun yetkisiz bir kimlikle de
// çalıştığını kanıtlar.
//
// Vitrinin kimliği publishable anahtardır ve o anahtar tanımı gereği yetki
// TAŞIMAZ. Yönetim uçlarına yetki eklerken store ucuna da eklemek, ilk
// dağıtımda bütün vitrini kapatmanın en sessiz yoludur; bu test o hatayı
// yakalar.
func TestMagazaUcuYetkiIstemez(t *testing.T) {
	t.Parallel()

	r := yeniRouter(&fakeFulfillments{quoted: ornekQuoted()})

	yetkisiz := corehttp.Principal{ID: "pk_1", Kind: "api_key"}
	rec := istekKimlikle(t, r, yetkisiz, http.MethodGet,
		"/store/v1/shipping-options?currency_code=TRY", "")
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}
