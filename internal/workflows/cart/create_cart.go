package cart

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
)

// CreateCartInput is the input for a new cart.
type CreateCartInput struct {
	// CountryCode is the customer's country (ISO 3166-1 alpha-2); it is
	// MANDATORY. The cart's region and currency are derived from it.
	CountryCode string
	// CustomerID is the cart's owner; it is OPTIONAL. If it is left empty the
	// cart belongs to a GUEST and no customer call is made at all.
	CustomerID string
	// Email is the cart's contact address; it is optional. If it is left empty
	// on a registered customer's cart, the customer's registered address is used.
	Email string
	// Metadata is the FREE-FORM JSON object to attach to the cart; it is optional.
	//
	// The flow does NOT READ it and lets it into none of its decisions, it only
	// carries it to the cart: the field really is the caller's own data (the
	// campaign source, the storefront session) and it has no counterpart that
	// could be derived. The criterion of the distinction is the same as
	// [CountryCode]'s — there what was put in the body was the server's data,
	// here it is not.
	Metadata json.RawMessage
}

// CreateCartResult holds the fields of the created cart that concern the caller.
type CreateCartResult struct {
	// CartID is the id of the created cart.
	CartID string
	// RegionID is the region the cart is bound to.
	RegionID string
	// CurrencyCode is the cart's currency.
	CurrencyCode string
	// CustomerID is the cart's owner; it is empty on a guest cart.
	CustomerID string
	// Email is the cart's contact address.
	Email string
	// Guest reports whether the cart belongs to a guest.
	Guest bool
}

// CreateCart resolves the region from the country code and creates the cart.
//
// The flow touches three modules: it takes the region and the currency from region,
// it validates the registered customer in customer, it writes the cart to cart.
// There is a single write, so no compensation (saga) is needed; the reasoning is in
// the package comment.
//
// # Guest and registered customer
//
// If CustomerID is empty the cart is the guest's and the customer module is NEVER
// called: the guest flow not depending on the registered-customer service keeps the
// ability of a customer without an account to open a cart from hanging on the
// customer module being up.
//
// If CustomerID is set the customer is VALIDATED. The validation is done by reading
// the email address, and it does two jobs at once: if the customer does not exist
// the call stops here with errors.NotFound (otherwise the cart would be bound to a
// customer that does not exist and the error would only be seen at the order stage),
// and if no email was given the customer's registered address passes into the cart.
// The second is not an arbitrary convenience: the cart's contact address will be the
// order's contact address in Phase 6, and leaving a registered customer's cart
// without an address means asking for that information again at the payment step.
//
// If the caller gave an email address the customer's is NOT OVERWRITTEN: the cart's
// address is the address that order will be sent to, and not the current address in
// the customer ledger.
//
// # Why the currency's decimal digits are not used
//
// [Regions.RegionCurrency] returns the number of digits as well; the cart does NOT
// STORE it. Money is a whole number of minor units everywhere (plan Section 8) and
// the number of digits is needed only for PRESENTATION — the layer that displays the
// amount can read it back from the currency code. A digit count copied into the cart
// would go stale silently the day the reference table was corrected.
func (w *Workflows) CreateCart(ctx context.Context, in CreateCartInput) (CreateCartResult, error) {
	country := strings.TrimSpace(in.CountryCode)
	if country == "" {
		return CreateCartResult{}, errors.Invalid(CodeInvalidInput, "country_code cannot be empty")
	}
	if in.CustomerID != "" {
		if err := requireID("customer_id", in.CustomerID); err != nil {
			return CreateCartResult{}, err
		}
	}

	regionID, err := w.regions.RegionIDForCountry(ctx, country)
	if err != nil {
		return CreateCartResult{}, err
	}
	currency, _, err := w.regions.RegionCurrency(ctx, regionID)
	if err != nil {
		return CreateCartResult{}, err
	}

	email := strings.TrimSpace(in.Email)
	if in.CustomerID != "" {
		known, customerErr := w.customers.CustomerEmail(ctx, in.CustomerID)
		if customerErr != nil {
			return CreateCartResult{}, customerErr
		}
		if email == "" {
			email = known
		}
	}

	cartID, err := w.carts.OpenCart(ctx, regionID, currency, in.CustomerID, email, in.Metadata)
	if err != nil {
		return CreateCartResult{}, err
	}

	w.log.InfoContext(ctx, "cart opened",
		"cart_id", cartID, "region_id", regionID, "currency_code", currency,
		"guest", in.CustomerID == "")

	return CreateCartResult{
		CartID:       cartID,
		RegionID:     regionID,
		CurrencyCode: currency,
		CustomerID:   in.CustomerID,
		Email:        email,
		Guest:        in.CustomerID == "",
	}, nil
}
