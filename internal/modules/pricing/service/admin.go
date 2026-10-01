package service

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/bdrtr/gobit/core/errors"

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

// CodeUnitPriceAmbiguous reports a price set holding more than one base price
// at one unit in a currency, so that a write naming the currency cannot tell
// which of them it means (ADR 0206).
const CodeUnitPriceAmbiguous = "pricing_unit_price_ambiguous"

// SetBasePriceAmount sets the base price at one unit in a currency and leaves
// every other price on the set untouched (ADR 0206).
//
// The price it changes is ADR 0041's: bound to no list, carrying no rules, and
// covering one unit. A quantity tier starting above one is another price and
// keeps its amount. When the currency has no price at one unit, one is added,
// ending below the currency's lowest tier so the two do not overlap. An amount
// that already stands writes nothing.
//
// # What it costs
//
// The write replaces the set whole, so every price on the set is rewritten and
// the price IDs are REGENERATED. That is contained: a price id is
// referenced only by pricing's own price_rule rows, which are rewritten with
// it, and no other module or table holds one. It is written down because it is
// a real side effect of an edit that looks local.
//
// Two saves of the same set, each changing its own currency, both land: the set
// is read under its lock and written back from what it holds (D193). read is the
// amount the form was drawn with, and a price that holds another one by the
// time the write takes the lock is refused with [CodePriceMoved] (ADR 0280), so
// two saves of the same currency do not silently overwrite each other.
func (a *AdminSurface) SetBasePriceAmount(
	ctx context.Context, priceSetID, currencyCode string, read, amount int64,
) error {
	if a == nil {
		return errors.Unavailable(CodeInvalidInput, "the pricing admin surface is not set up")
	}
	if err := a.svc.ready(); err != nil {
		return err
	}

	currency := strings.ToUpper(strings.TrimSpace(currencyCode))
	if currency == "" {
		return errors.Invalid(CodeInvalidInput, "the currency is required")
	}
	_, err := a.svc.reviseUnitBasePrices(ctx, priceSetID, map[string]int64{currency: amount},
		func(existing []models.Price) error {
			at := unitBasePrices(existing, currency)
			// Two prices at one unit are refused as ambiguous by the write
			// itself; which of them moved is not this check's to say.
			if len(at) > 1 || (len(at) == 1 && existing[at[0]].Amount == read) {
				return nil
			}
			now := "none"
			if len(at) == 1 {
				now = strconv.FormatInt(existing[at[0]].Amount, 10)
			}

			return errors.Conflict(CodePriceMoved,
				"the base price in %s is %s, not the %d the form was drawn with; nothing was saved",
				currency, now, read)
		})

	return err
}

// CodePriceMoved refuses a price written over an amount the writer was not
// shown (ADR 0280).
const CodePriceMoved = "pricing_price_moved"

// setUnitBasePrices sets the base price at one unit in each named currency,
// leaves every other price on the set as it is, and reports whether it wrote.
// The panel's surface and the interop's [Service.SetUnitBasePrices] share it.
//
// The currency code is normalized but NOT checked for emptiness here. buildPrices
// below refuses an empty currency with the same Kind and writes nothing, so a
// guard here would be a branch no test could distinguish from its absence.
func (s *Service) setUnitBasePrices(
	ctx context.Context, priceSetID string, amountsByCurrency map[string]int64,
) (bool, error) {
	return s.reviseUnitBasePrices(ctx, priceSetID, amountsByCurrency, nil)
}

// reviseUnitBasePrices is the write behind setUnitBasePrices; check, when given,
// looks at the prices read under the set's lock before anything is decided and
// refuses the write by returning an error (ADR 0280).
func (s *Service) reviseUnitBasePrices(
	ctx context.Context, priceSetID string, amountsByCurrency map[string]int64,
	check func(existing []models.Price) error,
) (bool, error) {
	amounts := make(map[string]int64, len(amountsByCurrency))
	for code, amount := range amountsByCurrency {
		currency := strings.ToUpper(strings.TrimSpace(code))
		if _, twice := amounts[currency]; twice {
			return false, errors.Invalid(CodeInvalidInput, "the currency %s is named twice", currency)
		}
		amounts[currency] = amount
	}

	if err := s.ready(); err != nil {
		return false, err
	}
	if err := requireID(priceSetID, models.PriceSetIDPrefix, "price set id"); err != nil {
		return false, err
	}

	// The prices are read UNDER the set's lock, in the transaction that writes
	// them back (D193): read before it, two writes to one set each wrote back
	// the other's currency as it was, and one change was lost. The read is of
	// EVERY price, the ones with rules and the ones on a price list included;
	// a filtered read would have the write below delete the rest.
	changed := false
	_, err := s.repo.RevisePrices(ctx, priceSetID, func(existing []models.Price) ([]models.Price, bool, error) {
		if check != nil {
			if err := check(existing); err != nil {
				return nil, false, err
			}
		}
		inputs, write, err := unitBaseInputs(priceSetID, existing, amounts)
		if err != nil || !write {
			return nil, false, err
		}
		built, err := s.buildPrices(priceSetID, inputs, s.clock())
		if err != nil {
			return nil, false, err
		}
		changed = true

		return built, true, nil
	}, s.clock)
	if err != nil {
		return false, err
	}

	return changed, nil
}

// unitBaseInputs is the set's prices with the base price at one unit set in
// each named currency, and whether that changes anything.
func unitBaseInputs(
	priceSetID string, existing []models.Price, amounts map[string]int64,
) ([]PriceInput, bool, error) {
	inputs := make([]PriceInput, 0, len(existing)+len(amounts))
	for i := range existing {
		inputs = append(inputs, PriceInput{
			CurrencyCode: existing[i].CurrencyCode,
			Amount:       existing[i].Amount,
			MinQuantity:  existing[i].MinQuantity,
			MaxQuantity:  existing[i].MaxQuantity,
			PriceListID:  existing[i].PriceListID,
			Rules:        ruleInputs(existing[i].Rules),
		})
	}

	changed := false
	for _, currency := range slices.Sorted(maps.Keys(amounts)) {
		at := unitBasePrices(existing, currency)
		switch len(at) {
		case 0:
			inputs = append(inputs, PriceInput{
				CurrencyCode: currency,
				Amount:       amounts[currency],
				MinQuantity:  models.MinQuantity,
				MaxQuantity:  belowTiers(existing, currency),
			})
			changed = true
		case 1:
			if inputs[at[0]].Amount != amounts[currency] {
				inputs[at[0]].Amount = amounts[currency]
				changed = true
			}
		default:
			return nil, false, errors.Conflict(CodeUnitPriceAmbiguous,
				"the price set %s has %d base prices at one unit in %s; which one to change is not this write's to guess",
				priceSetID, len(at), currency)
		}
	}
	if !changed {
		return nil, false, nil
	}

	// The order is pinned so the same edit writes the same rows every time and
	// an index in an error message means something.
	slices.SortStableFunc(inputs, func(a, b PriceInput) int {
		return strings.Compare(a.CurrencyCode, b.CurrencyCode)
	})

	return inputs, true, nil
}

// unitBasePrices returns the indexes of the base prices at one unit in a
// currency: ADR 0041's definition, the price the export writes and the catalog
// filter compares.
func unitBasePrices(prices []models.Price, currency string) []int {
	var out []int
	for i := range prices {
		price := &prices[i]
		if !isBasePrice(*price) || !strings.EqualFold(price.CurrencyCode, currency) {
			continue
		}
		if price.MinQuantity <= models.MinQuantity &&
			(price.MaxQuantity == nil || *price.MaxQuantity >= models.MinQuantity) {
			out = append(out, i)
		}
	}

	return out
}

// belowTiers is the upper end of a new price at one unit: one below the
// lowest tier the currency's base prices start at, or open when there is none.
func belowTiers(prices []models.Price, currency string) *int32 {
	var lowest *int32
	for i := range prices {
		price := &prices[i]
		if !isBasePrice(*price) || !strings.EqualFold(price.CurrencyCode, currency) {
			continue
		}
		if lowest == nil || price.MinQuantity < *lowest {
			lowest = &price.MinQuantity
		}
	}
	if lowest == nil {
		return nil
	}

	upTo := *lowest - 1

	return &upTo
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
