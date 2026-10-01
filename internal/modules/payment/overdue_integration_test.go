//go:build integration

// The overdue offline listing, proven against a real PostgreSQL (ADR 0289).
//
// The unit tests run a fake written to match the query, so the two agree by
// construction; what is proven here is the predicate itself — each provider's
// own cutoff found by array_position, the collection that captured something
// left out, and the (created_at, id) key, ties included.
package payment_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	"github.com/bdrtr/gobit/internal/modules/payment/offline"
	"github.com/bdrtr/gobit/internal/modules/payment/repository"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// The methods are named for this file alone: the database is shared, and the
// query reads by provider.
const (
	overdueTransfer = "overdue_transfer"
	overdueSlow     = "overdue_slow"
	overdueCash     = "overdue_cash"
)

// overdueService gives the transfer three days, the slow method forty and the
// cash none: two waits, so each session is held to its own provider's.
func overdueService(t *testing.T) *service.Service {
	t.Helper()

	repo := repository.New(testPool.Pool())
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(manual.New(repo, nil)))
	for _, method := range []string{overdueTransfer, overdueSlow, overdueCash} {
		provider, err := offline.New(method)
		require.NoError(t, err)
		require.NoError(t, registry.Register(provider))
	}
	svc, err := service.New(service.Options{
		Store: repo, Providers: registry, Events: eventbus.NewInMemory(nil),
		OfflineWaitDays: map[string]int{overdueTransfer: 3, overdueSlow: 40},
	})
	require.NoError(t, err)

	return svc
}

// The state a fixture session is left in.
const (
	leftPending = iota
	leftAuthorized
	leftCaptured
)

// openedAgo opens a session in a new collection, or in the given one, leaves it
// in the given state and moves its opening into the past.
func openedAgo(
	ctx context.Context, t *testing.T, svc *service.Service,
	collectionID, providerID string, opened time.Time, state int,
) (sessionID, collection string) {
	t.Helper()

	collection = collectionID
	if collection == "" {
		col, err := svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
			Reference: "cart_overdue", Amount: 10_000, CurrencyCode: "TRY",
		})
		require.NoError(t, err)
		collection = col.ID
	}
	var data map[string]any
	if providerID == manual.ID {
		data = map[string]any{manual.DataKeyOutcome: manual.OutcomeAuthorize}
	}
	ses, err := svc.CreateSession(ctx, collection, providerID, service.CreateSessionInput{
		Amount: 5_000, IdempotencyKey: fmt.Sprintf("%s-%s-%s", t.Name(), providerID, collection),
		Data: data,
	})
	require.NoError(t, err)
	if state >= leftAuthorized {
		_, err = svc.AuthorizePayment(ctx, ses.ID)
		require.NoError(t, err)
	}
	if state == leftCaptured {
		_, err = svc.CapturePayment(ctx, ses.ID, 0)
		require.NoError(t, err)
	}
	_, err = testPool.Pool().Exec(ctx, `UPDATE payment_sessions SET created_at = $2 WHERE id = $1`, ses.ID, opened)
	require.NoError(t, err)

	return ses.ID, collection
}

// overdueIDs pages through the listing one session at a time and returns what
// it read, in order.
func overdueIDs(ctx context.Context, t *testing.T, svc *service.Service, now time.Time) []string {
	t.Helper()

	var ids []string
	key := service.OverdueKey{}
	for range 20 {
		page, err := svc.ListOverdueOffline(ctx, now, key, 1)
		require.NoError(t, err)
		if len(page) == 0 {
			return ids
		}
		ids = append(ids, page[0].ID)
		key = service.OverdueKey{OpenedAt: page[0].CreatedAt, SessionID: page[0].ID}
	}
	t.Fatal("the listing did not end")

	return nil
}

// TestTheOverdueListingOnTheRealQuery: the sessions past their own method's
// wait, still authorized, in collections that captured nothing, oldest first;
// two opened at the same moment are each read once, in id order.
func TestTheOverdueListingOnTheRealQuery(t *testing.T) {
	ctx := context.Background()
	svc := overdueService(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	day := 24 * time.Hour

	slowPast, _ := openedAgo(ctx, t, svc, "", overdueSlow, now.Add(-41*day), leftAuthorized)
	openedAgo(ctx, t, svc, "", overdueSlow, now.Add(-30*day), leftAuthorized)
	sevenDays, _ := openedAgo(ctx, t, svc, "", overdueTransfer, now.Add(-7*day), leftAuthorized)
	twinA, _ := openedAgo(ctx, t, svc, "", overdueTransfer, now.Add(-4*day), leftAuthorized)
	twinB, _ := openedAgo(ctx, t, svc, "", overdueTransfer, now.Add(-4*day), leftAuthorized)
	openedAgo(ctx, t, svc, "", overdueTransfer, now.Add(-2*day), leftAuthorized)
	openedAgo(ctx, t, svc, "", overdueTransfer, now.Add(-10*day), leftPending)
	openedAgo(ctx, t, svc, "", overdueCash, now.Add(-90*day), leftAuthorized)
	openedAgo(ctx, t, svc, "", overdueTransfer, now.Add(-6*day), leftCaptured)
	_, split := openedAgo(ctx, t, svc, "", overdueTransfer, now.Add(-5*day), leftAuthorized)
	openedAgo(ctx, t, svc, split, manual.ID, now.Add(-5*day), leftCaptured)

	twins := []string{twinA, twinB}
	if twinB < twinA {
		twins = []string{twinB, twinA}
	}
	assert.Equal(t, append([]string{slowPast, sevenDays}, twins...), overdueIDs(ctx, t, svc, now),
		"the slow method's thirty days are inside its forty, a pending session promised nothing, "+
			"cash on delivery has no wait, and a captured payment is money")
}
