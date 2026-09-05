package service

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/core/errors"
)

// This file is the inventory module's ADMIN WRITE surface (ADR 0013).
//
// It carries a READ as well as a write, which the product and pricing surfaces
// do not, and the reason is a gap rather than a preference: the query provider
// exposes only a single total per item ([FieldAvailableQuantity], summed across
// locations). An operator cannot edit stock with a total — they have to know
// WHICH location holds what.
//
// The per-location breakdown was not added to the query provider because it is
// not storefront data: it carries reserved quantities and internal location
// names, and the read layer's consumers include the storefront listing. Putting
// it here keeps the audience the same as the write's.

// CodeAdminReadFailed reports that the admin read could not be assembled.
const CodeAdminReadFailed = "inventory_admin_read_failed"

// AdminSurface is the inventory module's admin write surface.
type AdminSurface struct{ svc *Service }

// NewAdminSurface builds the admin surface over the given service.
func NewAdminSurface(svc *Service) *AdminSurface { return &AdminSurface{svc: svc} }

// adminStockLevel is one location's line in the admin view.
//
// The JSON schema is the contract: the consumer cannot import this package, so
// this struct's tags are what it reads.
//
//	[
//	  {
//	    "location_id":       "sloc_...",
//	    "location_name":     "Main warehouse",
//	    "stocked_quantity":  42,
//	    "reserved_quantity": 3,
//	    "available_quantity": 39
//	  }
//	]
type adminStockLevel struct {
	LocationID        string `json:"location_id"`
	LocationName      string `json:"location_name"`
	StockedQuantity   int64  `json:"stocked_quantity"`
	ReservedQuantity  int64  `json:"reserved_quantity"`
	AvailableQuantity int64  `json:"available_quantity"`
}

// StockLevelsJSON returns one line per stock location for the given item.
//
// EVERY location is returned, including the ones holding nothing. A form that
// listed only the locations with a level could never be used to stock a new
// warehouse: the location would not appear until it already had stock, which is
// the state the operator is trying to reach.
//
// The reserved quantity is included because it explains a refusal the operator
// will otherwise not understand: stock cannot be set below what is already
// promised to a sale, and a form that showed only the physical count would make
// that rejection look arbitrary.
//
// The rows come out in the ORDER THE SERVICE GAVE THEM. Re-sorting here was
// tried and removed: ListStockLocations already returns a deterministic order
// (newest first), so a sort added no stability, and sorting by location id put
// the warehouses in ULID order — an order that means nothing to the operator
// reading the page.
func (a *AdminSurface) StockLevelsJSON(ctx context.Context, itemID string) (json.RawMessage, error) {
	if a == nil || a.svc == nil {
		return nil, errors.Unavailable(CodeInvalidInput, "the inventory service is not set up")
	}

	levels, err := a.svc.ListInventoryLevels(ctx, itemID)
	if err != nil {
		return nil, err
	}

	locations, _, err := a.svc.ListStockLocations(ctx, Page{Limit: MaxLimit})
	if err != nil {
		return nil, err
	}

	stocked := make(map[string]int64, len(levels))
	reserved := make(map[string]int64, len(levels))
	for i := range levels {
		stocked[levels[i].LocationID] = levels[i].StockedQuantity
		reserved[levels[i].LocationID] = levels[i].ReservedQuantity
	}

	out := make([]adminStockLevel, 0, len(locations))
	for i := range locations {
		id := locations[i].ID
		out = append(out, adminStockLevel{
			LocationID:        id,
			LocationName:      locations[i].Name,
			StockedQuantity:   stocked[id],
			ReservedQuantity:  reserved[id],
			AvailableQuantity: stocked[id] - reserved[id],
		})
	}

	body, err := json.Marshal(out)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeAdminReadFailed,
			"the stock levels of %q could not be encoded", itemID)
	}

	return body, nil
}

// SetStockLevel sets the PHYSICAL quantity held at one location.
//
// It is the physical count and not the sellable one. The sellable quantity is
// derived (stocked minus reserved) and writing it directly would mean deciding
// what to do with the reservations, which is a decision the form cannot make.
//
// The service refuses to set a count below what is already reserved, and that
// refusal reaches the operator as a Conflict: the goods are promised to a sale
// that has not shipped yet.
func (a *AdminSurface) SetStockLevel(ctx context.Context, itemID, locationID string, quantity int64) error {
	if a == nil || a.svc == nil {
		return errors.Unavailable(CodeInvalidInput, "the inventory service is not set up")
	}

	_, err := a.svc.SetInventoryLevel(ctx, itemID, locationID, quantity)

	return err
}
