package service_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/link"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// deletionTime is the soft delete stamp of the fake repository; being fixed
// keeps the tests independent of time.
var deletionTime = time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

// creationTime is the record creation stamp of the fake repository.
//
// In the real schema the created_at/updated_at columns have a DEFAULT now()
// value and the row comes back with RETURNING: the stamp is produced by THE
// DATABASE, not by the model the caller sent. Had the fake repository not
// imitated this, the "return the stored row" rule could not be tested here at
// all and an endpoint returning zero stamps would pass the tests.
var creationTime = time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)

// fakeLinker is the in-memory implementation of [service.Linker].
//
// The real link service lives in the core and wants a database; what is
// verified here is HOW THE SERVICE USES the links: that it removes the old link
// and creates the new one, that it verifies the existence of the variant first
// and that it cleans up on a delete.
//
// The fake reads the cardinality from the DECLARATION in [service.Definitions]
// and enforces it (see [cardinalities]): on the price/stock links both the FROM
// and the TO end are singular and a violation returns errors.Conflict, while on
// the sales channel link there is no constraint. Had the cardinality not been
// enforced, the fake would silently accept a flow the real link service would
// reject and the tests would "prove" a behavior that does not exist; had it not
// been read from the declaration, the opposite would happen — a many-to-many
// link would produce a conflict on the second channel because of the fake.
type fakeLinker struct {
	mu sync.Mutex
	// links is link name -> fromID -> list of toIDs.
	links map[string]map[string][]string
	// cardinality is link name -> the declared cardinality.
	cardinality map[string]link.Cardinality
	// createErr, when set, is the error Create returns.
	createErr error
	// listErr, when set, is the error List returns.
	listErr error
	// deletes is the record of the removed links ("name|from|to").
	deletes []string
}

// cardinalities returns the declared cardinalities by link name.
//
// The values are read from the DECLARATION in [service.Definitions], they are
// not repeated by hand: when the cardinality of a link changes the fake changes
// with it and the tests do not go on enforcing a constraint that does not
// really exist.
func cardinalities() map[string]link.Cardinality {
	out := map[string]link.Cardinality{}
	for _, def := range service.Definitions() {
		out[def.Name] = def.Cardinality
	}
	return out
}

// newFakeLinker builds an empty fake link service.
func newFakeLinker() *fakeLinker {
	return &fakeLinker{links: map[string]map[string][]string{}, cardinality: cardinalities()}
}

// Create records the link; a no-op for the same pair, errors.Conflict on a
// cardinality violation (the contract of the real service).
func (f *fakeLinker) Create(_ context.Context, name, fromID, toID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	if f.links[name] == nil {
		f.links[name] = map[string][]string{}
	}
	for _, existing := range f.links[name][fromID] {
		if existing == toID {
			return nil
		}
	}

	cardinality := f.cardinality[name]
	// The FROM end is unique only under OneToOne: the record is already linked
	// to another target.
	if cardinality == link.OneToOne && len(f.links[name][fromID]) > 0 {
		return errors.Conflict("link_cardinality_violation",
			"on the %q link %s is already linked", name, fromID)
	}
	// The TO end is unique under OneToOne and OneToMany: the target is already
	// linked to another record. Under ManyToMany there is no constraint.
	if cardinality != link.ManyToMany {
		for otherFrom, targets := range f.links[name] {
			if otherFrom == fromID {
				continue
			}
			for _, existing := range targets {
				if existing == toID {
					return errors.Conflict("link_cardinality_violation",
						"on the %q link %s is already linked to %s", name, toID, otherFrom)
				}
			}
		}
	}
	f.links[name][fromID] = append(f.links[name][fromID], toID)
	return nil
}

// Delete removes the link and keeps a record of it.
func (f *fakeLinker) Delete(_ context.Context, name, fromID, toID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes = append(f.deletes, name+"|"+fromID+"|"+toID)

	kept := make([]string, 0, len(f.links[name][fromID]))
	for _, existing := range f.links[name][fromID] {
		if existing != toID {
			kept = append(kept, existing)
		}
	}
	if f.links[name] != nil {
		f.links[name][fromID] = kept
	}
	return nil
}

// List returns the linked ids.
func (f *fakeLinker) List(_ context.Context, name, fromID string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]string(nil), f.links[name][fromID]...), nil
}

// linked returns the targets of the given link (for the test assertions).
func (f *fakeLinker) linked(name, fromID string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.links[name][fromID]...)
}

// fakeGraph is the fake implementation of [service.Grapher].
//
// The specs it records prove that the store listing CALLS the Query layer
// CORRECTLY (one call, two expansions, the right link names).
type fakeGraph struct {
	mu      sync.Mutex
	specs   []query.GraphSpec
	records []query.Record
	err     error
}

// Graph returns the recorded records and records the call.
func (f *fakeGraph) Graph(_ context.Context, spec query.GraphSpec) ([]query.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.specs = append(f.specs, spec)
	if f.err != nil {
		return nil, f.err
	}
	return f.records, nil
}

// callCount returns how many times Graph was called.
func (f *fakeGraph) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.specs)
}

// lastSpec returns the spec of the last call.
func (f *fakeGraph) lastSpec(t *testing.T) query.GraphSpec {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.NotEmpty(t, f.specs, "Graph was never called")
	return f.specs[len(f.specs)-1]
}

// fakeBus is the in-memory counterpart of [service.EventPublisher].
//
// It keeps the published events IN ORDER: that the catalog events are really
// published can only be proven by observing the publish — there is no trace of
// the event in the service's return value (Publish does not wait for the
// handlers).
type fakeBus struct {
	mu sync.Mutex
	// published holds the published events.
	published []eventbus.Event
	// failErr, when set, is the error Publish returns.
	failErr error
}

// That the fake bus satisfies the surface the service expects is verified at
// compile time.
var _ service.EventPublisher = (*fakeBus)(nil)

// newFakeBus builds an empty fake bus.
func newFakeBus() *fakeBus { return &fakeBus{} }

// Publish records the event.
func (b *fakeBus) Publish(_ context.Context, e eventbus.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.failErr != nil {
		return b.failErr
	}
	b.published = append(b.published, e)
	return nil
}

// events returns a snapshot copy of the published events.
func (b *fakeBus) events() []eventbus.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]eventbus.Event(nil), b.published...)
}

// byName returns the events published under the given name.
func (b *fakeBus) byName(name string) []eventbus.Event {
	out := make([]eventbus.Event, 0, 1)
	for _, e := range b.events() {
		if e.Name == name {
			out = append(out, e)
		}
	}
	return out
}

// newService builds a service for the tests.
//
// The fake repository and the fake link service are wired to each other HERE:
// in reality the sales channel link lives in a single table and the service
// WRITES it through link while reading it back with a repository query. Had they
// not been wired, the write would go to one place and the read to another and
// the filtering tests would prove nothing (see memStore.links).
//
// No bus is GIVEN: the tests that observe events use [newServiceWithBus]. The
// distinction is itself an assertion — a service without a bus has to keep
// working on every write path (see service.Service.publishProductEvent).
func newService(t *testing.T, store *memStore, links service.Linker, graph service.Grapher) *service.Service {
	t.Helper()
	return newServiceWithBus(t, store, links, graph, nil)
}

// newServiceWithBus builds a service with an event bus wired in.
func newServiceWithBus(
	t *testing.T,
	store *memStore,
	links service.Linker,
	graph service.Grapher,
	bus service.EventPublisher,
) *service.Service {
	t.Helper()
	if fake, ok := links.(*fakeLinker); ok && store != nil {
		store.links = fake
	}
	svc, err := service.New(service.Options{Repo: store, Links: links, Query: graph, Events: bus})
	require.NoError(t, err)
	return svc
}

// seedProduct is the shared setup of the tests: one published product with one
// variant.
func seedProduct(t *testing.T, svc *service.Service, handle, title string) models.Product {
	t.Helper()
	product := seedProductInput(t, svc, service.CreateProductInput{
		Handle: handle,
		Title:  title,
		Status: models.StatusPublished,
	})
	require.Len(t, product.Variants, 1)
	return product
}

// seedProductInput creates the product the input describes and stops the test
// if it cannot be written.
//
// [seedProduct] fixes the status and gives the product no category and no tag,
// which is right for most of the tests here and wrong for exactly one family:
// the taxonomy filters of the Query provider. Those are questions about
// MEMBERSHIP, and a fixture whose products all belong to nothing cannot tell a
// filter that matches everything from a filter that matches nothing — both
// return the same empty page. The status has to vary in the same fixture too,
// because "category_id together with status" is one of the combinations that
// has to keep working.
//
// The variant is filled in when the input leaves it out: it is not what these
// tests are about, and every product in this package has one.
func seedProductInput(t *testing.T, svc *service.Service, in service.CreateProductInput) models.Product {
	t.Helper()
	if len(in.Variants) == 0 {
		in.Variants = []service.CreateVariantInput{{Title: "One size"}}
	}
	product, err := svc.CreateProduct(context.Background(), in)
	require.NoError(t, err)
	return product
}

// ptr returns the address of the given value.
func ptr[T any](v T) *T { return &v }

// requireCount returns the total count of the result and asserts that it WAS
// COUNTED.
//
// The count is a pointer and nil means "not counted" (see
// [service.ListResult]). Tests writing a raw "*res.Count" would conflate two
// assertions: that the number is right and that the count was COMPUTED. Were
// the second one to disappear silently one day — someone flipping the default —
// the raw dereference would fall over with a panic instead of a readable
// failure.
func requireCount[T any](t *testing.T, res service.ListResult[T]) int {
	t.Helper()
	require.NotNil(t, res.Count, "the count should have been computed")

	return *res.Count
}
