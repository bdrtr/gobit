package http_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// izleyiciKur span'ları belleğe toplayan bir tracer sağlayıcısı kurar ve
// global sağlayıcıyı test süresince onunla değiştirir.
//
// Global durumu değiştirdiği için bu testler PARALEL DEĞİLDİR.
func izleyiciKur(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()

	kayit := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(kayit))

	onceki := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)

	t.Cleanup(func() {
		otel.SetTracerProvider(onceki)
		_ = tp.Shutdown(t.Context())
	})

	return kayit
}

// olcerKur metrikleri belleğe toplayan bir meter sağlayıcısı kurar ve global
// sağlayıcıyı test süresince onunla değiştirir.
//
// Router'dan ÖNCE çağrılmalıdır: [corehttp.Telemetry] metrik araçlarını
// middleware kurulurken bir kez oluşturur ve sonradan takılan bir sağlayıcıyı
// görmez.
//
// Global durumu değiştirdiği için bu testler PARALEL DEĞİLDİR.
func olcerKur(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()

	okuyucu := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(okuyucu))

	onceki := otel.GetMeterProvider()
	otel.SetMeterProvider(mp)

	t.Cleanup(func() {
		otel.SetMeterProvider(onceki)
		// t.Context() temizlik sırasında zaten iptal edilmiştir; Shutdown'ın
		// dışa aktarmayı tamamlayabilmesi için canlı bir bağlam gerekir.
		_ = mp.Shutdown(context.Background())
	})

	return okuyucu
}

// oznitelik span'dan bir özniteliği okur.
func oznitelik(t *testing.T, s sdktrace.ReadOnlySpan, anahtar string) attribute.Value {
	t.Helper()

	for _, kv := range s.Attributes() {
		if string(kv.Key) == anahtar {
			return kv.Value
		}
	}

	t.Fatalf("%q özniteliği bulunamadı", anahtar)

	return attribute.Value{}
}

// oznitelikVar span'da bir özniteliğin bulunup bulunmadığını söyler.
//
// [oznitelik] yokluğu testi düşürdüğü için "olmamalı" iddialarında
// kullanılamaz; bu yardımcı o boşluğu kapatır.
func oznitelikVar(s sdktrace.ReadOnlySpan, anahtar string) bool {
	for _, kv := range s.Attributes() {
		if string(kv.Key) == anahtar {
			return true
		}
	}

	return false
}

// metrikVerisi toplanan metrikler içinden verilen adlı metriğin verisini
// döner; metrik yoksa testi düşürür.
func metrikVerisi(t *testing.T, rm *metricdata.ResourceMetrics, ad string) metricdata.Aggregation {
	t.Helper()

	for _, kapsam := range rm.ScopeMetrics {
		for _, m := range kapsam.Metrics {
			if m.Name == ad {
				return m.Data
			}
		}
	}

	t.Fatalf("%q metriği bulunamadı", ad)

	return nil
}

// histogramNoktalari verilen adlı histogram metriğinin veri noktalarını döner.
func histogramNoktalari(
	t *testing.T, rm *metricdata.ResourceMetrics, ad string,
) []metricdata.HistogramDataPoint[float64] {
	t.Helper()

	h, ok := metrikVerisi(t, rm, ad).(metricdata.Histogram[float64])
	require.True(t, ok, "%q bir histogram olmalı", ad)

	return h.DataPoints
}

// toplamNoktalari verilen adlı sayaç metriğinin veri noktalarını döner.
func toplamNoktalari(
	t *testing.T, rm *metricdata.ResourceMetrics, ad string,
) []metricdata.DataPoint[int64] {
	t.Helper()

	s, ok := metrikVerisi(t, rm, ad).(metricdata.Sum[int64])
	require.True(t, ok, "%q bir sayaç olmalı", ad)

	return s.DataPoints
}

// routerKur telemetri middleware'i takılı bir chi router'ı kurar.
func routerKur(t *testing.T) chi.Router {
	t.Helper()

	return routerKurAdli(t, "gobit-test")
}

// routerKurAdli verilen servis adıyla telemetri middleware'i takılı bir chi
// router'ı kurar.
func routerKurAdli(t *testing.T, servisAdi string) chi.Router {
	t.Helper()

	r := chi.NewRouter()
	r.Use(corehttp.Telemetry(servisAdi))
	r.Get("/store/v1/products/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	r.Get("/store/v1/patlat", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	r.Get("/store/v1/gecersiz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
	})

	return r
}

// TestTelemetrySpanAdiKimlikIcermez span adının ham yolu DEĞİL route desenini
// kullandığını doğrular.
//
// Bu testin koruduğu şey kardinalite patlamasıdır: ham yol kullanılsaydı her
// ürün kimliği ayrı bir span adı ve ayrı bir metrik serisi üretir, birkaç bin
// ürünle metrik deposu sorgulanamaz hâle gelirdi.
func TestTelemetrySpanAdiKimlikIcermez(t *testing.T) {
	kayit := izleyiciKur(t)
	r := routerKur(t)

	for _, id := range []string{"prod_01", "prod_02", "prod_03"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
			"/store/v1/products/"+id, http.NoBody))
		require.Equal(t, http.StatusOK, w.Code)
	}

	spanlar := kayit.Ended()
	require.Len(t, spanlar, 3)

	adlar := map[string]int{}
	for _, s := range spanlar {
		adlar[s.Name()]++
		assert.NotContains(t, s.Name(), "prod_", "span adı kimlik içermemeli")
	}

	assert.Equal(t, map[string]int{"GET /store/v1/products/{id}": 3}, adlar,
		"üç farklı kimlik TEK bir span adına düşmeli")
}

// TestTelemetryHamYolOznitelikteKalir kimliğin span adından çıkarılmasının
// bilgiyi KAYBETTİRMEDİĞİNİ doğrular.
//
// Ham yol bir öznitelik olarak durur: öznitelikler kardinaliteyi metrik
// serilerine taşımaz, tekil bir span'ı incelerken ise hangi kaydın istendiği
// hâlâ görülebilir.
func TestTelemetryHamYolOznitelikteKalir(t *testing.T) {
	kayit := izleyiciKur(t)
	r := routerKur(t)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
		"/store/v1/products/prod_42", http.NoBody))

	spanlar := kayit.Ended()
	require.Len(t, spanlar, 1)

	assert.Equal(t, "/store/v1/products/prod_42",
		oznitelik(t, spanlar[0], "url.path").AsString())
	assert.Equal(t, "/store/v1/products/{id}",
		oznitelik(t, spanlar[0], "http.route").AsString())
}

// TestTelemetrySunucuHatasiSpaniIsaretler 5xx'in span'da hata olarak
// göründüğünü doğrular.
func TestTelemetrySunucuHatasiSpaniIsaretler(t *testing.T) {
	kayit := izleyiciKur(t)
	r := routerKur(t)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/store/v1/patlat", http.NoBody))
	require.Equal(t, http.StatusInternalServerError, w.Code)

	spanlar := kayit.Ended()
	require.Len(t, spanlar, 1)
	assert.Equal(t, codes.Error, spanlar[0].Status().Code)
	assert.EqualValues(t, http.StatusInternalServerError,
		oznitelik(t, spanlar[0], "http.response.status_code").AsInt64())
}

// TestTelemetryIstemciHatasiSpaniIsaretlemez 4xx'in hata SAYILMADIĞINI
// doğrular.
//
// İstemcinin gönderdiği geçersiz veri sunucunun hatası değildir; hata olarak
// işaretlemek hata oranı grafiklerini yanıltır ve gerçek arızaları gürültüde
// boğardı.
func TestTelemetryIstemciHatasiSpaniIsaretlemez(t *testing.T) {
	kayit := izleyiciKur(t)
	r := routerKur(t)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/store/v1/gecersiz", http.NoBody))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)

	spanlar := kayit.Ended()
	require.Len(t, spanlar, 1)
	assert.Equal(t, codes.Unset, spanlar[0].Status().Code,
		"4xx sunucu hatası olarak işaretlenmemeli")
}

// TestTelemetryEslesmeyenYolTekKovadaToplanir 404'lerin kardinaliteyi
// patlatmadığını doğrular.
//
// En kritik durum budur: bir tarayıcı ya da bot rastgele yollar denediğinde
// her biri ayrı span adı üretseydi, metrik deposunu doldurmak için tek bir
// saldırgan yeterdi.
func TestTelemetryEslesmeyenYolTekKovadaToplanir(t *testing.T) {
	kayit := izleyiciKur(t)
	r := routerKur(t)

	for _, yol := range []string{"/rastgele/1", "/rastgele/2", "/bambaska"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, yol, http.NoBody))
		require.Equal(t, http.StatusNotFound, w.Code)
	}

	adlar := map[string]int{}
	for _, s := range kayit.Ended() {
		adlar[s.Name()]++
	}

	assert.Len(t, adlar, 1, "eşleşmeyen yollar tek bir span adında toplanmalı")
}

// TestTelemetryGelenTraceBaglamiSurdurulur W3C traceparent başlığının
// izlendiğini doğrular.
//
// Sürdürülmezse her servis kendi kopuk trace'ini üretir ve dağıtık izleme
// hiçbir isteği uçtan uca gösteremez.
func TestTelemetryGelenTraceBaglamiSurdurulur(t *testing.T) {
	kayit := izleyiciKur(t)

	onceki := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagator())

	t.Cleanup(func() { otel.SetTextMapPropagator(onceki) })

	r := routerKur(t)

	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"

	req := httptest.NewRequest(http.MethodGet, "/store/v1/products/p1", http.NoBody)
	req.Header.Set("traceparent", "00-"+traceID+"-00f067aa0ba902b7-01")

	r.ServeHTTP(httptest.NewRecorder(), req)

	spanlar := kayit.Ended()
	require.Len(t, spanlar, 1)
	assert.Equal(t, traceID, spanlar[0].SpanContext().TraceID().String(),
		"gelen trace kimliği sürdürülmeli")
	assert.True(t, spanlar[0].Parent().IsValid(), "span'ın ebeveyni olmalı")
}

// TestTelemetryServisAdiSpanaYazilir [corehttp.Telemetry]'ye verilen servis
// adının span özniteliklerine düştüğünü doğrular.
//
// Parametre bir zamanlar gövdede HİÇ kullanılmıyordu; belge ise adın izlemede
// raporlandığını söylüyordu. Sessiz tuzak buydu: operatör
// RouterOptions.TelemetryService'i değiştirir, izlemede hiçbir şey değişmezdi.
// Bu test o bağı canlı tutar.
func TestTelemetryServisAdiSpanaYazilir(t *testing.T) {
	kayit := izleyiciKur(t)
	r := routerKurAdli(t, "gobit-magaza")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
		"/store/v1/products/prod_1", http.NoBody))
	require.Equal(t, http.StatusOK, w.Code)

	spanlar := kayit.Ended()
	require.Len(t, spanlar, 1)
	assert.Equal(t, "gobit-magaza",
		oznitelik(t, spanlar[0], string(semconv.ServiceNameKey)).AsString())
}

// TestTelemetryBosServisAdiOznitelikYazmaz boş adın span'a boş bir
// service.name yazmadığını doğrular.
//
// Boş bir öznitelik, adı hiç raporlamamaktan kötüdür: panolarda gerçek bir
// servismiş gibi duran adsız bir seri açar ve iki farklı kurulumun ölçümleri
// aynı kovada birikir.
func TestTelemetryBosServisAdiOznitelikYazmaz(t *testing.T) {
	kayit := izleyiciKur(t)
	r := routerKurAdli(t, "")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
		"/store/v1/products/prod_1", http.NoBody))
	require.Equal(t, http.StatusOK, w.Code)

	spanlar := kayit.Ended()
	require.Len(t, spanlar, 1)
	assert.False(t, oznitelikVar(spanlar[0], string(semconv.ServiceNameKey)),
		"boş servis adı öznitelik olarak yazılmamalı")
	assert.Equal(t, "/store/v1/products/{id}",
		oznitelik(t, spanlar[0], "http.route").AsString(),
		"servis adı olmasa da diğer öznitelikler yazılmalı")
}

// TestTelemetryServisAdiMetrigeYazilirVeSeriCogaltmaz servis adının süre
// metriğinin özniteliklerinde yer aldığını, ama seri sayısını
// ÇOĞALTMADIĞINI doğrular.
//
// Kardinalite güvenliğinin kanıtı budur: ad süreç ömrü boyunca sabit olduğu
// için üç istek hâlâ TEK bir seriye düşer. İstek başına değişen bir değer
// eklenseydi bu iddia anında kırılırdı.
func TestTelemetryServisAdiMetrigeYazilirVeSeriCogaltmaz(t *testing.T) {
	okuyucu := olcerKur(t)
	r := routerKurAdli(t, "gobit-magaza")

	for _, id := range []string{"prod_01", "prod_02", "prod_03"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
			"/store/v1/products/"+id, http.NoBody))
		require.Equal(t, http.StatusOK, w.Code)
	}

	var rm metricdata.ResourceMetrics
	require.NoError(t, okuyucu.Collect(t.Context(), &rm))

	noktalar := histogramNoktalari(t, &rm, "http.server.request.duration")
	require.Len(t, noktalar, 1, "sabit servis adı seri sayısını çoğaltmamalı")
	assert.EqualValues(t, 3, noktalar[0].Count)

	deger, ok := noktalar[0].Attributes.Value(semconv.ServiceNameKey)
	require.True(t, ok, "süre metriğinde service.name bulunmalı")
	assert.Equal(t, "gobit-magaza", deger.AsString())
}

// TestTelemetryAktifIstekSayaciSifiraDoner sayacın istekler bittiğinde sıfıra
// döndüğünü doğrular.
//
// Servis adı özniteliği eklendikten sonra gerçek bir tuzak doğdu: artış ile
// azalış farklı öznitelik kümeleri kullansaydı iki ayrı seri oluşur, biri
// kalıcı olarak +3'te diğeri -3'te takılırdı ve "kaç istek işleniyor" panosu
// hiçbir zaman doğruya dönmezdi.
func TestTelemetryAktifIstekSayaciSifiraDoner(t *testing.T) {
	okuyucu := olcerKur(t)
	r := routerKurAdli(t, "gobit-magaza")

	for range 3 {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
			"/store/v1/products/prod_1", http.NoBody))
		require.Equal(t, http.StatusOK, w.Code)
	}

	var rm metricdata.ResourceMetrics
	require.NoError(t, okuyucu.Collect(t.Context(), &rm))

	noktalar := toplamNoktalari(t, &rm, "http.server.active_requests")
	require.Len(t, noktalar, 1, "artış ve azalış TEK bir seride toplanmalı")
	assert.EqualValues(t, 0, noktalar[0].Value, "biten istek sayacı sıfıra dönmeli")

	deger, ok := noktalar[0].Attributes.Value(semconv.ServiceNameKey)
	require.True(t, ok, "aktif istek sayacında service.name bulunmalı")
	assert.Equal(t, "gobit-magaza", deger.AsString())
}

// propagator testlerde kullanılan W3C TraceContext yayıcısıdır.
func propagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{})
}
