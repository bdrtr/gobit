package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
)

// MaxLineItems is the largest number of DISTINCT lines a cart may carry
// (ADR 0227).
//
// # Why there is a ceiling
//
// Every write that changes a cart reprices ALL its lines and rewrites the
// amount of all of them, so building an N-line cart costs N² line amount
// writes: 5,050 for 100 lines and 500,500 for 1,000. Pricing's bulk read, the
// totals round's one request for every line, refuses more than its own ceiling
// (1,000 today, pricing's MaxCalculateItems); a cart past it could never be
// priced again and so never bought. The value is the page size ceiling of this
// module's lists, so a full cart's lines fit on one page.
//
// # Where it is asked
//
// In [Service.openLine], the one place a line is created, which both paths that
// open one call: [Service.AddLineItem] and the merge. It is asked there because
// that is where a line's identity is decided — a variant and its properties
// (ADR 0223) — and under the cart's lock, so two concurrent additions cannot
// both pass it. A write that raises the quantity of an existing line opens
// nothing and is never refused: the owner of a full cart can still change their
// own lines, and a cart opened before the ceiling with more lines than it stays
// priceable and payable.
const MaxLineItems = 100

// CodeLineLimit is the refusal of a line past [MaxLineItems]. The code is the
// one the cart workflow answered while it held the ceiling, kept so that a
// client handling it does not have to change (ADR 0227).
const CodeLineLimit = "cart_workflow_line_limit_reached"

// openLine creates a line on a cart holding lines living lines, unless the cart
// is at [MaxLineItems]. The caller holds the cart's lock and has decided the
// line is a new one; every line this service creates is created here.
func (s *Service) openLine(ctx context.Context, item models.LineItem, lines int) (models.LineItem, error) {
	if lines >= MaxLineItems {
		return models.LineItem{}, errors.Invalid(CodeLineLimit,
			"a cart can carry at most %d lines; cart %s has %d lines (the quantity of an existing line can be increased)",
			MaxLineItems, item.CartID, lines)
	}
	return s.store.CreateLineItem(ctx, item)
}

// MaxLineAddOns is how many add-ons one line carries (ADR 0229).
const MaxLineAddOns = 10

// The refusals of an add-on line (ADR 0229).
const (
	// CodeAddOnInvalid reports add-ons a line cannot carry: too many, one
	// named twice, or one without its variant, title or price.
	CodeAddOnInvalid = "cart_line_add_on_invalid"
	// CodeAddOnFollows reports a write to an add-on alone: its quantity is its
	// line's, and it goes when the line goes.
	CodeAddOnFollows = "cart_line_is_an_add_on"
)

// AddOnInput is one add-on line opened with the line it belongs to
// (ADR 0229). Its price is the flow's, as the line's is.
type AddOnInput struct {
	VariantID  string
	Title      string
	UnitPrice  int64
	Properties map[string]string
}

// AddLineItemInput holds the fields of the line to be added to the cart.
type AddLineItemInput struct {
	// VariantID is the product variant being added; it is REQUIRED. It belongs
	// to the product module, its existence is not validated here (ADR 0001) and
	// no foreign key is given.
	VariantID string
	// Title is the line's display name; it is REQUIRED. It is COPIED from the
	// variant: even if the catalog changes later, the name seen in the cart does
	// not change.
	Title string
	// Quantity is the quantity to be added; it must be POSITIVE.
	Quantity int64
	// UnitPrice is the unit price (minor unit).
	//
	// THE CLIENT DOES NOT and cannot give its value: there is no price field in
	// the storefront body (see addLineItemRequest in the api package) and the
	// only way to open a line is the add_line_item flow, which takes the price
	// from the pricing module. Its looking optional here is not a shortcoming of
	// the service but its boundary — the module cannot know whether the price is
	// CORRECT, it only checks its range; that is why the authority over the price
	// is guarded by WHO the caller is, and the only caller is the flow.
	//
	// The final value is written by the flow as well: the calculation round that
	// runs right after the line is added re-prices all of the lines with the
	// current quantity and writes the result with [Service.SetTotals].
	UnitPrice int64
	// Metadata is the caller's free-form extra data.
	Metadata map[string]any
	// Properties are what the shopper wrote on the line (ADR 0223); they are
	// checked by [models.NormalizeLineProperties].
	Properties map[string]string
	// AddOns are the add-on lines opened with this one, bound to it
	// (ADR 0229); their quantity is the line's. They are part of what the line
	// is, so they are given with it and never attached later.
	AddOns []AddOnInput
}

// AddLineItem adds a line to the cart.
//
// # What happens if the same variant is added a second time
//
// A NEW LINE IS NOT OPENED; the QUANTITY of the existing line GOES UP. The
// decision rests on three grounds:
//
//  1. Price tiers. The pricing module picks the price by quantity range
//     (min_quantity/max_quantity). If the same variant is split into two lines
//     as 3 + 2, both lines are priced from the "1-4" tier and the customer DOES
//     NOT GET the "5+" price they earned. When the quantity is summed into a
//     single line, the tier is picked correctly.
//  2. Stock reservation. complete_cart in Phase 6 makes a reservation per line;
//     two lines of the same variant mean two separate reservations for the same
//     stock, and the compensation gets complicated on a partial success.
//  3. Customer expectation. The same product appearing twice in the cart gives
//     the impression that the products are different.
//
// The decision is enforced at the database level too: the
// cart_line_items_identity_uniq partial unique index prevents even a write path
// that somehow gets around the cart lock from opening the second line.
//
// In the merge only the QUANTITY is carried over; the existing line's title,
// unit price and metadata are PRESERVED.
//
// # Properties make another line
//
// Since ADR 0223 the criterion is the variant AND its properties: the same
// variant with another engraving is another line, and with the same engraving
// the quantity goes up. A variant split across lines by its properties is priced
// at each line's quantity and reserved line by line; that is the price of two
// different things being made from one variant.
func (s *Service) AddLineItem(ctx context.Context, cartID string, in AddLineItemInput) (models.LineItem, error) {
	if err := requireID("variant_id", in.VariantID); err != nil {
		return models.LineItem{}, err
	}
	title := strings.TrimSpace(in.Title)
	if err := requireText("title", title); err != nil {
		return models.LineItem{}, err
	}
	if err := checkQuantity(in.Quantity); err != nil {
		return models.LineItem{}, err
	}
	if err := checkAmount("unit_price", in.UnitPrice, models.MaxAmount); err != nil {
		return models.LineItem{}, err
	}
	properties, err := models.NormalizeLineProperties(in.Properties)
	if err != nil {
		return models.LineItem{}, err
	}
	addOns, key, err := normalizeAddOns(in.AddOns)
	if err != nil {
		return models.LineItem{}, err
	}

	var item models.LineItem
	_, err = s.mutate(ctx, cartID, func(ctx context.Context, cart models.Cart) error {
		existing, err := s.store.GetLineItemByVariant(ctx, cart.ID, in.VariantID, properties, key)
		switch {
		case err == nil:
			// The sum is checked without overflow: even if the sum of the two
			// quantities fits into an int64, it cannot go over the model's
			// quantity ceiling.
			if existing.Quantity > models.MaxQuantity-in.Quantity {
				return errors.Invalid(CodeInvalidInput,
					"the line quantity exceeds the limit once merged: %d + %d > %d",
					existing.Quantity, in.Quantity, models.MaxQuantity)
			}
			item, err = s.store.SetLineItemQuantity(ctx, cart.ID, existing.ID, existing.Quantity+in.Quantity)
			if err != nil || key == "" {
				return err
			}
			// The same add-ons are already under the line: they follow it.
			return s.store.SetAddOnQuantities(ctx, cart.ID, existing.ID, item.Quantity)
		case errors.IsNotFound(err):
			lines, err := s.store.CountLineItems(ctx, cart.ID)
			if err != nil {
				return err
			}
			item, err = s.openLine(ctx, models.LineItem{
				ID:         models.NewLineItemID(),
				CartID:     cart.ID,
				VariantID:  in.VariantID,
				Title:      title,
				Quantity:   in.Quantity,
				UnitPrice:  in.UnitPrice,
				Metadata:   in.Metadata,
				Properties: properties,
				AddOnKey:   key,
			}, lines)
			if err != nil {
				return err
			}
			// Each add-on is a line of its own against the ceiling; one past it
			// refuses the whole add, the line included, since the transaction
			// rolls back.
			for i := range addOns {
				parent := item.ID
				if _, err := s.openLine(ctx, models.LineItem{
					ID:           models.NewLineItemID(),
					CartID:       cart.ID,
					VariantID:    addOns[i].VariantID,
					Title:        addOns[i].Title,
					Quantity:     in.Quantity,
					UnitPrice:    addOns[i].UnitPrice,
					Properties:   addOns[i].Properties,
					ParentLineID: &parent,
				}, lines+1+i); err != nil {
					return err
				}
			}
			return nil
		default:
			return err
		}
	})
	if err != nil {
		return models.LineItem{}, err
	}
	return item, nil
}

// UpdateLineItemQuantity writes the line's quantity as an ABSOLUTE value.
//
// If quantity is zero or negative, errors.Invalid is returned; the line IS NOT
// DELETED. "Set the quantity to zero" and "remove the line" are separate
// intents and they have separate methods ([Service.RemoveLineItem]); turning one
// into the other would mean that a bug sending zero into the quantity field
// silently deletes data.
func (s *Service) UpdateLineItemQuantity(ctx context.Context, cartID, lineID string, quantity int64) (models.LineItem, error) {
	if err := requireID("line_item_id", lineID); err != nil {
		return models.LineItem{}, err
	}
	if err := checkQuantity(quantity); err != nil {
		return models.LineItem{}, err
	}

	var item models.LineItem
	_, err := s.mutate(ctx, cartID, func(ctx context.Context, cart models.Cart) error {
		line, err := s.store.GetLineItem(ctx, cart.ID, lineID)
		if err != nil {
			return err
		}
		if line.ParentLineID != nil {
			return addOnFollows(lineID)
		}
		item, err = s.store.SetLineItemQuantity(ctx, cart.ID, lineID, quantity)
		if err != nil || line.AddOnKey == "" {
			return err
		}
		// A line's add-ons follow its quantity (ADR 0229).
		return s.store.SetAddOnQuantities(ctx, cart.ID, lineID, quantity)
	})
	if err != nil {
		return models.LineItem{}, err
	}
	return item, nil
}

// RemoveLineItem removes the line from the cart (soft delete).
// If the line is not in the cart, errors.NotFound is returned.
func (s *Service) RemoveLineItem(ctx context.Context, cartID, lineID string) error {
	if err := requireID("line_item_id", lineID); err != nil {
		return err
	}
	_, err := s.mutate(ctx, cartID, func(ctx context.Context, cart models.Cart) error {
		line, err := s.store.GetLineItem(ctx, cart.ID, lineID)
		if err != nil {
			return err
		}
		if line.ParentLineID != nil {
			return addOnFollows(lineID)
		}
		// A line's add-ons go with it (ADR 0229).
		if line.AddOnKey != "" {
			if err := s.store.SoftDeleteAddOnLines(ctx, cart.ID, lineID); err != nil {
				return err
			}
		}
		return s.store.SoftDeleteLineItem(ctx, cart.ID, lineID)
	})
	return err
}

// addOnFollows refuses a write to an add-on alone (ADR 0229). The add-ons are
// part of what their line is, so dropping one would make the line another; the
// line is removed and added again with the add-ons wanted.
func addOnFollows(lineID string) error {
	return errors.Invalid(CodeAddOnFollows,
		"line %s is an add-on: its quantity is its line's and it is removed with it", lineID)
}

// normalizeAddOns checks the add-ons given with a line and returns them with
// their properties normalized, and the digest they make the line's identity
// with ([models.AddOnKey]).
func normalizeAddOns(in []AddOnInput) ([]AddOnInput, string, error) {
	if len(in) == 0 {
		return nil, "", nil
	}
	if len(in) > MaxLineAddOns {
		return nil, "", errors.Invalid(CodeAddOnInvalid,
			"a line carries at most %d add-ons, %d given", MaxLineAddOns, len(in))
	}
	out := make([]AddOnInput, 0, len(in))
	identities := make([]models.AddOn, 0, len(in))
	for i := range in {
		addOn := in[i]
		if err := requireID("add_on variant_id", addOn.VariantID); err != nil {
			return nil, "", errors.Invalid(CodeAddOnInvalid, "add-on %d names no variant", i)
		}
		addOn.Title = strings.TrimSpace(addOn.Title)
		if err := requireText("add_on title", addOn.Title); err != nil {
			return nil, "", err
		}
		if err := checkAmount("add_on unit_price", addOn.UnitPrice, models.MaxAmount); err != nil {
			return nil, "", err
		}
		properties, err := models.NormalizeLineProperties(addOn.Properties)
		if err != nil {
			return nil, "", err
		}
		addOn.Properties = properties
		for _, earlier := range out {
			if earlier.VariantID == addOn.VariantID {
				return nil, "", errors.Invalid(CodeAddOnInvalid,
					"the add-on %s is named twice on one line", addOn.VariantID)
			}
		}
		out = append(out, addOn)
		identities = append(identities, models.AddOn{VariantID: addOn.VariantID, Properties: properties})
	}
	return out, models.AddOnKey(identities), nil
}

// LinesOfCarts returns the living lines of the given carts in one read, by
// cart, each cart's in the order they were written (ADR 0290).
func (s *Service) LinesOfCarts(ctx context.Context, cartIDs []string) (map[string][]models.LineItem, error) {
	lines, err := s.store.ListLineItemsOfCarts(ctx, cartIDs)
	if err != nil {
		return nil, err
	}

	out := make(map[string][]models.LineItem, len(cartIDs))
	for i := range lines {
		out[lines[i].CartID] = append(out[lines[i].CartID], lines[i])
	}

	return out, nil
}

// AddressesOfCarts returns the shipping and the billing address of each of the
// given carts that has one, in one read (ADR 0291, ADR 0303).
func (s *Service) AddressesOfCarts(
	ctx context.Context, cartIDs []string,
) (shipping, billing map[string]models.CartAddress, err error) {
	addresses, err := s.store.ListCartAddressesOfCarts(ctx, cartIDs)
	if err != nil {
		return nil, nil, err
	}

	shipping = make(map[string]models.CartAddress, len(cartIDs))
	billing = make(map[string]models.CartAddress, len(cartIDs))
	for i := range addresses {
		switch addresses[i].Type {
		case models.AddressShipping:
			shipping[addresses[i].CartID] = addresses[i]
		case models.AddressBilling:
			billing[addresses[i].CartID] = addresses[i]
		}
	}

	return shipping, billing, nil
}

// ShippingMethodsOfCarts returns the living shipping methods of the given
// carts in one read, by cart (ADR 0291).
func (s *Service) ShippingMethodsOfCarts(
	ctx context.Context, cartIDs []string,
) (map[string][]models.ShippingMethod, error) {
	methods, err := s.store.ListShippingMethodsOfCarts(ctx, cartIDs)
	if err != nil {
		return nil, err
	}

	out := make(map[string][]models.ShippingMethod, len(cartIDs))
	for i := range methods {
		out[methods[i].CartID] = append(out[methods[i].CartID], methods[i])
	}

	return out, nil
}
