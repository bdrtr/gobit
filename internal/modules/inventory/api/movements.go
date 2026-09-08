package api

// This file is English because ADR 0012 makes language a property of the FILE
// and every new file is English; api.go beside it stays Turkish.

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/openapi"
	corepage "github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// movementListing names this listing inside an opaque cursor.
//
// The name travels inside the encoded value and is checked on the way back, so
// a cursor from another listing is REFUSED rather than silently applied here —
// which would answer a question the caller did not ask, from a position that
// means nothing in this key space.
const movementListing = "inventory_movements"

// The query parameters [Handler.listMovements] reads. There are no others: a
// parameter the schema names and the server ignores is worse than a missing one
// (TestEveryQueryParameterAHandlerReadsIsDescribed holds the other direction).
const (
	paramLocationID = "location_id"
	paramAfter      = "after"
)

// movementDTO is one ledger row as it goes over the wire.
type movementDTO struct {
	// ID is the row's identifier and half of its paging position.
	ID string `json:"id"`
	// InventoryItemID and LocationID are the item and the place whose physical
	// count changed.
	InventoryItemID string `json:"inventory_item_id"`
	LocationID      string `json:"location_id"`
	// Reason is why it changed, one of a closed set.
	Reason string `json:"reason"`
	// FromAdminRequest says whether an admin request produced this movement,
	// which is to say whether the audit log holds a caller for it.
	//
	// It is derived from the reason and published anyway, because it is the
	// answer to the question this ledger deliberately does not store: there is
	// no actor column, and an operator reading a row should not have to
	// remember which of four reasons has a person behind it (ADR 0068).
	FromAdminRequest bool `json:"from_admin_request"`
	// ReservationID is the promise the units left against; it is present on a
	// sale and absent on every other reason.
	ReservationID string `json:"reservation_id,omitempty"`
	// Delta is the signed change; it is never zero.
	Delta int64 `json:"delta"`
	// StockedAfter is the physical count the change produced.
	StockedAfter int64 `json:"stocked_after"`
	// CreatedAt is when it happened, in UTC.
	CreatedAt time.Time `json:"created_at"`
}

// movementPageDTO is a page of ledger rows.
//
// It carries "next_cursor" and NOT "count", "offset" or "limit", and the
// absence is the honest part: paging here is keyset, so there is no offset to
// report, and a total would mean counting an append-only table on every request
// to answer a question nobody asked. An empty "next_cursor" is the last page.
type movementPageDTO struct {
	// Data is the page, newest first.
	Data []movementDTO `json:"data"`
	// NextCursor is the opaque position the next page starts below; it is
	// absent on the last page.
	NextCursor string `json:"next_cursor,omitempty"`
}

// listMovements reads a page of one item's stock movements.
func (h *Handler) listMovements(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	query := r.URL.Query()

	in := service.ListMovementsInput{
		InventoryItemID: chi.URLParam(r, "id"),
		LocationID:      query.Get(paramLocationID),
	}

	limit, err := parseInt64Param(r, "limit")
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	in.Limit = limit

	// The cursor is decoded HERE and not in the service: turning a position into
	// an opaque string is an HTTP concern, and the service's business is a
	// keyset position (ADR 0037's split, applied again).
	cursor, err := corepage.Decode(movementListing, query.Get(paramAfter))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	in.After = cursor

	movements, err := h.svc.ListMovements(ctx, in)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, movementPage(movements, in.Limit))
}

// movementPage renders the rows and the position of the next page.
//
// A page SHORTER than the limit is the last one and carries no cursor. That is
// the cheapest correct answer: the alternative is asking for one row more than
// was wanted and throwing it away, which costs a row read on every page to save
// a caller one empty request at the end.
//
// The limit compared against is the one the SERVICE applied, so a request that
// named none is compared against the default rather than against zero.
func movementPage(movements []models.Movement, limit int64) movementPageDTO {
	if limit == 0 {
		limit = service.DefaultLimit
	}

	out := movementPageDTO{Data: make([]movementDTO, 0, len(movements))}

	for i := range movements {
		mv := &movements[i]
		out.Data = append(out.Data, movementDTO{
			ID:               mv.ID,
			InventoryItemID:  mv.InventoryItemID,
			LocationID:       mv.LocationID,
			Reason:           mv.Reason.String(),
			FromAdminRequest: mv.Reason.FromAdminRequest(),
			ReservationID:    mv.ReservationID,
			Delta:            mv.Delta,
			StockedAfter:     mv.StockedAfter,
			CreatedAt:        mv.CreatedAt.UTC(),
		})
	}

	if int64(len(movements)) == limit && limit > 0 {
		last := movements[len(movements)-1]
		out.NextCursor = corepage.Encode(movementListing,
			corepage.Cursor{Time: last.CreatedAt, ID: last.ID})
	}

	return out
}

// describeMovements describes the ledger's listing.
func describeMovements(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathItemMovements, openapi.Operation{
		Summary: "Reads the item's stock movements, newest first.",
		Description: "A row explains ONE change to the PHYSICAL count of the item at one " +
			"location: the signed delta, the count it produced, and why. The physical " +
			"count itself stays in inventory_levels and is what every availability " +
			"read answers from; this ledger explains it rather than replacing it." +
			"\n\n" +
			"A RESERVATION IS NOT A MOVEMENT. Reserving and releasing change what is " +
			"AVAILABLE, not what is present, so neither appears here; only the CONFIRM " +
			"does, as a \"sale\", because only the confirm takes units out of the " +
			"warehouse. A sale is the one reason that names its reservation." +
			"\n\n" +
			"THE LEDGER BEGINS WHERE THE TABLE DOES. No opening balance was written for " +
			"the stock that existed before it, so the deltas DO NOT SUM to the current " +
			"count — \"stocked_after\" is what makes that harmless, and the oldest " +
			"movement of an item names the balance the ledger inherited as its " +
			"stocked_after minus its delta." +
			"\n\n" +
			"There is NO actor. Two of the four reasons come from an admin request, " +
			"which the audit log already records together with the caller, and " +
			"\"from_admin_request\" says which rows those are; the other two come from " +
			"a flow with no person behind it — a checkout confirming its reservation, " +
			"or goods received back from a customer." +
			"\n\n" +
			"Paging is KEYSET, not offset. Send back the \"next_cursor\" of the previous " +
			"page as \"after\"; when the response carries no cursor the listing is " +
			"exhausted. Offset was refused because an append-only ledger read " +
			"newest-first is the shape it is worst at: a row written between two " +
			"requests shifts every later page by one, so a reader following a stock " +
			"discrepancy silently misses a row or sees it twice." +
			"\n\n" +
			"Rows are never deleted and no retention window removes them, which is what " +
			"this repository already does with its audit log: how long they are kept is " +
			"the operator's to decide, and a statement they schedule (ADR 0068).",
		Parameters: []openapi.Parameter{
			queryParameter("limit", typeInteger,
				"Page size; absent, the default page size applies. Above the maximum it is "+
					"REFUSED rather than clamped, so a caller never silently receives fewer "+
					"rows than it asked for."),
			queryParameter(paramLocationID, typeString,
				"Narrows the listing to one location. It has no index of its own: the "+
					"filter runs inside the rows the item has already narrowed to."),
			queryParameter(paramAfter, typeString,
				"The previous page's \"next_cursor\", sent back exactly as it was given. A "+
					"cursor from another listing is refused rather than applied to this one."),
		},
		Responses: map[string]any{
			"200": openapi.Response("A page of stock movements, newest first",
				d.SchemaOf(movementPageDTO{})),
		},
	})
}
