//go:build integration

package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errorreport"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/link"
	"github.com/bdrtr/gobit/internal/adminui"
	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/modules/order"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
	"github.com/bdrtr/gobit/internal/modules/payment"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	paymentsvc "github.com/bdrtr/gobit/internal/modules/payment/service"
)

// TestThePanelShowsWhereARealOrderGoes is ADR 0196's gate: the panel built from
// a real installation's container reads an order's addresses, its correction
// and its additions through the real read layer.
//
// The panel's own tests fake the read layer, which accepts any field name; the
// provider refuses one it does not offer. So only an assembly can show that the
// names the page asks for are the names the order module fills.
func TestThePanelShowsWhereARealOrderGoes(t *testing.T) {
	ctx := context.Background()

	dsn := migrateDSN(t)
	t.Setenv("APP_ENV", "development")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("JWT_SECRET", "panel-order-test-secret-32-bytes-long!")
	t.Setenv("LOG_LEVEL", "warn")
	cfg, err := config.Load()
	require.NoError(t, err)

	app, closeApp, err := openApplication(ctx, cfg, slog.New(slog.DiscardHandler), errorreport.NewSink(),
		Options{}, publishesOnly)
	require.NoError(t, err)
	defer closeApp()

	svc, err := container.Resolve[*ordersvc.Service](app.container, order.ServiceName)
	require.NoError(t, err)

	place := func(addsTo string, addresses []models.OrderAddress) models.Order {
		t.Helper()

		placed, err := svc.CreateOrder(ctx, ordersvc.CreateOrderInput{
			RegionID: "reg_panel", CustomerID: "cus_panel", CurrencyCode: "TRY",
			AddsToOrderID: addsTo,
			Subtotal:      1000, TaxTotal: 200, Total: 1200,
			Items: []ordersvc.CreateOrderItemInput{{
				VariantID: "variant_panel", Title: "Panel item", Quantity: 1,
				UnitPrice: 1000, Subtotal: 1000, TaxTotal: 200, Total: 1200,
			}},
			Addresses: addresses,
		})
		require.NoError(t, err)
		return placed
	}
	parent := place("", []models.OrderAddress{
		{Type: models.AddressShipping, FirstName: "Ada", Address1: "12 Wrong St", CountryCode: "TR"},
		{Type: models.AddressBilling, Company: "Engines Ltd", CountryCode: "TR"},
	})
	addition := place(parent.ID, nil)
	_, err = svc.CorrectShippingAddress(ctx, parent.ID, models.OrderAddress{
		FirstName: "Ada", Address1: "12 Right St",
	})
	require.NoError(t, err)

	panel, err := adminui.FromContainer(app.container, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	page := func(orderID string) string {
		t.Helper()

		req := httptest.NewRequest(http.MethodGet, adminui.OrdersPath+"/"+orderID, http.NoBody)
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_panel", Kind: "user", Scopes: []string{corehttp.ScopeAdmin},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		return rec.Body.String()
	}

	parentPage := page(parent.ID)
	assert.Contains(t, parentPage, "12 Right St", "the page reads the CURRENT address")
	assert.NotContains(t, parentPage, "12 Wrong St")
	assert.Contains(t, parentPage, "Engines Ltd")
	assert.Contains(t, parentPage, "corrected ")
	assert.Contains(t, parentPage, fmt.Sprintf(`/%s">#%d</a>`, addition.ID, addition.DisplayID),
		"the parent lists its addition")

	additionPage := page(addition.ID)
	assert.Contains(t, additionPage, fmt.Sprintf(`Adds to <a href="%s/%s">#%d</a>`,
		adminui.OrdersPath, parent.ID, parent.DisplayID))
	assert.Contains(t, additionPage, "no shipping address")
}

// TestThePanelShowsARealOrdersAfterSales is ADR 0270's gate: the panel built
// from a real installation reads an order's return, claim, replacement and
// exchange through the order module's four entities, every field the page asks
// for being one the provider fills.
func TestThePanelShowsARealOrdersAfterSales(t *testing.T) {
	ctx := context.Background()

	dsn := migrateDSN(t)
	t.Setenv("APP_ENV", "development")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("JWT_SECRET", "panel-order-test-secret-32-bytes-long!")
	t.Setenv("LOG_LEVEL", "warn")
	cfg, err := config.Load()
	require.NoError(t, err)

	app, closeApp, err := openApplication(ctx, cfg, slog.New(slog.DiscardHandler), errorreport.NewSink(),
		Options{}, publishesOnly)
	require.NoError(t, err)
	defer closeApp()

	svc, err := container.Resolve[*ordersvc.Service](app.container, order.ServiceName)
	require.NoError(t, err)
	placed, err := svc.CreateOrder(ctx, ordersvc.CreateOrderInput{
		RegionID: "reg_panel", CustomerID: "cus_panel", CurrencyCode: "TRY",
		Subtotal: 2000, TaxTotal: 400, Total: 2400,
		Items: []ordersvc.CreateOrderItemInput{{
			VariantID: "variant_panel", Title: "Panel item", Quantity: 2,
			UnitPrice: 1000, Subtotal: 2000, TaxTotal: 400, Total: 2400,
		}},
	})
	require.NoError(t, err)
	detail, err := svc.GetOrder(ctx, placed.ID)
	require.NoError(t, err)
	lineID := detail.Items[0].ID

	ret, err := svc.CreateReturn(ctx, ordersvc.CreateReturnInput{
		OrderID: placed.ID, RefundAmount: 1200, Reason: "scratched",
		Lines: []ordersvc.ReturnLineInput{{OrderLineItemID: lineID, Quantity: 1, RefundAmount: 1200}},
	})
	require.NoError(t, err)
	_, err = svc.ReceiveReturn(ctx, ret.ID, "sloc_back")
	require.NoError(t, err)
	claim, err := svc.CreateClaim(ctx, ordersvc.CreateClaimInput{OrderID: placed.ID, Type: models.ClaimReplace})
	require.NoError(t, err)
	_, err = svc.CreateReplacement(ctx, ordersvc.CreateReplacementInput{
		ClaimID: claim.ID, ShippingOptionID: "so_panel", LocationID: "sloc_ship",
		Lines: []ordersvc.ReplacementLineInput{{OrderLineItemID: lineID, Quantity: 1}},
	})
	require.NoError(t, err)
	_, err = svc.CreateExchange(ctx, ordersvc.CreateExchangeInput{OrderID: placed.ID, DifferenceDue: 300})
	require.NoError(t, err)

	panel, err := adminui.FromContainer(app.container, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	req := httptest.NewRequest(http.MethodGet, adminui.OrdersPath+"/"+placed.ID, http.NoBody)
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
		ID: "usr_panel", Kind: "user", Scopes: []string{"order:read"},
	}))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()

	assert.NotContains(t, body, "could not be read")
	for _, want := range []string{
		ret.ID, "Panel item × 1", "scratched", "received", "· sloc_back",
		claim.ID, "settled by replace",
		"for claim " + claim.ID, "from sloc_ship",
		"due from the customer",
	} {
		assert.Contains(t, body, want)
	}
}

// TestThePanelActsOnARealOrdersAfterSales is ADR 0271's gate: the panel built
// from a real installation receives a return through the returns flow the API
// uses and withdraws another through the service, and the records say so.
func TestThePanelActsOnARealOrdersAfterSales(t *testing.T) {
	ctx := context.Background()

	dsn := migrateDSN(t)
	t.Setenv("APP_ENV", "development")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("JWT_SECRET", "panel-order-test-secret-32-bytes-long!")
	t.Setenv("LOG_LEVEL", "warn")
	cfg, err := config.Load()
	require.NoError(t, err)

	app, closeApp, err := openApplication(ctx, cfg, slog.New(slog.DiscardHandler), errorreport.NewSink(),
		Options{}, publishesOnly)
	require.NoError(t, err)
	defer closeApp()

	svc, err := container.Resolve[*ordersvc.Service](app.container, order.ServiceName)
	require.NoError(t, err)
	placed, err := svc.CreateOrder(ctx, ordersvc.CreateOrderInput{
		RegionID: "reg_panel", CustomerID: "cus_panel", CurrencyCode: "TRY",
		Subtotal: 2000, TaxTotal: 400, Total: 2400,
		Items: []ordersvc.CreateOrderItemInput{{
			VariantID: "variant_panel", Title: "Panel item", Quantity: 2,
			UnitPrice: 1000, Subtotal: 2000, TaxTotal: 400, Total: 2400,
		}},
	})
	require.NoError(t, err)
	detail, err := svc.GetOrder(ctx, placed.ID)
	require.NoError(t, err)
	lineID := detail.Items[0].ID
	received, err := svc.CreateReturn(ctx, ordersvc.CreateReturnInput{
		OrderID: placed.ID, Lines: []ordersvc.ReturnLineInput{{OrderLineItemID: lineID, Quantity: 1}},
	})
	require.NoError(t, err)
	withdrawn, err := svc.CreateReturn(ctx, ordersvc.CreateReturnInput{OrderID: placed.ID})
	require.NoError(t, err)

	panel, err := adminui.FromContainer(app.container, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	post := func(kind, record, act string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(http.MethodPost,
			adminui.OrdersPath+"/"+placed.ID+"/after-sales/"+kind+"/"+record+"/"+act,
			strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_panel", Kind: "user", Scopes: []string{"order:read", "order:write"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	page := post("return", received.ID, "receive", url.Values{"location_id": {"sloc_panel"}})
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	assert.Contains(t, page.Body.String(), "The return was received")
	back, err := svc.GetReturn(ctx, received.ID)
	require.NoError(t, err)
	assert.Equal(t, models.ReturnReceived, back.Status)
	assert.Equal(t, "sloc_panel", back.ReceivedLocationID)

	page = post("return", withdrawn.ID, "cancel", nil)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	assert.Contains(t, page.Body.String(), "The return was withdrawn.")
	gone, err := svc.GetReturn(ctx, withdrawn.ID)
	require.NoError(t, err)
	assert.Equal(t, models.ReturnCanceled, gone.Status)

	page = post("return", received.ID, "cancel", nil)
	assert.Equal(t, http.StatusUnprocessableEntity, page.Code, "a received return is not withdrawn")
	assert.Contains(t, page.Body.String(), `<p role="alert">`)
	still, err := svc.GetReturn(ctx, received.ID)
	require.NoError(t, err)
	assert.Equal(t, models.ReturnReceived, still.Status)
}

// TestThePanelOpensARealOrdersAfterSales is ADR 0272's gate: the real
// assembly opens a return naming the line and units that come back, a claim
// settled by goods, and the replacement that settles it.
func TestThePanelOpensARealOrdersAfterSales(t *testing.T) {
	ctx := context.Background()

	dsn := migrateDSN(t)
	t.Setenv("APP_ENV", "development")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("JWT_SECRET", "panel-order-test-secret-32-bytes-long!")
	t.Setenv("LOG_LEVEL", "warn")
	cfg, err := config.Load()
	require.NoError(t, err)

	app, closeApp, err := openApplication(ctx, cfg, slog.New(slog.DiscardHandler), errorreport.NewSink(),
		Options{}, publishesOnly)
	require.NoError(t, err)
	defer closeApp()

	svc, err := container.Resolve[*ordersvc.Service](app.container, order.ServiceName)
	require.NoError(t, err)
	placed, err := svc.CreateOrder(ctx, ordersvc.CreateOrderInput{
		RegionID: "reg_panel", CustomerID: "cus_panel", CurrencyCode: "TRY",
		Subtotal: 2000, TaxTotal: 400, Total: 2400,
		Items: []ordersvc.CreateOrderItemInput{{
			VariantID: "variant_panel", Title: "Panel item", Quantity: 2,
			UnitPrice: 1000, Subtotal: 2000, TaxTotal: 400, Total: 2400,
		}},
	})
	require.NoError(t, err)
	detail, err := svc.GetOrder(ctx, placed.ID)
	require.NoError(t, err)
	lineID := detail.Items[0].ID

	panel, err := adminui.FromContainer(app.container, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	open := func(kind string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(http.MethodPost, adminui.OrdersPath+"/"+placed.ID+"/after-sales/"+kind,
			strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_panel", Kind: "user", Scopes: []string{"order:read", "order:write"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		return rec
	}

	page := open("return", url.Values{"line_id": {lineID}, "quantity": {"1"}, "reason": {"too big"}})
	returns, _, err := svc.ListReturns(ctx, placed.ID, ordersvc.Page{Limit: 10})
	require.NoError(t, err)
	require.Len(t, returns, 1)
	assert.Contains(t, page.Body.String(), "The return "+returns[0].ID+" was opened.")
	assert.Contains(t, page.Body.String(), "Panel item × 1",
		"the page read back through the return's entity names the line that comes back")

	open("claim", url.Values{"type": {"replace"}, "reason": {"crushed"}})
	claims, _, err := svc.ListClaims(ctx, placed.ID, ordersvc.Page{Limit: 10})
	require.NoError(t, err)
	require.Len(t, claims, 1)
	assert.Equal(t, models.ClaimReplace, claims[0].Type)

	page = open("replacement", url.Values{
		"source": {"claim:" + claims[0].ID}, "line_id": {lineID}, "quantity": {"1"},
		"shipping_option_id": {"so_panel"}, "location_id": {"sloc_panel"},
	})
	assert.Contains(t, page.Body.String(), "The replacement orepl_")
	sent, err := svc.ListReplacementsOfClaim(ctx, claims[0].ID)
	require.NoError(t, err)
	require.Len(t, sent, 1)
	assert.Equal(t, "sloc_panel", sent[0].LocationID)
}

// TestThePanelRecordsARealOfflinePayment is ADR 0287's gate: on a real
// installation naming a bank transfer, the order page reads what the payment
// module awaits through the real read layer, and the form records it through
// the module's own surface.
//
// The collection holds a card's session beside the transfer's, both
// authorized: the page awaits only the transfer, and recording the card's is
// refused — its money moves through the provider.
func TestThePanelRecordsARealOfflinePayment(t *testing.T) {
	ctx := context.Background()

	dsn := migrateDSN(t)
	t.Setenv("APP_ENV", "development")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("JWT_SECRET", "panel-order-test-secret-32-bytes-long!")
	t.Setenv("LOG_LEVEL", "warn")
	t.Setenv("PAYMENT_OFFLINE_METHODS", "bank_transfer")
	cfg, err := config.Load()
	require.NoError(t, err)

	app, closeApp, err := openApplication(ctx, cfg, slog.New(slog.DiscardHandler), errorreport.NewSink(),
		Options{}, publishesOnly)
	require.NoError(t, err)
	defer closeApp()

	orders, err := container.Resolve[*ordersvc.Service](app.container, order.ServiceName)
	require.NoError(t, err)
	payments, err := container.Resolve[*paymentsvc.Service](app.container, payment.ServiceName)
	require.NoError(t, err)
	links, err := container.Resolve[link.LinkService](app.container, svcLink)
	require.NoError(t, err)

	placed, err := orders.CreateOrder(ctx, ordersvc.CreateOrderInput{
		RegionID: "reg_panel", CustomerID: "cus_panel", CurrencyCode: "TRY",
		Subtotal: 10_000, Total: 10_000,
		Items: []ordersvc.CreateOrderItemInput{{
			VariantID: "variant_panel", Title: "Panel item", Quantity: 1,
			UnitPrice: 10_000, Subtotal: 10_000, Total: 10_000,
		}},
	})
	require.NoError(t, err)
	collection, err := payments.CreatePaymentCollection(ctx, paymentsvc.CreateCollectionInput{
		Reference: placed.ID, Amount: 10_000, CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	authorized := func(providerID string, amount int64, data map[string]any) string {
		t.Helper()

		session, err := payments.CreateSession(ctx, collection.ID, providerID, paymentsvc.CreateSessionInput{
			Amount: amount, IdempotencyKey: collection.ID + providerID, Data: data,
		})
		require.NoError(t, err)
		_, err = payments.AuthorizePayment(ctx, session.ID)
		require.NoError(t, err)
		return session.ID
	}
	cardID := authorized(manual.ID, 4_000, map[string]any{manual.DataKeyOutcome: manual.OutcomeAuthorize})
	transferID := authorized("bank_transfer", 6_000, nil)
	require.NoError(t, links.Create(ctx, "order_payment", placed.ID, collection.ID))

	panel, err := adminui.FromContainer(app.container, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	operator := corehttp.Principal{
		ID: "usr_panel", Kind: "user", Scopes: []string{"order:read", "payment:read", "payment:write"},
	}
	serve := func(req *http.Request) *httptest.ResponseRecorder {
		t.Helper()

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req.WithContext(corehttp.WithPrincipal(req.Context(), operator)))
		return rec
	}
	record := func(sessionID string) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(http.MethodPost,
			adminui.OrdersPath+"/"+placed.ID+"/payment/"+sessionID+"/received", http.NoBody)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return serve(req)
	}

	page := serve(httptest.NewRequest(http.MethodGet, adminui.OrdersPath+"/"+placed.ID, http.NoBody))
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	// The installation registers no currency, so amounts print in minor units.
	assert.Contains(t, page.Body.String(), `Awaiting <span class="num">6000 TRY</span> by bank_transfer.`)
	assert.Contains(t, page.Body.String(), "/payment/"+transferID+"/received")
	assert.NotContains(t, page.Body.String(), "/payment/"+cardID+"/received",
		"the card's money is not awaited")

	refused := record(cardID)
	assert.Equal(t, http.StatusUnprocessableEntity, refused.Code, refused.Body.String())
	assert.Contains(t, refused.Body.String(), `<p role="alert">`)
	untouched, err := payments.GetPaymentCollection(ctx, collection.ID)
	require.NoError(t, err)
	assert.Zero(t, untouched.CapturedAmount, "an operator's word is not a capture of the card's money")

	done := record(transferID)
	require.Equal(t, http.StatusOK, done.Code, done.Body.String())
	assert.Contains(t, done.Body.String(), "The payment of 6000 TRY (minor units) was recorded as received.")
	assert.NotContains(t, done.Body.String(), "Awaiting", "nothing is awaited any more")
	paid, err := payments.GetPaymentCollection(ctx, collection.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(6_000), paid.CapturedAmount, "the transfer is captured whole")
}
