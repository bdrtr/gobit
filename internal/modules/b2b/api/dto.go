package api

import (
	"time"

	"github.com/bdrtr/gobit/internal/modules/b2b/models"
	"github.com/bdrtr/gobit/internal/modules/b2b/service"
)

// DTO'lar domain modellerinden AYRI tutulur: JSON alan adları dış sözleşmedir
// ve modelde yapılan bir yeniden adlandırma istemciyi kırmamalıdır.

// companyDTO bir şirketin yanıt gövdesidir.
type companyDTO struct {
	// ID şirketin kimliğidir.
	ID string `json:"id"`
	// Name şirketin ticari unvanıdır.
	Name string `json:"name"`
	// Email şirketin iletişim adresidir (küçük harfe normalize edilmiş).
	Email string `json:"email"`
	// Phone şirketin telefonudur.
	Phone string `json:"phone"`
	// Address fatura adresinin sokak satırıdır.
	Address string `json:"address"`
	// City şehirdir.
	City string `json:"city"`
	// PostalCode posta kodudur.
	PostalCode string `json:"postal_code"`
	// CountryCode ISO 3166-1 alpha-2 ülke kodudur (BÜYÜK harf); boş olabilir.
	CountryCode string `json:"country_code"`
	// CurrencyCode ISO 4217 para birimi kodudur; harcama limitleri bu para
	// biriminde ifade edilir.
	CurrencyCode string `json:"currency_code"`
	// SpendingLimitResetPeriod çalışan limitlerinin sıfırlanma aralığıdır:
	// "monthly", "yearly" ya da "never".
	SpendingLimitResetPeriod string `json:"spending_limit_reset_period"`
	// CreatedAt oluşturulma anıdır (RFC3339, UTC).
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt son güncellenme anıdır (RFC3339, UTC).
	UpdatedAt time.Time `json:"updated_at"`
}

// employeeDTO bir şirket çalışanının yanıt gövdesidir.
type employeeDTO struct {
	// ID çalışan kaydının kimliğidir.
	ID string `json:"id"`
	// CompanyID çalışanın bağlı olduğu şirkettir.
	CompanyID string `json:"company_id"`
	// CustomerID çalışanın müşteri kaydıdır (customer modülü). Değer link
	// katmanından gelir; boş görünüyorsa bağ kurulamamış demektir.
	CustomerID string `json:"customer_id"`
	// SpendingLimit pencere başına harcanabilecek azami tutardır (minor unit).
	// null SINIRSIZ demektir; 0 gerçek bir sıfır limittir.
	SpendingLimit *int64 `json:"spending_limit"`
	// IsCompanyAdmin çalışanın şirket yöneticisi olup olmadığını bildirir.
	IsCompanyAdmin bool `json:"is_company_admin"`
	// CreatedAt oluşturulma anıdır.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt son güncellenme anıdır.
	UpdatedAt time.Time `json:"updated_at"`
}

// storeEmployeeDTO vitrindeki müşterinin KENDİ çalışan kaydıdır.
//
// Yönetim yanıtından iki alanla ayrılır ve ikisi de şirketten türetilir:
// harcama penceresinin aralığı ve o pencerenin başlangıcı. Yönetim listesinde
// bu alanlar YOKTUR çünkü orada çalışan başına şirket okumak N+1 üretirdi;
// vitrinde ise zaten tek bir kayıt vardır.
type storeEmployeeDTO struct {
	// ID çalışan kaydının kimliğidir.
	ID string `json:"id"`
	// CompanyID çalışanın bağlı olduğu şirkettir.
	CompanyID string `json:"company_id"`
	// CustomerID çalışanın müşteri kaydıdır.
	CustomerID string `json:"customer_id"`
	// SpendingLimit pencere başına harcanabilecek azami tutardır (minor unit);
	// null sınırsız demektir.
	SpendingLimit *int64 `json:"spending_limit"`
	// SpendingLimitResetPeriod limitin sıfırlanma aralığıdır (şirketten gelir).
	SpendingLimitResetPeriod string `json:"spending_limit_reset_period"`
	// SpendingWindowStart geçerli harcama penceresinin başlangıcıdır; periyot
	// "never" ise null (pencere yoktur).
	//
	// KALAN HAK BURADA YOKTUR: kalanı hesaplamak pencere içindeki siparişlerin
	// toplamını gerektirir ve o veri order modülünündür. Uydurulmuş bir kalan
	// alanı, istemciye yanlış bir sayı verirdi (bkz. service.Membership).
	SpendingWindowStart *time.Time `json:"spending_window_start"`
	// IsCompanyAdmin çalışanın şirket yöneticisi olup olmadığını bildirir.
	IsCompanyAdmin bool `json:"is_company_admin"`
	// CreatedAt oluşturulma anıdır.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt son güncellenme anıdır.
	UpdatedAt time.Time `json:"updated_at"`
}

// companyRequest şirket oluşturma gövdesidir.
type companyRequest struct {
	Name                     string `json:"name"`
	Email                    string `json:"email"`
	Phone                    string `json:"phone"`
	Address                  string `json:"address"`
	City                     string `json:"city"`
	PostalCode               string `json:"postal_code"`
	CountryCode              string `json:"country_code"`
	CurrencyCode             string `json:"currency_code"`
	SpendingLimitResetPeriod string `json:"spending_limit_reset_period"`
}

// updateCompanyRequest şirket güncelleme gövdesidir.
//
// Alanlar işaretçidir: verilmeyen alan "dokunma", verilen boş dize ise gerçek
// bir temizlemedir. İki durumu ayırmayan bir gövdede, yalnızca telefonunu
// güncelleyen istemci şirketin adresini silmiş olurdu.
type updateCompanyRequest struct {
	Name                     *string `json:"name"`
	Email                    *string `json:"email"`
	Phone                    *string `json:"phone"`
	Address                  *string `json:"address"`
	City                     *string `json:"city"`
	PostalCode               *string `json:"postal_code"`
	CountryCode              *string `json:"country_code"`
	CurrencyCode             *string `json:"currency_code"`
	SpendingLimitResetPeriod *string `json:"spending_limit_reset_period"`
}

// employeeRequest çalışan oluşturma gövdesidir.
type employeeRequest struct {
	CompanyID      string `json:"company_id"`
	CustomerID     string `json:"customer_id"`
	SpendingLimit  *int64 `json:"spending_limit"`
	IsCompanyAdmin bool   `json:"is_company_admin"`
}

// updateEmployeeRequest çalışan güncelleme gövdesidir.
//
// Limitin KALDIRILMASI ayrı bir bayrakla istenir. Sebebi encoding/json'un
// sınırıdır: "spending_limit": null ile alanın hiç gönderilmemesi Go tarafında
// aynı nil işaretçiye çözülür, dolayısıyla "dokunma" ile "sınırsız yap"
// ayrılamaz. Ayrılmasaydı bir kez konmuş limit hiçbir zaman kaldırılamazdı.
//
// Şirket ve müşteri alanları YOKTUR: ikisi de kaydın kimliğidir ve
// değişmeleri, kaydı güncellemek değil yenisini açmak demektir
// (bkz. service.UpdateEmployeeInput).
type updateEmployeeRequest struct {
	SpendingLimit      *int64 `json:"spending_limit"`
	ClearSpendingLimit bool   `json:"clear_spending_limit"`
	IsCompanyAdmin     *bool  `json:"is_company_admin"`
}

// toCompanyDTO şirketi yanıt gövdesine çevirir.
func toCompanyDTO(c models.Company) companyDTO {
	return companyDTO{
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
		CreatedAt:                c.CreatedAt,
		UpdatedAt:                c.UpdatedAt,
	}
}

// toEmployeeDTO çalışanı yanıt gövdesine çevirir.
func toEmployeeDTO(e models.CompanyEmployee) employeeDTO {
	return employeeDTO{
		ID:             e.ID,
		CompanyID:      e.CompanyID,
		CustomerID:     e.CustomerID,
		SpendingLimit:  e.SpendingLimit,
		IsCompanyAdmin: e.IsCompanyAdmin,
		CreatedAt:      e.CreatedAt,
		UpdatedAt:      e.UpdatedAt,
	}
}

// toStoreEmployeeDTO üyeliği vitrin yanıt gövdesine çevirir.
func toStoreEmployeeDTO(m service.Membership) storeEmployeeDTO {
	return storeEmployeeDTO{
		ID:                       m.Employee.ID,
		CompanyID:                m.Employee.CompanyID,
		CustomerID:               m.Employee.CustomerID,
		SpendingLimit:            m.Employee.SpendingLimit,
		SpendingLimitResetPeriod: string(m.Company.SpendingLimitResetPeriod),
		SpendingWindowStart:      m.SpendingWindowStart,
		IsCompanyAdmin:           m.Employee.IsCompanyAdmin,
		CreatedAt:                m.Employee.CreatedAt,
		UpdatedAt:                m.Employee.UpdatedAt,
	}
}
