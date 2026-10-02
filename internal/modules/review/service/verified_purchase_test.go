package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/review/service"
)

// boughtBy answers a purchase from a fixed list, and counts how often it was
// asked.
type boughtBy struct {
	buyers map[string]bool
	err    error
	asked  int
}

func (b *boughtBy) Bought(_ context.Context, customerID, productID string) (bool, error) {
	b.asked++
	if b.err != nil {
		return false, b.err
	}

	return b.buyers[customerID+"|"+productID], nil
}

// TestAProvenBuyersReviewIsAVerifiedPurchase is ADR 0372: a review whose
// request proved a customer who bought the product is marked so; one who did
// not buy it, and a writer the request proved nobody about, are not — and a
// writer nobody proved is not even asked about.
func TestAProvenBuyersReviewIsAVerifiedPurchase(t *testing.T) {
	t.Parallel()

	purchases := &boughtBy{buyers: map[string]bool{"cust_BUYER|prod_1": true}}
	svc := service.New(newFakeRepo(), service.Options{Purchases: purchases})
	ctx := context.Background()

	for name, tc := range map[string]struct {
		customer string
		verified bool
	}{
		"a proven buyer":                    {"cust_BUYER", true},
		"a proven customer who did not buy": {"cust_BROWSER", false},
	} {
		in := validSubmission()
		in.CustomerID = tc.customer
		review, err := svc.Submit(ctx, in)
		require.NoError(t, err, name)
		assert.Equal(t, tc.verified, review.VerifiedPurchase, name)
	}
	asked := purchases.asked

	review, err := svc.Submit(ctx, validSubmission())
	require.NoError(t, err)
	assert.False(t, review.VerifiedPurchase, "a writer nobody proved")
	assert.Equal(t, asked, purchases.asked, "and nobody's purchase is asked about")
}

// TestAPurchaseThatCannotBeReadWritesNothing: a failure to read the writer's
// orders ends the submission instead of writing the review unverified, since a
// buyer's badge cannot be added afterwards.
func TestAPurchaseThatCannotBeReadWritesNothing(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	svc := service.New(repo, service.Options{
		Purchases: &boughtBy{err: errors.Unavailable("orders_down", "the orders could not be read")},
	})
	in := validSubmission()
	in.CustomerID = "cust_BUYER"

	_, err := svc.Submit(context.Background(), in)
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable), "%v", err)
	assert.Empty(t, repo.reviews, "nothing was written")
}

// TestAServiceWithNoPurchasesWritesUnverified: a composition that cannot ask
// writes every review unverified rather than refusing it.
func TestAServiceWithNoPurchasesWritesUnverified(t *testing.T) {
	t.Parallel()

	svc, _ := newService()
	in := validSubmission()
	in.CustomerID = "cust_BUYER"

	review, err := svc.Submit(context.Background(), in)
	require.NoError(t, err)
	assert.False(t, review.VerifiedPurchase)
}
