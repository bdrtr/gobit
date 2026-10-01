package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// CodeCampaignMoved refuses putting a promotion into a campaign when it is no
// longer in the campaign the caller read (ADR 0320).
const CodeCampaignMoved = "promotion_campaign_moved"

// CodeCampaignGone refuses putting a promotion into a campaign that is not
// live (ADR 0320).
const CodeCampaignGone = "promotion_campaign_gone"

// SetPromotionCampaign puts the promotion into the campaign to, or out of any
// when to is nil, if it is still in the campaign from the caller read, nil for
// none (ADR 0320). It writes the campaign alone, so an edit of the promotion's
// other fields made meanwhile is kept, and it refuses with [CodeCampaignMoved]
// when the promotion moved since and with [CodeCampaignGone] when to is not a
// live campaign.
func (s *Service) SetPromotionCampaign(ctx context.Context, id string, from, to *string) (models.Promotion, error) {
	if err := s.ready(); err != nil {
		return models.Promotion{}, err
	}
	if err := requireID(id, models.PromotionIDPrefix, "promotion id"); err != nil {
		return models.Promotion{}, err
	}
	for _, campaign := range []*string{from, to} {
		if campaign == nil {
			continue
		}
		if err := requireID(*campaign, models.CampaignIDPrefix, "campaign id"); err != nil {
			return models.Promotion{}, err
		}
	}
	if sameCampaign(from, to) {
		return models.Promotion{}, errors.Invalid(CodeInvalidInput,
			"the promotion is put into the campaign it is in: %s", campaignName(to))
	}

	promo, set, err := s.repo.SetPromotionCampaign(ctx, id, from, to, s.clock())
	if err != nil {
		return models.Promotion{}, err
	}
	if set {
		return promo, nil
	}

	// Nothing moved: the promotion is gone, it is in another campaign now, or
	// the campaign is not live.
	current, err := s.GetPromotion(ctx, id)
	if err != nil {
		return models.Promotion{}, err
	}
	if !sameCampaign(current.CampaignID, from) {
		return models.Promotion{}, errors.Conflict(CodeCampaignMoved,
			"promotion %s is in %s now, not %s; draw the page again",
			id, campaignName(current.CampaignID), campaignName(from))
	}

	return models.Promotion{}, errors.NotFound(CodeCampaignGone, "there is no live campaign %s", *to)
}

// sameCampaign reports whether two campaign references name the same one,
// nil naming none.
func sameCampaign(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}

	return *a == *b
}

// campaignName spells a campaign reference for a message.
func campaignName(campaign *string) string {
	if campaign == nil {
		return "no campaign"
	}

	return "campaign " + *campaign
}
