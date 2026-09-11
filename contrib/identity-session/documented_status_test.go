package identitysession_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	identitysession "github.com/bdrtr/gobit/contrib/identity-session"
	"github.com/bdrtr/gobit/core/openapi"
)

// This file closes the drift the description loop cannot see, in this module.
//
// Its sibling one directory over carries the same test and the same reasoning;
// both were written after two documented statuses in each module were found
// wrong (gap D66). The loop test asks whether a route has a description and
// whether a description has a route, never what the description SAYS.
//
// The comparison is by CODE rather than by "is the status listed", and that is
// measured rather than chosen: core/openapi adds default responses to every
// operation — 422, 429 and 500 are in the document whether a module wrote them or
// not — so "the status is documented" is true for every route by construction and
// would have passed over the drift that shipped. Which status the document says
// carries a given code is a sentence only a module writes.

// TestEveryDocumentedRefusalIsTheOneTheRouteAnswers drives each refusal a test
// can produce and compares it with the document.
//
// What it does not cover is named rather than implied: 409 needs a store that
// refuses a write and 500 a store that breaks, and both are exercised in the
// module's own tests without reading the document.
func TestEveryDocumentedRefusalIsTheOneTheRouteAnswers(t *testing.T) {
	t.Parallel()

	m := identitysession.New(identitysession.Options{
		Secret:      []byte("a documented-status secret of thirty-two!"),
		Credentials: refusingCredentials{},
	})
	require.NoError(t, m.Register(t.Context(), emptyContainer(t)))

	r := chi.NewRouter()
	m.Routes(r)

	doc := openapi.New("identity-session", "v1")
	m.Describe(doc)
	built, err := doc.Build(r)
	require.NoError(t, err)

	for _, tc := range []struct {
		name   string
		path   string
		body   string
		status int
		code   string
	}{
		{
			name: "a body this endpoint cannot read",
			path: "/store/v1/auth/sign-in", body: `{"nope":1}`,
			status: http.StatusUnprocessableEntity, code: identitysession.CodeInvalid,
		},
		{
			name: "an address and a password that do not match",
			path: "/store/v1/auth/sign-in", body: `{"email":"a@b.test","password":"x"}`,
			status: http.StatusUnauthorized, code: identitysession.CodeRejected,
		},
		{
			name: "a credential with no customer",
			path: "/admin/v1/customer-credentials", body: `{"email":"a@b.test","password":"x"}`,
			status: http.StatusUnprocessableEntity, code: identitysession.CodeInvalid,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := sendJSON(t, r, tc.path, tc.body)

			require.Equal(t, tc.status, rec.Code,
				"%s answered %d; body: %s", tc.path, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), tc.code)

			assert.Equal(t, strconv.Itoa(tc.status),
				documentedStatusForCode(t, built, tc.path, tc.code),
				"%s answers %d with code %q and the published document puts that code "+
					"under a DIFFERENT status.\n"+
					"A description is a promise (ADR 0026): an integrator reading it codes "+
					"against a status the endpoint never sends.", tc.path, tc.status, tc.code)
		})
	}
}

// sendJSON posts a body to a route.
func sendJSON(t *testing.T, r chi.Router, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	method := http.MethodPost
	if strings.HasPrefix(path, "/admin/") {
		method = http.MethodPut
	}
	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// documentedStatusForCode answers which documented status says it carries a code.
//
// Exactly one must: two statuses naming one code is a document that cannot be
// coded against, and none is a code an integrator has never been told about.
func documentedStatusForCode(
	t *testing.T, built map[string]any, path, code string,
) string {
	t.Helper()

	raw, err := json.Marshal(built)
	require.NoError(t, err)

	var document struct {
		Paths map[string]map[string]struct {
			Responses map[string]struct {
				Description string `json:"description"`
			} `json:"responses"`
		} `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(raw, &document))

	operations, ok := document.Paths[path]
	require.True(t, ok, "the document describes no %s", path)

	var found []string
	for _, operation := range operations {
		for status, response := range operation.Responses {
			if strings.Contains(response.Description, code) {
				found = append(found, status)
			}
		}
	}

	require.Len(t, found, 1,
		"%s: %d documented statuses name the code %q and exactly one must. Found: %v",
		path, len(found), code, found)

	return found[0]
}

// refusingCredentials answers every address the way a wrong password is answered.
type refusingCredentials struct{}

// Credential refuses everything.
func (refusingCredentials) Credential(
	context.Context, string,
) (customerID, passwordHash string, err error) {
	return "", "", identitysession.ErrPasswordMismatch
}

// Put writes nothing and reports success, so the admin path's own refusals are
// the ones under test.
func (refusingCredentials) Put(context.Context, string, string, string) error { return nil }
