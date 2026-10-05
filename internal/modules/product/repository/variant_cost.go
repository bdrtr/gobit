package repository

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository/productdb"
)

// ListVariantCosts returns the unit costs of every given variant in one
// statement, each variant's in currency order (ADR 0401); a variant with none
// is absent from the map.
func (r *Repo) ListVariantCosts(ctx context.Context, variantIDs []string) (map[string][]models.VariantCost, error) {
	out := make(map[string][]models.VariantCost)
	if len(variantIDs) == 0 {
		return out, nil
	}
	rows, err := r.q.ListVariantCostsOf(ctx, variantIDs)
	if err != nil {
		return nil, wrapDB(err, "could not read the costs of %d variants", len(variantIDs))
	}
	for _, row := range rows {
		out[row.VariantID] = append(out[row.VariantID], models.VariantCost{
			CurrencyCode: row.CurrencyCode, Amount: row.Amount,
		})
	}
	return out, nil
}

// ReplaceVariantCosts writes a variant's unit costs whole: the old rows are
// deleted and the given ones written. It has to run inside a transaction that
// holds the variant's row, or a reader between the two statements sees no cost
// and two writers interleave their rows.
func (r *Repo) ReplaceVariantCosts(ctx context.Context, variantID string, costs []models.VariantCost) error {
	if err := r.q.DeleteVariantCosts(ctx, variantID); err != nil {
		return wrapDB(err, "could not clear the costs of variant: %s", variantID)
	}
	if len(costs) == 0 {
		return nil
	}
	codes := make([]string, 0, len(costs))
	amounts := make([]int64, 0, len(costs))
	for _, c := range costs {
		codes = append(codes, c.CurrencyCode)
		amounts = append(amounts, c.Amount)
	}
	if err := r.q.InsertVariantCosts(ctx, productdb.InsertVariantCostsParams{
		VariantID: variantID, CurrencyCodes: codes, Amounts: amounts,
	}); err != nil {
		return wrapDB(err, "could not write the costs of variant: %s", variantID)
	}
	return nil
}
