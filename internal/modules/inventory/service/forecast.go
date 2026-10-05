package service

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/bdrtr/gobit/internal/modules/inventory/models"
)

// RestockForecast answers, per item and per warehouse with nothing sellable
// now, the first expected moment at which units are left for sale there: the
// receipts still expected, taken in the order they are expected, each filling
// ADR 0392's waiting claims oldest first exactly as receiving it would
// (ADR 0399). A warehouse selling now, and one no receipt leaves anything at,
// has no key; an item with neither has none.
//
// It is the fill read forward rather than a rule of its own: receiving a receipt
// raises the sellable quantity, and [Service.fillWaiting] then gives it to the
// oldest waiting claims naming the warehouse that it can complete whole,
// passing over the ones it cannot.
//
// It makes three reads and takes no lock and no shared snapshot, so a write
// that lands between them can skew one answer and the next read corrects it. A
// receipt whose moment has passed is left out until somebody receives, cancels
// or re-records it, so the forecast errs late.
func (s *Service) RestockForecast(ctx context.Context, itemIDs []string) (map[string]map[string]time.Time, error) {
	out := map[string]map[string]time.Time{}
	if len(itemIDs) == 0 {
		return out, nil
	}

	now, err := s.AvailableQuantitiesByLocation(ctx, itemIDs)
	if err != nil {
		return nil, err
	}
	claims, err := s.store.WaitingBackordersOfItems(ctx, itemIDs)
	if err != nil {
		return nil, err
	}
	receipts, err := s.store.ExpectedSupplierReceiptsOfItems(ctx, itemIDs)
	if err != nil {
		return nil, err
	}

	byItem := map[string][]models.SupplierReceipt{}
	for i := range receipts {
		byItem[receipts[i].InventoryItemID] = append(byItem[receipts[i].InventoryItemID], receipts[i])
	}
	waiting := map[string][]models.Backorder{}
	for i := range claims {
		waiting[claims[i].InventoryItemID] = append(waiting[claims[i].InventoryItemID], claims[i])
	}

	for itemID, expected := range byItem {
		if dates := restockDates(now[itemID], waiting[itemID], expected); len(dates) > 0 {
			out[itemID] = dates
		}
	}

	return out, nil
}

// restockDates is one item's forecast: now is what each warehouse sells today
// (absent is nothing), claims the waiting claims in queue order and receipts the
// expected receipts in the order they are expected.
func restockDates(
	now map[string]int64, claims []models.Backorder, receipts []models.SupplierReceipt,
) map[string]time.Time {
	slices.SortStableFunc(claims, func(a, b models.Backorder) int { return cmp.Compare(a.Seq, b.Seq) })
	slices.SortStableFunc(receipts, func(a, b models.SupplierReceipt) int {
		if c := a.ExpectedAt.Compare(b.ExpectedAt); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})

	available := make(map[string]int64, len(now))
	for location, quantity := range now {
		available[location] = quantity
	}
	filled := make([]bool, len(claims))
	out := map[string]time.Time{}

	for r := range receipts {
		receipt := &receipts[r]
		location := receipt.LocationID
		available[location] += receipt.Quantity

		for i := range claims {
			need := claims[i].Owed()
			if filled[i] || need <= 0 || need > available[location] ||
				!slices.Contains(claims[i].LocationIDs, location) {
				continue
			}
			available[location] -= need
			filled[i] = true
		}

		if _, dated := out[location]; !dated && now[location] <= 0 && available[location] > 0 {
			out[location] = receipt.ExpectedAt
		}
	}

	return out
}
