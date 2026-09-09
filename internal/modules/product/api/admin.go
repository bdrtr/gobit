package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// createProductRequest is the body of a new-product request.
//
// The wire shape is kept SEPARATE from the service input: JSON field names are
// the outer contract and must not be dragged along with the service's internal
// field names.
type createProductRequest struct {
	Handle        string                 `json:"handle"`
	Title         string                 `json:"title"`
	Subtitle      *string                `json:"subtitle"`
	Description   *string                `json:"description"`
	Thumbnail     *string                `json:"thumbnail"`
	Status        string                 `json:"status"`
	IsGiftcard    bool                   `json:"is_giftcard"`
	Discountable  *bool                  `json:"discountable"`
	Weight        *int32                 `json:"weight"`
	Length        *int32                 `json:"length"`
	Height        *int32                 `json:"height"`
	Width         *int32                 `json:"width"`
	Material      *string                `json:"material"`
	OriginCountry *string                `json:"origin_country"`
	CollectionID  *string                `json:"collection_id"`
	TypeID        *string                `json:"type_id"`
	Metadata      map[string]any         `json:"metadata"`
	Options       []createOptionRequest  `json:"options"`
	Variants      []createVariantRequest `json:"variants"`
	Images        []createImageRequest   `json:"images"`
	TagIDs        []string               `json:"tag_ids"`
	CategoryIDs   []string               `json:"category_ids"`
}

// toInput converts the request body into the service input.
func (r createProductRequest) toInput() service.CreateProductInput {
	in := service.CreateProductInput{
		Handle:        r.Handle,
		Title:         r.Title,
		Subtitle:      r.Subtitle,
		Description:   r.Description,
		Thumbnail:     r.Thumbnail,
		Status:        models.Status(r.Status),
		IsGiftcard:    r.IsGiftcard,
		Discountable:  r.Discountable,
		Weight:        r.Weight,
		Length:        r.Length,
		Height:        r.Height,
		Width:         r.Width,
		Material:      r.Material,
		OriginCountry: r.OriginCountry,
		CollectionID:  r.CollectionID,
		TypeID:        r.TypeID,
		Metadata:      r.Metadata,
		TagIDs:        r.TagIDs,
		CategoryIDs:   r.CategoryIDs,
	}
	for _, opt := range r.Options {
		in.Options = append(in.Options, opt.toInput())
	}
	for _, variant := range r.Variants {
		in.Variants = append(in.Variants, variant.toInput())
	}
	for _, img := range r.Images {
		in.Images = append(in.Images, service.CreateImageInput{
			URL:      img.URL,
			UploadID: img.UploadID,
			AltText:  img.AltText,
			Rank:     img.Rank,
			Metadata: img.Metadata,
		})
	}
	return in
}

// updateProductRequest is the body of a product update request; a field that
// is not given does not change.
type updateProductRequest struct {
	Handle        *string        `json:"handle"`
	Title         *string        `json:"title"`
	Subtitle      *string        `json:"subtitle"`
	Description   *string        `json:"description"`
	Thumbnail     *string        `json:"thumbnail"`
	Status        *string        `json:"status"`
	Discountable  *bool          `json:"discountable"`
	Weight        *int32         `json:"weight"`
	Length        *int32         `json:"length"`
	Height        *int32         `json:"height"`
	Width         *int32         `json:"width"`
	Material      *string        `json:"material"`
	OriginCountry *string        `json:"origin_country"`
	CollectionID  *string        `json:"collection_id"`
	TypeID        *string        `json:"type_id"`
	Metadata      map[string]any `json:"metadata"`
	TagIDs        []string       `json:"tag_ids"`
	CategoryIDs   []string       `json:"category_ids"`
}

// toInput converts the request body into the service input.
func (r updateProductRequest) toInput() service.UpdateProductInput {
	in := service.UpdateProductInput{
		Handle:        r.Handle,
		Title:         r.Title,
		Subtitle:      r.Subtitle,
		Description:   r.Description,
		Thumbnail:     r.Thumbnail,
		Discountable:  r.Discountable,
		Weight:        r.Weight,
		Length:        r.Length,
		Height:        r.Height,
		Width:         r.Width,
		Material:      r.Material,
		OriginCountry: r.OriginCountry,
		CollectionID:  r.CollectionID,
		TypeID:        r.TypeID,
		Metadata:      r.Metadata,
		TagIDs:        r.TagIDs,
		CategoryIDs:   r.CategoryIDs,
	}
	if r.Status != nil {
		status := models.Status(*r.Status)
		in.Status = &status
	}
	return in
}

// createImageRequest is the body of an image request.
type createImageRequest struct {
	URL string `json:"url"`
	// UploadID names the upload record the image was made from; it may be left
	// out.
	//
	// The client that uploaded the file receives the id and the address in the
	// SAME response (POST /admin/v1/uploads) and sends both back here. The
	// address alone would leave the image unable to say which file it shows —
	// the record behind it, with the detected content type, the size and the
	// checksum, is reachable only through this id.
	UploadID string `json:"upload_id"`
	// AltText is what the picture shows, for a reader who cannot see it.
	//
	// It may be left out, and an empty value is a real answer rather than a
	// missing one: HTML gives alt="" the meaning "this image carries no
	// information", which is what a decorative picture is.
	AltText  string         `json:"alt_text"`
	Rank     int32          `json:"rank"`
	Metadata map[string]any `json:"metadata"`
}

// deleted is the body of a deletion response.
//
// Returning the id of the deleted record instead of an empty 204 lets the
// client see what was deleted on a retried deletion request.
type deleted struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Deleted bool   `json:"deleted"`
}

// adminCreateProduct POST /admin/v1/products
func (h *Handler) adminCreateProduct(w http.ResponseWriter, r *http.Request) {
	req, err := decode[createProductRequest](w, r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	product, err := h.svc.CreateProduct(r.Context(), req.toInput())
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, product)
}

// adminListProducts GET /admin/v1/products
func (h *Handler) adminListProducts(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := paging(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	withRelations, err := boolParam(r, "expand", false)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	after, err := afterParam(r, service.ProductListing, offset)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	opts := service.ListProductsOptions{
		CollectionID:  stringParam(r, "collection_id"),
		Handle:        stringParam(r, "handle"),
		Search:        stringParam(r, "q"),
		Limit:         limit,
		Offset:        offset,
		After:         after,
		WithRelations: withRelations,
	}
	if raw := stringParam(r, "status"); raw != nil {
		status := models.Status(*raw)
		opts.Status = &status
	}

	result, err := h.svc.ListProducts(r.Context(), opts)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeList(w, r, result)
}

// adminGetProduct GET /admin/v1/products/{id}
func (h *Handler) adminGetProduct(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	product, err := h.svc.GetProduct(r.Context(), id)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, product)
}

// adminUpdateProduct PATCH /admin/v1/products/{id}
func (h *Handler) adminUpdateProduct(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	req, err := decode[updateProductRequest](w, r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	product, err := h.svc.UpdateProduct(r.Context(), id, req.toInput())
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, product)
}

// updateImageRequest is the body of PATCH /admin/v1/products/{id}/images/{imageId}.
//
// Every field is a POINTER, which is the PATCH contract: a field left out does
// not change. `url` is not among them — the address and the upload binding were
// written together and moving one alone would put them at odds; replacing the
// picture is a new image plus the removal of the old one.
type updateImageRequest struct {
	// AltText is the text a screen reader reads out. An empty STRING clears it
	// on purpose, which is HTML's own word for a decorative image; leaving the
	// field out is what means "do not change it".
	AltText *string `json:"alt_text"`
	// Rank is the image's position among the product's images.
	Rank *int32 `json:"rank"`
	// Metadata REPLACES the whole bag when it is given.
	Metadata map[string]any `json:"metadata"`
}

// toInput converts the body into the service input.
func (r updateImageRequest) toInput() service.UpdateImageInput {
	return service.UpdateImageInput{
		AltText:  r.AltText,
		Rank:     r.Rank,
		Metadata: r.Metadata,
	}
}

// adminAddProductImage POST /admin/v1/products/{id}/images
func (h *Handler) adminAddProductImage(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	req, err := decode[createImageRequest](w, r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	image, err := h.svc.AddProductImage(r.Context(), id, service.CreateImageInput{
		URL:      req.URL,
		UploadID: req.UploadID,
		AltText:  req.AltText,
		Rank:     req.Rank,
		Metadata: req.Metadata,
	})
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, image)
}

// adminUpdateProductImage PATCH /admin/v1/products/{id}/images/{imageId}
//
// It is the endpoint ADR 0104 left missing: `alt_text` was published and could
// not be corrected, so a wrong description of a picture lasted as long as the
// product did.
func (h *Handler) adminUpdateProductImage(w http.ResponseWriter, r *http.Request) {
	productID, imageID, err := imagePath(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	req, err := decode[updateImageRequest](w, r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	image, err := h.svc.UpdateProductImage(r.Context(), productID, imageID, req.toInput())
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, image)
}

// adminRemoveProductImage DELETE /admin/v1/products/{id}/images/{imageId}
func (h *Handler) adminRemoveProductImage(w http.ResponseWriter, r *http.Request) {
	productID, imageID, err := imagePath(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	if err := h.svc.RemoveProductImage(r.Context(), productID, imageID); err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	// The module's own delete answer, not a 204: every other delete here says
	// what it deleted, and one endpoint answering differently would make the
	// client generator produce two shapes for one intent.
	writeItem(w, r, http.StatusOK, deleted{ID: imageID, Object: "product_image", Deleted: true})
}

// imagePath reads the product's id and the image's from the route.
//
// The two are read TOGETHER because they travel together: every handler below
// needs both, and a handler that took only one of them would be addressing an
// image without saying whose it is.
func imagePath(r *http.Request) (productID, imageID string, err error) {
	productID, err = pathParam(r, "id")
	if err != nil {
		return "", "", err
	}
	imageID, err = pathParam(r, "imageId")
	if err != nil {
		return "", "", err
	}

	return productID, imageID, nil
}

// adminDeleteProduct DELETE /admin/v1/products/{id}
func (h *Handler) adminDeleteProduct(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	if err := h.svc.DeleteProduct(r.Context(), id); err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, deleted{ID: id, Object: "product", Deleted: true})
}
