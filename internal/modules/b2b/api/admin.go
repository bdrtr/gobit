package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/b2b/service"
)

// --- şirketler ----------------------------------------------------------------

// adminCreateCompany yeni bir şirket oluşturur (POST /admin/v1/b2b/companies).
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

// adminListCompanies şirketleri süzerek ve sayfalayarak listeler
// (GET /admin/v1/b2b/companies).
//
// "email" süzgeci BİRDEN ÇOK kayıt getirebilir: şirket e-postası benzersiz
// değildir (bkz. models.Company).
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

// adminGetCompany tek bir şirketi döner (GET /admin/v1/b2b/companies/{id}).
func (h *Handler) adminGetCompany(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	company, err := h.svc.GetCompany(ctx, pathParam(r, paramID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toCompanyDTO(company))
}

// adminUpdateCompany şirketin verilen alanlarını günceller
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

// adminDeleteCompany şirketi ve ÇALIŞANLARINI yumuşak siler
// (DELETE /admin/v1/b2b/companies/{id}).
//
// Çalışanların da silinmesi bilinçlidir: sahipsiz kalan bir çalışan kaydı,
// vitrinde artık okunamayan bir şirkete çözülürdü (bkz. service.DeleteCompany).
func (h *Handler) adminDeleteCompany(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.DeleteCompany(ctx, pathParam(r, paramID)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// --- çalışanlar ---------------------------------------------------------------

// adminCreateEmployee şirkete yeni bir çalışan ekler
// (POST /admin/v1/b2b/employees).
//
// Müşteri başka bir şirketin çalışanıysa 409 döner; sınıflandırma servisten
// (ve nihayetinde link tablosunun benzersizlik kısıtından) gelir, handler
// status seçmez.
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

// adminListEmployees çalışanları süzerek ve sayfalayarak listeler
// (GET /admin/v1/b2b/employees).
//
// Süzgeçler: company_id, is_company_admin.
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

// adminGetEmployee tek bir çalışanı döner
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

// adminUpdateEmployee çalışanın harcama yetkisini günceller
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

// adminDeleteEmployee çalışanı yumuşak siler ve müşteri bağını kaldırır
// (DELETE /admin/v1/b2b/employees/{id}).
func (h *Handler) adminDeleteEmployee(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.DeleteEmployee(ctx, pathParam(r, paramID)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}
