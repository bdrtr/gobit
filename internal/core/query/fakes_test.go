package query_test

import (
	"context"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/link"
	"github.com/bdrtr/gobit/internal/core/query"
)

// --- the fake provider ------------------------------------------------------

// providerCalls is how many calls a provider received.
//
// It is the proof of the N+1 test: however many root records there are,
// FetchByIDs has to be called exactly once per expansion.
type providerCalls struct {
	list  int
	fetch int
}

// fakeProvider imitates a module's query.Provider surface inside the process.
//
// The provider behaves like a real module: it applies the field selection,
// returns no record for an unknown id and produces fresh records on every call
// (a module reading from a database behaves that way too). That a provider which
// does not copy also works safely is proven by sharingProvider.
type fakeProvider struct {
	entity  string
	order   []string
	records map[string]query.Record

	// When listErr or fetchErr is non-nil the matching call returns that error.
	listErr  error
	fetchErr error
	// When afterList is set it is called right after List returns; the tests use
	// it to build the "the root was read, then the context was canceled" case.
	afterList func()

	mu         sync.Mutex
	listCalls  int
	fetchCalls int
	lastOpts   query.ListOptions
	lastIDs    []string
	lastFields []string
}

var _ query.Provider = (*fakeProvider)(nil)

// newProvider produces a fake provider holding the given records. Their order is
// the order the List call will return them in.
func newProvider(entity string, records ...query.Record) *fakeProvider {
	p := &fakeProvider{
		entity:  entity,
		order:   make([]string, 0, len(records)),
		records: make(map[string]query.Record, len(records)),
	}
	for _, rec := range records {
		id, _ := rec[query.IDField].(string)
		p.order = append(p.order, id)
		p.records[id] = rec
	}
	return p
}

// Entity returns the entity name the provider serves.
func (p *fakeProvider) Entity() string { return p.entity }

// List returns the root records, applying Offset/Limit and the field selection.
func (p *fakeProvider) List(ctx context.Context, opts query.ListOptions) ([]query.Record, error) {
	p.mu.Lock()
	p.listCalls++
	p.lastOpts = opts
	p.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.listErr != nil {
		return nil, p.listErr
	}

	ids := p.order
	if opts.Offset >= len(ids) {
		ids = nil
	} else {
		ids = ids[opts.Offset:]
	}
	if opts.Limit > 0 && opts.Limit < len(ids) {
		ids = ids[:opts.Limit]
	}

	out := make([]query.Record, 0, len(ids))
	for _, id := range ids {
		out = append(out, project(p.records[id], opts.Fields))
	}
	if p.afterList != nil {
		p.afterList()
	}
	return out, nil
}

// FetchByIDs returns the records of the given ids; an unknown id is skipped.
func (p *fakeProvider) FetchByIDs(ctx context.Context, ids, fields []string) ([]query.Record, error) {
	p.mu.Lock()
	p.fetchCalls++
	p.lastIDs = slices.Clone(ids)
	p.lastFields = slices.Clone(fields)
	p.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.fetchErr != nil {
		return nil, p.fetchErr
	}

	out := make([]query.Record, 0, len(ids))
	for _, id := range ids {
		rec, ok := p.records[id]
		if !ok {
			continue
		}
		out = append(out, project(rec, fields))
	}
	return out, nil
}

// calls returns the provider's call counts.
func (p *fakeProvider) calls() providerCalls {
	p.mu.Lock()
	defer p.mu.Unlock()
	return providerCalls{list: p.listCalls, fetch: p.fetchCalls}
}

// opts returns the options of the last List call.
func (p *fakeProvider) opts() query.ListOptions {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastOpts
}

// fetchArgs returns the ids and fields of the last FetchByIDs call.
func (p *fakeProvider) fetchArgs() (ids, fields []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.lastIDs), slices.Clone(p.lastFields)
}

// project returns a copy of the record; with fields set it takes only those.
func project(rec query.Record, fields []string) query.Record {
	if rec == nil {
		return nil
	}
	if len(fields) == 0 {
		return maps.Clone(rec)
	}
	out := make(query.Record, len(fields))
	for _, f := range fields {
		if v, ok := rec[f]; ok {
			out[f] = v
		}
	}
	return out
}

// --- sahte link servisi -----------------------------------------------------

// fakeLinks imitates link.LinkService inside the process and counts the calls.
//
// It satisfies the whole contract; the forward and the reverse direction are
// derived from the same forward map, so the two stay consistent in the test too.
type fakeLinks struct {
	defs    map[string]link.LinkDefinition
	forward map[string]map[string][]string

	// When defErr or listErr is non-nil the matching call returns that error.
	defErr  error
	listErr error

	definitionCalls atomic.Int64
	listCalls       atomic.Int64
	listManyCalls   atomic.Int64
	// listManyByToCalls is there to measure that the reverse direction is called
	// IN BULK too (that there is no N+1).
	listManyByToCalls atomic.Int64
}

// ListManyByTo resolves the reverse direction in bulk, by inverting the forward map.
func (f *fakeLinks) ListManyByTo(_ context.Context, name string, toIDs []string) (map[string][]string, error) {
	f.listManyByToCalls.Add(1)
	if f.listErr != nil {
		return nil, f.listErr
	}
	byTo := make(map[string][]string, len(toIDs))
	want := make(map[string]struct{}, len(toIDs))
	for _, id := range toIDs {
		want[id] = struct{}{}
	}
	for fromID, tos := range f.forward[name] {
		for _, toID := range tos {
			if _, ok := want[toID]; ok {
				byTo[toID] = append(byTo[toID], fromID)
			}
		}
	}
	for _, froms := range byTo {
		slices.Sort(froms)
	}
	return byTo, nil
}

var _ link.LinkService = (*fakeLinks)(nil)

// newLinks produces a fake link service with the given definitions and no links.
func newLinks(defs ...link.LinkDefinition) *fakeLinks {
	f := &fakeLinks{
		defs:    make(map[string]link.LinkDefinition, len(defs)),
		forward: make(map[string]map[string][]string, len(defs)),
	}
	for _, def := range defs {
		f.defs[def.Name] = def
		f.forward[def.Name] = make(map[string][]string)
	}
	return f
}

// connect links fromID to the given toIDs (for the test setup).
func (f *fakeLinks) connect(name, fromID string, toIDs ...string) *fakeLinks {
	if f.forward[name] == nil {
		f.forward[name] = make(map[string][]string)
	}
	f.forward[name][fromID] = append(f.forward[name][fromID], toIDs...)
	return f
}

// Define writes the definition into the ledger.
func (f *fakeLinks) Define(_ context.Context, def link.LinkDefinition) error {
	f.defs[def.Name] = def
	if f.forward[def.Name] == nil {
		f.forward[def.Name] = make(map[string][]string)
	}
	return nil
}

// Create makes a link.
func (f *fakeLinks) Create(_ context.Context, name, fromID, toID string) error {
	if _, ok := f.defs[name]; !ok {
		return errors.NotFound("link_not_defined", "%q is not defined", name)
	}
	if slices.Contains(f.forward[name][fromID], toID) {
		return nil
	}
	f.forward[name][fromID] = append(f.forward[name][fromID], toID)
	return nil
}

// Delete removes a link.
func (f *fakeLinks) Delete(_ context.Context, name, fromID, toID string) error {
	f.forward[name][fromID] = slices.DeleteFunc(f.forward[name][fromID],
		func(id string) bool { return id == toID })
	return nil
}

// List returns the toIDs linked to fromID.
func (f *fakeLinks) List(ctx context.Context, name, fromID string) ([]string, error) {
	f.listCalls.Add(1)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.listErr != nil {
		return nil, f.listErr
	}
	if _, ok := f.defs[name]; !ok {
		return nil, errors.NotFound("link_not_defined", "%q is not defined", name)
	}
	return slices.Clone(f.forward[name][fromID]), nil
}

// ListMany returns the links of several fromIDs in one call.
func (f *fakeLinks) ListMany(ctx context.Context, name string, fromIDs []string) (map[string][]string, error) {
	f.listManyCalls.Add(1)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.listErr != nil {
		return nil, f.listErr
	}
	if _, ok := f.defs[name]; !ok {
		return nil, errors.NotFound("link_not_defined", "%q is not defined", name)
	}

	out := make(map[string][]string, len(fromIDs))
	for _, id := range fromIDs {
		if targets := f.forward[name][id]; len(targets) > 0 {
			out[id] = slices.Clone(targets)
		}
	}
	return out, nil
}

// Definition returns the definition of the link with the given name.
func (f *fakeLinks) Definition(ctx context.Context, name string) (link.LinkDefinition, error) {
	f.definitionCalls.Add(1)
	if err := ctx.Err(); err != nil {
		return link.LinkDefinition{}, err
	}
	if f.defErr != nil {
		return link.LinkDefinition{}, f.defErr
	}
	def, ok := f.defs[name]
	if !ok {
		return link.LinkDefinition{}, errors.NotFound("link_not_defined",
			"there is no link defined under the name %q", name)
	}
	return def, nil
}

// reverseByTo inverts the forward table and returns the fromIDs of the wanted
// toIDs; it is the shared body of the reverse-direction fakes.
func (f *fakeLinks) reverseByTo(name string, toIDs []string) map[string][]string {
	want := make(map[string]struct{}, len(toIDs))
	for _, id := range toIDs {
		want[id] = struct{}{}
	}

	out := make(map[string][]string, len(toIDs))
	for _, from := range slices.Sorted(maps.Keys(f.forward[name])) {
		for _, to := range f.forward[name][from] {
			if _, ok := want[to]; ok {
				out[to] = append(out[to], from)
			}
		}
	}
	return out
}

// reverseLinks is the fake link service that also resolves the reverse in bulk.
type reverseLinks struct {
	*fakeLinks
	listManyByToCalls atomic.Int64
}

// newReverseLinks produces a fake link service supporting the reverse in bulk.
func newReverseLinks(defs ...link.LinkDefinition) *reverseLinks {
	return &reverseLinks{fakeLinks: newLinks(defs...)}
}

// ListManyByTo returns the fromIDs several toIDs are linked to, in one call.
func (f *reverseLinks) ListManyByTo(ctx context.Context, name string, toIDs []string) (map[string][]string, error) {
	f.listManyByToCalls.Add(1)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.reverseByTo(name, toIDs), nil
}

// --- shared setup ----------------------------------------------------------

// newContainer registers the given providers under "<entity>.query" (ADR 0004).
func newContainer(t *testing.T, providers ...*fakeProvider) *container.Container {
	t.Helper()

	c := container.New(nil)
	for _, p := range providers {
		require.NoError(t, c.Provide(p.Entity()+query.ProviderSuffix, p))
	}
	return c
}

// --- the fake provider that does not copy -----------------------------------

// sharingProvider imitates a module that does NOT COPY the records it returns:
// it hands out the maps it keeps in the process directly.
//
// The contract does not require copying (a module keeping a small reference
// table in memory may reasonably behave this way), so this fake is what proves
// that Query writing into the returned maps does not corrupt the provider's OWN
// state.
type sharingProvider struct {
	entity  string
	records []query.Record
}

var _ query.Provider = (*sharingProvider)(nil)

// newSharingProvider produces a provider serving the given records without copying.
func newSharingProvider(entity string, records ...query.Record) *sharingProvider {
	return &sharingProvider{entity: entity, records: records}
}

// Entity returns the entity name the provider serves.
func (p *sharingProvider) Entity() string { return p.entity }

// List returns the records; only the slice is copied, the maps are shared.
func (p *sharingProvider) List(_ context.Context, _ query.ListOptions) ([]query.Record, error) {
	return slices.Clone(p.records), nil
}

// FetchByIDs returns the records of the given ids without copying the maps.
func (p *sharingProvider) FetchByIDs(_ context.Context, ids, _ []string) ([]query.Record, error) {
	out := make([]query.Record, 0, len(ids))
	for _, id := range ids {
		for _, rec := range p.records {
			if rec[query.IDField] == id {
				out = append(out, rec)
			}
		}
	}
	return out, nil
}
