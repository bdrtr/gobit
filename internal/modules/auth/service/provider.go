package service

import (
	"context"
	"slices"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
)

// Entity auth'un Query katmanına açtığı entity adıdır.
//
// Modülün adı "auth" olduğu hâlde entity "sales_channel"dır: Query, bir
// genişletmenin hedefini ENTITY adından bulur ve link tanımlarının ucunda
// yazacak ad budur (örn. product ↔ sales_channel). Sağlayıcı container'a
// "sales_channel" + query.ProviderSuffix adıyla kaydedilir.
//
// Kullanıcılar ve API anahtarları Query'ye AÇILMAZ: ikisi de kimlik verisidir
// ve cross-module bir okuma yüzeyine konması, bir gün bir genişletmenin
// yönetici listesini vitrin yanıtına eklemesi demek olurdu.
const Entity = "sales_channel"

// Sağlayıcının sunduğu alan adları.
const (
	fieldID          = "id"
	fieldName        = "name"
	fieldDescription = "description"
	fieldIsDisabled  = "is_disabled"
	fieldMetadata    = "metadata"
	fieldCreatedAt   = "created_at"
	fieldUpdatedAt   = "updated_at"
)

// Sağlayıcının tanıdığı filtre adları.
const (
	filterID         = "id"
	filterName       = "name"
	filterIsDisabled = "is_disabled"
)

// supportedFields sağlayıcının tanıdığı alanlardır; başka bir alan istenirse
// errors.Invalid dönülür (ADR 0004: alan doğrulaması sağlayıcıya aittir).
var supportedFields = []string{
	fieldID, fieldName, fieldDescription, fieldIsDisabled,
	fieldMetadata, fieldCreatedAt, fieldUpdatedAt,
}

// supportedFilters sağlayıcının tanıdığı filtrelerdir.
var supportedFilters = []string{filterID, filterName, filterIsDisabled}

// QueryProvider satış kanallarını Query katmanına açar (ADR 0004).
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

// List kök satış kanalı kayıtlarını döner.
//
// Desteklenen filtreler: "id" (dize ya da dize dilimi), "name", "is_disabled".
// "id" filtresi DİĞERLERİYLE BİRLEŞTİRİLEMEZ — kesin bir kimlik kümesi zaten
// adlandırılmışken ikinci bir süzgeç, çağıranın istediği kaydın sessizce
// elenmesi demek olurdu ve sonuç boş dönerdi.
//
// Limit sıfır verilirse Query sözleşmesindeki "sınırsız" YERİNE modülün
// varsayılan sayfa boyu uygulanır.
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

	limit, offset, err := normalizePaging(int64(opts.Limit), int64(opts.Offset))
	if err != nil {
		return nil, err
	}

	channels, _, err := p.svc.repo.ListSalesChannels(ctx, filter, limit, offset)
	if err != nil {
		return nil, err
	}
	return records(channels, fields), nil
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

	channels, err := p.svc.repo.GetSalesChannelsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return records(channels, fields), nil
}

// records satış kanallarını Query kayıtlarına çevirir.
func records(channels []models.SalesChannel, fields []string) []query.Record {
	out := make([]query.Record, 0, len(channels))
	for i := range channels {
		c := &channels[i]
		record := make(query.Record, len(fields))
		for _, field := range fields {
			switch field {
			case fieldID:
				record[fieldID] = c.ID
			case fieldName:
				record[fieldName] = c.Name
			case fieldDescription:
				record[fieldDescription] = c.Description
			case fieldIsDisabled:
				record[fieldIsDisabled] = c.IsDisabled
			case fieldMetadata:
				record[fieldMetadata] = c.Metadata
			case fieldCreatedAt:
				record[fieldCreatedAt] = c.CreatedAt
			case fieldUpdatedAt:
				record[fieldUpdatedAt] = c.UpdatedAt
			}
		}
		out = append(out, record)
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

// splitFilters filtreleri kimlik kümesine ve süzgece ayırır.
//
// Kimlik filtresi yoksa nil kimlik dilimi döner. Boş bir dilim, nil'den AYRI
// bir anlam taşır: "hiçbir kimlik" demektir ve boş sonuç döner.
func splitFilters(filters map[string]any) ([]string, models.SalesChannelFilter, error) {
	var (
		ids    []string
		filter models.SalesChannelFilter
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
		case filterName:
			raw, err := stringValue(name, value)
			if err != nil {
				return nil, filter, err
			}
			filter.Name = &raw
		case filterIsDisabled:
			flag, ok := value.(bool)
			if !ok {
				return nil, filter, errors.Invalid(CodeInvalidInput,
					"%q filtresi mantıksal (bool) olmalı, %T verildi", name, value)
			}
			filter.IsDisabled = &flag
		default:
			return nil, filter, errors.Invalid(CodeInvalidInput,
				"%q filtresi %s sağlayıcısında desteklenmiyor (desteklenen: %v)",
				name, Entity, supportedFilters)
		}
	}

	if ids != nil && len(filters) > 1 {
		return nil, filter, errors.Invalid(CodeInvalidInput,
			"%q filtresi başka filtrelerle birlikte kullanılamaz", filterID)
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
			"%q filtresi dize ya da dize dilimi olmalı, %T verildi", name, value)
	}
}

// stringValue bir filtre değerini tek dizeye çevirir.
func stringValue(name string, value any) (string, error) {
	typed, ok := value.(string)
	if !ok {
		return "", errors.Invalid(CodeInvalidInput,
			"%q filtresi dize olmalı, %T verildi", name, value)
	}
	return typed, nil
}
