//go:build integration

// Bu dosyadaki testler gerçek bir PostgreSQL örneği (dolayısıyla Docker)
// gerektirir; `make test` hızlı kalsın diye `integration` etiketiyle
// ayrılmıştır. Çalıştırmak için: make test-integration
//
// Birim testleri sahte bir depo ve sahte bir bağ servisiyle servisin
// KARARLARINI kanıtlar. Buradaki testler kararların dayandığı ZEMİNİ kanıtlar:
// migration'ın gerçekten geri alınabildiğini, "bir müşteri en fazla BİR
// şirketin çalışanıdır" kuralının link tablosunun benzersiz indeksinde
// durduğunu, şirket silmenin çalışanları ve bağları TEK işlemde temizlediğini
// ve CHECK kısıtlarının uygulama doğrulaması atlansa bile tuttuğunu.
//
// Bu iddiaların hiçbiri sahte bağ servisiyle sınanamaz: sahte, kardinaliteyi
// Go'da TAKLİT eder ve taklidin gerçeğe uyduğunu yalnızca bu dosya gösterir.
package b2b_test

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/link"
	"github.com/bdrtr/gobit/internal/modules/b2b"
	"github.com/bdrtr/gobit/internal/modules/b2b/models"
	"github.com/bdrtr/gobit/internal/modules/b2b/repository"
	"github.com/bdrtr/gobit/internal/modules/b2b/service"
)

const postgresImage = "postgres:16-alpine"

// modulTablolari modülün sahip olduğu tablolardır; migration testleri bu
// listeyi kullanır.
var modulTablolari = []string{"b2b_company", "b2b_company_employee"}

var (
	// testPool tüm testlerin paylaştığı havuzdur.
	testPool *db.Pool
	// testDSN migration çağrıları için bağlantı adresidir.
	testDSN string
	// testLinks gerçek link servisidir; tanım bir kez bildirilir.
	testLinks link.LinkService
	// musteriSayaci testler arasında benzersiz customer id üretir.
	musteriSayaci atomic.Int64
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

	testPool, err = db.New(ctx, db.DefaultConfig(testDSN), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bağlantı havuzu açılamadı: %v\n", err)
		return 1
	}
	defer testPool.Close()

	if err := db.Migrate(ctx, testDSN, b2b.New(nil).Migrations(), b2b.ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "migration uygulanamadı: %v\n", err)
		return 1
	}

	// Link tanımı ÜRETİMDEKİ yoldan bildirilir (modülün Register'ı da bunu
	// yapar): tablo ve kardinalite indeksleri böylece gerçek olur.
	testLinks = link.New(testPool, nil)
	for _, def := range service.Definitions() {
		if err := testLinks.Define(ctx, def); err != nil {
			fmt.Fprintf(os.Stderr, "link tanımı bildirilemedi: %v\n", err)
			return 1
		}
	}

	return m.Run()
}

// yeniServis gerçek depo ve gerçek link servisi üzerinde çalışan bir servis
// kurar.
func yeniServis(t *testing.T) *service.Service {
	t.Helper()

	svc, err := service.New(service.Options{
		Repo:  repository.New(testPool.Pool()),
		Links: testLinks,
	})
	require.NoError(t, err)
	return svc
}

// yeniMusteriID testler arasında çakışmayan bir customer id üretir.
//
// Kimlik customer modülünün önekini taşır ama o modülden GELMEZ: b2b müşterinin
// var olduğunu doğrulamaz (ADR 0001) ve bağ, serbest bir kimlik dizgesidir.
func yeniMusteriID() string {
	return fmt.Sprintf("%s%026d", models.CustomerIDPrefix, musteriSayaci.Add(1))
}

// yeniSirket test için bir şirket oluşturur.
func yeniSirket(t *testing.T, svc *service.Service, periyot models.SpendingResetPeriod) models.Company {
	t.Helper()

	company, err := svc.CreateCompany(t.Context(), service.CompanyInput{
		Name:                     t.Name(),
		Email:                    "muhasebe@ornek.test",
		CurrencyCode:             "TRY",
		SpendingLimitResetPeriod: string(periyot),
	})
	require.NoError(t, err)
	return company
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

// TestMigrationGeriAlinabilir migration'ın uygulanıp geri alınabildiğini
// doğrular (plan Bölüm 8).
//
// Geri alma modülün GERÇEK durumu üzerinde koşar: şirket ve çalışan satırları
// YERİNDE bırakılır. Şart bilinçlidir — modülün tek silme yolu SOFT delete'tir,
// yani operatör API'den her kaydı silse bile satırlar tabloda kalır ve
// b2b_company_employee -> b2b_company foreign key'i tutmaya devam eder. Boş bir
// tabloda koşan bir geri alma, tam da patlayacak durumu testten çıkarırdı.
//
// Asıl iddia dirty=false'tur: patlayan bir down, golang-migrate'in defterini
// "dirty" bırakır ve kompozisyon kökü her açılışta modül başına Migrate
// çağırdığı için modül bir daha AÇILAMAZ.
func TestMigrationGeriAlinabilir(t *testing.T) {
	ctx := t.Context()
	svc := yeniServis(t)

	company := yeniSirket(t, svc, models.ResetMonthly)
	_, err := svc.CreateEmployee(t.Context(), service.EmployeeInput{
		CompanyID: company.ID, CustomerID: yeniMusteriID(),
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), sayim(ctx, t,
		`SELECT count(*) FROM b2b_company_employee WHERE company_id = $1`, company.ID),
		"geri alma CANLI kayıtlar dururken koşmalı")

	src := b2b.New(nil).Migrations()

	require.NoError(t, db.MigrateDown(ctx, testDSN, src, b2b.ModuleName, 0),
		"down başarısız — bu, modülün bir daha migrate EDİLEMEMESİ demektir")
	for _, table := range modulTablolari {
		assert.False(t, tabloVar(ctx, t, table), "%s geri alma sonrası kalmamalı", table)
	}

	require.NoError(t, db.Migrate(ctx, testDSN, src, b2b.ModuleName))
	for _, table := range modulTablolari {
		assert.True(t, tabloVar(ctx, t, table), "%s yeniden uygulanmalı", table)
	}

	version, dirty, err := db.Version(ctx, testDSN, b2b.ModuleName)
	require.NoError(t, err)
	assert.False(t, dirty, "yarıda kalmış migration olmamalı")
	assert.Equal(t, uint(1), version)
	assert.Zero(t, sayim(ctx, t, `SELECT count(*) FROM b2b_company`),
		"şema düşüp yeniden kurulduğu için hiçbir şirket kalmamalı")
}

// TestUctanUcaSirketVeCalisanAkisi modülün yönetim akışını gerçek veritabanında
// koşturur.
func TestUctanUcaSirketVeCalisanAkisi(t *testing.T) {
	svc := yeniServis(t)
	ctx := t.Context()

	company := yeniSirket(t, svc, models.ResetMonthly)
	musteri := yeniMusteriID()

	limit := int64(250000)
	employee, err := svc.CreateEmployee(ctx, service.EmployeeInput{
		CompanyID:     company.ID,
		CustomerID:    musteri,
		SpendingLimit: &limit,
	})
	require.NoError(t, err)
	assert.Equal(t, musteri, employee.CustomerID)

	// Okuma yolu customer idni link'ten DOLDURMALI: sütunu yoktur.
	okunan, err := svc.GetEmployee(ctx, employee.ID)
	require.NoError(t, err)
	assert.Equal(t, musteri, okunan.CustomerID)
	assert.Zero(t, sayim(ctx, t,
		`SELECT count(*) FROM information_schema.columns
         WHERE table_name = 'b2b_company_employee' AND column_name = 'customer_id'`),
		"müşteri bağı şemada DEĞİL link tablosunda durmalı (Prensip 2.2)")

	// Limit kaldırılabilmeli ("dokunma" ile "sınırsız yap" ayrımı).
	guncel, err := svc.UpdateEmployee(ctx, employee.ID, service.UpdateEmployeeInput{
		ClearSpendingLimit: true,
	})
	require.NoError(t, err)
	assert.Nil(t, guncel.SpendingLimit)

	sayfa, err := svc.ListEmployees(ctx, service.ListEmployeesInput{CompanyID: &company.ID})
	require.NoError(t, err)
	require.Len(t, sayfa.Items, 1)
	assert.Equal(t, musteri, sayfa.Items[0].CustomerID)

	// Yumuşak silinen kayıt hiçbir okumada görünmez.
	require.NoError(t, svc.DeleteEmployee(ctx, employee.ID))
	_, err = svc.GetEmployee(ctx, employee.ID)
	assert.True(t, errors.IsNotFound(err), "silinen çalışan okunmamalı, gelen: %v", err)

	sayfa, err = svc.ListEmployees(ctx, service.ListEmployeesInput{CompanyID: &company.ID})
	require.NoError(t, err)
	assert.Empty(t, sayfa.Items)
}

// TestBaskasininSirketiOkunamaz modülün vitrin değişmezini GERÇEK link
// tablosunda sabitler.
//
// İki müşteri, iki şirket: her biri yalnızca kendi şirketini görür ve hiçbir
// şirkete bağlı olmayan müşteri boş kayıt değil 404 alır. Vitrinde şirketi
// kimliğiyle isteyebilecek bir uç bulunmadığı için (bkz. api paketi) bu, o
// yüzeyin tek giriş noktasıdır.
func TestBaskasininSirketiOkunamaz(t *testing.T) {
	svc := yeniServis(t)
	ctx := t.Context()

	acme := yeniSirket(t, svc, models.ResetMonthly)
	beta := yeniSirket(t, svc, models.ResetYearly)

	musteriA, musteriB := yeniMusteriID(), yeniMusteriID()
	_, err := svc.CreateEmployee(ctx, service.EmployeeInput{CompanyID: acme.ID, CustomerID: musteriA})
	require.NoError(t, err)
	_, err = svc.CreateEmployee(ctx, service.EmployeeInput{CompanyID: beta.ID, CustomerID: musteriB})
	require.NoError(t, err)

	uyelikA, err := svc.MembershipOfCustomer(ctx, musteriA)
	require.NoError(t, err)
	assert.Equal(t, acme.ID, uyelikA.Company.ID)
	require.NotNil(t, uyelikA.SpendingWindowStart, "aylık periyotta pencere olmalı")

	uyelikB, err := svc.MembershipOfCustomer(ctx, musteriB)
	require.NoError(t, err)
	assert.Equal(t, beta.ID, uyelikB.Company.ID,
		"müşteri YALNIZCA kendi şirketini görmeli")

	_, err = svc.MembershipOfCustomer(ctx, yeniMusteriID())
	assert.True(t, errors.IsNotFound(err),
		"hiçbir şirkete bağlı olmayan müşteri 404 almalı, gelen: %v", err)
}

// TestAyniMusteriIkinciSirketeEklenemez kardinalitenin VERİTABANINDA
// durduğunu doğrular.
//
// Kural uygulamada da tutulabilirdi ("önce oku sonra yaz") ama iki eşzamanlı
// istek arasında tutmazdı; link tablosunun benzersiz indeksi yarışı
// veritabanına bırakır ve ihlali tipli bir Conflict'e çevirir.
func TestAyniMusteriIkinciSirketeEklenemez(t *testing.T) {
	svc := yeniServis(t)
	ctx := t.Context()

	acme := yeniSirket(t, svc, models.ResetNever)
	beta := yeniSirket(t, svc, models.ResetNever)
	musteri := yeniMusteriID()

	_, err := svc.CreateEmployee(ctx, service.EmployeeInput{CompanyID: acme.ID, CustomerID: musteri})
	require.NoError(t, err)

	_, err = svc.CreateEmployee(ctx, service.EmployeeInput{CompanyID: beta.ID, CustomerID: musteri})
	require.True(t, errors.IsConflict(err), "beklenen sınıf Conflict, gelen: %v", err)

	// Bağı kurulamayan çalışan kaydı GERİ ALINMIŞ olmalı: aksi hâlde beta'nın
	// çalışan listesinde müşterisiz bir kayıt kalırdı.
	sayfa, err := svc.ListEmployees(ctx, service.ListEmployeesInput{CompanyID: &beta.ID})
	require.NoError(t, err)
	assert.Empty(t, sayfa.Items, "bağı kurulamayan çalışan ayakta kalmamalı")
}

// TestSirketSilinceCalisanlarVeBaglarTemizlenir silme kararının üç sonucunu
// birlikte doğrular: çalışanlar silinir, bağlar kalkar ve müşteri yeniden işe
// alınabilir.
//
// Üçüncüsü en kolay gözden kaçandır: bağ tekil olduğu için temizlenmemiş bir
// satır, müşteriyi ömür boyu tek bir kapanmış şirkete kilitlerdi.
func TestSirketSilinceCalisanlarVeBaglarTemizlenir(t *testing.T) {
	svc := yeniServis(t)
	ctx := t.Context()

	company := yeniSirket(t, svc, models.ResetMonthly)
	musteri := yeniMusteriID()
	employee, err := svc.CreateEmployee(ctx, service.EmployeeInput{
		CompanyID: company.ID, CustomerID: musteri,
	})
	require.NoError(t, err)

	require.NoError(t, svc.DeleteCompany(ctx, company.ID))

	assert.Equal(t, int64(1), sayim(ctx, t,
		`SELECT count(*) FROM b2b_company_employee WHERE id = $1 AND deleted_at IS NOT NULL`,
		employee.ID), "çalışan da yumuşak silinmeli")
	assert.Zero(t, sayim(ctx, t,
		`SELECT count(*) FROM link_b2b_employee_customer WHERE from_id = $1`, employee.ID),
		"müşteri bağı kaldırılmalı")

	_, err = svc.MembershipOfCustomer(ctx, musteri)
	assert.True(t, errors.IsNotFound(err), "silinen şirketin çalışanı vitrinde görünmemeli")

	yeni := yeniSirket(t, svc, models.ResetMonthly)
	_, err = svc.CreateEmployee(ctx, service.EmployeeInput{CompanyID: yeni.ID, CustomerID: musteri})
	assert.NoError(t, err, "bağı serbest kalan müşteri yeniden işe alınabilmeli")
}

// TestKisitlarVeritabanindaTutar CHECK kısıtlarının SON SAVUNMA olduğunu
// doğrular.
//
// Yazımlar servisi ATLAYARAK ham SQL ile yapılır: sınanan şey uygulama
// doğrulaması değil, o doğrulama bir gün atlandığında (başka bir kod yolu, elle
// müdahale, taşıma betiği) verinin yine de bozulamayacağıdır.
func TestKisitlarVeritabanindaTutar(t *testing.T) {
	ctx := t.Context()

	durumlar := map[string]string{
		"büyük harfli e-posta": `INSERT INTO b2b_company (id, name, email, currency_code)
             VALUES ('comp_kisit_1', 'X', 'BUYUK@ornek.test', 'TRY')`,
		"geçersiz para birimi": `INSERT INTO b2b_company (id, name, email, currency_code)
             VALUES ('comp_kisit_2', 'X', 'x@ornek.test', 'TR')`,
		"tanımsız sıfırlama periyodu": `INSERT INTO b2b_company
             (id, name, email, currency_code, spending_limit_reset_period)
             VALUES ('comp_kisit_3', 'X', 'x@ornek.test', 'TRY', 'weekly')`,
		"geçersiz ülke kodu": `INSERT INTO b2b_company
             (id, name, email, currency_code, country_code)
             VALUES ('comp_kisit_4', 'X', 'x@ornek.test', 'TRY', 'TUR')`,
	}

	for ad, sql := range durumlar {
		t.Run(ad, func(t *testing.T) {
			_, err := testPool.Pool().Exec(ctx, sql)
			assert.Error(t, err, "kısıt bu yazımı reddetmeliydi")
		})
	}

	t.Run("negatif harcama limiti", func(t *testing.T) {
		svc := yeniServis(t)
		company := yeniSirket(t, svc, models.ResetNever)

		_, err := testPool.Pool().Exec(ctx,
			`INSERT INTO b2b_company_employee (id, company_id, spending_limit)
             VALUES ('compemp_kisit_1', $1, -1)`, company.ID)
		assert.Error(t, err, "negatif limit bir sınır değil, anlamsız bir sayıdır")
	})

	t.Run("boş ülke kodu geçerlidir", func(t *testing.T) {
		// Adres opsiyoneldir: kayıt çoğu zaman fatura adresi kesinleşmeden
		// açılır ve kısıt bunu KABUL etmelidir.
		_, err := testPool.Pool().Exec(ctx,
			`INSERT INTO b2b_company (id, name, email, currency_code, country_code)
             VALUES ('comp_kisit_5', 'X', 'x@ornek.test', 'TRY', '')`)
		assert.NoError(t, err)
	})
}
