package eventbus

import (
	"bytes"
	"context"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/bdrtr/gobit/core/errors"
)

// testEventName is the event name used in the in-package tests.
const testEventName = "order.placed"

// fakeRead is a single scripted XREADGROUP round; if err is given the round
// returns that error.
type fakeRead struct {
	streams []redis.XStream
	err     error
}

// fakeStreamClient imitates streamClient inside the process.
//
// XReadGroup returns the scripted rounds in order; once the script is
// exhausted the call blocks until the ctx is canceled, so that the consumer
// loop does not spin. The requested cursors, the ACKed ids and the written
// messages are recorded.
type fakeStreamClient struct {
	// onGroupCreate is called instead of XGroupCreateMkStream when it is
	// non-nil; the tests use it to slow the group setup down or to make it
	// fail.
	onGroupCreate func(ctx context.Context) error

	mu      sync.Mutex
	reads   []fakeRead
	cursors []string
	acked   []string
	added   []*redis.XAddArgs

	// drained closes once the last scripted round has been served too.
	drained     chan struct{}
	drainedOnce sync.Once
}

var _ streamClient = (*fakeStreamClient)(nil)

// newFakeStreamClient builds a fake client returning the given rounds in
// order.
func newFakeStreamClient(reads ...fakeRead) *fakeStreamClient {
	f := &fakeStreamClient{reads: reads, drained: make(chan struct{})}
	if len(reads) == 0 {
		f.drainedOnce.Do(func() { close(f.drained) })
	}
	return f
}

func (f *fakeStreamClient) XAdd(ctx context.Context, a *redis.XAddArgs) *redis.StringCmd {
	cmd := redis.NewStringCmd(ctx)

	f.mu.Lock()
	f.added = append(f.added, a)
	f.mu.Unlock()

	cmd.SetVal("1-0")
	return cmd
}

func (f *fakeStreamClient) XGroupCreateMkStream(ctx context.Context, _, _, _ string) *redis.StatusCmd {
	cmd := redis.NewStatusCmd(ctx)
	if f.onGroupCreate != nil {
		if err := f.onGroupCreate(ctx); err != nil {
			cmd.SetErr(err)
			return cmd
		}
	}
	cmd.SetVal("OK")
	return cmd
}

func (f *fakeStreamClient) XReadGroup(ctx context.Context, a *redis.XReadGroupArgs) *redis.XStreamSliceCmd {
	cmd := redis.NewXStreamSliceCmd(ctx)

	f.mu.Lock()
	if n := len(a.Streams); n > 0 {
		f.cursors = append(f.cursors, a.Streams[n-1])
	}
	scripted := len(f.reads) > 0
	var next fakeRead
	if scripted {
		next, f.reads = f.reads[0], f.reads[1:]
		if len(f.reads) == 0 {
			f.drainedOnce.Do(func() { close(f.drained) })
		}
	}
	f.mu.Unlock()

	switch {
	case !scripted:
		// The script is over: block until the bus closes, do not set up a busy
		// loop.
		<-ctx.Done()
		cmd.SetErr(ctx.Err())
	case next.err != nil:
		cmd.SetErr(next.err)
	default:
		cmd.SetVal(next.streams)
	}
	return cmd
}

func (f *fakeStreamClient) XAck(ctx context.Context, _, _ string, ids ...string) *redis.IntCmd {
	cmd := redis.NewIntCmd(ctx)

	f.mu.Lock()
	f.acked = append(f.acked, ids...)
	f.mu.Unlock()

	cmd.SetVal(int64(len(ids)))
	return cmd
}

// requestedCursors returns the cursors given to XReadGroup in order.
func (f *fakeStreamClient) requestedCursors() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.cursors)
}

// ackedIDs returns the ACKed message ids.
func (f *fakeStreamClient) ackedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.acked)
}

// addedArgs returns the arguments given to XAdd in order.
func (f *fakeStreamClient) addedArgs() []*redis.XAddArgs {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.added)
}

// fakeConfig returns the isolated configuration used with the fake client.
func fakeConfig() RedisConfig {
	return RedisConfig{
		StreamPrefix: "test:events",
		Group:        "test-group",
		Consumer:     "test-consumer",
		BlockTimeout: 10 * time.Millisecond,
	}
}

// scriptedRead builds a round returning messages from a single stream.
func scriptedRead(stream string, msgs ...redis.XMessage) fakeRead {
	return fakeRead{streams: []redis.XStream{{Stream: stream, Messages: msgs}}}
}

// eventMessage builds the stream counterpart of a published event.
func eventMessage(msgID, eventID, name string, when time.Time, data string) redis.XMessage {
	return redis.XMessage{
		ID: msgID,
		Values: map[string]any{
			fieldID:         eventID,
			fieldName:       name,
			fieldOccurredAt: when.Format(time.RFC3339Nano),
			fieldData:       data,
		},
	}
}

// waitClosed waits for the channel to close; if it does not, it fails the
// test.
func waitClosed(t *testing.T, ch <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out: %s", message)
	}
}

// shutdownBus closes the bus and fails the test if it returns an error.
func shutdownBus(t *testing.T, b *redisBus) {
	t.Helper()
	if err := b.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown returned an error: %v", err)
	}
}

func TestDecodeMessage(t *testing.T) {
	when := time.Date(2026, 8, 23, 12, 30, 0, 0, time.UTC)

	tests := []struct {
		name     string
		values   map[string]any
		wantErr  bool
		wantName string
		wantID   string
		wantTime time.Time
		wantData map[string]any
	}{
		{
			name: "complete message",
			values: map[string]any{
				fieldID:         "evt_01",
				fieldName:       "order.paid",
				fieldOccurredAt: when.Format(time.RFC3339Nano),
				fieldData:       `{"order_id":"order_01","total":1999}`,
			},
			wantName: "order.paid",
			wantID:   "evt_01",
			wantTime: when,
			wantData: map[string]any{"order_id": "order_01", "total": float64(1999)},
		},
		{
			name: "the fallback name is used when the name field is absent",
			values: map[string]any{
				fieldID:   "evt_02",
				fieldData: `{"order_id":"order_02"}`,
			},
			wantName: testEventName,
			wantID:   "evt_02",
			wantData: map[string]any{"order_id": "order_02"},
		},
		{
			name:     "no data field",
			values:   map[string]any{fieldID: "evt_03"},
			wantName: testEventName,
			wantID:   "evt_03",
		},
		{
			// Redis returns an entry that sits in the pending list but was
			// trimmed away without fields.
			name:    "message without fields (tombstone)",
			values:  nil,
			wantErr: true,
		},
		{
			name:    "empty field map",
			values:  map[string]any{},
			wantErr: true,
		},
		{
			name:    "malformed occurred_at",
			values:  map[string]any{fieldID: "evt_04", fieldOccurredAt: "yesterday"},
			wantErr: true,
		},
		{
			name:    "malformed json data",
			values:  map[string]any{fieldID: "evt_05", fieldData: "{malformed"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeMessage(testEventName, redis.XMessage{ID: "1-0", Values: tt.values})

			if tt.wantErr {
				if err == nil {
					t.Fatalf("an error was expected, an event came back: %+v", got)
				}
				if !errors.IsInvalid(err) {
					t.Errorf("Kind = %v, expected invalid", errors.KindOf(err))
				}
				if code := errors.CodeOf(err); code != CodeInvalidEvent {
					t.Errorf("Code = %q, expected %q", code, CodeInvalidEvent)
				}
				return
			}

			if err != nil {
				t.Fatalf("decodeMessage returned an error: %v", err)
			}
			if got.Name != tt.wantName {
				t.Errorf("Name = %q, expected %q", got.Name, tt.wantName)
			}
			if got.ID != tt.wantID {
				t.Errorf("ID = %q, expected %q", got.ID, tt.wantID)
			}
			if !got.OccurredAt.Equal(tt.wantTime) {
				t.Errorf("OccurredAt = %v, expected %v", got.OccurredAt, tt.wantTime)
			}
			if !maps.Equal(got.Data, tt.wantData) {
				t.Errorf("Data = %v, expected %v", got.Data, tt.wantData)
			}
		})
	}
}

func TestMessagesOf(t *testing.T) {
	first := redis.XMessage{ID: "1-0", Values: map[string]any{fieldID: "evt_01"}}
	res := []redis.XStream{
		{Stream: "other:stream", Messages: []redis.XMessage{{ID: "9-0"}}},
		{Stream: "test:events:order.placed", Messages: []redis.XMessage{first}},
	}

	got := messagesOf(res, "test:events:order.placed")
	if len(got) != 1 || got[0].ID != "1-0" {
		t.Errorf("messagesOf = %v, expected only the 1-0 message", got)
	}
	if got := messagesOf(res, "missing:stream"); got != nil {
		t.Errorf("messagesOf for a non-matching stream = %v, expected nil", got)
	}
	if got := messagesOf(nil, "test:events:order.placed"); got != nil {
		t.Errorf("messagesOf for an empty response = %v, expected nil", got)
	}
}

func TestStringField(t *testing.T) {
	values := map[string]any{"text": "value", "number": 42}

	if got := stringField(values, "text"); got != "value" {
		t.Errorf("stringField(text) = %q, expected value", got)
	}
	if got := stringField(values, "number"); got != "" {
		t.Errorf("stringField for a non-string field = %q, expected empty", got)
	}
	if got := stringField(values, "missing"); got != "" {
		t.Errorf("stringField for an absent field = %q, expected empty", got)
	}
	if got := stringField(nil, "text"); got != "" {
		t.Errorf("stringField for a nil map = %q, expected empty", got)
	}
}

func TestConsumeReadsPendingListBeforeNewMessages(t *testing.T) {
	cfg := fakeConfig()
	stream := cfg.StreamName(testEventName)
	when := time.Date(2026, 8, 23, 12, 30, 0, 0, time.UTC)

	fake := newFakeStreamClient(
		// Round 1: a pending message left over from a restart.
		scriptedRead(stream, eventMessage("1-1", "evt_pending", testEventName, when, `{}`)),
		// Round 2: the pending list is drained.
		scriptedRead(stream),
		// Round 3: from now on only new messages.
		scriptedRead(stream, eventMessage("2-1", "evt_new", testEventName, when, `{}`)),
	)

	bus := newRedisBus(fake, cfg, quietLogger())

	seen := make(chan string, 4)
	if err := bus.Subscribe(testEventName, func(_ context.Context, e Event) error {
		seen <- e.ID
		return nil
	}); err != nil {
		t.Fatalf("Subscribe returned an error: %v", err)
	}

	waitClosed(t, fake.drained, "the scripted rounds were not consumed")
	shutdownBus(t, bus)

	// When the process restarts, consumption must start from the PENDING list;
	// the list must be paged through and, once drained, must switch to the ">"
	// marker.
	cursors := fake.requestedCursors()
	if len(cursors) < 3 {
		t.Fatalf("requested cursors = %v, at least 3 rounds were expected", cursors)
	}
	want := []string{cursorPending, "1-1", cursorNew}
	if !slices.Equal(cursors[:3], want) {
		t.Errorf("requested cursors = %v, expected %v", cursors[:3], want)
	}

	close(seen)
	var got []string
	for id := range seen {
		got = append(got, id)
	}
	if !slices.Equal(got, []string{"evt_pending", "evt_new"}) {
		t.Errorf("delivered ids = %v, expected [evt_pending evt_new]", got)
	}
}

func TestConsumeDeliversDecodedEventAndAcks(t *testing.T) {
	cfg := fakeConfig()
	stream := cfg.StreamName(testEventName)
	when := time.Date(2026, 8, 23, 12, 30, 0, 0, time.UTC)

	fake := newFakeStreamClient(
		scriptedRead(stream, eventMessage("5-0", "evt_01", "order.paid", when,
			`{"order_id":"order_01","total":1999,"items":["a","b"]}`)),
	)
	bus := newRedisBus(fake, cfg, quietLogger())

	got := make(chan Event, 1)
	if err := bus.Subscribe(testEventName, func(_ context.Context, e Event) error {
		got <- e
		return nil
	}); err != nil {
		t.Fatalf("Subscribe returned an error: %v", err)
	}

	waitClosed(t, fake.drained, "the message was never read")
	shutdownBus(t, bus)

	select {
	case e := <-got:
		if e.Name != "order.paid" {
			t.Errorf("Name = %q, expected order.paid (the name in the message must beat the fallback)", e.Name)
		}
		if e.ID != "evt_01" {
			t.Errorf("ID = %q, expected evt_01", e.ID)
		}
		if !e.OccurredAt.Equal(when) {
			t.Errorf("OccurredAt = %v, expected %v", e.OccurredAt, when)
		}
		if e.Data["order_id"] != "order_01" {
			t.Errorf("Data[order_id] = %v, expected order_01", e.Data["order_id"])
		}
		if e.Data["total"] != float64(1999) {
			t.Errorf("Data[total] = %v, expected 1999 (the payload must not silently empty out)", e.Data["total"])
		}
		if items, ok := e.Data["items"].([]any); !ok || len(items) != 2 {
			t.Errorf("Data[items] = %v, expected an array of 2 elements", e.Data["items"])
		}
	default:
		t.Fatal("the event never reached the handler")
	}

	if acked := fake.ackedIDs(); !slices.Equal(acked, []string{"5-0"}) {
		t.Errorf("ACKed ids = %v, expected [5-0]", acked)
	}
}

// TestConsumeHandlerCtxCarriesNoPublisherValues pins the Redis backend's ctx
// behavior from the [Handler] contract.
//
// In this backend the event crosses the PROCESS BOUNDARY: the consumer never
// sees the publisher's ctx, so whatever the publisher puts into the ctx does
// not reach the handler. The in-memory backend's behavior at the same point is
// THE EXACT OPPOSITE (see TestInMemoryHandlerContextSurvivesCallerCancel), and
// because the default backend is that one, the difference can only be seen if
// it is written down here — otherwise a design carrying something in the ctx
// passes green in tests and reads silently empty in production.
//
// The message's FIELD SET is pinned too: nothing from the publisher's ctx is
// serialized. Adding a field is not forbidden, but this test and the [Handler]
// godoc must change TOGETHER; the two drifting apart was precisely this debt.
func TestConsumeHandlerCtxCarriesNoPublisherValues(t *testing.T) {
	type ctxKey struct{}

	cfg := fakeConfig()
	stream := cfg.StreamName(testEventName)
	when := time.Date(2026, 8, 23, 12, 30, 0, 0, time.UTC)

	fake := newFakeStreamClient(
		scriptedRead(stream, eventMessage("9-0", "evt_01", testEventName, when, `{}`)),
	)
	bus := newRedisBus(fake, cfg, quietLogger())

	got := make(chan context.Context, 1)
	if err := bus.Subscribe(testEventName, func(ctx context.Context, _ Event) error {
		got <- ctx
		return nil
	}); err != nil {
		t.Fatalf("Subscribe returned an error: %v", err)
	}

	// The publisher carries a request value in its ctx; it must reach neither
	// the message nor the handler.
	publishCtx := context.WithValue(t.Context(), ctxKey{}, "req_01")
	if err := bus.Publish(publishCtx, Event{Name: testEventName}); err != nil {
		t.Fatalf("Publish returned an error: %v", err)
	}

	waitClosed(t, fake.drained, "the message was never read")
	shutdownBus(t, bus)

	select {
	case hctx := <-got:
		if v := hctx.Value(ctxKey{}); v != nil {
			t.Errorf("the handler ctx carries the publisher's value (%v); the consuming process "+
				"cannot see the publisher's ctx, and the godoc must not promise it", v)
		}
		if err := hctx.Err(); err != nil {
			t.Errorf("the handler ctx was canceled: %v (Shutdown must not cut processing short)", err)
		}
	default:
		t.Fatal("the event never reached the handler")
	}

	added := fake.addedArgs()
	if len(added) != 1 {
		t.Fatalf("XAdd call count = %d, expected 1", len(added))
	}

	values, ok := added[0].Values.(map[string]any)
	if !ok {
		t.Fatalf("the XAdd values have type %T; map[string]any was expected", added[0].Values)
	}

	want := []string{fieldID, fieldName, fieldOccurredAt, fieldData}
	slices.Sort(want)

	if fields := slices.Sorted(maps.Keys(values)); !slices.Equal(fields, want) {
		t.Errorf("message fields = %v, expected %v (nothing from the publisher's ctx is "+
			"serialized; if a field was added, the Handler godoc must change too)", fields, want)
	}
}

func TestConsumeDoesNotDeliverTombstoneMessage(t *testing.T) {
	cfg := fakeConfig()
	stream := cfg.StreamName(testEventName)
	when := time.Date(2026, 8, 23, 12, 30, 0, 0, time.UTC)

	// A trimmed/deleted stream entry stays in the pending list and is returned
	// by go-redis as an XMessage without fields.
	tombstone := redis.XMessage{ID: "7-0", Values: nil}

	fake := newFakeStreamClient(
		scriptedRead(stream, tombstone,
			eventMessage("7-1", "evt_intact", testEventName, when, `{"order_id":"order_01"}`)),
	)

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	bus := newRedisBus(fake, cfg, log)

	seen := make(chan Event, 4)
	if err := bus.Subscribe(testEventName, func(_ context.Context, e Event) error {
		seen <- e
		return nil
	}); err != nil {
		t.Fatalf("Subscribe returned an error: %v", err)
	}

	waitClosed(t, fake.drained, "the messages were never read")
	shutdownBus(t, bus)

	if got := len(seen); got != 1 {
		t.Fatalf("handler call count = %d, expected 1 (a tombstone must not be delivered)", got)
	}
	if e := <-seen; e.ID != "evt_intact" {
		t.Errorf("the delivered event = %+v, expected only evt_intact", e)
	}

	// The tombstone must still be ACKed; otherwise it stays in the pending
	// list forever.
	if acked := fake.ackedIDs(); !slices.Equal(acked, []string{"7-0", "7-1"}) {
		t.Errorf("ACKed ids = %v, expected [7-0 7-1]", acked)
	}
	if out := buf.String(); !strings.Contains(out, "could not be decoded") {
		t.Errorf("the undecodable message was not logged; log output: %s", out)
	}
}

func TestConsumeContinuesAfterReadError(t *testing.T) {
	cfg := fakeConfig()
	stream := cfg.StreamName(testEventName)
	when := time.Date(2026, 8, 23, 12, 30, 0, 0, time.UTC)

	fake := newFakeStreamClient(
		fakeRead{err: errors.New("redis went down")},
		scriptedRead(stream, eventMessage("3-0", "evt_01", testEventName, when, `{}`)),
	)

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	bus := newRedisBus(fake, cfg, log)

	got := make(chan string, 1)
	if err := bus.Subscribe(testEventName, func(_ context.Context, e Event) error {
		got <- e.ID
		return nil
	}); err != nil {
		t.Fatalf("Subscribe returned an error: %v", err)
	}

	waitClosed(t, fake.drained, "consumption did not continue after the read error")
	shutdownBus(t, bus)

	select {
	case id := <-got:
		if id != "evt_01" {
			t.Errorf("the delivered id = %q, expected evt_01", id)
		}
	default:
		t.Error("the message after the read error was not delivered")
	}
	if out := buf.String(); !strings.Contains(out, "could not be read") {
		t.Errorf("the read error was not logged; log output: %s", out)
	}
}

func TestSubscribeDoesNotBlockPublishWhileCreatingGroup(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	fake := newFakeStreamClient()
	fake.onGroupCreate = func(context.Context) error {
		close(entered)
		<-release
		return nil
	}

	bus := newRedisBus(fake, fakeConfig(), quietLogger())

	subscribed := make(chan error, 1)
	go func() {
		subscribed <- bus.Subscribe(testEventName, func(context.Context, Event) error { return nil })
	}()

	// A publication is attempted while the consumer group setup (a real
	// network round trip) is in flight.
	waitClosed(t, entered, "the consumer group setup did not start")

	published := make(chan error, 1)
	go func() {
		published <- bus.Publish(context.Background(), Event{Name: testEventName})
	}()

	select {
	case err := <-published:
		if err != nil {
			t.Fatalf("Publish returned an error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked while Subscribe was creating the consumer group")
	}

	close(release)
	if err := <-subscribed; err != nil {
		t.Fatalf("Subscribe returned an error: %v", err)
	}
	shutdownBus(t, bus)
}

func TestSubscribeFailsWhenGroupCannotBeCreated(t *testing.T) {
	fake := newFakeStreamClient()
	fake.onGroupCreate = func(context.Context) error {
		return errors.New("connection refused")
	}

	bus := newRedisBus(fake, fakeConfig(), quietLogger())
	defer func() { _ = bus.Shutdown(context.Background()) }()

	err := bus.Subscribe(testEventName, func(context.Context, Event) error { return nil })
	if err == nil {
		t.Fatal("Subscribe returned no error when the group could not be created")
	}
	if !errors.HasKind(err, errors.KindUnavailable) {
		t.Errorf("Kind = %v, expected unavailable", errors.KindOf(err))
	}
	if code := errors.CodeOf(err); code != CodeSubscribeFailed {
		t.Errorf("Code = %q, expected %q", code, CodeSubscribeFailed)
	}

	// After a failed setup the consumer loop must not have started.
	if cursors := fake.requestedCursors(); len(cursors) != 0 {
		t.Errorf("requested cursors = %v, expected empty (the loop should not have started)", cursors)
	}
}

func TestRedisShutdownReturnsWhenContextExpires(t *testing.T) {
	cfg := fakeConfig()
	stream := cfg.StreamName(testEventName)
	when := time.Date(2026, 8, 23, 12, 30, 0, 0, time.UTC)

	fake := newFakeStreamClient(
		scriptedRead(stream, eventMessage("4-0", "evt_01", testEventName, when, `{}`)),
	)
	bus := newRedisBus(fake, cfg, quietLogger())

	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)

	if err := bus.Subscribe(testEventName, func(context.Context, Event) error {
		close(entered)
		<-release
		return nil
	}); err != nil {
		t.Fatalf("Subscribe returned an error: %v", err)
	}
	waitClosed(t, entered, "the handler never started")

	// A stuck handler must not lock the shutdown forever.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := bus.Shutdown(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Shutdown returned nil despite the stuck handler")
	}
	if !errors.HasKind(err, errors.KindUnavailable) {
		t.Errorf("Kind = %v, expected unavailable", errors.KindOf(err))
	}
	if code := errors.CodeOf(err); code != CodeShutdownTimeout {
		t.Errorf("Code = %q, expected %q", code, CodeShutdownTimeout)
	}
	if elapsed > 5*time.Second {
		t.Errorf("Shutdown took %v; it should have been bounded by the ctx budget", elapsed)
	}
	if !bus.isClosed() {
		t.Error("after the timeout the bus must count as closed")
	}
}
