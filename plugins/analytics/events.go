package analytics

import (
	"context"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
)

// This file holds the three subscribers.
//
// # The error policy: an error IS returned, and it is NOT a retry request
//
// The bus's contract is explicit: a handler that returns an error has its error
// LOGGED and the event counts as processed — no backend redelivers it. So the
// sentence "returning the error makes the bus try again" is false in this
// framework. The error is returned anyway, because swallowing it is the only
// option with a real cost: a funnel falling behind the shop would otherwise be
// visible nowhere.
//
// There is no retry inside the handler either. A handler that waits while the
// database is unreachable blocks the single consumer loop on the Redis backend
// and delays every event on the same stream; one missing row is cheaper than a
// stopped stream.
//
// The accepted price: a missed event leaves the funnel one short, and there is no
// repair path. That is a stated limit of the slice rather than an oversight — the
// rows the relay delivers cover the ordinary loss, and a reconciliation job that
// re-derived the funnel from the modules' own tables would be a second history of
// the same facts.
//
// # The handlers are IDEMPOTENT
//
// Each one is a single insert that drops a key it already holds, so processing an
// event twice gives the same table as processing it once. That is what makes the
// bus's at-least-once delivery safe to count with.

// codeEventInvalid reports that an event payload does not match the contract.
const codeEventInvalid = "analytics_event_payload_invalid"

// cartCreated records an opened cart.
func (m *funnelModule) cartCreated(ctx context.Context, event eventbus.Event) error {
	return m.record(ctx, event, eventFieldOccurredAt)
}

// cartCompleted records a completed cart.
func (m *funnelModule) cartCompleted(ctx context.Context, event eventbus.Event) error {
	return m.record(ctx, event, eventFieldOccurredAt)
}

// orderPlaced records a placed order.
//
// It reads a DIFFERENT moment key from the other two, because the order module
// named its own field ("placed_at") rather than sharing the cart's word. The
// difference is carried here, in one line, instead of being asked of the modules:
// a plugin does not get to rename another module's payload.
func (m *funnelModule) orderPlaced(ctx context.Context, event eventbus.Event) error {
	return m.record(ctx, event, eventFieldPlacedAt)
}

// record turns an event into a row.
//
// # Why an event with no id is REFUSED
//
// Because the id is what makes counting safe. Written with an empty key, the
// first such event would occupy the primary key and every later one would be
// dropped as a redelivery of it — a funnel that stops counting and says nothing.
// The three topics this plugin listens to all carry a DERIVED id (the publishing
// modules put the same one on both of their delivery paths), so an event without
// one means the contract changed, and that is worth a logged error rather than a
// silent row.
func (m *funnelModule) record(ctx context.Context, event eventbus.Event, momentKey string) error {
	if err := m.ready(); err != nil {
		return err
	}
	if event.ID == "" {
		return coreerrors.Invalid(codeEventInvalid,
			"the %q event carries no id, so it cannot be counted once; an empty key would "+
				"take the primary key and make every later event look like a redelivery",
			event.Name)
	}

	region, err := stringField(event, eventFieldRegionID)
	if err != nil {
		return err
	}
	moment, err := momentField(event, momentKey)
	if err != nil {
		return err
	}

	return m.store.Record(ctx, eventRow{
		ID:         event.ID,
		Topic:      event.Name,
		OccurredAt: moment,
		RegionID:   region,
	})
}

// stringField reads a required string out of the payload.
//
// A payload is read field by field rather than unmarshalled into a struct,
// because this plugin needs two of the eight keys "order.placed" carries and
// naming the rest would make every field the order module adds a field this
// plugin has an opinion about.
func stringField(event eventbus.Event, key string) (string, error) {
	value, found := event.Data[key]
	if !found {
		return "", coreerrors.Invalid(codeEventInvalid,
			"the %q event carries no %q field", event.Name, key)
	}
	text, ok := value.(string)
	if !ok || text == "" {
		return "", coreerrors.Invalid(codeEventInvalid,
			"the %q event's %q field is not a non-empty string", event.Name, key)
	}

	return text, nil
}

// momentField reads a required RFC 3339 moment out of the payload.
//
// Every value in these payloads is a STRING, amounts included, and that is the
// publishers' own decision: a JSON round-trip through Redis turns an int64 into a
// float64, so a typed number on the wire is a number that changes shape depending
// on the backend. The price is this parse.
func momentField(event eventbus.Event, key string) (time.Time, error) {
	text, err := stringField(event, key)
	if err != nil {
		return time.Time{}, err
	}
	moment, parseErr := time.Parse(time.RFC3339Nano, text)
	if parseErr != nil {
		return time.Time{}, coreerrors.Invalid(codeEventInvalid,
			"the %q event's %q field is not an RFC 3339 moment (%q)", event.Name, key, text)
	}

	return moment.UTC(), nil
}
