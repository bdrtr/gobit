package models

// CartFilter is the set of criteria for listing carts.
//
// The filter fields are pointers: nil means "do not apply this criterion". That
// is how "completed = false" (only the carts that are not completed) is told
// apart from "no completed filter at all" (all of them); a bool's zero value
// never turns silently into a filter.
//
// The type lives in models and not in repository: both the service and the
// repository already import models, so the service's store interface can carry
// these criteria without binding itself to the repository package (ADR 0001).
type CartFilter struct {
	// CustomerID, when given, returns only that customer's carts.
	CustomerID *string
	// RegionID, when given, returns only that region's carts.
	RegionID *string
	// Completed, when given, filters the carts by whether they are completed.
	Completed *bool
	// Limit is the maximum number of rows to return.
	Limit int64
	// Offset is the number of rows to skip.
	Offset int64
}
