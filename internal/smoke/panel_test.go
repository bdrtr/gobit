//go:build smoke

package smoke

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/adminui"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
)

// panelLink finds the first link of a kind on a panel page: the way an
// operator goes from a list to a record.
func panelLink(t *testing.T, page, kind string) string {
	t.Helper()

	// A record's id carries its prefix's underscore; a list also links to a
	// form of its own (the product list's "new", ADR 0307), which is no record.
	found := regexp.MustCompile(`href="(` + regexp.QuoteMeta(adminui.URLPrefix) + `/` + kind + `/[A-Za-z0-9]+_[A-Za-z0-9_]+)"`).FindStringSubmatch(page)
	require.NotNil(t, found, "the page links to no %s; it renders an empty list", kind)

	return found[1]
}

// TestThePanelRendersAgainstARealDatabase closes a known limit: every panel
// gate runs over a real router with fake services, and nothing proved that the
// panel renders what a real database holds. A real process is given a product,
// a customer and an order through its own API; an operator signs in through
// the panel's form and opens every screen, going from each list to a record by
// the list's own link, and each answers 200 with what the database holds.
func TestThePanelRendersAgainstARealDatabase(t *testing.T) {
	dsn := scenarioDatabase(t)
	cfg := baseSettings(dsn, freePort(t))
	cfg["ADMIN_BOOTSTRAP_EMAIL"] = seedEmail
	cfg["ADMIN_BOOTSTRAP_PASSWORD"] = seedPassword
	s := startServer(t, cfg)
	s.waitForReady(startupTimeout)

	token, _, storefrontKey := setUpAdminHarness(t, s, "Smoke Panel Channel")
	regionID := openStorefrontRegion(t, s, token)
	variantID := setUpStorefrontCatalog(t, s, token)
	const customerEmail = "smoke-panel-customer@example.test"
	b2bOpenCustomer(t, s, token, customerEmail)
	const orderEmail = "smoke-panel-order@example.test"
	cartID := openStorefrontCart(t, s, storefrontKey, regionID, orderEmail)
	status, body := s.storefrontRequest(http.MethodPost, "/store/v1/carts/"+cartID+"/line-items", storefrontKey,
		map[string]any{"variant_id": variantID, "quantity": storefrontQuantity})
	require.Equal(t, http.StatusCreated, status, body)
	status, body = s.storefrontRequest(http.MethodPost, "/store/v1/carts/"+cartID+"/complete", storefrontKey,
		map[string]any{
			"payment_provider_id": manual.ID,
			"payment_data":        map[string]any{manual.DataKeyOutcome: manual.OutcomeAuthorize},
			"expected_total":      storefrontTotal,
		})
	require.Equal(t, http.StatusOK, status, body)

	// Sign in through the form, as an operator does.
	form := url.Values{"email": {seedEmail}, "password": {seedPassword}}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, s.addr+adminui.LoginPath, strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", s.addr)
	client := &http.Client{Timeout: requestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusSeeOther, resp.StatusCode, "the sign-in is answered with a redirect")
	var session *http.Cookie
	for _, cookie := range resp.Cookies() {
		if cookie.Name == adminui.CookieName {
			session = cookie
		}
	}
	require.NotNil(t, session, "the sign-in sets the panel's session cookie")

	open := func(path string) string {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, s.addr+path, http.NoBody)
		require.NoError(t, err)
		req.AddCookie(session)
		code, page := s.send(req)
		require.Equal(t, http.StatusOK, code, "%s renders against the real database:\n%s\n%s", path, page, s.logBuf())
		return page
	}

	require.Contains(t, open(adminui.URLPrefix), "</html>")
	products := open(adminui.ProductsPath)
	require.Contains(t, products, storefrontVariantTitle)
	product := open(panelLink(t, products, "products"))
	require.Contains(t, product, storefrontVariantTitle)
	orders := open(adminui.OrdersPath)
	order := open(panelLink(t, orders, "orders"))
	require.Contains(t, order, orderEmail)
	require.Contains(t, order, storefrontVariantTitle, "the order page lists the line it sold")
	require.Contains(t, order, fmt.Sprintf(`<td class="num">%d</td>`, storefrontQuantity),
		"the line's quantity reaches the page as a number")
	require.NotContains(t, order, "could not be read")
	require.Contains(t, order, "<th>Authorized</th>",
		"the checkout links its payment collection to the order, and the page prints it")
	require.Contains(t, order, "No parcel has been opened for this order.")
	require.Contains(t, open(adminui.SalesPath), storefrontVariantTitle)
	customers := open(adminui.CustomersPath)
	require.Contains(t, customers, customerEmail)
	require.Contains(t, open(panelLink(t, customers, "customers")), "@example.test")
	require.Contains(t, open(adminui.InventoryPath), "</html>")
	require.Contains(t, open(adminui.ReviewsPath), "</html>")
}
