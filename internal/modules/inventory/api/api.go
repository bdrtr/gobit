// Package api is the inventory module's HTTP surface.
//
// There are admin routes only. STOCK IS NOT OPENED DIRECTLY TO THE STORE: the
// customer side sees stock through the product listing, via the Query layer's
// "inventory_item" provider (see service.QueryProvider). That way the stock
// surface goes through a single read path, and internal details such as the
// per-location breakdown do not leak to the store.
//
// Handlers do NOT CHOOSE the status code: the service returns its core/errors
// typed error and corehttp.WriteError writes the code that fits its class
// (plan Section 8).
//
// # Scopes
//
// The /admin/v1 endpoints ask for a scope and the dictionary splits in two: GET
// endpoints ask for [ScopeRead], POST/PUT/PATCH/DELETE endpoints for
// [ScopeWrite] (see [Handler.Routes]). corehttp.ScopeAdmin is a SUPERSCOPE and
// satisfies both on its own.
//
// The module has NO /store/v1 endpoint, and so it has no unscoped surface
// either.
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

// Route paths. Module routes are registered with the FULL PATH; a prefix such
// as "/admin/v1" is NOT MOUNTED, because the first module to mount it would own
// that whole subtree and collide with the other modules using the same prefix.
const (
	pathStockLocations = "/admin/v1/stock-locations"
	pathStockLocation  = "/admin/v1/stock-locations/{id}"
	// A location is retired by CLOSING it (ADR 0055) and the path says so.
	// DELETE would be the lie the decision rejects: the row survives and it is
	// still read.
	pathStockLocationClose = "/admin/v1/stock-locations/{id}/close"
	pathItems              = "/admin/v1/inventory-items"
	pathItem               = "/admin/v1/inventory-items/{id}"
	pathItemLevels         = "/admin/v1/inventory-items/{id}/levels"
	pathItemLevelAdjust    = "/admin/v1/inventory-items/{id}/levels/{location_id}/adjust"
	// The ledger hangs off the ITEM rather than being a top-level resource,
	// because the question it answers is "what happened to this item" and there
	// is no listing across items — the index leads on the item for the same
	// reason (ADR 0068).
	pathItemMovements = "/admin/v1/inventory-items/{id}/movements"
	// The orders waiting for the item's units, in queue order (ADR 0392).
	pathItemBackorders = "/admin/v1/inventory-items/{id}/backorders"
	// The units suppliers owe the item's warehouses (ADR 0399). A receipt is
	// closed by RECEIVING or CANCELING it, two verbs, because each is a fact of
	// its own and neither deletes the row.
	pathItemSupplierReceipts       = "/admin/v1/inventory-items/{id}/supplier-receipts"
	pathItemSupplierReceiptReceive = "/admin/v1/inventory-items/{id}/supplier-receipts/{receipt_id}/receive"
	pathItemSupplierReceiptCancel  = "/admin/v1/inventory-items/{id}/supplier-receipts/{receipt_id}/cancel"
)

// maxBodyBytes is the upper bound on a request body. Without a bound a single
// request could exhaust the server's memory.
const maxBodyBytes int64 = 1 << 20 // 1 MiB

// codeInvalidRequest is the error code returned when a body or a parameter
// cannot be parsed.
const codeInvalidRequest = "inventory_invalid_request"

// codeLinkUnavailable reports that the channel binding endpoints were called
// without the link service (see saleschannel.go).
const codeLinkUnavailable = "inventory_link_unavailable"

// The scope dictionary: the scopes inventory's admin endpoints ask for.
//
// The dictionary has the SAME shape in every module and DELIBERATELY consists
// of two entries: read and write. Defining a separate scope per resource
// ("stock-locations:write", "levels:read" …) makes the list longer but makes no
// new decision possible that could be made today; the distinction is added
// when it is really needed.
const (
	// ScopeRead is the scope the READ endpoints on inventory's admin surface ask
	// for.
	//
	// It is enough to read locations, inventory items and levels; it opens no
	// write endpoint. Fully privileged identities do not need to be granted it
	// separately: a caller carrying corehttp.ScopeAdmin satisfies it too (see
	// corehttp.Principal.HasScope).
	ScopeRead = "inventory:read"

	// ScopeWrite is the scope the WRITE endpoints on inventory's admin surface
	// ask for.
	//
	// The distinction is especially useful here: an integration that only
	// REPORTS stock (a warehouse dashboard, a sales forecast) can work with
	// [ScopeRead], and when it goes wrong it cannot corrupt the real stock.
	ScopeWrite = "inventory:write"
)

// Inventory is the surface the handlers need from the service.
//
// Keeping it narrow keeps the tests simple: the HTTP behavior can be verified
// without a real database, with a fake a few lines long.
type Inventory interface {
	// CreateStockLocation creates a new stock location.
	CreateStockLocation(ctx context.Context, in service.CreateStockLocationInput) (models.StockLocation, error)
	// GetStockLocation returns the location by its id, the closed ones too.
	GetStockLocation(ctx context.Context, id string) (models.StockLocation, error)
	// ListStockLocations pages the locations.
	ListStockLocations(ctx context.Context, in service.ListStockLocationsInput) ([]models.StockLocation, int64, error)
	// CloseStockLocation closes the location; Conflict when it is not empty.
	CloseStockLocation(ctx context.Context, id string) (models.StockLocation, error)

	// CreateInventoryItem creates a new inventory item.
	CreateInventoryItem(ctx context.Context, in service.CreateInventoryItemInput) (models.InventoryItem, error)
	// GetInventoryItem returns the item by its id.
	GetInventoryItem(ctx context.Context, id string) (models.InventoryItem, error)
	// ListInventoryItems pages the items.
	ListInventoryItems(ctx context.Context, in service.ListInventoryItemsInput) ([]models.InventoryItem, int64, error)
	// DeleteInventoryItem soft-deletes the item.
	DeleteInventoryItem(ctx context.Context, id string) error

	// ListInventoryLevels returns the item's stock levels.
	ListInventoryLevels(ctx context.Context, itemID string) ([]models.InventoryLevel, error)
	// SetInventoryLevel writes the physical quantity as an absolute value.
	SetInventoryLevel(ctx context.Context, itemID, locationID string, stockedQty int64) (models.InventoryLevel, error)
	// AdjustInventory changes the physical quantity by delta.
	AdjustInventory(ctx context.Context, itemID, locationID string, delta int64) (models.InventoryLevel, error)
	// ListMovements returns a page of the item's stock movements, newest first.
	ListMovements(ctx context.Context, in service.ListMovementsInput) ([]models.Movement, error)
	// ListBackorders returns a page of the item's claims in queue order.
	ListBackorders(ctx context.Context, in service.ListBackordersInput) ([]models.Backorder, int64, error)

	// RecordSupplierReceipt records units a supplier owes a warehouse.
	RecordSupplierReceipt(ctx context.Context, in service.RecordSupplierReceiptInput) (models.SupplierReceipt, error)
	// ListSupplierReceipts returns a page of the item's receipts by expected
	// moment.
	ListSupplierReceipts(ctx context.Context, in service.ListSupplierReceiptsInput) ([]models.SupplierReceipt, int64, error)
	// ReceiveSupplierReceipt writes a receipt's counted units through the
	// ledger and closes it.
	ReceiveSupplierReceipt(
		ctx context.Context, itemID, receiptID string, quantity int64,
	) (models.SupplierReceipt, *models.InventoryLevel, error)
	// CancelSupplierReceipt closes a receipt that will bring nothing.
	CancelSupplierReceipt(ctx context.Context, itemID, receiptID string) (models.SupplierReceipt, error)
}

// Handler is the inventory module's set of HTTP handlers.
type Handler struct {
	svc Inventory
	// links is the core service that writes the warehouse↔channel binding. IT
	// MAY BE NIL, and then only the binding endpoints fail CLOSED (see
	// Handler.bindings); the rest of the module works in an installation
	// without a link service too.
	links ChannelBindings
}

// NewHandler builds the set of handlers working over the given service.
func NewHandler(svc Inventory, links ChannelBindings) *Handler {
	return &Handler{svc: svc, links: links}
}

// Routes binds the module's admin routes to the router.
//
// # PROTECTION
//
// There are two layers and both are needed:
//
//  1. IDENTITY — the endpoints are protected by corehttp.RequireAdmin. That
//     middleware is attached not in this module but on the side that builds
//     the router (see corehttp.APIGuards).
//  2. SCOPE — the endpoints are marked HERE, one by one, with
//     corehttp.RequireScope: GET endpoints ask for [ScopeRead], POST/DELETE
//     endpoints for [ScopeWrite].
//
// Without the second layer authentication would stand in for authorization:
// an admin user whose scopes had been emptied could log in and write stock
// levels or delete items. Stock is the number that decides what can be sold;
// writing it wrong is directly a lost sale or an oversell.
func (h *Handler) Routes(r chi.Router) {
	read := r.With(corehttp.RequireScope(ScopeRead))
	write := r.With(corehttp.RequireScope(ScopeWrite))

	write.Post(pathStockLocations, h.createStockLocation)
	read.Get(pathStockLocations, h.listStockLocations)
	read.Get(pathStockLocation, h.getStockLocation)
	write.Post(pathStockLocationClose, h.closeStockLocation)

	write.Post(pathItems, h.createItem)
	read.Get(pathItems, h.listItems)
	read.Get(pathItem, h.getItem)
	write.Delete(pathItem, h.deleteItem)

	read.Get(pathItemLevels, h.listLevels)
	write.Post(pathItemLevels, h.setLevel)
	write.Post(pathItemLevelAdjust, h.adjustLevel)

	// The ledger is READ authority and not a third scope. The module's
	// dictionary is deliberately two entries (see above), and this listing shows
	// the history of the same numbers GET .../levels already shows to the same
	// audience; a scope of its own would name a power nobody has to grant
	// separately (ADR 0068).
	read.Get(pathItemMovements, h.listMovements)
	// The queue is READ authority for the ledger's reason: it shows who the
	// next units of the item go to, and nothing here writes it (ADR 0392).
	read.Get(pathItemBackorders, h.listBackorders)
	// Stock on its way (ADR 0399): recording, receiving and canceling write,
	// and the listing reads.
	write.Post(pathItemSupplierReceipts, h.recordSupplierReceipt)
	read.Get(pathItemSupplierReceipts, h.listSupplierReceipts)
	write.Post(pathItemSupplierReceiptReceive, h.receiveSupplierReceipt)
	write.Post(pathItemSupplierReceiptCancel, h.cancelSupplierReceipt)

	// Which sales channels the warehouse ships for. The binding is NOT this
	// module's table but core/link's: the channel is the auth module's record
	// and foreign keys between modules are forbidden (Principle 2.2). The
	// reasoning is in service.Definitions.
	read.Get(pathLocationChannels, h.listLocationSalesChannels)
	write.Post(pathLocationChannels, h.bindLocationSalesChannel)
	write.Delete(pathLocationChannel, h.unbindLocationSalesChannel)
}

// --- stock locations ---------------------------------------------------------

// createStockLocationRequest is the body of POST /admin/v1/stock-locations.
type createStockLocationRequest struct {
	Name     string `json:"name"`
	Address1 string `json:"address_1"`
	Address2 string `json:"address_2"`
	City     string `json:"city"`
	// Province is the sub-country unit under the country — an il in Turkey, a
	// state in the US. It is NOT the district a domestic carrier prices on; that
	// has no field of its own (ADR 0067).
	Province    string `json:"province"`
	PostalCode  string `json:"postal_code"`
	CountryCode string `json:"country_code"`
}

// createStockLocation creates a new stock location.
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

// getStockLocation returns the location by its id.
func (h *Handler) getStockLocation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	loc, err := h.svc.GetStockLocation(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toLocationDTO(loc)})
}

// closeStockLocation closes the location.
//
// A close is not a DELETE (ADR 0055): the row stays and goes on being read. The
// service answers Conflict while the location still holds stock, an active
// reservation or an expected supplier receipt (ADR 0399), and on success the
// body of the closed location carries closed_at.
func (h *Handler) closeStockLocation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	loc, err := h.svc.CloseStockLocation(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toLocationDTO(loc)})
}

// listStockLocations returns the stock locations page by page.
//
// Closed locations do NOT come BY DEFAULT; include_closed=true asks for them.
func (h *Handler) listStockLocations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	in := service.ListStockLocationsInput{Page: page}
	if raw := r.URL.Query().Get("include_closed"); raw != "" {
		flag, parseErr := strconv.ParseBool(raw)
		if parseErr != nil {
			corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest,
				"include_closed has to be a boolean: %q", raw))
			return
		}
		in.IncludeClosed = flag
	}

	locations, count, err := h.svc.ListStockLocations(ctx, in)
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

// --- inventory items ---------------------------------------------------------

// createItemRequest is the body of POST /admin/v1/inventory-items.
type createItemRequest struct {
	SKU         string `json:"sku"`
	Title       string `json:"title"`
	Description string `json:"description"`
	// RequiresShipping is taken to be true when it is not sent; its being a
	// pointer is what preserves that distinction.
	RequiresShipping *bool `json:"requires_shipping"`
}

// createItem creates a new inventory item.
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

// listItems returns the items page by page.
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
				"requires_shipping has to be a boolean: %q", raw))
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

// getItem returns the item by its id.
func (h *Handler) getItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	item, err := h.svc.GetInventoryItem(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toItemDTO(item)})
}

// deleteItem soft-deletes the item.
func (h *Handler) deleteItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.DeleteInventoryItem(ctx, chi.URLParam(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// --- stock levels ------------------------------------------------------------

// listLevels returns the item's levels at every location.
//
// This endpoint is not paged: the number of levels an item has is bounded by
// the number of locations. The envelope is consistent all the same; count is
// the number of rows.
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

// setLevelRequest is the body of POST /admin/v1/inventory-items/{id}/levels.
type setLevelRequest struct {
	LocationID string `json:"location_id"`
	// StockedQuantity is the PHYSICAL quantity; the reserved quantity is not
	// changed through this endpoint.
	StockedQuantity *int64 `json:"stocked_quantity"`
}

// setLevel writes the physical quantity at a location as an absolute value.
func (h *Handler) setLevel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body setLevelRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	if body.StockedQuantity == nil {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest,
			"stocked_quantity is required"))
		return
	}

	level, err := h.svc.SetInventoryLevel(ctx, chi.URLParam(r, "id"), body.LocationID, *body.StockedQuantity)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toLevelDTO(level)})
}

// adjustLevelRequest is the body of the adjust endpoint.
type adjustLevelRequest struct {
	// Delta is the amount to add to the physical quantity (to subtract when it
	// is negative).
	Delta *int64 `json:"delta"`
}

// adjustLevel changes the physical quantity by delta.
func (h *Handler) adjustLevel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body adjustLevelRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	if body.Delta == nil {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest, "delta is required"))
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

// --- envelopes, DTOs and helpers ---------------------------------------------

// singleEnvelope is the envelope of single-record responses (plan Section 8).
type singleEnvelope struct {
	// Data is the body of the response.
	Data any `json:"data"`
}

// listEnvelope is the envelope of list responses (plan Section 8).
type listEnvelope struct {
	// Data are the records on the page.
	Data any `json:"data"`
	// Count is the number of ALL the records matching the filter, not the
	// number of rows on the page.
	Count int64 `json:"count"`
	// Offset is the number of records skipped.
	Offset int64 `json:"offset"`
	// Limit is the requested page size.
	Limit int64 `json:"limit"`
}

// stockLocationDTO is the external representation of a stock location.
type stockLocationDTO struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Address1 string `json:"address_1,omitempty"`
	Address2 string `json:"address_2,omitempty"`
	City     string `json:"city,omitempty"`
	// Province is the sub-country unit under the country — an il in Turkey, a
	// state in the US. It is NOT the district a domestic carrier prices on; that
	// has no field of its own (ADR 0067).
	Province    string    `json:"province,omitempty"`
	PostalCode  string    `json:"postal_code,omitempty"`
	CountryCode string    `json:"country_code,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	// ClosedAt is the moment the location was closed; it is null while the
	// location is open.
	//
	// There is NO omitempty and that is deliberate: the field appears in every
	// response, because "not closed" and "this build never writes this field"
	// look the same on the client side, and the first one is a FACT.
	ClosedAt *time.Time `json:"closed_at"`
}

// inventoryItemDTO is the external representation of an inventory item.
type inventoryItemDTO struct {
	ID               string    `json:"id"`
	SKU              string    `json:"sku"`
	Title            string    `json:"title,omitempty"`
	Description      string    `json:"description,omitempty"`
	RequiresShipping bool      `json:"requires_shipping"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// inventoryLevelDTO is the external representation of a stock level.
//
// AvailableQuantity is not a stored field; it is derived from the difference
// stocked - reserved, and it is put into the response so that the client does
// not have to make the same derivation itself.
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

// toLocationDTO converts the model to its external representation.
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
		ClosedAt:    loc.ClosedAt,
	}
}

// toItemDTO converts the model to its external representation.
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

// toLevelDTO converts the model to its external representation.
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

// decodeBody decodes the request body.
//
// The body size is bounded and UNKNOWN FIELDS are rejected: a field that is
// silently swallowed means a setting the client believes it sent and that is
// never applied.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return coreerrors.Invalid(codeInvalidRequest, "request body cannot be empty")
		}
		return coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"request body could not be parsed")
	}
	// More than a single JSON value having been sent is a client error as well.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return coreerrors.Invalid(codeInvalidRequest,
			"request body has to be a single JSON object")
	}
	return nil
}

// parsePage parses the limit/offset query parameters.
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
		// The default is made visible here too, so that the limit field of the
		// response shows the bound that is really applied.
		page.Limit = service.DefaultLimit
	}
	return page, nil
}

// parseInt64Param converts a query parameter to an integer; it returns 0 when
// the parameter is absent.
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
