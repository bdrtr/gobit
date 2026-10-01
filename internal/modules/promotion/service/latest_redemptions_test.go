package service

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// ListLatestRedemptions mirrors the query: the promotion's uses, newest id
// first, at most limit of them.
func (m *memRepo) ListLatestRedemptions(_ context.Context, promotionID string, limit int32) ([]models.Redemption, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("ListLatestRedemptions"); err != nil {
		return nil, err
	}
	var out []models.Redemption
	for i := range m.redemptions {
		if m.redemptions[i].PromotionID == promotionID {
			out = append(out, m.redemptions[i])
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if int32(len(out)) > limit {
		out = out[:limit]
	}
	return out, nil
}

func TestLatestRedemptionsBoundsTheRead(t *testing.T) {
	repo := newMemRepo()
	for _, id := range []string{"predeem_1", "predeem_2", "predeem_3"} {
		repo.redemptions = append(repo.redemptions, models.Redemption{ID: id, PromotionID: "promo_1"})
	}
	repo.redemptions = append(repo.redemptions, models.Redemption{ID: "predeem_4", PromotionID: "promo_2"})

	got, err := newTestService(repo).LatestRedemptions(context.Background(), "promo_1", 2)
	require.NoError(t, err)
	ids := make([]string, 0, len(got))
	for _, r := range got {
		ids = append(ids, r.ID)
	}
	assert.Equal(t, []string{"predeem_3", "predeem_2"}, ids, "newest first, the promotion's own, at most the limit")

	all, err := newTestService(repo).LatestRedemptions(context.Background(), "promo_1", 0)
	require.NoError(t, err)
	assert.Len(t, all, 3, "no limit is the default page, not none")
	assert.True(t, sort.SliceIsSorted(all, func(i, j int) bool { return all[i].ID > all[j].ID }))
}

func TestLatestRedemptionsRefusesAForeignID(t *testing.T) {
	repo := newMemRepo()

	_, err := newTestService(repo).LatestRedemptions(context.Background(), "camp_1", 20)

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Zero(t, repo.calls["ListLatestRedemptions"])
}
