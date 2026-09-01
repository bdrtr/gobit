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

	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/api"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// Bu dosya product'ın yönetim uçlarındaki YETKİ katmanını sınar.
//
// Kimlik katmanı (corehttp.RequireAdmin) burada taklit edilir: testin
// kanıtlamak istediği şey "kimlik doğru çözülüyor mu" değil, "çözülmüş
// kimliğin YETKİSİ uç bazında zorlanıyor mu" sorusudur. İkisi ayrı
// sınandığında, kimlik doğrulaması kusursuz çalışırken yetkilendirmenin hiç
// bağlanmamış olduğu durum — yani düzeltilen arıza — görünür kalır.

// yetkiKatalogu her çağrıyı sayan, sıfır değer dönen bir [api.Catalog]'dur.
//
// api_test.go'daki fakeCatalog burada KULLANILMAZ: o sahte, gömülü nil arayüzü
// sayesinde geçersiz kılınmamış her metotta panikler ve tabloya yeni bir uç
// eklendiğinde testi 403 yerine panikle düşürürdü. Yetki testinin tablosu
// bilinçli olarak TÜM uçları gezer, bu yüzden burada tüm yüzeyi karşılayan
// sessiz bir sahte gerekir.
type yetkiKatalogu struct {
	cagriSayisi int
}

var _ api.Catalog = (*yetkiKatalogu)(nil)

// say bir servis çağrısını kaydeder.
func (f *yetkiKatalogu) say() { f.cagriSayisi++ }

// CreateProduct çağrıyı sayar.
func (f *yetkiKatalogu) CreateProduct(context.Context, service.CreateProductInput) (models.Product, error) {
	f.say()
	return models.Product{}, nil
}

// GetProduct çağrıyı sayar.
func (f *yetkiKatalogu) GetProduct(context.Context, string) (models.Product, error) {
	f.say()
	return models.Product{}, nil
}

// ListProducts çağrıyı sayar.
func (f *yetkiKatalogu) ListProducts(
	context.Context, service.ListProductsOptions,
) (service.ListResult[models.Product], error) {
	f.say()
	return service.ListResult[models.Product]{}, nil
}

// UpdateProduct çağrıyı sayar.
func (f *yetkiKatalogu) UpdateProduct(
	context.Context, string, service.UpdateProductInput,
) (models.Product, error) {
	f.say()
	return models.Product{}, nil
}

// DeleteProduct çağrıyı sayar.
func (f *yetkiKatalogu) DeleteProduct(context.Context, string) error {
	f.say()
	return nil
}

// CreateVariant çağrıyı sayar.
func (f *yetkiKatalogu) CreateVariant(
	context.Context, string, service.CreateVariantInput,
) (models.Variant, error) {
	f.say()
	return models.Variant{}, nil
}

// GetVariant çağrıyı sayar.
func (f *yetkiKatalogu) GetVariant(context.Context, string) (models.Variant, error) {
	f.say()
	return models.Variant{}, nil
}

// ListVariants çağrıyı sayar.
func (f *yetkiKatalogu) ListVariants(
	context.Context, service.ListVariantsOptions,
) (service.ListResult[models.Variant], error) {
	f.say()
	return service.ListResult[models.Variant]{}, nil
}

// UpdateVariant çağrıyı sayar.
func (f *yetkiKatalogu) UpdateVariant(
	context.Context, string, service.UpdateVariantInput,
) (models.Variant, error) {
	f.say()
	return models.Variant{}, nil
}

// DeleteVariant çağrıyı sayar.
func (f *yetkiKatalogu) DeleteVariant(context.Context, string) error {
	f.say()
	return nil
}

// CreateOption çağrıyı sayar.
func (f *yetkiKatalogu) CreateOption(
	context.Context, string, service.CreateOptionInput,
) (models.Option, error) {
	f.say()
	return models.Option{}, nil
}

// ListOptions çağrıyı sayar.
func (f *yetkiKatalogu) ListOptions(context.Context, string) ([]models.Option, error) {
	f.say()
	return nil, nil
}

// AddOptionValue çağrıyı sayar.
func (f *yetkiKatalogu) AddOptionValue(context.Context, string, string) (models.OptionValue, error) {
	f.say()
	return models.OptionValue{}, nil
}

// DeleteOption çağrıyı sayar.
func (f *yetkiKatalogu) DeleteOption(context.Context, string) error {
	f.say()
	return nil
}

// SetVariantPriceSet çağrıyı sayar.
func (f *yetkiKatalogu) SetVariantPriceSet(context.Context, string, string) error {
	f.say()
	return nil
}

// ClearVariantPriceSet çağrıyı sayar.
func (f *yetkiKatalogu) ClearVariantPriceSet(context.Context, string) error {
	f.say()
	return nil
}

// SetVariantInventoryItem çağrıyı sayar.
func (f *yetkiKatalogu) SetVariantInventoryItem(context.Context, string, string) error {
	f.say()
	return nil
}

// ClearVariantInventoryItem çağrıyı sayar.
func (f *yetkiKatalogu) ClearVariantInventoryItem(context.Context, string) error {
	f.say()
	return nil
}

// VariantLinkIDs çağrıyı sayar.
func (f *yetkiKatalogu) VariantLinkIDs(context.Context, string) (service.VariantLinks, error) {
	f.say()
	return service.VariantLinks{}, nil
}

// AddProductSalesChannel çağrıyı sayar.
func (f *yetkiKatalogu) AddProductSalesChannel(context.Context, string, string) error {
	f.say()
	return nil
}

// RemoveProductSalesChannel çağrıyı sayar.
func (f *yetkiKatalogu) RemoveProductSalesChannel(context.Context, string, string) error {
	f.say()
	return nil
}

// ProductSalesChannelIDs çağrıyı sayar.
func (f *yetkiKatalogu) ProductSalesChannelIDs(context.Context, string) ([]string, error) {
	f.say()
	return nil, nil
}

// CreateCollection çağrıyı sayar.
func (f *yetkiKatalogu) CreateCollection(
	context.Context, service.CreateCollectionInput,
) (models.Collection, error) {
	f.say()
	return models.Collection{}, nil
}

// GetCollection çağrıyı sayar.
func (f *yetkiKatalogu) GetCollection(context.Context, string) (models.Collection, error) {
	f.say()
	return models.Collection{}, nil
}

// ListCollections çağrıyı sayar.
func (f *yetkiKatalogu) ListCollections(
	context.Context, int, int,
) (service.ListResult[models.Collection], error) {
	f.say()
	return service.ListResult[models.Collection]{}, nil
}

// CreateCategory çağrıyı sayar.
func (f *yetkiKatalogu) CreateCategory(
	context.Context, service.CreateCategoryInput,
) (models.Category, error) {
	f.say()
	return models.Category{}, nil
}

// GetCategory çağrıyı sayar.
func (f *yetkiKatalogu) GetCategory(context.Context, string) (models.Category, error) {
	f.say()
	return models.Category{}, nil
}

// ListCategories çağrıyı sayar.
func (f *yetkiKatalogu) ListCategories(
	context.Context, service.ListCategoriesOptions,
) (service.ListResult[models.Category], error) {
	f.say()
	return service.ListResult[models.Category]{}, nil
}

// CreateTag çağrıyı sayar.
func (f *yetkiKatalogu) CreateTag(context.Context, string) (models.Tag, error) {
	f.say()
	return models.Tag{}, nil
}

// ListTags çağrıyı sayar.
func (f *yetkiKatalogu) ListTags(context.Context, int, int) (service.ListResult[models.Tag], error) {
	f.say()
	return service.ListResult[models.Tag]{}, nil
}

// ListStoreProducts çağrıyı sayar.
func (f *yetkiKatalogu) ListStoreProducts(
	context.Context, service.StoreListOptions,
) (service.ListResult[service.StoreProduct], error) {
	f.say()
	return service.ListResult[service.StoreProduct]{}, nil
}

// GetStoreProduct çağrıyı sayar.
func (f *yetkiKatalogu) GetStoreProduct(context.Context, string, []string) (service.StoreProduct, error) {
	f.say()
	return service.StoreProduct{}, nil
}

// yetkiliRouter verilen yetkileri taşıyan DOĞRULANMIŞ bir kimlikle router
// kurar.
//
// Hiç yetki verilmemesi geçerli bir durumdur ve "kimliği var ama yetkisi yok"
// çağıranı üretir — arızanın ta kendisi bu kullanıcıydı.
func yetkiliRouter(t *testing.T, scopes ...string) (chi.Router, *yetkiKatalogu) {
	t.Helper()

	svc := &yetkiKatalogu{}
	r := chi.NewRouter()
	r.Use(kimlikVer(scopes...))
	api.New(svc, graph.Options{}).Routes(r)

	return r, svc
}

// kimliksizRouter context'e HİÇ kimlik konmayan bir router kurar.
func kimliksizRouter(t *testing.T) (chi.Router, *yetkiKatalogu) {
	t.Helper()

	svc := &yetkiKatalogu{}
	r := chi.NewRouter()
	api.New(svc, graph.Options{}).Routes(r)

	return r, svc
}

// kimlikVer doğrulanmış bir kimliği context'e koyan middleware döner.
//
// Üretimde bunu corehttp.RequireAdmin yapar; testte kimliği elle koymak, yetki
// katmanını jeton üretimi ve veritabanı olmadan sınamayı sağlar.
func kimlikVer(scopes ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal := corehttp.Principal{ID: "usr_test", Kind: "user", Scopes: scopes}
			next.ServeHTTP(w, r.WithContext(corehttp.WithPrincipal(r.Context(), principal)))
		})
	}
}

// yetkiIstegi bir istek çalıştırır ve yanıt kaydını döner.
//
// api_test.go'daki do'dan ayrı durur çünkü o yardımcı isteğe TAM YETKİLİ bir
// kimlik ekler; burada kimliği testin kendisi belirler.
func yetkiIstegi(t *testing.T, r chi.Router, method, yol, govde string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, yol, strings.NewReader(govde))
	req.Header.Set("Content-Type", "application/json")

	kayit := httptest.NewRecorder()
	r.ServeHTTP(kayit, req)
	return kayit
}

// yetkiHataKodu hata zarfındaki kodu döner.
func yetkiHataKodu(t *testing.T, kayit *httptest.ResponseRecorder) string {
	t.Helper()

	var zarf struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(kayit.Body.Bytes(), &zarf), "gövde: %s", kayit.Body.String())
	return zarf.Error.Code
}

// yazmaUclari [api.ScopeWrite] isteyen tüm yönetim uçlarıdır.
//
// Liste [api.Handler.Routes] ile birlikte büyümelidir: eklenen ama buraya
// yazılmayan bir yazma ucu, sessizce yetkisiz kalabilecek tek yerdir.
var yazmaUclari = map[string]struct {
	method string
	yol    string
	govde  string
}{
	"ürün oluşturma":     {http.MethodPost, "/admin/v1/products", `{}`},
	"ürün güncelleme":    {http.MethodPatch, "/admin/v1/products/prod_1", `{}`},
	"ürün silme":         {http.MethodDelete, "/admin/v1/products/prod_1", ""},
	"varyant oluşturma":  {http.MethodPost, "/admin/v1/products/prod_1/variants", `{}`},
	"varyant güncelleme": {http.MethodPatch, "/admin/v1/variants/var_1", `{}`},
	"varyant silme":      {http.MethodDelete, "/admin/v1/variants/var_1", ""},
	"seçenek oluşturma":  {http.MethodPost, "/admin/v1/products/prod_1/options", `{}`},
	"seçenek değeri":     {http.MethodPost, "/admin/v1/product-options/opt_1/values", `{}`},
	"seçenek silme":      {http.MethodDelete, "/admin/v1/product-options/opt_1", ""},
	"fiyat seti bağlama": {http.MethodPut, "/admin/v1/variants/var_1/price-set", `{}`},
	"fiyat seti kaldırma": {
		http.MethodDelete, "/admin/v1/variants/var_1/price-set", "",
	},
	"stok kalemi bağlama": {http.MethodPut, "/admin/v1/variants/var_1/inventory-item", `{}`},
	"stok kalemi kaldırma": {
		http.MethodDelete, "/admin/v1/variants/var_1/inventory-item", "",
	},
	"satış kanalı bağlama": {
		http.MethodPost, "/admin/v1/products/prod_1/sales-channels", `{}`,
	},
	"satış kanalı kaldırma": {
		http.MethodDelete, "/admin/v1/products/prod_1/sales-channels/sc_1", "",
	},
	"koleksiyon oluşturma": {http.MethodPost, "/admin/v1/product-collections", `{}`},
	"kategori oluşturma":   {http.MethodPost, "/admin/v1/product-categories", `{}`},
	"etiket oluşturma":     {http.MethodPost, "/admin/v1/product-tags", `{}`},
}

// okumaUclari [api.ScopeRead] isteyen tüm yönetim uçlarıdır.
var okumaUclari = map[string]string{
	"ürün listesi":       "/admin/v1/products",
	"tekil ürün":         "/admin/v1/products/prod_1",
	"varyant listesi":    "/admin/v1/products/prod_1/variants",
	"tekil varyant":      "/admin/v1/variants/var_1",
	"seçenek listesi":    "/admin/v1/products/prod_1/options",
	"varyant bağları":    "/admin/v1/variants/var_1/links",
	"satış kanalları":    "/admin/v1/products/prod_1/sales-channels",
	"koleksiyon listesi": "/admin/v1/product-collections",
	"kategori listesi":   "/admin/v1/product-categories",
	"etiket listesi":     "/admin/v1/product-tags",
}

// TestYazmaUcuDarYetkiliCagiraniReddeder yazma uçlarının [api.ScopeWrite]
// istediğini kanıtlar.
//
// Çağıran GERÇEK bir kimliktir ve okuma yetkisi vardır; eksik olan tek şey
// yazma yetkisidir. Arızanın kendisi tam buydu: kimliği doğrulanmış her
// çağıran, yetkisine bakılmadan kataloğu silebiliyordu.
func TestYazmaUcuDarYetkiliCagiraniReddeder(t *testing.T) {
	for ad, tt := range yazmaUclari {
		t.Run(ad, func(t *testing.T) {
			r, svc := yetkiliRouter(t, api.ScopeRead)

			kayit := yetkiIstegi(t, r, tt.method, tt.yol, tt.govde)

			assert.Equal(t, http.StatusForbidden, kayit.Code,
				"okuma yetkili çağıran yazma ucunda 403 almalı; gövde: %s", kayit.Body.String())
			assert.Equal(t, corehttp.CodeForbidden, yetkiHataKodu(t, kayit))
			assert.Zero(t, svc.cagriSayisi,
				"reddedilen istek servise HİÇ ulaşmamalı; yazma reddedilmeden önce yapılmış olurdu")
		})
	}
}

// TestOkumaUcuDarYetkiyleCalisir okuma uçlarının aynı dar kimliği GEÇİRDİĞİNİ
// kanıtlar.
//
// Ayrı bir test olması bilinçlidir: her isteği reddeden bir middleware
// yukarıdaki tabloyu kusursuz geçer ama yönetim yüzeyini tümüyle kilitlerdi.
// [api.ScopeRead] yalnızca yazmayı kapalı tutmak için vardır; okumayı da
// admin'e bağlamak, kataloğu raporlayan dar yetkili bir entegrasyonun tam
// yetki istemesine yol açardı.
func TestOkumaUcuDarYetkiyleCalisir(t *testing.T) {
	for ad, yol := range okumaUclari {
		t.Run(ad, func(t *testing.T) {
			r, svc := yetkiliRouter(t, api.ScopeRead)

			kayit := yetkiIstegi(t, r, http.MethodGet, yol, "")

			assert.Equal(t, http.StatusOK, kayit.Code,
				"okuma yetkisi okuma ucuna yetmeli; gövde: %s", kayit.Body.String())
			assert.Positive(t, svc.cagriSayisi, "istek servise ulaşmalı")
		})
	}
}

// TestYazmaUcuAdminCagiraniKabulEder corehttp.ScopeAdmin'in ÜST YETKİ
// olduğunu, yani "product:write" ayrıca verilmeden de yazmaya yettiğini
// kanıtlar.
func TestYazmaUcuAdminCagiraniKabulEder(t *testing.T) {
	for ad, tt := range yazmaUclari {
		t.Run(ad, func(t *testing.T) {
			r, svc := yetkiliRouter(t, corehttp.ScopeAdmin)

			kayit := yetkiIstegi(t, r, tt.method, tt.yol, tt.govde)

			assert.NotEqual(t, http.StatusForbidden, kayit.Code,
				"admin yazma ucunda 403 ALMAMALI; gövde: %s", kayit.Body.String())
			assert.Positive(t, svc.cagriSayisi, "istek servise ulaşmalı")
		})
	}
}

// TestYetkisizKullaniciKatalogaErisemez yetkisi hiç olmayan bir yönetim
// kullanıcısının hiçbir katalog ucunu çağıramadığını kanıtlar.
//
// auth service.CreateUserInput.Scopes godoc'u boş yetki listesinin "giriş
// yapabilir ama hiçbir yönetim ucuna erişemez" bir kullanıcı ürettiğini
// söylüyor; bu test o cümlenin product tarafındaki karşılığıdır.
func TestYetkisizKullaniciKatalogaErisemez(t *testing.T) {
	for ad, yol := range okumaUclari {
		t.Run("okuma/"+ad, func(t *testing.T) {
			r, svc := yetkiliRouter(t)

			kayit := yetkiIstegi(t, r, http.MethodGet, yol, "")

			assert.Equal(t, http.StatusForbidden, kayit.Code,
				"yetkisiz kullanıcı okuma ucunda 403 almalı; gövde: %s", kayit.Body.String())
			assert.Zero(t, svc.cagriSayisi)
		})
	}

	for ad, tt := range yazmaUclari {
		t.Run("yazma/"+ad, func(t *testing.T) {
			r, svc := yetkiliRouter(t)

			kayit := yetkiIstegi(t, r, tt.method, tt.yol, tt.govde)

			assert.Equal(t, http.StatusForbidden, kayit.Code,
				"yetkisiz kullanıcı yazma ucunda 403 almalı; gövde: %s", kayit.Body.String())
			assert.Zero(t, svc.cagriSayisi)
		})
	}
}

// TestMagazaUclariYetkiIstemez /store/v1 uçlarının yetki SORMADIĞINI
// kanıtlar.
//
// Mağaza yüzeyinin kimliği publishable anahtardır ve o anahtar tanımı gereği
// yetki TAŞIMAZ. Store uçlarına bir scope eklenseydi, hiçbir mağaza istemcisi
// ürün listeleyemezdi — yani vitrin kapanırdı.
func TestMagazaUclariYetkiIstemez(t *testing.T) {
	r, svc := kimliksizRouter(t)

	liste := yetkiIstegi(t, r, http.MethodGet, "/store/v1/products", "")
	assert.Equal(t, http.StatusOK, liste.Code,
		"vitrin listesi yetki istememeli; gövde: %s", liste.Body.String())

	tekil := yetkiIstegi(t, r, http.MethodGet, "/store/v1/products/prod_1", "")
	assert.Equal(t, http.StatusOK, tekil.Code,
		"vitrin tekil ucu yetki istememeli; gövde: %s", tekil.Body.String())

	assert.Equal(t, 2, svc.cagriSayisi)
}

// TestKimliksizYonetimIstegi401Dondurur kimliğin hiç olmadığı durumda yetki
// katmanının 403 DEĞİL 401 döndüğünü kanıtlar.
//
// Ayrım istemci için anlamlıdır: 401 "kim olduğunu söyle", 403 "kim olduğunu
// biliyorum ama yetkin yok" demektir. 403 dönseydi, kimlik başlığını unutan
// bir istemci jetonunu yenilemek yerine yetki istemeye giderdi.
func TestKimliksizYonetimIstegi401Dondurur(t *testing.T) {
	r, svc := kimliksizRouter(t)

	kayit := yetkiIstegi(t, r, http.MethodGet, "/admin/v1/products", "")

	assert.Equal(t, http.StatusUnauthorized, kayit.Code, "gövde: %s", kayit.Body.String())
	assert.Equal(t, "Bearer", kayit.Header().Get("WWW-Authenticate"),
		"RFC 9110: 401 hangi şemanın beklendiğini bildirmeli")
	assert.Zero(t, svc.cagriSayisi)
}
