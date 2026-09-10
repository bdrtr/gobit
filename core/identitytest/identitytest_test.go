package identitytest_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/identitytest"
)

// This file proves the suite by running it against implementations whose
// verdict is known in advance.
//
// A compliance suite has one failure mode that matters and it is silence: a
// green run over an implementation that is wrong. So each rule gets an
// implementation that breaks THAT rule and nothing else, and the suite has to
// name it. The reference at the bottom breaks none and has to pass, which is
// the other half — a suite that failed everything would also be useless.

// recorder collects what the suite reported.
type recorder struct{ failures []string }

// Helper satisfies the suite's T.
func (r *recorder) Helper() {}

// Errorf records a failure.
func (r *recorder) Errorf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

// run applies the suite and returns what it said.
func run(identity interface {
	CustomerID(*http.Request) (string, error)
}) []string {
	rec := &recorder{}
	identitytest.Contract(rec, identity)

	return rec.failures
}

// funcIdentity turns a function into an identity.
type funcIdentity func(*http.Request) (string, error)

// CustomerID answers with the function.
func (f funcIdentity) CustomerID(r *http.Request) (string, error) { return f(r) }

// TestTheNaiveImplementationFails is the one the whole package exists for.
//
// Reading a header and handing it back is what a first implementation does, it
// compiles, it satisfies the interface, and it lets every caller be every
// customer whose identifier they know — an identifier that travels in every
// order response.
func TestTheNaiveImplementationFails(t *testing.T) {
	t.Parallel()

	failures := run(funcIdentity(func(r *http.Request) (string, error) {
		return r.Header.Get("X-Customer-Id"), nil
	}))

	require.NotEmpty(t, failures, "the naive implementation must not pass")
	assert.True(t, contains(failures, "was believed"),
		"the failure has to say the claim was believed; got %v", failures)
}

// TestEveryPlantedSurfaceIsProbed keeps the probe from covering one place.
//
// A verifier reading the query string, a cookie or the body is the same defect
// wearing a different hat, and a suite that only probed headers would pass all
// three.
func TestEveryPlantedSurfaceIsProbed(t *testing.T) {
	t.Parallel()

	for name, read := range map[string]func(*http.Request) string{
		"a header": func(r *http.Request) string { return r.Header.Get("X-Customer-Id") },
		"another header": func(r *http.Request) string {
			return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		},
		"the query string": func(r *http.Request) string { return r.URL.Query().Get("customer_id") },
		"a cookie": func(r *http.Request) string {
			c, err := r.Cookie("session")
			if err != nil {
				return ""
			}

			return c.Value
		},
		"the body": func(r *http.Request) string {
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				return ""
			}
			var body struct {
				CustomerID string `json:"customer_id"`
			}
			_ = json.Unmarshal(raw, &body)

			return body.CustomerID
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			failures := run(funcIdentity(func(r *http.Request) (string, error) {
				return read(r), nil
			}))

			assert.True(t, contains(failures, "was believed"),
				"a verifier reading %s must be caught; got %v", name, failures)
		})
	}
}

// TestAnInventedIdentifierFails catches the shape the spoofing probe cannot:
// an identifier that came from nowhere.
func TestAnInventedIdentifierFails(t *testing.T) {
	t.Parallel()

	failures := run(funcIdentity(func(*http.Request) (string, error) {
		return "cust_ALWAYS_THIS_ONE", nil
	}))

	assert.True(t, contains(failures, "carrying no credentials"),
		"an identifier from nothing must be caught; got %v", failures)
}

// TestAnIdentifierBesideAnErrorFails holds the rule gobit's own caller does not
// need and the next caller might.
func TestAnIdentifierBesideAnErrorFails(t *testing.T) {
	t.Parallel()

	failures := run(funcIdentity(func(*http.Request) (string, error) {
		return "cust_1", errors.New("the session had expired")
	}))

	assert.True(t, contains(failures, "AND answered with"),
		"an identifier returned beside an error must be caught; got %v", failures)
}

// TestAConsumedBodyFails catches the defect whose symptom lands nowhere near
// its cause: every storefront POST failing to parse a body that was there.
func TestAConsumedBodyFails(t *testing.T) {
	t.Parallel()

	failures := run(funcIdentity(func(r *http.Request) (string, error) {
		_, _ = io.ReadAll(r.Body)

		return "", errors.New("no session")
	}))

	assert.True(t, contains(failures, "consumed the request body"),
		"a verifier that eats the body must be caught; got %v", failures)
}

// upstreamIdentity trusts a header a gateway is responsible for stripping, and
// SAYS so.
type upstreamIdentity struct{}

// CustomerID reads the declared header.
func (upstreamIdentity) CustomerID(r *http.Request) (string, error) {
	id := r.Header.Get("X-Authenticated-User")
	if id == "" {
		return "", errors.New("the gateway named nobody")
	}

	return id, nil
}

// TrustedUpstreamHeader declares the header the deployment strips.
func (upstreamIdentity) TrustedUpstreamHeader() string { return "X-Authenticated-User" }

// TestADeclaredUpstreamHeaderIsNotProbed lets the shape the interface's own
// contract names through.
func TestADeclaredUpstreamHeaderIsNotProbed(t *testing.T) {
	t.Parallel()

	assert.Empty(t, run(upstreamIdentity{}),
		"a verifier that declares its trusted header must pass; the contract names an "+
			"upstream proxy header as a source and this suite cannot see the gateway")
}

// silentUpstreamIdentity reads the same header and does NOT declare it.
type silentUpstreamIdentity struct{}

// CustomerID reads the header without saying it does.
func (silentUpstreamIdentity) CustomerID(r *http.Request) (string, error) {
	id := r.Header.Get("X-Authenticated-User")
	if id == "" {
		return "", errors.New("the gateway named nobody")
	}

	return id, nil
}

// TestAnUNDECLAREDUpstreamHeaderIsProbed is what makes the declaration worth
// writing.
//
// The two implementations above are the same code. The only difference is that
// one has looked at the question, and the failure below is what an author who
// has not gets — naming the interface to implement if the answer is really yes.
func TestAnUNDECLAREDUpstreamHeaderIsProbed(t *testing.T) {
	t.Parallel()

	failures := run(silentUpstreamIdentity{})

	require.NotEmpty(t, failures, "an undeclared header must be probed like any other")
	assert.True(t, contains(failures, "identitytest.UpstreamTrust"),
		"the failure has to name the way out; got %v", failures)
}

// signedCookieIdentity is a minimal verifier that really proves something.
//
// It is here as the suite's positive fixture and not as a recommendation: the
// key handling, the expiry and the rotation an installation needs are all
// missing. What it has is the property the suite is about — the identifier it
// returns is one this code MINTED, and a caller cannot produce the cookie
// without the key.
type signedCookieIdentity struct{ key []byte }

// CustomerID reads the signed cookie and refuses one it did not sign.
func (s signedCookieIdentity) CustomerID(r *http.Request) (string, error) {
	cookie, err := r.Cookie("gobit_session")
	if err != nil {
		return "", errors.New("no session cookie")
	}

	id, signature, found := strings.Cut(cookie.Value, ".")
	if !found {
		return "", errors.New("the session cookie is not signed")
	}
	raw, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return "", errors.New("the signature is not readable")
	}
	if !hmac.Equal(raw, s.sign(id)) {
		return "", errors.New("the signature does not match")
	}

	return id, nil
}

// sign is the MAC over the identifier.
func (s signedCookieIdentity) sign(id string) []byte {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(id))

	return mac.Sum(nil)
}

// TestAVerifierThatReallyProvesPasses is the other half of the proof.
//
// A suite everything fails is as useless as one everything passes, and this is
// the fixture that says which one this is.
func TestAVerifierThatReallyProvesPasses(t *testing.T) {
	t.Parallel()

	assert.Empty(t, run(signedCookieIdentity{key: []byte("a key nobody outside this test has")}),
		"a verifier whose identifier it minted itself must pass every rule")
}

// contains reports whether any failure carries the fragment.
func contains(failures []string, fragment string) bool {
	for _, failure := range failures {
		if strings.Contains(failure, fragment) {
			return true
		}
	}

	return false
}
