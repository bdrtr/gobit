package cart

import (
	"encoding/json"

	"github.com/bdrtr/gobit/core/errors"
)

// Snapshot is the shape of the cart a calculation round IS BASED ON.
//
// The type is the schema of the [Carts.CartSnapshotJSON] body: the cart module
// produces these fields, this package reads them. The schema is DELIBERATELY
// narrow — it is whatever enters the calculation and nothing more. Unrecognized
// fields are silently skipped so that this package need not be updated when the
// cart module grows the schema.
//
// The snapshot is taken in a SINGLE READ and is consistent: the lines, the
// shipping methods and [Snapshot.Revision] belong to the same instant. Were
// there separate calls per field, a change slipping in between would lead to
// reading the lines from one shape and the revision from another, and the
// calculation would silently be stamped wrong.
type Snapshot struct {
	// ID is the identity of the cart.
	ID string `json:"id"`
	// RegionID is the region of the cart; the tax rate and the price context
	// come from it.
	RegionID string `json:"region_id"`
	// CustomerID is the owner of the cart; when empty the cart belongs to a guest.
	CustomerID string `json:"customer_id"`
	// CurrencyCode is the currency of the cart (ISO 4217).
	CurrencyCode string `json:"currency_code"`
	// Revision is the shape counter of the cart; it is the stamp of the calculation.
	Revision int64 `json:"revision"`
	// Completed reports whether the cart has been completed.
	Completed bool `json:"completed"`
	// Items are the lines of the cart.
	Items []SnapshotItem `json:"items"`
	// ShippingMethods are the shipping methods chosen for the cart.
	ShippingMethods []SnapshotShippingMethod `json:"shipping_methods"`

	// PromotionCodes are the coupon codes the cart HOLDS, in UPPER case.
	//
	// They come off the cart's own row and not off the call, and that is the
	// whole decision: a code passed into one entry point of the totals flow
	// would make the discount appear and disappear depending on which entry
	// point ran last, so raising a quantity by one would silently drop the
	// coupon (ADR 0109). This field is the first of the three points the
	// package comment named.
	PromotionCodes []string `json:"promotion_codes"`
	// Metadata is the cart's own free-form data; it becomes rule CONTEXT under a
	// prefix (see [Workflows.ruleContext] and ADR 0111).
	Metadata map[string]any `json:"metadata,omitempty"`
}

// SnapshotItem is the set of fields of a cart line that enter the calculation.
//
// The STORED amounts of the line are NOT here and must not be: every calculation
// round fetches the price from pricing again. Reading a stored amount and
// trusting it would mean that a price that changed in the catalog stays frozen in
// the cart forever.
type SnapshotItem struct {
	// ID is the identity of the line.
	ID string `json:"id"`
	// VariantID is the product variant the line points at.
	VariantID string `json:"variant_id"`
	// Quantity is the count on the line.
	Quantity int64 `json:"quantity"`
}

// SnapshotShippingMethod is the set of fields of a shipping method that enter
// the calculation.
type SnapshotShippingMethod struct {
	// ID is the identity of the method.
	ID string `json:"id"`
	// Amount is the shipping amount (minor unit).
	Amount int64 `json:"amount"`
}

// VariantIDs returns the variant identities of the lines WITHOUT DUPLICATES and
// in line order.
//
// The order is preserved so that the input of the bulk link query (and therefore
// the error messages it produces) stays reproducible; the deduplication is there
// so that a cart holding two lines of the same variant does not grow the link
// query needlessly.
func (s Snapshot) VariantIDs() []string {
	seen := make(map[string]struct{}, len(s.Items))
	out := make([]string, 0, len(s.Items))
	for i := range s.Items {
		id := s.Items[i].VariantID
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// decodeSnapshot decodes the body coming from the cart module and VALIDATES it.
//
// The validation is done even though the body comes from the cart module: this
// boundary is the only one the compiler cannot check (the accepted price of
// ADR 0006) and if a broken field silently entered the calculation, the fault
// would show up in the cart's total days later. A broken body is an
// errors.Internal — there is nothing the caller can fix, the provider has
// violated the contract.
func decodeSnapshot(cartID string, payload json.RawMessage) (Snapshot, error) {
	var snap Snapshot
	if len(payload) == 0 {
		return Snapshot{}, errors.Internal(CodeSnapshotInvalid,
			"cart snapshot came back empty: %s", cartID)
	}
	if err := json.Unmarshal(payload, &snap); err != nil {
		return Snapshot{}, errors.Wrap(err, errors.KindInternal, CodeSnapshotInvalid,
			"cart snapshot could not be decoded: %s", cartID)
	}
	if err := snap.validate(cartID); err != nil {
		return Snapshot{}, err
	}
	return snap, nil
}

// validate verifies that the snapshot is in a state that can enter the
// calculation.
func (s Snapshot) validate(cartID string) error {
	if s.ID != cartID {
		return errors.Internal(CodeSnapshotInvalid,
			"snapshot belongs to another cart: requested %s, received %q", cartID, s.ID)
	}
	if s.RegionID == "" {
		return errors.Internal(CodeSnapshotInvalid, "cart region is empty: %s", cartID)
	}
	if s.CurrencyCode == "" {
		return errors.Internal(CodeSnapshotInvalid, "cart currency is empty: %s", cartID)
	}
	if s.Revision < 0 {
		return errors.Internal(CodeSnapshotInvalid,
			"the cart shape counter is negative: %s (%d)", cartID, s.Revision)
	}

	for i := range s.Items {
		if err := s.Items[i].validate(cartID); err != nil {
			return err
		}
	}
	for i := range s.ShippingMethods {
		method := s.ShippingMethods[i]
		if method.Amount < 0 || method.Amount > MaxAmount {
			return errors.Internal(CodeSnapshotInvalid,
				"the shipping amount must be in the range [0, %d]: %s (%q -> %d)",
				MaxAmount, cartID, method.ID, method.Amount)
		}
	}
	return nil
}

// validate verifies that a single line is in a state that can enter the
// calculation.
func (i SnapshotItem) validate(cartID string) error {
	if i.ID == "" {
		return errors.Internal(CodeSnapshotInvalid, "cart has a line without an identity: %s", cartID)
	}
	if i.VariantID == "" {
		return errors.Internal(CodeSnapshotInvalid,
			"line variant is empty: %s (%q)", cartID, i.ID)
	}
	if i.Quantity < MinQuantity || i.Quantity > MaxQuantity {
		return errors.Internal(CodeSnapshotInvalid,
			"the line quantity must be in the range [%d, %d]: %s (%q -> %d)",
			MinQuantity, MaxQuantity, cartID, i.ID, i.Quantity)
	}
	return nil
}
