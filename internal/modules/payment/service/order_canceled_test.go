package service_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/offline"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// A canceled order holds no authorization (ADR 0288).

// orderLinks binds orders to their collections, as order_payment does.
type orderLinks map[string][]string

func (l orderLinks) List(_ context.Context, name, fromID string) ([]string, error) {
	if name != service.LinkOrderPayment {
		return nil, fmt.Errorf("unexpected link %q", name)
	}

	return l[fromID], nil
}

// staleSessionStore reports every session it lists as authorized, which is
// what a list read just before a capture took the session's lock looks like.
type staleSessionStore struct{ *fakeStore }

func (s staleSessionStore) ListPaymentSessionsByCollection(
	ctx context.Context, collectionID string,
) ([]models.PaymentSession, error) {
	sessions, err := s.fakeStore.ListPaymentSessionsByCollection(ctx, collectionID)
	for i := range sessions {
		sessions[i].Status = models.SessionAuthorized
	}

	return sessions, err
}

// errorLog keeps the messages a service logs at error level, which is where
// an operator is told about money on a canceled order.
type errorLog struct {
	mu       sync.Mutex
	messages []string
}

func (l *errorLog) Enabled(context.Context, slog.Level) bool { return true }

func (l *errorLog) Handle(_ context.Context, r slog.Record) error {
	if r.Level >= slog.LevelError {
		l.mu.Lock()
		l.messages = append(l.messages, r.Message)
		l.mu.Unlock()
	}

	return nil
}

func (l *errorLog) WithAttrs([]slog.Attr) slog.Handler { return l }

func (l *errorLog) WithGroup(string) slog.Handler { return l }

// canceledOrder is order_a, whose collection holds a card captured for 3,000,
// a card authorized for 2,000 and a transfer authorized for 5,000; and
// order_b, whose collection holds a transfer authorized for 4,000.
type canceledOrder struct {
	svc                              *service.Service
	card                             *fakeProvider
	errors                           *errorLog
	capturedCard, heldCard, transfer string
	otherOrdersTransfer              string
}

func canceledOrderFixture(t *testing.T, store service.Store, links service.OrderLinks) canceledOrder {
	t.Helper()

	transfer, err := offline.New("bank_transfer")
	require.NoError(t, err)
	card, log := newFakeProvider(refundProviderID), &errorLog{}
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(card))
	require.NoError(t, registry.Register(transfer))
	svc, err := service.New(service.Options{
		Store: store, Providers: registry, Events: newFakeBus(), Links: links, Logger: slog.New(log),
	})
	require.NoError(t, err)

	collection := func(reference string, amount int64) string {
		col, err := svc.CreatePaymentCollection(t.Context(), service.CreateCollectionInput{
			Reference: reference, Amount: amount, CurrencyCode: "TRY",
		})
		require.NoError(t, err)

		return col.ID
	}
	authorized := func(collectionID, providerID string, amount int64, key string) string {
		ses, err := svc.CreateSession(t.Context(), collectionID, providerID, service.CreateSessionInput{
			Amount: amount, IdempotencyKey: key,
		})
		require.NoError(t, err)
		_, err = svc.AuthorizePayment(t.Context(), ses.ID)
		require.NoError(t, err)

		return ses.ID
	}

	ours, theirs := collection("cart_a", 10_000), collection("cart_b", 4_000)
	c := canceledOrder{svc: svc, card: card, errors: log}
	c.capturedCard = authorized(ours, refundProviderID, 3_000, "captured-card")
	_, err = svc.CapturePayment(t.Context(), c.capturedCard, 0)
	require.NoError(t, err)
	c.heldCard = authorized(ours, refundProviderID, 2_000, "held-card")
	c.transfer = authorized(ours, transfer.ID(), 5_000, "transfer-a")
	c.otherOrdersTransfer = authorized(theirs, transfer.ID(), 4_000, "transfer-b")

	if links, ok := links.(orderLinks); ok {
		links["order_a"] = []string{ours}
		links["order_b"] = []string{theirs}
	}

	return c
}

// canceled is the order module's event for the order.
func canceled(orderID string) eventbus.Event {
	return eventbus.Event{
		ID: service.TopicOrderCanceled + ":" + orderID, Name: service.TopicOrderCanceled,
		Data: map[string]any{"order_id": orderID, "canceled_at": "2026-10-01T12:00:00Z"},
	}
}

// statusOf reads one session's status.
func statusOf(t *testing.T, svc *service.Service, sessionID string) models.SessionStatus {
	t.Helper()

	ses, err := svc.GetPaymentSession(t.Context(), sessionID)
	require.NoError(t, err)

	return ses.Status
}

// TestACanceledOrdersAuthorizedSessionsAreClosed: the transfer the shop no
// longer waits for and the card's hold are both closed; the captured card is
// money, left to a refund; another order's transfer is untouched.
func TestACanceledOrdersAuthorizedSessionsAreClosed(t *testing.T) {
	c := canceledOrderFixture(t, newFakeStore(), orderLinks{})

	require.NoError(t, c.svc.HandleOrderCanceled(t.Context(), canceled("order_a")))

	assert.Equal(t, models.SessionCanceled, statusOf(t, c.svc, c.transfer))
	assert.Equal(t, models.SessionCanceled, statusOf(t, c.svc, c.heldCard))
	assert.Equal(t, models.SessionCaptured, statusOf(t, c.svc, c.capturedCard))
	assert.Equal(t, models.SessionAuthorized, statusOf(t, c.svc, c.otherOrdersTransfer))
	assert.Empty(t, c.errors.messages, "a captured session is money, not a race; nothing is alarming")

	require.NoError(t, c.svc.HandleOrderCanceled(t.Context(), canceled("order_a")),
		"a second delivery finds nothing authorized")
}

// TestAProviderThatCannotCloseIsTriedAgain: a cancel the provider refuses for
// a reason other than a capture is returned, so the bus delivers the event
// again.
func TestAProviderThatCannotCloseIsTriedAgain(t *testing.T) {
	c := canceledOrderFixture(t, newFakeStore(), orderLinks{})
	c.card.cancelErr = errors.New("the provider is unreachable")

	require.Error(t, c.svc.HandleOrderCanceled(t.Context(), canceled("order_a")))

	assert.Equal(t, models.SessionAuthorized, statusOf(t, c.svc, c.heldCard))
}

// TestACaptureThatWonTheRaceIsLeftAndTheRestClosed: a session listed as
// authorized and captured before its lock is refused by the cancel; the
// handler goes on to close the others and does not ask to be retried, since
// a retry cannot undo a capture.
func TestACaptureThatWonTheRaceIsLeftAndTheRestClosed(t *testing.T) {
	c := canceledOrderFixture(t, staleSessionStore{newFakeStore()}, orderLinks{})

	require.NoError(t, c.svc.HandleOrderCanceled(t.Context(), canceled("order_a")))

	assert.Equal(t, models.SessionCaptured, statusOf(t, c.svc, c.capturedCard))
	assert.Equal(t, models.SessionCanceled, statusOf(t, c.svc, c.heldCard))
	assert.Equal(t, models.SessionCanceled, statusOf(t, c.svc, c.transfer))
	assert.Len(t, c.errors.messages, 1, "the money on the canceled order is told to an operator")
}

// TestAnOrderWithoutAPaymentIsNothingToDo: a saga that unwound before it
// bound the order to its collection leaves nothing to close.
func TestAnOrderWithoutAPaymentIsNothingToDo(t *testing.T) {
	c := canceledOrderFixture(t, newFakeStore(), orderLinks{})

	require.NoError(t, c.svc.HandleOrderCanceled(t.Context(), canceled("order_unbound")))

	assert.Equal(t, models.SessionAuthorized, statusOf(t, c.svc, c.transfer))
}

// TestAnUnusableOrderEventIsRefused: an event naming no order, and a service
// built without links, are refused rather than taken as nothing to do.
func TestAnUnusableOrderEventIsRefused(t *testing.T) {
	c := canceledOrderFixture(t, newFakeStore(), orderLinks{})
	nameless := canceled("")

	err := c.svc.HandleOrderCanceled(t.Context(), nameless)
	require.Error(t, err)
	assert.Equal(t, service.CodeOrderEventUnusable, coreerrors.CodeOf(err))

	unlinked := canceledOrderFixture(t, newFakeStore(), nil)
	err = unlinked.svc.HandleOrderCanceled(t.Context(), canceled("order_a"))
	require.Error(t, err)
	assert.True(t, coreerrors.HasKind(err, coreerrors.KindInternal))
	assert.Equal(t, models.SessionAuthorized, statusOf(t, unlinked.svc, unlinked.transfer))
}
