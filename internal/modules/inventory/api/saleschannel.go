package api

// This file is English because ADR 0012 makes language a property of the FILE
// and every new file is English; api.go beside it stays Turkish.

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// The paths of the warehouse-to-channel binding.
const (
	pathLocationChannels = "/admin/v1/stock-locations/{id}/sales-channels"
	pathLocationChannel  = "/admin/v1/stock-locations/{id}/sales-channels/{salesChannelId}"
)

// paramSalesChannelID is the channel id in the path.
const paramSalesChannelID = "salesChannelId"

// ChannelBindings is the surface of the core's link service used here.
//
// It is declared in this package rather than imported as core/link's own
// interface for the reason every narrow surface in this tree gives: the handler
// needs three of that service's seven methods, and a consumer that names only
// what it uses cannot be broken by a method it never calls.
type ChannelBindings interface {
	// Create binds fromID to toID; binding a pair twice is a no-op.
	Create(ctx context.Context, name, fromID, toID string) error
	// Delete removes the binding; removing an absent one is a no-op.
	Delete(ctx context.Context, name, fromID, toID string) error
	// List returns the toIDs bound to fromID, ascending.
	List(ctx context.Context, name, fromID string) ([]string, error)
}

// salesChannelBindingRequest is the body of the bind endpoint.
type salesChannelBindingRequest struct {
	// SalesChannelID is the channel the warehouse ships for. It is required.
	//
	// It is SINGULAR, like the api key's own binding endpoint: one call binds
	// one channel, and a list would have to answer what happens when the third
	// of five fails.
	SalesChannelID string `json:"sales_channel_id"`
}

// salesChannelsResponse is what the listing answers with.
type salesChannelsResponse struct {
	// SalesChannelIDs are the channels this warehouse ships for, ascending.
	//
	// An EMPTY list is not "ships for nobody": it is a warehouse nobody has
	// bound, and an unbound channel is narrowed by nothing — see
	// [service.LinkStockLocationSalesChannel].
	SalesChannelIDs []string `json:"sales_channel_ids"`
}

// bindings returns the link surface; if it is not bound it returns an ERROR.
//
// It fails CLOSED. The endpoints below exist to say which warehouses a channel
// ships from, and that answer is what the checkout narrows its reservations
// with: a binding that silently did not happen would leave the merchant
// believing a channel was restricted while every warehouse still served it.
func (h *Handler) bindings() (ChannelBindings, error) {
	if h.links == nil {
		return nil, coreerrors.Internal(codeLinkUnavailable,
			"the link service is not bound; a warehouse cannot be bound to a sales channel "+
				"without it, and a binding that did not happen would leave the channel "+
				"looking narrowed while every warehouse still serves it")
	}

	return h.links, nil
}

// listLocationSalesChannels answers which channels the warehouse ships for.
func (h *Handler) listLocationSalesChannels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	links, err := h.bindings()
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	locationID := chi.URLParam(r, "id")
	if _, err := h.svc.GetStockLocation(ctx, locationID); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	channels, err := links.List(ctx, service.LinkStockLocationSalesChannel, locationID)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK,
		singleEnvelope{Data: salesChannelsResponse{SalesChannelIDs: channels}})
}

// bindLocationSalesChannel binds the warehouse to a channel.
//
// # Why the warehouse is read first and the channel is not
//
// The warehouse is this module's record and an unknown id is a mistake it can
// see. The channel belongs to the auth module and this one never validates a
// foreign reference (Principle 2.2) — the link service cannot either, since it
// knows no module's schema. So a channel id with a typo is recorded, and the
// fault shows up where it is visible: the checkout narrows to a channel nothing
// is bound to and the merchant sees an unrestricted channel.
func (h *Handler) bindLocationSalesChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	links, err := h.bindings()
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	var body salesChannelBindingRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}
	if body.SalesChannelID == "" {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest,
			"sales_channel_id is required"))

		return
	}

	locationID := chi.URLParam(r, "id")
	if _, err := h.svc.GetStockLocation(ctx, locationID); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	if err := links.Create(ctx,
		service.LinkStockLocationSalesChannel, locationID, body.SalesChannelID); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	channels, err := links.List(ctx, service.LinkStockLocationSalesChannel, locationID)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK,
		singleEnvelope{Data: salesChannelsResponse{SalesChannelIDs: channels}})
}

// unbindLocationSalesChannel removes the binding.
//
// Removing one that is not there succeeds, because the link service treats it
// as the state the caller asked for; the answer is the remaining list either
// way, which is what a client refreshing its view needs.
func (h *Handler) unbindLocationSalesChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	links, err := h.bindings()
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	locationID := chi.URLParam(r, "id")
	if _, err := h.svc.GetStockLocation(ctx, locationID); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	if err := links.Delete(ctx, service.LinkStockLocationSalesChannel,
		locationID, chi.URLParam(r, paramSalesChannelID)); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	channels, err := links.List(ctx, service.LinkStockLocationSalesChannel, locationID)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK,
		singleEnvelope{Data: salesChannelsResponse{SalesChannelIDs: channels}})
}
