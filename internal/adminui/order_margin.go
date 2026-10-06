package adminui

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// What an order's goods cost on the panel (ADR 0412): the order list prints
// each order's placed margin and the order page the margin and each line's
// unit cost, read from the order module's surface in one call per page. The
// margin is the module's (ADR 0401); the panel computes none.

// OrderMargins is the narrow surface an order's placed margin and its lines'
// costs are read through: the order module's.
type OrderMargins interface {
	// PlacedMarginsJSON returns the placed margin of each given order that
	// has one; an order with no line that is not a gift card is absent.
	PlacedMarginsJSON(ctx context.Context, orderIDs []string) (json.RawMessage, error)
	// LineCostsJSON returns the unit cost each of the order's lines kept.
	LineCostsJSON(ctx context.Context, orderID string) (json.RawMessage, error)
}

// placedMarginRecord is one order's placed margin as the surface sends it; the
// json tags are the contract with that surface, exercised end to end.
type placedMarginRecord struct {
	OrderID          string `json:"order_id"`
	Sales            int64  `json:"sales"`
	Cost             *int64 `json:"cost"`
	Margin           *int64 `json:"margin"`
	LinesWithoutCost int64  `json:"lines_without_cost"`
}

// lineCostRecord is one line's unit cost as the surface sends it.
type lineCostRecord struct {
	LineItemID string `json:"line_item_id"`
	UnitCost   *int64 `json:"unit_cost"`
}

// marginView is an order's placed margin as the panel prints it. Known says a
// margin is printed; otherwise Note says why there is none.
type marginView struct {
	Sales  string
	Cost   string
	Margin string
	Note   string
	Known  bool
}

// noGoodsNote is why an order whose lines are all gift cards has no margin.
const noGoodsNote = "no line that is not a gift card"

// marginOf prints one order's margin in its currency; nil is an order the
// surface did not answer, which has no line that is not a gift card.
func marginOf(record *placedMarginRecord, currency string, scales map[string]int) marginView {
	if record == nil {
		return marginView{Note: noGoodsNote}
	}
	printed := func(minor int64) string {
		text, known := formatAmount(minor, currency, scales)
		return withCurrency(text, currency, known)
	}
	view := marginView{Sales: printed(record.Sales)}
	switch {
	case record.Cost != nil && record.Margin != nil:
		view.Cost, view.Margin, view.Known = printed(*record.Cost), printed(*record.Margin), true
	case record.LinesWithoutCost == 1:
		view.Note = "1 line kept no cost"
	case record.LinesWithoutCost > 1:
		view.Note = strconv.FormatInt(record.LinesWithoutCost, 10) + " lines kept no cost"
	default:
		view.Note = "the cost passes what an order can hold"
	}

	return view
}

// marginsOf reads the placed margin of every order on a page in one call;
// shown says the surface reads margins, and unread that the read failed,
// which leaves the list standing.
func (u *UI) marginsOf(r *http.Request, rows []orderRow, scales map[string]int) (
	margins map[string]marginView, shown, unread bool,
) {
	reader, ok := u.afterSales.(OrderMargins)
	if !ok || len(rows) == 0 {
		return nil, ok, false
	}
	ids := make([]string, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].ID)
	}
	records, err := readMargins(r.Context(), reader, ids)
	if err != nil {
		ctx := r.Context()
		corehttp.LoggerFromContext(ctx).WarnContext(ctx,
			"the panel could not read the orders' margins", "error", err, "orders", len(ids))
		return nil, true, true
	}
	margins = make(map[string]marginView, len(rows))
	for i := range rows {
		margins[rows[i].ID] = marginOf(records[rows[i].ID], rows[i].Currency, scales)
	}

	return margins, true, false
}

// readMargins decodes the surface's margins by order.
func readMargins(ctx context.Context, reader OrderMargins, ids []string) (map[string]*placedMarginRecord, error) {
	raw, err := reader.PlacedMarginsJSON(ctx, ids)
	if err != nil {
		return nil, err
	}
	var records []placedMarginRecord
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, err
	}
	out := make(map[string]*placedMarginRecord, len(records))
	for i := range records {
		out[records[i].OrderID] = &records[i]
	}

	return out, nil
}

// orderCosts is what the order page prints of what its goods cost.
type orderCosts struct {
	// Shown says the surface reads them, Unread that a read failed.
	Shown  bool
	Unread bool
	// Margin is the order's placed margin; Lines each line's unit cost as
	// printed, by line id, absent on a line that kept none.
	Margin marginView
	Lines  map[string]string
}

// costsOf reads the order's placed margin and its lines' costs for the order
// page; a failed read leaves the page standing and says so.
func (u *UI) costsOf(r *http.Request, detail *orderDetail, scales map[string]int) orderCosts {
	reader, ok := u.afterSales.(OrderMargins)
	if !ok {
		return orderCosts{}
	}
	ctx := r.Context()
	margins, err := readMargins(ctx, reader, []string{detail.ID})
	var lines []lineCostRecord
	if err == nil {
		var raw json.RawMessage
		if raw, err = reader.LineCostsJSON(ctx, detail.ID); err == nil {
			err = json.Unmarshal(raw, &lines)
		}
	}
	if err != nil {
		corehttp.LoggerFromContext(ctx).WarnContext(ctx,
			"the panel could not read what the order's goods cost", "error", err, "order_id", detail.ID)
		return orderCosts{Shown: true, Unread: true}
	}

	out := orderCosts{
		Shown:  true,
		Margin: marginOf(margins[detail.ID], detail.Currency, scales),
		Lines:  make(map[string]string, len(lines)),
	}
	for _, line := range lines {
		if line.UnitCost == nil {
			continue
		}
		text, _ := formatAmount(*line.UnitCost, detail.Currency, scales)
		out.Lines[line.LineItemID] = text
	}

	return out
}
