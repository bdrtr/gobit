package api_test

import (
	"context"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/b2b/api"
	"github.com/bdrtr/gobit/internal/modules/b2b/models"
	"github.com/bdrtr/gobit/internal/modules/b2b/service"
)

// stubB2B [api.B2B]'nin testler için betiklenebilir uygulamasıdır.
//
// HTTP katmanı testleri servisin İŞ MANTIĞINI değil taşımayı sınar:
// yönlendirme, gövde çözümleme, zarf biçimi ve hata sınıfının status koduna
// çevrilmesi. Bu yüzden servis yerine betiklenebilir bir sahte kullanılır;
// testin dönmesini istediği tipli hata doğrudan verilir ve beklenen status kodu
// ölçülür.
//
// Betiklenmemiş bir metot çağrılırsa tipli bir hata döner: sessiz sıfır değer,
// testin yanlış nedenle geçmesine yol açardı.
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

	// son çağrının argümanları; handler'ın doğru değerleri ilettiğini kanıtlar.
	sonID              string
	sonCustomerID      string
	sonCompanyInput    service.CompanyInput
	sonEmployeeInput   service.EmployeeInput
	sonEmployeeUpdate  service.UpdateEmployeeInput
	sonCompanyListArgs service.ListCompaniesInput
	sonEmployeeList    service.ListEmployeesInput
}

var _ api.B2B = (*stubB2B)(nil)

// unset betiklenmemiş bir metot çağrıldığında dönen hatadır.
func unset(name string) error {
	return errors.Internal("stub_unset", "%s testte betiklenmedi", name)
}

func (s *stubB2B) CreateCompany(ctx context.Context, in service.CompanyInput) (models.Company, error) {
	s.sonCompanyInput = in
	if s.createCompanyFn == nil {
		return models.Company{}, unset("CreateCompany")
	}
	return s.createCompanyFn(ctx, in)
}

func (s *stubB2B) GetCompany(ctx context.Context, id string) (models.Company, error) {
	s.sonID = id
	if s.getCompanyFn == nil {
		return models.Company{}, unset("GetCompany")
	}
	return s.getCompanyFn(ctx, id)
}

func (s *stubB2B) ListCompanies(
	ctx context.Context,
	in service.ListCompaniesInput,
) (service.Page[models.Company], error) {
	s.sonCompanyListArgs = in
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
	s.sonID = id
	if s.updateCompanyFn == nil {
		return models.Company{}, unset("UpdateCompany")
	}
	return s.updateCompanyFn(ctx, id, in)
}

func (s *stubB2B) DeleteCompany(ctx context.Context, id string) error {
	s.sonID = id
	if s.deleteCompanyFn == nil {
		return unset("DeleteCompany")
	}
	return s.deleteCompanyFn(ctx, id)
}

func (s *stubB2B) CreateEmployee(
	ctx context.Context,
	in service.EmployeeInput,
) (models.CompanyEmployee, error) {
	s.sonEmployeeInput = in
	if s.createEmployeeFn == nil {
		return models.CompanyEmployee{}, unset("CreateEmployee")
	}
	return s.createEmployeeFn(ctx, in)
}

func (s *stubB2B) GetEmployee(ctx context.Context, id string) (models.CompanyEmployee, error) {
	s.sonID = id
	if s.getEmployeeFn == nil {
		return models.CompanyEmployee{}, unset("GetEmployee")
	}
	return s.getEmployeeFn(ctx, id)
}

func (s *stubB2B) ListEmployees(
	ctx context.Context,
	in service.ListEmployeesInput,
) (service.Page[models.CompanyEmployee], error) {
	s.sonEmployeeList = in
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
	s.sonID = id
	s.sonEmployeeUpdate = in
	if s.updateEmployeeFn == nil {
		return models.CompanyEmployee{}, unset("UpdateEmployee")
	}
	return s.updateEmployeeFn(ctx, id, in)
}

func (s *stubB2B) DeleteEmployee(ctx context.Context, id string) error {
	s.sonID = id
	if s.deleteEmployeeFn == nil {
		return unset("DeleteEmployee")
	}
	return s.deleteEmployeeFn(ctx, id)
}

func (s *stubB2B) MembershipOfCustomer(
	ctx context.Context,
	customerID string,
) (service.Membership, error) {
	s.sonCustomerID = customerID
	if s.membershipFn == nil {
		return service.Membership{}, unset("MembershipOfCustomer")
	}
	return s.membershipFn(ctx, customerID)
}
