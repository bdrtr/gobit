package searchpg_test

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/eventbus"
	"github.com/bdrtr/gobit/internal/core/module"
	coreplugin "github.com/bdrtr/gobit/internal/core/plugin"
	"github.com/bdrtr/gobit/plugins/searchpg"
)

// Bu dosya eklentiyi DIŞARIDAN, çekirdeğin gördüğü yüzeyle sınar: kayıt
// noktaları, adlar ve kurulumun neyi çözmediği.
//
// Eklenti hiçbir modülü import EDEMEZ (internal/arch
// TestEklentilerModulleriImportEtmez) ve bu yasak test dosyalarını da kapsar;
// bu yüzden burada gerçek product modülü YOKTUR. Katalog, container'a
// "product.interop" adıyla konan sahte bir yüzeyle temsil edilir — çekirdek de
// product'ı tam olarak böyle görür.

// sahteVeriYolu abonelikleri kaydeden bir olay veri yoludur.
type sahteVeriYolu struct {
	mu        sync.Mutex
	abonelik  []string
	yayimlama []eventbus.Event
}

var _ eventbus.EventBus = (*sahteVeriYolu)(nil)

// Publish olayı listeye alır.
func (b *sahteVeriYolu) Publish(_ context.Context, e eventbus.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.yayimlama = append(b.yayimlama, e)

	return nil
}

// Subscribe abone olunan olay adını kaydeder.
func (b *sahteVeriYolu) Subscribe(eventName string, _ eventbus.Handler) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.abonelik = append(b.abonelik, eventName)

	return nil
}

// Shutdown hiçbir şey yapmaz.
func (b *sahteVeriYolu) Shutdown(_ context.Context) error { return nil }

// kurulum eklentiyi verilen container üzerinde Start'a kadar götürür.
func kurulum(t *testing.T, c *container.Container) (*module.Registry, *sahteVeriYolu, error) {
	t.Helper()

	log := slog.New(slog.DiscardHandler)
	moduller := module.NewRegistry(log, nil)
	bus := &sahteVeriYolu{}

	reg := coreplugin.NewRegistry(log)
	reg.Add(searchpg.New())

	h := coreplugin.NewHost(c, moduller, bus, log, nil)
	if err := reg.Install(t.Context(), h); err != nil {
		return moduller, bus, err
	}

	return moduller, bus, reg.Start(t.Context(), h)
}

// TestKurulumModuluVeAbonelikleriKaydeder eklentinin üç uzatma noktasının
// ikisini kurulumda kullandığını doğrular.
func TestKurulumModuluVeAbonelikleriKaydeder(t *testing.T) {
	t.Parallel()

	c := container.New(slog.New(slog.DiscardHandler))
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	moduller, bus, err := kurulum(t, c)
	require.NoError(t, err)

	kayitli := moduller.Modules()
	require.Len(t, kayitli, 1, "eklenti KENDİ modülünü kayda eklemeli")
	assert.Equal(t, searchpg.ModuleName, kayitli[0].Name())
	assert.NotNil(t, kayitli[0].Migrations(), "modül kendi migration'ını getirmeli")

	assert.Equal(t,
		[]string{"product.created", "product.updated", "product.deleted"},
		bus.abonelik,
		"indeks üç katalog olayıyla taze tutulur; adlar modüller arası sözleşmedir")
}

// TestKurulumBosContainerdaCalisir Setup'ın container'dan HİÇBİR ŞEY
// çözmediğini doğrular.
//
// Kurulum sırasında modüller henüz ayağa kalkmamıştır: "product.interop" o anda
// container'da YOKTUR. Eklenti onu Setup'ta çözmeye çalışsaydı, product kurulu
// olsa bile açılış hata verirdi — hiçbir şeyin gerçekten eksik olmadığı bir
// hatayla.
func TestKurulumBosContainerdaCalisir(t *testing.T) {
	t.Parallel()

	c := container.New(slog.New(slog.DiscardHandler))
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	moduller, _, err := kurulum(t, c)

	require.NoError(t, err, "kurulum boş bir container'da da tamamlanmalı")
	assert.Len(t, moduller.Modules(), 1)
	assert.False(t, c.Has("product.interop"), "kurulum katalog kaydını ARAMAMALI, yaratmamalı")
}

// TestKurulumAyarIstemez eklentinin yapılandırmasız kurulduğunu doğrular.
//
// paymentstripe'ın aksine burada eksikse açılışı durduracak bir ayar yoktur;
// indeks tablosu migration'la kurulur ve arama motoru zaten var olan
// PostgreSQL'dir.
func TestKurulumAyarIstemez(t *testing.T) {
	t.Parallel()

	c := container.New(slog.New(slog.DiscardHandler))
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	_, _, err := kurulum(t, c)

	assert.NoError(t, err)
}

// TestEklentiRouteKancasiKullanmaz uçların MODÜL yaşam döngüsünden geldiğini
// doğrular.
//
// coreplugin.Host.AddRoutes ile bağlanan route'lar modül route'larından SONRA
// ve ayrı bir çakışma denetiminden geçerek eklenir. Arama uçları oraya değil
// modülün Routes'una aittir: uçlar modülün servisine bağlıdır ve modül
// kaydedilmemişse hiç var olmamalıdırlar.
func TestEklentiRouteKancasiKullanmaz(t *testing.T) {
	t.Parallel()

	c := container.New(slog.New(slog.DiscardHandler))
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	log := slog.New(slog.DiscardHandler)
	reg := coreplugin.NewRegistry(log)
	reg.Add(searchpg.New())
	h := coreplugin.NewHost(c, module.NewRegistry(log, nil), &sahteVeriYolu{}, log, nil)
	require.NoError(t, reg.Install(t.Context(), h))

	router := chi.NewRouter()
	require.NoError(t, reg.MountRoutes(router, h))

	var desenler []string
	require.NoError(t, chi.Walk(router,
		func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			desenler = append(desenler, method+" "+route)

			return nil
		}))
	assert.Empty(t, desenler, "eklenti route kancasına hiçbir uç bağlamamalı")
}

// TestAdlarSozlesmedir dışarıdan görünen adların bilinçli seçimler olduğunu
// sabitler.
//
// Eklenti adı PLUGINS listesine yazılır; modül adı ise doğrudan bir SQL tablo
// adına ("searchpg_schema_migrations") ve yetki önekine dönüşür. Bu yüzden
// ikisi ayrıdır: modül adı tire taşıyamaz (bkz. core/db.MigrationsTable).
func TestAdlarSozlesmedir(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "search-pg", searchpg.Name)
	assert.Equal(t, "searchpg", searchpg.ModuleName)
	assert.Equal(t, "searchpg:write", searchpg.ScopeWrite,
		"yetki sözlüğü modüllerinkiyle aynı biçimde olmalı: <modül>:write")
	assert.Equal(t, "/store/v1/search", searchpg.SearchPath)
	assert.Equal(t, "/admin/v1/search/reindex", searchpg.ReindexPath)
	assert.NotEqual(t, searchpg.Name, searchpg.ModuleName,
		"modül adı migration sürüm tablosuna dönüşür ve tire taşıyamaz")
}

// TestMigrationlarGeriAlinabilir her up dosyasının down çiftini doğrular.
//
// internal/arch'taki aynı adlı kapı YALNIZCA internal/modules altını tarar;
// plugins/ ağacı hiçbir mimari testin kapsamında değildir. Geri alınamayan bir
// migration, açılışta uygulanan bir şemayı geri alınamaz kılar.
func TestMigrationlarGeriAlinabilir(t *testing.T) {
	t.Parallel()

	moduller, _, err := kurulum(t, container.New(slog.New(slog.DiscardHandler)))
	require.NoError(t, err)
	require.Len(t, moduller.Modules(), 1)

	src := moduller.Modules()[0].Migrations()
	require.NotNil(t, src)

	girdiler, err := fs.ReadDir(src, ".")
	require.NoError(t, err)

	var uplar []string
	mevcut := map[string]struct{}{}
	for _, girdi := range girdiler {
		mevcut[girdi.Name()] = struct{}{}
		if strings.HasSuffix(girdi.Name(), ".up.sql") {
			uplar = append(uplar, girdi.Name())
		}
	}

	require.NotEmpty(t, uplar, "eklenti kendi şemasını getirmeli")
	for _, up := range uplar {
		down := strings.TrimSuffix(up, ".up.sql") + ".down.sql"
		assert.Contains(t, mevcut, down, "%s dosyasının down çifti olmalı", up)
	}
}
