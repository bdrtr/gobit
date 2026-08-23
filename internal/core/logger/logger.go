// Package logger uygulama genelinde kullanılan slog tabanlı yapısal log
// kurulumunu sağlar. Bilinçli olarak config paketini tanımaz; ayarlar
// çağıran tarafından Options ile verilir.
package logger

import (
	"io"
	"log/slog"
	"os"
)

// Options New'in üreteceği logger'ın davranışını belirler.
type Options struct {
	// Level, altındaki kayıtların atılacağı en düşük log seviyesidir.
	Level slog.Level
	// Format çıktı biçimidir: "text" ise metin, diğer tüm değerlerde JSON.
	Format string
	// Output, logların yazılacağı hedeftir. Boş bırakılırsa os.Stdout kullanılır.
	Output io.Writer
	// AddSource true ise her kayda çağıran dosya/satır bilgisi eklenir.
	// Üretimde maliyetli olduğu için genelde yalnızca geliştirmede açılır.
	AddSource bool
}

// New verilen ayarlarla yapılandırılmış bir *slog.Logger üretir.
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

	return slog.New(h)
}
