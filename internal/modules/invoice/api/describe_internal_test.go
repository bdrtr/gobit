package api

import (
	"encoding/json"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/openapi"
)

// This package had no test while thirteen of the fifteen module api packages
// did, and the review module's was written the same day for the same reason:
// the guard lives PER PACKAGE and nothing asks whether a package has one.
//
// The composed application does refuse to boot on a description that matches no
// route, so the class is not open — but that answer needs the whole system, it
// only covers a module that is registered, and it arrives seconds rather than
// milliseconds after the mistake.

// documentPaths builds the document against the REAL route tree and returns its
// paths as read back from JSON.
func documentPaths(t *testing.T) map[string]any {
	t.Helper()

	doc := openapi.New("test", "v1")
	Describe(doc)

	r := chi.NewRouter()
	New(nil).Routes(r)

	raw, err := doc.Build(r)
	require.NoError(t, err)
	require.Empty(t, doc.UnmatchedDescriptions(),
		"every described endpoint must match a route; a description that matches none "+
			"never enters the document and nothing else says so")

	encoded, err := json.Marshal(raw)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(encoded, &decoded))

	paths, ok := decoded["paths"].(map[string]any)
	require.True(t, ok, "the document must have paths")

	return paths
}

// TestEveryRouteIsDescribed verifies that every endpoint this module mounts
// carries a summary.
//
// An undescribed endpoint is not absent from the document: it appears with its
// path and its security and without a body, which reads as "it exists but what
// it takes is unknown" — worse than missing, because nobody looks for it.
func TestEveryRouteIsDescribed(t *testing.T) {
	t.Parallel()

	paths := documentPaths(t)

	described := 0
	for path, operations := range paths {
		byMethod, ok := operations.(map[string]any)
		require.True(t, ok, "a path entry has to be a method map")

		for method, raw := range byMethod {
			op, ok := raw.(map[string]any)
			require.True(t, ok)

			assert.NotEmpty(t, op["summary"], "%s %s has to be described", method, path)
			described++
		}
	}

	require.Positive(t, described,
		"NOT ONE operation was found in the document; the router or the description must "+
			"have gone silent, and an empty walk passes every assertion above it")
}
