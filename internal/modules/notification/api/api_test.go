package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/notification/api"
	"github.com/bdrtr/gobit/internal/modules/notification/models"
	"github.com/bdrtr/gobit/internal/modules/notification/service"
)

// fakeDeliveries api.Deliveries'in senaryolanabilir karşılığıdır.
//
// HTTP davranışının gerçek bir veritabanı olmadan sınanabilmesi için vardır:
// handler'ın işi status kodu SEÇMEK değil, servisin tipli hatasını
// corehttp.WriteError'a vermektir ve bu ancak servis yerine bir sahte konarak
// doğrulanabilir.
type fakeDeliveries struct {
	kayitlar []models.Delivery
	toplam   int64
	err      error

	// sonGirdi son çağrının girdisidir; sorgu parametrelerinin servise
	// BOZULMADAN ulaştığı bununla kanıtlanır.
	sonGirdi service.ListDeliveriesInput
}

func (f *fakeDeliveries) ListDeliveries(
	_ context.Context,
	in service.ListDeliveriesInput,
) ([]models.Delivery, int64, error) {
	f.sonGirdi = in
	if f.err != nil {
		return nil, 0, f.err
	}
	return f.kayitlar, f.toplam, nil
}

// testKayit tipik bir teslim günlüğü kaydıdır.
func testKayit() models.Delivery {
	an := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)

	return models.Delivery{
		ID:         "notif_01H",
		Template:   "order.placed",
		Channel:    "email",
		Reference:  "order_01H",
		ProviderID: "log",
		Status:     models.DeliverySent,
		CreatedAt:  an,
		UpdatedAt:  an,
	}
}

// yeniRouter sahte servis üzerinde çalışan bir router kurar.
func yeniRouter(svc *fakeDeliveries) chi.Router {
	r := chi.NewRouter()
	api.New(svc).Routes(r)

	return r
}

// yonetici testlerin varsayılan kimliğidir: tam yetkili bir yönetim
// kullanıcısı.
//
// Router burada DOĞRUDAN kuruluyor, yani corehttp.RequireAdmin zincirde yok ve
// context'e kimliği koyan kimse yok; uç corehttp.RequireScope ile korunduğu
// için kimliksiz istek 401 döner ve testin asıl doğruladığı davranışa sıra
// gelmezdi.
func yonetici() corehttp.Principal {
	return corehttp.Principal{ID: "user_test", Kind: "user", Scopes: []string{corehttp.ScopeAdmin}}
}

// istek verilen isteği tam yetkili bir kimlikle router'a uygular.
func istek(t *testing.T, r chi.Router, path string) *httptest.ResponseRecorder {
	t.Helper()

	return kimlikliIstek(t, r, path, yonetici())
}

// kimlikliIstek verilen isteği belirtilen kimlikle uygular.
func kimlikliIstek(t *testing.T, r chi.Router, path string, kimlik corehttp.Principal) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, path, strings.NewReader(""))
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), kimlik))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// TestListeKayitlariZarfIcindeDoner yanıt zarfını ve gövde alanlarını
// doğrular.
//
// Alıcı adresinin gövdede OLMADIĞI ayrıca sınanır: kayıtta da yoktur ve
// uydurulacak tek kaynak siparişin kendisi olurdu — bu uç, kişisel veriyi
// ikinci bir yerden servis eden bir kapıya dönüşmemelidir.
func TestListeKayitlariZarfIcindeDoner(t *testing.T) {
	svc := &fakeDeliveries{kayitlar: []models.Delivery{testKayit()}, toplam: 1}

	rec := istek(t, yeniRouter(svc), "/admin/v1/notifications")

	require.Equal(t, http.StatusOK, rec.Code)

	var govde struct {
		Data   []map[string]any `json:"data"`
		Count  int64            `json:"count"`
		Offset int64            `json:"offset"`
		Limit  int64            `json:"limit"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &govde))

	assert.Equal(t, int64(1), govde.Count)
	assert.Equal(t, service.DefaultLimit, govde.Limit, "zarf UYGULANAN limiti bildirmeli")
	require.Len(t, govde.Data, 1)

	kayit := govde.Data[0]
	assert.Equal(t, "notif_01H", kayit["id"])
	assert.Equal(t, "order.placed", kayit["template"])
	assert.Equal(t, "order_01H", kayit["reference"])
	assert.Equal(t, "sent", kayit["status"])
	assert.Equal(t, "log", kayit["provider_id"])
	assert.NotContains(t, kayit, "to", "gövde alıcı adresi TAŞIMAMALI")
	assert.NotContains(t, kayit, "email")
	assert.NotContains(t, kayit, "error", "boş hata alanı gövdeye yazılmamalı")
}

// TestListeSuzgecleriServiseGecer sorgu parametrelerinin bozulmadan servise
// ulaştığını doğrular.
func TestListeSuzgecleriServiseGecer(t *testing.T) {
	svc := &fakeDeliveries{}

	rec := istek(t, yeniRouter(svc),
		"/admin/v1/notifications?reference=order_01H&status=failed&limit=10&offset=20")

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, svc.sonGirdi.Reference)
	assert.Equal(t, "order_01H", *svc.sonGirdi.Reference)
	require.NotNil(t, svc.sonGirdi.Status)
	assert.Equal(t, "failed", *svc.sonGirdi.Status)
	assert.Equal(t, int64(10), svc.sonGirdi.Page.Limit)
	assert.Equal(t, int64(20), svc.sonGirdi.Page.Offset)
}

// TestVerilmeyenSuzgecNILGecer "verilmedi" ile "boş verildi" ayrımının
// korunduğunu doğrular.
//
// Ayrım kaybolsaydı "?reference=" yazan bir istemciye sessizce TÜM günlük
// dönerdi.
func TestVerilmeyenSuzgecNILGecer(t *testing.T) {
	svc := &fakeDeliveries{}

	require.Equal(t, http.StatusOK, istek(t, yeniRouter(svc), "/admin/v1/notifications").Code)
	assert.Nil(t, svc.sonGirdi.Reference)
	assert.Nil(t, svc.sonGirdi.Status)

	require.Equal(t, http.StatusOK, istek(t, yeniRouter(svc), "/admin/v1/notifications?reference=").Code)
	require.NotNil(t, svc.sonGirdi.Reference)
	assert.Empty(t, *svc.sonGirdi.Reference)
}

// TestSayiOlmayanSayfalamaParametresiReddedilir sessizce ilk sayfaya
// düşülmediğini doğrular.
func TestSayiOlmayanSayfalamaParametresiReddedilir(t *testing.T) {
	rec := istek(t, yeniRouter(&fakeDeliveries{}), "/admin/v1/notifications?limit=abc")

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

// TestServisHatasiStatusKoduSecmez handler'ın status kodunu SEÇMEDİĞİNİ,
// hata sınıfının çevirdiğini doğrular (plan Bölüm 2.7).
func TestServisHatasiStatusKoduSecmez(t *testing.T) {
	tests := map[string]struct {
		err  error
		kod  int
		kodu string
	}{
		"geçersiz süzgeç": {
			err: errors.Invalid("notification_invalid_input", "tanınmayan durum"),
			kod: http.StatusUnprocessableEntity,
		},
		"veritabanı yok": {
			err: errors.Unavailable("notification_query_failed", "havuz yok"),
			kod: http.StatusServiceUnavailable,
		},
	}

	for ad, tt := range tests {
		t.Run(ad, func(t *testing.T) {
			rec := istek(t, yeniRouter(&fakeDeliveries{err: tt.err}), "/admin/v1/notifications")

			assert.Equal(t, tt.kod, rec.Code)
		})
	}
}

// TestYetkisizKimlikReddedilir okuma yetkisi olmayan bir yönetim kimliğinin
// günlüğü göremediğini doğrular.
//
// Kimlik doğrulama yetkilendirmenin yerine geçseydi, yetkileri boşaltılmış bir
// kullanıcı da sipariş akışının zaman çizelgesini okuyabilirdi.
func TestYetkisizKimlikReddedilir(t *testing.T) {
	dar := corehttp.Principal{ID: "user_dar", Kind: "user", Scopes: []string{"product:read"}}

	rec := kimlikliIstek(t, yeniRouter(&fakeDeliveries{}), "/admin/v1/notifications", dar)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

// TestOkumaYetkisiYeter modülün kendi yetkisiyle erişilebildiğini doğrular;
// admin üst yetkisi ZORUNLU değildir.
func TestOkumaYetkisiYeter(t *testing.T) {
	dar := corehttp.Principal{ID: "user_okur", Kind: "user", Scopes: []string{api.ScopeRead}}

	rec := kimlikliIstek(t, yeniRouter(&fakeDeliveries{}), "/admin/v1/notifications", dar)

	assert.Equal(t, http.StatusOK, rec.Code)
}
