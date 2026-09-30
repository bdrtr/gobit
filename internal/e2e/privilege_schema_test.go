//go:build integration

package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/auth/models"
	authsvc "github.com/bdrtr/gobit/internal/modules/auth/service"
)

// identityOnlyOperations are the admin operations the served document names
// no privilege for, each with why: they establish an identity or act on the
// caller's own, and a privilege there would lock an operator out of their own
// session.
var identityOnlyOperations = map[string]string{
	"get /admin/v1/auth/me":                 "reads back the caller's own identity",
	"post /admin/v1/auth/login":             "establishes an identity; nobody holds a privilege yet",
	"post /admin/v1/auth/accept-invitation": "establishes an identity from an invitation",
	"post /admin/v1/auth/logout":            "closes the caller's own sessions",
	"post /admin/v1/auth/mfa":               "enrolls the caller's own second factor (ADR 0264)",
	"post /admin/v1/auth/mfa/confirm":       "proves the caller's own second factor (ADR 0264)",
	"post /admin/v1/auth/mfa/remove":        "removes the caller's own second factor, given its code (ADR 0264)",
}

// refusedPrivilege reads the privilege a 403 names.
var refusedPrivilege = regexp.MustCompile(`requires the "([^"]+)" privilege`)

// TestEveryOperationNamesThePrivilegeItsRouteRefuses is ADR 0263 on the
// production wiring: for every admin operation in the served document, an
// operator holding no privilege is refused with 403 naming the document's
// first privilege, and one holding that privilege alone is refused naming the
// second where there is one. Every request is refused before its handler runs,
// so the walk writes nothing.
//
// The document derives the privilege from the route's guards and the refusal
// comes from the guard running, so the two are held to each other rather than
// to a list written here.
func TestEveryOperationNamesThePrivilegeItsRouteRefuses(t *testing.T) {
	_, doc := schemaDocument(t)
	paths, ok := doc["paths"].(map[string]any)
	require.True(t, ok)
	scopeless := "Bearer " + yetkisizYoneticiJetonu(t)

	refused := func(method, path, credential string) (int, string) {
		req := httptest.NewRequest(strings.ToUpper(method), pathParamRe.ReplaceAllString(path, "privilege_probe_id"),
			strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", credential)
		rec := httptest.NewRecorder()
		testRouter.ServeHTTP(rec, req)
		var body struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		match := refusedPrivilege.FindStringSubmatch(body.Error.Message)
		if match == nil {
			return rec.Code, ""
		}
		return rec.Code, match[1]
	}

	keys := map[string]string{}
	keyHolding := func(scopes []string) string {
		set := strings.Join(scopes, " ")
		if keys[set] == "" {
			keys[set] = apiKeyHolding(t, scopes...)
		}
		return keys[set]
	}

	probed, identityOnly := 0, []string{}
	for path, raw := range paths {
		if !strings.HasPrefix(path, "/admin/v1") {
			continue
		}
		methods, ok := raw.(map[string]any)
		require.True(t, ok)
		for method, rawOperation := range methods {
			operation, ok := rawOperation.(map[string]any)
			require.True(t, ok)
			scopes := documentedScopes(t, operation)
			name := method + " " + path
			if len(scopes) == 0 {
				identityOnly = append(identityOnly, name)
				continue
			}
			probed++

			code, named := refused(method, path, scopeless)
			assert.Equal(t, http.StatusForbidden, code, "%s: an operator holding no privilege was not refused", name)
			assert.Equal(t, scopes[0], named,
				"%s: the document names %v and the route refused naming %q", name, scopes, named)

			if len(scopes) > 1 {
				code, named = refused(method, path, "Bearer "+keyHolding(scopes[:1]))
				assert.Equal(t, http.StatusForbidden, code, "%s: %s alone was not refused", name, scopes[0])
				assert.Equal(t, scopes[1], named,
					"%s: holding %s, the route refused naming %q", name, scopes[0], named)
			}
			// A read runs its handler, which writes nothing, so it can also show
			// that the privileges the document names are ENOUGH: a document that
			// left one out would send an operator holding what it names into a
			// refusal.
			if method == "get" {
				code, named = refused(method, path, "Bearer "+keyHolding(scopes))
				assert.NotEqual(t, http.StatusForbidden, code,
					"%s: an operator holding %v, all the document names, was refused naming %q", name, scopes, named)
			}
		}
	}

	require.Greater(t, probed, 300, "only %d admin operations name a privilege; the walk is broken", probed)
	sort.Strings(identityOnly)
	want := make([]string, 0, len(identityOnlyOperations))
	for name := range identityOnlyOperations {
		want = append(want, name)
	}
	sort.Strings(want)
	assert.Equal(t, want, identityOnly,
		"the admin operations that name no privilege changed. Each one is reachable by any "+
			"signed-in operator; a new one is a decision to write into identityOnlyOperations "+
			"with its reason, and a missing one means a privilege was put on an identity route")

	code, _ := refused(http.MethodGet, "/admin/v1/auth/me", scopeless)
	assert.Equal(t, http.StatusOK, code, "an identity-only route refused an operator holding no privilege")
}

// documentedScopes reads the privileges an operation's security requirement names.
func documentedScopes(t *testing.T, operation map[string]any) []string {
	t.Helper()

	raw, err := json.Marshal(operation["security"])
	require.NoError(t, err)
	var requirements []map[string][]string
	require.NoError(t, json.Unmarshal(raw, &requirements))

	var out []string
	for _, requirement := range requirements {
		for _, scopes := range requirement {
			out = append(out, scopes...)
		}
	}
	return out
}

// apiKeyHolding mints a secret key holding exactly these privileges.
func apiKeyHolding(t *testing.T, scopes ...string) string {
	t.Helper()

	_, key, err := authSvc.CreateAPIKey(t.Context(), authsvc.CreateAPIKeyInput{
		Type: models.APIKeySecret, Title: "privilege probe " + strings.Join(scopes, " "), CreatedBy: adminID,
		Scopes: scopes,
	})
	require.NoError(t, err)
	return key
}
