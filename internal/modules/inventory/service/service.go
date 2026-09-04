// Package service is the business logic of the inventory module.
//
// The responsibility of the module in a single sentence: to know how many units
// of an item are at which location and how much of that is promised (reserved).
// The sellable quantity (available) IS NOT STORED, it is derived as
// stocked - reserved.
//
// # Concurrency
//
// Every flow that changes a stock quantity runs inside a single database
// transaction and does its read under a row lock (SELECT ... FOR UPDATE). That
// makes the "read first, write later" race structurally impossible: two
// concurrent reservations have to lock the same level row, the second one waits
// until the first one's transaction is over and under READ COMMITTED reads the
// CURRENT version of the row. Of two calls racing for the last unit exactly one
// wins, the other gets errors.Conflict. A check made in the application layer
// could not provide this; the boundary is in the database.
//
// The locks are taken in the same order in EVERY flow — first the item, then the
// level (see the lock order section on [Store]). Had the order changed from flow
// to flow, two different flows would ask for the same two rows in the reverse
// order, lock each other out and the database would kill one of the
// transactions: when a reservation collided with a stock update, the request
// would get an unexpected error instead of a conflict it can retry.
//
// # Module isolation
//
// This module knows no other module. The information about which product variant
// a stock item belongs to IS NOT HERE; the tie is established with the
// "product_variant_inventory" link that the product module declares (Principle
// 2.2, ADR 0001). The line_item_id the reservation carries is likewise an id
// belonging to the cart module and IS NOT a foreign key.
package service

import (
	"context"
	"log/slog"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
)

// EntityName is the entity name the module offers to the Query layer. The
// provider is registered in the container under the name "<EntityName>.query"
// (ADR 0004).
const EntityName = "inventory_item"

// Error codes. Clients may branch on these; the messages can change, the codes
// do not.
const (
	// CodeInvalidInput reports that the input did not pass validation.
	CodeInvalidInput = "inventory_invalid_input"
	// CodeInsufficientStock reports that the requested quantity is more than the
	// sellable stock. The call that loses a reservation race gets this one too.
	CodeInsufficientStock = "inventory_insufficient_stock"
	// CodeReservationNotActive reports that an invalid transition was attempted
	// on a finished reservation.
	CodeReservationNotActive = "inventory_reservation_not_active"
	// CodeItemHasReservations reports that an item with an active reservation was
	// asked to be deleted.
	CodeItemHasReservations = "inventory_item_has_reservations"
	// CodeInconsistentState reports that the reserved quantity and the
	// reservation records do not match each other; it does not occur in normal
	// operation.
	CodeInconsistentState = "inventory_inconsistent_state"
)

// Pagination limits (plan Section 8: limit/offset).
const (
	// DefaultLimit is the page size applied when no limit is given.
	DefaultLimit int64 = 50
	// MaxLimit is the largest page size that can be asked for in one request.
	MaxLimit int64 = 100
)

// maxTextLen is the upper bound for free-text fields. The bound keeps a single
// request from writing text of unlimited size into the database.
const maxTextLen = 512

// Service is the outward-facing service of the inventory module.
// It is safe for concurrent use.
type Service struct {
	store Store
	log   *slog.Logger
}

// New produces a service running on the given store.
// If log is given as nil, the logs are dropped.
func New(store Store, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{store: store, log: log}
}

// Page holds the pagination parameters of list requests.
type Page struct {
	// Limit is the maximum number of rows to return; if 0, [DefaultLimit] applies.
	Limit int64
	// Offset is the number of rows to skip.
	Offset int64
}

// normalize validates the pagination parameters and applies the defaults.
func (p Page) normalize() (Page, error) {
	if p.Limit < 0 {
		return Page{}, errors.Invalid(CodeInvalidInput, "the limit cannot be negative: %d", p.Limit)
	}
	if p.Offset < 0 {
		return Page{}, errors.Invalid(CodeInvalidInput, "the offset cannot be negative: %d", p.Offset)
	}
	if p.Limit > MaxLimit {
		return Page{}, errors.Invalid(CodeInvalidInput,
			"the limit can be at most %d: %d", MaxLimit, p.Limit)
	}
	if p.Limit == 0 {
		p.Limit = DefaultLimit
	}
	return p, nil
}

// CreateStockLocationInput holds the fields of a new stock location.
type CreateStockLocationInput struct {
	// Name is the display name of the location; it is required.
	Name string
	// Address1, Address2, City, Province and PostalCode are location details.
	Address1   string
	Address2   string
	City       string
	Province   string
	PostalCode string
	// CountryCode is the ISO 3166-1 alpha-2 country code; if it is given it has to
	// be two letters.
	CountryCode string
}

// CreateStockLocation creates a new stock location.
func (s *Service) CreateStockLocation(ctx context.Context, in CreateStockLocationInput) (models.StockLocation, error) {
	name := strings.TrimSpace(in.Name)
	if err := requireText("name", name); err != nil {
		return models.StockLocation{}, err
	}
	country := strings.ToUpper(strings.TrimSpace(in.CountryCode))
	if country != "" && len(country) != 2 {
		return models.StockLocation{}, errors.Invalid(CodeInvalidInput,
			"country_code has to be a two-letter ISO 3166-1 alpha-2 code: %q", in.CountryCode)
	}
	// The order is deliberately fixed: ranging over a map would leave which error
	// is returned up to chance when more than one field is too long at once.
	for _, field := range []struct{ label, value string }{
		{"address_1", in.Address1},
		{"address_2", in.Address2},
		{"city", in.City},
		{"province", in.Province},
		{"postal_code", in.PostalCode},
	} {
		if err := checkTextLen(field.label, field.value); err != nil {
			return models.StockLocation{}, err
		}
	}

	return s.store.CreateStockLocation(ctx, models.StockLocation{
		ID:          models.NewStockLocationID(),
		Name:        name,
		Address1:    strings.TrimSpace(in.Address1),
		Address2:    strings.TrimSpace(in.Address2),
		City:        strings.TrimSpace(in.City),
		Province:    strings.TrimSpace(in.Province),
		PostalCode:  strings.TrimSpace(in.PostalCode),
		CountryCode: country,
	})
}

// ListStockLocations returns the stock locations page by page.
// The second return value belongs not to the page but to ALL the matching rows.
func (s *Service) ListStockLocations(ctx context.Context, page Page) ([]models.StockLocation, int64, error) {
	page, err := page.normalize()
	if err != nil {
		return nil, 0, err
	}
	return s.store.ListStockLocations(ctx, page.Limit, page.Offset)
}

// GetStockLocation returns the location by its id.
func (s *Service) GetStockLocation(ctx context.Context, id string) (models.StockLocation, error) {
	if err := requireText("id", id); err != nil {
		return models.StockLocation{}, err
	}
	return s.store.GetStockLocation(ctx, id)
}

// CreateInventoryItemInput holds the fields of a new inventory item.
type CreateInventoryItemInput struct {
	// SKU is the stock keeping code; it is required and unique among living items.
	SKU string
	// Title and Description are optional.
	Title       string
	Description string
	// RequiresShipping is assumed true when it is left nil. That is the reason it
	// is a pointer: the zero value of a bool would mean "shipping is not required"
	// and a client that never sends the field would count as having created a
	// digital product.
	RequiresShipping *bool
}

// CreateInventoryItem creates a new inventory item.
// If the same SKU exists on a living item it returns errors.Conflict.
func (s *Service) CreateInventoryItem(ctx context.Context, in CreateInventoryItemInput) (models.InventoryItem, error) {
	sku := strings.TrimSpace(in.SKU)
	if err := requireText("sku", sku); err != nil {
		return models.InventoryItem{}, err
	}
	if err := checkTextLen("title", in.Title); err != nil {
		return models.InventoryItem{}, err
	}
	if err := checkTextLen("description", in.Description); err != nil {
		return models.InventoryItem{}, err
	}

	requiresShipping := true
	if in.RequiresShipping != nil {
		requiresShipping = *in.RequiresShipping
	}

	return s.store.CreateInventoryItem(ctx, models.InventoryItem{
		ID:               models.NewInventoryItemID(),
		SKU:              sku,
		Title:            strings.TrimSpace(in.Title),
		Description:      strings.TrimSpace(in.Description),
		RequiresShipping: requiresShipping,
	})
}

// GetInventoryItem returns the item by its id; errors.NotFound if there is none.
func (s *Service) GetInventoryItem(ctx context.Context, id string) (models.InventoryItem, error) {
	if err := requireText("id", id); err != nil {
		return models.InventoryItem{}, err
	}
	return s.store.GetInventoryItem(ctx, id)
}

// ListInventoryItemsInput is the input of the item listing.
type ListInventoryItemsInput struct {
	// SKU, when given, returns only the item carrying that code.
	SKU *string
	// RequiresShipping, when given, filters the items by shipping requirement.
	RequiresShipping *bool
	// Page holds the pagination parameters.
	Page Page
}

// ListInventoryItems returns the items page by page.
// The second return value is the count of ALL the rows matching the filter.
func (s *Service) ListInventoryItems(ctx context.Context, in ListInventoryItemsInput) ([]models.InventoryItem, int64, error) {
	page, err := in.Page.normalize()
	if err != nil {
		return nil, 0, err
	}
	filter := models.InventoryItemFilter{
		RequiresShipping: in.RequiresShipping,
		Limit:            page.Limit,
		Offset:           page.Offset,
	}
	if in.SKU != nil {
		sku := strings.TrimSpace(*in.SKU)
		if err := requireText("sku", sku); err != nil {
			return nil, 0, err
		}
		filter.SKU = &sku
	}
	return s.store.ListInventoryItems(ctx, filter)
}

// ListInventoryItemsByIDs returns the items of the given ids in a SINGLE query.
// No record is returned for an id that is not found; that is not an error.
func (s *Service) ListInventoryItemsByIDs(ctx context.Context, ids []string) ([]models.InventoryItem, error) {
	if len(ids) == 0 {
		return []models.InventoryItem{}, nil
	}
	return s.store.InventoryItemsByIDs(ctx, ids)
}

// DeleteInventoryItem soft deletes the item and its stock levels.
//
// If the item has an ACTIVE reservation it returns errors.Conflict: deleting
// would mean silently destroying promised stock. The check and the deletion are
// done in the same transaction and under the EXCLUSIVE lock of the item; a
// reservation slipping in between cannot dodge the check, because
// [Service.Reserve] also starts its transaction by locking the item in shared
// mode: either it finishes before the deletion and shows up in the count, or it
// waits until the deletion is over and finds the item deleted.
func (s *Service) DeleteInventoryItem(ctx context.Context, id string) error {
	if err := requireText("id", id); err != nil {
		return err
	}

	return s.store.WithTx(ctx, func(ctx context.Context) error {
		if err := s.store.LockInventoryItem(ctx, id); err != nil {
			return err
		}
		active, err := s.store.CountActiveReservations(ctx, id)
		if err != nil {
			return err
		}
		if active > 0 {
			return errors.Conflict(CodeItemHasReservations,
				"the item cannot be deleted: it has %d active reservations (%s)", active, id)
		}
		if err := s.store.SoftDeleteInventoryLevelsByItem(ctx, id); err != nil {
			return err
		}
		return s.store.SoftDeleteInventoryItem(ctx, id)
	})
}

// requireText validates a required text field.
func requireText(label, value string) error {
	if value == "" {
		return errors.Invalid(CodeInvalidInput, "%s cannot be empty", label)
	}
	return checkTextLen(label, value)
}

// checkTextLen validates the length bound of a text field.
func checkTextLen(label, value string) error {
	if len(value) > maxTextLen {
		return errors.Invalid(CodeInvalidInput,
			"%s can be at most %d bytes: %d", label, maxTextLen, len(value))
	}
	return nil
}
