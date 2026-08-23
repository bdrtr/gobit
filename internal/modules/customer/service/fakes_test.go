package service

import (
	"context"
	"slices"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/repository"
)

// memRepo [Repository]'nin bellek içi uygulamasıdır.
//
// Sahte depo, GERÇEK deponun iki değişmezini taklit eder: kayıtlı hesapların
// e-posta benzersizliği ve müşteri başına tek varsayılan adresi. Taklit
// etmeseydi birim testleri bu kuralları "servis uyguluyor" sanarak geçerdi;
// oysa kuralların yeri veritabanıdır ve burada yalnızca servisin onların
// ürettiği hatayı doğru sınıflandırdığı sınanır.
//
// Kuralların gerçekten veritabanında tuttuğu ayrıca entegrasyon testinde
// kanıtlanır (bkz. customer_integration_test.go).
type memRepo struct {
	customers map[string]models.Customer
	groups    map[string]models.CustomerGroup
	members   map[string]map[string]bool // customerID -> groupID -> üye mi
	addresses map[string]models.CustomerAddress

	// calls metot adı -> çağrı sayısıdır; toplu (batch) davranışın kanıtı budur.
	calls map[string]int
}

var _ Repository = (*memRepo)(nil)

// newMemRepo boş bir bellek içi depo üretir.
func newMemRepo() *memRepo {
	return &memRepo{
		customers: map[string]models.Customer{},
		groups:    map[string]models.CustomerGroup{},
		members:   map[string]map[string]bool{},
		addresses: map[string]models.CustomerAddress{},
		calls:     map[string]int{},
	}
}

// record bir çağrıyı sayar.
func (m *memRepo) record(name string) { m.calls[name]++ }

// liveCustomer canlı müşteriyi döner.
func (m *memRepo) liveCustomer(id string) (models.Customer, bool) {
	c, ok := m.customers[id]
	if !ok || c.DeletedAt != nil {
		return models.Customer{}, false
	}
	return c, true
}

// liveGroup canlı grubu döner.
func (m *memRepo) liveGroup(id string) (models.CustomerGroup, bool) {
	g, ok := m.groups[id]
	if !ok || g.DeletedAt != nil {
		return models.CustomerGroup{}, false
	}
	return g, true
}

// accountEmailTaken e-postanın başka bir canlı HESAP tarafından kullanılıp
// kullanılmadığını bildirir; kısmi benzersiz indeksin bellekteki karşılığıdır.
func (m *memRepo) accountEmailTaken(email, exceptID string) bool {
	for id := range m.customers {
		c := m.customers[id]
		if id == exceptID || c.DeletedAt != nil || !c.HasAccount {
			continue
		}
		if c.Email == email {
			return true
		}
	}
	return false
}

func (m *memRepo) CreateCustomer(_ context.Context, c models.Customer) (models.Customer, error) {
	m.record("CreateCustomer")
	if c.HasAccount && m.accountEmailTaken(c.Email, "") {
		return models.Customer{}, errors.Conflict(repository.CodeEmailTaken,
			"%q e-postasıyla kayıtlı bir hesap zaten var", c.Email)
	}
	c.UpdatedAt = c.CreatedAt
	m.customers[c.ID] = c
	return c, nil
}

func (m *memRepo) GetCustomer(_ context.Context, id string) (models.Customer, error) {
	m.record("GetCustomer")
	c, ok := m.liveCustomer(id)
	if !ok {
		return models.Customer{}, errors.NotFound(repository.CodeCustomerNotFound,
			"müşteri bulunamadı: %s", id)
	}
	return c, nil
}

func (m *memRepo) GetAccountByEmail(_ context.Context, email string) (models.Customer, error) {
	m.record("GetAccountByEmail")
	for id := range m.customers {
		if c := m.customers[id]; c.DeletedAt == nil && c.HasAccount && c.Email == email {
			return c, nil
		}
	}
	return models.Customer{}, errors.NotFound(repository.CodeCustomerNotFound,
		"%q e-postasıyla kayıtlı hesap bulunamadı", email)
}

func (m *memRepo) ListCustomers(
	_ context.Context,
	filter models.CustomerFilter,
	limit, offset int64,
) ([]models.Customer, int64, error) {
	m.record("ListCustomers")

	matched := make([]models.Customer, 0, len(m.customers))
	for id := range m.customers {
		c := m.customers[id]
		if c.DeletedAt != nil {
			continue
		}
		if filter.Email != nil && c.Email != *filter.Email {
			continue
		}
		if filter.HasAccount != nil && c.HasAccount != *filter.HasAccount {
			continue
		}
		// Grup süzgeci CANLI gruba bakar; gerçek sorgu da üyelik satırını
		// customer_group ile birleştirip deleted_at IS NULL süzer.
		if filter.GroupID != nil {
			if _, live := m.liveGroup(*filter.GroupID); !live || !m.members[c.ID][*filter.GroupID] {
				continue
			}
		}
		matched = append(matched, c)
	}
	slices.SortFunc(matched, func(a, b models.Customer) int {
		return cmpString(a.ID, b.ID)
	})

	total := int64(len(matched))
	if offset >= total {
		return []models.Customer{}, total, nil
	}
	end := min(offset+limit, total)
	return slices.Clone(matched[offset:end]), total, nil
}

func (m *memRepo) GetCustomersByIDs(_ context.Context, ids []string) ([]models.Customer, error) {
	m.record("GetCustomersByIDs")

	out := make([]models.Customer, 0, len(ids))
	for _, id := range ids {
		if c, ok := m.liveCustomer(id); ok {
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b models.Customer) int { return cmpString(a.ID, b.ID) })
	return out, nil
}

func (m *memRepo) UpdateCustomer(
	_ context.Context,
	id string,
	patch models.CustomerPatch,
	now time.Time,
) (models.Customer, error) {
	m.record("UpdateCustomer")

	c, ok := m.liveCustomer(id)
	if !ok {
		return models.Customer{}, errors.NotFound(repository.CodeCustomerNotFound,
			"müşteri bulunamadı: %s", id)
	}
	if patch.Email != nil {
		if c.HasAccount && m.accountEmailTaken(*patch.Email, id) {
			return models.Customer{}, errors.Conflict(repository.CodeEmailTaken,
				"e-posta başka bir hesap tarafından kullanılıyor")
		}
		c.Email = *patch.Email
	}
	if patch.FirstName != nil {
		c.FirstName = *patch.FirstName
	}
	if patch.LastName != nil {
		c.LastName = *patch.LastName
	}
	if patch.Phone != nil {
		c.Phone = *patch.Phone
	}
	if patch.Metadata != nil {
		c.Metadata = patch.Metadata
	}
	c.UpdatedAt = now
	m.customers[id] = c
	return c, nil
}

func (m *memRepo) PromoteGuest(_ context.Context, id string, now time.Time) (models.Customer, error) {
	m.record("PromoteGuest")

	c, ok := m.liveCustomer(id)
	if !ok {
		return models.Customer{}, errors.NotFound(repository.CodeCustomerNotFound,
			"müşteri bulunamadı: %s", id)
	}
	if c.HasAccount {
		return models.Customer{}, errors.Conflict(repository.CodeAlreadyAccount,
			"müşteri zaten bir hesaba sahip: %s", id)
	}
	if m.accountEmailTaken(c.Email, id) {
		return models.Customer{}, errors.Conflict(repository.CodeEmailTaken,
			"%q e-postasıyla kayıtlı bir hesap zaten var", c.Email)
	}

	c.HasAccount = true
	c.UpdatedAt = now
	m.customers[id] = c
	return c, nil
}

func (m *memRepo) DeleteCustomer(_ context.Context, id string, now time.Time) error {
	m.record("DeleteCustomer")

	c, ok := m.liveCustomer(id)
	if !ok {
		return errors.NotFound(repository.CodeCustomerNotFound, "müşteri bulunamadı: %s", id)
	}
	c.DeletedAt = &now
	c.UpdatedAt = now
	m.customers[id] = c

	for addrID := range m.addresses {
		if a := m.addresses[addrID]; a.CustomerID == id && a.DeletedAt == nil {
			a.DeletedAt = &now
			m.addresses[addrID] = a
		}
	}
	return nil
}

func (m *memRepo) CreateGroup(_ context.Context, g models.CustomerGroup) (models.CustomerGroup, error) {
	m.record("CreateGroup")
	for _, existing := range m.groups {
		if existing.DeletedAt == nil && existing.Name == g.Name {
			return models.CustomerGroup{}, errors.Conflict(repository.CodeGroupNameTaken,
				"%q adında bir müşteri grubu zaten var", g.Name)
		}
	}
	g.UpdatedAt = g.CreatedAt
	m.groups[g.ID] = g
	return g, nil
}

func (m *memRepo) GetGroup(_ context.Context, id string) (models.CustomerGroup, error) {
	m.record("GetGroup")
	g, ok := m.liveGroup(id)
	if !ok {
		return models.CustomerGroup{}, errors.NotFound(repository.CodeGroupNotFound,
			"müşteri grubu bulunamadı: %s", id)
	}
	return g, nil
}

func (m *memRepo) UpdateGroup(
	_ context.Context,
	id string,
	patch models.CustomerGroupPatch,
	now time.Time,
) (models.CustomerGroup, error) {
	m.record("UpdateGroup")

	g, ok := m.liveGroup(id)
	if !ok {
		return models.CustomerGroup{}, errors.NotFound(repository.CodeGroupNotFound,
			"müşteri grubu bulunamadı: %s", id)
	}
	if patch.Name != nil {
		for otherID := range m.groups {
			other := m.groups[otherID]
			if otherID != id && other.DeletedAt == nil && other.Name == *patch.Name {
				return models.CustomerGroup{}, errors.Conflict(repository.CodeGroupNameTaken,
					"bu adda bir müşteri grubu zaten var")
			}
		}
		g.Name = *patch.Name
	}
	if patch.Metadata != nil {
		g.Metadata = patch.Metadata
	}
	g.UpdatedAt = now
	m.groups[id] = g
	return g, nil
}

func (m *memRepo) DeleteGroup(_ context.Context, id string, now time.Time) error {
	m.record("DeleteGroup")

	g, ok := m.liveGroup(id)
	if !ok {
		return errors.NotFound(repository.CodeGroupNotFound, "müşteri grubu bulunamadı: %s", id)
	}
	g.DeletedAt = &now
	g.UpdatedAt = now
	m.groups[id] = g
	return nil
}

func (m *memRepo) ListGroups(_ context.Context, limit, offset int64) ([]models.CustomerGroup, int64, error) {
	m.record("ListGroups")

	all := make([]models.CustomerGroup, 0, len(m.groups))
	for _, g := range m.groups {
		if g.DeletedAt == nil {
			all = append(all, g)
		}
	}
	slices.SortFunc(all, func(a, b models.CustomerGroup) int { return cmpString(a.ID, b.ID) })

	total := int64(len(all))
	if offset >= total {
		return []models.CustomerGroup{}, total, nil
	}
	end := min(offset+limit, total)
	return slices.Clone(all[offset:end]), total, nil
}

func (m *memRepo) AddToGroup(_ context.Context, customerID, groupID string, _ time.Time) error {
	m.record("AddToGroup")

	if _, ok := m.liveCustomer(customerID); !ok {
		return errors.NotFound(repository.CodeCustomerNotFound, "müşteri bulunamadı: %s", customerID)
	}
	if _, ok := m.liveGroup(groupID); !ok {
		return errors.NotFound(repository.CodeGroupNotFound, "müşteri grubu bulunamadı: %s", groupID)
	}
	if m.members[customerID] == nil {
		m.members[customerID] = map[string]bool{}
	}
	m.members[customerID][groupID] = true
	return nil
}

func (m *memRepo) RemoveFromGroup(_ context.Context, customerID, groupID string) error {
	m.record("RemoveFromGroup")

	if !m.members[customerID][groupID] {
		return errors.NotFound(repository.CodeMembershipNotFound,
			"%s müşterisi %s grubunun üyesi değil", customerID, groupID)
	}
	delete(m.members[customerID], groupID)
	return nil
}

func (m *memRepo) ListGroupsOf(_ context.Context, customerID string) ([]models.CustomerGroup, error) {
	m.record("ListGroupsOf")

	out := make([]models.CustomerGroup, 0, len(m.members[customerID]))
	for groupID := range m.members[customerID] {
		if g, ok := m.groups[groupID]; ok && g.DeletedAt == nil {
			out = append(out, g)
		}
	}
	slices.SortFunc(out, func(a, b models.CustomerGroup) int { return cmpString(a.ID, b.ID) })
	return out, nil
}

func (m *memRepo) GroupIDsOfCustomers(_ context.Context, customerIDs []string) (map[string][]string, error) {
	m.record("GroupIDsOfCustomers")

	out := make(map[string][]string, len(customerIDs))
	for _, id := range customerIDs {
		var ids []string
		for groupID := range m.members[id] {
			if g, ok := m.groups[groupID]; ok && g.DeletedAt == nil {
				ids = append(ids, groupID)
			}
		}
		if len(ids) > 0 {
			slices.Sort(ids)
			out[id] = ids
		}
	}
	return out, nil
}

func (m *memRepo) CreateAddress(_ context.Context, a models.CustomerAddress) (models.CustomerAddress, error) {
	m.record("CreateAddress")

	if _, ok := m.liveCustomer(a.CustomerID); !ok {
		return models.CustomerAddress{}, errors.NotFound(repository.CodeCustomerNotFound,
			"müşteri bulunamadı: %s", a.CustomerID)
	}
	m.clearDefaults(a.CustomerID, a.IsDefaultShipping, a.IsDefaultBilling, a.CreatedAt)

	a.UpdatedAt = a.CreatedAt
	m.addresses[a.ID] = a
	return a, nil
}

func (m *memRepo) GetAddress(_ context.Context, customerID, addressID string) (models.CustomerAddress, error) {
	m.record("GetAddress")
	a, ok := m.liveAddress(customerID, addressID)
	if !ok {
		return models.CustomerAddress{}, errors.NotFound(repository.CodeAddressNotFound,
			"müşteri adresi bulunamadı: %s", addressID)
	}
	return a, nil
}

func (m *memRepo) ListAddresses(_ context.Context, customerID string) ([]models.CustomerAddress, error) {
	m.record("ListAddresses")

	out := make([]models.CustomerAddress, 0, len(m.addresses))
	for id := range m.addresses {
		if a := m.addresses[id]; a.CustomerID == customerID && a.DeletedAt == nil {
			out = append(out, a)
		}
	}
	slices.SortFunc(out, func(x, y models.CustomerAddress) int { return cmpString(x.ID, y.ID) })
	return out, nil
}

func (m *memRepo) UpdateAddress(
	_ context.Context,
	customerID, addressID string,
	patch models.AddressPatch,
	now time.Time,
) (models.CustomerAddress, error) {
	m.record("UpdateAddress")

	a, ok := m.liveAddress(customerID, addressID)
	if !ok {
		return models.CustomerAddress{}, errors.NotFound(repository.CodeAddressNotFound,
			"müşteri adresi bulunamadı: %s", addressID)
	}
	assign(&a.FirstName, patch.FirstName)
	assign(&a.LastName, patch.LastName)
	assign(&a.Company, patch.Company)
	assign(&a.Address1, patch.Address1)
	assign(&a.Address2, patch.Address2)
	assign(&a.City, patch.City)
	assign(&a.CountryCode, patch.CountryCode)
	assign(&a.PostalCode, patch.PostalCode)
	assign(&a.Phone, patch.Phone)
	a.UpdatedAt = now

	m.addresses[addressID] = a
	return a, nil
}

func (m *memRepo) DeleteAddress(_ context.Context, customerID, addressID string, now time.Time) error {
	m.record("DeleteAddress")

	a, ok := m.liveAddress(customerID, addressID)
	if !ok {
		return errors.NotFound(repository.CodeAddressNotFound,
			"müşteri adresi bulunamadı: %s", addressID)
	}
	a.DeletedAt = &now
	a.UpdatedAt = now
	m.addresses[addressID] = a
	return nil
}

func (m *memRepo) SetDefaultAddress(
	_ context.Context,
	customerID, addressID string,
	kind models.DefaultKind,
	now time.Time,
) (models.CustomerAddress, error) {
	m.record("SetDefaultAddress")

	if !kind.Valid() {
		return models.CustomerAddress{}, errors.Invalid(repository.CodeInvalidDefaultKind,
			"tanımsız varsayılan işaret türü: %d", uint8(kind))
	}
	if _, ok := m.liveCustomer(customerID); !ok {
		return models.CustomerAddress{}, errors.NotFound(repository.CodeCustomerNotFound,
			"müşteri bulunamadı: %s", customerID)
	}
	a, ok := m.liveAddress(customerID, addressID)
	if !ok {
		return models.CustomerAddress{}, errors.NotFound(repository.CodeAddressNotFound,
			"müşteri adresi bulunamadı: %s", addressID)
	}

	m.clearDefaults(customerID, kind == models.DefaultShipping, kind == models.DefaultBilling, now)

	a = m.addresses[addressID]
	if kind == models.DefaultShipping {
		a.IsDefaultShipping = true
	} else {
		a.IsDefaultBilling = true
	}
	a.UpdatedAt = now
	m.addresses[addressID] = a
	return a, nil
}

// liveAddress müşteriye ait canlı adresi döner.
func (m *memRepo) liveAddress(customerID, addressID string) (models.CustomerAddress, bool) {
	a, ok := m.addresses[addressID]
	if !ok || a.DeletedAt != nil || a.CustomerID != customerID {
		return models.CustomerAddress{}, false
	}
	return a, true
}

// clearDefaults istenen türlerdeki varsayılan işaretlerini kaldırır.
func (m *memRepo) clearDefaults(customerID string, shipping, billing bool, now time.Time) {
	for id := range m.addresses {
		a := m.addresses[id]
		if a.CustomerID != customerID || a.DeletedAt != nil {
			continue
		}
		changed := false
		if shipping && a.IsDefaultShipping {
			a.IsDefaultShipping = false
			changed = true
		}
		if billing && a.IsDefaultBilling {
			a.IsDefaultBilling = false
			changed = true
		}
		if changed {
			a.UpdatedAt = now
			m.addresses[id] = a
		}
	}
}

// assign işaretçi doluysa hedefe yazar.
func assign(dst, src *string) {
	if src != nil {
		*dst = *src
	}
}

// cmpString iki dizeyi karşılaştırır; slices.SortFunc için.
func cmpString(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
