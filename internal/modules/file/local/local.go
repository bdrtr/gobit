// Package local dosyaları YEREL DİSKTE, yapılandırılan bir kök dizinde
// saklayan varsayılan dosya sağlayıcısıdır (plan Bölüm 5.6).
//
// [Provider], internal/core/provider'daki FileProvider sözleşmesini karşılar ve
// kutudan çıkan tek sağlayıcıdır: gobit bir çerçevedir ve hangi nesne
// deposunun kullanılacağını bilemez, ama yükleme yolunun ayakta olduğunu
// göstermek zorundadır.
//
// # Depo anahtarını SAĞLAYICI üretir
//
// [Provider.Upload]'ın girdisinde dosya adı YOKTUR (çekirdek sözleşmesinin
// kararı). Anahtar burada üretilir: zaman sıralı bir kimlik + tespit edilen
// içerik tipinden türeyen uzantı. İstemciden gelen hiçbir dize bir yol
// bileşenine dönüşmediği için "../" ile kök dışına yazmak YAPISAL olarak
// imkânsızdır — bir "temizleme" adımının doğru çalışmasına bağlı değildir.
//
// Anahtar tek düzlemdedir (alt dizin yoktur) ve bu, sunum yolundaki iddiayı
// da basitleştirir: geçerli bir anahtarda yol ayıracı HİÇ bulunmaz, yani
// anahtar ile kökün birleşimi kökün altından çıkamaz. Bilinen sınırı da
// yazalım: tek dizinde milyonlarca girdi, dosya sisteminin dizin taramasını
// yavaşlatır. O noktada doğru cevap alt dizinlere bölmek değil, yerel diski
// bırakıp bir nesne deposuna geçmektir.
//
// # Yazma ATOMİKTİR
//
// Dosya önce aynı dizinde geçici bir ada yazılır, fsync edilir ve ancak sonra
// nihai adına taşınır ([os.Rename]). Doğrudan nihai ada yazmak, yarım yazılmış
// bir dosyanın o an sunulabilmesi demekti: tarayıcı bozuk bir görsel gösterir
// ve dosya "var" olduğu için hiçbir yeniden deneme onu düzeltmez.
//
// Geçici dosyanın AYNI DİZİNDE açılması zorunludur: [os.Rename] yalnızca aynı
// dosya sistemi içinde atomiktir. Geçici dosya /tmp'de açılsaydı taşıma
// çoğu kurulumda EXDEV ile başarısız olur, olduğu yerde ise atomiklik
// kaybolurdu.
package local

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/modules/file/models"
)

// ID sağlayıcının kimliğidir; FILE_PROVIDER varsayılanı budur.
const ID = "local"

// DefaultURLPrefix üretilen adreslerin varsayılan yol önekidir.
//
// Önek /admin/v1 ya da /store/v1 ALTINDA DEĞİLDİR ve olamaz; gerekçesi
// internal/modules/file/api paketinin sunum bölümündedir (özet: vitrindeki
// <img> etiketi başlık gönderemez).
const DefaultURLPrefix = "/files"

// Hata kodları.
const (
	// CodeNotReady sağlayıcının kök dizin olmadan kurulduğunu bildirir.
	CodeNotReady = "file_local_not_ready"
	// CodeRootUnusable kök dizinin açılamadığını ya da yazılamadığını bildirir.
	CodeRootUnusable = "file_local_root_unusable"
	// CodeWriteFailed dosyanın diske yazılamadığını bildirir.
	CodeWriteFailed = "file_local_write_failed"
	// CodeInvalidKey depo anahtarının bu sağlayıcının ürettiği biçimde
	// olmadığını bildirir.
	CodeInvalidKey = "file_local_invalid_key"
	// CodeReadFailed dosyanın okunamadığını bildirir.
	CodeReadFailed = "file_local_read_failed"
)

// gecicOnek yarım yazılan dosyaların geçici ad önekidir.
//
// Nokta ile başlar ve geçerli bir anahtarda nokta ile başlayan bir gövde
// BULUNAMAZ ([anahtarGecerli]); yani bir çökme sonrası ortada kalan geçici
// dosya hiçbir zaman sunulabilir bir anahtarla eşleşmez.
const gecicOnek = ".yukleniyor-"

// dizinIzni kök dizin yaratılırken kullanılan izindir.
const dizinIzni os.FileMode = 0o750

// uzantilar tespit edilen içerik tipinden dosya uzantısına eşlemedir.
//
// Uzantı yalnızca İNSAN ve ARAÇ kolaylığıdır: kök dizini elle inceleyen ya da
// yedekleyen kişi dosyanın ne olduğunu ondan anlar. Sunum kararı ona
// BAKMAZ — Content-Type kayıttaki tespit edilmiş tipten yazılır. Bu yüzden
// tanınmayan bir tip için uzantının ".bin" olması da zararsızdır.
var uzantilar = map[string]string{
	coreprovider.ContentTypeJPEG: ".jpg",
	coreprovider.ContentTypePNG:  ".png",
	coreprovider.ContentTypeGIF:  ".gif",
	coreprovider.ContentTypeWebP: ".webp",
}

// varsayilanUzanti eşlemede bulunmayan tipler için kullanılır.
const varsayilanUzanti = ".bin"

// Options sağlayıcının kurulum ayarlarıdır.
type Options struct {
	// Root dosyaların yazılacağı kök dizindir; zorunludur.
	Root string
	// URLPrefix üretilen adreslerin yol önekidir; boşsa [DefaultURLPrefix].
	URLPrefix string
	// Logger temizlik uyarılarının yazıldığı hedeftir; nil ise loglar atılır.
	//
	// Yalnızca ELDE KALAN geçici dosyayı bildirmek için vardır: o dosyanın
	// hiçbir veritabanı kaydı yoktur, dolayısıyla loglanmazsa varlığı hiçbir
	// yerden anlaşılamaz ve disk sessizce dolar.
	Logger *slog.Logger
}

// Provider dosyaları yerel diske yazan sağlayıcıdır.
// Eşzamanlı kullanıma güvenlidir: durumu yalnızca değişmeyen ayarlardır.
type Provider struct {
	root      string
	urlPrefix string
	log       *slog.Logger
}

// Provider'ın çekirdek sözleşmesini karşıladığı derleme zamanında doğrulanır;
// imza kayması çalışma zamanına kalmaz.
var _ coreprovider.FileProvider = (*Provider)(nil)

// New verilen kök dizin üzerinde çalışan bir sağlayıcı üretir.
//
// Dizin BURADA yaratılır ve yaratılamazsa hata döner. Kurulum anında
// denenmesi bilinçlidir: yazılamayan bir kök, ilk yüklemeye kadar
// beklerse arıza müşteri karşısında ortaya çıkar — oysa yanlış yazılmış bir
// yol ya da eksik bir bağlama noktası, açılışta düzeltilebilecek bir
// yapılandırma hatasıdır.
//
// Boş kök REDDEDİLİR ve GEÇİCİ DİZİNE düşülmez. Geçici dizin cazip olurdu
// ("hiçbir şey yapılandırmadan çalışsın") ama yeniden başlatmada görselleri
// sessizce kaybettirirdi: adres ürün kaydında kalıcı olarak durur, dosya ise
// gitmiştir ve hiçbir hata görünmez. Sessiz veri kaybı, açılışta patlayan bir
// yapılandırma hatasından her zaman pahalıdır.
func New(opts Options) (*Provider, error) {
	if opts.Root == "" {
		return nil, coreerrors.Internal(CodeNotReady,
			"%q dosya sağlayıcısı kök dizin olmadan kurulamaz", ID)
	}

	if err := os.MkdirAll(opts.Root, dizinIzni); err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindUnavailable, CodeRootUnusable,
			"dosya kök dizini hazırlanamadı: %s", opts.Root)
	}

	prefix := opts.URLPrefix
	if prefix == "" {
		prefix = DefaultURLPrefix
	}

	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &Provider{root: opts.Root, urlPrefix: prefix, log: log}, nil
}

// ID sağlayıcının kimliğini döner.
func (p *Provider) ID() string { return ID }

// Root sağlayıcının kök dizinini döner; loglama ve teşhis içindir.
func (p *Provider) Root() string { return p.root }

// Upload gövdeyi diske yazar ve erişilebilir adresi döner.
//
// Anahtar burada üretilir; girdide dosya adı yoktur (bkz. paket belgesi).
//
// Okuma yarıda hata verirse — boyut sınırını aşan bir gövde tam da böyle
// kesilir — geçici dosya SİLİNİR ve hata döner. Temizlik çekirdek
// sözleşmesinin şartıdır: yarım nesne, hiçbir kaydın işaret etmediği ve
// hiçbir silme yolunun anahtarını bilmediği bir dosyadır, yani sınırı aşan
// istekler diski yine de doldurabilirdi.
func (p *Provider) Upload(ctx context.Context, in coreprovider.UploadInput) (coreprovider.File, error) {
	if in.Body == nil {
		return coreprovider.File{}, coreerrors.Internal(CodeWriteFailed,
			"yükleme gövdesi nil olamaz")
	}
	// Bağlam iptal edilmişse tek bayt yazmadan dönülür. io.Copy ctx'i
	// GÖRMEZ; dosya sistemi çağrıları bloklamaz ama istemci çoktan gitmişken
	// diske yazmanın da bir karşılığı yoktur.
	if err := ctx.Err(); err != nil {
		return coreprovider.File{}, coreerrors.Wrap(err, coreerrors.KindUnavailable, CodeWriteFailed,
			"yükleme başlamadan iptal edildi")
	}

	key := yeniAnahtar(in.ContentType, time.Now())

	gecici, err := os.CreateTemp(p.root, gecicOnek+"*")
	if err != nil {
		return coreprovider.File{}, coreerrors.Wrap(err, coreerrors.KindUnavailable, CodeRootUnusable,
			"geçici dosya açılamadı: %s", p.root)
	}
	gecicYol := gecici.Name()

	// Temizlik DEFER EDİLİR, hata dallarına serpiştirilmez.
	//
	// Bugünkü dallar tamdır ama iki durum onların dışında kalır: aradaki bir
	// PANİK (Recoverer onu 500'e çevirir ve istek biter) ve ileride eklenecek
	// herhangi bir erken return. İkisinde de geride yarım bir ".yukleniyor"
	// dosyası kalır; disk sessizce dolar ve kimse fark etmez çünkü o dosyanın
	// hiçbir kaydı yoktur. Defer, temizliği yeni bir dalın hatırlanmasına
	// bağlı olmaktan çıkarır.
	//
	// Temizlik hatası YUTULMAZ ama asıl hatayı da DEĞİŞTİRMEZ: loglanır.
	// Değiştirseydi, okuma hatasının teşhisi silinirdi.
	tasindi := false

	defer func() {
		if tasindi {
			return
		}

		if err := os.Remove(gecicYol); err != nil && !errors.Is(err, os.ErrNotExist) {
			p.log.Warn("geçici yükleme dosyası silinemedi, elle temizlenmeli",
				"yol", gecicYol, "error", err)
		}
	}()

	yazilan, err := yazVeKapat(gecici, in.Body)
	if err != nil {
		return coreprovider.File{}, err
	}

	if err := os.Rename(gecicYol, filepath.Join(p.root, key)); err != nil {
		return coreprovider.File{}, coreerrors.Wrap(err, coreerrors.KindUnavailable, CodeWriteFailed,
			"dosya nihai adına taşınamadı: %s", key)
	}

	tasindi = true

	return coreprovider.File{
		Key:         key,
		URL:         p.urlPrefix + "/" + key,
		ContentType: in.ContentType,
		Size:        yazilan,
	}, nil
}

// Delete dosyayı diskten siler. İDEMPOTENTTİR: olmayan bir anahtar hata
// değildir.
//
// GEÇERSİZ bir anahtar da hata değildir ve bu bilinçlidir: böyle bir anahtarla
// yazılmış bir dosya hiç var olamaz, dolayısıyla "silinmiş olma" son durumu
// zaten sağlanmıştır. Hata dönmek, silme akışını düzeltilemeyecek bir şey
// yüzünden sonsuza kadar tekrar ettirirdi.
func (p *Provider) Delete(_ context.Context, key string) error {
	if !anahtarGecerli(key) {
		return nil
	}

	if err := os.Remove(filepath.Join(p.root, key)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return coreerrors.Wrap(err, coreerrors.KindUnavailable, CodeWriteFailed,
			"dosya silinemedi: %s", key)
	}

	return nil
}

// Open dosyayı okumak üzere açar ve son değişiklik zamanını döner.
//
// Çekirdek sözleşmesinde ([coreprovider.FileProvider]) yoktur ve olmamalıdır:
// dosyayı sunmak sağlayıcının işi değildir ve bir nesne deposu sunumu CDN'e
// bırakır. Yerel diskte ise sunacak başka kimse yoktur, bu yüzden bu sağlayıcı
// sözleşmenin ÜSTÜNE bir metot ekler; HTTP katmanı onu kendi tanımladığı dar
// arayüzle arar (ADR 0001'in tüketici tarafı örüntüsü).
//
// # Anahtar biçimi DOĞRULANIR
//
// Değer normal akışta veritabanındaki kayıttan gelir, yani zaten bu
// sağlayıcının ürettiği bir anahtardır. Doğrulama yine de yapılır ve bir
// "temizleme" DEĞİLDİR: bozuk bir anahtar düzeltilmez, REDDEDİLİR. Böylece
// çağıranı ne olursa olsun — bugünkü kayıt yolu ya da yarın yazılacak başka
// bir yol — kök dizinin dışına çıkan bir yol ifadesi hiç kurulamaz.
func (p *Provider) Open(_ context.Context, key string) (io.ReadSeekCloser, time.Time, error) {
	if !anahtarGecerli(key) {
		return nil, time.Time{}, coreerrors.Invalid(CodeInvalidKey,
			"geçersiz depo anahtarı: %q", key)
	}

	// G304 bastırması bilinçlidir ve tam olarak yukarıdaki denetime dayanır:
	// key, [anahtarGecerli]'den geçmiştir ve kabul edilen alfabede yol
	// ayıracı, nokta-nokta ve NUL YOKTUR — yani birleşim kökün altından
	// çıkamaz. Denetimi kaldıran biri bu satırı da gözden geçirmek
	// zorundadır; bu yorum o bağın kendisidir.
	f, err := os.Open(filepath.Join(p.root, key)) //nolint:gosec // G304: anahtar biçimi doğrulandı, kök dışına çıkamaz
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, time.Time{}, coreerrors.NotFound(CodeReadFailed,
				"dosya depoda bulunamadı: %s", key)
		}

		return nil, time.Time{}, coreerrors.Wrap(err, coreerrors.KindUnavailable, CodeReadFailed,
			"dosya açılamadı: %s", key)
	}

	bilgi, err := f.Stat()
	if err != nil {
		_ = f.Close()

		return nil, time.Time{}, coreerrors.Wrap(err, coreerrors.KindUnavailable, CodeReadFailed,
			"dosya bilgisi okunamadı: %s", key)
	}

	return f, bilgi.ModTime(), nil
}

// yazVeKapat gövdeyi geçici dosyaya yazar, diske işler ve kapatır.
//
// [os.File.Sync] çağrısı bilinçlidir: taşıma (rename) dosya adını atomik
// yapar ama İÇERİĞİN diske ulaştığını garanti etmez. Sync olmadan, taşımadan
// hemen sonra düşen bir makinede nihai adla duran ama içi boş bir dosya
// kalabilirdi — yani tam olarak kaçınmak istediğimiz "bozuk görsel".
func yazVeKapat(f *os.File, body io.Reader) (int64, error) {
	yazilan, copyErr := io.Copy(f, body)

	if copyErr == nil {
		copyErr = f.Sync()
	}

	// Kapatma her durumda denenir; açık bir dosya tanıtıcısı sızdırmak, hata
	// yolunda da kabul edilemez.
	if closeErr := f.Close(); copyErr == nil {
		copyErr = closeErr
	}

	if copyErr != nil {
		// Sarmalama zinciri KORUR: çağıran (servis) boyut sınırı hatasını
		// errors.Is ile bu zincirin içinde arar ve onu istemci hatasına
		// çevirir. Burada sınıflandırmak, sağlayıcının bilmediği bir kararı
		// vermek olurdu.
		return 0, coreerrors.Wrap(copyErr, coreerrors.KindInternal, CodeWriteFailed,
			"dosya diske yazılamadı")
	}

	return yazilan, nil
}

// yeniAnahtar tespit edilen içerik tipinden bir depo anahtarı üretir.
func yeniAnahtar(contentType string, t time.Time) string {
	uzanti, bilinen := uzantilar[contentType]
	if !bilinen {
		uzanti = varsayilanUzanti
	}

	// Önek BOŞ verilir: anahtar bir kayıt kimliği değildir ve "upl_" öneki
	// taşısaydı, iki farklı şey (kayıt kimliği ve depo anahtarı) logda ve
	// adres çubuğunda birbirine karışırdı.
	return models.NewID("", t) + uzanti
}

// anahtarGecerli değerin bu sağlayıcının ürettiği biçimde olduğunu bildirir.
//
// Biçim: 26 karakterlik Crockford Base32 gövde + nokta + küçük harf/rakam
// uzantı. Kabul edilen alfabede yol ayıracı, nokta-nokta ve NUL YOKTUR; yani
// geçerli sayılan bir anahtar kök dizinin dışını gösteremez.
//
// Denetim ALFABE üzerinden yapılır, "yasak dizi arama" ile değil: "../" aramak,
// her yeni kodlama numarası (%2e%2e, ters bölü, gömülü NUL, Unicode
// normalizasyon) için listeye bir satır daha eklemek demekti ve doğruluğu,
// listeyi yazanın o gün kaç numara hatırladığına bağlı olurdu. İzin verilen
// alfabenin dışındaki her şeyi reddetmenin böyle bir borcu yoktur.
func anahtarGecerli(key string) bool {
	govde, uzanti, bulundu := kes(key)
	if !bulundu || len(govde) != models.IDBodyLength() || uzanti == "" {
		return false
	}

	for _, r := range govde {
		// Crockford Base32 alfabesi: 0-9 ve I, L, O, U dışındaki büyük
		// harfler. Alfabenin tamamını burada tekrar etmek yerine sınıf
		// denetimi yapılır; amaç anahtarı çözmek değil, yol üretebilecek
		// hiçbir karakterin geçmediğini garanti etmektir.
		if (r < '0' || r > '9') && (r < 'A' || r > 'Z') {
			return false
		}
	}

	for _, r := range uzanti {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}

	return true
}

// kes anahtarı gövde ve uzantı olarak ayırır; nokta yoksa bulundu false olur.
//
// Ayrım SONDAKİ noktaya göre değil, TEK noktaya göre yapılır: birden çok nokta
// taşıyan bir değer bu sağlayıcının ürettiği bir anahtar değildir ve
// ayrıştırmaya çalışmak yerine reddedilmelidir.
func kes(key string) (govde, uzanti string, bulundu bool) {
	nokta := -1
	for i, r := range key {
		if r != '.' {
			continue
		}
		if nokta >= 0 {
			return "", "", false
		}
		nokta = i
	}

	if nokta < 0 {
		return "", "", false
	}

	return key[:nokta], key[nokta+1:], true
}
