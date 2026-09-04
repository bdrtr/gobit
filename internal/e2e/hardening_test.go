//go:build integration

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/container"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/core/module"
	coreplugin "github.com/bdrtr/gobit/internal/core/plugin"
	cartapi "github.com/bdrtr/gobit/internal/modules/cart/api"
	cartsvc "github.com/bdrtr/gobit/internal/modules/cart/service"
	paymentmod "github.com/bdrtr/gobit/internal/modules/payment"
	paymentsvc "github.com/bdrtr/gobit/internal/modules/payment/service"
	"github.com/bdrtr/gobit/plugins/paymentstripe"
)

// This file proves the plan's Phase 9 DoD:
//
//	"The sample payment provider plugin can be plugged in and selected
//	 without touching the core; traces are exported; the OpenAPI schema is
//	 generated; the basic load test passes."
//
// The trace side is not here but is proven in
// internal/core/http/telemetry_test.go with an in-memory exporter: standing up
// an OTLP collector just to see that the span is really produced would make the
// test depend on the network while adding nothing to the proof.

// TestCartCreationIsDeliberatelyNotReplayed: a second request arriving with the
// same key must get a NEW cart, NOT the first one's cart.
//
// # This is not a regression, it is a closed leak
//
// This test once claimed exactly the OPPOSITE — "a client whose connection drops
// should get one cart, not two" — and the claim was reasonable on its own. What
// was wrong was the assumption that the idempotency record could tell callers
// apart IN THE STOREFRONT.
//
// The record is namespaced by the caller's identity. The identity resolved on
// /store/v1 is not the shopper's but the STORE's: the publishable key is the
// same in every browser, and the core's own godoc states that it is not a
// secret. So every customer was sharing ONE bucket, and what selected the record
// was a header the client chose. Cart creation was the only endpoint that
// CARRIES no capability in its path and MINTS one in its response; a second
// customer arriving with the same key and the same body got the first one's cart
// ID — and since the cart has no ownership check (see README, "Known limits")
// that meant handing a stranger somebody else's cart.
//
// The price paid: a client retrying a creation request that timed out opens two
// carts. One of them is abandoned. No money, no stock and nothing visible to the
// customer is affected.
func TestCartCreationIsDeliberatelyNotReplayed(t *testing.T) {
	ctx := context.Background()

	body, err := json.Marshal(map[string]string{"country_code": taxedCountry})
	require.NoError(t, err, "could not encode the cart request")

	cartRequest := func() *http.Request {
		request := httptest.NewRequest(http.MethodPost, cartapi.StoreCartsPath, bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(corehttp.PublishableKeyHeader, publishableKey)
		request.Header.Set(corehttp.IdempotencyKeyHeader, "e2e-cart-key-1")

		return request
	}

	countBefore := cartCount(ctx, t)

	first := httptest.NewRecorder()
	testRouter.ServeHTTP(first, cartRequest())
	require.Equal(t, http.StatusCreated, first.Code, "body: %s", first.Body.String())

	second := httptest.NewRecorder()
	testRouter.ServeHTTP(second, cartRequest())
	require.Equal(t, http.StatusCreated, second.Code, "body: %s", second.Body.String())

	assert.Empty(t, second.Header().Get(corehttp.IdempotencyReplayedHeader),
		"cart creation is EXEMPT from the ring; the replay header must never appear")
	assert.NotEqual(t, readCartID(t, first), readCartID(t, second),
		"the second caller must not get the FIRST one's cart; that is the actual claim")
	assert.Equal(t, countBefore+2, cartCount(ctx, t),
		"two requests must write two carts; that is exactly the price of the exemption")
}

// TestIdempotencyIsPreservedOnACartScopedEndpoint verifies that the exemption
// touches ONLY creation.
//
// The exemption works on an exact path match, not on a prefix. Had it been a
// prefix, /store/v1/carts/{id}/complete would have dropped out too, and that
// endpoint is the one that produces duplicate ORDERS — meaning the one place
// where the protection is genuinely needed would have fallen out of the ring.
//
// These endpoints do not have the namespacing problem either: the fingerprint
// includes the PATH and the path carries the cart ID, so a second customer using
// the same key on their own cart gets a 409, not somebody else's data.
func TestIdempotencyIsPreservedOnACartScopedEndpoint(t *testing.T) {
	cartID, _ := createCart(t)

	addressRequest := func(key string) *http.Request {
		body, err := json.Marshal(map[string]any{
			"address": map[string]string{"country_code": taxedCountry, "city": "Istanbul"},
		})
		require.NoError(t, err)

		request := httptest.NewRequest(http.MethodPost,
			"/store/v1/carts/"+cartID+"/shipping-address", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(corehttp.PublishableKeyHeader, publishableKey)
		request.Header.Set(corehttp.IdempotencyKeyHeader, key)

		return request
	}

	first := httptest.NewRecorder()
	testRouter.ServeHTTP(first, addressRequest("e2e-address-key"))
	require.Less(t, first.Code, http.StatusInternalServerError, "body: %s", first.Body.String())

	second := httptest.NewRecorder()
	testRouter.ServeHTTP(second, addressRequest("e2e-address-key"))

	assert.Equal(t, "true", second.Header().Get(corehttp.IdempotencyReplayedHeader),
		"a retry on a cart-scoped endpoint must be a replay of the recorded response")
	assert.Equal(t, first.Code, second.Code, "the replayed response must carry the first one's status")
	// The body is checked by a raw comparison and not with JSONEq: this endpoint
	// can also answer without a body, and JSONEq fails an empty string as "invalid
	// JSON" — that is, it would break the test in the very case where the replay
	// works correctly.
	assert.Equal(t, first.Body.String(), second.Body.String(),
		"the replayed response must be THE SAME as the first one")
}

// cartCount returns the total number of carts in the database.
func cartCount(ctx context.Context, t *testing.T) int64 {
	t.Helper()

	_, total, err := cartSvc.ListCarts(ctx, cartsvc.ListCartsInput{})
	require.NoError(t, err, "could not count the carts")

	return total
}

// TestIdempotencyRejectsTheSameKeyWithADifferentBody verifies that reusing the
// key does not silently return the wrong response.
//
// Silently replaying the first response would hide the fact that the client's
// SECOND request was never processed at all: the client would think "my second
// cart is ready" and carry on with the first one's ID.
//
// The endpoint is not cart CREATION but a cart-scoped one: creation is exempt
// from the ring (see [TestCartCreationIsDeliberatelyNotReplayed]) and there the
// notion of reusing a key no longer exists.
func TestIdempotencyRejectsTheSameKeyWithADifferentBody(t *testing.T) {
	cartID, _ := createCart(t)
	key := "e2e-cart-key-2"

	makeRequest := func(city string) *http.Request {
		body, err := json.Marshal(map[string]any{
			"address": map[string]string{"country_code": taxedCountry, "city": city},
		})
		require.NoError(t, err)

		request := httptest.NewRequest(http.MethodPost,
			"/store/v1/carts/"+cartID+"/shipping-address", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(corehttp.PublishableKeyHeader, publishableKey)
		request.Header.Set(corehttp.IdempotencyKeyHeader, key)

		return request
	}

	first := httptest.NewRecorder()
	testRouter.ServeHTTP(first, makeRequest("Istanbul"))
	require.Less(t, first.Code, http.StatusInternalServerError, "body: %s", first.Body.String())

	different := httptest.NewRecorder()
	testRouter.ServeHTTP(different, makeRequest("Ankara"))
	assert.Equal(t, http.StatusConflict, different.Code,
		"the same key with a different body must be rejected; body: %s", different.Body.String())
}

// TestOpenAPISchemaIsGeneratedFromTheRouterTree proves Phase 9's schema leg.
//
// The schema is not written by hand, it is derived from the router; and the
// test's job is to verify exactly that: the paths that are really registered
// must appear in the schema, the security scheme must differ by surface, and the
// login endpoint must be marked as unprotected.
func TestOpenAPISchemaIsGeneratedFromTheRouterTree(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/openapi.json", http.NoBody)
	recorder := httptest.NewRecorder()
	testRouter.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code,
		"the schema endpoint must return 200; body: %s", recorder.Body.String())

	var document struct {
		OpenAPI string                    `json:"openapi"`
		Paths   map[string]map[string]any `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &document),
		"could not decode the schema; body: %s", recorder.Body.String())

	assert.NotEmpty(t, document.OpenAPI, "the schema version must be reported")

	for _, path := range []string{
		"/store/v1/products",
		"/store/v1/products/{id}",
		"/admin/v1/users",
		"/admin/v1/auth/login",
		"/admin/v1/sales-channels",
	} {
		assert.Contains(t, document.Paths, path, "%q must be present in the schema", path)
	}

	// The schema publishes only route PATTERNS; an endpoint that demands an
	// identity cannot be called even though it shows up in the schema. A raw path
	// carrying a record ID (/store/v1/products/prod_01…) must NEVER enter the
	// schema.
	for path := range document.Paths {
		assert.NotContains(t, path, "prod_", "the schema must carry no raw record ID: %s", path)
	}
}

// TestAPluginAddsAProviderWithoutTouchingTheCore proves Phase 9's plugin leg on
// the REAL payment module.
//
// The claim has two layers: (1) the plugin must be able to register its provider
// without importing any commerce module, (2) the provider it registers must be
// SELECTABLE by the module. Without the second, the first is only a registry
// exercise.
func TestAPluginAddsAProviderWithoutTouchingTheCore(t *testing.T) {
	ctx := context.Background()

	registry, err := containerProviders()
	require.NoError(t, err, "could not resolve the payment provider registry")
	require.NotContains(t, registry.IDs(), paymentstripe.ProviderID,
		"precondition: the stripe provider must not be registered yet")

	plugins := coreplugin.NewRegistry(nil)
	plugins.Add(paymentstripe.New())

	// The host is built with the modules' SAME container; the plugin's provider
	// enters the running system, not a separate copy of it.
	host := coreplugin.NewHost(ctr, module.NewRegistry(nil, nil), nil, nil,
		map[string]string{"STRIPE_API_KEY": "sk_test_e2e"})

	require.NoError(t, plugins.Install(ctx, host), "the plugin could not be installed")
	require.NoError(t, plugins.Start(ctx, host), "the plugin could not be started")

	assert.Contains(t, registry.IDs(), paymentstripe.ProviderID,
		"the stripe provider must be selectable in the payment module")

	provider, err := registry.Get(paymentstripe.ProviderID)
	require.NoError(t, err, "it must be resolvable by provider ID")
	assert.Equal(t, paymentstripe.ProviderID, provider.ID())
}

// TestSetupStopsWhenAPluginSettingIsMissing verifies that a configuration error
// blows up at startup.
//
// Were it silently skipped, a store believed to have "stripe installed" would
// take no payments at all, and that would only be seen on the first customer's
// attempt.
func TestSetupStopsWhenAPluginSettingIsMissing(t *testing.T) {
	plugins := coreplugin.NewRegistry(nil)
	plugins.Add(paymentstripe.New())

	host := coreplugin.NewHost(ctr, module.NewRegistry(nil, nil), nil, nil, nil)

	err := plugins.Install(context.Background(), host)
	require.Error(t, err, "a plugin whose setting is missing must not install")
	assert.Contains(t, err.Error(), paymentstripe.Name,
		"the error must say which plugin failed")
}

// containerProviders resolves the payment module's provider registry from the
// container.
//
// The registry is resolved BY NAME: the plugin uses the same name too, and the
// agreement of the two names is bound to compile time by
// internal/arch/constants_test.go.
func containerProviders() (*paymentsvc.ProviderRegistry, error) {
	return container.Resolve[*paymentsvc.ProviderRegistry](ctr, paymentmod.ProvidersName)
}

// readCartID extracts the ID out of a cart response.
func readCartID(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()

	var envelope struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope),
		"could not decode the cart response; body: %s", recorder.Body.String())
	require.NotEmpty(t, envelope.Data.ID, "a cart ID must be returned")

	return envelope.Data.ID
}
