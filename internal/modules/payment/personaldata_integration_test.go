//go:build integration

package payment_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/payment/balancetender"
	"github.com/bdrtr/gobit/internal/modules/payment/giftcard"
	"github.com/bdrtr/gobit/internal/modules/payment/loyaltypoints"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/repository"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
	"github.com/bdrtr/gobit/internal/modules/payment/storecredit"
)

// TestTheAnswersAboutACustomerReadTheRealRows is ADR 0277 over the real
// schema: a customer who holds credit, paid part of an order with it and the
// rest with a gift card, was given money back and earned points, and whose
// second collection went through the manual provider, is found in every table
// the declaration names and nowhere another customer is.
func TestTheAnswersAboutACustomerReadTheRealRows(t *testing.T) {
	ctx := context.Background()
	repo := repository.New(testPool.Pool())
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(manual.New(repo, nil)))
	require.NoError(t, registry.Register(giftcard.New(repo, nil)))
	require.NoError(t, registry.Register(storecredit.New(repo, nil)))
	require.NoError(t, registry.Register(loyaltypoints.New(repo, nil)))
	svc, err := service.New(service.Options{
		Store: repo, Providers: registry, Events: eventbus.NewInMemory(nil),
		LoyaltyEarnBasisPoints: service.MaxLoyaltyEarnBasisPoints,
	})
	require.NoError(t, err)

	stamp := time.Now().UnixNano()
	ada, bob := fmt.Sprintf("cus_pd_ada_%d", stamp), fmt.Sprintf("cus_pd_bob_%d", stamp)

	_, err = svc.IssueCredit(ctx, service.IssueCreditInput{
		CustomerID: ada, CurrencyCode: testCurrency, Amount: 3_000, Reason: "a late parcel for Ada",
		Reference: "ticket 42",
	})
	require.NoError(t, err)
	_, err = svc.IssueCredit(ctx, service.IssueCreditInput{
		CustomerID: bob, CurrencyCode: testCurrency, Amount: 1_000, Reason: "Bob's own credit",
	})
	require.NoError(t, err)

	// The first collection: credit and a gift card, captured, part refunded.
	first, err := svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
		Reference: "cart_pd_" + ada, Amount: 5_000, CurrencyCode: testCurrency, CustomerID: ada,
		Metadata: map[string]any{"gift_note": "for Ada's mother"},
	})
	require.NoError(t, err)
	credit, err := svc.CreateSession(ctx, first.ID, storecredit.ID, service.CreateSessionInput{
		IdempotencyKey: "pd-credit-" + first.ID,
		Data:           map[string]any{balancetender.DataPartial: true},
	})
	require.NoError(t, err)
	_, err = svc.AuthorizePayment(ctx, credit.ID)
	require.NoError(t, err)
	captured, err := svc.CapturePayment(ctx, credit.ID, 0)
	require.NoError(t, err)
	_, err = svc.RefundPayment(ctx, captured.ID, 500, "Ada sent the scarf back")
	require.NoError(t, err)

	card, err := svc.IssueGiftCard(ctx, service.IssueGiftCardInput{
		CurrencyCode: testCurrency, Amount: 2_000, Reason: "a card Ada was given",
	})
	require.NoError(t, err)
	_, err = svc.CreateSession(ctx, first.ID, giftcard.ID, service.CreateSessionInput{
		IdempotencyKey: "pd-card-" + first.ID,
		Data:           map[string]any{giftcard.DataCode: card.Code},
	})
	require.NoError(t, err)

	// The second collection: the manual provider, with the caller's data.
	second, err := svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
		Reference: "cart_pd2_" + ada, Amount: 700, CurrencyCode: testCurrency, CustomerID: ada,
	})
	require.NoError(t, err)
	_, err = svc.CreateSession(ctx, second.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: "pd-manual-" + second.ID,
	})
	require.NoError(t, err)

	// The third collection spends the points the first one earned.
	third, err := svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
		Reference: "cart_pd3_" + ada, Amount: 100, CurrencyCode: testCurrency, CustomerID: ada,
	})
	require.NoError(t, err)
	points, err := svc.CreateSession(ctx, third.ID, loyaltypoints.ID, service.CreateSessionInput{
		IdempotencyKey: "pd-points-" + third.ID,
		Data:           map[string]any{balancetender.DataPartial: true},
	})
	require.NoError(t, err)
	_, err = svc.AuthorizePayment(ctx, points.ID)
	require.NoError(t, err)

	// Bob's collection, paid and so earning him points, which no answer about
	// Ada may reach.
	bobs, err := svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
		Reference: "cart_pd_" + bob, Amount: 900, CurrencyCode: testCurrency, CustomerID: bob,
	})
	require.NoError(t, err)
	bobSession, err := svc.CreateSession(ctx, bobs.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: "pd-bob-" + bobs.ID,
	})
	require.NoError(t, err)
	_, err = svc.AuthorizePayment(ctx, bobSession.ID)
	require.NoError(t, err)
	_, err = svc.CapturePayment(ctx, bobSession.ID, 0)
	require.NoError(t, err)
	var bobsPoints int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM payment_loyalty_entries WHERE customer_id = $1`, bob).Scan(&bobsPoints))
	require.Positive(t, bobsPoints, "the fixture needs another customer's points to tell a narrowed read apart")

	answers := service.NewPersonalData(repo, nil)
	disclosure, err := answers.Disclose(ctx, personaldata.Subject{CustomerID: ada})
	require.NoError(t, err)
	require.Equal(t, personaldata.Disclosed, disclosure.State)

	seen := map[string][]personaldata.Record{}
	for _, record := range disclosure.Records {
		seen[record.Table] = append(seen[record.Table], record)
		for _, field := range record.Fields {
			if field.Column == "customer_id" {
				assert.Equal(t, ada, field.Value, "%s %s names another customer", record.Table, record.ID)
			}
		}
	}
	for _, table := range []string{
		"payment_collections", "payment_sessions", "refunds", "payment_manual_sessions",
		"payment_gift_card_sessions", "payment_store_credit_entries", "payment_store_credit_sessions",
		"payment_loyalty_entries", "payment_loyalty_sessions",
	} {
		assert.NotEmpty(t, seen[table], "Ada has rows in %s and the dossier does not mention them", table)
	}
	assert.Len(t, seen["payment_collections"], 3)
	for _, table := range []string{"payment_sessions", "refunds", "payment_manual_sessions", "payment_gift_card_sessions"} {
		for _, record := range seen[table] {
			assert.True(t, strings.HasPrefix(record.ID, first.ID+"/") || strings.HasPrefix(record.ID, second.ID+"/") ||
				strings.HasPrefix(record.ID, third.ID+"/"),
				"%s record %q does not name its collection", table, record.ID)
		}
	}
	require.Len(t, seen["refunds"], 1)
	assert.Equal(t, "Ada sent the scarf back", seen["refunds"][0].Fields[0].Value)

	var reasons []any
	for _, record := range seen["payment_store_credit_entries"] {
		for _, field := range record.Fields {
			if field.Column == "reason" && field.Value != nil {
				reasons = append(reasons, field.Value)
			}
		}
	}
	assert.Contains(t, reasons, "a late parcel for Ada")
	assert.NotContains(t, reasons, "Bob's own credit")

	result, err := answers.Erase(ctx, personaldata.Subject{CustomerID: ada})
	require.NoError(t, err)
	assert.Equal(t, personaldata.Retained, result.Outcome)
	assert.Positive(t, result.Rows)
	assert.Equal(t, personaldata.Declaration{Holdings: service.PersonalDataHoldings()}.Paths(), result.Kept)

	// The snapshot the answers read in takes no write: a statement inside it
	// is refused rather than committed beside a read.
	err = repo.WithReadTx(ctx, func(ctx context.Context) error {
		_, writeErr := repo.AppendStoreCreditEntry(ctx, models.StoreCreditEntry{
			ID: fmt.Sprintf("scredit_pd_%d", stamp), CustomerID: ada, CurrencyCode: testCurrency,
			Amount: 1, Kind: models.StoreCreditIssue, Reason: "must not land",
		})

		return writeErr
	})
	require.Error(t, err, "a write inside the answers' snapshot was accepted")
	assert.Contains(t, err.Error(), "read-only")

	var remaining int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM payment_store_credit_entries WHERE customer_id = $1`, ada).Scan(&remaining))
	assert.Positive(t, remaining, "an erasure answered as retained rewrites nothing")
}
