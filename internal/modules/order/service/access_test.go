package service_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestAnOrderOpensToEveryTokenItIssued is the token's life at the service
// (ADR 0439): each issue is a new token, an earlier one keeps opening the
// order, and the order keeps digests only.
func TestAnOrderOpensToEveryTokenItIssued(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	env := newEnv(t)
	ord, err := env.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	completion, err := env.svc.IssueAccessToken(ctx, ord.ID)
	require.NoError(t, err)
	mail, err := env.svc.IssueAccessToken(ctx, ord.ID)
	require.NoError(t, err)
	require.NotEqual(t, completion, mail)

	for _, token := range []string{completion, mail} {
		access, err := env.svc.StorefrontAccess(ctx, ord.ID, token)
		require.NoError(t, err)
		assert.True(t, access.TokenMatches, "a token the order issued")
		assert.Equal(t, testCustomerID, access.CustomerID)
	}
	for _, digest := range env.store.tokens[ord.ID] {
		assert.NotEqual(t, []byte(completion), digest, "the order keeps the plaintext")
		assert.NotEqual(t, []byte(mail), digest, "the order keeps the plaintext")
	}
	assert.Len(t, env.store.tokens[ord.ID], 2)
}

// TestAnOrderDoesNotOpenToAnotherOrdersToken holds the token to its order:
// the lookup is by the order the path names, and B's token says nothing
// about A.
func TestAnOrderDoesNotOpenToAnotherOrdersToken(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	env := newEnv(t)
	first, err := env.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	second, err := env.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	secondToken, err := env.svc.IssueAccessToken(ctx, second.ID)
	require.NoError(t, err)
	_, err = env.svc.IssueAccessToken(ctx, first.ID)
	require.NoError(t, err)

	access, err := env.svc.StorefrontAccess(ctx, first.ID, secondToken)
	require.NoError(t, err)
	assert.False(t, access.TokenMatches, "another order's token opened this one")

	access, err = env.svc.StorefrontAccess(ctx, first.ID, "")
	require.NoError(t, err)
	assert.False(t, access.TokenMatches, "no token matched")
}

// TestAnUnknownOrderIssuesAndOpensNothing keeps the not-found the routes'
// refusals are answered with.
func TestAnUnknownOrderIssuesAndOpensNothing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	env := newEnv(t)

	_, err := env.svc.StorefrontAccess(ctx, "order_NOPE", "ordtok_x")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "%v", err)

	_, err = env.svc.IssueAccessToken(ctx, "order_NOPE")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "%v", err)
	assert.Empty(t, env.store.tokens)
}

// TestAnErasureWithdrawsTheOrdersTokens is the erasure's half: a forgotten
// person's links stop opening the order, and the report's rows do not count
// the tokens, so a second sweep answers the same.
func TestAnErasureWithdrawsTheOrdersTokens(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	env := newEnv(t)
	in := refundableInput()
	ord, err := env.svc.CreateOrder(ctx, in)
	require.NoError(t, err)
	_, err = env.svc.SetOrderSummaryTotals(ctx, ord.ID, service.SummaryTotalsInput{PaidTotal: in.Total})
	require.NoError(t, err)
	_, err = env.svc.CompleteOrder(ctx, ord.ID)
	require.NoError(t, err)
	token, err := env.svc.IssueAccessToken(ctx, ord.ID)
	require.NoError(t, err)

	first, err := env.svc.Erase(ctx, personaldata.Subject{CustomerID: testCustomerID})
	require.NoError(t, err)
	require.Equal(t, personaldata.Anonymized, first.Outcome, first.Why)

	access, err := env.svc.StorefrontAccess(ctx, ord.ID, token)
	require.NoError(t, err)
	assert.False(t, access.TokenMatches, "an erased order still opens to its link")

	second, err := env.svc.Erase(ctx, personaldata.Subject{CustomerID: testCustomerID})
	require.NoError(t, err)
	assert.Equal(t, first.Rows, second.Rows, "the withdrawn tokens changed the report")
}

// TestAnAccessTokenHasNoPlaceInTheOrdersRecord pins the model: no field of
// the order carries a token or a digest, so no record built from it can.
func TestAnAccessTokenHasNoPlaceInTheOrdersRecord(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	env := newEnv(t)
	ord, err := env.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	token, err := env.svc.IssueAccessToken(ctx, ord.ID)
	require.NoError(t, err)

	detail, err := env.svc.GetOrder(ctx, ord.ID)
	require.NoError(t, err)
	record := fmt.Sprintf("%+v", detail)
	assert.NotContains(t, record, token)
	assert.NotContains(t, record, models.AccessTokenPrefix)
}

// TestAnErasureWithdrawsARetainedOrdersTokens: an order the erasure keeps,
// because it is still being performed, keeps its contact for the shop and
// loses its links, which were the person's access.
func TestAnErasureWithdrawsARetainedOrdersTokens(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	env := newEnv(t)
	ord, err := env.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	token, err := env.svc.IssueAccessToken(ctx, ord.ID)
	require.NoError(t, err)

	result, err := env.svc.Erase(ctx, personaldata.Subject{CustomerID: testCustomerID})
	require.NoError(t, err)
	require.Equal(t, personaldata.Retained, result.Outcome, "a pending order is retained")

	access, err := env.svc.StorefrontAccess(ctx, ord.ID, token)
	require.NoError(t, err)
	assert.False(t, access.TokenMatches, "a retained order still opens to the forgotten person's link")
	assert.Equal(t, testCustomerID, access.CustomerID, "the customer's own proof still opens it")
}
