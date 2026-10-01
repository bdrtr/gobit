package adminui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// The telephone order's cart on the panel (ADR 0290).

// fakeCarts records what reached the cart module's surface and answers as
// scripted.
type fakeCarts struct {
	opened []string
	added  []string
	err    error
}

func (f *fakeCarts) OpenCart(_ context.Context, countryCode, customerID, email string) (string, error) {
	f.opened = append(f.opened, countryCode+"|"+customerID+"|"+email)
	if f.err != nil {
		return "", f.err
	}

	return "cart_phone", nil
}

func (f *fakeCarts) AddLine(
	_ context.Context, cartID, salesChannelID, variantID string, quantity int64,
) (string, error) {
	f.added = append(f.added, strings.Join([]string{cartID, salesChannelID, variantID,
		strings.TrimSpace(strings.Repeat("I", int(quantity)))}, "|"))
	if f.err != nil {
		return "", f.err
	}

	return "line_1", nil
}

// phoneCatalog holds one open cart of two lines, the second an add-on.
func phoneCatalog(completed bool) *fakeCatalog {
	return &fakeCatalog{byEntity: map[string][]query.Record{EntityCart: {{
		fieldID: "cart_phone", fieldCurrencyCod: "TRY", fieldEmail: "caller@example.com",
		fieldCartCustomerID: "", fieldSubtotal: int64(32_000), fieldTax: int64(6_400),
		fieldShipping: int64(0), fieldTotal: int64(38_400), fieldCartTotalsStale: false,
		fieldCartCompleted: completed,
		fieldCartLines: []map[string]any{
			{
				cartLineID: "line_1", cartLineVariantID: "variant_shirt", cartLineTitle: "Shirt",
				cartLineQuantity: int64(3), cartLineUnitPrice: int64(10_000), cartLineTotal: int64(30_000),
				cartLineParentID: "",
			},
			{
				cartLineID: "line_2", cartLineVariantID: "variant_wrap", cartLineTitle: "Gift wrap",
				cartLineQuantity: int64(1), cartLineUnitPrice: int64(2_000), cartLineTotal: int64(2_000),
				cartLineParentID: "line_1",
			},
		},
	}}}}
}

// phoneRouter mounts the telephone order's routes.
func phoneRouter(panel *UI) chi.Router {
	r := chi.NewRouter()
	r.Get(CartsPath, panel.newTelephoneOrder)
	r.Post(CartsPath, panel.openTelephoneOrder)
	r.Get(CartPath, panel.showCart)
	r.Post(CartLinesPath, panel.addCartLine)

	return r
}

// phoneRequest sends one request as an operator holding the scopes.
func phoneRequest(panel *UI, method, path string, form url.Values, scopes ...string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request = request.WithContext(corehttp.WithPrincipal(request.Context(),
		corehttp.Principal{ID: "user_1", Kind: "user", Scopes: scopes}))
	rec := httptest.NewRecorder()
	phoneRouter(panel).ServeHTTP(rec, request)

	return rec
}

// TestTheFormOpensACartAndGoesToIt: the country, the customer and the e-mail
// reach the surface, and the operator is sent to the new cart.
func TestTheFormOpensACartAndGoesToIt(t *testing.T) {
	t.Parallel()

	carts := &fakeCarts{}
	panel := newCatalogPanel(t, phoneCatalog(false))
	panel.carts = carts

	form := phoneRequest(panel, http.MethodGet, CartsPath, nil, scopeCartWrite)
	require.Equal(t, http.StatusOK, form.Code, form.Body.String())
	assert.Contains(t, form.Body.String(), `action="`+CartsPath+`"`)

	rec := phoneRequest(panel, http.MethodPost, CartsPath, url.Values{
		formCountryCode: {"TR"}, formEmail: {" caller@example.com "}, formCustomerID: {""},
	}, scopeCartWrite)

	assert.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, CartsPath+"/cart_phone", rec.Header().Get("Location"))
	assert.Equal(t, []string{"TR||caller@example.com"}, carts.opened)
}

// TestARefusedOpeningKeepsWhatWasTyped: the module's refusal is printed on the
// form, which keeps the operator's values.
func TestARefusedOpeningKeepsWhatWasTyped(t *testing.T) {
	t.Parallel()

	panel := newCatalogPanel(t, phoneCatalog(false))
	panel.carts = &fakeCarts{err: errors.NotFound("region_country_unserved", "no region serves XX")}

	rec := phoneRequest(panel, http.MethodPost, CartsPath, url.Values{
		formCountryCode: {"XX"}, formEmail: {"caller@example.com"},
	}, scopeCartWrite)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `<p role="alert">`)
	assert.Contains(t, rec.Body.String(), "no region serves XX")
	assert.Contains(t, rec.Body.String(), `value="caller@example.com"`)
}

// TestTheCartPageNamesItsLines: each line with its variant, quantity and
// amounts, the add-on under its line, and the total with its currency; the
// form is offered to a writer on a panel that has the surface, while the cart
// is open.
func TestTheCartPageNamesItsLines(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		scopes    []string
		carts     TelephoneCarts
		completed bool
		form      bool
	}{
		"a writer":                 {[]string{scopeCartRead, scopeCartWrite}, &fakeCarts{}, false, true},
		"a reader":                 {[]string{scopeCartRead}, &fakeCarts{}, false, false},
		"no surface":               {[]string{scopeCartRead, scopeCartWrite}, nil, false, false},
		"a completed cart":         {[]string{scopeCartRead, scopeCartWrite}, &fakeCarts{}, true, false},
		"an administrator":         {[]string{corehttp.ScopeAdmin}, &fakeCarts{}, false, true},
		"an order writer, no cart": {[]string{scopeCartRead, scopeOrderWrite}, &fakeCarts{}, false, false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			panel := newCatalogPanel(t, phoneCatalog(tc.completed))
			if tc.carts != nil {
				panel.carts = tc.carts
			}
			rec := phoneRequest(panel, http.MethodGet, CartsPath+"/cart_phone", nil, tc.scopes...)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			body := rec.Body.String()
			assert.Contains(t, body, "Shirt")
			assert.Contains(t, body, "variant_shirt")
			assert.Contains(t, body, "&nbsp;&nbsp;+ Gift wrap", "the add-on sits under its line")
			assert.Contains(t, body, "38400 TRY")
			assert.Equal(t, tc.form, strings.Contains(body, `action="`+CartsPath+`/cart_phone/lines"`))
		})
	}
}

// TestALineIsAddedAndThePageDrawnAgain: the channel, the variant and the
// quantity reach the surface, and the operator is sent back to the cart.
func TestALineIsAddedAndThePageDrawnAgain(t *testing.T) {
	t.Parallel()

	carts := &fakeCarts{}
	panel := newCatalogPanel(t, phoneCatalog(false))
	panel.carts = carts

	rec := phoneRequest(panel, http.MethodPost, CartsPath+"/cart_phone/lines", url.Values{
		formSalesChannelID: {"sc_shop"}, formVariantID: {" variant_shirt "}, formQuantity: {"3"},
	}, scopeCartRead, scopeCartWrite)

	assert.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, CartsPath+"/cart_phone", rec.Header().Get("Location"))
	assert.Equal(t, []string{"cart_phone|sc_shop|variant_shirt|III"}, carts.added)
}

// TestARefusedLineSaysWhy: a quantity that is not a whole number is refused
// before the surface, and the module's refusal is printed on the page with
// what was typed.
func TestARefusedLineSaysWhy(t *testing.T) {
	t.Parallel()

	carts := &fakeCarts{}
	panel := newCatalogPanel(t, phoneCatalog(false))
	panel.carts = carts

	rec := phoneRequest(panel, http.MethodPost, CartsPath+"/cart_phone/lines", url.Values{
		formSalesChannelID: {"sc_shop"}, formVariantID: {"variant_shirt"}, formQuantity: {"two"},
	}, scopeCartRead, scopeCartWrite)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "The quantity is a whole number.")
	assert.Empty(t, carts.added)

	carts.err = errors.NotFound("cart_variant_not_in_channel", "the variant is not sold in that channel")
	rec = phoneRequest(panel, http.MethodPost, CartsPath+"/cart_phone/lines", url.Values{
		formSalesChannelID: {"sc_other"}, formVariantID: {"variant_shirt"}, formQuantity: {"1"},
	}, scopeCartRead, scopeCartWrite)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "the variant is not sold in that channel")
	assert.Contains(t, rec.Body.String(), `value="sc_other"`)

	writer := phoneRequest(panel, http.MethodPost, CartsPath+"/cart_phone/lines", url.Values{
		formSalesChannelID: {"sc_other"}, formVariantID: {"variant_shirt"}, formQuantity: {"1"},
	}, scopeCartWrite)
	assert.Equal(t, http.StatusUnprocessableEntity, writer.Code)
	assert.Contains(t, writer.Body.String(), "the variant is not sold in that channel")
	assert.NotContains(t, writer.Body.String(), "Shirt", "a writer who may not read sees the reason only")
}

// TestTheTelephoneOrderWithoutTheSurfaceIsUnavailable: an installation without
// the cart module's surface answers 503 to both writes.
func TestTheTelephoneOrderWithoutTheSurfaceIsUnavailable(t *testing.T) {
	t.Parallel()

	panel := newCatalogPanel(t, phoneCatalog(false))

	assert.Equal(t, http.StatusServiceUnavailable,
		phoneRequest(panel, http.MethodPost, CartsPath, url.Values{formCountryCode: {"TR"}}, scopeCartWrite).Code)
	assert.Equal(t, http.StatusServiceUnavailable,
		phoneRequest(panel, http.MethodPost, CartsPath+"/cart_phone/lines",
			url.Values{formQuantity: {"1"}}, scopeCartWrite).Code)
}
