package eventbus_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/eventbus"
)

// unreachableClient hiçbir zaman bağlanmayacak bir istemci döner.
//
// go-redis bağlantıyı ilk komutta kurduğu için, ağa çıkmadan dönen kod
// yollarını (doğrulama, serileştirme, kapalı veri yolu) Docker olmadan test
// etmeyi sağlar. Uçtan uca davranış redis_integration_test.go'dadır.
func unreachableClient() *redis.Client {
	return redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
}

func TestNewRedisStreamRejectsNilClient(t *testing.T) {
	bus, err := eventbus.NewRedisStream(nil, eventbus.RedisConfig{}, discardLogger())
	if err == nil {
		t.Fatal("nil istemci için hata dönmedi")
	}
	if bus != nil {
		t.Error("hata durumunda nil olmayan veri yolu döndü")
	}
	if !errors.IsInvalid(err) {
		t.Errorf("Kind = %v, beklenen invalid", errors.KindOf(err))
	}
	if got := errors.CodeOf(err); got != eventbus.CodeInvalidConfig {
		t.Errorf("Code = %q, beklenen %q", got, eventbus.CodeInvalidConfig)
	}
}

func TestRedisConfigStreamName(t *testing.T) {
	tests := []struct {
		name  string
		cfg   eventbus.RedisConfig
		event string
		want  string
	}{
		{
			name:  "varsayilan onek",
			cfg:   eventbus.RedisConfig{},
			event: "order.placed",
			want:  eventbus.DefaultStreamPrefix + ":order.placed",
		},
		{
			name:  "ozel onek",
			cfg:   eventbus.RedisConfig{StreamPrefix: "test:events"},
			event: "cart.updated",
			want:  "test:events:cart.updated",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.StreamName(tt.event); got != tt.want {
				t.Errorf("StreamName(%q) = %q, beklenen %q", tt.event, got, tt.want)
			}
		})
	}
}

// TestWithNamespaceStreamVeGrubuBirlikteAyirir ad alanı önekinin İKİ alanı
// birden ayırdığını doğrular.
//
// Yalnızca stream öneki ayrılsaydı iki kurulum aynı consumer group'a bağlanır
// ve bir olayı ikisinden yalnızca BİRİ alırdı — üretimin "order.placed" olayı
// staging tarafından tüketilip yutulabilirdi. Bu test, o yarım ayrımın sessizce
// geri gelmesini engeller.
func TestWithNamespaceStreamVeGrubuBirlikteAyirir(t *testing.T) {
	cfg := eventbus.RedisConfig{}.WithNamespace("gobit-staging")

	if got, want := cfg.StreamName("order.placed"), "gobit-staging:events:order.placed"; got != want {
		t.Errorf("StreamName() = %q, beklenen %q", got, want)
	}
	if got, want := cfg.Group, "gobit-staging"; got != want {
		t.Errorf("Group = %q, beklenen %q", got, want)
	}

	uretim := eventbus.RedisConfig{}.WithNamespace("gobit-prod")
	if uretim.Group == cfg.Group {
		t.Errorf("iki ad alanı aynı consumer group'a düştü (%q); olaylar ayrılmıyor", cfg.Group)
	}
}

// TestWithNamespaceTuketiciAdinaDokunmaz ad alanının SÜREÇ kimliğini
// ezmediğini doğrular.
//
// İkisi ters yönde çalışır: ad alanı KURULUMLARI ayırır, tüketici adı ise aynı
// gruptaki süreçleri. Ad alanı tüketiciye de yazılsaydı, aynı kurulumun tüm
// örnekleri aynı tüketici adını alır ve her açılışta birbirlerinin işlemekte
// olduğu mesajları okurdu — yani aynı olay iki kez işlenirdi.
func TestWithNamespaceTuketiciAdinaDokunmaz(t *testing.T) {
	cfg := eventbus.RedisConfig{Consumer: "gobit-0"}.WithNamespace("gobit-prod")

	if got, want := cfg.Consumer, "gobit-0"; got != want {
		t.Errorf("Consumer = %q, beklenen %q", got, want)
	}
}

// TestWithNamespaceBosAdAlaniniYokSayar başsız bir anahtar üretilmediğini
// doğrular.
//
// Boş bir ad alanı ":events:order.placed" gibi bir anahtar verirdi: ayıracak
// bir AD olmadan yapılmış bir ayrım, hiçbir şeyi ayırmaz ama varsayılanı da
// bozardı.
func TestWithNamespaceBosAdAlaniniYokSayar(t *testing.T) {
	cfg := eventbus.RedisConfig{}.WithNamespace("")

	if got, want := cfg.StreamName("order.placed"), eventbus.DefaultStreamPrefix+":order.placed"; got != want {
		t.Errorf("StreamName() = %q, beklenen %q", got, want)
	}
	if cfg.Group != "" {
		t.Errorf("Group = %q, beklenen boş (varsayılana düşmeli)", cfg.Group)
	}
}

// TestVarsayilanAdAlaniAyrimlaUyusuyor varsayılanların, ad alanı türetmesinin
// bir ÖZEL DURUMU olduğunu sabitler.
//
// Bugüne kadarki kurulumların anahtarları "gobit:events:*" ve grubu "gobit"tir;
// varsayılan önekle (config.DefaultRedisKeyPrefix) türetilen ad alanı bundan
// ayrışırsa, yükseltilen bir kurulum yeni ve BOŞ bir stream ile yeni bir gruba
// geçer — eski stream'de bekleyen olaylar orada kalır ve kimseye teslim
// edilmez.
func TestVarsayilanAdAlaniAyrimlaUyusuyor(t *testing.T) {
	cfg := eventbus.RedisConfig{}.WithNamespace(eventbus.DefaultGroup)

	if got, want := cfg.StreamPrefix, eventbus.DefaultStreamPrefix; got != want {
		t.Errorf("StreamPrefix = %q, beklenen %q", got, want)
	}
	if got, want := cfg.Group, eventbus.DefaultGroup; got != want {
		t.Errorf("Group = %q, beklenen %q", got, want)
	}

	// Anahtarın TARİHSEL hâli ayrıca sabitlenir: yukarıdaki iddia sabitlerin
	// birbiriyle tutarlı olduğunu söyler, bu satır ise değerin ne olduğunu.
	// İkisi olmadan sabitler birlikte kayabilir ve yükseltilen bir kurulum
	// yeni, BOŞ bir stream'e geçerdi — eski stream'de bekleyen olaylar orada
	// kalır ve kimseye teslim edilmezdi. Değer, config.DefaultRedisKeyPrefix
	// ile eşleşir (çekirdek config'i import EDEMEZ, bkz. Prensip 2.4).
	const tarihsel = "gobit:events:order.placed"
	if got := cfg.StreamName("order.placed"); got != tarihsel {
		t.Errorf("StreamName() = %q, beklenen %q (yükseltilen kurulum eski stream'ini kaybeder)",
			got, tarihsel)
	}
}

// TestConsumerNameBosAdiSurecBasinaTamamlar otomatik tüketici adının GERÇEKTEN
// üretildiğini doğrular.
//
// Verilen ad korunur; boş ad süreç başına türetilir. İkincisi sessizce boş
// kalsaydı Redis, adı boş olan tek bir tüketici görürdü ve tüm örnekler aynı
// bekleyen listeyi paylaşırdı.
func TestConsumerNameBosAdiSurecBasinaTamamlar(t *testing.T) {
	if got, want := eventbus.ConsumerName("gobit-0"), "gobit-0"; got != want {
		t.Errorf("ConsumerName(%q) = %q, beklenen %q", want, got, want)
	}
	if got := eventbus.ConsumerName(""); got == "" {
		t.Error("ConsumerName(\"\") boş döndü; süreç başına bir ad üretmeliydi")
	}
}

func TestRedisPublishRejectsUnserializableData(t *testing.T) {
	bus, err := eventbus.NewRedisStream(unreachableClient(), eventbus.RedisConfig{}, discardLogger())
	if err != nil {
		t.Fatalf("NewRedisStream hata verdi: %v", err)
	}
	defer func() { _ = bus.Shutdown(context.Background()) }()

	tests := []struct {
		name string
		data map[string]any
	}{
		{name: "fonksiyon degeri", data: map[string]any{"cb": func() {}}},
		{name: "kanal degeri", data: map[string]any{"ch": make(chan int)}},
		{name: "NaN", data: map[string]any{"tutar": math.NaN()}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Serileştirme hatası ağa çıkılmadan, tipli hata olarak dönmeli.
			err := bus.Publish(t.Context(), eventbus.Event{Name: "order.placed", Data: tt.data})
			if err == nil {
				t.Fatal("serileştirilemeyen veri için Publish hata dönmedi")
			}
			if !errors.IsInvalid(err) {
				t.Errorf("Kind = %v, beklenen invalid", errors.KindOf(err))
			}
			if got := errors.CodeOf(err); got != eventbus.CodeInvalidEvent {
				t.Errorf("Code = %q, beklenen %q", got, eventbus.CodeInvalidEvent)
			}
		})
	}
}

func TestRedisPublishRejectsEmptyName(t *testing.T) {
	bus, err := eventbus.NewRedisStream(unreachableClient(), eventbus.RedisConfig{}, discardLogger())
	if err != nil {
		t.Fatalf("NewRedisStream hata verdi: %v", err)
	}
	defer func() { _ = bus.Shutdown(context.Background()) }()

	if err := bus.Publish(t.Context(), eventbus.Event{}); !errors.IsInvalid(err) {
		t.Errorf("boş adlı olay için hata = %v, beklenen invalid", err)
	}
}

func TestRedisSubscribeValidates(t *testing.T) {
	bus, err := eventbus.NewRedisStream(unreachableClient(), eventbus.RedisConfig{}, discardLogger())
	if err != nil {
		t.Fatalf("NewRedisStream hata verdi: %v", err)
	}
	defer func() { _ = bus.Shutdown(context.Background()) }()

	if err := bus.Subscribe("", nil); !errors.IsInvalid(err) {
		t.Errorf("boş olay adı için hata = %v, beklenen invalid", err)
	}
	if err := bus.Subscribe("order.placed", nil); !errors.IsInvalid(err) {
		t.Errorf("nil handler için hata = %v, beklenen invalid", err)
	}
}

func TestRedisSubscribeFailsWhenRedisUnreachable(t *testing.T) {
	client := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:1",
		DialTimeout: 200 * time.Millisecond,
		MaxRetries:  -1,
	})
	bus, err := eventbus.NewRedisStream(client, eventbus.RedisConfig{}, discardLogger())
	if err != nil {
		t.Fatalf("NewRedisStream hata verdi: %v", err)
	}
	defer func() { _ = bus.Shutdown(context.Background()) }()

	// Consumer group kurulamadığında abonelik sessizce başarılı sayılmamalı.
	err = bus.Subscribe("order.placed", func(_ context.Context, _ eventbus.Event) error { return nil })
	if err == nil {
		t.Fatal("erişilemeyen redis'te Subscribe hata dönmedi")
	}
	if !errors.HasKind(err, errors.KindUnavailable) {
		t.Errorf("Kind = %v, beklenen unavailable", errors.KindOf(err))
	}
	if got := errors.CodeOf(err); got != eventbus.CodeSubscribeFailed {
		t.Errorf("Code = %q, beklenen %q", got, eventbus.CodeSubscribeFailed)
	}
}

func TestRedisClosedBusRejectsUse(t *testing.T) {
	bus, err := eventbus.NewRedisStream(unreachableClient(), eventbus.RedisConfig{}, discardLogger())
	if err != nil {
		t.Fatalf("NewRedisStream hata verdi: %v", err)
	}
	if err := bus.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown hata verdi: %v", err)
	}

	// Kapalı veri yolunda Publish, ağa hiç çıkmadan hata dönmeli.
	err = bus.Publish(t.Context(), eventbus.Event{Name: "order.placed"})
	if got := errors.CodeOf(err); got != eventbus.CodeClosed {
		t.Errorf("Publish Code = %q, beklenen %q (hata: %v)", got, eventbus.CodeClosed, err)
	}
	if !errors.HasKind(err, errors.KindUnavailable) {
		t.Errorf("Publish Kind = %v, beklenen unavailable", errors.KindOf(err))
	}

	err = bus.Subscribe("order.placed", func(_ context.Context, _ eventbus.Event) error { return nil })
	if got := errors.CodeOf(err); got != eventbus.CodeClosed {
		t.Errorf("Subscribe Code = %q, beklenen %q (hata: %v)", got, eventbus.CodeClosed, err)
	}

	if err := bus.Shutdown(context.Background()); err != nil {
		t.Errorf("ikinci Shutdown hata verdi: %v", err)
	}
}
