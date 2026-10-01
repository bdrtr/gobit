package adminui

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// A variant's prices on price lists (ADR 0327): each with its list, its
// amount and the customer groups it is limited to, read through the pricing
// module's surface because the read layer leaves out a price with rules; a
// price is added on a list, for every customer or for some groups, and
// removed, under pricing:write.

// VariantListPricesPath adds a price on a list to the variant, and
// VariantListPriceRemovePath removes one.
const (
	VariantListPricesPath      = VariantPath + "/list-prices"
	VariantListPriceRemovePath = VariantListPricesPath + "/{priceID}/remove"
)

// formPriceSetID is the hidden field a price form names its price set in.
const formPriceSetID = "price_set_id"

// The list price form's fields.
const (
	formListPriceList     = "price_list_id"
	formListPriceCurrency = "currency"
	formListPriceAmount   = "amount"
	formListPriceGroup    = "customer_group"
)

// priceListChoices is how many price lists the form offers, the pricing
// module's page ceiling.
const priceListChoices = 100

// ListPriceAdmin is the narrow surface a variant's list prices are read and
// written through: the pricing module's.
type ListPriceAdmin interface {
	// ListPricesJSON lists the set's prices on price lists with their rules.
	ListPricesJSON(ctx context.Context, priceSetID string) (json.RawMessage, error)
	// AddListPrice adds a price on the list, for every customer or for the
	// groups named.
	AddListPrice(ctx context.Context, priceSetID, priceListID, currencyCode string, amount int64, groupIDs []string) error
	// RemoveListPrice removes one of the set's list prices.
	RemoveListPrice(ctx context.Context, priceSetID, priceID string) error
}

// listPriceRow is one list price as the pricing module's surface sends it;
// the json tags are the contract with that surface, exercised end to end.
type listPriceRow struct {
	ID          string `json:"id"`
	PriceListID string `json:"price_list_id"`
	ListTitle   string `json:"price_list_title"`
	Currency    string `json:"currency_code"`
	Amount      int64  `json:"amount"`
	MinQuantity int    `json:"min_quantity"`
	MaxQuantity *int   `json:"max_quantity"`
	Rules       []struct {
		Attribute string   `json:"attribute"`
		Operator  string   `json:"operator"`
		Values    []string `json:"values"`
	} `json:"rules"`
}

// listPriceView is one list price as the page prints it.
type listPriceView struct {
	ID     string
	List   string
	Amount string
	// Applies is when it applies: its quantities, and its customers.
	Applies string
}

// listPricesOf reads the set's list prices for the page, naming the groups a
// price is limited to for an operator who may read the customers; unread says
// the read failed, which leaves the variant on the page.
func (u *UI) listPricesOf(r *http.Request, admin ListPriceAdmin, priceSetID string, scales map[string]int) ([]listPriceView, bool) {
	ctx := r.Context()
	raw, err := admin.ListPricesJSON(ctx, priceSetID)
	var rows []listPriceRow
	if err == nil {
		err = json.Unmarshal(raw, &rows)
	}
	if err != nil {
		corehttp.LoggerFromContext(ctx).WarnContext(ctx,
			"the panel could not read a variant's list prices", "error", err, "price_set_id", priceSetID)

		return nil, true
	}

	names := map[string]string{}
	if principal, _ := corehttp.PrincipalFromContext(ctx); principal.HasScope(scopeCustomerRead) {
		for _, option := range u.groupList(ctx).Options {
			names[option.ID] = option.Name
		}
	}
	views := make([]listPriceView, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		list := row.ListTitle
		if list == "" {
			list = row.PriceListID
		}
		views = append(views, listPriceView{
			ID: row.ID, List: list, Amount: minorText(row.Amount, row.Currency, scales),
			Applies: applies(row.MinQuantity, row.MaxQuantity, "") + forCustomers(row, names),
		})
	}

	return views, false
}

// forCustomers says which customers a list price is for: every one, the named
// groups, or the conditions the panel did not write, spelled as they stand.
func forCustomers(row *listPriceRow, names map[string]string) string {
	if len(row.Rules) == 0 {
		return ", for every customer"
	}
	var parts []string
	for _, rule := range row.Rules {
		if rule.Attribute == RuleAttributeCustomerGroup {
			groups := make([]string, 0, len(rule.Values))
			for _, id := range rule.Values {
				if name := names[id]; name != "" {
					id = name
				}
				groups = append(groups, id)
			}
			parts = append(parts, "for "+strings.Join(groups, ", "))
			continue
		}
		parts = append(parts, rule.Attribute+" "+rule.Operator+" "+strings.Join(rule.Values, ", "))
	}

	return ", " + strings.Join(parts, "; ")
}

// listPriceForm is what the page's form offers: the lists, the currencies and,
// for an operator who may read the customers, the groups.
type listPriceForm struct {
	Lists      []priceListRow
	Currencies []string
	Groups     []groupOption
}

// canWriteListPrices reports whether the operator may write the variant's
// list prices here.
func (u *UI) canWriteListPrices(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, prices := u.prices.(ListPriceAdmin)
	_, lists := u.prices.(PriceListAdmin)

	return prices && lists && principal.HasScope(scopePricingWrite)
}

// listPriceFormOf reads what the form offers; nil when the lists cannot be
// read, which leaves the page without the form.
func (u *UI) listPriceFormOf(r *http.Request, scales map[string]int) *listPriceForm {
	ctx := r.Context()
	admin, _ := u.prices.(PriceListAdmin)
	raw, _, err := admin.PriceListsJSON(ctx, priceListChoices, 0)
	var lists []priceListRow
	if err == nil {
		err = json.Unmarshal(raw, &lists)
	}
	if err != nil {
		corehttp.LoggerFromContext(ctx).WarnContext(ctx, "the panel could not read the price lists to offer", "error", err)
		return nil
	}

	form := &listPriceForm{Lists: lists, Currencies: slices.Sorted(maps.Keys(scales))}
	if principal, _ := corehttp.PrincipalFromContext(ctx); principal.HasScope(scopeCustomerRead) {
		form.Groups = u.groupList(ctx).Options
	}

	return form
}

// addListPrice adds the form's price on a list to the variant's price set and
// returns to the variant's page (ADR 0327); a refusal is printed there.
func (u *UI) addListPrice(w http.ResponseWriter, r *http.Request) {
	u.listPriceWrite(w, r, func(ctx context.Context, admin ListPriceAdmin, priceSetID string) error {
		currency := strings.ToUpper(strings.TrimSpace(r.PostFormValue(formListPriceCurrency)))
		scale, known := u.currencyScales(ctx)[currency]
		amount, err := parseAmount(r.PostFormValue(formListPriceAmount), scale, !known)
		if err != nil {
			return err
		}

		return admin.AddListPrice(ctx, priceSetID, strings.TrimSpace(r.PostFormValue(formListPriceList)),
			currency, amount, chosenValues(r.PostForm[formListPriceGroup]))
	})
}

// removeListPrice removes the list price in the path from the variant's price
// set and returns to the variant's page (ADR 0327).
func (u *UI) removeListPrice(w http.ResponseWriter, r *http.Request) {
	u.listPriceWrite(w, r, func(ctx context.Context, admin ListPriceAdmin, priceSetID string) error {
		return admin.RemoveListPrice(ctx, priceSetID, chi.URLParam(r, "priceID"))
	})
}

// listPriceWrite runs one of the page's list price writes on the price set
// the form names and returns to the variant's page; a refusal is printed
// there.
func (u *UI) listPriceWrite(
	w http.ResponseWriter, r *http.Request, write func(ctx context.Context, admin ListPriceAdmin, priceSetID string) error,
) {
	productID, variantID := chi.URLParam(r, "id"), chi.URLParam(r, "variantID")
	admin, ok := u.prices.(ListPriceAdmin)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Pricing unavailable",
			"The pricing module's panel surface cannot write a list price in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	switch err := write(r.Context(), admin, strings.TrimSpace(r.PostFormValue(formPriceSetID))); {
	case err == nil:
		corehttp.WriteRedirect(r.Context(), w, variantURL(productID, variantID))
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		u.renderVariant(w, r, http.StatusUnprocessableEntity, productID, variantID, messageFor(err))
	default:
		u.unexpectedFailure(w, r, err, "The list price could not be written")
	}
}
