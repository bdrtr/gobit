package api

import (
	"time"

	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/service"
)

// The DTOs are kept SEPARATE from the domain models: JSON field names are the outer
// contract and a rename made in the model must not break the client.
//
// The split serves a second purpose here: the body of the CUSTOMER surface
// ([storeCouponDTO]) is a type distinct from the admin surface's one (see
// [promotionDTO]), and that distinction prevents a newly added field from leaking to
// the customer by accident.

// campaignDTO is the response body of a campaign.
type campaignDTO struct {
	// ID is the identifier of the campaign.
	ID string `json:"id"`
	// Name is the display name of the campaign.
	Name string `json:"name"`
	// CampaignIdentifier is the unique business identifier given by the operator.
	CampaignIdentifier string `json:"campaign_identifier"`
	// Description is the description.
	Description string `json:"description"`
	// StartsAt is the start of the validity window; null if there is none.
	StartsAt *time.Time `json:"starts_at"`
	// EndsAt is the end of the validity window; null if there is none.
	EndsAt *time.Time `json:"ends_at"`
	// BudgetType is the unit of measure of the budget (none | spend | usage).
	BudgetType string `json:"budget_type"`
	// BudgetLimit is the upper bound of the budget; null if unbounded.
	BudgetLimit *int64 `json:"budget_limit"`
	// BudgetUsed is the consumed part of the budget.
	BudgetUsed int64 `json:"budget_used"`
	// BudgetCurrencyCode is the currency of a "spend" budget; null if there is none.
	BudgetCurrencyCode *string `json:"budget_currency_code"`
	// CreatedAt is the moment of creation (RFC3339, UTC).
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the moment of the last update (RFC3339, UTC).
	UpdatedAt time.Time `json:"updated_at"`
}

// promotionDTO is the ADMIN response body of a promotion.
type promotionDTO struct {
	// ID is the identifier of the promotion.
	ID string `json:"id"`
	// Code is the coupon code (UPPERCASE).
	Code string `json:"code"`
	// IsAutomatic is whether the promotion is applied without a code.
	IsAutomatic bool `json:"is_automatic"`
	// Type is the mechanic of the promotion (standard | buyget).
	Type string `json:"type"`
	// CampaignID is the promotion's campaign; null if it has no campaign.
	CampaignID *string `json:"campaign_id"`
	// Status is the publication status (draft | active | inactive).
	Status string `json:"status"`
	// UsageLimit is the usage bound; null if unbounded.
	UsageLimit *int64 `json:"usage_limit"`
	// UsageCount is the number of times it has been used.
	UsageCount int64 `json:"usage_count"`
	// Metadata is the operator's free note.
	Metadata map[string]string `json:"metadata"`
	// CreatedAt is the moment of creation.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the moment of the last update.
	UpdatedAt time.Time `json:"updated_at"`
}

// applicationMethodDTO is the response body of an application method.
type applicationMethodDTO struct {
	// ID is the identifier of the method.
	ID string `json:"id"`
	// PromotionID is the promotion the method is bound to.
	PromotionID string `json:"promotion_id"`
	// Type is the measure of the discount (fixed | percentage).
	Type string `json:"type"`
	// TargetType is the target of the discount (items | shipping_methods | order).
	TargetType string `json:"target_type"`
	// Allocation is the distribution form (each | across).
	Allocation string `json:"allocation"`
	// Value is the fixed amount (minor unit) or the basis points.
	Value int64 `json:"value"`
	// MaxQuantity is the maximum quantity the fixed amount will be applied to; null if
	// unbounded.
	MaxQuantity *int64 `json:"max_quantity"`
	// CurrencyCode is the currency of a fixed-amount discount; null on a percentage.
	CurrencyCode *string `json:"currency_code"`
	// CreatedAt is the moment of creation.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the moment of the last update.
	UpdatedAt time.Time `json:"updated_at"`
}

// promotionRuleDTO is the ADMIN response body of a promotion rule.
//
// This type has NO counterpart on the store surface: the right-hand side of a rule is
// business information and it goes to the customer from no endpoint.
type promotionRuleDTO struct {
	// ID is the identifier of the rule.
	ID string `json:"id"`
	// PromotionID is the promotion the rule is bound to.
	PromotionID string `json:"promotion_id"`
	// RuleType is what the rule looks at (context | target).
	RuleType string `json:"rule_type"`
	// Attribute is the name of the field being looked at.
	Attribute string `json:"attribute"`
	// Operator is the comparison operator.
	Operator string `json:"operator"`
	// Values is the right-hand side of the comparison.
	Values []string `json:"values"`
	// CreatedAt is the moment of creation.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the moment of the last update.
	UpdatedAt time.Time `json:"updated_at"`
}

// redemptionDTO is the response body of a redemption record.
type redemptionDTO struct {
	// ID is the identifier of the use.
	ID string `json:"id"`
	// PromotionID is the promotion that was used.
	PromotionID string `json:"promotion_id"`
	// CampaignID is the campaign at the moment of use; null if there is none.
	CampaignID *string `json:"campaign_id"`
	// Reference is the business record reference of the use.
	Reference string `json:"reference"`
	// Amount is the applied discount amount (minor unit).
	Amount int64 `json:"amount"`
	// CurrencyCode is the currency of the discount.
	CurrencyCode string `json:"currency_code"`
	// BudgetDelta is the value added to the campaign budget.
	BudgetDelta int64 `json:"budget_delta"`
	// CreatedAt is the moment of creation.
	CreatedAt time.Time `json:"created_at"`
	// ReleasedAt is the moment of release; null if it is still in force.
	ReleasedAt *time.Time `json:"released_at"`
}

// computeResultDTO is the response body of a discount computation.
//
// The field names are EXACTLY the same as the interop schema (service.Interop): the
// two surfaces describing the same computation under different names would mean the
// client not knowing which one to look at.
//
// # The ONE field the interop schema does not carry
//
// `skipped` is here and not there, and the difference is deliberate on both
// counts. The interop body is consumed by the cart flow, which writes amounts
// onto a cart and has no use for a reason — a field with no consumer is refused
// (ADR 0009). And the cart's totals are read by the STOREFRONT, so a reason that
// traveled that way would eventually be readable by the customer whose code was
// refused, which is the leak this field must not open (ADR 0110).
type computeResultDTO struct {
	// CurrencyCode is the currency of the computation.
	CurrencyCode string `json:"currency_code"`
	// Items are the discounts per line.
	Items []lineDiscountDTO `json:"items"`
	// ShippingMethods are the discounts per shipping method.
	ShippingMethods []lineDiscountDTO `json:"shipping_methods"`
	// ItemsDiscountTotal is the total discount falling on the lines.
	ItemsDiscountTotal int64 `json:"items_discount_total"`
	// ShippingDiscountTotal is the total discount falling on shipping.
	ShippingDiscountTotal int64 `json:"shipping_discount_total"`
	// DiscountTotal is the total discount.
	DiscountTotal int64 `json:"discount_total"`
	// Applied are the promotions that actually produced a discount.
	Applied []appliedPromotionDTO `json:"applied"`
	// Skipped are the promotions that were CONSIDERED and left out, each with a
	// reason.
	//
	// It answers the question the rest of this body cannot: a merchant looking at
	// a cart with no discount could see WHAT applied and never why the coupon
	// they published did not. The population is the candidates the query
	// returned; the reasons are a closed set (see service.SkipReason).
	//
	// It is on the ADMIN endpoint alone. Telling a customer that their code
	// exists but its campaign has not started hands a code guesser a campaign
	// calendar — the leak the storefront's coupon lookup refuses to open, and
	// this must not open it from the side.
	Skipped []skippedPromotionDTO `json:"skipped"`
	// UnmatchedCodes are the coupon codes that could not be bound.
	UnmatchedCodes []string `json:"unmatched_codes"`
}

// skippedPromotionDTO is the response body of one promotion that was left out.
type skippedPromotionDTO struct {
	// PromotionID is the promotion that was left out.
	PromotionID string `json:"promotion_id"`
	// Code is its coupon code, and it is always written.
	//
	// Every promotion here has a code; an AUTOMATIC one simply does not need it
	// typed. A merchant reading this list wants the code either way — it is what
	// they published and what they will search for.
	Code string `json:"code"`
	// Reason is why it was left out; it is one of the service.SkipReason values.
	Reason string `json:"reason"`
}

// lineDiscountDTO is the response body of a single line discount.
type lineDiscountDTO struct {
	// ID is the identifier of the line.
	ID string `json:"id"`
	// Amount is the discount falling on the line (minor unit).
	Amount int64 `json:"amount"`
}

// appliedPromotionDTO is the response body of an applied promotion.
type appliedPromotionDTO struct {
	// PromotionID is the identifier of the promotion.
	PromotionID string `json:"promotion_id"`
	// Code is the coupon code of the promotion.
	Code string `json:"code"`
	// IsAutomatic is whether the promotion is applied without a code.
	IsAutomatic bool `json:"is_automatic"`
	// Amount is the total discount the promotion actually applied.
	Amount int64 `json:"amount"`
}

// storeCouponDTO is the coupon body that goes TO THE CUSTOMER.
//
// It is deliberately NARROW and is a type separate from the admin body: the status,
// the usage counter, the campaign budget, the metadata and the rule conditions ARE
// NOT HERE. Being a separate type structurally prevents a field added to the admin
// body from leaking in here by accident.
type storeCouponDTO struct {
	// Code is the coupon code (UPPERCASE).
	Code string `json:"code"`
	// Type is the measure of the discount (fixed | percentage).
	Type string `json:"type"`
	// TargetType is the target of the discount (items | shipping_methods | order).
	TargetType string `json:"target_type"`
	// Value is the fixed amount (minor unit) or the basis points.
	Value int64 `json:"value"`
	// CurrencyCode is the currency of a fixed-amount discount; null on a percentage.
	CurrencyCode *string `json:"currency_code"`
}

// toCampaignDTO turns the campaign into the response body.
func toCampaignDTO(c models.Campaign) campaignDTO {
	return campaignDTO{
		ID:                 c.ID,
		Name:               c.Name,
		CampaignIdentifier: c.CampaignIdentifier,
		Description:        c.Description,
		StartsAt:           c.StartsAt,
		EndsAt:             c.EndsAt,
		BudgetType:         string(c.BudgetType),
		BudgetLimit:        c.BudgetLimit,
		BudgetUsed:         c.BudgetUsed,
		BudgetCurrencyCode: stringOrNil(c.BudgetCurrencyCode),
		CreatedAt:          c.CreatedAt,
		UpdatedAt:          c.UpdatedAt,
	}
}

// toPromotionDTO turns the promotion into the admin response body.
func toPromotionDTO(p models.Promotion) promotionDTO {
	metadata := p.Metadata
	if metadata == nil {
		metadata = map[string]string{}
	}
	return promotionDTO{
		ID:          p.ID,
		Code:        p.Code,
		IsAutomatic: p.IsAutomatic,
		Type:        string(p.Type),
		CampaignID:  p.CampaignID,
		Status:      string(p.Status),
		UsageLimit:  p.UsageLimit,
		UsageCount:  p.UsageCount,
		Metadata:    metadata,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
	}
}

// toApplicationMethodDTO turns the application method into the response body.
func toApplicationMethodDTO(m models.ApplicationMethod) applicationMethodDTO {
	return applicationMethodDTO{
		ID:           m.ID,
		PromotionID:  m.PromotionID,
		Type:         string(m.Type),
		TargetType:   string(m.TargetType),
		Allocation:   string(m.Allocation),
		Value:        m.Value,
		MaxQuantity:  m.MaxQuantity,
		CurrencyCode: stringOrNil(m.CurrencyCode),
		CreatedAt:    m.CreatedAt,
		UpdatedAt:    m.UpdatedAt,
	}
}

// toPromotionRuleDTO turns the rule into the response body.
func toPromotionRuleDTO(rule models.PromotionRule) promotionRuleDTO {
	values := rule.Values
	if values == nil {
		values = []string{}
	}
	return promotionRuleDTO{
		ID:          rule.ID,
		PromotionID: rule.PromotionID,
		RuleType:    string(rule.RuleType),
		Attribute:   rule.Attribute,
		Operator:    string(rule.Operator),
		Values:      values,
		CreatedAt:   rule.CreatedAt,
		UpdatedAt:   rule.UpdatedAt,
	}
}

// toPromotionRuleDTOs turns the rule list into response bodies.
func toPromotionRuleDTOs(rules []models.PromotionRule) []promotionRuleDTO {
	out := make([]promotionRuleDTO, 0, len(rules))
	for i := range rules {
		out = append(out, toPromotionRuleDTO(rules[i]))
	}
	return out
}

// toRedemptionDTO turns the redemption record into the response body.
func toRedemptionDTO(r models.Redemption) redemptionDTO {
	return redemptionDTO{
		ID:           r.ID,
		PromotionID:  r.PromotionID,
		CampaignID:   r.CampaignID,
		Reference:    r.Reference,
		Amount:       r.Amount,
		CurrencyCode: r.CurrencyCode,
		BudgetDelta:  r.BudgetDelta,
		CreatedAt:    r.CreatedAt,
		ReleasedAt:   r.ReleasedAt,
	}
}

// toComputeResultDTO turns the computation result into the response body.
func toComputeResultDTO(result service.ComputeResult) computeResultDTO {
	out := computeResultDTO{
		CurrencyCode:          result.CurrencyCode,
		Items:                 make([]lineDiscountDTO, 0, len(result.Items)),
		ShippingMethods:       make([]lineDiscountDTO, 0, len(result.ShippingMethods)),
		ItemsDiscountTotal:    result.ItemsDiscountTotal,
		ShippingDiscountTotal: result.ShippingDiscountTotal,
		DiscountTotal:         result.DiscountTotal,
		Applied:               make([]appliedPromotionDTO, 0, len(result.Applied)),
		Skipped:               make([]skippedPromotionDTO, 0, len(result.Skipped)),
		UnmatchedCodes:        result.UnmatchedCodes,
	}
	for i := range result.Skipped {
		out.Skipped = append(out.Skipped, skippedPromotionDTO{
			PromotionID: result.Skipped[i].PromotionID,
			Code:        result.Skipped[i].Code,
			Reason:      string(result.Skipped[i].Reason),
		})
	}
	if out.UnmatchedCodes == nil {
		out.UnmatchedCodes = []string{}
	}
	for i := range result.Items {
		out.Items = append(out.Items, lineDiscountDTO{
			ID:     result.Items[i].ID,
			Amount: result.Items[i].Amount,
		})
	}
	for i := range result.ShippingMethods {
		out.ShippingMethods = append(out.ShippingMethods, lineDiscountDTO{
			ID:     result.ShippingMethods[i].ID,
			Amount: result.ShippingMethods[i].Amount,
		})
	}
	for i := range result.Applied {
		out.Applied = append(out.Applied, appliedPromotionDTO{
			PromotionID: result.Applied[i].PromotionID,
			Code:        result.Applied[i].Code,
			IsAutomatic: result.Applied[i].IsAutomatic,
			Amount:      result.Applied[i].Amount,
		})
	}
	return out
}

// toStoreCouponDTO turns the coupon into the CUSTOMER body.
func toStoreCouponDTO(c service.StoreCoupon) storeCouponDTO {
	return storeCouponDTO{
		Code:         c.Code,
		Type:         string(c.MethodType),
		TargetType:   string(c.TargetType),
		Value:        c.Value,
		CurrencyCode: stringOrNil(c.CurrencyCode),
	}
}

// stringOrNil turns the empty string into a JSON null.
//
// The distinction is meaningful: on a percentage discount there IS NO currency, and
// null appearing instead of an empty string prevents the confusion between "there is
// no currency" and "the currency is empty".
func stringOrNil(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
