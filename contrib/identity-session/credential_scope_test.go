package identitysession_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	identitysession "github.com/bdrtr/gobit/contrib/identity-session"
	"github.com/bdrtr/gobit/core/audit"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// credentialPattern is the credential route as the router binds it.
const credentialPattern = "/admin/v1/customer-credentials/{customer_id}"

// TestTheCredentialRouteDemandsThePrivilegeItPublishes holds the published
// constant and the guard on the route together (ADR 0434).
//
// An integrator grants what [identitysession.ScopeCredentialWrite] names, and
// gobit's installation and its document read the privilege off the router
// (ADR 0263). A guard that drifted from the constant would demand a privilege
// nobody is told to grant.
func TestTheCredentialRouteDemandsThePrivilegeItPublishes(t *testing.T) {
	t.Parallel()

	m := identitysession.New(identitysession.Options{
		Secret:      []byte("a describing secret of at least thirty-two"),
		Credentials: nowhere{},
	})
	require.NoError(t, m.Register(t.Context(), emptyContainer(t)))

	r := chi.NewRouter()
	m.Routes(r)

	var demanded []string
	found := false
	require.NoError(t, chi.Walk(r, func(
		method, route string, _ http.Handler, middlewares ...func(http.Handler) http.Handler,
	) error {
		if method != http.MethodPut || route != credentialPattern {
			return nil
		}
		found = true
		for _, mw := range middlewares {
			if scope, ok := corehttp.ScopeDemandedBy(mw); ok {
				demanded = append(demanded, scope)
			}
		}

		return nil
	}))

	require.True(t, found, "the walk did not reach the credential route")
	assert.Equal(t, []string{identitysession.ScopeCredentialWrite}, demanded)
	assert.Equal(t, "customer-credential:write", identitysession.ScopeCredentialWrite,
		"the privilege is the credential's own, spelled as the scope dictionary spells a "+
			"resource of two words")
}

// TestTheAuditLogNamesTheCustomerWhoseCredentialWasWritten: the audit row of a
// write records the request's path, and the path is where every other audited
// write names its subject. The customer was in the body, so the row of a
// credential written for anybody read the same as one written for nobody.
func TestTheAuditLogNamesTheCustomerWhoseCredentialWasWritten(t *testing.T) {
	t.Parallel()

	m := identitysession.New(identitysession.Options{
		Secret:      []byte("a describing secret of at least thirty-two"),
		Credentials: nowhere{},
	})
	require.NoError(t, m.Register(t.Context(), emptyContainer(t)))

	rows := &auditRows{}
	operator := corehttp.Principal{
		ID: "usr_support", Kind: "user", Scopes: []string{"customer-credential:write"},
	}
	r := corehttp.NewRouter(corehttp.RouterOptions{
		Logger: slog.New(slog.DiscardHandler),
		Middlewares: []func(http.Handler) http.Handler{
			corehttp.Audit(rows, func() string { return "aud_1" }, slog.New(slog.DiscardHandler)),
			func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					next.ServeHTTP(w, req.WithContext(corehttp.WithPrincipal(req.Context(), operator)))
				})
			},
		},
	})
	m.Routes(r)

	const customer = "cust_06G8AUDITEDCREDENTIAL00"
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut,
		"/admin/v1/customer-credentials/"+customer,
		strings.NewReader(`{"email":"audited@example.test","password":"set by support"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())

	require.Len(t, rows.entries, 1, "the write was not audited")
	entry := rows.entries[0]
	assert.Equal(t, "usr_support", entry.ActorID)
	assert.Equal(t, http.StatusNoContent, entry.Status)
	assert.Equal(t, "/admin/v1/customer-credentials/"+customer, entry.Path,
		"the row names the customer whose credential was written")
}

// auditRows keeps what the audit middleware writes.
type auditRows struct {
	mu      sync.Mutex
	entries []audit.Entry
}

// Write records one entry.
func (a *auditRows) Write(_ context.Context, _ string, e audit.Entry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, e)

	return nil
}
