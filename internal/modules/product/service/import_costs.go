package service

import (
	"context"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
)

// costColumnPrefix begins a cost column of the export: a variant's unit cost in
// one currency, in minor units (ADR 0401, ADR 0424).
const costColumnPrefix = "variant_cost_"

// costColumn is a cost column as an import reads it.
var costColumn = regexp.MustCompile(`^variant_cost_[a-z]{3}$`)

// costColumns returns the header's cost columns.
func costColumns(header []string) []string {
	var out []string
	for _, column := range header {
		if costColumn.MatchString(column) {
			out = append(out, column)
		}
	}

	return out
}

// costs reads the row's non-empty cost cells as minor units by currency, each
// held to the bound a cost list holds it to.
//
// An empty cell writes nothing, as an empty price cell does (ADR 0207): a cost
// is cleared one currency at a time on the variant's page (ADR 0412) or by
// replacing the list.
func (r importRow) costs() (map[string]int64, error) {
	var out map[string]int64
	for _, column := range costColumns(r.header) {
		value := r.text(column)
		if value == "" {
			continue
		}
		amount, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, errors.Invalid(codeImportInvalid,
				"%s is not a whole number of minor units: %q", column, value)
		}
		if amount < 0 || amount > MaxCostAmount {
			return nil, errors.Invalid(codeImportInvalid,
				"%s is a cost between 0 and %d, %d given", column, MaxCostAmount, amount)
		}
		if out == nil {
			out = map[string]int64{}
		}
		out[strings.ToUpper(strings.TrimPrefix(column, costColumnPrefix))] = amount
	}

	return out, nil
}

// importCosts writes the given currencies' unit costs on a variant, leaves its
// other currencies as they are, and reports whether it wrote (ADR 0424).
//
// The variant's row is held, the lock [Service.SetVariantCosts] and
// [Service.SetVariantCost] take, and the list that results is held to the same
// rules, its bound on currencies included: a cost in a new currency on a
// variant that carries [MaxVariantCosts] is refused, not cut. A list that
// already holds every amount given is no change, and nothing is written.
//
// There is no comparison with what the file was drawn from, as ADR 0412's
// form makes: an import states the catalog, as it does a price (ADR 0207).
func (s *Service) importCosts(ctx context.Context, variantID string, amounts map[string]int64) (bool, error) {
	if len(amounts) == 0 {
		return false, nil
	}

	changed := false
	err := s.repo.InTx(ctx, func(ctx context.Context, tx repository.Store) error {
		changed = false
		locked, err := tx.LockLiveVariantsForBundle(ctx, []string{variantID})
		if err != nil {
			return err
		}
		if _, ok := locked[variantID]; !ok {
			return errors.NotFound(codeNotFound, "no such variant: %s", variantID)
		}
		byVariant, err := tx.ListVariantCosts(ctx, []string{variantID})
		if err != nil {
			return err
		}

		merged := make(map[string]int64, len(byVariant[variantID])+len(amounts))
		for _, c := range byVariant[variantID] {
			merged[c.CurrencyCode] = c.Amount
		}
		for currency, amount := range amounts {
			if current, held := merged[currency]; !held || current != amount {
				merged[currency], changed = amount, true
			}
		}
		if !changed {
			return nil
		}

		next := make([]models.VariantCost, 0, len(merged))
		for _, currency := range slices.Sorted(maps.Keys(merged)) {
			next = append(next, models.VariantCost{CurrencyCode: currency, Amount: merged[currency]})
		}
		normalized, err := normalizeVariantCosts(next)
		if err != nil {
			return err
		}

		return tx.ReplaceVariantCosts(ctx, variantID, normalized)
	})
	if err != nil {
		return false, err
	}

	return changed, nil
}
