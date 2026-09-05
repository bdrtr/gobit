package provider

import (
	"context"
	"encoding/json"
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
