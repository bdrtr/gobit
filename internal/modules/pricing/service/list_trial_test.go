package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// compareRepo is a price set whose base price is 1000, and 950 from three,
// with a sale list's 700, and 600 from three, in a draft list under trial, an
// active override of another list at 900 that only reaches the "vip" group, and
// an expired sale at 500.
func compareRepo(trialStatus models.PriceListStatus) *stubRepo {
	past := testNow.Add(-48 * time.Hour)
	candidates := []models.PriceCandidate{
		basePrice("price_base", "TRY", 1000, 1, nil),
		basePrice("price_base_three", "TRY", 950, 3, nil),
		withList(basePrice("price_trial", "TRY", 700, 1, nil), "plist_trial",
			&models.PriceListInfo{ID: "plist_trial", Type: models.PriceListSale, Status: trialStatus, EndsAt: &past}),
		withList(basePrice("price_trial_three", "TRY", 600, 3, nil), "plist_trial",
			&models.PriceListInfo{ID: "plist_trial", Type: models.PriceListSale, Status: trialStatus, EndsAt: &past}),
		withRules(withList(basePrice("price_vip", "TRY", 900, 1, nil), "plist_vip",
			&models.PriceListInfo{ID: "plist_vip", Type: models.PriceListOverride, Status: models.PriceListActive}),
			rule("customer_group_id", models.OpEq, "vip")),
		withList(basePrice("price_old", "TRY", 500, 1, nil), "plist_old",
			&models.PriceListInfo{ID: "plist_old", Type: models.PriceListSale, Status: models.PriceListActive, EndsAt: &past}),
	}
	return &stubRepo{
		getPriceListFn: func(_ context.Context, id string) (models.PriceList, error) {
			if id != "plist_trial" {
				return models.PriceList{}, errors.NotFound("pricing_not_found", "no list %s", id)
			}
			return models.PriceList{ID: id, Type: models.PriceListSale, Status: trialStatus}, nil
		},
		listCandidatesBySetsFn: func(_ context.Context, ids []string) (map[string][]models.PriceCandidate, error) {
			out := map[string][]models.PriceCandidate{}
			for _, id := range ids {
				if id == "pset_1" {
					out[id] = candidates
				}
			}
			return out, nil
		},
	}
}

// compare runs one comparison and decodes it.
func compare(t *testing.T, svc *Service, listID string, req compareListRequest) compareListResponse {
	t.Helper()

	payload, err := json.Marshal(req)
	require.NoError(t, err)
	raw, err := svc.CompareListJSON(context.Background(), listID, payload)
	require.NoError(t, err)
	var out compareListResponse
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

// TestAListIsComparedAsIfActiveAndAsIfAbsent is ADR 0220: a draft list whose
// window has closed is offered as active with no window, the baseline leaves it
// out, and every other list is read as it is today — an expired one not at all,
// and one with a rule only for the context that matches it.
func TestAListIsComparedAsIfActiveAndAsIfAbsent(t *testing.T) {
	t.Parallel()

	svc := newTestService(compareRepo(models.PriceListDraft))
	out := compare(t, svc, "plist_trial", compareListRequest{Entries: []compareListEntry{
		{Reference: "order_a", CurrencyCode: "try", Items: []calculateAmountsItem{{PriceSetID: "pset_1", Quantity: 2}}},
		{Reference: "order_b", CurrencyCode: "TRY", Attributes: map[string]string{"customer_group_id": "vip"},
			Items: []calculateAmountsItem{{PriceSetID: "pset_1"}}},
		{Reference: "order_c", CurrencyCode: "EUR", Items: []calculateAmountsItem{{PriceSetID: "pset_1"}}},
		{Reference: "order_d", CurrencyCode: "TRY", Items: []calculateAmountsItem{{PriceSetID: "pset_1", Quantity: 3}}},
	}})

	require.Len(t, out.Entries, 4)
	a := out.Entries[0].Items[0]
	assert.Equal(t, []int64{1000, 700}, []int64{a.Baseline.Amount, a.Trial.Amount},
		"the base without the list; the sale with it, its window and draft status set aside")
	assert.Equal(t, "price_trial", a.Trial.PriceID)
	b := out.Entries[1].Items[0]
	assert.Equal(t, []int64{900, 900}, []int64{b.Baseline.Amount, b.Trial.Amount},
		"the vip override outranks the sale either way")
	c := out.Entries[2].Items[0]
	assert.False(t, c.Baseline.Priced, "no price in the currency")
	assert.False(t, c.Trial.Priced)
	d := out.Entries[3].Items[0]
	assert.Equal(t, []int64{950, 600}, []int64{d.Baseline.Amount, d.Trial.Amount}, "each side's quantity tier")
}

// TestAListIsComparedAsTheTypeItIs: an override under trial takes the base's
// place even above another list's sale, as it would once active.
func TestAListIsComparedAsTheTypeItIs(t *testing.T) {
	t.Parallel()

	candidates := []models.PriceCandidate{
		basePrice("price_base", "TRY", 1000, 1, nil),
		withList(basePrice("price_sale", "TRY", 950, 1, nil), "plist_sale",
			&models.PriceListInfo{ID: "plist_sale", Type: models.PriceListSale, Status: models.PriceListActive}),
		withList(basePrice("price_contract", "TRY", 1100, 1, nil), "plist_contract",
			&models.PriceListInfo{ID: "plist_contract", Type: models.PriceListOverride, Status: models.PriceListDraft}),
	}
	svc := newTestService(&stubRepo{
		getPriceListFn: func(_ context.Context, id string) (models.PriceList, error) {
			return models.PriceList{ID: id, Type: models.PriceListOverride, Status: models.PriceListDraft}, nil
		},
		listCandidatesBySetsFn: func(_ context.Context, _ []string) (map[string][]models.PriceCandidate, error) {
			return map[string][]models.PriceCandidate{"pset_1": candidates}, nil
		},
	})

	out := compare(t, svc, "plist_contract", compareListRequest{Entries: []compareListEntry{
		{Reference: "order_a", CurrencyCode: "TRY", Items: []calculateAmountsItem{{PriceSetID: "pset_1"}}},
	}})

	item := out.Entries[0].Items[0]
	assert.Equal(t, []int64{950, 1100}, []int64{item.Baseline.Amount, item.Trial.Amount})
	assert.Equal(t, "override", item.Trial.PriceListType)
}

// TestAComparisonNeedsAListThatExistsAndBoundsItsSize: an unknown list, too
// many purchases and a bad set id are refused.
func TestAComparisonNeedsAListThatExistsAndBoundsItsSize(t *testing.T) {
	t.Parallel()

	svc := newTestService(compareRepo(models.PriceListActive))
	payload := json.RawMessage(`{"entries":[]}`)

	_, err := svc.CompareListJSON(context.Background(), "plist_other", payload)
	assert.True(t, errors.IsNotFound(err))
	_, err = svc.CompareListJSON(context.Background(), "pset_1", payload)
	assert.True(t, errors.IsInvalid(err), "a list id of another kind")

	many := compareListRequest{Entries: make([]compareListEntry, MaxCompareEntries+1)}
	for i := range many.Entries {
		many.Entries[i] = compareListEntry{Reference: "o", CurrencyCode: "TRY"}
	}
	body, err := json.Marshal(many)
	require.NoError(t, err)
	_, err = svc.CompareListJSON(context.Background(), "plist_trial", body)
	assert.True(t, errors.IsInvalid(err), "one purchase too many")
	many.Entries = many.Entries[:MaxCompareEntries]
	body, err = json.Marshal(many)
	require.NoError(t, err)
	_, err = svc.CompareListJSON(context.Background(), "plist_trial", body)
	require.NoError(t, err, "the most purchases")

	_, err = svc.CompareListJSON(context.Background(), "plist_trial",
		json.RawMessage(`{"entries":[{"reference":"o","currency_code":"TRY","items":[{"price_set_id":"bad"}]}]}`))
	assert.True(t, errors.IsInvalid(err), "a set id of another kind")

	lines := strings.Repeat(`{"price_set_id":"pset_1"},`, MaxCompareItems/2)
	half := `{"reference":"o","currency_code":"TRY","items":[` + strings.TrimSuffix(lines, ",") + `]}`
	_, err = svc.CompareListJSON(context.Background(), "plist_trial",
		json.RawMessage(`{"entries":[`+half+`,`+half+`]}`))
	require.NoError(t, err, "the most lines")
	_, err = svc.CompareListJSON(context.Background(), "plist_trial",
		json.RawMessage(`{"entries":[`+half+`,`+half+`,{"reference":"p","currency_code":"TRY","items":[{"price_set_id":"pset_1"}]}]}`))
	assert.True(t, errors.IsInvalid(err), "one line too many")
}
