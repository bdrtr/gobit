package service

import (
	"context"
	"maps"
	"slices"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
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
	// FieldLines is the cart's living lines in the order they were written,
	// each a record keyed by the Line* names below (ADR 0290); an empty list
	// for a cart with none. It costs a read only when asked for, like the
	// payment collection's movements.
	FieldLines = "lines"
)

// The keys of one entry of [FieldLines], part of the entity's contract for the
// lines' reason.
const (
	// LineID is the line's identifier.
	LineID = "id"
	// LineVariantID is the variant the line sells.
	LineVariantID = "variant_id"
	// LineTitle is the line's title.
	LineTitle = "title"
	// LineQuantity is how many units it holds.
	LineQuantity = "quantity"
	// LineUnitPrice is one unit's price (minor unit).
	LineUnitPrice = "unit_price"
	// LineTotal is the line's total (minor unit).
	LineTotal = "total"
	// LineParentID is the line an add-on follows; empty for a line standing
	// on its own (ADR 0229).
	LineParentID = "parent_line_id"
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
// it BY NAME (ADR 0004).
//
// It offers the lines as one list-valued field since ADR 0290. This comment used
// to refuse them, as an unpaginated set of variable length that a Record could
// not carry under Query's single join key. A list is a field and not a join, as
// the payment collection's movements had shown (ADR 0170), and since ADR 0227 a
// cart holds at most [MaxLineItems] lines.
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
// The supported filters: "id" (text or a list of text), "customer_id" (string),
// "region_id" (string) and "completed" (bool). Any other filter or an
// unrecognized field is rejected with errors.Invalid (ADR 0004).
//
// The id filter is the order provider's, for the reason it gives: a caller
// holding a cart's id reads that cart through a root query, and the panel's
// cart page found the filter missing the way the order page had (ADR 0290).
//
// The limit is CLAMPED to [MaxLimit]; see [providerLimit].
func (p *QueryProvider) List(ctx context.Context, opts query.ListOptions) ([]query.Record, error) {
	if err := validateFields(opts.Fields); err != nil {
		return nil, err
	}

	// An id filter is the batch read, and it selects by identity alone.
	if raw, ok := opts.Filters[FieldID]; ok {
		if len(opts.Filters) > 1 {
			return nil, errors.Invalid(CodeInvalidInput,
				"the %q filter selects records by identity and cannot be combined with another "+
					"filter", FieldID)
		}
		ids, err := idFilter(raw)
		if err != nil {
			return nil, err
		}

		return p.FetchByIDs(ctx, ids, opts.Fields)
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
	lines, err := p.lines(ctx, result.Items, opts.Fields)
	if err != nil {
		return nil, err
	}
	return records(result.Items, opts.Fields, lines), nil
}

// idFilter reads the id filter's value: one id, or a list of them.
func idFilter(raw any) ([]string, error) {
	switch value := raw.(type) {
	case string:
		return []string{value}, nil
	case []string:
		return value, nil
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			id, ok := item.(string)
			if !ok {
				return nil, errors.Invalid(CodeInvalidInput,
					"the values of filter %q have to be text, %T given", FieldID, item)
			}
			out = append(out, id)
		}

		return out, nil
	default:
		return nil, errors.Invalid(CodeInvalidInput,
			"filter %q has to be text or a list of text, %T given", FieldID, raw)
	}
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
	lines, err := p.lines(ctx, carts, fields)
	if err != nil {
		return nil, err
	}
	return records(carts, fields, lines), nil
}

// lines reads the carts' lines in one read, ONLY when [FieldLines] was asked
// for — a read that names no field asks for every field.
func (p *QueryProvider) lines(
	ctx context.Context, carts []models.Cart, fields []string,
) (map[string][]models.LineItem, error) {
	if len(carts) == 0 || (len(fields) > 0 && !slices.Contains(fields, FieldLines)) {
		return nil, nil
	}

	ids := make([]string, 0, len(carts))
	for i := range carts {
		ids = append(ids, carts[i].ID)
	}

	return p.svc.LinesOfCarts(ctx, ids)
}

// offeredFields is every field this entity offers, sorted: the getters PLUS
// the lines, which do not come off the cart row.
func offeredFields() []string {
	return slices.Sorted(slices.Values(append(slices.Collect(maps.Keys(cartFieldGetters)), FieldLines)))
}

// records converts the carts into records with the requested fields.
func records(carts []models.Cart, fields []string, lines map[string][]models.LineItem) []query.Record {
	selected := fields
	if len(selected) == 0 {
		selected = offeredFields()
	}

	out := make([]query.Record, 0, len(carts))
	// The loop is walked by index: the cart struct is large and copying it by
	// value would carry a few hundred bytes for nothing on every turn.
	for i := range carts {
		record := make(query.Record, len(selected))
		for _, name := range selected {
			if name == FieldLines {
				record[name] = lineRecords(lines[carts[i].ID])
				continue
			}
			record[name] = cartFieldGetters[name](carts[i])
		}
		out = append(out, record)
	}
	return out
}

// lineRecords turns one cart's lines into the field's value; never nil, so a
// cart with no line answers an empty list rather than nothing.
func lineRecords(list []models.LineItem) []map[string]any {
	out := make([]map[string]any, 0, len(list))
	for i := range list {
		parent := ""
		if list[i].ParentLineID != nil {
			parent = *list[i].ParentLineID
		}
		out = append(out, map[string]any{
			LineID:        list[i].ID,
			LineVariantID: list[i].VariantID,
			LineTitle:     list[i].Title,
			LineQuantity:  list[i].Quantity,
			LineUnitPrice: list[i].UnitPrice,
			LineTotal:     list[i].Total,
			LineParentID:  parent,
		})
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
		if name == FieldLines {
			// The lines have no getter: they do not come off the cart row.
			continue
		}
		if _, ok := cartFieldGetters[name]; !ok {
			return errors.Invalid(CodeInvalidInput,
				"the %q entity does not offer the %q field", EntityName, name)
		}
	}
	return nil
}
