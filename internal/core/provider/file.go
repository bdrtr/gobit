package provider

import (
	"context"
	"io"
)

// Yüklemenin varsayılan olarak kabul ettiği içerik tipleri; izin listesi
// (allow-list) bunlardan ibarettir ve [UploadInput.ContentType] bu değerlerden
// birini taşır.
//
// Sabitler çekirdektedir çünkü değeri hem yüklemeyi doğrulayan taraf hem
// dosyayı sunan taraf yazar ve ikisi birbirini import EDEMEZ (Prensip 2.4).
//
// # Neden izin listesi, yasak listesi değil
//
// Yasak listesi, listelenmemiş HER tipi varsayılan olarak KABUL eder: bugün
// akla gelmemiş tek bir biçim (bir belge, bir arşiv, bir betik) sessizce
// depoya girer ve listeyi yazan kişi her yeni biçimi önceden tahmin etmek
// zorunda kalır. İzin listesinde bilinmeyen tip REDDEDİLİR; yeni bir tipi
// kabul etmek, unutulabilecek bir eksiklik değil, bilinçli bir karardır.
//
// # Neden SVG YOK
//
// SVG bir görsel gibi görünür ama bir BELGEDİR: <script> taşıyabilir ve aynı
// kökenden sunulduğunda depolanmış XSS olur — dosyayı yükleyen kullanıcı,
// onu görüntüleyen herkesin oturumunda kod çalıştırır. Listede olmaması
// ayrıca kendiliğinden gerçekleşir: [net/http.DetectContentType] bir SVG için
// "text/xml" ya da "text/plain" döner, "image/svg+xml" DEĞİL. Yani içerikten
// tespit edilen tip zaten hiçbir zaman bu listeye düşmez; sabitin yokluğu o
// gerçeği yalnızca yazıya geçirir.
const (
	// ContentTypeJPEG JPEG görselidir.
	ContentTypeJPEG = "image/jpeg"
	// ContentTypePNG PNG görselidir.
	ContentTypePNG = "image/png"
	// ContentTypeGIF GIF görselidir.
	ContentTypeGIF = "image/gif"
	// ContentTypeWebP WebP görselidir.
	ContentTypeWebP = "image/webp"
)

// UploadInput depoya yazılacak tek bir dosyanın girdisidir.
//
// # İstemcinin dosya adı YOKTUR
//
// Alanlar arasında dosya adının bulunmaması bilinçlidir. Depo anahtarını
// sağlayıcı ÜRETİR (kimlik + tespit edilen tipten türeyen uzantı); istemciden
// gelen ad hiçbir aşamada yol bileşeni olmaz, dolayısıyla "../" ile depo
// dışına yazmak YAPISAL olarak imkânsızdır.
//
// Alternatif — adı alıp "temizlemek" — aynı kararı her yeni kodlama
// numarasında (%2e%2e, ters bölü, gömülü NUL, Unicode normalizasyon) yeniden
// vermek demekti ve doğruluğu, temizleyen kişinin o gün kaç numara
// hatırladığına bağlı olurdu. Var olmayan bir alanın kaçırılacak numarası da
// yoktur.
type UploadInput struct {
	// ContentType dosyanın İÇERİĞİNDEN tespit edilmiş tipidir
	// ([net/http.DetectContentType]); istemcinin Content-Type başlığı ASLA
	// buraya yazılmaz.
	//
	// İstemcinin bildirdiği tip bir İDDİADIR, olgu değil: "image/png" diye
	// gönderilen bir HTML dosyası ona güvenen izin listesinden geçer ve
	// sunulduğunda tarayıcıda HTML olarak çalışır. İddiaya bakan bir liste
	// hiçbir şey elemez.
	//
	// Tespitin çağırana bırakılması da bilinçlidir: izin listesi depoya TEK
	// BAYT yazılmadan önce uygulanmalıdır. Sağlayıcı tespit etseydi denetim
	// ancak yazma başladıktan sonra yapılabilir, reddedilen dosya için bir
	// silme çağrısı gerekir ve o silme başarısız olduğunda dosya depoda
	// kalırdı.
	ContentType string
	// Body dosyanın gövdesidir ve AKIŞ olarak okunur.
	//
	// []byte olsaydı 50 MB'lik bir yükleme 50 MB bellek demekti; eş zamanlı
	// birkaç yükleme süreci düşürürdü. Boyut sınırı bu yüzden gövdeyi saran
	// [net/http.MaxBytesReader] ile zorlanır ve yapılandırılabilir olur:
	// sınırsız gövde, tek istekle diski doldurmanın en ucuz yoludur.
	//
	// Sağlayıcı Body'yi KAPATMAZ; kapatmak açanın işidir.
	Body io.Reader
}

// File depoya yazılmış bir dosyadır.
type File struct {
	// Key dosyanın depodaki anahtarıdır ve sağlayıcı tarafından ÜRETİLİR.
	// [FileProvider.Delete] bu değeri alır.
	Key string
	// URL dosyanın erişilebilir adresidir.
	//
	// Anahtar ile adres AYRI şeylerdir: S3'te anahtar "urun/x.jpg" iken adres
	// imzalı bir URL olabilir ve imzadan anahtara geri dönülemez. Silmenin
	// adresi değil anahtarı almasının nedeni budur; tek alan tutulsaydı silme
	// yolu adresi ayrıştırmak zorunda kalır ve her sağlayıcı için yeniden
	// yazılırdı.
	//
	// Çağıran adresi KALICI olarak saklar (ürün görseli kaydına yazılır),
	// dolayısıyla kısa ömürlü bir imza dönen sağlayıcı, veritabanında sessizce
	// çürüyen bir alan bırakır.
	URL string
	// ContentType saklanan tiptir ve [UploadInput.ContentType] ile aynıdır.
	// Dosya sunulurken Content-Type başlığı BUNDAN yazılır.
	ContentType string
	// Size yazılan bayt sayısıdır.
	Size int64
}

// FileProvider bir dosya deposunun çekirdeğe sunduğu sözleşmedir
// (plan Bölüm 5.6).
//
// # İdempotency BEKLENMEZ
//
// [PaymentProvider]'ın aksine burada IdempotencyKey yoktur. Tekrarlanan bir
// yükleme İKİNCİ BİR nesne bırakır; bedeli disk alanıdır, mükerrer tahsilat
// değil. Tekrarı bir anahtarla önlemek, aynı içeriği tanımak için gövdenin
// özetini almayı gerektirirdi ve özet ancak TÜM baytlar okunduktan sonra
// bilinir — yani gövdeyi belleğe ya da geçici dosyaya almak, akış kararını
// tersine çevirmek demekti.
//
// # Sunum sağlayıcının işi DEĞİLDİR ama kural bağlayıcıdır
//
// Dosyayı hangi katman sunarsa sunsun (yerel disk sağlayıcısı kendisi
// sunabilir, S3'te bir CDN sunar) iki kural geçerlidir: Content-Type SAKLANAN
// tipten yazılır, asla istemciden gelenden; ve HER yanıt
// X-Content-Type-Options: nosniff taşır. İkincisi olmadan tarayıcı içeriğe
// bakıp kendi tahminini yapar ve "image/png" olarak saklanmış bir dosya
// HTML'e benziyorsa HTML gibi çalıştırılabilir — yani tespit ve izin listesi
// doğru çalışsa bile sunum aşaması onları geçersiz kılardı.
type FileProvider interface {
	Provider

	// Upload gövdeyi depoya yazar ve erişilebilir adresi döner.
	//
	// Gövde TAMAMEN okunur. Çağrı BLOKLAYICIDIR ve dış bir servise
	// gidebilir; çağıran ctx'e süre sınırı koymalıdır.
	//
	// Okuma yarıda hata verirse — [net/http.MaxBytesReader] sınırı aşan bir
	// gövdeyi tam da böyle keser — sağlayıcı yarım yazdığı nesneyi
	// TEMİZLEMELİ ve hata dönmelidir. Yarım nesne, hiçbir kaydın işaret
	// etmediği ve hiçbir silme yolunun anahtarını bilmediği bir dosyadır:
	// sınırı aşan istekler diski yine de doldurabilirdi.
	Upload(ctx context.Context, in UploadInput) (File, error)

	// Delete dosyayı depodan siler. İDEMPOTENT olmalıdır: olmayan bir anahtar
	// hata DEĞİLDİR.
	//
	// Silme, dosyaya işaret eden kaydı kaldıran akışın temizlik adımıdır ve o
	// akış yeniden denenebilir. İkinci çağrının patlaması, kaydı çoktan
	// silinmiş bir dosyayı temizlenemez hâle getirir — yani tam olarak
	// temizlemesi gereken çöpü kalıcı kılardı.
	Delete(ctx context.Context, key string) error
}
