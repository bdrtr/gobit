package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/invoice/models"
	"github.com/bdrtr/gobit/internal/modules/invoice/service"
)

// testNow is the fixed clock; the YEAR decides which series a document lands
// in, so a test that could not choose it could not exercise the rollover.
var testNow = time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC)

// newService builds a service over the fake with the clock pinned.
func newService(repo *fakeRepo) *service.Service {
	return service.New(repo, service.Options{Now: func() time.Time { return testNow }})
}

// TestTheFirstDocumentOfASeriesIsNumberOne is the shape of every number.
//
// The number is the whole point of the module, and its FORMAT is legal rather
// than cosmetic: 3 letters, a 4-digit year, a 9-digit sequence.
func TestTheFirstDocumentOfASeriesIsNumberOne(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	svc := newService(repo)

	issued, err := svc.Issue(context.Background(), validIssue())
	require.NoError(t, err)

	assert.Equal(t, "GBT2026000000001", issued.Number)
	assert.Equal(t, models.StatusIssued, issued.Status,
		"a document is born issued; there is no draft to be born into")
	assert.Len(t, issued.Number, 16, "the Turkish format is 16 characters")
}

// TestTheNumbersRunWithoutAGap is the guarantee, at the level a fake can show
// it: consecutive issues take consecutive numbers.
//
// What a fake CANNOT show is the half that matters most — that a failed issue
// gives its number back. That needs a real transaction and is proven in the
// module's integration test.
func TestTheNumbersRunWithoutAGap(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	svc := newService(repo)

	var numbers []string

	for range 3 {
		issued, err := svc.Issue(context.Background(), validIssue())
		require.NoError(t, err)
		numbers = append(numbers, issued.Number)
	}

	assert.Equal(t,
		[]string{"GBT2026000000001", "GBT2026000000002", "GBT2026000000003"},
		numbers)
}

// TestTheYearIsPartOfTheSeries covers the rollover.
//
// The numbering restarts each year, so the same sequence value appears again
// under a different year and the two documents are still distinct.
func TestTheYearIsPartOfTheSeries(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	year2026 := service.New(repo, service.Options{Now: func() time.Time { return testNow }})
	year2027 := service.New(repo, service.Options{
		Now: func() time.Time { return testNow.AddDate(1, 0, 0) },
	})

	first, err := year2026.Issue(context.Background(), validIssue())
	require.NoError(t, err)

	next, err := year2027.Issue(context.Background(), validIssue())
	require.NoError(t, err)

	assert.Equal(t, "GBT2026000000001", first.Number)
	assert.Equal(t, "GBT2027000000001", next.Number,
		"the new year opens its own series and starts at one again")
	assert.NotEqual(t, first.SeriesID, next.SeriesID)
}

// TestADocumentThatDoesNotAddUpIsRefused is why the caller's totals are sent at
// all.
//
// Deriving the totals from the lines would accept an assembler that lost a row:
// the document would add up perfectly and be missing a line. Checking is the
// only arrangement that catches the mistake that actually happens.
func TestADocumentThatDoesNotAddUpIsRefused(t *testing.T) {
	t.Parallel()

	tests := map[string]func(in *service.IssueInput){
		"the subtotal disagrees with the lines": func(in *service.IssueInput) { in.Subtotal = 1999 },
		"the tax disagrees with the lines":      func(in *service.IssueInput) { in.TaxTotal = 399 },
		"the total disagrees with the lines":    func(in *service.IssueInput) { in.Total = 2401 },
		"the discount disagrees with the lines": func(in *service.IssueInput) {
			in.DiscountTotal = 1
		},
		"a line does not add up": func(in *service.IssueInput) { in.Lines[0].Total = 2401 },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			in := validIssue()
			mutate(&in)

			_, err := newService(newFakeRepo()).Issue(context.Background(), in)
			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
		})
	}
}

// TestAnUnprintableRequestIsRefused covers the fields without which the
// document could not be printed at all.
func TestAnUnprintableRequestIsRefused(t *testing.T) {
	t.Parallel()

	tests := map[string]func(in *service.IssueInput){
		"a two-letter prefix":     func(in *service.IssueInput) { in.SeriesPrefix = "GB" },
		"a lower-case prefix":     func(in *service.IssueInput) { in.SeriesPrefix = "gbt" },
		"a prefix with a symbol":  func(in *service.IssueInput) { in.SeriesPrefix = "GB-" },
		"an unknown kind":         func(in *service.IssueInput) { in.Kind = "gift" },
		"no currency":             func(in *service.IssueInput) { in.CurrencyCode = "" },
		"no seller name":          func(in *service.IssueInput) { in.Seller.Name = "  " },
		"no buyer name":           func(in *service.IssueInput) { in.Buyer.Name = "" },
		"no lines":                func(in *service.IssueInput) { in.Lines = nil },
		"a line with no quantity": func(in *service.IssueInput) { in.Lines[0].Quantity = 0 },
		"a line with no description": func(in *service.IssueInput) {
			in.Lines[0].Description = " "
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			in := validIssue()
			mutate(&in)

			_, err := newService(newFakeRepo()).Issue(context.Background(), in)
			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
		})
	}
}

// TestThePrefixIsCheckedBeforeTheNumberIsSpent is why the format rule lives
// here and not at transmission.
//
// A document refused by the regime for its prefix would already have taken its
// number, and the shop would be left with a hole it cannot fill.
func TestThePrefixIsCheckedBeforeTheNumberIsSpent(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	in := validIssue()
	in.SeriesPrefix = "TOOLONG"

	_, err := newService(repo).Issue(context.Background(), in)
	require.Error(t, err)

	series, listErr := repo.ListSeries(context.Background())
	require.NoError(t, listErr)
	assert.Empty(t, series, "a refused request must not have opened a series")
}

// TestTheLinesAreNumberedFromOne keeps the printed order out of the id format's
// hands.
func TestTheLinesAreNumberedFromOne(t *testing.T) {
	t.Parallel()

	in := validIssue()
	in.Lines = append(in.Lines, service.LineInput{
		Description: "Shipping", Quantity: 1, UnitPrice: 500,
		Subtotal: 500, TaxRateBps: 2000, TaxTotal: 100, Total: 600,
	})
	in.Subtotal = 2500
	in.TaxTotal = 500
	in.Total = 3000

	issued, err := newService(newFakeRepo()).Issue(context.Background(), in)
	require.NoError(t, err)
	require.Len(t, issued.Lines, 2)

	assert.Equal(t, int32(1), issued.Lines[0].Position)
	assert.Equal(t, int32(2), issued.Lines[1].Position)
	assert.Equal(t, "Shipping", issued.Lines[1].Description,
		"shipping reaches the document as a LINE, because that is how it is printed")
}

// TestFormatNumberIsTheOneFormatter keeps the legal shape in one place.
func TestFormatNumberIsTheOneFormatter(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "GBT2026000000001", service.FormatNumber("GBT", 2026, 1))
	assert.Equal(t, "GBT2026999999999", service.FormatNumber("GBT", 2026, 999_999_999))
	assert.True(t, strings.HasPrefix(service.FormatNumber("ABC", 2030, 42), "ABC2030"))
}

// TestADigitInThePrefixIsAccepted holds a rule the framework does NOT own.
//
// The first version refused digits, on the reading that a series code is three
// letters. Integrators differ, and a framework that refused a prefix the shop's
// own integrator accepts would be wrong in a way the shop cannot work around.
// The length stays firm, because that one the number format really does own.
func TestADigitInThePrefixIsAccepted(t *testing.T) {
	t.Parallel()

	in := validIssue()
	in.SeriesPrefix = "A1B"

	issued, err := newService(newFakeRepo()).Issue(context.Background(), in)
	require.NoError(t, err)
	assert.Equal(t, "A1B2026000000001", issued.Number)
}
