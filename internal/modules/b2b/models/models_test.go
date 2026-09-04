package models_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/b2b/models"
)

// TestSpendingWindowIsComputedFromTheCalendar pins the contract the module
// leaves to the next step.
//
// The window starts from the CALENDAR, not from the OPENING OF THE RECORD: a
// monthly limit resets on the 1st of the month even if the company was opened
// on the 20th. Accounting periods run on the calendar and a sliding month would
// line up with no financial report.
func TestSpendingWindowIsComputedFromTheCalendar(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 17, 9, 30, 45, 0, time.UTC)

	monthly := models.ResetMonthly.WindowStart(now)
	require.NotNil(t, monthly)
	assert.Equal(t, time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC), *monthly)

	yearly := models.ResetYearly.WindowStart(now)
	require.NotNil(t, yearly)
	assert.Equal(t, time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC), *yearly)

	assert.Nil(t, models.ResetNever.WindowStart(now),
		"with no reset there is no window either; the limit applies to the whole history")
}

// TestSpendingWindowUsesUTCNotLocalTime verifies that the month starts at the
// SAME moment for two employees of the same company in two different countries.
//
// Had local time been used, the same limit would reset at different moments for
// the two employees and the company total would never settle into a single
// period.
func TestSpendingWindowUsesUTCNotLocalTime(t *testing.T) {
	t.Parallel()

	// 00:30 on 1 April in UTC, but still 31 March in UTC-3.
	zone := time.FixedZone("UTC-3", -3*60*60)
	now := time.Date(2026, time.April, 1, 0, 30, 0, 0, time.UTC).In(zone)

	start := models.ResetMonthly.WindowStart(now)
	require.NotNil(t, start)
	assert.Equal(t, time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC), *start,
		"the window has to be computed against the UTC calendar")
}

// TestUndefinedPeriodOpensNoWindow verifies that a value outside the enum falls
// to the safest outcome.
//
// Returning an "unbounded window" would mean silently widening the limit; the
// value cannot enter the database anyway (CHECK), but the behavior still has to
// be definite.
func TestUndefinedPeriodOpensNoWindow(t *testing.T) {
	t.Parallel()

	var weekly models.SpendingResetPeriod = "weekly"
	assert.False(t, weekly.Valid())
	assert.Nil(t, weekly.WindowStart(time.Now()))

	assert.True(t, models.ResetMonthly.Valid())
	assert.True(t, models.ResetYearly.Valid())
	assert.True(t, models.ResetNever.Valid())
}

// TestZeroLimitDiffersFromUnlimited pins the model's most easily confused
// distinction: nil is "unlimited", 0 is "cannot spend at all".
func TestZeroLimitDiffersFromUnlimited(t *testing.T) {
	t.Parallel()

	zero := int64(0)
	assert.True(t, models.CompanyEmployee{SpendingLimit: &zero}.HasSpendingLimit())
	assert.False(t, models.CompanyEmployee{}.HasSpendingLimit())
}

// TestNormalizationConvertsToStorageForm verifies that the e-mail comes down to
// lowercase and the codes go up to UPPERCASE.
func TestNormalizationConvertsToStorageForm(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "muhasebe@acme.example", models.NormalizeEmail("  Muhasebe@Acme.Example "))
	assert.Equal(t, "TR", models.NormalizeCountryCode(" tr "))
	assert.Equal(t, "TRY", models.NormalizeCurrencyCode("try"))
}

// TestIdentifiersArePrefixedAndTimeOrdered verifies the identifier rule of plan
// Section 8: the prefix says the kind, the body carries the creation order.
func TestIdentifiersArePrefixedAndTimeOrdered(t *testing.T) {
	t.Parallel()

	earlier := time.Date(2026, time.March, 17, 9, 0, 0, 0, time.UTC)
	later := earlier.Add(time.Second)

	first := models.NewCompanyID(earlier)
	second := models.NewCompanyID(later)

	assert.True(t, strings.HasPrefix(first, models.CompanyIDPrefix))
	assert.Len(t, strings.TrimPrefix(first, models.CompanyIDPrefix), models.IDBodyLength())
	assert.Less(t, first, second, "identifiers have to be time-ordered lexicographically too")

	employee := models.NewEmployeeID(earlier)
	assert.True(t, strings.HasPrefix(employee, models.EmployeeIDPrefix))
	assert.NotEqual(t, models.CompanyIDPrefix, models.EmployeeIDPrefix,
		"the two kinds of identifier have to be told apart at a single glance")
}
