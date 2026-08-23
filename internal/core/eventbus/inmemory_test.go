package eventbus_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/eventbus"
)

// waitTimeout testlerin asenkron teslimi beklerken kullandığı üst sınırdır.
const waitTimeout = 2 * time.Second

// discardLogger çıktısı atılan bir logger döner.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// bufferLogger bir tampona yazan logger ve tamponu döner.
func bufferLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

// waitSignal kanaldan sinyal gelmesini bekler; gelmezse testi düşürür.
func waitSignal(t *testing.T, ch <-chan struct{}, mesaj string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(waitTimeout):
		t.Fatalf("zaman aşımı: %s", mesaj)
	}
}

func TestInMemoryPublishSubscribe(t *testing.T) {
	bus := eventbus.NewInMemory(discardLogger())
	defer func() { _ = bus.Shutdown(context.Background()) }()

	got := make(chan eventbus.Event, 1)
	if err := bus.Subscribe("order.placed", func(_ context.Context, e eventbus.Event) error {
		got <- e
		return nil
	}); err != nil {
		t.Fatalf("Subscribe hata verdi: %v", err)
	}

	before := time.Now().UTC()
	if err := bus.Publish(t.Context(), eventbus.Event{
		Name: "order.placed",
		Data: map[string]any{"order_id": "order_01"},
	}); err != nil {
		t.Fatalf("Publish hata verdi: %v", err)
	}

	select {
	case e := <-got:
		if e.Name != "order.placed" {
			t.Errorf("Name = %q, beklenen %q", e.Name, "order.placed")
		}
		if e.Data["order_id"] != "order_01" {
			t.Errorf("Data[order_id] = %v, beklenen order_01", e.Data["order_id"])
		}
		if !strings.HasPrefix(e.ID, "evt_") {
			t.Errorf("ID = %q, beklenen evt_ önekli üretilmiş kimlik", e.ID)
		}
		if e.OccurredAt.Before(before) {
			t.Errorf("OccurredAt = %v, yayım anından (%v) önce olamaz", e.OccurredAt, before)
		}
	case <-time.After(waitTimeout):
		t.Fatal("zaman aşımı: olay handler'a ulaşmadı")
	}
}

func TestInMemoryPublishKeepsGivenIDAndTime(t *testing.T) {
	bus := eventbus.NewInMemory(discardLogger())
	defer func() { _ = bus.Shutdown(context.Background()) }()

	when := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)
	got := make(chan eventbus.Event, 1)
	if err := bus.Subscribe("order.placed", func(_ context.Context, e eventbus.Event) error {
		got <- e
		return nil
	}); err != nil {
		t.Fatalf("Subscribe hata verdi: %v", err)
	}

	if err := bus.Publish(t.Context(), eventbus.Event{
		Name:       "order.placed",
		ID:         "evt_ozel",
		OccurredAt: when,
	}); err != nil {
		t.Fatalf("Publish hata verdi: %v", err)
	}

	e := <-got
	if e.ID != "evt_ozel" {
		t.Errorf("ID = %q, beklenen evt_ozel (yayımlayanın verdiği kimlik korunmalı)", e.ID)
	}
	if !e.OccurredAt.Equal(when) {
		t.Errorf("OccurredAt = %v, beklenen %v", e.OccurredAt, when)
	}
}

func TestInMemoryMultipleSubscribers(t *testing.T) {
	bus := eventbus.NewInMemory(discardLogger())

	const subscriberCount = 5
	var wg sync.WaitGroup
	wg.Add(subscriberCount)

	var calls atomic.Int64
	for range subscriberCount {
		if err := bus.Subscribe("order.placed", func(_ context.Context, _ eventbus.Event) error {
			calls.Add(1)
			wg.Done()
			return nil
		}); err != nil {
			t.Fatalf("Subscribe hata verdi: %v", err)
		}
	}

	if err := bus.Publish(t.Context(), eventbus.Event{Name: "order.placed"}); err != nil {
		t.Fatalf("Publish hata verdi: %v", err)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	waitSignal(t, done, "tüm aboneler çağrılmadı")

	if err := bus.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown hata verdi: %v", err)
	}
	if got := calls.Load(); got != subscriberCount {
		t.Errorf("çağrı sayısı = %d, beklenen %d", got, subscriberCount)
	}
}

func TestInMemoryOtherEventsAreNotDelivered(t *testing.T) {
	bus := eventbus.NewInMemory(discardLogger())

	var calls atomic.Int64
	if err := bus.Subscribe("order.placed", func(_ context.Context, _ eventbus.Event) error {
		calls.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("Subscribe hata verdi: %v", err)
	}

	// Abonesi olmayan olay sessizce yutulur, hata dönmez.
	if err := bus.Publish(t.Context(), eventbus.Event{Name: "cart.updated"}); err != nil {
		t.Fatalf("abonesiz olay için Publish hata verdi: %v", err)
	}

	if err := bus.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown hata verdi: %v", err)
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("çağrı sayısı = %d, beklenen 0 (farklı olay teslim edilmemeli)", got)
	}
}

func TestInMemoryPublishRejectsEmptyName(t *testing.T) {
	bus := eventbus.NewInMemory(discardLogger())
	defer func() { _ = bus.Shutdown(context.Background()) }()

	err := bus.Publish(t.Context(), eventbus.Event{})
	if err == nil {
		t.Fatal("boş adlı olay için Publish hata dönmedi")
	}
	if !errors.IsInvalid(err) {
		t.Errorf("Kind = %v, beklenen invalid", errors.KindOf(err))
	}
	if got := errors.CodeOf(err); got != eventbus.CodeInvalidEvent {
		t.Errorf("Code = %q, beklenen %q", got, eventbus.CodeInvalidEvent)
	}
}

func TestInMemorySubscribeValidates(t *testing.T) {
	bus := eventbus.NewInMemory(discardLogger())
	defer func() { _ = bus.Shutdown(context.Background()) }()

	noop := func(context.Context, eventbus.Event) error { return nil }

	if err := bus.Subscribe("", noop); !errors.IsInvalid(err) {
		t.Errorf("boş olay adı için hata = %v, beklenen invalid", err)
	}
	if err := bus.Subscribe("order.placed", nil); !errors.IsInvalid(err) {
		t.Errorf("nil handler için hata = %v, beklenen invalid", err)
	}
}

func TestInMemoryHandlerPanicDoesNotBreakBus(t *testing.T) {
	log, buf := bufferLogger()
	bus := eventbus.NewInMemory(log)

	survivor := make(chan struct{}, 2)
	if err := bus.Subscribe("order.placed", func(context.Context, eventbus.Event) error {
		panic("handler patladı")
	}); err != nil {
		t.Fatalf("Subscribe hata verdi: %v", err)
	}
	if err := bus.Subscribe("order.placed", func(context.Context, eventbus.Event) error {
		survivor <- struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("Subscribe hata verdi: %v", err)
	}

	// Aynı olayın diğer abonesi paniğe rağmen çağrılmalı...
	if err := bus.Publish(t.Context(), eventbus.Event{Name: "order.placed"}); err != nil {
		t.Fatalf("Publish hata verdi: %v", err)
	}
	waitSignal(t, survivor, "panikleyen handler'ın yanındaki abone çağrılmadı")

	// ...ve veri yolu sonraki yayımlarda da çalışmaya devam etmeli.
	if err := bus.Publish(t.Context(), eventbus.Event{Name: "order.placed"}); err != nil {
		t.Fatalf("panik sonrası Publish hata verdi: %v", err)
	}
	waitSignal(t, survivor, "panik sonrası ikinci yayım teslim edilmedi")

	if err := bus.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown hata verdi: %v", err)
	}
	if out := buf.String(); !strings.Contains(out, "panikledi") {
		t.Errorf("panik loglanmadı; log çıktısı: %s", out)
	}
}

func TestInMemoryHandlerErrorIsLoggedAndIsolated(t *testing.T) {
	log, buf := bufferLogger()
	bus := eventbus.NewInMemory(log)

	healthy := make(chan struct{}, 2)
	if err := bus.Subscribe("order.placed", func(context.Context, eventbus.Event) error {
		return errors.Internal("stok_yok", "stok rezerve edilemedi")
	}); err != nil {
		t.Fatalf("Subscribe hata verdi: %v", err)
	}
	if err := bus.Subscribe("order.placed", func(context.Context, eventbus.Event) error {
		healthy <- struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("Subscribe hata verdi: %v", err)
	}

	// Handler hatası çağırana dönmez; yayım başarılıdır.
	if err := bus.Publish(t.Context(), eventbus.Event{Name: "order.placed"}); err != nil {
		t.Fatalf("Publish hata verdi: %v", err)
	}
	waitSignal(t, healthy, "hata dönen handler'ın yanındaki abone çağrılmadı")

	if err := bus.Publish(t.Context(), eventbus.Event{Name: "order.placed"}); err != nil {
		t.Fatalf("hata sonrası Publish hata verdi: %v", err)
	}
	waitSignal(t, healthy, "hata sonrası ikinci yayım teslim edilmedi")

	if err := bus.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown hata verdi: %v", err)
	}
	if out := buf.String(); !strings.Contains(out, "stok_yok") {
		t.Errorf("handler hatası loglanmadı; log çıktısı: %s", out)
	}
}

func TestInMemoryPublishDoesNotWaitForHandlers(t *testing.T) {
	bus := eventbus.NewInMemory(discardLogger())

	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	if err := bus.Subscribe("order.placed", func(context.Context, eventbus.Event) error {
		entered <- struct{}{}
		<-release
		return nil
	}); err != nil {
		t.Fatalf("Subscribe hata verdi: %v", err)
	}

	start := time.Now()
	if err := bus.Publish(t.Context(), eventbus.Event{Name: "order.placed"}); err != nil {
		t.Fatalf("Publish hata verdi: %v", err)
	}
	elapsed := time.Since(start)

	waitSignal(t, entered, "handler hiç çağrılmadı")
	if elapsed > 500*time.Millisecond {
		t.Errorf("Publish %v sürdü; handler'ı beklememeliydi", elapsed)
	}

	close(release)
	if err := bus.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown hata verdi: %v", err)
	}
}

func TestInMemoryHandlerContextSurvivesCallerCancel(t *testing.T) {
	bus := eventbus.NewInMemory(discardLogger())
	defer func() { _ = bus.Shutdown(context.Background()) }()

	type ctxKey struct{}

	got := make(chan context.Context, 1)
	if err := bus.Subscribe("order.placed", func(ctx context.Context, _ eventbus.Event) error {
		got <- ctx
		return nil
	}); err != nil {
		t.Fatalf("Subscribe hata verdi: %v", err)
	}

	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), ctxKey{}, "req_01"))
	if err := bus.Publish(ctx, eventbus.Event{Name: "order.placed"}); err != nil {
		t.Fatalf("Publish hata verdi: %v", err)
	}
	// Çağıranın isteği hemen sonlanıyor; yan etki bundan etkilenmemeli.
	cancel()

	hctx := <-got
	if err := hctx.Err(); err != nil {
		t.Errorf("handler ctx'i iptal edilmiş: %v", err)
	}
	if v := hctx.Value(ctxKey{}); v != "req_01" {
		t.Errorf("ctx değeri = %v, beklenen req_01 (istek bağlamı korunmalı)", v)
	}
}

func TestInMemoryHandlersGetIndependentData(t *testing.T) {
	bus := eventbus.NewInMemory(discardLogger())

	var wg sync.WaitGroup
	wg.Add(2)

	// İki handler aynı anda Data'nın üst düzey anahtarlarına yazar; kopyalama
	// olmasaydı bu -race altında yarış olarak raporlanırdı.
	seen := make(chan any, 2)
	for range 2 {
		if err := bus.Subscribe("order.placed", func(_ context.Context, e eventbus.Event) error {
			defer wg.Done()
			e.Data["yerel"] = "deger"
			seen <- e.Data["order_id"]
			return nil
		}); err != nil {
			t.Fatalf("Subscribe hata verdi: %v", err)
		}
	}

	data := map[string]any{"order_id": "order_01"}
	if err := bus.Publish(t.Context(), eventbus.Event{Name: "order.placed", Data: data}); err != nil {
		t.Fatalf("Publish hata verdi: %v", err)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	waitSignal(t, done, "handler'lar tamamlanmadı")

	if err := bus.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown hata verdi: %v", err)
	}
	for range 2 {
		if v := <-seen; v != "order_01" {
			t.Errorf("handler'ın gördüğü order_id = %v, beklenen order_01", v)
		}
	}
	if _, ok := data["yerel"]; ok {
		t.Error("handler'ın yazdığı anahtar yayımlayanın haritasına sızdı")
	}
}

func TestInMemoryShutdownWaitsForRunningHandlers(t *testing.T) {
	bus := eventbus.NewInMemory(discardLogger())

	entered := make(chan struct{})
	var finished atomic.Bool
	if err := bus.Subscribe("order.placed", func(context.Context, eventbus.Event) error {
		close(entered)
		time.Sleep(100 * time.Millisecond)
		finished.Store(true)
		return nil
	}); err != nil {
		t.Fatalf("Subscribe hata verdi: %v", err)
	}

	if err := bus.Publish(t.Context(), eventbus.Event{Name: "order.placed"}); err != nil {
		t.Fatalf("Publish hata verdi: %v", err)
	}
	waitSignal(t, entered, "handler hiç başlamadı")

	if err := bus.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown hata verdi: %v", err)
	}
	if !finished.Load() {
		t.Error("Shutdown çalışan handler'ı beklemeden döndü")
	}
}

func TestInMemoryShutdownReturnsWhenContextExpires(t *testing.T) {
	bus := eventbus.NewInMemory(discardLogger())

	entered := make(chan struct{})
	release := make(chan struct{})
	// Handler bilerek takılı bırakılır: zaman aşımı ayarlanmamış bir HTTP
	// çağrısında asılı kalan gerçek bir handler'ı temsil eder.
	defer close(release)

	if err := bus.Subscribe("order.placed", func(context.Context, eventbus.Event) error {
		close(entered)
		<-release
		return nil
	}); err != nil {
		t.Fatalf("Subscribe hata verdi: %v", err)
	}

	if err := bus.Publish(t.Context(), eventbus.Event{Name: "order.placed"}); err != nil {
		t.Fatalf("Publish hata verdi: %v", err)
	}
	waitSignal(t, entered, "handler hiç başlamadı")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := bus.Shutdown(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("takılı handler'a rağmen Shutdown nil döndü; kapanış sonsuza kilitlenirdi")
	}
	if !errors.HasKind(err, errors.KindUnavailable) {
		t.Errorf("Kind = %v, beklenen unavailable", errors.KindOf(err))
	}
	if got := errors.CodeOf(err); got != eventbus.CodeShutdownTimeout {
		t.Errorf("Code = %q, beklenen %q", got, eventbus.CodeShutdownTimeout)
	}
	if elapsed > waitTimeout {
		t.Errorf("Shutdown %v sürdü; ctx süresiyle sınırlı olmalıydı", elapsed)
	}

	// Zaman aşımına uğrasa da veri yolu kapalıdır; yeni yayım kabul edilmez.
	if err := bus.Publish(t.Context(), eventbus.Event{Name: "order.placed"}); !errors.HasKind(err, errors.KindUnavailable) {
		t.Errorf("zaman aşımından sonra Publish hatası = %v, beklenen unavailable", err)
	}
}

func TestInMemoryClosedBusRejectsUse(t *testing.T) {
	bus := eventbus.NewInMemory(discardLogger())
	if err := bus.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown hata verdi: %v", err)
	}

	err := bus.Publish(t.Context(), eventbus.Event{Name: "order.placed"})
	if err == nil {
		t.Fatal("kapalı veri yolunda Publish hata dönmedi")
	}
	if !errors.HasKind(err, errors.KindUnavailable) {
		t.Errorf("Publish Kind = %v, beklenen unavailable", errors.KindOf(err))
	}
	if got := errors.CodeOf(err); got != eventbus.CodeClosed {
		t.Errorf("Publish Code = %q, beklenen %q", got, eventbus.CodeClosed)
	}

	err = bus.Subscribe("order.placed", func(context.Context, eventbus.Event) error { return nil })
	if err == nil {
		t.Fatal("kapalı veri yolunda Subscribe hata dönmedi")
	}
	if got := errors.CodeOf(err); got != eventbus.CodeClosed {
		t.Errorf("Subscribe Code = %q, beklenen %q", got, eventbus.CodeClosed)
	}

	// Shutdown yeniden çağrılabilir olmalı.
	if err := bus.Shutdown(context.Background()); err != nil {
		t.Errorf("ikinci Shutdown hata verdi: %v", err)
	}
}

func TestInMemoryShutdownLeavesNoGoroutines(t *testing.T) {
	bus := eventbus.NewInMemory(discardLogger())

	for range 4 {
		if err := bus.Subscribe("order.placed", func(context.Context, eventbus.Event) error {
			return nil
		}); err != nil {
			t.Fatalf("Subscribe hata verdi: %v", err)
		}
	}

	// Ölçüm noktası: aboneler kurulduktan sonra, yayımdan hemen önce.
	base := runtime.NumGoroutine()

	for range 200 {
		if err := bus.Publish(t.Context(), eventbus.Event{Name: "order.placed"}); err != nil {
			t.Fatalf("Publish hata verdi: %v", err)
		}
	}

	if err := bus.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown hata verdi: %v", err)
	}

	// Shutdown handler'ların bitmesini bekler; goroutine'lerin tamamen sönmesi
	// zamanlayıcıya bağlı olduğu için kısa bir toleransla yoklanır.
	deadline := time.Now().Add(waitTimeout)
	for {
		got := runtime.NumGoroutine()
		if got <= base {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutine sızıntısı: Shutdown sonrası %d goroutine var, beklenen <= %d", got, base)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestInMemoryConcurrentPublishAndShutdown(t *testing.T) {
	bus := eventbus.NewInMemory(discardLogger())

	var delivered atomic.Int64
	if err := bus.Subscribe("order.placed", func(context.Context, eventbus.Event) error {
		delivered.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("Subscribe hata verdi: %v", err)
	}

	// Publish ve Close eşzamanlı koşar: -race altında yarış olmamalı, kapanış
	// sonrası yayımlar hata dönmeli, teslim edilenler kaybolmamalıdır.
	var wg sync.WaitGroup
	var rejected atomic.Int64
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 25 {
				if err := bus.Publish(t.Context(), eventbus.Event{Name: "order.placed"}); err != nil {
					rejected.Add(1)
				}
			}
		}()
	}

	time.Sleep(5 * time.Millisecond)
	if err := bus.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown hata verdi: %v", err)
	}
	wg.Wait()

	accepted := int64(16*25) - rejected.Load()
	if delivered.Load() != accepted {
		t.Errorf("teslim = %d, kabul edilen yayım = %d; kapanış teslimleri düşürdü",
			delivered.Load(), accepted)
	}
}
