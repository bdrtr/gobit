// Package logger provides the slog-based structured logging setup used across
// the application. It deliberately does not know the config package; the
// settings are handed in by the caller through Options.
package logger

import (
	"io"
	"log/slog"
	"os"
)

// Options decides the behavior of the logger New produces.
type Options struct {
	// Level is the lowest log level; records below it are dropped.
	Level slog.Level
	// Format is the output shape: "text" gives text, every other value gives
	// JSON.
	Format string
	// Output is where the logs are written. Left empty, os.Stdout is used.
	Output io.Writer
	// AddSource true adds the calling file and line to every record. Because
	// it is costly in production it is usually only turned on in development.
	AddSource bool
	// Middleware wraps the handler before the logger is built. Nil leaves the
	// handler as it is.
	//
	// It exists so that something can observe the log without this package
	// knowing what that something is. Error reporting is the caller today
	// (core/errorreport); had this package imported it, the logger —
	// which everything depends on — would have gained a dependency on a
	// collector integration, and nothing in this file would be testable without
	// it.
	Middleware func(slog.Handler) slog.Handler
}

// New produces a *slog.Logger configured with the given options.
func New(opts Options) *slog.Logger {
	out := opts.Output
	if out == nil {
		out = os.Stdout
	}

	handlerOpts := &slog.HandlerOptions{
		Level:     opts.Level,
		AddSource: opts.AddSource,
	}

	var h slog.Handler
	if opts.Format == "text" {
		h = slog.NewTextHandler(out, handlerOpts)
	} else {
		h = slog.NewJSONHandler(out, handlerOpts)
	}

	if opts.Middleware != nil {
		h = opts.Middleware(h)
	}

	return slog.New(h)
}
