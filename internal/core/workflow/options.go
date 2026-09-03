package workflow

import (
	"context"
	"math"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// Varsayılan süre bütçeleri.
const (
	// DefaultCompensationTimeout TEK BİR telafi çağrısı için verilen varsayılan
	// süredir; bütçe adım başınadır (bkz. WithCompensationTimeout).
	DefaultCompensationTimeout = 30 * time.Second
	// DefaultStoreTimeout tek bir Store çağrısı için verilen varsayılan süredir.
	DefaultStoreTimeout = 5 * time.Second
)

// RetryPolicy bir adımın kaç kez ve hangi aralıkla yeniden deneneceğini tanımlar.
//
// Sıfır değeri geçerli DEĞİLDİR; politikayı WithRetry/WithCompensationRetry
// doğrular ve eksik alanları makul varsayılanlara çeker.
type RetryPolicy struct {
	// MaxAttempts toplam deneme sayısıdır (ilk deneme dâhil). En az 1 olmalıdır;
	// 1 "yeniden deneme yok" demektir.
	MaxAttempts int
	// Backoff ilk yeniden denemeden önceki beklemedir. 0 ise beklenmez.
	Backoff time.Duration
	// Multiplier her denemede beklemenin çarpanıdır. 0 veya 1 ise bekleme
	// sabittir; 2 ikili üstel geri çekilme verir.
	Multiplier float64
	// MaxBackoff beklemenin üst sınırıdır. 0 ise sınır yoktur.
	MaxBackoff time.Duration
	// Retryable bir hatanın yeniden denenebilir olup olmadığını söyler.
	// nil ise DefaultRetryable kullanılır.
	//
	// Yüklem KENDİ BAŞINA karar vermez: panik ve bağlam hataları ona hiç
	// sorulmadan elenir (bkz. allow). Aksi hâlde "her hatayı dene" diyen bir
	// yüklem, panikleyen bir Invoke'un kısmi yan etkisini her denemede yeniden
	// uygular ve ölü bir bağlamda boşuna döner.
	Retryable func(error) bool
}

// NoRetry yeniden deneme yapmayan politikadır ve motorun VARSAYILANIDIR.
//
// Varsayılanın "denemesin" olması bilinçlidir: motor bir adımın Invoke'unun
// idempotent olup olmadığını bilemez. "Kartı çek" gibi bir adımı kendiliğinden
// yeniden denemek, hatanın yanıt yolunda (istek gitti, cevap kayboldu)
// oluştuğu durumda yan etkiyi İKİ KEZ uygular. Bu yüzden yeniden deneme adımın
// idempotentliğini bilen çağıranın açık kararıdır: WithRetry ile istenir.
func NoRetry() RetryPolicy {
	return RetryPolicy{MaxAttempts: 1}
}

// DefaultRetryable bir hatanın yeniden denenmeye değer olup olmadığını söyler.
//
// Sınıflandırma hatanın SEBEBİNE bakar: aynı girdiyle aynı sonucu verecek bir
// hatayı yeniden denemek yalnızca gecikme üretir.
//
//   - KindInvalid, KindConflict, KindNotFound, KindUnauthorized, KindForbidden
//     → DENENMEZ. Girdi, durum ya da yetki hatasıdır; adım değişmediği sürece
//     sonuç da değişmez.
//   - KindUnavailable → DENENİR. Tanımı gereği geçicidir.
//   - KindInternal → DENENİR. Sınıflandırılmamış hatalar (tipli olmayan
//     hatalar dâhil) bu sınıfa düşer ve aralarında ağ/veritabanı kesintisi
//     gibi geçici olanlar vardır; iyimser davranmanın bedeli birkaç denemedir.
//   - Panik → DENENMEZ. Panik bir programlama hatasıdır; tekrarı aynı çöküşü
//     üretir ve yalnızca hata mesajını geciktirir.
//   - context.Canceled / DeadlineExceeded → DENENMEZ. Bağlam ölmüşken
//     yeniden denemek bütçesi olmayan bir işe girmektir.
//   - ErrUncompensated → DENENMEZ. Adım arkasında geri alınamamış bir yan etki
//     bırakmıştır; tekrar, o asılı işin ÜSTÜNE ikincisini koyardı.
func DefaultRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrPanic) || errors.Is(err, ErrUncompensated) {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	switch errors.KindOf(err) {
	case errors.KindUnavailable, errors.KindInternal:
		return true
	default:
		return false
	}
}

// allow hatanın politikaya göre yeniden denenebilir olup olmadığını söyler.
//
// Panik, bağlam ve "telafi edilmemiş yan etki" hataları özel yükleme
// SORULMADAN elenir: bu elemeler motorun güvencesidir (bkz. paket yorumu),
// politikanın tercihi değil. Özel yüklem yalnızca kalan hatalar için
// danışılır.
func (p RetryPolicy) allow(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrPanic) || errors.Is(err, ErrUncompensated) {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	if p.Retryable != nil {
		return p.Retryable(err)
	}
	return DefaultRetryable(err)
}

// backoffFor verilen denemeden sonraki beklemeyi hesaplar (attempt 1'den başlar).
func (p RetryPolicy) backoffFor(attempt int) time.Duration {
	if p.Backoff <= 0 {
		return 0
	}

	d := float64(p.Backoff)
	if p.Multiplier > 1 && attempt > 1 {
		d *= math.Pow(p.Multiplier, float64(attempt-1))
	}
	// Taşma koruması: üstel büyüme int64 sınırını aşarsa üst sınıra çekilir.
	if d > float64(math.MaxInt64) {
		d = float64(math.MaxInt64)
	}

	out := time.Duration(d)
	if p.MaxBackoff > 0 && out > p.MaxBackoff {
		out = p.MaxBackoff
	}
	return out
}

// normalize politikayı doğrular ve eksik alanları varsayılana çeker.
func (p RetryPolicy) normalize(what string) (RetryPolicy, error) {
	if p.MaxAttempts < 1 {
		return p, errors.Invalid(CodeInvalidOption, "%s: MaxAttempts en az 1 olmalı, %d verildi", what, p.MaxAttempts)
	}
	if p.Backoff < 0 {
		return p, errors.Invalid(CodeInvalidOption, "%s: Backoff negatif olamaz, %s verildi", what, p.Backoff)
	}
	if p.MaxBackoff < 0 {
		return p, errors.Invalid(CodeInvalidOption, "%s: MaxBackoff negatif olamaz, %s verildi", what, p.MaxBackoff)
	}
	if p.Multiplier < 0 || math.IsNaN(p.Multiplier) {
		return p, errors.Invalid(CodeInvalidOption, "%s: Multiplier negatif ya da NaN olamaz", what)
	}
	if p.Multiplier < 1 {
		// 0 (sıfır değer) sabit bekleme anlamına gelir.
		p.Multiplier = 1
	}
	return p, nil
}

// runOptions tek bir Run çağrısının çözülmüş ayarlarıdır.
type runOptions struct {
	idempotencyKey      string
	retry               RetryPolicy
	compensationRetry   RetryPolicy
	compensationTimeout time.Duration
	storeTimeout        time.Duration
	lease               time.Duration
	// compensationRetrySet kullanıcının telafi politikasını AYRICA verip
	// vermediğini tutar; vermediyse telafi, adım politikasını devralır.
	compensationRetrySet bool
}

// RunOption Executor.Run çağrısının davranışını değiştirir.
type RunOption func(*runOptions) error

// WithIdempotencyKey yürütmeyi bir tekrar koruma anahtarına bağlar.
//
// Aynı workflow adı ve aynı anahtarla yapılan ikinci çağrı adımları TEKRAR
// ÇALIŞTIRMAZ; ilk yürütmenin sonucuna göre davranır (bkz. Executor.Run).
// Anahtar boş olamaz ve MaxIdempotencyKeyLen baytı aşamaz: sınır Store
// sözleşmesinin parçasıdır ve burada uygulanması, kalıcı bir Store'un
// indeksinde patlayacak bir anahtarın hiç iş yapmadan reddedilmesini sağlar.
func WithIdempotencyKey(key string) RunOption {
	return func(o *runOptions) error {
		if key == "" {
			return errors.Invalid(CodeInvalidOption, "idempotency anahtarı boş olamaz")
		}
		if len(key) > MaxIdempotencyKeyLen {
			return errors.Invalid(CodeInvalidOption,
				"idempotency anahtarı en fazla %d bayt olabilir, %d bayt verildi",
				MaxIdempotencyKeyLen, len(key))
		}
		o.idempotencyKey = key
		return nil
	}
}

// WithRetry adımların yeniden deneme politikasını belirler.
//
// Telafi için ayrıca WithCompensationRetry verilmediyse telafi de bu
// politikayı devralır.
func WithRetry(p RetryPolicy) RunOption {
	return func(o *runOptions) error {
		np, err := p.normalize("WithRetry")
		if err != nil {
			return err
		}
		o.retry = np
		return nil
	}
}

// WithCompensationRetry telafinin yeniden deneme politikasını ayrıca belirler.
//
// Verilmezse telafi, WithRetry ile verilen adım politikasını devralır. Ayrı
// verilebilmesinin sebebi, iki tarafın bedelinin farklı olmasıdır: başarısız
// bir Invoke'un bedeli yürütmenin geri alınmasıdır, başarısız bir Compensate'in
// bedeli ELLE MÜDAHALEDİR. Bu yüzden "adımı bir kez dene ama telafide ısrar et"
// meşru bir yapılandırmadır.
func WithCompensationRetry(p RetryPolicy) RunOption {
	return func(o *runOptions) error {
		np, err := p.normalize("WithCompensationRetry")
		if err != nil {
			return err
		}
		o.compensationRetry = np
		o.compensationRetrySet = true
		return nil
	}
}

// WithLease bir yürütmenin MEŞRU olarak sürebileceği en uzun süreyi bildirir.
//
// # Neden gerekli
//
// Bir yürütme kaydı "running" açılır ve uç duruma geçerek kapanır. Süreç o
// geçişi yazamadan ölürse — deploy, OOM, pod tahliyesi, çökme — kayıt SONSUZA
// DEK running kalır. Motorun tekrar mantığı ona bakıp "hâlâ sürüyor" der ve
// aynı anahtarla gelen her çağrı 409 alır. Ölçüldü: üç gün önce çökmüş bir
// yürütme hâlâ "sürüyor" diyordu ve o sepet bir daha ödenemiyordu.
//
// Yaşlılık tek başına kanıt değildir, kira SÜRESİ kanıttır: çağıran zaten
// akışa sonlu bir bütçe veriyorsa (sepet akışı iki dakika), o bütçeden uzun
// süre "running" duran bir kayıt, hiçbir sürecin tutamayacağı bir kayıttır.
// Bu yüzden süre motorca tahmin edilmez, çağıranca BİLDİRİLİR.
//
// # Ne yapılır
//
// Kira dolmuş bir kayıt "sürüyor" sayılmaz; ne yapıldığı adım kayıtlarına
// bakılarak belirlenir (bkz. [Executor.Run]):
//
//   - Hiçbir adım iş yapmamışsa telafi edilecek bir şey yoktur: kayıt
//     [StatusFailed] olur, anahtarını bırakır ve çağıran YENİDEN deneyebilir.
//   - İş yapılmışsa telafi HİÇ çalışmamıştır ve yarım iş ortadadır: kayıt
//     [StatusCompensationFailed] olur, anahtarını TUTAR ve elle müdahale
//     gerektiğini söyler. Sessizce yeniden denemek, ayrılmış stoğun ikinci kez
//     ayrılması demekti.
//
// Sıfır ya da negatif değer bu davranışı KAPATIR: kira bildirmeyen bir çağıran
// eski davranışı alır.
func WithLease(d time.Duration) RunOption {
	return func(o *runOptions) error {
		o.lease = d

		return nil
	}
}

// WithCompensationTimeout her bir telafi çağrısı için süre bütçesi verir.
//
// Bütçe ADIM BAŞINADIR: her Compensate kendi süresini alır ve bir adımın yavaş
// telafisi kendisinden önceki adımların bütçesini yemez. Zincirin toplam
// süresi en kötü hâlde adım sayısı × bütçedir; bu bilinçli bir takastır, çünkü
// alternatifi (paylaşılan tek bütçe) tam da en erken ve tipik olarak en ağır
// kaynağı tutan adımı ölü bağlamla çağırmaktı. Adımın yeniden denenen
// telafileri de bu bütçeyi paylaşır. Varsayılanı DefaultCompensationTimeout'tur.
// Sıfır ya da negatif verilemez: bütçesiz bir telafi, ölü bir bağımlılıkta
// süresiz asılır.
func WithCompensationTimeout(d time.Duration) RunOption {
	return func(o *runOptions) error {
		if d <= 0 {
			return errors.Invalid(CodeInvalidOption, "telafi süresi pozitif olmalı, %s verildi", d)
		}
		o.compensationTimeout = d
		return nil
	}
}

// WithStoreTimeout tek bir Store çağrısı için süre bütçesi verir.
//
// Varsayılanı DefaultStoreTimeout'tur. Sıfır ya da negatif verilemez.
func WithStoreTimeout(d time.Duration) RunOption {
	return func(o *runOptions) error {
		if d <= 0 {
			return errors.Invalid(CodeInvalidOption, "store süresi pozitif olmalı, %s verildi", d)
		}
		o.storeTimeout = d
		return nil
	}
}

// newRunOptions seçenekleri sırayla uygular ve doğrulanmış ayarları döner.
func newRunOptions(opts []RunOption) (*runOptions, error) {
	o := &runOptions{
		retry:               NoRetry(),
		compensationRetry:   NoRetry(),
		compensationTimeout: DefaultCompensationTimeout,
		storeTimeout:        DefaultStoreTimeout,
	}

	for _, opt := range opts {
		if opt == nil {
			return nil, errors.Invalid(CodeInvalidOption, "nil RunOption verilemez")
		}
		if err := opt(o); err != nil {
			return nil, err
		}
	}

	if !o.compensationRetrySet {
		o.compensationRetry = o.retry
	}
	return o, nil
}

// storeContext Store çağrıları için iptalden ETKİLENMEYEN, süreli bir bağlam üretir.
//
// Çağıranın bağlamı iptal edilmiş olsa bile yürütmenin izi yazılabilmelidir:
// kalıcılaştırmayı iptale bağlamak, tam da izin en çok gerektiği anda (iptal
// edilmiş, telafi edilmiş bir yürütme) kaydı boş bırakırdı. Bütçe yine vardır;
// erişilemez bir veritabanı yürütmeyi süresiz asamaz.
func (o *runOptions) storeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), o.storeTimeout)
}
