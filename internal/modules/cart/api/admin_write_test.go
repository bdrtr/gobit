package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/cart/api"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
)

// The admin write surface: an operator taking an order over the telephone
// (ADR 0146).
//
// # What these tests can prove and what they cannot
//
// They prove that the channel the LINE WRITE names is the channel the FLOW is
// run under — the handler's whole job, since the cart's scope rule lives in the
// flow and reads the identity rather than an argument. What they cannot prove is that
// the catalog then filters by it: the flows here are fakes, and a fake catalog
// answers the same for every channel. That half is the integration test's
// subject, with a variant assigned to one channel and refused for the other,
// and it is written down here because a unit test passing is exactly what a
// handler that dropped the claim would also produce.

// channelRecordingOpening is the cart-opening flow's stand-in, and the only
// thing it records is the IDENTITY it was run under.
//
// It reads the channels the same way the real flow does — through
// corehttp.SalesChannelIDs, the one derivation — so what the test asserts is
// what the flow would see, not a second reading of the field.
type channelRecordingOpening struct {
	cartID string

	gotChannels   []string
	gotPrincipal  corehttp.Principal
	gotCustomerID string
	gotAddsTo     string
	gotOpenedBy   string
	calls         int
}

var _ api.CartOpening = (*channelRecordingOpening)(nil)

// OpenCartForCountry records the identity and returns the scripted id.
func (f *channelRecordingOpening) OpenCartForCountry(
	ctx context.Context, _, customerID, _, addsToOrderID, openedBy string, _ json.RawMessage,
) (string, error) {
	f.calls++
	f.gotChannels = corehttp.SalesChannelIDs(ctx)
	f.gotPrincipal, _ = corehttp.PrincipalFromContext(ctx)
	f.gotCustomerID = customerID
	f.gotAddsTo = addsToOrderID
	f.gotOpenedBy = openedBy

	return f.cartID, nil
}

// channelRecordingPricing is the line pricing flow's stand-in; it records the
// identity for [channelRecordingOpening]'s reason.
type channelRecordingPricing struct {
	lineID string

	gotChannels   []string
	gotCartID     string
	gotVariant    string
	gotQuantity   int64
	gotProperties map[string]string
	calls         int
}

var _ api.LinePricing = (*channelRecordingPricing)(nil)

// AddPricedLineItem records the identity and the arguments.
func (f *channelRecordingPricing) AddPricedLineItem(
	ctx context.Context, cartID, variantID string, quantity int64, _ json.RawMessage, properties map[string]string,
	_ json.RawMessage,
) (string, error) {
	f.calls++
	f.gotProperties = properties
	f.gotChannels = corehttp.SalesChannelIDs(ctx)
	f.gotCartID, f.gotVariant, f.gotQuantity = cartID, variantID, quantity

	return f.lineID, nil
}

// SetLineItemQuantity is not reached from the admin surface; it exists so the
// stand-in satisfies the flow's interface.
func (f *channelRecordingPricing) SetLineItemQuantity(
	context.Context, string, string, int64,
) (bool, error) {
	return false, nil
}

// adminWriter is the caller these tests use: an administrator holding the write
// scope and NO channel of their own.
//
// The empty channel list is the state that makes the endpoint necessary. An
// admin key carries no channel — it is not a publishable key — and a principal
// with none is bound to no channel rather than unscoped, so without the claim in
// the body the catalog would answer with the products assigned to nothing.
var adminWriter = corehttp.Principal{
	ID:     "user_operator",
	Kind:   "user",
	Scopes: []string{"cart:write"},
}

// newAdminWriteServer builds the router with the recording flows.
func newAdminWriteServer(
	t *testing.T, opening *channelRecordingOpening, pricing *channelRecordingPricing,
) http.Handler {
	t.Helper()

	svc := &fakeCarts{
		detail: models.CartDetail{
			Cart:  models.Cart{ID: "cart_1", OpenedBy: "user_operator"},
			Items: []models.LineItem{{ID: "item_1", Title: "A shirt"}},
		},
	}

	r := chi.NewRouter()
	api.New(svc, api.Flows{Opening: opening, Pricing: pricing},
		boundTo(signedInAs(testCustomerID)), false).Routes(r)

	return r
}

// TestAnAdminCartOpensForTheCustomerTheOperatorNames verifies the endpoint's
// whole difference from the storefront's: the customer is taken on the
// operator's word.
func TestAnAdminCartOpensForTheCustomerTheOperatorNames(t *testing.T) {
	opening := &channelRecordingOpening{cartID: "cart_1"}
	h := newAdminWriteServer(t, opening, &channelRecordingPricing{})

	rec := doRequestAs(t, h, &adminWriter, http.MethodPost, "/admin/v1/carts",
		`{"country_code":"tr","customer_id":"cust_9","email":"a@example.test",`+
			`"adds_to_order_id":"order_7"}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, 1, opening.calls)
	assert.Equal(t, "cust_9", opening.gotCustomerID,
		"the customer reaches the flow unproven; on the storefront the same field "+
			"has to be proved first (ADR 0125)")
	assert.Equal(t, "order_7", opening.gotAddsTo,
		"the order the cart adds to reaches the flow, which asks the order module (ADR 0192)")

	// The identity reaches the flow whole, or the audit row would name nobody.
	assert.Equal(t, "user_operator", opening.gotPrincipal.ID)
	assert.Equal(t, []string{"cart:write"}, opening.gotPrincipal.Scopes)
	assert.Equal(t, "user_operator", opening.gotOpenedBy,
		"the cart names the operator the guard ring proved (ADR 0296)")
}

// TestAnAdminCartNamesNoOperatorABodyNames verifies that the opener is the
// caller's identity and nothing the body says: a body that names one is refused
// rather than believed (ADR 0296).
func TestAnAdminCartNamesNoOperatorABodyNames(t *testing.T) {
	opening := &channelRecordingOpening{cartID: "cart_1"}
	h := newAdminWriteServer(t, opening, &channelRecordingPricing{})

	rec := doRequestAs(t, h, &adminWriter, http.MethodPost, "/admin/v1/carts",
		`{"country_code":"tr","opened_by":"user_someone_else"}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Zero(t, opening.calls, "a refused body opens no cart")
}

// TestOpeningACartMayNameItsChannel verifies that an operator's cart is opened
// in the channel the body names, and in none when it names none (ADR 0397).
//
// The claim is asserted into the principal, so the flow derives the cart's
// channel from it the one way it derives a shopper's. The three halves are the
// three answers: a channel reaches the flow, a body without one leaves the
// operator's identity untouched, and a blank one is read as none rather than
// refused, since the field is optional here and mandatory only where a catalog
// is read.
func TestOpeningACartMayNameItsChannel(t *testing.T) {
	opening := &channelRecordingOpening{cartID: "cart_1"}
	h := newAdminWriteServer(t, opening, &channelRecordingPricing{})

	with := doRequestAs(t, h, &adminWriter, http.MethodPost, "/admin/v1/carts",
		`{"country_code":"tr","sales_channel_id":" sc_phone "}`)
	require.Equal(t, http.StatusCreated, with.Code, with.Body.String())
	assert.Equal(t, []string{"sc_phone"}, opening.gotChannels,
		"the flow opens the cart under the channel the operator named, trimmed")
	assert.Equal(t, "user_operator", opening.gotPrincipal.ID, "the audit still names the operator")

	for name, body := range map[string]string{
		"absent": `{"country_code":"tr"}`,
		"blank":  `{"country_code":"tr","sales_channel_id":"   "}`,
	} {
		opening.gotPrincipal = corehttp.Principal{}
		rec := doRequestAs(t, h, &adminWriter, http.MethodPost, "/admin/v1/carts", body)
		require.Equal(t, http.StatusCreated, rec.Code, "%s: %s", name, rec.Body.String())
		assert.Nil(t, opening.gotPrincipal.SalesChannelIDs,
			"%s: the flow runs under the operator's own identity, untouched", name)
		assert.Equal(t, "user_operator", opening.gotPrincipal.ID, name)
	}
	assert.Equal(t, 3, opening.calls)
}

// TestAnAdminLineIsPricedUnderTheChannelTheRequestNames verifies the claim on the
// write that reads the catalog.
//
// It is a separate case from the cart's, deliberately: the two handlers make the
// claim independently — the opening's is optional and names the channel the cart
// is priced in (ADR 0397), the line's is mandatory and names the catalog — and a
// fixture asserting both through one request would let an implementation that
// dropped it on the line write pass on the cart's.
func TestAnAdminLineIsPricedUnderTheChannelTheRequestNames(t *testing.T) {
	pricing := &channelRecordingPricing{lineID: "item_1"}
	h := newAdminWriteServer(t, &channelRecordingOpening{}, pricing)

	rec := doRequestAs(t, h, &adminWriter, http.MethodPost,
		"/admin/v1/carts/cart_1/line-items",
		`{"sales_channel_id":"sc_phone","variant_id":"var_1","quantity":2,"properties":{"Engraving":"B"}}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, map[string]string{"Engraving": "B"}, pricing.gotProperties, "ADR 0223")
	assert.Equal(t, []string{"sc_phone"}, pricing.gotChannels)
	assert.Equal(t, "cart_1", pricing.gotCartID)
	assert.Equal(t, "var_1", pricing.gotVariant)
	assert.Equal(t, int64(2), pricing.gotQuantity)
}

// TestAnAdminLineRefusesToGuessTheChannel verifies that the scope claim is made
// per REQUEST: the channel the cart remembers prices it and does not scope its
// lines (ADR 0397).
func TestAnAdminLineRefusesToGuessTheChannel(t *testing.T) {
	pricing := &channelRecordingPricing{lineID: "item_1"}
	h := newAdminWriteServer(t, &channelRecordingOpening{}, pricing)

	rec := doRequestAs(t, h, &adminWriter, http.MethodPost,
		"/admin/v1/carts/cart_1/line-items", `{"variant_id":"var_1","quantity":2}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Zero(t, pricing.calls, "nothing may be priced without the claim")
}

// TestAnAdminLineNeedsAQuantityOfItsOwn verifies that an ABSENT quantity is
// refused rather than read as zero.
//
// A missing field decodes to the zero value, and the pointer is what separates
// "the operator sent 0" from "the operator sent nothing". Only the second is
// this handler's to refuse: a quantity of zero IS a number, and the flow refuses
// it for the same reason it does on the storefront. What would be silent is
// treating an absent field as a request to buy nothing.
func TestAnAdminLineNeedsAQuantityOfItsOwn(t *testing.T) {
	pricing := &channelRecordingPricing{lineID: "item_1"}
	h := newAdminWriteServer(t, &channelRecordingOpening{}, pricing)

	rec := doRequestAs(t, h, &adminWriter, http.MethodPost,
		"/admin/v1/carts/cart_1/line-items",
		`{"sales_channel_id":"sc_phone","variant_id":"var_1"}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Zero(t, pricing.calls)
	assert.Contains(t, rec.Body.String(), "quantity")
}

// TestTheAdminLineAnswersInTheSameShapeTheStorefrontDoes verifies that the
// response is the DTO and not the module's model.
//
// The two differ in every field name — the model is a Go struct with no json
// tags — and the schema this endpoint publishes is the DTO's. A handler that
// wrote the model would produce a document-shaped lie that no gate catches,
// because the description is derived from the TYPE named in describe.go rather
// than from what the handler passes to the encoder.
func TestTheAdminLineAnswersInTheSameShapeTheStorefrontDoes(t *testing.T) {
	pricing := &channelRecordingPricing{lineID: "item_1"}
	h := newAdminWriteServer(t, &channelRecordingOpening{}, pricing)

	rec := doRequestAs(t, h, &adminWriter, http.MethodPost,
		"/admin/v1/carts/cart_1/line-items",
		`{"sales_channel_id":"sc_phone","variant_id":"var_1","quantity":1}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	item := object(t, bodyMap(t, rec)["data"])
	assert.Equal(t, "item_1", item["id"], "the field names are the DTO's")
	assert.Equal(t, "A shirt", item["title"])
}

// TestAnAdminWriteTakesNoPrice verifies that neither body has a place to put an
// amount.
//
// The endpoint exists so an operator can build a cart the SERVER prices; a field
// that carried a price would make that promise false on the first request that
// used it. Unknown fields are refused by the decoder, so the assertion is that
// the name is not part of the contract rather than that the handler ignores it —
// a handler that ignored it would still let a client believe it worked.
func TestAnAdminWriteTakesNoPrice(t *testing.T) {
	for name, tc := range map[string]struct{ path, body string }{
		"the cart takes no total": {
			"/admin/v1/carts",
			`{"country_code":"tr","total":0}`,
		},
		"the line takes no unit price": {
			"/admin/v1/carts/cart_1/line-items",
			`{"sales_channel_id":"sc_phone","variant_id":"var_1","quantity":1,"unit_price":1}`,
		},
		"the line takes no title": {
			"/admin/v1/carts/cart_1/line-items",
			`{"sales_channel_id":"sc_phone","variant_id":"var_1","quantity":1,"title":"free shirt"}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			opening := &channelRecordingOpening{cartID: "cart_1"}
			pricing := &channelRecordingPricing{lineID: "item_1"}
			h := newAdminWriteServer(t, opening, pricing)

			rec := doRequestAs(t, h, &adminWriter, http.MethodPost, tc.path, tc.body)

			assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
			assert.Zero(t, opening.calls+pricing.calls)
		})
	}
}

// TestTheAdminWritesAskForTheWriteScope verifies that reading a cart is not
// enough to build one.
//
// cart:read was the only scope the surface ever asked for, so every list handed
// out before ADR 0146 carries it; if the writes accepted it, opening this surface
// would have silently granted a write to everybody who could already read.
func TestTheAdminWritesAskForTheWriteScope(t *testing.T) {
	reader := corehttp.Principal{ID: "user_reader", Kind: "user", Scopes: []string{"cart:read"}}

	for name, tc := range map[string]struct{ path, body string }{
		"opening a cart": {
			"/admin/v1/carts", `{"country_code":"tr"}`,
		},
		"adding a line": {
			"/admin/v1/carts/cart_1/line-items",
			`{"sales_channel_id":"sc_phone","variant_id":"var_1","quantity":1}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			opening := &channelRecordingOpening{cartID: "cart_1"}
			pricing := &channelRecordingPricing{lineID: "item_1"}
			h := newAdminWriteServer(t, opening, pricing)

			rec := doRequestAs(t, h, &reader, http.MethodPost, tc.path, tc.body)

			assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
			assert.Zero(t, opening.calls+pricing.calls)
		})
	}
}
