package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// This file holds the shipping option endpoints under
// /admin/v1/shipping-options. The option is the catalog record itself —
// provider, profile, price type and rate. The RULES that narrow it and the
// ELIGIBILITY listing that quotes it are separate surfaces with their own
// routes, and they live in their own files.

// createOptionRequest is the body of POST /admin/v1/shipping-options.
type createOptionRequest struct {
	Name              string `json:"name"`
	ProviderID        string `json:"provider_id"`
	ShippingProfileID string `json:"shipping_profile_id"`
	PriceType         string `json:"price_type"`
	// Amount is an INTEGER in minor units and is meaningful only on "flat"
	// options.
	Amount       int64          `json:"amount"`
	CurrencyCode string         `json:"currency_code"`
	RegionID     string         `json:"region_id"`
	IsReturn     bool           `json:"is_return"`
	AdminOnly    bool           `json:"admin_only"`
	Data         map[string]any `json:"data"`
	Metadata     map[string]any `json:"metadata"`
}

// createOption creates a new shipping option.
func (h *Handler) createOption(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createOptionRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	option, err := h.svc.CreateShippingOption(ctx, service.CreateOptionInput{
		Name:              body.Name,
		ProviderID:        body.ProviderID,
		ShippingProfileID: body.ShippingProfileID,
		PriceType:         body.PriceType,
		Amount:            body.Amount,
		CurrencyCode:      body.CurrencyCode,
		RegionID:          body.RegionID,
		IsReturn:          body.IsReturn,
		AdminOnly:         body.AdminOnly,
		Data:              body.Data,
		Metadata:          body.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toOptionDTO(option)})
}

// listOptions returns the shipping options page by page.
func (h *Handler) listOptions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	in := service.ListOptionsAdminInput{Page: page}
	if raw := r.URL.Query().Get("region_id"); raw != "" {
		in.RegionID = &raw
	}
	if raw := r.URL.Query().Get("shipping_profile_id"); raw != "" {
		in.ProfileID = &raw
	}
	if raw := r.URL.Query().Get("provider_id"); raw != "" {
		in.ProviderID = &raw
	}
	if raw := r.URL.Query().Get("price_type"); raw != "" {
		in.PriceType = &raw
	}

	options, count, err := h.svc.ListShippingOptions(ctx, in)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]optionDTO, 0, len(options))
	for i := range options {
		data = append(data, toOptionDTO(options[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:   data,
		Count:  count,
		Offset: page.Offset,
		Limit:  page.Limit,
	})
}

// getOption returns the option together with its rules.
func (h *Handler) getOption(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	option, err := h.svc.GetShippingOption(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toOptionDTO(option)})
}

// updateOptionRequest is the body of PATCH /admin/v1/shipping-options/{id}.
//
// provider_id and shipping_profile_id are ABSENT HERE; the rationale is in the
// [service.UpdateOptionInput] documentation.
type updateOptionRequest struct {
	Name      *string        `json:"name"`
	PriceType *string        `json:"price_type"`
	Amount    *int64         `json:"amount"`
	RegionID  *string        `json:"region_id"`
	IsReturn  *bool          `json:"is_return"`
	AdminOnly *bool          `json:"admin_only"`
	Data      map[string]any `json:"data"`
	Metadata  map[string]any `json:"metadata"`
}

// updateOption updates the given fields of the option.
func (h *Handler) updateOption(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body updateOptionRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	option, err := h.svc.UpdateShippingOption(ctx, chi.URLParam(r, "id"), service.UpdateOptionInput{
		Name:      body.Name,
		PriceType: body.PriceType,
		Amount:    body.Amount,
		RegionID:  body.RegionID,
		IsReturn:  body.IsReturn,
		AdminOnly: body.AdminOnly,
		Data:      body.Data,
		Metadata:  body.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toOptionDTO(option)})
}

// deleteOption soft deletes the option.
func (h *Handler) deleteOption(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.DeleteShippingOption(ctx, chi.URLParam(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}
