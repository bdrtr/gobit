//go:build integration

package e2e

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/eventbus"
	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
)

// olayBeklemeSuresi is the time granted for an event to arrive over the bus.
//
// The wait is MANDATORY and its length is not a preference but a consequence of
// the contract: [eventbus.EventBus].Publish does NOT WAIT for the handlers and the
// in-memory backend runs every handler in its own goroutine. So the event may not
// be visible in the log yet even though the order has already been written. The
// duration is generous because the purpose of the test is not to show HOW FAST the
// event arrives, but THAT it arrives.
const olayBeklemeSuresi = 5 * time.Second

// orderEventLog is the test-side record of the "order.placed" events.
//
// # Why a SINGLE, PROCESS-LIFETIME subscriber
//
// [eventbus.EventBus] offers no way to UNSUBSCRIBE — its signature deliberately
// takes no context, and a subscription is bound to the lifetime of the process.
// Subscribing per test would therefore produce a pile of handlers that accumulates
// as the run proceeds and records every event many times over. The single log is
// wired once in TestMain and the tests filter their own orders BY ID; the filtering
// makes it structurally impossible for one test to see another test's event.
//
// The type is safe for concurrent use: handlers run in separate goroutines and the
// write and the read share the same lock.
type orderEventLog struct {
	mu      sync.Mutex
	records []eventbus.Event
}

// abone wires the log to the bus.
//
// The wiring must happen BEFORE the modules come up: a subscriber attached later
// CANNOT SEE the events published before it (the in-memory backend keeps no
// history, it delivers AT MOST ONCE).
func (l *orderEventLog) abone(bus eventbus.EventBus) error {
	return bus.Subscribe(ordersvc.EventOrderPlaced, func(_ context.Context, e eventbus.Event) error {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.records = append(l.records, e)
		return nil
	})
}

// events returns the records belonging to the given order.
//
// The filtering goes over the order_id field; events that lack the field or belong
// to another order are skipped.
func (l *orderEventLog) events(orderID string) []eventbus.Event {
	l.mu.Lock()
	defer l.mu.Unlock()

	var found []eventbus.Event
	for i := range l.records {
		if value, ok := l.records[i].Data[ordersvc.EventFieldOrderID].(string); ok && value == orderID {
			found = append(found, l.records[i])
		}
	}
	return found
}

// waitFor waits for the order's event to arrive and returns the SINGLE event.
//
// Uniqueness is asserted as well: two events for the same order mean that the
// subscribers process the order twice (sending the notification twice, for
// example), and since the delivery guarantee is "at most once" this is a PUBLISH
// bug, not a repeated delivery.
func (l *orderEventLog) waitFor(t *testing.T, orderID string) eventbus.Event {
	t.Helper()

	var found []eventbus.Event
	require.Eventually(t, func() bool {
		found = l.events(orderID)
		return len(found) > 0
	}, olayBeklemeSuresi, 20*time.Millisecond,
		"the %q event must be published for order %s; if the event is not published, "+
			"subscribers such as notification, accounting and the search index stay "+
			"UNAWARE of the order and the gap is only noticed through a customer "+
			"complaint", ordersvc.EventOrderPlaced, orderID)

	require.Len(t, found, 1,
		"exactly ONE event must be published for order %s; a second event means that "+
			"every subscriber processes the same order twice", orderID)
	return found[0]
}

// olayAlani reads one field of the event's payload as a STRING.
//
// The type assertion is gathered into a separate helper because the contract
// repeats it for every field: ALL values of the payload are strings and the numeric
// ones carry a decimal string with no fractional part (see ordersvc.EventFieldTotal).
// The rule follows from the transport format of the bus — a field written as an
// int64 comes back to the subscriber as a float64 on the Redis backend. This is why
// the test asserts the type as well: were the field turned into a number, the
// subscriber in production would fall over.
func olayAlani(t *testing.T, event eventbus.Event, key string) string {
	t.Helper()

	raw, present := event.Data[key]
	require.True(t, present, "the %q field must be present in the event payload", key)

	value, ok := raw.(string)
	require.True(t, ok,
		"the %q field of the event payload must be a STRING, not %T; numeric types "+
			"resolve to a different Go type on the two backends and a subscriber "+
			"written to the contract would fall over in production",
		key, raw)
	return value
}
