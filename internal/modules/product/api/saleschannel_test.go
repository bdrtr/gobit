package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// Bu dosya vitrin uçlarının satış kanallarını NEREDEN okuduğunu ve ürün ↔ kanal
// yönetim uçlarının kablolamasını sınar.

// magazaIstegi verilen kimlikle bir mağaza isteği çalıştırır.
//
// principal nil ise context'e HİÇ kimlik konmaz; bu, mağaza kimlik
// doğrulamasının bağlanmadığı kurulumu temsil eder. Üretimde kimliği
// corehttp.RequireStore koyar.
func magazaIstegi(
	t *testing.T,
	r chi.Router,
	target string,
	principal *corehttp.Principal,
) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, target, strings.NewReader(""))
	if principal != nil {
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), *principal))
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestStoreListReadsChannelsFromPrincipal vitrin listesinin satış kanallarını
// DOĞRULANMIŞ KİMLİKTEN okuduğunu doğrular.
func TestStoreListReadsChannelsFromPrincipal(t *testing.T) {
	t.Parallel()

	var got service.StoreListOptions
	catalog := &fakeCatalog{
		listStoreProducts: func(
			_ context.Context, opts service.StoreListOptions,
		) (service.ListResult[service.StoreProduct], error) {
			got = opts
			return service.ListResult[service.StoreProduct]{}, nil
		},
	}

	rec := magazaIstegi(t, newRouter(catalog), "/store/v1/products", &corehttp.Principal{
		ID: "apk_1", Kind: "api_key", SalesChannelIDs: []string{"sc_a", "sc_b"},
	})
	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())
	assert.Equal(t, []string{"sc_a", "sc_b"}, got.SalesChannelIDs,
		"kanallar anahtarın kimliğinden gelmeli")
}

// TestStoreListIgnoresChannelQueryParam kanalın SORGU DİZESİNDEN
// okunmadığını doğrular.
//
// Arızanın en tehlikeli biçimi budur: kanal istemcinin bildirdiği bir değer
// olsaydı, elindeki herhangi bir publishable anahtarla gelen bir istemci BAŞKA
// bir kanalın kataloğunu okuyabilir ve süzgeç bir yetkilendirme olmaktan çıkıp
// görüntüleme tercihine dönüşürdü.
func TestStoreListIgnoresChannelQueryParam(t *testing.T) {
	t.Parallel()

	var got service.StoreListOptions
	catalog := &fakeCatalog{
		listStoreProducts: func(
			_ context.Context, opts service.StoreListOptions,
		) (service.ListResult[service.StoreProduct], error) {
			got = opts
			return service.ListResult[service.StoreProduct]{}, nil
		},
	}

	rec := magazaIstegi(t, newRouter(catalog),
		"/store/v1/products?sales_channel_id=sc_baskasi&sales_channel_ids=sc_baskasi",
		&corehttp.Principal{ID: "apk_1", Kind: "api_key", SalesChannelIDs: []string{"sc_a"}})
	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())
	assert.Equal(t, []string{"sc_a"}, got.SalesChannelIDs,
		"sorgu dizesindeki kanal YOK SAYILMALI; kaynak yalnızca kimliktir")
}

// TestStoreListWithoutPrincipalPassesNil kimliksiz istekte süzgecin hiç
// uygulanmadığını doğrular.
//
// nil, servis sözleşmesinde "süzme yok" demektir; mağaza kimlik doğrulaması
// bağlanmamış bir kurulumda (product tek başına) vitrin böyle çalışmaya devam
// eder.
func TestStoreListWithoutPrincipalPassesNil(t *testing.T) {
	t.Parallel()

	var got service.StoreListOptions
	catalog := &fakeCatalog{
		listStoreProducts: func(
			_ context.Context, opts service.StoreListOptions,
		) (service.ListResult[service.StoreProduct], error) {
			got = opts
			return service.ListResult[service.StoreProduct]{}, nil
		},
	}

	rec := magazaIstegi(t, newRouter(catalog), "/store/v1/products", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Nil(t, got.SalesChannelIDs, "kimlik yoksa süzgeç uygulanmamalı")
}

// TestStoreListWithChannellessPrincipalPassesEmptySet kanalsız bir kimliğin
// nil DEĞİL boş küme ürettiğini doğrular.
//
// İkisi bir tutulsaydı kanalsız bir kimlik "süzme yok" dalından geçer ve TÜM
// kanalların katalogunu okurdu. Ayrım yalnızca burada, kimliği okuyan yerde
// korunabilir.
func TestStoreListWithChannellessPrincipalPassesEmptySet(t *testing.T) {
	t.Parallel()

	var got service.StoreListOptions
	catalog := &fakeCatalog{
		listStoreProducts: func(
			_ context.Context, opts service.StoreListOptions,
		) (service.ListResult[service.StoreProduct], error) {
			got = opts
			return service.ListResult[service.StoreProduct]{}, nil
		},
	}

	rec := magazaIstegi(t, newRouter(catalog), "/store/v1/products",
		&corehttp.Principal{ID: "apk_1", Kind: "api_key"})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotNil(t, got.SalesChannelIDs, "kanalsız kimlik nil DEĞİL boş küme üretmeli")
	assert.Empty(t, got.SalesChannelIDs)
}

// TestStoreGetProductPassesChannels tekil ucun da kanalları taşıdığını
// doğrular; listede gizleyip tekil uçta göstermek gizlemeyi anlamsız kılardı.
func TestStoreGetProductPassesChannels(t *testing.T) {
	t.Parallel()

	var got []string
	catalog := &fakeCatalog{
		getStoreProduct: func(_ context.Context, _ string, channels []string) (service.StoreProduct, error) {
			got = channels
			return service.StoreProduct{}, nil
		},
	}

	rec := magazaIstegi(t, newRouter(catalog), "/store/v1/products/tisort",
		&corehttp.Principal{ID: "apk_1", Kind: "api_key", SalesChannelIDs: []string{"sc_a"}})
	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())
	assert.Equal(t, []string{"sc_a"}, got)
}

// TestAdminAddSalesChannelReturnsCurrentList bağlama ucunun servise doğru
// kimlikleri geçirdiğini ve GÜNCEL listeyi döndürdüğünü doğrular.
func TestAdminAddSalesChannelReturnsCurrentList(t *testing.T) {
	t.Parallel()

	var gotProduct, gotChannel string
	catalog := &fakeCatalog{
		addSalesChannel: func(_ context.Context, productID, channelID string) error {
			gotProduct, gotChannel = productID, channelID
			return nil
		},
		salesChannelIDs: func(context.Context, string) ([]string, error) {
			return []string{"sc_a"}, nil
		},
	}

	rec := do(t, newRouter(catalog), http.MethodPost, "/admin/v1/products/prod_1/sales-channels",
		`{"sales_channel_id": "sc_a"}`)
	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())
	assert.Equal(t, "prod_1", gotProduct)
	assert.Equal(t, "sc_a", gotChannel)

	data, ok := decodeBody(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "prod_1", data["product_id"])
	assert.Equal(t, []any{"sc_a"}, data["sales_channel_ids"])
}

// TestAdminRemoveSalesChannelReadsChannelFromPath kaldırma ucunun kanal
// kimliğini YOLDAN okuduğunu doğrular.
func TestAdminRemoveSalesChannelReadsChannelFromPath(t *testing.T) {
	t.Parallel()

	var gotProduct, gotChannel string
	catalog := &fakeCatalog{
		removeSalesChannel: func(_ context.Context, productID, channelID string) error {
			gotProduct, gotChannel = productID, channelID
			return nil
		},
		salesChannelIDs: func(context.Context, string) ([]string, error) { return nil, nil },
	}

	rec := do(t, newRouter(catalog), http.MethodDelete,
		"/admin/v1/products/prod_1/sales-channels/sc_a", "")
	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())
	assert.Equal(t, "prod_1", gotProduct)
	assert.Equal(t, "sc_a", gotChannel)

	assert.Contains(t, rec.Body.String(), `"sales_channel_ids":[]`,
		"boş liste null değil boş dizi olmalı: %s", rec.Body.String())
}

// TestAdminSalesChannelErrorKeepsErrorClass servisin tipli hatasının HTTP
// koduna elle değil SINIFINDAN çevrildiğini doğrular.
func TestAdminSalesChannelErrorKeepsErrorClass(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{
		addSalesChannel: func(context.Context, string, string) error {
			return coreerrors.NotFound("product_not_found", "ürün bulunamadı: prod_yok")
		},
	}

	rec := do(t, newRouter(catalog), http.MethodPost, "/admin/v1/products/prod_yok/sales-channels",
		`{"sales_channel_id": "sc_a"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code, "gövde: %s", rec.Body.String())
}
