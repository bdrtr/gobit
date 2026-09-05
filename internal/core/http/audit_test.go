package http_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/audit"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// fakeAudit records what the middleware asked to store.
type fakeAudit struct {
	entries []audit.Entry
	ids     []string
	err     error
}

// Write records the entry and applies the scripted behavior.
func (f *fakeAudit) Write(_ context.Context, id string, e audit.Entry) error {
	f.ids = append(f.ids, id)
	f.entries = append(f.entries, e)

	return f.err
}

// auditServer wraps a handler in the request logger — which supplies the
// response wrapper the audit reads the status from — and the audit itself.
func auditServer(writer corehttp.AuditWriter, status int) http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	})

	audited := corehttp.Audit(writer, func() string { return "audit_1" }, nil)(inner)

	return corehttp.RequestLogger(discardLogger())(audited)
}

// auditRequest builds a request carrying an admin identity.
func auditRequest(method string) *http.Request {
	r := httptest.NewRequest(method, "/admin/v1/orders/order_1/cancel", http.NoBody)

	return r.WithContext(corehttp.WithPrincipal(r.Context(), corehttp.Principal{
		ID:   "user_1",
		Kind: "user",
	}))
}

// TestAnAdminWriteLeavesATrail is what the admin API did not do.
//
// It authenticated and authorized every write and then forgot it happened; the
// only durable trace of a change was a timestamp on the row.
func TestAnAdminWriteLeavesATrail(t *testing.T) {
	writer := &fakeAudit{}
	h := auditServer(writer, http.StatusOK)

	h.ServeHTTP(httptest.NewRecorder(), auditRequest(http.MethodPost))

	require.Len(t, writer.entries, 1)
	got := writer.entries[0]
	assert.Equal(t, "user_1", got.ActorID)
	assert.Equal(t, "user", got.ActorKind)
	assert.Equal(t, http.MethodPost, got.Method)
	assert.Equal(t, "/admin/v1/orders/order_1/cancel", got.Path,
		"the REQUEST's path is recorded, not the route pattern: the question is which one was touched")
	assert.Equal(t, http.StatusOK, got.Status)
}

// TestAREFUSEDWriteIsRecordedToo is why the audit sits outside the identity
// guard.
//
// An attempt to change something one is not allowed to change is exactly the
// line an incident is looking for.
func TestAREFUSEDWriteIsRecordedToo(t *testing.T) {
	writer := &fakeAudit{}
	h := auditServer(writer, http.StatusForbidden)

	h.ServeHTTP(httptest.NewRecorder(), auditRequest(http.MethodDelete))

	require.Len(t, writer.entries, 1)
	assert.Equal(t, http.StatusForbidden, writer.entries[0].Status)
}

// TestReadsAreNotAudited keeps the writes from being buried in volume.
func TestReadsAreNotAudited(t *testing.T) {
	writer := &fakeAudit{}
	h := auditServer(writer, http.StatusOK)

	h.ServeHTTP(httptest.NewRecorder(), auditRequest(http.MethodGet))

	assert.Empty(t, writer.entries, "knowing that somebody listed the orders answers no question")
}

// TestAFailedAuditDoesNotFailTheRequest holds the residual this decision
// accepts.
//
// The change is already committed. Refusing the response would undo nothing and
// would turn a logging fault into a customer-visible outage.
func TestAFailedAuditDoesNotFailTheRequest(t *testing.T) {
	writer := &fakeAudit{err: assertAnError()}
	h := auditServer(writer, http.StatusOK)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, auditRequest(http.MethodPost))

	assert.Equal(t, http.StatusOK, rec.Code)
}

// TestEveryRowGetsItsOwnID stops two writes from collapsing into one.
func TestEveryRowGetsItsOwnID(t *testing.T) {
	writer := &fakeAudit{}
	h := auditServer(writer, http.StatusOK)

	h.ServeHTTP(httptest.NewRecorder(), auditRequest(http.MethodPost))
	h.ServeHTTP(httptest.NewRecorder(), auditRequest(http.MethodPost))

	require.Len(t, writer.ids, 2, "two writes are two facts")
}

// assertAnError returns an error the fake writer can fail with.
func assertAnError() error {
	return context.DeadlineExceeded
}

// auditGuardedServer wraps the handler the way the REAL stack does: the audit
// OUTSIDE, the identity guard inside it.
//
// The other helper in this file puts the identity on the request itself, which
// is a shape no real request has.
func auditGuardedServer(writer corehttp.AuditWriter, auth corehttp.Authenticator) http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	guarded := corehttp.RequireAdmin(auth)(inner)
	audited := corehttp.Audit(writer, func() string { return "audit_1" }, nil)(guarded)

	return corehttp.RequestLogger(discardLogger())(audited)
}

// guardedWrite sends an admin write through the given stack.
func guardedWrite(t *testing.T, h http.Handler) {
	t.Helper()

	r := httptest.NewRequest(http.MethodPost, "/admin/v1/orders/order_1/cancel", http.NoBody)
	r.Header.Set("Authorization", "Bearer token")
	h.ServeHTTP(httptest.NewRecorder(), r)
}

// TestTheActorIsRecordedWhenTheGuardRunsInside is the bug the tests above could
// not see.
//
// They hand the middleware a request that already carries the identity. A real
// one never does: the guard establishes it on a DERIVED request, and the audit
// — outside, so that refused writes are recorded — kept holding the original.
// Every row named nobody, which is the one question the table exists to answer.
// A live run against the binary is what showed it.
func TestTheActorIsRecordedWhenTheGuardRunsInside(t *testing.T) {
	writer := &fakeAudit{}
	guardedWrite(t, auditGuardedServer(writer, fixedAuthenticator{
		principal: corehttp.Principal{ID: "user_1", Kind: "user"},
	}))

	require.Len(t, writer.entries, 1)
	assert.Equal(t, "user_1", writer.entries[0].ActorID)
	assert.Equal(t, "user", writer.entries[0].ActorKind)
	assert.Equal(t, http.StatusOK, writer.entries[0].Status)
}

// TestARefusedWriteNamesNobodyAndIsStillRecorded holds the reason the audit
// sits outside the guard.
//
// Moving it inside would make the actor arrive for free and would silently drop
// this row, which is the one a person goes looking for after an attempt they
// did not expect.
func TestARefusedWriteNamesNobodyAndIsStillRecorded(t *testing.T) {
	writer := &fakeAudit{}
	guardedWrite(t, auditGuardedServer(writer, fixedAuthenticator{
		err: errors.New("invalid"),
	}))

	require.Len(t, writer.entries, 1)
	assert.Empty(t, writer.entries[0].ActorID)
	assert.Equal(t, http.StatusUnauthorized, writer.entries[0].Status)
}
