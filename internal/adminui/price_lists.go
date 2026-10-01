package adminui

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// The price lists screen (ADR 0326): the pricing module's lists — a sale, an
// override, a segment's contract prices — with their type, status and
// window, and the form that writes one, through the module's panel surface.

// PriceListsPath lists the price lists and takes the form that writes one.
const PriceListsPath = URLPrefix + "/price-lists"

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
	u.renderPriceLists(w, r, http.StatusOK, "", url.Values{})
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
		u.renderPriceLists(w, r, http.StatusUnprocessableEntity, messageFor(err), r.PostForm)
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
// what was typed. An operator who may write and not read the prices is told
// the reason alone (ADR 0260).
func (u *UI) renderPriceLists(w http.ResponseWriter, r *http.Request, code int, refused string, typed url.Values) {
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

	data := map[string]any{
		titleKey:     priceListsLabel,
		"PriceLists": rows,
		totalKey:     total,
		createdKey:   r.URL.Query().Get(paramCreated),
		canCreateKey: u.canCreatePriceLists(r),
		refusedKey:   refused,
		typedKey:     typed,
	}
	addPaging(data, page, int64(page*priceListsPerPage) < total, PriceListsPath)

	u.templates.render(w, r, code, "price_lists.gohtml", data)
}
