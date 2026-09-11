package identitysession

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/identitytest"
)

// testSecret is a signing secret no other test shares.
var testSecret = []byte("a-test-signing-secret-of-thirty-two-plus")

// testCustomerID is the customer the sessions in these tests belong to.
const testCustomerID = "cust_06G8TESTCUSTOMER00000000"

// newSessions builds a verifier with the clock under the test's control.
func newSessions(t *testing.T, now func() time.Time) *Sessions {
	t.Helper()

	return &Sessions{
		secret:     testSecret,
		ttl:        DefaultTTL,
		cookieName: DefaultCookieName,
		secure:     true,
		now:        now,
	}
}

// fixedClock is a clock that does not move.
func fixedClock(at time.Time) func() time.Time { return func() time.Time { return at } }

// TestItPassesTheSuiteGobitPublishes is the test this module was written to
// pass.
//
// gobit cannot check an embedder's verifier and says so (ADR 0126). What it can
// do is publish the rules, and a module shipped IN this repository proving it
// obeys them is the only in-tree run those rules will ever get — every other
// corehttp.Identity here is a test double.
func TestItPassesTheSuiteGobitPublishes(t *testing.T) {
	t.Parallel()

	identitytest.Contract(t, newSessions(t, time.Now))
}

// TestASessionRoundTrips is the happy path and the one thing the suite cannot
// check: that a cookie this module issued is one it accepts.
func TestASessionRoundTrips(t *testing.T) {
	t.Parallel()

	sessions := newSessions(t, time.Now)
	rec := httptest.NewRecorder()
	sessions.Issue(rec, testCustomerID)

	cookie := sessionCookie(t, rec)
	assert.True(t, cookie.HttpOnly, "a session a script can read is one an injected script can steal")
	assert.True(t, cookie.Secure)
	assert.Equal(t, http.SameSiteLaxMode, cookie.SameSite)

	id, err := sessions.CustomerID(requestWith(cookie))
	require.NoError(t, err)
	assert.Equal(t, testCustomerID, id)
}

// TestAnEditedCookieIsRefused is the rule the MAC exists for.
//
// The payload is readable — it is not encrypted and does not need to be — so the
// only thing standing between a caller and any customer's session is that the
// signature does not follow the edit.
func TestAnEditedCookieIsRefused(t *testing.T) {
	t.Parallel()

	sessions := newSessions(t, time.Now)
	rec := httptest.NewRecorder()
	sessions.Issue(rec, testCustomerID)
	cookie := sessionCookie(t, rec)

	for name, edited := range map[string]string{
		"another customer": strings.Replace(cookie.Value, testCustomerID, "cust_SOMEBODY_ELSE", 1),
		"a later expiry": strings.Replace(cookie.Value,
			"."+expiryOf(t, cookie.Value)+".", ".99999999999.", 1),
		"no signature": payloadOf(t, cookie.Value),
		"empty":        "",
		"a lone dot":   ".",
		"junk":         "not-a-session",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := sessions.CustomerID(requestWith(&http.Cookie{
				Name: DefaultCookieName, Value: edited,
			}))
			require.Error(t, err, "an edited cookie must not prove anybody")
			assert.Equal(t, errNoSession.Error(), err.Error(),
				"every failure answers the same text; telling them apart tells a forger "+
					"which half of the forgery worked")
		})
	}
}

// TestACookieFromAnotherKeyIsRefused is what makes the secret worth protecting.
func TestACookieFromAnotherKeyIsRefused(t *testing.T) {
	t.Parallel()

	other := &Sessions{
		secret:     []byte("a different secret of thirty-two bytes!!"),
		ttl:        DefaultTTL,
		cookieName: DefaultCookieName,
		now:        time.Now,
	}
	rec := httptest.NewRecorder()
	other.Issue(rec, testCustomerID)

	_, err := newSessions(t, time.Now).CustomerID(requestWith(sessionCookie(t, rec)))
	assert.Error(t, err, "a cookie signed with another key must prove nobody")
}

// TestAnExpiredSessionIsRefused proves the expiry is READ and not only written.
//
// The cookie's own Expires attribute is a request to the browser; a caller who
// keeps sending an old cookie is not obliged to honor it. What refuses them is
// the stamp inside the MAC.
func TestAnExpiredSessionIsRefused(t *testing.T) {
	t.Parallel()

	issued := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	sessions := newSessions(t, fixedClock(issued))
	rec := httptest.NewRecorder()
	sessions.Issue(rec, testCustomerID)
	cookie := sessionCookie(t, rec)

	sessions.now = fixedClock(issued.Add(DefaultTTL - time.Minute))
	id, err := sessions.CustomerID(requestWith(cookie))
	require.NoError(t, err, "a minute before the end the session is still good")
	assert.Equal(t, testCustomerID, id)

	sessions.now = fixedClock(issued.Add(DefaultTTL + time.Second))
	_, err = sessions.CustomerID(requestWith(cookie))
	assert.Error(t, err, "a second past the end it proves nobody")
}

// TestClearingLeavesACookieThatProvesNobody is why Clear overwrites rather than
// only asking the browser to drop the cookie.
func TestClearingLeavesACookieThatProvesNobody(t *testing.T) {
	t.Parallel()

	sessions := newSessions(t, time.Now)
	rec := httptest.NewRecorder()
	sessions.Clear(rec)

	cookie := sessionCookie(t, rec)
	assert.Empty(t, cookie.Value)
	_, err := sessions.CustomerID(requestWith(cookie))
	assert.Error(t, err, "a client that ignores MaxAge still sends what it holds")
}

// TestTheSecretIsRequired refuses the installation that would let anybody mint a
// session.
func TestTheSecretIsRequired(t *testing.T) {
	t.Parallel()

	for name, secret := range map[string][]byte{
		"absent": nil,
		"short":  []byte("too short"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := New(Options{Secret: secret}).Register(context.Background(), container.New(nil))

			require.Error(t, err, "a %s secret must stop the startup", name)
			assert.Contains(t, err.Error(), "Secret",
				"the failure has to name the field an operator has to set")
		})
	}
}

// TestAWrongPasswordAndAnUnknownAddressAnswerTheSame is the oracle rule.
//
// A sign-in that answered differently would let anybody enumerate which e-mail
// addresses have accounts, one request at a time, without ever signing in.
func TestAWrongPasswordAndAnUnknownAddressAnswerTheSame(t *testing.T) {
	t.Parallel()

	hash, err := HashPassword("the real password")
	require.NoError(t, err)

	r := chi.NewRouter()
	newModuleWith(t, fakeCredentials{customerID: testCustomerID, hash: hash}).Routes(r)

	wrongPassword := post(t, r, "/store/v1/auth/sign-in",
		`{"email":"known@example.test","password":"not it"}`)
	unknownAddress := post(t, r, "/store/v1/auth/sign-in",
		`{"email":"nobody@example.test","password":"not it"}`)

	assert.Equal(t, http.StatusUnauthorized, wrongPassword.Code)
	assert.Equal(t, unknownAddress.Code, wrongPassword.Code,
		"the two must answer the same STATUS")
	assert.JSONEq(t, unknownAddress.Body.String(), wrongPassword.Body.String(),
		"and the same BODY; a difference is an address enumerator")
	assert.Empty(t, wrongPassword.Result().Cookies(), "no cookie is issued either way")
}

// TestSigningInIssuesTheSession is the endpoint's happy path.
func TestSigningInIssuesTheSession(t *testing.T) {
	t.Parallel()

	hash, err := HashPassword("the real password")
	require.NoError(t, err)

	m := newModuleWith(t, fakeCredentials{customerID: testCustomerID, hash: hash})
	r := chi.NewRouter()
	m.Routes(r)

	rec := post(t, r, "/store/v1/auth/sign-in",
		`{"email":"known@example.test","password":"the real password"}`)

	require.Equal(t, http.StatusNoContent, rec.Code, "body: %s", rec.Body.String())
	require.Empty(t, rec.Body.String(),
		"the answer carries no body: an echoed identifier lands in every logged URL")

	id, err := m.Sessions().CustomerID(requestWith(sessionCookie(t, rec)))
	require.NoError(t, err)
	assert.Equal(t, testCustomerID, id, "the cookie it issued proves the customer it read")
}

// TestRoutesMountNothingWithoutRegister keeps a handler with no secret from
// answering.
func TestRoutesMountNothingWithoutRegister(t *testing.T) {
	t.Parallel()

	r := chi.NewRouter()
	New(Options{Secret: testSecret}).Routes(r)

	rec := post(t, r, "/store/v1/auth/sign-in", `{"email":"a@b.test","password":"x"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code,
		"an endpoint that exists and panics is worse than one that does not exist")
}

// fakeCredentials answers for one address.
type fakeCredentials struct {
	customerID string
	hash       string
}

// Credential answers for "known@example.test" and refuses everything else the
// way a wrong password is refused.
func (f fakeCredentials) Credential(
	_ context.Context, email string,
) (customerID, passwordHash string, err error) {
	if foldEmail(email) != "known@example.test" {
		return "", "", ErrPasswordMismatch
	}

	return f.customerID, f.hash, nil
}

// Put records nothing; no test here writes.
func (f fakeCredentials) Put(context.Context, string, string, string) error {
	return errors.New("not needed")
}

// newModuleWith registers a module on a store the test wrote.
func newModuleWith(t *testing.T, store Credentials) *Module {
	t.Helper()

	m := New(Options{Secret: testSecret, Credentials: store})
	require.NoError(t, m.Register(context.Background(), container.New(nil)))

	return m
}

// post sends a JSON body.
func post(t *testing.T, r chi.Router, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// sessionCookie reads this module's cookie out of a response.
func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()

	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == DefaultCookieName {
			return cookie
		}
	}
	t.Fatalf("the response set no %q cookie; it set %d", DefaultCookieName,
		len(rec.Result().Cookies()))

	return nil
}

// requestWith is a request carrying one cookie.
func requestWith(cookie *http.Cookie) *http.Request {
	r := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/store/v1/customers/"+testCustomerID, http.NoBody)
	r.Header.Set("Cookie", cookie.Name+"="+cookie.Value)

	return r
}

// payloadOf returns a cookie value without its signature.
func payloadOf(t *testing.T, value string) string {
	t.Helper()

	payload, _, found := cutLast(value)
	require.True(t, found, "the cookie value has no signature to cut")

	return payload
}

// expiryOf returns the expiry stamp out of a cookie value.
func expiryOf(t *testing.T, value string) string {
	t.Helper()

	_, stamp, found := strings.Cut(payloadOf(t, value), ".")
	require.True(t, found, "the payload carries no expiry")

	return stamp
}

// TestTheStoredHashCarriesItsOwnCost proves a credential keeps the parameters it
// was written with.
//
// It is what makes raising the cost safe: a hash written at today's cost still
// verifies tomorrow, so nobody's password stops working when the constants move.
func TestTheStoredHashCarriesItsOwnCost(t *testing.T) {
	t.Parallel()

	hash, err := HashPassword("a password")
	require.NoError(t, err)

	fields := strings.Split(hash, "$")
	require.Len(t, fields, 6, "the stored form is $argon2id$v=..$m=..,t=..,p=..$salt$key")
	assert.Equal(t, "argon2id", fields[1])
	assert.Contains(t, fields[3], "m=", "the cost is IN the hash, not only in the constants")

	require.NoError(t, VerifyPassword(hash, "a password"))
	assert.ErrorIs(t, VerifyPassword(hash, "another password"), ErrPasswordMismatch)
}

// TestTwoHashesOfOnePasswordDiffer is the salt doing its job.
//
// A shared salt would let one table-wide precomputation answer every credential
// at once, and two identical hashes in the table would say which customers chose
// the same password.
func TestTwoHashesOfOnePasswordDiffer(t *testing.T) {
	t.Parallel()

	first, err := HashPassword("the same password")
	require.NoError(t, err)
	second, err := HashPassword("the same password")
	require.NoError(t, err)

	assert.NotEqual(t, first, second, "the salt is per password and random")
	require.NoError(t, VerifyPassword(first, "the same password"))
	require.NoError(t, VerifyPassword(second, "the same password"))
}

// TestACorruptStoredHashIsRefusedAsAMismatch keeps a broken row out of an
// operator's error log and out of a caller's answer.
func TestACorruptStoredHashIsRefusedAsAMismatch(t *testing.T) {
	t.Parallel()

	good, err := HashPassword("a password")
	require.NoError(t, err)

	for name, stored := range map[string]string{
		"empty":           "",
		"not argon2id":    "$bcrypt$v=19$m=1,t=1,p=1$c2FsdA$a2V5",
		"missing fields":  "$argon2id$v=19$m=65536,t=1,p=4",
		"bad salt":        "$argon2id$v=19$m=65536,t=1,p=4$!!!$a2V5",
		"truncated key":   strings.TrimSuffix(good, "AAAA") + "!!!!",
		"unreadable cost": "$argon2id$v=19$m=lots,t=1,p=4$c2FsdA$a2V5",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.ErrorIs(t, VerifyPassword(stored, "a password"), ErrPasswordMismatch,
				"a row this module cannot read must answer like a wrong password")
		})
	}
}

// TestTheVerifierDoesNotPrintItsSecret keeps a signing secret out of a log line.
func TestTheVerifierDoesNotPrintItsSecret(t *testing.T) {
	t.Parallel()

	printed := stringOf(newSessions(t, time.Now))

	assert.NotContains(t, printed, string(testSecret),
		"a secret printed once into a log file is a secret an operator has to rotate")
	assert.Contains(t, printed, DefaultCookieName)
}

// stringOf formats a value the way a log line would.
func stringOf(v any) string {
	raw, err := json.Marshal(map[string]string{"v": fmtOf(v)})
	if err != nil {
		return ""
	}

	return string(raw)
}

// fmtOf is %v on a value.
func fmtOf(v any) string {
	if stringer, ok := v.(interface{ String() string }); ok {
		return stringer.String()
	}

	return ""
}

// TestASealedValueIsNotASession is the whole reason the MAC carries a purpose.
//
// One key signs both, and the payload of a sealed value is often something a
// caller influences — a passkey ceremony's challenge is the case this was
// written for. Without domain separation a caller who could steer that payload
// could mint a token that VERIFIES as a session and names whatever customer they
// spelled. With it, neither shape can be read as the other whatever the bytes
// say.
func TestASealedValueIsNotASession(t *testing.T) {
	t.Parallel()

	sessions := newSessions(t, time.Now)

	// A sealed value whose payload spells a session's shape exactly.
	forged := sessions.SealValue(testCustomerID, time.Hour)

	_, err := sessions.CustomerID(requestWith(&http.Cookie{
		Name: DefaultCookieName, Value: forged,
	}))
	require.Error(t, err, "a sealed value must not be readable as a session")

	// And the other direction: a session cookie is not a sealed value.
	rec := httptest.NewRecorder()
	sessions.Issue(rec, testCustomerID)

	_, err = sessions.OpenValue(sessionCookie(t, rec).Value)
	assert.Error(t, err, "a session must not be readable as a sealed value")
}

// TestASealedValueRoundTripsAndExpires is the primitive's own contract.
func TestASealedValueRoundTripsAndExpires(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.September, 11, 9, 0, 0, 0, time.UTC)
	sessions := newSessions(t, fixedClock(at))

	// A payload with the separators the format uses, so a naive parse breaks.
	const awkward = `{"challenge":"a.b.c","user":"cust_1"}`
	sealed := sessions.SealValue(awkward, 5*time.Minute)

	got, err := sessions.OpenValue(sealed)
	require.NoError(t, err)
	assert.Equal(t, awkward, got, "the value comes back byte for byte")

	sessions.now = fixedClock(at.Add(5*time.Minute + time.Second))
	_, err = sessions.OpenValue(sealed)
	assert.Error(t, err, "a value past its ttl proves nothing")
}

// TestASealedValueRefusesAnEditedOne keeps the MAC honest on this path too.
func TestASealedValueRefusesAnEditedOne(t *testing.T) {
	t.Parallel()

	sessions := newSessions(t, time.Now)
	sealed := sessions.SealValue("the original", time.Hour)

	for name, edited := range map[string]string{
		"another payload": strings.Replace(sealed,
			base64.RawURLEncoding.EncodeToString([]byte("the original")),
			base64.RawURLEncoding.EncodeToString([]byte("the forged!!")), 1),
		"no signature": payloadOf(t, sealed),
		"empty":        "",
		"junk":         "not.a.value",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := sessions.OpenValue(edited)
			assert.Error(t, err, "an edited sealed value must prove nothing")
		})
	}
}

// TestThePurposeCannotBeConfusedWithThePayload pins the SEPARATOR, which no
// behavior observes today.
//
// The two purposes this key signs for are the same length, so dropping the
// separator between the purpose and the payload changes nothing any other test
// here can see — measured: that mutation leaves the suite green. What it would
// break is the next purpose, if it were a different length: without the
// separator, signing ("s", "1x") and ("s1", "x") are the same bytes, and the
// token minted for one purpose verifies for the other.
//
// So the property is asserted directly rather than through a behavior, because
// the behavior that would show it does not exist yet and the cost of it
// appearing is a forged token.
func TestThePurposeCannotBeConfusedWithThePayload(t *testing.T) {
	t.Parallel()

	sessions := newSessions(t, time.Now)

	assert.NotEqual(t,
		sessions.sign("s", "1x"),
		sessions.sign("s1", "x"),
		"a purpose and a payload that CONCATENATE to the same bytes must not sign "+
			"to the same MAC; the separator between them is the whole defense and "+
			"today's two purposes are the same length, so nothing else would notice "+
			"if it went")
}
