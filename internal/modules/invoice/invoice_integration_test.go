//go:build integration

// The tests in this file need a real PostgreSQL instance (and therefore
// Docker); they are separated behind the `integration` tag so `make test` stays
// fast. To run them: make test-integration
//
// # What only a real database can show here
//
// The module's whole reason to exist is one sentence: within a series the
// numbers run without a gap and without a repeat. Neither half of it can be
// shown with a fake.
//
// A fake has no transaction, so it cannot show that a FAILED issue gives its
// number back — which is the half the design is actually built around. And a
// fake serializes on a mutex, so it cannot show that two concurrent issues on
// the same series take different numbers for the right reason: the row lock the
// UPDATE takes, held to commit.
package invoice_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/modules/invoice"
	"github.com/bdrtr/gobit/internal/modules/invoice/models"
	"github.com/bdrtr/gobit/internal/modules/invoice/repository"
	"github.com/bdrtr/gobit/internal/modules/invoice/service"
)

// postgresImage is the database the tests run against.
const postgresImage = "postgres:16-alpine"

// moduleTables are the tables the module owns.
var moduleTables = []string{"invoices", "invoice_lines", "invoice_series"}

// testPool is the pool every test shares.
var testPool *db.Pool

// testDSN is the connection string the migration calls use.
var testDSN string

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres brings up one Postgres container and runs every test against
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
		fmt.Fprintf(os.Stderr, "the connection string could not be read: %v\n", err)

		return 1
	}

	cfg := db.DefaultConfig(testDSN)
	// The concurrency test runs dozens of goroutines at once and each of them
	// holds a connection for the length of its transaction, so the pool is
	// opened wider than the default.
	cfg.MaxConns = 24

	testPool, err = db.New(ctx, cfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "the connection pool could not be opened: %v\n", err)

		return 1
	}

	defer testPool.Close()

	if err := db.Migrate(ctx, testDSN, invoice.New(invoice.Options{}).Migrations(),
		invoice.ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "the migration could not be applied: %v\n", err)

		return 1
	}

	return m.Run()
}

// newService builds a service over the real repository.
func newService(t *testing.T) *service.Service {
	t.Helper()

	return service.New(repository.New(testPool.Pool()), service.Options{})
}

// issueFor builds a valid request against the given series prefix.
func issueFor(prefix string) service.IssueInput {
	return service.IssueInput{
		SeriesPrefix: prefix,
		Kind:         models.KindSale,
		CurrencyCode: "TRY",
		Seller:       models.Party{Name: "Gobit Shop", TaxNumber: "1234567890", CountryCode: "TR"},
		Buyer:        models.Party{Name: "A Customer", CountryCode: "TR"},
		Lines: []service.LineInput{{
			Description: "Red T-Shirt",
			Quantity:    2,
			UnitPrice:   1000,
			Subtotal:    2000,
			TaxRateBps:  2000,
			TaxTotal:    400,
			Total:       2400,
		}},
		Subtotal: 2000,
		TaxTotal: 400,
		Total:    2400,
	}
}

// TestTheTablesArePresent verifies the migration actually ran.
func TestTheTablesArePresent(t *testing.T) {
	ctx := context.Background()

	for _, table := range moduleTables {
		var exists bool

		err := testPool.Pool().QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			  WHERE table_schema = 'public' AND table_name = $1)`, table).Scan(&exists)
		require.NoError(t, err)
		assert.True(t, exists, "the %s table has to exist", table)
	}
}

// TestAFailedIssueGivesItsNumberBack is the half a fake cannot show, and the
// half the whole design is built around.
//
// The number is taken by an UPDATE inside the same transaction that writes the
// document. When the write fails, the rollback takes the increment with it — so
// the next document gets the number the failed one was going to have, and the
// series has no hole.
//
// A database SEQUENCE would fail this test. That is the entire reason this
// module does not use one, while the order module does use one for its order
// numbers: a sequence advances outside the transaction and a rollback burns its
// value. For an order number the hole is harmless; here it is what a tax
// authority reads as a document that was issued and then hidden.
func TestAFailedIssueGivesItsNumberBack(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	first, err := svc.Issue(ctx, issueFor("GAP"))
	require.NoError(t, err)
	require.Equal(t, int64(1), sequenceOf(t, first.Number))

	// A document whose line quantity the DATABASE refuses: the check constraint
	// fires after the number has been taken, which is exactly the moment the
	// rollback has to matter. The service's own validation is bypassed by
	// writing through the repository, because a request the service rejects
	// never reaches the number at all — and that is a different test.
	failed := issueFor("GAP")
	failed.Lines[0].Quantity = 2
	failed.Lines[0].Description = "Broken"

	repo := repository.New(testPool.Pool())
	txErr := repo.WithTx(ctx, func(ctx context.Context) error {
		series, err := repo.TakeNextNumber(ctx, "GAP", int32(first.IssuedAt.Year()))
		require.NoError(t, err)
		require.Equal(t, int64(2), series.LastNumber,
			"the failing attempt has to take the NEXT number before it fails")

		// A quantity of zero is refused by the table's own CHECK.
		_, err = repo.CreateInvoice(ctx, models.Invoice{
			ID:           models.NewInvoiceID(),
			Number:       service.FormatNumber(series.Prefix, series.Year, series.LastNumber),
			SeriesID:     series.ID,
			Kind:         models.KindSale,
			Status:       models.StatusIssued,
			CurrencyCode: "TRY",
			Seller:       models.Party{Name: "Gobit Shop"},
			Buyer:        models.Party{Name: "A Customer"},
			Subtotal:     2000,
			TaxTotal:     400,
			Total:        2400,
			IssuedAt:     first.IssuedAt,
			Lines: []models.Line{{
				ID:          models.NewLineID(),
				Position:    1,
				Description: "Broken",
				Quantity:    0,
				Subtotal:    2000,
				TaxTotal:    400,
				Total:       2400,
			}},
		})

		return err
	})
	require.Error(t, txErr, "the database has to refuse a line with no quantity")

	next, err := svc.Issue(ctx, issueFor("GAP"))
	require.NoError(t, err)

	assert.Equal(t, int64(2), sequenceOf(t, next.Number),
		"the number the failed attempt took has to come back; a gap here is the one thing "+
			"this module exists to prevent (got %s)", next.Number)
}

// sequenceOf returns the sequence part of a number.
//
// The assertions are written against the SEQUENCE rather than against a whole
// number string, because the year comes from the real clock — it is a fact
// about when the document was issued, not a test parameter — and a test that
// pinned the whole string would start failing on the first of January for a
// reason that has nothing to do with what it is checking.
//
// The first version of this helper REBUILT the number from the prefix and a
// sequence the caller passed in, and then compared it to a constant. That
// compares a constant to a constant: the number under test only contributed its
// first three characters. A mutation that committed the number outside the
// transaction — the exact failure the gap test exists for — passed against it.
func sequenceOf(t *testing.T, number string) int64 {
	t.Helper()

	require.Len(t, number, 16, "the number has to be 3 letters + 4 year digits + 9 sequence digits")

	sequence, err := strconv.ParseInt(number[7:], 10, 64)
	require.NoError(t, err, "the last 9 characters of %q have to be the sequence", number)

	return sequence
}

// TestConcurrentIssuesTakeDistinctConsecutiveNumbers is the other half.
//
// Twenty documents are issued at the same moment against ONE series. What has
// to come out is 1..20 with nothing missing and nothing repeated. Two callers
// reading the same last_number and both writing the next one is exactly the
// failure the row lock prevents, and a fake that serializes on a mutex cannot
// tell the difference between having that lock and not.
func TestConcurrentIssuesTakeDistinctConsecutiveNumbers(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	const issues = 20

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		numbers []string
		failed  []error
	)

	for range issues {
		wg.Add(1)

		go func() {
			defer wg.Done()

			issued, err := svc.Issue(ctx, issueFor("RAC"))

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				failed = append(failed, err)

				return
			}

			numbers = append(numbers, issued.Number)
		}()
	}

	wg.Wait()

	require.Empty(t, failed, "no issue may fail: a second caller has to WAIT for the lock, not lose")
	require.Len(t, numbers, issues)

	seen := map[int64]bool{}
	for _, number := range numbers {
		sequence := sequenceOf(t, number)
		require.False(t, seen[sequence], "sequence %d was handed out twice (%s)", sequence, number)
		seen[sequence] = true
	}

	for sequence := int64(1); sequence <= issues; sequence++ {
		assert.True(t, seen[sequence], "the series has to contain %d; a missing one is a gap", sequence)
	}
}

// TestAnIssuedDocumentComesBackWhole covers the round trip every field takes.
//
// A document is a snapshot: what was written is what has to come back, down to
// the tax rate of a line. A field dropped from either the INSERT or the row
// conversion still compiles and still passes every unit test that uses a fake.
func TestAnIssuedDocumentComesBackWhole(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	in := issueFor("WHL")
	in.Seller.TaxOffice = "Kadikoy"
	in.Buyer.TaxNumber = "12345678901"
	in.Buyer.Address = "A street, a city"
	in.Metadata = map[string]any{"channel": "web"}

	issued, err := svc.Issue(ctx, in)
	require.NoError(t, err)

	read, err := svc.GetInvoice(ctx, issued.ID)
	require.NoError(t, err)

	assert.Equal(t, issued.Number, read.Number)
	assert.Equal(t, "Kadikoy", read.Seller.TaxOffice)
	assert.Equal(t, "12345678901", read.Buyer.TaxNumber)
	assert.Equal(t, "A street, a city", read.Buyer.Address)
	assert.Equal(t, "web", read.Metadata["channel"])
	assert.True(t, read.TotalsConsistent())

	require.Len(t, read.Lines, 1)
	assert.Equal(t, int32(1), read.Lines[0].Position)
	assert.Equal(t, int32(2000), read.Lines[0].TaxRateBps,
		"the rate the line was charged at has to survive the database")
}

// TestACanceledDocumentKeepsItsNumber holds the other end of the gap rule.
//
// Deleting a withdrawn document would put the hole in from the far side, so a
// cancellation is a status and the row stays where it is.
func TestACanceledDocumentKeepsItsNumber(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	issued, err := svc.Issue(ctx, issueFor("CAN"))
	require.NoError(t, err)

	canceled, err := svc.MoveStatus(ctx, issued.ID, service.MoveInput{
		To:     models.StatusCanceled,
		Reason: "the customer withdrew the order",
	})
	require.NoError(t, err)
	assert.Equal(t, models.StatusCanceled, canceled.Status)
	assert.Equal(t, issued.Number, canceled.Number, "a cancellation does not release the number")

	next, err := svc.Issue(ctx, issueFor("CAN"))
	require.NoError(t, err)
	assert.Equal(t, int64(2), sequenceOf(t, next.Number),
		"the canceled number is spent; the next document takes the one after it")
}
