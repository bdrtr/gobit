package service_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/link"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// deletionTime sahte deponun soft delete damgasıdır; sabit olması testlerin
// zamana bağlı kalmamasını sağlar.
var deletionTime = time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

// creationTime sahte deponun kayıt oluşturma damgasıdır.
//
// Gerçek şemada created_at/updated_at sütunlarının DEFAULT now() değeri vardır
// ve satır RETURNING ile geri döner: damgayı üreten VERİTABANIDIR, çağıranın
// gönderdiği model değil. Sahte depo bunu taklit etmeseydi "saklanan satırı dön"
// kuralı burada hiç sınanamaz, sıfır damga dönen bir uç testten geçerdi.
var creationTime = time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)

// fakeLinker [service.Linker]'ın bellek içi uygulamasıdır.
//
// Gerçek link servisi çekirdektedir ve veritabanı ister; burada doğrulanan şey
// SERVİSİN link'leri nasıl kullandığıdır: eski bağı kaldırıp yenisini kurması,
// varyantın varlığını önce doğrulaması ve silmede temizlik yapması.
//
// Sahte, kardinaliteyi [service.Definitions] içindeki BİLDİRİMDEN okuyup zorlar
// (bkz. [cardinalities]): fiyat/stok bağlarında hem FROM hem TO ucu tektir ve
// ihlal errors.Conflict döner, satış kanalı bağında ise kısıt yoktur.
// Kardinalite zorlanmasaydı sahte, gerçek link servisinin reddedeceği bir akışı
// sessizce kabul eder ve testler var olmayan bir davranışı "kanıtlardı";
// bildirimden okunmasaydı da tersi olur — çoktan çoğa bir bağ, sahte yüzünden
// ikinci kanalda çakışma verirdi.
type fakeLinker struct {
	mu sync.Mutex
	// links link adı -> fromID -> toID listesi.
	links map[string]map[string][]string
	// cardinality link adı -> bildirilen kardinalite.
	cardinality map[string]link.Cardinality
	// createErr doluysa Create bu hatayı döner.
	createErr error
	// listErr doluysa List bu hatayı döner.
	listErr error
	// deletes kaldırılan bağların kaydıdır ("ad|from|to").
	deletes []string
}

// cardinalities link adına göre bildirilen kardinaliteleri döner.
//
// Değerler [service.Definitions] içindeki BİLDİRİMDEN okunur, elle
// tekrarlanmaz: bir bağın kardinalitesi değiştiğinde sahte de kendiliğinden
// değişir ve testler gerçekte olmayan bir kısıtı zorlamaya devam etmez.
func cardinalities() map[string]link.Cardinality {
	out := map[string]link.Cardinality{}
	for _, def := range service.Definitions() {
		out[def.Name] = def.Cardinality
	}
	return out
}

// newFakeLinker boş bir sahte link servisi üretir.
func newFakeLinker() *fakeLinker {
	return &fakeLinker{links: map[string]map[string][]string{}, cardinality: cardinalities()}
}

// Create bağı kaydeder; aynı çift için no-op, kardinalite ihlalinde
// errors.Conflict döner (gerçek servisin sözleşmesi).
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
	// FROM ucu yalnızca OneToOne'da benzersizdir: kayıt zaten başka bir hedefe
	// bağlı.
	if cardinality == link.OneToOne && len(f.links[name][fromID]) > 0 {
		return errors.Conflict("link_cardinality_violation",
			"%q linkinde %s zaten bağlı", name, fromID)
	}
	// TO ucu OneToOne ve OneToMany'de benzersizdir: hedef zaten başka bir kayda
	// bağlı. ManyToMany'de kısıt yoktur.
	if cardinality != link.ManyToMany {
		for otherFrom, targets := range f.links[name] {
			if otherFrom == fromID {
				continue
			}
			for _, existing := range targets {
				if existing == toID {
					return errors.Conflict("link_cardinality_violation",
						"%q linkinde %s zaten %s'e bağlı", name, toID, otherFrom)
				}
			}
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
//
// Sahte depo ile sahte link servisi BURADA birbirine bağlanır: satış kanalı
// bağı gerçekte tek bir tabloda durur ve servis onu link üzerinden yazıp depo
// sorgusuyla okur. Bağlanmasalardı yazma bir yere, okuma başka bir yere gider
// ve süzme testleri hiçbir şey kanıtlamazdı (bkz. memStore.links).
func newService(t *testing.T, store *memStore, links service.Linker, graph service.Grapher) *service.Service {
	t.Helper()
	if fake, ok := links.(*fakeLinker); ok && store != nil {
		store.links = fake
	}
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
