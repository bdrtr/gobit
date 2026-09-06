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
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
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

// TestDatabaseRejectsValuelessRule kural değerleri kısıtının GERÇEKTEN
// kapandığını kanıtlar (migration 000002).
//
// 000001'deki CHECK (array_length(rule_values, 1) >= 1) boş diziyi geçiriyordu:
// array_length('{}', 1) NULL döner ve sonucu NULL olan bir CHECK sağlanmış
// sayılır. Değersiz bir kural, koşulu okunamaz bir fiyat demektir; kapının veri
// düzeyinde de durması bu yüzden gerekir.
func TestDatabaseRejectsValuelessRule(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{{CurrencyCode: "TRY", Amount: 100}})
	require.NoError(t, err)

	prices, err := svc.ListPrices(ctx, set.ID)
	require.NoError(t, err)
	require.Len(t, prices, 1)

	_, err = testPool.Pool().Exec(ctx, `
		INSERT INTO price_rule (id, price_id, attribute, operator, rule_values, created_at, updated_at)
		VALUES ($1, $2, 'region_id', 'eq', '{}', now(), now())`,
		models.NewPriceRuleID(time.Now()), prices[0].ID)
	require.Error(t, err, "veritabanı değersiz kuralı reddetmeli")

	// Aynı satır tek değerle KABUL edilmelidir; kısıt her şeyi reddetmiyor.
	_, err = testPool.Pool().Exec(ctx, `
		INSERT INTO price_rule (id, price_id, attribute, operator, rule_values, created_at, updated_at)
		VALUES ($1, $2, 'region_id', 'eq', '{reg_1}', now(), now())`,
		models.NewPriceRuleID(time.Now()), prices[0].ID)
	require.NoError(t, err)
}

// TestCreatePriceSetIsAtomic kap ile fiyatlarının TEK işlemde yazıldığını
// kanıtlar.
//
// Senaryo: fiyatlardan biri var olmayan bir fiyat listesine bağlıdır ve
// veritabanı onu foreign key ile reddeder. İki ayrı işlemde kap ÇOKTAN commit
// edilmiş olurdu ve çağıran hata alsa bile geride fiyatsız, kimseye bağlanmamış
// bir kap kalırdı. Servis doğrulaması bu dalı yakalayamaz: kimlik biçimi
// geçerlidir, yalnızca kayıt yoktur.
func TestCreatePriceSetIsAtomic(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	countSets := func() int64 {
		var count int64
		require.NoError(t, testPool.Pool().QueryRow(ctx, "SELECT count(*) FROM price_set").Scan(&count))
		return count
	}

	before := countSets()

	_, err := svc.CreatePriceSet(ctx, []service.PriceInput{
		{CurrencyCode: "TRY", Amount: 100},
		{CurrencyCode: "USD", Amount: 200, PriceListID: ptr("plist_OLMAYAN")},
	})
	require.Error(t, err, "olmayan fiyat listesine bağlı fiyat reddedilmeli")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	assert.Equal(t, before, countSets(), "reddedilen yazma geride yetim bir kap BIRAKMAMALI")
}

// TestConcurrentSetPricesDoesNotMerge eşzamanlı iki yazımın "yerine koyma"
// semantiğini bozmadığını kanıtlar.
//
// Senaryo, ReplacePrices'in SQL dizisinin birebir aynısını iki işlemde
// çalıştırır. Kabın varlık denetimi KİLİTSİZ olsaydı ikinci işlemin "eski
// fiyatları sil" adımı, READ COMMITTED altında kendi statement snapshot'ında
// birincinin YENİ satırlarını göremez ve onları silmezdi; kapta iki yazımın
// fiyatları birlikte canlı kalır ve iki çağıran da hatasız dönerdi. Yanlış
// fiyat tam olarak böyle doğar.
func TestConcurrentSetPricesDoesNotMerge(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{{CurrencyCode: "TRY", Amount: 100}})
	require.NoError(t, err)

	// Birinci yazan: ReplacePrices'in adımlarını elle yürütür ve AÇIK kalır.
	first, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = first.Rollback(ctx) }()

	var lockedID string
	require.NoError(t, first.QueryRow(ctx,
		"SELECT id FROM price_set WHERE id = $1 AND deleted_at IS NULL FOR UPDATE",
		set.ID).Scan(&lockedID))

	_, err = first.Exec(ctx,
		"UPDATE price SET deleted_at = now(), updated_at = now() WHERE price_set_id = $1 AND deleted_at IS NULL",
		set.ID)
	require.NoError(t, err)
	_, err = first.Exec(ctx, `
		INSERT INTO price (id, price_set_id, currency_code, amount, min_quantity, created_at, updated_at)
		VALUES ($1, $2, 'USD', 500, 1, now(), now())`,
		models.NewPriceID(time.Now()), set.ID)
	require.NoError(t, err)

	// İkinci yazan: gerçek yazma yolu. Birinci açıkken beklemeye girmelidir.
	done := make(chan error, 1)
	go func() {
		_, setErr := svc.SetPrices(ctx, set.ID, []service.PriceInput{{CurrencyCode: "EUR", Amount: 700}})
		done <- setErr
	}()
	requireLockWait(ctx, t, done)

	require.NoError(t, first.Commit(ctx))
	require.NoError(t, <-done, "birinci yazan bitince ikincisi tamamlanmalı")

	prices, err := svc.ListPrices(ctx, set.ID)
	require.NoError(t, err)

	currencies := make([]string, 0, len(prices))
	for _, price := range prices {
		currencies = append(currencies, price.CurrencyCode)
	}
	assert.Equal(t, []string{"EUR"}, currencies,
		"ikinci yazma birincinin fiyatını da silmeli; yerine koyma BİRLEŞMEYE dönüşmemeli")
}

// requireLockWait ikinci yazanın gerçekten kilit beklediğini doğrular.
//
// Sabit bir bekleme yerine veritabanına sorulur: bekleyen bir backend görünene
// kadar yoklanır, bu arada işlem tamamlanırsa test hemen düşer. Böylece
// zamanlamaya değil, gözlemlenen duruma dayanılır.
func requireLockWait(ctx context.Context, t *testing.T, done <-chan error) {
	t.Helper()

	deadline := time.Now().Add(15 * time.Second)
	for {
		select {
		case err := <-done:
			t.Fatalf("ikinci yazma kilit beklemeden tamamlandı: %v", err)
		default:
		}

		var waiting int
		require.NoError(t, testPool.Pool().QueryRow(ctx, `
			SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting))
		if waiting > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("ikinci yazma kilit beklemeye hiç girmedi")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestQueryProviderHidesUnpublishedListPrices okuma yüzeyinin, hesaplamanın
// GEÇERSİZ saydığı fiyatları sızdırmadığını kanıtlar.
//
// Sağlayıcı hesaplama bağlamı taşımaz; taşımadığı bir bağlama koşullu fiyatı
// dönerse tüketici (product'ın store listelemesi) onu eleyemez ve vitrin
// yayınlanmamış bir kampanyayı gösterir. Testin ikinci yarısı süzgecin AŞIRI
// olmadığını da kanıtlar: liste yayına alınınca fiyat görünür.
func TestQueryProviderHidesUnpublishedListPrices(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	provider := service.NewQueryProvider(svc)

	list, err := svc.CreatePriceList(ctx, service.PriceListInput{
		Title:  "Yayınlanmamış kampanya",
		Type:   models.PriceListSale,
		Status: models.PriceListDraft,
	})
	require.NoError(t, err)

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{
		{CurrencyCode: "TRY", Amount: 10000},
		{CurrencyCode: "TRY", Amount: 1, PriceListID: &list.ID},
	})
	require.NoError(t, err)

	// Hesaplama taslak kampanyayı zaten elemektedir; okuma yüzeyi de elemelidir.
	calculated, err := svc.CalculatePrice(ctx, set.ID, service.CalculateParams{CurrencyCode: "TRY"})
	require.NoError(t, err)
	require.Equal(t, int64(10000), calculated.Amount)

	amounts := providerAmounts(ctx, t, provider, set.ID)
	assert.Equal(t, []int64{10000}, amounts,
		"yayınlanmamış kampanyanın fiyatı okuma yüzeyine SIZMAMALI")

	_, err = svc.UpdatePriceList(ctx, list.ID, service.PriceListInput{
		Title:  list.Title,
		Type:   models.PriceListSale,
		Status: models.PriceListActive,
	})
	require.NoError(t, err)

	amounts = providerAmounts(ctx, t, provider, set.ID)
	assert.ElementsMatch(t, []int64{10000, 1}, amounts,
		"yayına alınan kampanyanın fiyatı görünmeli")
}

// providerAmounts bir kabın sağlayıcı üzerinden görünen fiyat tutarlarını döner.
func providerAmounts(
	ctx context.Context,
	t *testing.T,
	provider *service.QueryProvider,
	setID string,
) []int64 {
	t.Helper()

	records, err := provider.FetchByIDs(ctx, []string{setID}, nil)
	require.NoError(t, err)
	require.Len(t, records, 1)

	prices, ok := records[0]["prices"].([]map[string]any)
	require.True(t, ok, "fiyatlar kayıtla birlikte gelmeli")

	amounts := make([]int64, 0, len(prices))
	for _, price := range prices {
		amount, isInt := price["amount"].(int64)
		require.True(t, isInt, "tutar tam sayı minor unit olmalı")
		amounts = append(amounts, amount)
	}
	return amounts
}

// TestStorePricesHideDraftListsAndRules müşteri yüzeyinin yayınlanmamış kampanya
// fiyatlarını ve kural koşullarını SIZDIRMADIĞINI kanıtlar.
//
// Regresyon: Query sağlayıcısı süzgeci uygularken GET /store/v1/price-sets/{id}
// süzgeçsiz ListPrices yolunu kullanıyordu. Sonuç: taslak bir kampanyanın
// fiyatı ve bir müşteri segmentine bağlı kuralın koşulu (ör. customer_group_id)
// müşteri gövdesine çıkıyordu. İki müşteri yüzeyi artık AYNI süzgeci kullanır.
func TestStorePricesHideDraftListsAndRules(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	taslak, err := svc.CreatePriceList(ctx, service.PriceListInput{
		Title:  "Yayınlanmamış kampanya",
		Type:   models.PriceListSale,
		Status: models.PriceListDraft,
	})
	require.NoError(t, err)

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{
		{CurrencyCode: "TRY", Amount: 10000},                      // taban: görünmeli
		{CurrencyCode: "TRY", Amount: 1, PriceListID: &taslak.ID}, // taslak: GÖRÜNMEMELİ
		{CurrencyCode: "TRY", Amount: 2, Rules: []service.RuleInput{ // kurala bağlı: GÖRÜNMEMELİ
			{Attribute: "customer_group_id", Operator: models.OpEq, Values: []string{"vip"}},
		}},
	})
	require.NoError(t, err)

	// Yönetim yüzeyi HER ŞEYİ görür: operatör taslak kampanyayı ve kuralı
	// görebilmelidir.
	adminPrices, err := svc.ListPrices(ctx, set.ID)
	require.NoError(t, err)
	assert.Len(t, adminPrices, 3, "yönetim yüzeyi tüm fiyatları görmeli")

	// Müşteri yüzeyi YALNIZCA taban fiyatı görür.
	storePrices, err := svc.ListStorePrices(ctx, set.ID)
	require.NoError(t, err)
	require.Len(t, storePrices, 1, "müşteriye yalnızca gösterilebilir fiyat çıkmalı: %+v", storePrices)
	assert.Equal(t, int64(10000), storePrices[0].Amount)
	assert.Nil(t, storePrices[0].PriceListID, "taslak kampanya fiyatı sızdı")
	assert.Empty(t, storePrices[0].Rules, "kural koşulları müşteriye çıkmamalı")

	// Kampanya yayına alınınca müşteri yüzeyinde GÖRÜNMELİ — süzgeç kalıcı
	// olarak gizlemiyor, yalnızca yayında olmayanı eliyor.
	_, err = svc.UpdatePriceList(ctx, taslak.ID, service.PriceListInput{
		Title:  taslak.Title,
		Type:   models.PriceListSale,
		Status: models.PriceListActive,
	})
	require.NoError(t, err)

	storePrices, err = svc.ListStorePrices(ctx, set.ID)
	require.NoError(t, err)
	assert.Len(t, storePrices, 2, "yayına alınan kampanya müşteriye görünmeli")
}

// countingTracer havuzun açtığı sorguları sayar.
//
// Sayaç, "toplu okuma kalem başına sorgu açmaz" iddiasının tek doğrudan
// kanıtıdır: süre ölçmek testi makineye bağlar, sorgu sayısı bağlamaz.
type countingTracer struct {
	count atomic.Int64
}

// TraceQueryStart her sorgu başlangıcında sayacı artırır.
func (c *countingTracer) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	_ pgx.TraceQueryStartData,
) context.Context {
	c.count.Add(1)
	return ctx
}

// TraceQueryEnd sözleşme gereği vardır; sayım başlangıçta yapılır.
func (c *countingTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// newCountingService sorgularını sayan KENDİ havuzu üzerinde bir servis üretir.
//
// Havuz tek bağlantılıdır: çok bağlantılı bir havuzda ısınma sorguları sayıma
// karışır ve sayı makineye göre değişirdi.
func newCountingService(t *testing.T) (*service.Service, *countingTracer) {
	t.Helper()

	cfg, err := pgxpool.ParseConfig(testDSN)
	require.NoError(t, err)
	tracer := &countingTracer{}
	cfg.ConnConfig.Tracer = tracer
	cfg.MaxConns = 1

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return service.New(repository.New(pool), service.Options{}), tracer
}

// TestCalculateAmountsJSONMatchesPerSetOnRealData toplu fiyat yolunun GERÇEK
// sorgularla kap başına yolla AYNI tutarı seçtiğini ve kalem sayısından
// bağımsız olarak SABİT sayıda sorgu açtığını kanıtlar.
//
// İki iddia da birim testiyle kanıtlanamaz: eşitlik, iki ayrı SQL'in aynı aday
// satırlarını döndürmesine dayanır (biri "= $1", diğeri "= ANY($1)") ve sorgu
// sayısı ancak gerçek bir havuzda sayılabilir.
//
// Sepet hesabının tamamı bu eşitliğe dayanır: farklı bir fiyat seçen bir toplu
// okuma müşteriden başka bir tutar tahsil eder ve sonraki hiçbir denetim bunu
// görmez — toplamlar iki durumda da kendi içinde tutarlıdır.
func TestCalculateAmountsJSONMatchesPerSetOnRealData(t *testing.T) {
	ctx := context.Background()
	svc, tracer := newCountingService(t)

	list, err := svc.CreatePriceList(ctx, service.PriceListInput{
		Title:  "Toplu okuma kampanyası",
		Type:   models.PriceListSale,
		Status: models.PriceListActive,
	})
	require.NoError(t, err)

	// Kaplar seçim kuralının her dalını taşır: taban fiyat, adet kademesi,
	// yayındaki kampanya, bölge kuralı ve başka para birimi.
	setIDs := make([]string, 0, 8)
	for i := range 8 {
		inputs := []service.PriceInput{{CurrencyCode: "TRY", Amount: int64(1000 + i)}}
		switch i % 4 {
		case 1:
			inputs = append(inputs, service.PriceInput{
				CurrencyCode: "TRY", Amount: int64(800 + i), MinQuantity: 10, MaxQuantity: ptr(int32(20)),
			})
		case 2:
			inputs = append(inputs, service.PriceInput{
				CurrencyCode: "TRY", Amount: int64(9000 + i), PriceListID: &list.ID,
			})
		case 3:
			inputs = append(inputs, service.PriceInput{
				CurrencyCode: "TRY", Amount: int64(600 + i),
				Rules: []service.RuleInput{
					{Attribute: "region_id", Operator: models.OpEq, Values: []string{"reg_1"}},
				},
			})
			inputs = append(inputs, service.PriceInput{CurrencyCode: "USD", Amount: int64(50 + i)})
		}

		set, err := svc.CreatePriceSet(ctx, inputs)
		require.NoError(t, err)
		setIDs = append(setIDs, set.ID)
	}

	attrs := map[string]string{"region_id": "reg_1"}
	type kalem struct {
		setID    string
		quantity int32
	}
	items := make([]kalem, 0, len(setIDs)+2)
	for i, id := range setIDs {
		items = append(items, kalem{id, int32(1 + i%15)})
	}
	// Aynı kap iki farklı adette ve fiyatı olmayan bir kap da isteğe girer.
	items = append(items, kalem{setIDs[1], 12}, kalem{"pset_OLMAYAN", 1})

	request := map[string]any{
		"currency_code": "TRY",
		"attributes":    attrs,
		"items": func() []map[string]any {
			out := make([]map[string]any, 0, len(items))
			for _, item := range items {
				out = append(out, map[string]any{"price_set_id": item.setID, "quantity": item.quantity})
			}
			return out
		}(),
	}
	payload, err := json.Marshal(request)
	require.NoError(t, err)

	// Isınma: ilk çalıştırma bağlantıyı açar ve deyimleri hazırlar; sayım
	// bundan sonra başlar.
	_, err = svc.CalculateAmountsJSON(ctx, payload)
	require.NoError(t, err)
	_, err = svc.CalculateAmount(ctx, setIDs[0], "TRY", 1, attrs)
	require.NoError(t, err)

	before := tracer.count.Load()
	raw, err := svc.CalculateAmountsJSON(ctx, payload)
	require.NoError(t, err)
	batchQueries := tracer.count.Load() - before

	var resp struct {
		Items []struct {
			Amount int64 `json:"amount"`
			Priced bool  `json:"priced"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(raw, &resp))
	require.Len(t, resp.Items, len(items))

	before = tracer.count.Load()
	for i, item := range items {
		amount, err := svc.CalculateAmount(ctx, item.setID, "TRY", item.quantity, attrs)
		if err != nil {
			require.True(t, errors.IsNotFound(err), "%s: %v", item.setID, err)
			assert.False(t, resp.Items[i].Priced, "%s toplu yolda fiyatlı görünüyor", item.setID)
			continue
		}
		require.True(t, resp.Items[i].Priced, "%s toplu yolda fiyatsız görünüyor", item.setID)
		assert.Equal(t, amount, resp.Items[i].Amount,
			"%s (adet %d) iki yolda farklı fiyatlandı", item.setID, item.quantity)
	}
	perSetQueries := tracer.count.Load() - before

	assert.Equal(t, int64(2), batchQueries,
		"toplu yol kalem sayısından bağımsız olarak iki sorgu açmalı (adaylar + kurallar)")
	assert.Greater(t, perSetQueries, int64(2*len(items)-2),
		"kap başına yol kalem başına en az iki sorgu açar; ölçülen: %d", perSetQueries)
}

// TestSilinmisFiyataKuralYazilabilirAmaUlasilamaz foreign key'in yumuşak silmeyi
// YAKALAMADIĞINI ve bunun sonucunun ne olduğunu birlikte ölçer.
//
// CreatePriceRule bir yazma yolu olmasına rağmen TEK depo çağrısı yapar: kural
// hangi fiyata bağlanacaksa onu çağıran verir, servis öncesinde hiçbir şey
// okumaz. Yani "oku → karar ver → yaz" yarışı burada YOKTUR ve tax'taki kusurun
// biçimi bu modülde hiçbir metotta bulunmaz.
//
// Yine de yazılan kural, silinmiş bir fiyatın altına inebilir: price_rule
// price(id)'ye referans verir ama silme YUMUŞAKTIR ve satırı yerinde bırakır.
// Test bunu kanıtlar ve HEMEN ARDINDAN sonucu ölçer: fiyatın kendisi zaten
// silinmiş olduğu için aday sorgusuna girmez, dolayısıyla kural hiçbir hesabı
// değiştiremez. Ölçülen sonuç ULAŞILAMAZ bir satırdır — müşterinin ödediği
// tutar değişmez.
//
// Test bir kusuru değil, repository.CreatePriceRule godoc'undaki cümleyi tutar:
// o cümle bir süre "kuralın yetim kalması yapısal olarak imkânsızdır" diyordu
// ve ölçüm bunun yanlış olduğunu gösterdi (2026-09-06).
func TestSilinmisFiyataKuralYazilabilirAmaUlasilamaz(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{
		{CurrencyCode: "TRY", Amount: 10000},
	})
	require.NoError(t, err)

	prices, err := svc.ListPrices(ctx, set.ID)
	require.NoError(t, err)
	require.Len(t, prices, 1)
	priceID := prices[0].ID

	// Kap silinince fiyatları da yumuşak silinir; satırlar yerinde kalır.
	require.NoError(t, svc.DeletePriceSet(ctx, set.ID))

	_, err = svc.CreatePriceRule(ctx, priceID, service.RuleInput{
		Attribute: "region_id",
		Operator:  models.OpEq,
		Values:    []string{"reg_1"},
	})
	require.NoError(t, err,
		"foreign key yumuşak silmeyi yakalamaz: silinmiş fiyatın altına kural yazılabilir")

	// SONUÇ: kural yazıldı ama hiçbir hesap yolu ona ulaşamaz, çünkü fiyatın
	// kendisi aday sorgusundan elenir.
	_, err = svc.CalculatePrice(ctx, set.ID, service.CalculateParams{
		CurrencyCode: "TRY",
		Attributes:   map[string]string{"region_id": "reg_1"},
	})
	require.Error(t, err, "silinmiş kabın fiyatı hesaba girmemeli")
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}
