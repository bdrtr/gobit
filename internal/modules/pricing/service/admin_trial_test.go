package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// recordingListTrial is a price list trial flow that records what it is asked.
type recordingListTrial struct {
	asked []string
}

func (f *recordingListTrial) TrialPriceListJSON(
	_ context.Context, id string, from, to time.Time,
) (json.RawMessage, error) {
	f.asked = append(f.asked, id+"|"+from.Format(time.RFC3339)+"|"+to.Format(time.RFC3339))

	return json.RawMessage(`{"price_list_id":"` + id + `"}`), nil
}

// TestThePanelTriesAPriceList is ADR 0395 through the pricing module's panel
// surface: the flow's report as it is, after the endpoint's refusals of a
// future end and of an unbound flow.
func TestThePanelTriesAPriceList(t *testing.T) {
	ctx := context.Background()
	flow := &recordingListTrial{}
	surface := NewAdminSurface(nil).WithTrial(flow)
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

	report, err := surface.TrialPriceListJSON(ctx, "plist_1", from, to)
	require.NoError(t, err)
	assert.JSONEq(t, `{"price_list_id":"plist_1"}`, string(report))
	assert.Equal(t, []string{"plist_1|2026-09-01T00:00:00Z|2026-10-01T00:00:00Z"}, flow.asked)

	_, err = surface.TrialPriceListJSON(ctx, "plist_1", from, time.Now().Add(time.Minute))
	require.Error(t, err)
	assert.Equal(t, CodeListTrialInvalidPeriod, errors.CodeOf(err))
	assert.True(t, errors.IsInvalid(err))
	assert.Len(t, flow.asked, 1, "a future end asks no flow")

	_, err = NewAdminSurface(nil).TrialPriceListJSON(ctx, "plist_1", from, to)
	require.Error(t, err)
	assert.Equal(t, CodeListTrialUnavailable, errors.CodeOf(err))
	assert.Equal(t, errors.KindInternal, errors.KindOf(err))
}
