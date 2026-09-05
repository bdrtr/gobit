package eventbus_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
)

// unreachableClient returns a client that will never connect.
//
// Because go-redis opens the connection on the first command, this lets the
// code paths that never reach the network (validation, serialization, a closed
// bus) be tested without Docker. The end-to-end behavior is in
// redis_integration_test.go.
func unreachableClient() *redis.Client {
	return redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
}

func TestNewRedisStreamRejectsNilClient(t *testing.T) {
	bus, err := eventbus.NewRedisStream(nil, eventbus.RedisConfig{}, discardLogger())
	if err == nil {
		t.Fatal("no error was returned for a nil client")
	}
	if bus != nil {
		t.Error("a non-nil bus was returned on the error path")
	}
	if !errors.IsInvalid(err) {
		t.Errorf("Kind = %v, expected invalid", errors.KindOf(err))
	}
	if got := errors.CodeOf(err); got != eventbus.CodeInvalidConfig {
		t.Errorf("Code = %q, expected %q", got, eventbus.CodeInvalidConfig)
	}
}

func TestRedisConfigStreamName(t *testing.T) {
	tests := []struct {
		name  string
		cfg   eventbus.RedisConfig
		event string
		want  string
	}{
		{
			name:  "default prefix",
			cfg:   eventbus.RedisConfig{},
			event: "order.placed",
			want:  eventbus.DefaultStreamPrefix + ":order.placed",
		},
		{
			name:  "custom prefix",
			cfg:   eventbus.RedisConfig{StreamPrefix: "test:events"},
			event: "cart.updated",
			want:  "test:events:cart.updated",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.StreamName(tt.event); got != tt.want {
				t.Errorf("StreamName(%q) = %q, expected %q", tt.event, got, tt.want)
			}
		})
	}
}

// TestWithNamespaceSeparatesStreamAndGroupTogether verifies that the namespace
// prefix separates BOTH fields at once.
//
// Had only the stream prefix been separated, the two setups would join the
// same consumer group and only ONE of them would receive an event —
// production's "order.placed" event could be consumed and swallowed by
// staging. This test keeps that half-separation from silently coming back.
func TestWithNamespaceSeparatesStreamAndGroupTogether(t *testing.T) {
	cfg := eventbus.RedisConfig{}.WithNamespace("gobit-staging")

	if got, want := cfg.StreamName("order.placed"), "gobit-staging:events:order.placed"; got != want {
		t.Errorf("StreamName() = %q, expected %q", got, want)
	}
	if got, want := cfg.Group, "gobit-staging"; got != want {
		t.Errorf("Group = %q, expected %q", got, want)
	}

	production := eventbus.RedisConfig{}.WithNamespace("gobit-prod")
	if production.Group == cfg.Group {
		t.Errorf("two namespaces fell into the same consumer group (%q); the events are not separated", cfg.Group)
	}
}

// TestWithNamespaceLeavesConsumerNameAlone verifies that the namespace does
// not overwrite the PROCESS identity.
//
// The two work in opposite directions: the namespace separates SETUPS, the
// consumer name separates the processes within one group. Had the namespace
// been written into the consumer too, every instance of the same setup would
// take the same consumer name and on every startup would read the messages the
// others are processing — that is, the same event would be processed twice.
func TestWithNamespaceLeavesConsumerNameAlone(t *testing.T) {
	cfg := eventbus.RedisConfig{Consumer: "gobit-0"}.WithNamespace("gobit-prod")

	if got, want := cfg.Consumer, "gobit-0"; got != want {
		t.Errorf("Consumer = %q, expected %q", got, want)
	}
}

// TestWithNamespaceIgnoresEmptyNamespace verifies that no headless key is
// built.
//
// An empty namespace would give a key like ":events:order.placed": a
// separation made without a NAME to separate by separates nothing, and it
// would break the default as well.
func TestWithNamespaceIgnoresEmptyNamespace(t *testing.T) {
	cfg := eventbus.RedisConfig{}.WithNamespace("")

	if got, want := cfg.StreamName("order.placed"), eventbus.DefaultStreamPrefix+":order.placed"; got != want {
		t.Errorf("StreamName() = %q, expected %q", got, want)
	}
	if cfg.Group != "" {
		t.Errorf("Group = %q, expected empty (it must fall back to the default)", cfg.Group)
	}
}

// TestDefaultsMatchNamespaceDerivation pins that the defaults are a SPECIAL
// CASE of the namespace derivation.
//
// The keys of every setup so far are "gobit:events:*" and the group is
// "gobit"; if the namespace derived from the default prefix
// (config.DefaultRedisKeyPrefix) diverges from that, an upgraded setup moves
// to a new and EMPTY stream and to a new group — the events waiting in the old
// stream stay there and are delivered to nobody.
func TestDefaultsMatchNamespaceDerivation(t *testing.T) {
	cfg := eventbus.RedisConfig{}.WithNamespace(eventbus.DefaultGroup)

	if got, want := cfg.StreamPrefix, eventbus.DefaultStreamPrefix; got != want {
		t.Errorf("StreamPrefix = %q, expected %q", got, want)
	}
	if got, want := cfg.Group, eventbus.DefaultGroup; got != want {
		t.Errorf("Group = %q, expected %q", got, want)
	}

	// The HISTORICAL form of the key is pinned separately: the assertion above
	// says the constants are consistent with each other, this line says what
	// the value is. Without both, the constants could drift together and an
	// upgraded setup would move to a new, EMPTY stream — the events waiting in
	// the old stream would stay there and be delivered to nobody. The value
	// matches config.DefaultRedisKeyPrefix (the core CANNOT import config, see
	// Principle 2.4).
	const historical = "gobit:events:order.placed"
	if got := cfg.StreamName("order.placed"); got != historical {
		t.Errorf("StreamName() = %q, expected %q (an upgraded setup loses its old stream)",
			got, historical)
	}
}

// TestConsumerNameCompletesEmptyNamePerProcess verifies that the automatic
// consumer name is REALLY generated.
//
// A given name is preserved; an empty name is derived per process. Had the
// latter silently stayed empty, Redis would see a single consumer whose name
// is empty and every instance would share the same pending list.
func TestConsumerNameCompletesEmptyNamePerProcess(t *testing.T) {
	if got, want := eventbus.ConsumerName("gobit-0"), "gobit-0"; got != want {
		t.Errorf("ConsumerName(%q) = %q, expected %q", want, got, want)
	}
	if got := eventbus.ConsumerName(""); got == "" {
		t.Error("ConsumerName(\"\") returned empty; it should have generated a per-process name")
	}
}

func TestRedisPublishRejectsUnserializableData(t *testing.T) {
	bus, err := eventbus.NewRedisStream(unreachableClient(), eventbus.RedisConfig{}, discardLogger())
	if err != nil {
		t.Fatalf("NewRedisStream returned an error: %v", err)
	}
	defer func() { _ = bus.Shutdown(context.Background()) }()

	tests := []struct {
		name string
		data map[string]any
	}{
		{name: "function value", data: map[string]any{"cb": func() {}}},
		{name: "channel value", data: map[string]any{"ch": make(chan int)}},
		{name: "NaN", data: map[string]any{"amount": math.NaN()}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The serialization error must come back as a typed error without
			// going to the network.
			err := bus.Publish(t.Context(), eventbus.Event{Name: "order.placed", Data: tt.data})
			if err == nil {
				t.Fatal("Publish returned no error for unserializable data")
			}
			if !errors.IsInvalid(err) {
				t.Errorf("Kind = %v, expected invalid", errors.KindOf(err))
			}
			if got := errors.CodeOf(err); got != eventbus.CodeInvalidEvent {
				t.Errorf("Code = %q, expected %q", got, eventbus.CodeInvalidEvent)
			}
		})
	}
}

func TestRedisPublishRejectsEmptyName(t *testing.T) {
	bus, err := eventbus.NewRedisStream(unreachableClient(), eventbus.RedisConfig{}, discardLogger())
	if err != nil {
		t.Fatalf("NewRedisStream returned an error: %v", err)
	}
	defer func() { _ = bus.Shutdown(context.Background()) }()

	if err := bus.Publish(t.Context(), eventbus.Event{}); !errors.IsInvalid(err) {
		t.Errorf("the error for an event with an empty name = %v, expected invalid", err)
	}
}

func TestRedisSubscribeValidates(t *testing.T) {
	bus, err := eventbus.NewRedisStream(unreachableClient(), eventbus.RedisConfig{}, discardLogger())
	if err != nil {
		t.Fatalf("NewRedisStream returned an error: %v", err)
	}
	defer func() { _ = bus.Shutdown(context.Background()) }()

	if err := bus.Subscribe("", nil); !errors.IsInvalid(err) {
		t.Errorf("the error for an empty event name = %v, expected invalid", err)
	}
	if err := bus.Subscribe("order.placed", nil); !errors.IsInvalid(err) {
		t.Errorf("the error for a nil handler = %v, expected invalid", err)
	}
}

func TestRedisSubscribeFailsWhenRedisUnreachable(t *testing.T) {
	client := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:1",
		DialTimeout: 200 * time.Millisecond,
		MaxRetries:  -1,
	})
	bus, err := eventbus.NewRedisStream(client, eventbus.RedisConfig{}, discardLogger())
	if err != nil {
		t.Fatalf("NewRedisStream returned an error: %v", err)
	}
	defer func() { _ = bus.Shutdown(context.Background()) }()

	// When the consumer group cannot be created, the subscription must not
	// count as silently successful.
	err = bus.Subscribe("order.placed", func(_ context.Context, _ eventbus.Event) error { return nil })
	if err == nil {
		t.Fatal("Subscribe returned no error against an unreachable redis")
	}
	if !errors.HasKind(err, errors.KindUnavailable) {
		t.Errorf("Kind = %v, expected unavailable", errors.KindOf(err))
	}
	if got := errors.CodeOf(err); got != eventbus.CodeSubscribeFailed {
		t.Errorf("Code = %q, expected %q", got, eventbus.CodeSubscribeFailed)
	}
}

func TestRedisClosedBusRejectsUse(t *testing.T) {
	bus, err := eventbus.NewRedisStream(unreachableClient(), eventbus.RedisConfig{}, discardLogger())
	if err != nil {
		t.Fatalf("NewRedisStream returned an error: %v", err)
	}
	if err := bus.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown returned an error: %v", err)
	}

	// On a closed bus Publish must return an error without ever going to the
	// network.
	err = bus.Publish(t.Context(), eventbus.Event{Name: "order.placed"})
	if got := errors.CodeOf(err); got != eventbus.CodeClosed {
		t.Errorf("Publish Code = %q, expected %q (error: %v)", got, eventbus.CodeClosed, err)
	}
	if !errors.HasKind(err, errors.KindUnavailable) {
		t.Errorf("Publish Kind = %v, expected unavailable", errors.KindOf(err))
	}

	err = bus.Subscribe("order.placed", func(_ context.Context, _ eventbus.Event) error { return nil })
	if got := errors.CodeOf(err); got != eventbus.CodeClosed {
		t.Errorf("Subscribe Code = %q, expected %q (error: %v)", got, eventbus.CodeClosed, err)
	}

	if err := bus.Shutdown(context.Background()); err != nil {
		t.Errorf("the second Shutdown returned an error: %v", err)
	}
}
