//go:build integration

package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gatePanelOrigin is the panel's site the gate's installation lists in
// ADMIN_CORS_ALLOWED_ORIGINS.
const gatePanelOrigin = "https://panel.gate.example"

// corsSafelistedHeaders are the request headers a browser sends without
// asking a preflight (the Fetch standard's CORS-safelisted request headers),
// so the admin surface has no need to allow them.
var corsSafelistedHeaders = map[string]bool{
	"accept": true, "accept-language": true, "content-language": true, "range": true,
}

// TestEveryHeaderAnAdminOperationDescribesPassesItsPreflight holds the admin
// surface's CORS list to the document (ADR 0461). ADR 0451 wrote the list by
// hand and left out If-Match, which every product write reads (ADR 0222), so
// a panel on its own host could not send one with its precondition: the
// browser refused the write after its preflight. The population is the
// headers the installation's own document describes on /admin/v1, each asked
// for by a preflight through the real router.
func TestEveryHeaderAnAdminOperationDescribesPassesItsPreflight(t *testing.T) {
	setUpGateInstallation(t)
	t.Setenv("PLUGINS", "")
	t.Setenv("ADMIN_CORS_ALLOWED_ORIGINS", gatePanelOrigin)

	installation, closeAll, err := Open(t.Context(), Options{Version: "gate"})
	require.NoError(t, err)
	t.Cleanup(closeAll)
	built, err := installation.Describe().Build(installation.Router)
	require.NoError(t, err)
	encoded, err := json.Marshal(built)
	require.NoError(t, err)
	var document struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				In   string `json:"in"`
				Name string `json:"name"`
			} `json:"parameters"`
		} `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(encoded, &document))

	asked := 0
	for path, operations := range document.Paths {
		if !strings.HasPrefix(path, "/admin/v1/") {
			continue
		}
		for method, operation := range operations {
			for _, parameter := range operation.Parameters {
				if parameter.In != "header" || corsSafelistedHeaders[strings.ToLower(parameter.Name)] {
					continue
				}
				asked++
				allowed := preflightAllows(t, installation.Router, path, method, parameter.Name)
				assert.True(t, allowed, "%s %s describes the header %s and its preflight does not allow it",
					strings.ToUpper(method), path, parameter.Name)
			}
		}
	}
	require.NotZero(t, asked, "no admin operation describes a header; the gate read nothing")
}

// preflightAllows sends a panel's preflight for one header and reports whether
// the answer allows it.
func preflightAllows(t *testing.T, router http.Handler, path, method, header string) bool {
	t.Helper()

	r := httptest.NewRequest(http.MethodOptions, path, http.NoBody)
	r.Header.Set("Origin", gatePanelOrigin)
	r.Header.Set("Access-Control-Request-Method", strings.ToUpper(method))
	r.Header.Set("Access-Control-Request-Headers", "authorization, "+strings.ToLower(header))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	allowed := strings.Split(rec.Header().Get("Access-Control-Allow-Headers"), ",")

	return rec.Header().Get("Access-Control-Allow-Origin") == gatePanelOrigin &&
		slices.ContainsFunc(allowed, func(name string) bool { return strings.EqualFold(strings.TrimSpace(name), header) })
}
