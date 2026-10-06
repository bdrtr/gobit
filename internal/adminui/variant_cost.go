package adminui

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// A variant's unit costs on its page (ADR 0412): read from the variant's
// record, where the product module computes them only when named (ADR 0401),
// and written one currency at a time from the cost the form was drawn with, so
// a save never writes back a currency another operator changed.

// The read layer's field carrying a variant's unit costs, and the keys of one
// cost in it. They are exported for the reason the bundle's are: internal/arch
// binds them to the product module's spelling.
const (
	FieldVariantUnitCosts = "unit_costs"
	FieldUnitCostCurrency = "currency_code"
	FieldUnitCostAmount   = "amount"
)

// VariantCoster is the narrow surface a variant's unit cost is written
// through: the product module's (ADR 0412).
type VariantCoster interface {
	// SetVariantCost writes the variant's unit cost in one currency from the
	// cost the form was drawn with, read, nil for none; a nil amount clears
	// it, and a cost that moved since is refused.
	SetVariantCost(ctx context.Context, variantID, currencyCode string, read, amount *int64) error
}

// costRow is one of a variant's unit costs on its page. Amount is the box's
// value and the value the form says it was drawn with: a scaled decimal when
// the currency's scale is known, the raw minor-unit integer when it is not,
// which Minor says.
type costRow struct {
	Currency string
	Amount   string
	Minor    bool
}

// costEntries are the variant's unit costs as its record carries them, in the
// order the module gives them (currency order).
func costEntries(record query.Record) []map[string]any {
	var entries []map[string]any
	switch raw := record[FieldVariantUnitCosts].(type) {
	case []query.Record:
		for _, entry := range raw {
			entries = append(entries, entry)
		}
	case []map[string]any:
		entries = raw
	case []any:
		for _, entry := range raw {
			if m, ok := entry.(map[string]any); ok {
				entries = append(entries, m)
			}
		}
	}

	return entries
}

// variantCosts prints the unit costs the record carries in their currencies'
// scales.
func variantCosts(entries []map[string]any, scales map[string]int) []costRow {
	rows := make([]costRow, 0, len(entries))
	for _, entry := range entries {
		amount, ok := intValue(entry[FieldUnitCostAmount])
		if !ok {
			continue
		}
		code := strings.ToUpper(stringValue(entry[FieldUnitCostCurrency]))
		text, exact := formatAmount(int64(amount), code, scales)
		rows = append(rows, costRow{Currency: code, Amount: text, Minor: !exact})
	}

	return rows
}

// uncostedCurrencies are the shop's currencies, sorted, the variant has no
// unit cost in.
func uncostedCurrencies(scales map[string]int, costed []costRow) []string {
	var out []string
	for code := range scales {
		if !slices.ContainsFunc(costed, func(row costRow) bool { return row.Currency == code }) {
			out = append(out, code)
		}
	}
	slices.Sort(out)

	return out
}

// canCostVariant reports whether the operator may write a variant's cost: the
// cost is the product module's, written under product:write as on the admin
// API, and the module's surface must be able to write it.
func (u *UI) canCostVariant(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, ok := u.products.(VariantCoster)

	return ok && principal.HasScope(scopeProductWrite)
}

// submitVariantCost writes the variant's unit cost in the currency the form
// names, from the cost it was drawn with, and returns to the variant's page;
// an empty amount clears the cost, and a refusal is printed on the page.
func (u *UI) submitVariantCost(w http.ResponseWriter, r *http.Request) {
	productID := chi.URLParam(r, "id")
	variantID := chi.URLParam(r, "variantID")

	coster, ok := u.products.(VariantCoster)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Costs unavailable",
			"The product module's admin surface cannot write a variant's cost in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	currency := strings.ToUpper(strings.TrimSpace(r.PostFormValue("currency")))
	scale, known := u.currencyScales(r.Context())[currency]
	// The cost the form was drawn with, written in the same notation; an
	// empty one is none, which a form for a new currency carries.
	read, err := optionalAmount(r.PostFormValue(formReadAmount), scale, !known)
	if err != nil {
		u.renderVariant(w, r, http.StatusUnprocessableEntity, productID, variantID,
			"The form does not say which cost it was drawn with; open the page again.")
		return
	}
	amount, err := optionalAmount(r.PostFormValue("amount"), scale, !known)
	if err != nil {
		u.renderVariant(w, r, http.StatusUnprocessableEntity, productID, variantID, err.Error())
		return
	}

	if err := coster.SetVariantCost(r.Context(), variantID, currency, read, amount); err != nil {
		u.afterWrite(w, r, err, productID, variantID, "The cost could not be saved")
		return
	}

	corehttp.WriteRedirect(r.Context(), w, variantURL(productID, variantID))
}

// optionalAmount reads an amount as [parseAmount] does, nil for an empty box.
func optionalAmount(text string, digits int, minor bool) (*int64, error) {
	// An empty box is no amount, which is not an error.
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	value, err := parseAmount(text, digits, minor)
	if err != nil {
		return nil, err
	}

	return &value, nil
}
