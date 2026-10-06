package adminui

import (
	"context"
	"encoding/json"
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
// drawn with, through the module's panel surface; and the form that writes
// one (ADR 0334).

const (
	// ShippingOptionsPath lists the shipping options and takes the form that
	// writes one.
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
	fieldOptionMinDays  = "delivery_min_days"
	fieldOptionMaxDays  = "delivery_max_days"
)

// priceFlat is the price type whose fee the option carries; the other,
// calculated, takes its fee from the provider.
const priceFlat = "flat"

// The option forms' fields beside the name and the read ones the other
// revise forms share: the fee, whether the storefront hides the option, what
// the row was drawn with to read the fee in, and what a new option is
// written on (ADR 0334).
const (
	formOptionAmount       = "amount"
	formOptionAdminOnly    = "admin_only"
	formReadAdminOnly      = "read_admin_only"
	formOptionPriceType    = "price_type"
	formOptionCurrencyCode = "currency_code"
	formOptionProvider     = "provider_id"
	formOptionProfile      = "shipping_profile_id"
	formOptionRegion       = "region_id"
	formOptionReturn       = "is_return"
	formOptionMinDays      = "delivery_min_days"
	formOptionMaxDays      = "delivery_max_days"
	formReadMinDays        = "read_delivery_min_days"
	formReadMaxDays        = "read_delivery_max_days"
)

// ShippingOptionCreator is the narrow surface a shipping option is written
// through (ADR 0334).
type ShippingOptionCreator interface {
	// OptionChoicesJSON lists the registered providers and the newest
	// profiles an option is written on.
	OptionChoicesJSON(ctx context.Context) (json.RawMessage, error)
	// CreateShippingOption writes an option and returns its id; the fee is in
	// the currency's minor units, an empty region is every region, and the
	// days are how many business days its delivery takes, both nil for none
	// (ADR 0421).
	CreateShippingOption(
		ctx context.Context, name, providerID, profileID, priceType string, amount int64,
		currency, regionID string, isReturn, adminOnly bool, minDays, maxDays *int64,
	) (string, error)
}

// optionChoices is what the surface offers a new option on; the json tags
// are the contract with that surface, exercised end to end.
type optionChoices struct {
	Providers []string `json:"providers"`
	Profiles  []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"profiles"`
	ProfilesMore bool `json:"profiles_more"`
}

// optionRegion is one region a new option may be offered in, with the
// currency its fee is then in.
type optionRegion struct {
	ID       string
	Name     string
	Currency string
}

// ShippingOptionReviser is the narrow surface a shipping option's terms are
// revised through (ADR 0333).
type ShippingOptionReviser interface {
	// ReviseShippingOption writes the option's name, fee in minor units,
	// storefront visibility and delivery days, and refuses when they are no
	// longer the ones read (ADR 0421).
	ReviseShippingOption(
		ctx context.Context, id, readName string, readAmount int64, readAdminOnly bool,
		readMinDays, readMaxDays *int64,
		name string, amount int64, adminOnly bool, minDays, maxDays *int64,
	) error
}

// shippingOptionRow is one option as the list prints it, with what its form
// offers: the option as drawn, or what was typed in the form a refusal came
// back to.
type shippingOptionRow struct {
	ID        string
	Name      string
	Provider  string
	Profile   string
	PriceType string
	Amount    int64
	Currency  string
	Fee       string
	Region    string
	IsReturn  bool
	AdminOnly bool
	// MinDays and MaxDays are the option's business days as drawn, empty when
	// it says none; Days prints them (ADR 0421).
	MinDays       string
	MaxDays       string
	Days          string
	FormName      string
	FormAmount    string
	FormAdminOnly bool
	FormMinDays   string
	FormMaxDays   string
	Refused       bool
}

// canReviseShippingOptions reports whether the operator may revise an option
// here.
func (u *UI) canReviseShippingOptions(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, ok := u.parcels.(ShippingOptionReviser)

	return ok && principal.HasScope(scopeFulfillmentWrite)
}

// canCreateShippingOptions reports whether the operator may write an option
// here.
func (u *UI) canCreateShippingOptions(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, ok := u.parcels.(ShippingOptionCreator)

	return ok && principal.HasScope(scopeFulfillmentWrite)
}

// createShippingOption writes the option the form describes and returns to
// the list, which names it; a refusal comes back on the list with what was
// typed (ADR 0334).
func (u *UI) createShippingOption(w http.ResponseWriter, r *http.Request) {
	creator, ok := u.parcels.(ShippingOptionCreator)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Shipping options unavailable",
			"The fulfillment module's panel surface cannot write a shipping option in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	name := strings.TrimSpace(r.PostFormValue(formGroupName))
	err := u.writeShippingOption(r, creator, name)
	switch {
	case err == nil:
		created := url.Values{paramCreated: {name}}
		corehttp.WriteRedirect(r.Context(), w, ShippingOptionsPath+"?"+created.Encode())
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		u.renderShippingOptions(w, r, http.StatusUnprocessableEntity, messageFor(err), r.PostForm, "")
	default:
		u.unexpectedFailure(w, r, err, "The shipping option could not be written")
	}
}

// writeShippingOption reads the form into the surface's terms: an option in a
// region charges in that region's currency, one in every region in the
// currency typed, and a fee is read in the currency's decimals, or as minor
// units when the panel cannot read its scale.
func (u *UI) writeShippingOption(r *http.Request, creator ShippingOptionCreator, name string) error {
	ctx := r.Context()
	regionID := strings.TrimSpace(r.PostFormValue(formOptionRegion))
	currency := strings.ToUpper(strings.TrimSpace(r.PostFormValue(formOptionCurrencyCode)))
	if regionID != "" {
		region, found := u.optionRegion(ctx, regionID)
		if !found {
			return errors.Invalid("admin_ui_region_unknown",
				"The region %s could not be read; draw the page again.", regionID)
		}
		currency = region.Currency
	}
	var amount int64
	if text := strings.TrimSpace(r.PostFormValue(formOptionAmount)); text != "" {
		scale, known := u.currencyScales(ctx)[currency]
		var err error
		if amount, err = parseAmount(text, scale, !known); err != nil {
			return err
		}
	}

	minDays, maxDays, err := formDays(r.PostFormValue(formOptionMinDays), r.PostFormValue(formOptionMaxDays))
	if err != nil {
		return err
	}

	_, err = creator.CreateShippingOption(ctx, name,
		r.PostFormValue(formOptionProvider), r.PostFormValue(formOptionProfile), r.PostFormValue(formOptionPriceType),
		amount, currency, regionID,
		r.PostFormValue(formOptionReturn) != "", r.PostFormValue(formOptionAdminOnly) != "", minDays, maxDays)

	return err
}

// formDays reads two day fields: both blank says none, and a figure that is not
// a whole number is refused here; the module holds the pair to an option's
// range (ADR 0421).
func formDays(minText, maxText string) (minDays, maxDays *int64, err error) {
	read := func(text string) (*int64, error) {
		text = strings.TrimSpace(text)
		if text == "" {
			return nil, nil
		}
		value, parseErr := strconv.ParseInt(text, 10, 64)
		if parseErr != nil {
			return nil, errors.Invalid("admin_ui_delivery_days",
				"Delivery days are whole numbers of business days: %q is not one.", text)
		}
		return &value, nil
	}
	if minDays, err = read(minText); err != nil {
		return nil, nil, err
	}
	if maxDays, err = read(maxText); err != nil {
		return nil, nil, err
	}

	return minDays, maxDays, nil
}

// optionRegions reads the regions a new option may be offered in.
func (u *UI) optionRegions(ctx context.Context) []optionRegion {
	records, err := u.catalog.Graph(ctx, query.GraphSpec{
		Entity: EntityRegion, Fields: []string{fieldID, fieldName, fieldCurrencyCod}, Limit: regionsOffered,
	})
	if err != nil {
		corehttp.LoggerFromContext(ctx).WarnContext(ctx, "the panel could not read the regions", "error", err)
		return nil
	}
	regions := make([]optionRegion, 0, len(records))
	for _, rec := range records {
		regions = append(regions, optionRegion{
			ID: recordString(rec, fieldID), Name: recordString(rec, fieldName),
			Currency: strings.ToUpper(recordString(rec, fieldCurrencyCod)),
		})
	}

	return regions
}

// optionRegion is the region the form named, as the read layer has it.
func (u *UI) optionRegion(ctx context.Context, id string) (optionRegion, bool) {
	for _, region := range u.optionRegions(ctx) {
		if region.ID == id {
			return region, true
		}
	}

	return optionRegion{}, false
}

// optionChoicesFor is what the new option's form is drawn on, nil when the
// operator may not write one or the surface cannot say.
func (u *UI) optionChoicesFor(r *http.Request) *optionChoices {
	if !u.canCreateShippingOptions(r) {
		return nil
	}
	creator, _ := u.parcels.(ShippingOptionCreator)
	ctx := r.Context()
	raw, err := creator.OptionChoicesJSON(ctx)
	var choices optionChoices
	if err == nil {
		err = json.Unmarshal(raw, &choices)
	}
	if err != nil {
		corehttp.LoggerFromContext(ctx).WarnContext(ctx,
			"the panel could not read what a shipping option is written on", "error", err)
		return nil
	}

	return &choices
}

// regionsOffered is how many regions the new option's form offers; a shop
// sells in a handful.
const regionsOffered = 100

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
	readMin, readMax, daysErr := formDays(r.PostFormValue(formReadMinDays), r.PostFormValue(formReadMaxDays))
	if amountErr != nil || adminErr != nil || daysErr != nil {
		return errors.Invalid("admin_ui_read_option",
			"The fee, the visibility or the days the row was drawn with could not be read; draw the list again.")
	}
	minDays, maxDays, err := formDays(r.PostFormValue(formOptionMinDays), r.PostFormValue(formOptionMaxDays))
	if err != nil {
		return err
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
		r.PostFormValue(formReadName), readAmount, readAdminOnly, readMin, readMax,
		name, amount, r.PostFormValue(formOptionAdminOnly) != "", minDays, maxDays)
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
			fieldOptionMinDays, fieldOptionMaxDays,
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

	if revised != "" {
		// What was typed is the row's, not the new option's.
		typed = url.Values{}
	}
	choices := u.optionChoicesFor(r)
	var regions []optionRegion
	if choices != nil {
		regions = u.optionRegions(r.Context())
	}
	data := map[string]any{
		titleKey:        shippingOptionsLabel,
		"Options":       rows,
		createdKey:      r.URL.Query().Get(paramCreated),
		canReviseKey:    u.canReviseShippingOptions(r),
		"Choices":       choices,
		"Regions":       regions,
		refusedKey:      refused,
		typedKey:        typed,
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
	minDays, hasMin := intValue(rec[fieldOptionMinDays])
	maxDays, hasMax := intValue(rec[fieldOptionMaxDays])
	if hasMin && hasMax {
		row.MinDays, row.MaxDays = strconv.Itoa(minDays), strconv.Itoa(maxDays)
		row.Days = daysText(minDays, maxDays)
	}
	row.FormName, row.FormAdminOnly = row.Name, row.AdminOnly
	row.FormMinDays, row.FormMaxDays = row.MinDays, row.MaxDays
	if revised != "" && row.ID == revised {
		row.FormName, row.FormAmount = typed.Get(formGroupName), typed.Get(formOptionAmount)
		row.FormAdminOnly, row.Refused = typed.Get(formOptionAdminOnly) != "", true
		row.FormMinDays, row.FormMaxDays = typed.Get(formOptionMinDays), typed.Get(formOptionMaxDays)
	}

	return row
}

// daysText prints an option's business days: one figure when the least and the
// most agree, a range otherwise.
func daysText(minDays, maxDays int) string {
	if minDays == maxDays {
		if minDays == 1 {
			return "1 business day"
		}
		return strconv.Itoa(minDays) + " business days"
	}

	return strconv.Itoa(minDays) + "–" + strconv.Itoa(maxDays) + " business days"
}
