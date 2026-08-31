//go:build integration

// Bu dosyadaki testler gerçek bir PostgreSQL örneği (dolayısıyla Docker)
// gerektirir; `make test` hızlı kalsın diye `integration` etiketiyle
// ayrılmıştır. Çalıştırmak için: make test-integration
//
// Buradaki iddiaların hiçbiri sahteyle kanıtlanamaz: tsvector eşleşmesi, alaka
// sıralaması, websearch_to_tsquery'nin bozuk girdiyi sözdizimi hatasına
// ÇEVİRMEMESİ, süpürmenin yalnızca bayat satırları silmesi ve migration'ın
// gerçekten geri alınabilmesi ancak sunucunun kendisine sorularak görülür.
//
// Katalog burada da SAHTEDİR ve olmak zorundadır: eklenti hiçbir modülü import
// edemez (internal/arch TestEklentilerModulleriImportEtmez) ve yasak test
// dosyalarını da kapsar. Yani bu dosya "indeks + uçlar gerçek veritabanında
// çalışıyor" der; "product'ın JSON şeması bu sahteyle aynı" DEMEZ — o bağ
// yalnızca uçtan uca bir kurulumda kanıtlanabilir (bkz. görev raporu).
package searchpg

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/db"
	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/link"
	"github.com/bdrtr/gobit/internal/core/query"
)

const postgresImage = "postgres:16-alpine"

var (
	// testPool tüm testlerin paylaştığı havuzdur.
	testPool *db.Pool
	// testDSN paylaşılan veritabanının adresidir.
	testDSN string
)

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres tek bir Postgres konteyneri kaldırır, eklentinin şemasını
// uygular ve tüm testleri onun üzerinde çalıştırır.
func runWithPostgres(m *testing.M) int {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_searchpg"),
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

	testDSN, err = ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "bağlantı adresi alınamadı: %v\n", err)
		return 1
	}

	// Şema, üretimdeki yolla AYNI şekilde uygulanır: modülün Migrations()'ı ve
	// modül adı. Elle CREATE TABLE yazmak, migration'ın kendisini sınamadan
	// bırakırdı.
	if err = db.Migrate(ctx, testDSN, migrationsRoot, ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "searchpg şeması uygulanamadı: %v\n", err)
		return 1
	}

	testPool, err = db.New(ctx, db.DefaultConfig(testDSN), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bağlantı havuzu açılamadı: %v\n", err)
		return 1
	}
	defer testPool.Close()

	return m.Run()
}

// gercekIndeks boş bir indeks tablosu üzerinde depo döner.
func gercekIndeks(t *testing.T) *indeks {
	t.Helper()

	_, err := testPool.Pool().Exec(t.Context(), "TRUNCATE searchpg_product")
	require.NoError(t, err)

	return newIndeks(testPool.Pool())
}

// yaz verilen belgeleri indekse yazar.
func yaz(t *testing.T, i *indeks, belgeler ...belge) {
	t.Helper()

	require.NoError(t, i.Upsert(t.Context(), belgeler))
}

// TestIndeksYazarAramaVeSiler indeksin temel döngüsünü gerçek SQL'de doğrular.
//
// Test verisi ASCII'dir ve bu bilinçlidir: PostgreSQL'in küçük harfe çevirmesi
// veritabanının ctype ayarına bağlıdır ve C locale ile kurulmuş bir kümede
// ASCII dışı harfler KATLANMAZ. Testin konteynerin locale'ine bağlı olması,
// eklentinin davranışı hakkında yanlış bir güven verirdi.
func TestIndeksYazarAramaVeSiler(t *testing.T) {
	i := gercekIndeks(t)

	yaz(t, i,
		belge{urunID: "prod_1", baslik: "Mavi gomlek", anahtar: "mavi-gomlek SKU-1", metin: "Pamuklu yazlik"},
		belge{urunID: "prod_2", baslik: "Siyah pantolon", anahtar: "siyah-pantolon SKU-2", metin: "Kot pantolon"},
	)

	ids, err := i.Search(t.Context(), "gomlek", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"prod_1"}, ids)

	// SKU aranabilir olmalı: müşteri ürün kodunu yapıştırdığında ürünü bulmalı.
	ids, err = i.Search(t.Context(), "SKU-2", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"prod_2"}, ids)

	// Büyük/küçük harf duyarsızlığı 'simple' sözlüğünün verdiğidir.
	ids, err = i.Search(t.Context(), "GOMLEK", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"prod_1"}, ids)

	silinen, err := i.Delete(t.Context(), "prod_1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), silinen)

	ids, err = i.Search(t.Context(), "gomlek", 10, 0)
	require.NoError(t, err)
	assert.Empty(t, ids, "silinen ürün aramada görünmemeli")
}

// TestAlakaSiralamasiAgirliklariKullanir başlıkta geçen bir kelimenin
// açıklamada geçenden ÖNCE geldiğini doğrular.
//
// Ağırlık olmasaydı sıralama rastgeleye yakın olurdu ve "alaka sırası" iddiası
// boş kalırdı: uzun açıklamalı bir ürün, tam adı aranan üründen önce gelirdi.
func TestAlakaSiralamasiAgirliklariKullanir(t *testing.T) {
	i := gercekIndeks(t)

	yaz(t, i,
		belge{urunID: "prod_baslik", baslik: "Sneaker", anahtar: "", metin: "Rahat ayakkabi"},
		belge{urunID: "prod_metin", baslik: "Bot", anahtar: "", metin: "Sneaker gibi rahat bir bot"},
		belge{urunID: "prod_anahtar", baslik: "Terlik", anahtar: "sneaker-benzeri", metin: ""},
	)

	ids, err := i.Search(t.Context(), "sneaker", 10, 0)
	require.NoError(t, err)
	require.Len(t, ids, 3, "üç kayıt da eşleşmeli")
	assert.Equal(t, "prod_baslik", ids[0], "başlık eşleşmesi (A) en önde olmalı")
	assert.Equal(t, "prod_metin", ids[2], "açıklama eşleşmesi (C) en arkada olmalı")
}

// TestSayfalamaDeterministiktir eşit alakalı kayıtların sayfalar arasında
// KAYMADIĞINI doğrular.
//
// İkinci sıralama anahtarı olmasaydı, aynı sorgu iki sayfada aynı ürünü
// gösterebilir ya da bir ürünü hiç göstermeyebilirdi.
func TestSayfalamaDeterministiktir(t *testing.T) {
	i := gercekIndeks(t)

	var belgeler []belge
	for n := range 5 {
		belgeler = append(belgeler, belge{
			urunID: "prod_" + strconv.Itoa(n),
			baslik: "Ayni baslik",
		})
	}
	yaz(t, i, belgeler...)

	ilk, err := i.Search(t.Context(), "ayni", 2, 0)
	require.NoError(t, err)
	ikinci, err := i.Search(t.Context(), "ayni", 2, 2)
	require.NoError(t, err)
	ucuncu, err := i.Search(t.Context(), "ayni", 2, 4)
	require.NoError(t, err)

	tum := append(append(append([]string{}, ilk...), ikinci...), ucuncu...)
	assert.Equal(t, []string{"prod_0", "prod_1", "prod_2", "prod_3", "prod_4"}, tum,
		"sayfalar çakışmadan ve boşluk bırakmadan tüm kayıtları vermeli")
}

// TestBozukSorgu500Uretmez kullanıcı metninin sorguyu düşürmediğini doğrular.
//
// to_tsquery seçilseydi bu girdiler sözdizimi hatası verir ve arama kutusuna ne
// yazıldığına bağlı 500'ler üretirdi; websearch_to_tsquery hepsini metin olarak
// ele alır.
func TestBozukSorgu500Uretmez(t *testing.T) {
	i := gercekIndeks(t)
	yaz(t, i, belge{urunID: "prod_1", baslik: "Mavi gomlek"})

	for _, sorgu := range []string{"& &", "!", "a | | b", "((", "'", `"`, "-"} {
		ids, err := i.Search(t.Context(), sorgu, 10, 0)
		require.NoError(t, err, "sorgu %q hata üretmemeli", sorgu)
		assert.Empty(t, ids, "anlamsız sorgu eşleşme döndürmemeli: %q", sorgu)
	}

	// websearch'ün getirdiği sözdizimi de çalışmalı: tırnak tam ifade demektir.
	ids, err := i.Search(t.Context(), `"mavi gomlek"`, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"prod_1"}, ids)
}

// TestUpsertAyniKimligiGunceller ikinci yazmanın yeni satır AÇMADIĞINI
// doğrular; abonenin idempotent olması buna dayanır.
func TestUpsertAyniKimligiGunceller(t *testing.T) {
	i := gercekIndeks(t)

	yaz(t, i, belge{urunID: "prod_1", baslik: "Eski baslik"})
	yaz(t, i, belge{urunID: "prod_1", baslik: "Yeni baslik"})

	var satir int
	require.NoError(t, testPool.Pool().
		QueryRow(t.Context(), "SELECT count(*) FROM searchpg_product").Scan(&satir))
	assert.Equal(t, 1, satir, "aynı ürün için tek satır olmalı")

	ids, err := i.Search(t.Context(), "yeni", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"prod_1"}, ids)

	ids, err = i.Search(t.Context(), "eski", 10, 0)
	require.NoError(t, err)
	assert.Empty(t, ids, "güncellenen belge eski metniyle eşleşmemeli")
}

// TestSupurmeSadeceBayatlariSiler süpürmenin turdan sonra yazılan satırlara
// dokunmadığını doğrular.
//
// Eşik veritabanı saatinden alınır; uygulama saati kullanılsaydı iki saat
// arasındaki kayma taze kayıtları bayat gösterebilirdi.
func TestSupurmeSadeceBayatlariSiler(t *testing.T) {
	i := gercekIndeks(t)

	yaz(t, i, belge{urunID: "prod_bayat", baslik: "Eski"})

	esik, err := i.Now(t.Context())
	require.NoError(t, err)

	yaz(t, i, belge{urunID: "prod_taze", baslik: "Yeni"})

	silinen, err := i.Sweep(t.Context(), esik)
	require.NoError(t, err)
	assert.Equal(t, int64(1), silinen)

	ids, err := i.Search(t.Context(), "eski", 10, 0)
	require.NoError(t, err)
	assert.Empty(t, ids, "eşikten eski satır silinmiş olmalı")

	ids, err = i.Search(t.Context(), "yeni", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"prod_taze"}, ids, "turda tazelenen satır KALMALI")
}

// TestSemaGINIndeksiKurar migration'ın gerçekten bir GIN indeksi ürettiğini
// doğrular.
//
// "CREATE INDEX IF NOT EXISTS", aynı adda başka bir ilişki varsa hata değil
// NOTICE üretip atlar; yani indeks sessizce hiç kurulmamış olabilir. O
// durumda her arama tam tablo taramasına düşer ve bu, katalog büyüyene kadar
// hiçbir testte görünmez (core/link'in verifySchema gerekçesiyle aynı).
func TestSemaGINIndeksiKurar(t *testing.T) {
	var tanim string
	err := testPool.Pool().QueryRow(t.Context(), `
		SELECT indexdef FROM pg_indexes
		WHERE tablename = 'searchpg_product' AND indexname = 'searchpg_product_document_idx'`).
		Scan(&tanim)

	require.NoError(t, err, "belge indeksi kurulmuş olmalı")
	assert.Contains(t, tanim, "USING gin", "belge indeksi GIN olmalı")
}

// TestAramaUcuGercekIndeksleCalisir vitrin ucunu uçtan uca doğrular: gerçek
// indeks kimlikleri verir, katalog kayıtları döner.
func TestAramaUcuGercekIndeksleCalisir(t *testing.T) {
	i := gercekIndeks(t)
	k := newSahteKatalog()
	k.urunEkle("prod_1", "Mavi gomlek", "Pamuklu")
	k.urunEkle("prod_2", "Siyah pantolon", "Kot")
	yaz(t, i,
		belge{urunID: "prod_1", baslik: "Mavi gomlek", metin: "Pamuklu"},
		belge{urunID: "prod_2", baslik: "Siyah pantolon", metin: "Kot"},
	)

	m := testModul(i, k)
	rec := istek(m, http.MethodGet, SearchPath+"?q=gomlek", magazaKimligi())

	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"id":"prod_1"`)
	assert.NotContains(t, rec.Body.String(), `"id":"prod_2"`)
}

// TestYenidenIndekslemeGercekVeritabaniniTazeler tam turun hem yazdığını hem
// bayat kayıtları düşürdüğünü doğrular.
//
// Bu, eklentinin var olan bir kuruluma takıldığında ya da bir olay kaçtığında
// tek onarım yoludur.
func TestYenidenIndekslemeGercekVeritabaniniTazeler(t *testing.T) {
	i := gercekIndeks(t)

	// Artık yayında olmayan (kataloğun döndürmediği) bayat bir kayıt.
	yaz(t, i, belge{urunID: "prod_kaldirilmis", baslik: "Kaldirilmis urun"})

	k := newSahteKatalog()
	graph := &sahteGraph{}
	for n := range 3 {
		id := "prod_" + strconv.Itoa(n)
		graph.ids = append(graph.ids, id)
		k.urunEkle(id, "Urun "+strconv.Itoa(n), "aciklama")
	}

	m := testModul(i, k)
	m.graph = graph

	sonuc, err := m.yenidenIndeksle(t.Context())
	require.NoError(t, err)

	assert.Equal(t, 3, sonuc.Indexed)
	assert.Equal(t, int64(1), sonuc.Removed, "yayından kalkmış ürün süpürülmeli")
	assert.Equal(t, 1, sonuc.Pages)

	ids, err := i.Search(t.Context(), "urun", 10, 0)
	require.NoError(t, err)
	assert.Len(t, ids, 3)

	ids, err = i.Search(t.Context(), "kaldirilmis", 10, 0)
	require.NoError(t, err)
	assert.Empty(t, ids, "bayat kayıt aramada görünmemeli")
}

// TestOlayAkisiGercekIndekseYazar abone -> katalog -> indeks yolunu gerçek
// tabloda doğrular.
func TestOlayAkisiGercekIndekseYazar(t *testing.T) {
	i := gercekIndeks(t)
	k := newSahteKatalog()
	k.urunEkle("prod_1", "Mavi gomlek", "Pamuklu yazlik")
	m := testModul(i, k)

	require.NoError(t, m.urunYazildi(t.Context(), olay(eventProductCreated, "prod_1")))
	ids, err := i.Search(t.Context(), "gomlek", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"prod_1"}, ids)

	require.NoError(t, m.urunSilindi(t.Context(), olay(eventProductDeleted, "prod_1")))
	ids, err = i.Search(t.Context(), "gomlek", 10, 0)
	require.NoError(t, err)
	assert.Empty(t, ids, "silme olayı indeksten düşürmeli")
}

// TestModulRegisterCekirdektenCozer modülün yalnızca çekirdek servisleri
// istediğini ve eksikse açılışı DÜŞÜRDÜĞÜNÜ doğrular.
func TestModulRegisterCekirdektenCozer(t *testing.T) {
	log := slog.New(slog.DiscardHandler)

	t.Run("eksik çekirdek servisi", func(t *testing.T) {
		c := container.New(log)
		t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

		m := newModul(c, log)
		err := m.Register(t.Context(), c)

		require.Error(t, err, "core.db yokken kayıt düşmeli")
		assert.Equal(t, codeSetupFailed, coreerrors.CodeOf(err))
	})

	t.Run("tam kurulum", func(t *testing.T) {
		c := container.New(log)
		t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

		links := link.New(testPool, nil)
		require.NoError(t, c.Provide(svcDB, testPool))
		require.NoError(t, c.Provide(svcQuery, query.New(links, c, nil)))

		m := newModul(c, log)
		require.NoError(t, m.Register(t.Context(), c))

		// Uçlar ancak kayıttan SONRA bağlanır.
		desenler := chiDesenleri(testRouter(m))
		assert.Contains(t, desenler, "GET "+SearchPath)
		assert.Contains(t, desenler, "POST "+ReindexPath)
	})
}

// TestMigrationGeriAlinipYenidenUygulanabilir şemanın up -> down -> up
// döngüsünden geçtiğini doğrular.
//
// internal/arch'ın aynı kapısı yalnızca internal/modules altını tarar; geri
// alınamayan bir migration, golang-migrate'in sürüm defterini "dirty" bırakır
// ve o noktadan sonra sunucu bir daha AÇILMAZ.
//
// Test EN SONA konmuştur: tabloyu düşürüp yeniden kurar, yani kendisinden
// sonra çalışacak testler boş bir tabloyla başlar.
func TestMigrationGeriAlinipYenidenUygulanabilir(t *testing.T) {
	ctx := t.Context()

	require.NoError(t, db.MigrateDown(ctx, testDSN, migrationsRoot, ModuleName, 0),
		"şema geri alınabilmeli")

	var kalan int
	require.NoError(t, testPool.Pool().QueryRow(ctx, `
		SELECT count(*) FROM pg_tables WHERE tablename = 'searchpg_product'`).Scan(&kalan))
	assert.Zero(t, kalan, "geri alma tabloyu düşürmeli")

	require.NoError(t, db.Migrate(ctx, testDSN, migrationsRoot, ModuleName),
		"şema yeniden uygulanabilmeli")

	_, err := newIndeks(testPool.Pool()).Search(ctx, "gomlek", 10, 0)
	assert.NoError(t, err, "yeniden kurulan şema kullanılabilir olmalı")
}
