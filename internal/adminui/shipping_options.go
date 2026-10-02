package adminui

import (
	"context"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// The shipping options screen (ADR 0333): the fulfillment module's options
// with their provider, profile, region and fee, read through its
// shipping_option entity, and each row's form that renames the option, sets
// its fee and says whether the storefront offers it, from what the row was
// drawn with, through the module's panel surface.

const (
	// ShippingOptionsPath lists the shipping options.
	ShippingOptionsPath = URLPrefix + "/shipping-options"
	// ShippingOptionRevisePath takes a row's form that revises its option.
	ShippingOptionRevisePath = ShippingOptionsPath + "/{id}"
)

// EntityShippingOption is the fulfillment module's option entity in the read
// layer, spelled by hand and pinned against the module's in internal/arch.
const EntityShippingOption = "shipping_option"

// shippingOptionsLabel is what the section is called on screen.
const shippingOptionsLabel = "Shipping options"

// shippingOptionsPerPage is the list's page size, the other lists'.
const shippingOptionsPerPage = 25

// The option entity's fields beside the ones every entity has.
const (
	fieldOptionProvider = "provider_id"
	fieldOptionProfile  = "shipping_profile_id"
	fieldOptionPrice    = "price_type"
	fieldOptionRegion   = "region_id"
	fieldOptionReturn   = "is_return"
	fieldOptionAdmin    = "admin_only"
)

// priceFlat is the price type whose fee the option carries; the other,
// calculated, takes its fee from the provider.
const priceFlat = "flat"

// The revise form's fields beside the name and the read ones the other
// revise forms share: the fee, whether the storefront hides the option, and
// what the row was drawn with to read the fee in.
const (
	formOptionAmount       = "amount"
	formOptionAdminOnly    = "admin_only"
	formReadAdminOnly      = "read_admin_only"
	formOptionPriceType    = "price_type"
	formOptionCurrencyCode = "currency_code"
)

// ShippingOptionReviser is the narrow surface a shipping option's terms are
// revised through (ADR 0333).
type ShippingOptionReviser interface {
	// ReviseShippingOption writes the option's name, fee in minor units and
	// storefront visibility, and refuses when they are no longer the ones
	// read.
	ReviseShippingOption(
		ctx context.Context, id, readName string, readAmount int64, readAdminOnly bool,
		name string, amount int64, adminOnly bool,
	) error
}

// shippingOptionRow is one option as the list prints it, with what its form
// offers: the option as drawn, or what was typed in the form a refusal came
// back to.
type shippingOptionRow struct {
	ID            string
	Name          string
	Provider      string
	Profile       string
	PriceType     string
	Amount        int64
	Currency      string
	Fee           string
	Region        string
	IsReturn      bool
	AdminOnly     bool
	FormName      string
	FormAmount    string
	FormAdminOnly bool
	Refused       bool
}

// canReviseShippingOptions reports whether the operator may revise an option
// here.
func (u *UI) canReviseShippingOptions(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, ok := u.parcels.(ShippingOptionReviser)

	return ok && principal.HasScope(scopeFulfillmentWrite)
}

// listShippingOptions renders the options.
func (u *UI) listShippingOptions(w http.ResponseWriter, r *http.Request) {
	u.renderShippingOptions(w, r, http.StatusOK, "", url.Values{}, "")
}

// reviseShippingOption writes the terms a row's form was sent with, from the
// ones the row was drawn with, and returns to the list's page, which names the
// option; a refusal, an option another operator revised first included, comes
// back on the page with what was typed in the row (ADR 0333).
func (u *UI) reviseShippingOption(w http.ResponseWriter, r *http.Request) {
	reviser, ok := u.parcels.(ShippingOptionReviser)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Shipping options unavailable",
			"The fulfillment module's panel surface cannot revise a shipping option in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	id := chi.URLParam(r, "id")
	name := strings.TrimSpace(r.PostFormValue(formGroupName))
	err := u.sendOptionRevision(r, reviser, id, name)
	switch {
	case err == nil:
		landing := url.Values{paramCreated: {name}}
		if page := pageNumber(r.URL.Query().Get("page")); page > 1 {
			landing.Set("page", strconv.Itoa(page))
		}
		corehttp.WriteRedirect(r.Context(), w, ShippingOptionsPath+"?"+landing.Encode())
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		u.renderShippingOptions(w, r, http.StatusUnprocessableEntity, messageFor(err), r.PostForm, id)
	default:
		u.unexpectedFailure(w, r, err, "The shipping option could not be revised")
	}
}

// sendOptionRevision reads the row's drawn terms and the typed ones and sends
// them. A calculated option's fee is its provider's, so its form carries none
// and none is sent.
func (u *UI) sendOptionRevision(r *http.Request, reviser ShippingOptionReviser, id, name string) error {
	readAmount, amountErr := strconv.ParseInt(r.PostFormValue(formReadAmount), 10, 64)
	readAdminOnly, adminErr := strconv.ParseBool(r.PostFormValue(formReadAdminOnly))
	if amountErr != nil || adminErr != nil {
		return errors.Invalid("admin_ui_read_option",
			"The fee or the visibility the row was drawn with could not be read; draw the list again.")
	}
	var amount int64
	if r.PostFormValue(formOptionPriceType) == priceFlat {
		scale, known := u.currencyScales(r.Context())[r.PostFormValue(formOptionCurrencyCode)]
		var err error
		if amount, err = parseAmount(r.PostFormValue(formOptionAmount), scale, !known); err != nil {
			return err
		}
	}

	return reviser.ReviseShippingOption(r.Context(), id,
		r.PostFormValue(formReadName), readAmount, readAdminOnly,
		name, amount, r.PostFormValue(formOptionAdminOnly) != "")
}

// renderShippingOptions lists the options, newest first, with a refused
// write's reason and what was typed in the row of the option revised. An
// operator who may write and not read the options is told the reason alone
// (ADR 0260).
func (u *UI) renderShippingOptions(
	w http.ResponseWriter, r *http.Request, code int, refused string, typed url.Values, revised string,
) {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	if refused != "" && !principal.HasScope(scopeFulfillmentRead) {
		u.errorPage(w, r, code, "Not done", refused)
		return
	}

	page := pageNumber(r.URL.Query().Get("page"))
	offset := (page - 1) * shippingOptionsPerPage
	if offset > math.MaxInt32 {
		offset = math.MaxInt32
	}
	records, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity: EntityShippingOption,
		Fields: []string{
			fieldID, fieldName, fieldOptionProvider, fieldOptionProfile, fieldOptionPrice, fieldAmount,
			fieldCurrencyCod, fieldOptionRegion, fieldOptionReturn, fieldOptionAdmin,
		},
		// One more than the page, so whether there is a next one comes out of
		// this read.
		Limit:  shippingOptionsPerPage + 1,
		Offset: offset,
	})
	if err != nil {
		u.catalogFailure(w, r, err, "The shipping options could not be read.")
		return
	}
	more := len(records) > shippingOptionsPerPage
	if more {
		records = records[:shippingOptionsPerPage]
	}
	scales := u.currencyScales(r.Context())
	rows := make([]shippingOptionRow, 0, len(records))
	for _, rec := range records {
		rows = append(rows, shippingOptionOf(rec, scales, typed, revised))
	}

	data := map[string]any{
		titleKey:        shippingOptionsLabel,
		"Options":       rows,
		createdKey:      r.URL.Query().Get(paramCreated),
		canReviseKey:    u.canReviseShippingOptions(r),
		refusedKey:      refused,
		"FlatPriceType": priceFlat,
	}
	addPaging(data, page, more, ShippingOptionsPath)

	u.templates.render(w, r, code, "shipping_options.gohtml", data)
}

// shippingOptionOf prints one option and its form; the option revised, when
// one was, carries what was typed.
func shippingOptionOf(rec query.Record, scales map[string]int, typed url.Values, revised string) shippingOptionRow {
	row := shippingOptionRow{
		ID: recordString(rec, fieldID), Name: recordString(rec, fieldName),
		Provider: recordString(rec, fieldOptionProvider), Profile: recordString(rec, fieldOptionProfile),
		PriceType: recordString(rec, fieldOptionPrice), Amount: recordInt(rec, fieldAmount),
		Currency: recordString(rec, fieldCurrencyCod), Region: recordString(rec, fieldOptionRegion),
		IsReturn: recordBool(rec, fieldOptionReturn), AdminOnly: recordBool(rec, fieldOptionAdmin),
	}
	if row.PriceType == priceFlat {
		row.FormAmount, _ = formatAmount(row.Amount, row.Currency, scales)
		row.Fee = minorText(row.Amount, row.Currency, scales)
	}
	row.FormName, row.FormAdminOnly = row.Name, row.AdminOnly
	if revised != "" && row.ID == revised {
		row.FormName, row.FormAmount = typed.Get(formGroupName), typed.Get(formOptionAmount)
		row.FormAdminOnly, row.Refused = typed.Get(formOptionAdminOnly) != "", true
	}

	return row
}
