package provider

import (
	"context"
	"encoding/json"
	"time"
)

// FulfillmentStatus is a shipment's status on the provider side.
type FulfillmentStatus string

// The shipment statuses.
const (
	// FulfillmentPending means the shipment was created but not collected yet.
	FulfillmentPending FulfillmentStatus = "pending"
	// FulfillmentShipped means the carrier has collected the shipment.
	FulfillmentShipped FulfillmentStatus = "shipped"
	// FulfillmentDelivered means the shipment reached the recipient.
	FulfillmentDelivered FulfillmentStatus = "delivered"
	// FulfillmentCanceled means the shipment was canceled.
	FulfillmentCanceled FulfillmentStatus = "canceled"
)

// ShippingQuote is a shipping option's price for a particular cart and address.
type ShippingQuote struct {
	// OptionID is the shipping option the price belongs to.
	OptionID string
	// Amount is the shipping charge, an INTEGER in minor units (plan
	// Section 8).
	Amount int64
	// CurrencyCode is the ISO 4217 currency code.
	CurrencyCode string
	// Data is the raw data returned by the provider; the core does not
	// interpret it.
	Data json.RawMessage
}

// QuoteInput is the input of a price query.
type QuoteInput struct {
	// OptionID is the shipping option being priced.
	OptionID string
	// CurrencyCode is the expected currency.
	CurrencyCode string
	// CountryCode is the delivery country (ISO 3166-1 alpha-2).
	CountryCode string
	// TotalWeight is the shipment's total weight in grams; zero when unknown.
	TotalWeight int64
	// ItemCount is the number of items in the shipment.
	ItemCount int64
	// Data is provider-specific free-form data.
	Data map[string]any
}

// CreateFulfillmentInput is the input of creating a shipment.
type CreateFulfillmentInput struct {
	// Reference is the identity the caller gave its own record (e.g. the
	// fulfillment's id). The provider stores it on its side; it is the field
	// that matches the two systems during reconciliation.
	Reference string
	// OptionID is the shipping option to use.
	OptionID string
	// IdempotencyKey stops the same shipment from being created twice.
	//
	// A saga may retry a step (plan Section 2.6); without the key a retry
	// would mean a SECOND SHIPPING LABEL.
	IdempotencyKey string
	// Data is provider-specific free-form data (the address, the item list and
	// so on).
	Data map[string]any
}

// Fulfillment is a shipment created at the provider.
type Fulfillment struct {
	// ID is the shipment's identity on the provider side.
	ID string
	// Status is the shipment's current status.
	Status FulfillmentStatus
	// TrackingNumber and TrackingURL are the tracking details; empty when the
	// provider gives none.
	TrackingNumber string
	TrackingURL    string
	// Data is the raw data returned by the provider.
	Data json.RawMessage
}

// TrackingUpdate is a provider's OWN view of where a shipment is.
//
// Every field is the provider's answer and none of it is this repository's
// record: what the module holds sits beside it, and the two are reported apart so
// that a label printed with one number and recorded with another is visible
// (ADR 0149).
type TrackingUpdate struct {
	// Status is the shipment's status as the provider has it.
	Status FulfillmentStatus
	// TrackingNumber and TrackingURL are the carrier's, which may differ from
	// the ones an operator typed into the module.
	TrackingNumber string
	TrackingURL    string
	// Detail is the carrier's own words about the last movement, free-form and
	// often empty ("handed to courier", "held at depot").
	//
	// It is NOT parsed and NOT mapped onto Status: a carrier's vocabulary is its
	// own and a mapping table maintained here would be wrong in a way nobody
	// could see. Status is the neutral answer; this is the sentence a human
	// reads beside it.
	Detail string
	// MovedAt is when the carrier last moved the parcel (UTC), or the zero time
	// when the provider does not say.
	//
	// A zero value means "not answered" rather than "the epoch": a provider that
	// reports a status without a moment is ordinary, and inventing `now` would
	// turn a silence into a movement that never happened.
	MovedAt time.Time
}

// ShipmentTracker is the OPTIONAL capability of asking a provider where a
// shipment is.
//
// # Why optional rather than a method on FulfillmentProvider
//
// [SessionInspector]'s reason, applied to parcels: adding a method to
// [FulfillmentProvider] would make every provider change so that one of them can
// gain a capability, and it would force a provider that genuinely cannot answer
// to implement something that lies.
//
// A provider that does not implement this is not broken; nothing can be asked of
// it, and whatever asks must SAY SO. "The carrier says it is still at the depot"
// and "nobody could ask" must never look the same.
//
// # It is a READ
//
// Track must not create a label, must not change the provider's state and must be
// safe to call repeatedly — a status page can refresh. What is done with what it
// reveals is a decision for a human.
//
// # Why the module's own status is not overwritten with the answer
//
// Because which side is authoritative depends on the provider. A real carrier
// knows where the parcel is and the module does not; the provider that ships in
// the box is the SHOP itself, so there the module's status — an operator marking
// a parcel handed over — is the true one and the provider's row is a stub nobody
// moves. A write here would pick a winner for both cases and be wrong in one.
type ShipmentTracker interface {
	FulfillmentProvider

	// Track returns the provider's view of the shipment, addressed by the
	// identifier the provider itself gave it ([Fulfillment.ID], stored locally as
	// the fulfillment's external id).
	//
	// A shipment the provider has never heard of returns a NotFound error rather
	// than a zero update: "the provider has no such shipment" and "the provider
	// says it has not moved" are different facts, and treating the first as the
	// second would hide a label opened against the wrong account.
	Track(ctx context.Context, shipmentID string) (TrackingUpdate, error)
}

// FulfillmentProvider is the contract a shipping provider offers the core
// (plan Section 5.6).
//
// # Idempotency and the saga
//
// The same rule as [PaymentProvider]'s applies: the methods are called from
// saga steps and a saga MAY RETRY a step.
//   - Create, called a second time with the same IdempotencyKey, does not
//     create a NEW shipment; it returns the existing one.
//   - Cancel is the saga's compensation and must be IDEMPOTENT: a shipment
//     canceled twice does NOT fail on the second call.
//
// # A price query has no side effects
//
// Quote creates nothing and may be called again; because it can be called many
// times while a cart total is computed, it has to be cheap.
type FulfillmentProvider interface {
	Provider

	// Quote returns the shipping charge for the given option. It has NO SIDE
	// EFFECTS.
	Quote(ctx context.Context, in QuoteInput) (ShippingQuote, error)

	// Create creates a shipment at the provider.
	Create(ctx context.Context, in CreateFulfillmentInput) (Fulfillment, error)

	// Cancel cancels the shipment. It is the saga's compensation and must be
	// IDEMPOTENT.
	Cancel(ctx context.Context, fulfillmentID string) error
}
