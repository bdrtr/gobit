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
)

// A promotion's discount value on its page (ADR 0338): how much it gives,
// changed through the promotion module's panel surface from the type and the
// value the page was drawn with.

// PromotionDiscountPath takes the form that changes how much a promotion's
// discount gives.
const PromotionDiscountPath = PromotionPath + "/discount"

// The discount form's fields: the value as typed, and the type and the value
// the page was drawn with, the currency a fixed amount is read in.
const (
	formDiscountValue    = "value"
	formDiscountCurrency = "currency"
	formReadMethodType   = "read_type"
	formReadMethodValue  = "read_value"
)

// measurePercentage is a discount measured in basis points of what it lands
// on, beside the fixed amount [measureFixed] names.
const measurePercentage = "percentage"

// DiscountReviser is the narrow surface a promotion's discount value is
// changed through (ADR 0338).
type DiscountReviser interface {
	// ReviseDiscountValue changes the value, in minor units for a fixed
	// discount and basis points for a percentage, from the type and the value
	// read.
	ReviseDiscountValue(ctx context.Context, promotionID, readType string, readValue, value int64) error
}

// discountForm is what the discount form is drawn with.
type discountForm struct {
	ReadType  string
	ReadValue int64
	Value     string
	Currency  string
	Refused   bool
}

// canReviseDiscount reports whether the operator may change a discount here.
func (u *UI) canReviseDiscount(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, ok := u.promotions.(DiscountReviser)

	return ok && principal.HasScope(scopePromotionWrite)
}

// discountValueText is a discount's value as the form shows it: a percentage
// as a percent, a fixed amount in its currency's decimals.
func discountValueText(kind string, value int64, currency string, scales map[string]int) string {
	if kind == measurePercentage {
		return percentText(value)
	}
	text, _ := formatAmount(value, currency, scales)

	return text
}

// reviseDiscount changes the promotion's discount value from the type and
// the value the page was drawn with and returns to the page, which says so;
// a refusal, a discount changed since included, comes back on the page with
// what was typed (ADR 0338).
func (u *UI) reviseDiscount(w http.ResponseWriter, r *http.Request) {
	reviser, ok := u.promotions.(DiscountReviser)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Promotions unavailable",
			"The promotion module's panel surface cannot change a discount in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	id := chi.URLParam(r, "id")
	kind := r.PostFormValue(formReadMethodType)
	readValue, err := strconv.ParseInt(r.PostFormValue(formReadMethodValue), 10, 64)
	if err != nil {
		err = errors.Invalid("admin_ui_read_discount",
			"The discount the page was drawn with could not be read; draw the page again.")
	}
	var value int64
	if err == nil {
		value, err = u.discountValue(r, kind)
	}
	if err == nil {
		err = reviser.ReviseDiscountValue(r.Context(), id, kind, readValue, value)
	}
	switch {
	case err == nil:
		corehttp.WriteRedirect(r.Context(), w, PromotionsPath+"/"+id+"?"+url.Values{paramWritten: {"1"}}.Encode())
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		u.renderPromotionTyped(w, r, http.StatusUnprocessableEntity, messageFor(err), r.PostForm)
	default:
		u.unexpectedFailure(w, r, err, "The discount could not be changed")
	}
}

// discountValue reads the typed value in the discount's unit: a percent to
// two decimals into basis points, a fixed amount in its currency's decimals,
// or minor units when the panel cannot read the currency's scale.
func (u *UI) discountValue(r *http.Request, kind string) (int64, error) {
	text := strings.TrimSpace(r.PostFormValue(formDiscountValue))
	if kind == measurePercentage {
		return parseAmount(text, 2, false)
	}
	scale, known := u.currencyScales(r.Context())[r.PostFormValue(formDiscountCurrency)]

	return parseAmount(text, scale, !known)
}
