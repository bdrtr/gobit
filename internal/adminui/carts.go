package adminui

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// The telephone order (ADR 0290): an operator opens a cart for a caller and
// adds priced lines to it through the cart module's panel surface, the acts
// the admin API took since ADR 0146.

// ServiceCartAdmin is the cart module's panel surface, spelled by hand and
// pinned against the module's constant in internal/arch.
const ServiceCartAdmin = "cart.admin"

// EntityCart is the cart module's entity in the read layer.
const EntityCart = "cart"

// The telephone order's paths: the form that opens a cart, the cart's page,
// and the form that adds a line to it.
const (
	CartsPath     = URLPrefix + "/carts"
	CartPath      = CartsPath + "/{id}"
	CartLinesPath = CartPath + "/lines"
)

// telephoneLabel is what the section is called on screen.
const telephoneLabel = "Telephone order"

// TelephoneCarts is the narrow surface the panel builds a telephone order
// through (ADR 0001); each method is the cart module's admin act, so the panel
// refuses what the API refuses.
type TelephoneCarts interface {
	// OpenCart opens a cart for the country's region, for a customer or a
	// guest with an e-mail, and returns its id.
	OpenCart(ctx context.Context, countryCode, customerID, email string) (string, error)
	// AddLine adds a variant priced by the named channel's catalog and returns
	// the line's id.
	AddLine(ctx context.Context, cartID, salesChannelID, variantID string, quantity int64) (string, error)
}

// The cart fields the page reads beyond the order's, and the keys of one
// line. They are the cart module's names, repeated for [EntityOrder]'s reason.
const (
	fieldCartCustomerID  = "customer_id"
	fieldCartTotalsStale = "totals_stale"
	fieldCartCompleted   = "completed"
	fieldCartLines       = "lines"
	cartLineID           = "id"
	cartLineVariantID    = "variant_id"
	cartLineTitle        = "title"
	cartLineQuantity     = "quantity"
	cartLineUnitPrice    = "unit_price"
	cartLineTotal        = "total"
	cartLineParentID     = "parent_line_id"
)

// The forms' fields beside [formVariantID] and [formQuantity].
const (
	formCountryCode    = "country_code"
	formCustomerID     = "customer_id"
	formEmail          = "email"
	formSalesChannelID = "sales_channel_id"
)

// cartLine is one line as the page prints it.
type cartLine struct {
	ID, VariantID, Title string
	Quantity             int64
	UnitPrice, Total     string
	// AddOn is a line that follows another (ADR 0229); the page sets it under
	// its line.
	AddOn bool
}

// cartPage is the cart as the page prints it.
type cartPage struct {
	ID, Currency, Email, CustomerID  string
	Subtotal, Tax, Shipping, Total   string
	TotalsStale, Completed, Unscaled bool
	Lines                            []cartLine
}

// newTelephoneOrder renders the form that opens a cart.
func (u *UI) newTelephoneOrder(w http.ResponseWriter, r *http.Request) {
	u.renderOpenForm(w, r, http.StatusOK, "", url.Values{})
}

// openTelephoneOrder opens a cart and goes to its page; a refusal comes back
// on the form with what was typed.
func (u *UI) openTelephoneOrder(w http.ResponseWriter, r *http.Request) {
	if u.carts == nil {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Telephone orders unavailable",
			"The cart module's panel surface is not registered in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	// The country goes as typed: the flow trims it and the region's lookup
	// reads it in either case.
	id, err := u.carts.OpenCart(r.Context(),
		r.PostFormValue(formCountryCode),
		strings.TrimSpace(r.PostFormValue(formCustomerID)),
		strings.TrimSpace(r.PostFormValue(formEmail)))
	switch {
	case err == nil:
		corehttp.WriteRedirect(r.Context(), w, CartsPath+"/"+id)
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		u.renderOpenForm(w, r, http.StatusUnprocessableEntity, messageFor(err), r.PostForm)
	default:
		u.unexpectedFailure(w, r, err, "The cart could not be opened")
	}
}

// renderOpenForm writes the form with a refusal and what was typed.
func (u *UI) renderOpenForm(w http.ResponseWriter, r *http.Request, status int, refused string, typed url.Values) {
	u.templates.render(w, r, status, "carts.gohtml", map[string]any{
		titleKey:    telephoneLabel,
		"CartsPath": CartsPath,
		"Refused":   refused,
		"Typed":     typed,
	})
}

// showCart renders the cart's page.
func (u *UI) showCart(w http.ResponseWriter, r *http.Request) {
	u.renderCart(w, r, http.StatusOK, chi.URLParam(r, "id"), "", url.Values{})
}

// addCartLine adds a line and returns to the cart's page; a refusal comes back
// on the page with what was typed.
func (u *UI) addCartLine(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if u.carts == nil {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Telephone orders unavailable",
			"The cart module's panel surface is not registered in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	quantity, err := strconv.ParseInt(strings.TrimSpace(r.PostFormValue(formQuantity)), 10, 64)
	if err != nil {
		u.renderCart(w, r, http.StatusUnprocessableEntity, id, "The quantity is a whole number.", r.PostForm)
		return
	}

	_, err = u.carts.AddLine(r.Context(), id,
		strings.TrimSpace(r.PostFormValue(formSalesChannelID)),
		strings.TrimSpace(r.PostFormValue(formVariantID)), quantity)
	switch {
	case err == nil:
		corehttp.WriteRedirect(r.Context(), w, CartsPath+"/"+id)
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		u.renderCart(w, r, http.StatusUnprocessableEntity, id, messageFor(err), r.PostForm)
	default:
		u.unexpectedFailure(w, r, err, "The line could not be added")
	}
}

// renderCart reads the cart and writes its page, with a refused write's reason
// and what was typed. An operator who may write carts and not read them is
// told the reason and shown nothing of the cart (ADR 0260).
func (u *UI) renderCart(
	w http.ResponseWriter, r *http.Request, status int, id, refused string, typed url.Values,
) {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	if refused != "" && !principal.HasScope(scopeCartRead) {
		u.errorPage(w, r, status, "Not done", refused)
		return
	}

	records, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity: EntityCart,
		Fields: []string{
			fieldID, fieldCurrencyCod, fieldEmail, fieldCartCustomerID,
			fieldSubtotal, fieldTax, fieldShipping, fieldTotal,
			fieldCartTotalsStale, fieldCartCompleted, fieldCartLines,
		},
		Filters: map[string]any{filterID: []string{id}},
		Limit:   1,
	})
	if err != nil {
		u.catalogFailure(w, r, err, "The cart could not be read.")
		return
	}
	if len(records) == 0 {
		u.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such cart.")
		return
	}

	u.templates.render(w, r, status, "cart.gohtml", map[string]any{
		titleKey:    telephoneLabel,
		"Cart":      cartPageOf(records[0], u.currencyScales(r.Context())),
		"CartsPath": CartsPath,
		"CanWrite":  u.carts != nil && principal.HasScope(scopeCartWrite),
		"Refused":   refused,
		"Typed":     typed,
	})
}

// cartPageOf reads the cart record the page asked for.
func cartPageOf(record query.Record, scales map[string]int) cartPage {
	page := cartPage{
		ID:          recordString(record, fieldID),
		Currency:    recordString(record, fieldCurrencyCod),
		Email:       recordString(record, fieldEmail),
		CustomerID:  recordString(record, fieldCartCustomerID),
		TotalsStale: record[fieldCartTotalsStale] == true,
		Completed:   record[fieldCartCompleted] == true,
	}
	var known bool
	page.Total, known = amountField(record, fieldTotal, page.Currency, scales)
	page.Unscaled = !known
	page.Subtotal, _ = amountField(record, fieldSubtotal, page.Currency, scales)
	page.Tax, _ = amountField(record, fieldTax, page.Currency, scales)
	page.Shipping, _ = amountField(record, fieldShipping, page.Currency, scales)

	var entries []query.Record
	switch value := record[fieldCartLines].(type) {
	case []map[string]any:
		for _, entry := range value {
			entries = append(entries, entry)
		}
	case []query.Record:
		entries = value
	}
	for _, entry := range entries {
		line := cartLine{
			ID:        recordString(entry, cartLineID),
			VariantID: recordString(entry, cartLineVariantID),
			Title:     recordString(entry, cartLineTitle),
			Quantity:  recordInt(entry, cartLineQuantity),
			AddOn:     recordString(entry, cartLineParentID) != "",
		}
		line.UnitPrice, _ = amountField(entry, cartLineUnitPrice, page.Currency, scales)
		line.Total, _ = amountField(entry, cartLineTotal, page.Currency, scales)
		page.Lines = append(page.Lines, line)
	}

	return page
}
