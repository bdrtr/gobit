// Package observability OpenTelemetry trace ve metrik altyapısını kurar.
//
// # Kapalıysa gerçekten kapalıdır
//
// Toplayıcı adresi verilmemişse hiçbir dış bağlantı denenmez ve tüm izleme
// çağrıları OTel'in kendi no-op uygulamalarına düşer. Bu, geliştirme
// ortamında sürekli "connection refused" gürültüsü üretmemek için bilinçli
// bir tercihtir; toplayıcı adresi vermek AÇIK bir karardır.
//
// # Kurulum başarısızlığı uygulamayı düşürmez
//
// [Setup] toplayıcıya bağlanamazsa uygulama yine de açılır ve izleme kapalı
// kalır. Gerekçe ADR 0007'deki ile aynıdır: izleme, ürünün DOĞRULUĞU için
// değil görünürlüğü için vardır. Toplayıcının kesintisi mağazayı kapatmamalı.
// gRPC dışa aktarıcısı zaten tembel bağlanır, yani asıl arıza modu da
// açılışta değil çalışma anındadır ve orada da sessizce yeniden denenir.
//
// # Örnekleme kararı istemciye BIRAKILMAZ
//
// Gelen traceparent başlığı okunur ama "sampled" bayrağı örnekleme oranını
// ezemez; ayrıntı ve gerekçe için bkz. [ornekleyici].
package observability

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// shutdownTimeout kapanışta HER dışa aktarıcıya AYRI AYRI tanınan süredir.
//
// Kapanışta bekleyen span'ları göndermek değerlidir ama süresiz beklemek,
// toplayıcı erişilemezken SIGTERM'i asmak demektir.
//
// Sürenin sağlayıcı BAŞINA olması bilinçlidir; gerekçesi [kapanisiYurut]'ta.
const shutdownTimeout = 5 * time.Second

// Options izleme kurulumunun girdileridir.
type Options struct {
	// Endpoint OTLP toplayıcısının gRPC adresidir. Boşsa izleme KAPALIDIR.
	Endpoint string
	// Insecure TLS'siz bağlanılacağını bildirir.
	Insecure bool
	// ServiceName trace ve metriklerde raporlanan servis adıdır.
	ServiceName string
	// ServiceVersion derleme sürümüdür.
	ServiceVersion string
	// Environment çalışma ortamıdır (development | staging | production).
	Environment string
	// SampleRatio örneklenecek trace oranıdır (0.0 - 1.0).
	SampleRatio float64
	// MetricInterval metriklerin gönderilme sıklığıdır; sıfırsa 60sn.
	MetricInterval time.Duration
	// Logger kurulum olaylarını yazar; nil ise slog.Default.
	Logger *slog.Logger
}

// ShutdownFunc izleme altyapısını kapatır.
type ShutdownFunc func(ctx context.Context) error

// Setup global tracer ve meter sağlayıcılarını kurar.
//
// Dönen ShutdownFunc her zaman çağrılabilir (nil DÖNMEZ): izleme kapalıyken
// bile çağıranın koşullu bir kapanış yolu yazması gerekmesin diye. Koşullu
// kapanış, "kapalıyken nil dönüyor" ayrıntısını unutan bir çağıranda nil
// pointer paniğine dönüşürdü.
//
// Hata YALNIZCA yapılandırma bozukluğunda döner; ağ erişilemezliği hata
// değildir çünkü gRPC dışa aktarıcısı tembel bağlanır.
func Setup(ctx context.Context, opts Options) (ShutdownFunc, error) {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}

	if opts.Endpoint == "" {
		log.InfoContext(ctx, "OTLP adresi verilmedi, izleme kapalı")

		return noopShutdown, nil
	}

	res, err := kaynak(ctx, opts)
	if err != nil {
		return noopShutdown, err
	}

	tracerSağlayıcı, err := izSaglayici(ctx, opts, res)
	if err != nil {
		return noopShutdown, err
	}

	meterSağlayıcı, err := metrikSaglayici(ctx, opts, res)
	if err != nil {
		// İz sağlayıcısı kuruldu ama metrik kurulamadı: yarım kurulumu
		// bırakmak, kapanışta göndermeye çalışan yetim bir goroutine demektir.
		_ = tracerSağlayıcı.Shutdown(ctx)

		return noopShutdown, err
	}

	otel.SetTracerProvider(tracerSağlayıcı)
	otel.SetMeterProvider(meterSağlayıcı)

	// W3C TraceContext + Baggage: istemciden gelen trace başlığının
	// sürdürülebilmesi için gereklidir. Ayarlanmazsa her servis kendi kopuk
	// trace'ini üretir ve dağıtık izleme hiçbir şeyi birleştiremez.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	log.InfoContext(ctx, "izleme kuruldu",
		"endpoint", opts.Endpoint,
		"insecure", opts.Insecure,
		"ornekleme_orani", opts.SampleRatio)

	return func(ctx context.Context) error {
		return kapanisiYurut(ctx, shutdownTimeout, tracerSağlayıcı, meterSağlayıcı)
	}, nil
}

// kapanabilir kapanışta bir dışa aktarıcıdan beklenen tek davranıştır.
//
// Somut SDK tipleri yerine bu arayüzle çalışmak, süre paylaşımının gerçek bir
// toplayıcı ayağa kaldırmadan sınanabilmesi içindir.
type kapanabilir interface {
	Shutdown(ctx context.Context) error
}

// kapanisiYurut sağlayıcıları PARALEL kapatır ve her birine KENDİ süresini verir.
//
// Önceki hâlde tek bir bağlam tracer ile meter arasında paylaşılıyordu:
// toplayıcı erişilemezken tracer bütün bütçeyi yiyor, meter ise süresi çoktan
// dolmuş bir bağlamla çağrılıyordu. Sonuç, kapanışta bekleyen metriklerin
// SESSİZCE düşmesiydi — üstelik en çok ihtiyaç duyulan metrikler, sürecin son
// anlarına ait olanlardı.
//
// Süreyi ikiye bölmek (her birine yarım bütçe) de starvation'ı çözerdi ama
// yalnızca BİR sağlayıcının yavaş olduğu yaygın durumda onu boş yere yarı
// bütçeye mahkûm ederdi. Sırayla kapatıp her birine tam süre vermek ise en
// kötü durumda kapanışı iki katına çıkarır; iki sağlayıcı ayrı gRPC
// bağlantıları kullandığı için beklemeyi seri hâle getirmenin bir karşılığı
// yok. Bu yüzden paralel + sağlayıcı başına tam süre seçildi: toplam bekleme
// yine tek bir sure ile sınırlı kalır.
func kapanisiYurut(ctx context.Context, sure time.Duration, sağlayıcılar ...kapanabilir) error {
	hatalar := make([]error, len(sağlayıcılar))

	var wg sync.WaitGroup
	wg.Add(len(sağlayıcılar))

	for i, s := range sağlayıcılar {
		go func() {
			defer wg.Done()

			kctx, iptal := context.WithTimeout(ctx, sure)
			defer iptal()

			hatalar[i] = s.Shutdown(kctx)
		}()
	}
	wg.Wait()

	return errors.Join(hatalar...)
}

// noopShutdown izleme kapalıyken kullanılan kapanış işlevidir.
func noopShutdown(context.Context) error { return nil }

// kaynak trace ve metriklere iliştirilen servis kimliğini kurar.
func kaynak(ctx context.Context, opts Options) (*resource.Resource, error) {
	return resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(opts.ServiceName),
			semconv.ServiceVersion(opts.ServiceVersion),
			semconv.DeploymentEnvironmentNameKey.String(opts.Environment),
		),
	)
}

// izSaglayici OTLP dışa aktarıcılı tracer sağlayıcısını kurar.
func izSaglayici(
	ctx context.Context, opts Options, res *resource.Resource,
) (*sdktrace.TracerProvider, error) {
	cikis := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(opts.Endpoint)}
	if opts.Insecure {
		cikis = append(cikis, otlptracegrpc.WithInsecure())
	}

	exp, err := otlptracegrpc.New(ctx, cikis...)
	if err != nil {
		return nil, err
	}

	return sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(ornekleyici(opts.SampleRatio)),
	), nil
}

// ornekleyici örnekleme kararını veren sampler'ı kurar.
//
// UZAK ebeveynin "sampled" bayrağı oranı EZEMEZ. traceparent başlığı halka
// açık bir uçta tamamen istemcinin denetimindedir; ParentBased'in varsayılanı
// olan [sdktrace.AlwaysSample] ile her isteği "örneklenmiş" işaretleyen bir
// saldırgan OTEL_TRACES_SAMPLER_ARG=0.01 ayarını anlamsız kılar ve izleme
// maliyetini istediği kadar şişirir. Bu yüzden uzak ebeveyn geldiğinde karar
// yeniden AYNI oranla hesaplanır.
//
// Bu, dağıtık trace'i delik deşik ETMEZ: [sdktrace.TraceIDRatioBased] kararı
// trace ID'nin kendisinden türetir, yani aynı oranı kullanan her servis aynı
// trace için aynı sonuca varır. Oranların servisler arasında farklı olduğu
// kurulumda tutarlılık zaten kaybolur; oradaki doğru çözüm oranı hizalamaktır,
// istemciye güvenmek değil.
//
// Uzak ebeveyn örneklenmemişse hiç örneklemeyiz: üst servis kararı zaten
// "hayır" iken bizim "evet" dememiz, hiçbir zaman tamamlanmayacak, ebeveynsiz
// span'lar üretirdi.
//
// YEREL ebeveyn için ParentBased'in varsayılanı korunur (üst span'ı izleriz):
// aynı süreçteki alt span'a bağımsız karar verdirmek, tek bir isteğin span
// ağacını kendi içinde parçalardı.
func ornekleyici(oran float64) sdktrace.Sampler {
	oransal := sdktrace.TraceIDRatioBased(oran)

	return sdktrace.ParentBased(
		oransal,
		sdktrace.WithRemoteParentSampled(oransal),
		sdktrace.WithRemoteParentNotSampled(sdktrace.NeverSample()),
	)
}

// metrikSaglayici OTLP dışa aktarıcılı meter sağlayıcısını kurar.
func metrikSaglayici(
	ctx context.Context, opts Options, res *resource.Resource,
) (*sdkmetric.MeterProvider, error) {
	cikis := []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpoint(opts.Endpoint)}
	if opts.Insecure {
		cikis = append(cikis, otlpmetricgrpc.WithInsecure())
	}

	exp, err := otlpmetricgrpc.New(ctx, cikis...)
	if err != nil {
		return nil, err
	}

	aralik := opts.MetricInterval
	if aralik <= 0 {
		aralik = time.Minute
	}

	return sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp,
			sdkmetric.WithInterval(aralik))),
	), nil
}

// Attrs slog kayıtlarına eklenecek ortak öznitelikleri döner.
func Attrs(opts Options) []attribute.KeyValue {
	return []attribute.KeyValue{
		semconv.ServiceName(opts.ServiceName),
		semconv.ServiceVersion(opts.ServiceVersion),
	}
}
