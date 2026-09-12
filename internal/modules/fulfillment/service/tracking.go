package service

import (
	"context"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// This file answers one question — where is this parcel — and its whole design is
// about not answering it wrongly (ADR 0149).
//
// # Why the provider's answer is REPORTED and never written
//
// Which side is authoritative depends on the provider. A real carrier knows where
// a parcel is and this module does not; the provider that ships in the box is the
// SHOP, so there the module's status — an operator marking a parcel handed over —
// is the true one and the provider's row is a stub nobody moves. A write would
// pick a winner for both cases and be wrong in one, so both answers are reported
// side by side and the human decides.
//
// # Why "nobody could ask" is its own answer
//
// [coreprovider.ShipmentTracker] is optional, which is the whole reason the
// payment module's [coreprovider.SessionInspector] is optional too: a provider
// that cannot answer must not be made to fake an answer. What follows is that the
// caller has to be able to tell the cases apart. "The carrier says it is at the
// depot", "the carrier has never heard of this label", "this carrier cannot be
// asked at all" and "we never opened a label" are four different facts, and a
// surface that collapsed them would report the last three as a parcel that has
// not moved.

// TrackingAnswer says WHAT KIND of answer a tracking read produced.
//
// It is a typed string rather than a boolean pair because there are four
// outcomes, and the reason they are enumerated is the payment reconciliation's:
// "the two agree" and "nobody asked" must never look the same (the payment
// module's ReconciliationReport counts its Unaskable sessions apart for exactly
// this reason).
type TrackingAnswer string

// The four outcomes of asking a provider where a shipment is.
const (
	// TrackingAnswered means the provider answered and its view is filled in.
	TrackingAnswered TrackingAnswer = "answered"
	// TrackingUnknown means the provider was asked and disowned the shipment.
	//
	// It is a FINDING rather than an error: this module holds an external
	// identifier the provider says is not its own, which is what a label opened
	// against the wrong account looks like from here.
	TrackingUnknown TrackingAnswer = "unknown_to_provider"
	// TrackingUnaskable means the provider cannot be asked at all — it is not
	// registered, or it is registered without the tracking capability.
	//
	// The ordinary case today: the provider that ships in the box is the only one
	// that answers, and a plugin's carrier may legitimately offer no tracking.
	TrackingUnaskable TrackingAnswer = "unaskable"
	// TrackingUnreachable means the provider was asked and could not answer.
	//
	// A network fault, a rate limit, an expired credential. It is separate from
	// [TrackingUnknown] because the repair is different: one is a retry and the
	// other is a standing misconfiguration.
	TrackingUnreachable TrackingAnswer = "unreachable"
	// TrackingNotOpened means this module holds no provider identity for the
	// shipment, so there is nothing to ask about.
	//
	// It is not [TrackingUnknown]: nobody disowned anything, the label was simply
	// never opened at a provider.
	TrackingNotOpened TrackingAnswer = "not_opened"
)

// ShipmentTracking is what a provider says about a shipment, beside what this
// module holds.
type ShipmentTracking struct {
	// FulfillmentID and ProviderID identify the shipment and whose carrier it is.
	FulfillmentID string
	ProviderID    string
	// ExternalID is the provider's own identifier for the shipment; empty when
	// the label was never opened.
	ExternalID string

	// Answer says which of the five outcomes this is; everything below is filled
	// in only for [TrackingAnswered].
	Answer TrackingAnswer
	// Reason is the sentence behind an answer that is not [TrackingAnswered] —
	// the provider's error, or why it could not be asked.
	//
	// It is a message for a human and is never parsed. An empty Reason on a
	// non-answer would leave an operator with a status word and nothing to act on.
	Reason string

	// Local* is this module's own record, and it is here rather than left to the
	// caller to fetch: a tracking report whose two halves come from two requests
	// can show a status the shipment held a moment ago beside one it holds now.
	LocalStatus         models.FulfillmentStatus
	LocalTrackingNumber string
	LocalTrackingURL    string

	// Provider* is the carrier's view.
	ProviderStatus         models.FulfillmentStatus
	ProviderTrackingNumber string
	ProviderTrackingURL    string
	// ProviderDetail is the carrier's own words about the last movement; often
	// empty, never interpreted.
	ProviderDetail string
	// ProviderMovedAt is when the carrier last moved the parcel, or nil when it
	// does not say.
	ProviderMovedAt *time.Time
}

// TrackingNumbersAgree reports whether the two sides hold the same tracking
// number.
//
// It answers false when the provider did not answer, which is the safe direction:
// "they agree" must not be readable out of a question nobody could ask. An empty
// number on BOTH sides counts as agreement — neither side has one, which is the
// ordinary state of a parcel that has not been handed over yet.
func (t ShipmentTracking) TrackingNumbersAgree() bool {
	if t.Answer != TrackingAnswered {
		return false
	}

	return t.LocalTrackingNumber == t.ProviderTrackingNumber
}

// TrackShipment asks the shipment's provider where it is and reports that beside
// this module's own record.
//
// It WRITES NOTHING. The reasoning is at the top of this file and it is the same
// one that keeps the payment reconciliation a report: recording a status off the
// back of somebody else's answer would mean this module deciding, on its own, that
// a parcel moved.
//
// An unknown fulfillment is a NotFound error rather than an empty report: the
// caller asked about a shipment, and answering "it cannot be tracked" about a
// parcel that does not exist would hide a wrong identifier.
func (s *Service) TrackShipment(ctx context.Context, id string) (ShipmentTracking, error) {
	if err := requireID(id, models.FulfillmentIDPrefix, "the fulfillment identifier"); err != nil {
		return ShipmentTracking{}, err
	}

	ful, err := s.store.GetFulfillment(ctx, id)
	if err != nil {
		return ShipmentTracking{}, err
	}

	out := ShipmentTracking{
		FulfillmentID:       ful.ID,
		ProviderID:          ful.ProviderID,
		ExternalID:          ful.ExternalID,
		LocalStatus:         ful.Status,
		LocalTrackingNumber: ful.TrackingNumber,
		LocalTrackingURL:    ful.TrackingURL,
	}

	if strings.TrimSpace(ful.ExternalID) == "" {
		out.Answer = TrackingNotOpened
		out.Reason = "this shipment carries no provider identifier, so no label was " +
			"opened at a carrier and there is nothing to ask about"

		return out, nil
	}

	provider, err := s.providers.Get(ful.ProviderID)
	if err != nil {
		// A provider that is not registered is unaskable rather than an error:
		// the shipment exists, its carrier's plugin is simply not installed in
		// THIS process, and refusing the whole read would hide the local record
		// too.
		//
		// The registry's error IS the answer: it says the carrier cannot be asked,
		// which this report carries as a named outcome with the reason. Returning it
		// would turn "nobody could ask" into a failed read and take the local half
		// down with it.
		out.Answer = TrackingUnaskable
		out.Reason = "the shipping provider " + quoted(ful.ProviderID) +
			" is not registered in this installation, so it cannot be asked"

		return out, nil //nolint:nilerr // the error is the answer; see above
	}

	tracker, ok := provider.(coreprovider.ShipmentTracker)
	if !ok {
		out.Answer = TrackingUnaskable
		out.Reason = "the shipping provider " + quoted(ful.ProviderID) +
			" offers no tracking; a provider that cannot answer is not asked to guess"

		return out, nil
	}

	update, err := tracker.Track(ctx, ful.ExternalID)
	if err != nil {
		if errors.IsNotFound(err) {
			out.Answer = TrackingUnknown
			out.Reason = "the provider disowns " + quoted(ful.ExternalID) +
				"; this module holds an identifier the carrier says is not its own"

			return out, nil
		}

		out.Answer = TrackingUnreachable
		out.Reason = err.Error()

		return out, nil
	}

	out.Answer = TrackingAnswered
	out.ProviderStatus = models.FulfillmentStatus(update.Status)
	out.ProviderTrackingNumber = update.TrackingNumber
	out.ProviderTrackingURL = update.TrackingURL
	out.ProviderDetail = update.Detail
	if !update.MovedAt.IsZero() {
		moved := update.MovedAt.UTC()
		out.ProviderMovedAt = &moved
	}

	return out, nil
}

// quoted wraps a value in quotes for a message.
//
// A bare identifier in the middle of a sentence is unreadable when it is empty,
// which is exactly when an operator is reading the sentence.
func quoted(value string) string { return `"` + value + `"` }
