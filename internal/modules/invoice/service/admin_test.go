package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/invoice/service"
)

// TestThePanelListsTheSeries is ADR 0335: the surface lists each series with
// its year and the last number it handed out, which is what the order page
// offers an invoice on.
func TestThePanelListsTheSeries(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	svc := newService(repo)
	surface := service.NewAdminSurface(svc)

	raw, err := surface.SeriesJSON(context.Background())
	require.NoError(t, err)
	assert.JSONEq(t, `[]`, string(raw), "a shop that has issued nothing has no series")

	for range 2 {
		_, err = svc.Issue(context.Background(), validIssue())
		require.NoError(t, err)
	}
	raw, err = surface.SeriesJSON(context.Background())
	require.NoError(t, err)
	var series []struct {
		Prefix     string `json:"prefix"`
		Year       int32  `json:"year"`
		LastNumber int64  `json:"last_number"`
	}
	require.NoError(t, json.Unmarshal(raw, &series))
	require.Len(t, series, 1)
	assert.Equal(t, "GBT", series[0].Prefix)
	assert.Equal(t, int32(2026), series[0].Year)
	assert.Equal(t, int64(2), series[0].LastNumber)
}
