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
