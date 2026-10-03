package service

import (
	"context"
	"log/slog"
	"strings"

	"github.com/bdrtr/gobit/internal/modules/b2b/models"
)

// CompanyInput is the write input of a company.
type CompanyInput struct {
	// Name is the company's trade name; it is required.
	Name string
	// Email is the company's contact address; it is required, it is stored
	// normalized to lower case, and it is NOT UNIQUE.
	Email string
	// Phone is the company's telephone number; it may be left empty.
	Phone string
	// Address is the street line of the billing address; it may be left empty.
	Address string
	// City is the city; it may be left empty.
	City string
	// PostalCode is the postal code; it may be left empty.
	PostalCode string
	// CountryCode is the ISO 3166-1 alpha-2 country code; it may be left empty.
	CountryCode string
	// CurrencyCode is the ISO 4217 currency code; it is REQUIRED. Spending
	// limits are expressed in this currency.
	CurrencyCode string
	// SpendingLimitResetPeriod is the interval at which employee limits reset;
	// if left empty, [models.ResetNever] applies.
	SpendingLimitResetPeriod string
}

// CreateCompany creates a new company.
//
// E-mail uniqueness is NOT REQUIRED: no identity is built on the e-mail
// address in this module, and two legal entities of the same holding may share
// the same accounting address (the reasoning is in the table documentation in
// the migration).
func (s *Service) CreateCompany(ctx context.Context, in CompanyInput) (models.Company, error) {
	company, err := s.validateCompanyInput(in)
	if err != nil {
		return models.Company{}, err
	}

	now := s.clock()
	company.ID = models.NewCompanyID(now)
	company.CreatedAt = now

	created, err := s.repo.CreateCompany(ctx, company)
	if err != nil {
		return models.Company{}, err
	}

	s.log.InfoContext(ctx, "company created",
		slog.String("company_id", created.ID),
		slog.String("currency_code", created.CurrencyCode),
	)
	return created, nil
}

// validateCompanyInput validates the input and turns it into the model to be
// stored.
//
// The id and time fields are NOT filled in HERE: the validation stays pure,
// and because it does not touch the time source it is deterministic in a test.
func (s *Service) validateCompanyInput(in CompanyInput) (models.Company, error) {
	if err := requireText("company name", in.Name); err != nil {
		return models.Company{}, err
	}
	name := strings.TrimSpace(in.Name)
	if err := checkLen("company name", name, models.MaxNameLen); err != nil {
		return models.Company{}, err
	}

	email, err := normalizeEmail(in.Email)
	if err != nil {
		return models.Company{}, err
	}
	if err := checkLen("phone", in.Phone, models.MaxPhoneLen); err != nil {
		return models.Company{}, err
	}
	if err := checkLen("address", in.Address, models.MaxAddressLen); err != nil {
		return models.Company{}, err
	}
	if err := checkLen("city", in.City, models.MaxAddressLen); err != nil {
		return models.Company{}, err
	}
	if err := checkLen("postal code", in.PostalCode, models.MaxPostalCodeLen); err != nil {
		return models.Company{}, err
	}

	country, err := normalizeCountryCode(in.CountryCode)
	if err != nil {
		return models.Company{}, err
	}
	currency, err := normalizeCurrencyCode(in.CurrencyCode)
	if err != nil {
		return models.Company{}, err
	}
	period, err := normalizeResetPeriod(in.SpendingLimitResetPeriod)
	if err != nil {
		return models.Company{}, err
	}

	return models.Company{
		Name:                     name,
		Email:                    email,
		Phone:                    strings.TrimSpace(in.Phone),
		Address:                  strings.TrimSpace(in.Address),
		City:                     strings.TrimSpace(in.City),
		PostalCode:               strings.TrimSpace(in.PostalCode),
		CountryCode:              country,
		CurrencyCode:             currency,
		SpendingLimitResetPeriod: period,
	}, nil
}

// GetCompany returns a company by id; errors.NotFound if there is none.
func (s *Service) GetCompany(ctx context.Context, id string) (models.Company, error) {
	if err := requireID(id, models.CompanyIDPrefix, "company id"); err != nil {
		return models.Company{}, err
	}
	return s.repo.GetCompany(ctx, id)
}

// ListCompaniesInput is the input of the company listing.
type ListCompaniesInput struct {
	// Email, if given, returns only the companies with this e-mail address;
	// the result can contain MORE THAN ONE record.
	Email *string
	// Limit is the page size; if 0, [DefaultLimit] applies.
	Limit int64
	// Offset is the number of records to skip.
	Offset int64
}

// ListCompanies lists companies, filtered and paged.
func (s *Service) ListCompanies(ctx context.Context, in ListCompaniesInput) (Page[models.Company], error) {
	limit, offset, err := normalizePaging(in.Limit, in.Offset)
	if err != nil {
		return Page[models.Company]{}, err
	}

	filter := models.CompanyFilter{}
	if in.Email != nil {
		// The filter value is turned into the STORAGE form too: the column holds
		// the lower-case value, and a filter that was not normalized would find
		// no row.
		email := models.NormalizeEmail(*in.Email)
		filter.Email = &email
	}

	items, total, err := s.repo.ListCompanies(ctx, filter, limit, offset)
	if err != nil {
		return Page[models.Company]{}, err
	}
	return Page[models.Company]{Items: items, Count: total, Limit: limit, Offset: offset}, nil
}

// UpdateCompanyInput is the partial update input of a company.
//
// A nil field means "leave it alone", a filled field means "write this value";
// in the address fields an empty string is a real clearing.
type UpdateCompanyInput struct {
	// Name is the new trade name; if given it cannot be empty.
	Name *string
	// Email is the new e-mail address; if given it is validated and normalized.
	Email *string
	// Phone is the new telephone number.
	Phone *string
	// Address is the new street line of the address.
	Address *string
	// City is the new city.
	City *string
	// PostalCode is the new postal code.
	PostalCode *string
	// CountryCode is the new country code; an empty string clears the address.
	CountryCode *string
	// CurrencyCode is the new currency code; if given it cannot be empty.
	CurrencyCode *string
	// SpendingLimitResetPeriod is the new reset interval.
	//
	// Changing it works RETROACTIVELY: the new window is counted from the start
	// of the current calendar month or year (see [models.SpendingResetPeriod]).
	// This is deliberate — the alternative was to store the moment of the
	// change separately for every employee and to detach the reset from the
	// calendar.
	SpendingLimitResetPeriod *string
}

// UpdateCompany updates the given fields of a company; errors.NotFound if
// there is none.
//
// The given fields pass the SAME validation as on creation: a partial update
// can skip a field, but it cannot remove an existing requirement.
func (s *Service) UpdateCompany(
	ctx context.Context,
	id string,
	in UpdateCompanyInput,
) (models.Company, error) {
	if err := requireID(id, models.CompanyIDPrefix, "company id"); err != nil {
		return models.Company{}, err
	}

	patch, err := s.validateCompanyPatch(in)
	if err != nil {
		return models.Company{}, err
	}
	return s.repo.UpdateCompany(ctx, id, patch, s.clock())
}

// validateCompanyPatch turns the partial update input into a validated
// patch.
func (s *Service) validateCompanyPatch(in UpdateCompanyInput) (models.CompanyPatch, error) {
	var patch models.CompanyPatch

	if in.Name != nil {
		if err := requireText("company name", *in.Name); err != nil {
			return models.CompanyPatch{}, err
		}
		name := strings.TrimSpace(*in.Name)
		if err := checkLen("company name", name, models.MaxNameLen); err != nil {
			return models.CompanyPatch{}, err
		}
		patch.Name = &name
	}
	if in.Email != nil {
		email, err := normalizeEmail(*in.Email)
		if err != nil {
			return models.CompanyPatch{}, err
		}
		patch.Email = &email
	}

	textFields := []struct {
		dst   **string
		src   *string
		label string
		limit int
	}{
		{&patch.Phone, in.Phone, "phone", models.MaxPhoneLen},
		{&patch.Address, in.Address, "address", models.MaxAddressLen},
		{&patch.City, in.City, "city", models.MaxAddressLen},
		{&patch.PostalCode, in.PostalCode, "postal code", models.MaxPostalCodeLen},
	}
	for _, f := range textFields {
		if f.src == nil {
			continue
		}
		value := strings.TrimSpace(*f.src)
		if err := checkLen(f.label, value, f.limit); err != nil {
			return models.CompanyPatch{}, err
		}
		*f.dst = &value
	}

	if in.CountryCode != nil {
		country, err := normalizeCountryCode(*in.CountryCode)
		if err != nil {
			return models.CompanyPatch{}, err
		}
		patch.CountryCode = &country
	}
	if in.CurrencyCode != nil {
		currency, err := normalizeCurrencyCode(*in.CurrencyCode)
		if err != nil {
			return models.CompanyPatch{}, err
		}
		patch.CurrencyCode = &currency
	}
	if in.SpendingLimitResetPeriod != nil {
		// An empty string is NOT "never" here: on an update an empty value
		// would silently pull the field to the most restrictive option, and the
		// client may not have asked for that. That is the difference between
		// the default on creation and the refusal on update.
		if err := requireText("spending limit reset period", *in.SpendingLimitResetPeriod); err != nil {
			return models.CompanyPatch{}, err
		}
		period, err := normalizeResetPeriod(*in.SpendingLimitResetPeriod)
		if err != nil {
			return models.CompanyPatch{}, err
		}
		patch.SpendingLimitResetPeriod = &period
	}

	return patch, nil
}

// DeleteCompany soft-deletes a company and its EMPLOYEES; errors.NotFound if
// there is none.
//
// # Decision: the employees are deleted with the company
//
// The alternative was to leave the employee records in place, and those
// records would be left OWNERLESS in the storefront: the "my own company"
// question would resolve to a company that can no longer be read, while the
// customer would see a record still carrying a spending limit — with no legal
// entity behind it to pay. So the invariant is this: a live employee record
// ALWAYS belongs to a live company.
//
// The customer bonds are removed too, and this is the most critical step of
// the deletion: since the bond is unique, a dangling row means that customer
// can never again be added as an employee of ANY company. Removing the bonds
// is OUTSIDE the database transaction (link is a separate subsystem); if it
// fails, no error is returned and a warning is logged — the deletion has
// already happened, and returning an error to the caller would give the
// impression that "the company was not deleted".
func (s *Service) DeleteCompany(ctx context.Context, id string) error {
	if err := requireID(id, models.CompanyIDPrefix, "company id"); err != nil {
		return err
	}

	employeeIDs, err := s.repo.DeleteCompany(ctx, id, s.clock())
	if err != nil {
		return err
	}
	s.unlinkCustomers(ctx, employeeIDs)

	s.log.InfoContext(ctx, "company deleted",
		slog.String("company_id", id),
		slog.Int("deleted_employees", len(employeeIDs)),
	)
	return nil
}
