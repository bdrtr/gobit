//go:build integration

// The tests in this file need a real PostgreSQL instance (and therefore
// Docker); they are separated behind the `integration` tag so that `make test`
// stays fast. To run them: make test-integration
//
// The unit tests run against a fake link service and prove Query's logic alone.
// The tests here verify the end-to-end flow against the REAL link service (link
// tables living in Postgres): two dummy modules, a real link definition, real
// links, providers resolved from the container by name. This is what Phase 2's
// "verify end to end with two dummy modules" item asks for.
package query_test

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/link"
	"github.com/bdrtr/gobit/internal/core/query"
)

const postgresImage = "postgres:16-alpine"

// testPool is the pool every integration test shares.
var testPool *db.Pool

// The link definitions used in the integration tests. Between the two dummy
// modules both a many-ended and a single-ended relation is built, so that the
// shape coming from the cardinality is verified against a real database too.
var (
	itemPrice = link.LinkDefinition{
		Name:        "item_price",
		From:        link.LinkSide{Module: "shop_item", Field: "item_id"},
		To:          link.LinkSide{Module: "shop_price", Field: "price_id"},
		Cardinality: link.OneToMany,
	}
	itemMainPrice = link.LinkDefinition{
		Name:        "item_main_price",
		From:        link.LinkSide{Module: "shop_item", Field: "item_id"},
		To:          link.LinkSide{Module: "shop_price", Field: "price_id"},
		Cardinality: link.OneToOne,
	}
)

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres brings up a single Postgres container and runs every test
// against it. It is a separate function because os.Exit skips the defers.
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
		fmt.Fprintf(os.Stderr, "the connection string could not be read: %v\n", err)
		return 1
	}

	testPool, err = db.New(ctx, db.DefaultConfig(dsn), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "the pool could not be built: %v\n", err)
		return 1
	}
	defer testPool.Close()

	return m.Run()
}

// --- the dummy modules ------------------------------------------------------

// dummyModule stands for a commerce module in the integration tests: it reads
// ITS OWN table only (Principle 2.1) and satisfies the query.Provider surface
// put into the container under "<entity>.query" (ADR 0004).
//
// The provider does a batch read with a single SQL statement, like a real
// module's repository; the call counters prove the N+1 claim against a real
// database as well.
type dummyModule struct {
	entity string
	table  string
	pool   *pgxpool.Pool

	listCalls  atomic.Int64
	fetchCalls atomic.Int64
}

var _ query.Provider = (*dummyModule)(nil)

// newModule creates the table for the given entity from scratch and returns the module.
func newModule(t *testing.T, entity, kolonlar string) *dummyModule {
	t.Helper()

	m := &dummyModule{entity: entity, table: "dummy_" + entity, pool: testPool.Pool()}

	_, err := m.pool.Exec(t.Context(), "DROP TABLE IF EXISTS "+m.table)
	require.NoError(t, err)
	_, err = m.pool.Exec(t.Context(),
		fmt.Sprintf("CREATE TABLE %s (id TEXT PRIMARY KEY, %s)", m.table, kolonlar))
	require.NoError(t, err)

	return m
}

// insert writes a single record into the module's table.
func (m *dummyModule) insert(t *testing.T, kolonlar string, values ...any) {
	t.Helper()

	placeholders := make([]string, len(values))
	for i := range values {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}
	_, err := m.pool.Exec(t.Context(),
		fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", m.table, kolonlar, strings.Join(placeholders, ", ")),
		values...)
	require.NoError(t, err)
}

// Entity returns the entity name the module serves.
func (m *dummyModule) Entity() string { return m.entity }

// List returns the root records; it recognizes the "status" filter only.
func (m *dummyModule) List(ctx context.Context, opts query.ListOptions) ([]query.Record, error) {
	m.listCalls.Add(1)

	sql := "SELECT * FROM " + m.table
	args := make([]any, 0, 1)
	for field, value := range opts.Filters {
		if field != "status" {
			// ADR 0004: an unsupported field is refused by the provider.
			return nil, errors.Invalid("dummy_unknown_filter",
				"the %q provider does not support the %q filter", m.entity, field)
		}
		args = append(args, value)
		sql += fmt.Sprintf(" WHERE status = $%d", len(args))
	}
	sql += " ORDER BY id"
	if opts.Limit > 0 {
		sql += fmt.Sprintf(" LIMIT %d", opts.Limit)
	}
	if opts.Offset > 0 {
		sql += fmt.Sprintf(" OFFSET %d", opts.Offset)
	}

	return m.query(ctx, sql, opts.Fields, args...)
}

// FetchByIDs returns the records of the given ids in ONE query.
func (m *dummyModule) FetchByIDs(ctx context.Context, ids, fields []string) ([]query.Record, error) {
	m.fetchCalls.Add(1)

	return m.query(ctx, "SELECT * FROM "+m.table+" WHERE id = ANY($1)", fields, ids)
}

// query runs the query and turns the rows into Records, applying the field selection.
func (m *dummyModule) query(ctx context.Context, sql string, fields []string, args ...any) ([]query.Record, error) {
	rows, err := m.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindUnavailable, "dummy_query_failed",
			"the %q provider could not be queried", m.entity)
	}

	collected, err := pgx.CollectRows(rows, pgx.RowToMap)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindUnavailable, "dummy_scan_failed",
			"the rows of the %q provider could not be read", m.entity)
	}

	out := make([]query.Record, 0, len(collected))
	for _, row := range collected {
		rec := make(query.Record, len(row))
		for field, value := range row {
			if len(fields) > 0 && !slices.Contains(fields, field) {
				continue
			}
			rec[field] = value
		}
		out = append(out, rec)
	}
	return out, nil
}

// --- testler ----------------------------------------------------------------

func TestGraphEndToEndWithTwoDummyModules(t *testing.T) {
	ctx := t.Context()

	items, prices, links := setUp(t)

	c := container.New(nil)
	require.NoError(t, c.Provide(items.Entity()+query.ProviderSuffix, items))
	require.NoError(t, c.Provide(prices.Entity()+query.ProviderSuffix, prices))

	q := query.New(links, c, nil)

	got, err := q.Graph(ctx, query.GraphSpec{
		Entity:  "shop_item",
		Fields:  []string{"title"},
		Filters: map[string]any{"status": "published"},
		Expand: []query.Expansion{
			{Link: "item_price", As: "fiyatlar", Fields: []string{"amount", "currency"}},
			{Link: "item_main_price", As: "ana_fiyat"},
		},
	})
	require.NoError(t, err)
	require.Len(t, got, 2, "only the published records may come back")

	// The root: the selected field plus the id added for the join.
	assert.Equal(t, "item_1", got[0]["id"])
	assert.Equal(t, "T-shirt", got[0]["title"])
	assert.NotContains(t, got[0], "status", "an unselected field must not come back")

	// OneToMany: a slice, in the link's order.
	fiyatlar, ok := got[0]["fiyatlar"].([]query.Record)
	require.Truef(t, ok, "OneToMany has to write a slice; the type that arrived: %T", got[0]["prices"])
	require.Len(t, fiyatlar, 2)
	assert.Equal(t, "price_1", fiyatlar[0]["id"])
	assert.Equal(t, int64(1990), fiyatlar[0]["amount"])
	assert.Equal(t, "TRY", fiyatlar[0]["currency"])
	assert.Equal(t, "price_2", fiyatlar[1]["id"])

	// OneToOne: a single record, nil on a root with no link.
	ana, ok := got[0]["ana_fiyat"].(query.Record)
	require.Truef(t, ok, "OneToOne has to write a single record; the type that arrived: %T", got[0]["main_price"])
	assert.Equal(t, "price_1", ana["id"])

	ikinci, ok := got[1]["fiyatlar"].([]query.Record)
	require.True(t, ok)
	require.Len(t, ikinci, 1)
	assert.Equal(t, "price_3", ikinci[0]["id"])
	assert.Nil(t, got[1]["main_price"], "a single-ended expansion has to be nil on a root with no link")

	// No N+1: one query per expansion, one List for the root.
	assert.Equal(t, int64(1), items.listCalls.Load())
	assert.Zero(t, items.fetchCalls.Load())
	assert.Equal(t, int64(2), prices.fetchCalls.Load(),
		"two expansions, two batch queries; there must be no query per record")
}

// TestGraphResolvesTheReverseDirectionWithTheRealLinkService verifies that the
// expansion works while the root entity sits at the TO end of the link.
//
// Regression: when the link and query packages were written by separate agents,
// LinkService offered the From->To direction only while Query looked for a
// surface that was not on the concrete type (ListManyByTo). The unit tests
// written against the fake service passed while every reverse expansion failed
// against the REAL one. ListManyByTo is part of the LinkService contract now.
func TestGraphResolvesTheReverseDirectionWithTheRealLinkService(t *testing.T) {
	items, prices, links := setUp(t)

	c := container.New(nil)
	require.NoError(t, c.Provide(items.Entity()+query.ProviderSuffix, items))
	require.NoError(t, c.Provide(prices.Entity()+query.ProviderSuffix, prices))

	q := query.New(links, c, nil)

	// The root entity sits at the link's TO end: it resolves from price to product.
	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "shop_price",
		Expand: []query.Expansion{{Link: "item_price", As: "product"}},
	})
	require.NoError(t, err)
	require.NotEmpty(t, got, "a reverse expansion has to return results")

	// At least one price's product has to have come back.
	var linked query.Record
	for _, rec := range got {
		if product, ok := rec["product"]; ok && product != nil {
			linked = rec
			break
		}
	}
	require.NotNil(t, linked, "at least one linked price record was expected")

	urunler, ok := linked["product"].([]query.Record)
	if ok {
		require.NotEmpty(t, urunler)
		assert.Contains(t, urunler[0], "id")
	} else {
		product, single := linked["product"].(query.Record)
		require.True(t, single, "an expansion has to be a Record or []Record, got: %T", linked["product"])
		assert.Contains(t, product, "id")
	}

	// No N+1: the reverse direction is one batch too.
	assert.Equal(t, int64(1), items.fetchCalls.Load(),
		"the reverse direction has to make one batch call as well, not one per record")
}

func TestGraphReturnsNotFoundForAnUnknownLinkWithTheRealService(t *testing.T) {
	items, prices, links := setUp(t)

	c := container.New(nil)
	require.NoError(t, c.Provide(items.Entity()+query.ProviderSuffix, items))
	require.NoError(t, c.Provide(prices.Entity()+query.ProviderSuffix, prices))

	q := query.New(links, c, nil)

	_, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "shop_item",
		Expand: []query.Expansion{{Link: "tanimsiz_link"}},
	})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "the expected class is NotFound, got: %v", err)
	assert.Contains(t, err.Error(), "tanimsiz_link")
}

func TestGraphReturnsNotFoundForAnUnregisteredProvider(t *testing.T) {
	items, _, links := setUp(t)

	// shop_price.query bilerek kaydedilmiyor.
	c := container.New(nil)
	require.NoError(t, c.Provide(items.Entity()+query.ProviderSuffix, items))

	q := query.New(links, c, nil)

	_, err := q.Graph(t.Context(), query.GraphSpec{
		Entity: "shop_item",
		Expand: []query.Expansion{{Link: "item_price"}},
	})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "the expected class is NotFound, got: %v", err)

	var typed *errors.Error
	require.True(t, errors.As(err, &typed))
	assert.Equal(t, "shop_price"+query.ProviderSuffix, typed.Details["looked_up_name"])
}

func TestGraphAProviderErrorDropsTheWholeCall(t *testing.T) {
	items, prices, links := setUp(t)

	c := container.New(nil)
	require.NoError(t, c.Provide(items.Entity()+query.ProviderSuffix, items))
	require.NoError(t, c.Provide(prices.Entity()+query.ProviderSuffix, prices))

	q := query.New(links, c, nil)

	// The provider refuses a filter it does not know (ADR 0004); Query must not swallow it.
	got, err := q.Graph(t.Context(), query.GraphSpec{
		Entity:  "shop_item",
		Filters: map[string]any{"bilinmeyen_alan": "x"},
		Expand:  []query.Expansion{{Link: "item_price"}},
	})
	require.Error(t, err)
	assert.Nil(t, got)
	assert.True(t, errors.IsInvalid(err), "the provider's error class has to be preserved, got: %v", err)
	assert.Zero(t, prices.fetchCalls.Load(), "with the root failing the expansion must not be reached")
}

// --- helpers ----------------------------------------------------------------

// setUp prepares the two dummy modules, their data and the real link service.
//
// The module tables are built from scratch on every call; the link tables are
// emptied after Define. That keeps the tests independent of each other and of
// earlier runs.
func setUp(t *testing.T) (items, prices *dummyModule, links link.LinkService) {
	t.Helper()
	ctx := t.Context()

	items = newModule(t, "shop_item", "title TEXT NOT NULL, status TEXT NOT NULL")
	prices = newModule(t, "shop_price", "amount BIGINT NOT NULL, currency TEXT NOT NULL")

	items.insert(t, "id, title, status", "item_1", "T-shirt", "published")
	items.insert(t, "id, title, status", "item_2", "Hat", "published")
	items.insert(t, "id, title, status", "item_3", "Taslak", "draft")

	prices.insert(t, "id, amount, currency", "price_1", int64(1990), "TRY")
	prices.insert(t, "id, amount, currency", "price_2", int64(2490), "TRY")
	prices.insert(t, "id, amount, currency", "price_3", int64(3990), "TRY")

	links = link.New(testPool, nil)
	require.NoError(t, links.Define(ctx, itemPrice))
	require.NoError(t, links.Define(ctx, itemMainPrice))
	clearLinks(t, itemPrice.Name, itemMainPrice.Name)

	require.NoError(t, links.Create(ctx, itemPrice.Name, "item_1", "price_1"))
	require.NoError(t, links.Create(ctx, itemPrice.Name, "item_1", "price_2"))
	require.NoError(t, links.Create(ctx, itemPrice.Name, "item_2", "price_3"))
	require.NoError(t, links.Create(ctx, itemMainPrice.Name, "item_1", "price_1"))

	return items, prices, links
}

// clearLinks empties the given link tables.
func clearLinks(t *testing.T, names ...string) {
	t.Helper()

	for _, name := range names {
		table, err := link.TableName(name)
		require.NoError(t, err)

		_, err = testPool.Pool().Exec(t.Context(), "TRUNCATE TABLE "+table)
		require.NoError(t, err)
	}
}
