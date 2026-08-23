package workflow

import (
	"context"
	"log/slog"
	"maps"
	"reflect"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// ParallelStep dallarını EŞZAMANLI yürüten bileşik adımdır.
//
// Motor açısından tek bir Step'tir: kalıcı kayıtta tek satır görünür, yeniden
// deneme politikası dallara ayrı ayrı değil BİLEŞİĞE uygulanır ve bir dal
// patlarsa telafi zinciri bileşiğin kendisinden değil, ondan ÖNCEKİ adımlardan
// devam eder.
//
// # Bir dal patlarsa ne olur
//
// Invoke, başaran kardeş dalları KENDİ İÇİNDE geri alır ve sonra hata döner.
// Böylece motorun "patlayan adım telafi edilmez" kuralı bozulmadan geçerli
// kalır: bileşik adım hata döndüyse arkasında iş bırakmamıştır. Bu iç geri alma
// sırayla ve ters dal sırasında yürür.
//
// İç geri alma PATLARSA bu iddia düşer: kardeş dalın yan etkisi (örn. stok
// rezervasyonu) asılı kalmıştır. O durumda dönen hata ErrUncompensated'i sarar
// ve motor yürütmeyi StatusFailed değil StatusCompensationFailed olarak yazar —
// aksi hâlde kayıt "geri alındı, sistem tutarlı" derken yalan söyler ve
// compensation_failed sayan izleme kuralı bu yürütmeyi hiç göremezdi.
//
// # Shared verisi
//
// Dallar eşzamanlı çalıştığı için ortak bir map'e yazmaları veri yarışı olurdu.
// Bu yüzden her dal, üst bağlamın Shared'ının KENDİ KOPYASINI görür. Birleştirme
// iki kurala uyar:
//
//   - Yazmalar YALNIZCA tüm dallar başarılı olduğunda işlenir. Bir dal bile
//     patlarsa hiçbir dalın yazması üst bağlama geçmez; başaranlar geri
//     alınmıştır ve geri alınmış bir rezervasyonun kimliğini sonraki adımlara
//     (ya da önceki adımın Compensate'ine) sızdırmak, yanlış kaydı iptal
//     ettirir.
//   - İşlenen şey dalın kopyasının TAMAMI değil, DEĞİŞTİRDİĞİ anahtarlardır.
//     Kopyanın tamamı geri yazılsaydı, bir anahtara hiç dokunmayan dalın bayat
//     kopyası, o anahtarı güncelleyen kardeşinin yazmasını EZERDİ. Aynı
//     anahtarı gerçekten değiştiren iki daldan sonraki kazanır.
//
// Kopya yüzeyseldir: bir dal Shared'daki bir map ya da slice değerini yerinde
// değiştirirse yarış yine oluşur — dallar paylaşılan değerleri mutasyona
// uğratmamalıdır. Dallar Shared'dan anahtar SİLEMEZ; silme üst haritaya
// yansımaz.
//
// # Telafi
//
// Compensate tüm dalları TERS SIRADA ve SIRAYLA çağırır (eşzamanlı telafi,
// dalların artık paylaştığı Shared üzerinde yarışa açık olurdu). Bir dalın
// telafisi patlarsa kalanlar yine denenir; hatalar birleştirilir.
type ParallelStep struct {
	name     string
	branches []Step
	// rollbackTimeout Invoke'un iç geri alması için süre bütçesidir.
	rollbackTimeout time.Duration
	log             *slog.Logger
}

var _ Step = (*ParallelStep)(nil)

// NewParallel verilen dalları eşzamanlı yürüten bileşik bir adım üretir.
//
// name bileşiğin kayıtlarda görünen adıdır. En az bir dal verilmelidir; aksi
// hâlde Invoke errors.Invalid döner.
func NewParallel(name string, branches ...Step) *ParallelStep {
	return &ParallelStep{
		name:            name,
		branches:        branches,
		rollbackTimeout: DefaultCompensationTimeout,
	}
}

// WithRollbackTimeout dal telafilerinin süre bütçesini değiştirir ve adımı döner.
//
// Bütçe DAL BAŞINADIR ve hem iç geri almada hem motorun tetiklediği telafide
// geçerlidir. Dal telafileri EŞZAMANLI yürütüldüğü için her dal bütçesini aynı
// anda alır; yavaş bir dal kardeşlerini aç bırakmaz. Motor telafisinde
// bileşiğin tamamı ayrıca motorun adım bütçesiyle sınırlıdır
// (bkz. WithCompensationTimeout) ve bu bütçe onu UZATMAZ: dal bütçesi motorun
// kalan bütçesinden büyükse fiilen motorunki geçerlidir. Pozitif olmayan değer
// yok sayılır.
func (p *ParallelStep) WithRollbackTimeout(d time.Duration) *ParallelStep {
	if d > 0 {
		p.rollbackTimeout = d
	}
	return p
}

// WithLogger bileşiğin kullanacağı log'u belirler ve adımı döner.
//
// Verilmezse slog.Default kullanılır. Step arayüzü log taşımadığı için bileşik
// kendi log'unu almak zorundadır; panik yığın izleri buraya yazılır.
func (p *ParallelStep) WithLogger(log *slog.Logger) *ParallelStep {
	if log != nil {
		p.log = log
	}
	return p
}

// Name bileşiğin adını döner.
func (p *ParallelStep) Name() string { return p.name }

// branchResult tek bir dalın sonucudur.
type branchResult struct {
	out    any
	shared map[string]any
	err    error
}

// Invoke tüm dalları eşzamanlı çalıştırır.
//
// Hepsi başarılı olursa çıktı, dal sırasındaki çıktıların []any dilimidir.
// En az biri patlarsa başaran dallar geri alınır ve dal hataları (varsa geri
// alma hataları da) birleştirilerek döner.
func (p *ParallelStep) Invoke(ctx context.Context, sc *StepContext) (any, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	if sc.Shared == nil {
		sc.Shared = make(map[string]any)
	}

	// Dallar başlamadan önceki anlık görüntü: birleştirmede "bu dal bu anahtara
	// dokundu mu" sorusunun yanıtı buradan gelir.
	snapshot := maps.Clone(sc.Shared)

	results := make([]branchResult, len(p.branches))
	var wg sync.WaitGroup
	wg.Add(len(p.branches))

	for i, b := range p.branches {
		go func() {
			defer wg.Done()

			shared := maps.Clone(sc.Shared)
			if shared == nil {
				shared = make(map[string]any)
			}
			bsc := branchContext(sc, shared, b.Name(), i)
			out, err := p.safeCall(ctx, bsc, "Invoke", func() (any, error) {
				return b.Invoke(ctx, bsc)
			})
			results[i] = branchResult{out: out, shared: shared, err: err}
		}()
	}
	wg.Wait()

	var succeeded []int
	var failures []error
	for i, r := range results {
		if r.err != nil {
			failures = append(failures, errors.Wrap(r.err, errors.KindOf(r.err), CodeParallelBranchFailed,
				"%q bileşiğinin %q dalı başarısız oldu", p.name, p.branches[i].Name()))
			continue
		}
		succeeded = append(succeeded, i)
	}

	if len(failures) > 0 {
		// Hiçbir dalın yazması işlenmez: başaranlar geri alınacaktır, patlayanın
		// yazdığı verinin ise sahibi yoktur (bkz. tip yorumu, "Shared verisi").
		if rerr := p.rollback(ctx, sc, succeeded); rerr != nil {
			failures = append(failures, rerr)
		}
		return nil, combineBranchErrors(p.name, failures)
	}

	outputs := make([]any, len(p.branches))
	for i, r := range results {
		outputs[i] = r.out
		mergeShared(sc.Shared, snapshot, r.shared)
	}
	return outputs, nil
}

// mergeShared bir dalın DEĞİŞTİRDİĞİ anahtarları üst haritaya işler.
//
// snapshot, dallar başlamadan önceki üst haritadır. Bir anahtar orada yoksa ya
// da dalın kopyasındaki değeri farklıysa dal ona yazmıştır; değişmemiş
// anahtarlar hiç dokunulmaz ki bir dalın bayat kopyası kardeşinin yazmasını
// ezmesin.
//
// Karşılaştırma reflect.DeepEqual iledir: Shared'a konmuş bir map ya da slice
// değerinde == operatörü PANİKLERDİ. Amaç eşitlik ölçmek değil, "dokunulmadı"
// tespitidir.
func mergeShared(dst, snapshot, branch map[string]any) {
	for k, v := range branch {
		if old, ok := snapshot[k]; ok && reflect.DeepEqual(old, v) {
			continue
		}
		dst[k] = v
	}
}

// Compensate tüm dalları ters sırada ve sırayla telafi eder.
//
// Normalde yalnızca Invoke'u başarıyla dönmüş bir bileşik için çağrılır; o
// durumda tüm dallar başarılı olmuştur, bu yüzden hepsi telafi edilir. Motor
// bileşiği yeniden denemiş ve bileşik yine patlamışsa EN İYİ ÇABA telafisinde
// de çağrılabilir (bkz. paket yorumu); o çağrıda hiç çalışmamış ya da zaten
// geri alınmış dallar da telafi edilir. Dal yazarının Compensate'i bu yüzden
// idempotent olmalı ve geri alacak bir şey bulamadığında nil dönmelidir.
func (p *ParallelStep) Compensate(ctx context.Context, sc *StepContext) error {
	if err := p.validate(); err != nil {
		return err
	}

	all := make([]int, len(p.branches))
	for i := range p.branches {
		all[i] = i
	}
	return p.compensateBranches(ctx, sc, all)
}

// rollback Invoke içinde başaran dalları geri alır.
//
// Geri alma, iptalden ETKİLENMEYEN bir bağlam üzerinde yürür: dal hatası
// bağlam iptalinden kaynaklanıyorsa, çağıranın ölmüş bağlamıyla geri alma
// imkânsız olurdu.
//
// Geri alma patlarsa dönen hata ErrUncompensated'i sarar: bileşik artık
// arkasında telafi edilmemiş iş bırakmıştır ve motor bunu görmeden yürütmeyi
// "geri alındı" diye yazamaz.
func (p *ParallelStep) rollback(ctx context.Context, sc *StepContext, succeeded []int) error {
	if len(succeeded) == 0 {
		return nil
	}

	err := p.compensateBranches(context.WithoutCancel(ctx), sc, succeeded)
	if err == nil {
		return nil
	}

	return errors.Wrap(errors.Join(ErrUncompensated, err), errors.KindInternal, CodeCompensationFailed,
		"%q eşzamanlı bileşiğinin iç geri alması tamamlanamadı; ELLE MÜDAHALE gerekir", p.name)
}

// compensateBranches verilen dalları ters sırada telafi eder; hata zinciri durdurmaz.
//
// Her dal KENDİ süre bütçesini alır (bkz. WithRollbackTimeout): yavaş bir dalın
// paylaşılan bir bütçeyi tüketip kalan dalları ölü bağlamla çağırması, geri
// alınabilecek işleri de asılı bırakırdı.
func (p *ParallelStep) compensateBranches(ctx context.Context, sc *StepContext, idx []int) error {
	// Dal telafileri EŞZAMANLI yürütülür. Dallar birbirine sıra bağımlılığı
	// olmadan (eşzamanlı) çalıştığı için telafileri arasında da sıra
	// bağımlılığı yoktur; motorun ADIMLAR arası ters sıra kuralı bileşiğin
	// İÇİNE uygulanmaz.
	//
	// Bu, sıralı yürütmenin yarattığı AÇLIK sorununu da ortadan kaldırır:
	// sıralı yapıldığında her dal ortak ebeveyn bütçesinden türetiliyordu ve
	// yavaş bir dalın bütçeyi tüketmesi, sırası SONRA gelen dalların ölü
	// bağlamla çağrılmasına yol açıyordu. Eşzamanlı yürütmede her dal aynı
	// anda kendi bütçesini alır.
	//
	// Her dal, üst haritanın KENDİ KOPYASINI görür: telafiler eşzamanlı
	// olduğu için ortak haritaya yazmak veri yarışı olurdu. Telafi zaten
	// Shared'a yazmak için değil, ne geri alacağını OKUMAK için erişir.
	var (
		mu       sync.Mutex
		failures []error
		wg       sync.WaitGroup
	)

	for _, i := range idx {
		wg.Add(1)
		go func() {
			defer wg.Done()

			b := p.branches[i]
			bsc := branchContext(sc, maps.Clone(sc.Shared), b.Name(), i)

			bctx, cancel := context.WithTimeout(ctx, p.rollbackTimeout)
			defer cancel()

			_, err := p.safeCall(bctx, bsc, "Compensate", func() (any, error) {
				return nil, b.Compensate(bctx, bsc)
			})
			if err == nil {
				return
			}

			mu.Lock()
			failures = append(failures, errors.Wrap(err, errors.KindOf(err), CodeCompensationFailed,
				"%q bileşiğinin %q dalının telafisi başarısız oldu", p.name, b.Name()))
			mu.Unlock()
		}()
	}
	wg.Wait()

	// Hata sırası deterministik olsun diye dal sırasına göre toplanır.
	slices.SortFunc(failures, func(a, b error) int { return strings.Compare(a.Error(), b.Error()) })
	return errors.Join(failures...)
}

// validate bileşiğin yürütülebilir olup olmadığını denetler.
func (p *ParallelStep) validate() error {
	if p.name == "" {
		return errors.Invalid(CodeInvalidWorkflow, "eşzamanlı bileşik adımın adı boş olamaz")
	}
	if len(p.branches) == 0 {
		return errors.Invalid(CodeInvalidWorkflow, "%q eşzamanlı bileşiğinin hiç dalı yok", p.name)
	}
	for i, b := range p.branches {
		if isNilStep(b) {
			return errors.Invalid(CodeInvalidWorkflow, "%q eşzamanlı bileşiğinin %d. dalı nil", p.name, i)
		}
		if b.Name() == "" {
			return errors.Invalid(CodeInvalidWorkflow, "%q eşzamanlı bileşiğinin %d. dalının adı boş", p.name, i)
		}
	}
	return nil
}

// safeCall bir dal çağrısını panik yakalayarak yürütür.
func (p *ParallelStep) safeCall(ctx context.Context, sc *StepContext, phase string, fn func() (any, error)) (out any, err error) {
	defer func() {
		if r := recover(); r != nil {
			out = nil
			err = panicError(sc.StepName, phase, r)

			p.logger().ErrorContext(ctx, "workflow: eşzamanlı dal panikledi",
				attrWorkflow, sc.Workflow, attrExecutionID, sc.ExecutionID,
				attrStep, p.name, "branch", sc.StepName, "phase", phase,
				"panic", r, "stack", string(debug.Stack()))
		}
	}()

	return fn()
}

// logger bileşiğin log'unu döner; verilmediyse slog.Default kullanılır.
func (p *ParallelStep) logger() *slog.Logger {
	if p.log != nil {
		return p.log
	}
	return slog.Default()
}

// branchContext bir dal için StepContext türetir.
func branchContext(parent *StepContext, shared map[string]any, name string, index int) *StepContext {
	return &StepContext{
		Input:       parent.Input,
		Shared:      shared,
		ExecutionID: parent.ExecutionID,
		Workflow:    parent.Workflow,
		StepName:    name,
		StepIndex:   index,
		Attempt:     parent.Attempt,
	}
}

// combineBranchErrors dal hatalarını tek bir tipli hatada birleştirir.
//
// Bileşiğin sınıfı, yeniden denenebilirliği DOĞRU tarafa düşürecek biçimde
// seçilir: dallardan biri bile yeniden denenmeyecek sınıftaysa (örn. geçersiz
// girdi) bileşiğin tamamı yeniden denenmez — o dal her denemede aynı hatayı
// vereceği için tekrar yalnızca diğer dalların yan etkilerini boşuna yeniden
// uygular.
func combineBranchErrors(name string, failures []error) error {
	joined := errors.Join(failures...)

	kind := errors.KindOf(joined)
	for _, f := range failures {
		if !DefaultRetryable(f) {
			kind = errors.KindOf(f)
			break
		}
	}

	return errors.Wrap(joined, kind, CodeParallelBranchFailed, "%q eşzamanlı bileşik adımı başarısız oldu", name)
}
