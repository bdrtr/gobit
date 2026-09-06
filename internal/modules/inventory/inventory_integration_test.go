//go:build integration

// Bu dosyadaki testler gerçek bir PostgreSQL örneği (dolayısıyla Docker)
// gerektirir; `make test` hızlı kalsın diye `integration` etiketiyle
// ayrılmıştır. Çalıştırmak için: make test-integration
//
// Birim testleri sahte bir depo ile servisin KARARLARINI kanıtlar. Buradaki
// testler kararların dayandığı ZEMİNİ kanıtlar: migration'ın geri alınabildiğini,
// kısıtların gerçekten uygulandığını ve eşzamanlılık iddiasının veritabanı
// düzeyinde tuttuğunu. Özellikle "iki eşzamanlı Reserve aynı son adedi alamaz"
// iddiası yalnızca burada, gerçek goroutine'lerle gerçek satır kilitleri
// üzerinde sınanabilir.
package inventory_test

import (
	"context"
	"fmt"
	"os"
	"slices"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/inventory"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/repository"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

const postgresImage = "postgres:16-alpine"

// modulTablolari modülün sahip olduğu tablolardır; migration testleri bu
// listeyi kullanır.
var modulTablolari = []string{
	"stock_locations", "inventory_items", "inventory_levels", "inventory_reservations",
}

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

	if err := db.Migrate(ctx, testDSN, inventory.New().Migrations(), inventory.ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "migration uygulanamadı: %v\n", err)
		return 1
	}

	return m.Run()
}

// yeniServis gerçek depo üzerinde çalışan bir servis kurar.
func yeniServis(t *testing.T) *service.Service {
	t.Helper()

	return service.New(repository.New(testPool.Pool()), nil)
}

// yeniKalem test için benzersiz SKU'lu bir stok kalemi oluşturur.
func yeniKalem(ctx context.Context, t *testing.T, svc *service.Service) models.InventoryItem {
	t.Helper()

	item, err := svc.CreateInventoryItem(ctx, service.CreateInventoryItemInput{
		SKU:   "SKU-" + models.NewInventoryItemID(),
		Title: t.Name(),
	})
	require.NoError(t, err)
	return item
}

// yeniLokasyon test için bir stok lokasyonu oluşturur.
func yeniLokasyon(ctx context.Context, t *testing.T, svc *service.Service) models.StockLocation {
	t.Helper()

	loc, err := svc.CreateStockLocation(ctx, service.CreateStockLocationInput{
		Name:        "Depo " + t.Name(),
		City:        "İstanbul",
		CountryCode: "TR",
	})
	require.NoError(t, err)
	return loc
}

// stoklu kalem, lokasyon ve verilen fiziksel adetle bir seviye kurar.
func stoklu(ctx context.Context, t *testing.T, svc *service.Service, adet int64) (models.InventoryItem, models.StockLocation) {
	t.Helper()

	item := yeniKalem(ctx, t, svc)
	loc := yeniLokasyon(ctx, t, svc)
	_, err := svc.SetInventoryLevel(ctx, item.ID, loc.ID, adet)
	require.NoError(t, err)
	return item, loc
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
	src := inventory.New().Migrations()

	for _, table := range modulTablolari {
		require.True(t, tabloVar(ctx, t, table), "%s başlangıçta var olmalı", table)
	}

	require.NoError(t, db.MigrateDown(ctx, testDSN, src, inventory.ModuleName, 0))
	for _, table := range modulTablolari {
		assert.False(t, tabloVar(ctx, t, table), "%s geri alma sonrası kalmamalı", table)
	}

	require.NoError(t, db.Migrate(ctx, testDSN, src, inventory.ModuleName))
	for _, table := range modulTablolari {
		assert.True(t, tabloVar(ctx, t, table), "%s yeniden uygulanmalı", table)
	}

	version, dirty, err := db.Version(ctx, testDSN, inventory.ModuleName)
	require.NoError(t, err)
	assert.False(t, dirty, "yarıda kalmış migration olmamalı")
	assert.Equal(t, uint(2), version)
}

// TestCrossModuleForeignKeyYok modülün tablolarındaki TÜM foreign key'lerin
// yine modülün kendi tablolarına gittiğini doğrular (Prensip 2.2).
//
// Özellikle inventory_reservations.line_item_id cart modülüne ait bir kimliktir
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

// TestKalemYasamDongusu kalem oluşturma, okuma, listeleme ve yumuşak silmeyi
// uçtan uca doğrular.
func TestKalemYasamDongusu(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	sku := "SKU-" + models.NewInventoryItemID()
	item, err := svc.CreateInventoryItem(ctx, service.CreateInventoryItemInput{
		SKU: sku, Title: "Kırmızı Tişört", Description: "M beden",
	})
	require.NoError(t, err)
	assert.True(t, item.RequiresShipping)
	assert.False(t, item.CreatedAt.IsZero(), "created_at veritabanından gelmeli")
	assert.Equal(t, item.CreatedAt.Location().String(), "UTC", "zaman UTC olmalı")

	okunan, err := svc.GetInventoryItem(ctx, item.ID)
	require.NoError(t, err)
	assert.Equal(t, item.ID, okunan.ID)
	assert.Equal(t, "Kırmızı Tişört", okunan.Title)

	// Aynı SKU ikinci kez alınamaz.
	_, err = svc.CreateInventoryItem(ctx, service.CreateInventoryItemInput{SKU: sku})
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))

	items, count, err := svc.ListInventoryItems(ctx, service.ListInventoryItemsInput{SKU: &sku})
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, int64(1), count)

	require.NoError(t, svc.DeleteInventoryItem(ctx, item.ID))

	_, err = svc.GetInventoryItem(ctx, item.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err), "yumuşak silinen kalem okunamamalı")

	_, count, err = svc.ListInventoryItems(ctx, service.ListInventoryItemsInput{SKU: &sku})
	require.NoError(t, err)
	assert.Zero(t, count, "yumuşak silinen kalem listede görünmemeli")

	// SKU yeniden kullanılabilir olmalı: benzersizlik yalnızca yaşayan
	// kalemler arasındadır.
	_, err = svc.CreateInventoryItem(ctx, service.CreateInventoryItemInput{SKU: sku})
	require.NoError(t, err, "silinen kalemin SKU'su yeniden kullanılabilmeli")
}

// TestStokSeviyesiVeSatilabilirAdet seviye yazma, düzeltme ve satılabilir
// adedin lokasyonlar arası toplamını doğrular.
func TestStokSeviyesiVeSatilabilirAdet(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	item := yeniKalem(ctx, t, svc)
	locA := yeniLokasyon(ctx, t, svc)
	locB := yeniLokasyon(ctx, t, svc)

	_, err := svc.SetInventoryLevel(ctx, item.ID, locA.ID, 10)
	require.NoError(t, err)
	_, err = svc.SetInventoryLevel(ctx, item.ID, locB.ID, 5)
	require.NoError(t, err)

	available, err := svc.AvailableQuantity(ctx, item.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(15), available)

	// Aynı çağrı ikinci kez seviye yaratmaz, günceller.
	level, err := svc.SetInventoryLevel(ctx, item.ID, locA.ID, 7)
	require.NoError(t, err)
	assert.Equal(t, int64(7), level.StockedQuantity)

	levels, err := svc.ListInventoryLevels(ctx, item.ID)
	require.NoError(t, err)
	assert.Len(t, levels, 2, "lokasyon başına tek seviye olmalı")

	level, err = svc.AdjustInventory(ctx, item.ID, locB.ID, -3)
	require.NoError(t, err)
	assert.Equal(t, int64(2), level.StockedQuantity)

	_, err = svc.AdjustInventory(ctx, item.ID, locB.ID, -3)
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err), "stok negatife düşemez")

	available, err = svc.AvailableQuantity(ctx, item.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(9), available, "7 + 2 = 9")
}

// TestOlmayanLokasyonaSeviyeAcilamaz sürücünün foreign key hatasının anlamlı
// bir tipli hataya çevrildiğini doğrular.
//
// Sınıflandırılmasaydı istemcinin düzeltebileceği bu durum 500 olarak görünür
// ve gerçek sebep yalnızca logda kalırdı.
func TestOlmayanLokasyonaSeviyeAcilamaz(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	item := yeniKalem(ctx, t, svc)

	_, err := svc.SetInventoryLevel(ctx, item.ID, "sloc_OLMAYAN", 5)

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
	assert.Equal(t, "inventory_location_not_found", errors.CodeOf(err))
}

// TestSatilabilirAdetSQLVeServisAyniSonucuVerir toplu (SQL tarafında
// hesaplanan) satılabilirlik ile tek kalem üzerinden (Go tarafında toplanan)
// hesabın aynı sayıyı verdiğini doğrular.
//
// İki yol da gerekli: biri Query sağlayıcısının tek turluk toplu yolu, diğeri
// tekil sorgunun yolu. Ayrışırlarsa stok, bakılan yere göre farklı görünürdü.
func TestSatilabilirAdetSQLVeServisAyniSonucuVerir(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	item, loc := stoklu(ctx, t, svc, 10)
	locB := yeniLokasyon(ctx, t, svc)
	_, err := svc.SetInventoryLevel(ctx, item.ID, locB.ID, 6)
	require.NoError(t, err)

	_, err = svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 4,
	})
	require.NoError(t, err)

	tekil, err := svc.AvailableQuantity(ctx, item.ID)
	require.NoError(t, err)

	toplu, err := svc.AvailableQuantities(ctx, []string{item.ID})
	require.NoError(t, err)

	assert.Equal(t, int64(12), tekil, "(10-4) + 6 = 12")
	assert.Equal(t, tekil, toplu[item.ID], "iki hesap yolu aynı sonucu vermeli")
}

// TestLocationsWithStockYeterliLokasyonlariSiraliDoner aday lokasyon listesini
// gerçek veritabanı üzerinde doğrular: eşiği karşılamayan lokasyon listede
// yoktur ve sıra lokasyon kimliğine göre artandır.
//
// Fikstür seviyeleri KİMLİK SIRASININ TERSİNE yazar. Sebep budur:
// ListInventoryLevels satırları created_at'e göre döndürür, yani deponun
// verdiği sıra beklenen sıranın tam tersidir. Seviyeler kimlik sırasında
// yazılsaydı iki sıra çakışır ve hiç sıralamayan bir uygulama da bu testi
// geçerdi.
func TestLocationsWithStockYeterliLokasyonlariSiraliDoner(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	item := yeniKalem(ctx, t, svc)
	ids := []string{
		yeniLokasyon(ctx, t, svc).ID,
		yeniLokasyon(ctx, t, svc).ID,
		yeniLokasyon(ctx, t, svc).ID,
	}
	slices.Sort(ids)

	adetler := map[string]int64{ids[0]: 10, ids[1]: 4, ids[2]: 6}
	for i := len(ids) - 1; i >= 0; i-- {
		_, err := svc.SetInventoryLevel(ctx, item.ID, ids[i], adetler[ids[i]])
		require.NoError(t, err)
	}

	locations, err := svc.LocationsWithStock(ctx, item.ID, 5)

	require.NoError(t, err)
	assert.Equal(t, []string{ids[0], ids[2]}, locations,
		"4 adetlik lokasyon eşiği karşılamıyor; kalanlar kimlik sırasında dönmeli")
	assert.True(t, slices.IsSorted(locations), "sıra deterministik olmalı")
}

// TestLocationsWithStockRezervasyonSonrasiAdaydanCikar rezervasyonun aday
// listesini düşürdüğünü ve serbest bırakmanın geri getirdiğini doğrular.
//
// Testin çekirdeği ortadaki iki iddiadır: liste ile [service.Service.Reserve]
// AYNI "satılabilir" tanımını kullanır. Ayrışsalardı, listede görünen bir
// lokasyon rezervasyonda Conflict alır ve saga adayı olan bir depoda sipariş
// veremediğini açıklayamazdı.
func TestLocationsWithStockRezervasyonSonrasiAdaydanCikar(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	item, loc := stoklu(ctx, t, svc, 10)

	locations, err := svc.LocationsWithStock(ctx, item.ID, 6)
	require.NoError(t, err)
	require.Equal(t, []string{loc.ID}, locations, "rezervasyondan önce aday olmalı")

	res, err := svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 5,
	})
	require.NoError(t, err)

	locations, err = svc.LocationsWithStock(ctx, item.ID, 6)
	require.NoError(t, err)
	assert.Empty(t, locations, "10-5=5 adet, 6'lık istek için yetmez")

	_, err = svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 6,
	})
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err),
		"listede olmayan lokasyon Reserve'de de reddedilmeli")

	locations, err = svc.LocationsWithStock(ctx, item.ID, 5)
	require.NoError(t, err)
	assert.Equal(t, []string{loc.ID}, locations, "kalan 5 adet için hâlâ aday")

	require.NoError(t, svc.ReleaseReservation(ctx, res.ID))

	locations, err = svc.LocationsWithStock(ctx, item.ID, 6)
	require.NoError(t, err)
	assert.Equal(t, []string{loc.ID}, locations, "serbest bırakma adaylığı geri getirmeli")
}

// TestLocationsWithStockAdaySizsaBosDilim aday yokken hata değil BOŞ dilim
// döndüğünü, olmayan kalem içinse NotFound döndüğünü doğrular.
//
// "Yeterli stok yok" bir arıza değil bir cevaptır; saga onu kendi bağlamında
// Conflict'e çevirmeyi seçer. Olmayan kalem ise çağıranın hatasıdır ve boş
// listeye karışmamalıdır.
func TestLocationsWithStockAdaySizsaBosDilim(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	stoksuz := yeniKalem(ctx, t, svc)
	locations, err := svc.LocationsWithStock(ctx, stoksuz.ID, 1)
	require.NoError(t, err)
	assert.Empty(t, locations, "hiç seviyesi olmayan kalem için aday yok")
	assert.NotNil(t, locations, "boş dilim dönmeli, nil değil")

	item, _ := stoklu(ctx, t, svc, 3)
	locations, err = svc.LocationsWithStock(ctx, item.ID, 4)
	require.NoError(t, err)
	assert.Empty(t, locations, "3 adet 4'lük isteği karşılamaz")
	assert.NotNil(t, locations, "boş dilim dönmeli, nil değil")

	_, err = svc.LocationsWithStock(ctx, "invitem_YOK", 1)
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestInteropLokasyonYuzeyiAdiylaCozulur modüller arası yüzeyin container'dan
// SABİT ADIYLA ve tüketicinin yazacağı DAR ARAYÜZLE çözülebildiğini doğrular.
//
// İmza bir sözleşmedir ve tüketici bu modülü import edemez (ADR 0006); bir
// kayma ancak çözüm anında görünür. Test o anı erkene çeker ve yüzeyi gerçek
// veriyle bir kez çağırarak kablolamanın da doğru olduğunu gösterir.
func TestInteropLokasyonYuzeyiAdiylaCozulur(t *testing.T) {
	ctx := context.Background()
	c := container.New(nil)
	require.NoError(t, c.Provide("core.db", testPool))
	require.NoError(t, inventory.New().Register(ctx, c))

	// Tüketicinin kendi paketinde yazacağı dar arayüzün birebir kopyası.
	type stockLocations interface {
		LocationsWithStock(ctx context.Context, inventoryItemID string, quantity int64) ([]string, error)
	}

	// Ad ELDE yazılır; sabiti kullanmak testi totolojiye çevirirdi.
	yuzey, err := container.Resolve[stockLocations](c, "inventory.interop")
	require.NoError(t, err, "yüzey sabit adıyla ve dar arayüzle çözülebilmeli")
	assert.Equal(t, "inventory.interop", inventory.InteropName,
		"ad değişirse tüketici akışlar yüzeyi bulamaz")

	svc := yeniServis(t)
	item, loc := stoklu(ctx, t, svc, 7)

	locations, err := yuzey.LocationsWithStock(ctx, item.ID, 7)
	require.NoError(t, err)
	assert.Equal(t, []string{loc.ID}, locations, "tam son adet de yeterlidir")

	locations, err = yuzey.LocationsWithStock(ctx, item.ID, 8)
	require.NoError(t, err)
	assert.Empty(t, locations)
}

// TestEszamanliReserveSonAdediTekKazanir eşzamanlılık iddiasının çekirdeğini
// kanıtlar: son bir adet için yarışan çok sayıda çağrıdan TAM OLARAK BİRİ
// kazanır.
//
// Uygulama katmanında yapılan bir "önce oku sonra yaz" kontrolü bu testi
// geçemez; kazananın tek olması satır kilidinden gelir.
func TestEszamanliReserveSonAdediTekKazanir(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	item, loc := stoklu(ctx, t, svc, 1)

	const yarismaci = 8
	basla := make(chan struct{})
	sonuclar := make([]error, yarismaci)

	var wg sync.WaitGroup
	for i := range yarismaci {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-basla
			_, err := svc.Reserve(ctx, service.ReserveInput{
				InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 1,
			})
			sonuclar[i] = err
		}(i)
	}
	close(basla)
	wg.Wait()

	var kazanan int
	for i, err := range sonuclar {
		if err == nil {
			kazanan++
			continue
		}
		assert.Equal(t, errors.KindConflict, errors.KindOf(err),
			"kaybeden çağrı %d Conflict almalı, aldığı: %v", i, err)
		assert.Equal(t, service.CodeInsufficientStock, errors.CodeOf(err))
	}
	assert.Equal(t, 1, kazanan, "son adedi tam olarak bir çağrı almalı")

	available, err := svc.AvailableQuantity(ctx, item.ID)
	require.NoError(t, err)
	assert.Zero(t, available)
	assert.Equal(t, int64(1), aktifRezervasyonAdedi(ctx, t, item.ID),
		"kazanan sayısı kadar rezervasyon kaydı olmalı")
}

// TestEszamanliReserveStoguAsmaz stoktan fazla eşzamanlı istek geldiğinde tam
// olarak stok kadarının kazandığını doğrular.
//
// Tek adetlik yarışın aksine burada birden çok kazanan vardır; iddia
// kazananların TOPLAM adedinin stoğu aşmamasıdır. Kilit yerine artımlı bir
// güncelleme kullanılsaydı rezerve adet stoğun üstüne çıkabilirdi.
func TestEszamanliReserveStoguAsmaz(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	const stok = 10
	const yarismaci = 25
	item, loc := stoklu(ctx, t, svc, stok)

	basla := make(chan struct{})
	sonuclar := make([]error, yarismaci)

	var wg sync.WaitGroup
	for i := range yarismaci {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-basla
			_, err := svc.Reserve(ctx, service.ReserveInput{
				InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 1,
			})
			sonuclar[i] = err
		}(i)
	}
	close(basla)
	wg.Wait()

	var kazanan int
	for _, err := range sonuclar {
		if err == nil {
			kazanan++
			continue
		}
		assert.Equal(t, errors.KindConflict, errors.KindOf(err), "beklenmeyen hata: %v", err)
	}
	assert.Equal(t, stok, kazanan)

	levels, err := svc.ListInventoryLevels(ctx, item.ID)
	require.NoError(t, err)
	require.Len(t, levels, 1)
	assert.Equal(t, int64(stok), levels[0].ReservedQuantity, "rezerve adet stoğu aşamaz")
	assert.Zero(t, levels[0].Available())
	assert.Equal(t, int64(stok), aktifRezervasyonAdedi(ctx, t, item.ID))
}

// TestReserveIleSeviyeYazmaKilitlenmez KARIŞIK akışların birbirini
// kilitlemediğini kanıtlar: aynı kalem üzerinde Reserve ile SetInventoryLevel
// gerçek goroutine'lerle yarıştırılır.
//
// Bu, tek tip yarışlardan (Reserve x N) BAŞKA bir hata sınıfıdır. İki akış aynı
// iki satırı ters sırada kilitlerse PostgreSQL kilitlenmeyi (SQLSTATE 40P01)
// saptar ve işlemlerden birini öldürür: müşterinin rezervasyonu, yöneticinin
// stok güncellemesiyle çakıştığı için — hem de kilitlenme zaman aşımı kadar
// bekledikten sonra — hata alır. Kilit sırası TEK olduğu sürece her tur temiz
// geçer; bu yüzden testin iddiası "hiçbir çağrı hata almaz"dır.
//
// Stok her turda yeniden 1000'e yazıldığı ve turda yalnızca 1 adet ayrıldığı
// için iş kuralı gereği düşecek bir çağrı YOKTUR; dolayısıyla görülen her hata
// eşzamanlılık hatasıdır.
func TestReserveIleSeviyeYazmaKilitlenmez(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	const stok int64 = 1000
	item, loc := stoklu(ctx, t, svc, stok)

	const tur = 40
	hatalar := make(chan error, 2*tur)
	for range tur {
		basla := make(chan struct{})
		var wg sync.WaitGroup

		wg.Add(2)
		go func() {
			defer wg.Done()
			<-basla
			_, err := svc.Reserve(ctx, service.ReserveInput{
				InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 1,
			})
			hatalar <- err
		}()
		go func() {
			defer wg.Done()
			<-basla
			_, err := svc.SetInventoryLevel(ctx, item.ID, loc.ID, stok)
			hatalar <- err
		}()

		close(basla)
		wg.Wait()
	}
	close(hatalar)

	for err := range hatalar {
		require.NoError(t, err, "Reserve ile SetInventoryLevel birbirini kilitlememeli")
	}
}

// TestReserveIleKalemSilmeKilitlenmez Reserve ile DeleteInventoryItem'i aynı
// kalem üzerinde yarıştırır ve iki şeyi birden kanıtlar: akışlar kilitlenmez ve
// yarışı TAM OLARAK BİRİ kazanır.
//
// Kazanan hangisi olursa olsun sonuç tutarlıdır: rezervasyon önce yazıldıysa
// silme "aktif rezervasyon var" diye Conflict alır, silme önce bittiyse
// rezervasyon kalemi bulamaz. İkisinin birden başarılı olması, silinmiş bir
// kalemin arkasında aktif rezervasyon bırakırdı.
func TestReserveIleKalemSilmeKilitlenmez(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	const tur = 25
	for range tur {
		item, loc := stoklu(ctx, t, svc, 10)

		basla := make(chan struct{})
		var hatalar [2]error
		var wg sync.WaitGroup

		wg.Add(2)
		go func() {
			defer wg.Done()
			<-basla
			_, hatalar[0] = svc.Reserve(ctx, service.ReserveInput{
				InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 1,
			})
		}()
		go func() {
			defer wg.Done()
			<-basla
			hatalar[1] = svc.DeleteInventoryItem(ctx, item.ID)
		}()

		close(basla)
		wg.Wait()

		if hatalar[0] != nil {
			assert.Equal(t, errors.KindNotFound, errors.KindOf(hatalar[0]),
				"rezervasyon yalnızca kalem silinmiş olduğu için düşebilir: %v", hatalar[0])
		}
		if hatalar[1] != nil {
			assert.Equal(t, errors.KindConflict, errors.KindOf(hatalar[1]),
				"silme yalnızca aktif rezervasyon yüzünden düşebilir: %v", hatalar[1])
			assert.Equal(t, service.CodeItemHasReservations, errors.CodeOf(hatalar[1]))
		}
		require.True(t, (hatalar[0] == nil) != (hatalar[1] == nil),
			"tam olarak biri kazanmalı (rezervasyon: %v, silme: %v)", hatalar[0], hatalar[1])
	}
}

// TestKilitlenmeConflictOlarakSiniflanir kilitlenme (40P01) hatasının tipli
// hataya çevrildiğini doğrular.
//
// Kilit sırası tekleştirildiği için normal akışlarda kilitlenme oluşmaz; bu
// test SON SAVUNMAYI sınar. İki işlem iki kalemi bilerek ters sırada kilitler.
// Sınıflandırma olmasaydı kurban işlem errors.Internal (HTTP 500) alırdı ve
// çağıran, isteğin yeniden denenebilir olduğunu anlayamazdı.
func TestKilitlenmeConflictOlarakSiniflanir(t *testing.T) {
	ctx := context.Background()
	repo := repository.New(testPool.Pool())
	svc := yeniServis(t)

	ilkKalem := yeniKalem(ctx, t, svc)
	ikinciKalem := yeniKalem(ctx, t, svc)

	ilkKilitli, ikinciKilitli := make(chan struct{}), make(chan struct{})
	hatalar := make(chan error, 2)

	go func() {
		hatalar <- repo.WithTx(ctx, func(ctx context.Context) error {
			if err := repo.LockInventoryItem(ctx, ilkKalem.ID); err != nil {
				return err
			}
			close(ilkKilitli)
			<-ikinciKilitli
			return repo.LockInventoryItem(ctx, ikinciKalem.ID)
		})
	}()
	go func() {
		hatalar <- repo.WithTx(ctx, func(ctx context.Context) error {
			if err := repo.LockInventoryItem(ctx, ikinciKalem.ID); err != nil {
				return err
			}
			close(ikinciKilitli)
			<-ilkKilitli
			return repo.LockInventoryItem(ctx, ilkKalem.ID)
		})
	}()

	var kurban int
	for range 2 {
		err := <-hatalar
		if err == nil {
			continue
		}
		kurban++
		assert.Equal(t, errors.KindConflict, errors.KindOf(err),
			"kilitlenme kurbanı yeniden denenebilir bir hata almalı, aldığı: %v", err)
		// Kod ELDE yazılır: sabiti kullanmak, sabit yanlış olsa bile geçen bir
		// totoloji üretirdi.
		assert.Equal(t, "inventory_concurrent_update", errors.CodeOf(err))
	}
	assert.Equal(t, 1, kurban, "kilitlenmede tam olarak bir işlem öldürülür")
}

// TestReserveReleaseReserveDongusu telafiden sonra adedin gerçekten yeniden
// satılabilir olduğunu doğrular. Faz 6'daki saga başarısız olup yeniden
// denendiğinde bu döngü yaşanır.
func TestReserveReleaseReserveDongusu(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	item, loc := stoklu(ctx, t, svc, 1)

	ilk, err := svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 1, LineItemID: "li_1",
	})
	require.NoError(t, err)

	_, err = svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 1,
	})
	require.Error(t, err, "son adet ayrılmışken ikinci rezervasyon olmamalı")

	require.NoError(t, svc.ReleaseReservation(ctx, ilk.ID))

	available, err := svc.AvailableQuantity(ctx, item.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), available, "telafi adedi geri vermeli")

	ikinci, err := svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 1,
	})
	require.NoError(t, err)
	assert.NotEqual(t, ilk.ID, ikinci.ID)

	serbest, err := svc.GetReservation(ctx, ilk.ID)
	require.NoError(t, err)
	assert.Equal(t, models.ReservationReleased, serbest.Status)
	assert.Equal(t, "li_1", serbest.LineItemID, "cart satırı kimliği korunmalı")
}

// TestReleaseReservationIdempotentVeritabaninda telafinin veritabanı üzerinde
// de idempotent olduğunu doğrular: ikinci çağrı hata vermez ve rezerve adedi
// ikinci kez düşürmez.
func TestReleaseReservationIdempotentVeritabaninda(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	item, loc := stoklu(ctx, t, svc, 5)

	res, err := svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 3,
	})
	require.NoError(t, err)

	require.NoError(t, svc.ReleaseReservation(ctx, res.ID))
	require.NoError(t, svc.ReleaseReservation(ctx, res.ID), "ikinci çağrı hata vermemeli")
	require.NoError(t, svc.ReleaseReservation(ctx, res.ID), "üçüncü çağrı da hata vermemeli")

	levels, err := svc.ListInventoryLevels(ctx, item.ID)
	require.NoError(t, err)
	require.Len(t, levels, 1)
	assert.Zero(t, levels[0].ReservedQuantity, "rezerve adet yalnızca bir kez düşmeli")
	assert.Equal(t, int64(5), levels[0].StockedQuantity)
	assert.Equal(t, int64(5), levels[0].Available(), "stok yoktan var edilmemeli")
}

// TestEszamanliReleaseTekSeferDuser aynı rezervasyonu aynı anda serbest
// bırakmaya çalışan çağrıların hepsinin başarılı döndüğünü ve stoğun yalnızca
// bir kez iade edildiğini doğrular.
//
// Kritik nokta rezervasyon satırının da kilitlenmesidir. Kilitsiz okunsaydı iki
// çağrı da durumu "active" görür, ikincisi seviye kilidini birinciden sonra
// alır ve rezerve adedi (artık düşülmüş olan) değerden bir kez daha düşmeye
// çalışıp tutarsızlık hatası verirdi — yani telafi eşzamanlılık altında
// idempotent OLMAKTAN ÇIKARDI.
//
// Yarış birden çok TURDA denenir: tek tur, zamanlama nedeniyle pencereyi
// ıskalayabilir. Turların hepsinde her çağrı başarılı dönmelidir.
func TestEszamanliReleaseTekSeferDuser(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	const tur = 5
	const cagiran = 6

	for round := range tur {
		item, loc := stoklu(ctx, t, svc, 5)
		res, err := svc.Reserve(ctx, service.ReserveInput{
			InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 3,
		})
		require.NoError(t, err)

		basla := make(chan struct{})
		hatalar := make([]error, cagiran)

		var wg sync.WaitGroup
		for i := range cagiran {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-basla
				hatalar[i] = svc.ReleaseReservation(ctx, res.ID)
			}(i)
		}
		close(basla)
		wg.Wait()

		for i, err := range hatalar {
			require.NoError(t, err, "tur %d: eşzamanlı telafi %d hata vermemeli", round, i)
		}

		levels, err := svc.ListInventoryLevels(ctx, item.ID)
		require.NoError(t, err)
		require.Len(t, levels, 1)
		assert.Zero(t, levels[0].ReservedQuantity, "tur %d", round)
		assert.Equal(t, int64(5), levels[0].StockedQuantity, "tur %d", round)
	}
}

// TestConfirmReservationStoktanDuser onayın fiziksel stoğu düşürdüğünü,
// satılabilir adedi değiştirmediğini ve serbest bırakmayı kilitlediğini
// doğrular.
func TestConfirmReservationStoktanDuser(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	item, loc := stoklu(ctx, t, svc, 10)

	res, err := svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 4,
	})
	require.NoError(t, err)

	oncekiAvailable, err := svc.AvailableQuantity(ctx, item.ID)
	require.NoError(t, err)

	require.NoError(t, svc.ConfirmReservation(ctx, res.ID))

	levels, err := svc.ListInventoryLevels(ctx, item.ID)
	require.NoError(t, err)
	require.Len(t, levels, 1)
	assert.Equal(t, int64(6), levels[0].StockedQuantity, "fiziksel stok düşmeli")
	assert.Zero(t, levels[0].ReservedQuantity)
	assert.Equal(t, oncekiAvailable, levels[0].Available(),
		"onay satılabilir adedi değiştirmemeli")

	onaylanan, err := svc.GetReservation(ctx, res.ID)
	require.NoError(t, err)
	assert.Equal(t, models.ReservationConfirmed, onaylanan.Status)

	require.NoError(t, svc.ConfirmReservation(ctx, res.ID), "onay idempotent olmalı")
	assert.Equal(t, int64(6), stokAdedi(ctx, t, item.ID, loc.ID),
		"ikinci onay stoğu bir kez daha düşürmemeli")

	err = svc.ReleaseReservation(ctx, res.ID)
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err),
		"onaylanmış rezervasyon serbest bırakılamaz")
}

// TestAktifRezervasyonluKalemSilinemez söz verilmiş stoğu olan kalemin
// silinemediğini, rezervasyon sonlandıktan sonra silinebildiğini doğrular.
func TestAktifRezervasyonluKalemSilinemez(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	item, loc := stoklu(ctx, t, svc, 5)

	res, err := svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 2,
	})
	require.NoError(t, err)

	err = svc.DeleteInventoryItem(ctx, item.ID)
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeItemHasReservations, errors.CodeOf(err))

	require.NoError(t, svc.ReleaseReservation(ctx, res.ID))
	require.NoError(t, svc.DeleteInventoryItem(ctx, item.ID))

	_, err = svc.ListInventoryLevels(ctx, item.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err), "seviyeler de silinmeli")
}

// TestVeritabaniKisitiSonSavunma servis atlanıp doğrudan SQL yazılsa bile
// satılabilir adedin negatife düşürülemediğini doğrular.
//
// Bu kısıt olmasaydı, servis dışından yapılan tek bir müdahale stoğu sessizce
// tutarsız hâle getirebilirdi.
func TestVeritabaniKisitiSonSavunma(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	item, loc := stoklu(ctx, t, svc, 5)

	_, err := svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 3,
	})
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx,
		`UPDATE inventory_levels SET stocked_quantity = 1
         WHERE inventory_item_id = $1 AND location_id = $2`, item.ID, loc.ID)
	require.Error(t, err, "rezerve adedin altına inen doğrudan güncelleme reddedilmeli")

	_, err = testPool.Pool().Exec(ctx,
		`UPDATE inventory_levels SET stocked_quantity = -1
         WHERE inventory_item_id = $1 AND location_id = $2`, item.ID, loc.ID)
	require.Error(t, err, "negatif fiziksel adet reddedilmeli")

	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO inventory_reservations (id, inventory_item_id, location_id, quantity)
         VALUES ($1, $2, $3, 0)`, models.NewReservationID(), item.ID, loc.ID)
	require.Error(t, err, "sıfır adetli rezervasyon reddedilmeli")
}

// TestAralikDisiSayfadaToplamKorunur listenin toplam sayısının, sayfada hiç
// satır olmasa bile doğru kaldığını doğrular.
//
// Toplam sayfa satırlarından türetilirse (örn. satırla birlikte dönen bir
// pencere fonksiyonundan okunursa) aralık dışı bir sayfa için hiç satır
// dönmediğinden toplam 0 görünür; istemci "hiç kayıt yok" sonucuna varır.
// Zarfın count alanı ise sayfanın değil, FİLTREYE UYAN TÜM kayıtların
// sayısıdır. Sahte depo bu ayrımı gösteremez, çünkü orada sayım zaten
// satırlardan bağımsızdır; ayrım yalnızca gerçek SQL'de vardır.
func TestAralikDisiSayfadaToplamKorunur(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	t.Run("kalem", func(t *testing.T) {
		item := yeniKalem(ctx, t, svc)

		items, toplam, err := svc.ListInventoryItems(ctx, service.ListInventoryItemsInput{
			SKU: &item.SKU, Page: service.Page{Limit: 10},
		})
		require.NoError(t, err)
		require.Len(t, items, 1)
		require.Equal(t, int64(1), toplam)

		items, toplam, err = svc.ListInventoryItems(ctx, service.ListInventoryItemsInput{
			SKU: &item.SKU, Page: service.Page{Limit: 10, Offset: 50},
		})
		require.NoError(t, err)
		assert.Empty(t, items, "aralık dışı sayfada satır olmamalı")
		assert.Equal(t, int64(1), toplam,
			"toplam filtreye uyan TÜM kayıtların sayısıdır; sayfadaki satır sayısı değil")
	})

	t.Run("lokasyon", func(t *testing.T) {
		yeniLokasyon(ctx, t, svc)

		locs, toplam, err := svc.ListStockLocations(ctx, service.Page{Limit: 10, Offset: 1_000_000})

		require.NoError(t, err)
		assert.Empty(t, locs, "aralık dışı sayfada satır olmamalı")
		assert.Positive(t, toplam, "en az bir lokasyon var; toplam sayfayla birlikte sıfırlanamaz")
	})
}

// TestQuerySaglayicisiStoklaBirlikteDoner sağlayıcının kalemi TOPLAM
// satılabilir adediyle döndürdüğünü gerçek veritabanı üzerinde doğrular.
// product'ın mağaza listelemesi stoğu bu yoldan okur.
func TestQuerySaglayicisiStoklaBirlikteDoner(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	provider := service.NewQueryProvider(svc)

	item, locA := stoklu(ctx, t, svc, 10)
	locB := yeniLokasyon(ctx, t, svc)
	_, err := svc.SetInventoryLevel(ctx, item.ID, locB.ID, 5)
	require.NoError(t, err)
	_, err = svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: item.ID, LocationID: locA.ID, Quantity: 4,
	})
	require.NoError(t, err)

	stoksuz := yeniKalem(ctx, t, svc)

	records, err := provider.FetchByIDs(ctx, []string{item.ID, stoksuz.ID}, nil)
	require.NoError(t, err)
	require.Len(t, records, 2)

	byID := map[string]query.Record{}
	for _, record := range records {
		id, ok := record[query.IDField].(string)
		require.True(t, ok, "kayıt kimliği metin olmalı")
		byID[id] = record
	}

	assert.Equal(t, int64(11), byID[item.ID][service.FieldAvailableQuantity], "(10-4) + 5 = 11")
	assert.Equal(t, int64(0), byID[stoksuz.ID][service.FieldAvailableQuantity],
		"seviyesi olmayan kalem sıfırla dönmeli")
	assert.Equal(t, item.SKU, byID[item.ID][service.FieldSKU])
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

	mod := inventory.New()
	require.NoError(t, mod.Register(ctx, c))

	svc, err := container.Resolve[*service.Service](c, "inventory.service")
	require.NoError(t, err, "servis, sabit adıyla çözülebilmeli")
	require.NotNil(t, svc)
	assert.Equal(t, "inventory.service", inventory.ServiceName,
		"servis adı değişirse tüketici modüller onu bulamaz")

	// Ad, ADR 0004'ün kuralıyla ELDE hesaplanır: sağlayıcı "<entity>.query"
	// adıyla aranır. Sabiti kullanmak testi totolojiye çevirirdi — sabit
	// yanlışsa test de yanlış adı arardı.
	provider, err := container.Resolve[query.Provider](c, "inventory_item"+query.ProviderSuffix)
	require.NoError(t, err, "Query sağlayıcısı adıyla çözülebilmeli (ADR 0004)")
	assert.Equal(t, "inventory_item", provider.Entity(),
		"kayıt adının öneki Entity() ile aynı olmalı")

	// Asıl kanıt: çekirdeğin Query katmanı, modülü hiç tanımadan yalnızca
	// entity adıyla sağlayıcıyı bulup veriyi çekebilmeli.
	item := yeniKalem(ctx, t, svc)
	_, err = svc.SetInventoryLevel(ctx, item.ID, yeniLokasyon(ctx, t, svc).ID, 4)
	require.NoError(t, err)

	records, err := query.New(nil, c, nil).Graph(ctx, query.GraphSpec{
		Entity:  "inventory_item",
		Filters: map[string]any{"sku": item.SKU},
	})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, item.ID, records[0][query.IDField])
	assert.Equal(t, int64(4), records[0][service.FieldAvailableQuantity])
}

// aktifRezervasyonAdedi kalemin aktif rezervasyon kaydı sayısını döner.
func aktifRezervasyonAdedi(ctx context.Context, t *testing.T, itemID string) int64 {
	t.Helper()

	var count int64
	err := testPool.Pool().QueryRow(ctx,
		`SELECT COUNT(*) FROM inventory_reservations
         WHERE inventory_item_id = $1 AND status = 'active'`,
		itemID).Scan(&count)
	require.NoError(t, err)
	return count
}

// stokAdedi seviyenin fiziksel adedini doğrudan veritabanından okur.
func stokAdedi(ctx context.Context, t *testing.T, itemID, locationID string) int64 {
	t.Helper()

	var stocked int64
	err := testPool.Pool().QueryRow(ctx,
		`SELECT stocked_quantity FROM inventory_levels
         WHERE inventory_item_id = $1 AND location_id = $2 AND deleted_at IS NULL`,
		itemID, locationID).Scan(&stocked)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0
	}
	require.NoError(t, err)
	return stocked
}

// TestKilitliOkumaIslemDisindaReddedilir kilit alan depo metotlarının işlem
// dışında çağrıldığında hata döndüğünü doğrular.
//
// İşlemsiz bir FOR UPDATE kilidi ifade biter bitmez serbest kalır; yani hiçbir
// şeyi korumaz ama koruyormuş gibi görünür. Sessizce çalışmasına izin vermek,
// eşzamanlılık güvencesini fark edilmeden kaybetmenin en kolay yoludur.
func TestKilitliOkumaIslemDisindaReddedilir(t *testing.T) {
	ctx := context.Background()
	repo := repository.New(testPool.Pool())
	svc := yeniServis(t)
	item, loc := stoklu(ctx, t, svc, 3)

	_, err := repo.LockInventoryLevel(ctx, item.ID, loc.ID)
	require.Error(t, err, "seviye kilidi işlem dışında alınamamalı")
	assert.Contains(t, err.Error(), "işlem")

	err = repo.LockInventoryItem(ctx, item.ID)
	require.Error(t, err, "kalem kilidi işlem dışında alınamamalı")

	_, err = repo.LockReservation(ctx, "invres_x")
	require.Error(t, err, "rezervasyon kilidi işlem dışında alınamamalı")

	// Aynı çağrılar işlem içinde başarılı olmalı.
	require.NoError(t, repo.WithTx(ctx, func(ctx context.Context) error {
		if lockErr := repo.LockInventoryItem(ctx, item.ID); lockErr != nil {
			return lockErr
		}
		_, lockErr := repo.LockInventoryLevel(ctx, item.ID, loc.ID)
		return lockErr
	}))
}

// TestIslemHataliBitersegeriAlinir işlemin hata durumunda gerçekten geri
// alındığını veritabanı üzerinde doğrular.
func TestIslemHataliBitersegeriAlinir(t *testing.T) {
	ctx := context.Background()
	repo := repository.New(testPool.Pool())
	sku := "SKU-" + models.NewInventoryItemID()

	bilerek := errors.Internal("test_hata", "işlem geri alınmalı")
	err := repo.WithTx(ctx, func(ctx context.Context) error {
		_, createErr := repo.CreateInventoryItem(ctx, models.InventoryItem{
			ID: models.NewInventoryItemID(), SKU: sku, RequiresShipping: true,
		})
		if createErr != nil {
			return createErr
		}
		return bilerek
	})

	require.ErrorIs(t, err, bilerek)

	var count int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT COUNT(*) FROM inventory_items WHERE sku = $1`, sku).Scan(&count))
	assert.Zero(t, count, "geri alınan işlemin yazdığı satır kalmamalı")
}
