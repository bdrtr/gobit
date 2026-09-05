// Package api inventory modülünün HTTP yüzeyidir.
//
// Yalnızca yönetim (admin) route'ları vardır. STOK MAĞAZAYA DOĞRUDAN AÇILMAZ:
// müşteri tarafı stoğu ürün listelemesi üzerinden, Query katmanının
// "inventory_item" sağlayıcısıyla görür (bkz. service.QueryProvider). Böylece
// stok yüzeyi tek bir okuma yolundan geçer ve mağazaya lokasyon kırılımı gibi
// iç ayrıntılar sızmaz.
//
// Handler'lar status kodu SEÇMEZ: servis core/errors tipli hatasını döner,
// corehttp.WriteError sınıfına uygun kodu yazar (plan Bölüm 8).
//
// # Yetki
//
// /admin/v1 uçları yetki ister ve sözlük ikiye ayrılır: GET uçları [ScopeRead],
// POST/PUT/PATCH/DELETE uçları [ScopeWrite] (bkz. [Handler.Routes]).
// corehttp.ScopeAdmin ÜST YETKİDİR ve ikisini de tek başına karşılar.
//
// Modülün /store/v1 ucu YOKTUR, dolayısıyla yetkisiz bir yüzeyi de yoktur.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// Route yolları. Modül route'ları TAM YOL ile kaydedilir; "/admin/v1" gibi bir
// ön ek MOUNT EDİLMEZ, çünkü mount eden ilk modül o alt ağacın tamamını sahiplenir
// ve aynı ön eki kullanan diğer modüllerle çakışırdı.
const (
	pathStockLocations  = "/admin/v1/stock-locations"
	pathStockLocation   = "/admin/v1/stock-locations/{id}"
	pathItems           = "/admin/v1/inventory-items"
	pathItem            = "/admin/v1/inventory-items/{id}"
	pathItemLevels      = "/admin/v1/inventory-items/{id}/levels"
	pathItemLevelAdjust = "/admin/v1/inventory-items/{id}/levels/{location_id}/adjust"
)

// maxBodyBytes istek gövdesi için üst sınırdır. Sınır olmadan tek bir istek
// sunucunun belleğini tüketebilirdi.
const maxBodyBytes int64 = 1 << 20 // 1 MiB

// codeInvalidRequest gövde/parametre çözümlenemediğinde dönen hata kodudur.
const codeInvalidRequest = "inventory_invalid_request"

// Yetki sözlüğü: inventory'nin yönetim uçlarının istediği yetkiler.
//
// Sözlük tüm modüllerde AYNI biçimdedir ve BİLİNÇLİ olarak iki girdiden
// ibarettir: okuma ve yazma. Kaynak başına ayrı yetki ("stock-locations:write",
// "levels:read" …) tanımlamak listeyi büyütür ama bugün verilebilecek hiçbir
// yeni kararı mümkün kılmaz; ayrım gerçekten gerektiğinde eklenir.
const (
	// ScopeRead inventory yönetim yüzeyindeki OKUMA uçlarının istediği
	// yetkidir.
	//
	// Lokasyonları, stok kalemlerini ve seviyeleri okumaya yeter; hiçbir yazma
	// ucunu açmaz. Tam yetkili kimliklere ayrıca verilmesi gerekmez:
	// corehttp.ScopeAdmin taşıyan bir çağıran bunu da karşılar (bkz.
	// corehttp.Principal.HasScope).
	ScopeRead = "inventory:read"

	// ScopeWrite inventory yönetim yüzeyindeki YAZMA uçlarının istediği
	// yetkidir.
	//
	// Ayrım burada özellikle işe yarar: stoğu yalnızca RAPORLAYAN bir
	// entegrasyon (depo panosu, satış tahmini) [ScopeRead] ile çalışabilir ve
	// bir hata durumunda gerçek stoğu bozamaz.
	ScopeWrite = "inventory:write"
)

// Inventory handler'ların servisten ihtiyaç duyduğu yüzeydir.
//
// Dar tutulması testleri sadeleştirir: HTTP davranışı, gerçek bir veritabanı
// olmadan birkaç satırlık bir sahte ile doğrulanabilir.
type Inventory interface {
	// CreateStockLocation yeni bir stok lokasyonu oluşturur.
	CreateStockLocation(ctx context.Context, in service.CreateStockLocationInput) (models.StockLocation, error)
	// GetStockLocation lokasyonu kimliğiyle döner.
	GetStockLocation(ctx context.Context, id string) (models.StockLocation, error)
	// ListStockLocations lokasyonları sayfalar.
	ListStockLocations(ctx context.Context, page service.Page) ([]models.StockLocation, int64, error)

	// CreateInventoryItem yeni bir stok kalemi oluşturur.
	CreateInventoryItem(ctx context.Context, in service.CreateInventoryItemInput) (models.InventoryItem, error)
	// GetInventoryItem kalemi kimliğiyle döner.
	GetInventoryItem(ctx context.Context, id string) (models.InventoryItem, error)
	// ListInventoryItems kalemleri sayfalar.
	ListInventoryItems(ctx context.Context, in service.ListInventoryItemsInput) ([]models.InventoryItem, int64, error)
	// DeleteInventoryItem kalemi yumuşak siler.
	DeleteInventoryItem(ctx context.Context, id string) error

	// ListInventoryLevels kalemin stok seviyelerini döner.
	ListInventoryLevels(ctx context.Context, itemID string) ([]models.InventoryLevel, error)
	// SetInventoryLevel fiziksel adedi mutlak olarak yazar.
	SetInventoryLevel(ctx context.Context, itemID, locationID string, stockedQty int64) (models.InventoryLevel, error)
	// AdjustInventory fiziksel adedi delta kadar değiştirir.
	AdjustInventory(ctx context.Context, itemID, locationID string, delta int64) (models.InventoryLevel, error)
}

// Handler inventory modülünün HTTP handler kümesidir.
type Handler struct {
	svc Inventory
}

// NewHandler verilen servis üzerinde çalışan handler kümesini üretir.
func NewHandler(svc Inventory) *Handler {
	return &Handler{svc: svc}
}

// Routes modülün admin route'larını router'a bağlar.
//
// # KORUMA
//
// İki katman vardır ve ikisi de gereklidir:
//
//  1. KİMLİK — uçlar corehttp.RequireAdmin ile korunur. O middleware bu
//     modülde değil, router'ı kuran tarafta takılır (bkz. corehttp.APIGuards).
//  2. YETKİ — uçlar BURADA, uç uç corehttp.RequireScope ile işaretlenir:
//     GET uçları [ScopeRead], POST/DELETE uçları [ScopeWrite] ister.
//
// İkinci katman olmasaydı kimlik doğrulama yetkilendirmenin yerine geçerdi:
// yetkileri boşaltılmış bir yönetim kullanıcısı giriş yapıp stok seviyelerini
// yazabilir ya da kalemleri silebilirdi. Stok, satılabilirliği belirleyen
// sayıdır; yanlış yazılması doğrudan satış kaybı ya da fazla satıştır.
func (h *Handler) Routes(r chi.Router) {
	okuma := r.With(corehttp.RequireScope(ScopeRead))
	yazma := r.With(corehttp.RequireScope(ScopeWrite))

	yazma.Post(pathStockLocations, h.createStockLocation)
	okuma.Get(pathStockLocations, h.listStockLocations)
	okuma.Get(pathStockLocation, h.getStockLocation)

	yazma.Post(pathItems, h.createItem)
	okuma.Get(pathItems, h.listItems)
	okuma.Get(pathItem, h.getItem)
	yazma.Delete(pathItem, h.deleteItem)

	okuma.Get(pathItemLevels, h.listLevels)
	yazma.Post(pathItemLevels, h.setLevel)
	yazma.Post(pathItemLevelAdjust, h.adjustLevel)
}

// --- stok lokasyonları -------------------------------------------------------

// createStockLocationRequest POST /admin/v1/stock-locations gövdesidir.
type createStockLocationRequest struct {
	Name        string `json:"name"`
	Address1    string `json:"address_1"`
	Address2    string `json:"address_2"`
	City        string `json:"city"`
	Province    string `json:"province"`
	PostalCode  string `json:"postal_code"`
	CountryCode string `json:"country_code"`
}

// createStockLocation yeni bir stok lokasyonu oluşturur.
func (h *Handler) createStockLocation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createStockLocationRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	loc, err := h.svc.CreateStockLocation(ctx, service.CreateStockLocationInput{
		Name:        body.Name,
		Address1:    body.Address1,
		Address2:    body.Address2,
		City:        body.City,
		Province:    body.Province,
		PostalCode:  body.PostalCode,
		CountryCode: body.CountryCode,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toLocationDTO(loc)})
}

// getStockLocation lokasyonu kimliğiyle döner.
func (h *Handler) getStockLocation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	loc, err := h.svc.GetStockLocation(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toLocationDTO(loc)})
}

// listStockLocations stok lokasyonlarını sayfalayarak döner.
func (h *Handler) listStockLocations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	locations, count, err := h.svc.ListStockLocations(ctx, page)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]stockLocationDTO, 0, len(locations))
	for i := range locations {
		data = append(data, toLocationDTO(locations[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:   data,
		Count:  count,
		Offset: page.Offset,
		Limit:  page.Limit,
	})
}

// --- stok kalemleri ----------------------------------------------------------

// createItemRequest POST /admin/v1/inventory-items gövdesidir.
type createItemRequest struct {
	SKU         string `json:"sku"`
	Title       string `json:"title"`
	Description string `json:"description"`
	// RequiresShipping gönderilmezse true varsayılır; işaretçi olması bu
	// ayrımı korur.
	RequiresShipping *bool `json:"requires_shipping"`
}

// createItem yeni bir stok kalemi oluşturur.
func (h *Handler) createItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createItemRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	item, err := h.svc.CreateInventoryItem(ctx, service.CreateInventoryItemInput{
		SKU:              body.SKU,
		Title:            body.Title,
		Description:      body.Description,
		RequiresShipping: body.RequiresShipping,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toItemDTO(item)})
}

// listItems kalemleri sayfalayarak döner.
func (h *Handler) listItems(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	in := service.ListInventoryItemsInput{Page: page}
	if raw := r.URL.Query().Get("sku"); raw != "" {
		in.SKU = &raw
	}
	if raw := r.URL.Query().Get("requires_shipping"); raw != "" {
		flag, parseErr := strconv.ParseBool(raw)
		if parseErr != nil {
			corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest,
				"requires_shipping mantıksal bir değer olmalı: %q", raw))
			return
		}
		in.RequiresShipping = &flag
	}

	items, count, err := h.svc.ListInventoryItems(ctx, in)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]inventoryItemDTO, 0, len(items))
	for _, item := range items {
		data = append(data, toItemDTO(item))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:   data,
		Count:  count,
		Offset: page.Offset,
		Limit:  page.Limit,
	})
}

// getItem kalemi kimliğiyle döner.
func (h *Handler) getItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	item, err := h.svc.GetInventoryItem(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toItemDTO(item)})
}

// deleteItem kalemi yumuşak siler.
func (h *Handler) deleteItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.DeleteInventoryItem(ctx, chi.URLParam(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// --- stok seviyeleri ---------------------------------------------------------

// listLevels kalemin tüm lokasyonlardaki seviyelerini döner.
//
// Bu uç sayfalanmaz: bir kalemin seviye sayısı lokasyon sayısıyla sınırlıdır.
// Zarf yine de tutarlıdır; count satır sayısıdır.
func (h *Handler) listLevels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	levels, err := h.svc.ListInventoryLevels(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]inventoryLevelDTO, 0, len(levels))
	for _, level := range levels {
		data = append(data, toLevelDTO(level))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:   data,
		Count:  int64(len(data)),
		Offset: 0,
		Limit:  int64(len(data)),
	})
}

// setLevelRequest POST /admin/v1/inventory-items/{id}/levels gövdesidir.
type setLevelRequest struct {
	LocationID string `json:"location_id"`
	// StockedQuantity FİZİKSEL adettir; rezerve adet bu uçtan değiştirilmez.
	StockedQuantity *int64 `json:"stocked_quantity"`
}

// setLevel bir lokasyondaki fiziksel adedi mutlak olarak yazar.
func (h *Handler) setLevel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body setLevelRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	if body.StockedQuantity == nil {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest,
			"stocked_quantity zorunludur"))
		return
	}

	level, err := h.svc.SetInventoryLevel(ctx, chi.URLParam(r, "id"), body.LocationID, *body.StockedQuantity)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toLevelDTO(level)})
}

// adjustLevelRequest adjust ucunun gövdesidir.
type adjustLevelRequest struct {
	// Delta fiziksel adede eklenecek (negatifse çıkarılacak) miktardır.
	Delta *int64 `json:"delta"`
}

// adjustLevel fiziksel adedi delta kadar değiştirir.
func (h *Handler) adjustLevel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body adjustLevelRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	if body.Delta == nil {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest, "delta zorunludur"))
		return
	}

	level, err := h.svc.AdjustInventory(ctx,
		chi.URLParam(r, "id"), chi.URLParam(r, "location_id"), *body.Delta)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toLevelDTO(level)})
}

// --- zarflar, DTO'lar ve yardımcılar -----------------------------------------

// singleEnvelope tekil yanıtların zarfıdır (plan Bölüm 8).
type singleEnvelope struct {
	// Data yanıtın gövdesidir.
	Data any `json:"data"`
}

// listEnvelope liste yanıtlarının zarfıdır (plan Bölüm 8).
type listEnvelope struct {
	// Data sayfadaki kayıtlardır.
	Data any `json:"data"`
	// Count filtreye uyan TÜM kayıtların sayısıdır; sayfadaki satır sayısı değil.
	Count int64 `json:"count"`
	// Offset atlanan kayıt sayısıdır.
	Offset int64 `json:"offset"`
	// Limit istenen sayfa boyutudur.
	Limit int64 `json:"limit"`
}

// stockLocationDTO stok lokasyonunun dış gösterimidir.
type stockLocationDTO struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Address1    string    `json:"address_1,omitempty"`
	Address2    string    `json:"address_2,omitempty"`
	City        string    `json:"city,omitempty"`
	Province    string    `json:"province,omitempty"`
	PostalCode  string    `json:"postal_code,omitempty"`
	CountryCode string    `json:"country_code,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// inventoryItemDTO stok kaleminin dış gösterimidir.
type inventoryItemDTO struct {
	ID               string    `json:"id"`
	SKU              string    `json:"sku"`
	Title            string    `json:"title,omitempty"`
	Description      string    `json:"description,omitempty"`
	RequiresShipping bool      `json:"requires_shipping"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// inventoryLevelDTO stok seviyesinin dış gösterimidir.
//
// AvailableQuantity saklanan bir alan değildir, stocked - reserved farkından
// türetilir; istemcinin aynı çıkarımı kendi yapmasına gerek kalmasın diye
// yanıta konur.
type inventoryLevelDTO struct {
	ID                string    `json:"id"`
	InventoryItemID   string    `json:"inventory_item_id"`
	LocationID        string    `json:"location_id"`
	StockedQuantity   int64     `json:"stocked_quantity"`
	ReservedQuantity  int64     `json:"reserved_quantity"`
	AvailableQuantity int64     `json:"available_quantity"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// toLocationDTO modeli dış gösterime çevirir.
func toLocationDTO(loc models.StockLocation) stockLocationDTO {
	return stockLocationDTO{
		ID:          loc.ID,
		Name:        loc.Name,
		Address1:    loc.Address1,
		Address2:    loc.Address2,
		City:        loc.City,
		Province:    loc.Province,
		PostalCode:  loc.PostalCode,
		CountryCode: loc.CountryCode,
		CreatedAt:   loc.CreatedAt,
		UpdatedAt:   loc.UpdatedAt,
	}
}

// toItemDTO modeli dış gösterime çevirir.
func toItemDTO(item models.InventoryItem) inventoryItemDTO {
	return inventoryItemDTO{
		ID:               item.ID,
		SKU:              item.SKU,
		Title:            item.Title,
		Description:      item.Description,
		RequiresShipping: item.RequiresShipping,
		CreatedAt:        item.CreatedAt,
		UpdatedAt:        item.UpdatedAt,
	}
}

// toLevelDTO modeli dış gösterime çevirir.
func toLevelDTO(level models.InventoryLevel) inventoryLevelDTO {
	return inventoryLevelDTO{
		ID:                level.ID,
		InventoryItemID:   level.InventoryItemID,
		LocationID:        level.LocationID,
		StockedQuantity:   level.StockedQuantity,
		ReservedQuantity:  level.ReservedQuantity,
		AvailableQuantity: level.Available(),
		CreatedAt:         level.CreatedAt,
		UpdatedAt:         level.UpdatedAt,
	}
}

// decodeBody istek gövdesini çözer.
//
// Gövde boyutu sınırlanır ve TANINMAYAN ALANLAR reddedilir: sessizce yutulan
// bir alan, istemcinin gönderdiğini sandığı ama uygulanmayan bir ayar demektir.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return coreerrors.Invalid(codeInvalidRequest, "istek gövdesi boş olamaz")
		}
		return coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"istek gövdesi çözümlenemedi")
	}
	// Tek bir JSON değerinden fazlası gönderilmişse bu da bir istemci hatasıdır.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return coreerrors.Invalid(codeInvalidRequest,
			"istek gövdesi tek bir JSON nesnesi olmalı")
	}
	return nil
}

// parsePage limit/offset sorgu parametrelerini çözer.
func parsePage(r *http.Request) (service.Page, error) {
	limit, err := parseInt64Param(r, "limit")
	if err != nil {
		return service.Page{}, err
	}
	offset, err := parseInt64Param(r, "offset")
	if err != nil {
		return service.Page{}, err
	}
	page := service.Page{Limit: limit, Offset: offset}
	if page.Limit == 0 {
		// Yanıttaki limit alanının gerçekten uygulanan sınırı göstermesi için
		// varsayılan burada da görünür kılınır.
		page.Limit = service.DefaultLimit
	}
	return page, nil
}

// parseInt64Param bir sorgu parametresini tam sayıya çevirir; yoksa 0 döner.
func parseInt64Param(r *http.Request, name string) (int64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"%s has to be an integer: %q", name, raw)
	}
	return value, nil
}
