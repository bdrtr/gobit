// Package api tax modülünün HTTP yüzeyidir.
//
// # Neden yalnızca /admin/v1
//
// Vergi doğrudan MÜŞTERİYE açılmaz. Vitrinin göreceği tek şey sepetin
// hesaplanmış vergi satırıdır ve o, sepet akışı üzerinden gelir
// (internal/workflows/cart, "tax.interop" yüzeyi). Müşteriye bir "vergiyi
// hesapla" ucu açmak iki şeyi birden yapardı: mağazanın oran yapılandırmasını
// dışarıya ifşa eder ve sepetin toplamıyla ayrı hesaplanmış bir vergi
// arasında tutarsızlık kapısı açardı.
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
	"github.com/bdrtr/gobit/internal/modules/tax/service"
)

// Route yolları. Modül route'ları TAM YOL ile kaydedilir; "/admin/v1" gibi bir
// ön ek MOUNT EDİLMEZ, çünkü mount eden ilk modül o alt ağacın tamamını
// sahiplenir ve aynı ön eki kullanan diğer modüllerle çakışırdı.
const (
	pathAdminRegions     = "/admin/v1/tax-regions"
	pathAdminRegion      = "/admin/v1/tax-regions/{id}"
	pathAdminRegionRates = "/admin/v1/tax-regions/{id}/tax-rates"

	pathAdminRates     = "/admin/v1/tax-rates"
	pathAdminRate      = "/admin/v1/tax-rates/{id}"
	pathAdminRateRules = "/admin/v1/tax-rates/{id}/rules"
	pathAdminRateRule  = "/admin/v1/tax-rates/{id}/rules/{ruleID}"
)

// maxBodyBytes tek bir istek gövdesinin azami boyutudur.
//
// Vergi gövdeleri küçüktür (ülke kodu, ad, oran, üstveri); sınır bu yüzden
// dardır. Sınırsız bir gövde, tek istekle belleği tüketmenin en ucuz yoludur.
const maxBodyBytes int64 = 64 << 10 // 64 KiB

// codeInvalidBody istek gövdesi ya da parametresi çözümlenemediğinde dönen
// hata kodudur.
const codeInvalidBody = "tax_invalid_body"

// API tax'ın HTTP handler'larını barındırır.
type API struct {
	svc *service.Service
}

// New verilen servis üzerinde çalışan bir API üretir.
func New(svc *service.Service) *API {
	return &API{svc: svc}
}

// Routes tax'ın admin route'larını router'a bağlar.
func (a *API) Routes(r chi.Router) {
	r.Post(pathAdminRegions, a.createRegion)
	r.Get(pathAdminRegions, a.listRegions)
	r.Get(pathAdminRegion, a.getRegion)
	r.Delete(pathAdminRegion, a.deleteRegion)
	r.Get(pathAdminRegionRates, a.listRegionRates)

	r.Post(pathAdminRates, a.createRate)
	r.Get(pathAdminRates, a.listRates)
	r.Get(pathAdminRate, a.getRate)
	r.Put(pathAdminRate, a.updateRate)
	r.Delete(pathAdminRate, a.deleteRate)

	r.Post(pathAdminRateRules, a.createRule)
	r.Get(pathAdminRateRules, a.listRules)
	r.Delete(pathAdminRateRule, a.deleteRule)
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

// writeAll sayfalanmayan bir listeyi zarfıyla yazar.
//
// Count, dönen kayıt sayısıdır ve Limit ile aynıdır: liste sayfalanmadığı için
// "toplam" ile "sayfadaki" ayrımı yoktur. Zarfın şekli yine de tek biçimli
// kalır — istemcinin sayfalanan ve sayfalanmayan listeler için iki ayrı
// çözümleyici yazması gerekmez.
func writeAll[S any, T any](w http.ResponseWriter, r *http.Request, items []S, convert func(S) T) {
	out := make([]T, 0, len(items))
	for _, item := range items {
		out = append(out, convert(item))
	}
	// Uzunluk int32'ye sığdırılır: bu uçlar sayfalanmasa da dönen liste her
	// zaman servis sınırlarının altındadır ve sıkıştırma yalnızca çevrimin
	// SESSİZCE sarmasını imkânsız kılar.
	count := len(out)
	limit := int32(math.MaxInt32)
	if count < math.MaxInt32 {
		limit = int32(count)
	}
	corehttp.WriteJSON(r.Context(), w, http.StatusOK, listEnvelope{
		Data:   out,
		Count:  int64(count),
		Offset: 0,
		Limit:  limit,
	})
}

// decodeBody istek gövdesini hedefe çözer.
//
// Bilinmeyen alanlar REDDEDİLİR: sessizce yok sayılan bir alan, istemcinin
// gönderdiğini sandığı bir vergi oranının hiç yazılmaması demektir. Gövde
// boyutu da sınırlıdır; aşılırsa çözümleme hatası olarak döner.
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

// pathParam yol parametresini okur.
func pathParam(r *http.Request, name string) string {
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
