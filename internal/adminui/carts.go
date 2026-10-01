package adminui

import (
	"cmp"
	"context"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// The telephone order: an operator opens a cart for a caller and adds priced
// lines to it (ADR 0290), writes its shipping address, chooses its shipping
// method and completes it with an offline method (ADR 0291), writes its
// billing address (ADR 0303) and corrects it (ADR 0300), through the cart
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
	CartsPath       = URLPrefix + "/carts"
	CartPath        = CartsPath + "/{id}"
	CartLinesPath   = CartPath + "/lines"
	CartAddressPath = CartPath + "/address"
	// CartBillingPath writes the cart's billing address (ADR 0303).
	CartBillingPath  = CartPath + "/billing"
	CartShippingPath = CartPath + "/shipping"
	CartCompletePath = CartPath + "/complete"
	// The operator's corrections of their own cart (ADR 0300): one line
	// removed, the whole cart discarded.
	CartLineRemovePath = CartLinesPath + "/{line}/remove"
	CartDiscardPath    = CartPath + "/discard"
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
	// SetBillingAddress writes the billing address from the same keys (ADR
	// 0303).
	SetBillingAddress(ctx context.Context, cartID string, address map[string]string) error
	// ShippingOptions lists the options the cart can take, each its id, name
	// and amount in the cart's currency (ADR 0292).
	ShippingOptions(ctx context.Context, cartID string) (ids, names []string, amounts []int64, err error)
	// AddShippingMethod prices the shipping option for the cart and adds it.
	AddShippingMethod(ctx context.Context, cartID, shippingOptionID string) (string, error)
	// RemoveLine removes a line from an operator's cart and reprices it (ADR
	// 0300).
	RemoveLine(ctx context.Context, cartID, lineID string) error
	// Discard deletes an operator's cart that will not be completed (ADR 0300).
	Discard(ctx context.Context, cartID string) error
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
	fieldCartBillingAddress  = "billing_address"
	fieldCartShippingMethods = "shipping_methods"
	cartMethodName           = "name"
	cartMethodAmount         = "amount"
)

// addressFields are the address form's fields, the cart surface's keys, in the
// order the form asks for them.
var addressFields = []string{
	"first_name", "last_name", "company", "address_1", "city", "postal_code", "country_code", "phone",
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
	// OpenedBy is the operator who opened the cart; empty on a shopper's,
	// which the page only reads (ADR 0296, ADR 0299).
	OpenedBy string
	// TotalMinor is the total in minor units, carried by the completion form.
	TotalMinor int64
	// ShipTo is the shipping address, a line a part; Address its fields, to
	// draw the form with what is there.
	ShipTo  []string
	Address map[string]string
	// BillTo is the billing address, a line a part; Billing the fields the
	// billing form is drawn with: the billing address, or the shipping
	// address when none was written, for the operator to confirm (ADR 0303).
	BillTo  []string
	Billing map[string]string
	// Methods are the chosen shipping methods, each its name and amount.
	Methods []cartMethod
}

// cartMethod is one chosen shipping method.
type cartMethod struct {
	Name, Amount string
}

// paramFind is the cart page's search: the text typed to find a product by its
// title, whose variants the add form then offers (ADR 0293).
const paramFind = "find"

// productsFound is how many products one search reads.
const productsFound = 10

// fieldVariantProduct is the product a variant record belongs to.
const fieldVariantProduct = "product_id"

// foundVariant is one variant a search offers to the add form.
type foundVariant struct {
	ID, Label string
}

// findVariants reads the variants of the products whose title matches the
// term, in the products' order, each labeled with its product, its title and
// its SKU.
func (u *UI) findVariants(r *http.Request, term string) ([]foundVariant, error) {
	products, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity:  EntityProduct,
		Fields:  []string{fieldID, fieldTitle},
		Filters: map[string]any{filterSearch: term},
		Limit:   productsFound,
	})
	if err != nil {
		return nil, err
	}
	if len(products) == 0 {
		return []foundVariant{}, nil
	}

	ids := make([]string, 0, len(products))
	titles := make(map[string]string, len(products))
	for _, product := range products {
		id := recordString(product, fieldID)
		ids = append(ids, id)
		titles[id] = recordString(product, fieldTitle)
	}
	variants, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity:  EntityVariant,
		Fields:  []string{fieldID, fieldTitle, fieldSKU, fieldVariantProduct},
		Filters: map[string]any{filterProductID: ids},
	})
	if err != nil {
		return nil, err
	}

	byProduct := make(map[string][]foundVariant, len(ids))
	for _, variant := range variants {
		label := titles[recordString(variant, fieldVariantProduct)] + " — " + recordString(variant, fieldTitle)
		if sku := recordString(variant, fieldSKU); sku != "" {
			label += " (" + sku + ")"
		}
		product := recordString(variant, fieldVariantProduct)
		byProduct[product] = append(byProduct[product], foundVariant{ID: recordString(variant, fieldID), Label: label})
	}
	out := []foundVariant{}
	for _, id := range ids {
		out = append(out, byProduct[id]...)
	}

	return out, nil
}

// openCartsShown is how many open operator carts the telephone order's page
// lists, the newest first (ADR 0296).
const openCartsShown = 20

// The cart field and filter the open carts' list reads, the cart module's
// names (ADR 0296), pinned against the module's in internal/arch.
const (
	FieldCartOpenedBy      = "opened_by"
	FilterOpenedByOperator = "opened_by_operator"
)

// openCart is one cart an operator opened and nobody completed, as the
// telephone order's page lists it.
type openCart struct {
	ID, Email, CustomerID, OpenedBy, Total string
	OpenedAt                               time.Time
}

// openCarts reads the carts operators opened and nobody completed, the newest
// first, and whether they could be read (ADR 0296).
func (u *UI) openCarts(r *http.Request) ([]openCart, bool) {
	records, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity: EntityCart,
		Fields: []string{
			fieldID, fieldEmail, fieldCartCustomerID, FieldCartOpenedBy,
			fieldCurrencyCod, fieldTotal, fieldCreatedAt,
		},
		Filters: map[string]any{fieldCartCompleted: false, FilterOpenedByOperator: true},
		Limit:   openCartsShown,
	})
	if err != nil {
		return nil, false
	}

	scales := u.currencyScales(r.Context())
	out := make([]openCart, 0, len(records))
	for _, record := range records {
		currency := recordString(record, fieldCurrencyCod)
		total, known := amountField(record, fieldTotal, currency, scales)
		out = append(out, openCart{
			ID:         recordString(record, fieldID),
			Email:      recordString(record, fieldEmail),
			CustomerID: recordString(record, fieldCartCustomerID),
			OpenedBy:   recordString(record, FieldCartOpenedBy),
			Total:      withCurrency(total, currency, known),
			OpenedAt:   recordTime(record, fieldCreatedAt),
		})
	}

	return out, true
}

// paramCaller is the telephone order page's search: the caller's e-mail, whose
// customer records the open form then offers (ADR 0297).
const paramCaller = "caller"

// callersFound is how many customer records one search reads.
const callersFound = 10

// filterCustomerEmail is the customer provider's e-mail filter, an exact match
// on the address as the customer module normalizes it.
const filterCustomerEmail = "email"

// caller is one customer record the search found, as the open form offers it.
type caller struct {
	ID, Label  string
	HasAccount bool
}

// findCallers reads the customer records holding the e-mail, the account
// before the guest records, each labeled with its name, its kind and its id.
func (u *UI) findCallers(r *http.Request, email string) ([]caller, error) {
	records, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity:  EntityCustomer,
		Fields:  []string{fieldID, fieldEmail, fieldFirstName, fieldLastName, fieldHasAccount},
		Filters: map[string]any{filterCustomerEmail: email},
		Limit:   callersFound,
	})
	if err != nil {
		return nil, err
	}

	out := make([]caller, 0, len(records))
	for _, record := range records {
		row := customerRowOf(record)
		kind := "guest record"
		if row.HasAccount {
			kind = "account"
		}
		out = append(out, caller{
			ID: row.ID, HasAccount: row.HasAccount,
			Label: row.display() + " — " + kind + " (" + row.ID + ")",
		})
	}
	// An e-mail holds one account and any number of guest records (the
	// customer module's unique index), and the account is the one a caller
	// who signs in would see the order under.
	slices.SortStableFunc(out, func(a, b caller) int {
		return -cmp.Compare(boolRank(a.HasAccount), boolRank(b.HasAccount))
	})

	return out, nil
}

// boolRank orders true before false.
func boolRank(b bool) int {
	if b {
		return 1
	}

	return 0
}

// EntitySalesChannel is the auth module's sales channel entity in the read
// layer, pinned against the module's in internal/arch (ADR 0305).
const EntitySalesChannel = "sales_channel"

// The sales channel's fields and filter the channel list reads.
const (
	fieldChannelName     = "name"
	filterChannelOff     = "is_disabled"
	channelsListedAtMost = 50
)

// channelOption is one sales channel the forms offer.
type channelOption struct {
	ID, Name string
}

// channelsOf reads the enabled sales channels, by name, for the forms that
// claim one, and whether they could be read (ADR 0305).
func (u *UI) channelsOf(r *http.Request) ([]channelOption, bool) {
	records, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity:  EntitySalesChannel,
		Fields:  []string{fieldID, fieldChannelName},
		Filters: map[string]any{filterChannelOff: false},
		Limit:   channelsListedAtMost,
	})
	if err != nil {
		return nil, false
	}

	out := make([]channelOption, 0, len(records))
	for _, record := range records {
		out = append(out, channelOption{ID: recordString(record, fieldID), Name: recordString(record, fieldChannelName)})
	}
	slices.SortStableFunc(out, func(a, b channelOption) int { return strings.Compare(a.Name, b.Name) })

	return out, true
}

// cartOption is one shipping option the cart can take, as the form offers it.
type cartOption struct {
	ID, Name, Amount string
}

// shippingOptionsOf lists the options the cart can take for its form, and
// whether they could be read; a page whose listing failed offers the option's
// id as a text box instead (ADR 0292).
func (u *UI) shippingOptionsOf(r *http.Request, page cartPage, scales map[string]int) ([]cartOption, bool) {
	ids, names, amounts, err := u.carts.ShippingOptions(r.Context(), page.ID)
	if err != nil || len(names) != len(ids) || len(amounts) != len(ids) {
		return nil, false
	}

	out := make([]cartOption, 0, len(ids))
	for i := range ids {
		amount, known := formatAmount(amounts[i], page.Currency, scales)
		out = append(out, cartOption{ID: ids[i], Name: names[i], Amount: withCurrency(amount, page.Currency, known)})
	}

	return out, true
}

// newTelephoneOrder renders the form that opens a cart, with the caller's
// e-mail when the page was asked to find them (ADR 0297).
func (u *UI) newTelephoneOrder(w http.ResponseWriter, r *http.Request) {
	typed := url.Values{}
	if term := strings.TrimSpace(r.URL.Query().Get(paramCaller)); term != "" {
		typed.Set(paramCaller, term)
		typed.Set(formEmail, term)
	}
	u.renderOpenForm(w, r, http.StatusOK, "", typed)
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

// renderOpenForm writes the form with a refusal and what was typed, the open
// operator carts to an operator who may read carts (ADR 0296), and the
// caller's customer records to one who may read customers (ADR 0297); each
// module's data is read under that module's privilege (ADR 0260).
func (u *UI) renderOpenForm(w http.ResponseWriter, r *http.Request, status int, refused string, typed url.Values) {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	canList := principal.HasScope(scopeCartRead)
	var open []openCart
	openRead := false
	if canList {
		open, openRead = u.openCarts(r)
	}

	canFind := principal.HasScope(scopeCustomerRead)
	term := strings.TrimSpace(typed.Get(paramCaller))
	var callers []caller
	findRefused := ""
	if canFind && term != "" {
		var err error
		callers, err = u.findCallers(r, term)
		switch {
		case errors.IsInvalid(err):
			findRefused = "That is not an e-mail address."
		case err != nil:
			findRefused = "The customers could not be read."
		}
	}
	// The account is chosen until the operator chooses otherwise; a refused
	// form keeps what they chose, a guest included.
	selected := typed.Get(formCustomerID)
	if !typed.Has(formCustomerID) {
		for _, found := range callers {
			if found.HasAccount {
				selected = found.ID
				break
			}
		}
	}

	u.templates.render(w, r, status, "carts.gohtml", map[string]any{
		titleKey:    telephoneLabel,
		"CartsPath": CartsPath,
		refusedKey:  refused,
		typedKey:    typed,
		"CanList":   canList,
		"Open":      open,
		"OpenRead":  openRead,
		"OpenShown": openCartsShown,
		// The caller's search (ADR 0297).
		"CanFind":     canFind,
		"Caller":      term,
		"Callers":     callers,
		"FindRefused": findRefused,
		"Selected":    selected,
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

// removeCartLine removes the line in the path and returns to the cart's page
// (ADR 0300).
func (u *UI) removeCartLine(w http.ResponseWriter, r *http.Request) {
	u.cartWrite(w, r, func(ctx context.Context, id string) (string, error) {
		return "", u.carts.RemoveLine(ctx, id, chi.URLParam(r, "line"))
	})
}

// discardCart deletes the cart and returns to the telephone order's page,
// where it is no longer listed (ADR 0300).
func (u *UI) discardCart(w http.ResponseWriter, r *http.Request) {
	u.cartWrite(w, r, func(ctx context.Context, id string) (string, error) {
		if err := u.carts.Discard(ctx, id); err != nil {
			return "", err
		}

		return CartsPath, nil
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
			fieldCartShippingAddress, fieldCartShippingMethods, FieldCartOpenedBy,
			fieldCartBillingAddress,
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

	scales := u.currencyScales(r.Context())
	page := cartPageOf(records[0], scales)
	// A cart a shopper opened is read here and changed by no form: the
	// surface refuses an operator's write to it (ADR 0299).
	canWrite := u.carts != nil && principal.HasScope(scopeCartWrite) && page.OpenedBy != ""
	// A customer's cart with no shipping address draws its address form with
	// the customer's default shipping address, read under the customer
	// module's privilege (ADR 0260, ADR 0304). Nothing is written until the
	// operator saves it.
	prefilled := false
	if canWrite && !page.Completed && page.CustomerID != "" && len(page.ShipTo) == 0 &&
		principal.HasScope(scopeCustomerRead) {
		if address := u.customerDefaultAddress(r, page.CustomerID); len(address) > 0 {
			page.Address = address
			if len(page.BillTo) == 0 {
				page.Billing = address
			}
			prefilled = true
		}
	}
	var options []cartOption
	optionsRead := false
	if canWrite && !page.Completed {
		options, optionsRead = u.shippingOptionsOf(r, page, scales)
	}
	// The channel the line and the completion claim is offered by name to an
	// operator who may read the sales channels (ADR 0260, ADR 0305).
	var channels []channelOption
	if canWrite && !page.Completed && principal.HasScope(scopeAuthRead) {
		channels, _ = u.channelsOf(r)
	}
	// The offline methods the completion takes are the payment module's,
	// offered under its privilege (ADR 0306).
	var methods []string
	if canWrite && !page.Completed && u.payments != nil && principal.HasScope(scopePaymentRead) {
		methods = u.payments.OfflineMethods(r.Context())
	}
	// The search reads the product module's catalog, so it is offered only to
	// an operator who may read it (ADR 0260, ADR 0293).
	canSearch := canWrite && !page.Completed && principal.HasScope(scopeProductRead)
	search := strings.TrimSpace(r.URL.Query().Get(paramFind))
	var found []foundVariant
	findFailed := false
	if canSearch && search != "" {
		var err error
		if found, err = u.findVariants(r, search); err != nil {
			findFailed = true
		}
	}

	u.templates.render(w, r, status, "cart.gohtml", map[string]any{
		titleKey:      telephoneLabel,
		"Cart":        page,
		"CartsPath":   CartsPath,
		"CanWrite":    canWrite,
		"Options":     options,
		"OptionsRead": optionsRead,
		"CanSearch":   canSearch,
		"Search":      search,
		"Found":       found,
		"FindFailed":  findFailed,
		refusedKey:    refused,
		typedKey:      typed,
		"Prefilled":   prefilled,
		"Channels":    channels,
		"Methods":     methods,
		// AddressFields orders the address forms (ADR 0291, ADR 0303).
		"AddressFields": addressFields,
		"BillingPrefix": billingPrefix,
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
		OpenedBy:    recordString(record, FieldCartOpenedBy),
	}
	var known bool
	page.Total, known = amountField(record, fieldTotal, page.Currency, scales)
	page.Unscaled = !known
	page.TotalMinor = recordInt(record, fieldTotal)
	page.ShipTo = addressLines(record[fieldCartShippingAddress])
	page.Address = addressValues(record[fieldCartShippingAddress])
	page.BillTo = addressLines(record[fieldCartBillingAddress])
	page.Billing = addressValues(record[fieldCartBillingAddress])
	if len(page.BillTo) == 0 {
		page.Billing = page.Address
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

// fieldCustomerDefaultShippingAddress is the customer provider's default
// shipping address, keyed as a cart's address is (ADR 0304).
const fieldCustomerDefaultShippingAddress = "default_shipping_address"

// customerDefaultAddress reads the customer's default shipping address into the
// form's fields; empty when they have none or it could not be read, and the
// form is then drawn empty as before.
func (u *UI) customerDefaultAddress(r *http.Request, customerID string) map[string]string {
	records, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity:  EntityCustomer,
		Fields:  []string{fieldID, fieldCustomerDefaultShippingAddress},
		Filters: map[string]any{filterID: []string{customerID}},
		Limit:   1,
	})
	if err != nil || len(records) == 0 || records[0][fieldCustomerDefaultShippingAddress] == nil {
		return nil
	}

	return addressValues(records[0][fieldCustomerDefaultShippingAddress])
}

// addressValues reads an address field into the form's fields; empty when the
// cart has none.
func addressValues(value any) map[string]string {
	out := map[string]string{}
	if address, ok := value.(map[string]any); ok {
		for _, name := range addressFields {
			out[name] = stringValue(address[name])
		}
	}

	return out
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

// billingPrefix names the billing form's fields apart from the shipping form's,
// so a refused billing write draws the shipping form with what it holds.
const billingPrefix = "billing_"

// setCartBilling writes the billing address and returns to the cart (ADR
// 0303).
func (u *UI) setCartBilling(w http.ResponseWriter, r *http.Request) {
	u.cartWrite(w, r, func(ctx context.Context, id string) (string, error) {
		address := make(map[string]string, len(addressFields))
		for _, name := range addressFields {
			address[name] = strings.TrimSpace(r.PostFormValue(billingPrefix + name))
		}

		return "", u.carts.SetBillingAddress(ctx, id, address)
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
