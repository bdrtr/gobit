package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// This file is the one endpoint that asks a carrier where a parcel is
// (GET /admin/v1/fulfillments/{id}/tracking, ADR 0149).

// trackingDTO is the answer of the tracking read.
//
// # Why both sides are in one record
//
// The provider's view and this module's own are reported TOGETHER, and neither
// overwrites the other: with a real carrier the carrier knows where the parcel is,
// while the provider that ships in the box is the shop itself and there the
// module's status is the true one. A response that merged them would pick a winner
// for both cases and be wrong in one.
type trackingDTO struct {
	// FulfillmentID is the parcel this is about.
	FulfillmentID string `json:"fulfillment_id"`
	// ProviderID is whose carrier it is, and ExternalID the identifier that
	// carrier gave the shipment; the second is empty when no label was opened.
	ProviderID string `json:"provider_id"`
	ExternalID string `json:"external_id,omitempty"`

	// Answer is one of "answered", "unknown_to_provider", "unaskable",
	// "unreachable" and "not_opened".
	//
	// A client branches on THIS and not on whether the provider fields are empty:
	// a carrier that answers "pending" with no tracking number looks exactly like
	// a carrier nobody could ask, and those are different facts.
	Answer string `json:"answer"`
	// Reason is the sentence behind an answer that is not "answered"; it is for a
	// human and is never machine-read.
	//
	// It carries no omitempty, so an "answered" report shows it EMPTY rather than
	// dropping the key: a client that has to explain a non-answer should not have
	// to tell an absent field from an empty one to find out whether there is
	// anything to print.
	Reason string `json:"reason"`

	// Local is what this module holds.
	Local trackingSideDTO `json:"local"`
	// Provider is what the carrier says; present only when the answer is
	// "answered".
	Provider *trackingSideDTO `json:"provider,omitempty"`

	// TrackingNumbersAgree says whether the two sides hold the same number.
	//
	// It is FALSE whenever the provider did not answer, and that direction is the
	// point: "they agree" must not be readable out of a question nobody could ask.
	TrackingNumbersAgree bool `json:"tracking_numbers_agree"`
}

// trackingSideDTO is one side's view of the shipment.
type trackingSideDTO struct {
	// Status is the shipment's status on that side.
	Status string `json:"status"`
	// TrackingNumber and TrackingURL are that side's tracking details.
	TrackingNumber string `json:"tracking_number,omitempty"`
	TrackingURL    string `json:"tracking_url,omitempty"`
	// Detail is the carrier's own words about the last movement; it exists only
	// on the provider's side and is often empty.
	Detail string `json:"detail,omitempty"`
	// MovedAt is when that side last saw the parcel move.
	MovedAt *time.Time `json:"moved_at,omitempty"`
}

// trackFulfillment asks the provider where the parcel is
// (GET /admin/v1/fulfillments/{id}/tracking).
//
// # Why it is a READ and asks for the READ scope
//
// Nothing is written — not even when the carrier says something the module does
// not hold. The reasoning is in the service: which side is authoritative depends
// on the provider, so recording the carrier's answer would be this module deciding
// on its own that a parcel moved. What changes is that a human can see both.
//
// # Why it is on the ADMIN surface and not the storefront
//
// A shopper's own "where is my parcel" would put a carrier's network call on a
// storefront request, on the busiest surface there is and with no cache in front
// of it. The operator answering a telephone call is the caller this endpoint has,
// and the shopper's view stays the order timeline's stored state (ADR 0100).
func (h *Handler) trackFulfillment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	tracking, err := h.svc.TrackShipment(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toTrackingDTO(tracking)})
}

// toTrackingDTO maps the service's report onto the response record.
//
// The provider's half is a POINTER and is left out entirely when the carrier did
// not answer. An empty object there would be read as "the carrier says: nothing",
// which is the one reading this whole endpoint exists to prevent.
func toTrackingDTO(tracking service.ShipmentTracking) trackingDTO {
	out := trackingDTO{
		FulfillmentID: tracking.FulfillmentID,
		ProviderID:    tracking.ProviderID,
		ExternalID:    tracking.ExternalID,
		Answer:        string(tracking.Answer),
		Reason:        tracking.Reason,
		Local: trackingSideDTO{
			Status:         string(tracking.LocalStatus),
			TrackingNumber: tracking.LocalTrackingNumber,
			TrackingURL:    tracking.LocalTrackingURL,
		},
		TrackingNumbersAgree: tracking.TrackingNumbersAgree(),
	}

	if tracking.Answer != service.TrackingAnswered {
		return out
	}

	out.Provider = &trackingSideDTO{
		Status:         string(tracking.ProviderStatus),
		TrackingNumber: tracking.ProviderTrackingNumber,
		TrackingURL:    tracking.ProviderTrackingURL,
		Detail:         tracking.ProviderDetail,
		MovedAt:        tracking.ProviderMovedAt,
	}

	return out
}
