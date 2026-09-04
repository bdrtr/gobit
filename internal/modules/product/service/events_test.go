package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// This file tests the CONTRACT of the catalog events: the names, the fields of
// the payload, the type of the values and that a publish failure does not fail
// the write.
//
// The events do not show up in the service's return value (Publish does not
// wait for the handlers), so the only evidence is the fake bus.

// eventFixture is the shared setup of the event tests.
type eventFixture struct {
	svc *service.Service
	bus *fakeBus
}

// newEventFixture builds a service with a bus wired in.
func newEventFixture(t *testing.T) eventFixture {
	t.Helper()

	bus := newFakeBus()
	return eventFixture{svc: newServiceWithBus(t, newMemStore(), newFakeLinker(), nil, bus), bus: bus}
}

// TestProductEventsArePublished verifies that all three catalog events are
// published and that their payload carries the fields in the contract.
func TestProductEventsArePublished(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fx := newEventFixture(t)

	product, err := fx.svc.CreateProduct(ctx, service.CreateProductInput{
		Title:  "Shirt",
		Status: models.StatusDraft,
	})
	require.NoError(t, err)

	created := fx.bus.byName(service.EventProductCreated)
	require.Len(t, created, 1, "exactly one event should be published when the product is written")
	assert.Equal(t, product.ID, created[0].Data[service.EventFieldProductID])
	assert.Equal(t, models.StatusDraft.String(), created[0].Data[service.EventFieldStatus],
		"the event should carry the status the product had AT THE MOMENT IT WAS WRITTEN")

	// The payload is NARROW: the title, the handle and the variants are NOT in
	// the event. Putting them there would turn the event into a second copy of
	// the record.
	assert.NotContains(t, created[0].Data, "title")
	assert.NotContains(t, created[0].Data, "handle")
	assert.NotContains(t, created[0].Data, "variants")

	_, err = fx.svc.UpdateProduct(ctx, product.ID, service.UpdateProductInput{
		Status: ptr(models.StatusPublished),
	})
	require.NoError(t, err)

	updated := fx.bus.byName(service.EventProductUpdated)
	require.Len(t, updated, 1)
	assert.Equal(t, product.ID, updated[0].Data[service.EventFieldProductID])
	assert.Equal(t, models.StatusPublished.String(), updated[0].Data[service.EventFieldStatus],
		"the update event should carry the NEW status; the indexing decision looks at it")

	require.NoError(t, fx.svc.DeleteProduct(ctx, product.ID))

	deleted := fx.bus.byName(service.EventProductDeleted)
	require.Len(t, deleted, 1)
	assert.Equal(t, product.ID, deleted[0].Data[service.EventFieldProductID])
	// The delete event carries NO status: a soft deleted record is returned by
	// no read, so the subscriber cannot verify the value and the "drop it from
	// the index" action does not look at the status anyway.
	assert.NotContains(t, deleted[0].Data, service.EventFieldStatus)

	// The event names are a cross-module contract and on Redis they are the
	// stream names.
	assert.Equal(t, "product.created", service.EventProductCreated)
	assert.Equal(t, "product.updated", service.EventProductUpdated)
	assert.Equal(t, "product.deleted", service.EventProductDeleted)
}

// TestProductEventPayloadKeepsItsTypeThroughJSON verifies that the payload does
// not change TYPE when it goes through the production bus.
//
// The Redis Streams backend in production writes Data with json.Marshal and
// decodes it into a map[string]any on read; because JSON has a single number
// type, a field set as an int64 reaches the subscriber as a float64. The full
// rationale is in internal/modules/order/service/events.go. The test imitates
// that conversion and stops a numeric field added to the payload later (variant
// count, version) from silently breaking the rule.
func TestProductEventPayloadKeepsItsTypeThroughJSON(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fx := newEventFixture(t)

	product, err := fx.svc.CreateProduct(ctx, service.CreateProductInput{Title: "Trousers"})
	require.NoError(t, err)
	require.NoError(t, fx.svc.DeleteProduct(ctx, product.ID))

	events := fx.bus.events()
	require.NotEmpty(t, events)

	for _, event := range events {
		raw, err := json.Marshal(event.Data)
		require.NoError(t, err)
		var delivered map[string]any
		require.NoError(t, json.Unmarshal(raw, &delivered))

		require.NotEmpty(t, delivered, "the payload of the %q event should not be empty", event.Name)
		for key, value := range delivered {
			assert.IsType(t, "", value,
				"the %q field of the %q event should stay a string through the bus", key, event.Name)
		}
	}
}

// TestProductStaysWrittenWhenEventPublishFails verifies that a publish failure
// does not fail the catalog write.
//
// The decision is deliberate: the product is the RECORD, the event is the
// announcement. Returning an error would tell the caller "the change was not
// applied" — whereas it was, and the caller's retry would produce a second
// product or a handle conflict.
func TestProductStaysWrittenWhenEventPublishFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fx := newEventFixture(t)
	fx.bus.failErr = errors.Unavailable("eventbus_publish_failed", "the event bus is unreachable")

	product, err := fx.svc.CreateProduct(ctx, service.CreateProductInput{Title: "Coat"})
	require.NoError(t, err, "an event publish failure should not fail the product creation")

	_, err = fx.svc.UpdateProduct(ctx, product.ID, service.UpdateProductInput{Title: ptr("Overcoat")})
	require.NoError(t, err, "an event publish failure should not fail the update")

	fetched, err := fx.svc.GetProduct(ctx, product.ID)
	require.NoError(t, err, "the product should have been written")
	assert.Equal(t, "Overcoat", fetched.Title, "the update should have been applied")

	require.NoError(t, fx.svc.DeleteProduct(ctx, product.ID), "an event publish failure should not fail the delete")
	_, err = fx.svc.GetProduct(ctx, product.ID)
	assert.True(t, errors.IsNotFound(err), "the delete should have been applied")
}

// TestWritesWorkWithoutAnEventBus verifies that a service built without a bus
// works on all of its write paths.
//
// This path is ONLY for embedded use and for tests — the module's Register
// resolves the bus from the container and fails startup if it cannot find it.
// It is tested all the same: a publish that panics on a nil bus would fail every
// call that uses the service embedded.
func TestWritesWorkWithoutAnEventBus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newService(t, newMemStore(), newFakeLinker(), nil)

	product, err := svc.CreateProduct(ctx, service.CreateProductInput{Title: "Hat"})
	require.NoError(t, err)
	_, err = svc.UpdateProduct(ctx, product.ID, service.UpdateProductInput{Title: ptr("Beanie")})
	require.NoError(t, err)
	require.NoError(t, svc.DeleteProduct(ctx, product.ID))
}
