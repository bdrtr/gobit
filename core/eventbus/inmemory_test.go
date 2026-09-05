package eventbus_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
)

// waitTimeout is the upper bound the tests use while waiting for an
// asynchronous delivery.
const waitTimeout = 2 * time.Second

// discardLogger returns a logger whose output is discarded.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// bufferLogger returns a logger writing into a buffer, plus the buffer.
func bufferLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

// waitSignal waits for a signal on the channel; if none arrives it fails the
// test.
func waitSignal(t *testing.T, ch <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(waitTimeout):
		t.Fatalf("timed out: %s", message)
	}
}

func TestInMemoryPublishSubscribe(t *testing.T) {
	bus := eventbus.NewInMemory(discardLogger())
	defer func() { _ = bus.Shutdown(context.Background()) }()

	got := make(chan eventbus.Event, 1)
	if err := bus.Subscribe("order.placed", func(_ context.Context, e eventbus.Event) error {
		got <- e
		return nil
	}); err != nil {
		t.Fatalf("Subscribe returned an error: %v", err)
	}

	before := time.Now().UTC()
	if err := bus.Publish(t.Context(), eventbus.Event{
		Name: "order.placed",
		Data: map[string]any{"order_id": "order_01"},
	}); err != nil {
		t.Fatalf("Publish returned an error: %v", err)
	}

	select {
	case e := <-got:
		if e.Name != "order.placed" {
			t.Errorf("Name = %q, expected %q", e.Name, "order.placed")
		}
		if e.Data["order_id"] != "order_01" {
			t.Errorf("Data[order_id] = %v, expected order_01", e.Data["order_id"])
		}
		if !strings.HasPrefix(e.ID, "evt_") {
			t.Errorf("ID = %q, expected a generated id with the evt_ prefix", e.ID)
		}
		if e.OccurredAt.Before(before) {
			t.Errorf("OccurredAt = %v, it cannot be before the moment of publication (%v)", e.OccurredAt, before)
		}
	case <-time.After(waitTimeout):
		t.Fatal("timed out: the event never reached the handler")
	}
}

func TestInMemoryPublishKeepsGivenIDAndTime(t *testing.T) {
	bus := eventbus.NewInMemory(discardLogger())
	defer func() { _ = bus.Shutdown(context.Background()) }()

	when := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)
	got := make(chan eventbus.Event, 1)
	if err := bus.Subscribe("order.placed", func(_ context.Context, e eventbus.Event) error {
		got <- e
		return nil
	}); err != nil {
		t.Fatalf("Subscribe returned an error: %v", err)
	}

	if err := bus.Publish(t.Context(), eventbus.Event{
		Name:       "order.placed",
		ID:         "evt_custom",
		OccurredAt: when,
	}); err != nil {
		t.Fatalf("Publish returned an error: %v", err)
	}

	e := <-got
	if e.ID != "evt_custom" {
		t.Errorf("ID = %q, expected evt_custom (the id given by the publisher must be preserved)", e.ID)
	}
	if !e.OccurredAt.Equal(when) {
		t.Errorf("OccurredAt = %v, expected %v", e.OccurredAt, when)
	}
}

func TestInMemoryMultipleSubscribers(t *testing.T) {
	bus := eventbus.NewInMemory(discardLogger())

	const subscriberCount = 5
	var wg sync.WaitGroup
	wg.Add(subscriberCount)

	var calls atomic.Int64
	for range subscriberCount {
		if err := bus.Subscribe("order.placed", func(_ context.Context, _ eventbus.Event) error {
			calls.Add(1)
			wg.Done()
			return nil
		}); err != nil {
			t.Fatalf("Subscribe returned an error: %v", err)
		}
	}

	if err := bus.Publish(t.Context(), eventbus.Event{Name: "order.placed"}); err != nil {
		t.Fatalf("Publish returned an error: %v", err)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	waitSignal(t, done, "not every subscriber was called")

	if err := bus.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown returned an error: %v", err)
	}
	if got := calls.Load(); got != subscriberCount {
		t.Errorf("call count = %d, expected %d", got, subscriberCount)
	}
}

func TestInMemoryOtherEventsAreNotDelivered(t *testing.T) {
	bus := eventbus.NewInMemory(discardLogger())

	var calls atomic.Int64
	if err := bus.Subscribe("order.placed", func(_ context.Context, _ eventbus.Event) error {
		calls.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("Subscribe returned an error: %v", err)
	}

	// An event with no subscriber is swallowed silently, no error is returned.
	if err := bus.Publish(t.Context(), eventbus.Event{Name: "cart.updated"}); err != nil {
		t.Fatalf("Publish returned an error for an event with no subscriber: %v", err)
	}

	if err := bus.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown returned an error: %v", err)
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("call count = %d, expected 0 (a different event must not be delivered)", got)
	}
}

func TestInMemoryPublishRejectsEmptyName(t *testing.T) {
	bus := eventbus.NewInMemory(discardLogger())
	defer func() { _ = bus.Shutdown(context.Background()) }()

	err := bus.Publish(t.Context(), eventbus.Event{})
	if err == nil {
		t.Fatal("Publish returned no error for an event with an empty name")
	}
	if !errors.IsInvalid(err) {
		t.Errorf("Kind = %v, expected invalid", errors.KindOf(err))
	}
	if got := errors.CodeOf(err); got != eventbus.CodeInvalidEvent {
		t.Errorf("Code = %q, expected %q", got, eventbus.CodeInvalidEvent)
	}
}

func TestInMemorySubscribeValidates(t *testing.T) {
	bus := eventbus.NewInMemory(discardLogger())
	defer func() { _ = bus.Shutdown(context.Background()) }()

	noop := func(context.Context, eventbus.Event) error { return nil }

	if err := bus.Subscribe("", noop); !errors.IsInvalid(err) {
		t.Errorf("the error for an empty event name = %v, expected invalid", err)
	}
	if err := bus.Subscribe("order.placed", nil); !errors.IsInvalid(err) {
		t.Errorf("the error for a nil handler = %v, expected invalid", err)
	}
}

func TestInMemoryHandlerPanicDoesNotBreakBus(t *testing.T) {
	log, buf := bufferLogger()
	bus := eventbus.NewInMemory(log)

	survivor := make(chan struct{}, 2)
	if err := bus.Subscribe("order.placed", func(context.Context, eventbus.Event) error {
		panic("the handler blew up")
	}); err != nil {
		t.Fatalf("Subscribe returned an error: %v", err)
	}
	if err := bus.Subscribe("order.placed", func(context.Context, eventbus.Event) error {
		survivor <- struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("Subscribe returned an error: %v", err)
	}

	// The other subscriber of the same event must be called despite the
	// panic...
	if err := bus.Publish(t.Context(), eventbus.Event{Name: "order.placed"}); err != nil {
		t.Fatalf("Publish returned an error: %v", err)
	}
	waitSignal(t, survivor, "the subscriber next to the panicking handler was not called")

	// ...and the bus must keep working on later publications too.
	if err := bus.Publish(t.Context(), eventbus.Event{Name: "order.placed"}); err != nil {
		t.Fatalf("Publish returned an error after the panic: %v", err)
	}
	waitSignal(t, survivor, "the second publication after the panic was not delivered")

	if err := bus.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown returned an error: %v", err)
	}
	if out := buf.String(); !strings.Contains(out, "panicked") {
		t.Errorf("the panic was not logged; log output: %s", out)
	}
}

func TestInMemoryHandlerErrorIsLoggedAndIsolated(t *testing.T) {
	log, buf := bufferLogger()
	bus := eventbus.NewInMemory(log)

	healthy := make(chan struct{}, 2)
	if err := bus.Subscribe("order.placed", func(context.Context, eventbus.Event) error {
		return errors.Internal("out_of_stock", "the stock could not be reserved")
	}); err != nil {
		t.Fatalf("Subscribe returned an error: %v", err)
	}
	if err := bus.Subscribe("order.placed", func(context.Context, eventbus.Event) error {
		healthy <- struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("Subscribe returned an error: %v", err)
	}

	// A handler error does not come back to the caller; the publication
	// succeeds.
	if err := bus.Publish(t.Context(), eventbus.Event{Name: "order.placed"}); err != nil {
		t.Fatalf("Publish returned an error: %v", err)
	}
	waitSignal(t, healthy, "the subscriber next to the failing handler was not called")

	if err := bus.Publish(t.Context(), eventbus.Event{Name: "order.placed"}); err != nil {
		t.Fatalf("Publish returned an error after the failure: %v", err)
	}
	waitSignal(t, healthy, "the second publication after the failure was not delivered")

	if err := bus.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown returned an error: %v", err)
	}
	if out := buf.String(); !strings.Contains(out, "out_of_stock") {
		t.Errorf("the handler error was not logged; log output: %s", out)
	}
}

func TestInMemoryPublishDoesNotWaitForHandlers(t *testing.T) {
	bus := eventbus.NewInMemory(discardLogger())

	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	if err := bus.Subscribe("order.placed", func(context.Context, eventbus.Event) error {
		entered <- struct{}{}
		<-release
		return nil
	}); err != nil {
		t.Fatalf("Subscribe returned an error: %v", err)
	}

	start := time.Now()
	if err := bus.Publish(t.Context(), eventbus.Event{Name: "order.placed"}); err != nil {
		t.Fatalf("Publish returned an error: %v", err)
	}
	elapsed := time.Since(start)

	waitSignal(t, entered, "the handler was never called")
	if elapsed > 500*time.Millisecond {
		t.Errorf("Publish took %v; it should not have waited for the handler", elapsed)
	}

	close(release)
	if err := bus.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown returned an error: %v", err)
	}
}

// TestInMemoryHandlerContextSurvivesCallerCancel verifies that the handler ctx
// is unaffected by the caller's cancellation and keeps its values.
//
// Preserving the values is the behavior of THIS backend ONLY; under Redis the
// event crosses the process boundary and the publisher's ctx does not cross
// with it (see TestConsumeHandlerCtxCarriesNoPublisherValues). The contrast
// between the two tests is deliberate: because the default backend is this
// one, a green here does NOT on its own mean "the values are carried".
func TestInMemoryHandlerContextSurvivesCallerCancel(t *testing.T) {
	bus := eventbus.NewInMemory(discardLogger())
	defer func() { _ = bus.Shutdown(context.Background()) }()

	type ctxKey struct{}

	got := make(chan context.Context, 1)
	if err := bus.Subscribe("order.placed", func(ctx context.Context, _ eventbus.Event) error {
		got <- ctx
		return nil
	}); err != nil {
		t.Fatalf("Subscribe returned an error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), ctxKey{}, "req_01"))
	if err := bus.Publish(ctx, eventbus.Event{Name: "order.placed"}); err != nil {
		t.Fatalf("Publish returned an error: %v", err)
	}
	// The caller's request ends immediately; the side effect must not be
	// affected by that.
	cancel()

	hctx := <-got
	if err := hctx.Err(); err != nil {
		t.Errorf("the handler ctx was canceled: %v", err)
	}
	if v := hctx.Value(ctxKey{}); v != "req_01" {
		t.Errorf("the ctx value = %v, expected req_01 (the request context must be preserved)", v)
	}
}

func TestInMemoryHandlersGetIndependentData(t *testing.T) {
	bus := eventbus.NewInMemory(discardLogger())

	var wg sync.WaitGroup
	wg.Add(2)

	// Two handlers write to Data's top-level keys at the same time; without
	// the copy this would be reported as a race under -race.
	seen := make(chan any, 2)
	for range 2 {
		if err := bus.Subscribe("order.placed", func(_ context.Context, e eventbus.Event) error {
			defer wg.Done()
			e.Data["local"] = "value"
			seen <- e.Data["order_id"]
			return nil
		}); err != nil {
			t.Fatalf("Subscribe returned an error: %v", err)
		}
	}

	data := map[string]any{"order_id": "order_01"}
	if err := bus.Publish(t.Context(), eventbus.Event{Name: "order.placed", Data: data}); err != nil {
		t.Fatalf("Publish returned an error: %v", err)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	waitSignal(t, done, "the handlers did not complete")

	if err := bus.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown returned an error: %v", err)
	}
	for range 2 {
		if v := <-seen; v != "order_01" {
			t.Errorf("the order_id the handler saw = %v, expected order_01", v)
		}
	}
	if _, ok := data["local"]; ok {
		t.Error("the key the handler wrote leaked into the publisher's map")
	}
}

func TestInMemoryShutdownWaitsForRunningHandlers(t *testing.T) {
	bus := eventbus.NewInMemory(discardLogger())

	entered := make(chan struct{})
	var finished atomic.Bool
	if err := bus.Subscribe("order.placed", func(context.Context, eventbus.Event) error {
		close(entered)
		time.Sleep(100 * time.Millisecond)
		finished.Store(true)
		return nil
	}); err != nil {
		t.Fatalf("Subscribe returned an error: %v", err)
	}

	if err := bus.Publish(t.Context(), eventbus.Event{Name: "order.placed"}); err != nil {
		t.Fatalf("Publish returned an error: %v", err)
	}
	waitSignal(t, entered, "the handler never started")

	if err := bus.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown returned an error: %v", err)
	}
	if !finished.Load() {
		t.Error("Shutdown returned without waiting for the running handler")
	}
}

func TestInMemoryShutdownReturnsWhenContextExpires(t *testing.T) {
	bus := eventbus.NewInMemory(discardLogger())

	entered := make(chan struct{})
	release := make(chan struct{})
	// The handler is deliberately left stuck: it stands for a real handler
	// hanging on an HTTP call with no timeout set.
	defer close(release)

	if err := bus.Subscribe("order.placed", func(context.Context, eventbus.Event) error {
		close(entered)
		<-release
		return nil
	}); err != nil {
		t.Fatalf("Subscribe returned an error: %v", err)
	}

	if err := bus.Publish(t.Context(), eventbus.Event{Name: "order.placed"}); err != nil {
		t.Fatalf("Publish returned an error: %v", err)
	}
	waitSignal(t, entered, "the handler never started")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := bus.Shutdown(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Shutdown returned nil despite the stuck handler; the shutdown would lock forever")
	}
	if !errors.HasKind(err, errors.KindUnavailable) {
		t.Errorf("Kind = %v, expected unavailable", errors.KindOf(err))
	}
	if got := errors.CodeOf(err); got != eventbus.CodeShutdownTimeout {
		t.Errorf("Code = %q, expected %q", got, eventbus.CodeShutdownTimeout)
	}
	if elapsed > waitTimeout {
		t.Errorf("Shutdown took %v; it should have been bounded by the ctx budget", elapsed)
	}

	// Even after the timeout the bus is closed; a new publication is not
	// accepted.
	if err := bus.Publish(t.Context(), eventbus.Event{Name: "order.placed"}); !errors.HasKind(err, errors.KindUnavailable) {
		t.Errorf("the Publish error after the timeout = %v, expected unavailable", err)
	}
}

func TestInMemoryClosedBusRejectsUse(t *testing.T) {
	bus := eventbus.NewInMemory(discardLogger())
	if err := bus.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown returned an error: %v", err)
	}

	err := bus.Publish(t.Context(), eventbus.Event{Name: "order.placed"})
	if err == nil {
		t.Fatal("Publish returned no error on a closed bus")
	}
	if !errors.HasKind(err, errors.KindUnavailable) {
		t.Errorf("Publish Kind = %v, expected unavailable", errors.KindOf(err))
	}
	if got := errors.CodeOf(err); got != eventbus.CodeClosed {
		t.Errorf("Publish Code = %q, expected %q", got, eventbus.CodeClosed)
	}

	err = bus.Subscribe("order.placed", func(context.Context, eventbus.Event) error { return nil })
	if err == nil {
		t.Fatal("Subscribe returned no error on a closed bus")
	}
	if got := errors.CodeOf(err); got != eventbus.CodeClosed {
		t.Errorf("Subscribe Code = %q, expected %q", got, eventbus.CodeClosed)
	}

	// Shutdown must be callable again.
	if err := bus.Shutdown(context.Background()); err != nil {
		t.Errorf("the second Shutdown returned an error: %v", err)
	}
}

func TestInMemoryShutdownLeavesNoGoroutines(t *testing.T) {
	bus := eventbus.NewInMemory(discardLogger())

	for range 4 {
		if err := bus.Subscribe("order.placed", func(context.Context, eventbus.Event) error {
			return nil
		}); err != nil {
			t.Fatalf("Subscribe returned an error: %v", err)
		}
	}

	// The measurement point: after the subscribers are set up, just before the
	// publications.
	base := runtime.NumGoroutine()

	for range 200 {
		if err := bus.Publish(t.Context(), eventbus.Event{Name: "order.placed"}); err != nil {
			t.Fatalf("Publish returned an error: %v", err)
		}
	}

	if err := bus.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown returned an error: %v", err)
	}

	// Shutdown waits for the handlers to finish; because the goroutines dying
	// off completely depends on the scheduler, it is polled with a short
	// tolerance.
	deadline := time.Now().Add(waitTimeout)
	for {
		got := runtime.NumGoroutine()
		if got <= base {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutine leak: %d goroutines after Shutdown, expected <= %d", got, base)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestInMemoryConcurrentPublishAndShutdown(t *testing.T) {
	bus := eventbus.NewInMemory(discardLogger())

	var delivered atomic.Int64
	if err := bus.Subscribe("order.placed", func(context.Context, eventbus.Event) error {
		delivered.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("Subscribe returned an error: %v", err)
	}

	// Publish and Close run concurrently: under -race there must be no race,
	// publications after the shutdown must return errors, and the ones
	// delivered must not be lost.
	var wg sync.WaitGroup
	var rejected atomic.Int64
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 25 {
				if err := bus.Publish(t.Context(), eventbus.Event{Name: "order.placed"}); err != nil {
					rejected.Add(1)
				}
			}
		}()
	}

	time.Sleep(5 * time.Millisecond)
	if err := bus.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown returned an error: %v", err)
	}
	wg.Wait()

	accepted := int64(16*25) - rejected.Load()
	if delivered.Load() != accepted {
		t.Errorf("delivered = %d, accepted publications = %d; the shutdown dropped deliveries",
			delivered.Load(), accepted)
	}
}
