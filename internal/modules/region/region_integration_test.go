//go:build integration

// Bu dosyadaki testler gerçek bir PostgreSQL örneği (dolayısıyla Docker)
// gerektirir; `make test` hızlı kalsın diye `integration` etiketiyle
// ayrılmıştır. Çalıştırmak için: make test-integration
//
// Birim testleri sahte bir depo ile servisin KARARLARINI kanıtlar. Buradaki
// testler kararların dayandığı ZEMİNİ kanıtlar: migration'ın geri
// alınabildiğini, TOHUM VERİSİNİN gerçekten yüklendiğini, kısıtların
// uygulandığını ve "bir ülke en fazla bir bölgeye ait olabilir" kuralının
// eşzamanlı iki istekte de tuttuğunu. Sonuncusu yalnızca burada, gerçek
// goroutine'lerle gerçek satır kilitleri üzerinde sınanabilir.
package region_test

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"sync"
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
	"github.com/bdrtr/gobit/internal/modules/region"
	"github.com/bdrtr/gobit/internal/modules/region/models"
	"github.com/bdrtr/gobit/internal/modules/region/repository"
	"github.com/bdrtr/gobit/internal/modules/region/service"
)

const postgresImage = "postgres:16-alpine"

// modulTablolari modülün sahip olduğu tablolardır; migration testleri bu
// listeyi kullanır.
var modulTablolari = []string{"currency", "region", "country"}

// tohumdakiUlkeSayisi ISO 3166-1'de resmen atanmış alpha-2 kodu sayısıdır.
//
// Sabit bilinçlidir: tohum dosyası kazara kırpılırsa ya da bir satır
// kopyalanırken düşerse, sayı testte anında görünür. "Sıfırdan büyük" gibi
// gevşek bir iddia bunu yakalayamazdı.
const tohumdakiUlkeSayisi = 249

// tohumdakiParaBirimiSayisi tohumlanan para birimi sayısıdır.
const tohumdakiParaBirimiSayisi = 41

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
	// Eşzamanlılık testi onlarca goroutine'i aynı anda koşturur; her işlem bir
	// bağlantı tuttuğu için havuz varsayılandan geniş açılır.
	cfg.MaxConns = 24
	testPool, err = db.New(ctx, cfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bağlantı havuzu açılamadı: %v\n", err)
		return 1
	}
	defer testPool.Close()

	if err := db.Migrate(ctx, testDSN, region.New(nil).Migrations(), region.ModuleName); err != nil {
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

// yeniBolge test için benzersiz adlı bir bölge oluşturur.
func yeniBolge(ctx context.Context, t *testing.T, svc *service.Service, currency string) models.Region {
	t.Helper()

	bolge, err := svc.CreateRegion(ctx, service.CreateRegionInput{
		Name:           t.Name() + " " + currency,
		CurrencyCode:   currency,
		AutomaticTaxes: true,
		TaxRate:        2000,
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

// TestMigrationGeriAlinabilir migration'ın uygulanıp geri alınabildiğini ve
// tohumun yeniden yüklendiğini doğrular (plan Bölüm 8).
//
// Geri alma, modülün GERÇEK durumu üzerinde koşar: bölge satırları YERİNDE
// bırakılır. Bu şart bilinçlidir — modülün tek silme yolu SOFT delete'tir,
// yani operatör API'den her bölgeyi silse bile satır tabloda kalır ve tohum
// para birimine giden foreign key'i tutmaya devam eder. Satırları ham SQL ile
// süpürmek (DELETE FROM region) modülün API'siyle ULAŞILAMAZ bir ön koşul
// kurardı ve tam da hatanın tetikleyicisini testten çıkarırdı: tohumun down'ı
// kullanımdaki para birimlerini atlamasa bile test sessizce yeşil kalırdı.
//
// İki durum AYRI AYRI sınanır: canlı bir bölge ve yumuşak silinmiş bir bölge.
// İkincisi ayrıdır çünkü "silinmiş" bölge şemada hâlâ bir satırdır ve foreign
// key'i canlısı kadar sıkı tutar; ikisini tek koşuda birleştirmek hangisinin
// down'ı ayakta tuttuğunu belirsiz bırakırdı.
func TestMigrationGeriAlinabilir(t *testing.T) {
	ctx := context.Background()

	for _, table := range modulTablolari {
		require.True(t, tabloVar(ctx, t, table), "%s başlangıçta var olmalı", table)
	}

	t.Run("canlı bölge yerinde dururken", func(t *testing.T) {
		svc := yeniServis(t)
		bolge := yeniBolge(ctx, t, svc, "TRY")
		_, err := svc.AddCountryToRegion(ctx, bolge.ID, "TR")
		require.NoError(t, err)
		require.Equal(t, int64(1), sayim(ctx, t,
			`SELECT count(*) FROM region WHERE id = $1 AND deleted_at IS NULL`, bolge.ID),
			"geri alma CANLI bir bölge dururken koşmalı")

		geriAlVeYenidenUygula(ctx, t)
	})

	t.Run("yumuşak silinmiş bölge yerinde dururken", func(t *testing.T) {
		svc := yeniServis(t)
		bolge := yeniBolge(ctx, t, svc, "USD")
		require.NoError(t, svc.DeleteRegion(ctx, bolge.ID))
		require.Equal(t, int64(1), sayim(ctx, t,
			`SELECT count(*) FROM region WHERE id = $1 AND deleted_at IS NOT NULL`, bolge.ID),
			"yumuşak silme satırı tabloda BIRAKIR; foreign key bu yüzden hâlâ tutar")

		geriAlVeYenidenUygula(ctx, t)
	})
}

// geriAlVeYenidenUygula modülün migration'larını sıfıra indirir, yeniden
// uygular ve sürüm defterinin temiz kaldığını doğrular.
//
// Asıl iddia dirty=false'tur: patlayan bir down, golang-migrate'in defterini
// "dirty" bırakır ve cmd/server her açılışta modül başına Migrate çağırdığı
// için modül bir daha AÇILAMAZ. Tablo ve tohum sayımları down'ın işini
// gerçekten yaptığını, sessizce atlamadığını gösterir.
func geriAlVeYenidenUygula(ctx context.Context, t *testing.T) {
	t.Helper()

	src := region.New(nil).Migrations()

	require.NoError(t, db.MigrateDown(ctx, testDSN, src, region.ModuleName, 0))
	for _, table := range modulTablolari {
		assert.False(t, tabloVar(ctx, t, table), "%s geri alma sonrası kalmamalı", table)
	}

	require.NoError(t, db.Migrate(ctx, testDSN, src, region.ModuleName))
	for _, table := range modulTablolari {
		assert.True(t, tabloVar(ctx, t, table), "%s yeniden uygulanmalı", table)
	}

	version, dirty, err := db.Version(ctx, testDSN, region.ModuleName)
	require.NoError(t, err)
	assert.False(t, dirty, "yarıda kalmış migration olmamalı")
	assert.Equal(t, uint(2), version, "şema (1) ve tohum (2) ayrı sürümlerdir")

	assert.Equal(t, int64(tohumdakiUlkeSayisi), sayim(ctx, t, `SELECT count(*) FROM country`),
		"ülke tohumu yeniden uygulanmalı")
	assert.Equal(t, int64(tohumdakiParaBirimiSayisi), sayim(ctx, t, `SELECT count(*) FROM currency`),
		"para birimi tohumu yeniden uygulanmalı")
	assert.Zero(t, sayim(ctx, t, `SELECT count(*) FROM region`),
		"şema düşüp yeniden kurulduğu için hiçbir bölge kalmamalı")
}

// TestTohumVerisiYuklendi referans verisinin migration ile geldiğini doğrular.
//
// Tohum, modülün kullanılabilirliğinin ön şartıdır: eksik bir ülke, o ülkedeki
// müşteri için sepet açılamaması demektir.
func TestTohumVerisiYuklendi(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	assert.Equal(t, int64(tohumdakiUlkeSayisi), sayim(ctx, t, `SELECT count(*) FROM country`))
	assert.Equal(t, int64(tohumdakiParaBirimiSayisi), sayim(ctx, t, `SELECT count(*) FROM currency`))

	// Ondalık basamak sayısı bu modülün varlık sebebidir; üç sınıf da
	// tohumda bulunmalıdır.
	basamaklar := map[string]int32{"TRY": 2, "USD": 2, "EUR": 2, "GBP": 2, "JPY": 0, "KWD": 3}
	for kod, beklenen := range basamaklar {
		currency, err := svc.GetCurrency(ctx, kod)
		require.NoError(t, err, "%s tohumda olmalı", kod)
		assert.Equal(t, beklenen, currency.DecimalDigits, "%s ondalık basamağı", kod)
		assert.NotEmpty(t, currency.Symbol, "%s sembolü olmalı", kod)
		assert.NotEmpty(t, currency.Name, "%s adı olmalı", kod)
	}

	// Çarpanın tam sayı bölmesiyle kullanımı: 1999 minor unit, para birimine
	// göre farklı bir major unit verir.
	jpy, err := svc.GetCurrency(ctx, "JPY")
	require.NoError(t, err)
	try, err := svc.GetCurrency(ctx, "TRY")
	require.NoError(t, err)
	kwd, err := svc.GetCurrency(ctx, "KWD")
	require.NoError(t, err)
	assert.Equal(t, int64(1999), 1999/jpy.MinorUnitFactor())
	assert.Equal(t, int64(19), 1999/try.MinorUnitFactor())
	assert.Equal(t, int64(1), 1999/kwd.MinorUnitFactor())

	// Ülke adları ISO'nun İngilizce kısa adlarıdır ve kodlar BÜYÜK harftir.
	ulkeler, err := svc.ListCountries(ctx, service.ListCountriesInput{Limit: service.MaxLimit})
	require.NoError(t, err)
	require.NotEmpty(t, ulkeler.Items)
	assert.Equal(t, int64(tohumdakiUlkeSayisi), ulkeler.Count)

	assert.Zero(t, sayim(ctx, t, `SELECT count(*) FROM country WHERE iso_2 <> upper(iso_2)`),
		"tüm ülke kodları BÜYÜK harf olmalı")
	assert.Zero(t, sayim(ctx, t, `SELECT count(*) FROM currency WHERE code <> upper(code)`),
		"tüm para birimi kodları BÜYÜK harf olmalı")
	assert.Equal(t, int64(1), sayim(ctx, t, `SELECT count(*) FROM country WHERE iso_2 = 'TR' AND name = 'Türkiye'`))
}

// TestTohumTekrarUygulanabilir tohum dosyasının GERÇEĞİNİ ikinci kez
// çalıştırıp idempotent olduğunu doğrular.
//
// Dosyanın kendisi okunur; testin içine kopyalanmış bir SQL, dosyada yapılan
// bir değişikliği görmez ve kanıt değeri taşımazdı.
//
// İki iddia birden sınanır: yeniden çalıştırma birincil anahtar ihlaliyle
// PATLAMAMALI (aksi hâlde bir yeniden dağıtım migration'ı kirli bırakırdı) ve
// operatörün düzelttiği bir değer EZİLMEMELİ.
func TestTohumTekrarUygulanabilir(t *testing.T) {
	ctx := context.Background()

	tohum, err := fs.ReadFile(region.New(nil).Migrations(), "000002_region_seed.up.sql")
	require.NoError(t, err, "tohum dosyası gömülü olmalı")

	_, err = testPool.Pool().Exec(ctx, `UPDATE currency SET symbol = 'X' WHERE code = 'TRY'`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, restoreErr := testPool.Pool().Exec(ctx,
			`UPDATE currency SET symbol = '₺' WHERE code = 'TRY'`)
		require.NoError(t, restoreErr)
	})

	oncekiUlke := sayim(ctx, t, `SELECT count(*) FROM country`)

	_, err = testPool.Pool().Exec(ctx, string(tohum))
	require.NoError(t, err, "tohum ikinci kez uygulanabilmeli")

	assert.Equal(t, oncekiUlke, sayim(ctx, t, `SELECT count(*) FROM country`),
		"ikinci uygulama satır ÇOĞALTMAMALI")

	var symbol string
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT symbol FROM currency WHERE code = 'TRY'`).Scan(&symbol))
	assert.Equal(t, "X", symbol, "operatörün düzelttiği değer EZİLMEMELİ")
}

// TestBolgeGuncellemeKilitAltindaOkur kısmi güncellemenin okumasını satır
// kilidi altında yaptığını belirlenimci biçimde doğrular.
//
// Kurgu [TestUlkeAtamasiKilitAltindaOkur] ile aynıdır ve KAYIP GÜNCELLEMEYİ
// (lost update) üretir: rakip bir işlem satırı kilitlerken güncelleme başlar
// ve bekler; rakip işlem vergi oranını değiştirip commit eder. Okuma kilit
// ALTINDA yapıldığı için bekleyen güncelleme satırın YENİ hâlini okur ve
// yalnızca kendi alanını değiştirir. Kilitsiz okumada ise güncelleme, satırı
// ESKİ hâliyle okumuş olurdu ve yazarken rakip işlemin oranını eski değeriyle
// geri yazardı — hiçbir hata dönmeden.
func TestBolgeGuncellemeKilitAltindaOkur(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	bolge := yeniBolge(ctx, t, svc, "TRY")
	require.Equal(t, int32(2000), bolge.TaxRate)

	conn, err := testPool.Pool().Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	var kilitli string
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT id FROM region WHERE id = $1 FOR UPDATE`, bolge.ID).Scan(&kilitli))

	sonuc := make(chan error, 1)
	go func() {
		yeniAd := "Kilit Altında Güncellendi"
		_, updErr := svc.UpdateRegion(ctx, bolge.ID, service.UpdateRegionInput{Name: &yeniAd})
		sonuc <- updErr
	}()

	requireKilitBekleyen(ctx, t)

	_, err = tx.Exec(ctx,
		`UPDATE region SET tax_rate = 500, updated_at = now() WHERE id = $1`, bolge.ID)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	select {
	case updErr := <-sonuc:
		require.NoError(t, updErr)
	case <-time.After(15 * time.Second):
		t.Fatal("bekleyen güncelleme zamanında tamamlanmadı")
	}

	guncel, err := svc.GetRegion(ctx, bolge.ID)
	require.NoError(t, err)
	assert.Equal(t, "Kilit Altında Güncellendi", guncel.Name, "yamanın alanı yazılmalı")
	assert.Equal(t, int32(500), guncel.TaxRate,
		"rakip işlemin yazdığı oran EZİLMEMELİ (kayıp güncelleme)")
}

// TestCrossModuleForeignKeyYok modülün tablolarındaki TÜM foreign key'lerin
// yine modülün kendi tablolarına gittiğini doğrular (Prensip 2.2).
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
	assert.Equal(t, 2, sayi, "region->currency ve country->region bağları kurulmuş olmalı")
}

// TestBolgeYasamDongusu bölge oluşturma, okuma, kısmi güncelleme ve yumuşak
// silmeyi uçtan uca doğrular.
func TestBolgeYasamDongusu(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	bolge, err := svc.CreateRegion(ctx, service.CreateRegionInput{
		Name: "Türkiye", CurrencyCode: "try", AutomaticTaxes: true, TaxRate: 2000,
	})
	require.NoError(t, err)
	assert.Equal(t, "TRY", bolge.CurrencyCode, "kod BÜYÜK harf saklanmalı")
	assert.False(t, bolge.CreatedAt.IsZero(), "created_at veritabanından gelmeli")
	assert.Equal(t, "UTC", bolge.CreatedAt.Location().String(), "zaman UTC olmalı")

	okunan, err := svc.GetRegion(ctx, bolge.ID)
	require.NoError(t, err)
	assert.Equal(t, bolge.ID, okunan.ID)
	assert.Equal(t, int32(2000), okunan.TaxRate)

	// Kısmi güncelleme: yalnızca oran değişir.
	oran := int32(1000)
	guncel, err := svc.UpdateRegion(ctx, bolge.ID, service.UpdateRegionInput{TaxRate: &oran})
	require.NoError(t, err)
	assert.Equal(t, int32(1000), guncel.TaxRate)
	assert.Equal(t, "Türkiye", guncel.Name, "verilmeyen alan değişmemeli")
	assert.Equal(t, "TRY", guncel.CurrencyCode, "verilmeyen alan değişmemeli")
	assert.True(t, guncel.UpdatedAt.After(bolge.UpdatedAt) || guncel.UpdatedAt.Equal(bolge.UpdatedAt))

	require.NoError(t, svc.DeleteRegion(ctx, bolge.ID))

	_, err = svc.GetRegion(ctx, bolge.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err), "yumuşak silinen bölge okunamamalı")

	sayfa, err := svc.ListRegions(ctx, service.MaxLimit, 0)
	require.NoError(t, err)
	for _, item := range sayfa.Items {
		assert.NotEqual(t, bolge.ID, item.ID, "yumuşak silinen bölge listede görünmemeli")
	}

	// Satır hâlâ oradadır; silme SOFT'tur.
	assert.Equal(t, int64(1), sayim(ctx, t,
		`SELECT count(*) FROM region WHERE id = $1 AND deleted_at IS NOT NULL`, bolge.ID))
}

// TestTanimsizParaBirimiReddedilir foreign key ihlalinin anlamlı bir tipli
// hataya çevrildiğini doğrular.
//
// Sınıflandırılmasaydı istemcinin düzeltebileceği bu durum 500 olarak görünür
// ve gerçek sebep yalnızca logda kalırdı.
func TestTanimsizParaBirimiReddedilir(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	_, err := svc.CreateRegion(ctx, service.CreateRegionInput{Name: "X", CurrencyCode: "XBT"})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, repository.CodeUnknownCurrency, errors.CodeOf(err))
}

// TestVeritabaniKisitlariUygulanir servis doğrulaması atlansa bile kısıtların
// ikinci kapı olduğunu doğrular.
func TestVeritabaniKisitlariUygulanir(t *testing.T) {
	ctx := context.Background()

	// Aralık dışı vergi oranı (servis onu zaten eler; buradaki iddia CHECK'in
	// gerçekten kurulmuş olduğudur).
	_, err := testPool.Pool().Exec(ctx,
		`INSERT INTO region (id, name, currency_code, tax_rate) VALUES ('reg_check', 'X', 'TRY', 10001)`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "region_tax_rate_check")

	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO region (id, name, currency_code) VALUES ('reg_check', '', 'TRY')`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "region_name_check")

	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO currency (code, symbol, name) VALUES ('try', '₺', 'X')`)
	require.Error(t, err, "küçük harfli kod kabul edilmemeli")
	assert.Contains(t, err.Error(), "currency_code_check")

	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO currency (code, symbol, name, decimal_digits) VALUES ('ZZZ', 'Z', 'X', 5)`)
	require.Error(t, err, "beşten fazla basamak kabul edilmemeli")
	assert.Contains(t, err.Error(), "currency_digits_check")

	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO country (iso_2, name) VALUES ('TRX', 'X')`)
	require.Error(t, err, "üç harfli ülke kodu kabul edilmemeli")
	assert.Contains(t, err.Error(), "country_iso_2_check")
}

// TestUlkeTekilligi bir ülkenin en fazla bir bölgeye ait olabileceğini gerçek
// veritabanında doğrular.
func TestUlkeTekilligi(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	ilk := yeniBolge(ctx, t, svc, "TRY")
	ikinci := yeniBolge(ctx, t, svc, "USD")

	ulke, err := svc.AddCountryToRegion(ctx, ilk.ID, "cy")
	require.NoError(t, err)
	require.NotNil(t, ulke.RegionID)
	assert.Equal(t, ilk.ID, *ulke.RegionID)
	assert.Equal(t, "CY", ulke.Code)

	_, err = svc.AddCountryToRegion(ctx, ikinci.ID, "CY")
	require.Error(t, err, "aynı ülke ikinci bir bölgeye eklenememeli")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))

	// Aynı bölgeye tekrar ekleme idempotenttir.
	tekrar, err := svc.AddCountryToRegion(ctx, ilk.ID, "CY")
	require.NoError(t, err)
	assert.Equal(t, ilk.ID, *tekrar.RegionID)

	// Olmayan ülke ve olmayan bölge ayrı ayrı bulunamadı döner.
	_, err = svc.AddCountryToRegion(ctx, ilk.ID, "ZZ")
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))

	_, err = svc.AddCountryToRegion(ctx, "reg_OLMAYAN", "MT")
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// kilitBekleyenSayisi kilit bekleyen (bloke olmuş) oturum sayısını döner.
func kilitBekleyenSayisi(ctx context.Context, t *testing.T) int64 {
	t.Helper()

	return sayim(ctx, t,
		`SELECT count(*) FROM pg_stat_activity
         WHERE datname = current_database()
           AND wait_event_type = 'Lock'
           AND pid <> pg_backend_pid()`)
}

// requireKilitBekleyen bir isteğin gerçekten kilitte beklediğini doğrular.
//
// Uyku yerine BEKLEME DURUMUNA bakılır: sabit bir uyku ya yavaş makinede
// erken uyanıp testi kırılgan yapardı, ya da her koşuya boş bekleme eklerdi.
func requireKilitBekleyen(ctx context.Context, t *testing.T) {
	t.Helper()

	require.Eventually(t, func() bool {
		return kilitBekleyenSayisi(ctx, t) > 0
	}, 10*time.Second, 10*time.Millisecond, "istek satır kilidinde beklemeliydi")
}

// TestUlkeAtamasiKilitAltindaOkur ülke satırının OKUMA ANINDA kilitlendiğini
// belirlenimci biçimde doğrular.
//
// EŞZAMANLILIK İDDİASININ ASIL KANITI BUDUR. Kurgu, yarışın kaybeden tarafını
// zamanlamaya bırakmadan üretir:
//
//  1. Rakip bir işlem ülke satırını FOR UPDATE ile kilitler (henüz kimseye
//     ait değildir).
//  2. Servis, aynı ülkeyi başka bir bölgeye eklemeye çalışır ve BEKLER.
//  3. Rakip işlem ülkeyi ilk bölgeye alıp commit eder.
//  4. Bekleyen istek uyanır.
//
// Doğru uygulamada 4. adımda okuma kilit ALTINDA yapıldığı için satırın GÜNCEL
// hâli görülür ve errors.Conflict döner. Okuma kilitsiz olsaydı istek 2.
// adımda region_id'yi BOŞ okumuş olurdu; uyandığında kararı çoktan verilmiş
// olur, UPDATE'i WHERE koşulunu yeniden değerlendirip başarılı olur ve ülke
// SESSİZCE ikinci bölgeye geçerdi — hiçbir hata dönmeden.
func TestUlkeAtamasiKilitAltindaOkur(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	ilk := yeniBolge(ctx, t, svc, "TRY")
	ikinci := yeniBolge(ctx, t, svc, "USD")

	const ulkeKodu = "IS"

	conn, err := testPool.Pool().Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	var kilitli string
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT iso_2 FROM country WHERE iso_2 = $1 FOR UPDATE`, ulkeKodu).Scan(&kilitli))

	sonuc := make(chan error, 1)
	go func() {
		_, addErr := svc.AddCountryToRegion(ctx, ikinci.ID, ulkeKodu)
		sonuc <- addErr
	}()

	requireKilitBekleyen(ctx, t)

	_, err = tx.Exec(ctx,
		`UPDATE country SET region_id = $2, updated_at = now() WHERE iso_2 = $1`, ulkeKodu, ilk.ID)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	select {
	case addErr := <-sonuc:
		require.Error(t, addErr,
			"bekleyen istek, uyandığında satırın GÜNCEL hâlini görmeli ve çakışma dönmeli")
		assert.Equal(t, errors.KindConflict, errors.KindOf(addErr))
	case <-time.After(15 * time.Second):
		t.Fatal("bekleyen istek zamanında tamamlanmadı")
	}

	cozulen, err := svc.ResolveRegionForCountry(ctx, ulkeKodu)
	require.NoError(t, err)
	assert.Equal(t, ilk.ID, cozulen.ID, "ülke rakip işlemin bölgesinde kalmalı")
}

// TestSilinmekteOlanBolgeyeUlkeEklenemez bölge satırının PAYLAŞIMLI kilidinin
// işe yaradığını belirlenimci biçimde doğrular.
//
// Kilit sırasının ilk adımı bölgedir ve bilinçlidir: ülke ekleme, o sırada
// silinmekte olan bir bölgeyi CANLI görmemelidir. Kurgu:
//
//  1. Rakip bir işlem bölge satırını kilitler.
//  2. Ülke ekleme başlar ve bölge kilidinde BEKLER.
//  3. Rakip işlem bölgeyi yumuşak siler ve commit eder.
//  4. Bekleyen istek uyanır; FOR SHARE kilidi alındıktan sonra WHERE koşulu
//     (deleted_at IS NULL) YENİDEN değerlendirilir ve satır "yok" görünür.
//
// Bölge kilitsiz okunsaydı istek 2. adımda bölgeyi canlı görür, uyanmaz ve
// ülkeyi SİLİNMİŞ bir bölgeye bağlardı: ülke ölü bir bölgeye kilitlenir,
// başka hiçbir bölgeye eklenemez ve ResolveRegionForCountry onun için kalıcı
// olarak tutarsızlık kodu dönerdi.
func TestSilinmekteOlanBolgeyeUlkeEklenemez(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	bolge := yeniBolge(ctx, t, svc, "TRY")

	const ulkeKodu = "FI"

	conn, err := testPool.Pool().Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	var kilitli string
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT id FROM region WHERE id = $1 FOR UPDATE`, bolge.ID).Scan(&kilitli))

	sonuc := make(chan error, 1)
	go func() {
		_, addErr := svc.AddCountryToRegion(ctx, bolge.ID, ulkeKodu)
		sonuc <- addErr
	}()

	requireKilitBekleyen(ctx, t)

	_, err = tx.Exec(ctx,
		`UPDATE region SET deleted_at = now(), updated_at = now() WHERE id = $1`, bolge.ID)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	select {
	case addErr := <-sonuc:
		require.Error(t, addErr, "silinmiş bölgeye ülke eklenememeli")
		assert.Equal(t, errors.KindNotFound, errors.KindOf(addErr))
	case <-time.After(15 * time.Second):
		t.Fatal("bekleyen istek zamanında tamamlanmadı")
	}

	assert.Zero(t, sayim(ctx, t,
		`SELECT count(*) FROM country WHERE iso_2 = $1 AND region_id IS NOT NULL`, ulkeKodu),
		"ülke silinmiş bölgeye bağlanmamalı")
}

// TestEszamanliUlkeAtamasiTekKazanan aynı ülkeyi farklı bölgelere eşzamanlı
// eklemeye çalışan isteklerden YALNIZCA BİRİNİN kazandığını doğrular.
//
// Bu test kuralın uçtan uca tuttuğunu gösterir ama KİLİDİN VARLIĞINI KANITLAMAZ:
// istekler doğal olarak seri hâle gelirse kilitsiz bir uygulama da geçerdi.
// Kilidin asıl kanıtı [TestUlkeAtamasiKilitAltindaOkur] içindedir; ikisi
// birlikte hem kuralı hem mekanizmasını kapsar.
func TestEszamanliUlkeAtamasiTekKazanan(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	const bolgeSayisi = 8
	bolgeler := make([]models.Region, 0, bolgeSayisi)
	for i := range bolgeSayisi {
		bolgeler = append(bolgeler, yeniBolge(ctx, t, svc, []string{"TRY", "USD", "EUR", "JPY"}[i%4]))
	}

	const ulkeKodu = "MT"
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		kazanan  []string
		cakisma  int
		digerErr []error
	)

	wg.Add(bolgeSayisi)
	for _, bolge := range bolgeler {
		go func(regionID string) {
			defer wg.Done()

			_, err := svc.AddCountryToRegion(ctx, regionID, ulkeKodu)

			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				kazanan = append(kazanan, regionID)
			case errors.IsConflict(err):
				cakisma++
			default:
				digerErr = append(digerErr, err)
			}
		}(bolge.ID)
	}
	wg.Wait()

	assert.Empty(t, digerErr, "beklenmeyen hata: %v", digerErr)
	require.Len(t, kazanan, 1, "yarışı tam olarak bir bölge kazanmalı")
	assert.Equal(t, bolgeSayisi-1, cakisma, "kaybedenlerin hepsi çakışma almalı")

	// Veritabanındaki son durum kazananla uyuşmalı.
	cozulen, err := svc.ResolveRegionForCountry(ctx, ulkeKodu)
	require.NoError(t, err)
	assert.Equal(t, kazanan[0], cozulen.ID)
}

// TestResolveRegionForCountry ülkeden bölgeye çözümü ve üç başarısızlık
// durumunu gerçek veritabanında doğrular.
func TestResolveRegionForCountry(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	bolge := yeniBolge(ctx, t, svc, "EUR")

	_, err := svc.AddCountryToRegion(ctx, bolge.ID, "PT")
	require.NoError(t, err)

	cozulen, err := svc.ResolveRegionForCountry(ctx, "pt")
	require.NoError(t, err)
	assert.Equal(t, bolge.ID, cozulen.ID)
	assert.Equal(t, "EUR", cozulen.CurrencyCode, "sepet para birimini buradan alır")

	_, err = svc.ResolveRegionForCountry(ctx, "ZZ")
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
	assert.Equal(t, repository.CodeCountryNotFound, errors.CodeOf(err))

	_, err = svc.ResolveRegionForCountry(ctx, "AQ")
	require.Error(t, err, "bölgeye bağlanmamış ülke için bölge bulunmamalı")
	assert.Equal(t, service.CodeCountryUnassigned, errors.CodeOf(err))

	_, err = svc.ResolveRegionForCountry(ctx, "PRT")
	require.Error(t, err, "alpha-3 kodu kabul edilmemeli")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestBolgeSilinceUlkelerSerbestKalir silme ile ülkelerin serbest bırakılmasının
// TEK işlemde olduğunu doğrular.
//
// Serbest bırakılmasaydı ülke ölü bir bölgeye bağlı kalır, foreign key
// yüzünden başka hiçbir bölgeye eklenemez ve o ülkedeki müşteri için sepet
// hiç açılamazdı.
func TestBolgeSilinceUlkelerSerbestKalir(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	ilk := yeniBolge(ctx, t, svc, "TRY")
	ikinci := yeniBolge(ctx, t, svc, "USD")

	for _, kod := range []string{"AL", "AD"} {
		_, err := svc.AddCountryToRegion(ctx, ilk.ID, kod)
		require.NoError(t, err)
	}

	require.NoError(t, svc.DeleteRegion(ctx, ilk.ID))

	assert.Zero(t, sayim(ctx, t, `SELECT count(*) FROM country WHERE region_id = $1`, ilk.ID),
		"silinen bölgeye bağlı ülke kalmamalı")

	ulke, err := svc.AddCountryToRegion(ctx, ikinci.ID, "AL")
	require.NoError(t, err, "serbest kalan ülke başka bölgeye eklenebilmeli")
	require.NotNil(t, ulke.RegionID)
	assert.Equal(t, ikinci.ID, *ulke.RegionID)
}

// TestUlkeBolgedenCikarilir ülke çıkarma yolunu ve yanlış bölgeyle yapılan
// çağrının reddini doğrular.
func TestUlkeBolgedenCikarilir(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	ilk := yeniBolge(ctx, t, svc, "TRY")
	ikinci := yeniBolge(ctx, t, svc, "USD")

	_, err := svc.AddCountryToRegion(ctx, ilk.ID, "BG")
	require.NoError(t, err)

	err = svc.RemoveCountryFromRegion(ctx, ikinci.ID, "BG")
	require.Error(t, err, "başka bölgenin ülkesi çıkarılamamalı")
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
	assert.Equal(t, int64(1), sayim(ctx, t,
		`SELECT count(*) FROM country WHERE iso_2 = 'BG' AND region_id = $1`, ilk.ID),
		"başarısız çıkarma bağı bozmamalı")

	require.NoError(t, svc.RemoveCountryFromRegion(ctx, ilk.ID, "bg"))
	assert.Zero(t, sayim(ctx, t, `SELECT count(*) FROM country WHERE iso_2 = 'BG' AND region_id IS NOT NULL`))
}

// TestUlkeListesiBolgeyeGoreSuzulur ülke listesinin bölge süzgecini gerçek
// sorguyla doğrular.
func TestUlkeListesiBolgeyeGoreSuzulur(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	bolge := yeniBolge(ctx, t, svc, "TRY")

	for _, kod := range []string{"GE", "AM"} {
		_, err := svc.AddCountryToRegion(ctx, bolge.ID, kod)
		require.NoError(t, err)
	}

	sayfa, err := svc.ListCountries(ctx, service.ListCountriesInput{RegionID: &bolge.ID})
	require.NoError(t, err)
	require.Len(t, sayfa.Items, 2)
	assert.Equal(t, int64(2), sayfa.Count)
	assert.Equal(t, "AM", sayfa.Items[0].Code, "koda göre sıralı dönmeli")
	assert.Equal(t, "GE", sayfa.Items[1].Code)

	tumu, err := svc.ListCountries(ctx, service.ListCountriesInput{Limit: 5})
	require.NoError(t, err)
	assert.Len(t, tumu.Items, 5, "sayfa boyu uygulanmalı")
	assert.Equal(t, int64(tohumdakiUlkeSayisi), tumu.Count, "toplam sayı sayfa boyundan bağımsızdır")
}

// TestVitrinBolgeleriParaBirimiyleDoner vitrin görünümünün para birimi ve
// ülkeleri tek çağrıda taşıdığını doğrular.
func TestVitrinBolgeleriParaBirimiyleDoner(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	bolge := yeniBolge(ctx, t, svc, "JPY")
	_, err := svc.AddCountryToRegion(ctx, bolge.ID, "SG")
	require.NoError(t, err)

	item, err := svc.GetStoreRegion(ctx, bolge.ID)
	require.NoError(t, err)
	require.NotNil(t, item.Currency)
	assert.Equal(t, "JPY", item.Currency.Code)
	assert.Equal(t, int32(0), item.Currency.DecimalDigits, "JPY ondalıksızdır")
	assert.Equal(t, int64(1), item.Currency.MinorUnitFactor())
	require.Len(t, item.Countries, 1)
	assert.Equal(t, "SG", item.Countries[0].Code)
}

// TestInteropYuzeyiGercekVeriyleCalisir modüller arası dar yüzeyin gerçek
// veritabanında beklenen değerleri döndürdüğünü doğrular.
//
// Bu yüzey Faz 5'te cart'ın, Faz 6'da order'ın ve Faz 7'de tax'ın kullanacağı
// tek kapıdır; uyumsuzluk derleme zamanında değil çözüm anında görüneceği için
// (ADR 0001) gerçek veriyle sınanması ZORUNLUDUR.
func TestInteropYuzeyiGercekVeriyleCalisir(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	bolge := yeniBolge(ctx, t, svc, "KWD")
	_, err := svc.AddCountryToRegion(ctx, bolge.ID, "KW")
	require.NoError(t, err)

	id, err := svc.RegionIDForCountry(ctx, "kw")
	require.NoError(t, err)
	assert.Equal(t, bolge.ID, id)

	kod, basamak, err := svc.RegionCurrency(ctx, bolge.ID)
	require.NoError(t, err)
	assert.Equal(t, "KWD", kod)
	assert.Equal(t, int32(3), basamak, "KWD üç basamaklıdır")

	oran, otomatik, err := svc.RegionTax(ctx, bolge.ID)
	require.NoError(t, err)
	assert.Equal(t, int32(2000), oran)
	assert.True(t, otomatik)

	// Verginin tam sayı aritmetiğiyle hesabı: 19,990 KWD (19990 fils) için
	// 19990 * 2000 / 10000 = 3998 fils.
	assert.Equal(t, int64(3998), 19990*int64(oran)/int64(models.MaxTaxRate))

	basamak, err = svc.CurrencyDecimalDigits(ctx, "jpy")
	require.NoError(t, err)
	assert.Zero(t, basamak)
}

// TestQuerySaglayicisiTopluOkur sağlayıcının gerçek sorgularla toplu okuduğunu
// ve kayıtları para birimi/ülke ile döndürdüğünü doğrular.
func TestQuerySaglayicisiTopluOkur(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	provider := service.NewQueryProvider(svc)

	ilk := yeniBolge(ctx, t, svc, "TRY")
	ikinci := yeniBolge(ctx, t, svc, "JPY")
	_, err := svc.AddCountryToRegion(ctx, ilk.ID, "AZ")
	require.NoError(t, err)
	_, err = svc.AddCountryToRegion(ctx, ikinci.ID, "TH")
	require.NoError(t, err)

	records, err := provider.FetchByIDs(ctx, []string{ilk.ID, ikinci.ID}, nil)
	require.NoError(t, err)
	require.Len(t, records, 2)

	byID := map[string]query.Record{}
	for _, record := range records {
		id, ok := record[query.IDField].(string)
		require.True(t, ok)
		byID[id] = record
	}

	jp, ok := byID[ikinci.ID]["currency"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "JPY", jp["code"])
	assert.Equal(t, int32(0), jp["decimal_digits"])

	countries, ok := byID[ilk.ID]["countries"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, countries, 1)
	assert.Equal(t, "AZ", countries[0]["code"])

	// Alan seçimi: istenmeyen alt kayıtlar hiç dönmez.
	dar, err := provider.FetchByIDs(ctx, []string{ilk.ID}, []string{"currency_code"})
	require.NoError(t, err)
	require.Len(t, dar, 1)
	assert.NotContains(t, dar[0], "currency")
	assert.NotContains(t, dar[0], "countries")
	assert.Equal(t, "TRY", dar[0]["currency_code"])
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

	mod := region.New(nil)
	require.NoError(t, mod.Register(ctx, c))

	svc, err := container.Resolve[*service.Service](c, "region.service")
	require.NoError(t, err, "servis, sabit adıyla çözülebilmeli")
	require.NotNil(t, svc)
	assert.Equal(t, "region.service", region.ServiceName,
		"servis adı değişirse tüketici modüller onu bulamaz")

	// Ad, ADR 0004'ün kuralıyla ELDE hesaplanır: sağlayıcı "<entity>.query"
	// adıyla aranır. Sabiti kullanmak testi totolojiye çevirirdi.
	provider, err := container.Resolve[query.Provider](c, "region"+query.ProviderSuffix)
	require.NoError(t, err, "Query sağlayıcısı adıyla çözülebilmeli (ADR 0004)")
	assert.Equal(t, "region", provider.Entity(),
		"kayıt adının öneki Entity() ile aynı olmalı")

	// Tüketici modülün (Faz 5'te cart) yazacağı DAR arayüz burada çözülür;
	// region import EDİLMEDEN yalnızca imzayla eşleşir (ADR 0001).
	type regionReader interface {
		RegionIDForCountry(ctx context.Context, countryCode string) (string, error)
		RegionCurrency(ctx context.Context, regionID string) (string, int32, error)
		RegionTax(ctx context.Context, regionID string) (int32, bool, error)
	}
	reader, err := container.Resolve[regionReader](c, region.ServiceName)
	require.NoError(t, err, "dar tüketici arayüzü servisi karşılamalı")

	bolge := yeniBolge(ctx, t, svc, "TRY")
	_, err = svc.AddCountryToRegion(ctx, bolge.ID, "MD")
	require.NoError(t, err)

	id, err := reader.RegionIDForCountry(ctx, "MD")
	require.NoError(t, err)
	assert.Equal(t, bolge.ID, id)

	// Asıl kanıt: çekirdeğin Query katmanı, modülü hiç tanımadan yalnızca
	// entity adıyla sağlayıcıyı bulup veriyi çekebilmeli.
	records, err := query.New(nil, c, nil).Graph(ctx, query.GraphSpec{
		Entity:  "region",
		Filters: map[string]any{"id": bolge.ID},
	})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, bolge.ID, records[0][query.IDField])
	assert.Equal(t, "TRY", records[0]["currency_code"])
}
