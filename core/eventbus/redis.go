package eventbus

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/bdrtr/gobit/core/errors"
)

// The default settings of the Redis backend.
const (
	// DefaultStreamPrefix is the default prefix of the stream keys. The
	// "order.placed" event is written to the "gobit:events:order.placed"
	// stream.
	//
	// The value is not spelled out by hand, it is DERIVED from [DefaultGroup]:
	// the two are the two faces of a single namespace and
	// [RedisConfig.WithNamespace] builds them from the same prefix. Had they
	// been spelled out separately, an edit changing one and forgetting the
	// other would leave the default setup silently half-separated.
	DefaultStreamPrefix = DefaultGroup + ":" + streamSegment
	// DefaultGroup is the default consumer group name.
	DefaultGroup = "gobit"
	// DefaultBlockTimeout is the default blocking duration of XREADGROUP.
	DefaultBlockTimeout = 5 * time.Second
	// DefaultBatchSize is the default number of messages read in one round.
	DefaultBatchSize = 16
	// DefaultMaxLen is the default approximate length limit of a stream.
	DefaultMaxLen = 10_000
	// MaxLenUnlimited turns stream trimming off when given to
	// RedisConfig.MaxLen.
	MaxLenUnlimited = -1
)

// The internal constants of the Redis backend.
const (
	// streamSegment is the fixed part between the namespace prefix and the
	// event name. A key is built as "<namespace>:events:<event name>"; the
	// presence of the segment keeps event streams from getting mixed up in
	// Redis with the guard keys using the same namespace ("<namespace>:rl:*",
	// "<namespace>:idem:*").
	streamSegment = "events"
	// cursorNew is XREADGROUP's marker for "messages not handed to any
	// consumer".
	cursorNew = ">"
	// cursorPending is XREADGROUP's marker for "this consumer's pending
	// messages".
	cursorPending = "0"
	// groupStart is the starting position of a newly created consumer group.
	// "0" is chosen so that events published before the subscription are
	// delivered too.
	groupStart = "0"
	// controlTimeout is the budget of the short-lived control commands
	// (XGROUP, XACK).
	controlTimeout = 5 * time.Second
	// readErrorBackoff is the pause after a read error; it keeps a broken
	// Redis from being sent thousands of requests per second.
	readErrorBackoff = time.Second
)

// The field names in a stream message.
const (
	fieldID         = "id"
	fieldName       = "name"
	fieldOccurredAt = "occurred_at"
	fieldData       = "data"
)

// RedisConfig holds the settings of the Redis Streams backend.
//
// Every field is optional; the ones left empty are filled in from the Default*
// constants.
type RedisConfig struct {
	// StreamPrefix is the prefix of the stream keys; a key is built as
	// "<StreamPrefix>:<event name>".
	StreamPrefix string

	// Group is the consumer group name. Consumers joined to the same group
	// receive a message only ONCE; that is how scaling is done. Different
	// group names consume the same event independently (fan-out).
	Group string

	// Consumer is the name identifying this process within the group. If the
	// same name is used when the process restarts, messages that were
	// processed but not ACKed (the pending list) are delivered to this process
	// again. If it is empty "<hostname>-<pid>" is used; if a stable identity
	// is wanted (a StatefulSet pod name, say) it must be given explicitly
	// (see [ConsumerName]).
	//
	// The SAME name must not be given to two PROCESSES, and this is the exact
	// opposite of [RedisConfig.Group]: sharing the group name is scaling
	// itself, while sharing the consumer name is sharing the pending list. At
	// startup every process reads the pending list of its own name (consume,
	// cursorPending), that is, it also picks up the messages the other one is
	// STILL processing, and the same event is processed twice. The bus cannot
	// see this — a single process knows nothing but itself.
	Consumer string

	// BlockTimeout is how long XREADGROUP blocks while waiting for a message.
	// A smaller value means faster shutdown but more empty rounds.
	BlockTimeout time.Duration

	// BatchSize is the maximum number of messages read in one round.
	BatchSize int64

	// MaxLen is the approximate upper bound of a stream (XADD MAXLEN ~ N);
	// older entries above that bound are trimmed. If MaxLenUnlimited is given,
	// trimming is turned off and the stream grows without bound.
	MaxLen int64
}

// StreamName returns the Redis stream key corresponding to the given event
// name. It is exported so that operations and tests do not have to build the
// key by hand.
func (c RedisConfig) StreamName(eventName string) string {
	return c.withDefaults().StreamPrefix + ":" + eventName
}

// WithNamespace returns a copy whose stream prefix and consumer group name are
// derived from the given namespace.
//
// # Why BOTH
//
// The events of two setups sharing the SAME Redis must be separated too, and
// separating only the stream key IS NOT ENOUGH. Sharing the group is worse: by
// the very definition of a consumer group, only ONE of the consumers in the
// group receives a message, meaning production's "order.placed" event can be
// consumed and swallowed by staging — the order confirmation never goes out
// and no error shows up anywhere. That both are separated by the one knob is
// therefore not a convenience but a requirement; had they been settable
// separately it would be possible to separate the stream and forget the group,
// and that half-measure would produce exactly the failure above.
//
// # Why Consumer is left alone
//
// That field separates the PROCESSES within a group, not the setups, and it
// has nothing to do with the namespace; its reasoning is in the
// [RedisConfig.Consumer] godoc.
//
// An empty namespace changes nothing: a headless key of the form ":events"
// would be a separation made without a NAME to separate by, and keeping the
// defaults (see [DefaultStreamPrefix]) is the more honest answer.
func (c RedisConfig) WithNamespace(namespace string) RedisConfig {
	if namespace == "" {
		return c
	}

	c.StreamPrefix = namespace + ":" + streamSegment
	c.Group = namespace

	return c
}

// withDefaults returns a copy whose empty fields are filled in with the
// defaults.
func (c RedisConfig) withDefaults() RedisConfig {
	if c.StreamPrefix == "" {
		c.StreamPrefix = DefaultStreamPrefix
	}
	if c.Group == "" {
		c.Group = DefaultGroup
	}
	c.Consumer = ConsumerName(c.Consumer)
	if c.BlockTimeout <= 0 {
		c.BlockTimeout = DefaultBlockTimeout
	}
	if c.BatchSize <= 0 {
		c.BatchSize = DefaultBatchSize
	}
	if c.MaxLen == 0 {
		c.MaxLen = DefaultMaxLen
	}
	return c
}

// ConsumerName completes the given consumer name, deriving a per-process name
// if it was left empty.
//
// [RedisConfig.withDefaults] already does the same job; this function is
// EXPORTED so that the side building the bus CAN LOG the name that will be
// used at startup. Logging is the only chance of noticing: giving the same
// name to two processes leads to double processing (see
// [RedisConfig.Consumer]), and that is a failure only visible when two startup
// logs are put side by side — it produces no error at runtime, some events are
// simply processed twice.
func ConsumerName(name string) string {
	if name != "" {
		return name
	}

	return defaultConsumerName()
}

// defaultConsumerName builds a name distinguishing the process within the
// group.
func defaultConsumerName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "gobit"
	}
	return host + "-" + strconv.Itoa(os.Getpid())
}

// streamClient is the set of Redis Streams commands redisBus uses.
//
// In production *redis.Client satisfies it directly. The reason for a separate
// interface is to keep the consumption loop (consume, dispatch, ack) open to
// unit testing without Docker; the signatures are identical to go-redis's so
// that no adapter is needed.
type streamClient interface {
	XAdd(ctx context.Context, a *redis.XAddArgs) *redis.StringCmd
	XReadGroup(ctx context.Context, a *redis.XReadGroupArgs) *redis.XStreamSliceCmd
	XAck(ctx context.Context, stream, group string, ids ...string) *redis.IntCmd
	XGroupCreateMkStream(ctx context.Context, stream, group, start string) *redis.StatusCmd
}

var _ streamClient = (*redis.Client)(nil)

// redisBus is a durable EventBus built on Redis Streams.
type redisBus struct {
	client streamClient
	cfg    RedisConfig
	log    *slog.Logger

	// ctx governs the lifetime of the consumer loops. Because Subscribe takes
	// no context.Context by contract, the bus carries its own root; Shutdown
	// triggers its cancellation.
	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.RWMutex
	closed   bool
	handlers map[string][]Handler

	// setupMu serializes consumer setup and guards the consuming map. The
	// reason it is SEPARATE from mu is that creating a consumer group is a
	// real network round trip: done under mu's write lock, every concurrent
	// Publish (mu.RLock) would block for that whole time while Redis is slow.
	setupMu   sync.Mutex
	consuming map[string]struct{}

	wg sync.WaitGroup
}

var _ EventBus = (*redisBus)(nil)

// NewRedisStream builds an EventBus based on Redis Streams.
//
// Every event name maps to a separate stream ("<prefix>:<event name>") and
// every subscription to the cfg.Group consumer group; a processed message is
// XACKed and consumption resumes where it left off when the process restarts.
// The client is owned by the caller: Shutdown does not close it.
//
// If log is nil, slog.Default is used. If client is nil, errors.KindInvalid is
// returned.
func NewRedisStream(client *redis.Client, cfg RedisConfig, log *slog.Logger) (EventBus, error) {
	if client == nil {
		return nil, errors.Invalid(CodeInvalidConfig, "the redis client cannot be nil")
	}
	return newRedisBus(client, cfg, log), nil
}

// newRedisBus builds the bus over the client abstraction.
//
// The in-package tests use it to exercise the consumption loop without a real
// Redis; the exported path is NewRedisStream.
func newRedisBus(client streamClient, cfg RedisConfig, log *slog.Logger) *redisBus {
	ctx, cancel := context.WithCancel(context.Background())
	return &redisBus{
		client:    client,
		cfg:       cfg.withDefaults(),
		log:       orDefaultLogger(log),
		ctx:       ctx,
		cancel:    cancel,
		handlers:  make(map[string][]Handler),
		consuming: make(map[string]struct{}),
	}
}

// Publish writes the event to the matching stream and returns without waiting
// for the handlers.
//
// Data is serialized to JSON; if it holds a value that cannot be serialized
// errors.KindInvalid is returned, and if it cannot be written to Redis
// errors.KindUnavailable.
func (b *redisBus) Publish(ctx context.Context, e Event) error {
	e, err := normalize(e)
	if err != nil {
		return err
	}

	if b.isClosed() {
		return closedPublishError(e.Name)
	}

	payload, err := json.Marshal(e.Data)
	if err != nil {
		return errors.Wrap(err, errors.KindInvalid, CodeInvalidEvent,
			"the data of the %q event could not be converted to JSON", e.Name)
	}

	args := &redis.XAddArgs{
		Stream: b.cfg.StreamName(e.Name),
		Values: map[string]any{
			fieldID:         e.ID,
			fieldName:       e.Name,
			fieldOccurredAt: e.OccurredAt.Format(time.RFC3339Nano),
			fieldData:       string(payload),
		},
	}
	if b.cfg.MaxLen > 0 {
		// Approximate trimming (~) is chosen: it lets Redis stop at a radix
		// node boundary and is far cheaper than exact trimming.
		args.MaxLen = b.cfg.MaxLen
		args.Approx = true
	}

	if err := b.client.XAdd(ctx, args).Err(); err != nil {
		return errors.Wrap(err, errors.KindUnavailable, CodePublishFailed,
			"the %q event could not be written to the redis stream", e.Name)
	}

	return nil
}

// Subscribe binds a handler to the given event name.
//
// On the first subscription the stream and the consumer group are created
// (reused if they already exist) and a single consumer loop is started for
// that event. Later handlers bound to the same name are fed from the same
// loop; that is, the message is read from the group once and given to every
// handler inside the process.
//
// The network round trip of creating the consumer group happens OUTSIDE the
// lock Publish uses: even when Redis is slow or unreachable, concurrent
// publications do not block, only this call waits.
func (b *redisBus) Subscribe(eventName string, h Handler) error {
	if eventName == "" {
		return errors.Invalid(CodeSubscribeFailed, "the event name to subscribe to cannot be empty")
	}
	if h == nil {
		return errors.Invalid(CodeSubscribeFailed, "the handler for %q cannot be nil", eventName)
	}

	// setupMu serializes concurrent Subscribes; because mu stays free, Publish
	// keeps working throughout the network round trip below.
	b.setupMu.Lock()
	defer b.setupMu.Unlock()

	if b.isClosed() {
		return closedSubscribeError(eventName)
	}

	_, consuming := b.consuming[eventName]
	if !consuming {
		if err := b.ensureGroup(eventName); err != nil {
			return err
		}
	}

	b.mu.Lock()
	if b.closed {
		// Shutdown was called while the network round trip was in flight;
		// never start the loop.
		b.mu.Unlock()
		return closedSubscribeError(eventName)
	}
	if !consuming {
		// wg.Add happens under the same lock as the closed check, so that
		// Shutdown's Wait does not miss this loop.
		b.wg.Add(1)
	}
	b.handlers[eventName] = append(b.handlers[eventName], h)
	b.mu.Unlock()

	if !consuming {
		b.consuming[eventName] = struct{}{}
		go b.consume(eventName)
	}
	return nil
}

// Shutdown stops the consumer loops and waits for the events being processed
// to finish.
//
// After the return Publish and Subscribe return errors. The wait is bounded by
// ctx: if the loops and the handlers complete before the ctx expires it
// returns nil and no goroutine is left running; if the budget runs out the
// stuck handlers are NOT WAITED FOR and errors.KindUnavailable /
// CodeShutdownTimeout is returned. Because the client belongs to the caller it
// is not closed. It can be called several times.
func (b *redisBus) Shutdown(ctx context.Context) error {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()

	b.cancel()
	return awaitHandlers(ctx, &b.wg)
}

// isClosed reports whether the bus has been closed.
func (b *redisBus) isClosed() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.closed
}

// ensureGroup makes sure the stream and the consumer group exist for the
// event. If the group already exists (BUSYGROUP) that is not an error.
func (b *redisBus) ensureGroup(eventName string) error {
	ctx, cancel := context.WithTimeout(b.ctx, controlTimeout)
	defer cancel()

	err := b.client.XGroupCreateMkStream(ctx, b.cfg.StreamName(eventName), b.cfg.Group, groupStart).Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return errors.Wrap(err, errors.KindUnavailable, CodeSubscribeFailed,
			"the consumer group for the %q event could not be created", eventName)
	}
	return nil
}

// consume continuously reads the stream of a single event name.
//
// First the messages this consumer took earlier but did not ACK (the pending
// list) are read; that is what lets the process resume where it left off after
// a restart. Once the list is drained it switches to the ">" marker and waits
// only for new messages.
func (b *redisBus) consume(eventName string) {
	defer b.wg.Done()

	stream := b.cfg.StreamName(eventName)
	cursor := cursorPending

	for {
		if b.ctx.Err() != nil {
			return
		}

		res, err := b.client.XReadGroup(b.ctx, &redis.XReadGroupArgs{
			Group:    b.cfg.Group,
			Consumer: b.cfg.Consumer,
			Streams:  []string{stream, cursor},
			Count:    b.cfg.BatchSize,
			Block:    b.cfg.BlockTimeout,
		}).Result()

		switch {
		case err == nil:
			// The message(s) were read; they are processed below.
		case errors.Is(err, redis.Nil):
			// The blocking budget ran out, there is no new message.
			continue
		case b.ctx.Err() != nil:
			// Shutdown was called; that is why the read was interrupted.
			return
		default:
			b.log.ErrorContext(b.ctx, "the event stream could not be read",
				attrStream, stream, attrError, err)
			if !b.sleep(readErrorBackoff) {
				return
			}
			continue
		}

		msgs := messagesOf(res, stream)

		if cursor != cursorNew {
			if len(msgs) == 0 {
				// No pending message is left; from here on it is only new
				// messages.
				cursor = cursorNew
				continue
			}
			// Paging through the pending list: the next round continues from
			// the last id.
			cursor = msgs[len(msgs)-1].ID
		}

		for _, msg := range msgs {
			b.dispatch(stream, eventName, msg)
		}
	}
}

// dispatch decodes a single message, gives it to the handlers and ACKs it.
//
// The message is ACKed even if a handler returns an error or panics: there is
// deliberately no redelivery policy (see the package comment). A message that
// cannot be decoded is logged and ACKed too; otherwise it would stay in the
// pending list forever.
func (b *redisBus) dispatch(stream, eventName string, msg redis.XMessage) {
	defer b.ack(stream, msg.ID)

	e, err := decodeMessage(eventName, msg)
	if err != nil {
		b.log.ErrorContext(b.ctx, "the event message could not be decoded",
			attrStream, stream, "message_id", msg.ID, attrError, err)
		return
	}

	b.mu.RLock()
	handlers := slices.Clone(b.handlers[eventName])
	b.mu.RUnlock()

	// The handlers receive a ctx derived from the bus's root that does not
	// inherit its cancellation; that way processing is not cut short when
	// Shutdown is called and Shutdown waits for it to finish (a graceful
	// shutdown).
	//
	// The root is NOT the publisher's ctx and cannot be: the message may have
	// come from another process. The publisher's request values (e.g.
	// request_id) are therefore ABSENT here — this is the single point where
	// we diverge from the in-memory backend, and the reasoning is in "Context
	// and observability" in the package comment.
	hctx := context.WithoutCancel(b.ctx)
	for _, h := range handlers {
		invokeHandler(hctx, b.log, e, h)
	}
}

// ack marks the message as processed in the consumer group.
// It uses a ctx that does not inherit cancellation so that it can also run
// while the bus is closing.
func (b *redisBus) ack(stream, messageID string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(b.ctx), controlTimeout)
	defer cancel()

	if err := b.client.XAck(ctx, stream, b.cfg.Group, messageID).Err(); err != nil {
		b.log.ErrorContext(ctx, "the event message could not be ACKed",
			attrStream, stream, "message_id", messageID, attrError, err)
	}
}

// sleep waits for the given duration; it returns false if the bus is closed
// meanwhile.
func (b *redisBus) sleep(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-b.ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// messagesOf returns the messages of the relevant stream inside the response.
func messagesOf(res []redis.XStream, stream string) []redis.XMessage {
	for _, s := range res {
		if s.Stream == stream {
			return s.Messages
		}
	}
	return nil
}

// decodeMessage converts a stream message into an Event.
// eventName is the fallback used when the message carries no name field.
//
// A message whose field map is empty counts as INVALID. Redis returns entries
// that sit in the pending list but were deleted from the stream or trimmed
// away by MAXLEN without fields (a tombstone), and go-redis turns that into an
// XMessage without an error. Returning an error keeps a fake event with no id
// and no data from reaching the handlers; the caller logs the message and ACKs
// it to clear it from the pending list.
func decodeMessage(eventName string, msg redis.XMessage) (Event, error) {
	if len(msg.Values) == 0 {
		return Event{}, errors.Invalid(CodeInvalidEvent,
			"the message has no fields; it may have been deleted from the stream or trimmed")
	}

	e := Event{
		Name: eventName,
		ID:   stringField(msg.Values, fieldID),
	}

	if name := stringField(msg.Values, fieldName); name != "" {
		e.Name = name
	}

	if raw := stringField(msg.Values, fieldOccurredAt); raw != "" {
		t, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return Event{}, errors.Wrap(err, errors.KindInvalid, CodeInvalidEvent,
				"the event time could not be parsed: %q", raw)
		}
		e.OccurredAt = t.UTC()
	}

	if raw := stringField(msg.Values, fieldData); raw != "" {
		if err := json.Unmarshal([]byte(raw), &e.Data); err != nil {
			return Event{}, errors.Wrap(err, errors.KindInvalid, CodeInvalidEvent,
				"the event data could not be decoded as JSON")
		}
	}

	return e, nil
}

// stringField converts a field coming from Redis into a string; if the field
// is absent it returns an empty string.
func stringField(values map[string]any, key string) string {
	if v, ok := values[key].(string); ok {
		return v
	}
	return ""
}
