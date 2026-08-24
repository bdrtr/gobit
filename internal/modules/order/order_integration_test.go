//go:build integration

// Bu dosyadaki testler gerçek bir PostgreSQL örneği (dolayısıyla Docker)
// gerektirir; `make test` hızlı kalsın diye `integration` etiketiyle
// ayrılmıştır. Çalıştırmak için: make test-integration
//
// Birim testleri sahte bir depo ile servisin KARARLARINI kanıtlar. Buradaki
// testler kararların dayandığı ZEMİNİ kanıtlar: migration'ın geri
// alınabildiğini, kısıtların gerçekten uygulandığını, link'lerin gerçekten
// kurulduğunu ve sipariş NUMARASININ eşzamanlı yazmalarda gerçekten benzersiz
// kaldığını. Özellikle "eşzamanlı 20 sipariş aynı numarayı almaz" iddiası
// yalnızca burada, gerçek goroutine'lerle gerçek bir sequence üzerinde
// sınanabilir.
package order_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/eventbus"
	"github.com/bdrtr/gobit/internal/core/link"
	"github.com/bdrtr/gobit/internal/modules/order"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/repository"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// Entegrasyon testlerinde kullanılan konteyner imajları.
const (
	postgresImage = "postgres:16-alpine"
	redisImage    = "redis:7-alpine"
)

// modulTablolari modülün sahip olduğu tablolardır; migration testleri bu
// listeyi kullanır.
var modulTablolari = []string{
	"orders", "order_line_items", "order_summaries",
	"order_returns", "order_exchanges", "order_claims",
}

// Test verisinde kullanılan sabitler. Bölge, müşteri ve varyant kimlikleri
// BAŞKA modüllere aittir; bu modül onların varlığını doğrulamaz (Prensip 2.2).
const (
	testRegionID   = "reg_TEST"
	testCustomerID = "cus_TEST"
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

	if err := db.Migrate(ctx, testDSN, order.New().Migrations(), order.ModuleName); err != nil {
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

// yeniServis gerçek depo, gerçek link servisi ve gerçek olay veri yolu üzerinde
// çalışan bir servis kurar; ikinci dönüş değeri veri yoludur.
func yeniServis(t *testing.T) (*service.Service, eventbus.EventBus) {
	t.Helper()
	return yeniServisDepoyla(t, repository.New(testPool.Pool()))
}

// yeniServisDepoyla verilen depo üzerinde çalışan bir servis kurar.
//
// Depo parametredir çünkü tek bir test gerçek deponun ÜSTÜNE bir sarmalayıcı
// koyar (bkz. [aramaBariyeriDepo]); geri kalan her şey aynıdır ve iki ayrı
// kurulum yazmak, birine eklenen bir bağımlılığın diğerinde unutulmasını davet
// ederdi.
func yeniServisDepoyla(t *testing.T, depo service.Store) (*service.Service, eventbus.EventBus) {
	t.Helper()

	bus := eventbus.NewInMemory(nil)
	t.Cleanup(func() {
		kapatmaCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := bus.Shutdown(kapatmaCtx); err != nil {
			t.Logf("olay veri yolu kapatılamadı: %v", err)
		}
	})

	svc, err := service.New(service.Options{
		Repo:   depo,
		Links:  testLinks,
		Events: bus,
	})
	require.NoError(t, err)
	return svc, bus
}

// gecerliGirdi tutarlı bir sipariş girdisi üretir.
func gecerliGirdi() service.CreateOrderInput {
	return service.CreateOrderInput{
		RegionID:      testRegionID,
		CustomerID:    testCustomerID,
		Email:         "musteri@ornek.com",
		CurrencyCode:  testCurrency,
		Subtotal:      3000,
		TaxTotal:      600,
		ShippingTotal: 2500,
		Total:         6100,
		Items: []service.CreateOrderItemInput{{
			VariantID: "variant_A", Title: "Kırmızı Tişört",
			Quantity: 3, UnitPrice: 1000, Subtotal: 3000, TaxTotal: 600, Total: 3600,
		}},
	}
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
//
// Test SIRAYA duyarlıdır ve diğer testlerden önce koşmalıdır: şemayı düşürüp
// yeniden kurar. Go testleri dosya içinde tanım sırasına göre koştuğu için
// dosyanın başındadır.
func TestMigrationGeriAlinabilir(t *testing.T) {
	ctx := context.Background()
	src := order.New().Migrations()

	for _, table := range modulTablolari {
		require.True(t, tabloVar(ctx, t, table), "%s başlangıçta var olmalı", table)
	}

	require.NoError(t, db.MigrateDown(ctx, testDSN, src, order.ModuleName, 0))
	for _, table := range modulTablolari {
		assert.False(t, tabloVar(ctx, t, table), "%s geri alma sonrası kalmamalı", table)
	}

	require.NoError(t, db.Migrate(ctx, testDSN, src, order.ModuleName))
	for _, table := range modulTablolari {
		assert.True(t, tabloVar(ctx, t, table), "%s yeniden uygulanmalı", table)
	}

	version, dirty, err := db.Version(ctx, testDSN, order.ModuleName)
	require.NoError(t, err)
	assert.False(t, dirty, "yarıda kalmış migration olmamalı")
	assert.Equal(t, uint(1), version)
}

// TestCrossModuleForeignKeyYok modülün tablolarındaki TÜM foreign key'lerin
// yine modülün kendi tablolarına gittiğini doğrular (Prensip 2.2).
//
// Özellikle orders.region_id (region), orders.customer_id (customer),
// orders.cart_id (cart) ve order_line_items.variant_id (product) başka
// modüllerin kimlikleridir ve foreign key OLAMAZ.
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

// TestSiparisYasamDongusu siparişin uçtan uca akışını doğrular: oluştur ->
// oku -> numarayla oku -> tamamla -> arşivle.
func TestSiparisYasamDongusu(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	in := gecerliGirdi()
	in.Email = "Musteri@Ornek.COM"
	in.CurrencyCode = "try"
	in.CartID = "cart_YASAM"
	in.Metadata = map[string]any{"kanal": "web"}

	siparis, err := svc.CreateOrder(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, "TRY", siparis.CurrencyCode)
	assert.Equal(t, "musteri@ornek.com", siparis.Email)
	assert.Equal(t, models.OrderPending, siparis.Status)
	assert.Positive(t, siparis.DisplayID, "numara veritabanı tarafından üretilmeli")
	assert.Equal(t, "UTC", siparis.PlacedAt.Location().String(), "zaman UTC olmalı")
	assert.Equal(t, map[string]any{"kanal": "web"}, siparis.Metadata)

	detay, err := svc.GetOrder(ctx, siparis.ID)
	require.NoError(t, err)
	require.Len(t, detay.Items, 1)
	assert.Equal(t, int64(3600), detay.Items[0].Total)
	assert.Equal(t, siparis.ID, detay.Summary.OrderID)
	assert.Equal(t, int64(6100), detay.Summary.Outstanding(detay.Total))

	numarayla, err := svc.GetOrderByDisplayID(ctx, siparis.DisplayID)
	require.NoError(t, err)
	assert.Equal(t, siparis.ID, numarayla.ID)

	// Bağlar gerçekten kurulmuş olmalı.
	bolgeler, err := testLinks.List(ctx, service.LinkOrderRegion, siparis.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{testRegionID}, bolgeler)
	musteriler, err := testLinks.List(ctx, service.LinkOrderCustomer, siparis.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{testCustomerID}, musteriler)

	tamamlanan, err := svc.CompleteOrder(ctx, siparis.ID)
	require.NoError(t, err)
	require.NotNil(t, tamamlanan.CompletedAt)
	assert.Equal(t, "UTC", tamamlanan.CompletedAt.Location().String())

	arsivlenen, err := svc.ArchiveOrder(ctx, siparis.ID)
	require.NoError(t, err)
	assert.Equal(t, models.OrderArchived, arsivlenen.Status)

	// Arşivlenmiş sipariş artık iptal edilemez.
	err = svc.CancelOrder(ctx, siparis.ID, "geç kalındı")
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "aldığı: %v", err)
}

// TestEszamanliSiparislerinNumaralariBenzersiz DoD'nin en kritik iddiasını
// gerçek yarış altında kanıtlar.
//
// Yirmi goroutine AYNI ANDA sipariş açar; hepsi tek bir bariyerde beklediği
// için yazmalar gerçekten çakışır. Numaralar uygulama katmanında "en büyüğü
// oku, bir ekle" ile üretilseydi bu testte MUTLAKA çakışma görülürdü;
// IDENTITY sütunu (sequence) ile çakışma yapısal olarak imkânsızdır.
func TestEszamanliSiparislerinNumaralariBenzersiz(t *testing.T) {
	const adet = 20

	ctx := context.Background()
	svc, _ := yeniServis(t)

	var (
		basla    sync.WaitGroup
		bitir    sync.WaitGroup
		sonuclar = make([]models.Order, adet)
		hatalar  = make([]error, adet)
	)
	basla.Add(1)
	bitir.Add(adet)

	for i := range adet {
		go func(idx int) {
			defer bitir.Done()
			basla.Wait()

			in := gecerliGirdi()
			in.CartID = fmt.Sprintf("cart_YARIS_%02d", idx)
			sonuclar[idx], hatalar[idx] = svc.CreateOrder(ctx, in)
		}(i)
	}

	basla.Done()
	bitir.Wait()

	numaralar := make(map[int64]string, adet)
	kimlikler := make(map[string]struct{}, adet)
	for i := range adet {
		require.NoError(t, hatalar[i], "%d. sipariş açılamadı", i)

		numara := sonuclar[i].DisplayID
		assert.True(t, models.ValidDisplayID(numara), "numara geçerli olmalı: %d", numara)

		onceki, cakisma := numaralar[numara]
		require.False(t, cakisma,
			"iki sipariş aynı numarayı aldı (%d): %s ve %s", numara, onceki, sonuclar[i].ID)
		numaralar[numara] = sonuclar[i].ID

		_, kimlikCakismasi := kimlikler[sonuclar[i].ID]
		require.False(t, kimlikCakismasi, "iki sipariş aynı kimliği aldı: %s", sonuclar[i].ID)
		kimlikler[sonuclar[i].ID] = struct{}{}
	}
	assert.Len(t, numaralar, adet, "tüm numaralar benzersiz olmalı")
}

// TestCancelOrderIkiKezCagrilabilir saga telafisinin idempotency'sini gerçek
// veritabanında doğrular (DoD şartı).
func TestCancelOrderIkiKezCagrilabilir(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	siparis, err := svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)

	require.NoError(t, svc.CancelOrder(ctx, siparis.ID, "ödeme reddedildi"))
	require.NoError(t, svc.CancelOrder(ctx, siparis.ID, "telafi tekrarı"),
		"ikinci iptal hata vermemeli")

	iptal, err := svc.GetOrder(ctx, siparis.ID)
	require.NoError(t, err)
	assert.Equal(t, models.OrderCanceled, iptal.Status)
	require.NotNil(t, iptal.CanceledAt)
	assert.Equal(t, "ödeme reddedildi", iptal.CancelReason,
		"ilk iptalin gerekçesi korunmalı")
}

// TestOrderPlacedOlayiGercektenYayimlanir DoD şartını GERÇEK bir abone ile
// doğrular.
//
// Birim testi sahte bir veri yolu üzerinde yayımı görür; burada sınanan şey,
// olayın gerçek [eventbus.EventBus] üzerinden bir aboneye TESLİM edildiğidir.
// InMemory backend'i handler'ları ayrı goroutine'lerde çalıştırır ve Publish
// onları BEKLEMEZ; bu yüzden test kanalla bekler.
func TestOrderPlacedOlayiGercektenYayimlanir(t *testing.T) {
	ctx := context.Background()
	svc, bus := yeniServis(t)

	teslim := make(chan eventbus.Event, 1)
	require.NoError(t, bus.Subscribe(service.EventOrderPlaced, func(_ context.Context, e eventbus.Event) error {
		teslim <- e
		return nil
	}))

	siparis, err := svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)

	select {
	case olay := <-teslim:
		assert.Equal(t, service.EventOrderPlaced, olay.Name)
		assert.Equal(t, siparis.ID, olay.Data[service.EventFieldOrderID])
		assert.Equal(t, strconv.FormatInt(siparis.DisplayID, 10),
			olay.Data[service.EventFieldDisplayID])
		assert.Equal(t, "6100", olay.Data[service.EventFieldTotal])
		assert.Equal(t, siparis.CurrencyCode, olay.Data[service.EventFieldCurrencyCode])
		assert.Equal(t, siparis.CustomerID, olay.Data[service.EventFieldCustomerID])
		assert.NotEmpty(t, olay.ID, "veri yolu olaya kimlik vermeli")
	case <-time.After(5 * time.Second):
		t.Fatal("order.placed olayı aboneye teslim edilmedi")
	}
}

// TestOrderPlacedOlayiRedisUzerindeTipDegistirmez olay yükünün ÜRETİM veri
// yolunda da aynı tiple ve aynı değerle ulaştığını doğrular.
//
// Ayrım yalnızca burada görünür: Redis Streams backend'i Data'yı json.Marshal
// ile yazar ve okurken map[string]any içine çözer, dolayısıyla int64 olarak
// konan bir alan aboneye float64 olarak ulaşırdı — InMemory backend'inde aynı
// alan int64 kalırdı. Sözleşmeye göre yazılmış bir abone geliştirmede çalışır,
// ÜRETİMDE düşerdi; üstelik 2^53 üstündeki tutarlar sessizce yuvarlanır, yani
// para float üzerinden geçerdi (plan Bölüm 8: float ASLA).
//
// Sipariş toplamı bilinçli olarak 2^53'ün üstündedir: float64'e uğrayan bir yol
// burada değeri değiştirir.
func TestOrderPlacedOlayiRedisUzerindeTipDegistirmez(t *testing.T) {
	const buyukTutar int64 = 9_007_199_254_740_993

	ctx := context.Background()
	bus := redisVeriYolu(t)

	svc, err := service.New(service.Options{
		Repo:   repository.New(testPool.Pool()),
		Links:  testLinks,
		Events: bus,
	})
	require.NoError(t, err)

	teslim := make(chan eventbus.Event, 1)
	require.NoError(t, bus.Subscribe(service.EventOrderPlaced,
		func(_ context.Context, e eventbus.Event) error {
			teslim <- e
			return nil
		}))

	in := gecerliGirdi()
	in.ShippingTotal = buyukTutar - 3600
	in.Total = buyukTutar
	siparis, err := svc.CreateOrder(ctx, in)
	require.NoError(t, err)

	select {
	case olay := <-teslim:
		for anahtar, deger := range olay.Data {
			assert.IsType(t, "", deger,
				"%q alanı Redis'ten dize olarak dönmeli", anahtar)
		}

		hamTutar, dize := olay.Data[service.EventFieldTotal].(string)
		require.True(t, dize, "tutar dize olarak taşınmalı")
		okunan, parseErr := strconv.ParseInt(hamTutar, 10, 64)
		require.NoError(t, parseErr)
		assert.Equal(t, siparis.Total, okunan, "tutar yuvarlanmadan geri okunmalı")
		assert.Equal(t, buyukTutar, okunan)
	case <-time.After(15 * time.Second):
		t.Fatal("order.placed olayı redis üzerinden teslim edilmedi")
	}
}

// redisVeriYolu test süresince yaşayan bir Redis üzerinde gerçek veri yolunu
// kurar.
//
// Konteyner yalnızca bu testin ihtiyacı olduğu için TestMain'de değil, burada
// açılır: diğer testlerin hiçbiri Redis'e dokunmaz ve paket başına ikinci bir
// konteyner her koşuya bedel eklerdi.
func redisVeriYolu(t *testing.T) eventbus.EventBus {
	t.Helper()

	ctx := t.Context()
	ctr, err := tcredis.Run(ctx, redisImage)
	testcontainers.CleanupContainer(t, ctr)
	require.NoError(t, err, "redis konteyneri başlatılamadı")

	uri, err := ctr.ConnectionString(ctx)
	require.NoError(t, err)
	opts, err := redis.ParseURL(uri)
	require.NoError(t, err)

	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Ping(ctx).Err())

	bus, err := eventbus.NewRedisStream(client, eventbus.RedisConfig{
		StreamPrefix: "gobit-test:" + t.Name(),
		Group:        "grup-" + t.Name(),
		Consumer:     "tuketici-1",
		BlockTimeout: 200 * time.Millisecond,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		kapatmaCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := bus.Shutdown(kapatmaCtx); err != nil {
			t.Logf("redis veri yolu kapatılamadı: %v", err)
		}
	})
	return bus
}

// TestToplamKisitiVeritabaninda tutarsız bir toplamın veritabanı düzeyinde de
// reddedildiğini doğrular.
//
// Servis aynı kontrolü daha okunabilir bir hatayla önce yapar; buradaki kısıt
// SON SAVUNMADIR ve doğrudan SQL ile yapılan müdahaleyi de kapsar.
func TestToplamKisitiVeritabaninda(t *testing.T) {
	ctx := context.Background()

	_, err := testPool.Pool().Exec(ctx,
		`INSERT INTO orders (id, region_id, currency_code, subtotal, tax_total, shipping_total, total)
         VALUES ('order_KISIT', $1, $2, 3000, 600, 2500, 9999)`, testRegionID, testCurrency)

	require.Error(t, err, "tutarsız toplam veritabanı kısıtına çarpmalı")
	assert.Contains(t, err.Error(), "orders_totals_consistent")
}

// TestAsiriIndirimKisitiVeritabaninda indirimin ara toplamı aşamayacağını
// veritabanı düzeyinde doğrular.
//
// Senaryo, kimlik kontrolünün TEK BAŞINA yetmediği durumdur: subtotal=1000,
// discount=3000, shipping=2500 -> total=500. Kimlik SAĞLANIR ve toplam negatif
// bile olmaz; yakalayan tek şey indirim sınırıdır.
func TestAsiriIndirimKisitiVeritabaninda(t *testing.T) {
	ctx := context.Background()

	_, err := testPool.Pool().Exec(ctx,
		`INSERT INTO orders (id, region_id, currency_code,
                             subtotal, discount_total, tax_total, shipping_total, total)
         VALUES ('order_INDIRIM', $1, $2, 1000, 3000, 0, 2500, 500)`, testRegionID, testCurrency)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "orders_discount_within_subtotal")
}

// TestNumaraBenzersizligiKisiti sequence elle geri sarılsa bile aynı numaranın
// ikinci kez yazılamadığını doğrular.
//
// display_id GENERATED ALWAYS olduğu için değer INSERT'te verilemez; kısıtı
// sınamanın tek yolu sequence'ı geri sarmaktır — kısıtın var oluş sebebi de tam
// olarak budur.
func TestNumaraBenzersizligiKisiti(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	ilk, err := svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)

	// Sequence'ı ilk siparişin numarasını yeniden üretecek şekilde geri sar.
	_, err = testPool.Pool().Exec(ctx,
		`SELECT setval(pg_get_serial_sequence('orders', 'display_id'), $1, false)`, ilk.DisplayID)
	require.NoError(t, err)

	_, err = svc.CreateOrder(ctx, gecerliGirdi())

	require.Error(t, err, "aynı numara ikinci kez yazılamamalı")
	assert.True(t, errors.IsConflict(err), "aldığı: %v", err)

	// Sequence'ı ileri sararak sonraki testleri etkilemekten kaçın.
	var enBuyuk int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT COALESCE(MAX(display_id), 0) FROM orders`).Scan(&enBuyuk))
	_, err = testPool.Pool().Exec(ctx,
		`SELECT setval(pg_get_serial_sequence('orders', 'display_id'), $1, true)`, enBuyuk)
	require.NoError(t, err)
}

// TestIdempotencyAnahtariArdisikCagridaUcuzYoluKullanir aynı anahtarla yapılan
// ARDIŞIK ikinci çağrının mevcut siparişi döndürdüğünü doğrular.
//
// Sınanan şey benzersiz İNDEKS DEĞİLDİR: ikinci çağrı CreateOrder'ın ucuz
// yolunda (anahtarla arama) kısa devre olur ve INSERT'e hiç ulaşmaz. İndeksin
// gerçekten yerinde olduğunu yalnızca eşzamanlı senaryo kanıtlar
// ([TestEszamanliAyniAnahtarlaTekSiparisAcilir]); ikisi birlikte, korumanın iki
// katmanını da kapsar.
func TestIdempotencyAnahtariArdisikCagridaUcuzYoluKullanir(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	in := gecerliGirdi()
	in.IdempotencyKey = "wf_" + models.NewOrderID()

	ilk, err := svc.CreateOrder(ctx, in)
	require.NoError(t, err)

	ikinci, err := svc.CreateOrder(ctx, in)
	require.NoError(t, err, "aynı anahtarla ikinci çağrı mevcut siparişi dönmeli")
	assert.Equal(t, ilk.ID, ikinci.ID)
	assert.Equal(t, ilk.DisplayID, ikinci.DisplayID)
}

// aramaBariyeriDepo idempotency ARAMASINDAN SONRA çağrıları tek noktada
// buluşturan depo sarmalayıcısıdır.
//
// Yalnızca [TestEszamanliAyniAnahtarlaTekSiparisAcilir] içindir ve orada
// gerekçesi anlatılan tek şeyi yapar: ucuz yolun aramasını yapan HERKESİN,
// hiçbiri yazmaya başlamadan önce "kayıt yok" görmesini sağlar. Geri kalan tüm
// metotlar gerçek depoya gider.
type aramaBariyeriDepo struct {
	*repository.Repository
	// adet bariyerde buluşması beklenen çağrı sayısıdır.
	adet int64
	// gelen bariyere ulaşan çağrıları sayar.
	gelen atomic.Int64
	// serbest sonuncusu geldiğinde KAPANIR; bekleyenlerin hepsi aynı anda
	// çözülür ve kapandıktan sonraki çağrılar hiç beklemez (yarışı kaybeden
	// çağrının yaptığı ikinci arama gibi).
	serbest chan struct{}
}

// GetOrderByIdempotencyKey aramayı yapar ve ilk [aramaBariyeriDepo.adet] çağrıyı
// bariyerde bekletir.
func (d *aramaBariyeriDepo) GetOrderByIdempotencyKey(ctx context.Context, key string) (models.Order, error) {
	order, err := d.Repository.GetOrderByIdempotencyKey(ctx, key)
	if d.gelen.Add(1) == d.adet {
		close(d.serbest)
	}
	<-d.serbest
	return order, err
}

// TestEszamanliAyniAnahtarlaTekSiparisAcilir modülün en kritik saga
// güvencesini GERÇEK indeks üzerinde kanıtlar.
//
// On altı goroutine AYNI idempotency anahtarıyla sipariş açmaya çalışır.
// Hepsi ucuz yolun aramasında kaydı bulamaz, hepsi yazmaya kalkar ve yalnızca
// birinin INSERT'i geçer; kalanlar orders_idempotency_key_uniq'e çarpıp kaydı
// yeniden okur ve AYNI siparişi döner.
//
// Ardışık çağrılarla kurulan bir test bunu gösteremez: ikinci çağrı ucuz yolda
// kısa devre olur ve INSERT'e hiç ulaşmaz, dolayısıyla indeks kaldırılsa bile
// yeşil kalırdı. Goroutine'leri yalnızca başlangıçta buluşturmak da yetmez —
// ölçüldü: kazanan o kadar hızlı commit ediyor ki kalanların ARAMASI kaydı
// buluyor ve yine kimse INSERT'e ulaşmıyor, yani indeks düşürülse bile test
// yeşil kalıyor.
//
// Bu yüzden bariyer aramanın SONRASINDADIR ([aramaBariyeriDepo]): on altı
// çağrının hepsi "kayıt yok" gördükten sonra serbest kalır ve hepsi YAZMAYA
// gider. Senaryo artık zamanlamaya değil yapıya bağlıdır ve indeks
// düşürüldüğünde MUTLAKA birden çok sipariş üretir.
func TestEszamanliAyniAnahtarlaTekSiparisAcilir(t *testing.T) {
	const adet = 16

	ctx := context.Background()

	svc, _ := yeniServisDepoyla(t, &aramaBariyeriDepo{
		Repository: repository.New(testPool.Pool()),
		adet:       adet,
		serbest:    make(chan struct{}),
	})

	anahtar := "wf_" + models.NewOrderID()

	var (
		bitir    sync.WaitGroup
		sonuclar = make([]models.Order, adet)
		hatalar  = make([]error, adet)
	)
	bitir.Add(adet)

	for i := range adet {
		go func(idx int) {
			defer bitir.Done()

			in := gecerliGirdi()
			in.IdempotencyKey = anahtar
			in.CartID = fmt.Sprintf("cart_IDEM_%02d", idx)
			sonuclar[idx], hatalar[idx] = svc.CreateOrder(ctx, in)
		}(i)
	}

	bitir.Wait()

	kimlikler := make(map[string]struct{}, adet)
	for i := range adet {
		require.NoError(t, hatalar[i], "%d. çağrı hata dönmemeli", i)
		kimlikler[sonuclar[i].ID] = struct{}{}
	}
	assert.Len(t, kimlikler, 1, "tüm çağrılar AYNI siparişi dönmeli: %v", kimlikler)

	var yazilan int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM orders WHERE idempotency_key = $1`, anahtar).Scan(&yazilan))
	assert.Equal(t, int64(1), yazilan, "aynı anahtarla yalnızca tek satır yazılmalı")

	// Dönen kimlik ÇÖZÜLEBİLİR olmalı: kaybeden çağrıların döndürdüğü sipariş
	// gerçekten vardır ve satırları da yerindedir.
	for id := range kimlikler {
		detay, err := svc.GetOrder(ctx, id)
		require.NoError(t, err, "dönen kimlik çözülebilmeli")
		assert.Len(t, detay.Items, 1)
	}
}

// TestOzetTutarlariGecikmisBildirimdeIadeyiSilmez özet yazımının SIRADAN
// BAĞIMSIZ olduğunu gerçek sorgu üzerinde doğrular.
//
// Birleştirme (GREATEST) sorgunun kendisindedir; birim testi sahtenin taklidini
// görür, burada sınanan şey PostgreSQL'in gerçekten öyle davrandığıdır.
func TestOzetTutarlariGecikmisBildirimdeIadeyiSilmez(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	siparis, err := svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)

	_, err = svc.SetOrderSummaryTotals(ctx, siparis.ID,
		service.SummaryTotalsInput{PaidTotal: 6100, RefundedTotal: 1000})
	require.NoError(t, err)

	// Gecikmiş tahsilat olayı yeniden işleniyor; iadeden haberi yok.
	gecikmis, err := svc.SetOrderSummaryTotals(ctx, siparis.ID,
		service.SummaryTotalsInput{PaidTotal: 6100, RefundedTotal: 0})
	require.NoError(t, err)
	assert.Equal(t, int64(1000), gecikmis.RefundedTotal,
		"kaydedilmiş iade gecikmiş bir bildirimle silinmemeli")

	var kayitli int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT refunded_total FROM order_summaries WHERE order_id = $1`, siparis.ID).Scan(&kayitli))
	assert.Equal(t, int64(1000), kayitli)
}

// TestSatisSonrasiKayitlariGercekVeritabaninda iade/değişim/hasar iskeletinin
// gerçek şema üzerinde çalıştığını doğrular.
func TestSatisSonrasiKayitlariGercekVeritabaninda(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	siparis, err := svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)

	iade, err := svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: siparis.ID, RefundAmount: 3600, Reason: "beden uymadı",
	})
	require.NoError(t, err)
	assert.Equal(t, models.ReturnRequested, iade.Status)

	degisim, err := svc.CreateExchange(ctx, service.CreateExchangeInput{
		OrderID: siparis.ID, DifferenceDue: -500,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(-500), degisim.DifferenceDue,
		"negatif fark veritabanında da saklanabilmeli")

	hasar, err := svc.CreateClaim(ctx, service.CreateClaimInput{
		OrderID: siparis.ID, Type: models.ClaimReplace, Reason: "kırık geldi",
	})
	require.NoError(t, err)
	assert.Equal(t, models.ClaimReplace, hasar.Type)

	iadeler, sayi, err := svc.ListReturns(ctx, siparis.ID, service.Page{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), sayi)
	require.Len(t, iadeler, 1)

	// Sipariş silinirse çocukları da düşer (modül içi ON DELETE CASCADE).
	_, err = testPool.Pool().Exec(ctx, `DELETE FROM orders WHERE id = $1`, siparis.ID)
	require.NoError(t, err)

	var kalan int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM order_returns WHERE order_id = $1`, siparis.ID).Scan(&kalan))
	assert.Zero(t, kalan, "sipariş silinince iade kaydı da düşmeli")
}

// TestOzetTutarlariGercekVeritabaninda özet yazımını gerçek şema üzerinde
// doğrular.
func TestOzetTutarlariGercekVeritabaninda(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	siparis, err := svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)

	ozet, err := svc.SetOrderSummaryTotals(ctx, siparis.ID,
		service.SummaryTotalsInput{PaidTotal: 6100, RefundedTotal: 1000})
	require.NoError(t, err)
	assert.Equal(t, int64(6100), ozet.PaidTotal)
	assert.Equal(t, int64(1000), ozet.RefundedTotal)
	assert.Equal(t, int64(1000), ozet.Outstanding(siparis.Total))

	// Tahsil edilmemiş tutarın iadesi veritabanı kısıtına da çarpar.
	_, err = testPool.Pool().Exec(ctx,
		`UPDATE order_summaries SET refunded_total = paid_total + 1 WHERE order_id = $1`, siparis.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "order_summaries_refund_within_paid")
}
