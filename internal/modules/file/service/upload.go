package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bdrtr/gobit/internal/core/errors"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/modules/file/models"
)

// ErrTooLarge gövdenin azami boyutu aştığını bildiren nöbetçi hatadır.
//
// Sağlayıcının okuma zincirinden geçip geri döneceği için nöbetçi olması
// gerekir: hata, sağlayıcının kendi sarmalayıcısının İÇİNDE gelir ve tek
// tanınma yolu errors.Is'tir. Dize karşılaştırmasıyla tanımak, mesaj her
// düzenlendiğinde sessizce bozulan bir bağ olurdu.
var ErrTooLarge = errors.Invalid(CodeTooLarge, "yükleme boyut sınırını aştı")

// UploadInput yeni bir yüklemenin girdisidir.
//
// # DOSYA ADI BURADA DA YOL DEĞİLDİR
//
// [UploadInput.OriginalName] yalnızca deftere yazılır ve yanıtta gösterilir;
// depo anahtarını sağlayıcı üretir ve ad hiçbir yol ifadesine girmez. Alanın
// varlığı bu yüzden bir risk değildir — riski yaratan şey adın YOL OLARAK
// KULLANILMASI olurdu ve o yol hiç yoktur.
type UploadInput struct {
	// ContentType dosyanın İÇERİĞİNDEN tespit edilmiş tipidir
	// ([net/http.DetectContentType]); istemcinin Content-Type başlığı ASLA
	// buraya yazılmaz.
	//
	// Tespitin ÇAĞIRANDA yapılmasının sebebi, tipi bilebilen tek yerin ilk
	// baytları okuyan katman olmasıdır (bkz. api paketi). Servis tespiti
	// tekrarlamaz, DENETLER.
	ContentType string
	// Body dosyanın gövdesidir ve AKIŞ olarak okunur; çağıran kapatmakla
	// yükümlüdür.
	Body io.Reader
	// OriginalName istemcinin bildirdiği dosya adıdır; boş olabilir.
	OriginalName string
	// UploadedBy yüklemeyi yapan çağıranın kimliğidir; boş olabilir.
	UploadedBy string
}

// Upload gövdeyi denetleyip depoya yazdırır ve deftere kaydeder.
//
// # Adımların SIRASI
//
//  1. İzin listesi — depoya TEK BAYT yazılmadan önce. Reddedilen bir dosyanın
//     temizlenmesi gerekmez; temizlik gerektiren her tasarımda o temizliğin
//     başarısız olduğu bir dal da vardır.
//  2. Sağlayıcıya yazma — gövde akış olarak geçer, belleğe alınmaz.
//  3. Deftere kayıt — dosya YAZILDIKTAN sonra. Ters sıra, kaydın işaret ettiği
//     dosyanın henüz var olmadığı bir pencere bırakırdı.
//
// Üçüncü adım patlarsa depoda kaydı olmayan bir dosya kalır ve o dosya
// TEMİZLENİR: erişilemez bir nesne, kimsenin anahtarını bilmediği için
// sonsuza kadar yer kaplardı.
func (s *Service) Upload(ctx context.Context, in UploadInput) (models.Upload, error) {
	if err := s.dogrula(in); err != nil {
		return models.Upload{}, err
	}

	prov, err := s.providers.Get(s.providerID)
	if err != nil {
		return models.Upload{}, err
	}

	// Zincir DIŞTAN İÇE: sınır önce uygulanır (sınırı aşan bayt özete bile
	// girmemelidir), özet sonra alınır, en dışta sağlayıcı okur.
	ozet := sha256.New()
	govde := io.TeeReader(&sinirliOkuyucu{r: in.Body, kalan: s.maxBytes + 1}, ozet)

	dosya, err := prov.Upload(ctx, coreprovider.UploadInput{
		ContentType: in.ContentType,
		Body:        govde,
	})
	if err != nil {
		// Sınır hatası sağlayıcının sarmalayıcısının içinde gelir; sınıfı
		// BURADA verilir çünkü sınırı bilen taraf burasıdır.
		if errors.Is(err, ErrTooLarge) {
			return models.Upload{}, errors.Invalid(CodeTooLarge,
				"dosya en fazla %d bayt olabilir", s.maxBytes)
		}

		return models.Upload{}, errors.Wrap(err, errors.KindOf(err), CodeUploadFailed,
			"dosya depoya yazılamadı")
	}

	kayit, err := s.store.CreateUpload(ctx, models.Upload{
		ID:           models.NewUploadID(time.Now()),
		StorageKey:   dosya.Key,
		ProviderID:   prov.ID(),
		ContentType:  dosya.ContentType,
		Size:         dosya.Size,
		Checksum:     hex.EncodeToString(ozet.Sum(nil)),
		OriginalName: in.OriginalName,
		URL:          dosya.URL,
		UploadedBy:   in.UploadedBy,
	})
	if err != nil {
		s.yaziliDosyayiTemizle(ctx, prov, dosya.Key)

		return models.Upload{}, err
	}

	s.log.DebugContext(ctx, "dosya yüklendi",
		"upload_id", kayit.ID,
		"saglayici", kayit.ProviderID,
		"icerik_tipi", kayit.ContentType,
		"boyut", kayit.Size)

	return kayit, nil
}

// dogrula girdinin depoya gitmeden önce geçmesi gereken denetimleri uygular.
func (s *Service) dogrula(in UploadInput) error {
	if in.Body == nil {
		return errors.Invalid(CodeInvalidInput, "yükleme gövdesi boş olamaz")
	}

	tip := strings.TrimSpace(in.ContentType)
	if tip == "" {
		return errors.Invalid(CodeInvalidInput, "içerik tipi tespit edilemedi")
	}
	if !slices.Contains(s.allowedTypes, tip) {
		// Reddedilen tip mesaja YAZILIR: yükleyen kişinin düzeltebileceği tek
		// şey odur ve "kabul edilmedi" demek, hangi dosyayı seçeceğini
		// söylemez. Değer istemciden gelmez, İÇERİKTEN tespit edilmiştir;
		// yani yanıta konan şey saldırganın seçtiği bir dize değildir.
		return errors.Invalid(CodeTypeNotAllowed,
			"%q içerik tipi kabul edilmiyor; kabul edilenler: %s",
			tip, strings.Join(s.allowedTypes, ", "))
	}

	// Ad UZUNLUĞU denetlenir ama İÇERİĞİ temizlenmez: ad hiçbir yol ifadesine
	// ve hiçbir HTTP başlığına girmez, yalnızca JSON gövdesinde döner.
	// Uzunluk sınırının sebebi de güvenlik değil, defterdir — megabaytlık bir
	// "dosya adı" satırı şişirirdi.
	if utf8.RuneCountInString(in.OriginalName) > models.MaxOriginalNameLen {
		return errors.Invalid(CodeInvalidInput,
			"dosya adı en fazla %d karakter olabilir", models.MaxOriginalNameLen)
	}

	return nil
}

// yaziliDosyayiTemizle kaydı açılamamış bir dosyayı depodan siler.
//
// Hata YUTULMAZ, LOGLANIR: çağırana dönecek olan asıl hata kayıt hatasıdır ve
// onu temizlik hatasıyla değiştirmek, arızanın sebebini gizlemek olurdu. Ama
// sessiz de kalınmaz — geride kalan dosyanın anahtarını bilen tek yer bu
// satırdır.
func (s *Service) yaziliDosyayiTemizle(ctx context.Context, prov coreprovider.FileProvider, key string) {
	// Bağlam iptal edilmiş olabilir (istemci bağlantıyı kapattı); temizlik
	// yine de denenmelidir, aksi hâlde iptal edilen her istek bir çöp dosya
	// bırakırdı.
	temizlikCtx := context.WithoutCancel(ctx)

	if err := prov.Delete(temizlikCtx, key); err != nil {
		s.log.ErrorContext(ctx, "kaydı açılamayan dosya depodan silinemedi",
			"error", err,
			"saglayici", prov.ID(),
			"depo_anahtari", key,
			"anlami", "depoda hiçbir kaydın işaret etmediği bir dosya kaldı; elle temizlenmeli")
	}
}

// sinirliOkuyucu okunan bayt sayısı sınırı AŞTIĞINDA [ErrTooLarge] döner.
//
// [io.LimitReader] burada YANLIŞ olurdu: o, sınıra gelince io.EOF döner, yani
// gövdeyi sessizce KESER. Sağlayıcı bunu "dosya bitti" diye okur ve yarım bir
// görsel başarıyla kaydedilir — sınırı aşan istek reddedilmek yerine bozuk
// veri üretirdi.
//
// kalan, sınırdan BİR FAZLA başlatılır: tam sınır kadar bayt taşıyan bir gövde
// geçmeli, bir bayt fazlası reddedilmelidir. Sınır kadar başlatılsaydı, tam
// sınırdaki dosya için ek bir okuma denemesi hata üretirdi.
type sinirliOkuyucu struct {
	r     io.Reader
	kalan int64
}

// Read io.Reader'ı karşılar.
func (s *sinirliOkuyucu) Read(p []byte) (int, error) {
	if s.kalan <= 0 {
		return 0, ErrTooLarge
	}

	if int64(len(p)) > s.kalan {
		p = p[:s.kalan]
	}

	n, err := s.r.Read(p)
	s.kalan -= int64(n)

	return n, err
}
