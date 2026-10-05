package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// The addresses of a product's add-ons (ADR 0228).
const (
	// pathProductAddOns reads and replaces a product's add-on list on the
	// admin surface.
	pathProductAddOns = "/admin/v1/products/{id}/add-ons"
	// pathStoreAddOns reads it on the storefront, under the channel segment
	// like every other catalog read (ADR 0044), and whole for pathStoreRelated's
	// reason.
	pathStoreAddOns = "/store/v1/sales-channels/{sales_channel_id}/products/{id}/add-ons"
)

// addOnsDTO is a product's add-on list, in the operator's order.
type addOnsDTO struct {
	// VariantIDs are the variants a line of the product may carry as add-ons.
	VariantIDs []string `json:"variant_ids"`
}

// setAddOnsRequest is the body of PUT /admin/v1/products/{id}/add-ons.
type setAddOnsRequest struct {
	// VariantIDs is the whole list, in the order the storefront shows it; an
	// empty list takes every add-on off.
	VariantIDs []string `json:"variant_ids"`
}

// adminListAddOns returns a product's add-ons
// (GET /admin/v1/products/{id}/add-ons).
func (h *Handler) adminListAddOns(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	ids, err := h.svc.ProductAddOns(r.Context(), id)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, addOnsDTO{VariantIDs: ids})
}

// adminSetAddOns replaces a product's add-ons
// (PUT /admin/v1/products/{id}/add-ons).
//
// It is PUT because the body is the whole list, the order included, as a
// relation list's is (ADR 0180).
func (h *Handler) adminSetAddOns(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	req, err := decode[setAddOnsRequest](w, r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	ids, err := h.svc.SetProductAddOns(r.Context(), id, req.VariantIDs)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, addOnsDTO{VariantIDs: ids})
}

// storeAddOns returns a product's add-ons as the storefront shows them
// (GET /store/v1/sales-channels/{sales_channel_id}/products/{id}/add-ons).
func (h *Handler) storeAddOns(w http.ResponseWriter, r *http.Request) {
	channels, err := storeChannelScope(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	addOns, err := h.svc.StoreProductAddOns(r.Context(), id, channels)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	if addOns == nil {
		addOns = []service.StoreAddOn{}
	}
	// The body is a function of the URL alone (ADR 0044), so it may be reused;
	// how long and by whom is the installation's (ADR 0151).
	h.writeCatalog(w, r, itemEnvelope{Data: addOns})
}
