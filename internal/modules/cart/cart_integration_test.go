//go:build integration

// The tests in this file need a real PostgreSQL instance (and therefore
// Docker); they are separated behind the `integration` tag so that `make test`
// stays fast. To run them: make test-integration
//
// The unit tests prove the service's DECISIONS against a fake repository. The
// tests here prove the GROUND those decisions rest on: that the migration can
// be rolled back, that the constraints really are enforced, and that the
// concurrency claim holds at the database level.
// In particular, the claim that "concurrent AddLineItem does not corrupt the
// lines" can only be exercised here, over real goroutines and real row locks.
package cart_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/link"
	"github.com/bdrtr/gobit/internal/core/query"
	cartmod "github.com/bdrtr/gobit/internal/modules/cart"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
	"github.com/bdrtr/gobit/internal/modules/cart/repository"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

const postgresImage = "postgres:16-alpine"

// moduleTables are the tables the module owns; the migration tests use this
// list.
var moduleTables = []string{
	"carts", "cart_line_items", "cart_addresses", "cart_shipping_methods",
}

// Constants used in the test data. The region and customer ids belong to OTHER
// modules; this module does not verify their existence (Principle 2.2).
const (
	testRegionID   = "reg_TEST"
	testCustomerID = "cust_TEST"
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

	if err := db.Migrate(ctx, testDSN, cartmod.New().Migrations(), cartmod.ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "the migration could not be applied: %v\n", err)
		return 1
	}

	return m.Run()
}

// newService sets up a service running on a real repository.
func newService(t *testing.T) *service.Service {
	t.Helper()

	svc, err := service.New(service.Options{Repo: repository.New(testPool.Pool())})
	require.NoError(t, err)
	return svc
}

// newCart creates a guest cart for the test.
func newCart(ctx context.Context, t *testing.T, svc *service.Service) models.Cart {
	t.Helper()

	cart, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID:     testRegionID,
		CurrencyCode: testCurrency,
	})
	require.NoError(t, err)
	return cart
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

// TestMigrationCanBeRolledBack verifies that the migration can be applied and
// rolled back (plan Section 8: up/down pairs, reversible).
func TestMigrationCanBeRolledBack(t *testing.T) {
	ctx := context.Background()
	src := cartmod.New().Migrations()

	for _, table := range moduleTables {
		require.True(t, tableExists(ctx, t, table), "%s must exist at the start", table)
	}

	require.NoError(t, db.MigrateDown(ctx, testDSN, src, cartmod.ModuleName, 0))
	for _, table := range moduleTables {
		assert.False(t, tableExists(ctx, t, table), "%s must not remain after the rollback", table)
	}

	require.NoError(t, db.Migrate(ctx, testDSN, src, cartmod.ModuleName))
	for _, table := range moduleTables {
		assert.True(t, tableExists(ctx, t, table), "%s must be applied again", table)
	}

	version, dirty, err := db.Version(ctx, testDSN, cartmod.ModuleName)
	require.NoError(t, err)
	assert.False(t, dirty, "there must be no half-finished migration")
	assert.Equal(t, uint(1), version)
}

// TestNoCrossModuleForeignKeys verifies that ALL the foreign keys in the
// module's tables go to the module's own tables again (Principle 2.2).
//
// In particular cart_line_items.variant_id (product), carts.region_id (region)
// and carts.customer_id (customer) are other modules' ids and CANNOT be foreign
// keys; this test shows that the rule really holds in the schema.
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

// TestCartLifecycle verifies the cart's end-to-end flow: create -> add line ->
// update quantity -> write address -> add shipping -> write totals ->
// complete.
func TestCartLifecycle(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	cart, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID:     testRegionID,
		CustomerID:   testCustomerID,
		Email:        "Customer@Example.COM",
		CurrencyCode: "try",
		Metadata:     map[string]any{"channel": "web"},
	})
	require.NoError(t, err)
	assert.Equal(t, "TRY", cart.CurrencyCode)
	assert.Equal(t, "customer@example.com", cart.Email)
	assert.Equal(t, "UTC", cart.CreatedAt.Location().String(), "the time must be UTC")
	assert.Equal(t, map[string]any{"channel": "web"}, cart.Metadata)
	assert.False(t, cart.TotalsStale())

	item, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: "variant_A", Title: "Red T-Shirt", Quantity: 2, UnitPrice: 1000,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), item.Quantity)

	item, err = svc.UpdateLineItemQuantity(ctx, cart.ID, item.ID, 3)
	require.NoError(t, err)
	assert.Equal(t, int64(3), item.Quantity)

	_, err = svc.SetShippingAddress(ctx, cart.ID, service.AddressInput{
		FirstName: "Alice", LastName: "Smith", Address1: "1 Main Street",
		City: "Istanbul", CountryCode: "tr", PostalCode: "34000",
	})
	require.NoError(t, err)
	_, err = svc.SetBillingAddress(ctx, cart.ID, service.AddressInput{
		FirstName: "Alice", City: "Ankara", CountryCode: "TR",
	})
	require.NoError(t, err)

	method, err := svc.AddShippingMethod(ctx, cart.ID, service.AddShippingMethodInput{
		Name: "Standard Shipping", Amount: 2500, Data: map[string]any{"branch": "Kadikoy"},
	})
	require.NoError(t, err)

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1)
	require.NotNil(t, detail.ShippingAddress)
	require.NotNil(t, detail.BillingAddress)
	assert.Equal(t, "TR", detail.ShippingAddress.CountryCode)
	assert.Equal(t, "Ankara", detail.BillingAddress.City)
	require.Len(t, detail.ShippingMethods, 1)
	assert.Equal(t, map[string]any{"branch": "Kadikoy"}, detail.ShippingMethods[0].Data)
	assert.True(t, detail.TotalsStale(), "before being calculated the totals must be stale")

	// The totals: 3 x 1000 = 3000 subtotal, 20% tax 600, shipping 2500.
	// The shape the calculation rests on comes FROM THE CALLER; the workflow
	// does exactly this: it reads first, then writes the calculation together
	// with the shape it read.
	require.NoError(t, svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: detail.Revision,
		Subtotal: 3000, TaxTotal: 600, ShippingTotal: 2500, Total: 6100,
		Lines: []service.LineTotals{
			{LineItemID: item.ID, UnitPrice: 1000, Subtotal: 3000, TaxTotal: 600, Total: 3600},
		},
	}))

	detail, err = svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(6100), detail.Total)
	assert.True(t, detail.TotalsConsistent())
	assert.False(t, detail.TotalsStale())
	assert.Equal(t, int64(3600), detail.Items[0].Total)

	completed, err := svc.MarkCompleted(ctx, cart.ID)
	require.NoError(t, err)
	require.True(t, completed.Completed())
	assert.Equal(t, "UTC", completed.CompletedAt.Location().String())

	// A completed cart can no longer be changed.
	err = svc.RemoveShippingMethod(ctx, cart.ID, method.ID)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err),
		"a shipping method must not be removable on a completed cart, got: %v", err)
}

// cartRevisions reads the cart's shape and totals counters from the database.
//
// They are read with a DIRECT query rather than through the service: what is
// under test here is which value the stamp was really written with, and the
// service's own read would carry the same assumption, so it would not be an
// independent witness.
func cartRevisions(ctx context.Context, t *testing.T, cartID string) (revision, totalsRevision int64) {
	t.Helper()

	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT revision, totals_revision FROM carts WHERE id = $1`, cartID).
		Scan(&revision, &totalsRevision))
	return revision, totalsRevision
}

// TestSetTotalsRejectsUnpricedLineOnTheRealDatabase verifies on real Postgres
// that the calculation must cover ALL of the cart's lines.
//
// This is the costliest violation of the contract: a calculation round that
// forgets to send the lines looks CONSISTENT with "subtotal 0, total 0",
// because the cart's stored line amounts are zero. Were the coverage not
// mandatory, the cart would really be written with a total of 0 and would pass
// MarkCompleted too.
func TestSetTotalsRejectsUnpricedLineOnTheRealDatabase(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	cart := newCart(ctx, t, svc)

	item, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: "variant_UNPRICED", Title: "T-Shirt", Quantity: 3, UnitPrice: 100000,
	})
	require.NoError(t, err)
	revision, totalsRevision := cartRevisions(ctx, t, cart.ID)
	require.NotEqual(t, revision, totalsRevision, "an uncalculated cart is stale")

	err = svc.SetTotals(ctx, cart.ID, service.Totals{Revision: revision})

	require.Error(t, err, "a calculation that skips an unpriced line must not be accepted")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, service.CodeTotalsInconsistent, errors.CodeOf(err))
	assert.Contains(t, err.Error(), item.ID)

	// Neither the totals nor the stamp must have been written.
	newRevision, newTotalsRevision := cartRevisions(ctx, t, cart.ID)
	assert.Equal(t, revision, newRevision)
	assert.Equal(t, totalsRevision, newTotalsRevision, "a rejected round must not stamp")

	_, err = svc.MarkCompleted(ctx, cart.ID)
	require.Error(t, err, "an unpriced cart must not be completable")
	assert.Equal(t, service.CodeTotalsStale, errors.CodeOf(err))

	// Once the lines' amounts are given, the same round passes.
	require.NoError(t, svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: revision, Subtotal: 300000, Total: 300000,
		Lines: []service.LineTotals{
			{LineItemID: item.ID, UnitPrice: 100000, Subtotal: 300000, Total: 300000},
		},
	}))
	_, newTotalsRevision = cartRevisions(ctx, t, cart.ID)
	assert.Equal(t, revision, newTotalsRevision,
		"the stamp must be made with the shape the calculation rests on")
}

// TestSetTotalsRejectsStaleCalculationOnTheRealDatabase verifies on real
// Postgres that the cart shape the calculation rests on is taken FROM THE
// CALLER.
//
// The scenario is the race cart.go claims to defend against: the workflow reads
// the cart, makes its calculation OUTSIDE THE LOCK, meanwhile the customer adds
// a line to the cart, and the workflow writes the stale result. Were the stamp
// taken from the shape at write time, the stale calculation would be stamped as
// CURRENT, MarkCompleted's staleness gate would open and the customer would pay
// for less than what is in their cart.
func TestSetTotalsRejectsStaleCalculationOnTheRealDatabase(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	cart := newCart(ctx, t, svc)

	first, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: "variant_STALE_A", Title: "T-Shirt", Quantity: 1,
	})
	require.NoError(t, err)

	// The workflow reads and makes its calculation according to THIS shape.
	calculated, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)

	// The customer cuts in: a second line is added.
	_, err = svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: "variant_STALE_B", Title: "Trousers", Quantity: 1,
	})
	require.NoError(t, err)

	err = svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: calculated.Revision, Subtotal: 1000, Total: 1000,
		Lines: []service.LineTotals{
			{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 1000, Total: 1000},
		},
	})

	require.Error(t, err, "a stale calculation must not be accepted")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeTotalsStale, errors.CodeOf(err))

	revision, totalsRevision := cartRevisions(ctx, t, cart.ID)
	assert.Equal(t, int64(2), revision)
	assert.NotEqual(t, revision, totalsRevision,
		"a stale calculation must not stamp the cart as FRESH")

	_, err = svc.MarkCompleted(ctx, cart.ID)
	require.Error(t, err, "a stale cart must not be completable")
	assert.Equal(t, service.CodeTotalsStale, errors.CodeOf(err))

	// Once the workflow reads again and recalculates, the round is accepted.
	current, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.NoError(t, svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: current.Revision, Subtotal: 1500, Total: 1500,
		Lines: []service.LineTotals{
			{LineItemID: current.Items[0].ID, UnitPrice: 1000, Subtotal: 1000, Total: 1000},
			{LineItemID: current.Items[1].ID, UnitPrice: 500, Subtotal: 500, Total: 500},
		},
	}))
	_, err = svc.MarkCompleted(ctx, cart.ID)
	require.NoError(t, err)
}

// TestMarkCompletedRejectsCartWithoutLines verifies that a cart without lines
// cannot be completed.
//
// The rule also closes the "the totals were NEVER calculated" hole: in a new
// cart both revision and totals_revision are zero, that is, the staleness
// criterion is silent. Because adding a line always increments the counter, in
// a cart whose counters are equal and that HAS LINES the calculation really did
// run; what remains is only the cart that was never touched (and is therefore
// necessarily without lines).
func TestMarkCompletedRejectsCartWithoutLines(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	cart := newCart(ctx, t, svc)

	revision, totalsRevision := cartRevisions(ctx, t, cart.ID)
	require.Equal(t, revision, totalsRevision,
		"in a new cart the staleness criterion is silent")

	_, err := svc.MarkCompleted(ctx, cart.ID)

	require.Error(t, err, "a cart that was never calculated must not be completable")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeCartEmpty, errors.CodeOf(err))

	var completed bool
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT completed_at IS NOT NULL FROM carts WHERE id = $1`, cart.ID).Scan(&completed))
	assert.False(t, completed, "a rejected completion must not stamp")
}

// TestUpdateCartTransfersGuestCartToCustomer verifies the transfer of a guest
// cart to a registered customer and that after the transfer it shows up in the
// customer filter.
func TestUpdateCartTransfersGuestCartToCustomer(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	cart := newCart(ctx, t, svc)
	require.True(t, cart.Guest())

	_, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: "variant_TRANSFER", Title: "T-Shirt", Quantity: 1,
	})
	require.NoError(t, err)

	customerID := "cust_TRANSFER_" + models.NewCartID()
	email := "Transfer@Example.COM"
	updated, err := svc.UpdateCart(ctx, cart.ID, service.UpdateCartInput{
		Email: &email, CustomerID: customerID,
	})

	require.NoError(t, err)
	assert.Equal(t, "transfer@example.com", updated.Email)
	assert.Equal(t, customerID, updated.CustomerID)

	// The cart now shows up in the customer filter and has kept its lines.
	listed, err := svc.ListCarts(ctx, service.ListCartsInput{CustomerID: &customerID})
	require.NoError(t, err)

	count := listed.Count
	assert.Equal(t, int64(1), count)

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1, "the transfer must not lose the lines")

	// The cart cannot be handed over to ANOTHER customer.
	_, err = svc.UpdateCart(ctx, cart.ID, service.UpdateCartInput{CustomerID: "cust_OTHER"})
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeCustomerMismatch, errors.CodeOf(err))
}

// TestGuestAndRegisteredCustomerCarts verifies that both scenarios work and can
// be told apart from each other.
func TestGuestAndRegisteredCustomerCarts(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	guest, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: testRegionID, CurrencyCode: testCurrency, Email: "guest@example.com",
	})
	require.NoError(t, err)
	assert.True(t, guest.Guest())

	customerID := "cust_" + guest.ID
	registered, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: testRegionID, CustomerID: customerID, CurrencyCode: testCurrency,
	})
	require.NoError(t, err)
	assert.False(t, registered.Guest())

	// The guest cart's customer column stays EMPTY; the registered one's fills.
	assert.Empty(t, guest.CustomerID, "a guest cart must have no customer")
	assert.Equal(t, customerID, registered.CustomerID)

	// The filter separates the two.
	listed, err := svc.ListCarts(ctx, service.ListCartsInput{CustomerID: &customerID})
	require.NoError(t, err)

	count := listed.Count
	assert.Equal(t, int64(1), count)
}

// TestManyCartsCanBeOpenedInTheSameRegion verifies that more than one cart can
// be opened for a region and a customer.
//
// This is the cart's nature: a customer has more than one cart over time, and a
// region holds thousands of carts. An index anywhere in the schema imposing
// UNIQUENESS per region or per customer brings this test down.
func TestManyCartsCanBeOpenedInTheSameRegion(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	customerID := "cust_MANY_" + models.NewCartID()

	for range 3 {
		_, err := svc.CreateCart(ctx, service.CreateCartInput{
			RegionID: testRegionID, CustomerID: customerID, CurrencyCode: testCurrency,
		})
		require.NoError(t, err,
			"a second cart must be openable for the same region and customer")
	}

	listed, err := svc.ListCarts(ctx, service.ListCartsInput{CustomerID: &customerID})
	require.NoError(t, err)

	count := listed.Count
	assert.Equal(t, int64(3), count)
}

// TestConcurrentAddLineItemDoesNotCorruptLines verifies that additions made to
// the same cart at the same time do not corrupt the lines.
//
// Racing calls for the same variant must produce ONE line and the quantities
// must add up WITHOUT BEING LOST. Without the cart lock both calls would read
// "no line", both would try an INSERT and one would hit the unique index (or,
// had there been no index, two lines would be created).
func TestConcurrentAddLineItemDoesNotCorruptLines(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	cart := newCart(ctx, t, svc)

	const racers = 12
	start := make(chan struct{})
	results := make([]error, racers)

	var wg sync.WaitGroup
	for i := range racers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
				VariantID: "variant_RACE", Title: "T-Shirt", Quantity: 1, UnitPrice: 1000,
			})
			results[i] = err
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range results {
		require.NoError(t, err, "concurrent addition %d must not error", i)
	}

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1, "the same variant must be collected on one line")
	assert.Equal(t, int64(racers), detail.Items[0].Quantity,
		"no quantity must be lost")
	assert.Equal(t, int64(racers), detail.Revision,
		"every structural change must increment the shape counter by one")
}

// TestConcurrentDifferentVariantAdditions verifies that concurrent additions of
// different variants record all of them.
func TestConcurrentDifferentVariantAdditions(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	cart := newCart(ctx, t, svc)

	const racers = 10
	start := make(chan struct{})
	results := make([]error, racers)

	var wg sync.WaitGroup
	for i := range racers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
				VariantID: fmt.Sprintf("variant_%02d", i),
				Title:     fmt.Sprintf("Product %d", i),
				Quantity:  int64(i + 1),
			})
			results[i] = err
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range results {
		require.NoError(t, err, "concurrent addition %d must not error", i)
	}

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.Len(t, detail.Items, racers, "every variant must get its own line")
}

// TestWritingToCompletedCartIsRejected verifies that on a completed cart all
// the write paths are rejected at the database level too.
func TestWritingToCompletedCartIsRejected(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	cart := newCart(ctx, t, svc)

	item, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: "variant_DONE", Title: "T-Shirt", Quantity: 1,
	})
	require.NoError(t, err)
	current, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.NoError(t, svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: current.Revision,
		Subtotal: 1500, Total: 1500,
		Lines: []service.LineTotals{
			{LineItemID: item.ID, UnitPrice: 1500, Subtotal: 1500, Total: 1500},
		},
	}))
	_, err = svc.MarkCompleted(ctx, cart.ID)
	require.NoError(t, err)

	writes := map[string]func() error{
		"AddLineItem": func() error {
			_, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
				VariantID: "variant_OTHER", Title: "Trousers", Quantity: 1,
			})
			return err
		},
		"UpdateLineItemQuantity": func() error {
			_, err := svc.UpdateLineItemQuantity(ctx, cart.ID, item.ID, 5)
			return err
		},
		"RemoveLineItem": func() error { return svc.RemoveLineItem(ctx, cart.ID, item.ID) },
		"SetShippingAddress": func() error {
			_, e := svc.SetShippingAddress(ctx, cart.ID, service.AddressInput{City: "Izmir"})
			return e
		},
		"AddShippingMethod": func() error {
			_, e := svc.AddShippingMethod(ctx, cart.ID, service.AddShippingMethodInput{Name: "Express"})
			return e
		},
		"SetTotals": func() error {
			return svc.SetTotals(ctx, cart.ID, service.Totals{
				Revision: current.Revision, Subtotal: 1500, Total: 1500,
			})
		},
		"DeleteCart":    func() error { return svc.DeleteCart(ctx, cart.ID) },
		"MarkCompleted": func() error { _, e := svc.MarkCompleted(ctx, cart.ID); return e },
	}

	for name, write := range writes {
		t.Run(name, func(t *testing.T) {
			err := write()
			require.Error(t, err, "%s must return an error on a completed cart", name)
			assert.Equal(t, errors.KindConflict, errors.KindOf(err),
				"%s must return Conflict, got: %v", name, err)
		})
	}

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1)
	assert.Equal(t, int64(1), detail.Items[0].Quantity, "the cart really must not change")
	assert.Equal(t, int64(1500), detail.Total)
}

// TestDatabaseEnforcesTotalsIdentity verifies that the totals identity is
// protected by a database constraint too.
//
// The service makes the same check first, with a more readable error; the
// constraint here is the LAST DEFENSE and it also covers an intervention made
// directly with SQL.
func TestDatabaseEnforcesTotalsIdentity(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	cart := newCart(ctx, t, svc)

	_, err := testPool.Pool().Exec(ctx,
		`UPDATE carts SET subtotal = 1000, total = 999 WHERE id = $1`, cart.ID)

	require.Error(t, err, "a direct update that breaks the identity must be rejected")
	assert.Contains(t, err.Error(), "carts_totals_consistent")
}

// TestDatabaseEnforcesLineUniqueness verifies that a second line for the same
// variant cannot be opened at the database level either.
func TestDatabaseEnforcesLineUniqueness(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	cart := newCart(ctx, t, svc)

	_, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: "variant_UNIQ", Title: "T-Shirt", Quantity: 1,
	})
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO cart_line_items (id, cart_id, variant_id, title, quantity)
         VALUES ($1, $2, 'variant_UNIQ', 'Copy', 1)`,
		models.NewLineItemID(), cart.ID)

	require.Error(t, err, "a second line for the same variant must not be openable")
	assert.Contains(t, err.Error(), "cart_line_items_cart_variant_uniq")
}

// TestSoftDeleteDropsOutOfReads verifies that a soft-deleted cart is not read
// (plan Section 8).
func TestSoftDeleteDropsOutOfReads(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	cart := newCart(ctx, t, svc)

	require.NoError(t, svc.DeleteCart(ctx, cart.ID))

	_, err := svc.GetCart(ctx, cart.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))

	carts, err := svc.ListCartsByIDs(ctx, []string{cart.ID})
	require.NoError(t, err)
	assert.Empty(t, carts, "a deleted cart must not show up in the bulk read either")

	// The row must PHYSICALLY still be there: the deletion is soft.
	var deleted bool
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT deleted_at IS NOT NULL FROM carts WHERE id = $1`, cart.ID).Scan(&deleted))
	assert.True(t, deleted)
}

// TestModuleRegisterBindsToTheContainer verifies that Register does both of the
// things in the contract: registering the service and the Query provider.
func TestModuleRegisterBindsToTheContainer(t *testing.T) {
	ctx := context.Background()

	c := container.New(nil)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	links := link.New(testPool, nil)
	require.NoError(t, c.Provide("core.db", testPool))
	require.NoError(t, c.Provide("core.link", links))
	require.NoError(t, c.Provide("core.query", query.New(links, c, nil)))

	mod := cartmod.New()
	require.NoError(t, mod.Register(ctx, c))

	svc, err := container.Resolve[*service.Service](c, cartmod.ServiceName)
	require.NoError(t, err, "the service must be resolvable under the name %q", cartmod.ServiceName)
	require.NotNil(t, svc)

	provider, err := container.Resolve[query.Provider](c, cartmod.ProviderName)
	require.NoError(t, err, "the provider must be resolvable under the name %q", cartmod.ProviderName)
	assert.Equal(t, service.EntityName, provider.Entity(),
		"the provider name's prefix must match Entity() (ADR 0004)")

	// The registered service really must work.
	created, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: testRegionID, CurrencyCode: testCurrency,
	})
	require.NoError(t, err)

	records, err := provider.FetchByIDs(ctx, []string{created.ID}, nil)
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, created.ID, records[0][query.IDField])
}

// TestQueryLayerReadsTheCart verifies that the real Query layer can find the
// cart provider.
func TestQueryLayerReadsTheCart(t *testing.T) {
	ctx := context.Background()

	c := container.New(nil)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	links := link.New(testPool, nil)
	require.NoError(t, c.Provide("core.db", testPool))
	require.NoError(t, c.Provide("core.link", links))
	graph := query.New(links, c, nil)
	require.NoError(t, c.Provide("core.query", graph))

	mod := cartmod.New()
	require.NoError(t, mod.Register(ctx, c))

	svc := mod.Service()
	require.NotNil(t, svc)
	customerID := "cust_QUERY_" + models.NewCartID()
	created, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: testRegionID, CustomerID: customerID, CurrencyCode: testCurrency,
	})
	require.NoError(t, err)

	records, err := graph.Graph(ctx, query.GraphSpec{
		Entity:  service.EntityName,
		Fields:  []string{query.IDField, service.FieldCustomerID, service.FieldTotal},
		Filters: map[string]any{service.FieldCustomerID: customerID},
	})

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, created.ID, records[0][query.IDField])
	assert.Equal(t, customerID, records[0][service.FieldCustomerID])
}

// lineAmounts reads a line's MONEY fields with a direct query.
//
// They are read directly rather than through the service: what is under test is
// which ROW in the database the amounts were written to, and the service's own
// read carries the same mapping assumption as the write, so it would not be an
// independent witness.
func lineAmounts(ctx context.Context, t *testing.T, lineID string) models.LineTotals {
	t.Helper()

	var out models.LineTotals
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT unit_price, subtotal, discount_total, tax_total, total
         FROM cart_line_items WHERE id = $1`, lineID).
		Scan(&out.UnitPrice, &out.Subtotal, &out.DiscountTotal, &out.TaxTotal, &out.Total))
	return out
}

// TestSetTotalsWritesEachLineITSOWNAmounts verifies on real Postgres that the
// bulk write matches the amounts with the RIGHT lines.
//
// The bulk UPDATE sends six parallel arrays (the ids and five money fields) and
// the matching rests only on the ORDER of the arrays. If the order slips, no
// gate makes a sound: the cart's subtotal is still the sum of the lines, the
// line identity (total = subtotal - discount + tax) still holds, and the
// database's cart_line_items_totals_consistent constraint still passes. The
// only thing that breaks is the money taken from the customer.
//
// The fixture makes this visible: every line's quantity, unit price, discount
// and tax are DIFFERENT, so even a one-step slip in the order changes every
// line's stored quadruple.
func TestSetTotalsWritesEachLineITSOWNAmounts(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	cart := newCart(ctx, t, svc)

	const lineCount = 12
	expected := make(map[string]models.LineTotals, lineCount)
	lines := make([]service.LineTotals, 0, lineCount)
	var cartSubtotal, cartDiscount, cartTax int64
	for i := range lineCount {
		quantity := int64(i + 1)
		item, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
			VariantID: fmt.Sprintf("variant_MATCH_%d", i), Title: "Product", Quantity: quantity,
		})
		require.NoError(t, err)

		amounts := models.LineTotals{
			UnitPrice:     int64(100 * (i + 1)),
			DiscountTotal: int64(3 * i),
			TaxTotal:      int64(7 * (i + 1)),
		}
		amounts.Subtotal = amounts.UnitPrice * quantity
		amounts.Total = amounts.Subtotal - amounts.DiscountTotal + amounts.TaxTotal

		expected[item.ID] = amounts
		lines = append(lines, service.LineTotals{
			LineItemID: item.ID, UnitPrice: amounts.UnitPrice, Subtotal: amounts.Subtotal,
			DiscountTotal: amounts.DiscountTotal, TaxTotal: amounts.TaxTotal, Total: amounts.Total,
		})
		cartSubtotal += amounts.Subtotal
		cartDiscount += amounts.DiscountTotal
		cartTax += amounts.TaxTotal
	}

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.NoError(t, svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: detail.Revision,
		Subtotal: cartSubtotal, DiscountTotal: cartDiscount, TaxTotal: cartTax,
		Total: cartSubtotal - cartDiscount + cartTax,
		Lines: lines,
	}))

	for id, amounts := range expected {
		assert.Equal(t, amounts, lineAmounts(ctx, t, id),
			"line %s must get its own amounts", id)
	}
}

// TestSetTotalsKeepsLargeAmountsExact verifies that the largest permitted
// amount comes back from the array round trip UNCORRUPTED.
//
// The bulk write carries the money fields in bigint[] arrays. Money is always
// an INTEGER minor unit; were there a floating point conversion on the array
// path, an amount on the order of 10^18 would be silently rounded and the
// difference would only show up in the accounting. models.MaxTotal (10^18) is
// far above the range float64 can represent exactly (2^53 ≈ 9x10^15), so such a
// conversion MUST become visible here.
func TestSetTotalsKeepsLargeAmountsExact(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	cart := newCart(ctx, t, svc)

	// MaxTotal = MaxAmount x MaxQuantity; because the subtotal's product is
	// validated too, the line is set up with exactly this quantity and unit
	// price.
	item, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: "variant_LARGE", Title: "Expensive", Quantity: models.MaxQuantity,
		UnitPrice: models.MaxAmount,
	})
	require.NoError(t, err)

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.NoError(t, svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: detail.Revision,
		Subtotal: models.MaxTotal, Total: models.MaxTotal,
		Lines: []service.LineTotals{{
			LineItemID: item.ID, UnitPrice: models.MaxAmount,
			Subtotal: models.MaxTotal, Total: models.MaxTotal,
		}},
	}))

	assert.Equal(t, models.LineTotals{
		UnitPrice: models.MaxAmount, Subtotal: models.MaxTotal, Total: models.MaxTotal,
	}, lineAmounts(ctx, t, item.ID),
		"an amount on the order of 10^18 must be preserved bit for bit")
}

// TestSetLineItemTotalsCannotWriteAnotherCartsLine verifies that the bulk write
// CANNOT CROSS the cart boundary.
//
// In the per-line UPDATE the boundary was in the query's WHERE and, when the
// line was not found, NotFound was returned. In the bulk form the ids arrive as
// an array; had the cart_id condition fallen away or slipped into the matching,
// a calculation made FOR one cart could be written to ANOTHER cart's line.
//
// The repository is called directly: because the service reads the line set
// under the lock and looks for coverage, it cannot produce this request at all.
// What is under test is that the layer BELOW the service is safe on its own.
func TestSetLineItemTotalsCannotWriteAnotherCartsLine(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	repo := repository.New(testPool.Pool())

	victim := newCart(ctx, t, svc)
	victimLine, err := svc.AddLineItem(ctx, victim.ID, service.AddLineItemInput{
		VariantID: "variant_VICTIM", Title: "Victim", Quantity: 1, UnitPrice: 1000,
	})
	require.NoError(t, err)

	other := newCart(ctx, t, svc)
	otherLine, err := svc.AddLineItem(ctx, other.ID, service.AddLineItemInput{
		VariantID: "variant_OTHER", Title: "Other", Quantity: 1, UnitPrice: 1000,
	})
	require.NoError(t, err)

	// The state BEFORE the write is taken as a witness: because AddLineItem
	// already writes the line's first unit price, "never written" does not mean
	// zero.
	victimBefore := lineAmounts(ctx, t, victimLine.ID)
	otherBefore := lineAmounts(ctx, t, otherLine.ID)

	// The other cart's round is trying to write the victim's line too.
	err = repo.WithTx(ctx, func(ctx context.Context) error {
		return repo.SetLineItemTotals(ctx, other.ID, []models.LineItemTotals{
			{LineItemID: otherLine.ID, Totals: models.LineTotals{
				UnitPrice: 4242, Subtotal: 4242, Total: 4242}},
			{LineItemID: victimLine.ID, Totals: models.LineTotals{
				UnitPrice: 1, Subtotal: 1, Total: 1}},
		})
	})

	require.Error(t, err, "another cart's line must not be writable")
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
	assert.Contains(t, err.Error(), victimLine.ID,
		"the error must say which line could not be written")

	assert.Equal(t, victimBefore, lineAmounts(ctx, t, victimLine.ID),
		"the victim's line MUST NOT CHANGE")
	assert.Equal(t, otherBefore, lineAmounts(ctx, t, otherLine.ID),
		"because the round fell, the caller's own line must not be written either")
}

// TestSetLineItemTotalsNamesMissingLineIn_CALLER_Order verifies that the error
// message names the FIRST line that could not be written — in the order the
// caller gave.
//
// The order is a contract and its rationale is written in the firstUnwritten
// godoc in repository (without brackets: the symbol is unexported and this
// package cannot see it): PostgreSQL does not guarantee RETURNING order, so the
// only ground for the message being reproducible is the caller's slice. An
// error that gives a different message for the same input makes it impossible
// for the operator to tell two failures apart.
//
// It is ESSENTIAL that the test has two missing lines: in a fixture with a
// single missing line, "first" and "last" are the same id and the contract goes
// unexercised (verified by mutation — the version returning the last missing
// line passed the single-missing tests).
func TestSetLineItemTotalsNamesMissingLineIn_CALLER_Order(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	repo := repository.New(testPool.Pool())
	cart := newCart(ctx, t, svc)

	remaining, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: "variant_REMAINING", Title: "Remaining", Quantity: 1, UnitPrice: 1000,
	})
	require.NoError(t, err)
	firstMissing, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: "variant_MISSING_A", Title: "Missing A", Quantity: 1, UnitPrice: 1000,
	})
	require.NoError(t, err)
	secondMissing, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: "variant_MISSING_B", Title: "Missing B", Quantity: 1, UnitPrice: 1000,
	})
	require.NoError(t, err)

	for _, id := range []string{firstMissing.ID, secondMissing.ID} {
		_, err = testPool.Pool().Exec(ctx,
			`UPDATE cart_line_items SET deleted_at = now() WHERE id = $1`, id)
		require.NoError(t, err)
	}

	amounts := models.LineTotals{UnitPrice: 5000, Subtotal: 5000, Total: 5000}
	err = repo.WithTx(ctx, func(ctx context.Context) error {
		return repo.SetLineItemTotals(ctx, cart.ID, []models.LineItemTotals{
			{LineItemID: remaining.ID, Totals: amounts},
			{LineItemID: firstMissing.ID, Totals: amounts},
			{LineItemID: secondMissing.ID, Totals: amounts},
		})
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
	assert.Contains(t, err.Error(), firstMissing.ID,
		"the FIRST missing line in the caller's order must be named")
	assert.NotContains(t, err.Error(), secondMissing.ID,
		"the second missing line must not be named; the message must be single and reproducible")
}

// TestSetLineItemTotalsPassesWithoutErrorForALinelessRound verifies that a
// round without lines DOES NOT PRODUCE AN ERROR.
//
// The path is not dead: when a cart whose last line has also been removed is
// priced again, the calculation round arrives with zero lines and that round
// has to pass — had it fallen, the cart would become uncalculable the moment
// the customer emptied it. The early return was verified by mutation: the
// version returning an error brought no other test down.
func TestSetLineItemTotalsPassesWithoutErrorForALinelessRound(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	repo := repository.New(testPool.Pool())
	cart := newCart(ctx, t, svc)

	require.NoError(t, repo.WithTx(ctx, func(ctx context.Context) error {
		return repo.SetLineItemTotals(ctx, cart.ID, nil)
	}))

	revision, _ := cartRevisions(ctx, t, cart.ID)
	require.NoError(t, svc.SetTotals(ctx, cart.ID, service.Totals{Revision: revision}),
		"the calculation of a cart without lines must be writable")
}

// TestSetLineItemTotalsDropsTheRoundOnAMissingLine verifies that when one line
// cannot be written, the round is rolled back COMPLETELY.
//
// The bulk UPDATE silently SKIPS an id that does not match: a deleted line or
// an id that never existed produces no error, only fewer lines are written. Had
// it stayed silent, the cart's subtotal and the sum of its lines would diverge
// and the customer would be charged the wrong amount. This is why the written
// ids are compared against the requested ones and, if any are missing, the
// transaction is rolled back.
//
// The rule is the SECOND defense today — the service reads the line set under
// the cart's lock and every path that changes the cart takes the same lock —
// but it is pinned down here so that a path bypassing the lock (direct SQL, a
// flow to be added later) does not stay silent.
func TestSetLineItemTotalsDropsTheRoundOnAMissingLine(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	repo := repository.New(testPool.Pool())
	cart := newCart(ctx, t, svc)

	remaining, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: "variant_REMAINING", Title: "Remaining", Quantity: 1, UnitPrice: 1000,
	})
	require.NoError(t, err)
	removed, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: "variant_DELETED", Title: "Deleted", Quantity: 1, UnitPrice: 1000,
	})
	require.NoError(t, err)

	// The line is deleted BYPASSING the service's lock: what is under test is
	// exactly that such a path does not stay silent.
	_, err = testPool.Pool().Exec(ctx,
		`UPDATE cart_line_items SET deleted_at = now() WHERE id = $1`, removed.ID)
	require.NoError(t, err)

	// The state BEFORE the write is taken as a witness: because AddLineItem
	// already writes the line's first unit price, "never written" does not mean
	// zero.
	remainingBefore := lineAmounts(ctx, t, remaining.ID)

	err = repo.WithTx(ctx, func(ctx context.Context) error {
		return repo.SetLineItemTotals(ctx, cart.ID, []models.LineItemTotals{
			{LineItemID: remaining.ID, Totals: models.LineTotals{
				UnitPrice: 5000, Subtotal: 5000, Total: 5000}},
			{LineItemID: removed.ID, Totals: models.LineTotals{
				UnitPrice: 7000, Subtotal: 7000, Total: 7000}},
		})
	})

	require.Error(t, err, "a round written incompletely must not pass silently")
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
	assert.Contains(t, err.Error(), removed.ID,
		"the error must name the line that could not be written")

	assert.Equal(t, remainingBefore, lineAmounts(ctx, t, remaining.ID),
		"because the round fell, the SURVIVING line must not be written either: all or nothing")
}

// TestSetLineItemTotalsCannotWriteTheSameLineTwice verifies that the same id
// cannot be given twice in one round.
//
// UPDATE ... FROM does not define WHICH source wins when one target row matches
// more than one source row: the cart would take one of the two amounts at
// random and which one it was would depend on the plan. The service already
// filters this out; the repository layer filtering it too takes the statement's
// undefined behavior out of this package.
func TestSetLineItemTotalsCannotWriteTheSameLineTwice(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	repo := repository.New(testPool.Pool())
	cart := newCart(ctx, t, svc)

	item, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: "variant_REPEAT", Title: "Repeat", Quantity: 1, UnitPrice: 1000,
	})
	require.NoError(t, err)
	before := lineAmounts(ctx, t, item.ID)

	err = repo.WithTx(ctx, func(ctx context.Context) error {
		return repo.SetLineItemTotals(ctx, cart.ID, []models.LineItemTotals{
			{LineItemID: item.ID, Totals: models.LineTotals{
				UnitPrice: 100, Subtotal: 100, Total: 100}},
			{LineItemID: item.ID, Totals: models.LineTotals{
				UnitPrice: 900, Subtotal: 900, Total: 900}},
		})
	})

	require.Error(t, err, "two amounts for the same line must not be accepted")
	assert.True(t, errors.IsInvalid(err), "the kind must be Invalid: %v", err)
	assert.Equal(t, before, lineAmounts(ctx, t, item.ID),
		"a rejected round must write nothing")
}
