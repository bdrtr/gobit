package http_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/audit"
	corehttp "github.com/bdrtr/gobit/core/http"
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

// auditServerReading wraps a handler that records the reads of the given paths.
func auditServerReading(writer corehttp.AuditWriter, status int, paths ...string) http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	})

	audited := corehttp.Audit(writer, func() string { return "audit_1" }, nil,
		corehttp.AuditReadsOf(paths...))(inner)

	return corehttp.RequestLogger(discardLogger())(audited)
}

// readRequest builds a GET carrying an admin identity, for the given path.
func readRequest(path string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, http.NoBody)

	return r.WithContext(corehttp.WithPrincipal(r.Context(), corehttp.Principal{
		ID: "user_1", Kind: "user",
	}))
}

// TestTheOneReadThatIsAudited is the exception to the rule above, and both
// halves of it.
//
// The rule excludes reads because "somebody listed the orders" answers no
// question. That reason does not survive contact with one path: the audit log's
// own. Who READ the record of who did what is the question an incident asks, and
// it is the one read an intruder makes — they cannot alter the log, but they can
// learn from it what is known about them.
//
// The second half matters as much as the first. An exception that leaked would
// bury the writes it exists to protect: the audited path is compared EXACTLY, so
// a neighboring path, a prefix of it and a longer path that starts with it are
// all still unrecorded.
func TestTheOneReadThatIsAudited(t *testing.T) {
	const audited = "/admin/v1/audit-log"

	writer := &fakeAudit{}
	h := auditServerReading(writer, http.StatusOK, audited)

	h.ServeHTTP(httptest.NewRecorder(), readRequest(audited))

	require.Len(t, writer.entries, 1,
		"reading the record of who did what is the question an incident starts with")
	assert.Equal(t, http.MethodGet, writer.entries[0].Method)
	assert.Equal(t, audited, writer.entries[0].Path)
	assert.Equal(t, "user_1", writer.entries[0].ActorID)

	for _, other := range []string{
		"/admin/v1/orders",
		"/admin/v1/audit",
		"/admin/v1/audit-logs",
		"/admin/v1/audit-log/extra",
	} {
		writer.entries = nil
		h.ServeHTTP(httptest.NewRecorder(), readRequest(other))

		assert.Empty(t, writer.entries,
			"%s was recorded; the exception is one exact path, and an exception that "+
				"spreads buries the writes the log exists for", other)
	}
}

// TestTheAuditedReadCarriesItsQueryStringNowhere pins that the recorded path is
// the PATH.
//
// A listing's filters are query parameters, and recording them would turn one
// audited endpoint into an unbounded set of distinct entries — every filter
// combination its own row, and an operator paging through an incident writing a
// row per page. What the record has to answer is that the log was read, by whom,
// and with what outcome.
func TestTheAuditedReadCarriesItsQueryStringNowhere(t *testing.T) {
	const audited = "/admin/v1/audit-log"

	writer := &fakeAudit{}
	h := auditServerReading(writer, http.StatusOK, audited)

	h.ServeHTTP(httptest.NewRecorder(), readRequest(audited+"?actor_id=usr_1&limit=10"))

	require.Len(t, writer.entries, 1, "the query string must not stop the path from matching")
	assert.Equal(t, audited, writer.entries[0].Path,
		"the row records the path; filters would multiply one endpoint into many entries")
}

// TestWithNoAuditedReadsNothingChanges keeps the exception opt-in.
//
// Every installation that does not expose the audit log passes no paths at all,
// and for those the rule is exactly what it was: writes only.
func TestWithNoAuditedReadsNothingChanges(t *testing.T) {
	writer := &fakeAudit{}
	h := auditServerReading(writer, http.StatusOK)

	h.ServeHTTP(httptest.NewRecorder(), readRequest("/admin/v1/audit-log"))

	assert.Empty(t, writer.entries,
		"with no audited reads declared the middleware records writes only")
}
