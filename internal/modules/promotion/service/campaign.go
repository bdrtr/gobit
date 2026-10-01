package service

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// CampaignInput is the write input of a campaign.
type CampaignInput struct {
	// Name is the campaign's display name; it cannot be empty.
	Name string
	// CampaignIdentifier is the unique business identifier the operator gives.
	CampaignIdentifier string
	// Description is the optional description.
	Description string
	// StartsAt is the start of the validity window; if nil there is no lower
	// bound.
	StartsAt *time.Time
	// EndsAt is the end of the validity window; if nil there is no upper bound.
	EndsAt *time.Time
	// BudgetType is the budget's unit of measure; if it is given empty "none" is
	// assumed.
	BudgetType models.CampaignBudgetType
	// BudgetLimit is the budget's upper bound; if nil there is no bound.
	BudgetLimit *int64
	// BudgetCurrencyCode is the currency of a "spend" budget; it must not be
	// given for the other types.
	BudgetCurrencyCode string
}

// CreateCampaign creates a new campaign.
//
// The business identifier (CampaignIdentifier) is UNIQUE; the same identifier
// cannot be taken a second time, and the attempt returns errors.Conflict. The
// uniqueness is enforced by a partial index in the database, not by the
// service: between two concurrent requests only the database can be the
// arbiter.
//
// The budget COUNTER starts at zero and cannot be written through this path.
func (s *Service) CreateCampaign(ctx context.Context, in CampaignInput) (models.Campaign, error) {
	if err := s.ready(); err != nil {
		return models.Campaign{}, err
	}

	now := s.clock()
	campaign, err := buildCampaign(models.NewCampaignID(now), in, now)
	if err != nil {
		return models.Campaign{}, err
	}
	return s.repo.CreateCampaign(ctx, campaign, now)
}

// GetCampaign returns the campaign by id; errors.NotFound if there is none.
func (s *Service) GetCampaign(ctx context.Context, id string) (models.Campaign, error) {
	if err := s.ready(); err != nil {
		return models.Campaign{}, err
	}
	if err := requireID(id, models.CampaignIDPrefix, "campaign id"); err != nil {
		return models.Campaign{}, err
	}
	return s.repo.GetCampaign(ctx, id)
}

// GetCampaignByIdentifier returns the campaign by business identifier;
// errors.NotFound if there is none.
func (s *Service) GetCampaignByIdentifier(ctx context.Context, identifier string) (models.Campaign, error) {
	if err := s.ready(); err != nil {
		return models.Campaign{}, err
	}
	if err := validateText("campaign business identifier", identifier, 1, MaxIdentifierLen); err != nil {
		return models.Campaign{}, err
	}
	return s.repo.GetCampaignByIdentifier(ctx, identifier)
}

// ListCampaigns returns a paged list of campaigns.
func (s *Service) ListCampaigns(ctx context.Context, limit, offset int32) (Page[models.Campaign], error) {
	if err := s.ready(); err != nil {
		return Page[models.Campaign]{}, err
	}
	limit, offset, err := normalizePaging(limit, offset)
	if err != nil {
		return Page[models.Campaign]{}, err
	}

	items, total, err := s.repo.ListCampaigns(ctx, limit, offset)
	if err != nil {
		return Page[models.Campaign]{}, err
	}
	return Page[models.Campaign]{Items: items, Count: total, Limit: limit, Offset: offset}, nil
}

// UpdateCampaign REPLACES the campaign's definition; the budget counter does
// not change.
//
// It is not a partial update (for the reasoning see [Service.UpdatePromotion]).
//
// # The budget's UNIT cannot be changed while the counter is not zero
//
// While the counter (budget_used) is non-zero, changing the budget's TYPE or
// CURRENCY returns errors.Conflict. The reason is that the counter itself stays
// in the old unit: a COUNT of 30 accumulated in a "usage" budget is read as 30
// MINOR UNITS once the type is made "spend", and the budget silently loses its
// meaning. The rule is enforced in the database, with a single conditional
// UPDATE (see repository.UpdateCampaign) — so it cannot race a concurrent use.
//
// The date window, the name, the description and the budget LIMIT can always
// be updated, independently of the counter: none of them changes the counter's
// unit.
func (s *Service) UpdateCampaign(ctx context.Context, id string, in CampaignInput) (models.Campaign, error) {
	if err := s.ready(); err != nil {
		return models.Campaign{}, err
	}
	if err := requireID(id, models.CampaignIDPrefix, "campaign id"); err != nil {
		return models.Campaign{}, err
	}

	now := s.clock()
	campaign, err := buildCampaign(id, in, now)
	if err != nil {
		return models.Campaign{}, err
	}
	return s.repo.UpdateCampaign(ctx, campaign, now)
}

// DeleteCampaign deletes the campaign with a soft delete.
//
// The campaign's promotions are NOT DELETED; left without their campaign, they
// are skipped in the computation (see the skip rule of
// [Service.ComputeDiscounts]). This is the way to stop a campaign without
// losing its promotions.
func (s *Service) DeleteCampaign(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(id, models.CampaignIDPrefix, "campaign id"); err != nil {
		return err
	}
	return s.repo.DeleteCampaign(ctx, id, s.clock())
}

// buildCampaign validates the input and converts it into the domain model to
// be written.
func buildCampaign(id string, in CampaignInput, now time.Time) (models.Campaign, error) {
	if err := validateText("campaign name", in.Name, 1, MaxNameLen); err != nil {
		return models.Campaign{}, err
	}
	if err := validateText("campaign business identifier", in.CampaignIdentifier, 1, MaxIdentifierLen); err != nil {
		return models.Campaign{}, err
	}
	if err := validateText("campaign description", in.Description, 0, MaxDescriptionLen); err != nil {
		return models.Campaign{}, err
	}
	if in.StartsAt != nil && in.EndsAt != nil && !in.StartsAt.Before(*in.EndsAt) {
		return models.Campaign{}, errors.Invalid(CodeInvalidInput,
			"the campaign start has to be before its end (start: %s, end: %s)",
			in.StartsAt.UTC().Format(time.RFC3339), in.EndsAt.UTC().Format(time.RFC3339))
	}

	budgetType, limit, currency, err := normalizeBudget(in)
	if err != nil {
		return models.Campaign{}, err
	}

	return models.Campaign{
		ID:                 id,
		Name:               in.Name,
		CampaignIdentifier: in.CampaignIdentifier,
		Description:        in.Description,
		StartsAt:           copyTime(in.StartsAt),
		EndsAt:             copyTime(in.EndsAt),
		BudgetType:         budgetType,
		BudgetLimit:        limit,
		BudgetCurrencyCode: currency,
		CreatedAt:          now,
		UpdatedAt:          now,
	}, nil
}

// normalizeBudget validates the budget fields and makes them consistent.
//
// Three rules are enforced, and all three match the CHECK constraints in the
// migration:
//
//   - A "spend" budget REQUIRES a currency; the other types CARRY NO currency.
//   - A campaign without a budget ("none") CANNOT have a limit; if there is a
//     limit, a type has to be chosen.
//   - The limit cannot be negative and cannot exceed [models.MaxAmount].
//
// Validating here as well is deliberate: the database constraint is the last
// line of defense, but its error reaches the user as a "constraint violation";
// the error here says WHAT is wrong.
func normalizeBudget(in CampaignInput) (
	budgetType models.CampaignBudgetType,
	limit *int64,
	currencyCode string,
	err error,
) {
	budgetType = in.BudgetType
	if budgetType == "" {
		budgetType = models.BudgetNone
	}
	if !budgetType.Valid() {
		return "", nil, "", errors.Invalid(CodeInvalidInput,
			"campaign budget type is undefined: %q", string(in.BudgetType))
	}

	if budgetType == models.BudgetNone {
		if in.BudgetLimit != nil {
			return "", nil, "", errors.Invalid(CodeInvalidInput,
				"a campaign without a budget cannot be given a budget limit; "+
					"a budget type has to be chosen first")
		}
		if in.BudgetCurrencyCode != "" {
			return "", nil, "", errors.Invalid(CodeInvalidInput,
				"a campaign without a budget cannot be given a budget currency")
		}
		return budgetType, nil, "", nil
	}

	if in.BudgetLimit == nil {
		return "", nil, "", errors.Invalid(CodeInvalidInput,
			"a %q budget requires a limit; for an unlimited budget the type has to be %q",
			string(budgetType), string(models.BudgetNone))
	}
	if *in.BudgetLimit < 0 {
		return "", nil, "", errors.Invalid(CodeInvalidInput,
			"budget limit cannot be negative, %d given", *in.BudgetLimit)
	}
	if *in.BudgetLimit > models.MaxAmount {
		return "", nil, "", errors.Invalid(CodeInvalidInput,
			"budget limit can be at most %d, %d given", models.MaxAmount, *in.BudgetLimit)
	}

	if budgetType == models.BudgetUsage {
		if in.BudgetCurrencyCode != "" {
			return "", nil, "", errors.Invalid(CodeInvalidInput,
				"a count-measured budget cannot be given a currency, %q given", in.BudgetCurrencyCode)
		}
		return budgetType, copyInt64(in.BudgetLimit), "", nil
	}

	currency, err := normalizeCurrency(in.BudgetCurrencyCode)
	if err != nil {
		return "", nil, "", err
	}
	return budgetType, copyInt64(in.BudgetLimit), currency, nil
}

// copyTime converts a time pointer to UTC and returns it as a COPY.
func copyTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	out := t.UTC()
	return &out
}
