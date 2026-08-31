package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// createProductRequest yeni ürün isteğinin gövdesidir.
//
// Wire biçimi servis girdisinden AYRI tutulur: JSON alan adları dış
// sözleşmedir ve servisin iç alan adlarıyla birlikte sürüklenmemelidir.
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

// toInput istek gövdesini servis girdisine çevirir.
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
			Rank:     img.Rank,
			Metadata: img.Metadata,
		})
	}
	return in
}

// updateProductRequest ürün güncelleme isteğinin gövdesidir; verilmeyen alan
// değişmez.
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

// toInput istek gövdesini servis girdisine çevirir.
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

// createVariantRequest varyant isteğinin gövdesidir.
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

// toInput istek gövdesini servis girdisine çevirir.
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

// updateVariantRequest varyant güncelleme isteğinin gövdesidir.
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

// toInput istek gövdesini servis girdisine çevirir.
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

// createOptionRequest seçenek isteğinin gövdesidir.
type createOptionRequest struct {
	Title  string   `json:"title"`
	Values []string `json:"values"`
	Rank   int32    `json:"rank"`
}

// toInput istek gövdesini servis girdisine çevirir.
func (r createOptionRequest) toInput() service.CreateOptionInput {
	return service.CreateOptionInput{Title: r.Title, Values: r.Values, Rank: r.Rank}
}

// createImageRequest görsel isteğinin gövdesidir.
type createImageRequest struct {
	URL      string         `json:"url"`
	Rank     int32          `json:"rank"`
	Metadata map[string]any `json:"metadata"`
}

// optionValueRequest seçeneğe değer ekleme isteğidir.
type optionValueRequest struct {
	Value string `json:"value"`
}

// linkRequest bir varyantı başka modüldeki kayda bağlama isteğidir.
type linkRequest struct {
	PriceSetID      string `json:"price_set_id"`
	InventoryItemID string `json:"inventory_item_id"`
}

// salesChannelRequest ürünü bir satış kanalına bağlama isteğidir.
//
// [linkRequest]'e eklenmez: o bağlar VARYANT düzeyinde ve tekildir, bu bağ ÜRÜN
// düzeyinde ve çoktan çoğadır. Tek bir gövde tipini paylaşmaları, bir ucun
// diğerinin alanını sessizce yok saymasına izin verirdi.
type salesChannelRequest struct {
	SalesChannelID string `json:"sales_channel_id"`
}

// productSalesChannels ürünün satış kanalı bağlarının yanıt gövdesidir.
//
// Yanıt, tek bir bağı değil GÜNCEL LİSTEYİ döner: bağ çoktan çoğa olduğu için
// istemcinin asıl merak ettiği şey "hangi kanallardayım" sorusunun cevabıdır ve
// ikinci bir GET gerektirmemelidir.
type productSalesChannels struct {
	ProductID       string   `json:"product_id"`
	SalesChannelIDs []string `json:"sales_channel_ids"`
}

// createCollectionRequest koleksiyon isteğinin gövdesidir.
type createCollectionRequest struct {
	Title    string         `json:"title"`
	Handle   string         `json:"handle"`
	Metadata map[string]any `json:"metadata"`
}

// createCategoryRequest kategori isteğinin gövdesidir.
type createCategoryRequest struct {
	Name        string  `json:"name"`
	Handle      string  `json:"handle"`
	Description *string `json:"description"`
	ParentID    *string `json:"parent_id"`
	IsActive    *bool   `json:"is_active"`
	IsInternal  bool    `json:"is_internal"`
	Rank        int32   `json:"rank"`
}

// createTagRequest etiket isteğinin gövdesidir.
type createTagRequest struct {
	Value string `json:"value"`
}

// deleted silme yanıtının gövdesidir.
//
// Boş bir 204 yerine silinen kaydın kimliğini döndürmek, yeniden denenen bir
// silme isteğinde istemcinin neyin silindiğini görmesini sağlar.
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
	withRelations, err := boolParam(r, "expand")
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
	writeList(w, r, service.ListResult[models.Option]{
		Items:  options,
		Count:  len(options),
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

// adminSetPriceSet PUT /admin/v1/variants/{id}/price-set
//
// Bağın KURULDUĞU yer burasıdır: fiyat kümesini pricing modülü üretir, bağı
// katalog kurar. Uç PUT'tur çünkü işlem idempotenttir — aynı kümeyi ikinci kez
// bağlamak aynı sonucu verir.
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
// Uç POST'tur çünkü bağ ÇOKTAN ÇOĞADIR: istek bir kaynağı değiştirmez, bir
// koleksiyona üye ekler. Fiyat/stok uçlarının PUT'u burada yanlış olurdu — PUT
// "bu ucun tamamı budur" der ve ürünün diğer kanal bağlarını silmek zorunda
// kalırdı.
//
// Yanıt 201 DEĞİL 200'dür: link servisi aynı çifti ikinci kez bağlamayı no-op
// sayar (idempotent), dolayısıyla her istek yeni bir kayıt yaratmaz ve 201
// yaratılmamış bir kaynağı bildirirdi.
func (h *Handler) adminAddSalesChannel(w http.ResponseWriter, r *http.Request) {
	productID, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	req, err := decode[salesChannelRequest](w, r)
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
// Kanal kimliği gövdede değil YOLDA taşınır: kaldırılan şey ürün ile kanal
// arasındaki bağdır ve o bağın adresi budur; DELETE gövdesi ise ara katmanlar
// tarafından atılabildiği için güvenilir bir taşıyıcı değildir.
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

// adminListSalesChannels GET /admin/v1/products/{id}/sales-channels
func (h *Handler) adminListSalesChannels(w http.ResponseWriter, r *http.Request) {
	productID, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	h.writeSalesChannels(w, r, productID)
}

// writeSalesChannels ürünün güncel satış kanalı bağlarını yanıtlar.
func (h *Handler) writeSalesChannels(w http.ResponseWriter, r *http.Request, productID string) {
	ids, err := h.svc.ProductSalesChannelIDs(r.Context(), productID)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	if ids == nil {
		// JSON'da "null" değil "[]" dönmeli; istemci alanı her zaman dizi
		// sayabilmelidir (writeList ile aynı gerekçe).
		ids = []string{}
	}
	writeItem(w, r, http.StatusOK, productSalesChannels{ProductID: productID, SalesChannelIDs: ids})
}

// writeVariantLinks varyantın güncel bağlarını yanıtlar.
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
