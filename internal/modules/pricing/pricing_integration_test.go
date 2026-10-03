//go:build integration

// The tests in this file need a real PostgreSQL instance (and therefore
// Docker); they are separated with the `integration` tag so that `make test`
// stays fast. To run them: make test-integration
//
// The claims proven here CANNOT BE PROVEN with a unit test: that the migration
// can be rolled back, that SetPrices really runs in a single transaction, that
// the database CHECK constraints close the service validation a second time,
// and how the query provider batches with real queries.
package pricing_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/pricing"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
	"github.com/bdrtr/gobit/internal/modules/pricing/repository"
	"github.com/bdrtr/gobit/internal/modules/pricing/service"
	"github.com/bdrtr/gobit/internal/testdb"
)

const postgresImage = "postgres:16-alpine"

var (
	// testPool is the pool every test shares; the schema is set up in TestMain.
	testPool *db.Pool
	// testDSN is the pool's connection address; the migration tests need it.
	testDSN string
)

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres brings up a single Postgres container and runs every test on
// it. It is a separate function because os.Exit skips defers.
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
		fmt.Fprintf(os.Stderr, "could not get the connection address: %v\n", err)
		return 1
	}

	testPool, err = db.New(ctx, db.DefaultConfig(testDSN), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not open the connection pool: %v\n", err)
		return 1
	}
	defer testPool.Close()

	if err := db.Migrate(ctx, testDSN, pricing.New(nil).Migrations(), pricing.Name); err != nil {
		fmt.Fprintf(os.Stderr, "could not set up the pricing schema: %v\n", err)
		return 1
	}

	return m.Run()
}

// newService builds a service that runs on the real repository.
func newService(t *testing.T) *service.Service {
	t.Helper()
	return service.New(repository.New(testPool.Pool()), service.Options{})
}

// tableExists reports whether a table exists.
func tableExists(ctx context.Context, t *testing.T, dsn, table string) bool {
	t.Helper()

	pool, err := db.New(ctx, db.DefaultConfig(dsn), nil)
	require.NoError(t, err)
	defer pool.Close()

	var regclass *string
	require.NoError(t, pool.Pool().QueryRow(ctx,
		"SELECT to_regclass($1)::text", "public."+table).Scan(&regclass))
	return regclass != nil
}

// TestMigrationsAreReversible proves the schema can be applied and ROLLED BACK
// (plan Section 8: up/down pairs have to be reversible).
//
// It runs in a database of its own: dropping the shared schema would make the
// other tests depend on the order they ran in.
func TestMigrationsAreReversible(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.New(t, testDSN, "pricing_migration")

	src := pricing.New(nil).Migrations()
	tables := []string{
		"price_set", "price", "price_list", "price_rule",
		"price_set_history", "price_list_history",
	}

	require.NoError(t, db.Migrate(ctx, dsn, src, pricing.Name))
	for _, table := range tables {
		assert.True(t, tableExists(ctx, t, dsn, table), "the %s table has to be created", table)
	}

	require.NoError(t, db.MigrateDown(ctx, dsn, src, pricing.Name, 0))
	for _, table := range tables {
		assert.False(t, tableExists(ctx, t, dsn, table), "the %s table has to be rolled back", table)
	}

	// Reapplicability: up after down has to run cleanly.
	require.NoError(t, db.Migrate(ctx, dsn, src, pricing.Name))
	assert.True(t, tableExists(ctx, t, dsn, "price"))
}

// TestPriceSetLifecycle proves the end-to-end CRUD flow on the real database.
func TestPriceSetLifecycle(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{
		{CurrencyCode: "try", Amount: 19900},
		{CurrencyCode: "usd", Amount: 599, MinQuantity: 10, MaxQuantity: ptr(int32(20))},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, set.ID)
	assert.False(t, set.CreatedAt.IsZero())
	assert.Equal(t, time.UTC, set.CreatedAt.Location(), "times have to come back in UTC")

	fetched, err := svc.GetPriceSet(ctx, set.ID)
	require.NoError(t, err)
	assert.Equal(t, set.ID, fetched.ID)

	prices, err := svc.ListPrices(ctx, set.ID)
	require.NoError(t, err)
	require.Len(t, prices, 2)
	for _, price := range prices {
		assert.Equal(t, price.CurrencyCode, upper(price.CurrencyCode),
			"the currency has to be stored in UPPER case")
	}

	require.NoError(t, svc.DeletePriceSet(ctx, set.ID))

	_, err = svc.GetPriceSet(ctx, set.ID)
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))

	// Soft delete: the row stays, it is only hidden from reads.
	var deletedAt *time.Time
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		"SELECT deleted_at FROM price_set WHERE id = $1", set.ID).Scan(&deletedAt))
	assert.NotNil(t, deletedAt, "the delete has to be SOFT; the row has to stay")

	var livePrices int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		"SELECT count(*) FROM price WHERE price_set_id = $1 AND deleted_at IS NULL",
		set.ID).Scan(&livePrices))
	assert.Zero(t, livePrices, "when the container is deleted its prices have to be hidden too")
}

// TestSetPricesIsAtomic proves that the bulk write really runs in a SINGLE
// TRANSACTION.
//
// Scenario: the second price is bound to a price list that does not exist, and
// the database rejects it with a foreign key. Without a transaction, the
// deletion of the old prices would ALREADY have been written and the container
// would be left without prices. The test verifies that the old prices stay in
// place.
//
// After ADR 0047 the load this test carries grew: deletion is no longer a stamp
// but the removal of the row itself, so had the rollback failed, the old prices
// would not be hidden but GONE.
func TestSetPricesIsAtomic(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{
		{CurrencyCode: "TRY", Amount: 100},
		{CurrencyCode: "USD", Amount: 200},
	})
	require.NoError(t, err)

	before, err := svc.ListPrices(ctx, set.ID)
	require.NoError(t, err)
	require.Len(t, before, 2)

	_, err = svc.SetPrices(ctx, set.ID, []service.PriceInput{
		{CurrencyCode: "EUR", Amount: 300},
		{CurrencyCode: "GBP", Amount: 400, PriceListID: ptr("plist_MISSING")},
	})
	require.Error(t, err, "a price bound to a missing price list has to be rejected")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	after, err := svc.ListPrices(ctx, set.ID)
	require.NoError(t, err)
	require.Len(t, after, 2, "a failed bulk write has to KEEP the old prices")

	currencies := map[string]bool{}
	for _, price := range after {
		currencies[price.CurrencyCode] = true
	}
	assert.True(t, currencies["TRY"] && currencies["USD"])
	assert.False(t, currencies["EUR"], "because the transaction was rolled back, the new price must not be written")
}

// TestSetPricesReplacesAndKeepsRules proves that a successful bulk write
// replaces the old prices and writes the rules along with them.
func TestSetPricesReplacesAndKeepsRules(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{{CurrencyCode: "TRY", Amount: 100}})
	require.NoError(t, err)

	written, err := svc.SetPrices(ctx, set.ID, []service.PriceInput{{
		CurrencyCode: "TRY",
		Amount:       9000,
		Rules: []service.RuleInput{
			{Attribute: "region_id", Operator: models.OpEq, Values: []string{"reg_1"}},
			{Attribute: "customer_group_id", Operator: models.OpIn, Values: []string{"vip", "b2b"}},
		},
	}})
	require.NoError(t, err)
	require.Len(t, written, 1)
	require.Len(t, written[0].Rules, 2)

	reread, err := svc.ListPrices(ctx, set.ID)
	require.NoError(t, err)
	require.Len(t, reread, 1, "the replacement has to delete the old price")
	assert.Equal(t, int64(9000), reread[0].Amount)
	require.Len(t, reread[0].Rules, 2, "the rules have to be read together with the price")

	values := map[string][]string{}
	for _, rule := range reread[0].Rules {
		values[rule.Attribute] = rule.Values
	}
	assert.Equal(t, []string{"reg_1"}, values["region_id"])
	assert.Equal(t, []string{"vip", "b2b"}, values["customer_group_id"])
}

// TestDatabaseRejectsInvalidMoney proves that the database CHECK constraints are
// a second gate INDEPENDENT of the service validation.
//
// Were the validation only in the application, a maintenance script running
// SQL directly could write a negative price.
func TestDatabaseRejectsInvalidMoney(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	set, err := svc.CreatePriceSet(ctx, nil)
	require.NoError(t, err)

	for _, tc := range []struct {
		name     string
		currency string
		amount   int64
		minQty   int32
		maxQty   any
	}{
		{"negative amount", "TRY", -1, 1, nil},
		{"excessively large amount", "TRY", models.MaxAmount + 1, 1, nil},
		{"lower-case currency", "try", 100, 1, nil},
		{"two-letter currency", "TR", 100, 1, nil},
		{"zero minimum quantity", "TRY", 100, 0, nil},
		{"maximum quantity below the minimum", "TRY", 100, 10, int32(5)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := testPool.Pool().Exec(ctx, `
				INSERT INTO price (id, price_set_id, currency_code, amount, min_quantity, max_quantity, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, now(), now())`,
				models.NewPriceID(time.Now()), set.ID, tc.currency, tc.amount, tc.minQty, tc.maxQty)
			require.Error(t, err, "the database has to reject this row")
		})
	}
}

// TestCalculatePriceWithPriceList proves list precedence over the real
// database: a published campaign overrides the base price, a draft does not.
func TestCalculatePriceWithPriceList(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	list, err := svc.CreatePriceList(ctx, service.PriceListInput{
		Title:  "Summer campaign",
		Type:   models.PriceListSale,
		Status: models.PriceListDraft,
	})
	require.NoError(t, err)

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{
		{CurrencyCode: "TRY", Amount: 10000},
		{CurrencyCode: "TRY", Amount: 7500, PriceListID: &list.ID},
	})
	require.NoError(t, err)

	calculated, err := svc.CalculatePrice(ctx, set.ID, service.CalculateParams{CurrencyCode: "TRY"})
	require.NoError(t, err)
	assert.Equal(t, int64(10000), calculated.Amount, "a draft campaign must not offer a price")

	_, err = svc.UpdatePriceList(ctx, list.ID, service.PriceListInput{
		Title:  list.Title,
		Type:   models.PriceListSale,
		Status: models.PriceListActive,
	})
	require.NoError(t, err)

	calculated, err = svc.CalculatePrice(ctx, set.ID, service.CalculateParams{CurrencyCode: "TRY"})
	require.NoError(t, err)
	assert.Equal(t, int64(7500), calculated.Amount, "a published campaign has to override the base price")
	require.NotNil(t, calculated.PriceListID)
	assert.Equal(t, list.ID, *calculated.PriceListID)
	assert.Equal(t, models.PriceListSale, calculated.PriceListType)
}

// TestCalculatePriceSkipsDeletedPriceList proves that a price whose list was
// deleted is eliminated in the real query too; the LEFT JOIN's deleted_at
// condition rests on this.
func TestCalculatePriceSkipsDeletedPriceList(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	list, err := svc.CreatePriceList(ctx, service.PriceListInput{
		Title:  "Campaign to be deleted",
		Type:   models.PriceListOverride,
		Status: models.PriceListActive,
	})
	require.NoError(t, err)

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{
		{CurrencyCode: "TRY", Amount: 10000},
		{CurrencyCode: "TRY", Amount: 1, PriceListID: &list.ID},
	})
	require.NoError(t, err)

	calculated, err := svc.CalculatePrice(ctx, set.ID, service.CalculateParams{CurrencyCode: "TRY"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), calculated.Amount)

	require.NoError(t, svc.DeletePriceList(ctx, list.ID))

	calculated, err = svc.CalculatePrice(ctx, set.ID, service.CalculateParams{CurrencyCode: "TRY"})
	require.NoError(t, err)
	assert.Equal(t, int64(10000), calculated.Amount, "a price whose list was deleted must not be counted")
}

// TestCalculatePriceUsesRules proves that the rules are applied on a real
// database round trip too.
func TestCalculatePriceUsesRules(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{
		{CurrencyCode: "TRY", Amount: 10000},
		{
			CurrencyCode: "TRY",
			Amount:       6000,
			Rules: []service.RuleInput{
				{Attribute: "region_id", Operator: models.OpEq, Values: []string{"reg_tr"}},
			},
		},
	})
	require.NoError(t, err)

	withoutContext, err := svc.CalculatePrice(ctx, set.ID, service.CalculateParams{CurrencyCode: "TRY"})
	require.NoError(t, err)
	assert.Equal(t, int64(10000), withoutContext.Amount, "without a context the ruled price has to be eliminated")
	assert.Zero(t, withoutContext.MatchedRules)

	withContext, err := svc.CalculatePrice(ctx, set.ID, service.CalculateParams{
		CurrencyCode: "TRY",
		Attributes:   map[string]string{"region_id": "reg_tr"},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(6000), withContext.Amount, "when the rule matches, the specific price has to win")
	assert.Equal(t, 1, withContext.MatchedRules)
}

// TestQueryProviderBatchesRealQueries proves that the provider batches with real
// queries and carries the prices (ADR 0004).
func TestQueryProviderBatchesRealQueries(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	provider := service.NewQueryProvider(svc)

	ids := make([]string, 0, 3)
	for i := range 3 {
		set, err := svc.CreatePriceSet(ctx, []service.PriceInput{
			{CurrencyCode: "TRY", Amount: int64(1000 * (i + 1))},
		})
		require.NoError(t, err)
		ids = append(ids, set.ID)
	}
	// An id that is not found is not an error; no record is returned.
	ids = append(ids, "pset_MISSING")

	records, err := provider.FetchByIDs(ctx, ids, nil)
	require.NoError(t, err)
	require.Len(t, records, 3, "a missing id has to be skipped silently")

	byID := map[string]query.Record{}
	for _, record := range records {
		id, ok := record[query.IDField].(string)
		require.True(t, ok, "the record id has to be a string")
		byID[id] = record
	}

	for i, id := range ids[:3] {
		record, ok := byID[id]
		require.True(t, ok, "the %s record has to be returned", id)

		prices, ok := record["prices"].([]map[string]any)
		require.True(t, ok, "the prices have to come with the record")
		require.Len(t, prices, 1)
		assert.Equal(t, int64(1000*(i+1)), prices[0]["amount"])
		assert.Equal(t, "TRY", prices[0]["currency_code"])
	}
}

// TestQueryProviderListFiltersByID proves that the provider applies the id
// filter with a real query.
func TestQueryProviderListFiltersByID(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	provider := service.NewQueryProvider(svc)

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{{CurrencyCode: "TRY", Amount: 4242}})
	require.NoError(t, err)

	records, err := provider.List(ctx, query.ListOptions{
		Filters: map[string]any{"id": set.ID},
		Fields:  []string{"id", "prices"},
	})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, set.ID, records[0][query.IDField])

	prices, ok := records[0]["prices"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, prices, 1)
	assert.Equal(t, int64(4242), prices[0]["amount"])
}

// TestPriceRuleCascadesWithPrice proves that rules are bound to the price with a
// foreign key inside the same module: if the price row goes, the rule goes too.
//
// An FK WITHIN a module is allowed and should be used; what is forbidden is a
// cross-module FK (Principle 2.2).
func TestPriceRuleCascadesWithPrice(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{{
		CurrencyCode: "TRY",
		Amount:       100,
		Rules: []service.RuleInput{
			{Attribute: "region_id", Operator: models.OpEq, Values: []string{"reg_1"}},
		},
	}})
	require.NoError(t, err)

	prices, err := svc.ListPrices(ctx, set.ID)
	require.NoError(t, err)
	require.Len(t, prices, 1)
	priceID := prices[0].ID

	var ruleCount int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		"SELECT count(*) FROM price_rule WHERE price_id = $1", priceID).Scan(&ruleCount))
	require.Equal(t, 1, ruleCount)

	_, err = testPool.Pool().Exec(ctx, "DELETE FROM price WHERE id = $1", priceID)
	require.NoError(t, err)

	require.NoError(t, testPool.Pool().QueryRow(ctx,
		"SELECT count(*) FROM price_rule WHERE price_id = $1", priceID).Scan(&ruleCount))
	assert.Zero(t, ruleCount, "when the price goes, its rules have to go too")
}

// TestNoCrossModuleForeignKeys proves that pricing's tables reference ONLY its
// own tables (Principle 2.2).
func TestNoCrossModuleForeignKeys(t *testing.T) {
	ctx := context.Background()

	rows, err := testPool.Pool().Query(ctx, `
		SELECT tc.table_name, ccu.table_name AS referenced
		FROM information_schema.table_constraints tc
		JOIN information_schema.constraint_column_usage ccu
		  ON ccu.constraint_name = tc.constraint_name
		WHERE tc.constraint_type = 'FOREIGN KEY'
		  AND tc.table_schema = 'public'
		  AND tc.table_name IN ('price_set', 'price', 'price_list', 'price_rule')`)
	require.NoError(t, err)
	defer rows.Close()

	owned := map[string]bool{"price_set": true, "price": true, "price_list": true, "price_rule": true}
	found := 0
	for rows.Next() {
		var table, referenced string
		require.NoError(t, rows.Scan(&table, &referenced))
		assert.True(t, owned[referenced],
			"the %s table references %s; a cross-module FK is forbidden", table, referenced)
		found++
	}
	require.NoError(t, rows.Err())
	assert.Positive(t, found, "the in-module FKs have to be set up")
}

// ptr returns the address of a value.
func ptr[T any](v T) *T { return &v }

// upper converts a string to ASCII upper case; it is for the currency check in
// the tests.
func upper(s string) string {
	out := []rune(s)
	for i, r := range out {
		if r >= 'a' && r <= 'z' {
			out[i] = r - ('a' - 'A')
		}
	}
	return string(out)
}

// TestDatabaseRejectsValuelessRule proves that the rule values constraint REALLY
// closes (migration 000002).
//
// The CHECK (array_length(rule_values, 1) >= 1) in 000001 let the empty array
// through: array_length('{}', 1) returns NULL, and a CHECK whose result is NULL
// counts as satisfied. A rule without values means a price whose condition
// cannot be read; that is why the gate has to stand at the data level too.
func TestDatabaseRejectsValuelessRule(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{{CurrencyCode: "TRY", Amount: 100}})
	require.NoError(t, err)

	prices, err := svc.ListPrices(ctx, set.ID)
	require.NoError(t, err)
	require.Len(t, prices, 1)

	_, err = testPool.Pool().Exec(ctx, `
		INSERT INTO price_rule (id, price_id, attribute, operator, rule_values, created_at, updated_at)
		VALUES ($1, $2, 'region_id', 'eq', '{}', now(), now())`,
		models.NewPriceRuleID(time.Now()), prices[0].ID)
	require.Error(t, err, "the database has to reject a valueless rule")

	// The same row with a single value has to be ACCEPTED; the constraint does
	// not reject everything.
	_, err = testPool.Pool().Exec(ctx, `
		INSERT INTO price_rule (id, price_id, attribute, operator, rule_values, created_at, updated_at)
		VALUES ($1, $2, 'region_id', 'eq', '{reg_1}', now(), now())`,
		models.NewPriceRuleID(time.Now()), prices[0].ID)
	require.NoError(t, err)
}

// TestCreatePriceSetIsAtomic proves that the container and its prices are
// written in ONE transaction.
//
// Scenario: one of the prices is bound to a price list that does not exist, and
// the database rejects it with a foreign key. With two separate transactions
// the container would ALREADY have been committed, and even though the caller
// got an error, a container without prices, bound to nothing, would be left
// behind. The service validation cannot catch this branch: the id's format is
// valid, only the record is missing.
func TestCreatePriceSetIsAtomic(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	countSets := func() int64 {
		var count int64
		require.NoError(t, testPool.Pool().QueryRow(ctx, "SELECT count(*) FROM price_set").Scan(&count))
		return count
	}

	before := countSets()

	_, err := svc.CreatePriceSet(ctx, []service.PriceInput{
		{CurrencyCode: "TRY", Amount: 100},
		{CurrencyCode: "USD", Amount: 200, PriceListID: ptr("plist_MISSING")},
	})
	require.Error(t, err, "a price bound to a missing price list has to be rejected")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	assert.Equal(t, before, countSets(), "a rejected write must NOT LEAVE an orphan container behind")
}

// TestConcurrentSetPricesDoesNotMerge proves that two concurrent writes do not
// break the "replace" semantics.
//
// The scenario runs exactly the same SQL sequence as ReplacePrices in two
// transactions. Were the container's existence check WITHOUT A LOCK, the second
// transaction's "delete the old prices" step, under READ COMMITTED, could not
// see the first one's NEW rows in its own statement snapshot and would not
// delete them; the prices of both writes would stay live together in the
// container and both callers would return without an error. That is exactly
// how a wrong price is born.
//
// The hand-run step was turned into a DELETE along with ADR 0047. Leaving the
// imitation as a stamp would have kept the test green but emptied its claim:
// what is proven here is that the lock serializes the real write path, and if
// the imitation departs from the real path it no longer proves that.
func TestConcurrentSetPricesDoesNotMerge(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{{CurrencyCode: "TRY", Amount: 100}})
	require.NoError(t, err)

	// The first writer: runs ReplacePrices' steps by hand and stays OPEN.
	first, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = first.Rollback(ctx) }()

	var lockedID string
	require.NoError(t, first.QueryRow(ctx,
		"SELECT id FROM price_set WHERE id = $1 AND deleted_at IS NULL FOR UPDATE",
		set.ID).Scan(&lockedID))

	_, err = first.Exec(ctx,
		"DELETE FROM price WHERE price_set_id = $1 AND deleted_at IS NULL",
		set.ID)
	require.NoError(t, err)
	_, err = first.Exec(ctx, `
		INSERT INTO price (id, price_set_id, currency_code, amount, min_quantity, created_at, updated_at)
		VALUES ($1, $2, 'USD', 500, 1, now(), now())`,
		models.NewPriceID(time.Now()), set.ID)
	require.NoError(t, err)

	// The second writer: the real write path. It has to start waiting while the
	// first one is open.
	done := make(chan error, 1)
	go func() {
		_, setErr := svc.SetPrices(ctx, set.ID, []service.PriceInput{{CurrencyCode: "EUR", Amount: 700}})
		done <- setErr
	}()
	requireLockWait(ctx, t, done)

	require.NoError(t, first.Commit(ctx))
	require.NoError(t, <-done, "once the first writer finishes, the second has to complete")

	prices, err := svc.ListPrices(ctx, set.ID)
	require.NoError(t, err)

	currencies := make([]string, 0, len(prices))
	for _, price := range prices {
		currencies = append(currencies, price.CurrencyCode)
	}
	assert.Equal(t, []string{"EUR"}, currencies,
		"the second write has to delete the first one's price too; the replacement must not turn into a MERGE")
}

// requireLockWait verifies that the second writer really is waiting on a lock.
//
// Instead of a fixed wait, the database is asked: it is polled until a waiting
// backend shows up, and if the transaction completes in the meantime the test
// fails at once. That way it rests on the observed state, not on timing.
func requireLockWait(ctx context.Context, t *testing.T, done <-chan error) {
	t.Helper()

	deadline := time.Now().Add(15 * time.Second)
	for {
		select {
		case err := <-done:
			t.Fatalf("the second write completed without waiting on the lock: %v", err)
		default:
		}

		var waiting int
		require.NoError(t, testPool.Pool().QueryRow(ctx, `
			SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting))
		if waiting > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the second write never started waiting on the lock")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestQueryProviderHidesUnpublishedListPrices proves that the read surface does
// not leak prices the calculation counts as INVALID.
//
// The provider carries no calculation context; if it returns a price that is
// conditional on a context it does not carry, the consumer (product's store
// listing) cannot eliminate it and the storefront shows an unpublished
// campaign. The second half of the test proves the filter is not EXCESSIVE
// either: once the list is published, the price shows.
func TestQueryProviderHidesUnpublishedListPrices(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	provider := service.NewQueryProvider(svc)

	list, err := svc.CreatePriceList(ctx, service.PriceListInput{
		Title:  "Unpublished campaign",
		Type:   models.PriceListSale,
		Status: models.PriceListDraft,
	})
	require.NoError(t, err)

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{
		{CurrencyCode: "TRY", Amount: 10000},
		{CurrencyCode: "TRY", Amount: 1, PriceListID: &list.ID},
	})
	require.NoError(t, err)

	// The calculation already eliminates the draft campaign; the read surface has
	// to eliminate it too.
	calculated, err := svc.CalculatePrice(ctx, set.ID, service.CalculateParams{CurrencyCode: "TRY"})
	require.NoError(t, err)
	require.Equal(t, int64(10000), calculated.Amount)

	amounts := providerAmounts(ctx, t, provider, set.ID)
	assert.Equal(t, []int64{10000}, amounts,
		"an unpublished campaign's price must NOT LEAK to the read surface")

	_, err = svc.UpdatePriceList(ctx, list.ID, service.PriceListInput{
		Title:  list.Title,
		Type:   models.PriceListSale,
		Status: models.PriceListActive,
	})
	require.NoError(t, err)

	amounts = providerAmounts(ctx, t, provider, set.ID)
	assert.ElementsMatch(t, []int64{10000, 1}, amounts,
		"a published campaign's price has to show")
}

// providerAmounts returns the price amounts of a container as seen through the
// provider.
func providerAmounts(
	ctx context.Context,
	t *testing.T,
	provider *service.QueryProvider,
	setID string,
) []int64 {
	t.Helper()

	records, err := provider.FetchByIDs(ctx, []string{setID}, nil)
	require.NoError(t, err)
	require.Len(t, records, 1)

	prices, ok := records[0]["prices"].([]map[string]any)
	require.True(t, ok, "the prices have to come with the record")

	amounts := make([]int64, 0, len(prices))
	for _, price := range prices {
		amount, isInt := price["amount"].(int64)
		require.True(t, isInt, "the amount has to be an integer in minor units")
		amounts = append(amounts, amount)
	}
	return amounts
}

// TestStorePricesHideDraftListsAndRules proves that the customer surface does
// NOT LEAK unpublished campaign prices or rule conditions.
//
// Regression: while the Query provider applied the filter,
// GET /store/v1/price-sets/{id} used the unfiltered ListPrices path. The result:
// the price of a draft campaign and the condition of a rule bound to a customer
// segment (e.g. customer_group_id) went out in the customer body. The two
// customer surfaces now use the SAME filter.
func TestStorePricesHideDraftListsAndRules(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	draft, err := svc.CreatePriceList(ctx, service.PriceListInput{
		Title:  "Unpublished campaign",
		Type:   models.PriceListSale,
		Status: models.PriceListDraft,
	})
	require.NoError(t, err)

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{
		{CurrencyCode: "TRY", Amount: 10000},                     // base: has to show
		{CurrencyCode: "TRY", Amount: 1, PriceListID: &draft.ID}, // draft: must NOT SHOW
		{CurrencyCode: "TRY", Amount: 2, Rules: []service.RuleInput{ // rule-bound: must NOT SHOW
			{Attribute: "customer_group_id", Operator: models.OpEq, Values: []string{"vip"}},
		}},
	})
	require.NoError(t, err)

	// The admin surface sees EVERYTHING: the operator has to be able to see the
	// draft campaign and the rule.
	adminPrices, err := svc.ListPrices(ctx, set.ID)
	require.NoError(t, err)
	assert.Len(t, adminPrices, 3, "the admin surface has to see every price")

	// The customer surface sees ONLY the base price.
	storePrices, err := svc.ListStorePrices(ctx, set.ID)
	require.NoError(t, err)
	require.Len(t, storePrices, 1, "only a displayable price may go out to the customer: %+v", storePrices)
	assert.Equal(t, int64(10000), storePrices[0].Price.Amount)
	assert.Nil(t, storePrices[0].Price.PriceListID, "the draft campaign price leaked")
	assert.Empty(t, storePrices[0].Price.Rules, "rule conditions must not go out to the customer")

	// Once the campaign is published it has to SHOW on the customer surface —
	// the filter does not hide it permanently, it only eliminates what is not
	// published.
	_, err = svc.UpdatePriceList(ctx, draft.ID, service.PriceListInput{
		Title:  draft.Title,
		Type:   models.PriceListSale,
		Status: models.PriceListActive,
	})
	require.NoError(t, err)

	storePrices, err = svc.ListStorePrices(ctx, set.ID)
	require.NoError(t, err)
	assert.Len(t, storePrices, 2, "a published campaign has to be visible to the customer")
}

// countingTracer counts the queries the pool opens.
//
// The counter is the one direct proof of the claim "a batch read does not open
// a query per item": measuring time ties the test to the machine, counting
// queries does not.
type countingTracer struct {
	count atomic.Int64
}

// TraceQueryStart increments the counter at the start of every query.
func (c *countingTracer) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	_ pgx.TraceQueryStartData,
) context.Context {
	c.count.Add(1)
	return ctx
}

// TraceQueryEnd exists because the contract requires it; the counting is done
// at the start.
func (c *countingTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// newCountingService builds a service on a pool of ITS OWN that counts its
// queries.
//
// The pool has a single connection: in a pool with many connections, warm-up
// queries would mix into the count and the number would vary by machine.
func newCountingService(t *testing.T) (*service.Service, *countingTracer) {
	t.Helper()

	cfg, err := pgxpool.ParseConfig(testDSN)
	require.NoError(t, err)
	tracer := &countingTracer{}
	cfg.ConnConfig.Tracer = tracer
	cfg.MaxConns = 1

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return service.New(repository.New(pool), service.Options{}), tracer
}

// TestCalculateAmountsJSONMatchesPerSetOnRealData proves that, with REAL
// queries, the batch price path selects the SAME amount as the per-container
// path, and that it opens a CONSTANT number of queries regardless of the number
// of items.
//
// Neither claim can be proven with a unit test: the equality rests on two
// separate SQL statements returning the same candidate rows (one "= $1", the
// other "= ANY($1)"), and the number of queries can only be counted on a real
// pool.
//
// The whole cart calculation rests on this equality: a batch read that selects
// a different price charges the customer a different amount and no later check
// sees it — the totals are internally consistent in both cases.
func TestCalculateAmountsJSONMatchesPerSetOnRealData(t *testing.T) {
	ctx := context.Background()
	svc, tracer := newCountingService(t)

	list, err := svc.CreatePriceList(ctx, service.PriceListInput{
		Title:  "Batch read campaign",
		Type:   models.PriceListSale,
		Status: models.PriceListActive,
	})
	require.NoError(t, err)

	// The containers carry every branch of the selection rule: base price,
	// quantity tier, published campaign, region rule and another currency.
	setIDs := make([]string, 0, 8)
	for i := range 8 {
		inputs := []service.PriceInput{{CurrencyCode: "TRY", Amount: int64(1000 + i)}}
		switch i % 4 {
		case 1:
			inputs = append(inputs, service.PriceInput{
				CurrencyCode: "TRY", Amount: int64(800 + i), MinQuantity: 10, MaxQuantity: ptr(int32(20)),
			})
		case 2:
			inputs = append(inputs, service.PriceInput{
				CurrencyCode: "TRY", Amount: int64(9000 + i), PriceListID: &list.ID,
			})
		case 3:
			inputs = append(inputs,
				service.PriceInput{
					CurrencyCode: "TRY", Amount: int64(600 + i),
					Rules: []service.RuleInput{
						{Attribute: "region_id", Operator: models.OpEq, Values: []string{"reg_1"}},
					},
				},
				service.PriceInput{CurrencyCode: "USD", Amount: int64(50 + i)})
		}

		set, err := svc.CreatePriceSet(ctx, inputs)
		require.NoError(t, err)
		setIDs = append(setIDs, set.ID)
	}

	attrs := map[string]string{"region_id": "reg_1"}
	type lineItem struct {
		setID    string
		quantity int32
	}
	items := make([]lineItem, 0, len(setIDs)+2)
	for i, id := range setIDs {
		items = append(items, lineItem{id, int32(1 + i%15)})
	}
	// The same container at two different quantities, and a container with no
	// price, go into the request too.
	items = append(items, lineItem{setIDs[1], 12}, lineItem{"pset_MISSING", 1})

	request := map[string]any{
		"currency_code": "TRY",
		"attributes":    attrs,
		"items": func() []map[string]any {
			out := make([]map[string]any, 0, len(items))
			for _, item := range items {
				out = append(out, map[string]any{"price_set_id": item.setID, "quantity": item.quantity})
			}
			return out
		}(),
	}
	payload, err := json.Marshal(request)
	require.NoError(t, err)

	// Warm-up: the first run opens the connection and prepares the statements;
	// the counting starts after it.
	_, err = svc.CalculateAmountsJSON(ctx, payload)
	require.NoError(t, err)
	_, err = svc.CalculateAmount(ctx, setIDs[0], "TRY", 1, attrs)
	require.NoError(t, err)

	before := tracer.count.Load()
	raw, err := svc.CalculateAmountsJSON(ctx, payload)
	require.NoError(t, err)
	batchQueries := tracer.count.Load() - before

	var resp struct {
		Items []struct {
			Amount int64 `json:"amount"`
			Priced bool  `json:"priced"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(raw, &resp))
	require.Len(t, resp.Items, len(items))

	before = tracer.count.Load()
	for i, item := range items {
		amount, err := svc.CalculateAmount(ctx, item.setID, "TRY", item.quantity, attrs)
		if err != nil {
			require.True(t, errors.IsNotFound(err), "%s: %v", item.setID, err)
			assert.False(t, resp.Items[i].Priced, "%s shows as priced on the batch path", item.setID)
			continue
		}
		require.True(t, resp.Items[i].Priced, "%s shows as unpriced on the batch path", item.setID)
		assert.Equal(t, amount, resp.Items[i].Amount,
			"%s (quantity %d) was priced differently on the two paths", item.setID, item.quantity)
	}
	perSetQueries := tracer.count.Load() - before

	assert.Equal(t, int64(2), batchQueries,
		"the batch path has to open two queries regardless of the number of items (candidates + rules)")
	assert.Greater(t, perSetQueries, int64(2*len(items)-2),
		"the per-container path opens at least two queries per item; measured: %d", perSetQueries)
}

// TestARuleCanBeWrittenToADeletedPriceButIsUnreachable measures, together, that
// the foreign key does NOT CATCH a soft delete and what the consequence of that
// is.
//
// Although CreatePriceRule is a write path, it makes a SINGLE repository call:
// the caller supplies the price the rule is to be bound to, and the service
// reads nothing beforehand. So there is NO "read → decide → write" race here,
// and the shape of the defect in tax is found in no method of this module.
//
// Even so, the rule written can land under a deleted price: price_rule
// references price(id), but deletion is SOFT and leaves the row in place. The
// test proves this and RIGHT AFTER measures the consequence: because the price
// itself is already deleted, it does not enter the candidate query, so the rule
// cannot change any calculation. The measured consequence is an UNREACHABLE
// row — the amount the customer pays does not change.
//
// The test does not hold a defect; it holds the sentence in the
// repository.CreatePriceRule godoc: for a while that sentence said "a rule
// being orphaned is structurally impossible", and the measurement showed that
// to be wrong (2026-09-06).
func TestARuleCanBeWrittenToADeletedPriceButIsUnreachable(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{
		{CurrencyCode: "TRY", Amount: 10000},
	})
	require.NoError(t, err)

	prices, err := svc.ListPrices(ctx, set.ID)
	require.NoError(t, err)
	require.Len(t, prices, 1)
	priceID := prices[0].ID

	// When the container is deleted its prices are soft-deleted too; the rows
	// stay in place.
	require.NoError(t, svc.DeletePriceSet(ctx, set.ID))

	_, err = svc.CreatePriceRule(ctx, priceID, service.RuleInput{
		Attribute: "region_id",
		Operator:  models.OpEq,
		Values:    []string{"reg_1"},
	})
	require.NoError(t, err,
		"the foreign key does not catch a soft delete: a rule can be written under a deleted price")

	// CONSEQUENCE: the rule was written, but no calculation path can reach it,
	// because the price itself is eliminated from the candidate query.
	_, err = svc.CalculatePrice(ctx, set.ID, service.CalculateParams{
		CurrencyCode: "TRY",
		Attributes:   map[string]string{"region_id": "reg_1"},
	})
	require.Error(t, err, "the price of a deleted container must not enter the calculation")
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestAReplacedPriceIsDeletedAsARow proves that a replacement leaves no ROW
// behind (ADR 0047).
//
// The setup is the same as the one the decision measured: a single container
// is taken through four generations — 10000, 12000, 9000, 15000 — and every
// generation carries a rule. While the stamp was in force, the module's own
// read returned ONE row and an unfiltered count(*) on the same container
// returned FOUR.
//
// That the count is unfiltered is the heart of the test. Because every read of
// the module carries deleted_at IS NULL, a test that counted the way the module
// reads would have stayed green under the stamp too; that is exactly why the
// accumulation went unnoticed while there was an integration suite on these
// tables. That is why this test looks at the table itself, not at the read
// surface.
func TestAReplacedPriceIsDeletedAsARow(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	ruleFor := func(region string) []service.RuleInput {
		return []service.RuleInput{
			{Attribute: "region_id", Operator: models.OpEq, Values: []string{region}},
		}
	}

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{
		{CurrencyCode: "TRY", Amount: 10000, Rules: ruleFor("reg_1")},
	})
	require.NoError(t, err)

	first, err := svc.ListPrices(ctx, set.ID)
	require.NoError(t, err)
	require.Len(t, first, 1)
	priceIDs := []string{first[0].ID}

	for _, generation := range []struct {
		amount int64
		region string
	}{
		{12000, "reg_2"},
		{9000, "reg_3"},
		{15000, "reg_4"},
	} {
		written, err := svc.SetPrices(ctx, set.ID, []service.PriceInput{
			{CurrencyCode: "TRY", Amount: generation.amount, Rules: ruleFor(generation.region)},
		})
		require.NoError(t, err)
		require.Len(t, written, 1)
		priceIDs = append(priceIDs, written[0].ID)
	}

	// The generations' ids are distinct; the stamped rows did not form a thread.
	unique := map[string]bool{}
	for _, id := range priceIDs {
		unique[id] = true
	}
	require.Len(t, unique, 4, "every replacement produces a NEW id")

	var totalRows int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		"SELECT count(*) FROM price WHERE price_set_id = $1", set.ID).Scan(&totalRows))
	assert.Equal(t, 1, totalRows,
		"a replaced price must not be stamped and left behind, it has to be DELETED from the table")

	// The rules go not with a new statement but with the cascade on
	// price_rule.price_id; the stamp never triggered that cascade.
	var ruleRows int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		"SELECT count(*) FROM price_rule WHERE price_id = ANY($1)", priceIDs).Scan(&ruleRows))
	assert.Equal(t, 1, ruleRows,
		"the old generations' rules have to go together with their parent")

	live, err := svc.ListPrices(ctx, set.ID)
	require.NoError(t, err)
	require.Len(t, live, 1)
	assert.Equal(t, int64(15000), live[0].Amount, "the row left standing has to be the LAST generation")
	require.Len(t, live[0].Rules, 1)
	assert.Equal(t, []string{"reg_4"}, live[0].Rules[0].Values)
}

// TestADeletedSetsPricesAreStamped proves that deleting a container stays SOFT.
//
// ADR 0047 hardened a single call site; this second one has to stay as it is,
// because the stamp is the only thing that hides a deleted container's price
// from the calculation: ListPriceCandidates does not JOIN price_set, and the
// service reads the container only when zero candidates come back.
//
// A test that looks at the number of LIVE prices CANNOT SEE this distinction —
// were the container delete turned into a hard delete too, the live count
// would still be zero — so the row itself is looked up and its stamp is read.
func TestADeletedSetsPricesAreStamped(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{{CurrencyCode: "TRY", Amount: 100}})
	require.NoError(t, err)

	prices, err := svc.ListPrices(ctx, set.ID)
	require.NoError(t, err)
	require.Len(t, prices, 1)
	priceID := prices[0].ID

	require.NoError(t, svc.DeletePriceSet(ctx, set.ID))

	var stamp *time.Time
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		"SELECT deleted_at FROM price WHERE id = $1", priceID).Scan(&stamp),
		"deleting the container must NOT REMOVE the price row; the row was not found")
	assert.NotNil(t, stamp, "deleting the container has to stamp the price row")
}
