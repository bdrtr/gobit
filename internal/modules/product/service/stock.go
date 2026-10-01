package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
)

// StockItemWriter is the NARROW surface a variant's inventory item is created
// through (ADR 0310).
//
// Inventory owns its items and this module cannot import it (Principle 2.4),
// so the surface is declared here and satisfied STRUCTURALLY by inventory's
// service, resolved by name; internal/arch pins the two together. The link
// from a variant to its item is this module's, as the price set's is (ADR
// 0207).
type StockItemWriter interface {
	// CreateItemForStock creates an inventory item with the SKU and the title
	// and returns its id.
	CreateItemForStock(ctx context.Context, sku, title string) (string, error)
}

// VariantStock is what the service holds for a variant's stock: inventory's
// surface, and whether this installation has one.
type VariantStock interface {
	StockItemWriter
	// Installed answers nil when inventory's surface is there, and an error
	// saying why not otherwise.
	Installed(ctx context.Context) error
}

// codeStockAbsent refuses to stock a variant in an installation without
// inventory.
const codeStockAbsent = "product_stock_unavailable"

// stockVariant gives the variant an inventory item and links it, and returns
// the item's id; a variant already linked to one returns it unchanged (ADR
// 0310).
//
// The item takes the variant's SKU, or the variant's id when it has none, so
// the item is named uniquely either way, and the product's and the variant's
// titles. A run stopped after the item is created and before it is linked
// leaves an item nothing names, as an import's price set is left; the next
// call creates another.
func (s *Service) stockVariant(ctx context.Context, variantID string) (string, error) {
	if s.stock == nil {
		return "", errors.Unavailable(codeStockAbsent, "this installation keeps no stock")
	}
	if err := s.stock.Installed(ctx); err != nil {
		return "", err
	}
	if s.links == nil {
		return "", s.linkerMissing()
	}

	variant, err := s.GetVariant(ctx, variantID)
	if err != nil {
		return "", err
	}
	existing, err := s.firstLink(ctx, LinkVariantInventory, variantID)
	if err != nil {
		return "", err
	}
	if existing != nil {
		return *existing, nil
	}
	// A bundle takes no item of its own (ADR 0234); asked before the item is
	// made, so a refusal leaves nothing behind.
	if err := s.requireNotBundle(ctx, variantID); err != nil {
		return "", err
	}
	product, err := s.GetProduct(ctx, variant.ProductID)
	if err != nil {
		return "", err
	}

	sku := variant.ID
	if variant.SKU != nil && *variant.SKU != "" {
		sku = *variant.SKU
	}
	itemID, err := s.stock.CreateItemForStock(ctx, sku, product.Title+" — "+variant.Title)
	if err != nil {
		return "", err
	}
	if err := s.SetVariantInventoryItem(ctx, variantID, itemID); err != nil {
		return "", err
	}

	return itemID, nil
}
