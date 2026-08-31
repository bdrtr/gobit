// Package api region modülünün HTTP yüzeyidir.
//
// İki ad alanı vardır (plan Bölüm 8): /admin/v1 yönetim, /store/v1 müşteri.
//
// # Yazma yüzeyi neden yalnızca bölgelerde
//
// Yönetim tarafında tam CRUD YALNIZCA bölgelere açıktır. Para birimi ve ülke
// REFERANS VERİDİR: ikisi de migration ile tohumlanır (bkz. 000002_region_seed)
// ve dış standartların (ISO 4217 / ISO 3166-1) kopyasıdır. Onlara yazma yüzeyi
// açmak, standardı elle "düzeltilebilir" kılardı; yanlış ondalık basamakla
// girilmiş tek bir para birimi, o para birimindeki her tutarı yanlış ölçekte
// gösterirdi. Bu yüzden currency ve country uç noktaları OKUMADIR; değiştirilen
// tek şey ülkenin hangi bölgeye ait olduğudur ve o da bölgenin alt kaynağıdır.
//
// # Yetki
//
// /admin/v1 altındaki uçlar kimlikten AYRI olarak yetki ister:
//
//   - [ScopeRead] ("region:read") — GET uçlarını açar.
//   - [ScopeWrite] ("region:write") — POST, PUT ve DELETE uçlarını açar.
//
// corehttp.ScopeAdmin ("admin") ÜST YETKİDİR ve ikisini de karşılar; tam
// yetkili bir kimliğe ayrıca verilmesi gerekmez.
//
// /store/v1 uçları yetki İSTEMEZ: mağaza yüzeyinin kimliği publishable
// anahtardır ve o anahtar tanımı gereği yetki taşımaz.
//
// Handler'lar status kodu SEÇMEZ: servis tipli hata döner, corehttp.WriteError
// onu status koduna çevirir (plan Bölüm 2.7). Bu, hata sınıflandırmasının tek
// bir yerde kalmasını sağlar.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/region/service"
)

// Route yolları. Modül route'ları TAM YOL ile kaydedilir; "/admin/v1" gibi bir
// ön ek MOUNT EDİLMEZ, çünkü mount eden ilk modül o alt ağacın tamamını
// sahiplenir ve aynı ön eki kullanan diğer modüllerle çakışırdı.
const (
	pathAdminRegions         = "/admin/v1/regions"
	pathAdminRegion          = "/admin/v1/regions/{id}"
	pathAdminRegionCountries = "/admin/v1/regions/{id}/countries"
	pathAdminRegionCountry   = "/admin/v1/regions/{id}/countries/{code}"
	pathAdminCountries       = "/admin/v1/countries"
	pathAdminCurrencies      = "/admin/v1/currencies"
	pathAdminCurrency        = "/admin/v1/currencies/{code}"

	pathStoreRegions = "/store/v1/regions"
	pathStoreRegion  = "/store/v1/regions/{id}"
)

// maxBodyBytes tek bir istek gövdesinin azami boyutudur.
//
// Bölge gövdeleri küçüktür (ad, para birimi, oran); sınır bu yüzden dardır.
// Sınırsız bir gövde, tek istekle belleği tüketmenin en ucuz yoludur.
const maxBodyBytes int64 = 64 << 10 // 64 KiB

// codeInvalidBody istek gövdesi ya da parametresi çözümlenemediğinde dönen
// hata kodudur.
const codeInvalidBody = "region_invalid_body"

// API region'ın HTTP handler'larını barındırır.
type API struct {
	svc *service.Service
}

// New verilen servis üzerinde çalışan bir API üretir.
func New(svc *service.Service) *API {
	return &API{svc: svc}
}

// Yetki sözlüğü: region'ın yönetim uçlarının istediği yetkiler.
//
// Adlar TÜM modüllerde aynı kalıptadır ("<modül>:read" / "<modül>:write").
// Her modülün kendi sözcüğünü uydurması, yetki dağıtan kişinin modül başına
// ayrı bir sözlük ezberlemesi demek olurdu; ezberlenmeyen sözlükte yapılan
// hata da her zaman aynı yöne düşer — fazla yetki verilir.
const (
	// ScopeRead region yönetim yüzeyindeki OKUMA uçlarının istediği yetkidir.
	//
	// Bölgeleri, ülkeleri ve para birimlerini okumaya yeter; hiçbir yazma
	// ucunu açmaz. Tam yetkili kimliklere ayrıca verilmesi gerekmez:
	// corehttp.ScopeAdmin taşıyan bir çağıran bunu da karşılar (bkz.
	// corehttp.Principal.HasScope).
	ScopeRead = "region:read"

	// ScopeWrite region yönetim yüzeyindeki YAZMA uçlarının istediği
	// yetkidir.
	//
	// Bölge oluşturma, güncelleme, silme ve bölge-ülke bağını değiştirme
	// uçlarını açar. Bu uçlar vergi ve para birimi seçimini belirler: bir
	// ülkeyi başka bir bölgeye taşımak, o ülkeden gelen her siparişin para
	// birimini ve vergi oranını değiştirir.
	ScopeWrite = "region:write"
)

// Routes region'ın admin ve store route'larını router'a bağlar.
//
// # KORUMA
//
// Yönetim uçlarının iki katmanı vardır ve ikisi de gereklidir:
//
//  1. KİMLİK — corehttp.RequireAdmin. Bu modülde DEĞİL, router'ı kuran
//     tarafta takılır (bkz. corehttp.APIGuards).
//  2. YETKİ — BURADA, uç uç corehttp.RequireScope ile: okuma uçları
//     [ScopeRead], yazma uçları [ScopeWrite] ister.
//
// İkinci katman olmasaydı kimlik doğrulama yetkilendirmenin yerine geçerdi:
// yetkileri bilinçli olarak boşaltılmış bir yönetim kullanıcısı da geçerli bir
// kimliktir ve DELETE /admin/v1/regions/{id} ile bölgeleri silebilirdi.
//
// Para birimi ve ülke uçları zaten OKUMADIR (bkz. paket belgesi); onlara
// ayrıca yazma yetkisi bağlanacak bir route yoktur.
//
// Store uçlarına yetki EKLENMEZ: mağaza yüzeyinin kimliği publishable
// anahtardır ve o anahtar tanımı gereği yetki TAŞIMAZ.
func (a *API) Routes(r chi.Router) {
	okuma := r.With(corehttp.RequireScope(ScopeRead))
	yazma := r.With(corehttp.RequireScope(ScopeWrite))

	yazma.Post(pathAdminRegions, a.createRegion)
	okuma.Get(pathAdminRegions, a.listRegions)
	okuma.Get(pathAdminRegion, a.getRegion)
	yazma.Put(pathAdminRegion, a.updateRegion)
	yazma.Delete(pathAdminRegion, a.deleteRegion)

	yazma.Post(pathAdminRegionCountries, a.addCountry)
	okuma.Get(pathAdminRegionCountries, a.listRegionCountries)
	yazma.Delete(pathAdminRegionCountry, a.removeCountry)

	okuma.Get(pathAdminCountries, a.listCountries)
	okuma.Get(pathAdminCurrencies, a.listCurrencies)
	okuma.Get(pathAdminCurrency, a.getCurrency)

	r.Get(pathStoreRegions, a.storeListRegions)
	r.Get(pathStoreRegion, a.storeGetRegion)
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

// optionalParam bir sorgu parametresini işaretçi olarak okur; yoksa nil.
//
// Boş dize ile "hiç verilmedi" ayrımı korunur: boş bir region_id süzgeci
// istemcinin yanlışlıkla boş gönderdiği bir kimliktir ve servis doğrulaması
// onu reddetmelidir, sessizce "süzme yok"a dönüşmemelidir.
func optionalParam(r *http.Request, name string) *string {
	if !r.URL.Query().Has(name) {
		return nil
	}
	value := r.URL.Query().Get(name)
	return &value
}
