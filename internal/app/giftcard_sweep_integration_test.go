//go:build integration

package app

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errorreport"
	"github.com/bdrtr/gobit/core/jobreport"
	"github.com/bdrtr/gobit/core/link"
	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/jobs/giftcardsweep"
	"github.com/bdrtr/gobit/internal/modules/order"
	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
	"github.com/bdrtr/gobit/internal/modules/payment"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	paymentsvc "github.com/bdrtr/gobit/internal/modules/payment/service"
)

// TestTheSweepIssuesACardNoCaptureDeliveryDid is ADR 0212 on the production
// wiring.
//
// The installation is opened in a command's role, which subscribes nothing
// (ADR 0160): an order's capture is written and published and reaches no
// handler, which is what a handler's failure or a stopped process leaves
// behind. The sweep is then run as registerJobs registered it, against the real
// read layer, links and payment module. It issues the paid order's two cards
// and mails their codes, and nothing for the mug beside them; it finds them on
// a second run, and issues nothing for a paid order that was canceled.
func TestTheSweepIssuesACardNoCaptureDeliveryDid(t *testing.T) {
	ctx := context.Background()
	dsn := migrateDSN(t)
	jobsEnv(t, dsn)
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

	// paidOrder places an order of two 2,500 cards and a 1,000 mug and pays it
	// the way the checkout does, and returns the order and its two lines.
	paidOrder := func() (orderID, cardLine, mugLine string) {
		t.Helper()

		placed, err := orders.CreateOrder(ctx, ordersvc.CreateOrderInput{
			RegionID: "reg_sweep", CustomerID: "cus_sweep", Email: "buyer@example.com", CurrencyCode: "TRY",
			Subtotal: 6_000, Total: 6_000,
			Items: []ordersvc.CreateOrderItemInput{
				{
					VariantID: "variant_card", Title: "Gift card", Quantity: 2,
					UnitPrice: 2_500, Subtotal: 5_000, Total: 5_000, IsGiftcard: true,
				},
				{VariantID: "variant_mug", Title: "Mug", Quantity: 1, UnitPrice: 1_000, Subtotal: 1_000, Total: 1_000},
			},
		})
		require.NoError(t, err)
		collection, err := payments.CreatePaymentCollection(ctx, paymentsvc.CreateCollectionInput{
			Reference: placed.ID, Amount: 6_000, CurrencyCode: "TRY",
		})
		require.NoError(t, err)
		session, err := payments.CreateSession(ctx, collection.ID, manual.ID, paymentsvc.CreateSessionInput{
			Amount: 6_000, IdempotencyKey: collection.ID,
			Data: map[string]any{manual.DataKeyOutcome: manual.OutcomeAuthorize},
		})
		require.NoError(t, err)
		_, err = payments.AuthorizePayment(ctx, session.ID)
		require.NoError(t, err)
		_, err = payments.CapturePayment(ctx, session.ID, 0)
		require.NoError(t, err)
		require.NoError(t, links.Create(ctx, "order_payment", placed.ID, collection.ID))

		detail, err := orders.GetOrder(ctx, placed.ID)
		require.NoError(t, err)
		require.Len(t, detail.Items, 2)
		for _, item := range detail.Items {
			if item.IsGiftcard {
				cardLine = item.ID
			} else {
				mugLine = item.ID
			}
		}

		return placed.ID, cardLine, mugLine
	}
	_, kept, mug := paidOrder()
	canceledID, canceledLine, _ := paidOrder()
	require.NoError(t, orders.CancelOrder(ctx, canceledID, "sweep test"))

	registry, err := registerJobs(app.container, app.host, log)
	require.NoError(t, err)
	sweep, err := registry.Get(giftcardsweep.Name)
	require.NoError(t, err)
	pass := func() string {
		t.Helper()

		ctx := jobreport.WithReporter(ctx)
		require.NoError(t, sweep.Run(ctx))

		return jobreport.Detail(ctx)
	}
	cards := func(lineID string) (made, mailed int) {
		t.Helper()

		require.NoError(t, pool.Pool().QueryRow(ctx,
			`SELECT count(*) FROM payment_gift_cards WHERE source_reference IN ($1, $2)`,
			lineID+":1", lineID+":2").Scan(&made))
		require.NoError(t, pool.Pool().QueryRow(ctx, `
            SELECT count(*) FROM notification_deliveries n
            JOIN payment_gift_cards g ON g.id = n.reference
            WHERE n.template = 'gift_card.issued' AND g.source_reference IN ($1, $2)`,
			lineID+":1", lineID+":2").Scan(&mailed))

		return made, mailed
	}

	made, _ := cards(kept)
	require.Zero(t, made, "the capture reached no handler, so nothing issued a card")

	assert.Equal(t, "issued 2 gift cards on 1 orders; 0 orders wait for their capture", pass())
	made, mailed := cards(kept)
	assert.Equal(t, []int{2, 2}, []int{made, mailed}, "two cards, each mailed once")
	made, _ = cards(canceledLine)
	assert.Zero(t, made, "a canceled order's cards are not issued")
	made, _ = cards(mug)
	assert.Zero(t, made, "the mug is not a gift card")

	assert.Equal(t, "issued 0 gift cards on 0 orders; 0 orders wait for their capture", pass())
	made, mailed = cards(kept)
	assert.Equal(t, []int{2, 2}, []int{made, mailed}, "the second run made and mailed nothing")
}
