package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/modules/b2b/models"
	"github.com/bdrtr/gobit/internal/modules/b2b/repository/b2bdb"
)

// CreateEmployee writes a new employee.
//
// [models.CompanyEmployee.CustomerID] is IGNORED: the customer bond lives not
// in the schema but in the link table, and the service layer is what
// establishes it. If the company does not exist the foreign key violation is
// turned into errors.Invalid; the caller can produce a better error by
// verifying beforehand that the company exists.
func (r *Repo) CreateEmployee(ctx context.Context, e models.CompanyEmployee) (models.CompanyEmployee, error) {
	if err := r.ready(); err != nil {
		return models.CompanyEmployee{}, err
	}

	row, err := r.q.InsertEmployee(ctx, b2bdb.InsertEmployeeParams{
		ID:             e.ID,
		CompanyID:      e.CompanyID,
		SpendingLimit:  e.SpendingLimit,
		IsCompanyAdmin: e.IsCompanyAdmin,
		CreatedAt:      fromTime(e.CreatedAt),
	})
	if err != nil {
		return models.CompanyEmployee{}, wrapDB(err, "the employee could not be created")
	}
	return toEmployee(row), nil
}

// GetEmployee returns an employee by id; errors.NotFound if there is none.
func (r *Repo) GetEmployee(ctx context.Context, id string) (models.CompanyEmployee, error) {
	if err := r.ready(); err != nil {
		return models.CompanyEmployee{}, err
	}

	row, err := r.q.GetEmployee(ctx, id)
	if err != nil {
		return models.CompanyEmployee{}, notFoundOr(err, CodeEmployeeNotFound, "employee not found: %s", id)
	}
	return toEmployee(row), nil
}

// ListEmployees returns the paged employee list and the TOTAL record count.
func (r *Repo) ListEmployees(
	ctx context.Context,
	filter models.EmployeeFilter,
	limit, offset int64,
) ([]models.CompanyEmployee, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	rows, err := r.q.ListEmployees(ctx, b2bdb.ListEmployeesParams{
		CompanyID:      filter.CompanyID,
		IsCompanyAdmin: filter.IsCompanyAdmin,
		Lim:            toInt32(limit),
		Off:            toInt32(offset),
	})
	if err != nil {
		return nil, 0, wrapDB(err, "the employee list could not be read")
	}

	total, err := r.q.CountEmployees(ctx, b2bdb.CountEmployeesParams{
		CompanyID:      filter.CompanyID,
		IsCompanyAdmin: filter.IsCompanyAdmin,
	})
	if err != nil {
		return nil, 0, wrapDB(err, "the employees could not be counted")
	}
	return toEmployees(rows), total, nil
}

// UpdateEmployee updates the given fields of an employee; errors.NotFound if
// there is none.
func (r *Repo) UpdateEmployee(
	ctx context.Context,
	id string,
	patch models.EmployeePatch,
	now time.Time,
) (models.CompanyEmployee, error) {
	if err := r.ready(); err != nil {
		return models.CompanyEmployee{}, err
	}

	row, err := r.q.UpdateEmployee(ctx, b2bdb.UpdateEmployeeParams{
		ID:             id,
		ClearLimit:     patch.ClearSpendingLimit,
		SpendingLimit:  patch.SpendingLimit,
		IsCompanyAdmin: patch.IsCompanyAdmin,
		UpdatedAt:      fromTime(now),
	})
	if err != nil {
		return models.CompanyEmployee{}, notFoundOr(err, CodeEmployeeNotFound,
			"employee not found: %s", id)
	}
	return toEmployee(row), nil
}

// DeleteEmployee soft-deletes an employee; errors.NotFound if there is none.
//
// The customer BOND is not removed here; the service layer is what removes it
// (link is a separate subsystem). Removing the bond is mandatory: if it stays,
// the customer, because the bond is unique, can never again be added as an
// employee of any company.
func (r *Repo) DeleteEmployee(ctx context.Context, id string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	if _, err := r.q.SoftDeleteEmployee(ctx, b2bdb.SoftDeleteEmployeeParams{
		ID:        id,
		DeletedAt: fromTime(now),
	}); err != nil {
		return notFoundOr(err, CodeEmployeeNotFound, "employee not found: %s", id)
	}
	return nil
}

// toEmployee turns a generated row into the domain model.
//
// CustomerID stays EMPTY: it has no column, its value comes from link and the
// service layer fills it in (see the package documentation).
func toEmployee(row b2bdb.B2bCompanyEmployee) models.CompanyEmployee {
	return models.CompanyEmployee{
		ID:             row.ID,
		CompanyID:      row.CompanyID,
		SpendingLimit:  row.SpendingLimit,
		IsCompanyAdmin: row.IsCompanyAdmin,
		CreatedAt:      toTime(row.CreatedAt),
		UpdatedAt:      toTime(row.UpdatedAt),
		DeletedAt:      toTimePtr(row.DeletedAt),
	}
}

// toEmployees turns a slice of rows into domain models.
func toEmployees(rows []b2bdb.B2bCompanyEmployee) []models.CompanyEmployee {
	out := make([]models.CompanyEmployee, 0, len(rows))
	for i := range rows {
		out = append(out, toEmployee(rows[i]))
	}
	return out
}
