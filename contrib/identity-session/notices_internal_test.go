package identitysession

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
)

// The rules of the account notices (ADR 0379).

// recordingNotices records the notices it was asked to send. Given a context
// already canceled it sends nothing, as a mailer that honors its context does.
type recordingNotices struct {
	mu   sync.Mutex
	sent []string
	err  error
}

func (n *recordingNotices) SendPasswordChanged(ctx context.Context, email string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	n.sent = append(n.sent, "password "+email)

	return n.err
}

func (n *recordingNotices) SendAddressChanged(ctx context.Context, oldEmail, newEmail string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	n.sent = append(n.sent, "address "+oldEmail+" -> "+newEmail)

	return n.err
}

// noticedModule is a module over one account that resets, changes and moves
// it, telling the notices given and logging to log when there is one.
func noticedModule(t *testing.T, notices AccountNotices, log *slog.Logger) (*anchoredStore, *lastLink, *proofs, chi.Router) {
	t.Helper()

	hash, err := HashPassword("the old password")
	require.NoError(t, err)
	store := &anchoredStore{customerID: testCustomerID, email: "known@example.test", hash: hash}
	link, sent := &lastLink{}, &proofs{}
	m := New(Options{
		Secret: testSecret, Credentials: store, Limiter: allowAll{},
		PasswordReset: link, AddressProof: sent, AccountNotices: notices, Logger: log,
		Accounts: &movingAccounts{owners: map[string]string{"known@example.test": testCustomerID}},
	})
	require.NoError(t, m.Register(context.Background(), container.New(nil)))
	m.sessions.now = (&steppingClock{at: time.Date(2026, time.October, 3, 9, 0, 0, 0, time.UTC)}).now
	m.noticeTimeout = time.Second
	r := chi.NewRouter()
	m.Routes(r)

	return store, link, sent, r
}

// TestAnAccountIsToldWhatChanged: a reset and a change tell the account's
// address that its password was replaced, and a move tells the address it
// left; a refused change tells nobody.
func TestAnAccountIsToldWhatChanged(t *testing.T) {
	t.Parallel()

	notices := &recordingNotices{}
	_, link, sent, r := noticedModule(t, notices, nil)

	require.Equal(t, http.StatusAccepted,
		post(t, r, "/store/v1/auth/password-reset", `{"email":"known@example.test"}`).Code)
	rec := post(t, r, "/store/v1/auth/password-reset/confirm", `{"token":"`+link.token+`","password":"a second one"}`)
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	here := sessionCookie(t, rec)

	refused := postJSON(t, r, "/store/v1/auth/password", here, `{"current_password":"a guess","new_password":"x"}`)
	require.Equal(t, http.StatusForbidden, refused.Code)
	changed := postJSON(t, r, "/store/v1/auth/password", here,
		`{"current_password":"a second one","new_password":"a third one"}`)
	require.Equal(t, http.StatusNoContent, changed.Code, changed.Body.String())
	here = sessionCookie(t, changed)

	require.Equal(t, http.StatusAccepted, postJSON(t, r, "/store/v1/auth/email", here,
		`{"new_email":"new@example.test","current_password":"a third one"}`).Code)
	moved := postJSON(t, r, "/store/v1/auth/email/confirm", nil, `{"token":"`+sent.tokens[0]+`"}`)
	require.Equal(t, http.StatusNoContent, moved.Code, moved.Body.String())

	assert.Equal(t, []string{
		"password known@example.test",
		"password known@example.test",
		"address known@example.test -> new@example.test",
	}, notices.sent, "the reset, the change and the move, and not the refused change")
}

// TestAChangeThatIsNotWrittenTellsNobody: a notice says a change happened, so
// a reset, a change or a move whose write fails tells nobody.
func TestAChangeThatIsNotWrittenTellsNobody(t *testing.T) {
	t.Parallel()

	notices := &recordingNotices{}
	store, link, sent, r := noticedModule(t, notices, nil)
	here := signInAs(t, r, "known@example.test", "the old password")
	require.Equal(t, http.StatusAccepted, postJSON(t, r, "/store/v1/auth/email", here,
		`{"new_email":"new@example.test","current_password":"the old password"}`).Code)
	require.Equal(t, http.StatusAccepted,
		post(t, r, "/store/v1/auth/password-reset", `{"email":"known@example.test"}`).Code)
	store.putErr = errors.New("the database is down")

	reset := post(t, r, "/store/v1/auth/password-reset/confirm", `{"token":"`+link.token+`","password":"a second one"}`)
	require.Equal(t, http.StatusInternalServerError, reset.Code, reset.Body.String())
	changed := postJSON(t, r, "/store/v1/auth/password", signInAs(t, r, "known@example.test", "the old password"),
		`{"current_password":"the old password","new_password":"a second one"}`)
	require.Equal(t, http.StatusInternalServerError, changed.Code, changed.Body.String())
	moved := postJSON(t, r, "/store/v1/auth/email/confirm", nil, `{"token":"`+sent.tokens[0]+`"}`)
	require.Equal(t, http.StatusInternalServerError, moved.Code, moved.Body.String())

	assert.Empty(t, notices.sent)
}

// TestANoticeThatCannotBeSentFailsNothing: the change has happened, so a
// notice that fails is logged and the person is answered as if it went.
func TestANoticeThatCannotBeSentFailsNothing(t *testing.T) {
	t.Parallel()

	var logged bytes.Buffer
	notices := &recordingNotices{err: errors.New("the mail server is down")}
	store, link, sent, r := noticedModule(t, notices, slog.New(slog.NewTextHandler(&logged, nil)))

	require.Equal(t, http.StatusAccepted,
		post(t, r, "/store/v1/auth/password-reset", `{"email":"known@example.test"}`).Code)
	rec := post(t, r, "/store/v1/auth/password-reset/confirm", `{"token":"`+link.token+`","password":"a second one"}`)
	assert.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	require.NoError(t, VerifyPassword(store.hash, "a second one"))

	require.Equal(t, http.StatusAccepted, postJSON(t, r, "/store/v1/auth/email", sessionCookie(t, rec),
		`{"new_email":"new@example.test","current_password":"a second one"}`).Code)
	moved := postJSON(t, r, "/store/v1/auth/email/confirm", nil, `{"token":"`+sent.tokens[0]+`"}`)
	assert.Equal(t, http.StatusNoContent, moved.Code, moved.Body.String())
	assert.Equal(t, "new@example.test", store.email)

	assert.Len(t, notices.sent, 2, "both notices were tried")
	assert.Contains(t, logged.String(), "could not tell an account its password changed")
	assert.Contains(t, logged.String(), "could not tell an account's old address it moved")
}

// TestANoticeOutlivesTheCallerHangingUp: a caller that hangs up the moment the
// change is written does not cancel the notice, which is for the account's
// owner and not for whoever holds the connection (D231).
func TestANoticeOutlivesTheCallerHangingUp(t *testing.T) {
	t.Parallel()

	notices := &recordingNotices{}
	store, link, sent, r := noticedModule(t, notices, nil)

	require.Equal(t, http.StatusAccepted,
		post(t, r, "/store/v1/auth/password-reset", `{"email":"known@example.test"}`).Code)
	reset := postHangingUp(t, store, r, "/store/v1/auth/password-reset/confirm", nil,
		`{"token":"`+link.token+`","password":"a second one"}`)
	require.Equal(t, http.StatusNoContent, reset.Code, reset.Body.String())
	here := sessionCookie(t, reset)

	changed := postHangingUp(t, store, r, "/store/v1/auth/password", here,
		`{"current_password":"a second one","new_password":"a third one"}`)
	require.Equal(t, http.StatusNoContent, changed.Code, changed.Body.String())

	require.Equal(t, http.StatusAccepted, postJSON(t, r, "/store/v1/auth/email", sessionCookie(t, changed),
		`{"new_email":"new@example.test","current_password":"a third one"}`).Code)
	moved := postHangingUp(t, store, r, "/store/v1/auth/email/confirm", nil, `{"token":"`+sent.tokens[0]+`"}`)
	require.Equal(t, http.StatusNoContent, moved.Code, moved.Body.String())

	assert.Equal(t, []string{
		"password known@example.test",
		"password known@example.test",
		"address known@example.test -> new@example.test",
	}, notices.sent)
}

// postHangingUp sends body as [postJSON] does, on a context canceled the
// moment the store writes.
func postHangingUp(
	t *testing.T, store *anchoredStore, r chi.Router, path string, cookie *http.Cookie, body string,
) *httptest.ResponseRecorder {
	t.Helper()

	ctx, hangUp := context.WithCancel(t.Context())
	defer hangUp()
	store.mu.Lock()
	store.afterPut = hangUp
	store.mu.Unlock()
	defer func() {
		store.mu.Lock()
		store.afterPut = nil
		store.mu.Unlock()
	}()

	req := httptest.NewRequestWithContext(ctx, http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.Header.Set("Cookie", cookie.Name+"="+cookie.Value)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// stallingNotices waits for its context to end, as a mailer whose server never
// answers.
type stallingNotices struct{}

func (stallingNotices) SendPasswordChanged(ctx context.Context, _ string) error {
	<-ctx.Done()

	return ctx.Err()
}

func (stallingNotices) SendAddressChanged(ctx context.Context, _, _ string) error {
	<-ctx.Done()

	return ctx.Err()
}

// TestAStalledNoticeDoesNotHoldTheRequest: a notice that never returns is
// ended by its own bound, and the person is answered (D231).
func TestAStalledNoticeDoesNotHoldTheRequest(t *testing.T) {
	t.Parallel()

	_, link, _, r := noticedModule(t, stallingNotices{}, nil)
	require.Equal(t, http.StatusAccepted,
		post(t, r, "/store/v1/auth/password-reset", `{"email":"known@example.test"}`).Code)

	answered := make(chan int, 1)
	go func() {
		answered <- post(t, r, "/store/v1/auth/password-reset/confirm",
			`{"token":"`+link.token+`","password":"a second one"}`).Code
	}()
	select {
	case code := <-answered:
		assert.Equal(t, http.StatusNoContent, code)
	case <-time.After(10 * time.Second):
		t.Fatal("the request was still waiting on the notice after ten seconds")
	}
}

// TestNoticesHoldingANilPointerAreNone: an AccountNotices holding a nil
// pointer is not there, so a change that would call it does not panic after
// the write.
func TestNoticesHoldingANilPointerAreNone(t *testing.T) {
	t.Parallel()

	store, link, _, r := noticedModule(t, (*recordingNotices)(nil), nil)
	require.Equal(t, http.StatusAccepted,
		post(t, r, "/store/v1/auth/password-reset", `{"email":"known@example.test"}`).Code)
	require.NotPanics(t, func() {
		rec := post(t, r, "/store/v1/auth/password-reset/confirm",
			`{"token":"`+link.token+`","password":"a second one"}`)
		assert.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	})
	require.NoError(t, VerifyPassword(store.hash, "a second one"))
}

// TestNoNoticesIsAnOption: without notices bound, the changes work as they
// did.
func TestNoNoticesIsAnOption(t *testing.T) {
	t.Parallel()

	store, link, _, r := noticedModule(t, nil, nil)
	require.Equal(t, http.StatusAccepted,
		post(t, r, "/store/v1/auth/password-reset", `{"email":"known@example.test"}`).Code)
	rec := post(t, r, "/store/v1/auth/password-reset/confirm", `{"token":"`+link.token+`","password":"a second one"}`)
	assert.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	require.NoError(t, VerifyPassword(store.hash, "a second one"))
}
