package api

import (
	"net/http"
	"time"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// paramVariantID names the saved variant in the wishlist's paths (ADR 0190).
const paramVariantID = "variant_id"

// wishlistItemDTO is the response body of one wishlist item.
type wishlistItemDTO struct {
	// CustomerID is the customer that saved the variant.
	CustomerID string `json:"customer_id"`
	// VariantID is the saved product variant.
	VariantID string `json:"variant_id"`
	// CreatedAt is when the variant was first saved.
	CreatedAt time.Time `json:"created_at"`
}

// toWishlistItemDTO converts the domain model into the response body.
func toWishlistItemDTO(item models.WishlistItem) wishlistItemDTO {
	return wishlistItemDTO{
		CustomerID: item.CustomerID,
		VariantID:  item.VariantID,
		CreatedAt:  item.CreatedAt,
	}
}

// storeListWishlist lists the proven customer's wishlist
// (GET /store/v1/customers/{id}/wishlist).
func (h *Handler) storeListWishlist(w http.ResponseWriter, r *http.Request) {
	customerID, err := h.storeCustomerID(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	h.listWishlist(w, r, customerID)
}

// storeSaveToWishlist saves a variant on the proven customer's wishlist
// (PUT /store/v1/customers/{id}/wishlist/{variant_id}).
//
// It is a PUT because it can be repeated: the variant is on the list after the
// first call and every later one, and the item keeps the moment it was first
// saved. There is no body; the path names everything.
func (h *Handler) storeSaveToWishlist(w http.ResponseWriter, r *http.Request) {
	customerID, err := h.storeCustomerID(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	item, err := h.svc.SaveToWishlist(r.Context(), customerID, pathParam(r, paramVariantID))
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toWishlistItemDTO(item))
}

// storeRemoveFromWishlist takes a variant off the proven customer's wishlist
// (DELETE /store/v1/customers/{id}/wishlist/{variant_id}).
func (h *Handler) storeRemoveFromWishlist(w http.ResponseWriter, r *http.Request) {
	customerID, err := h.storeCustomerID(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	if err := h.svc.RemoveFromWishlist(r.Context(), customerID, pathParam(r, paramVariantID)); err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	corehttp.WriteJSON(r.Context(), w, http.StatusNoContent, nil)
}

// adminListWishlist lists any customer's wishlist
// (GET /admin/v1/customers/{id}/wishlist).
func (h *Handler) adminListWishlist(w http.ResponseWriter, r *http.Request) {
	h.listWishlist(w, r, pathParam(r, paramID))
}

// listWishlist writes the customer's wishlist, which the cap keeps small enough
// to be one unpaged listing.
func (h *Handler) listWishlist(w http.ResponseWriter, r *http.Request, customerID string) {
	items, err := h.svc.ListWishlist(r.Context(), customerID)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItems(w, r, convertAll(items, toWishlistItemDTO))
}
