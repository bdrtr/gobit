package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

func TestComputeDiscountsJSONSemayiKarsilar(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "YAZ20", IsAutomatic: false},
		percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))
	seedPromotion(repo, models.Promotion{ID: "promo_2", Code: "KARGO", IsAutomatic: true},
		percentageMethod("promo_2", 10000, models.TargetShippingMethods, models.AllocationEach))

	interop := NewInterop(newTestService(repo))
	istek := []byte(`{
	  "currency_code": "TRY",
	  "context": {"region_id": "reg_1"},
	  "items": [{"id": "li_1", "amount": 25000, "quantity": 2, "attributes": {"kategori": "giyim"}}],
	  "shipping_methods": [{"id": "sm_1", "amount": 4990, "attributes": {}}],
	  "codes": ["yaz20", "HICYOK"],
	  "at": "2026-08-24T10:00:00Z"
	}`)

	payload, err := interop.ComputeDiscountsJSON(context.Background(), istek)
	require.NoError(t, err)

	// Şema, tüketicinin gördüğü sözleşmedir; alan adları BİREBİR doğrulanır.
	var gelen map[string]any
	require.NoError(t, json.Unmarshal(payload, &gelen))
	for _, alan := range []string{
		"currency_code", "items", "shipping_methods", "items_discount_total",
		"shipping_discount_total", "discount_total", "applied", "unmatched_codes",
	} {
		assert.Contains(t, gelen, alan, "%q alanı şemada olmalı", alan)
	}

	var cozulen interopResponse
	require.NoError(t, json.Unmarshal(payload, &cozulen))

	assert.Equal(t, "TRY", cozulen.CurrencyCode)
	require.Len(t, cozulen.Items, 1)
	assert.Equal(t, "li_1", cozulen.Items[0].ID)
	assert.Equal(t, int64(5000), cozulen.Items[0].Amount)
	require.Len(t, cozulen.ShippingMethods, 1)
	assert.Equal(t, int64(4990), cozulen.ShippingMethods[0].Amount)
	assert.Equal(t, int64(5000), cozulen.ItemsDiscountTotal)
	assert.Equal(t, int64(4990), cozulen.ShippingDiscountTotal)
	assert.Equal(t, int64(9990), cozulen.DiscountTotal)
	assert.Equal(t, []string{"HICYOK"}, cozulen.UnmatchedCodes)

	require.Len(t, cozulen.Applied, 2)
	assert.Equal(t, "YAZ20", cozulen.Applied[0].Code)
	assert.False(t, cozulen.Applied[0].IsAutomatic)
	assert.Equal(t, "KARGO", cozulen.Applied[1].Code)
	assert.True(t, cozulen.Applied[1].IsAutomatic)

	var appliedToplam int64
	for _, uygulanan := range cozulen.Applied {
		appliedToplam += uygulanan.Amount
	}
	assert.Equal(t, cozulen.DiscountTotal, appliedToplam,
		"şemanın beyan ettiği kimlik: Σ applied = discount_total")
}

func TestComputeDiscountsJSONBosListeleriNullDegilDiziYazar(t *testing.T) {
	interop := NewInterop(newTestService(newMemRepo()))

	payload, err := interop.ComputeDiscountsJSON(context.Background(),
		[]byte(`{"currency_code": "TRY"}`))
	require.NoError(t, err)

	assert.JSONEq(t, `{
	  "currency_code": "TRY",
	  "items": [],
	  "shipping_methods": [],
	  "items_discount_total": 0,
	  "shipping_discount_total": 0,
	  "discount_total": 0,
	  "applied": [],
	  "unmatched_codes": []
	}`, string(payload), "tüketici için tek biçimli bir yüzey: boş liste null değil []'dir")
}

func TestComputeDiscountsJSONBozukGovde(t *testing.T) {
	interop := NewInterop(newTestService(newMemRepo()))

	testler := []struct {
		ad      string
		govde   string
		gerekce string
	}{
		{ad: "boş gövde", govde: "", gerekce: "boş istek çözülemez"},
		{ad: "bozuk JSON", govde: `{`, gerekce: "eksik JSON çözülemez"},
		{
			ad:      "bilinmeyen alan",
			govde:   `{"currency_code": "TRY", "bilinmeyen": 1}`,
			gerekce: "sessizce yok sayılan bir alan, gönderilenin hiç işlenmemesi demektir",
		},
		{
			ad:      "bozuk zaman damgası",
			govde:   `{"currency_code": "TRY", "at": "dün"}`,
			gerekce: "bozuk damga sessizce 'şimdi'ye düşmemeli",
		},
	}

	for _, tt := range testler {
		t.Run(tt.ad, func(t *testing.T) {
			_, err := interop.ComputeDiscountsJSON(context.Background(), []byte(tt.govde))
			require.Error(t, err, tt.gerekce)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err), tt.gerekce)
			assert.Equal(t, CodeInteropRequestInvalid, errors.CodeOf(err))
		})
	}
}

func TestComputeDiscountsJSONBuyukTamSayilariBozmaz(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "YUZDE1", IsAutomatic: true},
		percentageMethod("promo_1", 100, models.TargetItems, models.AllocationEach))

	interop := NewInterop(newTestService(repo))
	// float64 yalnızca 2^53'e kadar tam sayıyı kayıpsız taşır; buradaki tutar
	// onun üzerindedir ve JSON'dan float'a uğrasaydı kuruş düzeyinde bozulurdu.
	istek := []byte(`{"currency_code":"TRY","items":[{"id":"li_1","amount":999999999999,"quantity":1}]}`)

	payload, err := interop.ComputeDiscountsJSON(context.Background(), istek)
	require.NoError(t, err)

	var cozulen interopResponse
	require.NoError(t, json.Unmarshal(payload, &cozulen))
	assert.Equal(t, int64(9_999_999_999), cozulen.DiscountTotal,
		"%%1 × 999999999999 = 9999999999 (aşağı yuvarlanmış); float bu değeri bozardı")
}

func TestComputeDiscountsJSONZamanDamgasiKampanyaPenceresiniSecer(t *testing.T) {
	repo := newMemRepo()
	repo.campaigns["camp_1"] = models.Campaign{
		ID: "camp_1", Name: "Yaz", CampaignIdentifier: "YAZ", BudgetType: models.BudgetNone,
		StartsAt: ptr(testNow.Add(-48 * time.Hour)), EndsAt: ptr(testNow.Add(-time.Hour)),
	}
	seedPromotion(repo, models.Promotion{
		ID: "promo_1", Code: "GECMIS", IsAutomatic: true, CampaignID: ptr("camp_1"),
	}, percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))

	interop := NewInterop(newTestService(repo))
	kalem := `"currency_code":"TRY","items":[{"id":"li_1","amount":10000,"quantity":1}]`

	simdi, err := interop.ComputeDiscountsJSON(context.Background(), []byte("{"+kalem+"}"))
	require.NoError(t, err)
	assert.Contains(t, string(simdi), `"discount_total":0`,
		"kampanya penceresi kapandığı için bugün indirim yok")

	gecmisAn := testNow.Add(-24 * time.Hour).Format(time.RFC3339)
	gecmis, err := interop.ComputeDiscountsJSON(context.Background(),
		[]byte(fmt.Sprintf("{%s,\"at\":%q}", kalem, gecmisAn)))
	require.NoError(t, err)
	assert.Contains(t, string(gecmis), `"discount_total":2000`,
		"geçmiş bir an verildiğinde kampanya penceresi O ANA göre değerlendirilir")
}

func TestInteropRedeemVeReleaseIlkelYuzeydenCalisir(t *testing.T) {
	repo := kuponluDepo(nil, nil)
	interop := NewInterop(newTestService(repo))

	id, err := interop.RedeemPromotion(context.Background(), "", "yaz20", "order_1", "TRY", 2500)
	require.NoError(t, err)
	assert.NotEmpty(t, id)
	assert.Equal(t, int64(1), repo.promotions["promo_1"].UsageCount)

	ikinciID, err := interop.RedeemPromotion(context.Background(), "", "yaz20", "order_1", "TRY", 2500)
	require.NoError(t, err)
	assert.Equal(t, id, ikinciID, "ilkel yüzey de idempotenttir")
	assert.Equal(t, int64(1), repo.promotions["promo_1"].UsageCount)

	released, err := interop.ReleasePromotion(context.Background(), "promo_1", "", "order_1")
	require.NoError(t, err)
	assert.True(t, released)

	released, err = interop.ReleasePromotion(context.Background(), "promo_1", "", "order_1")
	require.NoError(t, err, "telafi tekrar çalıştırılabilir")
	assert.False(t, released)
	assert.Zero(t, repo.promotions["promo_1"].UsageCount)
}
