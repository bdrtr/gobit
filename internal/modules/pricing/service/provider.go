package service

import (
	"context"
	"slices"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// Entity pricing'in Query katmanına açtığı entity adıdır.
// Sağlayıcı container'a "price_set" + query.ProviderSuffix adıyla kaydedilir.
const Entity = "price_set"

// Sağlayıcının sunduğu alan adları.
const (
	fieldID        = "id"
	fieldCreatedAt = "created_at"
	fieldUpdatedAt = "updated_at"
	fieldPrices    = "prices"
)

// Fiyat alt kayıtlarının alan adları.
const (
	fieldCurrencyCode = "currency_code"
	fieldAmount       = "amount"
	fieldMinQuantity  = "min_quantity"
	fieldMaxQuantity  = "max_quantity"
	fieldPriceListID  = "price_list_id"
)

// supportedFields sağlayıcının tanıdığı alanlardır; başka bir alan istenirse
// errors.Invalid dönülür (ADR 0004: alan doğrulaması sağlayıcıya aittir).
var supportedFields = []string{fieldID, fieldCreatedAt, fieldUpdatedAt, fieldPrices}

// QueryProvider price set'leri Query katmanına açar (ADR 0004).
//
// Kayıtlar fiyatlarıyla BİRLİKTE döner. Bu bilinçlidir: sağlayıcının tek
// tüketicisi product'ın store listelemesidir ve fiyatsız bir price set kaydı
// orada hiçbir işe yaramaz — ikinci bir tur gerekirdi ki Query'nin N+1 yasağı
// tam da bunu engellemek içindir. Fiyatlar istenmiyorsa Fields ile
// dışarıda bırakılabilir.
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

// List kök price set kayıtlarını döner.
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

	sets, _, err := p.svc.repo.ListPriceSets(ctx, limit, offset)
	if err != nil {
		return nil, err
	}
	return p.records(ctx, sets, fields)
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

	sets, err := p.svc.repo.GetPriceSetsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return p.records(ctx, sets, fields)
}

// records price set'leri Query kayıtlarına çevirir; gerekiyorsa fiyatları TEK
// sorguyla toplu getirir.
func (p *QueryProvider) records(
	ctx context.Context,
	sets []models.PriceSet,
	fields []string,
) ([]query.Record, error) {
	records := make([]query.Record, 0, len(sets))
	if len(sets) == 0 {
		return records, nil
	}

	var pricesBySet map[string][]models.Price
	if slices.Contains(fields, fieldPrices) {
		setIDs := make([]string, 0, len(sets))
		for _, set := range sets {
			setIDs = append(setIDs, set.ID)
		}

		var err error
		pricesBySet, err = p.svc.repo.ListPricesBySets(ctx, setIDs)
		if err != nil {
			return nil, err
		}
	}

	for _, set := range sets {
		record := make(query.Record, len(fields))
		for _, field := range fields {
			switch field {
			case fieldID:
				record[fieldID] = set.ID
			case fieldCreatedAt:
				record[fieldCreatedAt] = set.CreatedAt
			case fieldUpdatedAt:
				record[fieldUpdatedAt] = set.UpdatedAt
			case fieldPrices:
				record[fieldPrices] = priceRecords(pricesBySet[set.ID])
			}
		}
		records = append(records, record)
	}
	return records, nil
}

// priceRecords fiyatları alt kayıtlara çevirir.
//
// Kimliği olmayan bir kap için boş (nil olmayan) dilim döner; JSON'da null
// yerine [] görünmesi tüketici için tek biçimli bir yüzeydir.
func priceRecords(prices []models.Price) []map[string]any {
	out := make([]map[string]any, 0, len(prices))
	for i := range prices {
		price := &prices[i]
		record := map[string]any{
			fieldID:           price.ID,
			fieldCurrencyCode: price.CurrencyCode,
			fieldAmount:       price.Amount,
			fieldMinQuantity:  price.MinQuantity,
			fieldMaxQuantity:  price.MaxQuantity,
			fieldPriceListID:  price.PriceListID,
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
				"%q filtresi %s sağlayıcısında desteklenmiyor (desteklenen: %q)", name, Entity, fieldID)
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
				"%q filtresi dize ya da dize dilimi olmalı, %T verildi", fieldID, value)
		}
	}
	return ids, nil
}
