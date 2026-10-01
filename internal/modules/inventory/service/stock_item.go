package service

import "context"

// CreateItemForStock creates an inventory item with the SKU and the title for
// a variant the product module begins to stock, and returns its id (ADR
// 0310). It is the narrow write the product module resolves by name, in
// primitives, as pricing's empty price set is for the import (ADR 0207).
func (s *Service) CreateItemForStock(ctx context.Context, sku, title string) (string, error) {
	item, err := s.CreateInventoryItem(ctx, CreateInventoryItemInput{SKU: sku, Title: title})
	if err != nil {
		return "", err
	}

	return item.ID, nil
}
