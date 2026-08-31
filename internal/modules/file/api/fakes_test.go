package api_test

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/file/api"
	"github.com/bdrtr/gobit/internal/modules/file/models"
	"github.com/bdrtr/gobit/internal/modules/file/service"
)

// testAzamiBoyut handler testlerinde kullanılan boyut sınırıdır.
const testAzamiBoyut int64 = 64

// sahteYuklemeler api.Uploads'ın senaryolanabilir karşılığıdır.
//
// GERÇEK bir servis kullanılmaz: sınanan şey handler'ın kararlarıdır —
// multipart ayrıştırma, içerik tipi tespiti, sunum başlıkları, status
// eşlemesi. Servisin kendi kararları (izin listesi, boyut, silme sırası) kendi
// paketinde sınanır ve burada tekrarlanmaları, aynı iddiayı iki yerde tutmak
// olurdu.
type sahteYuklemeler struct {
	mu sync.Mutex

	// gorulenTipler Upload'a gelen içerik tipleridir; tespitin doğruluğu
	// bununla kanıtlanır.
	gorulenTipler []string
	// gorulenAdlar Upload'a gelen istemci dosya adlarıdır.
	gorulenAdlar []string
	// gorulenGovdeler Upload'a AKAN baytlardır.
	gorulenGovdeler []string
	// silinenler DeleteUpload çağrılarının kimlikleridir.
	silinenler []string

	// yuklemeHatasi verilirse Upload bu hatayı döner.
	yuklemeHatasi error
	// silmeHatasi verilirse DeleteUpload bu hatayı döner.
	silmeHatasi error
	// acilan OpenByKey'in döneceği dosyadır.
	acilan service.OpenedFile
	// acmaHatasi verilirse OpenByKey bu hatayı döner.
	acmaHatasi error
	// kayitlar ListUploads'ın döneceği kayıtlardır.
	kayitlar []models.Upload
}

// Sahtenin handler'ın beklediği yüzeyi karşıladığı derleme zamanında
// sabitlenir.
var _ api.Uploads = (*sahteYuklemeler)(nil)

// Upload gövdeyi okur ve sahte bir kayıt döner.
//
// Gövde GERÇEKTEN okunur: handler'ın ilk 512 baytı okuyup akışın önüne geri
// koyması ancak baytlar sonuna kadar akarsa doğrulanabilir.
func (f *sahteYuklemeler) Upload(_ context.Context, in service.UploadInput) (models.Upload, error) {
	ham, err := io.ReadAll(in.Body)
	if err != nil {
		return models.Upload{}, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.gorulenTipler = append(f.gorulenTipler, in.ContentType)
	f.gorulenAdlar = append(f.gorulenAdlar, in.OriginalName)
	f.gorulenGovdeler = append(f.gorulenGovdeler, string(ham))

	if f.yuklemeHatasi != nil {
		return models.Upload{}, f.yuklemeHatasi
	}

	return models.Upload{
		ID:           "upl_TEST",
		StorageKey:   "URETILENANAHTAR0123456789.png",
		ProviderID:   "local",
		ContentType:  in.ContentType,
		Size:         int64(len(ham)),
		Checksum:     "ozet",
		OriginalName: in.OriginalName,
		URL:          "/files/URETILENANAHTAR0123456789.png",
		UploadedBy:   in.UploadedBy,
		CreatedAt:    time.Unix(0, 0).UTC(),
	}, nil
}

// ListUploads sahte kayıtları döner.
func (f *sahteYuklemeler) ListUploads(
	_ context.Context, _ service.Page,
) ([]models.Upload, int64, error) {
	return f.kayitlar, int64(len(f.kayitlar)), nil
}

// DeleteUpload silme çağrısını kaydeder.
func (f *sahteYuklemeler) DeleteUpload(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.silinenler = append(f.silinenler, id)

	return f.silmeHatasi
}

// OpenByKey sahte dosyayı döner.
func (f *sahteYuklemeler) OpenByKey(_ context.Context, _ string) (service.OpenedFile, error) {
	if f.acmaHatasi != nil {
		return service.OpenedFile{}, f.acmaHatasi
	}

	return f.acilan, nil
}

// MaxUploadBytes boyut sınırını döner.
func (f *sahteYuklemeler) MaxUploadBytes() int64 { return testAzamiBoyut }

// tipler Upload'a gelen içerik tiplerini döner.
func (f *sahteYuklemeler) tipler() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.gorulenTipler...)
}

// adlar Upload'a gelen istemci dosya adlarını döner.
func (f *sahteYuklemeler) adlar() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.gorulenAdlar...)
}

// govdeler Upload'a akan baytları döner.
func (f *sahteYuklemeler) govdeler() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.gorulenGovdeler...)
}

// nopKapatici bir okuyucuya boş bir Close ekler.
type nopKapatici struct {
	*strings.Reader
}

// Close io.Closer'ı karşılar ve hiçbir şey yapmaz.
func (nopKapatici) Close() error { return nil }

// acilanDosya verilen tip ve içerikle sunulmaya hazır bir dosya üretir.
func acilanDosya(tip, icerik string) service.OpenedFile {
	return service.OpenedFile{
		Upload:  models.Upload{ContentType: tip, StorageKey: "K.png"},
		Content: nopKapatici{strings.NewReader(icerik)},
		ModTime: time.Unix(1_700_000_000, 0).UTC(),
	}
}

// yeniRouter sahte servis üzerinde çalışan bir router kurar.
func yeniRouter(svc *sahteYuklemeler) chi.Router {
	r := chi.NewRouter()
	api.New(svc).Routes(r)

	return r
}

// yonetici testlerin varsayılan kimliğidir: tam yetkili bir yönetim
// kullanıcısı.
//
// Router burada DOĞRUDAN kuruluyor, yani corehttp.RequireAdmin zincirde yok ve
// context'e kimliği koyan kimse yok; yönetim uçları corehttp.RequireScope ile
// korunduğu için kimliksiz istek 401 döner ve testin asıl doğruladığı
// davranışa sıra gelmezdi.
func yonetici() corehttp.Principal {
	return corehttp.Principal{ID: "user_test", Kind: "user", Scopes: []string{corehttp.ScopeAdmin}}
}

// multipartGovde tek dosyalı bir multipart gövdesi kurar.
//
// Parçanın Content-Type'ı ÇAĞIRANDAN gelir ve bilerek yalan söyleyebilir:
// testlerin çoğunun sınadığı şey tam olarak o iddianın yok sayılmasıdır.
func multipartGovde(
	t *testing.T, alan, dosyaAdi, bildirilenTip, icerik string,
) (govde io.Reader, icerikTipi string) {
	t.Helper()

	var buf bytes.Buffer
	yazici := multipart.NewWriter(&buf)

	basliklar := make(textproto.MIMEHeader)
	basliklar.Set("Content-Disposition",
		`form-data; name="`+alan+`"; filename="`+dosyaAdi+`"`)
	if bildirilenTip != "" {
		basliklar.Set("Content-Type", bildirilenTip)
	}

	parca, err := yazici.CreatePart(basliklar)
	require.NoError(t, err)

	_, err = parca.Write([]byte(icerik))
	require.NoError(t, err)
	require.NoError(t, yazici.Close())

	return &buf, yazici.FormDataContentType()
}

// yukle verilen gövdeyle yükleme isteği yapar.
func yukle(t *testing.T, r chi.Router, govde io.Reader, icerikTipi string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/admin/v1/uploads", govde)
	req.Header.Set("Content-Type", icerikTipi)
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), yonetici()))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// istek kimlikli bir istek yapar.
func istek(t *testing.T, r chi.Router, metot, yol string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(metot, yol, http.NoBody)
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), yonetici()))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// hataKodu yanıt gövdesindeki makine kodunu döner.
func hataKodu(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	var govde struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, jsonCoz(rec.Body.Bytes(), &govde), "gövde: %s", rec.Body.String())

	return govde.Error.Code
}

// bulunamadi tipli bir NotFound hatası üretir.
func bulunamadi() error {
	return coreerrors.NotFound("file_upload_not_found", "yükleme bulunamadı")
}
