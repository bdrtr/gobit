// Package api product modülünün HTTP yüzeyidir.
//
// Katman İNCEDİR: gövdeyi çözer, sorgu parametrelerini okur, servisi çağırır ve
// yanıtı zarfa sarar. İş kuralı burada YOKTUR ve HTTP durum kodu ELLE
// SEÇİLMEZ — kod, servisin döndürdüğü tipli hatanın sınıfından
// corehttp.WriteError tarafından türetilir (plan Bölüm 8).
//
// Yanıt zarfı: liste uçları {"data": [...], "count": N, "offset": N, "limit": N},
// tekil uçlar {"data": {...}} döner.
//
// # Yetki
//
// /admin/v1 uçları yetki ister ve sözlük ikiye ayrılır: GET uçları [ScopeRead],
// POST/PUT/PATCH/DELETE uçları [ScopeWrite] (bkz. [Handler.Routes]).
// corehttp.ScopeAdmin ÜST YETKİDİR ve ikisini de tek başına karşılar.
//
// /store/v1 uçlarına yetki EKLENMEZ: mağaza yüzeyinin kimliği publishable
// anahtardır ve o anahtar tanımı gereği yetki TAŞIMAZ.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// maxBodyBytes tek bir istek gövdesinin üst sınırıdır.
//
// Sınır olmadan tek bir istemci, gövdeyi çözerken sunucunun belleğini
// tüketebilirdi. 1 MiB, görsel listesi ve varyantlarıyla birlikte gelen bir
// ürün için fazlasıyla yeterlidir.
const maxBodyBytes = 1 << 20

// Hata kodları; istemci errors.CodeOf ile bunlara bakabilir.
const (
	codeBadJSON  = "product_bad_json"
	codeBadParam = "product_bad_query_param"
)

// Handler modülün HTTP handler'larını taşır.
type Handler struct {
	svc Catalog
	// graphql vitrinin GraphQL okuma ucudur (bkz. [graph.NewHandler]).
	//
	// Handler'ın bir ALANIDIR, istek başına kurulmaz: gqlgen sunucusu şemayı
	// bir kez ayrıştırır ve ayrıştırılmış sorgu önbelleğini içinde taşır;
	// her istekte yeniden kurmak ikisini de çöpe atardı.
	graphql http.Handler
}

// New verilen servis ve GraphQL sınırlarıyla handler üretir.
//
// GraphQL ucu BURADA kurulur ki modülün tüm HTTP yüzeyi tek bir yerden
// (bkz. [Handler.Routes]) bağlansın; yoksa uçların listesi iki dosyaya
// bölünürdü.
//
// graphOpts SIFIR DEĞER olabilir ve paket varsayılanlarını verir; "sınırsız"
// anlamına GELMEZ (bkz. [graph.Options]). Sınırlar api katmanında
// yorumlanmaz, olduğu gibi geçirilir: burada bir varsayılan seçmek aynı
// kuralın ikinci bir tanımı olurdu.
//
// svc nil olabilir (belge üretimi bunu yapar): gqlgen sunucusu kurulurken
// servise dokunmaz, yalnızca istek geldiğinde çağırır.
func New(svc Catalog, graphOpts graph.Options) *Handler {
	return &Handler{svc: svc, graphql: graph.NewHandler(svc, graphOpts)}
}

// Somut servisin api katmanının beklediği yüzeyi karşıladığı derleme zamanında
// sabitlenir: imza kayması testte değil derlemede görünür.
var _ Catalog = (*service.Service)(nil)

// listEnvelope liste yanıtlarının zarfıdır (plan Bölüm 8).
type listEnvelope struct {
	Data any `json:"data"`
	// Count süzgece uyan TOPLAM kayıt sayısıdır ve SAYILMADIYSA gövdeden
	// tümüyle DÜŞER (omitempty + işaretçi).
	//
	// Sayımın kapatılabildiği tek uç bugün vitrin listesidir
	// (bkz. [Handler.storeListProducts]); geri kalan her liste her zaman sayar
	// ve alan onlarda hep yazılır — yani varsayılan yanıtın baytları
	// DEĞİŞMEZ.
	//
	// # Neden 0 değil, neden null değil, neden YOK
	//
	// 0 bir YALANDIR: "eşleşen kayıt yok" cümlesinden ayırt edilemez ve
	// istemci sayfa sayısını sıfır hesaplar. JSON null ise alanın TİPİNİ
	// değiştirir (integer → integer|null) ve JavaScript'te sessizce sayıya
	// çevrilir — `null / 20` sıfırdır, `undefined / 20` NaN'dır; yani eksik
	// alan yanlış cevabı GÜRÜLTÜLÜ verir, null sessiz verir.
	//
	// Asıl gerekçe ise bu değil, SİMETRİDİR: aynı katalogun GraphQL yüzeyinde
	// "count" istemcinin seçmediği sürece zaten yanıtta yoktur. Alanı düşürmek
	// yeni bir biçim icat etmez, iki okuma yüzeyini aynı cümleye getirir —
	// sayaç, İSTENDİĞİ SÜRECE vardır.
	//
	// Alanın düşebildiği OpenAPI belgesinde de yazılıdır: vitrin listesinin
	// yanıt şeması "count"u zorunlu alanlar arasına KOYMAZ
	// (bkz. openapi.Doc.ListOptionalCount).
	Count  *int `json:"count,omitempty"`
	Offset int  `json:"offset"`
	Limit  int  `json:"limit"`
}

// itemEnvelope tekil yanıtların zarfıdır.
type itemEnvelope struct {
	Data any `json:"data"`
}

// writeList sayfalı sonucu zarfa sararak yazar.
//
// Boş sonuç JSON'da null değil BOŞ DİZİ olur: istemcinin "data" alanını her
// zaman dizi sayabilmesi, her yanıtta null kontrolü yapmasından iyidir.
func writeList[T any](w http.ResponseWriter, r *http.Request, res service.ListResult[T]) {
	items := res.Items
	if items == nil {
		items = []T{}
	}
	corehttp.WriteJSON(r.Context(), w, http.StatusOK, listEnvelope{
		Data:   items,
		Count:  res.Count,
		Offset: res.Offset,
		Limit:  res.Limit,
	})
}

// writeItem tekil kaydı zarfa sararak yazar.
func writeItem(w http.ResponseWriter, r *http.Request, status int, v any) {
	corehttp.WriteJSON(r.Context(), w, status, itemEnvelope{Data: v})
}

// decode istek gövdesini çözer.
//
// Bilinmeyen alan REDDEDİLİR: "titel" yazan bir istemci sessizce başlıksız bir
// ürün oluşturmak yerine ne yaptığını hemen öğrenir. Gövde sınırı aşarsa da
// tipli bir doğrulama hatası döner; sunucu hatası değildir.
func decode[T any](w http.ResponseWriter, r *http.Request) (T, error) {
	var out T

	// Sınır aşıldığında sunucunun isteği düzgün sonlandırabilmesi için yanıt
	// yazıcısı verilir; aksi hâlde bağlantı yarım okunmuş gövdeyle kalırdı.
	body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(&out); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return out, coreerrors.Wrap(err, coreerrors.KindInvalid, codeBadJSON,
				"istek gövdesi çok büyük (en fazla %d bayt)", maxBodyBytes)
		}
		if errors.Is(err, io.EOF) {
			return out, coreerrors.Invalid(codeBadJSON, "istek gövdesi boş")
		}
		return out, coreerrors.Wrap(err, coreerrors.KindInvalid, codeBadJSON,
			"istek gövdesi çözümlenemedi: %v", err)
	}

	// Tek bir JSON değerinden fazlası gönderilmişse istek belirsizdir; ikinci
	// gövdenin sessizce yok sayılması, gönderenin hangi kaydın yazıldığını
	// bilmemesi demektir.
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return out, coreerrors.Invalid(codeBadJSON, "istek gövdesi tek bir JSON nesnesi olmalı")
	}
	return out, nil
}

// pathParam yol parametresini okur ve boşsa tipli hata döner.
func pathParam(r *http.Request, name string) (string, error) {
	value := strings.TrimSpace(chi.URLParam(r, name))
	if value == "" {
		return "", coreerrors.Invalid(codeBadParam, "%s yol parametresi zorunludur", name)
	}
	return value, nil
}

// intParam sorgu parametresini tam sayı olarak okur; yoksa varsayılanı döner.
func intParam(r *http.Request, name string, fallback int) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, coreerrors.Wrap(err, coreerrors.KindInvalid, codeBadParam,
			"%s parametresi tam sayı olmalı (verilen: %q)", name, raw)
	}
	return value, nil
}

// stringParam sorgu parametresini okur; verilmemişse nil döner.
//
// Boş dizge de "verilmedi" sayılır: "?handle=" ile hiçbir ürünü eşleştirmeyen
// bir filtre kurmak istemcinin niyeti değildir.
func stringParam(r *http.Request, name string) *string {
	value := strings.TrimSpace(r.URL.Query().Get(name))
	if value == "" {
		return nil
	}
	return &value
}

// boolParam sorgu parametresini mantıksal değer olarak okur; yoksa
// varsayılanı döner.
//
// Varsayılan ÇAĞRIDA yazılır, burada değil — [intParam] ile aynı biçim. Sabit
// bir false varsayılanı yeterli olmayı bıraktı: "with_count" parametresinin
// varsayılanı TRUE'dur (bkz. [Handler.storeListProducts]) ve varsayılanı
// fonksiyonun içine gömmek, çağıranın gördüğü imzada YAZMAYAN bir kural
// olurdu.
func boolParam(r *http.Request, name string, fallback bool) (bool, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, coreerrors.Wrap(err, coreerrors.KindInvalid, codeBadParam,
			"%s parametresi mantıksal değer olmalı (verilen: %q)", name, raw)
	}
	return value, nil
}

// paging sayfalama parametrelerini okur.
func paging(r *http.Request) (limit, offset int, err error) {
	limit, err = intParam(r, "limit", 0)
	if err != nil {
		return 0, 0, err
	}
	offset, err = intParam(r, "offset", 0)
	if err != nil {
		return 0, 0, err
	}
	return limit, offset, nil
}
