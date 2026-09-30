//go:build integration

package webhookout

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/eventbus"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/testdb"
)

// upMigrations counts the plugin's up files, so a test states the version as
// the tree has it rather than as a number that stops being true (D148's shape).
func upMigrations(t *testing.T) int {
	t.Helper()

	ups, err := fs.Glob(migrationsRoot, "*.up.sql")
	require.NoError(t, err)
	require.NotEmpty(t, ups)

	return len(ups)
}

// registerNarrowed creates a receiver with filters and fields, validated as the
// admin endpoint validates them.
func registerNarrowed(
	t *testing.T, m *webhookModule, target string, topics []string, filters topicFilters, fields topicFields,
) endpoint {
	t.Helper()

	validated, err := validateTopics(topics)
	require.NoError(t, err)
	filters, fields, err = validateNarrowing(validated, filters, fields)
	require.NoError(t, err)
	e, err := m.store.createEndpoint(t.Context(), target, validated, filters, fields, "narrowed")
	require.NoError(t, err)

	return e
}

// owed counts the deliveries written for a receiver.
func owed(t *testing.T, endpointID string) int {
	t.Helper()

	var n int
	require.NoError(t, testPool.Pool().QueryRow(t.Context(),
		`SELECT count(*) FROM webhook_delivery WHERE endpoint_id = $1`, endpointID).Scan(&n))

	return n
}

// cartEvent is a cart.created event in a region, or with no region.
func cartEvent(id, region string) eventbus.Event {
	data := map[string]any{"cart_id": id, "currency_code": "TRY"}
	if region != "" {
		data["region_id"] = region
	}

	return eventbus.Event{ID: "cart.created:" + id, Name: topicCartCreated, Data: data}
}

// TestAFilterDecidesWhichEventsAReceiverIsOwed is ADR 0218 in the enqueue
// statement: a filtered receiver is owed only the events whose field carries
// one of its values, every named field has to match, an event without the field
// matches nothing, and a receiver beside it that filters nothing is owed all.
func TestAFilterDecidesWhichEventsAReceiverIsOwed(t *testing.T) {
	m := freshModule(t)
	regional := registerNarrowed(t, m, "https://regional.test/hook", []string{topicCartCreated},
		topicFilters{topicCartCreated: {"region_id": {"reg_1", "reg_2"}}}, nil)
	both := registerNarrowed(t, m, "https://both.test/hook", []string{topicCartCreated},
		topicFilters{topicCartCreated: {"region_id": {"reg_1"}, "currency_code": {"EUR"}}}, nil)
	everything := register(t, m, "https://everything.test/hook", topicCartCreated)

	for _, e := range []eventbus.Event{
		cartEvent("cart_1", "reg_1"), cartEvent("cart_2", "reg_2"),
		cartEvent("cart_3", "reg_3"), cartEvent("cart_4", ""),
	} {
		require.NoError(t, m.onEvent(t.Context(), e))
	}

	assert.Equal(t, 2, owed(t, regional.ID), "reg_1 and reg_2, not reg_3 nor a cart with no region")
	assert.Zero(t, owed(t, both.ID), "the region matched and the currency did not")
	assert.Equal(t, 4, owed(t, everything.ID))
}

// TestAFieldListNarrowsTheBodyThatIsQueued: the receiver is sent the fields it
// named, the redaction still says what was withheld, and the queued row holds
// the narrowed body, so a redrive sends the same.
func TestAFieldListNarrowsTheBodyThatIsQueued(t *testing.T) {
	m := freshModule(t)
	var secret string
	rec := newReceiver(t, func() string { return secret })
	e := registerNarrowed(t, m, rec.server.URL, []string{topicOrderPlaced, topicCartCreated}, nil,
		topicFields{topicOrderPlaced: {"order_id", "total"}})
	secret = e.Secret

	require.NoError(t, m.onEvent(t.Context(), eventbus.Event{
		ID: "order.placed:ord_9", Name: topicOrderPlaced,
		Data: map[string]any{"order_id": "ord_9", "total": "1500", "region_id": "reg_1", "customer_id": "cus_1"},
	}))
	require.NoError(t, m.onEvent(t.Context(), cartEvent("cart_9", "reg_1")))
	require.NoError(t, runPass(t, m))

	seen := rec.seen()
	require.Len(t, seen, 2)
	bodies := map[string]body{}
	for _, s := range seen {
		assert.True(t, s.Verified, "the narrowed body is the signed body")
		bodies[s.Envelope.Event] = s.Envelope
	}
	assert.Equal(t, map[string]any{"order_id": "ord_9", "total": "1500"}, bodies[topicOrderPlaced].Data)
	assert.Equal(t, []string{"customer_id"}, bodies[topicOrderPlaced].Redacted)
	assert.Len(t, bodies[topicCartCreated].Data, 3, "a topic with no field list is sent whole")

	var stored string
	require.NoError(t, testPool.Pool().QueryRow(t.Context(),
		`SELECT payload::text FROM webhook_delivery WHERE event_name = $1`, topicOrderPlaced).Scan(&stored))
	assert.JSONEq(t, `{"order_id": "ord_9", "total": "1500"}`, stored)
}

// adminRouter is the plugin's admin surface with every scope granted.
func adminRouter(m *webhookModule) http.Handler {
	r := chi.NewRouter()
	m.Routes(r)

	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "user_webhook", Kind: "user", Scopes: []string{corehttp.ScopeAdmin},
		})
		r.ServeHTTP(w, req.WithContext(ctx))
	})
}

// call sends one admin request.
func call(t *testing.T, h http.Handler, method, path, payload string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(payload))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

// TestAReceiverIsChangedInPlace: a PATCH replaces what it names and keeps the
// rest, keeps the secret, takes effect for the events after it, and refuses a
// topic taken away while a filter still names it.
func TestAReceiverIsChangedInPlace(t *testing.T) {
	m := freshModule(t)
	h := adminRouter(m)
	created := call(t, h, http.MethodPost, "/admin/v1/webhooks", `{"url":"https://patch.test/hook",
		"topics":["cart.created","order.placed"],"description":"before"}`)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var registered struct {
		Data createResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &registered))
	id := registered.Data.ID

	require.NoError(t, m.onEvent(t.Context(), cartEvent("cart_a", "reg_2")))
	patched := call(t, h, http.MethodPatch, "/admin/v1/webhooks/"+id,
		`{"filters":{"cart.created":{"region_id":["reg_1"]}}}`)
	require.Equal(t, http.StatusOK, patched.Code, patched.Body.String())
	var changed struct {
		Data endpointResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(patched.Body.Bytes(), &changed))
	assert.Equal(t, "before", changed.Data.Description, "what the request leaves out is kept")
	assert.Equal(t, []string{topicCartCreated, topicOrderPlaced}, changed.Data.Topics)
	assert.NotContains(t, patched.Body.String(), registered.Data.Secret, "the secret is never shown again")

	require.NoError(t, m.onEvent(t.Context(), cartEvent("cart_b", "reg_2")))
	require.NoError(t, m.onEvent(t.Context(), cartEvent("cart_c", "reg_1")))
	assert.Equal(t, 2, owed(t, id), "the delivery written before the change stays; after it, reg_1 alone")

	listed := call(t, h, http.MethodGet, "/admin/v1/webhooks/", "")
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	var listing endpointListResponse
	require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &listing))
	require.Len(t, listing.Data, 1)
	assert.Equal(t, topicFilters{topicCartCreated: {"region_id": {"reg_1"}}}, listing.Data[0].Filters)
	assert.Equal(t, TopicFields, listing.TopicFields, "the names a filter can use travel with the listing")

	var secret string
	require.NoError(t, testPool.Pool().QueryRow(t.Context(),
		`SELECT secret FROM webhook_endpoint WHERE id = $1`, id).Scan(&secret))
	assert.Equal(t, registered.Data.Secret, secret)

	dropped := call(t, h, http.MethodPatch, "/admin/v1/webhooks/"+id, `{"topics":["order.placed"]}`)
	assert.Equal(t, http.StatusUnprocessableEntity, dropped.Code, dropped.Body.String())
	assert.Contains(t, dropped.Body.String(), "not among")
	droppedWithFilters := call(t, h, http.MethodPatch, "/admin/v1/webhooks/"+id,
		`{"topics":["order.placed"],"filters":{}}`)
	assert.Equal(t, http.StatusOK, droppedWithFilters.Code, droppedWithFilters.Body.String())

	nothing := call(t, h, http.MethodPatch, "/admin/v1/webhooks/"+id, `{}`)
	assert.Equal(t, http.StatusUnprocessableEntity, nothing.Code)
	missing := call(t, h, http.MethodPatch, "/admin/v1/webhooks/whe_MISSING", `{"description":"x"}`)
	assert.Equal(t, http.StatusNotFound, missing.Code)
	unknown := call(t, h, http.MethodPatch, "/admin/v1/webhooks/"+id, `{"url":"https://elsewhere.test"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, unknown.Code, "the URL is not changed in place")
}

// TestTheNarrowingRollsBackAndItsReceiversStay: rolling back migration 000002
// with a narrowed receiver in the table takes the two columns and keeps the
// receiver, which is then sent every event of its topics again.
func TestTheNarrowingRollsBackAndItsReceiversStay(t *testing.T) {
	ctx := t.Context()
	dsn := testdb.New(t, testDSN, "webhookout_narrowing")
	require.NoError(t, db.Migrate(ctx, dsn, migrationsRoot, ModuleName))
	pool := testPoolFor(t, dsn)
	st := newStore(pool.Pool())
	_, err := st.createEndpoint(ctx, "https://narrowed.test/hook", []string{topicCartCreated},
		topicFilters{topicCartCreated: {"region_id": {"reg_1"}}}, nil, "narrowed")
	require.NoError(t, err)

	// Back to 000001 whatever came after 000002: a step count written as a
	// number would roll back the newest migration instead (D185).
	current, _, err := db.Version(ctx, dsn, ModuleName)
	require.NoError(t, err)
	require.GreaterOrEqual(t, current, uint(2))
	require.NoError(t, db.MigrateDown(ctx, dsn, migrationsRoot, ModuleName, int(current)-1))

	version, _, err := db.Version(ctx, dsn, ModuleName)
	require.NoError(t, err)
	assert.Equal(t, uint(1), version)
	var receivers, columns int
	require.NoError(t, pool.Pool().QueryRow(ctx, `SELECT count(*) FROM webhook_endpoint`).Scan(&receivers))
	require.NoError(t, pool.Pool().QueryRow(ctx, `
		SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'webhook_endpoint' AND column_name IN ('filters', 'fields')`).Scan(&columns))
	assert.Equal(t, []int{1, 0}, []int{receivers, columns})
	require.NoError(t, db.Migrate(ctx, dsn, migrationsRoot, ModuleName), "and it applies again")
}

// TestARegistrationCarriesItsNarrowing: the admin endpoint stores what it was
// sent and answers with it, and the queue applies it.
func TestARegistrationCarriesItsNarrowing(t *testing.T) {
	m := freshModule(t)
	h := adminRouter(m)

	created := call(t, h, http.MethodPost, "/admin/v1/webhooks", `{"url":"https://narrow.test/hook",
		"topics":["cart.created"],"filters":{"cart.created":{"region_id":["reg_1"]}},
		"fields":{"cart.created":["cart_id"]}}`)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var registered struct {
		Data createResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &registered))
	assert.Equal(t, topicFilters{topicCartCreated: {"region_id": {"reg_1"}}}, registered.Data.Filters)
	assert.Equal(t, topicFields{topicCartCreated: {"cart_id"}}, registered.Data.Fields)

	require.NoError(t, m.onEvent(t.Context(), cartEvent("cart_x", "reg_2")))
	require.NoError(t, m.onEvent(t.Context(), cartEvent("cart_y", "reg_1")))
	var stored string
	require.NoError(t, testPool.Pool().QueryRow(t.Context(),
		`SELECT payload::text FROM webhook_delivery WHERE endpoint_id = $1`, registered.Data.ID).Scan(&stored))
	assert.JSONEq(t, `{"cart_id": "cart_y"}`, stored, "one delivery, reg_1's, with the listed field alone")

	refused := call(t, h, http.MethodPost, "/admin/v1/webhooks", `{"url":"https://narrow.test/hook",
		"topics":["cart.created"],"filters":{"cart.created":{"total":["1"]}}}`)
	assert.Equal(t, http.StatusUnprocessableEntity, refused.Code, refused.Body.String())
}

// TestAChangeIsCheckedAgainstTheRowAsItIs: a second transaction holds the
// receiver's row lock and takes a topic away; a change that filters that topic
// meanwhile is judged after the commit, against the topics the row then has, and
// is refused rather than stored over them.
func TestAChangeIsCheckedAgainstTheRowAsItIs(t *testing.T) {
	m := freshModule(t)
	h := adminRouter(m)
	e := register(t, m, "https://held.test/hook", topicCartCreated, topicOrderPlaced)

	tx, err := testPool.Pool().Begin(t.Context())
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(context.Background()) }()
	_, err = tx.Exec(t.Context(), `SELECT 1 FROM webhook_endpoint WHERE id = $1 FOR UPDATE`, e.ID)
	require.NoError(t, err)
	_, err = tx.Exec(t.Context(), `UPDATE webhook_endpoint SET topics = ARRAY['order.placed'] WHERE id = $1`, e.ID)
	require.NoError(t, err)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- call(t, h, http.MethodPatch, "/admin/v1/webhooks/"+e.ID,
			`{"filters":{"cart.created":{"region_id":["reg_1"]}}}`)
	}()
	var answer *httptest.ResponseRecorder
	select {
	case answer = <-done:
	case <-time.After(500 * time.Millisecond):
	}
	require.NoError(t, tx.Commit(t.Context()))
	if answer == nil {
		answer = <-done
	}

	assert.Equal(t, http.StatusUnprocessableEntity, answer.Code, answer.Body.String())
	var filters string
	require.NoError(t, testPool.Pool().QueryRow(t.Context(),
		`SELECT filters::text FROM webhook_endpoint WHERE id = $1`, e.ID).Scan(&filters))
	assert.JSONEq(t, `{}`, filters, "no filter for a topic the receiver no longer takes")
}

// TestTheNarrowingColumnsHoldObjects: the schema refuses a filter or a field
// list that is not a JSON object, for a writer around the admin endpoint.
func TestTheNarrowingColumnsHoldObjects(t *testing.T) {
	m := freshModule(t)
	e := register(t, m, "https://schema.test/hook", topicCartCreated)

	for constraint, update := range map[string]string{
		"webhook_endpoint_filters_object": `SET filters = '[]'`,
		"webhook_endpoint_fields_object":  `SET fields = '"cart_id"'`,
	} {
		_, err := testPool.Pool().Exec(t.Context(), `UPDATE webhook_endpoint `+update+` WHERE id = $1`, e.ID)
		var pgErr *pgconn.PgError
		require.ErrorAs(t, err, &pgErr, constraint)
		assert.Equal(t, constraint, pgErr.ConstraintName)
	}
}
