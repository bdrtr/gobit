package adminui

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/query"
)

// EntityOrderLineItem is the order line's entity name in the read layer.
//
// It is a STRING and not an import, for the reason [EntityOrder] is: the panel
// knows no module (ADR 0011). The value is the order module's
// service.LineItemEntity, and the name carries its owner rather than being a
// bare "line_item" — the read layer's entity namespace is flat and shared by
// every module, so a cart line and a quote line would want that bare name too.
const EntityOrderLineItem = "order_line_item"

// The order-line fields this screen reads.
//
// They are the read layer's names, repeated here for the reason the entity name
// is. The line entity's names are its OWN even where the string matches the
// order's ("title", "total"): the two contracts can move apart, and a constant
// shared between them would hide the day they do.
const (
	fieldOrderID   = "order_id"
	fieldVariantID = "variant_id"
	fieldQuantity  = "quantity"
	fieldUnitPrice = "unit_price"
)

// The date filters the line entity accepts.
//
// Neither names a field of the line, and that is the provider's decision rather
// than an oversight: the moment of a sale is a fact of the ORDER (its
// placed_at), reached through a join, and copying it onto the line record would
// make one fact answerable from two entities. The line's own created_at is NOT
// that moment — a line added to an existing order carries the day of the
// exchange — so filtering on it would date an exchange as a fresh sale.
const (
	// filterPlacedFrom is the INCLUSIVE lower bound of the order's placed_at.
	filterPlacedFrom = "placed_from"
	// filterPlacedTo is the EXCLUSIVE upper bound of the same.
	filterPlacedTo = "placed_to"
)

// The query parameters that carry the report's window.
//
// The report is a URL an operator bookmarks and sends to somebody else, so the
// period travels in the address rather than in a session or a form post. The
// same two names are written by hand into the paging links of
// internal/adminui/templates/sales.gohtml, because a "next page" that dropped
// the window would silently move the reader to the last thirty days without
// saying so.
const (
	paramFrom = "from"
	paramTo   = "to"
)

// salesLabel is what the section is called on screen.
//
// It is a constant for the reason [ordersLabel] is: the menu, the page title
// and the heading print the same word, and three copies are three places to
// rename it in — the one that is missed is the one an operator sees.
const salesLabel = "Sales"

// salesPerPage is the page size of the sales report.
//
// It matches the other four screens', and matching is the point: five screens
// in one panel that paged differently would make an operator learn five habits
// for no reason.
const salesPerPage = 25

// salesWindowDays is how many days the report covers when no period was asked
// for.
//
// Thirty is not a month and is not meant to be: an operator who wants a
// calendar month types its two dates. What this number has to be is a window
// small enough that the first page of a busy shop is still about RECENT trade,
// and large enough that the screen is not empty on a quiet one.
const salesWindowDays = 30

// dayLayout is how a day is written in the address bar and on screen.
//
// It is the ISO order deliberately: the report is read by people who do not all
// write a date the same way round, and 2026-08-01 cannot be misread as the
// first of August in one country and the eighth of January in another.
const dayLayout = "2006-01-02"

// The data keys the sales page reads its window from.
//
// They are constants for the reason [titleKey] is: the template looks them up
// BY NAME, so a typo would not fail — the form would render with empty date
// boxes and the paging links would quietly lose the period.
const (
	fromKey = "From"
	toKey   = "To"
)

// saleRow is one sold line of the report.
//
// It carries facts from TWO reads: the line's own (what was sold, how many, at
// what amount) and the order's (when, under which number, in which currency).
// They are joined here rather than in the template so that a row whose order
// could not be found is still a row — see [UI.listSales] for why dropping it
// would be the worse failure.
type saleRow struct {
	// PlacedAt is the moment the ORDER was placed, which is the moment of the
	// sale. The line's own created_at is not it and is not read here.
	PlacedAt time.Time
	// DisplayID is the order's human-facing number, empty when the order was
	// not in the second read.
	DisplayID string
	// OrderID is the line's join key upwards and what the row links to.
	OrderID string
	// VariantID is shown because it is what an operator PASTES into a catalog
	// search: the title on the line is the one the item sold under and may no
	// longer find anything.
	VariantID string
	// Title is the name the line was SOLD UNDER — the copy taken at the moment
	// of sale. It is deliberately not the variant's current name: a catalog
	// rename must not rewrite what a past sale says it was.
	Title string
	// Quantity is how many units the line sold.
	Quantity int64
	// UnitPrice is the price of one unit at the moment of sale, formatted the
	// way Total is.
	UnitPrice string
	// Total is the line's total as it is printed, and Minor says whether the
	// scale was known — see [formatAmount] for why an unknown scale is never
	// guessed. One flag covers both amounts because both are in the same
	// currency and the scale is a property of the currency, not of the amount.
	Total    string
	Minor    bool
	Currency string
}

// orderFacts are the parts of an order a sold line cannot carry itself.
//
// The line record deliberately holds none of them: two entities answering the
// same question is how the two start to disagree. They arrive from a second
// read keyed by order_id, which is what that field is for.
type orderFacts struct {
	DisplayID string
	Currency  string
	PlacedAt  time.Time
}

// reportWindow is the period a sales report covers.
//
// The interval is HALF-OPEN [From, To): From is included, To is not. The two
// fields are instants rather than days because that is what the read layer's
// filters take, and because "a day" is not a length — it is a calendar step in
// some zone, and the zone is the one thing a date range must not guess at.
type reportWindow struct {
	// From is the first instant the report includes.
	From time.Time
	// To is the first instant it EXCLUDES: the start of the day after the last
	// one the operator asked for.
	To time.Time
}

// lastDay is the final day the window covers, as the operator wrote it.
//
// It is To minus one day, and it exists because the screen and the address bar
// speak in INCLUSIVE days while the filter is half-open. Printing To itself
// would show the operator a day their report does not contain.
func (w reportWindow) lastDay() time.Time {
	return w.To.AddDate(0, 0, -1)
}

// listSales renders the sales report: the lines sold in a period, newest first.
//
// It is the first consumer of the order module's line entity. Everything the
// screen knows about that entity is a NAME (ADR 0011), the same way every other
// panel screen reaches the read layer.
//
// # Why two reads
//
// The line record carries what was sold and for how much, and deliberately not
// the order's own facts — the currency, the display number, the moment of the
// sale. Those come from a SECOND Graph call against the "order" entity, keyed
// by the order_id the line carries. This is the shape [UI.showProduct] already
// uses for the same reason: the read layer joins across LINKS, and two entities
// of one module are not linked to each other. Two calls per page is the cost;
// the alternative is a per-row read, which is the N+1 the read layer exists to
// prevent.
//
// # Why the range is half-open
//
// "to" names a day and the filter takes an instant, so the day the operator
// typed is turned into the START OF THE DAY AFTER it. A report for 1-31 August
// therefore ends at 2026-09-01T00:00 and includes everything placed on the
// 31st. The reverse — passing the start of the 31st as an exclusive bound —
// looks identical on screen and silently drops a whole day of sales, which is
// the kind of off-by-one nobody notices until a month is reconciled by hand.
// The screen prints the INCLUSIVE last day so the two never diverge; see
// [reportWindow.lastDay].
//
// # Why there is no total and no per-variant summary
//
// This is the report an operator would most like a bottom line on, and it does
// not have one. The read layer offers no aggregation at all: the line provider
// returns records, never sums, and it says so — a GROUP BY behind that
// interface would produce records that are not records of an entity. What this
// screen holds is therefore ONE PAGE, at most 25 rows of a limit the provider
// clamps to 100 anyway. A sum over that page would print under a heading that
// says "Sales" and would be read as the period's takings, while actually being
// the takings of whichever 25 lines happened to sort first. That number is
// wrong in a way an operator cannot see, which is worse than no number at all:
// a missing total sends someone to write the query, a wrong one does not.
// Aggregation waits for a read surface that can do it — and that surface, when
// it exists, belongs to the module, not to a loop in a handler.
func (u *UI) listSales(w http.ResponseWriter, r *http.Request) {
	page := pageNumber(r.URL.Query().Get("page"))
	window := salesWindowOf(
		r.URL.Query().Get(paramFrom), r.URL.Query().Get(paramTo), time.Now())

	lines, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity: EntityOrderLineItem,
		Fields: []string{
			fieldID, fieldOrderID, fieldVariantID, fieldTitle,
			fieldQuantity, fieldUnitPrice, fieldTotal,
		},
		Filters: map[string]any{
			filterPlacedFrom: window.From,
			filterPlacedTo:   window.To,
		},
		Limit:  salesPerPage + 1,
		Offset: (page - 1) * salesPerPage,
	})
	if err != nil {
		u.catalogFailure(w, r, err, "The sales report could not be read.")

		return
	}

	// One record more than the page is read so "is there a next page" needs no
	// count, exactly as the other four lists do it. The extra row is dropped
	// BEFORE the second read, so the orders fetched are only the ones shown.
	hasNext := len(lines) > salesPerPage
	if hasNext {
		lines = lines[:salesPerPage]
	}

	orders, err := u.orderFactsOf(r.Context(), lines)
	if err != nil {
		u.catalogFailure(w, r, err, "The orders behind the sold lines could not be read.")

		return
	}

	scales := u.currencyScales(r.Context())

	rows := make([]saleRow, 0, len(lines))
	for _, line := range lines {
		rows = append(rows, saleRowOf(line, orders, scales))
	}

	data := map[string]any{
		titleKey:     salesLabel,
		"Sales":      rows,
		"OrdersPath": OrdersPath,
		fromKey:      window.From.Format(dayLayout),
		toKey:        window.lastDay().Format(dayLayout),
	}
	addPaging(data, page, hasNext, SalesPath)

	u.templates.render(w, r, http.StatusOK, "sales.gohtml", data)
}

// orderFactsOf reads the orders the given lines belong to, keyed by identity.
//
// It is ONE read for the whole page: the identifiers are collected first and
// passed as a list, which is the batch shape the order provider answers by
// identity with. A read per row would be the N+1 the read layer exists to
// prevent, and on a report it would be 25 of them per page.
//
// A FAILURE is returned rather than swallowed, and that is the opposite of what
// happens to a merely MISSING order (see [saleRowOf]). The two are different
// facts: one order absent from the answer costs one row its date and its
// number, while a failed read costs EVERY row all of them — a sales report in
// which nothing has a date or a currency is not a degraded report, it is a
// misleading one, and the operator is better told that it could not be built.
func (u *UI) orderFactsOf(
	ctx context.Context, lines []query.Record,
) (map[string]orderFacts, error) {
	ids := make([]string, 0, len(lines))
	seen := make(map[string]bool, len(lines))
	for _, line := range lines {
		id := recordString(line, fieldOrderID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}

	facts := make(map[string]orderFacts, len(ids))
	if len(ids) == 0 {
		return facts, nil
	}

	records, err := u.catalog.Graph(ctx, query.GraphSpec{
		Entity:  EntityOrder,
		Fields:  []string{fieldID, fieldDisplayID, fieldCurrencyCod, fieldPlacedAt},
		Filters: map[string]any{filterID: ids},
		Limit:   len(ids),
	})
	if err != nil {
		return nil, err
	}

	for _, rec := range records {
		facts[recordString(rec, fieldID)] = orderFacts{
			DisplayID: recordNumber(rec, fieldDisplayID),
			Currency:  recordString(rec, fieldCurrencyCod),
			PlacedAt:  recordTime(rec, fieldPlacedAt),
		}
	}

	return facts, nil
}

// saleRowOf joins one sold line with its order's facts.
//
// A line whose order is NOT in the map still becomes a row. The order was
// deleted between the two reads, or the identifier points at a record the order
// provider no longer serves; either way the line was sold, and a row silently
// dropped from a report is a sale that disappeared from the books. What such a
// row loses is its date, its number and its currency — the amount then prints
// as minor units and says so, through the same path an unknown currency scale
// takes, because an amount with no currency is exactly an amount whose scale is
// unknown.
func saleRowOf(
	line query.Record, orders map[string]orderFacts, scales map[string]int,
) saleRow {
	orderID := recordString(line, fieldOrderID)
	facts := orders[orderID]

	total, known := amountField(line, fieldTotal, facts.Currency, scales)
	unit, _ := amountField(line, fieldUnitPrice, facts.Currency, scales)

	return saleRow{
		PlacedAt:  facts.PlacedAt,
		DisplayID: facts.DisplayID,
		OrderID:   orderID,
		VariantID: recordString(line, fieldVariantID),
		Title:     recordString(line, fieldTitle),
		Quantity:  recordInt(line, fieldQuantity),
		UnitPrice: unit,
		Total:     total,
		Minor:     !known,
		Currency:  facts.Currency,
	}
}

// salesWindowOf reads the period out of the two query parameters.
//
// Both are DAYS written as 2006-01-02 and both are read in the zone of the
// clock it is given, which in a request is the server's own. A day is not an
// instant until a zone says so, and reading "2026-09-01" as UTC in a shop that
// keeps its books in Istanbul would move the boundary three hours into the
// previous day's trade. The panel has no shop zone to consult — no
// configuration reaches this tree — so it uses the one the deployment was given
// and leaves that visible here rather than hard-coding UTC, which would be the
// same guess made silently.
//
// # Why nothing here refuses
//
// An unreadable, absent or REVERSED range falls back to the last
// [salesWindowDays] days instead of rendering an error page, following
// [pageNumber]: this address is edited by hand, and a typo in a date is not
// something the operator needs a 500 to learn about — the window is printed on
// the screen and in the form, so the fallback is visible where the mistake was
// made. The reversed case has a second reason to be caught here: the provider
// REFUSES from >= to rather than answering with an empty page, so passing it
// through would turn a typo into "Catalog unavailable", which sends the reader
// to look at the database.
func salesWindowOf(fromRaw, toRaw string, now time.Time) reportWindow {
	today := startOfDay(now)
	fallback := reportWindow{
		From: today.AddDate(0, 0, -(salesWindowDays - 1)),
		To:   today.AddDate(0, 0, 1),
	}

	window := fallback
	if from, err := parseDay(fromRaw, now.Location()); err == nil {
		window.From = from
	}
	if to, err := parseDay(toRaw, now.Location()); err == nil {
		// The day the operator typed is INCLUDED, so the exclusive bound is the
		// start of the day after it. See [UI.listSales] on why the other
		// spelling loses a day of sales without looking wrong.
		window.To = to.AddDate(0, 0, 1)
	}

	if !window.From.Before(window.To) {
		return fallback
	}

	return window
}

// parseDay reads a 2006-01-02 day in the given zone.
func parseDay(raw string, in *time.Location) (time.Time, error) {
	return time.ParseInLocation(dayLayout, strings.TrimSpace(raw), in)
}

// startOfDay is midnight of the day the instant falls on, in its own zone.
//
// It is calendar arithmetic and not a truncation to 24 hours: time.Truncate
// works on absolute time, so on the day a zone changes its offset it lands
// somewhere other than midnight — an hour that would then be the boundary
// between two reporting periods.
func startOfDay(at time.Time) time.Time {
	year, month, day := at.Date()

	return time.Date(year, month, day, 0, 0, 0, 0, at.Location())
}
