package service

import (
	"context"
	"maps"
	"slices"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// The field names the Query provider offers.
const (
	// FieldID is the record's identifier; Query does the joining over this
	// field.
	FieldID = query.IDField
	// FieldReference is the cart/order identifier the collection is attached
	// to.
	FieldReference = "reference"
	// FieldAmount is the total amount that must be collected (minor unit).
	FieldAmount = "amount"
	// FieldCurrencyCode is the ISO 4217 currency code.
	FieldCurrencyCode = "currency_code"
	// FieldStatus is the collection's derived status.
	FieldStatus = "status"
	// FieldAuthorizedAmount is the total amount put on hold.
	FieldAuthorizedAmount = "authorized_amount"
	// FieldCapturedAmount is the total captured amount.
	FieldCapturedAmount = "captured_amount"
	// FieldRefundedAmount is the total refunded amount.
	FieldRefundedAmount = "refunded_amount"
	// FieldCreatedAt is the creation time.
	FieldCreatedAt = "created_at"
	// FieldUpdatedAt is the last update time.
	FieldUpdatedAt = "updated_at"
)

// collectionFieldGetters are the extractors of the offered fields.
//
// The field set being defined in a single place makes it impossible for
// validation and production to drift apart: if a field that is not here is
// asked for, errors.Invalid is returned (ADR 0004), and every field that is
// here can also be produced.
//
// Metadata is DELIBERATELY not offered: it is free-form data put there by the
// caller, and carrying a field that has no schema on a cross-module read
// surface would make the records Query joins unpredictable.
var collectionFieldGetters = map[string]func(col models.PaymentCollection) any{
	FieldID:               func(col models.PaymentCollection) any { return col.ID },
	FieldReference:        func(col models.PaymentCollection) any { return col.Reference },
	FieldAmount:           func(col models.PaymentCollection) any { return col.Amount },
	FieldCurrencyCode:     func(col models.PaymentCollection) any { return col.CurrencyCode },
	FieldStatus:           func(col models.PaymentCollection) any { return col.Status.String() },
	FieldAuthorizedAmount: func(col models.PaymentCollection) any { return col.AuthorizedAmount },
	FieldCapturedAmount:   func(col models.PaymentCollection) any { return col.CapturedAmount },
	FieldRefundedAmount:   func(col models.PaymentCollection) any { return col.RefundedAmount },
	FieldCreatedAt:        func(col models.PaymentCollection) any { return col.CreatedAt },
	FieldUpdatedAt:        func(col models.PaymentCollection) any { return col.UpdatedAt },
}

// QueryProvider is the read surface the payment module opens to the Query
// layer.
//
// It is registered in the container under the name "payment_collection.query";
// Query resolves it BY NAME (ADR 0004). An order listing sees the order's
// payment status through this provider and through the "order_payment" link.
type QueryProvider struct {
	svc *Service
}

// QueryProvider satisfying the core contract is verified at compile time; a
// signature drift does not get left to run time.
var _ query.Provider = (*QueryProvider)(nil)

// NewQueryProvider produces a provider that works over the given service.
func NewQueryProvider(svc *Service) *QueryProvider {
	return &QueryProvider{svc: svc}
}

// Entity returns the entity name the provider offers.
func (p *QueryProvider) Entity() string { return EntityName }

// List returns the root records.
//
// The supported filters: "reference" and "status" (both text). Any other
// filter or an unrecognized field is rejected with errors.Invalid (ADR 0004).
//
// The limit is CLAMPED to [MaxLimit]; see [providerLimit]. The clamping is
// silent and returns no error, but the result means that the page size cannot
// be exceeded: the caller must not assume it got all of the records, and
// should read a response that returns [MaxLimit] records as "there may be
// more".
func (p *QueryProvider) List(ctx context.Context, opts query.ListOptions) ([]query.Record, error) {
	if err := validateFields(opts.Fields); err != nil {
		return nil, err
	}

	in := ListCollectionsInput{
		Page: Page{Limit: providerLimit(opts.Limit), Offset: int64(opts.Offset)},
	}
	// The filters are walked sorted: going over the map would leave it random
	// which error is returned when more than one filter is invalid at once.
	for _, name := range slices.Sorted(maps.Keys(opts.Filters)) {
		value := opts.Filters[name]
		text, ok := value.(string)
		if !ok {
			return nil, errors.Invalid(CodeInvalidInput,
				"the %q filter must be text, %T given", name, value)
		}
		switch name {
		case FieldReference:
			in.Reference = &text
		case FieldStatus:
			in.Status = &text
		default:
			return nil, errors.Invalid(CodeInvalidInput,
				"the %q entity does not support the %q filter", EntityName, name)
		}
	}

	collections, _, err := p.svc.ListPaymentCollections(ctx, in)
	if err != nil {
		return nil, err
	}
	return records(collections, opts.Fields), nil
}

// FetchByIDs returns the records of the given identifiers as a BATCH.
// No record is returned for an identifier that is not found; this is not an
// error.
func (p *QueryProvider) FetchByIDs(ctx context.Context, ids, fields []string) ([]query.Record, error) {
	if err := validateFields(fields); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []query.Record{}, nil
	}

	collections, err := p.svc.ListPaymentCollectionsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return records(collections, fields), nil
}

// records turns the collections into records with the requested fields.
// If fields is empty, ALL of the offered fields are returned.
func records(collections []models.PaymentCollection, fields []string) []query.Record {
	selected := fields
	if len(selected) == 0 {
		selected = slices.Sorted(maps.Keys(collectionFieldGetters))
	}

	out := make([]query.Record, 0, len(collections))
	// The slice is walked BY INDEX: walking it by value would copy the whole
	// collection struct on every iteration and the price would grow as the
	// record count rises.
	for i := range collections {
		record := make(query.Record, len(selected))
		for _, name := range selected {
			record[name] = collectionFieldGetters[name](collections[i])
		}
		out = append(out, record)
	}
	return out
}

// providerLimit clamps the core's limit value to the provider's page ceiling.
//
// In the core contract ([query.ListOptions]) 0 means "UNLIMITED"; this
// provider does not offer unlimited listing, because an unlimited root query
// would pull the whole collection table into memory. An unlimited request is
// therefore turned into [MaxLimit] — NOT into [DefaultLimit]: the caller has
// explicitly said "I want all of them" and should get the most it can get. A
// meaningless negative value is put in the same basket: on this path the limit
// is not a client input but a number coming from another module's query
// definition, and rejecting it would bring the whole read down.
func providerLimit(limit int) int64 {
	if limit <= 0 || int64(limit) > MaxLimit {
		return MaxLimit
	}
	return int64(limit)
}

// validateFields verifies that all of the requested fields are offered.
func validateFields(fields []string) error {
	for _, name := range fields {
		if _, ok := collectionFieldGetters[name]; !ok {
			return errors.Invalid(CodeInvalidInput,
				"the %q entity does not offer the %q field", EntityName, name)
		}
	}
	return nil
}
