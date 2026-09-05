package page_test

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/page"
)

// listing is the name the tests encode positions for.
const listing = "products"

// TestACursorSurvivesTheRoundTrip is the whole contract: what the client sends
// back has to name the position it was given.
func TestACursorSurvivesTheRoundTrip(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 5, 10, 30, 45, 123456789, time.UTC)
	want := page.Cursor{Time: at, ID: "prod_01J8"}

	got, err := page.Decode(listing, page.Encode(listing, want))
	require.NoError(t, err)
	assert.True(t, got.Time.Equal(want.Time), "want %s, got %s", want.Time, got.Time)
	assert.Equal(t, want.ID, got.ID)
}

// TestAnIDOnlyCursorComesBackWithTheZeroTime keeps the sentinel comparison
// working for listings ordered by id alone.
//
// A zero time that came back as an instant would be compared against the
// sentinel as a real bound and would exclude every row — an empty listing with
// nothing in the log to say why.
func TestAnIDOnlyCursorComesBackWithTheZeroTime(t *testing.T) {
	t.Parallel()

	got, err := page.Decode(listing, page.Encode(listing, page.Cursor{ID: "reg_01J8"}))
	require.NoError(t, err)
	assert.True(t, got.Time.IsZero(), "the time has to come back ZERO, got %s", got.Time)
	assert.Equal(t, "reg_01J8", got.ID)
}

// TestTheEmptyCursorIsTheFirstPage covers the ordinary first request.
func TestTheEmptyCursorIsTheFirstPage(t *testing.T) {
	t.Parallel()

	got, err := page.Decode(listing, "")
	require.NoError(t, err)
	assert.True(t, got.IsZero())
}

// TestACursorFromAnotherListingIsRefused is the reason the name is inside it.
//
// Both listings order by (created_at, id), so the wrong cursor decodes cleanly
// and selects rows that exist. Served rather than refused, it reads to the
// client as a listing that skipped part of its own contents.
func TestACursorFromAnotherListingIsRefused(t *testing.T) {
	t.Parallel()

	encoded := page.Encode("orders", page.Cursor{Time: time.Now(), ID: "order_1"})

	_, err := page.Decode(listing, encoded)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "orders")
	assert.Contains(t, err.Error(), listing)
}

// TestAnUnreadableCursorIsRefused covers what a client can send by accident:
// a truncated string, something that was never a cursor, or a cursor whose
// time was edited into nonsense.
func TestAnUnreadableCursorIsRefused(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"not base64":     "!!!!not-base64!!!!",
		"too few fields": base64.RawURLEncoding.EncodeToString([]byte("products\nonly-two")),
		"time is words":  base64.RawURLEncoding.EncodeToString([]byte("products\nyesterday\nprod_1")),
	}

	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := page.Decode(listing, raw)
			require.Error(t, err)
		})
	}
}

// TestAnEncodedCursorIsOpaque keeps the shape from becoming a promise.
//
// A client that reads the id out of a cursor and builds its own would be
// coupled to the ordering key, which is exactly the thing a listing has to stay
// free to change.
func TestAnEncodedCursorIsOpaque(t *testing.T) {
	t.Parallel()

	encoded := page.Encode(listing, page.Cursor{Time: time.Now(), ID: "prod_01J8"})
	assert.NotContains(t, encoded, "prod_01J8")
	assert.NotContains(t, encoded, listing)
}
