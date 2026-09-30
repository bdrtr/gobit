//go:build integration

package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errorreport"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/modules/order"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
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
