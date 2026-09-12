package storefront_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"example.com/gobit-storefront/storefront"
)

// The shop is tested through its ROUTER, because that is what a browser meets.
//
// The module holds no service and reads no database, so there is nothing else to
// test: what can break is a page that stops rendering, a value that stops
// reaching the markup, and a script served at an address the page does not ask
// for. All three are answerable from the response.

// The values a test shop is configured with. They are shaped like the real ones
// and carry no meaning; nothing in this module validates their form.
const (
	testKey     = "pk_test_storefront"
	testChannel = "sc_test_storefront"
)

// shopRouter builds the shop and binds it, the way a gobit installation does.
func shopRouter(t *testing.T) chi.Router {
	t.Helper()

	shop, err := storefront.New(storefront.Options{
		PublishableKey: testKey,
		SalesChannelID: testChannel,
	})
	if err != nil {
		t.Fatalf("the shop could not be built: %v", err)
	}

	r := chi.NewRouter()
	shop.Routes(r)

	return r
}

// get requests a path and returns the recorder.
func get(t *testing.T, r chi.Router, path string) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, http.NoBody))

	return rec
}

// TestTheShopRefusesToStartWithoutWhatItCannotDiscover pins the startup refusal.
//
// Neither value is readable from the store surface: no endpoint lists sales
// channels and a publishable key is returned once, by the admin endpoint that
// mints it. A shop that started without them would answer 401 to every request
// its own page makes and look like an empty catalog — which is a shop that is
// wrong and says nothing.
func TestTheShopRefusesToStartWithoutWhatItCannotDiscover(t *testing.T) {
	t.Parallel()

	for name, opts := range map[string]storefront.Options{
		"no key":     {SalesChannelID: testChannel},
		"no channel": {PublishableKey: testKey},
		"neither":    {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := storefront.New(opts); err == nil {
				t.Fatal("the shop started without a value it cannot discover")
			}
		})
	}
}

// TestEveryPageCarriesWhatTheScriptNeeds is the shell's contract.
//
// The script is one file for every installation, so the key, the channel and the
// product handle travel as attributes. A page that stopped carrying one of them
// would render, and the script would then fetch with an empty key and get a 401 —
// a failure that looks like an outage rather than like a missing attribute.
func TestEveryPageCarriesWhatTheScriptNeeds(t *testing.T) {
	t.Parallel()

	r := shopRouter(t)

	for name, path := range map[string]string{
		"the catalog": storefront.ListPath,
		"one product": storefront.ListPath + "/products/a-handle",
		"the cart":    storefront.CartPath,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec := get(t, r, path)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s answered %d", path, rec.Code)
			}

			body := rec.Body.String()
			for _, want := range []string{
				`data-key="` + testKey + `"`,
				`data-channel="` + testChannel + `"`,
				`<script src="` + storefront.ScriptPath,
				"<noscript>",
			} {
				if !strings.Contains(body, want) {
					t.Errorf("%s does not carry %q", path, want)
				}
			}
		})
	}
}

// TestTheProductPageCarriesItsHandle is what makes the product page a page.
//
// The handle is the only thing that distinguishes it from the catalog, and the
// script reads it from the markup rather than from the address — a page that
// dropped it would show the catalog under a product's title.
func TestTheProductPageCarriesItsHandle(t *testing.T) {
	t.Parallel()

	rec := get(t, shopRouter(t), storefront.ListPath+"/products/a-shirt")

	if !strings.Contains(rec.Body.String(), `data-handle="a-shirt"`) {
		t.Error("the product page does not carry the handle it was asked for")
	}
}

// TestTheScriptIsServedAtTheAddressThePageAsksFor ties the two halves.
//
// The response says `immutable`, so a browser that has the old copy will not ask
// again for a year. That is only honest when a changed file is asked for at a
// changed address, which is why the stamp is in the query — and why the page's
// address and the served stamp have to agree.
func TestTheScriptIsServedAtTheAddressThePageAsksFor(t *testing.T) {
	t.Parallel()

	r := shopRouter(t)

	served := get(t, r, storefront.ScriptPath)
	if served.Code != http.StatusOK {
		t.Fatalf("the script answered %d", served.Code)
	}

	stamp := strings.Trim(served.Header().Get("ETag"), `"`)
	if stamp == "" {
		t.Fatal("the script is served with no stamp")
	}

	page := get(t, r, storefront.ListPath).Body.String()
	if !strings.Contains(page, storefront.ScriptPath+"?v="+stamp) {
		t.Errorf("the page asks for the script at an address that does not carry the "+
			"stamp %q the route serves", stamp)
	}
}

// TestEveryPageCarriesTheShopsPolicy is the content policy's population.
//
// It is the shop's OWN policy and not the panel's: a catalog shows product
// images and the panel's starts at default-src 'none' with no img-src. Nothing
// published carries a policy for an embedder's pages, so this one is written in
// the example — and a page that escaped the group would carry none at all.
func TestEveryPageCarriesTheShopsPolicy(t *testing.T) {
	t.Parallel()

	r := shopRouter(t)

	for _, path := range []string{
		storefront.ListPath,
		storefront.ListPath + "/products/a-handle",
		storefront.CartPath,
		storefront.ScriptPath,
	} {
		rec := get(t, r, path)

		policy := rec.Header().Get("Content-Security-Policy")
		if policy == "" {
			t.Errorf("%s answers with no content policy", path)

			continue
		}
		if !strings.Contains(policy, "img-src") {
			t.Errorf("%s carries a policy with no img-src; a catalog shows product "+
				"images and would silently show none", path)
		}
		if rec.Header().Get("X-Frame-Options") != "DENY" {
			t.Errorf("%s can be framed", path)
		}
	}
}
