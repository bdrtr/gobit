// Package eventbus modüller arası asenkron yan etkiler için olay veri yolunu
// sağlar (plan Bölüm 1 ve 5.4).
//
// Sözleşme tek, backend değiştirilebilirdir: NewInMemory geliştirme ve test
// için süreç içi bir veri yolu, NewRedisStream üretim için Redis Streams
// tabanlı kalıcı bir veri yolu üretir. Ortak olan yalnızca ARAYÜZDÜR; teslim,
// sıra, eşzamanlılık ve handler'a verilen BAĞLAM backend'e göre değişir ve
// aşağıdaki bölümlerde tek tek tanımlanmıştır. Backend değiştirmeden önce
// handler'ların bu garantilerin EN ZAYIFINA göre yazıldığından emin olun.
//
// # Teslim semantiği
//
// Publish asenkrondur ve handler'ları BEKLEMEZ; çağıran taraf yalnızca olayın
// kabul edildiğini öğrenir. Bu yüzden bir handler'ın hatası çağırana geri
// dönmez. InMemory backend'i en fazla bir kez (at-most-once) teslim eder ve
// süreç ölürse olay kaybolur; Redis backend'i en az bir kez (at-least-once)
// teslim eder ve süreç yeniden başladığında kaldığı yerden devam eder.
// Handler'lar bu yüzden idempotent yazılmalıdır (plan Bölüm 2.6).
//
// # Sıra ve eşzamanlılık garantileri
//
// Sözleşme tek olsa da iki backend'in eşzamanlılık davranışı AYNI DEĞİLDİR ve
// hiçbiri teslim sırasını garanti etmez:
//
//   - InMemory her handler çağrısını ayrı bir goroutine'de çalıştırır. Aynı
//     handler aynı anda birden çok olayla koşabilir ve olaylar yayım
//     sırasından farklı sırada teslim edilebilir.
//   - Redis backend'i bir stream'in mesajlarını tek tüketici döngüsünde sırayla
//     işler, ama aynı gruba birden çok süreç bağlandığında mesajlar süreçlere
//     dağıtılır ve genel sıra yine korunmaz.
//
// Bu yüzden handler'lar yalnızca idempotent değil, YENİDEN GİRİŞE UYGUN
// (reentrant) da yazılmalıdır: paylaşılan durum kilitlenmeli, sıraya bağlı
// kararlar Event.OccurredAt veya yükteki bir sürüm alanı üzerinden verilmeli,
// "önceki olay zaten işlendi" varsayımı yapılmamalıdır. Kesin sıralı, çok
// adımlı akışlar core/workflow'un saga motoruna aittir (plan Faz 3).
//
// # Hata ve yeniden deneme politikası
//
// Bir handler paniklerse panik yakalanır, yığın iziyle loglanır ve veri yolu
// ayakta kalır; diğer handler'lar etkilenmez. Bir handler hata dönerse hata
// loglanır ve olay işlenmiş sayılır — HİÇBİR backend otomatik yeniden deneme
// YAPMAZ. Bu bilinçli bir karardır: ölü mektup kuyruğu olmadan yapılan
// yeniden teslim, bozuk bir olayın (poison pill) tüketiciyi sonsuz döngüde
// kilitlemesine yol açar. Yeniden deneme ve telafi gerektiren işler
// core/workflow'un saga motoruna aittir (plan Faz 3); handler'ın kendi
// içinde yeniden denemesi de serbesttir.
//
// # Bağlam ve gözlemlenebilirlik
//
// Handler'a verilen ctx'in İPTAL davranışı iki backend'de aynıdır, DEĞERLERİ
// değildir (bkz. [Handler]): InMemory olayı süreç içinde taşıdığı için
// Publish'e verilen ctx'in değerleri handler'a ulaşır, Redis ise olayı
// SÜREÇLER ARASI taşır ve tüketici süreç yayımcının ctx'ini hiç görmez.
//
// Bunun ölçülen bedeli bir gözlemlenebilirlik farkıdır ve BİLİNÇLİ olarak
// kapatılmamıştır: in-memory kurulumda bir handler'ın logları yayımlayan
// isteğin request_id'sini taşır, Redis kurulumunda TAŞIMAZ. Yani bir olayı
// tetikleyen istek ile onun yan etkisi, üretim kurulumunda request_id
// üzerinden birbirine bağlanamaz.
//
// Farkı kapatmak teknik olarak ucuzdur — request_id mesaja bir alan olarak
// yazılıp tüketicide ctx'e geri konabilirdi — ama ÜÇ bedeli vardır ve
// toplamı, kazandırdığı tek log alanından pahalıdır:
//
//   - Veri yolu, hangi ctx değerlerinin taşınmaya değer olduğunu BİLMEK
//     zorunda kalırdı. request_id çekirdeğin HTTP katmanının anahtarıdır;
//     taşımak, taşıma katmanı olan bu paketi o katmana bağlar ve listeye her
//     eklenen değer bağı büyütür.
//   - Sonuç YARIM bir doğruluk olurdu: request_id dolu ama logger, kimlik ve
//     izleme span'i boş bir ctx, handler yazarını "hangi değer var" diye
//     tahmine iter. Bugünkü kural tahmine yer bırakmıyor: Redis'te HİÇBİRİ
//     yok.
//   - En az bir kez teslim, mesajı süreç yeniden başladıktan sonra da
//     verebilir. O anda geri konan request_id, çoktan bitmiş bir isteğe ait
//     olurdu; log satırı canlı bir isteğe aitmiş gibi görünür ve bu, hiç
//     korelasyon olmamasından daha yanıltıcıdır.
//
// İki backend'de de çalışan korelasyon yolu Event'in KENDİSİDİR: Event.ID
// mesajla birlikte taşınır ve handler hatalarında zaten loglanır (event_id),
// yayımlayan taraf isteğe bağlı olarak request_id'yi Event.Data'ya AÇIKÇA
// koyabilir. Data, iki backend'in de taşıdığı tek şeydir.
//
// Bu karar, dağıtık izleme (trace context) gerçekten istendiğinde yeniden
// açılır. O zaman taşınacak şey request_id değil W3C traceparent'tır, taşıma
// yeri yine mesajın kendisidir ve tüketici tarafında span, kopyalanmış bir
// değer olarak değil AÇIKÇA sürdürülür.
package eventbus

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"log/slog"
	"maps"
	"runtime/debug"
	"sync"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// Event veri yolunda taşınan tek bir olaydır.
type Event struct {
	// Name olayın "modül.eylem" biçimindeki adıdır (örn. "order.placed").
	// Abonelikler bu ada göre eşleşir; Redis backend'inde her ad ayrı bir
	// stream'e karşılık gelir. Boş bırakılamaz.
	Name string

	// Data olayın taşıdığı yüktür. Redis backend'inde JSON'a serileştirildiği
	// için yalnızca JSON'a çevrilebilir değerler içermelidir. Handler'lar bu
	// haritayı SALT OKUNUR kabul etmelidir; üst düzey anahtarlar her handler
	// için ayrı kopyalanır, iç içe değerler paylaşılmaya devam eder.
	Data map[string]any

	// ID olayın tekil kimliğidir ve yayımlayan tarafından verilebilir; boş
	// bırakılırsa zaman sıralı bir kimlik üretilir. Tüketiciler bu kimliği
	// idempotency anahtarı olarak kullanabilir.
	ID string

	// OccurredAt olayın gerçekleştiği andır ve her zaman UTC'ye çevrilir.
	// Sıfır değer verilirse Publish anı kullanılır.
	OccurredAt time.Time
}

// Handler bir olayı işleyen fonksiyondur.
//
// Verilen ctx hiçbir backend'de İPTAL DEVRALMAZ: çağıranın isteği bitse de,
// Shutdown çağrılsa da işleme yarıda kesilmez. Ctx'in DEĞERLERİ ise backend'e
// göre değişir:
//
//   - InMemory: ctx, Publish'e verilen ctx'ten türetilir; istek değerleri
//     (örn. request_id) korunur.
//   - Redis: olay SÜREÇLER ARASI gider. Tüketici süreç yayımcının ctx'ini HİÇ
//     görmez; handler, veri yolunun kendi kök ctx'inden türeyen ve hiçbir
//     istek değeri TAŞIMAYAN bir ctx alır.
//
// Handler bu yüzden ctx'teki değerlere GÜVENMEMELİDİR. Varsayılan backend
// in-memory olduğundan, ctx'te bir şey taşıyan tasarım testlerde ve yerel
// geliştirmede yeşil geçer, üretimde sessizce boş okur: ihtiyaç duyulan her
// şey Event.Data'da olmalıdır. Korelasyonun iki backend'de de çalışan hâli
// için bkz. paket yorumundaki "Bağlam ve gözlemlenebilirlik".
//
// Dönen hata çağırana ulaşmaz, yalnızca loglanır.
type Handler func(ctx context.Context, e Event) error

// EventBus olay yayımlama ve abonelik sözleşmesidir.
type EventBus interface {
	// Publish olayı yayımlar ve handler'ları beklemeden döner.
	Publish(ctx context.Context, e Event) error
	// Subscribe verilen olay adına bir handler bağlar.
	//
	// Bilinçli olarak context.Context almaz: abonelik bir isteğe değil,
	// sürecin ömrüne bağlıdır (plan Bölüm 5.4 imzası). Yaşam döngüsü Shutdown
	// ile yönetilir.
	Subscribe(eventName string, h Handler) error
	// Shutdown veri yolunu kapatır ve çalışan handler'ların bitmesini bekler.
	//
	// Bekleme ctx ile sınırlıdır: süre dolarsa takılan handler'lar beklenmez
	// ve errors.KindUnavailable / CodeShutdownTimeout döner. Veri yolu her
	// hâlükârda kapanır; dönüşten sonra Publish ve Subscribe hata döner.
	// İmza container.Shutdowner ile uyumludur.
	Shutdown(ctx context.Context) error
}

// Hata kodları; çağıran taraf errors.CodeOf ile bunlara göre dallanabilir.
const (
	// CodeClosed kapatılmış bir veri yolunun kullanıldığını bildirir.
	CodeClosed = "eventbus_closed"
	// CodeInvalidEvent olayın geçersiz veya serileştirilemez olduğunu bildirir.
	CodeInvalidEvent = "eventbus_invalid_event"
	// CodeInvalidConfig veri yolu ayarlarının geçersiz olduğunu bildirir.
	CodeInvalidConfig = "eventbus_invalid_config"
	// CodePublishFailed yayımlamanın backend seviyesinde başarısız olduğunu bildirir.
	CodePublishFailed = "eventbus_publish_failed"
	// CodeSubscribeFailed aboneliğin kurulamadığını bildirir.
	CodeSubscribeFailed = "eventbus_subscribe_failed"
	// CodeShutdownTimeout kapanış beklenirken sürenin dolduğunu bildirir.
	CodeShutdownTimeout = "eventbus_shutdown_timeout"
)

// Log kayıtlarında kullanılan sabit anahtarlar.
const (
	attrEvent   = "event"
	attrEventID = "event_id"
	attrError   = "error"
	attrStream  = "stream"
)

// idPrefix üretilen olay kimliklerinin önekidir (plan Bölüm 8).
const idPrefix = "evt_"

// idEncoding Crockford Base32 alfabesiyle dolgusuz kodlamadır. Alfabe ASCII'de
// artan sırada olduğundan kodlanmış dize, kodlanan baytlarla aynı sözlüksel
// sırayı korur; kimlikler bu sayede zamana göre sıralanabilir kalır.
var idEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// newEventID zaman sıralı ve tekil bir olay kimliği üretir.
//
// Yapısı ULID ile aynıdır: 48 bit milisaniye zaman damgası + 80 bit
// kriptografik rastgelelik, Crockford Base32 ile 26 karaktere kodlanır.
func newEventID(t time.Time) string {
	ms := t.UTC().UnixMilli()
	if ms < 0 {
		// 1970 öncesi bir zaman damgası olay için anlamlı değildir; sıralamayı
		// bozmamak için tabana çekilir.
		ms = 0
	}

	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], uint64(ms))

	var buf [16]byte
	// UnixMilli 48 bite sığar; ilk iki bayt daima sıfırdır ve atılır.
	copy(buf[:6], stamp[2:])
	if _, err := rand.Read(buf[6:]); err != nil {
		// crypto/rand.Read hata dönmez; yine de bir gün dönerse kimlik
		// yalnızca nanosaniye çözünürlüğüne dayanır — tekillik zayıflar ama
		// yayımlama başarısız olmaz.
		binary.BigEndian.PutUint64(buf[8:], uint64(t.UnixNano()))
	}

	return idPrefix + idEncoding.EncodeToString(buf[:])
}

// normalize olayı doğrular ve boş bırakılan alanlarını doldurur.
func normalize(e Event) (Event, error) {
	if e.Name == "" {
		return Event{}, errors.Invalid(CodeInvalidEvent, "olay adı boş olamaz")
	}

	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now().UTC()
	} else {
		e.OccurredAt = e.OccurredAt.UTC()
	}

	if e.ID == "" {
		e.ID = newEventID(e.OccurredAt)
	}

	return e, nil
}

// deliverable handler'a verilecek olay kopyasını üretir.
//
// Data sığ kopyalanır; böylece bir handler'ın üst düzey anahtarlarda yaptığı
// değişiklik aynı anda çalışan diğer handler'ları etkilemez ve yarış durumuna
// yol açmaz. İç içe değerler paylaşılmaya devam eder.
func deliverable(e Event) Event {
	e.Data = maps.Clone(e.Data)
	return e
}

// invokeHandler handler'ı panik ve hata güvenli biçimde çağırır.
//
// Panik yakalanıp yığın iziyle loglanır, hata yalnızca loglanır; ikisi de
// veri yolunu durdurmaz ve yeniden denemeye yol açmaz (bkz. paket yorumu).
func invokeHandler(ctx context.Context, log *slog.Logger, e Event, h Handler) {
	defer func() {
		if r := recover(); r != nil {
			log.ErrorContext(ctx, "olay işleyicisi panikledi",
				attrEvent, e.Name,
				attrEventID, e.ID,
				"panic", r,
				"stack", string(debug.Stack()),
			)
		}
	}()

	if err := h(ctx, deliverable(e)); err != nil {
		log.ErrorContext(ctx, "olay işleyicisi hata döndü",
			attrEvent, e.Name,
			attrEventID, e.ID,
			attrError, err,
		)
	}
}

// awaitHandlers çalışan handler'ların bitmesini ctx ile sınırlı biçimde bekler.
//
// ctx süresi dolarsa bekleme bırakılır ve CodeShutdownTimeout kodlu tipli hata
// döner; takılan handler'lar süreçle birlikte sonlanır. Her iki backend'in
// Shutdown'ı bu davranışı paylaşır.
func awaitHandlers(ctx context.Context, wg *sync.WaitGroup) error {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return errors.Wrap(ctx.Err(), errors.KindUnavailable, CodeShutdownTimeout,
			"olay veri yolu kapatılırken süre doldu; çalışan handler'lar beklenemedi")
	}
}

// closedPublishError kapatılmış veri yolunda yayım için tipli hatayı üretir.
func closedPublishError(eventName string) error {
	return errors.Unavailable(CodeClosed,
		"olay veri yolu kapatıldı: %q yayımlanamaz", eventName)
}

// closedSubscribeError kapatılmış veri yolunda abonelik için tipli hatayı üretir.
func closedSubscribeError(eventName string) error {
	return errors.Unavailable(CodeClosed,
		"olay veri yolu kapatıldı: %q olayına abone olunamaz", eventName)
}

// orDefaultLogger nil logger yerine sürecin varsayılan logger'ını döner.
func orDefaultLogger(log *slog.Logger) *slog.Logger {
	if log == nil {
		return slog.Default()
	}
	return log
}
