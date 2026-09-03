//go:build integration

// Bu dosyadaki testler gerçek bir PostgreSQL örneği (dolayısıyla Docker)
// gerektirir; `make test` hızlı kalsın diye `integration` etiketiyle
// ayrılmıştır. Çalıştırmak için: make test-integration
//
// Birim testleri sahte bir depo ile servisin KARARLARINI kanıtlar. Buradaki
// testler kararların dayandığı ZEMİNİ kanıtlar: migration'ın geri
// alınabildiğini, kısıtların gerçekten uygulandığını ve eşzamanlılık
// iddiasının veritabanı düzeyinde tuttuğunu.
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

	return m.Run()
}

// yeniServis gerçek depo üzerinde çalışan bir servis kurar.
func yeniServis(t *testing.T) *service.Service {
	t.Helper()

	svc, err := service.New(service.Options{Repo: repository.New(testPool.Pool())})
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
// müşteriye devrini ve devrin ardından müşteri süzgecinde göründüğünü
// doğrular.
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

	// Misafir sepetinin müşteri sütunu BOŞ kalır; kayıtlı sepetinki dolar.
	assert.Empty(t, misafir.CustomerID, "misafir sepetinin müşterisi olmamalı")
	assert.Equal(t, musteriID, kayitli.CustomerID)

	// Süzgeç ikisini ayırır.
	_, count, err := svc.ListCarts(ctx, service.ListCartsInput{CustomerID: &musteriID})
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

// TestAyniBolgedeCokSepetAcilabilir bir bölge ve müşteri için birden çok
// sepet açılabildiğini doğrular.
//
// Bu, sepetin tabiatıdır: bir müşterinin zaman içinde birden çok sepeti olur ve
// bir bölgede binlerce sepet bulunur. Şemanın herhangi bir yerinde bölge ya da
// müşteri başına TEKİLLİK dayatan bir indeks bu testi düşürür.
func TestAyniBolgedeCokSepetAcilabilir(t *testing.T) {
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

// TestModuleRegisterContaineraBaglar Register'ın sözleşmedeki iki şeyi de
// yaptığını doğrular: servis kaydı ve Query sağlayıcısı.
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

// satirTutarlari bir satırın PARA alanlarını doğrudan sorguyla okur.
//
// Servis üzerinden değil doğrudan okunur: sınanan şey, tutarların
// veritabanındaki hangi SATIRA yazıldığıdır ve servisin kendi okuması yazma ile
// aynı eşleştirme varsayımını taşıdığı için bağımsız bir tanık olmazdı.
func satirTutarlari(ctx context.Context, t *testing.T, lineID string) models.LineTotals {
	t.Helper()

	var out models.LineTotals
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT unit_price, subtotal, discount_total, tax_total, total
         FROM cart_line_items WHERE id = $1`, lineID).
		Scan(&out.UnitPrice, &out.Subtotal, &out.DiscountTotal, &out.TaxTotal, &out.Total))
	return out
}

// TestSetTotalsHerSatiraKENDITutariniYazar toplu yazmanın tutarları DOĞRU
// satırlarla eşleştirdiğini gerçek Postgres üzerinde doğrular.
//
// Toplu UPDATE altı paralel dizi gönderir (kimlikler ve beş para alanı) ve
// eşleştirme yalnızca dizilerin SIRASINA dayanır. Sıra kayarsa hiçbir kapı
// ötmez: sepetin ara toplamı yine satırların toplamıdır, satır kimliği
// (total = subtotal - discount + tax) yine sağlanır, veritabanının
// cart_line_items_totals_consistent kısıtı yine geçer. Bozulan tek şey
// müşteriden alınan paradır.
//
// Fikstür bunu görünür kılar: her satırın adedi, birim fiyatı, indirimi ve
// vergisi FARKLIDIR, yani sıranın bir adım kayması bile her satırın saklanan
// dörtlüsünü değiştirir.
func TestSetTotalsHerSatiraKENDITutariniYazar(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	sepet := yeniSepet(ctx, t, svc)

	const satirSayisi = 12
	bekleniyor := make(map[string]models.LineTotals, satirSayisi)
	lines := make([]service.LineTotals, 0, satirSayisi)
	var sepetAra, sepetIndirim, sepetVergi int64
	for i := range satirSayisi {
		adet := int64(i + 1)
		item, err := svc.AddLineItem(ctx, sepet.ID, service.AddLineItemInput{
			VariantID: fmt.Sprintf("variant_ESLESME_%d", i), Title: "Ürün", Quantity: adet,
		})
		require.NoError(t, err)

		tutar := models.LineTotals{
			UnitPrice:     int64(100 * (i + 1)),
			DiscountTotal: int64(3 * i),
			TaxTotal:      int64(7 * (i + 1)),
		}
		tutar.Subtotal = tutar.UnitPrice * adet
		tutar.Total = tutar.Subtotal - tutar.DiscountTotal + tutar.TaxTotal

		bekleniyor[item.ID] = tutar
		lines = append(lines, service.LineTotals{
			LineItemID: item.ID, UnitPrice: tutar.UnitPrice, Subtotal: tutar.Subtotal,
			DiscountTotal: tutar.DiscountTotal, TaxTotal: tutar.TaxTotal, Total: tutar.Total,
		})
		sepetAra += tutar.Subtotal
		sepetIndirim += tutar.DiscountTotal
		sepetVergi += tutar.TaxTotal
	}

	detay, err := svc.GetCart(ctx, sepet.ID)
	require.NoError(t, err)
	require.NoError(t, svc.SetTotals(ctx, sepet.ID, service.Totals{
		Revision: detay.Revision,
		Subtotal: sepetAra, DiscountTotal: sepetIndirim, TaxTotal: sepetVergi,
		Total: sepetAra - sepetIndirim + sepetVergi,
		Lines: lines,
	}))

	for id, tutar := range bekleniyor {
		assert.Equal(t, tutar, satirTutarlari(ctx, t, id),
			"satır %s kendi tutarlarını almalı", id)
	}
}

// TestSetTotalsBuyukTutarlarTamSayiKalir en büyük izinli tutarın dizi gidiş
// dönüşünden BOZULMADAN geçtiğini doğrular.
//
// Toplu yazma para alanlarını bigint[] dizileriyle taşır. Para her zaman TAM
// SAYI minor unit'tir; dizi yolunda bir kayan nokta dönüşümü olsaydı 10^18
// mertebesindeki bir tutar sessizce yuvarlanır ve fark ancak muhasebede
// görünürdü. models.MaxTotal (10^18) float64'ün tam gösterebildiği aralığın
// (2^53 ≈ 9×10^15) çok üstündedir, yani böyle bir dönüşüm burada MUTLAKA
// görünür.
func TestSetTotalsBuyukTutarlarTamSayiKalir(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	sepet := yeniSepet(ctx, t, svc)

	// MaxTotal = MaxAmount × MaxQuantity; ara toplam çarpımı da doğrulandığı
	// için satır tam olarak bu adet ve birim fiyatla kurulur.
	item, err := svc.AddLineItem(ctx, sepet.ID, service.AddLineItemInput{
		VariantID: "variant_BUYUK", Title: "Pahalı", Quantity: models.MaxQuantity,
		UnitPrice: models.MaxAmount,
	})
	require.NoError(t, err)

	detay, err := svc.GetCart(ctx, sepet.ID)
	require.NoError(t, err)
	require.NoError(t, svc.SetTotals(ctx, sepet.ID, service.Totals{
		Revision: detay.Revision,
		Subtotal: models.MaxTotal, Total: models.MaxTotal,
		Lines: []service.LineTotals{{
			LineItemID: item.ID, UnitPrice: models.MaxAmount,
			Subtotal: models.MaxTotal, Total: models.MaxTotal,
		}},
	}))

	assert.Equal(t, models.LineTotals{
		UnitPrice: models.MaxAmount, Subtotal: models.MaxTotal, Total: models.MaxTotal,
	}, satirTutarlari(ctx, t, item.ID), "10^18 mertebesindeki tutar bit bit korunmalı")
}

// TestSetLineItemTotalsBaskaSepetinSatirinaYazamaz toplu yazmanın sepet
// sınırını AŞAMADIĞINI doğrular.
//
// Satır başına UPDATE'te sınır sorgunun WHERE'indeydi ve satır bulunamayınca
// NotFound dönüyordu. Toplu biçimde kimlikler bir dizi olarak gelir; cart_id
// koşulu düşseydi ya da eşleştirmeye kaysaydı, bir sepet İÇİN yapılan hesap
// BAŞKA bir sepetin satırına yazılabilirdi.
//
// Depo doğrudan çağrılır: servis satır kümesini kilit altında okuyup kapsama
// aradığı için bu isteği zaten üretemez. Sınanan şey, servisin ALTINDAKİ
// katmanın kendi başına güvenli olduğudur.
func TestSetLineItemTotalsBaskaSepetinSatirinaYazamaz(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	repo := repository.New(testPool.Pool())

	kurban := yeniSepet(ctx, t, svc)
	kurbanSatiri, err := svc.AddLineItem(ctx, kurban.ID, service.AddLineItemInput{
		VariantID: "variant_KURBAN", Title: "Kurban", Quantity: 1, UnitPrice: 1000,
	})
	require.NoError(t, err)

	digeri := yeniSepet(ctx, t, svc)
	digeriSatiri, err := svc.AddLineItem(ctx, digeri.ID, service.AddLineItemInput{
		VariantID: "variant_DIGERI", Title: "Diğeri", Quantity: 1, UnitPrice: 1000,
	})
	require.NoError(t, err)

	// Yazmadan ÖNCEKİ hâl tanık olarak alınır: AddLineItem satırın ilk birim
	// fiyatını zaten yazdığı için "hiç yazılmamış" sıfır demek değildir.
	oncekiKurban := satirTutarlari(ctx, t, kurbanSatiri.ID)
	oncekiDigeri := satirTutarlari(ctx, t, digeriSatiri.ID)

	// Diğer sepetin turu, kurbanın satırını da yazmaya çalışıyor.
	err = repo.WithTx(ctx, func(ctx context.Context) error {
		return repo.SetLineItemTotals(ctx, digeri.ID, []models.LineItemTotals{
			{LineItemID: digeriSatiri.ID, Totals: models.LineTotals{
				UnitPrice: 4242, Subtotal: 4242, Total: 4242}},
			{LineItemID: kurbanSatiri.ID, Totals: models.LineTotals{
				UnitPrice: 1, Subtotal: 1, Total: 1}},
		})
	})

	require.Error(t, err, "başka sepetin satırı yazılamamalı")
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
	assert.Contains(t, err.Error(), kurbanSatiri.ID, "hata hangi satırın yazılamadığını söylemeli")

	assert.Equal(t, oncekiKurban, satirTutarlari(ctx, t, kurbanSatiri.ID),
		"kurbanın satırı DEĞİŞMEMELİ")
	assert.Equal(t, oncekiDigeri, satirTutarlari(ctx, t, digeriSatiri.ID),
		"tur düştüğü için çağıranın kendi satırı da yazılmamalı")
}

// TestSetLineItemTotalsEksikSatiriCAGIRAN_SIRASIYLA_Adlandirir hata mesajının
// yazılamayan İLK satırı — çağıranın verdiği sırayla — adlandırdığını doğrular.
//
// Sıra bir sözleşmedir ve gerekçesi repository'deki firstUnwritten godoc'unda
// yazılıdır (ayraçsız: sembol dışa açık değil ve bu paket onu göremez):
// PostgreSQL
// RETURNING sırasını garanti etmez, dolayısıyla mesajın yeniden üretilebilir
// olmasının tek dayanağı çağıranın dilimidir. Aynı girdiye farklı mesaj veren
// bir hata, operatörün iki arızayı ayırt etmesini imkânsız kılar.
//
// Testin iki eksik satırı olması ŞART: tek eksikli bir fikstürde "ilk" ile
// "son" aynı kimliktir ve sözleşme sınanmamış olur (mutasyonla doğrulandı —
// son eksik satırı dönen sürüm tek eksikli testleri geçiyordu).
func TestSetLineItemTotalsEksikSatiriCAGIRAN_SIRASIYLA_Adlandirir(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	repo := repository.New(testPool.Pool())
	sepet := yeniSepet(ctx, t, svc)

	kalan, err := svc.AddLineItem(ctx, sepet.ID, service.AddLineItemInput{
		VariantID: "variant_KALAN", Title: "Kalan", Quantity: 1, UnitPrice: 1000,
	})
	require.NoError(t, err)
	ilkEksik, err := svc.AddLineItem(ctx, sepet.ID, service.AddLineItemInput{
		VariantID: "variant_EKSIK_A", Title: "Eksik A", Quantity: 1, UnitPrice: 1000,
	})
	require.NoError(t, err)
	ikinciEksik, err := svc.AddLineItem(ctx, sepet.ID, service.AddLineItemInput{
		VariantID: "variant_EKSIK_B", Title: "Eksik B", Quantity: 1, UnitPrice: 1000,
	})
	require.NoError(t, err)

	for _, id := range []string{ilkEksik.ID, ikinciEksik.ID} {
		_, err = testPool.Pool().Exec(ctx,
			`UPDATE cart_line_items SET deleted_at = now() WHERE id = $1`, id)
		require.NoError(t, err)
	}

	tutar := models.LineTotals{UnitPrice: 5000, Subtotal: 5000, Total: 5000}
	err = repo.WithTx(ctx, func(ctx context.Context) error {
		return repo.SetLineItemTotals(ctx, sepet.ID, []models.LineItemTotals{
			{LineItemID: kalan.ID, Totals: tutar},
			{LineItemID: ilkEksik.ID, Totals: tutar},
			{LineItemID: ikinciEksik.ID, Totals: tutar},
		})
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
	assert.Contains(t, err.Error(), ilkEksik.ID,
		"çağıranın sırasındaki İLK eksik satır adlandırılmalı")
	assert.NotContains(t, err.Error(), ikinciEksik.ID,
		"ikinci eksik satır adlandırılmamalı; mesaj tek ve yeniden üretilebilir olmalı")
}

// TestSetLineItemTotalsSatirsizTurHatasizGecer satırsız bir turun HATA
// ÜRETMEDİĞİNİ doğrular.
//
// Yol ölü değildir: son satırı da kaldırılmış bir sepet yeniden fiyatlanınca
// hesap turu sıfır satırla gelir ve o tur geçmek zorundadır — düşseydi müşteri
// sepetini boşalttığı anda sepeti hesaplanamaz hâle gelirdi. Erken dönüş
// mutasyonla doğrulandı: hata döndüren sürüm başka hiçbir testi düşürmüyordu.
func TestSetLineItemTotalsSatirsizTurHatasizGecer(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	repo := repository.New(testPool.Pool())
	sepet := yeniSepet(ctx, t, svc)

	require.NoError(t, repo.WithTx(ctx, func(ctx context.Context) error {
		return repo.SetLineItemTotals(ctx, sepet.ID, nil)
	}))

	revision, _ := sepetSurumu(ctx, t, sepet.ID)
	require.NoError(t, svc.SetTotals(ctx, sepet.ID, service.Totals{Revision: revision}),
		"satırsız sepetin hesabı yazılabilmeli")
}

// TestSetLineItemTotalsEksikSatirTuruDusurur bir satır yazılamadığında turun
// TAMAMEN geri alındığını doğrular.
//
// Toplu UPDATE eşleşmeyen kimliği sessizce ATLAR: silinmiş bir satır ya da hiç
// olmayan bir kimlik hata üretmez, yalnızca daha az satır yazılır. Sessiz
// kalsaydı sepetin ara toplamı ile satırlarının toplamı ayrışır ve müşteriye
// yanlış tutar tahsil edilirdi. Bu yüzden yazılan kimlikler istenenlerle
// karşılaştırılır ve eksik varsa işlem geri alınır.
//
// Kural bugün İKİNCİ savunmadır — servis satır kümesini sepetin kilidi altında
// okur ve sepeti değiştiren her yol aynı kilidi alır — ama kilidi atlayan bir
// yolun (doğrudan SQL, ileride eklenecek bir akış) sessiz kalmaması için burada
// sabitlenir.
func TestSetLineItemTotalsEksikSatirTuruDusurur(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	repo := repository.New(testPool.Pool())
	sepet := yeniSepet(ctx, t, svc)

	kalan, err := svc.AddLineItem(ctx, sepet.ID, service.AddLineItemInput{
		VariantID: "variant_KALAN", Title: "Kalan", Quantity: 1, UnitPrice: 1000,
	})
	require.NoError(t, err)
	silinen, err := svc.AddLineItem(ctx, sepet.ID, service.AddLineItemInput{
		VariantID: "variant_SILINEN", Title: "Silinen", Quantity: 1, UnitPrice: 1000,
	})
	require.NoError(t, err)

	// Satır, servisin kilidini ATLAYARAK siliniyor: sınanan şey tam olarak
	// böyle bir yolun sessiz kalmamasıdır.
	_, err = testPool.Pool().Exec(ctx,
		`UPDATE cart_line_items SET deleted_at = now() WHERE id = $1`, silinen.ID)
	require.NoError(t, err)

	// Yazmadan ÖNCEKİ hâl tanık olarak alınır: AddLineItem satırın ilk birim
	// fiyatını zaten yazdığı için "hiç yazılmamış" sıfır demek değildir.
	oncekiKalan := satirTutarlari(ctx, t, kalan.ID)

	err = repo.WithTx(ctx, func(ctx context.Context) error {
		return repo.SetLineItemTotals(ctx, sepet.ID, []models.LineItemTotals{
			{LineItemID: kalan.ID, Totals: models.LineTotals{
				UnitPrice: 5000, Subtotal: 5000, Total: 5000}},
			{LineItemID: silinen.ID, Totals: models.LineTotals{
				UnitPrice: 7000, Subtotal: 7000, Total: 7000}},
		})
	})

	require.Error(t, err, "eksik yazılan tur sessiz geçmemeli")
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
	assert.Contains(t, err.Error(), silinen.ID, "hata yazılamayan satırı adlandırmalı")

	assert.Equal(t, oncekiKalan, satirTutarlari(ctx, t, kalan.ID),
		"tur düştüğü için YAŞAYAN satır da yazılmamalı: ya hepsi ya hiçbiri")
}

// TestSetLineItemTotalsAyniSatiriIkiKezYazamaz aynı kimliğin bir turda iki kez
// verilemediğini doğrular.
//
// UPDATE ... FROM bir hedef satır birden çok kaynak satırla eşleştiğinde HANGİ
// kaynağın kazandığını tanımlamaz: sepet iki tutardan birini rastgele alırdı ve
// hangisi olduğu plana bağlı olurdu. Servis bunu zaten eler; depo katmanının da
// elemesi, deyimin tanımsız davranışını bu paketten çıkarır.
func TestSetLineItemTotalsAyniSatiriIkiKezYazamaz(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	repo := repository.New(testPool.Pool())
	sepet := yeniSepet(ctx, t, svc)

	item, err := svc.AddLineItem(ctx, sepet.ID, service.AddLineItemInput{
		VariantID: "variant_TEKRAR", Title: "Tekrar", Quantity: 1, UnitPrice: 1000,
	})
	require.NoError(t, err)
	oncekiTutar := satirTutarlari(ctx, t, item.ID)

	err = repo.WithTx(ctx, func(ctx context.Context) error {
		return repo.SetLineItemTotals(ctx, sepet.ID, []models.LineItemTotals{
			{LineItemID: item.ID, Totals: models.LineTotals{
				UnitPrice: 100, Subtotal: 100, Total: 100}},
			{LineItemID: item.ID, Totals: models.LineTotals{
				UnitPrice: 900, Subtotal: 900, Total: 900}},
		})
	})

	require.Error(t, err, "aynı satır için iki tutar kabul edilmemeli")
	assert.True(t, errors.IsInvalid(err), "sınıf Invalid olmalı: %v", err)
	assert.Equal(t, oncekiTutar, satirTutarlari(ctx, t, item.ID),
		"reddedilen tur hiçbir şey yazmamalı")
}
