package api

import (
	"time"

	"github.com/bdrtr/gobit/internal/modules/region/models"
	"github.com/bdrtr/gobit/internal/modules/region/service"
)

// DTO'lar domain modellerinden AYRI tutulur: JSON alan adları dış sözleşmedir
// ve modelde yapılan bir yeniden adlandırma istemciyi kırmamalıdır.

// regionDTO bir bölgenin yanıt gövdesidir.
type regionDTO struct {
	// ID bölgenin kimliğidir.
	ID string `json:"id"`
	// Name bölgenin görünen adıdır.
	Name string `json:"name"`
	// CurrencyCode bölgenin para birimidir (ISO 4217, BÜYÜK harf).
	CurrencyCode string `json:"currency_code"`
	// AutomaticTaxes verginin otomatik uygulanıp uygulanmayacağıdır.
	AutomaticTaxes bool `json:"automatic_taxes"`
	// TaxRate GEÇİCİ vergi oranıdır; BAZ PUAN cinsindendir (2000 = %20).
	//
	// Alan adının sonundaki birim bilinçlidir: "tax_rate": 20 gövdesi %20 mi
	// yoksa 0,2 mi olduğu belirsiz kalırdı ve istemci tarafında yüz kat hata
	// üretirdi. Baz puan tam sayıdır, float değildir (plan Bölüm 8).
	TaxRateBps int32 `json:"tax_rate_bps"`
	// CreatedAt oluşturulma anıdır (RFC3339, UTC).
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt son güncellenme anıdır (RFC3339, UTC).
	UpdatedAt time.Time `json:"updated_at"`
}

// storeRegionDTO bir bölgenin MÜŞTERİYE dönen gövdesidir.
//
// Yönetim gövdesinden farkı bilinçlidir: vitrin para biriminin sembolünü ve
// ondalık basamağını görmelidir (tutarlar minor unit tam sayıdır), ama vergi
// oranı ve otomatik vergi bayrağı iş yapılandırmasıdır ve müşteriye gitmez —
// vergi, sepet toplamının içinde hesaplanmış olarak görünür.
type storeRegionDTO struct {
	// ID bölgenin kimliğidir.
	ID string `json:"id"`
	// Name bölgenin görünen adıdır.
	Name string `json:"name"`
	// CurrencyCode bölgenin para birimidir.
	CurrencyCode string `json:"currency_code"`
	// Currency para biriminin sunum bilgisidir; bulunamazsa null.
	Currency *currencyDTO `json:"currency"`
	// Countries bölgenin ülkeleridir; istenmediyse boş dilim.
	Countries []countryDTO `json:"countries"`
}

// currencyDTO bir para biriminin yanıt gövdesidir.
type currencyDTO struct {
	// Code ISO 4217 kodudur (BÜYÜK harf).
	Code string `json:"code"`
	// Symbol gösterim sembolüdür.
	Symbol string `json:"symbol"`
	// Name para biriminin ISO'daki İngilizce adıdır.
	Name string `json:"name"`
	// DecimalDigits ondalık basamak sayısıdır (TRY/USD 2, JPY 0, KWD 3).
	//
	// Tutarlar minor unit TAM SAYI olarak taşınır; sunum katmanı bölme
	// çarpanını (10^DecimalDigits) buradan öğrenir. Sabit 100 varsayan bir
	// istemci yen tutarlarını yüz kat küçük gösterir.
	DecimalDigits int32 `json:"decimal_digits"`
}

// countryDTO bir ülkenin yanıt gövdesidir.
type countryDTO struct {
	// Code ISO 3166-1 alpha-2 kodudur (BÜYÜK harf).
	Code string `json:"code"`
	// Name ülkenin ISO'daki İngilizce kısa adıdır.
	Name string `json:"name"`
	// RegionID ülkenin bağlı olduğu bölgedir; bağlı değilse null.
	RegionID *string `json:"region_id"`
}

// createRegionRequest bölge oluşturma isteğidir.
type createRegionRequest struct {
	// Name bölgenin görünen adıdır; zorunludur.
	Name string `json:"name"`
	// CurrencyCode ISO 4217 kodudur; büyük/küçük harf serbesttir. Zorunludur.
	CurrencyCode string `json:"currency_code"`
	// AutomaticTaxes verginin otomatik uygulanıp uygulanmayacağıdır.
	AutomaticTaxes bool `json:"automatic_taxes"`
	// TaxRateBps GEÇİCİ vergi oranıdır (baz puan; 2000 = %20).
	TaxRateBps int32 `json:"tax_rate_bps"`
}

// updateRegionRequest bölge güncelleme isteğidir.
//
// Tüm alanlar işaretçidir: verilmeyen alan DEĞİŞMEZ. Tam gövde istenseydi,
// gövdesinde tax_rate_bps göndermeyi unutan bir istemci oranı sessizce
// sıfırlardı.
type updateRegionRequest struct {
	// Name yeni addır; null/eksikse ad değişmez.
	Name *string `json:"name"`
	// CurrencyCode yeni para birimi kodudur; null/eksikse değişmez.
	CurrencyCode *string `json:"currency_code"`
	// AutomaticTaxes verginin otomatik uygulanıp uygulanmayacağıdır; null/eksikse değişmez.
	AutomaticTaxes *bool `json:"automatic_taxes"`
	// TaxRateBps yeni vergi oranıdır (baz puan); null/eksikse değişmez.
	TaxRateBps *int32 `json:"tax_rate_bps"`
}

// addCountryRequest bir bölgeye ülke ekleme isteğidir.
type addCountryRequest struct {
	// CountryCode ISO 3166-1 alpha-2 kodudur; büyük/küçük harf serbesttir.
	CountryCode string `json:"country_code"`
}

// toRegionDTO bölge modelini yönetim yanıt gövdesine çevirir.
func toRegionDTO(region models.Region) regionDTO {
	return regionDTO{
		ID:             region.ID,
		Name:           region.Name,
		CurrencyCode:   region.CurrencyCode,
		AutomaticTaxes: region.AutomaticTaxes,
		TaxRateBps:     region.TaxRate,
		CreatedAt:      region.CreatedAt,
		UpdatedAt:      region.UpdatedAt,
	}
}

// toCurrencyDTO para birimi modelini yanıt gövdesine çevirir.
func toCurrencyDTO(currency models.Currency) currencyDTO {
	return currencyDTO{
		Code:          currency.Code,
		Symbol:        currency.Symbol,
		Name:          currency.Name,
		DecimalDigits: currency.DecimalDigits,
	}
}

// toCountryDTO ülke modelini yanıt gövdesine çevirir.
func toCountryDTO(country models.Country) countryDTO {
	return countryDTO{
		Code:     country.Code,
		Name:     country.Name,
		RegionID: country.RegionID,
	}
}

// toCreateRegionInput istek gövdesini servis girdisine çevirir.
//
// Doğrulama YAPILMAZ: geçerliliğe servis karar verir ve tek bir doğrulama
// yerinin olması, HTTP ile modüller arası çağrının aynı kuralları görmesini
// sağlar.
func toCreateRegionInput(req createRegionRequest) service.CreateRegionInput {
	return service.CreateRegionInput{
		Name:           req.Name,
		CurrencyCode:   req.CurrencyCode,
		AutomaticTaxes: req.AutomaticTaxes,
		TaxRate:        req.TaxRateBps,
	}
}

// toUpdateRegionInput istek gövdesini servis girdisine çevirir.
func toUpdateRegionInput(req updateRegionRequest) service.UpdateRegionInput {
	return service.UpdateRegionInput{
		Name:           req.Name,
		CurrencyCode:   req.CurrencyCode,
		AutomaticTaxes: req.AutomaticTaxes,
		TaxRate:        req.TaxRateBps,
	}
}
