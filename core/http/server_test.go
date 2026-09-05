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

	corehttp "github.com/bdrtr/gobit/core/http"
)

// discardLogger swallows the logs so the test output stays clean.
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

	// Give the server a moment to start listening.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() = %v, expected nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run() did not return after the ctx was canceled")
	}
}

func TestServerReturnsErrorOnBusyPort(t *testing.T) {
	// Hold the port first; the server must not be able to bind the same address.
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
		t.Fatal("Run() should have returned an error on a taken port")
	}
}

func TestServerServesRequests(t *testing.T) {
	// Verify end to end that the server really answers requests.
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

	// Retry at short intervals until it starts listening.
	var resp *http.Response
	for range 50 {
		resp, err = http.Get("http://" + addr + "/health")
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("the /health request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, expected 200", resp.StatusCode)
	}

	cancel()
	if err := <-done; err != nil {
		t.Errorf("Run() = %v, expected nil", err)
	}
}

// TestServerForceClosesOnShutdownTimeout verifies that open connections are
// really cut when ShutdownTimeout expires.
//
// Regression: on its own, Shutdown returns an error when the deadline passes
// but DOES NOT close the active connections. Unless Close is called the
// handler and the TCP connection live on after Run returns, and the client
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
		time.Sleep(3 * time.Second) // far longer than the ShutdownTimeout
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
		t.Fatal("the handler was never entered")
	}

	cancel() // trigger the shutdown

	select {
	case err := <-runDone:
		if err == nil {
			t.Fatal("Run() returned nil; an error was expected on the timeout")
		}
		if !strings.Contains(err.Error(), "the graceful shutdown could not finish") {
			t.Errorf("unexpected error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run() did not return after the timeout")
	}

	// The real claim: because the connection was cut the client MUST NOT get a
	// response. Without Close the client would have got a clean 200 three
	select {
	case err := <-clientDone:
		if err == nil {
			t.Error("the client got a clean response; the connection was not force-closed")
		}
	case <-time.After(2 * time.Second):
		t.Error("the client request neither failed nor completed; the connection was not cut")
	}
}
