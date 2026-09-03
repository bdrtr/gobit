package workflow

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"time"
)

// Status bir yürütmenin genel durumudur.
//
// Dört değer, yürütmenin dünyada bıraktığı ize göre okunmalıdır: completed
// "iş yapıldı", failed "iş yapıldı ve GERİ ALINDI", compensation_failed ise
// "iş yapıldı, geri alınamadı" demektir. Bu ayrım olmadan bir operatör hangi
// yürütmeye elle dokunması gerektiğini bilemez.
type Status string

// Yürütme durumları.
const (
	// StatusRunning yürütmenin hâlâ sürdüğünü bildirir. Bir yürütme kaydı bu
	// durumda açılır ve yalnızca uç durumlardan birine geçer.
	StatusRunning Status = "running"
	// StatusCompleted tüm adımların başarıyla tamamlandığını bildirir.
	StatusCompleted Status = "completed"
	// StatusFailed bir adımın patladığını ve telafinin BAŞARIYLA tamamlandığını
	// bildirir: yarım kalmış iş yoktur, sistem tutarlıdır.
	//
	// # Bu duruma geçmek idempotency anahtarını BIRAKIR
	//
	// Anlamı gereği: "telafi edildi" tam olarak "bu deneme dünyada iz
	// bırakmadı" demektir ve anahtar da bir izdir. Bırakılmasaydı — ki bir
	// zamanlar bırakılmıyordu — aynı anahtarla gelen her sonraki çağrı sonsuza
	// dek 409 alırdı. Vitrinde bunun karşılığı şuydu: kartı reddedilen müşteri
	// O SEPETİ BİR DAHA ÖDEYEMİYORDU. Anahtar sepet kimliğinden türetildiği
	// için "yeni bir anahtar kullanın" tavsiyesinin HTTP yüzeyinde bir
	// karşılığı da yoktu.
	//
	// Kayıt SİLİNMEZ, yalnızca anahtarı düşer: başarısız deneme denetim kaydı
	// olarak kalır.
	//
	// Öteki uç durumların hiçbiri anahtarı bırakmaz ve bırakmamalıdır:
	// [StatusCompleted] bıraksaydı aynı sepet iki kez tahsil edilirdi,
	// [StatusCompensationFailed] bıraksaydı elle müdahale bekleyen yarım bir iş
	// yeni bir denemenin üstüne binerdi.
	StatusFailed Status = "failed"
	// StatusCompensationFailed hem adımın hem telafinin patladığını bildirir.
	// Sistem tutarsız kalmıştır; ELLE MÜDAHALE gerekir. Bir izleme kuralı
	// öncelikle bu durumu saymalıdır.
	StatusCompensationFailed Status = "compensation_failed"
)

// StepStatus tek bir adımın son durumudur.
type StepStatus string

// Adım durumları.
const (
	// StepInvoked adımın Invoke'unun başarıyla tamamlandığını bildirir.
	StepInvoked StepStatus = "invoked"
	// StepFailed adımın Invoke'unun patladığını bildirir. Bu adım kural olarak
	// TELAFİ EDİLMEZ; başarısız bir Invoke'un geri alınacak bir işi yoktur.
	// Tek istisna motorun KENDİ tetiklediği tekrardır: Attempts > 1 ise adım en
	// iyi çaba telafi edilir ve kaydı compensated ya da compensation_failed
	// olarak güncellenir (bkz. paket yorumu).
	StepFailed StepStatus = "failed"
	// StepCompensated adımın Compensate'inin başarıyla çalıştığını bildirir.
	StepCompensated StepStatus = "compensated"
	// StepCompensationFailed adımın Compensate'inin patladığını bildirir.
	// Bu adımın yan etkisi sistemde ASILI kalmıştır.
	StepCompensationFailed StepStatus = "compensation_failed"
)

// Ad ve anahtar uzunluk sınırları.
//
// Sınırlar Store SÖZLEŞMESİNİN parçasıdır. Kalıcı bir uygulama bu alanları
// indeksler ve sınırsız bir değer orada anlaşılmaz bir sürücü hatasına
// dönüşür; sınırın motorda durması iki şeyi sağlar — uygulamalar aynı girdide
// aynı davranır (bellek içi Store'da geçip Postgres'te düşen bir workflow
// olmaz) ve hata, hiçbir yan etki uygulanmadan ÖNCE çağırana döner. Motor
// bunları Workflow.Validate ve WithIdempotencyKey içinde uygular; Store
// uygulamaları en az bu uzunlukları KABUL ETMELİDİR.
const (
	// MaxNameLen workflow ve adım adlarının bayt cinsinden üst sınırıdır.
	MaxNameLen = 128
	// MaxIdempotencyKeyLen idempotency anahtarının bayt cinsinden üst sınırıdır.
	MaxIdempotencyKeyLen = 256
)

// StepRecord tek bir adımın kalıcı izidir.
type StepRecord struct {
	// Name adımın Step.Name() ile bildirdiği adıdır.
	Name string
	// Index adımın workflow içindeki sırasıdır ve kaydın KİMLİĞİDİR:
	// Store.AppendStep aynı Index'li kaydı günceller. Bir adım önce invoked,
	// sonra compensated olarak yazıldığında aynı satır güncellenir.
	Index int
	// Status adımın son durumudur.
	Status StepStatus
	// Output Invoke'un döndürdüğü değerin JSON karşılığıdır.
	//
	// Status invoked iken bu alan boş ve Failure dolu ise adım BAŞARILIYDI ama
	// çıktısı JSON'a çevrilemedi; bkz. Executor.Run'ın kalıcılaştırma politikası.
	//
	// Telafi kaydı bu alanı SİLMEZ: compensated ve compensation_failed
	// durumlarında da Invoke'un çıktısı okunabilir kalır — elle müdahale eden
	// operatörün ihtiyacı olan tek veri (hangi rezervasyon, hangi ödeme)
	// buradadır.
	Output json.RawMessage
	// Failure hata mesajıdır; adım ve telafisi başarılıysa boştur.
	//
	// Invoke patladıktan sonra en iyi çaba telafi edilen bir adımda Invoke'un
	// hatası KORUNUR; telafi de patlarsa iki mesaj ";" ile birleştirilir.
	Failure string
	// Attempts adımın INVOKE denemesi sayısıdır (ilk deneme dâhil, en az 1).
	// Telafi kaydı bu sayıyı korur; telafi denemeleri kayda değil loga yazılır.
	Attempts int
	// StartedAt Invoke'un ilk denemesinin başladığı andır (UTC); telafi kaydı
	// bu anı korur.
	StartedAt time.Time
	// EndedAt kayda yazılan son işin bittiği andır (UTC): adım telafi edildiyse
	// telafinin, edilmediyse son Invoke denemesinin bitiş anı.
	EndedAt time.Time
}

// Execution tek bir workflow yürütmesinin kalıcı durumudur.
type Execution struct {
	// ID yürütmenin tekil kimliğidir ("wfx_" ön ekli, zaman sıralı).
	ID string
	// Workflow yürütülen workflow'un adıdır.
	Workflow string
	// IdempotencyKey çağıranın verdiği tekrar koruma anahtarıdır; verilmediyse
	// boştur. Store (Workflow, IdempotencyKey) çiftinin tekilliğini yalnızca
	// anahtar boş DEĞİLKEN zorlar.
	IdempotencyKey string
	// Status yürütmenin genel durumudur.
	Status Status
	// Input workflow girdisinin JSON karşılığıdır.
	Input json.RawMessage
	// Output son adımın çıktısının JSON karşılığıdır; yalnızca completed
	// durumunda anlamlıdır.
	Output json.RawMessage
	// Failure uç durumdaki hata mesajıdır; başarılı yürütmede boştur.
	Failure string
	// Steps adım kayıtlarıdır; yürütme sırasındadır.
	Steps []StepRecord
	// CreatedAt kaydın açıldığı andır (UTC).
	CreatedAt time.Time
	// UpdatedAt son yazmanın anıdır (UTC).
	UpdatedAt time.Time
}

// Store yürütme durumunu kalıcılaştırır.
//
// Motor bu arayüzü TÜKETİCİ olarak tanımlar (ADR 0001 örüntüsü): Postgres
// uygulaması ayrı bir pakettedir ve bu paket onu import etmez. Süreç içi
// uygulama için bkz. NewMemoryStore.
//
// Uygulamalar eşzamanlı çağrılara güvenli olmalıdır: aynı anda birden çok
// yürütme koşabilir ve aynı yürütmenin adımları tek goroutine'den yazılsa da
// Get/FindByIdempotencyKey başka goroutine'lerden okunabilir.
//
// Ad ve anahtar alanları için uygulamalar en az MaxNameLen ve
// MaxIdempotencyKeyLen kadarını kabul etmelidir; motor bu sınırların üstünü
// zaten reddeder. Adların baştaki/sondaki boşluklarına ANLAM YÜKLENMEMELİDİR:
// bir uygulama bu alanları normalleştirebilir (örn. kırpabilir) ve iki
// uygulama aynı değeri farklı saklayabilir.
type Store interface {
	// Create yeni yürütme kaydı açar. Aynı (Workflow, IdempotencyKey) çifti
	// zaten varsa errors.Conflict döner.
	Create(ctx context.Context, exec *Execution) error
	// FindByIdempotencyKey anahtara karşılık gelen yürütmeyi döner; yoksa errors.NotFound.
	FindByIdempotencyKey(ctx context.Context, workflow, key string) (*Execution, error)
	// AppendStep bir adım kaydını ekler ya da aynı Index'li kaydı günceller.
	AppendStep(ctx context.Context, executionID string, rec StepRecord) error
	// UpdateStatus yürütmenin son durumunu yazar.
	//
	// [StatusFailed] yazıldığında uygulama yürütmenin idempotency ANAHTARINI
	// da bırakmalıdır (kaydı silmeden). Gerekçe [StatusFailed] godoc'undadır ve
	// bu, ayrı bir metot değil aynı yazımın parçasıdır: iki ayrı yazım
	// arasında düşen bir süreç anahtarı sonsuza dek tutulu bırakırdı — yani
	// düzeltilen arızanın kendisini nadir bir yarış olarak geri getirirdi.
	UpdateStatus(ctx context.Context, executionID string, status Status, output json.RawMessage, failure string) error
	// Get yürütmeyi adımlarıyla birlikte okur; yoksa errors.NotFound.
	Get(ctx context.Context, executionID string) (*Execution, error)
}

// executionIDPrefix yürütme kimliklerinin ön ekidir (plan Bölüm 8).
const executionIDPrefix = "wfx_"

// idEncoding Crockford Base32 alfabesidir; kimlik dizgesi hem sıralanabilir
// hem de elle okunabilir olsun diye seçilmiştir (I/L/O/U yoktur).
var idEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// newExecutionID zaman sıralı ve tekil bir yürütme kimliği üretir.
//
// Yapısı ULID ile aynıdır: 48 bit milisaniye zaman damgası + 80 bit
// kriptografik rastgelelik, Crockford Base32 ile 26 karaktere kodlanır.
// Aynı yaklaşım eventbus'ta olay kimlikleri için de kullanılır.
func newExecutionID(t time.Time) string {
	ms := t.UTC().UnixMilli()
	if ms < 0 {
		// 1970 öncesi bir zaman damgası yürütme için anlamlı değildir;
		// sıralamayı bozmamak için tabana çekilir.
		ms = 0
	}

	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], uint64(ms))

	var buf [16]byte
	// UnixMilli 48 bite sığar; ilk iki bayt daima sıfırdır ve atılır.
	copy(buf[:6], stamp[2:])
	if _, err := rand.Read(buf[6:]); err != nil {
		// crypto/rand.Read hata dönmez; yine de bir gün dönerse kimlik
		// nanosaniye çözünürlüğüne düşer — tekillik zayıflar ama yürütme
		// başlatılamaz duruma gelmez.
		binary.BigEndian.PutUint64(buf[8:], uint64(t.UnixNano()))
	}

	return executionIDPrefix + idEncoding.EncodeToString(buf[:])
}
