package service_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// channelRepo revises channels as scripted over the shared fake, recording
// what it was asked to write.
type channelRepo struct {
	*fakeRepo
	written    bool
	missing    bool
	read, next models.ChannelTerms
}

func (r *channelRepo) ReviseSalesChannel(
	_ context.Context, id string, read, next models.ChannelTerms, _ time.Time,
) (models.SalesChannel, bool, error) {
	r.read, r.next = read, next
	if !r.written {
		return models.SalesChannel{}, false, nil
	}
	return models.SalesChannel{ID: id, Name: next.Name}, true, nil
}

func (r *channelRepo) GetSalesChannel(ctx context.Context, id string) (models.SalesChannel, error) {
	if r.missing {
		return models.SalesChannel{}, errors.NotFound("auth_sales_channel_not_found", "sales channel not found: %s", id)
	}
	return r.fakeRepo.GetSalesChannel(ctx, id)
}

// TestThePanelRevisesASalesChannel is ADR 0352: the read and the written
// terms cross the surface as JSON under the provider's field names and reach
// the store; a name that is empty or too long, and an id that is not a
// channel's, reach nothing; nothing written is a refusal naming that it was
// revised since, or that the channel is not there.
func TestThePanelRevisesASalesChannel(t *testing.T) {
	t.Parallel()

	repo := &channelRepo{fakeRepo: &fakeRepo{}, written: true}
	surface := service.NewAccountSurface(service.New(repo, service.Options{JWTSecret: "test-signing-secret-long-enough"}), "gobit")
	read := json.RawMessage(`{"name":"Web","description":"the shop","is_disabled":false}`)

	require.NoError(t, surface.ReviseSalesChannel(context.Background(), "sc_1", read,
		json.RawMessage(`{"name":"Web shop","description":"","is_disabled":true}`)))
	assert.Equal(t, models.ChannelTerms{Name: "Web", Description: "the shop"}, repo.read)
	assert.Equal(t, models.ChannelTerms{Name: "Web shop", IsDisabled: true}, repo.next)

	repo.next = models.ChannelTerms{}
	for label, next := range map[string]string{
		"an empty name": `{"name":" ","description":"","is_disabled":false}`,
		"a long name":   `{"name":"` + strings.Repeat("n", models.MaxNameLen+1) + `","description":"","is_disabled":false}`,
		"a long description": `{"name":"Web","description":"` + strings.Repeat("d", models.MaxDescriptionLen+1) +
			`","is_disabled":false}`,
		"no JSON": `[`,
	} {
		err := surface.ReviseSalesChannel(context.Background(), "sc_1", read, json.RawMessage(next))
		assert.True(t, errors.IsInvalid(err), "%s: %v", label, err)
	}
	err := surface.ReviseSalesChannel(context.Background(), "apikey_1", read, read)
	assert.True(t, errors.IsInvalid(err), "an id that is not a channel's: %v", err)
	assert.Equal(t, models.ChannelTerms{}, repo.next, "nothing refused reached the store")

	repo.written = false
	err = surface.ReviseSalesChannel(context.Background(), "sc_1", read, read)
	assert.Equal(t, service.CodeSalesChannelRevised, errors.CodeOf(err), "%v", err)
	repo.missing = true
	err = surface.ReviseSalesChannel(context.Background(), "sc_1", read, read)
	assert.True(t, errors.IsNotFound(err), "%v", err)
}

// madeChannelRepo records the channel the panel makes over the shared fake.
type madeChannelRepo struct {
	*fakeRepo
	made []models.SalesChannel
}

func (r *madeChannelRepo) CreateSalesChannel(_ context.Context, c models.SalesChannel) (models.SalesChannel, error) {
	r.made = append(r.made, c)
	return c, nil
}

// TestThePanelMakesASalesChannel is ADR 0353: the surface makes the channel
// with its name, description and state, returns its id, and refuses an
// empty name without making anything.
func TestThePanelMakesASalesChannel(t *testing.T) {
	t.Parallel()

	repo := &madeChannelRepo{fakeRepo: &fakeRepo{}}
	surface := service.NewAccountSurface(service.New(repo, service.Options{JWTSecret: "test-signing-secret-long-enough"}), "gobit")

	id, err := surface.MakeSalesChannel(context.Background(), "Phone", "telephone orders", true)
	require.NoError(t, err)
	require.Len(t, repo.made, 1)
	assert.Equal(t, id, repo.made[0].ID)
	assert.True(t, strings.HasPrefix(id, models.SalesChannelIDPrefix), id)
	assert.Equal(t, models.ChannelTerms{Name: "Phone", Description: "telephone orders", IsDisabled: true}, repo.made[0].Terms())

	_, err = surface.MakeSalesChannel(context.Background(), " ", "", false)
	assert.True(t, errors.IsInvalid(err), "%v", err)
	assert.Len(t, repo.made, 1, "nothing was made")
}
