package models

// InventoryItemFilter is the criteria set of the item listing.
//
// The filter fields are pointers: nil means "do not apply this criterion". That
// is what keeps "requires_shipping = false" apart from "there is no
// requires_shipping filter"; the zero value of a bool does not silently turn
// into a filter.
//
// The type is inside models and not inside repository: both the service and the
// repository already import models, so the store interface of the service can
// carry these criteria without binding itself to the repository package.
type InventoryItemFilter struct {
	// SKU, when given, returns only the item carrying that stock code.
	SKU *string
	// RequiresShipping, when given, separates the items that require shipping
	// from the ones that do not.
	RequiresShipping *bool
	// Limit is the maximum number of rows to return.
	Limit int64
	// Offset is the number of rows to skip.
	Offset int64
}
