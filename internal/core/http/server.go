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
	// ReadHeaderTimeout, yalnızca istek başlıklarının okunma süresidir.
	ReadHeaderTimeout time.Duration
	// ReadTimeout, başlık + gövdenin tamamının okunma süresidir. Bu sınır
	// olmadan gövdeyi bayt bayt akıtan bir istemci bağlantıyı süresiz tutar.
	ReadTimeout time.Duration
	// WriteTimeout, yanıtın yazılma süresidir.
	WriteTimeout time.Duration
	// IdleTimeout, keep-alive bağlantısının boşta bekleme süresidir.
	IdleTimeout time.Duration
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
			ReadTimeout:       opts.ReadTimeout,
			WriteTimeout:      opts.WriteTimeout,
			IdleTimeout:       opts.IdleTimeout,
		},
		log:             log,
		shutdownTimeout: opts.ShutdownTimeout,
	}
}

// Run sunucuyu başlatır ve ctx iptal edilene kadar bloklar.
//
// ctx iptal edildiğinde yeni bağlantı kabul edilmez ve açık istekler
// ShutdownTimeout süresince tamamlanmaya çalışılır. Süre dolarsa kalan
// bağlantılar Close ile zorla kapatılır ve hata döner.
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
		// Shutdown, deadline dolduğunda hata döner ama AKTİF BAĞLANTILARI
		// KAPATMAZ; zorla kapatmak için ayrıca Close gerekir. Bu olmadan
		// handler goroutine'leri ve TCP bağlantıları Run döndükten sonra
		// yaşamaya devam ederdi.
		s.log.Warn("düzgün kapanış süresi doldu, bağlantılar zorla kapatılıyor", "error", err)
		closeErr := s.httpSrv.Close()
		<-errCh // ListenAndServe'in dönmesini bekle, goroutine'i sızdırma
		return fmt.Errorf("http: düzgün kapanış tamamlanamadı: %w", errors.Join(err, closeErr))
	}

	s.log.Info("HTTP sunucusu kapandı")
	return <-errCh
}
