package service

import (
	"context"

	"github.com/bdrtr/gobit/internal/core/errors"
	corepage "github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
)

// CreateCartInput holds the fields of a new cart.
type CreateCartInput struct {
	// RegionID is the cart's region; it is REQUIRED. That the region really
	// exists is NOT validated HERE: the side that knows the region module is the
	// workflow (ADR 0001/0006).
	RegionID string
	// CustomerID is the cart's owner; it is OPTIONAL. If it is left empty the
	// cart belongs to a GUEST.
	CustomerID string
	// Email is the cart's contact address; it is optional.
	Email string
	// CurrencyCode is the ISO 4217 code; it is REQUIRED.
	//
	// The currency is the region's data and it is COPIED from the region. The
	// reason the copy sits on the cart is historical: if the region changes its
	// currency later, the amounts of the open carts must not silently be read in
	// another currency.
	//
	// The side that does the copying is ALWAYS THE SERVER and it is OUTSIDE this
	// service: the create_cart workflow resolves both the region and the
	// currency from the country code, and the storefront end goes through that
	// flow too. The service cannot ask this question itself — it does not call
	// the region module (ADR 0006) — and that is why it only validates the SHAPE
	// of the code. The field once came from the storefront body, that is, FROM
	// THE CLIENT; the rationale for its removal is written in the
	// api.createCartRequest godoc.
	CurrencyCode string
	// Metadata is the caller's free-form extra data.
	Metadata map[string]any
}

// CreateCart creates a new cart.
//
// The cart's region and (if there is one) its customer sit IN THEIR OWN COLUMNS
// (carts.region_id / carts.customer_id) and both are written in the SAME INSERT
// as the cart row. That is why there is no half-formed cart: the row is either
// born together with its region and its owner or it is not born at all.
func (s *Service) CreateCart(ctx context.Context, in CreateCartInput) (models.Cart, error) {
	if err := requireID("region_id", in.RegionID); err != nil {
		return models.Cart{}, err
	}
	if in.CustomerID != "" {
		if err := requireID("customer_id", in.CustomerID); err != nil {
			return models.Cart{}, err
		}
	}
	currency, err := normalizeCurrency(in.CurrencyCode)
	if err != nil {
		return models.Cart{}, err
	}
	email, err := normalizeEmail(in.Email)
	if err != nil {
		return models.Cart{}, err
	}

	cart, err := s.store.CreateCart(ctx, models.Cart{
		ID:           models.NewCartID(),
		RegionID:     in.RegionID,
		CustomerID:   in.CustomerID,
		Email:        email,
		CurrencyCode: currency,
		Metadata:     in.Metadata,
	})
	if err != nil {
		return models.Cart{}, err
	}
	return cart, nil
}

// GetCart returns the cart together with its lines, addresses and shipping
// methods.
//
// The children are fetched with THREE fixed queries; whatever the number of
// lines or records, the number of queries does not change (there is no N+1). If
// the cart does not exist or has been soft deleted, errors.NotFound is returned.
//
// # Why a read-only transaction
//
// The four queries run on a SINGLE SNAPSHOT ([Store.WithReadTx]). No lock is
// taken; the only thing provided is that all four see the SAME state of the
// cart. Without the transaction, each query could take another connection from
// the pool and another snapshot: an intervening [Service.AddLineItem] or
// [Service.SetTotals] could make the cart header carry the NEW totals while the
// line list returned its OLD state, and the customer could be shown a cart that
// is inconsistent with itself, like "the total is 3000 but the single line is
// 1000". The data was not being corrupted because the write paths are locked;
// what was TEARING was only the read view — but that is what the customer sees
// on the checkout page.
func (s *Service) GetCart(ctx context.Context, cartID string) (models.CartDetail, error) {
	if err := requireID("cart_id", cartID); err != nil {
		return models.CartDetail{}, err
	}

	var detail models.CartDetail
	err := s.store.WithReadTx(ctx, func(ctx context.Context) error {
		cart, err := s.store.GetCart(ctx, cartID)
		if err != nil {
			return err
		}

		items, err := s.store.ListLineItems(ctx, cartID)
		if err != nil {
			return err
		}
		addresses, err := s.store.ListCartAddresses(ctx, cartID)
		if err != nil {
			return err
		}
		methods, err := s.store.ListShippingMethods(ctx, cartID)
		if err != nil {
			return err
		}

		detail = models.CartDetail{Cart: cart, Items: items, ShippingMethods: methods}
		for i := range addresses {
			switch addresses[i].Type {
			case models.AddressShipping:
				detail.ShippingAddress = &addresses[i]
			case models.AddressBilling:
				detail.BillingAddress = &addresses[i]
			}
		}
		return nil
	})
	if err != nil {
		return models.CartDetail{}, err
	}
	return detail, nil
}

// UpdateCartInput is the update of the cart's contact and ownership fields.
//
// The shape of the two fields is deliberately DIFFERENT, and the difference
// tells the contract: the email can be corrected and cleared, whereas ownership
// can only be ESTABLISHED.
type UpdateCartInput struct {
	// Email being nil means the email IS NOT TOUCHED; an empty string clears it.
	//
	// That is the reason it is a pointer: "do not send the field" and "empty the
	// field" are separate intents, and reducing the two to a single empty string
	// would mean that every request that does not carry an email in its body
	// silently deletes the cart's email.
	Email *string
	// CustomerID being empty means the customer IS NOT TOUCHED; if it is filled,
	// a GUEST cart is handed over to that customer.
	//
	// It IS NOT a pointer, because "empty it" is not a valid intent: taking the
	// ownership back would mean losing who opened the cart. Handing a cart that
	// already has an owner over to ANOTHER customer is rejected as well
	// (errors.Conflict); two different customers owning the same cart would
	// leave the question of who the order is written to unanswered.
	CustomerID string
}

// UpdateCart updates the cart's email and/or its customer.
//
// The real flow requires this: the customer opens the cart as a GUEST, enters
// their email at the checkout step and/or signs in along the way. Without this
// path the cart would have to be built from scratch and the lines would be lost;
// on top of that, because complete_cart reads the order's contact address from
// the cart, the gap would be carried over there.
//
// # Why it makes the totals stale
//
// The call runs in the [Service.mutate] frame, that is, it INCREMENTS the cart's
// shape counter and makes the totals stale. The reason is the change of
// ownership: the price can change by customer group and the tax by exemption,
// and once the cart's owner has changed the old calculation is no longer that
// cart's calculation. The side that knows which of them really changed is
// pricing/tax, not cart (ADR 0006); that is why the decision is made
// conservatively — one extra calculation round is cheaper than writing an order
// with the wrong amount.
//
// A completed cart cannot be written to: errors.Conflict is returned.
func (s *Service) UpdateCart(ctx context.Context, cartID string, in UpdateCartInput) (models.Cart, error) {
	var email *string
	if in.Email != nil {
		normalized, err := normalizeEmail(*in.Email)
		if err != nil {
			return models.Cart{}, err
		}
		email = &normalized
	}
	if in.CustomerID != "" {
		if err := requireID("customer_id", in.CustomerID); err != nil {
			return models.Cart{}, err
		}
	}
	if email == nil && in.CustomerID == "" {
		return models.Cart{}, errors.Invalid(CodeInvalidInput,
			"no field was given to update: email or customer_id is required")
	}

	updated, err := s.mutate(ctx, cartID, func(ctx context.Context, cart models.Cart) error {
		contact := models.CartContact{Email: cart.Email, CustomerID: cart.CustomerID}
		if email != nil {
			contact.Email = *email
		}
		if in.CustomerID != "" {
			if cart.CustomerID != "" && cart.CustomerID != in.CustomerID {
				return errors.Conflict(CodeCustomerMismatch,
					"the cart belongs to another customer: %s (requested: %s)", cart.CustomerID, in.CustomerID)
			}
			contact.CustomerID = in.CustomerID
		}

		_, err := s.store.UpdateCartContact(ctx, cart.ID, contact)
		return err
	})
	if err != nil {
		return models.Cart{}, err
	}
	return updated, nil
}

// ListCartsInput is the input of the cart listing.
type ListCartsInput struct {
	// CustomerID, if given, makes only that customer's carts come back.
	CustomerID *string
	// RegionID, if given, makes only that region's carts come back.
	RegionID *string
	// Completed, if given, filters the carts by completeness.
	Completed *bool
	// Page holds the pagination parameters.
	Page Page
}

// ListCarts returns the carts with pagination (WITHOUT LOADING their children).
//
// The second return value is the count of ALL of the rows matching the filter.
// The lines are not loaded here: fetching the children of dozens of carts per
// page would make the list heavy and open to N+1. The children arrive only with
// [Service.GetCart].
func (s *Service) ListCarts(ctx context.Context, in ListCartsInput) (CartPage, error) {
	page, err := in.Page.normalize()
	if err != nil {
		return CartPage{}, err
	}

	filter := models.CartFilter{
		Completed: in.Completed,
		Limit:     page.Limit,
		Offset:    page.Offset,
		After:     in.Page.After,
	}
	if in.CustomerID != nil {
		if err := requireID("customer_id", *in.CustomerID); err != nil {
			return CartPage{}, err
		}
		filter.CustomerID = in.CustomerID
	}
	if in.RegionID != nil {
		if err := requireID("region_id", *in.RegionID); err != nil {
			return CartPage{}, err
		}
		filter.RegionID = in.RegionID
	}

	// One row MORE than asked for is fetched and the extra one is dropped
	// below: that is how "is there a next page" is answered without a second
	// query, and it is what lets the cursor be absent on the last page.
	filter.Limit = page.Limit + 1

	carts, count, err := s.store.ListCarts(ctx, filter)
	if err != nil {
		return CartPage{}, err
	}

	result := CartPage{Items: carts, Count: count}
	if int64(len(carts)) > page.Limit {
		result.Items = carts[:page.Limit]
		last := result.Items[len(result.Items)-1]
		result.NextCursor = corepage.Encode(CartListing,
			corepage.Cursor{Time: last.CreatedAt, ID: last.ID})
	}

	return result, nil
}

// CartListing names the cart listing inside a cursor.
//
// A cursor carries the name of the listing it belongs to so that one handed to
// a different listing is REFUSED rather than silently selecting the wrong rows.
const CartListing = "carts"

// CartPage is one page of the cart listing.
type CartPage struct {
	// Items are the carts on this page.
	Items []models.Cart
	// Count is the total number of rows matching the filter, independent of the
	// page.
	Count int64
	// NextCursor is the opaque position the NEXT page starts below; empty means
	// this page is the last one.
	NextCursor string
}

// ListCartsByIDs returns the carts of the given identifiers in a SINGLE query.
// No record is returned for an identifier that is not found; that is not an
// error.
func (s *Service) ListCartsByIDs(ctx context.Context, ids []string) ([]models.Cart, error) {
	if len(ids) == 0 {
		return []models.Cart{}, nil
	}
	return s.store.CartsByIDs(ctx, ids)
}

// DeleteCart soft deletes the cart and its children.
//
// A COMPLETED cart cannot be deleted (errors.Conflict): it is the record the
// order rests on and deleting it would be destroying history.
func (s *Service) DeleteCart(ctx context.Context, cartID string) error {
	if err := requireID("cart_id", cartID); err != nil {
		return err
	}

	return s.store.WithTx(ctx, func(ctx context.Context) error {
		cart, err := s.store.LockCart(ctx, cartID)
		if err != nil {
			return err
		}
		if cart.Completed() {
			return completedError(cart.ID)
		}
		if err := s.store.SoftDeleteLineItemsByCart(ctx, cartID); err != nil {
			return err
		}
		if err := s.store.SoftDeleteCartAddressesByCart(ctx, cartID); err != nil {
			return err
		}
		if err := s.store.SoftDeleteShippingMethodsByCart(ctx, cartID); err != nil {
			return err
		}
		return s.store.SoftDeleteCart(ctx, cartID)
	})
}

// MarkCompleted stamps the cart as completed.
//
// The complete_cart saga in Phase 6 CALLS this; once the stamp is applied the
// cart CANNOT BE CHANGED and no second order can be born from the same cart.
//
// # Why a second call is a Conflict
//
// If completing an already completed cart counted as silently successful, a flow
// in which the same cart was ordered twice would produce an error nowhere. Retry
// safety (plan Section 2.6) is solved not here but in the workflow engine's
// idempotency key: a step that finished SUCCESSFULLY is not run again.
//
// # Why a cart without lines is rejected
//
// The order that would be born from a cart without lines is an order in which
// nothing was sold. The rule also closes a second hole: the "the totals were
// NEVER calculated" case. The staleness criterion is totals_revision ≠ revision,
// and because both are zero on a new cart, "never calculated" and "calculated
// for the zeroth shape" cannot be told apart. Nor do they NEED to be told apart:
// the counter never goes down anywhere and adding a line necessarily increments
// it, therefore if a cart with revision = totals_revision has a line then
// [Service.SetTotals] REALLY did run for that shape. What is left is only the
// cart that has never been touched (and is necessarily without lines); this gate
// rejects that one as well. The alternative was to add a "was it calculated"
// stamp to the schema — a path that gives the same result but grows the version
// ledger.
//
// # Why stale totals are rejected
//
// If the totals do not belong to the cart's current shape
// ([models.Cart.TotalsStale]), the cart changed AFTER it was calculated — the
// classic case of a line being added to the cart while the checkout page is
// open. Applying the stamp would turn the wrong amount of that moment into the
// order amount. That is why the call returns errors.Conflict and asks for
// calculate_totals to be run again.
func (s *Service) MarkCompleted(ctx context.Context, cartID string) (models.Cart, error) {
	if err := requireID("cart_id", cartID); err != nil {
		return models.Cart{}, err
	}

	var completed models.Cart
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		cart, err := s.store.LockCart(ctx, cartID)
		if err != nil {
			return err
		}
		if cart.Completed() {
			return completedError(cart.ID)
		}
		items, err := s.store.ListLineItems(ctx, cart.ID)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return errors.Conflict(CodeCartEmpty,
				"the cart cannot be completed: it has no lines at all (%s)", cart.ID)
		}
		if cart.TotalsStale() {
			return errors.Conflict(CodeTotalsStale,
				"the cart cannot be completed: the totals are not current (cart shape %d, totals %d); calculate_totals must be run again",
				cart.Revision, cart.TotalsRevision)
		}
		completed, err = s.store.MarkCartCompleted(ctx, cartID)
		return err
	})
	if err != nil {
		return models.Cart{}, err
	}
	return completed, nil
}
