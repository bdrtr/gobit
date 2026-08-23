// Package http uygulamanın HTTP taşıma katmanını kurar.
//
// Paket adı net/http ile çakıştığı için çağıranlar corehttp alias'ıyla
// import eder. Router, middleware yığını, yanıt/hata yardımcıları ve
// graceful shutdown destekli sunucu buradadır.
package http

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// defaultReadinessTimeout readiness kontrollerinin toplam süre sınırıdır;
// tek bir kontrol takılırsa istek asılı kalmasın diye vardır.
const defaultReadinessTimeout = 5 * time.Second

// HealthCheck bir bağımlılığın erişilebilirliğini sınar.
// nil dönerse bağımlılık sağlıklı sayılır.
type HealthCheck func(ctx context.Context) error

// RouterOptions kök router'ın kurulumunu belirler.
type RouterOptions struct {
	// Version /health ve /ready yanıtlarında raporlanan derleme sürümüdür.
	Version string
	// Logger middleware yığınının kullanacağı logger'dır; nil ise slog.Default.
	Logger *slog.Logger
	// ReadinessChecks /ready uç noktasında çalıştırılacak bağımlılık
	// kontrolleridir (örn. "postgres", "redis"). Boşsa /ready her zaman 200 döner.
	ReadinessChecks map[string]HealthCheck
	// ReadinessTimeout tüm kontrollere tanınan toplam süredir; sıfırsa 5sn.
	ReadinessTimeout time.Duration
}

// healthResponse /health uç noktasının gövdesidir.
type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// checkResult tek bir bağımlılık kontrolünün sonucudur.
type checkResult struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// readyResponse /ready uç noktasının gövdesidir.
type readyResponse struct {
	Status  string                 `json:"status"`
	Version string                 `json:"version"`
	Checks  map[string]checkResult `json:"checks,omitempty"`
}

// NewRouter uygulamanın kök router'ını middleware yığınıyla birlikte kurar.
//
// Middleware sırası bilinçlidir: RequestID en başta çalışır ki logger ve
// recoverer isteği kimliğiyle raporlayabilsin; RequestLogger, Recoverer'ı
// sarar ki panik sonrası yazılan 500 de loglansın.
func NewRouter(opts RouterOptions) chi.Router {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}

	r := chi.NewRouter()
	r.Use(RequestID)
	r.Use(RequestLogger(log))
	r.Use(Recoverer(log))

	r.Get("/health", healthHandler(opts.Version))
	r.Get("/ready", readyHandler(opts, log))

	return r
}

// healthHandler sürecin ayakta olduğunu bildiren liveness uç noktasıdır.
// Bağımlılıkları SINAMAZ; orkestratör bunu süreç canlılığı için kullanır ve
// geçici bir veritabanı kesintisi bu sürecin öldürülmesine yol açmaz.
func healthHandler(version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(r.Context(), w, http.StatusOK, healthResponse{
			Status:  "ok",
			Version: version,
		})
	}
}

// readyHandler bağımlılıkları sınayan readiness uç noktasıdır.
// Herhangi bir kontrol başarısızsa 503 döner; orkestratör bu süreci trafikten
// çeker ama öldürmez.
func readyHandler(opts RouterOptions, log *slog.Logger) http.HandlerFunc {
	timeout := opts.ReadinessTimeout
	if timeout <= 0 {
		timeout = defaultReadinessTimeout
	}

	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		results := runChecks(ctx, opts.ReadinessChecks)

		status := http.StatusOK
		body := readyResponse{Status: "ok", Version: opts.Version, Checks: results}
		for name, res := range results {
			if res.Status != "ok" {
				status = http.StatusServiceUnavailable
				body.Status = "degraded"
				log.WarnContext(ctx, "readiness kontrolü başarısız", "kontrol", name, "error", res.Error)
			}
		}

		WriteJSON(ctx, w, status, body)
	}
}

// runChecks tüm kontrolleri eşzamanlı çalıştırır ve sonuçlarını toplar.
func runChecks(ctx context.Context, checks map[string]HealthCheck) map[string]checkResult {
	if len(checks) == 0 {
		return nil
	}

	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		results = make(map[string]checkResult, len(checks))
	)

	for name, check := range checks {
		wg.Add(1)
		go func() {
			defer wg.Done()

			res := checkResult{Status: "ok"}
			if err := check(ctx); err != nil {
				res = checkResult{Status: "error", Error: err.Error()}
			}

			mu.Lock()
			results[name] = res
			mu.Unlock()
		}()
	}

	wg.Wait()
	return results
}
