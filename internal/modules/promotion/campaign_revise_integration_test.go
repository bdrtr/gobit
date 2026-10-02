//go:build integration

package promotion_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository"
	"github.com/bdrtr/gobit/internal/modules/promotion/service"
)

// TestThePanelRevisesACampaignFromWhatItRead is ADR 0331 against a real
// PostgreSQL: the name, the description, the window, a start to the
// microsecond included, and the budget limit are written from the terms
// read, the identifier, the budget's unit and the counter a use moved kept; a
// stale name, start or limit writes nothing; a limit is refused on a campaign
// without a budget and an open one on a campaign with one; a deleted campaign
// is not found.
func TestThePanelRevisesACampaignFromWhatItRead(t *testing.T) {
	ctx := context.Background()
	svc := service.New(repository.New(testPool.Pool()), service.Options{})
	surface := promotion.NewAdminSurface(svc)

	starts := time.Date(2026, 3, 1, 9, 30, 15, 123456000, time.UTC)
	limit := int64(500000)
	identifier := "rev-" + uniqueCode()
	id, err := surface.CreateCampaign(ctx, "Spring", identifier, "the spring sale", &starts, nil, "spend", &limit, "TRY")
	require.NoError(t, err)
	promo := activePromotion(ctx, t, svc, service.PromotionInput{CampaignID: &id})
	_, err = svc.RedeemPromotion(ctx, service.RedeemInput{
		PromotionID: promo.ID, Reference: "order_" + uniqueCode(), Amount: 1250, CurrencyCode: "TRY",
	})
	require.NoError(t, err)

	ends := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	raised := int64(900000)
	require.NoError(t, surface.ReviseCampaign(ctx, id, "Spring", "the spring sale", &starts, nil, &limit,
		"Spring 2026", "", nil, &ends, &raised))
	campaign, err := svc.GetCampaign(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "Spring 2026|", campaign.Name+"|"+campaign.Description)
	assert.Nil(t, campaign.StartsAt, "the start opened")
	require.NotNil(t, campaign.EndsAt)
	assert.True(t, ends.Equal(*campaign.EndsAt), "the end as written")
	require.NotNil(t, campaign.BudgetLimit)
	assert.Equal(t, fmt.Sprintf("%s|spend|TRY|900000|1250", identifier), fmt.Sprintf("%s|%s|%s|%d|%d",
		campaign.CampaignIdentifier, campaign.BudgetType, campaign.BudgetCurrencyCode, *campaign.BudgetLimit, campaign.BudgetUsed),
		"the identifier, the budget's unit and its counter are kept")

	stale := int64(500000)
	for label, read := range map[string]models.CampaignTerms{
		"a name read before the revision": {Name: "Spring", EndsAt: &ends, BudgetLimit: &raised},
		"a description read before the revision": {
			Name: "Spring 2026", Description: "the spring sale", EndsAt: &ends, BudgetLimit: &raised,
		},
		"a start read before the revision": {Name: "Spring 2026", StartsAt: &starts, EndsAt: &ends, BudgetLimit: &raised},
		"an end read a microsecond off":    {Name: "Spring 2026", EndsAt: new(ends.Add(time.Microsecond)), BudgetLimit: &raised},
		"a limit read before the revision": {Name: "Spring 2026", EndsAt: &ends, BudgetLimit: &stale},
		"a limit read as none":             {Name: "Spring 2026", EndsAt: &ends},
	} {
		err = surface.ReviseCampaign(ctx, id, read.Name, read.Description, read.StartsAt, read.EndsAt, read.BudgetLimit,
			"Summer", "", nil, nil, &raised)
		require.Error(t, err, label)
		assert.Equal(t, service.CodeCampaignRevised, errors.CodeOf(err), "%s: %v", label, err)
		assert.Contains(t, err.Error(), `it is "Spring 2026" now`, label)
	}
	campaign, err = svc.GetCampaign(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "Spring 2026", campaign.Name, "a stale read writes nothing")

	err = surface.ReviseCampaign(ctx, id, "Spring 2026", "", nil, &ends, &raised, "Spring 2026", "", nil, &ends, nil)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a spend budget left without a limit: %v", err)

	plainID, err := surface.CreateCampaign(ctx, "Plain", "plain-"+uniqueCode(), "", nil, nil, "none", nil, "")
	require.NoError(t, err)
	err = surface.ReviseCampaign(ctx, plainID, "Plain", "", nil, nil, nil, "Plain", "", nil, nil, &raised)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a limit on a campaign without a budget: %v", err)
	assert.Contains(t, err.Error(), "a campaign without a budget cannot be given a budget limit")
	require.NoError(t, surface.ReviseCampaign(ctx, plainID, "Plain", "", nil, nil, nil, "Plainer", "", nil, nil, nil))

	require.NoError(t, svc.DeleteCampaign(ctx, plainID))
	err = surface.ReviseCampaign(ctx, plainID, "Plainer", "", nil, nil, nil, "Gone", "", nil, nil, nil)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "a deleted campaign: %v", err)
}
