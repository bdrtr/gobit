package service

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// A variant's prices on price lists, as the panel reads and writes them (ADR
// 0327). The read provider leaves out a price with rules and one on a list
// that is not live, so the panel reads them here, every one of the set's list
// prices with its rules; and it writes one by reading every price on the set
// under its lock and writing them back with the one added or removed, as the
// base price's form does (ADR 0280).

// CodeListPriceTaken refuses a list price the set already holds: the same
// list, currency and customers at one unit; CodePriceNotFound a price the set
// does not hold.
const (
	CodeListPriceTaken = "pricing_list_price_taken"
	CodePriceNotFound  = "pricing_price_not_found"
)

// adminRule is one condition of a price as the panel shows it.
type adminRule struct {
	Attribute string   `json:"attribute"`
	Operator  string   `json:"operator"`
	Values    []string `json:"values"`
}

// adminListPrice is one of a set's list prices as the panel lists it; the
// json tags are the contract with the panel, which cannot import this
// package.
type adminListPrice struct {
	ID          string      `json:"id"`
	PriceListID string      `json:"price_list_id"`
	ListTitle   string      `json:"price_list_title"`
	Currency    string      `json:"currency_code"`
	Amount      int64       `json:"amount"`
	MinQuantity int32       `json:"min_quantity"`
	MaxQuantity *int32      `json:"max_quantity"`
	Rules       []adminRule `json:"rules"`
}

// ListPricesJSON lists the set's prices on price lists with their list's
// title and their rules (ADR 0327), every one, a draft list's and a ruled one
// included.
func (a *AdminSurface) ListPricesJSON(ctx context.Context, priceSetID string) (json.RawMessage, error) {
	prices, err := a.svc.ListPrices(ctx, priceSetID)
	if err != nil {
		return nil, err
	}
	titles := map[string]string{}
	out := make([]adminListPrice, 0, len(prices))
	for i := range prices {
		p := &prices[i]
		if p.PriceListID == nil {
			continue
		}
		listID := *p.PriceListID
		if _, read := titles[listID]; !read {
			titles[listID] = ""
			if list, listErr := a.svc.GetPriceList(ctx, listID); listErr == nil {
				titles[listID] = list.Title
			} else if !errors.IsNotFound(listErr) {
				return nil, listErr
			}
		}
		rules := make([]adminRule, 0, len(p.Rules))
		for j := range p.Rules {
			rule := &p.Rules[j]
			rules = append(rules, adminRule{Attribute: rule.Attribute, Operator: string(rule.Operator), Values: rule.Values})
		}
		out = append(out, adminListPrice{
			ID: p.ID, PriceListID: listID, ListTitle: titles[listID], Currency: p.CurrencyCode,
			Amount: p.Amount, MinQuantity: p.MinQuantity, MaxQuantity: p.MaxQuantity, Rules: rules,
		})
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, codeAdminEncodeFailed,
			"the list prices could not be encoded")
	}

	return body, nil
}

// AddListPrice adds a price at one unit and up to the set on the price list,
// for every customer or, when groups are named, for a customer whose group
// ranking first is one of them (ADR 0327); the set's other prices are kept.
// The same list, currency and customers twice is refused.
func (a *AdminSurface) AddListPrice(
	ctx context.Context, priceSetID, priceListID, currencyCode string, amount int64, groupIDs []string,
) error {
	if err := a.svc.ready(); err != nil {
		return err
	}
	if _, err := a.svc.GetPriceList(ctx, priceListID); err != nil {
		return err
	}
	currency := strings.ToUpper(strings.TrimSpace(currencyCode))
	var rules []RuleInput
	if len(groupIDs) > 0 {
		rules = []RuleInput{{Attribute: models.AttrCustomerGroupID, Operator: models.OpIn, Values: slices.Sorted(slices.Values(groupIDs))}}
	}

	return a.revise(ctx, priceSetID, "", func(inputs []PriceInput) ([]PriceInput, error) {
		for i := range inputs {
			in := &inputs[i]
			if in.PriceListID != nil && *in.PriceListID == priceListID && in.CurrencyCode == currency &&
				in.MinQuantity == models.MinQuantity && sameRules(in.Rules, rules) {
				return nil, errors.Conflict(CodeListPriceTaken,
					"the variant already has a price on price list %s in %s for those customers", priceListID, currency)
			}
		}

		return append(inputs, PriceInput{
			CurrencyCode: currency, Amount: amount, MinQuantity: models.MinQuantity,
			PriceListID: &priceListID, Rules: rules,
		}), nil
	})
}

// RemoveListPrice removes one of the set's list prices and keeps the others;
// a base price is not removed here, since the variant would be left with
// nothing to sell at (ADR 0327).
func (a *AdminSurface) RemoveListPrice(ctx context.Context, priceSetID, priceID string) error {
	if err := a.svc.ready(); err != nil {
		return err
	}

	return a.revise(ctx, priceSetID, priceID, nil)
}

// revise writes the set's prices back under the set's lock, without the
// price named by removeID, which must be a list price, or with change applied
// when none is named.
func (a *AdminSurface) revise(
	ctx context.Context, priceSetID, removeID string, change func([]PriceInput) ([]PriceInput, error),
) error {
	_, err := a.svc.repo.RevisePrices(ctx, priceSetID, func(existing []models.Price) ([]models.Price, bool, error) {
		inputs := make([]PriceInput, 0, len(existing)+1)
		removed := false
		for i := range existing {
			if removeID != "" && existing[i].ID == removeID {
				if existing[i].PriceListID == nil {
					return nil, false, errors.Invalid(CodeInvalidInput,
						"price %s is a base price; it is changed on the variant's page, not removed", existing[i].ID)
				}
				removed = true
				continue
			}
			inputs = append(inputs, PriceInput{
				CurrencyCode: existing[i].CurrencyCode, Amount: existing[i].Amount,
				MinQuantity: existing[i].MinQuantity, MaxQuantity: existing[i].MaxQuantity,
				PriceListID: existing[i].PriceListID, Rules: ruleInputs(existing[i].Rules),
			})
		}
		if removeID != "" && !removed {
			return nil, false, errors.NotFound(CodePriceNotFound, "price %s is not on price set %s", removeID, priceSetID)
		}
		if change != nil {
			var err error
			if inputs, err = change(inputs); err != nil {
				return nil, false, err
			}
		}
		built, err := a.svc.buildPrices(priceSetID, inputs, a.svc.clock())
		if err != nil {
			return nil, false, err
		}

		return built, true, nil
	}, a.svc.clock)

	return err
}

// sameRules reports whether two rule sets name the same conditions.
func sameRules(a, b []RuleInput) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Attribute != b[i].Attribute || a[i].Operator != b[i].Operator ||
			!slices.Equal(slices.Sorted(slices.Values(a[i].Values)), slices.Sorted(slices.Values(b[i].Values))) {
			return false
		}
	}

	return true
}
