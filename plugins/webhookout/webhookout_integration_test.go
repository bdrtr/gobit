//go:build integration

// The tests in this file need a real PostgreSQL (and therefore Docker) and a
// real HTTP server; they are behind the `integration` tag so `make test` stays
// fast. Run them with: make test-integration
//
// # Why a fake sender would not have proved this
//
// An outbound webhook is a request that leaves the process. The things that can
// be wrong with it are the things a fake cannot have: whether the bytes that
// were signed are the bytes that arrived, whether the headers survive Go's
// canonicalization, whether a receiver's refusal is read as a refusal, and
// whether a retry actually re-sends. Every test here runs against an
// httptest.Server that verifies the signature the way a customer's receiver
// would — with the standard library and the documented rule, not by calling
// [Sign].
//
// # And why a real database
//
// The delivery queue's whole behaviour is in SQL: the fan-out is one INSERT ...
// SELECT, the idempotency is a unique constraint, the claim is an UPDATE with a
// lease, and giving up is a CASE inside the failure statement. None of that
// exists in Go, so none of it can be tested in Go.
//
// This file also carries the migration's rollback certification. THE
// ARCHITECTURE GATES DO NOT COVER IT: both rollback tests walk moduleNames(t),
// which reads internal/modules/ only, so a plugin that brings a table has its
// up/down pair certified by nothing. ADR 0018 made that a requirement rather
// than a nicety.
package webhookout

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/eventbus"
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
		tcpostgres.WithDatabase("gobit_webhookout"),
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
		fmt.Fprintf(os.Stderr, "the webhookout schema could not be applied: %v\n", err)

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

// --- the harness ------------------------------------------------------------

// freshModule empties both tables and returns a module wired to them.
func freshModule(t *testing.T) *webhookModule {
	t.Helper()

	_, err := testPool.Pool().Exec(t.Context(),
		`TRUNCATE webhook_delivery, webhook_endpoint`)
	require.NoError(t, err)

	m := newModule(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelError,
	})))
	m.store = newStore(testPool.Pool())

	return m
}

// receiver is a test server standing in for a customer's endpoint.
//
// It verifies the signature the way the documentation tells a receiver to,
// using only the standard library. Calling [VerifySignature] would have made
// this agree with any change to the scheme, including one that stopped signing
// a field.
type receiver struct {
	server *httptest.Server

	mu       sync.Mutex
	requests []receivedRequest
	// status is what the next request is answered with.
	status int
}

// receivedRequest is one delivery as the receiver saw it.
type receivedRequest struct {
	Headers   http.Header
	Body      []byte
	Verified  bool
	Envelope  body
	Delivered time.Time
}

// newReceiver starts a receiver that answers 200 and verifies every signature.
func newReceiver(t *testing.T, secret func() string) *receiver {
	t.Helper()

	r := &receiver{status: http.StatusOK}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		payload := make([]byte, req.ContentLength)
		_, _ = req.Body.Read(payload)

		var envelope body
		_ = json.Unmarshal(payload, &envelope)

		r.mu.Lock()
		status := r.status
		r.requests = append(r.requests, receivedRequest{
			Headers:   req.Header.Clone(),
			Body:      payload,
			Verified:  independentVerify(secret(), req.Header, payload),
			Envelope:  envelope,
			Delivered: time.Now(),
		})
		r.mu.Unlock()

		w.WriteHeader(status)
		_, _ = w.Write([]byte("thanks"))
	}))
	t.Cleanup(r.server.Close)

	return r
}

// answerWith changes what the receiver returns from now on.
func (r *receiver) answerWith(status int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.status = status
}

// seen returns the requests received so far.
func (r *receiver) seen() []receivedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]receivedRequest(nil), r.requests...)
}

// independentVerify is the receiver's own implementation of the check.
//
// It is written from the rule in signature.go's documentation — length-prefixed
// join of six parts, HMAC-SHA256, standard base64, "v1=" in front — with
// nothing from this package. If the two ever disagree, the documented scheme
// and the shipped one have parted company, and the documented one is the
// contract.
func independentVerify(secret string, headers http.Header, payload []byte) bool {
	var signed strings.Builder
	for _, part := range []string{
		"v1",
		headers.Get(HeaderTimestamp),
		headers.Get(HeaderDelivery),
		headers.Get(HeaderEvent),
		headers.Get(HeaderAttempt),
		string(payload),
	} {
		signed.WriteString(strconv.Itoa(len(part)) + ":" + part)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signed.String()))
	want := "v1=" + base64.StdEncoding.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(want), []byte(headers.Get(HeaderSignature)))
}

// register creates a receiver row through the store, the way the admin endpoint
// does.
func register(t *testing.T, m *webhookModule, target string, topics ...string) endpoint {
	t.Helper()

	validated, err := validateTopics(topics)
	require.NoError(t, err)

	e, err := m.store.createEndpoint(t.Context(), target, validated, "an integration test")
	require.NoError(t, err)

	return e
}

// makeDue clears the backoff so the next pass picks the row up.
//
// The ladder's first step is a minute and its last is six hours; a test that
// waited them out would take a day. What is proved here is that a FAILED
// delivery comes back and is retried — the arithmetic of the wait itself is
// pinned separately, and computed rather than asserted, in
// TestTheLadderDoublesAndThenStops.
func makeDue(t *testing.T) {
	t.Helper()

	_, err := testPool.Pool().Exec(t.Context(),
		`UPDATE webhook_delivery SET next_attempt_at = now() - interval '1 second'
		 WHERE delivered_at IS NULL AND dead_lettered_at IS NULL`)
	require.NoError(t, err)
}

// runPass makes one delivery pass with a realistic budget.
func runPass(t *testing.T, m *webhookModule) error {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), maxRun)
	defer cancel()

	return m.deliverPass(ctx)
}

// --- the migration ----------------------------------------------------------

// TestTheMigrationIsReallyReversible is the gate no architecture test provides.
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

	pool := testPoolFor(t, dsn)
	for _, table := range []string{"webhook_endpoint", "webhook_delivery"} {
		var exists bool
		require.NoError(t, pool.Pool().QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.tables WHERE table_name = $1
			)`, table).Scan(&exists))
		assert.False(t, exists, "%s has to be gone after the rollback", table)
	}

	// Applying again proves the pair is a CYCLE rather than a one-way trip: a
	// down migration that leaves an index behind passes the check above and
	// fails here.
	require.NoError(t, db.Migrate(ctx, dsn, migrationsRoot, ModuleName),
		"the schema has to apply again after a rollback")
}

// TestTheMigrationIsReversibleWithDataInIt covers what the arch gate's own
// godoc admits it cannot: the repository's rollback gate runs against a fresh
// EMPTY container.
func TestTheMigrationIsReversibleWithDataInIt(t *testing.T) {
	ctx := t.Context()

	dsn := freshDatabase(t)
	require.NoError(t, db.Migrate(ctx, dsn, migrationsRoot, ModuleName))

	st := newStore(testPoolFor(t, dsn).Pool())
	e, err := st.createEndpoint(ctx, "https://receiver.test/hook",
		[]string{topicOrderPlaced}, "with data")
	require.NoError(t, err)
	_, err = st.enqueue(ctx, "evt_1", topicOrderPlaced, time.Now(),
		map[string]any{"order_id": "ord_1"}, nil)
	require.NoError(t, err)
	require.NotEmpty(t, e.ID)

	require.NoError(t, db.MigrateDown(ctx, dsn, migrationsRoot, ModuleName, 1),
		"the rollback has to work with rows in the tables, not only on empty ones")
}

// --- the delivery ------------------------------------------------------------

// TestASignedDeliveryReachesAReceiverThatCanVerifyIt is the whole feature in
// one test.
//
// An event goes onto the subscriber, a pass sends it, and a receiver that has
// only the shared secret and the documented rule verifies it. Everything else
// in this file is a way this can go wrong.
func TestASignedDeliveryReachesAReceiverThatCanVerifyIt(t *testing.T) {
	m := freshModule(t)

	var secret string
	rec := newReceiver(t, func() string { return secret })

	e := register(t, m, rec.server.URL, topicOrderPlaced)
	secret = e.Secret

	require.NoError(t, m.onEvent(t.Context(), eventbus.Event{
		ID:         "order.placed:ord_1",
		Name:       topicOrderPlaced,
		OccurredAt: time.Now().UTC().Truncate(time.Second),
		Data: map[string]any{
			"order_id":   "ord_1",
			"display_id": "1001",
			"total":      "12300",
		},
	}))

	require.NoError(t, runPass(t, m))

	seen := rec.seen()
	require.Len(t, seen, 1, "exactly one delivery had to arrive")

	got := seen[0]
	assert.True(t, got.Verified,
		"the receiver could not verify the signature with the secret it was issued.\n"+
			"Either the bytes signed are not the bytes sent, or the scheme in the "+
			"documentation is no longer the scheme in the code — and the documentation is "+
			"the contract a customer implements against.")

	assert.Equal(t, topicOrderPlaced, got.Headers.Get(HeaderEvent))
	assert.Equal(t, "1", got.Headers.Get(HeaderAttempt))
	assert.True(t, strings.HasPrefix(got.Headers.Get(HeaderDelivery), deliveryPrefix))
	assert.NotEmpty(t, got.Headers.Get(HeaderTimestamp))

	assert.Equal(t, "order.placed:ord_1", got.Envelope.EventID)
	assert.Equal(t, "ord_1", got.Envelope.Data["order_id"])
	assert.Equal(t, "12300", got.Envelope.Data["total"])
	assert.Equal(t, got.Headers.Get(HeaderDelivery), got.Envelope.ID,
		"the delivery id in the body and in the header have to be the same value; a "+
			"receiver told to deduplicate on one of them must not be able to pick wrong")

	// The row is closed, so the next pass does not send it again.
	assert.Equal(t, 0, pendingCount(t))
}

// TestTheCustomerIDIsNotOnTheWire is the payload boundary, proved where it
// matters — ON THE WIRE, not in a unit.
//
// The unit test proves redact() drops the field. This proves nothing puts it
// back on the way out — the envelope is built in the sender, from stored
// columns, and a body assembled there could reintroduce it.
func TestTheCustomerIDIsNotOnTheWire(t *testing.T) {
	m := freshModule(t)

	var secret string
	rec := newReceiver(t, func() string { return secret })
	e := register(t, m, rec.server.URL, topicOrderPlaced)
	secret = e.Secret

	require.NoError(t, m.onEvent(t.Context(), eventbus.Event{
		ID:   "order.placed:ord_2",
		Name: topicOrderPlaced,
		Data: map[string]any{"order_id": "ord_2", "customer_id": "cus_leak"},
	}))
	require.NoError(t, runPass(t, m))

	seen := rec.seen()
	require.Len(t, seen, 1)

	assert.NotContains(t, string(seen[0].Body), "cus_leak",
		"a customer id left the installation in a webhook body. It is a bearer token "+
			"for /store/v1/customers/{id}, which is unauthenticated by decision, so the "+
			"receiver now has standing access to that customer's name, email and addresses.")
	assert.Equal(t, []string{"customer_id"}, seen[0].Envelope.Redacted,
		"the withholding has to be visible to the receiver")

	// It is not in the queue either. The redaction happens before the write, so
	// the value never enters a table that everyone with database access can read.
	var stored string
	require.NoError(t, testPool.Pool().QueryRow(t.Context(),
		`SELECT payload::text FROM webhook_delivery LIMIT 1`).Scan(&stored))
	assert.NotContains(t, stored, "cus_leak",
		"the redacted field was stored and only removed on the way out; a field that "+
			"never enters the table cannot leak from it either")
}

// TestAnUnsubscribedTopicIsNotDelivered is the fan-out's filter.
//
// A receiver that asked for order.placed must not be sent product.created. The
// filter is one array containment operator in the enqueue statement, which
// means an error in it would send EVERY event to EVERY receiver — a leak with
// no error and no log line.
func TestAnUnsubscribedTopicIsNotDelivered(t *testing.T) {
	m := freshModule(t)

	var secret string
	rec := newReceiver(t, func() string { return secret })
	e := register(t, m, rec.server.URL, topicOrderPlaced)
	secret = e.Secret

	require.NoError(t, m.onEvent(t.Context(), eventbus.Event{
		ID: "product.created:prd_1", Name: topicProductCreated,
		Data: map[string]any{"product_id": "prd_1"},
	}))
	require.NoError(t, runPass(t, m))

	assert.Empty(t, rec.seen(),
		"a receiver registered for %q was sent a %q delivery", topicOrderPlaced,
		topicProductCreated)
	assert.Equal(t, 0, deliveryCount(t),
		"no delivery row may even be written for a topic nobody asked for")

	// The same receiver DOES get the topic it asked for, which is what makes
	// the assertion above about the filter rather than about a broken sender.
	require.NoError(t, m.onEvent(t.Context(), eventbus.Event{
		ID: "order.placed:ord_3", Name: topicOrderPlaced,
		Data: map[string]any{"order_id": "ord_3"},
	}))
	require.NoError(t, runPass(t, m))
	assert.Len(t, rec.seen(), 1)
}

// TestAFailedDeliveryIsRetried is the retry, end to end.
//
// The first attempt is refused, the row goes back with its backoff, and the
// second attempt carries attempt 2 and the SAME delivery id — which is what
// lets a receiver that answered and lost the answer deduplicate.
func TestAFailedDeliveryIsRetried(t *testing.T) {
	m := freshModule(t)

	var secret string
	rec := newReceiver(t, func() string { return secret })
	e := register(t, m, rec.server.URL, topicOrderPlaced)
	secret = e.Secret

	rec.answerWith(http.StatusInternalServerError)

	require.NoError(t, m.onEvent(t.Context(), eventbus.Event{
		ID: "order.placed:ord_4", Name: topicOrderPlaced,
		Data: map[string]any{"order_id": "ord_4"},
	}))

	require.NoError(t, runPass(t, m), "a receiver refusing a delivery is not a job failure; "+
		"only a dead letter is")
	require.Len(t, rec.seen(), 1)

	attempts, nextAt := deliveryState(t)
	assert.Equal(t, int64(1), attempts, "the failed attempt has to be counted")
	assert.True(t, nextAt.After(time.Now()),
		"a failed delivery has to wait out its backoff; a row that is due again "+
			"immediately fills every pass and starves the healthy deliveries behind it")

	// A pass while the backoff is running must not touch it. This is the half
	// of the ladder that keeps the queue moving.
	require.NoError(t, runPass(t, m))
	assert.Len(t, rec.seen(), 1, "the delivery was retried before its backoff elapsed")

	rec.answerWith(http.StatusOK)
	makeDue(t)
	require.NoError(t, runPass(t, m))

	seen := rec.seen()
	require.Len(t, seen, 2, "the delivery had to be retried once the backoff elapsed")

	assert.Equal(t, "2", seen[1].Headers.Get(HeaderAttempt))
	assert.Equal(t, seen[0].Headers.Get(HeaderDelivery), seen[1].Headers.Get(HeaderDelivery),
		"the delivery id changed between attempts. A receiver is told to deduplicate on "+
			"it, so a changing id makes every retry look like a new event — which is the "+
			"duplicate the id exists to prevent.")
	assert.True(t, seen[1].Verified, "a retry has to be signed too, with its own timestamp")
	assert.NotEqual(t, seen[0].Headers.Get(HeaderSignature), seen[1].Headers.Get(HeaderSignature),
		"the two attempts carry the same signature, so the timestamp is not really "+
			"per-attempt and a receiver's freshness window would reject legitimate retries")

	assert.Equal(t, 0, pendingCount(t))
}

// TestGivingUpIsVisible is B12's rule, applied to a third party.
//
// A receiver that is never coming back must not be retried forever, and the
// giving up must not be silent. Both halves are asserted: the attempts stop at
// the ceiling, and the pass FAILS while the pile is not empty — which is the
// one channel that reaches `gobit jobs`.
func TestGivingUpIsVisible(t *testing.T) {
	m := freshModule(t)

	var secret string
	rec := newReceiver(t, func() string { return secret })
	e := register(t, m, rec.server.URL, topicOrderPlaced)
	secret = e.Secret

	rec.answerWith(http.StatusInternalServerError)

	require.NoError(t, m.onEvent(t.Context(), eventbus.Event{
		ID: "order.placed:ord_5", Name: topicOrderPlaced,
		Data: map[string]any{"order_id": "ord_5"},
	}))

	// One pass per allowed attempt, with the backoff cleared between them. The
	// waits themselves are pinned by TestTheLadderDoublesAndThenStops; what is
	// proved here is that the ceiling is REACHED and what happens at it.
	var lastErr error
	for range maxAttempts {
		lastErr = runPass(t, m)
		makeDue(t)
	}

	assert.Len(t, rec.seen(), int(maxAttempts),
		"the sender made a different number of attempts than the ceiling allows")

	require.Error(t, lastErr,
		"the pass reported success with a delivery given up on. A job that says \"ok\" "+
			"while an event nobody was told about sits undelivered is the write-only "+
			"ledger this repository already built once, in audit_log.")
	assert.Contains(t, lastErr.Error(), "GIVEN UP",
		"the failure has to say what happened; it is one line of `gobit jobs`")
	assert.Contains(t, lastErr.Error(), rec.server.URL,
		"the failure has to name the receiver that was not reached")

	// The alarm does not clear itself: the next pass fails too, with nothing
	// new having happened.
	assert.Error(t, runPass(t, m),
		"the pile cleared itself; the alarm is supposed to stand until a human acts")

	// And it is READABLE, with what a decision needs.
	report, err := m.store.deadLetters(t.Context(), 5)
	require.NoError(t, err)
	require.Equal(t, int64(1), report.Count)
	dead := report.Oldest[0]
	assert.Equal(t, maxAttempts, dead.Attempts)
	assert.Equal(t, http.StatusInternalServerError, dead.LastStatus,
		"the last status is the first thing a human needs: 500 is the receiver's own "+
			"bug, 404 is a wrong URL, 0 is a host that does not resolve")
	assert.Contains(t, dead.LastError, "500")
	assert.Equal(t, rec.server.URL, dead.URL)

	// The dead row leaves the delivery queue entirely, which is what lets the
	// deliveries behind it move.
	assert.Equal(t, 0, pendingCount(t))
}

// TestTheOffSwitchesWork is the operator half.
//
// A dead letter that only a human can clear needs a way for the human to clear
// it. Without these the alarm has no off switch that is not psql, which is the
// gap B12 shipped with and had to close the same day.
func TestTheOffSwitchesWork(t *testing.T) {
	m := freshModule(t)

	var secret string
	rec := newReceiver(t, func() string { return secret })
	e := register(t, m, rec.server.URL, topicOrderPlaced)
	secret = e.Secret
	rec.answerWith(http.StatusInternalServerError)

	require.NoError(t, m.onEvent(t.Context(), eventbus.Event{
		ID: "order.placed:ord_6", Name: topicOrderPlaced,
		Data: map[string]any{"order_id": "ord_6"},
	}))
	for range maxAttempts {
		_ = runPass(t, m)
		makeDue(t)
	}

	report, err := m.store.deadLetters(t.Context(), 5)
	require.NoError(t, err)
	require.Equal(t, int64(1), report.Count)
	id := report.Oldest[0].ID

	// A LIVE delivery cannot be redriven or discarded. Both act only on the
	// pile, because either verb on a delivery still being retried is a way to
	// drop an event nobody has given up on.
	live, err := m.store.enqueue(t.Context(), "order.placed:ord_7", topicOrderPlaced,
		time.Now(), map[string]any{"order_id": "ord_7"}, nil)
	require.NoError(t, err)
	require.Equal(t, int64(1), live)

	var liveID string
	require.NoError(t, testPool.Pool().QueryRow(t.Context(),
		`SELECT id FROM webhook_delivery WHERE event_id = 'order.placed:ord_7'`).Scan(&liveID))

	acted, err := m.store.discard(t.Context(), liveID)
	require.NoError(t, err)
	assert.False(t, acted, "a live delivery was discarded; that is a silent drop of an "+
		"event nobody has given up on")

	// Redrive puts the dead one back with a clean count — the meaning of the
	// verb. A row that kept its attempts would be back on the pile after one
	// more failure, which is a single retry wearing the name of a second chance.
	acted, err = m.store.redrive(t.Context(), id)
	require.NoError(t, err)
	require.True(t, acted)

	rec.answerWith(http.StatusOK)
	require.NoError(t, runPass(t, m))

	report, err = m.store.deadLetters(t.Context(), 5)
	require.NoError(t, err)
	assert.Equal(t, int64(0), report.Count, "the redriven delivery is still on the pile")

	// And discard removes a dead one for good. A NEW event is used rather than
	// the redriven one: the pass that proved the redrive worked also delivered
	// everything else that was due, which is the correct behaviour and leaves
	// nothing to kill.
	rec.answerWith(http.StatusInternalServerError)
	require.NoError(t, m.onEvent(t.Context(), eventbus.Event{
		ID: "order.placed:ord_11", Name: topicOrderPlaced,
		Data: map[string]any{"order_id": "ord_11"},
	}))
	for range maxAttempts {
		_ = runPass(t, m)
		makeDue(t)
	}
	report, err = m.store.deadLetters(t.Context(), 5)
	require.NoError(t, err)
	require.Positive(t, report.Count)

	acted, err = m.store.discard(t.Context(), report.Oldest[0].ID)
	require.NoError(t, err)
	assert.True(t, acted)
}

// TestTheSameEventTwiceIsOneDelivery is the subscriber's idempotency.
//
// The bus delivers at least once, and the order module publishes order.placed
// TWICE by design — directly and through the outbox relay — with the same event
// id. A sender without this constraint would POST every order to every receiver
// twice.
func TestTheSameEventTwiceIsOneDelivery(t *testing.T) {
	m := freshModule(t)

	var secret string
	rec := newReceiver(t, func() string { return secret })
	e := register(t, m, rec.server.URL, topicOrderPlaced)
	secret = e.Secret

	event := eventbus.Event{
		ID: "order.placed:ord_8", Name: topicOrderPlaced,
		Data: map[string]any{"order_id": "ord_8"},
	}

	require.NoError(t, m.onEvent(t.Context(), event))
	require.NoError(t, m.onEvent(t.Context(), event),
		"a repeat delivery of the same event must not be an error; the bus's contract "+
			"makes it normal")

	assert.Equal(t, 1, deliveryCount(t),
		"the same event produced two deliveries. The order module publishes order.placed "+
			"twice on purpose — the direct publish and the outbox relay — so every order "+
			"would reach every receiver twice.")

	require.NoError(t, runPass(t, m))
	assert.Len(t, rec.seen(), 1)
}

// TestTwoReceiversFailIndependently is the reason this queue is not the outbox.
//
// event_outbox is keyed on the EVENT and carries one attempts, one
// next_attempt_at and one dead_lettered_at, with no destination column. One
// event owed to two receivers, one of them down, has no expressible state in
// such a row: closing it loses the failure, leaving it open re-sends to the
// receiver that already answered 200.
func TestTwoReceiversFailIndependently(t *testing.T) {
	m := freshModule(t)

	var healthySecret, brokenSecret string
	healthy := newReceiver(t, func() string { return healthySecret })
	broken := newReceiver(t, func() string { return brokenSecret })

	healthySecret = register(t, m, healthy.server.URL, topicOrderPlaced).Secret
	brokenSecret = register(t, m, broken.server.URL, topicOrderPlaced).Secret
	broken.answerWith(http.StatusServiceUnavailable)

	require.NoError(t, m.onEvent(t.Context(), eventbus.Event{
		ID: "order.placed:ord_9", Name: topicOrderPlaced,
		Data: map[string]any{"order_id": "ord_9"},
	}))
	require.NoError(t, runPass(t, m))

	assert.Len(t, healthy.seen(), 1)
	assert.Len(t, broken.seen(), 1)

	// The healthy one is CLOSED and the broken one is not. That is the state
	// one outbox row cannot hold.
	makeDue(t)
	require.NoError(t, runPass(t, m))

	assert.Len(t, healthy.seen(), 1,
		"the receiver that already accepted the delivery was sent it again because the "+
			"other receiver failed")
	assert.Len(t, broken.seen(), 2, "the failing receiver has to be retried on its own")

	// Each secret is its own: the delivery meant for one must not verify at the
	// other, which is what stops a captured delivery being replayed sideways.
	assert.True(t, healthy.seen()[0].Verified)
	assert.False(t, independentVerify(brokenSecret, healthy.seen()[0].Headers,
		healthy.seen()[0].Body),
		"one receiver's secret verified another receiver's delivery")
}

// TestADeliveryToAVanishedReceiverIsCounted closes the hole the claim's join
// opens.
//
// Deleting a receiver leaves its undelivered rows behind on purpose — the
// record of what was never sent outlives the integration. Those rows can never
// be claimed again, because the claim joins to the endpoint for its secret, so
// without a count they would be due forever and invisible forever.
func TestADeliveryToAVanishedReceiverIsCounted(t *testing.T) {
	m := freshModule(t)

	var secret string
	rec := newReceiver(t, func() string { return secret })
	e := register(t, m, rec.server.URL, topicOrderPlaced)
	secret = e.Secret
	rec.answerWith(http.StatusInternalServerError)

	require.NoError(t, m.onEvent(t.Context(), eventbus.Event{
		ID: "order.placed:ord_10", Name: topicOrderPlaced,
		Data: map[string]any{"order_id": "ord_10"},
	}))
	require.NoError(t, runPass(t, m))

	removed, err := m.store.deleteEndpoint(t.Context(), e.ID)
	require.NoError(t, err)
	require.True(t, removed)

	makeDue(t)
	err = runPass(t, m)
	require.Error(t, err,
		"a pending delivery whose receiver is gone can never be sent and nothing "+
			"reported it")
	assert.Contains(t, err.Error(), "no longer exists")

	assert.Empty(t, rec.seen()[1:], "no further attempt may be made to a deleted receiver")
}

// --- helpers ----------------------------------------------------------------

// deliveryCount reports how many delivery rows exist.
func deliveryCount(t *testing.T) int {
	t.Helper()

	var n int
	require.NoError(t, testPool.Pool().
		QueryRow(t.Context(), `SELECT count(*) FROM webhook_delivery`).Scan(&n))

	return n
}

// pendingCount reports how many deliveries are still owed.
func pendingCount(t *testing.T) int {
	t.Helper()

	var n int
	require.NoError(t, testPool.Pool().QueryRow(t.Context(),
		`SELECT count(*) FROM webhook_delivery
		 WHERE delivered_at IS NULL AND dead_lettered_at IS NULL`).Scan(&n))

	return n
}

// deliveryState reads the single delivery row's attempt count and due instant.
func deliveryState(t *testing.T) (attempts int64, nextAt time.Time) {
	t.Helper()

	require.NoError(t, testPool.Pool().QueryRow(t.Context(),
		`SELECT attempts, next_attempt_at FROM webhook_delivery LIMIT 1`).
		Scan(&attempts, &nextAt))

	return attempts, nextAt
}

// freshDatabase creates a database of its own for a migration test.
//
// The rollback tests cannot run on the shared schema: rolling it back would
// pull both tables out from under every other test in the file.
func freshDatabase(t *testing.T) string {
	t.Helper()

	name := fmt.Sprintf("webhookout_migration_%d", time.Now().UnixNano())
	_, err := testPool.Pool().Exec(t.Context(), `CREATE DATABASE `+name)
	require.NoError(t, err)

	t.Cleanup(func() {
		// The drop runs on a context detached from the test's, which is already
		// canceled by the time cleanup runs.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := testPool.Pool().Exec(ctx,
			`DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`); err != nil {
			t.Logf("the temporary database %s could not be dropped: %v", name, err)
		}
	})

	return replaceDatabase(testDSN, name)
}

// replaceDatabase swaps the database name in a DSN.
func replaceDatabase(dsn, name string) string {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	parsed.Path = "/" + name

	return parsed.String()
}

// testPoolFor opens a pool on another database and closes it with the test.
func testPoolFor(t *testing.T, dsn string) *db.Pool {
	t.Helper()

	pool, err := db.New(t.Context(), db.DefaultConfig(dsn), nil)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return pool
}
