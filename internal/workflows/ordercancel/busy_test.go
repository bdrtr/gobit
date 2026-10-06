package ordercancel_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/internal/workflows/ordercancel"
)

// The tests here bound the flow's pauses while a parcel of the order is being
// opened (ADR 0420): what one delivery may spend, how a pause is spread, and
// that shutdown ends them.

// TestABusyDeliveryEndsBeforeTheBusTakesItOver derives the whole of one busy
// delivery from the bus's own figures: the bus calls the handler once more than
// it has retry delays and waits those delays between the calls, and every call
// may spend the flow's pauses. On the Redis backend the sum has to stay below
// the takeover idle time, or another process takes the message over while this
// one still handles it; ADR 0240 refused minutes of waiting in a handler.
func TestABusyDeliveryEndsBeforeTheBusTakesItOver(t *testing.T) {
	t.Parallel()

	var perCall time.Duration
	for _, wait := range ordercancel.DefaultBusyWaits() {
		perCall += wait
	}
	delays := eventbus.HandlerRetryDelays()
	total := perCall * time.Duration(len(delays)+1)
	for _, delay := range delays {
		total += delay
	}

	assert.LessOrEqual(t, perCall, ordercancel.BusyBudget(), "one call keeps to its budget")
	assert.Less(t, total, eventbus.DefaultClaimMinIdle,
		"a busy delivery of %s reaches the takeover idle time", total)
	assert.LessOrEqual(t, total, eventbus.DefaultClaimMinIdle*3/4,
		"a quarter of the idle time is left for the asks themselves")
	assert.Greater(t, perCall, 10*time.Second, "the pauses outlast an ordinary carrier call")
}

// TestAPauseIsSpreadOverItsUpperHalf pins the jitter: a pause is never longer
// than its nominal length, so the budget still holds, and never shorter than
// half of it, and the draw decides where in between.
func TestAPauseIsSpreadOverItsUpperHalf(t *testing.T) {
	t.Parallel()

	const wait = time.Second
	assert.Equal(t, wait/2, ordercancel.Jittered(wait, func(int64) int64 { return 0 }))
	assert.Equal(t, wait, ordercancel.Jittered(wait, func(n int64) int64 { return n }))
	assert.Equal(t, 3*wait/4, ordercancel.Jittered(wait, func(n int64) int64 { return n / 2 }))
}

// TestShutdownEndsAPauseAtOnce is a handler pausing for an hour while a parcel
// of the order is being opened: the flow's shutdown ends the pause, and the
// handler returns its busy fault, which the relay's delivery after the restart
// makes good.
func TestShutdownEndsAPauseAtOnce(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.flow.SetBusyWaits([]time.Duration{time.Hour})
	h.busyFor = 1_000

	done := make(chan error, 1)
	go func() { done <- h.flow.HandleLineCanceled(context.Background(), canceledEvent(5, 0, 2)) }()
	require.NoError(t, h.flow.Shutdown(context.Background()))

	select {
	case err := <-done:
		require.Error(t, err)
		assert.Equal(t, coreerrors.KindUnavailable, coreerrors.KindOf(err))
	case <-time.After(5 * time.Second):
		t.Fatal("the handler was still pausing after the flow shut down")
	}
	assert.Zero(t, h.inventory.returned)
}

// TestTheContainerStopsTheFlowBeforeTheBus is the production wiring: the flow
// registers itself after the bus, so the container's shutdown closes it first,
// and the bus's shutdown, which waits for its handlers, finds the one pausing
// on a busy count ended. Were the flow not registered, the bus would wait out
// the whole busy delivery and its shutdown would fail on the deadline.
func TestTheContainerStopsTheFlowBeforeTheBus(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.busyFor = 1_000
	bus := eventbus.NewInMemory(slog.New(slog.DiscardHandler))
	c := container.New(nil)
	for name, service := range map[string]any{
		ordercancel.ServiceEventBus:    bus,
		ordercancel.ServiceInventory:   h.inventory,
		ordercancel.ServiceFulfillment: h,
		ordercancel.ServiceOrder:       h,
		ordercancel.ServiceLink:        h,
	} {
		require.NoError(t, c.Provide(name, service))
	}
	_, err := ordercancel.FromContainer(c, slog.New(slog.DiscardHandler))
	require.NoError(t, err)

	require.NoError(t, bus.Publish(context.Background(), canceledEvent(5, 0, 2)))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	assert.NoError(t, c.Shutdown(ctx), "the bus found no handler still pausing")
}
