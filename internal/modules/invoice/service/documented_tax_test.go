package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/invoice/models"
	"github.com/bdrtr/gobit/internal/modules/invoice/service"
)

// crowdedRepo answers the journal's read with as many documents as it may
// return, so the ceiling is all that stands between them and the caller.
type crowdedRepo struct{ *fakeRepo }

// DocumentedTax returns limit documents.
func (r crowdedRepo) DocumentedTax(context.Context, time.Time, time.Time, string, int64) ([]models.Invoice, error) {
	return make([]models.Invoice, service.MaxDocumentedTax+1), nil
}

// TestTheJournalsReadOfDocumentsRefusesWhatItCannotAnswerWhole: a window with
// an end missing, ending before it begins or wider than the journal's quarter
// is refused, and so is one holding more documents than one read returns
// (ADR 0419).
func TestTheJournalsReadOfDocumentsRefusesWhatItCannotAnswerWhole(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	start := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	svc := newService(newFakeRepo())
	for name, window := range map[string][2]time.Time{
		"no start":         {{}, start},
		"no end":           {start, {}},
		"an end before it": {start, start.Add(-time.Hour)},
		"an empty window":  {start, start},
		"past the quarter": {start, start.Add(service.MaxDocumentedTaxWindow + time.Second)},
	} {
		_, err := svc.DocumentedTax(ctx, window[0], window[1], "")
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalid(err), "%s: %v", name, err)
	}

	_, err := svc.DocumentedTax(ctx, start, start.Add(service.MaxDocumentedTaxWindow), "")
	require.NoError(t, err, "the quarter itself is read")

	crowded := service.New(crowdedRepo{newFakeRepo()}, service.Options{})
	_, err = crowded.DocumentedTax(ctx, start, start.AddDate(0, 1, 0), "")
	require.Error(t, err, "more documents than one read returns are refused rather than cut")
	assert.True(t, errors.IsInvalid(err), "%v", err)
}
