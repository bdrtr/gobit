package models_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// TestAnAccessTokenIsRandomAndKeptAsItsDigest is the token's shape (ADR 0439):
// the prefix, 256 random bits, and a digest that is the token's and no other
// token's.
func TestAnAccessTokenIsRandomAndKeptAsItsDigest(t *testing.T) {
	first, firstDigest, err := models.NewAccessToken()
	require.NoError(t, err)
	second, secondDigest, err := models.NewAccessToken()
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(first, models.AccessTokenPrefix), first)
	assert.Len(t, strings.TrimPrefix(first, models.AccessTokenPrefix), 43, "32 bytes, unpadded base64url")
	assert.NotEqual(t, first, second, "two tokens are two random draws")
	assert.Len(t, firstDigest, models.AccessTokenDigestBytes)
	assert.Equal(t, models.AccessTokenDigest(first), firstDigest, "the digest kept is the token's")
	assert.NotEqual(t, firstDigest, secondDigest)
	assert.NotContains(t, string(firstDigest), first, "the digest is not the token")
}

// TestAnAccessTokenMatchesOnlyADigestTheOrderKept is the comparison: a token
// matches when its digest is among the order's, wherever it stands, and
// nothing else matches — another order's token, a token's digest presented as
// a token, an empty header against no digests and against the empty string's.
func TestAnAccessTokenMatchesOnlyADigestTheOrderKept(t *testing.T) {
	token, digest, err := models.NewAccessToken()
	require.NoError(t, err)
	other, otherDigest, err := models.NewAccessToken()
	require.NoError(t, err)

	assert.True(t, models.AccessTokenMatches(token, [][]byte{digest}))
	assert.True(t, models.AccessTokenMatches(token, [][]byte{otherDigest, digest}),
		"a later issue keeps the earlier token valid")
	assert.True(t, models.AccessTokenMatches(token, [][]byte{digest, otherDigest}),
		"the first digest matching does not stop the walk from answering yes")

	assert.False(t, models.AccessTokenMatches(other, [][]byte{digest}), "another order's token")
	assert.False(t, models.AccessTokenMatches(token, nil), "an order that issued none")
	assert.False(t, models.AccessTokenMatches(string(digest), [][]byte{digest}), "the digest is no token")
	assert.False(t, models.AccessTokenMatches("", nil))
	assert.False(t, models.AccessTokenMatches("", [][]byte{models.AccessTokenDigest("")}),
		"a request that presents no token presents none, whatever the order keeps")
	assert.False(t, models.AccessTokenMatches(token, [][]byte{digest[:16]}), "a truncated digest")
}
