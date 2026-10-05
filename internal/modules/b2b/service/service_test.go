package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/b2b/models"
)

// fixedClock is the tests' deterministic time source. The MIDDLE of the month
// was chosen: that way the start of the monthly window is not confused with
// "now".
var fixedClock = time.Date(2026, time.March, 17, 9, 30, 0, 0, time.UTC)

// newTestService builds a service with a fake repository and a fake bond
// service.
func newTestService(t *testing.T) (*Service, *memRepo, *memLinker) {
	t.Helper()

	repo := newMemRepo()
	links := newMemLinker()
	svc, err := New(Options{
		Repo:  repo,
		Links: links,
		Now:   func() time.Time { return fixedClock },
	})
	require.NoError(t, err)
	return svc, repo, links
}

// validCompanyInput is the tests' default company input.
func validCompanyInput() CompanyInput {
	return CompanyInput{
		Name:                     "Acme Sanayi A.S.",
		Email:                    "Muhasebe@Acme.example",
		CurrencyCode:             "try",
		SpendingLimitResetPeriod: string(models.ResetMonthly),
	}
}

// newTestCompany creates a company for the tests.
func newTestCompany(t *testing.T, svc *Service) models.Company {
	t.Helper()

	company, err := svc.CreateCompany(t.Context(), validCompanyInput())
	require.NoError(t, err)
	return company
}

// TestTheServiceCannotBeBuiltWithoutItsDependencies verifies that a missing
// dependency is caught AT SETUP.
//
// Had it been deferred to run time, the module would come up, employee
// records would be written and none of them would be bound to a customer; the
// gap would only show in the storefront.
func TestTheServiceCannotBeBuiltWithoutItsDependencies(t *testing.T) {
	// The refusal is required before its class (D136): this test used to pass
	// with the checks removed, because HasKind(nil, KindInternal) was true.
	_, err := New(Options{Links: newMemLinker()})
	require.Error(t, err, "it must not be built without a repository")
	assert.True(t, errors.HasKind(err, errors.KindInternal), "it must not be built without a repository")

	_, err = New(Options{Repo: newMemRepo()})
	require.Error(t, err, "it must not be built without a link service")
	assert.True(t, errors.HasKind(err, errors.KindInternal), "it must not be built without a link service")
}

// TestCreateCompanyValidatesItsInput shows that the validation in the service
// layer runs without going to the database.
func TestCreateCompanyValidatesItsInput(t *testing.T) {
	cases := map[string]func(in *CompanyInput){
		"empty name":             func(in *CompanyInput) { in.Name = "   " },
		"empty e-mail":           func(in *CompanyInput) { in.Email = "" },
		"malformed e-mail":       func(in *CompanyInput) { in.Email = "muhasebe@acme" },
		"empty currency":         func(in *CompanyInput) { in.CurrencyCode = "" },
		"short currency":         func(in *CompanyInput) { in.CurrencyCode = "TR" },
		"currency not letters":   func(in *CompanyInput) { in.CurrencyCode = "TR1" },
		"invalid country code":   func(in *CompanyInput) { in.CountryCode = "TUR" },
		"undefined reset period": func(in *CompanyInput) { in.SpendingLimitResetPeriod = "weekly" },
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			svc, repo, _ := newTestService(t)

			in := validCompanyInput()
			mutate(&in)

			_, err := svc.CreateCompany(t.Context(), in)
			assert.True(t, errors.IsInvalid(err), "expected class Invalid, got: %v", err)
			assert.Zero(t, repo.calls["CreateCompany"], "invalid input must never reach the repository")
		})
	}
}

// TestCreateCompanyNormalizes verifies that the e-mail address is turned to
// lower case and the codes to UPPER case, and the default of the period.
//
// The normalization is done ON STORAGE: the filter compares against the value
// in the column, and if two different spellings entered the table the filter
// would take them for different values.
func TestCreateCompanyNormalizes(t *testing.T) {
	svc, _, _ := newTestService(t)

	in := validCompanyInput()
	in.CountryCode = "tr"
	in.SpendingLimitResetPeriod = ""

	company, err := svc.CreateCompany(t.Context(), in)
	require.NoError(t, err)

	assert.Equal(t, "muhasebe@acme.example", company.Email)
	assert.Equal(t, "TRY", company.CurrencyCode)
	assert.Equal(t, "TR", company.CountryCode)
	assert.Equal(t, models.ResetNever, company.SpendingLimitResetPeriod,
		"if no period is given the most restrictive option has to apply")
	assert.Equal(t, fixedClock, company.CreatedAt)
}

// TestCreateEmployeeBondsTheCustomer verifies that the bond is established and
// the record comes back with the customer id.
//
// The id coming back matters: since it has no column, the value can only come
// from link, and its coming back empty would be the sign that the bond was
// never established.
func TestCreateEmployeeBondsTheCustomer(t *testing.T) {
	svc, _, links := newTestService(t)
	company := newTestCompany(t, svc)

	limit := int64(150000)
	employee, err := svc.CreateEmployee(t.Context(), EmployeeInput{
		CompanyID:      company.ID,
		CustomerID:     "cust_01",
		SpendingLimit:  &limit,
		IsCompanyAdmin: true,
	})
	require.NoError(t, err)

	assert.Equal(t, "cust_01", employee.CustomerID)
	assert.Equal(t, company.ID, employee.CompanyID)
	require.NotNil(t, employee.SpendingLimit)
	assert.Equal(t, limit, *employee.SpendingLimit)
	assert.True(t, links.bonds[LinkEmployeeCustomer][employee.ID]["cust_01"], "the bond has to be established")
}

// TestCreateEmployeeChecksIDPrefixes verifies that an id of the wrong type is
// caught without ever going to the database.
func TestCreateEmployeeChecksIDPrefixes(t *testing.T) {
	svc, repo, _ := newTestService(t)
	company := newTestCompany(t, svc)

	_, err := svc.CreateEmployee(t.Context(), EmployeeInput{
		CompanyID: company.ID,
		// A company id given in place of the customer id.
		CustomerID: company.ID,
	})
	assert.True(t, errors.IsInvalid(err), "expected class Invalid, got: %v", err)
	assert.Zero(t, repo.calls["CreateEmployee"])
}

// TestCreateEmployeeForAMissingCompanyIsNotFound verifies that a missing
// resource is reported in the 404 class, not 422.
func TestCreateEmployeeForAMissingCompanyIsNotFound(t *testing.T) {
	svc, repo, _ := newTestService(t)

	_, err := svc.CreateEmployee(t.Context(), EmployeeInput{
		CompanyID:  "comp_MISSING",
		CustomerID: "cust_01",
	})
	assert.True(t, errors.IsNotFound(err), "expected class NotFound, got: %v", err)
	assert.Zero(t, repo.calls["CreateEmployee"], "if the company does not exist no employee may be written")
}

// TestCreateEmployeeIsRolledBackWhenTheBondFails verifies that the
// compensation works.
//
// Had it not been rolled back, an employee record without a customer would
// stay up: the record carries a spending limit but resolves to nobody in the
// storefront.
func TestCreateEmployeeIsRolledBackWhenTheBondFails(t *testing.T) {
	svc, repo, links := newTestService(t)
	company := newTestCompany(t, svc)
	links.failCreate = true

	_, err := svc.CreateEmployee(t.Context(), EmployeeInput{
		CompanyID:  company.ID,
		CustomerID: "cust_01",
	})
	require.Error(t, err)

	require.Len(t, repo.employees, 1, "the record has to have been written")
	for _, e := range repo.employees {
		assert.NotNil(t, e.DeletedAt, "an employee whose bond could not be established has to be rolled back")
	}
}

// TestACustomerCannotJoinASecondCompany verifies the consequence of the
// cardinality.
//
// The rule is in the link table; what is exercised here is that the service
// carries that violation as a CONFLICT — had it been turned into Internal, the
// client would take a state it can fix for a server error.
func TestACustomerCannotJoinASecondCompany(t *testing.T) {
	svc, _, _ := newTestService(t)
	first := newTestCompany(t, svc)

	second, err := svc.CreateCompany(t.Context(), CompanyInput{
		Name: "Beta Ltd.", Email: "beta@example.test", CurrencyCode: "TRY",
	})
	require.NoError(t, err)

	_, err = svc.CreateEmployee(t.Context(), EmployeeInput{CompanyID: first.ID, CustomerID: "cust_01"})
	require.NoError(t, err)

	_, err = svc.CreateEmployee(t.Context(), EmployeeInput{CompanyID: second.ID, CustomerID: "cust_01"})
	assert.True(t, errors.IsConflict(err), "expected class Conflict, got: %v", err)
}

// TestDeleteCompanyClearsEmployeesAndBonds verifies the decision behind
// deleting a company: NO dangling employee record is LEFT and the customer
// bond is freed.
//
// The bond being freed is the test's real claim: had it stayed, the customer,
// because of a closed company, could never again be added as an employee of
// any company.
func TestDeleteCompanyClearsEmployeesAndBonds(t *testing.T) {
	svc, repo, links := newTestService(t)
	company := newTestCompany(t, svc)

	employee, err := svc.CreateEmployee(t.Context(), EmployeeInput{
		CompanyID: company.ID, CustomerID: "cust_01",
	})
	require.NoError(t, err)

	require.NoError(t, svc.DeleteCompany(t.Context(), company.ID))

	assert.NotNil(t, repo.employees[employee.ID].DeletedAt, "the employee has to be deleted too")
	assert.Empty(t, links.bonds[LinkEmployeeCustomer][employee.ID], "the customer bond has to be removed")

	// The customer has to be addable to another company now.
	next, err := svc.CreateCompany(t.Context(), CompanyInput{
		Name: "Gamma Ltd.", Email: "gamma@example.test", CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	_, err = svc.CreateEmployee(t.Context(), EmployeeInput{CompanyID: next.ID, CustomerID: "cust_01"})
	assert.NoError(t, err, "a customer whose bond was freed has to be hirable again")
}

// TestDeleteEmployeeRemovesTheBond verifies that the bond is cleaned up when a
// single employee is deleted too. Somebody who leaves starting work at another
// company is the ordinary case.
func TestDeleteEmployeeRemovesTheBond(t *testing.T) {
	svc, _, links := newTestService(t)
	company := newTestCompany(t, svc)

	employee, err := svc.CreateEmployee(t.Context(), EmployeeInput{
		CompanyID: company.ID, CustomerID: "cust_01",
	})
	require.NoError(t, err)

	require.NoError(t, svc.DeleteEmployee(t.Context(), employee.ID))
	assert.Empty(t, links.bonds[LinkEmployeeCustomer][employee.ID])
}

// TestMembershipReturnsOnlyTheirOwnCompany pins the module's storefront
// invariant: a customer cannot read SOMEBODY ELSE's company.
//
// Since the surface has no endpoint taking a company id (see the api package),
// this is the only entry point; as long as it holds here it holds in the
// storefront too.
func TestMembershipReturnsOnlyTheirOwnCompany(t *testing.T) {
	svc, _, _ := newTestService(t)

	acme := newTestCompany(t, svc)
	beta, err := svc.CreateCompany(t.Context(), CompanyInput{
		Name: "Beta Ltd.", Email: "beta@example.test", CurrencyCode: "EUR",
		SpendingLimitResetPeriod: string(models.ResetYearly),
	})
	require.NoError(t, err)

	_, err = svc.CreateEmployee(t.Context(), EmployeeInput{CompanyID: acme.ID, CustomerID: "cust_A"})
	require.NoError(t, err)
	_, err = svc.CreateEmployee(t.Context(), EmployeeInput{CompanyID: beta.ID, CustomerID: "cust_B"})
	require.NoError(t, err)

	membershipA, err := svc.MembershipOfCustomer(t.Context(), "cust_A")
	require.NoError(t, err)
	assert.Equal(t, acme.ID, membershipA.Company.ID)

	membershipB, err := svc.MembershipOfCustomer(t.Context(), "cust_B")
	require.NoError(t, err)
	assert.Equal(t, beta.ID, membershipB.Company.ID,
		"every customer has to see ONLY their own company")

	_, err = svc.MembershipOfCustomer(t.Context(), "cust_YABANCI")
	assert.True(t, errors.IsNotFound(err),
		"a customer bound to no company has to get a 404, got: %v", err)
}

// TestMembershipComputesTheWindowStart verifies that the spending window is
// derived from the company's period.
//
// The window itself is not enforced in this round; this is exactly the value
// the next step will read, and it follows the CALENDAR (not the date the
// record was opened).
func TestMembershipComputesTheWindowStart(t *testing.T) {
	svc, _, _ := newTestService(t)
	company := newTestCompany(t, svc) // monthly
	_, err := svc.CreateEmployee(t.Context(), EmployeeInput{
		CompanyID: company.ID, CustomerID: "cust_A",
	})
	require.NoError(t, err)

	membership, err := svc.MembershipOfCustomer(t.Context(), "cust_A")
	require.NoError(t, err)
	require.NotNil(t, membership.SpendingWindowStart)
	assert.Equal(t, time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC), *membership.SpendingWindowStart)

	// When the period is "never" there is no window.
	never := string(models.ResetNever)
	_, err = svc.UpdateCompany(t.Context(), company.ID,
		UpdateCompanyInput{SpendingLimitResetPeriod: &never})
	require.NoError(t, err)

	membership, err = svc.MembershipOfCustomer(t.Context(), "cust_A")
	require.NoError(t, err)
	assert.Nil(t, membership.SpendingWindowStart)
}

// TestMembershipDoesNotResolveToADeletedEmployee verifies that a bond that
// could not be cleaned up cannot bring back a deleted record.
//
// The scenario is real: removing a link is outside the database transaction
// and can fail (see Service.unlinkCustomers). That is why the read path RELIES
// on filtering deleted_at IS NULL; had it not filtered, a deleted employee
// would still carry spending authority in the storefront.
func TestMembershipDoesNotResolveToADeletedEmployee(t *testing.T) {
	svc, _, links := newTestService(t)
	company := newTestCompany(t, svc)

	employee, err := svc.CreateEmployee(t.Context(), EmployeeInput{
		CompanyID: company.ID, CustomerID: "cust_A",
	})
	require.NoError(t, err)

	links.failDelete = true // keep the bond from being cleaned up
	require.NoError(t, svc.DeleteEmployee(t.Context(), employee.ID))
	require.True(t, links.bonds[LinkEmployeeCustomer][employee.ID]["cust_A"],
		"test setup: the bond was deliberately left dangling")

	_, err = svc.MembershipOfCustomer(t.Context(), "cust_A")
	assert.True(t, errors.IsNotFound(err),
		"a dangling bond must not bring back a deleted employee, got: %v", err)
}

// TestTheSpendingLimitCanBeCleared verifies the distinction between "leave it
// alone" and "make it unlimited".
//
// Without the distinction a limit once set could never be removed: in JSON,
// null and not sending the field at all resolve to the same nil pointer.
func TestTheSpendingLimitCanBeCleared(t *testing.T) {
	svc, _, _ := newTestService(t)
	company := newTestCompany(t, svc)

	limit := int64(5000)
	employee, err := svc.CreateEmployee(t.Context(), EmployeeInput{
		CompanyID: company.ID, CustomerID: "cust_A", SpendingLimit: &limit,
	})
	require.NoError(t, err)

	// Changing only the admin flag must NOT TOUCH the limit.
	isAdmin := true
	updated, err := svc.UpdateEmployee(t.Context(), employee.ID,
		UpdateEmployeeInput{IsCompanyAdmin: &isAdmin})
	require.NoError(t, err)
	require.NotNil(t, updated.SpendingLimit, "the limit has to be kept")
	assert.Equal(t, limit, *updated.SpendingLimit)

	updated, err = svc.UpdateEmployee(t.Context(), employee.ID,
		UpdateEmployeeInput{ClearSpendingLimit: true})
	require.NoError(t, err)
	assert.Nil(t, updated.SpendingLimit, "the limit has to be removed")
	assert.False(t, updated.HasSpendingLimit())
}

// TestTheSpendingLimitCannotBeSetAndClearedAtOnce verifies that contradictory
// input is rejected. Silently picking one would mean the client does not know
// which one was applied.
func TestTheSpendingLimitCannotBeSetAndClearedAtOnce(t *testing.T) {
	svc, _, _ := newTestService(t)
	company := newTestCompany(t, svc)

	limit := int64(100)
	employee, err := svc.CreateEmployee(t.Context(), EmployeeInput{
		CompanyID: company.ID, CustomerID: "cust_A",
	})
	require.NoError(t, err)

	_, err = svc.UpdateEmployee(t.Context(), employee.ID, UpdateEmployeeInput{
		SpendingLimit: &limit, ClearSpendingLimit: true,
	})
	assert.True(t, errors.IsInvalid(err), "expected class Invalid, got: %v", err)
}

// TestANegativeSpendingLimitIsRejected forces the bound to be meaningful: a
// negative limit exceeds every comparison and would silently bar the employee
// from buying.
func TestANegativeSpendingLimitIsRejected(t *testing.T) {
	svc, _, _ := newTestService(t)
	company := newTestCompany(t, svc)

	negative := int64(-1)
	_, err := svc.CreateEmployee(t.Context(), EmployeeInput{
		CompanyID: company.ID, CustomerID: "cust_A", SpendingLimit: &negative,
	})
	assert.True(t, errors.IsInvalid(err), "expected class Invalid, got: %v", err)
}

// TestAZeroSpendingLimitDiffersFromUnlimited verifies that 0 and nil carry
// different meanings: one is "can spend nothing", the other "unlimited".
func TestAZeroSpendingLimitDiffersFromUnlimited(t *testing.T) {
	svc, _, _ := newTestService(t)
	company := newTestCompany(t, svc)

	zero := int64(0)
	limited, err := svc.CreateEmployee(t.Context(), EmployeeInput{
		CompanyID: company.ID, CustomerID: "cust_A", SpendingLimit: &zero,
	})
	require.NoError(t, err)
	require.NotNil(t, limited.SpendingLimit)
	assert.Equal(t, int64(0), *limited.SpendingLimit)
	assert.True(t, limited.HasSpendingLimit(), "a zero limit is a bound too")

	unlimited, err := svc.CreateEmployee(t.Context(), EmployeeInput{
		CompanyID: company.ID, CustomerID: "cust_B",
	})
	require.NoError(t, err)
	assert.False(t, unlimited.HasSpendingLimit())
}

// TestTheEmployeeListFillsCustomerIDsInOneQuery pins ADR 0004's N+1 ban.
//
// The customer ids come from link; a separate bond read per record would mean
// the number of queries growing as the page grows.
func TestTheEmployeeListFillsCustomerIDsInOneQuery(t *testing.T) {
	svc, _, links := newTestService(t)
	company := newTestCompany(t, svc)

	for _, id := range []string{"cust_A", "cust_B", "cust_C"} {
		_, err := svc.CreateEmployee(t.Context(), EmployeeInput{CompanyID: company.ID, CustomerID: id})
		require.NoError(t, err)
	}
	links.calls["ListMany"] = 0

	page, err := svc.ListEmployees(t.Context(), ListEmployeesInput{CompanyID: &company.ID})
	require.NoError(t, err)

	require.Len(t, page.Items, 3)
	assert.Equal(t, int64(3), page.Count)
	assert.Equal(t, 1, links.calls["ListMany"], "there has to be ONE bond query, whatever the number of records")
	for _, e := range page.Items {
		assert.NotEmpty(t, e.CustomerID, "every record's customer id has to be filled in")
	}
}

// TestListingAppliesThePagingLimits verifies that the default and the upper
// bound are applied.
//
// An excessive limit is NOT CLIPPED but rejected: a silently clipped limit
// misreports the page size to the client, and the paging loop reads the same
// records again.
func TestListingAppliesThePagingLimits(t *testing.T) {
	svc, _, _ := newTestService(t)
	newTestCompany(t, svc)

	page, err := svc.ListCompanies(t.Context(), ListCompaniesInput{})
	require.NoError(t, err)
	assert.Equal(t, DefaultLimit, page.Limit, "if no limit is given the default has to apply")

	_, err = svc.ListCompanies(t.Context(), ListCompaniesInput{Limit: MaxLimit + 1})
	assert.True(t, errors.IsInvalid(err), "a limit above the upper bound has to be rejected")

	_, err = svc.ListEmployees(t.Context(), ListEmployeesInput{Offset: -1})
	assert.True(t, errors.IsInvalid(err), "a negative offset has to be rejected")
}

// TestTheCompanyFilterIsNormalized verifies that the filter value is turned
// into the storage form too; had it not been, an upper-case e-mail address
// would find no record.
func TestTheCompanyFilterIsNormalized(t *testing.T) {
	svc, _, _ := newTestService(t)
	newTestCompany(t, svc)

	wanted := "MUHASEBE@acme.EXAMPLE"
	page, err := svc.ListCompanies(t.Context(), ListCompaniesInput{Email: &wanted})
	require.NoError(t, err)
	assert.Len(t, page.Items, 1)
}

// TestUpdateCompanyCannotRemoveARequirement pins the bound of a partial
// update: a field that is not given does not change, but a GIVEN field cannot
// be emptied.
func TestUpdateCompanyCannotRemoveARequirement(t *testing.T) {
	svc, _, _ := newTestService(t)
	company := newTestCompany(t, svc)

	empty := ""
	_, err := svc.UpdateCompany(t.Context(), company.ID, UpdateCompanyInput{Name: &empty})
	assert.True(t, errors.IsInvalid(err), "the name cannot be emptied")

	_, err = svc.UpdateCompany(t.Context(), company.ID, UpdateCompanyInput{CurrencyCode: &empty})
	assert.True(t, errors.IsInvalid(err), "the currency cannot be emptied")

	_, err = svc.UpdateCompany(t.Context(), company.ID, UpdateCompanyInput{SpendingLimitResetPeriod: &empty})
	assert.True(t, errors.IsInvalid(err),
		"a period given empty must not silently fall back to 'never'")

	// The address fields, on the other hand, really can be cleared.
	updated, err := svc.UpdateCompany(t.Context(), company.ID, UpdateCompanyInput{PostalCode: &empty})
	require.NoError(t, err)
	assert.Empty(t, updated.PostalCode)
}
