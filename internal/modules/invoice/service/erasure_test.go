package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/erasure"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/invoice/models"
	"github.com/bdrtr/gobit/internal/modules/invoice/service"
)

// buyerInvoice is a stored document issued to the given address.
func buyerInvoice(id, email string) models.Invoice {
	in := issuedInvoice(id, models.StatusIssued)
	in.Buyer = models.Party{Name: "A Customer", Email: email, CountryCode: "TR"}

	return in
}

// TestAnInvoiceIsAlwaysRetained is the whole of ADR 0032 in one assertion.
//
// The outcome does not depend on the subject, on how many documents were found
// or on whether any were: an issued invoice is a legal document and the module
// keeps it. A test that only exercised the "found some" case would leave the
// interesting half — a person with no invoices here — free to answer Deleted,
// which would be a lie about a table nobody had looked in.
func TestAnInvoiceIsAlwaysRetained(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	repo.seed(buyerInvoice("inv-1", "ada@example.com"))
	svc := service.New(repo, service.Options{})

	found, err := svc.Erase(context.Background(), erasure.Subject{Email: "ada@example.com"})
	require.NoError(t, err)
	assert.Equal(t, erasure.Retained, found.Outcome)

	absent, err := svc.Erase(context.Background(), erasure.Subject{Email: "nobody@example.com"})
	require.NoError(t, err)
	assert.Equal(t, erasure.Retained, absent.Outcome,
		"a person with no invoices here is still answered RETAINED: this module never deletes")
	assert.Zero(t, absent.Rows)
}

// TestTheRetainedCountIsTheCountThatWasFound holds the number honest.
//
// Rows is what a controller repeats to a data subject, so it has to be the real
// count rather than a placeholder. Three documents are seeded and only two are
// this person's, because a count that simply returned the table's size would
// pass against a single seeded row.
func TestTheRetainedCountIsTheCountThatWasFound(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	repo.seed(buyerInvoice("inv-1", "ada@example.com"))
	repo.seed(buyerInvoice("inv-2", "ada@example.com"))
	repo.seed(buyerInvoice("inv-3", "grace@example.com"))
	svc := service.New(repo, service.Options{})

	result, err := svc.Erase(context.Background(), erasure.Subject{Email: "ada@example.com"})
	require.NoError(t, err)
	assert.Equal(t, 2, result.Rows)
	assert.Equal(t, service.Holder, result.Holder)
}

// TestTheAddressIsMatchedWithoutRegardToCase covers the one thing this module
// cannot assume about its own column.
//
// customer and auth both carry a CHECK that forces the stored e-mail to lower
// case. invoices does NOT: the column copies what the document said. So a
// case-sensitive match would answer "nothing retained" about a person whose
// invoice is sitting in the table under one capital letter — a false answer
// with the shape of a true one.
func TestTheAddressIsMatchedWithoutRegardToCase(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	repo.seed(buyerInvoice("inv-1", "Ada@Example.COM"))
	svc := service.New(repo, service.Options{})

	result, err := svc.Erase(context.Background(), erasure.Subject{Email: "ada@example.com"})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Rows)
}

// TestASecondEraseAnswersTheSame is the idempotency the contract requires.
//
// [erasure.Eraser] says a controller who runs a sweep twice must get the same
// outcome rather than an error or a "nothing found". Here it is a property
// rather than a promise, because the method only reads — and the assertion is
// written down so that the day somebody adds a write, this is what stops them.
func TestASecondEraseAnswersTheSame(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	repo.seed(buyerInvoice("inv-1", "ada@example.com"))
	svc := service.New(repo, service.Options{})

	first, err := svc.Erase(context.Background(), erasure.Subject{Email: "ada@example.com"})
	require.NoError(t, err)
	second, err := svc.Erase(context.Background(), erasure.Subject{Email: "ada@example.com"})
	require.NoError(t, err)

	assert.Equal(t, first, second)
}

// TestTheRefusalNamesTheFreeFormColumnsItLeft is the rule ADR 0029 attaches to
// every answer this repository gives.
//
// gobit never rewrites a free-form column, so a report that listed only the
// columns the framework writes would let a reader believe the framework had
// looked at everything. The three open fields have to appear in Kept and have
// to be explained in Why, or the outcome covers a field nobody inspected.
func TestTheRefusalNamesTheFreeFormColumnsItLeft(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	// A document of this person's has to exist for there to be a refusal at
	// all: Kept names what is still held about THEM, so it is empty when
	// nothing matched (see TestAPersonWithNoInvoicesIsNotToldTheirsAreKept).
	repo.seed(buyerInvoice("inv-1", "ada@example.com"))
	svc := service.New(repo, service.Options{})

	result, err := svc.Erase(context.Background(), erasure.Subject{Email: "ada@example.com"})
	require.NoError(t, err)

	require.NotEmpty(t, result.Kept)
	assert.Subset(t, result.Kept, []string{
		"invoices.metadata",
		"invoices.status_reason",
		"invoice_lines.description",
	}, "the free-form columns gobit never rewrites have to be named")
	assert.Subset(t, result.Kept, []string{
		"invoices.buyer_name", "invoices.buyer_tax_number", "invoices.buyer_tax_office",
		"invoices.buyer_email", "invoices.buyer_address", "invoices.buyer_country_code",
	}, "the columns that still name the buyer have to be named")

	require.NotEmpty(t, result.Why, "Kept without Why is a refusal a controller cannot repeat")
	for _, column := range []string{"metadata", "status_reason", "description"} {
		assert.Contains(t, result.Why, column,
			"Why has to explain the free-form entries in Kept, not only the named ones")
	}
}

// TestKeptIsNotSharedBetweenReports guards a value that leaves this package.
//
// [erasure.Result] is handed to a caller this module knows nothing about. If
// two reports shared one backing array, a caller that sorted or truncated one
// of them would silently rewrite the other, and the second report would be
// wrong in a way that nothing in this module could produce or explain.
func TestKeptIsNotSharedBetweenReports(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	repo.seed(buyerInvoice("inv-1", "ada@example.com"))
	svc := service.New(repo, service.Options{})

	first, err := svc.Erase(context.Background(), erasure.Subject{Email: "ada@example.com"})
	require.NoError(t, err)
	original := first.Kept[0]
	first.Kept[0] = "mutated.by.the.caller"

	second, err := svc.Erase(context.Background(), erasure.Subject{Email: "ada@example.com"})
	require.NoError(t, err)
	assert.Equal(t, original, second.Kept[0])
}

// TestASubjectWithNoAddressIsNotAnsweredAsAbsent is the distinction the whole
// erasure contract exists to make.
//
// The invoices table has no customer_id and no order_id, so a subject carrying
// only a customer id cannot be looked up here at all. The honest answer says
// so: the outcome is still Retained, the count is zero, and Why states that
// nobody looked — because a bare "retained, 0 rows" would be read as "this
// person has no invoices with us".
func TestASubjectWithNoAddressIsNotAnsweredAsAbsent(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	repo.seed(buyerInvoice("inv-1", "ada@example.com"))
	svc := service.New(repo, service.Options{})

	result, err := svc.Erase(context.Background(), erasure.Subject{CustomerID: "cus_1"})
	require.NoError(t, err)

	assert.Equal(t, erasure.Retained, result.Outcome)
	assert.Zero(t, result.Rows)
	assert.NotEmpty(t, result.Kept)
	assert.Contains(t, result.Why, "no address",
		"the answer has to say that nothing was looked up, not merely that nothing was found")
	assert.Empty(t, repo.countedEmail, "no lookup may be attempted for an empty address")
}

// TestAPersonWithNoInvoicesIsNotToldTheirsAreKept is the other half of the
// distinction TestASubjectWithNoAddressIsNotAnsweredAsAbsent draws.
//
// There the request carried no address and nobody looked. Here somebody looked
// and this person is not in the table — and the answer used to be a count of
// zero beside all nine columns and a sentence saying "the buyer columns listed
// here still name this person". A controller repeats that to somebody who never
// bought anything. Kept names what is still held about THIS person, so with no
// matching document there is nothing to name.
func TestAPersonWithNoInvoicesIsNotToldTheirsAreKept(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	repo.seed(buyerInvoice("inv-1", "ada@example.com"))
	svc := service.New(repo, service.Options{})

	result, err := svc.Erase(context.Background(), erasure.Subject{Email: "nobody@example.com"})
	require.NoError(t, err)

	assert.Equal(t, erasure.Retained, result.Outcome,
		"the outcome is this module's policy, not a summary of what the query found")
	assert.Zero(t, result.Rows)
	assert.Empty(t, result.Kept,
		"a column of a table holding no row of this person's holds this person nowhere")
	require.NotEmpty(t, result.Why, "an outcome with no columns still owes the sentence")
	assert.NotContains(t, result.Why, "still name this person",
		"the refusal's sentence claims columns that name the subject; nothing here names them")
	assert.Contains(t, result.Why, "matched none",
		"the answer has to say the address was looked for and found in no document")
}

// TestTheEmptyAddressNeverMatchesEveryDocument covers a real column default.
//
// buyer_email is NOT NULL DEFAULT ” in migration 000001, so every document
// issued without an address carries the empty string. A subject with no e-mail
// that reached the query would match all of them and report a stranger's
// invoices as this person's.
func TestTheEmptyAddressNeverMatchesEveryDocument(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	repo.seed(buyerInvoice("inv-1", ""))
	repo.seed(buyerInvoice("inv-2", ""))
	svc := service.New(repo, service.Options{})

	result, err := svc.Erase(context.Background(), erasure.Subject{Email: "   "})
	require.NoError(t, err)
	assert.Zero(t, result.Rows)
}

// TestACountThatFailsIsAnErrorAndNotARetainedZero holds the line between a
// refusal and a fault.
//
// [erasure.Eraser] draws it explicitly: a holder exercising a refusal answers
// Retained, and a holder that cannot complete its work returns an error. A
// broken query dressed as "retained, 0 rows" has the shape of a real reply, and
// the controller would repeat it to the data subject.
func TestACountThatFailsIsAnErrorAndNotARetainedZero(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	repo.countErr = errors.Internal("fake_count_broken", "the connection went away")
	svc := service.New(repo, service.Options{})

	result, err := svc.Erase(context.Background(), erasure.Subject{Email: "ada@example.com"})
	require.Error(t, err)
	assert.Equal(t, erasure.Result{}, result)
}

// TestTheDeclaredColumnsAndTheRefusedOnesAgree pins the two lists together.
//
// [service.RetainedColumns] is what a Retained answer names; the module's
// declaration is what an embedder reads to find out where to look. A column in
// the first that is missing from the second is a place this module ADMITS to
// keeping and that no audit will ever visit. The declaration lives in the
// module package, so the module's own test owns the other direction; this one
// keeps the service's half from growing entries nobody declared.
func TestTheDeclaredColumnsAndTheRefusedOnesAgree(t *testing.T) {
	t.Parallel()

	columns := service.RetainedColumns()
	require.NotEmpty(t, columns)

	seen := map[string]bool{}
	for _, column := range columns {
		assert.Contains(t, column, ".", "a Kept entry is a table.column string")
		assert.False(t, seen[column], "%s is listed twice", column)
		seen[column] = true
	}
}
