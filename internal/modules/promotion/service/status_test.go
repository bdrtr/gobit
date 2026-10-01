package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository"
)

// SwitchPromotionStatus mirrors the query: the status moves only from the one
// the caller read, and only the status and the stamp are written.
func (m *memRepo) SwitchPromotionStatus(
	_ context.Context, id string, from, to models.PromotionStatus, now time.Time,
) (models.Promotion, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("SwitchPromotionStatus"); err != nil {
		return models.Promotion{}, false, err
	}
	p, ok := m.promotions[id]
	if !ok || p.Status != from {
		return models.Promotion{}, false, nil
	}
	p.Status = to
	p.UpdatedAt = now
	m.promotions[id] = p
	return p, true, nil
}

// statusFixture writes a promotion in the status and returns its id.
func statusFixture(t *testing.T, repo *memRepo, code string, status models.PromotionStatus) string {
	t.Helper()
	promo, err := newTestService(repo).CreatePromotion(context.Background(), PromotionInput{
		Code: code, Status: status,
	})
	require.NoError(t, err)
	return promo.ID
}

func TestSwitchPromotionStatusMovesFromTheStatusRead(t *testing.T) {
	repo := newMemRepo()
	id := statusFixture(t, repo, "SPRING", models.PromotionActive)
	other := statusFixture(t, repo, "AUTUMN", models.PromotionActive)

	promo, err := newTestService(repo).SwitchPromotionStatus(context.Background(), id,
		models.PromotionActive, models.PromotionInactive)
	require.NoError(t, err)

	assert.Equal(t, models.PromotionInactive, promo.Status)
	assert.Equal(t, models.PromotionInactive, repo.promotions[id].Status)
	assert.Equal(t, models.PromotionActive, repo.promotions[other].Status,
		"only the named promotion moves")
}

func TestSwitchPromotionStatusRefusesAStatusThatMoved(t *testing.T) {
	repo := newMemRepo()
	id := statusFixture(t, repo, "SPRING", models.PromotionInactive)

	_, err := newTestService(repo).SwitchPromotionStatus(context.Background(), id,
		models.PromotionActive, models.PromotionInactive)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, CodeStatusMoved, errors.CodeOf(err))
	assert.Contains(t, err.Error(), "is inactive now", "the refusal names the status the promotion is in")
}

func TestSwitchPromotionStatusReportsAMissingPromotion(t *testing.T) {
	repo := newMemRepo()

	_, err := newTestService(repo).SwitchPromotionStatus(context.Background(), "promo_missing",
		models.PromotionActive, models.PromotionInactive)

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
	assert.Equal(t, repository.CodePromotionNotFound, errors.CodeOf(err))
}

func TestSwitchPromotionStatusRefusesBadInputBeforeWriting(t *testing.T) {
	cases := map[string]struct {
		id       string
		from, to models.PromotionStatus
	}{
		"foreign id":       {"camp_1", models.PromotionActive, models.PromotionInactive},
		"undefined from":   {"promo_1", "paused", models.PromotionInactive},
		"undefined to":     {"promo_1", models.PromotionActive, "paused"},
		"no move":          {"promo_1", models.PromotionActive, models.PromotionActive},
		"empty from":       {"promo_1", "", models.PromotionActive},
		"empty to":         {"promo_1", models.PromotionDraft, ""},
		"draft into draft": {"promo_1", models.PromotionDraft, models.PromotionDraft},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			repo := newMemRepo()

			_, err := newTestService(repo).SwitchPromotionStatus(context.Background(), tc.id, tc.from, tc.to)

			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
			assert.Zero(t, repo.calls["SwitchPromotionStatus"], "a refused switch writes nothing")
		})
	}
}

func TestSwitchPromotionStatusPassesTheRepositoryError(t *testing.T) {
	repo := newMemRepo()
	repo.errOn["SwitchPromotionStatus"] = errors.Unavailable("db_down", "the database is down")

	_, err := newTestService(repo).SwitchPromotionStatus(context.Background(), "promo_1",
		models.PromotionDraft, models.PromotionActive)

	require.Error(t, err)
	assert.Equal(t, "db_down", errors.CodeOf(err))
	assert.Zero(t, repo.calls["GetPromotion"], "a failed write is not read back")
}
