package service

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// testNow testlerin sabit saatidir; kampanya penceresine bağlı dallar ancak
// belirlenimci bir saatle sınanabilir.
var testNow = time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

// newTestService bellek deposu üzerinde çalışan, saati sabitlenmiş bir servis
// kurar.
func newTestService(repo *memRepo) *Service {
	return New(repo, Options{
		Logger: slog.New(slog.DiscardHandler),
		Now:    func() time.Time { return testNow },
	})
}

// ptr bir değerin işaretçisini döner; isteğe bağlı alanları kısa yazmak için.
func ptr[T any](v T) *T { return &v }

// percentageMethod yüzde indirimli bir uygulama yöntemi üretir.
func percentageMethod(promotionID string, bps int64, target models.ApplicationTargetType, alloc models.Allocation) *models.ApplicationMethod {
	return &models.ApplicationMethod{
		ID:          "appm_" + promotionID,
		PromotionID: promotionID,
		Type:        models.MethodPercentage,
		TargetType:  target,
		Allocation:  alloc,
		Value:       bps,
	}
}

// fixedMethod sabit tutarlı bir uygulama yöntemi üretir.
func fixedMethod(promotionID string, amount int64, target models.ApplicationTargetType, alloc models.Allocation) *models.ApplicationMethod {
	return &models.ApplicationMethod{
		ID:           "appm_" + promotionID,
		PromotionID:  promotionID,
		Type:         models.MethodFixed,
		TargetType:   target,
		Allocation:   alloc,
		Value:        amount,
		CurrencyCode: "TRY",
	}
}

// seedPromotion depoya bir promosyonu yöntemi ve kurallarıyla birlikte yazar.
func seedPromotion(
	repo *memRepo,
	promo models.Promotion,
	method *models.ApplicationMethod,
	rules ...models.PromotionRule,
) {
	if promo.Status == "" {
		promo.Status = models.PromotionActive
	}
	if promo.Type == "" {
		promo.Type = models.PromotionStandard
	}
	repo.promotions[promo.ID] = promo
	if method != nil {
		repo.methods[promo.ID] = *method
	}
	if len(rules) > 0 {
		repo.rules[promo.ID] = rules
	}
}

// item hesap girdisine giren bir kalem üretir.
func item(id string, amount, quantity int64, attrs map[string]string) ComputeItem {
	return ComputeItem{ID: id, Amount: amount, Quantity: quantity, Attributes: attrs}
}

// assertInvariants sonucun DEĞİŞMEZLERİNİ doğrular.
//
// Bu yardımcı neredeyse her testte çağrılır ve modülün en kritik iddialarını
// tek yerde toplar: satır sınırı, toplam kimliği ve Σ satır = Σ promosyon
// eşitliği. Hesabın herhangi bir dalında yapılan bir hata, dalın kendi
// iddiasını geçse bile buraya takılır.
func assertInvariants(t *testing.T, in ComputeInput, res ComputeResult) {
	t.Helper()

	require.Len(t, res.Items, len(in.Items), "her kalem için bir sonuç kaydı olmalı")
	require.Len(t, res.ShippingMethods, len(in.ShippingMethods), "her kargo yöntemi için bir sonuç kaydı olmalı")

	var itemsTotal int64
	for i := range in.Items {
		assert.Equal(t, in.Items[i].ID, res.Items[i].ID, "sonuç girdiyle aynı sırada olmalı")
		assert.GreaterOrEqual(t, res.Items[i].Amount, int64(0), "indirim negatif olamaz")
		assert.LessOrEqual(t, res.Items[i].Amount, in.Items[i].Amount,
			"%s kaleminin indirimi tutarını aşamaz", in.Items[i].ID)
		itemsTotal += res.Items[i].Amount
	}
	var shippingTotal int64
	for i := range in.ShippingMethods {
		assert.Equal(t, in.ShippingMethods[i].ID, res.ShippingMethods[i].ID)
		assert.LessOrEqual(t, res.ShippingMethods[i].Amount, in.ShippingMethods[i].Amount,
			"%s kargo yönteminin indirimi tutarını aşamaz", in.ShippingMethods[i].ID)
		shippingTotal += res.ShippingMethods[i].Amount
	}

	assert.Equal(t, itemsTotal, res.ItemsDiscountTotal, "Σ kalem indirimi kalem toplamıyla birebir tutmalı")
	assert.Equal(t, shippingTotal, res.ShippingDiscountTotal, "Σ kargo indirimi kargo toplamıyla birebir tutmalı")
	assert.Equal(t, itemsTotal+shippingTotal, res.DiscountTotal, "toplam indirim iki bileşenin toplamıdır")

	var appliedTotal int64
	for i := range res.Applied {
		assert.Positive(t, res.Applied[i].Amount, "sıfır indirim uygulanmış sayılmaz")
		appliedTotal += res.Applied[i].Amount
	}
	assert.Equal(t, res.DiscountTotal, appliedTotal,
		"promosyon başına uygulanan tutarların toplamı, toplam indirimle birebir tutmalı")
}

func TestComputeDiscountsYuzdeEachAsagiYuvarlar(t *testing.T) {
	repo := newMemRepo()
	// %20 → 999 * 2000 / 10000 = 199.8 → 199 (AŞAĞI).
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "YUZDE20", IsAutomatic: true},
		percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_1", 999, 1, nil)},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(199), res.Items[0].Amount,
		"yüzde indirim aşağı yuvarlanmalı (199.8 → 199); yukarı yuvarlama vaat edilen oranı aşardı")
	assert.Equal(t, int64(199), res.DiscountTotal)
}

func TestComputeDiscountsSabitTutarEachAdedeUygulanir(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "SABIT10", IsAutomatic: true},
		fixedMethod("promo_1", 1000, models.TargetItems, models.AllocationEach))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			item("li_1", 5000, 3, nil), // 3 birim × 1000 = 3000
			item("li_2", 2000, 1, nil), // 1 birim × 1000 = 1000
		},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(3000), res.Items[0].Amount, "sabit tutar her BİRİME uygulanır")
	assert.Equal(t, int64(1000), res.Items[1].Amount)
	assert.Equal(t, int64(4000), res.DiscountTotal)
}

func TestComputeDiscountsSabitTutarEachMaxQuantityIleSinirlanir(t *testing.T) {
	repo := newMemRepo()
	method := fixedMethod("promo_1", 1000, models.TargetItems, models.AllocationEach)
	method.MaxQuantity = ptr(int64(2))
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "SABIT10", IsAutomatic: true}, method)

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_1", 50000, 5, nil)},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(2000), res.Items[0].Amount,
		"azami adet 2 ise indirim yalnızca iki birime uygulanır")
}

func TestComputeDiscountsYuzdeEachMaxQuantityYokSayar(t *testing.T) {
	repo := newMemRepo()
	method := percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationEach)
	method.MaxQuantity = ptr(int64(1))
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "YARIM", IsAutomatic: true}, method)

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_1", 10000, 5, nil)},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(5000), res.Items[0].Amount,
		"yüzde indirimde azami adet yok sayılır; taban satırın TUTARIDIR")
}

func TestComputeDiscountsAcrossKurusArtigiDagitilirVeToplamBirebirTutar(t *testing.T) {
	repo := newMemRepo()
	// 100 birimlik sabit indirim üç eşit satıra dağıtılır: 33 + 33 + 33 = 99,
	// artan 1 kuruş kesirli kalanı eşit olanlar arasında KİMLİĞİ EN KÜÇÜK olana
	// gider.
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "SABIT100", IsAutomatic: true},
		fixedMethod("promo_1", 100, models.TargetItems, models.AllocationAcross))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			item("li_a", 1000, 1, nil),
			item("li_b", 1000, 1, nil),
			item("li_c", 1000, 1, nil),
		},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(100), res.DiscountTotal, "Σ satır indirimi dağıtılan toplamla BİREBİR tutmalı")
	assert.Equal(t, int64(34), res.Items[0].Amount, "kuruş artığı kimliği en küçük satıra gider")
	assert.Equal(t, int64(33), res.Items[1].Amount)
	assert.Equal(t, int64(33), res.Items[2].Amount)
}

func TestComputeDiscountsAcrossTahsisiGirdiSirasindanBagimsizdir(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "SABIT100", IsAutomatic: true},
		fixedMethod("promo_1", 100, models.TargetItems, models.AllocationAcross))
	svc := newTestService(repo)

	duz := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			item("li_a", 1000, 1, nil),
			item("li_b", 1000, 1, nil),
			item("li_c", 1000, 1, nil),
		},
	}
	ters := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			item("li_c", 1000, 1, nil),
			item("li_b", 1000, 1, nil),
			item("li_a", 1000, 1, nil),
		},
	}

	ileri, err := svc.ComputeDiscounts(context.Background(), duz)
	require.NoError(t, err)
	geri, err := svc.ComputeDiscounts(context.Background(), ters)
	require.NoError(t, err)

	byID := map[string]int64{}
	for _, line := range geri.Items {
		byID[line.ID] = line.Amount
	}
	for _, line := range ileri.Items {
		assert.Equal(t, line.Amount, byID[line.ID],
			"%s satırının kuruşu, satırların GELİŞ SIRASINDAN bağımsız olmalı", line.ID)
	}
}

func TestComputeDiscountsAcrossYuzdeTekSeferYuvarlar(t *testing.T) {
	repo := newMemRepo()
	// Taban 3 × 333 = 999; %20 → 199 (tek seferde). Satır başına hesaplansaydı
	// her satır 66 alır ve toplam 198 olurdu — bir kuruş kayıp.
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "YUZDE20", IsAutomatic: true},
		percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationAcross))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			item("li_a", 333, 1, nil),
			item("li_b", 333, 1, nil),
			item("li_c", 333, 1, nil),
		},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(199), res.DiscountTotal,
		"across yüzdesi TOPLAM üzerinden bir kez yuvarlanır (199), satır başına değil (198)")
}

func TestComputeDiscountsSiparisHedefiTumKalemlereDagitilirVeHedefKuraliniYokSayar(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo,
		models.Promotion{ID: "promo_1", Code: "SIPARIS10", IsAutomatic: true},
		percentageMethod("promo_1", 1000, models.TargetOrder, models.AllocationAcross),
		models.PromotionRule{
			ID: "prule_1", PromotionID: "promo_1", RuleType: models.RuleTarget,
			Attribute: "kategori", Operator: models.OpEq, Values: []string{"elektronik"},
		},
	)

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			item("li_a", 6000, 1, map[string]string{"kategori": "elektronik"}),
			item("li_b", 4000, 1, map[string]string{"kategori": "giyim"}),
		},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(1000), res.DiscountTotal, "%10 × 10000 = 1000")
	assert.Equal(t, int64(600), res.Items[0].Amount)
	assert.Equal(t, int64(400), res.Items[1].Amount,
		"sipariş hedefi hedef kuralını yok sayar; indirim TÜM kalemlere dağıtılır")
}

func TestComputeDiscountsHedefKuraliKalemleriSuzer(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo,
		models.Promotion{ID: "promo_1", Code: "ELEKTRONIK", IsAutomatic: true},
		percentageMethod("promo_1", 1000, models.TargetItems, models.AllocationEach),
		models.PromotionRule{
			ID: "prule_1", PromotionID: "promo_1", RuleType: models.RuleTarget,
			Attribute: "kategori", Operator: models.OpEq, Values: []string{"elektronik"},
		},
	)

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			item("li_a", 6000, 1, map[string]string{"kategori": "elektronik"}),
			item("li_b", 4000, 1, map[string]string{"kategori": "giyim"}),
			item("li_c", 3000, 1, nil),
		},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(600), res.Items[0].Amount)
	assert.Zero(t, res.Items[1].Amount, "kuralı sağlamayan kalem indirim almaz")
	assert.Zero(t, res.Items[2].Amount, "özniteliği olmayan kalem kuralı SAĞLAMAZ")
}

func TestComputeDiscountsKargoHedefi(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "KARGOBEDAVA", IsAutomatic: true},
		percentageMethod("promo_1", 10000, models.TargetShippingMethods, models.AllocationEach))

	in := ComputeInput{
		CurrencyCode:    "TRY",
		Items:           []ComputeItem{item("li_a", 10000, 1, nil)},
		ShippingMethods: []ComputeShippingMethod{{ID: "sm_1", Amount: 4990}},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Zero(t, res.Items[0].Amount, "kargo indirimi kalemlere dokunmaz")
	assert.Equal(t, int64(4990), res.ShippingMethods[0].Amount)
	assert.Equal(t, int64(4990), res.ShippingDiscountTotal)
}

func TestComputeDiscountsYuzdelerBirbirineBinmez(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "ON", IsAutomatic: true},
		percentageMethod("promo_1", 1000, models.TargetItems, models.AllocationEach))
	seedPromotion(repo, models.Promotion{ID: "promo_2", Code: "YIRMI", IsAutomatic: true},
		percentageMethod("promo_2", 2000, models.TargetItems, models.AllocationEach))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(3000), res.DiscountTotal,
		"%10 + %20 ORİJİNAL tutar üzerinden 3000 eder; bileşik hesap 2800 verirdi")
	require.Len(t, res.Applied, 2)
	assert.Equal(t, int64(1000), res.Applied[0].Amount)
	assert.Equal(t, int64(2000), res.Applied[1].Amount)
}

func TestComputeDiscountsKuponlarOtomatiklerdenONCEUygulanir(t *testing.T) {
	repo := newMemRepo()
	// İkisi birlikte satırın tutarını aşar; kırpılan SONUNCU olmalıdır ve
	// sonuncu, otomatik promosyondur.
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "OTOMATIK", IsAutomatic: true},
		percentageMethod("promo_1", 8000, models.TargetItems, models.AllocationEach))
	seedPromotion(repo, models.Promotion{ID: "promo_2", Code: "KUPON", IsAutomatic: false},
		percentageMethod("promo_2", 8000, models.TargetItems, models.AllocationEach))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
		Codes:        []string{"KUPON"},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(10000), res.DiscountTotal, "toplam indirim satır tutarını aşamaz")
	require.Len(t, res.Applied, 2)
	assert.Equal(t, "KUPON", res.Applied[0].Code, "kupon önce uygulanır")
	assert.Equal(t, int64(8000), res.Applied[0].Amount, "müşterinin yazdığı kupon TAM uygulanır")
	assert.Equal(t, "OTOMATIK", res.Applied[1].Code)
	assert.Equal(t, int64(2000), res.Applied[1].Amount, "kırpılan, sonra gelen otomatik promosyondur")
}

func TestComputeDiscountsAyniGruptaKimlikSirasiyleUygulanir(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_a", Code: "ILK", IsAutomatic: true},
		percentageMethod("promo_a", 8000, models.TargetItems, models.AllocationEach))
	seedPromotion(repo, models.Promotion{ID: "promo_b", Code: "IKINCI", IsAutomatic: true},
		percentageMethod("promo_b", 8000, models.TargetItems, models.AllocationEach))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	require.Len(t, res.Applied, 2)
	assert.Equal(t, "promo_a", res.Applied[0].PromotionID, "aynı grupta kimliği küçük olan önce uygulanır")
	assert.Equal(t, int64(8000), res.Applied[0].Amount)
	assert.Equal(t, int64(2000), res.Applied[1].Amount)
}

func TestComputeDiscountsIndirimSatirTutariniAsamaz(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "COKBUYUK", IsAutomatic: true},
		fixedMethod("promo_1", 999_999, models.TargetItems, models.AllocationEach))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_1", 500, 1, nil)},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(500), res.Items[0].Amount, "indirim satırın tutarında durur")
	assert.Equal(t, int64(500), res.Applied[0].Amount,
		"promosyona yazılan tutar, KIRPILDIKTAN sonraki gerçek tutardır")
}

func TestComputeDiscountsSifirTutarliKalemIndirimAlmaz(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "SABIT100", IsAutomatic: true},
		fixedMethod("promo_1", 100, models.TargetItems, models.AllocationAcross))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			item("li_a", 0, 1, nil),
			item("li_b", 1000, 1, nil),
		},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Zero(t, res.Items[0].Amount, "sıfır tutarlı kaleme kuruş yazılmaz")
	assert.Equal(t, int64(100), res.Items[1].Amount)
}

func TestComputeDiscountsTumKalemlerSifirsaIndirimYok(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "YUZDE50", IsAutomatic: true},
		percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationAcross))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_a", 0, 1, nil)},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Zero(t, res.DiscountTotal)
	assert.Empty(t, res.Applied, "hiç indirim üretmeyen promosyon uygulanmış sayılmaz")
}

func TestComputeDiscountsKalemsizSepetHataVermez(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "YUZDE50", IsAutomatic: true},
		percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationEach))

	in := ComputeInput{CurrencyCode: "TRY"}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Zero(t, res.DiscountTotal)
	assert.Empty(t, res.Items)
}

func TestComputeDiscountsBuyukTutarlarTasmaz(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "YUZDE100", IsAutomatic: true},
		percentageMethod("promo_1", 10000, models.TargetItems, models.AllocationAcross))

	// İki satır da azami tutarın yarısıdır; taban tam [models.MaxAmount] olur ve
	// %100 indirim 10^12 × 10^4 / 10^4 ara çarpımını gerektirir.
	yarim := models.MaxAmount / 2
	in := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			item("li_a", yarim, 1, nil),
			item("li_b", yarim, 1, nil),
		},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, models.MaxAmount, res.DiscountTotal, "azami tutarda bile hesap taşmadan tamamlanır")
	assert.Equal(t, yarim, res.Items[0].Amount)
	assert.Equal(t, yarim, res.Items[1].Amount)
}

func TestComputeDiscountsAraToplamSinirasiAsilirsaReddedilir(t *testing.T) {
	repo := newMemRepo()
	in := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			item("li_a", models.MaxAmount, 1, nil),
			item("li_b", 1, 1, nil),
		},
	}
	_, err := newTestService(repo).ComputeDiscounts(context.Background(), in)

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err),
		"ara toplamın azami tutarı aşması, taşma korumasının sınırıdır ve reddedilir")
}

func TestComputeDiscountsElemeDallari(t *testing.T) {
	gecmisPencere := models.Campaign{
		ID: "camp_gecmis", Name: "Geçmiş", CampaignIdentifier: "GECMIS",
		BudgetType: models.BudgetNone,
		EndsAt:     ptr(testNow.Add(-time.Hour)),
	}
	tukenmisButce := models.Campaign{
		ID: "camp_tukenmis", Name: "Tükenmiş", CampaignIdentifier: "TUKENMIS",
		BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(1000)),
		BudgetUsed: 1000, BudgetCurrencyCode: "TRY",
	}

	testler := []struct {
		ad      string
		hazirla func(repo *memRepo)
		gerekce string
	}{
		{
			ad: "taslak promosyon",
			hazirla: func(repo *memRepo) {
				seedPromotion(repo, models.Promotion{
					ID: "promo_1", Code: "TASLAK", IsAutomatic: true, Status: models.PromotionDraft,
				}, percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationEach))
			},
			gerekce: "taslak promosyon indirim üretmez",
		},
		{
			ad: "pasif promosyon",
			hazirla: func(repo *memRepo) {
				seedPromotion(repo, models.Promotion{
					ID: "promo_1", Code: "PASIF", IsAutomatic: true, Status: models.PromotionInactive,
				}, percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationEach))
			},
			gerekce: "pasif promosyon indirim üretmez",
		},
		{
			ad: "uygulama yöntemi yok",
			hazirla: func(repo *memRepo) {
				seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "YONTEMSIZ", IsAutomatic: true}, nil)
			},
			gerekce: "yöntemsiz promosyon indirimin NASIL uygulanacağını söylemez",
		},
		{
			ad: "buyget türü",
			hazirla: func(repo *memRepo) {
				seedPromotion(repo, models.Promotion{
					ID: "promo_1", Code: "BUYGET", IsAutomatic: true, Type: models.PromotionBuyGet,
				}, percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationEach))
			},
			gerekce: "buyget mekaniği bu fazda yok; hesap onu atlar",
		},
		{
			ad: "kullanım hakkı bitmiş",
			hazirla: func(repo *memRepo) {
				seedPromotion(repo, models.Promotion{
					ID: "promo_1", Code: "BITMIS", IsAutomatic: true,
					UsageLimit: ptr(int64(2)), UsageCount: 2,
				}, percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationEach))
			},
			gerekce: "kullanım hakkı biten kupon uygulanmaz",
		},
		{
			ad: "kampanya penceresi kapalı",
			hazirla: func(repo *memRepo) {
				repo.campaigns[gecmisPencere.ID] = gecmisPencere
				seedPromotion(repo, models.Promotion{
					ID: "promo_1", Code: "GECMIS", IsAutomatic: true, CampaignID: ptr(gecmisPencere.ID),
				}, percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationEach))
			},
			gerekce: "penceresi kapanmış kampanyanın promosyonu uygulanmaz",
		},
		{
			ad: "kampanya bütçesi tükenmiş",
			hazirla: func(repo *memRepo) {
				repo.campaigns[tukenmisButce.ID] = tukenmisButce
				seedPromotion(repo, models.Promotion{
					ID: "promo_1", Code: "TUKENMIS", IsAutomatic: true, CampaignID: ptr(tukenmisButce.ID),
				}, percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationEach))
			},
			gerekce: "bütçesi tükenmiş kampanya KISMİ de uygulanmaz",
		},
		{
			ad: "kampanya silinmiş",
			hazirla: func(repo *memRepo) {
				seedPromotion(repo, models.Promotion{
					ID: "promo_1", Code: "SAHIPSIZ", IsAutomatic: true, CampaignID: ptr("camp_yok"),
				}, percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationEach))
			},
			gerekce: "kampanyası silinmiş promosyon sınırsız hâle GELMEZ, elenir",
		},
		{
			ad: "sabit indirimde para birimi uyuşmuyor",
			hazirla: func(repo *memRepo) {
				method := fixedMethod("promo_1", 1000, models.TargetItems, models.AllocationEach)
				method.CurrencyCode = "USD"
				seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "DOLAR", IsAutomatic: true}, method)
			},
			gerekce: "kur çevirisi yapılmaz; farklı para birimindeki sabit indirim elenir",
		},
		{
			ad: "bağlam kuralı sağlanmıyor",
			hazirla: func(repo *memRepo) {
				seedPromotion(repo,
					models.Promotion{ID: "promo_1", Code: "VIPONLY", IsAutomatic: true},
					percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationEach),
					models.PromotionRule{
						ID: "prule_1", PromotionID: "promo_1", RuleType: models.RuleContext,
						Attribute: "customer_group_id", Operator: models.OpEq, Values: []string{"vip"},
					},
				)
			},
			gerekce: "bağlamda alan yoksa kural EŞLEŞMEZ ve promosyon elenir",
		},
	}

	for _, tt := range testler {
		t.Run(tt.ad, func(t *testing.T) {
			repo := newMemRepo()
			tt.hazirla(repo)

			in := ComputeInput{
				CurrencyCode: "TRY",
				Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
			}
			res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
			require.NoError(t, err)

			assertInvariants(t, in, res)
			assert.Zero(t, res.DiscountTotal, tt.gerekce)
			assert.Empty(t, res.Applied, tt.gerekce)
		})
	}
}

func TestComputeDiscountsBaglamKuraliSaglaninca(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo,
		models.Promotion{ID: "promo_1", Code: "VIPONLY", IsAutomatic: true},
		percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationEach),
		models.PromotionRule{
			ID: "prule_1", PromotionID: "promo_1", RuleType: models.RuleContext,
			Attribute: "customer_group_id", Operator: models.OpIn, Values: []string{"vip", "b2b"},
		},
	)

	in := ComputeInput{
		CurrencyCode: "TRY",
		Context:      map[string]string{"customer_group_id": "vip"},
		Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(5000), res.DiscountTotal, "bağlam kuralı sağlandığında promosyon uygulanır")
}

func TestComputeDiscountsKuponKoduVerilmezseOtomatikOlmayanUygulanmaz(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "GIZLIKUPON", IsAutomatic: false},
		percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationEach))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Zero(t, res.DiscountTotal, "kodu verilmeyen kupon kendiliğinden uygulanmaz")
}

func TestComputeDiscountsKuponKoduBuyukKucukHarfDuyarsizdir(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "YAZ20", IsAutomatic: false},
		percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
		Codes:        []string{"  yaz20 "},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(2000), res.DiscountTotal, "kupon kodu büyük/küçük harf ve boşluğa duyarsızdır")
	assert.Empty(t, res.UnmatchedCodes)
}

func TestComputeDiscountsAyniKodIkiKezVerilirseBirKezUygulanir(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "YAZ20", IsAutomatic: false},
		percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
		// Aynı kupon iki kez, ve bağlanamayan bir kod da iki kez: ilki indirimin
		// ikiye katlanmadığını, ikincisi tekilleştirmenin YANITTA da geçerli
		// olduğunu sınar.
		Codes: []string{"YAZ20", "yaz20", "HICYOK", "hicyok"},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(2000), res.DiscountTotal, "tekrarlanan kod indirimi ikiye katlamamalı")
	require.Len(t, res.Applied, 1)
	assert.Equal(t, []string{"HICYOK"}, res.UnmatchedCodes,
		"bağlanamayan bir kod, kaç kez verilirse verilsin bir kez bildirilir")
}

func TestComputeDiscountsBaglanamayanKodlarBildirilir(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{
		ID: "promo_1", Code: "TASLAK", IsAutomatic: false, Status: models.PromotionDraft,
	}, percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
		Codes:        []string{"TASLAK", "HICYOK"},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, []string{"TASLAK", "HICYOK"}, res.UnmatchedCodes,
		"taslak promosyon ile var olmayan kod AYNI biçimde bildirilir; ayrım sızıntı olurdu")
}

func TestComputeDiscountsIndirimUretmeyenGecerliKodEslesmisSayilir(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo,
		models.Promotion{ID: "promo_1", Code: "ELEKTRONIK", IsAutomatic: false},
		percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach),
		models.PromotionRule{
			ID: "prule_1", PromotionID: "promo_1", RuleType: models.RuleTarget,
			Attribute: "kategori", Operator: models.OpEq, Values: []string{"elektronik"},
		},
	)

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_1", 10000, 1, map[string]string{"kategori": "giyim"})},
		Codes:        []string{"ELEKTRONIK"},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Zero(t, res.DiscountTotal)
	assert.Empty(t, res.UnmatchedCodes,
		"hedefine uyan kalemi olmayan GEÇERLİ kupon, geçersiz kod değildir")
}

func TestComputeDiscountsGirdiDogrulamasi(t *testing.T) {
	gecerliKalem := item("li_1", 1000, 1, nil)

	testler := []struct {
		ad      string
		in      ComputeInput
		gerekce string
	}{
		{
			ad:      "para birimi yok",
			in:      ComputeInput{Items: []ComputeItem{gecerliKalem}},
			gerekce: "para birimi zorunludur",
		},
		{
			ad: "kalem kimliği tekrar ediyor",
			in: ComputeInput{CurrencyCode: "TRY", Items: []ComputeItem{
				item("li_1", 1000, 1, nil), item("li_1", 2000, 1, nil),
			}},
			gerekce: "aynı kimlik iki kez geçerse hangi satırın hangi indirimi aldığı ayırt edilemez",
		},
		{
			ad:      "negatif tutar",
			in:      ComputeInput{CurrencyCode: "TRY", Items: []ComputeItem{item("li_1", -1, 1, nil)}},
			gerekce: "negatif tutar bir indirim değildir",
		},
		{
			ad:      "sıfır adet",
			in:      ComputeInput{CurrencyCode: "TRY", Items: []ComputeItem{item("li_1", 1000, 0, nil)}},
			gerekce: "adet en az bir olmalı",
		},
		{
			ad:      "geçersiz kupon kodu",
			in:      ComputeInput{CurrencyCode: "TRY", Codes: []string{"a b"}},
			gerekce: "kupon kodu boşluk içeremez",
		},
	}

	for _, tt := range testler {
		t.Run(tt.ad, func(t *testing.T) {
			_, err := newTestService(newMemRepo()).ComputeDiscounts(context.Background(), tt.in)
			require.Error(t, err, tt.gerekce)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err), tt.gerekce)
		})
	}
}

func TestComputeDiscountsGirdiyiDegistirmez(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "YAZ20", IsAutomatic: false},
		percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))

	kodlar := []string{"yaz20"}
	oznitelikler := map[string]string{"kategori": "giyim"}
	in := ComputeInput{
		CurrencyCode: "try",
		Items:        []ComputeItem{item("li_1", 10000, 1, oznitelikler)},
		Codes:        kodlar,
	}
	_, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assert.Equal(t, []string{"yaz20"}, kodlar, "çağıranın kod dilimi değiştirilmemeli")
	assert.Equal(t, "try", in.CurrencyCode, "çağıranın girdisi normalleştirme yüzünden değişmemeli")
	assert.Equal(t, map[string]string{"kategori": "giyim"}, oznitelikler)
}

func TestComputeDiscountsDepoHatasiYukseltilir(t *testing.T) {
	repo := newMemRepo()
	repo.errOn["ListCandidates"] = errors.Unavailable("test_db", "veritabanı yok")

	_, err := newTestService(repo).ComputeDiscounts(context.Background(), ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_1", 1000, 1, nil)},
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err),
		"depo hatası sessizce 'indirim yok'a düşmemeli")
}

func TestComputeDiscountsHicYazmaz(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{
		ID: "promo_1", Code: "YAZ20", IsAutomatic: true, UsageLimit: ptr(int64(5)),
	}, percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))

	svc := newTestService(repo)
	for range 3 {
		_, err := svc.ComputeDiscounts(context.Background(), ComputeInput{
			CurrencyCode: "TRY",
			Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
		})
		require.NoError(t, err)
	}

	assert.Zero(t, repo.promotions["promo_1"].UsageCount,
		"hesap yan etkisizdir; sepete bakmak kuponu HARCAMAZ")
	assert.Zero(t, repo.calls["Redeem"])
}

// TestComputeDiscountsAcrossDoluSatirIkinciPromosyonuYariyaDusurmez "across"
// tahsisinde dağıtılacak toplamın hedeflerin ORİJİNAL tutarına göre
// hesaplandığını pinler (bkz. [acrossTotal]).
//
// Senaryo üst üste binen iki promosyondur: kupon li_1'i tamamen indirir,
// otomatik sipariş promosyonu ise siparişin TAMAMINA %100 uygular. Toplam
// hedeflerin KALANINA kırpılsaydı havuz 1000'e düşer, ama paylar yine orijinal
// tutarlara göre dağıtıldığı için dolu li_1 payının yarısını yiyip kırptırır ve
// li_2'ye vaat edilenin yarısı (500) verilirdi — yani promosyon iki kez
// cezalandırılırdı.
func TestComputeDiscountsAcrossDoluSatirIkinciPromosyonuYariyaDusurmez(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo,
		models.Promotion{ID: "promo_1", Code: "KUPON", IsAutomatic: false},
		percentageMethod("promo_1", 10000, models.TargetItems, models.AllocationEach),
		models.PromotionRule{
			ID: "prule_1", PromotionID: "promo_1", RuleType: models.RuleTarget,
			Attribute: "kategori", Operator: models.OpEq, Values: []string{"a"},
		},
	)
	seedPromotion(repo, models.Promotion{ID: "promo_2", Code: "SIPARIS", IsAutomatic: true},
		percentageMethod("promo_2", 10000, models.TargetOrder, models.AllocationAcross))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			item("li_1", 1000, 1, map[string]string{"kategori": "a"}),
			item("li_2", 1000, 1, map[string]string{"kategori": "b"}),
		},
		Codes: []string{"KUPON"},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(1000), res.Items[0].Amount, "kupon li_1'i tamamen indirir")
	assert.Equal(t, int64(1000), res.Items[1].Amount,
		"dolu satırın payı kırpılıp kaybolur; boş satır yine de TAM payını alır")
	assert.Equal(t, int64(2000), res.DiscountTotal,
		"%100 sipariş indirimi kalana kırpılsaydı toplam 1500 olurdu; yüzdeler bileşik değildir")
	require.Len(t, res.Applied, 2)
	assert.Equal(t, int64(1000), res.Applied[1].Amount,
		"sipariş promosyonuna yazılan tutar, satır sınırına takılan kısım düşüldükten sonrasıdır")
}

// TestComputeDiscountsAcrossSabitTutarTabaniAsamaz kırpmanın hâlâ var olduğunu
// pinler: kırpmayı tamamen kaldıran bir değişiklik hedeflerin tutarından
// fazlasını dağıtmaya çalışırdı.
func TestComputeDiscountsAcrossSabitTutarTabaniAsamaz(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "COKBUYUK", IsAutomatic: true},
		fixedMethod("promo_1", 999_999, models.TargetItems, models.AllocationAcross))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			item("li_a", 300, 1, nil),
			item("li_b", 700, 1, nil),
		},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(1000), res.DiscountTotal, "tahsis dağıttığı tabandan fazlasını dağıtamaz")
	assert.Equal(t, int64(300), res.Items[0].Amount)
	assert.Equal(t, int64(700), res.Items[1].Amount)
}

// TestComputeDiscountsKampanyaButceParaBirimiElenir PARA ölçülü bir kampanya
// bütçesinin sepetinkinden farklı para biriminde olması hâlinde promosyonun
// hesaba HİÇ girmediğini pinler (bkz. [campaignBudgetCurrencyMatches]).
//
// Eleme olmasaydı indirim sepette görünür, [Service.RedeemPromotion] ise aynı
// tutarı campaign_budget_currency_mismatch ile reddederdi.
func TestComputeDiscountsKampanyaButceParaBirimiElenir(t *testing.T) {
	tryButcesi := models.Campaign{
		ID: "camp_try", Name: "Yaz", CampaignIdentifier: "YAZ",
		BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(100_000)), BudgetCurrencyCode: "TRY",
	}
	adetButcesi := models.Campaign{
		ID: "camp_adet", Name: "Adet", CampaignIdentifier: "ADET",
		BudgetType: models.BudgetUsage, BudgetLimit: ptr(int64(10)),
	}

	testler := []struct {
		ad       string
		campaign models.Campaign
		sepet    string
		beklenen int64
		gerekce  string
	}{
		{
			ad: "para birimi uyuşmuyor", campaign: tryButcesi, sepet: "USD", beklenen: 0,
			gerekce: "TRY bütçeli kampanyanın promosyonu USD sepete uygulanamaz",
		},
		{
			ad: "para birimi uyuşuyor", campaign: tryButcesi, sepet: "TRY", beklenen: 2000,
			gerekce: "aynı para biriminde eleme YAPILMAZ",
		},
		{
			ad: "adet ölçülü bütçe para birimine bakmaz", campaign: adetButcesi, sepet: "USD", beklenen: 2000,
			gerekce: "adet sayan bir bütçenin para birimi yoktur; sepetinkiyle karşılaştırılamaz",
		},
	}

	for _, tt := range testler {
		t.Run(tt.ad, func(t *testing.T) {
			repo := newMemRepo()
			repo.campaigns[tt.campaign.ID] = tt.campaign
			seedPromotion(repo, models.Promotion{
				ID: "promo_1", Code: "YUZDE20", IsAutomatic: true, CampaignID: ptr(tt.campaign.ID),
			}, percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))

			in := ComputeInput{
				CurrencyCode: tt.sepet,
				Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
			}
			res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
			require.NoError(t, err)

			assertInvariants(t, in, res)
			assert.Equal(t, tt.beklenen, res.DiscountTotal, tt.gerekce)
		})
	}
}

// TestComputeDiscountsSayisalKuralCevrilemeyenDegerleEslesmez [matchNumeric]
// godoc'undaki "tam sayıya çevrilemeyen bir değer kuralı EŞLEŞMEZ" kararını
// pinler.
//
// Karar güvenlik açısından taşıyıcıdır: aksi hâlde bozuk ya da kötü niyetli tek
// bir bağlam alanı ("total": "abc") eşik kurallarını herkese açardı.
func TestComputeDiscountsSayisalKuralCevrilemeyenDegerleEslesmez(t *testing.T) {
	testler := []struct {
		ad          string
		kuralDegeri string
		baglam      string
		beklenen    int64
		gerekce     string
	}{
		{
			ad: "bağlam değeri sayı değil", kuralDegeri: "5000", baglam: "abc", beklenen: 0,
			gerekce: "çevrilemeyen bağlam değeri kuralı EŞLEŞMEZ; eşiği herkese açmaz",
		},
		{
			ad: "kuralın değeri sayı değil", kuralDegeri: "besbin", baglam: "10000", beklenen: 0,
			gerekce: "çevrilemeyen kural değeri de eşleşmez; okunamayan koşul indirimi açmamalı",
		},
		{
			ad: "iki taraf da sayı", kuralDegeri: "5000", baglam: "10000", beklenen: 5000,
			gerekce: "iki taraf da çevrilebiliyorsa kural normal değerlendirilir",
		},
	}

	for _, tt := range testler {
		t.Run(tt.ad, func(t *testing.T) {
			repo := newMemRepo()
			seedPromotion(repo,
				models.Promotion{ID: "promo_1", Code: "ESIK", IsAutomatic: true},
				percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationEach),
				models.PromotionRule{
					ID: "prule_1", PromotionID: "promo_1", RuleType: models.RuleContext,
					Attribute: "cart_total", Operator: models.OpGte, Values: []string{tt.kuralDegeri},
				},
			)

			in := ComputeInput{
				CurrencyCode: "TRY",
				Context:      map[string]string{"cart_total": tt.baglam},
				Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
			}
			res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
			require.NoError(t, err)

			assertInvariants(t, in, res)
			assert.Equal(t, tt.beklenen, res.DiscountTotal, tt.gerekce)
		})
	}
}
