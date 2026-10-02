package identitysession

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// The rules of an address change (ADR 0377); the two statements are in the
// integration lane.

// movingAccounts is the shop's records: who holds which address, and the
// writes an address change made.
type movingAccounts struct {
	mu        sync.Mutex
	owners    map[string]string // address -> customer id
	changeErr error
}

func (a *movingAccounts) CustomerIDForEmail(_ context.Context, email string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	return a.owners[email], nil
}

func (a *movingAccounts) OpenAccount(context.Context, string) (string, error) {
	return "", errors.New("not needed")
}

func (a *movingAccounts) ChangeAccountEmail(_ context.Context, customerID, email string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.changeErr != nil {
		return a.changeErr
	}
	for address, owner := range a.owners {
		if owner == customerID {
			delete(a.owners, address)
		}
	}
	a.owners[email] = customerID

	return nil
}

// addressOf answers the address the shop's records hold for a customer.
func (a *movingAccounts) addressOf(customerID string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	for address, owner := range a.owners {
		if owner == customerID {
			return address
		}
	}

	return ""
}

// proofs records the address change links it was asked to carry.
type proofs struct {
	mu     sync.Mutex
	to     []string
	tokens []string
}

func (p *proofs) SendAddressProof(_ context.Context, email, token string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.to = append(p.to, email)
	p.tokens = append(p.tokens, token)

	return nil
}

// movingModule is a module that can move an account, over the anchored store
// holding one, and its routes.
func movingModule(t *testing.T) (*anchoredStore, *movingAccounts, *proofs, chi.Router) {
	t.Helper()

	hash, err := HashPassword("the password")
	require.NoError(t, err)
	store := &anchoredStore{customerID: testCustomerID, email: "known@example.test", hash: hash}
	accounts := &movingAccounts{owners: map[string]string{"known@example.test": testCustomerID}}
	sent := &proofs{}
	m := New(Options{
		Secret: testSecret, Credentials: store, Accounts: accounts, AddressProof: sent, Limiter: allowAll{},
	})
	require.NoError(t, m.Register(context.Background(), container.New(nil)))
	r := chi.NewRouter()
	m.Routes(r)

	return store, accounts, sent, r
}

// postJSON sends a JSON body, with a cookie when there is one.
func postJSON(t *testing.T, r chi.Router, path string, cookie *http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.Header.Set("Cookie", cookie.Name+"="+cookie.Value)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// TestAnAddressChangeMovesTheAccountOnceTheLinkIsFollowed: nothing moves until
// the link sent to the new address is followed; then the record and the
// credential both move, the link works once, and nobody is signed out.
func TestAnAddressChangeMovesTheAccountOnceTheLinkIsFollowed(t *testing.T) {
	t.Parallel()

	store, accounts, sent, r := movingModule(t)
	here := signInAs(t, r, "known@example.test", "the password")

	rec := postJSON(t, r, "/store/v1/auth/email", here,
		`{"new_email":" New@Example.test ","current_password":"the password"}`)
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	require.Equal(t, []string{"new@example.test"}, sent.to, "the link goes to the new address, folded")
	assert.Equal(t, "known@example.test", accounts.addressOf(testCustomerID), "nothing moved yet")
	assert.Equal(t, "known@example.test", store.email)

	confirmed := postJSON(t, r, "/store/v1/auth/email/confirm", nil, `{"token":"`+sent.tokens[0]+`"}`)
	require.Equal(t, http.StatusNoContent, confirmed.Code, confirmed.Body.String())
	assert.Equal(t, "new@example.test", accounts.addressOf(testCustomerID), "the record moved")
	assert.Equal(t, "new@example.test", store.email, "the credential moved")
	require.NoError(t, VerifyPassword(store.hash, "the password"), "the password did not change")
	assert.Empty(t, confirmed.Result().Cookies(), "nobody is signed in by the link")
	assert.Equal(t, http.StatusOK, get(t, r, sessionPath, here).Code, "nobody is signed out")
	signInAs(t, r, "new@example.test", "the password")

	again := postJSON(t, r, "/store/v1/auth/email/confirm", nil, `{"token":"`+sent.tokens[0]+`"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, again.Code)
	assert.Contains(t, again.Body.String(), CodeAddressChangeNotUsable)
}

// TestAnAddressAnotherAccountHoldsGetsNothing: the answer is the same and no
// link is sent, so the endpoint tells nobody which addresses have accounts.
func TestAnAddressAnotherAccountHoldsGetsNothing(t *testing.T) {
	t.Parallel()

	store, accounts, sent, r := movingModule(t)
	accounts.owners["taken@example.test"] = "cust_SOMEBODY_ELSE"
	here := signInAs(t, r, "known@example.test", "the password")

	taken := postJSON(t, r, "/store/v1/auth/email", here,
		`{"new_email":"taken@example.test","current_password":"the password"}`)
	free := postJSON(t, r, "/store/v1/auth/email", here,
		`{"new_email":"free@example.test","current_password":"the password"}`)
	assert.Equal(t, http.StatusAccepted, taken.Code, taken.Body.String())
	assert.Equal(t, taken.Body.String(), free.Body.String(), "the two answers cannot be told apart")
	assert.Equal(t, []string{"free@example.test"}, sent.to, "only the free address is mailed")
	pending := []string{}
	for _, email := range store.moving {
		pending = append(pending, email)
	}
	assert.Equal(t, []string{"free@example.test"}, pending, "only the free address waits for its link")
}

// TestAnAddressChangeAsksForTheCurrentPassword: a session alone does not move
// the account, nor does a body that is not a change.
func TestAnAddressChangeAsksForTheCurrentPassword(t *testing.T) {
	t.Parallel()

	_, _, sent, r := movingModule(t)
	here := signInAs(t, r, "known@example.test", "the password")

	for name, tc := range map[string]struct {
		cookie *http.Cookie
		body   string
		status int
		code   string
	}{
		"no session":           {nil, `{"new_email":"new@example.test","current_password":"the password"}`, http.StatusUnauthorized, CodeNoSession},
		"a wrong password":     {here, `{"new_email":"new@example.test","current_password":"a guess"}`, http.StatusForbidden, CodeCurrentPasswordWrong},
		"not an address":       {here, `{"new_email":"nobody","current_password":"the password"}`, http.StatusUnprocessableEntity, CodeAddressChangeInvalid},
		"the account's own":    {here, `{"new_email":"Known@Example.test","current_password":"the password"}`, http.StatusUnprocessableEntity, CodeAddressChangeInvalid},
		"a body it cannot use": {here, `{"email":"new@example.test"}`, http.StatusUnprocessableEntity, CodeInvalid},
	} {
		rec := postJSON(t, r, "/store/v1/auth/email", tc.cookie, tc.body)
		assert.Equal(t, tc.status, rec.Code, "%s: %s", name, rec.Body.String())
		assert.Contains(t, rec.Body.String(), tc.code, name)
	}
	assert.Empty(t, sent.to, "no link was sent")
}

// TestAnAddressTakenBeforeTheLinkIsFollowedMovesNothing: another account that
// took the address meanwhile is told apart before anything is written, and so
// is one that takes it between the check and the write.
func TestAnAddressTakenBeforeTheLinkIsFollowedMovesNothing(t *testing.T) {
	t.Parallel()

	for name, take := range map[string]func(*movingAccounts){
		"before the check": func(a *movingAccounts) { a.owners["new@example.test"] = "cust_SOMEBODY_ELSE" },
		"at the write": func(a *movingAccounts) {
			a.changeErr = coreerrors.Conflict("customer_email_taken", "another account holds it")
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store, accounts, sent, r := movingModule(t)
			here := signInAs(t, r, "known@example.test", "the password")
			require.Equal(t, http.StatusAccepted, postJSON(t, r, "/store/v1/auth/email", here,
				`{"new_email":"new@example.test","current_password":"the password"}`).Code)
			take(accounts)

			rec := postJSON(t, r, "/store/v1/auth/email/confirm", nil, `{"token":"`+sent.tokens[0]+`"}`)
			assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), CodeAddressTaken)
			assert.Equal(t, "known@example.test", store.email, "the credential did not move")
		})
	}
}

// TestTheRecordMovesBeforeTheCredential: a record that cannot be moved leaves
// the person signing in where they always did.
func TestTheRecordMovesBeforeTheCredential(t *testing.T) {
	t.Parallel()

	store, accounts, sent, r := movingModule(t)
	here := signInAs(t, r, "known@example.test", "the password")
	require.Equal(t, http.StatusAccepted, postJSON(t, r, "/store/v1/auth/email", here,
		`{"new_email":"new@example.test","current_password":"the password"}`).Code)
	accounts.changeErr = errors.New("the customer module is down")

	rec := postJSON(t, r, "/store/v1/auth/email/confirm", nil, `{"token":"`+sent.tokens[0]+`"}`)
	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	assert.Equal(t, "known@example.test", store.email)
	assert.Zero(t, store.puts)
}

// TestAddressChangeIsMountedOnlyWithItsSeams: no messenger, or an Accounts
// that cannot move a record, and the endpoints do not exist.
func TestAddressChangeIsMountedOnlyWithItsSeams(t *testing.T) {
	t.Parallel()

	hash, err := HashPassword("the password")
	require.NoError(t, err)
	for name, opts := range map[string]Options{
		"no messenger": {Accounts: &movingAccounts{owners: map[string]string{}}},
		"no move":      {Accounts: stillAccounts{}, AddressProof: &proofs{}},
	} {
		opts.Secret = testSecret
		opts.Credentials = &anchoredStore{customerID: testCustomerID, email: "known@example.test", hash: hash}
		m := New(opts)
		require.NoError(t, m.Register(context.Background(), container.New(nil)))
		r := chi.NewRouter()
		m.Routes(r)

		rec := postJSON(t, r, "/store/v1/auth/email/confirm", nil, `{"token":"x"}`)
		assert.Contains(t, []int{http.StatusNotFound, http.StatusMethodNotAllowed}, rec.Code, name)
	}
}

// stillAccounts is an Accounts that cannot move a record.
type stillAccounts struct{}

func (stillAccounts) CustomerIDForEmail(context.Context, string) (string, error) { return "", nil }
func (stillAccounts) OpenAccount(context.Context, string) (string, error)        { return "", nil }

// signInAs signs an address in and answers the cookie.
func signInAs(t *testing.T, r chi.Router, email, password string) *http.Cookie {
	t.Helper()

	rec := post(t, r, "/store/v1/auth/sign-in", `{"email":"`+email+`","password":"`+password+`"}`)
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())

	return sessionCookie(t, rec)
}
