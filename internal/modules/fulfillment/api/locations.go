package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// This file holds the shipping policy of a stock location, under
// /admin/v1/shipping-locations. The record is a PREFERENCE — a priority and the
// regions the location serves — and not the location itself: the location is
// another module's record and this module only says how one is chosen among
// them. A location with no record here is not closed, it is at the default.

// setLocationRequest is the body of
// PUT /admin/v1/shipping-locations/{location_id}.
//
// The route is PUT, not PATCH, and that is deliberate: the body is not a
// CORRECTION but the WHOLE policy of the location. A field left out does not
// mean "do not change it"; if the region list is not given, the location's
// bindings are DELETED and the location comes to serve all regions. PATCH would
// promise "change what I sent, leave the rest alone" and that promise could not
// be kept for this body — an empty slice and a missing field cannot be told
// apart as they pass through JSON.
type setLocationRequest struct {
	// Priority is the preference order; a smaller value comes first, negative
	// values are allowed.
	Priority int64 `json:"priority"`
	// RegionIDs are the shipping regions the location serves. If EMPTY, the
	// location serves ALL regions.
	RegionIDs []string `json:"region_ids"`
}

// setLocation writes or overwrites the shipping policy of a location.
func (h *Handler) setLocation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body setLocationRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	loc, err := h.svc.SetShippingLocation(ctx, service.SetShippingLocationInput{
		LocationID: chi.URLParam(r, "location_id"),
		Priority:   body.Priority,
		RegionIDs:  body.RegionIDs,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toLocationDTO(loc)})
}

// getLocation returns the policy of the location.
func (h *Handler) getLocation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	loc, err := h.svc.GetShippingLocation(ctx, chi.URLParam(r, "location_id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toLocationDTO(loc)})
}

// listLocations returns the written policies in priority order, page by page.
func (h *Handler) listLocations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	locations, count, err := h.svc.ListShippingLocations(ctx, page)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]locationDTO, 0, len(locations))
	for i := range locations {
		data = append(data, toLocationDTO(locations[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:   data,
		Count:  count,
		Offset: page.Offset,
		Limit:  page.Limit,
	})
}

// deleteLocation deletes the policy and returns the location to the DEFAULT.
//
// Deleting does not close the location: a location without a record is
// considered to be at priority zero and to serve all regions. Removing a
// location from candidacy is not within the shipping module's authority — the
// candidate list is produced by an inventory fact.
func (h *Handler) deleteLocation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.DeleteShippingLocation(ctx, chi.URLParam(r, "location_id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}
