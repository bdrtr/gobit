package identitysession_test

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/openapi"
)

// TestEveryBodyARouteDecodesIsDescribed is D216: each route that decodes a
// JSON body describes it, field for field as its decoder reads it — the
// decoder refuses a field it does not know, so a client left to guess a name
// from the prose is refused — and the two routes that take none describe none.
func TestEveryBodyARouteDecodesIsDescribed(t *testing.T) {
	t.Parallel()

	h := newRegHarness(t)
	doc := openapi.New("identity-session", "v1")
	h.module.Describe(doc)
	built, err := doc.Build(h.router)
	require.NoError(t, err)
	encoded, err := json.Marshal(built)
	require.NoError(t, err)
	var document struct {
		Paths map[string]map[string]*struct {
			RequestBody *struct {
				Content map[string]struct {
					Schema struct {
						Ref string `json:"$ref"`
					} `json:"schema"`
				} `json:"content"`
			} `json:"requestBody"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]struct {
				Properties map[string]any `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	require.NoError(t, json.Unmarshal(encoded, &document))

	for _, c := range []struct {
		method, path string
		fields       []string
	}{
		{"post", "/store/v1/auth/sign-in", []string{"email", "password"}},
		{"put", "/admin/v1/customer-credentials", []string{"customer_id", "email", "password"}},
		{"post", "/store/v1/auth/register", []string{"email", "password"}},
		{"post", "/store/v1/auth/register/verify", []string{"token"}},
	} {
		operation := document.Paths[c.path][c.method]
		require.NotNil(t, operation, "%s %s is not in the document", c.method, c.path)
		require.NotNil(t, operation.RequestBody, "%s %s describes no request body", c.method, c.path)
		ref := operation.RequestBody.Content["application/json"].Schema.Ref
		component, found := document.Components.Schemas[strings.TrimPrefix(ref, "#/components/schemas/")]
		require.True(t, found, "%s %s: the body's schema %q is not a component", c.method, c.path, ref)
		assert.Equal(t, c.fields, slices.Sorted(maps.Keys(component.Properties)), "%s %s", c.method, c.path)
	}

	for _, c := range []struct{ method, path string }{
		{"post", "/store/v1/auth/sign-out"},
		{"get", "/store/v1/auth/session"},
	} {
		operation := document.Paths[c.path][c.method]
		require.NotNil(t, operation, "%s %s is not in the document", c.method, c.path)
		assert.Nil(t, operation.RequestBody, "%s %s takes no body", c.method, c.path)
	}
}
