package eventbus

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/bdrtr/gobit/core/errors"
)

// shortRetries makes the waits between attempts negligible for one test.
func shortRetries(t *testing.T) {
	t.Helper()
	previous := handlerRetryDelays
	handlerRetryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { handlerRetryDelays = previous })
}

// countingHandler fails with the given errors in turn and then succeeds, and
// counts its calls.
func countingHandler(calls *int, errs ...error) Handler {
	return func(context.Context, Event) error {
		*calls++
		if *calls <= len(errs) {
			return errs[*calls-1]
		}
		return nil
	}
}

// TestAFailingHandlerIsCalledAgain is ADR 0240: a fault that passes on the
// third call is not lost, and nothing is logged as an error.
func TestAFailingHandlerIsCalledAgain(t *testing.T) {
	shortRetries(t)
	var buf bytes.Buffer
	calls := 0
	transient := errors.Unavailable("db_down", "the connection was reset")

	failed := invokeHandler(context.Background(), slog.New(slog.NewTextHandler(&buf, nil)),
		Event{Name: "order.placed", ID: "evt_1"}, countingHandler(&calls, transient, transient))

	if calls != 3 {
		t.Fatalf("the handler was called %d times, expected 3", calls)
	}
	if failed {
		t.Error("a handler that succeeded on its third call did not fail")
	}
	if strings.Contains(buf.String(), "level=ERROR") {
		t.Errorf("a handler that succeeded is not an error: %s", buf.String())
	}
}

// TestAHandlerThatKeepsFailingIsCalledThreeTimesAndLogged bounds the retries
// and keeps the last error in the log with the count.
func TestAHandlerThatKeepsFailingIsCalledThreeTimesAndLogged(t *testing.T) {
	shortRetries(t)
	var buf bytes.Buffer
	calls := 0
	down := errors.Unavailable("db_down", "the connection was reset")

	failed := invokeHandler(context.Background(), slog.New(slog.NewTextHandler(&buf, nil)),
		Event{Name: "order.placed", ID: "evt_2"}, countingHandler(&calls, down, down, down, down))

	if calls != 3 {
		t.Fatalf("the handler was called %d times, expected 3", calls)
	}
	if !failed {
		t.Error("a handler whose fault that may pass still stood is reported as failed, " +
			"which is what leaves its message pending during a Redis shutdown (ADR 0420)")
	}
	if !strings.Contains(buf.String(), "level=ERROR") || !strings.Contains(buf.String(), "attempts=3") {
		t.Errorf("the last error has to be logged with the attempts: %s", buf.String())
	}
}

// TestAnInvalidEventIsNotTriedAgain spares an event that cannot succeed, and
// a panic, which is a bug rather than a fault.
func TestAnInvalidEventIsNotTriedAgain(t *testing.T) {
	shortRetries(t)
	calls := 0
	failed := invokeHandler(context.Background(), quietLogger(), Event{Name: "order.placed"},
		countingHandler(&calls, errors.Invalid("event_unusable", "no order_id")))
	if calls != 1 {
		t.Errorf("an invalid event was handled %d times, expected once", calls)
	}
	if failed {
		t.Error("an invalid event is not a fault another delivery could pass")
	}

	panics := 0
	failed = invokeHandler(context.Background(), quietLogger(), Event{Name: "order.placed"},
		func(context.Context, Event) error { panics++; panic("a bug") })
	if panics != 1 {
		t.Errorf("a panicking handler was called %d times, expected once", panics)
	}
	if failed {
		t.Error("a panic is a bug, not a fault another delivery could pass")
	}
}

// TestEveryAttemptGetsItsOwnCopy keeps an attempt's writes to the event's data
// out of the next attempt's.
func TestEveryAttemptGetsItsOwnCopy(t *testing.T) {
	shortRetries(t)
	seen := []any{}
	invokeHandler(context.Background(), quietLogger(), Event{Name: "x", Data: map[string]any{"k": "original"}},
		func(_ context.Context, e Event) error {
			seen = append(seen, e.Data["k"])
			e.Data["k"] = "written"
			if len(seen) < 2 {
				return errors.Unavailable("again", "once more")
			}
			return nil
		})
	if len(seen) != 2 || seen[1] != "original" {
		t.Errorf("the second attempt saw %v, expected the original value", seen)
	}
}
