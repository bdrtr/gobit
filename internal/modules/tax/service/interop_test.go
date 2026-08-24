package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// interopYuzeyi tüketici tarafındaki DAR arayüzün birebir kopyasıdır.
//
// Sepet akışı (internal/workflows/cart) tax modülünü import EDEMEZ ve bu iki
// imzayı kendi paketinde tekrar tanımlayacaktır. Buradaki bildirim, somut
// [Interop] tipinin o arayüzü YAPISAL olarak karşıladığını derleme zamanında
// sabitler: imza değişirse bu test dosyası derlenmez ve uyumsuzluk ancak
// çalışma zamanında çözüm anında görülmek yerine BURADA yakalanır.
type interopYuzeyi interface {
	CalculateTaxJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error)
	RateForCountry(ctx context.Context, countryCode string) (rateBps int32, found bool, err error)
}

var _ interopYuzeyi = (*Interop)(nil)

// newTestInterop bellek içi depo üzerinde çalışan bir interop yüzeyi kurar.
func newTestInterop(t *testing.T) (*Interop, *memRepo) {
	t.Helper()

	svc, repo := newTestService(t)
	return NewInterop(svc), repo
}

// TestCalculateTaxJSONSemasi istek ve yanıt şemasının BELGELENEN alan adlarını
// kullandığını doğrular.
//
// Alan adları dış sözleşmedir: tüketici bu adlarla kendi şemasını yazar ve
// derleyici iki tarafı karşılaştıramaz. Bu yüzden adlar HAM JSON üzerinden
// denetlenir; Go tipleri üzerinden yapılan bir iddia, etiketi değişen bir alanı
// yakalayamazdı.
func TestCalculateTaxJSONSemasi(t *testing.T) {
	interop, repo := newTestInterop(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	raw, err := interop.CalculateTaxJSON(context.Background(), json.RawMessage(`{
		"country_code": "TR",
		"province_code": "",
		"items": [
			{"id": "li_1", "product_id": "prod_1", "product_type_id": "ptyp_1", "amount": 3000}
		],
		"shipping": {"option_id": "sopt_1", "amount": 2500, "taxable": false}
	}`))
	require.NoError(t, err)

	var body map[string]any
	require.NoError(t, json.Unmarshal(raw, &body))

	assert.Equal(t, trRegionID, body["region_id"])
	assert.Equal(t, true, body["region_found"])
	assert.Equal(t, LocalProviderID, body["provider_id"])
	assert.Equal(t, float64(600), body["tax_total"])

	items, ok := body["items"].([]any)
	require.True(t, ok, "items dizi olmalı: %s", raw)
	require.Len(t, items, 1)

	line, ok := items[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "li_1", line["id"])
	assert.Equal(t, rateA, line["rate_id"])
	assert.Equal(t, float64(2000), line["rate_bps"])
	assert.Equal(t, float64(3000), line["taxable_amount"])
	assert.Equal(t, float64(600), line["tax_amount"])

	shipping, ok := body["shipping"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, ShippingLineID, shipping["id"])
	assert.Equal(t, float64(0), shipping["tax_amount"], "kargo istenmedikçe vergilenmez")
}

// TestCalculateTaxJSONKalemSirasiKorunur yanıtın istekteki sırayı koruduğunu
// doğrular.
//
// Sıra sözleşmenin parçasıdır: tüketici kalemleri kimlikle eşleştirmek yerine
// sırayla okumayı seçerse, kararsız bir sıra vergileri satırlar arasında
// kaydırırdı.
func TestCalculateTaxJSONKalemSirasiKorunur(t *testing.T) {
	interop, repo := newTestInterop(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	raw, err := interop.CalculateTaxJSON(context.Background(), json.RawMessage(`{
		"country_code": "TR",
		"items": [
			{"id": "li_c", "amount": 100},
			{"id": "li_a", "amount": 200},
			{"id": "li_b", "amount": 300}
		]
	}`))
	require.NoError(t, err)

	var body struct {
		Items []struct {
			ID        string `json:"id"`
			TaxAmount int64  `json:"tax_amount"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))

	require.Len(t, body.Items, 3)
	assert.Equal(t, "li_c", body.Items[0].ID)
	assert.Equal(t, "li_a", body.Items[1].ID)
	assert.Equal(t, "li_b", body.Items[2].ID)
	assert.Equal(t, int64(20), body.Items[0].TaxAmount)
	assert.Equal(t, int64(40), body.Items[1].TaxAmount)
	assert.Equal(t, int64(60), body.Items[2].TaxAmount)
}

// TestCalculateTaxJSONKargoVergilendirilebilir kargo bayrağının JSON'dan
// geçtiğini doğrular.
func TestCalculateTaxJSONKargoVergilendirilebilir(t *testing.T) {
	interop, repo := newTestInterop(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	raw, err := interop.CalculateTaxJSON(context.Background(), json.RawMessage(`{
		"country_code": "TR",
		"items": [],
		"shipping": {"option_id": "sopt_1", "amount": 2500, "taxable": true}
	}`))
	require.NoError(t, err)

	var body struct {
		TaxTotal int64 `json:"tax_total"`
		Shipping struct {
			TaxableAmount int64 `json:"taxable_amount"`
			TaxAmount     int64 `json:"tax_amount"`
		} `json:"shipping"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))

	assert.Equal(t, int64(2500), body.Shipping.TaxableAmount)
	assert.Equal(t, int64(500), body.Shipping.TaxAmount)
	assert.Equal(t, int64(500), body.TaxTotal)
}

// TestCalculateTaxJSONBolgeYoksaGorunurDoner yapılandırma eksiğinin yanıtta
// AÇIKÇA göründüğünü doğrular.
func TestCalculateTaxJSONBolgeYoksaGorunurDoner(t *testing.T) {
	interop, _ := newTestInterop(t)

	raw, err := interop.CalculateTaxJSON(context.Background(), json.RawMessage(`{
		"country_code": "DE",
		"items": [{"id": "li_1", "amount": 10000}]
	}`))
	require.NoError(t, err)

	var body struct {
		RegionFound bool  `json:"region_found"`
		TaxTotal    int64 `json:"tax_total"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))

	assert.False(t, body.RegionFound)
	assert.Equal(t, int64(0), body.TaxTotal)
}

// TestCalculateTaxJSONBozukIstekReddedilir katı çözümlemeyi doğrular.
func TestCalculateTaxJSONBozukIstekReddedilir(t *testing.T) {
	tests := map[string]string{
		"boş gövde":               ``,
		"bozuk JSON":              `{"country_code":`,
		"bilinmeyen alan":         `{"country_code":"TR","tax_rate":2000}`,
		"kalemde bilinmeyen alan": `{"country_code":"TR","items":[{"id":"li_1","amount":1,"vat":5}]}`,
		"kesirli tutar":           `{"country_code":"TR","items":[{"id":"li_1","amount":30.5}]}`,
		"tutar dize":              `{"country_code":"TR","items":[{"id":"li_1","amount":"3000"}]}`,
		"ikinci belge":            `{"country_code":"TR"}{"country_code":"DE"}`,
	}

	for name, request := range tests {
		t.Run(name, func(t *testing.T) {
			interop, repo := newTestInterop(t)
			repo.seedRootRegion(trRegionID, "TR")

			_, err := interop.CalculateTaxJSON(context.Background(), json.RawMessage(request))
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "hata: %v", err)
			assert.Equal(t, CodeInteropRequestInvalid, errors.CodeOf(err))
			assert.Zero(t, repo.callCount("ResolveTaxRegions"))
		})
	}
}

// TestCalculateTaxJSONServisHatasiYukselir servis doğrulamasının yüzeyden
// geçtiğini doğrular.
func TestCalculateTaxJSONServisHatasiYukselir(t *testing.T) {
	interop, _ := newTestInterop(t)

	_, err := interop.CalculateTaxJSON(context.Background(), json.RawMessage(`{"country_code":"TUR"}`))
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, CodeInvalidInput, errors.CodeOf(err),
		"servis hatası interop koduna dönüştürülmemeli")
}

// TestRateForCountryYuzeyi sade yolun ilkel imzasını doğrular.
func TestRateForCountryYuzeyi(t *testing.T) {
	interop, repo := newTestInterop(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	rate, found, err := interop.RateForCountry(context.Background(), "tr")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, int32(2000), rate)

	rate, found, err = interop.RateForCountry(context.Background(), "DE")
	require.NoError(t, err)
	assert.False(t, found)
	assert.Equal(t, int32(0), rate, "found false iken oran daima sıfır olmalı")
}

// TestRateForCountrySaglayiciyiCagirmaz sade yolun dış sağlayıcıya
// GİTMEDİĞİNİ doğrular.
//
// Aksi hâlde sepetin her turu bir ağ çağrısı üretirdi.
func TestRateForCountrySaglayiciyiCagirmaz(t *testing.T) {
	repo := newMemRepo()
	repo.seedRegion(models.TaxRegion{ID: trRegionID, CountryCode: "TR", ProviderID: "sahte"})
	repo.seedDefaultRate(rateA, trRegionID, 1800)

	stub := &countingProvider{id: "sahte"}
	registry := NewProviderRegistry()
	require.NoError(t, registry.Register(stub))
	interop := NewInterop(New(repo, Options{Providers: registry}))

	rate, found, err := interop.RateForCountry(context.Background(), "TR")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, int32(1800), rate)
	assert.Zero(t, stub.calls, "sade yol sağlayıcıyı çağırmamalı")
}

// TestInteropKurulmamisServis nil servisin panik yerine tipli hata
// döndürdüğünü doğrular.
func TestInteropKurulmamisServis(t *testing.T) {
	var interop *Interop

	_, err := interop.CalculateTaxJSON(context.Background(), json.RawMessage(`{"country_code":"TR"}`))
	require.Error(t, err)
	assert.Equal(t, CodeUnconfigured, errors.CodeOf(err))

	_, _, err = interop.RateForCountry(context.Background(), "TR")
	require.Error(t, err)
	assert.Equal(t, CodeUnconfigured, errors.CodeOf(err))
}

// countingProvider çağrı sayan bir sahte sağlayıcıdır.
type countingProvider struct {
	id    string
	calls int
}

var _ TaxProvider = (*countingProvider)(nil)

// ID sağlayıcının kimliğini döner.
func (p *countingProvider) ID() string { return p.id }

// Calculate çağrıyı sayar ve boş sonuç döner.
func (p *countingProvider) Calculate(_ context.Context, in ProviderInput) (ProviderResult, error) {
	p.calls++
	out := ProviderResult{
		Items:    make([]ProviderItemTax, 0, len(in.Items)),
		Shipping: ProviderItemTax{ID: ShippingLineID},
	}
	for i := range in.Items {
		out.Items = append(out.Items, ProviderItemTax{ID: in.Items[i].ID})
	}
	return out, nil
}
