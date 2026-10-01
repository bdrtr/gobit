package adminui

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"slices"
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
	u.renderPromotions(w, r, http.StatusOK, r.URL.Query().Get(paramPromotionStatus), "")
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
		u.renderPromotions(w, r, http.StatusUnprocessableEntity, from, messageFor(err))
	default:
		u.unexpectedFailure(w, r, err, "The promotion could not be switched")
	}
}

// renderPromotions lists the promotions in the status, the first tab's when
// it is none of them, with a refused switch's reason. An operator who may
// switch a status and not read the list is told the reason alone (ADR 0260).
func (u *UI) renderPromotions(w http.ResponseWriter, r *http.Request, code int, status, refused string) {
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
		"Status":     status,
		"Statuses":   promotionStatuses,
		"Total":      total,
		refusedKey:   refused,
	}
	if u.canSwitchPromotions(r) {
		data["Switch"] = promotionSwitches[status]
	}
	addPaging(data, page, int64(page*promotionsPerPage) < total, PromotionsPath)

	u.templates.render(w, r, code, "promotions.gohtml", data)
}
