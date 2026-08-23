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
package api

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
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
func (a *API) Routes(r chi.Router) {
	r.Post("/admin/v1/price-sets", a.createPriceSet)
	r.Get("/admin/v1/price-sets", a.listPriceSets)
	r.Get("/admin/v1/price-sets/{id}", a.getPriceSet)
	r.Delete("/admin/v1/price-sets/{id}", a.deletePriceSet)
	r.Get("/admin/v1/price-sets/{id}/prices", a.listPrices)
	r.Post("/admin/v1/price-sets/{id}/prices", a.setPrices)
	r.Post("/admin/v1/price-sets/{id}/calculate", a.calculatePrice)

	r.Post("/admin/v1/price-lists", a.createPriceList)
	r.Get("/admin/v1/price-lists", a.listPriceLists)
	r.Get("/admin/v1/price-lists/{id}", a.getPriceList)
	r.Put("/admin/v1/price-lists/{id}", a.updatePriceList)
	r.Delete("/admin/v1/price-lists/{id}", a.deletePriceList)

	r.Get("/admin/v1/prices/{price_id}/rules", a.listPriceRules)
	r.Post("/admin/v1/prices/{price_id}/rules", a.createPriceRule)
	r.Delete("/admin/v1/price-rules/{id}", a.deletePriceRule)

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
