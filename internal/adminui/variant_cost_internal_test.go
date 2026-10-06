package adminui

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// fakeCoster writes a variant's cost as the product module's surface does and
// records each write.
type fakeCoster struct {
	fakeProductWriter
	written []string
	err     error
}

func (f *fakeCoster) SetVariantCost(_ context.Context, variantID, currencyCode string, read, amount *int64) error {
	text := func(v *int64) string {
		if v == nil {
			return "none"
		}
		return fmt.Sprint(*v)
	}
	f.written = append(f.written, variantID+"|"+currencyCode+"|"+text(read)+"|"+text(amount))

	return f.err
}

// costCatalog holds the variant costed in TRY in a shop that also sells in
// USD, both scales known.
func costCatalog() *fakeCatalog {
	catalog := variantCatalog(int64(2))
	catalog.byEntity[EntityVariant][0][FieldVariantUnitCosts] = []query.Record{
		{FieldUnitCostCurrency: "TRY", FieldUnitCostAmount: int64(1_250)},
	}
	catalog.byEntity[EntityRegion] = append(catalog.byEntity[EntityRegion], query.Record{
		"id": "reg_2", "currency_code": "USD", "currency": map[string]any{"code": "USD", "decimal_digits": int64(2)},
	})

	return catalog
}

// costForm is the form posting to the cost path, the one naming currency when
// given, empty when there is none.
func costForm(body, currency string) string {
	for _, form := range strings.Split(body, "<form") {
		form, _, _ = strings.Cut(form, "</form>")
		if !strings.Contains(form, `/cost"`) {
			continue
		}
		if currency == "" || strings.Contains(form, `name="currency" value="`+currency+`"`) {
			return form
		}
	}

	return ""
}

// TestTheVariantPageWritesOneCurrencysCost is ADR 0412 on the variant page:
// a reader sees the variant's costs and no form; a writer is offered one form
// per currency carrying the cost it was drawn with and one for a currency with
// none; a save reaches the product module's surface with the amount in the
// currency's scale, an empty amount clears, and the module's refusal, the cost
// having moved, is printed on the page.
func TestTheVariantPageWritesOneCurrencysCost(t *testing.T) {
	t.Parallel()

	coster := &fakeCoster{}
	panel := newVariantPanel(t, costCatalog(), &fakePriceWriter{}, nil)
	panel.products = coster
	panel.scopes = builtInScopes()
	reader := []string{scopeProductRead}
	writer := []string{scopeProductRead, scopeProductWrite}

	page := campaignsRequest(panel, http.MethodGet, variantURLFor(), nil, reader...)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	assert.Contains(t, page.Body.String(), "<h2>Unit cost</h2>")
	assert.Contains(t, page.Body.String(), "TRY: 12.50", "a reader reads the cost")
	assert.Empty(t, costForm(page.Body.String(), ""), "a reader is offered no form")

	page = campaignsRequest(panel, http.MethodGet, variantURLFor(), nil, writer...)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	form := costForm(page.Body.String(), "TRY")
	require.NotEmpty(t, form, "a writer edits the cost in TRY")
	assert.Contains(t, form, `name="read_amount" value="12.50"`, "the form carries the cost it was drawn with")
	newForm := costForm(page.Body.String(), "")
	assert.Contains(t, page.Body.String(), `<option value="USD">USD</option>`, "a currency with no cost is offered")
	assert.NotEmpty(t, newForm)

	cost := VariantPath
	cost = strings.Replace(strings.Replace(cost, "{id}", "prod_1", 1), "{variantID}", "var_1", 1) + "/cost"
	for _, write := range []struct {
		form url.Values
		want string
	}{
		{url.Values{"currency": {"TRY"}, "read_amount": {"12.50"}, "amount": {"13.75"}}, "var_1|TRY|1250|1375"},
		{url.Values{"currency": {"usd"}, "read_amount": {""}, "amount": {"4"}}, "var_1|USD|none|400"},
		{url.Values{"currency": {"TRY"}, "read_amount": {"12.50"}, "amount": {" "}}, "var_1|TRY|1250|none"},
	} {
		rec := campaignsRequest(panel, http.MethodPost, cost, write.form, writer...)
		require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
		assert.Equal(t, variantURLFor(), rec.Header().Get("Location"))
		assert.Equal(t, write.want, coster.written[len(coster.written)-1])
	}

	before := len(coster.written)
	for reason, form := range map[string]url.Values{
		"open the page again": {"currency": {"TRY"}, "read_amount": {"twelve"}, "amount": {"1"}},
		"decimal digits":      {"currency": {"TRY"}, "read_amount": {"12.50"}, "amount": {"1.234"}},
	} {
		rec := campaignsRequest(panel, http.MethodPost, cost, form, writer...)
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, reason)
		assert.Contains(t, rec.Body.String(), reason)
	}
	assert.Len(t, coster.written, before, "what the panel cannot read is not sent")

	coster.err = errors.Conflict("product_variant_cost_moved",
		"the unit cost in TRY is 1300, not the 1250 the form was drawn with; nothing was saved")
	rec := campaignsRequest(panel, http.MethodPost, cost,
		url.Values{"currency": {"TRY"}, "read_amount": {"12.50"}, "amount": {"14"}}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "not the 1250 the form was drawn with")

	rec = campaignsRequest(panel, http.MethodPost, cost,
		url.Values{"currency": {"TRY"}, "read_amount": {"12.50"}, "amount": {"14"}}, reader...)
	assert.Equal(t, http.StatusForbidden, rec.Code, "the write is product:write's")
}
