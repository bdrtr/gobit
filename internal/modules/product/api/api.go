// Package api product modülünün HTTP yüzeyidir.
//
// Katman İNCEDİR: gövdeyi çözer, sorgu parametrelerini okur, servisi çağırır ve
// yanıtı zarfa sarar. İş kuralı burada YOKTUR ve HTTP durum kodu ELLE
// SEÇİLMEZ — kod, servisin döndürdüğü tipli hatanın sınıfından
// corehttp.WriteError tarafından türetilir (plan Bölüm 8).
//
// Yanıt zarfı: liste uçları {"data": [...], "count": N, "offset": N, "limit": N},
// tekil uçlar {"data": {...}} döner.
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
}

// New verilen servisle handler üretir.
func New(svc Catalog) *Handler {
	return &Handler{svc: svc}
}

// Somut servisin api katmanının beklediği yüzeyi karşıladığı derleme zamanında
// sabitlenir: imza kayması testte değil derlemede görünür.
var _ Catalog = (*service.Service)(nil)

// listEnvelope liste yanıtlarının zarfıdır (plan Bölüm 8).
type listEnvelope struct {
	Data   any `json:"data"`
	Count  int `json:"count"`
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
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

// boolParam sorgu parametresini mantıksal değer olarak okur.
func boolParam(r *http.Request, name string) (bool, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return false, nil
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
