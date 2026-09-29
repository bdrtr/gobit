package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/models"
)

// pathVariantBundle reads and replaces what a bundle variant is made of
// (ADR 0234).
const pathVariantBundle = "/admin/v1/variants/{id}/bundle"

// bundleDTO is a variant's composition as a bundle, in the operator's order.
type bundleDTO struct {
	// Components are the variants one unit of the bundle holds; empty for a
	// variant that is no bundle.
	Components []models.BundleComponent `json:"components"`
}

// setBundleRequest is the body of PUT /admin/v1/variants/{id}/bundle.
type setBundleRequest struct {
	// Components is the whole composition, in the order the storefront shows
	// it; an empty list makes the variant a plain one again.
	Components []models.BundleComponent `json:"components"`
}

// adminGetBundle returns what a variant is made of
// (GET /admin/v1/variants/{id}/bundle).
func (h *Handler) adminGetBundle(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	components, err := h.svc.VariantBundle(r.Context(), id)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, bundleDTO{Components: components})
}

// adminSetBundle replaces what a variant is made of
// (PUT /admin/v1/variants/{id}/bundle).
//
// It is PUT because the body is the whole composition, the order included, as
// an add-on list's is (ADR 0228). It is a revision of the variant's product and
// takes If-Match, as the variant's own update does (ADR 0222).
func (h *Handler) adminSetBundle(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	req, err := decode[setBundleRequest](w, r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	components, err := h.svc.SetVariantBundle(r.Context(), id, req.Components)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, bundleDTO{Components: components})
}
