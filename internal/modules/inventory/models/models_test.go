package models_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/inventory/models"
)

// TestAvailable verifies that the sellable quantity is the stocked - reserved
// difference.
//
// In the fixtures stocked and reserved differ from each other; an implementation
// that returns only one of them instead of taking the difference cannot match a
// single row.
func TestAvailable(t *testing.T) {
	tests := []struct {
		name     string
		stocked  int64
		reserved int64
		expected int64
	}{
		{"nothing reserved", 10, 0, 10},
		{"partly reserved", 10, 4, 6},
		{"fully reserved", 7, 7, 0},
		{"empty stock", 0, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			level := models.InventoryLevel{StockedQuantity: tt.stocked, ReservedQuantity: tt.reserved}

			assert.Equal(t, tt.expected, level.Available())
		})
	}
}

// TestReservationStatusValid verifies that only the defined states are counted
// as valid.
func TestReservationStatusValid(t *testing.T) {
	for _, status := range []models.ReservationStatus{
		models.ReservationActive, models.ReservationReleased, models.ReservationConfirmed,
	} {
		assert.True(t, status.Valid(), "%q has to be valid", status)
	}

	assert.False(t, models.ReservationStatus("").Valid())
	assert.False(t, models.ReservationStatus("canceled").Valid())
}

// TestIDPrefixes verifies that every entity gets its own prefix and that the
// prefixes can be told apart from each other.
func TestIDPrefixes(t *testing.T) {
	tests := []struct {
		name     string
		generate func() string
		prefix   string
	}{
		{"location", models.NewStockLocationID, models.StockLocationIDPrefix},
		{"item", models.NewInventoryItemID, models.InventoryItemIDPrefix},
		{"level", models.NewInventoryLevelID, models.InventoryLevelIDPrefix},
		{"reservation", models.NewReservationID, models.ReservationIDPrefix},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := tt.generate()

			assert.True(t, strings.HasPrefix(id, tt.prefix), "%q has to start with %q", id, tt.prefix)
			assert.Len(t, id, len(tt.prefix)+26, "the body has to be 26 characters")
			assert.NotContains(t, strings.TrimPrefix(id, tt.prefix), "=", "there has to be no padding character")
		})
	}
}

// TestIDIsUnique verifies that even the ids produced within the same millisecond
// do not collide.
func TestIDIsUnique(t *testing.T) {
	const count = 2000

	seen := make(map[string]struct{}, count)
	for range count {
		id := models.NewInventoryItemID()
		_, repeated := seen[id]
		require.False(t, repeated, "the id repeated: %s", id)
		seen[id] = struct{}{}
	}
}

// TestIDsSortByTime verifies that an id produced later also comes later
// lexicographically.
//
// Sortability is a requirement of plan Section 8 and it is not free: the
// timestamp has to be at the START of the body and the encoding has to be over
// an alphabet that preserves the order. A random id cannot pass this test.
func TestIDsSortByTime(t *testing.T) {
	const count = 8

	ids := make([]string, 0, count)
	for range count {
		ids = append(ids, models.NewReservationID())
		// Enough for the stamp at millisecond resolution to move on.
		time.Sleep(2 * time.Millisecond)
	}

	assert.True(t, slices.IsSorted(ids), "the ids have to be sorted in production order: %v", ids)
}
