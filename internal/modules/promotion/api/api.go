// Package api promotion modülünün HTTP yüzeyidir.
//
// İki ad alanı vardır (plan Bölüm 8): /admin/v1 yönetim, /store/v1 müşteri.
// Yazma yüzeyi YALNIZCA admin tarafındadır; store tarafında tek bir kupon
// doğrulama uç noktası bulunur.
//
// # Store yüzeyi ne SIZDIRMAZ
//
// Müşteriye giden gövde promosyonun DURUMUNU, kullanım sayacını, kampanya
// bütçesini, üstverisini ve KURAL KOŞULLARINI içermez. Kod geçerli değilse
// sebep de söylenmez: taslak, pasif, süresi geçmiş, bütçesi bitmiş ve
// var olmayan kod AYNI 404'ü döner (gerekçe:
// service.Service.LookupStoreCoupon).
//
// Handler'lar status kodu SEÇMEZ: servis tipli hata döner, corehttp.WriteError
// onu status koduna çevirir (plan Bölüm 2.7). Bu, hata sınıflandırmasının tek
// bir yerde kalmasını sağlar.
//
// # Yetki
//
// Yönetim uçlarının tamamı yetki ister ve sözlük iki girdiden ibarettir:
//
//   - [ScopeRead] — /admin/v1 altındaki OKUMA (GET, HEAD) uçlarını açar:
//     kampanyalar, promosyonlar, kurallar ve kullanım kayıtları okunabilir.
//   - [ScopeWrite] — /admin/v1 altındaki YAZMA (POST, PUT, PATCH, DELETE)
//     uçlarını açar: CRUD'un yanı sıra kullanım/iade (redeem, release) ve
//     indirim hesabı (compute) uçları da buraya girer.
//
// corehttp.ScopeAdmin ÜST YETKİDİR ve ikisini de karşılar; ayrıca
// listelenmesine gerek yoktur, corehttp.Principal.HasScope bunu zaten yapar.
//
// /store/v1 kupon doğrulama ucu yetki İSTEMEZ: mağaza yüzeyinin kimliği
// publishable anahtardır ve o anahtar tanımı gereği yetki TAŞIMAZ.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/promotion/service"
)

// maxBodyBytes tek bir istek gövdesinin azami boyutudur.
//
// İndirim hesabı tek istekte yüzlerce satır taşıyabildiği için sınır
// cömerttir; ama sınırsız değildir — sınırsız bir gövde, tek istekle belleği
// tüketmenin en ucuz yoludur.
const maxBodyBytes int64 = 1 << 20 // 1 MiB

// codeInvalidBody istek gövdesi çözümlenemediğinde dönen hata kodudur.
const codeInvalidBody = "promotion_invalid_body"

// API promotion'ın HTTP handler'larını barındırır.
type API struct {
	svc *service.Service
}

// New verilen servis üzerinde çalışan bir API üretir.
func New(svc *service.Service) *API {
	return &API{svc: svc}
}

// Yetki sözlüğü: promotion'ın yönetim uçlarının istediği yetkiler.
//
// Sözlük BİLİNÇLİ OLARAK okuma/yazma ayrımından ibarettir. Kaynak başına ayrı
// yetki ("campaigns:write", "redemptions:write" …) tanımlamak listeyi büyütür
// ama bugün verilebilecek yeni bir kararı mümkün kılmaz: promosyonu
// yazabilen bir kimlik zaten kampanyayı da yazabilmelidir, çünkü bütçe
// kampanyada tutulur. Ayrım gerçekten gerektiğinde eklenir; şimdiden
// eklenirse yalnızca yanlış bir kesinlik hissi verir.
const (
	// ScopeRead promotion yönetim yüzeyindeki OKUMA uçlarının istediği
	// yetkidir.
	ScopeRead = "promotion:read"
	// ScopeWrite promotion yönetim yüzeyindeki YAZMA uçlarının istediği
	// yetkidir.
	ScopeWrite = "promotion:write"
)

// Routes promotion'ın admin ve store route'larını router'a bağlar.
//
// Route'lar chi'nin Route/Mount yardımcılarıyla DEĞİL, tam yollarla kaydedilir:
// /admin/v1 önekini birden çok modül paylaşır ve aynı öneki iki kez Mount etmek
// chi'de panik üretirdi. Tam yol kaydı aynı ağaca yan yana yazar.
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
// yetkileri BOŞALTILMIŞ bir yönetim kullanıcısı promosyon oluşturup kendine
// %100 indirim yazabilirdi — doğrudan para kaybı.
//
// POST /admin/v1/promotions/compute yalnızca HESAPLAR, hiçbir şey yazmaz; yine
// de [ScopeWrite] ister. Sözlük yöntem üzerinden tanımlıdır ("POST → write")
// ve istisnası yoktur: "aslında okuma olan POST" ayrımı, sözlüğü uç uç
// tartışılan bir şeye çevirir ve bir sonraki gevşetme sessizce gelir. Hesabın
// gerçekten yazmadığını doğrulayan yer servis katmanıdır.
func (a *API) Routes(r chi.Router) {
	okuma := r.With(corehttp.RequireScope(ScopeRead))
	yazma := r.With(corehttp.RequireScope(ScopeWrite))

	yazma.Post("/admin/v1/campaigns", a.createCampaign)
	okuma.Get("/admin/v1/campaigns", a.listCampaigns)
	okuma.Get("/admin/v1/campaigns/{id}", a.getCampaign)
	yazma.Put("/admin/v1/campaigns/{id}", a.updateCampaign)
	yazma.Delete("/admin/v1/campaigns/{id}", a.deleteCampaign)

	yazma.Post("/admin/v1/promotions", a.createPromotion)
	okuma.Get("/admin/v1/promotions", a.listPromotions)
	okuma.Get("/admin/v1/promotions/{id}", a.getPromotion)
	yazma.Put("/admin/v1/promotions/{id}", a.updatePromotion)
	yazma.Delete("/admin/v1/promotions/{id}", a.deletePromotion)

	yazma.Put("/admin/v1/promotions/{id}/application-method", a.setApplicationMethod)
	yazma.Delete("/admin/v1/promotions/{id}/application-method", a.deleteApplicationMethod)

	okuma.Get("/admin/v1/promotions/{id}/rules", a.listPromotionRules)
	yazma.Post("/admin/v1/promotions/{id}/rules", a.createPromotionRule)
	yazma.Delete("/admin/v1/promotion-rules/{id}", a.deletePromotionRule)

	okuma.Get("/admin/v1/promotions/{id}/redemptions", a.listRedemptions)
	yazma.Post("/admin/v1/promotions/{id}/redeem", a.redeemPromotion)
	yazma.Post("/admin/v1/promotions/{id}/release", a.releasePromotion)

	yazma.Post("/admin/v1/promotions/compute", a.computeDiscounts)

	// Mağaza ucu DEĞİŞMEZ: publishable anahtar yetki taşımaz.
	r.Get("/store/v1/promotions/{code}", a.storeGetPromotion)
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
// Sayfalanmayan uç noktalarda (bir promosyonun kuralları) zarfın sayısal
// alanları kayıt sayısıyla doldurulur: istemcinin zarf şekli uç noktaya göre
// değişmez.
//
// Limit, dönen kayıt sayısına EŞİTTİR ve [service.MaxLimit] ile KIRPILMAZ.
// Kırpılsaydı 250 kurallı bir promosyon için yanıt "count=250, limit=100"
// derdi; istemci sayfa boyunu 100 sanıp sayfalama döngüsüne girer ve aynı
// kayıtları tekrar okurdu. Burada sayfa yoktur — tek sayfa tüm kayıtlardır.
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
// YEREL olarak kanıtlar.
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
// gönderdiğini sandığı bir kuralın hiç yazılmaması demektir. Gövde boyutu da
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

// stringParam tek bir dize sorgu parametresini işaretçi olarak döner; yoksa nil.
//
// İşaretçi dönmesi bilinçlidir: "süzgeç verilmedi" ile "boş değerle süzülsün"
// ayrımı korunur.
func stringParam(r *http.Request, name string) *string {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil
	}
	return &raw
}
