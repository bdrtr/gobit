package analytics

import (
	"net/http"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// The endpoint and the scope it requires.
const (
	// FunnelPath is the daily funnel endpoint.
	FunnelPath = "/admin/v1/analytics/funnel"
	// ScopeRead is the scope the funnel endpoint requires.
	//
	// It has the same shape as the modules' ("<module>:read"). No write scope was
	// defined: nothing writes this table over HTTP — the rows arrive on the bus —
	// and a scope name nobody hands out is a name whose purpose is forgotten by
	// the day it is first deployed.
	ScopeRead = ModuleName + ":read"
)

// The names of the query parameters.
const (
	paramFrom = "from"
	paramTo   = "to"
)

// The window's limits.
const (
	// defaultWindowDays is how far back the funnel reaches when no window is
	// given. Thirty days is the span a shop compares a month against.
	defaultWindowDays = 30
	// maxWindowDays is the widest window one request may ask for.
	//
	// It is a bound on the ROWS a single response carries (a day per region), not
	// on the query's cost — the index makes the scan cheap either way. Without it
	// one request could ask for a decade and hand a client a body nothing pages
	// through, because this endpoint deliberately does not page: a funnel is read
	// as a whole or it is not a funnel.
	maxWindowDays = 366
)

// codeBadWindow reports a window this endpoint cannot answer for.
const codeBadWindow = "analytics_bad_window"

// funnelDayDTO is one day's counts in one region.
type funnelDayDTO struct {
	// Day is the UTC date, "2006-01-02".
	Day string `json:"day"`
	// RegionID is the region the counts belong to.
	RegionID string `json:"region_id"`
	// CartsCreated is how many carts were opened.
	CartsCreated int64 `json:"carts_created"`
	// CartsCompleted is how many of them were completed.
	//
	// It is NOT a subset of CartsCreated in a given window: a cart opened on
	// Monday and completed on Tuesday is counted on two different days, which is
	// what a shop asking "what happened on Tuesday" means. Dividing one by the
	// other inside a single day is therefore an approximation, and it is the
	// client's to make rather than this endpoint's to hide.
	CartsCompleted int64 `json:"carts_completed"`
	// OrdersPlaced is how many orders were placed.
	//
	// It is published beside the completions rather than instead of them: the
	// checkout saga places the order before it completes the cart, so a placement
	// with no completion is an order that failed after it was opened. That gap is
	// the most useful thing on this page.
	OrdersPlaced int64 `json:"orders_placed"`
}

// funnelResponse is the endpoint's envelope.
//
// It is the list envelope's shape without the paging fields, because this
// endpoint does not page (see [maxWindowDays]). From and To are echoed so a
// client can tell what it actually asked for after the defaults were applied.
type funnelResponse struct {
	Data []funnelDayDTO `json:"data"`
	From string         `json:"from"`
	To   string         `json:"to"`
}

// funnel answers GET /admin/v1/analytics/funnel.
func (m *funnelModule) funnel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	from, to, err := window(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	rows, err := m.store.Funnel(ctx, from, to)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	out := make([]funnelDayDTO, 0, len(rows))
	for i := range rows {
		out = append(out, funnelDayDTO{
			Day:            rows[i].Day.UTC().Format(time.DateOnly),
			RegionID:       rows[i].RegionID,
			CartsCreated:   rows[i].CartsCreated,
			CartsCompleted: rows[i].CartsCompleted,
			OrdersPlaced:   rows[i].OrdersPlaced,
		})
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, funnelResponse{
		Data: out,
		From: from.Format(time.DateOnly),
		To:   to.Format(time.DateOnly),
	})
}

// window reads the requested window, or the default one.
//
// The window is closed on the left and OPEN on the right, which is the only form
// that tiles: two adjacent windows asked for separately add up to the wide one,
// and no day is counted twice at the boundary. "to" is therefore the first day
// NOT included, and the response echoes both so a client can see it.
func window(r *http.Request) (from, to time.Time, err error) {
	to, err = dayParam(r, paramTo, today().AddDate(0, 0, 1))
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	from, err = dayParam(r, paramFrom, to.AddDate(0, 0, -defaultWindowDays))
	if err != nil {
		return time.Time{}, time.Time{}, err
	}

	if !from.Before(to) {
		return time.Time{}, time.Time{}, coreerrors.Invalid(codeBadWindow,
			"%s must be before %s; the window is closed on the left and open on the right, "+
				"so an empty window is a request for nothing", paramFrom, paramTo)
	}
	if to.Sub(from) > maxWindowDays*24*time.Hour {
		return time.Time{}, time.Time{}, coreerrors.Invalid(codeBadWindow,
			"the window may span at most %d days; this endpoint does not page, so a wider "+
				"one would hand back a body nothing reads to the end", maxWindowDays)
	}

	return from, to, nil
}

// dayParam reads a "2006-01-02" parameter, or returns the fallback.
func dayParam(r *http.Request, name string, fallback time.Time) (time.Time, error) {
	text := r.URL.Query().Get(name)
	if text == "" {
		return fallback, nil
	}
	day, err := time.Parse(time.DateOnly, text)
	if err != nil {
		return time.Time{}, coreerrors.Invalid(codeBadWindow,
			"%s must be a date in the form 2006-01-02 (%q given)", name, text)
	}

	return day.UTC(), nil
}

// today is the current UTC date, midnight.
//
// It is a function rather than an expression so the default window is derived
// once, in one place; a handler computing "now" twice can straddle midnight and
// return a window whose two ends disagree about which day it is.
func today() time.Time {
	now := time.Now().UTC()

	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}
