package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/b2b/models"
)

// ruleBody is the tests' counterpart of the body the surface returns.
//
// The type is defined SEPARATELY and interopSpendingRule is not reused: what is
// exercised is exactly the FIELD NAMES, and a test using the production type
// would pass even if the names changed. The consumer (the order module) writes
// these names separately in its own package, and the compiler cannot tie the
// two sides together.
type ruleBody struct {
	Limited       bool   `json:"limited"`
	SpendingLimit int64  `json:"spending_limit"`
	CurrencyCode  string `json:"currency_code"`
	WindowStart   string `json:"window_start"`
}

// decodeRule calls the surface and decodes the body.
func decodeRule(t *testing.T, svc *Service, customerID string) ruleBody {
	t.Helper()

	payload, err := NewInterop(svc).SpendingLimitJSON(t.Context(), customerID)
	require.NoError(t, err)

	var rule ruleBody
	require.NoError(t, json.Unmarshal(payload, &rule))
	return rule
}

// addEmployee adds an employee with the given limit to a company.
func addEmployee(t *testing.T, svc *Service, companyID, customerID string, limit *int64) {
	t.Helper()

	_, err := svc.CreateEmployee(t.Context(), EmployeeInput{
		CompanyID:     companyID,
		CustomerID:    customerID,
		SpendingLimit: limit,
	})
	require.NoError(t, err)
}

// TestTheRulePublishesTheLimitAndTheWindow verifies the rule of a limited
// employee.
//
// The company's period is monthly and the fixed clock is on the 17th of the
// month; the window therefore has to start on the 1st of the month at 00:00
// UTC. That the window follows the CALENDAR (does not shift with the day the
// company was opened) is visible only here, in the returned string.
func TestTheRulePublishesTheLimitAndTheWindow(t *testing.T) {
	svc, _, _ := newTestService(t)
	company := newTestCompany(t, svc)
	limit := int64(500_000)
	addEmployee(t, svc, company.ID, "cust_01", &limit)

	rule := decodeRule(t, svc, "cust_01")

	assert.True(t, rule.Limited)
	assert.Equal(t, int64(500_000), rule.SpendingLimit)
	assert.Equal(t, "TRY", rule.CurrencyCode, "the limit is expressed in the COMPANY's currency")
	assert.Equal(t, "2026-03-01T00:00:00Z", rule.WindowStart)
}

// TestTheRulePublishesAYearlyWindow verifies that a yearly period starts on
// 1 January.
func TestTheRulePublishesAYearlyWindow(t *testing.T) {
	svc, _, _ := newTestService(t)
	in := validCompanyInput()
	in.SpendingLimitResetPeriod = string(models.ResetYearly)
	company, err := svc.CreateCompany(t.Context(), in)
	require.NoError(t, err)

	limit := int64(10)
	addEmployee(t, svc, company.ID, "cust_01", &limit)

	assert.Equal(t, "2026-01-01T00:00:00Z", decodeRule(t, svc, "cust_01").WindowStart)
}

// TestTheRuleWindowIsEmptyForAPeriodWithoutOne verifies what the "never"
// period comes out as.
//
// Without a window the field is an EMPTY string. Sending a zero timestamp
// would mean expecting the consumer to tell "since 0001-01-01" from "there is
// no window", and the first looks like a date and could silently produce a
// wrong range.
func TestTheRuleWindowIsEmptyForAPeriodWithoutOne(t *testing.T) {
	svc, _, _ := newTestService(t)
	in := validCompanyInput()
	in.SpendingLimitResetPeriod = string(models.ResetNever)
	company, err := svc.CreateCompany(t.Context(), in)
	require.NoError(t, err)

	limit := int64(10)
	addEmployee(t, svc, company.ID, "cust_01", &limit)

	rule := decodeRule(t, svc, "cust_01")
	assert.True(t, rule.Limited)
	assert.Empty(t, rule.WindowStart)
}

// TestTheRuleIsUnlimitedForAnUnlimitedEmployee verifies that a nil limit
// resolves to "no rule".
//
// nil means UNLIMITED; had it been published as a limited rule, the consumer
// would take it for a ceiling, and the unlimited employee would turn into one
// with a zero limit.
func TestTheRuleIsUnlimitedForAnUnlimitedEmployee(t *testing.T) {
	svc, _, _ := newTestService(t)
	company := newTestCompany(t, svc)
	addEmployee(t, svc, company.ID, "cust_01", nil)

	assert.False(t, decodeRule(t, svc, "cust_01").Limited)
}

// TestTheRuleKEEPSAZeroLimit verifies that the distinction between 0 and nil
// is not lost at the boundary.
//
// An employee with a zero limit is LIMITED and can spend nothing. Collapsing
// the two into one answer would hand a company that says "I set the limit to
// zero" an unlimited employee.
func TestTheRuleKEEPSAZeroLimit(t *testing.T) {
	svc, _, _ := newTestService(t)
	company := newTestCompany(t, svc)
	zero := int64(0)
	addEmployee(t, svc, company.ID, "cust_01", &zero)

	rule := decodeRule(t, svc, "cust_01")
	assert.True(t, rule.Limited)
	assert.Zero(t, rule.SpendingLimit)
}

// TestTheRuleForACustomerWhoIsNoEmployee returns NO ERROR.
//
// Most installations are B2C, and the consumer calls this surface for EVERY
// order; "this customer is not an employee of a company" is the normal path
// for it. Returning an error would leave the consumer unable to tell "there is
// no rule" from "we could not learn the rule".
func TestTheRuleForACustomerWhoIsNoEmployee(t *testing.T) {
	svc, _, _ := newTestService(t)

	rule := decodeRule(t, svc, "cust_BAGSIZ")
	assert.False(t, rule.Limited)
}

// TestTheRuleIsUnlimitedForAnUnrecognizedID verifies that a string that is not
// even a customer id returns no error.
//
// Such an id CANNOT BE BOUND as an employee (CreateEmployee checks the
// prefix), so the answer "it has no limit" is not a guess but a provable fact.
// Returning an error would put b2b's opinion about the id format in front of
// every order.
func TestTheRuleIsUnlimitedForAnUnrecognizedID(t *testing.T) {
	svc, _, _ := newTestService(t)

	for _, id := range []string{"", "cus_ESKI_ONEK", "  "} {
		payload, err := NewInterop(svc).SpendingLimitJSON(t.Context(), id)
		require.NoError(t, err, "id: %q", id)

		var rule ruleBody
		require.NoError(t, json.Unmarshal(payload, &rule))
		assert.False(t, rule.Limited, "id: %q", id)
	}
}

// TestTheRuleDoesNotHIDEAReadFailure verifies that an infrastructure error is
// not swallowed.
//
// When the bond layer cannot be read, what the limit is is NOT KNOWN.
// Returning "unlimited" would silently remove the spending limit on every
// failure of the link service; the consumer therefore has to see the error and
// refuse the order.
func TestTheRuleDoesNotHIDEAReadFailure(t *testing.T) {
	svc, _, links := newTestService(t)
	links.failListByTo = errors.Internal("link_down", "the bond layer does not answer")

	_, err := NewInterop(svc).SpendingLimitJSON(t.Context(), "cust_01")

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInternal))
}

// TestTheRuleIsUnlimitedForADeletedCompany verifies that a soft-deleted
// company's rule is not published.
//
// When a company is deleted its employees are deleted too and their bonds are
// removed; even a bond left behind must not bring the rule back. Otherwise a
// closed company's limit would go on being enforced against a budget that does
// not exist.
func TestTheRuleIsUnlimitedForADeletedCompany(t *testing.T) {
	svc, _, _ := newTestService(t)
	company := newTestCompany(t, svc)
	limit := int64(500)
	addEmployee(t, svc, company.ID, "cust_01", &limit)

	require.NoError(t, svc.DeleteCompany(t.Context(), company.ID))

	assert.False(t, decodeRule(t, svc, "cust_01").Limited)
}

// TestTheRuleWindowIsUTC verifies that the returned time carries no time zone.
//
// A local time zone would mean the month starting at different moments for
// the same company's employees in two countries; that is why the string ends
// in "Z".
func TestTheRuleWindowIsUTC(t *testing.T) {
	svc, _, _ := newTestService(t)
	company := newTestCompany(t, svc)
	limit := int64(1)
	addEmployee(t, svc, company.ID, "cust_01", &limit)

	instant, err := time.Parse(time.RFC3339, decodeRule(t, svc, "cust_01").WindowStart)
	require.NoError(t, err)
	assert.Equal(t, time.UTC, instant.Location())
}
