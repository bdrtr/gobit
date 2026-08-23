// Package workflow modüller arası çok adımlı işlemleri yürüten saga motorudur
// (plan Bölüm 5.5, Faz 3).
//
// Bir workflow, sırayla çalışan adımlardan oluşur. Her adımın bir Invoke'u ve
// onun geri alımı olan bir Compensate'i vardır. Motor adımları sırayla yürütür;
// biri patlarsa O ANA KADAR BAŞARILI olmuş adımların Compensate'lerini TERS
// SIRADA çağırır. Dağıtık bir işlemin (2PC) yerini tutan şey budur: modüller
// ayrı tablolara, ileride ayrı servislere sahip olduğu için tek bir veritabanı
// işlemiyle sarılamazlar (plan Bölüm 2.2, 2.3).
//
// # Patlayan adım telafi EDİLMEZ — tek istisnası motorun kendi tekrarıdır
//
// Telafi zinciri kural olarak Invoke'u BAŞARIYLA dönmüş adımlarla sınırlıdır.
// Patlayan adımın kendisi telafi edilmez: Invoke hata döndüyse geri alınacak
// bir iş yoktur ve olmayan bir işi geri almaya çalışmak, yarım kalmış durumu
// daha da bozar (örn. hiç yaratılmamış bir rezervasyonu "iptal etmek", var olan
// başka bir rezervasyonu iptal edebilir). Tek denemede patlayan bir adım için
// bedel adım yazarına düşer: Invoke'u ya tümüyle başarılı olmalı ya da KENDİ
// İÇİNDE temiz bırakmalıdır.
//
// Kuralın TEK İSTİSNASI motorun KENDİ tetiklediği yeniden denemedir. Adım
// birden çok kez denendiyse (kayıtta Attempts > 1) patlayan adım da EN İYİ
// ÇABA ile telafi edilir ve telafi zincirinin BAŞINA konur. Gerekçe: yeniden
// denemenin var olma sebebi tam da "istek gitti, yanıt kayboldu" durumudur ve
// o durumda 1. deneme yan etkiyi DÜNYAYA UYGULAMIŞTIR. Onu geri almayan bir
// motor, yürütmeyi StatusFailed (= "iş yapıldı ve GERİ ALINDI") diye yazarken
// yalan söyler; gerçek bir siparişte bu, kimsenin göremediği yetim bir
// rezervasyondur. Tekrarı adım değil MOTOR başlattığı için bedelini de motor
// üstlenir. Çağrı sözleşmeye göre güvenlidir: Compensate zaten IDEMPOTENT
// olmak ve iki kez çağrılabilmek zorundadır (bkz. Step). Bunun adım yazarına
// getirdiği tek şart açıktır — Compensate, yan etkinin HİÇ uygulanmamış
// olabileceği durumda da doğru davranmalı, yani geri alacak bir şey
// bulamadığında no-op yapıp nil dönmelidir.
//
// Diğer seçenekler neden seçilmedi: yürütmeyi failed yerine ayrı bir "kirli"
// duruma yazmak yan etkiyi GERİ ALMAZ, yalnızca bildirir — üstelik en iyi çaba
// telafi patlarsa motor zaten StatusCompensationFailed yazar, yani o seçeneğin
// verdiği izleme sinyali bunun içinde vardır. "Adım yazarı temiz bıraksın"
// demek ise yetmez: adım, motorun kaçıncı denemesinde olduğunu bilse bile
// tekrarı o istememiştir.
//
// # Asılı yan etkiyi bildiren adımlar
//
// Kendi içinde geri alma yapan bileşik adımlar (bkz. ParallelStep) geri alma
// patladığında arkalarında telafi EDİLMEMİŞ iş bırakır. Böyle bir adım
// hatasını ErrUncompensated ile sarmalıdır: motor gözcüyü hata zincirinde
// görürse, telafi zinciri eksiksiz tamamlanmış olsa bile yürütmeyi
// StatusFailed değil StatusCompensationFailed olarak yazar.
//
// # Telafi hatası zinciri DURDURMAZ
//
// Telafi sırasında bir Compensate patlarsa kalan telafiler YİNE DE denenir.
// Sebep basittir: 3. adımın telafisinin patlaması, 1. adımın telafisinin
// çalışmaması için bir gerekçe değildir; zinciri orada kesmek, geri
// alınabilecek işleri de asılı bırakır. Hatalar errors.Join ile birleştirilir
// ve yürütme StatusCompensationFailed olur — bu durum ELLE MÜDAHALE ister ve
// izlemenin öncelikle saydığı şey olmalıdır.
//
// # Yeniden deneme
//
// Yeniden deneme adım başınadır ve VARSAYILAN OLARAK KAPALIDIR (bkz. NoRetry);
// WithRetry ile açılır. Hangi hataların denenebilir olduğu için bkz.
// DefaultRetryable. Telafi de yeniden denenir: telafi hatasının bedeli elle
// müdahale olduğu için, geçici bir arızada ısrar etmek Invoke'ta ısrar
// etmekten daha değerlidir. Telafi politikası ayrıca verilmediyse adım
// politikasını devralır (bkz. WithCompensationRetry).
//
// Panik ve bağlam hataları, RetryPolicy.Retryable ile ÖZEL bir yüklem
// verilmiş olsa bile yeniden denenmez; eleme yüklemden önce ve koşulsuz
// uygulanır (bkz. RetryPolicy.Retryable).
//
// # Idempotency-key
//
// WithIdempotencyKey verilen bir yürütme, (workflow adı, anahtar) çiftiyle
// Store'da tekildir. İkinci çağrının davranışı ilk yürütmenin durumuna bağlıdır
// ve Executor.Run'da tek tek belgelenmiştir. Tekillik "önce oku sonra yaz" ile
// değil, doğrudan Store.Create'in Conflict dönüşüyle kurulur; okuma-yazma
// arasındaki yarışa açık bir kontrol, iki eşzamanlı isteğin ikisini de
// çalıştırabilirdi.
//
// Bağlam çağrı ANINDA ölüyse (istemci bağlantıyı kesmiştir) motor kaydı HİÇ
// AÇMAZ ve hemen hata döner. Sebep: anahtarın var olma sebebi, istemcinin
// zaman aşımından sonra AYNI anahtarla güvenle tekrar denemesidir; hiçbir adım
// çalışmamışken kaydı açıp uç duruma yazmak, o anahtarı kalıcı olarak yakar ve
// istemci sonsuza dek Conflict alır. Denetim yarışı tümüyle kapatmaz — bağlam
// kayıt açıldıktan hemen sonra da ölebilir — ama yaygın olan durumu, yani
// çağrıya ölü gelen bağlamı kesin olarak karşılar.
//
// # Kalıcılaştırma politikası (Store hatalarında ne olur)
//
// Store hataları TEK TİP ele alınmaz; ölçü, hatanın yan etkiyi iki kez
// uygulama riski taşıyıp taşımadığıdır:
//
//   - Create ve FindByIdempotencyKey hataları YÜRÜTMEYİ DÜŞÜRÜR. İkisi de
//     tekrar korumasının kapısıdır: kaydı açamadan ya da var olan yürütmenin
//     sonucunu okuyamadan adımları çalıştırmak, aynı işi ikinci kez yapma
//     riskini kabul etmek olurdu. Hiçbir adım çalışmadan hata döner.
//   - AppendStep ve UpdateStatus hataları LOGLANIR ve yürütme DEVAM EDER.
//     Bu noktada adımın yan etkisi ZATEN DÜNYAYA UYGULANMIŞTIR; kayıt onun
//     kendisi değil, izidir. Defter tutulamadı diye başarılı bir iş akışını
//     geri almak, muhasebe arızasını müşteriye görünen bir arızaya çevirir —
//     üstelik geri alma kayıtları da aynı bozuk Store'a yazılacaktır. Telafi
//     için gereken "hangi adımlar başarılı oldu" bilgisi motorun BELLEĞİNDE
//     tutulur, Store'dan okunmaz; bu yüzden Store çökse de telafi doğru çalışır.
//
// Kabul edilen bedel, izin delik kalmasıdır: UpdateStatus yazılamazsa yürütme
// Store'da running görünmeye devam eder ve aynı anahtarla yapılan bir sonraki
// çağrı Conflict alır. Bu yön bilinçli seçilmiştir — yanlış tarafa düşmek
// (çıktıyı hiç çalışmamış gibi göstermek) işi ikinci kez yaptırırdı. Her
// başarısız Store yazması ERROR seviyesinde, yürütme kimliğiyle loglanır.
//
// # Bağlam iptali
//
// ctx çağrı ANINDA ölüyse yürütme hiç başlamaz (bkz. Executor.Run). Yürütme
// başladıktan sonra iptal edilirse durur ve o ana kadarki adımlar YİNE DE
// telafi edilir. Telafi iptal edilmiş bir bağlamla çalışamayacağı için motor
// context.WithoutCancel ile türetilmiş, kendi süre bütçesi olan ayrı bir
// bağlam kullanır (bkz. WithCompensationTimeout). Bütçe ADIM BAŞINADIR: tek
// bir paylaşılan bütçe, zincirin sonundaki yavaş bir telafiyle tükendiğinde
// geriye kalan — ve tipik olarak EN AĞIR kaynağı tutan, en erken — adımları
// ölü bir bağlamla çağırırdı. Aynı gerekçeyle Store yazmaları da iptalden
// etkilenmez (bkz. WithStoreTimeout).
//
// # Panik
//
// Bir adımın Invoke ya da Compensate'inde çıkan panik motoru çökertmez:
// yakalanır, yığın iziyle loglanır ve ErrPanic'i saran tipli bir hataya
// çevrilir. Panik sonrası akış normal hata akışıyla aynıdır — Invoke panikledi
// ise telafi başlar, Compensate panikledi ise zincir kalan adımlarla sürer.
// Panik yeniden DENENMEZ (bkz. DefaultRetryable).
//
// Adım tanımının kendisinden gelen panikler de motoru çökertmez: tipli-nil bir
// adım (nil işaretçi taşıyan, ama nil OLMAYAN arayüz değeri) Name() çağrılmadan
// önce Workflow.Validate'te yakalanır ve errors.Invalid'e çevrilir.
//
// # Serileştirme
//
// Girdi, çıktı ve adım çıktıları Store'a JSON olarak yazılır. Girdi JSON'a
// çevrilemiyorsa yürütme HİÇ BAŞLAMAZ (errors.Invalid) — henüz yan etki
// yokken hatayı erken vermek bedavadır. Adım çıktısı çevrilemiyorsa adım
// başarılı sayılır, olay loglanır ve kayıtta Output boş, Failure açıklama
// dolu kalır: o noktada yan etki uygulanmıştır, serileştirme ayrıntısı için
// geri alınamaz.
//
// Executor.Run'ın çıktısı HER İKİ YOLDA da json.RawMessage'dır: hem adımların
// çalıştığı mutlu yolda hem idempotency tekrarında. Tip kararlılığı çağıranın
// tip doğrulamasının yarışa bağlı olmaması içindir — tekrar yolunda çıktı
// Store'dan okunur ve Go tipi orada zaten kaybolmuştur. Tipli okuma için
// bkz. RunInto.
package workflow

import (
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"runtime/debug"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// Hata kodları. Çağıran taraf bunlara göre dallanabilir.
const (
	// CodeInvalidWorkflow workflow tanımının geçersiz olduğunu bildirir.
	CodeInvalidWorkflow = "workflow_invalid"
	// CodeInvalidOption bir RunOption'ın geçersiz olduğunu bildirir.
	CodeInvalidOption = "workflow_invalid_option"
	// CodeInvalidOutput yürütme çıktısının istenen tipe çevrilemediğini bildirir.
	CodeInvalidOutput = "workflow_invalid_output"
	// CodeStepFailed bir adımın patladığını ve telafinin tamamlandığını bildirir.
	CodeStepFailed = "workflow_step_failed"
	// CodeStepPanicked bir adımın panik ettiğini bildirir.
	CodeStepPanicked = "workflow_step_panicked"
	// CodeParallelBranchFailed bir ParallelStep dalının patladığını bildirir.
	CodeParallelBranchFailed = "workflow_parallel_branch_failed"
	// CodeCompensationFailed telafinin tamamlanamadığını bildirir; elle müdahale gerekir.
	CodeCompensationFailed = "workflow_compensation_failed"
	// CodeCanceled yürütmenin bağlam iptali yüzünden durduğunu bildirir.
	CodeCanceled = "workflow_canceled"
	// CodeStoreFailed kalıcılaştırma katmanının hata döndüğünü bildirir.
	CodeStoreFailed = "workflow_store_failed"
	// CodeExecutionRunning aynı anahtarlı bir yürütmenin hâlâ sürdüğünü bildirir.
	CodeExecutionRunning = "workflow_execution_running"
	// CodeExecutionFailed aynı anahtarlı bir yürütmenin daha önce patladığını bildirir.
	CodeExecutionFailed = "workflow_execution_failed"
	// CodeExecutionNotFound istenen yürütmenin bulunamadığını bildirir.
	CodeExecutionNotFound = "workflow_execution_not_found"
	// CodeExecutionExists aynı kimlikli ya da anahtarlı yürütmenin zaten var olduğunu bildirir.
	CodeExecutionExists = "workflow_execution_exists"
)

// ErrPanic bir adımın panik ettiğini bildiren gözcü (sentinel) hatadır.
//
// Motorun ürettiği panik hatası bunu sarar; çağıran errors.Is(err, ErrPanic)
// ile programlama hatasını geçici arızadan ayırabilir.
var ErrPanic = errors.New("adım panikledi")

// ErrUncompensated bir adımın GERİ ALINAMAMIŞ yan etki bıraktığını bildiren
// gözcü (sentinel) hatadır.
//
// Kendi içinde geri alma yapan adımlar (bkz. ParallelStep) geri alma
// patladığında hatalarını bununla sarar. Motor gözcüyü adım hatasının
// zincirinde görürse, telafi zinciri eksiksiz tamamlansa bile yürütmeyi
// StatusCompensationFailed olarak yazar: StatusFailed "iş yapıldı ve GERİ
// ALINDI" demektir ve asılı bir yan etki varken o kayıt yalan olurdu.
var ErrUncompensated = errors.New("adımın telafi edilmemiş yan etkisi var")

// StepContext bir adımın yürütme sırasında gördüğü bağlamdır.
type StepContext struct {
	// Input workflow'a verilen girdidir; tüm adımlar aynı değeri görür.
	Input any

	// Shared adımlar arası veri taşıyan haritadır. Adımlar buraya yazar,
	// sonraki adımlar okur. TELAFİ SIRASINDA DA aynı harita geçirilir;
	// bir Compensate, kendi Invoke'unun yazdığı değeri buradan bulur
	// (örn. "hangi rezervasyonu iptal edeceğim").
	//
	// Harita ardışık adımlar arasında kilitsiz kullanılır çünkü motor adımları
	// tek goroutine'de sırayla çağırır. Eşzamanlı dallar için bkz. ParallelStep.
	Shared map[string]any

	// ExecutionID yürütmenin kimliğidir; adımlar bunu kendi idempotency
	// anahtarları olarak kullanabilir.
	ExecutionID string
	// Workflow yürütülen workflow'un adıdır.
	Workflow string
	// StepName o an çalışan adımın adıdır; motor her çağrıdan önce yazar.
	StepName string
	// StepIndex o an çalışan adımın sırasıdır; motor her çağrıdan önce yazar.
	StepIndex int
	// Attempt o an çalışan denemenin sırasıdır (1'den başlar); motor her
	// çağrıdan önce yazar. Adım, ilk denemeyle yeniden denemeyi buna göre
	// ayırt edebilir.
	Attempt int
}

// Step workflow'un tek bir adımıdır.
//
// Uygulamalar iki söz verir: Invoke ya tümüyle başarılı olur ya da arkasında
// iş bırakmaz; Compensate ise Invoke'un yan etkisini geri alır ve IDEMPOTENT
// çalışır (yeniden denenebildiği ve iki kez çağrılabildiği için).
type Step interface {
	// Name adımın kayıtlarda ve loglarda görünen adıdır; boş olamaz.
	Name() string
	// Invoke adımın işini yapar ve çıktısını döner. Çıktı Store'a JSON olarak
	// yazılır; son adımın çıktısı workflow'un çıktısıdır.
	Invoke(ctx context.Context, sc *StepContext) (output any, err error)
	// Compensate Invoke'un yan etkisini geri alır. Yalnızca Invoke'u BAŞARIYLA
	// dönmüş adımlar için çağrılır.
	Compensate(ctx context.Context, sc *StepContext) error
}

// Workflow adımlardan oluşan bir iş akışıdır.
type Workflow struct {
	// Name workflow'un adıdır; Store'da idempotency anahtarıyla birlikte
	// tekilliği tanımlar. Boş olamaz.
	Name string
	// Steps sırayla yürütülecek adımlardır; en az bir adım olmalıdır.
	// Aynı ada sahip adımlar serbesttir: kayıtlarda kimlik, ad değil Index'tir.
	Steps []Step
}

// Validate workflow tanımının yürütülebilir olup olmadığını denetler.
//
// Ad uzunlukları da burada denetlenir (bkz. MaxNameLen): sınır Store
// sözleşmesinin parçasıdır ve motorda uygulanması, kalıcı bir Store'da
// patlayacak bir yürütmenin HİÇ başlamamasını sağlar — bellek içi Store'da
// geçip Postgres'te düşen bir workflow olmaz.
//
// nil denetimi TİPLİ NİL'i de kapsar (bkz. isNilStep): arayüz değeri nil
// olmasa da içindeki işaretçi nil olabilir ve öyle bir değerde Name()
// çağırmak motoru çökertirdi.
func (w Workflow) Validate() error {
	if w.Name == "" {
		return errors.Invalid(CodeInvalidWorkflow, "workflow adı boş olamaz")
	}
	if len(w.Name) > MaxNameLen {
		return errors.Invalid(CodeInvalidWorkflow,
			"workflow adı en fazla %d bayt olabilir, %d bayt verildi", MaxNameLen, len(w.Name))
	}
	if len(w.Steps) == 0 {
		return errors.Invalid(CodeInvalidWorkflow, "%q workflow'unda hiç adım yok", w.Name)
	}

	for i, s := range w.Steps {
		if isNilStep(s) {
			return errors.Invalid(CodeInvalidWorkflow, "%q workflow'unun %d. adımı nil", w.Name, i)
		}

		name := s.Name()
		if name == "" {
			return errors.Invalid(CodeInvalidWorkflow, "%q workflow'unun %d. adımının adı boş", w.Name, i)
		}
		if len(name) > MaxNameLen {
			return errors.Invalid(CodeInvalidWorkflow,
				"%q workflow'unun %d. adımının adı en fazla %d bayt olabilir, %d bayt verildi",
				w.Name, i, MaxNameLen, len(name))
		}
	}
	return nil
}

// isNilStep bir adımın nil ya da TİPLİ NİL olup olmadığını söyler.
//
// Arayüz değeri, içinde nil bir işaretçi taşırken bile nil DEĞİLDİR: bir
// eklentinin adım yapıcısı hata durumunda (*myStep)(nil) dönerse s == nil
// denetimi geçer ve s.Name() nil işaretçi çözümlemesiyle panikler. Panik
// motoru çökertmeden önce reflect ile yakalanır; bedeli tanım başına tek bir
// yansıma çağrısıdır.
func isNilStep(s Step) bool {
	if s == nil {
		return true
	}

	v := reflect.ValueOf(s)
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice,
		reflect.Func, reflect.Chan, reflect.UnsafePointer:
		return v.IsNil()
	default:
		return false
	}
}

// Executor bir workflow'u yürüten motordur.
type Executor interface {
	// Run adımları sırayla yürütür; bir adım patlarsa o ana kadar başarılı
	// adımların Compensate'lerini ters sırada çalıştırır ve durumu persist eder.
	// Çıktı her yolda json.RawMessage'dır; tipli okuma için bkz. RunInto.
	Run(ctx context.Context, wf Workflow, input any, opts ...RunOption) (any, error)
}

// RunInto workflow'u yürütür ve çıktısını T'ye çözer.
//
// Executor.Run'ın çıktısı json.RawMessage'dır ama imzasındaki any bunu
// derleyiciden gizler; bu yardımcı, çıktının tipini çağrı yerinde sözleşmeye
// bağlar. Çıktı boşsa (son adım nil döndüyse ya da çıktı JSON'a çevrilemediyse)
// T'nin sıfır değeri ve nil hata döner: yürütme başarılıdır, okunacak bir şey
// yoktur. Çözümleme patlarsa hata errors.Invalid'dir — yürütme O NOKTADA
// TAMAMLANMIŞTIR, başarısız olan yalnızca okumadır.
func RunInto[T any](ctx context.Context, e Executor, wf Workflow, input any, opts ...RunOption) (T, error) {
	var out T

	raw, err := e.Run(ctx, wf, input, opts...)
	if err != nil {
		return out, err
	}

	payload, ok := raw.(json.RawMessage)
	if !ok {
		return out, errors.Internal(CodeInvalidOutput,
			"%q workflow'unun çıktısı json.RawMessage değil: %T", wf.Name, raw)
	}
	if len(payload) == 0 {
		return out, nil
	}
	if uerr := json.Unmarshal(payload, &out); uerr != nil {
		return out, errors.Wrap(uerr, errors.KindInvalid, CodeInvalidOutput,
			"%q workflow'unun çıktısı %T tipine çevrilemedi", wf.Name, out)
	}
	return out, nil
}

// executor Executor'ın tek uygulamasıdır.
type executor struct {
	store Store
	log   *slog.Logger
}

var _ Executor = (*executor)(nil)

// Log alan adları; tüm kayıtlarda aynı anahtarlar kullanılır.
const (
	attrWorkflow    = "workflow"
	attrExecutionID = "execution_id"
	attrStep        = "step"
	attrStepIndex   = "step_index"
	attrAttempt     = "attempt"
	attrError       = "error"
)

// New verilen Store üzerine bir saga motoru kurar.
//
// log nil verilirse slog.Default kullanılır. store nil verilirse motor kurulur
// ama ÇALIŞMAZ: kurulum ERROR olarak loglanır ve her Run errors.Invalid ile
// reddedilir. Eksik Store'da sessizce süreç içi bir depoya düşmek, idempotency
// korumasını süreç sınırına indirir — yanlış kablolanmış iki replika aynı
// anahtarla aynı ödemeyi çeker ve bunun tek izi bir uyarı satırı olurdu.
// Süreç içi depo bu yüzden ÇAĞIRANIN açık kararıdır (bkz. NewInMemory).
//
// Reddin kurulumda değil ilk Run'da hata olarak dönmesinin sebebi imzadır:
// Executor döndüren bir yapıcının hata kanalı yoktur ve eksik bağımlılık
// yüzünden panik atmak, kablolamayı bir arıza sinyaline değil çökmeye çevirir.
func New(store Store, log *slog.Logger) Executor {
	if log == nil {
		log = slog.Default()
	}
	if store == nil {
		log.Error("workflow: Store verilmedi; motor kuruldu ama her Run reddedilecek — süreç içi depo için NewInMemory kullanın")
	}
	return &executor{store: store, log: log}
}

// NewInMemory süreç içi, kalıcılığı olmayan bir depo üzerine motor kurar.
//
// Test ve geliştirme içindir ve adı gereği AÇIK bir karardır: süreç ölürse
// yürütme geçmişi kaybolur, birden çok replikada ise idempotency yalnızca tek
// süreç içinde geçerlidir. Üretim için New kullanılmalıdır.
func NewInMemory(log *slog.Logger) Executor {
	if log == nil {
		log = slog.Default()
	}
	return &executor{store: NewMemoryStore(), log: log}
}

// doneStep telafi zincirine giren bir adımdır.
//
// rec, adımın Invoke kaydıdır: telafi kaydı aynı Index'i GÜNCELLEDİĞİ için
// (bkz. Store.AppendStep) çıktı ve deneme bilgisi ancak burada taşınırsa
// korunur.
type doneStep struct {
	step Step
	rec  StepRecord
	// bestEffort adımın Invoke'unun PATLADIĞINI ama motorun onu birden çok kez
	// denemiş olması yüzünden yine de telafi edileceğini bildirir.
	bestEffort bool
}

// Run adımları sırayla yürütür ve yürütme durumunu kalıcılaştırır.
//
// Dönüş değeri son adımın çıktısının json.RawMessage karşılığıdır (tipli okuma
// için bkz. RunInto). Bir adım patlarsa o ana kadar başarılı olmuş adımlar
// TERS SIRADA telafi edilir; patlayan adımın kendisi yalnızca MOTOR onu birden
// çok kez denediyse en iyi çaba olarak telafi edilir (bkz. paket yorumu).
// Telafi eksiksiz tamamlanırsa yürütme StatusFailed, telafinin kendisi
// patlarsa ya da adım hatası ErrUncompensated taşıyorsa
// StatusCompensationFailed olarak yazılır; ikinci durumda dönen hata hem adım
// hatasını hem telafi hatalarını errors.Join ile taşır ve KindInternal'dır.
//
// Bağlam çağrı anında zaten iptal edilmişse hiçbir kayıt AÇILMAZ ve
// errors.Unavailable döner: hiç iş yapılmadan idempotency anahtarını yakmak,
// anahtarın var olma sebebini tersine çevirirdi. Motor Store olmadan
// kurulmuşsa hiçbir adım çalıştırılmaz ve errors.Invalid döner (bkz. New).
//
// Dönen hatanın sınıfı (Kind), yürütme [StatusFailed] yazıldıysa PATLAYAN
// ADIMIN sınıfını korur; böylece HTTP katmanı geçersiz girdiyi 422, çakışmayı
// 409 olarak haritalamaya devam edebilir. [StatusCompensationFailed] ve asılı
// yan etki bildiren ([ErrUncompensated]) durumlarda sınıf KindInternal'a
// yükseltilir: geride temizlenmemiş iş varken çağırana "girdin geçersizdi"
// demek yanıltıcı olurdu.
//
// WithIdempotencyKey verilmişse aynı anahtarla yapılan ikinci çağrı adımları
// TEKRAR ÇALIŞTIRMAZ ve ilk yürütmenin durumuna göre davranır:
//
//   - completed → ilk yürütmenin ÇIKTISI döner. Çıktı Store'dan okunur; Go tipi
//     kalıcılaştırmada kaybolduğu için json.RawMessage'dır. Mutlu yol da aynı
//     tipi döndürür, böylece çağıranın tip doğrulaması hangi yola düştüğüne
//     BAĞLI DEĞİLDİR.
//   - running → errors.Conflict. Aynı iş hâlâ uçuştadır; ikinci bir kopyasını
//     başlatmak tam da anahtarın engellemek için var olduğu şeydir.
//   - failed → errors.Conflict. Yürütme geri alınmış olsa bile motor kendiliğinden
//     TEKRARLAMAZ: anahtar bir denemenin SONUCUNU adlandırır, sonsuz bir
//     tekrar hakkını değil. Aynı anahtarla sessizce yeniden çalıştırmak, ilk
//     denemenin yan etkilerinin gerçekten geri alındığı VARSAYIMINA dayanırdı;
//     telafi ise en iyi çabadır. Yeniden denemek çağıranın açık kararıdır ve
//     YENİ bir anahtar ister — böylece "bu iş kaç kez denendi" sorusu Store'dan
//     yanıtlanabilir kalır.
//   - compensation_failed → errors.Conflict. Sistem tutarsızdır; elle müdahale
//     edilmeden üstüne yeni bir yürütme koymak hasarı büyütür.
func (e *executor) Run(ctx context.Context, wf Workflow, input any, opts ...RunOption) (any, error) {
	if e.store == nil {
		return nil, errors.Invalid(CodeInvalidOption,
			"workflow motoru Store olmadan kuruldu; kalıcı bir Store verin ya da süreç içi depo için NewInMemory kullanın")
	}

	o, err := newRunOptions(opts)
	if err != nil {
		return nil, err
	}
	if verr := wf.Validate(); verr != nil {
		return nil, verr
	}
	if cerr := ctx.Err(); cerr != nil {
		// Bağlam çağrıya ölü geldi: kayıt AÇILMAZ. Açılsaydı hiçbir adım
		// çalışmadan uç duruma yazılır ve idempotency anahtarı kalıcı olarak
		// yanardı (bkz. paket yorumu, "Idempotency-key").
		return nil, errors.Wrap(cerr, errors.KindUnavailable, CodeCanceled,
			"%q workflow'u başlatılmadı: bağlam çağrı anında zaten iptal edilmişti", wf.Name)
	}

	payload, merr := json.Marshal(input)
	if merr != nil {
		return nil, errors.Wrap(merr, errors.KindInvalid, CodeInvalidWorkflow,
			"%q workflow'unun girdisi JSON'a çevrilemedi", wf.Name)
	}

	exec, err := e.open(ctx, wf, payload, o)
	if err != nil {
		return nil, err
	}
	if exec == nil {
		// Aynı anahtarla açılmış bir yürütme bulundu; sonucu replay verdi.
		return e.replay(ctx, wf, o)
	}

	return e.execute(ctx, wf, input, exec, o)
}

// open yürütme kaydını açar.
//
// Aynı anahtarlı bir kayıt zaten varsa (nil, nil) döner; çağıran replay'e
// gider. Diğer Store hataları yürütmeyi düşürür: tekrar korumasının kapısı
// buradadır.
func (e *executor) open(ctx context.Context, wf Workflow, payload json.RawMessage, o *runOptions) (*Execution, error) {
	now := time.Now().UTC()
	exec := &Execution{
		ID:             newExecutionID(now),
		Workflow:       wf.Name,
		IdempotencyKey: o.idempotencyKey,
		Status:         StatusRunning,
		Input:          payload,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	sctx, cancel := o.storeContext(ctx)
	err := e.store.Create(sctx, exec)
	cancel()

	switch {
	case err == nil:
		return exec, nil
	case o.idempotencyKey != "" && errors.IsConflict(err):
		return nil, nil
	default:
		return nil, errors.Wrap(err, errors.KindOf(err), CodeStoreFailed,
			"%q workflow'u için yürütme kaydı açılamadı", wf.Name)
	}
}

// replay aynı idempotency anahtarıyla açılmış yürütmenin sonucunu döner.
func (e *executor) replay(ctx context.Context, wf Workflow, o *runOptions) (any, error) {
	sctx, cancel := o.storeContext(ctx)
	prev, err := e.store.FindByIdempotencyKey(sctx, wf.Name, o.idempotencyKey)
	cancel()
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeStoreFailed,
			"%q workflow'unun %q anahtarlı yürütmesi okunamadı", wf.Name, o.idempotencyKey)
	}
	if prev == nil {
		// Sözleşme ihlali: hata yoksa kayıt dolu olmalıdır. Store ayrı bir
		// pakette yazıldığı için motor bunu nil çözümlemesiyle değil, tipli
		// hatayla karşılar.
		return nil, errors.Internal(CodeStoreFailed,
			"Store %q workflow'unun %q anahtarı için hatasız nil kayıt döndürdü", wf.Name, o.idempotencyKey)
	}

	switch prev.Status {
	case StatusCompleted:
		e.log.Info("workflow: idempotency anahtarı eşleşti, adımlar tekrar çalıştırılmadı",
			attrWorkflow, wf.Name, attrExecutionID, prev.ID)
		return prev.Output, nil
	case StatusRunning:
		return nil, errors.Conflict(CodeExecutionRunning,
			"%q workflow'unun %q anahtarlı yürütmesi (%s) hâlâ sürüyor", wf.Name, o.idempotencyKey, prev.ID)
	case StatusFailed:
		return nil, errors.Conflict(CodeExecutionFailed,
			"%q workflow'unun %q anahtarlı yürütmesi (%s) daha önce başarısız oldu ve telafi edildi; yeniden denemek için YENİ bir anahtar kullanın: %s",
			wf.Name, o.idempotencyKey, prev.ID, prev.Failure)
	case StatusCompensationFailed:
		return nil, errors.Conflict(CodeExecutionFailed,
			"%q workflow'unun %q anahtarlı yürütmesi (%s) telafi edilemedi; elle müdahale gerekir: %s",
			wf.Name, o.idempotencyKey, prev.ID, prev.Failure)
	default:
		return nil, errors.Internal(CodeStoreFailed,
			"%q yürütmesi bilinmeyen durumda: %q", prev.ID, prev.Status)
	}
}

// execute adımları sırayla yürütür ve sonucu kalıcılaştırır.
func (e *executor) execute(ctx context.Context, wf Workflow, input any, exec *Execution, o *runOptions) (any, error) {
	sc := &StepContext{
		Input:       input,
		Shared:      make(map[string]any),
		ExecutionID: exec.ID,
		Workflow:    wf.Name,
	}

	done := make([]doneStep, 0, len(wf.Steps))
	var last any

	for i, s := range wf.Steps {
		if cerr := ctx.Err(); cerr != nil {
			// İptal adımlar ARASINDA yakalandı: yeni adım başlatılmaz, o ana
			// kadarki adımlar telafi edilir.
			cause := errors.Wrap(cerr, errors.KindUnavailable, CodeCanceled,
				"%q workflow'u %d. adımdan önce iptal edildi", wf.Name, i)
			return e.unwind(ctx, sc, exec, done, o, cause)
		}

		name := s.Name()
		sc.StepName, sc.StepIndex, sc.Attempt = name, i, 1

		started := time.Now().UTC()
		out, attempts, serr := e.invokeStep(ctx, s, sc, o)
		ended := time.Now().UTC()

		if serr != nil {
			rec := StepRecord{
				Name:      name,
				Index:     i,
				Status:    StepFailed,
				Failure:   serr.Error(),
				Attempts:  attempts,
				StartedAt: started,
				EndedAt:   ended,
			}
			e.persistStep(ctx, exec.ID, o, rec)
			e.log.ErrorContext(ctx, "workflow: adım başarısız, telafi başlıyor",
				attrWorkflow, wf.Name, attrExecutionID, exec.ID,
				attrStep, name, attrStepIndex, i, attrAttempt, attempts, attrError, serr)

			if attempts > 1 {
				// Motor adımı KENDİ kararıyla birden çok kez denedi: ilk
				// denemenin yan etkisi dünyaya uygulanmış olabilir. Adım,
				// telafi zincirinin başına en iyi çaba olarak eklenir
				// (bkz. paket yorumu).
				done = append(done, doneStep{step: s, rec: rec, bestEffort: true})
			}

			cause := errors.Wrap(serr, errors.KindOf(serr), CodeStepFailed,
				"%q workflow'unun %q adımı (%d) başarısız oldu", wf.Name, name, i)
			return e.unwind(ctx, sc, exec, done, o, cause)
		}

		rec := StepRecord{
			Name:      name,
			Index:     i,
			Status:    StepInvoked,
			Attempts:  attempts,
			StartedAt: started,
			EndedAt:   ended,
		}
		rec.Output, rec.Failure = e.encode(ctx, out, exec.ID, name)
		e.persistStep(ctx, exec.ID, o, rec)

		done = append(done, doneStep{step: s, rec: rec})
		last = out
	}

	output, note := e.encode(ctx, last, exec.ID, "")
	e.persistStatus(ctx, exec.ID, o, StatusCompleted, output, note)
	e.log.InfoContext(ctx, "workflow: tamamlandı",
		attrWorkflow, wf.Name, attrExecutionID, exec.ID, "steps", len(wf.Steps))

	return output, nil
}

// unwind telafi zincirini yürütür ve yürütmeyi uç duruma yazar.
//
// cause telafiyi tetikleyen hatadır (adım hatası ya da bağlam iptali).
// StatusFailed yalnızca telafi eksiksiz tamamlandığında VE adım hatası
// ErrUncompensated taşımadığında yazılır: kendi içinde geri alma yapan bir
// adım o gözcüyle "arkamda asılı iş kaldı" der ve motorun telafi zinciri temiz
// bitse bile kayıt "geri alındı" diyemez.
func (e *executor) unwind(ctx context.Context, sc *StepContext, exec *Execution, done []doneStep, o *runOptions, cause error) (any, error) {
	compErr := e.compensate(ctx, sc, exec.ID, done, o)

	if compErr == nil && !errors.Is(cause, ErrUncompensated) {
		e.persistStatus(ctx, exec.ID, o, StatusFailed, nil, cause.Error())
		return nil, cause
	}

	final := errors.Wrap(errors.Join(cause, compErr), errors.KindInternal, CodeCompensationFailed,
		"%q workflow'unun telafisi tamamlanamadı (%s); ELLE MÜDAHALE gerekir", sc.Workflow, exec.ID)
	e.persistStatus(ctx, exec.ID, o, StatusCompensationFailed, nil, final.Error())
	e.log.ErrorContext(ctx, "workflow: telafi tamamlanamadı, elle müdahale gerekir",
		attrWorkflow, sc.Workflow, attrExecutionID, exec.ID, attrError, final)

	return nil, final
}

// compensate telafi zincirini TERS SIRADA çağırır.
//
// Her adım KENDİ süre bütçesini alır ve bu bütçe çağıranın bağlamının
// iptalinden ETKİLENMEZ (context.WithoutCancel): telafinin iptal edilmiş bir
// bağlamla çalışması imkânsızdır, oysa iptal telafinin en çok gerektiği
// durumlardan biridir. Bütçenin adım başına olması bilinçlidir — zincirin
// sonundaki yavaş bir telafi paylaşılan bir bütçeyi tüketseydi, geriye kalan
// ve tipik olarak en ağır kaynağı tutan (ödeme çekimi gibi) EN ERKEN adımlar
// ölü bir bağlamla çağrılır, bağlama saygılı her Compensate anında düşerdi.
// Bir Compensate patlarsa zincir DURMAZ; hata biriktirilir ve kalan adımlar
// yine denenir. Dönüş, biriken hataların errors.Join'idir.
//
// Telafi kaydı, adımın Invoke kaydının ÜZERİNE yazılır (Store.AppendStep aynı
// Index'i günceller) ve Output, Attempts ile StartedAt KORUNUR: elle müdahale
// gerektiren tek durum olan compensation_failed'de operatörün ihtiyacı olan
// tek veri, adımın hangi rezervasyonu/ödemeyi ürettiğidir.
func (e *executor) compensate(ctx context.Context, sc *StepContext, execID string, done []doneStep, o *runOptions) error {
	if len(done) == 0 {
		return nil
	}

	base := context.WithoutCancel(ctx)

	var failures []error
	for i := len(done) - 1; i >= 0; i-- {
		d := done[i]
		sc.StepName, sc.StepIndex, sc.Attempt = d.rec.Name, d.rec.Index, 1

		cctx, cancel := context.WithTimeout(base, o.compensationTimeout)
		attempts, err := e.compensateStep(cctx, d.step, sc, o)
		ended := time.Now().UTC()
		cancel()

		// Invoke'un kaydı taşınır; yalnızca durum, hata ve bitiş anı değişir.
		rec := d.rec
		rec.Status = StepCompensated
		rec.EndedAt = ended

		if err != nil {
			wrapped := errors.Wrap(err, errors.KindOf(err), CodeCompensationFailed,
				"%q adımının (%d) telafisi başarısız oldu", rec.Name, rec.Index)
			failures = append(failures, wrapped)

			rec.Status = StepCompensationFailed
			rec.Failure = joinFailure(rec.Failure, wrapped.Error())

			e.log.ErrorContext(ctx, "workflow: telafi başarısız, zincire devam ediliyor",
				attrWorkflow, sc.Workflow, attrExecutionID, execID,
				attrStep, rec.Name, attrStepIndex, rec.Index, attrAttempt, attempts,
				"best_effort", d.bestEffort, attrError, err)
		}

		e.persistStep(ctx, execID, o, rec)
	}

	return errors.Join(failures...)
}

// joinFailure iki hata metnini kayıt için birleştirir.
//
// En iyi çaba telafi edilen bir adımın kaydında Invoke'un hatası zaten
// yazılıdır; telafi de patlarsa ikisi de gerekir — biri neyin denendiğini,
// diğeri neyin asılı kaldığını söyler.
func joinFailure(existing, added string) string {
	if existing == "" {
		return added
	}
	return existing + "; " + added
}

// invokeStep bir adımın Invoke'unu politika kadar yeniden dener.
//
// Dönüşteki sayı yapılan toplam deneme sayısıdır (en az 1).
func (e *executor) invokeStep(ctx context.Context, s Step, sc *StepContext, o *runOptions) (output any, attempts int, err error) {
	p := o.retry
	for attempt := 1; ; attempt++ {
		sc.Attempt = attempt

		out, serr := e.safeInvoke(ctx, s, sc)
		if serr == nil {
			return out, attempt, nil
		}
		if attempt >= p.MaxAttempts || !p.allow(serr) {
			return nil, attempt, serr
		}

		e.log.WarnContext(ctx, "workflow: adım başarısız, yeniden denenecek",
			attrWorkflow, sc.Workflow, attrExecutionID, sc.ExecutionID,
			attrStep, sc.StepName, attrStepIndex, sc.StepIndex, attrAttempt, attempt, attrError, serr)

		if werr := wait(ctx, p.backoffFor(attempt)); werr != nil {
			// Bekleme sırasında bağlam öldü; yeni deneme başlatmanın anlamı yok.
			return nil, attempt, serr
		}
	}
}

// compensateStep bir adımın Compensate'ini politika kadar yeniden dener.
func (e *executor) compensateStep(ctx context.Context, s Step, sc *StepContext, o *runOptions) (attempts int, err error) {
	p := o.compensationRetry
	for attempt := 1; ; attempt++ {
		sc.Attempt = attempt

		cerr := e.safeCompensate(ctx, s, sc)
		if cerr == nil {
			return attempt, nil
		}
		if attempt >= p.MaxAttempts || !p.allow(cerr) {
			return attempt, cerr
		}

		e.log.WarnContext(ctx, "workflow: telafi başarısız, yeniden denenecek",
			attrWorkflow, sc.Workflow, attrExecutionID, sc.ExecutionID,
			attrStep, sc.StepName, attrStepIndex, sc.StepIndex, attrAttempt, attempt, attrError, cerr)

		if werr := wait(ctx, p.backoffFor(attempt)); werr != nil {
			return attempt, cerr
		}
	}
}

// safeInvoke adımın Invoke'unu panik yakalayarak çağırır.
func (e *executor) safeInvoke(ctx context.Context, s Step, sc *StepContext) (out any, err error) {
	defer func() {
		if r := recover(); r != nil {
			out = nil
			err = e.recovered(ctx, sc, "Invoke", r)
		}
	}()

	return s.Invoke(ctx, sc)
}

// safeCompensate adımın Compensate'ini panik yakalayarak çağırır.
func (e *executor) safeCompensate(ctx context.Context, s Step, sc *StepContext) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = e.recovered(ctx, sc, "Compensate", r)
		}
	}()

	return s.Compensate(ctx, sc)
}

// recovered yakalanan paniği loglayıp tipli hataya çevirir.
//
// Yığın izi hataya DEĞİL loga yazılır: hata metni HTTP katmanında istemciye
// dönebilir ve yığın izi dosya yolları ile iç yapıyı sızdırır (plan Bölüm 8).
func (e *executor) recovered(ctx context.Context, sc *StepContext, phase string, r any) error {
	e.log.ErrorContext(ctx, "workflow: adım panikledi",
		attrWorkflow, sc.Workflow, attrExecutionID, sc.ExecutionID,
		attrStep, sc.StepName, attrStepIndex, sc.StepIndex, "phase", phase,
		"panic", r, "stack", string(debug.Stack()))

	return panicError(sc.StepName, phase, r)
}

// panicError panik değerini ErrPanic'i saran tipli hataya çevirir.
func panicError(step, phase string, r any) error {
	if err, ok := r.(error); ok {
		return errors.Wrap(errors.Join(ErrPanic, err), errors.KindInternal, CodeStepPanicked,
			"%q adımının %s'u panikledi", step, phase)
	}
	return errors.Wrap(ErrPanic, errors.KindInternal, CodeStepPanicked,
		"%q adımının %s'u panikledi: %v", step, phase, r)
}

// wait verilen süre kadar bekler; bağlam ölürse erken döner.
func wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}

	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// encode bir değeri JSON'a çevirir; çevrilemezse (nil, açıklama) döner.
//
// Çevrilememek yürütmeyi DÜŞÜRMEZ: bu noktada adımın yan etkisi zaten
// uygulanmıştır ve serileştirme ayrıntısı için geri alınamaz. Olay loglanır,
// açıklama kayda yazılır. step boşsa çevrilen şey workflow çıktısıdır.
func (e *executor) encode(ctx context.Context, v any, execID, step string) (payload json.RawMessage, note string) {
	payload, err := json.Marshal(v)
	if err == nil {
		return payload, ""
	}

	e.log.ErrorContext(ctx, "workflow: çıktı JSON'a çevrilemedi, kayıt çıktısız yazılıyor",
		attrExecutionID, execID, attrStep, step, attrError, err)

	return nil, "çıktı JSON'a çevrilemedi: " + err.Error()
}

// persistStep adım kaydını yazar; hata loglanır ve yürütme devam eder.
func (e *executor) persistStep(ctx context.Context, execID string, o *runOptions, rec StepRecord) {
	sctx, cancel := o.storeContext(ctx)
	defer cancel()

	if err := e.store.AppendStep(sctx, execID, rec); err != nil {
		e.log.ErrorContext(ctx, "workflow: adım kaydı yazılamadı, yürütme devam ediyor",
			attrExecutionID, execID, attrStep, rec.Name, attrStepIndex, rec.Index,
			"step_status", string(rec.Status), attrError, err)
	}
}

// persistStatus yürütmenin uç durumunu yazar; hata loglanır ve sonuç değişmez.
func (e *executor) persistStatus(ctx context.Context, execID string, o *runOptions, status Status, output json.RawMessage, failure string) {
	sctx, cancel := o.storeContext(ctx)
	defer cancel()

	if err := e.store.UpdateStatus(sctx, execID, status, output, failure); err != nil {
		e.log.ErrorContext(ctx, "workflow: yürütme durumu yazılamadı; kayıt Store'da running kalmış olabilir",
			attrExecutionID, execID, "status", string(status), attrError, err)
	}
}
