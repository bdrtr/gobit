package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/order/api"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// fakeOrders api.Orders'ın test karşılığıdır.
//
// HTTP katmanının sorumluluğu dardır: gövdeyi çöz, servisi çağır, zarfı ve
// status kodunu yaz. Sahte servis bu yüzden iş mantığı taşımaz; yalnızca
// kaydedilen çağrıyı ve önceden ayarlanmış yanıtı döner.
type fakeOrders struct {
	order    models.Order
	detail   models.OrderDetail
	orders   []models.Order
	count    int64
	ret      models.Return
	exchange models.Exchange
	claim    models.Claim
	returns  []models.Return

	// err ayarlanırsa tüm metotlar bu hatayı döner; hata eşlemesini sınamak
	// için kullanılır.
	err error

	// Son çağrının argümanları.
	listInput     service.ListOrdersInput
	returnInput   service.CreateReturnInput
	exchangeInput service.CreateExchangeInput
	claimInput    service.CreateClaimInput
	page          service.Page
	gotOrderID    string
	gotChildID    string
	gotReason     string
	// calls çağrılan metotları SIRASIYLA kaydeder; iptal ucunun okumayı da
	// yaptığını doğrulamak için gerekir.
	calls []string
}

// Sahtenin handler'ın beklediği yüzeyi karşıladığı derleme zamanında doğrulanır.
var _ api.Orders = (*fakeOrders)(nil)

// kaydet çağrıyı sıraya ekler.
func (f *fakeOrders) kaydet(name string) { f.calls = append(f.calls, name) }

// GetOrder siparişi çocuklarıyla döner.
func (f *fakeOrders) GetOrder(_ context.Context, orderID string) (models.OrderDetail, error) {
	f.kaydet("GetOrder")
	f.gotOrderID = orderID
	return f.detail, f.err
}

// ListOrders siparişleri döner.
func (f *fakeOrders) ListOrders(_ context.Context, in service.ListOrdersInput) ([]models.Order, int64, error) {
	f.kaydet("ListOrders")
	f.listInput = in
	return f.orders, f.count, f.err
}

// CancelOrder siparişi iptal eder.
func (f *fakeOrders) CancelOrder(_ context.Context, orderID, reason string) error {
	f.kaydet("CancelOrder")
	f.gotOrderID = orderID
	f.gotReason = reason
	return f.err
}

// CompleteOrder siparişi tamamlar.
func (f *fakeOrders) CompleteOrder(_ context.Context, orderID string) (models.Order, error) {
	f.kaydet("CompleteOrder")
	f.gotOrderID = orderID
	return f.order, f.err
}

// ArchiveOrder siparişi arşivler.
func (f *fakeOrders) ArchiveOrder(_ context.Context, orderID string) (models.Order, error) {
	f.kaydet("ArchiveOrder")
	f.gotOrderID = orderID
	return f.order, f.err
}

// CreateReturn iade kaydı açar.
func (f *fakeOrders) CreateReturn(_ context.Context, in service.CreateReturnInput) (models.Return, error) {
	f.kaydet("CreateReturn")
	f.returnInput = in
	return f.ret, f.err
}

// GetReturn iade kaydını döner.
func (f *fakeOrders) GetReturn(_ context.Context, returnID string) (models.Return, error) {
	f.kaydet("GetReturn")
	f.gotChildID = returnID
	return f.ret, f.err
}

// ListReturns iade kayıtlarını döner.
func (f *fakeOrders) ListReturns(_ context.Context, orderID string, page service.Page) ([]models.Return, int64, error) {
	f.kaydet("ListReturns")
	f.gotOrderID = orderID
	f.page = page
	return f.returns, f.count, f.err
}

// CreateExchange değişim kaydı açar.
func (f *fakeOrders) CreateExchange(_ context.Context, in service.CreateExchangeInput) (models.Exchange, error) {
	f.kaydet("CreateExchange")
	f.exchangeInput = in
	return f.exchange, f.err
}

// GetExchange değişim kaydını döner.
func (f *fakeOrders) GetExchange(_ context.Context, exchangeID string) (models.Exchange, error) {
	f.kaydet("GetExchange")
	f.gotChildID = exchangeID
	return f.exchange, f.err
}

// ListExchanges değişim kayıtlarını döner.
func (f *fakeOrders) ListExchanges(_ context.Context, orderID string, page service.Page) ([]models.Exchange, int64, error) {
	f.kaydet("ListExchanges")
	f.gotOrderID = orderID
	f.page = page
	return nil, f.count, f.err
}

// CreateClaim hasar kaydı açar.
func (f *fakeOrders) CreateClaim(_ context.Context, in service.CreateClaimInput) (models.Claim, error) {
	f.kaydet("CreateClaim")
	f.claimInput = in
	return f.claim, f.err
}

// GetClaim hasar kaydını döner.
func (f *fakeOrders) GetClaim(_ context.Context, claimID string) (models.Claim, error) {
	f.kaydet("GetClaim")
	f.gotChildID = claimID
	return f.claim, f.err
}

// ListClaims hasar kayıtlarını döner.
func (f *fakeOrders) ListClaims(_ context.Context, orderID string, page service.Page) ([]models.Claim, int64, error) {
	f.kaydet("ListClaims")
	f.gotOrderID = orderID
	f.page = page
	return nil, f.count, f.err
}

// ornekSiparis testlerde kullanılan sipariş modelidir.
func ornekSiparis() models.Order {
	return models.Order{
		ID:            "order_1",
		DisplayID:     1042,
		Status:        models.OrderPending,
		RegionID:      "reg_1",
		CustomerID:    "cus_1",
		Email:         "musteri@ornek.com",
		CurrencyCode:  "TRY",
		CartID:        "cart_1",
		Subtotal:      3000,
		TaxTotal:      600,
		ShippingTotal: 2500,
		Total:         6100,
		PlacedAt:      time.Unix(0, 0).UTC(),
		CreatedAt:     time.Unix(0, 0).UTC(),
		UpdatedAt:     time.Unix(0, 0).UTC(),
	}
}

// ornekDetay testlerde kullanılan sipariş ayrıntısıdır.
func ornekDetay() models.OrderDetail {
	return models.OrderDetail{
		Order: ornekSiparis(),
		Items: []models.OrderLineItem{{
			ID: "oli_1", OrderID: "order_1", VariantID: "variant_1",
			Title: "Kırmızı Tişört", Quantity: 3, UnitPrice: 1000,
			Subtotal: 3000, TaxTotal: 600, Total: 3600,
		}},
		Summary: models.OrderSummary{
			ID: "osum_1", OrderID: "order_1", PaidTotal: 6100,
		},
	}
}

// yeniRouter sahte servisle bağlanmış bir router üretir.
func yeniRouter(svc api.Orders) chi.Router {
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

// istek verilen yolu tam yetkili bir kimlikle çağırır ve yanıtı döner.
func istek(t *testing.T, r chi.Router, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	return kimlikliIstek(t, r, method, path, body, yonetici())
}

// kimlikliIstek verilen yolu belirtilen kimlikle çağırır ve yanıtı döner.
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
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), kimlik))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// coz yanıt gövdesini haritaya çözer.
func coz(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

// TestSiparisOlusturmaUcuYok siparişin HTTP'den açılamadığını doğrular.
//
// Uç açık olsaydı bir istemci kendi belirlediği toplamla — örneğin sıfır
// tutarla — sipariş yazabilirdi; tutarların GERÇEK fiyatlara karşılık geldiğini
// yalnızca complete_cart workflow'u güvence altına alabilir.
func TestSiparisOlusturmaUcuYok(t *testing.T) {
	svc := &fakeOrders{}
	r := yeniRouter(svc)

	// /admin/v1/orders yalnızca GET tanımlıdır; POST'u chi 405 ile geri çevirir.
	rec := istek(t, r, http.MethodPost, "/admin/v1/orders", `{"region_id":"reg_1"}`)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)

	// Müşteri tarafında bu yolun hiçbir metodu yoktur; sonuç 404'tür.
	rec = istek(t, r, http.MethodPost, "/store/v1/orders", `{"region_id":"reg_1"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	assert.Empty(t, svc.calls, "servise hiç çağrı gitmemeli")
}

// TestStoreListeUcuYok müşteri tarafında liste ucu olmadığını doğrular.
//
// Liste ucu, tek bir sipariş kimliğini bilmeyi tüm siparişleri okumaya
// çevirirdi; yetkilendirme Faz 8'e kaldığı için bu kapı bugün hiç açılmaz.
func TestStoreListeUcuYok(t *testing.T) {
	svc := &fakeOrders{}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodGet, "/store/v1/orders", "")

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Empty(t, svc.calls)
}

// TestAdminSiparisOkuma tekil okuma zarfını doğrular.
func TestAdminSiparisOkuma(t *testing.T) {
	svc := &fakeOrders{detail: ornekDetay()}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodGet, "/admin/v1/orders/order_1", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "order_1", svc.gotOrderID)

	govde := coz(t, rec)
	veri, ok := govde["data"].(map[string]any)
	require.True(t, ok, "yanıt data zarfıyla dönmeli")
	assert.Equal(t, float64(1042), veri["display_id"])
	assert.Equal(t, "pending", veri["status"])
	assert.Equal(t, float64(6100), veri["total"])

	satirlar, ok := veri["items"].([]any)
	require.True(t, ok)
	assert.Len(t, satirlar, 1)

	ozet, ok := veri["summary"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(6100), ozet["paid_total"])
	assert.Equal(t, float64(0), ozet["outstanding"],
		"kalan tutar türetilmiş olarak sunulmalı")
}

// TestStoreSiparisOkuma müşteri ucunun aynı zarfı döndürdüğünü doğrular.
func TestStoreSiparisOkuma(t *testing.T) {
	svc := &fakeOrders{detail: ornekDetay()}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodGet, "/store/v1/orders/order_1", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{"GetOrder"}, svc.calls)
}

// TestAdminSiparisListesi liste zarfını ve süzgeçleri doğrular (plan Bölüm 8).
func TestAdminSiparisListesi(t *testing.T) {
	svc := &fakeOrders{orders: []models.Order{ornekSiparis()}, count: 7}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodGet,
		"/admin/v1/orders?limit=2&offset=4&customer_id=cus_1&region_id=reg_1&status=pending", "")

	require.Equal(t, http.StatusOK, rec.Code)

	govde := coz(t, rec)
	assert.Equal(t, float64(7), govde["count"], "count FİLTRENİN toplamı olmalı")
	assert.Equal(t, float64(4), govde["offset"])
	assert.Equal(t, float64(2), govde["limit"])
	veri, ok := govde["data"].([]any)
	require.True(t, ok)
	assert.Len(t, veri, 1)

	require.NotNil(t, svc.listInput.CustomerID)
	assert.Equal(t, "cus_1", *svc.listInput.CustomerID)
	require.NotNil(t, svc.listInput.RegionID)
	assert.Equal(t, "reg_1", *svc.listInput.RegionID)
	require.NotNil(t, svc.listInput.Status)
	assert.Equal(t, models.OrderPending, *svc.listInput.Status)
	assert.Equal(t, int64(2), svc.listInput.Page.Limit)
	assert.Equal(t, int64(4), svc.listInput.Page.Offset)
}

// TestAdminSiparisListesiVarsayilanLimit limit verilmeyen istekte yanıtın
// GERÇEKTEN uygulanan sınırı gösterdiğini doğrular.
func TestAdminSiparisListesiVarsayilanLimit(t *testing.T) {
	svc := &fakeOrders{}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodGet, "/admin/v1/orders", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, float64(service.DefaultLimit), coz(t, rec)["limit"])
	assert.Equal(t, service.DefaultLimit, svc.listInput.Page.Limit)
}

// TestAdminSiparisListesiBozukParametre sayısal olmayan sayfalamayı reddeder.
func TestAdminSiparisListesiBozukParametre(t *testing.T) {
	svc := &fakeOrders{}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodGet, "/admin/v1/orders?limit=cok", "")

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Empty(t, svc.calls, "geçersiz parametrede servise gidilmemeli")
}

// TestAdminIptalGovdesizCalisir gerekçesiz iptalin kabul edildiğini doğrular.
func TestAdminIptalGovdesizCalisir(t *testing.T) {
	svc := &fakeOrders{detail: ornekDetay()}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodPost, "/admin/v1/orders/order_1/cancel", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "", svc.gotReason)
	assert.Equal(t, []string{"CancelOrder", "GetOrder"}, svc.calls,
		"iptalden sonra siparişin GÜNCEL hâli okunmalı")
}

// TestAdminIptalGerekceyiGecirir gövdedeki gerekçenin servise ulaştığını
// doğrular.
func TestAdminIptalGerekceyiGecirir(t *testing.T) {
	svc := &fakeOrders{detail: ornekDetay()}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodPost, "/admin/v1/orders/order_1/cancel",
		`{"reason":"ödeme reddedildi"}`)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ödeme reddedildi", svc.gotReason)
}

// TestAdminIptalBilinmeyenAlaniReddeder sessizce yutulan alan olmadığını
// doğrular.
func TestAdminIptalBilinmeyenAlaniReddeder(t *testing.T) {
	svc := &fakeOrders{detail: ornekDetay()}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodPost, "/admin/v1/orders/order_1/cancel",
		`{"sebep":"yazım hatası"}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Empty(t, svc.calls)
}

// TestAdminTamamlaVeArsivle geçiş uçlarının servise gittiğini doğrular.
func TestAdminTamamlaVeArsivle(t *testing.T) {
	testler := map[string]struct {
		yol   string
		cagri string
	}{
		"tamamla": {yol: "/admin/v1/orders/order_1/complete", cagri: "CompleteOrder"},
		"arşivle": {yol: "/admin/v1/orders/order_1/archive", cagri: "ArchiveOrder"},
	}

	for ad, tc := range testler {
		t.Run(ad, func(t *testing.T) {
			svc := &fakeOrders{detail: ornekDetay()}
			r := yeniRouter(svc)

			rec := istek(t, r, http.MethodPost, tc.yol, "")

			require.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, []string{tc.cagri, "GetOrder"}, svc.calls)
		})
	}
}

// TestHataSiniflariStatusKodunaCevrilir handler'ın status kodu SEÇMEDİĞİNİ,
// core/errors sınıfının eşlendiğini doğrular (plan Bölüm 8).
func TestHataSiniflariStatusKodunaCevrilir(t *testing.T) {
	testler := map[string]struct {
		err    error
		status int
	}{
		"bulunamadı": {err: errors.NotFound("order_not_found", "yok"), status: http.StatusNotFound},
		"çakışma":    {err: errors.Conflict("order_not_pending", "olmaz"), status: http.StatusConflict},
		"geçersiz":   {err: errors.Invalid("order_invalid_input", "hatalı"), status: http.StatusUnprocessableEntity},
		"iç hata":    {err: errors.Internal("order_query_failed", "patladı"), status: http.StatusInternalServerError},
	}

	for ad, tc := range testler {
		t.Run(ad, func(t *testing.T) {
			svc := &fakeOrders{err: tc.err}
			r := yeniRouter(svc)

			rec := istek(t, r, http.MethodGet, "/admin/v1/orders/order_1", "")

			assert.Equal(t, tc.status, rec.Code)
			govde := coz(t, rec)
			_, ok := govde["error"].(map[string]any)
			assert.True(t, ok, "hata gövdesi error zarfıyla dönmeli")
		})
	}
}

// TestAdminIadeOlusturma iade ucunun gövdesini ve status kodunu doğrular.
func TestAdminIadeOlusturma(t *testing.T) {
	svc := &fakeOrders{ret: models.Return{
		ID: "ret_1", OrderID: "order_1", Status: models.ReturnRequested, RefundAmount: 3600,
	}}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodPost, "/admin/v1/orders/order_1/returns",
		`{"refund_amount":3600,"reason":"beden uymadı","note":"","metadata":{"kanal":"destek"}}`)

	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "order_1", svc.returnInput.OrderID)
	assert.Equal(t, int64(3600), svc.returnInput.RefundAmount)
	assert.Equal(t, "beden uymadı", svc.returnInput.Reason)
	assert.Equal(t, map[string]any{"kanal": "destek"}, svc.returnInput.Metadata)

	veri, ok := coz(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "ret_1", veri["id"])
	assert.Equal(t, "requested", veri["status"])
}

// TestAdminIadeListesi liste zarfını doğrular.
func TestAdminIadeListesi(t *testing.T) {
	svc := &fakeOrders{
		returns: []models.Return{{ID: "ret_1", OrderID: "order_1", Status: models.ReturnRequested}},
		count:   1,
	}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodGet, "/admin/v1/orders/order_1/returns?limit=10", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, int64(10), svc.page.Limit)
	govde := coz(t, rec)
	assert.Equal(t, float64(1), govde["count"])
}

// TestAdminSatisSonrasiTekilOkuma üç tekil okuma ucunun kaydı kimliğiyle
// getirdiğini doğrular.
//
// Yoldaki sipariş kimliği kaydın SAHİBİNİ doğrulamaz; servise yalnızca kaydın
// kendi kimliği gider (bkz. [api.Handler.adminGetReturn] gerekçesi).
func TestAdminSatisSonrasiTekilOkuma(t *testing.T) {
	testler := map[string]struct {
		yol    string
		cagri  string
		kimlik string
		alan   string
		deger  any
	}{
		"iade": {
			yol: "/admin/v1/orders/order_1/returns/ret_1", cagri: "GetReturn",
			kimlik: "ret_1", alan: "status", deger: "requested",
		},
		"değişim": {
			yol: "/admin/v1/orders/order_1/exchanges/exch_1", cagri: "GetExchange",
			kimlik: "exch_1", alan: "difference_due", deger: float64(-500),
		},
		"hasar": {
			yol: "/admin/v1/orders/order_1/claims/claim_1", cagri: "GetClaim",
			kimlik: "claim_1", alan: "type", deger: "refund",
		},
	}

	for ad, tc := range testler {
		t.Run(ad, func(t *testing.T) {
			svc := &fakeOrders{
				ret: models.Return{
					ID: "ret_1", OrderID: "order_1", Status: models.ReturnRequested,
				},
				exchange: models.Exchange{
					ID: "exch_1", OrderID: "order_1",
					Status: models.ExchangeRequested, DifferenceDue: -500,
				},
				claim: models.Claim{
					ID: "claim_1", OrderID: "order_1", Type: models.ClaimRefund,
					Status: models.ClaimRequested,
				},
			}
			r := yeniRouter(svc)

			rec := istek(t, r, http.MethodGet, tc.yol, "")

			require.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, []string{tc.cagri}, svc.calls)
			assert.Equal(t, tc.kimlik, svc.gotChildID)

			veri, ok := coz(t, rec)["data"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, tc.kimlik, veri["id"])
			assert.Equal(t, tc.deger, veri[tc.alan])
		})
	}
}

// TestAdminSatisSonrasiTekilOkumaBulunamadi eksik kaydın 404 döndüğünü
// doğrular.
func TestAdminSatisSonrasiTekilOkumaBulunamadi(t *testing.T) {
	svc := &fakeOrders{err: errors.NotFound("order_return_not_found", "iade kaydı bulunamadı")}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodGet, "/admin/v1/orders/order_1/returns/ret_YOK", "")

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestAdminDegisimOlusturma değişim ucunda NEGATİF farkın taşındığını doğrular.
func TestAdminDegisimOlusturma(t *testing.T) {
	svc := &fakeOrders{exchange: models.Exchange{
		ID: "exch_1", OrderID: "order_1", Status: models.ExchangeRequested, DifferenceDue: -500,
	}}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodPost, "/admin/v1/orders/order_1/exchanges",
		`{"difference_due":-500,"note":"ucuz modelle"}`)

	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, int64(-500), svc.exchangeInput.DifferenceDue)

	veri, ok := coz(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(-500), veri["difference_due"])
}

// TestAdminHasarOlusturmaTuruZorunlu tür alanının boş bırakılamadığını
// doğrular.
//
// Varsayılan bir tür seçmek, talebin nasıl karşılanacağına istemci adına karar
// vermek olurdu.
func TestAdminHasarOlusturmaTuruZorunlu(t *testing.T) {
	svc := &fakeOrders{}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodPost, "/admin/v1/orders/order_1/claims",
		`{"refund_amount":100}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Empty(t, svc.calls, "tür verilmeden servise gidilmemeli")
}

// TestAdminHasarOlusturma hasar ucunun gövdesini taşıdığını doğrular.
func TestAdminHasarOlusturma(t *testing.T) {
	svc := &fakeOrders{claim: models.Claim{
		ID: "claim_1", OrderID: "order_1", Type: models.ClaimRefund,
		Status: models.ClaimRequested, RefundAmount: 1200,
	}}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodPost, "/admin/v1/orders/order_1/claims",
		`{"type":"refund","refund_amount":1200,"reason":"kırık geldi"}`)

	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, models.ClaimRefund, svc.claimInput.Type)
	assert.Equal(t, int64(1200), svc.claimInput.RefundAmount)

	veri, ok := coz(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "refund", veri["type"])
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
// Buradaki uç bir sipariş İPTALİDİR ve geri alınamaz: ödemesi alınmış bir
// sipariş kapanır. Yetki denetimi olmasaydı kimlik doğrulama tek başına
// yetkilendirme yerine geçerdi ve raporlama için verilmiş bir kimlik siparişi
// iptal edebilirdi.
func TestDarYetkiliKimlikYazmaUcunda403Alir(t *testing.T) {
	svc := &fakeOrders{}
	r := yeniRouter(svc)

	rec := kimlikliIstek(t, r, http.MethodPost, "/admin/v1/orders/order_1/cancel", "", darYetkili())

	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Empty(t, svc.calls, "yetki yetmiyorsa servise hiç gidilmemeli")

	hata, ok := coz(t, rec)["error"].(map[string]any)
	require.True(t, ok, "hata zarfı bekleniyordu: %s", rec.Body.String())
	assert.Equal(t, corehttp.CodeForbidden, hata["code"])
}

// TestDarYetkiliKimlikOkumaUcundaGecer aynı kimliğin okuma ucunda geçtiğini
// doğrular.
//
// Çift eşlik eden test budur: 403 dönen bir uç, yetki haritasının fazla dar
// olmasından da kaynaklanabilirdi. Aynı kimliğin okumada geçmesi, reddin
// yetki AYRIMINDAN geldiğini gösterir.
func TestDarYetkiliKimlikOkumaUcundaGecer(t *testing.T) {
	svc := &fakeOrders{
		orders: []models.Order{{ID: "order_1", Status: models.OrderPending}},
		count:  1,
	}
	r := yeniRouter(svc)

	rec := kimlikliIstek(t, r, http.MethodGet, "/admin/v1/orders", "", darYetkili())

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{"ListOrders"}, svc.calls)
}

// TestYetkisizKimlikYonetimUcunuAcamaz yetkileri BOŞ bırakılmış bir yönetim
// kullanıcısının hiçbir yönetim ucuna erişemediğini doğrular.
//
// Kimlik geçerlidir — giriş yapabilir, kim olduğu bilinir — ama yetkisi
// yoktur. Bu ayrım olmasaydı yetki listesi boş bırakılan bir kullanıcı
// "hiçbir şeye erişemez" sanılırken siparişleri okuyup kapatabilirdi.
func TestYetkisizKimlikYonetimUcunuAcamaz(t *testing.T) {
	svc := &fakeOrders{}
	yetkisiz := corehttp.Principal{ID: "user_bos", Kind: "user", Scopes: []string{}}
	r := yeniRouter(svc)

	for _, tc := range []struct {
		ad     string
		method string
		yol    string
	}{
		{ad: "okuma", method: http.MethodGet, yol: "/admin/v1/orders"},
		{ad: "yazma", method: http.MethodPost, yol: "/admin/v1/orders/order_1/complete"},
	} {
		t.Run(tc.ad, func(t *testing.T) {
			rec := kimlikliIstek(t, r, tc.method, tc.yol, "", yetkisiz)

			assert.Equal(t, http.StatusForbidden, rec.Code)
		})
	}
	assert.Empty(t, svc.calls, "yetkisiz kimlik için servise hiç gidilmemeli")
}

// TestKimliksizIstek401Alir kimliği hiç olmayan isteğin 403 DEĞİL 401
// aldığını doğrular.
//
// Ayrım istemci için anlamlıdır: 401 "kim olduğunu söyle" (kimlikle tekrar
// dene), 403 "kim olduğunu biliyorum ama yetkin yok" (tekrar denemenin
// anlamı yok) demektir.
func TestKimliksizIstek401Alir(t *testing.T) {
	svc := &fakeOrders{}
	r := yeniRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/orders", strings.NewReader(""))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Empty(t, svc.calls)
}

// TestMagazaUcuYetkiIstemez mağaza ucunun yetki taşımayan bir kimlikle
// çalıştığını doğrular.
//
// /store/v1'in kimliği publishable anahtardır ve o anahtar tanımı gereği yetki
// TAŞIMAZ. Yönetim yetkisi eklerken mağaza ucunu da kapatmak, tüm vitrini
// düşürürdü.
func TestMagazaUcuYetkiIstemez(t *testing.T) {
	svc := &fakeOrders{detail: models.OrderDetail{Order: models.Order{ID: "order_1"}}}
	magaza := corehttp.Principal{ID: "pk_1", Kind: "api_key", Scopes: []string{}}
	r := yeniRouter(svc)

	rec := kimlikliIstek(t, r, http.MethodGet, "/store/v1/orders/order_1", "", magaza)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{"GetOrder"}, svc.calls)
}
