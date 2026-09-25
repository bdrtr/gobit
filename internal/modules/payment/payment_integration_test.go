//go:build integration

// Bu dosyadaki testler gerçek bir PostgreSQL örneği (dolayısıyla Docker)
// gerektirir; `make test` hızlı kalsın diye `integration` etiketiyle
// ayrılmıştır. Çalıştırmak için: make test-integration
//
// Birim testleri sahte bir depo ile servisin KARARLARINI kanıtlar. Buradaki
// testler kararların dayandığı ZEMİNİ kanıtlar: migration'ın VERİ VARKEN geri
// alınabildiğini, kısıtların gerçekten uygulandığını, sağlayıcının durumunun
// süreç dışında yaşadığını ve eşzamanlılık iddiasının veritabanı düzeyinde
// tuttuğunu. Özellikle "eşzamanlı iki Authorize tek yetkilendirme üretir"
// iddiası yalnızca burada, gerçek goroutine'lerle gerçek satır kilitleri
// üzerinde sınanabilir.
package payment_test

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/eventbus/outbox"
	"github.com/bdrtr/gobit/core/link"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/payment"
	"github.com/bdrtr/gobit/internal/modules/payment/loyaltypoints"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/repository"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
	"github.com/bdrtr/gobit/internal/modules/payment/storecredit"
)

const postgresImage = "postgres:16-alpine"

// modulTablolari modülün sahip olduğu tablolardır; migration testleri bu
// listeyi kullanır.
var modulTablolari = []string{
	"payment_collections", "payment_sessions", "payments", "refunds",
	"payment_manual_sessions",
	// Mağaza kredisinin iki tablosu (ADR 0152): defter modülün, oturumlar
	// sağlayıcının.
	"payment_store_credit_entries", "payment_store_credit_sessions",
	// Sadakat puanı defteri (ADR 0164) ve puanı harcayan sağlayıcının kendi
	// oturumları (ADR 0165): kredideki ayrımın aynısı, defter modülün, oturumlar
	// sağlayıcının.
	"payment_loyalty_entries", "payment_loyalty_sessions",
}

// Test verisinde kullanılan sabitler. Referans BAŞKA bir modüle (sepet ya da
// sipariş) aittir; bu modül varlığını doğrulamaz (Prensip 2.2).
const (
	testReference = "cart_TEST"
	testCurrency  = "TRY"
	testAmount    = int64(50_000)
)

var (
	// testPool tüm testlerin paylaştığı havuzdur.
	testPool *db.Pool
	// testDSN migration çağrıları için bağlantı adresidir.
	testDSN string
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

	testDSN, err = ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "bağlantı adresi alınamadı: %v\n", err)
		return 1
	}

	cfg := db.DefaultConfig(testDSN)
	// Eşzamanlılık testleri onlarca goroutine'i aynı anda koşturur; her işlem
	// bir bağlantı tuttuğu için havuz varsayılandan geniş açılır.
	cfg.MaxConns = 24
	testPool, err = db.New(ctx, cfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bağlantı havuzu açılamadı: %v\n", err)
		return 1
	}
	defer testPool.Close()

	if err := db.Migrate(ctx, testDSN, payment.New().Migrations(), payment.ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "migration uygulanamadı: %v\n", err)
		return 1
	}

	// Outbox bir ÇEKİRDEK şeması ve modül ona kendi işleminin içinde yazıyor
	// (ADR 0121), yani düzeneğin onu da uygulaması gerekiyor — bileşim kökünün
	// çekirdek şemalarını modül şemalarından önce uygulaması gibi.
	//
	// Bu satır ödeme modülünün göçlerinin event_outbox'a DOKUNMAMASI gerektiği
	// için var: tabloyu core/eventbus/outbox sahipleniyor ve iki sahip aynı
	// tabloyu ayrı sürümlerden ilerletemez.
	if err := db.Migrate(ctx, testDSN, outbox.Migrations(), outbox.MigrationOwner); err != nil {
		fmt.Fprintf(os.Stderr, "outbox migration'ı uygulanamadı: %v\n", err)
		return 1
	}

	return m.Run()
}

// newService gerçek depo ve GERÇEK manuel sağlayıcı üzerinde çalışan bir
// servis kurar.
func newService(t *testing.T) (*service.Service, *manual.Provider) {
	t.Helper()

	repo := repository.New(testPool.Pool())
	prov := manual.New(repo, nil)
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(prov))

	svc, err := service.New(service.Options{Store: repo, Providers: registry, Events: eventbus.NewInMemory(nil)})
	require.NoError(t, err)
	return svc, prov
}

// sayanSaglayici gerçek sağlayıcıyı sarar ve ÇAĞRI SAYAR.
//
// "Tek yetkilendirme üretilir" iddiası ancak böyle KESİN olarak sınanabilir:
// manuel sağlayıcı kendi içinde idempotent olduğu için, ikinci bir çağrının
// yaptığı işi para tutarına bakarak ayırt etmek mümkün değildir — iki çağrı da
// aynı sonucu yazar. Ölçülmesi gereken şey tutar değil, sağlayıcıya KAÇ KEZ
// GİDİLDİĞİDİR: satır kilidi olmadan birden çok goroutine oturumu "pending"
// görür ve hepsi sağlayıcıya gider.
type sayanSaglayici struct {
	inner *manual.Provider

	mu        sync.Mutex
	authorize int
	capture   int
	cancel    int
}

// Dekoratörün çekirdek sözleşmesini karşıladığı derleme zamanında doğrulanır.
var _ coreprovider.PaymentProvider = (*sayanSaglayici)(nil)

// ID sarılan sağlayıcının kimliğini döner; oturumlar aynı adla açılır.
func (s *sayanSaglayici) ID() string { return s.inner.ID() }

// CreateSession çağrıyı olduğu gibi iletir.
func (s *sayanSaglayici) CreateSession(
	ctx context.Context,
	in coreprovider.CreateSessionInput,
) (coreprovider.Session, error) {
	return s.inner.CreateSession(ctx, in)
}

// Authorize çağrıyı sayar ve iletir.
func (s *sayanSaglayici) Authorize(ctx context.Context, sessionID string) (coreprovider.AuthResult, error) {
	s.mu.Lock()
	s.authorize++
	s.mu.Unlock()
	return s.inner.Authorize(ctx, sessionID)
}

// Capture çağrıyı sayar ve iletir.
func (s *sayanSaglayici) Capture(ctx context.Context, sessionID string, amount int64) error {
	s.mu.Lock()
	s.capture++
	s.mu.Unlock()
	return s.inner.Capture(ctx, sessionID, amount)
}

// Refund çağrıyı olduğu gibi iletir.
func (s *sayanSaglayici) Refund(ctx context.Context, sessionID string, amount int64) error {
	return s.inner.Refund(ctx, sessionID, amount)
}

// Cancel çağrıyı sayar ve iletir.
func (s *sayanSaglayici) Cancel(ctx context.Context, sessionID string) error {
	s.mu.Lock()
	s.cancel++
	s.mu.Unlock()
	return s.inner.Cancel(ctx, sessionID)
}

// sayimlar sağlayıcıya yapılan çağrı sayılarını döner.
func (s *sayanSaglayici) sayimlar() (authorize, capture, cancel int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authorize, s.capture, s.cancel
}

// yeniSayanServis çağrıları sayan bir sağlayıcı üzerinde servis kurar.
func yeniSayanServis(t *testing.T) (*service.Service, *sayanSaglayici) {
	t.Helper()

	repo := repository.New(testPool.Pool())
	sayan := &sayanSaglayici{inner: manual.New(repo, nil)}
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(sayan))

	svc, err := service.New(service.Options{Store: repo, Providers: registry, Events: eventbus.NewInMemory(nil)})
	require.NoError(t, err)
	return svc, sayan
}

// yeniKoleksiyon test için bir ödeme koleksiyonu açar.
func yeniKoleksiyon(ctx context.Context, t *testing.T, svc *service.Service) models.PaymentCollection {
	t.Helper()

	col, err := svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
		Reference:    testReference,
		Amount:       testAmount,
		CurrencyCode: testCurrency,
	})
	require.NoError(t, err)
	return col
}

// isolatedDatabase creates a database of the test's own, applies the module's
// and the outbox's migrations to it, and returns its address and a pool on it.
//
// A test that drops the schema has to run here: in the shared database it
// would rewind the rows of every test before it and depend on which ones had
// run (D135).
func isolatedDatabase(ctx context.Context, t *testing.T, prefix string) (string, *db.Pool) {
	t.Helper()

	name := prefix + "_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	_, err := testPool.Pool().Exec(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err)

	parsed, err := url.Parse(testDSN)
	require.NoError(t, err)
	parsed.Path = "/" + name
	dsn := parsed.String()

	require.NoError(t, db.Migrate(ctx, dsn, payment.New().Migrations(), payment.ModuleName))
	require.NoError(t, db.Migrate(ctx, dsn, outbox.Migrations(), outbox.MigrationOwner))
	pool, err := db.New(ctx, db.DefaultConfig(dsn), nil)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return dsn, pool
}

// serviceOnPool is newService on another database's pool.
func serviceOnPool(t *testing.T, pool *db.Pool) *service.Service {
	t.Helper()

	repo := repository.New(pool.Pool())
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(manual.New(repo, nil)))
	svc, err := service.New(service.Options{Store: repo, Providers: registry, Events: eventbus.NewInMemory(nil)})
	require.NoError(t, err)

	return svc
}

// tableExistsIn reports whether the table exists in the pool's database.
func tableExistsIn(ctx context.Context, t *testing.T, pool *db.Pool, table string) bool {
	t.Helper()

	var exists bool
	require.NoError(t, pool.Pool().QueryRow(ctx,
		`SELECT EXISTS (
             SELECT 1 FROM pg_class c
             JOIN pg_namespace n ON n.oid = c.relnamespace
             WHERE c.relname = $1 AND c.relkind = 'r' AND n.nspname = current_schema()
         )`, table).Scan(&exists))
	return exists
}

// TestMigrationVeriVarkenGeriAlinabilir migration'ın DOLU bir şemada
// uygulanıp geri alınabildiğini doğrular.
//
// internal/arch'taki kapı yalnızca BOŞ bir veritabanında up -> down -> up
// koşar ve veriye bağlı geri alma hatalarını yakalayamaz — Faz 5'te tam o
// açıktan bir hata geçmişti. Buradaki test önce koleksiyon, oturum, tahsilat,
// iade ve sağlayıcı oturumundan oluşan TAM grafiği yazar; foreign key sırasını
// yanlış kuran bir down dosyası ancak böyle düşer.
func TestMigrationVeriVarkenGeriAlinabilir(t *testing.T) {
	ctx := context.Background()
	src := payment.New().Migrations()
	// The test drops and re-creates the module's schema, so it runs in a
	// database of its own (D135). In the shared one it rewound every row the
	// tests before it wrote, and 000006's down refuses a point ledger holding a
	// spend row: it passed only while it ran before every test that spends
	// points, which file order happened to arrange.
	dsn, pool := isolatedDatabase(ctx, t, "payment_migration")
	svc := serviceOnPool(t, pool)
	tabloVar := func(ctx context.Context, t *testing.T, table string) bool {
		t.Helper()
		return tableExistsIn(ctx, t, pool, table)
	}

	col := yeniKoleksiyon(ctx, t, svc)
	ses, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: "migration-key",
	})
	require.NoError(t, err)
	_, err = svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)
	pay, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)
	_, err = svc.RefundPayment(ctx, pay.ID, 1_000, "migration testi")
	require.NoError(t, err)

	for _, table := range modulTablolari {
		require.True(t, tabloVar(ctx, t, table), "%s başlangıçta var olmalı", table)
	}

	require.NoError(t, db.MigrateDown(ctx, dsn, src, payment.ModuleName, 0),
		"down başarısız — bu, modülün bir daha migrate EDİLEMEMESİ demektir")
	for _, table := range modulTablolari {
		assert.False(t, tabloVar(ctx, t, table), "%s geri alma sonrası kalmamalı", table)
	}

	require.NoError(t, db.Migrate(ctx, dsn, src, payment.ModuleName))
	for _, table := range modulTablolari {
		assert.True(t, tabloVar(ctx, t, table), "%s yeniden uygulanmalı", table)
	}

	version, dirty, err := db.Version(ctx, dsn, payment.ModuleName)
	require.NoError(t, err)
	assert.False(t, dirty, "yarıda kalmış migration olmamalı")
	assert.Equal(t, enYuksekSurum(t, src), version,
		"yeniden uygulama TÜM migration'ları koşturmalı, en son olanı değil")
}

// enYuksekSurum gömülü migration kümesindeki en büyük sürüm numarasını döner.
//
// Sayı SABİT YAZILMAZ: sabit yazıldığında test, modüle her migration
// eklendiğinde kırılır ve kıran şey bir hata değil, testin kendi eskimiş
// beklentisidir. Kümeden okununca sınanan şey de doğrusu oluyor — "geri
// alındıktan sonra HEPSİ yeniden uygulandı" — yalnızca "sayı bir".
func enYuksekSurum(t *testing.T, src fs.FS) uint {
	t.Helper()

	entries, err := fs.ReadDir(src, ".")
	require.NoError(t, err)

	var en uint
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}

		digits := name[:strings.IndexByte(name, '_')]
		n, convErr := strconv.ParseUint(digits, 10, 32)
		require.NoError(t, convErr, "%s bir sürüm numarasıyla başlamıyor", name)

		if uint(n) > en {
			en = uint(n)
		}
	}

	require.Positive(t, en, "gömülü migration kümesi boş görünüyor")
	return en
}

// TestCrossModuleForeignKeyYok modülün tablolarındaki TÜM foreign key'lerin
// yine modülün kendi tablolarına gittiğini doğrular (Prensip 2.2).
//
// Özellikle payment_collections.reference bir sepet ya da sipariş kimliğidir
// ve foreign key OLAMAZ; bu test o kuralın şemada gerçekten tutulduğunu
// gösterir.
func TestCrossModuleForeignKeyYok(t *testing.T) {
	ctx := context.Background()

	rows, err := testPool.Pool().Query(ctx,
		`SELECT c.conname, src.relname, tgt.relname
         FROM pg_constraint c
         JOIN pg_class src ON src.oid = c.conrelid
         JOIN pg_class tgt ON tgt.oid = c.confrelid
         WHERE c.contype = 'f' AND src.relname = ANY($1)`, modulTablolari)
	require.NoError(t, err)
	defer rows.Close()

	sahipli := make(map[string]struct{}, len(modulTablolari))
	for _, table := range modulTablolari {
		sahipli[table] = struct{}{}
	}

	var sayi int
	for rows.Next() {
		var name, src, tgt string
		require.NoError(t, rows.Scan(&name, &src, &tgt))
		assert.Contains(t, sahipli, tgt,
			"%s kısıtı modül dışına referans veriyor (%s -> %s)", name, src, tgt)
		sayi++
	}
	require.NoError(t, rows.Err())
	assert.Positive(t, sayi, "modül içi foreign key'ler kullanılmalı")
}

// TestUctanUcaOdemeAkisi Faz 6'nın istediği tam akışı GERÇEK sağlayıcıyla
// yürütür: CreateSession -> Authorize -> Capture -> Refund.
//
// Her adımda hem modülün kaydı hem SAĞLAYICININ defteri denetlenir; ikisinin
// ayrıştığı bir hata ancak iki tarafa birden bakılarak görülür.
func TestUctanUcaOdemeAkisi(t *testing.T) {
	ctx := context.Background()
	svc, prov := newService(t)
	col := yeniKoleksiyon(ctx, t, svc)

	ses, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: "e2e-" + col.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, models.SessionPending, ses.Status)

	guncelKol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionAwaiting, guncelKol.Status)

	authorized, err := svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionAuthorized, authorized.Status)
	assert.Equal(t, testAmount, authorized.AuthorizedAmount)

	saglayiciOturum, err := prov.GetSession(ctx, ses.ExternalID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionAuthorized, saglayiciOturum.Status,
		"sağlayıcının defteri de yetkilendirilmiş olmalı")

	guncelKol, err = svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionAuthorized, guncelKol.Status)
	assert.Equal(t, testAmount, guncelKol.AuthorizedAmount)

	pay, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)
	assert.Equal(t, testAmount, pay.Amount)
	assert.Equal(t, testCurrency, pay.CurrencyCode)

	saglayiciOturum, err = prov.GetSession(ctx, ses.ExternalID)
	require.NoError(t, err)
	assert.Equal(t, testAmount, saglayiciOturum.CapturedAmount)

	guncelKol, err = svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionCaptured, guncelKol.Status)

	refund, err := svc.RefundPayment(ctx, pay.ID, testAmount/2, "kısmi iade")
	require.NoError(t, err)
	assert.Equal(t, testAmount/2, refund.Amount)

	guncelKol, err = svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionPartiallyRefunded, guncelKol.Status)

	_, err = svc.RefundPayment(ctx, pay.ID, 0, "kalan iade")
	require.NoError(t, err)

	guncelKol, err = svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionRefunded, guncelKol.Status)
	assert.Equal(t, testAmount, guncelKol.RefundedAmount)

	saglayiciOturum, err = prov.GetSession(ctx, ses.ExternalID)
	require.NoError(t, err)
	assert.Equal(t, testAmount, saglayiciOturum.RefundedAmount,
		"iade sağlayıcının defterine de yansımalı")
}

// TestEszamanliIkiAuthorizeTekYetkilendirmeUretir eşzamanlılık iddiasını
// gerçek satır kilitleri üzerinde sınar.
//
// İki goroutine aynı oturumu aynı anda yetkilendirmeye çalışır. Koleksiyon
// satırının kilidi ikisini seri hâle getirir; ikinci çağrı birincinin yazdığı
// durumu görür ve no-op'a düşer. Kilit alınmasaydı ikisi de "pending" okur,
// ikisi de sağlayıcıya gider ve koleksiyonun bloke tutarı İKİ KAT olurdu —
// aşağıdaki tutar iddiası tam olarak bunu yakalar.
func TestEszamanliIkiAuthorizeTekYetkilendirmeUretir(t *testing.T) {
	ctx := context.Background()
	svc, sayan := yeniSayanServis(t)
	col := yeniKoleksiyon(ctx, t, svc)
	ses, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: "concurrent-auth-" + col.ID,
	})
	require.NoError(t, err)

	const goroutines = 8
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		hatalar  []error
		basarili int
	)
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			_, authErr := svc.AuthorizePayment(ctx, ses.ID)

			mu.Lock()
			defer mu.Unlock()
			if authErr != nil {
				hatalar = append(hatalar, authErr)
				return
			}
			basarili++
		}()
	}
	wg.Wait()

	assert.Empty(t, hatalar, "tüm çağrılar başarılı olmalı (biri yetkilendirir, kalanı no-op)")
	assert.Equal(t, goroutines, basarili)

	authorizeCagrilari, _, _ := sayan.sayimlar()
	assert.Equal(t, 1, authorizeCagrilari,
		"SAĞLAYICIYA yalnızca bir kez gidilmeli; kalan çağrılar no-op'a düşmeli")

	guncelKol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, testAmount, guncelKol.AuthorizedAmount,
		"bloke tutar TEK yetkilendirme kadar olmalı, katları değil")
	assert.Equal(t, models.CollectionAuthorized, guncelKol.Status)
}

// TestEszamanliIkiCreateSessionTekOturumUretir idempotency anahtarının
// eşzamanlı çağrılar altında da tuttuğunu doğrular.
//
// "Önce oku, yoksa yaz" iki adımı arasına giren bir çağrı, koleksiyon kilidi
// olmasaydı ikinci bir oturum açardı; benzersiz indeks son savunmadır ama
// önce kilidin çalıştığı burada görülür.
func TestEszamanliIkiCreateSessionTekOturumUretir(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	col := yeniKoleksiyon(ctx, t, svc)
	anahtar := "concurrent-create-" + col.ID

	const goroutines = 8
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		kimlikler = map[string]int{}
		hatalar   []error
	)
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			ses, createErr := svc.CreateSession(ctx, col.ID, manual.ID,
				service.CreateSessionInput{IdempotencyKey: anahtar})

			mu.Lock()
			defer mu.Unlock()
			if createErr != nil {
				hatalar = append(hatalar, createErr)
				return
			}
			kimlikler[ses.ID]++
		}()
	}
	wg.Wait()

	assert.Empty(t, hatalar, "aynı anahtarla eşzamanlı çağrılar hata vermemeli")
	assert.Len(t, kimlikler, 1, "tüm çağrılar AYNI oturumu dönmeli")

	oturumlar, err := svc.ListPaymentSessions(ctx, col.ID)
	require.NoError(t, err)
	assert.Len(t, oturumlar, 1, "veritabanında tek oturum satırı olmalı")
}

// TestCancelIdempotencyGercekVeritabaninda saga telafisinin gerçek satırlar
// üzerinde de idempotent olduğunu doğrular.
//
// İkinci çağrının hata vermemesi yetmez: koleksiyonun bloke tutarına İKİNCİ
// KEZ dokunulmadığı da kanıtlanır. Dokunulsaydı tutar negatife düşer ve
// CHECK kısıtı işlemi patlatırdı — yani sessiz bir hata değil, üretimde
// telafiyi tamamen kilitleyen bir hata olurdu.
func TestCancelIdempotencyGercekVeritabaninda(t *testing.T) {
	ctx := context.Background()
	svc, prov := newService(t)
	col := yeniKoleksiyon(ctx, t, svc)
	ses, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: "cancel-" + col.ID,
	})
	require.NoError(t, err)
	_, err = svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)

	require.NoError(t, svc.CancelPayment(ctx, ses.ID))
	require.NoError(t, svc.CancelPayment(ctx, ses.ID), "ikinci telafi hata VERMEMELİ")
	require.NoError(t, svc.CancelPayment(ctx, ses.ID), "üçüncü telafi de hata vermemeli")

	guncelOturum, err := svc.GetPaymentSession(ctx, ses.ID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionCanceled, guncelOturum.Status)

	guncelKol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Zero(t, guncelKol.AuthorizedAmount)
	assert.Equal(t, models.CollectionCanceled, guncelKol.Status)

	saglayiciOturum, err := prov.GetSession(ctx, ses.ExternalID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionCanceled, saglayiciOturum.Status)
	assert.Zero(t, saglayiciOturum.AuthorizedAmount)
}

// TestEszamanliIkiCancelTekTelafiUretir telafinin yarış altında da tek kez
// uygulandığını doğrular.
func TestEszamanliIkiCancelTekTelafiUretir(t *testing.T) {
	ctx := context.Background()
	svc, sayan := yeniSayanServis(t)
	col := yeniKoleksiyon(ctx, t, svc)
	ses, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: "concurrent-cancel-" + col.ID,
	})
	require.NoError(t, err)
	_, err = svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)

	const goroutines = 8
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		hatalar []error
	)
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			if cancelErr := svc.CancelPayment(ctx, ses.ID); cancelErr != nil {
				mu.Lock()
				hatalar = append(hatalar, cancelErr)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	assert.Empty(t, hatalar, "eşzamanlı telafiler hata vermemeli")

	_, _, cancelCagrilari := sayan.sayimlar()
	assert.Equal(t, 1, cancelCagrilari, "SAĞLAYICIYA yalnızca bir kez gidilmeli")

	guncelKol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Zero(t, guncelKol.AuthorizedAmount, "blokaj yalnızca BİR KEZ geri alınmalı")
}

// TestRedliAkisTelafiyeAcik saga'nın ödeme adımı patladığında telafinin
// çalıştığını uçtan uca doğrular.
//
// Faz 6'nın DoD'si bunu şart koşar. Ret, oturumun Data alanına yazılan
// davranış anahtarıyla ENJEKTE edilir ve anahtar oturumla birlikte
// saklandığı için yetkilendirme AYRI bir istekte de aynı biçimde davranır.
func TestRedliAkisTelafiyeAcik(t *testing.T) {
	ctx := context.Background()
	svc, prov := newService(t)
	col := yeniKoleksiyon(ctx, t, svc)

	ses, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: "declined-" + col.ID,
		Data: map[string]any{
			manual.DataKeyOutcome:       manual.OutcomeDecline,
			manual.DataKeyDeclineReason: "test reddi",
		},
	})
	require.NoError(t, err)

	_, err = svc.AuthorizePayment(ctx, ses.ID)
	require.Error(t, err, "ödeme adımı PATLAMALI ki saga telafiye geçsin")
	assert.True(t, errors.HasKind(err, errors.KindConflict), "hata: %v", err)
	assert.Equal(t, service.CodeAuthorizationDeclined, errors.CodeOf(err))

	reddedilen, err := svc.GetPaymentSession(ctx, ses.ID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionFailed, reddedilen.Status, "ret KALICI yazılmalı")
	assert.Equal(t, "test reddi", reddedilen.DeclineReason)

	// Telafi: oturumu açan adımın geri alınması.
	require.NoError(t, svc.CancelPayment(ctx, ses.ID))
	require.NoError(t, svc.CancelPayment(ctx, ses.ID), "telafi tekrar çalıştırılabilmeli")

	kapanan, err := svc.GetPaymentSession(ctx, ses.ID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionCanceled, kapanan.Status)
	assert.Equal(t, "test reddi", kapanan.DeclineReason, "ret sebebi korunmalı")

	saglayiciOturum, err := prov.GetSession(ctx, ses.ExternalID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionCanceled, saglayiciOturum.Status)
}

// TestSaglayiciHatasiEnjeksiyonuIslemiGeriAlir sağlayıcıya ulaşılamadığında
// hiçbir şeyin yazılmadığını doğrular.
//
// Ret ile hata arasındaki fark burada görünür: hata YENİDEN DENENEBİLİR olmak
// zorundadır, bu yüzden oturum "pending" kalmalı ve aynı istek tekrar
// edilebilmelidir.
func TestSaglayiciHatasiEnjeksiyonuIslemiGeriAlir(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	col := yeniKoleksiyon(ctx, t, svc)

	ses, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: "provider-error-" + col.ID,
		Data:           map[string]any{manual.DataKeyOutcome: manual.OutcomeError},
	})
	require.NoError(t, err)

	_, err = svc.AuthorizePayment(ctx, ses.ID)
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable), "hata: %v", err)

	guncelOturum, err := svc.GetPaymentSession(ctx, ses.ID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionPending, guncelOturum.Status, "durum değişmemeli")

	guncelKol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Zero(t, guncelKol.AuthorizedAmount)
	assert.Equal(t, models.CollectionAwaiting, guncelKol.Status)
}

// TestSaglayiciDurumuSurecDisindaYasar manuel sağlayıcının durumunun BELLEKTE
// DEĞİL veritabanında tutulduğunu doğrular.
//
// Yeni bir sağlayıcı örneği kurmak, sürecin yeniden başlamasının taklididir:
// bellekte tutulan bir defter bu noktada sıfırlanmış olurdu ve oturum
// "bulunamadı" derdi. e2e akışları ve Faz 9 yük testi süreç yeniden
// başladığında açılmış bir oturumu bulabilmelidir; saga telafisi de tam o
// senaryoda çalışmak zorundadır.
func TestSaglayiciDurumuSurecDisindaYasar(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	col := yeniKoleksiyon(ctx, t, svc)
	ses, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: "restart-" + col.ID,
	})
	require.NoError(t, err)

	// "Süreç yeniden başladı": tamamen YENİ bir sağlayıcı ve servis örneği.
	yenidenSvc, yenidenProv := newService(t)

	saglayiciOturum, err := yenidenProv.GetSession(ctx, ses.ExternalID)
	require.NoError(t, err, "sağlayıcı oturumu yeniden başlatmadan sonra da bulunmalı")
	assert.Equal(t, models.SessionPending, saglayiciOturum.Status)

	authorized, err := yenidenSvc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err, "yeniden başlatma sonrası yetkilendirme çalışmalı")
	assert.Equal(t, models.SessionAuthorized, authorized.Status)

	require.NoError(t, yenidenSvc.CancelPayment(ctx, ses.ID),
		"telafi yeniden başlatma sonrası da çalışmalı")
}

// TestAyniAnahtarSaglayiciDefterindeDeTekOturumAcar sağlayıcının kendi
// idempotency kısıtının gerçekten uygulandığını doğrular.
//
// Modülün kaydı silinse bile sağlayıcı aynı anahtarla ikinci bir oturum
// açmamalıdır; kısıt son savunmadır ve doğrudan sağlayıcıya gidilerek sınanır.
func TestAyniAnahtarSaglayiciDefterindeDeTekOturumAcar(t *testing.T) {
	ctx := context.Background()
	svc, prov := newService(t)
	col := yeniKoleksiyon(ctx, t, svc)
	anahtar := "provider-idem-" + col.ID

	ses, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: anahtar,
	})
	require.NoError(t, err)

	var sayi int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT COUNT(*) FROM payment_manual_sessions WHERE idempotency_key = $1`,
		anahtar).Scan(&sayi))
	assert.Equal(t, int64(1), sayi)

	saglayiciOturum, err := prov.GetSession(ctx, ses.ExternalID)
	require.NoError(t, err)
	assert.Equal(t, anahtar, saglayiciOturum.IdempotencyKey)
	assert.Equal(t, col.ID, saglayiciOturum.Reference,
		"sağlayıcı mutabakat için koleksiyon kimliğini saklamalı")
}

// TestVeritabaniKisitlariSonSavunmadir servis atlansa bile şemanın parayı
// koruduğunu doğrular.
//
// Kısıtlar servis katmanının kopyası değildir; DOĞRUDAN SQL ile yapılan bir
// müdahalenin de negatif tutar yazamamasını, tanımsız durum koyamamasını ve
// olmayan parayı iade edememesini sağlarlar.
func TestVeritabaniKisitlariSonSavunmadir(t *testing.T) {
	ctx := context.Background()

	tests := map[string]string{
		"negatif tutar": `INSERT INTO payment_collections (id, reference, amount, currency_code)
                          VALUES ('paycol_neg', 'cart_x', -1, 'TRY')`,
		"sifir tutar": `INSERT INTO payment_collections (id, reference, amount, currency_code)
                        VALUES ('paycol_zero', 'cart_x', 0, 'TRY')`,
		"gecersiz para birimi": `INSERT INTO payment_collections (id, reference, amount, currency_code)
                                 VALUES ('paycol_cur', 'cart_x', 100, 'try')`,
		"taninmayan durum": `INSERT INTO payment_collections (id, reference, amount, currency_code, status)
                             VALUES ('paycol_st', 'cart_x', 100, 'TRY', 'paid')`,
		"tahsilattan fazla iade": `INSERT INTO payment_collections
                                   (id, reference, amount, currency_code, captured_amount, refunded_amount)
                                   VALUES ('paycol_ref', 'cart_x', 100, 'TRY', 10, 20)`,
		// Koleksiyon toplanacak paranın TAVANIDIR: aşan bir bloke ya da tahsilat,
		// müşteriden siparişten fazlasının alınması demektir. Servis bunu zaten
		// reddeder; kısıt doğrudan SQL ile yapılan müdahaleyi de durdurur.
		"koleksiyondan fazla bloke": `INSERT INTO payment_collections
                                      (id, reference, amount, currency_code, authorized_amount)
                                      VALUES ('paycol_auth', 'cart_x', 100, 'TRY', 101)`,
		"koleksiyondan fazla tahsilat": `INSERT INTO payment_collections
                                         (id, reference, amount, currency_code, captured_amount)
                                         VALUES ('paycol_cap', 'cart_x', 100, 'TRY', 101)`,
	}

	for ad, sorgu := range tests {
		t.Run(ad, func(t *testing.T) {
			_, err := testPool.Pool().Exec(ctx, sorgu)
			require.Error(t, err, "kısıt uygulanmalı")
		})
	}
}

// TestKismiTahsilatDurumuSemadaTanimli türetilen yeni durumun status CHECK
// listesinde bulunduğunu doğrular.
//
// Durum sütunu bir beyaz liste ile korunur: listeye yazılmayan bir değer,
// servis onu türettiği anda işlemi patlatır ve hata ancak KISMİ tahsilat
// yapılan bir üretim akışında görünürdü.
func TestKismiTahsilatDurumuSemadaTanimli(t *testing.T) {
	ctx := context.Background()

	_, err := testPool.Pool().Exec(ctx,
		`INSERT INTO payment_collections (id, reference, amount, currency_code, status, captured_amount)
         VALUES ('paycol_partial', 'cart_x', 100, 'TRY', $1, 1)`,
		models.CollectionPartiallyCaptured.String())
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx, `DELETE FROM payment_collections WHERE id = 'paycol_partial'`)
	require.NoError(t, err)
}

// TestModulContainerdaAdlariKaydeder modülün yayımladığı yüzeylerin
// container'dan ADLA çözülebildiğini doğrular.
//
// Bu, ADR 0001/0004/0006'nın çalışma zamanı karşılığıdır: tüketiciler bu
// modülü import ETMEDEN, yalnızca adla erişir. Bir adın yanlış yazılması ya da
// bir kaydın unutulması derleme zamanında değil, ancak burada görünür.
func TestModulContainerdaAdlariKaydeder(t *testing.T) {
	ctx := context.Background()
	c := container.New(nil)
	require.NoError(t, c.Provide("core.db", testPool))
	// Link servisi de veriliyor ve bu isteğe bağlı değil: modül artık
	// "order_payment" tanımını açılışta bildiriyor (ADR 0005), yani onsuz
	// kaydolamaz. Ürün modülü de aynı gereksinimi taşıyor.
	require.NoError(t, c.Provide("core.link", link.New(testPool, slog.New(slog.DiscardHandler))))
	// Olay otobüsü de zorunlu (ADR 0121): modül para hareketlerini yayımlıyor
	// ve kaybolan bir para olayının telafisi yok. Ayrı bir testte reddin
	// kendisi doğrulanıyor.
	require.NoError(t, c.Provide("core.eventbus", eventbus.NewInMemory(nil)))

	mod := payment.New()
	require.NoError(t, mod.Register(ctx, c))

	svc, err := container.Resolve[*service.Service](c, payment.ServiceName)
	require.NoError(t, err)
	assert.NotNil(t, svc)

	iop, err := container.Resolve[*service.Interop](c, payment.InteropName)
	require.NoError(t, err)
	assert.NotNil(t, iop)

	registry, err := container.Resolve[*service.ProviderRegistry](c, payment.ProvidersName)
	require.NoError(t, err)
	assert.Equal(t, []string{manual.ID}, registry.IDs(),
		"varsayılan sağlayıcı Register sırasında kaydedilmeli")

	provider, err := container.Resolve[query.Provider](c, payment.ProviderName)
	require.NoError(t, err)
	assert.Equal(t, service.EntityName, provider.Entity())
}

// TestModulOtobussuzKaydolmaz olay otobüsünün ZORUNLU olduğunu doğrular.
//
// İsteğe bağlı olsaydı, otobüssüz bir kurulum sağlıklı görünür ve hiçbir şey
// söylemezdi: tahsilatlar çalışır, iadeler çalışır, siparişin kaydı hiç
// öğrenmez. Kaybolan bir para olayının telafisi yok — bu yüzden hata açılışta
// verilir, ilk para hareketinde değil.
func TestModulOtobussuzKaydolmaz(t *testing.T) {
	ctx := context.Background()
	c := container.New(nil)
	require.NoError(t, c.Provide("core.db", testPool))
	require.NoError(t, c.Provide("core.link", link.New(testPool, slog.New(slog.DiscardHandler))))

	err := payment.New().Register(ctx, c)

	require.Error(t, err, "otobüssüz kurulum AÇILIŞTA durmalı")
	assert.Contains(t, err.Error(), "core.eventbus",
		"hata, eksik olan servisi ADIYLA söylemeli; operatörün düzeltmesi gereken şey o")
}

// TestInteropUctanUcaAkisGercekVeritabaninda saga'nın kullanacağı İLKEL
// yüzeyin gerçek veritabanı üzerinde çalıştığını doğrular.
//
// Yüzeyin İMZASI artık derleme zamanında denetleniyor (ADR 0136): internal/arch
// içindeki pin dosyası bu tipi tüketicisinin bildirdiği arayüze atıyor, yani
// eksilen bir metot yapıyı düşürür.
//
// Bu testin kanıtladığı şey o değil ve olmadı da: ilkel yüzeyin GERÇEK
// bağımlılıklarla koştuğunu, yani SQL'in, işlemin ve dönen değerlerin doğru
// olduğunu gösteriyor. İmza denetlenebilir, davranış denetlenemez.
func TestInteropUctanUcaAkisGercekVeritabaninda(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	iop := service.NewInterop(svc)

	colID, err := iop.CreateCollection(ctx, testReference, "", testCurrency, testAmount)
	require.NoError(t, err)

	sesID, err := iop.OpenSession(ctx, colID, manual.ID, "interop-"+colID)
	require.NoError(t, err)

	durum, bloke, err := iop.Authorize(ctx, sesID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionAuthorized.String(), durum)
	assert.Equal(t, testAmount, bloke, "yüzey bloke edilen TUTARI da taşımalı")

	payID, err := iop.Capture(ctx, sesID, 0)
	require.NoError(t, err)

	kolDurum, kolTutar, _, kolTahsil, _, err := iop.Collection(ctx, colID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionCaptured.String(), kolDurum)
	assert.Equal(t, testAmount, kolTutar)
	assert.Equal(t, testAmount, kolTahsil, "saga ödemenin TAM olduğunu sayıdan doğrulayabilmeli")

	refundID, err := iop.Refund(ctx, payID, 0, "interop iadesi")
	require.NoError(t, err)
	assert.NotEmpty(t, refundID)

	kolDurum, _, _, _, kolIade, err := iop.Collection(ctx, colID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionRefunded.String(), kolDurum)
	assert.Equal(t, testAmount, kolIade)
}

// TestInteropEksikOdemeGercekVeritabaninda saga'nın ödemenin EKSİK olduğunu
// ilkel yüzeyden görebildiğini gerçek veritabanı ve GERÇEK sağlayıcı üzerinde
// doğrular.
//
// Faz 6'nın ödeme atlatması tam buradaydı: sağlayıcı kısmi yetkilendirdiğinde
// durum yine "authorized", kısmi tahsilatta koleksiyon yine "captured"
// görünüyordu ve saga'nın bakacağı hiçbir sayı yoktu.
func TestInteropEksikOdemeGercekVeritabaninda(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	iop := service.NewInterop(svc)

	colID, err := iop.CreateCollection(ctx, testReference, "", testCurrency, testAmount)
	require.NoError(t, err)
	sesID, err := iop.OpenSessionWithData(ctx, colID, manual.ID, "interop-partial-"+colID,
		[]byte(`{"manual_authorized_amount":1}`))
	require.NoError(t, err)

	durum, bloke, err := iop.Authorize(ctx, sesID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionAuthorized.String(), durum)
	assert.Equal(t, int64(1), bloke, "sağlayıcı yalnızca 1 birim bloke etti")

	_, err = iop.Capture(ctx, sesID, 0)
	require.NoError(t, err)

	kolDurum, kolTutar, kolBloke, kolTahsil, _, err := iop.Collection(ctx, colID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionPartiallyCaptured.String(), kolDurum)
	assert.Equal(t, testAmount, kolTutar)
	assert.Zero(t, kolBloke, "çekilmeyen blokaj asılı kalmamalı")
	assert.Equal(t, int64(1), kolTahsil)
	assert.Less(t, kolTahsil, kolTutar, "saga bu karşılaştırmayla siparişi onaylamamalı")
}

// TestAyniKoleksiyondaIkiTamOturumAcilamazGercekVeritabaninda ÇİFT TAHSİLATIN
// kapısının gerçek sorgularla da kapalı olduğunu doğrular.
//
// Bulgunun senaryosu buydu: hiçbiri yetkilendirilmemişken açılan iki TAM
// tutarlı oturum, ikisi de yetkilendirilip tahsil edilince koleksiyonun iki
// katını çekiyordu. Kalan tutarın hesabı açık oturumları saymak zorundadır ve
// bu ancak gerçek toplama sorgusuyla kanıtlanır.
func TestAyniKoleksiyondaIkiTamOturumAcilamazGercekVeritabaninda(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	col := yeniKoleksiyon(ctx, t, svc)

	_, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: "double-1-" + col.ID,
	})
	require.NoError(t, err)

	_, err = svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: "double-2-" + col.ID,
	})

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "hata: %v", err)
	assert.Equal(t, service.CodeCollectionClosed, errors.CodeOf(err))
}

// TestKismiTahsilatGercekVeritabaninda kısmi tahsilatın iki deftere de aynı
// biçimde yazıldığını doğrular.
//
// Çekilmeyen blokaj serbest bırakılmazsa oturum "captured" olduğu için bir
// daha iptal edilemez ve tutar sonsuza kadar asılı kalır; sağlayıcının defteri
// ile modülün kaydının ayrışması da ancak iki tarafa birden bakılarak görülür.
func TestKismiTahsilatGercekVeritabaninda(t *testing.T) {
	ctx := context.Background()
	svc, prov := newService(t)
	col := yeniKoleksiyon(ctx, t, svc)
	ses, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: "partial-capture-" + col.ID,
	})
	require.NoError(t, err)
	_, err = svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)

	_, err = svc.CapturePayment(ctx, ses.ID, 1)
	require.NoError(t, err)

	guncelKol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Zero(t, guncelKol.AuthorizedAmount, "çekilmeyen blokaj koleksiyonda KALMAMALI")
	assert.Equal(t, int64(1), guncelKol.CapturedAmount)
	assert.Equal(t, models.CollectionPartiallyCaptured, guncelKol.Status)

	saglayiciOturum, err := prov.GetSession(ctx, ses.ExternalID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), saglayiciOturum.AuthorizedAmount,
		"sağlayıcının defteri de kalan blokajı bırakmalı")
	assert.Equal(t, int64(1), saglayiciOturum.CapturedAmount)

	require.Error(t, svc.CancelPayment(ctx, ses.ID),
		"tahsil edilmiş oturum iptal edilemez; serbest bırakma tahsilat anında olmalı")
}

// TestInteropRedliOturumTelafiEdilebilir saga'nın patlayan ödeme adımını ilkel
// yüzeyden telafi edebildiğini doğrular.
func TestInteropRedliOturumTelafiEdilebilir(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	iop := service.NewInterop(svc)

	colID, err := iop.CreateCollection(ctx, testReference, "", testCurrency, testAmount)
	require.NoError(t, err)

	sesID, err := iop.OpenSessionWithData(ctx, colID, manual.ID, "interop-decline-"+colID,
		[]byte(`{"manual_outcome":"decline","manual_decline_reason":"saga testi"}`))
	require.NoError(t, err)

	_, _, err = iop.Authorize(ctx, sesID)
	require.Error(t, err, "ödeme adımı patlamalı")

	require.NoError(t, iop.Cancel(ctx, sesID))
	require.NoError(t, iop.Cancel(ctx, sesID))

	durum, err := iop.SessionStatus(ctx, sesID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionCanceled.String(), durum)
}

// TestQuerySaglayicisiGercekVeritabaninda Query katmanına açılan okuma
// yüzeyinin gerçek satırlar üzerinde çalıştığını doğrular (ADR 0004).
func TestQuerySaglayicisiGercekVeritabaninda(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	col := yeniKoleksiyon(ctx, t, svc)
	p := service.NewQueryProvider(svc)

	records, err := p.FetchByIDs(ctx, []string{col.ID, "paycol_YOK"},
		[]string{service.FieldID, service.FieldReference, service.FieldAmount, service.FieldStatus})

	require.NoError(t, err)
	require.Len(t, records, 1, "bulunamayan kimlik için kayıt DÖNMEZ")
	assert.Equal(t, col.ID, records[0][service.FieldID])
	assert.Equal(t, testReference, records[0][service.FieldReference])
	assert.Equal(t, testAmount, records[0][service.FieldAmount])
	assert.Equal(t, models.CollectionNotPaid.String(), records[0][service.FieldStatus])
}

// TestEszamanliFarkliOturumlarKoleksiyonTutariniKaybetmez koleksiyon satırı
// kilidinin KAYIP GÜNCELLEMEYİ engellediğini doğrular.
//
// Aynı koleksiyonda iki AYRI oturum, yarısı yarısına, aynı anda yetkilendirilir.
// Doğru sonuç ikisinin TOPLAMIDIR. Koleksiyon kilidi alınmasaydı iki akış da
// bloke tutarı sıfır okur, her biri kendi tutarını yazar ve son yazan diğerini
// EZERDİ — koleksiyon yarı ödenmiş görünür, tahsilat adımı eksik para çekerdi.
//
// Bu iddia "tek yetkilendirme" iddiasından farklıdır ve onunla aynı testte
// sınanamaz: orada aynı oturum, burada FARKLI oturumlar yarışır.
func TestEszamanliFarkliOturumlarKoleksiyonTutariniKaybetmez(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	col := yeniKoleksiyon(ctx, t, svc)

	yarim := testAmount / 2
	ilk, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		Amount:         yarim,
		IdempotencyKey: "split-1-" + col.ID,
	})
	require.NoError(t, err)
	ikinci, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		Amount:         yarim,
		IdempotencyKey: "split-2-" + col.ID,
	})
	require.NoError(t, err)

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		hatalar []error
	)
	wg.Add(2)
	for _, sessionID := range []string{ilk.ID, ikinci.ID} {
		go func() {
			defer wg.Done()
			if _, authErr := svc.AuthorizePayment(ctx, sessionID); authErr != nil {
				mu.Lock()
				hatalar = append(hatalar, authErr)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	assert.Empty(t, hatalar)

	guncelKol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, testAmount, guncelKol.AuthorizedAmount,
		"iki oturumun bloke tutarı TOPLANMALI; biri diğerini ezmemeli")
	assert.Equal(t, models.CollectionAuthorized, guncelKol.Status)
}

// TestEszamanliIkiCaptureTekTahsilatUretir bir oturumdan yalnızca BİR
// tahsilat çıktığını yarış altında doğrular.
//
// Oturum kilidi olmasaydı iki akış da oturumu "authorized" görür, ikisi de
// tahsilat satırı yazmaya çalışır ve benzersiz indekse çarpardı: biri
// errors.Conflict alır. Aşağıdaki "hiç hata olmamalı" iddiası tam olarak bunu
// yakalar — kilit, kısıtın patlamasını değil, ikinci akışın no-op'a düşmesini
// sağlar.
func TestEszamanliIkiCaptureTekTahsilatUretir(t *testing.T) {
	ctx := context.Background()
	svc, sayan := yeniSayanServis(t)
	col := yeniKoleksiyon(ctx, t, svc)
	ses, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: "concurrent-capture-" + col.ID,
	})
	require.NoError(t, err)
	_, err = svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)

	const goroutines = 8
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		hatalar   []error
		kimlikler = map[string]int{}
	)
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			pay, capErr := svc.CapturePayment(ctx, ses.ID, 0)

			mu.Lock()
			defer mu.Unlock()
			if capErr != nil {
				hatalar = append(hatalar, capErr)
				return
			}
			kimlikler[pay.ID]++
		}()
	}
	wg.Wait()

	assert.Empty(t, hatalar, "eşzamanlı tahsilatlar hata vermemeli")
	assert.Len(t, kimlikler, 1, "tüm çağrılar AYNI tahsilatı dönmeli")

	_, captureCagrilari, _ := sayan.sayimlar()
	assert.Equal(t, 1, captureCagrilari, "SAĞLAYICIYA yalnızca bir kez gidilmeli")

	tahsilatlar, err := svc.ListPayments(ctx, col.ID)
	require.NoError(t, err)
	assert.Len(t, tahsilatlar, 1, "veritabanında tek tahsilat satırı olmalı")

	guncelKol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, testAmount, guncelKol.CapturedAmount,
		"tahsil edilen tutar TEK tahsilat kadar olmalı, katları değil")
}

// --- mağaza kredisi ----------------------------------------------------------

// TestModulAyarAcikkenKrediSaglayicisiniKaydeder ayar AÇIKKEN sağlayıcının
// kaydedildiğini doğrular.
//
// Eşlik eden çift budur. [TestModulContainerdaAdlariKaydeder] varsayılan
// kurulumda kayıtta YALNIZCA manuel sağlayıcının olduğunu çiviliyor; tek başına
// o iddia, sağlayıcılar hiç kaydedilmiyor olsa da geçerdi. İkisi birlikte reddin
// AYARIN kararı olduğunu söylüyor — ki o ayar bir güvenlik kararı: müşteri
// iddiasına kanıtsız güvenen bir kurulumda kişiye bağlı bir tender, birinin
// adını yazan herkesin onun bakiyesini harcaması demek olurdu (ADR 0152). Ayar
// TEK ve iki tender'ı birden açıyor (ADR 0165): kredi kayıtlıyken puanın
// kayıtsız olduğu bir kurulum yok.
func TestModulAyarAcikkenKrediSaglayicisiniKaydeder(t *testing.T) {
	ctx := context.Background()
	c := container.New(nil)
	require.NoError(t, c.Provide("core.db", testPool))
	require.NoError(t, c.Provide("core.link", link.New(testPool, slog.New(slog.DiscardHandler))))
	require.NoError(t, c.Provide("core.eventbus", eventbus.NewInMemory(nil)))

	mod := payment.New(payment.Options{PersonBoundTenders: true})
	require.NoError(t, mod.Register(ctx, c))

	registry, err := container.Resolve[*service.ProviderRegistry](c, payment.ProvidersName)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{manual.ID, storecredit.ID, loyaltypoints.ID}, registry.IDs(),
		"ayar açıkken mağaza kredisi de sadakat puanı da seçilebilir birer ödeme yöntemi olmalı")
}

// lockWaiters counts the requests WAITING on a lock the given backend holds.
//
// Narrowing the waiters to a known blocker is what makes the count mean
// anything: "somebody in this database waits on a lock" is also true of another
// test's session, and then the assertion would hold before the request under
// test had run a single statement — green, and measuring nothing.
//
// It returns its error instead of asserting it, because it is polled: the
// condition of require.Eventually runs on a goroutine of its own, where a
// failed require is a runtime.Goexit of the wrong goroutine rather than a
// failed test.
func lockWaiters(ctx context.Context, blockerPID int32) (int64, error) {
	var waiters int64
	err := testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM pg_stat_activity
         WHERE datname = current_database()
           AND wait_event_type = 'Lock'
           AND $1 = ANY(pg_blocking_pids(pid))`, blockerPID).Scan(&waiters)

	return waiters, err
}

// singleConnectionRepository is a repository on a pool of exactly ONE
// connection, and the backend that connection runs on.
//
// It is how a competing transaction goes through the repository's own code and
// still has a backend the test knows before the transaction begins. The earlier
// competitors took the lock with a copy of its SQL, and a copy is what would
// have gone on proving the old lock's shape after the lock itself had changed.
func singleConnectionRepository(
	ctx context.Context, t *testing.T,
) (repo *repository.Repository, backendPID int32) {
	t.Helper()

	cfg := db.DefaultConfig(testDSN)
	cfg.MaxConns, cfg.MinConns = 1, 1
	pool, err := db.New(ctx, cfg, nil)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	require.NoError(t, pool.Pool().QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&backendPID))

	return repository.New(pool.Pool()), backendPID
}

// balanceTenderService builds a real-repository service with both balance
// tenders and the manual provider registered, earning at the ceiling rate, on a
// pool whose connections start with the given default isolation level — empty
// being the server's own.
//
// A pool at another level is built with pgxpool directly, because core/db
// refuses to build one (ADR 0166). That refusal is the installation's guard;
// this case proves the payment repository's own, which names READ COMMITTED on
// every transaction it begins and so holds on a pool nobody guarded (D119).
func balanceTenderService(t *testing.T, defaultIsolation string) *service.Service {
	t.Helper()

	pool := testPool.Pool()
	if defaultIsolation != "" {
		dsn := testDSN + "&default_transaction_isolation=" + url.QueryEscape(defaultIsolation)
		own, err := pgxpool.New(context.Background(), dsn)
		require.NoError(t, err)
		t.Cleanup(own.Close)

		var level string
		require.NoError(t, own.QueryRow(context.Background(),
			`SHOW default_transaction_isolation`).Scan(&level))
		require.Equal(t, defaultIsolation, level,
			"the pool's connections have to start at the level under test, or the case "+
				"proves the server's default again")
		pool = own
	}

	repo := repository.New(pool)
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(manual.New(repo, nil)))
	require.NoError(t, registry.Register(storecredit.New(repo, nil)))
	require.NoError(t, registry.Register(loyaltypoints.New(repo, nil)))

	svc, err := service.New(service.Options{
		Store: repo, Providers: registry, Events: eventbus.NewInMemory(nil),
		LoyaltyEarnBasisPoints: service.MaxLoyaltyEarnBasisPoints,
	})
	require.NoError(t, err)

	return svc
}

// balanceTender is what the lock test needs to know about one tender.
type balanceTender struct {
	name       string
	providerID string
	// fund puts testAmount on the balance the only way the module has: an
	// operator's issue for credit, a capture's earn for points.
	fund func(ctx context.Context, t *testing.T, svc *service.Service, customer string)
	// balance reads it.
	balance func(ctx context.Context, t *testing.T, svc *service.Service, customer string) int64
	// lock and spend are the competitor's two steps, through the repository.
	lock  func(ctx context.Context, repo *repository.Repository, customer string) error
	spend func(ctx context.Context, repo *repository.Repository, customer string) error
}

// balanceTenders are the two tenders the shared machine runs.
func balanceTenders() []balanceTender {
	return []balanceTender{
		{
			name:       "store credit",
			providerID: storecredit.ID,
			fund: func(ctx context.Context, t *testing.T, svc *service.Service, customer string) {
				t.Helper()
				_, err := svc.IssueCredit(ctx, service.IssueCreditInput{
					CustomerID: customer, CurrencyCode: testCurrency, Amount: testAmount,
					Reason: "lock test",
				})
				require.NoError(t, err)
			},
			balance: func(ctx context.Context, t *testing.T, svc *service.Service, customer string) int64 {
				t.Helper()
				balance, err := svc.StoreCreditBalance(ctx, customer, testCurrency)
				require.NoError(t, err)

				return balance
			},
			lock: func(ctx context.Context, repo *repository.Repository, customer string) error {
				return repo.LockStoreCreditBalance(ctx, customer, testCurrency)
			},
			spend: func(ctx context.Context, repo *repository.Repository, customer string) error {
				_, err := repo.AppendStoreCreditEntry(ctx, models.StoreCreditEntry{
					ID: models.NewStoreCreditEntryID(), CustomerID: customer,
					CurrencyCode: testCurrency, Amount: -testAmount,
					Kind: models.StoreCreditHold, Reference: "the competitor",
				})

				return err
			},
		},
		{
			name:       "loyalty points",
			providerID: loyaltypoints.ID,
			fund: func(ctx context.Context, t *testing.T, svc *service.Service, customer string) {
				t.Helper()
				earnPoints(ctx, t, svc, customer)
			},
			balance: pointBalance,
			lock: func(ctx context.Context, repo *repository.Repository, customer string) error {
				return repo.LockLoyaltyBalance(ctx, customer, testCurrency)
			},
			spend: func(ctx context.Context, repo *repository.Repository, customer string) error {
				_, err := repo.AppendLoyaltyEntry(ctx, models.LoyaltyEntry{
					ID: models.NewLoyaltyEntryID(), CustomerID: customer,
					CurrencyCode: testCurrency, Points: -testAmount,
					Kind: models.LoyaltyHold, Reference: "the competitor",
				})

				return err
			},
		},
	}
}

// TestAnAuthorizationWaitsOnTheBalanceLock proves the correctness argument of
// both balance tenders against a real server: the balance is read and acted on,
// so two authorizations of one customer must not both see the same money.
//
// The interleaving is FORCED, not hoped for. A competitor takes the balance
// lock through the repository's own function; that the authorization under test
// WAITS on it is seen through pg_blocking_pids; the competitor spends the whole
// balance and commits; only then does the authorization go on, and it has to
// read the fresh balance and decline. The balance covers ONE spend, so a lock
// that lets the two through together ends below zero.
//
// Three shapes, and each one is a way the lock was, or could be, not a lock:
//
//   - the customer had rows when the competitor locked: the shape ADR 0152 and
//     the first draft of ADR 0165 proved, and the only one a row lock passes;
//   - the customer had NO rows until the competitor held the lock, and the
//     money arrived while it did: a row lock took nothing from a customer with
//     none, the authorization locked the new row without waiting, and both
//     spent it (D118);
//   - the pool's connections default to REPEATABLE READ: the sum after the wait
//     then reads the snapshot taken before it, and the lock serializes two
//     authorizations that both decide on the balance before either hold (D119).
func TestAnAuthorizationWaitsOnTheBalanceLock(t *testing.T) {
	shapes := []struct {
		name                string
		fundedBeforeTheLock bool
		defaultIsolation    string
	}{
		{name: "the customer had rows", fundedBeforeTheLock: true},
		{name: "the customer had no rows until the lock was held"},
		{
			name:                "the server defaults to repeatable read",
			fundedBeforeTheLock: true,
			defaultIsolation:    "repeatable read",
		},
	}

	for _, tender := range balanceTenders() {
		for _, shape := range shapes {
			t.Run(tender.name+"/"+shape.name, func(t *testing.T) {
				ctx := context.Background()
				svc := balanceTenderService(t, shape.defaultIsolation)

				customer := "cus_" + models.NewPaymentCollectionID()
				if shape.fundedBeforeTheLock {
					tender.fund(ctx, t, svc, customer)
				}

				col := customerCollection(ctx, t, svc, customer, testAmount)
				ses, err := svc.CreateSession(ctx, col.ID, tender.providerID,
					service.CreateSessionInput{IdempotencyKey: tender.providerID + "-lock-" + col.ID})
				require.NoError(t, err)

				// --- the competitor: takes the balance and holds it ---

				rival, rivalPID := singleConnectionRepository(ctx, t)
				locked, spend := make(chan struct{}), make(chan struct{})
				rivalDone := make(chan error, 1)
				go func() {
					rivalDone <- rival.WithTx(ctx, func(ctx context.Context) error {
						if err := tender.lock(ctx, rival, customer); err != nil {
							return err
						}
						close(locked)
						<-spend

						return tender.spend(ctx, rival, customer)
					})
				}()
				select {
				case <-locked:
				case err := <-rivalDone:
					t.Fatalf("the competitor could not take the balance lock: %v", err)
				}

				if !shape.fundedBeforeTheLock {
					// The money a row lock could not see: committed while the
					// competitor holds the balance of a customer who had no row
					// for it to take.
					tender.fund(ctx, t, svc, customer)
				}
				require.Equal(t, testAmount, tender.balance(ctx, t, svc, customer),
					"the balance covers exactly ONE spend; covering both would let the "+
						"test tell nothing apart")

				// --- the authorization under test: it has to WAIT ---

				done := make(chan error, 1)
				go func() { _, authErr := svc.AuthorizePayment(ctx, ses.ID); done <- authErr }()

				var lastPollErr atomic.Value
				waited := assert.Eventually(t, func() bool {
					waiters, err := lockWaiters(ctx, rivalPID)
					if err != nil {
						lastPollErr.Store(err.Error())

						return false
					}

					return waiters > 0
				}, 10*time.Second, 10*time.Millisecond)

				// The competitor spends the balance and commits, whatever the poll
				// saw, so that nothing below waits on a transaction left open.
				close(spend)
				require.NoError(t, <-rivalDone)

				if !waited {
					pollErr, _ := lastPollErr.Load().(string)
					t.Fatalf("the authorization never waited on the competitor's balance lock, "+
						"so it decided on a balance somebody else was spending (last poll "+
						"error: %q)", pollErr)
				}

				// --- and it has to read the FRESH balance and decline ---

				select {
				case authErr := <-done:
					require.Error(t, authErr,
						"the authorization must NOT pass after the competitor spent the "+
							"balance: if it did, it read the balance from before the spend")
					assert.True(t, errors.IsConflict(authErr),
						"an insufficient balance is a DECLINE, not a server error: %v", authErr)
				case <-time.After(10 * time.Second):
					t.Fatal("the authorization did not finish after the competitor committed")
				}

				assert.Zero(t, tender.balance(ctx, t, svc, customer),
					"the balance has to stay at zero; below it the customer spent money "+
						"they did not hold twice over")
			})
		}
	}
}

// Defterin yalnızca EKLENEN bir kayıt olduğunu okuyan kapı buradan TAŞINDI:
// internal/arch'taki TestThePaymentLedgersAreAppendOnlyInSQL (ADR 0164).
//
// Buradaki sürümün öznesi bir DOSYAydı — adıyla okunan tek bir yol — ve modülün
// ikinci defteri eklendiği gün onu göremezdi. Taşınan sürümün öznesi DİZİN, iki
// tabloyu da adıyla arıyor, dosya taşındığında sessizce boş dize okumak yerine
// kırmızı oluyor ve entegrasyon etiketinin arkasında değil hızlı şeritte
// koşuyor.

// --- sadakat puanı defteri (ADR 0164) ----------------------------------------

// earningService builds a real-repository service that earns at the given rate.
func earningService(t *testing.T, basisPoints int64) *service.Service {
	t.Helper()

	repo := repository.New(testPool.Pool())
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(manual.New(repo, nil)))

	svc, err := service.New(service.Options{
		Store: repo, Providers: registry, Events: eventbus.NewInMemory(nil),
		LoyaltyEarnBasisPoints: basisPoints,
	})
	require.NoError(t, err)

	return svc
}

// TestACaptureWritesARealPointRow proves the ledger against the real schema.
//
// The unit tests prove the ARITHMETIC against a fake store. What only this test
// can see is that the row the arithmetic produces satisfies the table: the sign
// matches the kind, the currency matches the format, the reference is not blank
// and the points are not zero. A row that the service is happy with and the
// schema refuses would fail a capture whose money has already moved.
func TestACaptureWritesARealPointRow(t *testing.T) {
	ctx := context.Background()
	svc := earningService(t, 100)
	customer := "cus_" + models.NewPaymentCollectionID()

	col, err := svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
		Reference: testReference, Amount: testAmount,
		CurrencyCode: testCurrency, CustomerID: customer,
	})
	require.NoError(t, err)

	ses, err := svc.CreateSession(ctx, col.ID, manual.ID,
		service.CreateSessionInput{IdempotencyKey: "loyalty-" + col.ID})
	require.NoError(t, err)
	_, err = svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)
	capture, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)

	balance, err := svc.LoyaltyBalance(ctx, customer, testCurrency)
	require.NoError(t, err)
	assert.Equal(t, testAmount*100/10_000, balance,
		"the balance is the target the captured money implies")

	rows, total, err := svc.ListLoyalty(ctx, service.ListLoyaltyInput{
		CustomerID: customer, CurrencyCode: testCurrency,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	assert.Equal(t, models.LoyaltyEarn, rows[0].Kind)
	assert.Equal(t, col.ID, rows[0].Reference)
	assert.False(t, rows[0].CreatedAt.IsZero(), "the moment is stamped by the schema's default")

	_, err = svc.RefundPayment(ctx, capture.ID, testAmount/2, "half back")
	require.NoError(t, err)

	balance, err = svc.LoyaltyBalance(ctx, customer, testCurrency)
	require.NoError(t, err)
	assert.Equal(t, testAmount/2*100/10_000, balance,
		"half the money back is half the points back")
}

// TestTheLedgerRefusesASignThatContradictsItsKind proves the constraint pair.
//
// The sign is decided by the kind and the SCHEMA holds the pairing, which is the
// only reason a refund that was written positive — the mistake that hands a
// customer points for taking their money back — cannot enter the table. No Go
// code checks it, and none should: a check in the service is a check one caller
// can go around.
//
// The three spending kinds (ADR 0165) are in the table too, each with the sign
// its meaning forbids: a hold that ADDED points would let a checkout pay the
// customer for buying, and a release or a refund that took points away would
// charge them twice for a session that gave up or a payment they got back.
// Migration 000006 re-creates the pair under the same names; this is what says
// the re-created pair still binds.
func TestTheLedgerRefusesASignThatContradictsItsKind(t *testing.T) {
	ctx := context.Background()
	repo := repository.New(testPool.Pool())
	customer := "cus_" + models.NewPaymentCollectionID()

	for _, bad := range []struct {
		name  string
		entry models.LoyaltyEntry
	}{
		{
			name: "a reverse written positive",
			entry: models.LoyaltyEntry{
				CustomerID: customer, CurrencyCode: testCurrency,
				Points: 10, Kind: models.LoyaltyReverse, Reference: "paycol_x",
			},
		},
		{
			name: "an earn written negative",
			entry: models.LoyaltyEntry{
				CustomerID: customer, CurrencyCode: testCurrency,
				Points: -10, Kind: models.LoyaltyEarn, Reference: "paycol_x",
			},
		},
		{
			name: "a hold written positive",
			entry: models.LoyaltyEntry{
				CustomerID: customer, CurrencyCode: testCurrency,
				Points: 10, Kind: models.LoyaltyHold, Reference: "lpses_x",
			},
		},
		{
			name: "a release written negative",
			entry: models.LoyaltyEntry{
				CustomerID: customer, CurrencyCode: testCurrency,
				Points: -10, Kind: models.LoyaltyRelease, Reference: "lpses_x",
			},
		},
		{
			name: "a refund written negative",
			entry: models.LoyaltyEntry{
				CustomerID: customer, CurrencyCode: testCurrency,
				Points: -10, Kind: models.LoyaltyRefund, Reference: "lpses_x",
			},
		},
		{
			name: "a kind outside the vocabulary",
			entry: models.LoyaltyEntry{
				CustomerID: customer, CurrencyCode: testCurrency,
				Points: 10, Kind: models.LoyaltyKind("bonus"), Reference: "paycol_x",
			},
		},
		{
			name: "no points at all",
			entry: models.LoyaltyEntry{
				CustomerID: customer, CurrencyCode: testCurrency,
				Points: 0, Kind: models.LoyaltyEarn, Reference: "paycol_x",
			},
		},
		{
			name: "a row belonging to no collection",
			entry: models.LoyaltyEntry{
				CustomerID: customer, CurrencyCode: testCurrency,
				Points: 10, Kind: models.LoyaltyEarn, Reference: "   ",
			},
		},
		{
			name: "a currency that is not a code",
			entry: models.LoyaltyEntry{
				CustomerID: customer, CurrencyCode: "try",
				Points: 10, Kind: models.LoyaltyEarn, Reference: "paycol_x",
			},
		},
		{
			name: "points belonging to nobody",
			entry: models.LoyaltyEntry{
				CustomerID: "  ", CurrencyCode: testCurrency,
				Points: 10, Kind: models.LoyaltyEarn, Reference: "paycol_x",
			},
		},
	} {
		t.Run(bad.name, func(t *testing.T) {
			entry := bad.entry
			entry.ID = models.NewLoyaltyEntryID()

			_, err := repo.AppendLoyaltyEntry(ctx, entry)

			require.Error(t, err, "the schema has to refuse this row")
		})
	}
}

// TestTwoConcurrentCapturesEarnTheTargetOnce proves the serialization the
// decision rests on, with real row locks.
//
// Each capture computes a TARGET and appends the difference between it and what
// the collection has already been written. That read and that write are inside
// the collection's own lock, so two captures on the same collection queue up and
// the second sees the first one's row. Without the lock both would read nothing
// written, both would append their own whole target, and the customer would hold
// twice the points the money earned.
//
// A unit test cannot see this: the fake store has no transactions and no rows to
// lock, so it would be asserting about a mechanism it does not have.
func TestTwoConcurrentCapturesEarnTheTargetOnce(t *testing.T) {
	ctx := context.Background()
	svc := earningService(t, 100)
	customer := "cus_" + models.NewPaymentCollectionID()

	col, err := svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
		Reference: testReference, Amount: testAmount,
		CurrencyCode: testCurrency, CustomerID: customer,
	})
	require.NoError(t, err)

	// TWO sessions, each holding half, so both captures are real and both move
	// the collection's captured total.
	half := testAmount / 2
	sessions := make([]models.PaymentSession, 0, 2)
	for i := range 2 {
		ses, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
			IdempotencyKey: fmt.Sprintf("loyalty-race-%s-%d", col.ID, i),
			Amount:         half,
		})
		require.NoError(t, err)
		_, err = svc.AuthorizePayment(ctx, ses.ID)
		require.NoError(t, err)
		sessions = append(sessions, ses)
	}

	var wg sync.WaitGroup
	errs := make([]error, len(sessions))
	start := make(chan struct{})
	for i := range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = svc.CapturePayment(ctx, sessions[i].ID, 0)
		}()
	}
	close(start)
	wg.Wait()

	for i := range errs {
		require.NoError(t, errs[i], "both captures have to succeed")
	}

	balance, err := svc.LoyaltyBalance(ctx, customer, testCurrency)
	require.NoError(t, err)
	assert.Equal(t, testAmount*100/10_000, balance,
		"the points are the target the whole captured amount implies, whatever order "+
			"the two captures ran in; a balance of twice this would mean each capture "+
			"read a ledger the other had not written to yet")
}

// --- the loyalty-points tender (ADR 0165) -------------------------------------

// pointsService builds a real-repository service with BOTH the manual provider
// and the loyalty-points tender registered, earning at the ceiling rate.
//
// The ceiling — one point per minor unit — is chosen so that ONE manual capture
// of testAmount leaves the customer holding exactly testAmount points, which is
// what a spend of testAmount needs and not a point more. Points are only ever
// EARNED: the ledger has no issue endpoint, so every balance below starts as a
// capture through the manual provider. The repository is shared between the
// service and the tender for yeniKrediServisi's reason: the hold and the session
// state are written in one transaction, or not at all.
func pointsService(t *testing.T) *service.Service {
	t.Helper()

	repo := repository.New(testPool.Pool())
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(manual.New(repo, nil)))
	require.NoError(t, registry.Register(loyaltypoints.New(repo, nil)))

	svc, err := service.New(service.Options{
		Store: repo, Providers: registry, Events: eventbus.NewInMemory(nil),
		LoyaltyEarnBasisPoints: service.MaxLoyaltyEarnBasisPoints,
	})
	require.NoError(t, err)

	return svc
}

// customerCollection opens a collection of the given amount that NAMES the
// customer, so the tender has an owner and the earn path a recipient.
func customerCollection(
	ctx context.Context, t *testing.T, svc *service.Service, customer string, amount int64,
) models.PaymentCollection {
	t.Helper()

	col, err := svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
		Reference:    testReference + "-points",
		CustomerID:   customer,
		Amount:       amount,
		CurrencyCode: testCurrency,
	})
	require.NoError(t, err)

	return col
}

// payThrough opens a session at the given provider for the given amount — zero
// being the collection's remainder — authorizes it and captures it. It returns
// the module's session, whose ExternalID is the provider's own, and the capture.
func payThrough(
	ctx context.Context, t *testing.T, svc *service.Service,
	col models.PaymentCollection, providerID string, amount int64,
) (models.PaymentSession, models.Payment) {
	t.Helper()

	ses, err := svc.CreateSession(ctx, col.ID, providerID, service.CreateSessionInput{
		IdempotencyKey: fmt.Sprintf("%s-%s-%d", providerID, col.ID, amount),
		Amount:         amount,
	})
	require.NoError(t, err)
	_, err = svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)
	capture, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)

	return ses, capture
}

// earnPoints gives the customer testAmount points the only way there is: a
// manual capture of testAmount at the ceiling rate. It returns the collection
// that earned them and the capture, so a test can refund it.
func earnPoints(
	ctx context.Context, t *testing.T, svc *service.Service, customer string,
) (models.PaymentCollection, models.Payment) {
	t.Helper()

	col := customerCollection(ctx, t, svc, customer, testAmount)
	_, capture := payThrough(ctx, t, svc, col, manual.ID, 0)

	return col, capture
}

// pointBalance reads the customer's points in the test currency.
func pointBalance(ctx context.Context, t *testing.T, svc *service.Service, customer string) int64 {
	t.Helper()

	balance, err := svc.LoyaltyBalance(ctx, customer, testCurrency)
	require.NoError(t, err)

	return balance
}

// pointRows reads the customer's whole point history in the test currency.
func pointRows(ctx context.Context, t *testing.T, svc *service.Service, customer string) []models.LoyaltyEntry {
	t.Helper()

	rows, total, err := svc.ListLoyalty(ctx, service.ListLoyaltyInput{
		CustomerID: customer, CurrencyCode: testCurrency,
	})
	require.NoError(t, err)
	require.Len(t, rows, int(total), "the whole history fits in one page")

	return rows
}

// rowsReferencing keeps the rows whose reference is the given identifier.
func rowsReferencing(rows []models.LoyaltyEntry, reference string) []models.LoyaltyEntry {
	var out []models.LoyaltyEntry
	for _, row := range rows {
		if row.Reference == reference {
			out = append(out, row)
		}
	}

	return out
}

// rowsOfKind keeps the rows of the given kind.
func rowsOfKind(rows []models.LoyaltyEntry, kind models.LoyaltyKind) []models.LoyaltyEntry {
	var out []models.LoyaltyEntry
	for _, row := range rows {
		if row.Kind == kind {
			out = append(out, row)
		}
	}

	return out
}

// TestACapturePaidWithPointsEarnsNothing proves the exclusion the earn rule
// gained in ADR 0165, against the real query that joins captures to sessions.
//
// At the ceiling rate a capture paid with points would earn itself back: the
// customer spends testAmount points, the capture earns testAmount points, and
// one point buys unbounded goods. The earn target is therefore the net of the
// collection's captures whose session is at ANOTHER provider, and this test
// reads it two ways: an order paid entirely with points earns nothing at all,
// and an order split between points and the manual provider earns on the
// manual half only. The split is the module's, not the storefront's — the
// storefront pays an order with one tender — and the module has to get it
// right for the admin surface that can split.
func TestACapturePaidWithPointsEarnsNothing(t *testing.T) {
	ctx := context.Background()
	svc := pointsService(t)
	customer := "cus_" + models.NewPaymentCollectionID()
	earning, _ := earnPoints(ctx, t, svc, customer)

	// --- an order paid entirely with points ---

	paid := customerCollection(ctx, t, svc, customer, testAmount)
	ses, _ := payThrough(ctx, t, svc, paid, loyaltypoints.ID, 0)

	assert.Zero(t, pointBalance(ctx, t, svc, customer),
		"the points were spent and the spend earned none back")

	rows := pointRows(ctx, t, svc, customer)
	assert.Empty(t, rowsReferencing(rows, paid.ID),
		"no row may reference the collection paid with points: nothing was earned on it")
	require.Len(t, rows, 2, "one earn and one hold, and nothing else")

	holds := rowsReferencing(rows, ses.ExternalID)
	require.Len(t, holds, 1, "the hold references the provider's OWN session")
	assert.Equal(t, models.LoyaltyHold, holds[0].Kind)
	assert.Equal(t, -testAmount, holds[0].Points)

	earns := rowsReferencing(rows, earning.ID)
	require.Len(t, earns, 1)
	assert.Equal(t, models.LoyaltyEarn, earns[0].Kind)

	// --- an order split between points and the manual provider ---

	earnPoints(ctx, t, svc, customer)
	require.Equal(t, testAmount, pointBalance(ctx, t, svc, customer))

	half := testAmount / 2
	split := customerCollection(ctx, t, svc, customer, testAmount)
	payThrough(ctx, t, svc, split, loyaltypoints.ID, half)
	payThrough(ctx, t, svc, split, manual.ID, 0)

	var earnedOnSplit int64
	for _, row := range rowsReferencing(pointRows(ctx, t, svc, customer), split.ID) {
		assert.Contains(t, []models.LoyaltyKind{models.LoyaltyEarn, models.LoyaltyReverse}, row.Kind,
			"a row referencing a collection is an earn or a reverse, never a spend")
		earnedOnSplit += row.Points
	}
	manualHalfEarns := half * service.MaxLoyaltyEarnBasisPoints / 10_000
	assert.Equal(t, manualHalfEarns, earnedOnSplit,
		"only the half that came through the manual provider earns; the half that came "+
			"out of the point ledger is a liability being extinguished, not revenue")
	assert.Equal(t, testAmount-half+manualHalfEarns, pointBalance(ctx, t, svc, customer))
}

// TestCreditThatIsSpentStillEarns is the other half of the exclusion, against
// the real query: only the POINTS tender is left out of the earn base.
//
// Store credit is money the shop owes at face value — a refund that stayed in
// the shop — and an order paid with it is revenue the day the credit is spent;
// points are the programme's own currency and earning on them would let a point
// earn itself back. An exclusion that grew to "every balance tender" would
// answer both with nothing, and no other test would notice, because every other
// earn in this file is a manual capture.
func TestCreditThatIsSpentStillEarns(t *testing.T) {
	ctx := context.Background()
	svc := balanceTenderService(t, "")

	customer := "cus_" + models.NewPaymentCollectionID()
	_, err := svc.IssueCredit(ctx, service.IssueCreditInput{
		CustomerID: customer, CurrencyCode: testCurrency, Amount: testAmount,
		Reason: "a late delivery",
	})
	require.NoError(t, err)

	col := customerCollection(ctx, t, svc, customer, testAmount)
	payThrough(ctx, t, svc, col, storecredit.ID, 0)

	credit, err := svc.StoreCreditBalance(ctx, customer, testCurrency)
	require.NoError(t, err)
	require.Zero(t, credit, "the order spent the whole credit")

	earned := rowsReferencing(pointRows(ctx, t, svc, customer), col.ID)
	require.Len(t, earned, 1, "the credit-paid capture earned, once")
	assert.Equal(t, models.LoyaltyEarn, earned[0].Kind)
	assert.Equal(t, testAmount*service.MaxLoyaltyEarnBasisPoints/10_000, earned[0].Points,
		"credit earns like the money it stands for")
}

// TestARefundOfAPointsPaidOrderGivesThePointsBack proves the refund half of the
// state machine on the real ledger.
//
// A points tender holds no card and no account, so a refund has one destination:
// a positive row in the customer's own balance, at FACE VALUE — a point is worth
// one minor unit in both directions. The row references the provider's own
// session and not the collection, because the earn target is recomputed per
// collection from the rows that reference it, and a refund row carrying the
// collection id would read as points the collection had been written. And no
// reverse appears: the collection earned nothing, so there is nothing to
// reverse.
func TestARefundOfAPointsPaidOrderGivesThePointsBack(t *testing.T) {
	ctx := context.Background()
	svc := pointsService(t)
	customer := "cus_" + models.NewPaymentCollectionID()
	earnPoints(ctx, t, svc, customer)

	paid := customerCollection(ctx, t, svc, customer, testAmount)
	ses, capture := payThrough(ctx, t, svc, paid, loyaltypoints.ID, 0)
	require.Zero(t, pointBalance(ctx, t, svc, customer))

	_, err := svc.RefundPayment(ctx, capture.ID, testAmount/2, "half back")
	require.NoError(t, err)

	assert.Equal(t, testAmount/2, pointBalance(ctx, t, svc, customer),
		"half the money back is half the points back, at face value and not at the earn rate")

	rows := pointRows(ctx, t, svc, customer)
	require.Len(t, rows, 3, "an earn, a hold and a refund")

	refunds := rowsOfKind(rows, models.LoyaltyRefund)
	require.Len(t, refunds, 1, "the row a refund writes is a REFUND, not a release")
	assert.Equal(t, testAmount/2, refunds[0].Points)
	assert.Equal(t, ses.ExternalID, refunds[0].Reference,
		"the refund references the PROVIDER's session, the one the hold references")
	assert.True(t, strings.HasPrefix(refunds[0].Reference, models.LoyaltySessionIDPrefix),
		"the reference is the tender's own identifier: %s", refunds[0].Reference)
	assert.NotEqual(t, paid.ID, refunds[0].Reference,
		"a spend row never references a collection")

	assert.Empty(t, rowsOfKind(rows, models.LoyaltyReverse),
		"nothing was earned on this collection, so nothing is reversed")
	assert.Empty(t, rowsReferencing(rows, paid.ID))
}

// TestABalanceBelowZeroIsAState proves the sentence ADR 0165 wrote about the
// hole: a balance can fall below zero, the refund that makes it fall is never
// refused, the tender declines against it and the next earn fills it first.
//
// The points a capture earned may already be spent when that capture is
// refunded. The reverse is written all the same — a refund is money going back
// and the ledger records what happened — so the balance goes negative. That is a
// STATE, not a fault: nothing repairs it, the tender simply reads a balance that
// covers nothing, and the next capture's earn is where the hole closes.
//
// No mutation is needed for the refund half: the assertion that it is not
// refused is a require.NoError on the real path, and Service.RefundPayment reads
// no balance anywhere between its lock and its ledger write.
func TestABalanceBelowZeroIsAState(t *testing.T) {
	ctx := context.Background()
	svc := pointsService(t)
	customer := "cus_" + models.NewPaymentCollectionID()

	earning, earningCapture := earnPoints(ctx, t, svc, customer)
	spent := customerCollection(ctx, t, svc, customer, testAmount)
	payThrough(ctx, t, svc, spent, loyaltypoints.ID, 0)
	require.Zero(t, pointBalance(ctx, t, svc, customer), "everything earned is spent")

	// --- the capture that earned is refunded in full ---

	_, err := svc.RefundPayment(ctx, earningCapture.ID, 0, "everything back")
	require.NoError(t, err,
		"a refund is never refused because the points it reverses were spent already")

	assert.Equal(t, -testAmount, pointBalance(ctx, t, svc, customer),
		"the reverse takes back what was earned, and what was earned is gone: the hole")

	reverses := rowsOfKind(pointRows(ctx, t, svc, customer), models.LoyaltyReverse)
	require.Len(t, reverses, 1)
	assert.Equal(t, -testAmount, reverses[0].Points)
	assert.Equal(t, earning.ID, reverses[0].Reference,
		"the reverse references the collection whose earn it undoes")

	// --- the tender declines against the hole ---

	tiny := customerCollection(ctx, t, svc, customer, 1)
	tinySes, err := svc.CreateSession(ctx, tiny.ID, loyaltypoints.ID,
		service.CreateSessionInput{IdempotencyKey: "points-hole-" + tiny.ID})
	require.NoError(t, err)

	_, err = svc.AuthorizePayment(ctx, tinySes.ID)
	require.Error(t, err, "one minor unit is more than a negative balance covers")
	assert.True(t, errors.IsConflict(err),
		"a balance that does not cover the payment is a DECLINE, not a server error: %v", err)
	assert.Equal(t, -testAmount, pointBalance(ctx, t, svc, customer),
		"a declined authorization writes no hold")

	// --- the next earn fills the hole first ---

	filling := customerCollection(ctx, t, svc, customer, testAmount+1)
	payThrough(ctx, t, svc, filling, manual.ID, 0)

	assert.Equal(t, int64(1), pointBalance(ctx, t, svc, customer),
		"the earn lands on the negative balance: what is left to spend is one point")
}

// TestBothTendersAnswerReconciliation proves that the hourly reconciliation can
// ASK both balance tenders about real sessions.
//
// Service.reconcileOne reaches a provider only through the contract and asks it
// with a TYPE ASSERTION to [coreprovider.SessionInspector]; a provider that does
// not satisfy the interface is counted as unaskable, and nothing goes red. Store
// credit was that provider until ADR 0165, the same shape as the manual provider
// that did answer. The compile-time pins in the two tender packages say the TYPE
// satisfies the interface; what this test adds is that the answer is RIGHT: an
// authorized session reports authorized with the amount held and nothing
// captured, and a session the tender never opened is disowned with NotFound
// rather than answered with zeros — which the reconciler counts as unknown, its
// one finding about an installation.
func TestBothTendersAnswerReconciliation(t *testing.T) {
	ctx := context.Background()
	repo := repository.New(testPool.Pool())
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(manual.New(repo, nil)))
	require.NoError(t, registry.Register(storecredit.New(repo, nil)))
	require.NoError(t, registry.Register(loyaltypoints.New(repo, nil)))

	svc, err := service.New(service.Options{
		Store: repo, Providers: registry, Events: eventbus.NewInMemory(nil),
		LoyaltyEarnBasisPoints: service.MaxLoyaltyEarnBasisPoints,
	})
	require.NoError(t, err)

	customer := "cus_" + models.NewPaymentCollectionID()
	// Both balances are funded the way each is: credit is issued, points are
	// earned.
	_, err = svc.IssueCredit(ctx, service.IssueCreditInput{
		CustomerID: customer, CurrencyCode: testCurrency,
		Amount: testAmount, Reason: "reconciliation",
	})
	require.NoError(t, err)
	earnPoints(ctx, t, svc, customer)

	for _, tender := range []struct {
		id      string
		unknown string
	}{
		{id: storecredit.ID, unknown: models.StoreCreditSessionIDPrefix + "NOBODYOPENEDTHIS"},
		{id: loyaltypoints.ID, unknown: models.LoyaltySessionIDPrefix + "NOBODYOPENEDTHIS"},
	} {
		t.Run(tender.id, func(t *testing.T) {
			prov, err := registry.Get(tender.id)
			require.NoError(t, err)

			// The same assertion the reconciler makes, on the same static type.
			inspector, ok := prov.(coreprovider.SessionInspector)
			require.True(t, ok,
				"the reconciler would count every %s session as unaskable", tender.id)

			col := customerCollection(ctx, t, svc, customer, testAmount)
			ses, err := svc.CreateSession(ctx, col.ID, tender.id,
				service.CreateSessionInput{IdempotencyKey: "reconcile-" + col.ID})
			require.NoError(t, err)
			_, err = svc.AuthorizePayment(ctx, ses.ID)
			require.NoError(t, err)

			inspection, err := inspector.InspectSession(ctx, ses.ExternalID)
			require.NoError(t, err)
			assert.Equal(t, coreprovider.SessionAuthorized, inspection.Status)
			assert.Equal(t, testAmount, inspection.AuthorizedAmount,
				"the tender reports the amount it holds")
			assert.Zero(t, inspection.CapturedAmount)
			assert.Zero(t, inspection.RefundedAmount)

			_, err = inspector.InspectSession(ctx, tender.unknown)
			assert.True(t, errors.IsNotFound(err),
				"a session the tender never opened is disowned, not answered with zeros: %v", err)
		})
	}
}

// TestTheEarnTargetSumsOnlyWhatACollectionEarned pins the SUBJECT of the query
// the earn target is computed from.
//
// LoyaltyPointsForReference sums the two earning kinds and nothing else. Every
// spend row the tender writes references its own session, so the filter changes
// no sum the system produces today — which is exactly why it needs a witness of
// its own: a mutation that dropped it would leave every other test green. The
// row planted here is one nothing in the tree writes, a hold carrying a
// collection id, and it is planted straight into the repository because the
// point is what the QUERY does with such a row, not how it got there. Without
// the filter the target would read the hold as points already written and the
// next capture would earn them back (ADR 0165).
func TestTheEarnTargetSumsOnlyWhatACollectionEarned(t *testing.T) {
	ctx := context.Background()
	repo := repository.New(testPool.Pool())
	customer := "cus_" + models.NewPaymentCollectionID()
	collection := models.NewPaymentCollectionID()

	for _, entry := range []models.LoyaltyEntry{
		{Points: 300, Kind: models.LoyaltyEarn},
		{Points: -100, Kind: models.LoyaltyReverse},
		// The three spend rows deliberately do NOT net to zero: a fixture whose
		// spend rows canceled out would read the same with and without the
		// filter, and the mutation would survive it.
		{Points: -50, Kind: models.LoyaltyHold},
		{Points: 20, Kind: models.LoyaltyRelease},
		{Points: 10, Kind: models.LoyaltyRefund},
	} {
		entry.ID = models.NewLoyaltyEntryID()
		entry.CustomerID = customer
		entry.CurrencyCode = testCurrency
		entry.Reference = collection
		_, err := repo.AppendLoyaltyEntry(ctx, entry)
		require.NoError(t, err)
	}

	earned, err := repo.LoyaltyPointsForReference(ctx, collection)
	require.NoError(t, err)
	assert.Equal(t, int64(200), earned,
		"300 earned - 100 reversed; the hold, the release and the refund are not what the collection EARNED")

	balance, err := repo.LoyaltyBalance(ctx, customer, testCurrency)
	require.NoError(t, err)
	assert.Equal(t, int64(180), balance,
		"the balance, unlike the target, is every row: 300 - 100 - 50 + 20 + 10")
}
