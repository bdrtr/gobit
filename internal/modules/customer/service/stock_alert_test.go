package service

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/repository"
)

// The stock alert half of memRepo (ADR 0215). A mark lives on the item, as the
// columns do.

func (m *memRepo) MarkStockAlert(
	ctx context.Context, customerID, variantID string, channels []string, limit int64, now time.Time,
) (models.WishlistItem, error) {
	if _, err := m.SaveToWishlist(ctx, customerID, variantID, limit, now); err != nil {
		return models.WishlistItem{}, err
	}
	var out models.WishlistItem
	m.updateItem(customerID, variantID, func(item *models.WishlistItem) {
		item.StockAlert, item.StockAlertChannels, item.StockAlertArmedAt = true, channels, nil
		out = *item
	})

	return out, nil
}

func (m *memRepo) UnmarkStockAlert(_ context.Context, customerID, variantID string) error {
	if _, ok := m.liveCustomer(customerID); !ok {
		return errors.NotFound(repository.CodeCustomerNotFound, "customer not found: %s", customerID)
	}
	m.updateItem(customerID, variantID, func(item *models.WishlistItem) {
		item.StockAlert, item.StockAlertChannels, item.StockAlertArmedAt = false, nil, nil
	})

	return nil
}

func (m *memRepo) ListStockAlerts(
	_ context.Context, afterCustomerID, afterVariantID string, limit int32,
) ([]models.WishlistItem, error) {
	var out []models.WishlistItem
	for customerID, items := range m.wishlist {
		if _, ok := m.liveCustomer(customerID); !ok {
			continue
		}
		for _, item := range items {
			key := [2]string{item.CustomerID, item.VariantID}
			if item.StockAlert && (key[0] > afterCustomerID || (key[0] == afterCustomerID && key[1] > afterVariantID)) {
				out = append(out, item)
			}
		}
	}
	slices.SortFunc(out, func(a, b models.WishlistItem) int {
		if c := strings.Compare(a.CustomerID, b.CustomerID); c != 0 {
			return c
		}
		return strings.Compare(a.VariantID, b.VariantID)
	})
	if len(out) > int(limit) {
		out = out[:limit]
	}

	return out, nil
}

func (m *memRepo) ArmStockAlert(_ context.Context, customerID, variantID string) (bool, error) {
	armed := false
	m.updateItem(customerID, variantID, func(item *models.WishlistItem) {
		if item.StockAlert && item.StockAlertArmedAt == nil {
			at := time.Unix(3_000, 0).UTC()
			item.StockAlertArmedAt, armed = &at, true
		}
	})

	return armed, nil
}

func (m *memRepo) ClearStockAlert(_ context.Context, customerID, variantID string, armedAt time.Time) (bool, error) {
	cleared := false
	m.updateItem(customerID, variantID, func(item *models.WishlistItem) {
		if item.StockAlertArmedAt != nil && item.StockAlertArmedAt.Equal(armedAt) {
			item.StockAlert, item.StockAlertChannels, item.StockAlertArmedAt = false, nil, nil
			cleared = true
		}
	})

	return cleared, nil
}

// updateItem changes one saved item in place, if it is there.
func (m *memRepo) updateItem(customerID, variantID string, change func(*models.WishlistItem)) {
	items := m.wishlist[customerID]
	for i := range items {
		if items[i].VariantID == variantID {
			change(&items[i])
		}
	}
}

// TestAMarkSavesTheVariantAndRecordsTheChannels is ADR 0215: the variant goes
// on the list if it is not there, and the mark keeps the request's channels.
func TestAMarkSavesTheVariantAndRecordsTheChannels(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newWishlistService(t)
	customer := newWishlistCustomer(ctx, t, svc)

	item, err := svc.MarkStockAlert(ctx, customer.ID, "variant_A", []string{"sc_1"})

	require.NoError(t, err)
	assert.True(t, item.StockAlert)
	assert.Equal(t, []string{"sc_1"}, item.StockAlertChannels)
	items, err := svc.ListWishlist(ctx, customer.ID)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.True(t, items[0].StockAlert)
}

// TestAFullListTakesNoNewMark: a mark on a variant not on a full list is the
// save the cap refuses.
func TestAFullListTakesNoNewMark(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newWishlistService(t)
	customer := newWishlistCustomer(ctx, t, svc)
	for i := range models.MaxWishlistItems {
		_, err := svc.SaveToWishlist(ctx, customer.ID, "variant_"+strings.Repeat("x", i+1))
		require.NoError(t, err)
	}

	_, err := svc.MarkStockAlert(ctx, customer.ID, "variant_new", nil)

	assert.Equal(t, models.CodeWishlistFull, errors.CodeOf(err))
}

// TestAnUnmarkLeavesTheItem: the variant stays on the list.
func TestAnUnmarkLeavesTheItem(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newWishlistService(t)
	customer := newWishlistCustomer(ctx, t, svc)
	_, err := svc.MarkStockAlert(ctx, customer.ID, "variant_A", nil)
	require.NoError(t, err)

	require.NoError(t, svc.UnmarkStockAlert(ctx, customer.ID, "variant_A"))

	items, err := svc.ListWishlist(ctx, customer.ID)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.False(t, items[0].StockAlert)
}

// TestTheAlertFlowReadsArmsAndClearsThroughTheInterop: the page names the marks
// with their channels, arming happens once, and a clear only takes the arming
// its mail was sent for.
func TestTheAlertFlowReadsArmsAndClearsThroughTheInterop(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newWishlistService(t)
	customer := newWishlistCustomer(ctx, t, svc)
	_, err := svc.MarkStockAlert(ctx, customer.ID, "variant_A", []string{"sc_1"})
	require.NoError(t, err)
	_, err = svc.SaveToWishlist(ctx, customer.ID, "variant_B")
	require.NoError(t, err)

	raw, err := svc.StockAlertsJSON(ctx, "", "", 100)
	require.NoError(t, err)
	var page []map[string]any
	require.NoError(t, json.Unmarshal(raw, &page))
	assert.Equal(t, []map[string]any{{
		"customer_id": customer.ID, "variant_id": "variant_A",
		"sales_channel_ids": []any{"sc_1"}, "armed_at": nil,
	}}, page, "the unmarked item is not a page's")

	armed, err := svc.ArmStockAlert(ctx, customer.ID, "variant_A")
	require.NoError(t, err)
	assert.True(t, armed)
	again, err := svc.ArmStockAlert(ctx, customer.ID, "variant_A")
	require.NoError(t, err)
	assert.False(t, again, "armed once")

	stale, err := svc.ClearStockAlert(ctx, customer.ID, "variant_A", time.Unix(1, 0))
	require.NoError(t, err)
	assert.False(t, stale, "a clear for another arming takes nothing")
	cleared, err := svc.ClearStockAlert(ctx, customer.ID, "variant_A", time.Unix(3_000, 0).UTC())
	require.NoError(t, err)
	assert.True(t, cleared)

	_, err = svc.StockAlertsJSON(ctx, "", "", 0)
	assert.True(t, errors.IsInvalid(err), "a page of nothing is a mistake")
}
