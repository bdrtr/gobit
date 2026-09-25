package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// The addresses of a product's relations (ADR 0180).
const (
	// pathProductRelations lists a product's relations on the admin surface.
	pathProductRelations = "/admin/v1/products/{id}/relations"
	// pathProductRelationsOfType replaces one kind of them.
	pathProductRelationsOfType = "/admin/v1/products/{id}/relations/{type}"
	// pathStoreRelated reads one kind of them on the storefront, under the
	// channel segment like every other catalog read (ADR 0044).
	pathStoreRelated = pathStoreProduct + "/related"
)

// relationsDTO is a product's relations: every kind, each in the operator's
// order. Every key is always present, an empty list included.
type relationsDTO struct {
	// CrossSell is what goes with the product.
	CrossSell []string `json:"cross_sell"`
	// UpSell is the better one.
	UpSell []string `json:"up_sell"`
	// Substitute is what to buy instead.
	Substitute []string `json:"substitute"`
}

// toRelationsDTO turns the service's map into the body.
func toRelationsDTO(relations map[models.RelationType][]string) relationsDTO {
	return relationsDTO{
		CrossSell:  relations[models.RelationCrossSell],
		UpSell:     relations[models.RelationUpSell],
		Substitute: relations[models.RelationSubstitute],
	}
}

// setRelationsRequest is the body of PUT /admin/v1/products/{id}/relations/{type}.
type setRelationsRequest struct {
	// ProductIDs is the kind's whole list, in the order the storefront shows
	// it; an empty list takes the kind off.
	ProductIDs []string `json:"product_ids"`
}

// adminListRelations returns a product's relations
// (GET /admin/v1/products/{id}/relations).
func (h *Handler) adminListRelations(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	relations, err := h.svc.ProductRelations(r.Context(), id)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toRelationsDTO(relations))
}

// adminSetRelations replaces one kind of a product's relations
// (PUT /admin/v1/products/{id}/relations/{type}).
//
// It is PUT because the body is the kind's whole list: the order is part of it,
// and adding or removing one entry is sending the list again.
func (h *Handler) adminSetRelations(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	req, err := decode[setRelationsRequest](w, r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	relations, err := h.svc.SetProductRelations(r.Context(), id,
		models.RelationType(chi.URLParam(r, "type")), req.ProductIDs)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toRelationsDTO(relations))
}

// storeRelatedProducts returns one kind of a product's relations as storefront
// products (GET /store/v1/sales-channels/{sales_channel_id}/products/{id}/related).
//
// The kind is required: a product page shows each kind in a widget of its own,
// and every product in the answer is enriched with its prices and stock, so
// the three kinds at once would triple the read for a page that wanted one.
func (h *Handler) storeRelatedProducts(w http.ResponseWriter, r *http.Request) {
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

	products, err := h.svc.StoreRelatedProducts(r.Context(), id,
		models.RelationType(r.URL.Query().Get("type")), channels)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	if products == nil {
		products = []service.StoreProduct{}
	}
	// The body is a function of the URL alone (ADR 0044), so it may be reused;
	// how long and by whom is the installation's (ADR 0151).
	h.allowCaching(w)

	writeItem(w, r, http.StatusOK, products)
}
