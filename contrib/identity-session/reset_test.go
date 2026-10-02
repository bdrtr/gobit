package identitysession_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	identitysession "github.com/bdrtr/gobit/contrib/identity-session"
	"github.com/bdrtr/gobit/core/container"
)

// The tests in this file are about the RULES of the password reset (ADR 0373).
// What the two statements do against a real table — replacing a pending reset,
// a single-use token, the cascade an erasure relies on — is in the integration
// lane.

// memoryResets is a credential store that keeps pending resets.
type memoryResets struct {
	*memoryRegistrations
	mu      sync.Mutex
	pending map[string]string // token hash -> customer id
}

func newMemoryResets() *memoryResets {
	return &memoryResets{memoryRegistrations: newMemoryRegistrations(), pending: map[string]string{}}
}

// PutPasswordReset replaces the customer's pending reset.
func (s *memoryResets) PutPasswordReset(_ context.Context, tokenHash, customerID string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for hash, owner := range s.pending {
		if owner == customerID {
			delete(s.pending, hash)
		}
	}
	s.pending[tokenHash] = customerID

	return nil
}

// TakePasswordReset removes a pending reset and answers its customer and the
// address their credential signs in with.
func (s *memoryResets) TakePasswordReset(_ context.Context, tokenHash string) (customerID, email string, err error) {
	s.mu.Lock()
	customerID, ok := s.pending[tokenHash]
	delete(s.pending, tokenHash)
	s.mu.Unlock()
	if !ok {
		return "", "", identitysession.ErrNoPasswordReset
	}

	s.memoryRegistrations.mu.Lock()
	defer s.memoryRegistrations.mu.Unlock()
	for _, row := range s.credentials {
		if row.customerID == customerID {
			return customerID, row.email, nil
		}
	}

	return "", "", identitysession.ErrNoPasswordReset
}

// resetSender records the reset links it was asked to carry.
type resetSender struct {
	mu      sync.Mutex
	sent    []string
	tokens  []string
	sendErr error
}

func (s *resetSender) SendPasswordReset(_ context.Context, email, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sendErr != nil {
		return s.sendErr
	}
	s.sent = append(s.sent, email)
	s.tokens = append(s.tokens, token)

	return nil
}

func (s *resetSender) lastToken(t *testing.T) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	require.NotEmpty(t, s.tokens, "a reset link was sent")

	return s.tokens[len(s.tokens)-1]
}

// resetRouter mounts a module whose reset is wired to the store and sender.
func resetRouter(t *testing.T, store identitysession.Credentials, sender identitysession.PasswordReset) chi.Router {
	t.Helper()

	m := identitysession.New(identitysession.Options{
		Secret:        []byte("a password reset test signing secret 32!"),
		Insecure:      true,
		Credentials:   store,
		PasswordReset: sender,
		Limiter:       neverLimits{},
	})
	require.NoError(t, m.Register(t.Context(), container.New(nil)))
	r := chi.NewRouter()
	m.Routes(r)

	return r
}

// withAccount is a store holding one customer's credential.
func withAccount(t *testing.T, customerID, email, password string) *memoryResets {
	t.Helper()

	store := newMemoryResets()
	hash, err := identitysession.HashPassword(password)
	require.NoError(t, err)
	require.NoError(t, store.Put(t.Context(), customerID, email, hash))

	return store
}

// TestAResetIsAnsweredTheSameForAnybody: an address that signs in here and one
// that does not both get 202, and only the first gets mail — the endpoint is
// neither an oracle nor a way to mail strangers.
func TestAResetIsAnsweredTheSameForAnybody(t *testing.T) {
	t.Parallel()

	sender := &resetSender{}
	r := resetRouter(t, withAccount(t, "cust_ADA", "ada@example.test", "the old one"), sender)

	known := sendJSON(t, r, "/store/v1/auth/password-reset", `{"email":" Ada@Example.test "}`)
	unknown := sendJSON(t, r, "/store/v1/auth/password-reset", `{"email":"nobody@example.test"}`)

	assert.Equal(t, http.StatusAccepted, known.Code, known.Body.String())
	assert.Equal(t, http.StatusAccepted, unknown.Code, unknown.Body.String())
	assert.Equal(t, known.Body.String(), unknown.Body.String(), "the two answers cannot be told apart")
	assert.Equal(t, []string{"ada@example.test"}, sender.sent, "only the account's address is mailed")
}

// TestAResetLinkReplacesThePasswordOnce: the link sets the new password and
// signs the person in; the old password stops working; the same link works once.
func TestAResetLinkReplacesThePasswordOnce(t *testing.T) {
	t.Parallel()

	store := withAccount(t, "cust_ADA", "ada@example.test", "the old one")
	sender := &resetSender{}
	r := resetRouter(t, store, sender)
	require.Equal(t, http.StatusAccepted,
		sendJSON(t, r, "/store/v1/auth/password-reset", `{"email":"ada@example.test"}`).Code)
	token := sender.lastToken(t)

	confirmed := sendJSON(t, r, "/store/v1/auth/password-reset/confirm",
		`{"token":"`+token+`","password":"a brand new one"}`)
	require.Equal(t, http.StatusNoContent, confirmed.Code, confirmed.Body.String())
	assert.NotEmpty(t, confirmed.Result().Cookies(), "the person is signed in")

	_, hash, err := store.Credential(t.Context(), "ada@example.test")
	require.NoError(t, err)
	require.NoError(t, identitysession.VerifyPassword(hash, "a brand new one"))
	require.Error(t, identitysession.VerifyPassword(hash, "the old one"), "the old password is gone")

	again := sendJSON(t, r, "/store/v1/auth/password-reset/confirm",
		`{"token":"`+token+`","password":"a third one"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, again.Code)
	assert.Contains(t, again.Body.String(), identitysession.CodePasswordResetNotUsable)
}

// TestOnlyTheNewestResetLinkWorks: asking again replaces the pending reset.
func TestOnlyTheNewestResetLinkWorks(t *testing.T) {
	t.Parallel()

	sender := &resetSender{}
	r := resetRouter(t, withAccount(t, "cust_ADA", "ada@example.test", "the old one"), sender)
	sendJSON(t, r, "/store/v1/auth/password-reset", `{"email":"ada@example.test"}`)
	first := sender.lastToken(t)
	sendJSON(t, r, "/store/v1/auth/password-reset", `{"email":"ada@example.test"}`)
	second := sender.lastToken(t)

	stale := sendJSON(t, r, "/store/v1/auth/password-reset/confirm", `{"token":"`+first+`","password":"x-new"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, stale.Code)
	fresh := sendJSON(t, r, "/store/v1/auth/password-reset/confirm", `{"token":"`+second+`","password":"x-new"}`)
	assert.Equal(t, http.StatusNoContent, fresh.Code, fresh.Body.String())
}

// TestAPasswordTheHashRefusesDoesNotSpendTheLink: the password is checked
// before the token is taken.
func TestAPasswordTheHashRefusesDoesNotSpendTheLink(t *testing.T) {
	t.Parallel()

	sender := &resetSender{}
	r := resetRouter(t, withAccount(t, "cust_ADA", "ada@example.test", "the old one"), sender)
	sendJSON(t, r, "/store/v1/auth/password-reset", `{"email":"ada@example.test"}`)
	token := sender.lastToken(t)

	empty := sendJSON(t, r, "/store/v1/auth/password-reset/confirm", `{"token":"`+token+`","password":""}`)
	assert.Equal(t, http.StatusUnprocessableEntity, empty.Code)
	assert.Contains(t, empty.Body.String(), identitysession.CodePasswordResetInvalid)
	ok := sendJSON(t, r, "/store/v1/auth/password-reset/confirm", `{"token":"`+token+`","password":"now a real one"}`)
	assert.Equal(t, http.StatusNoContent, ok.Code, "the link survived the refused password: %s", ok.Body.String())
}

// TestAResetThatCannotBeSentIsAFailure: nothing pretends the link went out.
func TestAResetThatCannotBeSentIsAFailure(t *testing.T) {
	t.Parallel()

	sender := &resetSender{sendErr: errors.New("the mail server is down")}
	r := resetRouter(t, withAccount(t, "cust_ADA", "ada@example.test", "the old one"), sender)

	rec := sendJSON(t, r, "/store/v1/auth/password-reset", `{"email":"ada@example.test"}`)
	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), identitysession.CodeUnavailable)
}

// TestThePasswordResetIsMountedOnlyWithItsSeams: no sender, or a store that
// keeps no pending reset, and the endpoints do not exist.
func TestThePasswordResetIsMountedOnlyWithItsSeams(t *testing.T) {
	t.Parallel()

	for name, r := range map[string]chi.Router{
		"no sender":                    resetRouter(t, newMemoryResets(), nil),
		"a store that keeps no resets": resetRouter(t, newMemoryRegistrations(), &resetSender{}),
	} {
		rec := sendJSON(t, r, "/store/v1/auth/password-reset", `{"email":"ada@example.test"}`)
		assert.Contains(t, []int{http.StatusNotFound, http.StatusMethodNotAllowed}, rec.Code, name)
	}
}
