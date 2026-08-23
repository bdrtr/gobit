package eventbus

import (
	"bytes"
	"context"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// testEventName paket içi testlerde kullanılan olay adıdır.
const testEventName = "order.placed"

// fakeRead betiklenmiş tek bir XREADGROUP turudur; err verilirse tur hata döner.
type fakeRead struct {
	streams []redis.XStream
	err     error
}

// fakeStreamClient streamClient'ı süreç içinde taklit eder.
//
// XReadGroup betiklenmiş turları sırayla döner; betik tükendiğinde çağrı ctx
// iptal edilene kadar bloklanır, böylece tüketici döngüsü boşa dönmez. İstenen
// imler, ACK'lenen kimlikler ve yazılan mesajlar kaydedilir.
type fakeStreamClient struct {
	// onGroupCreate nil değilse XGroupCreateMkStream yerine çağrılır; testler
	// grup kurulumunu yavaşlatmak veya hata döndürmek için kullanır.
	onGroupCreate func(ctx context.Context) error

	mu      sync.Mutex
	reads   []fakeRead
	cursors []string
	acked   []string
	added   []*redis.XAddArgs

	// drained betiklenmiş son tur da servis edildiğinde kapanır.
	drained     chan struct{}
	drainedOnce sync.Once
}

var _ streamClient = (*fakeStreamClient)(nil)

// newFakeStreamClient verilen turları sırayla dönen bir taklit istemci üretir.
func newFakeStreamClient(reads ...fakeRead) *fakeStreamClient {
	f := &fakeStreamClient{reads: reads, drained: make(chan struct{})}
	if len(reads) == 0 {
		f.drainedOnce.Do(func() { close(f.drained) })
	}
	return f
}

func (f *fakeStreamClient) XAdd(ctx context.Context, a *redis.XAddArgs) *redis.StringCmd {
	cmd := redis.NewStringCmd(ctx)

	f.mu.Lock()
	f.added = append(f.added, a)
	f.mu.Unlock()

	cmd.SetVal("1-0")
	return cmd
}

func (f *fakeStreamClient) XGroupCreateMkStream(ctx context.Context, _, _, _ string) *redis.StatusCmd {
	cmd := redis.NewStatusCmd(ctx)
	if f.onGroupCreate != nil {
		if err := f.onGroupCreate(ctx); err != nil {
			cmd.SetErr(err)
			return cmd
		}
	}
	cmd.SetVal("OK")
	return cmd
}

func (f *fakeStreamClient) XReadGroup(ctx context.Context, a *redis.XReadGroupArgs) *redis.XStreamSliceCmd {
	cmd := redis.NewXStreamSliceCmd(ctx)

	f.mu.Lock()
	if n := len(a.Streams); n > 0 {
		f.cursors = append(f.cursors, a.Streams[n-1])
	}
	scripted := len(f.reads) > 0
	var next fakeRead
	if scripted {
		next, f.reads = f.reads[0], f.reads[1:]
		if len(f.reads) == 0 {
			f.drainedOnce.Do(func() { close(f.drained) })
		}
	}
	f.mu.Unlock()

	switch {
	case !scripted:
		// Betik bitti: veri yolu kapanana kadar blokla, meşgul döngü kurma.
		<-ctx.Done()
		cmd.SetErr(ctx.Err())
	case next.err != nil:
		cmd.SetErr(next.err)
	default:
		cmd.SetVal(next.streams)
	}
	return cmd
}

func (f *fakeStreamClient) XAck(ctx context.Context, _, _ string, ids ...string) *redis.IntCmd {
	cmd := redis.NewIntCmd(ctx)

	f.mu.Lock()
	f.acked = append(f.acked, ids...)
	f.mu.Unlock()

	cmd.SetVal(int64(len(ids)))
	return cmd
}

// requestedCursors XReadGroup'a verilen imleri sırayla döner.
func (f *fakeStreamClient) requestedCursors() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.cursors)
}

// ackedIDs ACK'lenen mesaj kimliklerini döner.
func (f *fakeStreamClient) ackedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.acked)
}

// fakeConfig taklit istemciyle kullanılan yalıtılmış ayarı döner.
func fakeConfig() RedisConfig {
	return RedisConfig{
		StreamPrefix: "test:events",
		Group:        "test-grup",
		Consumer:     "test-tuketici",
		BlockTimeout: 10 * time.Millisecond,
	}
}

// scriptedRead tek bir stream'den mesaj dönen bir tur üretir.
func scriptedRead(stream string, msgs ...redis.XMessage) fakeRead {
	return fakeRead{streams: []redis.XStream{{Stream: stream, Messages: msgs}}}
}

// eventMessage yayımlanmış bir olayın stream karşılığını üretir.
func eventMessage(msgID, eventID, name string, when time.Time, data string) redis.XMessage {
	return redis.XMessage{
		ID: msgID,
		Values: map[string]any{
			fieldID:         eventID,
			fieldName:       name,
			fieldOccurredAt: when.Format(time.RFC3339Nano),
			fieldData:       data,
		},
	}
}

// waitClosed kanalın kapanmasını bekler; kapanmazsa testi düşürür.
func waitClosed(t *testing.T, ch <-chan struct{}, mesaj string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("zaman aşımı: %s", mesaj)
	}
}

// shutdownBus veri yolunu kapatır ve hata dönerse testi düşürür.
func shutdownBus(t *testing.T, b *redisBus) {
	t.Helper()
	if err := b.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown hata verdi: %v", err)
	}
}

func TestDecodeMessage(t *testing.T) {
	when := time.Date(2026, 8, 23, 12, 30, 0, 0, time.UTC)

	tests := []struct {
		name     string
		values   map[string]any
		wantErr  bool
		wantName string
		wantID   string
		wantTime time.Time
		wantData map[string]any
	}{
		{
			name: "tam mesaj",
			values: map[string]any{
				fieldID:         "evt_01",
				fieldName:       "order.paid",
				fieldOccurredAt: when.Format(time.RFC3339Nano),
				fieldData:       `{"order_id":"order_01","toplam":1999}`,
			},
			wantName: "order.paid",
			wantID:   "evt_01",
			wantTime: when,
			wantData: map[string]any{"order_id": "order_01", "toplam": float64(1999)},
		},
		{
			name: "ad alani yoksa yedek ad kullanilir",
			values: map[string]any{
				fieldID:   "evt_02",
				fieldData: `{"order_id":"order_02"}`,
			},
			wantName: testEventName,
			wantID:   "evt_02",
			wantData: map[string]any{"order_id": "order_02"},
		},
		{
			name:     "veri alani yok",
			values:   map[string]any{fieldID: "evt_03"},
			wantName: testEventName,
			wantID:   "evt_03",
		},
		{
			// Redis, bekleyen listede duran ama kırpılmış girdiyi alansız döner.
			name:    "alansiz mesaj (tombstone)",
			values:  nil,
			wantErr: true,
		},
		{
			name:    "bos alan haritasi",
			values:  map[string]any{},
			wantErr: true,
		},
		{
			name:    "bozuk occurred_at",
			values:  map[string]any{fieldID: "evt_04", fieldOccurredAt: "dun"},
			wantErr: true,
		},
		{
			name:    "bozuk json veri",
			values:  map[string]any{fieldID: "evt_05", fieldData: "{bozuk"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeMessage(testEventName, redis.XMessage{ID: "1-0", Values: tt.values})

			if tt.wantErr {
				if err == nil {
					t.Fatalf("hata beklendi, olay döndü: %+v", got)
				}
				if !errors.IsInvalid(err) {
					t.Errorf("Kind = %v, beklenen invalid", errors.KindOf(err))
				}
				if code := errors.CodeOf(err); code != CodeInvalidEvent {
					t.Errorf("Code = %q, beklenen %q", code, CodeInvalidEvent)
				}
				return
			}

			if err != nil {
				t.Fatalf("decodeMessage hata verdi: %v", err)
			}
			if got.Name != tt.wantName {
				t.Errorf("Name = %q, beklenen %q", got.Name, tt.wantName)
			}
			if got.ID != tt.wantID {
				t.Errorf("ID = %q, beklenen %q", got.ID, tt.wantID)
			}
			if !got.OccurredAt.Equal(tt.wantTime) {
				t.Errorf("OccurredAt = %v, beklenen %v", got.OccurredAt, tt.wantTime)
			}
			if !maps.Equal(got.Data, tt.wantData) {
				t.Errorf("Data = %v, beklenen %v", got.Data, tt.wantData)
			}
		})
	}
}

func TestMessagesOf(t *testing.T) {
	first := redis.XMessage{ID: "1-0", Values: map[string]any{fieldID: "evt_01"}}
	res := []redis.XStream{
		{Stream: "baska:stream", Messages: []redis.XMessage{{ID: "9-0"}}},
		{Stream: "test:events:order.placed", Messages: []redis.XMessage{first}},
	}

	got := messagesOf(res, "test:events:order.placed")
	if len(got) != 1 || got[0].ID != "1-0" {
		t.Errorf("messagesOf = %v, beklenen yalnızca 1-0 mesajı", got)
	}
	if got := messagesOf(res, "yok:stream"); got != nil {
		t.Errorf("eşleşmeyen stream için messagesOf = %v, beklenen nil", got)
	}
	if got := messagesOf(nil, "test:events:order.placed"); got != nil {
		t.Errorf("boş yanıt için messagesOf = %v, beklenen nil", got)
	}
}

func TestStringField(t *testing.T) {
	values := map[string]any{"metin": "deger", "sayi": 42}

	if got := stringField(values, "metin"); got != "deger" {
		t.Errorf("stringField(metin) = %q, beklenen deger", got)
	}
	if got := stringField(values, "sayi"); got != "" {
		t.Errorf("dize olmayan alan için stringField = %q, beklenen boş", got)
	}
	if got := stringField(values, "yok"); got != "" {
		t.Errorf("olmayan alan için stringField = %q, beklenen boş", got)
	}
	if got := stringField(nil, "metin"); got != "" {
		t.Errorf("nil harita için stringField = %q, beklenen boş", got)
	}
}

func TestConsumeReadsPendingListBeforeNewMessages(t *testing.T) {
	cfg := fakeConfig()
	stream := cfg.StreamName(testEventName)
	when := time.Date(2026, 8, 23, 12, 30, 0, 0, time.UTC)

	fake := newFakeStreamClient(
		// 1. tur: yeniden başlatmadan kalan bekleyen mesaj.
		scriptedRead(stream, eventMessage("1-1", "evt_bekleyen", testEventName, when, `{}`)),
		// 2. tur: bekleyen liste tükendi.
		scriptedRead(stream),
		// 3. tur: artık yalnızca yeni mesajlar.
		scriptedRead(stream, eventMessage("2-1", "evt_yeni", testEventName, when, `{}`)),
	)

	bus := newRedisBus(fake, cfg, quietLogger())

	seen := make(chan string, 4)
	if err := bus.Subscribe(testEventName, func(_ context.Context, e Event) error {
		seen <- e.ID
		return nil
	}); err != nil {
		t.Fatalf("Subscribe hata verdi: %v", err)
	}

	waitClosed(t, fake.drained, "betiklenmiş turlar tüketilmedi")
	shutdownBus(t, bus)

	// Tüketim, süreç yeniden başladığında BEKLEYEN listeden başlamalı; liste
	// sayfalanarak ilerlemeli ve tükendiğinde ">" imine geçmelidir.
	cursors := fake.requestedCursors()
	if len(cursors) < 3 {
		t.Fatalf("istenen imler = %v, en az 3 tur beklendi", cursors)
	}
	want := []string{cursorPending, "1-1", cursorNew}
	if !slices.Equal(cursors[:3], want) {
		t.Errorf("istenen imler = %v, beklenen %v", cursors[:3], want)
	}

	close(seen)
	var got []string
	for id := range seen {
		got = append(got, id)
	}
	if !slices.Equal(got, []string{"evt_bekleyen", "evt_yeni"}) {
		t.Errorf("teslim edilen kimlikler = %v, beklenen [evt_bekleyen evt_yeni]", got)
	}
}

func TestConsumeDeliversDecodedEventAndAcks(t *testing.T) {
	cfg := fakeConfig()
	stream := cfg.StreamName(testEventName)
	when := time.Date(2026, 8, 23, 12, 30, 0, 0, time.UTC)

	fake := newFakeStreamClient(
		scriptedRead(stream, eventMessage("5-0", "evt_01", "order.paid", when,
			`{"order_id":"order_01","toplam":1999,"kalemler":["a","b"]}`)),
	)
	bus := newRedisBus(fake, cfg, quietLogger())

	got := make(chan Event, 1)
	if err := bus.Subscribe(testEventName, func(_ context.Context, e Event) error {
		got <- e
		return nil
	}); err != nil {
		t.Fatalf("Subscribe hata verdi: %v", err)
	}

	waitClosed(t, fake.drained, "mesaj hiç okunmadı")
	shutdownBus(t, bus)

	select {
	case e := <-got:
		if e.Name != "order.paid" {
			t.Errorf("Name = %q, beklenen order.paid (mesajdaki ad yedeği ezmeli)", e.Name)
		}
		if e.ID != "evt_01" {
			t.Errorf("ID = %q, beklenen evt_01", e.ID)
		}
		if !e.OccurredAt.Equal(when) {
			t.Errorf("OccurredAt = %v, beklenen %v", e.OccurredAt, when)
		}
		if e.Data["order_id"] != "order_01" {
			t.Errorf("Data[order_id] = %v, beklenen order_01", e.Data["order_id"])
		}
		if e.Data["toplam"] != float64(1999) {
			t.Errorf("Data[toplam] = %v, beklenen 1999 (yük sessizce boşalmamalı)", e.Data["toplam"])
		}
		if kalemler, ok := e.Data["kalemler"].([]any); !ok || len(kalemler) != 2 {
			t.Errorf("Data[kalemler] = %v, beklenen 2 elemanlı dizi", e.Data["kalemler"])
		}
	default:
		t.Fatal("olay handler'a hiç ulaşmadı")
	}

	if acked := fake.ackedIDs(); !slices.Equal(acked, []string{"5-0"}) {
		t.Errorf("ACK'lenen kimlikler = %v, beklenen [5-0]", acked)
	}
}

func TestConsumeDoesNotDeliverTombstoneMessage(t *testing.T) {
	cfg := fakeConfig()
	stream := cfg.StreamName(testEventName)
	when := time.Date(2026, 8, 23, 12, 30, 0, 0, time.UTC)

	// Kırpılmış/silinmiş bir stream girdisi bekleyen listede kalır ve go-redis
	// tarafından alansız bir XMessage olarak dönülür.
	tombstone := redis.XMessage{ID: "7-0", Values: nil}

	fake := newFakeStreamClient(
		scriptedRead(stream, tombstone,
			eventMessage("7-1", "evt_saglam", testEventName, when, `{"order_id":"order_01"}`)),
	)

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	bus := newRedisBus(fake, cfg, log)

	seen := make(chan Event, 4)
	if err := bus.Subscribe(testEventName, func(_ context.Context, e Event) error {
		seen <- e
		return nil
	}); err != nil {
		t.Fatalf("Subscribe hata verdi: %v", err)
	}

	waitClosed(t, fake.drained, "mesajlar hiç okunmadı")
	shutdownBus(t, bus)

	if got := len(seen); got != 1 {
		t.Fatalf("handler çağrı sayısı = %d, beklenen 1 (tombstone teslim edilmemeli)", got)
	}
	if e := <-seen; e.ID != "evt_saglam" {
		t.Errorf("teslim edilen olay = %+v, beklenen yalnızca evt_saglam", e)
	}

	// Tombstone yine de ACK'lenmeli; aksi hâlde bekleyen listede sonsuza kalır.
	if acked := fake.ackedIDs(); !slices.Equal(acked, []string{"7-0", "7-1"}) {
		t.Errorf("ACK'lenen kimlikler = %v, beklenen [7-0 7-1]", acked)
	}
	if out := buf.String(); !strings.Contains(out, "çözülemedi") {
		t.Errorf("çözülemeyen mesaj loglanmadı; log çıktısı: %s", out)
	}
}

func TestConsumeContinuesAfterReadError(t *testing.T) {
	cfg := fakeConfig()
	stream := cfg.StreamName(testEventName)
	when := time.Date(2026, 8, 23, 12, 30, 0, 0, time.UTC)

	fake := newFakeStreamClient(
		fakeRead{err: errors.New("redis düştü")},
		scriptedRead(stream, eventMessage("3-0", "evt_01", testEventName, when, `{}`)),
	)

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	bus := newRedisBus(fake, cfg, log)

	got := make(chan string, 1)
	if err := bus.Subscribe(testEventName, func(_ context.Context, e Event) error {
		got <- e.ID
		return nil
	}); err != nil {
		t.Fatalf("Subscribe hata verdi: %v", err)
	}

	waitClosed(t, fake.drained, "okuma hatasından sonra tüketim sürmedi")
	shutdownBus(t, bus)

	select {
	case id := <-got:
		if id != "evt_01" {
			t.Errorf("teslim edilen kimlik = %q, beklenen evt_01", id)
		}
	default:
		t.Error("okuma hatasından sonraki mesaj teslim edilmedi")
	}
	if out := buf.String(); !strings.Contains(out, "okunamadı") {
		t.Errorf("okuma hatası loglanmadı; log çıktısı: %s", out)
	}
}

func TestSubscribeDoesNotBlockPublishWhileCreatingGroup(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	fake := newFakeStreamClient()
	fake.onGroupCreate = func(context.Context) error {
		close(entered)
		<-release
		return nil
	}

	bus := newRedisBus(fake, fakeConfig(), quietLogger())

	subscribed := make(chan error, 1)
	go func() {
		subscribed <- bus.Subscribe(testEventName, func(context.Context, Event) error { return nil })
	}()

	// Consumer group kurulumu (gerçek bir ağ turu) sürerken yayım denenir.
	waitClosed(t, entered, "consumer group kurulumu başlamadı")

	published := make(chan error, 1)
	go func() {
		published <- bus.Publish(context.Background(), Event{Name: testEventName})
	}()

	select {
	case err := <-published:
		if err != nil {
			t.Fatalf("Publish hata verdi: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Subscribe consumer group kurarken Publish bloklandı")
	}

	close(release)
	if err := <-subscribed; err != nil {
		t.Fatalf("Subscribe hata verdi: %v", err)
	}
	shutdownBus(t, bus)
}

func TestSubscribeFailsWhenGroupCannotBeCreated(t *testing.T) {
	fake := newFakeStreamClient()
	fake.onGroupCreate = func(context.Context) error {
		return errors.New("bağlantı reddedildi")
	}

	bus := newRedisBus(fake, fakeConfig(), quietLogger())
	defer func() { _ = bus.Shutdown(context.Background()) }()

	err := bus.Subscribe(testEventName, func(context.Context, Event) error { return nil })
	if err == nil {
		t.Fatal("grup kurulamadığında Subscribe hata dönmedi")
	}
	if !errors.HasKind(err, errors.KindUnavailable) {
		t.Errorf("Kind = %v, beklenen unavailable", errors.KindOf(err))
	}
	if code := errors.CodeOf(err); code != CodeSubscribeFailed {
		t.Errorf("Code = %q, beklenen %q", code, CodeSubscribeFailed)
	}

	// Başarısız kurulumdan sonra tüketici döngüsü başlamamış olmalı.
	if cursors := fake.requestedCursors(); len(cursors) != 0 {
		t.Errorf("istenen imler = %v, beklenen boş (döngü başlamamalıydı)", cursors)
	}
}

func TestRedisShutdownReturnsWhenContextExpires(t *testing.T) {
	cfg := fakeConfig()
	stream := cfg.StreamName(testEventName)
	when := time.Date(2026, 8, 23, 12, 30, 0, 0, time.UTC)

	fake := newFakeStreamClient(
		scriptedRead(stream, eventMessage("4-0", "evt_01", testEventName, when, `{}`)),
	)
	bus := newRedisBus(fake, cfg, quietLogger())

	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)

	if err := bus.Subscribe(testEventName, func(context.Context, Event) error {
		close(entered)
		<-release
		return nil
	}); err != nil {
		t.Fatalf("Subscribe hata verdi: %v", err)
	}
	waitClosed(t, entered, "handler hiç başlamadı")

	// Takılan handler kapanışı sonsuza dek kilitlememeli.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := bus.Shutdown(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("takılı handler'a rağmen Shutdown nil döndü")
	}
	if !errors.HasKind(err, errors.KindUnavailable) {
		t.Errorf("Kind = %v, beklenen unavailable", errors.KindOf(err))
	}
	if code := errors.CodeOf(err); code != CodeShutdownTimeout {
		t.Errorf("Code = %q, beklenen %q", code, CodeShutdownTimeout)
	}
	if elapsed > 5*time.Second {
		t.Errorf("Shutdown %v sürdü; ctx süresiyle sınırlı olmalıydı", elapsed)
	}
	if !bus.isClosed() {
		t.Error("zaman aşımından sonra veri yolu kapalı sayılmalı")
	}
}
