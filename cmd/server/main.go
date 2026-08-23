// Command server, gobit commerce framework'ünün tek binary giriş noktasıdır.
//
// Görevi: config yükle -> logger kur -> container kur -> modülleri register et
// -> HTTP router'ı mount et -> dinle. Faz 0'da container ve modül kaydı henüz
// yoktur; bunlar Faz 1'de eklenecektir.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/bdrtr/gobit/internal/core/config"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/core/logger"
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
	)

	// Faz 1: container.New() + ModuleRegistry.Bootstrap(ctx, c) burada çalışacak,
	// modüllerin route'ları router'a mount edilecek.
	router := corehttp.NewRouter(version)

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
