// Package service is the business logic of the notification module.
//
// The module's responsibility in one sentence: when an event has to be
// reported to the customer, have the SELECTED provider do it and write the
// attempt into a log. This module does not produce the text itself; the
// provider resolves the template (see [coreprovider.Notification] in the
// core).
//
// # Why there is a delivery log
//
// An e-mail that has been sent cannot be taken back, so there is no
// compensation path in this module either. The only protection left is
// PREVENTING A REPEAT, and that is possible only through a durable record: the
// (template, reference) pair is UNIQUE in the log, and the record is opened
// BEFORE the provider is reached. The record's second job is diagnosis — the
// answer to "did the confirmation go out to the customer" exists nowhere else.
//
// # Module isolation
//
// This module knows no other module (Principle 2.1/2.4, ADR 0001). The order's
// contact information is read through the NARROW interface defined in this
// package ([OrderContactReader]) and from the "order.interop" surface resolved
// from the container BY NAME; the data carried is JSON (ADR 0006).
// [models.Delivery.Reference] is an order identifier; it is stored as free
// text and its existence is not validated here.
package service

import (
	"context"
	"log/slog"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/notification/models"
)

// Error codes. Clients may branch on these; the messages can change, the codes
// do not.
const (
	// CodeInvalidInput reports that the input did not pass validation.
	CodeInvalidInput = "notification_invalid_input"
	// CodeProviderNotFound reports that the selected provider is not
	// registered.
	CodeProviderNotFound = "notification_provider_not_found"
	// CodeProviderExists reports that a second provider was about to be
	// registered under the same identifier.
	CodeProviderExists = "notification_provider_already_registered"
	// CodeSendFailed reports that the provider refused the send.
	CodeSendFailed = "notification_send_failed"
	// CodeEventInvalid reports that the event payload does not obey the
	// contract.
	CodeEventInvalid = "notification_event_payload_invalid"
	// CodeContactUnavailable reports that the order reading surface could not
	// be reached.
	CodeContactUnavailable = "notification_order_contact_unavailable"
	// CodeContactInvalid reports that the order surface's response could not
	// be decoded.
	CodeContactInvalid = "notification_order_contact_invalid"
	// CodeNotReady reports that the service was constructed with a missing
	// dependency.
	CodeNotReady = "notification_service_not_ready"
)

// Pagination bounds (plan Section 8: limit/offset).
const (
	// DefaultLimit is the page size applied when no limit is given.
	DefaultLimit int64 = 50
	// MaxLimit is the largest page size that can be asked for in a single
	// request.
	MaxLimit int64 = 100
)

// Store is the persistence surface the service needs.
//
// The interface is defined on the CONSUMING side, that is, here (the pattern
// of ADR 0001). The service does NOT import the repository package; the
// concrete store satisfies these signatures structurally and the wiring is
// established in module.go. Unit tests can therefore be written without a real
// database, against a fake store a few lines long.
type Store interface {
	// ClaimDelivery writes the record only if the (template, reference) pair
	// has not been used yet. The second return value is whether the row was
	// written; a collision is NOT AN ERROR.
	ClaimDelivery(ctx context.Context, d models.Delivery) (models.Delivery, bool, error)
	// FinishDelivery writes the outcome of the send attempt.
	FinishDelivery(
		ctx context.Context,
		id string,
		status models.DeliveryStatus,
		failure string,
	) (models.Delivery, error)
	// GetDelivery returns the record by its identifier; NotFound when absent.
	GetDelivery(ctx context.Context, id string) (models.Delivery, error)
	// ListDeliveries filters and pages the records; the second value is the
	// count of ALL rows matching the filter.
	ListDeliveries(ctx context.Context, filter models.DeliveryFilter) ([]models.Delivery, int64, error)
}

// Options holds the construction dependencies of the service.
type Options struct {
	// Store is the persistence surface; it is required.
	Store Store
	// Providers holds the registered notification providers; it is required.
	Providers *ProviderRegistry
	// ProviderID is the identifier of the provider TO BE USED for sending
	// (NOTIFICATION_PROVIDER); it is required.
	//
	// Whether the provider is registered is not validated HERE and cannot be:
	// the providers brought in by plugins are registered AFTER the modules
	// come up (see the two phases of coreplugin.Registry). The check that runs
	// once the whole setup is finished lives at the composition root
	// (internal/app).
	ProviderID string
	// Contacts is the surface the order contact information is read from; it
	// is required.
	Contacts OrderContactReader
	// Logger, when given as nil, makes the logs be discarded.
	Logger *slog.Logger
}

// Service is the outward-facing service of the notification module.
// It is safe for concurrent use.
type Service struct {
	store      Store
	providers  *ProviderRegistry
	providerID string
	contacts   OrderContactReader
	log        *slog.Logger
}

// New produces a service with the given dependencies.
//
// A missing dependency is a construction error and is returned EXPLICITLY: a
// service built with a nil store would panic on the first event, and the fault
// would surface long after construction — at the moment the first order is
// placed.
func New(opts Options) (*Service, error) {
	if opts.Store == nil {
		return nil, errors.Internal(CodeNotReady,
			"the notification service cannot be constructed without a store")
	}
	if opts.Providers == nil {
		return nil, errors.Internal(CodeNotReady,
			"the notification service cannot be constructed without a provider registry")
	}
	if opts.Contacts == nil {
		return nil, errors.Internal(CodeNotReady,
			"the notification service cannot be constructed without an order reading surface")
	}
	if opts.ProviderID == "" {
		return nil, errors.Internal(CodeNotReady,
			"the notification service cannot be constructed without a provider identifier")
	}

	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{
		store:      opts.Store,
		providers:  opts.Providers,
		providerID: opts.ProviderID,
		contacts:   opts.Contacts,
		log:        log,
	}, nil
}

// ProviderID returns the identifier of the provider used for sending.
func (s *Service) ProviderID() string { return s.providerID }

// ListDeliveriesInput is the input of a delivery log listing.
type ListDeliveriesInput struct {
	// Reference, when given, returns only the records of that order.
	Reference *string
	// Status, when given, returns only the records in that status.
	Status *string
	// Page holds the pagination parameters.
	Page Page
}

// Page holds the pagination parameters of list requests.
type Page struct {
	// Limit is the maximum number of rows to return; when 0, [DefaultLimit]
	// is applied.
	Limit int64
	// Offset is the number of rows to skip.
	Offset int64
}

// normalize validates the pagination parameters and applies the defaults.
func (p Page) normalize() (Page, error) {
	if p.Limit < 0 {
		return Page{}, errors.Invalid(CodeInvalidInput, "the limit cannot be negative: %d", p.Limit)
	}
	if p.Offset < 0 {
		return Page{}, errors.Invalid(CodeInvalidInput, "the offset cannot be negative: %d", p.Offset)
	}
	if p.Limit > MaxLimit {
		return Page{}, errors.Invalid(CodeInvalidInput,
			"the limit can be at most %d: %d", MaxLimit, p.Limit)
	}
	if p.Limit == 0 {
		p.Limit = DefaultLimit
	}
	return p, nil
}

// ListDeliveries returns the delivery log, filtered and paged.
// The second return value is the count of ALL records matching the filter.
//
// An unrecognized status filter is REFUSED with errors.Invalid; returning an
// empty list silently would make "there are no failed notifications at all"
// indistinguishable from "I typed the status name wrong".
func (s *Service) ListDeliveries(
	ctx context.Context,
	in ListDeliveriesInput,
) ([]models.Delivery, int64, error) {
	page, err := in.Page.normalize()
	if err != nil {
		return nil, 0, err
	}
	if in.Status != nil && !models.DeliveryStatus(*in.Status).Valid() {
		return nil, 0, errors.Invalid(CodeInvalidInput,
			"%q is not a recognized delivery status; it has to be %s, %s, %s or %s",
			*in.Status, models.DeliveryPending, models.DeliverySent,
			models.DeliveryFailed, models.DeliverySkipped)
	}

	return s.store.ListDeliveries(ctx, models.DeliveryFilter{
		Reference: in.Reference,
		Status:    in.Status,
		Limit:     page.Limit,
		Offset:    page.Offset,
	})
}

// GetDelivery returns a single delivery record; errors.NotFound when absent.
func (s *Service) GetDelivery(ctx context.Context, id string) (models.Delivery, error) {
	if id == "" {
		return models.Delivery{}, errors.Invalid(CodeInvalidInput,
			"the record identifier cannot be empty")
	}
	return s.store.GetDelivery(ctx, id)
}
