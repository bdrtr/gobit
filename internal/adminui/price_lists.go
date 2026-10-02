package adminui

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// The price lists screen (ADR 0326): the pricing module's lists — a sale, an
// override, a segment's contract prices — with their type, status and
// window, and the form that writes one, through the module's panel surface.

// PriceListsPath lists the price lists and takes the form that writes one,
// and PriceListStatusPath switches one's status (ADR 0328).
const (
	PriceListsPath      = URLPrefix + "/price-lists"
	PriceListStatusPath = PriceListsPath + "/{id}/status"
)

// PriceListSwitcher is the narrow surface a price list's status is switched
// through (ADR 0328).
type PriceListSwitcher interface {
	// SwitchPriceListStatus moves the list from the status the operator read
	// to another, and refuses when it is no longer in the first.
	SwitchPriceListStatus(ctx context.Context, id, from, to string) error
}

// The pricing module's list statuses, as its surface spells them, beside the
// draft every module spells alike.
const (
	priceListActive  = "active"
	priceListExpired = "expired"
)

// priceListMoves is the one move the screen offers a list in a status: a
// draft is published, an active list ended, an ended one reopened.
var priceListMoves = map[string]promotionSwitch{
	statusDraft:      {To: priceListActive, Label: "Publish"},
	priceListActive:  {To: priceListExpired, Label: "End"},
	priceListExpired: {To: priceListActive, Label: "Reopen"},
}

// canSwitchPriceLists reports whether the operator may switch a list's status
// here.
func (u *UI) canSwitchPriceLists(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, ok := u.prices.(PriceListSwitcher)

	return ok && principal.HasScope(scopePricingWrite)
}

// switchPriceList moves the list in the path from the status its row was
// drawn in to the one the operator chose and returns to the list; a refusal,
// a list another operator moved first included, is drawn on the list (ADR
// 0328).
func (u *UI) switchPriceList(w http.ResponseWriter, r *http.Request) {
	switcher, ok := u.prices.(PriceListSwitcher)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Price lists unavailable",
			"The pricing module's panel surface cannot switch a price list in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	err := switcher.SwitchPriceListStatus(r.Context(), chi.URLParam(r, "id"),
		r.PostFormValue(formStatusFrom), r.PostFormValue(formStatusTo))
	switch {
	case err == nil:
		corehttp.WriteRedirect(r.Context(), w, PriceListsPath)
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		u.renderPriceLists(w, r, http.StatusUnprocessableEntity, messageFor(err), url.Values{}, "")
	default:
		u.unexpectedFailure(w, r, err, "The price list could not be switched")
	}
}

// priceListsLabel is what the section is called on screen.
const priceListsLabel = "Price lists"

// priceListsPerPage is the list's page size, the other lists'.
const priceListsPerPage = 25

// The price list form's fields.
const (
	formPriceListTitle  = "title"
	formPriceListDesc   = "description"
	formPriceListType   = "type"
	formPriceListStatus = "status"
	formPriceListStarts = "starts_at"
	formPriceListEnds   = "ends_at"
)

// PriceListAdmin is the narrow surface the screen reads and writes through:
// the pricing module's.
type PriceListAdmin interface {
	// PriceListsJSON lists the price lists a page at a time, with the total.
	PriceListsJSON(ctx context.Context, limit, offset int32) (json.RawMessage, int64, error)
	// CreatePriceList writes a price list and returns its id.
	CreatePriceList(
		ctx context.Context, title, description, listType, status string, startsAt, endsAt *time.Time,
	) (string, error)
}

// priceListRow is one price list as the module's surface sends it; the json
// tags are the contract with that surface, exercised end to end.
type priceListRow struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Type        string     `json:"type"`
	Status      string     `json:"status"`
	StartsAt    *time.Time `json:"starts_at"`
	EndsAt      *time.Time `json:"ends_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

// canCreatePriceLists reports whether the operator may write a price list
// here.
func (u *UI) canCreatePriceLists(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, ok := u.prices.(PriceListAdmin)

	return ok && principal.HasScope(scopePricingWrite)
}

// listPriceLists renders the price lists.
func (u *UI) listPriceLists(w http.ResponseWriter, r *http.Request) {
	u.renderPriceLists(w, r, http.StatusOK, "", url.Values{}, "")
}

// createPriceList writes the price list the form describes and returns to the
// list, which names it; a refusal comes back on the list with what was typed
// (ADR 0326).
func (u *UI) createPriceList(w http.ResponseWriter, r *http.Request) {
	admin, ok := u.prices.(PriceListAdmin)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Price lists unavailable",
			"The pricing module's panel surface cannot write a price list in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	title := strings.TrimSpace(r.PostFormValue(formPriceListTitle))
	err := u.writePriceList(r, admin, title)
	switch {
	case err == nil:
		created := url.Values{paramCreated: {title}}
		corehttp.WriteRedirect(r.Context(), w, PriceListsPath+"?"+created.Encode())
	case errors.IsInvalid(err) || errors.IsConflict(err):
		u.renderPriceLists(w, r, http.StatusUnprocessableEntity, messageFor(err), r.PostForm, "")
	default:
		u.unexpectedFailure(w, r, err, "The price list could not be written")
	}
}

// writePriceList reads the form's window in UTC and writes the list.
func (u *UI) writePriceList(r *http.Request, admin PriceListAdmin, title string) error {
	startsAt, err := readWindowMoment(r.PostFormValue(formPriceListStarts), "price list", "start")
	if err != nil {
		return err
	}
	endsAt, err := readWindowMoment(r.PostFormValue(formPriceListEnds), "price list", "end")
	if err != nil {
		return err
	}

	_, err = admin.CreatePriceList(r.Context(), title, strings.TrimSpace(r.PostFormValue(formPriceListDesc)),
		r.PostFormValue(formPriceListType), r.PostFormValue(formPriceListStatus), startsAt, endsAt)

	return err
}

// renderPriceLists lists the price lists with a refused write's reason and
// what was typed: in the new list's form, or in the row of the list revised
// when one was (ADR 0330). An operator who may write and not read the prices
// is told the reason alone (ADR 0260).
func (u *UI) renderPriceLists(
	w http.ResponseWriter, r *http.Request, code int, refused string, typed url.Values, revised string,
) {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	if refused != "" && !principal.HasScope(scopePricingRead) {
		u.errorPage(w, r, code, "Not done", refused)
		return
	}
	admin, ok := u.prices.(PriceListAdmin)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Price lists unavailable",
			"The pricing module's panel surface cannot list the price lists in this installation.")
		return
	}

	page := pageNumber(r.URL.Query().Get("page"))
	offset := (page - 1) * priceListsPerPage
	if offset > math.MaxInt32 {
		offset = math.MaxInt32
	}
	raw, total, err := admin.PriceListsJSON(r.Context(), priceListsPerPage, int32(offset))
	if err != nil {
		u.unexpectedFailure(w, r, err, "The price lists could not be read")
		return
	}
	var rows []priceListRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		u.unexpectedFailure(w, r, err, "The price lists could not be read")
		return
	}

	views := priceListViews(rows, typed, revised)
	if revised != "" {
		// What was typed is the row's, not the new list's.
		typed = url.Values{}
	}

	data := map[string]any{
		titleKey:     priceListsLabel,
		"PriceLists": views,
		canReviseKey: u.canRevisePriceLists(r),
		totalKey:     total,
		createdKey:   r.URL.Query().Get(paramCreated),
		canCreateKey: u.canCreatePriceLists(r),
		"Moves":      priceListMovesFor(u.canSwitchPriceLists(r)),
		refusedKey:   refused,
		typedKey:     typed,
	}
	addPaging(data, page, int64(page*priceListsPerPage) < total, PriceListsPath)

	u.templates.render(w, r, code, "price_lists.gohtml", data)
}

// priceListMovesFor is the moves the rows offer, none to an operator who may
// not switch a list.
func priceListMovesFor(may bool) map[string]promotionSwitch {
	if !may {
		return nil
	}

	return priceListMoves
}
