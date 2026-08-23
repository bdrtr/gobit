package container_test

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/errors"
)

// --- test yardımcıları ------------------------------------------------------

// recorder kapanış çağrılarını sırasıyla kaydeder.
type recorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *recorder) add(call string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
}

func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

// closerSvc yalnızca io.Closer karşılar.
type closerSvc struct {
	name string
	rec  *recorder
	err  error
}

func (s *closerSvc) Close() error {
	s.rec.add("close:" + s.name)
	return s.err
}

// shutdownSvc yalnızca Shutdown(ctx) error karşılar.
type shutdownSvc struct {
	name string
	rec  *recorder
	err  error
}

func (s *shutdownSvc) Shutdown(_ context.Context) error {
	s.rec.add("shutdown:" + s.name)
	return s.err
}

// panicOnCloseSvc kapanışında panikler (çift kapatmada panikleyen üçüncü parti
// istemcileri temsil eder).
type panicOnCloseSvc struct{}

func (panicOnCloseSvc) Close() error { panic("kapanış patladı") }

// bothSvc her iki arayüzü de karşılar; Shutdown tercih edilmelidir.
type bothSvc struct {
	name string
	rec  *recorder
}

func (s *bothSvc) Close() error {
	s.rec.add("close:" + s.name)
	return nil
}

func (s *bothSvc) Shutdown(_ context.Context) error {
	s.rec.add("shutdown:" + s.name)
	return nil
}

// productService sağlayıcı modülün somut servisini temsil eder.
type productService struct{ id string }

func (p productService) GetVariant(_ context.Context, variantID string) (string, error) {
	return p.id + ":" + variantID, nil
}

// productReader ADR 0001'deki tüketici tarafı dar arayüzdür.
type productReader interface {
	GetVariant(ctx context.Context, variantID string) (string, error)
}

// stockReader productService'in karşılamadığı bir arayüzdür.
type stockReader interface {
	Reserve(ctx context.Context, variantID string, qty int) error
}

// wrongVariantReader aynı adı taşır ama imzası uyumsuzdur.
type wrongVariantReader interface {
	GetVariant(ctx context.Context, variantID string) (int, error)
}

// pointerService'in metodları işaretçi alıcılıdır.
type pointerService struct{}

func (p *pointerService) Ping(_ context.Context) error { return nil }

type pinger interface {
	Ping(ctx context.Context) error
}

// logRecorder loglanan kayıtları biriktiren slog handler'ıdır.
type logRecorder struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *logRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (h *logRecorder) Handle(_ context.Context, rec slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, rec.Clone())
	return nil
}

func (h *logRecorder) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *logRecorder) WithGroup(string) slog.Handler { return h }

// has verilen seviyede ve mesajı substr içeren bir kayıt olup olmadığını
// bildirir.
func (h *logRecorder) has(level slog.Level, substr string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.records {
		if h.records[i].Level == level && strings.Contains(h.records[i].Message, substr) {
			return true
		}
	}
	return false
}

// mustFinish fn'i süre içinde bitmezse testi düşürür; deadlock'a karşı korur.
func mustFinish(t *testing.T, d time.Duration, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("işlem %s içinde bitmedi; deadlock olabilir", d)
	}
}

// --- kayıt ve çözüm ---------------------------------------------------------

func TestProvideValueAndResolve(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide("product.service", productService{id: "p1"}))

	got, err := container.Resolve[productService](c, "product.service")
	require.NoError(t, err)
	require.Equal(t, "p1", got.id)
}

func TestLazyCtorRunsOnlyOnFirstResolve(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	c := container.New(nil)
	require.NoError(t, c.Provide("lazy", func(_ *container.Container) (any, error) {
		calls.Add(1)
		return productService{id: "lazy"}, nil
	}))

	// Kayıt görünür ama yapıcı henüz çalışmamış olmalı.
	require.True(t, c.Has("lazy"))
	require.Equal(t, []string{"lazy"}, c.Names())
	require.Equal(t, int64(0), calls.Load())

	first, err := container.Resolve[productService](c, "lazy")
	require.NoError(t, err)
	require.Equal(t, int64(1), calls.Load())

	second, err := container.Resolve[productService](c, "lazy")
	require.NoError(t, err)
	require.Equal(t, int64(1), calls.Load(), "yapıcı ikinci Resolve'da yeniden çalışmaz")
	require.Equal(t, first, second)
}

func TestSingletonUnderConcurrentResolve(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	c := container.New(nil)
	require.NoError(t, c.Provide("singleton", func(_ *container.Container) (any, error) {
		calls.Add(1)
		time.Sleep(5 * time.Millisecond) // yarışı görünür kıl
		return &productService{id: "tek"}, nil
	}))

	const n = 100
	results := make([]*productService, n)
	errs := make([]error, n)

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i], errs[i] = container.Resolve[*productService](c, "singleton")
		}()
	}
	close(start)
	wg.Wait()

	require.Equal(t, int64(1), calls.Load(), "yapıcı tam bir kez çalışmalı")
	for i := range n {
		require.NoError(t, errs[i])
		require.Same(t, results[0], results[i], "tüm çözümler aynı örneği vermeli")
	}
}

func TestResolveUnknownNameIsNotFound(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide("product.service", productService{}))

	_, err := container.Resolve[productService](c, "pricing.service")
	require.Error(t, err)
	require.True(t, errors.IsNotFound(err))
	require.Contains(t, err.Error(), "pricing.service")
	require.Contains(t, err.Error(), "product.service", "mesaj kayıtlı adları listelemeli")
}

func TestDuplicateProvideIsConflict(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide("dummy.service", productService{id: "ilk"}))

	err := c.Provide("dummy.service", productService{id: "ikinci"})
	require.Error(t, err)
	require.True(t, errors.IsConflict(err))
	require.Contains(t, err.Error(), "dummy.service")

	// İlk kayıt korunmalı.
	got, err := container.Resolve[productService](c, "dummy.service")
	require.NoError(t, err)
	require.Equal(t, "ilk", got.id)
}

func TestProvideRejectsInvalidArgs(t *testing.T) {
	t.Parallel()

	c := container.New(nil)

	require.True(t, errors.IsInvalid(c.Provide("", productService{})))
	require.True(t, errors.IsInvalid(c.Provide("nil.value", nil)))

	var nilCtor func(*container.Container) (any, error)
	require.True(t, errors.IsInvalid(c.Provide("nil.ctor", nilCtor)))
}

func TestProvideRejectsTypedNil(t *testing.T) {
	t.Parallel()

	c := container.New(nil)

	// (*closerSvc)(nil) ARAYÜZ-nil değildir: value == nil kontrolünü geçer ama
	// servis olarak kullanılamaz.
	var svc *closerSvc
	err := c.Provide("typed.nil", svc)
	require.Error(t, err)
	require.True(t, errors.IsInvalid(err))
	require.Equal(t, "container_invalid_ctor", errors.CodeOf(err))
	require.Contains(t, err.Error(), "*container_test.closerSvc", "mesaj verilen tipi yazmalı")
	require.False(t, c.Has("typed.nil"), "nil servis kaba girmemeli")
}

func TestProvideRejectsWrongCtorSignature(t *testing.T) {
	t.Parallel()

	c := container.New(nil)

	// Somut tip dönen yapıcı Ctor imzasını tutturmaz; sessizce DEĞER olarak
	// kaydedilmemeli, kayıt anında reddedilmeli.
	err := c.Provide("typed.ctor", func(_ *container.Container) (*closerSvc, error) {
		return &closerSvc{}, nil
	})
	require.Error(t, err)
	require.True(t, errors.IsInvalid(err))
	require.Equal(t, "container_invalid_ctor", errors.CodeOf(err))
	require.Contains(t, err.Error(), "func(*container.Container) (any, error)", "beklenen imza gösterilmeli")
	require.False(t, c.Has("typed.ctor"))

	// *Container almayan bir işlev hâlâ hazır değer olarak kaydedilebilir.
	require.NoError(t, c.Provide("fabrika", func() string { return "x" }))
	fn, err := container.Resolve[func() string](c, "fabrika")
	require.NoError(t, err)
	require.Equal(t, "x", fn())
}

// --- tip uyumsuzluğu teşhisi (ADR 0001) -------------------------------------

func TestResolveConsumerInterface(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide("product.service", productService{id: "prod"}))

	// Tüketici, sağlayıcıyı import etmeden kendi dar arayüzüyle çözer.
	reader, err := container.Resolve[productReader](c, "product.service")
	require.NoError(t, err)

	variant, err := reader.GetVariant(t.Context(), "v1")
	require.NoError(t, err)
	require.Equal(t, "prod:v1", variant)
}

func TestResolveTypeMismatchNamesBothTypes(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide("product.service", productService{id: "prod"}))

	_, err := container.Resolve[stockReader](c, "product.service")
	require.Error(t, err)
	require.True(t, errors.IsInvalid(err))

	msg := err.Error()
	require.Contains(t, msg, "container_test.productService", "kayıtlı somut tip yazılmalı")
	require.Contains(t, msg, "container_test.stockReader", "beklenen tip yazılmalı")
	require.Contains(t, msg, "eksik: Reserve(context.Context, string, int) error")
}

func TestResolveTypeMismatchExplainsSignature(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide("product.service", productService{}))

	_, err := container.Resolve[wrongVariantReader](c, "product.service")
	require.Error(t, err)
	require.Contains(t, err.Error(), "uyumsuz: GetVariant(context.Context, string) (int, error)")
	require.Contains(t, err.Error(), "kayıtlı: GetVariant(context.Context, string) (string, error)")
}

func TestResolveTypeMismatchHintsPointerReceiver(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide("ptr.service", pointerService{}))

	_, err := container.Resolve[pinger](c, "ptr.service")
	require.Error(t, err)
	require.Contains(t, err.Error(), "işaretçi alıcılıdır")
	require.Contains(t, err.Error(), "*container_test.pointerService")
}

func TestResolveTypeMismatchOnConcreteType(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide("product.service", productService{}))

	_, err := container.Resolve[*productService](c, "product.service")
	require.Error(t, err)
	require.Contains(t, err.Error(), "container_test.productService")
	require.Contains(t, err.Error(), "*container_test.productService")
}

// --- bağımlılık zinciri ve döngü --------------------------------------------

func TestCtorResolvesDependencyChain(t *testing.T) {
	t.Parallel()

	c := container.New(nil)

	// c, b'ye; b, a'ya bağımlı. Kayıt sırası bilinçli olarak terstir:
	// tembel yapıcı sayesinde henüz kayıtlı olmayan bir ada bağımlı olunabilir.
	require.NoError(t, c.Provide("c.service", func(cc *container.Container) (any, error) {
		dep, err := container.Resolve[string](cc, "b.service")
		if err != nil {
			return nil, err
		}
		return dep + ">c", nil
	}))
	require.NoError(t, c.Provide("b.service", func(cc *container.Container) (any, error) {
		dep, err := container.Resolve[string](cc, "a.service")
		if err != nil {
			return nil, err
		}
		return dep + ">b", nil
	}))
	require.NoError(t, c.Provide("a.service", "a"))

	got, err := container.Resolve[string](c, "c.service")
	require.NoError(t, err)
	require.Equal(t, "a>b>c", got)
}

func TestDependencyCycleIsReportedNotDeadlocked(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide("a", func(cc *container.Container) (any, error) {
		return container.Resolve[string](cc, "b")
	}))
	require.NoError(t, c.Provide("b", func(cc *container.Container) (any, error) {
		return container.Resolve[string](cc, "a")
	}))

	var err error
	mustFinish(t, 5*time.Second, func() {
		_, err = container.Resolve[string](c, "a")
	})

	require.Error(t, err)
	require.True(t, errors.IsConflict(err))
	require.Contains(t, err.Error(), "bağımlılık döngüsü: a -> b -> a")
}

func TestSelfDependencyCycle(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide("self", func(cc *container.Container) (any, error) {
		return container.Resolve[string](cc, "self")
	}))

	var err error
	mustFinish(t, 5*time.Second, func() {
		_, err = container.Resolve[string](c, "self")
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "bağımlılık döngüsü: self -> self")
}

func TestCycleAcrossGoroutines(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	started := make(chan struct{}, 2)
	release := make(chan struct{})

	provide := func(name, dep string) {
		require.NoError(t, c.Provide(name, func(cc *container.Container) (any, error) {
			started <- struct{}{}
			<-release
			return container.Resolve[string](cc, dep)
		}))
	}
	provide("a", "b")
	provide("b", "a")

	errs := make([]error, 2)
	mustFinish(t, 5*time.Second, func() {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, errs[0] = container.Resolve[string](c, "a")
		}()
		go func() {
			defer wg.Done()
			_, errs[1] = container.Resolve[string](c, "b")
		}()
		// İki yapıcı da kendi kaydını üstlenene kadar bekle, sonra bırak.
		<-started
		<-started
		close(release)
		wg.Wait()
	})

	require.Error(t, errs[0])
	require.Error(t, errs[1])
	require.True(t,
		strings.Contains(errs[0].Error(), "bağımlılık döngüsü") ||
			strings.Contains(errs[1].Error(), "bağımlılık döngüsü"),
		"iki goroutine arasındaki döngü de raporlanmalı: %v / %v", errs[0], errs[1])
}

func TestCycleDetectedWhenCtorResolvesConcurrently(t *testing.T) {
	t.Parallel()

	// "a" yapıcısı aynı anda İKİ Resolve çağırır: biri döngüyü kapatan "b",
	// diğeri bağımsız "yavas". Bekleme grafiği düğüm başına tek kenar tutsaydı
	// ikinci kenar birincisini ezer, döngü görülmez ve kap kilitlenirdi.
	c := container.New(nil)
	bStarted := make(chan struct{})
	slowStarted := make(chan struct{})
	letB := make(chan struct{})
	releaseSlow := make(chan struct{})

	var bErr error
	require.NoError(t, c.Provide("a", func(cc *container.Container) (any, error) {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, bErr = container.Resolve[string](cc, "b")
		}()
		<-bStarted // a -> b kenarı eklendi

		go func() {
			defer wg.Done()
			_, _ = container.Resolve[string](cc, "yavas")
		}()
		<-slowStarted // a -> yavas kenarı da eklendi

		close(letB)
		close(releaseSlow)
		wg.Wait()
		return "a", nil
	}))
	require.NoError(t, c.Provide("b", func(cc *container.Container) (any, error) {
		close(bStarted)
		<-letB
		return container.Resolve[string](cc, "a")
	}))
	require.NoError(t, c.Provide("yavas", func(_ *container.Container) (any, error) {
		close(slowStarted)
		<-releaseSlow
		return "yavas", nil
	}))

	var (
		got string
		err error
	)
	mustFinish(t, 5*time.Second, func() {
		got, err = container.Resolve[string](c, "a")
	})

	require.NoError(t, err)
	require.Equal(t, "a", got)
	require.Error(t, bErr)
	require.True(t, errors.IsConflict(bErr))
	require.Contains(t, bErr.Error(), "bağımlılık döngüsü: a -> b -> a")
}

func TestSlowBuildLogsWaitWarning(t *testing.T) {
	t.Parallel()

	// Kök container'ı closure ile yakalayan yapıcıların kapattığı döngü
	// grafikte görünmez ve bekleme sonsuza kadar sürer. En azından loglanmalı.
	logs := &logRecorder{}
	c := container.New(slog.New(logs))
	container.SetWaitWarn(c, 10*time.Millisecond)

	started := make(chan struct{})
	release := make(chan struct{})
	require.NoError(t, c.Provide("yavas", func(_ *container.Container) (any, error) {
		close(started)
		<-release
		return "hazır", nil
	}))

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = container.Resolve[string](c, "yavas")
	}()
	<-started
	go func() {
		defer wg.Done()
		_, _ = container.Resolve[string](c, "yavas") // kuran goroutine'i bekler
	}()

	require.Eventually(t, func() bool {
		return logs.has(slog.LevelWarn, "bağımlılık döngüsü olabilir")
	}, 5*time.Second, 5*time.Millisecond, "uzayan kurulum beklemesi uyarı loglamalı")

	close(release)
	wg.Wait()
}

// --- yapıcı hataları --------------------------------------------------------

func TestCtorErrorIsCachedAndRunsOnce(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	boom := errors.NotFound("dep_missing", "bağımlılık yok")

	c := container.New(nil)
	require.NoError(t, c.Provide("broken", func(_ *container.Container) (any, error) {
		calls.Add(1)
		return nil, boom
	}))

	for range 3 {
		_, err := container.Resolve[string](c, "broken")
		require.Error(t, err)
		require.ErrorIs(t, err, boom)
		// Yapıcının hata sınıfı korunur.
		require.True(t, errors.IsNotFound(err))
		require.Contains(t, err.Error(), `"broken" servisi kurulamadı`)
	}
	require.Equal(t, int64(1), calls.Load(), "hata önbelleğe alınmalı; yapıcı yeniden çalışmaz")
}

func TestCtorPanicBecomesError(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide("panicky", func(_ *container.Container) (any, error) {
		panic("bağlantı dizesi boş")
	}))

	var err error
	mustFinish(t, 5*time.Second, func() {
		_, err = container.Resolve[string](c, "panicky")
	})

	require.Error(t, err)
	require.Equal(t, "container_ctor_panic", errors.CodeOf(err))
	require.Contains(t, err.Error(), "bağlantı dizesi boş")
}

func TestCtorReturningNilIsError(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide("nilly", func(_ *container.Container) (any, error) {
		return nil, nil
	}))

	_, err := container.Resolve[productReader](c, "nilly")
	require.Error(t, err)
	require.Equal(t, "container_ctor_nil", errors.CodeOf(err))
}

func TestCtorReturningTypedNilIsError(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide("typed.nilly", func(_ *container.Container) (any, error) {
		// Hata yolunda sık yapılan yazım hatası: nil yerine sıfır değerli
		// işaretçi dönmek.
		var svc *closerSvc
		return svc, nil
	}))

	_, err := container.Resolve[*closerSvc](c, "typed.nilly")
	require.Error(t, err)
	require.Equal(t, "container_ctor_nil", errors.CodeOf(err))
	require.Contains(t, err.Error(), "*container_test.closerSvc", "mesaj dönen tipi yazmalı")
}

// --- MustResolve ------------------------------------------------------------

func TestMustResolve(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide("product.service", productService{id: "p"}))

	require.NotPanics(t, func() {
		got := container.MustResolve[productReader](c, "product.service")
		require.NotNil(t, got)
	})
	require.Panics(t, func() {
		_ = container.MustResolve[productReader](c, "yok")
	})
}

// --- Names / Has ------------------------------------------------------------

func TestNamesAreSorted(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	for _, name := range []string{"pricing.service", "auth.service", "product.service"} {
		require.NoError(t, c.Provide(name, name))
	}

	require.Equal(t,
		[]string{"auth.service", "pricing.service", "product.service"},
		c.Names())
	require.True(t, c.Has("auth.service"))
	require.False(t, c.Has("cart.service"))
}

// --- Shutdown ---------------------------------------------------------------

func TestShutdownClosesInReverseOrderAndJoinsErrors(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	boom := errors.New("kapatma patladı")

	c := container.New(nil)
	require.NoError(t, c.Provide("a", &closerSvc{name: "a", rec: rec, err: boom}))
	require.NoError(t, c.Provide("b", &shutdownSvc{name: "b", rec: rec}))
	require.NoError(t, c.Provide("c", &bothSvc{name: "c", rec: rec}))
	require.NoError(t, c.Provide("d", "kapatılamayan düz değer"))

	err := c.Shutdown(t.Context())
	require.Error(t, err)
	require.ErrorIs(t, err, boom)
	require.Contains(t, err.Error(), `"a" servisi kapatılamadı`)

	require.Equal(t,
		[]string{"shutdown:c", "shutdown:b", "close:a"},
		rec.snapshot(),
		"kapatma kayıt sırasının tersi olmalı; Shutdown, Close'a tercih edilmeli")
}

func TestShutdownJoinsMultipleErrors(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	first := errors.New("ilk")
	second := errors.New("ikinci")

	c := container.New(nil)
	require.NoError(t, c.Provide("a", &closerSvc{name: "a", rec: rec, err: first}))
	require.NoError(t, c.Provide("b", &shutdownSvc{name: "b", rec: rec, err: second}))

	err := c.Shutdown(t.Context())
	require.Error(t, err)
	require.ErrorIs(t, err, first)
	require.ErrorIs(t, err, second)
	require.Equal(t, []string{"shutdown:b", "close:a"}, rec.snapshot())
}

func TestShutdownDoesNotBuildLazyServices(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	rec := &recorder{}

	c := container.New(nil)
	require.NoError(t, c.Provide("hazır", &closerSvc{name: "hazır", rec: rec}))
	require.NoError(t, c.Provide("tembel", func(_ *container.Container) (any, error) {
		calls.Add(1)
		return &closerSvc{name: "tembel", rec: rec}, nil
	}))

	require.NoError(t, c.Shutdown(t.Context()))
	require.Equal(t, int64(0), calls.Load(), "çözülmemiş tembel kayıt kapatmak için kurulmaz")
	require.Equal(t, []string{"close:hazır"}, rec.snapshot())
}

func TestShutdownIsIdempotentAndSealsContainer(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	c := container.New(nil)
	require.NoError(t, c.Provide("a", &closerSvc{name: "a", rec: rec}))

	require.NoError(t, c.Shutdown(t.Context()))
	require.NoError(t, c.Shutdown(t.Context()), "ikinci Shutdown no-op olmalı")
	require.Equal(t, []string{"close:a"}, rec.snapshot())

	provideErr := c.Provide("b", "x")
	require.True(t, errors.HasKind(provideErr, errors.KindUnavailable))

	_, resolveErr := container.Resolve[*closerSvc](c, "a")
	require.True(t, errors.HasKind(resolveErr, errors.KindUnavailable))
}

func TestShutdownClosesEvenWithCanceledContext(t *testing.T) {
	t.Parallel()

	// İptal edilmiş bir bütçe kaynakların sızmasına neden olmamalı: idempotentlik
	// yüzünden ikinci bir deneme şansı da yok.
	rec := &recorder{}
	c := container.New(nil)
	require.NoError(t, c.Provide("a", &closerSvc{name: "a", rec: rec}))
	require.NoError(t, c.Provide("b", &shutdownSvc{name: "b", rec: rec}))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := c.Shutdown(ctx)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	require.Contains(t, err.Error(), "kapatma bağlamı iptal edildi")
	require.Equal(t, []string{"shutdown:b", "close:a"}, rec.snapshot(),
		"iptal edilmiş bağlamda da tüm servisler kapatılmalı")
}

func TestShutdownClosesResolvedLazyService(t *testing.T) {
	t.Parallel()

	// Üretimde kapatılması gereken servislerin (DB havuzu, Redis) tamamı bu
	// yoldan geçer: tembel kayıt + Resolve + Shutdown.
	rec := &recorder{}
	c := container.New(nil)
	require.NoError(t, c.Provide("hazır", &closerSvc{name: "hazır", rec: rec}))
	require.NoError(t, c.Provide("tembel", func(_ *container.Container) (any, error) {
		return &closerSvc{name: "tembel", rec: rec}, nil
	}))

	_, err := container.Resolve[*closerSvc](c, "tembel")
	require.NoError(t, err)

	require.NoError(t, c.Shutdown(t.Context()))
	require.Equal(t, []string{"close:tembel", "close:hazır"}, rec.snapshot(),
		"çözülmüş tembel servis de kayıt sırasının tersine kapatılmalı")
}

func TestShutdownSkipsFailedLazyService(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	c := container.New(nil)
	require.NoError(t, c.Provide("bozuk", func(_ *container.Container) (any, error) {
		return &closerSvc{name: "bozuk", rec: rec}, errors.New("kurulamadı")
	}))

	_, err := container.Resolve[*closerSvc](c, "bozuk")
	require.Error(t, err)

	require.NoError(t, c.Shutdown(t.Context()))
	require.Empty(t, rec.snapshot(), "yapıcısı hata dönen kaydın servisi kapatılmaz")
}

func TestShutdownWaitsForInFlightCtor(t *testing.T) {
	t.Parallel()

	// SIGTERM anında bir istek 'db' servisini ilk kez çözüyor olabilir. Yapıcı
	// kilit dışında çalıştığı için kapanış onu görmezse servis ne kapatılır ne
	// de bir daha erişilebilir; kalıcı sızıntı.
	rec := &recorder{}
	started := make(chan struct{})

	c := container.New(nil)
	require.NoError(t, c.Provide("db", func(_ *container.Container) (any, error) {
		close(started)
		// Kapanış BAŞLAYANA kadar bekle: kap kapandığında çözüm denemeleri
		// Unavailable döner, kayıtsız bir ad bundan önce NotFound dönerdi.
		for {
			_, err := container.Resolve[string](c, "kayıtsız")
			if errors.HasKind(err, errors.KindUnavailable) {
				return &closerSvc{name: "db", rec: rec}, nil
			}
			time.Sleep(time.Millisecond)
		}
	}))

	resolveErr := make(chan error, 1)
	go func() {
		_, err := container.Resolve[*closerSvc](c, "db")
		resolveErr <- err
	}()
	<-started

	var shutErr error
	mustFinish(t, 10*time.Second, func() { shutErr = c.Shutdown(t.Context()) })

	require.NoError(t, shutErr)
	require.Equal(t, []string{"close:db"}, rec.snapshot(),
		"kapanış sırasında kurulmayı bitiren servis de kapatılmalı")
	require.True(t, errors.HasKind(<-resolveErr, errors.KindUnavailable),
		"kapatılmış kap canlı servis dağıtmamalı")
}

func TestShutdownRecoversPanickingClose(t *testing.T) {
	t.Parallel()

	// Kapatma kayıt sırasının tersine yürür: panikleyen bir servis, kendisinden
	// ÖNCE kaydedilmiş servislerin kapatılmasını engellememeli.
	rec := &recorder{}
	c := container.New(nil)
	require.NoError(t, c.Provide("sağlam", &closerSvc{name: "sağlam", rec: rec}))
	require.NoError(t, c.Provide("panikli", panicOnCloseSvc{}))

	var err error
	require.NotPanics(t, func() { err = c.Shutdown(t.Context()) })

	require.Error(t, err)
	require.Contains(t, err.Error(), "container_close_panic")
	require.Contains(t, err.Error(), "kapanış patladı")
	require.Contains(t, err.Error(), `"panikli" servisi kapatılamadı`)
	require.Equal(t, []string{"close:sağlam"}, rec.snapshot(),
		"panik sonrası kalan servisler kapatılmaya devam etmeli")
}
