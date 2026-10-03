package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/modules/b2b/models"
	"github.com/bdrtr/gobit/internal/modules/b2b/repository/b2bdb"
)

// CreateCompany writes a new company.
func (r *Repo) CreateCompany(ctx context.Context, c models.Company) (models.Company, error) {
	if err := r.ready(); err != nil {
		return models.Company{}, err
	}

	row, err := r.q.InsertCompany(ctx, b2bdb.InsertCompanyParams{
		ID:                       c.ID,
		Name:                     c.Name,
		Email:                    c.Email,
		Phone:                    c.Phone,
		Address:                  c.Address,
		City:                     c.City,
		PostalCode:               c.PostalCode,
		CountryCode:              c.CountryCode,
		CurrencyCode:             c.CurrencyCode,
		SpendingLimitResetPeriod: string(c.SpendingLimitResetPeriod),
		CreatedAt:                fromTime(c.CreatedAt),
	})
	if err != nil {
		return models.Company{}, wrapDB(err, "the company could not be created")
	}
	return toCompany(row), nil
}

// GetCompany returns a company by id; errors.NotFound if there is none.
func (r *Repo) GetCompany(ctx context.Context, id string) (models.Company, error) {
	if err := r.ready(); err != nil {
		return models.Company{}, err
	}

	row, err := r.q.GetCompany(ctx, id)
	if err != nil {
		return models.Company{}, notFoundOr(err, CodeCompanyNotFound, "company not found: %s", id)
	}
	return toCompany(row), nil
}

// ListCompanies returns the paged company list and the TOTAL record count.
func (r *Repo) ListCompanies(
	ctx context.Context,
	filter models.CompanyFilter,
	limit, offset int64,
) ([]models.Company, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	rows, err := r.q.ListCompanies(ctx, b2bdb.ListCompaniesParams{
		Email: filter.Email,
		Lim:   toInt32(limit),
		Off:   toInt32(offset),
	})
	if err != nil {
		return nil, 0, wrapDB(err, "the company list could not be read")
	}

	total, err := r.q.CountCompanies(ctx, filter.Email)
	if err != nil {
		return nil, 0, wrapDB(err, "the companies could not be counted")
	}
	return toCompanies(rows), total, nil
}

// UpdateCompany updates the given fields of a company; errors.NotFound if
// there is none.
func (r *Repo) UpdateCompany(
	ctx context.Context,
	id string,
	patch models.CompanyPatch,
	now time.Time,
) (models.Company, error) {
	if err := r.ready(); err != nil {
		return models.Company{}, err
	}

	var resetPeriod *string
	if patch.SpendingLimitResetPeriod != nil {
		value := string(*patch.SpendingLimitResetPeriod)
		resetPeriod = &value
	}

	row, err := r.q.UpdateCompany(ctx, b2bdb.UpdateCompanyParams{
		ID:           id,
		Name:         patch.Name,
		Email:        patch.Email,
		Phone:        patch.Phone,
		Address:      patch.Address,
		City:         patch.City,
		PostalCode:   patch.PostalCode,
		CountryCode:  patch.CountryCode,
		CurrencyCode: patch.CurrencyCode,
		ResetPeriod:  resetPeriod,
		UpdatedAt:    fromTime(now),
	})
	if err != nil {
		return models.Company{}, notFoundOr(err, CodeCompanyNotFound, "company not found: %s", id)
	}
	return toCompany(row), nil
}

// DeleteCompany soft-deletes a company and its EMPLOYEES; errors.NotFound if
// the company does not exist. The returned slice holds the ids of the deleted
// employees.
//
// # Why the employees are deleted too
//
// A dangling employee record would have no owner in the storefront: the "my
// own company" question would resolve to a company that can no longer be read
// (soft-deleted), and the customer would either get a 404 or see a deleted
// company. The second is worse — the record still carries a spending limit,
// and there is no legal entity behind that limit to pay. So the invariant is
// this: a LIVE employee record ALWAYS belongs to a live company.
//
// Both are done in ONE transaction; an error between them would produce exactly
// the forbidden state. The employees' customer BONDS are removed not here but
// in the service layer (link is a separate subsystem and cannot take part in
// this transaction); that is also why the ids are returned.
func (r *Repo) DeleteCompany(ctx context.Context, id string, now time.Time) ([]string, error) {
	var employeeIDs []string

	err := r.inTx(ctx, func(q *b2bdb.Queries) error {
		if _, err := q.SoftDeleteCompany(ctx, b2bdb.SoftDeleteCompanyParams{
			ID:        id,
			DeletedAt: fromTime(now),
		}); err != nil {
			return notFoundOr(err, CodeCompanyNotFound, "company not found: %s", id)
		}

		ids, err := q.SoftDeleteEmployeesOfCompany(ctx, b2bdb.SoftDeleteEmployeesOfCompanyParams{
			CompanyID: id,
			DeletedAt: fromTime(now),
		})
		if err != nil {
			return wrapDB(err, "the company's employees could not be deleted: %s", id)
		}
		employeeIDs = ids
		return nil
	})
	if err != nil {
		return nil, err
	}
	return employeeIDs, nil
}

// toCompany turns a generated row into the domain model.
func toCompany(row b2bdb.B2bCompany) models.Company {
	return models.Company{
		ID:                       row.ID,
		Name:                     row.Name,
		Email:                    row.Email,
		Phone:                    row.Phone,
		Address:                  row.Address,
		City:                     row.City,
		PostalCode:               row.PostalCode,
		CountryCode:              row.CountryCode,
		CurrencyCode:             row.CurrencyCode,
		SpendingLimitResetPeriod: models.SpendingResetPeriod(row.SpendingLimitResetPeriod),
		CreatedAt:                toTime(row.CreatedAt),
		UpdatedAt:                toTime(row.UpdatedAt),
		DeletedAt:                toTimePtr(row.DeletedAt),
	}
}

// toCompanies turns a slice of rows into domain models.
func toCompanies(rows []b2bdb.B2bCompany) []models.Company {
	out := make([]models.Company, 0, len(rows))
	for i := range rows {
		out = append(out, toCompany(rows[i]))
	}
	return out
}
