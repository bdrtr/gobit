package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// ReviseCampaign mirrors the query: the terms are written only from the ones
// the caller read, on a live campaign, and a limit only beside a budget type
// and none without.
func (m *memRepo) ReviseCampaign(
	_ context.Context, id string, read, next models.CampaignTerms, now time.Time,
) (models.Campaign, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("ReviseCampaign"); err != nil {
		return models.Campaign{}, false, err
	}
	c, ok := m.campaigns[id]
	if !ok || c.DeletedAt != nil || !c.Terms().Same(read) ||
		(c.BudgetType == models.BudgetNone) != (next.BudgetLimit == nil) {
		return models.Campaign{}, false, nil
	}
	c.Name, c.Description, c.StartsAt, c.EndsAt, c.BudgetLimit = next.Name, next.Description, next.StartsAt, next.EndsAt, next.BudgetLimit
	c.UpdatedAt = now
	m.campaigns[id] = c
	return c, true, nil
}

// TestACampaignIsRevisedFromWhatWasRead is ADR 0331: the name and the
// description trimmed, the window in UTC and the limit are written from the
// terms read, the identifier, the budget's unit and its counter kept; a
// campaign revised since is refused by what it is now; a limit on a campaign
// without a budget and none on one with a budget are refused as on a new
// campaign; a bad name, window or limit is refused before the store is asked;
// an unknown campaign is not found.
func TestACampaignIsRevisedFromWhatWasRead(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := newMemRepo()
	svc := New(repo, Options{})
	limit := int64(10000)
	spring, err := svc.CreateCampaign(ctx, CampaignInput{
		Name: "Spring", CampaignIdentifier: "SPRING-26", BudgetType: models.BudgetSpend,
		BudgetLimit: &limit, BudgetCurrencyCode: "TRY",
	})
	require.NoError(t, err)
	plain, err := svc.CreateCampaign(ctx, CampaignInput{Name: "Plain", CampaignIdentifier: "PLAIN"})
	require.NoError(t, err)

	istanbul := time.FixedZone("TRT", 3*60*60)
	starts := time.Date(2026, 4, 1, 3, 0, 0, 0, istanbul)
	raised := int64(25000)
	revised, err := svc.ReviseCampaign(ctx, spring.ID, spring.Terms(), models.CampaignTerms{
		Name: " Spring 2026 ", Description: " the season ", StartsAt: &starts, BudgetLimit: &raised,
	})
	require.NoError(t, err)
	assert.Equal(t, "Spring 2026|the season", revised.Name+"|"+revised.Description)
	require.NotNil(t, revised.StartsAt)
	assert.Equal(t, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), *revised.StartsAt)
	assert.Equal(t, time.UTC, revised.StartsAt.Location(), "the window in UTC")
	require.NotNil(t, revised.BudgetLimit)
	assert.Equal(t, int64(25000), *revised.BudgetLimit)
	assert.Equal(t, "SPRING-26|spend|TRY", revised.CampaignIdentifier+"|"+string(revised.BudgetType)+"|"+revised.BudgetCurrencyCode,
		"the identifier and the budget's unit are kept")

	_, err = svc.ReviseCampaign(ctx, spring.ID, spring.Terms(), models.CampaignTerms{Name: "Summer", BudgetLimit: &limit})
	require.Error(t, err)
	assert.Equal(t, CodeCampaignRevised, errors.CodeOf(err), "read as Spring, it is Spring 2026 now: %v", err)
	assert.Contains(t, err.Error(), `it is "Spring 2026" now`)

	_, err = svc.ReviseCampaign(ctx, spring.ID, revised.Terms(), models.CampaignTerms{Name: "Spring 2026"})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a spend budget without a limit: %v", err)
	assert.Contains(t, err.Error(), `a "spend" budget requires a limit`)
	_, err = svc.ReviseCampaign(ctx, plain.ID, plain.Terms(), models.CampaignTerms{Name: "Plain", BudgetLimit: &limit})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a limit without a budget: %v", err)
	assert.Contains(t, err.Error(), "a campaign without a budget cannot be given a budget limit")

	before := repo.calls["ReviseCampaign"]
	negative := int64(-1)
	for label, next := range map[string]models.CampaignTerms{
		"an empty name":           {Name: "  ", BudgetLimit: &limit},
		"a backwards window":      {Name: "X", StartsAt: &starts, EndsAt: &starts, BudgetLimit: &limit},
		"a negative limit":        {Name: "X", BudgetLimit: &negative},
		"an overlong description": {Name: "X", Description: string(make([]byte, MaxDescriptionLen+1)), BudgetLimit: &limit},
	} {
		_, err = svc.ReviseCampaign(ctx, spring.ID, revised.Terms(), next)
		assert.True(t, errors.IsInvalid(err), "%s: %v", label, err)
	}
	assert.Equal(t, before, repo.calls["ReviseCampaign"], "a refused input never reaches the store")

	_, err = svc.ReviseCampaign(ctx, "camp_missing", spring.Terms(), models.CampaignTerms{Name: "X"})
	assert.True(t, errors.IsNotFound(err), "an unknown campaign: %v", err)
}

// TestCampaignTermsCompareInstantsAndLimits: the same instant in two zones is
// the same end, an open end and no limit are only the same as an open end
// and no limit, and the text and the limit are compared as they are.
func TestCampaignTermsCompareInstantsAndLimits(t *testing.T) {
	t.Parallel()

	utc := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	local := utc.In(time.FixedZone("TRT", 3*60*60))
	later := utc.Add(time.Microsecond)
	limit, same, other := int64(100), int64(100), int64(101)
	base := models.CampaignTerms{Name: "A", Description: "d", StartsAt: &utc, BudgetLimit: &limit}

	assert.True(t, base.Same(models.CampaignTerms{Name: "A", Description: "d", StartsAt: &local, BudgetLimit: &same}))
	for name, next := range map[string]models.CampaignTerms{
		"a later start":       {Name: "A", Description: "d", StartsAt: &later, BudgetLimit: &limit},
		"an open start":       {Name: "A", Description: "d", BudgetLimit: &limit},
		"a closed end":        {Name: "A", Description: "d", StartsAt: &utc, EndsAt: &utc, BudgetLimit: &limit},
		"another limit":       {Name: "A", Description: "d", StartsAt: &utc, BudgetLimit: &other},
		"no limit":            {Name: "A", Description: "d", StartsAt: &utc},
		"another name":        {Name: "B", Description: "d", StartsAt: &utc, BudgetLimit: &limit},
		"another description": {Name: "A", Description: "e", StartsAt: &utc, BudgetLimit: &limit},
	} {
		assert.False(t, base.Same(next), name)
		assert.False(t, next.Same(base), "%s, the other way", name)
	}
}
