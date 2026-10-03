package service

import (
	"context"
	"sort"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/b2b/models"
	"github.com/bdrtr/gobit/internal/modules/b2b/repository"
)

// memRepo is an in-memory implementation of [Repository].
//
// The fake repository imitates two invariants of the REAL repository: reads
// skip soft-deleted records, and deleting a company deletes its employees too.
// Had it not imitated them, the unit tests would pass believing "the service
// enforces" these rules; but both belong in SQL, and all that is exercised
// here is that the service uses the outcome correctly. That the rules really
// hold in the database is proven separately in the integration test (see
// b2b_integration_test.go).
type memRepo struct {
	companies map[string]models.Company
	employees map[string]models.CompanyEmployee

	// calls is method name -> call count; it is the proof of the batch
	// behavior.
	calls map[string]int
	// failCreateEmployee, if true, makes writing an employee return an error.
	failCreateEmployee bool
}

var _ Repository = (*memRepo)(nil)

// newMemRepo builds an empty in-memory repository.
func newMemRepo() *memRepo {
	return &memRepo{
		companies: map[string]models.Company{},
		employees: map[string]models.CompanyEmployee{},
		calls:     map[string]int{},
	}
}

// record counts a call.
func (m *memRepo) record(name string) { m.calls[name]++ }

// liveCompany returns the live company.
func (m *memRepo) liveCompany(id string) (models.Company, bool) {
	c, ok := m.companies[id]
	if !ok || c.DeletedAt != nil {
		return models.Company{}, false
	}
	return c, true
}

// liveEmployee returns the live employee.
func (m *memRepo) liveEmployee(id string) (models.CompanyEmployee, bool) {
	e, ok := m.employees[id]
	if !ok || e.DeletedAt != nil {
		return models.CompanyEmployee{}, false
	}
	return e, true
}

func (m *memRepo) CreateCompany(_ context.Context, c models.Company) (models.Company, error) {
	m.record("CreateCompany")
	c.UpdatedAt = c.CreatedAt
	m.companies[c.ID] = c
	return c, nil
}

func (m *memRepo) GetCompany(_ context.Context, id string) (models.Company, error) {
	m.record("GetCompany")
	c, ok := m.liveCompany(id)
	if !ok {
		return models.Company{}, errors.NotFound(repository.CodeCompanyNotFound,
			"company not found: %s", id)
	}
	return c, nil
}

func (m *memRepo) ListCompanies(
	_ context.Context,
	filter models.CompanyFilter,
	limit, offset int64,
) ([]models.Company, int64, error) {
	m.record("ListCompanies")

	var all []models.Company
	for id := range m.companies {
		c := m.companies[id]
		if c.DeletedAt != nil {
			continue
		}
		if filter.Email != nil && c.Email != *filter.Email {
			continue
		}
		all = append(all, c)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID > all[j].ID })

	return paginate(all, limit, offset), int64(len(all)), nil
}

func (m *memRepo) UpdateCompany(
	_ context.Context,
	id string,
	patch models.CompanyPatch,
	now time.Time,
) (models.Company, error) {
	m.record("UpdateCompany")

	c, ok := m.liveCompany(id)
	if !ok {
		return models.Company{}, errors.NotFound(repository.CodeCompanyNotFound,
			"company not found: %s", id)
	}

	fields := []struct {
		dst *string
		src *string
	}{
		{&c.Name, patch.Name},
		{&c.Email, patch.Email},
		{&c.Phone, patch.Phone},
		{&c.Address, patch.Address},
		{&c.City, patch.City},
		{&c.PostalCode, patch.PostalCode},
		{&c.CountryCode, patch.CountryCode},
		{&c.CurrencyCode, patch.CurrencyCode},
	}
	for _, f := range fields {
		if f.src != nil {
			*f.dst = *f.src
		}
	}
	if patch.SpendingLimitResetPeriod != nil {
		c.SpendingLimitResetPeriod = *patch.SpendingLimitResetPeriod
	}

	c.UpdatedAt = now
	m.companies[id] = c
	return c, nil
}

func (m *memRepo) DeleteCompany(_ context.Context, id string, now time.Time) ([]string, error) {
	m.record("DeleteCompany")

	c, ok := m.liveCompany(id)
	if !ok {
		return nil, errors.NotFound(repository.CodeCompanyNotFound, "company not found: %s", id)
	}
	c.DeletedAt = &now
	c.UpdatedAt = now
	m.companies[id] = c

	var deleted []string
	for eid, e := range m.employees {
		if e.CompanyID != id || e.DeletedAt != nil {
			continue
		}
		e.DeletedAt = &now
		e.UpdatedAt = now
		m.employees[eid] = e
		deleted = append(deleted, eid)
	}
	sort.Strings(deleted)
	return deleted, nil
}

func (m *memRepo) CreateEmployee(
	_ context.Context,
	e models.CompanyEmployee,
) (models.CompanyEmployee, error) {
	m.record("CreateEmployee")
	if m.failCreateEmployee {
		return models.CompanyEmployee{}, errors.Internal(repository.CodeQueryFailed,
			"the employee could not be created (test)")
	}
	// The repository does NOT STORE the customer id: it has no column.
	e.CustomerID = ""
	e.UpdatedAt = e.CreatedAt
	m.employees[e.ID] = e
	return e, nil
}

func (m *memRepo) GetEmployee(_ context.Context, id string) (models.CompanyEmployee, error) {
	m.record("GetEmployee")
	e, ok := m.liveEmployee(id)
	if !ok {
		return models.CompanyEmployee{}, errors.NotFound(repository.CodeEmployeeNotFound,
			"employee not found: %s", id)
	}
	return e, nil
}

func (m *memRepo) ListEmployees(
	_ context.Context,
	filter models.EmployeeFilter,
	limit, offset int64,
) ([]models.CompanyEmployee, int64, error) {
	m.record("ListEmployees")

	var all []models.CompanyEmployee
	for _, e := range m.employees {
		if e.DeletedAt != nil {
			continue
		}
		if filter.CompanyID != nil && e.CompanyID != *filter.CompanyID {
			continue
		}
		if filter.IsCompanyAdmin != nil && e.IsCompanyAdmin != *filter.IsCompanyAdmin {
			continue
		}
		all = append(all, e)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID > all[j].ID })

	return paginate(all, limit, offset), int64(len(all)), nil
}

func (m *memRepo) UpdateEmployee(
	_ context.Context,
	id string,
	patch models.EmployeePatch,
	now time.Time,
) (models.CompanyEmployee, error) {
	m.record("UpdateEmployee")

	e, ok := m.liveEmployee(id)
	if !ok {
		return models.CompanyEmployee{}, errors.NotFound(repository.CodeEmployeeNotFound,
			"employee not found: %s", id)
	}
	switch {
	case patch.ClearSpendingLimit:
		e.SpendingLimit = nil
	case patch.SpendingLimit != nil:
		e.SpendingLimit = patch.SpendingLimit
	}
	if patch.IsCompanyAdmin != nil {
		e.IsCompanyAdmin = *patch.IsCompanyAdmin
	}
	e.UpdatedAt = now
	m.employees[id] = e
	return e, nil
}

func (m *memRepo) DeleteEmployee(_ context.Context, id string, now time.Time) error {
	m.record("DeleteEmployee")

	e, ok := m.liveEmployee(id)
	if !ok {
		return errors.NotFound(repository.CodeEmployeeNotFound, "employee not found: %s", id)
	}
	e.DeletedAt = &now
	e.UpdatedAt = now
	m.employees[id] = e
	return nil
}

// paginate applies paging to an in-memory list.
func paginate[T any](all []T, limit, offset int64) []T {
	if offset >= int64(len(all)) {
		return []T{}
	}
	end := offset + limit
	if end > int64(len(all)) {
		end = int64(len(all))
	}
	return all[offset:end]
}

// memLinker is an in-memory implementation of [Linker].
//
// It enforces the cardinality the way the REAL link service does: both the
// employee side and the customer side are unique, and a violation returns
// errors.Conflict. Had it not enforced it, the service-side consequences of
// the rule "a customer is an employee of at most one company" (the 409 being
// classified correctly) could never be exercised.
type memLinker struct {
	// bonds is link name -> fromID -> set of toIDs.
	bonds map[string]map[string]map[string]bool
	// calls is method name -> call count; it is the proof that there is no
	// N+1.
	calls map[string]int
	// failCreate, if true, makes establishing a bond return an error.
	failCreate bool
	// failDelete, if true, makes removing a bond return an error.
	failDelete bool
	// failListByTo, if set, is the error the reverse-direction read returns.
	//
	// "There is no bond" and "we could not read the bond" are different
	// states, and only a fake that can make the read fail can exercise the
	// distinction.
	failListByTo error
}

var _ Linker = (*memLinker)(nil)

// newMemLinker builds an empty in-memory bond service.
func newMemLinker() *memLinker {
	return &memLinker{
		bonds: map[string]map[string]map[string]bool{},
		calls: map[string]int{},
	}
}

func (l *memLinker) Create(_ context.Context, name, fromID, toID string) error {
	l.calls["Create"]++
	if l.failCreate {
		return errors.Internal("link_query_failed", "the bond could not be established (test)")
	}
	if l.bonds[name] == nil {
		l.bonds[name] = map[string]map[string]bool{}
	}
	if l.bonds[name][fromID][toID] {
		return nil // idempotent
	}
	for from, tos := range l.bonds[name] {
		if from == fromID && len(tos) > 0 {
			return errors.Conflict("link_cardinality_violation",
				"in the %q link, %s is already bound", name, fromID)
		}
		if tos[toID] {
			return errors.Conflict("link_cardinality_violation",
				"in the %q link, %s is already bound", name, toID)
		}
	}
	if l.bonds[name][fromID] == nil {
		l.bonds[name][fromID] = map[string]bool{}
	}
	l.bonds[name][fromID][toID] = true
	return nil
}

func (l *memLinker) Delete(_ context.Context, name, fromID, toID string) error {
	l.calls["Delete"]++
	if l.failDelete {
		return errors.Internal("link_query_failed", "the bond could not be removed (test)")
	}
	delete(l.bonds[name][fromID], toID)
	return nil
}

func (l *memLinker) ListMany(
	_ context.Context,
	name string,
	fromIDs []string,
) (map[string][]string, error) {
	l.calls["ListMany"]++

	out := map[string][]string{}
	for _, fromID := range fromIDs {
		for toID := range l.bonds[name][fromID] {
			out[fromID] = append(out[fromID], toID)
		}
		sort.Strings(out[fromID])
	}
	return out, nil
}

func (l *memLinker) ListManyByTo(
	_ context.Context,
	name string,
	toIDs []string,
) (map[string][]string, error) {
	l.calls["ListManyByTo"]++
	if l.failListByTo != nil {
		return nil, l.failListByTo
	}

	wanted := map[string]bool{}
	for _, id := range toIDs {
		wanted[id] = true
	}

	out := map[string][]string{}
	for fromID, tos := range l.bonds[name] {
		for toID := range tos {
			if wanted[toID] {
				out[toID] = append(out[toID], fromID)
			}
		}
	}
	for _, ids := range out {
		sort.Strings(ids)
	}
	return out, nil
}
