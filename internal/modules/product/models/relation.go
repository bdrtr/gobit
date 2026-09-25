package models

// RelationType is the kind of pointer a product holds to another (ADR 0180).
type RelationType string

// The kinds a product relation can be. The set is CLOSED; the table's
// constraint holds the same list.
const (
	// RelationCrossSell is what goes WITH the product: bought together, an
	// accessory, a refill.
	RelationCrossSell RelationType = "cross_sell"
	// RelationUpSell is the BETTER one: a higher model, a larger size.
	RelationUpSell RelationType = "up_sell"
	// RelationSubstitute is what to buy INSTEAD: a product that does the same
	// job, offered when this one is not the right choice or not in stock.
	RelationSubstitute RelationType = "substitute"
)

// RelationTypes returns every kind, in the order the admin surface lists them.
func RelationTypes() []RelationType {
	return []RelationType{RelationCrossSell, RelationUpSell, RelationSubstitute}
}

// Valid reports whether the kind is one of the closed set.
func (t RelationType) Valid() bool {
	switch t {
	case RelationCrossSell, RelationUpSell, RelationSubstitute:
		return true
	default:
		return false
	}
}
