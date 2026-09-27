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

// rollBackTo rolls the module's schema back to the given version.
//
// MigrateDown counts STEPS, and a step count written as a number is the size
// of the tree the day it was written: the next migration makes it undo the
// wrong one. The count is derived from the version the database is at, and a
// database already there is left alone, since zero steps means every one.
func rollBackTo(ctx context.Context, t *testing.T, dsn string, version uint) {
	t.Helper()

	current, dirty, err := db.Version(ctx, dsn, payment.ModuleName)
	require.NoError(t, err)
	require.False(t, dirty)
	require.GreaterOrEqual(t, current, version)
	if current == version {
		return
	}
	require.NoError(t, db.MigrateDown(ctx, dsn, payment.New().Migrations(), payment.ModuleName,
		int(current-version)), "the migrations after %d could not be rolled back", version)
}

// assertRefusedBy asserts that a rollback was stopped by the named CHECK.
//
// The migration's error carries the migration's own text, and a down file
// names its refusal twice — where it adds it and where it drops it — so the
// bare name is in the message whatever stopped the rollback (D148). The
// server's report quotes the constraint that refused, and that is what is
// asserted.
func assertRefusedBy(t *testing.T, err error, constraint string) {
	t.Helper()

	require.Error(t, err)
	assert.Contains(t, err.Error(), `check constraint "`+constraint+`"`,
		"the rollback has to be stopped by %s itself", constraint)
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

	// MigrateDown counts STEPS back, not a version: the later migrations are
	// undone first, then 000009 on its own.
	emptyDSN, empty := isolatedDatabase(ctx, t, "payment_gift_rollback_empty")
	rollBackTo(ctx, t, emptyDSN, 8)
	assert.False(t, tableExistsIn(ctx, t, empty, "payment_gift_cards"), "with no card the rollback goes through")

	heldDSN, held := isolatedDatabase(ctx, t, "payment_gift_rollback_held")
	repo := repository.New(held.Pool())
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(manual.New(repo, nil)))
	svc, err := service.New(service.Options{Store: repo, Providers: registry, Events: eventbus.NewInMemory(nil)})
	require.NoError(t, err)
	issued := issueCard(ctx, t, svc, 1_000)

	rollBackTo(ctx, t, heldDSN, 9)
	err = db.MigrateDown(ctx, heldDSN, src, payment.ModuleName, 1)
	require.Error(t, err, "the rollback has to stop while a card is owed to its holder")
	assertRefusedBy(t, err, "payment_gift_cards_none_on_rollback")
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

// TestTheSoldReferencesAreReadOnTheRealSchema: the sweep's read names the
// sales that made a card and only them, an operator's card included in none
// (ADR 0212).
func TestTheSoldReferencesAreReadOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc, _ := giftCardServiceOn(t, "")
	sold := "oline_" + models.NewGiftCardID() + ":1"
	_, _, err := svc.IssueSoldGiftCard(ctx, service.SoldGiftCardInput{
		Reference: sold, OrderID: "order_refs", CurrencyCode: testCurrency, Amount: 1_000,
	})
	require.NoError(t, err)
	_, err = svc.IssueGiftCard(ctx, service.IssueGiftCardInput{CurrencyCode: testCurrency, Amount: 1_000, Reason: "refs"})
	require.NoError(t, err)

	found, err := svc.SoldGiftCardReferences(ctx, []string{sold, "oline_nobody:1"})

	require.NoError(t, err)
	assert.Equal(t, []string{sold}, found)
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
	rollBackTo(ctx, t, dsn, 10)

	err = db.MigrateDown(ctx, dsn, src, payment.ModuleName, 1)
	require.Error(t, err, "the rollback has to stop while a card records the sale it came from")
	assertRefusedBy(t, err, "payment_gift_cards_none_sold_on_rollback")
}

// TestAClosedCardOnTheRealSchema is ADR 0213 in the module: an issued card and
// a sold one are closed, what they held is voided, the journal books the two
// differently, and a closed card's code opens no payment.
func TestAClosedCardOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc, _ := giftCardServiceOn(t, "")
	from := time.Now().UTC().Add(-time.Second)
	issued := issueCard(ctx, t, svc, 5_000)
	sold, _, err := svc.IssueSoldGiftCard(ctx, service.SoldGiftCardInput{
		Reference: "oline_" + models.NewGiftCardID() + ":1", OrderID: "order_close",
		CurrencyCode: testCurrency, Amount: 3_000,
	})
	require.NoError(t, err)

	for _, id := range []string{issued.Card.ID, sold.Card.ID} {
		closed, err := svc.DisableGiftCard(ctx, id, "closed by a test")
		require.NoError(t, err)
		require.NotNil(t, closed.Card.DisabledAt)
		assert.Zero(t, cardBalance(ctx, t, svc, id))
	}

	journal, err := svc.Journal(ctx, service.JournalQuery{From: from, To: time.Now().UTC().Add(time.Minute)})
	require.NoError(t, err)
	credits := map[models.JournalKind]models.JournalLine{}
	for _, entry := range journal.Entries {
		if entry.Kind == models.JournalGiftCardVoid || entry.Kind == models.JournalGiftCardForfeit {
			assert.Equal(t, models.AccountGiftCard, entry.Lines[0].Account)
			credits[entry.Kind] = entry.Lines[1]
		}
	}
	assert.Equal(t, models.JournalLine{Account: models.AccountGiftCardGranted, Credit: 5_000},
		credits[models.JournalGiftCardVoid], "an issued card's cost comes back")
	assert.Equal(t, models.JournalLine{Account: models.AccountGiftCardForfeited, Credit: 3_000},
		credits[models.JournalGiftCardForfeit], "a sold card's price is kept")

	col, err := svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
		Reference: testReference + "-closed", Amount: 1_000, CurrencyCode: testCurrency,
	})
	require.NoError(t, err)
	_, err = svc.CreateSession(ctx, col.ID, giftcard.ID, service.CreateSessionInput{
		IdempotencyKey: "closed-" + col.ID, Data: map[string]any{giftcard.DataCode: issued.Code},
	})
	require.Error(t, err)
	assert.Equal(t, giftcard.CodeDisabled, errors.CodeOf(err))
}

// TestAHeldCardIsClosedOnceItsPaymentEnds: a card an authorized session holds
// is not closed, and once the session is canceled it is.
func TestAHeldCardIsClosedOnceItsPaymentEnds(t *testing.T) {
	ctx := context.Background()
	svc, _ := giftCardServiceOn(t, "")
	issued := issueCard(ctx, t, svc, 5_000)
	ses := cardSession(ctx, t, svc, issued.Code, 2_000)
	_, err := svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)

	_, err = svc.DisableGiftCard(ctx, issued.Card.ID, "closed by a test")
	require.Error(t, err)
	assert.Equal(t, service.CodeGiftCardHeld, errors.CodeOf(err))

	err = svc.CancelPayment(ctx, ses.ID)
	require.NoError(t, err)
	closed, err := svc.DisableGiftCard(ctx, issued.Card.ID, "closed by a test")
	require.NoError(t, err)
	assert.NotNil(t, closed.Card.DisabledAt)
	assert.Zero(t, cardBalance(ctx, t, svc, issued.Card.ID), "the released hold was voided with the rest")
}

// TestAClosedCardRefusesTheRefundOfWhatItPaid: money a card paid is not sent
// back onto it once it is closed, and nothing is written.
func TestAClosedCardRefusesTheRefundOfWhatItPaid(t *testing.T) {
	ctx := context.Background()
	svc, _ := giftCardServiceOn(t, "")
	issued := issueCard(ctx, t, svc, 5_000)
	ses := cardSession(ctx, t, svc, issued.Code, 2_000)
	_, err := svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)
	captured, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)
	_, err = svc.DisableGiftCard(ctx, issued.Card.ID, "closed by a test")
	require.NoError(t, err)

	_, err = svc.RefundPayment(ctx, captured.ID, 500, "a test")

	require.Error(t, err)
	assert.Equal(t, giftcard.CodeDisabled, errors.CodeOf(err))
	assert.Zero(t, cardBalance(ctx, t, svc, issued.Card.ID), "nothing landed on the closed card")
}

// TestTheCloseConstraintsAreTheLastDefence writes past the service with raw
// SQL and names the constraint each refusal comes from.
func TestTheCloseConstraintsAreTheLastDefence(t *testing.T) {
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
	entry := `INSERT INTO payment_gift_card_entries (id, gift_card_id, amount, kind, reference)
              VALUES ($1, $2, $3, 'void', $4)`

	refusedBy(t, "payment_gift_card_entries_sign_matches_kind", entry, models.NewGiftCardEntryID(), card.ID, 100, "")
	refusedBy(t, "payment_gift_card_entries_session_named", entry, models.NewGiftCardEntryID(), card.ID, -100, "gcses_1")
	_, err := pool.Exec(ctx, entry, models.NewGiftCardEntryID(), card.ID, -100, "")
	require.NoError(t, err)
	refusedBy(t, "payment_gift_card_entries_one_void", entry, models.NewGiftCardEntryID(), card.ID, -100, "")
	refusedBy(t, "payment_gift_cards_disable_explained",
		`UPDATE payment_gift_cards SET disabled_at = now() WHERE id = $1`, card.ID)
	refusedBy(t, "payment_gift_cards_disable_reason_not_blank",
		`UPDATE payment_gift_cards SET disabled_at = now(), disable_reason = '  ' WHERE id = $1`, card.ID)
}

// TestAClosedCardHoldsBackTheRollback: 000011's down stops while a closed card
// exists. It runs in a database of its own (D135).
func TestAClosedCardHoldsBackTheRollback(t *testing.T) {
	ctx := context.Background()
	src := payment.New().Migrations()
	dsn, pool := isolatedDatabase(ctx, t, "payment_closed_rollback")
	repo := repository.New(pool.Pool())
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(manual.New(repo, nil)))
	svc, err := service.New(service.Options{Store: repo, Providers: registry, Events: eventbus.NewInMemory(nil)})
	require.NoError(t, err)
	card := issueCard(ctx, t, svc, 1_000).Card
	_, err = svc.DisableGiftCard(ctx, card.ID, "closed by a test")
	require.NoError(t, err)
	rollBackTo(ctx, t, dsn, 11)

	err = db.MigrateDown(ctx, dsn, src, payment.ModuleName, 1)
	require.Error(t, err, "the rollback has to stop while a card is closed")
	assertRefusedBy(t, err, "payment_gift_cards_none_closed_on_rollback")
}

// TestACloseWaitsOnTheCard forces the interleaving the close is written for: a
// competitor holds the card's lock and puts a payment's hold on it, the close is
// seen waiting, and once the competitor commits the close refuses the held card
// rather than voiding a balance the hold already took part of.
func TestACloseWaitsOnTheCard(t *testing.T) {
	ctx := context.Background()
	svc, _ := giftCardServiceOn(t, "")
	issued := issueCard(ctx, t, svc, 5_000)

	rival, rivalPID := singleConnectionRepository(ctx, t)
	locked, hold := make(chan struct{}), make(chan struct{})
	rivalDone := make(chan error, 1)
	go func() {
		rivalDone <- rival.WithTx(ctx, func(ctx context.Context) error {
			if err := rival.LockGiftCardBalance(ctx, issued.Card.ID, testCurrency); err != nil {
				return err
			}
			close(locked)
			<-hold
			session, _, err := rival.InsertGiftCardSessionIfAbsent(ctx, models.TenderSession{
				ID: models.NewGiftCardSessionID(), IdempotencyKey: "rival-" + issued.Card.ID,
				Reference: "paycol_rival", OwnerID: issued.Card.ID, Amount: 2_000,
				CurrencyCode: testCurrency, Status: models.SessionPending,
			})
			if err != nil {
				return err
			}
			if _, err := rival.UpdateGiftCardSessionState(ctx, session.ID,
				models.SessionAuthorized, 2_000, 0, 0, ""); err != nil {
				return err
			}
			_, err = rival.AppendGiftCardEntry(ctx, models.GiftCardEntry{
				ID: models.NewGiftCardEntryID(), GiftCardID: issued.Card.ID,
				Amount: -2_000, Kind: models.GiftCardHold, Reference: session.ID,
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
	go func() { _, closeErr := svc.DisableGiftCard(ctx, issued.Card.ID, "closed by a test"); done <- closeErr }()

	var lastPollErr atomic.Value
	waited := assert.Eventually(t, func() bool {
		waiters, err := lockWaiters(ctx, rivalPID)
		if err != nil {
			lastPollErr.Store(err.Error())

			return false
		}

		return waiters > 0
	}, 10*time.Second, 10*time.Millisecond)
	close(hold)
	require.NoError(t, <-rivalDone)
	if !waited {
		pollErr, _ := lastPollErr.Load().(string)
		t.Fatalf("the close never waited on the card's lock (last poll error: %q)", pollErr)
	}

	select {
	case closeErr := <-done:
		require.Error(t, closeErr, "the close read the card from before the hold")
		assert.Equal(t, service.CodeGiftCardHeld, errors.CodeOf(closeErr))
	case <-time.After(10 * time.Second):
		t.Fatal("the close did not finish after the competitor committed")
	}
	assert.Equal(t, int64(3_000), cardBalance(ctx, t, svc, issued.Card.ID), "nothing was voided under the hold")
}

// TestTwoClosesAtOnceCloseOnce: a second close that waited on the card's lock
// while the first closed it reads the card again and changes nothing, rather
// than failing on a card that is no longer open.
func TestTwoClosesAtOnceCloseOnce(t *testing.T) {
	ctx := context.Background()
	svc, _ := giftCardServiceOn(t, "")
	issued := issueCard(ctx, t, svc, 5_000)

	rival, rivalPID := singleConnectionRepository(ctx, t)
	locked, finish := make(chan struct{}), make(chan struct{})
	rivalDone := make(chan error, 1)
	go func() {
		rivalDone <- rival.WithTx(ctx, func(ctx context.Context) error {
			if err := rival.LockGiftCardBalance(ctx, issued.Card.ID, testCurrency); err != nil {
				return err
			}
			close(locked)
			<-finish
			_, err := rival.DisableGiftCard(ctx, issued.Card.ID, "closed first")

			return err
		})
	}()
	select {
	case <-locked:
	case err := <-rivalDone:
		t.Fatalf("the first close could not take the card's lock: %v", err)
	}

	done := make(chan error, 1)
	go func() { _, closeErr := svc.DisableGiftCard(ctx, issued.Card.ID, "closed second"); done <- closeErr }()
	waited := assert.Eventually(t, func() bool {
		waiters, err := lockWaiters(ctx, rivalPID)

		return err == nil && waiters > 0
	}, 10*time.Second, 10*time.Millisecond)
	close(finish)
	require.NoError(t, <-rivalDone)
	require.True(t, waited, "the second close never waited on the card's lock")

	select {
	case closeErr := <-done:
		require.NoError(t, closeErr, "the second close found the card closed and left it so")
	case <-time.After(10 * time.Second):
		t.Fatal("the second close did not finish after the first committed")
	}
	card, err := svc.GetGiftCard(ctx, issued.Card.ID)
	require.NoError(t, err)
	assert.Equal(t, "closed first", card.Card.DisableReason, "the first close's reason stands")
}

// TestAClosedCardStopsTheCollectionRefundAtItsShare: a collection a card and a
// provider paid refunds its newest capture first, the card's, which a closed
// card refuses, so nothing moves; the provider's capture is refunded through
// its own payment.
func TestAClosedCardStopsTheCollectionRefundAtItsShare(t *testing.T) {
	ctx := context.Background()
	svc, _ := giftCardServiceOn(t, "")
	issued := issueCard(ctx, t, svc, 2_000)
	col, err := svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
		Reference: testReference + "-split-closed", Amount: 5_000, CurrencyCode: testCurrency,
	})
	require.NoError(t, err)
	card, err := svc.CreateSession(ctx, col.ID, giftcard.ID, service.CreateSessionInput{
		IdempotencyKey: "card-" + col.ID, Data: map[string]any{giftcard.DataCode: issued.Code},
	})
	require.NoError(t, err)
	_, err = svc.AuthorizePayment(ctx, card.ID)
	require.NoError(t, err)
	provider, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		Amount: 3_000, IdempotencyKey: "manual-" + col.ID,
		Data: map[string]any{manual.DataKeyOutcome: manual.OutcomeAuthorize},
	})
	require.NoError(t, err)
	_, err = svc.AuthorizePayment(ctx, provider.ID)
	require.NoError(t, err)
	providerPayment, err := svc.CapturePayment(ctx, provider.ID, 0)
	require.NoError(t, err)
	_, err = svc.CapturePayment(ctx, card.ID, 0)
	require.NoError(t, err)
	_, err = svc.DisableGiftCard(ctx, issued.Card.ID, "closed by a test")
	require.NoError(t, err)

	made, err := svc.RefundCollection(ctx, col.ID, 0, "a test", "")
	require.Error(t, err)
	assert.Equal(t, giftcard.CodeDisabled, errors.CodeOf(err))
	assert.Empty(t, made, "the card's capture is the newest and nothing moved")

	_, err = svc.RefundPayment(ctx, providerPayment.ID, 3_000, "a test")
	require.NoError(t, err, "the provider's capture is refunded through its own payment")
	assert.Zero(t, cardBalance(ctx, t, svc, issued.Card.ID))
}
