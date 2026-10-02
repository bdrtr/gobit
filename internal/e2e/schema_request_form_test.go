//go:build integration

package e2e

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNoRequestBodyRequiresAField is ADR 0363 over the installation: no
// component a request body reaches lists a required field, and no component
// is reached both from a request body and from a response, so a client
// generated from the served document is asked to send only what it chooses
// and reads every response field the server always writes.
func TestNoRequestBodyRequiresAField(t *testing.T) {
	_, doc := schemaDocument(t)
	schemas := schemaComponents(t, doc)

	var reach func(node any, into map[string]bool)
	reach = func(node any, into map[string]bool) {
		switch typed := node.(type) {
		case map[string]any:
			for key, value := range typed {
				if ref, isRef := value.(string); isRef && key == "$ref" {
					name := strings.TrimPrefix(ref, refPrefix)
					if !into[name] {
						into[name] = true
						reach(schemas[name], into)
					}

					continue
				}

				reach(value, into)
			}
		case []any:
			for _, value := range typed {
				reach(value, into)
			}
		}
	}

	read, written := map[string]bool{}, map[string]bool{}
	bodies := 0

	for path, item := range objectField(t, doc, "paths", "the document") {
		operations, ok := item.(map[string]any)
		require.True(t, ok, path)

		for method, raw := range operations {
			operation, ok := raw.(map[string]any)
			require.True(t, ok, "%s %s", method, path)

			if body, hasBody := operation["requestBody"]; hasBody {
				bodies++
				reach(body, read)
			}

			reach(operation["responses"], written)
		}
	}

	require.NotEmpty(t, read, "precondition: a request body reaches a component; with none, this audits nothing")

	for name := range read {
		component, ok := schemas[name].(map[string]any)
		require.True(t, ok, "%s is a component", name)
		assert.NotContains(t, component, "required", "%s is read from a request body and requires a field", name)
		assert.False(t, written[name], "%s is reached both from a request body and from a response", name)
	}

	t.Logf("%d request bodies read %d components; responses write %d", bodies, len(read), len(written))
}
