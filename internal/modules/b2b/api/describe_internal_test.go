package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/openapi"
)

// The test is in the INTERNAL package because the bodies being described
// ([companyRequest], [employeeDTO] …) are unexported. The only way to exercise
// them from the outside would be to export the types; widening the module's
// surface for the sake of exercising the document would break the very thing
// being exercised.

// buildDoc produces Describe's output against the REAL route tree and returns
// it as read back from JSON.
//
// The router has to be real: the moment the description's path drifts from
// the route's, let the failure show up HERE, not in somebody looking at
// /openapi.json in production.
func buildDoc(t *testing.T) map[string]any {
	t.Helper()

	doc := openapi.New("test", "v1")
	// The namespace the composition root applies to this module (ADR 0036).
	// Without it the test would build a document whose component names differ
	// from the shipped one by exactly the prefix that IS the published
	// contract. The name is a literal because an api package cannot import
	// the module package that holds the constant — that import goes the other
	// way. What keeps the literal honest is the audit in internal/app, which
	// reads the REAL registry.
	doc.ForModule("b2b", func() { Describe(doc) })

	r := chi.NewRouter()
	New(nil, nil, false).Routes(r)

	raw, err := doc.Build(r)
	require.NoError(t, err)
	require.Empty(t, doc.UnmatchedDescriptions(),
		"every described endpoint has to match a route; an unmatched record never enters the document")

	encoded, err := json.Marshal(raw)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(encoded, &decoded))

	paths, ok := decoded["paths"].(map[string]any)
	require.True(t, ok)
	return paths
}

// TestEveryEndpointIsDescribed verifies that the document COINCIDES with the
// route tree.
//
// Both directions are needed, and each closes a separate silence: an endpoint
// that is described but has no route never enters the document (Build drops
// it), while an endpoint that has a route but is not described shows its PATH
// in the document but not its body — a client generator generates that method
// without arguments, and why it does not work is only found out at run time.
func TestEveryEndpointIsDescribed(t *testing.T) {
	paths := buildDoc(t)

	r := chi.NewRouter()
	New(nil, nil, false).Routes(r)

	err := chi.Walk(r, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		operations, ok := paths[pattern].(map[string]any)
		require.True(t, ok, "%s is not in the document", pattern)

		op, ok := operations[strings.ToLower(method)].(map[string]any)
		require.True(t, ok, "%s %s is not in the document", method, pattern)
		assert.NotEmpty(t, op["summary"], "no summary was written for %s %s", method, pattern)
		return nil
	})
	require.NoError(t, err)
}

// TestStoreResponseCarriesTheWindowFields verifies that the storefront schema
// contains the fields it leaves for the next step.
//
// The fields' names are the PUBLISHED contract: the client that will enforce
// the spending limit is going to read exactly these.
func TestStoreResponseCarriesTheWindowFields(t *testing.T) {
	paths := buildDoc(t)

	operations, ok := paths["/store/v1/b2b/customers/{customer_id}/employee"].(map[string]any)
	require.True(t, ok)

	op, ok := operations["get"].(map[string]any)
	require.True(t, ok)
	assert.NotEmpty(t, op["responses"])

	// The schema component is derived from the body; the source of the field
	// names is the DTO's json tags, and this pins that they are really written.
	raw, err := json.Marshal(storeEmployeeDTO{})
	require.NoError(t, err)

	var fields map[string]any
	require.NoError(t, json.Unmarshal(raw, &fields))
	for _, name := range []string{
		"spending_limit", "spending_limit_reset_period", "spending_window_start",
	} {
		assert.Contains(t, fields, name, "the storefront response has to carry the %q field", name)
	}
	assert.NotContains(t, fields, "spending_remaining",
		"the remaining allowance is not computed in this round; the field must not be made up")
}
