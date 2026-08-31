package observability

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// ornekTraceID örnekleme kararının deterministik olduğu sabit bir trace ID'dir.
//
// Rastgele üretilmemesi bilinçlidir: TraceIDRatioBased kararı trace ID'nin
// kendisinden türettiği için rastgele bir ID testi aralıklı olarak
// başarısızlaştırırdı.
var ornekTraceID = trace.TraceID{
	0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
	0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10,
}

// ornekSpanID ebeveyn span bağlamının geçerli sayılması için gereken kimliktir.
var ornekSpanID = trace.SpanID{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}

// ebeveynBaglami verilen bayraklarla bir ebeveyn span bağlamı kurar.
func ebeveynBaglami(uzak, orneklenmis bool) context.Context {
	var bayraklar trace.TraceFlags
	if orneklenmis {
		bayraklar = trace.FlagsSampled
	}

	return trace.ContextWithSpanContext(context.Background(),
		trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    ornekTraceID,
			SpanID:     ornekSpanID,
			TraceFlags: bayraklar,
			Remote:     uzak,
		}))
}

// TestUzakEbeveynOrneklemeOraniniEzemez istemcinin gönderdiği traceparent'ın
// örnekleme oranını devre dışı bırakamadığını doğrular.
//
// Regresyon: ParentBased'in VARSAYILANI, uzak ebeveyn "sampled" işaretliyse
// AlwaysSample'dır. Halka açık bir uçta traceparent tamamen istemcinin
// denetimindedir; her isteği örneklenmiş işaretleyen bir saldırgan
// OTEL_TRACES_SAMPLER_ARG=0.01 ayarını anlamsız kılıp izleme maliyetini
// şişirebiliyordu.
func TestUzakEbeveynOrneklemeOraniniEzemez(t *testing.T) {
	tests := map[string]struct {
		oran     float64
		uzak     bool
		ebeveyn  bool
		beklenen sdktrace.SamplingDecision
	}{
		"uzak ebeveyn sampled ama oran sıfır": {
			oran: 0, uzak: true, ebeveyn: true, beklenen: sdktrace.Drop,
		},
		"uzak ebeveyn sampled ve oran bir": {
			oran: 1, uzak: true, ebeveyn: true, beklenen: sdktrace.RecordAndSample,
		},
		"uzak ebeveyn sampled değil": {
			oran: 1, uzak: true, ebeveyn: false, beklenen: sdktrace.Drop,
		},
		// Aynı süreçteki alt span üst span'ı izler; aksi hâlde tek bir isteğin
		// span ağacı kendi içinde delik deşik olurdu.
		"yerel ebeveyn sampled ve oran sıfır": {
			oran: 0, uzak: false, ebeveyn: true, beklenen: sdktrace.RecordAndSample,
		},
		"yerel ebeveyn sampled değil": {
			oran: 1, uzak: false, ebeveyn: false, beklenen: sdktrace.Drop,
		},
	}

	for ad, tt := range tests {
		t.Run(ad, func(t *testing.T) {
			sonuc := ornekleyici(tt.oran).ShouldSample(sdktrace.SamplingParameters{
				ParentContext: ebeveynBaglami(tt.uzak, tt.ebeveyn),
				TraceID:       ornekTraceID,
				Name:          "GET /public",
			})

			assert.Equal(t, tt.beklenen, sonuc.Decision)
		})
	}
}

// TestEbeveynsizIstekOranaTabidir ebeveyn bağlamı olmayan (kök) span'ların
// yapılandırılan oranla örneklendiğini doğrular.
func TestEbeveynsizIstekOranaTabidir(t *testing.T) {
	params := sdktrace.SamplingParameters{
		ParentContext: context.Background(),
		TraceID:       ornekTraceID,
		Name:          "GET /public",
	}

	assert.Equal(t, sdktrace.Drop, ornekleyici(0).ShouldSample(params).Decision)
	assert.Equal(t, sdktrace.RecordAndSample, ornekleyici(1).ShouldSample(params).Decision)
}

// sahteSaglayici kapanış çağrısını ve aldığı süreyi kaydeden bir dışa
// aktarıcı taklididir.
type sahteSaglayici struct {
	// bekle true ise Shutdown, bağlamı sona erene kadar bloklar; erişilemez
	// bir toplayıcının bütçeyi yemesi böyle taklit edilir.
	bekle bool
	// hata Shutdown'ın döneceği hatadır.
	hata error

	// kalan Shutdown'a girildiğinde bağlamda KALAN süredir.
	kalan atomic.Int64
	// cagrildi Shutdown'ın çağrılıp çağrılmadığını bildirir.
	cagrildi atomic.Bool
}

// Shutdown kapanabilir arayüzünü karşılar.
func (s *sahteSaglayici) Shutdown(ctx context.Context) error {
	s.cagrildi.Store(true)

	if sonTarih, ok := ctx.Deadline(); ok {
		s.kalan.Store(int64(time.Until(sonTarih)))
	}

	if s.bekle {
		<-ctx.Done()
	}

	return s.hata
}

// TestKapanisSuresiSaglayicilarArasindaPaylasilmaz yavaş bir sağlayıcının
// diğerinin süresini tüketmediğini doğrular.
//
// Regresyon: tek bir bağlam tracer ve meter arasında paylaşılıyordu. Toplayıcı
// erişilemezken tracer bütçenin tamamını yiyor, meter ise süresi dolmuş bir
// bağlamla çağrılıyordu; bekleyen metrikler sessizce düşüyordu.
func TestKapanisSuresiSaglayicilarArasindaPaylasilmaz(t *testing.T) {
	const sure = 100 * time.Millisecond

	// İkisi de bloklar: seri kapanışta ikincisi ancak birincinin süresi
	// dolduktan SONRA başlardı.
	iz := &sahteSaglayici{bekle: true}
	metrik := &sahteSaglayici{bekle: true}

	basla := time.Now()
	err := kapanisiYurut(context.Background(), sure, iz, metrik)
	gecen := time.Since(basla)

	require.NoError(t, err)
	assert.True(t, iz.cagrildi.Load(), "iz sağlayıcısı kapatılmalı")
	assert.True(t, metrik.cagrildi.Load(), "metrik sağlayıcısı kapatılmalı")

	// Her ikisi de TAM bütçeyle çağrılmalı; paylaşılan bağlamda ikincinin
	// kalan süresi sıfıra yakın olurdu.
	assert.Greater(t, metrik.kalan.Load(), int64(sure/2),
		"metrik sağlayıcısı kendi süresini almalı")
	assert.Greater(t, iz.kalan.Load(), int64(sure/2),
		"iz sağlayıcısı kendi süresini almalı")

	// Paralel kapanış: toplam bekleme tek bir sure ile sınırlı kalmalı.
	assert.Less(t, gecen, 2*sure, "sağlayıcılar paralel kapatılmalı")
}

// TestKapanisHatalariBirlestirilir bir sağlayıcının hatasının diğerinin
// kapatılmasını ENGELLEMEDİĞİNİ ve hataların birleştirildiğini doğrular.
func TestKapanisHatalariBirlestirilir(t *testing.T) {
	izHatasi := errors.New("iz gönderilemedi")
	metrikHatasi := errors.New("metrik gönderilemedi")

	iz := &sahteSaglayici{hata: izHatasi}
	metrik := &sahteSaglayici{hata: metrikHatasi}

	err := kapanisiYurut(context.Background(), time.Second, iz, metrik)

	require.Error(t, err)
	assert.ErrorIs(t, err, izHatasi)
	assert.ErrorIs(t, err, metrikHatasi)
}
