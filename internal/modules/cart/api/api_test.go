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

// fakeRegions api.RegionCurrencyReader'ın test karşılığıdır.
//
// Sahte, bölgenin para birimini SABİT döner ve sorulan kimliği kaydeder:
// testin sınadığı şey handler'ın para birimini gövdeden değil BURADAN
// alması, ve sorduğu bölgenin gövdedeki bölge olmasıdır.
type fakeRegions struct {
	code   string
	digits int32
	err    error

	// gotRegionID son çağrının bölge kimliğidir.
	gotRegionID string
	calls       int
}

// Sahtenin handler'ın beklediği yüzeyi karşıladığı derleme zamanında doğrulanır.
var _ api.RegionCurrencyReader = (*fakeRegions)(nil)

// RegionCurrency bölgenin para birimini döner ve argümanı kaydeder.
func (f *fakeRegions) RegionCurrency(
	_ context.Context,
	regionID string,
) (code string, decimalDigits int32, err error) {
	f.calls++
	f.gotRegionID = regionID
	return f.code, f.digits, f.err
}

// fakePricing api.LinePricing'in test karşılığıdır.
//
// Sahte hiçbir fiyat HESAPLAMAZ; testin sınadığı şey handler'ın fiyatı KENDİ
// belirlemediği, akışa bıraktığıdır. Kaydedilen argümanlar tam olarak bunu
// görünür kılar: gövdede fiyat alanı olmadığı için akışa geçirilecek bir fiyat
// da yoktur.
type fakePricing struct {
	lineID  string
	removed bool
	err     error

	// Son çağrının argümanları.
	gotCartID    string
	gotVariantID string
	gotLineID    string
	gotQuantity  int64
	gotMetadata  json.RawMessage
	calls        int
}

// Sahtenin handler'ın beklediği yüzeyi karşıladığı derleme zamanında doğrulanır.
var _ api.LinePricing = (*fakePricing)(nil)

// AddPricedLineItem satırın kimliğini döner ve argümanları kaydeder.
func (f *fakePricing) AddPricedLineItem(
	_ context.Context,
	cartID, variantID string,
	quantity int64,
	metadata json.RawMessage,
) (string, error) {
	f.calls++
	f.gotCartID, f.gotVariantID, f.gotQuantity, f.gotMetadata = cartID, variantID, quantity, metadata
	return f.lineID, f.err
}

// SetLineItemQuantity adedi kaydeder ve satırın kaldırılıp kaldırılmadığını
// döner.
func (f *fakePricing) SetLineItemQuantity(
	_ context.Context,
	cartID, lineItemID string,
	quantity int64,
) (bool, error) {
	f.calls++
	f.gotCartID, f.gotLineID, f.gotQuantity = cartID, lineItemID, quantity
	return f.removed, f.err
}

// fakeCheckout api.CartCompletion'ın test karşılığıdır.
type fakeCheckout struct {
	response json.RawMessage
	err      error

	// got akışa gönderilen ham istektir; e-postanın SEPETTEN geldiği ancak
	// buradan görülebilir.
	got   json.RawMessage
	calls int
}

// Sahtenin handler'ın beklediği yüzeyi karşıladığı derleme zamanında doğrulanır.
var _ api.CartCompletion = (*fakeCheckout)(nil)

// CompleteCartJSON isteği kaydeder ve betiklenen yanıtı döner.
func (f *fakeCheckout) CompleteCartJSON(_ context.Context, request json.RawMessage) (json.RawMessage, error) {
	f.calls++
	f.got = request
	return f.response, f.err
}

// varsayilanParaBirimi sahte bölgenin döndüğü para birimidir.
//
// Sepet gövdelerinde geçen koddan ("TRY") FARKLI seçilmiştir ve bu bilinçlidir:
// ikisi aynı olsaydı, para biriminin bölgeden mi yoksa hâlâ istemciden mi
// geldiği hiçbir iddiada ayırt edilemezdi.
const varsayilanParaBirimi = "EUR"

// yeniSunucu sahte servise, sahte bölgeye ve BOŞ sahte akışlara bağlı bir
// router kurar.
//
// Bölgeye ve akışlara dokunmayan testler için yeterlidir; argümanlarını ya da
// yokluğunu sınayan testler [yeniBagimliSunucu] ile kendi sahtelerini verir.
func yeniSunucu(t *testing.T, svc *fakeCarts) http.Handler {
	t.Helper()

	return yeniAkisliSunucu(t, svc, api.Flows{Pricing: &fakePricing{}, Checkout: &fakeCheckout{}})
}

// yeniAkisliSunucu verilen servis ve akışlarla, VARSAYILAN sahte bölgeyle bir
// router kurar.
func yeniAkisliSunucu(t *testing.T, svc *fakeCarts, akislar api.Flows) http.Handler {
	t.Helper()

	return yeniBagimliSunucu(t, svc, &fakeRegions{code: varsayilanParaBirimi, digits: 2}, akislar)
}

// yeniBagimliSunucu verilen servis, bölge yüzeyi ve akışlarla bir router kurar.
//
// bolgeler nil verilebilir; handler'ın bölge yüzeyi olmadan KAPALI arızalandığı
// ancak böyle sınanabilir.
func yeniBagimliSunucu(
	t *testing.T,
	svc *fakeCarts,
	bolgeler api.RegionCurrencyReader,
	akislar api.Flows,
) http.Handler {
	t.Helper()

	r := chi.NewRouter()
	api.New(svc, bolgeler, akislar).Routes(r)
	return r
}

// adminKimlik testlerin varsayılan çağıranıdır: tam yetkili yönetim kimliği.
var adminKimlik = corehttp.Principal{
	ID:     "user_test",
	Kind:   "user",
	Scopes: []string{corehttp.ScopeAdmin},
}

// istek verilen isteği TAM YETKİLİ bir kimlikle router'a gönderir.
//
// Kimliğin context'e konması, yönetim uçları corehttp.RequireScope ile
// korunduğu için gereklidir: o middleware kimliği context'ten okur ve kimliği
// oraya koyan corehttp.RequireAdmin bu testte YOKTUR (router doğrudan
// kurulur). Kimlik eklenmeseydi bu dosyadaki her yönetim testi, sınadığı
// davranışa hiç ulaşamadan 401 alırdı. Testlerin ne doğruladığı değişmedi;
// yalnızca çağıranın kim olduğu belirtildi.
func istek(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return istekGonder(t, h, &adminKimlik, method, path, body)
}

// istekGonder isteği verilen kimlikle çalıştırır; kimlik nil ise istek
// KİMLİKSİZ gider.
func istekGonder(t *testing.T, h http.Handler, kimlik *corehttp.Principal, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if kimlik != nil {
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), *kimlik))
	}
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
		ID: "cart_1", RegionID: "reg_1", CurrencyCode: varsayilanParaBirimi,
		CreatedAt: time.Unix(0, 0).UTC(), UpdatedAt: time.Unix(0, 0).UTC(),
	}}
	h := yeniSunucu(t, svc)

	rec := istek(t, h, http.MethodPost, "/store/v1/carts",
		`{"region_id":"reg_1","customer_id":"cust_1","email":"a@b.c"}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	data := nesne(t, govde(t, rec)["data"])
	assert.Equal(t, "cart_1", data["id"])
	assert.Equal(t, false, data["totals_stale"], "bayatlık toplamlarla birlikte sunulmalı")

	assert.Equal(t, "reg_1", svc.createInput.RegionID)
	assert.Equal(t, "cust_1", svc.createInput.CustomerID)
	assert.Equal(t, "a@b.c", svc.createInput.Email)
}

// TestCreateCartParaBirimiBolgedenGelir sepetin para biriminin gövdeden değil
// BÖLGEDEN türetildiğini doğrular.
//
// İddia iki parçalıdır ve ikisi de gereklidir: handler bölgeye SORMALI (sorulan
// kimlik gövdedeki bölge olmalı) ve servise yazılan kod bölgenin cevabı olmalı.
// Yalnızca ikincisine bakmak yetmezdi — sabit bir varsayılan yazan bir handler
// da aynı iddiayı geçerdi.
func TestCreateCartParaBirimiBolgedenGelir(t *testing.T) {
	svc := &fakeCarts{cart: models.Cart{ID: "cart_1", RegionID: "reg_1"}}
	bolgeler := &fakeRegions{code: "JPY", digits: 0}
	h := yeniBagimliSunucu(t, svc, bolgeler,
		api.Flows{Pricing: &fakePricing{}, Checkout: &fakeCheckout{}})

	rec := istek(t, h, http.MethodPost, "/store/v1/carts", `{"region_id":"reg_1"}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, 1, bolgeler.calls, "para birimi bölgeye SORULMALI")
	assert.Equal(t, "reg_1", bolgeler.gotRegionID,
		"sorulan bölge gövdedeki bölge olmalı; başka bir bölgeye sorulsaydı sepet "+
			"kendi bölgesinin para biriminde olmazdı")
	assert.Equal(t, "JPY", svc.createInput.CurrencyCode,
		"sepete yazılan kod BÖLGENİN cevabı olmalı")
}

// TestCreateCartIstemciParaBirimiReddedilir vitrinin para birimi KABUL
// ETMEDİĞİNİ doğrular.
//
// İddia iki katmanlıdır: istek reddedilmeli VE hiçbir sepet yazılmamalı. Alan
// sessizce yok sayılıp sepet yine açılsaydı istemci gönderdiğini sanır, sunucu
// başka bir para birimi yazardı — ve satır, beklenenden başka bir fiyat
// listesinden fiyatlanırdı. Aynı ölçüt satır ucundaki unit_price için de
// uygulanmıştı.
func TestCreateCartIstemciParaBirimiReddedilir(t *testing.T) {
	svc := &fakeCarts{cart: models.Cart{ID: "cart_1", RegionID: "reg_1"}}
	bolgeler := &fakeRegions{code: varsayilanParaBirimi, digits: 2}
	h := yeniBagimliSunucu(t, svc, bolgeler,
		api.Flows{Pricing: &fakePricing{}, Checkout: &fakeCheckout{}})

	rec := istek(t, h, http.MethodPost, "/store/v1/carts",
		`{"region_id":"reg_1","currency_code":"USD"}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code,
		"tanınmayan alan reddedilmeli; gövde: %s", rec.Body.String())
	assert.Empty(t, svc.createInput.RegionID, "reddedilen istek sepet YAZMAMALI")
	assert.Zero(t, bolgeler.calls, "reddedilen istek bölgeye de sormamalı")
}

// TestCreateCartBolgeYuzeyiYoksaKapali bölge yüzeyi bağlı değilken sepetin HİÇ
// AÇILMADIĞINI doğrular.
//
// Kapalı arızalanma bilinçlidir ve gerekçesi fiyatlandırma akışınınkiyle
// aynıdır: bölge yüzeyi yoksa doğru cevap "para birimi yok" ya da "istemcinin
// dediğini kullan" değildir. Para birimi hangi fiyat listesinin uygulanacağını
// seçer; bir varsayılana düşmek, kapatılan yetki kapısını geri açardı.
func TestCreateCartBolgeYuzeyiYoksaKapali(t *testing.T) {
	svc := &fakeCarts{cart: models.Cart{ID: "cart_1", RegionID: "reg_1"}}
	h := yeniBagimliSunucu(t, svc, nil,
		api.Flows{Pricing: &fakePricing{}, Checkout: &fakeCheckout{}})

	rec := istek(t, h, http.MethodPost, "/store/v1/carts", `{"region_id":"reg_1"}`)

	assert.Equal(t, http.StatusInternalServerError, rec.Code,
		"kurulum arızası istemciye 5xx olmalı; gövde: %s", rec.Body.String())
	assert.Empty(t, svc.createInput.RegionID, "para birimi türetilemeden sepet YAZILMAMALI")
}

// TestCreateCartBilinmeyenBolgeSepetAcmaz bölgenin gerçekten var olduğunun
// artık DOĞRULANDIĞINI gösterir.
//
// Doğrulama bir yan kazançtır: cart servisi region'ı tanımadığı için (ADR 0006)
// bölgenin varlığını denetleyemez ve uydurma bir kimlik eskiden sepet
// açabiliyordu. Para birimi bölgeden okunduğu için o kapı da kapandı. Hata
// SINIFI korunur — region'ın errors.NotFound'u 404'e düşer ve istemciye
// düzeltebileceği bir şey söyler.
func TestCreateCartBilinmeyenBolgeSepetAcmaz(t *testing.T) {
	svc := &fakeCarts{cart: models.Cart{ID: "cart_1"}}
	bolgeler := &fakeRegions{err: errors.NotFound("region_not_found", "bölge bulunamadı")}
	h := yeniBagimliSunucu(t, svc, bolgeler,
		api.Flows{Pricing: &fakePricing{}, Checkout: &fakeCheckout{}})

	rec := istek(t, h, http.MethodPost, "/store/v1/carts", `{"region_id":"reg_yok"}`)

	assert.Equal(t, http.StatusNotFound, rec.Code, "gövde: %s", rec.Body.String())
	assert.Empty(t, svc.createInput.RegionID, "bilinmeyen bölgede sepet YAZILMAMALI")
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

// sepetliSatir sepet ayrıntısında tek bir satır taşıyan sahte servis üretir.
//
// Satırın tutarları DOLUDUR ve bilinçlidir: handler yanıtı akıştan dönen
// kimlikle sepetten OKUR, yani yanıttaki fiyat sepette yazılı olandır.
func sepetliSatir() *fakeCarts {
	return &fakeCarts{detail: models.CartDetail{
		Cart: models.Cart{ID: "cart_1", CurrencyCode: "TRY", Email: "a@b.c"},
		Items: []models.LineItem{{
			ID: "li_1", CartID: "cart_1", VariantID: "var_1", Title: "Tişört",
			Quantity: 3, UnitPrice: 1000, Subtotal: 3000, Total: 3000,
		}},
	}}
}

// TestAddLineItem201 satır eklemenin 201 döndürdüğünü ve isteği AKIŞA
// aktardığını doğrular.
//
// Handler'ın servisin AddLineItem'ını çağırmaması iddianın yarısıdır: satırı
// yazan taraf akıştır, çünkü fiyatı bilen tek taraf odur.
func TestAddLineItem201(t *testing.T) {
	svc := sepetliSatir()
	akis := &fakePricing{lineID: "li_1"}
	h := yeniAkisliSunucu(t, svc, api.Flows{Pricing: akis})

	rec := istek(t, h, http.MethodPost, "/store/v1/carts/cart_1/line-items",
		`{"variant_id":"var_1","quantity":3,"metadata":{"not":"hediye"}}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, 1, akis.calls, "satırı akış yazmalı")
	assert.Equal(t, "cart_1", akis.gotCartID)
	assert.Equal(t, "var_1", akis.gotVariantID)
	assert.Equal(t, int64(3), akis.gotQuantity)
	assert.JSONEq(t, `{"not":"hediye"}`, string(akis.gotMetadata),
		"metadata gerçekten istemcinin bilgisidir ve akışa taşınmalı")
	assert.Empty(t, svc.addInput.VariantID, "satır servise DOĞRUDAN yazılmamalı")

	// Yanıt sepetten okunur: gösterilen fiyat, sepette yazılı olandır.
	data := nesne(t, govde(t, rec)["data"])
	assert.Equal(t, "li_1", data["id"])
	assert.InDelta(t, 1000, data["unit_price"], 0.0)
}

// TestAddLineItemIstemciFiyatiniReddeder vitrinin fiyat ve başlık KABUL
// ETMEDİĞİNİ doğrular.
//
// Bulunan arıza tam olarak buydu: gövdedeki "unit_price" servise olduğu gibi
// yazılıyordu ve nihai fiyatı yazacağı söylenen workflow hiçbir kurulumda
// kablolanmamıştı. Vitrinin kimliği publishable anahtardır — yani bu, herkese
// açık bir "kendi fiyatını yaz" ucuydu.
//
// Alanların sessizce YOK SAYILMASI yetmezdi: eski bir istemci gönderdiğini
// sanır, sunucu başka bir fiyat yazardı. Gövde tanınmayan alanı REDDEDER.
func TestAddLineItemIstemciFiyatiniReddeder(t *testing.T) {
	for ad, govdeMetni := range map[string]string{
		"fiyat":   `{"variant_id":"var_1","quantity":3,"unit_price":1}`,
		"baslik":  `{"variant_id":"var_1","quantity":3,"title":"Bedava Tişört"}`,
		"ikisi":   `{"variant_id":"var_1","quantity":3,"title":"X","unit_price":1}`,
		"sifirla": `{"variant_id":"var_1","quantity":3,"unit_price":0}`,
	} {
		t.Run(ad, func(t *testing.T) {
			svc := sepetliSatir()
			akis := &fakePricing{lineID: "li_1"}
			h := yeniAkisliSunucu(t, svc, api.Flows{Pricing: akis})

			rec := istek(t, h, http.MethodPost, "/store/v1/carts/cart_1/line-items", govdeMetni)

			assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
			assert.Zero(t, akis.calls, "reddedilen istek akışa hiç ulaşmamalı")
			assert.Empty(t, svc.addInput.VariantID, "servise satır yazılmamalı")
		})
	}
}

// TestAddLineItemFiyatlandiriciYokkenSatirEklenmez fiyat yolunun KAPALI
// arızalandığını doğrular.
//
// Bu, b2b harcama kuralının tersidir ve fark bilinçlidir: b2b kurulu değilse
// "limit yok" doğru cevaptır, ama fiyatlandırıcı yoksa "fiyat yok" satırı
// yazmak sessizce bedava mal satmaktır. Tek doğru sonuç, satırın HİÇ
// eklenmemesidir.
func TestAddLineItemFiyatlandiriciYokkenSatirEklenmez(t *testing.T) {
	svc := sepetliSatir()
	h := yeniAkisliSunucu(t, svc, api.Flows{})

	rec := istek(t, h, http.MethodPost, "/store/v1/carts/cart_1/line-items",
		`{"variant_id":"var_1","quantity":3}`)

	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	assert.Empty(t, svc.addInput.VariantID, "fiyatı bilinmeyen satır sepete yazılmamalı")
}

// TestAddLineItemAdetZorunlu gövdede adet yoksa isteğin reddedildiğini
// doğrular.
//
// Adet işaretçi olmasaydı, alanı hiç göndermeyen bir istemci "sıfır adet"
// göndermiş sayılırdı.
func TestAddLineItemAdetZorunlu(t *testing.T) {
	svc := &fakeCarts{}
	akis := &fakePricing{}
	h := yeniAkisliSunucu(t, svc, api.Flows{Pricing: akis})

	rec := istek(t, h, http.MethodPost, "/store/v1/carts/cart_1/line-items",
		`{"variant_id":"var_1"}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Zero(t, akis.calls, "akış çağrılmamalı")
}

// TestUpdateLineItemAdetYazar adet güncellemenin AKIŞA bağlandığını doğrular.
//
// Adet fiyatı değiştirebilir (pricing birim fiyatı adet aralığına göre seçer),
// bu yüzden yol servise değil akışa gider: servise gitseydi satır yeni adetle
// ama eski kademenin fiyatıyla kalırdı.
func TestUpdateLineItemAdetYazar(t *testing.T) {
	svc := sepetliSatir()
	akis := &fakePricing{}
	h := yeniAkisliSunucu(t, svc, api.Flows{Pricing: akis})

	rec := istek(t, h, http.MethodPatch, "/store/v1/carts/cart_1/line-items/li_1", `{"quantity":5}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "cart_1", akis.gotCartID)
	assert.Equal(t, "li_1", akis.gotLineID)
	assert.Equal(t, int64(5), akis.gotQuantity)
	assert.Zero(t, svc.gotQuantity, "adet servise DOĞRUDAN yazılmamalı")
}

// TestUpdateLineItemSifirAdetKaldirir sıfır adedin satırı kaldırdığını ve
// gövdesiz 204 döndüğünü doğrular.
//
// Kaldırılmış bir satırın kaydını yanıtta sunmak, istemciye artık var olmayan
// bir kaynağı vermek olurdu.
func TestUpdateLineItemSifirAdetKaldirir(t *testing.T) {
	svc := sepetliSatir()
	akis := &fakePricing{removed: true}
	h := yeniAkisliSunucu(t, svc, api.Flows{Pricing: akis})

	rec := istek(t, h, http.MethodPatch, "/store/v1/carts/cart_1/line-items/li_1", `{"quantity":0}`)

	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	assert.Empty(t, rec.Body.String())
	assert.Equal(t, int64(0), akis.gotQuantity)
}

// TestCompleteCartSiparisUretir tamamlama ucunun akışı çağırdığını ve siparişi
// döndürdüğünü doğrular.
func TestCompleteCartSiparisUretir(t *testing.T) {
	svc := sepetliSatir()
	akis := &fakeCheckout{response: json.RawMessage(
		`{"order_id":"order_1","cart_id":"cart_1","currency_code":"TRY","amount":3600}`)}
	h := yeniAkisliSunucu(t, svc, api.Flows{Checkout: akis})

	rec := istek(t, h, http.MethodPost, "/store/v1/carts/cart_1/complete",
		`{"payment_provider_id":"test","payment_data":{"token":"tok_1"},"expected_total":3600}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, akis.calls)

	data := nesne(t, govde(t, rec)["data"])
	assert.Equal(t, "order_1", data["order_id"])
	assert.InDelta(t, 3600, data["total"], 0.0)

	// Akışa giden istek: sepet kimliği yoldan, e-posta SEPETTEN gelir.
	gonderilen := map[string]any{}
	require.NoError(t, json.Unmarshal(akis.got, &gonderilen))
	assert.Equal(t, "cart_1", gonderilen["cart_id"])
	assert.Equal(t, "test", gonderilen["payment_provider_id"])
	assert.InDelta(t, 3600, gonderilen["expected_total"], 0.0)
	assert.Equal(t, "a@b.c", gonderilen["email"],
		"iletişim adresi sepetin verisidir; istemciden alınmaz")
}

// TestCompleteCartEpostaGovdedenAlinmaz e-postanın istek gövdesinde
// TAŞINAMADIĞINI doğrular.
//
// Alan kabul edilseydi sipariş, sepette görünenden başka bir adrese
// bağlanabilirdi; adresin tek kaynağı sepettir.
func TestCompleteCartEpostaGovdedenAlinmaz(t *testing.T) {
	svc := sepetliSatir()
	akis := &fakeCheckout{}
	h := yeniAkisliSunucu(t, svc, api.Flows{Checkout: akis})

	rec := istek(t, h, http.MethodPost, "/store/v1/carts/cart_1/complete",
		`{"payment_provider_id":"test","expected_total":3600,"email":"saldirgan@x.y"}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Zero(t, akis.calls, "reddedilen istek akışa ulaşmamalı")
}

// TestCompleteCartLokasyonGovdedenAlinmaz depo seçiminin istemciye
// bırakılmadığını doğrular.
//
// Hangi depodan çıkılacağı bir kargo kararıdır; alanın kabul edilmesi hem stok
// topolojisini sızdırır hem de siparişin nereden çıkacağını müşteriye
// bırakırdı.
func TestCompleteCartLokasyonGovdedenAlinmaz(t *testing.T) {
	svc := sepetliSatir()
	akis := &fakeCheckout{}
	h := yeniAkisliSunucu(t, svc, api.Flows{Checkout: akis})

	rec := istek(t, h, http.MethodPost, "/store/v1/carts/cart_1/complete",
		`{"payment_provider_id":"test","expected_total":3600,"location_id":"sloc_1"}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Zero(t, akis.calls)
}

// TestCompleteCartOnaylananToplamZorunlu expected_total'ın eksikliğinin
// reddedildiğini doğrular.
//
// Opsiyonel olsaydı alanı unutan her istemci, "gördüğün tutarla çekilen tutar
// aynı mı" korumasını sessizce kapatırdı — kural tanımlı, uygulandığı yer yok.
func TestCompleteCartOnaylananToplamZorunlu(t *testing.T) {
	svc := sepetliSatir()
	akis := &fakeCheckout{}
	h := yeniAkisliSunucu(t, svc, api.Flows{Checkout: akis})

	rec := istek(t, h, http.MethodPost, "/store/v1/carts/cart_1/complete",
		`{"payment_provider_id":"test"}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Zero(t, akis.calls, "onaylanan toplam bildirilmeden akış çalışmamalı")
}

// TestCompleteCartAkisYokkenTamamlanmaz akış bağlı değilken sepetin
// tamamlanmadığını doğrular.
func TestCompleteCartAkisYokkenTamamlanmaz(t *testing.T) {
	h := yeniAkisliSunucu(t, sepetliSatir(), api.Flows{})

	rec := istek(t, h, http.MethodPost, "/store/v1/carts/cart_1/complete",
		`{"payment_provider_id":"test","expected_total":3600}`)

	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
}

// TestCompleteCartAkisHatasiOlduguGibiDoner akışın hata SINIFININ korunduğunu
// doğrular.
//
// Onaylanan tutar ile hesaplanan tutar ayrıştığında akış errors.Conflict döner
// ve istemcinin bunu 409 olarak görmesi gerekir: 500 görseydi "sunucu bozuk"
// diye yeniden denerdi, oysa yapması gereken müşteriye yeni tutarı
// onaylatmaktır.
func TestCompleteCartAkisHatasiOlduguGibiDoner(t *testing.T) {
	akis := &fakeCheckout{err: errors.Conflict("checkout_workflow_total_mismatch",
		"onaylanan toplam ile hesaplanan toplam uyuşmuyor")}
	h := yeniAkisliSunucu(t, sepetliSatir(), api.Flows{Checkout: akis})

	rec := istek(t, h, http.MethodPost, "/store/v1/carts/cart_1/complete",
		`{"payment_provider_id":"test","expected_total":1}`)

	assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
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

// TestToplamUclariHTTPyeAcilmaz servisin workflow yüzeyinin HTTP'ye
// AÇILMADIĞINI doğrular.
//
// [service.Service.SetTotals] ve [service.Service.MarkCompleted] hâlâ route
// almaz: açık olsalardı bir istemci sepetin tutarını kendi yazabilir ya da
// ödeme yapmadan sepeti kapatabilirdi.
//
// Vitrindeki /complete bu kuralın istisnası DEĞİLDİR ve bu yüzden listede
// yoktur: o uç sepeti "tamamlandı" damgalamaz, complete_cart saga'sını
// çalıştırır — stok ayrılır, sipariş açılır, ödeme tahsil edilir ve sepet
// ancak ONDAN SONRA, akış tarafından kapatılır. Kapatma yetkisi hâlâ HTTP'de
// değil akıştadır (bkz. [TestCompleteCartSiparisUretir]). Yönetim tarafında
// karşılığı yoktur: sepeti tamamlayan taraf müşteridir.
func TestToplamUclariHTTPyeAcilmaz(t *testing.T) {
	h := yeniSunucu(t, &fakeCarts{})

	for _, path := range []string{
		"/store/v1/carts/cart_1/totals",
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

// TestDarYetkiliKimlikYonetimOkumasindaGecer yalnızca [api.ScopeRead] taşıyan
// bir kimliğin yönetim OKUMA ucundan geçtiğini doğrular.
//
// Yetki zorlamasının değeri, dar yetkiyi de GERÇEKTEN kabul etmesindedir:
// yalnızca reddetseydi kimse dar yetki dağıtmaz, herkese admin verilirdi.
func TestDarYetkiliKimlikYonetimOkumasindaGecer(t *testing.T) {
	h := yeniSunucu(t, &fakeCarts{})
	darKimlik := corehttp.Principal{ID: "user_dar", Kind: "user", Scopes: []string{api.ScopeRead}}

	rec := istekGonder(t, h, &darKimlik, http.MethodGet, "/admin/v1/carts", "")

	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// TestYetkisizKimlikYonetimOkumasinda403Alir sepetlerin, yetkileri boşaltılmış
// bir yönetim kimliğine KAPALI olduğunu doğrular.
//
// Bu testin sınadığı senaryo somuttur: geçerli bir oturum açan ama hiçbir
// yetkisi olmayan bir kullanıcı, GET /admin/v1/carts ile tüm müşterilerin
// sepetlerini e-posta adresleriyle birlikte okuyabilirdi.
//
// Cart'ın yönetim yüzeyinde YAZMA ucu bulunmadığı için "okuma yetkisiyle
// yazma ucuna gitme" durumu burada sınanamaz; onun yerine [api.ScopeWrite]
// taşıyan kimlik kullanılır ve yazma yetkisinin okumayı AÇMADIĞI gösterilir.
func TestYetkisizKimlikYonetimOkumasinda403Alir(t *testing.T) {
	h := yeniSunucu(t, &fakeCarts{})

	for ad, kimlik := range map[string]corehttp.Principal{
		"yetkisiz":       {ID: "user_bos", Kind: "user", Scopes: []string{}},
		"baska modül":    {ID: "user_ord", Kind: "user", Scopes: []string{"order:read"}},
		"yalnızca yazma": {ID: "user_yaz", Kind: "user", Scopes: []string{api.ScopeWrite}},
	} {
		t.Run(ad, func(t *testing.T) {
			rec := istekGonder(t, h, &kimlik, http.MethodGet, "/admin/v1/carts", "")

			assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
		})
	}
}

// TestKimliksizYonetimIstegi401Alir kimliği hiç olmayan isteğin 401, yetkisi
// yetmeyenin 403 aldığını doğrular.
//
// Ayrım bilinçlidir: 401 "kim olduğunu söyle", 403 "kim olduğunu biliyorum
// ama yetkin yok" demektir. İkisi karışsaydı istemci, kimliğini yenileyerek
// çözülmeyecek bir sorun için oturum tazelemeyi denerdi.
func TestKimliksizYonetimIstegi401Alir(t *testing.T) {
	h := yeniSunucu(t, &fakeCarts{})

	rec := istekGonder(t, h, nil, http.MethodGet, "/admin/v1/carts", "")

	assert.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
}

// TestMagazaUclariYetkiIstemez store yüzeyine yetki EKLENMEDİĞİNİ doğrular.
//
// Mağaza yüzeyinin kimliği publishable anahtardır ve o anahtar tanımı gereği
// yetki taşımaz; store uçlarına yanlışlıkla bir scope takılırsa vitrin
// tamamen çalışmaz hâle gelir ve bu test onu hemen yakalar.
func TestMagazaUclariYetkiIstemez(t *testing.T) {
	h := yeniSunucu(t, &fakeCarts{})

	rec := istekGonder(t, h, nil, http.MethodPost, "/store/v1/carts", `{"region_id":"reg_1"}`)

	assert.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
}
