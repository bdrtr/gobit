// Package api fulfillment modülünün HTTP yüzeyidir.
//
// İki yüzey vardır ve yetkileri farklıdır:
//
//   - /admin/v1 — kargo kataloğunu ve gönderileri YÖNETİR: profil, seçenek ve
//     kural CRUD'u, gönderi oluşturma, iptal, kargoya verme ve teslim
//     bildirimi.
//   - /store/v1 — müşterinin gördüğü TEK yüzey: bir sepet bağlamı için uygun
//     kargo seçenekleri ve ücretleri. Gönderi oluşturmak, iptal etmek ya da
//     durumunu değiştirmek mağaza tarafından TETİKLENMEZ; onları sipariş
//     akışları ve yönetim yürütür. Müşterinin kendi tarayıcısından gönderi
//     açabilmesi, siparişi hiç oluşmamış bir sepet için kargo etiketi
//     bastırmak demek olurdu.
//
// # Mağaza yüzeyine NE SIZMAZ
//
// Vitrin yanıtında yalnızca müşterinin görmesi gereken alanlar vardır:
// kimlik ad, ücret, para birimi ve fiyat türü. Dışarıda bırakılan üç şey
// bilinçlidir:
//
//   - admin_only seçenekler HİÇ listelenmez; süzgeç SQL'de durur ve satır
//     mağaza yolunda hiç okunmaz (bkz. service.ListShippingOptionsFor).
//   - provider_id ve sağlayıcının ham verisi ("data") YAZILMAZ: hangi kargo
//     firmasıyla çalışıldığı ve o firmanın iç yanıtı, mağazanın operasyonel
//     bilgisidir.
//   - shipping_profile_id ve metadata YAZILMAZ: ikisi de kataloğun iç
//     yapısıdır ve müşterinin bir seçim yapması için gerekmez.
//
// Handler'lar status kodu SEÇMEZ: servis core/errors tipli hatasını döner,
// corehttp.WriteError sınıfına uygun kodu yazar (plan Bölüm 8).
//
// # Yetki
//
// Yönetim uçlarının tamamı yetki ister ve sözlük iki girdiden ibarettir:
//
//   - [ScopeRead] — /admin/v1 altındaki OKUMA (GET, HEAD) uçlarını açar:
//     sağlayıcı listesi, profiller, seçenekler, kurallar, uygunluk listelemesi
//     ve gönderiler okunabilir.
//   - [ScopeWrite] — /admin/v1 altındaki YAZMA (POST, PATCH, DELETE) uçlarını
//     açar: katalog CRUD'unun yanı sıra gönderi açma, iptal, kargoya verme ve
//     teslim bildirimi de buraya girer.
//
// corehttp.ScopeAdmin ÜST YETKİDİR ve ikisini de karşılar; ayrıca
// listelenmesine gerek yoktur, corehttp.Principal.HasScope bunu zaten yapar.
//
// /store/v1 uygun seçenek ucu yetki İSTEMEZ: mağaza yüzeyinin kimliği
// publishable anahtardır ve o anahtar tanımı gereği yetki TAŞIMAZ.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// Route yolları. Modül route'ları TAM YOL ile kaydedilir; "/admin/v1" gibi bir
// ön ek MOUNT EDİLMEZ, çünkü mount eden ilk modül o alt ağacın tamamını
// sahiplenir ve aynı ön eki kullanan diğer modüllerle çakışırdı.
const (
	pathAdminProviders = "/admin/v1/fulfillment-providers"

	pathAdminProfiles = "/admin/v1/shipping-profiles"
	pathAdminProfile  = "/admin/v1/shipping-profiles/{id}"

	pathAdminOptions = "/admin/v1/shipping-options"
	// pathAdminEligible yönetim yüzeyinin uygunluk listelemesidir ve
	// admin_only seçenekleri DE içerir. Statik parça, "{id}" yolundan önce
	// eşleşir; chi sabit segmenti parametreye tercih eder.
	pathAdminEligible    = "/admin/v1/shipping-options/eligible"
	pathAdminOption      = "/admin/v1/shipping-options/{id}"
	pathAdminOptionRules = "/admin/v1/shipping-options/{id}/rules"
	pathAdminOptionRule  = "/admin/v1/shipping-options/{id}/rules/{rule_id}"

	pathAdminFulfillments = "/admin/v1/fulfillments"
	pathAdminFulfillment  = "/admin/v1/fulfillments/{id}"
	pathAdminCancel       = "/admin/v1/fulfillments/{id}/cancel"
	pathAdminShip         = "/admin/v1/fulfillments/{id}/ship"
	pathAdminDeliver      = "/admin/v1/fulfillments/{id}/deliver"

	pathStoreOptions = "/store/v1/shipping-options"
)

// maxBodyBytes istek gövdesi için üst sınırdır. Sınır olmadan tek bir istek
// sunucunun belleğini tüketebilirdi.
const maxBodyBytes int64 = 1 << 20 // 1 MiB

// codeInvalidRequest gövde/parametre çözümlenemediğinde dönen hata kodudur.
const codeInvalidRequest = "fulfillment_invalid_request"

// Fulfillments handler'ların servisten ihtiyaç duyduğu yüzeydir.
//
// Dar tutulması testleri sadeleştirir: HTTP davranışı, gerçek bir veritabanı
// olmadan birkaç satırlık bir sahte ile doğrulanabilir.
type Fulfillments interface {
	// ProviderIDs kayıtlı sağlayıcı kimliklerini döner.
	ProviderIDs(ctx context.Context) []string

	// CreateShippingProfile yeni bir kargo profili oluşturur.
	CreateShippingProfile(ctx context.Context, in service.CreateProfileInput) (models.ShippingProfile, error)
	// GetShippingProfile profili kimliğiyle döner.
	GetShippingProfile(ctx context.Context, id string) (models.ShippingProfile, error)
	// ListShippingProfiles profilleri sayfalar.
	ListShippingProfiles(ctx context.Context, in service.ListProfilesInput) ([]models.ShippingProfile, int64, error)
	// UpdateShippingProfile profilin verilen alanlarını günceller.
	UpdateShippingProfile(ctx context.Context, id string, in service.UpdateProfileInput) (models.ShippingProfile, error)
	// DeleteShippingProfile profili yumuşak siler.
	DeleteShippingProfile(ctx context.Context, id string) error

	// CreateShippingOption yeni bir kargo seçeneği oluşturur.
	CreateShippingOption(ctx context.Context, in service.CreateOptionInput) (models.ShippingOption, error)
	// GetShippingOption seçeneği kurallarıyla döner.
	GetShippingOption(ctx context.Context, id string) (models.ShippingOption, error)
	// ListShippingOptions seçenekleri sayfalar.
	ListShippingOptions(ctx context.Context, in service.ListOptionsAdminInput) ([]models.ShippingOption, int64, error)
	// UpdateShippingOption seçeneğin verilen alanlarını günceller.
	UpdateShippingOption(ctx context.Context, id string, in service.UpdateOptionInput) (models.ShippingOption, error)
	// DeleteShippingOption seçeneği yumuşak siler.
	DeleteShippingOption(ctx context.Context, id string) error

	// CreateShippingOptionRule bir seçeneğe kural ekler.
	CreateShippingOptionRule(ctx context.Context, optionID string, in service.CreateRuleInput) (models.ShippingOptionRule, error)
	// ListShippingOptionRules bir seçeneğin kurallarını döner.
	ListShippingOptionRules(ctx context.Context, optionID string) ([]models.ShippingOptionRule, error)
	// DeleteShippingOptionRule kuralı yumuşak siler.
	DeleteShippingOptionRule(ctx context.Context, ruleID string) error

	// ListShippingOptionsFor bir sepet bağlamı için uygun seçenekleri döner.
	ListShippingOptionsFor(ctx context.Context, in service.ListOptionsInput) ([]service.QuotedOption, error)

	// CreateFulfillment sağlayıcıda bir gönderi açar.
	CreateFulfillment(ctx context.Context, in service.CreateFulfillmentInput) (models.Fulfillment, error)
	// GetFulfillment gönderiyi kalemleriyle döner.
	GetFulfillment(ctx context.Context, id string) (models.Fulfillment, error)
	// ListFulfillments gönderileri sayfalar.
	ListFulfillments(ctx context.Context, in service.ListFulfillmentsInput) ([]models.Fulfillment, int64, error)
	// CancelFulfillment gönderiyi iptal eder (saga telafisi).
	CancelFulfillment(ctx context.Context, id string) error
	// MarkShipped gönderiyi kargoya verilmiş olarak işaretler.
	MarkShipped(ctx context.Context, id, trackingNumber, trackingURL string) (models.Fulfillment, error)
	// MarkDelivered gönderiyi teslim edilmiş olarak işaretler.
	MarkDelivered(ctx context.Context, id string) (models.Fulfillment, error)
}

// Handler fulfillment modülünün HTTP handler kümesidir.
type Handler struct {
	svc Fulfillments
}

// New verilen servis üzerinde çalışan handler kümesini üretir.
func New(svc Fulfillments) *Handler { return &Handler{svc: svc} }

// Yetki sözlüğü: fulfillment'ın yönetim uçlarının istediği yetkiler.
//
// Sözlük BİLİNÇLİ OLARAK okuma/yazma ayrımından ibarettir. "Kataloğu yazan"
// ile "gönderiyi yürüten" yetkileri ayırmak akla yatkın görünür ama bugün
// verilebilecek bir kararı mümkün kılmaz: gönderiyi yürüten kimlik, gönderinin
// açılacağı seçeneği de belirleyebilmelidir. Ayrım gerçekten gerektiğinde
// eklenir; şimdiden eklenirse yalnızca yanlış bir kesinlik hissi verir.
const (
	// ScopeRead fulfillment yönetim yüzeyindeki OKUMA uçlarının istediği
	// yetkidir.
	ScopeRead = "fulfillment:read"
	// ScopeWrite fulfillment yönetim yüzeyindeki YAZMA uçlarının istediği
	// yetkidir.
	ScopeWrite = "fulfillment:write"
)

// Routes modülün admin ve store route'larını router'a bağlar.
//
// # KORUMA
//
// İki katman vardır ve ikisi de gereklidir:
//
//  1. KİMLİK — corehttp.RequireAdmin ile, router'ı kuran tarafta.
//  2. YETKİ — BURADA, uç uç corehttp.RequireScope ile: okuma uçları
//     [ScopeRead], yazma uçları [ScopeWrite] ister.
//
// İkinci katman olmasaydı kimlik doğrulama yetkilendirmenin yerine geçerdi ve
// yetkileri BOŞALTILMIŞ bir yönetim kullanıcısı gönderi açıp kargo etiketi
// bastırabilir, açılmış bir gönderiyi iptal edebilir ya da hiç gönderilmemiş
// bir siparişi "teslim edildi" diye kapatabilirdi. Bunların üçü de dışarıya —
// kargo firmasına ve müşteriye — yansıyan, geri alınması para maliyetli
// işlemlerdir.
func (h *Handler) Routes(r chi.Router) {
	okuma := r.With(corehttp.RequireScope(ScopeRead))
	yazma := r.With(corehttp.RequireScope(ScopeWrite))

	okuma.Get(pathAdminProviders, h.listProviders)

	yazma.Post(pathAdminProfiles, h.createProfile)
	okuma.Get(pathAdminProfiles, h.listProfiles)
	okuma.Get(pathAdminProfile, h.getProfile)
	yazma.Patch(pathAdminProfile, h.updateProfile)
	yazma.Delete(pathAdminProfile, h.deleteProfile)

	yazma.Post(pathAdminOptions, h.createOption)
	okuma.Get(pathAdminOptions, h.listOptions)
	// Uygunluk listelemesi seçenek okumasından ÖNCE bağlanır; okunurluk
	// içindir, chi sıradan bağımsız olarak sabit segmenti tercih eder.
	okuma.Get(pathAdminEligible, h.listAdminEligibleOptions)
	okuma.Get(pathAdminOption, h.getOption)
	yazma.Patch(pathAdminOption, h.updateOption)
	yazma.Delete(pathAdminOption, h.deleteOption)

	yazma.Post(pathAdminOptionRules, h.createRule)
	okuma.Get(pathAdminOptionRules, h.listRules)
	yazma.Delete(pathAdminOptionRule, h.deleteRule)

	yazma.Post(pathAdminFulfillments, h.createFulfillment)
	okuma.Get(pathAdminFulfillments, h.listFulfillments)
	okuma.Get(pathAdminFulfillment, h.getFulfillment)
	yazma.Post(pathAdminCancel, h.cancelFulfillment)
	yazma.Post(pathAdminShip, h.shipFulfillment)
	yazma.Post(pathAdminDeliver, h.deliverFulfillment)

	// Mağaza ucu DEĞİŞMEZ: publishable anahtar yetki taşımaz.
	r.Get(pathStoreOptions, h.listStoreEligibleOptions)
}

// --- zarflar ve DTO'lar ------------------------------------------------------

// singleEnvelope tekil yanıtların zarfıdır (plan Bölüm 8).
type singleEnvelope struct {
	// Data yanıtın gövdesidir.
	Data any `json:"data"`
}

// listEnvelope liste yanıtlarının zarfıdır (plan Bölüm 8).
type listEnvelope struct {
	// Data sayfadaki kayıtlardır.
	Data any `json:"data"`
	// Count süzgece uyan TÜM kayıtların sayısıdır; sayfadaki satır sayısı değil.
	Count int64 `json:"count"`
	// Offset atlanan kayıt sayısıdır.
	Offset int64 `json:"offset"`
	// Limit istenen sayfa boyutudur.
	Limit int64 `json:"limit"`
}

// profileDTO kargo profilinin dış gösterimidir.
type profileDTO struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Type      string         `json:"type"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// optionDTO kargo seçeneğinin YÖNETİM gösterimidir.
//
// Sağlayıcı yapılandırması ("data") burada görünür: yöneticinin seçeneği
// düzenleyebilmesi için gereklidir. Mağaza gösterimi ayrıdır
// ([storeOptionDTO]) ve bu alanı taşımaz.
type optionDTO struct {
	ID                string         `json:"id"`
	Name              string         `json:"name"`
	ProviderID        string         `json:"provider_id"`
	ShippingProfileID string         `json:"shipping_profile_id"`
	PriceType         string         `json:"price_type"`
	Amount            int64          `json:"amount"`
	CurrencyCode      string         `json:"currency_code"`
	RegionID          string         `json:"region_id"`
	IsReturn          bool           `json:"is_return"`
	AdminOnly         bool           `json:"admin_only"`
	Data              map[string]any `json:"data,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
	Rules             []ruleDTO      `json:"rules,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

// ruleDTO kargo seçeneği kuralının dış gösterimidir.
type ruleDTO struct {
	ID               string    `json:"id"`
	ShippingOptionID string    `json:"shipping_option_id"`
	Attribute        string    `json:"attribute"`
	Operator         string    `json:"operator"`
	Values           []string  `json:"values"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// quotedOptionDTO YÖNETİM yüzeyinin fiyatlanmış seçenek gösterimidir.
type quotedOptionDTO struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Amount            int64  `json:"amount"`
	CurrencyCode      string `json:"currency_code"`
	PriceType         string `json:"price_type"`
	ProviderID        string `json:"provider_id"`
	ShippingProfileID string `json:"shipping_profile_id"`
	IsReturn          bool   `json:"is_return"`
	AdminOnly         bool   `json:"admin_only"`
}

// storeOptionDTO MAĞAZA yüzeyinin fiyatlanmış seçenek gösterimidir.
//
// Alan listesi bilinçli olarak KISADIR; neyin ve neden dışarıda kaldığı paket
// belgesinde yazılıdır. Yapının [quotedOptionDTO]'dan ayrı olması, bir alanın
// yönetim gösterimine eklenirken kazara vitrine sızmasını yapısal olarak
// engeller.
type storeOptionDTO struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Amount       int64  `json:"amount"`
	CurrencyCode string `json:"currency_code"`
	PriceType    string `json:"price_type"`
}

// fulfillmentDTO gönderinin dış gösterimidir.
type fulfillmentDTO struct {
	ID               string               `json:"id"`
	Reference        string               `json:"reference"`
	ShippingOptionID string               `json:"shipping_option_id"`
	ProviderID       string               `json:"provider_id"`
	ExternalID       string               `json:"external_id"`
	Status           string               `json:"status"`
	TrackingNumber   string               `json:"tracking_number,omitempty"`
	TrackingURL      string               `json:"tracking_url,omitempty"`
	ShippedAt        *time.Time           `json:"shipped_at,omitempty"`
	DeliveredAt      *time.Time           `json:"delivered_at,omitempty"`
	CanceledAt       *time.Time           `json:"canceled_at,omitempty"`
	Data             json.RawMessage      `json:"data,omitempty"`
	Metadata         map[string]any       `json:"metadata,omitempty"`
	Items            []fulfillmentItemDTO `json:"items"`
	CreatedAt        time.Time            `json:"created_at"`
	UpdatedAt        time.Time            `json:"updated_at"`
}

// fulfillmentItemDTO gönderi kaleminin dış gösterimidir.
type fulfillmentItemDTO struct {
	ID         string `json:"id"`
	LineItemID string `json:"line_item_id"`
	Quantity   int64  `json:"quantity"`
}

// toProfileDTO modeli dış gösterime çevirir.
func toProfileDTO(profile models.ShippingProfile) profileDTO {
	return profileDTO{
		ID:        profile.ID,
		Name:      profile.Name,
		Type:      profile.Type.String(),
		Metadata:  profile.Metadata,
		CreatedAt: profile.CreatedAt,
		UpdatedAt: profile.UpdatedAt,
	}
}

// toOptionDTO modeli yönetim gösterimine çevirir.
func toOptionDTO(option models.ShippingOption) optionDTO {
	rules := make([]ruleDTO, 0, len(option.Rules))
	for i := range option.Rules {
		rules = append(rules, toRuleDTO(option.Rules[i]))
	}
	if len(rules) == 0 {
		// omitempty'nin çalışması için nil bırakılır: kuralsız bir seçenekte
		// "rules": [] yazmak, kuralların hiç okunmadığı liste yanıtıyla
		// karışırdı.
		rules = nil
	}

	return optionDTO{
		ID:                option.ID,
		Name:              option.Name,
		ProviderID:        option.ProviderID,
		ShippingProfileID: option.ShippingProfileID,
		PriceType:         option.PriceType.String(),
		Amount:            option.Amount,
		CurrencyCode:      option.CurrencyCode,
		RegionID:          option.RegionID,
		IsReturn:          option.IsReturn,
		AdminOnly:         option.AdminOnly,
		Data:              option.Data,
		Metadata:          option.Metadata,
		Rules:             rules,
		CreatedAt:         option.CreatedAt,
		UpdatedAt:         option.UpdatedAt,
	}
}

// toRuleDTO modeli dış gösterime çevirir.
func toRuleDTO(rule models.ShippingOptionRule) ruleDTO {
	return ruleDTO{
		ID:               rule.ID,
		ShippingOptionID: rule.ShippingOptionID,
		Attribute:        rule.Attribute,
		Operator:         rule.Operator.String(),
		Values:           rule.Values,
		CreatedAt:        rule.CreatedAt,
		UpdatedAt:        rule.UpdatedAt,
	}
}

// toQuotedOptionDTO fiyatlanmış seçeneği yönetim gösterimine çevirir.
func toQuotedOptionDTO(quoted service.QuotedOption) quotedOptionDTO {
	return quotedOptionDTO{
		ID:                quoted.Option.ID,
		Name:              quoted.Option.Name,
		Amount:            quoted.Amount,
		CurrencyCode:      quoted.CurrencyCode,
		PriceType:         quoted.Option.PriceType.String(),
		ProviderID:        quoted.Option.ProviderID,
		ShippingProfileID: quoted.Option.ShippingProfileID,
		IsReturn:          quoted.Option.IsReturn,
		AdminOnly:         quoted.Option.AdminOnly,
	}
}

// toStoreOptionDTO fiyatlanmış seçeneği MAĞAZA gösterimine çevirir.
func toStoreOptionDTO(quoted service.QuotedOption) storeOptionDTO {
	return storeOptionDTO{
		ID:           quoted.Option.ID,
		Name:         quoted.Option.Name,
		Amount:       quoted.Amount,
		CurrencyCode: quoted.CurrencyCode,
		PriceType:    quoted.Option.PriceType.String(),
	}
}

// toFulfillmentDTO modeli dış gösterime çevirir.
func toFulfillmentDTO(ful models.Fulfillment) fulfillmentDTO {
	items := make([]fulfillmentItemDTO, 0, len(ful.Items))
	for i := range ful.Items {
		items = append(items, fulfillmentItemDTO{
			ID:         ful.Items[i].ID,
			LineItemID: ful.Items[i].LineItemID,
			Quantity:   ful.Items[i].Quantity,
		})
	}

	return fulfillmentDTO{
		ID:               ful.ID,
		Reference:        ful.Reference,
		ShippingOptionID: ful.ShippingOptionID,
		ProviderID:       ful.ProviderID,
		ExternalID:       ful.ExternalID,
		Status:           ful.Status.String(),
		TrackingNumber:   ful.TrackingNumber,
		TrackingURL:      ful.TrackingURL,
		ShippedAt:        ful.ShippedAt,
		DeliveredAt:      ful.DeliveredAt,
		CanceledAt:       ful.CanceledAt,
		Data:             ful.Data,
		Metadata:         ful.Metadata,
		Items:            items,
		CreatedAt:        ful.CreatedAt,
		UpdatedAt:        ful.UpdatedAt,
	}
}

// --- yardımcılar -------------------------------------------------------------

// decodeBody istek gövdesini çözer.
//
// Gövde boyutu sınırlanır ve TANINMAYAN ALANLAR reddedilir: sessizce yutulan
// bir alan, istemcinin gönderdiğini sandığı ama uygulanmayan bir ayar demektir.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return coreerrors.Invalid(codeInvalidRequest, "istek gövdesi boş olamaz")
		}
		return coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"istek gövdesi çözümlenemedi")
	}
	// Tek bir JSON değerinden fazlası gönderilmişse bu da bir istemci hatasıdır.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return coreerrors.Invalid(codeInvalidRequest,
			"istek gövdesi tek bir JSON nesnesi olmalı")
	}
	return nil
}

// decodeOptionalBody gövdeyi çözer ama BOŞ gövdeyi hata saymaz.
//
// Gövdesi isteğe bağlı olan uçlar içindir (örn. takip bilgisiz sevk bildirimi).
// Boşluk denetimi Content-Length'e BAKMAZ: chunked kodlamayla gelen bir
// istekte uzunluk -1'dir ve uzunluğa bakan bir kontrol, gerçekte GÖNDERİLMİŞ
// bir gövdeyi sessizce yok sayardı — istemci gönderdiğini sandığı takip
// numarasının hiç yazılmadığını ancak kargo ekranında görürdü. Boşluk, ilk
// çözümlemenin io.EOF ile dönmesinden anlaşılır.
func decodeOptionalBody(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"istek gövdesi çözümlenemedi")
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return coreerrors.Invalid(codeInvalidRequest,
			"istek gövdesi tek bir JSON nesnesi olmalı")
	}
	return nil
}

// parsePage limit/offset sorgu parametrelerini çözer.
func parsePage(r *http.Request) (service.Page, error) {
	limit, err := parseInt64Param(r, "limit")
	if err != nil {
		return service.Page{}, err
	}
	offset, err := parseInt64Param(r, "offset")
	if err != nil {
		return service.Page{}, err
	}
	page := service.Page{Limit: limit, Offset: offset}
	if page.Limit == 0 {
		// Yanıttaki limit alanının gerçekten uygulanan sınırı göstermesi için
		// varsayılan burada da görünür kılınır.
		page.Limit = service.DefaultLimit
	}
	return page, nil
}

// parseInt64Param bir sorgu parametresini tam sayıya çevirir; yoksa 0 döner.
func parseInt64Param(r *http.Request, name string) (int64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"%s tam sayı olmalı: %q", name, raw)
	}
	return value, nil
}

// parseBoolParam bir sorgu parametresini boolean'a çevirir; yoksa false döner.
func parseBoolParam(r *http.Request, name string) (bool, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return false, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"%s mantıksal değer olmalı: %q", name, raw)
	}
	return value, nil
}

// writeList bir dilimi liste zarfıyla yazar.
//
// Sayfalanmayan uçlarda (bir seçeneğin kuralları gibi) count satır sayısıdır
// ve limit ile aynıdır: zarf her yerde aynı şekle sahiptir, istemci iki farklı
// yanıt biçimi öğrenmek zorunda kalmaz.
func writeList[T any](ctx context.Context, w http.ResponseWriter, items []T) {
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:   items,
		Count:  int64(len(items)),
		Offset: 0,
		Limit:  int64(len(items)),
	})
}
