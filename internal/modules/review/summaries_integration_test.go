//go:build integration

package review_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/review/models"
	"github.com/bdrtr/gobit/internal/modules/review/service"
)

// TestAListingsRatingsAreEachProductsSummary is ADR 0449 against the real
// statement: each product named is answered as its own summary answers it,
// in the order named, the waiting review and the product without one alike.
func TestAListingsRatingsAreEachProductsSummary(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	rated, waiting, none := productID(t)+"_rated", productID(t)+"_waiting", productID(t)+"_none"

	for _, rating := range []int16{5, 4, 4} {
		stored := submit(t, svc, rated, rating)
		_, err := svc.Moderate(ctx, stored.ID, service.ModerateInput{To: models.StatusApproved})
		require.NoError(t, err)
	}
	submit(t, svc, waiting, 1)

	got, err := svc.SummarizeProducts(ctx, []string{none, rated, waiting, rated})
	require.NoError(t, err)
	assert.Equal(t, []models.Summary{
		{ProductID: none},
		{ProductID: rated, Count: 3, AverageHundredths: 433},
		{ProductID: waiting},
	}, got)
}
