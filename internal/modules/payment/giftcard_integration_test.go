//go:build integration

package payment_test

import (
	"context"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/payment"
	"github.com/bdrtr/gobit/internal/modules/payment/giftcard"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/repository"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// giftCardServiceOn builds a real-repository service with the gift-card
// provider, on a pool whose connections start at the given isolation level —
// empty being the server's own (the D119 shape the balance lock is proved in).
func giftCardServiceOn(t *testing.T, defaultIsolation string) (*service.Service, *repository.Repository) {
	t.Helper()

	pool := testPool.Pool()
	if defaultIsolation != "" {
		own, err := pgxpool.New(context.Background(),
			testDSN+"&default_transaction_isolation="+url.QueryEscape(defaultIsolation))
		require.NoError(t, err)
		t.Cleanup(own.Close)
		pool = own
	}

	repo := repository.New(pool)
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(manual.New(repo, nil)))
	require.NoError(t, registry.Register(giftcard.New(repo, nil)))
	svc, err := service.New(service.Options{Store: repo, Providers: registry, Events: eventbus.NewInMemory(nil)})
	require.NoError(t, err)

	return svc, repo
}

// issueCard issues a card for the amount and returns it with its code.
func issueCard(ctx context.Context, t *testing.T, svc *service.Service, amount int64) service.IssuedGiftCard {
	t.Helper()

	issued, err := svc.IssueGiftCard(ctx, service.IssueGiftCardInput{
		CurrencyCode: testCurrency, Amount: amount, Reason: "an integration test",
	})
	require.NoError(t, err)

	return issued
}

// cardSession opens a guest collection for the amount and a session on it that
// the card's code pays.
func cardSession(
	ctx context.Context, t *testing.T, svc *service.Service, code string, amount int64,
) models.PaymentSession {
	t.Helper()

	col, err := svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
		Reference: testReference + "-gift", Amount: amount, CurrencyCode: testCurrency,
	})
	require.NoError(t, err)
	ses, err := svc.CreateSession(ctx, col.ID, giftcard.ID, service.CreateSessionInput{
		IdempotencyKey: "gift-" + col.ID,
		Data:           map[string]any{giftcard.DataCode: code},
	})
	require.NoError(t, err)

	return ses
}

// cardBalance reads a card's balance through the service.
func cardBalance(ctx context.Context, t *testing.T, svc *service.Service, id string) int64 {
	t.Helper()

	card, err := svc.GetGiftCard(ctx, id)
	require.NoError(t, err)

	return card.Balance
}

// TestAGiftCardIsSpentOnTheRealSchema is ADR 0208 end to end in the module: a
// guest's collection paid by a code, captured and partly refunded, and the
// books the journal derives from it.
func TestAGiftCardIsSpentOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	from := time.Now().Add(-time.Minute)
	svc, _ := giftCardServiceOn(t, "")
	issued := issueCard(ctx, t, svc, 50_000)

	ses := cardSession(ctx, t, svc, issued.Code, 20_000)
	_, err := svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(30_000), cardBalance(ctx, t, svc, issued.Card.ID), "the hold is on the card")
	captured, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)
	refund, err := svc.RefundPayment(ctx, captured.ID, 5_000, "an integration test")
	require.NoError(t, err)

	assert.Equal(t, int64(35_000), cardBalance(ctx, t, svc, issued.Card.ID), "the refund went back onto the card")
	entries, total, err := svc.ListGiftCardEntries(ctx, issued.Card.ID, service.Page{})
	require.NoError(t, err)
	assert.Equal(t, int64(3), total, "the issue, the hold and the refund; a capture writes no row")
	kinds := map[models.GiftCardKind]int64{}
	for _, entry := range entries {
		kinds[entry.Kind] += entry.Amount
	}
	assert.Equal(t, map[models.GiftCardKind]int64{
		models.GiftCardIssue: 50_000, models.GiftCardHold: -20_000, models.GiftCardRefund: 5_000,
	}, kinds)

	journal, err := svc.Journal(ctx, service.JournalQuery{From: from, To: time.Now().Add(time.Minute)})
	require.NoError(t, err)
	lines := map[string][]models.JournalLine{}
	for _, entry := range journal.Entries {
		lines[entry.ID] = entry.Lines
	}
	var issueEntry string
	for _, entry := range entries {
		if entry.Kind == models.GiftCardIssue {
			issueEntry = entry.ID
		}
	}
	assert.Equal(t, []models.JournalLine{
		{Account: models.AccountGiftCardGranted, Debit: 50_000},
		{Account: models.AccountGiftCard, Credit: 50_000},
	}, lines[issueEntry], "the issue is a debt to the card's holder")
	assert.Equal(t, []models.JournalLine{
		{Account: models.AccountGiftCard, Debit: 20_000},
		{Account: models.AccountReceivable, Credit: 20_000},
	}, lines[captured.ID], "spending the card reduces that debt")
	assert.Equal(t, []models.JournalLine{
		{Account: models.AccountReceivable, Debit: 5_000},
		{Account: models.AccountGiftCard, Credit: 5_000},
	}, lines[refund.ID])
}

// TestAGiftCardAuthorizationWaitsOnTheCard proves the balance lock of the card
// against a real server, with the interleaving forced as
// [TestAnAuthorizationWaitsOnTheBalanceLock] forces it: a competitor holds the
// card's lock, the authorization is seen waiting on it, the competitor spends
// the whole balance and commits, and the authorization has to read the fresh
// balance and decline. The balance covers ONE spend.
func TestAGiftCardAuthorizationWaitsOnTheCard(t *testing.T) {
	for _, isolation := range []string{"", "repeatable read"} {
		t.Run("isolation "+isolation, func(t *testing.T) {
			ctx := context.Background()
			svc, _ := giftCardServiceOn(t, isolation)
			issued := issueCard(ctx, t, svc, testAmount)
			ses := cardSession(ctx, t, svc, issued.Code, testAmount)

			rival, rivalPID := singleConnectionRepository(ctx, t)
			locked, spend := make(chan struct{}), make(chan struct{})
			rivalDone := make(chan error, 1)
			go func() {
				rivalDone <- rival.WithTx(ctx, func(ctx context.Context) error {
					if err := rival.LockGiftCardBalance(ctx, issued.Card.ID, testCurrency); err != nil {
						return err
					}
					close(locked)
					<-spend
					_, err := rival.AppendGiftCardEntry(ctx, models.GiftCardEntry{
						ID: models.NewGiftCardEntryID(), GiftCardID: issued.Card.ID,
						Amount: -testAmount, Kind: models.GiftCardHold, Reference: "the competitor",
					})

					return err
				})
			}()
			select {
			case <-locked:
			case err := <-rivalDone:
				t.Fatalf("the competitor could not take the card's lock: %v", err)
			}

			done := make(chan error, 1)
			go func() { _, authErr := svc.AuthorizePayment(ctx, ses.ID); done <- authErr }()

			var lastPollErr atomic.Value
			waited := assert.Eventually(t, func() bool {
				waiters, err := lockWaiters(ctx, rivalPID)
				if err != nil {
					lastPollErr.Store(err.Error())

					return false
				}

				return waiters > 0
			}, 10*time.Second, 10*time.Millisecond)
			close(spend)
			require.NoError(t, <-rivalDone)
			if !waited {
				pollErr, _ := lastPollErr.Load().(string)
				t.Fatalf("the authorization never waited on the card's lock (last poll error: %q)", pollErr)
			}

			select {
			case authErr := <-done:
				require.Error(t, authErr, "the authorization read the balance from before the spend")
				assert.True(t, errors.IsConflict(authErr), "an insufficient card is a decline: %v", authErr)
			case <-time.After(10 * time.Second):
				t.Fatal("the authorization did not finish after the competitor committed")
			}
			assert.Zero(t, cardBalance(ctx, t, svc, issued.Card.ID), "below zero the card was spent twice")
		})
	}
}

// TestTheGiftCardConstraintsAreTheLastDefence writes past the service with raw
// SQL and names the constraint each refusal comes from.
func TestTheGiftCardConstraintsAreTheLastDefence(t *testing.T) {
	ctx := context.Background()
	pool := testPool.Pool()
	svc, _ := giftCardServiceOn(t, "")
	card := issueCard(ctx, t, svc, 1_000).Card

	refusedBy := func(t *testing.T, constraint, statement string, args ...any) {
		t.Helper()

		_, err := pool.Exec(ctx, statement, args...)
		require.Error(t, err)
		var pgErr *pgconn.PgError
		require.ErrorAs(t, err, &pgErr)
		assert.Equal(t, constraint, pgErr.ConstraintName)
	}
	insertCard := `INSERT INTO payment_gift_cards (id, code_digest, code_tail, currency_code, reason)
                   VALUES ($1, $2, $3, 'TRY', $4)`
	digest := models.GiftCardCodeDigest("ZZZZZZZZZZZZZZZZ")
	insertEntry := `INSERT INTO payment_gift_card_entries (id, gift_card_id, amount, kind, reference)
                    VALUES ($1, $2, $3, $4, $5)`

	refusedBy(t, "payment_gift_cards_digest_is_sha256", insertCard, models.NewGiftCardID(), "the code itself", "ZZZZ", "a test")
	refusedBy(t, "payment_gift_cards_tail_four", insertCard, models.NewGiftCardID(), digest, "ZZZZZ", "a test")
	refusedBy(t, "payment_gift_cards_reason_not_blank", insertCard, models.NewGiftCardID(), digest, "ZZZZ", "  ")
	refusedBy(t, "payment_gift_card_entries_sign_matches_kind", insertEntry,
		models.NewGiftCardEntryID(), card.ID, 100, "hold", "gcses_x")
	refusedBy(t, "payment_gift_card_entries_session_named", insertEntry,
		models.NewGiftCardEntryID(), card.ID, -100, "hold", "")
	refusedBy(t, "payment_gift_card_entries_session_named", insertEntry,
		models.NewGiftCardEntryID(), card.ID, 100, "issue", "gcses_x")
	refusedBy(t, "payment_gift_card_entries_one_issue", insertEntry,
		models.NewGiftCardEntryID(), card.ID, 100, "issue", "")
	refusedBy(t, "payment_gift_card_entries_kind_valid", insertEntry,
		models.NewGiftCardEntryID(), card.ID, 100, "bonus", "gcses_x")

	var storedDigest string
	require.NoError(t, pool.QueryRow(ctx, `SELECT code_digest FROM payment_gift_cards WHERE id = $1`, card.ID).
		Scan(&storedDigest))
	refusedBy(t, "payment_gift_cards_code_digest_uniq", insertCard, models.NewGiftCardID(), storedDigest, "ZZZZ", "a test")
}

// TestAGiftCardHoldsBackTheRollback: 000009's down stops while a card exists,
// and goes through when none does. It rolls the schema back, so it runs in a
// database of its own (D135).
func TestAGiftCardHoldsBackTheRollback(t *testing.T) {
	ctx := context.Background()
	src := payment.New().Migrations()

	// MigrateDown counts STEPS back, not a version: 000010 is undone first,
	// then 000009 on its own.
	emptyDSN, empty := isolatedDatabase(ctx, t, "payment_gift_rollback_empty")
	require.NoError(t, db.MigrateDown(ctx, emptyDSN, src, payment.ModuleName, 2))
	assert.False(t, tableExistsIn(ctx, t, empty, "payment_gift_cards"), "with no card the rollback goes through")

	heldDSN, held := isolatedDatabase(ctx, t, "payment_gift_rollback_held")
	repo := repository.New(held.Pool())
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(manual.New(repo, nil)))
	svc, err := service.New(service.Options{Store: repo, Providers: registry, Events: eventbus.NewInMemory(nil)})
	require.NoError(t, err)
	issued := issueCard(ctx, t, svc, 1_000)

	require.NoError(t, db.MigrateDown(ctx, heldDSN, src, payment.ModuleName, 1), "000010 holds no sold card here")
	err = db.MigrateDown(ctx, heldDSN, src, payment.ModuleName, 1)
	require.Error(t, err, "the rollback has to stop while a card is owed to its holder")
	assert.Contains(t, err.Error(), "payment_gift_cards_none_on_rollback", "000009's own refusal stopped it")
	var cards int
	require.NoError(t, held.Pool().QueryRow(ctx,
		`SELECT count(*) FROM payment_gift_cards WHERE id = $1`, issued.Card.ID).Scan(&cards))
	assert.Equal(t, 1, cards, "the card is still there")
}

// TestTwoDeliveriesOfOneSaleMakeOneCard is ADR 0210 on the real schema: the bus
// delivers a capture at least once, and two handlers issuing the same sale at
// once make one card, one of them holding its code.
func TestTwoDeliveriesOfOneSaleMakeOneCard(t *testing.T) {
	ctx := context.Background()
	svc, _ := giftCardServiceOn(t, "")
	reference := "oline_" + models.NewGiftCardID() + ":1"

	type result struct {
		code    string
		created bool
		err     error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			issued, created, err := svc.IssueSoldGiftCard(ctx, service.SoldGiftCardInput{
				Reference: reference, OrderID: "order_x", CurrencyCode: testCurrency, Amount: 4_000,
			})
			results <- result{code: issued.Code, created: created, err: err}
		}()
	}
	close(start)

	var codes, made int
	for range 2 {
		r := <-results
		require.NoError(t, r.err)
		if r.code != "" {
			codes++
		}
		if r.created {
			made++
		}
	}
	assert.Equal(t, 1, made, "one card")
	assert.Equal(t, 1, codes, "one code handed out")
	var cards, issues int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM payment_gift_cards WHERE source_reference = $1`, reference).Scan(&cards))
	require.NoError(t, testPool.Pool().QueryRow(ctx, `
        SELECT count(*) FROM payment_gift_card_entries e JOIN payment_gift_cards g ON g.id = e.gift_card_id
        WHERE g.source_reference = $1`, reference).Scan(&issues))
	assert.Equal(t, 1, cards)
	assert.Equal(t, 1, issues, "one issue row")
}

// TestASoldCardIsTheOrdersAndNotTheJournals: the payment journal books an
// issued card's cost and not a sold card's sale (ADR 0210).
func TestASoldCardIsTheOrdersAndNotTheJournals(t *testing.T) {
	ctx := context.Background()
	from := time.Now().Add(-time.Minute)
	svc, _ := giftCardServiceOn(t, "")
	issued := issueCard(ctx, t, svc, 1_000)
	sold, _, err := svc.IssueSoldGiftCard(ctx, service.SoldGiftCardInput{
		Reference: "oline_" + models.NewGiftCardID() + ":1", OrderID: "order_y", CurrencyCode: testCurrency, Amount: 2_000,
	})
	require.NoError(t, err)

	journal, err := svc.Journal(ctx, service.JournalQuery{From: from, To: time.Now().Add(time.Minute)})
	require.NoError(t, err)
	entryOf := func(cardID string) string {
		entries, _, err := svc.ListGiftCardEntries(ctx, cardID, service.Page{})
		require.NoError(t, err)
		require.Len(t, entries, 1)
		return entries[0].ID
	}
	booked := map[string]bool{}
	for _, entry := range journal.Entries {
		booked[entry.ID] = true
	}
	assert.True(t, booked[entryOf(issued.Card.ID)], "an issued card is a cost the shop took on")
	assert.False(t, booked[entryOf(sold.Card.ID)], "a sold card is the order's sale")
}

// TestAReplacedCodeStopsTheOldOne: the card is found by its new code only, and
// its balance is where it was.
func TestAReplacedCodeStopsTheOldOne(t *testing.T) {
	ctx := context.Background()
	svc, repo := giftCardServiceOn(t, "")
	issued := issueCard(ctx, t, svc, 6_000)

	replaced, err := svc.ReplaceGiftCardCode(ctx, issued.Card.ID)
	require.NoError(t, err)

	provider := giftcard.New(repo, nil)
	check := func(code string) error {
		return provider.CheckPayment(ctx, coreprovider.CreateSessionInput{
			CurrencyCode: testCurrency, Data: map[string]any{giftcard.DataCode: code},
		})
	}
	assert.Equal(t, giftcard.CodeUnknown, errors.CodeOf(check(issued.Code)), "the old code opens nothing")
	require.NoError(t, check(replaced.Code))
	assert.Equal(t, int64(6_000), replaced.Balance)
	assert.NotNil(t, replaced.Card.CodeChangedAt)
}

// TestTheSaleConstraintsAreTheLastDefence: a sold card names its sale, and a
// sale names one card.
func TestTheSaleConstraintsAreTheLastDefence(t *testing.T) {
	ctx := context.Background()
	insert := `INSERT INTO payment_gift_cards (id, code_digest, code_tail, currency_code, reason, source, source_reference)
               VALUES ($1, $2, 'ZZZZ', 'TRY', 'a test', $3, $4)`
	refusedBy := func(constraint string, args ...any) {
		t.Helper()
		_, err := testPool.Pool().Exec(ctx, insert, args...)
		require.Error(t, err)
		var pgErr *pgconn.PgError
		require.ErrorAs(t, err, &pgErr)
		assert.Equal(t, constraint, pgErr.ConstraintName)
	}
	digest := func() string { return models.GiftCardCodeDigest(models.NewGiftCardID()[6:22]) }

	refusedBy("payment_gift_cards_sold_names_its_sale", models.NewGiftCardID(), digest(), "sold", nil)
	refusedBy("payment_gift_cards_sold_names_its_sale", models.NewGiftCardID(), digest(), "issued", "oline_z:1")
	refusedBy("payment_gift_cards_source_valid", models.NewGiftCardID(), digest(), "gifted", nil)
	reference := "oline_" + models.NewGiftCardID() + ":1"
	_, err := testPool.Pool().Exec(ctx, insert, models.NewGiftCardID(), digest(), "sold", reference)
	require.NoError(t, err)
	refusedBy("payment_gift_cards_source_reference_uniq", models.NewGiftCardID(), digest(), "sold", reference)
}

// TestASoldCardHoldsBackTheSecondRollback: 000010's down stops while a sold
// card exists. It runs in a database of its own (D135).
func TestASoldCardHoldsBackTheSecondRollback(t *testing.T) {
	ctx := context.Background()
	src := payment.New().Migrations()
	dsn, pool := isolatedDatabase(ctx, t, "payment_sold_rollback")
	repo := repository.New(pool.Pool())
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(manual.New(repo, nil)))
	svc, err := service.New(service.Options{Store: repo, Providers: registry, Events: eventbus.NewInMemory(nil)})
	require.NoError(t, err)
	_, _, err = svc.IssueSoldGiftCard(ctx, service.SoldGiftCardInput{
		Reference: "oline_r:1", OrderID: "order_r", CurrencyCode: testCurrency, Amount: 1_000,
	})
	require.NoError(t, err)

	err = db.MigrateDown(ctx, dsn, src, payment.ModuleName, 1)
	require.Error(t, err, "the rollback has to stop while a card records the sale it came from")
	assert.Contains(t, err.Error(), "payment_gift_cards_none_sold_on_rollback", "000010's own refusal stopped it")
}
