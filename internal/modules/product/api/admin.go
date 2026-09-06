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
	UploadID string         `json:"upload_id"`
	Rank     int32          `json:"rank"`
	Metadata map[string]any `json:"metadata"`
}

// optionValueRequest is the request that adds a value to an option.
type optionValueRequest struct {
	Value string `json:"value"`
}

// linkRequest is the request that links a variant to a record in another
// module.
type linkRequest struct {
	PriceSetID      string `json:"price_set_id"`
	InventoryItemID string `json:"inventory_item_id"`
}

// linkSalesChannelRequest is the request that LINKS a product to a sales
// channel.
//
// Its name deliberately starts with "link": the auth module has a
// salesChannelRequest as well and that one creates the channel ITSELF. The two
// types are two separate things, but had the Go names been the same they would
// ask for the same component name in the published schema too
// ("SalesChannelRequest") and documentation generation would fall over
// COMPLETELY with a collision error — not just that endpoint, the whole of
// /openapi.json.
//
// The component name is the published contract; a coincidence of Go naming is
// not allowed to decide it.
//
// It is not added to [linkRequest]: those links are at the VARIANT level and
// singular, this link is at the PRODUCT level and many-to-many. Sharing a
// single body type would let one endpoint silently ignore the other's field.
type linkSalesChannelRequest struct {
	SalesChannelID string `json:"sales_channel_id"`
}

// productSalesChannels is the response body of a product's sales channel links.
//
// The response returns not a single link but the CURRENT LIST: because the link
// is many-to-many, what the client really wonders is the answer to "which
// channels am I in", and that must not need a second GET.
type productSalesChannels struct {
	ProductID       string   `json:"product_id"`
	SalesChannelIDs []string `json:"sales_channel_ids"`
}

// uploadImages is the response body of "which images use this upload".
//
// The upload id is echoed for the reason [productSalesChannels] echoes the
// product id: the body then stands on its own in a log or a cache, without the
// request URL next to it.
//
// The images are returned WHOLE rather than as ids. A bare id list would be
// enough for the yes/no question ("is this file in use") and useless for the
// next one an operator immediately asks — WHERE it is used — because resolving
// each id would need one more request per image. Each record already carries
// its product_id and its address.
type uploadImages struct {
	UploadID string         `json:"upload_id"`
	Images   []models.Image `json:"images"`
}

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

// createTagRequest is the body of a tag request.
type createTagRequest struct {
	Value string `json:"value"`
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

// adminSetPriceSet PUT /admin/v1/variants/{id}/price-set
//
// This is where the link is ESTABLISHED: the price set is produced by the
// pricing module, the link is established by the catalog. The endpoint is a PUT
// because the operation is idempotent — linking the same set a second time
// gives the same result.
func (h *Handler) adminSetPriceSet(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	req, err := decode[linkRequest](w, r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	if err := h.svc.SetVariantPriceSet(r.Context(), id, req.PriceSetID); err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	h.writeVariantLinks(w, r, id)
}

// adminDeletePriceSet DELETE /admin/v1/variants/{id}/price-set
func (h *Handler) adminDeletePriceSet(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	if err := h.svc.ClearVariantPriceSet(r.Context(), id); err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, deleted{ID: id, Object: "variant_price_set_link", Deleted: true})
}

// adminSetInventoryItem PUT /admin/v1/variants/{id}/inventory-item
func (h *Handler) adminSetInventoryItem(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	req, err := decode[linkRequest](w, r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	if err := h.svc.SetVariantInventoryItem(r.Context(), id, req.InventoryItemID); err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	h.writeVariantLinks(w, r, id)
}

// adminDeleteInventoryItem DELETE /admin/v1/variants/{id}/inventory-item
func (h *Handler) adminDeleteInventoryItem(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	if err := h.svc.ClearVariantInventoryItem(r.Context(), id); err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, deleted{ID: id, Object: "variant_inventory_link", Deleted: true})
}

// adminGetVariantLinks GET /admin/v1/variants/{id}/links
func (h *Handler) adminGetVariantLinks(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	h.writeVariantLinks(w, r, id)
}

// adminAddSalesChannel POST /admin/v1/products/{id}/sales-channels
//
// The endpoint is a POST because the link is MANY-TO-MANY: the request does not
// change a resource, it adds a member to a collection. The PUT of the
// price/stock endpoints would be wrong here — PUT says "this is the whole of
// this endpoint" and would have to delete the product's other channel links.
//
// The response is 200, NOT 201: the link service counts linking the same pair a
// second time as a no-op (idempotent), so not every request creates a new
// record and a 201 would report a resource that was never created.
func (h *Handler) adminAddSalesChannel(w http.ResponseWriter, r *http.Request) {
	productID, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	req, err := decode[linkSalesChannelRequest](w, r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	if err := h.svc.AddProductSalesChannel(r.Context(), productID, req.SalesChannelID); err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	h.writeSalesChannels(w, r, productID)
}

// adminRemoveSalesChannel DELETE /admin/v1/products/{id}/sales-channels/{sales_channel_id}
//
// The channel id is carried IN THE PATH, not in the body: what is removed is
// the link between the product and the channel and this is that link's address;
// a DELETE body, on the other hand, is not a reliable carrier because
// intermediaries may drop it.
func (h *Handler) adminRemoveSalesChannel(w http.ResponseWriter, r *http.Request) {
	productID, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	channelID, err := pathParam(r, "sales_channel_id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	if err := h.svc.RemoveProductSalesChannel(r.Context(), productID, channelID); err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	h.writeSalesChannels(w, r, productID)
}

// adminListImagesOfUpload GET /admin/v1/product-images/by-upload/{upload_id}
//
// It answers "which product images use this file", which is the question an
// operator has before deleting an upload: the delete endpoint of the file
// module cannot ask it, because that module can neither see nor import the
// catalog.
//
// # Why the upload id is in the PATH and not in the query string
//
// It is not a filter, it is the address of what is being asked about — the
// endpoint has no meaning without it. Every query parameter in this module is
// optional by convention (see queryParameter), and a required one would be the
// single exception a client cannot see in the document.
//
// # Why it is not under /admin/v1/uploads/{id}
//
// That path belongs to the file module and the catalog does not write routes
// into another module's namespace: in an installation without the file module
// the prefix would exist with only this one endpoint under it, which reads as a
// broken uploads API rather than as a catalog endpoint.
func (h *Handler) adminListImagesOfUpload(w http.ResponseWriter, r *http.Request) {
	uploadID, err := pathParam(r, "upload_id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	images, err := h.svc.ImagesOfUpload(r.Context(), uploadID)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	if images == nil {
		// It has to be "[]" in JSON, not "null": the client must be able to
		// treat the field as an array every time (the same reasoning as
		// writeList).
		images = []models.Image{}
	}

	writeItem(w, r, http.StatusOK, uploadImages{UploadID: uploadID, Images: images})
}

// adminListSalesChannels GET /admin/v1/products/{id}/sales-channels
func (h *Handler) adminListSalesChannels(w http.ResponseWriter, r *http.Request) {
	productID, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	h.writeSalesChannels(w, r, productID)
}

// writeSalesChannels responds with the product's current sales channel links.
func (h *Handler) writeSalesChannels(w http.ResponseWriter, r *http.Request, productID string) {
	ids, err := h.svc.ProductSalesChannelIDs(r.Context(), productID)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	if ids == nil {
		// It has to return "[]" in JSON, not "null"; the client must be able to
		// treat the field as an array every time (the same reasoning as
		// writeList).
		ids = []string{}
	}
	writeItem(w, r, http.StatusOK, productSalesChannels{ProductID: productID, SalesChannelIDs: ids})
}

// writeVariantLinks responds with the variant's current links.
func (h *Handler) writeVariantLinks(w http.ResponseWriter, r *http.Request, variantID string) {
	links, err := h.svc.VariantLinkIDs(r.Context(), variantID)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, links)
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
