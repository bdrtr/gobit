package adminui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// fakeSecondFactor stands in for the auth module's surface and records what
// reached it.
type fakeSecondFactor struct {
	proven, waiting, owed bool
	secret, uri           string
	err                   error

	userID, code string
	acted        string
}

func (f *fakeSecondFactor) SecondFactorStatus(_ context.Context, userID string) (proven, waiting, owed bool, err error) {
	f.userID = userID
	return f.proven, f.waiting, f.owed, nil
}

func (f *fakeSecondFactor) EnrollSecondFactor(_ context.Context, userID, code string) (secret, uri string, err error) {
	f.userID, f.code, f.acted = userID, code, "enroll"
	return f.secret, f.uri, f.err
}

func (f *fakeSecondFactor) ConfirmSecondFactor(_ context.Context, userID, code string) error {
	f.userID, f.code, f.acted = userID, code, "confirm"
	return f.err
}

func (f *fakeSecondFactor) RemoveSecondFactor(_ context.Context, userID, code string) error {
	f.userID, f.code, f.acted = userID, code, "remove"
	return f.err
}

// secondFactorRequest sends a request to the screen as a signed-in person
// holding the given privileges.
func secondFactorRequest(t *testing.T, surface SecondFactorAdmin, method, path string, form url.Values, scopes ...string) *httptest.ResponseRecorder {
	t.Helper()

	templates, err := loadTemplates()
	require.NoError(t, err)
	ui := &UI{templates: templates, secondFactor: surface}
	r := chi.NewRouter()
	r.Get(SecondFactorPath, ui.showSecondFactor)
	r.Post(SecondFactorEnrollPath, ui.submitSecondFactorEnroll)
	r.Post(SecondFactorConfirmPath, ui.submitSecondFactorConfirm)
	r.Post(SecondFactorRemovePath, ui.submitSecondFactorRemove)

	req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(corehttp.WithPrincipal(req.Context(),
		corehttp.Principal{ID: "usr_person", Kind: "user", Scopes: scopes}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// TestThePersonWithNoFactorIsOfferedOne draws the screen for an account with
// none: one form, which takes no code, and nothing to remove.
func TestThePersonWithNoFactorIsOfferedOne(t *testing.T) {
	t.Parallel()

	surface := &fakeSecondFactor{}
	rec := secondFactorRequest(t, surface, http.MethodGet, SecondFactorPath, nil, "order:read")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Equal(t, "usr_person", surface.userID, "the screen is about the person the session proved")
	assert.Contains(t, body, `action="`+SecondFactorEnrollPath+`"`)
	assert.Contains(t, body, "This account has no second factor.")
	assert.NotContains(t, body, `action="`+SecondFactorRemovePath+`"`, "there is nothing to remove")
	assert.NotContains(t, body, `name="code"`, "the first enrolment takes no code")
}

// TestAnEnrolmentIsShownOnceWithItsLink holds the one moment the secret is
// printed: as text and as an otpauth link the template does not neuter, with
// the confirmation beside it.
func TestAnEnrolmentIsShownOnceWithItsLink(t *testing.T) {
	t.Parallel()

	surface := &fakeSecondFactor{
		secret: "JBSWY3DPEHPK3PXP", uri: "otpauth://totp/gobit:a@b?secret=JBSWY3DPEHPK3PXP", waiting: true,
	}
	rec := secondFactorRequest(t, surface, http.MethodPost, SecondFactorEnrollPath, url.Values{})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Equal(t, "enroll", surface.acted)
	assert.Contains(t, body, "JBSWY3DPEHPK3PXP")
	assert.Contains(t, body, `href="otpauth://totp/gobit:a@b?secret=JBSWY3DPEHPK3PXP"`,
		"the link survives the template's URL filter")
	assert.Contains(t, body, `action="`+SecondFactorConfirmPath+`"`)

	again := secondFactorRequest(t, surface, http.MethodGet, SecondFactorPath, nil)
	assert.NotContains(t, again.Body.String(), "JBSWY3DPEHPK3PXP", "the secret is shown once, not on the next visit")
}

// TestALinkOfAnotherSchemeIsNotALink keeps the scheme check the template's
// filter was lifted for.
func TestALinkOfAnotherSchemeIsNotALink(t *testing.T) {
	t.Parallel()

	surface := &fakeSecondFactor{secret: "S", uri: "javascript:alert(1)"}
	rec := secondFactorRequest(t, surface, http.MethodPost, SecondFactorEnrollPath, url.Values{})

	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), `href="javascript`)
	assert.NotContains(t, rec.Body.String(), "Open in the authenticator")
}

// TestAProvenFactorIsChangedWithItsCode offers the replacement and the removal
// with a code field each, and hands the typed code to the surface.
func TestAProvenFactorIsChangedWithItsCode(t *testing.T) {
	t.Parallel()

	surface := &fakeSecondFactor{proven: true}
	page := secondFactorRequest(t, surface, http.MethodGet, SecondFactorPath, nil, "order:read")
	require.Equal(t, http.StatusOK, page.Code)
	body := page.Body.String()
	assert.Contains(t, body, "This account is protected by an authenticator.")
	assert.Contains(t, body, `action="`+SecondFactorRemovePath+`"`)
	assert.Equal(t, 2, strings.Count(body, `name="code"`), "both the replacement and the removal take a code")

	rec := secondFactorRequest(t, surface, http.MethodPost, SecondFactorRemovePath, url.Values{"code": {" 123456 "}})
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, SecondFactorPath, rec.Header().Get("Location"))
	assert.Equal(t, "remove", surface.acted)
	assert.Equal(t, "123456", surface.code)
}

// TestARefusalIsWordedForThePerson turns the auth module's refusals into the
// panel's sentences, on the same screen.
func TestARefusalIsWordedForThePerson(t *testing.T) {
	t.Parallel()

	for code, sentence := range map[string]string{
		CodeMFARequired:    "Enter the six digits your current authenticator shows to change it.",
		CodeMFACodeWrong:   "That code is not the one the authenticator shows now.",
		CodeMFALocked:      "Too many wrong codes have locked this account.",
		CodeMFAUnavailable: "MFA_SECRET_KEY is not set.",
	} {
		surface := &fakeSecondFactor{proven: true, err: errors.Forbidden(code, "refused")}
		rec := secondFactorRequest(t, surface, http.MethodPost, SecondFactorRemovePath, url.Values{"code": {"000000"}})
		assert.Contains(t, rec.Body.String(), sentence, code)
		assert.Contains(t, rec.Body.String(), `action="`+SecondFactorRemovePath+`"`, "%s leaves the person on the screen", code)
	}
}

// TestAPersonWhoOwesAFactorIsToldWhy draws the sentence the door sends them
// there for.
func TestAPersonWhoOwesAFactorIsToldWhy(t *testing.T) {
	t.Parallel()

	rec := secondFactorRequest(t, &fakeSecondFactor{owed: true}, http.MethodGet, SecondFactorPath, nil)

	assert.Contains(t, rec.Body.String(), "This installation requires a second factor")
}

// TestAnInstallationWithoutTheSurfaceSaysSo keeps the optional resolution
// honest: the screen names the missing surface rather than failing.
func TestAnInstallationWithoutTheSurfaceSaysSo(t *testing.T) {
	t.Parallel()

	rec := secondFactorRequest(t, nil, http.MethodGet, SecondFactorPath, nil)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "second factor surface is not registered")
}
