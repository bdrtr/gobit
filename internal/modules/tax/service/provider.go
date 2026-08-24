package service

import (
	"context"
	"slices"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// Entity tax'ın Query katmanına açtığı entity adıdır.
// Sağlayıcı container'a "tax_region" + query.ProviderSuffix adıyla kaydedilir.
const Entity = "tax_region"

// Sağlayıcının sunduğu alan adları.
const (
	fieldID           = "id"
	fieldCountryCode  = "country_code"
	fieldProvinceCode = "province_code"
	fieldParentID     = "parent_id"
	fieldProviderID   = "provider_id"
	fieldMetadata     = "metadata"
	fieldRates        = "rates"
	fieldCreatedAt    = "created_at"
	fieldUpdatedAt    = "updated_at"
)

// Alt kayıtların (oran) alan adları.
const (
	fieldName      = "name"
	fieldCode      = "code"
	fieldRateBps   = "rate_bps"
	fieldIsDefault = "is_default"
)

// supportedFields sağlayıcının tanıdığı alanlardır; başka bir alan istenirse
// errors.Invalid dönülür (ADR 0004: alan doğrulaması sağlayıcıya aittir).
var supportedFields = []string{
	fieldID, fieldCountryCode, fieldProvinceCode, fieldParentID, fieldProviderID,
	fieldMetadata, fieldRates, fieldCreatedAt, fieldUpdatedAt,
}

// QueryProvider vergi bölgelerini Query katmanına açar (ADR 0004).
//
// Kayıtlar istenirse ORANLARIYLA birlikte döner. Ayrı dönselerdi, bölge başına
// ikinci bir tur gerekirdi ki Query'nin N+1 yasağı tam da bunu engellemek
// içindir. İstenmiyorsa Fields ile dışarıda bırakılır; o durumda oran sorgusu
// HİÇ yapılmaz.
//
// # Neden vergi bölgesi sorgulanabilir olmalı
//
// Bir yönetim ekranı "hangi ülkelerde vergi yapılandırılmış" sorusunu, bir
// rapor da "bu siparişin bölgesi neydi" sorusunu bu katmandan sorar. Vergi
// HESABI ise buradan geçmez: hesap [Service.CalculateTax]'tır ve Query'nin
// gevşek tipli kayıt yüzeyi para aritmetiği için uygun değildir.
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

// List kök vergi bölgesi kayıtlarını döner.
//
// Desteklenen filtreler "id" ve "country_code"tur; "id" değeri tek bir dize ya
// da dize dilimi olabilir. Başka bir filtre errors.Invalid döner. "id" filtresi
// YALNIZ BAŞINA kullanılır: yanına konan bir daraltma sessizce düşürülmez,
// birleşim reddedilir (bkz. splitFilters).
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
	ids, country, err := splitFilters(opts.Filters)
	if err != nil {
		return nil, err
	}

	if ids != nil {
		// Kimlik filtresi varsa sayfalama uygulanmaz: çağıran zaten kesin bir
		// kümeyi adlandırmıştır.
		return p.fetch(ctx, ids, fields)
	}

	page, err := p.svc.ListTaxRegions(ctx, country, clampToInt32(opts.Limit), clampToInt32(opts.Offset))
	if err != nil {
		return nil, err
	}
	return p.records(ctx, page.Items, fields)
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

	regions, err := p.svc.repo.GetTaxRegionsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return p.records(ctx, regions, fields)
}

// records bölgeleri Query kayıtlarına çevirir; oranları gerekiyorsa TEK
// sorguyla toplu getirir.
//
// Toplu getirme, kaç bölge dönerse dönsün genişletme başına SABİT sayıda tur
// üretir. Bölge başına sorgu yapmak, Query'nin yapısal olarak engellediği
// N+1'i sağlayıcı içine geri sokmak olurdu.
func (p *QueryProvider) records(
	ctx context.Context,
	regions []models.TaxRegion,
	fields []string,
) ([]query.Record, error) {
	records := make([]query.Record, 0, len(regions))
	if len(regions) == 0 {
		return records, nil
	}

	rates := map[string][]models.TaxRate{}
	if slices.Contains(fields, fieldRates) {
		ids := make([]string, 0, len(regions))
		for i := range regions {
			ids = append(ids, regions[i].ID)
		}

		fetched, err := p.svc.repo.ListTaxRatesByRegions(ctx, ids)
		if err != nil {
			return nil, err
		}
		for i := range fetched {
			rates[fetched[i].TaxRegionID] = append(rates[fetched[i].TaxRegionID], fetched[i])
		}
	}

	for i := range regions {
		region := regions[i]
		record := make(query.Record, len(fields))
		for _, field := range fields {
			switch field {
			case fieldID:
				record[fieldID] = region.ID
			case fieldCountryCode:
				record[fieldCountryCode] = region.CountryCode
			case fieldProvinceCode:
				record[fieldProvinceCode] = region.Province()
			case fieldParentID:
				record[fieldParentID] = region.Parent()
			case fieldProviderID:
				record[fieldProviderID] = region.ProviderID
			case fieldMetadata:
				record[fieldMetadata] = region.Metadata
			case fieldRates:
				record[fieldRates] = rateRecords(rates[region.ID])
			case fieldCreatedAt:
				record[fieldCreatedAt] = region.CreatedAt
			case fieldUpdatedAt:
				record[fieldUpdatedAt] = region.UpdatedAt
			}
		}
		records = append(records, record)
	}
	return records, nil
}

// rateRecords oranları alt kayıtlara çevirir.
//
// Oranı olmayan bir bölge için boş (nil olmayan) dilim döner; JSON'da null
// yerine [] görünmesi tüketici için tek biçimli bir yüzeydir.
//
// Oran BAZ PUAN olarak, alan adında birimiyle birlikte yazılır: "rate": 20
// değerinin %20 mi 0,2 mi olduğu belirsiz kalırdı.
func rateRecords(rates []models.TaxRate) []map[string]any {
	out := make([]map[string]any, 0, len(rates))
	for i := range rates {
		out = append(out, map[string]any{
			fieldID:        rates[i].ID,
			fieldName:      rates[i].Name,
			fieldCode:      rates[i].RateCode(),
			fieldRateBps:   rates[i].RateBps,
			fieldIsDefault: rates[i].IsDefault,
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
				"%q alanı %s sağlayıcısında yok (desteklenen: %v)", field, Entity, supportedFields)
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

// splitFilters filtrelerden kimlik kümesini ve ülke süzgecini çıkarır.
//
// Kimlik filtresi yoksa nil döner (kimlik süzgeci uygulanmaz). Boş bir dilim,
// nil'den AYRI bir anlam taşır: "hiçbir kimlik" demektir ve boş sonuç döner.
//
// Kimlik filtresi BAŞKA bir filtreyle birlikte verilirse errors.Invalid döner.
// Kimlik yolu kesin bir kümeyi adlandırır ve sayfalamayı da atlar; yanına konan
// daraltmayı uygulamak yerine SESSİZCE düşürmek, çağıranın istediğinden geniş
// bir kümeyi eline alması demek olurdu. Reddetmek depodaki yerleşik
// konvansiyondur (customer/service/provider.go, aynı kombinasyon) ve bu
// sağlayıcının kendi ilkesiyle de tutarlıdır: desteklenmeyen bir filtre zaten
// reddediliyorken DESTEKLENEN bir filtreyi yok saymak çelişki olurdu.
func splitFilters(filters map[string]any) (ids []string, countryCode string, err error) {
	if len(filters) == 0 {
		return nil, "", nil
	}

	for name, value := range filters {
		switch name {
		case fieldID:
			ids, err = stringOrSlice(fieldID, value)
			if err != nil {
				return nil, "", err
			}
		case fieldCountryCode:
			code, ok := value.(string)
			if !ok {
				return nil, "", errors.Invalid(CodeInvalidInput,
					"%q filtresi dize olmalı, %T verildi", fieldCountryCode, value)
			}
			countryCode = code
		default:
			return nil, "", errors.Invalid(CodeInvalidInput,
				"%q filtresi %s sağlayıcısında desteklenmiyor (desteklenen: %q, %q)",
				name, Entity, fieldID, fieldCountryCode)
		}
	}

	if ids != nil && len(filters) > 1 {
		return nil, "", errors.Invalid(CodeInvalidInput,
			"%q filtresi başka filtrelerle birlikte kullanılamaz", fieldID)
	}
	return ids, countryCode, nil
}

// stringOrSlice tek bir dizeyi ya da dize dilimini kimlik kümesine çevirir.
func stringOrSlice(field string, value any) ([]string, error) {
	switch typed := value.(type) {
	case string:
		return []string{typed}, nil
	case []string:
		out := slices.Clone(typed)
		if out == nil {
			out = []string{}
		}
		return out, nil
	default:
		return nil, errors.Invalid(CodeInvalidInput,
			"%q filtresi dize ya da dize dilimi olmalı, %T verildi", field, value)
	}
}
