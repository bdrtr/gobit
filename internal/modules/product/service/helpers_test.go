package service_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// deletionTime sahte deponun soft delete damgasıdır; sabit olması testlerin
// zamana bağlı kalmamasını sağlar.
var deletionTime = time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

// fakeLinker [service.Linker]'ın bellek içi uygulamasıdır.
//
// Gerçek link servisi çekirdektedir ve veritabanı ister; burada doğrulanan şey
// SERVİSİN link'leri nasıl kullandığıdır: eski bağı kaldırıp yenisini kurması,
// varyantın varlığını önce doğrulaması ve silmede temizlik yapması.
type fakeLinker struct {
	mu sync.Mutex
	// links link adı -> fromID -> toID listesi.
	links map[string]map[string][]string
	// createErr doluysa Create bu hatayı döner.
	createErr error
	// listErr doluysa List bu hatayı döner.
	listErr error
	// deletes kaldırılan bağların kaydıdır ("ad|from|to").
	deletes []string
}

// newFakeLinker boş bir sahte link servisi üretir.
func newFakeLinker() *fakeLinker {
	return &fakeLinker{links: map[string]map[string][]string{}}
}

// Create bağı kaydeder.
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
	f.links[name][fromID] = append(f.links[name][fromID], toID)
	return nil
}

// Delete bağı kaldırır ve kaydını tutar.
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

// List bağlı kimlikleri döner.
func (f *fakeLinker) List(_ context.Context, name, fromID string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]string(nil), f.links[name][fromID]...), nil
}

// linked verilen bağın hedeflerini döner (test iddiaları için).
func (f *fakeLinker) linked(name, fromID string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.links[name][fromID]...)
}

// fakeGraph [service.Grapher]'ın sahte uygulamasıdır.
//
// Kaydettiği spec'ler, store listelemesinin Query katmanını DOĞRU ÇAĞIRDIĞINI
// (tek çağrı, iki genişletme, doğru link adları) kanıtlar.
type fakeGraph struct {
	mu      sync.Mutex
	specs   []query.GraphSpec
	records []query.Record
	err     error
}

// Graph kaydedilen kayıtları döner ve çağrıyı kaydeder.
func (f *fakeGraph) Graph(_ context.Context, spec query.GraphSpec) ([]query.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.specs = append(f.specs, spec)
	if f.err != nil {
		return nil, f.err
	}
	return f.records, nil
}

// callCount Graph'ın kaç kez çağrıldığını döner.
func (f *fakeGraph) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.specs)
}

// lastSpec son çağrının spec'ini döner.
func (f *fakeGraph) lastSpec(t *testing.T) query.GraphSpec {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.NotEmpty(t, f.specs, "Graph hiç çağrılmadı")
	return f.specs[len(f.specs)-1]
}

// newService test için servis kurar.
func newService(t *testing.T, store *memStore, links service.Linker, graph service.Grapher) *service.Service {
	t.Helper()
	svc, err := service.New(service.Options{Repo: store, Links: links, Query: graph})
	require.NoError(t, err)
	return svc
}

// seedProduct testlerin ortak kurulumudur: yayında bir ürün ve bir varyant.
func seedProduct(t *testing.T, svc *service.Service, handle, title string) models.Product {
	t.Helper()
	product, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Handle: handle,
		Title:  title,
		Status: models.StatusPublished,
		Variants: []service.CreateVariantInput{
			{Title: "Tek beden"},
		},
	})
	require.NoError(t, err)
	require.Len(t, product.Variants, 1)
	return product
}

// ptr verilen değerin adresini döner.
func ptr[T any](v T) *T { return &v }
