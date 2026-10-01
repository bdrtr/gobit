//go:build integration

package pricing_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/service"
)

// TestThePanelWritesAndListsPriceLists is ADR 0326 against a real
// PostgreSQL: the surface writes a list with its type, status and window,
// lists it under the panel's JSON with the total, and refuses a type the
// module does not know and a window that ends before it starts.
func TestThePanelWritesAndListsPriceLists(t *testing.T) {
	ctx := context.Background()
	surface := service.NewAdminSurface(newService(t))

	starts := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	ends := starts.AddDate(1, 0, 0)
	title := fmt.Sprintf("Wholesale %d", time.Now().UnixNano())
	_, err := surface.CreatePriceList(ctx, "Earlier "+title, "", "sale", "draft", nil, nil)
	require.NoError(t, err)
	id, err := surface.CreatePriceList(ctx, title, "trade prices", "override", "active", &starts, &ends)
	require.NoError(t, err)

	_, total, err := surface.PriceListsJSON(ctx, 1, 0)
	require.NoError(t, err)
	require.GreaterOrEqual(t, total, int64(2), "the total counts every list, not the page of one")
	// The lists come oldest first, by their time-ordered ids, so the one just
	// written is the last.
	raw, _, err := surface.PriceListsJSON(ctx, 1, int32(total-1))
	require.NoError(t, err)
	var rows []struct {
		ID          string     `json:"id"`
		Title       string     `json:"title"`
		Description string     `json:"description"`
		Type        string     `json:"type"`
		Status      string     `json:"status"`
		StartsAt    *time.Time `json:"starts_at"`
		EndsAt      *time.Time `json:"ends_at"`
		CreatedAt   time.Time  `json:"created_at"`
	}
	require.NoError(t, json.Unmarshal(raw, &rows))
	require.Len(t, rows, 1)
	assert.Equal(t, id, rows[0].ID, "the list written is the last")
	for _, row := range rows {
		assert.Equal(t, title+"|trade prices|override|active", row.Title+"|"+row.Description+"|"+row.Type+"|"+row.Status)
		require.NotNil(t, row.StartsAt)
		require.NotNil(t, row.EndsAt)
		assert.True(t, starts.Equal(*row.StartsAt) && ends.Equal(*row.EndsAt), "the window as written")
		assert.False(t, row.CreatedAt.IsZero())
	}

	_, err = surface.CreatePriceList(ctx, "Odd", "", "bargain", "draft", nil, nil)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a type the module does not know: %v", err)
	_, err = surface.CreatePriceList(ctx, "Backwards", "", "sale", "draft", &ends, &starts)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a window that ends before it starts: %v", err)
}
