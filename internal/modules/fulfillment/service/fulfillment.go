package service

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// This file is THE FULFILLMENT's life cycle: creation, cancellation (saga
// compensation), dispatch and delivery notification.

// FulfillmentItemInput is the input of a single item entering the fulfillment.
type FulfillmentItemInput struct {
	// LineItemID is the identifier of the order line; it is required and IS NOT
	// VALIDATED in this module (Principle 2.2).
	LineItemID string
	// Quantity is the count entering the fulfillment; it has to be positive.
	Quantity int64
}

// CreateFulfillmentInput is the input of a new fulfillment.
type CreateFulfillmentInput struct {
	// Reference is the order's identifier; it is required and IS NOT VALIDATED
	// in this module.
	Reference string
	// ShippingOptionID is the shipping option to be used; it is required.
	ShippingOptionID string
	// IdempotencyKey prevents the same fulfillment from being created twice; it
	// is required.
	IdempotencyKey string
	// Items are the items entering the fulfillment; it may be empty (e.g. the
	// shipping-free fulfillment of a digital product, or a bulk shipment with no
	// item breakdown).
	Items []FulfillmentItemInput
	// Data is the free-form data to be handed to the provider (address, branch
	// and so on).
	Data map[string]any
	// Metadata is the caller's free-form extra data.
	Metadata map[string]any
}

// CreateFulfillment opens a fulfillment at the provider and produces its record.
//
// # Order: the RECORD first, the provider second
//
// The fulfillment row is written BEFORE GOING to the provider, and the Reference
// handed to the provider is that row's identifier. The reason is written in the
// core contract (core/provider): Reference is the field that matches
// the two systems during reconciliation. Had the provider been called first and
// the response been lost, a shipping label would be left behind whose
// counterpart record could not be known.
//
// # Residual risk: an AMBIGUOUS provider error
//
// The order above is complete in the case where the failure is CERTAIN. If the
// provider returns an error the whole transaction is rolled back and no
// fulfillment is left behind (TestProviderErrorLeavesNoFulfillment pins this).
// With a real NETWORK provider, however, the failure can be ambiguous: the label
// was printed and the response timed out. In that case the row is rolled back
// but Reference=ful_X remains on the provider's side while ful_X never exists;
// on a retry a NEW ful_Y row is opened with the same key and the provider
// returns the old fulfillment.
//
// The consequence has been accepted explicitly: after an AMBIGUOUS failure the
// reconciliation CANNOT BE BUILT on Reference; the matching has to be done over
// [models.Fulfillment.ExternalID] — the provider's own identifier is the only
// field that stays the same on both sides. The same class of risk is documented
// at the payment module's capture pivot as well
// (internal/workflows/checkout/doc.go). Because the manual provider that ships
// in the box PARTICIPATES in this transaction, no such ambiguity can arise
// there; the risk is visible only with out-of-process providers and the tests
// cannot demonstrate it.
//
// # Idempotency
//
// A second call with the same IdempotencyKey does not open a NEW fulfillment, it
// returns the existing one. The race is resolved in a single statement: the row
// is written with ON CONFLICT DO NOTHING, the losing side WAITS until the
// winner's transaction finishes and then reads the completed row. If the key is
// the same but the reference, the option OR THE ITEM LIST differs,
// errors.Conflict is returned — idempotency means "repeating the same request",
// not "sending a different request with an old key".
//
// Comparing the item list too is a requirement: had only the two fields been
// looked at, a second request arriving with a corrected item breakdown would be
// swallowed silently, the caller would believe it was written and the
// fulfillment would keep its old items. The comparison is a SET: a difference in
// order is not counted as a difference, because the same set of items is the
// same fulfillment no matter which order it is given in.
//
// # The provider call is INSIDE the transaction
//
// The rationale is in the package documentation: had the lock been released
// before the provider, the second caller would read a HALF fulfillment whose
// provider identifier had not been written yet.
func (s *Service) CreateFulfillment(
	ctx context.Context,
	in CreateFulfillmentInput,
) (models.Fulfillment, error) {
	reference := strings.TrimSpace(in.Reference)
	if err := requireText("the reference", reference); err != nil {
		return models.Fulfillment{}, err
	}
	if err := requireID(in.ShippingOptionID, models.ShippingOptionIDPrefix,
		"the shipping option identifier"); err != nil {
		return models.Fulfillment{}, err
	}
	key := strings.TrimSpace(in.IdempotencyKey)
	if err := requireText("the idempotency key", key); err != nil {
		return models.Fulfillment{}, err
	}
	items, err := normalizeItems(in.Items)
	if err != nil {
		return models.Fulfillment{}, err
	}
	optionID := strings.TrimSpace(in.ShippingOptionID)

	var out models.Fulfillment
	err = s.store.WithTx(ctx, func(ctx context.Context) error {
		option, err := s.store.GetShippingOption(ctx, optionID)
		if err != nil {
			return err
		}
		provider, err := s.providers.Get(option.ProviderID)
		if err != nil {
			return err
		}

		created, inserted, err := s.store.InsertFulfillmentIfAbsent(ctx, models.Fulfillment{
			ID:               models.NewFulfillmentID(),
			Reference:        reference,
			ShippingOptionID: option.ID,
			ProviderID:       option.ProviderID,
			Status:           models.StatusPending,
			IdempotencyKey:   key,
			Metadata:         in.Metadata,
		})
		if err != nil {
			return err
		}
		if !inserted {
			existing, err := s.store.FulfillmentByIdempotencyKey(ctx, key)
			if err != nil {
				return err
			}
			if existing.Reference != reference || existing.ShippingOptionID != option.ID {
				return errors.Conflict(CodeIdempotencyMismatch,
					"the same idempotency key was used for a different fulfillment: existing %s/%s, requested %s/%s",
					existing.Reference, existing.ShippingOptionID, reference, option.ID)
			}
			out, err = s.withItems(ctx, existing)
			if err != nil {
				return err
			}
			if saved, wanted := savedItemsKey(out.Items), requestedItemsKey(items); saved != wanted {
				return errors.Conflict(CodeIdempotencyMismatch,
					"the same idempotency key was used with a different item list: existing [%s], requested [%s] (%s)",
					saved, wanted, existing.ID)
			}
			s.log.DebugContext(ctx, "the existing fulfillment was returned", "fulfillment", existing.ID, "key", key)
			return nil
		}

		result, err := provider.Create(ctx, coreprovider.CreateFulfillmentInput{
			Reference:      created.ID,
			OptionID:       option.ID,
			IdempotencyKey: key,
			Data:           mergeProviderData(option.Data, in.Data),
		})
		if err != nil {
			return err
		}
		if strings.TrimSpace(result.ID) == "" {
			return errors.Internal(CodeProviderContract,
				"the provider %q returned an empty fulfillment identifier (%s)", option.ProviderID, created.ID)
		}
		status, err := providerStatus(result.Status, option.ProviderID)
		if err != nil {
			return err
		}

		now := s.now()
		updated, err := s.store.UpdateFulfillmentProviderResult(ctx,
			created.ID, result.ID, status,
			result.TrackingNumber, result.TrackingURL, result.Data,
			stampFor(status, models.StatusShipped, now),
			stampFor(status, models.StatusDelivered, now),
			stampFor(status, models.StatusCanceled, now),
		)
		if err != nil {
			return err
		}

		updated.Items = make([]models.FulfillmentItem, 0, len(items))
		for _, item := range items {
			saved, err := s.store.CreateFulfillmentItem(ctx, models.FulfillmentItem{
				ID:            models.NewFulfillmentItemID(),
				FulfillmentID: updated.ID,
				LineItemID:    item.LineItemID,
				Quantity:      item.Quantity,
			})
			if err != nil {
				return err
			}
			updated.Items = append(updated.Items, saved)
		}

		out = updated
		return nil
	})
	if err != nil {
		return models.Fulfillment{}, err
	}
	return out, nil
}

// CancelFulfillment cancels the fulfillment.
//
// THIS IS THE SAGA COMPENSATION and IT IS IDEMPOTENT: no error is returned for
// an already canceled fulfillment, the provider is NOT called A SECOND TIME and
// no change is made to the record. The compensation being re-runnable is not a
// preference but a working requirement of the saga (plan Section 5.5).
//
// A DELIVERED fulfillment CANNOT be canceled (errors.Conflict). The rationale is
// in the [models.FulfillmentStatus.CancelAction] table: delivery is a physical
// fact that cannot be undone, and its remedy is not cancellation but a RETURN.
// The rule is exactly the same as a captured session in the payment module not
// being cancelable but refundable.
//
// errors.NotFound is returned for an unknown identifier: idempotency does not
// mean "swallow everything silently". A REAL fulfillment canceled twice and an
// identifier that never existed are different situations, and the second is an
// error on the caller's side. Because the record is not deleted (only its status
// changes), the first situation can always be told apart.
func (s *Service) CancelFulfillment(ctx context.Context, id string) error {
	if err := requireID(id, models.FulfillmentIDPrefix, "the fulfillment identifier"); err != nil {
		return err
	}

	return s.store.WithTx(ctx, func(ctx context.Context) error {
		ful, err := s.store.LockFulfillment(ctx, id)
		if err != nil {
			return err
		}

		switch ful.Status.CancelAction() {
		case models.ActionNoop:
			s.log.DebugContext(ctx, "the fulfillment is already canceled", "fulfillment", id)
			return nil
		case models.ActionConflict:
			return errors.Conflict(CodeInvalidTransition,
				"a fulfillment in the %q state cannot be canceled; the return flow has to be used: %s", ful.Status, id)
		case models.ActionProceed:
			// Handled below.
		}

		provider, err := s.providers.Get(ful.ProviderID)
		if err != nil {
			return err
		}
		// If the provider identifier is empty the fulfillment never reached the
		// provider and there is no external record to cancel; only our own row
		// is closed. This situation cannot arise from the rollback of a
		// transaction that failed before the provider responded (the row is
		// rolled back too), but a manual intervention can leave such a row
		// behind.
		if strings.TrimSpace(ful.ExternalID) != "" {
			if err := provider.Cancel(ctx, ful.ExternalID); err != nil {
				return err
			}
		}

		now := s.now()
		_, err = s.store.UpdateFulfillmentStatus(ctx, ful.ID, models.StatusCanceled,
			ful.TrackingNumber, ful.TrackingURL,
			ful.ShippedAt, ful.DeliveredAt, &now)
		return err
	})
}

// MarkShipped marks the fulfillment as shipped.
//
// THE PROVIDER IS NOT CALLED: this method records the fact the carrier REPORTED
// (a webhook or an administrator action). Telling the provider "ship this" is
// creating the fulfillment, and that is [Service.CreateFulfillment]; calling the
// provider from here would mean triggering the same fact twice.
//
// On an already shipped fulfillment, a second call made with the SAME tracking
// number (or with an empty one) DOES NOT return an error; if a DIFFERENT
// tracking number is requested errors.Conflict is returned, because that is no
// longer a repeat but a new request.
func (s *Service) MarkShipped(
	ctx context.Context,
	id, trackingNumber, trackingURL string,
) (models.Fulfillment, error) {
	if err := requireID(id, models.FulfillmentIDPrefix, "the fulfillment identifier"); err != nil {
		return models.Fulfillment{}, err
	}
	number := strings.TrimSpace(trackingNumber)
	if err := checkTextLen("the tracking number", number); err != nil {
		return models.Fulfillment{}, err
	}
	url := strings.TrimSpace(trackingURL)
	if err := checkTextLen("the tracking URL", url); err != nil {
		return models.Fulfillment{}, err
	}

	var out models.Fulfillment
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		ful, err := s.store.LockFulfillment(ctx, id)
		if err != nil {
			return err
		}

		switch ful.Status.ShipAction() {
		case models.ActionNoop:
			if number != "" && number != ful.TrackingNumber {
				return errors.Conflict(CodeInvalidTransition,
					"the fulfillment was shipped with the tracking number %q; it cannot be shipped again with %q (%s)",
					ful.TrackingNumber, number, id)
			}
			out = ful
			s.log.DebugContext(ctx, "the fulfillment has already been shipped", "fulfillment", id)
			return nil
		case models.ActionConflict:
			return errors.Conflict(CodeInvalidTransition,
				"a fulfillment in the %q state cannot be shipped: %s", ful.Status, id)
		case models.ActionProceed:
			// Handled below.
		}

		// Tracking information given empty DOES NOT ERASE what is there: the
		// provider may have given a number while opening the fulfillment, and "I
		// did not give the information" and "remove the information" are
		// different requests.
		if number == "" {
			number = ful.TrackingNumber
		}
		if url == "" {
			url = ful.TrackingURL
		}

		now := s.now()
		updated, err := s.store.UpdateFulfillmentStatus(ctx, ful.ID, models.StatusShipped,
			number, url, &now, ful.DeliveredAt, ful.CanceledAt)
		if err != nil {
			return err
		}
		out = updated
		return nil
	})
	if err != nil {
		return models.Fulfillment{}, err
	}
	return out, nil
}

// MarkDelivered marks the fulfillment as delivered.
//
// THE PROVIDER IS NOT CALLED; the rationale is the same as for
// [Service.MarkShipped].
//
// Only a SHIPPED fulfillment can be delivered: skipping the order would leave
// shipped_at empty and, during reconciliation, when the fulfillment set out
// would stay unanswered. On an already delivered fulfillment a second call
// returns no error (idempotency).
func (s *Service) MarkDelivered(ctx context.Context, id string) (models.Fulfillment, error) {
	if err := requireID(id, models.FulfillmentIDPrefix, "the fulfillment identifier"); err != nil {
		return models.Fulfillment{}, err
	}

	var out models.Fulfillment
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		ful, err := s.store.LockFulfillment(ctx, id)
		if err != nil {
			return err
		}

		switch ful.Status.DeliverAction() {
		case models.ActionNoop:
			out = ful
			s.log.DebugContext(ctx, "the fulfillment has already been delivered", "fulfillment", id)
			return nil
		case models.ActionConflict:
			return errors.Conflict(CodeInvalidTransition,
				"a fulfillment in the %q state cannot be delivered; it has to be shipped first: %s", ful.Status, id)
		case models.ActionProceed:
			// Handled below.
		}

		now := s.now()
		updated, err := s.store.UpdateFulfillmentStatus(ctx, ful.ID, models.StatusDelivered,
			ful.TrackingNumber, ful.TrackingURL, ful.ShippedAt, &now, ful.CanceledAt)
		if err != nil {
			return err
		}
		out = updated
		return nil
	})
	if err != nil {
		return models.Fulfillment{}, err
	}
	return out, nil
}

// GetFulfillment returns the fulfillment together with its ITEMS;
// errors.NotFound if absent.
func (s *Service) GetFulfillment(ctx context.Context, id string) (models.Fulfillment, error) {
	if err := requireID(id, models.FulfillmentIDPrefix, "the fulfillment identifier"); err != nil {
		return models.Fulfillment{}, err
	}
	ful, err := s.store.GetFulfillment(ctx, id)
	if err != nil {
		return models.Fulfillment{}, err
	}
	return s.withItems(ctx, ful)
}

// ListFulfillmentsInput is the input of the fulfillment listing.
type ListFulfillmentsInput struct {
	// Reference, if given, restricts the result to that order's fulfillments.
	Reference *string
	// Status, if given, restricts the result to the fulfillments in that state.
	Status *string
	// Page holds the pagination parameters.
	Page Page
}

// ListFulfillments returns the fulfillments together with their ITEMS, with
// pagination. The second return value is the count of ALL records matching the
// filter.
//
// The items are fetched with a SINGLE batch query; there is no query per
// fulfillment (N+1).
func (s *Service) ListFulfillments(
	ctx context.Context,
	in ListFulfillmentsInput,
) ([]models.Fulfillment, int64, error) {
	page, err := in.Page.normalize()
	if err != nil {
		return nil, 0, err
	}
	if in.Status != nil {
		if _, statusErr := normalizeStatus(*in.Status); statusErr != nil {
			return nil, 0, statusErr
		}
	}

	list, total, err := s.store.ListFulfillments(ctx, models.FulfillmentFilter{
		Reference: in.Reference,
		Status:    in.Status,
		Limit:     page.Limit,
		Offset:    page.Offset,
	})
	if err != nil {
		return nil, 0, err
	}
	if len(list) == 0 {
		return list, total, nil
	}

	ids := make([]string, 0, len(list))
	for i := range list {
		ids = append(ids, list[i].ID)
	}
	items, err := s.store.FulfillmentItemsByFulfillments(ctx, ids)
	if err != nil {
		return nil, 0, err
	}

	byFulfillment := make(map[string][]models.FulfillmentItem, len(list))
	for i := range items {
		byFulfillment[items[i].FulfillmentID] = append(byFulfillment[items[i].FulfillmentID], items[i])
	}
	for i := range list {
		list[i].Items = byFulfillment[list[i].ID]
	}
	return list, total, nil
}

// withItems attaches its items to the fulfillment.
func (s *Service) withItems(ctx context.Context, ful models.Fulfillment) (models.Fulfillment, error) {
	items, err := s.store.ListFulfillmentItems(ctx, ful.ID)
	if err != nil {
		return models.Fulfillment{}, err
	}
	ful.Items = items
	return ful, nil
}

// normalizeItems validates the item inputs and rejects duplicates.
//
// Giving the same order line twice would trip the unique index; catching it here
// is so that the error can say which line it came from.
func normalizeItems(in []FulfillmentItemInput) ([]FulfillmentItemInput, error) {
	if len(in) > maxItemsPerFulfillment {
		return nil, errors.Invalid(CodeInvalidInput,
			"a fulfillment can contain at most %d items: %d", maxItemsPerFulfillment, len(in))
	}

	seen := make(map[string]struct{}, len(in))
	out := make([]FulfillmentItemInput, 0, len(in))
	for i, item := range in {
		lineID := strings.TrimSpace(item.LineItemID)
		if err := requireText("the order line identifier", lineID); err != nil {
			return nil, err
		}
		if item.Quantity < models.MinQuantity || item.Quantity > models.MaxQuantity {
			return nil, errors.Invalid(CodeInvalidInput,
				"the quantity of item %d has to be between %d and %d: %d",
				i+1, models.MinQuantity, models.MaxQuantity, item.Quantity)
		}
		if _, dup := seen[lineID]; dup {
			return nil, errors.Invalid(CodeInvalidInput,
				"the same order line cannot appear twice in a fulfillment: %s", lineID)
		}
		seen[lineID] = struct{}{}
		out = append(out, FulfillmentItemInput{LineItemID: lineID, Quantity: item.Quantity})
	}
	return out, nil
}

// savedItemsKey turns the saved items into a single comparable text.
//
// The text is SORTED: if the item set is the same, the order in which the items
// were given or the order in which they were read from the database must not
// count as a difference. Because an order line identifier is unique within a
// fulfillment (unique index), the sorted text is the canonical form of the set.
func savedItemsKey(items []models.FulfillmentItem) string {
	parts := make([]string, 0, len(items))
	for i := range items {
		parts = append(parts, fmt.Sprintf("%s:%d", items[i].LineItemID, items[i].Quantity))
	}
	slices.Sort(parts)
	return strings.Join(parts, " ")
}

// requestedItemsKey turns the requested items into text in the SAME form as
// [savedItemsKey]; two texts are comparable only if they are produced the same
// way.
func requestedItemsKey(items []FulfillmentItemInput) string {
	parts := make([]string, 0, len(items))
	for i := range items {
		parts = append(parts, fmt.Sprintf("%s:%d", items[i].LineItemID, items[i].Quantity))
	}
	slices.Sort(parts)
	return strings.Join(parts, " ")
}

// providerStatus translates the core contract's status into the module's status.
//
// The translation is done EXPLICITLY and an unrecognized value returns an error:
// the two types carry the same strings today, but one is the core's contract and
// the other this module's schema. Converting directly would mean attempting to
// write an undefined value into the database the day the core adds a new status.
func providerStatus(status coreprovider.FulfillmentStatus, providerID string) (models.FulfillmentStatus, error) {
	switch status {
	case coreprovider.FulfillmentPending:
		return models.StatusPending, nil
	case coreprovider.FulfillmentShipped:
		return models.StatusShipped, nil
	case coreprovider.FulfillmentDelivered:
		return models.StatusDelivered, nil
	case coreprovider.FulfillmentCanceled:
		return models.StatusCanceled, nil
	case "":
		// For a provider that reports no status the safest assumption is
		// "created, has not set out yet": counting the fulfillment as pending
		// rather than mistakenly as completed leaves the flow advanceable.
		return models.StatusPending, nil
	default:
		return "", errors.Internal(CodeProviderContract,
			"the provider %q returned an unrecognized fulfillment status: %q", providerID, status)
	}
}

// mergeProviderData merges the option's configuration with the request's data.
//
// The option's Data field is THE PROVIDER CONFIGURATION (contract number, fee
// tiers) and is already passed to the price query as it is; passing it while the
// fulfillment is opened is a requirement too, otherwise the provider could not
// know which account to print the label against.
//
// On a clash THE REQUEST's data wins: the configuration is the store's fixed
// setting, while the request is specific to that fulfillment (address, branch,
// item breakdown) and is more particular.
//
// The returned map is NEW; the option's Data IS NOT MODIFIED on this path. An
// in-place merge would mean the next fulfillment opened with the same option
// carrying the previous one's data.
func mergeProviderData(config, request map[string]any) map[string]any {
	if len(config) == 0 && len(request) == 0 {
		return nil
	}
	out := make(map[string]any, len(config)+len(request))
	maps.Copy(out, config)
	maps.Copy(out, request)
	return out
}

// stampFor returns the given moment if the status equals the target, otherwise
// nil.
//
// The fulfillments_*_stamp constraints in the schema require the stamp of the
// written status to be filled in; this helper establishes that pairing in a
// single place.
func stampFor(status, target models.FulfillmentStatus, now time.Time) *time.Time {
	if status != target {
		return nil
	}
	stamp := now
	return &stamp
}
