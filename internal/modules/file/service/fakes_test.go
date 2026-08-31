package service_test

import (
	"context"
	"io"
	"strings"
	"sync"
	"time"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/modules/file/models"
	"github.com/bdrtr/gobit/internal/modules/file/service"
)

// Sahteler, servisin KARARLARINI gerçek bir disk ve gerçek bir veritabanı
// olmadan sınamak içindir. Sınanan şey servisin ne yaptığı değil, NEYE KARAR
// VERDİĞİDİR: hangi sırayla çağırdığı, neyi reddettiği, neyi temizlediği.

// sahteSaglayici yüklenenleri kaydeden bir dosya sağlayıcısıdır.
type sahteSaglayici struct {
	mu sync.Mutex

	// id sağlayıcının kimliğidir.
	id string
	// yuklenen okunmuş gövdelerdir.
	yuklenen []string
	// silinen Delete çağrılarının anahtarlarıdır; SIRA korunur.
	silinen []string
	// yuklemeHatasi verilirse Upload bu hatayı döner.
	yuklemeHatasi error
	// silmeHatasi verilirse Delete bu hatayı döner.
	silmeHatasi error
}

// sahteSaglayici'nın çekirdek sözleşmesini karşıladığı derleme zamanında
// sabitlenir.
var _ coreprovider.FileProvider = (*sahteSaglayici)(nil)

// ID sağlayıcının kimliğini döner.
func (p *sahteSaglayici) ID() string {
	if p.id == "" {
		return "sahte"
	}

	return p.id
}

// Upload gövdeyi belleğe okur ve sahte bir dosya kaydı döner.
//
// Gövde GERÇEKTEN okunur: servisin özet ve boyut sınırı zincirleri ancak
// baytlar akarsa çalışır ve okumayan bir sahte, o zincirleri sınanamaz
// kılardı.
func (p *sahteSaglayici) Upload(_ context.Context, in coreprovider.UploadInput) (coreprovider.File, error) {
	if p.yuklemeHatasi != nil {
		return coreprovider.File{}, p.yuklemeHatasi
	}

	ham, err := io.ReadAll(in.Body)
	if err != nil {
		return coreprovider.File{}, coreerrors.Wrap(err, coreerrors.KindInternal, "sahte_yazma",
			"sahte sağlayıcı gövdeyi okuyamadı")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.yuklenen = append(p.yuklenen, string(ham))

	return coreprovider.File{
		Key:         "ANAHTAR" + string(rune('0'+len(p.yuklenen))) + ".png",
		URL:         "/files/ANAHTAR" + string(rune('0'+len(p.yuklenen))) + ".png",
		ContentType: in.ContentType,
		Size:        int64(len(ham)),
	}, nil
}

// Delete silinen anahtarı kaydeder.
func (p *sahteSaglayici) Delete(_ context.Context, key string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.silinen = append(p.silinen, key)

	return p.silmeHatasi
}

// silinenler kaydedilen silme anahtarlarını döner.
func (p *sahteSaglayici) silinenler() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return append([]string(nil), p.silinen...)
}

// sahteAcilabilirSaglayici okuma yüzeyini de karşılayan sahtedir.
//
// AYRI bir tip olması bilinçlidir: servis, sağlayıcının okumayı destekleyip
// desteklemediğini TİP İDDİASIYLA anlar ve iki ayrı sahte olmadan o dalın iki
// yönü de sınanamazdı.
type sahteAcilabilirSaglayici struct {
	*sahteSaglayici

	// icerik Open'ın döndüreceği gövdedir.
	icerik string
}

// Open dosyayı okumak üzere açar.
func (p *sahteAcilabilirSaglayici) Open(
	_ context.Context, _ string,
) (io.ReadSeekCloser, time.Time, error) {
	return nopKapatici{strings.NewReader(p.icerik)}, time.Unix(0, 0).UTC(), nil
}

// nopKapatici bir okuyucuya boş bir Close ekler.
type nopKapatici struct {
	*strings.Reader
}

// Close io.Closer'ı karşılar ve hiçbir şey yapmaz.
func (nopKapatici) Close() error { return nil }

// sahteDepo yükleme defterinin bellek içi karşılığıdır.
type sahteDepo struct {
	mu sync.Mutex

	// kayitlar kimliğe göre yüklemelerdir.
	kayitlar map[string]models.Upload
	// sira eklenme sırasıdır; listeleme bunu kullanır.
	sira []string
	// yazmaHatasi verilirse CreateUpload bu hatayı döner.
	yazmaHatasi error
	// silmeHatasi verilirse DeleteUpload bu hatayı döner.
	silmeHatasi error
}

// sahteDepo'nun servisin beklediği yüzeyi karşıladığı derleme zamanında
// sabitlenir.
var _ service.Store = (*sahteDepo)(nil)

// yeniSahteDepo boş bir sahte depo üretir.
func yeniSahteDepo() *sahteDepo {
	return &sahteDepo{kayitlar: make(map[string]models.Upload)}
}

// CreateUpload kaydı belleğe yazar.
func (s *sahteDepo) CreateUpload(_ context.Context, u models.Upload) (models.Upload, error) {
	if s.yazmaHatasi != nil {
		return models.Upload{}, s.yazmaHatasi
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	u.CreatedAt = time.Unix(0, 0).UTC()
	u.UpdatedAt = u.CreatedAt
	s.kayitlar[u.ID] = u
	s.sira = append(s.sira, u.ID)

	return u, nil
}

// GetUpload kaydı kimliğiyle döner.
func (s *sahteDepo) GetUpload(_ context.Context, id string) (models.Upload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.kayitlar[id]
	if !ok {
		return models.Upload{}, coreerrors.NotFound("file_upload_not_found",
			"yükleme bulunamadı (kimlik: %s)", id)
	}

	return u, nil
}

// GetUploadByKey kaydı depo anahtarıyla döner.
func (s *sahteDepo) GetUploadByKey(_ context.Context, key string) (models.Upload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, id := range s.sira {
		if s.kayitlar[id].StorageKey == key {
			return s.kayitlar[id], nil
		}
	}

	return models.Upload{}, coreerrors.NotFound("file_upload_not_found",
		"yükleme bulunamadı (anahtar: %s)", key)
}

// ListUploads kayıtları sayfalar.
func (s *sahteDepo) ListUploads(
	_ context.Context, filter models.UploadFilter,
) ([]models.Upload, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]models.Upload, 0, len(s.sira))
	for i, id := range s.sira {
		if int64(i) < filter.Offset || int64(len(out)) >= filter.Limit {
			continue
		}
		out = append(out, s.kayitlar[id])
	}

	return out, int64(len(s.sira)), nil
}

// DeleteUpload kaydı siler; olmayan kimlik hata değildir.
func (s *sahteDepo) DeleteUpload(_ context.Context, id string) (bool, error) {
	if s.silmeHatasi != nil {
		return false, s.silmeHatasi
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.kayitlar[id]; !ok {
		return false, nil
	}

	delete(s.kayitlar, id)
	s.sira = slicesSil(s.sira, id)

	return true, nil
}

// sayi defterdeki kayıt sayısını döner.
func (s *sahteDepo) sayi() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.kayitlar)
}

// slicesSil dilimden bir değeri çıkarır.
func slicesSil(dilim []string, deger string) []string {
	out := dilim[:0]
	for _, v := range dilim {
		if v != deger {
			out = append(out, v)
		}
	}

	return out
}

// yeniModelKaydi belirli bir sağlayıcıya ait sahte bir yükleme kaydı üretir.
//
// Doğrudan depoya yazılır: amaç, servisin YÜKLEMEDİĞİ (yani başka bir
// yapılandırmada oluşmuş) bir kaydın nasıl ele alındığını sınamaktır.
func yeniModelKaydi(saglayici string) models.Upload {
	return models.Upload{
		ID:          "upl_ESKI",
		StorageKey:  "ESKI_ANAHTAR.png",
		ProviderID:  saglayici,
		ContentType: coreprovider.ContentTypePNG,
		Size:        5,
		Checksum:    "abc",
		URL:         "/files/ESKI_ANAHTAR.png",
	}
}
