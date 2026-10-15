package identitysession

import (
	"context"
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
)

// TestAnUnknownAddressIsCheckedLikeAKnownOne: a sign-in for an address with
// no account runs the same password check a known address does, against the
// decoy, so the two answer in one time (ADR 0442, D296). Measured before: a
// wrong password on a known address took 13 ms, an unknown address 0.4 ms.
func TestAnUnknownAddressIsCheckedLikeAKnownOne(t *testing.T) {
	_, _, _, r := anchoredModule(t)
	var checked []string
	original := verifyPassword
	verifyPassword = func(stored, password string) error {
		checked = append(checked, stored)

		return original(stored, password)
	}
	t.Cleanup(func() { verifyPassword = original })

	for _, email := range []string{"nobody@example.test", "known@example.test"} {
		checked = nil
		rec := post(t, r, "/store/v1/auth/sign-in", `{"email":"`+email+`","password":"wrong"}`)
		require.Equal(t, http.StatusUnauthorized, rec.Code, email)
		require.Len(t, checked, 1, "%s: one argon2id check", email)
		assert.True(t, strings.HasPrefix(checked[0], "$argon2id$"), "%s: checked against an argon2id hash", email)
	}
}

func TestTheDecoyIsAHashNoPasswordOpens(t *testing.T) {
	a, b := New(Options{}), New(Options{})
	require.True(t, strings.HasPrefix(a.decoy, "$argon2id$"))
	assert.NotEqual(t, a.decoy, b.decoy, "drawn per module, not a constant anyone can look up")
	assert.ErrorIs(t, VerifyPassword(a.decoy, ""), ErrPasswordMismatch)
}

// TestTheDecoyCostsWhatAStoredHashCosts: the decoy's argon2id parameters are
// the ones HashPassword writes, so checking it takes a stored hash's time; a
// cheaper decoy would answer early again (ADR 0442).
func TestTheDecoyCostsWhatAStoredHashCosts(t *testing.T) {
	stored, err := HashPassword("anything")
	require.NoError(t, err)
	params := func(h string) string {
		fields := strings.Split(h, "$")
		require.Len(t, fields, 6, h)

		return fields[2] + "$" + fields[3]
	}
	assert.Equal(t, params(stored), params(New(Options{}).decoy))
}

// TestAStoreThatCannotSwapIsSaidSo: a store bound through Options.Credentials
// that writes unconditionally gets an error, not an unconditional write, and a
// module not yet registered says that rather than pretending the store
// cannot swap.
func TestAStoreThatCannotSwapIsSaidSo(t *testing.T) {
	store, _, _, _ := anchoredModule(t)
	expected := CredentialValue{Email: "known@example.test", PasswordHash: store.hash}
	replacement := CredentialValue{Email: "known@example.test", PasswordHash: "$argon2id$x"}

	m := New(Options{Secret: testSecret, Credentials: store})
	ok, err := m.ReplaceCredential(context.Background(), testCustomerID, expected, replacement)
	assert.False(t, ok)
	assert.ErrorIs(t, err, ErrNotRegistered)

	require.NoError(t, m.Register(context.Background(), container.New(nil)))
	ok, err = m.ReplaceCredential(context.Background(), testCustomerID, expected, replacement)
	assert.False(t, ok)
	assert.True(t, errors.Is(err, ErrSwapUnsupported))
}

// TestASignInIsDatedAtItsRead: a password replaced the moment after a
// sign-in read the old one moves the anchor past the read, and the session
// that sign-in issues proves nobody (ADR 0443). Dated at its issue, the
// cookie outlived the replacement.
func TestASignInIsDatedAtItsRead(t *testing.T) {
	store, clock, _, r := anchoredModule(t)
	store.afterRead = func() {
		clock.mu.Lock()
		clock.at = clock.at.Add(time.Second)
		replaced := clock.at
		clock.mu.Unlock()
		store.mu.Lock()
		store.anchor, store.afterRead = replaced, nil
		store.mu.Unlock()
	}
	cookie := signIn(t, r, "the old password")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/store/v1/auth/session", http.NoBody)
	req.Header.Set("Cookie", cookie.Name+"="+cookie.Value)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "the replacement ended the session its read issued")
}

// atomicStore is the anchored fake that also writes the password and the
// anchor in one call, and counts how each was written.
type atomicStore struct {
	*anchoredStore
	atomic int
}

func (s *atomicStore) PutEndingSessions(ctx context.Context, customerID, email, passwordHash string, moment time.Time) error {
	s.atomic++
	if err := s.EndSessionsBefore(ctx, customerID, moment); err != nil && !errors.Is(err, ErrNoCredential) {
		return err
	}

	return s.Put(ctx, customerID, email, passwordHash)
}

// TestTheResetAndTheOperatorTakeTheOneWrite: the reset and the operator's
// route write the password and the anchor in the store's one call where it
// has it (ADR 0443); the password change shares their helper.
func TestTheResetAndTheOperatorTakeTheOneWrite(t *testing.T) {
	inner, clock, _, _ := anchoredModule(t)
	store := &atomicStore{anchoredStore: inner}
	link := &lastLink{}
	m := New(Options{Secret: testSecret, Credentials: store, PasswordReset: link, Limiter: allowAll{}})
	require.NoError(t, m.Register(context.Background(), container.New(nil)))
	m.sessions.now = clock.now
	r := chi.NewRouter()
	m.Routes(r)

	require.Equal(t, http.StatusAccepted, post(t, r, "/store/v1/auth/password-reset", `{"email":"known@example.test"}`).Code)
	rec := post(t, r, "/store/v1/auth/password-reset/confirm", `{"token":"`+link.wait(t)+`","password":"the new password"}`)
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	assert.Equal(t, 1, store.atomic, "the reset")

	rec = putCredentialAs(t, r, credentialWriter, testCustomerID, `{"email":"known@example.test","password":"set by support"}`)
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	assert.Equal(t, 2, store.atomic, "the operator's route")
}

// TestASessionIsNeverDatedAhead: a read moment in the future dates the session
// now, so a replacement made between now and that moment still ends it.
func TestASessionIsNeverDatedAhead(t *testing.T) {
	store, clock, _, _ := anchoredModule(t)
	m := New(Options{Secret: testSecret, Credentials: store})
	require.NoError(t, m.Register(context.Background(), container.New(nil)))
	m.sessions.now = clock.now
	rec := httptest.NewRecorder()
	m.sessions.IssueAsOf(rec, testCustomerID, clock.now().Add(time.Hour))
	cookie := sessionCookie(t, rec)

	clock.advance(time.Minute)
	store.mu.Lock()
	store.anchor = clock.now()
	store.mu.Unlock()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	req.Header.Set("Cookie", cookie.Name+"="+cookie.Value)
	_, err := m.sessions.CustomerID(req)
	assert.Error(t, err, "the replacement a minute later ends a session dated now")
}

// blockingLink holds every reset mail until the test releases it.
type blockingLink struct {
	release chan struct{}
	sent    chan string
}

func (b *blockingLink) SendPasswordReset(_ context.Context, _, token string) error {
	<-b.release
	b.sent <- token

	return nil
}

// TestAResetIsAnsweredBeforeItsMailAndDrainedAtShutdown: the reset request
// answers while its mail is still held (ADR 0442), and the module's shutdown
// waits for that mail within its budget.
func TestAResetIsAnsweredBeforeItsMailAndDrainedAtShutdown(t *testing.T) {
	store, _, _, _ := anchoredModule(t)
	link := &blockingLink{release: make(chan struct{}), sent: make(chan string, 1)}
	m := New(Options{Secret: testSecret, Credentials: store, PasswordReset: link, Limiter: allowAll{}})
	c := container.New(nil)
	require.NoError(t, m.Register(context.Background(), c))
	require.True(t, c.Has(ModuleName+".after_answer"))
	r := chi.NewRouter()
	m.Routes(r)

	answered := make(chan int, 1)
	go func() {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/store/v1/auth/password-reset",
			strings.NewReader(`{"email":"known@example.test"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		answered <- rec.Code
	}()
	select {
	case code := <-answered:
		assert.Equal(t, http.StatusAccepted, code)
	case <-time.After(5 * time.Second):
		require.Fail(t, "the answer waited for the mail")
	}

	short, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	assert.Error(t, afterAnswerDrain{m: m}.Shutdown(short), "the held mail is still running")
	close(link.release)
	<-link.sent
	assert.NoError(t, c.Shutdown(context.Background()), "the drain finds nothing left")
}
