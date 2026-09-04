package models

// CollectionFilter holds the filter and pagination parameters of a payment
// collection listing.
//
// The pointer fields preserve the distinction between "not given" and "given
// empty": a nil Reference means the filter is not applied, while a Reference
// pointing at the empty string means the records whose reference is empty are
// the ones being asked for. With a value type the two could not be told apart.
type CollectionFilter struct {
	// Reference, when given, returns only the collections carrying that
	// reference.
	Reference *string
	// Status, when given, returns only the collections in that status.
	Status *string
	// Limit is the maximum number of rows to return.
	Limit int64
	// Offset is the number of rows to skip.
	Offset int64
}
