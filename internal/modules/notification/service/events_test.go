package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/eventbus"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/modules/notification/models"
	"github.com/bdrtr/gobit/internal/modules/notification/service"
)

// testOrderBody is the body the "order.interop" surface returns.
//
// ALL the values are strings; that is the contract of the surface, and writing
// the body out by hand here is deliberate (see [fakeContacts]).
const testOrderBody = `{
	"order_id":      "order_01H",
	"display_id":    "1042",
	"email":         "customer@example.com",
	"currency_code": "TRY",
	"total":         "6100",
	"item_count":    "2"
}`

// orderPlacedEvent produces the payload of the "order.placed" event.
//
// There is NO E-MAIL in the payload and that is what this test rests on: the
// subscriber IS OBLIGED to read the address from the record and not from the
// event.
func orderPlacedEvent(orderID string) eventbus.Event {
	return eventbus.Event{
		Name: service.EventOrderPlaced,
		Data: map[string]any{
			"order_id":      orderID,
			"display_id":    "1042",
			"status":        "pending",
			"currency_code": "TRY",
			"total":         "6100",
			"item_count":    "2",
		},
	}
}

// setupWithContacts produces a service with a fake contact surface.
func setupWithContacts(t *testing.T, contacts *fakeContacts) (*service.Service, *fakeStore, *fakeProvider) {
	t.Helper()

	store := newFakeStore()
	prov := newFakeProvider("test")
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(prov))

	svc, err := newService(store, registry, prov.ID(), contacts)
	require.NoError(t, err)

	return svc, store, prov
}

// TestOrderPlacedReadsTheEmailFromTheRECORDNotTheEVENT verifies that the
// subscriber reads the address from the order.
//
// The event payload CARRIES no personal data (events are written to Redis and
// are durable there); that is why the subscriber takes only the order
// identifier from the event and reads the rest over "order.interop". The test
// pins this from two sides: the reading REALLY happens, and the address that
// goes to the provider comes from that reading.
func TestOrderPlacedReadsTheEmailFromTheRECORDNotTheEVENT(t *testing.T) {
	contacts := &fakeContacts{body: testOrderBody}
	svc, store, prov := setupWithContacts(t, contacts)

	require.NoError(t, svc.OrderPlaced(context.Background(), orderPlacedEvent("order_01H")))

	require.Equal(t, 1, contacts.calls, "the order record has to be read")
	assert.Equal(t, "order_01H", contacts.requested,
		"the reading has to be done with the identifier from the event")

	require.Equal(t, 1, prov.callCount())
	sent := prov.lastNotification()
	assert.Equal(t, "customer@example.com", sent.To, "the address has to come FROM THE RECORD")
	assert.Equal(t, coreprovider.ChannelEmail, sent.Channel)
	assert.Equal(t, service.TemplateOrderPlaced, sent.Template)
	assert.Equal(t, map[string]string{
		"order_id":      "order_01H",
		"display_id":    "1042",
		"currency_code": "TRY",
		"total":         "6100",
		"item_count":    "2",
	}, sent.Data, "the template data has to come from the order and ALL its values have to be strings")

	records := store.allRecords()
	require.Len(t, records, 1)
	assert.Equal(t, "order_01H", records[0].Reference)
	assert.Equal(t, service.TemplateOrderPlaced, records[0].Template)
}

// TestOrderPlacedTemplateNameIsTheSameAsTheEventName verifies that the template
// and the event name do not diverge.
//
// If the two diverge, the question "which event triggers which template" can
// only be answered by reading the code; on top of that the template name is
// half of the idempotency key and its changing means the notification becoming
// sendable a second time for ALL orders.
func TestOrderPlacedTemplateNameIsTheSameAsTheEventName(t *testing.T) {
	assert.Equal(t, service.EventOrderPlaced, service.TemplateOrderPlaced)
	assert.Equal(t, "order.placed", service.EventOrderPlaced,
		"the event name has to be the same as the name the order module publishes")
}

// TestOrderPlacedProcessingTheSameEventTwiceSendsOneNotification verifies the
// protection against the fact that the event bus GIVES NO ordering and
// uniqueness guarantee.
//
// The handler is obliged to be idempotent, and the uniqueness rests not on the
// code but on the (template, reference) uniqueness in the delivery log.
func TestOrderPlacedProcessingTheSameEventTwiceSendsOneNotification(t *testing.T) {
	contacts := &fakeContacts{body: testOrderBody}
	svc, store, prov := setupWithContacts(t, contacts)
	ctx := context.Background()
	event := orderPlacedEvent("order_01H")

	require.NoError(t, svc.OrderPlaced(ctx, event))
	require.NoError(t, svc.OrderPlaced(ctx, event))

	assert.Equal(t, 1, prov.callCount(), "the customer MUST NOT get a second confirmation e-mail")
	assert.Len(t, store.allRecords(), 1)
}

// TestOrderPlacedSkipsAnOrderWithoutAnEmail verifies that an order without an
// address produces not an error but a skipped record.
func TestOrderPlacedSkipsAnOrderWithoutAnEmail(t *testing.T) {
	contacts := &fakeContacts{body: `{"order_id":"order_01H","display_id":"7","email":"",` +
		`"currency_code":"TRY","total":"100","item_count":"1"}`}
	svc, store, prov := setupWithContacts(t, contacts)

	require.NoError(t, svc.OrderPlaced(context.Background(), orderPlacedEvent("order_01H")))

	assert.Equal(t, 0, prov.callCount())
	records := store.allRecords()
	require.Len(t, records, 1)
	assert.Equal(t, models.DeliverySkipped, records[0].Status)
}

// TestOrderPlacedRejectsABrokenEventPayload verifies that a violation of the
// event contract does not stay silent.
//
// A numeric order_id would arrive as a float64 on the Redis backend; carrying
// on silently would produce a notification attempt for an order that never
// existed.
func TestOrderPlacedRejectsABrokenEventPayload(t *testing.T) {
	tests := map[string]map[string]any{
		"no field":         {"display_id": "1042"},
		"not a string":     {"order_id": 42},
		"empty identifier": {"order_id": "   "},
		"nil valued":       {"order_id": nil},
		"wrong type":       {"order_id": []string{"order_01H"}},
	}

	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			contacts := &fakeContacts{body: testOrderBody}
			svc, store, prov := setupWithContacts(t, contacts)

			err := svc.OrderPlaced(context.Background(),
				eventbus.Event{Name: service.EventOrderPlaced, Data: data})

			require.Error(t, err)
			assert.Equal(t, service.CodeEventInvalid, errors.CodeOf(err))
			assert.Equal(t, 0, contacts.calls, "the order must not be read with a broken payload")
			assert.Equal(t, 0, prov.callCount())
			assert.Empty(t, store.allRecords())
		})
	}
}

// TestOrderPlacedReturnsAnErrorWhenTheOrderCannotBeRead verifies that a read
// failure is not swallowed.
//
// Returning an error is not ASKING for a redelivery (see the events.go file
// documentation); the bus logs it at the ERROR level and the notification not
// going out becomes visible.
func TestOrderPlacedReturnsAnErrorWhenTheOrderCannotBeRead(t *testing.T) {
	contacts := &fakeContacts{err: errors.NotFound("order_not_found", "there is no such order")}
	svc, store, prov := setupWithContacts(t, contacts)

	err := svc.OrderPlaced(context.Background(), orderPlacedEvent("order_MISSING"))

	require.Error(t, err)
	assert.Equal(t, service.CodeContactUnavailable, errors.CodeOf(err))
	assert.True(t, errors.IsNotFound(err), "the KIND of the error has to be preserved: %v", err)
	assert.Equal(t, 0, prov.callCount())
	assert.Empty(t, store.allRecords(),
		"an order that could not be read must not consume the idempotency key")
}

// TestOrderPlacedRejectsABrokenResponse verifies that when the schema of the
// surface changes an empty template is not silently sent.
func TestOrderPlacedRejectsABrokenResponse(t *testing.T) {
	tests := map[string]string{
		"not JSON":                   `{broken`,
		"body without an identifier": `{"email":"a@b.com"}`,
	}

	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			contacts := &fakeContacts{body: body}
			svc, _, prov := setupWithContacts(t, contacts)

			err := svc.OrderPlaced(context.Background(), orderPlacedEvent("order_01H"))

			require.Error(t, err)
			assert.Equal(t, service.CodeContactInvalid, errors.CodeOf(err))
			assert.Equal(t, 0, prov.callCount(), "a template must not be sent with an incomplete body")
		})
	}
}

// TestOrderPlacedIgnoresUnrecognizedFields verifies that a new field added to
// the surface does not drop the notification.
//
// Strict decoding would have meant every field added to order breaking ALL
// order notifications.
func TestOrderPlacedIgnoresUnrecognizedFields(t *testing.T) {
	contacts := &fakeContacts{body: `{"order_id":"order_01H","display_id":"7",` +
		`"email":"a@b.com","currency_code":"TRY","total":"100","item_count":"1",` +
		`"new_field":"added in the future"}`}
	svc, _, prov := setupWithContacts(t, contacts)

	require.NoError(t, svc.OrderPlaced(context.Background(), orderPlacedEvent("order_01H")))

	assert.Equal(t, 1, prov.callCount())
}
