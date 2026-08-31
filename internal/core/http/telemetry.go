package http

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

// tracerName bu paketin ürettiği span'ların enstrümantasyon adıdır.
const tracerName = "github.com/bdrtr/gobit/internal/core/http"

// unknownRoute eşleşen bir route deseni bulunamadığında kullanılan addır.
//
// Ham istek yolunu kullanmak CAZİPTİR ama yıkıcıdır: /store/v1/products/prod_01
// gibi her kimlik ayrı bir span adı ve ayrı bir metrik serisi üretir. Birkaç
// bin ürünle metrik deposu milyonlarca seriye çıkar ve sorgulanamaz hâle
// gelir. Tanımlanamayan yolu tek bir kovada toplamak, kardinaliteyi
// patlatmaktan iyidir.
const unknownRoute = "unknown"

// Telemetry her isteğe bir span açar ve süre/sayı metriklerini kaydeder.
//
// serviceName üretilen span'lara ve HTTP metriklerine service.name özniteliği
// olarak yazılır. Adı yalnızca OTel Resource'una bırakmak CAZİPTİR — servis
// kimliğinin doğal yeri orasıdır — ama bu middleware GLOBAL sağlayıcıyla
// çalışır ve [NewRouter] çağıranın observability.Setup'ı kurmuş olmasını şart
// koşmaz; sağlayıcıyı başka bir gömücü kurduysa Resource'ta servis adı hiç
// bulunmayabilir ve span'ın hangi servisten geldiği kaybolur. Parametre o
// hâlde yalnızca "takılsın mı" anahtarına dönüşürdü: operatör değeri
// değiştirir, izlemede hiçbir şey değişmezdi.
//
// Öznitelik kardinalite açısından güvenlidir: serviceName sürecin ömrü boyunca
// TEK bir değerdir, her seriye aynı değeri koyar ve seri sayısını çarpmaz.
// İstek başına değişen bir değer (kimlik, ham yol) buraya ASLA konmamalıdır.
//
// Boş ad öznitelik olarak yazılmaz: boş bir service.name, adı hiç
// raporlamamaktan kötüdür — panolarda gerçek bir servismiş gibi duran adsız
// bir seri açar.
//
// İzleme kurulmamışsa OTel'in global no-op sağlayıcıları devrededir ve
// middleware ölçülebilir bir maliyet getirmez; bu yüzden koşullu takmaya
// gerek yoktur.
//
// Middleware zincirinde [RequestID]'den SONRA, route eşleşmesinden ÖNCE
// çalışmalıdır: span adı olarak kullanılan route deseni ancak handler
// çalıştıktan sonra bilinir, bu yüzden ad next dönüşünde güncellenir.
func Telemetry(serviceName string) func(http.Handler) http.Handler {
	tracer := otel.Tracer(tracerName)
	meter := otel.Meter(tracerName)

	// Servis adını bir kez özniteliğe çevir: değer sürecin ömrü boyunca
	// sabittir, her istekte yeniden üretmek boşuna ayırma olurdu.
	var sabitOznitelikler []attribute.KeyValue
	if serviceName != "" {
		sabitOznitelikler = append(sabitOznitelikler, semconv.ServiceName(serviceName))
	}

	// Aktif istek sayacının artışı ile azalışı AYNI öznitelik kümesini
	// kullanmak zorundadır; ayrışırlarsa iki farklı seri oluşur, biri kalıcı
	// olarak +1'de diğeri -1'de takılır ve "kaç istek işleniyor" panosu hiçbir
	// zaman sıfıra dönmez. Seçeneği burada bir kez kurmak bunu yapısal olarak
	// garantiler.
	sabitOlcum := metric.WithAttributes(sabitOznitelikler...)

	// Metrik araçlarını bir kez kur. Hata yalnızca geçersiz araç adında olur;
	// o durumda araçlar nil kalır ve kayıt sessizce atlanır — izleme
	// kurulumundaki bir sorun istek yolunu düşürmemeli (ADR 0007).
	sure, err := meter.Float64Histogram("http.server.request.duration",
		metric.WithUnit("s"),
		metric.WithDescription("HTTP isteklerinin saniye cinsinden süresi"))
	if err != nil {
		slog.Default().Warn("istek süresi metriği kurulamadı", "error", err)
	}

	aktif, err := meter.Int64UpDownCounter("http.server.active_requests",
		metric.WithDescription("O an işlenmekte olan HTTP istekleri"))
	if err != nil {
		slog.Default().Warn("aktif istek metriği kurulamadı", "error", err)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// İstemciden gelen trace bağlamını sürdür; yoksa yenisi başlar.
			ctx := otel.GetTextMapPropagator().Extract(
				r.Context(), propagation.HeaderCarrier(r.Header))

			// Sabit öznitelikler span BAŞLARKEN verilir; sonradan
			// SetAttributes ile eklemek yetmezdi, çünkü örnekleyici (sampler)
			// kararını yalnızca başlangıç özniteliklerine bakarak verir ve
			// servis adına göre örnekleyen bir kurulum onu göremezdi.
			spanOznitelikleri := make([]attribute.KeyValue, 0, len(sabitOznitelikler)+3)
			spanOznitelikleri = append(spanOznitelikleri, sabitOznitelikler...)
			spanOznitelikleri = append(spanOznitelikleri,
				attribute.String("http.request.method", r.Method),
				attribute.String("url.path", r.URL.Path),
				attribute.String("server.address", r.Host),
			)

			ctx, span := tracer.Start(ctx, r.Method+" "+unknownRoute,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(spanOznitelikleri...))
			defer span.End()

			if id := RequestIDFromContext(ctx); id != "" {
				span.SetAttributes(attribute.String("gobit.request_id", id))
			}

			r = r.WithContext(ctx)
			sarmal := &responseWriter{ResponseWriter: w, status: http.StatusOK}

			basla := time.Now()

			if aktif != nil {
				aktif.Add(ctx, 1, sabitOlcum)
				defer aktif.Add(ctx, -1, sabitOlcum)
			}

			next.ServeHTTP(sarmal, r)

			desen := routePattern(r)
			span.SetName(r.Method + " " + desen)
			span.SetAttributes(
				attribute.String("http.route", desen),
				attribute.Int("http.response.status_code", sarmal.status),
			)

			// 5xx sunucunun kendi arızasıdır ve trace'te hata olarak görünmeli.
			// 4xx işaretlenmez: istemcinin gönderdiği geçersiz veri, sunucunun
			// hatası değildir ve hata oranı grafiklerini yanıltarak gerçek
			// arızaları gürültüde boğardı.
			if sarmal.status >= http.StatusInternalServerError {
				span.SetStatus(codes.Error, http.StatusText(sarmal.status))
			}

			if sure != nil {
				// Öznitelikler tek bir dilimde toplanır: metric.WithAttributes
				// kümeyi DEĞİŞTİRİR, eklemez — iki ayrı seçenek verilseydi
				// ikincisi sabit öznitelikleri sessizce silerdi.
				olcumOznitelikleri := make([]attribute.KeyValue, 0, len(sabitOznitelikler)+3)
				olcumOznitelikleri = append(olcumOznitelikleri, sabitOznitelikler...)
				olcumOznitelikleri = append(olcumOznitelikleri,
					attribute.String("http.request.method", r.Method),
					attribute.String("http.route", desen),
					attribute.Int("http.response.status_code", sarmal.status),
				)

				sure.Record(ctx, time.Since(basla).Seconds(),
					metric.WithAttributes(olcumOznitelikleri...))
			}
		})
	}
}

// routePattern eşleşen chi route desenini döner.
//
// Desen ancak router eşleşmeyi yaptıktan SONRA doludur; bu yüzden yalnızca
// handler çalıştıktan sonra çağrılmalıdır.
func routePattern(r *http.Request) string {
	rctx := chi.RouteContext(r.Context())
	if rctx == nil {
		return unknownRoute
	}

	if desen := rctx.RoutePattern(); desen != "" {
		return desen
	}

	return unknownRoute
}

// SpanFromContext context'teki aktif span'ı döner.
//
// İzleme kapalıyken de güvenle çağrılabilir: OTel no-op bir span döner.
func SpanFromContext(ctx context.Context) trace.Span {
	return trace.SpanFromContext(ctx)
}

// TraceIDFromContext aktif trace'in kimliğini döner; yoksa boş dize.
//
// Log kayıtlarına eklemek içindir: bir hata logunu trace'e bağlamanın en
// ucuz yolu budur.
func TraceIDFromContext(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return ""
	}

	return sc.TraceID().String()
}
