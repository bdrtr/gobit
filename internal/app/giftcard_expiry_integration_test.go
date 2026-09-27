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
	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/jobs/giftcardexpiry"
	"github.com/bdrtr/gobit/internal/modules/payment"
	paymentsvc "github.com/bdrtr/gobit/internal/modules/payment/service"
)

// TestAnInstallationsValidityExpiresItsCards is ADR 0214 on the production
// wiring: PAYMENT_GIFT_CARD_VALIDITY_DAYS reaches the payment service through
// the composition root, a sold card is made with it, and the expiry job
// registerJobs registered closes a card whose moment has come.
func TestAnInstallationsValidityExpiresItsCards(t *testing.T) {
	ctx := context.Background()
	dsn := migrateDSN(t)
	jobsEnv(t, dsn)
	t.Setenv("PAYMENT_GIFT_CARD_VALIDITY_DAYS", "30")
	cfg, err := config.Load()
	require.NoError(t, err)
	log := slog.New(slog.DiscardHandler)

	app, closeApp, err := openApplication(ctx, cfg, log, errorreport.NewSink(), Options{}, publishesOnly)
	require.NoError(t, err)
	defer closeApp()

	payments, err := container.Resolve[*paymentsvc.Service](app.container, payment.ServiceName)
	require.NoError(t, err)
	pool, err := container.Resolve[*db.Pool](app.container, svcDB)
	require.NoError(t, err)

	sold, _, err := payments.IssueSoldGiftCard(ctx, paymentsvc.SoldGiftCardInput{
		Reference: "oline_expiry:1", OrderID: "order_expiry", CurrencyCode: "TRY", Amount: 2_500,
	})
	require.NoError(t, err)
	require.NotNil(t, sold.Card.ExpiresAt, "the installation's validity reached the card")
	assert.Equal(t, sold.Card.CreatedAt.AddDate(0, 0, 30), *sold.Card.ExpiresAt)

	// The card's moment is moved behind, as a month passing would leave it.
	_, err = pool.Pool().Exec(ctx,
		`UPDATE payment_gift_cards SET expires_at = created_at + interval '1 microsecond' WHERE id = $1`,
		sold.Card.ID)
	require.NoError(t, err)

	registry, err := registerJobs(app.container, app.host, log)
	require.NoError(t, err)
	expiry, err := registry.Get(giftcardexpiry.Name)
	require.NoError(t, err)
	passCtx := jobreport.WithReporter(ctx)
	require.NoError(t, expiry.Run(passCtx))

	assert.Equal(t, "closed 1 expired gift cards; 0 wait for a payment that holds them", jobreport.Detail(passCtx))
	card, err := payments.GetGiftCard(ctx, sold.Card.ID)
	require.NoError(t, err)
	require.NotNil(t, card.Card.DisabledAt)
	assert.Equal(t, paymentsvc.ReasonExpired, card.Card.DisableReason)
	assert.Zero(t, card.Balance, "what the card held was voided")

	againCtx := jobreport.WithReporter(ctx)
	require.NoError(t, expiry.Run(againCtx))
	assert.Equal(t, "closed 0 expired gift cards; 0 wait for a payment that holds them", jobreport.Detail(againCtx),
		"the closed card is not read again")
}
