package service

import (
	"context"
	"slices"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// Entity promotion'ın Query katmanına açtığı entity adıdır.
// Sağlayıcı container'a "promotion" + query.ProviderSuffix adıyla kaydedilir.
const Entity = "promotion"

// Sağlayıcının sunduğu alan adları.
const (
	fieldID          = "id"
	fieldCode        = "code"
	fieldIsAutomatic = "is_automatic"
	fieldType        = "type"
	fieldStatus      = "status"
	fieldCampaignID  = "campaign_id"
	fieldCreatedAt   = "created_at"
	fieldUpdatedAt   = "updated_at"
)

// supportedFields sağlayıcının tanıdığı alanlardır; başka bir alan istenirse
// errors.Invalid dönülür (ADR 0004: alan doğrulaması sağlayıcıya aittir).
//
// # Listede OLMAYANLAR ve nedenleri
//
//   - Kurallar (promotion_rule): bir kuralın sağ tarafı iş bilgisidir (örn.
//     bir müşteri grubunun kimliği) ve Query'nin müşteri mi yönetim mi
//     okuduğunu ayırt etme imkânı yoktur. Kural görmek isteyen yönetim
//     yüzeyi /admin/v1/promotions/{id}/rules kullanır.
//   - Uygulama yöntemi: indirimin tutarı/oranı da aynı sınıftadır ve müşteriye
//     ancak kupon doğrulama ucundan, kupon kodu BİLİNEREK verilir.
//   - Kullanım sayacı ve kampanya bütçesi: bir kuponun kaç kez kullanıldığı
//     rekabete açık bir sayıdır ve okuma yüzeyinden sızmamalıdır.
//   - Üstveri (metadata): serbest metindir ve içine ne konduğu bilinemez.
var supportedFields = []string{
	fieldID, fieldCode, fieldIsAutomatic, fieldType,
	fieldStatus, fieldCampaignID, fieldCreatedAt, fieldUpdatedAt,
}

// QueryProvider promosyonları Query katmanına açar (ADR 0004).
//
// # Yalnızca AKTİF promosyonlar döner
//
// Hem [QueryProvider.List] hem [QueryProvider.FetchByIDs] aynı süzgeci uygular:
// taslak ve pasif promosyonlar hiç dönmez. Kural tek olmalıdır, çünkü Query
// katmanının müşteri mi yönetim mi okuduğunu ayırt etme imkânı yoktur ve
// taslak bir kuponun KODUNUN listelenebilmesi, yayınlanmamış bir kampanyayı
// ele verirdi.
//
// FetchByIDs için de aynı süzgecin geçerli olması bilinçli bir bedeldir: bir
// sipariş, sonradan pasifleştirilmiş bir promosyona bağlıysa bu yüzeyden kayıt
// GELMEZ. Doğru kaynak zaten sipariş tarafındaki anlık görüntüdür — bir
// siparişin hangi indirimi aldığı, promosyonun BUGÜNKÜ hâline değil, o günkü
// hesabına bağlıdır.
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

// List kök promosyon kayıtlarını döner.
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

	active := string(models.PromotionActive)
	promotions, _, err := p.svc.repo.ListPromotions(ctx, &active, nil, limit, offset)
	if err != nil {
		return nil, err
	}
	return records(promotions, fields), nil
}

// FetchByIDs verilen kimliklere karşılık gelen kayıtları TEK turda döner.
//
// Bulunamayan (ya da aktif olmayan) kimlik için kayıt dönmez; bu bir hata
// değildir (ADR 0004).
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

// fetch kimlik kümesini okuyup kayıtlara çevirir; aktif olmayanları eler.
func (p *QueryProvider) fetch(ctx context.Context, ids, fields []string) ([]query.Record, error) {
	if len(ids) == 0 {
		return []query.Record{}, nil
	}

	promotions, err := p.svc.repo.GetPromotionsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}

	active := make([]models.Promotion, 0, len(promotions))
	for i := range promotions {
		if promotions[i].Status == models.PromotionActive {
			active = append(active, promotions[i])
		}
	}
	return records(active, fields), nil
}

// records promosyonları Query kayıtlarına çevirir.
func records(promotions []models.Promotion, fields []string) []query.Record {
	out := make([]query.Record, 0, len(promotions))
	for i := range promotions {
		promo := &promotions[i]
		record := make(query.Record, len(fields))
		for _, field := range fields {
			switch field {
			case fieldID:
				record[fieldID] = promo.ID
			case fieldCode:
				record[fieldCode] = promo.Code
			case fieldIsAutomatic:
				record[fieldIsAutomatic] = promo.IsAutomatic
			case fieldType:
				record[fieldType] = string(promo.Type)
			case fieldStatus:
				record[fieldStatus] = string(promo.Status)
			case fieldCampaignID:
				record[fieldCampaignID] = promo.CampaignID
			case fieldCreatedAt:
				record[fieldCreatedAt] = promo.CreatedAt
			case fieldUpdatedAt:
				record[fieldUpdatedAt] = promo.UpdatedAt
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
