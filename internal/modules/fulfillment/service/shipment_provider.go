package service

import (
	"context"
	"maps"
	"slices"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// The fields the shipment offers the Query layer.
//
// They are the ones an order's timeline asks for: what the parcel is, where it
// is, and WHEN each transition happened. The provider's own data bag and its
// idempotency key are deliberately absent — the first is the carrier's raw
// response and belongs to whoever speaks that carrier's protocol, and the
// second is a guard, not a fact about the shipment.
const (
	// FieldShipmentID is the shipment's identifier.
	FieldShipmentID = "id"
	// FieldReference is the identifier of the caller's own record.
	//
	// It is offered because it is on the row, NOT as the association: the
	// module never validates it, and the binding that can be trusted is the
	// "order_fulfillment" link (ADR 0005). A consumer that reads this field as
	// "the order" is reading a convention.
	FieldReference = "reference"
	// FieldShipmentOptionID is the shipping option the shipment uses.
	FieldShipmentOptionID = "shipping_option_id"
	// FieldShipmentProviderID is the provider that carries it.
	FieldShipmentProviderID = "provider_id"
	// FieldExternalID is the shipment's identifier on the provider's side.
	FieldExternalID = "external_id"
	// FieldShipmentStatus is the shipment's current status.
	FieldShipmentStatus = "status"
	// FieldTrackingNumber and FieldTrackingURL are what a shopper is given.
	FieldTrackingNumber = "tracking_number"
	FieldTrackingURL    = "tracking_url"
	// FieldShippedAt, FieldDeliveredAt and FieldCanceledAt are the moments of
	// the transitions; they are null until the transition happens. These three
	// are the timeline.
	FieldShippedAt   = "shipped_at"
	FieldDeliveredAt = "delivered_at"
	FieldCanceledAt  = "canceled_at"
	// FieldShipmentCreatedAt is when the shipment was opened.
	FieldShipmentCreatedAt = "created_at"
)

// shipmentFieldGetters maps a field name to the value it reads off the model.
//
// The map IS the contract: a field that is not in it is refused rather than
// answered with a zero value, because a consumer cannot tell a zero it asked
// for from a zero that means "this field does not exist" (ADR 0004).
var shipmentFieldGetters = map[string]func(models.Fulfillment) any{
	FieldShipmentID:         func(f models.Fulfillment) any { return f.ID },
	FieldReference:          func(f models.Fulfillment) any { return f.Reference },
	FieldShipmentOptionID:   func(f models.Fulfillment) any { return f.ShippingOptionID },
	FieldShipmentProviderID: func(f models.Fulfillment) any { return f.ProviderID },
	FieldExternalID:         func(f models.Fulfillment) any { return f.ExternalID },
	FieldShipmentStatus:     func(f models.Fulfillment) any { return string(f.Status) },
	FieldTrackingNumber:     func(f models.Fulfillment) any { return f.TrackingNumber },
	FieldTrackingURL:        func(f models.Fulfillment) any { return f.TrackingURL },
	FieldShippedAt:          func(f models.Fulfillment) any { return f.ShippedAt },
	FieldDeliveredAt:        func(f models.Fulfillment) any { return f.DeliveredAt },
	FieldCanceledAt:         func(f models.Fulfillment) any { return f.CanceledAt },
	FieldShipmentCreatedAt:  func(f models.Fulfillment) any { return f.CreatedAt },
}

// ShipmentProviderName is the shipment provider's name in the container.
const ShipmentProviderName = FulfillmentEntity + query.ProviderSuffix

// ShipmentQueryProvider offers the SHIPMENT to the cross-module read layer.
//
// # Why a second provider in one module
//
// This module already offers "shipping_option"; a module may offer several
// entities and the Query layer looks each one up by its own name. The shipment
// needed its own because the "order_fulfillment" link points at it: a link
// whose far side has no provider can be READ through the link service and
// cannot be EXPANDED through a Query request, and expansion is what the order
// timeline is made of.
type ShipmentQueryProvider struct {
	svc *Service
}

// That the provider satisfies the core contract is verified at compile time.
var _ query.Provider = (*ShipmentQueryProvider)(nil)

// NewShipmentQueryProvider produces a provider running on the given service.
func NewShipmentQueryProvider(svc *Service) *ShipmentQueryProvider {
	return &ShipmentQueryProvider{svc: svc}
}

// Entity returns the entity name the provider offers.
func (p *ShipmentQueryProvider) Entity() string { return FulfillmentEntity }

// List returns the root records.
//
// Supported filters: "reference" and "status" (both text). Any other filter or
// an unrecognized field is rejected with errors.Invalid (ADR 0004).
//
// The limit is CLAMPED to [MaxLimit], silently, for the reason
// [providerLimit] gives: an unlimited root query would pull the whole shipment
// table into memory.
func (p *ShipmentQueryProvider) List(
	ctx context.Context, opts query.ListOptions,
) ([]query.Record, error) {
	if err := validateShipmentFields(opts.Fields); err != nil {
		return nil, err
	}

	in := ListFulfillmentsInput{
		Page: Page{Limit: providerLimit(opts.Limit), Offset: int64(opts.Offset)},
	}
	// The filters are walked in order: iterating over the map would leave it
	// random which error is returned when several filters are invalid at once.
	for _, name := range slices.Sorted(maps.Keys(opts.Filters)) {
		value := opts.Filters[name]
		text, ok := value.(string)
		if !ok {
			return nil, errors.Invalid(CodeInvalidInput,
				"filter %q has to be text, %T given", name, value)
		}
		switch name {
		case FieldReference:
			in.Reference = &text
		case FieldShipmentStatus:
			in.Status = &text
		default:
			return nil, errors.Invalid(CodeInvalidInput,
				"entity %q does not support the filter %q", FulfillmentEntity, name)
		}
	}

	shipments, _, err := p.svc.ListFulfillments(ctx, in)
	if err != nil {
		return nil, err
	}

	return shipmentRecords(shipments, opts.Fields), nil
}

// FetchByIDs returns the records of the given identifiers as a BATCH.
//
// No record is returned for an identifier that is not found; that is not an
// error. This is the call an expansion makes, and making it one query per id
// would put back the N+1 the read layer exists to prevent.
func (p *ShipmentQueryProvider) FetchByIDs(
	ctx context.Context, ids, fields []string,
) ([]query.Record, error) {
	if err := validateShipmentFields(fields); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []query.Record{}, nil
	}

	shipments, err := p.svc.ListFulfillmentsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}

	return shipmentRecords(shipments, fields), nil
}

// shipmentRecords turns the shipments into records with the requested fields.
// If fields is empty, ALL offered fields are returned.
func shipmentRecords(shipments []models.Fulfillment, fields []string) []query.Record {
	selected := fields
	if len(selected) == 0 {
		selected = slices.Sorted(maps.Keys(shipmentFieldGetters))
	}

	out := make([]query.Record, 0, len(shipments))
	// The slice is walked BY INDEX: walking by value would copy the whole
	// struct on every iteration and the cost would grow with the record count.
	for i := range shipments {
		record := make(query.Record, len(selected))
		for _, name := range selected {
			record[name] = shipmentFieldGetters[name](shipments[i])
		}
		out = append(out, record)
	}

	return out
}

// validateShipmentFields verifies that all the requested fields are offered.
func validateShipmentFields(fields []string) error {
	for _, name := range fields {
		if _, ok := shipmentFieldGetters[name]; !ok {
			return errors.Invalid(CodeInvalidInput,
				"entity %q does not offer the field %q", FulfillmentEntity, name)
		}
	}

	return nil
}
