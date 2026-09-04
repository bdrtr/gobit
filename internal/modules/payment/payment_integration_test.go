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
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/link"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/payment"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/repository"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

const postgresImage = "postgres:16-alpine"

// modulTablolari modülün sahip olduğu tablolardır; migration testleri bu
// listeyi kullanır.
var modulTablolari = []string{
	"payment_collections", "payment_sessions", "payments", "refunds",
	"payment_manual_sessions",
}

// Test verisinde kullanılan sabitler. Referans BAŞKA bir modüle (sepet ya da
// sipariş) aittir; bu modül varlığını doğrulamaz (Prensip 2.2).
const (
	testReferans = "cart_TEST"
	testPara     = "TRY"
	testTutar    = int64(50_000)
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

	return m.Run()
}

// yeniServis gerçek depo ve GERÇEK manuel sağlayıcı üzerinde çalışan bir
// servis kurar.
func yeniServis(t *testing.T) (*service.Service, *manual.Provider) {
	t.Helper()

	repo := repository.New(testPool.Pool())
	prov := manual.New(repo, nil)
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(prov))

	svc, err := service.New(service.Options{Store: repo, Providers: registry})
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

	svc, err := service.New(service.Options{Store: repo, Providers: registry})
	require.NoError(t, err)
	return svc, sayan
}

// yeniKoleksiyon test için bir ödeme koleksiyonu açar.
func yeniKoleksiyon(ctx context.Context, t *testing.T, svc *service.Service) models.PaymentCollection {
	t.Helper()

	col, err := svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
		Reference:    testReferans,
		Amount:       testTutar,
		CurrencyCode: testPara,
	})
	require.NoError(t, err)
	return col
}

// tabloVar tablonun veritabanında olup olmadığını bildirir.
func tabloVar(ctx context.Context, t *testing.T, table string) bool {
	t.Helper()

	var exists bool
	err := testPool.Pool().QueryRow(ctx,
		`SELECT EXISTS (
             SELECT 1 FROM pg_class c
             JOIN pg_namespace n ON n.oid = c.relnamespace
             WHERE c.relname = $1 AND c.relkind = 'r' AND n.nspname = current_schema()
         )`, table).Scan(&exists)
	require.NoError(t, err)
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
	svc, _ := yeniServis(t)

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

	require.NoError(t, db.MigrateDown(ctx, testDSN, src, payment.ModuleName, 0),
		"down başarısız — bu, modülün bir daha migrate EDİLEMEMESİ demektir")
	for _, table := range modulTablolari {
		assert.False(t, tabloVar(ctx, t, table), "%s geri alma sonrası kalmamalı", table)
	}

	require.NoError(t, db.Migrate(ctx, testDSN, src, payment.ModuleName))
	for _, table := range modulTablolari {
		assert.True(t, tabloVar(ctx, t, table), "%s yeniden uygulanmalı", table)
	}

	version, dirty, err := db.Version(ctx, testDSN, payment.ModuleName)
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
	svc, prov := yeniServis(t)
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
	assert.Equal(t, testTutar, authorized.AuthorizedAmount)

	saglayiciOturum, err := prov.GetSession(ctx, ses.ExternalID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionAuthorized, saglayiciOturum.Status,
		"sağlayıcının defteri de yetkilendirilmiş olmalı")

	guncelKol, err = svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionAuthorized, guncelKol.Status)
	assert.Equal(t, testTutar, guncelKol.AuthorizedAmount)

	pay, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)
	assert.Equal(t, testTutar, pay.Amount)
	assert.Equal(t, testPara, pay.CurrencyCode)

	saglayiciOturum, err = prov.GetSession(ctx, ses.ExternalID)
	require.NoError(t, err)
	assert.Equal(t, testTutar, saglayiciOturum.CapturedAmount)

	guncelKol, err = svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionCaptured, guncelKol.Status)

	refund, err := svc.RefundPayment(ctx, pay.ID, testTutar/2, "kısmi iade")
	require.NoError(t, err)
	assert.Equal(t, testTutar/2, refund.Amount)

	guncelKol, err = svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionPartiallyRefunded, guncelKol.Status)

	_, err = svc.RefundPayment(ctx, pay.ID, 0, "kalan iade")
	require.NoError(t, err)

	guncelKol, err = svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionRefunded, guncelKol.Status)
	assert.Equal(t, testTutar, guncelKol.RefundedAmount)

	saglayiciOturum, err = prov.GetSession(ctx, ses.ExternalID)
	require.NoError(t, err)
	assert.Equal(t, testTutar, saglayiciOturum.RefundedAmount,
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
	assert.Equal(t, testTutar, guncelKol.AuthorizedAmount,
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
	svc, _ := yeniServis(t)
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
	svc, prov := yeniServis(t)
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
	svc, prov := yeniServis(t)
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
	svc, _ := yeniServis(t)
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
	svc, _ := yeniServis(t)
	col := yeniKoleksiyon(ctx, t, svc)
	ses, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: "restart-" + col.ID,
	})
	require.NoError(t, err)

	// "Süreç yeniden başladı": tamamen YENİ bir sağlayıcı ve servis örneği.
	yenidenSvc, yenidenProv := yeniServis(t)

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
	svc, prov := yeniServis(t)
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

// TestInteropUctanUcaAkisGercekVeritabaninda saga'nın kullanacağı İLKEL
// yüzeyin gerçek veritabanı üzerinde çalıştığını doğrular.
//
// Modüller arası uyum derleyici tarafından denetlenemez (ADR 0001'in kabul
// edilen bedeli); bu yüzden yüzeyin gerçek bağımlılıklarla koştuğu bir
// entegrasyon testi ZORUNLUDUR.
func TestInteropUctanUcaAkisGercekVeritabaninda(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)
	iop := service.NewInterop(svc)

	colID, err := iop.CreateCollection(ctx, testReferans, testPara, testTutar)
	require.NoError(t, err)

	sesID, err := iop.OpenSession(ctx, colID, manual.ID, "interop-"+colID)
	require.NoError(t, err)

	durum, bloke, err := iop.Authorize(ctx, sesID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionAuthorized.String(), durum)
	assert.Equal(t, testTutar, bloke, "yüzey bloke edilen TUTARI da taşımalı")

	payID, err := iop.Capture(ctx, sesID, 0)
	require.NoError(t, err)

	kolDurum, kolTutar, _, kolTahsil, _, err := iop.Collection(ctx, colID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionCaptured.String(), kolDurum)
	assert.Equal(t, testTutar, kolTutar)
	assert.Equal(t, testTutar, kolTahsil, "saga ödemenin TAM olduğunu sayıdan doğrulayabilmeli")

	refundID, err := iop.Refund(ctx, payID, 0, "interop iadesi")
	require.NoError(t, err)
	assert.NotEmpty(t, refundID)

	kolDurum, _, _, _, kolIade, err := iop.Collection(ctx, colID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionRefunded.String(), kolDurum)
	assert.Equal(t, testTutar, kolIade)
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
	svc, _ := yeniServis(t)
	iop := service.NewInterop(svc)

	colID, err := iop.CreateCollection(ctx, testReferans, testPara, testTutar)
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
	assert.Equal(t, testTutar, kolTutar)
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
	svc, _ := yeniServis(t)
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
	svc, prov := yeniServis(t)
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
	svc, _ := yeniServis(t)
	iop := service.NewInterop(svc)

	colID, err := iop.CreateCollection(ctx, testReferans, testPara, testTutar)
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
	svc, _ := yeniServis(t)
	col := yeniKoleksiyon(ctx, t, svc)
	p := service.NewQueryProvider(svc)

	records, err := p.FetchByIDs(ctx, []string{col.ID, "paycol_YOK"},
		[]string{service.FieldID, service.FieldReference, service.FieldAmount, service.FieldStatus})

	require.NoError(t, err)
	require.Len(t, records, 1, "bulunamayan kimlik için kayıt DÖNMEZ")
	assert.Equal(t, col.ID, records[0][service.FieldID])
	assert.Equal(t, testReferans, records[0][service.FieldReference])
	assert.Equal(t, testTutar, records[0][service.FieldAmount])
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
	svc, _ := yeniServis(t)
	col := yeniKoleksiyon(ctx, t, svc)

	yarim := testTutar / 2
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
	assert.Equal(t, testTutar, guncelKol.AuthorizedAmount,
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
	assert.Equal(t, testTutar, guncelKol.CapturedAmount,
		"tahsil edilen tutar TEK tahsilat kadar olmalı, katları değil")
}
