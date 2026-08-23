// Package http uygulamanın HTTP taşıma katmanını kurar.
//
// Faz 0 kapsamında yalnızca router iskeleti ve /health uç noktası vardır.
// Middleware yığını (request-id, recover, logging), hata -> status kodu
// eşlemesi ve auth middleware'i Faz 1'de bu pakete eklenecektir.
package http

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// healthResponse /health uç noktasının gövdesidir.
type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// NewRouter uygulamanın kök router'ını kurar.
// version, /health yanıtında raporlanan derleme sürümüdür.
func NewRouter(version string) chi.Router {
	r := chi.NewRouter()
	r.Get("/health", healthHandler(version))
	return r
}

// healthHandler sürecin ayakta olduğunu bildiren liveness uç noktasıdır.
// Bağımlılık (Postgres/Redis) sağlığını kontrol eden readiness uç noktası
// Faz 1'de core/db hazır olduğunda eklenecektir.
func healthHandler(version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(r.Context(), w, http.StatusOK, healthResponse{
			Status:  "ok",
			Version: version,
		})
	}
}

// writeJSON verilen değeri JSON olarak yanıta yazar.
// Gövde kodlanamazsa status kodu çoktan gönderilmiş olacağından hata
// yalnızca loglanır.
func writeJSON(ctx context.Context, w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.ErrorContext(ctx, "yanıt gövdesi yazılamadı", "error", err)
	}
}
