//go:build integration

package webpush

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/testdb"
)

// The two rules of ADR 0440 on the plugin's own tables: an endpoint that is
// not a push service's is never sent to (D292), and a device is bound only to
// a customer the request proves (D293).

// TestAStoredEndpointThatIsNotAPushServiceIsRemovedUnsent is the row stored
// before the rule: neither an order nor a broadcast reaches its address, and
// the row is removed.
func TestAStoredEndpointThatIsNotAPushServiceIsRemovedUnsent(t *testing.T) {
	st := freshStore(t)
	push := newPushServer(t, http.StatusCreated)
	internal := newPushServer(t, http.StatusOK)
	m := newTestModule(t, st, push)

	device(t, st, push.URL+"/p/real", "cust_1", m.opts.fingerprint)
	device(t, st, internal.URL+"/admin/purge", "cust_1", m.opts.fingerprint)

	require.NoError(t, m.onOrderPlaced(t.Context(), placedEvent("cust_1")))

	assert.Len(t, push.received(), 1, "the push service's device is pushed")
	assert.Empty(t, internal.received(), "an address that is not a push service's hears nothing")
	assert.Equal(t, 1, countRows(t), "the row naming it is removed")

	device(t, st, internal.URL+"/admin/purge-again", "", m.opts.fingerprint)
	total := m.broadcast(t.Context(), broadcastRequest{Event: orderPlacedEvent, Data: map[string]string{}})

	assert.Equal(t, fanOutResult{Attempted: 2, Delivered: 1, Gone: 1}, total)
	assert.Empty(t, internal.received(), "nor does a broadcast reach it")
	assert.Equal(t, 1, countRows(t))
}

// subscribeBody is what a browser posts, for the endpoint and the customer.
func subscribeBody(t *testing.T, endpoint, customerID string) string {
	t.Helper()

	p256dh, auth := newDeviceKeys(t)
	body, err := json.Marshal(map[string]any{
		"endpoint":    endpoint,
		"keys":        map[string]string{"p256dh": p256dh, "auth": auth},
		"customer_id": customerID,
	})
	require.NoError(t, err)

	return string(body)
}

// TestASubscriptionStoresNothingItCannotProve drives the route: an endpoint
// off the list and a customer the request does not prove are refused before
// a row is written, and what is stored is bound to the proven customer.
func TestASubscriptionStoresNothingItCannotProve(t *testing.T) {
	st := freshStore(t)
	push := newPushServer(t, http.StatusCreated)
	internal := newPushServer(t, http.StatusOK)
	m := newTestModule(t, st, push)

	c := container.New(slog.New(slog.DiscardHandler))
	identity := &headerIdentity{}
	require.NoError(t, c.Provide(corehttp.IdentityName, identity))
	m.identity = &identityBinding{c: c, log: slog.New(slog.DiscardHandler)}

	router := chi.NewRouter()
	m.Routes(router)
	subscribe := func(body, proves string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, pathSubscribe, strings.NewReader(body))
		if proves != "" {
			r.Header.Set(provenHeader, proves)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)

		return w
	}

	for name, tc := range map[string]struct {
		body, proves string
		status       int
	}{
		"an address that is not a push service's":  {subscribeBody(t, internal.URL+"/p", ""), "", http.StatusUnprocessableEntity},
		"the metadata address":                     {subscribeBody(t, "http://169.254.169.254/latest/meta-data/", ""), "", http.StatusUnprocessableEntity},
		"a customer the request does not prove":    {subscribeBody(t, push.URL+"/p/1", "cust_victim"), "", http.StatusUnauthorized},
		"a customer another request proves":        {subscribeBody(t, push.URL+"/p/2", "cust_victim"), "cust_stranger", http.StatusForbidden},
		"an identity that cannot answer for them":  {subscribeBody(t, push.URL+"/p/3", "cust_victim"), "down", http.StatusServiceUnavailable},
		"an endpoint off the list, with the proof": {subscribeBody(t, internal.URL+"/p", "cust_victim"), "cust_victim", http.StatusUnprocessableEntity},
	} {
		t.Run(name, func(t *testing.T) {
			rec := subscribe(tc.body, tc.proves)
			assert.Equal(t, tc.status, rec.Code, rec.Body.String())
			assert.Zero(t, countRows(t), "a refused subscription stores nothing")
		})
	}

	asked := identity.asked.Load()
	rec := subscribe(subscribeBody(t, internal.URL+"/p", "cust_victim"), "cust_victim")
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Equal(t, asked, identity.asked.Load(), "a body that cannot be stored costs the identity nothing")

	rec = subscribe(subscribeBody(t, push.URL+"/p/guest", ""), "")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	rec = subscribe(subscribeBody(t, push.URL+"/p/owner", "cust_victim"), "cust_victim")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	bound, err := st.byCustomer(t.Context(), "cust_victim")
	require.NoError(t, err)
	require.Len(t, bound, 1, "the proven customer's device, and only it, is bound")
	assert.Equal(t, push.URL+"/p/owner", bound[0].Endpoint)
	assert.Equal(t, 2, countRows(t))
}

// TestEveryBindingBeforeTheProofIsCleared is migration 000003: a binding
// written before it cannot be told from a stranger's, so each leaves
// customer_id and the device is kept; the claim moves to unproven_customer_id
// with updated_at untouched, the evidence the advisory's queries read.
func TestEveryBindingBeforeTheProofIsCleared(t *testing.T) {
	ctx := t.Context()

	const clearing = "000003_a_device_is_bound_by_proof.up.sql"
	_, err := fs.Stat(migrationsRoot, clearing)
	require.NoError(t, err, "the migration this test is about is not in the tree")
	version := migrationVersion(t, clearing)

	dsn := testdb.New(t, testDSN, "webpush_proof")
	require.NoError(t, db.Migrate(ctx, dsn, migrationsUpTo(t, version-1), ModuleName))
	pool := testPoolFor(t, dsn)
	st := newStore(pool.Pool())

	device(t, st, "https://fcm.googleapis.com/fcm/send/claimed", "cust_victim", "fp")
	device(t, st, "https://fcm.googleapis.com/fcm/send/guest", "", "fp")

	var stampedBefore time.Time
	require.NoError(t, pool.Pool().QueryRow(ctx,
		`SELECT updated_at FROM webpush_subscription WHERE customer_id = 'cust_victim'`).
		Scan(&stampedBefore))

	require.NoError(t, db.Migrate(ctx, dsn, migrationsUpTo(t, version), ModuleName))

	var kept string
	var stampedAfter time.Time
	require.NoError(t, pool.Pool().QueryRow(ctx,
		`SELECT unproven_customer_id, updated_at FROM webpush_subscription
		 WHERE endpoint = 'https://fcm.googleapis.com/fcm/send/claimed'`).
		Scan(&kept, &stampedAfter))
	assert.Equal(t, "cust_victim", kept, "the claim is kept as evidence")
	assert.True(t, stampedBefore.Equal(stampedAfter), "the moment of the claim is kept as evidence")

	bystander, err := st.byCustomer(ctx, "cust_victim")
	require.NoError(t, err)
	assert.Empty(t, bystander, "an unproven claim reaches nobody's confirmations")

	removed, err := st.deleteByCustomer(ctx, "cust_victim")
	require.NoError(t, err)
	assert.EqualValues(t, 1, removed, "erasing the customer deletes the device kept under the claim")

	var bound, total int
	require.NoError(t, pool.Pool().QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE customer_id <> ''), count(*) FROM webpush_subscription`).
		Scan(&bound, &total))
	assert.Zero(t, bound, "no binding written on the storefront's word survives")
	assert.Equal(t, 1, total, "the guest's device stays")

	require.NoError(t, db.MigrateDown(ctx, dsn, migrationsRoot, ModuleName, 1),
		"the rollback of the clearing has to run")
}
