package models

// The pointer fields in this file's filters preserve the distinction between
// "not supplied" and "supplied empty": a nil RegionID means the filter is not
// applied, while a RegionID pointing at the empty string means the options
// whose region is EMPTY (that is, valid in every region) are the ones asked
// for. A value type could not tell the two apart.

// ProfileFilter holds the filter and pagination parameters of a shipping
// profile listing.
type ProfileFilter struct {
	// Type, when supplied, returns only the profiles of that type.
	Type *string
	// Limit is the maximum number of rows to return.
	Limit int64
	// Offset is the number of rows to skip.
	Offset int64
}

// OptionFilter holds the filter and pagination parameters of a shipping option
// listing.
type OptionFilter struct {
	// RegionID, when supplied, returns only the options of that region.
	RegionID *string
	// ProfileID, when supplied, returns only the options bound to that profile.
	ProfileID *string
	// ProviderID, when supplied, returns only that provider's options.
	ProviderID *string
	// PriceType, when supplied, returns only the options of that price type.
	PriceType *string
	// Limit is the maximum number of rows to return.
	Limit int64
	// Offset is the number of rows to skip.
	Offset int64
}

// EligibilityFilter is the query for the CANDIDATE options of a cart context.
//
// Only the eliminations that are cheap at the column level stop here; rule
// matching is done by the pure function in the service layer.
type EligibilityFilter struct {
	// RegionID is the cart's region. Options whose region equals this value AND
	// options whose region is empty become candidates.
	RegionID string
	// CurrencyCode is the cart's currency (ISO 4217, uppercase).
	CurrencyCode string
	// ProfileIDs are the profiles the cart's products are bound to. When left
	// EMPTY no profile filter is applied.
	ProfileIDs []string
	// IsReturn reports whether return options or normal options are being asked
	// for.
	IsReturn bool
	// IncludeAdminOnly is true only on the admin surface.
	IncludeAdminOnly bool
}

// FulfillmentFilter holds the filter and pagination parameters of a fulfillment
// listing.
type FulfillmentFilter struct {
	// Reference, when supplied, returns only the fulfillments of that reference.
	Reference *string
	// Status, when supplied, returns only the fulfillments in that status.
	Status *string
	// Limit is the maximum number of rows to return.
	Limit int64
	// Offset is the number of rows to skip.
	Offset int64
}

// LocationFilter is the listing of warehouse selection policies.
//
// There is NO filter field and that is deliberate: the table carries as many
// rows as the installation has warehouses (dozens, not millions), and an admin
// surface that wants to filter by a region can already look at the region list
// of the returned records. The day a filter is added, the listing and the
// COUNT query have to carry the same condition.
type LocationFilter struct {
	// Limit is the maximum number of rows to return.
	Limit int64
	// Offset is the number of rows to skip.
	Offset int64
}
