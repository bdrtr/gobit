//go:build integration

// Bu dosyadaki testler gerçek bir PostgreSQL örneği (dolayısıyla Docker)
// gerektirir; `make test` hızlı kalsın diye `integration` etiketiyle
// ayrılmıştır. Çalıştırmak için: make test-integration
//
// Buradaki iddiaların çoğu YALNIZCA gerçek veritabanında kanıtlanabilir:
// kısmi benzersiz indeksin eşzamanlı iki isteği ayırması, soft delete'in
// okuma sorgularından düşmesi, migration'ın geri alınabilmesi ve — Faz 4'ün
// kalbi — vitrin listelemesinin link'ler üzerinden fiyat ve stoğu gerçek
// Query katmanıyla toplaması.
package product_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/internal/core/db"
	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/link"
	"github.com/bdrtr/gobit/internal/modules/product"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

const postgresImage = "postgres:16-alpine"

var (
	// testPool tüm testlerin paylaştığı havuzdur.
	testPool *db.Pool
	// testDSN paylaşılan veritabanının adresidir.
	testDSN string
)

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres tek bir Postgres konteyneri kaldırır, product şemasını
// uygular ve tüm testleri onun üzerinde çalıştırır.
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

	mod := product.New()
	if err = db.Migrate(ctx, testDSN, mod.Migrations(), mod.Name()); err != nil {
		fmt.Fprintf(os.Stderr, "product şeması uygulanamadı: %v\n", err)
		return 1
	}

	testPool, err = db.New(ctx, db.DefaultConfig(testDSN), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bağlantı havuzu açılamadı: %v\n", err)
		return 1
	}
	defer testPool.Close()

	// Link tabloları migration'la DEĞİL, modülün bildirimiyle kurulur (ADR
	// 0005): şemayı core/link, link.Define çağrıldığında yaratır. Üretimde bunu
	// Module.Register açılışta yapar ve hiçbir istek ondan önce gelemez.
	//
	// Burada elle yapılmasının sebebi, bu dosyadaki bazı testlerin modülü
	// Register etmeden doğrudan depo üzerinde servis kurmasıdır. Bildirim
	// olmasaydı ürün listelemesi tümüyle düşerdi: süzgeç link tablosuna karşı
	// bir EXISTS koşulu taşır ve PostgreSQL ilişkiyi, koşul kısa devre etse
	// bile ayrıştırma anında arar. Bu bağımlılık bilinçlidir — eksik link
	// tablosunun sessizce "hiç atama yok" sayılması, her anahtara tüm kataloğu
	// açan arızanın ta kendisini geri getirirdi.
	if err = defineLinks(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "link tanımları bildirilemedi: %v\n", err)
		return 1
	}

	return m.Run()
}

// defineLinks product'ın link tanımlarını paylaşılan veritabanına bildirir.
func defineLinks(ctx context.Context) error {
	links := link.New(testPool, nil)
	for _, def := range service.Definitions() {
		if err := links.Define(ctx, def); err != nil {
			return err
		}
	}
	return nil
}

// --- yardımcılar --------------------------------------------------------

// newService gerçek depo üzerinde çalışan bir servis kurar.
//
// Olay veri yolu VERİLMEZ: buradaki testler deponun ve kuralların davranışını
// sınar, olayları değil — veri yolusuz serviste olaylar sessizce atlanır
// (bkz. service.Service.publishProductEvent). Olayların gerçekten yayımlandığı
// interop_integration_test.go içinde, modül Register edilerek kanıtlanır.
func newService(t *testing.T, links service.Linker, graph service.Grapher) *service.Service {
	t.Helper()

	svc, err := service.New(service.Options{
		Repo:  repository.New(testPool.Pool()),
		Links: links,
		Query: graph,
	})
	require.NoError(t, err)
	return svc
}

// uniqueHandle testler arası çakışmayı önleyen benzersiz bir handle üretir.
//
// Testler tek veritabanını paylaşır; sabit bir handle, ilgisiz bir testin
// bıraktığı kayıt yüzünden çakışma üretirdi.
func uniqueHandle(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

// newDatabase testin kendi veritabanını açar ve sonunda düşürür.
//
// Migration'ı geri alan test paylaşılan şemayı düşürseydi diğer testler
// çalışamazdı; bu yüzden yalnızca o test kendi veritabanında yürür.
func newDatabase(ctx context.Context, t *testing.T) string {
	t.Helper()

	name := fmt.Sprintf("gobit_product_%d", time.Now().UnixNano())

	conn, err := pgx.Connect(ctx, testDSN)
	require.NoError(t, err)
	defer func() { _ = conn.Close(ctx) }()

	// Veritabanı adı SQL'de parametrelenemez; ad testin ürettiği sabit
	// biçimdedir (harf, alt çizgi, rakam) ve dışarıdan veri almaz.
	_, err = conn.Exec(ctx, `CREATE DATABASE `+name)
	require.NoError(t, err)

	t.Cleanup(func() {
		cleanup := context.Background()
		c, cErr := pgx.Connect(cleanup, testDSN)
		if cErr != nil {
			return
		}
		defer func() { _ = c.Close(cleanup) }()
		_, _ = c.Exec(cleanup, `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`)
	})

	u, err := url.Parse(testDSN)
	require.NoError(t, err)
	u.Path = "/" + name
	return u.String()
}

// tableExists tablonun geçerli şemada bulunup bulunmadığını bildirir.
func tableExists(ctx context.Context, t *testing.T, dsn, table string) bool {
	t.Helper()

	conn, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err)
	defer func() { _ = conn.Close(ctx) }()

	var exists bool
	err = conn.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists)
	require.NoError(t, err)
	return exists
}

// --- migration ----------------------------------------------------------

// TestMigrationUpDownIsReversible şemanın uygulanabildiğini ve GERİ
// ALINABİLDİĞİNİ doğrular (plan Bölüm 8).
func TestMigrationUpDownIsReversible(t *testing.T) {
	ctx := context.Background()
	dsn := newDatabase(ctx, t)
	mod := product.New()

	require.NoError(t, db.Migrate(ctx, dsn, mod.Migrations(), mod.Name()))

	tables := []string{
		"product", "product_variant", "product_option", "product_option_value",
		"product_variant_option_value", "product_category", "product_collection",
		"product_tag", "product_image", "product_tag_map", "product_category_map",
	}
	for _, table := range tables {
		assert.True(t, tableExists(ctx, t, dsn, table), "%s tablosu oluşmalı", table)
	}

	version, dirty, err := db.Version(ctx, dsn, mod.Name())
	require.NoError(t, err)
	assert.False(t, dirty, "migration yarıda kalmamalı")
	assert.Equal(t, uint(1), version)

	require.NoError(t, db.MigrateDown(ctx, dsn, mod.Migrations(), mod.Name(), 0),
		"şema geri alınabilmeli")
	for _, table := range tables {
		assert.False(t, tableExists(ctx, t, dsn, table), "%s tablosu düşmeli", table)
	}

	// Geri alınan şema yeniden uygulanabilmeli: geri alma, bir sonraki
	// dağıtımı bloke etmemelidir.
	require.NoError(t, db.Migrate(ctx, dsn, mod.Migrations(), mod.Name()))
	assert.True(t, tableExists(ctx, t, dsn, "product"))
}

// --- CRUD ---------------------------------------------------------------

// TestProductLifecycle ürün oluşturma, okuma, güncelleme ve silmenin gerçek
// veritabanında uçtan uca çalıştığını doğrular.
func TestProductLifecycle(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	handle := uniqueHandle("tisort")

	created, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle:      handle,
		Title:       "Tişört",
		Status:      models.StatusPublished,
		Description: ptrString("Pamuklu"),
		Metadata:    map[string]any{"koleksiyon": "yaz"},
		Options: []service.CreateOptionInput{
			{Title: "Beden", Values: []string{"S", "M", "L"}},
		},
		Variants: []service.CreateVariantInput{
			{Title: "S beden", SKU: ptrString(uniqueHandle("sku-s")), Options: map[string]string{"Beden": "S"}},
			{Title: "M beden", SKU: ptrString(uniqueHandle("sku-m")), Options: map[string]string{"Beden": "M"}},
		},
		Images: []service.CreateImageInput{{URL: "https://cdn.example/1.png"}},
	})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(created.ID, "prod_"))
	assert.Equal(t, handle, created.Handle)
	require.Len(t, created.Variants, 2)
	require.Len(t, created.Options, 1)
	require.Len(t, created.Options[0].Values, 3)
	require.Len(t, created.Images, 1)
	assert.Equal(t, "yaz", created.Metadata["koleksiyon"], "jsonb alanı gidiş dönüş korunmalı")
	assert.False(t, created.CreatedAt.IsZero(), "zaman damgası veritabanından gelmeli")
	assert.Equal(t, time.UTC, created.CreatedAt.Location(), "zaman UTC olmalı")

	// Varyantlar seçenek değerlerine gerçekten bağlanmış olmalı.
	var sVariant models.Variant
	for _, v := range created.Variants {
		if v.Title == "S beden" {
			sVariant = v
		}
	}
	require.NotEmpty(t, sVariant.ID)
	require.Len(t, sVariant.OptionValues, 1)
	assert.Equal(t, "S", sVariant.OptionValues[0].Value)
	assert.Equal(t, "Beden", sVariant.OptionValues[0].OptionTitle)

	fetched, err := svc.GetProduct(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, fetched.ID)
	assert.Len(t, fetched.Variants, 2)

	updated, err := svc.UpdateProduct(ctx, created.ID, service.UpdateProductInput{
		Title:  ptrString("Tişört v2"),
		Status: ptrStatus(models.StatusArchived),
	})
	require.NoError(t, err)
	assert.Equal(t, "Tişört v2", updated.Title)
	assert.Equal(t, models.StatusArchived, updated.Status)
	assert.True(t, updated.UpdatedAt.After(created.UpdatedAt) || updated.UpdatedAt.Equal(created.UpdatedAt),
		"güncelleme damgası geri gitmemeli")

	require.NoError(t, svc.DeleteProduct(ctx, created.ID))
}

// TestSoftDeleteHidesFromReads silinen kaydın okuma sorgularından düştüğünü ve
// handle'ının serbest kaldığını doğrular.
//
// İkincisi kısmi benzersiz indeksin (WHERE deleted_at IS NULL) doğrudan
// sonucudur: silinmiş bir ürün yeni bir ürünün handle'ını tıkamamalıdır.
func TestSoftDeleteHidesFromReads(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	handle := uniqueHandle("silinecek")

	created, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle:   handle,
		Title:    "Silinecek",
		Status:   models.StatusPublished,
		Variants: []service.CreateVariantInput{{Title: "Tek"}},
	})
	require.NoError(t, err)
	variantID := created.Variants[0].ID

	require.NoError(t, svc.DeleteProduct(ctx, created.ID))

	_, err = svc.GetProduct(ctx, created.ID)
	assert.True(t, coreerrors.IsNotFound(err), "silinen ürün okunamamalı: %v", err)

	_, err = svc.GetVariant(ctx, variantID)
	assert.True(t, coreerrors.IsNotFound(err), "silinen ürünün varyantı da düşmeli: %v", err)

	list, err := svc.ListProducts(ctx, service.ListProductsOptions{Handle: &handle})
	require.NoError(t, err)
	assert.Empty(t, list.Items, "silinen ürün listelenmemeli")
	assert.Zero(t, list.Count, "silinen ürün sayıma girmemeli")

	// Handle serbest kalmalı.
	again, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: handle,
		Title:  "Yeniden",
	})
	require.NoError(t, err, "silinen ürünün handle'ı yeniden kullanılabilmeli")
	assert.NotEqual(t, created.ID, again.ID)
}

// TestHandleConflictIsEnforcedByDatabase eşzamanlı iki isteğin ARASINDAN
// geçilemediğini doğrular.
//
// Servisin ön kontrolü iki isteği de "boş" görebilir; benzersizliğin tek gerçek
// garantisi kısmi benzersiz indekstir. Bu iddia yalnızca gerçek veritabanında
// kanıtlanabilir.
func TestHandleConflictIsEnforcedByDatabase(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	handle := uniqueHandle("yaris")

	const attempts = 6
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		ok       int
		conflict int
		other    []error
	)

	wg.Add(attempts)
	for i := range attempts {
		go func(i int) {
			defer wg.Done()
			_, err := svc.CreateProduct(ctx, service.CreateProductInput{
				Handle: handle,
				Title:  fmt.Sprintf("Yarışan %d", i),
			})

			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case coreerrors.IsConflict(err):
				conflict++
			default:
				other = append(other, err)
			}
		}(i)
	}
	wg.Wait()

	assert.Empty(t, other, "beklenmeyen hata: %v", other)
	assert.Equal(t, 1, ok, "aynı handle ile yalnızca BİR ürün oluşabilmeli")
	assert.Equal(t, attempts-1, conflict, "kalan istekler çakışma almalı")
}

// TestDuplicateSKUIsRejected varyant SKU'sunun benzersiz olduğunu doğrular.
func TestDuplicateSKUIsRejected(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	sku := uniqueHandle("sku")

	first, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle:   uniqueHandle("sku-bir"),
		Title:    "Bir",
		Variants: []service.CreateVariantInput{{Title: "Tek", SKU: &sku}},
	})
	require.NoError(t, err)
	require.Len(t, first.Variants, 1)

	_, err = svc.CreateProduct(ctx, service.CreateProductInput{
		Handle:   uniqueHandle("sku-iki"),
		Title:    "İki",
		Variants: []service.CreateVariantInput{{Title: "Tek", SKU: &sku}},
	})
	require.Error(t, err)
	assert.True(t, coreerrors.IsConflict(err), "aynı SKU çakışma vermeli: %v", err)
	assert.Equal(t, "product_sku_taken", coreerrors.CodeOf(err))

	// Çakışan istek yarım kayıt bırakmamalı: ürün de yazılmamış olmalı.
	list, err := svc.ListProducts(ctx, service.ListProductsOptions{Search: ptrString("İki")})
	require.NoError(t, err)
	assert.Empty(t, list.Items, "işlem geri alınmalı; sahipsiz ürün kalmamalı")
}

// TestVariantOptionValueIsUniquePerOption bir varyantın aynı seçenekten tek
// değer taşıdığını doğrular.
//
// Kural şemadadır (birincil anahtar: variant_id, option_id); ikinci yazma yeni
// satır değil güncelleme üretir.
func TestVariantOptionValueIsUniquePerOption(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)

	created, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle:  uniqueHandle("secenek"),
		Title:   "Seçenekli",
		Options: []service.CreateOptionInput{{Title: "Beden", Values: []string{"S", "M"}}},
		Variants: []service.CreateVariantInput{
			{Title: "Değişecek", Options: map[string]string{"Beden": "S"}},
		},
	})
	require.NoError(t, err)
	variantID := created.Variants[0].ID

	var mValueID string
	for _, value := range created.Options[0].Values {
		if value.Value == "M" {
			mValueID = value.ID
		}
	}
	require.NotEmpty(t, mValueID)

	require.NoError(t, svc.SetVariantOptionValues(ctx, variantID, []string{mValueID}))

	variant, err := svc.GetVariant(ctx, variantID)
	require.NoError(t, err)
	require.Len(t, variant.OptionValues, 1, "aynı seçenekten iki değer taşınamaz")
	assert.Equal(t, "M", variant.OptionValues[0].Value)
}

// TestListProductsPagesConsistently sayfalamanın kararlı sıra ürettiğini
// doğrular.
func TestListProductsPagesConsistently(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	collection, err := svc.CreateCollection(ctx, service.CreateCollectionInput{
		Title: "Sayfalama " + uniqueHandle("koleksiyon"),
	})
	require.NoError(t, err)

	const total = 5
	for i := range total {
		_, err := svc.CreateProduct(ctx, service.CreateProductInput{
			Handle:       uniqueHandle(fmt.Sprintf("sayfa-%d", i)),
			Title:        fmt.Sprintf("Sayfa %d", i),
			CollectionID: &collection.ID,
		})
		require.NoError(t, err)
	}

	seen := map[string]struct{}{}
	for offset := 0; offset < total; offset += 2 {
		page, err := svc.ListProducts(ctx, service.ListProductsOptions{
			CollectionID: &collection.ID,
			Limit:        2,
			Offset:       offset,
		})
		require.NoError(t, err)
		assert.Equal(t, total, page.Count, "count sayfadan bağımsız olmalı")
		for _, item := range page.Items {
			_, dup := seen[item.ID]
			assert.False(t, dup, "aynı kayıt iki sayfada görünmemeli: %s", item.ID)
			seen[item.ID] = struct{}{}
		}
	}
	assert.Len(t, seen, total, "sayfalar bütün kümeyi kapsamalı")
}

// TestCreateVariantLosesRaceWithProductDeletion silinmekte olan bir ürüne
// varyant eklenemediğini doğrular.
//
// Yarış GERÇEKTİR ve yalnızca gerçek veritabanında görülür: silme SOFT olduğu
// için product_variant üzerindeki foreign key silinmiş ürünün satırını hâlâ
// görür ve boşluğu kapatmaz. Kontrol işlemin DIŞINDA yapılırsa araya giren bir
// DELETE, deleted_at'i NULL olan ama sahibi silinmiş bir varyant bırakır; bu
// varyant admin uçlarında ve "variant.query" sağlayıcısında görünmeye devam eder.
//
// Sıralama uydurulmaz, KİLİTLE zorlanır: test ürünün satırını kendi işleminde
// (henüz commit etmeden) siler, sonra CreateVariant'ı başlatır. Doğru davranışta
// CreateVariant satır kilidinde bekler ve commit'ten sonra "bulunamadı" alır;
// kontrol işlem dışında yapılırsa hiç beklemez, bekleme penceresi içinde
// varyantı yazar ve yetim kayıt oluşur.
func TestCreateVariantLosesRaceWithProductDeletion(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)

	created, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: uniqueHandle("yaris-silme"),
		Title:  "Silinmekte Olan",
	})
	require.NoError(t, err)

	// Silmeyi başlat ama COMMIT ETME: ürün satırının kilidi bizde kalsın.
	tx, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx,
		`UPDATE product SET deleted_at = now(), updated_at = now() WHERE id = $1`, created.ID)
	require.NoError(t, err)

	type result struct {
		variant models.Variant
		err     error
	}
	done := make(chan result, 1)
	go func() {
		variant, cErr := svc.CreateVariant(ctx, created.ID, service.CreateVariantInput{Title: "Yetim"})
		done <- result{variant: variant, err: cErr}
	}()

	// Pencere, kilitsiz bir uygulamanın kontrolü ve INSERT'i bitirmesine fazlasıyla
	// yeter; kilitli uygulama burada bekler.
	select {
	case got := <-done:
		t.Fatalf("varyant silme commit edilmeden yazıldı (yetim kayıt): %+v, err=%v", got.variant, got.err)
	case <-time.After(time.Second):
	}

	require.NoError(t, tx.Commit(ctx))

	got := <-done
	require.Error(t, got.err, "silinmiş ürüne varyant eklenememeli")
	assert.True(t, coreerrors.IsNotFound(got.err), "bulunamadı bekleniyordu: %v", got.err)

	variants, err := svc.ListVariants(ctx, service.ListVariantsOptions{ProductID: &created.ID})
	require.NoError(t, err)
	assert.Empty(t, variants.Items, "silinmiş ürünün altında canlı varyant kalmamalı")
}

// ptrString dizgenin adresini döner.
func ptrString(v string) *string { return &v }

// ptrInt32 tam sayı işaretçisi üretir.
func ptrInt32(v int32) *int32 { return &v }

// ptrBool mantıksal işaretçi üretir.
func ptrBool(v bool) *bool { return &v }

// ptrStatus durumun adresini döner.
func ptrStatus(v models.Status) *models.Status { return &v }

// TestUrunSutunEslemesiKaymamis elle yazılan sütun listesi ile sqlc'nin
// ürettiği alan sırasının ayrışmadığını doğrular.
//
// # Neden ayrı bir test gerekiyor
//
// Vitrin listesi satırları KONUMA göre çözer (pgx.RowToStructByPos), çünkü
// ada göre çözüm sqlc'nin etiketsiz alanlarıyla çalışmaz. Konum eşlemesi
// sessizce bozulabilir: bu tabloda handle ile title bitişik ve ikisi de text,
// subtitle/description/thumbnail üç text, weight/length/height/width dört
// integer. Aynı tipte iki komşunun yer değiştirmesi HİÇBİR hata üretmez —
// yalnızca her ürünün başlığıyla handle'ını takas eder.
//
// Bu yüzden test her alana AYIRT EDİLEBİLİR bir değer yazar: iki alan yer
// değiştirirse iddia düşer. Yalnızca "alan dolu mu" diye bakan bir test bu
// takası göremezdi.
func TestUrunSutunEslemesiKaymamis(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	handle := uniqueHandle("sutun-eslemesi")

	created, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: handle,
		Title:  "BASLIK-" + handle,
		Status: models.StatusPublished,
		// Aynı tipteki komşu alanların HEPSİ farklı değer taşır.
		Subtitle:      ptrString("ALTBASLIK-ayirt-edici"),
		Description:   ptrString("ACIKLAMA-ayirt-edici"),
		Thumbnail:     ptrString("KAPAK-ayirt-edici"),
		Material:      ptrString("MALZEME-ayirt-edici"),
		OriginCountry: ptrString("TR"),
		Weight:        ptrInt32(1001),
		Length:        ptrInt32(1002),
		Height:        ptrInt32(1003),
		Width:         ptrInt32(1004),
		Discountable:  ptrBool(false),
		Metadata:      map[string]any{"isaret": "ayirt-edici"},
	})
	require.NoError(t, err)

	// Vitrin listesi ELLE YAZILAN sorguyu kullanır; eşlemeyi sınayan yol budur.
	sayfa, err := svc.ListStoreProducts(ctx, service.StoreListOptions{
		Search: ptrString("BASLIK-" + handle),
		Limit:  10,
	})
	require.NoError(t, err)

	var okunan *service.StoreProduct

	for i := range sayfa.Items {
		if sayfa.Items[i].ID == created.ID {
			okunan = &sayfa.Items[i]
		}
	}

	require.NotNil(t, okunan, "ürün vitrin listesinde bulunmalı")

	assert.Equal(t, handle, okunan.Handle, "handle ile title yer değiştirmiş olabilir")
	assert.Equal(t, "BASLIK-"+handle, okunan.Title, "title ile handle yer değiştirmiş olabilir")
	assert.Equal(t, "ALTBASLIK-ayirt-edici", derefString(okunan.Subtitle))
	assert.Equal(t, "ACIKLAMA-ayirt-edici", derefString(okunan.Description))
	assert.Equal(t, "KAPAK-ayirt-edici", derefString(okunan.Thumbnail))
	assert.Equal(t, "MALZEME-ayirt-edici", derefString(okunan.Material))
	assert.Equal(t, "TR", derefString(okunan.OriginCountry))
	assert.Equal(t, int32(1001), derefInt32(okunan.Weight), "weight ile length/height/width karışmış olabilir")
	assert.Equal(t, int32(1002), derefInt32(okunan.Length))
	assert.Equal(t, int32(1003), derefInt32(okunan.Height))
	assert.Equal(t, int32(1004), derefInt32(okunan.Width))
	assert.Equal(t, models.StatusPublished, okunan.Status)
	assert.False(t, okunan.Discountable, "discountable ile is_giftcard yer değiştirmiş olabilir")
	assert.False(t, okunan.IsGiftcard)
	assert.Equal(t, "ayirt-edici", okunan.Metadata["isaret"])
	assert.False(t, okunan.CreatedAt.IsZero())
	assert.False(t, okunan.UpdatedAt.IsZero())
}

// derefString bir işaretçiyi güvenle çözer; nil ise boş dize döner.
func derefString(p *string) string {
	if p == nil {
		return ""
	}

	return *p
}

// derefInt32 bir işaretçiyi güvenle çözer; nil ise sıfır döner.
func derefInt32(p *int32) int32 {
	if p == nil {
		return 0
	}

	return *p
}
