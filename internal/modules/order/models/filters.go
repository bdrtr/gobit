package models

import "github.com/bdrtr/gobit/internal/core/page"

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
