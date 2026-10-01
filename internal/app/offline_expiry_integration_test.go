//go:build integration

package app

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errorreport"
	"github.com/bdrtr/gobit/core/jobreport"
	"github.com/bdrtr/gobit/core/link"
	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/jobs/offlineexpiry"
	"github.com/bdrtr/gobit/internal/modules/order"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
	"github.com/bdrtr/gobit/internal/modules/payment"
	paymentsvc "github.com/bdrtr/gobit/internal/modules/payment/service"
)

// TestAnOverdueTransferCancelsItsOrder is ADR 0289 on the production wiring:
// PAYMENT_OFFLINE_WAIT_DAYS gives the bank transfer three days, and the job
// registerJobs registered cancels the order whose transfer was opened four
// days ago. The order whose transfer is two days old waits on, and the cash on
// delivery order, given no wait, is never read.
func TestAnOverdueTransferCancelsItsOrder(t *testing.T) {
	ctx := context.Background()
	dsn := migrateDSN(t)
	jobsEnv(t, dsn)
	t.Setenv("PAYMENT_OFFLINE_METHODS", "bank_transfer,cash_on_delivery")
	t.Setenv("PAYMENT_OFFLINE_WAIT_DAYS", "bank_transfer:3")
	cfg, err := config.Load()
	require.NoError(t, err)
	log := slog.New(slog.DiscardHandler)

	app, closeApp, err := openApplication(ctx, cfg, log, errorreport.NewSink(), Options{}, publishesOnly)
	require.NoError(t, err)
	defer closeApp()

	orders, err := container.Resolve[*ordersvc.Service](app.container, order.ServiceName)
	require.NoError(t, err)
	payments, err := container.Resolve[*paymentsvc.Service](app.container, payment.ServiceName)
	require.NoError(t, err)
	links, err := container.Resolve[link.LinkService](app.container, svcLink)
	require.NoError(t, err)
	pool, err := container.Resolve[*db.Pool](app.container, svcDB)
	require.NoError(t, err)

	// placed places an order paid by the method, its session opened the given
	// days ago, as a checkout that long ago leaves it.
	placed := func(method string, daysAgo int) string {
		t.Helper()

		created, err := orders.CreateOrder(ctx, ordersvc.CreateOrderInput{
			RegionID: "reg_offline", CustomerID: "cus_offline", CurrencyCode: "TRY",
			Subtotal: 5_000, Total: 5_000,
			Items: []ordersvc.CreateOrderItemInput{{
				VariantID: "variant_offline", Title: "Offline item", Quantity: 1,
				UnitPrice: 5_000, Subtotal: 5_000, Total: 5_000,
			}},
		})
		require.NoError(t, err)
		collection, err := payments.CreatePaymentCollection(ctx, paymentsvc.CreateCollectionInput{
			Reference: created.ID, Amount: 5_000, CurrencyCode: "TRY",
		})
		require.NoError(t, err)
		session, err := payments.CreateSession(ctx, collection.ID, method, paymentsvc.CreateSessionInput{
			Amount: 5_000, IdempotencyKey: collection.ID,
		})
		require.NoError(t, err)
		_, err = payments.AuthorizePayment(ctx, session.ID)
		require.NoError(t, err)
		require.NoError(t, links.Create(ctx, "order_payment", created.ID, collection.ID))
		_, err = pool.Pool().Exec(ctx, `UPDATE payment_sessions SET created_at = $2 WHERE id = $1`,
			session.ID, time.Now().UTC().AddDate(0, 0, -daysAgo))
		require.NoError(t, err)

		return created.ID
	}
	overdue := placed("bank_transfer", 4)
	waiting := placed("bank_transfer", 2)
	atTheDoor := placed("cash_on_delivery", 30)

	registry, err := registerJobs(app.container, app.host, log)
	require.NoError(t, err)
	expiry, err := registry.Get(offlineexpiry.Name)
	require.NoError(t, err)
	passCtx := jobreport.WithReporter(ctx)
	require.NoError(t, expiry.Run(passCtx))

	assert.Equal(t, "1 orders whose offline payment did not arrive are canceled; 0 could not be",
		jobreport.Detail(passCtx))
	status := func(orderID string) models.OrderDetail {
		t.Helper()

		read, err := orders.GetOrder(ctx, orderID)
		require.NoError(t, err)
		return read
	}
	canceled := status(overdue)
	assert.Equal(t, models.OrderCanceled, canceled.Status)
	assert.Equal(t, offlineexpiry.Reason, canceled.CancelReason)
	assert.Equal(t, models.OrderPending, status(waiting).Status, "inside its wait")
	assert.Equal(t, models.OrderPending, status(atTheDoor).Status, "cash on delivery is given no wait")
}
