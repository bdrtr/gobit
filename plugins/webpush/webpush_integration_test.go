//go:build integration

// The tests in this file need a real PostgreSQL (and therefore Docker); they
// are behind the `integration` tag so `make test` stays fast. Run them with:
// make test-integration
//
// Two claims here cannot be proved by any smaller test.
//
// The first is that the migration is really reversible. THE ARCHITECTURE GATES
// DO NOT COVER THIS FILE'S MIGRATION: both TestMigrationsCanBeRolledBack and
// TestMigrationsCanReallyBeRolledBack walk moduleNames(t), which reads
// internal/modules/ only — a plugin that brings a table has its up/down pair
// certified by nothing. This test is that certification, and it is a
// requirement of ADR 0018 rather than a nicety.
//
// The second is that a 401 does NOT delete a subscription while a 410 does. It
// is the sharpest rule in the plugin — getting it backwards wipes the whole
// device registry the afternoon somebody rotates a VAPID key — and it can only
// be seen by watching a real row survive a real refusal.
package webpush

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/eventbus"
)

const postgresImage = "postgres:16-alpine"

var (
	// testPool is the pool every test shares.
	testPool *db.Pool
	// testDSN is the shared database's address.
	testDSN string
)

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres brings up one Postgres, applies the plugin's schema and runs
// every test on it.
func runWithPostgres(m *testing.M) int {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_webpush"),
		tcpostgres.WithUsername("gobit"),
		tcpostgres.WithPassword("gobit"),
		tcpostgres.BasicWaitStrategies(),
	)
	defer func() {
		if termErr := testcontainers.TerminateContainer(ctr); termErr != nil {
			fmt.Fprintf(os.Stderr, "the postgres container could not be stopped: %v\n", termErr)
		}
	}()
	if err != nil {
		fmt.Fprintf(os.Stderr, "the postgres container could not be started: %v\n", err)

		return 1
	}

	testDSN, err = ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "the connection string could not be read: %v\n", err)

		return 1
	}

	// The schema is applied the SAME way production applies it — through the
	// module's own Migrations() and module name. Writing CREATE TABLE by hand
	// here would leave the migration itself untested.
	if err = db.Migrate(ctx, testDSN, migrationsRoot, ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "the webpush schema could not be applied: %v\n", err)

		return 1
	}

	testPool, err = db.New(ctx, db.DefaultConfig(testDSN), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "the connection pool could not be opened: %v\n", err)

		return 1
	}
	defer testPool.Close()

	return m.Run()
}

// freshStore empties the table and returns a store over it.
func freshStore(t *testing.T) *store {
	t.Helper()

	_, err := testPool.Pool().Exec(t.Context(), `TRUNCATE webpush_subscription`)
	require.NoError(t, err)

	return newStore(testPool.Pool())
}

// newDeviceKeys mints what a browser would produce.
func newDeviceKeys(t *testing.T) (p256dh, auth string) {
	t.Helper()

	key, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)

	secret := make([]byte, authSecretLength)
	_, err = rand.Read(secret)
	require.NoError(t, err)

	return base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
		base64.RawURLEncoding.EncodeToString(secret)
}

// device stores one subscription and returns it.
func device(t *testing.T, st *store, endpoint, customerID, fingerprint string) subscription {
	t.Helper()

	p256dh, auth := newDeviceKeys(t)
	sub := subscription{
		Endpoint:    endpoint,
		P256DH:      p256dh,
		Auth:        auth,
		CustomerID:  customerID,
		Fingerprint: fingerprint,
	}

	id, err := st.upsert(t.Context(), sub)
	require.NoError(t, err)
	sub.ID = id

	return sub
}

// countRows reports how many subscriptions are stored.
func countRows(t *testing.T) int {
	t.Helper()

	var n int
	require.NoError(t, testPool.Pool().
		QueryRow(t.Context(), `SELECT count(*) FROM webpush_subscription`).Scan(&n))

	return n
}

// --- the migration ----------------------------------------------------------

// TestTheMigrationIsReallyReversible is the gate no architecture test provides.
//
// moduleNames(t) in internal/arch reads internal/modules/ only, so a plugin's
// migration pair is certified by nothing. Without this test a down migration
// that does not parse would ship, and it would be discovered by an operator
// trying to move between versions — at the worst possible moment.
//
// It runs on its OWN database rather than the shared one: rolling the shared
// schema back would pull the table out from under every other test in the file.
func TestTheMigrationIsReallyReversible(t *testing.T) {
	ctx := t.Context()

	dsn := freshDatabase(t)

	require.NoError(t, db.Migrate(ctx, dsn, migrationsRoot, ModuleName),
		"the up migration has to apply")

	version, dirty, err := db.Version(ctx, dsn, ModuleName)
	require.NoError(t, err)
	require.False(t, dirty, "the ledger must not be dirty after a clean apply")
	require.Equal(t, uint(1), version)

	require.NoError(t, db.MigrateDown(ctx, dsn, migrationsRoot, ModuleName, 1),
		"the down migration has to roll back")

	var exists bool
	require.NoError(t, testPoolFor(t, dsn).Pool().QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_name = 'webpush_subscription'
		)`).Scan(&exists))
	assert.False(t, exists, "the table has to be gone after the rollback")

	// Applying again proves the pair is a CYCLE rather than a one-way trip: a
	// down migration that leaves an index behind passes the check above and
	// fails here.
	require.NoError(t, db.Migrate(ctx, dsn, migrationsRoot, ModuleName),
		"the schema has to apply again after a rollback")
}

// TestTheMigrationIsReversibleWithDataInIt covers what the arch gate's own
// godoc admits it cannot.
//
// The repository's rollback gate runs against a FRESH EMPTY container, and says
// so: it does not catch data-dependent rollback failures. A DROP TABLE is
// blocked by nothing, but that is a fact worth pinning rather than assuming —
// the day a foreign key or a dependent view is added, this is what fails.
func TestTheMigrationIsReversibleWithDataInIt(t *testing.T) {
	ctx := t.Context()

	dsn := freshDatabase(t)
	require.NoError(t, db.Migrate(ctx, dsn, migrationsRoot, ModuleName))

	pool := testPoolFor(t, dsn)
	st := newStore(pool.Pool())
	p256dh, auth := newDeviceKeys(t)
	_, err := st.upsert(ctx, subscription{
		Endpoint: "https://push.example.test/p/withdata", P256DH: p256dh, Auth: auth,
		CustomerID: "cust_1", Fingerprint: "fp",
	})
	require.NoError(t, err)

	require.NoError(t, db.MigrateDown(ctx, dsn, migrationsRoot, ModuleName, 1),
		"the rollback has to work with rows in the table, not only on an empty one")
}

// --- the store's rules ------------------------------------------------------

// TestReSubscribeOverwritesTheKeysButKeepsTheBinding pins the upsert's two
// halves, which pull in opposite directions.
//
// The keys must be overwritten: they are what the browser just minted, and a
// stale pair means encrypting messages that device can no longer open — with no
// error on either side. The customer binding must NOT be cleared: a returning
// browser re-subscribes without sending one, and taking that as "log out" would
// unbind every device on every page load.
func TestReSubscribeOverwritesTheKeysButKeepsTheBinding(t *testing.T) {
	st := freshStore(t)
	const endpoint = "https://push.example.test/p/resub"

	first := device(t, st, endpoint, "cust_1", "fp_a")

	// The browser comes back: new keys, no customer id.
	newPublic, newAuth := newDeviceKeys(t)
	id, err := st.upsert(t.Context(), subscription{
		Endpoint: endpoint, P256DH: newPublic, Auth: newAuth,
		CustomerID: "", Fingerprint: "fp_a",
	})
	require.NoError(t, err)
	assert.Equal(t, first.ID, id, "a re-subscribe updates the row rather than opening a second one")
	assert.Equal(t, 1, countRows(t))

	stored, err := st.byCustomer(t.Context(), "cust_1")
	require.NoError(t, err)
	require.Len(t, stored, 1, "the customer binding must survive a re-subscribe")
	assert.Equal(t, newPublic, stored[0].P256DH, "the device's keys must be overwritten")
	assert.Equal(t, newAuth, stored[0].Auth)
}

// TestUnbindKeepsTheDevice proves logout clears the binding without losing the
// subscription.
//
// Deleting the row instead would leave the browser's permission grant alive
// while the server forgot it, so nothing would push to that device until the
// next re-subscribe repaired it by accident.
func TestUnbindKeepsTheDevice(t *testing.T) {
	st := freshStore(t)
	const endpoint = "https://push.example.test/p/shared"

	device(t, st, endpoint, "cust_1", "fp_a")

	require.NoError(t, st.unbind(t.Context(), endpoint))

	bound, err := st.byCustomer(t.Context(), "cust_1")
	require.NoError(t, err)
	assert.Empty(t, bound, "the previous user must no longer receive this device's pushes")
	assert.Equal(t, 1, countRows(t), "the device itself has to stay")
}

// TestAGuestOrderReachesNobody proves an empty customer id matches nothing.
//
// The query must not read an empty id as a wildcard: every device that
// subscribed before signing in carries one, so a guest order would push a
// stranger's confirmation to all of them.
func TestAGuestOrderReachesNobody(t *testing.T) {
	st := freshStore(t)
	device(t, st, "https://push.example.test/p/anon1", "", "fp_a")
	device(t, st, "https://push.example.test/p/anon2", "", "fp_a")

	found, err := st.byCustomer(t.Context(), "")

	require.NoError(t, err)
	assert.Empty(t, found, "an empty customer id must match no device at all")
}

// --- what the push service's answer does to a row ---------------------------

// pushServer is a fake push service that answers with a fixed status and
// records what it received.
type pushServer struct {
	*httptest.Server
	status   int
	requests []*http.Request
	bodies   [][]byte
}

// newPushServer starts a fake push service.
func newPushServer(t *testing.T, status int) *pushServer {
	t.Helper()

	p := &pushServer{status: status}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		p.requests = append(p.requests, r.Clone(context.Background()))
		p.bodies = append(p.bodies, body)
		w.WriteHeader(p.status)
	}))
	t.Cleanup(p.Close)

	return p
}

// newTestModule builds a module wired to the fake service.
func newTestModule(t *testing.T, st *store) *webpushModule {
	t.Helper()

	privateKey, publicKey, err := GenerateKey()
	require.NoError(t, err)
	key, err := parseVAPIDKey(privateKey)
	require.NoError(t, err)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(dir+"/order.placed.tmpl",
		[]byte(`{{define "title"}}Order {{.display_id}}{{end}}`+
			`{{define "body"}}{{.item_count}} items are on the way{{end}}`), 0o600))
	templates, err := loadTemplates(dir)
	require.NoError(t, err)

	m := newModule(moduleOptions{
		key:         key,
		publicKey:   publicKey,
		fingerprint: fingerprintOf(publicKey),
		subject:     "mailto:ops@example.test",
		templates:   templates,
		log:         slog.New(slog.DiscardHandler),
	})
	m.store = st
	m.sender = &sender{
		client:  &http.Client{},
		key:     key,
		subject: "mailto:ops@example.test",
		now:     time.Now,
	}

	return m
}

// placedEvent builds an order.placed event.
func placedEvent(customerID string) eventbus.Event {
	return eventbus.Event{
		Name: orderPlacedEvent,
		Data: map[string]any{
			"order_id":    "order_1",
			"display_id":  "1001",
			"customer_id": customerID,
			"item_count":  "2",
		},
	}
}

// TestAGoneSubscriptionIsRemoved proves a 410 drains the registry.
//
// The push service is the ONLY authoritative source for "this subscription is
// dead" — a browser that revokes permission tells nobody else. Without this the
// table grows forever and every order pays for pushes that cannot arrive.
func TestAGoneSubscriptionIsRemoved(t *testing.T) {
	st := freshStore(t)
	push := newPushServer(t, http.StatusGone)
	m := newTestModule(t, st)

	device(t, st, push.URL+"/p/dead", "cust_1", m.opts.fingerprint)

	require.NoError(t, m.onOrderPlaced(t.Context(), placedEvent("cust_1")))

	assert.Equal(t, 0, countRows(t), "a 410 has to remove the subscription")
}

// TestARefusedTokenNEVERRemovesASubscription is the sharpest rule in the
// plugin.
//
// A 401 means the token was refused, which happens when the VAPID key is
// rotated or a clock drifts — conditions that hit EVERY device at once and are
// repairable. Deleting on it wipes the entire registry the afternoon somebody
// rotates a key, and nothing on the server can put it back: only the browsers
// can, one visit at a time.
func TestARefusedTokenNEVERRemovesASubscription(t *testing.T) {
	for name, status := range map[string]int{
		"unauthorized": http.StatusUnauthorized,
		"forbidden":    http.StatusForbidden,
		"rate limited": http.StatusTooManyRequests,
		"server error": http.StatusInternalServerError,
	} {
		t.Run(name, func(t *testing.T) {
			st := freshStore(t)
			push := newPushServer(t, status)
			m := newTestModule(t, st)

			device(t, st, push.URL+"/p/live", "cust_1", m.opts.fingerprint)

			require.NoError(t, m.onOrderPlaced(t.Context(), placedEvent("cust_1")))

			assert.Equal(t, 1, countRows(t),
				"status %d must NOT delete a subscription; it is repairable and hits every device at once",
				status)
		})
	}
}

// TestASubscriptionFromAnotherKeyIsDrained proves the rotation graveyard drains
// itself.
//
// A row minted under a key we no longer hold can only ever answer 401, and 401
// never deletes — so without the fingerprint check the rotation leaves rows
// that are retried on every order forever and are reported by nothing.
func TestASubscriptionFromAnotherKeyIsDrained(t *testing.T) {
	st := freshStore(t)
	push := newPushServer(t, http.StatusCreated)
	m := newTestModule(t, st)

	device(t, st, push.URL+"/p/oldkey", "cust_1", "a fingerprint from a key that is gone")

	require.NoError(t, m.onOrderPlaced(t.Context(), placedEvent("cust_1")))

	assert.Equal(t, 0, countRows(t), "a row from a vanished key has to be removed")
	assert.Empty(t, push.requests, "and it must not be pushed to first")
}

// TestTheRequestCarriesEveryMandatoryHeader pins the headers whose absence is
// invisible from here.
//
// Without Content-Encoding the message arrives and the service worker sees
// nothing it can open. Without TTL the push service answers 400. Both look
// like success to a sender that only checks the transport.
func TestTheRequestCarriesEveryMandatoryHeader(t *testing.T) {
	st := freshStore(t)
	push := newPushServer(t, http.StatusCreated)
	m := newTestModule(t, st)

	device(t, st, push.URL+"/p/live", "cust_1", m.opts.fingerprint)

	require.NoError(t, m.onOrderPlaced(t.Context(), placedEvent("cust_1")))

	require.Len(t, push.requests, 1)
	got := push.requests[0]
	assert.Equal(t, "aes128gcm", got.Header.Get("Content-Encoding"))
	assert.Equal(t, "application/octet-stream", got.Header.Get("Content-Type"))
	assert.NotEmpty(t, got.Header.Get("TTL"), "RFC 8030 requires TTL; without it the answer is 400")
	assert.NotEmpty(t, got.Header.Get("Topic"), "the topic collapses a duplicate the bus delivered twice")
	assert.Contains(t, got.Header.Get("Authorization"), "vapid t=")
	assert.Contains(t, got.Header.Get("Authorization"), ", k=")

	assert.Equal(t, 1, countRows(t), "a delivered push leaves the subscription alone")
}

// TestAGuestOrderPushesNothing proves the handler stops before the registry.
func TestAGuestOrderPushesNothing(t *testing.T) {
	st := freshStore(t)
	push := newPushServer(t, http.StatusCreated)
	m := newTestModule(t, st)

	device(t, st, push.URL+"/p/somebody", "cust_1", m.opts.fingerprint)

	require.NoError(t, m.onOrderPlaced(t.Context(), placedEvent("")))

	assert.Empty(t, push.requests, "an order with no customer must reach no device")
}

// TestAFailedPushDoesNotFailTheEvent proves the subscriber never asks for a
// replay.
//
// Returning an error would make the bus redeliver, and a redelivery re-pushes
// to every device that already received the message. The order is already
// written; nothing about a courtesy notification is worth replaying an event
// for.
func TestAFailedPushDoesNotFailTheEvent(t *testing.T) {
	st := freshStore(t)
	push := newPushServer(t, http.StatusInternalServerError)
	m := newTestModule(t, st)

	device(t, st, push.URL+"/p/broken", "cust_1", m.opts.fingerprint)

	assert.NoError(t, m.onOrderPlaced(t.Context(), placedEvent("cust_1")),
		"a failed push must not ask the event bus for a redelivery")
}

// --- helpers ----------------------------------------------------------------

// freshDatabase creates an empty database and returns its address.
//
// The migration tests need their own: rolling the shared schema back would pull
// the table out from under every other test in the file.
func freshDatabase(t *testing.T) string {
	t.Helper()

	name := fmt.Sprintf("webpush_migration_%d", time.Now().UnixNano())
	_, err := testPool.Pool().Exec(t.Context(), `CREATE DATABASE `+name)
	require.NoError(t, err)

	t.Cleanup(func() {
		// The drop runs on a context detached from the test's, which is already
		// canceled by the time cleanup runs.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := testPool.Pool().Exec(ctx, `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`); err != nil {
			t.Logf("the temporary database %s could not be dropped: %v", name, err)
		}
	})

	return replaceDatabase(testDSN, name)
}

// replaceDatabase swaps the database name in a DSN.
func replaceDatabase(dsn, name string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	u.Path = "/" + name

	return u.String()
}

// testPoolFor opens a pool against one of the temporary databases.
func testPoolFor(t *testing.T, dsn string) *db.Pool {
	t.Helper()

	pool, err := db.New(t.Context(), db.DefaultConfig(dsn), nil)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return pool
}
