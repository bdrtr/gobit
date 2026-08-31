// Command server gobit commerce framework'ünün tek binary giriş noktasıdır.
//
// Akış: config yükle -> logger ve izleme kur -> container kur -> altyapı
// servislerini (Postgres, Redis, event bus) kaydet -> eklentileri kur ->
// modülleri bootstrap et -> eklentileri başlat -> dinle.
//
// Bu paket, mimarinin TEK "her şeyi bilen" noktasıdır: çekirdek modülleri,
// modüller birbirini, eklentiler de commerce modüllerini tanımaz. Kimin
// kiminle konuşacağına dair her karar burada, açıkça verilir.
package main

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/redis/go-redis/v9"

	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/eventbus"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/core/link"
	"github.com/bdrtr/gobit/internal/core/logger"
	"github.com/bdrtr/gobit/internal/core/module"
	"github.com/bdrtr/gobit/internal/core/observability"
	coreplugin "github.com/bdrtr/gobit/internal/core/plugin"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/core/workflow"
	"github.com/bdrtr/gobit/internal/core/workflow/pgstore"
	"github.com/bdrtr/gobit/internal/modules/auth"
	"github.com/bdrtr/gobit/internal/modules/cart"
	"github.com/bdrtr/gobit/internal/modules/customer"
	"github.com/bdrtr/gobit/internal/modules/fulfillment"
	"github.com/bdrtr/gobit/internal/modules/inventory"
	"github.com/bdrtr/gobit/internal/modules/order"
	"github.com/bdrtr/gobit/internal/modules/payment"
	"github.com/bdrtr/gobit/internal/modules/pricing"
	"github.com/bdrtr/gobit/internal/modules/product"
	"github.com/bdrtr/gobit/internal/modules/promotion"
	"github.com/bdrtr/gobit/internal/modules/region"
	"github.com/bdrtr/gobit/internal/modules/tax"
)

// Container'daki altyapı servislerinin adları. Modüller bu adlarla çözer.
const (
	svcDB       = "core.db"
	svcRedis    = "core.redis"
	svcEventBus = "core.eventbus"
	// svcWorkflow saga yürütücüsüdür; modüller arası akışlar buradan çalışır.
	svcWorkflow = "core.workflow"
	// svcWorkflowStore yürütme durumunun kalıcı deposudur.
	svcWorkflowStore = "core.workflow.store"
	// svcLink Module Links servisidir; modüller link tanımlarını buradan bildirir.
	svcLink = "core.link"
	// svcQuery cross-module okuma katmanıdır.
	svcQuery = "core.query"
)

// version derleme sırasında -ldflags ile doldurulur (bkz. Makefile).
var version = "dev"

func main() {
	if err := run(); err != nil {
		// Logger kurulmadan da hata görünür olmalı.
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

// run uygulamanın tüm yaşam döngüsünü yönetir ve ilk hatada geri döner.
// main'den ayrı tutulmasının sebebi, os.Exit'in defer'ları atlamasıdır.
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := logger.New(logger.Options{
		Level:     cfg.SlogLevel(),
		Format:    cfg.LogFormat,
		AddSource: !cfg.IsProduction(),
	})
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("gobit başlatılıyor",
		"version", version,
		"env", cfg.AppEnv,
		"log_level", cfg.LogLevel,
		"event_bus", cfg.EventBus,
		"eklentiler", cfg.Plugins,
	)

	// İzleme kurulumu BAŞARISIZ OLSA BİLE uygulama açılır (ADR 0007): izleme,
	// ürünün doğruluğu için değil görünürlüğü için vardır ve toplayıcının
	// kesintisi mağazayı kapatmamalıdır. OTLP adresi verilmemişse hiçbir dış
	// bağlantı denenmez.
	izlemeKapat, err := observability.Setup(ctx, observability.Options{
		Endpoint:       cfg.OTLPEndpoint,
		Insecure:       cfg.OTLPInsecure,
		ServiceName:    cfg.ServiceName,
		ServiceVersion: version,
		Environment:    cfg.AppEnv,
		SampleRatio:    cfg.TraceSampleRatio,
		MetricInterval: cfg.MetricInterval,
		Logger:         log,
	})
	if err != nil {
		log.Warn("izleme kurulamadı, kapalı devam ediliyor", "error", err)
	}
	// Kapanış bağlamı iptal EDİLMEMİŞ olmalıdır: SIGTERM ctx'i çoktan iptal
	// etmiş olur ve bekleyen span'lar gönderilemeden düşerdi.
	defer func() {
		if err := izlemeKapat(context.WithoutCancel(ctx)); err != nil {
			log.Error("izleme kapatılamadı", "error", err)
		}
	}()

	c := container.New(log)

	pool, err := db.New(ctx, db.DefaultConfig(cfg.DatabaseURL), log)
	if err != nil {
		return err
	}
	// Defer'lar LIFO çalışır: önce container servisleri kapanır, sonra havuz.
	defer pool.Close()
	defer shutdownContainer(ctx, c, cfg, log)

	if err := c.Provide(svcDB, pool); err != nil {
		return err
	}

	// Çekirdek migration'ları modül migration'larından ÖNCE uygulanır: modüller
	// workflow motorunun şemasının hazır olduğunu varsayabilmelidir.
	if err := db.Migrate(ctx, cfg.DatabaseURL, pgstore.Migrations(), pgstore.MigrationOwner); err != nil {
		return err
	}

	links := link.New(pool, log)
	if err := c.Provide(svcLink, links); err != nil {
		return err
	}
	if err := c.Provide(svcQuery, query.New(links, c, log)); err != nil {
		return err
	}

	workflowStore := pgstore.New(pool, log)
	if err := c.Provide(svcWorkflowStore, workflowStore); err != nil {
		return err
	}
	if err := c.Provide(svcWorkflow, workflow.New(workflowStore, log)); err != nil {
		return err
	}

	checks := map[string]corehttp.HealthCheck{"postgres": pool.Ping}

	// Redis istemcisi, olay veri yolu ve koruma arka ucu tarafından PAYLAŞILIR;
	// ikisi de bellek içiyse hiç açılmaz ve nil kalır.
	redisClient, err := setupRedis(ctx, c, cfg, checks, log)
	if err != nil {
		return err
	}

	bus, err := setupEventBus(ctx, cfg, redisClient, log)
	if err != nil {
		return err
	}
	if err := c.Provide(svcEventBus, bus); err != nil {
		return err
	}

	// Kimlik doğrulayıcı auth modülü Register olduğunda doğar, koruma
	// middleware'i ise router kurulurken takılmalıdır. Aradaki boşluğu
	// gecikmeli doğrulayıcı kapatır (bkz. kurulum.go).
	authn := &corehttp.DeferredAuthenticator{}

	yigin, err := korumaYigini(cfg, authn, redisClient, log)
	if err != nil {
		return err
	}

	router := corehttp.NewRouter(corehttp.RouterOptions{
		Version:          version,
		Logger:           log,
		ReadinessChecks:  checks,
		TelemetryService: cfg.ServiceName,
		Middlewares:      yigin,
	})

	registry := module.NewRegistry(log, func(ctx context.Context, src fs.FS, owner string) error {
		return db.Migrate(ctx, cfg.DatabaseURL, src, owner)
	})
	// Commerce modülleri. Sıra ÖNEMSİZDİR: registry tüm modülleri register
	// ettikten SONRA migration ve route adımlarına geçer, dolayısıyla bir
	// modülün handler'ı başka modülün servisini güvenle çözebilir.
	// Faz 4: katalog
	registry.Add(product.New())
	registry.Add(pricing.New(log))
	registry.Add(inventory.New())
	// Faz 5: sepet akışı
	registry.Add(region.New(log))
	registry.Add(customer.New(log))
	registry.Add(cart.New())
	// Faz 6: ödeme ve sipariş
	registry.Add(payment.New())
	registry.Add(order.New())
	// Faz 7: kargo, promosyon, vergi
	registry.Add(fulfillment.New())
	registry.Add(promotion.New(log))
	registry.Add(tax.New(log))
	// Faz 8: kimlik. Diğer modüllerden bağımsızdır; yalnızca çekirdek havuzunu
	// ister ve karşılığında koruma middleware'inin ihtiyacı olan doğrulayıcıyı
	// container'a bırakır.
	registry.Add(auth.New(auth.Options{
		JWTSecret: jwtSirri(cfg, log),
		JWTTTL:    cfg.JWTTTL,
		JWTIssuer: cfg.ServiceName,
		Logger:    log,
	}))

	// Eklentiler modüllerden ÖNCE kurulur: eklentinin getirdiği modül de
	// Register/migration/route döngüsünden geçebilmelidir.
	eklentiler, err := eklentileriSec(cfg.Plugins)
	if err != nil {
		return err
	}
	host := coreplugin.NewHost(c, registry, bus, log, eklentiAyarlari())
	if err := eklentiler.Install(ctx, host); err != nil {
		return err
	}

	if err := registry.Bootstrap(ctx, c, router); err != nil {
		return err
	}

	// Kimlik doğrulayıcı ancak Bootstrap'tan sonra container'dadır.
	// Çözülemezse açılış DURUR: korumalı görünen ama her isteği reddeden bir
	// yönetim yüzeyiyle çalışmaya devam etmek, arızayı ilk giriş denemesine
	// kadar gizlerdi.
	dogrulayici, err := container.Resolve[corehttp.Authenticator](c, auth.InteropName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), "auth_interop_missing",
			"kimlik doğrulayıcı %q çözülemedi", auth.InteropName)
	}
	authn.Bind(dogrulayici)

	// İlk yönetici tohumu da Bootstrap'tan SONRA çalışır: auth servisi ancak o
	// zaman container'dadır ve tablolar ancak o zaman göçürülmüştür. Servis DAR
	// bir arayüzle alınır (bkz. kurulum.go), somut tipiyle değil.
	//
	// Hata açılışı DURDURUR: yaratılamamış bir yönetici, yönetim yüzeyi olmayan
	// bir sistem demektir ve bu, açılıp da hiçbir yönetim isteğini kabul
	// etmeyen bir sunucudan çok daha erken fark edilir.
	kullanicilar, err := container.Resolve[yoneticiKullanicilari](c, auth.ServiceName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeBootstrapFailed,
			"auth servisi %q çözülemedi", auth.ServiceName)
	}
	if err := tohumlaYonetici(ctx, kullanicilar, cfg, log); err != nil {
		return err
	}

	// Sağlayıcı ve abonelik kayıtları modüller ayağa kalktıktan SONRA
	// uygulanır; route'lar ise modül route'larından sonra bağlanır.
	if err := eklentiler.Start(ctx, host); err != nil {
		return err
	}
	// Mevcut bir yolu gölgeleyen eklenti route'u AÇILIŞI DURDURUR. Hatayı
	// yutmak, modül ucunun sessizce eklenti tarafından ele geçirildiği ya da
	// eklentinin hiç bağlanmadığı bir kurulumla çalışmaya devam etmek olurdu;
	// ikisi de ancak ilk isteğin yanlış yere gitmesiyle fark edilirdi.
	if err := eklentiler.MountRoutes(router, host); err != nil {
		return err
	}

	// OpenAPI şeması router ağacından ÜRETİLİR, elle yazılmaz: elle yazılan
	// şema, ilk route değişikliğinde sessizce yalan söylemeye başlar.
	// Uç yalnızca route DESENLERİNİ yayımlar, veri değil.
	//
	// Modül listesi registry'den OKUNUR, burada ikinci bir liste tutulmaz:
	// eklentilerin getirdiği modüller (bkz. searchpg) yalnızca registry'de
	// görünür ve elle tutulan bir liste onları sessizce anlatmadan bırakırdı.
	doc := belgeyiAnlat(cfg.ServiceName+" API", version, registry.Modules())
	router.Get(openAPIPath, doc.Handler(router))
	semayiDenetle(ctx, doc, router, log)

	srv := corehttp.NewServer(corehttp.ServerOptions{
		Addr:              cfg.Addr(),
		Handler:           router,
		Logger:            log,
		ShutdownTimeout:   cfg.ShutdownTimeout,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	})

	return srv.Run(ctx)
}

// setupRedis gerekiyorsa Redis istemcisini açar, container'a kaydeder ve
// readiness kontrolüne ekler.
//
// Gerekmiyorsa (nil, nil) döner ve HİÇBİR bağlantı denenmez: Redis'e ihtiyacı
// olmayan bir kurulumda "bağlanamadım" uyarısı üretmek, gerçek bir arızayı
// gürültüde boğardı.
func setupRedis(
	ctx context.Context,
	c *container.Container,
	cfg config.Config,
	checks map[string]corehttp.HealthCheck,
	log *slog.Logger,
) (*redis.Client, error) {
	if !cfg.NeedsRedis() {
		return nil, nil //nolint:nilnil // "Redis gerekmiyor" bir hata değildir
	}

	opt, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, "redis_url_invalid",
			"REDIS_URL çözümlenemedi")
	}

	client := redis.NewClient(opt)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, errors.Wrap(err, errors.KindUnavailable, "redis_unreachable",
			"Redis'e bağlanılamadı (%s)", opt.Addr)
	}

	// Sıra önemli: container ters kayıt sırasında kapatır, yani veri yolu
	// istemciden ÖNCE kapanır.
	if err := c.Provide(svcRedis, client); err != nil {
		_ = client.Close()
		return nil, err
	}

	checks["redis"] = func(ctx context.Context) error { return client.Ping(ctx).Err() }
	log.InfoContext(ctx, "redis bağlandı",
		"addr", opt.Addr,
		"olay_veri_yolu", cfg.EventBus == config.BackendRedis,
		"koruma_arka_ucu", cfg.GuardBackend == config.BackendRedis,
	)

	return client, nil
}

// setupEventBus yapılandırmaya göre olay veri yolunu kurar.
//
// Redis istemcisi PARAMETREDİR, burada açılmaz: aynı istemciyi koruma arka ucu
// da kullanır ve iki yerde ayrı bağlantı açmak, kapanış sırasını ve sağlık
// kontrolünü ikiye bölerdi.
func setupEventBus(
	ctx context.Context,
	cfg config.Config,
	client *redis.Client,
	log *slog.Logger,
) (eventbus.EventBus, error) {
	if cfg.EventBus != config.BackendRedis {
		log.InfoContext(ctx, "olay veri yolu: bellek içi (tek süreç)")
		return eventbus.NewInMemory(log), nil
	}

	bus, err := eventbus.NewRedisStream(client, eventbus.RedisConfig{}, log)
	if err != nil {
		return nil, err
	}

	log.InfoContext(ctx, "olay veri yolu: Redis Streams")

	return bus, nil
}

// shutdownContainer container'daki servisleri kapatır ve hataları loglar.
func shutdownContainer(ctx context.Context, c *container.Container, cfg config.Config, log *slog.Logger) {
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
	defer cancel()

	if err := c.Shutdown(shutdownCtx); err != nil {
		log.Error("container servisleri kapatılamadı", "error", err)
	}
}
