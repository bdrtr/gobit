//go:build integration

// The tests in this file need a real PostgreSQL (and therefore Docker); they
// are behind the `integration` tag so `make test` stays fast. Run them with:
// make test-integration
//
// Two claims here cannot be proved by any smaller test.
//
// The first is that the migration is reversible with rows in its tables. The
// architecture gates walk every migration directory, this plugin's included
// (migrationDirs, since 2026-09-06), and run each up, down and up again on an
// EMPTY schema; a rollback that rows block is invisible to them, and their own
// godoc says so. Carrying that test here is a requirement of ADR 0018.
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
	"io/fs"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/internal/testdb"
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

// freshStore empties the tables and returns a store over them.
func freshStore(t *testing.T) *store {
	t.Helper()

	_, err := testPool.Pool().Exec(t.Context(), `TRUNCATE webpush_subscription, webpush_claimed_event`)
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

// TestTheMigrationIsReallyReversible rolls the plugin's schema back one step
// at a time through the embedded files the binary ships, under the plugin's own
// ledger name, and finds each step back where it started.
//
// The architecture gates round-trip the same directory from the file system
// (migrationDirs), and so does the second up here, but neither sees a leftover:
// every up file creates with IF NOT EXISTS and applies over one without a word.
// So the schema is read from the catalog instead. The ups are applied one
// version at a time and every relation taken down after each; each down step
// then has to return the schema to the census of the version below it. A table
// or index a later migration adds and its down forgets is caught without this
// test naming it, including an index on 000001's table, which a rollback all
// the way would drop with the table.
//
// It runs on its OWN database rather than the shared one: rolling the shared
// schema back would pull the table out from under every other test in the file.
func TestTheMigrationIsReallyReversible(t *testing.T) {
	ctx := t.Context()

	dsn := testdb.New(t, testDSN, "webpush_migration")
	pool := testPoolFor(t, dsn)
	ledger, err := db.MigrationsTable(ModuleName)
	require.NoError(t, err)

	// census[i] is the schema below versions[i]; the last is the newest.
	versions := migrationVersions(t)
	census := []map[string]bool{schemaRelations(ctx, t, pool, ledger)}
	for _, v := range versions {
		require.NoError(t, db.Migrate(ctx, dsn, migrationsUpTo(t, v), ModuleName),
			"the up to version %d has to apply", v)
		census = append(census, schemaRelations(ctx, t, pool, ledger))
	}

	version, dirty, err := db.Version(ctx, dsn, ModuleName)
	require.NoError(t, err)
	require.False(t, dirty, "the ledger must not be dirty after a clean apply")
	require.Equal(t, versions[len(versions)-1], version)

	// The subjects by name: a census that does not hold them has gone blind.
	require.Contains(t, census[len(census)-1], tableSubscription)
	require.Contains(t, census[len(census)-1], tableClaimedEvent)

	for i := len(versions) - 1; i >= 0; i-- {
		require.NoError(t, db.MigrateDown(ctx, dsn, migrationsRoot, ModuleName, 1),
			"the down of version %d has to roll back", versions[i])
		assert.Equalf(t, sortedNames(census[i]), sortedNames(schemaRelations(ctx, t, pool, ledger)),
			"the down of version %d has to leave the schema as the version below it had it", versions[i])
	}

	// Applying again proves the ledger came back to zero and the ups run
	// again; a leftover is the census's to find, since IF NOT EXISTS hides it
	// here.
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

	dsn := testdb.New(t, testDSN, "webpush_migration")
	require.NoError(t, db.Migrate(ctx, dsn, migrationsRoot, ModuleName))

	pool := testPoolFor(t, dsn)
	st := newStore(pool.Pool())
	p256dh, auth := newDeviceKeys(t)
	_, err := st.upsert(ctx, subscription{
		Endpoint: "https://push.example.test/p/withdata", P256DH: p256dh, Auth: auth,
		CustomerID: "cust_1", Fingerprint: "fp",
	})
	require.NoError(t, err)
	_, err = pool.Pool().Exec(ctx,
		`INSERT INTO webpush_claimed_event (event_id) VALUES ('order.placed:order_withdata')`)
	require.NoError(t, err)

	// Every step, not one: the table this pins is 000001's, and one step back
	// would be whichever migration is newest (D185).
	require.NoError(t, db.MigrateDown(ctx, dsn, migrationsRoot, ModuleName, 0),
		"the rollback has to work with rows in the tables, not only on empty ones")
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
//
// The handler may run on two goroutines at once — the bus delivers each event
// in its own — so what it received is read and written under a lock.
type pushServer struct {
	*httptest.Server
	status int

	mu       sync.Mutex
	requests []*http.Request
	bodies   [][]byte
}

// received returns the requests the service has answered so far.
func (p *pushServer) received() []*http.Request {
	p.mu.Lock()
	defer p.mu.Unlock()

	return slices.Clone(p.requests)
}

// newPushServer starts a fake push service.
func newPushServer(t *testing.T, status int) *pushServer {
	t.Helper()

	p := &pushServer{status: status}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		p.mu.Lock()
		p.requests = append(p.requests, r.Clone(context.Background()))
		p.bodies = append(p.bodies, body)
		p.mu.Unlock()
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

// placedEvents numbers the events placedEvent builds.
var placedEvents atomic.Int64

// placedEvent builds an order.placed event for an order placed now.
//
// Each carries an id of its own, as two orders do: the handler pushes an id
// once (ADR 0389), so two events that shared one would be one event.
func placedEvent(customerID string) eventbus.Event {
	n := placedEvents.Add(1)

	return eventbus.Event{
		ID:   fmt.Sprintf("order.placed:order_%d", n),
		Name: orderPlacedEvent,
		Data: map[string]any{
			fieldOrderID:    fmt.Sprintf("order_%d", n),
			fieldDisplayID:  "1001",
			fieldCustomerID: customerID,
			fieldItemCount:  "2",
			fieldPlacedAt:   time.Now().UTC().Format(time.RFC3339Nano),
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
	assert.Empty(t, push.received(), "and it must not be pushed to first")
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

	requests := push.received()
	require.Len(t, requests, 1)
	got := requests[0]
	assert.Equal(t, "aes128gcm", got.Header.Get("Content-Encoding"))
	assert.Equal(t, "application/octet-stream", got.Header.Get("Content-Type"))
	assert.NotEmpty(t, got.Header.Get("TTL"), "RFC 8030 requires TTL; without it the answer is 400")
	assert.NotEmpty(t, got.Header.Get("Topic"),
		"the topic lets the push service replace a push it has not handed over")
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

	assert.Empty(t, push.received(), "an order with no customer must reach no device")
}

// TestAFailedPushDoesNotFailTheEvent proves a failed push asks for no retry.
//
// The bus calls a handler again on an error (ADR 0240), and after the record a
// call pushes nothing (ADR 0389), so a failed push is retried by nobody; an
// error here would only cost two calls that do nothing. The order is already
// written; a courtesy notification is not worth more.
func TestAFailedPushDoesNotFailTheEvent(t *testing.T) {
	st := freshStore(t)
	push := newPushServer(t, http.StatusInternalServerError)
	m := newTestModule(t, st)

	device(t, st, push.URL+"/p/broken", "cust_1", m.opts.fingerprint)

	assert.NoError(t, m.onOrderPlaced(t.Context(), placedEvent("cust_1")),
		"a failed push must not ask the event bus for a redelivery")
}

// --- helpers ----------------------------------------------------------------

// migrationVersions lists the versions of the plugin's migration files, in
// order. They are read from the files rather than written as numbers, which
// would be the size of the tree the day they were written.
func migrationVersions(t *testing.T) []uint {
	t.Helper()

	names, err := fs.Glob(migrationsRoot, "*.up.sql")
	require.NoError(t, err)
	require.NotEmpty(t, names, "the plugin carries no up migration")

	versions := make([]uint, 0, len(names))
	for _, name := range names {
		versions = append(versions, migrationVersion(t, name))
	}
	slices.Sort(versions)

	return versions
}

// migrationVersion reads the version a migration file's name starts with.
func migrationVersion(t *testing.T, name string) uint {
	t.Helper()

	number, _, found := strings.Cut(name, "_")
	require.True(t, found, "%s is not named NNNNNN_description.{up,down}.sql", name)
	v, err := strconv.ParseUint(number, 10, 32)
	require.NoError(t, err, "%s does not start with its version", name)

	return uint(v)
}

// migrationsUpTo is the plugin's migration files up to and including version,
// for a migrator that should stop there.
func migrationsUpTo(t *testing.T, version uint) fs.FS {
	t.Helper()

	names, err := fs.Glob(migrationsRoot, "*.sql")
	require.NoError(t, err)

	out := fstest.MapFS{}
	for _, name := range names {
		if migrationVersion(t, name) > version {
			continue
		}
		body, err := fs.ReadFile(migrationsRoot, name)
		require.NoError(t, err)
		out[name] = &fstest.MapFile{Data: body}
	}

	return out
}

// rollBackTo rolls the plugin's schema back to the given version.
//
// MigrateDown counts STEPS, and a step count written as a number is the size of
// the tree the day it was written: the next migration makes it undo the wrong
// one (D185). The count is derived from the version the database is at, and a
// database already there is left alone, since zero steps means every one.
func rollBackTo(ctx context.Context, t *testing.T, dsn string, version uint) {
	t.Helper()

	current, dirty, err := db.Version(ctx, dsn, ModuleName)
	require.NoError(t, err)
	require.False(t, dirty)
	require.GreaterOrEqual(t, current, version)
	if current == version {
		return
	}
	require.NoError(t, db.MigrateDown(ctx, dsn, migrationsRoot, ModuleName, int(current-version)),
		"the migrations after %d could not be rolled back", version)
}

// schemaRelations lists every table, index, sequence and view in the pool's
// current schema by name, leaving out the migration ledger, which a rollback
// keeps.
func schemaRelations(ctx context.Context, t *testing.T, pool *db.Pool, ledger string) map[string]bool {
	t.Helper()

	rows, err := pool.Pool().Query(ctx, `
		SELECT c.relname
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = current_schema()`)
	require.NoError(t, err)
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)

	out := make(map[string]bool, len(names))
	for _, name := range names {
		if !strings.HasPrefix(name, ledger) {
			out[name] = true
		}
	}

	return out
}

// sortedNames is the set's names in order, for a comparison that prints them.
func sortedNames(set map[string]bool) []string {
	return slices.Sorted(maps.Keys(set))
}

// tableExists reports whether the table exists in the pool's database.
func tableExists(ctx context.Context, t *testing.T, pool *db.Pool, table string) bool {
	t.Helper()

	var exists bool
	require.NoError(t, pool.Pool().QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_name = $1 AND table_schema = current_schema()
		)`, table).Scan(&exists))

	return exists
}

// testPoolFor opens a pool against one of the temporary databases.
func testPoolFor(t *testing.T, dsn string) *db.Pool {
	t.Helper()

	pool, err := db.New(t.Context(), db.DefaultConfig(dsn), nil)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return pool
}
