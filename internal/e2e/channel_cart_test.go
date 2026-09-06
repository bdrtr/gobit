//go:build integration

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	pricingsvc "github.com/bdrtr/gobit/internal/modules/pricing/service"
	productmodels "github.com/bdrtr/gobit/internal/modules/product/models"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
)

// This file proves, with the REAL stack, that the sales channel scope is
// enforced on the WRITE path as well:
//
//	A client arriving with channel B's publishable key CANNOT ADD to its
//	cart a variant that is sold in channel A only.
//
// # Why it goes through HTTP
//
// The claim is not a workflow claim. For it to work correctly four layers have
// to line up: auth has to resolve the key's channels, the core's protection
// stack has to put the identity into the context, the cart workflow has to read
// the channels from THAT identity, and product's Query provider has to actually
// apply the filter. A test that called the workflow directly would stay green
// even with the first three links of the chain broken — and the hole is
// exploited exactly there, at the HTTP end.
//
// # Why a second key
//
// With a single key the observation "the variant cannot be added" proves
// nothing: the variant may have been deleted, it may have no price, or the
// product may be a draft; a gate that rejected everything would pass that test
// too. The SAME variant being addable with ITS OWN channel's key is the only
// observation that says the reason for the rejection is exactly the channel.
//
// # Symmetry with the read surface
//
// The channel catalog tests (channel_catalog_test.go) set up the same ground for
// the READ surface. This file is its mirror image and shares its fixture: the
// two surfaces giving the same answer on the SAME setup is what says the scope
// is a single rule.

// channelCart is the set-up ground of the write path scenario.
type channelCart struct {
	// firstChannelVariant is the variant of the product assigned to
	// [testChannelID] ONLY.
	firstChannelVariant string
	// secondChannelVariant is the variant of the product assigned to the second
	// channel ONLY.
	secondChannelVariant string
	// unassignedVariant is the variant of the product assigned to no channel at
	// all; by the rule it has to be sellable in BOTH storefronts.
	unassignedVariant string
}

// The state needed so that the fixture is set up only once.
var (
	// channelCartOnce makes sure the ground is set up only once.
	channelCartOnce sync.Once
	// channelCartGround is the set-up ground.
	channelCartGround channelCart
	// channelCartErr carries the setup error to the tests.
	channelCartErr error
)

// channelCartFixture prepares the three priced variants and the channel
// assignments.
//
// The setup rides on top of [channelCatalogFixture] (the second channel and the
// second key come from there) but it sets up its OWN products: the three
// products over there have neither a variant nor a price, and a variant that is
// to enter a cart needs both. Pricing those same products would have changed
// the set the read tests count.
func channelCartFixture(t *testing.T) channelCart {
	t.Helper()

	ground := channelCatalogFixture(t)

	channelCartOnce.Do(func() {
		// The setup context is NOT t.Context(); the reason is the same as in
		// [channelCatalogFixture].
		channelCartGround, channelCartErr = setUpChannelCart(context.Background(), ground.secondChannelID)
	})
	require.NoError(t, channelCartErr, "the channel cart fixture could not be set up")

	return channelCartGround
}

// setUpChannelCart sets up the three variants and binds two of them to one
// channel each.
func setUpChannelCart(ctx context.Context, secondChannelID string) (channelCart, error) {
	var ground channelCart
	var err error

	if ground.firstChannelVariant, err = setUpChannelCartVariant(ctx, "first", testChannelID); err != nil {
		return ground, err
	}
	if ground.secondChannelVariant, err = setUpChannelCartVariant(ctx, "second", secondChannelID); err != nil {
		return ground, err
	}
	if ground.unassignedVariant, err = setUpChannelCartVariant(ctx, "unassigned", ""); err != nil {
		return ground, err
	}

	return ground, nil
}

// setUpChannelCartVariant sets up a published product, a priced variant and (if
// asked for) a channel bond; it returns the VARIANT identity.
//
// The product is PUBLISHED and the variant is PRICED: had either been missing,
// adding the line would already have been rejected, and the test would be
// measuring some other gate rather than the one it wants to measure (the
// channel).
func setUpChannelCartVariant(ctx context.Context, name, channelID string) (string, error) {
	handle := "e2e-channel-cart-" + name

	product, err := productSvc.CreateProduct(ctx, productsvc.CreateProductInput{
		Handle: handle,
		Title:  "E2E Channel Cart " + name,
		Status: productmodels.StatusPublished,
	})
	if err != nil {
		return "", fmt.Errorf("the %q product could not be set up: %w", handle, err)
	}

	variant, err := productSvc.CreateVariant(ctx, product.ID, productsvc.CreateVariantInput{
		Title: "One size",
	})
	if err != nil {
		return "", fmt.Errorf("the %q variant could not be set up: %w", handle, err)
	}

	priceSet, err := pricingSvc.CreatePriceSet(ctx, []pricingsvc.PriceInput{{
		CurrencyCode: taxedCurrency,
		Amount:       1000,
		MinQuantity:  1,
	}})
	if err != nil {
		return "", fmt.Errorf("the %q price set could not be set up: %w", handle, err)
	}
	if err := productSvc.SetVariantPriceSet(ctx, variant.ID, priceSet.ID); err != nil {
		return "", fmt.Errorf("the %q price bond could not be set up: %w", handle, err)
	}

	if channelID != "" {
		if err := bindChannel(product.ID, channelID); err != nil {
			return "", err
		}
	}

	return variant.ID, nil
}

// keyedStorefrontRequest makes a store request with the GIVEN publishable key.
//
// [storefrontRequest] is this same helper pinned to the shared key; the two were
// split apart for the two-key scenarios. Producing a third copy would have been
// the easiest way to build a request that does not go through the protection
// stack.
func keyedStorefrontRequest(t *testing.T, key, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(corehttp.PublishableKeyHeader, key)

	recorder := httptest.NewRecorder()
	testRouter.ServeHTTP(recorder, request)

	return recorder
}

// openCartWithKey opens a cart with the given key and returns its identity.
func openCartWithKey(t *testing.T, key string) string {
	t.Helper()

	recorder := keyedStorefrontRequest(t, key, http.MethodPost, "/store/v1/carts",
		fmt.Sprintf(`{"country_code":%q}`, taxedCountry))
	require.Equal(t, http.StatusCreated, recorder.Code,
		"the cart must open; body: %s", recorder.Body.String())

	id, ok := storefrontData(t, recorder)["id"].(string)
	require.True(t, ok, "the opened cart must carry an identity; body: %s", recorder.Body.String())

	return id
}

// tryAddLineItem tries, with the given key, to add a line to the cart.
func tryAddLineItem(t *testing.T, key, cartID, variantID string) *httptest.ResponseRecorder {
	t.Helper()

	return keyedStorefrontRequest(t, key, http.MethodPost,
		"/store/v1/carts/"+cartID+"/line-items",
		fmt.Sprintf(`{"variant_id":%q,"quantity":1}`, variantID))
}

// cartLineCount reads the cart's line count from the storefront endpoint.
//
// The number is read from the cart's OWN record and not from a value the
// workflow returned: the claim under test is that the rejected request WRITES
// NOTHING to the cart.
func cartLineCount(t *testing.T, key, cartID string) int {
	t.Helper()

	recorder := keyedStorefrontRequest(t, key, http.MethodGet, "/store/v1/carts/"+cartID, "")
	require.Equal(t, http.StatusOK, recorder.Code, "the cart must be readable; body: %s", recorder.Body.String())

	var envelope struct {
		Data struct {
			Items []json.RawMessage `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope),
		"the cart response could not be decoded; body: %s", recorder.Body.String())

	return len(envelope.Data.Items)
}

// TestAForeignChannelsVariantCannotBeAddedToTheCart is the claim that closes
// the hole itself.
//
// This request used to return 201: the cart workflow read the variant BY
// IDENTITY ALONE and never asked for the request's sales channels. A product
// hidden in the storefront could be sold in the cart, which is to say the scope
// rule was not an authorization but a display preference.
func TestAForeignChannelsVariantCannotBeAddedToTheCart(t *testing.T) {
	ground := channelCartFixture(t)
	catalog := channelCatalogFixture(t)

	cartID := openCartWithKey(t, publishableKey)
	rejected := tryAddLineItem(t, publishableKey, cartID, ground.secondChannelVariant)

	assert.Equal(t, http.StatusNotFound, rejected.Code,
		"another channel's variant MUST NOT ENTER the cart; body: %s", rejected.Body.String())
	assert.Zero(t, cartLineCount(t, publishableKey, cartID),
		"a rejected request MUST NOT WRITE a line to the cart")

	// The reverse direction holds as well: the rule is not a one-way barrier,
	// it is the two storefronts being closed to each other's catalog.
	secondCart := openCartWithKey(t, catalog.ikinciAnahtar)
	reverseRejected := tryAddLineItem(t, catalog.ikinciAnahtar, secondCart, ground.firstChannelVariant)

	assert.Equal(t, http.StatusNotFound, reverseRejected.Code,
		"the first channel's variant must not be addable in the second storefront either; body: %s",
		reverseRejected.Body.String())
	assert.Zero(t, cartLineCount(t, catalog.ikinciAnahtar, secondCart))
}

// TestItsOwnChannelsVariantIsAddedToTheCart proves that the gate does not
// reject EVERYTHING.
//
// Without this claim the test above is worthless: a change that broke the
// catalog read entirely, or that closed off line addition altogether, would
// pass it too. The same variant being rejected with one key and accepted with
// the other is the only observation that says the reason for the rejection is
// exactly the CHANNEL.
func TestItsOwnChannelsVariantIsAddedToTheCart(t *testing.T) {
	ground := channelCartFixture(t)
	catalog := channelCatalogFixture(t)

	cartID := openCartWithKey(t, publishableKey)
	accepted := tryAddLineItem(t, publishableKey, cartID, ground.firstChannelVariant)
	require.Equal(t, http.StatusCreated, accepted.Code,
		"its own channel's variant must enter the cart; body: %s", accepted.Body.String())
	assert.Equal(t, 1, cartLineCount(t, publishableKey, cartID))

	// The variant rejected in the first storefront must be accepted in ITS OWN
	// storefront.
	secondCart := openCartWithKey(t, catalog.ikinciAnahtar)
	ownStorefront := tryAddLineItem(t, catalog.ikinciAnahtar, secondCart, ground.secondChannelVariant)
	assert.Equal(t, http.StatusCreated, ownStorefront.Code,
		"the hidden variant must be sellable in the storefront it belongs to; body: %s", ownStorefront.Body.String())
	assert.Equal(t, 1, cartLineCount(t, catalog.ikinciAnahtar, secondCart))
}

// TestAnUnassignedVariantEntersTheCartInEveryStorefront verifies that the
// BACKWARD COMPATIBLE half of the rule holds on the write path as well.
//
// A product with no assignment is visible in every channel; were it not, this
// change would break ALL the carts of the existing setups that use no channel
// assignment at all, overnight. The read surface carries the same claim
// (see TestTheStorefrontCatalogIsFilteredByTheRequestsSalesChannel).
func TestAnUnassignedVariantEntersTheCartInEveryStorefront(t *testing.T) {
	ground := channelCartFixture(t)
	catalog := channelCatalogFixture(t)

	for name, key := range map[string]string{
		"first storefront":  publishableKey,
		"second storefront": catalog.ikinciAnahtar,
	} {
		t.Run(name, func(t *testing.T) {
			cartID := openCartWithKey(t, key)
			recorder := tryAddLineItem(t, key, cartID, ground.unassignedVariant)

			assert.Equal(t, http.StatusCreated, recorder.Code,
				"the unassigned product's variant must be sellable in every storefront; body: %s", recorder.Body.String())
			assert.Equal(t, 1, cartLineCount(t, key, cartID))
		})
	}
}

// TestAnOutOfScopeVariantDoesNotRevealItsExistence verifies that the rejection
// of a foreign channel's variant cannot be told apart from the rejection of a
// variant that DOES NOT EXIST AT ALL.
//
// Could they be told apart, the hiding would be pierced: a competitor arriving
// with the publishable key in hand would learn, by trying variant identities,
// which of them are sold in ANOTHER channel. The read surface carries the same
// claim (see TestAHiddenProductDoesNotRevealItselfViaTheErrorCode) and there
// too the comparison is over the error CODE only; because the message echoes
// the requested identity it already differs between the two requests.
func TestAnOutOfScopeVariantDoesNotRevealItsExistence(t *testing.T) {
	ground := channelCartFixture(t)

	cartID := openCartWithKey(t, publishableKey)
	hidden := tryAddLineItem(t, publishableKey, cartID, ground.secondChannelVariant)
	missing := tryAddLineItem(t, publishableKey, cartID, "variant_e2e_no_such_variant")

	require.Equal(t, http.StatusNotFound, hidden.Code, "body: %s", hidden.Body.String())
	require.Equal(t, http.StatusNotFound, missing.Code, "body: %s", missing.Body.String())

	assert.Equal(t, errorSummary(t, missing)[0], errorSummary(t, hidden)[0],
		"an out-of-scope variant and a nonexistent variant must return the SAME error code")
}
