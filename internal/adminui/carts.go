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

// The telephone order: an operator opens a cart for a caller and adds priced
// lines to it (ADR 0290), writes its shipping address, chooses its shipping
// method and completes it with an offline method (ADR 0291), through the cart
// module's panel surface — the acts the admin API took since ADR 0146 and ADR
// 0286.

// ServiceCartAdmin is the cart module's panel surface, spelled by hand and
// pinned against the module's constant in internal/arch.
const ServiceCartAdmin = "cart.admin"

// EntityCart is the cart module's entity in the read layer.
const EntityCart = "cart"

// The telephone order's paths: the form that opens a cart, the cart's page,
// and the forms that add a line, write the address, choose the shipping and
// complete the cart.
const (
	CartsPath        = URLPrefix + "/carts"
	CartPath         = CartsPath + "/{id}"
	CartLinesPath    = CartPath + "/lines"
	CartAddressPath  = CartPath + "/address"
	CartShippingPath = CartPath + "/shipping"
	CartCompletePath = CartPath + "/complete"
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
	// SetShippingAddress writes the shipping address from the address keys
	// and reprices the cart.
	SetShippingAddress(ctx context.Context, cartID string, address map[string]string) error
	// AddShippingMethod prices the shipping option for the cart and adds it.
	AddShippingMethod(ctx context.Context, cartID, shippingOptionID string) (string, error)
	// Complete completes the cart in the named channel with an offline method
	// against the total read to the caller, and returns the order and what it
	// owes.
	Complete(ctx context.Context, cartID, salesChannelID, paymentProviderID string, expectedTotal int64) (
		orderID string, outstanding int64, err error)
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
	// The shipping address, keyed as an order's is, and the chosen methods
	// (ADR 0291).
	fieldCartShippingAddress = "shipping_address"
	fieldCartShippingMethods = "shipping_methods"
	cartMethodName           = "name"
	cartMethodAmount         = "amount"
)

// addressFields are the address form's fields, the cart surface's keys, in the
// order the form asks for them.
var addressFields = []string{
	"first_name", "last_name", "address_1", "city", "postal_code", "country_code", "phone",
}

// The forms' fields beside [formVariantID] and [formQuantity].
const (
	formCountryCode    = "country_code"
	formCustomerID     = "customer_id"
	formEmail          = "email"
	formSalesChannelID = "sales_channel_id"
	formShippingOption = "shipping_option_id"
	formPaymentMethod  = "payment_provider_id"
	// formReadTotal is the total the page was drawn with, in minor units: the
	// total the operator read to the caller (ADR 0280's read value).
	formReadTotal = "read_total"
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
	// TotalMinor is the total in minor units, carried by the completion form.
	TotalMinor int64
	// ShipTo is the shipping address, a line a part; Address its fields, to
	// draw the form with what is there.
	ShipTo  []string
	Address map[string]string
	// Methods are the chosen shipping methods, each its name and amount.
	Methods []cartMethod
}

// cartMethod is one chosen shipping method.
type cartMethod struct {
	Name, Amount string
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
	u.cartWrite(w, r, func(ctx context.Context, id string) (string, error) {
		quantity, err := strconv.ParseInt(strings.TrimSpace(r.PostFormValue(formQuantity)), 10, 64)
		if err != nil {
			return "", errors.Invalid("admin_ui_quantity", "The quantity is a whole number.")
		}
		_, err = u.carts.AddLine(ctx, id,
			strings.TrimSpace(r.PostFormValue(formSalesChannelID)),
			strings.TrimSpace(r.PostFormValue(formVariantID)), quantity)

		return "", err
	})
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
			fieldCartShippingAddress, fieldCartShippingMethods,
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
		// AddressFields orders the address form (ADR 0291).
		"AddressFields": addressFields,
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
	page.TotalMinor = recordInt(record, fieldTotal)
	page.ShipTo = addressLines(record[fieldCartShippingAddress])
	page.Address = map[string]string{}
	if address, ok := record[fieldCartShippingAddress].(map[string]any); ok {
		for _, name := range addressFields {
			page.Address[name] = stringValue(address[name])
		}
	}
	for _, entry := range recordList(record[fieldCartShippingMethods]) {
		method := cartMethod{Name: recordString(entry, cartMethodName)}
		method.Amount, _ = amountField(entry, cartMethodAmount, page.Currency, scales)
		page.Methods = append(page.Methods, method)
	}
	page.Subtotal, _ = amountField(record, fieldSubtotal, page.Currency, scales)
	page.Tax, _ = amountField(record, fieldTax, page.Currency, scales)
	page.Shipping, _ = amountField(record, fieldShipping, page.Currency, scales)

	for _, entry := range recordList(record[fieldCartLines]) {
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

// recordList reads a list-valued field as the provider built it, or as
// records when the read went through a layer that converts them.
func recordList(value any) []query.Record {
	var entries []query.Record
	switch list := value.(type) {
	case []map[string]any:
		for _, entry := range list {
			entries = append(entries, entry)
		}
	case []query.Record:
		entries = list
	}

	return entries
}

// setCartAddress writes the shipping address and returns to the cart.
func (u *UI) setCartAddress(w http.ResponseWriter, r *http.Request) {
	u.cartWrite(w, r, func(ctx context.Context, id string) (string, error) {
		address := make(map[string]string, len(addressFields))
		for _, name := range addressFields {
			address[name] = strings.TrimSpace(r.PostFormValue(name))
		}

		return "", u.carts.SetShippingAddress(ctx, id, address)
	})
}

// addCartShipping chooses the shipping option and returns to the cart.
func (u *UI) addCartShipping(w http.ResponseWriter, r *http.Request) {
	u.cartWrite(w, r, func(ctx context.Context, id string) (string, error) {
		_, err := u.carts.AddShippingMethod(ctx, id, strings.TrimSpace(r.PostFormValue(formShippingOption)))
		return "", err
	})
}

// completeCart completes the cart against the total the page was drawn with
// and goes to the order it placed.
func (u *UI) completeCart(w http.ResponseWriter, r *http.Request) {
	u.cartWrite(w, r, func(ctx context.Context, id string) (string, error) {
		total, err := strconv.ParseInt(strings.TrimSpace(r.PostFormValue(formReadTotal)), 10, 64)
		if err != nil {
			return "", errors.Invalid("admin_ui_read_total",
				"The page's total did not come back with the form; draw the cart again.")
		}
		orderID, _, err := u.carts.Complete(ctx, id,
			strings.TrimSpace(r.PostFormValue(formSalesChannelID)),
			strings.TrimSpace(r.PostFormValue(formPaymentMethod)), total)
		if err != nil {
			return "", err
		}

		return OrdersPath + "/" + orderID, nil
	})
}

// cartWrite runs one of the cart page's writes and goes to the page it names,
// the cart's own when it names none; a refusal is drawn on the cart's page
// with what was typed.
func (u *UI) cartWrite(
	w http.ResponseWriter, r *http.Request, write func(ctx context.Context, id string) (string, error),
) {
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

	next, err := write(r.Context(), id)
	switch {
	case err == nil:
		if next == "" {
			next = CartsPath + "/" + id
		}
		corehttp.WriteRedirect(r.Context(), w, next)
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		u.renderCart(w, r, http.StatusUnprocessableEntity, id, messageFor(err), r.PostForm)
	default:
		u.unexpectedFailure(w, r, err, "The cart could not be changed")
	}
}
