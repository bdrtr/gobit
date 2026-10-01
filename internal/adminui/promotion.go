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
)

// The promotion's page (ADR 0313): what it gives, to whom, under which
// campaign, and its latest uses, read through the promotion module's panel
// surface under promotion:read.

// PromotionPath is one promotion's page.
const PromotionPath = PromotionsPath + "/{id}"

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
	}
	if m := page.Method; m != nil {
		gives := percentText(m.Value) + "% off"
		if m.Type == "fixed" {
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

	u.templates.render(w, r, http.StatusOK, "promotion.gohtml", data)
}

// percentText prints basis points as a percentage without trailing zeros:
// 1000 is "10", 1250 is "12.5".
func percentText(basisPoints int64) string {
	text, _ := formatAmount(basisPoints, "%", map[string]int{"%": 2})

	return strings.TrimSuffix(strings.TrimRight(text, "0"), ".")
}
