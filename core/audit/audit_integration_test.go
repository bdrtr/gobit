//go:build integration

// The tests in this file need a real PostgreSQL instance (and therefore
// Docker); they are separated behind the `integration` tag so that `make test`
// stays fast. To run them: make test-integration
//
// # Why the write can only be proven here
//
// [audit.Store.Write] is one Exec against one table, and every claim worth
// making about it is a claim about the SCHEMA it is aimed at: that the seven
// placeholders land in the seven columns the reader will later select, and that
// the values the middleware collects survive the constraints the migration
// declares. A fake pool proves the method calls Exec; it cannot prove the row
// that comes out is the row an operator can read during an incident.
//
// The design makes that gap expensive rather than merely untidy. The write's own
// doc says a failure here does not fail the request, and the middleware above it
// only logs — so a broken INSERT is silent in production too. An installation
// whose admin writes leave no trail is indistinguishable, from the outside, from
// one where nobody wrote anything. Nothing but a test that reads the row back
// tells the two apart before an incident does.
package audit_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/core/audit"
	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
)

const postgresImage = "postgres:16-alpine"

// testPool is the pool every test in this file shares.
var testPool *db.Pool

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres brings up a single Postgres container, applies the audit
// schema and runs every test against it. It is a separate function because
// os.Exit skips the defers.
func runWithPostgres(m *testing.M) int {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_audit"),
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

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "the connection string could not be read: %v\n", err)

		return 1
	}

	// The migration is applied through the package's own [audit.Migrations] and
	// [audit.MigrationOwner] rather than from the files on disk: those two are
	// what the composition root passes at startup, so a wrong prefix inside
	// Migrations would break the server and leave a test reading the directory
	// green.
	if err = db.Migrate(ctx, dsn, audit.Migrations(), audit.MigrationOwner); err != nil {
		fmt.Fprintf(os.Stderr, "the audit schema could not be applied: %v\n", err)

		return 1
	}

	testPool, err = db.New(ctx, db.DefaultConfig(dsn), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "the connection pool could not be opened: %v\n", err)

		return 1
	}
	defer testPool.Close()

	return m.Run()
}

// storedRow is one audit_log row as an incident reader would select it.
type storedRow struct {
	actorID   string
	actorKind string
	method    string
	path      string
	status    int
	requestID string
	createdAt time.Time
}

// readRow selects the row with this id, or fails the test when there is none.
func readRow(ctx context.Context, t *testing.T, id string) storedRow {
	t.Helper()

	var row storedRow
	err := testPool.Pool().QueryRow(ctx,
		`SELECT actor_id, actor_kind, method, path, status, request_id, created_at
		   FROM audit_log WHERE id = $1`, id).
		Scan(&row.actorID, &row.actorKind, &row.method, &row.path,
			&row.status, &row.requestID, &row.createdAt)
	require.NoError(t, err, "the audit row %q could not be read back", id)

	return row
}

// countRows returns how many audit rows carry this id.
func countRows(ctx context.Context, t *testing.T, id string) int {
	t.Helper()

	var n int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE id = $1`, id).Scan(&n))

	return n
}

// pool returns the raw pool the store is built over.
func pool() *pgxpool.Pool { return testPool.Pool() }

// TestAnAuditedWriteLandsInTheColumnsAnIncidentReaderSelects is the whole point
// of the table, and it is the one thing a fake can never show.
//
// The INSERT is positional: seven placeholders against seven column names, in an
// order written once and never read again by anybody. Nothing in Go connects
// $4 to `method` — swap two of the text arguments and the code still compiles,
// the write still succeeds, the middleware still logs nothing, and the table
// fills up with rows saying that somebody sent "/admin/v1/products" to the
// "POST" endpoint. That mistake is invisible until the day an operator opens the
// table during an incident, which is the only day it costs anything.
//
// So each field is given a value only IT could have, and each is asserted
// against the column that is supposed to hold it. created_at is checked too: it
// has no placeholder at all and comes from the column DEFAULT, so a migration
// that dropped the default would leave every row's time NULL and the scan would
// blow up here.
func TestAnAuditedWriteLandsInTheColumnsAnIncidentReaderSelects(t *testing.T) {
	ctx := context.Background()
	store := audit.NewStore(pool())

	before := time.Now().UTC().Add(-time.Second)
	entry := audit.Entry{
		ActorID:   "usr_who",
		ActorKind: "user",
		Method:    "PATCH",
		Path:      "/admin/v1/products/prd_touched",
		Status:    200,
		RequestID: "req_traceable",
	}

	require.NoError(t, store.Write(ctx, "aud_columns", entry))

	row := readRow(ctx, t, "aud_columns")
	assert.Equal(t, "usr_who", row.actorID, "the row has to name WHO made the change")
	assert.Equal(t, "user", row.actorKind, "a human and an api key are answered to differently")
	assert.Equal(t, "PATCH", row.method, "the method landed in another column; the trail says the wrong thing")
	assert.Equal(t, "/admin/v1/products/prd_touched", row.path,
		"the path landed in another column; the trail cannot say WHICH product was touched")
	assert.Equal(t, 200, row.status, "whether the change went through is half the answer")
	assert.Equal(t, "req_traceable", row.requestID,
		"without the request id the row cannot be tied to the log lines of the same request")
	assert.False(t, row.createdAt.IsZero(),
		"created_at comes from the column DEFAULT and not from the INSERT; without it the trail has no WHEN")
	assert.True(t, row.createdAt.After(before),
		"created_at has to be the moment of the write, not a leftover from the schema")
}

// TestAnAnonymousRefusedWriteIsStillRecorded closes the row shape the middleware
// produces most often when something is wrong.
//
// A write refused by the guard has NO principal: the middleware records
// [audit.Entry] with an empty ActorID and ActorKind and the status the guard
// returned. Both columns are NOT NULL, and an empty string is not null — but
// that is a claim about the SCHEMA, not about Go, and nothing else states it.
// Had either column been declared with a CHECK against the empty string the way
// method and path are, every 401 would fail to record and the table would hold
// only the successful writes: exactly the rows nobody needs during a break-in.
func TestAnAnonymousRefusedWriteIsStillRecorded(t *testing.T) {
	ctx := context.Background()
	store := audit.NewStore(pool())

	err := store.Write(ctx, "aud_anonymous", audit.Entry{
		Method: "DELETE",
		Path:   "/admin/v1/orders/ord_1",
		Status: 401,
	})

	require.NoError(t, err, "a refused write is the row an operator wants MOST; it cannot be the one that fails to store")

	row := readRow(ctx, t, "aud_anonymous")
	assert.Empty(t, row.actorID, "nobody was identified, and the row says so rather than inventing an actor")
	assert.Equal(t, 401, row.status)
}

// TestAStatusOutsideTheHTTPRangeIsRefusedAndSaysSo pins the boundary the
// migration draws (CHECK status BETWEEN 100 AND 599) from the Go side.
//
// The store does not validate the status itself — deliberately, since the
// middleware reads it off the response recorder and there is no second opinion
// to have. The database is therefore the only thing standing between the table
// and a nonsense status, and the boundary is exactly where a mistake lands: a
// recorder that never saw WriteHeader carries 0, and 0 is refused. What this
// test fixes is that the refusal ARRIVES — as an error carrying
// [audit.CodeWriteFailed] — instead of being swallowed into a silently missing
// row, and that the two legal ends of the range are not refused with it.
//
// The pair matters more than either half. A CHECK written `BETWEEN 100 AND 599`
// is one keystroke away from `BETWEEN 100 AND 599` exclusive of its ends in a
// hand-rolled rewrite, and a 599 that stopped being storable would drop the
// upstream-timeout rows — the ones an incident is about.
func TestAStatusOutsideTheHTTPRangeIsRefusedAndSaysSo(t *testing.T) {
	ctx := context.Background()
	store := audit.NewStore(pool())

	accepted := []int{100, 599}
	for _, status := range accepted {
		id := fmt.Sprintf("aud_status_ok_%d", status)
		require.NoError(t, store.Write(ctx, id, audit.Entry{
			ActorID: "usr_1", ActorKind: "user",
			Method: "POST", Path: "/admin/v1/products", Status: status,
		}), "%d is a legal HTTP status and the table has to hold it", status)
		assert.Equal(t, status, readRow(ctx, t, id).status)
	}

	refused := []int{0, 99, 600}
	for _, status := range refused {
		id := fmt.Sprintf("aud_status_bad_%d", status)
		err := store.Write(ctx, id, audit.Entry{
			ActorID: "usr_1", ActorKind: "user",
			Method: "POST", Path: "/admin/v1/products", Status: status,
		})

		require.Error(t, err, "%d is not an HTTP status; the row must not be stored as though it were", status)
		assert.Equal(t, audit.CodeWriteFailed, errors.CodeOf(err),
			"the operator greps the log for one code when a write left no trail")
		assert.Contains(t, err.Error(), "/admin/v1/products",
			"the error has to name the request whose trail was lost; a bare constraint name identifies nothing")
		assert.Zero(t, countRows(ctx, t, id),
			"the write was refused, so no half-row may be left behind under that id")
	}
}

// seedRows writes n audit rows for the given actor and path, oldest first.
//
// The ids are zero-padded so that lexicographic order matches insertion order:
// created_at is the primary sort key and rows written in the same microsecond
// fall back to the id, which is exactly the tie the keyset boundary has to
// survive.
func seedRows(ctx context.Context, t *testing.T, prefix, actorID, path string, n int) {
	t.Helper()

	store := audit.NewStore(pool())
	for i := range n {
		require.NoError(t, store.Write(ctx, fmt.Sprintf("%s_%04d", prefix, i), audit.Entry{
			ActorID: actorID, ActorKind: "user",
			Method: "POST", Path: path, Status: 200,
			RequestID: fmt.Sprintf("req_%04d", i),
		}))
	}
}

// planOf returns the EXPLAIN output of a query as one string.
func planOf(ctx context.Context, t *testing.T, sql string, args ...any) string {
	t.Helper()

	rows, err := pool().Query(ctx, "EXPLAIN (COSTS OFF) "+sql, args...)
	require.NoError(t, err)

	defer rows.Close()

	var plan strings.Builder

	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		plan.WriteString(line)
		plan.WriteString("\n")
	}

	require.NoError(t, rows.Err())

	return plan.String()
}

// TestEveryListingShapeReachesAnIndex is the claim this package has already got
// wrong once, so it is measured rather than written down.
//
// [audit.Store.List] answers three questions and the schema carries three
// indexes, one per question. Nothing in Go connects them: the SQL orders by
// created_at while two of the indexes lead with another column, and whether the
// planner can use them at all depends on the column ORDER inside each index and
// on the row comparison in the keyset boundary. A wrong answer here is silent —
// the endpoint returns the right rows either way — and it only shows up as a
// sequential scan of an append-only log that grows forever.
//
// This repository has shipped a godoc claiming "the index is used" that turned
// out to be false when it was finally measured. That is why the claim lives in
// EXPLAIN output and not in a sentence.
//
// The rows seeded below are what makes the plan meaningful: on an empty table
// PostgreSQL prefers a sequential scan no matter what indexes exist, so a test
// that seeded nothing would prove the opposite of what it claims to.
func TestEveryListingShapeReachesAnIndex(t *testing.T) {
	ctx := context.Background()
	seedRows(ctx, t, "aud_plan_a", "usr_planned", "/admin/v1/planned", 300)
	seedRows(ctx, t, "aud_plan_b", "usr_other", "/admin/v1/other", 300)

	require.NoError(t, analyzeAuditLog(ctx))

	for name, tc := range map[string]struct {
		actorID, path string
		index         string
		why           string
	}{
		"the recent-activity question": {
			index: "audit_log_created_at_idx",
			why: "an operator asks what happened most recently BEFORE they know whose " +
				"or which; without this index that is a scan and a sort of the whole log",
		},
		"what did this person do": {
			actorID: "usr_planned",
			index:   "audit_log_actor_idx",
			why:     "the actor index leads with actor_id and carries created_at second",
		},
		"what happened to this endpoint": {
			path:  "/admin/v1/planned",
			index: "audit_log_path_idx",
			why:   "the path index leads with path and carries created_at second",
		},
	} {
		t.Run(name, func(t *testing.T) {
			plan := planOf(ctx, t, audit.ListSQLForTest(),
				tc.actorID, tc.path, nil, "", 50)

			assert.Contains(t, plan, tc.index,
				"the %q shape did not reach %s.\n%s\nPlan:\n%s",
				name, tc.index, tc.why, plan)
			assert.NotContains(t, plan, "Seq Scan",
				"the %q shape fell back to a sequential scan of the whole log.\nPlan:\n%s",
				name, plan)
		})
	}
}

// TestTheKeysetBoundaryIsAnIndexConditionAndNotAFilter pins the SHAPE of the
// paging comparison, which is the difference between a cheap deep page and an
// expensive one.
//
// The boundary is written as a ROW comparison, (created_at, id) < (a, b), which
// matches the index's own column order and becomes an Index Cond. Spelled the
// equivalent way — "created_at < a OR (created_at = a AND id < b)" — it is the
// same truth and a worse plan: the OR cannot be an index condition, so the index
// returns every row from the top of the listing and a Filter throws away the
// ones before the boundary. Both produce identical results, so no test that
// checks the ROWS can tell them apart, and the cost only appears on deep pages
// of a large log.
func TestTheKeysetBoundaryIsAnIndexConditionAndNotAFilter(t *testing.T) {
	ctx := context.Background()
	seedRows(ctx, t, "aud_keyset", "usr_keyset", "/admin/v1/keyset", 300)

	require.NoError(t, analyzeAuditLog(ctx))

	plan := planOf(ctx, t, audit.ListSQLForTest(),
		"", "", time.Now().UTC(), "aud_keyset_0200", 50)

	assert.Contains(t, plan, "Index Cond",
		"the paging boundary is not an index condition; a deep page now walks and "+
			"discards every row above it.\nPlan:\n%s", plan)
}

// analyzeAuditLog makes the planner's statistics current.
//
// Without it the seeded rows are invisible to the planner — a freshly filled
// table still looks empty in pg_class — and every plan above would be a
// sequential scan for a reason that has nothing to do with the indexes.
func analyzeAuditLog(ctx context.Context) error {
	_, err := pool().Exec(ctx, "ANALYZE audit_log")

	return err
}

// TestAListingComesBackNewestFirstAndPagesWithoutRepeatingARow is the reader's
// behavior, as an operator following an incident experiences it.
//
// Newest first is not a preference: an incident is read backwards from now. And
// the paging claim is the one offset cannot make — rows keep arriving while the
// reader walks, and with offset every arrival shifts the next page by one, so a
// row is silently skipped or seen twice. The write in the middle of this test is
// what makes that difference visible: with keyset paging the walk is unaffected.
func TestAListingComesBackNewestFirstAndPagesWithoutRepeatingARow(t *testing.T) {
	ctx := context.Background()
	store := audit.NewStore(pool())
	seedRows(ctx, t, "aud_walk", "usr_walk", "/admin/v1/walk", 10)

	first, err := store.List(ctx, audit.Filter{ActorID: "usr_walk", Limit: 4})
	require.NoError(t, err)
	require.Len(t, first, 4)

	for i := 1; i < len(first); i++ {
		assert.False(t, first[i].CreatedAt.After(first[i-1].CreatedAt),
			"the listing has to be newest first; an incident is read backwards from now")
	}

	// A row arrives while the reader is walking. Under offset paging this is
	// exactly what makes the next page skip a row.
	seedRows(ctx, t, "aud_walk_late", "usr_walk", "/admin/v1/walk", 1)

	last := first[len(first)-1]

	second, err := store.List(ctx, audit.Filter{
		ActorID: "usr_walk", Limit: 4,
		AfterAt: last.CreatedAt, AfterID: last.ID,
	})
	require.NoError(t, err)
	require.NotEmpty(t, second)

	seen := map[string]bool{}
	for _, r := range first {
		seen[r.ID] = true
	}

	for _, r := range second {
		assert.False(t, seen[r.ID],
			"%s appeared on two pages; the keyset boundary is not exclusive", r.ID)
	}
}

// TestAFilterAnswersOnlyItsOwnQuestion proves the two filters really narrow.
//
// A filter that silently did nothing would look identical on a table holding one
// actor's rows, which is what a small fixture usually holds. Both actors and
// both paths are seeded so that a no-op filter fails.
func TestAFilterAnswersOnlyItsOwnQuestion(t *testing.T) {
	ctx := context.Background()
	store := audit.NewStore(pool())
	seedRows(ctx, t, "aud_mine", "usr_mine", "/admin/v1/mine", 3)
	seedRows(ctx, t, "aud_yours", "usr_yours", "/admin/v1/yours", 3)

	byActor, err := store.List(ctx, audit.Filter{ActorID: "usr_mine", Limit: 50})
	require.NoError(t, err)
	require.NotEmpty(t, byActor)

	for _, r := range byActor {
		assert.Equal(t, "usr_mine", r.ActorID, "another caller's row entered the listing")
	}

	byPath, err := store.List(ctx, audit.Filter{Path: "/admin/v1/yours", Limit: 50})
	require.NoError(t, err)
	require.NotEmpty(t, byPath)

	for _, r := range byPath {
		assert.Equal(t, "/admin/v1/yours", r.Path, "another endpoint's row entered the listing")
	}
}
