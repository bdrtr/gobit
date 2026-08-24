//go:build integration

// Bu dosyadaki testler gerçek bir PostgreSQL örneği (dolayısıyla Docker)
// gerektirir; `make test` hızlı kalsın diye `integration` etiketiyle
// ayrılmıştır. Çalıştırmak için: make test-integration
//
// Birim testleri sahte bir depo ile servisin KARARLARINI kanıtlar. Buradaki
// testler kararların dayandığı ZEMİNİ kanıtlar: migration'ın VERİ VARKEN geri
// alınabildiğini, kısıtların gerçekten uygulandığını, sağlayıcının durumunun
// süreç dışında yaşadığını ve idempotency iddiasının veritabanı düzeyinde
// tuttuğunu. Özellikle "aynı anahtarla iki Create tek gönderi üretir" iddiası
// yalnızca burada, gerçek goroutine'lerle gerçek benzersiz indeks üzerinde
// sınanabilir.
package fulfillment_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/errors"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/fulfillment"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/manual"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/repository"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

const postgresImage = "postgres:16-alpine"

// modulTablolari modülün sahip olduğu tablolardır; migration testleri bu
// listeyi kullanır.
var modulTablolari = []string{
	"shipping_profiles", "shipping_options", "shipping_option_rules",
	"fulfillments", "fulfillment_items", "fulfillment_manual_shipments",
}

// Test verisinde kullanılan sabitler. Referans BAŞKA bir modüle (siparişe)
// aittir; bu modül varlığını doğrulamaz (Prensip 2.2).
const (
	testReferans = "order_TEST"
	testPara     = "TRY"
	testBolge    = "reg_TEST"
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
	// Eşzamanlılık testleri onlarca goroutine'i aynı anda koşturur; her işlem
	// bir bağlantı tuttuğu için havuz varsayılandan geniş açılır.
	cfg.MaxConns = 24
	testPool, err = db.New(ctx, cfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bağlantı havuzu açılamadı: %v\n", err)
		return 1
	}
	defer testPool.Close()

	if err := db.Migrate(ctx, testDSN, fulfillment.New().Migrations(), fulfillment.ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "migration uygulanamadı: %v\n", err)
		return 1
	}

	return m.Run()
}

// yeniServis gerçek depo ve GERÇEK manuel sağlayıcı üzerinde çalışan bir
// servis kurar.
func yeniServis(t *testing.T) (*service.Service, *manual.Provider) {
	t.Helper()

	repo := repository.New(testPool.Pool())
	prov := manual.New(repo, nil)
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(prov))

	svc, err := service.New(service.Options{Store: repo, Providers: registry})
	require.NoError(t, err)
	return svc, prov
}

// sayanSaglayici gerçek sağlayıcıyı sarar ve ÇAĞRI SAYAR.
//
// "Tek gönderi üretilir" iddiası ancak böyle KESİN olarak sınanabilir: manuel
// sağlayıcı kendi içinde idempotent olduğu için, ikinci bir çağrının yaptığı
// işi defterdeki satır sayısına bakarak ayırt etmek yetmez — asıl ölçülmesi
// gereken sağlayıcıya KAÇ KEZ GİDİLDİĞİDİR. Gerçek bir kargo firmasında her
// çağrı bir etiket demektir.
type sayanSaglayici struct {
	inner *manual.Provider

	mu     sync.Mutex
	quote  int
	create int
	cancel int
}

// Dekoratörün çekirdek sözleşmesini karşıladığı derleme zamanında doğrulanır.
var _ coreprovider.FulfillmentProvider = (*sayanSaglayici)(nil)

// ID sarılan sağlayıcının kimliğini döner; seçenekler aynı adla açılır.
func (s *sayanSaglayici) ID() string { return s.inner.ID() }

// Quote çağrıyı sayar ve iletir.
func (s *sayanSaglayici) Quote(
	ctx context.Context,
	in coreprovider.QuoteInput,
) (coreprovider.ShippingQuote, error) {
	s.mu.Lock()
	s.quote++
	s.mu.Unlock()
	return s.inner.Quote(ctx, in)
}

// Create çağrıyı sayar ve iletir.
func (s *sayanSaglayici) Create(
	ctx context.Context,
	in coreprovider.CreateFulfillmentInput,
) (coreprovider.Fulfillment, error) {
	s.mu.Lock()
	s.create++
	s.mu.Unlock()
	return s.inner.Create(ctx, in)
}

// Cancel çağrıyı sayar ve iletir.
func (s *sayanSaglayici) Cancel(ctx context.Context, fulfillmentID string) error {
	s.mu.Lock()
	s.cancel++
	s.mu.Unlock()
	return s.inner.Cancel(ctx, fulfillmentID)
}

// sayimlar sağlayıcıya yapılan çağrı sayılarını döner.
func (s *sayanSaglayici) sayimlar() (quote, create, cancel int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.quote, s.create, s.cancel
}

// yeniSayanServis çağrıları sayan bir sağlayıcı üzerinde servis kurar.
func yeniSayanServis(t *testing.T) (*service.Service, *sayanSaglayici) {
	t.Helper()

	repo := repository.New(testPool.Pool())
	sayan := &sayanSaglayici{inner: manual.New(repo, nil)}
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(sayan))

	svc, err := service.New(service.Options{Store: repo, Providers: registry})
	require.NoError(t, err)
	return svc, sayan
}

// yeniProfil test için benzersiz adlı bir kargo profili açar.
func yeniProfil(ctx context.Context, t *testing.T, svc *service.Service) models.ShippingProfile {
	t.Helper()

	profile, err := svc.CreateShippingProfile(ctx, service.CreateProfileInput{
		Name: "profil-" + models.NewShippingProfileID(),
	})
	require.NoError(t, err)
	return profile
}

// yeniSecenek test için sabit ücretli bir kargo seçeneği açar.
func yeniSecenek(
	ctx context.Context,
	t *testing.T,
	svc *service.Service,
	profileID string,
	tutar int64,
) models.ShippingOption {
	t.Helper()

	option, err := svc.CreateShippingOption(ctx, service.CreateOptionInput{
		Name:              "secenek-" + models.NewShippingOptionID(),
		ProviderID:        manual.ID,
		ShippingProfileID: profileID,
		Amount:            tutar,
		CurrencyCode:      testPara,
		RegionID:          testBolge,
	})
	require.NoError(t, err)
	return option
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

// TestMigrationVeriVarkenGeriAlinabilir migration'ın DOLU bir şemada
// uygulanıp geri alınabildiğini doğrular.
//
// internal/arch'taki kapı yalnızca BOŞ bir veritabanında up -> down -> up
// koşar ve veriye bağlı geri alma hatalarını yakalayamaz. Buradaki test önce
// profil seçenek, kural, gönderi, kalem ve sağlayıcı defterinden oluşan TAM
// grafiği yazar; foreign key sırasını yanlış kuran bir down dosyası ancak
// böyle düşer.
func TestMigrationVeriVarkenGeriAlinabilir(t *testing.T) {
	ctx := context.Background()
	src := fulfillment.New().Migrations()
	svc, _ := yeniServis(t)

	profile := yeniProfil(ctx, t, svc)
	option := yeniSecenek(ctx, t, svc, profile.ID, 2_500)
	_, err := svc.CreateShippingOptionRule(ctx, option.ID, service.CreateRuleInput{
		Attribute: service.AttrSubtotal,
		Operator:  "gte",
		Values:    []string{"50000"},
	})
	require.NoError(t, err)

	_, err = svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
		Reference:        testReferans,
		ShippingOptionID: option.ID,
		IdempotencyKey:   "migration-" + option.ID,
		Items:            []service.FulfillmentItemInput{{LineItemID: "line_1", Quantity: 2}},
	})
	require.NoError(t, err)

	for _, table := range modulTablolari {
		require.True(t, tabloVar(ctx, t, table), "%s başlangıçta var olmalı", table)
	}

	require.NoError(t, db.MigrateDown(ctx, testDSN, src, fulfillment.ModuleName, 0),
		"down başarısız — bu, modülün bir daha migrate EDİLEMEMESİ demektir")
	for _, table := range modulTablolari {
		assert.False(t, tabloVar(ctx, t, table), "%s geri alma sonrası kalmamalı", table)
	}

	require.NoError(t, db.Migrate(ctx, testDSN, src, fulfillment.ModuleName))
	for _, table := range modulTablolari {
		assert.True(t, tabloVar(ctx, t, table), "%s yeniden uygulanmalı", table)
	}

	version, dirty, err := db.Version(ctx, testDSN, fulfillment.ModuleName)
	require.NoError(t, err)
	assert.False(t, dirty, "yarıda kalmış migration olmamalı")
	assert.Equal(t, uint(1), version)
}

// TestCrossModuleForeignKeyYok modülün tablolarındaki TÜM foreign key'lerin
// yine modülün kendi tablolarına gittiğini doğrular (Prensip 2.2).
//
// Özellikle fulfillments.reference bir sipariş kimliğidir,
// shipping_options.region_id bir bölge kimliğidir ve
// fulfillment_items.line_item_id bir sipariş satırı kimliğidir; üçü de
// foreign key OLAMAZ.
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

// TestKatalogCRUD profil, seçenek ve kuralın gerçek şema üzerinde uçtan uca
// yönetilebildiğini doğrular.
func TestKatalogCRUD(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	profile := yeniProfil(ctx, t, svc)
	assert.Equal(t, models.ProfileDefault, profile.Type)

	option := yeniSecenek(ctx, t, svc, profile.ID, 2_500)
	rule, err := svc.CreateShippingOptionRule(ctx, option.ID, service.CreateRuleInput{
		Attribute: service.AttrSubtotal,
		Operator:  "gte",
		Values:    []string{"50000"},
	})
	require.NoError(t, err)

	okunan, err := svc.GetShippingOption(ctx, option.ID)
	require.NoError(t, err)
	require.Len(t, okunan.Rules, 1)
	assert.Equal(t, rule.ID, okunan.Rules[0].ID)

	yeniAd := "guncellenmis-" + option.ID
	yeniTutar := int64(1_750)
	guncel, err := svc.UpdateShippingOption(ctx, option.ID, service.UpdateOptionInput{
		Name:   &yeniAd,
		Amount: &yeniTutar,
	})
	require.NoError(t, err)
	assert.Equal(t, yeniAd, guncel.Name)
	assert.Equal(t, yeniTutar, guncel.Amount)
	assert.Equal(t, manual.ID, guncel.ProviderID, "sağlayıcı değişmemeli")

	// Seçeneği duran profil silinemez; kural, sipariş akışının kargo
	// seçeneksiz kalmamasını sağlar.
	err = svc.DeleteShippingProfile(ctx, profile.ID)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "hata errors.Conflict olmalı: %v", err)

	require.NoError(t, svc.DeleteShippingOptionRule(ctx, rule.ID))
	require.NoError(t, svc.DeleteShippingOption(ctx, option.ID))
	require.NoError(t, svc.DeleteShippingProfile(ctx, profile.ID))

	_, err = svc.GetShippingOption(ctx, option.ID)
	assert.True(t, errors.IsNotFound(err), "silinen seçenek okunamaz olmalı: %v", err)
}

// TestHesaplananSecenegeTutarYazilamaz şemadaki kısıtın SON SAVUNMA olarak
// çalıştığını doğrular.
//
// Servis bunu zaten reddeder; buradaki iddia, doğrudan SQL ile yapılan bir
// müdahalenin de durdurulduğudur.
func TestHesaplananSecenegeTutarYazilamaz(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)
	profile := yeniProfil(ctx, t, svc)

	option, err := svc.CreateShippingOption(ctx, service.CreateOptionInput{
		Name:              "hesaplanan-" + models.NewShippingOptionID(),
		ProviderID:        manual.ID,
		ShippingProfileID: profile.ID,
		PriceType:         "calculated",
		CurrencyCode:      testPara,
	})
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx,
		`UPDATE shipping_options SET amount = 500 WHERE id = $1`, option.ID)
	require.Error(t, err, "hesaplanan seçeneğe tutar yazılamamalı")
}

// TestUctanUcaGonderiAkisi Faz 7'nin istediği tam akışı GERÇEK sağlayıcıyla
// yürütür: uygunluk -> gönderi aç -> kargoya ver -> teslim et.
//
// Her adımda hem modülün kaydı hem SAĞLAYICININ defteri denetlenir; ikisinin
// ayrıştığı bir hata ancak iki tarafa birden bakılarak görülür.
func TestUctanUcaGonderiAkisi(t *testing.T) {
	ctx := context.Background()
	svc, prov := yeniServis(t)

	profile := yeniProfil(ctx, t, svc)
	option, err := svc.CreateShippingOption(ctx, service.CreateOptionInput{
		Name:              "hesaplanan-" + models.NewShippingOptionID(),
		ProviderID:        manual.ID,
		ShippingProfileID: profile.ID,
		PriceType:         "calculated",
		CurrencyCode:      testPara,
		RegionID:          testBolge,
		Data: map[string]any{
			manual.DataKeyBaseAmount:        1_000,
			manual.DataKeyPerKilogramAmount: 500,
			manual.DataKeyTrackingNumber:    "TK-E2E",
		},
	})
	require.NoError(t, err)

	secenekler, err := svc.ListShippingOptionsFor(ctx, service.ListOptionsInput{
		RegionID:           testBolge,
		CurrencyCode:       testPara,
		ShippingProfileIDs: []string{profile.ID},
		TotalWeight:        1_200,
	})
	require.NoError(t, err)
	require.Len(t, secenekler, 1)
	// 1000 taban + 500 × ⌈1200/1000⌉ = 1000 + 1000.
	assert.Equal(t, int64(2_000), secenekler[0].Amount, "ücret sağlayıcının formülünden gelmeli")

	ful, err := svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
		Reference:        testReferans,
		ShippingOptionID: option.ID,
		IdempotencyKey:   "e2e-" + option.ID,
		Items:            []service.FulfillmentItemInput{{LineItemID: "line_1", Quantity: 2}},
	})
	require.NoError(t, err)
	assert.Equal(t, models.StatusPending, ful.Status)
	require.NotEmpty(t, ful.ExternalID, "sağlayıcının kimliği yazılmalı")
	assert.Equal(t, "TK-E2E", ful.TrackingNumber)
	require.Len(t, ful.Items, 1)

	saglayiciKaydi, err := prov.GetShipment(ctx, ful.ExternalID)
	require.NoError(t, err)
	assert.Equal(t, ful.ID, saglayiciKaydi.Reference,
		"sağlayıcı, mutabakat için GÖNDERİNİN kimliğini saklamalı")

	kargoda, err := svc.MarkShipped(ctx, ful.ID, "TK-E2E", "https://kargo.example/TK-E2E")
	require.NoError(t, err)
	assert.Equal(t, models.StatusShipped, kargoda.Status)
	require.NotNil(t, kargoda.ShippedAt)

	teslim, err := svc.MarkDelivered(ctx, ful.ID)
	require.NoError(t, err)
	assert.Equal(t, models.StatusDelivered, teslim.Status)
	require.NotNil(t, teslim.DeliveredAt)

	// Teslim edilmiş gönderi iptal EDİLEMEZ; çaresi iadedir.
	err = svc.CancelFulfillment(ctx, ful.ID)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "hata errors.Conflict olmalı: %v", err)
}

// TestEszamanliIkiCreateTekGonderiUretir idempotency iddiasını GERÇEK
// benzersiz indeks ve gerçek goroutine'lerle sınar.
//
// Birim testindeki sahte depo yarışı taklit eder; buradaki test aynı iddiayı
// ON CONFLICT DO NOTHING ve satır kilidi üzerinde kanıtlar. Ölçülen şey
// sağlayıcıya KAÇ KEZ gidildiğidir: gerçek bir kargo firmasında her çağrı bir
// etiket demektir.
func TestEszamanliIkiCreateTekGonderiUretir(t *testing.T) {
	ctx := context.Background()
	svc, sayan := yeniSayanServis(t)

	profile := yeniProfil(ctx, t, svc)
	option := yeniSecenek(ctx, t, svc, profile.ID, 2_500)
	anahtar := "yaris-" + option.ID

	const eszamanli = 8
	kimlikler := make([]string, eszamanli)
	hatalar := make([]error, eszamanli)

	var basla sync.WaitGroup
	var bitti sync.WaitGroup
	basla.Add(1)
	bitti.Add(eszamanli)

	for i := range eszamanli {
		go func() {
			defer bitti.Done()
			basla.Wait()
			ful, err := svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
				Reference:        testReferans,
				ShippingOptionID: option.ID,
				IdempotencyKey:   anahtar,
			})
			kimlikler[i], hatalar[i] = ful.ID, err
		}()
	}
	basla.Done()
	bitti.Wait()

	for i, err := range hatalar {
		require.NoErrorf(t, err, "%d. çağrı hata döndü", i)
	}
	for i := 1; i < eszamanli; i++ {
		assert.Equal(t, kimlikler[0], kimlikler[i], "tüm çağrılar aynı gönderiyi dönmeli")
	}

	_, create, _ := sayan.sayimlar()
	assert.Equal(t, 1, create, "sağlayıcıya TAM OLARAK bir kez gidilmeli")

	var satir int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT COUNT(*) FROM fulfillments WHERE idempotency_key = $1 AND deleted_at IS NULL`,
		anahtar).Scan(&satir))
	assert.EqualValues(t, 1, satir, "benzersiz indeks tek satıra izin vermeli")
}

// TestIptalIkiKezCagrilabilir saga telafisinin şartını gerçek veritabanı
// üzerinde doğrular.
func TestIptalIkiKezCagrilabilir(t *testing.T) {
	ctx := context.Background()
	svc, sayan := yeniSayanServis(t)

	profile := yeniProfil(ctx, t, svc)
	option := yeniSecenek(ctx, t, svc, profile.ID, 2_500)
	ful, err := svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
		Reference:        testReferans,
		ShippingOptionID: option.ID,
		IdempotencyKey:   "iptal-" + option.ID,
	})
	require.NoError(t, err)

	require.NoError(t, svc.CancelFulfillment(ctx, ful.ID))
	require.NoError(t, svc.CancelFulfillment(ctx, ful.ID), "ikinci iptal hata dönmemeli")

	_, _, cancel := sayan.sayimlar()
	assert.Equal(t, 1, cancel, "sağlayıcıya yalnızca bir kez gidilmeli")

	okunan, err := svc.GetFulfillment(ctx, ful.ID)
	require.NoError(t, err)
	assert.Equal(t, models.StatusCanceled, okunan.Status)
	require.NotNil(t, okunan.CanceledAt)
}

// TestEszamanliIptalTekCagriYapar satır kilidinin gerçekten çalıştığını
// doğrular.
//
// Kilit olmasaydı, birden çok goroutine gönderiyi "pending" görür ve hepsi
// sağlayıcıya giderdi.
func TestEszamanliIptalTekCagriYapar(t *testing.T) {
	ctx := context.Background()
	svc, sayan := yeniSayanServis(t)

	profile := yeniProfil(ctx, t, svc)
	option := yeniSecenek(ctx, t, svc, profile.ID, 2_500)
	ful, err := svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
		Reference:        testReferans,
		ShippingOptionID: option.ID,
		IdempotencyKey:   "eszamanli-iptal-" + option.ID,
	})
	require.NoError(t, err)

	const eszamanli = 8
	hatalar := make([]error, eszamanli)

	var basla sync.WaitGroup
	var bitti sync.WaitGroup
	basla.Add(1)
	bitti.Add(eszamanli)

	for i := range eszamanli {
		go func() {
			defer bitti.Done()
			basla.Wait()
			hatalar[i] = svc.CancelFulfillment(ctx, ful.ID)
		}()
	}
	basla.Done()
	bitti.Wait()

	for i, err := range hatalar {
		require.NoErrorf(t, err, "%d. iptal hata döndü", i)
	}
	_, _, cancel := sayan.sayimlar()
	assert.Equal(t, 1, cancel, "sağlayıcıya TAM OLARAK bir iptal gitmeli")
}

// TestSaglayiciDefteriSurecDisindaYasar manuel sağlayıcının durumunun
// veritabanında olduğunu doğrular.
//
// Bellekte tutulsaydı, YENİ bir sağlayıcı örneği (süreç yeniden başlatmasının
// karşılığı) gönderiyi bulamaz ve saga telafisi hiçbir zaman çalışamazdı.
func TestSaglayiciDefteriSurecDisindaYasar(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	profile := yeniProfil(ctx, t, svc)
	option := yeniSecenek(ctx, t, svc, profile.ID, 2_500)
	ful, err := svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
		Reference:        testReferans,
		ShippingOptionID: option.ID,
		IdempotencyKey:   "kalici-" + option.ID,
	})
	require.NoError(t, err)

	// Yeni bir sağlayıcı örneği: sürecin yeniden başlamasının karşılığı.
	yeniProv := manual.New(repository.New(testPool.Pool()), nil)
	saklanan, err := yeniProv.GetShipment(ctx, ful.ExternalID)
	require.NoError(t, err, "gönderi yeni bir sağlayıcı örneğinden okunabilmeli")
	assert.Equal(t, models.StatusPending, saklanan.Status)

	require.NoError(t, yeniProv.Cancel(ctx, ful.ExternalID),
		"telafi süreç yeniden başladıktan sonra da çalışmalı")
}

// TestModulContainerYuzeylerineKaydeder modülün ilan ettiği tüm adların
// gerçekten çözülebildiğini doğrular.
//
// ADR 0001/0006 gereği tüketiciler bu adları KENDİ dar arayüzleriyle çözer;
// bir adın kaydedilmeyi unutulması ancak çalışma zamanında görülür.
func TestModulContainerYuzeylerineKaydeder(t *testing.T) {
	ctx := context.Background()

	c := container.New(nil)
	require.NoError(t, c.Provide("core.db", testPool))

	mod := fulfillment.New()
	require.NoError(t, mod.Register(ctx, c))

	svc, err := container.Resolve[*service.Service](c, fulfillment.ServiceName)
	require.NoError(t, err)
	assert.NotNil(t, svc)

	interop, err := container.Resolve[*service.Interop](c, fulfillment.InteropName)
	require.NoError(t, err)
	assert.NotNil(t, interop)

	providers, err := container.Resolve[*service.ProviderRegistry](c, fulfillment.ProvidersName)
	require.NoError(t, err)
	assert.Equal(t, []string{manual.ID}, providers.IDs())

	qp, err := container.Resolve[query.Provider](c, fulfillment.ProviderName)
	require.NoError(t, err)
	assert.Equal(t, service.EntityName, qp.Entity())
	assert.Equal(t, "shipping_option.query", fulfillment.ProviderName)
}

// TestInteropYuzeyiUctanUcaCalisir modüller arası ilkel yüzeyin gerçek
// veritabanı üzerinde çalıştığını doğrular.
//
// Saga'nın göreceği yüzey budur; JSON şemasının ve idempotency'nin birlikte
// tuttuğu ancak burada görülür.
func TestInteropYuzeyiUctanUcaCalisir(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)
	interop := service.NewInterop(svc)

	profile := yeniProfil(ctx, t, svc)
	option := yeniSecenek(ctx, t, svc, profile.ID, 2_500)

	istek := fmt.Sprintf(
		`{"region_id":%q,"currency_code":%q,"shipping_profile_ids":[%q],"subtotal":50000}`,
		testBolge, testPara, profile.ID)
	yanit, err := interop.ListOptionsJSON(ctx, []byte(istek))
	require.NoError(t, err)
	assert.Contains(t, string(yanit), option.ID)
	assert.Contains(t, string(yanit), `"amount":2500`)

	ilk, err := interop.CreateFulfillment(ctx, testReferans, option.ID, "interop-"+option.ID)
	require.NoError(t, err)
	ikinci, err := interop.CreateFulfillment(ctx, testReferans, option.ID, "interop-"+option.ID)
	require.NoError(t, err)
	assert.Equal(t, ilk, ikinci, "aynı anahtar tek gönderi üretmeli")

	require.NoError(t, interop.CancelFulfillment(ctx, ilk))
	require.NoError(t, interop.CancelFulfillment(ctx, ilk), "telafi iki kez çağrılabilmeli")

	durum, err := interop.FulfillmentStatus(ctx, ilk)
	require.NoError(t, err)
	assert.Equal(t, "canceled", durum)
}

// TestAyniProfilAdiIkinciKezKullanilamaz benzersiz indeksin gerçekten
// uygulandığını doğrular.
func TestAyniProfilAdiIkinciKezKullanilamaz(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	ad := "tekil-" + models.NewShippingProfileID()
	_, err := svc.CreateShippingProfile(ctx, service.CreateProfileInput{Name: ad})
	require.NoError(t, err)

	_, err = svc.CreateShippingProfile(ctx, service.CreateProfileInput{Name: ad})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "hata errors.Conflict olmalı: %v", err)
}

// TestQuerySaglayicisiGercekSemaUzerindeCalisir ADR 0004'ün okuma yüzeyini
// gerçek veriyle doğrular.
func TestQuerySaglayicisiGercekSemaUzerindeCalisir(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)
	provider := service.NewQueryProvider(svc)

	profile := yeniProfil(ctx, t, svc)
	option := yeniSecenek(ctx, t, svc, profile.ID, 2_500)

	kayitlar, err := provider.FetchByIDs(ctx, []string{option.ID}, []string{"id", "amount", "provider_id"})
	require.NoError(t, err)
	require.Len(t, kayitlar, 1)
	assert.Equal(t, option.ID, kayitlar[0]["id"])
	assert.Equal(t, int64(2_500), kayitlar[0]["amount"])
	assert.Equal(t, manual.ID, kayitlar[0]["provider_id"])

	suzulmus, err := provider.List(ctx, query.ListOptions{
		Filters: map[string]any{"shipping_profile_id": profile.ID},
		Fields:  []string{"id"},
	})
	require.NoError(t, err)
	require.Len(t, suzulmus, 1)
	assert.Equal(t, option.ID, suzulmus[0]["id"])
}

// TestAyniSatirGonderideIkiKezYerAlamaz benzersiz indeksin son savunma olarak
// çalıştığını doğrular.
func TestAyniSatirGonderideIkiKezYerAlamaz(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	profile := yeniProfil(ctx, t, svc)
	option := yeniSecenek(ctx, t, svc, profile.ID, 2_500)
	ful, err := svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
		Reference:        testReferans,
		ShippingOptionID: option.ID,
		IdempotencyKey:   "kalem-" + option.ID,
		Items:            []service.FulfillmentItemInput{{LineItemID: "line_1", Quantity: 1}},
	})
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO fulfillment_items (id, fulfillment_id, line_item_id, quantity)
         VALUES ($1, $2, 'line_1', 1)`, models.NewFulfillmentItemID(), ful.ID)
	require.Error(t, err, "aynı sipariş satırı iki kez yazılamamalı")
}

// TestProfilSilmeAcikSecenekYazmasiniBekler kontrol-sonra-yaz yarışının
// GERÇEK Postgres'te kapandığını doğrular.
//
// Regresyon: DeleteShippingProfile profili KİLİTSİZ okuyup sayıyor, sonra
// yumuşak siliyordu. Yumuşak silme anahtar olmayan bir sütunu güncellediği için
// FOR NO KEY UPDATE alır ve bu kilit, bir seçenek INSERT'ünün foreign key için
// aldığı FOR KEY SHARE ile ÇAKIŞMAZ. Sonuç: açık bir INSERT işlemi varken silme
// beklemeden tamamlanıyor, geriye silinmiş bir profile bağlı CANLI bir seçenek
// kalıyordu.
//
// Test o interleaving'i BİREBİR kurar: açık bir işlemde (henüz commit edilmemiş)
// seçenek satırı yazılır — yani profil satırında yalnızca FK'nın FOR KEY SHARE
// kilidi vardır — ve silme çağrılır. Düzeltmeden ÖNCE silme hatasız tamamlanır
// ve profil NotFound olurdu; düzeltmeyle silme, satırı FOR UPDATE ile almaya
// çalışıp BEKLER ve context süresi dolar.
func TestProfilSilmeAcikSecenekYazmasiniBekler(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	profile := yeniProfil(ctx, t, svc)

	// A: seçenek INSERT'ü açık bir işlemde bekletilir.
	tx, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			t.Logf("işlem geri alınamadı: %v", rbErr)
		}
	}()

	optionID := models.NewShippingOptionID()
	_, err = tx.Exec(ctx,
		`INSERT INTO shipping_options
             (id, name, provider_id, shipping_profile_id, price_type, amount, currency_code, region_id)
         VALUES ($1, 'yaris', $2, $3, 'flat', 2500, $4, $5)`,
		optionID, manual.ID, profile.ID, testPara, testBolge)
	require.NoError(t, err)

	// B: aynı anda profili silmeye çalışan yönetici.
	silmeCtx, iptal := context.WithTimeout(ctx, 2*time.Second)
	defer iptal()

	silmeErr := svc.DeleteShippingProfile(silmeCtx, profile.ID)
	require.Error(t, silmeErr,
		"açık bir seçenek yazması varken profil silme tamamlanmamalı (kilit bekler)")

	require.NoError(t, tx.Commit(ctx))

	// Asıl iddia: profil HÂLÂ canlıdır. Düzeltmeden önce burası NotFound'du ve
	// geriye silinmiş bir profile bağlı canlı bir seçenek kalıyordu.
	okunan, err := svc.GetShippingProfile(ctx, profile.ID)
	require.NoError(t, err, "profil silinmemiş olmalı")
	assert.Nil(t, okunan.DeletedAt)

	// Ve silme artık doğru sebeple reddedilir.
	err = svc.DeleteShippingProfile(ctx, profile.ID)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "hata errors.Conflict olmalı: %v", err)
	assert.Equal(t, service.CodeProfileInUse, errors.CodeOf(err))
}

// TestProfiliSilinmisSecenekVitrindeGorunmez uygunluk sorgusunun ikinci
// savunmasını doğrular.
//
// Normal akışta böyle bir satır artık oluşamaz (yukarıdaki kilit), ama doğrudan
// SQL çalıştıran bir bakım betiği üretebilir. Kargo kuralı ortadan kalkmış bir
// profilin seçeneği vitrinde durmamalıdır.
func TestProfiliSilinmisSecenekVitrindeGorunmez(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	profile := yeniProfil(ctx, t, svc)
	option := yeniSecenek(ctx, t, svc, profile.ID, 2_500)

	oncesi, err := svc.ListShippingOptionsFor(ctx, service.ListOptionsInput{
		RegionID:           testBolge,
		CurrencyCode:       testPara,
		ShippingProfileIDs: []string{profile.ID},
	})
	require.NoError(t, err)
	require.Len(t, oncesi, 1)
	assert.Equal(t, option.ID, oncesi[0].Option.ID)

	// Servis bu durumu üretmez; doğrudan SQL üretir.
	_, err = testPool.Pool().Exec(ctx,
		`UPDATE shipping_profiles SET deleted_at = now() WHERE id = $1`, profile.ID)
	require.NoError(t, err)

	sonrasi, err := svc.ListShippingOptionsFor(ctx, service.ListOptionsInput{
		RegionID:           testBolge,
		CurrencyCode:       testPara,
		ShippingProfileIDs: []string{profile.ID},
	})
	require.NoError(t, err)
	assert.Empty(t, sonrasi, "profili silinmiş seçenek uygunluk listesine girmemeli")
}

// TestKalemAdediUstSiniriSemadaZorlanir para/adet kuralının UYGULAMA
// KATMANINDAN bağımsız olarak da tutduğunu doğrular.
//
// Servis aynı sınırı zaten uygular; buradaki kısıt son savunmadır ve doğrudan
// SQL çalıştıran bir bakım betiğini de durdurur.
func TestKalemAdediUstSiniriSemadaZorlanir(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	profile := yeniProfil(ctx, t, svc)
	option := yeniSecenek(ctx, t, svc, profile.ID, 2_500)
	ful, err := svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
		Reference:        testReferans,
		ShippingOptionID: option.ID,
		IdempotencyKey:   "adet-" + option.ID,
	})
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO fulfillment_items (id, fulfillment_id, line_item_id, quantity)
         VALUES ($1, $2, 'line_1', $3)`,
		models.NewFulfillmentItemID(), ful.ID, models.MaxQuantity+1)
	require.Error(t, err, "üst sınırı aşan adet şemaya yazılamamalı")

	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO fulfillment_items (id, fulfillment_id, line_item_id, quantity)
         VALUES ($1, $2, 'line_2', $3)`,
		models.NewFulfillmentItemID(), ful.ID, models.MaxQuantity)
	require.NoError(t, err, "sınırdaki adet yazılabilmeli")
}

// TestGonderisiOlanSecenekFizikselSilinemez yumuşak silmenin neden zorunlu
// olduğunu doğrular.
//
// ON DELETE RESTRICT, geçmişi olan bir seçeneğin kaydını korur; servis bu
// yüzden yalnızca yumuşak silme sunar.
func TestGonderisiOlanSecenekFizikselSilinemez(t *testing.T) {
	ctx := context.Background()
	svc, _ := yeniServis(t)

	profile := yeniProfil(ctx, t, svc)
	option := yeniSecenek(ctx, t, svc, profile.ID, 2_500)
	_, err := svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
		Reference:        testReferans,
		ShippingOptionID: option.ID,
		IdempotencyKey:   "restrict-" + option.ID,
	})
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx, `DELETE FROM shipping_options WHERE id = $1`, option.ID)
	require.Error(t, err, "gönderisi olan seçenek fiziksel olarak silinememeli")

	require.NoError(t, svc.DeleteShippingOption(ctx, option.ID),
		"yumuşak silme her zaman mümkün olmalı")
}
