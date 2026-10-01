package promotion_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/service"
)

// pageRepo answers the reads a promotion's page makes before the one that
// fails; every other method is the nil interface's and is never reached.
type pageRepo struct {
	service.Repository
	promo     models.Promotion
	methodErr error
	campaign  error
}

func (r pageRepo) GetPromotion(context.Context, string) (models.Promotion, error) {
	return r.promo, nil
}

func (r pageRepo) GetApplicationMethod(context.Context, string) (models.ApplicationMethod, error) {
	return models.ApplicationMethod{}, r.methodErr
}

func (r pageRepo) GetCampaign(context.Context, string) (models.Campaign, error) {
	return models.Campaign{}, r.campaign
}

// TestAPromotionsPageDoesNotHideAFailedRead is ADR 0313: a discount or a
// campaign that is not there reads as null, but one that could not be read
// fails the page rather than showing a promotion that seems to give nothing.
func TestAPromotionsPageDoesNotHideAFailedRead(t *testing.T) {
	t.Parallel()

	campaignID := "camp_1"
	down := errors.Unavailable("db_down", "the database is down")
	missing := errors.NotFound("promotion_application_method_not_found", "no method")

	for name, repo := range map[string]pageRepo{
		"the discount": {promo: models.Promotion{ID: "promo_1"}, methodErr: down},
		"the campaign": {promo: models.Promotion{ID: "promo_1", CampaignID: &campaignID}, methodErr: missing, campaign: down},
	} {
		surface := promotion.NewAdminSurface(service.New(repo, service.Options{}))

		_, err := surface.PromotionJSON(context.Background(), "promo_1")

		require.Error(t, err, name)
		assert.Equal(t, "db_down", errors.CodeOf(err), name)
	}
}
