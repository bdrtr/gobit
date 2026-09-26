package service

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// The field names offered by the Query provider.
const (
	// FieldID is the record's identifier; Query does the joining over this
	// field.
	FieldID = query.IDField
	// FieldDisplayID is the human readable number of the order.
	FieldDisplayID = "display_id"
	// FieldStatus is the status of the order.
	FieldStatus = "status"
	// FieldRegionID is the region of the order.
	FieldRegionID = "region_id"
	// FieldCustomerID is the customer of the order; empty on a guest order.
	FieldCustomerID = "customer_id"
	// FieldEmail is the contact address of the order.
	FieldEmail = "email"
	// FieldCurrencyCode is the currency of the order.
	FieldCurrencyCode = "currency_code"
	// FieldCartID is the cart the order was born from; it may be empty.
	FieldCartID = "cart_id"
	// FieldSubtotal is the sum of the line subtotals (minor unit).
	FieldSubtotal = "subtotal"
	// FieldDiscountTotal is the total discount (minor unit).
	FieldDiscountTotal = "discount_total"
	// FieldTaxTotal is the total tax (minor unit).
	FieldTaxTotal = "tax_total"
	// FieldShippingTotal is the total shipping amount (minor unit).
	FieldShippingTotal = "shipping_total"
	// FieldTotal is the amount to be paid (minor unit).
	FieldTotal = "total"
	// FieldPlacedAt is the moment the order was placed.
	FieldPlacedAt = "placed_at"
	// FieldCompletedAt is the moment the order was completed; nil when it is
	// not completed.
	FieldCompletedAt = "completed_at"
	// FieldCanceledAt is the moment the order was canceled; nil when it is not
	// canceled.
	FieldCanceledAt = "canceled_at"
	// FieldArchivedAt is the moment the order was archived; nil when it is not
	// archived, and ALSO nil on an order archived before the column existed
	// (migration 000007).
	//
	// It is offered next to the two stamps above rather than left to the
	// module's own reads, because a read layer that can answer "when was this
	// completed" and not "when was it archived" makes the gap look like a
	// property of archiving instead of a missing field.
	FieldArchivedAt = "archived_at"
	// FieldCreatedAt is the creation time.
	FieldCreatedAt = "created_at"
	// FieldUpdatedAt is the time of the last update.
	FieldUpdatedAt = "updated_at"
	// FieldAddsToOrderID is the order this one adds to; empty when it adds to
	// nothing (ADR 0192). It is also a filter: the additions of one order.
	FieldAddsToOrderID = "adds_to_order_id"
)

// The fields read from the order's addresses rather than from its row
// (ADR 0196). They cost one batch read of the addresses for the whole page,
// made only when one of them is asked for.
const (
	// FieldShippingAddress is the current shipping address as a map of the
	// address's non-empty fields, by the order API's names; nil when the order
	// recorded none.
	FieldShippingAddress = "shipping_address"
	// FieldBillingAddress is the current billing address, in the same shape.
	FieldBillingAddress = "billing_address"
	// FieldShippingAddressCorrectedAt is the moment of the latest correction
	// of the shipping address (ADR 0195); nil when it was never corrected.
	FieldShippingAddressCorrectedAt = "shipping_address_corrected_at"
)

// The address's column names. They are also its names in the order API and in
// the read layer's address map, which is why one constant serves the
// disclosure, the erasure's declaration and [addressRecord].
const (
	columnFirstName   = "first_name"
	columnLastName    = "last_name"
	columnCompany     = "company"
	columnAddress1    = "address_1"
	columnAddress2    = "address_2"
	columnCity        = "city"
	columnProvince    = "province"
	columnPostalCode  = "postal_code"
	columnCountryCode = "country_code"
	columnPhone       = "phone"
)

// addressFieldGetters read the address fields from one order's address rows,
// the superseded ones included.
var addressFieldGetters = map[string]func(addresses []models.OrderAddress) any{
	FieldShippingAddress: func(addresses []models.OrderAddress) any {
		shipping, _ := splitAddresses(addresses)
		return addressRecord(shipping)
	},
	FieldBillingAddress: func(addresses []models.OrderAddress) any {
		_, billing := splitAddresses(addresses)
		return addressRecord(billing)
	},
	FieldShippingAddressCorrectedAt: func(addresses []models.OrderAddress) any {
		var latest *time.Time
		for i := range addresses {
			closed := addresses[i].SupersededAt
			if addresses[i].Type != models.AddressShipping || closed == nil {
				continue
			}
			if latest == nil || closed.After(*latest) {
				latest = closed
			}
		}
		if latest == nil {
			return nil
		}
		return *latest
	},
}

// addressRecord is one address as the read layer hands it over: the non-empty
// fields by the order API's names, so a reader tells "no company" from an empty
// one the same way the API's JSON does. nil when there is no address.
func addressRecord(address *models.OrderAddress) any {
	if address == nil {
		return nil
	}

	out := map[string]any{}
	for name, value := range map[string]string{
		columnFirstName: address.FirstName, columnLastName: address.LastName,
		columnCompany: address.Company, columnAddress1: address.Address1,
		columnAddress2: address.Address2, columnCity: address.City,
		columnProvince: address.Province, columnPostalCode: address.PostalCode,
		columnCountryCode: address.CountryCode, columnPhone: address.Phone,
	} {
		if value != "" {
			out[name] = value
		}
	}
	if len(address.Metadata) > 0 {
		out["metadata"] = address.Metadata
	}

	return out
}

// orderFieldGetters holds the extractors of the offered fields.
//
// The field set being defined in a single place makes it impossible for the
// validation and the production to diverge: if a field that is not here is
// requested errors.Invalid is returned (ADR 0004), and every field that is here
// can also be produced.
var orderFieldGetters = map[string]func(order models.Order) any{
	FieldID:            func(o models.Order) any { return o.ID },
	FieldDisplayID:     func(o models.Order) any { return o.DisplayID },
	FieldStatus:        func(o models.Order) any { return o.Status.String() },
	FieldRegionID:      func(o models.Order) any { return o.RegionID },
	FieldCustomerID:    func(o models.Order) any { return o.CustomerID },
	FieldEmail:         func(o models.Order) any { return o.Email },
	FieldCurrencyCode:  func(o models.Order) any { return o.CurrencyCode },
	FieldCartID:        func(o models.Order) any { return o.CartID },
	FieldSubtotal:      func(o models.Order) any { return o.Subtotal },
	FieldDiscountTotal: func(o models.Order) any { return o.DiscountTotal },
	FieldTaxTotal:      func(o models.Order) any { return o.TaxTotal },
	FieldShippingTotal: func(o models.Order) any { return o.ShippingTotal },
	FieldTotal:         func(o models.Order) any { return o.Total },
	FieldPlacedAt:      func(o models.Order) any { return o.PlacedAt },
	FieldCompletedAt: func(o models.Order) any {
		if o.CompletedAt == nil {
			return nil
		}
		return *o.CompletedAt
	},
	FieldCanceledAt: func(o models.Order) any {
		if o.CanceledAt == nil {
			return nil
		}
		return *o.CanceledAt
	},
	FieldArchivedAt: func(o models.Order) any {
		if o.ArchivedAt == nil {
			return nil
		}
		return *o.ArchivedAt
	},
	FieldCreatedAt:     func(o models.Order) any { return o.CreatedAt },
	FieldUpdatedAt:     func(o models.Order) any { return o.UpdatedAt },
	FieldAddsToOrderID: func(o models.Order) any { return o.AddsToOrderID },
}

// QueryProvider is the read surface the order module opens to the Query layer.
//
// It is registered in the container under the name "order.query"; Query
// resolves it BY NAME (ADR 0004). The provider DOES NOT OFFER THE LINES: the
// lines of an order are an unpaginated set of variable length per order, and
// embedding them inside a Record would not obey Query's join key contract (a
// single "id" field). The lines are read with [Service.GetOrder].
type QueryProvider struct {
	svc *Service
}

// QueryProvider satisfying the core contract is verified at compile time; a
// signature drift does not survive until run time.
var _ query.Provider = (*QueryProvider)(nil)

// NewQueryProvider produces a provider that runs on the given service.
func NewQueryProvider(svc *Service) *QueryProvider {
	return &QueryProvider{svc: svc}
}

// Entity returns the entity name the provider offers.
func (p *QueryProvider) Entity() string {
	return EntityName
}

// List returns the root records.
//
// Supported filters: "id" (string or string slice), "customer_id" (string),
// "region_id" (string), "status" (string) and "adds_to_order_id" (string).
// Any other filter or an unrecognized field is rejected with errors.Invalid
// (ADR 0004).
//
// # Why "id" is a filter and not only a batch fetch
//
// [QueryProvider.FetchByIDs] already reads orders by identifier, but it is the
// EXPANSION path: the read layer calls it when an order hangs off another
// record's link. A caller holding an order id and wanting that one order has to
// go through a root query, and without this filter it could not — the product
// provider offers it and this one did not, which is an inconsistency a caller
// finds by getting a 422.
//
// It was found exactly that way: the admin panel's order page asked for one
// order by id and the provider refused the filter.
//
// The limit is CLAMPED to [MaxLimit]; see [providerLimit].
func (p *QueryProvider) List(ctx context.Context, opts query.ListOptions) ([]query.Record, error) {
	if err := validateFields(opts.Fields); err != nil {
		return nil, err
	}

	// An id filter short-circuits the listing: the answer is the batch read,
	// which is the same records the listing would produce and one query rather
	// than a filtered scan.
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

	in := ListOrdersInput{
		Page: Page{Limit: providerLimit(opts.Limit), Offset: int64(opts.Offset)},
	}
	// The filters are walked IN NAME ORDER: map order is random, and if more
	// than one filter were invalid at once, which error is returned would be
	// random.
	for _, name := range slices.Sorted(maps.Keys(opts.Filters)) {
		value := opts.Filters[name]
		switch name {
		case FieldCustomerID:
			id, ok := value.(string)
			if !ok {
				return nil, errors.Invalid(CodeInvalidInput,
					"filter %q has to be text, %T given", name, value)
			}
			in.CustomerID = &id
		case FieldRegionID:
			id, ok := value.(string)
			if !ok {
				return nil, errors.Invalid(CodeInvalidInput,
					"filter %q has to be text, %T given", name, value)
			}
			in.RegionID = &id
		case FieldStatus:
			raw, ok := value.(string)
			if !ok {
				return nil, errors.Invalid(CodeInvalidInput,
					"filter %q has to be text, %T given", name, value)
			}
			status := models.OrderStatus(raw)
			in.Status = &status
		case FieldAddsToOrderID:
			id, ok := value.(string)
			if !ok {
				return nil, errors.Invalid(CodeInvalidInput,
					"filter %q has to be text, %T given", name, value)
			}
			in.AddsToOrderID = &id
		default:
			return nil, errors.Invalid(CodeInvalidInput,
				"%q entity'si %q filtresini desteklemiyor", EntityName, name)
		}
	}

	result, err := p.svc.ListOrders(ctx, in)
	if err != nil {
		return nil, err
	}
	return p.records(ctx, result.Items, opts.Fields)
}

// idFilter turns an id filter value into a list of identifiers.
//
// A single string is accepted as well as a slice: a caller reading one record
// should not have to wrap its identifier, and the []any form is what a filter
// arriving as JSON looks like.
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

	orders, err := p.svc.ListOrdersByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return p.records(ctx, orders, fields)
}

// records turns the orders into records with the requested fields.
//
// The address fields are read in ONE batch for the whole page, and only when
// one of them is asked for: a listing that wants totals does not pay for the
// addresses.
func (p *QueryProvider) records(
	ctx context.Context, orders []models.Order, fields []string,
) ([]query.Record, error) {
	selected := fields
	if len(selected) == 0 {
		selected = append(slices.Sorted(maps.Keys(orderFieldGetters)),
			slices.Sorted(maps.Keys(addressFieldGetters))...)
	}

	var addresses map[string][]models.OrderAddress
	if slices.ContainsFunc(selected, func(name string) bool { return addressFieldGetters[name] != nil }) {
		ids := make([]string, 0, len(orders))
		for i := range orders {
			ids = append(ids, orders[i].ID)
		}
		read, err := p.svc.store.OrderAddressesByOrderIDs(ctx, ids)
		if err != nil {
			return nil, err
		}
		addresses = read
	}

	out := make([]query.Record, 0, len(orders))
	// The loop is walked by index: the order struct is large and copying it by
	// value would carry a few hundred bytes for nothing on every turn.
	for i := range orders {
		record := make(query.Record, len(selected))
		for _, name := range selected {
			if getter, ok := addressFieldGetters[name]; ok {
				record[name] = getter(addresses[orders[i].ID])
				continue
			}
			record[name] = orderFieldGetters[name](orders[i])
		}
		out = append(out, record)
	}
	return out, nil
}

// providerLimit clamps the core's limit value to the provider's page ceiling.
//
// In the core contract ([query.ListOptions]) 0 means "UNLIMITED"; this provider
// does not offer unlimited listing, because an unlimited root query would pull
// the whole orders table into memory. An unlimited request is therefore turned
// into [MaxLimit] — NOT into [DefaultLimit]: the caller explicitly said "I want
// all of them" and must get the most it can. A meaningless negative value is
// put in the same basket: on this path the limit is not a client input but a
// number coming from another module's query definition, and rejecting it would
// bring the whole read down.
func providerLimit(limit int) int64 {
	if limit <= 0 || int64(limit) > MaxLimit {
		return MaxLimit
	}
	return int64(limit)
}

// validateFields verifies that every one of the requested fields is offered.
func validateFields(fields []string) error {
	for _, name := range fields {
		_, onRow := orderFieldGetters[name]
		_, onAddress := addressFieldGetters[name]
		if !onRow && !onAddress {
			return errors.Invalid(CodeInvalidInput,
				"entity %q does not offer the field %q", EntityName, name)
		}
	}
	return nil
}
