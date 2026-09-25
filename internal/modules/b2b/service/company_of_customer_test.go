package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/b2b/models"
)

// CompanyOfCustomer is the cart's question to this module (ADR 0185): which
// company does the customer buy for, so the company's contract price can match.

// companyFixture is a service over the in-memory repository and links.
func companyFixture(t *testing.T) (*Service, *memLinker) {
	t.Helper()

	links := newMemLinker()
	svc, err := New(Options{Repo: newMemRepo(), Links: links})
	require.NoError(t, err)
	return svc, links
}

// TestAnEmployeeBuysForTheirCompany is the answer that matters.
func TestAnEmployeeBuysForTheirCompany(t *testing.T) {
	svc, _ := companyFixture(t)
	company, err := svc.CreateCompany(t.Context(), CompanyInput{
		Name: "Acme", Email: "buyer@acme.example", CurrencyCode: "try",
		SpendingLimitResetPeriod: string(models.ResetMonthly),
	})
	require.NoError(t, err)
	_, err = svc.CreateEmployee(t.Context(), EmployeeInput{CompanyID: company.ID, CustomerID: "cust_EMPLOYEE"})
	require.NoError(t, err)

	got, err := NewInterop(svc).CompanyOfCustomer(t.Context(), "cust_EMPLOYEE")

	require.NoError(t, err)
	assert.Equal(t, company.ID, got)
}

// TestSomebodyWhoIsNoEmployeeBuysForNobody is an answer, not a failure: the
// cart omits the attribute, and a company price stays closed.
func TestSomebodyWhoIsNoEmployeeBuysForNobody(t *testing.T) {
	svc, _ := companyFixture(t)

	for _, customerID := range []string{"cust_UNBOUND", "", "not-a-customer-id"} {
		got, err := NewInterop(svc).CompanyOfCustomer(t.Context(), customerID)

		require.NoError(t, err, "customer %q", customerID)
		assert.Empty(t, got, "customer %q", customerID)
	}
}

// TestAnUnreadableMembershipIsReported keeps an outage from looking like "no
// company": the cart logs it, which it could not do for an empty answer.
func TestAnUnreadableMembershipIsReported(t *testing.T) {
	svc, links := companyFixture(t)
	links.failListByTo = errors.Internal("link_down", "the link layer does not answer")

	_, err := NewInterop(svc).CompanyOfCustomer(t.Context(), "cust_01")

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInternal))
}
