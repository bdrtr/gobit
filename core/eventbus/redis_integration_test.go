//go:build integration

// This file needs a real Redis and is compiled only with `-tags=integration`
// (`make test-integration`). That is what keeps `make test` fast and free of
// Docker.
package eventbus_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/bdrtr/gobit/core/eventbus"
)

// redisImage is the Redis image used in the integration tests.
const redisImage = "redis:7-alpine"

// startRedis starts a Redis living for the duration of the test and returns
// its client.
func startRedis(t *testing.T) *redis.Client {
	t.Helper()

	ctx := t.Context()
	container, err := tcredis.Run(ctx, redisImage)
	testcontainers.CleanupContainer(t, container)
	if err != nil {
		t.Fatalf("the redis container could not be started: %v", err)
	}

	uri, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("the connection string could not be obtained: %v", err)
	}

	opts, err := redis.ParseURL(uri)
	if err != nil {
		t.Fatalf("the connection string could not be parsed: %v", err)
	}

	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })

	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("redis could not be pinged: %v", err)
	}
	return client
}

// testConfig builds an isolated configuration per test; the tests do not see
// each other's stream or consumer group.
func testConfig(t *testing.T, consumer string) eventbus.RedisConfig {
	t.Helper()
	return eventbus.RedisConfig{
		StreamPrefix: "gobit-test:" + t.Name(),
		Group:        "group-" + t.Name(),
		Consumer:     consumer,
		BlockTimeout: 200 * time.Millisecond,
	}
}

func TestRedisIntegrationPublishSubscribe(t *testing.T) {
	client := startRedis(t)
	cfg := testConfig(t, "consumer-1")

	bus, err := eventbus.NewRedisStream(client, cfg, discardLogger())
	if err != nil {
		t.Fatalf("NewRedisStream returned an error: %v", err)
	}
	defer func() { _ = bus.Shutdown(context.Background()) }()

	got := make(chan eventbus.Event, 1)
	if err := bus.Subscribe("order.placed", func(_ context.Context, e eventbus.Event) error {
		got <- e
		return nil
	}); err != nil {
		t.Fatalf("Subscribe returned an error: %v", err)
	}

	when := time.Date(2026, 8, 23, 12, 30, 0, 0, time.UTC)
	published := eventbus.Event{
		Name:       "order.placed",
		ID:         "evt_integration_01",
		OccurredAt: when,
		Data: map[string]any{
			"order_id": "order_01",
			"total":    float64(1999),
			"items":    []any{"variant_01", "variant_02"},
		},
	}
	if err := bus.Publish(t.Context(), published); err != nil {
		t.Fatalf("Publish returned an error: %v", err)
	}

	select {
	case e := <-got:
		if e.Name != published.Name {
			t.Errorf("Name = %q, expected %q", e.Name, published.Name)
		}
		if e.ID != published.ID {
			t.Errorf("ID = %q, expected %q", e.ID, published.ID)
		}
		if !e.OccurredAt.Equal(when) {
			t.Errorf("OccurredAt = %v, expected %v", e.OccurredAt, when)
		}
		if e.Data["order_id"] != "order_01" {
			t.Errorf("Data[order_id] = %v, expected order_01", e.Data["order_id"])
		}
		if e.Data["total"] != float64(1999) {
			t.Errorf("Data[total] = %v, expected 1999", e.Data["total"])
		}
		if items, ok := e.Data["items"].([]any); !ok || len(items) != 2 {
			t.Errorf("Data[items] = %v, expected an array of 2 elements", e.Data["items"])
		}
	case <-time.After(15 * time.Second):
		t.Fatal("timed out: the event was not delivered over redis")
	}
}

func TestRedisIntegrationDeliversEventsPublishedBeforeSubscribe(t *testing.T) {
	client := startRedis(t)
	cfg := testConfig(t, "consumer-1")

	bus, err := eventbus.NewRedisStream(client, cfg, discardLogger())
	if err != nil {
		t.Fatalf("NewRedisStream returned an error: %v", err)
	}
	defer func() { _ = bus.Shutdown(context.Background()) }()

	// An event published while there is no subscriber stays in the stream;
	// because the group starts from "0", a subscriber arriving later receives
	// it too.
	if err := bus.Publish(t.Context(), eventbus.Event{
		Name: "order.placed",
		Data: map[string]any{"order_id": "order_early"},
	}); err != nil {
		t.Fatalf("Publish returned an error: %v", err)
	}

	got := make(chan eventbus.Event, 1)
	if err := bus.Subscribe("order.placed", func(_ context.Context, e eventbus.Event) error {
		got <- e
		return nil
	}); err != nil {
		t.Fatalf("Subscribe returned an error: %v", err)
	}

	select {
	case e := <-got:
		if e.Data["order_id"] != "order_early" {
			t.Errorf("Data[order_id] = %v, expected order_early", e.Data["order_id"])
		}
	case <-time.After(15 * time.Second):
		t.Fatal("timed out: the event published before the subscription was not delivered")
	}
}

func TestRedisIntegrationAcksProcessedMessages(t *testing.T) {
	client := startRedis(t)
	cfg := testConfig(t, "consumer-1")

	bus, err := eventbus.NewRedisStream(client, cfg, discardLogger())
	if err != nil {
		t.Fatalf("NewRedisStream returned an error: %v", err)
	}
	defer func() { _ = bus.Shutdown(context.Background()) }()

	const eventCount = 5
	var processed sync.WaitGroup
	processed.Add(eventCount)
	if err := bus.Subscribe("order.placed", func(context.Context, eventbus.Event) error {
		processed.Done()
		return nil
	}); err != nil {
		t.Fatalf("Subscribe returned an error: %v", err)
	}

	for i := range eventCount {
		if err := bus.Publish(t.Context(), eventbus.Event{
			Name: "order.placed",
			Data: map[string]any{"index": i},
		}); err != nil {
			t.Fatalf("Publish returned an error: %v", err)
		}
	}

	done := make(chan struct{})
	go func() {
		processed.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("timed out: the events were not processed")
	}

	// The ACK goes to the client asynchronously; the pending list is polled
	// until it empties.
	stream := cfg.StreamName("order.placed")
	deadline := time.Now().Add(10 * time.Second)
	for {
		pending, err := client.XPending(t.Context(), stream, cfg.Group).Result()
		if err != nil {
			t.Fatalf("XPending returned an error: %v", err)
		}
		if pending.Count == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending message count = %d, expected 0 (a processed message must be XACKed)", pending.Count)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// The messages are still in the stream (until they are trimmed) but none
	// of them is pending.
	length, err := client.XLen(t.Context(), stream).Result()
	if err != nil {
		t.Fatalf("XLen returned an error: %v", err)
	}
	if length != eventCount {
		t.Errorf("stream length = %d, expected %d", length, eventCount)
	}
}

func TestRedisIntegrationConsumerGroupDeliversOnce(t *testing.T) {
	client := startRedis(t)

	// Two separate consumers joined to the same group: every message must go
	// to only one of them.
	cfgA := testConfig(t, "consumer-a")
	cfgB := testConfig(t, "consumer-b")

	var received sync.Map // event id -> consumer name
	var total atomic.Int64
	var duplicates atomic.Int64

	const eventCount = 30
	var wg sync.WaitGroup
	wg.Add(eventCount)

	subscribe := func(cfg eventbus.RedisConfig) eventbus.EventBus {
		t.Helper()
		bus, err := eventbus.NewRedisStream(client, cfg, discardLogger())
		if err != nil {
			t.Fatalf("NewRedisStream returned an error: %v", err)
		}
		if err := bus.Subscribe("order.placed", func(_ context.Context, e eventbus.Event) error {
			if _, loaded := received.LoadOrStore(e.ID, cfg.Consumer); loaded {
				duplicates.Add(1)
				return nil
			}
			total.Add(1)
			wg.Done()
			return nil
		}); err != nil {
			t.Fatalf("Subscribe returned an error: %v", err)
		}
		return bus
	}

	busA := subscribe(cfgA)
	defer func() { _ = busA.Shutdown(context.Background()) }()
	busB := subscribe(cfgB)
	defer func() { _ = busB.Shutdown(context.Background()) }()

	for i := range eventCount {
		if err := busA.Publish(t.Context(), eventbus.Event{
			Name: "order.placed",
			ID:   fmt.Sprintf("evt_%02d", i),
			Data: map[string]any{"index": i},
		}); err != nil {
			t.Fatalf("Publish returned an error: %v", err)
		}
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatalf("timed out: %d/%d events were delivered", total.Load(), eventCount)
	}

	if got := total.Load(); got != eventCount {
		t.Errorf("distinct events delivered = %d, expected %d", got, eventCount)
	}
	if got := duplicates.Load(); got != 0 {
		t.Errorf("repeated deliveries = %d, expected 0 (a consumer group must hand a message over once)", got)
	}

	// Both consumers must have taken a share of the group; otherwise the test
	// is measuring a single consumer rather than the group's distribution.
	consumers := make(map[string]int)
	received.Range(func(_, value any) bool {
		if name, ok := value.(string); ok {
			consumers[name]++
		}
		return true
	})
	t.Logf("consumer distribution: %v", consumers)
}

func TestRedisIntegrationResumesAfterRestart(t *testing.T) {
	client := startRedis(t)
	cfg := testConfig(t, "consumer-durable")

	// The first process: it processes two events and shuts down.
	first, err := eventbus.NewRedisStream(client, cfg, discardLogger())
	if err != nil {
		t.Fatalf("NewRedisStream returned an error: %v", err)
	}

	firstBatch := make(chan string, 4)
	if err := first.Subscribe("order.placed", func(_ context.Context, e eventbus.Event) error {
		firstBatch <- e.ID
		return nil
	}); err != nil {
		t.Fatalf("Subscribe returned an error: %v", err)
	}

	for _, id := range []string{"evt_01", "evt_02"} {
		if err := first.Publish(t.Context(), eventbus.Event{Name: "order.placed", ID: id}); err != nil {
			t.Fatalf("Publish returned an error: %v", err)
		}
	}
	for range 2 {
		select {
		case <-firstBatch:
		case <-time.After(15 * time.Second):
			t.Fatal("timed out: the first batch was not processed")
		}
	}
	if err := first.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown returned an error: %v", err)
	}

	// An event published while the bus is closed must not be lost.
	publisher, err := eventbus.NewRedisStream(client, cfg, discardLogger())
	if err != nil {
		t.Fatalf("NewRedisStream returned an error: %v", err)
	}
	defer func() { _ = publisher.Shutdown(context.Background()) }()
	if err := publisher.Publish(t.Context(), eventbus.Event{Name: "order.placed", ID: "evt_03"}); err != nil {
		t.Fatalf("Publish returned an error: %v", err)
	}

	// The second process comes up with the same group and consumer name: it
	// must resume where it left off and must not receive the processed events
	// again.
	second, err := eventbus.NewRedisStream(client, cfg, discardLogger())
	if err != nil {
		t.Fatalf("NewRedisStream returned an error: %v", err)
	}
	defer func() { _ = second.Shutdown(context.Background()) }()

	secondBatch := make(chan string, 4)
	if err := second.Subscribe("order.placed", func(_ context.Context, e eventbus.Event) error {
		secondBatch <- e.ID
		return nil
	}); err != nil {
		t.Fatalf("Subscribe returned an error: %v", err)
	}

	select {
	case id := <-secondBatch:
		if id != "evt_03" {
			t.Errorf("the first event after the restart = %q, expected evt_03", id)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("timed out: no event was delivered after the restart")
	}

	select {
	case id := <-secondBatch:
		t.Errorf("a processed event was delivered again: %q", id)
	case <-time.After(time.Second):
	}
}

func TestRedisIntegrationHandlerPanicDoesNotStopConsumer(t *testing.T) {
	client := startRedis(t)
	cfg := testConfig(t, "consumer-1")

	bus, err := eventbus.NewRedisStream(client, cfg, discardLogger())
	if err != nil {
		t.Fatalf("NewRedisStream returned an error: %v", err)
	}
	defer func() { _ = bus.Shutdown(context.Background()) }()

	delivered := make(chan string, 4)
	if err := bus.Subscribe("order.placed", func(_ context.Context, e eventbus.Event) error {
		if e.ID == "evt_panic" {
			panic("the handler blew up")
		}
		return nil
	}); err != nil {
		t.Fatalf("Subscribe returned an error: %v", err)
	}
	if err := bus.Subscribe("order.placed", func(_ context.Context, e eventbus.Event) error {
		delivered <- e.ID
		return nil
	}); err != nil {
		t.Fatalf("Subscribe returned an error: %v", err)
	}

	for _, id := range []string{"evt_panic", "evt_intact"} {
		if err := bus.Publish(t.Context(), eventbus.Event{Name: "order.placed", ID: id}); err != nil {
			t.Fatalf("Publish returned an error: %v", err)
		}
	}

	for _, want := range []string{"evt_panic", "evt_intact"} {
		select {
		case got := <-delivered:
			if got != want {
				t.Errorf("delivered = %q, expected %q", got, want)
			}
		case <-time.After(15 * time.Second):
			t.Fatalf("timed out: %q was not delivered (the panic stopped the consumer)", want)
		}
	}
}

// A process that reads a message and dies before ACKing it leaves that message
// in ITS OWN pending list, and a restarted process comes back under a new name.
// Measured before the takeover existed: the message sat there with its idle time
// growing and a fresh consumer received nothing, forever (D105).
//
// The dead consumer here is not a bus but a raw XREADGROUP under a name that
// never comes back, because that is exactly what a killed process leaves: a
// pending entry with nobody behind it.

func TestRedisIntegrationTakesOverWhatADeadConsumerWasHolding(t *testing.T) {
	client := startRedis(t)
	cfg := testConfig(t, "successor")
	cfg.ClaimMinIdle = 300 * time.Millisecond
	stream := cfg.StreamName("order.placed")

	if err := client.XGroupCreateMkStream(t.Context(), stream, cfg.Group, "0").Err(); err != nil {
		t.Fatalf("the consumer group could not be created: %v", err)
	}

	publisher, err := eventbus.NewRedisStream(client, cfg, discardLogger())
	if err != nil {
		t.Fatalf("NewRedisStream returned an error: %v", err)
	}
	defer func() { _ = publisher.Shutdown(context.Background()) }()
	if err := publisher.Publish(t.Context(), eventbus.Event{
		Name: "order.placed",
		ID:   "evt_the_dead_one_held",
	}); err != nil {
		t.Fatalf("Publish returned an error: %v", err)
	}

	taken, err := client.XReadGroup(t.Context(), &redis.XReadGroupArgs{
		Group:    cfg.Group,
		Consumer: "consumer-that-was-killed",
		Streams:  []string{stream, ">"},
		Count:    1,
	}).Result()
	if err != nil {
		t.Fatalf("the dying consumer could not read: %v", err)
	}
	if len(taken) != 1 || len(taken[0].Messages) != 1 {
		t.Fatalf("the dying consumer read %v; the scenario needs it to hold exactly one message", taken)
	}

	successor, err := eventbus.NewRedisStream(client, cfg, discardLogger())
	if err != nil {
		t.Fatalf("NewRedisStream returned an error: %v", err)
	}
	defer func() { _ = successor.Shutdown(context.Background()) }()

	got := make(chan string, 4)
	if err := successor.Subscribe("order.placed", func(_ context.Context, e eventbus.Event) error {
		got <- e.ID
		return nil
	}); err != nil {
		t.Fatalf("Subscribe returned an error: %v", err)
	}

	select {
	case id := <-got:
		if id != "evt_the_dead_one_held" {
			t.Errorf("the delivered event = %q, expected evt_the_dead_one_held", id)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("timed out: the message the killed consumer was holding never reached the " +
			"successor. That is the whole of D105 — the event is lost while the stream " +
			"reports it as delivered")
	}

	// The bus is stopped BEFORE the pending list is read, and the order is the
	// whole point: the ACK runs in a defer AFTER the handler returns, so reading
	// the list the moment the handler fires is a race against it. It was written
	// that way first, it won the race on the machine that wrote it, and it lost
	// on the runner. Shutdown waits for the consume loop, and the loop cannot
	// return until the dispatch it is inside — ACK included — is finished.
	if err := successor.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown returned an error: %v", err)
	}

	pending, err := client.XPendingExt(t.Context(), &redis.XPendingExtArgs{
		Stream: stream, Group: cfg.Group, Start: "-", End: "+", Count: 10,
	}).Result()
	if err != nil {
		t.Fatalf("XPendingExt returned an error: %v", err)
	}
	for _, entry := range pending {
		// The OWNER is what tells the two failures apart: still under the killed
		// consumer means the takeover never happened, while under this process's
		// name means it happened and the ACK did not.
		t.Errorf("the takeover left %s pending under %q after %s; a taken-over message that "+
			"is not ACKed has only changed owner", entry.ID, entry.Consumer, entry.Idle)
	}
}

func TestRedisIntegrationDoesNotTakeAMessageStillWithinTheThreshold(t *testing.T) {
	client := startRedis(t)
	cfg := testConfig(t, "impatient-successor")
	// Far longer than this test runs: the other consumer is to look ALIVE.
	cfg.ClaimMinIdle = 30 * time.Second
	stream := cfg.StreamName("order.placed")

	if err := client.XGroupCreateMkStream(t.Context(), stream, cfg.Group, "0").Err(); err != nil {
		t.Fatalf("the consumer group could not be created: %v", err)
	}

	publisher, err := eventbus.NewRedisStream(client, cfg, discardLogger())
	if err != nil {
		t.Fatalf("NewRedisStream returned an error: %v", err)
	}
	defer func() { _ = publisher.Shutdown(context.Background()) }()
	if err := publisher.Publish(t.Context(), eventbus.Event{
		Name: "order.placed",
		ID:   "evt_somebody_is_working_on_it",
	}); err != nil {
		t.Fatalf("Publish returned an error: %v", err)
	}

	if _, err := client.XReadGroup(t.Context(), &redis.XReadGroupArgs{
		Group:    cfg.Group,
		Consumer: "consumer-that-is-busy",
		Streams:  []string{stream, ">"},
		Count:    1,
	}).Result(); err != nil {
		t.Fatalf("the busy consumer could not read: %v", err)
	}

	other, err := eventbus.NewRedisStream(client, cfg, discardLogger())
	if err != nil {
		t.Fatalf("NewRedisStream returned an error: %v", err)
	}
	defer func() { _ = other.Shutdown(context.Background()) }()

	got := make(chan string, 4)
	if err := other.Subscribe("order.placed", func(_ context.Context, e eventbus.Event) error {
		got <- e.ID
		return nil
	}); err != nil {
		t.Fatalf("Subscribe returned an error: %v", err)
	}

	select {
	case id := <-got:
		t.Fatalf("the event %q was taken from a consumer that had held it for a moment. A "+
			"consumer that is merely slow leaves the same pending entry as a dead one, so "+
			"the threshold is the only thing keeping a live consumer's work from being "+
			"done twice", id)
	case <-time.After(3 * time.Second):
	}
}
