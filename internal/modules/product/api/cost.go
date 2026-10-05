package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/models"
)

// pathVariantCosts reads and replaces what one unit of a variant costs the
// shop, per currency (ADR 0401).
const pathVariantCosts = "/admin/v1/variants/{id}/costs"

// variantCostsDTO is a variant's unit costs, in currency order.
type variantCostsDTO struct {
	// VariantID is the variant the costs are of.
	VariantID string `json:"variant_id"`
	// Costs are one entry per currency, net of tax, in minor units; empty for a
	// variant with none.
	Costs []models.VariantCost `json:"costs"`
}

// setVariantCostsRequest is the body of PUT /admin/v1/variants/{id}/costs.
type setVariantCostsRequest struct {
	// Costs is the whole list; an empty list clears every cost.
	Costs []models.VariantCost `json:"costs"`
}

// adminGetVariantCosts returns a variant's unit costs
// (GET /admin/v1/variants/{id}/costs).
func (h *Handler) adminGetVariantCosts(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	costs, err := h.svc.VariantCosts(r.Context(), id)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, variantCostsDTO{VariantID: id, Costs: costs})
}

// adminSetVariantCosts replaces a variant's unit costs
// (PUT /admin/v1/variants/{id}/costs).
//
// It is PUT because the body is the whole list, as an add-on list's is
// (ADR 0228). It is not a revision of the product: the costs are not in its
// view, so it takes no If-Match.
func (h *Handler) adminSetVariantCosts(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	req, err := decode[setVariantCostsRequest](w, r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	costs, err := h.svc.SetVariantCosts(r.Context(), id, req.Costs)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, variantCostsDTO{VariantID: id, Costs: costs})
}
