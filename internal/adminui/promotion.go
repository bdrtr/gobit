package adminui

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// The promotion's page (ADR 0313): what it gives, to whom, under which
// campaign, and its latest uses, read through the promotion module's panel
// surface under promotion:read.

// PromotionPath is one promotion's page; PromotionRulesPath adds a category
// rule to it and PromotionRuleRemovePath removes a rule (ADR 0315),
// PromotionCampaignPath puts it into a campaign (ADR 0320), and
// PromotionGroupRulesPath adds a customer group rule (ADR 0321).
const (
	PromotionPath           = PromotionsPath + "/{id}"
	PromotionRulesPath      = PromotionPath + "/rules"
	PromotionRuleRemovePath = PromotionRulesPath + "/{ruleID}/remove"
	PromotionCampaignPath   = PromotionPath + "/campaign"
	PromotionGroupRulesPath = PromotionRulesPath + "/customer-groups"
)

// RuleAttributeCategoryTree is the line attribute the cart flow fills with a
// product's categories and their ancestors, spelled by hand and pinned
// against the cart flow's constant in internal/arch (ADR 0315).
const RuleAttributeCategoryTree = "category_tree_ids"

// The rule the category form writes: a target rule, so it chooses the lines
// the discount lands on, matching when a line's categories meet the chosen.
const (
	ruleTypeTarget = "target"
	ruleOpAnyIn    = "any_in"
)

// formCategory is the category form's field, one value per category chosen.
const formCategory = "category"

// RuleAttributeCustomerGroup is the context attribute the cart flow fills
// with the customer's groups, the whole set in its list (ADR 0144), spelled by
// hand and pinned against the cart flow's constant in internal/arch (ADR
// 0321).
const RuleAttributeCustomerGroup = "customer_group_id"

// ruleTypeContext is the rule the group form writes: a context rule, so it
// decides whether the promotion applies to the cart at all.
const ruleTypeContext = "context"

// formGroup is the group form's field, one value per group chosen.
const formGroup = "customer_group"

// RuleEditor is the narrow surface a promotion's rules are written through
// (ADR 0315).
type RuleEditor interface {
	// AddPromotionRule adds a rule to the promotion.
	AddPromotionRule(ctx context.Context, promotionID, ruleType, attribute, operator string, values []string) error
	// RemovePromotionRule removes one of the promotion's rules.
	RemovePromotionRule(ctx context.Context, promotionID, ruleID string) error
}

// canEditRules reports whether the operator may write a promotion's rules.
func (u *UI) canEditRules(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, ok := u.promotions.(RuleEditor)

	return ok && principal.HasScope(scopePromotionWrite)
}

// PromotionReader is the narrow surface a promotion's page reads through.
type PromotionReader interface {
	// PromotionJSON reads one promotion with its discount, rules, campaign
	// and latest uses.
	PromotionJSON(ctx context.Context, id string) (json.RawMessage, error)
}

// promotionPage is one promotion as the module's surface sends it; the json
// tags are the contract with that surface, exercised end to end.
type promotionPage struct {
	promotionRow
	// CampaignID is the promotion's own reference, kept when the campaign it
	// names was deleted and Campaign is null (D205).
	CampaignID *string      `json:"campaign_id"`
	Campaign   *campaignRow `json:"campaign"`
	Method     *struct {
		Type            string `json:"type"`
		TargetType      string `json:"target_type"`
		Allocation      string `json:"allocation"`
		Value           int64  `json:"value"`
		MaxQuantity     *int64 `json:"max_quantity"`
		BuyQuantity     *int64 `json:"buy_quantity"`
		ApplyToQuantity *int64 `json:"apply_to_quantity"`
		CurrencyCode    string `json:"currency_code"`
	} `json:"application_method"`
	Rules []struct {
		ID        string   `json:"id"`
		Type      string   `json:"type"`
		Attribute string   `json:"attribute"`
		Operator  string   `json:"operator"`
		Values    []string `json:"values"`
	} `json:"rules"`
	Uses []struct {
		Reference    string     `json:"reference"`
		Amount       int64      `json:"amount"`
		CurrencyCode string     `json:"currency_code"`
		CreatedAt    time.Time  `json:"created_at"`
		ReleasedAt   *time.Time `json:"released_at"`
	} `json:"latest_uses"`
}

// promotionTargets names what a discount lands on, in the module's words.
var promotionTargets = map[string]string{
	"items":            "the items",
	"shipping_methods": "the shipping",
	"order":            "the order",
}

// showPromotion renders the promotion in the path.
func (u *UI) showPromotion(w http.ResponseWriter, r *http.Request) {
	u.renderPromotion(w, r, http.StatusOK, "")
}

// addCategoryRule limits the promotion's discount to the items of the chosen
// categories and returns to its page (ADR 0315): one target rule, matching a
// line whose product is filed under any of them or under one of their
// descendants. A refusal is drawn on the page.
func (u *UI) addCategoryRule(w http.ResponseWriter, r *http.Request) {
	u.ruleWrite(w, r, func(ctx context.Context, editor RuleEditor, promotionID string) error {
		chosen := chosenValues(r.PostForm[formCategory])
		if len(chosen) == 0 {
			return errors.Invalid(codeNoCategory, "Choose at least one category.")
		}

		return editor.AddPromotionRule(ctx, promotionID, ruleTypeTarget, RuleAttributeCategoryTree, ruleOpAnyIn, chosen)
	})
}

// addGroupRule limits the promotion to the customers in any of the chosen
// groups and returns to its page (ADR 0321): one context rule, matching a cart
// whose customer is in one of them, whichever of their groups ranks first. A
// guest's cart carries no group and does not match.
func (u *UI) addGroupRule(w http.ResponseWriter, r *http.Request) {
	u.ruleWrite(w, r, func(ctx context.Context, editor RuleEditor, promotionID string) error {
		chosen := chosenValues(r.PostForm[formGroup])
		if len(chosen) == 0 {
			return errors.Invalid(codeNoGroup, "Choose at least one customer group.")
		}

		return editor.AddPromotionRule(ctx, promotionID, ruleTypeContext, RuleAttributeCustomerGroup, ruleOpAnyIn, chosen)
	})
}

// chosenValues is a multiple choice's values, trimmed, the empty ones left
// out.
func chosenValues(values []string) []string {
	var chosen []string
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			chosen = append(chosen, value)
		}
	}

	return chosen
}

// removeRule removes the rule in the path from the promotion and returns to
// its page (ADR 0315).
func (u *UI) removeRule(w http.ResponseWriter, r *http.Request) {
	u.ruleWrite(w, r, func(ctx context.Context, editor RuleEditor, promotionID string) error {
		return editor.RemovePromotionRule(ctx, promotionID, chi.URLParam(r, "ruleID"))
	})
}

// codeNoCategory refuses the category form submitted with none chosen, and
// codeNoGroup the group form.
const (
	codeNoCategory = "adminui_no_category"
	codeNoGroup    = "adminui_no_customer_group"
)

// ruleWrite runs one of the page's rule writes and returns to the page; a
// refusal is drawn on it.
func (u *UI) ruleWrite(
	w http.ResponseWriter, r *http.Request, write func(ctx context.Context, editor RuleEditor, promotionID string) error,
) {
	editor, ok := u.promotions.(RuleEditor)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Promotions unavailable",
			"The promotion module's panel surface cannot write a rule in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	id := chi.URLParam(r, "id")
	u.backToPromotion(w, r, id, write(r.Context(), editor, id), "The rule could not be written")
}

// backToPromotion answers one of the page's writes: the page again when it
// was made, the refusal drawn on the page, anything else a failure.
func (u *UI) backToPromotion(w http.ResponseWriter, r *http.Request, id string, err error, failure string) {
	switch {
	case err == nil:
		corehttp.WriteRedirect(r.Context(), w, PromotionsPath+"/"+id)
	case errors.IsInvalid(err) || errors.IsNotFound(err) || errors.IsConflict(err):
		u.renderPromotion(w, r, http.StatusUnprocessableEntity, messageFor(err))
	default:
		u.unexpectedFailure(w, r, err, failure)
	}
}

// CampaignPlacer is the narrow surface a promotion is put into a campaign
// through (ADR 0320).
type CampaignPlacer interface {
	// SetPromotionCampaign puts the promotion into the campaign to, or out of
	// any when it is empty, if it is still in the campaign from, empty for
	// none.
	SetPromotionCampaign(ctx context.Context, id, from, to string) error
}

// The campaign form's fields: the campaign the page was drawn with, and the
// one chosen.
const (
	formCampaignFrom = "from"
	formCampaignTo   = "campaign_id"
)

// campaignChoices is how many campaigns the page offers, the module's page
// ceiling.
const campaignChoices = 100

// canPlaceInCampaigns reports whether the operator may put the promotion into
// a campaign here: the surface can list the campaigns and set one, and the
// operator holds the module's write privilege.
func (u *UI) canPlaceInCampaigns(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, places := u.promotions.(CampaignPlacer)
	_, lists := u.promotions.(CampaignLister)

	return places && lists && principal.HasScope(scopePromotionWrite)
}

// placeInCampaign puts the promotion in the path into the chosen campaign, or
// out of any, from the one its page was drawn with, and returns to the page;
// a promotion moved since or a campaign gone is refused on the page (ADR
// 0320).
func (u *UI) placeInCampaign(w http.ResponseWriter, r *http.Request) {
	placer, ok := u.promotions.(CampaignPlacer)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Promotions unavailable",
			"The promotion module's panel surface cannot put a promotion into a campaign in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	id := chi.URLParam(r, "id")
	err := placer.SetPromotionCampaign(r.Context(), id,
		strings.TrimSpace(r.PostFormValue(formCampaignFrom)), strings.TrimSpace(r.PostFormValue(formCampaignTo)))
	u.backToPromotion(w, r, id, err, "The promotion's campaign could not be set")
}

// campaignOptions reads the campaigns the page offers, whether there are more,
// and whether the read failed, which leaves the page without the form.
func (u *UI) campaignOptions(r *http.Request) (options []campaignRow, more, unread bool) {
	lister, ok := u.promotions.(CampaignLister)
	if !ok {
		return nil, false, true
	}
	raw, total, err := lister.CampaignsJSON(r.Context(), campaignChoices, 0)
	if err == nil {
		err = json.Unmarshal(raw, &options)
	}
	if err != nil {
		corehttp.LoggerFromContext(r.Context()).WarnContext(r.Context(),
			"the panel could not read the campaigns to offer", "error", err)

		return nil, false, true
	}

	return options, total > int64(len(options)), false
}

// ruleView is one rule as the page prints it: a category rule names its
// categories when the operator may read them, and every rule its values.
type ruleView struct {
	ID        string
	Type      string
	Attribute string
	Operator  string
	Values    string
}

// renderPromotion reads the promotion in the path and writes its page, with
// a refused write's reason. An operator who may write and not read the
// promotion is told the reason alone (ADR 0260).
func (u *UI) renderPromotion(w http.ResponseWriter, r *http.Request, code int, refused string) {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	if refused != "" && !principal.HasScope(scopePromotionRead) {
		u.errorPage(w, r, code, "Not done", refused)
		return
	}
	reader, ok := u.promotions.(PromotionReader)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Promotions unavailable",
			"The promotion module's panel surface cannot read a promotion in this installation.")
		return
	}

	raw, err := reader.PromotionJSON(r.Context(), chi.URLParam(r, "id"))
	switch {
	case errors.IsNotFound(err) || errors.IsInvalid(err):
		u.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such promotion.")
		return
	case err != nil:
		u.unexpectedFailure(w, r, err, "The promotion could not be read")
		return
	}
	var page promotionPage
	if err := json.Unmarshal(raw, &page); err != nil {
		u.unexpectedFailure(w, r, err, "The promotion could not be read")
		return
	}

	scales := u.currencyScales(r.Context())
	money := func(minor int64, currency string) string { return minorText(minor, currency, scales) }

	title := page.Code
	if page.IsAutomatic {
		title = "Automatic promotion"
	}
	data := map[string]any{
		titleKey:    title,
		"Promotion": page,
		pathKey:     PromotionsPath,
		refusedKey:  refused,
	}
	if m := page.Method; m != nil {
		gives := percentText(m.Value) + "% off"
		if m.Type == measureFixed {
			gives = money(m.Value, m.CurrencyCode) + " off"
		}
		data["Gives"] = gives
		data["Target"] = promotionTargets[m.TargetType]
	}
	if page.Campaign != nil {
		data["Budget"] = page.Campaign.budgetText(scales)
	}
	uses := make([]map[string]any, 0, len(page.Uses))
	for _, use := range page.Uses {
		uses = append(uses, map[string]any{
			"Reference": use.Reference, "Amount": money(use.Amount, use.CurrencyCode),
			"CreatedAt": use.CreatedAt, "ReleasedAt": use.ReleasedAt,
		})
	}
	data["Uses"] = uses

	// The categories are the product module's, read only for an operator who
	// may read them (ADR 0260): to name a category rule's values, and to offer
	// the category form when the discount lands on items.
	names := map[string]string{}
	if principal.HasScope(scopeProductRead) {
		categories := u.categoryList(r.Context())
		for _, option := range categories.Options {
			names[option.ID] = option.Name
		}
		if u.canEditRules(r) && page.Method != nil && page.Method.TargetType == "items" {
			data["Categories"] = categories
		}
	}
	// The groups are the customer module's, read only for an operator who may
	// read the customers (ADR 0321), to name a group rule's values and to
	// offer the group form.
	if principal.HasScope(scopeCustomerRead) {
		groups := u.groupList(r.Context())
		for _, option := range groups.Options {
			names[option.ID] = option.Name
		}
		if u.canEditRules(r) {
			data["Groups"] = groups
		}
	}
	rules := make([]ruleView, 0, len(page.Rules))
	for _, rule := range page.Rules {
		values := rule.Values
		if rule.Attribute == RuleAttributeCategoryTree || rule.Attribute == RuleAttributeCustomerGroup {
			values = make([]string, 0, len(rule.Values))
			for _, id := range rule.Values {
				if name := names[id]; name != "" {
					id = name
				}
				values = append(values, id)
			}
		}
		rules = append(rules, ruleView{
			ID: rule.ID, Type: rule.Type, Attribute: rule.Attribute, Operator: rule.Operator,
			Values: strings.Join(values, ", "),
		})
	}
	data["Rules"] = rules
	data["CanEditRules"] = u.canEditRules(r)
	if u.canPlaceInCampaigns(r) {
		data["CanPlace"] = true
		data["CampaignChoices"], data["CampaignsMore"], data["CampaignsUnread"] = u.campaignOptions(r)
		// The promotion's own reference, not the campaign read with it: a
		// deleted campaign reads as none and the promotion still names it
		// (D205).
		current := ""
		if page.CampaignID != nil {
			current = *page.CampaignID
		}
		data["CurrentCampaign"] = current
		data["CampaignChoiceCount"] = campaignChoices
	}

	u.templates.render(w, r, code, "promotion.gohtml", data)
}

// percentText prints basis points as a percentage without trailing zeros:
// 1000 is "10", 1250 is "12.5".
func percentText(basisPoints int64) string {
	text, _ := formatAmount(basisPoints, "%", map[string]int{"%": 2})

	return strings.TrimSuffix(strings.TrimRight(text, "0"), ".")
}
