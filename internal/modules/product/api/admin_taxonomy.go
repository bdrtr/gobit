// The catalog's taxonomy: collections, categories and tags.
//
// The three are one subject rather than three because they have the same shape
// — a named record a product is filed under, created, listed and deleted the
// same way — and because the family is CLOSED: a product carries their ids, but
// no handler here calls a product, variant or link handler and none of those
// calls one of these. The layer under this one is a single file for the same
// reason (service/taxonomy.go).

package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// createCollectionRequest is the body of a collection request.
type createCollectionRequest struct {
	Title    string         `json:"title"`
	Handle   string         `json:"handle"`
	Metadata map[string]any `json:"metadata"`
}

// createCategoryRequest is the body of a category request.
type createCategoryRequest struct {
	Name        string  `json:"name"`
	Handle      string  `json:"handle"`
	Description *string `json:"description"`
	ParentID    *string `json:"parent_id"`
	IsActive    *bool   `json:"is_active"`
	IsInternal  bool    `json:"is_internal"`
	Rank        int32   `json:"rank"`
}

// createProductTypeRequest is the body of a product type request.
type createProductTypeRequest struct {
	Value    string         `json:"value"`
	Handle   string         `json:"handle"`
	Metadata map[string]any `json:"metadata"`
}

// createTagRequest is the body of a tag request.
type createTagRequest struct {
	Value string `json:"value"`
}

// adminCreateCollection POST /admin/v1/product-collections
func (h *Handler) adminCreateCollection(w http.ResponseWriter, r *http.Request) {
	req, err := decode[createCollectionRequest](w, r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	collection, err := h.svc.CreateCollection(r.Context(), service.CreateCollectionInput{
		Title:    req.Title,
		Handle:   req.Handle,
		Metadata: req.Metadata,
	})
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, collection)
}

// adminListCollections GET /admin/v1/product-collections
func (h *Handler) adminListCollections(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := paging(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	result, err := h.svc.ListCollections(r.Context(), limit, offset)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeList(w, r, result)
}

// adminCreateCategory POST /admin/v1/product-categories
func (h *Handler) adminCreateCategory(w http.ResponseWriter, r *http.Request) {
	req, err := decode[createCategoryRequest](w, r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	category, err := h.svc.CreateCategory(r.Context(), service.CreateCategoryInput{
		Name:        req.Name,
		Handle:      req.Handle,
		Description: req.Description,
		ParentID:    req.ParentID,
		IsActive:    req.IsActive,
		IsInternal:  req.IsInternal,
		Rank:        req.Rank,
	})
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, category)
}

// updateCategoryRequest is the body of a category PATCH.
//
// Every field is a pointer because absent and empty are different answers here:
// an absent field is left alone and a present one is written. ClearParent is
// the exception and it is a plain bool, because "make this a root" is a request
// a client either makes or does not.
type updateCategoryRequest struct {
	Name        *string `json:"name"`
	Handle      *string `json:"handle"`
	Description *string `json:"description"`
	ParentID    *string `json:"parent_id"`
	ClearParent bool    `json:"clear_parent"`
	IsActive    *bool   `json:"is_active"`
	IsInternal  *bool   `json:"is_internal"`
	Rank        *int32  `json:"rank"`
}

// adminUpdateCategory PATCH /admin/v1/product-categories/{id}
func (h *Handler) adminUpdateCategory(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	req, err := decode[updateCategoryRequest](w, r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	category, err := h.svc.UpdateCategory(r.Context(), id, service.UpdateCategoryInput{
		Name:        req.Name,
		Handle:      req.Handle,
		Description: req.Description,
		ParentID:    req.ParentID,
		ClearParent: req.ClearParent,
		IsActive:    req.IsActive,
		IsInternal:  req.IsInternal,
		Rank:        req.Rank,
	})
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, category)
}

// adminListCategories GET /admin/v1/product-categories
func (h *Handler) adminListCategories(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := paging(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	result, err := h.svc.ListCategories(r.Context(), service.ListCategoriesOptions{
		ParentID: stringParam(r, "parent_id"),
		Limit:    limit,
		Offset:   offset,
	})
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeList(w, r, result)
}

// adminCreateTag POST /admin/v1/product-tags
func (h *Handler) adminCreateTag(w http.ResponseWriter, r *http.Request) {
	req, err := decode[createTagRequest](w, r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	tag, err := h.svc.CreateTag(r.Context(), req.Value)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, tag)
}

// adminListTags GET /admin/v1/product-tags
func (h *Handler) adminListTags(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := paging(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	result, err := h.svc.ListTags(r.Context(), limit, offset)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeList(w, r, result)
}

// adminDeleteCollection DELETE /admin/v1/product-collections/{id}
//
// The response names only the collection. The products the delete released
// carry no line here on purpose: a DELETE answers about the thing it deleted,
// and a list of side effects on the response would be a second, unpaginated
// listing that grows with the catalog.
func (h *Handler) adminDeleteCollection(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	if err := h.svc.DeleteCollection(r.Context(), id); err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, deleted{ID: id, Object: "product_collection", Deleted: true})
}

// adminDeleteCategory DELETE /admin/v1/product-categories/{id}
//
// A category with subcategories comes back as 409; see
// [service.Service.DeleteCategory] for why the refusal is the answer rather
// than a rule that moves the children somewhere.
func (h *Handler) adminDeleteCategory(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	if err := h.svc.DeleteCategory(r.Context(), id); err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, deleted{ID: id, Object: "product_category", Deleted: true})
}

// adminDeleteTag DELETE /admin/v1/product-tags/{id}
func (h *Handler) adminDeleteTag(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	if err := h.svc.DeleteTag(r.Context(), id); err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, deleted{ID: id, Object: "product_tag", Deleted: true})
}

// adminCreateProductType POST /admin/v1/product-types
func (h *Handler) adminCreateProductType(w http.ResponseWriter, r *http.Request) {
	req, err := decode[createProductTypeRequest](w, r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}

	productType, err := h.svc.CreateProductType(r.Context(), service.CreateProductTypeInput{
		Value:    req.Value,
		Handle:   req.Handle,
		Metadata: req.Metadata,
	})
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	writeItem(w, r, http.StatusCreated, productType)
}

// adminListProductTypes GET /admin/v1/product-types
func (h *Handler) adminListProductTypes(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := paging(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}

	result, err := h.svc.ListProductTypes(r.Context(), limit, offset)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	writeList(w, r, result)
}

// adminDeleteProductType DELETE /admin/v1/product-types/{id}
//
// The products bound to the type are RELEASED in the same transaction; the
// answer is still the id, because the API's answer to a DELETE is what it
// deleted rather than a report about a side effect (see
// [service.Service.DeleteProductType]).
func (h *Handler) adminDeleteProductType(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	if err := h.svc.DeleteProductType(r.Context(), id); err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	writeItem(w, r, http.StatusOK, deleted{ID: id, Object: "product_type", Deleted: true})
}
