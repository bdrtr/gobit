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
	"github.com/bdrtr/gobit/internal/modules/cart/api"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// fakeCarts api.Carts'ın test karşılığıdır.
//
// HTTP katmanının sorumluluğu dardır: gövdeyi çöz, servisi çağır, zarfı ve
// status kodunu yaz. Sahte servis bu yüzden iş mantığı taşımaz; yalnızca
// kaydedilen çağrıyı ve önceden ayarlanmış yanıtı döner.
type fakeCarts struct {
	cart   models.Cart
	detail models.CartDetail
	item   models.LineItem
	addr   models.CartAddress
	method models.ShippingMethod
	carts  []models.Cart
	count  int64

	// err ayarlanırsa tüm metotlar bu hatayı döner; hata eşlemesini sınamak
	// için kullanılır.
	err error

	// Son çağrının argümanları.
	createInput   service.CreateCartInput
	updateInput   service.UpdateCartInput
	addInput      service.AddLineItemInput
	addressInput  service.AddressInput
	shippingInput service.AddShippingMethodInput
	listInput     service.ListCartsInput
	gotCartID     string
	gotLineID     string
	gotMethodID   string
	gotQuantity   int64
	// billing, son adresi yazan çağrının fatura ucundan gelip gelmediğini bildirir.
	billing bool
}

// Sahtenin handler'ın beklediği yüzeyi karşıladığı derleme zamanında doğrulanır.
var _ api.Carts = (*fakeCarts)(nil)

// CreateCart sepeti döner.
func (f *fakeCarts) CreateCart(_ context.Context, in service.CreateCartInput) (models.Cart, error) {
	f.createInput = in
	return f.cart, f.err
}

// GetCart sepeti çocuklarıyla döner.
func (f *fakeCarts) GetCart(_ context.Context, cartID string) (models.CartDetail, error) {
	f.gotCartID = cartID
	return f.detail, f.err
}

// UpdateCart güncellenen sepeti döner.
func (f *fakeCarts) UpdateCart(_ context.Context, cartID string, in service.UpdateCartInput) (models.Cart, error) {
	f.gotCartID = cartID
	f.updateInput = in
	return f.cart, f.err
}

// ListCarts sepetleri döner.
func (f *fakeCarts) ListCarts(_ context.Context, in service.ListCartsInput) ([]models.Cart, int64, error) {
	f.listInput = in
	return f.carts, f.count, f.err
}

// DeleteCart sepeti siler.
func (f *fakeCarts) DeleteCart(_ context.Context, cartID string) error {
	f.gotCartID = cartID
	return f.err
}

// AddLineItem satır ekler.
func (f *fakeCarts) AddLineItem(_ context.Context, cartID string, in service.AddLineItemInput) (models.LineItem, error) {
	f.gotCartID, f.addInput = cartID, in
	return f.item, f.err
}

// UpdateLineItemQuantity adedi yazar.
func (f *fakeCarts) UpdateLineItemQuantity(_ context.Context, cartID, lineID string, quantity int64) (models.LineItem, error) {
	f.gotCartID, f.gotLineID, f.gotQuantity = cartID, lineID, quantity
	return f.item, f.err
}

// RemoveLineItem satırı kaldırır.
func (f *fakeCarts) RemoveLineItem(_ context.Context, cartID, lineID string) error {
	f.gotCartID, f.gotLineID = cartID, lineID
	return f.err
}

// SetShippingAddress kargo adresini yazar.
func (f *fakeCarts) SetShippingAddress(_ context.Context, cartID string, in service.AddressInput) (models.CartAddress, error) {
	f.gotCartID, f.addressInput, f.billing = cartID, in, false
	return f.addr, f.err
}

// SetBillingAddress fatura adresini yazar.
func (f *fakeCarts) SetBillingAddress(_ context.Context, cartID string, in service.AddressInput) (models.CartAddress, error) {
	f.gotCartID, f.addressInput, f.billing = cartID, in, true
	return f.addr, f.err
}

// AddShippingMethod kargo yöntemi ekler.
func (f *fakeCarts) AddShippingMethod(_ context.Context, cartID string, in service.AddShippingMethodInput) (models.ShippingMethod, error) {
	f.gotCartID, f.shippingInput = cartID, in
	return f.method, f.err
}

// RemoveShippingMethod kargo yöntemini kaldırır.
func (f *fakeCarts) RemoveShippingMethod(_ context.Context, cartID, methodID string) error {
	f.gotCartID, f.gotMethodID = cartID, methodID
	return f.err
}

// yeniSunucu sahte servise bağlı bir router kurar.
func yeniSunucu(t *testing.T, svc *fakeCarts) http.Handler {
	t.Helper()

	r := chi.NewRouter()
	api.New(svc).Routes(r)
	return r
}

// istek verilen isteği router'a gönderir ve yanıtı döner.
func istek(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// govde yanıt gövdesini haritaya çözer.
func govde(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), "yanıt JSON olmalı: %s", rec.Body.String())
	return out
}

// nesne bir JSON değerini nesne olarak okur; değilse testi düşürür.
func nesne(t *testing.T, value any) map[string]any {
	t.Helper()

	out, ok := value.(map[string]any)
	require.True(t, ok, "JSON nesnesi bekleniyordu, gelen: %T", value)
	return out
}

// dizi bir JSON değerini dizi olarak okur; değilse testi düşürür.
func dizi(t *testing.T, value any) []any {
	t.Helper()

	out, ok := value.([]any)
	require.True(t, ok, "JSON dizisi bekleniyordu, gelen: %T", value)
	return out
}

// TestCreateCart201VeTekilZarf sepet oluşturmanın 201 ve tekil zarf
// döndürdüğünü doğrular.
func TestCreateCart201VeTekilZarf(t *testing.T) {
	svc := &fakeCarts{cart: models.Cart{
		ID: "cart_1", RegionID: "reg_1", CurrencyCode: "TRY",
		CreatedAt: time.Unix(0, 0).UTC(), UpdatedAt: time.Unix(0, 0).UTC(),
	}}
	h := yeniSunucu(t, svc)

	rec := istek(t, h, http.MethodPost, "/store/v1/carts",
		`{"region_id":"reg_1","currency_code":"TRY","customer_id":"cust_1","email":"a@b.c"}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	data := nesne(t, govde(t, rec)["data"])
	assert.Equal(t, "cart_1", data["id"])
	assert.Equal(t, false, data["totals_stale"], "bayatlık toplamlarla birlikte sunulmalı")

	assert.Equal(t, "reg_1", svc.createInput.RegionID)
	assert.Equal(t, "cust_1", svc.createInput.CustomerID)
	assert.Equal(t, "a@b.c", svc.createInput.Email)
}

// TestUpdateCartAlanlariServiseGecirir güncelleme gövdesinin servise olduğu
// gibi geçtiğini doğrular.
//
// Özellikle e-postanın İŞARETÇİ olarak taşınması sınanır: gövdede alan hiç
// yoksa servise nil gider ve saklı e-posta korunur; gövdede boş dize varsa
// temizleme niyeti servise ulaşır.
func TestUpdateCartAlanlariServiseGecirir(t *testing.T) {
	svc := &fakeCarts{cart: models.Cart{
		ID: "cart_1", RegionID: "reg_1", CurrencyCode: "TRY", CustomerID: "cust_1",
	}}
	h := yeniSunucu(t, svc)

	rec := istek(t, h, http.MethodPost, "/store/v1/carts/cart_1",
		`{"email":"a@b.c","customer_id":"cust_1"}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "cart_1", svc.gotCartID)
	require.NotNil(t, svc.updateInput.Email)
	assert.Equal(t, "a@b.c", *svc.updateInput.Email)
	assert.Equal(t, "cust_1", svc.updateInput.CustomerID)
	assert.Equal(t, "cart_1", nesne(t, govde(t, rec)["data"])["id"])

	// Gönderilmeyen e-posta ile boşaltılmak istenen e-posta ayrı niyetlerdir.
	rec = istek(t, h, http.MethodPost, "/store/v1/carts/cart_1", `{"customer_id":"cust_1"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Nil(t, svc.updateInput.Email, "gönderilmeyen alan servise nil gitmeli")

	rec = istek(t, h, http.MethodPost, "/store/v1/carts/cart_1", `{"email":""}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotNil(t, svc.updateInput.Email, "boş dize temizleme niyetidir")
	assert.Empty(t, *svc.updateInput.Email)
}

// TestGetCartCocuklariyleDoner sepet ayrıntısının satır, adresi ve kargo
// yöntemini birlikte döndürdüğünü doğrular.
func TestGetCartCocuklariyleDoner(t *testing.T) {
	svc := &fakeCarts{detail: models.CartDetail{
		Cart:  models.Cart{ID: "cart_1", RegionID: "reg_1", CurrencyCode: "TRY", Revision: 2, TotalsRevision: 1},
		Items: []models.LineItem{{ID: "li_1", CartID: "cart_1", VariantID: "var_1", Title: "Tişört", Quantity: 2}},
		ShippingAddress: &models.CartAddress{
			ID: "addr_1", CartID: "cart_1", Type: models.AddressShipping, City: "İstanbul",
		},
		ShippingMethods: []models.ShippingMethod{{ID: "csm_1", CartID: "cart_1", Name: "Standart", Amount: 2500}},
	}}
	h := yeniSunucu(t, svc)

	rec := istek(t, h, http.MethodGet, "/store/v1/carts/cart_1", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "cart_1", svc.gotCartID)

	data := nesne(t, govde(t, rec)["data"])
	assert.Equal(t, true, data["totals_stale"], "bayat toplam yanıtta görünmeli")
	items := dizi(t, data["items"])
	require.Len(t, items, 1)
	assert.Equal(t, "li_1", nesne(t, items[0])["id"])
	assert.Equal(t, "İstanbul", nesne(t, data["shipping_address"])["city"])
	assert.Equal(t, "shipping", nesne(t, data["shipping_address"])["type"])
	assert.Nil(t, data["billing_address"], "olmayan kayıt yanıtta yer almamalı")
	methods := dizi(t, data["shipping_methods"])
	require.Len(t, methods, 1)
	assert.InDelta(t, 2500, nesne(t, methods[0])["amount"], 0.0)
}

// TestBosSepetteCocukAlanlariDizidir çocuğu olmayan sepette dizilerin null
// DEĞİL boş dizi döndüğünü doğrular.
//
// null dönseydi istemcilerin her yerde nil kontrolü yapması gerekirdi.
func TestBosSepetteCocukAlanlariDizidir(t *testing.T) {
	svc := &fakeCarts{detail: models.CartDetail{Cart: models.Cart{ID: "cart_1"}}}
	h := yeniSunucu(t, svc)

	rec := istek(t, h, http.MethodGet, "/store/v1/carts/cart_1", "")

	require.Equal(t, http.StatusOK, rec.Code)
	data := nesne(t, govde(t, rec)["data"])
	assert.Equal(t, []any{}, data["items"])
	assert.Equal(t, []any{}, data["shipping_methods"])
}

// TestAddLineItem201 satır eklemenin 201 döndürdüğünü ve girdiyi servise
// aktardığını doğrular.
func TestAddLineItem201(t *testing.T) {
	svc := &fakeCarts{item: models.LineItem{ID: "li_1", CartID: "cart_1", VariantID: "var_1", Quantity: 3}}
	h := yeniSunucu(t, svc)

	rec := istek(t, h, http.MethodPost, "/store/v1/carts/cart_1/line-items",
		`{"variant_id":"var_1","title":"Tişört","quantity":3,"unit_price":1000}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, "cart_1", svc.gotCartID)
	assert.Equal(t, "var_1", svc.addInput.VariantID)
	assert.Equal(t, int64(3), svc.addInput.Quantity)
	assert.Equal(t, int64(1000), svc.addInput.UnitPrice)
}

// TestAddLineItemAdetZorunlu gövdede adet yoksa isteğin reddedildiğini
// doğrular.
//
// Adet işaretçi olmasaydı, alanı hiç göndermeyen bir istemci "sıfır adet"
// göndermiş sayılırdı.
func TestAddLineItemAdetZorunlu(t *testing.T) {
	svc := &fakeCarts{}
	h := yeniSunucu(t, svc)

	rec := istek(t, h, http.MethodPost, "/store/v1/carts/cart_1/line-items",
		`{"variant_id":"var_1","title":"Tişört"}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Empty(t, svc.addInput.VariantID, "servis çağrılmamalı")
}

// TestUpdateLineItemAdetYazar adet güncellemenin doğru yola ve parametrelere
// bağlandığını doğrular.
func TestUpdateLineItemAdetYazar(t *testing.T) {
	svc := &fakeCarts{item: models.LineItem{ID: "li_1", Quantity: 5}}
	h := yeniSunucu(t, svc)

	rec := istek(t, h, http.MethodPatch, "/store/v1/carts/cart_1/line-items/li_1", `{"quantity":5}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "cart_1", svc.gotCartID)
	assert.Equal(t, "li_1", svc.gotLineID)
	assert.Equal(t, int64(5), svc.gotQuantity)
}

// TestRemoveLineItem204 satır kaldırmanın gövdesiz 204 döndürdüğünü doğrular.
func TestRemoveLineItem204(t *testing.T) {
	svc := &fakeCarts{}
	h := yeniSunucu(t, svc)

	rec := istek(t, h, http.MethodDelete, "/store/v1/carts/cart_1/line-items/li_1", "")

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String())
	assert.Equal(t, "li_1", svc.gotLineID)
}

// TestAdresUclariAyriDusler kargo ve fatura uçlarının AYRI servis metodlarına
// gittiğini doğrular.
//
// İkisi aynı metoda bağlansaydı, fatura adresi kargo adresinin üzerine yazardı.
func TestAdresUclariAyriDusler(t *testing.T) {
	svc := &fakeCarts{addr: models.CartAddress{ID: "addr_1", Type: models.AddressBilling}}
	h := yeniSunucu(t, svc)

	rec := istek(t, h, http.MethodPut, "/store/v1/carts/cart_1/shipping-address", `{"city":"İstanbul"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.False(t, svc.billing, "kargo ucu SetShippingAddress çağırmalı")
	assert.Equal(t, "İstanbul", svc.addressInput.City)

	rec = istek(t, h, http.MethodPut, "/store/v1/carts/cart_1/billing-address", `{"city":"Ankara"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.True(t, svc.billing, "fatura ucu SetBillingAddress çağırmalı")
	assert.Equal(t, "Ankara", svc.addressInput.City)
}

// TestKargoYontemiUclari kargo yöntemi ekleme ve kaldırmanın doğru
// parametrelerle çalıştığını doğrular.
func TestKargoYontemiUclari(t *testing.T) {
	svc := &fakeCarts{method: models.ShippingMethod{ID: "csm_1", Name: "Standart", Amount: 2500}}
	h := yeniSunucu(t, svc)

	rec := istek(t, h, http.MethodPost, "/store/v1/carts/cart_1/shipping-methods",
		`{"name":"Standart","amount":2500,"shipping_option_id":"so_1"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, "Standart", svc.shippingInput.Name)
	assert.Equal(t, int64(2500), svc.shippingInput.Amount)
	assert.Equal(t, "so_1", svc.shippingInput.ShippingOptionID)

	rec = istek(t, h, http.MethodDelete, "/store/v1/carts/cart_1/shipping-methods/csm_1", "")
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "csm_1", svc.gotMethodID)
}

// TestAdminListeZarfi yönetim listesinin liste zarfını döndürdüğünü ve
// süzgeçleri aktardığını doğrular.
func TestAdminListeZarfi(t *testing.T) {
	svc := &fakeCarts{
		carts: []models.Cart{{ID: "cart_1"}, {ID: "cart_2"}},
		count: 42,
	}
	h := yeniSunucu(t, svc)

	rec := istek(t, h, http.MethodGet,
		"/admin/v1/carts?limit=2&offset=4&customer_id=cust_1&region_id=reg_1&completed=true", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := govde(t, rec)
	assert.Len(t, body["data"], 2)
	assert.InDelta(t, 42, body["count"], 0.0, "count sayfanın değil filtrenin sayısı olmalı")
	assert.InDelta(t, 4, body["offset"], 0.0)
	assert.InDelta(t, 2, body["limit"], 0.0)

	require.NotNil(t, svc.listInput.CustomerID)
	assert.Equal(t, "cust_1", *svc.listInput.CustomerID)
	require.NotNil(t, svc.listInput.RegionID)
	assert.Equal(t, "reg_1", *svc.listInput.RegionID)
	require.NotNil(t, svc.listInput.Completed)
	assert.True(t, *svc.listInput.Completed)
}

// TestAdminListeVarsayilanLimit limit verilmediğinde yanıtta GERÇEKTEN
// uygulanan sınırın göründüğünü doğrular.
func TestAdminListeVarsayilanLimit(t *testing.T) {
	svc := &fakeCarts{}
	h := yeniSunucu(t, svc)

	rec := istek(t, h, http.MethodGet, "/admin/v1/carts", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.InDelta(t, float64(service.DefaultLimit), govde(t, rec)["limit"], 0.0)
}

// TestAdminYazmaUcuYok yönetim tarafının sepeti DEĞİŞTİREMEDİĞİNİ doğrular.
//
// Sepeti değiştiren tek taraf müşteridir; yönetim panelinden yapılan bir
// düzeltme, müşterinin gördüğü tutarı arkasından değiştirmek olurdu.
func TestAdminYazmaUcuYok(t *testing.T) {
	h := yeniSunucu(t, &fakeCarts{})

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/admin/v1/carts"},
		{http.MethodPatch, "/admin/v1/carts/cart_1"},
		{http.MethodDelete, "/admin/v1/carts/cart_1"},
		{http.MethodPut, "/admin/v1/carts/cart_1"},
	} {
		rec := istek(t, h, tc.method, tc.path, `{}`)
		assert.Contains(t, []int{http.StatusNotFound, http.StatusMethodNotAllowed}, rec.Code,
			"%s %s bağlanmamalı", tc.method, tc.path)
	}
}

// TestToplamUclariHTTPyeAcilmaz toplam yazma ve sepet tamamlama uçlarının
// BULUNMADIĞINI doğrular.
//
// Açık olsalardı bir istemci sepetin tutarını kendi yazabilir ya da ödeme
// yapmadan sepeti kapatabilirdi; ikisi de workflow yüzeyidir (ADR 0006).
func TestToplamUclariHTTPyeAcilmaz(t *testing.T) {
	h := yeniSunucu(t, &fakeCarts{})

	for _, path := range []string{
		"/store/v1/carts/cart_1/totals",
		"/store/v1/carts/cart_1/complete",
		"/admin/v1/carts/cart_1/totals",
		"/admin/v1/carts/cart_1/complete",
	} {
		rec := istek(t, h, http.MethodPost, path, `{}`)
		assert.Contains(t, []int{http.StatusNotFound, http.StatusMethodNotAllowed}, rec.Code,
			"%s bağlanmamalı", path)
	}
}

// TestHataSinifiStatusKoduna servis hatalarının sınıfına uygun status koduna
// çevrildiğini doğrular (plan Bölüm 8).
//
// Handler status kodu SEÇMEZ; eşleme corehttp.WriteError'dadır ve bu test o
// zincirin gerçekten kurulu olduğunu gösterir.
func TestHataSinifiStatusKoduna(t *testing.T) {
	testler := map[string]struct {
		err    error
		status int
	}{
		"bulunamadı": {errors.NotFound("cart_not_found", "sepet yok"), http.StatusNotFound},
		"geçersiz":   {errors.Invalid("cart_invalid_input", "adet pozitif olmalı"), http.StatusUnprocessableEntity},
		"çakışma":    {errors.Conflict("cart_completed", "sepet tamamlanmış"), http.StatusConflict},
	}

	for ad, tc := range testler {
		t.Run(ad, func(t *testing.T) {
			svc := &fakeCarts{err: tc.err}
			h := yeniSunucu(t, svc)

			rec := istek(t, h, http.MethodGet, "/store/v1/carts/cart_1", "")

			require.Equal(t, tc.status, rec.Code, rec.Body.String())
			errBody := nesne(t, govde(t, rec)["error"])
			assert.Equal(t, errors.CodeOf(tc.err), errBody["code"])
			assert.NotEmpty(t, errBody["message"])
		})
	}
}

// TestIcHataAyrintiSizdirmaz sunucu hatasının alttaki mesajı istemciye
// yazmadığını doğrular.
func TestIcHataAyrintiSizdirmaz(t *testing.T) {
	svc := &fakeCarts{err: errors.Internal("cart_query_failed",
		"pq: relation \"carts\" does not exist (host=10.0.0.1)")}
	h := yeniSunucu(t, svc)

	rec := istek(t, h, http.MethodGet, "/store/v1/carts/cart_1", "")

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "10.0.0.1")
	assert.NotContains(t, rec.Body.String(), "relation")
}

// TestTanimsizAlanReddedilir gövdedeki bilinmeyen alanın sessizce
// YUTULMADIĞINI doğrular.
//
// Yutulan bir alan, istemcinin gönderdiğini sandığı ama uygulanmayan bir ayar
// demektir.
func TestTanimsizAlanReddedilir(t *testing.T) {
	svc := &fakeCarts{}
	h := yeniSunucu(t, svc)

	rec := istek(t, h, http.MethodPost, "/store/v1/carts",
		`{"region_id":"reg_1","currency_code":"TRY","indirim":"bedava"}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Empty(t, svc.createInput.RegionID, "servis çağrılmamalı")
}

// TestBosGovdeReddedilir gövdesiz bir yazma isteğinin reddedildiğini doğrular.
func TestBosGovdeReddedilir(t *testing.T) {
	h := yeniSunucu(t, &fakeCarts{})

	rec := istek(t, h, http.MethodPost, "/store/v1/carts", "")

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestGecersizSayfalamaParametresi sayfalama parametresi sayı değilse isteğin
// reddedildiğini doğrular.
func TestGecersizSayfalamaParametresi(t *testing.T) {
	h := yeniSunucu(t, &fakeCarts{})

	rec := istek(t, h, http.MethodGet, "/admin/v1/carts?limit=cok", "")

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestGecersizCompletedParametresi completed süzgeci mantıksal değilse isteğin
// reddedildiğini doğrular.
func TestGecersizCompletedParametresi(t *testing.T) {
	h := yeniSunucu(t, &fakeCarts{})

	rec := istek(t, h, http.MethodGet, "/admin/v1/carts?completed=belki", "")

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}
