//go:build integration

// The tests in this file need a real PostgreSQL instance (and therefore
// Docker); they are separated behind the `integration` tag so that `make test`
// stays fast. To run them: make test-integration
//
// The claim that cardinality is enforced BY A DATABASE CONSTRAINT can only be
// proved here: the unit tests only show that the right DDL is produced, not
// that PostgreSQL applies that DDL as expected.
package link_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/link"
)

const postgresImage = "postgres:16-alpine"

// testPool is the pool shared by every test; because the tests use separate
// link names they do not affect each other.
var testPool *db.Pool

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres brings up a single Postgres container and runs every test on
// it. It is a separate function because os.Exit skips deferred calls.
func runWithPostgres(m *testing.M) int {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_test"),
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
		fmt.Fprintf(os.Stderr, "the connection address could not be obtained: %v\n", err)
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

// TestDefineCreatesSchema verifies that Define creates the table, the
// cardinality indexes and the durable definition record.
func TestDefineCreatesSchema(t *testing.T) {
	ctx := context.Background()
	svc := newLinkService()
	def := definition("define_schema", link.OneToMany)

	require.NoError(t, svc.Define(ctx, def))

	table := tableNameFor(t, def.Name)
	assert.True(t, tableExists(ctx, t, table), "the link table must come into being")
	for _, column := range []string{"from_id", "to_id", "created_at"} {
		assert.True(t, columnExists(ctx, t, table, column), "the %s column must come into being", column)
	}

	assert.True(t, indexExists(ctx, t, table, table+"_pkey"),
		"the uniqueness of the pair comes from the primary key")
	assert.True(t, indexExists(ctx, t, table, table+"_to_uniq"),
		"one_to_many makes the to end unique")
	assert.False(t, indexExists(ctx, t, table, table+"_from_uniq"),
		"one_to_many does NOT CONSTRAIN the from end")

	// Plan Section 2.2: a link table gives an FK to no module's table.
	assert.Zero(t, foreignKeyCount(ctx, t, table),
		"a link table CANNOT hold a foreign key; that is the cross-module FK ban")

	assert.Equal(t,
		[]string{"product", "variant_id", "pricing", "price_set_id", "one_to_many"},
		ledgerRow(ctx, t, def.Name),
		"the definition must be written to the durable ledger")
}

// TestDefineRefusesAColliderAndSaysWhichIsWhich covers the schema-verification
// branch that fires when the link's name is already taken by a relation of
// another kind.
//
// "CREATE TABLE IF NOT EXISTS" does not error against a view of the same name,
// it SKIPS with a notice, so without this check the cardinality constraint
// would silently never exist. That branch had no test, and its message is the
// one place in this package where three same-typed operands sit in one format
// string. The translation out of Turkish (ADR 0012) reordered exactly such
// arguments, and a wrong order there is invisible to the compiler, to go vet
// and to every other test — measured: swapping the operands back to the
// Turkish order leaves build, vet, the unit suite and this suite green.
//
// The assertion is therefore on the ORDER, not merely on the words: the
// colliding relation is named first, its kind second, the link name last.
func TestDefineRefusesAColliderAndSaysWhichIsWhich(t *testing.T) {
	ctx := context.Background()
	svc := newLinkService()
	// ManyToMany deliberately: it needs no cardinality index of its own, so
	// the DDL is a single CREATE TABLE IF NOT EXISTS, which SKIPS against the
	// view instead of failing — which is exactly the silent path this check
	// exists to catch. With a cardinality that also creates an index, the
	// CREATE INDEX errors first and the branch is never reached.
	def := definition("collider", link.ManyToMany)
	table := tableNameFor(t, def.Name)

	// A MATERIALIZED view, not a plain one: a plain view rejects the index the
	// DDL creates and the run fails one step earlier, before the check under
	// test. A materialized view accepts indexes, so the whole DDL "succeeds"
	// against a relation that is not a table — the silent shape.
	_, err := testPool.Pool().Exec(ctx,
		"CREATE MATERIALIZED VIEW "+table+
			" AS SELECT 'a'::text AS from_id, 'b'::text AS to_id, now() AS created_at")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = testPool.Pool().Exec(context.Background(), "DROP MATERIALIZED VIEW IF EXISTS "+table)
	})

	err = svc.Define(ctx, def)

	require.Error(t, err, "a name already taken by a view cannot become a link table")
	assert.Regexp(t,
		table+` is not a table \(relkind="m"\); the link name "`+def.Name+`" collides`,
		err.Error(),
		"the message must name the colliding relation, then its kind, then the link")
}

// TestDefineLogsStructuredFieldKeys pins the FIELD KEYS of the declaration log
// record.
//
// The sentence of the record is prose and may be reworded — it was rewritten
// when this package was translated out of Turkish (ADR 0012). Its field keys
// are not prose: they are what an operator greps and what a log pipeline
// indexes. Renaming one is silent for the compiler, and it was measured to be
// silent for the whole package too — renaming "table" to "tbl" left both
// `go test -race ./core/link/` and this suite green. Only the
// dashboard that stopped matching would have reported it.
//
// The keys are asserted through the RENDERED record rather than against the
// constants that produce them: an assertion that a constant equals another
// constant would still pass with the attribute dropped from the call.
func TestDefineLogsStructuredFieldKeys(t *testing.T) {
	ctx := context.Background()

	var buf bytes.Buffer
	svc := link.New(testPool, slog.New(slog.NewTextHandler(&buf, nil)))
	def := definition("define_log_fields", link.OneToMany)

	require.NoError(t, svc.Define(ctx, def))

	out := buf.String()
	for _, field := range []string{
		"link=" + def.Name,
		"table=" + tableNameFor(t, def.Name),
		"cardinality=one_to_many",
	} {
		assert.Contains(t, out, field,
			"the declaration record must carry %q; an operator filters on these keys\n%s", field, out)
	}
}

// TestDefineIsIdempotent verifies that the same definition can be redeclared
// on every startup; both through the same service and through a NEW service
// whose registry is empty.
func TestDefineIsIdempotent(t *testing.T) {
	ctx := context.Background()
	def := definition("define_idempotent", link.ManyToMany)
	svc := newLinkService()

	require.NoError(t, svc.Define(ctx, def))
	require.NoError(t, svc.Define(ctx, def), "the same service must be able to redeclare the same definition")

	// The new service's in-process registry is empty; this call really goes to
	// the database and compares against the durable ledger.
	require.NoError(t, newLinkService().Define(ctx, def),
		"a restarted process must be able to declare the same definition")

	table := tableNameFor(t, def.Name)
	assert.True(t, tableExists(ctx, t, table))
	assert.Equal(t, 1, ledgerRowCount(ctx, t, def.Name), "a single row must remain in the ledger")
}

// TestDefineRejectsChangedDefinition verifies that a definition changed
// between releases is caught by the durable ledger. The in-process registry
// cannot see this case; the only place that catches it is the database.
//
// Since ADR 0116 "changed" means NARROWED or moved: a widening is a declaration
// this path applies, and [TestALinkCanBeWidened] is where that lives.
func TestDefineRejectsChangedDefinition(t *testing.T) {
	ctx := context.Background()
	def := definition("define_changed", link.OneToMany)
	require.NoError(t, newLinkService().Define(ctx, def))

	// A NARROWING. Since ADR 0116 the widening direction is applied rather
	// than refused, so the case this test is about has to be the other one:
	// one_to_one demands a uniqueness the rows already on disk may violate,
	// and nothing here looked at them.
	changed := def
	changed.Cardinality = link.OneToOne

	// A NEW service with an empty registry: the conflict can only be known
	// from the database.
	err := newLinkService().Define(ctx, changed)

	require.Error(t, err, "a different definition under the same name cannot be accepted")
	assert.True(t, errors.IsConflict(err),
		"the error class must be KindConflict, got %v", errors.KindOf(err))
	assert.Equal(t, "link_definition_conflict", errors.CodeOf(err))
	assert.Contains(t, err.Error(), "one_to_many", "the message must show the stored definition")
	assert.Contains(t, err.Error(), "may only be widened",
		"the reader is most often rolling a release back and needs the direction named")

	// Because the transaction was rolled back, the ledger and the schema must
	// be UNCHANGED.
	assert.Equal(t, "one_to_many", ledgerRow(ctx, t, def.Name)[4])
	assert.True(t, indexExists(ctx, t, tableNameFor(t, def.Name), tableNameFor(t, def.Name)+"_to_uniq"),
		"a rejected declaration must not remove the existing constraint")

	// A definition with a changed end is rejected in the same way.
	otherEnd := def
	otherEnd.To.Module = "inventory"
	assert.True(t, errors.IsConflict(newLinkService().Define(ctx, otherEnd)))
}

// TestALinkCanBeWidened proves the claim ADR 0116 rests on: a declared
// cardinality can move to a freer one, ON A DATABASE THAT ALREADY HOLDS THE
// TABLE, and the constraint that stopped the extra row is really gone.
//
// The unit tests show that the right DDL is produced. Only here does PostgreSQL
// say whether it applied it — and the half that would have been missed without
// a real database is the DROP: declaring one_to_many does not by itself remove
// a unique index built under one_to_one, so without it the second row would
// still be refused while the ledger promised it was allowed.
func TestALinkCanBeWidened(t *testing.T) {
	ctx := context.Background()
	def := definition("widen_cardinality", link.OneToOne)

	// The service is the PROCESS: a release declares the link and then works
	// through the same registry. The widening is a second process, so it gets
	// a second service, which is also what forces the durable-ledger path.
	release1 := newLinkService()
	require.NoError(t, release1.Define(ctx, def))

	table := tableNameFor(t, def.Name)
	require.NoError(t, release1.Create(ctx, def.Name, "order_1", "coll_1"))

	// Under one_to_one the second target is refused, which is the state the
	// widening is being asked to leave.
	requireCardinalityConflict(t,
		release1.Create(ctx, def.Name, "order_1", "coll_2"), "order_1")

	widened := def
	widened.Cardinality = link.OneToMany
	release2 := newLinkService()
	require.NoError(t, release2.Define(ctx, widened),
		"a widening is a declaration, not a conflict")

	assert.Equal(t, "one_to_many", ledgerRow(ctx, t, def.Name)[4],
		"the ledger must carry the cardinality that is now enforced")
	assert.False(t, indexExists(ctx, t, table, table+"_from_uniq"),
		"the index enforcing the OLD constraint must be dropped, or the widening is a promise only")
	assert.True(t, indexExists(ctx, t, table, table+"_to_uniq"),
		"one_to_many still owns the to end")

	require.NoError(t, release2.Create(ctx, def.Name, "order_1", "coll_2"),
		"the second target is the whole point of the widening")

	ids, err := release2.List(ctx, def.Name, "order_1")
	require.NoError(t, err)
	assert.Equal(t, []string{"coll_1", "coll_2"}, ids)

	// The end the new cardinality still constrains is still constrained. A
	// widening that quietly freed BOTH ends would let a collection belong to
	// two orders.
	requireCardinalityConflict(t, release2.Create(ctx, def.Name, "order_2", "coll_1"), "coll_1")
}

// TestAWidenedLinkCanBeWidenedAgain carries the same link to the last step and
// proves the index SWAP on the to end: many_to_many replaces the unique index
// with a plain one, so the reverse-direction query keeps an index across a
// change that removes a constraint.
func TestAWidenedLinkCanBeWidenedAgain(t *testing.T) {
	ctx := context.Background()
	def := definition("widen_twice", link.OneToMany)
	release1 := newLinkService()
	require.NoError(t, release1.Define(ctx, def))
	require.NoError(t, release1.Create(ctx, def.Name, "order_1", "coll_1"))

	table := tableNameFor(t, def.Name)
	requireCardinalityConflict(t,
		release1.Create(ctx, def.Name, "order_2", "coll_1"), "coll_1")

	widened := def
	widened.Cardinality = link.ManyToMany
	release2 := newLinkService()
	require.NoError(t, release2.Define(ctx, widened))

	assert.Equal(t, "many_to_many", ledgerRow(ctx, t, def.Name)[4])
	assert.False(t, indexExists(ctx, t, table, table+"_to_uniq"),
		"many_to_many constrains neither end")
	assert.True(t, indexExists(ctx, t, table, table+"_to_lookup"),
		"the reverse-direction query must not be left to a table scan")

	require.NoError(t, release2.Create(ctx, def.Name, "order_2", "coll_1"),
		"under many_to_many a target may belong to more than one record")
}

// TestAWideningSurvivesTheDeclarationBeingRepeated verifies that the startup
// after the widening is an ordinary one.
//
// It is the case a widening is most likely to get wrong: the drops run on every
// Define, and the second run has nothing left to drop. If DROP INDEX were not
// guarded the process would fail to start the day AFTER the upgrade, which is
// the worst possible day to find out.
func TestAWideningSurvivesTheDeclarationBeingRepeated(t *testing.T) {
	ctx := context.Background()
	def := definition("widen_repeat", link.OneToOne)
	require.NoError(t, newLinkService().Define(ctx, def))

	widened := def
	widened.Cardinality = link.OneToMany
	require.NoError(t, newLinkService().Define(ctx, widened))

	restarted := newLinkService()
	require.NoError(t, restarted.Define(ctx, widened),
		"the startup after a widening is an ordinary startup")

	assert.Equal(t, "one_to_many", ledgerRow(ctx, t, def.Name)[4])
	require.NoError(t, restarted.Create(ctx, def.Name, "order_1", "coll_1"))
	require.NoError(t, restarted.Create(ctx, def.Name, "order_1", "coll_2"))
}

// TestASchemaConvergesOnTheDeclarationWhenTheLedgerRowIsGone covers the way a
// stored definition can be abandoned rather than changed.
//
// If the row is DELETED, the upsert on the next startup inserts a fresh one
// carrying the new cardinality, RETURNING hands back what was just written, and
// the comparison compares the declaration against itself. Nothing in the ledger
// can object, and every startup after that looks consistent — while the index
// built under the old cardinality is still there enforcing it.
//
// This is why the drops are unconditional rather than reserved for the widening
// branch: the schema is made to match the DECLARATION, whatever the ledger was
// able to say about it.
func TestASchemaConvergesOnTheDeclarationWhenTheLedgerRowIsGone(t *testing.T) {
	ctx := context.Background()
	def := definition("widen_ledger_gone", link.OneToOne)
	release1 := newLinkService()
	require.NoError(t, release1.Define(ctx, def))

	table := tableNameFor(t, def.Name)
	require.True(t, indexExists(ctx, t, table, table+"_from_uniq"),
		"one_to_one owns both ends, and this is the index the widening has to remove")

	// The row leaves; the table and its indexes stay exactly as they were.
	_, err := testPool.Pool().Exec(ctx, `DELETE FROM link_definitions WHERE name = $1`, def.Name)
	require.NoError(t, err)

	widened := def
	widened.Cardinality = link.OneToMany
	release2 := newLinkService()
	require.NoError(t, release2.Define(ctx, widened),
		"an absent row is an ordinary first declaration")

	assert.False(t, indexExists(ctx, t, table, table+"_from_uniq"),
		"the schema must match the declaration even when the ledger could not object")

	require.NoError(t, release2.Create(ctx, def.Name, "order_1", "coll_1"))
	require.NoError(t, release2.Create(ctx, def.Name, "order_1", "coll_2"),
		"the cardinality the ledger now states is the one the database enforces")
}

// TestDefineIsSafeUnderConcurrency verifies that processes starting at the
// same time (here: goroutines) can declare the same definition without racing.
// Without the advisory lock, concurrent "CREATE TABLE IF NOT EXISTS"
// statements would collide at the catalog level.
func TestDefineIsSafeUnderConcurrency(t *testing.T) {
	ctx := context.Background()
	def := definition("define_race", link.OneToOne)

	const concurrency = 8
	errs := make(chan error, concurrency)

	var wg sync.WaitGroup
	for range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- newLinkService().Define(ctx, def)
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err, "concurrent declarations must complete without a collision")
	}
	assert.Equal(t, 1, ledgerRowCount(ctx, t, def.Name))
	assert.True(t, tableExists(ctx, t, tableNameFor(t, def.Name)))
}

// TestCreateListDeleteEndToEnd verifies the create, read and delete path end to
// end; the idempotency decisions are proved here too.
func TestCreateListDeleteEndToEnd(t *testing.T) {
	ctx := context.Background()
	svc := newLinkService()
	def := definition("end_to_end", link.OneToMany)
	require.NoError(t, svc.Define(ctx, def))

	t.Run("a link is created and read back", func(t *testing.T) {
		require.NoError(t, svc.Create(ctx, def.Name, "var_1", "ps_2"))
		require.NoError(t, svc.Create(ctx, def.Name, "var_1", "ps_1"))

		ids, err := svc.List(ctx, def.Name, "var_1")
		require.NoError(t, err)
		assert.Equal(t, []string{"ps_1", "ps_2"}, ids, "the result must be in ascending order")
	})

	t.Run("the same pair a second time is a no-op", func(t *testing.T) {
		// Saga retries rerun the same step; that is not an error
		// (plan Section 2.6).
		require.NoError(t, svc.Create(ctx, def.Name, "var_1", "ps_1"))

		ids, err := svc.List(ctx, def.Name, "var_1")
		require.NoError(t, err)
		assert.Equal(t, []string{"ps_1", "ps_2"}, ids, "a repeated link must not duplicate a row")
	})

	t.Run("a record with no link returns an empty slice", func(t *testing.T) {
		ids, err := svc.List(ctx, def.Name, "var_absent")
		require.NoError(t, err, "an unknown fromID is not an error")
		assert.NotNil(t, ids, "an empty result must be an empty slice, not nil")
		assert.Empty(t, ids)
	})

	t.Run("a batch read comes back in a single query", func(t *testing.T) {
		require.NoError(t, svc.Create(ctx, def.Name, "var_2", "ps_3"))

		got, err := svc.ListMany(ctx, def.Name, []string{"var_1", "var_2", "var_absent"})
		require.NoError(t, err)
		assert.Equal(t, map[string][]string{
			"var_1": {"ps_1", "ps_2"},
			"var_2": {"ps_3"},
		}, got, "no key must be produced for a fromID with no link")
	})

	t.Run("a delete removes only the target pair", func(t *testing.T) {
		require.NoError(t, svc.Delete(ctx, def.Name, "var_1", "ps_1"))

		ids, err := svc.List(ctx, def.Name, "var_1")
		require.NoError(t, err)
		assert.Equal(t, []string{"ps_2"}, ids)
	})

	t.Run("deleting a link that does not exist is a no-op", func(t *testing.T) {
		// A compensation step also runs after a failed Create, and "absent" is
		// precisely the desired outcome.
		require.NoError(t, svc.Delete(ctx, def.Name, "var_1", "ps_1"))
		require.NoError(t, svc.Delete(ctx, def.Name, "var_never", "ps_1"))
	})
}

// TestOneToOneIsEnforcedByDatabase verifies that the OneToOne cardinality
// constrains both ends and that a violation comes back as a typed conflict.
func TestOneToOneIsEnforcedByDatabase(t *testing.T) {
	ctx := context.Background()
	svc := newLinkService()
	def := definition("one_to_one_link", link.OneToOne)
	require.NoError(t, svc.Define(ctx, def))
	require.NoError(t, svc.Create(ctx, def.Name, "a", "1"))

	t.Run("the same fromID cannot bind to a second target", func(t *testing.T) {
		requireCardinalityConflict(t, svc.Create(ctx, def.Name, "a", "2"), "a")
	})

	t.Run("the same toID cannot bind to a second source", func(t *testing.T) {
		requireCardinalityConflict(t, svc.Create(ctx, def.Name, "b", "1"), "1")
	})

	t.Run("the same pair is still a no-op", func(t *testing.T) {
		require.NoError(t, svc.Create(ctx, def.Name, "a", "1"),
			"a cardinality violation must not be confused with an idempotent repeat")
	})

	assert.Equal(t, 1, rowCount(ctx, t, tableNameFor(t, def.Name)))
}

// TestOneToManyIsEnforcedByDatabase verifies that under OneToMany a fromID can
// bind to many targets while a toID belongs to a single source.
func TestOneToManyIsEnforcedByDatabase(t *testing.T) {
	ctx := context.Background()
	svc := newLinkService()
	def := definition("one_to_many_link", link.OneToMany)
	require.NoError(t, svc.Define(ctx, def))

	require.NoError(t, svc.Create(ctx, def.Name, "a", "1"))
	require.NoError(t, svc.Create(ctx, def.Name, "a", "2"), "one source may bind to many targets")

	requireCardinalityConflict(t, svc.Create(ctx, def.Name, "b", "1"), "1")

	ids, err := svc.List(ctx, def.Name, "a")
	require.NoError(t, err)
	assert.Equal(t, []string{"1", "2"}, ids)
	assert.Equal(t, 2, rowCount(ctx, t, tableNameFor(t, def.Name)))
}

// TestManyToManyAllowsSharedIDs verifies that under ManyToMany only the pair
// is unique and that adding the same pair a second time does not duplicate a
// row.
func TestManyToManyAllowsSharedIDs(t *testing.T) {
	ctx := context.Background()
	svc := newLinkService()
	def := definition("many_to_many_link", link.ManyToMany)
	require.NoError(t, svc.Define(ctx, def))

	require.NoError(t, svc.Create(ctx, def.Name, "a", "1"))
	require.NoError(t, svc.Create(ctx, def.Name, "a", "2"))
	require.NoError(t, svc.Create(ctx, def.Name, "b", "1"))
	require.NoError(t, svc.Create(ctx, def.Name, "a", "1"), "the same pair is a no-op, not an error")

	assert.Equal(t, 3, rowCount(ctx, t, tableNameFor(t, def.Name)),
		"a repeated pair must not duplicate a row")

	ids, err := svc.List(ctx, def.Name, "a")
	require.NoError(t, err)
	assert.Equal(t, []string{"1", "2"}, ids)
}

// TestListOrderIsDeterministic verifies that the ordering is independent of
// the insertion order and reproducible; API responses and tests rely on it.
func TestListOrderIsDeterministic(t *testing.T) {
	ctx := context.Background()
	svc := newLinkService()
	def := definition("ordering", link.ManyToMany)
	require.NoError(t, svc.Define(ctx, def))

	// The insertion order is deliberately shuffled.
	for _, id := range []string{"ps_30", "ps_10", "ps_20", "ps_02", "ps_01"} {
		require.NoError(t, svc.Create(ctx, def.Name, "var_1", id))
	}
	want := []string{"ps_01", "ps_02", "ps_10", "ps_20", "ps_30"}

	for range 5 {
		ids, err := svc.List(ctx, def.Name, "var_1")
		require.NoError(t, err)
		assert.Equal(t, want, ids)
	}

	got, err := svc.ListMany(ctx, def.Name, []string{"var_1"})
	require.NoError(t, err)
	assert.Equal(t, want, got["var_1"], "the batch read must give the same order")
}

// TestCanceledContextIsReportedAsUnavailable verifies that a canceled context
// is reported as a cancellation and not as "the database is broken".
func TestCanceledContextIsReportedAsUnavailable(t *testing.T) {
	ctx := context.Background()
	svc := newLinkService()
	def := definition("cancellation", link.ManyToMany)
	require.NoError(t, svc.Define(ctx, def))

	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()

	err := svc.Create(canceledCtx, def.Name, "a", "1")

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable),
		"the error class must be KindUnavailable, got %v", errors.KindOf(err))
	assert.Equal(t, "link_canceled", errors.CodeOf(err))
	assert.Zero(t, rowCount(ctx, t, tableNameFor(t, def.Name)))
}

// --- helpers ---

// definition builds a definition with the given name and cardinality for the
// tests.
func definition(name string, c link.Cardinality) link.LinkDefinition {
	return link.LinkDefinition{
		Name:        name,
		From:        link.LinkSide{Module: "product", Field: "variant_id"},
		To:          link.LinkSide{Module: "pricing", Field: "price_set_id"},
		Cardinality: c,
	}
}

// newLinkService builds a new service whose in-process registry is EMPTY, so
// that the durable-ledger path is really exercised.
func newLinkService() link.LinkService {
	return link.New(testPool, nil)
}

// tableNameFor builds the table name from a link name with error checking.
func tableNameFor(t *testing.T, name string) string {
	t.Helper()

	table, err := link.TableName(name)
	require.NoError(t, err)
	return table
}

// requireCardinalityConflict verifies that a cardinality violation is typed
// and readable.
func requireCardinalityConflict(t *testing.T, err error, takenID string) {
	t.Helper()

	require.Error(t, err, "a cardinality violation cannot pass silently")
	assert.True(t, errors.IsConflict(err),
		"the error class must be KindConflict, got %v", errors.KindOf(err))
	assert.Equal(t, "link_cardinality_violation", errors.CodeOf(err))
	assert.Contains(t, err.Error(), takenID, "the message must write which id is taken")
}

// tableExists reports whether the given table exists in the public schema.
func tableExists(ctx context.Context, t *testing.T, table string) bool {
	t.Helper()

	var exists bool
	queryOne(ctx, t, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = $1
		)`, []any{table}, &exists)
	return exists
}

// columnExists reports whether a column exists in the given table.
func columnExists(ctx context.Context, t *testing.T, table, column string) bool {
	t.Helper()

	var exists bool
	queryOne(ctx, t, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2
		)`, []any{table, column}, &exists)
	return exists
}

// indexExists reports whether an index exists on the given table.
func indexExists(ctx context.Context, t *testing.T, table, index string) bool {
	t.Helper()

	var exists bool
	queryOne(ctx, t, `
		SELECT EXISTS (
			SELECT 1 FROM pg_indexes
			WHERE schemaname = 'public' AND tablename = $1 AND indexname = $2
		)`, []any{table, index}, &exists)
	return exists
}

// foreignKeyCount returns the number of foreign key constraints on the table;
// it must always be zero (plan Section 2.2).
func foreignKeyCount(ctx context.Context, t *testing.T, table string) int {
	t.Helper()

	var count int
	queryOne(ctx, t, `
		SELECT count(*) FROM pg_constraint c
		JOIN pg_class rel ON rel.oid = c.conrelid
		JOIN pg_namespace ns ON ns.oid = rel.relnamespace
		WHERE ns.nspname = 'public' AND rel.relname = $1 AND c.contype = 'f'`,
		[]any{table}, &count)
	return count
}

// rowCount returns the number of rows in the link table.
func rowCount(ctx context.Context, t *testing.T, table string) int {
	t.Helper()

	var count int
	// The table name is the validated name the test itself produced.
	queryOne(ctx, t, fmt.Sprintf("SELECT count(*) FROM %s", table), nil, &count)
	return count
}

// ledgerRowCount returns the number of rows in the durable ledger.
func ledgerRowCount(ctx context.Context, t *testing.T, name string) int {
	t.Helper()

	var count int
	queryOne(ctx, t, `SELECT count(*) FROM link_definitions WHERE name = $1`, []any{name}, &count)
	return count
}

// ledgerRow returns the definition in the durable ledger in field order.
func ledgerRow(ctx context.Context, t *testing.T, name string) []string {
	t.Helper()

	var fromModule, fromField, toModule, toField, cardinality string
	row := testPool.Pool().QueryRow(ctx, `
		SELECT from_module, from_field, to_module, to_field, cardinality
		FROM link_definitions WHERE name = $1`, name)
	require.NoError(t, row.Scan(&fromModule, &fromField, &toModule, &toField, &cardinality))
	return []string{fromModule, fromField, toModule, toField, cardinality}
}

// queryOne runs a single-valued verification query.
func queryOne(ctx context.Context, t *testing.T, sql string, args []any, dest any) {
	t.Helper()

	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	require.NoError(t, testPool.Pool().QueryRow(queryCtx, sql, args...).Scan(dest))
}

// TestDefineRejectsIndexNamespaceCollision verifies both layers that close the
// silent breakage arising from PostgreSQL keeping tables and indexes in the
// SAME namespace (pg_class).
//
// Regression: when a link named "x_from_uniq" was declared, the uniqueness
// index of link "x" resolved to the same relation name.
// "CREATE UNIQUE INDEX IF NOT EXISTS" does NOT error in that case; it raises a
// NOTICE and SKIPS — that is, Define returns success, the definition is
// written to the ledger, but the cardinality constraint is NEVER CREATED in
// the database. The breakage would only be noticed after the data was
// corrupted.
func TestDefineRejectsIndexNamespaceCollision(t *testing.T) {
	svc := newLinkService()
	ctx := t.Context()

	t.Run("a name with an index suffix is rejected", func(t *testing.T) {
		for _, name := range []string{"collision_from_uniq", "collision_to_uniq", "collision_to_lookup"} {
			err := svc.Define(ctx, definition(name, link.ManyToMany))
			require.Error(t, err, "%q was accepted", name)
			assert.True(t, errors.IsInvalid(err), "the class for %q must be Invalid, got: %v", name, err)
		}
	})

	t.Run("an externally created name collision is caught after the DDL", func(t *testing.T) {
		// Outside the link API, create an INDEX named link_<name> directly in
		// the database. Name validation cannot see this; the layer that must
		// catch it is verifySchema.
		pool := testPool.Pool()
		_, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS collision_carrier (id TEXT)`)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS link_external ON collision_carrier (id)`)
		require.NoError(t, err)

		err = svc.Define(ctx, definition("external", link.OneToOne))
		require.Error(t, err, "Define must not return success while link_external is an INDEX")
		assert.Contains(t, err.Error(), "external")
	})
}
