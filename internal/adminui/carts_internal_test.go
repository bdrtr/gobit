package adminui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
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
	// options is the cart's listing (ADR 0292); optionsErr fails it.
	options    [][3]string
	optionsErr error
	opened     []string
	added      []string
	addressed  []map[string]string
	shipped    []string
	completed  []string
	err        error
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

func (f *fakeCarts) ShippingOptions(_ context.Context, _ string) (ids, names []string, amounts []int64, err error) {
	if f.optionsErr != nil {
		return nil, nil, nil, f.optionsErr
	}
	for _, option := range f.options {
		amount, _ := strconv.ParseInt(option[2], 10, 64)
		ids, names, amounts = append(ids, option[0]), append(names, option[1]), append(amounts, amount)
	}

	return ids, names, amounts, nil
}

func (f *fakeCarts) SetShippingAddress(_ context.Context, cartID string, address map[string]string) error {
	f.addressed = append(f.addressed, address)

	return f.err
}

func (f *fakeCarts) AddShippingMethod(_ context.Context, cartID, shippingOptionID string) (string, error) {
	f.shipped = append(f.shipped, cartID+"|"+shippingOptionID)

	return "sm_1", f.err
}

func (f *fakeCarts) Complete(
	_ context.Context, cartID, salesChannelID, paymentProviderID string, expectedTotal int64,
) (orderID string, outstanding int64, err error) {
	f.completed = append(f.completed, strings.Join([]string{cartID, salesChannelID, paymentProviderID,
		strconv.FormatInt(expectedTotal, 10)}, "|"))
	if f.err != nil {
		return "", 0, f.err
	}

	return "order_phone", expectedTotal, nil
}

// phoneCatalog holds one open cart of two lines, the second an add-on.
func phoneCatalog(completed bool) *fakeCatalog {
	return &fakeCatalog{byEntity: map[string][]query.Record{EntityCart: {{
		fieldID: "cart_phone", fieldCurrencyCod: "TRY", fieldEmail: "caller@example.com",
		fieldCartCustomerID: "", fieldSubtotal: int64(32_000), fieldTax: int64(6_400),
		fieldShipping: int64(0), fieldTotal: int64(38_400), fieldCartTotalsStale: false,
		fieldCartCompleted: completed,
		fieldCartShippingAddress: map[string]any{
			"first_name": "Ada", "last_name": "Lovelace", "address_1": "12 Right St",
			"city": "Ankara", "postal_code": "06000", "country_code": "TR", "phone": "",
		},
		fieldCartShippingMethods: []map[string]any{
			{"id": "sm_1", "shipping_option_id": "so_courier", cartMethodName: "Courier", cartMethodAmount: int64(0)},
		},
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
	r.Post(CartAddressPath, panel.setCartAddress)
	r.Post(CartShippingPath, panel.addCartShipping)
	r.Post(CartCompletePath, panel.completeCart)

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

// TestTheCartPageNamesItsAddressAndShipping: the address and the chosen method
// are printed, the address form carries what is there, and the completion
// carries the total the page was drawn with (ADR 0291).
func TestTheCartPageNamesItsAddressAndShipping(t *testing.T) {
	t.Parallel()

	panel := newCatalogPanel(t, phoneCatalog(false))
	panel.carts = &fakeCarts{}

	rec := phoneRequest(panel, http.MethodGet, CartsPath+"/cart_phone", nil, scopeCartRead, scopeCartWrite)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, "Ada Lovelace<br>")
	assert.Contains(t, body, "12 Right St<br>")
	assert.Contains(t, body, "Courier")
	assert.Contains(t, body, `name="address_1" value="12 Right St"`, "the form starts from what is there")
	assert.Contains(t, body, `name="read_total" value="38400"`, "the completion carries the total read")
	assert.Contains(t, body, `action="`+CartsPath+`/cart_phone/complete"`)
}

// TestTheCartsWritesReachTheSurface: the address, the shipping option and the
// completion reach the cart module's surface; the first two return to the
// cart, and the completion goes to the order it placed.
func TestTheCartsWritesReachTheSurface(t *testing.T) {
	t.Parallel()

	carts := &fakeCarts{}
	panel := newCatalogPanel(t, phoneCatalog(false))
	panel.carts = carts
	cart := CartsPath + "/cart_phone"
	scopes := []string{scopeCartRead, scopeCartWrite}

	address := phoneRequest(panel, http.MethodPost, cart+"/address", url.Values{
		"first_name": {" Ada "}, "last_name": {"Lovelace"}, "address_1": {"12 Right St"},
		"city": {"Ankara"}, "postal_code": {"06000"}, "country_code": {"TR"}, "phone": {"+90"},
	}, scopes...)
	assert.Equal(t, http.StatusSeeOther, address.Code, address.Body.String())
	assert.Equal(t, cart, address.Header().Get("Location"))
	require.Len(t, carts.addressed, 1)
	assert.Equal(t, map[string]string{
		"first_name": "Ada", "last_name": "Lovelace", "address_1": "12 Right St",
		"city": "Ankara", "postal_code": "06000", "country_code": "TR", "phone": "+90",
	}, carts.addressed[0])

	shipping := phoneRequest(panel, http.MethodPost, cart+"/shipping",
		url.Values{formShippingOption: {" so_courier "}}, scopes...)
	assert.Equal(t, http.StatusSeeOther, shipping.Code, shipping.Body.String())
	assert.Equal(t, []string{"cart_phone|so_courier"}, carts.shipped)

	completed := phoneRequest(panel, http.MethodPost, cart+"/complete", url.Values{
		formSalesChannelID: {"sc_shop"}, formPaymentMethod: {"bank_transfer"}, formReadTotal: {"38400"},
	}, scopes...)
	assert.Equal(t, http.StatusSeeOther, completed.Code, completed.Body.String())
	assert.Equal(t, OrdersPath+"/order_phone", completed.Header().Get("Location"))
	assert.Equal(t, []string{"cart_phone|sc_shop|bank_transfer|38400"}, carts.completed)
}

// TestARefusedCompletionSaysWhy: a completion without the page's total is
// refused before the surface, and the module's refusal — the total moved, say
// — is printed on the cart with what was typed.
func TestARefusedCompletionSaysWhy(t *testing.T) {
	t.Parallel()

	carts := &fakeCarts{}
	panel := newCatalogPanel(t, phoneCatalog(false))
	panel.carts = carts
	scopes := []string{scopeCartRead, scopeCartWrite}

	missing := phoneRequest(panel, http.MethodPost, CartsPath+"/cart_phone/complete", url.Values{
		formSalesChannelID: {"sc_shop"}, formPaymentMethod: {"bank_transfer"},
	}, scopes...)
	assert.Equal(t, http.StatusUnprocessableEntity, missing.Code)
	assert.Contains(t, missing.Body.String(), "draw the cart again")
	assert.Empty(t, carts.completed)

	carts.err = errors.Conflict("checkout_workflow_total_mismatch", "the total moved since it was read")
	moved := phoneRequest(panel, http.MethodPost, CartsPath+"/cart_phone/complete", url.Values{
		formSalesChannelID: {"sc_shop"}, formPaymentMethod: {"bank_transfer"}, formReadTotal: {"1"},
	}, scopes...)
	assert.Equal(t, http.StatusUnprocessableEntity, moved.Code, moved.Body.String())
	assert.Contains(t, moved.Body.String(), "the total moved since it was read")
	assert.Contains(t, moved.Body.String(), `value="bank_transfer"`)
}

// TestTheShippingFormOffersTheCartsOptions is ADR 0292 on the page: the
// options the cart can take are a list, each named and priced; a listing that
// could not be read leaves the id box, and a cart nothing ships says so.
func TestTheShippingFormOffersTheCartsOptions(t *testing.T) {
	t.Parallel()

	shippingForm := `action="` + CartsPath + `/cart_phone/shipping"`
	for name, tc := range map[string]struct {
		carts    *fakeCarts
		contains []string
		form     bool
	}{
		"a list": {
			&fakeCarts{options: [][3]string{{"so_free", "Free over 500", "0"}, {"so_std", "Standard", "4900"}}},
			[]string{`<select name="shipping_option_id"`, `<option value="so_free">Free over 500 — 0 TRY`,
				`<option value="so_std">Standard — 4900 TRY`},
			true,
		},
		"an unread listing": {
			&fakeCarts{optionsErr: errors.Unavailable("fulfillment_down", "the fulfillment module did not answer")},
			[]string{"The options could not be read", `<input name="shipping_option_id"`},
			true,
		},
		"no option": {&fakeCarts{}, []string{"No shipping option serves this cart."}, false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			panel := newCatalogPanel(t, phoneCatalog(false))
			panel.carts = tc.carts
			rec := phoneRequest(panel, http.MethodGet, CartsPath+"/cart_phone", nil, scopeCartRead, scopeCartWrite)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			for _, want := range tc.contains {
				assert.Contains(t, rec.Body.String(), want)
			}
			assert.Equal(t, tc.form, strings.Contains(rec.Body.String(), shippingForm))
		})
	}
}

// searchCatalog is phoneCatalog with a product of two variants to find.
func searchCatalog() *fakeCatalog {
	catalog := phoneCatalog(false)
	catalog.byEntity[EntityProduct] = []query.Record{{fieldID: "prod_shirt", fieldTitle: "Shirt"}}
	catalog.byEntity[EntityVariant] = []query.Record{
		{fieldID: "variant_shirt_m", fieldTitle: "M", fieldSKU: "SH-M", fieldVariantProduct: "prod_shirt"},
		{fieldID: "variant_shirt_l", fieldTitle: "L", fieldSKU: "", fieldVariantProduct: "prod_shirt"},
	}

	return catalog
}

// TestTheCartFindsAVariantByItsProductsTitle is ADR 0293: an operator who may
// read the catalog finds a product by its title, and the add form offers its
// variants by name; one who may not is offered the id box and nothing of the
// catalog is read for them.
func TestTheCartFindsAVariantByItsProductsTitle(t *testing.T) {
	t.Parallel()

	catalog := searchCatalog()
	panel := newCatalogPanel(t, catalog)
	panel.carts = &fakeCarts{}

	rec := phoneRequest(panel, http.MethodGet, CartsPath+"/cart_phone?find=+shirt+", nil,
		scopeCartRead, scopeCartWrite, scopeProductRead)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, `<select name="variant_id"`)
	assert.Contains(t, body, `<option value="variant_shirt_m">Shirt — M (SH-M)</option>`)
	assert.Contains(t, body, `<option value="variant_shirt_l">Shirt — L</option>`)
	assert.Contains(t, body, `name="find" value="shirt"`)
	var asked []query.GraphSpec
	for _, spec := range catalog.specs {
		if spec.Entity == EntityProduct || spec.Entity == EntityVariant {
			asked = append(asked, spec)
		}
	}
	require.Len(t, asked, 2)
	assert.Equal(t, "shirt", asked[0].Filters[filterSearch])
	assert.Equal(t, []string{"prod_shirt"}, asked[1].Filters[filterProductID])

	blind := searchCatalog()
	panel = newCatalogPanel(t, blind)
	panel.carts = &fakeCarts{}
	rec = phoneRequest(panel, http.MethodGet, CartsPath+"/cart_phone?find=shirt", nil, scopeCartRead, scopeCartWrite)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), `name="find"`)
	assert.Contains(t, rec.Body.String(), `<input name="variant_id"`)
	for _, spec := range blind.specs {
		assert.NotEqual(t, EntityProduct, spec.Entity, "the catalog is the product module's, read under its privilege")
	}
}

// TestASearchThatFindsNothingSaysSo: no match is said, a failed read is said,
// and both leave the id box.
func TestASearchThatFindsNothingSaysSo(t *testing.T) {
	t.Parallel()

	empty := searchCatalog()
	empty.byEntity[EntityProduct] = nil
	panel := newCatalogPanel(t, empty)
	panel.carts = &fakeCarts{}
	rec := phoneRequest(panel, http.MethodGet, CartsPath+"/cart_phone?find=kilt", nil,
		scopeCartRead, scopeCartWrite, scopeProductRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `No product's title matches "kilt".`)
	assert.Contains(t, rec.Body.String(), `<input name="variant_id"`)

	failing := searchCatalog()
	failing.errByEntity = map[string]error{EntityProduct: errors.Unavailable("catalog_down", "the catalog did not answer")}
	panel = newCatalogPanel(t, failing)
	panel.carts = &fakeCarts{}
	rec = phoneRequest(panel, http.MethodGet, CartsPath+"/cart_phone?find=shirt", nil,
		scopeCartRead, scopeCartWrite, scopeProductRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "The products could not be read")
	assert.Contains(t, rec.Body.String(), `<input name="variant_id"`)
}
