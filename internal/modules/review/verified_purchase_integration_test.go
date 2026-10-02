//go:build integration

package review_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/review/models"
	"github.com/bdrtr/gobit/internal/modules/review/repository"
	"github.com/bdrtr/gobit/internal/modules/review/service"
)

// buyers answers a purchase from a fixed set of customers.
type buyers map[string]bool

func (b buyers) Bought(_ context.Context, customerID, _ string) (bool, error) {
	return b[customerID], nil
}

// TestAVerifiedPurchaseIsKeptAndPublished is ADR 0372 against a real
// PostgreSQL: a proven buyer's review is stored a verified purchase and keeps
// the badge through approval into the storefront listing; a stranger's is
// stored unverified; and the row holds no column naming either writer.
func TestAVerifiedPurchaseIsKeptAndPublished(t *testing.T) {
	ctx := context.Background()
	svc := service.New(repository.New(testPool.Pool()), service.Options{Purchases: buyers{"cust_BUYER": true}})
	product := productID(t)

	write := func(customerID string) models.Review {
		t.Helper()
		stored, err := svc.Submit(ctx, service.SubmitInput{
			ProductID: product, Rating: 5, Body: "it arrived quickly", AuthorName: "A customer", CustomerID: customerID,
		})
		require.NoError(t, err)

		return stored
	}
	verified := write("cust_BUYER")
	stranger := write("")
	assert.True(t, verified.VerifiedPurchase)
	assert.False(t, stranger.VerifiedPurchase)

	for _, review := range []models.Review{verified, stranger} {
		_, err := svc.Moderate(ctx, review.ID, service.ModerateInput{To: models.StatusApproved})
		require.NoError(t, err)
	}
	page, err := svc.ListApproved(ctx, product, models.Filter{Limit: 10})
	require.NoError(t, err)
	badges := map[string]bool{}
	for _, review := range page.Items {
		badges[review.ID] = review.VerifiedPurchase
	}
	assert.Equal(t, map[string]bool{verified.ID: true, stranger.ID: false}, badges)

	var columns []string
	rows, err := testPool.Pool().Query(ctx,
		`SELECT column_name FROM information_schema.columns WHERE table_name = 'reviews' AND column_name LIKE '%customer%'`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		columns = append(columns, name)
	}
	require.NoError(t, rows.Err())
	assert.Empty(t, columns, "the customer is asked about and not kept")
}
