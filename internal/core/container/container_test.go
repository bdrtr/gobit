package container_test

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/errors"
)

// --- test helpers -----------------------------------------------------------

// recorder records the shutdown calls in order.
type recorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *recorder) add(call string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
}

func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

// closerSvc satisfies only io.Closer.
type closerSvc struct {
	name string
	rec  *recorder
	err  error
}

func (s *closerSvc) Close() error {
	s.rec.add("close:" + s.name)
	return s.err
}

// shutdownSvc satisfies only Shutdown(ctx) error.
type shutdownSvc struct {
	name string
	rec  *recorder
	err  error
}

func (s *shutdownSvc) Shutdown(_ context.Context) error {
	s.rec.add("shutdown:" + s.name)
	return s.err
}

// panicOnCloseSvc panics on shutdown (it stands in for the third-party clients
// that panic on a double close).
type panicOnCloseSvc struct{}

func (panicOnCloseSvc) Close() error { panic("the shutdown blew up") }

// bothSvc satisfies both interfaces; Shutdown must be preferred.
type bothSvc struct {
	name string
	rec  *recorder
}

func (s *bothSvc) Close() error {
	s.rec.add("close:" + s.name)
	return nil
}

func (s *bothSvc) Shutdown(_ context.Context) error {
	s.rec.add("shutdown:" + s.name)
	return nil
}

// productService stands in for the providing module's concrete service.
type productService struct{ id string }

func (p productService) GetVariant(_ context.Context, variantID string) (string, error) {
	return p.id + ":" + variantID, nil
}

// productReader is the narrow consumer-side interface from ADR 0001.
type productReader interface {
	GetVariant(ctx context.Context, variantID string) (string, error)
}

// stockReader is an interface productService does not satisfy.
type stockReader interface {
	Reserve(ctx context.Context, variantID string, qty int) error
}

// wrongVariantReader carries the same name but a mismatched signature.
type wrongVariantReader interface {
	GetVariant(ctx context.Context, variantID string) (int, error)
}

// pointerService's methods have pointer receivers.
type pointerService struct{}

func (p *pointerService) Ping(_ context.Context) error { return nil }

type pinger interface {
	Ping(ctx context.Context) error
}

// logRecorder is a slog handler collecting the records logged.
type logRecorder struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *logRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (h *logRecorder) Handle(_ context.Context, rec slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, rec.Clone())
	return nil
}

func (h *logRecorder) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *logRecorder) WithGroup(string) slog.Handler { return h }

// has reports whether there is a record at the given level whose message
// contains substr.
func (h *logRecorder) has(level slog.Level, substr string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.records {
		if h.records[i].Level == level && strings.Contains(h.records[i].Message, substr) {
			return true
		}
	}
	return false
}

// mustFinish fails the test when fn does not finish in time; it guards against a deadlock.
func mustFinish(t *testing.T, d time.Duration, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("the operation did not finish within %s; there may be a deadlock", d)
	}
}

// --- registration and resolution --------------------------------------------

func TestProvideValueAndResolve(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide("product.service", productService{id: "p1"}))

	got, err := container.Resolve[productService](c, "product.service")
	require.NoError(t, err)
	require.Equal(t, "p1", got.id)
}

func TestLazyCtorRunsOnlyOnFirstResolve(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	c := container.New(nil)
	require.NoError(t, c.Provide("lazy", func(_ *container.Container) (any, error) {
		calls.Add(1)
		return productService{id: "lazy"}, nil
	}))

	// The registration is visible but the constructor must not have run yet.
	require.True(t, c.Has("lazy"))
	require.Equal(t, []string{"lazy"}, c.Names())
	require.Equal(t, int64(0), calls.Load())

	first, err := container.Resolve[productService](c, "lazy")
	require.NoError(t, err)
	require.Equal(t, int64(1), calls.Load())

	second, err := container.Resolve[productService](c, "lazy")
	require.NoError(t, err)
	require.Equal(t, int64(1), calls.Load(), "the constructor does not run again on the second Resolve")
	require.Equal(t, first, second)
}

func TestSingletonUnderConcurrentResolve(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	c := container.New(nil)
	require.NoError(t, c.Provide("singleton", func(_ *container.Container) (any, error) {
		calls.Add(1)
		time.Sleep(5 * time.Millisecond) // make the race visible
		return &productService{id: "only"}, nil
	}))

	const n = 100
	results := make([]*productService, n)
	errs := make([]error, n)

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i], errs[i] = container.Resolve[*productService](c, "singleton")
		}()
	}
	close(start)
	wg.Wait()

	require.Equal(t, int64(1), calls.Load(), "the constructor must run exactly once")
	for i := range n {
		require.NoError(t, errs[i])
		require.Same(t, results[0], results[i], "every resolution must give the same instance")
	}
}

func TestResolveUnknownNameIsNotFound(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide("product.service", productService{}))

	_, err := container.Resolve[productService](c, "pricing.service")
	require.Error(t, err)
	require.True(t, errors.IsNotFound(err))
	require.Contains(t, err.Error(), "pricing.service")
	require.Contains(t, err.Error(), "product.service", "the message must list the registered names")
}

func TestDuplicateProvideIsConflict(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide("dummy.service", productService{id: "first"}))

	err := c.Provide("dummy.service", productService{id: "second"})
	require.Error(t, err)
	require.True(t, errors.IsConflict(err))
	require.Contains(t, err.Error(), "dummy.service")

	// The first registration must be preserved.
	got, err := container.Resolve[productService](c, "dummy.service")
	require.NoError(t, err)
	require.Equal(t, "first", got.id)
}

func TestProvideRejectsInvalidArgs(t *testing.T) {
	t.Parallel()

	c := container.New(nil)

	require.True(t, errors.IsInvalid(c.Provide("", productService{})))
	require.True(t, errors.IsInvalid(c.Provide("nil.value", nil)))

	var nilCtor func(*container.Container) (any, error)
	require.True(t, errors.IsInvalid(c.Provide("nil.ctor", nilCtor)))
}

func TestProvideRejectsTypedNil(t *testing.T) {
	t.Parallel()

	c := container.New(nil)

	// (*closerSvc)(nil) is not INTERFACE-nil: it passes the value == nil check
	// but cannot be used as a service.
	var svc *closerSvc
	err := c.Provide("typed.nil", svc)
	require.Error(t, err)
	require.True(t, errors.IsInvalid(err))
	require.Equal(t, "container_invalid_ctor", errors.CodeOf(err))
	require.Contains(t, err.Error(), "*container_test.closerSvc", "the message must name the given type")
	require.False(t, c.Has("typed.nil"), "a nil service must not enter the container")
}

func TestProvideRejectsWrongCtorSignature(t *testing.T) {
	t.Parallel()

	c := container.New(nil)

	// A constructor returning a concrete type does not match the Ctor
	// signature; it must not be registered silently as a VALUE, it must be
	// rejected at registration.
	err := c.Provide("typed.ctor", func(_ *container.Container) (*closerSvc, error) {
		return &closerSvc{}, nil
	})
	require.Error(t, err)
	require.True(t, errors.IsInvalid(err))
	require.Equal(t, "container_invalid_ctor", errors.CodeOf(err))
	require.Contains(t, err.Error(), "func(*container.Container) (any, error)", "the expected signature must be shown")
	require.False(t, c.Has("typed.ctor"))

	// A function that does not take *Container can still be registered as a
	// ready value.
	require.NoError(t, c.Provide("factory", func() string { return "x" }))
	fn, err := container.Resolve[func() string](c, "factory")
	require.NoError(t, err)
	require.Equal(t, "x", fn())
}

// --- type mismatch diagnosis (ADR 0001) -------------------------------------

func TestResolveConsumerInterface(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide("product.service", productService{id: "prod"}))

	// The consumer resolves through its own narrow interface without importing
	// the provider.
	reader, err := container.Resolve[productReader](c, "product.service")
	require.NoError(t, err)

	variant, err := reader.GetVariant(t.Context(), "v1")
	require.NoError(t, err)
	require.Equal(t, "prod:v1", variant)
}

func TestResolveTypeMismatchNamesBothTypes(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide("product.service", productService{id: "prod"}))

	_, err := container.Resolve[stockReader](c, "product.service")
	require.Error(t, err)
	require.True(t, errors.IsInvalid(err))

	msg := err.Error()
	require.Contains(t, msg, "container_test.productService", "the registered concrete type must be named")
	require.Contains(t, msg, "container_test.stockReader", "the expected type must be named")
	require.Contains(t, msg, "missing: Reserve(context.Context, string, int) error")
}

func TestResolveTypeMismatchExplainsSignature(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide("product.service", productService{}))

	_, err := container.Resolve[wrongVariantReader](c, "product.service")
	require.Error(t, err)
	require.Contains(t, err.Error(), "mismatched: GetVariant(context.Context, string) (int, error)")
	require.Contains(t, err.Error(), "registered: GetVariant(context.Context, string) (string, error)")
}

func TestResolveTypeMismatchHintsPointerReceiver(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide("ptr.service", pointerService{}))

	_, err := container.Resolve[pinger](c, "ptr.service")
	require.Error(t, err)
	require.Contains(t, err.Error(), "have pointer receivers")
	require.Contains(t, err.Error(), "*container_test.pointerService")
}

func TestResolveTypeMismatchOnConcreteType(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide("product.service", productService{}))

	_, err := container.Resolve[*productService](c, "product.service")
	require.Error(t, err)
	require.Contains(t, err.Error(), "container_test.productService")
	require.Contains(t, err.Error(), "*container_test.productService")
}

// --- dependency chain and cycle ---------------------------------------------

func TestCtorResolvesDependencyChain(t *testing.T) {
	t.Parallel()

	c := container.New(nil)

	// c depends on b, b on a. The registration order is deliberately reversed:
	// thanks to the lazy constructor, a name that is not registered yet can be
	// depended on.
	require.NoError(t, c.Provide("c.service", func(cc *container.Container) (any, error) {
		dep, err := container.Resolve[string](cc, "b.service")
		if err != nil {
			return nil, err
		}
		return dep + ">c", nil
	}))
	require.NoError(t, c.Provide("b.service", func(cc *container.Container) (any, error) {
		dep, err := container.Resolve[string](cc, "a.service")
		if err != nil {
			return nil, err
		}
		return dep + ">b", nil
	}))
	require.NoError(t, c.Provide("a.service", "a"))

	got, err := container.Resolve[string](c, "c.service")
	require.NoError(t, err)
	require.Equal(t, "a>b>c", got)
}

func TestDependencyCycleIsReportedNotDeadlocked(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide("a", func(cc *container.Container) (any, error) {
		return container.Resolve[string](cc, "b")
	}))
	require.NoError(t, c.Provide("b", func(cc *container.Container) (any, error) {
		return container.Resolve[string](cc, "a")
	}))

	var err error
	mustFinish(t, 5*time.Second, func() {
		_, err = container.Resolve[string](c, "a")
	})

	require.Error(t, err)
	require.True(t, errors.IsConflict(err))
	require.Contains(t, err.Error(), "dependency cycle: a -> b -> a")
}

func TestSelfDependencyCycle(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide("self", func(cc *container.Container) (any, error) {
		return container.Resolve[string](cc, "self")
	}))

	var err error
	mustFinish(t, 5*time.Second, func() {
		_, err = container.Resolve[string](c, "self")
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "dependency cycle: self -> self")
}

func TestCycleAcrossGoroutines(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	started := make(chan struct{}, 2)
	release := make(chan struct{})

	provide := func(name, dep string) {
		require.NoError(t, c.Provide(name, func(cc *container.Container) (any, error) {
			started <- struct{}{}
			<-release
			return container.Resolve[string](cc, dep)
		}))
	}
	provide("a", "b")
	provide("b", "a")

	errs := make([]error, 2)
	mustFinish(t, 5*time.Second, func() {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, errs[0] = container.Resolve[string](c, "a")
		}()
		go func() {
			defer wg.Done()
			_, errs[1] = container.Resolve[string](c, "b")
		}()
		// Wait until both constructors have taken over their own registration,
		// then release them.
		<-started
		<-started
		close(release)
		wg.Wait()
	})

	require.Error(t, errs[0])
	require.Error(t, errs[1])
	require.True(t,
		strings.Contains(errs[0].Error(), "dependency cycle") ||
			strings.Contains(errs[1].Error(), "dependency cycle"),
		"a cycle across two goroutines must be reported too: %v / %v", errs[0], errs[1])
}

func TestCycleDetectedWhenCtorResolvesConcurrently(t *testing.T) {
	t.Parallel()

	// The "a" constructor calls TWO Resolves at once: "b", which closes the
	// cycle, and the independent "slow". Had the wait graph kept a single edge
	// per node, the second edge would overwrite the first, the cycle would go
	// unseen and the container would deadlock.
	c := container.New(nil)
	bStarted := make(chan struct{})
	slowStarted := make(chan struct{})
	letB := make(chan struct{})
	releaseSlow := make(chan struct{})

	var bErr error
	require.NoError(t, c.Provide("a", func(cc *container.Container) (any, error) {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, bErr = container.Resolve[string](cc, "b")
		}()
		<-bStarted // the a -> b edge was added

		go func() {
			defer wg.Done()
			_, _ = container.Resolve[string](cc, "slow")
		}()
		<-slowStarted // the a -> slow edge was added too

		close(letB)
		close(releaseSlow)
		wg.Wait()
		return "a", nil
	}))
	require.NoError(t, c.Provide("b", func(cc *container.Container) (any, error) {
		close(bStarted)
		<-letB
		return container.Resolve[string](cc, "a")
	}))
	require.NoError(t, c.Provide("slow", func(_ *container.Container) (any, error) {
		close(slowStarted)
		<-releaseSlow
		return "slow", nil
	}))

	var (
		got string
		err error
	)
	mustFinish(t, 5*time.Second, func() {
		got, err = container.Resolve[string](c, "a")
	})

	require.NoError(t, err)
	require.Equal(t, "a", got)
	require.Error(t, bErr)
	require.True(t, errors.IsConflict(bErr))
	require.Contains(t, bErr.Error(), "dependency cycle: a -> b -> a")
}

func TestSlowBuildLogsWaitWarning(t *testing.T) {
	t.Parallel()

	// A cycle closed by constructors capturing the root container in a closure
	// is invisible in the graph and the wait lasts forever. It must at least be
	// logged.
	logs := &logRecorder{}
	c := container.New(slog.New(logs))
	container.SetWaitWarn(c, 10*time.Millisecond)

	started := make(chan struct{})
	release := make(chan struct{})
	require.NoError(t, c.Provide("slow", func(_ *container.Container) (any, error) {
		close(started)
		<-release
		return "ready", nil
	}))

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = container.Resolve[string](c, "slow")
	}()
	<-started
	go func() {
		defer wg.Done()
		_, _ = container.Resolve[string](c, "slow") // waits for the building goroutine
	}()

	require.Eventually(t, func() bool {
		return logs.has(slog.LevelWarn, "there may be a dependency cycle")
	}, 5*time.Second, 5*time.Millisecond, "a long build wait must log a warning")

	close(release)
	wg.Wait()
}

// --- constructor errors -----------------------------------------------------

func TestCtorErrorIsCachedAndRunsOnce(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	boom := errors.NotFound("dep_missing", "the dependency is missing")

	c := container.New(nil)
	require.NoError(t, c.Provide("broken", func(_ *container.Container) (any, error) {
		calls.Add(1)
		return nil, boom
	}))

	for range 3 {
		_, err := container.Resolve[string](c, "broken")
		require.Error(t, err)
		require.ErrorIs(t, err, boom)
		// The constructor's error class is preserved.
		require.True(t, errors.IsNotFound(err))
		require.Contains(t, err.Error(), `the service "broken" could not be built`)
	}
	require.Equal(t, int64(1), calls.Load(), "the error must be cached; the constructor does not run again")
}

func TestCtorPanicBecomesError(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide("panicky", func(_ *container.Container) (any, error) {
		panic("the connection string is empty")
	}))

	var err error
	mustFinish(t, 5*time.Second, func() {
		_, err = container.Resolve[string](c, "panicky")
	})

	require.Error(t, err)
	require.Equal(t, "container_ctor_panic", errors.CodeOf(err))
	require.Contains(t, err.Error(), "the connection string is empty")
}

func TestCtorReturningNilIsError(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide("nilly", func(_ *container.Container) (any, error) {
		return nil, nil
	}))

	_, err := container.Resolve[productReader](c, "nilly")
	require.Error(t, err)
	require.Equal(t, "container_ctor_nil", errors.CodeOf(err))
}

func TestCtorReturningTypedNilIsError(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide("typed.nilly", func(_ *container.Container) (any, error) {
		// A common typo on the error path: returning a zero-valued pointer
		// instead of nil.
		var svc *closerSvc
		return svc, nil
	}))

	_, err := container.Resolve[*closerSvc](c, "typed.nilly")
	require.Error(t, err)
	require.Equal(t, "container_ctor_nil", errors.CodeOf(err))
	require.Contains(t, err.Error(), "*container_test.closerSvc", "the message must name the returned type")
}

// --- MustResolve ------------------------------------------------------------

func TestMustResolve(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide("product.service", productService{id: "p"}))

	require.NotPanics(t, func() {
		got := container.MustResolve[productReader](c, "product.service")
		require.NotNil(t, got)
	})
	require.Panics(t, func() {
		_ = container.MustResolve[productReader](c, "missing")
	})
}

// --- Names / Has ------------------------------------------------------------

func TestNamesAreSorted(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	for _, name := range []string{"pricing.service", "auth.service", "product.service"} {
		require.NoError(t, c.Provide(name, name))
	}

	require.Equal(t,
		[]string{"auth.service", "pricing.service", "product.service"},
		c.Names())
	require.True(t, c.Has("auth.service"))
	require.False(t, c.Has("cart.service"))
}

// --- Shutdown ---------------------------------------------------------------

func TestShutdownClosesInReverseOrderAndJoinsErrors(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	boom := errors.New("the close blew up")

	c := container.New(nil)
	require.NoError(t, c.Provide("a", &closerSvc{name: "a", rec: rec, err: boom}))
	require.NoError(t, c.Provide("b", &shutdownSvc{name: "b", rec: rec}))
	require.NoError(t, c.Provide("c", &bothSvc{name: "c", rec: rec}))
	require.NoError(t, c.Provide("d", "a plain value that cannot be closed"))

	err := c.Shutdown(t.Context())
	require.Error(t, err)
	require.ErrorIs(t, err, boom)
	require.Contains(t, err.Error(), `the service "a" could not be closed`)

	require.Equal(t,
		[]string{"shutdown:c", "shutdown:b", "close:a"},
		rec.snapshot(),
		"closing must be the reverse of registration order; Shutdown must be preferred over Close")
}

func TestShutdownJoinsMultipleErrors(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	first := errors.New("first")
	second := errors.New("second")

	c := container.New(nil)
	require.NoError(t, c.Provide("a", &closerSvc{name: "a", rec: rec, err: first}))
	require.NoError(t, c.Provide("b", &shutdownSvc{name: "b", rec: rec, err: second}))

	err := c.Shutdown(t.Context())
	require.Error(t, err)
	require.ErrorIs(t, err, first)
	require.ErrorIs(t, err, second)
	require.Equal(t, []string{"shutdown:b", "close:a"}, rec.snapshot())
}

func TestShutdownDoesNotBuildLazyServices(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	rec := &recorder{}

	c := container.New(nil)
	require.NoError(t, c.Provide("ready", &closerSvc{name: "ready", rec: rec}))
	require.NoError(t, c.Provide("lazy", func(_ *container.Container) (any, error) {
		calls.Add(1)
		return &closerSvc{name: "lazy", rec: rec}, nil
	}))

	require.NoError(t, c.Shutdown(t.Context()))
	require.Equal(t, int64(0), calls.Load(), "a lazy registration never resolved is not built just to close it")
	require.Equal(t, []string{"close:ready"}, rec.snapshot())
}

func TestShutdownIsIdempotentAndSealsContainer(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	c := container.New(nil)
	require.NoError(t, c.Provide("a", &closerSvc{name: "a", rec: rec}))

	require.NoError(t, c.Shutdown(t.Context()))
	require.NoError(t, c.Shutdown(t.Context()), "the second Shutdown must be a no-op")
	require.Equal(t, []string{"close:a"}, rec.snapshot())

	provideErr := c.Provide("b", "x")
	require.True(t, errors.HasKind(provideErr, errors.KindUnavailable))

	_, resolveErr := container.Resolve[*closerSvc](c, "a")
	require.True(t, errors.HasKind(resolveErr, errors.KindUnavailable))
}

func TestShutdownClosesEvenWithCanceledContext(t *testing.T) {
	t.Parallel()

	// A canceled budget must not leak resources: because of idempotency there is
	// no second chance to try.
	rec := &recorder{}
	c := container.New(nil)
	require.NoError(t, c.Provide("a", &closerSvc{name: "a", rec: rec}))
	require.NoError(t, c.Provide("b", &shutdownSvc{name: "b", rec: rec}))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := c.Shutdown(ctx)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	require.Contains(t, err.Error(), "the shutdown context was canceled")
	require.Equal(t, []string{"shutdown:b", "close:a"}, rec.snapshot(),
		"every service must be closed with a canceled context too")
}

func TestShutdownClosesResolvedLazyService(t *testing.T) {
	t.Parallel()

	// Every service that has to be closed in production (the DB pool, Redis)
	// goes down this path: a lazy registration, a Resolve and a Shutdown.
	rec := &recorder{}
	c := container.New(nil)
	require.NoError(t, c.Provide("ready", &closerSvc{name: "ready", rec: rec}))
	require.NoError(t, c.Provide("lazy", func(_ *container.Container) (any, error) {
		return &closerSvc{name: "lazy", rec: rec}, nil
	}))

	_, err := container.Resolve[*closerSvc](c, "lazy")
	require.NoError(t, err)

	require.NoError(t, c.Shutdown(t.Context()))
	require.Equal(t, []string{"close:lazy", "close:ready"}, rec.snapshot(),
		"a resolved lazy service must be closed in reverse registration order too")
}

func TestShutdownSkipsFailedLazyService(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	c := container.New(nil)
	require.NoError(t, c.Provide("failing", func(_ *container.Container) (any, error) {
		return &closerSvc{name: "failing", rec: rec}, errors.New("could not be built")
	}))

	_, err := container.Resolve[*closerSvc](c, "failing")
	require.Error(t, err)

	require.NoError(t, c.Shutdown(t.Context()))
	require.Empty(t, rec.snapshot(), "the service of a registration whose constructor failed is not closed")
}

func TestShutdownWaitsForInFlightCtor(t *testing.T) {
	t.Parallel()

	// At the moment of SIGTERM a request may be resolving the 'db' service for
	// the first time. Because the constructor runs outside the lock, if the
	// shutdown does not see it the service is neither closed nor reachable
	// again; a permanent leak.
	rec := &recorder{}
	started := make(chan struct{})

	c := container.New(nil)
	require.NoError(t, c.Provide("db", func(_ *container.Container) (any, error) {
		close(started)
		// Wait until the shutdown STARTS: once the container is closed,
		// resolution attempts return Unavailable, while before that an
		// unregistered name would return NotFound.
		for {
			_, err := container.Resolve[string](c, "unregistered")
			if errors.HasKind(err, errors.KindUnavailable) {
				return &closerSvc{name: "db", rec: rec}, nil
			}
			time.Sleep(time.Millisecond)
		}
	}))

	resolveErr := make(chan error, 1)
	go func() {
		_, err := container.Resolve[*closerSvc](c, "db")
		resolveErr <- err
	}()
	<-started

	var shutErr error
	mustFinish(t, 10*time.Second, func() { shutErr = c.Shutdown(t.Context()) })

	require.NoError(t, shutErr)
	require.Equal(t, []string{"close:db"}, rec.snapshot(),
		"a service that finishes being built during the shutdown must be closed too")
	require.True(t, errors.HasKind(<-resolveErr, errors.KindUnavailable),
		"a closed container must not hand out a live service")
}

func TestShutdownRecoversPanickingClose(t *testing.T) {
	t.Parallel()

	// Closing walks the reverse of registration order: a panicking service must
	// not stop the services registered BEFORE it from being closed.
	rec := &recorder{}
	c := container.New(nil)
	require.NoError(t, c.Provide("sound", &closerSvc{name: "sound", rec: rec}))
	require.NoError(t, c.Provide("panicking", panicOnCloseSvc{}))

	var err error
	require.NotPanics(t, func() { err = c.Shutdown(t.Context()) })

	require.Error(t, err)
	require.Contains(t, err.Error(), "container_close_panic")
	require.Contains(t, err.Error(), "the shutdown blew up")
	require.Contains(t, err.Error(), `the service "panicking" could not be closed`)
	require.Equal(t, []string{"close:sound"}, rec.snapshot(),
		"the remaining services must keep being closed after a panic")
}
