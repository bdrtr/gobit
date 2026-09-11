package identitypasskey_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	identitypasskey "github.com/bdrtr/gobit/contrib/identity-passkey"
	"github.com/bdrtr/gobit/core/openapi"
)

// This file closes the drift the description loop CANNOT see.
//
// TestEveryRouteIsDescribedAndEveryDescriptionMatchesARoute asks whether a route
// has a description and whether a description has a route. It never reads what
// the description SAYS, so a documented status the endpoint cannot return is
// invisible to it — and two of them shipped: register/finish and sign-in/finish
// were documented as answering 400 for a missing ceremony, and the kind is
// Invalid, which core/http maps to 422 (gap D66).
//
// A published description is a promise (ADR 0026), and gap D62 is the same class
// one surface over. What closes it is driving the path and comparing BOTH ends:
// the status the router really answers, and the status whose description NAMES
// that code.
//
// # Why the comparison is by CODE and not "is the status listed"
//
// Measured while writing this: core/openapi adds default responses to every
// operation — 422, 429 and 500 are in the document whether a module wrote them
// or not. So "the status is documented" is true for every route by construction,
// and an assertion on it would have passed over the very drift that shipped. The
// question that has an answer is which status the document says carries this
// code, because only a module writes that sentence.

// errorPath is one refusal this module can produce on demand.
type errorPath struct {
	name   string
	method string
	path   string
	body   string
	// signedIn says whether the request carries a session cookie.
	signedIn bool
	status   int
	code     string
	// keys and other build a DEDICATED harness when a refusal needs state.
	//
	// The last-way-in refusal cannot be produced on a shared empty store: it needs
	// a person who has exactly one key and no other way into the account, which is
	// the whole shape of the rule.
	keys  []string
	other identitypasskey.OtherSignIn
	// documentedPath is the path AS THE DOCUMENT SPELLS IT, when that differs from
	// the one a request uses.
	//
	// A path parameter is `{credential_id}` in the document and a real value in a
	// request, and nothing else in this package has needed the distinction before.
	documentedPath string
}

// TestEveryDocumentedRefusalIsTheOneTheRouteAnswers drives each refusal and
// compares it with the document.
//
// The table is the refusals a test can produce without an authenticator. What it
// does NOT cover is named rather than implied: a ceremony the library refuses
// (401 identity_passkey_refused) needs a real assertion and is exercised in
// ceremony_test.go, and 500 identity_passkey_unavailable needs a broken store.
func TestEveryDocumentedRefusalIsTheOneTheRouteAnswers(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	signedIn := h.signedInAs(t, testCustomer)

	doc := openapi.New("identity-passkey", "v1")
	h.module.Describe(doc)
	built, err := doc.Build(h.router)
	require.NoError(t, err)

	for _, tc := range []errorPath{
		{
			name:   "registering without an account",
			method: http.MethodPost, path: "/store/v1/auth/passkey/register/begin",
			status: http.StatusUnauthorized, code: identitypasskey.CodeNotSignedIn,
		},
		{
			name:   "finishing a registration with no ceremony",
			method: http.MethodPost, path: "/store/v1/auth/passkey/register/finish",
			body: `{}`, signedIn: true,
			status: http.StatusUnprocessableEntity, code: identitypasskey.CodeCeremonyMissing,
		},
		{
			name:   "finishing a sign-in with no ceremony",
			method: http.MethodPost, path: "/store/v1/auth/passkey/sign-in/finish",
			body:   `{}`,
			status: http.StatusUnprocessableEntity, code: identitypasskey.CodeCeremonyMissing,
		},
		{
			name:   "listing without an account",
			method: http.MethodGet, path: "/store/v1/auth/passkey/keys",
			status: http.StatusUnauthorized, code: identitypasskey.CodeNotSignedIn,
		},
		{
			name:   "removing without an account",
			method: http.MethodDelete, path: "/store/v1/auth/passkey/keys/anything",
			documentedPath: "/store/v1/auth/passkey/keys/{credential_id}",
			status:         http.StatusUnauthorized, code: identitypasskey.CodeNotSignedIn,
		},
		{
			name:   "removing an id that is not one of yours",
			method: http.MethodDelete, path: "/store/v1/auth/passkey/keys/bm90LW1pbmU",
			documentedPath: "/store/v1/auth/passkey/keys/{credential_id}",
			signedIn:       true,
			status:         http.StatusNotFound, code: identitypasskey.CodeNoSuchKey,
		},
		{
			name:   "removing the only way into the account",
			method: http.MethodDelete, path: "/store/v1/auth/passkey/keys/b25seQ",
			documentedPath: "/store/v1/auth/passkey/keys/{credential_id}",
			signedIn:       true,
			keys:           []string{"only"}, other: identitypasskey.NoOtherSignIn(),
			status: http.StatusConflict, code: identitypasskey.CodeLastWayIn,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router, cookie := h, signedIn
			if len(tc.keys) > 0 || tc.other != nil {
				router = withKeys(t, tc.other, tc.keys...)
				cookie = router.signedInAs(t, testCustomer)
			}

			var cookies []*http.Cookie
			if tc.signedIn {
				cookies = append(cookies, cookie)
			}

			rec := router.do(t, tc.method, tc.path, tc.body, cookies...)

			require.Equal(t, tc.status, rec.Code,
				"%s %s answered %d; body: %s", tc.method, tc.path, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), tc.code,
				"the code is what a client branches on")

			documented := tc.path
			if tc.documentedPath != "" {
				documented = tc.documentedPath
			}

			assert.Equal(t, strconv.Itoa(tc.status),
				documentedStatusForCode(t, built, documented, tc.method, tc.code),
				"%s %s answers %d with code %q and the published document puts that code "+
					"under a DIFFERENT status.\n"+
					"A description is a promise (ADR 0026): an integrator reading it codes "+
					"against a status the endpoint never sends, and the loop test next door "+
					"cannot see the difference — it checks that a description EXISTS.",
				tc.method, tc.path, tc.status, tc.code)
		})
	}
}

// documentedStatusForCode answers which documented status says it carries a code.
//
// Exactly one must: two statuses naming one code is a document that cannot be
// coded against, and none is a code an integrator has never been told about.
func documentedStatusForCode(
	t *testing.T, built map[string]any, path, method, code string,
) string {
	t.Helper()

	var found []string
	for status, description := range documentedResponses(t, built, path, method) {
		if strings.Contains(description, code) {
			found = append(found, status)
		}
	}

	require.Len(t, found, 1,
		"%s %s: %d documented statuses name the code %q and exactly one must — "+
			"none means an integrator was never told the code exists, two means the "+
			"document cannot be coded against. Found: %v",
		method, path, len(found), code, found)

	return found[0]
}

// documentedResponses reads the statuses and their descriptions for one
// operation.
//
// It goes through JSON rather than type-asserting the built map, because what an
// operation is in Go is the openapi package's business and what an integrator
// reads is the JSON. Asserting on the Go types would make this test depend on a
// shape that is free to change under a document that did not.
func documentedResponses(t *testing.T, built map[string]any, path, method string) map[string]string {
	t.Helper()

	raw, err := json.Marshal(built)
	require.NoError(t, err, "the document could not be encoded")

	var document struct {
		Paths map[string]map[string]struct {
			Responses map[string]struct {
				Description string `json:"description"`
			} `json:"responses"`
		} `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(raw, &document), "the document could not be read back")

	operations, ok := document.Paths[path]
	require.True(t, ok, "the document describes no %s", path)

	operation, ok := operations[lowerMethod(method)]
	require.True(t, ok, "the document describes no %s on %s", method, path)
	require.NotEmpty(t, operation.Responses, "%s %s carries no responses", method, path)

	out := make(map[string]string, len(operation.Responses))
	for status, response := range operation.Responses {
		out[status] = response.Description
	}

	return out
}

// lowerMethod spells a method the way the document keys it.
//
// This used to special-case POST and return everything else unchanged, which was
// correct for exactly as long as the table held only POSTs: an OpenAPI document
// keys operations in lower case, so the first GET would have failed looking for
// an operation named "GET". It was found by adding one.
func lowerMethod(method string) string {
	return strings.ToLower(method)
}
