package promotion_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion"
	"github.com/bdrtr/gobit/internal/modules/promotion/api"
)

// recordingTrial is a trial flow that records what it is asked.
type recordingTrial struct {
	asked []string
}

func (f *recordingTrial) TrialPromotionJSON(
	_ context.Context, id string, from, to time.Time,
) (json.RawMessage, error) {
	f.asked = append(f.asked, id+"|"+from.Format(time.RFC3339)+"|"+to.Format(time.RFC3339))

	return json.RawMessage(`{"promotion_id":"` + id + `"}`), nil
}

// TestThePanelTriesAPromotion is ADR 0395 through the promotion module's
// panel surface: the flow's report as it is, after the endpoint's refusals of
// a future end and of an unbound flow.
func TestThePanelTriesAPromotion(t *testing.T) {
	ctx := context.Background()
	flow := &recordingTrial{}
	surface := promotion.NewAdminSurface(nil).WithTrial(flow)
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

	report, err := surface.TrialPromotionJSON(ctx, "promo_1", from, to)
	require.NoError(t, err)
	assert.JSONEq(t, `{"promotion_id":"promo_1"}`, string(report))
	assert.Equal(t, []string{"promo_1|2026-09-01T00:00:00Z|2026-10-01T00:00:00Z"}, flow.asked)

	_, err = surface.TrialPromotionJSON(ctx, "promo_1", from, time.Now().Add(time.Minute))
	require.Error(t, err)
	assert.Equal(t, api.CodeTrialInvalidPeriod, errors.CodeOf(err))
	assert.True(t, errors.IsInvalid(err))
	assert.Len(t, flow.asked, 1, "a future end asks no flow")

	_, err = promotion.NewAdminSurface(nil).TrialPromotionJSON(ctx, "promo_1", from, to)
	require.Error(t, err)
	assert.Equal(t, api.CodeTrialUnavailable, errors.CodeOf(err))
	assert.Equal(t, errors.KindInternal, errors.KindOf(err))
}
