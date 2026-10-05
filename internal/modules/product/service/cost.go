package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
)

// MaxVariantCosts is the most currencies a variant carries a unit cost in
// (ADR 0401); a longer list is refused rather than cut.
const MaxVariantCosts = 50

// MaxCostAmount is the largest unit cost, in minor units: a price's bound, so a
// cost times a line's quantity stays inside an order's totals. internal/arch
// binds it to the order module's bound on a unit amount.
const MaxCostAmount int64 = 1_000_000_000_000

// costCurrencyLen is the length of an ISO 4217 alphabetic code.
const costCurrencyLen = 3

// VariantCosts returns what one unit of a variant costs the shop, one entry per
// currency in currency order, and an empty list for a variant with none
// (ADR 0401). An unknown or deleted variant is NotFound.
func (s *Service) VariantCosts(ctx context.Context, variantID string) ([]models.VariantCost, error) {
	if _, err := requireID("variant_id", variantID); err != nil {
		return nil, err
	}
	if _, err := s.repo.GetVariant(ctx, variantID); err != nil {
		return nil, err
	}
	byVariant, err := s.repo.ListVariantCosts(ctx, []string{variantID})
	if err != nil {
		return nil, err
	}
	costs := byVariant[variantID]
	if costs == nil {
		costs = []models.VariantCost{}
	}
	return costs, nil
}

// SetVariantCosts replaces a variant's unit costs with the given ones and
// returns them (ADR 0401); an empty list clears them.
//
// A currency is three letters, upper-cased as given, and appears once; an
// amount is a whole number of minor units from 0 to [MaxCostAmount]; a list
// holds at most [MaxVariantCosts]. The write holds the variant's row, the lock
// a variant's deletion and a bundle write take, so two writes of the same
// variant do not interleave their rows and a variant deleted at the same
// instant is seen as gone. It is not a revision of the product: the costs are
// not in its view (ADR 0221), as add-ons are not.
func (s *Service) SetVariantCosts(
	ctx context.Context, variantID string, costs []models.VariantCost,
) ([]models.VariantCost, error) {
	if _, err := requireID("variant_id", variantID); err != nil {
		return nil, err
	}
	normalized, err := normalizeVariantCosts(costs)
	if err != nil {
		return nil, err
	}

	if err := s.repo.InTx(ctx, func(ctx context.Context, tx repository.Store) error {
		locked, err := tx.LockLiveVariantsForBundle(ctx, []string{variantID})
		if err != nil {
			return err
		}
		if _, ok := locked[variantID]; !ok {
			return errors.NotFound(codeNotFound, "no such variant: %s", variantID)
		}
		return tx.ReplaceVariantCosts(ctx, variantID, normalized)
	}); err != nil {
		return nil, err
	}
	return s.VariantCosts(ctx, variantID)
}

// normalizeVariantCosts checks a cost list as given and returns it with every
// currency trimmed and upper-cased; each refusal names the entry.
func normalizeVariantCosts(costs []models.VariantCost) ([]models.VariantCost, error) {
	if len(costs) > MaxVariantCosts {
		return nil, invalid("a variant carries at most %d costs, %d given", MaxVariantCosts, len(costs))
	}
	out := make([]models.VariantCost, 0, len(costs))
	seen := make(map[string]bool, len(costs))
	for _, c := range costs {
		code := strings.ToUpper(strings.TrimSpace(c.CurrencyCode))
		if !isCurrencyCode(code) {
			return nil, invalid("a cost's currency_code is three letters (ISO 4217), %q given", c.CurrencyCode)
		}
		if seen[code] {
			return nil, invalid("the cost list names a currency more than once: %s", code)
		}
		seen[code] = true
		if c.Amount < 0 || c.Amount > MaxCostAmount {
			return nil, invalid("a cost's amount is between 0 and %d (%s: %d)", MaxCostAmount, code, c.Amount)
		}
		out = append(out, models.VariantCost{CurrencyCode: code, Amount: c.Amount})
	}
	return out, nil
}

// isCurrencyCode reports whether the code is exactly three letters A to Z.
func isCurrencyCode(code string) bool {
	if len(code) != costCurrencyLen {
		return false
	}
	for _, r := range code {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}
