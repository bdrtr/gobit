package adminui

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// The promotion's page (ADR 0313): what it gives, to whom, under which
// campaign, and its latest uses, read through the promotion module's panel
// surface under promotion:read.

// PromotionPath is one promotion's page; PromotionRulesPath adds a rule to
// it and PromotionRuleRemovePath removes one (ADR 0315).
const (
	PromotionPath           = PromotionsPath + "/{id}"
	PromotionRulesPath      = PromotionPath + "/rules"
	PromotionRuleRemovePath = PromotionRulesPath + "/{ruleID}/remove"
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
	Campaign *struct {
		Name               string     `json:"name"`
		StartsAt           *time.Time `json:"starts_at"`
		EndsAt             *time.Time `json:"ends_at"`
		BudgetType         string     `json:"budget_type"`
		BudgetLimit        *int64     `json:"budget_limit"`
		BudgetUsed         int64      `json:"budget_used"`
		BudgetCurrencyCode string     `json:"budget_currency_code"`
	} `json:"campaign"`
	Method *struct {
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
		var chosen []string
		for _, category := range r.PostForm[formCategory] {
			if category = strings.TrimSpace(category); category != "" {
				chosen = append(chosen, category)
			}
		}
		if len(chosen) == 0 {
			return errors.Invalid(codeNoCategory, "Choose at least one category.")
		}

		return editor.AddPromotionRule(ctx, promotionID, ruleTypeTarget, RuleAttributeCategoryTree, ruleOpAnyIn, chosen)
	})
}

// removeRule removes the rule in the path from the promotion and returns to
// its page (ADR 0315).
func (u *UI) removeRule(w http.ResponseWriter, r *http.Request) {
	u.ruleWrite(w, r, func(ctx context.Context, editor RuleEditor, promotionID string) error {
		return editor.RemovePromotionRule(ctx, promotionID, chi.URLParam(r, "ruleID"))
	})
}

// codeNoCategory refuses the category form submitted with none chosen.
const codeNoCategory = "adminui_no_category"

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
	err := write(r.Context(), editor, id)
	switch {
	case err == nil:
		corehttp.WriteRedirect(r.Context(), w, PromotionsPath+"/"+id)
	case errors.IsInvalid(err) || errors.IsNotFound(err) || errors.IsConflict(err):
		u.renderPromotion(w, r, http.StatusUnprocessableEntity, messageFor(err))
	default:
		u.unexpectedFailure(w, r, err, "The rule could not be written")
	}
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
	money := func(minor int64, currency string) string {
		text, exact := formatAmount(minor, currency, scales)
		if !exact {
			return text + " " + currency + " (minor units)"
		}
		return text + " " + currency
	}

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
	if c := page.Campaign; c != nil && c.BudgetLimit != nil {
		switch c.BudgetType {
		case "usage":
			data["Budget"] = strconv.FormatInt(c.BudgetUsed, 10) + " of " + strconv.FormatInt(*c.BudgetLimit, 10) + " uses"
		case "spend":
			data["Budget"] = money(c.BudgetUsed, c.BudgetCurrencyCode) + " of " + money(*c.BudgetLimit, c.BudgetCurrencyCode)
		}
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
	rules := make([]ruleView, 0, len(page.Rules))
	for _, rule := range page.Rules {
		values := rule.Values
		if rule.Attribute == RuleAttributeCategoryTree {
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

	u.templates.render(w, r, code, "promotion.gohtml", data)
}

// percentText prints basis points as a percentage without trailing zeros:
// 1000 is "10", 1250 is "12.5".
func percentText(basisPoints int64) string {
	text, _ := formatAmount(basisPoints, "%", map[string]int{"%": 2})

	return strings.TrimSuffix(strings.TrimRight(text, "0"), ".")
}
