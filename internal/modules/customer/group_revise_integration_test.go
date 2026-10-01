//go:build integration

package customer_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/repository"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

// TestAGroupIsRevisedOnlyAsItWasRead is ADR 0329 against a real PostgreSQL:
// the name and the rank are written from the ones read; a read name or a read
// rank that is no longer the group's writes nothing and is refused by what
// the group is now; a name another live group holds is refused, a deleted
// group's is free; a deleted group is not found.
func TestAGroupIsRevisedOnlyAsItWasRead(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	suffix := time.Now().UnixNano()
	name := func(label string) string { return fmt.Sprintf("%s %d", label, suffix) }

	trade, err := svc.CreateGroup(ctx, service.GroupInput{Name: name("Trade"), Rank: 2})
	require.NoError(t, err)
	vip, err := svc.CreateGroup(ctx, service.GroupInput{Name: name("VIP")})
	require.NoError(t, err)

	revised, err := svc.ReviseGroup(ctx, trade.ID, trade.Name, 2, " "+name("Wholesale")+" ", -3)
	require.NoError(t, err)
	assert.Equal(t, name("Wholesale"), revised.Name)
	assert.Equal(t, int32(-3), revised.Rank)
	stored, err := svc.GetGroup(ctx, trade.ID)
	require.NoError(t, err)
	assert.Equal(t, name("Wholesale")+"|-3", fmt.Sprintf("%s|%d", stored.Name, stored.Rank))

	for label, read := range map[string]struct {
		name string
		rank int32
	}{
		"a name read before the revision": {trade.Name, -3},
		"a rank read before the revision": {name("Wholesale"), 2},
	} {
		_, err = svc.ReviseGroup(ctx, trade.ID, read.name, read.rank, name("Retail"), 9)
		require.Error(t, err, label)
		assert.Equal(t, service.CodeGroupMoved, errors.CodeOf(err), "%s: %v", label, err)
		assert.Contains(t, err.Error(), fmt.Sprintf("is %q at rank -3 now", name("Wholesale")), label)
	}
	stored, err = svc.GetGroup(ctx, trade.ID)
	require.NoError(t, err)
	assert.Equal(t, name("Wholesale")+"|-3", fmt.Sprintf("%s|%d", stored.Name, stored.Rank), "a stale read writes nothing")

	_, err = svc.ReviseGroup(ctx, trade.ID, name("Wholesale"), -3, vip.Name, 0)
	require.Error(t, err)
	assert.Equal(t, repository.CodeGroupNameTaken, errors.CodeOf(err), "a live group's name: %v", err)
	require.NoError(t, svc.DeleteGroup(ctx, vip.ID))
	revised, err = svc.ReviseGroup(ctx, trade.ID, name("Wholesale"), -3, vip.Name, 0)
	require.NoError(t, err, "a deleted group's name is free")
	assert.Equal(t, vip.Name, revised.Name)

	_, err = svc.ReviseGroup(ctx, vip.ID, vip.Name, 0, name("Back"), 0)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "a deleted group is not revised: %v", err)
}
