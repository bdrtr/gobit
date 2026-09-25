package api

import (
	"net/http"
	"time"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// pathProductSchedule is the moment a draft is published (ADR 0177).
const pathProductSchedule = "/admin/v1/products/{id}/schedule"

// adminProduct is a product as the admin surface answers it: the record, and the
// moment it is scheduled to be published, which only this surface publishes
// (ADR 0177).
//
// The schedule is kept out of [models.Product]'s own JSON because the storefront
// answers with a type that embeds it; a launch date is the merchant's, and an
// unannounced product's date is exactly what a shopper should not be able to read.
type adminProduct struct {
	models.Product
	// PublishAt is the moment the draft is published; absent when it has none.
	PublishAt *time.Time `json:"publish_at,omitempty"`
}

// toAdminProduct adds the schedule to the product.
func toAdminProduct(product models.Product) adminProduct {
	return adminProduct{Product: product, PublishAt: product.PublishAt}
}

// toAdminProducts does the same to a page.
func toAdminProducts(page service.ListResult[models.Product]) service.ListResult[adminProduct] {
	out := service.ListResult[adminProduct]{
		Items:      make([]adminProduct, 0, len(page.Items)),
		Count:      page.Count,
		Offset:     page.Offset,
		Limit:      page.Limit,
		NextCursor: page.NextCursor,
	}
	for i := range page.Items {
		out.Items = append(out.Items, toAdminProduct(page.Items[i]))
	}

	return out
}

// scheduleRequest is the body of PUT /admin/v1/products/{id}/schedule.
type scheduleRequest struct {
	// PublishAt is the moment the draft is published: RFC 3339 with a zone, in
	// the future.
	PublishAt time.Time `json:"publish_at"`
}

// adminScheduleProduct sets the moment a draft is published
// (PUT /admin/v1/products/{id}/schedule).
//
// It is PUT because it replaces the schedule: a second call moves the moment
// rather than adding one.
func (h *Handler) adminScheduleProduct(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	req, err := decode[scheduleRequest](w, r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	product, err := h.svc.SchedulePublication(r.Context(), id, req.PublishAt)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toAdminProduct(product))
}

// adminCancelSchedule takes the schedule off a product
// (DELETE /admin/v1/products/{id}/schedule).
//
// It answers with the product rather than an empty 204, because what the
// operator wants to see next is the product as it now stands — still a draft,
// with no moment.
func (h *Handler) adminCancelSchedule(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	product, err := h.svc.CancelPublication(r.Context(), id)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toAdminProduct(product))
}
