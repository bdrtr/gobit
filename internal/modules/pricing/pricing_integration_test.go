//go:build integration

// Bu dosyadaki testler gerçek bir PostgreSQL örneği (dolayısıyla Docker)
// gerektirir; `make test` hızlı kalsın diye `integration` etiketiyle
// ayrılmıştır. Çalıştırmak için: make test-integration
//
// Burada kanıtlanan iddialar birim testiyle KANITLANAMAZ: migration'ın geri
// alınabilirliği, SetPrices'in gerçekten tek işlemde çalışması, veritabanı
// CHECK kısıtlarının servis doğrulamasını ikinci kez kapatması ve query
// sağlayıcısının gerçek sorgularla toplu davranışı.
package pricing_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/pricing"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
	"github.com/bdrtr/gobit/internal/modules/pricing/repository"
	"github.com/bdrtr/gobit/internal/modules/pricing/service"
)

const postgresImage = "postgres:16-alpine"

var (
	// testPool tüm testlerin paylaştığı havuzdur; şema TestMain'de kurulur.
	testPool *db.Pool
	// testDSN havuzun bağlantı adresidir; migration testleri buna ihtiyaç duyar.
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

	testPool, err = db.New(ctx, db.DefaultConfig(testDSN), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bağlantı havuzu açılamadı: %v\n", err)
		return 1
	}
	defer testPool.Close()

	if err := db.Migrate(ctx, testDSN, pricing.New(nil).Migrations(), pricing.Name); err != nil {
		fmt.Fprintf(os.Stderr, "pricing şeması kurulamadı: %v\n", err)
		return 1
	}

	return m.Run()
}

// newService gerçek depo üzerinde çalışan bir servis üretir.
func newService(t *testing.T) *service.Service {
	t.Helper()
	return service.New(repository.New(testPool.Pool()), service.Options{})
}

// tableExists bir tablonun var olup olmadığını bildirir.
func tableExists(ctx context.Context, t *testing.T, dsn, table string) bool {
	t.Helper()

	pool, err := db.New(ctx, db.DefaultConfig(dsn), nil)
	require.NoError(t, err)
	defer pool.Close()

	var regclass *string
	require.NoError(t, pool.Pool().QueryRow(ctx,
		"SELECT to_regclass($1)::text", "public."+table).Scan(&regclass))
	return regclass != nil
}

// TestMigrationsAreReversible şemanın uygulanıp GERİ ALINABİLDİĞİNİ kanıtlar
// (plan Bölüm 8: up/down çiftleri geri alınabilir olmalıdır).
//
// Test AYRI bir veritabanında çalışır: paylaşılan şemayı düşürmek diğer
// testleri sırasına bağımlı hâle getirirdi.
func TestMigrationsAreReversible(t *testing.T) {
	ctx := context.Background()

	const dbName = "pricing_migration_test"
	_, err := testPool.Pool().Exec(ctx, "CREATE DATABASE "+dbName)
	require.NoError(t, err)

	parsed, err := url.Parse(testDSN)
	require.NoError(t, err)
	parsed.Path = "/" + dbName
	dsn := parsed.String()

	src := pricing.New(nil).Migrations()
	tables := []string{"price_set", "price", "price_list", "price_rule"}

	require.NoError(t, db.Migrate(ctx, dsn, src, pricing.Name))
	for _, table := range tables {
		assert.True(t, tableExists(ctx, t, dsn, table), "%s tablosu oluşmalı", table)
	}

	require.NoError(t, db.MigrateDown(ctx, dsn, src, pricing.Name, 0))
	for _, table := range tables {
		assert.False(t, tableExists(ctx, t, dsn, table), "%s tablosu geri alınmalı", table)
	}

	// Yeniden uygulanabilirlik: down sonrası up temiz çalışmalıdır.
	require.NoError(t, db.Migrate(ctx, dsn, src, pricing.Name))
	assert.True(t, tableExists(ctx, t, dsn, "price"))
}

// TestPriceSetLifecycle uçtan uca CRUD akışını gerçek veritabanında kanıtlar.
func TestPriceSetLifecycle(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{
		{CurrencyCode: "try", Amount: 19900},
		{CurrencyCode: "usd", Amount: 599, MinQuantity: 10, MaxQuantity: ptr(int32(20))},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, set.ID)
	assert.False(t, set.CreatedAt.IsZero())
	assert.Equal(t, time.UTC, set.CreatedAt.Location(), "zamanlar UTC dönmeli")

	fetched, err := svc.GetPriceSet(ctx, set.ID)
	require.NoError(t, err)
	assert.Equal(t, set.ID, fetched.ID)

	prices, err := svc.ListPrices(ctx, set.ID)
	require.NoError(t, err)
	require.Len(t, prices, 2)
	for _, price := range prices {
		assert.Equal(t, price.CurrencyCode, upper(price.CurrencyCode),
			"para birimi BÜYÜK harf saklanmalı")
	}

	require.NoError(t, svc.DeletePriceSet(ctx, set.ID))

	_, err = svc.GetPriceSet(ctx, set.ID)
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))

	// Soft delete: satır durur, yalnızca okumalardan gizlenir.
	var deletedAt *time.Time
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		"SELECT deleted_at FROM price_set WHERE id = $1", set.ID).Scan(&deletedAt))
	assert.NotNil(t, deletedAt, "silme SOFT olmalı; satır durmalı")

	var livePrices int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		"SELECT count(*) FROM price WHERE price_set_id = $1 AND deleted_at IS NULL",
		set.ID).Scan(&livePrices))
	assert.Zero(t, livePrices, "kap silinince fiyatları da gizlenmeli")
}

// TestSetPricesIsAtomic toplu yazmanın gerçekten TEK İŞLEMDE çalıştığını
// kanıtlar.
//
// Senaryo: ikinci fiyat var olmayan bir fiyat listesine bağlıdır ve veritabanı
// onu foreign key ile reddeder. İşlem olmasaydı, eski fiyatların soft delete'i
// ÇOKTAN yazılmış olurdu ve kap fiyatsız kalırdı. Test eski fiyatların yerinde
// durduğunu doğrular.
func TestSetPricesIsAtomic(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{
		{CurrencyCode: "TRY", Amount: 100},
		{CurrencyCode: "USD", Amount: 200},
	})
	require.NoError(t, err)

	before, err := svc.ListPrices(ctx, set.ID)
	require.NoError(t, err)
	require.Len(t, before, 2)

	_, err = svc.SetPrices(ctx, set.ID, []service.PriceInput{
		{CurrencyCode: "EUR", Amount: 300},
		{CurrencyCode: "GBP", Amount: 400, PriceListID: ptr("plist_OLMAYAN")},
	})
	require.Error(t, err, "olmayan fiyat listesine bağlı fiyat reddedilmeli")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	after, err := svc.ListPrices(ctx, set.ID)
	require.NoError(t, err)
	require.Len(t, after, 2, "başarısız toplu yazma eski fiyatları KORUMALI")

	currencies := map[string]bool{}
	for _, price := range after {
		currencies[price.CurrencyCode] = true
	}
	assert.True(t, currencies["TRY"] && currencies["USD"])
	assert.False(t, currencies["EUR"], "işlem geri alındığı için yeni fiyat yazılmamalı")
}

// TestSetPricesReplacesAndKeepsRules başarılı toplu yazmanın eski fiyatları
// değiştirdiğini ve kuralları birlikte yazdığını kanıtlar.
func TestSetPricesReplacesAndKeepsRules(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{{CurrencyCode: "TRY", Amount: 100}})
	require.NoError(t, err)

	written, err := svc.SetPrices(ctx, set.ID, []service.PriceInput{{
		CurrencyCode: "TRY",
		Amount:       9000,
		Rules: []service.RuleInput{
			{Attribute: "region_id", Operator: models.OpEq, Values: []string{"reg_1"}},
			{Attribute: "customer_group_id", Operator: models.OpIn, Values: []string{"vip", "b2b"}},
		},
	}})
	require.NoError(t, err)
	require.Len(t, written, 1)
	require.Len(t, written[0].Rules, 2)

	reread, err := svc.ListPrices(ctx, set.ID)
	require.NoError(t, err)
	require.Len(t, reread, 1, "yerine koyma eski fiyatı silmeli")
	assert.Equal(t, int64(9000), reread[0].Amount)
	require.Len(t, reread[0].Rules, 2, "kurallar fiyatla birlikte okunmalı")

	values := map[string][]string{}
	for _, rule := range reread[0].Rules {
		values[rule.Attribute] = rule.Values
	}
	assert.Equal(t, []string{"reg_1"}, values["region_id"])
	assert.Equal(t, []string{"vip", "b2b"}, values["customer_group_id"])
}

// TestDatabaseRejectsInvalidMoney veritabanı CHECK kısıtlarının servis
// doğrulamasından BAĞIMSIZ ikinci bir kapı olduğunu kanıtlar.
//
// Doğrulama yalnızca uygulamada olsaydı, doğrudan SQL çalıştıran bir bakım
// betiği negatif fiyat yazabilirdi.
func TestDatabaseRejectsInvalidMoney(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	set, err := svc.CreatePriceSet(ctx, nil)
	require.NoError(t, err)

	for _, tc := range []struct {
		name     string
		currency string
		amount   int64
		minQty   int32
		maxQty   any
	}{
		{"negatif tutar", "TRY", -1, 1, nil},
		{"aşırı büyük tutar", "TRY", models.MaxAmount + 1, 1, nil},
		{"küçük harf para birimi", "try", 100, 1, nil},
		{"iki harfli para birimi", "TR", 100, 1, nil},
		{"sıfır asgari adet", "TRY", 100, 0, nil},
		{"azami adet asgariden küçük", "TRY", 100, 10, int32(5)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := testPool.Pool().Exec(ctx, `
				INSERT INTO price (id, price_set_id, currency_code, amount, min_quantity, max_quantity, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, now(), now())`,
				models.NewPriceID(time.Now()), set.ID, tc.currency, tc.amount, tc.minQty, tc.maxQty)
			require.Error(t, err, "veritabanı bu satırı reddetmeli")
		})
	}
}

// TestCalculatePriceWithPriceList gerçek veritabanı üzerinden liste
// önceliğini kanıtlar: yayındaki kampanya taban fiyatı ezer, taslak ezmez.
func TestCalculatePriceWithPriceList(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	list, err := svc.CreatePriceList(ctx, service.PriceListInput{
		Title:  "Yaz kampanyası",
		Type:   models.PriceListSale,
		Status: models.PriceListDraft,
	})
	require.NoError(t, err)

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{
		{CurrencyCode: "TRY", Amount: 10000},
		{CurrencyCode: "TRY", Amount: 7500, PriceListID: &list.ID},
	})
	require.NoError(t, err)

	calculated, err := svc.CalculatePrice(ctx, set.ID, service.CalculateParams{CurrencyCode: "TRY"})
	require.NoError(t, err)
	assert.Equal(t, int64(10000), calculated.Amount, "taslak kampanya fiyat sunmamalı")

	_, err = svc.UpdatePriceList(ctx, list.ID, service.PriceListInput{
		Title:  list.Title,
		Type:   models.PriceListSale,
		Status: models.PriceListActive,
	})
	require.NoError(t, err)

	calculated, err = svc.CalculatePrice(ctx, set.ID, service.CalculateParams{CurrencyCode: "TRY"})
	require.NoError(t, err)
	assert.Equal(t, int64(7500), calculated.Amount, "yayındaki kampanya taban fiyatı ezmeli")
	require.NotNil(t, calculated.PriceListID)
	assert.Equal(t, list.ID, *calculated.PriceListID)
	assert.Equal(t, models.PriceListSale, calculated.PriceListType)
}

// TestCalculatePriceSkipsDeletedPriceList listesi silinmiş bir fiyatın gerçek
// sorguda da elendiğini kanıtlar; LEFT JOIN'in deleted_at koşulu buna dayanır.
func TestCalculatePriceSkipsDeletedPriceList(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	list, err := svc.CreatePriceList(ctx, service.PriceListInput{
		Title:  "Silinecek kampanya",
		Type:   models.PriceListOverride,
		Status: models.PriceListActive,
	})
	require.NoError(t, err)

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{
		{CurrencyCode: "TRY", Amount: 10000},
		{CurrencyCode: "TRY", Amount: 1, PriceListID: &list.ID},
	})
	require.NoError(t, err)

	calculated, err := svc.CalculatePrice(ctx, set.ID, service.CalculateParams{CurrencyCode: "TRY"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), calculated.Amount)

	require.NoError(t, svc.DeletePriceList(ctx, list.ID))

	calculated, err = svc.CalculatePrice(ctx, set.ID, service.CalculateParams{CurrencyCode: "TRY"})
	require.NoError(t, err)
	assert.Equal(t, int64(10000), calculated.Amount, "listesi silinmiş fiyat hesaba katılmamalı")
}

// TestCalculatePriceUsesRules kuralların gerçek veritabanı turunda da
// uygulandığını kanıtlar.
func TestCalculatePriceUsesRules(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{
		{CurrencyCode: "TRY", Amount: 10000},
		{
			CurrencyCode: "TRY",
			Amount:       6000,
			Rules: []service.RuleInput{
				{Attribute: "region_id", Operator: models.OpEq, Values: []string{"reg_tr"}},
			},
		},
	})
	require.NoError(t, err)

	withoutContext, err := svc.CalculatePrice(ctx, set.ID, service.CalculateParams{CurrencyCode: "TRY"})
	require.NoError(t, err)
	assert.Equal(t, int64(10000), withoutContext.Amount, "bağlam yoksa kurallı fiyat elenmeli")
	assert.Zero(t, withoutContext.MatchedRules)

	withContext, err := svc.CalculatePrice(ctx, set.ID, service.CalculateParams{
		CurrencyCode: "TRY",
		Attributes:   map[string]string{"region_id": "reg_tr"},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(6000), withContext.Amount, "kural eşleşince belirgin fiyat kazanmalı")
	assert.Equal(t, 1, withContext.MatchedRules)
}

// TestQueryProviderBatchesRealQueries sağlayıcının gerçek sorgularla toplu
// çalıştığını ve fiyatları taşıdığını kanıtlar (ADR 0004).
func TestQueryProviderBatchesRealQueries(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	provider := service.NewQueryProvider(svc)

	ids := make([]string, 0, 3)
	for i := range 3 {
		set, err := svc.CreatePriceSet(ctx, []service.PriceInput{
			{CurrencyCode: "TRY", Amount: int64(1000 * (i + 1))},
		})
		require.NoError(t, err)
		ids = append(ids, set.ID)
	}
	// Bulunamayan kimlik hata değildir; kayıt dönmez.
	ids = append(ids, "pset_OLMAYAN")

	records, err := provider.FetchByIDs(ctx, ids, nil)
	require.NoError(t, err)
	require.Len(t, records, 3, "olmayan kimlik sessizce atlanmalı")

	byID := map[string]query.Record{}
	for _, record := range records {
		id, ok := record[query.IDField].(string)
		require.True(t, ok, "kayıt kimliği dize olmalı")
		byID[id] = record
	}

	for i, id := range ids[:3] {
		record, ok := byID[id]
		require.True(t, ok, "%s kaydı dönmeli", id)

		prices, ok := record["prices"].([]map[string]any)
		require.True(t, ok, "fiyatlar kayıtla birlikte gelmeli")
		require.Len(t, prices, 1)
		assert.Equal(t, int64(1000*(i+1)), prices[0]["amount"])
		assert.Equal(t, "TRY", prices[0]["currency_code"])
	}
}

// TestQueryProviderListFiltersByID sağlayıcının kimlik filtresini gerçek
// sorguyla uyguladığını kanıtlar.
func TestQueryProviderListFiltersByID(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	provider := service.NewQueryProvider(svc)

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{{CurrencyCode: "TRY", Amount: 4242}})
	require.NoError(t, err)

	records, err := provider.List(ctx, query.ListOptions{
		Filters: map[string]any{"id": set.ID},
		Fields:  []string{"id", "prices"},
	})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, set.ID, records[0][query.IDField])

	prices, ok := records[0]["prices"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, prices, 1)
	assert.Equal(t, int64(4242), prices[0]["amount"])
}

// TestPriceRuleCascadesWithPrice kuralların fiyatla aynı modül içinde foreign
// key ile bağlı olduğunu kanıtlar: fiyat satırı kalkarsa kural da kalkar.
//
// Modül İÇİ FK serbesttir ve kullanılmalıdır; yasak olan cross-module FK'dır
// (Prensip 2.2).
func TestPriceRuleCascadesWithPrice(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{{
		CurrencyCode: "TRY",
		Amount:       100,
		Rules: []service.RuleInput{
			{Attribute: "region_id", Operator: models.OpEq, Values: []string{"reg_1"}},
		},
	}})
	require.NoError(t, err)

	prices, err := svc.ListPrices(ctx, set.ID)
	require.NoError(t, err)
	require.Len(t, prices, 1)
	priceID := prices[0].ID

	var ruleCount int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		"SELECT count(*) FROM price_rule WHERE price_id = $1", priceID).Scan(&ruleCount))
	require.Equal(t, 1, ruleCount)

	_, err = testPool.Pool().Exec(ctx, "DELETE FROM price WHERE id = $1", priceID)
	require.NoError(t, err)

	require.NoError(t, testPool.Pool().QueryRow(ctx,
		"SELECT count(*) FROM price_rule WHERE price_id = $1", priceID).Scan(&ruleCount))
	assert.Zero(t, ruleCount, "fiyat düşünce kuralları da düşmeli")
}

// TestNoCrossModuleForeignKeys pricing tablolarının YALNIZCA kendi tablolarına
// referans verdiğini kanıtlar (Prensip 2.2).
func TestNoCrossModuleForeignKeys(t *testing.T) {
	ctx := context.Background()

	rows, err := testPool.Pool().Query(ctx, `
		SELECT tc.table_name, ccu.table_name AS referenced
		FROM information_schema.table_constraints tc
		JOIN information_schema.constraint_column_usage ccu
		  ON ccu.constraint_name = tc.constraint_name
		WHERE tc.constraint_type = 'FOREIGN KEY'
		  AND tc.table_schema = 'public'
		  AND tc.table_name IN ('price_set', 'price', 'price_list', 'price_rule')`)
	require.NoError(t, err)
	defer rows.Close()

	owned := map[string]bool{"price_set": true, "price": true, "price_list": true, "price_rule": true}
	found := 0
	for rows.Next() {
		var table, referenced string
		require.NoError(t, rows.Scan(&table, &referenced))
		assert.True(t, owned[referenced],
			"%s tablosu %s'e referans veriyor; cross-module FK yasaktır", table, referenced)
		found++
	}
	require.NoError(t, rows.Err())
	assert.Positive(t, found, "modül içi FK'ler kurulmuş olmalı")
}

// ptr bir değerin adresini döner.
func ptr[T any](v T) *T { return &v }

// upper bir dizeyi ASCII büyük harfe çevirir; testte para birimi denetimi için.
func upper(s string) string {
	out := []rune(s)
	for i, r := range out {
		if r >= 'a' && r <= 'z' {
			out[i] = r - ('a' - 'A')
		}
	}
	return string(out)
}
