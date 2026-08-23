package http_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

func TestStatusForHerSinifIcinDogruKod(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want int
	}{
		{"not found", coreerrors.NotFound("product_not_found", "ürün yok"), http.StatusNotFound},
		{"invalid", coreerrors.Invalid("invalid_body", "gövde geçersiz"), http.StatusUnprocessableEntity},
		{"conflict", coreerrors.Conflict("sku_exists", "sku kayıtlı"), http.StatusConflict},
		{"unauthorized", coreerrors.Unauthorized("no_token", "token yok"), http.StatusUnauthorized},
		{"forbidden", coreerrors.Forbidden("no_scope", "yetki yok"), http.StatusForbidden},
		{"unavailable", coreerrors.Unavailable("db_down", "veritabanı kapalı"), http.StatusServiceUnavailable},
		{"internal", coreerrors.Internal("boom", "patladı"), http.StatusInternalServerError},
		{"tipsiz hata", coreerrors.New("sıradan hata"), http.StatusInternalServerError},
		{"nil hata", nil, http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, corehttp.StatusFor(tt.err))
		})
	}
}

func TestStatusForSarmalanmisHatadaDaDogru(t *testing.T) {
	t.Parallel()

	// core/errors.Wrap ile sarmalama.
	wrapped := coreerrors.Wrap(coreerrors.New("pq: deadlock detected"),
		coreerrors.KindConflict, "order_conflict", "sipariş güncellenemedi")
	assert.Equal(t, http.StatusConflict, corehttp.StatusFor(wrapped))

	// fmt.Errorf %w ile sarmalama: tipli hata zincirin derininde.
	deep := fmt.Errorf("servis katmanı: %w",
		fmt.Errorf("repository: %w", coreerrors.NotFound("cart_not_found", "sepet yok")))
	assert.Equal(t, http.StatusNotFound, corehttp.StatusFor(deep))

	// Tipli hatanın sardığı tipsiz hata sınıfı belirlemez; en dıştaki tipli hata belirler.
	unavailable := coreerrors.Wrap(coreerrors.New("i/o timeout"),
		coreerrors.KindUnavailable, "redis_down", "redis erişilemiyor")
	assert.Equal(t, http.StatusServiceUnavailable, corehttp.StatusFor(unavailable))
}

// nilGorunuyorMu hatanın error arayüzünde nil görünüp görünmediğini bildirir.
// Karşılaştırma ayrı bir fonksiyonda tutulur: çağıran yerde somut tip bilindiği
// için statik analiz "her zaman doğru" uyarısı verir, oysa test tam olarak bu
// çalışma zamanı davranışını kanıtlamak ister. Ayrıca testify'ın NotNil'i
// yansıma kullandığından tipli-nil işaretçiyi nil sayar ve tuzağı gizlerdi.
func nilGorunuyorMu(err error) bool { return err == nil }

// tipliNilHata gerçek bir servis katmanının ürettiği tuzağı taklit eder:
// sarmalanacak hata nil iken errors.Wrap (*Error)(nil) döner.
func tipliNilHata(alttaki error) error {
	return coreerrors.Wrap(alttaki, coreerrors.KindInternal, "db_failed", "sorgu başarısız")
}

func TestStatusForTipliNilHatadaPaniklemez(t *testing.T) {
	t.Parallel()

	err := tipliNilHata(nil)
	require.False(t, nilGorunuyorMu(err), "tipli-nil işaretçi error arayüzünde nil görünmez")

	require.NotPanics(t, func() {
		assert.Equal(t, http.StatusInternalServerError, corehttp.StatusFor(err),
			"sınıflandırılamayan hata 500 olmalı")
	})

	log, buf := testLogger()
	ctx := corehttp.WithLogger(t.Context(), log)
	rec := httptest.NewRecorder()
	require.NotPanics(t, func() { corehttp.WriteError(ctx, rec, err) })

	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	var body corehttp.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "gövde JSON olmalı: %s", rec.Body.String())
	assert.Equal(t, "internal_error", body.Error.Code)
	assert.Equal(t, "beklenmeyen bir sunucu hatası oluştu", body.Error.Message)
	assert.Contains(t, buf.String(), "istek sunucu hatasıyla sonuçlandı", "hata yine de loglanmalı")
}

func TestStatusForZincirinDerinindekiTipliNilDePaniklemez(t *testing.T) {
	t.Parallel()

	var bos *coreerrors.Error
	derin := fmt.Errorf("servis katmanı: %w", bos)

	require.NotPanics(t, func() {
		assert.Equal(t, http.StatusInternalServerError, corehttp.StatusFor(derin))
	})
	require.NotPanics(t, func() {
		corehttp.WriteError(t.Context(), httptest.NewRecorder(), derin)
	})
}

func TestWriteErrorInternaldaAlttakiMesajiSizdirmaz(t *testing.T) {
	t.Parallel()

	const (
		gizliSurucuHatasi = `pq: password authentication failed for user "gobit_admin"`
		gizliSorgu        = "SELECT secret FROM api_keys WHERE id = $1"
	)

	log, buf := testLogger()
	ctx := corehttp.WithLogger(corehttp.WithRequestID(t.Context(), "req_internal"), log)

	err := coreerrors.Wrap(coreerrors.New(gizliSurucuHatasi),
		coreerrors.KindInternal, "db_query_failed", "sorgu başarısız: %s", gizliSorgu)

	rec := httptest.NewRecorder()
	corehttp.WriteError(ctx, rec, err)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	govde := rec.Body.String()
	assert.NotContains(t, govde, gizliSurucuHatasi, "alttaki sürücü hatası istemciye sızmamalı")
	assert.NotContains(t, govde, "password authentication")
	assert.NotContains(t, govde, gizliSorgu, "tipli hatanın Message'ı da sızmamalı")
	assert.NotContains(t, govde, "SELECT")

	var body corehttp.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "gövde JSON olmalı: %s", govde)
	assert.Equal(t, "db_query_failed", body.Error.Code, "makine kodu korunur")
	assert.Equal(t, "beklenmeyen bir sunucu hatası oluştu", body.Error.Message)
	assert.Equal(t, "req_internal", body.Error.RequestID)
	assert.Nil(t, body.Error.Details, "internal hatada Details istemciye gitmez")

	// KANIT: gerçek hata kaybolmaz, loga yazılır.
	records := logRecords(t, buf)
	require.Len(t, records, 1)
	assert.Equal(t, "istek sunucu hatasıyla sonuçlandı", records[0]["msg"])
	assert.Contains(t, records[0]["error"], gizliSurucuHatasi, "alttaki sürücü hatası loglanmalı")
	assert.Contains(t, records[0]["error"], gizliSorgu, "sarmalayan mesaj da loglanmalı")
	assert.Equal(t, "req_internal", records[0]["request_id"])
}

func TestWriteErrorTipsizHatayiDaMaskeler(t *testing.T) {
	t.Parallel()

	log, buf := testLogger()
	ctx := corehttp.WithLogger(t.Context(), log)

	rec := httptest.NewRecorder()
	corehttp.WriteError(ctx, rec, coreerrors.New("open /etc/gobit/secrets.yaml: permission denied"))

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "secrets.yaml", "sınıflandırılmamış hata da sızdırılmaz")

	var body corehttp.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "internal_error", body.Error.Code, "kodu olmayan hataya varsayılan kod verilir")
	assert.Equal(t, "beklenmeyen bir sunucu hatası oluştu", body.Error.Message)

	records := logRecords(t, buf)
	require.Len(t, records, 1)
	assert.Contains(t, records[0]["error"], "secrets.yaml", "gerçek hata loglanmalı")
}

func TestWriteErrorDigerSiniflardaMesajiGecirir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"not found", coreerrors.NotFound("product_not_found", "ürün bulunamadı: %s", "prod_1"), http.StatusNotFound, "product_not_found"},
		{"invalid", coreerrors.Invalid("invalid_email", "e-posta geçersiz"), http.StatusUnprocessableEntity, "invalid_email"},
		{"conflict", coreerrors.Conflict("sku_exists", "sku zaten kayıtlı"), http.StatusConflict, "sku_exists"},
		{"unauthorized", coreerrors.Unauthorized("token_expired", "token süresi doldu"), http.StatusUnauthorized, "token_expired"},
		{"forbidden", coreerrors.Forbidden("scope_missing", "bu işlem için yetkiniz yok"), http.StatusForbidden, "scope_missing"},
		{"unavailable", coreerrors.Unavailable("payment_provider_down", "ödeme sağlayıcısına ulaşılamıyor"), http.StatusServiceUnavailable, "payment_provider_down"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			corehttp.WriteError(t.Context(), rec, tt.err)

			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))

			var body corehttp.ErrorResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "gövde JSON olmalı: %s", rec.Body.String())
			assert.Equal(t, tt.wantCode, body.Error.Code)

			var typed *coreerrors.Error
			require.True(t, coreerrors.As(tt.err, &typed))
			assert.Equal(t, typed.Message, body.Error.Message, "internal dışı sınıflarda mesaj olduğu gibi geçer")
		})
	}
}

func TestWriteErrorDetaylariGecirir(t *testing.T) {
	t.Parallel()

	err := coreerrors.Invalid("validation_failed", "girdi doğrulanamadı").
		WithDetails(map[string]any{"field": "email", "rule": "format"})

	rec := httptest.NewRecorder()
	corehttp.WriteError(t.Context(), rec, err)

	var body corehttp.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "gövde JSON olmalı: %s", rec.Body.String())
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Equal(t, map[string]any{"field": "email", "rule": "format"}, body.Error.Details)
}

func TestWriteErrorKodsuzHataSinifAdiniKullanir(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	corehttp.WriteError(t.Context(), rec, coreerrors.NotFound("", "bulunamadı"))

	var body corehttp.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "not_found", body.Error.Code)
}

func TestWriteErrorRequestIDGovdeyeGirer(t *testing.T) {
	t.Parallel()

	// Kimlik gerçek middleware zinciriyle üretilir; el ile enjekte edilmez.
	h := corehttp.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		corehttp.WriteError(r.Context(), w, coreerrors.NotFound("cart_not_found", "sepet bulunamadı"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/store/v1/carts/cart_1", http.NoBody)
	req.Header.Set(requestIDHeaderName, "req_govde_kimligi")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var body corehttp.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "gövde JSON olmalı: %s", rec.Body.String())
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "req_govde_kimligi", body.Error.RequestID)
	assert.Equal(t, rec.Header().Get(requestIDHeaderName), body.Error.RequestID,
		"gövdedeki kimlik yanıt başlığıyla aynı olmalı")
}

func TestWriteErrorRequestIDYoksaAlanDusurulur(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	corehttp.WriteError(t.Context(), rec, coreerrors.NotFound("x_not_found", "yok"))

	assert.NotContains(t, rec.Body.String(), "request_id", "boş kimlik gövdeye yazılmaz")
}

func TestWriteJSONGovdeVeBasliklariYazar(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	corehttp.WriteJSON(t.Context(), rec, http.StatusCreated, map[string]any{"id": "prod_1", "count": 2})

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "prod_1", body["id"])
	assert.InDelta(t, 2.0, body["count"], 0)
}

func TestWriteJSONNilDegerdeGovdeYazmaz(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	corehttp.WriteJSON(t.Context(), rec, http.StatusNoContent, nil)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String())
}

// kodlanamazDeger JSON'a çevrilemeyen bir tiptir; WriteJSON'ın hata yolunu test eder.
type kodlanamazDeger struct {
	Fn func() `json:"fn"`
}

func TestWriteJSONKodlamaHatasinda500Doner(t *testing.T) {
	t.Parallel()

	log, buf := testLogger()
	ctx := corehttp.WithLogger(t.Context(), log)

	rec := httptest.NewRecorder()
	corehttp.WriteJSON(ctx, rec, http.StatusOK, kodlanamazDeger{Fn: func() {}})

	assert.Equal(t, http.StatusInternalServerError, rec.Code, "kodlanamayan gövde 200 ile dönmemeli")
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))

	var body corehttp.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "yedek gövde geçerli JSON olmalı: %s", rec.Body.String())
	assert.Equal(t, "internal_error", body.Error.Code)
	assert.Contains(t, buf.String(), "yanıt gövdesi kodlanamadı")
}

// TestWriteErrorMasksUnknownKind enum dışı bir Kind değerinin de maskelendiğini
// doğrular.
//
// Regresyon: maskeleme kararı `kind == KindInternal` eşitliğine bağlıydı.
// coreerrors.Kind bir uint8 ve Error.Kind alanı dışa açık olduğu için çağıran
// enum dışında bir değer kurabilir; böyle bir hata 500 dönüyor ama gövdesi
// MASKELENMİYORDU, yani sunucu içi ayrıntı (DSN, sorgu) istemciye sızıyordu.
func TestWriteErrorMasksUnknownKind(t *testing.T) {
	const gizli = "dsn=postgres://kullanici:parola@db.internal/gobit"

	unknown := &coreerrors.Error{
		Kind:    coreerrors.Kind(99), // enum dışı
		Code:    "beklenmedik",
		Message: "bağlantı kurulamadı " + gizli,
	}

	rec := httptest.NewRecorder()
	corehttp.WriteError(context.Background(), rec, unknown)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, beklenen 500", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, gizli) {
		t.Errorf("enum dışı Kind gövdeyi sızdırdı: %s", body)
	}
	if got := corehttp.StatusFor(unknown); got != http.StatusInternalServerError {
		t.Errorf("StatusFor() = %d, beklenen 500", got)
	}
}
