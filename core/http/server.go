package http

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// ServerOptions decides the Server's behavior.
type ServerOptions struct {
	// Addr is the TCP address to listen on (e.g. ":9000").
	Addr string
	// Handler is the root handler the requests are routed to.
	Handler http.Handler
	// Logger is where the server lifecycle events are written.
	Logger *slog.Logger
	// ShutdownTimeout is the time allowed at shutdown for the open requests to
	// finish.
	ShutdownTimeout time.Duration
	// ReadHeaderTimeout is the time allowed for reading the request headers
	// alone.
	ReadHeaderTimeout time.Duration
	// ReadTimeout is the time allowed for reading the headers and the whole
	// body. Without this bound a client streaming the body byte by byte holds
	// the connection forever.
	ReadTimeout time.Duration
	// WriteTimeout is the time allowed for writing the response.
	WriteTimeout time.Duration
	// IdleTimeout is how long a keep-alive connection may sit idle.
	IdleTimeout time.Duration
}

// Server is the HTTP server with graceful shutdown support.
type Server struct {
	httpSrv         *http.Server
	log             *slog.Logger
	shutdownTimeout time.Duration
}

// NewServer builds a Server with the given options.
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

// Run starts the server and blocks until ctx is canceled.
//
// Once ctx is canceled no new connection is accepted and the open requests are
// given ShutdownTimeout to finish. When that runs out the remaining connections
// are forced closed with Close and an error is returned.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)

	go func() {
		s.log.Info("the HTTP server is listening", "addr", s.httpSrv.Addr)
		if err := s.httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http: the server could not be started: %w", err)
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		s.log.Info("the shutdown signal arrived, waiting for the open requests", "timeout", s.shutdownTimeout)
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.shutdownTimeout)
	defer cancel()

	if err := s.httpSrv.Shutdown(shutdownCtx); err != nil {
		// When the deadline runs out Shutdown returns an error but does NOT
		// CLOSE THE ACTIVE CONNECTIONS; forcing them shut needs a separate
		// Close. Without it the handler goroutines and the TCP connections
		// would keep living after Run had returned.
		s.log.Warn("the graceful shutdown budget ran out, forcing the connections closed", "error", err)
		closeErr := s.httpSrv.Close()
		<-errCh // wait for ListenAndServe to return; do not leak the goroutine
		return fmt.Errorf("http: the graceful shutdown could not finish: %w", errors.Join(err, closeErr))
	}

	s.log.Info("the HTTP server is closed")
	return <-errCh
}
