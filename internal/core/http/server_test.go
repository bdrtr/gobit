package http_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
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
		Handler:           corehttp.NewRouter(corehttp.RouterOptions{Version: "test"}),
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
		Handler:           corehttp.NewRouter(corehttp.RouterOptions{Version: "test"}),
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
		Handler:           corehttp.NewRouter(corehttp.RouterOptions{Version: "e2e"}),
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

// TestServerForceClosesOnShutdownTimeout, ShutdownTimeout dolduğunda açık
// bağlantıların gerçekten koparıldığını doğrular.
//
// Regresyon: Shutdown tek başına deadline dolduğunda hata döner ama aktif
// bağlantıları KAPATMAZ. Close çağrılmadığı sürece handler ve TCP bağlantısı
// Run döndükten sonra yaşamaya devam eder ve istemci yanıtını hatasız alırdı.
func TestServerForceClosesOnShutdownTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("ln.Close: %v", err)
	}

	handlerEntered := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
		close(handlerEntered)
		time.Sleep(3 * time.Second) // ShutdownTimeout'tan çok daha uzun
		_, _ = w.Write([]byte("slow-done"))
	})

	srv := corehttp.NewServer(corehttp.ServerOptions{
		Addr:              addr,
		Handler:           mux,
		Logger:            discardLogger(),
		ShutdownTimeout:   300 * time.Millisecond,
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       10 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- srv.Run(ctx) }()

	clientDone := make(chan error, 1)
	go func() {
		var resp *http.Response
		var reqErr error
		for range 100 {
			resp, reqErr = http.Get("http://" + addr + "/slow")
			if reqErr == nil || !strings.Contains(reqErr.Error(), "connection refused") {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		clientDone <- reqErr
	}()

	select {
	case <-handlerEntered:
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("handler'a hiç girilmedi")
	}

	cancel() // kapanışı tetikle

	select {
	case err := <-runDone:
		if err == nil {
			t.Fatal("Run() nil döndü; zaman aşımında hata beklenirdi")
		}
		if !strings.Contains(err.Error(), "düzgün kapanış tamamlanamadı") {
			t.Errorf("beklenmedik hata: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run() zaman aşımı sonrası dönmedi")
	}

	// Asıl iddia: bağlantı koparıldığı için istemci yanıtı ALAMAMALI.
	// Close çağrılmasaydı istemci 3 sn sonra hatasız 200 alırdı.
	select {
	case err := <-clientDone:
		if err == nil {
			t.Error("istemci yanıtı hatasız aldı; bağlantı zorla kapatılmamış")
		}
	case <-time.After(2 * time.Second):
		t.Error("istemci isteği ne hata verdi ne tamamlandı; bağlantı koparılmamış")
	}
}
