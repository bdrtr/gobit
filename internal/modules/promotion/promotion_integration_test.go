//go:build integration

// Bu dosyadaki testler gerçek bir PostgreSQL örneği (dolayısıyla Docker)
// gerektirir; `make test` hızlı kalsın diye `integration` etiketiyle
// ayrılmıştır. Çalıştırmak için: make test-integration
//
// Birim testleri servisin KARARLARINI kanıtlar (indirim aritmetiği, eleme,
// tahsis). Buradaki testler kararların dayandığı ZEMİNİ kanıtlar:
// migration'ın geri alınabildiğini, kısıtların gerçekten uygulandığını ve
// eşzamanlılık iddiasının veritabanı düzeyinde tuttuğunu.
//
// Özellikle "eşzamanlı Redeem sayacı ve bütçeyi bozamaz" iddiası YALNIZCA
// burada, gerçek goroutine'lerle gerçek satır kilitleri üzerinde sınanabilir:
// bellek içi taklit o iddiayı kanıtlayamaz, çünkü kilitler taklidin değil
// veritabanının içindedir.
package promotion_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/promotion"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository"
	"github.com/bdrtr/gobit/internal/modules/promotion/service"
)

const postgresImage = "postgres:16-alpine"

// modulTablolari modülün sahip olduğu tablolardır; migration testleri bu
// listeyi kullanır.
var modulTablolari = []string{
	"campaign", "promotion", "promotion_application_method",
	"promotion_rule", "promotion_redemption",
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
	// Eşzamanlılık testleri onlarca goroutine'i aynı anda koşturur; her işlem
	// bir bağlantı tuttuğu için havuz varsayılandan geniş açılır.
	cfg.MaxConns = 24
	testPool, err = db.New(ctx, cfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bağlantı havuzu açılamadı: %v\n", err)
		return 1
	}
	defer testPool.Close()

	if err := db.Migrate(ctx, testDSN, promotion.New(nil).Migrations(), promotion.ModuleName); err != nil {
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

// benzersizKod test başına çakışmayan bir kupon kodu üretir.
//
// Kod BENZERSİZ bir indekse girdiği için testler birbirinin kodunu kullanamaz;
// kimlik üreticisi zaten çakışmayan bir gövde ürettiğinden ondan türetilir.
func benzersizKod() string {
	return "K" + models.NewPromotionID(time.Now())[len(models.PromotionIDPrefix):]
}

// aktifPromosyon yöntemi kurulmuş, aktif bir promosyon oluşturur.
func aktifPromosyon(ctx context.Context, t *testing.T, svc *service.Service, in service.PromotionInput) models.Promotion {
	t.Helper()

	if in.Code == "" {
		in.Code = benzersizKod()
	}
	if in.Status == "" {
		in.Status = models.PromotionActive
	}
	promo, err := svc.CreatePromotion(ctx, in)
	require.NoError(t, err)

	_, err = svc.SetApplicationMethod(ctx, promo.ID, service.ApplicationMethodInput{
		Type:       models.MethodPercentage,
		TargetType: models.TargetItems,
		Allocation: models.AllocationEach,
		Value:      2000,
	})
	require.NoError(t, err)
	return promo
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

// TestMigrationlarGercektenGeriAlinabilir migration'ın uygulanıp geri
// alınabildiğini ve YENİDEN uygulanabildiğini doğrular (plan Bölüm 8).
//
// up->down->up döngüsü şarttır: yalnızca "down dosyası var mı" diye bakan bir
// test, DROP sırası bağımlılığı yüzünden patlayan bir down'ı yakalayamaz.
func TestMigrationlarGercektenGeriAlinabilir(t *testing.T) {
	ctx := context.Background()
	src := promotion.New(nil).Migrations()

	for _, table := range modulTablolari {
		require.True(t, tabloVar(ctx, t, table), "%s başlangıçta var olmalı", table)
	}

	require.NoError(t, db.MigrateDown(ctx, testDSN, src, promotion.ModuleName, 0))
	for _, table := range modulTablolari {
		assert.False(t, tabloVar(ctx, t, table), "%s geri alma sonrası kalmamalı", table)
	}

	require.NoError(t, db.Migrate(ctx, testDSN, src, promotion.ModuleName))
	for _, table := range modulTablolari {
		assert.True(t, tabloVar(ctx, t, table), "%s yeniden uygulanmalı", table)
	}

	version, dirty, err := db.Version(ctx, testDSN, promotion.ModuleName)
	require.NoError(t, err)
	assert.False(t, dirty, "yarıda kalmış migration olmamalı")
	assert.Equal(t, uint(1), version)
}

// TestCrossModuleForeignKeyYok modülün tablolarındaki TÜM foreign key'lerin
// yine modülün kendi tablolarına gittiğini doğrular (Prensip 2.2).
//
// Özellikle promotion_redemption.reference bir sipariş kimliğidir ve foreign
// key OLAMAZ; bu test o kuralın şemada gerçekten tutulduğunu gösterir.
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

func TestKampanyaYasamDongusu(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	kimlik := "KAMPANYA-" + benzersizKod()

	campaign, err := svc.CreateCampaign(ctx, service.CampaignInput{
		Name:               "Yaz İndirimi",
		CampaignIdentifier: kimlik,
		Description:        "Yaz sezonu",
		BudgetType:         models.BudgetSpend,
		BudgetLimit:        ptr(int64(100_000)),
		BudgetCurrencyCode: "TRY",
	})
	require.NoError(t, err)
	assert.False(t, campaign.CreatedAt.IsZero(), "created_at veritabanından gelmeli")
	assert.Equal(t, "UTC", campaign.CreatedAt.Location().String(), "zaman UTC olmalı")
	assert.Zero(t, campaign.BudgetUsed)

	okunan, err := svc.GetCampaignByIdentifier(ctx, kimlik)
	require.NoError(t, err)
	assert.Equal(t, campaign.ID, okunan.ID)

	// Aynı iş kimliği ikinci kez alınamaz; hakem veritabanı kısmi indeksidir.
	_, err = svc.CreateCampaign(ctx, service.CampaignInput{Name: "İkinci", CampaignIdentifier: kimlik})
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))

	require.NoError(t, svc.DeleteCampaign(ctx, campaign.ID))
	_, err = svc.GetCampaign(ctx, campaign.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err), "yumuşak silinen kampanya okunamamalı")

	// Silinen iş kimliği yeniden kullanılabilir: kısmi indeks yalnızca canlı
	// kayıtları kapsar.
	_, err = svc.CreateCampaign(ctx, service.CampaignInput{Name: "Yeniden", CampaignIdentifier: kimlik})
	assert.NoError(t, err, "silinen bir iş kimliği sonsuza kadar rezerve kalmamalı")
}

func TestKuponKoduBenzersizdir(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	kod := benzersizKod()

	promo, err := svc.CreatePromotion(ctx, service.PromotionInput{Code: kod})
	require.NoError(t, err)

	_, err = svc.CreatePromotion(ctx, service.PromotionInput{Code: kod})
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))

	// Kod BÜYÜK harf saklandığı için küçük harfli deneme de aynı kupona çarpar.
	_, err = svc.CreatePromotion(ctx, service.PromotionInput{Code: lower(kod)})
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))

	require.NoError(t, svc.DeletePromotion(ctx, promo.ID))
	_, err = svc.CreatePromotion(ctx, service.PromotionInput{Code: kod})
	assert.NoError(t, err, "silinen bir kupon kodu yeniden kullanılabilir")
}

func TestUygulamaYontemiYerineKonur(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	promo := aktifPromosyon(ctx, t, svc, service.PromotionInput{})

	ikinci, err := svc.SetApplicationMethod(ctx, promo.ID, service.ApplicationMethodInput{
		Type:         models.MethodFixed,
		TargetType:   models.TargetOrder,
		Value:        5000,
		CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	assert.Equal(t, models.MethodFixed, ikinci.Type)
	assert.Equal(t, models.AllocationAcross, ikinci.Allocation)

	okunan, err := svc.GetApplicationMethod(ctx, promo.ID)
	require.NoError(t, err)
	assert.Equal(t, ikinci.ID, okunan.ID, "promosyon başına TEK yöntem olur; ikincisi üzerine yazar")

	require.NoError(t, svc.DeleteApplicationMethod(ctx, promo.ID))
	_, err = svc.GetApplicationMethod(ctx, promo.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))

	// Yöntemi silinen promosyon hesapta indirim ÜRETMEZ.
	res, err := svc.ComputeDiscounts(ctx, service.ComputeInput{
		CurrencyCode: "TRY",
		Items:        []service.ComputeItem{{ID: "li_1", Amount: 10000, Quantity: 1}},
	})
	require.NoError(t, err)
	assert.Zero(t, res.DiscountTotal)
}

func TestKurallarVeritabaniKisitlariylaKorunur(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	promo := aktifPromosyon(ctx, t, svc, service.PromotionInput{})

	rule, err := svc.AddPromotionRule(ctx, promo.ID, service.RuleInput{
		RuleType:  models.RuleContext,
		Attribute: "customer_group_id",
		Operator:  models.OpIn,
		Values:    []string{"vip", "b2b"},
	})
	require.NoError(t, err)

	kurallar, err := svc.ListPromotionRules(ctx, promo.ID)
	require.NoError(t, err)
	require.Len(t, kurallar, 1)
	assert.Equal(t, []string{"vip", "b2b"}, kurallar[0].Values, "TEXT[] sütunu değerleri sırasıyla taşır")

	require.NoError(t, svc.DeletePromotionRule(ctx, rule.ID))
	kurallar, err = svc.ListPromotionRules(ctx, promo.ID)
	require.NoError(t, err)
	assert.Empty(t, kurallar)
}

func TestHesapGercekVeritabaniUzerindeCalisir(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	kampanya, err := svc.CreateCampaign(ctx, service.CampaignInput{
		Name:               "Yaz",
		CampaignIdentifier: "HESAP-" + benzersizKod(),
		BudgetType:         models.BudgetSpend,
		BudgetLimit:        ptr(int64(1_000_000)),
		BudgetCurrencyCode: "TRY",
	})
	require.NoError(t, err)

	kupon := aktifPromosyon(ctx, t, svc, service.PromotionInput{
		CampaignID: &kampanya.ID,
	})
	_, err = svc.AddPromotionRule(ctx, kupon.ID, service.RuleInput{
		RuleType: models.RuleContext, Attribute: "region_id",
		Operator: models.OpEq, Values: []string{"reg_1"},
	})
	require.NoError(t, err)

	in := service.ComputeInput{
		CurrencyCode: "TRY",
		Context:      map[string]string{"region_id": "reg_1"},
		Items: []service.ComputeItem{
			{ID: "li_1", Amount: 10_000, Quantity: 1},
			{ID: "li_2", Amount: 5_001, Quantity: 1},
		},
		Codes: []string{kupon.Code},
	}

	res, err := svc.ComputeDiscounts(ctx, in)
	require.NoError(t, err)

	assert.Equal(t, int64(2000), res.Items[0].Amount)
	assert.Equal(t, int64(1000), res.Items[1].Amount, "%20 × 5001 = 1000 (aşağı yuvarlanmış)")
	assert.Equal(t, int64(3000), res.DiscountTotal)
	assert.Equal(t, res.ItemsDiscountTotal+res.ShippingDiscountTotal, res.DiscountTotal)

	// Bağlam sağlanmazsa kural eşleşmez ve indirim üretilmez.
	in.Context = nil
	res, err = svc.ComputeDiscounts(ctx, in)
	require.NoError(t, err)
	assert.Zero(t, res.DiscountTotal, "bağlam kuralı gerçek veritabanından okunduğunda da uygulanır")
}

// TestEszamanliRedeemKullanimSinirindaTamOlarakSinirKadarKazanir eşzamanlılık
// iddiasının çekirdeğini kanıtlar.
//
// Uygulama katmanında yapılan bir "önce oku sonra yaz" kontrolü bu testi
// GEÇEMEZ: kazananların sınırla birebir eşit olması satır kilidinden ve
// koşullu UPDATE'ten gelir.
func TestEszamanliRedeemKullanimSinirindaTamOlarakSinirKadarKazanir(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	const sinir = 5
	const yarismaci = 20
	promo := aktifPromosyon(ctx, t, svc, service.PromotionInput{UsageLimit: ptr(int64(sinir))})

	basla := make(chan struct{})
	sonuclar := make([]error, yarismaci)

	var wg sync.WaitGroup
	for i := range yarismaci {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-basla
			_, err := svc.RedeemPromotion(ctx, service.RedeemInput{
				PromotionID:  promo.ID,
				Reference:    fmt.Sprintf("order_%d", i),
				Amount:       100,
				CurrencyCode: "TRY",
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
		assert.Equal(t, repository.CodeUsageLimitReached, errors.CodeOf(err))
	}
	assert.Equal(t, sinir, kazanan, "kullanım hakkı kadar çağrı kazanmalı")

	guncel, err := svc.GetPromotion(ctx, promo.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(sinir), guncel.UsageCount, "sayaç sınırı AŞMAMALI")
}

// TestEszamanliRedeemAyniReferansIcinTekKayitYazar idempotency'nin eşzamanlı
// hâlini kanıtlar: aynı referansla yarışan çağrılardan yalnızca biri kayıt
// yaratır ve sayaç bir artar.
func TestEszamanliRedeemAyniReferansIcinTekKayitYazar(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	const yarismaci = 16
	promo := aktifPromosyon(ctx, t, svc, service.PromotionInput{})

	basla := make(chan struct{})
	kimlikler := make([]string, yarismaci)
	hatalar := make([]error, yarismaci)

	var wg sync.WaitGroup
	for i := range yarismaci {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-basla
			redemption, err := svc.RedeemPromotion(ctx, service.RedeemInput{
				PromotionID:  promo.ID,
				Reference:    "order_tek",
				Amount:       250,
				CurrencyCode: "TRY",
			})
			kimlikler[i], hatalar[i] = redemption.ID, err
		}(i)
	}
	close(basla)
	wg.Wait()

	for i, err := range hatalar {
		require.NoError(t, err, "idempotent çağrı %d hata vermemeli", i)
		assert.Equal(t, kimlikler[0], kimlikler[i], "hepsi AYNI kullanım kaydını görmeli")
	}

	guncel, err := svc.GetPromotion(ctx, promo.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), guncel.UsageCount,
		"aynı referans için sayaç yalnızca BİR kez artmalı")

	page, err := svc.ListRedemptions(ctx, promo.ID, 100, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), page.Count, "defterde tek bir kayıt olmalı")
}

// TestEszamanliRedeemKampanyaButcesiniAsmaz bütçe sayacının eşzamanlı
// kullanımda bozulmadığını kanıtlar.
//
// Sayaç iki promosyon arasında PAYLAŞILIR: ikisi de aynı kampanya satırını
// kilitler ve kilit sırası (önce promosyon, sonra kampanya) sabit olmasaydı bu
// test kilitlenmeyle (deadlock) takılırdı.
func TestEszamanliRedeemKampanyaButcesiniAsmaz(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	const butce = 1000
	const tutar = 100
	const yarismaci = 30

	kampanya, err := svc.CreateCampaign(ctx, service.CampaignInput{
		Name:               "Bütçeli",
		CampaignIdentifier: "BUTCE-" + benzersizKod(),
		BudgetType:         models.BudgetSpend,
		BudgetLimit:        ptr(int64(butce)),
		BudgetCurrencyCode: "TRY",
	})
	require.NoError(t, err)

	ilk := aktifPromosyon(ctx, t, svc, service.PromotionInput{CampaignID: &kampanya.ID})
	ikinci := aktifPromosyon(ctx, t, svc, service.PromotionInput{CampaignID: &kampanya.ID})
	promosyonlar := []models.Promotion{ilk, ikinci}

	basla := make(chan struct{})
	sonuclar := make([]error, yarismaci)

	var wg sync.WaitGroup
	for i := range yarismaci {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-basla
			_, err := svc.RedeemPromotion(ctx, service.RedeemInput{
				PromotionID:  promosyonlar[i%len(promosyonlar)].ID,
				Reference:    fmt.Sprintf("order_%d", i),
				Amount:       tutar,
				CurrencyCode: "TRY",
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
		assert.Equal(t, repository.CodeBudgetExceeded, errors.CodeOf(err))
	}
	assert.Equal(t, butce/tutar, kazanan, "bütçenin izin verdiği kadar kullanım kazanmalı")

	guncel, err := svc.GetCampaign(ctx, kampanya.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(butce), guncel.BudgetUsed, "bütçe sayacı sınırı AŞMAMALI")
}

// TestEszamanliReleaseSayaciBirKezDusurur telafinin idempotency'sinin
// eşzamanlı hâlini kanıtlar.
func TestEszamanliReleaseSayaciBirKezDusurur(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	const yarismaci = 16
	kampanya, err := svc.CreateCampaign(ctx, service.CampaignInput{
		Name:               "Telafi",
		CampaignIdentifier: "TELAFI-" + benzersizKod(),
		BudgetType:         models.BudgetSpend,
		BudgetLimit:        ptr(int64(10_000)),
		BudgetCurrencyCode: "TRY",
	})
	require.NoError(t, err)

	promo := aktifPromosyon(ctx, t, svc, service.PromotionInput{CampaignID: &kampanya.ID})
	_, err = svc.RedeemPromotion(ctx, service.RedeemInput{
		PromotionID: promo.ID, Reference: "order_1", Amount: 750, CurrencyCode: "TRY",
	})
	require.NoError(t, err)

	basla := make(chan struct{})
	geriAlindi := make([]bool, yarismaci)
	hatalar := make([]error, yarismaci)

	var wg sync.WaitGroup
	for i := range yarismaci {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-basla
			released, err := svc.ReleasePromotion(ctx, service.ReleaseInput{
				PromotionID: promo.ID, Reference: "order_1",
			})
			geriAlindi[i], hatalar[i] = released, err
		}(i)
	}
	close(basla)
	wg.Wait()

	var geriAlanSayisi int
	for i, err := range hatalar {
		require.NoError(t, err, "telafi %d hata vermemeli; idempotenttir", i)
		if geriAlindi[i] {
			geriAlanSayisi++
		}
	}
	assert.Equal(t, 1, geriAlanSayisi, "yalnızca BİR çağrı gerçekten geri almalı")

	guncelPromo, err := svc.GetPromotion(ctx, promo.ID)
	require.NoError(t, err)
	assert.Zero(t, guncelPromo.UsageCount, "sayaç yalnızca bir kez düşmeli")

	guncelKampanya, err := svc.GetCampaign(ctx, kampanya.ID)
	require.NoError(t, err)
	assert.Zero(t, guncelKampanya.BudgetUsed, "bütçe yalnızca bir kez düşmeli")
}

func TestReleaseHicKullanimYoksaHataVermez(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	promo := aktifPromosyon(ctx, t, svc, service.PromotionInput{})

	released, err := svc.ReleasePromotion(ctx, service.ReleaseInput{
		PromotionID: promo.ID, Reference: "hic_yazilmadi",
	})

	require.NoError(t, err, "yazmadan patlamış bir adımın telafisi de çalışabilmeli")
	assert.False(t, released)
}

func TestInteropYuzeyiJSONSemasiniKarsilar(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	promo := aktifPromosyon(ctx, t, svc, service.PromotionInput{IsAutomatic: true})

	interop := service.NewInterop(svc)
	istek := []byte(`{
	  "currency_code": "TRY",
	  "items": [{"id": "li_1", "amount": 10000, "quantity": 1}],
	  "shipping_methods": [{"id": "sm_1", "amount": 4990}]
	}`)

	payload, err := interop.ComputeDiscountsJSON(ctx, istek)
	require.NoError(t, err)

	var yanit struct {
		CurrencyCode          string `json:"currency_code"`
		DiscountTotal         int64  `json:"discount_total"`
		ItemsDiscountTotal    int64  `json:"items_discount_total"`
		ShippingDiscountTotal int64  `json:"shipping_discount_total"`
		Items                 []struct {
			ID     string `json:"id"`
			Amount int64  `json:"amount"`
		} `json:"items"`
		Applied []struct {
			PromotionID string `json:"promotion_id"`
			Amount      int64  `json:"amount"`
		} `json:"applied"`
	}
	require.NoError(t, json.Unmarshal(payload, &yanit))

	assert.Equal(t, "TRY", yanit.CurrencyCode)
	assert.Equal(t, int64(2000), yanit.DiscountTotal)
	assert.Equal(t, int64(2000), yanit.ItemsDiscountTotal)
	assert.Zero(t, yanit.ShippingDiscountTotal)
	require.Len(t, yanit.Items, 1)
	assert.Equal(t, "li_1", yanit.Items[0].ID)
	require.Len(t, yanit.Applied, 1)
	assert.Equal(t, promo.ID, yanit.Applied[0].PromotionID)

	// Kullanım ve telafi de ilkel yüzeyden çalışmalı.
	id, err := interop.RedeemPromotion(ctx, promo.ID, "", "order_interop", "TRY", 2000)
	require.NoError(t, err)
	assert.NotEmpty(t, id)

	released, err := interop.ReleasePromotion(ctx, promo.ID, "", "order_interop")
	require.NoError(t, err)
	assert.True(t, released)
}

// TestQuerySaglayicisiGercekDepodaSuzer sağlayıcının Query katmanına yalnızca
// AKTİF promosyonları ve dar bir alan kümesini açtığını doğrular (ADR 0004).
func TestQuerySaglayicisiGercekDepodaSuzer(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	aktif := aktifPromosyon(ctx, t, svc, service.PromotionInput{})
	taslak, err := svc.CreatePromotion(ctx, service.PromotionInput{
		Code: benzersizKod(), Status: models.PromotionDraft,
	})
	require.NoError(t, err)

	provider := service.NewQueryProvider(svc)
	assert.Equal(t, "promotion", provider.Entity())
	assert.Equal(t, "promotion"+query.ProviderSuffix, promotion.ProviderName)

	kayitlar, err := provider.FetchByIDs(ctx, []string{aktif.ID, taslak.ID}, nil)
	require.NoError(t, err)
	require.Len(t, kayitlar, 1, "taslak promosyon okuma yüzeyinden sızmamalı")
	assert.Equal(t, aktif.ID, kayitlar[0]["id"])

	for _, alan := range []string{"usage_count", "metadata"} {
		assert.NotContains(t, kayitlar[0], alan, "%q okuma yüzeyinde bulunmamalı", alan)
	}
}

// TestVeritabaniKisitlariSonSavunmadir servis doğrulaması atlansa bile
// şemanın tutarsız kayıtları reddettiğini doğrular.
//
// Depo doğrudan çağrılır: servis katmanı bu girdileri zaten eler, ama kısıtlar
// elle çalıştırılan bir SQL'e karşı da geçerli olmalıdır.
func TestVeritabaniKisitlariSonSavunmadir(t *testing.T) {
	ctx := context.Background()
	repo := repository.New(testPool.Pool())
	now := time.Now().UTC()

	testler := []struct {
		ad      string
		yaz     func() error
		gerekce string
	}{
		{
			ad: "küçük harfli kupon kodu",
			yaz: func() error {
				_, err := repo.CreatePromotion(ctx, models.Promotion{
					ID: models.NewPromotionID(now), Code: "kucuk",
					Type: models.PromotionStandard, Status: models.PromotionDraft,
				}, now)
				return err
			},
			gerekce: "kod daima BÜYÜK harf saklanır",
		},
		{
			ad: "tanımsız durum",
			yaz: func() error {
				_, err := repo.CreatePromotion(ctx, models.Promotion{
					ID: models.NewPromotionID(now), Code: benzersizKod(),
					Type: models.PromotionStandard, Status: "olmayan",
				}, now)
				return err
			},
			gerekce: "durum kümesi şemada kilitlidir",
		},
		{
			ad: "para birimsiz spend bütçesi",
			yaz: func() error {
				_, err := repo.CreateCampaign(ctx, models.Campaign{
					ID: models.NewCampaignID(now), Name: "X", CampaignIdentifier: benzersizKod(),
					BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(100)),
				}, now)
				return err
			},
			gerekce: "para ölçülü bütçe para birimi olmadan yazılamaz",
		},
		{
			ad: "para birimsiz sabit indirim yöntemi",
			yaz: func() error {
				promo, err := repo.CreatePromotion(ctx, models.Promotion{
					ID: models.NewPromotionID(now), Code: benzersizKod(),
					Type: models.PromotionStandard, Status: models.PromotionDraft,
				}, now)
				if err != nil {
					return err
				}
				_, err = repo.SetApplicationMethod(ctx, models.ApplicationMethod{
					ID: models.NewApplicationMethodID(now), PromotionID: promo.ID,
					Type: models.MethodFixed, TargetType: models.TargetItems,
					Allocation: models.AllocationEach, Value: 100,
				}, now)
				return err
			},
			gerekce: "sabit tutarlı indirim para birimi olmadan yazılamaz",
		},
		{
			ad: "yüzde indirimde para birimi",
			yaz: func() error {
				promo, err := repo.CreatePromotion(ctx, models.Promotion{
					ID: models.NewPromotionID(now), Code: benzersizKod(),
					Type: models.PromotionStandard, Status: models.PromotionDraft,
				}, now)
				if err != nil {
					return err
				}
				_, err = repo.SetApplicationMethod(ctx, models.ApplicationMethod{
					ID: models.NewApplicationMethodID(now), PromotionID: promo.ID,
					Type: models.MethodPercentage, TargetType: models.TargetItems,
					Allocation: models.AllocationEach, Value: 2000, CurrencyCode: "TRY",
				}, now)
				return err
			},
			gerekce: "yüzde indirim para birimi taşımaz",
		},
		{
			ad: "para birimsiz kullanım defteri satırı",
			yaz: func() error {
				promo, err := repo.CreatePromotion(ctx, models.Promotion{
					ID: models.NewPromotionID(now), Code: benzersizKod(),
					Type: models.PromotionStandard, Status: models.PromotionActive,
				}, now)
				if err != nil {
					return err
				}
				_, _, err = repo.Redeem(ctx, models.Redemption{
					ID: models.NewRedemptionID(now), PromotionID: promo.ID,
					Reference: "order_" + benzersizKod(), Amount: 100,
				}, now)
				return err
			},
			gerekce: "defterdeki her tutar hangi para biriminde olduğunu taşımak zorundadır",
		},
		{
			ad: "negatif bütçe sınırı",
			yaz: func() error {
				_, err := repo.CreateCampaign(ctx, models.Campaign{
					ID: models.NewCampaignID(now), Name: "X", CampaignIdentifier: benzersizKod(),
					BudgetType: models.BudgetUsage, BudgetLimit: ptr(int64(-1)),
				}, now)
				return err
			},
			gerekce: "negatif bütçe yazılamaz",
		},
	}

	for _, tt := range testler {
		t.Run(tt.ad, func(t *testing.T) {
			err := tt.yaz()
			require.Error(t, err, tt.gerekce)
			assert.Contains(t,
				[]errors.Kind{errors.KindInvalid, errors.KindConflict}, errors.KindOf(err),
				"kısıt ihlali istemci hatası olarak sınıflandırılmalı: %v", err)
		})
	}
}

// TestRedeemYayindaOlmayanPromosyonuGercekVeritabanindaReddeder taslak ve pasif
// promosyonun kullanılamadığını GERÇEK Postgres üzerinde doğrular.
//
// Denetim promosyon satırı FOR UPDATE ile kilitliyken yapılır; bellek içi
// taklit onu yalnızca taklit eder, burada zemin sınanır.
func TestRedeemYayindaOlmayanPromosyonuGercekVeritabanindaReddeder(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	for _, durum := range []models.PromotionStatus{models.PromotionDraft, models.PromotionInactive} {
		t.Run(string(durum), func(t *testing.T) {
			kampanya, err := svc.CreateCampaign(ctx, service.CampaignInput{
				Name:               "Yaz",
				CampaignIdentifier: "TASLAK-" + benzersizKod(),
				BudgetType:         models.BudgetSpend,
				BudgetLimit:        ptr(int64(1_000_000)),
				BudgetCurrencyCode: "TRY",
			})
			require.NoError(t, err)

			promo := aktifPromosyon(ctx, t, svc, service.PromotionInput{
				Status: durum, CampaignID: &kampanya.ID,
			})

			_, err = svc.RedeemPromotion(ctx, service.RedeemInput{
				PromotionID: promo.ID, Reference: "order_" + benzersizKod(),
				Amount: 2500, CurrencyCode: "TRY",
			})

			require.Error(t, err, "yayına alınmamış promosyon kullanılamaz")
			assert.Equal(t, errors.KindConflict, errors.KindOf(err))
			assert.Equal(t, repository.CodePromotionNotActive, errors.CodeOf(err))

			guncel, err := svc.GetPromotion(ctx, promo.ID)
			require.NoError(t, err)
			assert.Zero(t, guncel.UsageCount, "reddedilen kullanım sayacı artırmaz")

			guncelKampanya, err := svc.GetCampaign(ctx, kampanya.ID)
			require.NoError(t, err)
			assert.Zero(t, guncelKampanya.BudgetUsed,
				"yayına alınmamış promosyon kampanya bütçesini YEMEZ")
		})
	}
}

// TestRedeemKampanyaPenceresiKapaliysaGercekVeritabanindaReddeder kullanım
// anının kampanyanın penceresinde olmasını GERÇEK Postgres üzerinde doğrular.
//
// Denetim kampanya satırı kilitliyken yapılır: pencere ile bütçe sayacı aynı
// anın kaydı olmalıdır.
func TestRedeemKampanyaPenceresiKapaliysaGercekVeritabanindaReddeder(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	simdi := time.Now().UTC()

	kampanya, err := svc.CreateCampaign(ctx, service.CampaignInput{
		Name:               "Bitmiş",
		CampaignIdentifier: "PENCERE-" + benzersizKod(),
		StartsAt:           ptr(simdi.Add(-48 * time.Hour)),
		EndsAt:             ptr(simdi.Add(-24 * time.Hour)),
		BudgetType:         models.BudgetSpend,
		BudgetLimit:        ptr(int64(1_000_000)),
		BudgetCurrencyCode: "TRY",
	})
	require.NoError(t, err)

	promo := aktifPromosyon(ctx, t, svc, service.PromotionInput{CampaignID: &kampanya.ID})

	_, err = svc.RedeemPromotion(ctx, service.RedeemInput{
		PromotionID: promo.ID, Reference: "order_" + benzersizKod(),
		Amount: 2500, CurrencyCode: "TRY",
	})

	require.Error(t, err, "penceresi kapanmış kampanyanın bütçesi yenemez")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, repository.CodeCampaignWindowClosed, errors.CodeOf(err))

	guncel, err := svc.GetCampaign(ctx, kampanya.ID)
	require.NoError(t, err)
	assert.Zero(t, guncel.BudgetUsed)
}

// TestUpdateCampaignButceBirimiKilidiVeritabanindadir kilidin UYGULAMADA değil
// tek bir koşullu UPDATE'te olduğunu doğrular.
//
// Sayaç doldurulduktan sonra bütçe birimini değiştirme denemesi reddedilmeli,
// aynı isteğin birimi KORUYAN hâli ise geçmelidir.
func TestUpdateCampaignButceBirimiKilidiVeritabanindadir(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	kimlik := "KILIT-" + benzersizKod()

	kampanya, err := svc.CreateCampaign(ctx, service.CampaignInput{
		Name:               "Yaz",
		CampaignIdentifier: kimlik,
		BudgetType:         models.BudgetSpend,
		BudgetLimit:        ptr(int64(1_000_000)),
		BudgetCurrencyCode: "TRY",
	})
	require.NoError(t, err)

	promo := aktifPromosyon(ctx, t, svc, service.PromotionInput{CampaignID: &kampanya.ID})
	_, err = svc.RedeemPromotion(ctx, service.RedeemInput{
		PromotionID: promo.ID, Reference: "order_" + benzersizKod(),
		Amount: 30_000, CurrencyCode: "TRY",
	})
	require.NoError(t, err)

	_, err = svc.UpdateCampaign(ctx, kampanya.ID, service.CampaignInput{
		Name:               "Yaz",
		CampaignIdentifier: kimlik,
		BudgetType:         models.BudgetUsage,
		BudgetLimit:        ptr(int64(100)),
	})
	require.Error(t, err, "sayaçtaki 30000 KURUŞ, tür değişince 30000 ADET olarak okunurdu")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, repository.CodeBudgetUnitLocked, errors.CodeOf(err))

	_, err = svc.UpdateCampaign(ctx, kampanya.ID, service.CampaignInput{
		Name:               "Yaz",
		CampaignIdentifier: kimlik,
		BudgetType:         models.BudgetSpend,
		BudgetLimit:        ptr(int64(1_000_000)),
		BudgetCurrencyCode: "USD",
	})
	require.Error(t, err, "önceki TRY harcaması USD sayılırdı")
	assert.Equal(t, repository.CodeBudgetUnitLocked, errors.CodeOf(err))

	guncellenen, err := svc.UpdateCampaign(ctx, kampanya.ID, service.CampaignInput{
		Name:               "Yaz Sonu",
		CampaignIdentifier: kimlik,
		BudgetType:         models.BudgetSpend,
		BudgetLimit:        ptr(int64(2_000_000)),
		BudgetCurrencyCode: "TRY",
	})
	require.NoError(t, err, "birim korunduğu sürece tanım ve SINIR güncellenebilir")
	assert.Equal(t, "Yaz Sonu", guncellenen.Name)
	assert.Equal(t, int64(2_000_000), *guncellenen.BudgetLimit)
	assert.Equal(t, int64(30_000), guncellenen.BudgetUsed, "sayaç bu yoldan değişmez")
}

// TestUpdateCampaignOlmayanKampanyaNotFound kilit denetiminin "bulunamadı"yı
// yutmadığını doğrular: koşullu UPDATE iki sebeple de hiç satır dönmez ve
// ikisinin AYRI hatalar olması gerekir.
func TestUpdateCampaignOlmayanKampanyaNotFound(t *testing.T) {
	ctx := context.Background()

	_, err := yeniServis(t).UpdateCampaign(ctx, models.NewCampaignID(time.Now()), service.CampaignInput{
		Name: "Yok", CampaignIdentifier: "YOK-" + benzersizKod(),
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"var olmayan kampanya, kilit çakışmasıyla AYNI hatayı dönmemeli")
}

// ptr bir değerin işaretçisini döner.
func ptr[T any](v T) *T { return &v }

// lower bir kodu küçük harfe çevirir; kupon kodunun harf durumuna duyarsız
// olduğunu sınayan test bunu kullanır.
func lower(s string) string {
	out := []rune(s)
	for i, r := range out {
		if r >= 'A' && r <= 'Z' {
			out[i] = r + ('a' - 'A')
		}
	}
	return string(out)
}
