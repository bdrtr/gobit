//go:build integration

// The tests in this file require a real PostgreSQL instance (and therefore
// Docker); they are separated behind the `integration` tag so that `make test`
// stays fast. To run them: make test-integration
//
// Most of the claims here can be proven ONLY against a real database: that the
// partial unique index separates two concurrent requests, that a soft delete
// falls out of the read queries, that the migration can be rolled back and —
// the heart of Phase 4 — that the storefront listing gathers price and stock
// over the links with the real Query layer.
package product_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/core/db"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/link"
	"github.com/bdrtr/gobit/core/query"
	corepage "github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/product"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

const postgresImage = "postgres:16-alpine"

var (
	// testPool is the pool all the tests share.
	testPool *db.Pool
	// testDSN is the address of the shared database.
	testDSN string
)

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres brings up a single Postgres container, applies the product
// schema and runs all the tests on top of it.
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
			fmt.Fprintf(os.Stderr, "could not stop the postgres container: %v\n", termErr)
		}
	}()
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not start the postgres container: %v\n", err)
		return 1
	}

	testDSN, err = ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not obtain the connection address: %v\n", err)
		return 1
	}

	mod := product.New(product.Options{})
	if err = db.Migrate(ctx, testDSN, mod.Migrations(), mod.Name()); err != nil {
		fmt.Fprintf(os.Stderr, "could not apply the product schema: %v\n", err)
		return 1
	}

	testPool, err = db.New(ctx, db.DefaultConfig(testDSN), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not open the connection pool: %v\n", err)
		return 1
	}
	defer testPool.Close()

	// The link tables are created NOT by a migration but by the module's
	// declaration (ADR 0005): core/link creates the schema when link.Define is
	// called. In production Module.Register does this at startup and no
	// request can arrive before it.
	//
	// The reason it is done by hand here is that some tests in this file set
	// up a service directly on the repository without Registering the module.
	// Without the declaration, product listing would fall over entirely: the
	// filter carries an EXISTS condition against the link table and PostgreSQL
	// looks the relation up at parse time even when the condition short
	// circuits. That dependency is deliberate — silently treating a missing
	// link table as "no assignment at all" would bring back the very fault
	// that opened the whole catalog to every key.
	if err = defineLinks(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "could not declare the link definitions: %v\n", err)
		return 1
	}

	return m.Run()
}

// defineLinks declares product's link definitions on the shared database.
func defineLinks(ctx context.Context) error {
	links := link.New(testPool, nil)
	for _, def := range service.Definitions() {
		if err := links.Define(ctx, def); err != nil {
			return err
		}
	}
	return nil
}

// --- helpers ------------------------------------------------------------

// newService sets up a service running on the real repository.
//
// The event bus is NOT GIVEN: the tests here exercise the behavior of the
// repository and of the rules, not the events — in a service without a bus the
// events are skipped silently (see service.Service.publishProductEvent). That
// the events really are published is proven inside
// interop_integration_test.go, by Registering the module.
func newService(t *testing.T, links service.Linker, graph service.Grapher) *service.Service {
	t.Helper()

	svc, err := service.New(service.Options{
		Repo:  repository.New(testPool.Pool()),
		Links: links,
		Query: graph,
	})
	require.NoError(t, err)
	return svc
}

// uniqueHandle produces a unique handle that prevents collisions between
// tests.
//
// The tests share a single database; a fixed handle would produce a collision
// because of a record left behind by an unrelated test.
func uniqueHandle(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

// newDatabase opens the test's own database and drops it at the end.
//
// If the test that rolls the migration back dropped the shared schema, the
// other tests could not run; that is why only that test runs on a database of
// its own.
func newDatabase(ctx context.Context, t *testing.T) string {
	t.Helper()

	name := fmt.Sprintf("gobit_product_%d", time.Now().UnixNano())

	conn, err := pgx.Connect(ctx, testDSN)
	require.NoError(t, err)
	defer func() { _ = conn.Close(ctx) }()

	// A database name cannot be parameterized in SQL; the name has the fixed
	// shape the test produces (letters, underscore, digits) and takes no data
	// from the outside.
	_, err = conn.Exec(ctx, `CREATE DATABASE `+name)
	require.NoError(t, err)

	t.Cleanup(func() {
		cleanup := context.Background()
		c, cErr := pgx.Connect(cleanup, testDSN)
		if cErr != nil {
			return
		}
		defer func() { _ = c.Close(cleanup) }()
		_, _ = c.Exec(cleanup, `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`)
	})

	u, err := url.Parse(testDSN)
	require.NoError(t, err)
	u.Path = "/" + name
	return u.String()
}

// tableExists reports whether the table is present in the current schema.
func tableExists(ctx context.Context, t *testing.T, dsn, table string) bool {
	t.Helper()

	conn, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err)
	defer func() { _ = conn.Close(ctx) }()

	var exists bool
	err = conn.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists)
	require.NoError(t, err)
	return exists
}

// --- migration ----------------------------------------------------------

// TestMigrationUpDownIsReversible verifies that the schema can be applied and
// ROLLED BACK (Section 8 of the plan).
func TestMigrationUpDownIsReversible(t *testing.T) {
	ctx := context.Background()
	dsn := newDatabase(ctx, t)
	mod := product.New(product.Options{})

	require.NoError(t, db.Migrate(ctx, dsn, mod.Migrations(), mod.Name()))

	tables := []string{
		"product", "product_variant", "product_option", "product_option_value",
		"product_variant_option_value", "product_category", "product_collection",
		"product_tag", "product_image", "product_tag_map", "product_category_map",
	}
	for _, table := range tables {
		assert.True(t, tableExists(ctx, t, dsn, table), "the %s table must be created", table)
	}

	version, dirty, err := db.Version(ctx, dsn, mod.Name())
	require.NoError(t, err)
	assert.False(t, dirty, "the migration must not stop halfway")
	// The number moves with every migration added to the module, and it is
	// written out rather than derived on purpose: a count taken from the
	// embedded files would agree with itself whatever happened, and what this
	// line is for is noticing that a migration was added.
	assert.Equal(t, uint(2), version)

	require.NoError(t, db.MigrateDown(ctx, dsn, mod.Migrations(), mod.Name(), 0),
		"the schema must be reversible")
	for _, table := range tables {
		assert.False(t, tableExists(ctx, t, dsn, table), "the %s table must be dropped", table)
	}

	// A rolled back schema must be applicable again: a rollback must not block
	// the next deployment.
	require.NoError(t, db.Migrate(ctx, dsn, mod.Migrations(), mod.Name()))
	assert.True(t, tableExists(ctx, t, dsn, "product"))
}

// --- CRUD ---------------------------------------------------------------

// TestProductLifecycle verifies that creating, reading, updating and deleting
// a product works end to end against the real database.
func TestProductLifecycle(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	handle := uniqueHandle("tshirt")

	created, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle:      handle,
		Title:       "T-shirt",
		Status:      models.StatusPublished,
		Description: ptrString("Cotton"),
		Metadata:    map[string]any{"collection": "summer"},
		Options: []service.CreateOptionInput{
			{Title: "Size", Values: []string{"S", "M", "L"}},
		},
		Variants: []service.CreateVariantInput{
			{Title: "S size", SKU: ptrString(uniqueHandle("sku-s")), Options: map[string]string{"Size": "S"}},
			{Title: "M size", SKU: ptrString(uniqueHandle("sku-m")), Options: map[string]string{"Size": "M"}},
		},
		Images: []service.CreateImageInput{{URL: "https://cdn.example/1.png"}},
	})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(created.ID, "prod_"))
	assert.Equal(t, handle, created.Handle)
	require.Len(t, created.Variants, 2)
	require.Len(t, created.Options, 1)
	require.Len(t, created.Options[0].Values, 3)
	require.Len(t, created.Images, 1)
	assert.Equal(t, "summer", created.Metadata["collection"], "the jsonb field must survive the round trip")
	assert.False(t, created.CreatedAt.IsZero(), "the timestamp must come from the database")
	assert.Equal(t, time.UTC, created.CreatedAt.Location(), "the time must be UTC")

	// The variants must really be bound to the option values.
	var sVariant models.Variant
	for _, v := range created.Variants {
		if v.Title == "S size" {
			sVariant = v
		}
	}
	require.NotEmpty(t, sVariant.ID)
	require.Len(t, sVariant.OptionValues, 1)
	assert.Equal(t, "S", sVariant.OptionValues[0].Value)
	assert.Equal(t, "Size", sVariant.OptionValues[0].OptionTitle)

	fetched, err := svc.GetProduct(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, fetched.ID)
	assert.Len(t, fetched.Variants, 2)

	updated, err := svc.UpdateProduct(ctx, created.ID, service.UpdateProductInput{
		Title:  ptrString("T-shirt v2"),
		Status: ptrStatus(models.StatusArchived),
	})
	require.NoError(t, err)
	assert.Equal(t, "T-shirt v2", updated.Title)
	assert.Equal(t, models.StatusArchived, updated.Status)
	assert.True(t, updated.UpdatedAt.After(created.UpdatedAt) || updated.UpdatedAt.Equal(created.UpdatedAt),
		"the update stamp must not go backwards")

	require.NoError(t, svc.DeleteProduct(ctx, created.ID))
}

// TestSoftDeleteHidesFromReads verifies that a deleted record falls out of the
// read queries and that its handle becomes free again.
//
// The second is a direct consequence of the partial unique index (WHERE
// deleted_at IS NULL): a deleted product must not block a new product's
// handle.
func TestSoftDeleteHidesFromReads(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	handle := uniqueHandle("to-be-deleted")

	created, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle:   handle,
		Title:    "To be deleted",
		Status:   models.StatusPublished,
		Variants: []service.CreateVariantInput{{Title: "Single"}},
	})
	require.NoError(t, err)
	variantID := created.Variants[0].ID

	require.NoError(t, svc.DeleteProduct(ctx, created.ID))

	_, err = svc.GetProduct(ctx, created.ID)
	assert.True(t, coreerrors.IsNotFound(err), "a deleted product must not be readable: %v", err)

	_, err = svc.GetVariant(ctx, variantID)
	assert.True(t, coreerrors.IsNotFound(err), "the deleted product's variant must fall out too: %v", err)

	list, err := svc.ListProducts(ctx, service.ListProductsOptions{Handle: &handle})
	require.NoError(t, err)
	assert.Empty(t, list.Items, "a deleted product must not be listed")
	assert.Zero(t, totalCount(t, list), "a deleted product must not enter the count")

	// The handle must become free again.
	again, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: handle,
		Title:  "Again",
	})
	require.NoError(t, err, "the deleted product's handle must be reusable")
	assert.NotEqual(t, created.ID, again.ID)
}

// TestHandleConflictIsEnforcedByDatabase verifies that two concurrent requests
// cannot slip THROUGH each other.
//
// The service's pre-check may see both requests as "empty"; the only real
// guarantee of uniqueness is the partial unique index. This claim can be
// proven only against a real database.
func TestHandleConflictIsEnforcedByDatabase(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	handle := uniqueHandle("race")

	const attempts = 6
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		ok       int
		conflict int
		other    []error
	)

	wg.Add(attempts)
	for i := range attempts {
		go func(i int) {
			defer wg.Done()
			_, err := svc.CreateProduct(ctx, service.CreateProductInput{
				Handle: handle,
				Title:  fmt.Sprintf("Racer %d", i),
			})

			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case coreerrors.IsConflict(err):
				conflict++
			default:
				other = append(other, err)
			}
		}(i)
	}
	wg.Wait()

	assert.Empty(t, other, "unexpected error: %v", other)
	assert.Equal(t, 1, ok, "only ONE product may be created with the same handle")
	assert.Equal(t, attempts-1, conflict, "the remaining requests must get a conflict")
}

// TestDuplicateSKUIsRejected verifies that a variant's SKU is unique.
func TestDuplicateSKUIsRejected(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	sku := uniqueHandle("sku")

	first, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle:   uniqueHandle("sku-one"),
		Title:    "One",
		Variants: []service.CreateVariantInput{{Title: "Single", SKU: &sku}},
	})
	require.NoError(t, err)
	require.Len(t, first.Variants, 1)

	_, err = svc.CreateProduct(ctx, service.CreateProductInput{
		Handle:   uniqueHandle("sku-two"),
		Title:    "Two",
		Variants: []service.CreateVariantInput{{Title: "Single", SKU: &sku}},
	})
	require.Error(t, err)
	assert.True(t, coreerrors.IsConflict(err), "the same SKU must give a conflict: %v", err)
	assert.Equal(t, "product_sku_taken", coreerrors.CodeOf(err))

	// A conflicting request must not leave a half-written record behind: the
	// product must not have been written either.
	list, err := svc.ListProducts(ctx, service.ListProductsOptions{Search: ptrString("Two")})
	require.NoError(t, err)
	assert.Empty(t, list.Items, "the transaction must be rolled back; no orphan product may remain")
}

// TestVariantOptionValueIsUniquePerOption verifies that a variant carries a
// single value from the same option.
//
// The rule is in the schema (primary key: variant_id, option_id); a second
// write produces an update, not a new row.
func TestVariantOptionValueIsUniquePerOption(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)

	created, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle:  uniqueHandle("option"),
		Title:   "With options",
		Options: []service.CreateOptionInput{{Title: "Size", Values: []string{"S", "M"}}},
		Variants: []service.CreateVariantInput{
			{Title: "Will change", Options: map[string]string{"Size": "S"}},
		},
	})
	require.NoError(t, err)
	variantID := created.Variants[0].ID

	var mValueID string
	for _, value := range created.Options[0].Values {
		if value.Value == "M" {
			mValueID = value.ID
		}
	}
	require.NotEmpty(t, mValueID)

	require.NoError(t, svc.SetVariantOptionValues(ctx, variantID, []string{mValueID}))

	variant, err := svc.GetVariant(ctx, variantID)
	require.NoError(t, err)
	require.Len(t, variant.OptionValues, 1, "two values from the same option cannot be carried")
	assert.Equal(t, "M", variant.OptionValues[0].Value)
}

// TestListProductsPagesConsistently verifies that paging produces a stable
// order.
func TestListProductsPagesConsistently(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	collection, err := svc.CreateCollection(ctx, service.CreateCollectionInput{
		Title: "Paging " + uniqueHandle("collection"),
	})
	require.NoError(t, err)

	const total = 5
	for i := range total {
		_, err := svc.CreateProduct(ctx, service.CreateProductInput{
			Handle:       uniqueHandle(fmt.Sprintf("page-%d", i)),
			Title:        fmt.Sprintf("Page %d", i),
			CollectionID: &collection.ID,
		})
		require.NoError(t, err)
	}

	seen := map[string]struct{}{}
	for offset := 0; offset < total; offset += 2 {
		page, err := svc.ListProducts(ctx, service.ListProductsOptions{
			CollectionID: &collection.ID,
			Limit:        2,
			Offset:       offset,
		})
		require.NoError(t, err)
		assert.Equal(t, total, totalCount(t, page), "the count must be independent of the page")
		for _, item := range page.Items {
			_, dup := seen[item.ID]
			assert.False(t, dup, "the same record must not show up on two pages: %s", item.ID)
			seen[item.ID] = struct{}{}
		}
	}
	assert.Len(t, seen, total, "the pages must cover the whole set")
}

// TestTheTwoOrdersAreTheSameSetReadFromOppositeEnds verifies the listing order
// against a real database rather than against the string the builder produced.
//
// The SQL test next to the builder proves the seek and the ORDER BY name the
// same direction; only the database can say that the direction is the one the
// client asked for and that the set is the same either way. A statement that
// merely LOOKS reversed and quietly drops or duplicates a row would pass the
// first test and fail here.
func TestTheTwoOrdersAreTheSameSetReadFromOppositeEnds(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	collection, err := svc.CreateCollection(ctx, service.CreateCollectionInput{
		Title: "Order " + uniqueHandle("collection"),
	})
	require.NoError(t, err)

	const total = 5
	for i := range total {
		_, err := svc.CreateProduct(ctx, service.CreateProductInput{
			Handle:       uniqueHandle(fmt.Sprintf("order-%d", i)),
			Title:        fmt.Sprintf("Order %d", i),
			CollectionID: &collection.ID,
		})
		require.NoError(t, err)
	}

	ids := func(order models.ProductOrder) []string {
		page, err := svc.ListProducts(ctx, service.ListProductsOptions{
			Order:        order,
			CollectionID: &collection.ID,
			Limit:        total,
		})
		require.NoError(t, err)
		require.Len(t, page.Items, total)

		out := make([]string, 0, total)
		for _, item := range page.Items {
			out = append(out, item.ID)
		}

		return out
	}

	newest := ids(models.ProductOrderNewest)
	oldest := ids(models.ProductOrderOldest)

	assert.ElementsMatch(t, newest, oldest, "the two orders must return the SAME set")

	reversed := make([]string, len(oldest))
	for i, id := range oldest {
		reversed[len(oldest)-1-i] = id
	}
	assert.Equal(t, newest, reversed, "oldest-first must be newest-first read backwards")

	// The zero value is the order the listing always had.
	assert.Equal(t, newest, ids(""), "an unset order must still be newest first")
}

// TestPagingByCursorCoversTheSetInBothOrders verifies that the keyset seek walks
// the whole listing in either direction.
//
// The seek is the half that changes with the order, and its failure mode is not
// an error: a bound written for the wrong end returns the first page and then
// nothing, or returns the same page for ever. Walking to exhaustion and counting
// what came back is what tells those apart.
func TestPagingByCursorCoversTheSetInBothOrders(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	collection, err := svc.CreateCollection(ctx, service.CreateCollectionInput{
		Title: "Cursor " + uniqueHandle("collection"),
	})
	require.NoError(t, err)

	const total = 5
	for i := range total {
		_, err := svc.CreateProduct(ctx, service.CreateProductInput{
			Handle:       uniqueHandle(fmt.Sprintf("cursor-%d", i)),
			Title:        fmt.Sprintf("Cursor %d", i),
			CollectionID: &collection.ID,
		})
		require.NoError(t, err)
	}

	for _, order := range []models.ProductOrder{models.ProductOrderNewest, models.ProductOrderOldest} {
		seen := map[string]struct{}{}
		cursor := corepage.Cursor{}

		for range total {
			page, err := svc.ListProducts(ctx, service.ListProductsOptions{
				Order:        order,
				CollectionID: &collection.ID,
				Limit:        2,
				After:        cursor,
			})
			require.NoError(t, err, "order %q", order)

			for _, item := range page.Items {
				_, dup := seen[item.ID]
				assert.False(t, dup, "order %q: %s came back on two pages", order, item.ID)
				seen[item.ID] = struct{}{}
			}
			if page.NextCursor == "" {
				break
			}
			cursor, err = corepage.Decode(service.ProductListingFor(order), page.NextCursor)
			require.NoError(t, err, "order %q: the listing must accept its own cursor", order)
		}

		assert.Len(t, seen, total, "order %q: the cursor walk must cover the whole set", order)
	}
}

// TestCreateVariantLosesRaceWithProductDeletion verifies that a variant cannot
// be added to a product that is being deleted.
//
// The race is REAL and is seen only against a real database: because the
// delete is SOFT, the foreign key on product_variant still sees the deleted
// product's row and does not close the gap. If the check is done OUTSIDE the
// transaction, an intervening DELETE leaves behind a variant whose deleted_at
// is NULL but whose owner is deleted; that variant keeps showing up on the
// admin endpoints and in the "variant.query" provider.
//
// The ordering is not made up, it is forced BY THE LOCK: the test deletes the
// product's row inside its own transaction (without committing yet), then
// starts CreateVariant. With the correct behavior CreateVariant waits on the
// row lock and gets "not found" after the commit; if the check is done outside
// the transaction it never waits, writes the variant inside the waiting window
// and an orphan record is created.
func TestCreateVariantLosesRaceWithProductDeletion(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)

	created, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: uniqueHandle("race-delete"),
		Title:  "Being Deleted",
	})
	require.NoError(t, err)

	// Start the delete but DO NOT COMMIT: let the product row's lock stay with
	// us.
	tx, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx,
		`UPDATE product SET deleted_at = now(), updated_at = now() WHERE id = $1`, created.ID)
	require.NoError(t, err)

	type result struct {
		variant models.Variant
		err     error
	}
	done := make(chan result, 1)
	go func() {
		variant, cErr := svc.CreateVariant(ctx, created.ID, service.CreateVariantInput{Title: "Orphan"})
		done <- result{variant: variant, err: cErr}
	}()

	// The window is far more than enough for a lock-free implementation to
	// finish its check and its INSERT; a locking implementation waits here.
	select {
	case got := <-done:
		t.Fatalf("the variant was written before the delete was committed (orphan record): %+v, err=%v", got.variant, got.err)
	case <-time.After(time.Second):
	}

	require.NoError(t, tx.Commit(ctx))

	got := <-done
	require.Error(t, got.err, "a variant must not be addable to a deleted product")
	assert.True(t, coreerrors.IsNotFound(got.err), "not found was expected: %v", got.err)

	variants, err := svc.ListVariants(ctx, service.ListVariantsOptions{ProductID: &created.ID})
	require.NoError(t, err)
	assert.Empty(t, variants.Items, "no live variant may remain under a deleted product")
}

// ptrString returns the address of the string.
func ptrString(v string) *string { return &v }

// ptrInt32 produces an integer pointer.
func ptrInt32(v int32) *int32 { return &v }

// ptrBool produces a boolean pointer.
func ptrBool(v bool) *bool { return &v }

// ptrStatus returns the address of the status.
func ptrStatus(v models.Status) *models.Status { return &v }

// totalCount returns the result's total count and also verifies THAT IT WAS
// COUNTED.
//
// The count is a pointer and nil means "not counted" (see
// [service.ListResult]). A raw dereference would give a panic instead of a
// readable error if the count were one day silently turned off.
func totalCount[T any](t *testing.T, res service.ListResult[T]) int {
	t.Helper()
	require.NotNil(t, res.Count, "the count should have been computed")

	return *res.Count
}

// TestProductColumnMappingHasNotDrifted verifies that the hand-written column
// list and the field order sqlc produces have not diverged.
//
// # Why a separate test is needed
//
// The storefront list resolves the rows BY POSITION (pgx.RowToStructByPos),
// because resolving by name does not work with sqlc's untagged fields. A
// positional mapping can break silently: in this table handle and title are
// adjacent and both are text, subtitle/description/thumbnail are three texts,
// weight/length/height/width four integers. Two neighbors of the same type
// swapping places produces NO error at all — it merely swaps every product's
// title with its handle.
//
// That is why the test writes a DISTINGUISHABLE value into every field: if two
// fields swap, the assertion fails. A test that only looked at "is the field
// filled" could not see that swap.
func TestProductColumnMappingHasNotDrifted(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	handle := uniqueHandle("column-mapping")

	created, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: handle,
		Title:  "TITLE-" + handle,
		Status: models.StatusPublished,
		// ALL of the neighboring fields of the same type carry different
		// values.
		Subtitle:      ptrString("SUBTITLE-distinctive"),
		Description:   ptrString("DESCRIPTION-distinctive"),
		Thumbnail:     ptrString("THUMBNAIL-distinctive"),
		Material:      ptrString("MATERIAL-distinctive"),
		OriginCountry: ptrString("TR"),
		Weight:        ptrInt32(1001),
		Length:        ptrInt32(1002),
		Height:        ptrInt32(1003),
		Width:         ptrInt32(1004),
		Discountable:  ptrBool(false),
		Metadata:      map[string]any{"marker": "distinctive"},
	})
	require.NoError(t, err)

	// The storefront list uses the HAND-WRITTEN query; that is the path which
	// exercises the mapping.
	page, err := svc.ListStoreProducts(ctx, service.StoreListOptions{
		Search: ptrString("TITLE-" + handle),
		Limit:  10,
	})
	require.NoError(t, err)

	var found *service.StoreProduct

	for i := range page.Items {
		if page.Items[i].ID == created.ID {
			found = &page.Items[i]
		}
	}

	require.NotNil(t, found, "the product must be found in the storefront list")

	assert.Equal(t, handle, found.Handle, "handle and title may have swapped places")
	assert.Equal(t, "TITLE-"+handle, found.Title, "title and handle may have swapped places")
	assert.Equal(t, "SUBTITLE-distinctive", derefString(found.Subtitle))
	assert.Equal(t, "DESCRIPTION-distinctive", derefString(found.Description))
	assert.Equal(t, "THUMBNAIL-distinctive", derefString(found.Thumbnail))
	assert.Equal(t, "MATERIAL-distinctive", derefString(found.Material))
	assert.Equal(t, "TR", derefString(found.OriginCountry))
	assert.Equal(t, int32(1001), derefInt32(found.Weight), "weight may have been mixed up with length/height/width")
	assert.Equal(t, int32(1002), derefInt32(found.Length))
	assert.Equal(t, int32(1003), derefInt32(found.Height))
	assert.Equal(t, int32(1004), derefInt32(found.Width))
	assert.Equal(t, models.StatusPublished, found.Status)
	assert.False(t, found.Discountable, "discountable may have swapped places with is_giftcard")
	assert.False(t, found.IsGiftcard)
	assert.Equal(t, "distinctive", found.Metadata["marker"])
	assert.False(t, found.CreatedAt.IsZero())
	assert.False(t, found.UpdatedAt.IsZero())
}

// derefString safely resolves a pointer; it returns an empty string if it is
// nil.
func derefString(p *string) string {
	if p == nil {
		return ""
	}

	return *p
}

// derefInt32 safely resolves a pointer; it returns zero if it is nil.
func derefInt32(p *int32) int32 {
	if p == nil {
		return 0
	}

	return *p
}

// TestTheCategoryVisibilityFilterIsAppliedBySQL is the half a fake cannot prove.
//
// The service tests use an in-memory store, and an in-memory store agreeing
// with itself says nothing about the predicate the database applies — the two
// are separate implementations of one rule, and this repository has already
// been bitten by a fake that disagreed with its query. What is checked here is
// the SQL: that a switched-off and an operator-only category are absent from
// BOTH the page and the count, and that the admin view still has them.
func TestTheCategoryVisibilityFilterIsAppliedBySQL(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)

	parent, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name:   "Visibility " + uniqueHandle("root"),
		Handle: uniqueHandle("visibility-root"),
	})
	require.NoError(t, err)

	shown, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Shown", Handle: uniqueHandle("shown"), ParentID: &parent.ID,
	})
	require.NoError(t, err)
	_, err = svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Switched off", Handle: uniqueHandle("switched-off"),
		ParentID: &parent.ID, IsActive: ptrBool(false),
	})
	require.NoError(t, err)
	_, err = svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Operators only", Handle: uniqueHandle("operators-only"),
		ParentID: &parent.ID, IsInternal: true,
	})
	require.NoError(t, err)

	shop, err := svc.ListCategories(ctx, service.ListCategoriesOptions{
		ParentID: &parent.ID, PublicOnly: true,
	})
	require.NoError(t, err)
	require.Len(t, shop.Items, 1, "the database returned a category the storefront must not see")
	assert.Equal(t, shown.ID, shop.Items[0].ID)
	require.NotNil(t, shop.Count)
	assert.Equal(t, 1, *shop.Count,
		"the COUNT query does not apply the same predicate as the listing; a storefront "+
			"would ask for pages that never fill")

	admin, err := svc.ListCategories(ctx, service.ListCategoriesOptions{ParentID: &parent.ID})
	require.NoError(t, err)
	assert.Len(t, admin.Items, 3, "the admin view lost the categories the merchant has to manage")
}

// TestTheCategoryAndTagFiltersReturnAProductOnce is the claim a join would
// break.
//
// A product may sit in several categories and carry several tags. Written as a
// join the row multiplies: the product comes back once per membership, the page
// holds fewer products than its limit says, and the count becomes a number of
// MEMBERSHIPS. EXISTS asks the only question being asked, and this is the test
// that says so against the real database rather than against a fake that agrees
// with itself.
func TestTheCategoryAndTagFiltersReturnAProductOnce(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)

	first, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Shirts", Handle: uniqueHandle("shirts")})
	require.NoError(t, err)
	second, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Summer", Handle: uniqueHandle("summer")})
	require.NoError(t, err)

	sale, err := svc.CreateTag(ctx, uniqueHandle("sale"))
	require.NoError(t, err)
	newIn, err := svc.CreateTag(ctx, uniqueHandle("new"))
	require.NoError(t, err)

	product, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle:      uniqueHandle("multi"),
		Title:       "In two categories and carrying two tags",
		Status:      models.StatusPublished,
		CategoryIDs: []string{first.ID, second.ID},
		TagIDs:      []string{sale.ID, newIn.ID},
	})
	require.NoError(t, err)

	// A second product OUTSIDE the category. Without it the count assertion below
	// cannot discriminate: a count query that lost the filter would count one
	// product either way, and dropping the predicate from the count alone — the
	// mistake that makes a storefront ask for pages that never fill — would pass.
	_, err = svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: uniqueHandle("uncategorised"),
		Title:  "In no category and carrying no tag",
		Status: models.StatusPublished,
	})
	require.NoError(t, err)

	byCategory, err := svc.ListProducts(ctx, service.ListProductsOptions{CategoryID: &first.ID})
	require.NoError(t, err)
	require.Len(t, byCategory.Items, 1,
		"the product came back more than once; the filter multiplies the row")
	assert.Equal(t, product.ID, byCategory.Items[0].ID)
	require.NotNil(t, byCategory.Count)
	assert.Equal(t, 1, *byCategory.Count, "the count counted memberships rather than products")

	byTag, err := svc.ListProducts(ctx, service.ListProductsOptions{TagID: &sale.ID})
	require.NoError(t, err)
	require.Len(t, byTag.Items, 1)
	assert.Equal(t, product.ID, byTag.Items[0].ID)

	// A filter that matches nothing has to return nothing rather than everything:
	// an "IS NULL OR" predicate written the wrong way round degrades to no filter
	// at all, which is silent and looks like a wide catalog.
	other, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Empty", Handle: uniqueHandle("empty")})
	require.NoError(t, err)
	empty, err := svc.ListProducts(ctx, service.ListProductsOptions{CategoryID: &other.ID})
	require.NoError(t, err)
	assert.Empty(t, empty.Items, "a category with no products returned products")
}

// TestTheReadLayerAnswersTheTaxonomyFiltersAgainstTheDatabase is the provider's
// half of [TestTheCategoryAndTagFiltersReturnAProductOnce].
//
// That test drives the SERVICE. The read layer is a different caller with its
// own filter dispatch, and until this one existed nothing checked that the two
// reach the same predicate: the provider's unit tests run against the in-memory
// store, and a fake that agrees with itself says nothing about the EXISTS
// subqueries — those live in SQL (see repository/saleschannel.go) and only a
// real database can be asked whether they were reached.
//
// The comparison against the service is deliberate. Asserting a hand-written set
// of ids would only prove that the provider returns SOMETHING; asserting that it
// returns what the service's own listing returns is what makes the panel and the
// shop one answer.
func TestTheReadLayerAnswersTheTaxonomyFiltersAgainstTheDatabase(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	products := service.NewProductProvider(repository.New(testPool.Pool()))

	shirts, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Shirts", Handle: uniqueHandle("provider-shirts")})
	require.NoError(t, err)
	summer, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Summer", Handle: uniqueHandle("provider-summer")})
	require.NoError(t, err)
	empty, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Empty", Handle: uniqueHandle("provider-empty")})
	require.NoError(t, err)
	sale, err := svc.CreateTag(ctx, uniqueHandle("provider-sale"))
	require.NoError(t, err)

	// The listed product is in BOTH categories and carries the tag; the draft is
	// in one of them and carries none. Every assertion below can therefore fail:
	// a filter that lost its value, a filter that was dropped, and a status that
	// stopped being carried alongside a taxonomy filter all show up as a
	// different set.
	listed, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle:      uniqueHandle("provider-listed"),
		Title:       "Listed in two categories",
		Status:      models.StatusPublished,
		CategoryIDs: []string{shirts.ID, summer.ID},
		TagIDs:      []string{sale.ID},
	})
	require.NoError(t, err)
	draft, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle:      uniqueHandle("provider-draft"),
		Title:       "Draft in one category",
		Status:      models.StatusDraft,
		CategoryIDs: []string{shirts.ID},
	})
	require.NoError(t, err)

	byCategory, err := products.List(ctx, query.ListOptions{
		Filters: map[string]any{"category_id": shirts.ID},
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{listed.ID, draft.ID}, providerIDs(t, byCategory),
		"the read layer's category filter did not reach the EXISTS subquery")

	fromService, err := svc.ListProducts(ctx, service.ListProductsOptions{CategoryID: &shirts.ID})
	require.NoError(t, err)
	assert.ElementsMatch(t, modelIDs(fromService.Items), providerIDs(t, byCategory),
		"the panel and the shop are reading the same catalog with two different answers")

	// The product sits in two categories. Asked about one of them it must appear
	// ONCE: a row that multiplied would make a page hold fewer products than its
	// limit promises.
	bySecondCategory, err := products.List(ctx, query.ListOptions{
		Filters: map[string]any{"category_id": summer.ID},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{listed.ID}, providerIDs(t, bySecondCategory))

	byTag, err := products.List(ctx, query.ListOptions{
		Filters: map[string]any{"tag_id": sale.ID},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{listed.ID}, providerIDs(t, byTag))

	withStatus, err := products.List(ctx, query.ListOptions{
		Filters: map[string]any{"category_id": shirts.ID, "status": string(models.StatusPublished)},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{listed.ID}, providerIDs(t, withStatus),
		"the draft product in the same category was not filtered out")

	both, err := products.List(ctx, query.ListOptions{
		Filters: map[string]any{"category_id": summer.ID, "tag_id": sale.ID},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{listed.ID}, providerIDs(t, both))

	// A category with no products. An "IS NULL OR" predicate written the wrong
	// way round degrades into no filter at all — which returns the whole catalog
	// and looks like a wide answer rather than a broken one.
	none, err := products.List(ctx, query.ListOptions{
		Filters: map[string]any{"category_id": empty.ID},
	})
	require.NoError(t, err)
	assert.Empty(t, none, "a category with no products returned products")

	// The limit still binds on the filtered path: it goes to SQL as a LIMIT.
	page, err := products.List(ctx, query.ListOptions{
		Filters: map[string]any{"category_id": shirts.ID}, Limit: 1,
	})
	require.NoError(t, err)
	assert.Len(t, page, 1)

	// And the refusal holds against the real store too. It is a decision of the
	// dispatch, not of the fake: on the id path the membership is never read, so
	// the filter could not be applied and the caller is told instead of being
	// handed a page that quietly ignored it.
	_, err = products.List(ctx, query.ListOptions{
		Filters: map[string]any{"id": listed.ID, "category_id": shirts.ID},
	})
	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err),
		"combining an id with a taxonomy filter should be refused: %v", err)
}

// TestTheReadLayerSearchesTheTitleAgainstTheDatabase proves the free-text
// filter where it is actually defined.
//
// The provider's unit tests run against the in-memory store, and that store
// matches with strings.ToLower + strings.Contains — a Go answer to a question
// only PostgreSQL answers in production. A fake agreeing with itself proves
// nothing about ILIKE: whether the pattern really folds case, whether it really
// matches in the MIDDLE of a title rather than at its start, and whether the
// term reaches parameter $4 at all are properties of the SQL in
// repository/saleschannel.go.
//
// The token is unique per run because the tests share one database: a fixed
// word like "shirt" would be matched by rows an unrelated test left behind, and
// the assertions would then pass or fail depending on what ran before them.
//
// # The fixture is ASCII, deliberately
//
// The word appears in two cases and never outside ASCII. A non-ASCII fixture
// would not test this module at all — it would test the CLUSTER's CTYPE, which
// folds ASCII only when the cluster was created with --locale=C (see
// core/db/casefold.go and ADR 0015). This test would then pass or fail
// depending on how the container that runs it was initialized, and it would
// still say nothing about the filter dispatch it exists to cover.
func TestTheReadLayerSearchesTheTitleAgainstTheDatabase(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	products := service.NewProductProvider(repository.New(testPool.Pool()))

	linen, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Search linen", Handle: uniqueHandle("search-linen")})
	require.NoError(t, err)

	// The token carries the case difference: one title spells it in upper case,
	// the other in lower. A fold applied to a single side answers one of the two
	// searches below and fails the other.
	token := uniqueHandle("qsearch")
	upperTitle := "Blue " + strings.ToUpper(token) + " coat"
	lowerTitle := "Red " + token + " coat"

	published, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle:      uniqueHandle("search-published"),
		Title:       upperTitle,
		Status:      models.StatusPublished,
		CategoryIDs: []string{linen.ID},
	})
	require.NoError(t, err)
	draft, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: uniqueHandle("search-draft"),
		Title:  lowerTitle,
		Status: models.StatusDraft,
	})
	require.NoError(t, err)

	byLower, err := products.List(ctx, query.ListOptions{
		Filters: map[string]any{"q": token},
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{published.ID, draft.ID}, providerIDs(t, byLower),
		"the read layer's search did not reach the ILIKE pattern")

	byUpper, err := products.List(ctx, query.ListOptions{
		Filters: map[string]any{"q": strings.ToUpper(token)},
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{published.ID, draft.ID}, providerIDs(t, byUpper),
		"ILIKE folds both sides; a match found in one direction only is not case-insensitive")

	// A fragment cut out of the MIDDLE of the token: it is neither the start of
	// the title nor a whole word in it, so an anchored pattern ('term%') and a
	// word-boundary match both return nothing here.
	middle := token[3 : len(token)-3]
	byFragment, err := products.List(ctx, query.ListOptions{
		Filters: map[string]any{"q": middle},
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{published.ID, draft.ID}, providerIDs(t, byFragment),
		"the leading wildcard is what makes this a substring search")

	// The same question asked of the module's own listing. The two have to give
	// ONE answer; that is the whole point of the read layer spelling the filter
	// the way the storefront spells it.
	fromService, err := svc.ListProducts(ctx, service.ListProductsOptions{Search: &token})
	require.NoError(t, err)
	assert.ElementsMatch(t, modelIDs(fromService.Items), providerIDs(t, byLower),
		"the panel and the shop are searching the same catalog with two different answers")

	withStatus, err := products.List(ctx, query.ListOptions{
		Filters: map[string]any{"q": token, "status": string(models.StatusPublished)},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{published.ID}, providerIDs(t, withStatus),
		"the draft product matching the same term was not filtered out")

	withCategory, err := products.List(ctx, query.ListOptions{
		Filters: map[string]any{"q": token, "category_id": linen.ID},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{published.ID}, providerIDs(t, withCategory),
		"the search and the category EXISTS subquery should narrow together")

	// A term no title carries. An "IS NULL OR" predicate written the wrong way
	// round degrades into no filter at all — which returns the whole catalog and
	// looks like a wide answer rather than a broken one.
	none, err := products.List(ctx, query.ListOptions{
		Filters: map[string]any{"q": uniqueHandle("qmissing")},
	})
	require.NoError(t, err)
	assert.Empty(t, none, "a term no product carries returned products")

	// The normalization holds against the real store too, and here it is not a
	// matter of taste: a whitespace term handed to SQL as it came becomes
	// ILIKE '%   %' and returns an EMPTY catalog, while an empty one becomes
	// ILIKE '%%' and returns all of it. Both are silent. The rule is that
	// neither is a criterion, so the answer must be the unfiltered page.
	unfiltered, err := products.List(ctx, query.ListOptions{})
	require.NoError(t, err)
	for _, term := range []string{"", "   "} {
		blank, blankErr := products.List(ctx, query.ListOptions{
			Filters: map[string]any{"q": term},
		})
		require.NoError(t, blankErr, "a term of %q is not an error; it is not a criterion", term)
		assert.ElementsMatch(t, providerIDs(t, unfiltered), providerIDs(t, blank),
			"a term of %q must answer exactly what no term answers", term)
	}

	// A padded term is TRIMMED rather than passed through: '%  <token>  %'
	// matches neither of the two titles, both of which carry the token between
	// single spaces.
	padded := "  " + token + "  "
	byPadded, err := products.List(ctx, query.ListOptions{
		Filters: map[string]any{"q": padded},
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{published.ID, draft.ID}, providerIDs(t, byPadded),
		"the term that reached the query was not the trimmed one")

	// And the refusal holds against the real store as well. It is a decision of
	// the dispatch, not of the fake: on the id path the title would be matched
	// by a Go rule instead of the database's, and the two answer differently
	// outside ASCII (see core/db/casefold.go).
	_, err = products.List(ctx, query.ListOptions{
		Filters: map[string]any{"id": published.ID, "q": token},
	})
	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err),
		"combining an id with the search should be refused: %v", err)

	// A whitespace term is normalized away BEFORE the refusal is decided, so it
	// cannot refuse anything: an id filter beside a cleared search box is a
	// perfectly answerable request.
	cleared, err := products.List(ctx, query.ListOptions{
		Filters: map[string]any{"id": published.ID, "q": "  "},
	})
	require.NoError(t, err, "a whitespace term is not part of the request")
	assert.Equal(t, []string{published.ID}, providerIDs(t, cleared))
}

// TestTheCategoryProviderReadsTheRealTable proves the vocabulary entity against
// the database.
//
// The claims that need a real query are the two flag columns and the tree: the
// listing path applies them in SQL ([repository.CategoryFilter].PublicOnly) and
// the id path applies them in Go, so this is also where the two implementations
// of one predicate can be caught disagreeing.
func TestTheCategoryProviderReadsTheRealTable(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	categories := service.NewCategoryProvider(repository.New(testPool.Pool()))

	parent, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Vocabulary root", Handle: uniqueHandle("vocabulary-root")})
	require.NoError(t, err)
	shown, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Shown", Handle: uniqueHandle("vocabulary-shown"), ParentID: &parent.ID})
	require.NoError(t, err)
	switchedOff, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Switched off", Handle: uniqueHandle("vocabulary-off"),
		ParentID: &parent.ID, IsActive: ptrBool(false)})
	require.NoError(t, err)
	internalOnly, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Operators only", Handle: uniqueHandle("vocabulary-internal"),
		ParentID: &parent.ID, IsInternal: true})
	require.NoError(t, err)

	children, err := categories.List(ctx, query.ListOptions{
		Filters: map[string]any{"parent_id": parent.ID},
	})
	require.NoError(t, err)
	assert.ElementsMatch(t,
		[]string{shown.ID, switchedOff.ID, internalOnly.ID}, providerIDs(t, children),
		"the read layer hid a category the operator has to manage")

	public, err := categories.List(ctx, query.ListOptions{
		Filters: map[string]any{"parent_id": parent.ID, "public_only": true},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{shown.ID}, providerIDs(t, public))

	// The same question asked of the module's own storefront listing. The two
	// have to give one answer; that is the whole point of the flag having one
	// name on both surfaces.
	fromService, err := svc.ListCategories(ctx, service.ListCategoriesOptions{
		ParentID: &parent.ID, PublicOnly: true,
	})
	require.NoError(t, err)
	assert.Equal(t, modelIDs(fromService.Items), providerIDs(t, public))

	// The id path applies the flag in Go, over the rows the batch read returned.
	namedPublic, err := categories.List(ctx, query.ListOptions{
		Filters: map[string]any{
			"ids":         []string{shown.ID, switchedOff.ID, internalOnly.ID},
			"public_only": true,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{shown.ID}, providerIDs(t, namedPublic),
		"the id path and the listing path disagree about the same filter")

	// An expansion hides nothing: the id was already named by the caller, and
	// answering "no such record" for a switched-off category would leave it
	// holding a dangling reference.
	expanded, err := categories.FetchByIDs(ctx, []string{switchedOff.ID, "pcat_missing"}, nil)
	require.NoError(t, err, "an id that is not found is not an error")
	require.Len(t, expanded, 1)
	assert.Equal(t, false, expanded[0]["is_active"],
		"the record must say that this category is switched off")
}

// providerIDs returns the id field of every record.
func providerIDs(t *testing.T, records []query.Record) []string {
	t.Helper()

	out := make([]string, 0, len(records))
	for _, rec := range records {
		id, ok := rec[query.IDField].(string)
		require.True(t, ok, "the record carries no readable %q field: %#v", query.IDField, rec)
		out = append(out, id)
	}
	return out
}

// modelIDs returns the ids of the records the SERVICE returned.
//
// It is generic because the comparison is made for two different models
// (product and category) and a per-type copy would be the same three lines
// twice.
func modelIDs[T interface {
	models.Product | models.Category
}](items []T) []string {
	out := make([]string, 0, len(items))
	for i := range items {
		switch item := any(items[i]).(type) {
		case models.Product:
			out = append(out, item.ID)
		case models.Category:
			out = append(out, item.ID)
		}
	}
	return out
}

// --- taxonomy deletes (D18) ---------------------------------------------

// TestDeletingATagHidesItFromItsProductsAndFreesItsValue proves against a real
// database the two claims the fake cannot make.
//
// The first is the JOIN: ListTagsByProductIDs joins product_tag with
// "t.deleted_at IS NULL", so a deleted tag falls off every product that carried
// it WITHOUT a single row of product_tag_map being touched. That predicate was
// written for a state the module could not reach until the delete existed.
//
// The second is the PARTIAL INDEX: product_tag_value_uniq is built
// "WHERE deleted_at IS NULL" precisely so a deleted value does not block a new
// tag. Nothing had ever exercised it, because nothing had ever deleted a tag.
func TestDeletingATagHidesItFromItsProductsAndFreesItsValue(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	value := uniqueHandle("misspelled")

	tag, err := svc.CreateTag(ctx, value)
	require.NoError(t, err)
	keep, err := svc.CreateTag(ctx, uniqueHandle("kept"))
	require.NoError(t, err)

	product, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: uniqueHandle("tagged"),
		Title:  "Carries two tags",
		Status: models.StatusPublished,
		TagIDs: []string{tag.ID, keep.ID},
	})
	require.NoError(t, err)

	before, err := svc.GetProduct(ctx, product.ID)
	require.NoError(t, err)
	require.Len(t, before.Tags, 2)

	require.NoError(t, svc.DeleteTag(ctx, tag.ID))

	after, err := svc.GetProduct(ctx, product.ID)
	require.NoError(t, err)
	require.Len(t, after.Tags, 1, "the deleted tag must fall off the product")
	assert.Equal(t, keep.ID, after.Tags[0].ID, "the other tag must be untouched")

	again, err := svc.CreateTag(ctx, value)
	require.NoError(t, err, "the deleted tag's value must be free again")
	assert.NotEqual(t, tag.ID, again.ID)
}

// TestDeletingACollectionReleasesItsProductsInTheDatabase proves that the
// pointer is really cleared.
//
// product.collection_id says "ON DELETE SET NULL" and the database will never
// run it here: a soft delete is an UPDATE and the collection's row stays where
// it is. The listing filtered by collection_id is the check that matters —
// it does not join the collection, so a product left pointing at a deleted
// collection keeps coming back under it.
func TestDeletingACollectionReleasesItsProductsInTheDatabase(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)

	collection, err := svc.CreateCollection(ctx, service.CreateCollectionInput{
		Title: "Summer 2026", Handle: uniqueHandle("summer-2026")})
	require.NoError(t, err)

	inside, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle:       uniqueHandle("in-collection"),
		Title:        "In the collection",
		Status:       models.StatusPublished,
		CollectionID: &collection.ID,
	})
	require.NoError(t, err)

	listed, err := svc.ListProducts(ctx, service.ListProductsOptions{CollectionID: &collection.ID})
	require.NoError(t, err)
	require.Len(t, listed.Items, 1)

	require.NoError(t, svc.DeleteCollection(ctx, collection.ID))

	_, err = svc.GetCollection(ctx, collection.ID)
	assert.True(t, coreerrors.IsNotFound(err), "a deleted collection must not be readable: %v", err)

	released, err := svc.GetProduct(ctx, inside.ID)
	require.NoError(t, err)
	assert.Nil(t, released.CollectionID, "the product must have been released")

	empty, err := svc.ListProducts(ctx, service.ListProductsOptions{CollectionID: &collection.ID})
	require.NoError(t, err)
	assert.Empty(t, empty.Items,
		"a deleted collection still lists products; the release did not run")
}

// TestDeletingAnOptionValueIsRefusedWhileAVariantCarriesIt proves the guard
// against the real table.
//
// The count joins product_variant, and the second half of the test is what
// makes that join matter: once the carrier is soft-deleted the same value can
// be removed, because the binding row in product_variant_option_value is still
// there and only the variant's own deleted_at tells the two cases apart.
func TestDeletingAnOptionValueIsRefusedWhileAVariantCarriesIt(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)

	created, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: uniqueHandle("sized"),
		Title:  "T-shirt",
		Status: models.StatusPublished,
		Options: []service.CreateOptionInput{
			{Title: "Size", Values: []string{"S", "M"}},
		},
		Variants: []service.CreateVariantInput{
			{Title: "Small", Options: map[string]string{"Size": "S"}},
		},
	})
	require.NoError(t, err)

	options, err := svc.ListOptions(ctx, created.ID)
	require.NoError(t, err)
	require.Len(t, options, 1)
	require.Len(t, options[0].Values, 2)

	var inUse, unused string
	for _, value := range options[0].Values {
		if value.Value == "S" {
			inUse = value.ID
		} else {
			unused = value.ID
		}
	}

	err = svc.DeleteOptionValue(ctx, inUse)
	require.Error(t, err)
	assert.True(t, coreerrors.IsConflict(err), "a conflict was expected: %v", err)

	require.NoError(t, svc.DeleteOptionValue(ctx, unused))
	read, err := svc.GetProduct(ctx, created.ID)
	require.NoError(t, err)
	require.Len(t, read.Options, 1)
	assert.Len(t, read.Options[0].Values, 1, "the deleted value must fall out of the read")

	require.NoError(t, svc.DeleteVariant(ctx, created.Variants[0].ID))
	require.NoError(t, svc.DeleteOptionValue(ctx, inUse),
		"once the carrier is deleted the value must be deletable")
}

// TestDeletingAnOptionLeavesNoLivingValue is the cascade, checked with SQL
// rather than through a read.
//
// No read in this module can see the difference — every path to a value goes
// through its option and the option is stamped — so the only honest way to
// check it is to ask the table directly. That invisibility is exactly why
// product_option_value.deleted_at stayed unwritten from the first migration
// until 2026-09-06.
func TestDeletingAnOptionLeavesNoLivingValue(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)

	created, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: uniqueHandle("cascade"),
		Title:  "T-shirt",
		Status: models.StatusPublished,
		Options: []service.CreateOptionInput{
			{Title: "Size", Values: []string{"S", "M"}},
		},
	})
	require.NoError(t, err)

	options, err := svc.ListOptions(ctx, created.ID)
	require.NoError(t, err)
	require.Len(t, options, 1)
	optionID := options[0].ID

	assert.Equal(t, 2, liveOptionValues(ctx, t, optionID))
	require.NoError(t, svc.DeleteOption(ctx, optionID))
	assert.Zero(t, liveOptionValues(ctx, t, optionID),
		"the option's values are still alive under a deleted option")

	// And the same claim for the delete that reaches every child at once.
	second, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: uniqueHandle("cascade-product"),
		Title:  "Another T-shirt",
		Status: models.StatusPublished,
		Options: []service.CreateOptionInput{
			{Title: "Size", Values: []string{"S", "M", "L"}},
		},
	})
	require.NoError(t, err)
	secondOptions, err := svc.ListOptions(ctx, second.ID)
	require.NoError(t, err)
	require.Len(t, secondOptions, 1)

	require.NoError(t, svc.DeleteProduct(ctx, second.ID))
	assert.Zero(t, liveOptionValues(ctx, t, secondOptions[0].ID),
		"a deleted product left living option values behind")
}

// liveOptionValues counts the option's values that are not soft-deleted.
func liveOptionValues(ctx context.Context, t *testing.T, optionID string) int {
	t.Helper()

	var n int
	err := testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM product_option_value WHERE option_id = $1 AND deleted_at IS NULL`,
		optionID).Scan(&n)
	require.NoError(t, err)
	return n
}

// TestPartialVariantUpdateDoesNotResetTheFlags proves that an update which does
// not name manage_inventory or allow_backorder leaves both alone.
//
// # Why this needs a real database
//
// The preservation is not written in Go. The service copies the two *bool
// straight from the input into the patch and the repository hands them to
// sqlc; the whole rule lives in one SQL line each —
// `manage_inventory = COALESCE(sqlc.narg('manage_inventory')::boolean,
// manage_inventory)` in queries/variant.sql. The service package's fake
// repository does not model those columns at all, so a unit test there would
// pass against a query that had dropped the COALESCE and reset the column to
// NULL, or to the argument, on every rename.
//
// # Why it is worth a test even though nothing reads the flags
//
// This is the second way a carried-only flag gets silently poisoned, and it is
// worse than a wrong default because it needs no new products: it corrupts the
// rows a shop has ALREADY curated. A merchant who set allow_backorder on a
// pre-order line and later fixes a typo in its title would find the flag back
// at its default, with no error and nothing to notice — the value is not read
// today, so no behavior changes to give the loss away. It would surface only
// when docs/gaps.md A6 is finally answered and a reader is written, by which
// time the original intent is unrecoverable.
//
// # Both flags are set AGAINST their defaults first
//
// If the test wrote the default values it would pass against a query that
// ignores the columns entirely and re-derives them, which is exactly the bug
// class in question. Starting from manage_inventory=false and
// allow_backorder=true means every wrong implementation — reset to default,
// reset to the nil argument, swap the two columns — produces a different
// answer from the right one.
func TestPartialVariantUpdateDoesNotResetTheFlags(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)

	created, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: uniqueHandle("flag-preservation"),
		Title:  "Pre-order shirt",
		Status: models.StatusPublished,
		Variants: []service.CreateVariantInput{{
			Title: "One size",
			// The opposite of both defaults, so that a "reset to default"
			// implementation cannot pass.
			ManageInventory: ptrBool(false),
			AllowBackorder:  ptrBool(true),
		}},
	})
	require.NoError(t, err)
	require.Len(t, created.Variants, 1)

	variantID := created.Variants[0].ID
	require.False(t, created.Variants[0].ManageInventory, "the setup value must be stored as sent")
	require.True(t, created.Variants[0].AllowBackorder, "the setup value must be stored as sent")

	// A rename: the flags are not named at all, which is the whole point.
	updated, err := svc.UpdateVariant(ctx, variantID, service.UpdateVariantInput{
		Title: ptrString("One size (renamed)"),
	})
	require.NoError(t, err)

	assert.Equal(t, "One size (renamed)", updated.Title, "the rename itself has to take effect")
	assert.False(t, updated.ManageInventory,
		"an update that does not name manage_inventory must leave it alone; "+
			"nothing reads this flag today, so a reset would be silent until A6 is answered")
	assert.True(t, updated.AllowBackorder,
		"an update that does not name allow_backorder must leave it alone")

	// Read it back through a SECOND query rather than trusting the RETURNING
	// row: the update and the read use different statements, and a RETURNING
	// clause can echo the intended value while the row on disk holds another.
	reread, err := svc.GetVariant(ctx, variantID)
	require.NoError(t, err)
	assert.False(t, reread.ManageInventory, "the stored row, not just the RETURNING row, keeps the flag")
	assert.True(t, reread.AllowBackorder, "the stored row, not just the RETURNING row, keeps the flag")
}
