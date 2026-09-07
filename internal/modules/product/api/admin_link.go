// The links a product or a variant has to records OUTSIDE this module: the
// price set (pricing), the inventory item (inventory), the sales channels
// (auth) and the upload an image was made from (file).
//
// It is its own file because it is the part of the admin surface whose bodies
// carry FOREIGN ids — ids this module neither issues nor verifies — and whose
// handlers do nothing but establish, remove and read back a binding. Holding
// that boundary in one file is what keeps it visible; the layer under this one
// is split along the same line (service/links.go).

package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/models"
)

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
