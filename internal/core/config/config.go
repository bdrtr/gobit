// Package config uygulamanın tüm ayarlarını 12-factor ilkesine uygun biçimde
// ortam değişkenlerinden yükler ve doğrular.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

// minJWTSecretLen paylaşılan ortamlarda kabul edilen en kısa imza sırrıdır.
//
// HS256 için sır, çıktı uzunluğu kadar (32 bayt) entropi taşımalıdır; daha
// kısası kaba kuvvetle bulunabilir.
const minJWTSecretLen = 32

// devAppEnv sırların ve TLS zorunluluğunun GEVŞETİLDİĞİ tek ortamdır.
const devAppEnv = "development"

// BackendRedis paylaşılan Redis arka ucunun adıdır.
//
// Hem [Config.EventBus] hem [Config.GuardBackend] bu değeri alabilir ve ikisi
// AYNI istemciyi paylaşır (bkz. [Config.NeedsRedis]). Adın tek bir sabitten
// okunması, iki alanın sessizce farklı yazımlara ayrışmasını önler.
const BackendRedis = "redis"

// MinBootstrapPasswordLen paylaşılan ortamlarda ilk yönetici parolasının kabul
// edilen en kısa uzunluğudur.
//
// 16, auth modülünün HER yöneticiye uyguladığı 12'lik tabanın (service
// paketindeki MinPasswordLen) ÜSTÜNDEDİR ve bu bilinçlidir: buradaki değer bir
// kullanıcı parolası değil, DAĞITIM SIRRIDIR. Ortam dosyasında ya da secret
// deposunda durur, bir kez yazılır ve kimsenin ezberlemesi gerekmez — yani
// uzunluğun kullanıcıya maliyeti yokken karşılığında sistemin ilk ve en
// yetkili hesabının arama uzayı büyür.
//
// Sayı auth modülünden kopyalanmaz, ondan BAĞIMSIZ olarak seçilir; çekirdek
// modülleri tanımadığı için (Prensip 2.4) sabite bağlanmak zaten mümkün
// değildir. Tek şartı auth'un tabanının altına düşmemektir: düşseydi config
// kabul ettiği bir parolayı tohum adımı reddeder ve açılış anlaşılmaz biçimde
// dururdu.
//
// Yerel geliştirmede ZORLANMAZ: "make up && make run" ile denemek isteyen
// geliştirici kısa bir parola yazabilmelidir. Taban orada da kaybolmaz, auth
// modülünün kendi politikası yine uygulanır; yalnızca bu ek kat düşer.
//
// DIŞA AÇIKTIR ki auth modülünün genel parola tabanıyla ilişkisi bir testle
// sabitlenebilsin (bkz. internal/arch). Bağ elle tutulsaydı, auth'un tabanı bir
// gün bu değerin üstüne çıktığında buradaki kapı SESSİZCE etkisizleşirdi:
// auth'un zaten reddettiği bir parolayı burada ayrıca reddetmek hiçbir şey
// eklemez ve "paylaşılan ortamda daha uzun parola isteniyor" iddiası
// gerçekliğini kaybederdi.
const MinBootstrapPasswordLen = 16

// DefaultRedisKeyPrefix koruma anahtarlarının varsayılan ad alanı önekidir.
//
// Değer, önek yapılandırılabilir olmadan önce redisguard'a GÖMÜLÜ olan
// önekin ta kendisidir. Geriye uyumluluk burada bir tercih değil zorunluluktur:
// varsayılanı değiştirmek, yükseltilen bir kurulumun tüm hız sınırı sayaçlarını
// ve — çok daha kötüsü — işlemdeki idempotency kayıtlarını bir anda görünmez
// kılar; o an uçan her tekrar isteği ikinci kez işlenir, yani ikinci sipariş.
const DefaultRedisKeyPrefix = "gobit"

// DefaultNotificationProvider bildirim sağlayıcısı seçilmediğinde kullanılan
// kimliktir.
//
// Değer, notification modülünün kutudan çıkan sağlayıcısının kimliğiyle
// (logonly.ID) aynıdır ama o pakete BAĞLANAMAZ: çekirdek modülleri import
// edemez (Prensip 2.4). Ayrışırlarsa kurulum, kayıtta bulunmayan bir sağlayıcı
// adıyla açılmaya çalışır ve cmd/server açılışı durdurur — sessiz kalmaz.
//
// DIŞA AÇIKTIR ki envDefault etiketiyle uyumu bir testle sabitlenebilsin.
const DefaultNotificationProvider = "log"

// DefaultFileProvider dosya sağlayıcısı seçilmediğinde kullanılan kimliktir.
//
// Değer, file modülünün kutudan çıkan sağlayıcısının kimliğiyle (local.ID)
// aynıdır ama o pakete BAĞLANAMAZ: çekirdek modülleri import edemez
// (Prensip 2.4). Ayrışmanın bedeli [DefaultNotificationProvider]'ınkiyle
// aynıdır ve tekrarlanmıyor.
//
// DIŞA AÇIKTIR ki envDefault etiketiyle ve modülün sabitiyle uyumu birer
// testle sabitlenebilsin (bkz. internal/arch).
const DefaultFileProvider = "local"

// DefaultFileRoot "local" dosya sağlayıcısının varsayılan kök dizinidir.
//
// Göreli ve KALICI bir yoldur; gerekçesi [Config.FileRoot] alanındadır.
const DefaultFileRoot = "./data/uploads"

// DefaultFileMaxUploadBytes tek bir yüklemenin varsayılan azami boyutudur.
//
// 5 MiB, bir ürün görseli için bol; kazara sürüklenmiş bir video için dardır.
// Değer envDefault etiketinde ONDALIK olarak tekrarlanır (Go struct etiketleri
// sabit referansı kabul etmez) ve uyum bir testle sabitlenmiştir.
const DefaultFileMaxUploadBytes int64 = 5 << 20

// DefaultFileAllowedTypes yüklemede varsayılan olarak kabul edilen içerik
// tipleridir.
//
// Dize olmasının sebebi envSeparator'dır: etiketteki varsayılan da tek bir
// dizedir ve ikisinin uyumu ancak aynı biçimde tutulurlarsa denetlenebilir.
const DefaultFileAllowedTypes = "image/jpeg,image/png,image/gif,image/webp"

// GraphQL okuma yüzeyinin varsayılan sınırları.
//
// Değerler, sınırları UYGULAYAN paketin (internal/modules/product/graph)
// sabitlerinin tekrarıdır; çekirdek modülleri import EDEMEDİĞİ için
// (Prensip 2.4) onlara bağlanamaz. Ayrışmanın bedeli sessizdir: hiçbir ortam
// değişkeni vermemiş bir kurulum, hem bu dosyada hem modülün belgesinde
// yazandan BAŞKA bir sınırla çalışırdı. Bağ bu yüzden bir testle sabitlendi
// (bkz. internal/arch).
//
// Adların GRAPHQL_ öneki güvenlidir: METRIC_EXPORT_INTERVAL'ın kaçındığı
// durumun (bkz. [Config.MetricInterval]) tersine, ne GraphQL belirtimi ne de
// gqlgen bu adlardan birini AYIRMIŞTIR — yani ödünç alınmış bir ada, anlamı
// ödünç alınmadan sahip olunmuyor.
const (
	// DefaultGraphQLMaxDepth iç içe geçen alan sayısının varsayılan üst
	// sınırıdır.
	DefaultGraphQLMaxDepth = 10

	// DefaultGraphQLMaxComplexity tek bir belgenin varsayılan maliyet tavanıdır.
	DefaultGraphQLMaxComplexity = 50000

	// DefaultGraphQLIntrospection iç gözlemin varsayılan olarak açık olduğunu
	// bildirir.
	DefaultGraphQLIntrospection = true

	// DefaultGraphQLMaxFieldRepetition aynı alanın aynı nesne altında kaç kez
	// seçilebileceğinin varsayılan üst sınırıdır.
	DefaultGraphQLMaxFieldRepetition = 20

	// DefaultGraphQLMaxResponseBytes tek bir yanıtın varsayılan bayt tavanıdır.
	DefaultGraphQLMaxResponseBytes = 4 << 20

	// DefaultGraphQLMaxIntrospectionRoots bir belgedeki iç gözlem kökü
	// sayısının varsayılan üst sınırıdır.
	DefaultGraphQLMaxIntrospectionRoots = 2

	// DefaultGraphQLMaxIntrospectionDepth iç gözlem alt ağacının varsayılan
	// derinlik tavanıdır.
	DefaultGraphQLMaxIntrospectionDepth = 15

	// DefaultGraphQLMaxSelections bir belgenin açıldığında üretebileceği
	// varsayılan azami seçim sayısıdır.
	DefaultGraphQLMaxSelections = 10000
)

// Yalnızca yerel geliştirme için varsayılan bağlantı adresleri.
// deploy/docker-compose.yml ile eşleşirler. Validate, APP_ENV=production iken
// bu değerlerin ezilmiş olmasını ZORUNLU kılar; aksi hâlde eksik secret
// enjeksiyonu sessizce sabit-kodlu kimlik bilgisiyle üretime çıkardı.
//
// DİKKAT: Aşağıdaki envDefault etiketleri bu sabitlerle birebir aynı olmalıdır
// (Go struct etiketleri sabit referansı kabul etmez). TestDefaultTagsMatchConstants
// bu eşleşmeyi denetler.
const (
	// Buradaki gosec bastırmaları bilinçlidir: bu sabitler gizli bilgi DEĞİL,
	// tam tersine üretimde REDDEDİLMESİ gereken, bilinen yerel geliştirme
	// değerleridir. Validate bunlarla karşılaştırma yaparak korumayı uygular.
	DefaultDatabaseURL = "postgres://gobit:gobit@localhost:5432/gobit?sslmode=disable" //nolint:gosec // G101: kasıtlı yerel geliştirme varsayılanı; üretimde Validate reddeder
	DefaultRedisURL    = "redis://:gobit@localhost:6379/0"                             //nolint:gosec // G101: kasıtlı yerel geliştirme varsayılanı; üretimde Validate reddeder
)

// PostgreSQL havuzunun varsayılan sınırları.
//
// Değerler internal/core/db'nin KENDİ varsayılanlarıyla aynıdır ve öyle
// kalmalıdır: bu iki sabitin tek işi, havuz yapılandırılabilir hâle gelmeden
// önceki davranışı korumaktır — ortam değişkeni vermeyen bir kurulum
// bugünküyle birebir aynı havuzu açar. Ayrışmayı internal/arch'taki
// TestHavuzVarsayilanlariDbIleUyusuyor düşürür.
//
// Tip int32'dir çünkü pgxpool'un alanı int32'dir; komşularının hepsi int
// olduğu için bu bilinçli bir sapmadır ve gerekçesi ÖLÇÜLDÜ. int olsaydı
// bağlama noktasında bir daraltma dönüşümü (int32(cfg.DBMaxConns)) gerekirdi
// ve linter onu reddediyor: "G115: integer overflow conversion int -> int32".
// Tek kaçış yolu bir nolint satırıydı, yani denetimi kapatmak; tipi tüketiciye
// uydurmak dönüşümü hiç var etmiyor.
//
// Aralık dışı bir değer İKİ tipte de ayrıştırmada düşer — env kütüphanesi
// int'i de 32 bitle sınırlıyor ("strconv.ParseInt: parsing \"2147483648\":
// value out of range", tipi int iken ölçüldü). Yani buradaki seçim taşmayı
// önlemiyor; taşma zaten önlenmiş, seçim yalnızca bastırılmış bir denetimi
// önlüyor.
const (
	// DefaultDBMaxConns havuzun açabileceği varsayılan azami bağlantı sayısıdır.
	DefaultDBMaxConns int32 = 10

	// DefaultDBMinConns havuzun boştayken bile korumaya çalıştığı varsayılan
	// bağlantı sayısıdır.
	DefaultDBMinConns int32 = 2
)

// Geçerli enum değerleri; Validate bunlara göre doğrulama yapar.
var (
	validAppEnvs    = []string{devAppEnv, "staging", "production"}
	validLogLevels  = []string{"debug", "info", "warn", "error"}
	validLogFormats = []string{"json", "text"}
	validEventBuses = []string{"inmemory", BackendRedis}
	// validGuardBackends koruma bileşenlerinin geçerli arka uçlarıdır.
	validGuardBackends = []string{"memory", BackendRedis}
)

// Config sunucunun çalışması için gereken tüm ayarları tutar.
//
// Varsayılan değerler deploy/docker-compose.yml ile uyumludur; yerelde
// "make up && make run" ek ayar gerektirmeden çalışır. Üretimde DatabaseURL
// ve RedisURL mutlaka ortam değişkeniyle ezilmelidir.
type Config struct {
	// AppEnv çalışma ortamıdır: development | staging | production.
	AppEnv string `env:"APP_ENV" envDefault:"development"`
	// AppPort HTTP sunucusunun dinleyeceği TCP portudur.
	AppPort int `env:"APP_PORT" envDefault:"9000"`

	// DatabaseURL PostgreSQL bağlantı adresidir (pgx DSN formatı).
	DatabaseURL string `env:"DATABASE_URL" envDefault:"postgres://gobit:gobit@localhost:5432/gobit?sslmode=disable"`
	// RedisURL Redis bağlantı adresidir.
	RedisURL string `env:"REDIS_URL" envDefault:"redis://:gobit@localhost:6379/0"`

	// DBMaxConns PostgreSQL havuzunun açabileceği azami bağlantı sayısıdır.
	//
	// Bu sayı TEK BİR isteğin değil, TÜM SÜRECİN veritabanı eşzamanlılık
	// tavanıdır: HTTP istekleri, workflow motoru (pgstore) ve olay tüketicisi
	// aynı havuzdan çeker. Tavana varıldığında istek hata ALMAZ, sıraya girer —
	// pgxpool.Acquire bir bağlantı boşalana ya da isteğin son teslim tarihi
	// dolana kadar bekler.
	//
	// Tavanın kolayca gözden kaçan tarafı GraphQL'dedir: gqlgen kök alanlarını
	// EŞZAMANLI çözer ve sayıyı SINIRLAMAZ — graphql.FieldSet.Dispatch ilkini
	// çağıranın goroutine'inde, kalan her biri için bir tane daha açar.
	// GRAPHQL_MAX_FIELD_REPETITION=20 ile tek bir MEŞRU vitrin belgesi 20 takma
	// adlı "products" ve 20 takma adlı "product" taşıyabilir; yani tek istek 40
	// eşzamanlı okuma açar ve her biri sırayla birkaç gidiş dönüş yapar.
	//
	// Aşağıdaki tablo 40 eşzamanlı LİSTE alanıyla ölçüldü ve bu, tek bir
	// belgeden çıkmaz: tekrar sınırı (nesne, alan) çifti başına sayıldığı için
	// tek belge en fazla 20 liste + 20 tekil alan verir. Yani tablo bir yük
	// SEVİYESİDİR, "tek istek bunu yapar" değil — iki belge ya da iki eşzamanlı
	// istemci yapar.
	//
	// # Ölçüm
	//
	// 52 bin ürünlük katalogda, gerçek vitrin sorgularıyla, 40 eşzamanlı kök
	// alanı × 5 tur:
	//
	//	max_conns=10   p50 298,9 ms   813 alımın 771'i bekledi   ort. bekleme 65,3 ms
	//	max_conns=20   p50 313,8 ms   813 alımın 740'ı bekledi   ort. bekleme 37,4 ms
	//	max_conns=40   p50 314,4 ms   813 alımın  38'i bekledi   ort. bekleme  0,7 ms
	//
	// Tek başına çalışan aynı kök alanı 63,2 ms sürüyor; yani 10 bağlantıda
	// gecikme 4,7 katına çıkıyor. Ama havuzu büyütmek onu GERİ GETİRMİYOR:
	// veritabanı aynı kutudayken ve sorgu CPU'ya bağlıyken darboğaz havuz
	// değil sunucunun kendisidir, havuz yalnızca kuyruğun yerini değiştirir.
	//
	// Bağlantının TUTULMA süresi CPU'ya değil AĞ GECİKMESİNE bağlı olduğunda —
	// üretimde olağan olan, ayrı bir veritabanı sunucusu — tablo döner. Gecikme,
	// bağlantı TUTULURKEN beklenerek modellendi: sunucu CPU'su harcanmaz, bir ağ
	// atlaması havuza tam olarak bunu yapar. AYNI liste kök alanı, aynı 40'lık
	// fan-out, gidiş dönüş başına eklenen gecikmeyle:
	//
	//	gecikme   max_conns=10    max_conns=40
	//	yok       p50 306 ms      p50 368 ms
	//	5 ms      p50 459 ms      p50 348 ms
	//	20 ms     p50 638 ms      p50 351 ms
	//
	// Yani düğmenin kazandırdığı şey topolojiye bağlıdır ve LİSTE yolunda
	// ölçülü olarak ılımlıdır: 5 ms'lik bir atlamada 1,3 kat, 20 ms'de 1,8 kat.
	// Daha ucuz kök alanlarında etki büyür — üç gidiş dönüşlük TEKİL ürün
	// alanında, aynı fan-out ve 5 ms gecikmeyle p50 69,2 ms'den 18,0 ms'ye iner
	// (3,8 kat) — çünkü orada sorgunun kendisi neredeyse bedavadır ve geçen süre
	// neredeyse tamamen bekleyiştir. İki satırı karıştırmamak gerekir: aynı
	// düğme, aynı fan-out, farklı kök alanı.
	//
	// # Varsayılan neden 10 kaldı
	//
	// Ölçüm "büyük her zaman daha iyi" demiyor; eksik olanın SAYI DEĞİL DÜĞME
	// olduğunu söylüyor. Varsayılanı yükseltmek her örneğin SUNUCUDAKİ bağlantı
	// bütçesini de çarpardı: max_connections=100'lük bir kümede 40'lık havuz iki
	// örneğe yer bırakır ve üçüncüsü "sorry, too many clients already" ile
	// açılır. O bedel bütün kurulumlara, karşılığındaki kazanç ise yalnızca
	// gecikmeye bağlı topolojilere düşerdi.
	//
	// # Neden açılışta UYARI yok
	//
	// "Havuz (10), GraphQL fan-out'undan (40) küçük" uyarısı DOĞRU kurulan her
	// kurulumda çalardı: havuz tek bir belgenin değil, tüm eşzamanlı isteklerin
	// paylaştığı bir bütçedir ve onu en kötü tek belgeye göre boyutlamak
	// kimsenin yapmadığı bir şeydir. ADR 0015 karar 4'ün ölçütü de tutmuyor:
	// tükenmiş havuz SESSİZ değildir — ölçüldüğünde son teslim tarihi dolan
	// 20 isteğin 20'si de hata döndü ("context deadline exceeded"). Hata yavaş
	// bir sorgudan ayırt edilemiyor, ama havuzun sınırları açılışta zaten
	// loglanıyor (bkz. db.New, "the postgres connection pool is ready").
	DBMaxConns int32 `env:"DB_MAX_CONNS" envDefault:"10"`

	// DBMinConns havuzun boştayken bile korumaya çalıştığı bağlantı sayısıdır.
	//
	// DB_MAX_CONNS ile BİRLİKTE açılır. Tek başına açılan bir tavan yalnızca
	// yukarı çevrilebilirdi: alt sınır 2'de sabitken DB_MAX_CONNS=1 vermek
	// havuzun kendi doğrulamasına ("MinConns cannot be greater than MaxConns")
	// takılır ve süreç hiç açılmaz. Küçültme uydurma bir ihtiyaç değildir —
	// paylaşılan bir kümeye çok sayıda örnekle bağlanan kurulumun tek çaresi
	// örnek başına havuzu daraltmaktır.
	DBMinConns int32 `env:"DB_MIN_CONNS" envDefault:"2"`

	// JWTSecret admin oturum jetonlarının imzalandığı sırdır.
	//
	// Varsayılanı YOKTUR ve olmamalıdır: tahmin edilebilir bir imza sırrı,
	// herkesin kendine admin jetonu üretebilmesi demektir. Boş bırakıldığında
	// yalnızca YEREL GELİŞTİRMEDE uygulama açılır; paylaşılan her ortamda
	// (staging ve production) Validate bunu REDDEDER. Bkz. IsShared.
	JWTSecret string `env:"JWT_SECRET"`
	// JWTTTL admin oturum jetonunun geçerlilik süresidir.
	JWTTTL time.Duration `env:"JWT_TTL" envDefault:"12h"`

	// AdminBootstrapEmail açılışta yaratılacak İLK yönetim kullanıcısının
	// e-postasıdır.
	//
	// Boş bir veritabanıyla açılan sunucuda hiç yönetici yoktur ve yönetim
	// uçları korumalı olduğu için ilkini HTTP'den yaratmanın yolu da yoktur;
	// bu iki değişken olmadan taze bir kurulum KULLANILAMAZ kalırdı.
	//
	// [Config.AdminBootstrapPassword] ile BİRLİKTE verilir; yalnızca biri
	// verilirse Validate hata döner. İkisi de boşsa tohum adımı hiç çalışmaz
	// ve bu meşru bir seçimdir: KURULMUŞ bir sistemin ortamında bu
	// değişkenlerin durması gerekmez.
	//
	// "Kurulmuş" şartı doğrulamanın GÖREMEDİĞİ bir şarttır: veritabanında hiç
	// kullanıcı olup olmadığı buradan bilinemez. Denetim bu yüzden tohum
	// adımına aittir ve orada taze bir veritabanı + boş bu iki değişken,
	// paylaşılan ortamlarda açılışı DURDURUR; gerekçesi cmd/server
	// reportUnmanageableInstallation godoc'undadır.
	AdminBootstrapEmail string `env:"ADMIN_BOOTSTRAP_EMAIL"`
	// AdminBootstrapPassword ilk yönetim kullanıcısının parolasıdır.
	//
	// ASLA loglanmaz ve hiçbir hata mesajında geçmez; doğrulama yalnızca
	// UZUNLUĞUNU bildirir. Paylaşılan ortamlarda en az
	// [MinBootstrapPasswordLen] karakter olmalıdır.
	//
	// Tohum adımı yalnızca hiç kullanıcı yokken çalıştığı için (bkz. cmd/server
	// seedAdmin) bu değerin ortamda unutulması var olan bir yöneticinin
	// parolasını DEĞİŞTİRMEZ.
	AdminBootstrapPassword string `env:"ADMIN_BOOTSTRAP_PASSWORD"`
	// EventBus olay veri yolunun arka ucudur: inmemory | redis.
	//
	// inmemory tek süreçlidir ve KALICI DEĞİLDİR: teslim asenkrondur, süreç
	// çökerse ya da kapanış [Config.ShutdownTimeout] içinde bitmezse teslim
	// edilmemiş olaylar iz bırakmadan kaybolur — sipariş konulmuş, onay
	// bildirimi hiç gitmemiştir. Paylaşılan ortamlarda bu risk açılışta
	// UYARILIR (bkz. cmd/server warnAboutEventBus); durdurulmaz, çünkü tek
	// örnekli bir staging kurulumunda inmemory hâlâ meşru bir seçimdir ve
	// GUARD_BACKEND=memory ile aynı ödünç verilir.
	//
	// Birden çok örnek çalıştırılıyorsa redis kullanılmalıdır (plan Bölüm 3);
	// o zaman olayların ad alanını [Config.RedisKeyPrefix] belirler.
	EventBus string `env:"EVENT_BUS" envDefault:"inmemory"`

	// EventBusConsumer bu süreci consumer group içinde tanımlayan addır
	// (yalnızca EVENT_BUS=redis iken kullanılır).
	//
	// Boş bırakılırsa "<hostname>-<pid>" kullanılır ve bu, kap başına tek süreç
	// çalıştıran her dağıtımda doğru olandır. Açıkça verilmesinin tek meşru
	// sebebi KALICI bir kimliktir (örn. StatefulSet pod adı): veri yolu bekleyen
	// listeyi yalnızca KENDİ adına sorar, yani süreç her açılışta yeni bir adla
	// gelirse önceki çalışmanın işlenip ACK'lenmemiş mesajları hiçbir zaman
	// teslim edilmez.
	//
	// AYNI adı iki sürece vermek en kötü seçenektir ve sessizdir: ikisi de
	// açılışta o adın bekleyen listesini okur, yani ötekinin HÂLÂ işlemekte
	// olduğu mesajları da alır ve aynı olay iki kez işlenir. Doğrulama bunu
	// göremez — tek süreç, kendisinden başkasını bilmez — bu yüzden kullanılan
	// ad açılışta LOGLANIR; çakışma ancak iki açılış logu yan yana konduğunda
	// görülür.
	EventBusConsumer string `env:"EVENT_BUS_CONSUMER"`

	// NotificationProvider bildirimleri gönderecek sağlayıcının kimliğidir.
	//
	// Varsayılan [DefaultNotificationProvider], yani hiçbir yere GÖNDERMEYEN
	// "log" sağlayıcısıdır: çerçeve hangi e-posta/SMS servisinin
	// kullanılacağını bilemez ve varsayılanın adı, gönderim yapmadığını
	// açıkça söylemelidir.
	//
	// Hangi adların GEÇERLİ olduğunu config BİLMEZ ve bilemez: sağlayıcılar
	// eklentilerden gelir ve eklenti listesi derleme zamanında belirlenir
	// (aynı ayrım [Config.Plugins] için de geçerlidir). Burada yalnızca BİÇİM
	// doğrulanır; adın gerçekten kayıtlı olup olmadığını, tüm eklentiler
	// yüklendikten sonra kompozisyon kökü (cmd/server) denetler ve bilinmeyen
	// bir ad açılışı DURDURUR. Sessizce varsayılana düşmek, üretimde e-posta
	// gönderdiğini sanan ama hiçbir müşteriye ulaşmayan bir kurulum üretirdi.
	NotificationProvider string `env:"NOTIFICATION_PROVIDER" envDefault:"log"`

	// FileProvider yüklenen dosyaları saklayacak sağlayıcının kimliğidir.
	//
	// Varsayılan [DefaultFileProvider], yani dosyaları [Config.FileRoot]
	// altına yazan "local" sağlayıcısıdır. Adın GEÇERLİ olup olmadığını config
	// bilemez — gerekçe [Config.NotificationProvider] ile birebir aynıdır ve
	// tekrarlanmıyor; burada yalnızca BİÇİM doğrulanır.
	FileProvider string `env:"FILE_PROVIDER" envDefault:"local"`

	// FileRoot "local" sağlayıcısının dosyaları yazdığı kök dizindir.
	//
	// # Neden GEÇİCİ DİZİN DEĞİL
	//
	// Yapılandırılmadığında os.TempDir()'e yazmak cazip ama YANLIŞTIR: yüklenen
	// görselin adresi ürün kaydına KALICI olarak yazılır ve işletim sistemi o
	// dizini temizlediğinde (ya da süreç başka bir makinede yeniden
	// başladığında) vitrindeki her görsel 404 döner. Kimse hata görmez; yalnızca
	// resimler kaybolur. Sessiz veri kaybı, açılışta patlayan bir yapılandırma
	// hatasından her zaman pahalıdır.
	//
	// Varsayılan bu yüzden KALICI ve göze görünür bir yoldur: depo köküne göre
	// "./data/uploads". Yerel geliştirmede "make up && make run" ek ayar
	// istemez (deponun kuralı) ve yeniden başlatmada dosyalar yerinde durur.
	//
	// PAYLAŞILAN ortamlarda mutlak bir yol (bağlanmış bir birim) verilmelidir;
	// göreli yol orada aynı sessiz kaybın yavaş çekimidir. Bu AÇILIŞI
	// DURDURMAZ, uyarılır — gerekçesi [Config.LocalFileRootIsDurable]
	// godoc'undadır.
	//
	// Boş bırakılması ayrı bir karardır ve config onu REDDEDER: kök olmadan
	// yerel sağlayıcı kaydedilemez, kaydedilmeyen bir sağlayıcı seçiliyse de
	// açılış zaten durur. Reddin burada olması, aynı sonucu iki adım önce ve
	// hangi değişkenin eksik olduğunu söyleyerek verir.
	FileRoot string `env:"FILE_ROOT" envDefault:"./data/uploads"`

	// FileMaxUploadBytes tek bir yüklemenin azami boyutudur (bayt).
	//
	// Sınırsız gövde, tek istekle diski doldurmanın en ucuz yoludur; bu yüzden
	// sınır vardır ve yapılandırılabilirdir. Varsayılan
	// [DefaultFileMaxUploadBytes]'tır (5 MiB) — ürün görseli için bol, bir
	// videoyu kazara yüklemek için dar.
	FileMaxUploadBytes int64 `env:"FILE_MAX_UPLOAD_BYTES" envDefault:"5242880"`

	// FileAllowedTypes yüklemede kabul edilen İÇERİK tipleridir.
	//
	// Liste bir İZİN LİSTESİDİR: burada olmayan tip reddedilir. Yasak listesi
	// olsaydı, bugün akla gelmemiş her biçim (bir belge, bir arşiv, bir betik)
	// varsayılan olarak kabul edilirdi.
	//
	// Değerler İÇERİKTEN tespit edilen tiple (net/http.DetectContentType)
	// karşılaştırılır, istemcinin Content-Type başlığıyla DEĞİL. Bu yüzden
	// biçim de dar tutulur: küçük harf, parametresiz ("image/png"). "Image/PNG"
	// ya da "image/png; charset=..." yazılmış bir giriş hiçbir zaman eşleşmez
	// ve izin listesi sessizce DARALIRDI — listede duran ama hiçbir dosyayı
	// geçirmeyen bir satır, en kötü türden yapılandırma hatasıdır.
	//
	// Varsayılan [DefaultFileAllowedTypes]'tır ve SVG İÇERMEZ; gerekçesi
	// internal/core/provider'daki içerik tipi sabitlerinin godoc'undadır
	// (özet: SVG bir belgedir, script taşır ve aynı kökenden sunulduğunda
	// depolanmış XSS olur).
	FileAllowedTypes []string `env:"FILE_ALLOWED_TYPES" envSeparator:"," envDefault:"image/jpeg,image/png,image/gif,image/webp"`

	// LogLevel yapısal log seviyesidir: debug | info | warn | error.
	LogLevel string `env:"LOG_LEVEL" envDefault:"info"`
	// LogFormat log çıktı biçimidir: json | text.
	LogFormat string `env:"LOG_FORMAT" envDefault:"json"`

	// ShutdownTimeout, SIGTERM sonrası açık isteklerin tamamlanması için
	// tanınan azami süredir.
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"15s"`
	// ReadinessDegradedTimeout, /ready ucunun DERECELENDİREN bağımlılıkları
	// (bugün yalnızca Redis) için YOKLAMA BAŞINA bütçesidir.
	//
	// Derecelendiren bir yoklamanın cevabını kimse beklemiyor — örnek iki hâlde
	// de hizmet veriyor — dolayısıyla yavaşlığının yapabileceği tek şey PROBE'u
	// düşürmektir: erişilemez bir Redis'e atılan tek Ping 1,7 saniye sürüyor
	// (istemci beş kez deniyor) ve kubelet'in readinessProbe.timeoutSeconds
	// varsayılanı 1'dir, yani bütçesiz bir yoklama pod'u NotReady yapardı —
	// ayrımın önlemek için var olduğu tam kesintiyi arka kapıdan geri getirerek.
	//
	// Varsayılan 250 ms'dir ve ayarlanabilir olması şarttır: Redis ağın öte
	// yanındaysa sağlıklı bir Ping de bu bütçeyi aşabilir ve kurulum sürekli
	// "degraded" okur. Bütçe SESSİZ DEĞİLDİR — aşıldığında /ready gövdesindeki
	// mesaj ve WARN satırı bütçenin kendisini yazar.
	ReadinessDegradedTimeout time.Duration `env:"READINESS_DEGRADED_TIMEOUT" envDefault:"250ms"`
	// ReadHeaderTimeout, yalnızca istek BAŞLIKLARININ okunması için tanınan süredir.
	ReadHeaderTimeout time.Duration `env:"READ_HEADER_TIMEOUT" envDefault:"10s"`
	// ReadTimeout, başlık + gövdenin tamamının okunması için tanınan süredir.
	// ReadHeaderTimeout tek başına gövdeyi bayt bayt akıtan Slowloris türevini
	// durdurmaz; bu sınır olmadan her bağlantı süresiz goroutine + fd tutar.
	ReadTimeout time.Duration `env:"READ_TIMEOUT" envDefault:"15s"`
	// WriteTimeout, yanıtın yazılması için tanınan süredir.
	WriteTimeout time.Duration `env:"WRITE_TIMEOUT" envDefault:"30s"`
	// IdleTimeout, keep-alive bağlantısının boşta bekleyebileceği süredir.
	IdleTimeout time.Duration `env:"IDLE_TIMEOUT" envDefault:"120s"`

	// OTLPEndpoint OpenTelemetry toplayıcısının gRPC adresidir (host:port).
	//
	// Varsayılanı YOKTUR: boş bırakıldığında izleme tamamen kapanır ve
	// uygulama hiçbir dış bağlantı denemez. Varsayılan bir adres koymak,
	// toplayıcısı olmayan her geliştirme ortamında sürekli bağlantı hatası
	// üretirdi.
	OTLPEndpoint string `env:"OTEL_EXPORTER_OTLP_ENDPOINT"`
	// OTLPInsecure toplayıcıya TLS'siz bağlanılacağını bildirir.
	//
	// Paylaşılan ortamlarda (staging ve production) true olması Validate
	// tarafından REDDEDİLİR: trace'ler istek yollarını, kimlikleri ve hata
	// mesajlarını taşır; şifresiz göndermek bunları ağda dinlenebilir kılar.
	OTLPInsecure bool `env:"OTEL_EXPORTER_OTLP_INSECURE" envDefault:"false"`
	// ServiceName trace ve metriklerde raporlanan servis adıdır.
	ServiceName string `env:"OTEL_SERVICE_NAME" envDefault:"gobit"`
	// TraceSampleRatio örneklenecek trace oranıdır (0.0 - 1.0).
	//
	// Varsayılan 1.0'dır çünkü örnekleme kararı geri alınamaz: kaydedilmemiş
	// bir trace sonradan kurtarılamaz. Yük arttıkça düşürülmelidir.
	TraceSampleRatio float64 `env:"OTEL_TRACES_SAMPLER_ARG" envDefault:"1.0"`
	// MetricInterval metriklerin toplayıcıya gönderilme sıklığıdır.
	//
	// Adı bilinçli olarak OTEL_ önekli DEĞİLDİR, oysa komşuları öyle.
	// OpenTelemetry belirtimi OTEL_METRIC_EXPORT_INTERVAL adını AYIRMIŞTIR ve
	// değerini MİLİSANİYE TAMSAYI olarak tanımlar; bu paket ise her süreyi Go
	// süresi olarak okur. İki anlam aynı ada sığmaz ve çakışma iki yönde de
	// keser:
	//
	//   - Belirtime uyan değer (60000) burada "birimi eksik" hatası verir ve
	//     uygulama HİÇ AÇILMAZ.
	//   - Buraya uyan değer (60s) OTel SDK'sının kendi okuyucusunda her
	//     açılışta ayrıştırma hatası loglar.
	//
	// Komşu OTEL_* adları korunur çünkü onların anlamı belirtimle UYUŞUR;
	// ödünç alınan ad ancak anlam da ödünç alınabildiğinde doğrudur.
	MetricInterval time.Duration `env:"METRIC_EXPORT_INTERVAL" envDefault:"60s"`

	// RateLimitPerMinute bir istemcinin dakikada yapabileceği istek sayısıdır.
	//
	// Sıfır ya da negatif değer hız sınırlamayı KAPATIR; "0 istek" anlamına
	// gelmez. Bkz. ADR 0007.
	RateLimitPerMinute int `env:"RATE_LIMIT_PER_MINUTE" envDefault:"600"`
	// TrustedProxyHops istekle aramızdaki GÜVENİLEN ters proxy sayısıdır.
	//
	// Sıfırsa X-Forwarded-For hiç okunmaz ve hız sınırı anahtarı bağlantının
	// RemoteAddr'ına düşer. Yanlış (fazla) bir değer ise istemcinin uydurduğu
	// adresi gerçek sanmaya ve hız sınırının tamamen atlanmasına yol açar.
	//
	// İki yanlışın bedeli AYNI DEĞİLDİR ve varsayılanı sıfır yapan da budur:
	// fazla verilen bir değer korumayı YOK EDER (saldırgan her istekte taze bir
	// kova alır), eksik verilen bir değer ise yalnızca GEVŞETİR — kota tüm
	// mağaza için tek bir kovaya düşer. İlki bir güvenlik açığı, ikincisi bir
	// kapasite sorunudur; bu yüzden güvenli varsayılan sıfırdır.
	//
	// Eksik değerin bedeli yine de küçük değildir ve SESSİZDİR: ters proxy,
	// ingress ya da CDN arkasında RemoteAddr her istekte proxy'nin adresidir,
	// yani RATE_LIMIT_PER_MINUTE "müşteri başına" değil "TÜM MAĞAZA için" bir
	// tavan olur ve tek bir müşteri vitrini kilitleyebilir. Paylaşılan
	// ortamlarda bu durum açılışta UYARILIR; gerekçesi ve neden açılışın
	// durmadığı [Config.RateLimitKeyIsPerClient] godoc'undadır.
	TrustedProxyHops int `env:"TRUSTED_PROXY_HOPS" envDefault:"0"`
	// IdempotencyTTL idempotency kayıtlarının saklanma süresidir.
	IdempotencyTTL time.Duration `env:"IDEMPOTENCY_TTL" envDefault:"24h"`
	// GuardBackend hız sınırı ve idempotency deposunun arka ucudur:
	// memory | redis.
	//
	// Varsayılan "memory"dir ve TEK ÖRNEKLİ kurulum içindir. Birden çok örnek
	// çalıştırılıyorsa "redis" ZORUNLUDUR; bellek içi depoyla hız sınırı örnek
	// sayısıyla çarpılır (bir hız sorunu) ve idempotency koruması örnekler
	// arasında HİÇ çalışmaz — aynı anahtarla farklı örneklere düşen iki istek
	// iki kez işlenir, yani iki sipariş. İkincisi bir doğruluk sorunudur.
	//
	// Tek bir anahtarın ikisini birden seçmesi bilinçlidir: ayrı ayrı
	// seçilebilseydi, idempotency'yi paylaşılan yapıp hız sınırını unutmak
	// gibi yarım bir yapılandırma mümkün olurdu ve o yarımlık ancak yük
	// altında görünürdü.
	GuardBackend string `env:"GUARD_BACKEND" envDefault:"memory"`
	// RedisKeyPrefix kurulumun Redis'teki ad alanı önekidir.
	//
	// ÜÇ tür anahtarı birden kapsar: koruma anahtarları "<önek>:rl:<istemci>"
	// ve "<önek>:idem:<anahtar>" (bkz. internal/core/http/redisguard paket
	// godoc'u), olay akışları ise "<önek>:events:<olay adı>" biçiminde yazılır;
	// olay veri yolunun consumer group adı da önekin kendisidir (bkz.
	// eventbus.RedisConfig.WithNamespace).
	//
	// AYNI Redis'i paylaşan iki gobit kurulumu (staging ile production, ya da
	// aynı kümedeki iki mağaza) bu değeri FARKLI vermelidir. Aynı bırakılırsa
	// üç arıza birden doğar ve ağırlıkları farklıdır:
	//
	//   - Birbirlerinin hız sınırı kotasını harcarlar. Hız sorunudur.
	//   - Birbirlerinin idempotency kaydını OKURLAR: bir kurulumun yanıtı
	//     ötekinin istemcisine gider. Doğruluk sorunudur.
	//   - AYNI consumer group'a bağlanırlar. En ağırı budur: grubun tanımı
	//     gereği bir olayı iki kurulumdan yalnızca BİRİ alır, yani üretimin
	//     "order.placed" olayı staging tarafından tüketilip yutulabilir ve
	//     sipariş onayı hiçbir yere gitmez.
	//
	// Öneki DEĞİŞTİRMEK de bedava değildir ve bunu bilerek yapmak gerekir:
	// yeni önek yeni stream ve yeni grup demektir, yani eski stream'de bekleyen
	// teslim edilmemiş olaylar orada KALIR. Değişiklik, kurulumu ayırmak için
	// yapılır; çalışan bir kurulumun öneki oynatılmaz.
	//
	// Ayrı Redis DB'si (redis://.../1) ya da ayrı örnek de ayırır ama ikisi de
	// ALTYAPI kararıdır: Redis Cluster numaralı DB'leri desteklemez ve ayrı
	// örnek para/operasyon maliyetidir. Önek aynı ayrımı yapılandırmayla yapar.
	//
	// AYRI bir değişkendir; var olan OTEL_SERVICE_NAME'e bağlanmaz. O ad
	// panolarda görünen servis adıdır ve gözlemlenebilirlik için değiştirilmesi
	// SIRADAN bir iştir; ikisini tek değişkene bağlamak, panoda yapılan bir
	// yeniden adlandırmanın çalışan kurulumun tüm idempotency kayıtlarını
	// sessizce terk etmesi demek olurdu.
	//
	// Varsayılanı [DefaultRedisKeyPrefix]'tir ve bugünkü davranışı korur; tek
	// kurulumlu ortamlarda dokunulması gerekmez. Biçimi GUARD_BACKEND'den
	// bağımsız doğrulanır: bellek içi arka uçta değer atıldır, ama yalnızca
	// backend değiştirilince patlayan bir yazım hatası, arızayı tam da en
	// kötü ana — canlı geçiş anına — saklamak olurdu.
	RedisKeyPrefix string `env:"REDIS_KEY_PREFIX" envDefault:"gobit"`

	// GraphQLMaxDepth bir GraphQL belgesinde iç içe geçebilecek alan sayısının
	// üst sınırıdır.
	//
	// Bu ayar REST tarafında karşılığı OLMAYAN bir riski kapatır: orada bir
	// isteğin maliyetini sunucu belirler (yol sabit, gövde sabit), GraphQL'de
	// ise sorgunun ŞEKLİNİ, yani maliyetini istemci yazar. Hız sınırlayıcı iki
	// yüzeyde de aynı şeyi sayar — bir istek.
	//
	// SIFIR VE NEGATİF DEĞER GEÇERSİZDİR ve açılışı durdurur; "0 = sınırsız"
	// gibi bir okuma bilinçli olarak YOKTUR. RATE_LIMIT_PER_MINUTE'ta sıfırın
	// "kapat" demesiyle karıştırılmamalı: hız sınırını kapatmak bir kapasite
	// tercihidir ve etkisi hemen görülür, derinlik sınırını kapatmak ise tek
	// bir sorgunun sunucuyu tüketmesine izin vermektir.
	GraphQLMaxDepth int `env:"GRAPHQL_MAX_DEPTH" envDefault:"10"`

	// GraphQLMaxComplexity tek bir GraphQL belgesinin tahmini maliyet tavanıdır.
	//
	// Birim "kaç alan çözülür"dür ve liste alanlarında eleman sayısıyla
	// çarpılır; bu yüzden sayı büyüktür (bkz. modülün graph paketi). Derinlik
	// sınırının yerine geçmez: sığ ama geniş bir belge — takma adlarla
	// yüzlerce kök sorgu ya da limit=100 ile yüz ürünün tüm varyantları —
	// derinlik testinden geçer, buradan geçemez.
	//
	// Sıfır ve negatif değer GEÇERSİZDİR; gerekçe [Config.GraphQLMaxDepth] ile
	// aynıdır.
	GraphQLMaxComplexity int `env:"GRAPHQL_MAX_COMPLEXITY" envDefault:"50000"`

	// GraphQLIntrospection GraphQL şemasının iç gözlemle (introspection)
	// okunabilmesini belirler.
	//
	// Varsayılan AÇIKTIR: vitrin şeması bu deponun içinde duran bir dosyadır ve
	// her kurulum aynısını sunar, yani kapatmak saldırgandan bir şey saklamaz,
	// yalnızca istemci araçlarını (kod üreteçleri, IDE'ler) körleştirir. Uç
	// zaten publishable anahtarın ve hız sınırının arkasındadır; maliyetini de
	// GRAPHQL_MAX_INTROSPECTION_ROOTS ve GRAPHQL_MAX_INTROSPECTION_DEPTH
	// bağlar. O iki ayar AYRIDIR çünkü iç gözlem alt ağacı derinlik ve
	// karmaşıklık hesaplarının DIŞINDA kalır — kapatmamak, sınırsız bırakmakla
	// aynı şey olurdu.
	//
	// Şemasına kendi alanlarını ekleyen bir kurulum için hesap değişir ve
	// anahtar bu yüzden vardır: false verildiğinde tüm yüzeyi tek istekte
	// döken sorgu kapanır. Anahtarın varlığı, kapalılığı bir kaza değil karar
	// yapar.
	GraphQLIntrospection bool `env:"GRAPHQL_INTROSPECTION" envDefault:"true"`

	// GraphQLMaxFieldRepetition aynı alanın aynı nesne altında kaç kez
	// seçilebileceğinin üst sınırıdır.
	//
	// Karmaşıklık sınırının GÖREMEDİĞİ riski kapatır: o model alan SAYISINI
	// fiyatlar, BAYT'ı değil. Aynı ağır alanı — örneğin bir ürün açıklamasını
	// — takma adlarla yüzlerce kez seçen belge tavanın ALTINDA kalır ve yanıtı
	// yüzlerce katına çıkarır. Ölçüldüğünde 8 KiB'lık bir istek 191 MiB yanıt
	// üretiyordu ve hız sınırlayıcı bunu BİR istek sayıyordu.
	//
	// Sayım kardeş kapsamlıdır ve takma adlar anahtara girmez: saldırının tek
	// aracı takma addır, meşru istemcinin aynı alanı farklı adla iki kez
	// istemesi ise olağandır.
	//
	// Sıfır ve negatif değer GEÇERSİZDİR; gerekçe [Config.GraphQLMaxDepth] ile
	// aynıdır.
	GraphQLMaxFieldRepetition int `env:"GRAPHQL_MAX_FIELD_REPETITION" envDefault:"20"`

	// GraphQLMaxResponseBytes tek bir GraphQL yanıtının azami boyutudur.
	//
	// Diğer sınırlardan farkı ölçtüğü şeydir: onlar belgeye bakıp maliyeti
	// TAHMİN eder, bu sınır gerçekleşen baytı SAYAR. Tahmin modeli bir gün
	// yanıldığında son kapı budur ve tam da yanılmanın görülemediği yerde
	// durur.
	//
	// Sıfır ve negatif değer GEÇERSİZDİR.
	GraphQLMaxResponseBytes int `env:"GRAPHQL_MAX_RESPONSE_BYTES" envDefault:"4194304"`

	// GraphQLMaxIntrospectionRoots bir belgedeki __schema/__type kökü
	// sayısının üst sınırıdır.
	//
	// İç gözlem alt ağacı hem derinlik hem karmaşıklık hesabının DIŞINDADIR
	// (gqlgen kendi yürüyüşünde de atlar), yani o iki sınır onu hiç görmez.
	// Ölçüldüğünde 63 KB'lık takma adlı bir belge 7,3 MiB yanıt veriyordu ve
	// en katı derinlik/karmaşıklık ayarıyla bile geçiyordu — aynı ayarla en
	// küçük meşru veri sorgusu reddedilirken.
	//
	// İki kök yeterlidir: hiçbir istemci aracı aynı belgede iki kez şema
	// istemez. Sıfır ve negatif değer GEÇERSİZDİR.
	GraphQLMaxIntrospectionRoots int `env:"GRAPHQL_MAX_INTROSPECTION_ROOTS" envDefault:"2"`

	// GraphQLMaxIntrospectionDepth iç gözlem alt ağacının derinlik tavanıdır.
	//
	// GRAPHQL_MAX_DEPTH'ten AYRI olması zorunludur: standart iç gözlem
	// sorgusunun ölçülen derinliği 13'tür ve veri yüzeyinin sınırı ona göre
	// kalibre edilseydi, vitrinin gerçek sorguları için gereğinden çok gevşek
	// kalırdı. İki sayaç ayrıldığı için veri sınırı 10'da durabiliyor.
	//
	// Sıfır ve negatif değer GEÇERSİZDİR.
	GraphQLMaxIntrospectionDepth int `env:"GRAPHQL_MAX_INTROSPECTION_DEPTH" envDefault:"15"`

	// GraphQLMaxSelections bir belgenin AÇILDIĞINDA ürettiği azami seçim
	// sayısıdır.
	//
	// Fragment açılımı ÜSSELDİR: birbirini iki kez çağıran 26 seviyelik bir
	// fragment zinciri 1,1 KB'lık geçerli ve döngüsüz bir belgedir ama 2^26
	// seçim açar. Tuzak tek bir sayaçta değildir — derinlik, alan tekrarı ve
	// gqlgen'in karmaşıklık yürüyüşü fragment tanımına belleksiz iner — bu
	// yüzden bütçe hepsinden ÖNCE koşar ve tükendiğinde gezinme yarıda kesilir:
	// sınırı uygularken sınırın engellediği işi yapmamak için.
	//
	// Sıfır ve negatif değer GEÇERSİZDİR.
	GraphQLMaxSelections int `env:"GRAPHQL_MAX_SELECTIONS" envDefault:"10000"`

	// Plugins kurulacak eklentilerin adlarıdır (virgülle ayrılır).
	//
	// Varsayılanı BOŞTUR: derlenmiş bir eklentinin varlığı onu kurmak için
	// yeterli değildir, açıkça seçilmesi gerekir. Sebep somut: eklentiler
	// yapılandırma ister (örn. payment-stripe için STRIPE_API_KEY) ve kurulum
	// o ayar yoksa açılışta HATA verir. Derlenen her eklentiyi otomatik
	// kurmak, tek bir eksik ortam değişkeni yüzünden uygulamanın hiç
	// açılmaması demek olurdu.
	//
	// Bilinmeyen bir ad açılışta hata verir; sessizce yok saymak, adı yanlış
	// yazılmış bir eklentinin "kurulu" sanılmasına yol açardı.
	Plugins []string `env:"PLUGINS" envSeparator:","`
}

// Load ortam değişkenlerini okuyup doğrulanmış bir Config döner.
func Load() (Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return Config{}, fmt.Errorf("config: ortam değişkenleri okunamadı: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate Config alanlarının kendi içinde tutarlı olduğunu doğrular.
// Load bunu otomatik çağırır; elle kurulan Config'ler için de kullanılabilir.
func (c Config) Validate() error {
	if !slices.Contains(validAppEnvs, c.AppEnv) {
		return fmt.Errorf("config: geçersiz APP_ENV %q (beklenen: %s)", c.AppEnv, strings.Join(validAppEnvs, ", "))
	}
	if c.AppPort < 1 || c.AppPort > 65535 {
		return fmt.Errorf("config: geçersiz APP_PORT %d (beklenen: 1-65535)", c.AppPort)
	}
	if !slices.Contains(validLogLevels, c.LogLevel) {
		return fmt.Errorf("config: geçersiz LOG_LEVEL %q (beklenen: %s)", c.LogLevel, strings.Join(validLogLevels, ", "))
	}
	if !slices.Contains(validLogFormats, c.LogFormat) {
		return fmt.Errorf("config: geçersiz LOG_FORMAT %q (beklenen: %s)", c.LogFormat, strings.Join(validLogFormats, ", "))
	}
	if !slices.Contains(validGuardBackends, c.GuardBackend) {
		return fmt.Errorf("config: geçersiz GUARD_BACKEND %q (beklenen: %s)", c.GuardBackend, strings.Join(validGuardBackends, ", "))
	}
	if !slices.Contains(validEventBuses, c.EventBus) {
		return fmt.Errorf("config: geçersiz EVENT_BUS %q (beklenen: %s)", c.EventBus, strings.Join(validEventBuses, ", "))
	}
	if c.DatabaseURL == "" {
		return fmt.Errorf("config: DATABASE_URL boş olamaz")
	}
	if c.RedisURL == "" {
		return fmt.Errorf("config: REDIS_URL boş olamaz")
	}
	if c.ReadinessDegradedTimeout <= 0 {
		return fmt.Errorf("config: READINESS_DEGRADED_TIMEOUT pozitif olmalı, %s verildi", c.ReadinessDegradedTimeout)
	}
	if c.ShutdownTimeout <= 0 {
		return fmt.Errorf("config: SHUTDOWN_TIMEOUT pozitif olmalı, %s verildi", c.ShutdownTimeout)
	}
	for _, t := range []struct {
		name  string
		value time.Duration
	}{
		{"READ_HEADER_TIMEOUT", c.ReadHeaderTimeout},
		{"READ_TIMEOUT", c.ReadTimeout},
		{"WRITE_TIMEOUT", c.WriteTimeout},
		{"IDLE_TIMEOUT", c.IdleTimeout},
	} {
		if t.value <= 0 {
			return fmt.Errorf("config: %s pozitif olmalı, %s verildi", t.name, t.value)
		}
	}
	if c.ReadTimeout < c.ReadHeaderTimeout {
		return fmt.Errorf("config: READ_TIMEOUT (%s), READ_HEADER_TIMEOUT'tan (%s) küçük olamaz", c.ReadTimeout, c.ReadHeaderTimeout)
	}

	if c.JWTTTL <= 0 {
		return fmt.Errorf("config: JWT_TTL pozitif olmalı, %s verildi", c.JWTTTL)
	}

	if c.TraceSampleRatio < 0 || c.TraceSampleRatio > 1 {
		return fmt.Errorf("config: OTEL_TRACES_SAMPLER_ARG 0.0-1.0 aralığında olmalı, %v verildi", c.TraceSampleRatio)
	}
	if c.MetricInterval <= 0 {
		return fmt.Errorf("config: METRIC_EXPORT_INTERVAL pozitif olmalı, %s verildi", c.MetricInterval)
	}
	if c.ServiceName == "" {
		return fmt.Errorf("config: OTEL_SERVICE_NAME boş olamaz")
	}
	if c.TrustedProxyHops < 0 {
		return fmt.Errorf("config: TRUSTED_PROXY_HOPS negatif olamaz, %d verildi", c.TrustedProxyHops)
	}
	if c.IdempotencyTTL <= 0 {
		return fmt.Errorf("config: IDEMPOTENCY_TTL pozitif olmalı, %s verildi", c.IdempotencyTTL)
	}
	if err := c.validateDBPool(); err != nil {
		return err
	}
	if err := c.validateRedisKeyPrefix(); err != nil {
		return err
	}
	if err := c.validateEventBusConsumer(); err != nil {
		return err
	}
	if err := c.validateGraphQL(); err != nil {
		return err
	}
	if err := c.validatePlugins(); err != nil {
		return err
	}
	if err := c.validateNotificationProvider(); err != nil {
		return err
	}
	// Dosya ayarları kendi ortam kapısını (paylaşılan ortamda mutlak yol)
	// taşır; bu yüzden aşağıdaki IsShared bloğuna değil, sıradan
	// doğrulamaların yanına konur.
	if err := c.validateFile(); err != nil {
		return err
	}
	// Ortama bağlı kapıyı kendi içinde taşır; bu yüzden aşağıdaki IsShared
	// bloğuna değil, sıradan doğrulamaların yanına konur.
	if err := c.validateAdminBootstrap(); err != nil {
		return err
	}

	// Üretimde yerel geliştirme varsayılanlarına düşmek, sabit-kodlu gobit:gobit
	// kimlik bilgisi ve sslmode=disable demektir. Eksik/boş secret enjeksiyonu
	// bu kontrol olmadan sessizce buraya düşerdi.
	//
	// Bu kapı staging'e GENİŞLETİLMEDİ: staging'in localhost'a bakması çalışan
	// bir kurulumda zaten mümkün değil (bağlantı ilk sorguda patlar), yani
	// buradaki kontrolün sessiz arıza örtme değeri yok. Aşağıdaki iki kapı ise
	// tam tersine sessiz arızayı önler; bkz. IsShared.
	if c.IsProduction() {
		if c.DatabaseURL == DefaultDatabaseURL {
			return fmt.Errorf("config: APP_ENV=production iken DATABASE_URL ezilmelidir (yerel geliştirme varsayılanı kullanılıyor)")
		}
		if c.RedisURL == DefaultRedisURL {
			return fmt.Errorf("config: APP_ENV=production iken REDIS_URL ezilmelidir (yerel geliştirme varsayılanı kullanılıyor)")
		}
	}

	if c.IsShared() {
		// Trace'ler istek yollarını, kimlikleri ve hata mesajlarını taşır;
		// şifresiz göndermek bunları ağda dinlenebilir kılar. staging'in
		// trafiği "gerçek değil" sayılsa bile, ağı ve jetonları gerçektir.
		if c.OTLPEndpoint != "" && c.OTLPInsecure {
			return fmt.Errorf("config: APP_ENV=%s iken OTEL_EXPORTER_OTLP_INSECURE=true olamaz", c.AppEnv)
		}
		// Boş bir imza sırrı iki ayrı arızadır: sabit bir sır herkesin kendine
		// admin jetonu üretebilmesi, üretilmiş rastgele bir sır ise örnekler
		// arası jeton geçersizliğidir. İkisi de sessizce açılmaktansa açılışta
		// durmayı hak eder.
		if len(c.JWTSecret) < minJWTSecretLen {
			return fmt.Errorf("config: APP_ENV=%s iken JWT_SECRET en az %d karakter olmalıdır", c.AppEnv, minJWTSecretLen)
		}
	}
	return nil
}

// validateDBPool PostgreSQL havuzu sınırlarının kendi içinde tutarlı olduğunu
// doğrular.
//
// Aynı üç kural internal/core/db'nin Config.Validate'inde de vardır ve tekrar
// BİLİNÇLİDİR; gerekçe [Config.validateRedisKeyPrefix]'teki ile aynı sınıftan.
// Buradaki kopyanın kazandırdığı somut şey ADLARDIR: db'nin hatası "MinConns
// (5) cannot be greater than MaxConns (1)" der ve operatörün elinde MinConns
// diye bir kol yoktur — hangi ortam değişkenini düzelteceğini bu kopya söyler.
// db'deki kopya ise config'ten GEÇMEYEN çağıranları (testler, gömen
// uygulamalar) korur.
//
// ÜST sınır konmadı; gerekçe [Config.validateGraphQL] ile aynıdır. Kümenin
// max_connections'ını ve o kümeye kaç örneğin bağlanacağını config bilemez,
// yani "çok büyük"ü ancak tahmin ederdi.
func (c Config) validateDBPool() error {
	if c.DBMaxConns < 1 {
		return fmt.Errorf("config: DB_MAX_CONNS en az 1 olmalı, %d verildi", c.DBMaxConns)
	}
	if c.DBMinConns < 0 {
		return fmt.Errorf("config: DB_MIN_CONNS negatif olamaz, %d verildi", c.DBMinConns)
	}
	if c.DBMinConns > c.DBMaxConns {
		return fmt.Errorf("config: DB_MIN_CONNS (%d), DB_MAX_CONNS'tan (%d) büyük olamaz",
			c.DBMinConns, c.DBMaxConns)
	}
	return nil
}

// validateRedisKeyPrefix koruma anahtarı ad alanı önekinin biçimini doğrular.
//
// Kabul edilen: en az bir karakter, ve yalnızca ASCII harf, rakam, '-', '_',
// '.'. Kural burada TEKRARLANIR; redisguard kurucuları aynı denetimi kendi
// içlerinde de yapar. Tekrar bilinçlidir: config bu paketi import EDEMEZ
// (redisguard bir Redis istemcisi taşır ve config en alt katmandır), ayrıca
// bir kütüphane çağıranına güvenmemelidir. Buradaki kopya arızayı AÇILIŞA
// taşır ve operatöre hangi ortam değişkeninin yanlış olduğunu söyler;
// redisguard'daki kopya ise config'ten geçmeyen çağıranları korur.
//
// Reddedilen karakterlerin gerekçesi redisguard.dogrulaOnek godoc'undadır;
// özeti: ':' iki kurulumun anahtarlarını ÇAKIŞTIRABİLİR, glob imleri
// operatörün "<önek>:idem:*" taramasını bozar, boşluk ve kontrol karakterleri
// görünmez oldukları için kurulumu fark edilmeden başka bir ad alanına taşır.
func (c Config) validateRedisKeyPrefix() error {
	if c.RedisKeyPrefix == "" {
		return fmt.Errorf("config: REDIS_KEY_PREFIX boş olamaz (varsayılan: %q)", DefaultRedisKeyPrefix)
	}
	if strings.ContainsFunc(c.RedisKeyPrefix, func(r rune) bool { return !validPrefixRune(r) }) {
		return fmt.Errorf(
			"config: geçersiz REDIS_KEY_PREFIX %q (yalnızca ASCII harf, rakam, '-', '_' ve '.' kabul edilir)",
			c.RedisKeyPrefix)
	}
	return nil
}

// validateEventBusConsumer olay veri yolu tüketici adının BİÇİMİNİ doğrular.
//
// [adBicimi] KULLANILMAZ çünkü o, boş değeri reddeder; burada boş değer
// geçerlidir ve "adı sen üret" demektir (bkz. [Config.EventBusConsumer]).
// Baştaki/sondaki boşluk yine de reddedilir: Redis tüketici adı olarak
// " gobit-1" gibi bir değeri sorunsuz kabul eder, yani yazım hatası hiçbir
// hata üretmez — yalnızca süreç, bir sonraki açılışta kendi bekleyen listesini
// bulamaz ve o mesajlar kimseye teslim edilmez.
//
// Adın BENZERSİZ olduğu burada denetlenemez: tek süreç, aynı gruba bağlı öteki
// süreçleri bilmez. O yüzden kullanılan ad açılışta loglanır.
func (c Config) validateEventBusConsumer() error {
	if c.EventBusConsumer == "" {
		return nil
	}
	if strings.TrimSpace(c.EventBusConsumer) != c.EventBusConsumer {
		return fmt.Errorf("config: EVENT_BUS_CONSUMER %q baştaki/sondaki boşluk içeremez",
			c.EventBusConsumer)
	}
	return nil
}

// validateGraphQL okuma yüzeyinin sınırlarını doğrular.
//
// Kural tek satırdır ve bilinçlidir: SINIR YÜKSELTİLEBİLİR, KALDIRILAMAZ.
// Sıfır ya da negatif bir değer, "sınır uygulanmasın" niyetiyle yazılmış bile
// olsa, kaynak tüketimini istemcinin yazdığı sorguya devretmek demektir; bu
// yüzden kabul edilmez ve açılışta durur. Sessizce varsayılana düşmek daha da
// kötü olurdu: operatör verdiği değerin uygulandığını sanırdı.
//
// ÜST sınır konmadı. "Çok büyük" bir değerle "sınırsız" arasındaki farkı
// config tahmin edemez; devasa bir katalogda meşru olabilecek bir tavanı
// açılışta reddetmek, korumadığı bir şey için çalışan kurulumu durdurmak
// olurdu. Buradaki kapı yalnızca ANLAMSIZ değeri eler.
func (c Config) validateGraphQL() error {
	if c.GraphQLMaxDepth < 1 {
		return fmt.Errorf("config: GRAPHQL_MAX_DEPTH en az 1 olmalı, %d verildi (sınır yükseltilebilir, kaldırılamaz)",
			c.GraphQLMaxDepth)
	}
	if c.GraphQLMaxFieldRepetition < 1 {
		return fmt.Errorf("config: GRAPHQL_MAX_FIELD_REPETITION en az 1 olmalı, %d verildi (sınır yükseltilebilir, kaldırılamaz)",
			c.GraphQLMaxFieldRepetition)
	}
	if c.GraphQLMaxResponseBytes < 1 {
		return fmt.Errorf("config: GRAPHQL_MAX_RESPONSE_BYTES en az 1 olmalı, %d verildi (sınır yükseltilebilir, kaldırılamaz)",
			c.GraphQLMaxResponseBytes)
	}
	if c.GraphQLMaxIntrospectionRoots < 1 {
		return fmt.Errorf("config: GRAPHQL_MAX_INTROSPECTION_ROOTS en az 1 olmalı, %d verildi (sınır yükseltilebilir, kaldırılamaz)",
			c.GraphQLMaxIntrospectionRoots)
	}
	if c.GraphQLMaxIntrospectionDepth < 1 {
		return fmt.Errorf("config: GRAPHQL_MAX_INTROSPECTION_DEPTH en az 1 olmalı, %d verildi (sınır yükseltilebilir, kaldırılamaz)",
			c.GraphQLMaxIntrospectionDepth)
	}
	if c.GraphQLMaxSelections < 1 {
		return fmt.Errorf("config: GRAPHQL_MAX_SELECTIONS en az 1 olmalı, %d verildi (sınır yükseltilebilir, kaldırılamaz)",
			c.GraphQLMaxSelections)
	}
	if c.GraphQLMaxComplexity < 1 {
		return fmt.Errorf("config: GRAPHQL_MAX_COMPLEXITY en az 1 olmalı, %d verildi (sınır yükseltilebilir, kaldırılamaz)",
			c.GraphQLMaxComplexity)
	}
	return nil
}

// validPrefixRune karakterin ad alanı önekinde kullanılabildiğini bildirir.
func validPrefixRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	default:
		return r == '-' || r == '_' || r == '.'
	}
}

// validatePlugins eklenti listesinin biçimini doğrular.
//
// Boş ve tekrarlanan adlar REDDEDİLİR: "PLUGINS=stripe,,stripe" gibi bir değer
// neredeyse her zaman elle düzenlenmiş bir ortam dosyasındaki hatadır ve
// tekrarlanan ad eklenti kaydında zaten çakışma üretirdi. Hangi adların
// GEÇERLİ olduğunu config bilmez; onu uygulamayı kuran taraf (cmd/server)
// bilir ve bilinmeyen adı orada reddeder.
func (c Config) validatePlugins() error {
	gorulen := make(map[string]struct{}, len(c.Plugins))
	for i, ad := range c.Plugins {
		if strings.TrimSpace(ad) == "" {
			return fmt.Errorf("config: PLUGINS listesinde %d. sırada boş ad var", i+1)
		}
		if _, dup := gorulen[ad]; dup {
			return fmt.Errorf("config: PLUGINS listesinde %q iki kez geçiyor", ad)
		}
		gorulen[ad] = struct{}{}
	}
	return nil
}

// validateNotificationProvider bildirim sağlayıcısı adının BİÇİMİNİ doğrular.
//
// Yalnızca biçim: adın kayıtlı olup olmadığını config bilemez (bkz.
// [Config.NotificationProvider]). Boş bir değer REDDEDİLİR — envDefault
// yüzünden ancak "NOTIFICATION_PROVIDER=" yazılarak üretilebilir ve o hâlde
// sağlayıcı kaydında boş kimlik aranırdı; kimse boş kimlikle kayıt yapamayacağı
// için sonuç, her siparişte hata dönen bir bildirim yolu olurdu.
//
// Baştaki/sondaki boşluk da reddedilir. Ortam dosyalarında bu, gözle
// görülmeyen ve en sık yapılan hatadır; sessizce kırpmak da yanlış olurdu:
// operatörün yazdığı değer ile sistemin kullandığı değer ayrışır ve bir sonraki
// yazım hatası (örn. iki kelimelik bir ad) yine sessizce başka bir sonuç
// verirdi.
func (c Config) validateNotificationProvider() error {
	return adBicimi("NOTIFICATION_PROVIDER", c.NotificationProvider, DefaultNotificationProvider)
}

// validateFile dosya yükleme ayarlarının BİÇİMİNİ ve kök dizin kuralını
// doğrular.
//
// Sağlayıcı adının kayıtlı olup olmadığı burada bilinemez (bkz.
// [Config.FileProvider]); kayıt denetimi kompozisyon kökündedir.
//
// Kök dizin, sağlayıcı "local" OLMASA da biçim açısından doğrulanır: değer
// yalnızca yerel sağlayıcıda kullanılır ama boş bırakılmış bir kök, sağlayıcı
// bir gün "local"a çevrildiğinde patlardı — yani arıza tam da en kötü ana,
// canlı geçiş anına saklanırdı. Aynı gerekçe REDIS_KEY_PREFIX'te de yazılıdır.
//
// KÖKÜN KALICI OLDUĞU BURADA DENETLENMEZ ve bu bilinçlidir. Kural bir
// doğrulama değil bir UYARIDIR: soruyu [Config.LocalFileRootIsDurable] sorar,
// cevabı açılışta cmd/server yazar (bkz. warnAboutFileRoot). Doğrulamaya
// konsaydı, dosya yükleme özelliğini hiç kullanmayan her paylaşılan kurulum
// karşılığını göremediği bir ortam değişkeni vermeden açılamazdı; gerekçenin
// tamamı o godoc'tadır. Buradaki tek iş BİÇİMDİR.
func (c Config) validateFile() error {
	if err := adBicimi("FILE_PROVIDER", c.FileProvider, DefaultFileProvider); err != nil {
		return err
	}
	if err := adBicimi("FILE_ROOT", c.FileRoot, DefaultFileRoot); err != nil {
		return err
	}
	if c.FileMaxUploadBytes <= 0 {
		return fmt.Errorf("config: FILE_MAX_UPLOAD_BYTES pozitif olmalı, %d verildi (varsayılan: %d)",
			c.FileMaxUploadBytes, DefaultFileMaxUploadBytes)
	}
	return c.validateFileTypes()
}

// LocalFileRootIsDurable "local" sağlayıcısının kök dizininin, süreç yeniden
// başladığında YERİNDE kalıp kalmayacağını bildirir.
//
// İki ayrı yol aynı sonuca çıkar ve ikincisi ilkinden daha sinsidir:
//
//   - GÖRELİ kök sürecin ÇALIŞMA DİZİNİNE göre çözülür ve konteynerde
//     neredeyse her zaman kalıcı OLMAYAN katmana düşer.
//   - GEÇİCİ kök (bkz. [geciciKokler]) MUTLAKTIR, yani "mutlak yol verin"
//     öğüdünü geçer ve hiçbir kuşku uyandırmaz; ama işletim sistemi onu
//     temizler, üstelik çoğu dağıtımda tmpfs olduğu için yeniden başlatmayı
//     bile beklemez.
//
// Sonuç ikisinde de aynıdır: bir sonraki dağıtımda yüklenen görseller
// kaybolur, ürün kaydındaki adres yerinde kalır — yani hiçbir hata görünmeden
// vitrindeki her görsel 404 döner. Bu, [Config.FileRoot] godoc'unun varsayılan
// için REDDETTİĞİ sessiz veri kaybının ta kendisidir; ölçüt yalnızca
// filepath.IsAbs olsaydı, reddedilen davranış FILE_ROOT=/tmp/... yazılarak
// tek satırda geri gelir ve uyarı susardı.
//
// # Neden AÇILIŞI DURDURMAZ
//
// Kural [Config.Validate]'e konsaydı, dosya yükleme özelliğini hiç
// kullanmayan (görsel adreslerini elle giren) her üretim kurulumu, karşılığını
// göremediği bir ortam değişkenini vermeden açılamazdı. Aynı ödünç
// GUARD_BACKEND'de de verildi: bellek içi koruma çok örnekli dağıtımda
// BOZUKTUR ama açılışı durdurmaz, uyarı loglanır (bkz. cmd/server
// guardStack). Buradaki karar onunla tutarlıdır — ve nedeni ortaktır:
// yapılandırmanın YANLIŞ olduğu kesin değildir, yalnızca RİSKLİDİR. Geçici bir
// kök, dosyaların kalıcı olmasını istemeyen bir kurulumda (önizleme ortamı,
// tek seferlik gösterim) bilinçli bir tercih olabilir.
//
// Kararın config'te durmasının sebebi, "riskli" tanımının burada olmasıdır:
// uyarıyı yazan taraf (cmd/server) yalnızca çağırır.
func (c Config) LocalFileRootIsDurable() bool {
	if c.FileProvider != DefaultFileProvider {
		return true
	}
	if !filepath.IsAbs(c.FileRoot) {
		return false
	}

	kok := filepath.Clean(c.FileRoot)
	// os.TempDir listeye AYRICA bakılır: TMPDIR ayarlanmış bir kurulumda geçici
	// dizin /tmp olmayabilir ve sabit liste onu göremezdi.
	if altindaMi(kok, filepath.Clean(os.TempDir())) {
		return false
	}
	for _, gecici := range geciciKokler {
		if altindaMi(kok, gecici) {
			return false
		}
	}

	return true
}

// geciciKokler işletim sisteminin temizlediği, bilinen mutlak kök dizinlerdir.
//
// Liste kısa TUTULUR: uzun bir liste "burada yoksa kalıcıdır" izlenimi verir ve
// uyarıyı bir güvenceye çevirirdi — oysa bu, kesin bir sınıflandırma değil,
// mutlak yol şartını geçen tipik hataların yakalanmasıdır.
var geciciKokler = []string{"/tmp", "/var/tmp", "/dev/shm"}

// altindaMi yolun verilen kökün kendisi ya da altında olduğunu bildirir.
//
// Ayırıcı şartı gerekli: sade bir önek karşılaştırması "/tmpfoo" yolunu da
// "/tmp" altında sayardı.
func altindaMi(yol, kok string) bool {
	return yol == kok || strings.HasPrefix(yol, kok+string(filepath.Separator))
}

// tarayicidaCalisanTipler yükleme izin listesine KONAMAYACAK içerik tipleridir.
//
// Biçim denetimi tek başına yetmez: FILE_ALLOWED_TYPES=image/png,text/html
// yazan bir kurulumda zincirin tamamı çalışır — http.DetectContentType bir
// HTML dosyası için gerçekten "text/html" döner, izin listesi onu geçirir ve
// dosya AYNI KÖKENDEN sunulur. Sonuç depolanmış XSS'tir.
//
// X-Content-Type-Options: nosniff bunu DURDURMAZ ve bu, başlığın ne işe
// yaradığının yanlış anlaşılmasıdır: nosniff, tarayıcının bildirilen tipi
// TAHMİNLE değiştirmesini engeller. Burada tahmin yoktur — yanıt gerçekten
// text/html'dir ve tarayıcı onu doğru biçimde çalıştırır.
//
// text/* önekinin tamamı reddedilir: yeni bir metin tipi (text/vtt, text/xsl…)
// listeye eklenmeyi bekleyemez ve yasak listesi olarak yazılan her kural,
// listelenmeyeni varsayılan olarak kabul eder.
var tarayicidaCalisanTipler = map[string]struct{}{
	"application/xhtml+xml":  {},
	"application/xml":        {},
	"image/svg+xml":          {},
	"application/pdf":        {},
	"application/javascript": {},
	"application/ecmascript": {},
}

// validateFileTypes izin listesinin biçimini doğrular.
//
// Boş liste REDDEDİLİR: sıfır tipin kabul edildiği bir yükleme ucu, her isteği
// reddeden ama var olmaya devam eden bir kapıdır. "Her şeyi kabul et" demenin
// yolu listeyi boşaltmak DEĞİLDİR — o karar, tipi tek tek yazmayı gerektirecek
// kadar bilinçli olmalıdır.
func (c Config) validateFileTypes() error {
	if len(c.FileAllowedTypes) == 0 {
		return fmt.Errorf("config: FILE_ALLOWED_TYPES boş olamaz (varsayılan: %q)",
			DefaultFileAllowedTypes)
	}

	gorulen := make(map[string]struct{}, len(c.FileAllowedTypes))
	for i, tip := range c.FileAllowedTypes {
		switch {
		case strings.TrimSpace(tip) == "":
			return fmt.Errorf("config: FILE_ALLOWED_TYPES listesinde %d. sırada boş tip var", i+1)
		case strings.TrimSpace(tip) != tip:
			return fmt.Errorf("config: FILE_ALLOWED_TYPES içindeki %q baştaki/sondaki boşluk içeremez", tip)
		// Parametreli ya da büyük harfli bir tip, tespit edilen tiple HİÇBİR
		// ZAMAN eşleşmez; sessizce kabul etmek listede duran ama hiçbir dosyayı
		// geçirmeyen bir satır bırakırdı.
		case strings.ContainsAny(tip, ";"), tip != strings.ToLower(tip), !strings.Contains(tip, "/"):
			return fmt.Errorf(
				"config: geçersiz FILE_ALLOWED_TYPES girdisi %q (küçük harf ve parametresiz olmalı, örn. %q)",
				tip, "image/png")
		}

		if _, tehlikeli := tarayicidaCalisanTipler[tip]; tehlikeli || strings.HasPrefix(tip, "text/") {
			return fmt.Errorf(
				"config: FILE_ALLOWED_TYPES %q kabul edemez: tarayıcı bu tipi BELGE olarak çalıştırır "+
					"ve dosyalar aynı kökenden sunulduğu için depolanmış XSS olur (nosniff bunu durdurmaz, "+
					"çünkü yanıt gerçekten o tiptir)", tip)
		}

		if _, dup := gorulen[tip]; dup {
			return fmt.Errorf("config: FILE_ALLOWED_TYPES listesinde %q iki kez geçiyor", tip)
		}
		gorulen[tip] = struct{}{}
	}

	return nil
}

// adBicimi boş bırakılamayan tek satırlık bir ayarın biçimini doğrular.
//
// Baştaki/sondaki boşluk REDDEDİLİR ve kırpılmaz; gerekçe
// [Config.validateNotificationProvider] godoc'undadır (özet: sessizce kırpmak,
// operatörün yazdığı değer ile sistemin kullandığı değeri ayırır).
func adBicimi(degisken, deger, varsayilan string) error {
	if deger == "" {
		return fmt.Errorf("config: %s boş olamaz (varsayılan: %q)", degisken, varsayilan)
	}
	if strings.TrimSpace(deger) != deger {
		return fmt.Errorf("config: %s %q baştaki/sondaki boşluk içeremez", degisken, deger)
	}

	return nil
}

// validateAdminBootstrap ilk yönetici tohumunun yapılandırmasını doğrular.
//
// YARIM YAPILANDIRMA REDDEDİLİR. İki değişkenden birini yazıp diğerini unutan
// operatör, sessiz atlamada tohumun çalıştığını sanır ve eksikliği ancak ilk
// giriş denemesinde — çoğu zaman kurulumdan günler sonra, kimsenin ortam
// dosyasına bakmayacağı bir anda — keşfeder. Açılışta durmak bu arızayı
// yapılandırmanın hâlâ elde olduğu ana taşır.
//
// Parola HATA MESAJINDA GEÇMEZ; yalnızca beklenen uzunluk bildirilir. Hata
// metni stderr'e ve oradan çoğu kurulumda log toplayıcısına düşer.
func (c Config) validateAdminBootstrap() error {
	if (c.AdminBootstrapEmail == "") != (c.AdminBootstrapPassword == "") {
		return fmt.Errorf("config: ADMIN_BOOTSTRAP_EMAIL ve ADMIN_BOOTSTRAP_PASSWORD birlikte verilmelidir (yalnızca biri verildi)")
	}
	if c.AdminBootstrapPassword == "" {
		return nil
	}
	if c.IsShared() && len(c.AdminBootstrapPassword) < MinBootstrapPasswordLen {
		return fmt.Errorf("config: APP_ENV=%s iken ADMIN_BOOTSTRAP_PASSWORD en az %d karakter olmalıdır",
			c.AppEnv, MinBootstrapPasswordLen)
	}
	return nil
}

// RateLimitKeyIsPerClient hız sınırı kotasının gerçekten İSTEMCİ BAŞINA düşüp
// düşmediğini bildirir.
//
// Sınırlayıcı istemciyi TRUSTED_PROXY_HOPS pozitifken X-Forwarded-For
// zincirinden, aksi hâlde bağlantının RemoteAddr'ından okur (bkz.
// corehttp.TrustedProxyIPKey). Ters proxy, ingress ya da CDN arkasında
// RemoteAddr HER İSTEKTE proxy'nin adresidir: o kurulumda
// RATE_LIMIT_PER_MINUTE "müşteri başına 600" değil "TÜM MAĞAZA için dakikada
// 600" olur ve tek bir müşteri bütün vitrini kilitleyebilir. Headless
// ticarette ters proxy arkasında çalışmak neredeyse tek dağıtım biçimi
// olduğundan, sessiz kalırsa en sık karşılaşılan hâl budur.
//
// Sınır KAPALIYKEN (RATE_LIMIT_PER_MINUTE <= 0) soru konusuzdur ve true döner:
// hiç takılmamış bir sınırlayıcının anahtarı hakkında uyarmak, operatörü
// ilgisiz bir ayara yönlendirirdi. O hâlin kendi bildirimi ayrıdır (bkz.
// cmd/server warnAboutRateLimit).
//
// # Neden varsayılan DEĞİŞMEDİ ve neden açılış DURMUYOR
//
// Sıfır atlama, doğrudan internete bakan bir kurulumda DOĞRU cevaptır ve
// yapılandırma hangisinin geçerli olduğunu bilemez — bu yüzden burada da
// [Config.LocalFileRootIsDurable]'ın ölçütü geçerlidir: yanlış olduğu kesin
// değil, riskli. Varsayılanı 1'e çekmek kolay cevaptır ama daha pahalıdır:
// güvenilmeyen bir X-Forwarded-For okumak, istemcinin uydurduğu adresi gerçek
// saymak demektir ve saldırgan her istekte taze bir kova alarak sınırı TAMAMEN
// atlar. Sessiz gevşeme bir kapasite sorunudur, sahtecilik ise korumanın
// kendisini yok eder; ikisi arasında güvenli varsayılan sıfırdır.
func (c Config) RateLimitKeyIsPerClient() bool {
	return c.RateLimitPerMinute <= 0 || c.TrustedProxyHops > 0
}

// SlogLevel LogLevel alanını slog.Level'a çevirir.
// Validate geçmiş bir Config için her zaman geçerli bir seviye döner.
func (c Config) SlogLevel() slog.Level {
	switch c.LogLevel {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// IsProduction üretim ortamında çalışılıp çalışılmadığını bildirir.
func (c Config) IsProduction() bool { return c.AppEnv == "production" }

// IsShared ortamın PAYLAŞILAN (yerel geliştirme dışı) bir ortam olduğunu bildirir.
//
// Sır ve TLS zorunluluklarının kapısı budur, IsProduction değil: staging ile
// production arasında güvenlik açısından anlamlı bir fark yoktur, ikisi de
// birden çok geliştiricinin ve birden çok SUNUCU ÖRNEĞİNİN paylaştığı
// ortamlardır.
//
// Somut arıza: staging çok örnekli çalışırken JWT_SECRET boş bırakılırsa her
// örnek açılışta kendi rastgele sırrını üretir (bkz. cmd/server jwtSecret);
// A örneğinden alınan jeton B örneğinde 401 döner. Yük dengeleyicinin
// dağıtımına bağlı olduğu için arıza ARALIKLIDIR ve teşhisi zordur —
// üretime çıkmadan yakalanması gereken sınıftan bile değildir, çünkü üretimde
// zaten aynı ayar zorunludur.
//
// Kolaylık yalnızca yerel geliştirmeye tanınır: orada tek örnek çalışır,
// jetonun yeniden başlatmada düşmesinin bedeli yok denecek kadar azdır ve
// "make up && make run" ek ayar istememelidir.
func (c Config) IsShared() bool { return c.AppEnv != devAppEnv }

// NeedsRedis yapılandırmanın bir Redis bağlantısı gerektirip gerektirmediğini
// bildirir.
//
// İki bağımsız özellik aynı istemciyi paylaşır: olay veri yolu ve koruma arka
// ucu. Sorunun tek bir yerde sorulması, "Redis'i kim açacak"ın iki ayrı yerde
// farklı cevaplanmasını önler — o ayrışma, biri Redis'teyken diğerinin
// sessizce bellek içinde kalması demek olurdu.
func (c Config) NeedsRedis() bool {
	return c.EventBus == BackendRedis || c.GuardBackend == BackendRedis
}

// Addr HTTP sunucusunun dinleyeceği adresi döner.
func (c Config) Addr() string { return fmt.Sprintf(":%d", c.AppPort) }
