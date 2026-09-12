//go:build integration

// The tests in this file need a real PostgreSQL instance (and therefore Docker);
// they are separated behind the `integration` tag so that `make test` stays fast.
// To run them: make test-integration
//
// What only a real server can show is the thing the whole design rests on: the
// PRIMARY KEY dropping a redelivery. The unit tests run over a fake store, and a
// fake that de-duplicated by hand would prove the fake's arithmetic rather than
// the table's constraint — which is exactly the "fake that does not imitate the
// schema" class this repository keeps logging.
//
// The same goes for the funnel query: the conditional counts, the UTC date
// derived in SQL and the half-open window are all statements the server executes.
package analytics

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/core/db"
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

// runWithPostgres brings up a single Postgres container, applies the plugin's
// schema and runs every test against it.
func runWithPostgres(m *testing.M) int {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_analytics"),
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
		fmt.Fprintf(os.Stderr, "the connection address could not be read: %v\n", err)

		return 1
	}

	// The schema is applied the SAME way as in production: the module's
	// Migrations() and the module name. Writing CREATE TABLE by hand would leave
	// the migration itself untested.
	if err = db.Migrate(ctx, testDSN, migrationsRoot, ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "the analytics schema could not be applied: %v\n", err)

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

// realStore returns a store over an empty table.
func realStore(t *testing.T) eventStore {
	t.Helper()

	_, err := testPool.Pool().Exec(t.Context(), "TRUNCATE analytics_events")
	require.NoError(t, err)

	return newEventStore(testPool.Pool())
}

// day is a UTC midnight.
func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// TestARedeliveryIsDroppedByThePrimaryKey is the design's whole idempotency
// argument, on the real constraint.
//
// The bus delivers AT LEAST ONCE and the publishing modules put the same DERIVED
// id on both of their delivery paths, so the same event reaches this table twice
// in the ordinary case — once from the direct publish, once from the outbox relay
// if the first was lost. Counting it twice would turn a shop's conversion rate
// into a number that never happened.
func TestARedeliveryIsDroppedByThePrimaryKey(t *testing.T) {
	store := realStore(t)
	ctx := t.Context()

	row := eventRow{
		ID:         "cart.created:cart_1",
		Topic:      "cart.created",
		OccurredAt: day(2026, time.September, 12).Add(9 * time.Hour),
		RegionID:   "reg_1",
	}

	require.NoError(t, store.Record(ctx, row))
	require.NoError(t, store.Record(ctx, row),
		"a redelivery must not be an ERROR either: the bus would log a handler failure "+
			"for an event that was handled correctly")

	rows, err := store.Funnel(ctx, day(2026, time.September, 12), day(2026, time.September, 13))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, int64(1), rows[0].CartsCreated,
		"two deliveries of ONE event are one cart; the primary key is what says so")
}

// TestTheFunnelCountsTheThreeTopicsInOnePass is the endpoint's arithmetic.
func TestTheFunnelCountsTheThreeTopicsInOnePass(t *testing.T) {
	store := realStore(t)
	ctx := t.Context()
	at := day(2026, time.September, 12).Add(10 * time.Hour)

	for _, row := range []eventRow{
		{ID: "c1", Topic: "cart.created", OccurredAt: at, RegionID: "reg_1"},
		{ID: "c2", Topic: "cart.created", OccurredAt: at, RegionID: "reg_1"},
		{ID: "c3", Topic: "cart.created", OccurredAt: at, RegionID: "reg_2"},
		{ID: "d1", Topic: "cart.completed", OccurredAt: at, RegionID: "reg_1"},
		{ID: "o1", Topic: "order.placed", OccurredAt: at, RegionID: "reg_1"},
	} {
		require.NoError(t, store.Record(ctx, row))
	}

	rows, err := store.Funnel(ctx, day(2026, time.September, 12), day(2026, time.September, 13))
	require.NoError(t, err)

	require.Len(t, rows, 2, "a row per region")
	assert.Equal(t, "reg_1", rows[0].RegionID)
	assert.Equal(t, int64(2), rows[0].CartsCreated)
	assert.Equal(t, int64(1), rows[0].CartsCompleted)
	assert.Equal(t, int64(1), rows[0].OrdersPlaced)

	assert.Equal(t, "reg_2", rows[1].RegionID)
	assert.Equal(t, int64(1), rows[1].CartsCreated)
	assert.Zero(t, rows[1].CartsCompleted,
		"a region with no completion must report zero rather than be absent: a shop "+
			"comparing two regions needs the row that says 'nobody finished here'")
}

// TestTheWindowIsHalfOpenSoAdjacentWindowsTile is the boundary rule.
//
// Closed on the left, open on the right is the only form that tiles: two
// adjacent windows asked for separately add up to the wide one, and no day is
// counted twice. The alternative — both ends closed — double-counts the shared
// day in every report that pages through a month a week at a time.
func TestTheWindowIsHalfOpenSoAdjacentWindowsTile(t *testing.T) {
	store := realStore(t)
	ctx := t.Context()

	for i, at := range []time.Time{
		day(2026, time.September, 11).Add(23 * time.Hour),
		day(2026, time.September, 12).Add(time.Hour),
		day(2026, time.September, 13).Add(time.Hour),
	} {
		require.NoError(t, store.Record(ctx, eventRow{
			ID: fmt.Sprintf("c%d", i), Topic: "cart.created", OccurredAt: at, RegionID: "reg_1",
		}))
	}

	middle, err := store.Funnel(ctx, day(2026, time.September, 12), day(2026, time.September, 13))
	require.NoError(t, err)
	require.Len(t, middle, 1, "only the 12th is in [12, 13)")
	assert.Equal(t, day(2026, time.September, 12), middle[0].Day.UTC())

	first, err := store.Funnel(ctx, day(2026, time.September, 11), day(2026, time.September, 12))
	require.NoError(t, err)
	second, err := store.Funnel(ctx, day(2026, time.September, 12), day(2026, time.September, 14))
	require.NoError(t, err)
	wide, err := store.Funnel(ctx, day(2026, time.September, 11), day(2026, time.September, 14))
	require.NoError(t, err)

	assert.Len(t, wide, 3)
	assert.Equal(t, len(first)+len(second), len(wide),
		"two adjacent windows must add up to the wide one exactly; an overlap at the "+
			"boundary would count a day twice in every paged report")
}

// TestTheDayIsDerivedInUTCFromTheStampedMoment keeps a shop's Tuesday in place.
//
// The relay can deliver a minute late and a row dated by its own insert would
// move an event across midnight. The column is derived from occurred_at in SQL so
// the two cannot disagree.
func TestTheDayIsDerivedInUTCFromTheStampedMoment(t *testing.T) {
	store := realStore(t)
	ctx := t.Context()

	// 23:30 in a zone four hours ahead is 19:30 UTC on the SAME day; 01:30 in the
	// same zone is 21:30 UTC on the day BEFORE. The second is the one that moves.
	zone := time.FixedZone("test+4", 4*60*60)
	require.NoError(t, store.Record(ctx, eventRow{
		ID: "late", Topic: "cart.created", RegionID: "reg_1",
		OccurredAt: time.Date(2026, time.September, 13, 1, 30, 0, 0, zone),
	}))

	rows, err := store.Funnel(ctx, day(2026, time.September, 12), day(2026, time.September, 13))
	require.NoError(t, err)

	require.Len(t, rows, 1,
		"the day is the moment's UTC date: 01:30+04:00 on the 13th is 21:30 UTC on the 12th")
	assert.Equal(t, day(2026, time.September, 12), rows[0].Day.UTC())
}

// TestAnUnknownTopicIsRefusedByTheSchema is the last line of defense.
//
// The handlers only ever write the three names they subscribe to, so this cannot
// happen through them. The CHECK is there for the path that bypasses them: a
// later version of this plugin, or a hand-written row. A misspelled topic would
// sum correctly into a column nobody reads and leave the funnel silently short.
func TestAnUnknownTopicIsRefusedByTheSchema(t *testing.T) {
	store := realStore(t)

	err := store.Record(t.Context(), eventRow{
		ID: "x", Topic: "cart.abandoned", RegionID: "reg_1",
		OccurredAt: day(2026, time.September, 12),
	})

	require.Error(t, err, "the topic vocabulary is closed by a CHECK")
}

// TestTheMigrationIsReversible keeps the plugin removable.
//
// A plugin that cannot be migrated down is a plugin an installation cannot get
// rid of, which is the opposite of what the boundary promises.
func TestTheMigrationIsReversible(t *testing.T) {
	ctx := t.Context()
	store := realStore(t)
	require.NoError(t, store.Record(ctx, eventRow{
		ID: "x", Topic: "cart.created", RegionID: "reg_1",
		OccurredAt: day(2026, time.September, 12),
	}))

	require.NoError(t, db.MigrateDown(ctx, testDSN, migrationsRoot, ModuleName, 0),
		"down must work with DATA in the table; a down that only works on an empty "+
			"schema is a down nobody can run")
	assert.False(t, tableExists(t, "analytics_events"))

	require.NoError(t, db.Migrate(ctx, testDSN, migrationsRoot, ModuleName))
	assert.True(t, tableExists(t, "analytics_events"))
}

// tableExists reports whether the table is in the database.
func tableExists(t *testing.T, table string) bool {
	t.Helper()

	var exists bool
	err := testPool.Pool().QueryRow(t.Context(),
		`SELECT EXISTS (
             SELECT 1 FROM pg_class c
             JOIN pg_namespace n ON n.oid = c.relnamespace
             WHERE c.relname = $1 AND c.relkind = 'r' AND n.nspname = current_schema()
         )`, table).Scan(&exists)
	require.NoError(t, err)

	return exists
}
