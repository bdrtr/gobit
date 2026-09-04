package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/modules/notification/models"
	"github.com/bdrtr/gobit/internal/modules/notification/service"
)

// testInput is a typical order confirmation notification.
func testInput() service.NotifyInput {
	return service.NotifyInput{
		Template:  service.TemplateOrderPlaced,
		Channel:   coreprovider.ChannelEmail,
		Reference: "order_TEST",
		To:        "customer@example.com",
		Data:      map[string]string{"order_id": "order_TEST", "total": "6100"},
	}
}

// setup produces a service running with a fake store, registry and provider.
func setup(t *testing.T) (*service.Service, *fakeStore, *fakeProvider) {
	t.Helper()

	store := newFakeStore()
	prov := newFakeProvider("test")
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(prov))

	svc, err := newService(store, registry, prov.ID(), &fakeContacts{})
	require.NoError(t, err)

	return svc, store, prov
}

// TestNotifySendsAndWritesIntoTheLog verifies that the happy path both reaches
// the provider and writes "sent" into the log.
//
// The two are verified together because they can be right separately and wrong
// together: a notification that was sent but not recorded would be sent A
// SECOND TIME on the next event.
func TestNotifySendsAndWritesIntoTheLog(t *testing.T) {
	svc, store, prov := setup(t)

	require.NoError(t, svc.Notify(context.Background(), testInput()))

	require.Equal(t, 1, prov.callCount())
	sent := prov.lastNotification()
	assert.Equal(t, "customer@example.com", sent.To,
		"the address has to reach the provider UNCORRUPTED")
	assert.Equal(t, service.TemplateOrderPlaced, sent.Template)
	assert.Equal(t, coreprovider.ChannelEmail, sent.Channel)
	assert.Equal(t, "6100", sent.Data["total"], "the template data has to travel as a string")

	records := store.allRecords()
	require.Len(t, records, 1)
	assert.Equal(t, models.DeliverySent, records[0].Status)
	assert.Empty(t, records[0].Error)
	assert.Equal(t, "order_TEST", records[0].Reference)
	assert.Equal(t, "test", records[0].ProviderID,
		"the record has to write which provider was tried")
}

// TestNotifyDOESNOTSendTheSameTemplateAndReferenceASecondTime verifies the only
// thing idempotency rests on.
//
// The event bus does not redeliver today, but republishing an event by hand is
// possible; in that case the customer must not receive a second confirmation
// e-mail for the same order. The provider's call COUNT is the only proof of
// this: looking at the record count would not have been enough, because a
// second send could be made without opening a record too.
func TestNotifyDOESNOTSendTheSameTemplateAndReferenceASecondTime(t *testing.T) {
	svc, store, prov := setup(t)
	ctx := context.Background()

	require.NoError(t, svc.Notify(ctx, testInput()))
	require.NoError(t, svc.Notify(ctx, testInput()),
		"the second call has to be a silent skip, NOT AN ERROR")

	assert.Equal(t, 1, prov.callCount(), "the provider has to be reached ONLY once")
	assert.Len(t, store.allRecords(), 1, "there has to be a single record in the log")
}

// TestNotifyADifferentReferenceIsASeparateSend verifies that the uniqueness is
// over the PAIR and not over the template alone.
//
// A rule that looked only at the template would have meant no order after the
// first one getting a confirmation.
func TestNotifyADifferentReferenceIsASeparateSend(t *testing.T) {
	svc, _, prov := setup(t)
	ctx := context.Background()

	first := testInput()
	second := testInput()
	second.Reference = "order_OTHER"

	require.NoError(t, svc.Notify(ctx, first))
	require.NoError(t, svc.Notify(ctx, second))

	assert.Equal(t, 2, prov.callCount())
}

// TestNotifyWritesFailedOnAProviderError verifies that the provider's error is
// both written into the log and returned to the caller.
//
// Had the error been SWALLOWED, the notification that did not go out would only
// be visible to somebody looking at the table; the returned error is logged at
// the ERROR level by the event bus.
func TestNotifyWritesFailedOnAProviderError(t *testing.T) {
	svc, store, prov := setup(t)
	prov.err = errors.Unavailable("smtp_down", "the provider could not be reached")

	err := svc.Notify(context.Background(), testInput())

	require.Error(t, err)
	assert.Equal(t, service.CodeSendFailed, errors.CodeOf(err))

	records := store.allRecords()
	require.Len(t, records, 1)
	assert.Equal(t, models.DeliveryFailed, records[0].Status)
	assert.Contains(t, records[0].Error, "the provider could not be reached",
		"the provider's message has to be written into the record for diagnosis")
}

// TestNotifyDOESNOTRESENDAfterAFailedRecord verifies that triggering a failed
// attempt a second time is skipped as well.
//
// "Retry if it failed" looks intuitively right but it is wrong: the core
// contract says that returning an error does not mean the notification DID NOT
// GO OUT (a request that timed out may have been processed on the other side),
// that is, an automatic retry can produce a duplicate e-mail.
func TestNotifyDOESNOTRESENDAfterAFailedRecord(t *testing.T) {
	svc, store, prov := setup(t)
	ctx := context.Background()

	prov.err = errors.Unavailable("smtp_down", "the provider could not be reached")
	require.Error(t, svc.Notify(ctx, testInput()))

	prov.err = nil
	require.NoError(t, svc.Notify(ctx, testInput()),
		"the second call has to be skipped, it must not return an error")

	assert.Equal(t, 1, prov.callCount(),
		"the provider has to be reached only on the FIRST attempt; a failed record does not open a resend")
	records := store.allRecords()
	require.Len(t, records, 1)
	assert.Equal(t, models.DeliveryFailed, records[0].Status, "the record has to stay failed")
}

// TestNotifySkipsASendWithoutAnAddress verifies that a notification without an
// address produces a skipped record and NOT an error.
//
// The caller is an event handler and for it "there is no address" is a
// PERMANENT state; returning an error would make it indistinguishable from a
// fault that will be retried.
func TestNotifySkipsASendWithoutAnAddress(t *testing.T) {
	svc, store, prov := setup(t)

	input := testInput()
	input.To = ""

	require.NoError(t, svc.Notify(context.Background(), input))

	assert.Equal(t, 0, prov.callCount(), "with no address the provider must NOT be reached AT ALL")
	records := store.allRecords()
	require.Len(t, records, 1,
		"the skip has to be written into the log too; otherwise 'why did it not go out' stays unanswered")
	assert.Equal(t, models.DeliverySkipped, records[0].Status)
	assert.NotContains(t, records[0].Error, "@", "the explanation must not carry the address")
}

// TestNotifyAnUnknownProviderDOESNOTOpenARecord verifies that the provider is
// resolved BEFORE the record.
//
// Had the order been the other way round, a misconfigured setup would consume
// the idempotency key and after the configuration was fixed that notification
// could NEVER be sent again.
func TestNotifyAnUnknownProviderDOESNOTOpenARecord(t *testing.T) {
	store := newFakeStore()
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(newFakeProvider("log")))

	svc, err := newService(store, registry, "sendgrid", &fakeContacts{})
	require.NoError(t, err)

	err = svc.Notify(context.Background(), testInput())

	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "error: %v", err)
	assert.Equal(t, 0, store.claimCount, "the record must not even be attempted")
	assert.Empty(t, store.allRecords())
}

// TestNotifyRefusesInvalidInput verifies that the required fields are
// validated. A record without a reference would be written without half of the
// idempotency key.
func TestNotifyRefusesInvalidInput(t *testing.T) {
	svc, store, _ := setup(t)

	tests := map[string]func(in *service.NotifyInput){
		"without a template":  func(in *service.NotifyInput) { in.Template = "  " },
		"without a channel":   func(in *service.NotifyInput) { in.Channel = "" },
		"without a reference": func(in *service.NotifyInput) { in.Reference = "" },
	}

	for name, breakIt := range tests {
		t.Run(name, func(t *testing.T) {
			input := testInput()
			breakIt(&input)

			err := svc.Notify(context.Background(), input)

			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "error: %v", err)
		})
	}

	assert.Empty(t, store.allRecords(), "invalid input must not touch the log at all")
}

// TestNotifyCountsTheSendSUCCESSFULWhenTheOutcomeCannotBeWritten verifies that
// a send error is NOT INVENTED in the case where the outcome cannot be written.
//
// At this point the provider has already been reached; returning an error would
// be showing a notification that was sent as if it were "failed". The record
// staying 'pending' is the only sign that tells the real state ("it was sent
// but its outcome could not be written").
func TestNotifyCountsTheSendSUCCESSFULWhenTheOutcomeCannotBeWritten(t *testing.T) {
	svc, store, prov := setup(t)
	store.finishErr = errors.Unavailable("db_down", "there is no database")

	err := svc.Notify(context.Background(), testInput())

	require.NoError(t, err, "the send happened; a write error does not make it a failure")
	assert.Equal(t, 1, prov.callCount())

	records := store.allRecords()
	require.Len(t, records, 1)
	assert.Equal(t, models.DeliveryPending, records[0].Status,
		"a record whose outcome could not be written has to stay 'pending'; that is the trace of the fault")
}

// TestNotifyDOESNOTSendWhenTheRecordCannotBeOpened verifies that a notification
// whose record cannot be written does NOT reach the provider AT ALL.
//
// A send without a record would disable the only protection that stops a
// duplicate notification: the next trigger would send the same e-mail a second
// time.
func TestNotifyDOESNOTSendWhenTheRecordCannotBeOpened(t *testing.T) {
	svc, store, prov := setup(t)
	store.claimErr = errors.Unavailable("db_down", "there is no database")

	err := svc.Notify(context.Background(), testInput())

	require.Error(t, err)
	assert.Equal(t, 0, prov.callCount(), "if the record could not be opened the provider must not be reached")
}
