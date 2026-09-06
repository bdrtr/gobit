package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/openapi"
)

// This module had NO test in its api package while the other fifteen did, and
// the gap is the one this repository has paid for before: a module written,
// documented and covered end to end still misses a guard its peers have,
// because the guard lives per package and nothing asks whether a NEW package
// got one.
//
// The composed application does catch a description that matches no route —
// the composition root refuses to boot on it — but that is a slow, whole-system
// answer to a question this package can answer in milliseconds, and it only
// covers a module that is REGISTERED. An installation that deletes the review
// line, which the module's own godoc invites, would take the check with it.

// documentPaths builds the document against the REAL route tree and returns its
// paths as read back from JSON.
//
// The router has to be real: if a description and its route drift apart, the
// failure belongs here rather than in front of somebody reading /openapi.json.
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
// carries a summary in the document.
//
// An undescribed endpoint is not absent from the document — it appears with its
// path and its security and without a body, which reads as "it exists but what
// it takes is unknown". That is worse than missing, because nobody looks for it.
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

// TestTheStorefrontIsNotOfferedAStatusParameter is the document's half of this
// module's one guarantee.
//
// The module's claim is that an unapproved review is INVISIBLE on the
// storefront, and the SQL keeps it — the storefront reads carry the approved
// status as a literal. This is the other side of the same promise: the document
// must not hand a shopper a parameter that looks like it could ask for
// something else. A client generator turns every described parameter into an
// argument, and an argument named "status" on a storefront listing is an
// invitation to try.
func TestTheStorefrontIsNotOfferedAStatusParameter(t *testing.T) {
	t.Parallel()

	paths := documentPaths(t)

	for path, operations := range paths {
		if !isStorefrontPath(path) {
			continue
		}

		byMethod, ok := operations.(map[string]any)
		require.True(t, ok)

		for method, raw := range byMethod {
			op, ok := raw.(map[string]any)
			require.True(t, ok)

			for _, name := range queryParameterNames(t, op) {
				assert.NotEqual(t, "status", name,
					"%s %s must not offer a status parameter: the storefront sees approved "+
						"reviews and nothing else, and a parameter says otherwise", method, path)
			}
		}
	}
}

// isStorefrontPath reports whether the path is served to a shopper.
func isStorefrontPath(path string) bool {
	const storePrefix = "/store/v1/"

	return len(path) >= len(storePrefix) && path[:len(storePrefix)] == storePrefix
}

// queryParameterNames returns the names of an operation's query parameters.
func queryParameterNames(t *testing.T, op map[string]any) []string {
	t.Helper()

	raw, ok := op["parameters"].([]any)
	if !ok {
		return nil
	}

	var names []string
	for _, entry := range raw {
		parameter, ok := entry.(map[string]any)
		require.True(t, ok, "a parameter entry has to be an object")

		if parameter["in"] != "query" {
			continue
		}
		name, ok := parameter["name"].(string)
		require.True(t, ok, "a parameter has to carry a name")
		names = append(names, name)
	}

	return names
}

// TestTheAdminListingOffersTheStatusFilter is the counterpart, and it is what
// makes the test above mean something.
//
// Without it, deleting the status parameter from the ADMIN listing would leave
// this file green while the moderation queue lost the filter it exists for — an
// assertion that only forbids is satisfied by a document with nothing in it.
func TestTheAdminListingOffersTheStatusFilter(t *testing.T) {
	t.Parallel()

	paths := documentPaths(t)

	entry, ok := paths[pathAdminReviews].(map[string]any)
	require.True(t, ok, "%q must be in the document", pathAdminReviews)

	get, ok := entry[http.MethodGet].(map[string]any)
	if !ok {
		get, ok = entry["get"].(map[string]any)
	}
	require.True(t, ok, "GET must be described for %q", pathAdminReviews)

	assert.Contains(t, queryParameterNames(t, get), "status",
		"the moderation queue is the one listing that filters by status")
}
