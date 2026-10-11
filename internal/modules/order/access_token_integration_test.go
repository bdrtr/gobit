//go:build integration

package order_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// The order's access tokens on the real database (ADR 0439): the table keeps
// digests only, a token opens its own order and no other, an unknown order
// issues none, and an erasure withdraws them.

// TestAnOrderKeepsTheDigestOfEachTokenAndNothingElse issues two tokens and
// reads the table: two rows, each the SHA-256 of its token, neither the
// plaintext, and both opening the order.
func TestAnOrderKeepsTheDigestOfEachTokenAndNothingElse(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	ord, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	first, err := svc.IssueAccessToken(ctx, ord.ID)
	require.NoError(t, err)
	second, err := svc.IssueAccessToken(ctx, ord.ID)
	require.NoError(t, err)

	rows, err := testPool.Pool().Query(ctx,
		`SELECT token_hash FROM order_access_tokens WHERE order_id = $1`, ord.ID)
	require.NoError(t, err)
	var stored [][]byte
	for rows.Next() {
		var digest []byte
		require.NoError(t, rows.Scan(&digest))
		stored = append(stored, digest)
	}
	rows.Close()
	require.NoError(t, rows.Err())
	assert.ElementsMatch(t, [][]byte{models.AccessTokenDigest(first), models.AccessTokenDigest(second)}, stored,
		"the table keeps each token's digest, and only that")

	for _, token := range []string{first, second} {
		access, err := svc.StorefrontAccess(ctx, ord.ID, token)
		require.NoError(t, err)
		assert.True(t, access.TokenMatches)
		assert.Equal(t, testCustomerID, access.CustomerID)
	}
}

// TestATokenOpensItsOwnOrderOnly holds the lookup to the order the path
// names: another order's token, and a token's digest sent as a token, open
// nothing.
func TestATokenOpensItsOwnOrderOnly(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	mine, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	theirs, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	_, err = svc.IssueAccessToken(ctx, mine.ID)
	require.NoError(t, err)
	theirToken, err := svc.IssueAccessToken(ctx, theirs.ID)
	require.NoError(t, err)

	for name, token := range map[string]string{
		"another order's token": theirToken,
		"a digest as a token":   string(models.AccessTokenDigest(theirToken)),
		"no token":              "",
	} {
		access, err := svc.StorefrontAccess(ctx, mine.ID, token)
		require.NoError(t, err, name)
		assert.False(t, access.TokenMatches, "%s opened the order", name)
	}
}

// TestAnUnknownOrderIssuesNoToken is the foreign key's answer: NotFound, the
// class the storefront routes answer an unknown id with, and no row.
func TestAnUnknownOrderIssuesNoToken(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	_, err := svc.IssueAccessToken(ctx, "order_NOPE_ACCESS")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "%v", err)

	var count int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM order_access_tokens WHERE order_id = 'order_NOPE_ACCESS'`).Scan(&count))
	assert.Zero(t, count)
}

// TestTheTableRefusesAnythingButADigest is the CHECK: a value that is not 32
// bytes long cannot be stored, so a plaintext written by mistake is refused
// rather than kept.
func TestTheTableRefusesAnythingButADigest(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	ord, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO order_access_tokens (order_id, token_hash) VALUES ($1, $2)`,
		ord.ID, []byte(models.AccessTokenPrefix+"plaintext-written-by-mistake"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `check constraint "order_access_tokens_hash_length"`)
}

// TestAnErasureWithdrawsTheForgottenOrdersTokens is the erasure's half: the
// settled order's tokens are deleted in its transaction, and the report's rows
// do not count them.
func TestAnErasureWithdrawsTheForgottenOrdersTokens(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	const (
		customerID = "cus_ERASE_TOKENS"
		email      = "token.person@example.com"
	)
	ord := placeOrderFor(t, svc, customerID, email)
	settleOrder(t, svc, ord)
	token, err := svc.IssueAccessToken(ctx, ord.ID)
	require.NoError(t, err)

	result, err := svc.Erase(ctx, personaldata.Subject{CustomerID: customerID, Email: email})
	require.NoError(t, err)
	require.Equal(t, personaldata.Anonymized, result.Outcome, result.Why)
	assert.Equal(t, 3, result.Rows, "the tokens are not rows about the person")

	access, err := svc.StorefrontAccess(ctx, ord.ID, token)
	require.NoError(t, err)
	assert.False(t, access.TokenMatches, "a forgotten person's link still opens the order")

	var count int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM order_access_tokens WHERE order_id = $1`, ord.ID).Scan(&count))
	assert.Zero(t, count)
}

// TestAnErasureWithdrawsARetainedOrdersTokensOnTheDatabase: the pending order
// the erasure keeps keeps its contact and loses its links.
func TestAnErasureWithdrawsARetainedOrdersTokensOnTheDatabase(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	const (
		customerID = "cus_ERASE_RETAINED_TOKENS"
		email      = "retained.token.person@example.com"
	)
	ord := placeOrderFor(t, svc, customerID, email)
	token, err := svc.IssueAccessToken(ctx, ord.ID)
	require.NoError(t, err)

	result, err := svc.Erase(ctx, personaldata.Subject{CustomerID: customerID, Email: email})
	require.NoError(t, err)
	require.Equal(t, personaldata.Retained, result.Outcome, result.Why)

	access, err := svc.StorefrontAccess(ctx, ord.ID, token)
	require.NoError(t, err)
	assert.False(t, access.TokenMatches, "the retained order still opens to the forgotten person's link")

	var count int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM order_access_tokens WHERE order_id = $1`, ord.ID).Scan(&count))
	assert.Zero(t, count)
}
