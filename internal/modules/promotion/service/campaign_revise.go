package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// CodeCampaignRevised refuses a campaign's revision when another writer
// revised the campaign since the caller read it (ADR 0331).
const CodeCampaignRevised = "promotion_campaign_revised"

// ReviseCampaign renames the campaign, rewrites its description, moves its
// window and sets its budget limit, writing them only while they are the ones
// the caller read, and refuses with [CodeCampaignRevised] when another writer
// changed any of them since (ADR 0331). The next terms are checked as a new
// campaign's are; a campaign with a budget takes a limit and one without
// takes none, as on a new campaign, and the budget's unit and counter are
// kept.
func (s *Service) ReviseCampaign(
	ctx context.Context, id string, read, next models.CampaignTerms,
) (models.Campaign, error) {
	if err := s.ready(); err != nil {
		return models.Campaign{}, err
	}
	if err := requireID(id, models.CampaignIDPrefix, "campaign id"); err != nil {
		return models.Campaign{}, err
	}
	next.Name, next.Description = strings.TrimSpace(next.Name), strings.TrimSpace(next.Description)
	if err := validateText("campaign name", next.Name, 1, MaxNameLen); err != nil {
		return models.Campaign{}, err
	}
	if err := validateText("campaign description", next.Description, 0, MaxDescriptionLen); err != nil {
		return models.Campaign{}, err
	}
	if err := checkCampaignWindow(next.StartsAt, next.EndsAt); err != nil {
		return models.Campaign{}, err
	}
	if next.BudgetLimit != nil {
		if err := checkBudgetLimit(*next.BudgetLimit); err != nil {
			return models.Campaign{}, err
		}
	}
	next.StartsAt, next.EndsAt, next.BudgetLimit = copyTime(next.StartsAt), copyTime(next.EndsAt), copyInt64(next.BudgetLimit)

	campaign, revised, err := s.repo.ReviseCampaign(ctx, id, read, next, s.clock())
	if err != nil || revised {
		return campaign, err
	}

	// Nothing was written: the campaign is gone, it moved since it was read,
	// or its budget type does not take the limit given.
	current, err := s.repo.GetCampaign(ctx, id)
	if err != nil {
		return models.Campaign{}, err
	}
	if current.Terms().Same(read) {
		if current.BudgetType == models.BudgetNone {
			return models.Campaign{}, errors.Invalid(CodeInvalidInput,
				"a campaign without a budget cannot be given a budget limit; a budget type has to be chosen first")
		}
		return models.Campaign{}, errors.Invalid(CodeInvalidInput,
			"a %q budget requires a limit; for an unlimited budget the type has to be %q",
			string(current.BudgetType), string(models.BudgetNone))
	}

	return models.Campaign{}, errors.Conflict(CodeCampaignRevised,
		"campaign %s was revised since it was read: it is %q now; draw the list again", id, current.Name)
}
