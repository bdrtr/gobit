package api

import (
	"time"

	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// DTO'lar domain modellerinden AYRI tutulur: JSON alan adları dış sözleşmedir
// ve modelde yapılan bir yeniden adlandırma istemciyi kırmamalıdır.

// customerDTO bir müşterinin yanıt gövdesidir.
type customerDTO struct {
	// ID müşterinin kimliğidir.
	ID string `json:"id"`
	// Email müşterinin e-postasıdır (küçük harfe normalize edilmiş).
	Email string `json:"email"`
	// FirstName müşterinin adıdır.
	FirstName string `json:"first_name"`
	// LastName müşterinin soyadıdır.
	LastName string `json:"last_name"`
	// Phone müşterinin telefonudur.
	Phone string `json:"phone"`
	// HasAccount kaydın kayıtlı hesap mı misafir mi olduğunu bildirir.
	HasAccount bool `json:"has_account"`
	// Metadata serbest yapısal bağlamdır; boşsa gövdede görünmez.
	Metadata map[string]any `json:"metadata,omitempty"`
	// CreatedAt oluşturulma anıdır (RFC3339, UTC).
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt son güncellenme anıdır (RFC3339, UTC).
	UpdatedAt time.Time `json:"updated_at"`
}

// customerGroupDTO bir müşteri grubunun yanıt gövdesidir.
type customerGroupDTO struct {
	// ID grubun kimliğidir.
	ID string `json:"id"`
	// Name grubun adıdır.
	Name string `json:"name"`
	// Metadata serbest yapısal bağlamdır; boşsa gövdede görünmez.
	Metadata map[string]any `json:"metadata,omitempty"`
	// CreatedAt oluşturulma anıdır.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt son güncellenme anıdır.
	UpdatedAt time.Time `json:"updated_at"`
}

// addressDTO bir müşteri adresinin yanıt gövdesidir.
type addressDTO struct {
	// ID adresin kimliğidir.
	ID string `json:"id"`
	// CustomerID adresin sahibi müşteridir.
	CustomerID string `json:"customer_id"`
	// FirstName adresin üzerindeki addır.
	FirstName string `json:"first_name"`
	// LastName adresin üzerindeki soyaddır.
	LastName string `json:"last_name"`
	// Company şirket adıdır.
	Company string `json:"company"`
	// Address1 adresin ilk satırıdır.
	Address1 string `json:"address_1"`
	// Address2 adresin ikinci satırıdır.
	Address2 string `json:"address_2"`
	// City şehirdir.
	City string `json:"city"`
	// CountryCode ISO 3166-1 alpha-2 ülke kodudur (BÜYÜK harf).
	CountryCode string `json:"country_code"`
	// PostalCode posta kodudur.
	PostalCode string `json:"postal_code"`
	// Phone iletişim telefonudur.
	Phone string `json:"phone"`
	// IsDefaultShipping varsayılan kargo adresi işaretidir.
	IsDefaultShipping bool `json:"is_default_shipping"`
	// IsDefaultBilling varsayılan fatura adresi işaretidir.
	IsDefaultBilling bool `json:"is_default_billing"`
	// CreatedAt oluşturulma anıdır.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt son güncellenme anıdır.
	UpdatedAt time.Time `json:"updated_at"`
}

// customerRequest müşteri oluşturma ve misafir kaydı gövdesidir.
type customerRequest struct {
	Email     string         `json:"email"`
	FirstName string         `json:"first_name"`
	LastName  string         `json:"last_name"`
	Phone     string         `json:"phone"`
	Metadata  map[string]any `json:"metadata"`
}

// updateCustomerRequest müşteri güncelleme gövdesidir.
//
// Alanlar işaretçidir: verilmeyen alan "dokunma", verilen boş dize ise gerçek
// bir temizleme anlamına gelir. İki durumu ayırmayan bir gövdede, adını
// göndermeyen istemci adını silmiş olurdu.
type updateCustomerRequest struct {
	Email     *string        `json:"email"`
	FirstName *string        `json:"first_name"`
	LastName  *string        `json:"last_name"`
	Phone     *string        `json:"phone"`
	Metadata  map[string]any `json:"metadata"`
}

// groupRequest müşteri grubu oluşturma gövdesidir.
type groupRequest struct {
	Name     string         `json:"name"`
	Metadata map[string]any `json:"metadata"`
}

// updateGroupRequest müşteri grubu güncelleme gövdesidir.
//
// Ad işaretçidir: verilmeyen ad "dokunma" demektir. Ad zorunlu bir alan olduğu
// için verilirse boş olamaz; iki durumu ayırmayan bir gövdede yalnızca
// metadata gönderen istemci grubun adını silmiş olurdu.
type updateGroupRequest struct {
	Name     *string        `json:"name"`
	Metadata map[string]any `json:"metadata"`
}

// groupMemberRequest gruba müşteri ekleme gövdesidir.
type groupMemberRequest struct {
	CustomerID string `json:"customer_id"`
}

// addressRequest adresin oluşturma gövdesidir.
type addressRequest struct {
	FirstName         string `json:"first_name"`
	LastName          string `json:"last_name"`
	Company           string `json:"company"`
	Address1          string `json:"address_1"`
	Address2          string `json:"address_2"`
	City              string `json:"city"`
	CountryCode       string `json:"country_code"`
	PostalCode        string `json:"postal_code"`
	Phone             string `json:"phone"`
	IsDefaultShipping bool   `json:"is_default_shipping"`
	IsDefaultBilling  bool   `json:"is_default_billing"`
}

// updateAddressRequest adresin güncelleme gövdesidir.
//
// Varsayılan işaretleri BİLİNÇLİ OLARAK yoktur: işaret değiştirmek müşterinin
// diğer adreslerini de ilgilendirdiği için ayrı uç noktalardan yapılır.
type updateAddressRequest struct {
	FirstName   *string `json:"first_name"`
	LastName    *string `json:"last_name"`
	Company     *string `json:"company"`
	Address1    *string `json:"address_1"`
	Address2    *string `json:"address_2"`
	City        *string `json:"city"`
	CountryCode *string `json:"country_code"`
	PostalCode  *string `json:"postal_code"`
	Phone       *string `json:"phone"`
}

// toCustomerDTO müşteriyi yanıt gövdesine çevirir.
func toCustomerDTO(c models.Customer) customerDTO {
	return customerDTO{
		ID:         c.ID,
		Email:      c.Email,
		FirstName:  c.FirstName,
		LastName:   c.LastName,
		Phone:      c.Phone,
		HasAccount: c.HasAccount,
		Metadata:   c.Metadata,
		CreatedAt:  c.CreatedAt,
		UpdatedAt:  c.UpdatedAt,
	}
}

// toGroupDTO grubu yanıt gövdesine çevirir.
func toGroupDTO(g models.CustomerGroup) customerGroupDTO {
	return customerGroupDTO{
		ID:        g.ID,
		Name:      g.Name,
		Metadata:  g.Metadata,
		CreatedAt: g.CreatedAt,
		UpdatedAt: g.UpdatedAt,
	}
}

// toAddressDTO adresi yanıt gövdesine çevirir.
func toAddressDTO(a models.CustomerAddress) addressDTO {
	return addressDTO{
		ID:                a.ID,
		CustomerID:        a.CustomerID,
		FirstName:         a.FirstName,
		LastName:          a.LastName,
		Company:           a.Company,
		Address1:          a.Address1,
		Address2:          a.Address2,
		City:              a.City,
		CountryCode:       a.CountryCode,
		PostalCode:        a.PostalCode,
		Phone:             a.Phone,
		IsDefaultShipping: a.IsDefaultShipping,
		IsDefaultBilling:  a.IsDefaultBilling,
		CreatedAt:         a.CreatedAt,
		UpdatedAt:         a.UpdatedAt,
	}
}
