// Package container provides gobit's dependency injection container.
//
// The container holds services BY NAME and hands them back through [Resolve]
// with a type parameter. Because modules do not import each other (see ADR
// 0001), a module declares the narrow interface it needs in its own package and
// resolves the provider's concrete type from here by name.
//
// # Why not samber/do
//
// Plan Section 3 suggests samber/do v2 for DI, but the contract in Section 5.1
// binds: Provide(name string, ctor any) error and Resolve[T](c, name). do v2's
// registration surface is type-parameterized (ProvideNamed[T]); building a
// Provide that takes "any" on top of it would mean handing every service to do
// as any. At that moment all three things do brings are lost:
//
//  1. With the type information flattened into any, do's errors cannot give the
//     "registered concrete type vs expected type" diagnosis ADR 0001 wants.
//  2. do panics on a duplicate registration; the contract wants
//     errors.Conflict.
//  3. do closes according to its own dependency graph and recognizes only its
//     own Shutdowner interfaces; the contract requires the reverse of
//     REGISTRATION order and io.Closer support.
//
// What was left of do was a mutex-guarded map; this package writes that
// directly, with the behavior the contract asks for. Because only the surface
// here is visible from outside, the body can later move to a library.
//
// # Concurrency
//
// Every method is goroutine-safe. A name's lazy constructor runs exactly once
// even when 100 Resolve calls arrive at the same time; the other callers wait
// for the result.
//
// [Container.Shutdown] TRIES to cover resolutions racing with the shutdown: a
// service that finishes being built during shutdown is closed if the build
// finishes WITHIN the ctx budget given to Shutdown. If the budget runs out that
// service is left unclosed (Shutdown adds it to the error); in both cases it is
// not handed to its caller — a closed container hands out no LIVE service, it
// returns errors.Unavailable.
//
// In practice this means the ctx given to Shutdown has to outlast the slowest
// constructor.
package container

import (
	"context"
	"io"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bdrtr/gobit/core/errors"
)

// The error codes; a caller can branch on them through errors.CodeOf.
const (
	codeInvalidName  = "container_invalid_name"
	codeInvalidCtor  = "container_invalid_ctor"
	codeDuplicate    = "container_duplicate_service"
	codeNotFound     = "container_service_not_found"
	codeTypeMismatch = "container_type_mismatch"
	codeCycle        = "container_dependency_cycle"
	codeCtorFailed   = "container_ctor_failed"
	codeCtorPanic    = "container_ctor_panic"
	codeCtorNil      = "container_ctor_nil"
	codeShutdown     = "container_shutdown_failed"
	codeClosePanic   = "container_close_panic"
	codeCanceled     = "container_shutdown_canceled"
	codeClosed       = "container_closed"
)

// defaultWaitWarn is how long a caller waiting for a service that is being
// built waits in silence before logging a warning. See registry.waitReady.
const defaultWaitWarn = 5 * time.Second

// Ctor is the signature of a lazy constructor. It runs once, on the first
// [Resolve] call.
//
// A constructor must resolve its own dependencies through the *Container IT IS
// GIVEN; using the outer container through a closure disables dependency-cycle
// detection. Two mutually dependent constructors then wait for each other
// indefinitely; when the wait grows long a warning is logged together with the
// wait graph (see [New]'s log parameter), but no error is returned.
//
// A function that does not match this signature (e.g. a constructor returning a
// concrete type) is rejected by [Provide]; it is not silently registered as a
// ready value.
type Ctor = func(*Container) (any, error)

// Shutdowner is the interface of services with a context-aware shutdown.
// [Container.Shutdown] prefers it when a service satisfies both it and
// io.Closer (so the context can be passed on).
type Shutdowner interface {
	Shutdown(ctx context.Context) error
}

// entry is a single registration. Its fields are read and written only under
// registry.mu; the exception is running the constructor OUTSIDE the lock (see
// resolve).
type entry struct {
	name string
	// ctor is the lazy constructor; it is nil for a ready-value registration.
	ctor Ctor
	// value and err are filled in once the constructor has finished.
	value any
	err   error
	// built true means value/err are final (for a ready value it is true at
	// registration).
	built bool
	// building true means a goroutine is running the constructor; the waiters
	// wait for the ready channel to close.
	building bool
	ready    chan struct{}
}

// registry is the state shared by every container (the root and the derived
// ones handed to constructors). A Container value is light; the real state
// lives here.
type registry struct {
	mu      sync.Mutex
	log     *slog.Logger
	entries map[string]*entry
	// order is the registration order; Shutdown uses its reverse.
	order []string
	// blocked is the wait graph: b in the set blocked[a] means "a's
	// constructor is waiting for b". Because one constructor can call several
	// Resolves at once from goroutines, several edges are kept per node. Cycle
	// detection runs on this graph.
	blocked map[string]map[string]struct{}
	closed  bool
	// building is the number of constructors running OUTSIDE THE LOCK right
	// now; Shutdown waits for them to finish so no service stays out of the
	// snapshot.
	building int
	// drained is created while Shutdown waits for the in-flight constructors;
	// it is closed once the counter reaches zero.
	drained chan struct{}
	// waitWarn is how long a caller waiting for a build waits before logging a
	// warning; it is a field so tests can shorten it.
	waitWarn time.Duration
}

// Container is the DI container holding services registered by name.
//
// Its zero value is unusable; build one with [New].
type Container struct {
	reg *registry
	// current holds which service's constructor this container was handed to.
	// It is empty on the root container; cycle detection works from this field.
	current string
}

// New produces an empty container. With log nil the logs are discarded.
func New(log *slog.Logger) *Container {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Container{reg: &registry{
		log:      log,
		entries:  make(map[string]*entry),
		blocked:  make(map[string]map[string]struct{}),
		waitWarn: defaultWaitWarn,
	}}
}

// Provide registers a service by name.
//
// ctor may take one of two shapes:
//
//  1. A value directly (an already-built service).
//  2. A lazy constructor with the [Ctor] signature; it runs once, on the first
//     Resolve.
//
// The lazy shape gives independence from module order: module A can register a
// constructor depending on module B's service before B has registered.
//
// Registering the same name a second time returns errors.Conflict. These return
// errors.Invalid: an empty name; a nil registration (including an
// interface-nil and a typed nil such as (*T)(nil)); a function whose first
// parameter is *Container but which does not match the [Ctor] signature — such
// a function does not count as a ready value and, being the most common typo,
// is rejected at registration.
func (c *Container) Provide(name string, ctor any) error {
	if name == "" {
		return errors.Invalid(codeInvalidName, "a service name cannot be empty")
	}
	// A typed nil is filtered out too: a value like (*Pool)(nil) is NOT
	// interface-nil, and once inside the container it would panic on first use
	// or at shutdown.
	if isNil(ctor) {
		return errors.Invalid(codeInvalidCtor,
			"a nil registration cannot be made for %q (given type: %s)", name, typeName(reflect.TypeOf(ctor)))
	}

	r := c.reg
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return errors.Unavailable(codeClosed, "the container is closed; %q cannot be registered", name)
	}
	if _, dup := r.entries[name]; dup {
		return errors.Conflict(codeDuplicate, "a service is already registered under the name %q", name)
	}

	e := &entry{name: name}
	switch fn := ctor.(type) {
	case Ctor:
		e.ctor = fn
	default:
		if misusedCtor(ctor) {
			return errors.Invalid(codeInvalidCtor,
				"the %s given for %q looks like a constructor but its signature does not match; expected signature: func(*container.Container) (any, error)",
				typeName(reflect.TypeOf(ctor)), name)
		}
		e.value, e.built = ctor, true
	}

	r.entries[name] = e
	r.order = append(r.order, name)
	r.log.Debug("service registered", "service", name, "lazy", e.ctor != nil)
	return nil
}

// Has reports whether the given name is registered. It does not build it.
func (c *Container) Has(name string) bool {
	c.reg.mu.Lock()
	defer c.reg.mu.Unlock()
	_, ok := c.reg.entries[name]
	return ok
}

// Names returns every registered service name, sorted.
func (c *Container) Names() []string {
	c.reg.mu.Lock()
	defer c.reg.mu.Unlock()
	return slices.Sorted(maps.Keys(c.reg.entries))
}

// resolve resolves the name untyped, running the lazy constructor when needed.
//
// The constructor runs WITHOUT THE LOCK HELD: the Resolve calls made from
// inside the constructor need the lock too. The registration is protected by
// the building flag while it runs; the other callers arriving at the same time
// wait on the ready channel.
//
// Every time the lock is released and taken again, r.closed is re-read:
// Shutdown may have been called in the meantime, and a closed container must
// not hand out a LIVE service.
func (c *Container) resolve(name string) (any, error) {
	r := c.reg
	r.mu.Lock()

	if r.closed {
		r.mu.Unlock()
		return nil, errClosed(name)
	}

	e, ok := r.entries[name]
	if !ok {
		known := slices.Sorted(maps.Keys(r.entries))
		r.mu.Unlock()
		return nil, errors.NotFound(codeNotFound,
			"no service is registered under the name %q; registered names: %s", name, joinNames(known)).
			WithDetails(map[string]any{"service": name})
	}

	// The fast path: a built (or ready-registered) service.
	if e.built {
		value, err := e.value, e.err
		r.mu.Unlock()
		return value, err
	}

	// When this resolution comes from inside a constructor, add an edge to the
	// wait graph. If the edge closes a cycle, return an error instead of
	// deadlocking.
	owner := c.current
	if owner != "" {
		if path := r.addEdge(owner, name); path != nil {
			r.mu.Unlock()
			return nil, errors.Conflict(codeCycle, "dependency cycle: %s", strings.Join(path, " -> ")).
				WithDetails(map[string]any{"cycle": path})
		}
	}

	// Either we build the registration or we wait for the goroutine that is.
	for !e.built && e.building {
		ready, warnAfter := e.ready, r.waitWarn
		r.mu.Unlock()
		r.waitReady(ready, name, warnAfter)
		r.mu.Lock()
	}
	if e.built {
		value, err, closed := e.value, e.err, r.closed
		r.removeEdge(owner, name)
		r.mu.Unlock()
		if closed {
			return nil, errClosed(name)
		}
		return value, err
	}
	e.building = true
	e.ready = make(chan struct{})
	ctor := e.ctor
	r.building++
	r.mu.Unlock()

	value, err := runCtor(ctor, &Container{reg: r, current: name}, name)

	r.mu.Lock()
	e.value, e.err, e.built, e.building = value, err, true, false
	close(e.ready)
	r.removeEdge(owner, name)
	closed := r.closed
	r.buildFinished()
	r.mu.Unlock()

	if err != nil {
		r.log.Error("the service could not be built", "service", name, "error", err)
		return nil, err
	}
	r.log.Debug("service built", "service", name)
	if closed {
		// The container was closed while the constructor ran outside the lock.
		// The registration is left built: because Shutdown waits until the
		// building counter drops to zero, this service enters its snapshot and
		// is closed. The caller, however, gets the closed-container error and
		// not a live service.
		return nil, errClosed(name)
	}
	return value, nil
}

// runCtor runs the constructor, turning panics and a nil result into typed
// errors.
//
// The constructor's error is CACHED (by the calling resolve): a registration is
// built once and never retried. The reasons: (a) the contract says "the
// constructor runs exactly once", and a retry could trigger its side effects
// (opening a connection, registering a handler) a second time; (b) DI errors (a
// missing dependency, a type mismatch) are deterministic, and retrying produces
// the same error while hiding the real point of failure; (c) transient resource
// errors (the DB or Redis being unreachable) must be retried inside the service
// itself, not in the constructor — the constructor only establishes the
// connection, it does not use it.
func runCtor(ctor Ctor, c *Container, name string) (value any, err error) {
	defer func() {
		if p := recover(); p != nil {
			value = nil
			err = errors.Internal(codeCtorPanic, "the constructor of %q panicked: %v", name, p)
		}
	}()

	value, err = ctor(c)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), codeCtorFailed, "the service %q could not be built", name)
	}
	// A typed nil is filtered out too; (*Pool)(nil) is not interface-nil but
	// cannot be used as a service.
	if isNil(value) {
		return nil, errors.Internal(codeCtorNil,
			"the constructor of %q returned a nil service (type: %s)", name, typeName(reflect.TypeOf(value)))
	}
	return value, nil
}

// isNil reports whether the value is nil, counting typed nils too.
// The comparison value == nil catches only an INTERFACE-nil: a value like
// (*Pool)(nil) passes that comparison but panics on the first method call.
func isNil(value any) bool {
	if value == nil {
		return true
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Pointer, reflect.UnsafePointer, reflect.Interface,
		reflect.Map, reflect.Slice, reflect.Chan, reflect.Func:
		return rv.IsNil()
	default:
		return false
	}
}

// misusedCtor reports whether the value is a constructor that failed to match
// the [Ctor] signature: a function whose first parameter is *Container cannot
// have been meant as a ready value.
func misusedCtor(value any) bool {
	t := reflect.TypeOf(value)
	if t.Kind() != reflect.Func {
		return false
	}
	// Functions with no parameters are DELIBERATELY treated as values. func()
	// *Svc MAY be a mistyped constructor, but it cannot be told apart from the
	// legitimate pattern where the function itself is the service (func()
	// time.Time as a clock service, an id generator and so on). A false
	// positive would reject a valid registration; only signatures whose first
	// parameter is *Container — that is, the ones clearly TRYING to be a
	// constructor — are rejected.
	if t.NumIn() == 0 {
		return false
	}
	return t.In(0) == reflect.TypeFor[*Container]()
}

// errClosed produces the error reporting a resolution attempted on a closed
// container.
func errClosed(name string) error {
	return errors.Unavailable(codeClosed, "the container is closed; %q cannot be resolved", name)
}

// waitReady waits for the constructor to finish; if the wait exceeds warnAfter
// it logs a warning together with the wait graph and keeps waiting.
//
// The warning is for cycles that cannot be detected: a constructor capturing
// the root container in a closure adds no edge to the wait graph, so the cycle
// it closes is invisible and this wait lasts forever. The warning at least
// carries that silent deadlock into the logs.
func (r *registry) waitReady(ready <-chan struct{}, name string, warnAfter time.Duration) {
	timer := time.NewTimer(warnAfter)
	defer timer.Stop()

	select {
	case <-ready:
		return
	case <-timer.C:
	}

	r.mu.Lock()
	graph := r.waitEdges()
	r.mu.Unlock()

	r.log.Warn("the service is taking a long time to build; there may be a dependency cycle",
		"service", name, "waited", warnAfter, "wait_graph", graph)
	<-ready
}

// buildFinished decrements the in-flight constructor counter; when the counter
// reaches zero it closes the channel Shutdown is waiting on. The caller must
// hold r.mu.
func (r *registry) buildFinished() {
	r.building--
	if r.building == 0 && r.drained != nil {
		close(r.drained)
		r.drained = nil
	}
}

// drainSignal returns a channel that closes once every in-flight constructor
// has finished, or nil when there are none. The caller must hold r.mu.
func (r *registry) drainSignal() <-chan struct{} {
	if r.building == 0 {
		return nil
	}
	if r.drained == nil {
		r.drained = make(chan struct{})
	}
	return r.drained
}

// addEdge records that from's constructor is waiting for to. When the edge
// closes a cycle, the edge is not added and the cycle path (with the first node
// repeated at the end) is returned. The caller must hold r.mu.
//
// A constructor can call several Resolves AT THE SAME TIME from goroutines of
// its own; several edges are therefore kept per node and none overwrites
// another.
func (r *registry) addEdge(from, to string) []string {
	edges, ok := r.blocked[from]
	if !ok {
		edges = make(map[string]struct{}, 1)
		r.blocked[from] = edges
	}
	edges[to] = struct{}{}

	if path := r.cyclePath(to); path != nil {
		r.removeEdge(from, to)
		return path
	}
	return nil
}

// removeEdge deletes the edge where from waits for to, leaving from's other
// waits alone. The caller must hold r.mu.
func (r *registry) removeEdge(from, to string) {
	if from == "" {
		return
	}
	edges := r.blocked[from]
	delete(edges, to)
	if len(edges) == 0 {
		delete(r.blocked, from)
	}
}

// cyclePath follows the wait edges depth-first from start; when a node on the
// path is reached again it returns the cycle (with the first node repeated at
// the end), otherwise nil. The caller must hold r.mu.
func (r *registry) cyclePath(start string) []string {
	var (
		path    []string
		onPath  = make(map[string]bool, len(r.blocked)+1)
		visited = make(map[string]bool, len(r.blocked)+1)
		walk    func(node string) []string
	)

	walk = func(node string) []string {
		if onPath[node] {
			return append(slices.Clone(path[slices.Index(path, node):]), node)
		}
		if visited[node] {
			return nil
		}
		visited[node] = true
		onPath[node] = true
		path = append(path, node)

		// A sorted walk produces the same path for the same graph every time.
		for _, next := range slices.Sorted(maps.Keys(r.blocked[node])) {
			if found := walk(next); found != nil {
				return found
			}
		}

		path = path[:len(path)-1]
		onPath[node] = false
		return nil
	}

	return walk(start)
}

// waitEdges writes the wait graph in "a -> b" form, sorted.
// The caller must hold r.mu.
func (r *registry) waitEdges() []string {
	edges := make([]string, 0, len(r.blocked))
	for _, from := range slices.Sorted(maps.Keys(r.blocked)) {
		for _, to := range slices.Sorted(maps.Keys(r.blocked[from])) {
			edges = append(edges, from+" -> "+to)
		}
	}
	return edges
}

// Resolve resolves the service registered under name as type T. A lazy service
// is built on the first call.
//
// When the name is not registered it returns errors.NotFound. When the
// registered value does not satisfy T it returns errors.Invalid; the error
// message names both the registered concrete type and the expected T, and when
// T is an interface also the missing or mismatched methods (ADR 0001).
func Resolve[T any](c *Container, name string) (T, error) {
	var zero T

	value, err := c.resolve(name)
	if err != nil {
		return zero, err
	}

	typed, ok := value.(T)
	if !ok {
		return zero, typeMismatch(name, value, reflect.TypeFor[T]())
	}
	return typed, nil
}

// MustResolve resolves the service and panics on error. It is used only on the
// bootstrap path, for services whose absence counts as a programming error.
func MustResolve[T any](c *Container, name string) T {
	value, err := Resolve[T](c, name)
	if err != nil {
		panic(err)
	}
	return value
}

// Shutdown closes the built services in the REVERSE of REGISTRATION ORDER and
// joins the errors with errors.Join.
//
// Only successfully built services satisfying [Shutdowner] or io.Closer are
// closed; lazy registrations never resolved are NOT BUILT just to close them.
// The call is idempotent: a second call returns nil. After the shutdown,
// Provide and Resolve return errors.Unavailable.
//
// The shutdown tries EVERY SERVICE; individual failures do not cut it short:
//
//   - In-flight constructors (running outside the lock) are waited for, so a
//     service that finishes being built during the shutdown is not left
//     unclosed. The wait is bounded by the ctx budget; given an UNBOUNDED ctx,
//     a hung constructor keeps Shutdown waiting too.
//   - When a service's Close/Shutdown call panics, the panic is turned into an
//     error and the remaining services keep being closed.
//   - When ctx is already canceled the shutdown still runs (io.Closers need no
//     budget, and context-aware services see the cancellation themselves); the
//     cancellation is only added to the joined error as an extra record.
func (c *Container) Shutdown(ctx context.Context) error {
	r := c.reg

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	drained := r.drainSignal()
	r.mu.Unlock()

	// After closed=true no new constructor starts; the counter only goes down.
	if drained != nil {
		select {
		case <-drained:
		case <-ctx.Done():
		}
	}

	r.mu.Lock()
	targets := make([]*entry, 0, len(r.order))
	for i := len(r.order) - 1; i >= 0; i-- {
		// For registrations where e.err != nil, runCtor has already dropped
		// the value, so !isNil(e.value) alone is enough; an additional e.err
		// check was dead code.
		if e := r.entries[r.order[i]]; e != nil && e.built && !isNil(e.value) {
			targets = append(targets, e)
		}
	}
	r.mu.Unlock()

	var errs []error
	for _, e := range targets {
		if err := closeService(ctx, e.value); err != nil {
			r.log.Error("the service could not be closed", "service", e.name, "error", err)
			errs = append(errs, errors.Wrap(err, errors.KindOf(err), codeShutdown,
				"the service %q could not be closed", e.name))
			continue
		}
		r.log.Debug("service closed", "service", e.name)
	}

	if err := ctx.Err(); err != nil {
		errs = append(errs, errors.Wrap(err, errors.KindUnavailable, codeCanceled,
			"the shutdown context was canceled; %d service(s) were closed anyway", len(targets)))
	}
	return errors.Join(errs...)
}

// closeService closes the service through whichever interface fits; when it
// satisfies neither, nil.
//
// A service whose shutdown panics has that panic turned into an error: because
// closing walks the reverse of registration order, one panicking service in the
// middle would stop every service registered before it from being closed.
func closeService(ctx context.Context, value any) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = errors.Internal(codeClosePanic, "the shutdown panicked: %v", p)
		}
	}()

	switch s := value.(type) {
	case Shutdowner:
		return s.Shutdown(ctx)
	case io.Closer:
		return s.Close()
	default:
		return nil
	}
}

// joinNames writes the name list readably for a message.
func joinNames(names []string) string {
	if len(names) == 0 {
		return "(no registrations)"
	}
	return strings.Join(names, ", ")
}
