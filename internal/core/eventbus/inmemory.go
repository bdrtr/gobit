package eventbus

import (
	"context"
	"log/slog"
	"sync"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// inMemoryBus süreç içinde çalışan, kalıcılığı olmayan bir EventBus'tır.
type inMemoryBus struct {
	log *slog.Logger

	// mu handlers ve closed alanlarını korur. RWMutex seçilmiştir çünkü
	// Publish (okuma) sıcak yol, Subscribe (yazma) yalnızca başlangıçtadır.
	mu       sync.RWMutex
	closed   bool
	handlers map[string][]Handler

	// wg çalışan handler goroutine'lerini sayar; Shutdown bunun bitmesini bekler.
	wg sync.WaitGroup
}

var _ EventBus = (*inMemoryBus)(nil)

// NewInMemory süreç içinde çalışan bir EventBus üretir.
//
// Geliştirme, test ve tek süreçli kurulumlar içindir: olaylar kalıcı değildir,
// süreç ölürse teslim edilmemiş olaylar kaybolur. Üretim için NewRedisStream
// kullanılmalıdır. log nil verilirse slog.Default kullanılır.
func NewInMemory(log *slog.Logger) EventBus {
	return &inMemoryBus{
		log:      orDefaultLogger(log),
		handlers: make(map[string][]Handler),
	}
}

// Publish olayı, adına abone olmuş tüm handler'lara asenkron olarak dağıtır.
//
// Handler'lar beklenmez; her biri KENDİ goroutine'inde çalışır ve dönüş
// yalnızca olayın kabul edildiğini bildirir. Bu yüzden aynı handler aynı anda
// birden çok olayla koşabilir ve teslim sırası yayım sırasıyla aynı olmayabilir
// (bkz. paket yorumundaki sıra ve eşzamanlılık garantileri). Abone yoksa olay
// sessizce yutulur. Veri yolu kapatılmışsa errors.KindUnavailable döner.
func (b *inMemoryBus) Publish(ctx context.Context, e Event) error {
	e, err := normalize(e)
	if err != nil {
		return err
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.closed {
		return closedPublishError(e.Name)
	}

	handlers := b.handlers[e.Name]
	if len(handlers) == 0 {
		return nil
	}

	// Handler'lar çağıranın isteğinden bağımsız yaşar: ctx'in değerleri
	// (örn. request_id) korunur, iptali devralınmaz. Aksi hâlde istek biter
	// bitmez yan etki yarıda kesilirdi.
	hctx := context.WithoutCancel(ctx)

	// wg.Add RLock altında yapılır; Shutdown closed'ı yazma kilidiyle işaretlediği
	// için bu noktadan sonra sayaç artmaz ve Wait ile yarışmaz.
	b.wg.Add(len(handlers))
	for _, h := range handlers {
		go func() {
			defer b.wg.Done()
			invokeHandler(hctx, b.log, e, h)
		}()
	}

	return nil
}

// Subscribe verilen olay adına bir handler bağlar.
//
// Aynı ada birden çok kez abone olunabilir; yayımda hepsi çağrılır. Veri yolu
// kapatılmışsa errors.KindUnavailable döner.
func (b *inMemoryBus) Subscribe(eventName string, h Handler) error {
	if eventName == "" {
		return errors.Invalid(CodeSubscribeFailed, "abone olunacak olay adı boş olamaz")
	}
	if h == nil {
		return errors.Invalid(CodeSubscribeFailed, "%q için handler nil olamaz", eventName)
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return closedSubscribeError(eventName)
	}

	b.handlers[eventName] = append(b.handlers[eventName], h)
	return nil
}

// Shutdown veri yolunu kapatır ve çalışan handler'ların bitmesini bekler.
//
// Dönüşten sonra Publish ve Subscribe hata döner. Bekleme ctx ile sınırlıdır:
// ctx bitmeden tüm handler'lar tamamlanırsa nil döner ve çalışan goroutine
// kalmaz; süre dolarsa takılan handler'lar BEKLENMEZ ve errors.KindUnavailable
// / CodeShutdownTimeout döner (veri yolu yine kapalıdır). Birden çok kez
// çağrılabilir. Bir handler içinden çağrılırsa yalnızca ctx süresi dolduğunda
// döner — Shutdown her zaman handler'ların dışından çağrılmalıdır.
func (b *inMemoryBus) Shutdown(ctx context.Context) error {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()

	return awaitHandlers(ctx, &b.wg)
}
