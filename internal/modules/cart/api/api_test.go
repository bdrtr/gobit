package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/cart/api"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// fakeCarts is the test counterpart of api.Carts.
//
// The HTTP layer's responsibility is narrow: decode the body, call the service,
// write the envelope and the status code. That is why the fake service carries
// no business logic; it only records the call it received and returns the
// response set up in advance.
type fakeCarts struct {
	cart   models.Cart
	detail models.CartDetail
	item   models.LineItem
	addr   models.CartAddress
	method models.ShippingMethod
	carts  []models.Cart
	count  int64

	// err, when set, makes every method return this error; it is used to exercise
	// the error mapping.
	err error

	// The arguments of the last call.
	updateInput   service.UpdateCartInput
	addInput      service.AddLineItemInput
	addressInput  service.AddressInput
	shippingInput service.AddShippingMethodInput
	listInput     service.ListCartsInput
	gotCartID     string
	gotLineID     string
	gotMethodID   string
	gotQuantity   int64
	// billing reports whether the call that wrote the last address came from the
	// billing endpoint.
	billing bool
}

// The fake satisfying the surface the handler expects is verified at compile time.
var _ api.Carts = (*fakeCarts)(nil)

// GetCart returns the cart with its children.
func (f *fakeCarts) GetCart(_ context.Context, cartID string) (models.CartDetail, error) {
	f.gotCartID = cartID
	return f.detail, f.err
}

// UpdateCart returns the updated cart.
func (f *fakeCarts) UpdateCart(_ context.Context, cartID string, in service.UpdateCartInput) (models.Cart, error) {
	f.gotCartID = cartID
	f.updateInput = in
	return f.cart, f.err
}

// ListCarts returns the carts.
func (f *fakeCarts) ListCarts(_ context.Context, in service.ListCartsInput) ([]models.Cart, int64, error) {
	f.listInput = in
	return f.carts, f.count, f.err
}

// DeleteCart deletes the cart.
func (f *fakeCarts) DeleteCart(_ context.Context, cartID string) error {
	f.gotCartID = cartID
	return f.err
}

// AddLineItem adds a line item.
func (f *fakeCarts) AddLineItem(_ context.Context, cartID string, in service.AddLineItemInput) (models.LineItem, error) {
	f.gotCartID, f.addInput = cartID, in
	return f.item, f.err
}

// UpdateLineItemQuantity writes the quantity.
func (f *fakeCarts) UpdateLineItemQuantity(_ context.Context, cartID, lineID string, quantity int64) (models.LineItem, error) {
	f.gotCartID, f.gotLineID, f.gotQuantity = cartID, lineID, quantity
	return f.item, f.err
}

// RemoveLineItem removes the line item.
func (f *fakeCarts) RemoveLineItem(_ context.Context, cartID, lineID string) error {
	f.gotCartID, f.gotLineID = cartID, lineID
	return f.err
}

// SetShippingAddress writes the shipping address.
func (f *fakeCarts) SetShippingAddress(_ context.Context, cartID string, in service.AddressInput) (models.CartAddress, error) {
	f.gotCartID, f.addressInput, f.billing = cartID, in, false
	return f.addr, f.err
}

// SetBillingAddress writes the billing address.
func (f *fakeCarts) SetBillingAddress(_ context.Context, cartID string, in service.AddressInput) (models.CartAddress, error) {
	f.gotCartID, f.addressInput, f.billing = cartID, in, true
	return f.addr, f.err
}

// AddShippingMethod adds a shipping method.
func (f *fakeCarts) AddShippingMethod(_ context.Context, cartID string, in service.AddShippingMethodInput) (models.ShippingMethod, error) {
	f.gotCartID, f.shippingInput = cartID, in
	return f.method, f.err
}

// RemoveShippingMethod removes the shipping method.
func (f *fakeCarts) RemoveShippingMethod(_ context.Context, cartID, methodID string) error {
	f.gotCartID, f.gotMethodID = cartID, methodID
	return f.err
}

// fakeOpening is the test counterpart of api.CartOpening.
//
// The fake RESOLVES no region; what the test exercises is that the handler does
// not decide the region itself but leaves it to the flow. The recorded arguments
// make exactly that visible: because there is no region field in the body there
// is no region to pass to the flow either — the only piece of location
// information that goes through is the COUNTRY code.
type fakeOpening struct {
	cartID string
	err    error

	// The arguments of the last call.
	gotCountry    string
	gotCustomerID string
	gotEmail      string
	gotMetadata   json.RawMessage
	calls         int
}

// The fake satisfying the surface the handler expects is verified at compile time.
var _ api.CartOpening = (*fakeOpening)(nil)

// OpenCartForCountry returns the cart's id and records the arguments.
func (f *fakeOpening) OpenCartForCountry(
	_ context.Context,
	countryCode, customerID, email string,
	metadata json.RawMessage,
) (string, error) {
	f.calls++
	f.gotCountry, f.gotCustomerID, f.gotEmail, f.gotMetadata = countryCode, customerID, email, metadata
	return f.cartID, f.err
}

// fakePricing is the test counterpart of api.LinePricing.
//
// The fake COMPUTES no price; what the test exercises is that the handler does
// not decide the price ITSELF but leaves it to the flow. The recorded arguments
// make exactly that visible: because there is no price field in the body there is
// no price to pass to the flow either.
type fakePricing struct {
	lineID  string
	removed bool
	err     error

	// The arguments of the last call.
	gotCartID    string
	gotVariantID string
	gotLineID    string
	gotQuantity  int64
	gotMetadata  json.RawMessage
	calls        int
}

// The fake satisfying the surface the handler expects is verified at compile time.
var _ api.LinePricing = (*fakePricing)(nil)

// AddPricedLineItem returns the line item's id and records the arguments.
func (f *fakePricing) AddPricedLineItem(
	_ context.Context,
	cartID, variantID string,
	quantity int64,
	metadata json.RawMessage,
) (string, error) {
	f.calls++
	f.gotCartID, f.gotVariantID, f.gotQuantity, f.gotMetadata = cartID, variantID, quantity, metadata
	return f.lineID, f.err
}

// SetLineItemQuantity records the quantity and returns whether the line item was
// removed.
func (f *fakePricing) SetLineItemQuantity(
	_ context.Context,
	cartID, lineItemID string,
	quantity int64,
) (bool, error) {
	f.calls++
	f.gotCartID, f.gotLineID, f.gotQuantity = cartID, lineItemID, quantity
	return f.removed, f.err
}

// fakeCheckout is the test counterpart of api.CartCompletion.
type fakeCheckout struct {
	response json.RawMessage
	err      error

	// got is the raw request sent to the flow; the email having come FROM THE
	// CART can only be seen from here.
	got   json.RawMessage
	calls int
}

// The fake satisfying the surface the handler expects is verified at compile time.
var _ api.CartCompletion = (*fakeCheckout)(nil)

// CompleteCartJSON records the request and returns the scripted response.
func (f *fakeCheckout) CompleteCartJSON(_ context.Context, request json.RawMessage) (json.RawMessage, error) {
	f.calls++
	f.got = request
	return f.response, f.err
}

// defaultCurrency is the currency the fakes put on the cart.
//
// No currency travels in the cart bodies any more and the code therefore only
// exists to show that the response is read from the cart.
const defaultCurrency = "EUR"

// newServer sets up a router bound to the fake service and to EMPTY fake flows.
//
// It is enough for tests that do not touch the flows; the tests that exercise
// their arguments or their absence hand over their own fakes with
// [newServerWithFlows].
func newServer(t *testing.T, svc *fakeCarts) http.Handler {
	t.Helper()

	return newServerWithFlows(t, svc, api.Flows{
		Opening:  &fakeOpening{cartID: "cart_1"},
		Pricing:  &fakePricing{},
		Checkout: &fakeCheckout{},
	})
}

// newServerWithFlows sets up a router with the given service and flows.
//
// The fields of [api.Flows] may be left nil; the handler failing CLOSED without
// a flow can only be exercised that way.
func newServerWithFlows(t *testing.T, svc *fakeCarts, flows api.Flows) http.Handler {
	t.Helper()

	r := chi.NewRouter()
	api.New(svc, flows).Routes(r)
	return r
}

// adminPrincipal is the tests' default caller: a fully privileged admin identity.
var adminPrincipal = corehttp.Principal{
	ID:     "user_test",
	Kind:   "user",
	Scopes: []string{corehttp.ScopeAdmin},
}

// doRequest sends the given request to the router with a FULLY PRIVILEGED
// identity.
//
// Putting the identity into the context is necessary because the admin endpoints
// are guarded with corehttp.RequireScope: that middleware reads the identity from
// the context and corehttp.RequireAdmin, which puts the identity there, is NOT
// present in this test (the router is built directly). Without the identity every
// admin test in this file would get a 401 before ever reaching the behavior it
// exercises. What the tests verify has not changed; only who the caller is has
// been stated.
func doRequest(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return doRequestAs(t, h, &adminPrincipal, method, path, body)
}

// doRequestAs runs the request with the given identity; if the identity is nil
// the request goes WITHOUT ONE.
func doRequestAs(t *testing.T, h http.Handler, principal *corehttp.Principal, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if principal != nil {
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), *principal))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// bodyMap decodes the response body into a map.
func bodyMap(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), "the response has to be JSON: %s", rec.Body.String())
	return out
}

// object reads a JSON value as an object; if it is not one it fails the test.
func object(t *testing.T, value any) map[string]any {
	t.Helper()

	out, ok := value.(map[string]any)
	require.True(t, ok, "a JSON object was expected, got: %T", value)
	return out
}

// array reads a JSON value as an array; if it is not one it fails the test.
func array(t *testing.T, value any) []any {
	t.Helper()

	out, ok := value.([]any)
	require.True(t, ok, "a JSON array was expected, got: %T", value)
	return out
}

// withCart produces a fake service that has the cart ready both as the response
// record and for the read-back.
//
// Both are needed: the endpoint has the flow open the cart, then reads it back
// from its own service for the RESPONSE (see store.go, Handler.cart). Filling in
// only one of them would make the test get a 500 before ever reaching the
// behavior it exercises.
func withCart(cart models.Cart) *fakeCarts {
	return &fakeCarts{cart: cart, detail: models.CartDetail{Cart: cart}}
}

// TestCreateCartReturns201AndSingleEnvelope verifies that creating a cart returns
// a 201 and the single envelope.
func TestCreateCartReturns201AndSingleEnvelope(t *testing.T) {
	svc := withCart(models.Cart{
		ID: "cart_1", RegionID: "reg_1", CurrencyCode: defaultCurrency,
		CreatedAt: time.Unix(0, 0).UTC(), UpdatedAt: time.Unix(0, 0).UTC(),
	})
	flow := &fakeOpening{cartID: "cart_1"}
	h := newServerWithFlows(t, svc, api.Flows{
		Opening: flow, Pricing: &fakePricing{}, Checkout: &fakeCheckout{},
	})

	rec := doRequest(t, h, http.MethodPost, "/store/v1/carts",
		`{"country_code":"TR","customer_id":"cust_1","email":"a@b.c"}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	data := object(t, bodyMap(t, rec)["data"])
	assert.Equal(t, "cart_1", data["id"])
	assert.Equal(t, false, data["totals_stale"], "staleness has to be presented together with the totals")

	assert.Equal(t, "TR", flow.gotCountry)
	assert.Equal(t, "cust_1", flow.gotCustomerID)
	assert.Equal(t, "a@b.c", flow.gotEmail)
}

// TestCreateCartRegionComesFromTheFlow verifies that the cart's region comes not
// from the body but FROM THE FLOW.
//
// The claim has two parts and both are necessary: the handler has to give the
// COUNTRY to the flow (it must not write a cart to its own service) and the
// region in the response has to be read from the cart's own record. Looking at
// the response alone would not have been enough — a handler that took the region
// from the body and wrote it to the service could produce the same body.
func TestCreateCartRegionComesFromTheFlow(t *testing.T) {
	svc := withCart(models.Cart{ID: "cart_1", RegionID: "reg_tr", CurrencyCode: "JPY"})
	flow := &fakeOpening{cartID: "cart_1"}
	h := newServerWithFlows(t, svc, api.Flows{
		Opening: flow, Pricing: &fakePricing{}, Checkout: &fakeCheckout{},
	})

	rec := doRequest(t, h, http.MethodPost, "/store/v1/carts", `{"country_code":"tr"}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, 1, flow.calls, "the cart has to be opened WITH THE FLOW")
	assert.Equal(t, "tr", flow.gotCountry,
		"the only piece of location information going to the flow has to be the country code; region does the normalization")
	data := object(t, bodyMap(t, rec)["data"])
	assert.Equal(t, "reg_tr", data["region_id"],
		"the region in the response has to be read from THE CART's record")
	assert.Equal(t, "JPY", data["currency_code"],
		"the currency has to come from the same record too; both are the flow's derivation")
}

// TestCreateCartRejectsAClientSuppliedRegion verifies that the storefront DOES
// NOT ACCEPT a region.
//
// The claim has two layers: the request has to be rejected AND no cart may be
// opened. Had the field been silently ignored and the cart opened all the same,
// the client would believe it had sent it while the server opened the cart in
// another region — and that cart would be priced with another tax rate, from
// another price list. The same measure had been applied to the currency_code and
// unit_price fields.
func TestCreateCartRejectsAClientSuppliedRegion(t *testing.T) {
	for name, bodyText := range map[string]string{
		"region_id":     `{"country_code":"TR","region_id":"reg_1"}`,
		"currency_code": `{"country_code":"TR","currency_code":"USD"}`,
	} {
		t.Run(name, func(t *testing.T) {
			svc := withCart(models.Cart{ID: "cart_1"})
			flow := &fakeOpening{cartID: "cart_1"}
			h := newServerWithFlows(t, svc, api.Flows{
				Opening: flow, Pricing: &fakePricing{}, Checkout: &fakeCheckout{},
			})

			rec := doRequest(t, h, http.MethodPost, "/store/v1/carts", bodyText)

			assert.Equal(t, http.StatusUnprocessableEntity, rec.Code,
				"an unknown field has to be rejected; body: %s", rec.Body.String())
			assert.Zero(t, flow.calls, "a rejected request MUST NOT OPEN a cart")
		})
	}
}

// TestCreateCartFailsClosedWithoutTheFlow verifies that while the cart-opening
// flow is not bound the cart is NOT OPENED AT ALL.
//
// Failing closed is deliberate and its reasoning is the same as the pricing
// flow's: if the flow is missing, the correct answer is not "a cart without a
// region" or "the region the client says". The region picks the tax rate and the
// currency derived from it picks which price list is applied; falling back to a
// default would reopen the privilege door that was closed.
func TestCreateCartFailsClosedWithoutTheFlow(t *testing.T) {
	svc := withCart(models.Cart{ID: "cart_1", RegionID: "reg_1"})
	h := newServerWithFlows(t, svc, api.Flows{Pricing: &fakePricing{}, Checkout: &fakeCheckout{}})

	rec := doRequest(t, h, http.MethodPost, "/store/v1/carts", `{"country_code":"TR"}`)

	assert.Equal(t, http.StatusInternalServerError, rec.Code,
		"a setup failure has to be a 5xx to the client; body: %s", rec.Body.String())
	assert.Empty(t, svc.gotCartID, "without the region being derived the cart MUST NOT even be READ")
}

// TestCreateCartUnknownCountryOpensNoCart verifies that a country with no region
// does NOT get a cart opened.
//
// The error KIND is preserved: region's errors.NotFound falls to a 404 and tells
// the client something it can fix ("there is no selling to this country"). Had it
// been turned into Internal, the storefront would take a situation it can fix
// itself for a server failure.
func TestCreateCartUnknownCountryOpensNoCart(t *testing.T) {
	svc := withCart(models.Cart{ID: "cart_1"})
	flow := &fakeOpening{err: errors.NotFound("country_has_no_region", "the country is bound to no region")}
	h := newServerWithFlows(t, svc, api.Flows{
		Opening: flow, Pricing: &fakePricing{}, Checkout: &fakeCheckout{},
	})

	rec := doRequest(t, h, http.MethodPost, "/store/v1/carts", `{"country_code":"ZZ"}`)

	assert.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
	assert.Empty(t, svc.gotCartID, "on an unknown country the cart MUST NOT be READ")
}

// TestCreateCartCarriesMetadataToTheFlow verifies that the cart's free-form extra
// data reaches the flow AS IT IS.
//
// The field is in a class separate from the region and the currency: it really is
// the client's information and it enters no computation. That is why it was not
// removed from the body — but carrying it is mandatory as well, because the only
// way to open a cart now is the flow and had it not been carried the field the
// client sent would silently fall away. The decision is the same one made for the
// line item's metadata.
func TestCreateCartCarriesMetadataToTheFlow(t *testing.T) {
	svc := withCart(models.Cart{ID: "cart_1"})
	flow := &fakeOpening{cartID: "cart_1"}
	h := newServerWithFlows(t, svc, api.Flows{
		Opening: flow, Pricing: &fakePricing{}, Checkout: &fakeCheckout{},
	})

	rec := doRequest(t, h, http.MethodPost, "/store/v1/carts",
		`{"country_code":"TR","metadata":{"source":"storefront"}}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.NotNil(t, flow.gotMetadata, "the metadata has to reach the flow")
	assert.JSONEq(t, `{"source":"storefront"}`, string(flow.gotMetadata))
}

// TestUpdateCartPassesTheFieldsToTheService verifies that the update body is
// passed to the service as it is.
//
// The email being carried as a POINTER is exercised in particular: if the field
// is not in the body at all, nil goes to the service and the stored email is
// preserved; if the body has an empty string, the intent to clear it reaches the
// service.
func TestUpdateCartPassesTheFieldsToTheService(t *testing.T) {
	svc := &fakeCarts{cart: models.Cart{
		ID: "cart_1", RegionID: "reg_1", CurrencyCode: "TRY", CustomerID: "cust_1",
	}}
	h := newServer(t, svc)

	rec := doRequest(t, h, http.MethodPost, "/store/v1/carts/cart_1",
		`{"email":"a@b.c","customer_id":"cust_1"}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "cart_1", svc.gotCartID)
	require.NotNil(t, svc.updateInput.Email)
	assert.Equal(t, "a@b.c", *svc.updateInput.Email)
	assert.Equal(t, "cust_1", svc.updateInput.CustomerID)
	assert.Equal(t, "cart_1", object(t, bodyMap(t, rec)["data"])["id"])

	// An email that is not sent and an email meant to be cleared are separate
	// intents.
	rec = doRequest(t, h, http.MethodPost, "/store/v1/carts/cart_1", `{"customer_id":"cust_1"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Nil(t, svc.updateInput.Email, "a field that is not sent has to reach the service as nil")

	rec = doRequest(t, h, http.MethodPost, "/store/v1/carts/cart_1", `{"email":""}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotNil(t, svc.updateInput.Email, "an empty string is the intent to clear")
	assert.Empty(t, *svc.updateInput.Email)
}

// TestGetCartReturnsItsChildren verifies that the cart detail returns the line
// item, the address and the shipping method together.
func TestGetCartReturnsItsChildren(t *testing.T) {
	svc := &fakeCarts{detail: models.CartDetail{
		Cart:  models.Cart{ID: "cart_1", RegionID: "reg_1", CurrencyCode: "TRY", Revision: 2, TotalsRevision: 1},
		Items: []models.LineItem{{ID: "li_1", CartID: "cart_1", VariantID: "var_1", Title: "T-shirt", Quantity: 2}},
		ShippingAddress: &models.CartAddress{
			ID: "addr_1", CartID: "cart_1", Type: models.AddressShipping, City: "Istanbul",
		},
		ShippingMethods: []models.ShippingMethod{{ID: "csm_1", CartID: "cart_1", Name: "Standard", Amount: 2500}},
	}}
	h := newServer(t, svc)

	rec := doRequest(t, h, http.MethodGet, "/store/v1/carts/cart_1", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "cart_1", svc.gotCartID)

	data := object(t, bodyMap(t, rec)["data"])
	assert.Equal(t, true, data["totals_stale"], "a stale total has to be visible in the response")
	items := array(t, data["items"])
	require.Len(t, items, 1)
	assert.Equal(t, "li_1", object(t, items[0])["id"])
	assert.Equal(t, "Istanbul", object(t, data["shipping_address"])["city"])
	assert.Equal(t, "shipping", object(t, data["shipping_address"])["type"])
	assert.Nil(t, data["billing_address"], "a record that does not exist must not appear in the response")
	methods := array(t, data["shipping_methods"])
	require.Len(t, methods, 1)
	assert.InDelta(t, 2500, object(t, methods[0])["amount"], 0.0)
}

// TestEmptyCartChildFieldsAreArrays verifies that on a cart with no children the
// arrays come back as an empty array and NOT as null.
//
// Had null been returned, clients would have to do a nil check everywhere.
func TestEmptyCartChildFieldsAreArrays(t *testing.T) {
	svc := &fakeCarts{detail: models.CartDetail{Cart: models.Cart{ID: "cart_1"}}}
	h := newServer(t, svc)

	rec := doRequest(t, h, http.MethodGet, "/store/v1/carts/cart_1", "")

	require.Equal(t, http.StatusOK, rec.Code)
	data := object(t, bodyMap(t, rec)["data"])
	assert.Equal(t, []any{}, data["items"])
	assert.Equal(t, []any{}, data["shipping_methods"])
}

// withLineItem produces a fake service carrying a single line item in the cart
// detail.
//
// The line item's amounts are FILLED IN and that is deliberate: the handler READS
// the response out of the cart with the id returned by the flow, that is, the
// price in the response is the one written on the cart.
func withLineItem() *fakeCarts {
	return &fakeCarts{detail: models.CartDetail{
		Cart: models.Cart{ID: "cart_1", CurrencyCode: "TRY", Email: "a@b.c"},
		Items: []models.LineItem{{
			ID: "li_1", CartID: "cart_1", VariantID: "var_1", Title: "T-shirt",
			Quantity: 3, UnitPrice: 1000, Subtotal: 3000, Total: 3000,
		}},
	}}
}

// TestAddLineItemReturns201 verifies that adding a line item returns a 201 and
// hands the request over TO THE FLOW.
//
// The handler not calling the service's AddLineItem is half of the claim: the
// party that writes the line item is the flow, because it is the only party that
// knows the price.
func TestAddLineItemReturns201(t *testing.T) {
	svc := withLineItem()
	flow := &fakePricing{lineID: "li_1"}
	h := newServerWithFlows(t, svc, api.Flows{Pricing: flow})

	rec := doRequest(t, h, http.MethodPost, "/store/v1/carts/cart_1/line-items",
		`{"variant_id":"var_1","quantity":3,"metadata":{"note":"gift"}}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, 1, flow.calls, "the line item has to be written by the flow")
	assert.Equal(t, "cart_1", flow.gotCartID)
	assert.Equal(t, "var_1", flow.gotVariantID)
	assert.Equal(t, int64(3), flow.gotQuantity)
	assert.JSONEq(t, `{"note":"gift"}`, string(flow.gotMetadata),
		"the metadata really is the client's information and has to be carried to the flow")
	assert.Empty(t, svc.addInput.VariantID, "the line item MUST NOT be written to the service DIRECTLY")

	// The response is read from the cart: the price shown is the one written on
	// the cart.
	data := object(t, bodyMap(t, rec)["data"])
	assert.Equal(t, "li_1", data["id"])
	assert.InDelta(t, 1000, data["unit_price"], 0.0)
}

// TestAddLineItemRejectsAClientSuppliedPrice verifies that the storefront DOES
// NOT ACCEPT a price or a title.
//
// The failure that was found was exactly this: the "unit_price" in the body was
// written to the service as it is and the workflow that was said to write the
// final price was wired in no setup. The storefront's identity is the publishable
// key — that is, this was a "write your own price" endpoint open to everyone.
//
// Silently IGNORING the fields would not have been enough: an old client would
// believe it had sent them while the server wrote another price. The body REJECTS
// an unknown field.
func TestAddLineItemRejectsAClientSuppliedPrice(t *testing.T) {
	for name, bodyText := range map[string]string{
		"price":  `{"variant_id":"var_1","quantity":3,"unit_price":1}`,
		"title":  `{"variant_id":"var_1","quantity":3,"title":"Free T-shirt"}`,
		"both":   `{"variant_id":"var_1","quantity":3,"title":"X","unit_price":1}`,
		"zeroed": `{"variant_id":"var_1","quantity":3,"unit_price":0}`,
	} {
		t.Run(name, func(t *testing.T) {
			svc := withLineItem()
			flow := &fakePricing{lineID: "li_1"}
			h := newServerWithFlows(t, svc, api.Flows{Pricing: flow})

			rec := doRequest(t, h, http.MethodPost, "/store/v1/carts/cart_1/line-items", bodyText)

			assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
			assert.Zero(t, flow.calls, "a rejected request must not reach the flow at all")
			assert.Empty(t, svc.addInput.VariantID, "no line item may be written to the service")
		})
	}
}

// TestAddLineItemAddsNothingWithoutAPricer verifies that the price path fails
// CLOSED.
//
// This is the opposite of the b2b spending rule and the difference is deliberate:
// if b2b is not set up "no limit" is the correct answer, but if the pricer is
// missing, writing a "no price" line item is silently selling goods for free. The
// only correct outcome is the line item NOT being added at all.
func TestAddLineItemAddsNothingWithoutAPricer(t *testing.T) {
	svc := withLineItem()
	h := newServerWithFlows(t, svc, api.Flows{})

	rec := doRequest(t, h, http.MethodPost, "/store/v1/carts/cart_1/line-items",
		`{"variant_id":"var_1","quantity":3}`)

	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	assert.Empty(t, svc.addInput.VariantID, "a line item whose price is unknown must not be written to the cart")
}

// TestAddLineItemQuantityIsMandatory verifies that the request is rejected if
// there is no quantity in the body.
//
// Had the quantity not been a pointer, a client that never sent the field would
// count as having sent "a quantity of zero".
func TestAddLineItemQuantityIsMandatory(t *testing.T) {
	svc := &fakeCarts{}
	flow := &fakePricing{}
	h := newServerWithFlows(t, svc, api.Flows{Pricing: flow})

	rec := doRequest(t, h, http.MethodPost, "/store/v1/carts/cart_1/line-items",
		`{"variant_id":"var_1"}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Zero(t, flow.calls, "the flow must not be called")
}

// TestUpdateLineItemWritesTheQuantity verifies that the quantity update is bound
// TO THE FLOW.
//
// The quantity can change the price (pricing picks the unit price according to
// the quantity range), which is why the path goes not to the service but to the
// flow: had it gone to the service, the line item would stay with the new
// quantity but with the old tier's price.
func TestUpdateLineItemWritesTheQuantity(t *testing.T) {
	svc := withLineItem()
	flow := &fakePricing{}
	h := newServerWithFlows(t, svc, api.Flows{Pricing: flow})

	rec := doRequest(t, h, http.MethodPatch, "/store/v1/carts/cart_1/line-items/li_1", `{"quantity":5}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "cart_1", flow.gotCartID)
	assert.Equal(t, "li_1", flow.gotLineID)
	assert.Equal(t, int64(5), flow.gotQuantity)
	assert.Zero(t, svc.gotQuantity, "the quantity MUST NOT be written to the service DIRECTLY")
}

// TestUpdateLineItemZeroQuantityRemovesTheLineItem verifies that a quantity of
// zero removes the line item and returns a bodyless 204.
//
// Presenting the record of a removed line item in the response would mean handing
// the client a resource that no longer exists.
func TestUpdateLineItemZeroQuantityRemovesTheLineItem(t *testing.T) {
	svc := withLineItem()
	flow := &fakePricing{removed: true}
	h := newServerWithFlows(t, svc, api.Flows{Pricing: flow})

	rec := doRequest(t, h, http.MethodPatch, "/store/v1/carts/cart_1/line-items/li_1", `{"quantity":0}`)

	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	assert.Empty(t, rec.Body.String())
	assert.Equal(t, int64(0), flow.gotQuantity)
}

// TestCompleteCartProducesAnOrder verifies that the completion endpoint calls the
// flow and returns the order.
func TestCompleteCartProducesAnOrder(t *testing.T) {
	svc := withLineItem()
	flow := &fakeCheckout{response: json.RawMessage(
		`{"order_id":"order_1","cart_id":"cart_1","currency_code":"TRY","amount":3600}`)}
	h := newServerWithFlows(t, svc, api.Flows{Checkout: flow})

	rec := doRequest(t, h, http.MethodPost, "/store/v1/carts/cart_1/complete",
		`{"payment_provider_id":"test","payment_data":{"token":"tok_1"},"expected_total":3600}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, flow.calls)

	data := object(t, bodyMap(t, rec)["data"])
	assert.Equal(t, "order_1", data["order_id"])
	assert.InDelta(t, 3600, data["total"], 0.0)

	// The request going to the flow: the cart id comes from the path, the email
	// FROM THE CART.
	sent := map[string]any{}
	require.NoError(t, json.Unmarshal(flow.got, &sent))
	assert.Equal(t, "cart_1", sent["cart_id"])
	assert.Equal(t, "test", sent["payment_provider_id"])
	assert.InDelta(t, 3600, sent["expected_total"], 0.0)
	assert.Equal(t, "a@b.c", sent["email"],
		"the contact address is the cart's data; it is not taken from the client")
}

// TestCompleteCartEmailIsNotTakenFromTheBody verifies that the email CANNOT
// TRAVEL in the request body.
//
// Had the field been accepted, the order could be bound to an address other than
// the one visible on the cart; the address's only source is the cart.
func TestCompleteCartEmailIsNotTakenFromTheBody(t *testing.T) {
	svc := withLineItem()
	flow := &fakeCheckout{}
	h := newServerWithFlows(t, svc, api.Flows{Checkout: flow})

	rec := doRequest(t, h, http.MethodPost, "/store/v1/carts/cart_1/complete",
		`{"payment_provider_id":"test","expected_total":3600,"email":"attacker@x.y"}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Zero(t, flow.calls, "a rejected request must not reach the flow")
}

// TestCompleteCartLocationIsNotTakenFromTheBody verifies that the warehouse
// choice is not left to the client.
//
// Which warehouse things ship out of is a shipping decision; accepting the field
// would both leak the stock topology and leave where the order ships from up to
// the customer.
func TestCompleteCartLocationIsNotTakenFromTheBody(t *testing.T) {
	svc := withLineItem()
	flow := &fakeCheckout{}
	h := newServerWithFlows(t, svc, api.Flows{Checkout: flow})

	rec := doRequest(t, h, http.MethodPost, "/store/v1/carts/cart_1/complete",
		`{"payment_provider_id":"test","expected_total":3600,"location_id":"sloc_1"}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Zero(t, flow.calls)
}

// TestCompleteCartApprovedTotalIsMandatory verifies that the absence of
// expected_total is rejected.
//
// Had it been optional, every client that forgot the field would silently switch
// off the "is the amount you saw the same as the amount charged" protection — the
// rule defined, the place where it is applied missing.
func TestCompleteCartApprovedTotalIsMandatory(t *testing.T) {
	svc := withLineItem()
	flow := &fakeCheckout{}
	h := newServerWithFlows(t, svc, api.Flows{Checkout: flow})

	rec := doRequest(t, h, http.MethodPost, "/store/v1/carts/cart_1/complete",
		`{"payment_provider_id":"test"}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Zero(t, flow.calls, "the flow must not run without the approved total being declared")
}

// TestCompleteCartDoesNotCompleteWithoutTheFlow verifies that while the flow is
// not bound the cart is not completed.
func TestCompleteCartDoesNotCompleteWithoutTheFlow(t *testing.T) {
	h := newServerWithFlows(t, withLineItem(), api.Flows{})

	rec := doRequest(t, h, http.MethodPost, "/store/v1/carts/cart_1/complete",
		`{"payment_provider_id":"test","expected_total":3600}`)

	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
}

// TestCompleteCartReturnsTheFlowErrorAsItIs verifies that the flow's error KIND
// is preserved.
//
// When the approved amount and the computed amount drift apart the flow returns
// errors.Conflict and the client has to see that as a 409: had it seen a 500 it
// would retry thinking "the server is broken", whereas what it has to do is get
// the new amount approved by the customer.
func TestCompleteCartReturnsTheFlowErrorAsItIs(t *testing.T) {
	flow := &fakeCheckout{err: errors.Conflict("checkout_workflow_total_mismatch",
		"the approved total does not match the computed total")}
	h := newServerWithFlows(t, withLineItem(), api.Flows{Checkout: flow})

	rec := doRequest(t, h, http.MethodPost, "/store/v1/carts/cart_1/complete",
		`{"payment_provider_id":"test","expected_total":1}`)

	assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
}

// TestRemoveLineItemReturns204 verifies that removing a line item returns a
// bodyless 204.
func TestRemoveLineItemReturns204(t *testing.T) {
	svc := &fakeCarts{}
	h := newServer(t, svc)

	rec := doRequest(t, h, http.MethodDelete, "/store/v1/carts/cart_1/line-items/li_1", "")

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String())
	assert.Equal(t, "li_1", svc.gotLineID)
}

// TestAddressEndpointsLandOnSeparateMethods verifies that the shipping and
// billing endpoints go to SEPARATE service methods.
//
// Had the two been bound to the same method, the billing address would overwrite
// the shipping address.
func TestAddressEndpointsLandOnSeparateMethods(t *testing.T) {
	svc := &fakeCarts{addr: models.CartAddress{ID: "addr_1", Type: models.AddressBilling}}
	h := newServer(t, svc)

	rec := doRequest(t, h, http.MethodPut, "/store/v1/carts/cart_1/shipping-address", `{"city":"Istanbul"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.False(t, svc.billing, "the shipping endpoint has to call SetShippingAddress")
	assert.Equal(t, "Istanbul", svc.addressInput.City)

	rec = doRequest(t, h, http.MethodPut, "/store/v1/carts/cart_1/billing-address", `{"city":"Ankara"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.True(t, svc.billing, "the billing endpoint has to call SetBillingAddress")
	assert.Equal(t, "Ankara", svc.addressInput.City)
}

// TestShippingMethodEndpoints verifies that adding and removing a shipping method
// work with the right parameters.
func TestShippingMethodEndpoints(t *testing.T) {
	svc := &fakeCarts{method: models.ShippingMethod{ID: "csm_1", Name: "Standard", Amount: 2500}}
	h := newServer(t, svc)

	rec := doRequest(t, h, http.MethodPost, "/store/v1/carts/cart_1/shipping-methods",
		`{"name":"Standard","amount":2500,"shipping_option_id":"so_1"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, "Standard", svc.shippingInput.Name)
	assert.Equal(t, int64(2500), svc.shippingInput.Amount)
	assert.Equal(t, "so_1", svc.shippingInput.ShippingOptionID)

	rec = doRequest(t, h, http.MethodDelete, "/store/v1/carts/cart_1/shipping-methods/csm_1", "")
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "csm_1", svc.gotMethodID)
}

// TestAdminListEnvelope verifies that the admin list returns the list envelope
// and passes the filters through.
func TestAdminListEnvelope(t *testing.T) {
	svc := &fakeCarts{
		carts: []models.Cart{{ID: "cart_1"}, {ID: "cart_2"}},
		count: 42,
	}
	h := newServer(t, svc)

	rec := doRequest(t, h, http.MethodGet,
		"/admin/v1/carts?limit=2&offset=4&customer_id=cust_1&region_id=reg_1&completed=true", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	payload := bodyMap(t, rec)
	assert.Len(t, payload["data"], 2)
	assert.InDelta(t, 42, payload["count"], 0.0, "count has to be the filter's count, not the page's")
	assert.InDelta(t, 4, payload["offset"], 0.0)
	assert.InDelta(t, 2, payload["limit"], 0.0)

	require.NotNil(t, svc.listInput.CustomerID)
	assert.Equal(t, "cust_1", *svc.listInput.CustomerID)
	require.NotNil(t, svc.listInput.RegionID)
	assert.Equal(t, "reg_1", *svc.listInput.RegionID)
	require.NotNil(t, svc.listInput.Completed)
	assert.True(t, *svc.listInput.Completed)
}

// TestAdminListDefaultLimit verifies that when no limit is given the bound that
// is REALLY applied shows up in the response.
func TestAdminListDefaultLimit(t *testing.T) {
	svc := &fakeCarts{}
	h := newServer(t, svc)

	rec := doRequest(t, h, http.MethodGet, "/admin/v1/carts", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.InDelta(t, float64(service.DefaultLimit), bodyMap(t, rec)["limit"], 0.0)
}

// TestAdminHasNoWriteEndpoint verifies that the admin side CANNOT CHANGE the
// cart.
//
// The only party that changes the cart is the customer; a correction made from
// the admin panel would mean changing the amount the customer saw behind their
// back.
func TestAdminHasNoWriteEndpoint(t *testing.T) {
	h := newServer(t, &fakeCarts{})

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/admin/v1/carts"},
		{http.MethodPatch, "/admin/v1/carts/cart_1"},
		{http.MethodDelete, "/admin/v1/carts/cart_1"},
		{http.MethodPut, "/admin/v1/carts/cart_1"},
	} {
		rec := doRequest(t, h, tc.method, tc.path, `{}`)
		assert.Contains(t, []int{http.StatusNotFound, http.StatusMethodNotAllowed}, rec.Code,
			"%s %s must not be bound", tc.method, tc.path)
	}
}

// TestTotalsEndpointsAreNotOpenedToHTTP verifies that the service's workflow
// surface is NOT OPENED to HTTP.
//
// [service.Service.SetTotals] and [service.Service.MarkCompleted] still get no
// route: had they been open, a client could write the cart's amount itself or
// close the cart without paying.
//
// The storefront's /complete is NOT an exception to this rule and that is why it
// is not in the list: that endpoint does not stamp the cart "completed", it runs
// the complete_cart saga — stock is reserved, the order is opened, the payment is
// captured and only AFTER THAT is the cart closed, by the flow. The authority to
// close still sits not in HTTP but in the flow
// (see [TestCompleteCartProducesAnOrder]). There is no counterpart on the admin
// side: the party that completes the cart is the customer.
func TestTotalsEndpointsAreNotOpenedToHTTP(t *testing.T) {
	h := newServer(t, &fakeCarts{})

	for _, path := range []string{
		"/store/v1/carts/cart_1/totals",
		"/admin/v1/carts/cart_1/totals",
		"/admin/v1/carts/cart_1/complete",
	} {
		rec := doRequest(t, h, http.MethodPost, path, `{}`)
		assert.Contains(t, []int{http.StatusNotFound, http.StatusMethodNotAllowed}, rec.Code,
			"%s must not be bound", path)
	}
}

// TestErrorKindsAreTranslatedToStatusCodes verifies that service errors are
// translated into the status code matching their kind (plan Section 8).
//
// The handler does NOT PICK the status code; the mapping is in
// corehttp.WriteError and this test shows that the chain really is wired up.
func TestErrorKindsAreTranslatedToStatusCodes(t *testing.T) {
	cases := map[string]struct {
		err    error
		status int
	}{
		"not found": {errors.NotFound("cart_not_found", "the cart does not exist"), http.StatusNotFound},
		"invalid":   {errors.Invalid("cart_invalid_input", "the quantity has to be positive"), http.StatusUnprocessableEntity},
		"conflict":  {errors.Conflict("cart_completed", "the cart is completed"), http.StatusConflict},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			svc := &fakeCarts{err: tc.err}
			h := newServer(t, svc)

			rec := doRequest(t, h, http.MethodGet, "/store/v1/carts/cart_1", "")

			require.Equal(t, tc.status, rec.Code, rec.Body.String())
			errBody := object(t, bodyMap(t, rec)["error"])
			assert.Equal(t, errors.CodeOf(tc.err), errBody["code"])
			assert.NotEmpty(t, errBody["message"])
		})
	}
}

// TestInternalErrorLeaksNoDetail verifies that a server error does not write the
// underlying message to the client.
func TestInternalErrorLeaksNoDetail(t *testing.T) {
	svc := &fakeCarts{err: errors.Internal("cart_query_failed",
		"pq: relation \"carts\" does not exist (host=10.0.0.1)")}
	h := newServer(t, svc)

	rec := doRequest(t, h, http.MethodGet, "/store/v1/carts/cart_1", "")

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "10.0.0.1")
	assert.NotContains(t, rec.Body.String(), "relation")
}

// TestUnknownBodyFieldIsRejected verifies that an unknown field in the body is
// NOT SWALLOWED silently.
//
// A swallowed field means a setting the client believes it sent but that is never
// applied.
func TestUnknownBodyFieldIsRejected(t *testing.T) {
	svc := &fakeCarts{}
	h := newServer(t, svc)

	rec := doRequest(t, h, http.MethodPost, "/store/v1/carts",
		`{"country_code":"TR","discount":"free"}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Empty(t, svc.gotCartID, "the service must not be called")
}

// TestEmptyBodyIsRejected verifies that a write request without a body is
// rejected.
func TestEmptyBodyIsRejected(t *testing.T) {
	h := newServer(t, &fakeCarts{})

	rec := doRequest(t, h, http.MethodPost, "/store/v1/carts", "")

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestInvalidPaginationParameter verifies that the request is rejected if a
// pagination parameter is not a number.
func TestInvalidPaginationParameter(t *testing.T) {
	h := newServer(t, &fakeCarts{})

	rec := doRequest(t, h, http.MethodGet, "/admin/v1/carts?limit=many", "")

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestInvalidCompletedParameter verifies that the request is rejected if the
// completed filter is not a boolean.
func TestInvalidCompletedParameter(t *testing.T) {
	h := newServer(t, &fakeCarts{})

	rec := doRequest(t, h, http.MethodGet, "/admin/v1/carts?completed=maybe", "")

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestNarrowScopePassesOnAdminRead verifies that an identity carrying only
// [api.ScopeRead] gets through the admin READ endpoint.
//
// The value of enforcing scopes lies in it REALLY accepting a narrow scope too:
// had it only rejected, nobody would hand out narrow scopes and everyone would be
// given admin.
func TestNarrowScopePassesOnAdminRead(t *testing.T) {
	h := newServer(t, &fakeCarts{})
	narrowPrincipal := corehttp.Principal{ID: "user_narrow", Kind: "user", Scopes: []string{api.ScopeRead}}

	rec := doRequestAs(t, h, &narrowPrincipal, http.MethodGet, "/admin/v1/carts", "")

	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// TestUnscopedIdentityGets403OnAdminRead verifies that the carts are CLOSED to an
// admin identity whose scopes have been emptied.
//
// The scenario this test exercises is concrete: a user who opens a valid session
// but has no scope at all could read every customer's cart together with their
// email addresses with GET /admin/v1/carts.
//
// Because cart's admin surface has no WRITE endpoint, the case "going to a write
// endpoint with a read scope" cannot be exercised here; an identity carrying
// [api.ScopeWrite] is used instead and it is shown that the write scope DOES NOT
// OPEN the read.
func TestUnscopedIdentityGets403OnAdminRead(t *testing.T) {
	h := newServer(t, &fakeCarts{})

	for name, principal := range map[string]corehttp.Principal{
		"no scope":       {ID: "user_empty", Kind: "user", Scopes: []string{}},
		"another module": {ID: "user_ord", Kind: "user", Scopes: []string{"order:read"}},
		"write only":     {ID: "user_writer", Kind: "user", Scopes: []string{api.ScopeWrite}},
	} {
		t.Run(name, func(t *testing.T) {
			rec := doRequestAs(t, h, &principal, http.MethodGet, "/admin/v1/carts", "")

			assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
		})
	}
}

// TestAnonymousAdminRequestGets401 verifies that a request with no identity at
// all gets a 401 and one with an insufficient scope gets a 403.
//
// The distinction is deliberate: 401 means "tell me who you are", 403 means "I
// know who you are but you have no scope". Had the two been mixed up, the client
// would try refreshing its session for a problem that will not be solved by
// renewing its identity.
func TestAnonymousAdminRequestGets401(t *testing.T) {
	h := newServer(t, &fakeCarts{})

	rec := doRequestAs(t, h, nil, http.MethodGet, "/admin/v1/carts", "")

	assert.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
}

// TestStoreEndpointsRequireNoScope verifies that NO scope is ADDED to the store
// surface.
//
// The store surface's identity is the publishable key and that key by definition
// carries no scope; if a scope is attached to the store endpoints by mistake the
// storefront becomes completely unusable and this test catches it right away.
func TestStoreEndpointsRequireNoScope(t *testing.T) {
	h := newServer(t, &fakeCarts{})

	rec := doRequestAs(t, h, nil, http.MethodPost, "/store/v1/carts", `{"country_code":"TR"}`)

	assert.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
}
