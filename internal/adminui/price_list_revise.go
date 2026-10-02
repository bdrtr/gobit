package adminui

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// A price list's row revises the list's title, description and window from
// what the row was drawn with (ADR 0330).

// PriceListRevisePath takes a row's form that revises its price list.
const PriceListRevisePath = PriceListsPath + "/{id}"

// The revise form's fields beside the new list's: the terms the row was
// drawn with, the moments to the nanosecond.
const (
	formReadTitle       = "read_title"
	formReadDescription = "read_description"
	formReadStarts      = "read_starts_at"
	formReadEnds        = "read_ends_at"
)

// PriceListReviser is the narrow surface a price list's terms are revised
// through (ADR 0330).
type PriceListReviser interface {
	// RevisePriceList writes the list's title, description and window, and
	// refuses when they are no longer the ones read; a nil moment is an open
	// end.
	RevisePriceList(
		ctx context.Context, id, readTitle, readDescription string, readStartsAt, readEndsAt *time.Time,
		title, description string, startsAt, endsAt *time.Time,
	) error
}

// canRevisePriceLists reports whether the operator may revise a list here.
func (u *UI) canRevisePriceLists(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, ok := u.prices.(PriceListReviser)

	return ok && principal.HasScope(scopePricingWrite)
}

// revisePriceList writes the terms a row's form was sent with, from the ones
// the row was drawn with, and returns to the list's page, which names the
// list; a refusal, a list another operator revised first included, comes
// back on the page with what was typed in the row (ADR 0330).
func (u *UI) revisePriceList(w http.ResponseWriter, r *http.Request) {
	reviser, ok := u.prices.(PriceListReviser)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Price lists unavailable",
			"The pricing module's panel surface cannot revise a price list in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	id := chi.URLParam(r, "id")
	title := strings.TrimSpace(r.PostFormValue(formPriceListTitle))
	err := u.sendRevision(r, reviser, id, title)
	switch {
	case err == nil:
		landing := url.Values{paramCreated: {title}}
		if page := pageNumber(r.URL.Query().Get("page")); page > 1 {
			landing.Set("page", strconv.Itoa(page))
		}
		corehttp.WriteRedirect(r.Context(), w, PriceListsPath+"?"+landing.Encode())
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		u.renderPriceLists(w, r, http.StatusUnprocessableEntity, messageFor(err), r.PostForm, id)
	default:
		u.unexpectedFailure(w, r, err, "The price list could not be revised")
	}
}

// sendRevision reads the row's drawn terms and the typed ones and sends them.
func (u *UI) sendRevision(r *http.Request, reviser PriceListReviser, id, title string) error {
	readStarts, err := drawnMoment(r.PostFormValue(formReadStarts))
	if err != nil {
		return err
	}
	readEnds, err := drawnMoment(r.PostFormValue(formReadEnds))
	if err != nil {
		return err
	}
	startsAt, err := revisedMoment(r.PostFormValue(formPriceListStarts), readStarts, "price list", "start")
	if err != nil {
		return err
	}
	endsAt, err := revisedMoment(r.PostFormValue(formPriceListEnds), readEnds, "price list", "end")
	if err != nil {
		return err
	}

	return reviser.RevisePriceList(r.Context(), id,
		lineFeeds(r.PostFormValue(formReadTitle)), lineFeeds(r.PostFormValue(formReadDescription)), readStarts, readEnds,
		title, strings.TrimSpace(lineFeeds(r.PostFormValue(formPriceListDesc))), startsAt, endsAt)
}

// drawnMoment reads a moment a row was drawn with, empty for an open end.
func drawnMoment(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	at, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, errors.Invalid("admin_ui_read_moment",
			"The window the row was drawn with could not be read; draw the list again.")
	}

	return &at, nil
}

// revisedMoment reads a typed end of a price list's or a campaign's window.
// The picker shows a moment to the minute, so an end typed as it was shown is
// the moment drawn, not that minute: a revision of the title leaves the
// window where it was.
func revisedMoment(typed string, drawn *time.Time, whose, end string) (*time.Time, error) {
	if drawn != nil && strings.TrimSpace(typed) == drawn.UTC().Format(publishAtLayout) {
		return drawn, nil
	}

	return readWindowMoment(typed, whose, end)
}

// lineFeeds turns the CR LF a browser sends a line break as back into the
// LF it was drawn with.
func lineFeeds(value string) string {
	return strings.ReplaceAll(value, "\r\n", "\n")
}

// priceListView is one price list as the list prints it, with what its form
// offers: the list as drawn, or what was typed in the form a refusal came
// back to.
type priceListView struct {
	priceListRow
	ReadStarts      string
	ReadEnds        string
	FormTitle       string
	FormDescription string
	FormStarts      string
	FormEnds        string
	Refused         bool
}

// priceListViews prints the rows with their forms; the row revised, when one
// was, carries what was typed.
func priceListViews(rows []priceListRow, typed url.Values, revised string) []priceListView {
	views := make([]priceListView, 0, len(rows))
	for _, row := range rows {
		view := priceListView{
			priceListRow: row, FormTitle: row.Title, FormDescription: row.Description,
			ReadStarts: drawnText(row.StartsAt, time.RFC3339Nano), ReadEnds: drawnText(row.EndsAt, time.RFC3339Nano),
			FormStarts: drawnText(row.StartsAt, publishAtLayout), FormEnds: drawnText(row.EndsAt, publishAtLayout),
		}
		if revised != "" && row.ID == revised {
			view.FormTitle, view.FormDescription = typed.Get(formPriceListTitle), typed.Get(formPriceListDesc)
			view.FormStarts, view.FormEnds = typed.Get(formPriceListStarts), typed.Get(formPriceListEnds)
			view.Refused = true
		}
		views = append(views, view)
	}

	return views
}

// drawnText prints a moment in UTC in the layout, empty for an open end.
func drawnText(at *time.Time, layout string) string {
	if at == nil {
		return ""
	}

	return at.UTC().Format(layout)
}
