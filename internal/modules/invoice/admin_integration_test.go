//go:build integration

package invoice_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/invoice/service"
)

// TestThePanelListsTheSeriesOnTheRealSchema is ADR 0335 against a real
// PostgreSQL: a series opened by its first invoice is listed with the last
// number it handed out, the latest year first and the prefixes in order
// within a year.
func TestThePanelListsTheSeriesOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	surface := service.NewAdminSurface(svc)

	for _, prefix := range []string{"ZZB", "ZZA", "ZZB"} {
		_, err := svc.Issue(ctx, issueFor(prefix))
		require.NoError(t, err)
	}
	raw, err := surface.SeriesJSON(ctx)
	require.NoError(t, err)
	var series []struct {
		Prefix     string `json:"prefix"`
		Year       int32  `json:"year"`
		LastNumber int64  `json:"last_number"`
	}
	require.NoError(t, json.Unmarshal(raw, &series))
	last := map[string]int64{}
	var order []string
	for i, s := range series {
		if i > 0 {
			assert.GreaterOrEqual(t, series[i-1].Year, s.Year, "the latest year first")
		}
		if s.Prefix == "ZZA" || s.Prefix == "ZZB" {
			last[s.Prefix] = s.LastNumber
			order = append(order, s.Prefix)
		}
	}
	assert.Equal(t, []string{"ZZA", "ZZB"}, order, "the prefixes in order within a year")
	assert.GreaterOrEqual(t, last["ZZB"], int64(2), "the last number a series handed out")
	assert.GreaterOrEqual(t, last["ZZA"], int64(1))
}
