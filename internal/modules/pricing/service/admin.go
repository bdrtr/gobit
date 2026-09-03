package service

import (
	"context"
	"slices"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"

	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// This file is the pricing module's ADMIN WRITE surface (ADR 0013).
//
// It exists for one reason that is worth stating plainly: the module's only
// price writer is DESTRUCTIVE. [Service.SetPrices] replaces a price set's
// prices, deleting everything not in the input, and [Service.SetBasePrices] is
// a thin wrapper over it.
//
// An edit form built directly on either would delete data the operator never
// saw. The panel reads prices through the query provider, which returns only
// the LISTABLE ones — prices with rules and prices belonging to an unavailable
// list are filtered out. Editing one base price would therefore have silently
// removed every campaign price and every price-list entry on that set, and
// nothing in the response would have said so.
//
// This surface closes that gap by reading ALL prices, changing exactly one, and
// writing every other one back untouched.

// AdminSurface is the pricing module's admin write surface.
type AdminSurface struct{ svc *Service }

// NewAdminSurface builds the admin surface over the given service.
func NewAdminSurface(svc *Service) *AdminSurface { return &AdminSurface{svc: svc} }

// SetBasePriceAmount sets the amount of ONE base price and leaves every other
// price on the set untouched.
//
// "Base" means no price list and no rules — the price that applies when nothing
// else does. When the currency has no base price yet, one is added; the panel's
// form can then give a price to a set that only had campaign prices.
//
// # What it costs
//
// The write goes through [Service.SetPrices], so every price on the set is
// rewritten and the price IDs are REGENERATED. That is contained: a price id is
// referenced only by pricing's own price_rule rows, which are rewritten with
// it, and no other module or table holds one. It is written down because it is
// a real side effect of an edit that looks local.
//
// Two operators saving the same set at the same time is last-write-wins. There
// is no version on the form and no optimistic check underneath it; the same
// limit ADR 0013 names for the product form.
func (a *AdminSurface) SetBasePriceAmount(
	ctx context.Context, priceSetID, currencyCode string, amount int64,
) error {
	if a == nil {
		return errors.Unavailable(CodeInvalidInput, "the pricing admin surface is not set up")
	}
	if err := a.svc.ready(); err != nil {
		return err
	}

	// The code is normalized but NOT checked for emptiness here. SetPrices
	// below refuses an empty currency with the same Kind and writes nothing,
	// so a guard on this line would be a branch no test could distinguish from
	// its absence — and a branch nothing can distinguish is a branch that rots.
	currency := strings.ToUpper(strings.TrimSpace(currencyCode))

	// ListPrices returns EVERY price, including the ones with rules and the
	// ones on a price list. Reading through the storefront-facing list would
	// hand back a filtered set and the write below would delete the rest.
	existing, err := a.svc.ListPrices(ctx, priceSetID)
	if err != nil {
		return err
	}

	inputs := make([]PriceInput, 0, len(existing)+1)
	replaced := false

	for i := range existing {
		price := existing[i]
		input := PriceInput{
			CurrencyCode: price.CurrencyCode,
			Amount:       price.Amount,
			MinQuantity:  price.MinQuantity,
			MaxQuantity:  price.MaxQuantity,
			PriceListID:  price.PriceListID,
			Rules:        ruleInputs(price.Rules),
		}
		if isBasePrice(price) && strings.EqualFold(price.CurrencyCode, currency) {
			input.Amount = amount
			replaced = true
		}
		inputs = append(inputs, input)
	}

	if !replaced {
		inputs = append(inputs, PriceInput{
			CurrencyCode: currency,
			Amount:       amount,
			MinQuantity:  models.MinQuantity,
		})
	}

	// The order is pinned so the same edit writes the same rows every time and
	// an index in an error message means something.
	slices.SortStableFunc(inputs, func(a, b PriceInput) int {
		return strings.Compare(a.CurrencyCode, b.CurrencyCode)
	})

	_, err = a.svc.SetPrices(ctx, priceSetID, inputs)

	return err
}

// isBasePrice reports whether a price applies when nothing else does.
//
// Both halves matter. A price on a LIST belongs to a campaign the form does not
// show, and a price with RULES applies only in a context the form cannot
// express; changing either from an edit box would move a price the operator
// never asked about.
func isBasePrice(price models.Price) bool {
	return price.PriceListID == nil && len(price.Rules) == 0
}

// ruleInputs turns stored rules back into write inputs.
//
// The round trip has to be lossless: these rules belong to prices the form does
// not touch, and a field dropped here would be deleted by the write that
// follows.
func ruleInputs(rules []models.PriceRule) []RuleInput {
	if len(rules) == 0 {
		return nil
	}

	out := make([]RuleInput, 0, len(rules))
	for i := range rules {
		out = append(out, RuleInput{
			Attribute: rules[i].Attribute,
			Operator:  rules[i].Operator,
			Values:    slices.Clone(rules[i].Values),
		})
	}

	return out
}
