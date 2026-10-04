package identitysession

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/identitytest"
)

// The tests in this file are the RULES of the session anchor (ADR 0374). What
// the two statements do against a real table — a NULL anchor, a moment that
// never moves back, an erasure taking it — is in the integration lane.

// steppingClock is a clock a test moves by hand, so "issued before the anchor"
// is a fact the test set rather than a race it hoped to win.
type steppingClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *steppingClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.at
}

func (c *steppingClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.at = c.at.Add(d)
}

// anchoredStore keeps one customer's credential, their pending reset and their
// anchor, and can be told to fail ending sessions.
type anchoredStore struct {
	mu         sync.Mutex
	customerID string
	email      string
	hash       string
	anchor     time.Time
	reset      string // token hash
	puts       int
	reads      int // credentials read by customer
	endErr     error
	readErr    error
	putErr     error
	// afterPut runs once a write has succeeded, as a caller hanging up the
	// moment the change is made.
	afterPut func()
	// rounds writes a moment at a microsecond, rounding, which is what
	// PostgreSQL does to a timestamp it is sent as text.
	rounds bool
	// moving is the pending address change, by its token's hash.
	moving map[string]string
}

func (s *anchoredStore) Credential(_ context.Context, email string) (customerID, passwordHash string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if foldEmail(email) != s.email {
		return "", "", ErrPasswordMismatch
	}

	return s.customerID, s.hash, nil
}

func (s *anchoredStore) Put(_ context.Context, customerID, email, passwordHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.putErr != nil {
		return s.putErr
	}
	s.customerID, s.email, s.hash = customerID, foldEmail(email), passwordHash
	s.puts++
	if s.afterPut != nil {
		s.afterPut()
	}

	return nil
}

func (s *anchoredStore) SessionsValidFrom(_ context.Context, customerID string) (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readErr != nil {
		return time.Time{}, s.readErr
	}
	if customerID != s.customerID {
		return time.Time{}, nil
	}

	return s.anchor, nil
}

func (s *anchoredStore) EndSessionsBefore(_ context.Context, customerID string, moment time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.endErr != nil {
		return s.endErr
	}
	if customerID != s.customerID {
		return ErrNoCredential
	}
	if s.rounds {
		moment = moment.Round(time.Microsecond)
	}
	if moment.After(s.anchor) {
		s.anchor = moment
	}

	return nil
}

func (s *anchoredStore) CredentialOf(_ context.Context, customerID string) (email, passwordHash string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	if customerID != s.customerID {
		return "", "", ErrNoCredential
	}

	return s.email, s.hash, nil
}

func (s *anchoredStore) PutAddressChange(_ context.Context, tokenHash, _, email string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.moving = map[string]string{tokenHash: email}

	return nil
}

func (s *anchoredStore) TakeAddressChange(_ context.Context, tokenHash string) (customerID, email string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	email, ok := s.moving[tokenHash]
	if !ok {
		return "", "", ErrNoAddressChange
	}
	delete(s.moving, tokenHash)

	return s.customerID, email, nil
}

func (s *anchoredStore) PutPasswordReset(_ context.Context, tokenHash, _ string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reset = tokenHash

	return nil
}

func (s *anchoredStore) TakePasswordReset(_ context.Context, tokenHash string) (customerID, email string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tokenHash == "" || tokenHash != s.reset {
		return "", "", ErrNoPasswordReset
	}
	s.reset = ""

	return s.customerID, s.email, nil
}

// lastLink is a reset sender that keeps the newest token.
type lastLink struct{ token string }

func (l *lastLink) SendPasswordReset(_ context.Context, _, token string) error {
	l.token = token

	return nil
}

// anchoredModule is a module over an anchored store holding one account, its
// clock under the test's hand, and its routes.
func anchoredModule(t *testing.T) (*anchoredStore, *steppingClock, *lastLink, chi.Router) {
	t.Helper()

	hash, err := HashPassword("the old password")
	require.NoError(t, err)
	store := &anchoredStore{customerID: testCustomerID, email: "known@example.test", hash: hash}
	link := &lastLink{}
	m := New(Options{Secret: testSecret, Credentials: store, PasswordReset: link, Limiter: allowAll{}})
	require.NoError(t, m.Register(context.Background(), container.New(nil)))
	clock := &steppingClock{at: time.Date(2026, time.October, 3, 9, 0, 0, 0, time.UTC)}
	m.sessions.now = clock.now
	r := chi.NewRouter()
	m.Routes(r)

	return store, clock, link, r
}

// allowAll is a limiter that never limits.
type allowAll struct{}

func (allowAll) Allow(context.Context, string) (corehttp.Decision, error) {
	return corehttp.Decision{Allowed: true}, nil
}

// signIn signs the account in and answers the cookie.
func signIn(t *testing.T, r chi.Router, password string) *http.Cookie {
	t.Helper()

	rec := post(t, r, "/store/v1/auth/sign-in",
		`{"email":"known@example.test","password":"`+password+`"}`)
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())

	return sessionCookie(t, rec)
}

// postWith sends a POST carrying a cookie.
func postWith(t *testing.T, r chi.Router, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, http.NoBody)
	req.Header.Set("Cookie", cookie.Name+"="+cookie.Value)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// TestTheAnchorIsComparedAtAMillisecond: a session issued in an earlier
// millisecond than the anchor proves nobody; one issued in the anchor's own
// millisecond, which is the cookie a reset answers with, counts; no anchor
// ends nothing.
func TestTheAnchorIsComparedAtAMillisecond(t *testing.T) {
	t.Parallel()

	anchor := time.Date(2026, time.October, 3, 9, 0, 0, 700_400_000, time.UTC)
	for name, tc := range map[string]struct {
		issued time.Time
		anchor time.Time
		before bool
	}{
		"a millisecond earlier":    {issued: anchor.Add(-time.Millisecond), anchor: anchor, before: true},
		"a second earlier":         {issued: anchor.Add(-time.Second), anchor: anchor, before: true},
		"in the anchor's own":      {issued: anchor.Add(300 * time.Microsecond), anchor: anchor, before: false},
		"later":                    {issued: anchor.Add(time.Hour), anchor: anchor, before: false},
		"no anchor":                {issued: anchor, anchor: time.Time{}, before: false},
		"a cookie with no issue":   {issued: time.Time{}, anchor: anchor, before: true},
		"no issue and no anchor":   {issued: time.Time{}, anchor: time.Time{}, before: false},
		"the millisecond's bottom": {issued: anchor.Truncate(time.Millisecond), anchor: anchor, before: false},
	} {
		assert.Equal(t, tc.before, issuedBefore(tc.issued, tc.anchor), name)
	}
}

// TestASessionIssuedBeforeTheAnchorProvesNobody: the verifier reads the anchor
// and refuses with the one text every refusal has.
func TestASessionIssuedBeforeTheAnchorProvesNobody(t *testing.T) {
	t.Parallel()

	clock := &steppingClock{at: time.Date(2026, time.October, 3, 9, 0, 0, 0, time.UTC)}
	store := &anchoredStore{customerID: testCustomerID}
	sessions := newSessions(t, clock.now)
	sessions.anchors = store

	rec := httptest.NewRecorder()
	sessions.Issue(rec, testCustomerID)
	before := sessionCookie(t, rec)

	proven, err := sessions.CustomerID(requestWith(before))
	require.NoError(t, err, "with no anchor every session counts")
	assert.Equal(t, testCustomerID, proven)

	clock.advance(time.Millisecond)
	require.NoError(t, store.EndSessionsBefore(context.Background(), testCustomerID, clock.now()))
	rec = httptest.NewRecorder()
	sessions.Issue(rec, testCustomerID)
	after := sessionCookie(t, rec)

	_, err = sessions.CustomerID(requestWith(before))
	require.Error(t, err)
	assert.Equal(t, errNoSession.Error(), err.Error(), "a fifth refusal, told apart from none of the four")
	proven, err = sessions.CustomerID(requestWith(after))
	require.NoError(t, err, "a session issued at the anchor counts")
	assert.Equal(t, testCustomerID, proven)
}

// TestACookieSealedBeforeCookiesCarriedTheirIssueCountsUntilAnAnchor: a
// session issued before this change has no moment of issue; it works where
// nothing was ever ended, and any anchor ends it.
func TestACookieSealedBeforeCookiesCarriedTheirIssueCountsUntilAnAnchor(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.October, 3, 9, 0, 0, 0, time.UTC)
	store := &anchoredStore{customerID: testCustomerID}
	sessions := newSessions(t, fixedClock(at))
	sessions.anchors = store

	payload := testCustomerID + "." + strconv.FormatInt(at.Add(time.Hour).Unix(), 10)
	legacy := &http.Cookie{
		Name:  DefaultCookieName,
		Value: payload + "." + base64.RawURLEncoding.EncodeToString(sessions.sign(purposeSession, payload)),
	}

	proven, err := sessions.CustomerID(requestWith(legacy))
	require.NoError(t, err, "a session from before the change still works")
	assert.Equal(t, testCustomerID, proven)

	require.NoError(t, store.EndSessionsBefore(context.Background(), testCustomerID, at.Add(-24*time.Hour)))
	_, err = sessions.CustomerID(requestWith(legacy))
	assert.Error(t, err, "any anchor ends a session whose issue nobody knows")
}

// TestAnAnchorThatCannotBeReadIsAFailureNotARefusal: the caller is told the
// shop failed, not that they are nobody — the error is classified, which is
// what corehttp.ProvenCustomer passes through (ADR 0371).
func TestAnAnchorThatCannotBeReadIsAFailureNotARefusal(t *testing.T) {
	t.Parallel()

	store, _, _, r := anchoredModule(t)
	cookie := signIn(t, r, "the old password")
	store.readErr = errors.New("the database is down")

	rec := get(t, r, sessionPath, cookie)
	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), CodeUnavailable)

	sessions := newSessions(t, time.Now)
	sessions.anchors = store
	_, err := sessions.CustomerID(requestWith(cookie))
	require.Error(t, err)
	assert.True(t, coreerrors.HasKind(err, coreerrors.KindInternal), "classified, so it passes through: %v", err)
}

// TestEndingTheOtherSessionsKeepsThisOne: two browsers, one ends the other.
func TestEndingTheOtherSessionsKeepsThisOne(t *testing.T) {
	t.Parallel()

	_, clock, _, r := anchoredModule(t)
	here := signIn(t, r, "the old password")
	there := signIn(t, r, "the old password")
	clock.advance(time.Millisecond)

	rec := postWith(t, r, "/store/v1/auth/sessions/revoke-others", here)
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	renewed := sessionCookie(t, rec)

	assert.Equal(t, http.StatusUnauthorized, get(t, r, sessionPath, there).Code, "the other browser is out")
	assert.Equal(t, http.StatusUnauthorized, get(t, r, sessionPath, here).Code,
		"so is the cookie this browser sent, which is why it gets a new one")
	assert.Equal(t, http.StatusOK, get(t, r, sessionPath, renewed).Code, "this browser stays in")
}

// TestTheAnchorIsWrittenAtTheResolutionItIsComparedAt: a store may round a
// moment to its own resolution — PostgreSQL rounds a timestamp sent as text,
// which a connection through a statement pooler does, to microseconds — and a
// moment 0.4µs under a millisecond rounds into the next one, past the cookie
// the same request issues. The module truncates the anchor to the millisecond
// before it writes it.
func TestTheAnchorIsWrittenAtTheResolutionItIsComparedAt(t *testing.T) {
	t.Parallel()

	store, clock, _, r := anchoredModule(t)
	store.rounds = true
	here := signIn(t, r, "the old password")
	clock.advance(time.Second + 999_600*time.Nanosecond)

	rec := postWith(t, r, "/store/v1/auth/sessions/revoke-others", here)
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	assert.Equal(t, http.StatusOK, get(t, r, sessionPath, sessionCookie(t, rec)).Code,
		"the session this request issued counts")
}

// TestEndingSessionsAsksForAProvenCustomer: no session, nothing to end.
func TestEndingSessionsAsksForAProvenCustomer(t *testing.T) {
	t.Parallel()

	_, _, _, r := anchoredModule(t)
	rec := post(t, r, "/store/v1/auth/sessions/revoke-others", "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), CodeNoSession)
}

// TestACustomerWithNoPasswordHereCannotEndTheirSessionsHere: a customer an
// embedder signed in some other way has no anchor to move, and is told so
// rather than told it worked.
func TestACustomerWithNoPasswordHereCannotEndTheirSessionsHere(t *testing.T) {
	t.Parallel()

	store, _, _, r := anchoredModule(t)
	stranger := newSessions(t, time.Now)
	stranger.anchors = store
	rec := httptest.NewRecorder()
	stranger.Issue(rec, "cust_SIGNED_IN_ELSEWHERE")

	answer := postWith(t, r, "/store/v1/auth/sessions/revoke-others", sessionCookie(t, rec))
	assert.Equal(t, http.StatusConflict, answer.Code, answer.Body.String())
	assert.Contains(t, answer.Body.String(), CodeSessionsNotEnded)
}

// TestAResetEndsTheSessionsBeforeIt: the session the old password opened stops
// proving anybody, and the one the reset answers with works.
func TestAResetEndsTheSessionsBeforeIt(t *testing.T) {
	t.Parallel()

	_, clock, link, r := anchoredModule(t)
	stolen := signIn(t, r, "the old password")
	clock.advance(time.Millisecond)

	require.Equal(t, http.StatusAccepted,
		post(t, r, "/store/v1/auth/password-reset", `{"email":"known@example.test"}`).Code)
	rec := post(t, r, "/store/v1/auth/password-reset/confirm",
		`{"token":"`+link.token+`","password":"the new password"}`)
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())

	assert.Equal(t, http.StatusUnauthorized, get(t, r, sessionPath, stolen).Code,
		"a session opened before the reset ends with it")
	assert.Equal(t, http.StatusOK, get(t, r, sessionPath, sessionCookie(t, rec)).Code,
		"the reset's own session counts")
}

// TestAResetThatCannotEndTheSessionsLeavesThePassword: the anchor moves
// BEFORE the password, so a failure between them has replaced nothing.
func TestAResetThatCannotEndTheSessionsLeavesThePassword(t *testing.T) {
	t.Parallel()

	store, _, link, r := anchoredModule(t)
	require.Equal(t, http.StatusAccepted,
		post(t, r, "/store/v1/auth/password-reset", `{"email":"known@example.test"}`).Code)
	store.endErr = errors.New("the database is down")

	rec := post(t, r, "/store/v1/auth/password-reset/confirm",
		`{"token":"`+link.token+`","password":"the new password"}`)
	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	assert.Zero(t, store.puts, "the password was not replaced while the old sessions still worked")
	assert.NoError(t, VerifyPassword(store.hash, "the old password"))
}

// TestAnOperatorReplacingAPasswordEndsTheSessions: the same rule as a reset;
// a credential written for a customer who had none needs nothing ended.
func TestAnOperatorReplacingAPasswordEndsTheSessions(t *testing.T) {
	t.Parallel()

	store, clock, _, r := anchoredModule(t)
	before := signIn(t, r, "the old password")
	clock.advance(time.Millisecond)

	rec := sendTo(t, r, http.MethodPut, "/admin/v1/customer-credentials",
		`{"customer_id":"`+testCustomerID+`","email":"known@example.test","password":"set by support"}`)
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	assert.Equal(t, http.StatusUnauthorized, get(t, r, sessionPath, before).Code)

	rec = sendTo(t, r, http.MethodPut, "/admin/v1/customer-credentials",
		`{"customer_id":"cust_NEW","email":"new@example.test","password":"a first one"}`)
	assert.Equal(t, http.StatusNoContent, rec.Code, "a new credential has no sessions to end: %s", rec.Body.String())

	store.endErr = errors.New("the database is down")
	rec = sendTo(t, r, http.MethodPut, "/admin/v1/customer-credentials",
		`{"customer_id":"cust_NEW","email":"new@example.test","password":"a second one"}`)
	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
}

// TestAStoreThatKeepsNoAnchorEndsNothing: the shape before ADR 0374 — no route
// to end sessions, and a session counts until it expires.
func TestAStoreThatKeepsNoAnchorEndsNothing(t *testing.T) {
	t.Parallel()

	hash, err := HashPassword("the real password")
	require.NoError(t, err)
	m := newModuleWith(t, fakeCredentials{customerID: testCustomerID, hash: hash})
	assert.Nil(t, m.sessions.anchors)
	r := chi.NewRouter()
	m.Routes(r)

	cookie := signIn(t, r, "the real password")
	rec := postWith(t, r, "/store/v1/auth/sessions/revoke-others", cookie)
	assert.Contains(t, []int{http.StatusNotFound, http.StatusMethodNotAllowed}, rec.Code)
}

// TestAnAnchoredVerifierPassesTheSuiteGobitPublishes: reading the anchor does
// not touch what the suite guards.
func TestAnAnchoredVerifierPassesTheSuiteGobitPublishes(t *testing.T) {
	t.Parallel()

	sessions := newSessions(t, time.Now)
	sessions.anchors = &anchoredStore{customerID: testCustomerID}
	identitytest.Contract(t, sessions)
}

// sendTo sends a JSON body with a method.
func sendTo(t *testing.T, r chi.Router, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}
