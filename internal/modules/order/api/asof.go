package api

import (
	"net/http"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// orderAsOfDTO is an order as it stood at a moment (ADR 0171).
type orderAsOfDTO struct {
	OrderID string    `json:"order_id"`
	At      time.Time `json:"at"`
	// Status is the order's status at the moment; null for the one moment the
	// records cannot place (an order archived before archiving was dated).
	Status       *string            `json:"status"`
	Money        moneyAsOfDTO       `json:"money"`
	Lines        []lineAsOfDTO      `json:"lines"`
	Returns      []recordAsOfDTO    `json:"returns"`
	Claims       []recordAsOfDTO    `json:"claims"`
	Exchanges    []recordAsOfDTO    `json:"exchanges"`
	Replacements []recordAsOfDTO    `json:"replacements"`
	Shipments    []recordAsOfDTO    `json:"shipments"`
	Contact      models.ContactAsOf `json:"contact"`
}

// moneyAsOfDTO is the order's money at the moment.
type moneyAsOfDTO struct {
	Currency    string `json:"currency_code"`
	Total       int64  `json:"total"`
	Credited    int64  `json:"credited"`
	Captured    int64  `json:"captured"`
	Refunded    int64  `json:"refunded"`
	Outstanding int64  `json:"outstanding"`
}

// lineAsOfDTO is a line and what had been canceled of it by the moment.
type lineAsOfDTO struct {
	LineItemID string `json:"line_item_id"`
	VariantID  string `json:"variant_id"`
	Title      string `json:"title"`
	Quantity   int64  `json:"quantity"`
	UnitPrice  int64  `json:"unit_price"`
	Canceled   int64  `json:"canceled"`
}

// recordAsOfDTO is a record's status at the moment and since when.
type recordAsOfDTO struct {
	ID     string    `json:"id"`
	Status string    `json:"status"`
	Since  time.Time `json:"since"`
}

// adminGetOrderAsOf reads the order as it stood at the moment in "at".
//
// The moment is REQUIRED and has to be RFC 3339. A missing or unreadable one is
// refused rather than taken as "now": the order read already answers now, and a
// question about a past moment answered with the present is the silent wrong
// answer this endpoint exists to avoid.
func (h *Handler) adminGetOrderAsOf(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	raw := r.URL.Query().Get("at")
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		corehttp.WriteError(ctx, w, coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"the \"at\" parameter has to be an RFC 3339 moment, and %q is not", raw))

		return
	}

	asOf, err := h.svc.OrderAsOf(ctx, orderID(r), at.UTC())
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toOrderAsOfDTO(&asOf)})
}

// toOrderAsOfDTO converts the reading; every list is an array, never null.
func toOrderAsOfDTO(asOf *models.OrderAsOf) orderAsOfDTO {
	out := orderAsOfDTO{
		OrderID: asOf.OrderID,
		At:      asOf.At,
		Money: moneyAsOfDTO{
			Currency: asOf.Money.Currency, Total: asOf.Money.Total,
			Credited: asOf.Money.Credited, Captured: asOf.Money.Captured,
			Refunded: asOf.Money.Refunded, Outstanding: asOf.Money.Outstanding,
		},
		Lines:        make([]lineAsOfDTO, 0, len(asOf.Lines)),
		Returns:      toRecordAsOfDTOs(asOf.Returns),
		Claims:       toRecordAsOfDTOs(asOf.Claims),
		Exchanges:    toRecordAsOfDTOs(asOf.Exchanges),
		Replacements: toRecordAsOfDTOs(asOf.Replacements),
		Shipments:    toRecordAsOfDTOs(asOf.Shipments),
		Contact:      asOf.Contact,
	}
	if asOf.Status != nil {
		status := string(*asOf.Status)
		out.Status = &status
	}
	for i := range asOf.Lines {
		line := &asOf.Lines[i]
		out.Lines = append(out.Lines, lineAsOfDTO{
			LineItemID: line.LineItemID, VariantID: line.VariantID, Title: line.Title,
			Quantity: line.Quantity, UnitPrice: line.UnitPrice, Canceled: line.Canceled,
		})
	}

	return out
}

// toRecordAsOfDTOs converts a record list.
func toRecordAsOfDTOs(records []models.RecordAsOf) []recordAsOfDTO {
	out := make([]recordAsOfDTO, 0, len(records))
	for i := range records {
		out = append(out, recordAsOfDTO{ID: records[i].ID, Status: records[i].Status, Since: records[i].Since})
	}

	return out
}
