// Package api pricing modülünün HTTP yüzeyidir.
//
// İki ad alanı vardır (plan Bölüm 8): /admin/v1 yönetim, /store/v1 müşteri.
// Fiyat yazma yüzeyi YALNIZCA admin tarafındadır; store tarafında tek bir
// okuma uç noktası bulunur, çünkü müşteriye giden fiyat normalde product'ın
// store listelemesinden, Query katmanı üzerinden gelir (ADR 0004).
//
// Handler'lar status kodu SEÇMEZ: servis tipli hata döner, corehttp.WriteError
// onu status koduna çevirir (plan Bölüm 2.7). Bu, hata sınıflandırmasının tek
// bir yerde kalmasını sağlar.
//
// # Yetki
//
// /admin/v1 uçları yetki ister ve sözlük ikiye ayrılır: GET uçları [ScopeRead],
// POST/PUT/PATCH/DELETE uçları [ScopeWrite] (bkz. [API.Routes]).
// corehttp.ScopeAdmin ÜST YETKİDİR ve ikisini de tek başına karşılar.
//
// Sözlükte İSTİSNA YOKTUR ve bu bilinçlidir: yetkinin metottan okunabilmesi,
// bir ucun neyi açtığını handler'a bakmadan söyleyebilmek demektir. Yan
// etkisiz bir hesabı yazma yetkisine bağlamamanın doğru yolu sözlüğe istisna
// açmak değil, ucu okuma metoduna TAŞIMAKTIR; fiyat hesaplama ucu bunun
// örneğidir (GET /admin/v1/price-sets/{id}/calculate, bkz.
// [API.calculatePrice]).
//
// /store/v1 ucuna yetki EKLENMEZ: mağaza yüzeyinin kimliği publishable
// anahtardır ve o anahtar tanımı gereği yetki TAŞIMAZ.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
	"github.com/bdrtr/gobit/internal/modules/pricing/service"
)

// maxBodyBytes tek bir istek gövdesinin azami boyutudur.
//
// Toplu fiyat yazma (SetPrices) tek istekte binlerce satır taşıyabildiği için
// sınır cömerttir; ama sınırsız değildir — sınırsız bir gövde, tek istekle
// belleği tüketmenin en ucuz yoludur.
const maxBodyBytes int64 = 1 << 20 // 1 MiB

// codeInvalidBody istek gövdesi çözümlenemediğinde dönen hata kodudur.
const codeInvalidBody = "pricing_invalid_body"

// Yetki sözlüğü: pricing'in yönetim uçlarının istediği yetkiler.
//
// Sözlük tüm modüllerde AYNI biçimdedir ve BİLİNÇLİ olarak iki girdiden
// ibarettir: okuma ve yazma. Kaynak başına ayrı yetki ("price-lists:write",
// "price-rules:read" …) tanımlamak listeyi büyütür ama bugün verilebilecek
// hiçbir yeni kararı mümkün kılmaz; ayrım gerçekten gerektiğinde eklenir.
const (
	// ScopeRead pricing yönetim yüzeyindeki OKUMA uçlarının istediği yetkidir.
	//
	// Fiyat setlerini, fiyat listelerini ve fiyat kurallarını okumaya yeter;
	// hiçbir yazma ucunu açmaz. Tam yetkili kimliklere ayrıca verilmesi
	// gerekmez: corehttp.ScopeAdmin taşıyan bir çağıran bunu da karşılar
	// (bkz. corehttp.Principal.HasScope).
	ScopeRead = "pricing:read"

	// ScopeWrite pricing yönetim yüzeyindeki YAZMA uçlarının istediği
	// yetkidir.
	//
	// Fiyat yazabilen bir kimlik, tek istekle bütün kataloğu bir kuruşa
	// indirebilir; bu yüzden fiyatı yalnızca RAPORLAYAN entegrasyonların
	// [ScopeRead] ile yetinebilmesi önemlidir.
	ScopeWrite = "pricing:write"
)

// API pricing'in HTTP handler'larını barındırır.
type API struct {
	svc *service.Service
}

// New verilen servis üzerinde çalışan bir API üretir.
func New(svc *service.Service) *API {
	return &API{svc: svc}
}

// Routes pricing'in admin ve store route'larını router'a bağlar.
//
// Route'lar chi'nin Route/Mount yardımcılarıyla DEĞİL, tam yollarla kaydedilir:
// /admin/v1 önekini birden çok modül paylaşır ve aynı öneki iki kez Mount etmek
// chi'de panik üretirdi. Tam yol kaydı aynı ağaca yan yana yazar.
//
// # KORUMA
//
// İki katman vardır ve ikisi de gereklidir:
//
//  1. KİMLİK — /admin/v1 uçları corehttp.RequireAdmin ile korunur. O
//     middleware bu modülde değil, router'ı kuran tarafta takılır (bkz.
//     corehttp.APIGuards).
//  2. YETKİ — uçlar BURADA, uç uç corehttp.RequireScope ile işaretlenir:
//     GET uçları [ScopeRead], POST/PUT/DELETE uçları [ScopeWrite] ister.
//
// İkinci katman olmasaydı kimlik doğrulama yetkilendirmenin yerine geçerdi:
// yetkileri boşaltılmış bir yönetim kullanıcısı giriş yapıp
// POST /admin/v1/price-sets/{id}/prices ile bütün fiyatları değiştirebilirdi.
//
// Mağaza ucuna yetki EKLENMEZ: /store/v1'in kimliği publishable anahtardır ve
// o anahtar tanımı gereği yetki TAŞIMAZ.
func (a *API) Routes(r chi.Router) {
	okuma := r.With(corehttp.RequireScope(ScopeRead))
	yazma := r.With(corehttp.RequireScope(ScopeWrite))

	yazma.Post("/admin/v1/price-sets", a.createPriceSet)
	okuma.Get("/admin/v1/price-sets", a.listPriceSets)
	okuma.Get("/admin/v1/price-sets/{id}", a.getPriceSet)
	yazma.Delete("/admin/v1/price-sets/{id}", a.deletePriceSet)
	okuma.Get("/admin/v1/price-sets/{id}/prices", a.listPrices)
	yazma.Post("/admin/v1/price-sets/{id}/prices", a.setPrices)

	// Hesaplama ucu hiçbir şey YAZMAZ ve bu yüzden bir GET'tir; bağlamını
	// sorgu dizesinden alır (bkz. [API.calculatePrice]). Eskiden POST'tu ve
	// sözlük metoda baktığı için [ScopeWrite] istiyordu: fiyatı yalnızca
	// RAPORLAYAN bir entegrasyon, hesap yaptırabilmek için bütün kataloğu tek
	// istekte değiştirebilen bir kimlikle çalışmak zorundaydı. Çözüm sözlüğe
	// "bu POST aslında okuma" istisnası açmak DEĞİLDİ: istisna bir kez
	// açıldığında bir ucun neyi açtığını anlamak için handler'ı okumak
	// gerekirdi. Uç metoduyla niyetini söyleyecek biçimde taşındı.
	okuma.Get("/admin/v1/price-sets/{id}/calculate", a.calculatePrice)

	yazma.Post("/admin/v1/price-lists", a.createPriceList)
	okuma.Get("/admin/v1/price-lists", a.listPriceLists)
	okuma.Get("/admin/v1/price-lists/{id}", a.getPriceList)
	yazma.Put("/admin/v1/price-lists/{id}", a.updatePriceList)
	yazma.Delete("/admin/v1/price-lists/{id}", a.deletePriceList)

	okuma.Get("/admin/v1/prices/{price_id}/rules", a.listPriceRules)
	yazma.Post("/admin/v1/prices/{price_id}/rules", a.createPriceRule)
	yazma.Delete("/admin/v1/price-rules/{id}", a.deletePriceRule)

	r.Get("/store/v1/price-sets/{id}", a.storeGetPriceSet)
}

// itemEnvelope tekil yanıtların zarfıdır (plan Bölüm 8).
type itemEnvelope struct {
	// Data tek kaydın gövdesidir.
	Data any `json:"data"`
}

// listEnvelope liste yanıtlarının zarfıdır (plan Bölüm 8).
type listEnvelope struct {
	// Data geçerli sayfadaki kayıtlardır.
	Data any `json:"data"`
	// Count filtreye uyan TOPLAM kayıt sayısıdır.
	Count int64 `json:"count"`
	// Offset uygulanan atlama sayısıdır.
	Offset int32 `json:"offset"`
	// Limit uygulanan sayfa boyudur.
	Limit int32 `json:"limit"`
}

// writeItem tekil yanıtı zarfıyla yazar.
func writeItem(w http.ResponseWriter, r *http.Request, status int, data any) {
	corehttp.WriteJSON(r.Context(), w, status, itemEnvelope{Data: data})
}

// writeItems sayfalanmamış bir listeyi zarfıyla yazar.
//
// Sayfalanmayan uç noktalarda (bir kabın fiyatları, bir fiyatın kuralları)
// zarfın sayısal alanları kayıt sayısıyla doldurulur: istemcinin zarf şekli
// uç noktaya göre değişmez.
//
// Limit, dönen kayıt sayısına EŞİTTİR ve [service.MaxLimit] ile KIRPILMAZ.
// Kırpılsaydı 250 fiyatlı bir kap için yanıt "count=250, limit=100" derdi;
// istemci sayfa boyunu 100 sanıp sayfalama döngüsüne girer ve aynı kayıtları
// tekrar okurdu. Burada sayfa yoktur — tek sayfa tüm kayıtlardır.
func writeItems[T any](w http.ResponseWriter, r *http.Request, items []T) {
	if items == nil {
		items = []T{}
	}
	count := int64(len(items))
	corehttp.WriteJSON(r.Context(), w, http.StatusOK, listEnvelope{
		Data:   items,
		Count:  count,
		Offset: 0,
		Limit:  clampCount(count),
	})
}

// clampCount kayıt sayısını zarfın int32 limit alanına sığdırır.
//
// Yalnızca int32 ARALIĞINA sığdırır; sayfa boyu sınırı uygulamaz (bkz.
// writeItems). Alt sınır da denetlenir: count bir len() sonucudur ve negatif
// olamaz, ama denetimin varlığı int32'ye dönüşün her girdide güvenli olduğunu
// YEREL olarak kanıtlar; çağıranın uzağındaki bir değişiklik sessizce sarma
// üretemez.
func clampCount(count int64) int32 {
	if count < 0 {
		return 0
	}
	if count > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(count)
}

// writePage servis sayfasını liste zarfıyla yazar.
func writePage[S any, T any](w http.ResponseWriter, r *http.Request, page service.Page[S], convert func(S) T) {
	items := make([]T, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, convert(item))
	}
	corehttp.WriteJSON(r.Context(), w, http.StatusOK, listEnvelope{
		Data:   items,
		Count:  page.Count,
		Offset: page.Offset,
		Limit:  page.Limit,
	})
}

// decodeBody istek gövdesini hedefe çözer.
//
// Bilinmeyen alanlar REDDEDİLİR: sessizce yok sayılan bir alan, istemcinin
// gönderdiğini sandığı bir fiyatın hiç yazılmaması demektir. Gövde boyutu da
// sınırlıdır; aşılırsa çözümleme hatası olarak döner.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	reader := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(reader)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return coreerrors.Invalid(codeInvalidBody, "istek gövdesi boş olamaz")
		}
		return coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidBody,
			"istek gövdesi çözümlenemedi")
	}

	// Tek bir JSON belgesi beklenir; arkasından gelen ikinci belge sessizce
	// yok sayılırsa istemci gönderdiğinin işlendiğini sanırdı.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return coreerrors.Invalid(codeInvalidBody, "istek gövdesi tek bir JSON belgesi olmalı")
	}
	return nil
}

// pathID yol parametresini okur.
func pathID(r *http.Request, name string) string {
	return chi.URLParam(r, name)
}

// pageParams sorgu dizesinden sayfalama parametrelerini okur.
//
// Eksik parametre sıfır döner ve servis varsayılanı uygular; SAYIYA
// ÇEVRİLEMEYEN bir değer ise hata döner — sessizce sıfıra düşmek, istemcinin
// istediği sayfa yerine ilk sayfayı almasına yol açardı.
func pageParams(r *http.Request) (limit, offset int32, err error) {
	limit, err = intParam(r, "limit")
	if err != nil {
		return 0, 0, err
	}
	offset, err = intParam(r, "offset")
	if err != nil {
		return 0, 0, err
	}
	return limit, offset, nil
}

// intParam tek bir sayısal sorgu parametresini okur; yoksa sıfır döner.
func intParam(r *http.Request, name string) (int32, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return 0, coreerrors.Invalid(codeInvalidBody,
			"%q parametresi tam sayı olmalı, %q verildi", name, raw)
	}
	return int32(value), nil
}

// Hesaplama ucunun sorgu parametreleri.
//
// Ayrılmış adlar ucun POST hâlindeki JSON alan adlarının AYNISIDIR; taşınırken
// yeniden adlandırılmadılar. Yeni bir ad kümesi istemciye metot değişikliğinin
// üstüne bir de ad değişikliği yüklerdi ve karşılığında hiçbir şey
// kazandırmazdı. Biçimleri modülün öbür sorgu parametreleriyle de aynıdır
// (limit, offset): düz snake_case.
const (
	// paramCurrencyCode istenen para birimidir (ISO 4217).
	paramCurrencyCode = "currency_code"
	// paramQuantity hesaplamanın yapılacağı adettir.
	paramQuantity = "quantity"
	// paramAt hesaplama anıdır (RFC 3339).
	paramAt = "at"
	// paramAttrPrefix kural bağlamı alanlarının sorgu önekidir.
	paramAttrPrefix = "attr_"
)

// calculateQuery hesaplama bağlamını sorgu dizesinden okur.
//
// TANINMAYAN parametre hatadır; bu, ucun POST hâlindeki [decodeBody]
// katılığının GET karşılığıdır. Sessizce yok sayılsalardı "?qty=10" yazan bir
// istemci 10 adetlik fiyat sorduğunu sanırken tek adetlik fiyatı okurdu —
// hata dönmeyen, yalnızca YANLIŞ cevap veren bir arıza.
//
// Aynı parametrenin iki kez verilmesi de hatadır: net/url ikinci değeri
// saklar ama url.Values.Get yalnızca ilkini döner ve sessiz seçim, istemcinin
// gönderdiğinden BAŞKA bir bağlamda hesap yapmak demektir. Kural bağlamı da
// bir eşlemedir; bir alanın iki değeri olamaz.
func calculateQuery(r *http.Request) (service.CalculateParams, error) {
	attributes := map[string]string{}
	for name, values := range r.URL.Query() {
		if len(values) > 1 {
			return service.CalculateParams{}, coreerrors.Invalid(codeInvalidBody,
				"%q parametresi birden çok kez verildi", name)
		}
		switch {
		case name == paramCurrencyCode, name == paramQuantity, name == paramAt:
			// Ayrılmış adlar; değerleri aşağıda tek tek okunur.
		case strings.HasPrefix(name, paramAttrPrefix):
			attributes[strings.TrimPrefix(name, paramAttrPrefix)] = values[0]
		default:
			return service.CalculateParams{}, coreerrors.Invalid(codeInvalidBody,
				"%q parametresi tanınmıyor; kural bağlamı %q önekiyle verilir",
				name, paramAttrPrefix)
		}
	}

	quantity, err := intParam(r, paramQuantity)
	if err != nil {
		return service.CalculateParams{}, err
	}
	at, err := timeParam(r, paramAt)
	if err != nil {
		return service.CalculateParams{}, err
	}

	// Doğrulama YAPILMAZ: para biriminin geçerliliğine, adet sınırlarına ve
	// varsayılanlara servis karar verir (bkz. dto.go'daki toPriceInputs).
	return service.CalculateParams{
		CurrencyCode: r.URL.Query().Get(paramCurrencyCode),
		Quantity:     quantity,
		Attributes:   attributes,
		At:           at,
	}, nil
}

// timeParam tek bir zaman sorgu parametresini okur; yoksa sıfır zaman döner.
//
// Sıfır zaman servis için "şimdi" demektir. Çözülemeyen bir damga sessizce
// "şimdi"ye DÜŞMEZ: geçmiş ya da gelecek bir an için sorulmuş fiyata bugünün
// kampanyalarıyla cevap vermek, sessiz yanlışın en pahalı biçimidir.
func timeParam(r *http.Request, name string) (time.Time, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return time.Time{}, nil
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidBody,
			"%q parametresi RFC 3339 biçiminde olmalı, %q verildi", name, raw)
	}
	return value.UTC(), nil
}

// listTypeOrNil fiyat listesi türünü dize işaretçisi olarak döner; boşsa nil.
//
// Taban fiyatta liste türü yoktur ve JSON'da boş dize yerine null görünmesi,
// "liste yok" ile "türü boş liste" ayrımını korur.
func listTypeOrNil(t models.PriceListType) *string {
	if t == "" {
		return nil
	}
	value := string(t)
	return &value
}
