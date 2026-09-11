package service_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// This file is where the "order_fulfillment" binding is tested, because ADR 0140
// is where the write moved to.
//
// It used to live in internal/workflows/fulfilling, which wrote the link after
// asking this module to open a parcel. That covered ONE of the two ways a parcel
// is opened. The other — this module's own admin endpoint, and the only one that
// carries an item breakdown — wrote nothing, so those parcels were attributable
// to no order at all and every question asked through the link answered zero
// about them.

// fakeLinkWriter records the bindings, and can refuse.
type fakeLinkWriter struct {
	mu    sync.Mutex
	bound map[string][]string
	err   error
	calls int
}

// newFakeLinkWriter builds an empty one.
func newFakeLinkWriter() *fakeLinkWriter {
	return &fakeLinkWriter{bound: map[string][]string{}}
}

// Create records the pair under the definition.
func (f *fakeLinkWriter) Create(_ context.Context, definition, fromID, toID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls++
	if f.err != nil {
		return f.err
	}
	f.bound[definition+"/"+fromID] = append(f.bound[definition+"/"+fromID], toID)

	return nil
}

// linked returns what was bound to the reference.
func (f *fakeLinkWriter) linked(reference string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.bound["order_fulfillment/"+reference]
}

// newSetupWithLinks builds a service whose bindings are recorded.
func newSetupWithLinks(t *testing.T) (testSetup, *fakeLinkWriter) {
	t.Helper()

	setup := newSetup(t)
	links := newFakeLinkWriter()

	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(setup.provider))

	svc, err := service.New(service.Options{
		Store:         setup.store,
		Providers:     registry,
		DispatchBound: setup.bound,
		Events:        setup.events,
		Links:         links,
	})
	require.NoError(t, err)
	setup.svc = svc

	return setup, links
}

// TestAParcelWithAnItemBreakdownIsBoundToItsOrder is the defect.
//
// The endpoint that carries items is this module's own, and it bound nothing.
// So `bought − canceled − in a live parcel` never subtracted the third term for
// the only parcels that HAVE a third term: a write-off put back units sitting in
// a box, and a second parcel could hold the same units again.
func TestAParcelWithAnItemBreakdownIsBoundToItsOrder(t *testing.T) {
	t.Parallel()

	setup, links := newSetupWithLinks(t)

	ful, err := setup.svc.CreateFulfillment(t.Context(), service.CreateFulfillmentInput{
		Reference:        "ord_bound",
		ShippingOptionID: readyOption(t, setup),
		IdempotencyKey:   "key_bound",
		Items:            []service.FulfillmentItemInput{{LineItemID: "oli_bound", Quantity: 3}},
	})
	require.NoError(t, err)

	assert.Equal(t, []string{ful.ID}, links.linked("ord_bound"),
		"a parcel nothing can attribute to an order is invisible to every question "+
			"asked through the link")
}

// TestABindingFailureNAMESTheParcelThatExists is the failure, and the message is
// the whole remedy.
//
// The parcel is committed by the time the binding is written. Saying only "it
// failed" would invite the operator to press again, and a fresh idempotency key
// opens a SECOND parcel.
func TestABindingFailureNAMESTheParcelThatExists(t *testing.T) {
	t.Parallel()

	setup, links := newSetupWithLinks(t)
	links.err = errors.New("the link table is unreachable")

	_, err := setup.svc.CreateFulfillment(t.Context(), service.CreateFulfillmentInput{
		Reference:        "ord_bound_fail",
		ShippingOptionID: readyOption(t, setup),
		IdempotencyKey:   "key_bound_fail",
		Items:            []service.FulfillmentItemInput{{LineItemID: "oli_bound", Quantity: 1}},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ord_bound_fail",
		"the error must name what the parcel was opened for")
	assert.Contains(t, err.Error(), "ful_",
		"and it must name the parcel that EXISTS, or the operator opens a second one")
}

// TestARETRYRewritesTheBinding is what makes a half-finished create repairable.
//
// The first attempt can commit the parcel and fail the binding. The second — the
// same key, the same request — returns the existing parcel, and writing the
// binding again is what repairs it. The link service is idempotent, so the cost
// of always writing is one statement.
func TestARETRYRewritesTheBinding(t *testing.T) {
	t.Parallel()

	setup, links := newSetupWithLinks(t)
	in := service.CreateFulfillmentInput{
		Reference:        "ord_retry",
		ShippingOptionID: readyOption(t, setup),
		IdempotencyKey:   "key_retry",
		Items:            []service.FulfillmentItemInput{{LineItemID: "oli_bound", Quantity: 1}},
	}

	links.err = errors.New("the link table is unreachable")
	_, err := setup.svc.CreateFulfillment(t.Context(), in)
	require.Error(t, err)
	require.Empty(t, links.linked("ord_retry"))

	links.err = nil
	ful, err := setup.svc.CreateFulfillment(t.Context(), in)
	require.NoError(t, err)

	assert.Equal(t, []string{ful.ID}, links.linked("ord_retry"),
		"the retry has to repair the binding, or the parcel stays orphaned forever")
}

// TestAServiceWithNoLinkWriterStillOpensParcels pins the wiring choice.
//
// The MODULE does not allow nil — it resolves the link service as a hard
// dependency — but a service assembled by hand can have none, and the safe
// reading is "write no binding" rather than "refuse to open".
func TestAServiceWithNoLinkWriterStillOpensParcels(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)

	ful, err := setup.svc.CreateFulfillment(t.Context(), service.CreateFulfillmentInput{
		Reference:        "ord_no_links",
		ShippingOptionID: readyOption(t, setup),
		IdempotencyKey:   "key_no_links",
	})

	require.NoError(t, err)
	assert.NotEmpty(t, ful.ID)
}
