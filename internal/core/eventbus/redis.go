package eventbus

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// Redis backend'inin varsayılan ayarları.
const (
	// DefaultStreamPrefix stream anahtarlarının varsayılan önekidir.
	// "order.placed" olayı "gobit:events:order.placed" stream'ine yazılır.
	//
	// Değer elle yazılmaz, [DefaultGroup]'tan TÜRETİLİR: ikisi tek bir ad
	// alanının iki yüzüdür ve [RedisConfig.WithNamespace] onları aynı önekten
	// üretir. Elle yazılsalardı, birini değiştirip ötekini unutan bir
	// düzenleme varsayılan kurulumu sessizce yarım ayrılmış bırakırdı.
	DefaultStreamPrefix = DefaultGroup + ":" + streamSegment
	// DefaultGroup varsayılan consumer group adıdır.
	DefaultGroup = "gobit"
	// DefaultBlockTimeout XREADGROUP'un varsayılan bloklama süresidir.
	DefaultBlockTimeout = 5 * time.Second
	// DefaultBatchSize bir turda okunacak varsayılan mesaj sayısıdır.
	DefaultBatchSize = 16
	// DefaultMaxLen stream'in varsayılan yaklaşık uzunluk sınırıdır.
	DefaultMaxLen = 10_000
	// MaxLenUnlimited RedisConfig.MaxLen'e verildiğinde stream kırpması kapanır.
	MaxLenUnlimited = -1
)

// Redis backend'inin iç sabitleri.
const (
	// streamSegment ad alanı öneki ile olay adı arasındaki sabit parçadır.
	// Anahtar "<ad alanı>:events:<olay adı>" biçiminde kurulur; parçanın
	// varlığı, aynı ad alanını kullanan koruma anahtarlarıyla ("<ad
	// alanı>:rl:*", "<ad alanı>:idem:*") olay akışlarının Redis'te
	// karışmamasını sağlar.
	streamSegment = "events"
	// cursorNew XREADGROUP'un "hiçbir tüketiciye verilmemiş mesajlar" imidir.
	cursorNew = ">"
	// cursorPending XREADGROUP'un "bu tüketicinin bekleyen mesajları" imidir.
	cursorPending = "0"
	// groupStart yeni oluşturulan consumer group'un başlangıç konumudur.
	// "0" seçilir ki abone olmadan önce yayımlanmış olaylar da teslim edilsin.
	groupStart = "0"
	// controlTimeout kısa ömürlü yönetim komutlarının (XGROUP, XACK) süresidir.
	controlTimeout = 5 * time.Second
	// readErrorBackoff okuma hatasından sonra beklenen süredir; hatalı bir
	// Redis'e saniyede binlerce istek gönderilmesini engeller.
	readErrorBackoff = time.Second
)

// Stream mesajındaki alan adları.
const (
	fieldID         = "id"
	fieldName       = "name"
	fieldOccurredAt = "occurred_at"
	fieldData       = "data"
)

// RedisConfig Redis Streams backend'inin ayarlarıdır.
//
// Tüm alanlar isteğe bağlıdır; boş bırakılanlar Default* sabitleriyle doldurulur.
type RedisConfig struct {
	// StreamPrefix stream anahtarlarının önekidir; anahtar
	// "<StreamPrefix>:<olay adı>" olarak kurulur.
	StreamPrefix string

	// Group consumer group adıdır. Aynı gruba bağlı tüketiciler bir mesajı
	// yalnızca BİR KEZ alır; ölçekleme bu şekilde yapılır. Farklı grup adları
	// aynı olayı bağımsız olarak tüketir (fan-out).
	Group string

	// Consumer bu süreci grup içinde tanımlayan addır. Süreç yeniden
	// başladığında aynı ad kullanılırsa, işlenip ACK'lenmemiş mesajlar
	// (pending list) yeniden bu sürece teslim edilir. Boşsa "<hostname>-<pid>"
	// kullanılır; kalıcı bir kimlik isteniyorsa (örn. StatefulSet pod adı)
	// açıkça verilmelidir (bkz. [ConsumerName]).
	//
	// AYNI ad iki SÜRECE verilmemelidir ve bu, [RedisConfig.Group]'un tam
	// tersidir: grup adını paylaşmak ölçeklemenin ta kendisidir, tüketici adını
	// paylaşmak ise bekleyen listeyi paylaşmaktır. Açılışta her süreç kendi
	// adının bekleyen listesini okur (consume, cursorPending), yani ötekinin
	// HÂLÂ işlemekte olduğu mesajları da alır ve aynı olay iki kez işlenir.
	// Veri yolu bunu göremez — tek süreç, kendisinden başkasını bilmez.
	Consumer string

	// BlockTimeout XREADGROUP'un mesaj beklerken bloklanacağı süredir. Küçük
	// değer daha hızlı kapanma ama daha çok boş tur demektir.
	BlockTimeout time.Duration

	// BatchSize tek turda okunacak en fazla mesaj sayısıdır.
	BatchSize int64

	// MaxLen stream'in yaklaşık üst sınırıdır (XADD MAXLEN ~ N); eski girdiler
	// bu sınırın üstünde kırpılır. MaxLenUnlimited verilirse kırpma kapanır ve
	// stream sınırsız büyür.
	MaxLen int64
}

// StreamName verilen olay adına karşılık gelen Redis stream anahtarını döner.
// Operasyon ve testlerin anahtarı elle kurmak zorunda kalmaması için dışa verilir.
func (c RedisConfig) StreamName(eventName string) string {
	return c.withDefaults().StreamPrefix + ":" + eventName
}

// WithNamespace stream önekini ve consumer group adını verilen ad alanından
// türeten bir kopya döner.
//
// # Neden İKİSİ birden
//
// AYNI Redis'i paylaşan iki kurulumun olayları da ayrılmalıdır ve ayrımın
// yalnızca stream anahtarında yapılması YETMEZ. Grup paylaşımı daha kötüdür:
// consumer group'un tanımı gereği bir mesajı gruptaki tüketicilerden yalnızca
// BİRİ alır, yani üretimin "order.placed" olayı staging tarafından tüketilip
// yutulabilir — sipariş onayı hiç gitmez ve hiçbir yerde hata görünmez.
// Anahtarın ikisini birden ayırması bu yüzden bir kolaylık değil şarttır;
// ayrı ayrı verilebilseydi, stream'i ayırıp grubu unutmak mümkün olurdu ve o
// yarımlık tam olarak yukarıdaki arızayı üretirdi.
//
// # Neden Consumer'a dokunulmaz
//
// O alan kurulumları değil, aynı gruptaki SÜREÇLERİ ayırır ve ad alanıyla
// ilgisi yoktur; gerekçesi [RedisConfig.Consumer] godoc'undadır.
//
// Boş ad alanı hiçbir şeyi değiştirmez: ":events" biçiminde başsız bir anahtar,
// ayıracak bir AD olmadan yapılmış bir ayrım olurdu ve varsayılanları korumak
// (bkz. [DefaultStreamPrefix]) daha dürüst bir cevaptır.
func (c RedisConfig) WithNamespace(namespace string) RedisConfig {
	if namespace == "" {
		return c
	}

	c.StreamPrefix = namespace + ":" + streamSegment
	c.Group = namespace

	return c
}

// withDefaults boş bırakılan alanları varsayılanlarla doldurulmuş bir kopya döner.
func (c RedisConfig) withDefaults() RedisConfig {
	if c.StreamPrefix == "" {
		c.StreamPrefix = DefaultStreamPrefix
	}
	if c.Group == "" {
		c.Group = DefaultGroup
	}
	c.Consumer = ConsumerName(c.Consumer)
	if c.BlockTimeout <= 0 {
		c.BlockTimeout = DefaultBlockTimeout
	}
	if c.BatchSize <= 0 {
		c.BatchSize = DefaultBatchSize
	}
	if c.MaxLen == 0 {
		c.MaxLen = DefaultMaxLen
	}
	return c
}

// ConsumerName verilen tüketici adını, boş bırakılmışsa süreç başına türetilmiş
// bir adla tamamlar.
//
// [RedisConfig.withDefaults] zaten aynı işi yapar; bu işlev DIŞA AÇIKTIR ki
// veri yolunu kuran taraf, kullanılacak adı açılışta LOGLAYABİLSİN. Loglamak
// tek fark edilme şansıdır: aynı adın iki sürece verilmesi çift işlemeye yol
// açar (bkz. [RedisConfig.Consumer]) ve bu, ancak iki açılış logu yan yana
// konduğunda görülebilecek bir arızadır — çalışma zamanında hiçbir hata
// üretmez, yalnızca bazı olaylar iki kez işlenir.
func ConsumerName(name string) string {
	if name != "" {
		return name
	}

	return defaultConsumerName()
}

// defaultConsumerName süreci grup içinde ayırt eden bir ad üretir.
func defaultConsumerName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "gobit"
	}
	return host + "-" + strconv.Itoa(os.Getpid())
}

// streamClient redisBus'ın kullandığı Redis Streams komut kümesidir.
//
// Üretimde doğrudan *redis.Client karşılar. Ayrı bir arayüz olmasının nedeni
// tüketim döngüsünün (consume, dispatch, ack) Docker'sız birim testine açık
// kalmasıdır; imzalar go-redis'inkiyle birebir aynıdır ki adaptör gerekmesin.
type streamClient interface {
	XAdd(ctx context.Context, a *redis.XAddArgs) *redis.StringCmd
	XReadGroup(ctx context.Context, a *redis.XReadGroupArgs) *redis.XStreamSliceCmd
	XAck(ctx context.Context, stream, group string, ids ...string) *redis.IntCmd
	XGroupCreateMkStream(ctx context.Context, stream, group, start string) *redis.StatusCmd
}

var _ streamClient = (*redis.Client)(nil)

// redisBus Redis Streams üzerine kurulu, kalıcı bir EventBus'tır.
type redisBus struct {
	client streamClient
	cfg    RedisConfig
	log    *slog.Logger

	// ctx tüketici döngülerinin ömrünü yönetir. Subscribe sözleşme gereği
	// context.Context almadığı için veri yolu kendi kökünü taşır; iptali
	// Shutdown tetikler.
	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.RWMutex
	closed   bool
	handlers map[string][]Handler

	// setupMu tüketici kurulumunu serileştirir ve consuming haritasını korur.
	// mu'dan AYRI olmasının nedeni, consumer group oluşturmanın gerçek bir ağ
	// turu olmasıdır: mu'nun yazma kilidi altında yapılsaydı Redis yavaşken
	// eşzamanlı tüm Publish'ler (mu.RLock) o süre boyunca bloklanırdı.
	setupMu   sync.Mutex
	consuming map[string]struct{}

	wg sync.WaitGroup
}

var _ EventBus = (*redisBus)(nil)

// NewRedisStream Redis Streams tabanlı bir EventBus üretir.
//
// Her olay adı ayrı bir stream'e ("<önek>:<olay adı>"), her abonelik
// cfg.Group consumer group'una karşılık gelir; işlenen mesaj XACK'lenir ve
// süreç yeniden başladığında tüketim kaldığı yerden sürer. client'ın sahibi
// çağırandır: Shutdown istemciyi kapatmaz.
//
// log nil verilirse slog.Default kullanılır. client nil ise errors.KindInvalid
// döner.
func NewRedisStream(client *redis.Client, cfg RedisConfig, log *slog.Logger) (EventBus, error) {
	if client == nil {
		return nil, errors.Invalid(CodeInvalidConfig, "redis istemcisi nil olamaz")
	}
	return newRedisBus(client, cfg, log), nil
}

// newRedisBus veri yolunu istemci soyutlamasıyla kurar.
//
// Paket içi testler tüketim döngüsünü gerçek Redis olmadan sınamak için bunu
// kullanır; dışa verilen yol NewRedisStream'dir.
func newRedisBus(client streamClient, cfg RedisConfig, log *slog.Logger) *redisBus {
	ctx, cancel := context.WithCancel(context.Background())
	return &redisBus{
		client:    client,
		cfg:       cfg.withDefaults(),
		log:       orDefaultLogger(log),
		ctx:       ctx,
		cancel:    cancel,
		handlers:  make(map[string][]Handler),
		consuming: make(map[string]struct{}),
	}
}

// Publish olayı ilgili stream'e yazar ve handler'ları beklemeden döner.
//
// Data JSON'a serileştirilir; serileştirilemeyen bir değer içeriyorsa
// errors.KindInvalid, Redis'e yazılamazsa errors.KindUnavailable döner.
func (b *redisBus) Publish(ctx context.Context, e Event) error {
	e, err := normalize(e)
	if err != nil {
		return err
	}

	if b.isClosed() {
		return closedPublishError(e.Name)
	}

	payload, err := json.Marshal(e.Data)
	if err != nil {
		return errors.Wrap(err, errors.KindInvalid, CodeInvalidEvent,
			"%q olayının verisi JSON'a çevrilemedi", e.Name)
	}

	args := &redis.XAddArgs{
		Stream: b.cfg.StreamName(e.Name),
		Values: map[string]any{
			fieldID:         e.ID,
			fieldName:       e.Name,
			fieldOccurredAt: e.OccurredAt.Format(time.RFC3339Nano),
			fieldData:       string(payload),
		},
	}
	if b.cfg.MaxLen > 0 {
		// Yaklaşık kırpma (~) seçilir: Redis'in radix düğüm sınırında durmasına
		// izin verir, tam kırpmaya göre çok daha ucuzdur.
		args.MaxLen = b.cfg.MaxLen
		args.Approx = true
	}

	if err := b.client.XAdd(ctx, args).Err(); err != nil {
		return errors.Wrap(err, errors.KindUnavailable, CodePublishFailed,
			"%q olayı redis stream'ine yazılamadı", e.Name)
	}

	return nil
}

// Subscribe verilen olay adına bir handler bağlar.
//
// İlk abonelikte stream ve consumer group oluşturulur (varsa yeniden
// kullanılır) ve o olay için tek bir tüketici döngüsü başlatılır. Aynı ada
// bağlanan sonraki handler'lar aynı döngüden beslenir; yani mesaj gruptan bir
// kez okunur, süreç içindeki tüm handler'lara verilir.
//
// Consumer group oluşturmanın ağ turu, Publish'in kullandığı kilidin DIŞINDA
// yapılır: Redis yavaş ya da erişilemez olsa bile eşzamanlı yayımlar
// bloklanmaz, yalnızca bu çağrı bekler.
func (b *redisBus) Subscribe(eventName string, h Handler) error {
	if eventName == "" {
		return errors.Invalid(CodeSubscribeFailed, "abone olunacak olay adı boş olamaz")
	}
	if h == nil {
		return errors.Invalid(CodeSubscribeFailed, "%q için handler nil olamaz", eventName)
	}

	// setupMu eşzamanlı Subscribe'ları serileştirir; mu boş kaldığı için Publish
	// aşağıdaki ağ turu boyunca çalışmaya devam eder.
	b.setupMu.Lock()
	defer b.setupMu.Unlock()

	if b.isClosed() {
		return closedSubscribeError(eventName)
	}

	_, consuming := b.consuming[eventName]
	if !consuming {
		if err := b.ensureGroup(eventName); err != nil {
			return err
		}
	}

	b.mu.Lock()
	if b.closed {
		// Ağ turu sürerken Shutdown çağrılmış; döngüyü hiç başlatma.
		b.mu.Unlock()
		return closedSubscribeError(eventName)
	}
	if !consuming {
		// wg.Add, closed kontrolüyle aynı kilit altında yapılır; böylece
		// Shutdown'ın Wait'i bu döngüyü kaçırmaz.
		b.wg.Add(1)
	}
	b.handlers[eventName] = append(b.handlers[eventName], h)
	b.mu.Unlock()

	if !consuming {
		b.consuming[eventName] = struct{}{}
		go b.consume(eventName)
	}
	return nil
}

// Shutdown tüketici döngülerini durdurur ve işlenmekte olan olayların bitmesini
// bekler.
//
// Dönüşten sonra Publish ve Subscribe hata döner. Bekleme ctx ile sınırlıdır:
// ctx bitmeden döngüler ve handler'lar tamamlanırsa nil döner ve çalışan
// goroutine kalmaz; süre dolarsa takılan handler'lar BEKLENMEZ ve
// errors.KindUnavailable / CodeShutdownTimeout döner. İstemci çağırana ait
// olduğu için kapatılmaz. Birden çok kez çağrılabilir.
func (b *redisBus) Shutdown(ctx context.Context) error {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()

	b.cancel()
	return awaitHandlers(ctx, &b.wg)
}

// isClosed veri yolunun kapatılmış olup olmadığını döner.
func (b *redisBus) isClosed() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.closed
}

// ensureGroup olay için stream'in ve consumer group'un var olmasını sağlar.
// Group zaten varsa (BUSYGROUP) bu bir hata değildir.
func (b *redisBus) ensureGroup(eventName string) error {
	ctx, cancel := context.WithTimeout(b.ctx, controlTimeout)
	defer cancel()

	err := b.client.XGroupCreateMkStream(ctx, b.cfg.StreamName(eventName), b.cfg.Group, groupStart).Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return errors.Wrap(err, errors.KindUnavailable, CodeSubscribeFailed,
			"%q olayı için consumer group oluşturulamadı", eventName)
	}
	return nil
}

// consume tek bir olay adının stream'ini sürekli okur.
//
// Önce bu tüketicinin daha önce alıp ACK'lemediği mesajlar (pending list)
// okunur; süreç yeniden başladığında kaldığı yerden devam etmesini bu sağlar.
// Liste tükendiğinde ">" imine geçilerek yalnızca yeni mesajlar beklenir.
func (b *redisBus) consume(eventName string) {
	defer b.wg.Done()

	stream := b.cfg.StreamName(eventName)
	cursor := cursorPending

	for {
		if b.ctx.Err() != nil {
			return
		}

		res, err := b.client.XReadGroup(b.ctx, &redis.XReadGroupArgs{
			Group:    b.cfg.Group,
			Consumer: b.cfg.Consumer,
			Streams:  []string{stream, cursor},
			Count:    b.cfg.BatchSize,
			Block:    b.cfg.BlockTimeout,
		}).Result()

		switch {
		case err == nil:
			// Mesaj(lar) okundu; aşağıda işlenir.
		case errors.Is(err, redis.Nil):
			// Bloklama süresi doldu, yeni mesaj yok.
			continue
		case b.ctx.Err() != nil:
			// Shutdown çağrıldı; okuma bu yüzden kesildi.
			return
		default:
			b.log.ErrorContext(b.ctx, "olay akışı okunamadı",
				attrStream, stream, attrError, err)
			if !b.sleep(readErrorBackoff) {
				return
			}
			continue
		}

		msgs := messagesOf(res, stream)

		if cursor != cursorNew {
			if len(msgs) == 0 {
				// Bekleyen mesaj kalmadı; bundan sonrası yalnızca yeni mesaj.
				cursor = cursorNew
				continue
			}
			// Bekleyen listede sayfalama: bir sonraki tur son kimlikten devam eder.
			cursor = msgs[len(msgs)-1].ID
		}

		for _, msg := range msgs {
			b.dispatch(stream, eventName, msg)
		}
	}
}

// dispatch tek bir mesajı çözer, handler'lara verir ve ACK'ler.
//
// Handler hata dönse veya paniklese bile mesaj ACK'lenir: yeniden teslim
// politikası bilinçli olarak yoktur (bkz. paket yorumu). Çözülemeyen mesaj da
// loglanıp ACK'lenir; aksi hâlde bekleyen listede sonsuza dek kalırdı.
func (b *redisBus) dispatch(stream, eventName string, msg redis.XMessage) {
	defer b.ack(stream, msg.ID)

	e, err := decodeMessage(eventName, msg)
	if err != nil {
		b.log.ErrorContext(b.ctx, "olay mesajı çözülemedi",
			attrStream, stream, "message_id", msg.ID, attrError, err)
		return
	}

	b.mu.RLock()
	handlers := slices.Clone(b.handlers[eventName])
	b.mu.RUnlock()

	// Handler'lar veri yolunun kökünden türeyen ama iptali devralmayan bir ctx
	// alır; böylece Shutdown çağrıldığında çalışan işleme yarıda kesilmez ve
	// Shutdown onun bitmesini bekler (graceful kapanış).
	//
	// Kök, yayımcının ctx'i DEĞİLDİR ve olamaz: mesaj başka bir süreçten
	// gelmiş olabilir. Yayımcının istek değerleri (örn. request_id) bu yüzden
	// burada YOKTUR — in-memory backend'den ayrıştığımız tek nokta budur ve
	// gerekçesi paket yorumundaki "Bağlam ve gözlemlenebilirlik"tedir.
	hctx := context.WithoutCancel(b.ctx)
	for _, h := range handlers {
		invokeHandler(hctx, b.log, e, h)
	}
}

// ack mesajı consumer group'ta işlenmiş olarak işaretler.
// Veri yolu kapanırken de çalışabilmesi için iptal devralmayan bir ctx kullanır.
func (b *redisBus) ack(stream, messageID string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(b.ctx), controlTimeout)
	defer cancel()

	if err := b.client.XAck(ctx, stream, b.cfg.Group, messageID).Err(); err != nil {
		b.log.ErrorContext(ctx, "olay mesajı ACK'lenemedi",
			attrStream, stream, "message_id", messageID, attrError, err)
	}
}

// sleep verilen süre kadar bekler; bu sırada veri yolu kapatılırsa false döner.
func (b *redisBus) sleep(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-b.ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// messagesOf yanıt içindeki ilgili stream'in mesajlarını döner.
func messagesOf(res []redis.XStream, stream string) []redis.XMessage {
	for _, s := range res {
		if s.Stream == stream {
			return s.Messages
		}
	}
	return nil
}

// decodeMessage stream mesajını Event'e çevirir.
// eventName, mesajda ad alanı yoksa kullanılacak yedektir.
//
// Alan haritası boş olan mesaj GEÇERSİZ sayılır. Redis, bekleyen listede duran
// ama stream'den silinmiş ya da MAXLEN ile kırpılmış girdileri alansız döner
// (tombstone) ve go-redis bunu hatasız bir XMessage'a çevirir. Hata dönmek,
// kimliksiz ve verisiz sahte bir olayın handler'lara ulaşmasını engeller;
// çağıran mesajı loglayıp ACK'leyerek bekleyen listeden temizler.
func decodeMessage(eventName string, msg redis.XMessage) (Event, error) {
	if len(msg.Values) == 0 {
		return Event{}, errors.Invalid(CodeInvalidEvent,
			"mesajın alanları yok; stream'den silinmiş veya kırpılmış olabilir")
	}

	e := Event{
		Name: eventName,
		ID:   stringField(msg.Values, fieldID),
	}

	if name := stringField(msg.Values, fieldName); name != "" {
		e.Name = name
	}

	if raw := stringField(msg.Values, fieldOccurredAt); raw != "" {
		t, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return Event{}, errors.Wrap(err, errors.KindInvalid, CodeInvalidEvent,
				"olay zamanı ayrıştırılamadı: %q", raw)
		}
		e.OccurredAt = t.UTC()
	}

	if raw := stringField(msg.Values, fieldData); raw != "" {
		if err := json.Unmarshal([]byte(raw), &e.Data); err != nil {
			return Event{}, errors.Wrap(err, errors.KindInvalid, CodeInvalidEvent,
				"olay verisi JSON olarak çözülemedi")
		}
	}

	return e, nil
}

// stringField Redis'ten gelen alanı dizeye çevirir; alan yoksa boş dize döner.
func stringField(values map[string]any, key string) string {
	if v, ok := values[key].(string); ok {
		return v
	}
	return ""
}
