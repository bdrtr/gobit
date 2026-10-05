//go:build integration

// The tests in this file need a real PostgreSQL instance (and therefore
// Docker); they are separated behind the `integration` tag so `make test` stays
// fast. To run them: make test-integration
//
// The unit tests prove the service's DECISIONS against a fake store. The tests
// here prove the GROUND those decisions stand on: that the migration can be
// rolled back, that the constraints are really enforced and that the
// concurrency claim holds at the database level. The claim "two concurrent
// Reserve calls cannot both take the same last unit" in particular can only be
// tried here, with real goroutines on real row locks.
package inventory_test

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/link"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/inventory"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/repository"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
	"github.com/bdrtr/gobit/internal/testdb"
)

const postgresImage = "postgres:16-alpine"

// moduleTables are the tables the module owns; the migration tests use this
// list.
var moduleTables = []string{
	"stock_locations", "inventory_items", "inventory_levels", "inventory_reservations",
	"inventory_movements", "inventory_backorders",
}

var (
	// testPool is the pool every test shares.
	testPool *db.Pool
	// testDSN is the connection address for the migration calls.
	testDSN string
)

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres starts a single Postgres container and runs every test on
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

	testDSN, err = ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "the connection string could not be read: %v\n", err)
		return 1
	}

	cfg := db.DefaultConfig(testDSN)
	// The concurrency test runs dozens of goroutines at once; since every
	// transaction holds a connection, the pool is opened wider than the default.
	cfg.MaxConns = 24
	testPool, err = db.New(ctx, cfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "the connection pool could not be opened: %v\n", err)
		return 1
	}
	defer testPool.Close()

	if err := db.Migrate(ctx, testDSN, inventory.New().Migrations(), inventory.ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "the migration could not be applied: %v\n", err)
		return 1
	}

	return m.Run()
}

// newDBService builds a service that works on the real store.
func newDBService(t *testing.T) *service.Service {
	t.Helper()

	return service.New(repository.New(testPool.Pool()), nil)
}

// newItem creates an inventory item with a unique SKU for the test.
func newItem(ctx context.Context, t *testing.T, svc *service.Service) models.InventoryItem {
	t.Helper()

	item, err := svc.CreateInventoryItem(ctx, service.CreateInventoryItemInput{
		SKU:   "SKU-" + models.NewInventoryItemID(),
		Title: t.Name(),
	})
	require.NoError(t, err)
	return item
}

// newLocation creates a stock location for the test.
func newLocation(ctx context.Context, t *testing.T, svc *service.Service) models.StockLocation {
	t.Helper()

	loc, err := svc.CreateStockLocation(ctx, service.CreateStockLocationInput{
		Name:        "Warehouse " + t.Name(),
		City:        "Istanbul",
		CountryCode: "TR",
	})
	require.NoError(t, err)
	return loc
}

// withStock sets up an item, a location and a level with the given physical
// quantity.
func withStock(ctx context.Context, t *testing.T, svc *service.Service, quantity int64) (models.InventoryItem, models.StockLocation) {
	t.Helper()

	item := newItem(ctx, t, svc)
	loc := newLocation(ctx, t, svc)
	_, err := svc.SetInventoryLevel(ctx, item.ID, loc.ID, quantity)
	require.NoError(t, err)
	return item, loc
}

// TestTheMigrationCanBeRolledBack verifies that the migrations can be applied
// and rolled back (plan Section 8: up/down pairs, reversible).
func TestTheMigrationCanBeRolledBack(t *testing.T) {
	ctx := context.Background()
	src := inventory.New().Migrations()
	// The rollback runs in a database of its own. In the one this package
	// shares it would drop every other test's stock with the schema (D141).
	dsn := testdb.New(t, testDSN, "inventory_migration")
	require.NoError(t, db.Migrate(ctx, dsn, src, inventory.ModuleName))

	for _, table := range moduleTables {
		require.True(t, testdb.TableExists(t, dsn, table), "%s must exist at the start", table)
	}

	require.NoError(t, db.MigrateDown(ctx, dsn, src, inventory.ModuleName, 0))
	for _, table := range moduleTables {
		assert.False(t, testdb.TableExists(t, dsn, table), "%s must not remain after the rollback", table)
	}

	require.NoError(t, db.Migrate(ctx, dsn, src, inventory.ModuleName))
	for _, table := range moduleTables {
		assert.True(t, testdb.TableExists(t, dsn, table), "%s must be applied again", table)
	}

	version, dirty, err := db.Version(ctx, dsn, inventory.ModuleName)
	require.NoError(t, err)
	assert.False(t, dirty, "no migration may be left half-applied")
	assert.Equal(t, highestMigrationVersion(t, src), version)
}

// highestMigrationVersion returns the largest version number in the embedded
// migration set.
//
// The number is NOT WRITTEN BY HAND: a literal number breaks this test every
// time a migration is added to the module, and what breaks it is not a defect
// but the test's own stale expectation — exactly that happened when 000005 was
// added. Read from the set, the claim also becomes the right one: "after the
// rollback EVERYTHING was applied again", not "the number is five". The
// order module's twin of this helper carries the same name; it is repeated
// because test packages cannot import each other.
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

// TestNoCrossModuleForeignKey verifies that EVERY foreign key in the module's
// tables points back at the module's own tables (Principle 2.2).
//
// inventory_reservations.line_item_id in particular is an ID that belongs to
// the cart module and CANNOT be a foreign key; this test shows that the rule
// really holds in the schema.
func TestNoCrossModuleForeignKey(t *testing.T) {
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

	var foreignKeys int
	for rows.Next() {
		var name, src, tgt string
		require.NoError(t, rows.Scan(&name, &src, &tgt))
		assert.Contains(t, owned, tgt,
			"the %s constraint references outside the module (%s -> %s)", name, src, tgt)
		foreignKeys++
	}
	require.NoError(t, rows.Err())
	assert.Positive(t, foreignKeys, "foreign keys inside the module must be in use")
}

// TestItemLifecycle verifies creating, reading, listing and soft-deleting an
// item end to end.
func TestItemLifecycle(t *testing.T) {
	ctx := context.Background()
	svc := newDBService(t)

	sku := "SKU-" + models.NewInventoryItemID()
	item, err := svc.CreateInventoryItem(ctx, service.CreateInventoryItemInput{
		SKU: sku, Title: "Red T-Shirt", Description: "Size M",
	})
	require.NoError(t, err)
	assert.True(t, item.RequiresShipping)
	assert.False(t, item.CreatedAt.IsZero(), "created_at must come from the database")
	assert.Equal(t, item.CreatedAt.Location().String(), "UTC", "the time must be UTC")

	fetched, err := svc.GetInventoryItem(ctx, item.ID)
	require.NoError(t, err)
	assert.Equal(t, item.ID, fetched.ID)
	assert.Equal(t, "Red T-Shirt", fetched.Title)

	// The same SKU cannot be taken a second time.
	_, err = svc.CreateInventoryItem(ctx, service.CreateInventoryItemInput{SKU: sku})
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))

	items, count, err := svc.ListInventoryItems(ctx, service.ListInventoryItemsInput{SKU: &sku})
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, int64(1), count)

	require.NoError(t, svc.DeleteInventoryItem(ctx, item.ID))

	_, err = svc.GetInventoryItem(ctx, item.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err), "a soft-deleted item must not be readable")

	_, count, err = svc.ListInventoryItems(ctx, service.ListInventoryItemsInput{SKU: &sku})
	require.NoError(t, err)
	assert.Zero(t, count, "a soft-deleted item must not appear in the listing")

	// The SKU must be reusable: uniqueness holds only among living items.
	_, err = svc.CreateInventoryItem(ctx, service.CreateInventoryItemInput{SKU: sku})
	require.NoError(t, err, "the SKU of a deleted item must be reusable")
}

// TestStockLevelAndAvailableQuantity verifies writing and adjusting a level and
// the sum of the available quantity across locations.
func TestStockLevelAndAvailableQuantity(t *testing.T) {
	ctx := context.Background()
	svc := newDBService(t)

	item := newItem(ctx, t, svc)
	locA := newLocation(ctx, t, svc)
	locB := newLocation(ctx, t, svc)

	_, err := svc.SetInventoryLevel(ctx, item.ID, locA.ID, 10)
	require.NoError(t, err)
	_, err = svc.SetInventoryLevel(ctx, item.ID, locB.ID, 5)
	require.NoError(t, err)

	available, err := svc.AvailableQuantity(ctx, item.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(15), available)

	// The same call a second time does not create a level, it updates it.
	level, err := svc.SetInventoryLevel(ctx, item.ID, locA.ID, 7)
	require.NoError(t, err)
	assert.Equal(t, int64(7), level.StockedQuantity)

	levels, err := svc.ListInventoryLevels(ctx, item.ID)
	require.NoError(t, err)
	assert.Len(t, levels, 2, "there must be a single level per location")

	level, err = svc.AdjustInventory(ctx, item.ID, locB.ID, -3)
	require.NoError(t, err)
	assert.Equal(t, int64(2), level.StockedQuantity)

	_, err = svc.AdjustInventory(ctx, item.ID, locB.ID, -3)
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err), "stock cannot fall below zero")

	available, err = svc.AvailableQuantity(ctx, item.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(9), available, "7 + 2 = 9")
}

// TestALevelCannotBeOpenedAtAMissingLocation proves that a level cannot be
// opened at a location that does not exist.
//
// Until 2026-09-08 the error was CLASSIFIED out of the driver's foreign key
// violation. It now comes from the shared location lock that is the first step
// of the write path (ADR 0055), with the foreign key still in place as the last
// defense. The error CODE is unchanged and so is what the caller sees of it:
// something the client can fix stays a 404 rather than a 500.
func TestALevelCannotBeOpenedAtAMissingLocation(t *testing.T) {
	ctx := context.Background()
	svc := newDBService(t)
	item := newItem(ctx, t, svc)

	_, err := svc.SetInventoryLevel(ctx, item.ID, "sloc_MISSING", 5)

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
	assert.Equal(t, "inventory_location_not_found", errors.CodeOf(err))
}

// TestAvailableQuantityAgreesInSQLAndInTheService verifies that the batch
// availability (computed on the SQL side) and the calculation over a single
// item (summed on the Go side) give the same number.
//
// Both paths are needed: one is the query provider's single-round-trip batch
// path, the other the path of the single-item query. If they diverged, stock
// would look different depending on where one looked.
func TestAvailableQuantityAgreesInSQLAndInTheService(t *testing.T) {
	ctx := context.Background()
	svc := newDBService(t)

	item, loc := withStock(ctx, t, svc, 10)
	locB := newLocation(ctx, t, svc)
	_, err := svc.SetInventoryLevel(ctx, item.ID, locB.ID, 6)
	require.NoError(t, err)

	_, err = svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 4,
	})
	require.NoError(t, err)

	single, err := svc.AvailableQuantity(ctx, item.ID)
	require.NoError(t, err)

	batch, err := svc.AvailableQuantities(ctx, []string{item.ID})
	require.NoError(t, err)

	assert.Equal(t, int64(12), single, "(10-4) + 6 = 12")
	assert.Equal(t, single, batch[item.ID], "the two calculation paths must give the same result")
}

// TestLocationsWithStockReturnsTheSufficientLocationsInOrder verifies the
// candidate location list on a real database: a location that does not meet
// the threshold is not in the list, and the order is ascending by location ID.
//
// The fixture writes the levels in REVERSE ID ORDER. This is why:
// ListInventoryLevels returns the rows by created_at, so the order the store
// gives is the exact reverse of the expected order. Had the levels been written
// in ID order, the two orders would coincide and an implementation that does no
// sorting at all would pass this test too.
func TestLocationsWithStockReturnsTheSufficientLocationsInOrder(t *testing.T) {
	ctx := context.Background()
	svc := newDBService(t)

	item := newItem(ctx, t, svc)
	ids := []string{
		newLocation(ctx, t, svc).ID,
		newLocation(ctx, t, svc).ID,
		newLocation(ctx, t, svc).ID,
	}
	slices.Sort(ids)

	quantities := map[string]int64{ids[0]: 10, ids[1]: 4, ids[2]: 6}
	for i := len(ids) - 1; i >= 0; i-- {
		_, err := svc.SetInventoryLevel(ctx, item.ID, ids[i], quantities[ids[i]])
		require.NoError(t, err)
	}

	locations, err := svc.LocationsWithStock(ctx, item.ID, 5)

	require.NoError(t, err)
	assert.Equal(t, []string{ids[0], ids[2]}, locations,
		"the location with 4 units does not meet the threshold; the rest must come back in ID order")
	assert.True(t, slices.IsSorted(locations), "the order must be deterministic")
}

// TestLocationsWithStockDropsACandidateAfterAReservation verifies that a
// reservation brings the candidate list down and that a release brings it back.
//
// The core of the test is the two assertions in the middle: the list and
// [service.Service.Reserve] use the SAME definition of "available". If they
// diverged, a location that appears in the list would get Conflict on the
// reservation, and the saga could not explain why it cannot place an order at a
// warehouse that was a candidate.
func TestLocationsWithStockDropsACandidateAfterAReservation(t *testing.T) {
	ctx := context.Background()
	svc := newDBService(t)
	item, loc := withStock(ctx, t, svc, 10)

	locations, err := svc.LocationsWithStock(ctx, item.ID, 6)
	require.NoError(t, err)
	require.Equal(t, []string{loc.ID}, locations, "it must be a candidate before the reservation")

	res, err := svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 5,
	})
	require.NoError(t, err)

	locations, err = svc.LocationsWithStock(ctx, item.ID, 6)
	require.NoError(t, err)
	assert.Empty(t, locations, "10-5=5 units are not enough for a request of 6")

	_, err = svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 6,
	})
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err),
		"a location that is not in the list must be refused by Reserve as well")

	locations, err = svc.LocationsWithStock(ctx, item.ID, 5)
	require.NoError(t, err)
	assert.Equal(t, []string{loc.ID}, locations, "still a candidate for the remaining 5 units")

	require.NoError(t, svc.ReleaseReservation(ctx, res.ID))

	locations, err = svc.LocationsWithStock(ctx, item.ID, 6)
	require.NoError(t, err)
	assert.Equal(t, []string{loc.ID}, locations, "the release must bring the candidacy back")
}

// TestLocationsWithStockReturnsAnEmptySliceWithoutACandidate verifies that
// with no candidate an EMPTY slice comes back rather than an error, and that
// NotFound comes back for an item that does not exist.
//
// "Not enough stock" is not a fault but an answer; the saga chooses to turn it
// into Conflict in its own context. An item that does not exist, on the other
// hand, is the caller's mistake and must not be mixed up with an empty list.
func TestLocationsWithStockReturnsAnEmptySliceWithoutACandidate(t *testing.T) {
	ctx := context.Background()
	svc := newDBService(t)

	unstocked := newItem(ctx, t, svc)
	locations, err := svc.LocationsWithStock(ctx, unstocked.ID, 1)
	require.NoError(t, err)
	assert.Empty(t, locations, "no candidate for an item with no level at all")
	assert.NotNil(t, locations, "an empty slice must come back, not nil")

	item, _ := withStock(ctx, t, svc, 3)
	locations, err = svc.LocationsWithStock(ctx, item.ID, 4)
	require.NoError(t, err)
	assert.Empty(t, locations, "3 units do not meet a request of 4")
	assert.NotNil(t, locations, "an empty slice must come back, not nil")

	_, err = svc.LocationsWithStock(ctx, "invitem_MISSING", 1)
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestTheInteropLocationSurfaceResolvesByName verifies that the cross-module
// surface can be resolved from the container by its FIXED NAME and through the
// NARROW INTERFACE the consumer will write.
//
// The signature is a contract and the consumer cannot import this module
// (ADR 0006); a drift only becomes visible at resolution time. The test brings
// that moment forward and, by calling the surface once with real data, shows
// that the wiring is right too.
func TestTheInteropLocationSurfaceResolvesByName(t *testing.T) {
	ctx := context.Background()
	c := container.New(nil)
	require.NoError(t, c.Provide("core.db", testPool))
	// The link service is MANDATORY: the module declares the warehouse↔channel
	// link at startup (service.Definitions), and if it cannot declare it, it
	// does not register at all. The product module behaves the same way.
	require.NoError(t, c.Provide("core.link", link.New(testPool, nil)))
	require.NoError(t, inventory.New().Register(ctx, c))

	// An exact copy of the narrow interface the consumer will write in its own
	// package.
	type stockLocations interface {
		LocationsWithStock(ctx context.Context, inventoryItemID string, quantity int64) ([]string, error)
	}

	// The name is written BY HAND; using the constant would turn the test into
	// a tautology.
	surface, err := container.Resolve[stockLocations](c, "inventory.interop")
	require.NoError(t, err, "the surface must be resolvable by its fixed name and through the narrow interface")
	assert.Equal(t, "inventory.interop", inventory.InteropName,
		"if the name changes the consuming flows cannot find the surface")

	svc := newDBService(t)
	item, loc := withStock(ctx, t, svc, 7)

	locations, err := surface.LocationsWithStock(ctx, item.ID, 7)
	require.NoError(t, err)
	assert.Equal(t, []string{loc.ID}, locations, "exactly the last unit is enough as well")

	locations, err = surface.LocationsWithStock(ctx, item.ID, 8)
	require.NoError(t, err)
	assert.Empty(t, locations)
}

// TestConcurrentReservesLeaveTheLastUnitToOneWinner proves the core of the
// concurrency claim: of many calls racing for one last unit, EXACTLY ONE wins.
//
// A "read first, then write" check done in the application layer cannot pass
// this test; that there is a single winner comes from the row lock.
func TestConcurrentReservesLeaveTheLastUnitToOneWinner(t *testing.T) {
	ctx := context.Background()
	svc := newDBService(t)
	item, loc := withStock(ctx, t, svc, 1)

	const contenders = 8
	start := make(chan struct{})
	results := make([]error, contenders)

	var wg sync.WaitGroup
	for i := range contenders {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := svc.Reserve(ctx, service.ReserveInput{
				InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 1,
			})
			results[i] = err
		}(i)
	}
	close(start)
	wg.Wait()

	var winners int
	for i, err := range results {
		if err == nil {
			winners++
			continue
		}
		assert.Equal(t, errors.KindConflict, errors.KindOf(err),
			"losing call %d must get Conflict, it got: %v", i, err)
		assert.Equal(t, service.CodeInsufficientStock, errors.CodeOf(err))
	}
	assert.Equal(t, 1, winners, "exactly one call must take the last unit")

	available, err := svc.AvailableQuantity(ctx, item.ID)
	require.NoError(t, err)
	assert.Zero(t, available)
	assert.Equal(t, int64(1), activeReservationCount(ctx, t, item.ID),
		"there must be as many reservation records as winners")
}

// TestConcurrentReservesDoNotExceedTheStock verifies that when more concurrent
// requests arrive than there is stock, exactly as many win as there is stock.
//
// Unlike the single-unit race there are several winners here; the claim is
// that the TOTAL quantity of the winners does not exceed the stock. Had an
// incremental update been used instead of the lock, the reserved quantity could
// have risen above the stock.
func TestConcurrentReservesDoNotExceedTheStock(t *testing.T) {
	ctx := context.Background()
	svc := newDBService(t)

	const stock = 10
	const contenders = 25
	item, loc := withStock(ctx, t, svc, stock)

	start := make(chan struct{})
	results := make([]error, contenders)

	var wg sync.WaitGroup
	for i := range contenders {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := svc.Reserve(ctx, service.ReserveInput{
				InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 1,
			})
			results[i] = err
		}(i)
	}
	close(start)
	wg.Wait()

	var winners int
	for _, err := range results {
		if err == nil {
			winners++
			continue
		}
		assert.Equal(t, errors.KindConflict, errors.KindOf(err), "unexpected error: %v", err)
	}
	assert.Equal(t, stock, winners)

	levels, err := svc.ListInventoryLevels(ctx, item.ID)
	require.NoError(t, err)
	require.Len(t, levels, 1)
	assert.Equal(t, int64(stock), levels[0].ReservedQuantity, "the reserved quantity cannot exceed the stock")
	assert.Zero(t, levels[0].Available())
	assert.Equal(t, int64(stock), activeReservationCount(ctx, t, item.ID))
}

// TestReserveAndALevelWriteDoNotDeadlock proves that MIXED flows do not
// deadlock each other: Reserve and SetInventoryLevel are raced on the same item
// with real goroutines.
//
// This is a DIFFERENT class of fault from the single-kind races (Reserve x N).
// If two flows lock the same two rows in opposite orders, PostgreSQL detects
// the deadlock (SQLSTATE 40P01) and kills one of the transactions: the
// customer's reservation fails because it collided with the admin's stock
// update — and only after waiting as long as the deadlock timeout, at that. As
// long as the lock order is a SINGLE one, every round passes cleanly; that is
// why the test's claim is "no call gets an error".
//
// Because the stock is written back to 1000 in every round and only 1 unit is
// set aside per round, there is NO call that should fail by a business rule;
// any error seen is therefore a concurrency error.
func TestReserveAndALevelWriteDoNotDeadlock(t *testing.T) {
	ctx := context.Background()
	svc := newDBService(t)

	const stock int64 = 1000
	item, loc := withStock(ctx, t, svc, stock)

	const rounds = 40
	errs := make(chan error, 2*rounds)
	for range rounds {
		start := make(chan struct{})
		var wg sync.WaitGroup

		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, err := svc.Reserve(ctx, service.ReserveInput{
				InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 1,
			})
			errs <- err
		}()
		go func() {
			defer wg.Done()
			<-start
			_, err := svc.SetInventoryLevel(ctx, item.ID, loc.ID, stock)
			errs <- err
		}()

		close(start)
		wg.Wait()
	}
	close(errs)

	for err := range errs {
		require.NoError(t, err, "Reserve and SetInventoryLevel must not deadlock each other")
	}
}

// TestReserveAndAnItemDeleteDoNotDeadlock races Reserve and DeleteInventoryItem
// on the same item and proves two things at once: the flows do not deadlock,
// and EXACTLY ONE of them wins the race.
//
// Whichever wins, the outcome is consistent: if the reservation was written
// first, the delete gets Conflict for "there is an active reservation"; if the
// delete finished first, the reservation cannot find the item. Both succeeding
// would leave an active reservation behind a deleted item.
func TestReserveAndAnItemDeleteDoNotDeadlock(t *testing.T) {
	ctx := context.Background()
	svc := newDBService(t)

	const rounds = 25
	for range rounds {
		item, loc := withStock(ctx, t, svc, 10)

		start := make(chan struct{})
		var errs [2]error
		var wg sync.WaitGroup

		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, errs[0] = svc.Reserve(ctx, service.ReserveInput{
				InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 1,
			})
		}()
		go func() {
			defer wg.Done()
			<-start
			errs[1] = svc.DeleteInventoryItem(ctx, item.ID)
		}()

		close(start)
		wg.Wait()

		if errs[0] != nil {
			assert.Equal(t, errors.KindNotFound, errors.KindOf(errs[0]),
				"the reservation may fail only because the item was deleted: %v", errs[0])
		}
		if errs[1] != nil {
			assert.Equal(t, errors.KindConflict, errors.KindOf(errs[1]),
				"the delete may fail only because of an active reservation: %v", errs[1])
			assert.Equal(t, service.CodeItemHasReservations, errors.CodeOf(errs[1]))
		}
		require.True(t, (errs[0] == nil) != (errs[1] == nil),
			"exactly one must win (reservation: %v, delete: %v)", errs[0], errs[1])
	}
}

// TestADeadlockIsClassifiedAsConflict verifies that a deadlock (40P01) error is
// converted into a typed error.
//
// Because the lock order has been made uniform, no deadlock occurs in the
// normal flows; this test tries the LAST DEFENSE. Two transactions lock two
// items in opposite orders on purpose. Without the classification the victim
// transaction would get errors.Internal (HTTP 500) and the caller could not
// tell that the request can be retried.
func TestADeadlockIsClassifiedAsConflict(t *testing.T) {
	ctx := context.Background()
	repo := repository.New(testPool.Pool())
	svc := newDBService(t)

	firstItem := newItem(ctx, t, svc)
	secondItem := newItem(ctx, t, svc)

	firstLocked, secondLocked := make(chan struct{}), make(chan struct{})
	errs := make(chan error, 2)

	go func() {
		errs <- repo.WithTx(ctx, func(ctx context.Context) error {
			if err := repo.LockInventoryItem(ctx, firstItem.ID); err != nil {
				return err
			}
			close(firstLocked)
			<-secondLocked
			return repo.LockInventoryItem(ctx, secondItem.ID)
		})
	}()
	go func() {
		errs <- repo.WithTx(ctx, func(ctx context.Context) error {
			if err := repo.LockInventoryItem(ctx, secondItem.ID); err != nil {
				return err
			}
			close(secondLocked)
			<-firstLocked
			return repo.LockInventoryItem(ctx, firstItem.ID)
		})
	}()

	var victims int
	for range 2 {
		err := <-errs
		if err == nil {
			continue
		}
		victims++
		assert.Equal(t, errors.KindConflict, errors.KindOf(err),
			"the deadlock victim must get a retryable error, it got: %v", err)
		// The code is written BY HAND: using the constant would produce a
		// tautology that passes even if the constant is wrong.
		assert.Equal(t, "inventory_concurrent_update", errors.CodeOf(err))
	}
	assert.Equal(t, 1, victims, "exactly one transaction is killed in a deadlock")
}

// TestTheReserveReleaseReserveCycle verifies that after the compensation the
// quantity is really sellable again. This cycle happens when the Phase 6 saga
// fails and is retried.
func TestTheReserveReleaseReserveCycle(t *testing.T) {
	ctx := context.Background()
	svc := newDBService(t)
	item, loc := withStock(ctx, t, svc, 1)

	first, err := svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 1, LineItemID: "li_1",
	})
	require.NoError(t, err)

	_, err = svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 1,
	})
	require.Error(t, err, "there must be no second reservation while the last unit is set aside")

	require.NoError(t, svc.ReleaseReservation(ctx, first.ID))

	available, err := svc.AvailableQuantity(ctx, item.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), available, "the compensation must give the quantity back")

	second, err := svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 1,
	})
	require.NoError(t, err)
	assert.NotEqual(t, first.ID, second.ID)

	released, err := svc.GetReservation(ctx, first.ID)
	require.NoError(t, err)
	assert.Equal(t, models.ReservationReleased, released.Status)
	assert.Equal(t, "li_1", released.LineItemID, "the cart line ID must be kept")
}

// TestReleaseReservationIsIdempotentInTheDatabase verifies that the
// compensation is idempotent on the database too: the second call returns no
// error and does not decrease the reserved quantity a second time.
func TestReleaseReservationIsIdempotentInTheDatabase(t *testing.T) {
	ctx := context.Background()
	svc := newDBService(t)
	item, loc := withStock(ctx, t, svc, 5)

	res, err := svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 3,
	})
	require.NoError(t, err)

	require.NoError(t, svc.ReleaseReservation(ctx, res.ID))
	require.NoError(t, svc.ReleaseReservation(ctx, res.ID), "the second call must not return an error")
	require.NoError(t, svc.ReleaseReservation(ctx, res.ID), "the third call must not return an error either")

	levels, err := svc.ListInventoryLevels(ctx, item.ID)
	require.NoError(t, err)
	require.Len(t, levels, 1)
	assert.Zero(t, levels[0].ReservedQuantity, "the reserved quantity must decrease only once")
	assert.Equal(t, int64(5), levels[0].StockedQuantity)
	assert.Equal(t, int64(5), levels[0].Available(), "stock must not be created out of nothing")
}

// TestConcurrentReleasesGiveTheStockBackOnce verifies that calls trying to
// release the same reservation at the same time all return successfully and
// that the stock is given back only once.
//
// The critical point is that the reservation row is locked as well. Had it been
// read without a lock, both calls would see the status as "active", the second
// would take the level lock after the first, and it would try to decrease the
// reserved quantity once more from the (already decreased) value and return an
// inconsistency error — that is, the compensation would STOP BEING idempotent
// under concurrency.
//
// The race is tried over several ROUNDS: a single round can miss the window
// because of timing. Every call must return successfully in every round.
func TestConcurrentReleasesGiveTheStockBackOnce(t *testing.T) {
	ctx := context.Background()
	svc := newDBService(t)

	const rounds = 5
	const callers = 6

	for round := range rounds {
		item, loc := withStock(ctx, t, svc, 5)
		res, err := svc.Reserve(ctx, service.ReserveInput{
			InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 3,
		})
		require.NoError(t, err)

		start := make(chan struct{})
		errs := make([]error, callers)

		var wg sync.WaitGroup
		for i := range callers {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				errs[i] = svc.ReleaseReservation(ctx, res.ID)
			}(i)
		}
		close(start)
		wg.Wait()

		for i, err := range errs {
			require.NoError(t, err, "round %d: concurrent compensation %d must not return an error", round, i)
		}

		levels, err := svc.ListInventoryLevels(ctx, item.ID)
		require.NoError(t, err)
		require.Len(t, levels, 1)
		assert.Zero(t, levels[0].ReservedQuantity, "round %d", round)
		assert.Equal(t, int64(5), levels[0].StockedQuantity, "round %d", round)
	}
}

// TestConfirmReservationDeductsTheStock verifies that a confirmation decreases
// the physical stock, does not change the available quantity and locks out a
// release.
func TestConfirmReservationDeductsTheStock(t *testing.T) {
	ctx := context.Background()
	svc := newDBService(t)
	item, loc := withStock(ctx, t, svc, 10)

	res, err := svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 4,
	})
	require.NoError(t, err)

	availableBefore, err := svc.AvailableQuantity(ctx, item.ID)
	require.NoError(t, err)

	require.NoError(t, svc.ConfirmReservation(ctx, res.ID, testSaleOrderID))

	levels, err := svc.ListInventoryLevels(ctx, item.ID)
	require.NoError(t, err)
	require.Len(t, levels, 1)
	assert.Equal(t, int64(6), levels[0].StockedQuantity, "the physical stock must decrease")
	assert.Zero(t, levels[0].ReservedQuantity)
	assert.Equal(t, availableBefore, levels[0].Available(),
		"the confirmation must not change the available quantity")

	confirmed, err := svc.GetReservation(ctx, res.ID)
	require.NoError(t, err)
	assert.Equal(t, models.ReservationConfirmed, confirmed.Status)

	require.NoError(t, svc.ConfirmReservation(ctx, res.ID, testSaleOrderID), "the confirmation must be idempotent")
	assert.Equal(t, int64(6), stockedQuantityOf(ctx, t, item.ID, loc.ID),
		"a second confirmation must not decrease the stock once more")

	err = svc.ReleaseReservation(ctx, res.ID)
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err),
		"a confirmed reservation cannot be released")
}

// TestAnItemWithAnActiveReservationCannotBeDeleted verifies that an item with
// promised stock cannot be deleted, and that it can be deleted once the
// reservation has ended.
func TestAnItemWithAnActiveReservationCannotBeDeleted(t *testing.T) {
	ctx := context.Background()
	svc := newDBService(t)
	item, loc := withStock(ctx, t, svc, 5)

	res, err := svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 2,
	})
	require.NoError(t, err)

	err = svc.DeleteInventoryItem(ctx, item.ID)
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeItemHasReservations, errors.CodeOf(err))

	require.NoError(t, svc.ReleaseReservation(ctx, res.ID))
	require.NoError(t, svc.DeleteInventoryItem(ctx, item.ID))

	_, err = svc.ListInventoryLevels(ctx, item.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err), "the levels must be deleted too")
}

// TestTheDatabaseConstraintIsTheLastDefense verifies that the available
// quantity cannot be pushed below zero even if the service is bypassed and SQL
// is written directly.
//
// Without this constraint, a single intervention from outside the service could
// silently leave the stock inconsistent.
func TestTheDatabaseConstraintIsTheLastDefense(t *testing.T) {
	ctx := context.Background()
	svc := newDBService(t)
	item, loc := withStock(ctx, t, svc, 5)

	_, err := svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: item.ID, LocationID: loc.ID, Quantity: 3,
	})
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx,
		`UPDATE inventory_levels SET stocked_quantity = 1
         WHERE inventory_item_id = $1 AND location_id = $2`, item.ID, loc.ID)
	require.Error(t, err, "a direct update that goes below the reserved quantity must be refused")

	_, err = testPool.Pool().Exec(ctx,
		`UPDATE inventory_levels SET stocked_quantity = -1
         WHERE inventory_item_id = $1 AND location_id = $2`, item.ID, loc.ID)
	require.Error(t, err, "a negative physical quantity must be refused")

	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO inventory_reservations (id, inventory_item_id, location_id, quantity)
         VALUES ($1, $2, $3, 0)`, models.NewReservationID(), item.ID, loc.ID)
	require.Error(t, err, "a reservation of zero units must be refused")
}

// TestTheTotalSurvivesAnOutOfRangePage verifies that the listing's total count
// stays right even when the page holds no row at all.
//
// If the total were derived from the page rows (for example, read from a window
// function returned along with each row), no row would come back for an
// out-of-range page and the total would look like 0; the client would conclude
// "there are no records". The envelope's count field, however, is the count not
// of the page but of ALL the records MATCHING THE FILTER. The fake store cannot
// show this distinction, because there the count is independent of the rows
// anyway; the distinction exists only in real SQL.
func TestTheTotalSurvivesAnOutOfRangePage(t *testing.T) {
	ctx := context.Background()
	svc := newDBService(t)

	t.Run("item", func(t *testing.T) {
		item := newItem(ctx, t, svc)

		items, total, err := svc.ListInventoryItems(ctx, service.ListInventoryItemsInput{
			SKU: &item.SKU, Page: service.Page{Limit: 10},
		})
		require.NoError(t, err)
		require.Len(t, items, 1)
		require.Equal(t, int64(1), total)

		items, total, err = svc.ListInventoryItems(ctx, service.ListInventoryItemsInput{
			SKU: &item.SKU, Page: service.Page{Limit: 10, Offset: 50},
		})
		require.NoError(t, err)
		assert.Empty(t, items, "an out-of-range page must hold no row")
		assert.Equal(t, int64(1), total,
			"the total is the count of ALL the records matching the filter, not the number of rows on the page")
	})

	t.Run("location", func(t *testing.T) {
		newLocation(ctx, t, svc)

		locs, total, err := svc.ListStockLocations(ctx, service.ListStockLocationsInput{
			Page: service.Page{Limit: 10, Offset: 1_000_000},
		})

		require.NoError(t, err)
		assert.Empty(t, locs, "an out-of-range page must hold no row")
		assert.Positive(t, total, "there is at least one location; the total cannot drop to zero along with the page")
	})
}

// TestTheQueryProviderReturnsItemsWithTheirStock verifies on a real database
// that the provider returns the item with its TOTAL available quantity.
// product's storefront listing reads stock this way.
func TestTheQueryProviderReturnsItemsWithTheirStock(t *testing.T) {
	ctx := context.Background()
	svc := newDBService(t)
	provider := service.NewQueryProvider(svc)

	item, locA := withStock(ctx, t, svc, 10)
	locB := newLocation(ctx, t, svc)
	_, err := svc.SetInventoryLevel(ctx, item.ID, locB.ID, 5)
	require.NoError(t, err)
	_, err = svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: item.ID, LocationID: locA.ID, Quantity: 4,
	})
	require.NoError(t, err)

	unstocked := newItem(ctx, t, svc)

	records, err := provider.FetchByIDs(ctx, []string{item.ID, unstocked.ID}, nil)
	require.NoError(t, err)
	require.Len(t, records, 2)

	byID := map[string]query.Record{}
	for _, record := range records {
		id, ok := record[query.IDField].(string)
		require.True(t, ok, "the record ID must be a string")
		byID[id] = record
	}

	assert.Equal(t, int64(11), byID[item.ID][service.FieldAvailableQuantity], "(10-4) + 5 = 11")
	assert.Equal(t, int64(0), byID[unstocked.ID][service.FieldAvailableQuantity],
		"an item with no level must come back with zero")
	assert.Equal(t, item.SKU, byID[item.ID][service.FieldSKU])
}

// TestTheModuleRegistrationResolves verifies that the names the module
// registers in the container really resolve and satisfy the expected
// interfaces.
//
// This was the price of ADR 0001: there is no compile-time tie between provider
// and consumer, and a mismatch becomes visible only at resolution time. This
// test brings that moment forward.
func TestTheModuleRegistrationResolves(t *testing.T) {
	ctx := context.Background()
	c := container.New(nil)
	require.NoError(t, c.Provide("core.db", testPool))
	require.NoError(t, c.Provide("core.link", link.New(testPool, nil)))

	mod := inventory.New()
	require.NoError(t, mod.Register(ctx, c))

	svc, err := container.Resolve[*service.Service](c, "inventory.service")
	require.NoError(t, err, "the service must be resolvable by its fixed name")
	require.NotNil(t, svc)
	assert.Equal(t, "inventory.service", inventory.ServiceName,
		"if the service name changes the consuming modules cannot find it")

	// The name is computed BY HAND with ADR 0004's rule: the provider is looked
	// up under "<entity>.query". Using the constant would turn the test into a
	// tautology — if the constant were wrong the test would look up the wrong
	// name too.
	provider, err := container.Resolve[query.Provider](c, "inventory_item"+query.ProviderSuffix)
	require.NoError(t, err, "the query provider must be resolvable by its name (ADR 0004)")
	assert.Equal(t, "inventory_item", provider.Entity(),
		"the prefix of the registered name must equal Entity()")

	// The real proof: the core's query layer must be able to find the provider
	// by the entity name alone, without knowing the module at all, and fetch
	// the data.
	item := newItem(ctx, t, svc)
	_, err = svc.SetInventoryLevel(ctx, item.ID, newLocation(ctx, t, svc).ID, 4)
	require.NoError(t, err)

	records, err := query.New(nil, c, nil).Graph(ctx, query.GraphSpec{
		Entity:  "inventory_item",
		Filters: map[string]any{"sku": item.SKU},
	})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, item.ID, records[0][query.IDField])
	assert.Equal(t, int64(4), records[0][service.FieldAvailableQuantity])
}

// activeReservationCount returns the number of the item's active reservation
// records.
func activeReservationCount(ctx context.Context, t *testing.T, itemID string) int64 {
	t.Helper()

	var count int64
	err := testPool.Pool().QueryRow(ctx,
		`SELECT COUNT(*) FROM inventory_reservations
         WHERE inventory_item_id = $1 AND status = 'active'`,
		itemID).Scan(&count)
	require.NoError(t, err)
	return count
}

// stockedQuantityOf reads the level's physical quantity directly from the
// database.
func stockedQuantityOf(ctx context.Context, t *testing.T, itemID, locationID string) int64 {
	t.Helper()

	var stocked int64
	err := testPool.Pool().QueryRow(ctx,
		`SELECT stocked_quantity FROM inventory_levels
         WHERE inventory_item_id = $1 AND location_id = $2 AND deleted_at IS NULL`,
		itemID, locationID).Scan(&stocked)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0
	}
	require.NoError(t, err)
	return stocked
}

// TestALockingReadOutsideATransactionIsRefused verifies that the store methods
// that take a lock return an error when called outside a transaction.
//
// A FOR UPDATE lock without a transaction is released as soon as the statement
// ends; that is, it protects nothing but looks as if it does. Letting it run
// silently is the easiest way to lose the concurrency guarantee without
// noticing.
func TestALockingReadOutsideATransactionIsRefused(t *testing.T) {
	ctx := context.Background()
	repo := repository.New(testPool.Pool())
	svc := newDBService(t)
	item, loc := withStock(ctx, t, svc, 3)

	_, err := repo.LockInventoryLevel(ctx, item.ID, loc.ID)
	require.Error(t, err, "the level lock must not be taken outside a transaction")
	assert.Contains(t, err.Error(), "inside a transaction")

	err = repo.LockInventoryItem(ctx, item.ID)
	require.Error(t, err, "the item lock must not be taken outside a transaction")

	_, err = repo.LockReservation(ctx, "invres_x")
	require.Error(t, err, "the reservation lock must not be taken outside a transaction")

	// The same calls must succeed inside a transaction.
	require.NoError(t, repo.WithTx(ctx, func(ctx context.Context) error {
		if lockErr := repo.LockInventoryItem(ctx, item.ID); lockErr != nil {
			return lockErr
		}
		_, lockErr := repo.LockInventoryLevel(ctx, item.ID, loc.ID)
		return lockErr
	}))
}

// TestAFailedTransactionIsRolledBack verifies on the database that a
// transaction is really rolled back when it fails.
func TestAFailedTransactionIsRolledBack(t *testing.T) {
	ctx := context.Background()
	repo := repository.New(testPool.Pool())
	sku := "SKU-" + models.NewInventoryItemID()

	deliberate := errors.Internal("test_hata", "the transaction must be rolled back")
	err := repo.WithTx(ctx, func(ctx context.Context) error {
		_, createErr := repo.CreateInventoryItem(ctx, models.InventoryItem{
			ID: models.NewInventoryItemID(), SKU: sku, RequiresShipping: true,
		})
		if createErr != nil {
			return createErr
		}
		return deliberate
	})

	require.ErrorIs(t, err, deliberate)

	var count int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT COUNT(*) FROM inventory_items WHERE sku = $1`, sku).Scan(&count))
	assert.Zero(t, count, "no row written by the rolled-back transaction may remain")
}
