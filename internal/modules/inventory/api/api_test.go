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
	"github.com/bdrtr/gobit/internal/modules/inventory/api"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// fakeInventory api.Inventory'nin test karşılığıdır. Handler'ların HTTP
// davranışını (status kodu, zarf, hata eşlemesi) veritabanı olmadan
// sınayabilmek için vardır.
type fakeInventory struct {
	// Dönüş değerleri.
	location models.StockLocation
	item     models.InventoryItem
	level    models.InventoryLevel
	items    []models.InventoryItem
	levels   []models.InventoryLevel
	count    int64
	err      error

	// Kaydedilen çağrı bilgileri.
	gorulenPage       service.Page
	gorulenItemInput  service.CreateInventoryItemInput
	gorulenListInput  service.ListInventoryItemsInput
	gorulenID         string
	gorulenLocationID string
	gorulenStocked    int64
	gorulenDelta      int64
}

// Sahtenin handler'ın beklediği yüzeyi karşıladığı derleme zamanında
// doğrulanır.
var _ api.Inventory = (*fakeInventory)(nil)

// CreateStockLocation lokasyon oluşturma çağrısını kaydeder.
func (f *fakeInventory) CreateStockLocation(_ context.Context, _ service.CreateStockLocationInput) (models.StockLocation, error) {
	return f.location, f.err
}

// GetStockLocation istenen lokasyon kimliğini kaydeder.
func (f *fakeInventory) GetStockLocation(_ context.Context, id string) (models.StockLocation, error) {
	f.gorulenID = id
	return f.location, f.err
}

// ListStockLocations sayfalama parametrelerini kaydeder.
func (f *fakeInventory) ListStockLocations(_ context.Context, page service.Page) ([]models.StockLocation, int64, error) {
	f.gorulenPage = page
	return []models.StockLocation{f.location}, f.count, f.err
}

// CreateInventoryItem kalem oluşturma girdisini kaydeder.
func (f *fakeInventory) CreateInventoryItem(_ context.Context, in service.CreateInventoryItemInput) (models.InventoryItem, error) {
	f.gorulenItemInput = in
	return f.item, f.err
}

// GetInventoryItem istenen kimliği kaydeder.
func (f *fakeInventory) GetInventoryItem(_ context.Context, id string) (models.InventoryItem, error) {
	f.gorulenID = id
	return f.item, f.err
}

// ListInventoryItems listeleme girdisini kaydeder.
func (f *fakeInventory) ListInventoryItems(_ context.Context, in service.ListInventoryItemsInput) ([]models.InventoryItem, int64, error) {
	f.gorulenListInput = in
	return f.items, f.count, f.err
}

// DeleteInventoryItem silinen kimliği kaydeder.
func (f *fakeInventory) DeleteInventoryItem(_ context.Context, id string) error {
	f.gorulenID = id
	return f.err
}

// ListInventoryLevels kalemin seviyelerini döner.
func (f *fakeInventory) ListInventoryLevels(_ context.Context, itemID string) ([]models.InventoryLevel, error) {
	f.gorulenID = itemID
	return f.levels, f.err
}

// SetInventoryLevel yazılan adedi kaydeder.
func (f *fakeInventory) SetInventoryLevel(_ context.Context, itemID, locationID string, stockedQty int64) (models.InventoryLevel, error) {
	f.gorulenID, f.gorulenLocationID, f.gorulenStocked = itemID, locationID, stockedQty
	return f.level, f.err
}

// AdjustInventory düzeltme miktarını kaydeder.
func (f *fakeInventory) AdjustInventory(_ context.Context, itemID, locationID string, delta int64) (models.InventoryLevel, error) {
	f.gorulenID, f.gorulenLocationID, f.gorulenDelta = itemID, locationID, delta
	return f.level, f.err
}

// yeniSunucu handler'ları bağlı bir router ve sahte servis döner.
func yeniSunucu(t *testing.T) (chi.Router, *fakeInventory) {
	t.Helper()

	svc := &fakeInventory{}
	router := chi.NewRouter()
	api.NewHandler(svc).Routes(router)
	return router, svc
}

// istek verilen isteği router'a gönderir ve yanıtı döner.
//
// İstek TAM YETKİLİ bir kimlik taşır. Üretimde kimliği corehttp.RequireAdmin
// context'e koyar; bu testler router'ı doğrudan kurduğu için o middleware
// devrede değildir ve kimlik elle konur. Gerekçesi, yönetim uçlarına
// corehttp.RequireScope eklenmesidir: kimliksiz bir istek artık handler'a hiç
// ulaşmadan 401 alır ve buradaki testler stok davranışı yerine yetki katmanını
// sınamış olurdu. Yetkinin KENDİSİ ayrı bir dosyada sınanır (yetki_test.go);
// bu dosyanın iddiaları değişmedi.
func istek(t *testing.T, router chi.Router, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
		ID:     "usr_test",
		Kind:   "user",
		Scopes: []string{corehttp.ScopeAdmin},
	}))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// govde yanıt gövdesini haritaya çözer.
func govde(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), "yanıt JSON olmalı: %s", rec.Body.String())
	return out
}

// TestCreateStockLocation başarılı oluşturmanın 201 ve tekil zarf döndüğünü
// doğrular.
func TestCreateStockLocation(t *testing.T) {
	router, svc := yeniSunucu(t)
	svc.location = models.StockLocation{
		ID: "sloc_1", Name: "Merkez", CountryCode: "TR",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}

	rec := istek(t, router, http.MethodPost, "/admin/v1/stock-locations",
		`{"name":"Merkez","country_code":"TR"}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	data, ok := govde(t, rec)["data"].(map[string]any)
	require.True(t, ok, "yanıt data zarfı taşımalı")
	assert.Equal(t, "sloc_1", data["id"])
	assert.Equal(t, "Merkez", data["name"])
}

// TestGetStockLocation tekil lokasyon okumasının zarfını ve hata eşlemesini
// doğrular.
func TestGetStockLocation(t *testing.T) {
	router, svc := yeniSunucu(t)
	svc.location = models.StockLocation{ID: "sloc_1", Name: "Merkez"}

	rec := istek(t, router, http.MethodGet, "/admin/v1/stock-locations/sloc_1", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "sloc_1", svc.gorulenID)
	data, ok := govde(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Merkez", data["name"])

	svc.err = errors.NotFound("inventory_location_not_found", "lokasyon yok")
	rec = istek(t, router, http.MethodGet, "/admin/v1/stock-locations/sloc_YOK", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestListStockLocationsZarfi liste zarfının dört alanını da doğrular.
func TestListStockLocationsZarfi(t *testing.T) {
	router, svc := yeniSunucu(t)
	svc.location = models.StockLocation{ID: "sloc_1", Name: "Merkez"}
	svc.count = 42

	rec := istek(t, router, http.MethodGet, "/admin/v1/stock-locations?limit=10&offset=20", "")

	require.Equal(t, http.StatusOK, rec.Code)
	body := govde(t, rec)
	assert.Len(t, body["data"], 1)
	assert.InDelta(t, 42, body["count"], 0)
	assert.InDelta(t, 20, body["offset"], 0)
	assert.InDelta(t, 10, body["limit"], 0)
	assert.Equal(t, service.Page{Limit: 10, Offset: 20}, svc.gorulenPage)
}

// TestListVarsayilanLimit limit verilmediğinde varsayılanın uygulandığını ve
// yanıtta GÖRÜNDÜĞÜNÜ doğrular; istemci uygulanan sınırı bilmelidir.
func TestListVarsayilanLimit(t *testing.T) {
	router, svc := yeniSunucu(t)

	rec := istek(t, router, http.MethodGet, "/admin/v1/stock-locations", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.InDelta(t, float64(service.DefaultLimit), govde(t, rec)["limit"], 0)
	assert.Equal(t, service.DefaultLimit, svc.gorulenPage.Limit)
}

// TestListGecersizLimit sayı olmayan limit parametresinin 422 ürettiğini
// doğrular.
func TestListGecersizLimit(t *testing.T) {
	router, _ := yeniSunucu(t)

	rec := istek(t, router, http.MethodGet, "/admin/v1/stock-locations?limit=abc", "")

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

// TestCreateItemVarsayilanSevkiyat gövdede alan yoksa servise nil geçtiğini
// (yani varsayılan kararının serviste verildiğini) doğrular.
func TestCreateItemVarsayilanSevkiyat(t *testing.T) {
	router, svc := yeniSunucu(t)
	svc.item = models.InventoryItem{ID: "invitem_1", SKU: "SKU-1", RequiresShipping: true}

	rec := istek(t, router, http.MethodPost, "/admin/v1/inventory-items", `{"sku":"SKU-1"}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Nil(t, svc.gorulenItemInput.RequiresShipping)

	rec = istek(t, router, http.MethodPost, "/admin/v1/inventory-items",
		`{"sku":"SKU-2","requires_shipping":false}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.NotNil(t, svc.gorulenItemInput.RequiresShipping,
		"alan gönderildiyse servise taşınmalı")
	assert.False(t, *svc.gorulenItemInput.RequiresShipping)
}

// TestCreateItemTaninmayanAlan gövdedeki fazladan alanın sessizce yutulmayıp
// 422 ürettiğini doğrular.
func TestCreateItemTaninmayanAlan(t *testing.T) {
	router, _ := yeniSunucu(t)

	rec := istek(t, router, http.MethodPost, "/admin/v1/inventory-items",
		`{"sku":"SKU-1","fiyat":100}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

// TestCreateItemBosGovde boş gövdenin 422 ürettiğini doğrular.
func TestCreateItemBosGovde(t *testing.T) {
	router, _ := yeniSunucu(t)

	rec := istek(t, router, http.MethodPost, "/admin/v1/inventory-items", "")

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

// TestListItemsFiltreleri sorgu parametrelerinin servise taşındığını doğrular.
func TestListItemsFiltreleri(t *testing.T) {
	router, svc := yeniSunucu(t)

	rec := istek(t, router, http.MethodGet,
		"/admin/v1/inventory-items?sku=SKU-1&requires_shipping=false", "")

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, svc.gorulenListInput.SKU)
	assert.Equal(t, "SKU-1", *svc.gorulenListInput.SKU)
	require.NotNil(t, svc.gorulenListInput.RequiresShipping)
	assert.False(t, *svc.gorulenListInput.RequiresShipping)
}

// TestListItemsGecersizFiltre mantıksal olmayan requires_shipping değerinin
// 422 ürettiğini doğrular.
func TestListItemsGecersizFiltre(t *testing.T) {
	router, _ := yeniSunucu(t)

	rec := istek(t, router, http.MethodGet, "/admin/v1/inventory-items?requires_shipping=belki", "")

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

// TestGetItemBulunamadi servisin NotFound hatasının 404'e çevrildiğini ve
// hata kodunun gövdede korunduğunu doğrular. Handler status kodu SEÇMEZ;
// eşleme çekirdekte yapılır.
func TestGetItemBulunamadi(t *testing.T) {
	router, svc := yeniSunucu(t)
	svc.err = errors.NotFound("inventory_item_not_found", "kalem yok")

	rec := istek(t, router, http.MethodGet, "/admin/v1/inventory-items/invitem_YOK", "")

	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "invitem_YOK", svc.gorulenID)
	hata, ok := govde(t, rec)["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "inventory_item_not_found", hata["code"])
}

// TestDeleteItem silme başarılıysa gövdesiz 204 döndüğünü doğrular.
func TestDeleteItem(t *testing.T) {
	router, svc := yeniSunucu(t)

	rec := istek(t, router, http.MethodDelete, "/admin/v1/inventory-items/invitem_1", "")

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String())
	assert.Equal(t, "invitem_1", svc.gorulenID)
}

// TestDeleteItemCakisma aktif rezervasyon Conflict'inin 409'a çevrildiğini
// doğrular.
func TestDeleteItemCakisma(t *testing.T) {
	router, svc := yeniSunucu(t)
	svc.err = errors.Conflict(service.CodeItemHasReservations, "aktif rezervasyon var")

	rec := istek(t, router, http.MethodDelete, "/admin/v1/inventory-items/invitem_1", "")

	assert.Equal(t, http.StatusConflict, rec.Code)
}

// TestListLevelsSatilabilirAdetIcerir seviye yanıtının türetilmiş satılabilir
// adedi taşıdığını doğrular.
func TestListLevelsSatilabilirAdetIcerir(t *testing.T) {
	router, svc := yeniSunucu(t)
	svc.levels = []models.InventoryLevel{
		{ID: "invlevel_1", InventoryItemID: "invitem_1", LocationID: "sloc_1",
			StockedQuantity: 10, ReservedQuantity: 4},
	}

	rec := istek(t, router, http.MethodGet, "/admin/v1/inventory-items/invitem_1/levels", "")

	require.Equal(t, http.StatusOK, rec.Code)
	body := govde(t, rec)
	assert.InDelta(t, 1, body["count"], 0)
	data, ok := body["data"].([]any)
	require.True(t, ok)
	require.Len(t, data, 1)
	level, ok := data[0].(map[string]any)
	require.True(t, ok)
	assert.InDelta(t, 10, level["stocked_quantity"], 0)
	assert.InDelta(t, 4, level["reserved_quantity"], 0)
	assert.InDelta(t, 6, level["available_quantity"], 0)
}

// TestSetLevel gövdedeki adedin servise taşındığını doğrular.
func TestSetLevel(t *testing.T) {
	router, svc := yeniSunucu(t)
	svc.level = models.InventoryLevel{ID: "invlevel_1", StockedQuantity: 25}

	rec := istek(t, router, http.MethodPost, "/admin/v1/inventory-items/invitem_1/levels",
		`{"location_id":"sloc_1","stocked_quantity":25}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "invitem_1", svc.gorulenID)
	assert.Equal(t, "sloc_1", svc.gorulenLocationID)
	assert.Equal(t, int64(25), svc.gorulenStocked)
}

// TestSetLevelSifirAdet sıfır adedin "alan gönderilmedi" ile karışmadığını
// doğrular: işaretçi kullanılmasaydı 0 gönderen istemci 422 alırdı.
func TestSetLevelSifirAdet(t *testing.T) {
	router, svc := yeniSunucu(t)

	rec := istek(t, router, http.MethodPost, "/admin/v1/inventory-items/invitem_1/levels",
		`{"location_id":"sloc_1","stocked_quantity":0}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, int64(0), svc.gorulenStocked)
}

// TestSetLevelAdetZorunlu adet alanı gönderilmezse 422 dönüldüğünü doğrular.
func TestSetLevelAdetZorunlu(t *testing.T) {
	router, _ := yeniSunucu(t)

	rec := istek(t, router, http.MethodPost, "/admin/v1/inventory-items/invitem_1/levels",
		`{"location_id":"sloc_1"}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

// TestSetLevelYetersizStok servisin Conflict hatasının 409'a çevrildiğini
// doğrular.
func TestSetLevelYetersizStok(t *testing.T) {
	router, svc := yeniSunucu(t)
	svc.err = errors.Conflict(service.CodeInsufficientStock, "rezerve adedin altına inilemez")

	rec := istek(t, router, http.MethodPost, "/admin/v1/inventory-items/invitem_1/levels",
		`{"location_id":"sloc_1","stocked_quantity":1}`)

	require.Equal(t, http.StatusConflict, rec.Code)
	hata, ok := govde(t, rec)["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, service.CodeInsufficientStock, hata["code"])
}

// TestAdjustLevel yol parametrelerinin ve negatif delta'nın doğru taşındığını
// doğrular.
func TestAdjustLevel(t *testing.T) {
	router, svc := yeniSunucu(t)
	svc.level = models.InventoryLevel{ID: "invlevel_1", StockedQuantity: 3}

	rec := istek(t, router, http.MethodPost,
		"/admin/v1/inventory-items/invitem_1/levels/sloc_1/adjust", `{"delta":-2}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "invitem_1", svc.gorulenID)
	assert.Equal(t, "sloc_1", svc.gorulenLocationID)
	assert.Equal(t, int64(-2), svc.gorulenDelta)
}

// TestAdjustLevelDeltaZorunlu delta alanı yoksa 422 dönüldüğünü doğrular.
func TestAdjustLevelDeltaZorunlu(t *testing.T) {
	router, _ := yeniSunucu(t)

	rec := istek(t, router, http.MethodPost,
		"/admin/v1/inventory-items/invitem_1/levels/sloc_1/adjust", `{}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

// TestStokMagazayaAcilmaz store route'u tanımlanmadığını doğrular: müşteri
// tarafı stoğu yalnızca Query katmanı üzerinden görür.
func TestStokMagazayaAcilmaz(t *testing.T) {
	router, _ := yeniSunucu(t)

	for _, path := range []string{
		"/store/v1/inventory-items",
		"/store/v1/stock-locations",
	} {
		rec := istek(t, router, http.MethodGet, path, "")
		assert.Equal(t, http.StatusNotFound, rec.Code, "%s açık olmamalı", path)
	}
}
