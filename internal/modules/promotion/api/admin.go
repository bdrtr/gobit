package api

import (
	"net/http"
	"time"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/service"
)

// campaignRequest is the campaign create/update body.
type campaignRequest struct {
	// Name is the campaign's display name.
	Name string `json:"name"`
	// CampaignIdentifier is the unique business identifier.
	CampaignIdentifier string `json:"campaign_identifier"`
	// Description is the description.
	Description string `json:"description"`
	// StartsAt is the start of the validity window.
	StartsAt *time.Time `json:"starts_at"`
	// EndsAt is the end of the validity window.
	EndsAt *time.Time `json:"ends_at"`
	// BudgetType is the budget's unit of measure (none | spend | usage).
	BudgetType string `json:"budget_type"`
	// BudgetLimit is the budget's upper bound.
	BudgetLimit *int64 `json:"budget_limit"`
	// BudgetCurrencyCode is the currency of a "spend" budget.
	BudgetCurrencyCode string `json:"budget_currency_code"`
}

// toCampaignInput turns the body into the service input.
func (r campaignRequest) toCampaignInput() service.CampaignInput {
	return service.CampaignInput{
		Name:               r.Name,
		CampaignIdentifier: r.CampaignIdentifier,
		Description:        r.Description,
		StartsAt:           r.StartsAt,
		EndsAt:             r.EndsAt,
		BudgetType:         models.CampaignBudgetType(r.BudgetType),
		BudgetLimit:        r.BudgetLimit,
		BudgetCurrencyCode: r.BudgetCurrencyCode,
	}
}

// createCampaign creates a new campaign (POST /admin/v1/campaigns).
func (a *API) createCampaign(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req campaignRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	campaign, err := a.svc.CreateCampaign(ctx, req.toCampaignInput())
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toCampaignDTO(campaign))
}

// listCampaigns lists the campaigns, paginated (GET /admin/v1/campaigns).
func (a *API) listCampaigns(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, offset, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	page, err := a.svc.ListCampaigns(ctx, limit, offset)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writePage(w, r, page, toCampaignDTO)
}

// getCampaign returns a single campaign (GET /admin/v1/campaigns/{id}).
func (a *API) getCampaign(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	campaign, err := a.svc.GetCampaign(ctx, pathID(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toCampaignDTO(campaign))
}

// updateCampaign replaces the campaign's definition
// (PUT /admin/v1/campaigns/{id}).
//
// The budget COUNTER does not change through this path; only the redemption
// flow writes it.
func (a *API) updateCampaign(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req campaignRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	campaign, err := a.svc.UpdateCampaign(ctx, pathID(r, "id"), req.toCampaignInput())
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toCampaignDTO(campaign))
}

// deleteCampaign deletes the campaign with a soft delete
// (DELETE /admin/v1/campaigns/{id}).
func (a *API) deleteCampaign(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := a.svc.DeleteCampaign(ctx, pathID(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// promotionRequest is the promotion create/update body.
type promotionRequest struct {
	// Code is the coupon code.
	Code string `json:"code"`
	// IsAutomatic is whether the promotion is applied without a code.
	IsAutomatic bool `json:"is_automatic"`
	// Type is the promotion's mechanic (standard | buyget).
	Type string `json:"type"`
	// CampaignID ties the promotion to a campaign.
	CampaignID *string `json:"campaign_id"`
	// Status is the publication status (draft | active | inactive).
	Status string `json:"status"`
	// UsageLimit is the usage limit.
	UsageLimit *int64 `json:"usage_limit"`
	// Metadata is the operator's free-form note.
	Metadata map[string]string `json:"metadata"`
}

// toPromotionInput turns the body into the service input.
func (r promotionRequest) toPromotionInput() service.PromotionInput {
	return service.PromotionInput{
		Code:        r.Code,
		IsAutomatic: r.IsAutomatic,
		Type:        models.PromotionType(r.Type),
		CampaignID:  r.CampaignID,
		Status:      models.PromotionStatus(r.Status),
		UsageLimit:  r.UsageLimit,
		Metadata:    r.Metadata,
	}
}

// createPromotion creates a new promotion (POST /admin/v1/promotions).
func (a *API) createPromotion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req promotionRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	promo, err := a.svc.CreatePromotion(ctx, req.toPromotionInput())
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toPromotionDTO(promo))
}

// listPromotions lists the promotions, paginated
// (GET /admin/v1/promotions).
//
// It can be filtered with the "status" and "campaign_id" query parameters.
func (a *API) listPromotions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, offset, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	in := service.ListPromotionsInput{
		CampaignID: stringParam(r, "campaign_id"),
		Limit:      limit,
		Offset:     offset,
	}
	if raw := stringParam(r, "status"); raw != nil {
		status := models.PromotionStatus(*raw)
		in.Status = &status
	}

	page, err := a.svc.ListPromotions(ctx, in)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writePage(w, r, page, toPromotionDTO)
}

// getPromotion returns a single promotion (GET /admin/v1/promotions/{id}).
func (a *API) getPromotion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	promo, err := a.svc.GetPromotion(ctx, pathID(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toPromotionDTO(promo))
}

// updatePromotion replaces the promotion's definition
// (PUT /admin/v1/promotions/{id}).
func (a *API) updatePromotion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req promotionRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	promo, err := a.svc.UpdatePromotion(ctx, pathID(r, "id"), req.toPromotionInput())
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toPromotionDTO(promo))
}

// deletePromotion deletes the promotion with a soft delete
// (DELETE /admin/v1/promotions/{id}).
func (a *API) deletePromotion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := a.svc.DeletePromotion(ctx, pathID(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// applicationMethodRequest is the application method write body.
type applicationMethodRequest struct {
	// Type is the discount's measure (fixed | percentage).
	Type string `json:"type"`
	// TargetType is the discount's target (items | shipping_methods | order).
	TargetType string `json:"target_type"`
	// Allocation is the allocation mode (each | across).
	Allocation string `json:"allocation"`
	// Value is a fixed amount (minor unit) or basis points.
	Value int64 `json:"value"`
	// MaxQuantity is the maximum quantity the fixed amount is applied to.
	MaxQuantity *int64 `json:"max_quantity"`
	// BuyQuantity is the quantity that has to be bought to earn the reward.
	BuyQuantity *int64 `json:"buy_quantity"`
	// ApplyToQuantity is the quantity the reward lands on; it is given TOGETHER
	// with the buy quantity.
	ApplyToQuantity *int64 `json:"apply_to_quantity"`
	// CurrencyCode is the currency of a fixed-amount discount.
	CurrencyCode string `json:"currency_code"`
}

// setApplicationMethod writes the promotion's application method
// (PUT /admin/v1/promotions/{id}/application-method).
//
// It is a replacement: if the promotion already has a method, it is
// overwritten.
func (a *API) setApplicationMethod(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req applicationMethodRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	method, err := a.svc.SetApplicationMethod(ctx, pathID(r, "id"), service.ApplicationMethodInput{
		Type:            models.ApplicationMethodType(req.Type),
		TargetType:      models.ApplicationTargetType(req.TargetType),
		Allocation:      models.Allocation(req.Allocation),
		Value:           req.Value,
		MaxQuantity:     req.MaxQuantity,
		BuyQuantity:     req.BuyQuantity,
		ApplyToQuantity: req.ApplyToQuantity,
		CurrencyCode:    req.CurrencyCode,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toApplicationMethodDTO(method))
}

// deleteApplicationMethod deletes the method with a soft delete
// (DELETE /admin/v1/promotions/{id}/application-method).
func (a *API) deleteApplicationMethod(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := a.svc.DeleteApplicationMethod(ctx, pathID(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// promotionRuleRequest is the rule add body.
type promotionRuleRequest struct {
	// RuleType is what the rule looks at (context | target).
	RuleType string `json:"rule_type"`
	// Attribute is the name of the field to look at.
	Attribute string `json:"attribute"`
	// Operator is the comparison operator.
	Operator string `json:"operator"`
	// Values is the right-hand side of the comparison.
	Values []string `json:"values"`
}

// listPromotionRules returns a promotion's rules
// (GET /admin/v1/promotions/{id}/rules).
func (a *API) listPromotionRules(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rules, err := a.svc.ListPromotionRules(ctx, pathID(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItems(w, r, toPromotionRuleDTOs(rules))
}

// createPromotionRule adds a rule to a promotion
// (POST /admin/v1/promotions/{id}/rules).
func (a *API) createPromotionRule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req promotionRuleRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	rule, err := a.svc.AddPromotionRule(ctx, pathID(r, "id"), service.RuleInput{
		RuleType:  models.RuleType(req.RuleType),
		Attribute: req.Attribute,
		Operator:  models.RuleOperator(req.Operator),
		Values:    req.Values,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toPromotionRuleDTO(rule))
}

// deletePromotionRule deletes the rule with a soft delete
// (DELETE /admin/v1/promotion-rules/{id}).
func (a *API) deletePromotionRule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := a.svc.DeletePromotionRule(ctx, pathID(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// listRedemptions returns a promotion's redemption ledger
// (GET /admin/v1/promotions/{id}/redemptions).
func (a *API) listRedemptions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, offset, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	page, err := a.svc.ListRedemptions(ctx, pathID(r, "id"), limit, offset)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writePage(w, r, page, toRedemptionDTO)
}

// redeemRequest is the redemption write body.
type redeemRequest struct {
	// Reference is the redemption's business record reference; it is the
	// idempotency key.
	Reference string `json:"reference"`
	// Amount is the applied discount amount (minor unit).
	Amount int64 `json:"amount"`
	// CurrencyCode is the discount's currency.
	CurrencyCode string `json:"currency_code"`
}

// redeemPromotion redeems the promotion for a reference
// (POST /admin/v1/promotions/{id}/redeem).
//
// It is IDEMPOTENT: a second request with the same reference does not increment
// the counter and returns the existing record. That is why the response is 200
// and not 201 — the request does NOT always CREATE a new record.
//
// It returns 409 if the promotion is draft/inactive, if its campaign's window
// is closed or if a counter limit would be exceeded; the full list of reasons
// is in the [service.Service.RedeemPromotion] godoc. Being the admin surface
// does NOT LOOSEN these checks: the counter and the budget feed the same
// ledger.
func (a *API) redeemPromotion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req redeemRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	redemption, err := a.svc.RedeemPromotion(ctx, service.RedeemInput{
		PromotionID:  pathID(r, "id"),
		Reference:    req.Reference,
		Amount:       req.Amount,
		CurrencyCode: req.CurrencyCode,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toRedemptionDTO(redemption))
}

// releaseRequest is the redemption release body.
type releaseRequest struct {
	// Reference is the reference of the redemption to release.
	Reference string `json:"reference"`
}

// releaseResultDTO is the body of the release response.
type releaseResultDTO struct {
	// Released reports whether something was released IN THIS REQUEST.
	//
	// false does NOT MEAN the request failed: the compensation is idempotent
	// and a second call for an already released redemption returns no error.
	Released bool `json:"released"`
}

// releasePromotion releases a redemption
// (POST /admin/v1/promotions/{id}/release).
//
// It is IDEMPOTENT: a second call returns no error and the counters do not go
// down a second time.
func (a *API) releasePromotion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req releaseRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	released, err := a.svc.ReleasePromotion(ctx, service.ReleaseInput{
		PromotionID: pathID(r, "id"),
		Reference:   req.Reference,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, releaseResultDTO{Released: released})
}

// computeRequest is the discount computation body.
//
// The field names are EXACTLY the same as the interop schema's (see interop.go
// in the service package); two surfaces asking for the same computation under
// different names would mean a request the operator tried on the admin screen
// behaving differently in the cart flow.
type computeRequest struct {
	// CurrencyCode is the cart's currency.
	CurrencyCode string `json:"currency_code"`
	// Context holds the fields the context rules look at.
	Context map[string]string `json:"context"`
	// ContextLists is what a context rule reads as a SET (ADR 0144).
	//
	// The shape has to stay IDENTICAL to the interop request's, by that file's own
	// rule: two surfaces computing the same thing from two schemas is two chances
	// for one of them to drift. Only the "any_in" operator looks here.
	ContextLists map[string][]string `json:"context_lists"`
	// Items are the cart's lines.
	Items []computeItemRequest `json:"items"`
	// ShippingMethods are the cart's shipping methods.
	ShippingMethods []computeShippingRequest `json:"shipping_methods"`
	// Codes are the coupon codes to apply.
	Codes []string `json:"codes"`
	// At is the moment of the computation; "now" when empty.
	At *time.Time `json:"at"`
}

// computeItemRequest is the body of a single line in the computation.
type computeItemRequest struct {
	// ID is the line's id.
	ID string `json:"id"`
	// Amount is the line's subtotal (unit × quantity), in minor units.
	Amount int64 `json:"amount"`
	// UnitAmount is the line's unit price; it is REQUIRED and
	// UnitAmount × Quantity = Amount has to hold.
	UnitAmount int64 `json:"unit_amount"`
	// Quantity is the line's quantity.
	Quantity int64 `json:"quantity"`
	// Attributes are the attributes the target rules look at.
	Attributes map[string]string `json:"attributes"`
	// Lists is what a TARGET rule reads as a SET (ADR 0148): the line's product's
	// `category_ids` and `tag_ids`.
	//
	// It is here for [computeRequest.ContextLists]'s reason, and the cost of
	// leaving it out would have been concrete: an operator trying a category rule
	// on this endpoint would be told it discounts nothing, while the same rule
	// discounts the same cart in the shop.
	Lists map[string][]string `json:"lists"`
}

// computeShippingRequest is the body of a single shipping method in the
// computation.
type computeShippingRequest struct {
	// ID is the shipping method's id.
	ID string `json:"id"`
	// Amount is the shipping amount (minor unit).
	Amount int64 `json:"amount"`
	// Attributes are the attributes the target rules look at.
	Attributes map[string]string `json:"attributes"`
}

// computeDiscounts computes the discounts for the given cart context
// (POST /admin/v1/promotions/compute).
//
// It has NO SIDE EFFECTS: no counter changes. The endpoint is on the admin side
// because its body returns the promotions' IDS and codes; its counterpart on
// the customer side is the cart flow, which writes the discount into the cart
// total.
//
// It calls [service.Service.ExplainDiscounts], not ComputeDiscounts: the only
// difference is the candidate read, and the discount amounts are exactly the
// same. What it adds is `skipped` — WHY a promotion did not apply — and that
// answer is published only here, because handed to the customer it would let
// someone guessing codes read off a campaign calendar (ADR 0110).
func (a *API) computeDiscounts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req computeRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	in := service.ComputeInput{
		CurrencyCode:    req.CurrencyCode,
		Context:         req.Context,
		ContextLists:    req.ContextLists,
		Items:           make([]service.ComputeItem, 0, len(req.Items)),
		ShippingMethods: make([]service.ComputeShippingMethod, 0, len(req.ShippingMethods)),
		Codes:           req.Codes,
	}
	if req.At != nil {
		in.At = *req.At
	}
	for i := range req.Items {
		in.Items = append(in.Items, service.ComputeItem{
			ID:         req.Items[i].ID,
			Amount:     req.Items[i].Amount,
			UnitAmount: req.Items[i].UnitAmount,
			Quantity:   req.Items[i].Quantity,
			Attributes: req.Items[i].Attributes,
			Lists:      req.Items[i].Lists,
		})
	}
	for i := range req.ShippingMethods {
		in.ShippingMethods = append(in.ShippingMethods, service.ComputeShippingMethod{
			ID:         req.ShippingMethods[i].ID,
			Amount:     req.ShippingMethods[i].Amount,
			Attributes: req.ShippingMethods[i].Attributes,
		})
	}

	result, err := a.svc.ExplainDiscounts(ctx, in)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toComputeResultDTO(result))
}
