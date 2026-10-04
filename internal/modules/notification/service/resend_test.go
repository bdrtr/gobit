package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/notification/models"
	"github.com/bdrtr/gobit/internal/modules/notification/service"
)

// failedConfirmation places an order confirmation whose provider refused it,
// and has the bus call the handler again, as ADR 0240's retry does.
func failedConfirmation(t *testing.T) (*service.Service, *fakeStore, *fakeProvider, models.Delivery) {
	t.Helper()
	svc, store, prov := setupWithContacts(t, &fakeContacts{body: testOrderBody})
	prov.err = errors.Unavailable("smtp_down", "the mail server did not answer")

	require.Error(t, svc.OrderPlaced(context.Background(), orderPlacedEvent("order_01H")))
	require.NoError(t, svc.OrderPlaced(context.Background(), orderPlacedEvent("order_01H")),
		"the bus's second call finds the earlier attempt and skips it")
	records := store.allRecords()
	require.Len(t, records, 1)
	require.Equal(t, models.DeliveryFailed, records[0].Status)
	require.Equal(t, 1, prov.callCount(), "a failure is not sent again by itself")

	return svc, store, prov, records[0]
}

// TestAFailedConfirmationIsSentAgainOnAnOperatorsWord is ADR 0243: the record
// is reopened, the message rebuilt from the order and sent, and the record
// says it went.
func TestAFailedConfirmationIsSentAgainOnAnOperatorsWord(t *testing.T) {
	svc, _, prov, failed := failedConfirmation(t)
	prov.err = nil

	resent, err := svc.ResendDelivery(context.Background(), failed.ID)
	require.NoError(t, err)

	assert.Equal(t, models.DeliverySent, resent.Status)
	assert.Empty(t, resent.Error)
	assert.Equal(t, 2, prov.callCount())
	assert.Equal(t, "customer@example.com", prov.lastNotification().To, "the address is read from the order again")
	assert.Equal(t, service.TemplateOrderPlaced, prov.lastNotification().Template, "resent as a confirmation (ADR 0386)")
}

// TestAResendThatFailsAgainSaysSo keeps the record honest about a second
// failure and hands the provider's error back.
func TestAResendThatFailsAgainSaysSo(t *testing.T) {
	svc, store, _, failed := failedConfirmation(t)

	_, err := svc.ResendDelivery(context.Background(), failed.ID)
	require.Error(t, err)
	assert.Equal(t, models.DeliveryFailed, store.allRecords()[0].Status)
}

// TestOnlyAFailedConfirmationIsSentAgain refuses a sent record, which would be
// a second e-mail, and a template whose content another module holds.
func TestOnlyAFailedConfirmationIsSentAgain(t *testing.T) {
	svc, store, prov, failed := failedConfirmation(t)
	prov.err = nil
	_, err := svc.ResendDelivery(context.Background(), failed.ID)
	require.NoError(t, err)

	_, err = svc.ResendDelivery(context.Background(), failed.ID)
	require.Error(t, err, "a sent confirmation is not sent twice")
	assert.Equal(t, service.CodeNotResendable, errors.CodeOf(err))
	assert.Contains(t, err.Error(), "only a failed one", "the refusal says why, not that another resend won")
	assert.Equal(t, 2, prov.callCount(), "and nothing reached the provider")

	prov.err = errors.Unavailable("smtp_down", "down")
	require.Error(t, svc.Notify(context.Background(), service.NotifyInput{
		Template: "auth.invitation", Channel: "email", Reference: "inv_1", To: "x@example.com",
	}))
	var invitation models.Delivery
	for _, record := range store.allRecords() {
		if record.Reference == "inv_1" {
			invitation = record
		}
	}
	require.Equal(t, models.DeliveryFailed, invitation.Status)
	_, err = svc.ResendDelivery(context.Background(), invitation.ID)
	require.Error(t, err, "an invitation is resent by the module that holds its token")
	assert.Equal(t, service.CodeNotResendable, errors.CodeOf(err))
}

// TestAResendThatLosesTheRaceSendsNothing is two operators pressing at once:
// the record was failed when read and is no longer failed when reopened, and
// the loser reaches no provider, so the customer gets one e-mail.
func TestAResendThatLosesTheRaceSendsNothing(t *testing.T) {
	svc, store, prov, failed := failedConfirmation(t)
	prov.err = nil
	store.reopenLost = true

	_, err := svc.ResendDelivery(context.Background(), failed.ID)

	require.Error(t, err)
	assert.Equal(t, service.CodeNotResendable, errors.CodeOf(err))
	assert.Equal(t, 1, prov.callCount(), "only the first attempt ever reached the provider")
}

// pendingConfirmation places an order confirmation whose provider accepted it
// and whose outcome could not be written: the record stays pending, the shape
// a process that died between the send and the write leaves too.
func pendingConfirmation(t *testing.T) (*service.Service, *fakeStore, *fakeProvider, models.Delivery) {
	t.Helper()
	svc, store, prov := setupWithContacts(t, &fakeContacts{body: testOrderBody})
	store.finishErr = errors.Unavailable("db_down", "the log could not be written")

	require.NoError(t, svc.OrderPlaced(context.Background(), orderPlacedEvent("order_01H")))
	store.finishErr = nil
	records := store.allRecords()
	require.Len(t, records, 1)
	require.Equal(t, models.DeliveryPending, records[0].Status)

	return svc, store, prov, records[0]
}

// TestAConfirmationADeadAttemptLeftPendingIsSentAgain is ADR 0245: a record
// pending for longer than an attempt can live is sent again, like a failed one.
func TestAConfirmationADeadAttemptLeftPendingIsSentAgain(t *testing.T) {
	svc, store, prov, pending := pendingConfirmation(t)
	aged := store.records[pending.ID]
	aged.UpdatedAt = time.Now().Add(-time.Hour)
	store.records[pending.ID] = aged

	resent, err := svc.ResendDelivery(context.Background(), pending.ID)
	require.NoError(t, err)
	assert.Equal(t, models.DeliverySent, resent.Status)
	assert.Equal(t, 2, prov.callCount())
	assert.Equal(t, service.TemplateOrderPlaced, prov.lastNotification().Template, "resent as a confirmation (ADR 0386)")
}

// TestAnAttemptStillInFlightIsNotRaced refuses a record an attempt claimed a
// moment ago: it may be sending right now.
func TestAnAttemptStillInFlightIsNotRaced(t *testing.T) {
	svc, _, prov, pending := pendingConfirmation(t)

	_, err := svc.ResendDelivery(context.Background(), pending.ID)
	require.Error(t, err)
	assert.Equal(t, service.CodeNotResendable, errors.CodeOf(err))
	assert.Contains(t, err.Error(), "is pending; only a failed one", "the refusal says why")
	assert.Equal(t, 1, prov.callCount(), "the attempt in flight is the only one")
}

// TestAnAttemptTwentySecondsOldMayStillBeSending holds the bound: an attempt's
// call and its outcome's write are fifteen seconds each, so a record twenty
// seconds old may still be sent by it.
func TestAnAttemptTwentySecondsOldMayStillBeSending(t *testing.T) {
	svc, store, prov, pending := pendingConfirmation(t)
	aged := store.records[pending.ID]
	aged.UpdatedAt = time.Now().Add(-20 * time.Second)
	store.records[pending.ID] = aged

	_, err := svc.ResendDelivery(context.Background(), pending.ID)
	require.Error(t, err)
	assert.Equal(t, 1, prov.callCount())
}
