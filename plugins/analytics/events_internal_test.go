package analytics

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
)

// The subscribers' decisions, over a fake store.
//
// What a real database adds is the primary key, and that is proven in the
// integration test rather than imitated here: a fake that de-duplicated by hand
// would prove the fake's arithmetic instead of the table's constraint.

// fakeStore records what it is told.
type fakeStore struct {
	rows []eventRow
	err  error
}

// Record keeps the row.
func (f *fakeStore) Record(_ context.Context, row eventRow) error {
	if f.err != nil {
		return f.err
	}
	f.rows = append(f.rows, row)

	return nil
}

// Funnel is unused by these tests.
func (f *fakeStore) Funnel(context.Context, time.Time, time.Time) ([]funnelRow, error) {
	return nil, nil
}

// newTestModule builds a registered module over the fake store.
func newTestModule() (*funnelModule, *fakeStore) {
	store := &fakeStore{}
	mod := newFunnelModule(nil, nil)
	mod.store = store

	return mod, store
}

// cartEvent builds a well-formed cart event.
func cartEvent(topic, id, region, at string) eventbus.Event {
	return eventbus.Event{
		ID:   id,
		Name: topic,
		Data: map[string]any{
			eventFieldRegionID:   region,
			eventFieldOccurredAt: at,
		},
	}
}

// TestTheThreeTopicsBecomeRows is the decision in its shortest form.
func TestTheThreeTopicsBecomeRows(t *testing.T) {
	mod, store := newTestModule()
	ctx := context.Background()

	require.NoError(t, mod.cartCreated(ctx,
		cartEvent(eventCartCreated, "cart.created:cart_1", "reg_1", "2026-09-12T08:00:00Z")))
	require.NoError(t, mod.cartCompleted(ctx,
		cartEvent(eventCartCompleted, "cart.completed:cart_1", "reg_1", "2026-09-12T08:05:00Z")))
	require.NoError(t, mod.orderPlaced(ctx, eventbus.Event{
		ID:   "order.placed:order_1",
		Name: eventOrderPlaced,
		Data: map[string]any{
			eventFieldRegionID: "reg_1",
			// The order module named its OWN moment field; a plugin does not get
			// to rename another module's payload.
			eventFieldPlacedAt: "2026-09-12T08:06:00Z",
		},
	}))

	require.Len(t, store.rows, 3)
	assert.Equal(t, []string{eventCartCreated, eventCartCompleted, eventOrderPlaced},
		[]string{store.rows[0].Topic, store.rows[1].Topic, store.rows[2].Topic})
	assert.Equal(t, "order.placed:order_1", store.rows[2].ID,
		"the row is keyed on the EVENT's id, which is what makes a redelivery a no-op")
	assert.Equal(t, time.Date(2026, 9, 12, 8, 6, 0, 0, time.UTC), store.rows[2].OccurredAt,
		"the moment is the PUBLISHER's, not the row's write time: the relay can deliver a "+
			"minute late and a row dated by its insert would move a shop's Tuesday")
}

// TestAnEventWithNoIDIsRefused is the guard that keeps the table countable.
//
// Written with an empty key the first such event would take the primary key and
// every later one would be dropped as a redelivery of it — a funnel that stops
// counting and says nothing. The product module's events carry no id at all, so
// this is not a hypothetical shape in this tree.
func TestAnEventWithNoIDIsRefused(t *testing.T) {
	mod, store := newTestModule()

	err := mod.cartCreated(context.Background(), eventbus.Event{
		Name: eventCartCreated,
		Data: map[string]any{
			eventFieldRegionID:   "reg_1",
			eventFieldOccurredAt: "2026-09-12T08:00:00Z",
		},
	})

	require.Error(t, err)
	assert.Equal(t, codeEventInvalid, coreerrors.CodeOf(err))
	assert.Empty(t, store.rows, "nothing may be written for an event that cannot be counted once")
}

// TestAMalformedPayloadIsRefused keeps a wrong row out of the table.
//
// The counts are read as a business figure; a row with an empty region or a
// moment nobody can parse would be counted as a real cart in a group nobody can
// name.
func TestAMalformedPayloadIsRefused(t *testing.T) {
	for name, event := range map[string]eventbus.Event{
		"no region": {ID: "x", Name: eventCartCreated, Data: map[string]any{
			eventFieldOccurredAt: "2026-09-12T08:00:00Z",
		}},
		"empty region": {ID: "x", Name: eventCartCreated, Data: map[string]any{
			eventFieldRegionID: "", eventFieldOccurredAt: "2026-09-12T08:00:00Z",
		}},
		"region is not a string": {ID: "x", Name: eventCartCreated, Data: map[string]any{
			eventFieldRegionID: 7, eventFieldOccurredAt: "2026-09-12T08:00:00Z",
		}},
		"no moment": {ID: "x", Name: eventCartCreated, Data: map[string]any{
			eventFieldRegionID: "reg_1",
		}},
		"moment is not RFC 3339": {ID: "x", Name: eventCartCreated, Data: map[string]any{
			eventFieldRegionID: "reg_1", eventFieldOccurredAt: "yesterday",
		}},
	} {
		t.Run(name, func(t *testing.T) {
			mod, store := newTestModule()

			err := mod.cartCreated(context.Background(), event)

			require.Error(t, err)
			assert.Equal(t, codeEventInvalid, coreerrors.CodeOf(err))
			assert.Empty(t, store.rows)
		})
	}
}

// TestAnUnregisteredModuleRefusesRatherThanPanics pins the handler's own floor.
//
// The subscription is set up by the core and runs INDEPENDENTLY of Routes, so a
// module whose Register did not run still receives events. A nil store would
// panic inside the bus's goroutine.
func TestAnUnregisteredModuleRefusesRatherThanPanics(t *testing.T) {
	mod := newFunnelModule(nil, nil)

	err := mod.cartCreated(context.Background(),
		cartEvent(eventCartCreated, "id", "reg_1", "2026-09-12T08:00:00Z"))

	require.Error(t, err)
	assert.Equal(t, codeNotRegistered, coreerrors.CodeOf(err))
}

// TestTheOrderEventsMomentKeyIsNotTheCartsPinsTheDifference holds the two
// payloads apart.
//
// They spell the moment differently, and a handler reading the
// cart's word out of the order's event would refuse every order gobit places —
// silently, in a log line nobody reads, with the funnel's numerator stuck at
// zero.
func TestTheOrderEventsMomentKeyIsNotTheCartsPinsTheDifference(t *testing.T) {
	assert.NotEqual(t, eventFieldOccurredAt, eventFieldPlacedAt)

	mod, store := newTestModule()

	err := mod.orderPlaced(context.Background(), eventbus.Event{
		ID:   "order.placed:order_1",
		Name: eventOrderPlaced,
		// The CART's moment key, which the order module does not use.
		Data: map[string]any{
			eventFieldRegionID:   "reg_1",
			eventFieldOccurredAt: "2026-09-12T08:00:00Z",
		},
	})

	require.Error(t, err, "the order event is read with the ORDER module's field name")
	assert.Empty(t, store.rows)
}
