package eventbus

import (
	"context"
	"log/slog"
	"sync"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// inMemoryBus is an EventBus that runs inside the process and has no
// durability.
type inMemoryBus struct {
	log *slog.Logger

	// mu guards the handlers and closed fields. An RWMutex was chosen because
	// Publish (a read) is the hot path while Subscribe (a write) happens only
	// at startup.
	mu       sync.RWMutex
	closed   bool
	handlers map[string][]Handler

	// wg counts the running handler goroutines; Shutdown waits for it to
	// finish.
	wg sync.WaitGroup
}

var _ EventBus = (*inMemoryBus)(nil)

// NewInMemory builds an EventBus that runs inside the process.
//
// It is for development, tests and single-process setups: events are not
// durable, and undelivered events are lost if the process dies. For production
// NewRedisStream must be used. If log is nil, slog.Default is used.
func NewInMemory(log *slog.Logger) EventBus {
	return &inMemoryBus{
		log:      orDefaultLogger(log),
		handlers: make(map[string][]Handler),
	}
}

// Publish distributes the event asynchronously to every handler subscribed to
// its name.
//
// The handlers are not waited for; each runs in its OWN goroutine and the
// return only reports that the event was accepted. That is why the same
// handler can run with several events at once and the delivery order may
// differ from the publication order (see the ordering and concurrency
// guarantees in the package comment). If there is no subscriber the event is
// swallowed silently. If the bus was closed, errors.KindUnavailable is
// returned.
func (b *inMemoryBus) Publish(ctx context.Context, e Event) error {
	e, err := normalize(e)
	if err != nil {
		return err
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.closed {
		return closedPublishError(e.Name)
	}

	handlers := b.handlers[e.Name]
	if len(handlers) == 0 {
		return nil
	}

	// Handlers live independently of the caller's request: the ctx's values
	// (e.g. request_id) are preserved, its cancellation is not inherited.
	// Otherwise the side effect would be cut short the moment the request
	// ended.
	//
	// Preserving the values is a property of THIS backend ONLY; in the Redis
	// backend the event crosses the process boundary and the publisher's ctx
	// does not cross with it (see [Handler]). Handlers therefore cannot rely
	// on the values.
	hctx := context.WithoutCancel(ctx)

	// wg.Add happens under the RLock; because Shutdown marks closed under the
	// write lock, the counter does not grow past this point and does not race
	// with Wait.
	b.wg.Add(len(handlers))
	for _, h := range handlers {
		go func() {
			defer b.wg.Done()
			invokeHandler(hctx, b.log, e, h)
		}()
	}

	return nil
}

// Subscribe binds a handler to the given event name.
//
// The same name can be subscribed to several times; all of them are called on
// publication. If the bus was closed, errors.KindUnavailable is returned.
func (b *inMemoryBus) Subscribe(eventName string, h Handler) error {
	if eventName == "" {
		return errors.Invalid(CodeSubscribeFailed, "the event name to subscribe to cannot be empty")
	}
	if h == nil {
		return errors.Invalid(CodeSubscribeFailed, "the handler for %q cannot be nil", eventName)
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return closedSubscribeError(eventName)
	}

	b.handlers[eventName] = append(b.handlers[eventName], h)
	return nil
}

// Shutdown closes the bus and waits for the running handlers to finish.
//
// After the return Publish and Subscribe return errors. The wait is bounded by
// ctx: if every handler completes before the ctx expires it returns nil and no
// goroutine is left running; if the budget runs out the stuck handlers are NOT
// WAITED FOR and errors.KindUnavailable / CodeShutdownTimeout is returned (the
// bus is closed either way). It can be called several times. If it is called
// from inside a handler it only returns when the ctx budget runs out —
// Shutdown must always be called from outside the handlers.
func (b *inMemoryBus) Shutdown(ctx context.Context) error {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()

	return awaitHandlers(ctx, &b.wg)
}
