package service

import (
	"context"
	"sort"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/b2b/models"
	"github.com/bdrtr/gobit/internal/modules/b2b/repository"
)

// memRepo [Repository]'nin bellek içi uygulamasıdır.
//
// Sahte depo GERÇEK deponun iki değişmezini taklit eder: okumaların yumuşak
// silinmiş kayıtları atlaması ve şirket silmenin çalışanları da silmesi. Taklit
// etmeseydi birim testleri bu kuralları "servis uyguluyor" sanarak geçerdi;
// oysa ikisinin de yeri SQL'dir ve burada yalnızca servisin sonucu doğru
// kullandığı sınanır. Kuralların gerçekten veritabanında tuttuğu ayrıca
// entegrasyon testinde kanıtlanır (bkz. b2b_integration_test.go).
type memRepo struct {
	companies map[string]models.Company
	employees map[string]models.CompanyEmployee

	// calls metot adı -> çağrı sayısıdır; toplu (batch) davranışın kanıtı budur.
	calls map[string]int
	// failCreateEmployee doğruysa çalışan yazımı hata döner.
	failCreateEmployee bool
}

var _ Repository = (*memRepo)(nil)

// newMemRepo boş bir bellek içi depo üretir.
func newMemRepo() *memRepo {
	return &memRepo{
		companies: map[string]models.Company{},
		employees: map[string]models.CompanyEmployee{},
		calls:     map[string]int{},
	}
}

// record bir çağrıyı sayar.
func (m *memRepo) record(name string) { m.calls[name]++ }

// liveCompany canlı şirketi döner.
func (m *memRepo) liveCompany(id string) (models.Company, bool) {
	c, ok := m.companies[id]
	if !ok || c.DeletedAt != nil {
		return models.Company{}, false
	}
	return c, true
}

// liveEmployee canlı çalışanı döner.
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
			"şirket bulunamadı: %s", id)
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

	return sayfala(all, limit, offset), int64(len(all)), nil
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
			"şirket bulunamadı: %s", id)
	}

	metinler := []struct {
		hedef  *string
		kaynak *string
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
	for _, alan := range metinler {
		if alan.kaynak != nil {
			*alan.hedef = *alan.kaynak
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
		return nil, errors.NotFound(repository.CodeCompanyNotFound, "şirket bulunamadı: %s", id)
	}
	c.DeletedAt = &now
	c.UpdatedAt = now
	m.companies[id] = c

	var silinen []string
	for eid, e := range m.employees {
		if e.CompanyID != id || e.DeletedAt != nil {
			continue
		}
		e.DeletedAt = &now
		e.UpdatedAt = now
		m.employees[eid] = e
		silinen = append(silinen, eid)
	}
	sort.Strings(silinen)
	return silinen, nil
}

func (m *memRepo) CreateEmployee(
	_ context.Context,
	e models.CompanyEmployee,
) (models.CompanyEmployee, error) {
	m.record("CreateEmployee")
	if m.failCreateEmployee {
		return models.CompanyEmployee{}, errors.Internal(repository.CodeQueryFailed,
			"çalışan oluşturulamadı (test)")
	}
	// Depo müşteri kimliğini SAKLAMAZ: sütunu yoktur.
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
			"çalışan bulunamadı: %s", id)
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

	return sayfala(all, limit, offset), int64(len(all)), nil
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
			"çalışan bulunamadı: %s", id)
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
		return errors.NotFound(repository.CodeEmployeeNotFound, "çalışan bulunamadı: %s", id)
	}
	e.DeletedAt = &now
	e.UpdatedAt = now
	m.employees[id] = e
	return nil
}

// sayfala bellek içi listeye sayfalama uygular.
func sayfala[T any](all []T, limit, offset int64) []T {
	if offset >= int64(len(all)) {
		return []T{}
	}
	son := offset + limit
	if son > int64(len(all)) {
		son = int64(len(all))
	}
	return all[offset:son]
}

// memLinker [Linker]'ın bellek içi uygulamasıdır.
//
// Kardinaliteyi GERÇEK link servisi gibi zorlar: hem çalışan hem müşteri ucu
// tekildir ve ihlal errors.Conflict döner. Zorlamasaydı "bir müşteri en fazla
// bir şirketin çalışanıdır" kuralının servis tarafındaki sonuçları (409'un
// doğru sınıflandırılması) hiç sınanamazdı.
type memLinker struct {
	// bags link adı -> fromID -> toID kümesi.
	bags map[string]map[string]map[string]bool
	// calls metot adı -> çağrı sayısı; N+1 yokluğunun kanıtıdır.
	calls map[string]int
	// failCreate doğruysa bağ kurma hata döner.
	failCreate bool
	// failDelete doğruysa bağ kaldırma hata döner.
	failDelete bool
	// failListByTo ayarlanırsa ters yön okuması bu hatayı döner.
	//
	// "Bağ yok" ile "bağı okuyamadık" farklı durumlardır ve ayrımı yalnızca
	// okumayı düşürebilen bir sahte sınayabilir.
	failListByTo error
}

var _ Linker = (*memLinker)(nil)

// newMemLinker boş bir bellek içi bağ servisi üretir.
func newMemLinker() *memLinker {
	return &memLinker{
		bags:  map[string]map[string]map[string]bool{},
		calls: map[string]int{},
	}
}

func (l *memLinker) Create(_ context.Context, name, fromID, toID string) error {
	l.calls["Create"]++
	if l.failCreate {
		return errors.Internal("link_query_failed", "bağ kurulamadı (test)")
	}
	if l.bags[name] == nil {
		l.bags[name] = map[string]map[string]bool{}
	}
	if l.bags[name][fromID][toID] {
		return nil // idempotent
	}
	for from, tos := range l.bags[name] {
		if from == fromID && len(tos) > 0 {
			return errors.Conflict("link_cardinality_violation",
				"%q linkinde %s zaten bağlı", name, fromID)
		}
		if tos[toID] {
			return errors.Conflict("link_cardinality_violation",
				"%q linkinde %s zaten bağlı", name, toID)
		}
	}
	if l.bags[name][fromID] == nil {
		l.bags[name][fromID] = map[string]bool{}
	}
	l.bags[name][fromID][toID] = true
	return nil
}

func (l *memLinker) Delete(_ context.Context, name, fromID, toID string) error {
	l.calls["Delete"]++
	if l.failDelete {
		return errors.Internal("link_query_failed", "bağ kaldırılamadı (test)")
	}
	delete(l.bags[name][fromID], toID)
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
		for toID := range l.bags[name][fromID] {
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

	istenen := map[string]bool{}
	for _, id := range toIDs {
		istenen[id] = true
	}

	out := map[string][]string{}
	for fromID, tos := range l.bags[name] {
		for toID := range tos {
			if istenen[toID] {
				out[toID] = append(out[toID], fromID)
			}
		}
	}
	for _, ids := range out {
		sort.Strings(ids)
	}
	return out, nil
}
