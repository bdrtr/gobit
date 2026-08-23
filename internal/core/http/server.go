package http

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// ServerOptions Server'ın davranışını belirler.
type ServerOptions struct {
	// Addr, dinlenecek TCP adresidir (örn. ":9000").
	Addr string
	// Handler, isteklerin yönlendirileceği kök handler'dır.
	Handler http.Handler
	// Logger, sunucu yaşam döngüsü olaylarının yazılacağı logger'dır.
	Logger *slog.Logger
	// ShutdownTimeout, kapanışta açık isteklerin tamamlanması için tanınan süredir.
	ShutdownTimeout time.Duration
	// ReadHeaderTimeout, Slowloris tipi saldırılara karşı zorunlu sınırdır.
	ReadHeaderTimeout time.Duration
}

// Server graceful shutdown destekli HTTP sunucusudur.
type Server struct {
	httpSrv         *http.Server
	log             *slog.Logger
	shutdownTimeout time.Duration
}

// NewServer verilen ayarlarla bir Server kurar.
func NewServer(opts ServerOptions) *Server {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}

	return &Server{
		httpSrv: &http.Server{
			Addr:              opts.Addr,
			Handler:           opts.Handler,
			ReadHeaderTimeout: opts.ReadHeaderTimeout,
		},
		log:             log,
		shutdownTimeout: opts.ShutdownTimeout,
	}
}

// Run sunucuyu başlatır ve ctx iptal edilene kadar bloklar.
//
// ctx iptal edildiğinde yeni bağlantı kabul edilmez ve açık istekler
// ShutdownTimeout süresince tamamlanmaya çalışılır. Süre dolarsa kalan
// bağlantılar zorla kapatılır ve hata döner.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)

	go func() {
		s.log.Info("HTTP sunucusu dinlemede", "addr", s.httpSrv.Addr)
		if err := s.httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http: sunucu başlatılamadı: %w", err)
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		s.log.Info("kapanış sinyali alındı, açık istekler bekleniyor", "timeout", s.shutdownTimeout)
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.shutdownTimeout)
	defer cancel()

	if err := s.httpSrv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http: düzgün kapanış tamamlanamadı: %w", err)
	}

	s.log.Info("HTTP sunucusu kapandı")
	return <-errCh
}
