// Package models holds the domain models of the cart module.
//
// The types here are independent of the database driver: pgtype and the sqlc
// generated types DO NOT LEAK in here. The translation is done in the
// repository layer; the service, the API and the tests see only these types.
//
// Money is an INTEGER minor unit (cents) everywhere and the currency sits in a
// separate field (plan Section 8); floating point is used in no field. Times
// are UTC.
package models

import "time"

// Amount and quantity limits.
//
// The limits are not arbitrary: a line's subtotal is computed as unit price ×
// quantity and that product MUST FIT in an int64. Because MaxAmount ×
// MaxQuantity = 10^12 × 10^6 = 10^18 < 9.22×10^18, overflow is structurally
// impossible. The same limits are deliberately identical to the limits in the
// pricing module; because the two modules do not import each other, the value
// is repeated here (the accepted price of ADR 0001).
const (
	// MinAmount is the smallest allowed amount. A negative amount is not a
	// discount; a discount is carried in a separate field (discount_total).
	MinAmount int64 = 0
	// MaxAmount is the largest allowed amount (minor unit).
	MaxAmount int64 = 1_000_000_000_000
	// MinQuantity is the smallest quantity of a line; a line with zero quantity
	// means the line not being there at all.
	MinQuantity int64 = 1
	// MaxQuantity is the largest quantity of a line.
	MaxQuantity int64 = 1_000_000
	// MaxTotal is the largest value of a TOTAL field (minor unit).
	//
	// The value is MaxAmount × MaxQuantity: a single line's subtotal can be at
	// most this much, so it is also the natural ceiling for the cart totals.
	// The identity check (subtotal + tax_total + shipping_total) produces at
	// most 3 × 10^18 and fits in an int64 (9.22 × 10^18); overflow is
	// structurally impossible.
	MaxTotal int64 = MaxAmount * MaxQuantity
)

// Cart is a shopping cart.
//
// # Whom the totals fields belong to
//
// Subtotal, DiscountTotal, TaxTotal, ShippingTotal and Total are NOT COMPUTED
// by this module. The price is pricing's data, the tax is tax/region's, and the
// flow that brings the two together is the calculate_totals WORKFLOW (plan
// Section 2.5, ADR 0006). The cart service only STORES these fields and
// VALIDATES their consistency (see [Cart.TotalsConsistent]).
//
// # Stale totals
//
// Every operation that changes the shape of the cart increments the
// [Cart.Revision] counter; the side that writes the totals stamps the counter
// of that moment as [Cart.TotalsRevision]. When the two diverge, the totals do
// not belong to the CURRENT shape of the cart; [Cart.TotalsStale] reports this.
// Staleness is neither hidden nor made up: storing a stale total silently would
// be showing the customer a wrong amount, and zeroing the totals would be
// saying "free".
type Cart struct {
	// ID is the "cart_" prefixed, time-sortable identifier.
	ID string
	// RegionID is the cart's region; it belongs to the region module and IS NOT
	// A FOREIGN KEY (Principle 2.2). It is required.
	RegionID string
	// CustomerID is the customer who owns the cart; it belongs to the customer
	// module and IS NOT A FOREIGN KEY. If empty, the cart belongs to a GUEST.
	CustomerID string
	// Email is the cart's contact address; on a guest cart it is the only way
	// to follow it.
	Email string
	// CurrencyCode is the ISO 4217 code and is always stored in UPPERCASE. The
	// value is copied from the region; the side that copies it is the workflow,
	// the cart module does not call the region module (ADR 0001/0006).
	CurrencyCode string
	// Subtotal is the sum of the line subtotals (minor unit).
	Subtotal int64
	// DiscountTotal is the total discount (minor unit); it is stored positive
	// and is SUBTRACTED from the total.
	DiscountTotal int64
	// TaxTotal is the total tax (minor unit).
	TaxTotal int64
	// ShippingTotal is the total shipping amount (minor unit).
	ShippingTotal int64
	// Total is the amount payable (minor unit):
	// Subtotal - DiscountTotal + TaxTotal + ShippingTotal.
	Total int64
	// Revision is the cart's shape counter; it goes up by one on every
	// structural change that affects the totals.
	Revision int64
	// TotalsRevision stamps which shape the totals were computed for.
	TotalsRevision int64
	// Metadata is the caller's free-form extra data.
	Metadata map[string]any
	// CompletedAt is the moment the cart was completed; if it is set, the cart
	// IS IMMUTABLE.
	CompletedAt *time.Time
	// CreatedAt and UpdatedAt are UTC.
	CreatedAt time.Time
	UpdatedAt time.Time
	// DeletedAt is the moment of the soft delete; if nil, the cart is alive.
	DeletedAt *time.Time
}

// Completed reports whether the cart is completed.
func (c Cart) Completed() bool {
	return c.CompletedAt != nil
}

// Guest reports whether the cart belongs to a guest.
func (c Cart) Guest() bool {
	return c.CustomerID == ""
}

// TotalsStale reports that the totals DO NOT belong to the current shape of the
// cart.
//
// The criterion CANNOT TELL APART "never computed": on a cart nobody has
// touched, [Cart.Revision] and [Cart.TotalsRevision] are both zero. Nor does it
// need to be told apart — the counter does not go down and adding a line
// necessarily increments it, so the only uncomputed cart the criterion stays
// silent about is the one WITHOUT LINES, and a separate gate refuses to
// complete a cart without lines (see MarkCompleted in the service).
func (c Cart) TotalsStale() bool {
	return c.TotalsRevision != c.Revision
}

// TotalsConsistent reports that the totals identity holds:
// Total = Subtotal - DiscountTotal + TaxTotal + ShippingTotal.
//
// The check exists both at the service entrance and in a database constraint;
// both of them enforce the same identity and prevent a calculation error in the
// workflow from silently becoming permanent.
func (c Cart) TotalsConsistent() bool {
	return c.Total == c.Subtotal-c.DiscountTotal+c.TaxTotal+c.ShippingTotal
}

// CartDetail is the cart in its full form, together with its children.
//
// The type being separate is deliberate: [Cart] is a single row and the listing
// paths DO NOT run a child query (the N+1 ban). This type is the only place
// where the children are loaded, so the ambiguity "does this cart have lines,
// or were they simply not loaded?" never arises.
type CartDetail struct {
	// Cart is the cart itself.
	Cart
	// Items are the cart's lines; they are in creation order.
	Items []LineItem
	// ShippingAddress is the cart's shipping address; nil if there is none.
	ShippingAddress *CartAddress
	// BillingAddress is the cart's billing address; nil if there is none.
	BillingAddress *CartAddress
	// ShippingMethods are the shipping methods selected for the cart.
	ShippingMethods []ShippingMethod
}

// LineItem is a line in the cart.
//
// Title and UnitPrice are COPIED from the variant: even if the catalog changes
// later (or the variant is deleted), the name and the amount seen on the cart do
// not change. VariantID is another module's (product) identifier and IS NOT A
// FOREIGN KEY (Principle 2.2).
type LineItem struct {
	// ID is the "li_" prefixed identifier.
	ID string
	// CartID is the cart the line belongs to.
	CartID string
	// VariantID is the product variant the line points at; it belongs to the
	// product module.
	VariantID string
	// Title is the line's displayed name.
	Title string
	// Quantity is the quantity on the line; it is always positive.
	Quantity int64
	// UnitPrice is the unit price (minor unit); it comes from pricing and the
	// workflow writes it.
	UnitPrice int64
	// Subtotal is the line's subtotal (minor unit): UnitPrice × Quantity.
	Subtotal int64
	// DiscountTotal is the discount falling on the line (minor unit); it is
	// stored positive.
	DiscountTotal int64
	// TaxTotal is the tax falling on the line (minor unit).
	TaxTotal int64
	// Total is the line's total (minor unit):
	// Subtotal - DiscountTotal + TaxTotal.
	Total int64
	// Metadata is the caller's free-form extra data.
	Metadata map[string]any
	// CreatedAt and UpdatedAt are UTC.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// TotalsConsistent reports that the line totals identity holds:
// Total = Subtotal - DiscountTotal + TaxTotal.
//
// There is no shipping at the line level; shipping belongs to the whole cart.
func (l LineItem) TotalsConsistent() bool {
	return l.Total == l.Subtotal-l.DiscountTotal+l.TaxTotal
}

// CartTotals is the writable set of a cart's totals fields.
//
// The type is used at the boundary between the service and the store: instead
// of six separate int64 parameters, named fields make it impossible for two
// amounts to swap places by accident at the call site (the compiler could not
// have caught an ordering mistake, because they are all of the same type).
type CartTotals struct {
	// Subtotal is the sum of the line subtotals (minor unit).
	Subtotal int64
	// DiscountTotal is the total discount (minor unit); it is given positive.
	DiscountTotal int64
	// TaxTotal is the total tax (minor unit).
	TaxTotal int64
	// ShippingTotal is the total shipping amount (minor unit).
	ShippingTotal int64
	// Total is the amount payable (minor unit).
	Total int64
	// Revision is which cart shape the totals were computed for; it is stamped
	// onto the record as totals_revision.
	Revision int64
}

// Consistent reports that the totals identity holds:
// Total = Subtotal - DiscountTotal + TaxTotal + ShippingTotal.
func (t CartTotals) Consistent() bool {
	return t.Total == t.Subtotal-t.DiscountTotal+t.TaxTotal+t.ShippingTotal
}

// CartContact is the writable set of a cart's contact and ownership fields.
//
// The two are carried in a single type because they are two faces of one single
// intent: WHOM the cart belongs to. Had they been two separate string
// parameters, they could have swapped places at the call site and the compiler
// could not have caught it — both of them are string.
type CartContact struct {
	// Email is the cart's contact address; an empty string CLEARS the stored
	// value.
	Email string
	// CustomerID is the cart's owner; an empty string leaves the cart a guest
	// cart.
	CustomerID string
}

// LineTotals is the writable set of a cart line's money fields.
//
// The quantity IS NOT HERE: the quantity is the cart service's data, the
// amounts are the workflow's. The separation is deliberate — a calculation
// round cannot change the quantity silently.
type LineTotals struct {
	// UnitPrice is the unit price (minor unit).
	UnitPrice int64
	// Subtotal is the line's subtotal (minor unit).
	Subtotal int64
	// DiscountTotal is the discount falling on the line (minor unit); it is
	// given positive.
	DiscountTotal int64
	// TaxTotal is the tax falling on the line (minor unit).
	TaxTotal int64
	// Total is the line's total (minor unit).
	Total int64
}

// Consistent reports that the line totals identity holds:
// Total = Subtotal - DiscountTotal + TaxTotal.
func (t LineTotals) Consistent() bool {
	return t.Total == t.Subtotal-t.DiscountTotal+t.TaxTotal
}

// LineItemTotals carries a line's IDENTITY TOGETHER WITH its amounts.
//
// The identity and the amounts standing in the same value is deliberate: a whole
// calculation round is written with a single statement and the store builds six
// parallel arrays out of this slice (see cart_line_items.sql,
// SetLineItemTotals). Carrying the identifiers and the amounts as separate
// slices would make it possible for the caller to give them in different orders:
// writing the wrong amount to the wrong line is charging the customer the wrong
// amount, and no gate downstream sees it.
type LineItemTotals struct {
	// LineItemID is the line the amounts will be written to.
	LineItemID string
	// Totals are the line's money fields (minor unit).
	Totals LineTotals
}

// AddressType is the type of a cart address.
type AddressType string

// The types of a cart address.
const (
	// AddressShipping is the shipping address.
	AddressShipping AddressType = "shipping"
	// AddressBilling is the billing address.
	AddressBilling AddressType = "billing"
)

// Valid reports whether the type is a defined value.
func (t AddressType) Valid() bool {
	switch t {
	case AddressShipping, AddressBilling:
		return true
	default:
		return false
	}
}

// String returns the textual representation of the type.
func (t AddressType) String() string {
	return string(t)
}

// CartAddress is a shipping or billing address belonging to a cart.
//
// # Why a copy
//
// The cart's address is COPIED from the address book in the customer module; the
// cart keeps its own copy. When the customer later changes or deletes their
// record in the book, the past cart (and the order born out of it) is not
// broken: what is written on the cart is "the place the shipment was sent to",
// not "the customer's address today". Had it been kept by reference, a customer
// who moved would make their old order look as if it had been sent to their new
// address.
//
// [CartAddress.SourceAddressID] only documents the ORIGIN; it is not used for
// reading and IS NOT A FOREIGN KEY (Principle 2.2).
type CartAddress struct {
	// ID is the "addr_" prefixed identifier.
	ID string
	// CartID is the cart the address belongs to.
	CartID string
	// Type is the type of the address (shipping/billing).
	Type AddressType
	// SourceAddressID is the identifier of the customer address it was copied
	// from; it may be empty.
	SourceAddressID string
	// The name, title and location fields; all of them are optional.
	FirstName  string
	LastName   string
	Company    string
	Address1   string
	Address2   string
	City       string
	Province   string
	PostalCode string
	// CountryCode is the ISO 3166-1 alpha-2 country code (e.g. "TR");
	// UPPERCASE.
	CountryCode string
	Phone       string
	// Metadata is the caller's free-form extra data.
	Metadata map[string]any
	// CreatedAt and UpdatedAt are UTC.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ShippingMethod is a shipping method selected for the cart.
//
// ShippingOptionID is the fulfillment module's identifier (it will arrive in
// Phase 7) and IS NOT A FOREIGN KEY; it may be empty, because in Phase 5 there
// is no option catalog yet.
type ShippingMethod struct {
	// ID is the "csm_" prefixed identifier.
	ID string
	// CartID is the cart the method belongs to.
	CartID string
	// Name is the method's displayed name.
	Name string
	// ShippingOptionID is the identifier of the option in the fulfillment
	// module; it may be empty.
	ShippingOptionID string
	// Amount is the shipping amount (minor unit). It is summed into the cart's
	// ShippingTotal by the workflow; this record does not write the total
	// itself.
	Amount int64
	// Data is provider-specific free-form data (e.g. the selected branch).
	Data map[string]any
	// CreatedAt and UpdatedAt are UTC.
	CreatedAt time.Time
	UpdatedAt time.Time
}
