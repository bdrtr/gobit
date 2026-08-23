//go:build integration

// Bu dosyadaki testler gerçek bir PostgreSQL örneği (dolayısıyla Docker)
// gerektirir; `make test` hızlı kalsın diye `integration` etiketiyle
// ayrılmıştır. Çalıştırmak için: make test-integration
//
// Kardinalitenin VERİTABANI KISITIYLA zorlandığı iddiası ancak burada
// kanıtlanabilir: birim testleri yalnızca doğru DDL'in üretildiğini gösterir,
// PostgreSQL'in o DDL'i beklendiği gibi uyguladığını göstermez.
package link_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/link"
)

const postgresImage = "postgres:16-alpine"

// testPool tüm testlerin paylaştığı havuzdur; testler ayrı link adları
// kullandığı için birbirini etkilemez.
var testPool *db.Pool

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
		fmt.Fprintf(os.Stderr, "bağlantı havuzu açılamadı: %v\n", err)
		return 1
	}
	defer testPool.Close()

	return m.Run()
}

// TestDefineCreatesSchema Define'ın tabloyu, kardinalite indekslerini ve kalıcı
// tanım kaydını oluşturduğunu doğrular.
func TestDefineCreatesSchema(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis()
	def := tanim("define_semasi", link.OneToMany)

	require.NoError(t, svc.Define(ctx, def))

	table := tabloAdi(t, def.Name)
	assert.True(t, tabloVar(ctx, t, table), "link tablosu oluşmalı")
	for _, sutun := range []string{"from_id", "to_id", "created_at"} {
		assert.True(t, sutunVar(ctx, t, table, sutun), "%s sütunu oluşmalı", sutun)
	}

	assert.True(t, indeksVar(ctx, t, table, table+"_pkey"), "çift benzersizliği birincil anahtarla gelir")
	assert.True(t, indeksVar(ctx, t, table, table+"_to_uniq"), "one_to_many to ucunu benzersiz kılar")
	assert.False(t, indeksVar(ctx, t, table, table+"_from_uniq"), "one_to_many from ucunu KISITLAMAZ")

	// Plan Bölüm 2.2: link tablosu hiçbir modülün tablosuna FK vermez.
	assert.Zero(t, fkSayisi(ctx, t, table),
		"link tablosunda foreign key OLAMAZ; cross-module FK yasağı budur")

	assert.Equal(t,
		[]string{"product", "variant_id", "pricing", "price_set_id", "one_to_many"},
		defterSatiri(ctx, t, def.Name),
		"tanım kalıcı deftere yazılmalı")
}

// TestDefineIsIdempotent aynı tanımın her açılışta yeniden bildirilebildiğini
// doğrular; hem aynı servis hem de defteri boş YENİ bir servis üzerinden.
func TestDefineIsIdempotent(t *testing.T) {
	ctx := context.Background()
	def := tanim("define_idempotent", link.ManyToMany)
	svc := yeniServis()

	require.NoError(t, svc.Define(ctx, def))
	require.NoError(t, svc.Define(ctx, def), "aynı servis aynı tanımı yeniden bildirebilmeli")

	// Yeni servisin süreç içi defteri boştur; bu çağrı gerçekten veritabanına
	// gider ve kalıcı defterle karşılaştırma yapar.
	require.NoError(t, yeniServis().Define(ctx, def),
		"yeniden başlatılan bir süreç aynı tanımı bildirebilmeli")

	table := tabloAdi(t, def.Name)
	assert.True(t, tabloVar(ctx, t, table))
	assert.Equal(t, 1, defterSayisi(ctx, t, def.Name), "defterde tek satır kalmalı")
}

// TestDefineRejectsChangedDefinition sürümler arasında değişmiş bir tanımın
// kalıcı defterden yakalandığını doğrular. Süreç içi defter bu durumu göremez;
// yakalayan tek yer veritabanıdır.
func TestDefineRejectsChangedDefinition(t *testing.T) {
	ctx := context.Background()
	def := tanim("define_degisti", link.OneToMany)
	require.NoError(t, yeniServis().Define(ctx, def))

	degismis := def
	degismis.Cardinality = link.ManyToMany

	// Defteri boş YENİ bir servis: çakışma yalnızca veritabanından bilinebilir.
	err := yeniServis().Define(ctx, degismis)

	require.Error(t, err, "aynı adla farklı tanım kabul edilemez")
	assert.True(t, errors.IsConflict(err),
		"hata sınıfı KindConflict olmalı, %v alındı", errors.KindOf(err))
	assert.Equal(t, "link_definition_conflict", errors.CodeOf(err))
	assert.Contains(t, err.Error(), "one_to_many", "mesaj kayıtlı tanımı göstermeli")

	// İşlem geri alındığı için defter ve şema DEĞİŞMEMİŞ olmalı.
	assert.Equal(t, "one_to_many", defterSatiri(ctx, t, def.Name)[4])
	assert.True(t, indeksVar(ctx, t, tabloAdi(t, def.Name), tabloAdi(t, def.Name)+"_to_uniq"),
		"reddedilen bildirim var olan kısıtı kaldırmamalı")

	// Ucu değişen tanım da aynı biçimde reddedilir.
	baskaUc := def
	baskaUc.To.Module = "inventory"
	assert.True(t, errors.IsConflict(yeniServis().Define(ctx, baskaUc)))
}

// TestDefineIsSafeUnderConcurrency aynı anda açılan süreçlerin (burada:
// goroutine'lerin) aynı tanımı yarışmadan bildirebildiğini doğrular. Danışma
// kilidi olmasaydı eşzamanlı "CREATE TABLE IF NOT EXISTS" katalog düzeyinde
// çakışırdı.
func TestDefineIsSafeUnderConcurrency(t *testing.T) {
	ctx := context.Background()
	def := tanim("define_yaris", link.OneToOne)

	const esZamanli = 8
	hatalar := make(chan error, esZamanli)

	var wg sync.WaitGroup
	for range esZamanli {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hatalar <- yeniServis().Define(ctx, def)
		}()
	}
	wg.Wait()
	close(hatalar)

	for err := range hatalar {
		require.NoError(t, err, "eşzamanlı bildirim çakışmadan tamamlanmalı")
	}
	assert.Equal(t, 1, defterSayisi(ctx, t, def.Name))
	assert.True(t, tabloVar(ctx, t, tabloAdi(t, def.Name)))
}

// TestCreateListDeleteEndToEnd bağ kurma, okuma ve silme yolunu uçtan uca
// doğrular; idempotency kararları da burada kanıtlanır.
func TestCreateListDeleteEndToEnd(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis()
	def := tanim("uctan_uca", link.OneToMany)
	require.NoError(t, svc.Define(ctx, def))

	t.Run("bağ kurulur ve okunur", func(t *testing.T) {
		require.NoError(t, svc.Create(ctx, def.Name, "var_1", "ps_2"))
		require.NoError(t, svc.Create(ctx, def.Name, "var_1", "ps_1"))

		ids, err := svc.List(ctx, def.Name, "var_1")
		require.NoError(t, err)
		assert.Equal(t, []string{"ps_1", "ps_2"}, ids, "sonuç artan sıralı olmalı")
	})

	t.Run("aynı çift ikinci kez no-op'tur", func(t *testing.T) {
		// Saga yeniden denemeleri aynı adımı tekrar çalıştırır; bu bir hata
		// değildir (plan Bölüm 2.6).
		require.NoError(t, svc.Create(ctx, def.Name, "var_1", "ps_1"))

		ids, err := svc.List(ctx, def.Name, "var_1")
		require.NoError(t, err)
		assert.Equal(t, []string{"ps_1", "ps_2"}, ids, "tekrar eden bağ satır çoğaltmamalı")
	})

	t.Run("bağı olmayan kayıt boş dilim döner", func(t *testing.T) {
		ids, err := svc.List(ctx, def.Name, "var_yok")
		require.NoError(t, err, "bilinmeyen fromID hata değildir")
		assert.NotNil(t, ids, "boş sonuç nil değil boş dilim olmalı")
		assert.Empty(t, ids)
	})

	t.Run("batch okuma tek sorguda döner", func(t *testing.T) {
		require.NoError(t, svc.Create(ctx, def.Name, "var_2", "ps_3"))

		sonuc, err := svc.ListMany(ctx, def.Name, []string{"var_1", "var_2", "var_yok"})
		require.NoError(t, err)
		assert.Equal(t, map[string][]string{
			"var_1": {"ps_1", "ps_2"},
			"var_2": {"ps_3"},
		}, sonuc, "bağı olmayan fromID için anahtar üretilmemeli")
	})

	t.Run("silme yalnızca hedef çifti kaldırır", func(t *testing.T) {
		require.NoError(t, svc.Delete(ctx, def.Name, "var_1", "ps_1"))

		ids, err := svc.List(ctx, def.Name, "var_1")
		require.NoError(t, err)
		assert.Equal(t, []string{"ps_2"}, ids)
	})

	t.Run("olmayan bağı silmek no-op'tur", func(t *testing.T) {
		// Telafi (compensation) adımı başarısız bir Create'ten sonra da
		// çalışır; "yok" istenen sonucun ta kendisidir.
		require.NoError(t, svc.Delete(ctx, def.Name, "var_1", "ps_1"))
		require.NoError(t, svc.Delete(ctx, def.Name, "hic_yok", "ps_1"))
	})
}

// TestOneToOneIsEnforcedByDatabase OneToOne kardinalitesinin her iki ucu da
// kısıtladığını ve ihlalin tipli bir çakışma olarak döndüğünü doğrular.
func TestOneToOneIsEnforcedByDatabase(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis()
	def := tanim("bire_bir", link.OneToOne)
	require.NoError(t, svc.Define(ctx, def))
	require.NoError(t, svc.Create(ctx, def.Name, "a", "1"))

	t.Run("aynı fromID ikinci hedefe bağlanamaz", func(t *testing.T) {
		cakismaDogrula(t, svc.Create(ctx, def.Name, "a", "2"), "a")
	})

	t.Run("aynı toID ikinci kaynağa bağlanamaz", func(t *testing.T) {
		cakismaDogrula(t, svc.Create(ctx, def.Name, "b", "1"), "1")
	})

	t.Run("aynı çift yine no-op'tur", func(t *testing.T) {
		require.NoError(t, svc.Create(ctx, def.Name, "a", "1"),
			"kardinalite ihlali ile idempotent tekrar karıştırılmamalı")
	})

	assert.Equal(t, 1, satirSayisi(ctx, t, tabloAdi(t, def.Name)))
}

// TestOneToManyIsEnforcedByDatabase OneToMany'de bir fromID'nin çok hedefe
// bağlanabildiğini ama bir toID'nin tek kaynağa ait olduğunu doğrular.
func TestOneToManyIsEnforcedByDatabase(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis()
	def := tanim("bire_cok", link.OneToMany)
	require.NoError(t, svc.Define(ctx, def))

	require.NoError(t, svc.Create(ctx, def.Name, "a", "1"))
	require.NoError(t, svc.Create(ctx, def.Name, "a", "2"), "bir kaynak çok hedefe bağlanabilir")

	cakismaDogrula(t, svc.Create(ctx, def.Name, "b", "1"), "1")

	ids, err := svc.List(ctx, def.Name, "a")
	require.NoError(t, err)
	assert.Equal(t, []string{"1", "2"}, ids)
	assert.Equal(t, 2, satirSayisi(ctx, t, tabloAdi(t, def.Name)))
}

// TestManyToManyAllowsSharedIDs ManyToMany'de yalnızca çiftin benzersiz
// olduğunu ve aynı çiftin ikinci kez eklenmesinin satır çoğaltmadığını
// doğrular.
func TestManyToManyAllowsSharedIDs(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis()
	def := tanim("cok_coka", link.ManyToMany)
	require.NoError(t, svc.Define(ctx, def))

	require.NoError(t, svc.Create(ctx, def.Name, "a", "1"))
	require.NoError(t, svc.Create(ctx, def.Name, "a", "2"))
	require.NoError(t, svc.Create(ctx, def.Name, "b", "1"))
	require.NoError(t, svc.Create(ctx, def.Name, "a", "1"), "aynı çift no-op'tur, hata değil")

	assert.Equal(t, 3, satirSayisi(ctx, t, tabloAdi(t, def.Name)),
		"tekrarlanan çift satır çoğaltmamalı")

	ids, err := svc.List(ctx, def.Name, "a")
	require.NoError(t, err)
	assert.Equal(t, []string{"1", "2"}, ids)
}

// TestListOrderIsDeterministic sıralamanın ekleme sırasından bağımsız ve
// tekrarlanabilir olduğunu doğrular; API yanıtları ve testler buna dayanır.
func TestListOrderIsDeterministic(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis()
	def := tanim("siralama", link.ManyToMany)
	require.NoError(t, svc.Define(ctx, def))

	// Ekleme sırası kasten karışık.
	for _, id := range []string{"ps_30", "ps_10", "ps_20", "ps_02", "ps_01"} {
		require.NoError(t, svc.Create(ctx, def.Name, "var_1", id))
	}
	beklenen := []string{"ps_01", "ps_02", "ps_10", "ps_20", "ps_30"}

	for range 5 {
		ids, err := svc.List(ctx, def.Name, "var_1")
		require.NoError(t, err)
		assert.Equal(t, beklenen, ids)
	}

	sonuc, err := svc.ListMany(ctx, def.Name, []string{"var_1"})
	require.NoError(t, err)
	assert.Equal(t, beklenen, sonuc["var_1"], "batch okuma da aynı sırayı vermeli")
}

// TestCanceledContextIsReportedAsUnavailable iptal edilmiş bir bağlamın
// "veritabanı bozuk" gibi değil, iptal olarak raporlandığını doğrular.
func TestCanceledContextIsReportedAsUnavailable(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis()
	def := tanim("iptal", link.ManyToMany)
	require.NoError(t, svc.Define(ctx, def))

	iptalli, cancel := context.WithCancel(ctx)
	cancel()

	err := svc.Create(iptalli, def.Name, "a", "1")

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable),
		"hata sınıfı KindUnavailable olmalı, %v alındı", errors.KindOf(err))
	assert.Equal(t, "link_canceled", errors.CodeOf(err))
	assert.Zero(t, satirSayisi(ctx, t, tabloAdi(t, def.Name)))
}

// --- yardımcılar ---

// tanim testler için verilen ad ve kardinaliteyle bir tanım üretir.
func tanim(name string, c link.Cardinality) link.LinkDefinition {
	return link.LinkDefinition{
		Name:        name,
		From:        link.LinkSide{Module: "product", Field: "variant_id"},
		To:          link.LinkSide{Module: "pricing", Field: "price_set_id"},
		Cardinality: c,
	}
}

// yeniServis süreç içi defteri BOŞ, yeni bir servis üretir; böylece kalıcı
// defter yolu gerçekten sınanır.
func yeniServis() link.LinkService {
	return link.New(testPool, nil)
}

// tabloAdi link adından tablo adını hata denetimiyle üretir.
func tabloAdi(t *testing.T, name string) string {
	t.Helper()

	table, err := link.TableName(name)
	require.NoError(t, err)
	return table
}

// cakismaDogrula kardinalite ihlalinin tipli ve okunabilir olduğunu doğrular.
func cakismaDogrula(t *testing.T, err error, doluUc string) {
	t.Helper()

	require.Error(t, err, "kardinalite ihlali sessizce geçilemez")
	assert.True(t, errors.IsConflict(err),
		"hata sınıfı KindConflict olmalı, %v alındı", errors.KindOf(err))
	assert.Equal(t, "link_cardinality_violation", errors.CodeOf(err))
	assert.Contains(t, err.Error(), doluUc, "mesaj hangi kimliğin dolu olduğunu yazmalı")
}

// tabloVar public şemada verilen tablonun var olup olmadığını bildirir.
func tabloVar(ctx context.Context, t *testing.T, table string) bool {
	t.Helper()

	var exists bool
	sorgula(ctx, t, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = $1
		)`, []any{table}, &exists)
	return exists
}

// sutunVar verilen tabloda bir sütunun var olup olmadığını bildirir.
func sutunVar(ctx context.Context, t *testing.T, table, column string) bool {
	t.Helper()

	var exists bool
	sorgula(ctx, t, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2
		)`, []any{table, column}, &exists)
	return exists
}

// indeksVar verilen tabloda bir indeksin var olup olmadığını bildirir.
func indeksVar(ctx context.Context, t *testing.T, table, index string) bool {
	t.Helper()

	var exists bool
	sorgula(ctx, t, `
		SELECT EXISTS (
			SELECT 1 FROM pg_indexes
			WHERE schemaname = 'public' AND tablename = $1 AND indexname = $2
		)`, []any{table, index}, &exists)
	return exists
}

// fkSayisi tablodaki foreign key kısıtlarının sayısını döner; her zaman sıfır
// olmalıdır (plan Bölüm 2.2).
func fkSayisi(ctx context.Context, t *testing.T, table string) int {
	t.Helper()

	var count int
	sorgula(ctx, t, `
		SELECT count(*) FROM pg_constraint c
		JOIN pg_class rel ON rel.oid = c.conrelid
		JOIN pg_namespace ns ON ns.oid = rel.relnamespace
		WHERE ns.nspname = 'public' AND rel.relname = $1 AND c.contype = 'f'`,
		[]any{table}, &count)
	return count
}

// satirSayisi link tablosundaki satır sayısını döner.
func satirSayisi(ctx context.Context, t *testing.T, table string) int {
	t.Helper()

	var count int
	// Tablo adı testin kendi ürettiği doğrulanmış addır.
	sorgula(ctx, t, fmt.Sprintf("SELECT count(*) FROM %s", table), nil, &count)
	return count
}

// defterSayisi kalıcı defterdeki satır sayısını döner.
func defterSayisi(ctx context.Context, t *testing.T, name string) int {
	t.Helper()

	var count int
	sorgula(ctx, t, `SELECT count(*) FROM link_definitions WHERE name = $1`, []any{name}, &count)
	return count
}

// defterSatiri kalıcı defterdeki tanımı alan sırasıyla döner.
func defterSatiri(ctx context.Context, t *testing.T, name string) []string {
	t.Helper()

	var fromModule, fromField, toModule, toField, cardinality string
	row := testPool.Pool().QueryRow(ctx, `
		SELECT from_module, from_field, to_module, to_field, cardinality
		FROM link_definitions WHERE name = $1`, name)
	require.NoError(t, row.Scan(&fromModule, &fromField, &toModule, &toField, &cardinality))
	return []string{fromModule, fromField, toModule, toField, cardinality}
}

// sorgula tek değerli bir doğrulama sorgusu çalıştırır.
func sorgula(ctx context.Context, t *testing.T, sql string, args []any, dest any) {
	t.Helper()

	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	require.NoError(t, testPool.Pool().QueryRow(queryCtx, sql, args...).Scan(dest))
}

// TestDefineRejectsIndexNamespaceCollision PostgreSQL'in tablo ve indeksleri
// AYNI ad uzayında (pg_class) tutmasından doğan sessiz bozulmayı kapatan iki
// katmanı da doğrular.
//
// Regresyon: "x_from_uniq" adlı bir link tanımlandığında, "x" linkinin
// benzersizlik indeksi aynı ilişki adına çözülüyordu.
// "CREATE UNIQUE INDEX IF NOT EXISTS" bu durumda hata DEĞİL, NOTICE üretip
// ATLIYOR — yani Define başarı dönüyor, tanım deftere yazılıyor, ama
// kardinalite kısıtı veritabanında HİÇ KURULMUYOR. Bozulma ancak veri
// kirlendikten sonra fark edilirdi.
func TestDefineRejectsIndexNamespaceCollision(t *testing.T) {
	svc := yeniServis()
	ctx := t.Context()

	t.Run("indeks sonekli ad reddedilir", func(t *testing.T) {
		for _, name := range []string{"cakisma_from_uniq", "cakisma_to_uniq", "cakisma_to_lookup"} {
			err := svc.Define(ctx, tanim(name, link.ManyToMany))
			require.Error(t, err, "%q kabul edildi", name)
			assert.True(t, errors.IsInvalid(err), "%q için sınıf Invalid olmalı, gelen: %v", name, err)
		}
	})

	t.Run("dis kaynakli ad cakismasi DDL sonrasi yakalanir", func(t *testing.T) {
		// Link API'sinin dışında, doğrudan veritabanında link_<ad> adında bir
		// INDEKS oluştur. Ad doğrulaması bunu göremez; yakalayan katman
		// verifySchema olmalıdır.
		pool := testPool.Pool()
		_, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS cakisma_tasiyici (id TEXT)`)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS link_disaridan ON cakisma_tasiyici (id)`)
		require.NoError(t, err)

		err = svc.Define(ctx, tanim("disaridan", link.OneToOne))
		require.Error(t, err, "link_disaridan bir INDEKS iken Define başarı dönmemeli")
		assert.Contains(t, err.Error(), "disaridan")
	})
}
