// Package manual is the test/manual fulfillment provider that makes no real
// network call (plan Phase 7).
//
// [Provider] satisfies the FulfillmentProvider contract in
// internal/core/provider and meets the conditions written in that contract's
// godoc:
//
//   - [Provider.Quote] HAS NO SIDE EFFECTS: it writes nothing, does not read
//     the ledger and always returns the same fee for the same input. It may be
//     called over and over while a cart total is being computed.
//   - A second [Provider.Create] with the same IdempotencyKey does NOT open a
//     new shipment, it returns the existing one.
//   - [Provider.Cancel] is the saga compensation and it is IDEMPOTENT: a
//     shipment that is canceled twice does not fail on the second call.
//
// # Why the state is kept IN THE DATABASE
//
// The decision is THE SAME as for the manual provider in the payment module and
// rests on the same grounds. A ledger kept in memory would be reset on every
// restart of the process; the price would be paid in three places:
//
//   - The e2e flows (internal/e2e) and the Phase 9 load test have to be able to
//     find an OPENED shipment after the process restarts; otherwise the
//     fulfillment step fails with "shipment not found".
//   - The saga compensation has to run in exactly the scenario where the
//     process went down. In a memory-backed provider Cancel could never run
//     after a restart and a shipping label that had been printed would stay
//     open forever.
//   - Multiple processes (or horizontal scaling) would not see the same
//     shipment; the provider would only behave correctly on a server running as
//     a single instance.
//
// A real carrier's state also lives in its own system and is unaffected by
// process restarts; the imitation therefore has to be durable.
//
// [Provider.Quote] is OUTSIDE THIS RULE and stores nothing — if it stored
// anything it would not be free of side effects. The price is computed PURELY
// from the option's configuration and from the cart context.
//
// # The separateness of the ledger
//
// The provider's state lives in the fulfillment_manual_shipments table and is
// SEPARATE from the fulfillment service's tables. The service never touches
// this table; it reaches the provider only through the FulfillmentProvider
// interface. The separation structurally prevents the module from accidentally
// reading the provider's internal state — with a real provider such a read is
// not possible either.
//
// # Failure injection for tests
//
// Saga tests MUST BE ABLE TO BLOW UP the fulfillment step. The behavior is read
// from the shipping option's configuration ([coreprovider.QuoteInput.Data]) and
// from the Data field given while the shipment is being opened; the shipment
// behavior is stored durably together with the shipment, so that the same
// shipment behaves the same way even if the process restarts. See
// [DataKeyOutcome], [DataKeyQuoteAmount] and the price keys.
package manual

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// ID is the provider's identity; shipping options are opened under this name.
const ID = "manual"

// The Data keys that steer the provider's behavior.
//
// The price keys come from the shipping OPTION's configuration
// (shipping_options.data) and are passed to Quote as they are. The behavior
// keys ([DataKeyOutcome]) are read both in the price query and while a shipment
// is being opened; the ones given while opening a shipment are STORED together
// with the shipment, because the cancellation happens in another request (even
// in another process) and that call holds nothing but the shipment identifier.
const (
	// DataKeyOutcome decides the outcome of the call; its values are
	// [OutcomeOK] and [OutcomeError]. If it is not given, [OutcomeOK] is
	// assumed.
	DataKeyOutcome = "manual_outcome"
	// DataKeyQuoteAmount sets the price DIRECTLY; if it is given, the
	// components below are never computed.
	DataKeyQuoteAmount = "manual_quote_amount"
	// DataKeyBaseAmount is the flat fee per shipment (minor unit).
	DataKeyBaseAmount = "manual_base_amount"
	// DataKeyPerItemAmount is the fee per item (minor unit).
	DataKeyPerItemAmount = "manual_per_item_amount"
	// DataKeyPerKilogramAmount is the fee for every started kilogram
	// (minor unit); rounding goes UP (see [Provider.Quote]).
	DataKeyPerKilogramAmount = "manual_per_kilogram_amount"
	// DataKeyTrackingNumber is the tracking number to be written on the
	// shipment.
	DataKeyTrackingNumber = "manual_tracking_number"
	// DataKeyTrackingURL is the tracking address to be written on the shipment.
	DataKeyTrackingURL = "manual_tracking_url"
)

// The call outcomes ([DataKeyOutcome] values).
const (
	// OutcomeOK makes the call succeed; it is the default behavior.
	OutcomeOK = "ok"
	// OutcomeError imitates the provider being UNREACHABLE: the method returns
	// an error and nothing in the ledger changes. It exists to exercise the
	// saga's "the step blew up" branch and it is a retryable error
	// (errors.Unavailable).
	OutcomeError = "error"
)

// gramsPerKilogram is the gram equivalent of one kilogram.
const gramsPerKilogram int64 = 1000

// Error codes. Clients may branch on these; the messages may change, the codes
// do not.
const (
	// CodeInvalidInput reports that the input did not pass validation.
	CodeInvalidInput = "fulfillment_manual_invalid_input"
	// CodeInvalidState reports that an invalid transition was attempted on the
	// shipment's status.
	CodeInvalidState = "fulfillment_manual_invalid_state"
	// CodeIdempotencyMismatch reports that the same key was reused with a
	// DIFFERENT body.
	CodeIdempotencyMismatch = "fulfillment_manual_idempotency_mismatch"
	// CodeSimulatedFailure reports a failure injected for testing.
	CodeSimulatedFailure = "fulfillment_manual_simulated_failure"
	// CodeDataInvalid reports that the shipment data could not be parsed.
	CodeDataInvalid = "fulfillment_manual_data_invalid"
)

// Store is the persistence surface the provider needs.
//
// The interface is declared on the CONSUMING side, that is, here (the pattern
// of ADR 0001). The provider does NOT import the repository package; the
// concrete repository satisfies these signatures structurally and the wiring is
// done in module.go. That way the provider's idempotency behavior can be
// exercised without a real database, with a fake store a few lines long.
//
// The locking method ([Store.LockManualShipment]) may only be called inside
// [Store.WithTx]: a FOR UPDATE lock without a transaction protects nothing.
//
// The price query has NO counterpart here, and that is deliberate: Quote has no
// side effects and never touches the ledger.
type Store interface {
	// WithTx runs fn in a single transaction; if fn returns an error the
	// transaction is rolled back.
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error

	// InsertManualShipmentIfAbsent writes the shipment only if the idempotency
	// key has not been used yet. The second return value says whether the row
	// was written; a conflict is NOT AN ERROR.
	InsertManualShipmentIfAbsent(ctx context.Context, shipment models.ManualShipment) (models.ManualShipment, bool, error)
	// ManualShipmentByIdempotencyKey returns the shipment by its key; NotFound
	// if there is none.
	ManualShipmentByIdempotencyKey(ctx context.Context, key string) (models.ManualShipment, error)
	// ManualShipment returns the shipment by its identifier; NotFound if there
	// is none.
	ManualShipment(ctx context.Context, id string) (models.ManualShipment, error)
	// LockManualShipment locks the shipment for the duration of the transaction
	// and returns its current state.
	LockManualShipment(ctx context.Context, id string) (models.ManualShipment, error)
	// UpdateManualShipmentState writes the status and the tracking details as
	// ABSOLUTE values.
	UpdateManualShipmentState(
		ctx context.Context,
		id string,
		status models.FulfillmentStatus,
		trackingNumber, trackingURL string,
	) (models.ManualShipment, error)
}

// Provider is the manual/test fulfillment provider. It is safe for concurrent
// use.
type Provider struct {
	store Store
	log   *slog.Logger
}

// That Provider satisfies the core contract is verified at compile time; a
// signature drift does not survive until runtime.
var _ coreprovider.FulfillmentProvider = (*Provider)(nil)

// New produces a manual provider that works on the given store.
// If log is nil, the logs are discarded.
func New(store Store, log *slog.Logger) *Provider {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Provider{store: store, log: log}
}

// ID returns the provider's identity.
func (p *Provider) ID() string { return ID }

// Quote returns the shipping fee for the given option. IT HAS NO SIDE EFFECTS.
//
// The computation is PURE: it does not touch the database, does not look at the
// clock and always gives the same result for the same input. Because it may be
// called every time the cart total is updated, it has to be cheap.
//
// # Formula
//
//	fee = base + (per item x item count) + (per kilogram x ⌈grams/1000⌉)
//
// The components are read from the shipping option's configuration
// ([DataKeyBaseAmount], [DataKeyPerItemAmount], [DataKeyPerKilogramAmount]); a
// component that is not given is ZERO. If [DataKeyQuoteAmount] is given the
// formula never runs and that amount is returned.
//
// # Rounding goes UP
//
// The weight is in grams and the per-kilogram fee is charged for every STARTED
// kilogram: 1200 grams counts as TWO kilograms. The direction is deliberate and
// follows the volumetric/kilogram tiers of real carriers; rounding down would
// mean carrying a 1999 gram parcel for the price of one kilogram. The
// computation uses INTEGER arithmetic; money never passes through floating
// point at any stage (plan Section 8).
//
// # Overflow is an ERROR, not a negative price
//
// The item count and the weight come FROM THE CALLER and this provider does not
// know their upper bound (the side that sets the bound is the service; see
// [models.MaxItemCount] and [models.MaxTotalWeight]). That is why the
// arithmetic itself has to be defensive: both the rounding to kilograms and the
// product/sum are written without an overflow, and if the computation exceeds
// [models.MaxAmount] errors.Invalid is returned. The provider returns a
// negative fee for NO input — if it did, dropping it would be left to the
// caller's last line of defense.
//
// An unconfigured option returns a ZERO fee and that is valid: "free shipping"
// is a real business decision, and having the imitation provider MAKE UP a
// price would blur the behavior under test.
func (p *Provider) Quote(
	_ context.Context,
	in coreprovider.QuoteInput,
) (coreprovider.ShippingQuote, error) {
	optionID := strings.TrimSpace(in.OptionID)
	if optionID == "" {
		return coreprovider.ShippingQuote{}, errors.Invalid(CodeInvalidInput,
			"the shipping option identifier is required")
	}
	currency := strings.ToUpper(strings.TrimSpace(in.CurrencyCode))
	if len(currency) != currencyCodeLength {
		return coreprovider.ShippingQuote{}, errors.Invalid(CodeInvalidInput,
			"the currency has to be a three-letter ISO 4217 code: %q", in.CurrencyCode)
	}
	if in.ItemCount < 0 {
		return coreprovider.ShippingQuote{}, errors.Invalid(CodeInvalidInput,
			"the item count may not be negative: %d", in.ItemCount)
	}
	if in.TotalWeight < 0 {
		return coreprovider.ShippingQuote{}, errors.Invalid(CodeInvalidInput,
			"the total weight may not be negative: %d", in.TotalWeight)
	}

	config, err := parseData(in.Data)
	if err != nil {
		return coreprovider.ShippingQuote{}, err
	}
	if config.Outcome == OutcomeError {
		return coreprovider.ShippingQuote{}, errors.Unavailable(CodeSimulatedFailure,
			"the manual fulfillment provider is unreachable (failure injected for testing): %s", optionID)
	}

	amount, err := quoteAmount(config, in.ItemCount, in.TotalWeight)
	if err != nil {
		return coreprovider.ShippingQuote{}, err
	}

	return coreprovider.ShippingQuote{
		OptionID:     optionID,
		Amount:       amount,
		CurrencyCode: currency,
	}, nil
}

// Create opens a shipment in the provider's ledger.
//
// A second call with the same IdempotencyKey does NOT open a new shipment, it
// returns the existing one (the condition of the core contract). If the key is
// the same but the reference or the option is DIFFERENT, errors.Conflict is
// returned: idempotency means "repeating the same request", not "sending a
// different request under an old key" — accepting the second one silently would
// mean the shipment the caller believed it had opened was never opened at all.
func (p *Provider) Create(
	ctx context.Context,
	in coreprovider.CreateFulfillmentInput,
) (coreprovider.Fulfillment, error) {
	key := strings.TrimSpace(in.IdempotencyKey)
	if key == "" {
		return coreprovider.Fulfillment{}, errors.Invalid(CodeInvalidInput,
			"the idempotency key is required")
	}
	reference := strings.TrimSpace(in.Reference)
	if reference == "" {
		return coreprovider.Fulfillment{}, errors.Invalid(CodeInvalidInput, "reference zorunludur")
	}
	optionID := strings.TrimSpace(in.OptionID)
	if optionID == "" {
		return coreprovider.Fulfillment{}, errors.Invalid(CodeInvalidInput,
			"the shipping option identifier is required")
	}

	raw, err := json.Marshal(in.Data)
	if err != nil {
		return coreprovider.Fulfillment{}, errors.Wrap(err, errors.KindInvalid, CodeDataInvalid,
			"shipment data could not be encoded")
	}
	// The data is validated early and an injected failure blows up HERE: a
	// broken behavior key has to be reported while the shipment is being
	// opened; blowing up on a later call would make the diagnosis harder.
	config, err := parseData(in.Data)
	if err != nil {
		return coreprovider.Fulfillment{}, err
	}
	if config.Outcome == OutcomeError {
		return coreprovider.Fulfillment{}, errors.Unavailable(CodeSimulatedFailure,
			"the manual fulfillment provider is unreachable (failure injected for testing): %s", reference)
	}

	created, inserted, err := p.store.InsertManualShipmentIfAbsent(ctx, models.ManualShipment{
		ID:             models.NewManualShipmentID(),
		IdempotencyKey: key,
		Reference:      reference,
		OptionID:       optionID,
		Status:         models.StatusPending,
		TrackingNumber: config.TrackingNumber,
		TrackingURL:    config.TrackingURL,
		Data:           raw,
	})
	if err != nil {
		return coreprovider.Fulfillment{}, err
	}
	if inserted {
		return toProviderFulfillment(created), nil
	}

	existing, err := p.store.ManualShipmentByIdempotencyKey(ctx, key)
	if err != nil {
		return coreprovider.Fulfillment{}, err
	}
	if existing.Reference != reference || existing.OptionID != optionID {
		return coreprovider.Fulfillment{}, errors.Conflict(CodeIdempotencyMismatch,
			"the same idempotency key was used for a different shipment: existing %s/%s, requested %s/%s",
			existing.Reference, existing.OptionID, reference, optionID)
	}
	p.log.DebugContext(ctx, "manual provider returned the existing shipment",
		"gonderi", existing.ID, "anahtar", key)
	return toProviderFulfillment(existing), nil
}

// Cancel cancels the shipment.
//
// THIS IS THE SAGA COMPENSATION and it is IDEMPOTENT: it does not return an
// error for a shipment that is already canceled and it does not change the
// ledger a second time. A shipment that has been DELIVERED CANNOT be canceled
// (errors.Conflict); the parcel is with the recipient and the way to get it
// back is a return. A shipment that has been handed to the carrier CAN be
// canceled: the carrier can recall a parcel that is on its way (see
// [models.FulfillmentStatus.CancelAction]).
//
// For an unknown identifier errors.NotFound is returned: idempotency does not
// mean "swallow everything silently". A REAL shipment that is canceled twice
// and an identifier that never existed are different situations, and the second
// one is a bug on the caller's side. Because the shipment record is not deleted
// (only its status changes) the first situation is always distinguishable.
func (p *Provider) Cancel(ctx context.Context, fulfillmentID string) error {
	if strings.TrimSpace(fulfillmentID) == "" {
		return errors.Invalid(CodeInvalidInput, "the shipment identifier is required")
	}

	return p.store.WithTx(ctx, func(ctx context.Context) error {
		shipment, err := p.store.LockManualShipment(ctx, fulfillmentID)
		if err != nil {
			return err
		}

		switch shipment.Status.CancelAction() {
		case models.ActionNoop:
			p.log.DebugContext(ctx, "the manual provider's shipment is already canceled",
				"gonderi", fulfillmentID)
			return nil
		case models.ActionConflict:
			return errors.Conflict(CodeInvalidState,
				"a shipment in the %q status cannot be canceled; use a return: %s",
				shipment.Status, fulfillmentID)
		case models.ActionProceed:
			// Handled below.
		}

		// The tracking details are PRESERVED: which label a canceled shipment
		// was opened with must still be readable for diagnosis.
		_, err = p.store.UpdateManualShipmentState(ctx, shipment.ID, models.StatusCanceled,
			shipment.TrackingNumber, shipment.TrackingURL)
		return err
	})
}

// GetShipment returns the shipment in the provider's ledger; errors.NotFound if
// there is none.
//
// It is NOT part of the core contract and the fulfillment service does NOT call
// it. It exists only for integration tests and for diagnosis: a shipment's
// status on the provider's side has to be verifiable without looking at the
// module's own record — a bug where the two ledgers have drifted apart can only
// be seen that way.
func (p *Provider) GetShipment(ctx context.Context, id string) (models.ManualShipment, error) {
	if strings.TrimSpace(id) == "" {
		return models.ManualShipment{}, errors.Invalid(CodeInvalidInput, "the shipment identifier is required")
	}
	return p.store.ManualShipment(ctx, id)
}

// toProviderFulfillment converts a ledger record into the fulfillment type of
// the core contract.
func toProviderFulfillment(shipment models.ManualShipment) coreprovider.Fulfillment {
	return coreprovider.Fulfillment{
		ID:             shipment.ID,
		Status:         coreprovider.FulfillmentStatus(shipment.Status),
		TrackingNumber: shipment.TrackingNumber,
		TrackingURL:    shipment.TrackingURL,
		Data:           shipment.Data,
	}
}
