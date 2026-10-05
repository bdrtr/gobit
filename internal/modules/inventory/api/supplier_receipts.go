package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// supplierReceiptDTO is one expected supplier receipt as it goes over the wire
// (ADR 0399).
type supplierReceiptDTO struct {
	// ID is the receipt's identifier.
	ID string `json:"id"`
	// InventoryItemID and LocationID are the item owed and the warehouse.
	InventoryItemID string `json:"inventory_item_id"`
	LocationID      string `json:"location_id"`
	// Quantity is the units expected.
	Quantity int64 `json:"quantity"`
	// ExpectedAt is when the units are expected to be sellable there.
	ExpectedAt time.Time `json:"expected_at"`
	// Reference is the embedder's own document number, when one was given.
	Reference string `json:"reference,omitempty"`
	// Status is expected, received or canceled.
	Status string `json:"status"`
	// ReceivedQuantity and ReceivedAt are the count and the moment of the
	// ledger's supplier_receipt row; present once received.
	ReceivedQuantity int64      `json:"received_quantity,omitempty"`
	ReceivedAt       *time.Time `json:"received_at,omitempty"`
	// CanceledAt is present once canceled.
	CanceledAt *time.Time `json:"canceled_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// toSupplierReceiptDTO converts the model to its external representation.
func toSupplierReceiptDTO(r models.SupplierReceipt) supplierReceiptDTO {
	return supplierReceiptDTO{
		ID:               r.ID,
		InventoryItemID:  r.InventoryItemID,
		LocationID:       r.LocationID,
		Quantity:         r.Quantity,
		ExpectedAt:       r.ExpectedAt,
		Reference:        r.Reference,
		Status:           r.Status.String(),
		ReceivedQuantity: r.ReceivedQuantity,
		ReceivedAt:       r.ReceivedAt,
		CanceledAt:       r.CanceledAt,
		CreatedAt:        r.CreatedAt,
		UpdatedAt:        r.UpdatedAt,
	}
}

// recordSupplierReceiptRequest is the body of POST .../supplier-receipts.
type recordSupplierReceiptRequest struct {
	LocationID string `json:"location_id"`
	// Quantity and ExpectedAt are required; they are pointers so that a missing
	// one is told from a zero.
	Quantity   *int64     `json:"quantity"`
	ExpectedAt *time.Time `json:"expected_at"`
	Reference  string     `json:"reference"`
}

// receiveSupplierReceiptRequest is the body of POST .../receive. The quantity
// is the count, and it is required: a receipt is always a count.
type receiveSupplierReceiptRequest struct {
	Quantity *int64 `json:"quantity"`
}

// receivedSupplierReceiptDTO is a receive's answer: the receipt and the level
// after the receipt and any claim it filled.
type receivedSupplierReceiptDTO struct {
	SupplierReceipt supplierReceiptDTO `json:"supplier_receipt"`
	// Level is null only on a repeat of a receive whose level is gone.
	Level *inventoryLevelDTO `json:"level"`
}

// recordSupplierReceipt records units a supplier owes a warehouse.
func (h *Handler) recordSupplierReceipt(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body recordSupplierReceiptRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	if body.Quantity == nil {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest, "quantity is required"))
		return
	}
	if body.ExpectedAt == nil {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest, "expected_at is required"))
		return
	}

	receipt, err := h.svc.RecordSupplierReceipt(ctx, service.RecordSupplierReceiptInput{
		InventoryItemID: chi.URLParam(r, "id"),
		LocationID:      body.LocationID,
		Quantity:        *body.Quantity,
		ExpectedAt:      *body.ExpectedAt,
		Reference:       body.Reference,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toSupplierReceiptDTO(receipt)})
}

// listSupplierReceipts returns a page of the item's receipts by expected moment.
func (h *Handler) listSupplierReceipts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	receipts, count, err := h.svc.ListSupplierReceipts(ctx, service.ListSupplierReceiptsInput{
		InventoryItemID: chi.URLParam(r, "id"),
		Status:          r.URL.Query().Get(paramStatus),
		Page:            page,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]supplierReceiptDTO, 0, len(receipts))
	for i := range receipts {
		data = append(data, toSupplierReceiptDTO(receipts[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:   data,
		Count:  count,
		Offset: page.Offset,
		Limit:  page.Limit,
	})
}

// receiveSupplierReceipt writes a receipt's counted units through the ledger.
func (h *Handler) receiveSupplierReceipt(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body receiveSupplierReceiptRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	if body.Quantity == nil {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest,
			"quantity is required: a receipt is received with the count of what arrived"))
		return
	}

	receipt, level, err := h.svc.ReceiveSupplierReceipt(ctx,
		chi.URLParam(r, "id"), chi.URLParam(r, "receipt_id"), *body.Quantity)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	out := receivedSupplierReceiptDTO{SupplierReceipt: toSupplierReceiptDTO(receipt)}
	if level != nil {
		dto := toLevelDTO(*level)
		out.Level = &dto
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: out})
}

// cancelSupplierReceipt closes a receipt that will bring nothing.
func (h *Handler) cancelSupplierReceipt(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	receipt, err := h.svc.CancelSupplierReceipt(ctx, chi.URLParam(r, "id"), chi.URLParam(r, "receipt_id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toSupplierReceiptDTO(receipt)})
}

// describeSupplierReceipts describes the four receipt endpoints.
func describeSupplierReceipts(d *openapi.Doc) {
	const meaning = "A supplier receipt is units a supplier owes one of the shop's warehouses, expected " +
		"to be SELLABLE there at `expected_at` (after put-away). gobit keeps no supplier, cost or " +
		"purchase order: `reference` carries the embedder's own document number. A receipt still " +
		"expected keeps its warehouse from closing and its item from being deleted."

	d.Describe(http.MethodPost, pathItemSupplierReceipts, openapi.Operation{
		Summary:     "Records units a supplier owes an open warehouse.",
		Description: meaning + " A changed date is a cancel and a new receipt.",
		RequestBody: d.RequestBody(recordSupplierReceiptRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The expected receipt", d.Item(supplierReceiptDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathItemSupplierReceipts, openapi.Operation{
		Summary:     "Reads the item's supplier receipts, in the order they are expected.",
		Description: meaning,
		Parameters: append(pagingParameters(),
			queryParameter(paramStatus, typeString,
				"Narrows the listing to one status: expected, received or canceled; absent, every status."),
		),
		Responses: map[string]any{
			"200": openapi.Response("A page of the item's receipts", d.List(supplierReceiptDTO{})),
		},
	})

	d.Describe(http.MethodPost, pathItemSupplierReceiptReceive, openapi.Operation{
		Summary: "Receives the counted units of an expected receipt through the ledger.",
		Description: "The count is required and may differ from the quantity expected; the receipt " +
			"closes either way, and units still owed are a new receipt. The units are written as a " +
			"`supplier_receipt` movement naming the receipt, the level is opened if the warehouse had " +
			"none, and orders waiting for the item there are filled first (ADR 0392): the level " +
			"answered is the one after the fill. A repeat with the same count answers the same; a " +
			"different count, or a canceled receipt, answers 409 " +
			"`inventory_supplier_receipt_not_expected`.",
		RequestBody: d.RequestBody(receiveSupplierReceiptRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The received receipt and the level after it",
				d.Item(receivedSupplierReceiptDTO{})),
		},
	})

	d.Describe(http.MethodPost, pathItemSupplierReceiptCancel, openapi.Operation{
		Summary: "Cancels an expected receipt; nothing is written to the stock.",
		Description: "A repeat answers the canceled receipt; a received one answers 409 " +
			"`inventory_supplier_receipt_not_expected`.",
		Responses: map[string]any{
			"200": openapi.Response("The canceled receipt", d.Item(supplierReceiptDTO{})),
		},
	})
}
