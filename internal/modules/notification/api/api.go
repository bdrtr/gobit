// Package api notification modülünün HTTP yüzeyidir.
//
// # Neden yalnızca OKUMA ve yalnızca /admin/v1
//
// Modülün tek yazma yolu bir OLAY ABONESİDİR: bildirim, sipariş verildiğinde
// tetiklenir. Bir "bildirim gönder" ucu açmak, aynı işi iki yoldan yapılır
// kılardı ve ikinci yol idempotency anahtarını dışarıdan seçilebilir hâle
// getirirdi — yani mükerrer bildirimi engelleyen tek koruma, çağıranın
// dikkatine bırakılırdı.
//
// Müşteriye açılan bir uç da yoktur: teslim günlüğü mağazanın iç kaydıdır ve
// müşterinin ondan öğrenebileceği hiçbir şey, siparişin kendisinden zaten
// öğrenemeyeceği bir şey değildir.
//
// Handler'lar status kodu SEÇMEZ: servis tipli hata döner, corehttp.WriteError
// onu status koduna çevirir (plan Bölüm 2.7).
//
// # Yetki
//
// Tek uç [ScopeRead] ister. Yazma yetkisi TANIMLANMAMIŞTIR: verilebileceği bir
// uç yoktur ve şimdiden tanımlamak, kimsenin karşılığını göremediği bir yetkiyi
// yetki sözlüğüne sokmak olurdu.
//
// Yetki kontrolü KİMLİKTEN SONRA gelir: kimlik yoksa 401, kimlik var ama yetki
// yetmiyorsa 403 döner. Kimliği kuran corehttp.RequireAdmin bu modülde değil,
// router'ı kuran tarafta (corehttp.APIGuards) takılır.
package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/notification/models"
	"github.com/bdrtr/gobit/internal/modules/notification/service"
)

// pathAdminDeliveries teslim günlüğü listesinin yoludur.
//
// Route TAM YOL ile kaydedilir; "/admin/v1" gibi bir ön ek MOUNT EDİLMEZ,
// çünkü mount eden ilk modül o alt ağacın tamamını sahiplenir ve aynı ön eki
// kullanan diğer modüllerle çakışırdı.
const pathAdminDeliveries = "/admin/v1/notifications"

// codeInvalidQuery sorgu parametresi çözümlenemediğinde dönen hata kodudur.
const codeInvalidQuery = "notification_invalid_query"

// Sorgu parametreleri.
const (
	queryReference = "reference"
	queryStatus    = "status"
	queryLimit     = "limit"
	queryOffset    = "offset"
)

// Yetki sözlüğü TEK GİRDİDEN ibarettir; yazma yetkisi yoktur (bkz. paket
// belgesi).

// ScopeRead teslim günlüğünü okuma yetkisidir.
//
// corehttp.ScopeAdmin ÜST YETKİDİR ve bunu da karşılar; ayrıca listelenmesine
// gerek yoktur, corehttp.Principal.HasScope bunu zaten yapar.
const ScopeRead = "notification:read"

// Deliveries handler'ın servisten istediği DAR yüzeydir.
//
// Somut *service.Service yerine tek metotluk bir arayüz kullanılır: HTTP
// katmanı servisin tamamına değil yalnızca burada sayılan çağrıya bağlanır ve
// handler davranışı (zarf, status eşlemesi, parametre çözümü) gerçek bir
// veritabanı olmadan sahte bir uygulamayla sınanabilir.
type Deliveries interface {
	ListDeliveries(ctx context.Context, in service.ListDeliveriesInput) ([]models.Delivery, int64, error)
}

// Handler notification'ın HTTP handler'larını barındırır.
type Handler struct {
	svc Deliveries
}

// New verilen servis üzerinde çalışan bir handler üretir.
func New(svc Deliveries) *Handler { return &Handler{svc: svc} }

// Routes notification'ın admin route'larını router'a bağlar.
//
// İki koruma katmanı vardır ve ikisi de gereklidir: KİMLİK (corehttp.RequireAdmin,
// router'ı kuran tarafta) ve YETKİ (burada, [ScopeRead]). İkincisi olmasaydı
// yetkileri boşaltılmış bir yönetim kullanıcısı da teslim günlüğünü okuyabilirdi;
// günlük kişisel veri taşımaz ama hangi siparişe ne zaman bildirim gittiğini
// gösterir, yani sipariş akışının zaman çizelgesidir.
func (h *Handler) Routes(r chi.Router) {
	r.With(corehttp.RequireScope(ScopeRead)).Get(pathAdminDeliveries, h.listDeliveries)
}

// listDeliveries GET /admin/v1/notifications handler'ıdır.
//
// Süzgeçler: "reference" (sipariş kimliği) ve "status". İkisi de opsiyoneldir;
// verilmezse tüm günlük, en yeniden eskiye sayfalanır.
func (h *Handler) listDeliveries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	in, err := listInput(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	kayitlar, toplam, err := h.svc.ListDeliveries(ctx, in)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	writePage(w, r, kayitlar, toplam, in.Page)
}

// listInput sorgu dizesinden liste girdisini kurar.
func listInput(r *http.Request) (service.ListDeliveriesInput, error) {
	limit, err := intParam(r, queryLimit)
	if err != nil {
		return service.ListDeliveriesInput{}, err
	}
	offset, err := intParam(r, queryOffset)
	if err != nil {
		return service.ListDeliveriesInput{}, err
	}

	return service.ListDeliveriesInput{
		Reference: optionalParam(r, queryReference),
		Status:    optionalParam(r, queryStatus),
		Page:      service.Page{Limit: limit, Offset: offset},
	}, nil
}

// optionalParam verilmemiş bir süzgeci nil, verilmişi işaretçi olarak döner.
//
// Ayrım servise kadar taşınır: nil "süzme" demektir, boş dizeye işaret eden bir
// değer ise "referansı boş olan kayıtları getir". İkisini değer tipiyle
// taşımak, "?reference=" yazan bir istemciye sessizce TÜM günlüğü döndürmek
// olurdu.
func optionalParam(r *http.Request, name string) *string {
	values := r.URL.Query()
	if !values.Has(name) {
		return nil
	}
	value := values.Get(name)
	return &value
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
		return 0, coreerrors.Invalid(codeInvalidQuery,
			"%q parametresi tam sayı olmalı, %q verildi", name, raw)
	}
	return value, nil
}

// listEnvelope liste yanıtlarının zarfıdır (plan Bölüm 8).
type listEnvelope struct {
	// Data geçerli sayfadaki kayıtlardır.
	Data any `json:"data"`
	// Count süzgece uyan TOPLAM kayıt sayısıdır.
	Count int64 `json:"count"`
	// Offset uygulanan atlama sayısıdır.
	Offset int64 `json:"offset"`
	// Limit uygulanan sayfa boyudur.
	Limit int64 `json:"limit"`
}

// writePage kayıtları liste zarfıyla yazar.
//
// Zarftaki Limit, isteğin ham değeri DEĞİL servisin uyguladığı değerdir:
// limit verilmemişse servis varsayılanı uygular ve zarfın onu bildirmesi,
// istemcinin bir sonraki sayfayı doğru hesaplayabilmesi için gerekir.
func writePage(w http.ResponseWriter, r *http.Request, kayitlar []models.Delivery, toplam int64, page service.Page) {
	limit := page.Limit
	if limit == 0 {
		limit = service.DefaultLimit
	}

	items := make([]deliveryDTO, 0, len(kayitlar))
	for i := range kayitlar {
		items = append(items, toDeliveryDTO(kayitlar[i]))
	}

	corehttp.WriteJSON(r.Context(), w, http.StatusOK, listEnvelope{
		Data:   items,
		Count:  toplam,
		Offset: page.Offset,
		Limit:  limit,
	})
}
