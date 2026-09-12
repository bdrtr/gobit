package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/eventbus"
)

// A command process must not take a message the server is owed (D104).
//
// The claim is about CONSUMPTION, so the fixture is a bus that records what was
// asked of it: a test asserting that Subscribe returned nil would pass against
// the real bus, which also returns nil — and then starts reading.

// recordingBus is an event bus that remembers what it was asked to do.
type recordingBus struct {
	subscribed []string
	published  []string
}

var _ eventbus.EventBus = (*recordingBus)(nil)

func (b *recordingBus) Publish(_ context.Context, event eventbus.Event) error {
	b.published = append(b.published, event.Name)

	return nil
}

func (b *recordingBus) Subscribe(eventName string, _ eventbus.Handler) error {
	b.subscribed = append(b.subscribed, eventName)

	return nil
}

func (b *recordingBus) Shutdown(context.Context) error { return nil }

// TestACommandProcessSubscribesToNothing is the whole of the fix.
//
// On the Redis bus a subscription creates the consumer group and starts a
// goroutine reading from it, and consumers in one group receive each message
// once. A command that subscribed therefore took the server's messages for as
// long as it ran, ran their handlers — the notification module's subscriber is
// registered in the same Register that subscribed, so `gobit seed` sent order
// confirmations — and left whatever it had not acknowledged in the pending list
// under a consumer name that never comes back.
func TestACommandProcessSubscribesToNothing(t *testing.T) {
	t.Parallel()

	inner := &recordingBus{}
	bus := roleBus(inner, discardLogger(), publishesOnly)

	require.NoError(t, bus.Subscribe("order.placed", func(context.Context, eventbus.Event) error {
		return nil
	}))
	require.NoError(t, bus.Subscribe("payment.captured", func(context.Context, eventbus.Event) error {
		return nil
	}))

	assert.Empty(t, inner.subscribed,
		"a command process reached the real bus's Subscribe. On Redis that creates the "+
			"consumer group and starts reading, and every message it takes is one the "+
			"server does not get")
}

// TestACommandProcessStillPublishes is the other half, and it is why the bus is
// wrapped rather than replaced.
//
// A command that writes through a service writes an outbox row in the same
// transaction and publishes directly after the commit. Swapping the bus for an
// in-memory one would have dropped that direct publish silently; the outbox
// would still carry the event, which is a repair rather than a reason.
func TestACommandProcessStillPublishes(t *testing.T) {
	t.Parallel()

	inner := &recordingBus{}
	bus := roleBus(inner, discardLogger(), publishesOnly)

	require.NoError(t, bus.Publish(context.Background(), eventbus.Event{Name: "order.placed"}))

	assert.Equal(t, []string{"order.placed"}, inner.published,
		"a command's publish did not reach the bus; the direct half of the house pattern "+
			"was dropped")
}

// TestTheServerConsumes is the direction that makes the other two mean
// something.
//
// A wrapper that refused every subscription would pass both tests above and take
// the whole event bus down with it.
func TestTheServerConsumes(t *testing.T) {
	t.Parallel()

	inner := &recordingBus{}
	bus := roleBus(inner, discardLogger(), consumesEvents)

	require.NoError(t, bus.Subscribe("order.placed", func(context.Context, eventbus.Event) error {
		return nil
	}))

	assert.Equal(t, []string{"order.placed"}, inner.subscribed,
		"the server did not reach the real bus's Subscribe; it would serve requests and "+
			"consume nothing, and every subscriber in the installation would be silent")
}

// TestOnlyTheServingPathsConsumeEvents is the population check, and it is the
// one that matters: the three above prove the wrapper works, and none of them
// says which call sites are wrapped.
//
// The population is derived from the source rather than listed here — a verb
// added next year opens the application by copying a neighbor, and the neighbor
// it copies is a command.
//
// Two call sites consume, and both serve requests: the server itself, and the
// facade's in-process harness, whose whole purpose is to answer requests in a
// test (ADR 0150). Every other call site is a verb that does one thing and
// returns.
func TestOnlyTheServingPathsConsumeEvents(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob(filepath.Join("..", "app", "*.go"))
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(files), 20,
		"only %d files were read from the composition root; the glob has gone blind",
		len(files))

	consuming := map[string]bool{}
	publishing := map[string]bool{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}

		body, readErr := os.ReadFile(file)
		require.NoError(t, readErr)

		name := filepath.Base(file)
		for _, line := range strings.Split(string(body), "\n") {
			if !strings.Contains(line, "openApplication(ctx") {
				continue
			}
			switch {
			case strings.Contains(line, "consumesEvents"):
				consuming[name] = true
			case strings.Contains(line, "publishesOnly"):
				publishing[name] = true
			default:
				t.Errorf("%s opens the application without naming an event role; the "+
					"parameter exists because nothing in the assembly can guess it", name)
			}
		}
	}

	require.NotEmpty(t, consuming, "no consuming call site was found; the scan is blind")
	require.NotEmpty(t, publishing, "no command call site was found; the scan is blind")

	assert.Equal(t, map[string]bool{"app.go": true, "assemble.go": true}, consuming,
		"a call site consumes events and is not one of the two that serve requests.\n"+
			"On the Redis bus a subscription starts reading from the server's consumer "+
			"group, and every message the new one takes is a message the server does not "+
			"get — with its handlers running in a process that was asked to do one thing "+
			"and exit (D104).")

	assert.GreaterOrEqual(t, len(publishing), 5,
		"only %d command call sites were found and five verbs open the application; a "+
			"verb that stopped naming its role would have been caught above, so a smaller "+
			"number here means the scan stopped reading files", len(publishing))
}

// TestTheMCPVerbIsInTheDispatchAndTheUsage keeps the verb reachable and named.
//
// The dispatch and the usage text are two lists and the repository has paid for
// their drift before: TestUsageNamesEveryVerbTheDispatchAccepts derives the
// verbs from the switch for exactly that reason (D90). This adds nothing to that
// rule — it names the new verb so that a reader of this package finds it where
// the other verbs' own tests are.
func TestTheMCPVerbIsInTheDispatchAndTheUsage(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("app.go")
	require.NoError(t, err)

	assert.Contains(t, string(body), "case mcpCommand:",
		"the mcp verb is not in the dispatch; the binary would print the usage and refuse")

	usage := usageText("test")
	assert.Contains(t, usage, binaryName+" "+mcpCommand,
		"the usage text has no line for the mcp verb")
}
