//go:build integration

// The tests in this file need a real PostgreSQL instance (and therefore
// Docker); they are held behind the `integration` build tag so that `make test`
// stays fast. To run them: make test-integration
//
// The unit tests prove the service's DECISIONS against a fake repository. The
// tests here prove the GROUND those decisions stand on: that the migration can
// be rolled back, and that the module's central rules really hold IN THE
// DATABASE — "a registered e-mail is unique but a guest's is not", "a customer
// has one default address at most", "only the OWNER reaches an address", and
// "a soft-deleted row appears in no read".
//
// None of those claims can be tested against the fake repository. The first two
// live in the partial unique indexes themselves and the last two live in the
// WHERE clause of the queries; the fake IMITATES all four in Go, and this file
// is the only thing that shows the imitation matches. A unit test looking at
// the fake would stay green even after the clause fell out of the SQL.
package customer_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/customer"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/repository"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

const postgresImage = "postgres:16-alpine"

// nonASCIIName is a first name carrying a letter outside ASCII (U+015F). It is
// used where a test needs to show that text survives the round trip through the
// database unchanged, and it is spelled as an escape so that the file itself
// stays inside ADR 0012's diacritic lane: the letter is DATA, not language debt.
const nonASCIIName = "Ay\u015fe"

// moduleTables are the tables the module owns; the migration tests use this
// list.
var moduleTables = []string{
	"customer", "customer_group", "customer_group_customer", "customer_address",
}

var (
	// testPool is the pool every test shares.
	testPool *db.Pool
	// testDSN is the connection address the migration calls use.
	testDSN string
	// emailCounter hands out an e-mail no other test is using.
	emailCounter atomic.Int64
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
			fmt.Fprintf(os.Stderr, "the postgres container could not be stopped: %v\n", termErr)
		}
	}()
	if err != nil {
		fmt.Fprintf(os.Stderr, "the postgres container could not be started: %v\n", err)
		return 1
	}

	testDSN, err = ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "the connection address could not be read: %v\n", err)
		return 1
	}

	cfg := db.DefaultConfig(testDSN)
	// The concurrency tests run dozens of goroutines at once and every
	// transaction holds a connection, so the pool is opened wider than default.
	cfg.MaxConns = 24
	testPool, err = db.New(ctx, cfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "the connection pool could not be opened: %v\n", err)
		return 1
	}
	defer testPool.Close()

	if err := db.Migrate(ctx, testDSN, customer.New(nil).Migrations(), customer.ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "the migration could not be applied: %v\n", err)
		return 1
	}

	return m.Run()
}

// newService builds a service running on the real repository.
func newService(t *testing.T) *service.Service {
	t.Helper()

	return service.New(repository.New(testPool.Pool()), service.Options{})
}

// newEmail returns an e-mail address that collides with no other test.
func newEmail(t *testing.T) string {
	t.Helper()

	return fmt.Sprintf("t%d@example.com", emailCounter.Add(1))
}

// newAccount opens a registered customer.
func newAccount(ctx context.Context, t *testing.T, svc *service.Service) models.Customer {
	t.Helper()

	c, err := svc.CreateCustomer(ctx, service.CustomerInput{Email: newEmail(t)})
	require.NoError(t, err)
	return c
}

// validAddress is the input of a valid address, used throughout these tests.
//
// The street and the city carry letters outside ASCII ON PURPOSE: they are what
// shows the text columns round-trip UTF-8 rather than mangling it. They are
// written as escapes for the reason given on [nonASCIIName].
func validAddress() service.AddressInput {
	return service.AddressInput{
		FirstName:   "Ali",
		LastName:    "Veli",
		Address1:    "Atat\u00fcrk Cad. 1", // U+00FC in the street name
		City:        "\u0130stanbul",       // U+0130, the dotted capital I
		CountryCode: "tr",
		PostalCode:  "34000",
	}
}

// nowUTC is the instant identifier generation is given.
func nowUTC() time.Time { return time.Now().UTC() }

// tableExists reports whether the table is in the database.
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

// TestTheMigrationCanBeRolledBack checks that the migration applies and rolls
// back (plan Section 8: up/down pairs, reversible).
func TestTheMigrationCanBeRolledBack(t *testing.T) {
	ctx := context.Background()
	src := customer.New(nil).Migrations()

	for _, table := range moduleTables {
		require.True(t, tableExists(ctx, t, table), "%s must exist to begin with", table)
	}

	require.NoError(t, db.MigrateDown(ctx, testDSN, src, customer.ModuleName, 0))
	for _, table := range moduleTables {
		assert.False(t, tableExists(ctx, t, table), "%s must not survive the rollback", table)
	}

	require.NoError(t, db.Migrate(ctx, testDSN, src, customer.ModuleName))
	for _, table := range moduleTables {
		assert.True(t, tableExists(ctx, t, table), "%s must be applied again", table)
	}

	version, dirty, err := db.Version(ctx, testDSN, customer.ModuleName)
	require.NoError(t, err)
	assert.False(t, dirty, "no migration may be left half-applied")
	assert.Equal(t, uint(2), version)
}

// TestNoForeignKeyLeavesTheModule checks that EVERY foreign key on the module's
// tables points back at the module's own tables (Principle 2.2).
func TestNoForeignKeyLeavesTheModule(t *testing.T) {
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
	assert.Positive(t, count, "foreign keys inside the module must be used")
}

// TestAnAccountEmailIsUniqueInTheDatabase checks that the uniqueness really
// lives in the partial unique index.
//
// The service does not check this rule ITSELF; if the index is missing, or its
// WHERE clause is wrong, the test falls over here.
func TestAnAccountEmailIsUniqueInTheDatabase(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	email := newEmail(t)
	_, err := svc.CreateCustomer(ctx, service.CustomerInput{Email: email})
	require.NoError(t, err)

	_, err = svc.CreateCustomer(ctx, service.CustomerInput{Email: email})
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, repository.CodeEmailTaken, errors.CodeOf(err))
}

// TestManyGuestsMayShareOneEmail checks the guest scenario of the Phase 5 DoD
// against the REAL index.
//
// If the partial index's WHERE has_account clause falls away (that is, if the
// index covers every row) this test fails; it is the gate that keeps the rule.
func TestManyGuestsMayShareOneEmail(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	email := newEmail(t)
	var ids []string
	for range 3 {
		guest, err := svc.RegisterGuest(ctx, service.CustomerInput{Email: email})
		require.NoError(t, err, "a guest registration on the same e-mail must not be refused")
		ids = append(ids, guest.ID)
	}
	assert.Len(t, ids, 3)

	// ONE account may be opened on that e-mail too; the guest rows do not block it.
	account, err := svc.CreateCustomer(ctx, service.CustomerInput{Email: email})
	require.NoError(t, err, "guest rows must not stop an account from being opened")

	found, err := svc.GetCustomerByEmail(ctx, email)
	require.NoError(t, err)
	assert.Equal(t, account.ID, found.ID, "a lookup by e-mail must find the ACCOUNT")

	// The listing sees the guests as well; there are four rows in total.
	page, err := svc.ListCustomers(ctx, service.ListCustomersInput{Email: &email})
	require.NoError(t, err)
	assert.Equal(t, int64(4), page.Count)
}

// TestAGuestBecomesAnAccount checks the conversion, and its conflict, on the
// real database.
func TestAGuestBecomesAnAccount(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	t.Run("success", func(t *testing.T) {
		guest, err := svc.RegisterGuest(ctx, service.CustomerInput{Email: newEmail(t)})
		require.NoError(t, err)

		require.NoError(t, svc.ConvertGuestToAccount(ctx, guest.ID))

		stored, err := svc.GetCustomer(ctx, guest.ID)
		require.NoError(t, err)
		assert.True(t, stored.HasAccount)

		// It can be found by e-mail from now on.
		found, err := svc.GetCustomerByEmail(ctx, stored.Email)
		require.NoError(t, err)
		assert.Equal(t, guest.ID, found.ID)
	})

	t.Run("conflict", func(t *testing.T) {
		email := newEmail(t)
		_, err := svc.CreateCustomer(ctx, service.CustomerInput{Email: email})
		require.NoError(t, err)
		guest, err := svc.RegisterGuest(ctx, service.CustomerInput{Email: email})
		require.NoError(t, err)

		err = svc.ConvertGuestToAccount(ctx, guest.ID)
		require.Error(t, err)
		assert.Equal(t, errors.KindConflict, errors.KindOf(err))

		stored, getErr := svc.GetCustomer(ctx, guest.ID)
		require.NoError(t, getErr)
		assert.False(t, stored.HasAccount, "a conflicting conversion must be ROLLED BACK")
	})

	t.Run("already an account", func(t *testing.T) {
		account := newAccount(ctx, t, svc)

		err := svc.ConvertGuestToAccount(ctx, account.ID)
		require.Error(t, err)
		assert.Equal(t, errors.KindConflict, errors.KindOf(err))
		assert.Equal(t, repository.CodeAlreadyAccount, errors.CodeOf(err))
	})
}

// TestConcurrentGuestConversionsLeaveExactlyOneAccount checks that when two
// guests on the same e-mail are converted at the same moment, exactly ONE wins.
//
// The pre-check alone would not have been enough: both transactions could see
// "the e-mail is free" and both could write. What draws the line is the partial
// unique index, and this test probes it with real concurrency.
func TestConcurrentGuestConversionsLeaveExactlyOneAccount(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	email := newEmail(t)
	const racers = 8

	ids := make([]string, 0, racers)
	for range racers {
		guest, err := svc.RegisterGuest(ctx, service.CustomerInput{Email: email})
		require.NoError(t, err)
		ids = append(ids, guest.ID)
	}

	var (
		wg         sync.WaitGroup
		succeeded  atomic.Int64
		conflicted atomic.Int64
	)
	for _, id := range ids {
		wg.Add(1)
		go func(customerID string) {
			defer wg.Done()
			switch err := svc.ConvertGuestToAccount(ctx, customerID); {
			case err == nil:
				succeeded.Add(1)
			case errors.IsConflict(err):
				conflicted.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}(id)
	}
	wg.Wait()

	assert.Equal(t, int64(1), succeeded.Load(), "exactly one conversion must win")
	assert.Equal(t, int64(racers-1), conflicted.Load(), "the rest must get a conflict")

	page, err := svc.ListCustomers(ctx, service.ListCustomersInput{Email: &email})
	require.NoError(t, err)
	var accountCount int
	for _, c := range page.Items {
		if c.HasAccount {
			accountCount++
		}
	}
	assert.Equal(t, 1, accountCount, "one account at most may hold that e-mail")
}

// TestADeletedAccountsEmailBecomesFreeAgain checks that a soft delete takes the
// row out of the index's reach.
//
// If the partial index's deleted_at IS NULL clause falls away, a deleted
// account's e-mail would stay occupied forever.
func TestADeletedAccountsEmailBecomesFreeAgain(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	email := newEmail(t)
	first, err := svc.CreateCustomer(ctx, service.CustomerInput{Email: email})
	require.NoError(t, err)

	require.NoError(t, svc.DeleteCustomer(ctx, first.ID))

	second, err := svc.CreateCustomer(ctx, service.CustomerInput{Email: email})
	require.NoError(t, err, "a deleted account's e-mail must be usable again")
	assert.NotEqual(t, first.ID, second.ID)
}

// TestADeletedCustomerAppearsInNoRead checks that the soft delete is really
// filtered out in the REAL queries.
//
// The rule (plan Section 8) reads "deletion is SOFT, reads filter on
// deleted_at IS NULL", and the only thing holding it is the SQL's WHERE clause:
// the row STAYS in the table and that filter is the whole of its invisibility.
// A unit test cannot prove this — the fake repository applies the filter itself
// and so only confirms its own rule. If the filter fell away, deleted customers
// would come back in the admin listing, in the lookup by e-mail and in the
// Query provider's output (that is, in cart/order expansions).
//
// The test touches each of the FIVE queries that read the customer table
// separately: GetCustomer, GetAccountByEmail, ListCustomers/CountCustomers,
// ListCustomersByIDs and GetCustomerForUpdate.
func TestADeletedCustomerAppearsInNoRead(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	provider := service.NewQueryProvider(svc)

	email := newEmail(t)
	cust, err := svc.CreateCustomer(ctx, service.CustomerInput{Email: email})
	require.NoError(t, err)

	require.NoError(t, svc.DeleteCustomer(ctx, cust.ID))

	// A soft delete does NOT remove the row; the rest of the test is only
	// meaningful while the row is still standing.
	var rowsLeft int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM customer WHERE id = $1`, cust.ID).Scan(&rowsLeft))
	require.Equal(t, 1, rowsLeft, "a soft delete must leave the row in the table")

	// GetCustomer.
	_, err = svc.GetCustomer(ctx, cust.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"a deleted customer must not be readable by id")

	// GetAccountByEmail.
	_, err = svc.GetCustomerByEmail(ctx, email)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"a deleted account must not be found by e-mail")

	// ListCustomers + CountCustomers.
	page, err := svc.ListCustomers(ctx, service.ListCustomersInput{Email: &email})
	require.NoError(t, err)
	assert.Zero(t, page.Count, "a deleted customer must not appear in the count")
	assert.Empty(t, page.Items, "a deleted customer must not appear in the list")

	// ListCustomersByIDs (the Query provider's batched read path).
	records, err := provider.FetchByIDs(ctx, []string{cust.ID}, nil)
	require.NoError(t, err)
	assert.Empty(t, records, "a deleted customer must not appear in the Query provider")

	// GetCustomerForUpdate: the address write path reads the customer under a lock.
	_, err = svc.CreateAddress(ctx, cust.ID, validAddress())
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"no address may be written under a deleted customer")

	// The write paths carry the same filter.
	newFirstName := nonASCIIName
	_, err = svc.UpdateCustomer(ctx, cust.ID, service.UpdateCustomerInput{FirstName: &newFirstName})
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"a deleted customer must not be updatable")

	assert.Equal(t, errors.KindNotFound, errors.KindOf(svc.DeleteCustomer(ctx, cust.ID)),
		"a deleted customer must not be deletable a second time")
}

// TestADeletedGuestCannotBeConverted checks that the soft-delete filter also
// stands on the guest conversion path.
//
// The conversion reads the customer under a lock and the upgrade query carries
// the same filter; if the filter fell away a deleted guest could be turned into
// an account, and a deleted row would then occupy the e-mail's uniqueness.
func TestADeletedGuestCannotBeConverted(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	guest, err := svc.RegisterGuest(ctx, service.CustomerInput{Email: newEmail(t)})
	require.NoError(t, err)
	require.NoError(t, svc.DeleteCustomer(ctx, guest.ID))

	err = svc.ConvertGuestToAccount(ctx, guest.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"a deleted guest must not be convertible to an account")
}

// TestTheEmailCheckConstraintRefusesUpperCase checks that the normalisation is
// enforced in the database as well.
//
// Even if the service is bypassed (say by a bulk import written later), an
// e-mail with a capital letter cannot enter the table; if it could, the partial
// unique index would accept the same account twice.
func TestTheEmailCheckConstraintRefusesUpperCase(t *testing.T) {
	ctx := context.Background()

	_, err := testPool.Pool().Exec(ctx,
		`INSERT INTO customer (id, email, has_account) VALUES ($1, $2, TRUE)`,
		models.NewCustomerID(nowUTC()), "UPPER@EXAMPLE.COM")
	require.Error(t, err, "an e-mail with capitals must hit the CHECK constraint")
	assert.Contains(t, err.Error(), "customer_email_check")
}

// TestTheOneDefaultAddressRuleLivesInTheDatabase checks that "one default per
// customer" is in the database and NOT in the application.
//
// The test deliberately SKIPS the service and marks two rows with plain SQL: if
// the rule lived in the application alone this would pass, and two default
// shipping addresses would sit side by side in the table.
func TestTheOneDefaultAddressRuleLivesInTheDatabase(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	cust := newAccount(ctx, t, svc)

	first, err := svc.CreateAddress(ctx, cust.ID, validAddress())
	require.NoError(t, err)
	second, err := svc.CreateAddress(ctx, cust.ID, validAddress())
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx,
		`UPDATE customer_address SET is_default_shipping = TRUE WHERE id = $1`, first.ID)
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx,
		`UPDATE customer_address SET is_default_shipping = TRUE WHERE id = $1`, second.ID)
	require.Error(t, err, "the database must refuse a second default shipping address")
	assert.Contains(t, err.Error(), "customer_address_default_shipping_uniq")

	// The billing side is guarded the same way.
	_, err = testPool.Pool().Exec(ctx,
		`UPDATE customer_address SET is_default_billing = TRUE WHERE id IN ($1, $2)`, first.ID, second.ID)
	require.Error(t, err, "the database must refuse a second default billing address")
	assert.Contains(t, err.Error(), "customer_address_default_billing_uniq")
}

// TestTheServiceClearsTheOldDefaultShippingAddress checks that the service
// satisfies the constraint by clearing the previous mark.
func TestTheServiceClearsTheOldDefaultShippingAddress(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	cust := newAccount(ctx, t, svc)

	first, err := svc.CreateAddress(ctx, cust.ID, validAddress())
	require.NoError(t, err)
	second, err := svc.CreateAddress(ctx, cust.ID, validAddress())
	require.NoError(t, err)

	_, err = svc.SetDefaultShippingAddress(ctx, cust.ID, first.ID)
	require.NoError(t, err)
	_, err = svc.SetDefaultShippingAddress(ctx, cust.ID, second.ID)
	require.NoError(t, err, "the new default must be written after clearing the old one")

	assert.Equal(t, 1, defaultCount(ctx, t, cust.ID, "is_default_shipping"))

	old, err := svc.GetAddress(ctx, cust.ID, first.ID)
	require.NoError(t, err)
	assert.False(t, old.IsDefaultShipping)
}

// TestConcurrentDefaultAddressAssignmentsLeaveOneDefault checks that concurrent
// assignments against the same customer finish without deadlocking and leave a
// single default behind.
//
// If the lock order (the customer row first, then the addresses) were not
// fixed, the transactions would wait on each other in opposite orders and the
// database would kill some of them with a deadlock; the claim can only be
// probed with real goroutines.
func TestConcurrentDefaultAddressAssignmentsLeaveOneDefault(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	cust := newAccount(ctx, t, svc)

	const racers = 8
	addresses := make([]string, 0, racers)
	for range racers {
		a, err := svc.CreateAddress(ctx, cust.ID, validAddress())
		require.NoError(t, err)
		addresses = append(addresses, a.ID)
	}

	var wg sync.WaitGroup
	for _, id := range addresses {
		wg.Add(1)
		go func(addressID string) {
			defer wg.Done()
			if _, err := svc.SetDefaultShippingAddress(ctx, cust.ID, addressID); err != nil {
				t.Errorf("a concurrent default assignment failed: %v", err)
			}
		}(id)
	}
	wg.Wait()

	assert.Equal(t, 1, defaultCount(ctx, t, cust.ID, "is_default_shipping"),
		"one default must remain after the concurrent assignments")
}

// TestTheAddressLifecycle checks creation, update, listing and soft deletion
// end to end.
func TestTheAddressLifecycle(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	cust := newAccount(ctx, t, svc)

	addr, err := svc.CreateAddress(ctx, cust.ID, validAddress())
	require.NoError(t, err)
	assert.Equal(t, "TR", addr.CountryCode)
	assert.Equal(t, validAddress().City, addr.City, "the city must round-trip unchanged")
	assert.False(t, addr.CreatedAt.IsZero(), "created_at must come from the database")
	assert.Equal(t, "UTC", addr.CreatedAt.Location().String(), "the time must be UTC")

	newCity := "Ankara"
	updated, err := svc.UpdateAddress(ctx, cust.ID, addr.ID,
		service.UpdateAddressInput{City: &newCity})
	require.NoError(t, err)
	assert.Equal(t, "Ankara", updated.City)
	assert.Equal(t, addr.Address1, updated.Address1, "a field that was not given must be kept")

	require.NoError(t, svc.DeleteAddress(ctx, cust.ID, addr.ID))

	_, err = svc.GetAddress(ctx, cust.ID, addr.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))

	remaining, err := svc.ListAddresses(ctx, cust.ID)
	require.NoError(t, err)
	assert.Empty(t, remaining, "a soft-deleted address must not appear in the list")
}

// TestAnotherCustomersAddressIsUnreachable checks that an address's ownership
// is enforced in the REAL query.
//
// The ownership check is the customer_id equality in the WHERE clause of every
// query that touches an address (see queries/customer_address.sql). If the
// clause falls away, ANYONE who knows an address's id can read, update, delete
// and default someone else's address.
//
// ~~Because the store endpoints are unguarded until Phase 8 and the customer id
// comes from the path parameter, this clause is currently the ONLY barrier~~
// **2026-09-08: no longer the only one, but the innermost.** ADR 0043 put an
// identity check on the storefront endpoints; that check answers "is this
// request that customer" and it lives in the HTTP layer. The clause here
// answers a different question: what happens when a caller whose identity is
// correct sends a request carrying SOMEONE ELSE'S address id. The two checks
// live in two separate layers and neither stands in for the other — the admin
// endpoints do not pass through the identity check at all and rest on this
// clause alone.
//
// A unit test cannot prove this claim: the fake repository filters ownership
// itself and so only confirms its own rule. The error kind is not enough on its
// own either — a wrong query could have CHANGED the row without returning an
// error, so the address's raw state is read separately, bypassing the service.
func TestAnotherCustomersAddressIsUnreachable(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	owner := newAccount(ctx, t, svc)
	stranger := newAccount(ctx, t, svc)

	addr, err := svc.CreateAddress(ctx, owner.ID, validAddress())
	require.NoError(t, err)

	_, err = svc.GetAddress(ctx, stranger.ID, addr.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"someone else's address must not be readable")

	otherCity := "Ankara"
	_, err = svc.UpdateAddress(ctx, stranger.ID, addr.ID,
		service.UpdateAddressInput{City: &otherCity})
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"someone else's address must not be updatable")

	_, err = svc.SetDefaultShippingAddress(ctx, stranger.ID, addr.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"someone else's address must not become a default shipping address")

	_, err = svc.SetDefaultBillingAddress(ctx, stranger.ID, addr.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"someone else's address must not become a default billing address")

	err = svc.DeleteAddress(ctx, stranger.ID, addr.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"someone else's address must not be deletable")

	// The real proof: the row is standing in the table EXACTLY as it was.
	state := addressState(ctx, t, addr.ID)
	assert.Equal(t, validAddress().City, state.city, "the stranger's update must not be written")
	assert.False(t, state.defaultShipping, "a stranger must not set the default shipping mark")
	assert.False(t, state.defaultBilling, "a stranger must not set the default billing mark")
	assert.False(t, state.deleted, "a stranger must not delete the address")

	// The owner MUST reach their own address; otherwise the NotFounds above
	// would be coming from a wholly broken query rather than from ownership.
	own, err := svc.GetAddress(ctx, owner.ID, addr.ID)
	require.NoError(t, err, "the owner must be able to read their own address")
	assert.Equal(t, addr.ID, own.ID)
}

// TestDeletingACustomerDeletesTheirAddresses checks that the soft delete covers
// the addresses too.
//
// The foreign key's ON DELETE CASCADE only fires on a REAL delete; a soft
// delete is an UPDATE and so does not carry the addresses away by itself.
func TestDeletingACustomerDeletesTheirAddresses(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	cust := newAccount(ctx, t, svc)

	_, err := svc.CreateAddress(ctx, cust.ID, validAddress())
	require.NoError(t, err)

	require.NoError(t, svc.DeleteCustomer(ctx, cust.ID))

	var live int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM customer_address WHERE customer_id = $1 AND deleted_at IS NULL`,
		cust.ID).Scan(&live))
	assert.Zero(t, live, "a deleted customer must have no live address left")
}

// TestGroupMembership checks the group lifecycle and the idempotence of
// membership.
func TestGroupMembership(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	cust := newAccount(ctx, t, svc)
	group, err := svc.CreateGroup(ctx, service.GroupInput{
		Name:     "VIP-" + models.NewCustomerGroupID(nowUTC()),
		Metadata: map[string]any{"discount": "10"},
	})
	require.NoError(t, err)
	assert.Equal(t, "10", group.Metadata["discount"], "the metadata must come back out of jsonb")

	require.NoError(t, svc.AddToGroup(ctx, cust.ID, group.ID))
	require.NoError(t, svc.AddToGroup(ctx, cust.ID, group.ID), "a second add must not error")

	groups, err := svc.ListGroupsOf(ctx, cust.ID)
	require.NoError(t, err)
	require.Len(t, groups, 1, "the membership must not be duplicated")

	// A second group cannot take the same name.
	_, err = svc.CreateGroup(ctx, service.GroupInput{Name: group.Name})
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))

	require.NoError(t, svc.RemoveFromGroup(ctx, cust.ID, group.ID))
	err = svc.RemoveFromGroup(ctx, cust.ID, group.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"removing a membership that is not there must be NotFound")
}

// TestUpdatingAndDeletingAGroup checks that a group's name can be corrected and
// that the soft delete produces real invisibility IN THE DATABASE.
//
// The membership rows are LEFT BEHIND on deletion; the only thing hiding a
// deleted group is the deleted_at IS NULL filter on every query that reads a
// group. That is also why the customer list's group_id filter looks at the LIVE
// group the membership points at rather than at the membership row — had it
// looked only at the membership, a deleted group's members would go on being
// listed.
func TestUpdatingAndDeletingAGroup(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	provider := service.NewQueryProvider(svc)

	member := newAccount(ctx, t, svc)
	name := "Segment-" + models.NewCustomerGroupID(nowUTC())
	group, err := svc.CreateGroup(ctx, service.GroupInput{Name: name})
	require.NoError(t, err)
	require.NoError(t, svc.AddToGroup(ctx, member.ID, group.ID))

	// The name can be corrected; the partial unique index accepts the new one.
	corrected := name + "-corrected"
	updated, err := svc.UpdateGroup(ctx, group.ID, service.UpdateGroupInput{
		Name:     &corrected,
		Metadata: map[string]any{"discount": "10"},
	})
	require.NoError(t, err)
	assert.Equal(t, corrected, updated.Name)
	assert.Equal(t, "10", updated.Metadata["discount"], "the metadata must come back out of jsonb")
	assert.False(t, updated.UpdatedAt.Before(updated.CreatedAt), "updated_at must move forward")

	// Another live group's name cannot be taken; the rule is in the index.
	other, err := svc.CreateGroup(ctx, service.GroupInput{
		Name: "Segment-" + models.NewCustomerGroupID(nowUTC()),
	})
	require.NoError(t, err)
	_, err = svc.UpdateGroup(ctx, other.ID, service.UpdateGroupInput{Name: &corrected})
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, repository.CodeGroupNameTaken, errors.CodeOf(err))

	require.NoError(t, svc.DeleteGroup(ctx, group.ID))

	// The membership row is STANDING where it was; the filter is the whole of
	// its invisibility.
	var membershipRows int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM customer_group_customer WHERE customer_group_id = $1`,
		group.ID).Scan(&membershipRows))
	require.Equal(t, 1, membershipRows, "deleting a group must leave the membership row")

	_, err = svc.GetGroup(ctx, group.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err), "a deleted group must not be readable")

	groups, err := svc.ListGroupsOf(ctx, member.ID)
	require.NoError(t, err)
	assert.Empty(t, groups, "a deleted group must not appear among the customer's groups")

	records, err := provider.FetchByIDs(ctx, []string{member.ID}, nil)
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Empty(t, records[0]["group_ids"], "a deleted group must not reach the pricing context")

	page, err := svc.ListCustomers(ctx, service.ListCustomersInput{GroupID: &group.ID})
	require.NoError(t, err)
	assert.Zero(t, page.Count, "the members of a deleted group must be filtered out of the listing")
	assert.Empty(t, page.Items)

	assert.Equal(t, errors.KindNotFound, errors.KindOf(svc.AddToGroup(ctx, member.ID, group.ID)),
		"no member may be added to a deleted group")
	assert.Equal(t, errors.KindNotFound, errors.KindOf(svc.DeleteGroup(ctx, group.ID)),
		"a deleted group must not be deletable a second time")

	_, err = svc.UpdateGroup(ctx, group.ID, service.UpdateGroupInput{Name: &name})
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err), "a deleted group must not be updatable")

	// The name is free again because it left the index's reach.
	_, err = svc.CreateGroup(ctx, service.GroupInput{Name: corrected})
	require.NoError(t, err, "a deleted group's name must be usable again")
}

// TestTheQueryProviderReturnsGroupsInOneRound checks that the provider returns
// the customer together with their group ids, in a SINGLE round.
//
// pricing's rule context will ask for that field; had the group ids come in a
// second round, every customer would have cost another query (ADR 0004).
func TestTheQueryProviderReturnsGroupsInOneRound(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	provider := service.NewQueryProvider(svc)

	assert.Equal(t, "customer", provider.Entity())
	assert.Equal(t, customer.ProviderName, provider.Entity()+query.ProviderSuffix)

	group, err := svc.CreateGroup(ctx, service.GroupInput{
		Name: "Segment-" + models.NewCustomerGroupID(nowUTC()),
	})
	require.NoError(t, err)

	member := newAccount(ctx, t, svc)
	require.NoError(t, svc.AddToGroup(ctx, member.ID, group.ID))
	ungrouped := newAccount(ctx, t, svc)

	records, err := provider.FetchByIDs(ctx, []string{member.ID, ungrouped.ID}, nil)
	require.NoError(t, err)
	require.Len(t, records, 2)

	byID := map[string]query.Record{}
	for _, record := range records {
		id, ok := record[query.IDField].(string)
		require.True(t, ok)
		byID[id] = record
	}

	assert.Equal(t, []string{group.ID}, byID[member.ID]["group_ids"])
	assert.Empty(t, byID[ungrouped.ID]["group_ids"])
	assert.Equal(t, member.Email, byID[member.ID]["email"])
	assert.Equal(t, true, byID[member.ID]["has_account"])

	// An id that is not found is not an error; no record comes back.
	records, err = provider.FetchByIDs(ctx, []string{models.NewCustomerID(nowUTC())}, nil)
	require.NoError(t, err)
	assert.Empty(t, records)
}

// TestMetadataRoundTrips checks that metadata is written to jsonb and read back.
func TestMetadataRoundTrips(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	cust, err := svc.CreateCustomer(ctx, service.CustomerInput{
		Email:    newEmail(t),
		Metadata: map[string]any{"source": "web", "points": float64(12)},
	})
	require.NoError(t, err)

	stored, err := svc.GetCustomer(ctx, cust.ID)
	require.NoError(t, err)
	assert.Equal(t, "web", stored.Metadata["source"])
	assert.InDelta(t, 12, stored.Metadata["points"], 0.0001)

	// An update that does not carry metadata does NOT touch the column.
	newFirstName := nonASCIIName
	updated, err := svc.UpdateCustomer(ctx, cust.ID, service.UpdateCustomerInput{FirstName: &newFirstName})
	require.NoError(t, err)
	assert.Equal(t, nonASCIIName, updated.FirstName, "the name must round-trip unchanged")
	assert.Equal(t, "web", updated.Metadata["source"], "metadata that was not given must be kept")
}

// addressRow is an address's raw state as it stands in the table.
type addressRow struct {
	// city is the city column.
	city string
	// defaultShipping is the is_default_shipping column.
	defaultShipping bool
	// defaultBilling is the is_default_billing column.
	defaultBilling bool
	// deleted says whether the deleted_at column is filled in.
	deleted bool
}

// addressState reads the address straight out of the table, BYPASSING THE
// SERVICE.
//
// The error kind the service returns is not enough on its own: a query that
// does not filter on ownership could have changed the row without erroring at
// all. The raw read is what makes that difference visible.
func addressState(ctx context.Context, t *testing.T, addressID string) addressRow {
	t.Helper()

	var row addressRow
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT city, is_default_shipping, is_default_billing, deleted_at IS NOT NULL
         FROM customer_address WHERE id = $1`, addressID).
		Scan(&row.city, &row.defaultShipping, &row.defaultBilling, &row.deleted))
	return row
}

// defaultCount returns how many live addresses of the customer are marked in
// the given column.
func defaultCount(ctx context.Context, t *testing.T, customerID, column string) int {
	t.Helper()

	// A column name cannot be a SQL parameter, so only the test's own constants
	// are accepted.
	var stmt string
	switch column {
	case "is_default_shipping":
		stmt = `SELECT count(*) FROM customer_address
                 WHERE customer_id = $1 AND deleted_at IS NULL AND is_default_shipping`
	case "is_default_billing":
		stmt = `SELECT count(*) FROM customer_address
                 WHERE customer_id = $1 AND deleted_at IS NULL AND is_default_billing`
	default:
		t.Fatalf("unknown column: %s", column)
	}

	var n int
	require.NoError(t, testPool.Pool().QueryRow(ctx, stmt, customerID).Scan(&n))
	return n
}

// blockedRequestCount returns how many requests the given session is BLOCKING.
//
// Narrowing it to a known blocker with pg_blocking_pids is not optional: the
// condition "somebody in the database is waiting on a lock" is also satisfied
// by another test's session, and in that case the wait assertion below races
// ahead before the request under test has run its first statement. The test
// stays green and measures nothing.
func blockedRequestCount(ctx context.Context, t *testing.T, blockerPID int32) int64 {
	t.Helper()

	var n int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM pg_stat_activity
         WHERE datname = current_database()
           AND wait_event_type = 'Lock'
           AND $1 = ANY(pg_blocking_pids(pid))`, blockerPID).Scan(&n))
	return n
}

// requireBlockedRequest checks that the given session really is holding a
// request up.
//
// It looks at the WAIT STATE rather than sleeping: a fixed sleep either wakes
// early on a slow machine and makes the test flaky, or adds dead waiting to
// every run.
func requireBlockedRequest(ctx context.Context, t *testing.T, blockerPID int32) {
	t.Helper()

	require.Eventually(t, func() bool {
		return blockedRequestCount(ctx, t, blockerPID) > 0
	}, 10*time.Second, 10*time.Millisecond,
		"the write under test should have been waiting on this session's lock")
}

// TestACustomerBeingDeletedCannotTakeANewAddress checks GetCustomerForUpdate's
// CLAIM.
//
// queries/customer.sql says this: "after the FOR UPDATE lock is taken it
// re-evaluates the WHERE clause, so a delete that arrived in between shows up
// as 'no such row'." Until now no test held that. [TestADeletedCustomerAppearsInNoRead]
// deletes FIRST and then reads, and a plain read refuses that case too. What is
// interesting is the delete arriving WHILE the write is deciding, and only a
// rival transaction can produce it.
//
// The waiting is NOT itself the proof: the address INSERT's foreign key locks
// the same row with KEY SHARE and would have waited too. What carries the proof
// is being refused AFTER waking — the foreign key would find the row physically
// in place and accept the write, because the delete is SOFT.
func TestACustomerBeingDeletedCannotTakeANewAddress(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	cust := newAccount(ctx, t, svc)

	conn, err := testPool.Pool().Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	var blockerPID int32
	require.NoError(t, tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID))

	var locked string
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT id FROM customer WHERE id = $1 FOR UPDATE`, cust.ID).Scan(&locked))

	result := make(chan error, 1)
	go func() {
		_, addrErr := svc.CreateAddress(ctx, cust.ID, validAddress())
		result <- addrErr
	}()

	requireBlockedRequest(ctx, t, blockerPID)

	// The blocking transaction performs the delete ITSELF, so that the address
	// write can only wake into a world where the customer is already gone. Done
	// from a third session it would queue behind the same lock, and the order of
	// the two would be left to the scheduler.
	_, err = tx.Exec(ctx,
		`UPDATE customer SET deleted_at = $2, updated_at = $2 WHERE id = $1`,
		cust.ID, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	var addrErr error
	select {
	case addrErr = <-result:
	case <-time.After(15 * time.Second):
		t.Fatal("the waiting address write did not finish in time")
	}

	if assert.Error(t, addrErr, "no address may be added to a customer being deleted") {
		assert.True(t, errors.IsNotFound(addrErr), "kind: %s", errors.KindOf(addrErr))
	}
	var addressCount int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM customer_address WHERE customer_id = $1`, cust.ID).Scan(&addressCount))
	assert.Equal(t, int64(0), addressCount,
		"no live address may be left under a deleted customer")
}

// TestSettingTheDefaultBillingAddressClearsTheOld checks that the assignment on
// the billing side writes by clearing the OLD mark.
//
// The shipping side was pinned by [TestTheServiceClearsTheOldDefaultShippingAddress];
// the billing side applies the SAME rule through TWO SEPARATE queries
// (ClearDefaultBilling and MarkDefaultBilling), and until now those two queries
// had NEVER RUN in any test — which is to say the billing half of the rule
// rested on the code merely resembling the shipping half.
//
// If the clearing step is skipped on the billing side, the partial unique index
// refuses the second mark, and the result is this: a customer may choose a
// billing address ONCE, gets a "conflict" on their second choice, and can never
// change it again. Because the constraint is in the database the claim can only
// be probed against a real one; the fake repository would accept both marks
// side by side.
func TestSettingTheDefaultBillingAddressClearsTheOld(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	cust := newAccount(ctx, t, svc)

	first, err := svc.CreateAddress(ctx, cust.ID, validAddress())
	require.NoError(t, err)
	second, err := svc.CreateAddress(ctx, cust.ID, validAddress())
	require.NoError(t, err)

	marked, err := svc.SetDefaultBillingAddress(ctx, cust.ID, first.ID)
	require.NoError(t, err)
	assert.True(t, marked.IsDefaultBilling, "the address that was marked must come back marked")

	_, err = svc.SetDefaultBillingAddress(ctx, cust.ID, second.ID)
	require.NoError(t, err, "the new billing address must be written after clearing the old one")

	assert.Equal(t, 1, defaultCount(ctx, t, cust.ID, "is_default_billing"),
		"the customer must be left with a single default billing address")

	old, err := svc.GetAddress(ctx, cust.ID, first.ID)
	require.NoError(t, err)
	assert.False(t, old.IsDefaultBilling, "the old billing address's mark must be taken off")

	fresh, err := svc.GetAddress(ctx, cust.ID, second.ID)
	require.NoError(t, err)
	assert.True(t, fresh.IsDefaultBilling, "the new address must be the default billing address")
	assert.False(t, fresh.IsDefaultShipping,
		"the billing mark does NOT CARRY the shipping mark; the two fields are chosen separately")
}

// TestAddingAnAddressAsDefaultBillingClearsTheOld checks that a NEW address
// added as the default billing address clears the old mark.
//
// This is the mark's second write path and it is probed separately: most of the
// time a customer does not "choose" a billing address through a dedicated
// endpoint but ADDS the new address as the default straight from the checkout
// step. If the clearing is skipped on that path, the add request itself hits
// the partial unique index — that is, the customer cannot save their new
// billing address, and what they see is a conflict error that looks as though
// something were wrong with the address.
func TestAddingAnAddressAsDefaultBillingClearsTheOld(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	cust := newAccount(ctx, t, svc)

	input := validAddress()
	input.IsDefaultBilling = true

	first, err := svc.CreateAddress(ctx, cust.ID, input)
	require.NoError(t, err)
	assert.True(t, first.IsDefaultBilling, "an address added marked must come back marked")

	second, err := svc.CreateAddress(ctx, cust.ID, input)
	require.NoError(t, err,
		"a second address added as the default billing address must be ACCEPTED; the old mark is cleared during the add")
	assert.True(t, second.IsDefaultBilling)

	assert.Equal(t, 1, defaultCount(ctx, t, cust.ID, "is_default_billing"),
		"a single default billing address must remain after the add as well")

	old, err := svc.GetAddress(ctx, cust.ID, first.ID)
	require.NoError(t, err)
	assert.False(t, old.IsDefaultBilling, "the old billing address's mark must be taken off")
}
