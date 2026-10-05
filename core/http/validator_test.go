package http_test

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// The validator a catalog read answers with (ADR 0391).
//
// Every test here is about one of two failures, and they are not symmetric. A
// tag that misses when it should match costs a transfer. A 304 sent when the
// bytes differ hands a client a page it does not have, and nothing downstream
// can tell — so the tests that refuse a match are the ones that carry the
// decision.

// validatedBody is the value the tests write.
var validatedBody = map[string]any{"data": map[string]any{"id": "prod_1", "title": "Shirt"}}

// validated writes v through the validator for a request carrying the given
// If-None-Match lines, one header line each.
func validated(t *testing.T, v any, ifNoneMatch ...string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/store/v1/anything", http.NoBody)
	for _, line := range ifNoneMatch {
		req.Header.Add("If-None-Match", line)
	}
	rec := httptest.NewRecorder()
	corehttp.WriteJSONWithValidator(rec, req, v)

	return rec
}

// tagOf answers v once with no condition and returns the tag it carried.
func tagOf(t *testing.T, v any) string {
	t.Helper()

	rec := validated(t, v)
	require.Equal(t, http.StatusOK, rec.Code)
	tag := rec.Header().Get("ETag")
	require.NotEmpty(t, tag, "a success written through the validator must carry a tag")

	return tag
}

// TestTheTagIsTheBodysOwnHash pins what the tag is computed over: the bytes the
// client receives, and nothing the caller could hand in beside them.
func TestTheTagIsTheBodysOwnHash(t *testing.T) {
	t.Parallel()

	rec := validated(t, validatedBody)

	require.Equal(t, http.StatusOK, rec.Code)
	sum := sha256.Sum256(rec.Body.Bytes())
	assert.Equal(t, `"`+hex.EncodeToString(sum[:16])+`"`, rec.Header().Get("ETag"),
		"the tag is the hash of the body sent, so it can never confirm bytes the read would not send")
	assert.False(t, strings.HasPrefix(rec.Header().Get("ETag"), "W/"),
		"the tag is computed over the exact bytes, so it is strong")
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.NotEqual(t, tagOf(t, validatedBody), tagOf(t, map[string]any{"data": "other"}),
		"two bodies must carry two tags")
}

// TestAMatchingTagIsAnsweredWithNoBody is the 304 itself.
func TestAMatchingTagIsAnsweredWithNoBody(t *testing.T) {
	t.Parallel()

	tag := tagOf(t, validatedBody)

	req := httptest.NewRequest(http.MethodGet, "/store/v1/anything", http.NoBody)
	req.Header.Set("If-None-Match", tag)
	rec := httptest.NewRecorder()
	// A header the caller set before the writer runs, as the catalog sets its
	// freshness policy, rides on the 304 as it would on the 200.
	rec.Header().Set("Cache-Control", "private, max-age=60")
	corehttp.WriteJSONWithValidator(rec, req, validatedBody)

	require.Equal(t, http.StatusNotModified, rec.Code)
	assert.Empty(t, rec.Body.Bytes(), "a 304 carries no body")
	assert.Equal(t, tag, rec.Header().Get("ETag"), "a 304 carries the tag the 200 would")
	assert.Empty(t, rec.Header().Get("Content-Type"), "a 304 describes no body")
	assert.Equal(t, "private, max-age=60", rec.Header().Get("Cache-Control"),
		"a 304 refreshes the stored copy with the 200's headers")
}

// TestADifferentTagGetsTheBody is the refusal that carries the decision: a tag
// the body does not have is answered with the body.
func TestADifferentTagGetsTheBody(t *testing.T) {
	t.Parallel()

	tag := tagOf(t, map[string]any{"data": "what the client held"})

	rec := validated(t, validatedBody, tag)

	require.Equal(t, http.StatusOK, rec.Code, "a changed body must be sent, not confirmed")
	assert.JSONEq(t, `{"data":{"id":"prod_1","title":"Shirt"}}`, rec.Body.String())
	assert.Equal(t, tagOf(t, validatedBody), rec.Header().Get("ETag"))
}

// TestTheComparisonIsWeak is RFC 9110 §13.1.2: If-None-Match compares weakly,
// so a tag a proxy weakened on the way still matches.
func TestTheComparisonIsWeak(t *testing.T) {
	t.Parallel()

	tag := tagOf(t, validatedBody)

	rec := validated(t, validatedBody, "W/"+tag)

	assert.Equal(t, http.StatusNotModified, rec.Code)
}

// TestEveryMemberIsSearched reads the whole list, in one line or in several.
func TestEveryMemberIsSearched(t *testing.T) {
	t.Parallel()

	tag := tagOf(t, validatedBody)

	assert.Equal(t, http.StatusNotModified,
		validated(t, validatedBody, `"a", `+tag+`, "b"`).Code,
		"a member after the first must be read")
	assert.Equal(t, http.StatusNotModified,
		validated(t, validatedBody, `"a"`, tag).Code,
		"a second header line must be read")
}

// TestAStarMatches is the one member that names no tag.
func TestAStarMatches(t *testing.T) {
	t.Parallel()

	assert.Equal(t, http.StatusNotModified, validated(t, validatedBody, "*").Code)
}

// TestAnUnencodableValueIsStillA500 keeps the encode-first rule: the tag is
// written only after the body exists, so a failure is a plain 500 and never a
// 304, whatever the request sent.
func TestAnUnencodableValueIsStillA500(t *testing.T) {
	t.Parallel()

	rec := validated(t, map[string]any{"data": func() {}}, "*")

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Empty(t, rec.Header().Get("ETag"), "a failure carries no tag")
	assert.Contains(t, rec.Body.String(), "internal_error")
}
