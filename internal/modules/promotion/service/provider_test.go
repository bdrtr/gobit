package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// providerRepo produces a repository filled with active and non-active
// promotions.
func providerRepo() *memRepo {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{
		ID: "promo_1", Code: "AKTIF", Status: models.PromotionActive, IsAutomatic: true,
	}, nil)
	seedPromotion(repo, models.Promotion{
		ID: "promo_2", Code: "TASLAK", Status: models.PromotionDraft,
	}, nil)
	seedPromotion(repo, models.Promotion{
		ID: "promo_3", Code: "PASIF", Status: models.PromotionInactive,
	}, nil)
	return repo
}

func TestQueryProviderEntityName(t *testing.T) {
	provider := NewQueryProvider(newTestService(newMemRepo()))

	assert.Equal(t, "promotion", provider.Entity())
	assert.Equal(t, "promotion.query", provider.Entity()+query.ProviderSuffix,
		"Query looks the provider up by this name")
}

func TestQueryProviderListsOnlyActivePromotions(t *testing.T) {
	provider := NewQueryProvider(newTestService(providerRepo()))

	records, err := provider.List(context.Background(), query.ListOptions{})
	require.NoError(t, err)

	require.Len(t, records, 1, "draft and inactive promotions do NOT leak through the read surface")
	assert.Equal(t, "promo_1", records[0]["id"])
	assert.Equal(t, "AKTIF", records[0]["code"])
}

func TestQueryProviderFetchByIDsAppliesTheSameFilter(t *testing.T) {
	provider := NewQueryProvider(newTestService(providerRepo()))

	records, err := provider.FetchByIDs(context.Background(),
		[]string{"promo_1", "promo_2", "promo_missing"}, nil)
	require.NoError(t, err)

	require.Len(t, records, 1,
		"the rule has to be ONE; the two surfaces diverging would expose a draft coupon through a link")
	assert.Equal(t, "promo_1", records[0]["id"])
}

func TestQueryProviderDoesNotExposeSensitiveFields(t *testing.T) {
	provider := NewQueryProvider(newTestService(providerRepo()))

	records, err := provider.List(context.Background(), query.ListOptions{})
	require.NoError(t, err)
	require.Len(t, records, 1)

	for _, field := range []string{"usage_count", "usage_limit", "metadata", "rules", "application_method"} {
		assert.NotContains(t, records[0], field, "%q must not leak through the read surface", field)
	}
}

func TestQueryProviderFieldSelection(t *testing.T) {
	provider := NewQueryProvider(newTestService(providerRepo()))

	records, err := provider.FetchByIDs(context.Background(), []string{"promo_1"}, []string{"code"})
	require.NoError(t, err)
	require.Len(t, records, 1)

	assert.Equal(t, "AKTIF", records[0]["code"])
	assert.Contains(t, records[0], "id",
		"Query joins records by id; the id is added even when it is not requested")
	assert.NotContains(t, records[0], "status")
}

func TestQueryProviderRejectsAnUndefinedField(t *testing.T) {
	provider := NewQueryProvider(newTestService(providerRepo()))

	_, err := provider.FetchByIDs(context.Background(), []string{"promo_1"}, []string{"usage_count"})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err),
		"ADR 0004: field validation belongs to the provider")
}

func TestQueryProviderIDFilter(t *testing.T) {
	provider := NewQueryProvider(newTestService(providerRepo()))

	records, err := provider.List(context.Background(), query.ListOptions{
		Filters: map[string]any{"id": []string{"promo_1"}},
	})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "promo_1", records[0]["id"])

	empty, err := provider.List(context.Background(), query.ListOptions{
		Filters: map[string]any{"id": []string{}},
	})
	require.NoError(t, err)
	assert.Empty(t, empty, "an empty id set means 'none', not 'do not filter'")
}

func TestQueryProviderUnsupportedFilter(t *testing.T) {
	provider := NewQueryProvider(newTestService(providerRepo()))

	_, err := provider.List(context.Background(), query.ListOptions{
		Filters: map[string]any{"code": "AKTIF"},
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

func TestQueryProviderValidatesTheFilterType(t *testing.T) {
	provider := NewQueryProvider(newTestService(providerRepo()))

	_, err := provider.List(context.Background(), query.ListOptions{
		Filters: map[string]any{"id": 42},
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

func TestQueryProviderPreventsAnUnboundedList(t *testing.T) {
	repo := newMemRepo()
	for i := range int(MaxLimit) + 20 {
		id := models.NewPromotionID(testNow.Add(-1))
		seedPromotion(repo, models.Promotion{
			ID: id, Code: models.NewPromotionID(testNow) + string(rune('a'+i%26)),
			Status: models.PromotionActive, IsAutomatic: true,
		}, nil)
	}
	provider := NewQueryProvider(newTestService(repo))

	records, err := provider.List(context.Background(), query.ListOptions{})
	require.NoError(t, err)

	assert.LessOrEqual(t, len(records), int(MaxLimit),
		"an unbounded root list would load the whole table into memory in a single request")
}
