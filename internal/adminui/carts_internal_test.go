package adminui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

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
	removed    []string
	discarded  []string
	billed     []map[string]string
	err        error
}

func (f *fakeCarts) SetBillingAddress(_ context.Context, cartID string, address map[string]string) error {
	f.billed = append(f.billed, address)

	return f.err
}

func (f *fakeCarts) RemoveLine(_ context.Context, cartID, lineID string) error {
	f.removed = append(f.removed, cartID+"|"+lineID)

	return f.err
}

func (f *fakeCarts) Discard(_ context.Context, cartID string) error {
	f.discarded = append(f.discarded, cartID)

	return f.err
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
		fieldCartCompleted: completed, FieldCartOpenedBy: "usr_phone",
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
	r.Post(CartBillingPath, panel.setCartBilling)
	r.Post(CartShippingPath, panel.addCartShipping)
	r.Post(CartCompletePath, panel.completeCart)
	r.Post(CartLineRemovePath, panel.removeCartLine)
	r.Post(CartDiscardPath, panel.discardCart)

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
		"first_name": "Ada", "last_name": "Lovelace", "company": "", "address_1": "12 Right St",
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

// TestTheTelephoneOrderListsTheOpenOperatorCarts is ADR 0296 on the page: an
// operator who may read carts sees the carts operators opened and nobody
// completed, each linked to its page with its opener; one who may only write
// carts is shown the form and nothing of the carts is read for them.
func TestTheTelephoneOrderListsTheOpenOperatorCarts(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{EntityCart: {{
		fieldID: "cart_open", fieldEmail: "caller@example.com", fieldCartCustomerID: "cus_1",
		FieldCartOpenedBy: "user_7", fieldCurrencyCod: "TRY", fieldTotal: int64(38_400),
		fieldCreatedAt: time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC),
	}}}}
	panel := newCatalogPanel(t, catalog)
	panel.carts = &fakeCarts{}

	rec := phoneRequest(panel, http.MethodGet, CartsPath, nil, scopeCartRead, scopeCartWrite)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, `<a href="`+CartsPath+`/cart_open">cart_open</a>`)
	assert.Contains(t, body, "<td>user_7</td>")
	assert.Contains(t, body, "caller@example.com")
	assert.Contains(t, body, "customer cus_1")
	assert.Contains(t, body, "2026-10-01 09:30 UTC")
	var asked []query.GraphSpec
	for _, spec := range catalog.specs {
		if spec.Entity == EntityCart {
			asked = append(asked, spec)
		}
	}
	require.Len(t, asked, 1)
	assert.Equal(t, map[string]any{fieldCartCompleted: false, FilterOpenedByOperator: true}, asked[0].Filters)
	assert.Equal(t, openCartsShown, asked[0].Limit)
	assert.Contains(t, asked[0].Fields, FieldCartOpenedBy)

	blind := &fakeCatalog{byEntity: catalog.byEntity}
	panel = newCatalogPanel(t, blind)
	panel.carts = &fakeCarts{}
	rec = phoneRequest(panel, http.MethodGet, CartsPath, nil, scopeCartWrite)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `action="`+CartsPath+`"`)
	assert.NotContains(t, rec.Body.String(), "Open carts")
	for _, spec := range blind.specs {
		assert.NotEqual(t, EntityCart, spec.Entity, "the carts are read under cart:read alone")
	}
}

// TestTheOpenCartsListSaysWhenItIsEmptyOrUnread: no open cart is said, a failed
// read is said, and neither takes the form away.
func TestTheOpenCartsListSaysWhenItIsEmptyOrUnread(t *testing.T) {
	t.Parallel()

	panel := newCatalogPanel(t, &fakeCatalog{byEntity: map[string][]query.Record{}})
	panel.carts = &fakeCarts{}
	rec := phoneRequest(panel, http.MethodGet, CartsPath, nil, scopeCartRead, scopeCartWrite)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "No operator's cart is open.")
	assert.Contains(t, rec.Body.String(), `action="`+CartsPath+`"`)

	failing := &fakeCatalog{errByEntity: map[string]error{
		EntityCart: errors.Unavailable("carts_down", "the carts did not answer"),
	}}
	panel = newCatalogPanel(t, failing)
	panel.carts = &fakeCarts{}
	rec = phoneRequest(panel, http.MethodGet, CartsPath, nil, scopeCartRead, scopeCartWrite)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "The open carts could not be read.")
	assert.Contains(t, rec.Body.String(), `action="`+CartsPath+`"`)
}

// callerCatalog holds a guest record and an account under one e-mail, the
// guest first, as the provider may answer.
func callerCatalog() *fakeCatalog {
	return &fakeCatalog{byEntity: map[string][]query.Record{EntityCustomer: {
		{fieldID: "cus_guest", fieldEmail: "ada@example.com", fieldFirstName: "", fieldLastName: "", fieldHasAccount: false},
		{fieldID: "cus_account", fieldEmail: "ada@example.com", fieldFirstName: "Ada", fieldLastName: "Lovelace", fieldHasAccount: true},
	}}}
}

// TestTheTelephoneOrderFindsTheCallerByEmail is ADR 0297: an operator who may
// read customers finds the caller's records by e-mail, the account first and
// chosen, the guest's choice beside them, and the e-mail written into the
// form; one who may not keeps the id box and nothing of the customers is read
// for them.
func TestTheTelephoneOrderFindsTheCallerByEmail(t *testing.T) {
	t.Parallel()

	catalog := callerCatalog()
	panel := newCatalogPanel(t, catalog)
	panel.carts = &fakeCarts{}

	rec := phoneRequest(panel, http.MethodGet, CartsPath+"?caller=+Ada%40example.com+", nil,
		scopeCartWrite, scopeCustomerRead)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	account := `<option value="cus_account" selected>Ada Lovelace — account (cus_account)</option>`
	guest := `<option value="cus_guest">ada@example.com — guest record (cus_guest)</option>`
	assert.Contains(t, body, `<select name="customer_id"`)
	assert.Contains(t, body, `<option value="">a guest</option>`)
	require.Contains(t, body, account)
	require.Contains(t, body, guest)
	assert.Less(t, strings.Index(body, account), strings.Index(body, guest), "the account comes first")
	assert.Contains(t, body, `name="email" value="Ada@example.com"`)
	assert.Contains(t, body, `<input type="hidden" name="caller" value="Ada@example.com">`)
	var asked []query.GraphSpec
	for _, spec := range catalog.specs {
		if spec.Entity == EntityCustomer {
			asked = append(asked, spec)
		}
	}
	require.Len(t, asked, 1)
	assert.Equal(t, map[string]any{filterCustomerEmail: "Ada@example.com"}, asked[0].Filters)
	assert.Equal(t, callersFound, asked[0].Limit)

	blind := callerCatalog()
	panel = newCatalogPanel(t, blind)
	panel.carts = &fakeCarts{}
	rec = phoneRequest(panel, http.MethodGet, CartsPath+"?caller=ada%40example.com", nil, scopeCartWrite)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), `name="caller"`)
	assert.Contains(t, rec.Body.String(), `<input name="customer_id"`)
	for _, spec := range blind.specs {
		assert.NotEqual(t, EntityCustomer, spec.Entity, "the customers are read under customer:read alone")
	}
}

// TestTheCallersChoiceReachesTheSurface: the record the operator chose is the
// cart's customer, and a refused form keeps the choice, a guest included.
func TestTheCallersChoiceReachesTheSurface(t *testing.T) {
	t.Parallel()

	carts := &fakeCarts{}
	panel := newCatalogPanel(t, callerCatalog())
	panel.carts = carts
	rec := phoneRequest(panel, http.MethodPost, CartsPath, url.Values{
		paramCaller: {"ada@example.com"}, formCountryCode: {"TR"},
		formEmail: {"ada@example.com"}, formCustomerID: {"cus_guest"},
	}, scopeCartWrite, scopeCustomerRead)
	assert.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, []string{"TR|cus_guest|ada@example.com"}, carts.opened)

	panel = newCatalogPanel(t, callerCatalog())
	panel.carts = &fakeCarts{err: errors.NotFound("region_country_unserved", "no region serves XX")}
	rec = phoneRequest(panel, http.MethodPost, CartsPath, url.Values{
		paramCaller: {"ada@example.com"}, formCountryCode: {"XX"},
		formEmail: {"ada@example.com"}, formCustomerID: {""},
	}, scopeCartWrite, scopeCustomerRead)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `<select name="customer_id"`, "the search is drawn again")
	assert.NotContains(t, rec.Body.String(), " selected>", "the guest the operator chose stays chosen")
}

// TestACallerSearchThatFindsNothingSaysSo: no record, an address that is not
// one, and a failed read are each said, and the form keeps the id box.
func TestACallerSearchThatFindsNothingSaysSo(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		catalog *fakeCatalog
		says    string
	}{
		"no record": {
			catalog: &fakeCatalog{byEntity: map[string][]query.Record{}},
			says:    "No customer holds nobody@example.com; the cart opens as a guest's.",
		},
		"not an address": {
			catalog: &fakeCatalog{errByEntity: map[string]error{
				EntityCustomer: errors.Invalid("customer_invalid_input", "not an e-mail"),
			}},
			says: "That is not an e-mail address.",
		},
		"a failed read": {
			catalog: &fakeCatalog{errByEntity: map[string]error{
				EntityCustomer: errors.Unavailable("customers_down", "the customers did not answer"),
			}},
			says: "The customers could not be read.",
		},
	} {
		panel := newCatalogPanel(t, tc.catalog)
		panel.carts = &fakeCarts{}
		rec := phoneRequest(panel, http.MethodGet, CartsPath+"?caller=nobody%40example.com", nil,
			scopeCartWrite, scopeCustomerRead)
		require.Equal(t, http.StatusOK, rec.Code, name)
		assert.Contains(t, rec.Body.String(), tc.says, name)
		assert.Contains(t, rec.Body.String(), `<input name="customer_id"`, name)
	}
}

// TestAShoppersCartIsOnlyRead is ADR 0299 on the page: a cart a shopper
// opened is drawn with what it holds and offers no form, and the page says
// why; an operator's cart names its opener.
func TestAShoppersCartIsOnlyRead(t *testing.T) {
	t.Parallel()

	catalog := phoneCatalog(false)
	catalog.byEntity[EntityCart][0][FieldCartOpenedBy] = ""
	panel := newCatalogPanel(t, catalog)
	panel.carts = &fakeCarts{}

	rec := phoneRequest(panel, http.MethodGet, CartsPath+"/cart_phone", nil, scopeCartRead, scopeCartWrite)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, "A shopper opened this cart; the panel reads it and changes nothing in it.")
	assert.Contains(t, body, "Shirt", "the cart is still drawn")
	assert.NotContains(t, body, `action="`+CartsPath+`/cart_phone/`, "no form writes to a shopper's cart")
	spec, ok := catalog.specFor(EntityCart)
	require.True(t, ok)
	assert.Contains(t, spec.Fields, FieldCartOpenedBy, "the page asks who opened the cart")

	rec = phoneRequest(newPhonePanel(t), http.MethodGet, CartsPath+"/cart_phone", nil, scopeCartRead, scopeCartWrite)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "opened by operator usr_phone")
	assert.Contains(t, rec.Body.String(), `action="`+CartsPath+`/cart_phone/lines"`)
}

// newPhonePanel is a panel over [phoneCatalog]'s operator's cart.
func newPhonePanel(t *testing.T) *UI {
	t.Helper()

	panel := newCatalogPanel(t, phoneCatalog(false))
	panel.carts = &fakeCarts{}

	return panel
}

// TestTheOperatorCorrectsTheirCart is ADR 0300 on the page: each line has a
// remove button that reaches the surface and returns to the cart, and the
// discard form deletes the cart and returns to the telephone order's page; a
// refusal is printed on the cart.
func TestTheOperatorCorrectsTheirCart(t *testing.T) {
	t.Parallel()

	carts := &fakeCarts{}
	panel := newCatalogPanel(t, phoneCatalog(false))
	panel.carts = carts

	page := phoneRequest(panel, http.MethodGet, CartsPath+"/cart_phone", nil, scopeCartRead, scopeCartWrite)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	assert.Contains(t, page.Body.String(), `action="`+CartsPath+`/cart_phone/lines/line_1/remove"`)
	assert.NotContains(t, page.Body.String(), `action="`+CartsPath+`/cart_phone/lines/line_2/remove"`,
		"an add-on goes with its line and is not removed alone (ADR 0229)")
	assert.Contains(t, page.Body.String(), `action="`+CartsPath+`/cart_phone/discard"`)

	rec := phoneRequest(panel, http.MethodPost, CartsPath+"/cart_phone/lines/line_1/remove", url.Values{}, scopeCartWrite)
	assert.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, CartsPath+"/cart_phone", rec.Header().Get("Location"))
	assert.Equal(t, []string{"cart_phone|line_1"}, carts.removed)

	rec = phoneRequest(panel, http.MethodPost, CartsPath+"/cart_phone/discard", url.Values{}, scopeCartWrite)
	assert.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, CartsPath, rec.Header().Get("Location"), "a discarded cart has no page to return to")
	assert.Equal(t, []string{"cart_phone"}, carts.discarded)

	refusing := newCatalogPanel(t, phoneCatalog(false))
	refusing.carts = &fakeCarts{err: errors.Conflict("cart_opened_by_shopper", "a shopper opened this cart")}
	rec = phoneRequest(refusing, http.MethodPost, CartsPath+"/cart_phone/discard", url.Values{}, scopeCartRead, scopeCartWrite)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "a shopper opened this cart")

	done := newCatalogPanel(t, phoneCatalog(true))
	done.carts = &fakeCarts{}
	page = phoneRequest(done, http.MethodGet, CartsPath+"/cart_phone", nil, scopeCartRead, scopeCartWrite)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	assert.NotContains(t, page.Body.String(), "/remove\"", "a completed cart is not corrected")
	assert.NotContains(t, page.Body.String(), "/discard\"")
}

// TestTheTelephoneOrderWritesTheBillingAddress is ADR 0303 on the page: the
// billing form is drawn with the shipping address until a billing address is
// written, then with it; its fields reach the surface as the address keys;
// the page prints the billing address; and a refusal keeps what was typed in
// the billing form while the shipping form keeps the cart's.
func TestTheTelephoneOrderWritesTheBillingAddress(t *testing.T) {
	t.Parallel()

	carts := &fakeCarts{}
	panel := newCatalogPanel(t, phoneCatalog(false))
	panel.carts = carts

	page := phoneRequest(panel, http.MethodGet, CartsPath+"/cart_phone", nil, scopeCartRead, scopeCartWrite)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	assert.Contains(t, page.Body.String(), `name="billing_address_1" value="12 Right St"`,
		"with no billing address the form offers the shipping address")
	assert.Contains(t, page.Body.String(), "no billing address")
	catalog, isFake := panel.catalog.(*fakeCatalog)
	require.True(t, isFake)
	spec, ok := catalog.specFor(EntityCart)
	require.True(t, ok)
	assert.Contains(t, spec.Fields, fieldCartBillingAddress, "the page asks for the billing address")

	rec := phoneRequest(panel, http.MethodPost, CartsPath+"/cart_phone/billing", url.Values{
		"billing_company": {" Engines Ltd "}, "billing_address_1": {"1 Office St"}, "billing_country_code": {"TR"},
		"address_1": {"ignored"},
	}, scopeCartWrite)
	assert.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	require.Len(t, carts.billed, 1)
	assert.Equal(t, "Engines Ltd", carts.billed[0]["company"])
	assert.Equal(t, "1 Office St", carts.billed[0]["address_1"], "the billing form's fields, not the shipping form's")

	billed := phoneCatalog(false)
	billed.byEntity[EntityCart][0][fieldCartBillingAddress] = map[string]any{
		"company": "Engines Ltd", "address_1": "1 Office St", "country_code": "TR",
	}
	panel = newCatalogPanel(t, billed)
	panel.carts = &fakeCarts{}
	page = phoneRequest(panel, http.MethodGet, CartsPath+"/cart_phone", nil, scopeCartRead, scopeCartWrite)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	assert.Contains(t, page.Body.String(), "1 Office St<br>", "the page prints the billing address")
	assert.Contains(t, page.Body.String(), `name="billing_address_1" value="1 Office St"`)
	assert.Contains(t, page.Body.String(), `name="address_1" value="12 Right St"`, "the shipping form keeps its own")

	refusing := newCatalogPanel(t, phoneCatalog(false))
	refusing.carts = &fakeCarts{err: errors.Invalid("cart_invalid_input", "the country is not served")}
	rec = phoneRequest(refusing, http.MethodPost, CartsPath+"/cart_phone/billing", url.Values{
		"billing_address_1": {"Typed St 9"}, "billing_country_code": {"XX"},
	}, scopeCartRead, scopeCartWrite)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `name="billing_address_1" value="Typed St 9"`)
	assert.Contains(t, rec.Body.String(), `name="address_1" value="12 Right St"`)
}

// TestACustomersCartStartsFromTheirDefaultAddress is ADR 0304 on the page: a
// customer's cart with no shipping address draws both address forms with the
// customer's default shipping address and says so; an operator without
// customer:read, or a cart with an address, reads nothing of the customer; a
// failed read draws the form empty.
func TestACustomersCartStartsFromTheirDefaultAddress(t *testing.T) {
	t.Parallel()

	unaddressed := func(customerErr error) *fakeCatalog {
		catalog := phoneCatalog(false)
		catalog.byEntity[EntityCart][0][fieldCartCustomerID] = "cus_1"
		catalog.byEntity[EntityCart][0][fieldCartShippingAddress] = nil
		catalog.byEntity[EntityCustomer] = []query.Record{{
			fieldID: "cus_1",
			fieldCustomerDefaultShippingAddress: map[string]any{
				"first_name": "Ada", "company": "Engines Ltd", "address_1": "Home St 4", "city": "Ankara", "country_code": "TR",
			},
		}}
		if customerErr != nil {
			catalog.errByEntity = map[string]error{EntityCustomer: customerErr}
		}

		return catalog
	}
	customerReads := func(catalog *fakeCatalog) []query.GraphSpec {
		var out []query.GraphSpec
		for _, spec := range catalog.specs {
			if spec.Entity == EntityCustomer {
				out = append(out, spec)
			}
		}

		return out
	}

	catalog := unaddressed(nil)
	panel := newCatalogPanel(t, catalog)
	panel.carts = &fakeCarts{}
	page := phoneRequest(panel, http.MethodGet, CartsPath+"/cart_phone", nil, scopeCartRead, scopeCartWrite, scopeCustomerRead)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	assert.Contains(t, page.Body.String(), `name="address_1" value="Home St 4"`)
	assert.Contains(t, page.Body.String(), `name="billing_address_1" value="Home St 4"`)
	assert.Contains(t, page.Body.String(), "Drawn with the customer's default shipping address")
	reads := customerReads(catalog)
	require.Len(t, reads, 1)
	assert.Equal(t, map[string]any{filterID: []string{"cus_1"}}, reads[0].Filters)
	assert.Contains(t, reads[0].Fields, fieldCustomerDefaultShippingAddress)

	blind := unaddressed(nil)
	panel = newCatalogPanel(t, blind)
	panel.carts = &fakeCarts{}
	page = phoneRequest(panel, http.MethodGet, CartsPath+"/cart_phone", nil, scopeCartRead, scopeCartWrite)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	assert.Empty(t, customerReads(blind), "the customer is read under customer:read alone")
	assert.NotContains(t, page.Body.String(), "Home St 4")

	addressed := phoneCatalog(false)
	addressed.byEntity[EntityCart][0][fieldCartCustomerID] = "cus_1"
	panel = newCatalogPanel(t, addressed)
	panel.carts = &fakeCarts{}
	page = phoneRequest(panel, http.MethodGet, CartsPath+"/cart_phone", nil, scopeCartRead, scopeCartWrite, scopeCustomerRead)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	assert.Empty(t, customerReads(addressed), "a cart with an address keeps its own")
	assert.Contains(t, page.Body.String(), `name="address_1" value="12 Right St"`)

	failing := unaddressed(errors.Unavailable("customers_down", "the customers did not answer"))
	panel = newCatalogPanel(t, failing)
	panel.carts = &fakeCarts{}
	page = phoneRequest(panel, http.MethodGet, CartsPath+"/cart_phone", nil, scopeCartRead, scopeCartWrite, scopeCustomerRead)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	assert.Contains(t, page.Body.String(), `name="address_1" value=""`, "a failed read draws the form empty")
	assert.NotContains(t, page.Body.String(), "Drawn with the customer")
}
