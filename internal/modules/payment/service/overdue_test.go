package service_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/offline"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// An offline method's wait, and the sessions past it (ADR 0289).

// overdueRegistry registers a card, a bank transfer and cash on delivery.
func overdueRegistry(t *testing.T) *service.ProviderRegistry {
	t.Helper()

	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(newFakeProvider(refundProviderID)))
	for _, method := range []string{"bank_transfer", "cash_on_delivery"} {
		provider, err := offline.New(method)
		require.NoError(t, err)
		require.NoError(t, registry.Register(provider))
	}

	return registry
}

// overdueCase is a service whose bank transfer waits three days, and the
// sessions it was given, by what each is for.
type overdueCase struct {
	svc      *service.Service
	now      time.Time
	sessions map[string]string
}

// overdueFixture opens one collection per case, each with its sessions
// authorized and opened the given days before now.
func overdueFixture(t *testing.T) overdueCase {
	t.Helper()

	store := newFakeStore()
	svc, err := service.New(service.Options{
		Store: store, Providers: overdueRegistry(t), Events: newFakeBus(),
		OfflineWaitDays: map[string]int{"bank_transfer": 3},
	})
	require.NoError(t, err)
	c := overdueCase{svc: svc, now: time.Now().UTC(), sessions: map[string]string{}}

	open := func(name, providerID string, amount int64, daysAgo int, capture bool) string {
		col, err := svc.CreatePaymentCollection(t.Context(), service.CreateCollectionInput{
			Reference: "cart_" + name, Amount: 10_000, CurrencyCode: "TRY",
		})
		require.NoError(t, err)
		ses, err := svc.CreateSession(t.Context(), col.ID, providerID, service.CreateSessionInput{
			Amount: amount, IdempotencyKey: name + providerID,
		})
		require.NoError(t, err)
		_, err = svc.AuthorizePayment(t.Context(), ses.ID)
		require.NoError(t, err)
		if capture {
			_, err = svc.CapturePayment(t.Context(), ses.ID, 0)
			require.NoError(t, err)
		}
		backdated := store.sessions[ses.ID]
		backdated.CreatedAt = c.now.AddDate(0, 0, -daysAgo)
		store.sessions[ses.ID] = backdated
		c.sessions[name] = ses.ID

		return col.ID
	}

	open("four days", "bank_transfer", 10_000, 4, false)
	open("seven days", "bank_transfer", 10_000, 7, false)
	open("two days", "bank_transfer", 10_000, 2, false)
	open("cash", "cash_on_delivery", 10_000, 30, false)
	open("paid", "bank_transfer", 10_000, 6, true)
	split := open("split", "bank_transfer", 6_000, 5, false)
	gift, err := svc.CreateSession(t.Context(), split, refundProviderID, service.CreateSessionInput{
		Amount: 4_000, IdempotencyKey: "split-card",
	})
	require.NoError(t, err)
	_, err = svc.AuthorizePayment(t.Context(), gift.ID)
	require.NoError(t, err)
	_, err = svc.CapturePayment(t.Context(), gift.ID, 0)
	require.NoError(t, err)

	return c
}

// TestTheTransfersPastTheirWaitAreOverdue: of the transfers, the ones opened
// more than three days ago and still authorized are overdue, oldest first; the
// one inside its wait, the paid one and the one beside a captured card are
// not, and cash on delivery, given no wait, never is.
func TestTheTransfersPastTheirWaitAreOverdue(t *testing.T) {
	c := overdueFixture(t)

	overdue, err := c.svc.ListOverdueOffline(t.Context(), c.now, service.OverdueKey{}, 10)
	require.NoError(t, err)

	ids := make([]string, 0, len(overdue))
	for i := range overdue {
		ids = append(ids, overdue[i].ID)
	}
	assert.Equal(t, []string{c.sessions["seven days"], c.sessions["four days"]}, ids)
}

// TestTheOverduePageContinuesAfterItsKey: a page of one, continued from its
// last session, reaches the next and then nothing.
func TestTheOverduePageContinuesAfterItsKey(t *testing.T) {
	c := overdueFixture(t)

	first, err := c.svc.ListOverdueOffline(t.Context(), c.now, service.OverdueKey{}, 1)
	require.NoError(t, err)
	require.Len(t, first, 1)
	assert.Equal(t, c.sessions["seven days"], first[0].ID)

	key := service.OverdueKey{OpenedAt: first[0].CreatedAt, SessionID: first[0].ID}
	second, err := c.svc.ListOverdueOffline(t.Context(), c.now, key, 1)
	require.NoError(t, err)
	require.Len(t, second, 1)
	assert.Equal(t, c.sessions["four days"], second[0].ID)

	key = service.OverdueKey{OpenedAt: second[0].CreatedAt, SessionID: second[0].ID}
	last, err := c.svc.ListOverdueOffline(t.Context(), c.now, key, 1)
	require.NoError(t, err)
	assert.Empty(t, last)
}

// TestNothingIsOverdueWithoutAWait: an installation that gave no method a wait
// reports nothing, however old its transfers.
func TestNothingIsOverdueWithoutAWait(t *testing.T) {
	svc, err := service.New(service.Options{
		Store: newFakeStore(), Providers: overdueRegistry(t), Events: newFakeBus(),
	})
	require.NoError(t, err)

	overdue, err := svc.ListOverdueOffline(t.Context(), time.Now().AddDate(1, 0, 0), service.OverdueKey{}, 10)
	require.NoError(t, err)
	assert.NotNil(t, overdue)
	assert.Empty(t, overdue)
}

// TestAWaitTheServiceCannotHonorStopsTheStartup: a wait for a card, for a
// method nobody registered, or outside one day to a year is refused, since
// the shop would believe its orders expire.
func TestAWaitTheServiceCannotHonorStopsTheStartup(t *testing.T) {
	for name, waits := range map[string]map[string]int{
		"a card":           {refundProviderID: 3},
		"an unknown name":  {"wire_transfer": 3},
		"no day":           {"bank_transfer": 0},
		"more than a year": {"bank_transfer": service.MaxOfflineWaitDays + 1},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := service.New(service.Options{
				Store: newFakeStore(), Providers: overdueRegistry(t), Events: newFakeBus(),
				OfflineWaitDays: waits,
			})
			require.Error(t, err)
			assert.Equal(t, service.CodeNotReady, coreerrors.CodeOf(err))
		})
	}

	_, err := service.New(service.Options{
		Store: newFakeStore(), Providers: overdueRegistry(t), Events: newFakeBus(),
		OfflineWaitDays: map[string]int{"bank_transfer": service.MaxOfflineWaitDays},
	})
	require.NoError(t, err, "a year is the ceiling, and it holds")
}
