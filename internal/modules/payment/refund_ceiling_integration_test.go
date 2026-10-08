//go:build integration

package payment_test

import (
	"context"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/repository"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// gatedRefundProvider is the manual provider whose refund waits for the test
// once the gate is armed: the first refund to reach it says so and holds its
// transaction, with the collection's lock, until the test lets it go.
type gatedRefundProvider struct {
	coreprovider.PaymentProvider

	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

// Refund reports that a refund reached the provider and, while the gate is
// armed, waits to be let go.
func (p *gatedRefundProvider) Refund(ctx context.Context, sessionID string, amount int64) error {
	if !p.armed.Load() {
		return p.PaymentProvider.Refund(ctx, sessionID, amount)
	}
	select {
	case p.entered <- struct{}{}:
	default:
	}
	select {
	case <-p.release:
	case <-ctx.Done():
		return ctx.Err()
	}

	return p.PaymentProvider.Refund(ctx, sessionID, amount)
}

// gatedRefundService builds a real-repository service whose manual provider is
// gated, on a pool whose connections start at the given isolation level, empty
// being the server's own.
func gatedRefundService(t *testing.T, defaultIsolation string) (*service.Service, *gatedRefundProvider) {
	t.Helper()

	pool := testPool.Pool()
	if defaultIsolation != "" {
		own, err := pgxpool.New(context.Background(),
			testDSN+"&default_transaction_isolation="+url.QueryEscape(defaultIsolation))
		require.NoError(t, err)
		t.Cleanup(own.Close)
		var level string
		require.NoError(t, own.QueryRow(context.Background(), `SHOW default_transaction_isolation`).Scan(&level))
		require.Equal(t, defaultIsolation, level, "the pool has to start at the level under test")
		pool = own
	}

	repo := repository.New(pool)
	prov := &gatedRefundProvider{
		PaymentProvider: manual.New(repo, nil),
		entered:         make(chan struct{}, 1),
		release:         make(chan struct{}),
	}
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(prov))
	svc, err := service.New(service.Options{Store: repo, Providers: registry, Events: eventbus.NewInMemory(nil)})
	require.NoError(t, err)

	return svc, prov
}

// waitersOnALock counts the backends of the test database waiting on a lock.
// The package's tests run one at a time, so a waiter is this test's.
func waitersOnALock(ctx context.Context) (int64, error) {
	var waiters int64
	err := testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM pg_stat_activity
         WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiters)

	return waiters, err
}

// refundCause refunds a collection for a cause held to a ceiling.
func refundCause(
	ctx context.Context, svc *service.Service, collectionID string, amount, ceiling int64, reference string,
) ([]models.Refund, error) {
	return svc.RefundCollection(ctx, collectionID, amount, ceiling, "race", reference)
}

// TestACausesRefundsStayUnderItsCeilingAtOnce is ADR 0433's lock on a real
// server: two refunds of one 8 000 return out of a 20 000 collection a claim
// already took 4 000 from, the second asked while the first holds the
// collection's lock with its provider call in flight, give back 8 000 between
// them, and the second is refused with payment_refund_exceeds_cause. A check
// that read what the cause had left before the lock passes both, and before
// ADR 0433 both paid: 16 000.
//
// The interleaving is forced: the first refund's provider call holds until
// the second waits on a lock, so the second has read whatever it reads before
// the lock while the first is not yet committed. The outcome is asserted, not
// the wait. It runs on a pool at the server's level and on one whose
// connections default to REPEATABLE READ (D119).
func TestACausesRefundsStayUnderItsCeilingAtOnce(t *testing.T) {
	for _, isolation := range []string{"", "repeatable read"} {
		t.Run("isolation="+isolation, func(t *testing.T) {
			// A refund left waiting at the gate gives up with the context, so a
			// failing case ends rather than holding its connection for ever.
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			svc, prov := gatedRefundService(t, isolation)

			collection, err := svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
				Reference: testReference, Amount: 20_000, CurrencyCode: testCurrency,
			})
			require.NoError(t, err)
			session, err := svc.CreateSession(ctx, collection.ID, manual.ID, service.CreateSessionInput{
				Amount: 20_000, IdempotencyKey: "ceiling-" + collection.ID,
			})
			require.NoError(t, err)
			_, err = svc.AuthorizePayment(ctx, session.ID)
			require.NoError(t, err)
			_, err = svc.CapturePayment(ctx, session.ID, 0)
			require.NoError(t, err)

			// Another cause's refund out of the same collection, which the
			// race's cause does not count: a sum over the collection would
			// refuse both refunds below.
			_, err = refundCause(ctx, svc, collection.ID, 4_000, 4_000, "clm_other_"+collection.ID)
			require.NoError(t, err)

			cause := "ret_race_" + collection.ID
			prov.armed.Store(true)
			outcomes := make(chan error, 2)
			go func() {
				_, err := refundCause(ctx, svc, collection.ID, 8_000, 8_000, cause)
				outcomes <- err
			}()
			select {
			case <-prov.entered:
			case err := <-outcomes:
				close(prov.release)
				t.Fatalf("the first refund never reached the provider: %v", err)
			case <-time.After(10 * time.Second):
				close(prov.release)
				t.Fatal("the first refund never reached the provider")
			}

			go func() {
				_, err := refundCause(ctx, svc, collection.ID, 8_000, 8_000, cause)
				outcomes <- err
			}()
			waited := assert.Eventually(t, func() bool {
				waiters, err := waitersOnALock(ctx)
				return err == nil && waiters > 0
			}, 10*time.Second, 10*time.Millisecond, "the second refund waits on the first's lock")
			close(prov.release)

			var refused []error
			for range 2 {
				select {
				case err := <-outcomes:
					if err != nil {
						refused = append(refused, err)
					}
				case <-time.After(20 * time.Second):
					t.Fatal("a refund did not finish after the first was let go")
				}
			}
			require.True(t, waited, "the window was not opened, so the outcome proves nothing")

			given, err := svc.CausedRefundsOf(ctx, []string{cause})
			require.NoError(t, err)
			var total int64
			for i := range given {
				total += given[i].Amount
			}
			assert.Equal(t, int64(8_000), total, "the refunds naming the cause stay at its ceiling")
			current, err := svc.GetPaymentCollection(ctx, collection.ID)
			require.NoError(t, err)
			assert.Equal(t, int64(4_000+8_000), current.RefundedAmount,
				"the collection gave back the other cause's refund and the ceiling once")
			require.Len(t, refused, 1, "exactly one of the two is refused")
			assert.Equal(t, service.CodeRefundExceedsCause, errors.CodeOf(refused[0]), "%v", refused[0])
			assert.True(t, errors.IsConflict(refused[0]))
		})
	}
}
