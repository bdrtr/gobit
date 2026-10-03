package api_test

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/b2b/api"
	"github.com/bdrtr/gobit/internal/modules/b2b/models"
	"github.com/bdrtr/gobit/internal/modules/b2b/service"
)

// stubB2B is a scriptable implementation of [api.B2B] for the tests.
//
// The HTTP layer's tests exercise the transport, not the service's BUSINESS
// LOGIC: routing, body decoding, envelope shape and the translation of an error
// kind into a status code. That is why a scriptable fake stands in for the
// service; the typed error the test wants returned is handed over directly and
// the expected status code is measured.
//
// Calling a method that was not scripted returns a typed error: a silent zero
// value would let the test pass for the wrong reason.
type stubB2B struct {
	createCompanyFn func(ctx context.Context, in service.CompanyInput) (models.Company, error)
	getCompanyFn    func(ctx context.Context, id string) (models.Company, error)
	listCompaniesFn func(ctx context.Context, in service.ListCompaniesInput) (service.Page[models.Company], error)
	updateCompanyFn func(ctx context.Context, id string, in service.UpdateCompanyInput) (models.Company, error)
	deleteCompanyFn func(ctx context.Context, id string) error

	createEmployeeFn func(ctx context.Context, in service.EmployeeInput) (models.CompanyEmployee, error)
	getEmployeeFn    func(ctx context.Context, id string) (models.CompanyEmployee, error)
	listEmployeesFn  func(ctx context.Context, in service.ListEmployeesInput) (service.Page[models.CompanyEmployee], error)
	updateEmployeeFn func(ctx context.Context, id string, in service.UpdateEmployeeInput) (models.CompanyEmployee, error)
	deleteEmployeeFn func(ctx context.Context, id string) error

	membershipFn func(ctx context.Context, customerID string) (service.Membership, error)

	// The arguments of the last call; they prove the handler passed on the
	// right values.
	lastID              string
	lastCustomerID      string
	lastCompanyInput    service.CompanyInput
	lastEmployeeInput   service.EmployeeInput
	lastEmployeeUpdate  service.UpdateEmployeeInput
	lastCompanyListArgs service.ListCompaniesInput
	lastEmployeeList    service.ListEmployeesInput
}

var _ api.B2B = (*stubB2B)(nil)

// unset is the error returned when a method that was not scripted is called.
func unset(name string) error {
	return errors.Internal("stub_unset", "%s was not scripted in the test", name)
}

func (s *stubB2B) CreateCompany(ctx context.Context, in service.CompanyInput) (models.Company, error) {
	s.lastCompanyInput = in
	if s.createCompanyFn == nil {
		return models.Company{}, unset("CreateCompany")
	}
	return s.createCompanyFn(ctx, in)
}

func (s *stubB2B) GetCompany(ctx context.Context, id string) (models.Company, error) {
	s.lastID = id
	if s.getCompanyFn == nil {
		return models.Company{}, unset("GetCompany")
	}
	return s.getCompanyFn(ctx, id)
}

func (s *stubB2B) ListCompanies(
	ctx context.Context,
	in service.ListCompaniesInput,
) (service.Page[models.Company], error) {
	s.lastCompanyListArgs = in
	if s.listCompaniesFn == nil {
		return service.Page[models.Company]{}, unset("ListCompanies")
	}
	return s.listCompaniesFn(ctx, in)
}

func (s *stubB2B) UpdateCompany(
	ctx context.Context,
	id string,
	in service.UpdateCompanyInput,
) (models.Company, error) {
	s.lastID = id
	if s.updateCompanyFn == nil {
		return models.Company{}, unset("UpdateCompany")
	}
	return s.updateCompanyFn(ctx, id, in)
}

func (s *stubB2B) DeleteCompany(ctx context.Context, id string) error {
	s.lastID = id
	if s.deleteCompanyFn == nil {
		return unset("DeleteCompany")
	}
	return s.deleteCompanyFn(ctx, id)
}

func (s *stubB2B) CreateEmployee(
	ctx context.Context,
	in service.EmployeeInput,
) (models.CompanyEmployee, error) {
	s.lastEmployeeInput = in
	if s.createEmployeeFn == nil {
		return models.CompanyEmployee{}, unset("CreateEmployee")
	}
	return s.createEmployeeFn(ctx, in)
}

func (s *stubB2B) GetEmployee(ctx context.Context, id string) (models.CompanyEmployee, error) {
	s.lastID = id
	if s.getEmployeeFn == nil {
		return models.CompanyEmployee{}, unset("GetEmployee")
	}
	return s.getEmployeeFn(ctx, id)
}

func (s *stubB2B) ListEmployees(
	ctx context.Context,
	in service.ListEmployeesInput,
) (service.Page[models.CompanyEmployee], error) {
	s.lastEmployeeList = in
	if s.listEmployeesFn == nil {
		return service.Page[models.CompanyEmployee]{}, unset("ListEmployees")
	}
	return s.listEmployeesFn(ctx, in)
}

func (s *stubB2B) UpdateEmployee(
	ctx context.Context,
	id string,
	in service.UpdateEmployeeInput,
) (models.CompanyEmployee, error) {
	s.lastID = id
	s.lastEmployeeUpdate = in
	if s.updateEmployeeFn == nil {
		return models.CompanyEmployee{}, unset("UpdateEmployee")
	}
	return s.updateEmployeeFn(ctx, id, in)
}

func (s *stubB2B) DeleteEmployee(ctx context.Context, id string) error {
	s.lastID = id
	if s.deleteEmployeeFn == nil {
		return unset("DeleteEmployee")
	}
	return s.deleteEmployeeFn(ctx, id)
}

func (s *stubB2B) MembershipOfCustomer(
	ctx context.Context,
	customerID string,
) (service.Membership, error) {
	s.lastCustomerID = customerID
	if s.membershipFn == nil {
		return service.Membership{}, unset("MembershipOfCustomer")
	}
	return s.membershipFn(ctx, customerID)
}
