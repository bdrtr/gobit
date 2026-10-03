//go:build integration

// The tests in this file need a real PostgreSQL instance (and therefore
// Docker); they are separated with the `integration` tag so that `make test`
// stays fast. To run them: make test-integration
//
// The unit tests prove the service's DECISIONS (discount arithmetic, skipping,
// allocation). The tests here prove the GROUND those decisions stand on: that
// the migration can be rolled back, that the constraints are really enforced,
// and that the concurrency claim holds at the database level.
//
// The claim "a concurrent Redeem cannot corrupt the counter and the budget" in
// particular can be tested ONLY here, with real goroutines on real row locks:
// an in-memory fake cannot prove that claim, because the locks live inside the
// database, not inside the fake.
package promotion_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/promotion"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository"
	"github.com/bdrtr/gobit/internal/modules/promotion/service"
	"github.com/bdrtr/gobit/internal/testdb"
)

const postgresImage = "postgres:16-alpine"

// moduleTables are the tables the module owns; the migration tests use this
// list.
var moduleTables = []string{
	"campaign", "promotion", "promotion_application_method",
	"promotion_rule", "promotion_redemption",
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

	cfg := db.DefaultConfig(testDSN)
	// The concurrency tests run dozens of goroutines at once; because every
	// transaction holds a connection, the pool is opened wider than the default.
	cfg.MaxConns = 24
	testPool, err = db.New(ctx, cfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not open the connection pool: %v\n", err)
		return 1
	}
	defer testPool.Close()

	if err := db.Migrate(ctx, testDSN, promotion.New(nil).Migrations(), promotion.ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "could not apply the migration: %v\n", err)
		return 1
	}

	return m.Run()
}

// newService builds a service that runs on the real repository.
func newService(t *testing.T) *service.Service {
	t.Helper()

	return service.New(repository.New(testPool.Pool()), service.Options{})
}

// uniqueCode produces a coupon code that does not collide between tests.
//
// Because the code goes into a UNIQUE index, tests cannot use one another's
// code; it is derived from the id generator, which already produces a body
// that does not collide.
func uniqueCode() string {
	return "K" + models.NewPromotionID(time.Now())[len(models.PromotionIDPrefix):]
}

// activePromotion creates an active promotion with its method set up.
func activePromotion(ctx context.Context, t *testing.T, svc *service.Service, in service.PromotionInput) models.Promotion {
	t.Helper()

	if in.Code == "" {
		in.Code = uniqueCode()
	}
	if in.Status == "" {
		in.Status = models.PromotionActive
	}
	promo, err := svc.CreatePromotion(ctx, in)
	require.NoError(t, err)

	_, err = svc.SetApplicationMethod(ctx, promo.ID, service.ApplicationMethodInput{
		Type:       models.MethodPercentage,
		TargetType: models.TargetItems,
		Allocation: models.AllocationEach,
		Value:      2000,
	})
	require.NoError(t, err)
	return promo
}

// TestTheMigrationsReallyRollBack proves the migrations apply, roll back and
// apply AGAIN (plan Section 8).
//
// The up->down->up cycle is required: a test that only checks "is there a down
// file" cannot catch a down that fails on the order its DROPs depend on.
func TestTheMigrationsReallyRollBack(t *testing.T) {
	ctx := context.Background()
	src := promotion.New(nil).Migrations()
	// The rollback runs in a database of its own. In the one this package
	// shares it would drop every other test's promotions with the schema (D141).
	dsn := testdb.New(t, testDSN, "promotion_migration")
	require.NoError(t, db.Migrate(ctx, dsn, src, promotion.ModuleName))

	for _, table := range moduleTables {
		require.True(t, testdb.TableExists(t, dsn, table), "%s has to exist at the start", table)
	}

	require.NoError(t, db.MigrateDown(ctx, dsn, src, promotion.ModuleName, 0))
	for _, table := range moduleTables {
		assert.False(t, testdb.TableExists(t, dsn, table), "%s must be gone after the rollback", table)
	}

	require.NoError(t, db.Migrate(ctx, dsn, src, promotion.ModuleName))
	for _, table := range moduleTables {
		assert.True(t, testdb.TableExists(t, dsn, table), "%s has to be applied again", table)
	}

	version, dirty, err := db.Version(ctx, dsn, promotion.ModuleName)
	require.NoError(t, err)
	assert.False(t, dirty, "no migration may be left half applied")
	// The number is written BY HAND and not derived from the embedded files: a
	// derived number would agree with itself whatever it was, and this line's
	// job is to make an ADDED migration noticed. The same reason is written
	// beside the same line in the product module.
	assert.Equal(t, uint(5), version)
}

// TestNoCrossModuleForeignKeys verifies that ALL foreign keys on the module's
// tables go to the module's own tables as well (Principle 2.2).
//
// promotion_redemption.reference in particular is an order id and CANNOT be a
// foreign key; this test shows that the rule really holds in the schema.
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

	var found int
	for rows.Next() {
		var name, src, tgt string
		require.NoError(t, rows.Scan(&name, &src, &tgt))
		assert.Contains(t, owned, tgt,
			"the %s constraint references outside the module (%s -> %s)", name, src, tgt)
		found++
	}
	require.NoError(t, rows.Err())
	assert.Positive(t, found, "foreign keys inside the module must be in use")
}

func TestCampaignLifecycle(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	identifier := "KAMPANYA-" + uniqueCode()

	campaign, err := svc.CreateCampaign(ctx, service.CampaignInput{
		Name:               "Summer Sale",
		CampaignIdentifier: identifier,
		Description:        "Yaz sezonu",
		BudgetType:         models.BudgetSpend,
		BudgetLimit:        ptr(int64(100_000)),
		BudgetCurrencyCode: "TRY",
	})
	require.NoError(t, err)
	assert.False(t, campaign.CreatedAt.IsZero(), "created_at must come from the database")
	assert.Equal(t, "UTC", campaign.CreatedAt.Location().String(), "the time must be UTC")
	assert.Zero(t, campaign.BudgetUsed)

	fetched, err := svc.GetCampaignByIdentifier(ctx, identifier)
	require.NoError(t, err)
	assert.Equal(t, campaign.ID, fetched.ID)

	// The same business identifier cannot be taken a second time; the referee
	// is the database's partial index.
	_, err = svc.CreateCampaign(ctx, service.CampaignInput{Name: "Second", CampaignIdentifier: identifier})
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))

	require.NoError(t, svc.DeleteCampaign(ctx, campaign.ID))
	_, err = svc.GetCampaign(ctx, campaign.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err), "a soft-deleted campaign must not be readable")

	// A deleted business identifier can be used again: the partial index covers
	// only live records.
	_, err = svc.CreateCampaign(ctx, service.CampaignInput{Name: "Yeniden", CampaignIdentifier: identifier})
	assert.NoError(t, err, "a deleted business identifier must not stay reserved forever")
}

func TestCouponCodeIsUnique(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	code := uniqueCode()

	promo, err := svc.CreatePromotion(ctx, service.PromotionInput{Code: code})
	require.NoError(t, err)

	_, err = svc.CreatePromotion(ctx, service.PromotionInput{Code: code})
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))

	// Because the code is stored in UPPER case, a lower-case attempt hits the
	// same coupon too.
	_, err = svc.CreatePromotion(ctx, service.PromotionInput{Code: lower(code)})
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))

	require.NoError(t, svc.DeletePromotion(ctx, promo.ID))
	_, err = svc.CreatePromotion(ctx, service.PromotionInput{Code: code})
	assert.NoError(t, err, "a deleted coupon code can be used again")
}

func TestApplicationMethodIsReplaced(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	promo := activePromotion(ctx, t, svc, service.PromotionInput{})

	second, err := svc.SetApplicationMethod(ctx, promo.ID, service.ApplicationMethodInput{
		Type:         models.MethodFixed,
		TargetType:   models.TargetOrder,
		Value:        5000,
		CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	assert.Equal(t, models.MethodFixed, second.Type)
	assert.Equal(t, models.AllocationAcross, second.Allocation)

	fetched, err := svc.GetApplicationMethod(ctx, promo.ID)
	require.NoError(t, err)
	assert.Equal(t, second.ID, fetched.ID, "there is ONE method per promotion; the second overwrites the first")

	require.NoError(t, svc.DeleteApplicationMethod(ctx, promo.ID))
	_, err = svc.GetApplicationMethod(ctx, promo.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))

	// A promotion whose method was deleted produces NO discount in the
	// computation.
	res, err := svc.ComputeDiscounts(ctx, service.ComputeInput{
		CurrencyCode: "TRY",
		Items:        []service.ComputeItem{{ID: "li_1", Amount: 10000, UnitAmount: 10000, Quantity: 1}},
	})
	require.NoError(t, err)
	assert.Zero(t, res.DiscountTotal)
}

func TestRulesAreProtectedByTheDatabaseConstraints(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	promo := activePromotion(ctx, t, svc, service.PromotionInput{})

	rule, err := svc.AddPromotionRule(ctx, promo.ID, service.RuleInput{
		RuleType:  models.RuleContext,
		Attribute: "customer_group_id",
		Operator:  models.OpIn,
		Values:    []string{"vip", "b2b"},
	})
	require.NoError(t, err)

	rules, err := svc.ListPromotionRules(ctx, promo.ID)
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, []string{"vip", "b2b"}, rules[0].Values, "the TEXT[] column carries the values in order")

	require.NoError(t, svc.DeletePromotionRule(ctx, rule.ID))
	rules, err = svc.ListPromotionRules(ctx, promo.ID)
	require.NoError(t, err)
	assert.Empty(t, rules)
}

func TestTheComputationRunsOnTheRealDatabase(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	campaign, err := svc.CreateCampaign(ctx, service.CampaignInput{
		Name:               "Yaz",
		CampaignIdentifier: "HESAP-" + uniqueCode(),
		BudgetType:         models.BudgetSpend,
		BudgetLimit:        ptr(int64(1_000_000)),
		BudgetCurrencyCode: "TRY",
	})
	require.NoError(t, err)

	coupon := activePromotion(ctx, t, svc, service.PromotionInput{
		CampaignID: &campaign.ID,
	})
	_, err = svc.AddPromotionRule(ctx, coupon.ID, service.RuleInput{
		RuleType: models.RuleContext, Attribute: "region_id",
		Operator: models.OpEq, Values: []string{"reg_1"},
	})
	require.NoError(t, err)

	in := service.ComputeInput{
		CurrencyCode: "TRY",
		Context:      map[string]string{"region_id": "reg_1"},
		Items: []service.ComputeItem{
			{ID: "li_1", Amount: 10_000, UnitAmount: 10_000, Quantity: 1},
			{ID: "li_2", Amount: 5_001, UnitAmount: 5_001, Quantity: 1},
		},
		Codes: []string{coupon.Code},
	}

	res, err := svc.ComputeDiscounts(ctx, in)
	require.NoError(t, err)

	assert.Equal(t, int64(2000), res.Items[0].Amount)
	assert.Equal(t, int64(1000), res.Items[1].Amount, "20% × 5001 = 1000 (rounded down)")
	assert.Equal(t, int64(3000), res.DiscountTotal)
	assert.Equal(t, res.ItemsDiscountTotal+res.ShippingDiscountTotal, res.DiscountTotal)

	// Without the context the rule does not match and no discount is produced.
	in.Context = nil
	res, err = svc.ComputeDiscounts(ctx, in)
	require.NoError(t, err)
	assert.Zero(t, res.DiscountTotal, "the context rule applies when it is read from the real database too")
}

// TestConcurrentRedeemAtTheUsageLimitWinsExactlyTheLimit proves the core of the
// concurrency claim.
//
// A "read first, then write" check made in the application layer CANNOT PASS
// this test: the winners being exactly equal to the limit comes from the row
// lock and the conditional UPDATE.
func TestConcurrentRedeemAtTheUsageLimitWinsExactlyTheLimit(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	const limit = 5
	const contenders = 20
	promo := activePromotion(ctx, t, svc, service.PromotionInput{UsageLimit: ptr(int64(limit))})

	start := make(chan struct{})
	results := make([]error, contenders)

	var wg sync.WaitGroup
	for i := range contenders {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := svc.RedeemPromotion(ctx, service.RedeemInput{
				PromotionID:  promo.ID,
				Reference:    fmt.Sprintf("order_%d", i),
				Amount:       100,
				CurrencyCode: "TRY",
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
		assert.Equal(t, repository.CodeUsageLimitReached, errors.CodeOf(err))
	}
	assert.Equal(t, limit, winners, "as many calls as there are uses must win")

	current, err := svc.GetPromotion(ctx, promo.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(limit), current.UsageCount, "the counter must NOT EXCEED the limit")
}

// TestConcurrentRedeemWritesOneRecordForTheSameReference proves the concurrent
// form of the idempotency: of the calls racing with the same reference, only
// one creates a record and the counter goes up by one.
func TestConcurrentRedeemWritesOneRecordForTheSameReference(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	const contenders = 16
	promo := activePromotion(ctx, t, svc, service.PromotionInput{})

	start := make(chan struct{})
	ids := make([]string, contenders)
	errs := make([]error, contenders)

	var wg sync.WaitGroup
	for i := range contenders {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			redemption, err := svc.RedeemPromotion(ctx, service.RedeemInput{
				PromotionID:  promo.ID,
				Reference:    "order_tek",
				Amount:       250,
				CurrencyCode: "TRY",
			})
			ids[i], errs[i] = redemption.ID, err
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "idempotent call %d must not fail", i)
		assert.Equal(t, ids[0], ids[i], "all of them must see the SAME redemption record")
	}

	current, err := svc.GetPromotion(ctx, promo.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), current.UsageCount,
		"for the same reference the counter must go up only ONCE")

	page, err := svc.ListRedemptions(ctx, promo.ID, 100, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), page.Count, "there must be a single record in the ledger")
}

// TestConcurrentRedeemDoesNotExceedTheCampaignBudget proves that the budget
// counter is not corrupted under concurrent redemption.
//
// The counter is SHARED between two promotions: both lock the same campaign
// row, and had the lock order (first the promotion, then the campaign) not
// been fixed, this test would have hung on a deadlock.
func TestConcurrentRedeemDoesNotExceedTheCampaignBudget(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	const budget = 1000
	const amount = 100
	const contenders = 30

	campaign, err := svc.CreateCampaign(ctx, service.CampaignInput{
		Name:               "Budgeted",
		CampaignIdentifier: "BUTCE-" + uniqueCode(),
		BudgetType:         models.BudgetSpend,
		BudgetLimit:        ptr(int64(budget)),
		BudgetCurrencyCode: "TRY",
	})
	require.NoError(t, err)

	first := activePromotion(ctx, t, svc, service.PromotionInput{CampaignID: &campaign.ID})
	second := activePromotion(ctx, t, svc, service.PromotionInput{CampaignID: &campaign.ID})
	promotions := []models.Promotion{first, second}

	start := make(chan struct{})
	results := make([]error, contenders)

	var wg sync.WaitGroup
	for i := range contenders {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := svc.RedeemPromotion(ctx, service.RedeemInput{
				PromotionID:  promotions[i%len(promotions)].ID,
				Reference:    fmt.Sprintf("order_%d", i),
				Amount:       amount,
				CurrencyCode: "TRY",
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
		assert.Equal(t, repository.CodeBudgetExceeded, errors.CodeOf(err))
	}
	assert.Equal(t, budget/amount, winners, "as many redemptions as the budget allows must win")

	current, err := svc.GetCampaign(ctx, campaign.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(budget), current.BudgetUsed, "the budget counter must NOT EXCEED the limit")
}

// TestConcurrentReleaseDecrementsTheCounterOnce proves the concurrent form of
// the compensation's idempotency.
func TestConcurrentReleaseDecrementsTheCounterOnce(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	const contenders = 16
	campaign, err := svc.CreateCampaign(ctx, service.CampaignInput{
		Name:               "Telafi",
		CampaignIdentifier: "TELAFI-" + uniqueCode(),
		BudgetType:         models.BudgetSpend,
		BudgetLimit:        ptr(int64(10_000)),
		BudgetCurrencyCode: "TRY",
	})
	require.NoError(t, err)

	promo := activePromotion(ctx, t, svc, service.PromotionInput{CampaignID: &campaign.ID})
	_, err = svc.RedeemPromotion(ctx, service.RedeemInput{
		PromotionID: promo.ID, Reference: "order_1", Amount: 750, CurrencyCode: "TRY",
	})
	require.NoError(t, err)

	start := make(chan struct{})
	rolledBack := make([]bool, contenders)
	errs := make([]error, contenders)

	var wg sync.WaitGroup
	for i := range contenders {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			released, err := svc.ReleasePromotion(ctx, service.ReleaseInput{
				PromotionID: promo.ID, Reference: "order_1",
			})
			rolledBack[i], errs[i] = released, err
		}(i)
	}
	close(start)
	wg.Wait()

	var rolledBackCount int
	for i, err := range errs {
		require.NoError(t, err, "compensation %d must not fail; it is idempotent", i)
		if rolledBack[i] {
			rolledBackCount++
		}
	}
	assert.Equal(t, 1, rolledBackCount, "only ONE call must really roll back")

	currentPromo, err := svc.GetPromotion(ctx, promo.ID)
	require.NoError(t, err)
	assert.Zero(t, currentPromo.UsageCount, "the counter must go down only once")

	currentCampaign, err := svc.GetCampaign(ctx, campaign.ID)
	require.NoError(t, err)
	assert.Zero(t, currentCampaign.BudgetUsed, "the budget must go down only once")
}

func TestReleaseWithoutAnyRedemptionDoesNotFail(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	promo := activePromotion(ctx, t, svc, service.PromotionInput{})

	released, err := svc.ReleasePromotion(ctx, service.ReleaseInput{
		PromotionID: promo.ID, Reference: "hic_yazilmadi",
	})

	require.NoError(t, err, "the compensation of a step that blew up before writing must be able to run too")
	assert.False(t, released)
}

func TestTheInteropSurfaceMeetsTheJSONSchema(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	promo := activePromotion(ctx, t, svc, service.PromotionInput{IsAutomatic: true})

	interop := service.NewInterop(svc)
	request := []byte(`{
	  "currency_code": "TRY",
	  "items": [{"id": "li_1", "amount": 10000, "unit_amount": 10000, "quantity": 1}],
	  "shipping_methods": [{"id": "sm_1", "amount": 4990}]
	}`)

	payload, err := interop.ComputeDiscountsJSON(ctx, request)
	require.NoError(t, err)

	var response struct {
		CurrencyCode          string `json:"currency_code"`
		DiscountTotal         int64  `json:"discount_total"`
		ItemsDiscountTotal    int64  `json:"items_discount_total"`
		ShippingDiscountTotal int64  `json:"shipping_discount_total"`
		Items                 []struct {
			ID     string `json:"id"`
			Amount int64  `json:"amount"`
		} `json:"items"`
		Applied []struct {
			PromotionID string `json:"promotion_id"`
			Amount      int64  `json:"amount"`
		} `json:"applied"`
	}
	require.NoError(t, json.Unmarshal(payload, &response))

	assert.Equal(t, "TRY", response.CurrencyCode)
	assert.Equal(t, int64(2000), response.DiscountTotal)
	assert.Equal(t, int64(2000), response.ItemsDiscountTotal)
	assert.Zero(t, response.ShippingDiscountTotal)
	require.Len(t, response.Items, 1)
	assert.Equal(t, "li_1", response.Items[0].ID)
	require.Len(t, response.Applied, 1)
	assert.Equal(t, promo.ID, response.Applied[0].PromotionID)

	// Redemption and compensation must work from the primitive surface too.
	id, err := interop.RedeemPromotion(ctx, promo.ID, "", "order_interop", "TRY", 2000)
	require.NoError(t, err)
	assert.NotEmpty(t, id)

	released, err := interop.ReleasePromotion(ctx, promo.ID, "", "order_interop")
	require.NoError(t, err)
	assert.True(t, released)
}

// TestTheQueryProviderFiltersOnTheRealRepository verifies that the provider
// opens only ACTIVE promotions and a narrow set of fields to the Query layer
// (ADR 0004).
func TestTheQueryProviderFiltersOnTheRealRepository(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	active := activePromotion(ctx, t, svc, service.PromotionInput{})
	draft, err := svc.CreatePromotion(ctx, service.PromotionInput{
		Code: uniqueCode(), Status: models.PromotionDraft,
	})
	require.NoError(t, err)

	provider := service.NewQueryProvider(svc)
	assert.Equal(t, "promotion", provider.Entity())
	assert.Equal(t, "promotion"+query.ProviderSuffix, promotion.ProviderName)

	records, err := provider.FetchByIDs(ctx, []string{active.ID, draft.ID}, nil)
	require.NoError(t, err)
	require.Len(t, records, 1, "a draft promotion must not leak from the read surface")
	assert.Equal(t, active.ID, records[0]["id"])

	for _, field := range []string{"usage_count", "metadata"} {
		assert.NotContains(t, records[0], field, "%q must not be on the read surface", field)
	}
}

// TestTheDatabaseConstraintsAreTheLastDefense verifies that the schema refuses
// inconsistent records even when the service validation is bypassed.
//
// The repository is called directly: the service layer already filters these
// inputs out, but the constraints must hold against SQL run by hand too.
func TestTheDatabaseConstraintsAreTheLastDefense(t *testing.T) {
	ctx := context.Background()
	repo := repository.New(testPool.Pool())
	now := time.Now().UTC()

	cases := []struct {
		name   string
		write  func() error
		reason string
	}{
		{
			name: "lower-case coupon code",
			write: func() error {
				_, err := repo.CreatePromotion(ctx, models.Promotion{
					ID: models.NewPromotionID(now), Code: "kucuk",
					Type: models.PromotionStandard, Status: models.PromotionDraft,
				}, now)
				return err
			},
			reason: "the code is always stored in UPPER case",
		},
		{
			name: "undefined status",
			write: func() error {
				_, err := repo.CreatePromotion(ctx, models.Promotion{
					ID: models.NewPromotionID(now), Code: uniqueCode(),
					Type: models.PromotionStandard, Status: "olmayan",
				}, now)
				return err
			},
			reason: "the status set is locked in the schema",
		},
		{
			name: "spend budget without a currency",
			write: func() error {
				_, err := repo.CreateCampaign(ctx, models.Campaign{
					ID: models.NewCampaignID(now), Name: "X", CampaignIdentifier: uniqueCode(),
					BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(100)),
				}, now)
				return err
			},
			reason: "a budget measured in money cannot be written without a currency",
		},
		{
			name: "fixed discount method without a currency",
			write: func() error {
				promo, err := repo.CreatePromotion(ctx, models.Promotion{
					ID: models.NewPromotionID(now), Code: uniqueCode(),
					Type: models.PromotionStandard, Status: models.PromotionDraft,
				}, now)
				if err != nil {
					return err
				}
				_, err = repo.SetApplicationMethod(ctx, models.ApplicationMethod{
					ID: models.NewApplicationMethodID(now), PromotionID: promo.ID,
					Type: models.MethodFixed, TargetType: models.TargetItems,
					Allocation: models.AllocationEach, Value: 100,
				}, now)
				return err
			},
			reason: "a fixed-amount discount cannot be written without a currency",
		},
		{
			name: "currency on a percentage discount",
			write: func() error {
				promo, err := repo.CreatePromotion(ctx, models.Promotion{
					ID: models.NewPromotionID(now), Code: uniqueCode(),
					Type: models.PromotionStandard, Status: models.PromotionDraft,
				}, now)
				if err != nil {
					return err
				}
				_, err = repo.SetApplicationMethod(ctx, models.ApplicationMethod{
					ID: models.NewApplicationMethodID(now), PromotionID: promo.ID,
					Type: models.MethodPercentage, TargetType: models.TargetItems,
					Allocation: models.AllocationEach, Value: 2000, CurrencyCode: "TRY",
				}, now)
				return err
			},
			reason: "a percentage discount carries no currency",
		},
		{
			name: "redemption ledger row without a currency",
			write: func() error {
				promo, err := repo.CreatePromotion(ctx, models.Promotion{
					ID: models.NewPromotionID(now), Code: uniqueCode(),
					Type: models.PromotionStandard, Status: models.PromotionActive,
				}, now)
				if err != nil {
					return err
				}
				_, _, err = repo.Redeem(ctx, models.Redemption{
					ID: models.NewRedemptionID(now), PromotionID: promo.ID,
					Reference: "order_" + uniqueCode(), Amount: 100,
				}, now)
				return err
			},
			reason: "every amount in the ledger has to carry the currency it is in",
		},
		{
			name: "negative budget limit",
			write: func() error {
				_, err := repo.CreateCampaign(ctx, models.Campaign{
					ID: models.NewCampaignID(now), Name: "X", CampaignIdentifier: uniqueCode(),
					BudgetType: models.BudgetUsage, BudgetLimit: ptr(int64(-1)),
				}, now)
				return err
			},
			reason: "a negative budget cannot be written",
		},
		{
			name: "half a reward pair",
			write: func() error {
				promo, err := repo.CreatePromotion(ctx, models.Promotion{
					ID: models.NewPromotionID(now), Code: uniqueCode(),
					Type: models.PromotionBuyGet, Status: models.PromotionDraft,
				}, now)
				if err != nil {
					return err
				}
				_, err = repo.SetApplicationMethod(ctx, models.ApplicationMethod{
					ID: models.NewApplicationMethodID(now), PromotionID: promo.ID,
					Type: models.MethodPercentage, TargetType: models.TargetItems,
					Allocation: models.AllocationEach, Value: 10000,
					BuyQuantity: ptr(int64(2)),
				}, now)
				return err
			},
			reason: "a buy condition without a reward cannot be written; the pair is WHOLE or NOTHING",
		},
		{
			name: "zero reward quantity",
			write: func() error {
				promo, err := repo.CreatePromotion(ctx, models.Promotion{
					ID: models.NewPromotionID(now), Code: uniqueCode(),
					Type: models.PromotionBuyGet, Status: models.PromotionDraft,
				}, now)
				if err != nil {
					return err
				}
				_, err = repo.SetApplicationMethod(ctx, models.ApplicationMethod{
					ID: models.NewApplicationMethodID(now), PromotionID: promo.ID,
					Type: models.MethodPercentage, TargetType: models.TargetItems,
					Allocation: models.AllocationEach, Value: 10000,
					BuyQuantity: ptr(int64(2)), ApplyToQuantity: ptr(int64(0)),
				}, now)
				return err
			},
			reason: "a reward that comes down to zero units is not a reward",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.write()
			require.Error(t, err, tt.reason)
			assert.Contains(t,
				[]errors.Kind{errors.KindInvalid, errors.KindConflict}, errors.KindOf(err),
				"a constraint violation must be classified as a client error: %v", err)
		})
	}
}

// TestRewardQuantitiesAndTheBuyRuleComeBackFromTheDatabase tests the GROUND of
// the "buy X, get Y" mechanic: are the two new columns and the third rule type
// standing in the real schema.
//
// The unit tests prove the mechanic on hand-built candidates; what they cannot
// prove is that the candidate COMES from the database in this shape — if the
// migration has not been applied, or the mapping drops a column, the
// computation works correctly and no promotion can reach it.
func TestRewardQuantitiesAndTheBuyRuleComeBackFromTheDatabase(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	promo, err := svc.CreatePromotion(ctx, service.PromotionInput{
		Code: uniqueCode(), IsAutomatic: true,
		Type: models.PromotionBuyGet, Status: models.PromotionActive,
	})
	require.NoError(t, err, "a buyget promotion can be published (ADR 0112)")

	_, err = svc.SetApplicationMethod(ctx, promo.ID, service.ApplicationMethodInput{
		Type: models.MethodPercentage, TargetType: models.TargetItems,
		Allocation: models.AllocationEach, Value: 10000,
		BuyQuantity: ptr(int64(2)), ApplyToQuantity: ptr(int64(1)),
	})
	require.NoError(t, err)

	_, err = svc.AddPromotionRule(ctx, promo.ID, service.RuleInput{
		RuleType: models.RuleBuy, Attribute: "variant_id",
		Operator: models.OpIn, Values: []string{"var_1"},
	})
	require.NoError(t, err, "the third rule type must pass the schema's CHECK")

	method, err := svc.GetApplicationMethod(ctx, promo.ID)
	require.NoError(t, err)
	require.NotNil(t, method.BuyQuantity, "the buy quantity must come back from the database")
	require.NotNil(t, method.ApplyToQuantity, "the reward quantity must come back from the database")
	assert.Equal(t, int64(2), *method.BuyQuantity)
	assert.Equal(t, int64(1), *method.ApplyToQuantity)

	rules, err := svc.ListPromotionRules(ctx, promo.ID)
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, models.RuleBuy, rules[0].RuleType,
		"the rule type must come back as it is; the computation picks the buy set with it")
}

// TestRedeemRefusesAPromotionThatIsNotLiveOnTheRealDatabase verifies on REAL
// Postgres that a draft or inactive promotion cannot be redeemed.
//
// The check is made while the promotion row is locked with FOR UPDATE; the
// in-memory fake only imitates that, here the ground is tested.
func TestRedeemRefusesAPromotionThatIsNotLiveOnTheRealDatabase(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	for _, status := range []models.PromotionStatus{models.PromotionDraft, models.PromotionInactive} {
		t.Run(string(status), func(t *testing.T) {
			campaign, err := svc.CreateCampaign(ctx, service.CampaignInput{
				Name:               "Yaz",
				CampaignIdentifier: "TASLAK-" + uniqueCode(),
				BudgetType:         models.BudgetSpend,
				BudgetLimit:        ptr(int64(1_000_000)),
				BudgetCurrencyCode: "TRY",
			})
			require.NoError(t, err)

			promo := activePromotion(ctx, t, svc, service.PromotionInput{
				Status: status, CampaignID: &campaign.ID,
			})

			_, err = svc.RedeemPromotion(ctx, service.RedeemInput{
				PromotionID: promo.ID, Reference: "order_" + uniqueCode(),
				Amount: 2500, CurrencyCode: "TRY",
			})

			require.Error(t, err, "a promotion that has not been published cannot be redeemed")
			assert.Equal(t, errors.KindConflict, errors.KindOf(err))
			assert.Equal(t, repository.CodePromotionNotActive, errors.CodeOf(err))

			current, err := svc.GetPromotion(ctx, promo.ID)
			require.NoError(t, err)
			assert.Zero(t, current.UsageCount, "a refused redemption does not increment the counter")

			currentCampaign, err := svc.GetCampaign(ctx, campaign.ID)
			require.NoError(t, err)
			assert.Zero(t, currentCampaign.BudgetUsed,
				"a promotion that has not been published does NOT SPEND the campaign budget")
		})
	}
}

// TestRedeemRefusesAClosedCampaignWindowOnTheRealDatabase verifies on REAL
// Postgres that the moment of redemption has to be inside the campaign's
// window.
//
// The check is made while the campaign row is locked: the window and the
// budget counter must be a record of the same moment.
func TestRedeemRefusesAClosedCampaignWindowOnTheRealDatabase(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	now := time.Now().UTC()

	campaign, err := svc.CreateCampaign(ctx, service.CampaignInput{
		Name:               "Ended",
		CampaignIdentifier: "PENCERE-" + uniqueCode(),
		StartsAt:           ptr(now.Add(-48 * time.Hour)),
		EndsAt:             ptr(now.Add(-24 * time.Hour)),
		BudgetType:         models.BudgetSpend,
		BudgetLimit:        ptr(int64(1_000_000)),
		BudgetCurrencyCode: "TRY",
	})
	require.NoError(t, err)

	promo := activePromotion(ctx, t, svc, service.PromotionInput{CampaignID: &campaign.ID})

	_, err = svc.RedeemPromotion(ctx, service.RedeemInput{
		PromotionID: promo.ID, Reference: "order_" + uniqueCode(),
		Amount: 2500, CurrencyCode: "TRY",
	})

	require.Error(t, err, "the budget of a campaign whose window has closed cannot be spent")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, repository.CodeCampaignWindowClosed, errors.CodeOf(err))

	current, err := svc.GetCampaign(ctx, campaign.ID)
	require.NoError(t, err)
	assert.Zero(t, current.BudgetUsed)
}

// TestUpdateCampaignBudgetUnitLockIsInTheDatabase verifies that the lock is
// NOT IN THE APPLICATION but in a single conditional UPDATE.
//
// Once the counter has been filled, an attempt to change the budget unit must
// be refused, while the form of the same request that KEEPS the unit must pass.
func TestUpdateCampaignBudgetUnitLockIsInTheDatabase(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	identifier := "KILIT-" + uniqueCode()

	campaign, err := svc.CreateCampaign(ctx, service.CampaignInput{
		Name:               "Yaz",
		CampaignIdentifier: identifier,
		BudgetType:         models.BudgetSpend,
		BudgetLimit:        ptr(int64(1_000_000)),
		BudgetCurrencyCode: "TRY",
	})
	require.NoError(t, err)

	promo := activePromotion(ctx, t, svc, service.PromotionInput{CampaignID: &campaign.ID})
	_, err = svc.RedeemPromotion(ctx, service.RedeemInput{
		PromotionID: promo.ID, Reference: "order_" + uniqueCode(),
		Amount: 30_000, CurrencyCode: "TRY",
	})
	require.NoError(t, err)

	_, err = svc.UpdateCampaign(ctx, campaign.ID, service.CampaignInput{
		Name:               "Yaz",
		CampaignIdentifier: identifier,
		BudgetType:         models.BudgetUsage,
		BudgetLimit:        ptr(int64(100)),
	})
	require.Error(t, err, "the 30000 MINOR UNITS on the counter would be read as 30000 USES once the type changed")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, repository.CodeBudgetUnitLocked, errors.CodeOf(err))

	_, err = svc.UpdateCampaign(ctx, campaign.ID, service.CampaignInput{
		Name:               "Yaz",
		CampaignIdentifier: identifier,
		BudgetType:         models.BudgetSpend,
		BudgetLimit:        ptr(int64(1_000_000)),
		BudgetCurrencyCode: "USD",
	})
	require.Error(t, err, "the earlier TRY spending would be counted as USD")
	assert.Equal(t, repository.CodeBudgetUnitLocked, errors.CodeOf(err))

	updated, err := svc.UpdateCampaign(ctx, campaign.ID, service.CampaignInput{
		Name:               "Yaz Sonu",
		CampaignIdentifier: identifier,
		BudgetType:         models.BudgetSpend,
		BudgetLimit:        ptr(int64(2_000_000)),
		BudgetCurrencyCode: "TRY",
	})
	require.NoError(t, err, "as long as the unit is kept, the definition and the LIMIT can be updated")
	assert.Equal(t, "Yaz Sonu", updated.Name)
	assert.Equal(t, int64(2_000_000), *updated.BudgetLimit)
	assert.Equal(t, int64(30_000), updated.BudgetUsed, "the counter does not change on this path")
}

// TestUpdateCampaignMissingCampaignNotFound verifies that the lock check does
// not swallow "not found": the conditional UPDATE returns no row for either
// reason, and the two have to be SEPARATE errors.
func TestUpdateCampaignMissingCampaignNotFound(t *testing.T) {
	ctx := context.Background()

	_, err := newService(t).UpdateCampaign(ctx, models.NewCampaignID(time.Now()), service.CampaignInput{
		Name: "Missing", CampaignIdentifier: "MISSING-" + uniqueCode(),
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"a campaign that does not exist must not return the SAME error as a lock conflict")
}

// ptr returns the address of a value.
func ptr[T any](v T) *T { return &v }

// lower converts a code to lower case; the test that checks a coupon code is
// case-insensitive uses it.
func lower(s string) string {
	out := []rune(s)
	for i, r := range out {
		if r >= 'A' && r <= 'Z' {
			out[i] = r + ('a' - 'A')
		}
	}
	return string(out)
}

// countOf runs a single-column count query.
func countOf(ctx context.Context, t *testing.T, sql string, args ...any) int64 {
	t.Helper()

	var n int64
	require.NoError(t, testPool.Pool().QueryRow(ctx, sql, args...).Scan(&n))
	return n
}

// blockedRequestCount returns how many requests the given session is
// BLOCKING.
//
// The narrowing is mandatory: the condition "somebody in the database is
// waiting on a lock" is also met by another test's or the pool's wait, and in
// that case the assertion would run before the moment we want to measure had
// come, and of course it would hold. pg_blocking_pids says the wait comes from
// OUR locking transaction.
func blockedRequestCount(ctx context.Context, t *testing.T, blockerPID int32) int64 {
	t.Helper()

	return countOf(ctx, t,
		`SELECT count(*) FROM pg_stat_activity
         WHERE datname = current_database()
           AND wait_event_type = 'Lock'
           AND $1 = ANY(pg_blocking_pids(pid))`, blockerPID)
}

// requireBlockedRequest verifies that the given session really is holding a
// request up.
//
// It looks at the WAIT STATE instead of sleeping: a fixed sleep would either
// wake up early on a slow machine and make the test brittle, or add idle
// waiting to every run.
func requireBlockedRequest(ctx context.Context, t *testing.T, blockerPID int32) {
	t.Helper()

	require.Eventually(t, func() bool {
		return blockedRequestCount(ctx, t, blockerPID) > 0
	}, 10*time.Second, 10*time.Millisecond, "the request should have been waiting on this session's lock")
}

// promotionDeletingTx opens a transaction that SOFT-deletes the promotion but
// has NOT committed YET; the returned pid is there so the wait can be
// ATTRIBUTED to this transaction.
//
// The rival is NOT a plain `SELECT ... FOR UPDATE` but the delete's REAL
// statement (the UPDATE that repository.DeletePromotion runs). The difference
// carries the whole test: FOR UPDATE conflicts with every row lock and would
// hold the write path up whichever lock it took — so the test would stay green
// even if the wrong lock were chosen. The delete statement puts only FOR NO KEY
// UPDATE on the row; only this way is it tested that the SHARED lock the write
// path takes really waits for a real delete.
func promotionDeletingTx(
	ctx context.Context,
	t *testing.T,
	promotionID string,
) (tx pgx.Tx, pid int32, cleanup func()) {
	t.Helper()

	conn, err := testPool.Pool().Acquire(ctx)
	require.NoError(t, err)

	tx, err = conn.Begin(ctx)
	if err != nil {
		conn.Release()
		require.NoError(t, err)
	}
	cleanup = func() {
		_ = tx.Rollback(ctx)
		conn.Release()
	}

	require.NoError(t, tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid))

	tag, err := tx.Exec(ctx,
		`UPDATE promotion SET deleted_at = now(), updated_at = now()
         WHERE id = $1 AND deleted_at IS NULL`, promotionID)
	require.NoError(t, err)
	require.EqualValues(t, 1, tag.RowsAffected(), "the delete should have affected a single row")

	return tx, pid, cleanup
}

// TestAddingARuleDoesNotWriteUnderADeletedPromotion verifies deterministically
// that adding a rule CLOSES the "read first, then write" race.
//
// The setup does not leave the intermediate state to timing:
//
//  1. A rival transaction SOFT-deletes the promotion and does NOT commit.
//  2. AddPromotionRule starts and wants to read the promotion with a SHARED
//     lock; it WAITS on the delete's lock. pg_blocking_pids verifies that the
//     wait comes from the rival transaction.
//  3. The rival transaction commits; the waiting request wakes up, and after
//     taking the lock it evaluates the WHERE condition AGAIN and sees "no
//     record".
//
// Both assertions are needed and they measure SEPARATE things: step 2 shows
// that the lock is REALLY taken (otherwise there would have been no wait at
// all), and step 3's result being NotFound shows that the write did not land.
//
// Measured (2026-09-06): while the check was made in the service, with a
// separate autocommit read, this test failed in TWO places at once — the write
// did not wait at all, and the rule landed under the deleted promotion. A
// foreign key does not catch it: a soft delete leaves the row in place and the
// FK looks at the row's EXISTENCE, not at its deleted_at.
func TestAddingARuleDoesNotWriteUnderADeletedPromotion(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	promo := activePromotion(ctx, t, svc, service.PromotionInput{})

	tx, pid, cleanup := promotionDeletingTx(ctx, t, promo.ID)
	defer cleanup()

	done := make(chan error, 1)
	go func() {
		_, err := svc.AddPromotionRule(ctx, promo.ID, service.RuleInput{
			RuleType:  models.RuleContext,
			Attribute: "customer_group_id",
			Operator:  models.OpEq,
			Values:    []string{"vip"},
		})
		done <- err
	}()
	requireBlockedRequest(ctx, t, pid)
	require.NoError(t, tx.Commit(ctx))

	err := <-done
	require.Error(t, err, "no rule must be written to a deleted promotion")
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))

	assert.Zero(t, countOf(ctx, t,
		`SELECT count(*) FROM promotion_rule r
         JOIN promotion p ON p.id = r.promotion_id
         WHERE r.promotion_id = $1 AND p.deleted_at IS NOT NULL`, promo.ID),
		"no live rule must be left under a deleted promotion")
}

// TestWritingAMethodDoesNotWriteUnderADeletedPromotion verifies the same race
// on the application method path; the setup is the same as
// [TestAddingARuleDoesNotWriteUnderADeletedPromotion].
//
// The two paths are tested SEPARATELY because their writes differ: the rule is
// a plain INSERT, while the method is an upsert. "An upsert is a single
// statement, so it is atomic" is exactly the trap here — what is a single
// statement is the write itself, not the knowledge that the promotion is live.
func TestWritingAMethodDoesNotWriteUnderADeletedPromotion(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	promo, err := svc.CreatePromotion(ctx, service.PromotionInput{
		Code:   uniqueCode(),
		Status: models.PromotionActive,
	})
	require.NoError(t, err)

	tx, pid, cleanup := promotionDeletingTx(ctx, t, promo.ID)
	defer cleanup()

	done := make(chan error, 1)
	go func() {
		_, methodErr := svc.SetApplicationMethod(ctx, promo.ID, service.ApplicationMethodInput{
			Type:       models.MethodPercentage,
			TargetType: models.TargetItems,
			Allocation: models.AllocationEach,
			Value:      5000,
		})
		done <- methodErr
	}()
	requireBlockedRequest(ctx, t, pid)
	require.NoError(t, tx.Commit(ctx))

	err = <-done
	require.Error(t, err, "no application method must be written to a deleted promotion")
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))

	assert.Zero(t, countOf(ctx, t,
		`SELECT count(*) FROM promotion_application_method m
         JOIN promotion p ON p.id = m.promotion_id
         WHERE m.promotion_id = $1 AND m.deleted_at IS NULL AND p.deleted_at IS NOT NULL`,
		promo.ID),
		"no live application method must be left under a deleted promotion")
}

// TestTheRedemptionChainByCouponCodeCompletesOnTheRealDatabase verifies that a
// coupon code is resolved on REAL Postgres all the way from the computation to
// the redemption and the compensation.
//
// When every step of the chain was measured (2026-09-07), a single query had
// never run: GetPromotionByCode. While the lock, counter and idempotency
// queries were all covered 100%, the step that goes from the code itself to
// the promotion ran only against the in-memory fake — that is, on the path of
// a customer who TYPES a coupon code there was SQL that had never seen
// Postgres. The fake cannot see that gap: the fake's lookup loop is in Go, the
// real WHERE is in SQL, and the two can silently diverge.
//
// The redemption is asked for by code ONLY, ON PURPOSE (PromotionID is left
// empty): had the id been given as well, [service.Service.resolvePromotion]
// would pick the id branch and the code query would still never run. The code
// is also given in LOWER case; because the column stores UPPER case, this is
// the one attempt that shows the normalization holds against the real column.
func TestTheRedemptionChainByCouponCodeCompletesOnTheRealDatabase(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	coupon := activePromotion(ctx, t, svc, service.PromotionInput{})
	require.False(t, coupon.IsAutomatic, "the subject of this test is a promotion that NEEDS A CODE")

	res, err := svc.ComputeDiscounts(ctx, service.ComputeInput{
		CurrencyCode: "TRY",
		Items:        []service.ComputeItem{{ID: "li_1", Amount: 10_000, UnitAmount: 10_000, Quantity: 1}},
		Codes:        []string{coupon.Code},
	})
	require.NoError(t, err)
	assert.Empty(t, res.UnmatchedCodes, "a valid code must not be counted as unmatched")

	// The assertion is tied to THIS coupon's share, not to the cart TOTAL. The
	// reason is the shared database: other tests using the same container leave
	// AUTOMATIC promotions behind and those enter every computation too, so the
	// total is not under this test's control. The assertion keyed by the coupon
	// is both narrower and more correct — what is tested is not "how much did
	// the cart go down" but "how much did the coupon whose code was typed take
	// off".
	share := appliedShare(t, res, coupon.Code)
	require.Equal(t, int64(2000), share, "20% × 10000 must be the coupon's share")

	reference := "order_" + uniqueCode()
	redemption, err := svc.RedeemPromotion(ctx, service.RedeemInput{
		Code:         lower(coupon.Code),
		Reference:    reference,
		Amount:       share,
		CurrencyCode: "TRY",
	})
	require.NoError(t, err, "the coupon code alone must be able to name the redemption")
	assert.Equal(t, coupon.ID, redemption.PromotionID,
		"the lower-case code must resolve to the promotion of the row stored in UPPER case")
	assert.Equal(t, share, redemption.Amount, "the coupon's own share must be written to the ledger")

	// The redemption REALLY is in the ledger: the row is counted, the service's
	// answer is not repeated back. Had the record the service returned not been
	// written, it would have looked the same.
	assert.EqualValues(t, 1, countOf(ctx, t,
		`SELECT count(*) FROM promotion_redemption
         WHERE promotion_id = $1 AND reference = $2 AND released_at IS NULL`,
		coupon.ID, reference), "the redemption made by code must be a single row in the ledger")

	fetched, err := svc.GetRedemption(ctx, coupon.ID, reference)
	require.NoError(t, err)
	assert.Equal(t, redemption.ID, fetched.ID)

	current, err := svc.GetPromotion(ctx, coupon.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 1, current.UsageCount, "a redemption by code must increment the counter")

	// The compensation must be nameable by code too: the flow that cancels an
	// order may be holding only the code the customer typed.
	released, err := svc.ReleasePromotion(ctx, service.ReleaseInput{
		Code: coupon.Code, Reference: reference,
	})
	require.NoError(t, err)
	assert.True(t, released, "a compensation named by code must really do the work")

	current, err = svc.GetPromotion(ctx, coupon.ID)
	require.NoError(t, err)
	assert.Zero(t, current.UsageCount, "the compensation must roll the counter back")
}

// appliedShare returns the discount that falls to the GIVEN coupon code in a
// computation result.
//
// Reading the share instead of the total is mandatory: the tests share a
// single Postgres container, and a test that ran earlier may have left an
// AUTOMATIC promotion in the table. An automatic promotion enters every
// computation without a code, so the cart total depends not on the state this
// test set up but on the suite's history up to that moment. Measured
// (2026-09-07): the first version, which looked at the cart total, was green
// on its own and red inside the suite.
func appliedShare(t *testing.T, res service.ComputeResult, code string) int64 {
	t.Helper()

	for i := range res.Applied {
		if res.Applied[i].Code == code {
			return res.Applied[i].Amount
		}
	}
	t.Fatalf("the coupon %s is not among the applied ones: %+v", code, res.Applied)
	return 0
}

// TestADeletedPromotionsCouponCodeResolvesOnNoSurface verifies that the code of
// a soft-deleted promotion can no longer be found on the read surfaces.
//
// The claim is ONLY of the kind the database can witness: a soft delete leaves
// the row in place, so what decides "no record" is not a key removed from a
// map but the query's `deleted_at IS NULL` condition. The in-memory fake
// deletes the promotion by REMOVING it from the map and cannot see that
// condition being removed; the test's first assertion (the row is still in the
// table) sets up exactly that distinction.
//
// The consequence is heavy: if the condition goes, the coupon of an expired
// campaign looks valid again on the store surface, and the code the operator
// "deleted" comes back to the customer as usable.
//
// The redemption path is NOT TESTED here, and that is deliberate: the
// redemption reads the promotion with LockPromotion, and that query has its
// OWN `deleted_at IS NULL` condition, so even if the condition in the code
// query were removed the redemption would still be refused. Put here, it would
// have been an assertion that does not bite.
func TestADeletedPromotionsCouponCodeResolvesOnNoSurface(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	coupon := activePromotion(ctx, t, svc, service.PromotionInput{})
	require.NoError(t, svc.DeletePromotion(ctx, coupon.ID))

	require.EqualValues(t, 1, countOf(ctx, t,
		`SELECT count(*) FROM promotion WHERE id = $1 AND deleted_at IS NOT NULL`, coupon.ID),
		"a soft delete leaves the row IN PLACE; the test's meaning rests on it")

	_, err := svc.GetPromotionByCode(ctx, coupon.Code)
	require.Error(t, err, "the code of a deleted coupon must not resolve on the admin surface either")
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))

	_, err = svc.LookupStoreCoupon(ctx, coupon.Code)
	require.Error(t, err, "a deleted coupon must not look usable to the customer")
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
	assert.Equal(t, service.CodePromotionNotUsable, errors.CodeOf(err))

	// Because the code can be used again, a NEW promotion can be opened with the
	// same code; the deleted row's code is no longer ITS OWN.
	replacement, err := svc.CreatePromotion(ctx, service.PromotionInput{
		Code: coupon.Code, Status: models.PromotionActive,
	})
	require.NoError(t, err)
	resolved, err := svc.GetPromotionByCode(ctx, coupon.Code)
	require.NoError(t, err)
	assert.Equal(t, replacement.ID, resolved.ID,
		"while the code matches two rows the live one must be picked, not the deleted one")
}

// TestUpdatingAPromotionChangesTheDefinitionAndLeavesTheUsageCounter verifies
// on REAL Postgres what the promotion's edit path does and does not do.
//
// When it was measured (2026-09-07), both the HTTP handler and the UPDATE
// beneath it were at 0%: while creating and deleting were covered, an edit had
// never been sent to the database. Changing a promotion's definition is what
// an operator does most often, and it is a perfect example of the "SQL never
// sent" class.
//
// The real claim is about the counter: usage_count is ON PURPOSE absent from
// the UPDATE's SET list (see the reasoning in queries/promotion.sql). Had that
// column entered the list, an operator editing the coupon would reset its
// usage history and a coupon whose limit was used up would become
// distributable again — on top of which, because the redemption ledger rows
// would stay in place, the ledger and the counter would no longer agree. This
// can be seen only by running a real UPDATE; the fake protects the counter by
// hand on the Go side and says nothing about the SET list.
func TestUpdatingAPromotionChangesTheDefinitionAndLeavesTheUsageCounter(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	campaign, err := svc.CreateCampaign(ctx, service.CampaignInput{
		Name:               "Yaz",
		CampaignIdentifier: "GUNCELLEME-" + uniqueCode(),
		BudgetType:         models.BudgetSpend,
		BudgetLimit:        ptr(int64(1_000_000)),
		BudgetCurrencyCode: "TRY",
	})
	require.NoError(t, err)

	coupon := activePromotion(ctx, t, svc, service.PromotionInput{
		CampaignID: &campaign.ID,
		UsageLimit: ptr(int64(5)),
	})
	for i := range 2 {
		_, redeemErr := svc.RedeemPromotion(ctx, service.RedeemInput{
			PromotionID: coupon.ID, Reference: fmt.Sprintf("order_%d_%s", i, uniqueCode()),
			Amount: 2500, CurrencyCode: "TRY",
		})
		require.NoError(t, redeemErr)
	}

	newCode := uniqueCode()
	updated, err := svc.UpdatePromotion(ctx, coupon.ID, service.PromotionInput{
		Code:       lower(newCode),
		CampaignID: &campaign.ID,
		Status:     models.PromotionInactive,
		UsageLimit: ptr(int64(9)),
		Metadata:   map[string]string{"kanal": "eposta"},
	})
	require.NoError(t, err)

	assert.Equal(t, newCode, updated.Code, "the code must be written converted to UPPER case")
	assert.Equal(t, models.PromotionInactive, updated.Status)
	require.NotNil(t, updated.UsageLimit)
	assert.EqualValues(t, 9, *updated.UsageLimit)
	assert.Equal(t, map[string]string{"kanal": "eposta"}, updated.Metadata)

	assert.EqualValues(t, 2, updated.UsageCount,
		"an edit CANNOT ERASE the usage history; had it been reset, a used-up coupon could be distributed again")
	assert.Equal(t, coupon.CreatedAt, updated.CreatedAt, "the moment of creation does not change with an edit")
	assert.False(t, updated.UpdatedAt.Before(coupon.UpdatedAt),
		"the moment of the edit must not go backwards")

	// The ROW is tested, not the answer: the returned record may be right while
	// the write did not land.
	fetched, err := svc.GetPromotion(ctx, coupon.ID)
	require.NoError(t, err)
	assert.Equal(t, newCode, fetched.Code)
	assert.Equal(t, models.PromotionInactive, fetched.Status)
	assert.EqualValues(t, 2, fetched.UsageCount)

	// The ledger and the counter must agree; had the counter been reset these
	// two numbers would diverge.
	assert.EqualValues(t, 2, countOf(ctx, t,
		`SELECT count(*) FROM promotion_redemption
         WHERE promotion_id = $1 AND released_at IS NULL`, coupon.ID),
		"the redemption ledger must not be affected by the edit")

	currentCampaign, err := svc.GetCampaign(ctx, campaign.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 5000, currentCampaign.BudgetUsed,
		"editing a promotion does not give the campaign budget back")
}

// TestUpdatingAPromotionDoesNotLandOnADeletedRow verifies that a soft-deleted
// promotion cannot be edited.
//
// The condition is in the UPDATE's WHERE and only the database can witness it:
// because the row stays in place, "id = $1" alone finds it. If the condition
// goes, a deleted promotion can be silently published again — an edit that
// sets the status to `active` would turn the deleted row, without reviving it,
// into a record that produces discounts (it would not enter the computation
// because the candidate query looks at `deleted_at`, but the coupon ledger and
// the admin listing would diverge).
func TestUpdatingAPromotionDoesNotLandOnADeletedRow(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	coupon := activePromotion(ctx, t, svc, service.PromotionInput{})
	oldCode := coupon.Code
	require.NoError(t, svc.DeletePromotion(ctx, coupon.ID))

	_, err := svc.UpdatePromotion(ctx, coupon.ID, service.PromotionInput{
		Code: uniqueCode(), Status: models.PromotionActive,
	})

	require.Error(t, err, "a deleted promotion must not be editable")
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))

	assert.EqualValues(t, 1, countOf(ctx, t,
		`SELECT count(*) FROM promotion
         WHERE id = $1 AND code = $2 AND status = 'active' AND deleted_at IS NOT NULL`,
		coupon.ID, oldCode),
		"a refused edit must write NOTHING AT ALL to the row")
}

// TestUpdatingAPromotionCannotTakeAnotherOnesCouponCode verifies that coupon
// code uniqueness holds on the EDIT path too.
//
// [TestCouponCodeIsUnique] tests only creation; because the referee of
// uniqueness is a partial index, the two paths have to be shown separately.
// There is NO code collision check in the service layer and there must not be
// one — between two concurrent edits only the database can referee. If it
// slips, the consequence is concrete: the same code is tied to two live
// promotions and which discount the code a customer types will give becomes
// undefined (the code query is `:one` and fails when it sees the second row).
func TestUpdatingAPromotionCannotTakeAnotherOnesCouponCode(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	first := activePromotion(ctx, t, svc, service.PromotionInput{})
	second := activePromotion(ctx, t, svc, service.PromotionInput{})

	_, err := svc.UpdatePromotion(ctx, second.ID, service.PromotionInput{
		Code: first.Code, Status: models.PromotionActive,
	})

	require.Error(t, err, "a live coupon's code cannot be taken over by an edit")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err),
		"a uniqueness violation must be classified as a client conflict")

	assert.EqualValues(t, 1, countOf(ctx, t,
		`SELECT count(*) FROM promotion WHERE code = $1 AND deleted_at IS NULL`, first.Code),
		"the code must still belong to ONE live promotion")

	fetched, err := svc.GetPromotion(ctx, second.ID)
	require.NoError(t, err)
	assert.Equal(t, second.Code, fetched.Code, "a refused edit must not change the code")
}
