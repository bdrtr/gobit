package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/notification/models"
	"github.com/bdrtr/gobit/internal/modules/notification/service"
)

// A completed order is mailed to the order's own address unless the provider
// says it holds no copy for it (ADR 0386).

// orderCompletedEvent produces the payload of the "order.completed" event: the
// order and the moment, and no address.
func orderCompletedEvent(orderID string) eventbus.Event {
	return eventbus.Event{
		ID:   "order.completed:" + orderID,
		Name: service.EventOrderCompleted,
		Data: map[string]any{
			"order_id":     orderID,
			"completed_at": "2026-10-05T10:00:00Z",
		},
	}
}

// holdingProvider is a fake provider that answers which templates it holds, as
// the SMTP provider does.
type holdingProvider struct {
	*fakeProvider
	holds map[string]bool
}

func (p *holdingProvider) HoldsTemplate(name string) bool { return p.holds[name] }

var _ coreprovider.TemplateHolder = (*holdingProvider)(nil)

// setupWithHolder produces a service whose provider holds exactly the given
// templates.
func setupWithHolder(t *testing.T, templates ...string) (*service.Service, *fakeStore, *holdingProvider) {
	t.Helper()

	prov := &holdingProvider{fakeProvider: newFakeProvider("holder"), holds: map[string]bool{}}
	for _, name := range templates {
		prov.holds[name] = true
	}
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(prov))
	store := newFakeStore()
	svc, err := newService(store, registry, prov.ID(), &fakeContacts{body: testOrderBody})
	require.NoError(t, err)

	return svc, store, prov
}

// TestACompletionIsMailedToTheOrdersAddress: the address and the data come
// from the order record, and the template and the reference name the
// completion of this order.
func TestACompletionIsMailedToTheOrdersAddress(t *testing.T) {
	svc, store, prov := setupWithHolder(t, service.TemplateOrderCompleted)

	require.NoError(t, svc.OrderCompleted(context.Background(), orderCompletedEvent("order_01H")))

	require.Equal(t, 1, prov.callCount())
	sent := prov.lastNotification()
	assert.Equal(t, "customer@example.com", sent.To, "the address comes FROM THE RECORD")
	assert.Equal(t, coreprovider.ChannelEmail, sent.Channel)
	assert.Equal(t, "order.completed", sent.Template)
	assert.Equal(t, map[string]string{
		"order_id":      "order_01H",
		"display_id":    "1042",
		"currency_code": "TRY",
		"total":         "6100",
		"item_count":    "2",
	}, sent.Data, "the completion notice carries the confirmation's data")

	records := store.allRecords()
	require.Len(t, records, 1)
	assert.Equal(t, "order_01H", records[0].Reference)
	assert.Equal(t, "order.completed", records[0].Template)
	assert.Equal(t, models.DeliverySent, records[0].Status)
}

// TestTheCompletionTemplateIsNamedAfterItsEvent pins the cross-module name the
// order module publishes.
func TestTheCompletionTemplateIsNamedAfterItsEvent(t *testing.T) {
	assert.Equal(t, "order.completed", service.EventOrderCompleted)
	assert.Equal(t, service.EventOrderCompleted, service.TemplateOrderCompleted)
}

// TestACompletionIsMailedBesideItsConfirmation: the order's confirmation does
// not stand in for its completion notice in the delivery log.
func TestACompletionIsMailedBesideItsConfirmation(t *testing.T) {
	svc, store, prov := setupWithHolder(t, service.TemplateOrderPlaced, service.TemplateOrderCompleted)
	ctx := context.Background()

	require.NoError(t, svc.OrderPlaced(ctx, orderPlacedEvent("order_01H")))
	require.NoError(t, svc.OrderCompleted(ctx, orderCompletedEvent("order_01H")))

	assert.Equal(t, 2, prov.callCount(), "the confirmation and the completion notice")
	templates := map[string]bool{}
	for _, record := range store.allRecords() {
		templates[record.Template] = true
	}
	assert.Equal(t, map[string]bool{"order.placed": true, "order.completed": true}, templates)
}

// TestAProviderWithoutTheCopyIsNotAskedToMailACompletion: a provider that says
// it holds no completion template is not reached, and no record is opened, so
// no failed row stands for every completed order of an upgraded installation.
func TestAProviderWithoutTheCopyIsNotAskedToMailACompletion(t *testing.T) {
	svc, store, prov := setupWithHolder(t)

	require.NoError(t, svc.OrderCompleted(context.Background(), orderCompletedEvent("order_01H")))

	assert.Equal(t, 0, prov.callCount())
	assert.Empty(t, store.allRecords())
}

// TestAProviderHoldingOnlyTheConfirmationMailsNoCompletion: the question asked
// is about the completion's own template.
func TestAProviderHoldingOnlyTheConfirmationMailsNoCompletion(t *testing.T) {
	svc, store, prov := setupWithHolder(t, service.TemplateOrderPlaced)

	require.NoError(t, svc.OrderCompleted(context.Background(), orderCompletedEvent("order_01H")))

	assert.Equal(t, 0, prov.callCount())
	assert.Empty(t, store.allRecords())
}

// TestAProviderThatCannotSayStillMails: a provider that does not answer the
// question is asked to send, as for every other template.
func TestAProviderThatCannotSayStillMails(t *testing.T) {
	svc, store, prov := setupWithContacts(t, &fakeContacts{body: testOrderBody})

	require.NoError(t, svc.OrderCompleted(context.Background(), orderCompletedEvent("order_01H")))

	require.Equal(t, 1, prov.callCount())
	assert.Equal(t, "order.completed", prov.lastNotification().Template)
	assert.Len(t, store.allRecords(), 1)
}

// TestASecondCompletionEventMailsOnce: the outbox delivers every event twice,
// and the delivery log makes the two one mail.
func TestASecondCompletionEventMailsOnce(t *testing.T) {
	svc, store, prov := setupWithHolder(t, service.TemplateOrderCompleted)
	ctx := context.Background()
	event := orderCompletedEvent("order_01H")

	require.NoError(t, svc.OrderCompleted(ctx, event))
	require.NoError(t, svc.OrderCompleted(ctx, event))

	assert.Equal(t, 1, prov.callCount(), "the customer gets one completion notice")
	assert.Len(t, store.allRecords(), 1)
}

// TestAConfirmationIsAttemptedWithoutItsCopy: the copy check is the
// completion's alone; a confirmation the provider has no copy for is still
// attempted, and its failed record is what an operator's resend repairs.
func TestAConfirmationIsAttemptedWithoutItsCopy(t *testing.T) {
	svc, store, prov := setupWithHolder(t)
	prov.err = errors.Invalid("smtp_unknown_template", "no template named order.placed")

	require.Error(t, svc.OrderPlaced(context.Background(), orderPlacedEvent("order_01H")))

	assert.Equal(t, 1, prov.callCount())
	records := store.allRecords()
	require.Len(t, records, 1)
	assert.Equal(t, models.DeliveryFailed, records[0].Status)
}

// TestAFailedCompletionMailIsSentAgain: an operator resends a failed completion
// notice as a completion notice, and the panel offers it (ADR 0243, ADR 0386).
func TestAFailedCompletionMailIsSentAgain(t *testing.T) {
	svc, store, prov := setupWithHolder(t, service.TemplateOrderCompleted)
	prov.err = errors.Unavailable("smtp_down", "the mail server did not answer")
	require.Error(t, svc.OrderCompleted(context.Background(), orderCompletedEvent("order_01H")))
	records := store.allRecords()
	require.Len(t, records, 1)
	require.Equal(t, models.DeliveryFailed, records[0].Status)

	rows, _ := panelRows(t, service.NewAdminSurface(svc), "failed", "")
	require.Len(t, rows, 1)
	assert.True(t, rows[0].Resendable, "a failed completion notice is sent again here")

	prov.err = nil
	resent, err := svc.ResendDelivery(context.Background(), records[0].ID)
	require.NoError(t, err)

	assert.Equal(t, models.DeliverySent, resent.Status)
	assert.Equal(t, 2, prov.callCount())
	assert.Equal(t, "order.completed", prov.lastNotification().Template, "resent as itself, not as a confirmation")
	assert.Equal(t, "customer@example.com", prov.lastNotification().To)
}
