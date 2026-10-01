package promotion

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/service"
)

// AdminSurface is the promotion module's panel surface (ADR 0311): the
// promotions in a status with their usage, which the read provider keeps out
// because the read layer cannot tell a storefront from an operator. Only
// primitives and JSON cross it, as across every surface the panel resolves
// (ADR 0001).
type AdminSurface struct {
	svc *service.Service
}

// NewAdminSurface builds the panel's surface over the service.
func NewAdminSurface(svc *service.Service) *AdminSurface { return &AdminSurface{svc: svc} }

// codeAdminReadFailed reports a listing that could not be encoded.
const codeAdminReadFailed = "promotion_admin_read_failed"

// adminPromotion is one promotion as the panel lists it; the json tags are the
// contract with the panel, which cannot import this package.
type adminPromotion struct {
	ID          string    `json:"id"`
	Code        string    `json:"code"`
	IsAutomatic bool      `json:"is_automatic"`
	Type        string    `json:"type"`
	Status      string    `json:"status"`
	UsageCount  int64     `json:"usage_count"`
	UsageLimit  *int64    `json:"usage_limit"`
	CreatedAt   time.Time `json:"created_at"`
}

// PromotionsJSON lists the promotions in the status — draft, active or
// inactive — a page at a time, with the total in the status.
func (a *AdminSurface) PromotionsJSON(ctx context.Context, status string, limit, offset int32) (json.RawMessage, int64, error) {
	if a == nil || a.svc == nil {
		return nil, 0, errors.Unavailable(codeSetupFailed, "the promotion service is not set up")
	}

	wanted := models.PromotionStatus(status)
	page, err := a.svc.ListPromotions(ctx, service.ListPromotionsInput{Status: &wanted, Limit: limit, Offset: offset})
	if err != nil {
		return nil, 0, err
	}

	out := make([]adminPromotion, 0, len(page.Items))
	for i := range page.Items {
		p := &page.Items[i]
		out = append(out, adminPromotion{
			ID: p.ID, Code: p.Code, IsAutomatic: p.IsAutomatic, Type: string(p.Type),
			Status: string(p.Status), UsageCount: p.UsageCount, UsageLimit: p.UsageLimit,
			CreatedAt: p.CreatedAt,
		})
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, 0, errors.Wrap(err, errors.KindInternal, codeAdminReadFailed,
			"the promotions could not be encoded")
	}

	return body, page.Count, nil
}

// SwitchPromotionStatus moves the promotion from the status the operator read
// to another, and refuses when it is no longer in the first (ADR 0312).
func (a *AdminSurface) SwitchPromotionStatus(ctx context.Context, id, from, to string) error {
	if a == nil || a.svc == nil {
		return errors.Unavailable(codeSetupFailed, "the promotion service is not set up")
	}

	_, err := a.svc.SwitchPromotionStatus(ctx, id, models.PromotionStatus(from), models.PromotionStatus(to))
	return err
}

// latestUses is how many of a promotion's uses its page shows.
const latestUses = 20

// adminCampaign is the campaign a promotion belongs to, as its page shows it.
type adminCampaign struct {
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	StartsAt           *time.Time `json:"starts_at"`
	EndsAt             *time.Time `json:"ends_at"`
	BudgetType         string     `json:"budget_type"`
	BudgetLimit        *int64     `json:"budget_limit"`
	BudgetUsed         int64      `json:"budget_used"`
	BudgetCurrencyCode string     `json:"budget_currency_code"`
}

// adminMethod is what a promotion gives.
type adminMethod struct {
	Type            string `json:"type"`
	TargetType      string `json:"target_type"`
	Allocation      string `json:"allocation"`
	Value           int64  `json:"value"`
	MaxQuantity     *int64 `json:"max_quantity"`
	BuyQuantity     *int64 `json:"buy_quantity"`
	ApplyToQuantity *int64 `json:"apply_to_quantity"`
	CurrencyCode    string `json:"currency_code"`
}

// adminRule is one condition a promotion holds to.
type adminRule struct {
	Type      string   `json:"type"`
	Attribute string   `json:"attribute"`
	Operator  string   `json:"operator"`
	Values    []string `json:"values"`
}

// adminUse is one use of a promotion.
type adminUse struct {
	Reference    string     `json:"reference"`
	Amount       int64      `json:"amount"`
	CurrencyCode string     `json:"currency_code"`
	CreatedAt    time.Time  `json:"created_at"`
	ReleasedAt   *time.Time `json:"released_at"`
}

// adminPromotionPage is one promotion as its page shows it: the list's row,
// what it gives, to whom, under which campaign, and its latest uses.
type adminPromotionPage struct {
	adminPromotion
	Campaign *adminCampaign `json:"campaign"`
	Method   *adminMethod   `json:"application_method"`
	Rules    []adminRule    `json:"rules"`
	Uses     []adminUse     `json:"latest_uses"`
}

// PromotionJSON reads one promotion for its page (ADR 0313): its discount, or
// null when it has none and so applies nothing; its rules; its campaign, or
// null; and its latest uses, newest first.
func (a *AdminSurface) PromotionJSON(ctx context.Context, id string) (json.RawMessage, error) {
	if a == nil || a.svc == nil {
		return nil, errors.Unavailable(codeSetupFailed, "the promotion service is not set up")
	}

	p, err := a.svc.GetPromotion(ctx, id)
	if err != nil {
		return nil, err
	}
	page := adminPromotionPage{adminPromotion: adminPromotion{
		ID: p.ID, Code: p.Code, IsAutomatic: p.IsAutomatic, Type: string(p.Type),
		Status: string(p.Status), UsageCount: p.UsageCount, UsageLimit: p.UsageLimit,
		CreatedAt: p.CreatedAt,
	}, Rules: []adminRule{}, Uses: []adminUse{}}

	switch method, err := a.svc.GetApplicationMethod(ctx, id); {
	case err == nil:
		page.Method = &adminMethod{
			Type: string(method.Type), TargetType: string(method.TargetType),
			Allocation: string(method.Allocation), Value: method.Value,
			MaxQuantity: method.MaxQuantity, BuyQuantity: method.BuyQuantity,
			ApplyToQuantity: method.ApplyToQuantity, CurrencyCode: method.CurrencyCode,
		}
	case !errors.IsNotFound(err):
		return nil, err
	}

	if p.CampaignID != nil {
		switch c, err := a.svc.GetCampaign(ctx, *p.CampaignID); {
		case err == nil:
			page.Campaign = &adminCampaign{
				ID: c.ID, Name: c.Name, StartsAt: c.StartsAt, EndsAt: c.EndsAt,
				BudgetType: string(c.BudgetType), BudgetLimit: c.BudgetLimit,
				BudgetUsed: c.BudgetUsed, BudgetCurrencyCode: c.BudgetCurrencyCode,
			}
		case !errors.IsNotFound(err):
			return nil, err
		}
	}

	rules, err := a.svc.ListPromotionRules(ctx, id)
	if err != nil {
		return nil, err
	}
	for i := range rules {
		page.Rules = append(page.Rules, adminRule{
			Type: string(rules[i].RuleType), Attribute: rules[i].Attribute,
			Operator: string(rules[i].Operator), Values: rules[i].Values,
		})
	}

	uses, err := a.svc.LatestRedemptions(ctx, id, latestUses)
	if err != nil {
		return nil, err
	}
	for i := range uses {
		page.Uses = append(page.Uses, adminUse{
			Reference: uses[i].Reference, Amount: uses[i].Amount, CurrencyCode: uses[i].CurrencyCode,
			CreatedAt: uses[i].CreatedAt, ReleasedAt: uses[i].ReleasedAt,
		})
	}

	body, err := json.Marshal(page)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, codeAdminReadFailed,
			"the promotion could not be encoded")
	}

	return body, nil
}
