// Package api file modülünün HTTP yüzeyidir.
//
// İki ayrı yüzey vardır ve ikisinin KORUMASI da bilinçli olarak farklıdır:
// yönetim uçları (/admin/v1/uploads) korumalıdır, dosya SUNUM ucu (/files/…)
// değildir.
//
// # Sunum ucu neden /admin/v1 ya da /store/v1 ALTINDA DEĞİL
//
// Bu depodaki koruma yığını (corehttp.APIGuards) tam olarak iki öneki kapsar:
// /admin/v1 kimlik ister, /store/v1 publishable anahtar ister. Yüklenen
// görselin adresi ürün görseli akışına doğrudan takılır ve o adresi çağıran
// şey bir vitrin sayfasındaki <img src="…"> etiketidir. Tarayıcı, bir resim
// isteğine ÖZEL BAŞLIK EKLEYEMEZ: ne Authorization ne x-publishable-api-key.
// Yani sunum ucu korumalı bir önek altına konsaydı, yüklenen her görsel
// vitrinde 401 dönerdi — yükleme yolu teknik olarak çalışır, ürünü göstermek
// için hiçbir işe yaramazdı.
//
// Bu yüzden ayrı ve KORUMASIZ bir önek kullanılır. Ödünün sınırları açıkça
// çizilmiştir:
//
//   - Sunulan tek şey YÜKLENMİŞ dosyalardır. Adresteki anahtar önce yükleme
//     defterine sorulur; kaydı olmayan bir anahtar için depoya HİÇ
//     dokunulmaz (bkz. service.Service.OpenByKey). Uç bir "dosya oku" ucu
//     değil, "bu kaydı sun" ucudur.
//   - Anahtar TAHMİN EDİLEMEZ: 80 bit kriptografik rastgelelik taşır. Yani
//     korumasızlık "herkes her dosyayı listeleyebilir" demek değildir —
//     adresini bilen okuyabilir, ki zaten vitrinde yayımlanan şey odur.
//     Gizli kalması gereken belgeler için doğru cevap bu ucu korumak değil,
//     onları hiç buraya koymamaktır.
//   - Uç YALNIZCA OKUMADIR; yazma yolu tek yerdedir ve korumalıdır.
//
// Kabul edilen bedel de yazılmalı: /files hız sınırının DIŞINDADIR (yığın
// yalnızca iki API önekini kapsar). Karşılığında elde edilen şey, statik bir
// dosyanın statik dosya gibi sunulabilmesidir; alternatif, her görsel isteğine
// kimlik doğrulama maliyeti ödetmek olurdu.
//
// # Yetki
//
// /admin/v1 altındaki uçlar kimlikten AYRI olarak yetki ister:
//
//   - [ScopeRead] ("file:read") — listeleme.
//   - [ScopeWrite] ("file:write") — yükleme ve silme.
//
// corehttp.ScopeAdmin ("admin") ÜST YETKİDİR ve ikisini de karşılar.
//
// Handler'lar status kodu SEÇMEZ: servis tipli hata döner, corehttp.WriteError
// onu status koduna çevirir (plan Bölüm 2.7).
package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/file/models"
	"github.com/bdrtr/gobit/internal/modules/file/service"
)

// Route yolları. Modül route'ları TAM YOL ile kaydedilir; "/admin/v1" gibi bir
// ön ek MOUNT EDİLMEZ, çünkü mount eden ilk modül o alt ağacın tamamını
// sahiplenir ve aynı ön eki kullanan diğer modüllerle çakışırdı.
const (
	// pathAdminUploads yükleme ve listeleme ucudur.
	pathAdminUploads = "/admin/v1/uploads"
	// pathAdminUpload tek bir yüklemenin silme ucudur.
	pathAdminUpload = "/admin/v1/uploads/{id}"
	// pathFile dosya sunum ucudur; KORUMASIZ öneki bilinçlidir (paket belgesi).
	pathFile = "/files/{key}"
)

// Yol parametreleri.
const (
	paramID  = "id"
	paramKey = "key"
)

// Sorgu parametreleri.
const (
	queryLimit  = "limit"
	queryOffset = "offset"
)

// fieldFile multipart gövdesinde dosyanın beklendiği alan adıdır.
const fieldFile = "file"

// codeInvalidRequest istek gövdesi ya da parametresi çözümlenemediğinde dönen
// hata kodudur.
//
// TEK bir kod yeter ve bu bilinçlidir: bu katmanın verebileceği tüm kararlar
// aynı sınıfa girer ("istek beklenen biçimde değil"). İçerik tipi, boyut ve
// izin listesi kararları BU KATMANDA VERİLMEZ; onların kodları servistedir ve
// istemcinin gerçekten dallanacağı yer orasıdır.
const codeInvalidRequest = "file_invalid_request"

// headerContentTypeOptions tarayıcının içerik tipi tahminini KAPATAN başlıktır.
const headerContentTypeOptions = "X-Content-Type-Options"

// nosniff [headerContentTypeOptions] başlığının tek geçerli değeridir.
const nosniff = "nosniff"

// Yetki sözlüğü: file'ın yönetim uçlarının istediği yetkiler.
//
// Adlar TÜM modüllerde aynı kalıptadır ("<modül>:read" / "<modül>:write");
// her modülün kendi sözcüğünü uydurması, yetki dağıtan kişinin modül başına
// ayrı bir sözlük ezberlemesi demek olurdu.
const (
	// ScopeRead yükleme defterini OKUMA yetkisidir.
	ScopeRead = "file:read"

	// ScopeWrite yükleme ve silme yetkisidir.
	//
	// Yükleme ile silmenin AYNI yetkide toplanması bilinçlidir: ikisi de aynı
	// kaynağın yaşam döngüsüdür ve "yükleyebilen ama sildiğini geri
	// alamayan" bir rol, yanlış yüklenen dosyayı temizleyemeyeceği için
	// depoyu çöple doldururdu.
	ScopeWrite = "file:write"
)

// Uploads handler'ın servisten istediği DAR yüzeydir.
//
// Somut *service.Service yerine arayüz kullanılır: HTTP katmanı servisin
// tamamına değil yalnızca burada sayılan çağrılara bağlanır ve handler
// davranışı (multipart ayrıştırma, tip tespiti, zarf, sunum başlıkları)
// gerçek bir veritabanı ve gerçek bir disk olmadan sınanabilir.
type Uploads interface {
	// Upload gövdeyi denetleyip depoya yazdırır ve deftere kaydeder.
	Upload(ctx context.Context, in service.UploadInput) (models.Upload, error)
	// ListUploads defteri sayfalar; ikinci değer TÜM kayıtların sayısıdır.
	ListUploads(ctx context.Context, page service.Page) ([]models.Upload, int64, error)
	// DeleteUpload dosyayı ve kaydı siler; İDEMPOTENTTİR.
	DeleteUpload(ctx context.Context, id string) error
	// OpenByKey depo anahtarıyla bir dosyayı sunulmak üzere açar.
	OpenByKey(ctx context.Context, key string) (service.OpenedFile, error)
	// MaxUploadBytes tek bir yüklemenin azami boyutudur.
	MaxUploadBytes() int64
}

// Handler file'ın HTTP handler'larını barındırır.
type Handler struct {
	svc Uploads
}

// New verilen servis üzerinde çalışan bir handler üretir.
func New(svc Uploads) *Handler { return &Handler{svc: svc} }

// Routes file'ın route'larını router'a bağlar.
//
// Yönetim uçlarının iki koruma katmanı vardır ve ikisi de gereklidir: KİMLİK
// (corehttp.RequireAdmin, router'ı kuran tarafta) ve YETKİ (burada). Sunum
// ucunda ikisi de YOKTUR ve gerekçesi paket belgesindedir.
func (h *Handler) Routes(r chi.Router) {
	okuma := r.With(corehttp.RequireScope(ScopeRead))
	yazma := r.With(corehttp.RequireScope(ScopeWrite))

	yazma.Post(pathAdminUploads, h.createUpload)
	okuma.Get(pathAdminUploads, h.listUploads)
	yazma.Delete(pathAdminUpload, h.deleteUpload)

	r.Get(pathFile, h.serveFile)
}

// listUploads GET /admin/v1/uploads handler'ıdır.
func (h *Handler) listUploads(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := sayfaParametreleri(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	kayitlar, toplam, err := h.svc.ListUploads(ctx, page)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	writePage(w, r, kayitlar, toplam, page)
}

// deleteUpload DELETE /admin/v1/uploads/{id} handler'ıdır.
//
// Olmayan bir kimlik de 204 döner: servis idempotenttir ve gerekçesi orada
// yazılıdır (özet: silme bir son durum iddiasıdır, yeniden denenen temizlik
// akışı ikinci turda hata almamalıdır).
func (h *Handler) deleteUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.DeleteUpload(ctx, chi.URLParam(r, paramID)); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
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
	// Count TOPLAM kayıt sayısıdır.
	Count int64 `json:"count"`
	// Offset uygulanan atlama sayısıdır.
	Offset int64 `json:"offset"`
	// Limit uygulanan sayfa boyudur.
	Limit int64 `json:"limit"`
}

// writeItem tekil yanıtı zarfıyla yazar.
func writeItem(w http.ResponseWriter, r *http.Request, status int, data any) {
	corehttp.WriteJSON(r.Context(), w, status, itemEnvelope{Data: data})
}

// writePage kayıtları liste zarfıyla yazar.
//
// Zarftaki Limit, isteğin ham değeri DEĞİL servisin uyguladığı değerdir: limit
// verilmemişse servis varsayılanı uygular ve zarfın onu bildirmesi, istemcinin
// bir sonraki sayfayı doğru hesaplayabilmesi için gerekir.
func writePage(w http.ResponseWriter, r *http.Request, kayitlar []models.Upload, toplam int64, page service.Page) {
	limit := page.Limit
	if limit == 0 {
		limit = service.DefaultLimit
	}

	items := make([]uploadDTO, 0, len(kayitlar))
	for i := range kayitlar {
		items = append(items, toUploadDTO(kayitlar[i]))
	}

	corehttp.WriteJSON(r.Context(), w, http.StatusOK, listEnvelope{
		Data:   items,
		Count:  toplam,
		Offset: page.Offset,
		Limit:  limit,
	})
}

// sayfaParametreleri sorgu dizesinden sayfalama parametrelerini okur.
func sayfaParametreleri(r *http.Request) (service.Page, error) {
	limit, err := intParam(r, queryLimit)
	if err != nil {
		return service.Page{}, err
	}

	offset, err := intParam(r, queryOffset)
	if err != nil {
		return service.Page{}, err
	}

	return service.Page{Limit: limit, Offset: offset}, nil
}

// intParam tek bir sayısal sorgu parametresini okur; yoksa sıfır döner.
//
// SAYIYA ÇEVRİLEMEYEN bir değer hata döner; sessizce sıfıra düşmek, istemcinin
// istediği sayfa yerine ilk sayfayı almasına yol açardı.
func intParam(r *http.Request, name string) (int64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}

	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, coreerrors.Invalid(codeInvalidRequest,
			"%q parametresi tam sayı olmalı, %q verildi", name, raw)
	}

	return value, nil
}
