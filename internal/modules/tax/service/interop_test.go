package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// interopSurface is an exact copy of the NARROW interface on the consumer's
// side.
//
// The cart flow (internal/workflows/cart) CANNOT import the tax module and will
// define these two signatures again in its own package. The declaration here
// pins at compile time that the concrete [Interop] type satisfies that
// interface STRUCTURALLY: if a signature changes, this test file does not
// compile, and the mismatch is caught HERE instead of being seen only at run
// time, at the moment of resolution.
type interopSurface interface {
	CalculateTaxJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error)
	RateForCountry(ctx context.Context, countryCode string) (rateBps int32, found bool, err error)
}

var _ interopSurface = (*Interop)(nil)

// newTestInterop builds an interop surface that runs on an in-memory
// repository.
func newTestInterop(t *testing.T) (*Interop, *memRepo) {
	t.Helper()

	svc, repo := newTestService(t)
	return NewInterop(svc), repo
}

// TestCalculateTaxJSONSchema checks that the request and response schemas use
// the DOCUMENTED field names.
//
// The field names are an external contract: the consumer writes its own schema
// with these names and the compiler cannot compare the two sides. That is why
// the names are checked on the RAW JSON; an assertion made through the Go types
// could not catch a field whose tag changed.
func TestCalculateTaxJSONSchema(t *testing.T) {
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
	require.True(t, ok, "items has to be an array: %s", raw)
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
	assert.Equal(t, float64(0), shipping["tax_amount"], "shipping is not taxed unless asked for")
}

// TestCalculateTaxJSONKeepsTheLineOrder checks that the response keeps the
// order in the request.
//
// The order is part of the contract: if the consumer chooses to read the line
// items in order instead of matching them by id, an unstable order would shift
// the taxes between the lines.
func TestCalculateTaxJSONKeepsTheLineOrder(t *testing.T) {
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

// TestCalculateTaxJSONShippingCanBeTaxed checks that the shipping flag passes
// through the JSON.
func TestCalculateTaxJSONShippingCanBeTaxed(t *testing.T) {
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

// TestCalculateTaxJSONAMissingRegionIsVisible checks that a missing
// configuration shows EXPLICITLY in the response.
func TestCalculateTaxJSONAMissingRegionIsVisible(t *testing.T) {
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

// TestCalculateTaxJSONRejectsAMalformedRequest checks the strict decoding.
func TestCalculateTaxJSONRejectsAMalformedRequest(t *testing.T) {
	tests := map[string]string{
		"empty body":                   ``,
		"malformed JSON":               `{"country_code":`,
		"unknown field":                `{"country_code":"TR","tax_rate":2000}`,
		"unknown field in a line item": `{"country_code":"TR","items":[{"id":"li_1","amount":1,"vat":5}]}`,
		"fractional amount":            `{"country_code":"TR","items":[{"id":"li_1","amount":30.5}]}`,
		"amount as a string":           `{"country_code":"TR","items":[{"id":"li_1","amount":"3000"}]}`,
		"second document":              `{"country_code":"TR"}{"country_code":"DE"}`,
	}

	for name, request := range tests {
		t.Run(name, func(t *testing.T) {
			interop, repo := newTestInterop(t)
			repo.seedRootRegion(trRegionID, "TR")

			_, err := interop.CalculateTaxJSON(context.Background(), json.RawMessage(request))
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "error: %v", err)
			assert.Equal(t, CodeInteropRequestInvalid, errors.CodeOf(err))
			assert.Zero(t, repo.callCount("ResolveTaxRegions"))
		})
	}
}

// TestCalculateTaxJSONSurfacesTheServiceError checks that the service's
// validation passes through the surface.
func TestCalculateTaxJSONSurfacesTheServiceError(t *testing.T) {
	interop, _ := newTestInterop(t)

	_, err := interop.CalculateTaxJSON(context.Background(), json.RawMessage(`{"country_code":"TUR"}`))
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, CodeInvalidInput, errors.CodeOf(err),
		"a service error must not be converted into an interop code")
}

// TestRateForCountrySurface checks the plain path's primitive signature.
func TestRateForCountrySurface(t *testing.T) {
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
	assert.Equal(t, int32(0), rate, "while found is false the rate has to always be zero")
}

// TestRateForCountryDoesNotCallTheProvider checks that the plain path does NOT
// GO to the external provider.
//
// Otherwise every cart round would produce a network call.
func TestRateForCountryDoesNotCallTheProvider(t *testing.T) {
	repo := newMemRepo()
	repo.seedRegion(models.TaxRegion{ID: trRegionID, CountryCode: "TR", ProviderID: "fake"})
	repo.seedDefaultRate(rateA, trRegionID, 1800)

	stub := &countingProvider{id: "fake"}
	registry := NewProviderRegistry()
	require.NoError(t, registry.Register(stub))
	interop := NewInterop(New(repo, Options{Providers: registry}))

	rate, found, err := interop.RateForCountry(context.Background(), "TR")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, int32(1800), rate)
	assert.Zero(t, stub.calls, "the plain path must not call the provider")
}

// TestInteropUnconfiguredService checks that a nil service returns a typed
// error instead of panicking.
func TestInteropUnconfiguredService(t *testing.T) {
	var interop *Interop

	_, err := interop.CalculateTaxJSON(context.Background(), json.RawMessage(`{"country_code":"TR"}`))
	require.Error(t, err)
	assert.Equal(t, CodeUnconfigured, errors.CodeOf(err))

	_, _, err = interop.RateForCountry(context.Background(), "TR")
	require.Error(t, err)
	assert.Equal(t, CodeUnconfigured, errors.CodeOf(err))
}

// countingProvider is a fake provider that counts calls.
type countingProvider struct {
	id    string
	calls int
}

var _ TaxProvider = (*countingProvider)(nil)

// ID returns the provider's id.
func (p *countingProvider) ID() string { return p.id }

// Calculate counts the call and returns an empty result.
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
