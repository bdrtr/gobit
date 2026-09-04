package service

import (
	"context"
	"slices"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// Entity customer'ın Query katmanına açtığı entity adıdır.
// Sağlayıcı container'a "customer" + query.ProviderSuffix adıyla kaydedilir.
const Entity = "customer"

// Sağlayıcının sunduğu alan adları.
const (
	fieldID         = "id"
	fieldEmail      = "email"
	fieldFirstName  = "first_name"
	fieldLastName   = "last_name"
	fieldPhone      = "phone"
	fieldHasAccount = "has_account"
	fieldGroupIDs   = "group_ids"
	fieldMetadata   = "metadata"
	fieldCreatedAt  = "created_at"
	fieldUpdatedAt  = "updated_at"
)

// Sağlayıcının tanıdığı filtre adları.
const (
	filterID         = "id"
	filterEmail      = "email"
	filterHasAccount = "has_account"
	filterGroupID    = "group_id"
)

// supportedFields sağlayıcının tanıdığı alanlardır; başka bir alan istenirse
// errors.Invalid dönülür (ADR 0004: alan doğrulaması sağlayıcıya aittir).
var supportedFields = []string{
	fieldID, fieldEmail, fieldFirstName, fieldLastName, fieldPhone,
	fieldHasAccount, fieldGroupIDs, fieldMetadata, fieldCreatedAt, fieldUpdatedAt,
}

// supportedFilters sağlayıcının tanıdığı filtrelerdir.
var supportedFilters = []string{filterID, filterEmail, filterHasAccount, filterGroupID}

// QueryProvider müşterileri Query katmanına açar (ADR 0004).
//
// # Neden grup kimlikleriyle birlikte
//
// Kayıtlar müşterinin GRUP KİMLİKLERİYLE döner. Bunun tüketicisi fiyat
// hesabıdır: pricing'in kural bağlamı "customer_group_id" özniteliğine bakar
// ve bir sepetin fiyatlanması için müşterinin hangi segmentlerde olduğu
// bilinmelidir. Grup kimlikleri ayrı bir turda istenseydi bu, her müşteri için
// ikinci bir çağrı demekti; Query'nin N+1 yasağı tam da bunu engellemek
// içindir. Kimlikler müşteri başına değil, KÜME olarak tek sorguda getirilir.
//
// Grup kimlikleri istenmiyorsa Fields ile dışarıda bırakılabilir; o durumda
// üyelik sorgusu HİÇ çalıştırılmaz.
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

// List kök müşteri kayıtlarını döner.
//
// Desteklenen filtreler: "id" (dize ya da dize dilimi), "email", "has_account",
// "group_id". "id" filtresi DİĞERLERİYLE BİRLEŞTİRİLEMEZ — kesin bir kimlik
// kümesi zaten adlandırılmışken ikinci bir süzgeç, çağıranın istediği kaydın
// sessizce elenmesi demek olurdu ve sonuç boş dönerdi.
//
// Limit sıfır verilirse Query sözleşmesindeki "sınırsız" YERİNE modülün
// varsayılan sayfa boyu uygulanır: sınırsız bir kök listesi tek istekte tüm
// müşteri tablosunu belleğe alırdı.
func (p *QueryProvider) List(ctx context.Context, opts query.ListOptions) ([]query.Record, error) {
	if err := p.svc.ready(); err != nil {
		return nil, err
	}
	fields, err := normalizeFields(opts.Fields)
	if err != nil {
		return nil, err
	}
	ids, filter, err := splitFilters(opts.Filters)
	if err != nil {
		return nil, err
	}

	if ids != nil {
		// Kimlik filtresi varsa sayfalama uygulanmaz: çağıran zaten kesin bir
		// kümeyi adlandırmıştır.
		return p.fetch(ctx, ids, fields)
	}

	limit, offset, err := normalizePaging(clampToInt64(opts.Limit), clampToInt64(opts.Offset))
	if err != nil {
		return nil, err
	}

	customers, _, err := p.svc.repo.ListCustomers(ctx, filter, limit, offset)
	if err != nil {
		return nil, err
	}
	return p.records(ctx, customers, fields)
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

	customers, err := p.svc.repo.GetCustomersByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return p.records(ctx, customers, fields)
}

// records müşterileri Query kayıtlarına çevirir; gerekiyorsa grup kimliklerini
// TEK sorguyla toplu getirir.
func (p *QueryProvider) records(
	ctx context.Context,
	customers []models.Customer,
	fields []string,
) ([]query.Record, error) {
	records := make([]query.Record, 0, len(customers))
	if len(customers) == 0 {
		return records, nil
	}

	groupsByCustomer := map[string][]string{}
	if slices.Contains(fields, fieldGroupIDs) {
		ids := make([]string, 0, len(customers))
		for i := range customers {
			ids = append(ids, customers[i].ID)
		}

		var err error
		groupsByCustomer, err = p.svc.repo.GroupIDsOfCustomers(ctx, ids)
		if err != nil {
			return nil, err
		}
	}

	for i := range customers {
		c := &customers[i]
		record := make(query.Record, len(fields))
		for _, field := range fields {
			switch field {
			case fieldID:
				record[fieldID] = c.ID
			case fieldEmail:
				record[fieldEmail] = c.Email
			case fieldFirstName:
				record[fieldFirstName] = c.FirstName
			case fieldLastName:
				record[fieldLastName] = c.LastName
			case fieldPhone:
				record[fieldPhone] = c.Phone
			case fieldHasAccount:
				record[fieldHasAccount] = c.HasAccount
			case fieldGroupIDs:
				record[fieldGroupIDs] = groupIDs(groupsByCustomer[c.ID])
			case fieldMetadata:
				record[fieldMetadata] = c.Metadata
			case fieldCreatedAt:
				record[fieldCreatedAt] = c.CreatedAt
			case fieldUpdatedAt:
				record[fieldUpdatedAt] = c.UpdatedAt
			}
		}
		records = append(records, record)
	}
	return records, nil
}

// groupIDs grup kimliklerini kayıt için hazırlar.
//
// Hiç grubu olmayan müşteri için boş (nil olmayan) dilim döner; JSON'da null
// yerine [] görünmesi tüketici için tek biçimli bir yüzeydir. Dilim ayrıca
// KOPYALANIR: Query kayıtları sığ kopyalar ve dilimin kendisi paylaşılırsa
// çağıranlar aynı arka diziyi görürdü.
func groupIDs(ids []string) []string {
	if len(ids) == 0 {
		return []string{}
	}
	return slices.Clone(ids)
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

// splitFilters filtreleri kimlik kümesine ve süzgece ayırır.
//
// Kimlik filtresi yoksa nil kimlik dilimi döner. Boş bir dilim, nil'den AYRI
// bir anlam taşır: "hiçbir kimlik" demektir ve boş sonuç döner.
func splitFilters(filters map[string]any) ([]string, models.CustomerFilter, error) {
	var (
		ids    []string
		filter models.CustomerFilter
	)
	if len(filters) == 0 {
		return nil, filter, nil
	}

	for name, value := range filters {
		switch name {
		case filterID:
			parsed, err := stringSet(name, value)
			if err != nil {
				return nil, filter, err
			}
			ids = parsed
		case filterEmail:
			raw, err := stringValue(name, value)
			if err != nil {
				return nil, filter, err
			}
			normalized, err := normalizeEmail(raw)
			if err != nil {
				return nil, filter, err
			}
			filter.Email = &normalized
		case filterHasAccount:
			flag, ok := value.(bool)
			if !ok {
				return nil, filter, errors.Invalid(CodeInvalidInput,
					"filter %q has to be a boolean, %T given", name, value)
			}
			filter.HasAccount = &flag
		case filterGroupID:
			raw, err := stringValue(name, value)
			if err != nil {
				return nil, filter, err
			}
			if err := requireID(raw, models.CustomerGroupIDPrefix, "group id"); err != nil {
				return nil, filter, err
			}
			filter.GroupID = &raw
		default:
			return nil, filter, errors.Invalid(CodeInvalidInput,
				"filter %q is not supported by the %s provider (supported: %v)",
				name, Entity, supportedFilters)
		}
	}

	if ids != nil && len(filters) > 1 {
		return nil, filter, errors.Invalid(CodeInvalidInput,
			"filter %q cannot be used together with other filters", filterID)
	}
	return ids, filter, nil
}

// stringSet bir filtre değerini kimlik kümesine çevirir.
func stringSet(name string, value any) ([]string, error) {
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
			"filter %q has to be a string or a string slice, %T given", name, value)
	}
}

// stringValue bir filtre değerini tek dizeye çevirir.
func stringValue(name string, value any) (string, error) {
	typed, ok := value.(string)
	if !ok {
		return "", errors.Invalid(CodeInvalidInput,
			"filter %q has to be a string, %T given", name, value)
	}
	return typed, nil
}

// clampToInt64 Query'nin int sayfalama değerini servisin int64 yüzeyine taşır.
//
// Dönüşüm her platformda kayıpsızdır: int en fazla 64 bittir. Negatif değer
// düzeltilmeden geçirilir ki normalizePaging onu REDDETSİN — sessizce sıfıra
// çekmek, istemcinin hatalı isteğini gizlerdi.
func clampToInt64(n int) int64 {
	return int64(n)
}
