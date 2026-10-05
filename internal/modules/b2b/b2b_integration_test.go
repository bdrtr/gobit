//go:build integration

// The tests in this file need a real PostgreSQL instance (and therefore
// Docker); they are kept apart with the `integration` tag so that `make test`
// stays fast. To run them: make test-integration
//
// The unit tests prove the service's DECISIONS with a fake repository and a
// fake bond service. The tests here prove the GROUND those decisions rest on:
// that the migration really can be rolled back, that the rule "a customer is
// an employee of at most ONE company" lives in the link table's unique index,
// that deleting a company clears the employees and the bonds in ONE
// transaction, and that the CHECK constraints hold even when the application's
// validation is skipped.
//
// None of these claims can be exercised with the fake bond service: the fake
// IMITATES the cardinality in Go, and only this file shows that the imitation
// matches the real thing.
package b2b_test

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/link"
	"github.com/bdrtr/gobit/internal/modules/b2b"
	"github.com/bdrtr/gobit/internal/modules/b2b/models"
	"github.com/bdrtr/gobit/internal/modules/b2b/repository"
	"github.com/bdrtr/gobit/internal/modules/b2b/service"
	"github.com/bdrtr/gobit/internal/testdb"
)

const postgresImage = "postgres:16-alpine"

// moduleTables are the tables the module owns; the migration tests use this
// list.
var moduleTables = []string{"b2b_company", "b2b_company_employee"}

var (
	// testPool is the pool all tests share.
	testPool *db.Pool
	// testDSN is the connection address for the migration calls.
	testDSN string
	// testLinks is the real link service; the definition is declared once.
	testLinks link.LinkService
	// customerCounter produces customer ids that are unique across tests.
	customerCounter atomic.Int64
)

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres brings up a single Postgres container and runs all tests on
// it. It is a separate function because os.Exit skips the defers.
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

	testPool, err = db.New(ctx, db.DefaultConfig(testDSN), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "the connection pool could not be opened: %v\n", err)
		return 1
	}
	defer testPool.Close()

	if err := db.Migrate(ctx, testDSN, b2b.New(nil, b2b.Options{}).Migrations(), b2b.ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "the migration could not be applied: %v\n", err)
		return 1
	}

	// The link definition is declared through the PRODUCTION path (the
	// module's Register does the same): that way the table and the
	// cardinality indexes are real.
	testLinks = link.New(testPool, nil)
	for _, def := range service.Definitions() {
		if err := testLinks.Define(ctx, def); err != nil {
			fmt.Fprintf(os.Stderr, "the link definition could not be declared: %v\n", err)
			return 1
		}
	}

	return m.Run()
}

// newService builds a service that works over the real repository and the
// real link service.
func newService(t *testing.T) *service.Service {
	t.Helper()

	svc, err := service.New(service.Options{
		Repo:  repository.New(testPool.Pool()),
		Links: testLinks,
	})
	require.NoError(t, err)
	return svc
}

// newCustomerID produces a customer id that does not collide across tests.
//
// The id carries the customer module's prefix but does NOT COME from that
// module: b2b does not verify that the customer exists (ADR 0001), and the bond
// is a free-form id string.
func newCustomerID() string {
	return fmt.Sprintf("%s%026d", models.CustomerIDPrefix, customerCounter.Add(1))
}

// newCompany creates a company for a test.
func newCompany(t *testing.T, svc *service.Service, period models.SpendingResetPeriod) models.Company {
	t.Helper()

	company, err := svc.CreateCompany(t.Context(), service.CompanyInput{
		Name:                     t.Name(),
		Email:                    "accounting@example.test",
		CurrencyCode:             "TRY",
		SpendingLimitResetPeriod: string(period),
	})
	require.NoError(t, err)
	return company
}

// countRows runs a single-column count query.
func countRows(ctx context.Context, t *testing.T, sql string, args ...any) int64 {
	t.Helper()

	var count int64
	require.NoError(t, testPool.Pool().QueryRow(ctx, sql, args...).Scan(&count))
	return count
}

// serviceOn builds the service on the given database, with the module's links
// declared there the way its Register declares them.
func serviceOn(t *testing.T, pool *db.Pool) *service.Service {
	t.Helper()

	links := link.New(pool, nil)
	for _, def := range service.Definitions() {
		require.NoError(t, links.Define(t.Context(), def))
	}
	svc, err := service.New(service.Options{Repo: repository.New(pool.Pool()), Links: links})
	require.NoError(t, err)

	return svc
}

// countIn runs a count query on the given database.
func countIn(ctx context.Context, t *testing.T, pool *db.Pool, sql string, args ...any) int64 {
	t.Helper()

	var count int64
	require.NoError(t, pool.Pool().QueryRow(ctx, sql, args...).Scan(&count))

	return count
}

// TestTheMigrationCanBeRolledBack verifies that the migration can be applied
// and rolled back (plan Section 8).
//
// The rollback runs on the module's REAL state: company and employee rows are
// left WHERE THEY ARE. The condition is deliberate: the module deletes only
// softly, so even an operator who deleted every record through the API leaves
// the rows in the tables, and the b2b_company_employee -> b2b_company foreign
// key keeps holding. A rollback run on empty tables would leave out exactly the
// state that breaks.
//
// The claim that matters is dirty=false: a down that fails leaves
// golang-migrate's ledger "dirty", and since the composition root migrates each
// module at every start, the module would never come up again.
//
// It runs in a database of its own. In the one this package shares it would
// drop every other test's companies with the schema (D141).
func TestTheMigrationCanBeRolledBack(t *testing.T) {
	ctx := t.Context()
	src := b2b.New(nil, b2b.Options{}).Migrations()
	dsn := testdb.New(t, testDSN, "b2b_migration")
	require.NoError(t, db.Migrate(ctx, dsn, src, b2b.ModuleName))
	pool, err := db.New(ctx, db.DefaultConfig(dsn), nil)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	svc := serviceOn(t, pool)

	company := newCompany(t, svc, models.ResetMonthly)
	_, err = svc.CreateEmployee(ctx, service.EmployeeInput{
		CompanyID: company.ID, CustomerID: newCustomerID(),
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), countIn(ctx, t, pool,
		`SELECT count(*) FROM b2b_company_employee WHERE company_id = $1`, company.ID),
		"the rollback has to run while LIVE records are in place")

	require.NoError(t, db.MigrateDown(ctx, dsn, src, b2b.ModuleName, 0),
		"the down failed, which means the module can never be migrated again")
	for _, table := range moduleTables {
		assert.False(t, testdb.TableExists(t, dsn, table), "%s must not remain after the rollback", table)
	}

	require.NoError(t, db.Migrate(ctx, dsn, src, b2b.ModuleName))
	for _, table := range moduleTables {
		assert.True(t, testdb.TableExists(t, dsn, table), "%s must be applied again", table)
	}

	version, dirty, err := db.Version(ctx, dsn, b2b.ModuleName)
	require.NoError(t, err)
	assert.False(t, dirty, "no migration may be left half-applied")
	assert.Equal(t, uint(1), version)
	assert.Zero(t, countIn(ctx, t, pool, `SELECT count(*) FROM b2b_company`),
		"the schema was dropped and rebuilt, so no company may remain")
}

// TestCompanyAndEmployeeFlowEndToEnd runs the module's admin flow on the real
// database.
func TestCompanyAndEmployeeFlowEndToEnd(t *testing.T) {
	svc := newService(t)
	ctx := t.Context()

	company := newCompany(t, svc, models.ResetMonthly)
	customer := newCustomerID()

	limit := int64(250000)
	employee, err := svc.CreateEmployee(ctx, service.EmployeeInput{
		CompanyID:     company.ID,
		CustomerID:    customer,
		SpendingLimit: &limit,
	})
	require.NoError(t, err)
	assert.Equal(t, customer, employee.CustomerID)

	// The read path has to FILL the customer id from link: it has no column.
	fetched, err := svc.GetEmployee(ctx, employee.ID)
	require.NoError(t, err)
	assert.Equal(t, customer, fetched.CustomerID)
	assert.Zero(t, countRows(ctx, t,
		`SELECT count(*) FROM information_schema.columns
         WHERE table_name = 'b2b_company_employee' AND column_name = 'customer_id'`),
		"the customer bond has to live in the link table, NOT in the schema (Principle 2.2)")

	// The limit has to be removable (the "leave it alone" versus "make it
	// unlimited" distinction).
	updated, err := svc.UpdateEmployee(ctx, employee.ID, service.UpdateEmployeeInput{
		ClearSpendingLimit: true,
	})
	require.NoError(t, err)
	assert.Nil(t, updated.SpendingLimit)

	page, err := svc.ListEmployees(ctx, service.ListEmployeesInput{CompanyID: &company.ID})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, customer, page.Items[0].CustomerID)

	// A soft-deleted record shows up in no read.
	require.NoError(t, svc.DeleteEmployee(ctx, employee.ID))
	_, err = svc.GetEmployee(ctx, employee.ID)
	assert.True(t, errors.IsNotFound(err), "a deleted employee must not be read, got: %v", err)

	page, err = svc.ListEmployees(ctx, service.ListEmployeesInput{CompanyID: &company.ID})
	require.NoError(t, err)
	assert.Empty(t, page.Items)
}

// TestAnotherCustomersCompanyCannotBeRead pins the module's storefront
// invariant on the REAL link table.
//
// Two customers, two companies: each sees only their own company, and a
// customer bound to no company gets a 404, not an empty record. Since the
// storefront has no endpoint that could ask for a company by its id (see the
// api package), this is that surface's only entry point.
func TestAnotherCustomersCompanyCannotBeRead(t *testing.T) {
	svc := newService(t)
	ctx := t.Context()

	acme := newCompany(t, svc, models.ResetMonthly)
	beta := newCompany(t, svc, models.ResetYearly)

	customerA, customerB := newCustomerID(), newCustomerID()
	_, err := svc.CreateEmployee(ctx, service.EmployeeInput{CompanyID: acme.ID, CustomerID: customerA})
	require.NoError(t, err)
	_, err = svc.CreateEmployee(ctx, service.EmployeeInput{CompanyID: beta.ID, CustomerID: customerB})
	require.NoError(t, err)

	membershipA, err := svc.MembershipOfCustomer(ctx, customerA)
	require.NoError(t, err)
	assert.Equal(t, acme.ID, membershipA.Company.ID)
	require.NotNil(t, membershipA.SpendingWindowStart, "a monthly period has to have a window")

	membershipB, err := svc.MembershipOfCustomer(ctx, customerB)
	require.NoError(t, err)
	assert.Equal(t, beta.ID, membershipB.Company.ID,
		"the customer has to see ONLY their own company")

	_, err = svc.MembershipOfCustomer(ctx, newCustomerID())
	assert.True(t, errors.IsNotFound(err),
		"a customer bound to no company has to get a 404, got: %v", err)
}

// TestACustomerCannotJoinASecondCompany verifies that the cardinality lives IN
// THE DATABASE.
//
// The rule could have been kept in the application too ("read first, then
// write"), but it would not hold between two concurrent requests; the link
// table's unique index leaves the race to the database and turns the violation
// into a typed Conflict.
func TestACustomerCannotJoinASecondCompany(t *testing.T) {
	svc := newService(t)
	ctx := t.Context()

	acme := newCompany(t, svc, models.ResetNever)
	beta := newCompany(t, svc, models.ResetNever)
	customer := newCustomerID()

	_, err := svc.CreateEmployee(ctx, service.EmployeeInput{CompanyID: acme.ID, CustomerID: customer})
	require.NoError(t, err)

	_, err = svc.CreateEmployee(ctx, service.EmployeeInput{CompanyID: beta.ID, CustomerID: customer})
	require.True(t, errors.IsConflict(err), "expected class Conflict, got: %v", err)

	// The employee record whose bond could not be established has to have been
	// ROLLED BACK: otherwise a record without a customer would be left in
	// beta's employee list.
	page, err := svc.ListEmployees(ctx, service.ListEmployeesInput{CompanyID: &beta.ID})
	require.NoError(t, err)
	assert.Empty(t, page.Items, "an employee whose bond could not be established must not stay up")
}

// TestDeletingACompanyClearsItsEmployeesAndBonds verifies the three outcomes of
// the deletion decision together: the employees are deleted, the bonds go
// away, and the customer can be hired again.
//
// The third is the easiest to overlook: since the bond is unique, a row that
// was not cleaned up would lock the customer to a single closed company for
// life.
func TestDeletingACompanyClearsItsEmployeesAndBonds(t *testing.T) {
	svc := newService(t)
	ctx := t.Context()

	company := newCompany(t, svc, models.ResetMonthly)
	customer := newCustomerID()
	employee, err := svc.CreateEmployee(ctx, service.EmployeeInput{
		CompanyID: company.ID, CustomerID: customer,
	})
	require.NoError(t, err)

	require.NoError(t, svc.DeleteCompany(ctx, company.ID))

	assert.Equal(t, int64(1), countRows(ctx, t,
		`SELECT count(*) FROM b2b_company_employee WHERE id = $1 AND deleted_at IS NOT NULL`,
		employee.ID), "the employee has to be soft-deleted too")
	assert.Zero(t, countRows(ctx, t,
		`SELECT count(*) FROM link_b2b_employee_customer WHERE from_id = $1`, employee.ID),
		"the customer bond has to be removed")

	_, err = svc.MembershipOfCustomer(ctx, customer)
	assert.True(t, errors.IsNotFound(err), "an employee of a deleted company must not show up in the storefront")

	fresh := newCompany(t, svc, models.ResetMonthly)
	_, err = svc.CreateEmployee(ctx, service.EmployeeInput{CompanyID: fresh.ID, CustomerID: customer})
	assert.NoError(t, err, "a customer whose bond was freed has to be hirable again")
}

// TestConstraintsHoldInTheDatabase verifies that the CHECK constraints are the
// LAST LINE OF DEFENSE.
//
// The writes are made with raw SQL, BYPASSING the service: what is exercised is
// not the application's validation but that, on the day that validation is
// skipped (another code path, a manual intervention, a migration script), the
// data still cannot be corrupted.
func TestConstraintsHoldInTheDatabase(t *testing.T) {
	ctx := t.Context()

	cases := map[string]string{
		"upper-case e-mail": `INSERT INTO b2b_company (id, name, email, currency_code)
             VALUES ('comp_check_1', 'X', 'UPPER@example.test', 'TRY')`,
		"invalid currency": `INSERT INTO b2b_company (id, name, email, currency_code)
             VALUES ('comp_check_2', 'X', 'x@example.test', 'TR')`,
		"undefined reset period": `INSERT INTO b2b_company
             (id, name, email, currency_code, spending_limit_reset_period)
             VALUES ('comp_check_3', 'X', 'x@example.test', 'TRY', 'weekly')`,
		"invalid country code": `INSERT INTO b2b_company
             (id, name, email, currency_code, country_code)
             VALUES ('comp_check_4', 'X', 'x@example.test', 'TRY', 'TUR')`,
	}

	for name, sql := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := testPool.Pool().Exec(ctx, sql)
			assert.Error(t, err, "the constraint should have rejected this write")
		})
	}

	t.Run("negative spending limit", func(t *testing.T) {
		svc := newService(t)
		company := newCompany(t, svc, models.ResetNever)

		_, err := testPool.Pool().Exec(ctx,
			`INSERT INTO b2b_company_employee (id, company_id, spending_limit)
             VALUES ('compemp_check_1', $1, -1)`, company.ID)
		assert.Error(t, err, "a negative limit is not a bound but a meaningless number")
	})

	t.Run("an empty country code is valid", func(t *testing.T) {
		// The address is optional: a record is often opened before the billing
		// address is settled, and the constraint has to ACCEPT that.
		_, err := testPool.Pool().Exec(ctx,
			`INSERT INTO b2b_company (id, name, email, currency_code, country_code)
             VALUES ('comp_check_5', 'X', 'x@example.test', 'TRY', '')`)
		assert.NoError(t, err)
	})
}
