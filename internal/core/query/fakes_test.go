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

// --- sahte sağlayıcı --------------------------------------------------------

// providerCalls bir sağlayıcının aldığı çağrıların sayısıdır.
//
// N+1 testinin kanıtı budur: kaç kök kayıt olursa olsun, genişletme başına
// FetchByIDs tam olarak bir kez çağrılmalıdır.
type providerCalls struct {
	list  int
	fetch int
}

// fakeProvider bir modülün query.Provider yüzeyini süreç içinde taklit eder.
//
// Sağlayıcı gerçek modüller gibi davranır: alan seçimini uygular, bulunamayan
// kimlik için kayıt döndürmez ve her çağrıda taze kayıt üretir (veritabanından
// okuyan bir modül de böyle davranır). Kopyalamayan bir sağlayıcının da güvenle
// çalıştığını sharingProvider kanıtlar.
type fakeProvider struct {
	entity  string
	order   []string
	records map[string]query.Record

	// listErr ve fetchErr sıfırdan farklıysa ilgili çağrı bu hatayı döner.
	listErr  error
	fetchErr error
	// afterList doluysa List döndükten hemen sonra çağrılır; testler bununla
	// "kök çekildi, sonra bağlam iptal edildi" durumunu kurar.
	afterList func()

	mu         sync.Mutex
	listCalls  int
	fetchCalls int
	lastOpts   query.ListOptions
	lastIDs    []string
	lastFields []string
}

var _ query.Provider = (*fakeProvider)(nil)

// newProvider verilen kayıtlarla bir sahte sağlayıcı üretir. Kayıtların sırası
// List çağrısının döneceği sıradır.
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

// Entity sağlayıcının sunduğu entity adını döner.
func (p *fakeProvider) Entity() string { return p.entity }

// List kök kayıtları Offset/Limit ve alan seçimini uygulayarak döner.
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

// FetchByIDs verilen kimliklerin kayıtlarını döner; bilinmeyen kimlik atlanır.
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

// calls sağlayıcının aldığı çağrı sayılarını döner.
func (p *fakeProvider) calls() providerCalls {
	p.mu.Lock()
	defer p.mu.Unlock()
	return providerCalls{list: p.listCalls, fetch: p.fetchCalls}
}

// opts son List çağrısının seçeneklerini döner.
func (p *fakeProvider) opts() query.ListOptions {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastOpts
}

// fetchArgs son FetchByIDs çağrısının kimlik ve alan listelerini döner.
func (p *fakeProvider) fetchArgs() (ids, fields []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.lastIDs), slices.Clone(p.lastFields)
}

// project kaydın kopyasını döner; fields doluysa yalnızca istenen alanları alır.
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

// fakeLinks link.LinkService'i süreç içinde taklit eder ve çağrıları sayar.
//
// Sözleşmenin tamamını karşılar; ileri ve ters yön aynı forward haritasından
// türetilir, böylece iki yönün tutarlılığı testte de korunur.
type fakeLinks struct {
	defs    map[string]link.LinkDefinition
	forward map[string]map[string][]string

	// defErr ve listErr sıfırdan farklıysa ilgili çağrı bu hatayı döner.
	defErr  error
	listErr error

	definitionCalls atomic.Int64
	listCalls       atomic.Int64
	listManyCalls   atomic.Int64
	// listManyByToCalls ters yönün de TOPLU çağrıldığını (N+1 olmadığını)
	// ölçmek içindir.
	listManyByToCalls atomic.Int64
}

// ListManyByTo ters yönü toplu çözer; ileri haritayı tersine çevirerek üretir.
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

// newLinks verilen tanımlarla boş bağ tablosuna sahip bir sahte link servisi üretir.
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

// connect fromID ile verilen toID'ler arasında bağ kurar (test kurulumu için).
func (f *fakeLinks) connect(name, fromID string, toIDs ...string) *fakeLinks {
	if f.forward[name] == nil {
		f.forward[name] = make(map[string][]string)
	}
	f.forward[name][fromID] = append(f.forward[name][fromID], toIDs...)
	return f
}

// Define tanımı deftere yazar.
func (f *fakeLinks) Define(_ context.Context, def link.LinkDefinition) error {
	f.defs[def.Name] = def
	if f.forward[def.Name] == nil {
		f.forward[def.Name] = make(map[string][]string)
	}
	return nil
}

// Create bağ kurar.
func (f *fakeLinks) Create(_ context.Context, name, fromID, toID string) error {
	if _, ok := f.defs[name]; !ok {
		return errors.NotFound("link_not_defined", "%q tanımlı değil", name)
	}
	if slices.Contains(f.forward[name][fromID], toID) {
		return nil
	}
	f.forward[name][fromID] = append(f.forward[name][fromID], toID)
	return nil
}

// Delete bağı kaldırır.
func (f *fakeLinks) Delete(_ context.Context, name, fromID, toID string) error {
	f.forward[name][fromID] = slices.DeleteFunc(f.forward[name][fromID],
		func(id string) bool { return id == toID })
	return nil
}

// List fromID'ye bağlı toID'leri döner.
func (f *fakeLinks) List(ctx context.Context, name, fromID string) ([]string, error) {
	f.listCalls.Add(1)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.listErr != nil {
		return nil, f.listErr
	}
	if _, ok := f.defs[name]; !ok {
		return nil, errors.NotFound("link_not_defined", "%q tanımlı değil", name)
	}
	return slices.Clone(f.forward[name][fromID]), nil
}

// ListMany birden çok fromID'nin bağlarını tek çağrıda döner.
func (f *fakeLinks) ListMany(ctx context.Context, name string, fromIDs []string) (map[string][]string, error) {
	f.listManyCalls.Add(1)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.listErr != nil {
		return nil, f.listErr
	}
	if _, ok := f.defs[name]; !ok {
		return nil, errors.NotFound("link_not_defined", "%q tanımlı değil", name)
	}

	out := make(map[string][]string, len(fromIDs))
	for _, id := range fromIDs {
		if targets := f.forward[name][id]; len(targets) > 0 {
			out[id] = slices.Clone(targets)
		}
	}
	return out, nil
}

// Definition adı verilen linkin tanımını döner.
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
			"%q adıyla tanımlı link yok", name)
	}
	return def, nil
}

// reverseByTo ileri yön tablosunu tersine çevirip istenen toID'lerin
// fromID'lerini döner; ters yön sahtelerinin ortak gövdesidir.
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

// reverseLinks ters yönde TOPLU çözüm de sunan sahte link servisidir.
type reverseLinks struct {
	*fakeLinks
	listManyByToCalls atomic.Int64
}

// newReverseLinks ters yönü toplu destekleyen sahte link servisi üretir.
func newReverseLinks(defs ...link.LinkDefinition) *reverseLinks {
	return &reverseLinks{fakeLinks: newLinks(defs...)}
}

// ListManyByTo birden çok toID'nin bağlı olduğu fromID'leri tek çağrıda döner.
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

// --- ortak kurulum ----------------------------------------------------------

// newContainer verilen sağlayıcıları "<entity>.query" adıyla kaydeder (ADR 0004).
func newContainer(t *testing.T, providers ...*fakeProvider) *container.Container {
	t.Helper()

	c := container.New(nil)
	for _, p := range providers {
		require.NoError(t, c.Provide(p.Entity()+query.ProviderSuffix, p))
	}
	return c
}

// --- kopyalamayan sahte sağlayıcı -------------------------------------------

// sharingProvider döndürdüğü kayıtları KOPYALAMAYAN bir modülü taklit eder:
// süreç içinde tuttuğu haritaları doğrudan verir.
//
// Sözleşme kopyalamayı şart koşmaz (küçük bir referans tablosunu bellekte tutan
// bir modül makul olarak böyle davranır), bu yüzden Query'nin dönen haritalara
// yazmasının sağlayıcının KENDİ durumunu kirletmediği bu sahteyle kanıtlanır.
type sharingProvider struct {
	entity  string
	records []query.Record
}

var _ query.Provider = (*sharingProvider)(nil)

// newSharingProvider verilen kayıtları kopyalamadan sunan bir sağlayıcı üretir.
func newSharingProvider(entity string, records ...query.Record) *sharingProvider {
	return &sharingProvider{entity: entity, records: records}
}

// Entity sağlayıcının sunduğu entity adını döner.
func (p *sharingProvider) Entity() string { return p.entity }

// List kayıtları döner; yalnızca dilim kopyalanır, haritalar paylaşılır.
func (p *sharingProvider) List(_ context.Context, _ query.ListOptions) ([]query.Record, error) {
	return slices.Clone(p.records), nil
}

// FetchByIDs verilen kimliklerin kayıtlarını haritaları kopyalamadan döner.
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
