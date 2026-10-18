package stockalert_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/internal/workflows/stockalert"
)

// fakeBus records what the flow publishes, and fails while err is set.
type fakeBus struct {
	events []eventbus.Event
	err    error
}

func (b *fakeBus) Publish(_ context.Context, e eventbus.Event) error {
	if b.err != nil {
		return b.err
	}
	b.events = append(b.events, e)

	return nil
}

// TestAnAnsweredMarkIsOnTheBus is ADR 0448: the mail's answer publishes the
// customer, the variant, its product and the armed moment, under an id that
// names no customer.
func TestAnAnsweredMarkIsOnTheBus(t *testing.T) {
	t.Parallel()

	h := newHarness()
	bus := &fakeBus{}
	h.flow.WithEvents(bus)
	ctx := context.Background()

	_, _, _, err := h.flow.Pass(ctx)
	require.NoError(t, err)
	assert.Empty(t, bus.events, "arming a mark publishes nothing")

	h.catalog.inStock["var_mug"] = true
	_, _, mailed, err := h.flow.Pass(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, mailed)
	require.Len(t, bus.events, 1)
	e := bus.events[0]
	assert.Equal(t, stockalert.EventBackInStock, e.Name)
	assert.Equal(t, map[string]any{
		stockalert.EventFieldCustomerID: "cus_1", stockalert.EventFieldVariantID: "var_mug",
		stockalert.EventFieldProductID: "prod_var_mug", stockalert.EventFieldArmedAt: "2026-09-27T12:00:00Z",
	}, e.Data)
	assert.NotContains(t, e.ID, "cus_1", "the id names the alert by a digest")
}

// TestAnUnpublishedAlertStaysArmed holds the order: a bus that refuses the
// event leaves the mark armed, and the next pass publishes it under the same
// id while the mail is the notification module's repeat.
func TestAnUnpublishedAlertStaysArmed(t *testing.T) {
	t.Parallel()

	h := newHarness()
	bus := &fakeBus{err: errors.Unavailable("bus_down", "the bus is down")}
	h.flow.WithEvents(bus)
	ctx := context.Background()

	_, _, _, err := h.flow.Pass(ctx)
	require.NoError(t, err)
	h.catalog.inStock["var_mug"] = true
	_, _, mailed, err := h.flow.Pass(ctx)
	require.Error(t, err)
	assert.Zero(t, mailed)
	require.Len(t, h.customers.marks, 1, "the mark stays armed")

	bus.err = nil
	_, _, mailed, err = h.flow.Pass(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, mailed)
	require.Len(t, bus.events, 1)
	assert.Len(t, h.notifier.sent, 1, "the second send is the module's repeat of one reference, and sends nothing")
	assert.Empty(t, h.customers.marks)
}
