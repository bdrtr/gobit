//go:build integration

// Bu dosyadaki testler gerçek bir PostgreSQL örneği (dolayısıyla Docker)
// gerektirir; `make test` hızlı kalsın diye `integration` etiketiyle
// ayrılmıştır. Çalıştırmak için: make test-integration
//
// Birim testleri sahte bir link servisiyle çalışır ve yalnızca Query'nin
// mantığını kanıtlar. Buradaki testler GERÇEK link servisiyle (Postgres'te
// yaşayan link tabloları) uçtan uca akışı doğrular: iki dummy modül, gerçek
// link tanımı, gerçek bağlar, container'dan adla çözülen sağlayıcılar. Faz 2'nin
// "iki dummy modül ile uçtan uca doğrula" maddesinin karşılığı budur.
package query_test

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/link"
	"github.com/bdrtr/gobit/internal/core/query"
)

const postgresImage = "postgres:16-alpine"

// testPool tüm entegrasyon testlerinin paylaştığı havuzdur.
var testPool *db.Pool

// Entegrasyon testlerinde kullanılan link tanımları. İki dummy modül arasında
// hem çok uçlu hem tek uçlu bir ilişki kurulur; böylece şeklin kardinaliteden
// geldiği gerçek veritabanı üzerinde de doğrulanır.
var (
	itemPrice = link.LinkDefinition{
		Name:        "item_price",
		From:        link.LinkSide{Module: "shop_item", Field: "item_id"},
		To:          link.LinkSide{Module: "shop_price", Field: "price_id"},
		Cardinality: link.OneToMany,
	}
	itemMainPrice = link.LinkDefinition{
		Name:        "item_main_price",
		From:        link.LinkSide{Module: "shop_item", Field: "item_id"},
		To:          link.LinkSide{Module: "shop_price", Field: "price_id"},
		Cardinality: link.OneToOne,
	}
)

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres tek bir Postgres konteyneri kaldırıp tüm testleri onun
// üzerinde çalıştırır. os.Exit defer'ları atladığı için ayrı fonksiyondadır.
func runWithPostgres(m *testing.M) int {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_test"),
		tcpostgres.WithUsername("gobit"),
		tcpostgres.WithPassword("gobit"),
		tcpostgres.BasicWaitStrategies(),
	)
	defer func() {
		if termErr := testcontainers.TerminateContainer(ctr); termErr != nil {
			fmt.Fprintf(os.Stderr, "postgres konteyneri durdurulamadı: %v\n", termErr)
		}
	}()
	if err != nil {
		fmt.Fprintf(os.Stderr, "postgres konteyneri başlatılamadı: %v\n", err)
		return 1
	}

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "bağlantı adresi alınamadı: %v\n", err)
		return 1
	}

	testPool, err = db.New(ctx, db.DefaultConfig(dsn), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "havuz kurulamadı: %v\n", err)
		return 1
	}
	defer testPool.Close()

	return m.Run()
}

// --- dummy modüller ---------------------------------------------------------

// dummyModule entegrasyon testlerindeki bir commerce modülünü temsil eder:
// yalnızca KENDİ tablosunu okur (Prensip 2.1) ve container'a "<entity>.query"
// adıyla konan query.Provider yüzeyini karşılar (ADR 0004).
//
// Sağlayıcı, gerçek bir modül repository'si gibi tek SQL sorgusuyla batch
// okuma yapar; çağrı sayaçları N+1 iddiasını gerçek veritabanı üzerinde de
// kanıtlar.
type dummyModule struct {
	entity string
	table  string
	pool   *pgxpool.Pool

	listCalls  atomic.Int64
	fetchCalls atomic.Int64
}

var _ query.Provider = (*dummyModule)(nil)

// yeniModul verilen entity için tabloyu sıfırdan oluşturur ve modülü döner.
func yeniModul(t *testing.T, entity, kolonlar string) *dummyModule {
	t.Helper()

	m := &dummyModule{entity: entity, table: "dummy_" + entity, pool: testPool.Pool()}

	_, err := m.pool.Exec(t.Context(), "DROP TABLE IF EXISTS "+m.table)
	require.NoError(t, err)
	_, err = m.pool.Exec(t.Context(),
		fmt.Sprintf("CREATE TABLE %s (id TEXT PRIMARY KEY, %s)", m.table, kolonlar))
	require.NoError(t, err)

	return m
}

// ekle modülün tablosuna tek bir kayıt yazar.
func (m *dummyModule) ekle(t *testing.T, kolonlar string, degerler ...any) {
	t.Helper()

	yer := make([]string, len(degerler))
	for i := range degerler {
		yer[i] = fmt.Sprintf("$%d", i+1)
	}
	_, err := m.pool.Exec(t.Context(),
		fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", m.table, kolonlar, strings.Join(yer, ", ")),
		degerler...)
	require.NoError(t, err)
}

// Entity modülün sunduğu entity adını döner.
func (m *dummyModule) Entity() string { return m.entity }

// List kök kayıtları döner; yalnızca "status" filtresini tanır.
func (m *dummyModule) List(ctx context.Context, opts query.ListOptions) ([]query.Record, error) {
	m.listCalls.Add(1)

	sql := "SELECT * FROM " + m.table
	args := make([]any, 0, 1)
	for alan, deger := range opts.Filters {
		if alan != "status" {
			// ADR 0004: desteklenmeyen alan sağlayıcı tarafından reddedilir.
			return nil, errors.Invalid("dummy_unknown_filter",
				"%q sağlayıcısı %q filtresini desteklemiyor", m.entity, alan)
		}
		args = append(args, deger)
		sql += fmt.Sprintf(" WHERE status = $%d", len(args))
	}
	sql += " ORDER BY id"
	if opts.Limit > 0 {
		sql += fmt.Sprintf(" LIMIT %d", opts.Limit)
	}
	if opts.Offset > 0 {
		sql += fmt.Sprintf(" OFFSET %d", opts.Offset)
	}

	return m.sorgula(ctx, sql, opts.Fields, args...)
}

// FetchByIDs verilen kimliklerin kayıtlarını TEK sorguda döner.
func (m *dummyModule) FetchByIDs(ctx context.Context, ids, fields []string) ([]query.Record, error) {
	m.fetchCalls.Add(1)

	return m.sorgula(ctx, "SELECT * FROM "+m.table+" WHERE id = ANY($1)", fields, ids)
}

// sorgula sorguyu çalıştırır ve satırları alan seçimini uygulayarak Record'a çevirir.
func (m *dummyModule) sorgula(ctx context.Context, sql string, fields []string, args ...any) ([]query.Record, error) {
	rows, err := m.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindUnavailable, "dummy_query_failed",
			"%q sağlayıcısı sorgulanamadı", m.entity)
	}

	satirlar, err := pgx.CollectRows(rows, pgx.RowToMap)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindUnavailable, "dummy_scan_failed",
			"%q sağlayıcısının satırları okunamadı", m.entity)
	}

	out := make([]query.Record, 0, len(satirlar))
	for _, satir := range satirlar {
		rec := make(query.Record, len(satir))
		for alan, deger := range satir {
			if len(fields) > 0 && !slices.Contains(fields, alan) {
				continue
			}
			rec[alan] = deger
		}
		out = append(out, rec)
	}
	return out, nil
}

// --- testler ----------------------------------------------------------------

func TestGraphUctanUcaIkiDummyModul(t *testing.T) {
	ctx := t.Context()

	items, prices, links := kurulum(t)

	c := container.New(nil)
	require.NoError(t, c.Provide(items.Entity()+query.ProviderSuffix, items))
	require.NoError(t, c.Provide(prices.Entity()+query.ProviderSuffix, prices))

	q := query.New(links, c, nil)

	got, err := q.Graph(ctx, query.GraphSpec{
		Entity:  "shop_item",
		Fields:  []string{"title"},
		Filters: map[string]any{"status": "published"},
		Expand: []query.Expansion{
			{Link: "item_price", As: "fiyatlar", Fields: []string{"amount", "currency"}},
			{Link: "item_main_price", As: "ana_fiyat"},
		},
	})
	require.NoError(t, err)
	require.Len(t, got, 2, "yalnızca yayımlanmış kayıtlar dönmeli")

	// Kök: seçilen alan + birleştirme için eklenen kimlik.
	assert.Equal(t, "item_1", got[0]["id"])
	assert.Equal(t, "Tişört", got[0]["title"])
	assert.NotContains(t, got[0], "status", "seçilmeyen alan dönmemeli")

	// OneToMany: dilim, link'in sıralamasına göre.
	fiyatlar, ok := got[0]["fiyatlar"].([]query.Record)
	require.Truef(t, ok, "OneToMany dilim yazmalı; gelen tip: %T", got[0]["fiyatlar"])
	require.Len(t, fiyatlar, 2)
	assert.Equal(t, "price_1", fiyatlar[0]["id"])
	assert.Equal(t, int64(1990), fiyatlar[0]["amount"])
	assert.Equal(t, "TRY", fiyatlar[0]["currency"])
	assert.Equal(t, "price_2", fiyatlar[1]["id"])

	// OneToOne: tek kayıt, bağı olmayan kökte nil.
	ana, ok := got[0]["ana_fiyat"].(query.Record)
	require.Truef(t, ok, "OneToOne tek kayıt yazmalı; gelen tip: %T", got[0]["ana_fiyat"])
	assert.Equal(t, "price_1", ana["id"])

	ikinci, ok := got[1]["fiyatlar"].([]query.Record)
	require.True(t, ok)
	require.Len(t, ikinci, 1)
	assert.Equal(t, "price_3", ikinci[0]["id"])
	assert.Nil(t, got[1]["ana_fiyat"], "bağı olmayan kökte tek uçlu genişletme nil olmalı")

	// N+1 yok: genişletme başına tek sorgu, kök için tek List.
	assert.Equal(t, int64(1), items.listCalls.Load())
	assert.Zero(t, items.fetchCalls.Load())
	assert.Equal(t, int64(2), prices.fetchCalls.Load(),
		"iki genişletme, iki batch sorgu; kayıt başına sorgu yapılmamalı")
}

// TestGraphTersYonGercekLinkServisiyleCalisir kök entity link'in TO ucundayken
// genişletmenin çalıştığını doğrular.
//
// Regresyon: link ve query paketleri ayrı ajanlar tarafından yazıldığında
// LinkService yalnızca From->To yönünü sunuyor, Query ise ters yön için somut
// tipte olmayan bir yüzey (ListManyByTo) arıyordu. Sahte servisle yazılan birim
// testleri geçiyor, GERÇEK servisle her ters yönlü genişletme düşüyordu.
// ListManyByTo artık LinkService sözleşmesinin parçasıdır.
func TestGraphTersYonGercekLinkServisiyleCalisir(t *testing.T) {
	items, prices, links := kurulum(t)

	c := container.New(nil)
	require.NoError(t, c.Provide(items.Entity()+query.ProviderSuffix, items))
	require.NoError(t, c.Provide(prices.Entity()+query.ProviderSuffix, prices))

	q := query.New(links, c, nil)

	// Kök entity link'in TO ucunda: fiyattan ürüne doğru çözülüyor.
	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "shop_price",
		Expand: []query.Expansion{{Link: "item_price", As: "urun"}},
	})
	require.NoError(t, err)
	require.NotEmpty(t, got, "ters yönlü genişletme sonuç döndürmeli")

	// En az bir fiyatın bağlı olduğu ürün gelmiş olmalı.
	var bagliOlan query.Record
	for _, rec := range got {
		if urun, ok := rec["urun"]; ok && urun != nil {
			bagliOlan = rec
			break
		}
	}
	require.NotNil(t, bagliOlan, "bağlı en az bir fiyat kaydı bekleniyordu")

	urunler, ok := bagliOlan["urun"].([]query.Record)
	if ok {
		require.NotEmpty(t, urunler)
		assert.Contains(t, urunler[0], "id")
	} else {
		urun, tekil := bagliOlan["urun"].(query.Record)
		require.True(t, tekil, "genişletme Record veya []Record olmalı, gelen: %T", bagliOlan["urun"])
		assert.Contains(t, urun, "id")
	}

	// N+1 yok: ters yön de tek batch.
	assert.Equal(t, int64(1), items.fetchCalls.Load(),
		"ters yönde de kayıt başına değil, tek batch çağrı yapılmalı")
}

func TestGraphGercekLinkServisindeBilinmeyenLinkNotFoundDoner(t *testing.T) {
	items, prices, links := kurulum(t)

	c := container.New(nil)
	require.NoError(t, c.Provide(items.Entity()+query.ProviderSuffix, items))
	require.NoError(t, c.Provide(prices.Entity()+query.ProviderSuffix, prices))

	q := query.New(links, c, nil)

	_, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "shop_item",
		Expand: []query.Expansion{{Link: "tanimsiz_link"}},
	})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "beklenen sınıf NotFound, gelen: %v", err)
	assert.Contains(t, err.Error(), "tanimsiz_link")
}

func TestGraphKayitliOlmayanSaglayiciNotFoundDoner(t *testing.T) {
	items, _, links := kurulum(t)

	// shop_price.query bilerek kaydedilmiyor.
	c := container.New(nil)
	require.NoError(t, c.Provide(items.Entity()+query.ProviderSuffix, items))

	q := query.New(links, c, nil)

	_, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "shop_item",
		Expand: []query.Expansion{{Link: "item_price"}},
	})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "beklenen sınıf NotFound, gelen: %v", err)

	var typed *errors.Error
	require.True(t, errors.As(err, &typed))
	assert.Equal(t, "shop_price"+query.ProviderSuffix, typed.Details["looked_up_name"])
}

func TestGraphSaglayiciHatasiTumCagriyiDusurur(t *testing.T) {
	items, prices, links := kurulum(t)

	c := container.New(nil)
	require.NoError(t, c.Provide(items.Entity()+query.ProviderSuffix, items))
	require.NoError(t, c.Provide(prices.Entity()+query.ProviderSuffix, prices))

	q := query.New(links, c, nil)

	// Sağlayıcı tanımadığı filtreyi reddeder (ADR 0004); Query bunu yutmamalı.
	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity:  "shop_item",
		Filters: map[string]any{"bilinmeyen_alan": "x"},
		Expand:  []query.Expansion{{Link: "item_price"}},
	})
	require.Error(t, err)
	assert.Nil(t, got)
	assert.True(t, errors.IsInvalid(err), "sağlayıcının hata sınıfı korunmalı, gelen: %v", err)
	assert.Zero(t, prices.fetchCalls.Load(), "kök başarısızsa genişletmeye geçilmemeli")
}

// --- yardımcılar ------------------------------------------------------------

// kurulum iki dummy modülü, verilerini ve gerçek link servisini hazırlar.
//
// Modül tabloları her çağrıda sıfırdan kurulur; link tabloları Define'dan sonra
// boşaltılır. Böylece testler birbirinden ve önceki koşulardan bağımsızdır.
func kurulum(t *testing.T) (items, prices *dummyModule, links link.LinkService) {
	t.Helper()
	ctx := t.Context()

	items = yeniModul(t, "shop_item", "title TEXT NOT NULL, status TEXT NOT NULL")
	prices = yeniModul(t, "shop_price", "amount BIGINT NOT NULL, currency TEXT NOT NULL")

	items.ekle(t, "id, title, status", "item_1", "Tişört", "published")
	items.ekle(t, "id, title, status", "item_2", "Şapka", "published")
	items.ekle(t, "id, title, status", "item_3", "Taslak", "draft")

	prices.ekle(t, "id, amount, currency", "price_1", int64(1990), "TRY")
	prices.ekle(t, "id, amount, currency", "price_2", int64(2490), "TRY")
	prices.ekle(t, "id, amount, currency", "price_3", int64(3990), "TRY")

	links = link.New(testPool, nil)
	require.NoError(t, links.Define(ctx, itemPrice))
	require.NoError(t, links.Define(ctx, itemMainPrice))
	baglariTemizle(t, itemPrice.Name, itemMainPrice.Name)

	require.NoError(t, links.Create(ctx, itemPrice.Name, "item_1", "price_1"))
	require.NoError(t, links.Create(ctx, itemPrice.Name, "item_1", "price_2"))
	require.NoError(t, links.Create(ctx, itemPrice.Name, "item_2", "price_3"))
	require.NoError(t, links.Create(ctx, itemMainPrice.Name, "item_1", "price_1"))

	return items, prices, links
}

// baglariTemizle verilen link tablolarını boşaltır.
func baglariTemizle(t *testing.T, names ...string) {
	t.Helper()

	for _, name := range names {
		table, err := link.TableName(name)
		require.NoError(t, err)

		_, err = testPool.Pool().Exec(t.Context(), "TRUNCATE TABLE "+table)
		require.NoError(t, err)
	}
}
