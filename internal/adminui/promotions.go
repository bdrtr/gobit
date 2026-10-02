package adminui

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// The promotions screen (ADR 0311): the shop's promotions in one status at a
// time with their usage, read through the promotion module's panel surface
// because its read provider keeps drafts, inactive promotions and usage out.

// ServicePromotionAdmin is the promotion module's panel surface, spelled by
// hand and pinned against the module's constant in internal/arch.
const ServicePromotionAdmin = "promotion.admin"

// PromotionsPath lists the promotions, and PromotionStatusPath switches one's
// status (ADR 0312).
const (
	PromotionsPath      = URLPrefix + "/promotions"
	PromotionStatusPath = PromotionsPath + "/{id}/status"
)

// promotionsLabel is what the section is called on screen.
const promotionsLabel = "Promotions"

// scopePromotionRead and scopePromotionWrite are the promotion module's
// privileges, as the admin API names them.
const (
	scopePromotionRead  = "promotion:read"
	scopePromotionWrite = "promotion:write"
)

// paramPromotionStatus chooses the status listed; the promotion module's
// statuses, live ones first.
const paramPromotionStatus = "status"

// The promotion module's statuses, as its surface spells them.
const (
	promotionActive   = "active"
	promotionInactive = "inactive"
)

// promotionStatuses are the statuses the screen offers, in the order of its
// tabs; the first is listed when none is chosen.
var promotionStatuses = []string{promotionActive, statusDraft, promotionInactive}

// promotionsPerPage is the list's page size, the other lists'.
const promotionsPerPage = 25

// The status switch's form fields: the status the row was drawn in, and the
// one the operator moves it to (ADR 0312).
const (
	formStatusFrom = "from"
	formStatusTo   = "to"
)

// promotionSwitch is the one move the screen offers a promotion in a status:
// a draft is published, an active one paused, an inactive one resumed.
type promotionSwitch struct {
	To    string
	Label string
}

// promotionSwitches maps a status to its move.
var promotionSwitches = map[string]promotionSwitch{
	statusDraft:       {To: promotionActive, Label: "Publish"},
	promotionActive:   {To: promotionInactive, Label: "Pause"},
	promotionInactive: {To: promotionActive, Label: "Resume"},
}

// PromotionLister is the narrow surface the screen reads through (ADR 0001).
type PromotionLister interface {
	// PromotionsJSON lists the promotions in the status a page at a time, and
	// the total in the status.
	PromotionsJSON(ctx context.Context, status string, limit, offset int32) (json.RawMessage, int64, error)
}

// PromotionSwitcher is the narrow surface a promotion's status is switched
// through (ADR 0312).
type PromotionSwitcher interface {
	// SwitchPromotionStatus moves the promotion from the status the operator
	// read to another, and refuses when it is no longer in the first.
	SwitchPromotionStatus(ctx context.Context, id, from, to string) error
}

// CouponCreator is the narrow surface a coupon is written through (ADR 0314).
type CouponCreator interface {
	// CreateCoupon writes a draft coupon with its discount and returns its id.
	CreateCoupon(
		ctx context.Context, code, measure, target, allocation string, value int64, currency string, usageLimit *int64,
	) (string, error)
}

// The coupon form's fields (ADR 0314).
const (
	formCouponCode       = "code"
	formCouponMeasure    = "measure"
	formCouponAmount     = "amount"
	formCouponCurrency   = "currency"
	formCouponTarget     = "target"
	formCouponAllocation = "allocation"
	formCouponLimit      = "usage_limit"
)

// measureFixed is the measure whose amount is money; the other is a
// percentage.
const measureFixed = "fixed"

// canCreateCoupons reports whether the operator may write a coupon here.
func (u *UI) canCreateCoupons(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, ok := u.promotions.(CouponCreator)

	return ok && principal.HasScope(scopePromotionWrite)
}

// createCoupon writes the coupon the form describes and goes to its page; a
// refusal comes back on the drafts' list with what was typed (ADR 0314).
func (u *UI) createCoupon(w http.ResponseWriter, r *http.Request) {
	creator, ok := u.promotions.(CouponCreator)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Promotions unavailable",
			"The promotion module's panel surface cannot write a coupon in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	id, err := u.writeCoupon(r, creator)
	switch {
	case err == nil:
		corehttp.WriteRedirect(r.Context(), w, PromotionsPath+"/"+id)
	case errors.IsInvalid(err) || errors.IsConflict(err):
		u.renderPromotions(w, r, http.StatusUnprocessableEntity, statusDraft, messageFor(err), r.PostForm)
	default:
		u.unexpectedFailure(w, r, err, "The coupon could not be written")
	}
}

// writeCoupon reads the form into the surface's terms: a percentage into
// basis points, a fixed amount into its currency's minor units, or taken as
// minor units when the currency's scale is unknown, as a price is.
func (u *UI) writeCoupon(r *http.Request, creator CouponCreator) (string, error) {
	measure := r.PostFormValue(formCouponMeasure)
	currency := strings.ToUpper(strings.TrimSpace(r.PostFormValue(formCouponCurrency)))

	var value int64
	var err error
	if measure == measureFixed {
		scale, known := u.currencyScales(r.Context())[currency]
		value, err = parseAmount(r.PostFormValue(formCouponAmount), scale, !known)
	} else {
		value, err = parseAmount(r.PostFormValue(formCouponAmount), 2, false)
		if err != nil {
			err = errors.Invalid(CodeAmountInvalid, "A percentage is a number with at most two decimal places, such as 12.5.")
		}
	}
	if err != nil {
		return "", err
	}

	var limit *int64
	if text := strings.TrimSpace(r.PostFormValue(formCouponLimit)); text != "" {
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return "", errors.Invalid(CodeAmountInvalid, "The usage limit is a whole number.")
		}
		limit = &n
	}

	return creator.CreateCoupon(r.Context(), r.PostFormValue(formCouponCode), measure,
		r.PostFormValue(formCouponTarget), r.PostFormValue(formCouponAllocation), value, currency, limit)
}

// canSwitchPromotions reports whether the operator may switch a status here:
// the surface can, and the operator holds the module's write privilege.
func (u *UI) canSwitchPromotions(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, ok := u.promotions.(PromotionSwitcher)

	return ok && principal.HasScope(scopePromotionWrite)
}

// promotionRow is one promotion as the module's surface sends it; the json
// tags are the contract with that surface, exercised end to end.
type promotionRow struct {
	ID          string    `json:"id"`
	Code        string    `json:"code"`
	IsAutomatic bool      `json:"is_automatic"`
	Type        string    `json:"type"`
	Status      string    `json:"status"`
	UsageCount  int64     `json:"usage_count"`
	UsageLimit  *int64    `json:"usage_limit"`
	CreatedAt   time.Time `json:"created_at"`
}

// listPromotions renders the promotions in the chosen status.
func (u *UI) listPromotions(w http.ResponseWriter, r *http.Request) {
	if u.promotions == nil {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Promotions unavailable",
			"The promotion module's panel surface is not registered in this installation.")
		return
	}
	u.renderPromotions(w, r, http.StatusOK, r.URL.Query().Get(paramPromotionStatus), "", url.Values{})
}

// switchPromotion moves the promotion in the path from the status its row was
// drawn in to the one the operator chose, and returns to that row's list; a
// refusal, a promotion another operator moved first included, is drawn on the
// list (ADR 0312).
func (u *UI) switchPromotion(w http.ResponseWriter, r *http.Request) {
	switcher, ok := u.promotions.(PromotionSwitcher)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Promotions unavailable",
			"The promotion module's panel surface cannot switch a status in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	from := r.PostFormValue(formStatusFrom)
	err := switcher.SwitchPromotionStatus(r.Context(), chi.URLParam(r, "id"), from, r.PostFormValue(formStatusTo))
	switch {
	case err == nil:
		corehttp.WriteRedirect(r.Context(), w,
			PromotionsPath+"?"+url.Values{paramPromotionStatus: {from}}.Encode())
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		u.renderPromotions(w, r, http.StatusUnprocessableEntity, from, messageFor(err), url.Values{})
	default:
		u.unexpectedFailure(w, r, err, "The promotion could not be switched")
	}
}

// renderPromotions lists the promotions in the status, the first tab's when
// it is none of them, with a refused write's reason and what was typed. An
// operator who may write and not read the list is told the reason alone (ADR
// 0260).
func (u *UI) renderPromotions(
	w http.ResponseWriter, r *http.Request, code int, status, refused string, typed url.Values,
) {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	if refused != "" && !principal.HasScope(scopePromotionRead) {
		u.errorPage(w, r, code, "Not done", refused)
		return
	}
	if !slices.Contains(promotionStatuses, status) {
		status = promotionStatuses[0]
	}
	page := pageNumber(r.URL.Query().Get("page"))

	offset := (page - 1) * promotionsPerPage
	if offset > math.MaxInt32 {
		offset = math.MaxInt32
	}
	raw, total, err := u.promotions.PromotionsJSON(r.Context(), status,
		promotionsPerPage, int32(offset))
	if err != nil {
		u.unexpectedFailure(w, r, err, "The promotions could not be read")
		return
	}
	var rows []promotionRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		u.unexpectedFailure(w, r, err, "The promotions could not be read")
		return
	}

	data := map[string]any{
		titleKey:     promotionsLabel,
		"Promotions": rows,
		statusKey:    status,
		statusesKey:  promotionStatuses,
		totalKey:     total,
		refusedKey:   refused,
		typedKey:     typed,
	}
	data[canCreateKey] = u.canCreateCoupons(r)
	if u.canSwitchPromotions(r) {
		data["Switch"] = promotionSwitches[status]
	}
	addPaging(data, page, int64(page*promotionsPerPage) < total, PromotionsPath)

	u.templates.render(w, r, code, "promotions.gohtml", data)
}
