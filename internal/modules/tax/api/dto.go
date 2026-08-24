package api

import (
	"time"

	"github.com/bdrtr/gobit/internal/modules/tax/models"
	"github.com/bdrtr/gobit/internal/modules/tax/service"
)

// DTO'lar domain modellerinden AYRI tutulur: JSON alan adları dış sözleşmedir
// ve modelde yapılan bir yeniden adlandırma istemciyi kırmamalıdır.

// taxRegionDTO bir vergi bölgesinin yanıt gövdesidir.
type taxRegionDTO struct {
	// ID bölgenin kimliğidir.
	ID string `json:"id"`
	// CountryCode ISO 3166-1 alpha-2 kodudur (BÜYÜK harf).
	CountryCode string `json:"country_code"`
	// ProvinceCode eyalet/il kodudur; kök bölgede null.
	ProvinceCode *string `json:"province_code"`
	// ParentID kök bölgenin kimliğidir; kök bölgede null.
	ParentID *string `json:"parent_id"`
	// ProviderID vergi sağlayıcısının kimliğidir. Boş ise eyalet bölgesinde
	// ülkenin sağlayıcısı devralınır, kök bölgede yerel hesaplama uygulanır.
	ProviderID string `json:"provider_id"`
	// Metadata serbest üstveridir; boşsa alan hiç görünmez.
	Metadata map[string]any `json:"metadata,omitempty"`
	// CreatedAt oluşturulma anıdır (RFC3339, UTC).
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt son güncellenme anıdır (RFC3339, UTC).
	UpdatedAt time.Time `json:"updated_at"`
}

// taxRateDTO bir vergi oranının yanıt gövdesidir.
type taxRateDTO struct {
	// ID oranın kimliğidir.
	ID string `json:"id"`
	// TaxRegionID oranın ait olduğu bölgedir.
	TaxRegionID string `json:"tax_region_id"`
	// Name oranın görünen adıdır.
	Name string `json:"name"`
	// Code mutabakat kodudur; verilmediyse null.
	Code *string `json:"code"`
	// RateBps orandır; BAZ PUAN cinsindendir (2000 = %20).
	//
	// Alan adının sonundaki birim bilinçlidir: "rate": 20 gövdesi %20 mi
	// yoksa 0,2 mi olduğu belirsiz kalırdı ve istemci tarafında yüz kat hata
	// üretirdi. Baz puan tam sayıdır, float değildir (plan Bölüm 8).
	RateBps int32 `json:"rate_bps"`
	// IsDefault bölgenin varsayılan oranı olup olmadığıdır.
	IsDefault bool `json:"is_default"`
	// Metadata serbest üstveridir; boşsa alan hiç görünmez.
	Metadata map[string]any `json:"metadata,omitempty"`
	// CreatedAt oluşturulma anıdır (RFC3339, UTC).
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt son güncellenme anıdır (RFC3339, UTC).
	UpdatedAt time.Time `json:"updated_at"`
}

// taxRateRuleDTO bir vergi kuralının yanıt gövdesidir.
type taxRateRuleDTO struct {
	// ID kuralın kimliğidir.
	ID string `json:"id"`
	// TaxRateID kuralın bağlı olduğu orandır.
	TaxRateID string `json:"tax_rate_id"`
	// Reference kalemin türüdür: "product", "product_type" ya da
	// "shipping_option".
	Reference string `json:"reference"`
	// ReferenceID o türdeki kimliktir; BAŞKA bir modüle aittir ve bu modül
	// varlığını doğrulamaz.
	ReferenceID string `json:"reference_id"`
	// CreatedAt oluşturulma anıdır (RFC3339, UTC).
	CreatedAt time.Time `json:"created_at"`
}

// createTaxRegionRequest vergi bölgesi oluşturma isteğidir.
type createTaxRegionRequest struct {
	// CountryCode ISO 3166-1 alpha-2 kodudur; büyük/küçük harf serbesttir.
	// Zorunludur.
	CountryCode string `json:"country_code"`
	// ProvinceCode eyalet/il kodudur. Boş bırakılırsa ÜLKE KÖKÜ oluşturulur;
	// dolu verilirse parent_id de zorunludur.
	ProvinceCode string `json:"province_code"`
	// ParentID eyalet bölgesinin bağlanacağı ülke köküdür.
	ParentID string `json:"parent_id"`
	// ProviderID vergi sağlayıcısının kimliğidir ve KAYITLI olmalıdır;
	// kayıtlı olmayan bir kimlik 422 ile reddedilir. Boş bırakılırsa eyalet
	// bölgesi ülkenin sağlayıcısını devralır, kök bölge yerel hesaplama
	// kullanır.
	ProviderID string `json:"provider_id"`
	// Metadata serbest üstveridir.
	Metadata map[string]any `json:"metadata"`
}

// createTaxRateRequest vergi oranı oluşturma isteğidir.
type createTaxRateRequest struct {
	// TaxRegionID oranın ekleneceği bölgedir; zorunludur.
	//
	// Gövdede taşınması bilinçlidir: /admin/v1/tax-rates altına POST edilen bir
	// oran, hangi bölgeye ait olduğunu kendi gövdesinde söyler ve aynı gövde
	// bölgenin alt kaynağına (…/tax-regions/{id}/tax-rates) taşınırsa yol ile
	// gövde çelişebilirdi. Bu modülde oran yalnızca /admin/v1/tax-rates
	// üzerinden YAZILIR; bölge altındaki uç yalnızca OKUMADIR.
	TaxRegionID string `json:"tax_region_id"`
	// Name oranın görünen adıdır; zorunludur.
	Name string `json:"name"`
	// Code mutabakat kodudur; boş bırakılabilir.
	Code string `json:"code"`
	// RateBps orandır (baz puan; 2000 = %20).
	RateBps int32 `json:"rate_bps"`
	// IsDefault bölgenin varsayılan oranı olup olmadığıdır.
	IsDefault bool `json:"is_default"`
	// Metadata serbest üstveridir.
	Metadata map[string]any `json:"metadata"`
}

// updateTaxRateRequest vergi oranı güncelleme isteğidir.
//
// Tüm alanlar işaretçidir: verilmeyen alan DEĞİŞMEZ. Tam gövde istenseydi,
// gövdesinde rate_bps göndermeyi unutan bir istemci oranı sessizce sıfırlardı.
type updateTaxRateRequest struct {
	// Name yeni addır; null/eksikse ad değişmez.
	Name *string `json:"name"`
	// Code yeni mutabakat kodudur; null/eksikse değişmez. Kodu KALDIRMAK için
	// boş dize gönderilir.
	Code *string `json:"code"`
	// RateBps yeni orandır (baz puan); null/eksikse oran değişmez.
	RateBps *int32 `json:"rate_bps"`
	// IsDefault varsayılanlık bayrağıdır; null/eksikse değişmez.
	IsDefault *bool `json:"is_default"`
	// Metadata yeni üstveridir; null/eksikse üstveri değişmez.
	Metadata map[string]any `json:"metadata"`
}

// createTaxRateRuleRequest vergi kuralı oluşturma isteğidir.
type createTaxRateRuleRequest struct {
	// Reference kalemin türüdür: "product", "product_type" ya da
	// "shipping_option".
	Reference string `json:"reference"`
	// ReferenceID o türdeki kimliktir; zorunludur.
	ReferenceID string `json:"reference_id"`
}

// toTaxRegionDTO bölge modelini yanıt gövdesine çevirir.
func toTaxRegionDTO(region models.TaxRegion) taxRegionDTO {
	return taxRegionDTO{
		ID:           region.ID,
		CountryCode:  region.CountryCode,
		ProvinceCode: region.ProvinceCode,
		ParentID:     region.ParentID,
		ProviderID:   region.ProviderID,
		Metadata:     region.Metadata,
		CreatedAt:    region.CreatedAt,
		UpdatedAt:    region.UpdatedAt,
	}
}

// toTaxRateDTO oran modelini yanıt gövdesine çevirir.
func toTaxRateDTO(rate models.TaxRate) taxRateDTO {
	return taxRateDTO{
		ID:          rate.ID,
		TaxRegionID: rate.TaxRegionID,
		Name:        rate.Name,
		Code:        rate.Code,
		RateBps:     rate.RateBps,
		IsDefault:   rate.IsDefault,
		Metadata:    rate.Metadata,
		CreatedAt:   rate.CreatedAt,
		UpdatedAt:   rate.UpdatedAt,
	}
}

// toTaxRateRuleDTO kural modelini yanıt gövdesine çevirir.
func toTaxRateRuleDTO(rule models.TaxRateRule) taxRateRuleDTO {
	return taxRateRuleDTO{
		ID:          rule.ID,
		TaxRateID:   rule.TaxRateID,
		Reference:   rule.Reference.String(),
		ReferenceID: rule.ReferenceID,
		CreatedAt:   rule.CreatedAt,
	}
}

// toCreateTaxRegionInput istek gövdesini servis girdisine çevirir.
//
// Doğrulama YAPILMAZ: geçerliliğe servis karar verir ve tek bir doğrulama
// yerinin olması, HTTP ile modüller arası çağrının aynı kuralları görmesini
// sağlar.
func toCreateTaxRegionInput(req createTaxRegionRequest) service.CreateTaxRegionInput {
	return service.CreateTaxRegionInput{
		CountryCode:  req.CountryCode,
		ProvinceCode: req.ProvinceCode,
		ParentID:     req.ParentID,
		ProviderID:   req.ProviderID,
		Metadata:     req.Metadata,
	}
}

// toCreateTaxRateInput istek gövdesini servis girdisine çevirir.
func toCreateTaxRateInput(req createTaxRateRequest) service.CreateTaxRateInput {
	return service.CreateTaxRateInput{
		TaxRegionID: req.TaxRegionID,
		Name:        req.Name,
		Code:        req.Code,
		RateBps:     req.RateBps,
		IsDefault:   req.IsDefault,
		Metadata:    req.Metadata,
	}
}

// toUpdateTaxRateInput istek gövdesini servis girdisine çevirir.
func toUpdateTaxRateInput(req updateTaxRateRequest) service.UpdateTaxRateInput {
	return service.UpdateTaxRateInput{
		Name:      req.Name,
		Code:      req.Code,
		RateBps:   req.RateBps,
		IsDefault: req.IsDefault,
		Metadata:  req.Metadata,
	}
}
