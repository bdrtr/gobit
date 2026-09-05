package service

import (
	"context"
	"maps"
	"slices"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
)

// The field names the Query provider offers.
const (
	// FieldID is the id of the record; Query does the joining over this field.
	FieldID = query.IDField
	// FieldSKU is the stock keeping code.
	FieldSKU = "sku"
	// FieldTitle is the title of the item.
	FieldTitle = "title"
	// FieldDescription is the description of the item.
	FieldDescription = "description"
	// FieldRequiresShipping is whether the item requires shipping.
	FieldRequiresShipping = "requires_shipping"
	// FieldAvailableQuantity is the sellable total across ALL locations.
	// The store listing of product reads the stock from this field.
	FieldAvailableQuantity = "available_quantity"
	// FieldCreatedAt is the creation time.
	FieldCreatedAt = "created_at"
	// FieldUpdatedAt is the time of the last update.
	FieldUpdatedAt = "updated_at"
)

// itemFieldGetters are the extractors of the offered fields.
//
// The field set being defined in a single place makes it impossible for the
// validation and the production to drift apart: asking for a field that is not
// here returns errors.Invalid (ADR 0004), and every field that is here can also
// be produced.
var itemFieldGetters = map[string]func(item models.InventoryItem, available int64) any{
	FieldID:                func(item models.InventoryItem, _ int64) any { return item.ID },
	FieldSKU:               func(item models.InventoryItem, _ int64) any { return item.SKU },
	FieldTitle:             func(item models.InventoryItem, _ int64) any { return item.Title },
	FieldDescription:       func(item models.InventoryItem, _ int64) any { return item.Description },
	FieldRequiresShipping:  func(item models.InventoryItem, _ int64) any { return item.RequiresShipping },
	FieldCreatedAt:         func(item models.InventoryItem, _ int64) any { return item.CreatedAt },
	FieldUpdatedAt:         func(item models.InventoryItem, _ int64) any { return item.UpdatedAt },
	FieldAvailableQuantity: func(_ models.InventoryItem, available int64) any { return available },
}

// QueryProvider is the read surface the inventory module opens to the Query
// layer.
//
// It is registered in the container under the name "inventory_item.query"; Query
// resolves it BY NAME (ADR 0004). Because the records come back together with
// the "available_quantity" field, the store listing of product sees the product
// and its stock in a SINGLE query call.
type QueryProvider struct {
	svc *Service
}

// QueryProvider satisfying the core contract is verified at compile time; a
// signature drift does not survive until run time.
var _ query.Provider = (*QueryProvider)(nil)

// NewQueryProvider produces a provider running on the given service.
func NewQueryProvider(svc *Service) *QueryProvider {
	return &QueryProvider{svc: svc}
}

// Entity returns the entity name the provider offers.
func (p *QueryProvider) Entity() string {
	return EntityName
}

// List returns the root records.
//
// Supported filters: "sku" (string) and "requires_shipping" (bool). Any other
// filter or an unrecognized field is rejected with errors.Invalid (ADR 0004).
//
// The limit is CLAMPED to [MaxLimit]; see [providerLimit]. The clamping is silent
// and returns no error, but the result means the page size cannot be exceeded:
// the caller must not assume that it got all the records, it has to read a
// response that returned [MaxLimit] records as "there may be more".
func (p *QueryProvider) List(ctx context.Context, opts query.ListOptions) ([]query.Record, error) {
	if err := validateFields(opts.Fields); err != nil {
		return nil, err
	}

	in := ListInventoryItemsInput{
		Page: Page{Limit: providerLimit(opts.Limit), Offset: int64(opts.Offset)},
	}
	for _, name := range slices.Sorted(maps.Keys(opts.Filters)) {
		value := opts.Filters[name]
		switch name {
		case FieldSKU:
			sku, ok := value.(string)
			if !ok {
				return nil, errors.Invalid(CodeInvalidInput,
					"filter %q has to be text, %T given", name, value)
			}
			in.SKU = &sku
		case FieldRequiresShipping:
			flag, ok := value.(bool)
			if !ok {
				return nil, errors.Invalid(CodeInvalidInput,
					"filter %q has to be a boolean, %T given", name, value)
			}
			in.RequiresShipping = &flag
		default:
			return nil, errors.Invalid(CodeInvalidInput,
				"%q entity'si %q filtresini desteklemiyor", EntityName, name)
		}
	}

	items, _, err := p.svc.ListInventoryItems(ctx, in)
	if err != nil {
		return nil, err
	}
	return p.records(ctx, items, opts.Fields)
}

// FetchByIDs returns the records of the given ids as a BATCH.
// No record is returned for an id that is not found; that is not an error.
func (p *QueryProvider) FetchByIDs(ctx context.Context, ids, fields []string) ([]query.Record, error) {
	if err := validateFields(fields); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []query.Record{}, nil
	}

	items, err := p.svc.ListInventoryItemsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return p.records(ctx, items, fields)
}

// records turns the items into records with the requested fields.
//
// The sellable quantity is computed ONLY when it is asked for, and while it is
// computed a single query is made for ALL the items; there is no query per
// record (N+1).
func (p *QueryProvider) records(ctx context.Context, items []models.InventoryItem, fields []string) ([]query.Record, error) {
	selected := fields
	if len(selected) == 0 {
		selected = slices.Sorted(maps.Keys(itemFieldGetters))
	}

	available := map[string]int64{}
	if slices.Contains(selected, FieldAvailableQuantity) && len(items) > 0 {
		ids := make([]string, 0, len(items))
		for _, item := range items {
			ids = append(ids, item.ID)
		}
		var err error
		if available, err = p.svc.AvailableQuantities(ctx, ids); err != nil {
			return nil, err
		}
	}

	out := make([]query.Record, 0, len(items))
	for _, item := range items {
		record := make(query.Record, len(selected))
		for _, name := range selected {
			record[name] = itemFieldGetters[name](item, available[item.ID])
		}
		out = append(out, record)
	}
	return out, nil
}

// providerLimit clamps the core's limit value to the provider's page ceiling.
//
// In the core contract ([query.ListOptions]) 0 means "UNLIMITED"; this provider
// does not offer unlimited listing, because an unlimited root query would pull
// the whole item table into memory. An unlimited request is therefore turned into
// [MaxLimit] — NOT into [DefaultLimit]: the caller explicitly said "I want them
// all", and it should get the most it can get. A meaningless negative value is
// put in the same basket: on this path the limit is not a client input but a
// number coming from another module's query definition, and rejecting it would
// drop the whole read.
//
// Exceeding the ceiling IS NOT an error either, it is clamped. In the admin API
// exceeding the same limit is errors.Invalid, because there the number is written
// by the client and the client has to know that the number it wrote was not
// applied; the limit here, on the other hand, comes from another module's query
// definition, and returning no data at all because of a single number would be a
// failure the caller never expected.
func providerLimit(limit int) int64 {
	if limit <= 0 || int64(limit) > MaxLimit {
		return MaxLimit
	}
	return int64(limit)
}

// validateFields verifies that all of the requested fields are offered.
func validateFields(fields []string) error {
	for _, name := range fields {
		if _, ok := itemFieldGetters[name]; !ok {
			return errors.Invalid(CodeInvalidInput,
				"entity %q does not offer the field %q", EntityName, name)
		}
	}
	return nil
}
