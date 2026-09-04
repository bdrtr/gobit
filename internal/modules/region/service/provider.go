package service

import (
	"context"
	"slices"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/region/models"
)

// Entity region'ın Query katmanına açtığı entity adıdır.
// Sağlayıcı container'a "region" + query.ProviderSuffix adıyla kaydedilir.
const Entity = "region"

// Sağlayıcının sunduğu alan adları.
const (
	fieldID             = "id"
	fieldName           = "name"
	fieldCurrencyCode   = "currency_code"
	fieldAutomaticTaxes = "automatic_taxes"
	fieldTaxRate        = "tax_rate"
	fieldCurrency       = "currency"
	fieldCountries      = "countries"
	fieldCreatedAt      = "created_at"
	fieldUpdatedAt      = "updated_at"
)

// Alt kayıtların (para birimi, ülke) alan adları.
const (
	fieldCode          = "code"
	fieldSymbol        = "symbol"
	fieldDecimalDigits = "decimal_digits"
)

// supportedFields sağlayıcının tanıdığı alanlardır; başka bir alan istenirse
// errors.Invalid dönülür (ADR 0004: alan doğrulaması sağlayıcıya aittir).
var supportedFields = []string{
	fieldID, fieldName, fieldCurrencyCode, fieldAutomaticTaxes, fieldTaxRate,
	fieldCurrency, fieldCountries, fieldCreatedAt, fieldUpdatedAt,
}

// QueryProvider bölgeleri Query katmanına açar (ADR 0004).
//
// Kayıtlar para birimi ve ülkeleriyle BİRLİKTE döner. Bu bilinçlidir:
// sağlayıcının tüketicisi vitrinin bölge/para birimi seçimi ve (Faz 5'te)
// sepetin bölge genişletmesidir; ikisi de bölgeyi para biriminden ayrı
// düşünmez. Ayrı dönselerdi her bölge için ikinci bir tur gerekirdi ki
// Query'nin N+1 yasağı tam da bunu engellemek içindir.
//
// İstenmiyorsa Fields ile dışarıda bırakılabilir; o durumda ilgili sorgular
// HİÇ yapılmaz.
//
// # Ondalık basamak neden burada
//
// Kayıttaki "currency" alt kaydı [models.Currency.DecimalDigits] taşır. Sepet
// ve fiyat tutarları minor unit tam sayıdır; bölgeyi okuyan bir vitrin, bölme
// çarpanını aynı yanıttan öğrenmezse ikinci bir uç noktaya gitmek ya da sabit
// 100 varsaymak zorunda kalırdı — ikincisi yen tutarlarını yüz kat küçük
// gösterirdi.
//
// Arayüz internal/core/query'de tanımlıdır; bu tip yalnızca imzayı karşılar ve
// çekirdeğe hiçbir şey bildirmez (ADR 0001'in sağlayıcı tarafı).
type QueryProvider struct {
	svc *Service
}

var _ query.Provider = (*QueryProvider)(nil)

// NewQueryProvider verilen servis üzerinde çalışan bir sağlayıcı üretir.
func NewQueryProvider(svc *Service) *QueryProvider {
	return &QueryProvider{svc: svc}
}

// Entity sağlayıcının sunduğu entity adını döner.
func (p *QueryProvider) Entity() string { return Entity }

// List kök bölge kayıtlarını döner.
//
// Desteklenen tek filtre "id"dir; değeri tek bir dize ya da dize dilimi
// olabilir. Başka bir filtre errors.Invalid döner.
//
// Limit sıfır verilirse Query sözleşmesindeki "sınırsız" YERİNE modülün
// varsayılan sayfa boyu uygulanır ve [MaxLimit] aşılamaz: sınırsız bir kök
// listesi tek istekte tüm tabloyu belleğe alırdı.
func (p *QueryProvider) List(ctx context.Context, opts query.ListOptions) ([]query.Record, error) {
	if err := p.svc.ready(); err != nil {
		return nil, err
	}
	fields, err := normalizeFields(opts.Fields)
	if err != nil {
		return nil, err
	}
	ids, err := idFilter(opts.Filters)
	if err != nil {
		return nil, err
	}

	if ids != nil {
		// Kimlik filtresi varsa sayfalama uygulanmaz: çağıran zaten kesin bir
		// kümeyi adlandırmıştır.
		return p.fetch(ctx, ids, fields)
	}

	limit, offset, err := normalizePaging(clampToInt32(opts.Limit), clampToInt32(opts.Offset))
	if err != nil {
		return nil, err
	}

	regions, _, err := p.svc.repo.ListRegions(ctx, limit, offset)
	if err != nil {
		return nil, err
	}
	return p.records(ctx, regions, fields)
}

// FetchByIDs verilen kimliklere karşılık gelen kayıtları TEK turda döner.
//
// Bulunamayan kimlik için kayıt dönmez; bu bir hata değildir (ADR 0004).
func (p *QueryProvider) FetchByIDs(ctx context.Context, ids, fields []string) ([]query.Record, error) {
	if err := p.svc.ready(); err != nil {
		return nil, err
	}
	normalized, err := normalizeFields(fields)
	if err != nil {
		return nil, err
	}
	return p.fetch(ctx, ids, normalized)
}

// fetch kimlik kümesini okuyup kayıtlara çevirir.
func (p *QueryProvider) fetch(ctx context.Context, ids, fields []string) ([]query.Record, error) {
	if len(ids) == 0 {
		return []query.Record{}, nil
	}

	regions, err := p.svc.repo.GetRegionsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return p.records(ctx, regions, fields)
}

// records bölgeleri Query kayıtlarına çevirir; para birimlerini ve ülkeleri
// gerekiyorsa TEK sorguyla toplu getirir.
//
// Toplu getirme, kaç bölge dönerse dönsün genişletme başına SABİT sayıda tur
// üretir: bir bölge de yüz bölge de aynı sorgu sayısını yapar. Bölge başına
// sorgu yapmak, Query'nin yapısal olarak engellediği N+1'i sağlayıcı içine
// geri sokmak olurdu.
func (p *QueryProvider) records(
	ctx context.Context,
	regions []models.Region,
	fields []string,
) ([]query.Record, error) {
	records := make([]query.Record, 0, len(regions))
	if len(regions) == 0 {
		return records, nil
	}

	currencies := map[string]models.Currency{}
	if slices.Contains(fields, fieldCurrency) {
		codes := make([]string, 0, len(regions))
		for _, region := range regions {
			if !slices.Contains(codes, region.CurrencyCode) {
				codes = append(codes, region.CurrencyCode)
			}
		}

		fetched, err := p.svc.repo.GetCurrenciesByCodes(ctx, codes)
		if err != nil {
			return nil, err
		}
		for _, currency := range fetched {
			currencies[currency.Code] = currency
		}
	}

	countries := map[string][]models.Country{}
	if slices.Contains(fields, fieldCountries) {
		ids := make([]string, 0, len(regions))
		for _, region := range regions {
			ids = append(ids, region.ID)
		}

		fetched, err := p.svc.repo.ListCountriesByRegions(ctx, ids)
		if err != nil {
			return nil, err
		}
		countries = fetched
	}

	for _, region := range regions {
		record := make(query.Record, len(fields))
		for _, field := range fields {
			switch field {
			case fieldID:
				record[fieldID] = region.ID
			case fieldName:
				record[fieldName] = region.Name
			case fieldCurrencyCode:
				record[fieldCurrencyCode] = region.CurrencyCode
			case fieldAutomaticTaxes:
				record[fieldAutomaticTaxes] = region.AutomaticTaxes
			case fieldTaxRate:
				record[fieldTaxRate] = region.TaxRate
			case fieldCreatedAt:
				record[fieldCreatedAt] = region.CreatedAt
			case fieldUpdatedAt:
				record[fieldUpdatedAt] = region.UpdatedAt
			case fieldCurrency:
				record[fieldCurrency] = currencyRecord(currencies, region.CurrencyCode)
			case fieldCountries:
				record[fieldCountries] = countryRecords(countries[region.ID])
			}
		}
		records = append(records, record)
	}
	return records, nil
}

// currencyRecord para birimini alt kayda çevirir; bulunamadıysa nil döner.
//
// nil dönmesi bilinçlidir: eksik bir para biriminin yerine boş bir kayıt
// koymak, ondalık basamağı 0 gibi göstererek tutarları yanlış ölçekte
// gösterirdi. Foreign key nedeniyle bu durum normalde oluşamaz.
func currencyRecord(currencies map[string]models.Currency, code string) map[string]any {
	currency, ok := currencies[code]
	if !ok {
		return nil
	}
	return map[string]any{
		fieldCode:          currency.Code,
		fieldSymbol:        currency.Symbol,
		fieldName:          currency.Name,
		fieldDecimalDigits: currency.DecimalDigits,
	}
}

// countryRecords ülkeleri alt kayıtlara çevirir.
//
// Ülkesi olmayan bir bölge için boş (nil olmayan) dilim döner; JSON'da null
// yerine [] görünmesi tüketici için tek biçimli bir yüzeydir.
func countryRecords(countries []models.Country) []map[string]any {
	out := make([]map[string]any, 0, len(countries))
	for i := range countries {
		out = append(out, map[string]any{
			fieldCode: countries[i].Code,
			fieldName: countries[i].Name,
		})
	}
	return out
}

// normalizeFields istenen alanları doğrular; boş liste TÜM alanlar demektir.
//
// Kimlik alanı, istenmese bile listeye EKLENİR: Query kayıtları [query.IDField]
// üzerinden birleştirir ve kimliksiz bir kayıt errors.KindInternal ile
// sonuçlanırdı.
func normalizeFields(fields []string) ([]string, error) {
	if len(fields) == 0 {
		return slices.Clone(supportedFields), nil
	}

	out := make([]string, 0, len(fields)+1)
	for _, field := range fields {
		if !slices.Contains(supportedFields, field) {
			return nil, errors.Invalid(CodeInvalidInput,
				"field %q does not exist in the %s provider (supported: %v)", field, Entity, supportedFields)
		}
		if !slices.Contains(out, field) {
			out = append(out, field)
		}
	}
	if !slices.Contains(out, fieldID) {
		out = append(out, fieldID)
	}
	return out, nil
}

// idFilter filtrelerden kimlik kümesini çıkarır.
//
// Filtre yoksa nil döner (kimlik süzgeci uygulanmaz); "id" dışında bir filtre
// varsa errors.Invalid döner. Boş bir dilim, nil'den AYRI bir anlam taşır:
// "hiçbir kimlik" demektir ve boş sonuç döner.
func idFilter(filters map[string]any) ([]string, error) {
	if len(filters) == 0 {
		return nil, nil
	}

	var ids []string
	for name, value := range filters {
		if name != fieldID {
			return nil, errors.Invalid(CodeInvalidInput,
				"filter %q is not supported by the %s provider (supported: %q)", name, Entity, fieldID)
		}
		switch typed := value.(type) {
		case string:
			ids = []string{typed}
		case []string:
			ids = slices.Clone(typed)
			if ids == nil {
				ids = []string{}
			}
		default:
			return nil, errors.Invalid(CodeInvalidInput,
				"filter %q has to be a string or a string slice, %T given", fieldID, value)
		}
	}
	return ids, nil
}
