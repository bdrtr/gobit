package identitysession_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	identitysession "github.com/bdrtr/gobit/contrib/identity-session"
	"github.com/bdrtr/gobit/core/container"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// The tests in this file are about the RULES of self-registration.
//
// What the two statements do against a real table is in the integration lane:
// that asking again replaces the pending row, and that a token is single-use
// because deleting and reading are one statement. What is here is what no store
// can decide — whether the endpoint is an oracle, what exists before the address
// is proven, and whether the flow is mounted at all.

// regHarness is a module with self-registration wired to fakes.
type regHarness struct {
	module   *identitysession.Module
	router   chi.Router
	accounts *fakeAccounts
	sender   *fakeSender
	store    *memoryRegistrations
}

// newRegHarness binds all three seams.
func newRegHarness(t *testing.T) *regHarness {
	t.Helper()

	accounts := &fakeAccounts{byEmail: map[string]string{}}
	sender := &fakeSender{}
	store := newMemoryRegistrations()

	return buildRegHarness(t, accounts, sender, store)
}

// buildRegHarness is [newRegHarness] with every seam spelled out.
func buildRegHarness(
	t *testing.T,
	accounts *fakeAccounts, sender *fakeSender, store identitysession.Credentials,
) *regHarness {
	t.Helper()

	m := identitysession.New(identitysession.Options{
		Secret:       []byte("a registration test signing secret of 32!"),
		Insecure:     true,
		Credentials:  store,
		Accounts:     accounts,
		Verification: sender,
		// The rule tests are not about the bound, and the DEFAULT bound is real:
		// the first version of the malformed-address table sent six requests and
		// the sixth came back 429, which read as the address check failing. The
		// test that IS about the limit binds nothing and gets the default.
		Limiter: neverLimits{},
	})
	require.NoError(t, m.Register(t.Context(), container.New(nil)))

	r := chi.NewRouter()
	m.Routes(r)

	memory, _ := store.(*memoryRegistrations)

	return &regHarness{module: m, router: r, accounts: accounts, sender: sender, store: memory}
}

// post sends a JSON body.
func (h *regHarness) post(t *testing.T, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	return sendJSON(t, h.router, path, body)
}

// TestARegistrationCreatesNOTHINGAboutThePersonYet is the flow's first promise.
//
// An address typed into a form is a claim. If a customer record appeared here,
// anybody could fill the shop's customer table with addresses that are not
// theirs — and every one of those records would look like a person who shops
// here to every report that counts customers.
func TestARegistrationCreatesNOTHINGAboutThePersonYet(t *testing.T) {
	t.Parallel()

	h := newRegHarness(t)

	answer := h.post(t, "/store/v1/auth/register",
		`{"email":"new@example.test","password":"a long enough password"}`)

	require.Equal(t, http.StatusAccepted, answer.Code, "body: %s", answer.Body.String())
	assert.Empty(t, h.accounts.opened, "no customer was opened")
	assert.Empty(t, h.store.credentials, "and no credential was written")
	assert.Len(t, h.store.pending, 1, "one pending row, in this module's own table")
	assert.Empty(t, answer.Header().Values("Set-Cookie"), "and nobody was signed in")
}

// TestTheSameAnswerForAnAddressThatHasAnAccount is the oracle this endpoint
// refuses to be.
//
// A different status, a different body or a different code would answer, for any
// address a caller cares to try, whether that person shops here. That question is
// interesting to their neighbor, their employer, and whoever bought a list of
// addresses.
func TestTheSameAnswerForAnAddressThatHasAnAccount(t *testing.T) {
	t.Parallel()

	h := newRegHarness(t)
	h.accounts.byEmail["taken@example.test"] = "cust_06G8ALREADYTHERE000000"

	fresh := h.post(t, "/store/v1/auth/register",
		`{"email":"new@example.test","password":"a long enough password"}`)
	taken := h.post(t, "/store/v1/auth/register",
		`{"email":"taken@example.test","password":"a long enough password"}`)

	assert.Equal(t, fresh.Code, taken.Code, "the status must not tell them apart")
	assert.Equal(t, fresh.Body.String(), taken.Body.String(),
		"and neither must the body")

	// What DOES differ is the message, so somebody who forgot they had an account
	// is not left staring at a form that appeared to work.
	assert.Equal(t, []string{"new@example.test"}, h.sender.verified)
	assert.Equal(t, []string{"taken@example.test"}, h.sender.alreadyRegistered)
	assert.Len(t, h.store.pending, 1, "and no pending row for the address that has one")
}

// TestAProvenAddressOpensAnAccountAndSignsIn is the ordinary path end to end.
func TestAProvenAddressOpensAnAccountAndSignsIn(t *testing.T) {
	t.Parallel()

	h := newRegHarness(t)

	require.Equal(t, http.StatusAccepted, h.post(t, "/store/v1/auth/register",
		`{"email":"proven@example.test","password":"a long enough password"}`).Code)
	require.Len(t, h.sender.tokens, 1, "the token went to the address and nowhere else")

	done := h.post(t, "/store/v1/auth/register/verify",
		`{"token":"`+h.sender.tokens[0]+`"}`)

	require.Equal(t, http.StatusNoContent, done.Code, "body: %s", done.Body.String())
	assert.Equal(t, []string{"proven@example.test"}, h.accounts.opened)
	assert.Len(t, h.store.credentials, 1, "the credential is written")
	assert.NotEmpty(t, done.Header().Values("Set-Cookie"),
		"and they are signed in: they just proved they control the address")
	assert.Empty(t, h.store.pending, "the pending row is gone")
}

// TestATokenWorksONCE is what makes the link safe to leave in a mailbox.
func TestATokenWorksONCE(t *testing.T) {
	t.Parallel()

	h := newRegHarness(t)
	require.Equal(t, http.StatusAccepted, h.post(t, "/store/v1/auth/register",
		`{"email":"once@example.test","password":"a long enough password"}`).Code)
	token := h.sender.tokens[0]

	first := h.post(t, "/store/v1/auth/register/verify", `{"token":"`+token+`"}`)
	second := h.post(t, "/store/v1/auth/register/verify", `{"token":"`+token+`"}`)

	require.Equal(t, http.StatusNoContent, first.Code)
	require.Equal(t, http.StatusUnprocessableEntity, second.Code,
		"body: %s", second.Body.String())
	assert.Contains(t, second.Body.String(), identitysession.CodeRegistrationNotUsable)
	assert.Len(t, h.accounts.opened, 1, "and the second attempt opened nothing")
}

// TestOneAnswerForEveryTokenThatIsNotUsable keeps the three kinds of no apart
// from the caller.
func TestOneAnswerForEveryTokenThatIsNotUsable(t *testing.T) {
	t.Parallel()

	h := newRegHarness(t)
	h.store.expired = true
	require.Equal(t, http.StatusAccepted, h.post(t, "/store/v1/auth/register",
		`{"email":"expired@example.test","password":"a long enough password"}`).Code)

	expired := h.post(t, "/store/v1/auth/register/verify",
		`{"token":"`+h.sender.tokens[0]+`"}`)
	unknown := h.post(t, "/store/v1/auth/register/verify",
		`{"token":"a token nobody ever minted"}`)

	require.Equal(t, http.StatusUnprocessableEntity, expired.Code)
	assert.Equal(t, expired.Body.String(), unknown.Body.String(),
		"an expired token and one that never existed are the same answer; telling them "+
			"apart says, for any token somebody tries, whether it was ever real")
}

// TestAnAddressThatGainedACustomerInBetweenKeepsIt is the window the flow leaves
// open on purpose.
//
// Registering and clicking the link are two requests with a person's mail client
// in between, and a guest checkout can happen in that gap. The verification must
// attach to the record that now exists rather than opening a second one.
func TestAnAddressThatGainedACustomerInBetweenKeepsIt(t *testing.T) {
	t.Parallel()

	h := newRegHarness(t)
	require.Equal(t, http.StatusAccepted, h.post(t, "/store/v1/auth/register",
		`{"email":"between@example.test","password":"a long enough password"}`).Code)

	// The guest checkout, happening while the message sits in a mailbox.
	h.accounts.byEmail["between@example.test"] = "cust_06G8GUESTINBETWEEN0000"

	done := h.post(t, "/store/v1/auth/register/verify",
		`{"token":"`+h.sender.tokens[0]+`"}`)

	require.Equal(t, http.StatusNoContent, done.Code, "body: %s", done.Body.String())
	assert.Empty(t, h.accounts.opened, "no second record was opened")
	require.Len(t, h.store.credentials, 1)
	assert.Equal(t, "cust_06G8GUESTINBETWEEN0000", h.store.credentials[0].customerID,
		"the credential belongs to the record that already existed")
}

// TestAnAddressThatCannotBeAnAddressIsRefused holds the minimal check.
func TestAnAddressThatCannotBeAnAddressIsRefused(t *testing.T) {
	t.Parallel()

	h := newRegHarness(t)

	for _, body := range []string{
		`{"email":"","password":"a long enough password"}`,
		`{"email":"nobody","password":"a long enough password"}`,
		`{"email":"two@@example.test","password":"a long enough password"}`,
		`{"email":"@example.test","password":"a long enough password"}`,
		// A comma with exactly ONE @, so this case isolates the character check.
		// The first version used "someone@example.test, other@example.test", which
		// has TWO @ and was refused by the other rule — a fixture that cannot tell
		// the rules apart, found by a mutation that survived.
		`{"email":"some,one@example.test","password":"a long password"}`,
		`{"email":"someone@example.test\nBcc: other@example.test","password":"a long pass"}`,
		`{"email":"someone@example.test","password":""}`,
	} {
		answer := h.post(t, "/store/v1/auth/register", body)

		require.Equal(t, http.StatusUnprocessableEntity, answer.Code, "for %s", body)
		assert.Contains(t, answer.Body.String(), identitysession.CodeRegistrationInvalid)
	}

	assert.Empty(t, h.sender.verified, "and nothing was sent anywhere")
	assert.Empty(t, h.store.pending)
}

// TestTheEndpointsDoNotEXISTWithoutTheSeams is the unmounted state.
//
// Not a 500, not a 501: they are absent. An endpoint that takes a password and
// can never finish has taken something and given nothing back.
//
// The typed-nil case is in the table because a test built it by ACCIDENT and
// found a real hole: an interface holding a nil pointer is not nil, so
// `Verification: shop.Mailer()` returning a typed nil when unconfigured used to
// mount these endpoints and panic on the first registration.
func TestTheEndpointsDoNotEXISTWithoutTheSeams(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		accounts     identitysession.Accounts
		verification identitysession.Verification
	}{
		{name: "nothing bound"},
		{name: "no way to open an account", verification: &fakeSender{}},
		{name: "no way to send the proof", accounts: &fakeAccounts{byEmail: map[string]string{}}},
		{
			name:         "a TYPED nil for the sender",
			accounts:     &fakeAccounts{byEmail: map[string]string{}},
			verification: (*fakeSender)(nil),
		},
		{
			name:         "a TYPED nil for the accounts",
			accounts:     (*fakeAccounts)(nil),
			verification: &fakeSender{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := identitysession.New(identitysession.Options{
				Secret:       []byte("a registration test signing secret of 32!"),
				Insecure:     true,
				Credentials:  newMemoryRegistrations(),
				Accounts:     tc.accounts,
				Verification: tc.verification,
			})
			require.NoError(t, m.Register(t.Context(), container.New(nil)))

			r := chi.NewRouter()
			m.Routes(r)

			answer := sendJSON(t, r, "/store/v1/auth/register",
				`{"email":"nobody@example.test","password":"a long enough password"}`)
			assert.Equal(t, http.StatusNotFound, answer.Code,
				"the endpoint must not exist: body %s", answer.Body.String())

			// And signing in still works, because the rest of the module is
			// unaffected by a seam nobody bound.
			signIn := sendJSON(t, r, "/store/v1/auth/sign-in",
				`{"email":"nobody@example.test","password":"whatever"}`)
			assert.NotEqual(t, http.StatusNotFound, signIn.Code)
		})
	}
}

// TestAStoreThatCannotHoldAPendingRowLeavesItUnmounted is the third seam.
func TestAStoreThatCannotHoldAPendingRowLeavesItUnmounted(t *testing.T) {
	t.Parallel()

	// elsewhere implements Credentials and not Registrations, which is the shape
	// of every store bound by an installation keeping credentials of its own.
	h := buildRegHarness(t, &fakeAccounts{byEmail: map[string]string{}}, &fakeSender{}, elsewhere{})

	answer := h.post(t, "/store/v1/auth/register",
		`{"email":"nobody@example.test","password":"a long enough password"}`)

	assert.Equal(t, http.StatusNotFound, answer.Code,
		"a store with nowhere to put a pending row leaves the flow absent rather "+
			"than half-working: body %s", answer.Body.String())
}

// TestTheRegistrationIsRateLIMITED is the bound that keeps this shop from being
// pointed at somebody.
//
// One request makes the installation send mail to an address a stranger chose, so
// an unlimited endpoint is a mail cannon with a shop's reputation behind it.
func TestTheRegistrationIsRateLIMITED(t *testing.T) {
	t.Parallel()

	// The DEFAULT bound, which is what an installation that binds no limiter gets.
	accounts := &fakeAccounts{byEmail: map[string]string{}}
	sender := &fakeSender{}
	store := newMemoryRegistrations()
	m := identitysession.New(identitysession.Options{
		Secret:       []byte("a registration test signing secret of 32!"),
		Insecure:     true,
		Credentials:  store,
		Accounts:     accounts,
		Verification: sender,
	})
	require.NoError(t, m.Register(t.Context(), container.New(nil)))
	router := chi.NewRouter()
	m.Routes(router)
	h := &regHarness{module: m, router: router, accounts: accounts, sender: sender, store: store}

	var limited bool
	for i := range identitysession.DefaultRegistrationLimit + 3 {
		answer := h.post(t, "/store/v1/auth/register",
			`{"email":"flood`+string(rune('a'+i))+`@example.test","password":"a long password"}`)
		if answer.Code == http.StatusTooManyRequests {
			limited = true

			break
		}
	}

	assert.True(t, limited,
		"%d requests from one client went through unbounded",
		identitysession.DefaultRegistrationLimit+3)
	assert.LessOrEqual(t, len(h.sender.verified), identitysession.DefaultRegistrationLimit,
		"and no more messages went out than the bound allows")
}

// neverLimits is a limiter that allows everything.
//
// It is bound where the test is not about the bound. A nil Limiter would get the
// module's DEFAULT, which is a real limit and would make unrelated tests depend
// on how many requests they happen to send.
type neverLimits struct{}

// Allow says yes.
func (neverLimits) Allow(context.Context, string) (corehttp.Decision, error) {
	return corehttp.Decision{Allowed: true}, nil
}

// fakeAccounts is the installation's seam, in a map.
type fakeAccounts struct {
	mu      sync.Mutex
	byEmail map[string]string
	opened  []string
	lookErr error
	openErr error
}

// CustomerIDForEmail answers the empty string for an address with no account.
func (a *fakeAccounts) CustomerIDForEmail(_ context.Context, email string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.lookErr != nil {
		return "", a.lookErr
	}

	return a.byEmail[email], nil
}

// OpenAccount records the address and mints an id.
func (a *fakeAccounts) OpenAccount(_ context.Context, email string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.openErr != nil {
		return "", a.openErr
	}
	a.opened = append(a.opened, email)
	id := "cust_opened_" + email
	a.byEmail[email] = id

	return id, nil
}

// fakeSender records what would have gone out.
type fakeSender struct {
	mu                sync.Mutex
	verified          []string
	tokens            []string
	alreadyRegistered []string
	sendErr           error
}

// SendVerification records the address and the token.
func (s *fakeSender) SendVerification(_ context.Context, email, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sendErr != nil {
		return s.sendErr
	}
	s.verified = append(s.verified, email)
	s.tokens = append(s.tokens, token)

	return nil
}

// SendAlreadyRegistered records the address.
func (s *fakeSender) SendAlreadyRegistered(_ context.Context, email string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sendErr != nil {
		return s.sendErr
	}
	s.alreadyRegistered = append(s.alreadyRegistered, email)

	return nil
}

// storedCredential is one row the memory store wrote.
type storedCredential struct {
	customerID string
	email      string
	hash       string
}

// pendingRegistration is one row the memory store holds.
type pendingRegistration struct {
	tokenHash string
	email     string
	hash      string
	expiresAt time.Time
}

// memoryRegistrations imitates the real store's OBSERVABLE answers.
//
// It keeps the two behaviors the rules rest on: asking again replaces the
// address's pending row, and taking a token removes it in the same breath. That
// the SQL really does those is the integration lane's question.
type memoryRegistrations struct {
	mu          sync.Mutex
	pending     []pendingRegistration
	credentials []storedCredential
	// expired makes every pending row already past its deadline.
	expired bool
}

// newMemoryRegistrations is an empty one.
func newMemoryRegistrations() *memoryRegistrations {
	return &memoryRegistrations{}
}

// Credential answers a written credential.
func (s *memoryRegistrations) Credential(
	_ context.Context, email string,
) (customerID, passwordHash string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, row := range s.credentials {
		if row.email == email {
			return row.customerID, row.hash, nil
		}
	}

	return "", "", identitysession.ErrPasswordMismatch
}

// Put writes one, replacing the customer's own.
func (s *memoryRegistrations) Put(_ context.Context, customerID, email, hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.credentials {
		if s.credentials[i].customerID == customerID {
			s.credentials[i] = storedCredential{customerID, email, hash}

			return nil
		}
	}
	s.credentials = append(s.credentials, storedCredential{customerID, email, hash})

	return nil
}

// PutRegistration replaces the address's pending row.
func (s *memoryRegistrations) PutRegistration(
	_ context.Context, tokenHash, email, passwordHash string, expiresAt time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.expired {
		expiresAt = time.Now().UTC().Add(-time.Minute)
	}
	row := pendingRegistration{tokenHash, email, passwordHash, expiresAt}
	for i := range s.pending {
		if s.pending[i].email == email {
			s.pending[i] = row

			return nil
		}
	}
	s.pending = append(s.pending, row)

	return nil
}

// TakeRegistration removes the row and answers it, refusing an expired one.
func (s *memoryRegistrations) TakeRegistration(
	_ context.Context, tokenHash string,
) (email, passwordHash string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.pending {
		if s.pending[i].tokenHash != tokenHash {
			continue
		}
		row := s.pending[i]
		s.pending = append(s.pending[:i], s.pending[i+1:]...)
		if !row.expiresAt.After(time.Now().UTC()) {
			return "", "", identitysession.ErrNoRegistration
		}

		return row.email, row.hash, nil
	}

	return "", "", identitysession.ErrNoRegistration
}

var (
	_ identitysession.Credentials   = (*memoryRegistrations)(nil)
	_ identitysession.Registrations = (*memoryRegistrations)(nil)
	_ identitysession.Accounts      = (*fakeAccounts)(nil)
	_ identitysession.Verification  = (*fakeSender)(nil)
)
