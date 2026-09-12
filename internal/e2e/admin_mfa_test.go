//go:build integration

package e2e

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authapi "github.com/bdrtr/gobit/internal/modules/auth/api"
	authsvc "github.com/bdrtr/gobit/internal/modules/auth/service"
)

// This file proves, through the PRODUCTION stack, that an administrator who
// enrolled an authenticator cannot sign in with a password alone (ADR 0147).
//
// # Why it needs the whole stack
//
// Four things have to line up and only one of them is the service: the login body
// has to carry the code, the handler has to pass it on, the stored secret has to
// come back out of the database and open with the installation's key, and the
// refusal has to arrive as a code a client can branch on. A service test proves
// the middle one; a route that dropped the field would leave it green.
//
// # Why the code is computed here rather than asked for
//
// The module's own helper is a test export and this package cannot see it. What
// is written below is therefore a SECOND implementation of RFC 6238 — deliberately,
// because a copy of the module's arithmetic would compare it with itself. The
// module's version is checked against the RFC's own Appendix B vectors in its
// internal test; if these two disagree about a code, one of them is wrong and
// this test says so.
//
// # Why it enrolls its own administrator
//
// Every other identity test signs in as the shared admin user with a password.
// Enrolling a factor on that account would demand a code from all of them, so
// this file creates an account of its own and leaves the shared one alone.

// mfaTestPassword is the password of the administrator this file enrolls.
const mfaTestPassword = "second-factor-password-42"

// totpCodeAt is the code an authenticator holding the secret shows at `at`.
//
// RFC 6238 with the module's parameters: HMAC-SHA1 over the thirty-second step
// counter, dynamic truncation, six digits. It is short enough to read and it is
// the whole of what an authenticator app does.
func totpCodeAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()

	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).
		DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	require.NoError(t, err, "the enrollment secret has to be base32")

	counter := make([]byte, 8)
	binary.BigEndian.PutUint64(counter, uint64(at.UTC().Unix()/30))

	mac := hmac.New(sha1.New, key)
	_, err = mac.Write(counter)
	require.NoError(t, err)
	sum := mac.Sum(nil)

	offset := sum[len(sum)-1] & 0x0f
	truncated := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff

	return fmt.Sprintf("%06d", truncated%1_000_000)
}

// newMFAAdministrator creates an administrator with a password and returns their
// email.
func newMFAAdministrator(ctx context.Context, t *testing.T) string {
	t.Helper()

	email := fmt.Sprintf("mfa-%d@gobit.test", fixtureCounter.Add(1))
	_, err := authSvc.CreateUser(ctx, authsvc.CreateUserInput{
		Email:     email,
		FirstName: "Second",
		LastName:  "Factor",
	}, mfaTestPassword)
	require.NoError(t, err, "the administrator could not be created")

	return email
}

// tokenRequest sends a login body with the given fields.
func tokenRequest(t *testing.T, email, password, code string) *httptest.ResponseRecorder {
	t.Helper()

	body, err := json.Marshal(map[string]string{
		"email": email, "password": password, "code": code,
	})
	require.NoError(t, err, "the login body could not be encoded")

	req := httptest.NewRequest(http.MethodPost, authapi.LoginPath, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	testRouter.ServeHTTP(rec, req)

	return rec
}

// enrolSecondFactor draws a factor for the signed-in administrator, confirms it
// and returns the secret their authenticator now holds.
func enrolSecondFactor(t *testing.T, token string) string {
	t.Helper()

	enroll := tokenedRequest(t, http.MethodPost, authapi.MFAEnrolPath, token, "")
	require.Equal(t, http.StatusOK, enroll.Code,
		"the enrollment must succeed; body: %s", enroll.Body.String())

	var envelope struct {
		Data struct {
			Secret string `json:"secret"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(enroll.Body.Bytes(), &envelope),
		"the enrollment response could not be decoded; body: %s", enroll.Body.String())
	require.NotEmpty(t, envelope.Data.Secret)

	confirm := tokenedRequest(t, http.MethodPost, authapi.MFAConfirmPath, token,
		fmt.Sprintf(`{"code":%q}`, totpCodeAt(t, envelope.Data.Secret, time.Now())))
	require.Equal(t, http.StatusNoContent, confirm.Code,
		"the confirmation must succeed; body: %s", confirm.Body.String())

	return envelope.Data.Secret
}

// tokenedRequest sends an admin request with a SESSION token.
func tokenedRequest(t *testing.T, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	testRouter.ServeHTTP(rec, req)

	return rec
}

// TestAnEnrolledAdministratorCannotSignInWithAPasswordAlone is the decision, end
// to end.
func TestAnEnrolledAdministratorCannotSignInWithAPasswordAlone(t *testing.T) {
	ctx := t.Context()
	email := newMFAAdministrator(ctx, t)

	// Before enrolling, the password is enough — and that is the state every
	// installation is in until somebody enrolls.
	before := tokenRequest(t, email, mfaTestPassword, "")
	require.Equal(t, http.StatusOK, before.Code,
		"an account with no factor signs in with its password; body: %s", before.Body.String())

	secret := enrolSecondFactor(t, jetonAl(t, email, mfaTestPassword))

	refused := tokenRequest(t, email, mfaTestPassword, "")
	require.Equal(t, http.StatusUnauthorized, refused.Code,
		"the password alone must not open an enrolled account; body: %s", refused.Body.String())
	assert.Equal(t, authsvc.CodeMFARequired, errorSummary(t, refused)[0],
		"the refusal has to name the factor, or a client has no way to ask for the digits")

	wrong := tokenRequest(t, email, mfaTestPassword, "000000")
	require.Equal(t, http.StatusUnauthorized, wrong.Code, "body: %s", wrong.Body.String())
	assert.Equal(t, authsvc.CodeMFACodeWrong, errorSummary(t, wrong)[0])

	// And the code the authenticator shows does open it. Without this the test
	// above would pass on a build that refused every login.
	accepted := tokenRequest(t, email, mfaTestPassword, totpCodeAt(t, secret, time.Now()))
	require.Equal(t, http.StatusOK, accepted.Code,
		"the code the app shows has to sign them in; body: %s", accepted.Body.String())
}

// TestAMachineIsUnaffectedByTheSecondFactor verifies that the demand reaches
// people and not keys.
//
// A secret API key holds no authenticator and nobody could scan a code for one,
// so a demand that reached keys would take every integration down at once — and
// that is not theoretical here: this suite's own admin requests are made with a
// key.
func TestAMachineIsUnaffectedByTheSecondFactor(t *testing.T) {
	ctx := t.Context()
	email := newMFAAdministrator(ctx, t)
	enrolSecondFactor(t, jetonAl(t, email, mfaTestPassword))

	recorder, err := adminRequestWithBody(http.MethodGet, "/admin/v1/users", nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, recorder.Code,
		"a secret key must keep working while a person holds a factor; body: %s",
		recorder.Body.String())
}

// TestRemovingTheFactorGivesTheAccountBack is the way out, over HTTP.
//
// It is the OWNER's act: they are signed in, so they still hold the phone. The
// person who does NOT hold it cannot come this way at all, which is why
// `gobit mfa-reset` exists.
func TestRemovingTheFactorGivesTheAccountBack(t *testing.T) {
	ctx := t.Context()
	email := newMFAAdministrator(ctx, t)

	secret := enrolSecondFactor(t, jetonAl(t, email, mfaTestPassword))
	token := tokenFrom(t, tokenRequest(t, email, mfaTestPassword, totpCodeAt(t, secret, time.Now())))

	removed := tokenedRequest(t, http.MethodDelete, authapi.MFAEnrolPath, token, "")
	require.Equal(t, http.StatusNoContent, removed.Code,
		"the owner may switch their own factor off; body: %s", removed.Body.String())

	again := tokenRequest(t, email, mfaTestPassword, "")
	assert.Equal(t, http.StatusOK, again.Code,
		"after the removal the password alone signs in again; body: %s", again.Body.String())
}

// TestASecondEnrolmentLeavesTheProvenPhoneWorking is the rule that keeps an
// abandoned scan from switching the factor off.
func TestASecondEnrolmentLeavesTheProvenPhoneWorking(t *testing.T) {
	ctx := t.Context()
	email := newMFAAdministrator(ctx, t)

	first := enrolSecondFactor(t, jetonAl(t, email, mfaTestPassword))
	token := tokenFrom(t, tokenRequest(t, email, mfaTestPassword, totpCodeAt(t, first, time.Now())))

	// A new phone is scanned and the person is interrupted before proving it.
	started := tokenedRequest(t, http.MethodPost, authapi.MFAEnrolPath, token, "")
	require.Equal(t, http.StatusOK, started.Code, "body: %s", started.Body.String())

	stillDemanded := tokenRequest(t, email, mfaTestPassword, "")
	assert.Equal(t, http.StatusUnauthorized, stillDemanded.Code,
		"an abandoned enrollment must not turn the demand off; body: %s",
		stillDemanded.Body.String())

	working := tokenRequest(t, email, mfaTestPassword, totpCodeAt(t, first, time.Now()))
	assert.Equal(t, http.StatusOK, working.Code,
		"the proven phone keeps signing them in; body: %s", working.Body.String())
}

// tokenFrom reads the session token out of a successful login response.
func tokenFrom(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	require.Equal(t, http.StatusOK, rec.Code, "the login must succeed; body: %s", rec.Body.String())

	var envelope struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope),
		"the login response could not be decoded; body: %s", rec.Body.String())
	require.NotEmpty(t, envelope.Data.Token)

	return envelope.Data.Token
}
