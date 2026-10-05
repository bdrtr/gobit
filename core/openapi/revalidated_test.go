package openapi_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/openapi"
)

// TestRevalidatedDescribesTheTagWithoutTouchingItsInput is ADR 0391's
// description: the header a revalidating read accepts, its 304 and the ETag on
// its 200 — written onto a copy, because a description is a value a module may
// build once and hand to more than one operation.
func TestRevalidatedDescribesTheTagWithoutTouchingItsInput(t *testing.T) {
	t.Parallel()

	success := openapi.Response("A product", map[string]any{"type": "object"})
	// Spare capacity is what makes an append write into the caller's array.
	params := make([]openapi.Parameter, 1, 4)
	params[0] = openapi.Parameter{Name: "limit", In: "query"}
	input := openapi.Operation{
		Summary:    "A read",
		Parameters: params,
		Responses:  map[string]any{"200": success},
	}

	got := openapi.Revalidated(input)

	require.Len(t, got.Parameters, 2)
	header := got.Parameters[1]
	assert.Equal(t, "If-None-Match", header.Name)
	assert.Equal(t, "header", header.In)
	assert.False(t, header.Required, "a first read has no tag to send")
	assert.NotEmpty(t, header.Description)

	require.Contains(t, got.Responses, "304")
	notModified, ok := got.Responses["304"].(map[string]any)
	require.True(t, ok)
	assert.NotEmpty(t, notModified["description"])
	assert.NotContains(t, notModified, "content", "a 304 has no body")

	ok200, ok := got.Responses["200"].(map[string]any)
	require.True(t, ok)
	headers, ok := ok200["headers"].(map[string]any)
	require.True(t, ok, "the 200 has to describe the tag it carries")
	assert.Contains(t, headers, "ETag")
	assert.Equal(t, success["content"], ok200["content"], "the 200's body is unchanged")

	assert.NotContains(t, input.Responses, "304", "the caller's responses must not grow")
	assert.NotContains(t, success, "headers", "the caller's 200 must not grow")
	assert.Equal(t, openapi.Parameter{}, params[:2][1],
		"the caller's parameter array must not be written through")
}

// TestRevalidatedKeepsTheHeadersThe200AlreadyDescribes is the widening, not a
// replacement: a read that describes a header on its 200 still describes it
// after the tag is added, and the caller's own headers map does not gain the tag.
func TestRevalidatedKeepsTheHeadersThe200AlreadyDescribes(t *testing.T) {
	t.Parallel()

	link := map[string]any{"description": "The next page.", "schema": map[string]any{"type": "string"}}
	callerHeaders := map[string]any{"Link": link}
	success := map[string]any{"description": "A page", "headers": callerHeaders}

	got := openapi.Revalidated(openapi.Operation{
		Summary:   "A read",
		Responses: map[string]any{"200": success},
	})

	ok200, ok := got.Responses["200"].(map[string]any)
	require.True(t, ok)
	headers, ok := ok200["headers"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, link, headers["Link"], "the header the caller described survives")
	assert.Contains(t, headers, "ETag")

	assert.Equal(t, map[string]any{"Link": link}, callerHeaders,
		"the caller's headers map must not gain the tag")
}
