// Package eventbus provides the event bus for asynchronous cross-module side
// effects (plan Sections 1 and 5.4).
//
// The contract is single, the backend is replaceable: NewInMemory builds an
// in-process bus for development and tests, NewRedisStream a durable bus on
// Redis Streams for production. Only the INTERFACE is common; delivery,
// ordering, concurrency and the CONTEXT given to a handler differ per backend
// and are defined one by one in the sections below. Before switching backends,
// make sure the handlers were written against the WEAKEST of these guarantees.
//
// # Delivery semantics
//
// Publish is asynchronous and does NOT WAIT for the handlers; the caller only
// learns that the event was accepted. That is why a handler's error never
// comes back to the caller. The InMemory backend delivers at most once
// (at-most-once) and the event is lost if the process dies; the Redis backend
// delivers at least once (at-least-once) and resumes where it left off when
// the process restarts. Handlers must therefore be written idempotently
// (plan Section 2.6).
//
// # Ordering and concurrency guarantees
//
// Even though the contract is single, the concurrency behavior of the two
// backends IS NOT THE SAME, and neither of them guarantees delivery order:
//
//   - InMemory runs every handler call in a separate goroutine. The same
//     handler can run with several events at once, and events can be delivered
//     in an order different from the publication order.
//   - The Redis backend processes a stream's messages in order in a single
//     consumer loop, but when several processes join the same group the
//     messages are distributed across the processes and the overall order is
//     again not preserved.
//
// That is why handlers must be written not only idempotently but also
// REENTRANTLY: shared state must be locked, order-dependent decisions must be
// made through Event.OccurredAt or a version field in the payload, and no
// "the previous event was already processed" assumption may be made. Strictly
// ordered, multi-step flows belong to internal/core/workflow's saga engine
// (plan Phase 3).
//
// # Error and retry policy
//
// If a handler panics, the panic is recovered, logged with a stack trace and
// the bus stays up; the other handlers are unaffected. If a handler returns an
// error, the error is logged and the event counts as processed — NO backend
// retries automatically. This is a deliberate decision: without a dead letter
// queue, redelivery lets a broken event (a poison pill) lock the consumer in
// an endless loop. Work needing retries and compensation belongs to
// internal/core/workflow's saga engine (plan Phase 3); a handler is of course free to
// retry inside itself.
//
// # Context and observability
//
// The CANCELLATION behavior of the ctx given to a handler is the same in both
// backends, its VALUES are not (see [Handler]): InMemory carries the event
// inside the process, so the values of the ctx given to Publish reach the
// handler, whereas Redis carries the event ACROSS PROCESSES and the consuming
// process never sees the publisher's ctx.
//
// The measured price of this is an observability difference, and it is
// DELIBERATELY left open: in an in-memory setup a handler's logs carry the
// request_id of the publishing request, in a Redis setup they DO NOT. That is,
// in a production setup the request that triggered an event and its side
// effect cannot be tied to each other through request_id.
//
// Closing the difference is technically cheap — request_id could be written
// into the message as a field and put back into the ctx on the consumer side —
// but it has THREE costs, and their sum is dearer than the single log field it
// would earn:
//
//   - The bus would have to KNOW which ctx values are worth carrying.
//     request_id is a key of the core's HTTP layer; carrying it binds this
//     package, which is a transport layer, to that layer, and every value
//     added to the list grows the binding.
//   - The result would be a HALF truth: a ctx with a filled request_id but an
//     empty logger, identity and tracing span pushes the handler's author into
//     guessing "which value is there". Today's rule leaves no room to guess:
//     under Redis there is NONE.
//   - At-least-once delivery can hand over the message after the process has
//     restarted too. The request_id put back at that moment would belong to a
//     request that finished long ago; the log line would look as if it
//     belonged to a live request, and that is more misleading than having no
//     correlation at all.
//
// The correlation path that works in both backends is the Event ITSELF:
// Event.ID travels with the message and is already logged on handler errors
// (event_id), and the publisher can EXPLICITLY put request_id into Event.Data.
// Data is the only thing both backends carry.
//
// This decision is reopened the day distributed tracing (trace context) is
// really wanted. Then the thing to carry is not request_id but the W3C
// traceparent, the place to carry it is again the message itself, and on the
// consumer side the span is continued EXPLICITLY rather than as a copied
// value.
package eventbus

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"log/slog"
	"maps"
	"runtime/debug"
	"sync"
	"time"

	"github.com/bdrtr/gobit/core/errors"
)

// Event is a single event carried on the bus.
type Event struct {
	// Name is the event's name in "module.action" form (e.g. "order.placed").
	// Subscriptions match on this name; in the Redis backend every name maps
	// to a separate stream. It cannot be left empty.
	Name string

	// Data is the payload the event carries. Because it is serialized to JSON
	// in the Redis backend, it must hold only JSON-convertible values.
	// Handlers must treat this map as READ ONLY; the top-level keys are copied
	// per handler, nested values keep being shared.
	Data map[string]any

	// ID is the event's unique id and may be supplied by the publisher; if it
	// is left empty a time-ordered id is generated. Consumers can use this id
	// as an idempotency key.
	ID string

	// OccurredAt is the moment the event happened and is always converted to
	// UTC. If the zero value is given, the moment of Publish is used.
	OccurredAt time.Time
}

// Handler is a function that processes an event.
//
// The given ctx INHERITS NO CANCELLATION in any backend: processing is not cut
// short when the caller's request ends, nor when Shutdown is called. The
// VALUES of the ctx, however, differ per backend:
//
//   - InMemory: the ctx is derived from the ctx given to Publish; request
//     values (e.g. request_id) are preserved.
//   - Redis: the event travels ACROSS PROCESSES. The consuming process NEVER
//     sees the publisher's ctx; the handler receives a ctx derived from the
//     bus's own root ctx that CARRIES no request value.
//
// A handler must therefore NOT RELY on the values in the ctx. Since the
// default backend is in-memory, a design that carries something in the ctx
// passes green in tests and in local development and reads silently empty in
// production: everything needed must be in Event.Data. For the form of
// correlation that works in both backends see "Context and observability" in
// the package comment.
//
// The returned error never reaches the caller, it is only logged.
type Handler func(ctx context.Context, e Event) error

// EventBus is the event publication and subscription contract.
type EventBus interface {
	// Publish publishes the event and returns without waiting for the
	// handlers.
	Publish(ctx context.Context, e Event) error
	// Subscribe binds a handler to the given event name.
	//
	// It deliberately takes no context.Context: a subscription is bound to the
	// process's lifetime, not to a request (the signature of plan Section
	// 5.4). Its lifecycle is managed through Shutdown.
	Subscribe(eventName string, h Handler) error
	// Shutdown closes the bus and waits for the running handlers to finish.
	//
	// The wait is bounded by ctx: if the budget runs out the stuck handlers
	// are not waited for and errors.KindUnavailable / CodeShutdownTimeout is
	// returned. The bus is closed in either case; after the return Publish and
	// Subscribe return errors. The signature is compatible with
	// container.Shutdowner.
	Shutdown(ctx context.Context) error
}

// Error codes; the caller can branch on these through errors.CodeOf.
const (
	// CodeClosed reports that a closed bus was used.
	CodeClosed = "eventbus_closed"
	// CodeInvalidEvent reports that the event is invalid or not serializable.
	CodeInvalidEvent = "eventbus_invalid_event"
	// CodeInvalidConfig reports that the bus configuration is invalid.
	CodeInvalidConfig = "eventbus_invalid_config"
	// CodePublishFailed reports that publication failed at the backend level.
	CodePublishFailed = "eventbus_publish_failed"
	// CodeSubscribeFailed reports that the subscription could not be set up.
	CodeSubscribeFailed = "eventbus_subscribe_failed"
	// CodeShutdownTimeout reports that the budget ran out while waiting for
	// the shutdown.
	CodeShutdownTimeout = "eventbus_shutdown_timeout"
)

// The fixed keys used in log records.
const (
	attrEvent   = "event"
	attrEventID = "event_id"
	attrError   = "error"
	attrStream  = "stream"
)

// idPrefix is the prefix of the generated event ids (plan Section 8).
const idPrefix = "evt_"

// idEncoding is padless encoding with the Crockford Base32 alphabet. Because
// the alphabet is in ascending order in ASCII, the encoded string keeps the
// same lexicographic order as the bytes it encodes; that is what keeps the ids
// sortable by time.
var idEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// newEventID generates a time-ordered and unique event id.
//
// Its structure is the same as a ULID's: a 48-bit millisecond timestamp plus
// 80 bits of cryptographic randomness, encoded into 26 characters with
// Crockford Base32.
func newEventID(t time.Time) string {
	ms := t.UTC().UnixMilli()
	if ms < 0 {
		// A timestamp before 1970 is meaningless for an event; it is clamped
		// to the floor so that the ordering is not broken.
		ms = 0
	}

	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], uint64(ms))

	var buf [16]byte
	// UnixMilli fits in 48 bits; the first two bytes are always zero and are
	// dropped.
	copy(buf[:6], stamp[2:])
	if _, err := rand.Read(buf[6:]); err != nil {
		// crypto/rand.Read does not return an error; should it ever do so, the
		// id rests only on nanosecond resolution — uniqueness weakens but
		// publication does not fail.
		binary.BigEndian.PutUint64(buf[8:], uint64(t.UnixNano()))
	}

	return idPrefix + idEncoding.EncodeToString(buf[:])
}

// normalize validates the event and fills in the fields left empty.
func normalize(e Event) (Event, error) {
	if e.Name == "" {
		return Event{}, errors.Invalid(CodeInvalidEvent, "the event name cannot be empty")
	}

	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now().UTC()
	} else {
		e.OccurredAt = e.OccurredAt.UTC()
	}

	if e.ID == "" {
		e.ID = newEventID(e.OccurredAt)
	}

	return e, nil
}

// deliverable builds the copy of the event handed to a handler.
//
// Data is shallow-copied, so that a change one handler makes to the top-level
// keys does not affect the other handlers running at the same time and does
// not cause a race. Nested values keep being shared.
func deliverable(e Event) Event {
	e.Data = maps.Clone(e.Data)
	return e
}

// invokeHandler calls the handler in a panic- and error-safe way.
//
// A panic is recovered and logged with a stack trace, an error is only logged;
// neither stops the bus nor leads to a retry (see the package comment).
func invokeHandler(ctx context.Context, log *slog.Logger, e Event, h Handler) {
	defer func() {
		if r := recover(); r != nil {
			log.ErrorContext(ctx, "the event handler panicked",
				attrEvent, e.Name,
				attrEventID, e.ID,
				"panic", r,
				"stack", string(debug.Stack()),
			)
		}
	}()

	if err := h(ctx, deliverable(e)); err != nil {
		log.ErrorContext(ctx, "the event handler returned an error",
			attrEvent, e.Name,
			attrEventID, e.ID,
			attrError, err,
		)
	}
}

// awaitHandlers waits for the running handlers to finish, bounded by ctx.
//
// If the ctx budget runs out the wait is abandoned and a typed error with the
// CodeShutdownTimeout code is returned; the stuck handlers end together with
// the process. Both backends' Shutdown share this behavior.
func awaitHandlers(ctx context.Context, wg *sync.WaitGroup) error {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return errors.Wrap(ctx.Err(), errors.KindUnavailable, CodeShutdownTimeout,
			"the budget ran out while closing the event bus; the running handlers could not be waited for")
	}
}

// closedPublishError builds the typed error for a publication on a closed bus.
func closedPublishError(eventName string) error {
	return errors.Unavailable(CodeClosed,
		"the event bus was closed: %q cannot be published", eventName)
}

// closedSubscribeError builds the typed error for a subscription on a closed
// bus.
func closedSubscribeError(eventName string) error {
	return errors.Unavailable(CodeClosed,
		"the event bus was closed: cannot subscribe to the %q event", eventName)
}

// orDefaultLogger returns the process's default logger in place of a nil
// logger.
func orDefaultLogger(log *slog.Logger) *slog.Logger {
	if log == nil {
		return slog.Default()
	}
	return log
}
