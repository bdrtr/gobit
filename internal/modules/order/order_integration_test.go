//go:build integration

// The tests in this file require a real PostgreSQL instance (and therefore
// Docker); they are separated behind the `integration` tag so that `make test`
// stays fast. To run them: make test-integration
//
// The unit tests prove the service's DECISIONS with a fake store. The tests
// here prove the GROUND those decisions stand on: that the migration can be
// rolled back, that the constraints really are enforced, and that an order's
// NUMBER really stays unique under concurrent writes. The claim "20 concurrent
// orders do not take the same number" in particular can only be exercised here,
// with real goroutines over a real sequence.
package order_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/eventbus/outbox"
	"github.com/bdrtr/gobit/internal/modules/order"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/repository"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// The container images used in the integration tests.
const (
	postgresImage = "postgres:16-alpine"
	redisImage    = "redis:7-alpine"
)

// moduleTables are the tables the module owns; the migration tests use this
// list.
var moduleTables = []string{
	"orders", "order_line_items", "order_summaries",
	"order_returns", "order_return_items", "order_exchanges", "order_claims",
}

// Constants used in the test data. The region, customer and variant ids belong
// to OTHER modules; this module does not verify their existence (Principle
// 2.2).
const (
	testRegionID   = "reg_TEST"
	testCustomerID = "cus_TEST"
	testCurrency   = "TRY"
)

var (
	// testPool is the pool all the tests share.
	testPool *db.Pool
	// testDSN is the connection address for the migration calls.
	testDSN string
)

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres brings up a single Postgres container and runs all the tests
// on it. It is a separate function because os.Exit skips defers.
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

	testDSN, err = ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "the connection address could not be obtained: %v\n", err)
		return 1
	}

	cfg := db.DefaultConfig(testDSN)
	// The concurrency test runs dozens of goroutines at once; because every
	// transaction holds a connection, the pool is opened wider than the default.
	cfg.MaxConns = 24
	testPool, err = db.New(ctx, cfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "the connection pool could not be opened: %v\n", err)
		return 1
	}
	defer testPool.Close()

	if err := db.Migrate(ctx, testDSN, order.New().Migrations(), order.ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "the migration could not be applied: %v\n", err)
		return 1
	}

	// The outbox is a CORE schema and the module writes into it inside its own
	// transaction, so the harness has to apply it too — exactly as the
	// composition root applies the core schemas before the module ones.
	if err := db.Migrate(ctx, testDSN, outbox.Migrations(), outbox.MigrationOwner); err != nil {
		fmt.Fprintf(os.Stderr, "the outbox migration could not be applied: %v\n", err)
		return 1
	}

	return m.Run()
}

// newService sets up a service running on a real store and a real event bus;
// the second return value is the bus.
func newService(t *testing.T) (*service.Service, eventbus.EventBus) {
	t.Helper()
	return newServiceWithStore(t, repository.New(testPool.Pool()))
}

// newServiceWithStore sets up a service running on the given store.
//
// The store is a parameter because a single test puts a wrapper ON TOP of the
// real store (see [lookupBarrierStore]); everything else is the same, and
// writing two separate setups would invite a dependency added to one to be
// forgotten in the other.
func newServiceWithStore(t *testing.T, store service.Store) (*service.Service, eventbus.EventBus) {
	t.Helper()

	bus := eventbus.NewInMemory(nil)
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := bus.Shutdown(shutdownCtx); err != nil {
			t.Logf("the event bus could not be shut down: %v", err)
		}
	})

	svc, err := service.New(service.Options{
		Repo:   store,
		Events: bus,
	})
	require.NoError(t, err)
	return svc, bus
}

// validInput produces a consistent order input.
func validInput() service.CreateOrderInput {
	return service.CreateOrderInput{
		RegionID:      testRegionID,
		CustomerID:    testCustomerID,
		Email:         "customer@example.com",
		CurrencyCode:  testCurrency,
		Subtotal:      3000,
		TaxTotal:      600,
		ShippingTotal: 2500,
		Total:         6100,
		Items: []service.CreateOrderItemInput{{
			VariantID: "variant_A", Title: "Red T-Shirt",
			Quantity: 3, UnitPrice: 1000, Subtotal: 3000, TaxTotal: 600, Total: 3600,
		}},
	}
}

// tableExists reports whether the table is present in the database.
func tableExists(ctx context.Context, t *testing.T, table string) bool {
	t.Helper()

	var exists bool
	err := testPool.Pool().QueryRow(ctx,
		`SELECT EXISTS (
             SELECT 1 FROM pg_class c
             JOIN pg_namespace n ON n.oid = c.relnamespace
             WHERE c.relname = $1 AND c.relkind = 'r' AND n.nspname = current_schema()
         )`, table).Scan(&exists)
	require.NoError(t, err)
	return exists
}

// TestMigrationIsReversible verifies that the migration can be applied and
// rolled back (plan Section 8: up/down pairs, reversible).
//
// The test is ORDER sensitive and has to run before the others: it drops the
// schema and sets it up again. Because Go runs the tests of a file in
// declaration order, it stands at the top of the file.
func TestMigrationIsReversible(t *testing.T) {
	ctx := context.Background()
	src := order.New().Migrations()

	for _, table := range moduleTables {
		require.True(t, tableExists(ctx, t, table), "%s must exist at the start", table)
	}

	require.NoError(t, db.MigrateDown(ctx, testDSN, src, order.ModuleName, 0))
	for _, table := range moduleTables {
		assert.False(t, tableExists(ctx, t, table), "%s must not remain after the rollback", table)
	}

	require.NoError(t, db.Migrate(ctx, testDSN, src, order.ModuleName))
	for _, table := range moduleTables {
		assert.True(t, tableExists(ctx, t, table), "%s must be applied again", table)
	}

	version, dirty, err := db.Version(ctx, testDSN, order.ModuleName)
	require.NoError(t, err)
	assert.False(t, dirty, "there must be no half-finished migration")
	assert.Equal(t, highestMigrationVersion(t, src), version,
		"re-applying has to run EVERY migration, not only the last one")
}

// highestMigrationVersion returns the largest version number in the embedded
// migration set.
//
// The number is NOT written out: a literal breaks this test every time a
// migration is added to the module, and what breaks it is not a fault but the
// test's own stale expectation. Read from the set, the assertion also becomes
// the right one — "after the rollback EVERYTHING was applied again" rather than
// "the number is one".
func highestMigrationVersion(t *testing.T, src fs.FS) uint {
	t.Helper()

	entries, err := fs.ReadDir(src, ".")
	require.NoError(t, err)

	var highest uint
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}

		digits := name[:strings.IndexByte(name, '_')]
		n, convErr := strconv.ParseUint(digits, 10, 32)
		require.NoError(t, convErr, "%s does not start with a version number", name)

		if uint(n) > highest {
			highest = uint(n)
		}
	}

	require.Positive(t, highest, "the embedded migration set looks empty")

	return highest
}

// TestNoCrossModuleForeignKeys verifies that ALL the foreign keys in the
// module's tables go to the module's own tables again (Principle 2.2).
//
// In particular orders.region_id (region), orders.customer_id (customer),
// orders.cart_id (cart) and order_line_items.variant_id (product) are other
// modules' ids and CANNOT be foreign keys.
func TestNoCrossModuleForeignKeys(t *testing.T) {
	ctx := context.Background()

	rows, err := testPool.Pool().Query(ctx,
		`SELECT c.conname, src.relname, tgt.relname
         FROM pg_constraint c
         JOIN pg_class src ON src.oid = c.conrelid
         JOIN pg_class tgt ON tgt.oid = c.confrelid
         WHERE c.contype = 'f' AND src.relname = ANY($1)`, moduleTables)
	require.NoError(t, err)
	defer rows.Close()

	owned := make(map[string]struct{}, len(moduleTables))
	for _, table := range moduleTables {
		owned[table] = struct{}{}
	}

	var count int
	for rows.Next() {
		var name, src, tgt string
		require.NoError(t, rows.Scan(&name, &src, &tgt))
		assert.Contains(t, owned, tgt,
			"the %s constraint references outside the module (%s -> %s)", name, src, tgt)
		count++
	}
	require.NoError(t, rows.Err())
	assert.Positive(t, count, "in-module foreign keys must be used")
}

// TestOrderLifecycle verifies the order's end-to-end flow: create -> read ->
// read by number -> complete -> archive.
func TestOrderLifecycle(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	in := validInput()
	in.Email = "Customer@Example.COM"
	in.CurrencyCode = "try"
	in.CartID = "cart_LIFECYCLE"
	in.Metadata = map[string]any{"channel": "web"}

	ord, err := svc.CreateOrder(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, "TRY", ord.CurrencyCode)
	assert.Equal(t, "customer@example.com", ord.Email)
	assert.Equal(t, models.OrderPending, ord.Status)
	assert.Positive(t, ord.DisplayID, "the number must be produced by the database")
	assert.Equal(t, "UTC", ord.PlacedAt.Location().String(), "the time must be UTC")
	assert.Equal(t, map[string]any{"channel": "web"}, ord.Metadata)

	detail, err := svc.GetOrder(ctx, ord.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1)
	assert.Equal(t, int64(3600), detail.Items[0].Total)
	assert.Equal(t, ord.ID, detail.Summary.OrderID)
	assert.Equal(t, int64(6100), detail.Summary.Outstanding(detail.Total))

	byNumber, err := svc.GetOrderByDisplayID(ctx, ord.DisplayID)
	require.NoError(t, err)
	assert.Equal(t, ord.ID, byNumber.ID)

	// The region and the customer stand IN THE ORDER'S OWN COLUMNS; that is the
	// only place the relation lives.
	assert.Equal(t, testRegionID, ord.RegionID)
	assert.Equal(t, testCustomerID, ord.CustomerID)

	completed, err := svc.CompleteOrder(ctx, ord.ID)
	require.NoError(t, err)
	require.NotNil(t, completed.CompletedAt)
	assert.Equal(t, "UTC", completed.CompletedAt.Location().String())

	archived, err := svc.ArchiveOrder(ctx, ord.ID)
	require.NoError(t, err)
	assert.Equal(t, models.OrderArchived, archived.Status)

	// An archived order can no longer be canceled.
	err = svc.CancelOrder(ctx, ord.ID, "too late")
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "got: %v", err)
}

// TestConcurrentOrderNumbersAreUnique proves the DoD's most critical claim
// under a real race.
//
// Twenty goroutines open an order AT THE SAME TIME; because they all wait on a
// single barrier, the writes really do collide. Had the numbers been produced
// in the application layer with "read the largest, add one", this test would
// INEVITABLY have shown a collision; with an IDENTITY column (a sequence) a
// collision is structurally impossible.
func TestConcurrentOrderNumbersAreUnique(t *testing.T) {
	const count = 20

	ctx := context.Background()
	svc, _ := newService(t)

	var (
		start   sync.WaitGroup
		finish  sync.WaitGroup
		results = make([]models.Order, count)
		errs    = make([]error, count)
	)
	start.Add(1)
	finish.Add(count)

	for i := range count {
		go func(idx int) {
			defer finish.Done()
			start.Wait()

			in := validInput()
			in.CartID = fmt.Sprintf("cart_RACE_%02d", idx)
			results[idx], errs[idx] = svc.CreateOrder(ctx, in)
		}(i)
	}

	start.Done()
	finish.Wait()

	numbers := make(map[int64]string, count)
	ids := make(map[string]struct{}, count)
	for i := range count {
		require.NoError(t, errs[i], "order %d could not be opened", i)

		number := results[i].DisplayID
		assert.True(t, models.ValidDisplayID(number), "the number must be valid: %d", number)

		previous, clash := numbers[number]
		require.False(t, clash,
			"two orders took the same number (%d): %s and %s", number, previous, results[i].ID)
		numbers[number] = results[i].ID

		_, idClash := ids[results[i].ID]
		require.False(t, idClash, "two orders took the same id: %s", results[i].ID)
		ids[results[i].ID] = struct{}{}
	}
	assert.Len(t, numbers, count, "all the numbers must be unique")
}

// TestCancelOrderCanBeCalledTwice verifies the idempotency of the saga
// compensation on a real database (a DoD condition).
func TestCancelOrderCanBeCalledTwice(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	ord, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	require.NoError(t, svc.CancelOrder(ctx, ord.ID, "the payment was declined"))
	require.NoError(t, svc.CancelOrder(ctx, ord.ID, "compensation retry"),
		"the second cancellation must not return an error")

	canceled, err := svc.GetOrder(ctx, ord.ID)
	require.NoError(t, err)
	assert.Equal(t, models.OrderCanceled, canceled.Status)
	require.NotNil(t, canceled.CanceledAt)
	assert.Equal(t, "the payment was declined", canceled.CancelReason,
		"the first cancellation's reason must be preserved")
}

// TestOrderPlacedEventIsReallyPublished verifies the DoD condition with a REAL
// subscriber.
//
// The unit test sees the publication over a fake bus; what is exercised here is
// that the event is DELIVERED to a subscriber over a real [eventbus.EventBus].
// The InMemory backend runs the handlers in separate goroutines and Publish does
// NOT WAIT for them; this is why the test waits on a channel.
func TestOrderPlacedEventIsReallyPublished(t *testing.T) {
	ctx := context.Background()
	svc, bus := newService(t)

	delivered := make(chan eventbus.Event, 1)
	require.NoError(t, bus.Subscribe(service.EventOrderPlaced, func(_ context.Context, e eventbus.Event) error {
		delivered <- e
		return nil
	}))

	ord, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	select {
	case event := <-delivered:
		assert.Equal(t, service.EventOrderPlaced, event.Name)
		assert.Equal(t, ord.ID, event.Data[service.EventFieldOrderID])
		assert.Equal(t, strconv.FormatInt(ord.DisplayID, 10),
			event.Data[service.EventFieldDisplayID])
		assert.Equal(t, "6100", event.Data[service.EventFieldTotal])
		assert.Equal(t, ord.CurrencyCode, event.Data[service.EventFieldCurrencyCode])
		assert.Equal(t, ord.CustomerID, event.Data[service.EventFieldCustomerID])
		assert.NotEmpty(t, event.ID, "the bus must give the event an id")
	case <-time.After(5 * time.Second):
		t.Fatal("the order.placed event was not delivered to the subscriber")
	}
}

// TestOrderPlacedEventKeepsItsTypesOverRedis verifies that the event payload
// arrives with the same type and the same value on the PRODUCTION bus too.
//
// The difference shows up only here: the Redis Streams backend writes Data with
// json.Marshal and resolves it into a map[string]any when reading, so a field
// put in as an int64 would reach the subscriber as a float64 — the same field
// would have stayed an int64 on the InMemory backend. A subscriber written
// against the contract would work in development and fall over IN PRODUCTION;
// on top of that, amounts above 2^53 are silently rounded, that is, money would
// travel over a float (plan Section 8: NEVER a float).
//
// The order total is deliberately above 2^53: a path that goes through a float64
// changes the value here.
func TestOrderPlacedEventKeepsItsTypesOverRedis(t *testing.T) {
	const largeAmount int64 = 9_007_199_254_740_993

	ctx := context.Background()
	bus := redisEventBus(t)

	svc, err := service.New(service.Options{
		Repo:   repository.New(testPool.Pool()),
		Events: bus,
	})
	require.NoError(t, err)

	delivered := make(chan eventbus.Event, 1)
	require.NoError(t, bus.Subscribe(service.EventOrderPlaced,
		func(_ context.Context, e eventbus.Event) error {
			delivered <- e
			return nil
		}))

	in := validInput()
	in.ShippingTotal = largeAmount - 3600
	in.Total = largeAmount
	ord, err := svc.CreateOrder(ctx, in)
	require.NoError(t, err)

	select {
	case event := <-delivered:
		for key, value := range event.Data {
			assert.IsType(t, "", value,
				"the %q field must come back from Redis as a string", key)
		}

		rawTotal, isString := event.Data[service.EventFieldTotal].(string)
		require.True(t, isString, "the amount must travel as a string")
		parsed, parseErr := strconv.ParseInt(rawTotal, 10, 64)
		require.NoError(t, parseErr)
		assert.Equal(t, ord.Total, parsed, "the amount must be read back without rounding")
		assert.Equal(t, largeAmount, parsed)
	case <-time.After(15 * time.Second):
		t.Fatal("the order.placed event was not delivered over redis")
	}
}

// redisEventBus sets up the real event bus on a Redis that lives for the
// duration of the test.
//
// The container is opened here rather than in TestMain because only this test
// needs it: none of the other tests touch Redis, and a second container per
// package would add a cost to every run.
func redisEventBus(t *testing.T) eventbus.EventBus {
	t.Helper()

	ctx := t.Context()
	ctr, err := tcredis.Run(ctx, redisImage)
	testcontainers.CleanupContainer(t, ctr)
	require.NoError(t, err, "the redis container could not be started")

	uri, err := ctr.ConnectionString(ctx)
	require.NoError(t, err)
	opts, err := redis.ParseURL(uri)
	require.NoError(t, err)

	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Ping(ctx).Err())

	bus, err := eventbus.NewRedisStream(client, eventbus.RedisConfig{
		StreamPrefix: "gobit-test:" + t.Name(),
		Group:        "group-" + t.Name(),
		Consumer:     "consumer-1",
		BlockTimeout: 200 * time.Millisecond,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := bus.Shutdown(shutdownCtx); err != nil {
			t.Logf("the redis event bus could not be shut down: %v", err)
		}
	})
	return bus
}

// TestTotalConstraintInTheDatabase verifies that an inconsistent total is
// rejected at the database level too.
//
// The service makes the same check first, with a more readable error; the
// constraint here is the LAST DEFENSE and it covers an intervention made
// directly with SQL as well.
func TestTotalConstraintInTheDatabase(t *testing.T) {
	ctx := context.Background()

	_, err := testPool.Pool().Exec(ctx,
		`INSERT INTO orders (id, region_id, currency_code, subtotal, tax_total, shipping_total, total)
         VALUES ('order_CONSTRAINT', $1, $2, 3000, 600, 2500, 9999)`, testRegionID, testCurrency)

	require.Error(t, err, "an inconsistent total must hit the database constraint")
	assert.Contains(t, err.Error(), "orders_totals_consistent")
}

// TestExcessiveDiscountConstraintInTheDatabase verifies at the database level
// that a discount cannot exceed the subtotal.
//
// The scenario is the case where the identity check ALONE is not enough:
// subtotal=1000, discount=3000, shipping=2500 -> total=500. The identity HOLDS
// and the total does not even go negative; the only thing that catches it is the
// discount bound.
func TestExcessiveDiscountConstraintInTheDatabase(t *testing.T) {
	ctx := context.Background()

	_, err := testPool.Pool().Exec(ctx,
		`INSERT INTO orders (id, region_id, currency_code,
                             subtotal, discount_total, tax_total, shipping_total, total)
         VALUES ('order_DISCOUNT', $1, $2, 1000, 3000, 0, 2500, 500)`, testRegionID, testCurrency)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "orders_discount_within_subtotal")
}

// TestNumberUniquenessConstraint verifies that the same number cannot be
// written a second time even if the sequence is rewound by hand.
//
// Because display_id is GENERATED ALWAYS the value cannot be given in the
// INSERT; the only way to exercise the constraint is to rewind the sequence —
// and that is exactly the reason the constraint exists.
func TestNumberUniquenessConstraint(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	first, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	// Rewind the sequence so that it produces the first order's number again.
	_, err = testPool.Pool().Exec(ctx,
		`SELECT setval(pg_get_serial_sequence('orders', 'display_id'), $1, false)`, first.DisplayID)
	require.NoError(t, err)

	_, err = svc.CreateOrder(ctx, validInput())

	require.Error(t, err, "the same number must not be writable a second time")
	assert.True(t, errors.IsConflict(err), "got: %v", err)

	// Wind the sequence forward again to avoid affecting the later tests.
	var highest int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT COALESCE(MAX(display_id), 0) FROM orders`).Scan(&highest))
	_, err = testPool.Pool().Exec(ctx,
		`SELECT setval(pg_get_serial_sequence('orders', 'display_id'), $1, true)`, highest)
	require.NoError(t, err)
}

// TestIdempotencyKeyTakesTheCheapPathOnASequentialCall verifies that a SECOND,
// SEQUENTIAL call made with the same key returns the existing order.
//
// What is exercised is NOT the unique INDEX: the second call short-circuits on
// CreateOrder's cheap path (the lookup by key) and never reaches the INSERT.
// That the index really is in place is proved only by the concurrent scenario
// ([TestConcurrentCallsWithTheSameKeyOpenOneOrder]); together the two cover both
// layers of the protection.
func TestIdempotencyKeyTakesTheCheapPathOnASequentialCall(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	in := validInput()
	in.IdempotencyKey = "wf_" + models.NewOrderID()

	first, err := svc.CreateOrder(ctx, in)
	require.NoError(t, err)

	second, err := svc.CreateOrder(ctx, in)
	require.NoError(t, err, "a second call with the same key must return the existing order")
	assert.Equal(t, first.ID, second.ID)
	assert.Equal(t, first.DisplayID, second.DisplayID)
}

// lookupBarrierStore is the store wrapper that gathers the calls at a single
// point AFTER the idempotency LOOKUP.
//
// It is meant only for [TestConcurrentCallsWithTheSameKeyOpenOneOrder] and it
// does the single thing whose rationale is written there: it makes sure that
// EVERYONE who performs the cheap path's lookup sees "no record" before any of
// them starts writing. All the remaining methods go to the real store.
type lookupBarrierStore struct {
	*repository.Repository
	// count is the number of calls expected to meet at the barrier.
	count int64
	// arrived counts the calls that reach the barrier.
	arrived atomic.Int64
	// release CLOSES when the last one arrives; everyone waiting is resolved at
	// the same moment, and the calls that come after it has closed do not wait at
	// all (such as the second lookup made by the call that loses the race).
	release chan struct{}
}

// GetOrderByIdempotencyKey performs the lookup and holds the first
// [lookupBarrierStore.count] calls at the barrier.
func (d *lookupBarrierStore) GetOrderByIdempotencyKey(ctx context.Context, key string) (models.Order, error) {
	// The variable name does NOT SHADOW the package name (order): shadowing
	// would make a call written with the package name further down the file fail
	// to compile and be harder to read.
	ord, err := d.Repository.GetOrderByIdempotencyKey(ctx, key)
	if d.arrived.Add(1) == d.count {
		close(d.release)
	}
	<-d.release
	return ord, err
}

// TestConcurrentCallsWithTheSameKeyOpenOneOrder proves the module's most
// critical saga guarantee over the REAL index.
//
// Sixteen goroutines try to open an order with the SAME idempotency key. None of
// them finds the record on the cheap path's lookup, all of them go on to write,
// and only one INSERT gets through; the rest hit orders_idempotency_key_uniq,
// read the record again and return the SAME order.
//
// A test built out of sequential calls cannot show this: the second call
// short-circuits on the cheap path and never reaches the INSERT, so it would
// stay green even if the index were removed. Meeting the goroutines only at the
// start is not enough either — it was measured: the winner commits so fast that
// the LOOKUP of the rest finds the record, so again nobody reaches the INSERT,
// that is, the test stays green even if the index is dropped.
//
// This is why the barrier is AFTER the lookup ([lookupBarrierStore]): all
// sixteen calls are released once they have seen "no record", and all of them go
// on to WRITE. The scenario now depends on structure rather than on timing, and
// when the index is dropped it INEVITABLY produces more than one order.
func TestConcurrentCallsWithTheSameKeyOpenOneOrder(t *testing.T) {
	const count = 16

	ctx := context.Background()

	svc, _ := newServiceWithStore(t, &lookupBarrierStore{
		Repository: repository.New(testPool.Pool()),
		count:      count,
		release:    make(chan struct{}),
	})

	key := "wf_" + models.NewOrderID()

	var (
		finish  sync.WaitGroup
		results = make([]models.Order, count)
		errs    = make([]error, count)
	)
	finish.Add(count)

	for i := range count {
		go func(idx int) {
			defer finish.Done()

			in := validInput()
			in.IdempotencyKey = key
			in.CartID = fmt.Sprintf("cart_IDEM_%02d", idx)
			results[idx], errs[idx] = svc.CreateOrder(ctx, in)
		}(i)
	}

	finish.Wait()

	ids := make(map[string]struct{}, count)
	for i := range count {
		require.NoError(t, errs[i], "call %d must not return an error", i)
		ids[results[i].ID] = struct{}{}
	}
	assert.Len(t, ids, 1, "all the calls must return the SAME order: %v", ids)

	var written int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM orders WHERE idempotency_key = $1`, key).Scan(&written))
	assert.Equal(t, int64(1), written, "only a single row must be written with the same key")

	// The returned id must be RESOLVABLE: the order returned by the losing calls
	// really does exist and its lines are in place too.
	for id := range ids {
		detail, err := svc.GetOrder(ctx, id)
		require.NoError(t, err, "the returned id must be resolvable")
		assert.Len(t, detail.Items, 1)
	}
}

// TestSummaryTotalsDoNotEraseTheRefundOnALateNotification verifies over the real
// query that writing the summary is INDEPENDENT OF ORDER.
//
// The merge (GREATEST) is in the query itself; the unit test sees the fake's
// imitation, whereas what is exercised here is that PostgreSQL really does
// behave that way.
func TestSummaryTotalsDoNotEraseTheRefundOnALateNotification(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	ord, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	_, err = svc.SetOrderSummaryTotals(ctx, ord.ID,
		service.SummaryTotalsInput{PaidTotal: 6100, RefundedTotal: 1000})
	require.NoError(t, err)

	// A late capture event is being processed again; it knows nothing about the
	// refund.
	late, err := svc.SetOrderSummaryTotals(ctx, ord.ID,
		service.SummaryTotalsInput{PaidTotal: 6100, RefundedTotal: 0})
	require.NoError(t, err)
	assert.Equal(t, int64(1000), late.RefundedTotal,
		"a recorded refund must not be erased by a late notification")

	var stored int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT refunded_total FROM order_summaries WHERE order_id = $1`, ord.ID).Scan(&stored))
	assert.Equal(t, int64(1000), stored)
}

// TestAftersalesRecordsOnTheRealDatabase verifies that the return/exchange/claim
// skeleton works on the real schema.
func TestAftersalesRecordsOnTheRealDatabase(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	ord, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	returned, err := svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: ord.ID, RefundAmount: 3600, Reason: "the size did not fit",
	})
	require.NoError(t, err)
	assert.Equal(t, models.ReturnRequested, returned.Status)

	exchange, err := svc.CreateExchange(ctx, service.CreateExchangeInput{
		OrderID: ord.ID, DifferenceDue: -500,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(-500), exchange.DifferenceDue,
		"a negative difference must be storable in the database too")

	claim, err := svc.CreateClaim(ctx, service.CreateClaimInput{
		OrderID: ord.ID, Type: models.ClaimReplace, Reason: "it arrived broken",
	})
	require.NoError(t, err)
	assert.Equal(t, models.ClaimReplace, claim.Type)

	returns, count, err := svc.ListReturns(ctx, ord.ID, service.Page{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
	require.Len(t, returns, 1)

	// If the order is deleted its children fall with it (in-module ON DELETE
	// CASCADE).
	_, err = testPool.Pool().Exec(ctx, `DELETE FROM orders WHERE id = $1`, ord.ID)
	require.NoError(t, err)

	var remaining int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM order_returns WHERE order_id = $1`, ord.ID).Scan(&remaining))
	assert.Zero(t, remaining, "when the order is deleted the return record must fall too")
}

// TestSummaryTotalsOnTheRealDatabase verifies writing the summary on the real
// schema.
func TestSummaryTotalsOnTheRealDatabase(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	ord, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	summary, err := svc.SetOrderSummaryTotals(ctx, ord.ID,
		service.SummaryTotalsInput{PaidTotal: 6100, RefundedTotal: 1000})
	require.NoError(t, err)
	assert.Equal(t, int64(6100), summary.PaidTotal)
	assert.Equal(t, int64(1000), summary.RefundedTotal)
	assert.Equal(t, int64(1000), summary.Outstanding(ord.Total))

	// Refunding an amount that was not captured hits the database constraint too.
	_, err = testPool.Pool().Exec(ctx,
		`UPDATE order_summaries SET refunded_total = paid_total + 1 WHERE order_id = $1`, ord.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "order_summaries_refund_within_paid")
}

// TestOrderContactJSONOpensTheEmailToTheEventSubscriber verifies the
// notification path end to end: the event comes from the real bus, the e-mail is
// read from the real database.
//
// What is proved is that the two parts work TOGETHER. The "order.placed" payload
// carries no personal data, so a subscriber that is going to send a notification
// CANNOT get the e-mail from the event; all it holds is the order_id and it HAS
// TO READ the order. The unit test proves the surface's schema over a fake
// store; what is exercised here is that the subscriber really can find the real
// record with that id.
func TestOrderContactJSONOpensTheEmailToTheEventSubscriber(t *testing.T) {
	ctx := context.Background()
	svc, bus := newService(t)
	interop := service.NewInterop(svc)

	delivered := make(chan eventbus.Event, 1)
	require.NoError(t, bus.Subscribe(service.EventOrderPlaced, func(_ context.Context, e eventbus.Event) error {
		delivered <- e
		return nil
	}))

	in := validInput()
	in.Items = append(in.Items, service.CreateOrderItemInput{
		VariantID: "variant_B", Title: "Blue Mug",
		Quantity: 1, UnitPrice: 500, Subtotal: 500, TaxTotal: 100, Total: 600,
	})
	in.Subtotal = 3500
	in.TaxTotal = 700
	in.Total = 6700
	ord, err := svc.CreateOrder(ctx, in)
	require.NoError(t, err)

	var event eventbus.Event
	select {
	case event = <-delivered:
	case <-time.After(5 * time.Second):
		t.Fatal("the order.placed event was not delivered to the subscriber")
	}
	require.NotContains(t, event.Data, "email", "the event MUST NOT carry personal data")

	orderID, ok := event.Data[service.EventFieldOrderID].(string)
	require.True(t, ok, "order_id must travel as a string")

	body, err := interop.OrderContactJSON(ctx, orderID)
	require.NoError(t, err)

	// map[string]string exercises the contract itself: had a field been written
	// as a number, the unmarshalling would fall over here.
	var fields map[string]string
	require.NoError(t, json.Unmarshal(body, &fields))

	assert.Equal(t, map[string]string{
		"order_id":      ord.ID,
		"display_id":    strconv.FormatInt(ord.DisplayID, 10),
		"email":         "customer@example.com",
		"currency_code": testCurrency,
		"total":         "6700",
		"item_count":    "2",
	}, fields)

	// The fields the event carries must carry the SAME name and the SAME value
	// as the surface; a subscriber uses the two side by side.
	for _, field := range []string{
		service.EventFieldDisplayID,
		service.EventFieldCurrencyCode,
		service.EventFieldTotal,
		service.EventFieldItemCount,
	} {
		assert.Equal(t, event.Data[field], fields[field], "the %q field must not drift from the event", field)
	}
}

// TestOrderContactJSONOnAMissingOrder verifies that NotFound is returned on the
// real database too.
//
// The id the subscriber holds may have been deleted or may never have been
// written; being able to make the distinction BY THE ERROR TYPE is the only way
// for the notification side to tell "skip" apart from "retry".
func TestOrderContactJSONOnAMissingOrder(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	interop := service.NewInterop(svc)

	_, err := interop.OrderContactJSON(ctx, "order_MISSING")

	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "got: %v", err)
}

// TestTheOutboxRefusesToBeWrittenOutsideATransaction is the repository's own
// guard, and it is exercised HERE because no unit test can reach it: the
// service's tests run on a fake store, so the rule they check is the fake's.
//
// The rule is the whole guarantee. An outbox row written outside a transaction
// promises an event for work that may never commit — the exact fault the outbox
// exists to prevent, wearing the appearance of preventing it.
func TestTheOutboxRefusesToBeWrittenOutsideATransaction(t *testing.T) {
	ctx := context.Background()
	repo := repository.New(testPool.Pool())

	err := repo.WriteOutboxEvent(ctx, "order.placed:order_x", "order.placed",
		map[string]any{"order_id": "order_x"})

	require.Error(t, err)
	assert.Equal(t, errors.KindInternal, errors.KindOf(err))
	assert.Contains(t, err.Error(), "transaction")
}

// TestThePromisedEventCommitsWithTheOrder proves the guarantee against a REAL
// database, which is the only place it can be proven: what is being claimed is
// that two writes share one transaction.
func TestThePromisedEventCommitsWithTheOrder(t *testing.T) {
	ctx := context.Background()
	repo := repository.New(testPool.Pool())

	var orderID string
	require.NoError(t, repo.WithTx(ctx, func(ctx context.Context) error {
		created, err := repo.CreateOrder(ctx, models.Order{
			ID:           models.NewOrderID(),
			Status:       models.OrderPending,
			RegionID:     testRegionID,
			CurrencyCode: testCurrency,
			Email:        "outbox@example.com",
			// The totals have to add up: the schema enforces
			// total = subtotal - discount + tax + shipping.
			Subtotal: 1000,
			Total:    1000,
		})
		if err != nil {
			return err
		}
		orderID = created.ID

		return repo.WriteOutboxEvent(ctx, "order.placed:"+created.ID, "order.placed",
			map[string]any{"order_id": created.ID})
	}))

	var name string
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT name FROM event_outbox WHERE id = $1`, "order.placed:"+orderID).Scan(&name))
	assert.Equal(t, "order.placed", name)
}

// TestARolledBackOrderLeavesNoPromise is the other half of "commits with".
//
// If the event outlived a rolled-back order the relay would announce an order
// that does not exist, which is worse than the silence the outbox replaced.
func TestARolledBackOrderLeavesNoPromise(t *testing.T) {
	ctx := context.Background()
	repo := repository.New(testPool.Pool())

	eventID := "order.placed:" + models.NewOrderID()
	wanted := errors.Internal("test_rollback", "rolled back on purpose")

	err := repo.WithTx(ctx, func(ctx context.Context) error {
		if err := repo.WriteOutboxEvent(ctx, eventID, "order.placed", nil); err != nil {
			return err
		}

		return wanted
	})
	require.Error(t, err)

	var count int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM event_outbox WHERE id = $1`, eventID).Scan(&count))
	assert.Zero(t, count, "a promise must not outlive the work that promised it")
}
