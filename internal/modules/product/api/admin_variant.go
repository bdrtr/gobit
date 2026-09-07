// Variants and options: the request bodies of the variant and option endpoints,
// and the handlers that serve them.
//
// The family stands on its own — none of these handlers calls, and none is
// called by, the product, link or taxonomy handlers, and they reach only the
// service's variant and option methods. The layer under this one is already
// split along the same line (service/variant.go).

package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// createVariantRequest is the body of a variant request.
type createVariantRequest struct {
	Title           string            `json:"title"`
	SKU             *string           `json:"sku"`
	Barcode         *string           `json:"barcode"`
	EAN             *string           `json:"ean"`
	UPC             *string           `json:"upc"`
	ManageInventory *bool             `json:"manage_inventory"`
	AllowBackorder  *bool             `json:"allow_backorder"`
	Weight          *int32            `json:"weight"`
	Rank            *int32            `json:"rank"`
	Metadata        map[string]any    `json:"metadata"`
	OptionValueIDs  []string          `json:"option_value_ids"`
	Options         map[string]string `json:"options"`
}

// toInput converts the request body into the service input.
func (r createVariantRequest) toInput() service.CreateVariantInput {
	return service.CreateVariantInput{
		Title:           r.Title,
		SKU:             r.SKU,
		Barcode:         r.Barcode,
		EAN:             r.EAN,
		UPC:             r.UPC,
		ManageInventory: r.ManageInventory,
		AllowBackorder:  r.AllowBackorder,
		Weight:          r.Weight,
		Rank:            r.Rank,
		Metadata:        r.Metadata,
		OptionValueIDs:  r.OptionValueIDs,
		Options:         r.Options,
	}
}

// updateVariantRequest is the body of a variant update request.
type updateVariantRequest struct {
	Title           *string        `json:"title"`
	SKU             *string        `json:"sku"`
	Barcode         *string        `json:"barcode"`
	EAN             *string        `json:"ean"`
	UPC             *string        `json:"upc"`
	ManageInventory *bool          `json:"manage_inventory"`
	AllowBackorder  *bool          `json:"allow_backorder"`
	Weight          *int32         `json:"weight"`
	Rank            *int32         `json:"rank"`
	Metadata        map[string]any `json:"metadata"`
	OptionValueIDs  []string       `json:"option_value_ids"`
}

// toInput converts the request body into the service input.
func (r updateVariantRequest) toInput() service.UpdateVariantInput {
	return service.UpdateVariantInput{
		Title:           r.Title,
		SKU:             r.SKU,
		Barcode:         r.Barcode,
		EAN:             r.EAN,
		UPC:             r.UPC,
		ManageInventory: r.ManageInventory,
		AllowBackorder:  r.AllowBackorder,
		Weight:          r.Weight,
		Rank:            r.Rank,
		Metadata:        r.Metadata,
		OptionValueIDs:  r.OptionValueIDs,
	}
}

// createOptionRequest is the body of an option request.
type createOptionRequest struct {
	Title  string   `json:"title"`
	Values []string `json:"values"`
	Rank   int32    `json:"rank"`
}

// toInput converts the request body into the service input.
func (r createOptionRequest) toInput() service.CreateOptionInput {
	return service.CreateOptionInput{Title: r.Title, Values: r.Values, Rank: r.Rank}
}

// optionValueRequest is the request that adds a value to an option.
type optionValueRequest struct {
	Value string `json:"value"`
}

// adminCreateVariant POST /admin/v1/products/{id}/variants
func (h *Handler) adminCreateVariant(w http.ResponseWriter, r *http.Request) {
	productID, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	req, err := decode[createVariantRequest](w, r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	variant, err := h.svc.CreateVariant(r.Context(), productID, req.toInput())
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, variant)
}

// adminListVariants GET /admin/v1/products/{id}/variants
func (h *Handler) adminListVariants(w http.ResponseWriter, r *http.Request) {
	productID, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	limit, offset, err := paging(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	result, err := h.svc.ListVariants(r.Context(), service.ListVariantsOptions{
		ProductID:        &productID,
		Limit:            limit,
		Offset:           offset,
		WithOptionValues: true,
	})
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeList(w, r, result)
}

// adminGetVariant GET /admin/v1/variants/{id}
func (h *Handler) adminGetVariant(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	variant, err := h.svc.GetVariant(r.Context(), id)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, variant)
}

// adminUpdateVariant PATCH /admin/v1/variants/{id}
func (h *Handler) adminUpdateVariant(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	req, err := decode[updateVariantRequest](w, r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	variant, err := h.svc.UpdateVariant(r.Context(), id, req.toInput())
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, variant)
}

// adminDeleteVariant DELETE /admin/v1/variants/{id}
func (h *Handler) adminDeleteVariant(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	if err := h.svc.DeleteVariant(r.Context(), id); err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, deleted{ID: id, Object: "variant", Deleted: true})
}

// adminCreateOption POST /admin/v1/products/{id}/options
func (h *Handler) adminCreateOption(w http.ResponseWriter, r *http.Request) {
	productID, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	req, err := decode[createOptionRequest](w, r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	option, err := h.svc.CreateOption(r.Context(), productID, req.toInput())
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, option)
}

// adminListOptions GET /admin/v1/products/{id}/options
func (h *Handler) adminListOptions(w http.ResponseWriter, r *http.Request) {
	productID, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	options, err := h.svc.ListOptions(r.Context(), productID)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	// The number is filled in HERE: the endpoint does not paginate, it writes
	// the whole result as if it were a single page. Leaving the counter empty
	// would mean "not counted" on a list that is not paginated — while the
	// number is right there in our hands.
	count := len(options)

	writeList(w, r, service.ListResult[models.Option]{
		Items:  options,
		Count:  &count,
		Offset: 0,
		Limit:  len(options),
	})
}

// adminAddOptionValue POST /admin/v1/product-options/{id}/values
func (h *Handler) adminAddOptionValue(w http.ResponseWriter, r *http.Request) {
	optionID, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	req, err := decode[optionValueRequest](w, r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	value, err := h.svc.AddOptionValue(r.Context(), optionID, req.Value)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, value)
}

// adminDeleteOption DELETE /admin/v1/product-options/{id}
func (h *Handler) adminDeleteOption(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	if err := h.svc.DeleteOption(r.Context(), id); err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, deleted{ID: id, Object: "product_option", Deleted: true})
}

// adminDeleteOptionValue DELETE /admin/v1/product-option-values/{id}
//
// The path is not nested under the option. A value's id is unique on its own
// and the nested form would let a caller name an option the value does not
// belong to, which is a mismatch this handler would then have to check and
// report — a whole error case bought for nothing.
func (h *Handler) adminDeleteOptionValue(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	if err := h.svc.DeleteOptionValue(r.Context(), id); err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, deleted{ID: id, Object: "product_option_value", Deleted: true})
}
