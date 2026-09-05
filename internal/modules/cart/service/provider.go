package service

import (
	"context"
	"maps"
	"slices"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
)

// The field names offered by the Query provider.
const (
	// FieldID is the record's identifier; Query does the joining over this
	// field.
	FieldID = query.IDField
	// FieldRegionID is the cart's region.
	FieldRegionID = "region_id"
	// FieldCustomerID is the cart's customer; it is empty on a guest cart.
	FieldCustomerID = "customer_id"
	// FieldEmail is the cart's contact address.
	FieldEmail = "email"
	// FieldCurrencyCode is the cart's currency.
	FieldCurrencyCode = "currency_code"
	// FieldSubtotal is the sum of the line subtotals (minor unit).
	FieldSubtotal = "subtotal"
	// FieldDiscountTotal is the total discount (minor unit).
	FieldDiscountTotal = "discount_total"
	// FieldTaxTotal is the total tax (minor unit).
	FieldTaxTotal = "tax_total"
	// FieldShippingTotal is the total shipping amount (minor unit).
	FieldShippingTotal = "shipping_total"
	// FieldTotal is the amount payable (minor unit).
	FieldTotal = "total"
	// FieldTotalsStale reports that the totals DO NOT belong to the current
	// shape of the cart. The field is derived; it is offered TOGETHER WITH the
	// totals so that a stale amount on the cart is not taken for a correct one.
	FieldTotalsStale = "totals_stale"
	// FieldCompleted reports whether the cart is completed.
	FieldCompleted = "completed"
	// FieldCompletedAt is the moment the cart was completed; nil if it is not
	// completed.
	FieldCompletedAt = "completed_at"
	// FieldCreatedAt is the creation time.
	FieldCreatedAt = "created_at"
	// FieldUpdatedAt is the time of the last update.
	FieldUpdatedAt = "updated_at"
)

// cartFieldGetters are the extractors of the offered fields.
//
// The field set being defined in a single place makes it impossible for the
// validation and the production to diverge: if a field that is not here is
// requested, errors.Invalid is returned (ADR 0004), and every field that is here
// can also be produced.
var cartFieldGetters = map[string]func(cart models.Cart) any{
	FieldID:            func(c models.Cart) any { return c.ID },
	FieldRegionID:      func(c models.Cart) any { return c.RegionID },
	FieldCustomerID:    func(c models.Cart) any { return c.CustomerID },
	FieldEmail:         func(c models.Cart) any { return c.Email },
	FieldCurrencyCode:  func(c models.Cart) any { return c.CurrencyCode },
	FieldSubtotal:      func(c models.Cart) any { return c.Subtotal },
	FieldDiscountTotal: func(c models.Cart) any { return c.DiscountTotal },
	FieldTaxTotal:      func(c models.Cart) any { return c.TaxTotal },
	FieldShippingTotal: func(c models.Cart) any { return c.ShippingTotal },
	FieldTotal:         func(c models.Cart) any { return c.Total },
	FieldTotalsStale:   func(c models.Cart) any { return c.TotalsStale() },
	FieldCompleted:     func(c models.Cart) any { return c.Completed() },
	FieldCompletedAt: func(c models.Cart) any {
		if c.CompletedAt == nil {
			return nil
		}
		return *c.CompletedAt
	},
	FieldCreatedAt: func(c models.Cart) any { return c.CreatedAt },
	FieldUpdatedAt: func(c models.Cart) any { return c.UpdatedAt },
}

// QueryProvider is the read surface the cart module opens to the Query layer.
//
// It is registered in the container under the name "cart.query"; Query resolves
// it BY NAME (ADR 0004). The provider DOES NOT OFFER THE LINES: a cart's lines
// are an unpaginated set of variable length per cart, and embedding them into a
// Record would not obey Query's join key contract (a single "id" field). The
// lines are read with [Service.GetCart].
type QueryProvider struct {
	svc *Service
}

// That QueryProvider satisfies the core contract is verified at compile time;
// a signature drift does not survive to runtime.
var _ query.Provider = (*QueryProvider)(nil)

// NewQueryProvider produces a provider that works over the given service.
func NewQueryProvider(svc *Service) *QueryProvider {
	return &QueryProvider{svc: svc}
}

// Entity returns the entity name the provider offers.
func (p *QueryProvider) Entity() string {
	return EntityName
}

// List returns the root records.
//
// The supported filters: "customer_id" (string), "region_id" (string) and
// "completed" (bool). Any other filter or an unrecognized field is rejected with
// errors.Invalid (ADR 0004).
//
// The limit is CLAMPED to [MaxLimit]; see [providerLimit].
func (p *QueryProvider) List(ctx context.Context, opts query.ListOptions) ([]query.Record, error) {
	if err := validateFields(opts.Fields); err != nil {
		return nil, err
	}

	in := ListCartsInput{
		Page: Page{Limit: providerLimit(opts.Limit), Offset: int64(opts.Offset)},
	}
	// The filters are walked SORTED BY NAME: map order is random, and if more
	// than one filter were invalid at once, which error would be returned would
	// be random too.
	for _, name := range slices.Sorted(maps.Keys(opts.Filters)) {
		value := opts.Filters[name]
		switch name {
		case FieldCustomerID:
			id, ok := value.(string)
			if !ok {
				return nil, errors.Invalid(CodeInvalidInput,
					"the %q filter must be text, %T given", name, value)
			}
			in.CustomerID = &id
		case FieldRegionID:
			id, ok := value.(string)
			if !ok {
				return nil, errors.Invalid(CodeInvalidInput,
					"the %q filter must be text, %T given", name, value)
			}
			in.RegionID = &id
		case FieldCompleted:
			flag, ok := value.(bool)
			if !ok {
				return nil, errors.Invalid(CodeInvalidInput,
					"the %q filter must be boolean (bool), %T given", name, value)
			}
			in.Completed = &flag
		default:
			return nil, errors.Invalid(CodeInvalidInput,
				"the %q entity does not support the %q filter", EntityName, name)
		}
	}

	result, err := p.svc.ListCarts(ctx, in)
	if err != nil {
		return nil, err
	}
	return records(result.Items, opts.Fields), nil
}

// FetchByIDs returns the records of the given identifiers as a BATCH.
// No record is returned for an identifier that is not found; that is not an
// error.
func (p *QueryProvider) FetchByIDs(ctx context.Context, ids, fields []string) ([]query.Record, error) {
	if err := validateFields(fields); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []query.Record{}, nil
	}

	carts, err := p.svc.ListCartsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return records(carts, fields), nil
}

// records converts the carts into records with the requested fields.
func records(carts []models.Cart, fields []string) []query.Record {
	selected := fields
	if len(selected) == 0 {
		selected = slices.Sorted(maps.Keys(cartFieldGetters))
	}

	out := make([]query.Record, 0, len(carts))
	// The loop is walked by index: the cart struct is large and copying it by
	// value would carry a few hundred bytes for nothing on every turn.
	for i := range carts {
		record := make(query.Record, len(selected))
		for _, name := range selected {
			record[name] = cartFieldGetters[name](carts[i])
		}
		out = append(out, record)
	}
	return out
}

// providerLimit clamps the core's limit value to the provider's page ceiling.
//
// In the core contract ([query.ListOptions]) 0 means UNLIMITED; this provider
// does not offer unlimited listing, because an unlimited root query would pull
// the whole cart table into memory. An unlimited request is therefore turned
// into [MaxLimit] — NOT into [DefaultLimit]: the caller has explicitly said "I
// want all of them" and should get the most it can get. A meaningless negative
// value is put in the same basket: on this path the limit is not a client input
// but a number coming from another module's query definition, and rejecting it
// would bring the whole read down.
func providerLimit(limit int) int64 {
	if limit <= 0 || int64(limit) > MaxLimit {
		return MaxLimit
	}
	return int64(limit)
}

// validateFields verifies that all of the requested fields are offered.
func validateFields(fields []string) error {
	for _, name := range fields {
		if _, ok := cartFieldGetters[name]; !ok {
			return errors.Invalid(CodeInvalidInput,
				"the %q entity does not offer the %q field", EntityName, name)
		}
	}
	return nil
}
