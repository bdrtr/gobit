package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// This file holds the shipping profile endpoints under
// /admin/v1/shipping-profiles. The profile is the catalog GROUP an option is
// bound to and it has its own life cycle: the option endpoints next door carry
// a shipping_profile_id but never create, rename or delete the profile behind
// it.

// createProfileRequest is the body of POST /admin/v1/shipping-profiles.
type createProfileRequest struct {
	Name     string         `json:"name"`
	Type     string         `json:"type"`
	Metadata map[string]any `json:"metadata"`
}

// createProfile creates a new shipping profile.
func (h *Handler) createProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createProfileRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	profile, err := h.svc.CreateShippingProfile(ctx, service.CreateProfileInput{
		Name:     body.Name,
		Type:     body.Type,
		Metadata: body.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toProfileDTO(profile)})
}

// listProfiles returns the shipping profiles page by page.
func (h *Handler) listProfiles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	in := service.ListProfilesInput{Page: page}
	if raw := r.URL.Query().Get("type"); raw != "" {
		in.Type = &raw
	}

	profiles, count, err := h.svc.ListShippingProfiles(ctx, in)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]profileDTO, 0, len(profiles))
	for i := range profiles {
		data = append(data, toProfileDTO(profiles[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:   data,
		Count:  count,
		Offset: page.Offset,
		Limit:  page.Limit,
	})
}

// getProfile returns the profile by its identifier.
func (h *Handler) getProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	profile, err := h.svc.GetShippingProfile(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toProfileDTO(profile)})
}

// updateProfileRequest is the body of PATCH /admin/v1/shipping-profiles/{id}.
//
// The fields are POINTERS: the distinction between "not sent" and "sent empty"
// is preserved; a field that is not sent does not change.
type updateProfileRequest struct {
	Name     *string        `json:"name"`
	Type     *string        `json:"type"`
	Metadata map[string]any `json:"metadata"`
}

// updateProfile updates the given fields of the profile.
func (h *Handler) updateProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body updateProfileRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	profile, err := h.svc.UpdateShippingProfile(ctx, chi.URLParam(r, "id"), service.UpdateProfileInput{
		Name:     body.Name,
		Type:     body.Type,
		Metadata: body.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toProfileDTO(profile)})
}

// deleteProfile soft deletes the profile.
func (h *Handler) deleteProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.DeleteShippingProfile(ctx, chi.URLParam(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}
