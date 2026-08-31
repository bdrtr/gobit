package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/api"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// fakeCatalog api katmanının servisten beklediği yüzeyin sahtesidir.
//
// Gömülü arayüz bilinçlidir: testte KULLANILMAYAN bir metot çağrılırsa nil
// arayüz paniği verir ve handler'ın beklenmedik bir çağrı yaptığı anında
// görünür. Yalnızca sınanan metotlar geçersiz kılınır.
type fakeCatalog struct {
	api.Catalog

	createProduct     func(ctx context.Context, in service.CreateProductInput) (models.Product, error)
	getProduct        func(ctx context.Context, id string) (models.Product, error)
	listProducts      func(ctx context.Context, opts service.ListProductsOptions) (service.ListResult[models.Product], error)
	deleteProduct     func(ctx context.Context, id string) error
	createVariant     func(ctx context.Context, productID string, in service.CreateVariantInput) (models.Variant, error)
	setPriceSet       func(ctx context.Context, variantID, priceSetID string) error
	variantLinks      func(ctx context.Context, variantID string) (service.VariantLinks, error)
	listStoreProducts func(ctx context.Context, opts service.StoreListOptions) (service.ListResult[service.StoreProduct], error)
	getStoreProduct   func(ctx context.Context, idOrHandle string, salesChannelIDs []string) (service.StoreProduct, error)

	addSalesChannel    func(ctx context.Context, productID, salesChannelID string) error
	removeSalesChannel func(ctx context.Context, productID, salesChannelID string) error
	salesChannelIDs    func(ctx context.Context, productID string) ([]string, error)
}

func (f *fakeCatalog) CreateProduct(ctx context.Context, in service.CreateProductInput) (models.Product, error) {
	return f.createProduct(ctx, in)
}

func (f *fakeCatalog) GetProduct(ctx context.Context, id string) (models.Product, error) {
	return f.getProduct(ctx, id)
}

func (f *fakeCatalog) ListProducts(
	ctx context.Context,
	opts service.ListProductsOptions,
) (service.ListResult[models.Product], error) {
	return f.listProducts(ctx, opts)
}

func (f *fakeCatalog) DeleteProduct(ctx context.Context, id string) error {
	return f.deleteProduct(ctx, id)
}

func (f *fakeCatalog) CreateVariant(
	ctx context.Context,
	productID string,
	in service.CreateVariantInput,
) (models.Variant, error) {
	return f.createVariant(ctx, productID, in)
}

func (f *fakeCatalog) SetVariantPriceSet(ctx context.Context, variantID, priceSetID string) error {
	return f.setPriceSet(ctx, variantID, priceSetID)
}

func (f *fakeCatalog) VariantLinkIDs(ctx context.Context, variantID string) (service.VariantLinks, error) {
	return f.variantLinks(ctx, variantID)
}

func (f *fakeCatalog) ListStoreProducts(
	ctx context.Context,
	opts service.StoreListOptions,
) (service.ListResult[service.StoreProduct], error) {
	return f.listStoreProducts(ctx, opts)
}

func (f *fakeCatalog) GetStoreProduct(
	ctx context.Context,
	idOrHandle string,
	salesChannelIDs []string,
) (service.StoreProduct, error) {
	return f.getStoreProduct(ctx, idOrHandle, salesChannelIDs)
}

func (f *fakeCatalog) AddProductSalesChannel(ctx context.Context, productID, salesChannelID string) error {
	return f.addSalesChannel(ctx, productID, salesChannelID)
}

func (f *fakeCatalog) RemoveProductSalesChannel(ctx context.Context, productID, salesChannelID string) error {
	return f.removeSalesChannel(ctx, productID, salesChannelID)
}

func (f *fakeCatalog) ProductSalesChannelIDs(ctx context.Context, productID string) ([]string, error) {
	return f.salesChannelIDs(ctx, productID)
}

// newRouter sahte servisle bağlanmış bir router üretir.
func newRouter(catalog api.Catalog) chi.Router {
	r := chi.NewRouter()
	api.New(catalog).Routes(r)
	return r
}

// do isteği router'a uygular ve yanıtı döner.
//
// İstek TAM YETKİLİ bir kimlik taşır. Üretimde kimliği corehttp.RequireAdmin
// context'e koyar; bu testler router'ı doğrudan kurduğu için o middleware
// devrede değildir ve kimlik elle konur. Gerekçesi, yönetim uçlarına
// corehttp.RequireScope eklenmesidir: kimliksiz bir istek artık handler'a hiç
// ulaşmadan 401 alır ve buradaki testler zarf/hata eşlemesi yerine yetki
// katmanını sınamış olurdu. Yetkinin KENDİSİ ayrı bir dosyada sınanır
// (yetki_test.go); bu dosyanın iddiaları değişmedi.
func do(t *testing.T, r chi.Router, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()

	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
		ID:     "usr_test",
		Kind:   "user",
		Scopes: []string{corehttp.ScopeAdmin},
	}))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// decodeBody yanıt gövdesini haritaya çözer.
func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), "gövde: %s", rec.Body.String())
	return out
}

// TestListEnvelopeShape liste zarfının plan Bölüm 8'deki biçimi taşıdığını
// doğrular.
func TestListEnvelopeShape(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{
		listProducts: func(_ context.Context, opts service.ListProductsOptions) (service.ListResult[models.Product], error) {
			return service.ListResult[models.Product]{
				Items:  []models.Product{{ID: "prod_1", Handle: "tisort", Title: "Tişört"}},
				Count:  42,
				Offset: opts.Offset,
				Limit:  opts.Limit,
			}, nil
		},
	}

	rec := do(t, newRouter(catalog), http.MethodGet, "/admin/v1/products?limit=5&offset=10", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))

	body := decodeBody(t, rec)
	assert.Equal(t, float64(42), body["count"], "count toplam kayıt sayısıdır")
	assert.Equal(t, float64(10), body["offset"])
	assert.Equal(t, float64(5), body["limit"])

	data, ok := body["data"].([]any)
	require.True(t, ok, "data bir dizi olmalı: %#v", body["data"])
	require.Len(t, data, 1)
	item, ok := data[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "prod_1", item["id"])
	assert.Equal(t, "tisort", item["handle"])
}

// TestEmptyListReturnsArrayNotNull boş listenin JSON'da null değil boş dizi
// döndüğünü doğrular.
func TestEmptyListReturnsArrayNotNull(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{
		listProducts: func(context.Context, service.ListProductsOptions) (service.ListResult[models.Product], error) {
			return service.ListResult[models.Product]{}, nil
		},
	}

	rec := do(t, newRouter(catalog), http.MethodGet, "/admin/v1/products", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"data":[]`,
		"boş liste null değil boş dizi olmalı: %s", rec.Body.String())
}

// TestCreateProductReturns201AndItemEnvelope oluşturma yanıtının zarfını ve
// durum kodunu doğrular.
func TestCreateProductReturns201AndItemEnvelope(t *testing.T) {
	t.Parallel()

	var got service.CreateProductInput
	catalog := &fakeCatalog{
		createProduct: func(_ context.Context, in service.CreateProductInput) (models.Product, error) {
			got = in
			return models.Product{ID: "prod_1", Handle: "tisort", Title: in.Title}, nil
		},
	}

	rec := do(t, newRouter(catalog), http.MethodPost, "/admin/v1/products",
		`{"title":"Tişört","status":"published","options":[{"title":"Beden","values":["S"]}],
		  "variants":[{"title":"S","options":{"Beden":"S"},"manage_inventory":false}]}`)
	require.Equal(t, http.StatusCreated, rec.Code, "gövde: %s", rec.Body.String())

	body := decodeBody(t, rec)
	data, ok := body["data"].(map[string]any)
	require.True(t, ok, "tekil yanıt data nesnesi taşımalı: %#v", body)
	assert.Equal(t, "prod_1", data["id"])
	assert.NotContains(t, body, "count", "tekil yanıtta sayfalama alanları olmaz")

	assert.Equal(t, "Tişört", got.Title, "gövde servis girdisine çevrilmeli")
	assert.Equal(t, models.StatusPublished, got.Status)
	require.Len(t, got.Options, 1)
	assert.Equal(t, []string{"S"}, got.Options[0].Values)
	require.Len(t, got.Variants, 1)
	require.NotNil(t, got.Variants[0].ManageInventory)
	assert.False(t, *got.Variants[0].ManageInventory,
		"false değeri işaretçi sayesinde 'verilmedi'den ayrılmalı")
}

// TestErrorKindsMapToStatus servis hatalarının HTTP durum koduna doğru
// eşlendiğini doğrular.
//
// Handler durum kodu SEÇMEZ; kod hatanın sınıfından gelir (plan Bölüm 8).
func TestErrorKindsMapToStatus(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		err    error
		status int
		code   string
	}{
		"bulunamadı": {coreerrors.NotFound("product_not_found", "ürün yok"), http.StatusNotFound, "product_not_found"},
		"çakışma":    {coreerrors.Conflict("product_handle_taken", "dolu"), http.StatusConflict, "product_handle_taken"},
		"doğrulama":  {coreerrors.Invalid("product_invalid_input", "başlık zorunlu"), http.StatusUnprocessableEntity, "product_invalid_input"},
		"erişilemez": {coreerrors.Unavailable("product_link_failed", "link yok"), http.StatusServiceUnavailable, "product_link_failed"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			catalog := &fakeCatalog{
				getProduct: func(context.Context, string) (models.Product, error) {
					return models.Product{}, tc.err
				},
			}

			rec := do(t, newRouter(catalog), http.MethodGet, "/admin/v1/products/prod_1", "")
			require.Equal(t, tc.status, rec.Code)

			body := decodeBody(t, rec)
			errBody, ok := body["error"].(map[string]any)
			require.True(t, ok, "hata zarfı bekleniyordu: %#v", body)
			assert.Equal(t, tc.code, errBody["code"])
		})
	}
}

// TestInternalErrorIsMasked sunucu hatasının ayrıntısının istemciye
// sızmadığını doğrular.
func TestInternalErrorIsMasked(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{
		getProduct: func(context.Context, string) (models.Product, error) {
			return models.Product{}, coreerrors.Internal("db_failed", "dsn=postgres://gizli@host/db")
		},
	}

	rec := do(t, newRouter(catalog), http.MethodGet, "/admin/v1/products/prod_1", "")
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "gizli", "iç hata metni sızmamalı: %s", rec.Body.String())
}

// TestRejectsUnknownJSONField gövdedeki bilinmeyen alanın sessizce yok
// sayılmadığını doğrular.
func TestRejectsUnknownJSONField(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{
		createProduct: func(context.Context, service.CreateProductInput) (models.Product, error) {
			t.Fatal("bozuk gövde servise hiç gitmemeliydi")
			return models.Product{}, nil
		},
	}

	rec := do(t, newRouter(catalog), http.MethodPost, "/admin/v1/products", `{"titel":"Tişört"}`)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	errBody, ok := decodeBody(t, rec)["error"].(map[string]any)
	require.True(t, ok, "hata zarfı bekleniyordu")
	assert.Equal(t, "product_bad_json", errBody["code"])
}

// TestRejectsEmptyAndDoubleBody boş gövdenin ve birden çok JSON nesnesinin
// reddedildiğini doğrular.
func TestRejectsEmptyAndDoubleBody(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{
		createProduct: func(context.Context, service.CreateProductInput) (models.Product, error) {
			t.Fatal("bozuk gövde servise hiç gitmemeliydi")
			return models.Product{}, nil
		},
	}
	r := newRouter(catalog)

	rec := do(t, r, http.MethodPost, "/admin/v1/products", "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "boş gövde reddedilmeli")

	rec = do(t, r, http.MethodPost, "/admin/v1/products", `{"title":"Bir"}{"title":"İki"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "ikinci gövde sessizce yutulmamalı")
}

// TestRejectsBadQueryParams sorgu parametrelerinin doğrulandığını gösterir.
func TestRejectsBadQueryParams(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{
		listProducts: func(context.Context, service.ListProductsOptions) (service.ListResult[models.Product], error) {
			t.Fatal("bozuk parametre servise hiç gitmemeliydi")
			return service.ListResult[models.Product]{}, nil
		},
	}
	r := newRouter(catalog)

	rec := do(t, r, http.MethodGet, "/admin/v1/products?limit=cok", "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)

	rec = do(t, r, http.MethodGet, "/admin/v1/products?expand=belki", "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

// TestListProductsParsesFilters sorgu parametrelerinin servis seçeneklerine
// çevrildiğini doğrular.
func TestListProductsParsesFilters(t *testing.T) {
	t.Parallel()

	var got service.ListProductsOptions
	catalog := &fakeCatalog{
		listProducts: func(_ context.Context, opts service.ListProductsOptions) (service.ListResult[models.Product], error) {
			got = opts
			return service.ListResult[models.Product]{Items: []models.Product{}}, nil
		},
	}

	rec := do(t, newRouter(catalog), http.MethodGet,
		"/admin/v1/products?status=published&collection_id=pcol_1&q=tis&expand=true&limit=7&offset=3", "")
	require.Equal(t, http.StatusOK, rec.Code)

	require.NotNil(t, got.Status)
	assert.Equal(t, models.StatusPublished, *got.Status)
	require.NotNil(t, got.CollectionID)
	assert.Equal(t, "pcol_1", *got.CollectionID)
	require.NotNil(t, got.Search)
	assert.Equal(t, "tis", *got.Search)
	assert.True(t, got.WithRelations)
	assert.Equal(t, 7, got.Limit)
	assert.Equal(t, 3, got.Offset)
	// Verilmeyen filtre nil kalmalı; boş dizge filtresi hiçbir şeyi eşleştirmezdi.
	assert.Nil(t, got.Handle)
}

// TestCreateVariantRouteCarriesProductID yol parametresinin servise
// aktarıldığını doğrular.
func TestCreateVariantRouteCarriesProductID(t *testing.T) {
	t.Parallel()

	var gotProductID string
	catalog := &fakeCatalog{
		createVariant: func(_ context.Context, productID string, in service.CreateVariantInput) (models.Variant, error) {
			gotProductID = productID
			return models.Variant{ID: "variant_1", ProductID: productID, Title: in.Title}, nil
		},
	}

	rec := do(t, newRouter(catalog), http.MethodPost, "/admin/v1/products/prod_9/variants",
		`{"title":"S beden","option_value_ids":["poptval_1"]}`)
	require.Equal(t, http.StatusCreated, rec.Code, "gövde: %s", rec.Body.String())
	assert.Equal(t, "prod_9", gotProductID)
}

// TestSetPriceSetRouteLinksVariant fiyat kümesi bağlama ucunun gövdedeki
// kimliği servise geçirdiğini ve güncel bağları döndürdüğünü doğrular.
//
// Bu uç Faz 4'ün "bağ admin akışında kurulur" şartının karşılığıdır.
func TestSetPriceSetRouteLinksVariant(t *testing.T) {
	t.Parallel()

	var gotVariantID, gotPriceSetID string
	catalog := &fakeCatalog{
		setPriceSet: func(_ context.Context, variantID, priceSetID string) error {
			gotVariantID, gotPriceSetID = variantID, priceSetID
			return nil
		},
		variantLinks: func(context.Context, string) (service.VariantLinks, error) {
			id := "pset_1"
			return service.VariantLinks{PriceSetID: &id}, nil
		},
	}

	rec := do(t, newRouter(catalog), http.MethodPut, "/admin/v1/variants/variant_1/price-set",
		`{"price_set_id":"pset_1"}`)
	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())
	assert.Equal(t, "variant_1", gotVariantID)
	assert.Equal(t, "pset_1", gotPriceSetID)

	data, ok := decodeBody(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "pset_1", data["price_set_id"])
}

// TestDeleteProductReturnsDeletionEnvelope silme yanıtının silinen kaydı
// bildirdiğini doğrular.
func TestDeleteProductReturnsDeletionEnvelope(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{deleteProduct: func(context.Context, string) error { return nil }}

	rec := do(t, newRouter(catalog), http.MethodDelete, "/admin/v1/products/prod_1", "")
	require.Equal(t, http.StatusOK, rec.Code)

	data, ok := decodeBody(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "prod_1", data["id"])
	assert.Equal(t, "product", data["object"])
	assert.Equal(t, true, data["deleted"])
}

// TestStoreListIncludesPriceAndInventory vitrin yanıtının fiyat ve stok
// alanlarını taşıdığını doğrular.
func TestStoreListIncludesPriceAndInventory(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{
		listStoreProducts: func(_ context.Context, opts service.StoreListOptions) (service.ListResult[service.StoreProduct], error) {
			return service.ListResult[service.StoreProduct]{
				Items: []service.StoreProduct{{
					Product: models.Product{ID: "prod_1", Handle: "tisort", Title: "Tişört"},
					Variants: []service.StoreVariant{{
						Variant:       models.Variant{ID: "variant_1", ProductID: "prod_1", Title: "S"},
						PriceSet:      query.Record{"id": "pset_1", "amount": 19900, "currency_code": "try"},
						InventoryItem: query.Record{"id": "invitem_1", "stocked_quantity": 3},
					}},
				}},
				Count:  1,
				Limit:  opts.Limit,
				Offset: opts.Offset,
			}, nil
		},
	}

	rec := do(t, newRouter(catalog), http.MethodGet, "/store/v1/products", "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := decodeBody(t, rec)
	data, ok := body["data"].([]any)
	require.True(t, ok, "data dizi olmalı: %#v", body["data"])
	require.Len(t, data, 1)
	product, ok := data[0].(map[string]any)
	require.True(t, ok)
	variants, ok := product["variants"].([]any)
	require.True(t, ok, "vitrin ürünü varyantlarını taşımalı: %#v", product)
	require.Len(t, variants, 1)

	variant, ok := variants[0].(map[string]any)
	require.True(t, ok)
	priceSet, ok := variant["price_set"].(map[string]any)
	require.True(t, ok, "varyant fiyat kümesini taşımalı: %#v", variant)
	assert.Equal(t, float64(19900), priceSet["amount"])
	inventory, ok := variant["inventory_item"].(map[string]any)
	require.True(t, ok, "varyant stok kalemini taşımalı: %#v", variant)
	assert.Equal(t, float64(3), inventory["stocked_quantity"])
}

// TestStoreGetProductAcceptsHandle vitrin tekil ucunun handle ile
// çağrılabildiğini doğrular.
func TestStoreGetProductAcceptsHandle(t *testing.T) {
	t.Parallel()

	var got string
	catalog := &fakeCatalog{
		getStoreProduct: func(_ context.Context, idOrHandle string, _ []string) (service.StoreProduct, error) {
			got = idOrHandle
			return service.StoreProduct{
				Product:  models.Product{ID: "prod_1", Handle: idOrHandle},
				Variants: []service.StoreVariant{},
			}, nil
		},
	}

	rec := do(t, newRouter(catalog), http.MethodGet, "/store/v1/products/tisort", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "tisort", got)
}

// TestStoreProductHidesEmbeddedVariants gömülü ürünün varyant alanının
// vitrin yanıtını GÖLGELEMEDİĞİNİ doğrular.
//
// StoreProduct, models.Product'ı gömer ve kendi Variants alanıyla onu gölgeler;
// JSON'da tek bir "variants" anahtarı olmalıdır, yoksa istemci hangi listeye
// bakacağını bilemez.
func TestStoreProductHidesEmbeddedVariants(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{
		getStoreProduct: func(context.Context, string, []string) (service.StoreProduct, error) {
			return service.StoreProduct{
				Product: models.Product{
					ID: "prod_1",
					// Gömülü alan bilinçli olarak dolduruldu: yanıtta GÖRÜNMEMELİ.
					Variants: []models.Variant{{ID: "variant_gizli"}},
				},
				Variants: []service.StoreVariant{{Variant: models.Variant{ID: "variant_1"}}},
			}, nil
		},
	}

	rec := do(t, newRouter(catalog), http.MethodGet, "/store/v1/products/prod_1", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "variant_gizli",
		"gömülü varyant listesi yanıta sızmamalı: %s", rec.Body.String())
	assert.Contains(t, rec.Body.String(), "variant_1")
}

// TestRoutesDoNotMountSharedPrefixes route'ların "/admin/v1" ya da "/store/v1"
// önekini MOUNT ETMEDİĞİNİ doğrular.
//
// Registry tüm modüllerin Routes'unu aynı router üzerinde çağırır; ortak öneki
// mount eden ikinci modül chi'de panikle düşerdi. Bu test o sözleşmeyi kilitler:
// aynı router'a başka bir modülün aynı öneki altındaki ucu eklenebilmelidir.
func TestRoutesDoNotMountSharedPrefixes(t *testing.T) {
	t.Parallel()

	r := chi.NewRouter()
	api.New(&fakeCatalog{}).Routes(r)

	assert.NotPanics(t, func() {
		// Başka bir modülün (örn. pricing) aynı sürüm öneki altındaki ucu.
		r.Get("/admin/v1/price-sets", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		r.Get("/store/v1/prices", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
	}, "ortak sürüm öneki mount edilmemeli; başka modüller aynı önek altına uç ekleyebilmeli")

	rec := do(t, r, http.MethodGet, "/admin/v1/price-sets", "")
	assert.Equal(t, http.StatusOK, rec.Code)
}
