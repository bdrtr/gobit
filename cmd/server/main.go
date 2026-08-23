// Command server gobit commerce framework'ünün tek binary giriş noktasıdır.
//
// Akış: config yükle -> logger kur -> container kur -> altyapı servislerini
// (Postgres, Redis, event bus) kaydet -> modülleri bootstrap et -> HTTP
// router'ı mount et -> dinle. Modüller Faz 4'ten itibaren eklenecektir.
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
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/core/workflow"
	"github.com/bdrtr/gobit/internal/core/workflow/pgstore"
	"github.com/bdrtr/gobit/internal/modules/cart"
	"github.com/bdrtr/gobit/internal/modules/customer"
	"github.com/bdrtr/gobit/internal/modules/inventory"
	"github.com/bdrtr/gobit/internal/modules/pricing"
	"github.com/bdrtr/gobit/internal/modules/product"
	"github.com/bdrtr/gobit/internal/modules/region"
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
	)

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

	bus, err := setupEventBus(ctx, c, cfg, checks, log)
	if err != nil {
		return err
	}
	if err := c.Provide(svcEventBus, bus); err != nil {
		return err
	}

	router := corehttp.NewRouter(corehttp.RouterOptions{
		Version:         version,
		Logger:          log,
		ReadinessChecks: checks,
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
	if err := registry.Bootstrap(ctx, c, router); err != nil {
		return err
	}

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

// setupEventBus yapılandırmaya göre olay veri yolunu kurar ve gerekiyorsa
// Redis istemcisini container'a kaydedip readiness kontrolüne ekler.
func setupEventBus(
	ctx context.Context,
	c *container.Container,
	cfg config.Config,
	checks map[string]corehttp.HealthCheck,
	log *slog.Logger,
) (eventbus.EventBus, error) {
	if cfg.EventBus != "redis" {
		log.InfoContext(ctx, "olay veri yolu: bellek içi (tek süreç)")
		return eventbus.NewInMemory(log), nil
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

	bus, err := eventbus.NewRedisStream(client, eventbus.RedisConfig{}, log)
	if err != nil {
		return nil, err
	}

	checks["redis"] = func(ctx context.Context) error { return client.Ping(ctx).Err() }
	log.InfoContext(ctx, "olay veri yolu: Redis Streams", "addr", opt.Addr)
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
