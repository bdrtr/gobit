//go:build integration

package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/repository"
)

// TestASalesChannelIsRevisedOnlyAsItWasRead is ADR 0352 against a real
// PostgreSQL: the terms are written while they are the ones read; a name, a
// description or a state read wrong, and a deleted channel, write nothing;
// and a name another live channel holds is refused.
func TestASalesChannelIsRevisedOnlyAsItWasRead(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	channel := newChannel(ctx, t, repo)
	other := newChannel(ctx, t, repo)
	read := channel.Terms()

	next := models.ChannelTerms{Name: channel.Name + " renamed", Description: "for the phone", IsDisabled: true}
	revised, written, err := repo.ReviseSalesChannel(ctx, channel.ID, read, next, time.Now())
	require.NoError(t, err)
	require.True(t, written)
	assert.Equal(t, next, revised.Terms())
	stored, err := repo.GetSalesChannel(ctx, channel.ID)
	require.NoError(t, err)
	assert.Equal(t, next, stored.Terms(), "every term written")
	assert.True(t, stored.UpdatedAt.After(channel.UpdatedAt), "the moment it was written moves")

	for label, stale := range map[string]models.ChannelTerms{
		"a name read before":        {Name: read.Name, Description: next.Description, IsDisabled: next.IsDisabled},
		"a description read before": {Name: next.Name, Description: read.Description, IsDisabled: next.IsDisabled},
		"a state read before":       {Name: next.Name, Description: next.Description, IsDisabled: read.IsDisabled},
	} {
		_, written, err = repo.ReviseSalesChannel(ctx, channel.ID, stale, read, time.Now())
		require.NoError(t, err, label)
		assert.False(t, written, label)
	}

	_, _, err = repo.ReviseSalesChannel(ctx, channel.ID, next,
		models.ChannelTerms{Name: other.Name}, time.Now())
	assert.Equal(t, repository.CodeChannelNameTaken, errors.CodeOf(err), "another live channel's name: %v", err)

	require.NoError(t, repo.DeleteSalesChannel(ctx, channel.ID, time.Now()))
	_, written, err = repo.ReviseSalesChannel(ctx, channel.ID, next, read, time.Now())
	require.NoError(t, err)
	assert.False(t, written, "a deleted channel")
}
