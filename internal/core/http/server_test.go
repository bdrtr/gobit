package http_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// discardLogger test çıktısını kirletmemek için logları yutar.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestServerShutsDownOnContextCancel(t *testing.T) {
	srv := corehttp.NewServer(corehttp.ServerOptions{
		Addr:              "127.0.0.1:0",
		Handler:           corehttp.NewRouter("test"),
		Logger:            discardLogger(),
		ShutdownTimeout:   5 * time.Second,
		ReadHeaderTimeout: time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()

	// Sunucunun dinlemeye başlamasına fırsat ver.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() = %v, beklenen nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run() ctx iptalinden sonra dönmedi")
	}
}

func TestServerReturnsErrorOnBusyPort(t *testing.T) {
	// Portu önce biz tutalım; sunucu aynı adrese bağlanamamalı.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	srv := corehttp.NewServer(corehttp.ServerOptions{
		Addr:              ln.Addr().String(),
		Handler:           corehttp.NewRouter("test"),
		Logger:            discardLogger(),
		ShutdownTimeout:   time.Second,
		ReadHeaderTimeout: time.Second,
	})

	if err := srv.Run(context.Background()); err == nil {
		t.Fatal("Run() dolu portta hata dönmeliydi")
	}
}

func TestServerServesRequests(t *testing.T) {
	// Sunucunun gerçekten istek karşıladığını uçtan uca doğrula.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("ln.Close: %v", err)
	}

	srv := corehttp.NewServer(corehttp.ServerOptions{
		Addr:              addr,
		Handler:           corehttp.NewRouter("e2e"),
		Logger:            discardLogger(),
		ShutdownTimeout:   5 * time.Second,
		ReadHeaderTimeout: time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()

	// Dinlemeye başlayana kadar kısa aralıklarla dene.
	var resp *http.Response
	for range 50 {
		resp, err = http.Get("http://" + addr + "/health")
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("/health isteği başarısız: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, beklenen 200", resp.StatusCode)
	}

	cancel()
	if err := <-done; err != nil {
		t.Errorf("Run() = %v, beklenen nil", err)
	}
}
