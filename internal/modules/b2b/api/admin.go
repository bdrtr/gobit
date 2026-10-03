package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/b2b/service"
)

// --- companies ---------------------------------------------------------------

// adminCreateCompany creates a new company (POST /admin/v1/b2b/companies).
func (h *Handler) adminCreateCompany(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req companyRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	created, err := h.svc.CreateCompany(ctx, service.CompanyInput{
		Name:                     req.Name,
		Email:                    req.Email,
		Phone:                    req.Phone,
		Address:                  req.Address,
		City:                     req.City,
		PostalCode:               req.PostalCode,
		CountryCode:              req.CountryCode,
		CurrencyCode:             req.CurrencyCode,
		SpendingLimitResetPeriod: req.SpendingLimitResetPeriod,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toCompanyDTO(created))
}

// adminListCompanies lists companies, filtered and paged
// (GET /admin/v1/b2b/companies).
//
// The "email" filter can bring back MORE THAN ONE record: a company's e-mail
// address is not unique (see models.Company).
func (h *Handler) adminListCompanies(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, offset, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	page, err := h.svc.ListCompanies(ctx, service.ListCompaniesInput{
		Email:  stringParam(r, "email"),
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writePage(w, r, page, toCompanyDTO)
}

// adminGetCompany returns a single company (GET /admin/v1/b2b/companies/{id}).
func (h *Handler) adminGetCompany(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	company, err := h.svc.GetCompany(ctx, pathParam(r, paramID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toCompanyDTO(company))
}

// adminUpdateCompany updates the given fields of a company
// (PUT /admin/v1/b2b/companies/{id}).
func (h *Handler) adminUpdateCompany(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req updateCompanyRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	updated, err := h.svc.UpdateCompany(ctx, pathParam(r, paramID), service.UpdateCompanyInput{
		Name:                     req.Name,
		Email:                    req.Email,
		Phone:                    req.Phone,
		Address:                  req.Address,
		City:                     req.City,
		PostalCode:               req.PostalCode,
		CountryCode:              req.CountryCode,
		CurrencyCode:             req.CurrencyCode,
		SpendingLimitResetPeriod: req.SpendingLimitResetPeriod,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toCompanyDTO(updated))
}

// adminDeleteCompany soft-deletes a company and its EMPLOYEES
// (DELETE /admin/v1/b2b/companies/{id}).
//
// Deleting the employees too is deliberate: an employee record left without an
// owner would resolve, in the storefront, to a company that can no longer be
// read (see service.DeleteCompany).
func (h *Handler) adminDeleteCompany(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.DeleteCompany(ctx, pathParam(r, paramID)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// --- employees ---------------------------------------------------------------

// adminCreateEmployee adds a new employee to a company
// (POST /admin/v1/b2b/employees).
//
// If the customer is already an employee of another company it answers 409;
// the classification comes from the service (and ultimately from the link
// table's uniqueness constraint), and the handler does not choose a status.
func (h *Handler) adminCreateEmployee(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req employeeRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	created, err := h.svc.CreateEmployee(ctx, service.EmployeeInput{
		CompanyID:      req.CompanyID,
		CustomerID:     req.CustomerID,
		SpendingLimit:  req.SpendingLimit,
		IsCompanyAdmin: req.IsCompanyAdmin,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toEmployeeDTO(created))
}

// adminListEmployees lists employees, filtered and paged
// (GET /admin/v1/b2b/employees).
//
// Filters: company_id, is_company_admin.
func (h *Handler) adminListEmployees(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, offset, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	isAdmin, err := boolParam(r, "is_company_admin")
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	page, err := h.svc.ListEmployees(ctx, service.ListEmployeesInput{
		CompanyID:      stringParam(r, "company_id"),
		IsCompanyAdmin: isAdmin,
		Limit:          limit,
		Offset:         offset,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writePage(w, r, page, toEmployeeDTO)
}

// adminGetEmployee returns a single employee
// (GET /admin/v1/b2b/employees/{id}).
func (h *Handler) adminGetEmployee(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	employee, err := h.svc.GetEmployee(ctx, pathParam(r, paramID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toEmployeeDTO(employee))
}

// adminUpdateEmployee updates an employee's spending authority
// (PUT /admin/v1/b2b/employees/{id}).
func (h *Handler) adminUpdateEmployee(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req updateEmployeeRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	updated, err := h.svc.UpdateEmployee(ctx, pathParam(r, paramID), service.UpdateEmployeeInput{
		SpendingLimit:      req.SpendingLimit,
		ClearSpendingLimit: req.ClearSpendingLimit,
		IsCompanyAdmin:     req.IsCompanyAdmin,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toEmployeeDTO(updated))
}

// adminDeleteEmployee soft-deletes an employee and removes the customer bond
// (DELETE /admin/v1/b2b/employees/{id}).
func (h *Handler) adminDeleteEmployee(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.DeleteEmployee(ctx, pathParam(r, paramID)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}
