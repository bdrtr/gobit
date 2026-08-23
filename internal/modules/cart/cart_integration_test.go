//go:build integration

// Bu dosyadaki testler gerçek bir PostgreSQL örneği (dolayısıyla Docker)
// gerektirir; `make test` hızlı kalsın diye `integration` etiketiyle
// ayrılmıştır. Çalıştırmak için: make test-integration
//
// Birim testleri sahte bir depo ile servisin KARARLARINI kanıtlar. Buradaki
// testler kararların dayandığı ZEMİNİ kanıtlar: migration'ın geri
// alınabildiğini, kısıtların gerçekten uygulandığını, link'lerin gerçekten
// kurulduğunu ve eşzamanlılık iddiasının veritabanı düzeyinde tuttuğunu.
// Özellikle "eşzamanlı AddLineItem satırları bozmaz" iddiası yalnızca burada,
// gerçek goroutine'lerle gerçek satır kilitleri üzerinde sınanabilir.
package cart_test

import (
	"context"
	"fmt"
	"os"
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
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/cart"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
	"github.com/bdrtr/gobit/internal/modules/cart/repository"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

const postgresImage = "postgres:16-alpine"

// modulTablolari modülün sahip olduğu tablolardır; migration testleri bu
// listeyi kullanır.
var modulTablolari = []string{
	"carts", "cart_line_items", "cart_addresses", "cart_shipping_methods",
}

// Test verisinde kullanılan sabitler. Bölge ve müşteri kimlikleri BAŞKA
// modüllere aittir; bu modül onların varlığını doğrulamaz (Prensip 2.2).
const (
	testRegionID   = "reg_TEST"
	testCustomerID = "cust_TEST"
	testCurrency   = "TRY"
)

var (
	// testPool tüm testlerin paylaştığı havuzdur.
	testPool *db.Pool
	// testDSN migration çağrıları için bağlantı adresidir.
	testDSN string
	// testLinks gerçek link servisidir.
	testLinks link.LinkService
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

	if err := db.Migrate(ctx, testDSN, cart.New().Migrations(), cart.ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "migration uygulanamadı: %v\n", err)
		return 1
	}

	testLinks = link.New(testPool, nil)
	for _, def := range service.Definitions() {
		if err := testLinks.Define(ctx, def); err != nil {
			fmt.Fprintf(os.Stderr, "%q link tanımı bildirilemedi: %v\n", def.Name, err)
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

// yeniSepet test için misafir sepeti oluşturur.
func yeniSepet(ctx context.Context, t *testing.T, svc *service.Service) models.Cart {
	t.Helper()

	sepet, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID:     testRegionID,
		CurrencyCode: testCurrency,
	})
	require.NoError(t, err)
	return sepet
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

// TestMigrationGeriAlinabilir migration'ın uygulanıp geri alınabildiğini
// doğrular (plan Bölüm 8: up/down çiftleri, geri alınabilir).
func TestMigrationGeriAlinabilir(t *testing.T) {
	ctx := context.Background()
	src := cart.New().Migrations()

	for _, table := range modulTablolari {
		require.True(t, tabloVar(ctx, t, table), "%s başlangıçta var olmalı", table)
	}

	require.NoError(t, db.MigrateDown(ctx, testDSN, src, cart.ModuleName, 0))
	for _, table := range modulTablolari {
		assert.False(t, tabloVar(ctx, t, table), "%s geri alma sonrası kalmamalı", table)
	}

	require.NoError(t, db.Migrate(ctx, testDSN, src, cart.ModuleName))
	for _, table := range modulTablolari {
		assert.True(t, tabloVar(ctx, t, table), "%s yeniden uygulanmalı", table)
	}

	version, dirty, err := db.Version(ctx, testDSN, cart.ModuleName)
	require.NoError(t, err)
	assert.False(t, dirty, "yarıda kalmış migration olmamalı")
	assert.Equal(t, uint(1), version)
}

// TestCrossModuleForeignKeyYok modülün tablolarındaki TÜM foreign key'lerin
// yine modülün kendi tablolarına gittiğini doğrular (Prensip 2.2).
//
// Özellikle cart_line_items.variant_id (product), carts.region_id (region) ve
// carts.customer_id (customer) başka modüllerin kimlikleridir ve foreign key
// OLAMAZ; bu test o kuralın şemada gerçekten tutulduğunu gösterir.
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

// TestSepetYasamDongusu sepetin uçtan uca akışını doğrular: oluştur -> satır
// ekle -> adet güncelle -> adresi yaz -> kargo ekle -> toplamları yaz ->
// tamamla.
func TestSepetYasamDongusu(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	sepet, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID:     testRegionID,
		CustomerID:   testCustomerID,
		Email:        "Musteri@Ornek.COM",
		CurrencyCode: "try",
		Metadata:     map[string]any{"kanal": "web"},
	})
	require.NoError(t, err)
	assert.Equal(t, "TRY", sepet.CurrencyCode)
	assert.Equal(t, "musteri@ornek.com", sepet.Email)
	assert.Equal(t, "UTC", sepet.CreatedAt.Location().String(), "zaman UTC olmalı")
	assert.Equal(t, map[string]any{"kanal": "web"}, sepet.Metadata)
	assert.False(t, sepet.TotalsStale())

	item, err := svc.AddLineItem(ctx, sepet.ID, service.AddLineItemInput{
		VariantID: "variant_A", Title: "Kırmızı Tişört", Quantity: 2, UnitPrice: 1000,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), item.Quantity)

	item, err = svc.UpdateLineItemQuantity(ctx, sepet.ID, item.ID, 3)
	require.NoError(t, err)
	assert.Equal(t, int64(3), item.Quantity)

	_, err = svc.SetShippingAddress(ctx, sepet.ID, service.AddressInput{
		FirstName: "Ayşe", LastName: "Yılmaz", Address1: "Bağdat Cad. 1",
		City: "İstanbul", CountryCode: "tr", PostalCode: "34000",
	})
	require.NoError(t, err)
	_, err = svc.SetBillingAddress(ctx, sepet.ID, service.AddressInput{
		FirstName: "Ayşe", City: "Ankara", CountryCode: "TR",
	})
	require.NoError(t, err)

	method, err := svc.AddShippingMethod(ctx, sepet.ID, service.AddShippingMethodInput{
		Name: "Standart Kargo", Amount: 2500, Data: map[string]any{"sube": "Kadıköy"},
	})
	require.NoError(t, err)

	detail, err := svc.GetCart(ctx, sepet.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1)
	require.NotNil(t, detail.ShippingAddress)
	require.NotNil(t, detail.BillingAddress)
	assert.Equal(t, "TR", detail.ShippingAddress.CountryCode)
	assert.Equal(t, "Ankara", detail.BillingAddress.City)
	require.Len(t, detail.ShippingMethods, 1)
	assert.Equal(t, map[string]any{"sube": "Kadıköy"}, detail.ShippingMethods[0].Data)
	assert.True(t, detail.TotalsStale(), "hesaplanmadan önce toplamlar bayat olmalı")

	// Toplamlar: 3 × 1000 = 3000 ara toplam, %20 vergi 600, kargo 2500.
	// Hesabın dayandığı şekil ÇAĞIRANDAN gelir; workflow da tam olarak böyle
	// yapar: önce okur, sonra hesabı okuduğu şekille birlikte yazar.
	require.NoError(t, svc.SetTotals(ctx, sepet.ID, service.Totals{
		Revision: detail.Revision,
		Subtotal: 3000, TaxTotal: 600, ShippingTotal: 2500, Total: 6100,
		Lines: []service.LineTotals{
			{LineItemID: item.ID, UnitPrice: 1000, Subtotal: 3000, TaxTotal: 600, Total: 3600},
		},
	}))

	detail, err = svc.GetCart(ctx, sepet.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(6100), detail.Total)
	assert.True(t, detail.TotalsConsistent())
	assert.False(t, detail.TotalsStale())
	assert.Equal(t, int64(3600), detail.Items[0].Total)

	completed, err := svc.MarkCompleted(ctx, sepet.ID)
	require.NoError(t, err)
	require.True(t, completed.Completed())
	assert.Equal(t, "UTC", completed.CompletedAt.Location().String())

	// Tamamlanmış sepet artık değiştirilemez.
	err = svc.RemoveShippingMethod(ctx, sepet.ID, method.ID)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err),
		"tamamlanmış sepette kargo yöntemi kaldırılamamalı, aldığı: %v", err)
}

// sepetSurumu sepetin veritabanındaki şekil ve toplam sayaçlarını okur.
//
// Servis üzerinden değil DOĞRUDAN sorguyla okunur: damganın gerçekten hangi
// değerle yazıldığı burada sınanan şeydir ve servisin kendi okuması aynı
// varsayımı taşıdığı için bağımsız bir tanık olmazdı.
func sepetSurumu(ctx context.Context, t *testing.T, cartID string) (revision, totalsRevision int64) {
	t.Helper()

	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT revision, totals_revision FROM carts WHERE id = $1`, cartID).
		Scan(&revision, &totalsRevision))
	return revision, totalsRevision
}

// TestSetTotalsFiyatlanmamisSatirGercekVeritabanindaReddedilir hesabın sepetin
// TÜM satırlarını kapsaması gerektiğini gerçek Postgres üzerinde doğrular.
//
// Sözleşmenin en pahalı ihlali budur: satırları göndermeyi unutan bir hesap
// turu, sepetin saklı satır tutarları sıfır olduğu için "subtotal 0, total 0"
// ile TUTARLI görünür. Kapsama zorunlu olmasaydı sepet gerçekten 0 tutarla
// yazılır ve MarkCompleted'dan da geçerdi.
func TestSetTotalsFiyatlanmamisSatirGercekVeritabanindaReddedilir(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	sepet := yeniSepet(ctx, t, svc)

	item, err := svc.AddLineItem(ctx, sepet.ID, service.AddLineItemInput{
		VariantID: "variant_FIYATSIZ", Title: "Tişört", Quantity: 3, UnitPrice: 100000,
	})
	require.NoError(t, err)
	revision, totalsRevision := sepetSurumu(ctx, t, sepet.ID)
	require.NotEqual(t, revision, totalsRevision, "hesaplanmamış sepet bayattır")

	err = svc.SetTotals(ctx, sepet.ID, service.Totals{Revision: revision})

	require.Error(t, err, "fiyatlanmamış satırı atlayan hesap kabul edilmemeli")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, service.CodeTotalsInconsistent, errors.CodeOf(err))
	assert.Contains(t, err.Error(), item.ID)

	// Ne toplam ne damga yazılmış olmalı.
	yeniRevision, yeniTotalsRevision := sepetSurumu(ctx, t, sepet.ID)
	assert.Equal(t, revision, yeniRevision)
	assert.Equal(t, totalsRevision, yeniTotalsRevision, "reddedilen tur damga atmamalı")

	_, err = svc.MarkCompleted(ctx, sepet.ID)
	require.Error(t, err, "fiyatlanmamış sepet tamamlanamamalı")
	assert.Equal(t, service.CodeTotalsStale, errors.CodeOf(err))

	// Satırların tutarı verilince aynı tur geçer.
	require.NoError(t, svc.SetTotals(ctx, sepet.ID, service.Totals{
		Revision: revision, Subtotal: 300000, Total: 300000,
		Lines: []service.LineTotals{
			{LineItemID: item.ID, UnitPrice: 100000, Subtotal: 300000, Total: 300000},
		},
	}))
	_, yeniTotalsRevision = sepetSurumu(ctx, t, sepet.ID)
	assert.Equal(t, revision, yeniTotalsRevision, "damga hesabın dayandığı şekille atılmalı")
}

// TestSetTotalsBayatHesapGercekVeritabanindaReddedilir hesabın dayandığı sepet
// şeklinin ÇAĞIRANDAN alındığını gerçek Postgres üzerinde doğrular.
//
// Senaryo, cart.go'nun savunduğunu iddia ettiği yarıştır: workflow sepeti okur,
// hesabını KİLİDİN DIŞINDA yapar, bu arada müşteri sepete satır ekler ve
// workflow bayat sonucu yazar. Damga yazma anındaki şekilden alınsaydı bayat
// hesap GÜNCEL diye damgalanır, MarkCompleted'ın bayatlık kapısı açılır ve
// müşteri sepetindeki maldan azını öderdi.
func TestSetTotalsBayatHesapGercekVeritabanindaReddedilir(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	sepet := yeniSepet(ctx, t, svc)

	ilk, err := svc.AddLineItem(ctx, sepet.ID, service.AddLineItemInput{
		VariantID: "variant_BAYAT_A", Title: "Tişört", Quantity: 1,
	})
	require.NoError(t, err)

	// Workflow okur ve hesabını BU şekle göre yapar.
	hesaplanan, err := svc.GetCart(ctx, sepet.ID)
	require.NoError(t, err)

	// Müşteri araya girer: ikinci satır eklenir.
	_, err = svc.AddLineItem(ctx, sepet.ID, service.AddLineItemInput{
		VariantID: "variant_BAYAT_B", Title: "Pantolon", Quantity: 1,
	})
	require.NoError(t, err)

	err = svc.SetTotals(ctx, sepet.ID, service.Totals{
		Revision: hesaplanan.Revision, Subtotal: 1000, Total: 1000,
		Lines: []service.LineTotals{
			{LineItemID: ilk.ID, UnitPrice: 1000, Subtotal: 1000, Total: 1000},
		},
	})

	require.Error(t, err, "bayat hesap kabul edilmemeli")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeTotalsStale, errors.CodeOf(err))

	revision, totalsRevision := sepetSurumu(ctx, t, sepet.ID)
	assert.Equal(t, int64(2), revision)
	assert.NotEqual(t, revision, totalsRevision, "bayat hesap sepeti TAZE damgalamamalı")

	_, err = svc.MarkCompleted(ctx, sepet.ID)
	require.Error(t, err, "bayat sepet tamamlanamamalı")
	assert.Equal(t, service.CodeTotalsStale, errors.CodeOf(err))

	// Workflow yeniden okuyup yeniden hesaplayınca tur kabul edilir.
	guncel, err := svc.GetCart(ctx, sepet.ID)
	require.NoError(t, err)
	require.NoError(t, svc.SetTotals(ctx, sepet.ID, service.Totals{
		Revision: guncel.Revision, Subtotal: 1500, Total: 1500,
		Lines: []service.LineTotals{
			{LineItemID: guncel.Items[0].ID, UnitPrice: 1000, Subtotal: 1000, Total: 1000},
			{LineItemID: guncel.Items[1].ID, UnitPrice: 500, Subtotal: 500, Total: 500},
		},
	}))
	_, err = svc.MarkCompleted(ctx, sepet.ID)
	require.NoError(t, err)
}

// TestMarkCompletedSatirsizSepetiReddeder satırsız bir sepetin
// tamamlanamadığını doğrular.
//
// Kural, "toplamlar HİÇ hesaplanmadı" deliğini de kapatır: yeni bir sepette
// revision ve totals_revision'ın ikisi de sıfırdır, yani bayatlık ölçütü
// sessizdir. Satır eklemek sayacı mutlaka artırdığı için, sayaçları eşit ve
// SATIRLI bir sepette hesap gerçekten koşmuştur; geriye yalnızca hiç
// dokunulmamış (ve zorunlu olarak satırsız) sepet kalır.
func TestMarkCompletedSatirsizSepetiReddeder(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	sepet := yeniSepet(ctx, t, svc)

	revision, totalsRevision := sepetSurumu(ctx, t, sepet.ID)
	require.Equal(t, revision, totalsRevision, "yeni sepette bayatlık ölçütü sessizdir")

	_, err := svc.MarkCompleted(ctx, sepet.ID)

	require.Error(t, err, "hiç hesaplanmamış sepet tamamlanamamalı")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeCartEmpty, errors.CodeOf(err))

	var tamamlandi bool
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT completed_at IS NOT NULL FROM carts WHERE id = $1`, sepet.ID).Scan(&tamamlandi))
	assert.False(t, tamamlandi, "reddedilen tamamlama damga atmamalı")
}

// TestUpdateCartMisafirSepetiMusteriyeDevreder misafir sepetin kayıtlı
// müşteriye devrini ve müşteri bağının GERÇEKTEN kurulduğunu doğrular.
func TestUpdateCartMisafirSepetiMusteriyeDevreder(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	sepet := yeniSepet(ctx, t, svc)
	require.True(t, sepet.Guest())

	_, err := svc.AddLineItem(ctx, sepet.ID, service.AddLineItemInput{
		VariantID: "variant_DEVIR", Title: "Tişört", Quantity: 1,
	})
	require.NoError(t, err)

	musteriID := "cust_DEVIR_" + models.NewCartID()
	posta := "Devir@Ornek.COM"
	guncel, err := svc.UpdateCart(ctx, sepet.ID, service.UpdateCartInput{
		Email: &posta, CustomerID: musteriID,
	})

	require.NoError(t, err)
	assert.Equal(t, "devir@ornek.com", guncel.Email)
	assert.Equal(t, musteriID, guncel.CustomerID)

	bagli, err := testLinks.List(ctx, service.LinkCartCustomer, sepet.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{musteriID}, bagli, "devir müşteri bağını da kurmalı")

	// Sepet artık müşteri süzgecinde görünür ve satırlarını korumuştur.
	_, count, err := svc.ListCarts(ctx, service.ListCartsInput{CustomerID: &musteriID})
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	detail, err := svc.GetCart(ctx, sepet.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1, "devir satırları kaybetmemeli")

	// Sepet BAŞKA bir müşteriye geçirilemez.
	_, err = svc.UpdateCart(ctx, sepet.ID, service.UpdateCartInput{CustomerID: "cust_BASKA"})
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeCustomerMismatch, errors.CodeOf(err))
}

// TestMisafirVeKayitliMusteriSepeti iki senaryonun da çalıştığını ve
// birbirinden ayırt edilebildiğini doğrular.
func TestMisafirVeKayitliMusteriSepeti(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	misafir, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: testRegionID, CurrencyCode: testCurrency, Email: "misafir@ornek.com",
	})
	require.NoError(t, err)
	assert.True(t, misafir.Guest())

	musteriID := "cust_" + misafir.ID
	kayitli, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: testRegionID, CustomerID: musteriID, CurrencyCode: testCurrency,
	})
	require.NoError(t, err)
	assert.False(t, kayitli.Guest())

	// Misafir sepeti müşteriye BAĞLANMAZ.
	bagli, err := testLinks.List(ctx, service.LinkCartCustomer, misafir.ID)
	require.NoError(t, err)
	assert.Empty(t, bagli, "misafir sepetinin müşteri bağı olmamalı")

	bagli, err = testLinks.List(ctx, service.LinkCartCustomer, kayitli.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{musteriID}, bagli)

	// Süzgeç ikisini ayırır.
	_, count, err := svc.ListCarts(ctx, service.ListCartsInput{CustomerID: &musteriID})
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

// TestLinklerGercektenKurulur bölge ve müşteri bağlarının link tablolarına
// GERÇEKTEN yazıldığını, silmede de kaldırıldığını doğrular.
func TestLinklerGercektenKurulur(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	musteriID := "cust_LINK_" + models.NewCartID()
	sepet, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: testRegionID, CustomerID: musteriID, CurrencyCode: testCurrency,
	})
	require.NoError(t, err)

	bolge, err := testLinks.List(ctx, service.LinkCartRegion, sepet.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{testRegionID}, bolge)

	musteri, err := testLinks.List(ctx, service.LinkCartCustomer, sepet.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{musteriID}, musteri)

	require.NoError(t, svc.DeleteCart(ctx, sepet.ID))

	bolge, err = testLinks.List(ctx, service.LinkCartRegion, sepet.ID)
	require.NoError(t, err)
	assert.Empty(t, bolge, "silinen sepetin bölge bağı kalmamalı")
	musteri, err = testLinks.List(ctx, service.LinkCartCustomer, sepet.ID)
	require.NoError(t, err)
	assert.Empty(t, musteri, "silinen sepetin müşteri bağı kalmamalı")
}

// TestAyniBolgedeCokSepetBaglanabilir link kardinalitesinin çok sepete izin
// verdiğini doğrular.
//
// Bu test kardinalite seçiminin BEKÇİSİDİR: cart_region ya da cart_customer
// OneToOne bildirilseydi, link tablosunun to_id benzersiz indeksi ikinci
// sepeti reddederdi ve bir bölgede yalnızca tek sepet açılabilirdi
// (bkz. service.Definitions).
func TestAyniBolgedeCokSepetBaglanabilir(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	musteriID := "cust_COK_" + models.NewCartID()

	for range 3 {
		_, err := svc.CreateCart(ctx, service.CreateCartInput{
			RegionID: testRegionID, CustomerID: musteriID, CurrencyCode: testCurrency,
		})
		require.NoError(t, err, "aynı bölge ve müşteri için ikinci sepet açılabilmeli")
	}

	_, count, err := svc.ListCarts(ctx, service.ListCartsInput{CustomerID: &musteriID})
	require.NoError(t, err)
	assert.Equal(t, int64(3), count)
}

// TestEszamanliAddLineItemSatirlariBozmaz aynı sepete aynı anda yapılan
// eklemelerin satırları bozmadığını doğrular.
//
// Aynı varyant için yarışan çağrılar TEK satır üretmeli ve adetler
// KAYBOLMADAN toplanmalıdır. Sepet kilidi olmasaydı iki çağrı da "satır yok"
// okur, ikisi de INSERT dener ve biri benzersiz indekse çarpardı (ya da indeks
// olmasaydı iki satır oluşurdu).
func TestEszamanliAddLineItemSatirlariBozmaz(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	sepet := yeniSepet(ctx, t, svc)

	const yarismaci = 12
	basla := make(chan struct{})
	sonuclar := make([]error, yarismaci)

	var wg sync.WaitGroup
	for i := range yarismaci {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-basla
			_, err := svc.AddLineItem(ctx, sepet.ID, service.AddLineItemInput{
				VariantID: "variant_YARIS", Title: "Tişört", Quantity: 1, UnitPrice: 1000,
			})
			sonuclar[i] = err
		}(i)
	}
	close(basla)
	wg.Wait()

	for i, err := range sonuclar {
		require.NoError(t, err, "eşzamanlı ekleme %d hata vermemeli", i)
	}

	detail, err := svc.GetCart(ctx, sepet.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1, "aynı varyant tek satırda toplanmalı")
	assert.Equal(t, int64(yarismaci), detail.Items[0].Quantity,
		"hiçbir adet kaybolmamalı")
	assert.Equal(t, int64(yarismaci), detail.Revision,
		"her yapısal değişiklik şekil sayacını bir artırmalı")
}

// TestEszamanliFarkliVaryantEklemesi farklı varyantların eşzamanlı eklenmesinin
// hepsini kaydettiğini doğrular.
func TestEszamanliFarkliVaryantEklemesi(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	sepet := yeniSepet(ctx, t, svc)

	const yarismaci = 10
	basla := make(chan struct{})
	sonuclar := make([]error, yarismaci)

	var wg sync.WaitGroup
	for i := range yarismaci {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-basla
			_, err := svc.AddLineItem(ctx, sepet.ID, service.AddLineItemInput{
				VariantID: fmt.Sprintf("variant_%02d", i),
				Title:     fmt.Sprintf("Ürün %d", i),
				Quantity:  int64(i + 1),
			})
			sonuclar[i] = err
		}(i)
	}
	close(basla)
	wg.Wait()

	for i, err := range sonuclar {
		require.NoError(t, err, "eşzamanlı ekleme %d hata vermemeli", i)
	}

	detail, err := svc.GetCart(ctx, sepet.ID)
	require.NoError(t, err)
	assert.Len(t, detail.Items, yarismaci, "her varyant kendi satırını almalı")
}

// TestTamamlanmisSepeteYazmaReddedilir tamamlanmış bir sepette tüm yazma
// yollarının veritabanı düzeyinde de reddedildiğini doğrular.
func TestTamamlanmisSepeteYazmaReddedilir(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	sepet := yeniSepet(ctx, t, svc)

	item, err := svc.AddLineItem(ctx, sepet.ID, service.AddLineItemInput{
		VariantID: "variant_TAMAM", Title: "Tişört", Quantity: 1,
	})
	require.NoError(t, err)
	guncel, err := svc.GetCart(ctx, sepet.ID)
	require.NoError(t, err)
	require.NoError(t, svc.SetTotals(ctx, sepet.ID, service.Totals{
		Revision: guncel.Revision,
		Subtotal: 1500, Total: 1500,
		Lines: []service.LineTotals{
			{LineItemID: item.ID, UnitPrice: 1500, Subtotal: 1500, Total: 1500},
		},
	}))
	_, err = svc.MarkCompleted(ctx, sepet.ID)
	require.NoError(t, err)

	yazmalar := map[string]func() error{
		"AddLineItem": func() error {
			_, err := svc.AddLineItem(ctx, sepet.ID, service.AddLineItemInput{
				VariantID: "variant_BASKA", Title: "Pantolon", Quantity: 1,
			})
			return err
		},
		"UpdateLineItemQuantity": func() error {
			_, err := svc.UpdateLineItemQuantity(ctx, sepet.ID, item.ID, 5)
			return err
		},
		"RemoveLineItem": func() error { return svc.RemoveLineItem(ctx, sepet.ID, item.ID) },
		"SetShippingAddress": func() error {
			_, e := svc.SetShippingAddress(ctx, sepet.ID, service.AddressInput{City: "İzmir"})
			return e
		},
		"AddShippingMethod": func() error {
			_, e := svc.AddShippingMethod(ctx, sepet.ID, service.AddShippingMethodInput{Name: "Hızlı"})
			return e
		},
		"SetTotals": func() error {
			return svc.SetTotals(ctx, sepet.ID, service.Totals{
				Revision: guncel.Revision, Subtotal: 1500, Total: 1500,
			})
		},
		"DeleteCart":    func() error { return svc.DeleteCart(ctx, sepet.ID) },
		"MarkCompleted": func() error { _, e := svc.MarkCompleted(ctx, sepet.ID); return e },
	}

	for ad, yazma := range yazmalar {
		t.Run(ad, func(t *testing.T) {
			err := yazma()
			require.Error(t, err, "%s tamamlanmış sepette hata dönmeli", ad)
			assert.Equal(t, errors.KindConflict, errors.KindOf(err),
				"%s Conflict dönmeli, aldığı: %v", ad, err)
		})
	}

	detail, err := svc.GetCart(ctx, sepet.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1)
	assert.Equal(t, int64(1), detail.Items[0].Quantity, "sepet gerçekten değişmemeli")
	assert.Equal(t, int64(1500), detail.Total)
}

// TestVeritabaniToplamKimliginiZorlar toplam kimliğinin veritabanı kısıtıyla da
// korunduğunu doğrular.
//
// Servis aynı kontrolü daha okunabilir bir hatayla önce yapar; buradaki kısıt
// SON SAVUNMADIR ve doğrudan SQL ile yapılan bir müdahaleyi de kapsar.
func TestVeritabaniToplamKimliginiZorlar(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	sepet := yeniSepet(ctx, t, svc)

	_, err := testPool.Pool().Exec(ctx,
		`UPDATE carts SET subtotal = 1000, total = 999 WHERE id = $1`, sepet.ID)

	require.Error(t, err, "kimliği bozan doğrudan güncelleme reddedilmeli")
	assert.Contains(t, err.Error(), "carts_totals_consistent")
}

// TestVeritabaniSatirBenzersizligiZorlar aynı varyanttan ikinci satırın
// veritabanı düzeyinde de açılamadığını doğrular.
func TestVeritabaniSatirBenzersizligiZorlar(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	sepet := yeniSepet(ctx, t, svc)

	_, err := svc.AddLineItem(ctx, sepet.ID, service.AddLineItemInput{
		VariantID: "variant_UNIQ", Title: "Tişört", Quantity: 1,
	})
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO cart_line_items (id, cart_id, variant_id, title, quantity)
         VALUES ($1, $2, 'variant_UNIQ', 'Kopya', 1)`,
		models.NewLineItemID(), sepet.ID)

	require.Error(t, err, "aynı varyant için ikinci satır açılamamalı")
	assert.Contains(t, err.Error(), "cart_line_items_cart_variant_uniq")
}

// TestYumusakSilmeOkumalaridanDuser yumuşak silinen sepetin okunmadığını
// doğrular (plan Bölüm 8).
func TestYumusakSilmeOkumalaridanDuser(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	sepet := yeniSepet(ctx, t, svc)

	require.NoError(t, svc.DeleteCart(ctx, sepet.ID))

	_, err := svc.GetCart(ctx, sepet.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))

	carts, err := svc.ListCartsByIDs(ctx, []string{sepet.ID})
	require.NoError(t, err)
	assert.Empty(t, carts, "silinen sepet toplu okumada da görünmemeli")

	// Satır fiziksel olarak DURUYOR olmalı: silme yumuşaktır.
	var silinmis bool
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT deleted_at IS NOT NULL FROM carts WHERE id = $1`, sepet.ID).Scan(&silinmis))
	assert.True(t, silinmis)
}

// TestModuleRegisterContaineraBaglar Register'ın sözleşmedeki üç şeyi de
// yaptığını doğrular: servis kaydı, Query sağlayıcısı ve link tanımları.
func TestModuleRegisterContaineraBaglar(t *testing.T) {
	ctx := context.Background()

	c := container.New(nil)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	links := link.New(testPool, nil)
	require.NoError(t, c.Provide("core.db", testPool))
	require.NoError(t, c.Provide("core.link", links))
	require.NoError(t, c.Provide("core.query", query.New(links, c, nil)))

	mod := cart.New()
	require.NoError(t, mod.Register(ctx, c))

	svc, err := container.Resolve[*service.Service](c, cart.ServiceName)
	require.NoError(t, err, "servis %q adıyla çözülebilmeli", cart.ServiceName)
	require.NotNil(t, svc)

	provider, err := container.Resolve[query.Provider](c, cart.ProviderName)
	require.NoError(t, err, "sağlayıcı %q adıyla çözülebilmeli", cart.ProviderName)
	assert.Equal(t, service.EntityName, provider.Entity(),
		"sağlayıcı adının öneki Entity() ile örtüşmeli (ADR 0004)")

	for _, def := range service.Definitions() {
		stored, defErr := links.Definition(ctx, def.Name)
		require.NoError(t, defErr, "%q link tanımı bildirilmiş olmalı", def.Name)
		assert.Equal(t, def.Cardinality, stored.Cardinality)
		assert.Equal(t, def.To.Module, stored.To.Module)
	}

	// Kaydedilen servis gerçekten çalışmalı.
	created, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: testRegionID, CurrencyCode: testCurrency,
	})
	require.NoError(t, err)

	records, err := provider.FetchByIDs(ctx, []string{created.ID}, nil)
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, created.ID, records[0][query.IDField])
}

// TestQueryKatmaniSepetiOkur gerçek Query katmanının sepet sağlayıcısını
// bulabildiğini doğrular.
func TestQueryKatmaniSepetiOkur(t *testing.T) {
	ctx := context.Background()

	c := container.New(nil)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	links := link.New(testPool, nil)
	require.NoError(t, c.Provide("core.db", testPool))
	require.NoError(t, c.Provide("core.link", links))
	graph := query.New(links, c, nil)
	require.NoError(t, c.Provide("core.query", graph))

	mod := cart.New()
	require.NoError(t, mod.Register(ctx, c))

	svc := mod.Service()
	require.NotNil(t, svc)
	musteriID := "cust_QUERY_" + models.NewCartID()
	created, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: testRegionID, CustomerID: musteriID, CurrencyCode: testCurrency,
	})
	require.NoError(t, err)

	records, err := graph.Graph(ctx, query.GraphSpec{
		Entity:  service.EntityName,
		Fields:  []string{query.IDField, service.FieldCustomerID, service.FieldTotal},
		Filters: map[string]any{service.FieldCustomerID: musteriID},
	})

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, created.ID, records[0][query.IDField])
	assert.Equal(t, musteriID, records[0][service.FieldCustomerID])
}
