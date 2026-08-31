package models

import "time"

// MaxOriginalNameLen istemcinin bildirdiği dosya adının kabul edilen azami
// uzunluğudur (karakter).
//
// 255, yaygın dosya sistemlerinin dosya adı sınırıdır; daha uzun bir ad zaten
// istemcinin kendi diskinde de duramazdı. Sınır KIRPMAZ, REDDEDER: kırpmak
// istemcinin gönderdiği veriyi sessizce değiştirmek olurdu ve alanın tek işi
// zaten "kullanıcı ne gördüyse onu göstermek".
const MaxOriginalNameLen = 255

// Upload depoya yazılmış tek bir dosyanın kaydıdır.
//
// # İSTEMCİNİN DOSYA ADI YOL DEĞİLDİR
//
// [Upload.OriginalName] saklanır ama depoda hiçbir şeyin yerini belirlemez;
// dosyanın yeri [Upload.StorageKey]'dir ve onu SAĞLAYICI üretir. Ayrım
// yapısaldır: adın taşıdığı "../" ya da onun herhangi bir kodlaması, hiçbir
// aşamada bir yol bileşenine dönüşemez çünkü ad hiçbir yol ifadesine
// girmez.
//
// Adın yine de saklanmasının sebebi yönetim arayüzüdür: panelde yüklemeleri
// gözden geçiren kişi "urun-kirmizi-onden.jpg" ile üretilmiş anahtarı ayırt
// edemez. Ad bu yüzden GÖSTERİM verisidir — yanıtın JSON gövdesinde geçer,
// hiçbir HTTP BAŞLIĞINA yazılmaz (örn. Content-Disposition): başlığa yazmak,
// içeriğine güvenilmeyen bir dizeyi başlık dilbilgisinin içine koymak olurdu.
type Upload struct {
	// ID kaydın kimliğidir.
	ID string
	// StorageKey dosyanın depodaki anahtarıdır ve sağlayıcı tarafından
	// ÜRETİLMİŞTİR. Silme ve okuma bu değerle yapılır.
	StorageKey string
	// ProviderID dosyayı yazan sağlayıcının kimliğidir.
	//
	// Saklanır çünkü yapılandırma değişir: kurulum bir gün nesne deposuna
	// geçtiğinde eski kayıtlar hâlâ yerel diskte durur ve onları okuyabilecek
	// tek şey, o gün kullanılan sağlayıcıdır.
	ProviderID string
	// ContentType dosyanın İÇERİĞİNDEN tespit edilmiş tipidir; istemcinin
	// bildirdiği tip hiçbir zaman buraya yazılmaz.
	//
	// Dosya sunulurken Content-Type başlığı BU alandan yazılır.
	ContentType string
	// Size dosyanın bayt cinsinden boyutudur.
	Size int64
	// Checksum içeriğin SHA-256 özetidir (küçük harf onaltılık).
	//
	// Yükleme sırasında hesaplanır ve teşhis içindir: "diskteki dosya ile
	// kaydettiğimiz şey aynı mı" sorusunun başka cevabı yoktur. İdempotency
	// için KULLANILMAZ — özet ancak tüm baytlar okunduktan sonra bilinir,
	// yani tekrarı önlemek için ona bakmak gövdeyi akış olarak işlemeyi
	// bırakmak demekti (bkz. çekirdekteki FileProvider sözleşmesi).
	Checksum string
	// OriginalName istemcinin bildirdiği dosya adıdır; boş olabilir.
	// ASLA yol olarak kullanılmaz (bkz. tip belgesi).
	OriginalName string
	// URL dosyanın erişilebilir adresidir.
	//
	// Yerel sağlayıcıda KÖKE GÖRELİDİR ("/files/…"): kurulumun alan adını
	// kayda yazmak, alan adı değiştiği gün her satırı geçersiz kılardı.
	// Farklı bir kökenden sunulan vitrin, adresin önüne kendi kökenini koyar.
	URL string
	// UploadedBy yüklemeyi yapan çağıranın kimliğidir. Serbest metindir,
	// foreign key DEĞİLDİR (Prensip 2.2): kullanıcıyı auth modülü sahiplenir.
	UploadedBy string
	// CreatedAt kaydın açıldığı andır.
	CreatedAt time.Time
	// UpdatedAt kaydın son değiştiği andır.
	UpdatedAt time.Time
}

// UploadFilter yükleme listelemesinin sayfalama parametreleridir.
//
// Süzgeç alanı YOKTUR ve bu bilinçlidir: liste bir yönetim envanteridir ve
// bugün onu süzerek okuyan bir akış yoktur. Tüketicisi olmayan bir süzgeç,
// hem sorguya hem belgeye hem de teste giren ama hiçbir soruyu yanıtlamayan
// bir alan olurdu; sözleşmeye giren alan ise bir daha çıkarılamaz.
type UploadFilter struct {
	// Limit döndürülecek azami satır sayısıdır.
	Limit int64
	// Offset atlanacak satır sayısıdır.
	Offset int64
}
