package outboxrelay_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/eventbus/outbox"
	"github.com/bdrtr/gobit/internal/jobs/outboxrelay"
)

// fakeRelay stands in for the outbox store.
type fakeRelay struct {
	pending []outbox.Pending
	err     error

	gotLimit int32
	calls    int
}

// Relay hands every pending event to publish and reports the outcome.
func (f *fakeRelay) Relay(
	ctx context.Context, limit int32, publish func(context.Context, outbox.Pending) error,
) (published, failed int, err error) {
	f.calls++
	f.gotLimit = limit
	if f.err != nil {
		return 0, 0, f.err
	}

	for _, event := range f.pending {
		if publish(ctx, event) != nil {
			failed++

			continue
		}
		published++
	}

	return published, failed, nil
}

// fakeBus stands in for the event bus.
type fakeBus struct {
	err       error
	published []eventbus.Event
}

// Publish records the event and applies the scripted behavior.
func (f *fakeBus) Publish(_ context.Context, e eventbus.Event) error {
	if f.err != nil {
		return f.err
	}
	f.published = append(f.published, e)

	return nil
}

// runRelay runs one pass and returns everything it logged.
func runRelay(t *testing.T, r *fakeRelay, bus *fakeBus) (string, error) {
	t.Helper()

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	err := outboxrelay.Definition(r, bus, log).Run(context.Background())

	return buf.String(), err
}

// TestAPromisedEventIsPublished is the delivery half of the outbox.
//
// The module writes the event with its work; without this it would sit in the
// table forever and the guarantee would be a table nobody read.
func TestAPromisedEventIsPublished(t *testing.T) {
	r := &fakeRelay{pending: []outbox.Pending{
		{ID: "order.placed:order_1", Name: "order.placed", Data: map[string]any{"order_id": "order_1"}},
	}}
	bus := &fakeBus{}

	out, err := runRelay(t, r, bus)
	require.NoError(t, err)

	require.Len(t, bus.published, 1)
	assert.Equal(t, "order.placed", bus.published[0].Name)
	assert.Equal(t, "order.placed:order_1", bus.published[0].ID,
		"the id has to survive: it is what makes the row and the direct publish ONE event")
	assert.Contains(t, out, "level=INFO")
}

// TestAnEmptyOutboxStaysQUIET keeps the minute-by-minute line out of the log.
//
// A relay that says something every minute is one whose lines nobody reads,
// which is how the minute that matters gets missed.
func TestAnEmptyOutboxStaysQUIET(t *testing.T) {
	out, err := runRelay(t, &fakeRelay{}, &fakeBus{})
	require.NoError(t, err)

	assert.Contains(t, out, "level=DEBUG")
	assert.NotContains(t, out, "level=INFO")
	assert.NotContains(t, out, "level=ERROR")
}

// TestAnEventThatCouldNotBeSentIsREPORTED is why the count is worth having.
//
// Every one of these is a message somebody is waiting for. Staying silent would
// make an outage look like a quiet minute.
func TestAnEventThatCouldNotBeSentIsREPORTED(t *testing.T) {
	r := &fakeRelay{pending: []outbox.Pending{
		{ID: "order.placed:order_1", Name: "order.placed"},
	}}
	bus := &fakeBus{err: errors.New("the bus is unreachable")}

	out, err := runRelay(t, r, bus)
	require.NoError(t, err, "a failed delivery is retried, not a failed run")

	assert.Contains(t, out, "level=ERROR")
	assert.Contains(t, out, "failed=1")
}

// TestAFailedPassIsAFailedRun keeps a relay that could not read from looking
// like one with nothing to do.
func TestAFailedPassIsAFailedRun(t *testing.T) {
	r := &fakeRelay{err: errors.New("the pool is closed")}

	_, err := runRelay(t, r, &fakeBus{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not be relayed")
}

// TestTheRelayIsBounded keeps one pass from trying to send a whole backlog.
func TestTheRelayIsBounded(t *testing.T) {
	r := &fakeRelay{}

	_, err := runRelay(t, r, &fakeBus{})
	require.NoError(t, err)

	assert.Positive(t, r.gotLimit)
}

// TestDefinitionRunsOftenEnoughToMatter pins the interval's intent.
//
// What waits in the outbox is a message somebody is expecting — a confirmation
// for an order already paid for — so here the delay IS the damage, unlike the
// reporting jobs whose subjects have already been wrong for a while.
func TestDefinitionRunsOftenEnoughToMatter(t *testing.T) {
	def := outboxrelay.Definition(&fakeRelay{}, &fakeBus{}, nil)

	assert.Equal(t, outboxrelay.Name, def.Name)
	assert.LessOrEqual(t, def.Every.Minutes(), 1.0)
	assert.Positive(t, def.MaxRun)
}
