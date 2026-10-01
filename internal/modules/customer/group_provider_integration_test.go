//go:build integration

package customer_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

// TestTheGroupEntityReadsTheLiveGroups is ADR 0321 against a real
// PostgreSQL: the group entity fetches an id set in one query, leaving a
// deleted group and an unknown id out, and lists the live groups newest first
// with their names.
func TestTheGroupEntityReadsTheLiveGroups(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	provider := service.NewGroupProvider(svc)
	suffix := time.Now().UnixNano()

	older, err := svc.CreateGroup(ctx, service.GroupInput{Name: fmt.Sprintf("Older %d", suffix)})
	require.NoError(t, err)
	newer, err := svc.CreateGroup(ctx, service.GroupInput{Name: fmt.Sprintf("Newer %d", suffix), Rank: 2})
	require.NoError(t, err)
	gone, err := svc.CreateGroup(ctx, service.GroupInput{Name: fmt.Sprintf("Gone %d", suffix)})
	require.NoError(t, err)
	require.NoError(t, svc.DeleteGroup(ctx, gone.ID))

	fetched, err := provider.FetchByIDs(ctx, []string{newer.ID, gone.ID, "custgrp_missing", older.ID}, []string{"name", "rank"})
	require.NoError(t, err)
	names := map[string]any{}
	for _, record := range fetched {
		id, ok := record["id"].(string)
		require.True(t, ok, "a record carries its id")
		names[id] = record["name"]
	}
	assert.Equal(t, map[string]any{older.ID: older.Name, newer.ID: newer.Name}, names,
		"the live groups of the set, the deleted one and the unknown id left out")

	listed, err := provider.List(ctx, query.ListOptions{Fields: []string{"name"}, Limit: 100})
	require.NoError(t, err)
	var order []string
	for _, record := range listed {
		switch id, _ := record["id"].(string); id {
		case newer.ID, older.ID:
			order = append(order, id)
		case gone.ID:
			t.Errorf("the deleted group %s is listed", gone.ID)
		}
	}
	assert.Equal(t, []string{newer.ID, older.ID}, order, "newest first")
}
