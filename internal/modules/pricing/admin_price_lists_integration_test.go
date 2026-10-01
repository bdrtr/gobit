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
	"github.com/bdrtr/gobit/internal/modules/pricing/repository"
	"github.com/bdrtr/gobit/internal/modules/pricing/service"
)

// TestThePanelWritesAndListsPriceLists is ADR 0326 against a real
// PostgreSQL: the surface writes a list with its type, status and window,
// lists it under the panel's JSON with the total, oldest first, and refuses a
// type the module does not know and a window that ends before it starts.
func TestThePanelWritesAndListsPriceLists(t *testing.T) {
	ctx := context.Background()
	clock := &historyClock{now: time.Now()}
	surface := service.NewAdminSurface(service.New(repository.New(testPool.Pool()), service.Options{Now: clock.Now}))

	starts := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	ends := starts.AddDate(1, 0, 0)
	title := fmt.Sprintf("Wholesale %d", time.Now().UnixNano())
	earlier, err := surface.CreatePriceList(ctx, "Earlier "+title, "", "sale", "draft", nil, nil)
	require.NoError(t, err)
	// Two ids of one millisecond fall to their random tails (D175, D206), so
	// the second list is written a millisecond later.
	clock.now = clock.now.Add(time.Millisecond)
	id, err := surface.CreatePriceList(ctx, title, "trade prices", "override", "active", &starts, &ends)
	require.NoError(t, err)

	_, total, err := surface.PriceListsJSON(ctx, 1, 0)
	require.NoError(t, err)
	require.GreaterOrEqual(t, total, int64(2), "the total counts every list, not the page of one")
	type listed struct {
		ID          string     `json:"id"`
		Title       string     `json:"title"`
		Description string     `json:"description"`
		Type        string     `json:"type"`
		Status      string     `json:"status"`
		StartsAt    *time.Time `json:"starts_at"`
		EndsAt      *time.Time `json:"ends_at"`
		CreatedAt   time.Time  `json:"created_at"`
	}
	// Every list, a page at a time: another test's list may be newer.
	var rows []listed
	for offset := int32(0); int64(offset) < total; offset += 50 {
		raw, _, err := surface.PriceListsJSON(ctx, 50, offset)
		require.NoError(t, err)
		var page []listed
		require.NoError(t, json.Unmarshal(raw, &page))
		rows = append(rows, page...)
	}
	require.Len(t, rows, int(total), "the pages hold the total")
	at := map[string]int{}
	for i, row := range rows {
		at[row.ID] = i
	}
	require.Contains(t, at, earlier)
	require.Contains(t, at, id)
	assert.Less(t, at[earlier], at[id], "oldest first")
	row := rows[at[id]]
	assert.Equal(t, title+"|trade prices|override|active", row.Title+"|"+row.Description+"|"+row.Type+"|"+row.Status)
	require.NotNil(t, row.StartsAt)
	require.NotNil(t, row.EndsAt)
	assert.True(t, starts.Equal(*row.StartsAt) && ends.Equal(*row.EndsAt), "the window as written")
	assert.False(t, row.CreatedAt.IsZero())

	_, err = surface.CreatePriceList(ctx, "Odd", "", "bargain", "draft", nil, nil)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a type the module does not know: %v", err)
	_, err = surface.CreatePriceList(ctx, "Backwards", "", "sale", "draft", &ends, &starts)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a window that ends before it starts: %v", err)
}

// TestThePanelSwitchesAPriceListFromTheStatusItRead is ADR 0328 against a
// real PostgreSQL: a draft is published and keeps its other fields, the
// switch is recorded in the list's history as an edit is (ADR 0167), and a
// switch from a status the list is no longer in is refused and changes
// nothing.
func TestThePanelSwitchesAPriceListFromTheStatusItRead(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	surface := service.NewAdminSurface(svc)

	id, err := surface.CreatePriceList(ctx, "Spring", "the spring sale", "sale", "draft", nil, nil)
	require.NoError(t, err)
	var before int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM price_list_history WHERE price_list_id = $1`, id).Scan(&before))

	require.NoError(t, surface.SwitchPriceListStatus(ctx, id, "draft", "active"))
	list, err := svc.GetPriceList(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "active|Spring|the spring sale|sale", string(list.Status)+"|"+list.Title+"|"+list.Description+"|"+string(list.Type))
	var after int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM price_list_history WHERE price_list_id = $1`, id).Scan(&after))
	assert.Equal(t, before+1, after, "the switch is recorded in the list's history")

	err = surface.SwitchPriceListStatus(ctx, id, "draft", "active")
	require.Error(t, err)
	assert.Equal(t, service.CodePriceListMoved, errors.CodeOf(err), "read as a draft, it is active now: %v", err)
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM price_list_history WHERE price_list_id = $1`, id).Scan(&before))
	assert.Equal(t, after, before, "a refused switch records nothing")

	require.NoError(t, surface.SwitchPriceListStatus(ctx, id, "active", "expired"))
	list, err = svc.GetPriceList(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "expired", string(list.Status))
}

// TestASwitchReadsTheListUnderItsLock is ADR 0328's lock: while another
// transaction holds the list and publishes it, a switch from draft waits, and
// once it is let through it reads the list as the other left it and is
// refused. Read before the lock, it would have taken the list for a draft and
// written it over the other's write.
func TestASwitchReadsTheListUnderItsLock(t *testing.T) {
	ctx := context.Background()
	surface := service.NewAdminSurface(newService(t))
	id, err := surface.CreatePriceList(ctx, "Locked", "", "sale", "draft", nil, nil)
	require.NoError(t, err)

	other, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = other.Rollback(context.Background()) })
	_, err = other.Exec(ctx, `SELECT 1 FROM price_list WHERE id = $1 FOR UPDATE`, id)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- surface.SwitchPriceListStatus(ctx, id, "draft", "active") }()
	require.Eventually(t, func() bool {
		var waiting int
		err := testPool.Pool().QueryRow(ctx,
			`SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND datname = current_database()`).
			Scan(&waiting)
		return err == nil && waiting > 0
	}, 5*time.Second, 10*time.Millisecond, "the switch waits for the list")

	_, err = other.Exec(ctx, `UPDATE price_list SET status = 'active' WHERE id = $1`, id)
	require.NoError(t, err)
	require.NoError(t, other.Commit(ctx))

	err = <-done
	require.Error(t, err, "the switch read the list after the other published it")
	assert.Equal(t, service.CodePriceListMoved, errors.CodeOf(err))
}
