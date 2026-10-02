//go:build integration

package identitysession_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	identitysession "github.com/bdrtr/gobit/contrib/identity-session"
	"github.com/bdrtr/gobit/core/personaldata"
)

// anchors is the real store's session anchor (ADR 0374).
func anchors(t *testing.T, m *identitysession.Module) identitysession.SessionAnchors {
	t.Helper()

	store, ok := m.Credentials().(identitysession.SessionAnchors)
	require.True(t, ok, "the module's own store keeps the moment sessions count from (ADR 0374)")

	return store
}

// TestTheSessionAnchorNeverMovesBack is ADR 0374 against a real PostgreSQL: a
// credential starts with no anchor, the first end sets it, an earlier moment
// does not move it back, and a customer with no credential has none to move.
func TestTheSessionAnchorNeverMovesBack(t *testing.T) {
	m := registered(t)
	store := anchors(t, m)
	ctx := t.Context()
	customer := "cust_06G8ANCHORMOVES0000000"
	require.NoError(t, m.Credentials().Put(ctx, customer, "anchor@example.test", testHash))

	from, err := store.SessionsValidFrom(ctx, customer)
	require.NoError(t, err)
	assert.True(t, from.IsZero(), "a credential nobody ended counts every session")

	later := time.Date(2026, time.October, 3, 9, 0, 0, 700_000_000, time.UTC)
	require.NoError(t, store.EndSessionsBefore(ctx, customer, later))
	require.NoError(t, store.EndSessionsBefore(ctx, customer, later.Add(-time.Hour)))
	from, err = store.SessionsValidFrom(ctx, customer)
	require.NoError(t, err)
	assert.True(t, later.Equal(from), "the later moment stays, at the millisecond it was written: %s", from)

	require.ErrorIs(t, store.EndSessionsBefore(ctx, "cust_06G8NOCREDENTIAL000000", later),
		identitysession.ErrNoCredential)
	from, err = store.SessionsValidFrom(ctx, "cust_06G8NOCREDENTIAL000000")
	require.NoError(t, err)
	assert.True(t, from.IsZero(), "a customer with no credential here has no anchor")

	// Writing the password again leaves the anchor where it was: only the
	// handlers move it, before they write.
	require.NoError(t, m.Credentials().Put(ctx, customer, "anchor@example.test", testHash))
	from, err = store.SessionsValidFrom(ctx, customer)
	require.NoError(t, err)
	assert.True(t, later.Equal(from))
}

// TestEndingTheOtherSessionsAgainstTheRealStore: the route is mounted on the
// module's own store, and a session from before it proves nobody while the one
// it answers with works.
func TestEndingTheOtherSessionsAgainstTheRealStore(t *testing.T) {
	m := registered(t)
	r := chi.NewRouter()
	m.Routes(r)

	customerID := "cust_06G8ANCHORROUTE00000000"
	written := send(t, r, http.MethodPut, "/admin/v1/customer-credentials",
		fmt.Sprintf(`{"customer_id":%q,"email":"anchor-route@example.test","password":"a real password"}`,
			customerID))
	require.Equal(t, http.StatusNoContent, written.Code, written.Body.String())

	signIn := func() *http.Cookie {
		in := send(t, r, http.MethodPost, "/store/v1/auth/sign-in",
			`{"email":"anchor-route@example.test","password":"a real password"}`)
		require.Equal(t, http.StatusNoContent, in.Code, in.Body.String())

		return in.Result().Cookies()[0]
	}
	// The phone's session is issued a whole argon2id verification before the
	// laptop's request ends it, which is milliseconds at the anchor's resolution.
	phone := signIn()
	laptop := signIn()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/store/v1/auth/sessions/revoke-others", http.NoBody)
	req.Header.Set("Cookie", laptop.Name+"="+laptop.Value)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())

	_, err := m.Sessions().CustomerID(requestCarrying(t, phone))
	require.Error(t, err, "the phone's session proves nobody")
	proven, err := m.Sessions().CustomerID(requestCarrying(t, rec.Result().Cookies()[0]))
	require.NoError(t, err, "the laptop's new session counts")
	assert.Equal(t, customerID, proven)
}

// requestCarrying is a request with one cookie.
func requestCarrying(t *testing.T, cookie *http.Cookie) *http.Request {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	req.Header.Set("Cookie", cookie.Name+"="+cookie.Value)

	return req
}

// TestADossierSaysWhenTheSessionsWereEnded: the anchor is reported as the
// moment it is, and as nothing before there is one; an erasure takes it with
// the row.
func TestADossierSaysWhenTheSessionsWereEnded(t *testing.T) {
	m := registered(t)
	ctx := t.Context()
	customer := "cust_06G8ANCHORDOSSIER000000"
	require.NoError(t, m.Credentials().Put(ctx, customer, "anchor-dossier@example.test", testHash))

	reported := func() any {
		dossier, err := m.PersonalDataOf(ctx, personaldata.Subject{CustomerID: customer})
		require.NoError(t, err)
		require.Len(t, dossier.Records, 1)
		for _, field := range dossier.Records[0].Fields {
			if field.Column == "sessions_valid_from" {
				return field.Value
			}
		}
		t.Fatal("the dossier does not report the column the declaration names")

		return nil
	}
	assert.Nil(t, reported(), "nothing was ever ended")

	at := time.Date(2026, time.October, 3, 9, 30, 0, 0, time.UTC)
	require.NoError(t, anchors(t, m).EndSessionsBefore(ctx, customer, at))
	moment, ok := reported().(time.Time)
	require.True(t, ok, "a moment is reported as a time")
	assert.True(t, at.Equal(moment))

	result, err := m.Erase(ctx, personaldata.Subject{CustomerID: customer})
	require.NoError(t, err)
	assert.Equal(t, personaldata.Deleted, result.Outcome)
	from, err := anchors(t, m).SessionsValidFrom(ctx, customer)
	require.NoError(t, err)
	assert.True(t, from.IsZero(), "the anchor went with the row")
}
