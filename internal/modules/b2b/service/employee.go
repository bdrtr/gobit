package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/b2b/models"
)

// EmployeeInput is the write input of an employee.
type EmployeeInput struct {
	// CompanyID is the company the employee is bound to; it is required.
	CompanyID string
	// CustomerID is the employee's customer record (customer module); it is
	// required.
	//
	// That the customer REALLY exists is not verified: verifying it would mean
	// a dependency on the customer module, and that dependency is exactly the
	// one the link layer exists to remove (ADR 0001).
	CustomerID string
	// SpendingLimit is the most the employee may spend per window (minor
	// unit); nil means UNLIMITED, 0 is a real zero limit.
	SpendingLimit *int64
	// IsCompanyAdmin is whether the employee is a company admin.
	IsCompanyAdmin bool
}

// CreateEmployee adds a new employee to a company and establishes the customer
// bond.
//
// If the customer is an employee of ANOTHER company it returns
// errors.Conflict; the rule lives in the uniqueness in the link table (see
// [Definitions]) and is not repeated on the application side — had it been
// repeated, the race between two concurrent requests would still be settled by
// the index.
//
// Establishing the bond is NOT in the same transaction as the employee row (the
// link service uses its own connection); that is why the employee is ROLLED
// BACK if the bond cannot be established. The alternative was an employee
// record without a customer staying up: the record carries a spending limit
// but resolves to nobody in the storefront.
func (s *Service) CreateEmployee(ctx context.Context, in EmployeeInput) (models.CompanyEmployee, error) {
	if err := requireID(in.CompanyID, models.CompanyIDPrefix, "company id"); err != nil {
		return models.CompanyEmployee{}, err
	}
	if err := requireID(in.CustomerID, models.CustomerIDPrefix, "customer id"); err != nil {
		return models.CompanyEmployee{}, err
	}
	if err := validateSpendingLimit(in.SpendingLimit); err != nil {
		return models.CompanyEmployee{}, err
	}

	// The company's existence is verified FIRST: a foreign key violation would
	// give the same result, but it would reach the client as a 422, and the
	// right class for a missing resource is errors.NotFound.
	if _, err := s.repo.GetCompany(ctx, in.CompanyID); err != nil {
		return models.CompanyEmployee{}, err
	}

	now := s.clock()
	created, err := s.repo.CreateEmployee(ctx, models.CompanyEmployee{
		ID:             models.NewEmployeeID(now),
		CompanyID:      in.CompanyID,
		SpendingLimit:  in.SpendingLimit,
		IsCompanyAdmin: in.IsCompanyAdmin,
		CreatedAt:      now,
	})
	if err != nil {
		return models.CompanyEmployee{}, err
	}

	if err := s.linkCustomer(ctx, created.ID, in.CustomerID); err != nil {
		s.rollbackEmployee(ctx, created.ID)
		return models.CompanyEmployee{}, err
	}

	created.CustomerID = in.CustomerID
	s.log.InfoContext(ctx, "company employee added",
		slog.String("employee_id", created.ID),
		slog.String("company_id", created.CompanyID),
		slog.String("customer_id", in.CustomerID),
	)
	return created, nil
}

// rollbackEmployee rolls back an employee whose bond could not be established.
//
// It returns NO error: the caller is going to return the original error
// anyway, and if the compensation's error shadowed it the client would see a
// meaningless reason instead of one it can fix. A record that cannot be rolled
// back is logged as a warning; since it is bound to no customer it does not
// show in the storefront, but it has to stay visible.
func (s *Service) rollbackEmployee(ctx context.Context, employeeID string) {
	if err := s.repo.DeleteEmployee(ctx, employeeID, s.clock()); err != nil {
		s.log.WarnContext(ctx, "an employee whose bond could not be established could not be rolled back",
			"employee_id", employeeID, "error", err)
	}
}

// GetEmployee returns an employee by id; errors.NotFound if there is none.
//
// The customer id is read from link and ADDED to the record; it has no column.
func (s *Service) GetEmployee(ctx context.Context, id string) (models.CompanyEmployee, error) {
	if err := requireID(id, models.EmployeeIDPrefix, "employee id"); err != nil {
		return models.CompanyEmployee{}, err
	}

	employee, err := s.repo.GetEmployee(ctx, id)
	if err != nil {
		return models.CompanyEmployee{}, err
	}

	single := []models.CompanyEmployee{employee}
	if err := s.attachCustomerIDs(ctx, single); err != nil {
		return models.CompanyEmployee{}, err
	}
	return single[0], nil
}

// ListEmployeesInput is the input of the employee listing.
type ListEmployeesInput struct {
	// CompanyID, if given, returns only this company's employees.
	CompanyID *string
	// IsCompanyAdmin, if given, filters on whether the employee is an admin.
	IsCompanyAdmin *bool
	// Limit is the page size; if 0, [DefaultLimit] applies.
	Limit int64
	// Offset is the number of records to skip.
	Offset int64
}

// ListEmployees lists employees, filtered and paged.
//
// The customer ids are filled in with ONE extra query; a separate query per
// record would be N+1 (see [Service.attachCustomerIDs]).
func (s *Service) ListEmployees(
	ctx context.Context,
	in ListEmployeesInput,
) (Page[models.CompanyEmployee], error) {
	limit, offset, err := normalizePaging(in.Limit, in.Offset)
	if err != nil {
		return Page[models.CompanyEmployee]{}, err
	}
	if in.CompanyID != nil {
		if err := requireID(*in.CompanyID, models.CompanyIDPrefix, "company id"); err != nil {
			return Page[models.CompanyEmployee]{}, err
		}
	}

	items, total, err := s.repo.ListEmployees(ctx, models.EmployeeFilter{
		CompanyID:      in.CompanyID,
		IsCompanyAdmin: in.IsCompanyAdmin,
	}, limit, offset)
	if err != nil {
		return Page[models.CompanyEmployee]{}, err
	}
	if err := s.attachCustomerIDs(ctx, items); err != nil {
		return Page[models.CompanyEmployee]{}, err
	}
	return Page[models.CompanyEmployee]{Items: items, Count: total, Limit: limit, Offset: offset}, nil
}

// UpdateEmployeeInput is the partial update input of an employee.
//
// The COMPANY and CUSTOMER fields are DELIBERATELY absent: both make up the
// record's identity. If the employee moves to another company the right
// operation is not to move the record but to close the old one and open a new
// one — the spending history belongs to the old company, and moving it would
// silently hand it over to the new company.
type UpdateEmployeeInput struct {
	// SpendingLimit is the new spending limit (minor unit); nil is "leave it
	// alone".
	SpendingLimit *int64
	// ClearSpendingLimit, if true, removes the limit (the employee becomes
	// unlimited).
	//
	// It is a separate flag because the field itself can be nil too: a single
	// pointer cannot tell "leave it alone" from "make it unlimited", and
	// because it cannot, a limit once set could never be removed.
	ClearSpendingLimit bool
	// IsCompanyAdmin is the new value of the admin flag.
	IsCompanyAdmin *bool
}

// UpdateEmployee updates the given fields of an employee; errors.NotFound if
// there is none.
func (s *Service) UpdateEmployee(
	ctx context.Context,
	id string,
	in UpdateEmployeeInput,
) (models.CompanyEmployee, error) {
	if err := requireID(id, models.EmployeeIDPrefix, "employee id"); err != nil {
		return models.CompanyEmployee{}, err
	}
	if in.ClearSpendingLimit && in.SpendingLimit != nil {
		return models.CompanyEmployee{}, errors.Invalid(CodeInvalidInput,
			"the spending limit cannot be set and cleared at the same time")
	}
	if err := validateSpendingLimit(in.SpendingLimit); err != nil {
		return models.CompanyEmployee{}, err
	}

	updated, err := s.repo.UpdateEmployee(ctx, id, models.EmployeePatch{
		SpendingLimit:      in.SpendingLimit,
		ClearSpendingLimit: in.ClearSpendingLimit,
		IsCompanyAdmin:     in.IsCompanyAdmin,
	}, s.clock())
	if err != nil {
		return models.CompanyEmployee{}, err
	}

	single := []models.CompanyEmployee{updated}
	if err := s.attachCustomerIDs(ctx, single); err != nil {
		return models.CompanyEmployee{}, err
	}
	return single[0], nil
}

// DeleteEmployee soft-deletes an employee and removes the customer bond;
// errors.NotFound if the record does not exist.
//
// Removing the bond is an inseparable part of the deletion: since the bond is
// unique, a remaining row means that customer can never again be added as an
// employee of ANY company. And an employee who leaves starting work at another
// company is the ordinary case.
func (s *Service) DeleteEmployee(ctx context.Context, id string) error {
	if err := requireID(id, models.EmployeeIDPrefix, "employee id"); err != nil {
		return err
	}

	if err := s.repo.DeleteEmployee(ctx, id, s.clock()); err != nil {
		return err
	}
	s.unlinkCustomers(ctx, []string{id})

	s.log.InfoContext(ctx, "company employee deleted", slog.String("employee_id", id))
	return nil
}

// Membership is a customer's membership in their company: their own employee
// record, the company they belong to and the start of the current spending
// window.
//
// It is the only view the storefront reads. The three come back together
// because the three answer one question: "on whose behalf, how much and within
// which period can I spend?"
type Membership struct {
	// Employee is the customer's own employee record.
	Employee models.CompanyEmployee
	// Company is the company the employee belongs to.
	Company models.Company
	// SpendingWindowStart is the start of the current spending window; nil if
	// the company's reset period is [models.ResetNever] (there is no window).
	//
	// THE REMAINING ALLOWANCE IS NOT COMPUTED HERE, and that is a deliberate
	// gap: finding the remainder needs the sum of the orders inside the window,
	// and that data belongs to the order module — which is also the module that
	// enforces the limit (see [Interop.SpendingLimitJSON]). A made-up
	// "remaining" field (e.g. the limit itself) would misinform the client; a
	// field that is not given is merely missing.
	SpendingWindowStart *time.Time
}

// MembershipOfCustomer returns the customer's OWN membership; errors.NotFound
// if the customer is not an employee of a company.
//
// # Why somebody else's company cannot be read
//
// This is the storefront's ONLY way to a company, and its input is not a
// company id but a CUSTOMER id. The company is derived from the customer's own
// employee record; there is no endpoint through which a client could ask for a
// company by name (see the api package). A request to "read somebody else's
// company" thereby CANNOT BE EXPRESSED — it is not a request refused by an
// authorization check but one that cannot be put together.
//
// A soft-deleted employee or company is not found: both reads filter on
// deleted_at IS NULL. That is why even a bond left behind (one that could not
// be cleaned up) cannot bring back a deleted record.
func (s *Service) MembershipOfCustomer(ctx context.Context, customerID string) (Membership, error) {
	if err := requireID(customerID, models.CustomerIDPrefix, "customer id"); err != nil {
		return Membership{}, err
	}

	employeeID, err := s.employeeIDOfCustomer(ctx, customerID)
	if err != nil {
		return Membership{}, err
	}
	if employeeID == "" {
		return Membership{}, errors.NotFound(CodeEmployeeNotFound,
			"the customer is not an employee of any company: %s", customerID)
	}

	employee, err := s.repo.GetEmployee(ctx, employeeID)
	if err != nil {
		return Membership{}, err
	}
	employee.CustomerID = customerID

	company, err := s.repo.GetCompany(ctx, employee.CompanyID)
	if err != nil {
		return Membership{}, err
	}

	return Membership{
		Employee:            employee,
		Company:             company,
		SpendingWindowStart: company.SpendingLimitResetPeriod.WindowStart(s.clock()),
	}, nil
}
