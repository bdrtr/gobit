package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

func TestComputeDiscountsJSONMeetsTheSchema(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "YAZ20", IsAutomatic: false},
		percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))
	seedPromotion(repo, models.Promotion{ID: "promo_2", Code: "KARGO", IsAutomatic: true},
		percentageMethod("promo_2", 10000, models.TargetShippingMethods, models.AllocationEach))

	interop := NewInterop(newTestService(repo))
	request := []byte(`{
	  "currency_code": "TRY",
	  "context": {"region_id": "reg_1"},
	  "items": [{"id": "li_1", "amount": 25000, "unit_amount": 12500, "quantity": 2, "attributes": {"kategori": "giyim"}}],
	  "shipping_methods": [{"id": "sm_1", "amount": 4990, "attributes": {}}],
	  "codes": ["yaz20", "HICYOK"],
	  "at": "2026-08-24T10:00:00Z"
	}`)

	payload, err := interop.ComputeDiscountsJSON(context.Background(), request)
	require.NoError(t, err)

	// The schema is the contract the consumer sees; the field names are verified
	// EXACTLY.
	var raw map[string]any
	require.NoError(t, json.Unmarshal(payload, &raw))
	for _, field := range []string{
		"currency_code", "items", "shipping_methods", "items_discount_total",
		"shipping_discount_total", "discount_total", "applied", "unmatched_codes",
	} {
		assert.Contains(t, raw, field, "the %q field has to be in the schema", field)
	}

	var decoded interopResponse
	require.NoError(t, json.Unmarshal(payload, &decoded))

	assert.Equal(t, "TRY", decoded.CurrencyCode)
	require.Len(t, decoded.Items, 1)
	assert.Equal(t, "li_1", decoded.Items[0].ID)
	assert.Equal(t, int64(5000), decoded.Items[0].Amount)
	require.Len(t, decoded.ShippingMethods, 1)
	assert.Equal(t, int64(4990), decoded.ShippingMethods[0].Amount)
	assert.Equal(t, int64(5000), decoded.ItemsDiscountTotal)
	assert.Equal(t, int64(4990), decoded.ShippingDiscountTotal)
	assert.Equal(t, int64(9990), decoded.DiscountTotal)
	assert.Equal(t, []string{"HICYOK"}, decoded.UnmatchedCodes)

	require.Len(t, decoded.Applied, 2)
	assert.Equal(t, "YAZ20", decoded.Applied[0].Code)
	assert.False(t, decoded.Applied[0].IsAutomatic)
	assert.Equal(t, "KARGO", decoded.Applied[1].Code)
	assert.True(t, decoded.Applied[1].IsAutomatic)

	var appliedTotal int64
	for _, applied := range decoded.Applied {
		appliedTotal += applied.Amount
	}
	assert.Equal(t, decoded.DiscountTotal, appliedTotal,
		"the identity the schema declares: Σ applied = discount_total")
}

func TestComputeDiscountsJSONWritesEmptyListsAsArraysNotNull(t *testing.T) {
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
	}`, string(payload), "a uniform surface for the consumer: an empty list is [], not null")
}

func TestComputeDiscountsJSONMalformedBody(t *testing.T) {
	interop := NewInterop(newTestService(newMemRepo()))

	tests := []struct {
		name   string
		body   string
		reason string
	}{
		{name: "empty body", body: "", reason: "an empty request cannot be decoded"},
		{name: "malformed JSON", body: `{`, reason: "incomplete JSON cannot be decoded"},
		{
			name:   "unknown field",
			body:   `{"currency_code": "TRY", "unknown": 1}`,
			reason: "a silently ignored field means what was sent was never processed",
		},
		{
			name:   "malformed timestamp",
			body:   `{"currency_code": "TRY", "at": "yesterday"}`,
			reason: "a malformed timestamp must not silently fall back to 'now'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := interop.ComputeDiscountsJSON(context.Background(), []byte(tt.body))
			require.Error(t, err, tt.reason)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err), tt.reason)
			assert.Equal(t, CodeInteropRequestInvalid, errors.CodeOf(err))
		})
	}
}

func TestComputeDiscountsJSONDoesNotCorruptLargeIntegers(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "YUZDE1", IsAutomatic: true},
		percentageMethod("promo_1", 100, models.TargetItems, models.AllocationEach))

	interop := NewInterop(newTestService(repo))
	// float64 carries integers losslessly only up to 2^53; the amount here is
	// above that, and had it passed through a float on its way from JSON it
	// would have been corrupted at the minor-unit level.
	request := []byte(`{"currency_code":"TRY","items":[{"id":"li_1","amount":999999999999,"unit_amount":999999999999,"quantity":1}]}`)

	payload, err := interop.ComputeDiscountsJSON(context.Background(), request)
	require.NoError(t, err)

	var decoded interopResponse
	require.NoError(t, json.Unmarshal(payload, &decoded))
	assert.Equal(t, int64(9_999_999_999), decoded.DiscountTotal,
		"1%% × 999999999999 = 9999999999 (rounded down); a float would corrupt this value")
}

func TestComputeDiscountsJSONTimestampSelectsTheCampaignWindow(t *testing.T) {
	repo := newMemRepo()
	repo.campaigns["camp_1"] = models.Campaign{
		ID: "camp_1", Name: "Yaz", CampaignIdentifier: "YAZ", BudgetType: models.BudgetNone,
		StartsAt: ptr(testNow.Add(-48 * time.Hour)), EndsAt: ptr(testNow.Add(-time.Hour)),
	}
	seedPromotion(repo, models.Promotion{
		ID: "promo_1", Code: "GECMIS", IsAutomatic: true, CampaignID: ptr("camp_1"),
	}, percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))

	interop := NewInterop(newTestService(repo))
	item := `"currency_code":"TRY","items":[{"id":"li_1","amount":10000,"unit_amount":10000,"quantity":1}]`

	now, err := interop.ComputeDiscountsJSON(context.Background(), []byte("{"+item+"}"))
	require.NoError(t, err)
	assert.Contains(t, string(now), `"discount_total":0`,
		"no discount today, because the campaign window has closed")

	pastMoment := testNow.Add(-24 * time.Hour).Format(time.RFC3339)
	past, err := interop.ComputeDiscountsJSON(context.Background(),
		[]byte(fmt.Sprintf("{%s,\"at\":%q}", item, pastMoment)))
	require.NoError(t, err)
	assert.Contains(t, string(past), `"discount_total":2000`,
		"when a past moment is given, the campaign window is evaluated against THAT moment")
}

func TestInteropRedeemAndReleaseWorkThroughThePrimitiveSurface(t *testing.T) {
	repo := repoWithCoupon(nil, nil)
	interop := NewInterop(newTestService(repo))

	id, err := interop.RedeemPromotion(context.Background(), "", "yaz20", "order_1", "TRY", 2500)
	require.NoError(t, err)
	assert.NotEmpty(t, id)
	assert.Equal(t, int64(1), repo.promotions["promo_1"].UsageCount)

	secondID, err := interop.RedeemPromotion(context.Background(), "", "yaz20", "order_1", "TRY", 2500)
	require.NoError(t, err)
	assert.Equal(t, id, secondID, "the primitive surface is idempotent too")
	assert.Equal(t, int64(1), repo.promotions["promo_1"].UsageCount)

	released, err := interop.ReleasePromotion(context.Background(), "promo_1", "", "order_1")
	require.NoError(t, err)
	assert.True(t, released)

	released, err = interop.ReleasePromotion(context.Background(), "promo_1", "", "order_1")
	require.NoError(t, err, "the compensation can be rerun")
	assert.False(t, released)
	assert.Zero(t, repo.promotions["promo_1"].UsageCount)
}
