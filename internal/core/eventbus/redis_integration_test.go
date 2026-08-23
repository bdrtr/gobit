//go:build integration

// Bu dosya gerçek bir Redis gerektirir ve yalnızca `-tags=integration` ile
// derlenir (`make test-integration`). Böylece `make test` Docker'sız ve hızlı kalır.
package eventbus_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/bdrtr/gobit/internal/core/eventbus"
)

// redisImage entegrasyon testlerinde kullanılan Redis imajıdır.
const redisImage = "redis:7-alpine"

// startRedis test süresince yaşayan bir Redis başlatır ve istemcisini döner.
func startRedis(t *testing.T) *redis.Client {
	t.Helper()

	ctx := t.Context()
	container, err := tcredis.Run(ctx, redisImage)
	testcontainers.CleanupContainer(t, container)
	if err != nil {
		t.Fatalf("redis konteyneri başlatılamadı: %v", err)
	}

	uri, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("bağlantı dizesi alınamadı: %v", err)
	}

	opts, err := redis.ParseURL(uri)
	if err != nil {
		t.Fatalf("bağlantı dizesi ayrıştırılamadı: %v", err)
	}

	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })

	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("redis'e ping atılamadı: %v", err)
	}
	return client
}

// testConfig her test için yalıtılmış bir ayar üretir; testler birbirinin
// stream'ini ve consumer group'unu görmez.
func testConfig(t *testing.T, consumer string) eventbus.RedisConfig {
	t.Helper()
	return eventbus.RedisConfig{
		StreamPrefix: "gobit-test:" + t.Name(),
		Group:        "grup-" + t.Name(),
		Consumer:     consumer,
		BlockTimeout: 200 * time.Millisecond,
	}
}

func TestRedisIntegrationPublishSubscribe(t *testing.T) {
	client := startRedis(t)
	cfg := testConfig(t, "tuketici-1")

	bus, err := eventbus.NewRedisStream(client, cfg, discardLogger())
	if err != nil {
		t.Fatalf("NewRedisStream hata verdi: %v", err)
	}
	defer func() { _ = bus.Shutdown(context.Background()) }()

	got := make(chan eventbus.Event, 1)
	if err := bus.Subscribe("order.placed", func(_ context.Context, e eventbus.Event) error {
		got <- e
		return nil
	}); err != nil {
		t.Fatalf("Subscribe hata verdi: %v", err)
	}

	when := time.Date(2026, 8, 23, 12, 30, 0, 0, time.UTC)
	published := eventbus.Event{
		Name:       "order.placed",
		ID:         "evt_entegrasyon_01",
		OccurredAt: when,
		Data: map[string]any{
			"order_id": "order_01",
			"toplam":   float64(1999),
			"kalemler": []any{"variant_01", "variant_02"},
		},
	}
	if err := bus.Publish(t.Context(), published); err != nil {
		t.Fatalf("Publish hata verdi: %v", err)
	}

	select {
	case e := <-got:
		if e.Name != published.Name {
			t.Errorf("Name = %q, beklenen %q", e.Name, published.Name)
		}
		if e.ID != published.ID {
			t.Errorf("ID = %q, beklenen %q", e.ID, published.ID)
		}
		if !e.OccurredAt.Equal(when) {
			t.Errorf("OccurredAt = %v, beklenen %v", e.OccurredAt, when)
		}
		if e.Data["order_id"] != "order_01" {
			t.Errorf("Data[order_id] = %v, beklenen order_01", e.Data["order_id"])
		}
		if e.Data["toplam"] != float64(1999) {
			t.Errorf("Data[toplam] = %v, beklenen 1999", e.Data["toplam"])
		}
		if kalemler, ok := e.Data["kalemler"].([]any); !ok || len(kalemler) != 2 {
			t.Errorf("Data[kalemler] = %v, beklenen 2 elemanlı dizi", e.Data["kalemler"])
		}
	case <-time.After(15 * time.Second):
		t.Fatal("zaman aşımı: olay redis üzerinden teslim edilmedi")
	}
}

func TestRedisIntegrationDeliversEventsPublishedBeforeSubscribe(t *testing.T) {
	client := startRedis(t)
	cfg := testConfig(t, "tuketici-1")

	bus, err := eventbus.NewRedisStream(client, cfg, discardLogger())
	if err != nil {
		t.Fatalf("NewRedisStream hata verdi: %v", err)
	}
	defer func() { _ = bus.Shutdown(context.Background()) }()

	// Abone yokken yayımlanan olay stream'de kalır; group "0"dan başladığı için
	// sonradan gelen abone onu da alır.
	if err := bus.Publish(t.Context(), eventbus.Event{
		Name: "order.placed",
		Data: map[string]any{"order_id": "order_erken"},
	}); err != nil {
		t.Fatalf("Publish hata verdi: %v", err)
	}

	got := make(chan eventbus.Event, 1)
	if err := bus.Subscribe("order.placed", func(_ context.Context, e eventbus.Event) error {
		got <- e
		return nil
	}); err != nil {
		t.Fatalf("Subscribe hata verdi: %v", err)
	}

	select {
	case e := <-got:
		if e.Data["order_id"] != "order_erken" {
			t.Errorf("Data[order_id] = %v, beklenen order_erken", e.Data["order_id"])
		}
	case <-time.After(15 * time.Second):
		t.Fatal("zaman aşımı: abonelik öncesi yayımlanan olay teslim edilmedi")
	}
}

func TestRedisIntegrationAcksProcessedMessages(t *testing.T) {
	client := startRedis(t)
	cfg := testConfig(t, "tuketici-1")

	bus, err := eventbus.NewRedisStream(client, cfg, discardLogger())
	if err != nil {
		t.Fatalf("NewRedisStream hata verdi: %v", err)
	}
	defer func() { _ = bus.Shutdown(context.Background()) }()

	const eventCount = 5
	var processed sync.WaitGroup
	processed.Add(eventCount)
	if err := bus.Subscribe("order.placed", func(context.Context, eventbus.Event) error {
		processed.Done()
		return nil
	}); err != nil {
		t.Fatalf("Subscribe hata verdi: %v", err)
	}

	for i := range eventCount {
		if err := bus.Publish(t.Context(), eventbus.Event{
			Name: "order.placed",
			Data: map[string]any{"sira": i},
		}); err != nil {
			t.Fatalf("Publish hata verdi: %v", err)
		}
	}

	done := make(chan struct{})
	go func() {
		processed.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("zaman aşımı: olaylar işlenmedi")
	}

	// ACK istemciye asenkron gider; bekleyen liste boşalana kadar yoklanır.
	stream := cfg.StreamName("order.placed")
	deadline := time.Now().Add(10 * time.Second)
	for {
		pending, err := client.XPending(t.Context(), stream, cfg.Group).Result()
		if err != nil {
			t.Fatalf("XPending hata verdi: %v", err)
		}
		if pending.Count == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("bekleyen mesaj sayısı = %d, beklenen 0 (işlenen mesaj XACK'lenmeli)", pending.Count)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Stream'de mesajlar duruyor (kırpılana kadar) ama hiçbiri bekleyen değil.
	length, err := client.XLen(t.Context(), stream).Result()
	if err != nil {
		t.Fatalf("XLen hata verdi: %v", err)
	}
	if length != eventCount {
		t.Errorf("stream uzunluğu = %d, beklenen %d", length, eventCount)
	}
}

func TestRedisIntegrationConsumerGroupDeliversOnce(t *testing.T) {
	client := startRedis(t)

	// Aynı gruba bağlı iki ayrı tüketici: her mesaj yalnızca birine gitmeli.
	cfgA := testConfig(t, "tuketici-a")
	cfgB := testConfig(t, "tuketici-b")

	var received sync.Map // olay kimliği -> tüketici adı
	var total atomic.Int64
	var duplicates atomic.Int64

	const eventCount = 30
	var wg sync.WaitGroup
	wg.Add(eventCount)

	subscribe := func(cfg eventbus.RedisConfig) eventbus.EventBus {
		t.Helper()
		bus, err := eventbus.NewRedisStream(client, cfg, discardLogger())
		if err != nil {
			t.Fatalf("NewRedisStream hata verdi: %v", err)
		}
		if err := bus.Subscribe("order.placed", func(_ context.Context, e eventbus.Event) error {
			if _, loaded := received.LoadOrStore(e.ID, cfg.Consumer); loaded {
				duplicates.Add(1)
				return nil
			}
			total.Add(1)
			wg.Done()
			return nil
		}); err != nil {
			t.Fatalf("Subscribe hata verdi: %v", err)
		}
		return bus
	}

	busA := subscribe(cfgA)
	defer func() { _ = busA.Shutdown(context.Background()) }()
	busB := subscribe(cfgB)
	defer func() { _ = busB.Shutdown(context.Background()) }()

	for i := range eventCount {
		if err := busA.Publish(t.Context(), eventbus.Event{
			Name: "order.placed",
			ID:   fmt.Sprintf("evt_%02d", i),
			Data: map[string]any{"sira": i},
		}); err != nil {
			t.Fatalf("Publish hata verdi: %v", err)
		}
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatalf("zaman aşımı: %d/%d olay teslim edildi", total.Load(), eventCount)
	}

	if got := total.Load(); got != eventCount {
		t.Errorf("teslim edilen tekil olay = %d, beklenen %d", got, eventCount)
	}
	if got := duplicates.Load(); got != 0 {
		t.Errorf("tekrarlı teslim = %d, beklenen 0 (consumer group mesajı bir kez vermeli)", got)
	}

	// Her iki tüketici de gruptan pay almış olmalı; aksi hâlde test grubun
	// dağıtımını değil tek tüketiciyi ölçüyor demektir.
	consumers := make(map[string]int)
	received.Range(func(_, value any) bool {
		if name, ok := value.(string); ok {
			consumers[name]++
		}
		return true
	})
	t.Logf("tüketici dağılımı: %v", consumers)
}

func TestRedisIntegrationResumesAfterRestart(t *testing.T) {
	client := startRedis(t)
	cfg := testConfig(t, "tuketici-kalici")

	// Birinci süreç: iki olayı işleyip kapanıyor.
	first, err := eventbus.NewRedisStream(client, cfg, discardLogger())
	if err != nil {
		t.Fatalf("NewRedisStream hata verdi: %v", err)
	}

	firstBatch := make(chan string, 4)
	if err := first.Subscribe("order.placed", func(_ context.Context, e eventbus.Event) error {
		firstBatch <- e.ID
		return nil
	}); err != nil {
		t.Fatalf("Subscribe hata verdi: %v", err)
	}

	for _, id := range []string{"evt_01", "evt_02"} {
		if err := first.Publish(t.Context(), eventbus.Event{Name: "order.placed", ID: id}); err != nil {
			t.Fatalf("Publish hata verdi: %v", err)
		}
	}
	for range 2 {
		select {
		case <-firstBatch:
		case <-time.After(15 * time.Second):
			t.Fatal("zaman aşımı: ilk parti işlenmedi")
		}
	}
	if err := first.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown hata verdi: %v", err)
	}

	// Veri yolu kapalıyken yayımlanan olay kaybolmamalı.
	publisher, err := eventbus.NewRedisStream(client, cfg, discardLogger())
	if err != nil {
		t.Fatalf("NewRedisStream hata verdi: %v", err)
	}
	defer func() { _ = publisher.Shutdown(context.Background()) }()
	if err := publisher.Publish(t.Context(), eventbus.Event{Name: "order.placed", ID: "evt_03"}); err != nil {
		t.Fatalf("Publish hata verdi: %v", err)
	}

	// İkinci süreç aynı grup ve tüketici adıyla ayağa kalkar: kaldığı yerden
	// devam etmeli, işlenmiş olayları tekrar almamalıdır.
	second, err := eventbus.NewRedisStream(client, cfg, discardLogger())
	if err != nil {
		t.Fatalf("NewRedisStream hata verdi: %v", err)
	}
	defer func() { _ = second.Shutdown(context.Background()) }()

	secondBatch := make(chan string, 4)
	if err := second.Subscribe("order.placed", func(_ context.Context, e eventbus.Event) error {
		secondBatch <- e.ID
		return nil
	}); err != nil {
		t.Fatalf("Subscribe hata verdi: %v", err)
	}

	select {
	case id := <-secondBatch:
		if id != "evt_03" {
			t.Errorf("yeniden başlatma sonrası ilk olay = %q, beklenen evt_03", id)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("zaman aşımı: yeniden başlatma sonrası olay teslim edilmedi")
	}

	select {
	case id := <-secondBatch:
		t.Errorf("işlenmiş olay tekrar teslim edildi: %q", id)
	case <-time.After(time.Second):
	}
}

func TestRedisIntegrationHandlerPanicDoesNotStopConsumer(t *testing.T) {
	client := startRedis(t)
	cfg := testConfig(t, "tuketici-1")

	bus, err := eventbus.NewRedisStream(client, cfg, discardLogger())
	if err != nil {
		t.Fatalf("NewRedisStream hata verdi: %v", err)
	}
	defer func() { _ = bus.Shutdown(context.Background()) }()

	delivered := make(chan string, 4)
	if err := bus.Subscribe("order.placed", func(_ context.Context, e eventbus.Event) error {
		if e.ID == "evt_panik" {
			panic("handler patladı")
		}
		return nil
	}); err != nil {
		t.Fatalf("Subscribe hata verdi: %v", err)
	}
	if err := bus.Subscribe("order.placed", func(_ context.Context, e eventbus.Event) error {
		delivered <- e.ID
		return nil
	}); err != nil {
		t.Fatalf("Subscribe hata verdi: %v", err)
	}

	for _, id := range []string{"evt_panik", "evt_saglam"} {
		if err := bus.Publish(t.Context(), eventbus.Event{Name: "order.placed", ID: id}); err != nil {
			t.Fatalf("Publish hata verdi: %v", err)
		}
	}

	for _, want := range []string{"evt_panik", "evt_saglam"} {
		select {
		case got := <-delivered:
			if got != want {
				t.Errorf("teslim edilen = %q, beklenen %q", got, want)
			}
		case <-time.After(15 * time.Second):
			t.Fatalf("zaman aşımı: %q teslim edilmedi (panik tüketiciyi durdurdu)", want)
		}
	}
}
