package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// keysRepo answers the key listing as scripted over the shared fake,
// recording what it was asked for and who revoked what.
type keysRepo struct {
	*fakeRepo
	keys          []models.APIKey
	count         int64
	filter        models.APIKeyFilter
	limit, offset int64
	revokedBy     string
}

func (r *keysRepo) ListAPIKeys(
	_ context.Context, filter models.APIKeyFilter, limit, offset int64,
) ([]models.APIKey, int64, error) {
	r.filter, r.limit, r.offset = filter, limit, offset
	return r.keys, r.count, nil
}

func (r *keysRepo) RevokeAPIKey(ctx context.Context, id, revokedBy string, now time.Time) (models.APIKey, error) {
	r.revokedBy = revokedBy
	return r.fakeRepo.RevokeAPIKey(ctx, id, revokedBy, now)
}

// TestThePanelListsAndRevokesTheKeys is ADR 0350: the surface lists a page of
// the keys with how many there are, each with its token only as redacted,
// those still accepted or those revoked or every one, refusing a tab there
// is not; and it revokes a key in the operator's name.
func TestThePanelListsAndRevokesTheKeys(t *testing.T) {
	t.Parallel()

	used := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	repo := &keysRepo{fakeRepo: &fakeRepo{}, count: 3, keys: []models.APIKey{{
		ID: "apikey_1", Type: models.APIKeySecret, Title: "ERP", TokenHash: "never-shown", Redacted: "sk_…a1b2",
		Scopes: []string{"order:read"}, CreatedBy: "user_1", LastUsedAt: &used,
		CreatedAt: time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC),
	}, {
		ID: "apikey_2", Type: models.APIKeyPublishable, Title: "Shop", Redacted: "pk_…c3d4", CreatedBy: "user_1",
		RevokedAt: &used, RevokedBy: "user_3", CreatedAt: time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC),
	}}}
	surface := service.NewAccountSurface(service.New(repo, service.Options{JWTSecret: "test-signing-secret-long-enough"}), "gobit")

	raw, total, err := surface.APIKeysJSON(context.Background(), service.KeysOpen, 25, 25)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.JSONEq(t, `[{"id":"apikey_1","type":"secret","title":"ERP","redacted":"sk_…a1b2","scopes":["order:read"],
		"created_by":"user_1","last_used_at":"2026-09-30T12:00:00Z","revoked_at":null,"revoked_by":"",
		"created_at":"2026-09-01T08:00:00Z"},{"id":"apikey_2","type":"publishable","title":"Shop","redacted":"pk_…c3d4",
		"scopes":[],"created_by":"user_1","last_used_at":null,"revoked_at":"2026-09-30T12:00:00Z","revoked_by":"user_3",
		"created_at":"2026-08-01T08:00:00Z"}]`, string(raw))
	assert.NotContains(t, string(raw), "never-shown", "the token's hash does not cross")
	require.NotNil(t, repo.filter.Revoked)
	assert.False(t, *repo.filter.Revoked, "the keys still accepted")
	assert.Equal(t, []int64{25, 25}, []int64{repo.limit, repo.offset})

	_, _, err = surface.APIKeysJSON(context.Background(), service.KeysRevoked, 25, 0)
	require.NoError(t, err)
	require.NotNil(t, repo.filter.Revoked)
	assert.True(t, *repo.filter.Revoked)
	_, _, err = surface.APIKeysJSON(context.Background(), "", 25, 0)
	require.NoError(t, err)
	assert.Nil(t, repo.filter.Revoked, "every key")
	_, _, err = surface.APIKeysJSON(context.Background(), "maybe", 25, 0)
	assert.True(t, errors.IsInvalid(err), "%v", err)

	require.NoError(t, surface.RevokeAPIKey(context.Background(), "apikey_1", "user_7"))
	assert.Equal(t, "user_7", repo.revokedBy, "in the operator's name")
	err = surface.RevokeAPIKey(context.Background(), "user_1", "user_7")
	assert.True(t, errors.IsInvalid(err), "an id that is not a key's: %v", err)
}
