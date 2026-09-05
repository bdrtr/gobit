package models

import (
	"time"

	"github.com/bdrtr/gobit/internal/core/page"
)

// OrderFilter is the set of criteria for listing orders.
//
// The filter fields are pointers: nil means "do not apply this criterion". That
// keeps "status = pending" apart from "no status filter"; the zero value of a
// type never turns into a filter silently.
//
// The type lives in models and not in repository: both the service and the
// repository already import models, so the service's store interface can carry
// these criteria without binding itself to the repository package (ADR 0001).
type OrderFilter struct {
	// CustomerID, when given, returns only that customer's orders.
	CustomerID *string
	// RegionID, when given, returns only that region's orders.
	RegionID *string
	// Status, when given, filters the orders by status.
	Status *OrderStatus
	// Limit is the maximum number of rows to return.
	Limit int64
	// Offset is the number of rows to skip.
	Offset int64
	// After is the keyset position the page starts below; the zero value is the
	// first page.
	//
	// It is applied TOGETHER with Offset so the query keeps one shape; the API
	// refuses the two at once, because they name two different positions.
	After page.Cursor
}

// OrderLineItemFilter is the set of criteria for listing order LINES across
// orders.
//
// The fields are pointers for the reason [OrderFilter] gives: nil is "do not
// apply this criterion", and a zero time is a legitimate instant rather than
// "no date".
//
// # Why the date criterion names the ORDER's stamp
//
// PlacedFrom/PlacedTo filter orders.placed_at, NOT the line's own created_at.
// The line's stamp says when the ROW was written; for a line added to an order
// that already exists (an exchange) that is a different day from the sale. The
// question this filter serves is "what sold in this period", so it has to be
// bound to the moment of the sale, which only the order holds. The join that
// reaches it is argued in queries/order_line_items.sql and in migration 000006.
type OrderLineItemFilter struct {
	// OrderID, when given, returns only that order's lines.
	OrderID *string
	// VariantID, when given, returns only the lines of that product variant.
	VariantID *string
	// PlacedFrom is the INCLUSIVE lower bound of the order's placed_at.
	PlacedFrom *time.Time
	// PlacedTo is the EXCLUSIVE upper bound of the order's placed_at.
	//
	// The interval is half-open [from, to) so that consecutive periods can be
	// asked for back to back without a line falling into both of them: with two
	// inclusive bounds an order placed exactly at midnight would be counted in
	// two months at once.
	PlacedTo *time.Time
	// Limit is the maximum number of rows to return.
	Limit int64
	// Offset is the number of rows to skip.
	Offset int64
}

// ChildFilter is the listing criterion for the return/exchange/claim records of
// an order.
//
// The three share the same shape (one order + pagination) and having a single
// type makes swapping the parameters at the call site impossible.
type ChildFilter struct {
	// OrderID is the order the records belong to; it is required.
	OrderID string
	// Limit is the maximum number of rows to return.
	Limit int64
	// Offset is the number of rows to skip.
	Offset int64
}
