//go:build integration

// Bu dosyadaki testler gerçek bir PostgreSQL örneği (dolayısıyla Docker)
// gerektirir; `make test` hızlı kalsın diye `integration` etiketiyle
// ayrılmıştır. Çalıştırmak için: make test-integration
//
// Birim testleri sahte bir depo ile servisin KARARLARINI kanıtlar (oran
// seçimi, yuvarlama yönü, taşma, hata sınıflandırması). Buradaki testler
// kararların dayandığı ZEMİNİ kanıtlar: migration'ın VERİ VARKEN geri
// alınabildiğini, kısmi benzersiz indekslerin ikinci kök bölgeyi ve ikinci
// varsayılan oranı gerçekten reddettiğini, bileşik foreign key'in eyalet-ülke
// tutarsızlığını engellediğini ve eşzamanlı iki isteğin kuralı birlikte
// delemediğini. Sonuncular yalnızca burada, gerçek kısıtlar ve gerçek satır
// kilitleri üzerinde sınanabilir — sahte bir depo kendi yazdığı kuralı
// doğrulayamaz.
package tax_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/tax"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
	"github.com/bdrtr/gobit/internal/modules/tax/repository"
	"github.com/bdrtr/gobit/internal/modules/tax/service"
)

const postgresImage = "postgres:16-alpine"

// modulTablolari modülün sahip olduğu tablolardır; migration testleri bu
// listeyi kullanır.
var modulTablolari = []string{"tax_region", "tax_rate", "tax_rate_rule"}

var (
	// testPool tüm testlerin paylaştığı havuzdur.
	testPool *db.Pool
	// testDSN migration çağrıları için bağlantı adresidir.
	testDSN string
	// ulkeSayaci testler arasında BENZERSİZ ülke kodu üretir.
	//
	// Zorunludur: bir ülkenin en fazla bir kök vergi bölgesi olabilir ve tüm
	// testler aynı veritabanını paylaşır. Sabit bir kod kullanan iki test
	// birbirinin kısıtına takılır ve hangisinin gerçekten kuralı sınadığı
	// belirsizleşirdi.
	ulkeSayaci atomic.Int64
)

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres tek bir Postgres konteyneri kaldırıp tüm testleri onun
// üzerinde çalıştırır. os.Exit defer'ları atladığı için ayrı fonksiyondadır.
func runWithPostgres(m *testing.M) int {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_tax_test"),
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
	// Eşzamanlılık testi onlarca goroutine'i aynı anda koşturur; her işlem bir
	// bağlantı tuttuğu için havuz varsayılandan geniş açılır.
	cfg.MaxConns = 24
	testPool, err = db.New(ctx, cfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bağlantı havuzu açılamadı: %v\n", err)
		return 1
	}
	defer testPool.Close()

	if err := db.Migrate(ctx, testDSN, tax.New(nil).Migrations(), tax.ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "migration uygulanamadı: %v\n", err)
		return 1
	}

	return m.Run()
}

// yeniServis gerçek depo üzerinde çalışan bir servis kurar.
func yeniServis(t *testing.T) *service.Service {
	t.Helper()

	return service.New(repository.New(testPool.Pool()), service.Options{})
}

// benzersizUlke bu koşuda başka hiçbir testin kullanmadığı bir ülke kodu
// üretir.
//
// Kod ISO 3166-1'de tanımlı olmak ZORUNDA DEĞİLDİR: bu modül yalnızca BİÇİMİ
// doğrular ve ülke listesi region modülünün verisidir (tax onu import edemez,
// ADR 0001).
func benzersizUlke(t *testing.T) string {
	t.Helper()

	n := ulkeSayaci.Add(1)
	const harfler = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	kod := string(harfler[(n/26)%26]) + string(harfler[n%26])
	require.Len(t, kod, 2)
	return kod
}

// yeniKokBolge benzersiz bir ülke için kök vergi bölgesi oluşturur.
func yeniKokBolge(ctx context.Context, t *testing.T, svc *service.Service) models.TaxRegion {
	t.Helper()

	bolge, err := svc.CreateTaxRegion(ctx, service.CreateTaxRegionInput{
		CountryCode: benzersizUlke(t),
	})
	require.NoError(t, err)
	return bolge
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

// sayim tek sütunlu bir sayım sorgusunu çalıştırır.
func sayim(ctx context.Context, t *testing.T, sql string, args ...any) int64 {
	t.Helper()

	var count int64
	require.NoError(t, testPool.Pool().QueryRow(ctx, sql, args...).Scan(&count))
	return count
}

// TestMigrationGeriAlinabilir migration'ın VERİ VARKEN geri alınabildiğini
// doğrular (plan Bölüm 8).
//
// Geri alma modülün GERÇEK durumu üzerinde koşar: bölge, oran ve kural
// satırları YERİNDE bırakılır. Şart bilinçlidir — modülün tek silme yolu SOFT
// delete'tir, yani operatör API'den her kaydı silse bile satırlar tabloda kalır
// ve modül içi foreign key'leri (kural -> oran -> bölge, eyalet -> kök) tutmaya
// devam eder. Satırları ham SQL ile süpürmek, modülün API'siyle ULAŞILAMAZ bir
// ön koşul kurar ve hatanın tetikleyicisini testten çıkarırdı.
//
// internal/arch'taki TestMigrationsCanReallyBeRolledBack aynı gidiş dönüşü
// BOŞ şema üzerinde koşar; veriye bağlı geri alma hatası ancak burada yakalanır.
func TestMigrationGeriAlinabilir(t *testing.T) {
	ctx := context.Background()

	for _, table := range modulTablolari {
		require.True(t, tabloVar(ctx, t, table), "%s başlangıçta var olmalı", table)
	}

	svc := yeniServis(t)
	kok := yeniKokBolge(ctx, t, svc)
	eyalet, err := svc.CreateTaxRegion(ctx, service.CreateTaxRegionInput{
		CountryCode: kok.CountryCode, ProvinceCode: "34", ParentID: kok.ID,
	})
	require.NoError(t, err)

	oran, err := svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: eyalet.ID, Name: "İndirimli", RateBps: 100,
	})
	require.NoError(t, err)
	_, err = svc.CreateRateRule(ctx, service.CreateRateRuleInput{
		TaxRateID: oran.ID, Reference: "product", ReferenceID: "prod_1",
	})
	require.NoError(t, err)

	// Yumuşak silinmiş bir kayıt da bırakılır: satır tabloda KALIR ve foreign
	// key'i canlısı kadar sıkı tutar.
	silinecek, err := svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: kok.ID, Name: "Silinecek", RateBps: 500,
	})
	require.NoError(t, err)
	require.NoError(t, svc.DeleteTaxRate(ctx, silinecek.ID))
	require.Equal(t, int64(1), sayim(ctx, t,
		`SELECT count(*) FROM tax_rate WHERE id = $1 AND deleted_at IS NOT NULL`, silinecek.ID),
		"yumuşak silme satırı tabloda BIRAKIR")

	src := tax.New(nil).Migrations()

	require.NoError(t, db.MigrateDown(ctx, testDSN, src, tax.ModuleName, 0),
		"down başarısız — bu, modülün bir daha migrate EDİLEMEMESİ demektir")
	for _, table := range modulTablolari {
		assert.False(t, tabloVar(ctx, t, table), "%s geri alma sonrası kalmamalı", table)
	}

	require.NoError(t, db.Migrate(ctx, testDSN, src, tax.ModuleName))
	for _, table := range modulTablolari {
		assert.True(t, tabloVar(ctx, t, table), "%s yeniden uygulanmalı", table)
	}

	version, dirty, err := db.Version(ctx, testDSN, tax.ModuleName)
	require.NoError(t, err)
	assert.False(t, dirty, "yarıda kalmış migration olmamalı")
	assert.Equal(t, uint(1), version)
	assert.Zero(t, sayim(ctx, t, `SELECT count(*) FROM tax_region`),
		"şema düşüp yeniden kurulduğu için hiçbir bölge kalmamalı")
}

// TestCrossModuleForeignKeyYok Prensip 2.2'yi GERÇEK şema üzerinde doğrular.
//
// internal/arch aynı kuralı SQL metnini tarayarak denetler; bu test kısıtların
// veritabanında gerçekten kurulduğunu ve hedeflerinin modül içinde kaldığını
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
	assert.Equal(t, 3, sayi,
		"bölge->bölge (eyalet), oran->bölge ve kural->oran bağları kurulmuş olmalı")
}

// TestIkinciKokBolgeReddedilir kısmi benzersiz indeksin çalıştığını doğrular.
func TestIkinciKokBolgeReddedilir(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	kok := yeniKokBolge(ctx, t, svc)

	_, err := svc.CreateTaxRegion(ctx, service.CreateTaxRegionInput{CountryCode: kok.CountryCode})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
	assert.Equal(t, service.CodeRootExists, errors.CodeOf(err))

	// Silinen bir kökten sonra yenisi açılabilmelidir; aksi hâlde silme ülkeyi
	// kalıcı olarak yapılandırılamaz bırakırdı (kısmi indeks deleted_at IS NULL
	// süzer).
	require.NoError(t, svc.DeleteTaxRegion(ctx, kok.ID))
	_, err = svc.CreateTaxRegion(ctx, service.CreateTaxRegionInput{CountryCode: kok.CountryCode})
	require.NoError(t, err)
}

// TestEszamanliKokBolgeTekKazanan servis denetimini birlikte geçen iki isteğin
// veritabanı kısıtına takıldığını doğrular.
//
// Servis "önce oku, sonra yaz" yapar ve iki eşzamanlı istek o denetimi birlikte
// geçebilir; son savunma kısmi benzersiz indekstir ve YALNIZCA gerçek
// veritabanında sınanabilir.
func TestEszamanliKokBolgeTekKazanan(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	ulke := benzersizUlke(t)

	const istekSayisi = 8
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		kazanan  []string
		kodlar   []string
		digerErr []error
	)

	wg.Add(istekSayisi)
	for range istekSayisi {
		go func() {
			defer wg.Done()

			bolge, err := svc.CreateTaxRegion(ctx, service.CreateTaxRegionInput{CountryCode: ulke})

			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				kazanan = append(kazanan, bolge.ID)
			case errors.IsConflict(err):
				kodlar = append(kodlar, errors.CodeOf(err))
			default:
				digerErr = append(digerErr, err)
			}
		}()
	}
	wg.Wait()

	assert.Empty(t, digerErr, "beklenmeyen hata: %v", digerErr)
	require.Len(t, kazanan, 1, "yarışı tam olarak bir istek kazanmalı")
	assert.Len(t, kodlar, istekSayisi-1, "kaybedenlerin hepsi çakışma almalı")
	for _, kod := range kodlar {
		// Kaybeden istek hangi yoldan düşerse düşsün ("önce oku" denetimi ya
		// da benzersiz indeks) aynı kodu almalıdır; ayrıntı için
		// TestYarisiKaybedenKokAyniKoduAlir.
		assert.Equal(t, service.CodeRootExists, kod)
	}
	assert.Equal(t, int64(1), sayim(ctx, t,
		`SELECT count(*) FROM tax_region WHERE country_code = $1 AND parent_id IS NULL AND deleted_at IS NULL`,
		ulke))
}

// TestYarisiKaybedenKokAyniKoduAlir "önce oku" denetimini geçen isteğin,
// veritabanı indeksine çarptığında da AYNI hata kodunu aldığını doğrular.
//
// Yarış zamanlamaya değil KİLİDE bağlanır: rakip satır açık bir işlemde
// yazılır ve commit EDİLMEZ. Servisin okuması onu göremez (read committed), ama
// INSERT'i benzersiz indekste ona çarpar ve o işlem bitene kadar bekler. Böylece
// yarışın kaybeden ucu her koşuda kesin olarak sınanır; TestEszamanliKokBolge-
// TekKazanan aynı kodu doğrular ama kaybedenlerin hangi yoldan düştüğünü garanti
// edemez.
func TestYarisiKaybedenKokAyniKoduAlir(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	ulke := benzersizUlke(t)

	tx, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `INSERT INTO tax_region (id, country_code) VALUES ($1, $2)`,
		models.NewTaxRegionID(time.Now()), ulke)
	require.NoError(t, err)

	sonuc := make(chan error, 1)
	go func() {
		_, createErr := svc.CreateTaxRegion(ctx, service.CreateTaxRegionInput{CountryCode: ulke})
		sonuc <- createErr
	}()

	// Commit, isteğin indekste BEKLEDİĞİ görülmeden yapılmamalıdır: erken bir
	// commit isteği "önce oku" denetimine düşürür ve yarış yolu hiç sınanmazdı.
	require.Eventually(t, func() bool {
		var bekleyen int64
		scanErr := testPool.Pool().QueryRow(ctx,
			`SELECT count(*) FROM pg_stat_activity
             WHERE wait_event_type = 'Lock' AND query ILIKE '%INSERT INTO tax_region%'`).
			Scan(&bekleyen)
		return scanErr == nil && bekleyen > 0
	}, 10*time.Second, 20*time.Millisecond,
		"eşzamanlı istek benzersiz indekste beklemeliydi")

	require.NoError(t, tx.Commit(ctx))

	err = <-sonuc
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
	assert.Equal(t, service.CodeRootExists, errors.CodeOf(err),
		"yarışı kaybeden istek, denetime takılan istekle AYNI kodu almalı")
}

// TestSaglayiciKimligiKisiti provider_id'nin veritabanında da kırpılmış ve
// sınırlı tutulduğunu doğrular.
//
// Servis aynı kuralı okunabilir bir hatayla önce uygular; bu test kısıtın
// DOĞRUDAN SQL'e karşı da tuttuğunu gösterir — uygulama katmanı son savunma
// değildir.
func TestSaglayiciKimligiKisiti(t *testing.T) {
	ctx := context.Background()

	for ad, deger := range map[string]string{
		"baştaki boşluk":  " local",
		"sondaki boşluk":  "local ",
		"sınırın üstünde": strings.Repeat("a", 256),
	} {
		t.Run(ad, func(t *testing.T) {
			_, err := testPool.Pool().Exec(ctx,
				`INSERT INTO tax_region (id, country_code, provider_id) VALUES ($1, $2, $3)`,
				models.NewTaxRegionID(time.Now()), benzersizUlke(t), deger)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "tax_region_provider_id_check")
		})
	}

	// Sınırdaki değer ve boş değer serbesttir: kısıt yalnızca kırpılmamışı ve
	// sınırı AŞANI reddeder.
	_, err := testPool.Pool().Exec(ctx,
		`INSERT INTO tax_region (id, country_code, provider_id) VALUES ($1, $2, $3)`,
		models.NewTaxRegionID(time.Now()), benzersizUlke(t), strings.Repeat("a", 255))
	require.NoError(t, err)
}

// TestEyaletUlkeninSaglayicisiniDevralir devralmanın GERÇEK bölge sorgusu
// üzerinde çalıştığını doğrular.
//
// Birim testi kuralı sahte depoyla kanıtlar; burada kanıtlanan şey zincirin
// SQL'den en özelden genele sıralı gelmesidir — sıra tersine dönseydi eyaletin
// boş provider_id'si ülkeninkini EZER ve hesap yine yanlış otoriteye giderdi.
func TestEyaletUlkeninSaglayicisiniDevralir(t *testing.T) {
	ctx := context.Background()

	repo := repository.New(testPool.Pool())
	kayit := service.NewProviderRegistry()
	require.NoError(t, kayit.Register(service.NewLocalProvider(repo)))
	require.NoError(t, kayit.Register(&sahteSaglayici{kimlik: "avalara"}))
	svc := service.New(repo, service.Options{Providers: kayit})

	kok, err := svc.CreateTaxRegion(ctx, service.CreateTaxRegionInput{
		CountryCode: benzersizUlke(t), ProviderID: "avalara",
	})
	require.NoError(t, err)

	// Eyalet TEK BİR İSTİSNA için açılır ve sağlayıcısı boş bırakılır; kendi
	// varsayılan oranı vardır, yani yerele düşen bir hesap 725 bulurdu.
	eyalet, err := svc.CreateTaxRegion(ctx, service.CreateTaxRegionInput{
		CountryCode: kok.CountryCode, ProvinceCode: "CA", ParentID: kok.ID,
	})
	require.NoError(t, err)
	_, err = svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: eyalet.ID, Name: "Eyalet", RateBps: 725, IsDefault: true,
	})
	require.NoError(t, err)

	sonuc, err := svc.CalculateTax(ctx, service.CalculateTaxInput{
		CountryCode:  kok.CountryCode,
		ProvinceCode: "CA",
		Items:        []service.TaxableItem{{ID: "li_1", Amount: 10_000}},
	})
	require.NoError(t, err)

	assert.Equal(t, "avalara", sonuc.ProviderID,
		"eyaletin boş provider_id'si ülkenin dış otoritesini yerele DÜŞÜRMEMELİ")
	require.Len(t, sonuc.Items, 1)
	assert.Equal(t, int64(999), sonuc.Items[0].TaxAmount, "hesabı dış sağlayıcı yapmalı")
}

// sahteSaglayici sabit bir sonuç dönen dış vergi sağlayıcısıdır.
//
// Yerel hesabın ÜRETEMEYECEĞİ bir tutar döner: sonuçtaki tutar, hesabı kimin
// yaptığının kanıtıdır.
type sahteSaglayici struct {
	kimlik string
}

// ID sağlayıcının kimliğini döner.
func (p *sahteSaglayici) ID() string { return p.kimlik }

// Calculate her kaleme sabit bir vergi yazar.
func (p *sahteSaglayici) Calculate(
	_ context.Context,
	in service.ProviderInput,
) (service.ProviderResult, error) {
	out := service.ProviderResult{
		Items:    make([]service.ProviderItemTax, 0, len(in.Items)),
		Shipping: service.ProviderItemTax{ID: service.ShippingLineID},
	}
	for i := range in.Items {
		out.Items = append(out.Items, service.ProviderItemTax{
			ID: in.Items[i].ID, RateBps: 1000, TaxAmount: 999,
		})
	}
	return out, nil
}

// TestEyaletKokunUlkesiniDegistiremez bileşik foreign key'i doğrular.
//
// Servis aynı denetimi okunabilir bir hatayla önce yapar; bu test kısıtın
// DOĞRUDAN SQL'e karşı da tuttuğunu gösterir.
func TestEyaletKokunUlkesiniDegistiremez(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	kok := yeniKokBolge(ctx, t, svc)
	baskaUlke := benzersizUlke(t)

	_, err := testPool.Pool().Exec(ctx,
		`INSERT INTO tax_region (id, country_code, province_code, parent_id)
         VALUES ($1, $2, 'XX', $3)`,
		models.NewTaxRegionID(kok.CreatedAt), baskaUlke, kok.ID)
	require.Error(t, err, "kökün ülkesinden farklı bir eyalet yazılamamalı")
	assert.Contains(t, err.Error(), "tax_region_parent_fk")
}

// TestYarimHiyerarsiVeritabanindaReddedilir CHECK kısıtını doğrular.
func TestYarimHiyerarsiVeritabanindaReddedilir(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	kok := yeniKokBolge(ctx, t, svc)

	t.Run("ebeveynsiz eyalet", func(t *testing.T) {
		_, err := testPool.Pool().Exec(ctx,
			`INSERT INTO tax_region (id, country_code, province_code) VALUES ($1, $2, 'XX')`,
			models.NewTaxRegionID(kok.CreatedAt), benzersizUlke(t))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "tax_region_hierarchy_check")
	})

	t.Run("eyalet kodu taşıyan kök", func(t *testing.T) {
		_, err := testPool.Pool().Exec(ctx,
			`INSERT INTO tax_region (id, country_code, parent_id) VALUES ($1, $2, $3)`,
			models.NewTaxRegionID(kok.CreatedAt), kok.CountryCode, kok.ID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "tax_region_hierarchy_check")
	})
}

// TestIkinciVarsayilanOranReddedilir bölge başına tek varsayılan kuralının
// veritabanında da tuttuğunu doğrular.
func TestIkinciVarsayilanOranReddedilir(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	kok := yeniKokBolge(ctx, t, svc)

	ilk, err := svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: kok.ID, Name: "KDV", RateBps: 2000, IsDefault: true,
	})
	require.NoError(t, err)

	_, err = svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: kok.ID, Name: "İkinci", RateBps: 1000, IsDefault: true,
	})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
	assert.Equal(t, service.CodeDefaultExists, errors.CodeOf(err))

	// Doğrudan SQL de reddedilmeli: servis denetimi son savunma değildir.
	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO tax_rate (id, tax_region_id, name, rate_bps, is_default)
         VALUES ($1, $2, 'Ham', 1000, TRUE)`,
		models.NewTaxRateID(kok.CreatedAt), kok.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tax_rate_default_uniq")

	// Varsayılan silindikten sonra yenisi yazılabilmelidir.
	require.NoError(t, svc.DeleteTaxRate(ctx, ilk.ID))
	_, err = svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: kok.ID, Name: "Yeni", RateBps: 1800, IsDefault: true,
	})
	require.NoError(t, err)
}

// TestOranAralikKisiti rate_bps CHECK'ini doğrular.
func TestOranAralikKisiti(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	kok := yeniKokBolge(ctx, t, svc)

	for _, bps := range []int32{-1, 10_001} {
		_, err := testPool.Pool().Exec(ctx,
			`INSERT INTO tax_rate (id, tax_region_id, name, rate_bps) VALUES ($1, $2, 'Ham', $3)`,
			models.NewTaxRateID(kok.CreatedAt), kok.ID, bps)
		require.Error(t, err, "oran: %d", bps)
		assert.Contains(t, err.Error(), "tax_rate_bps_check")
	}
}

// TestVarsayilanOranaKuralEklenemez kapsam kuralının gerçek kilit altında
// tuttuğunu doğrular.
func TestVarsayilanOranaKuralEklenemez(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	kok := yeniKokBolge(ctx, t, svc)

	varsayilan, err := svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: kok.ID, Name: "KDV", RateBps: 2000, IsDefault: true,
	})
	require.NoError(t, err)

	_, err = svc.CreateRateRule(ctx, service.CreateRateRuleInput{
		TaxRateID: varsayilan.ID, Reference: "product", ReferenceID: "prod_1",
	})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))

	// Ters yön: kurallı bir oran varsayılan YAPILAMAZ.
	kurallı, err := svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: kok.ID, Name: "İndirimli", RateBps: 100,
	})
	require.NoError(t, err)
	_, err = svc.CreateRateRule(ctx, service.CreateRateRuleInput{
		TaxRateID: kurallı.ID, Reference: "product", ReferenceID: "prod_1",
	})
	require.NoError(t, err)

	dogru := true
	_, err = svc.UpdateTaxRate(ctx, kurallı.ID, service.UpdateTaxRateInput{IsDefault: &dogru})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
}

// TestKuralTekilligi aynı referansın iki kez yazılamayacağını doğrular.
func TestKuralTekilligi(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	kok := yeniKokBolge(ctx, t, svc)
	oran, err := svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: kok.ID, Name: "İndirimli", RateBps: 100,
	})
	require.NoError(t, err)

	in := service.CreateRateRuleInput{
		TaxRateID: oran.ID, Reference: "product", ReferenceID: "prod_1",
	}
	kural, err := svc.CreateRateRule(ctx, in)
	require.NoError(t, err)

	_, err = svc.CreateRateRule(ctx, in)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))

	// Farklı referans TÜRÜ aynı kimlikle serbesttir: ürün ile ürün tipi ayrı
	// ad uzaylarıdır.
	_, err = svc.CreateRateRule(ctx, service.CreateRateRuleInput{
		TaxRateID: oran.ID, Reference: "product_type", ReferenceID: "prod_1",
	})
	require.NoError(t, err)

	// Silinen kuralın referansı yeniden yazılabilmelidir.
	require.NoError(t, svc.DeleteRateRule(ctx, kural.ID))
	_, err = svc.CreateRateRule(ctx, in)
	require.NoError(t, err)
}

// TestGercekHesaplama vergi hesabının gerçek veritabanı üzerinde uçtan uca
// çalıştığını doğrular.
//
// Birim testleri aynı dalları sahte depoyla kanıtlar; burada kanıtlanan şey
// SORGULARIN doğruluğudur: bölge zincirinin sıralı çözülmesi, oranların ve
// kuralların toplu okunması ve soft delete süzgeci.
func TestGercekHesaplama(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	kok := yeniKokBolge(ctx, t, svc)
	eyalet, err := svc.CreateTaxRegion(ctx, service.CreateTaxRegionInput{
		CountryCode: kok.CountryCode, ProvinceCode: "34", ParentID: kok.ID,
	})
	require.NoError(t, err)

	_, err = svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: kok.ID, Name: "KDV", RateBps: 2000, IsDefault: true,
	})
	require.NoError(t, err)

	indirimli, err := svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: eyalet.ID, Name: "Kitap", RateBps: 100,
	})
	require.NoError(t, err)
	_, err = svc.CreateRateRule(ctx, service.CreateRateRuleInput{
		TaxRateID: indirimli.ID, Reference: "product", ReferenceID: "prod_kitap",
	})
	require.NoError(t, err)

	in := service.CalculateTaxInput{
		CountryCode:  kok.CountryCode,
		ProvinceCode: "34",
		Items: []service.TaxableItem{
			{ID: "li_kitap", ProductID: "prod_kitap", Amount: 10_000},
			{ID: "li_diger", ProductID: "prod_diger", Amount: 1_999},
		},
		Shipping: service.ShippingInput{OptionID: "sopt_1", Amount: 2_500},
	}

	sonuc, err := svc.CalculateTax(ctx, in)
	require.NoError(t, err)

	require.True(t, sonuc.RegionFound)
	assert.Equal(t, eyalet.ID, sonuc.RegionID, "en özel bölge dönmeli")
	assert.Equal(t, service.LocalProviderID, sonuc.ProviderID)
	require.Len(t, sonuc.Items, 2)

	assert.Equal(t, int32(100), sonuc.Items[0].RateBps, "eyaletin kuralı eşleşmeli")
	assert.Equal(t, int64(100), sonuc.Items[0].TaxAmount)
	assert.Equal(t, int32(2000), sonuc.Items[1].RateBps, "eşleşmeyen kalem ülkeye düşmeli")
	assert.Equal(t, int64(399), sonuc.Items[1].TaxAmount, "1999 × %%20 = 399,8 -> 399 (AŞAĞI)")
	assert.Equal(t, int64(0), sonuc.Shipping.TaxAmount, "kargo istenmedikçe vergilenmez")
	assert.Equal(t, int64(499), sonuc.TaxTotal)

	// Kargo açıkça istendiğinde varsayılan orana düşer.
	in.Shipping.Taxable = true
	sonuc, err = svc.CalculateTax(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, int64(500), sonuc.Shipping.TaxAmount, "2500 × %%20")
	assert.Equal(t, int64(999), sonuc.TaxTotal)

	// Eyalet silinince zincir tek halkaya iner ve kitap da ülke oranına düşer.
	require.NoError(t, svc.DeleteTaxRegion(ctx, eyalet.ID))
	sonuc, err = svc.CalculateTax(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, kok.ID, sonuc.RegionID)
	assert.Equal(t, int32(2000), sonuc.Items[0].RateBps,
		"silinen eyaletin oranı hesaba GİRMEMELİ")
}

// TestHesapYapilandirilmamisUlkedeSifirDoner bölgesiz ülkenin gerçek
// veritabanında da hata değil sıfır ürettiğini doğrular.
func TestHesapYapilandirilmamisUlkedeSifirDoner(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	sonuc, err := svc.CalculateTax(ctx, service.CalculateTaxInput{
		CountryCode: benzersizUlke(t),
		Items:       []service.TaxableItem{{ID: "li_1", Amount: 10_000}},
	})
	require.NoError(t, err)
	assert.False(t, sonuc.RegionFound)
	assert.Equal(t, int64(0), sonuc.TaxTotal)
}

// TestBolgeSilmeAgaciKapsar silmenin alt bölgeleri, oranları ve kuralları
// GERÇEKTEN kapsadığını doğrular.
func TestBolgeSilmeAgaciKapsar(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	kok := yeniKokBolge(ctx, t, svc)
	eyalet, err := svc.CreateTaxRegion(ctx, service.CreateTaxRegionInput{
		CountryCode: kok.CountryCode, ProvinceCode: "35", ParentID: kok.ID,
	})
	require.NoError(t, err)
	oran, err := svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: eyalet.ID, Name: "İndirimli", RateBps: 100,
	})
	require.NoError(t, err)
	_, err = svc.CreateRateRule(ctx, service.CreateRateRuleInput{
		TaxRateID: oran.ID, Reference: "product", ReferenceID: "prod_1",
	})
	require.NoError(t, err)

	require.NoError(t, svc.DeleteTaxRegion(ctx, kok.ID))

	assert.Zero(t, sayim(ctx, t,
		`SELECT count(*) FROM tax_region WHERE country_code = $1 AND deleted_at IS NULL`,
		kok.CountryCode), "kök ve eyalet birlikte silinmeli")
	assert.Zero(t, sayim(ctx, t,
		`SELECT count(*) FROM tax_rate WHERE id = $1 AND deleted_at IS NULL`, oran.ID),
		"alt bölgenin oranı da silinmeli")
	assert.Zero(t, sayim(ctx, t,
		`SELECT count(*) FROM tax_rate_rule WHERE tax_rate_id = $1 AND deleted_at IS NULL`, oran.ID),
		"oranın kuralları da silinmeli")

	// Silinmiş satırlar TABLODA durur: yumuşak silme kaydı yok etmez.
	assert.Equal(t, int64(2), sayim(ctx, t,
		`SELECT count(*) FROM tax_region WHERE country_code = $1`, kok.CountryCode))
}

// TestInteropYuzeyiGercekVeriyleCalisir modüller arası yüzeyi gerçek
// veritabanında doğrular.
func TestInteropYuzeyiGercekVeriyleCalisir(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	interop := service.NewInterop(svc)

	kok := yeniKokBolge(ctx, t, svc)
	_, err := svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: kok.ID, Name: "KDV", RateBps: 1800, IsDefault: true,
	})
	require.NoError(t, err)

	oran, bulundu, err := interop.RateForCountry(ctx, kok.CountryCode)
	require.NoError(t, err)
	assert.True(t, bulundu)
	assert.Equal(t, int32(1800), oran)

	raw, err := interop.CalculateTaxJSON(ctx, []byte(
		`{"country_code":"`+kok.CountryCode+`","items":[{"id":"li_1","amount":10000}]}`))
	require.NoError(t, err)
	assert.JSONEq(t,
		`{"region_id":"`+kok.ID+`","region_found":true,"provider_id":"local","tax_total":1800,
		  "items":[{"id":"li_1","rate_id":"`+ilkOranID(ctx, t, kok.ID)+`","rate_bps":1800,
		            "taxable_amount":10000,"tax_amount":1800}],
		  "shipping":{"id":"_shipping","rate_id":"","rate_bps":0,"taxable_amount":0,"tax_amount":0}}`,
		string(raw))
}

// ilkOranID bir bölgenin varsayılan oranının kimliğini döner.
func ilkOranID(ctx context.Context, t *testing.T, regionID string) string {
	t.Helper()

	var id string
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT id FROM tax_rate WHERE tax_region_id = $1 AND is_default AND deleted_at IS NULL`,
		regionID).Scan(&id))
	return id
}

// TestModulKaydiCozulebilir modülün container'a kaydettiği adların gerçekten
// çözülebildiğini ve beklenen arayüzleri karşıladığını doğrular.
//
// ADR 0001'in bedeli buydu: sağlayıcı ile tüketici arasında derleme zamanı
// bağı yoktur, uyumsuzluk ancak çözüm anında görünür. Bu test o anı erkene
// çeker.
func TestModulKaydiCozulebilir(t *testing.T) {
	ctx := context.Background()
	c := container.New(nil)
	require.NoError(t, c.Provide("core.db", testPool))

	mod := tax.New(nil)
	require.NoError(t, mod.Register(ctx, c))

	svc, err := container.Resolve[*service.Service](c, "tax.service")
	require.NoError(t, err, "servis, sabit adıyla çözülebilmeli")
	require.NotNil(t, svc)
	assert.Equal(t, "tax.service", tax.ServiceName,
		"servis adı değişirse tüketici modüller onu bulamaz")

	// Sepet akışının (internal/workflows/cart) yazacağı DAR arayüz burada
	// çözülür; tax import EDİLMEDEN yalnızca imzayla eşleşir (ADR 0001/0006).
	// json.RawMessage BİREBİR kullanılmalıdır: []byte ile aynı temel tipe
	// sahip olsa da adlandırılmış bir tiptir ve container'ın tip denetimi imza
	// EŞİTLİĞİ arar. Tüketici tarafında "[]byte" yazmak, çözüm anında
	// uyumsuzluk hatası demektir.
	type taxCalculator interface {
		CalculateTaxJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error)
		RateForCountry(ctx context.Context, countryCode string) (int32, bool, error)
	}
	hesaplayici, err := container.Resolve[taxCalculator](c, tax.InteropName)
	require.NoError(t, err, "dar tüketici arayüzü interop yüzeyini karşılamalı")

	registry, err := container.Resolve[*service.ProviderRegistry](c, tax.ProvidersName)
	require.NoError(t, err, "sağlayıcı kaydı adıyla çözülebilmeli")
	assert.Equal(t, []string{service.LocalProviderID}, registry.IDs())

	// Ad, ADR 0004'ün kuralıyla ELDE hesaplanır: sağlayıcı "<entity>.query"
	// adıyla aranır. Sabiti kullanmak testi totolojiye çevirirdi.
	provider, err := container.Resolve[query.Provider](c, "tax_region"+query.ProviderSuffix)
	require.NoError(t, err, "Query sağlayıcısı adıyla çözülebilmeli (ADR 0004)")
	assert.Equal(t, "tax_region", provider.Entity(),
		"kayıt adının öneki Entity() ile aynı olmalı")

	kok := yeniKokBolge(ctx, t, svc)
	_, err = svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
		TaxRegionID: kok.ID, Name: "KDV", RateBps: 2000, IsDefault: true,
	})
	require.NoError(t, err)

	oran, bulundu, err := hesaplayici.RateForCountry(ctx, kok.CountryCode)
	require.NoError(t, err)
	assert.True(t, bulundu)
	assert.Equal(t, int32(2000), oran)

	// Asıl kanıt: çekirdeğin Query katmanı, modülü hiç tanımadan yalnızca
	// entity adıyla sağlayıcıyı bulup veriyi çekebilmeli.
	records, err := query.New(nil, c, nil).Graph(ctx, query.GraphSpec{
		Entity:  "tax_region",
		Filters: map[string]any{"id": kok.ID},
	})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, kok.ID, records[0][query.IDField])
	assert.Equal(t, kok.CountryCode, records[0]["country_code"])

	rates, ok := records[0]["rates"].([]map[string]any)
	require.True(t, ok, "oranlar kayıtla birlikte dönmeli: %#v", records[0]["rates"])
	require.Len(t, rates, 1)
	assert.Equal(t, int32(2000), rates[0]["rate_bps"])
}
