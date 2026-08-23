package workflow_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
)

// TestParallelBranchesRunConcurrently dalların gerçekten eşzamanlı çalıştığını
// doğrular: her dal diğerinin başlamasını bekler, sıralı yürütülselerdi
// kilitlenirlerdi.
func TestParallelBranchesRunConcurrently(t *testing.T) {
	rec := &recorder{}

	startedA := make(chan struct{})
	startedB := make(chan struct{})

	a := step(rec, "a")
	a.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		close(startedA)
		select {
		case <-startedB:
			return "a-out", nil
		case <-time.After(2 * time.Second):
			return nil, errors.Internal("zaman_asimi", "b dalı başlamadı: dallar sıralı çalışıyor")
		}
	}

	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		close(startedB)
		select {
		case <-startedA:
			return "b-out", nil
		case <-time.After(2 * time.Second):
			return nil, errors.Internal("zaman_asimi", "a dalı başlamadı: dallar sıralı çalışıyor")
		}
	}

	par := workflow.NewParallel("çift", a, b).WithLogger(testLogger())
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	out, err := eng.Run(t.Context(), workflow.Workflow{Name: "eşzamanlı", Steps: []workflow.Step{par}}, nil)
	require.NoError(t, err)
	assert.JSONEq(t, `["a-out","b-out"]`, rawOutput(t, out), "çıktı dal SIRASINDA olmalı")
}

// TestParallelMergesSharedData dal kopyalarının üst bağlama işlendiğini doğrular.
func TestParallelMergesSharedData(t *testing.T) {
	rec := &recorder{}

	a := step(rec, "a")
	a.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		assert.Equal(t, "kök", sc.Shared["önceki"], "dal üst bağlamın verisini görmeli")
		sc.Shared["a"] = 1
		return nil, nil
	}

	b := step(rec, "b")
	b.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		sc.Shared["b"] = 2
		return nil, nil
	}

	önceki := step(rec, "önceki")
	önceki.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		sc.Shared["önceki"] = "kök"
		return nil, nil
	}

	var sonra map[string]any
	son := step(rec, "son")
	son.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		sonra = map[string]any{"a": sc.Shared["a"], "b": sc.Shared["b"]}
		return nil, nil
	}

	par := workflow.NewParallel("çift", a, b).WithLogger(testLogger())
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	_, err := eng.Run(t.Context(),
		workflow.Workflow{Name: "paylaşım", Steps: []workflow.Step{önceki, par, son}}, nil)
	require.NoError(t, err)

	assert.Equal(t, map[string]any{"a": 1, "b": 2}, sonra,
		"dalların yazdıkları sonraki adımda görünmeli")
}

// TestParallelBranchFailureCompensatesSiblings bir dal patladığında başaran
// kardeşlerin geri alındığını doğrular.
func TestParallelBranchFailureCompensatesSiblings(t *testing.T) {
	rec := &recorder{}

	boom := errors.New("b dalı patladı")

	a := step(rec, "a")
	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Wrap(boom, errors.KindInternal, "b_dal", "b dalı")
	}
	c := step(rec, "c")

	par := workflow.NewParallel("üçlü", a, b, c).WithLogger(testLogger())
	önce := step(rec, "önce")

	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	_, err := eng.Run(t.Context(),
		workflow.Workflow{Name: "dal_patlar", Steps: []workflow.Step{önce, par}}, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, boom)

	calls := rec.snapshot()
	// Başaran kardeşler geri alınır; patlayan dal telafi EDİLMEZ.
	assert.Contains(t, calls, "compensate:a")
	assert.Contains(t, calls, "compensate:c")
	assert.NotContains(t, calls, "compensate:b", "patlayan dalın kendisi telafi edilmemeli")
	// Bileşik adım hata döndüğü için motor onu ayrıca telafi etmez, ama
	// ondan ÖNCEKİ adım telafi edilir.
	assert.Contains(t, calls, "compensate:önce")

	// Kardeş telafileri EŞZAMANLI yürür; aralarında tersine çevrilecek bir sıra
	// yoktur (dallar da eşzamanlı çalışmıştı). Korunması gereken saga kuralı,
	// bileşiğin telafisinin ONDAN ÖNCEKİ adımın telafisinden ÖNCE bitmesidir.
	assert.Less(t, slices.Index(calls, "compensate:a"), slices.Index(calls, "compensate:önce"))
	assert.Less(t, slices.Index(calls, "compensate:c"), slices.Index(calls, "compensate:önce"))
}

// TestParallelCompensatedByEngine sonraki bir adım patladığında bileşiğin tüm
// dallarının telafi edildiğini doğrular.
func TestParallelCompensatedByEngine(t *testing.T) {
	rec := &recorder{}

	a, b := step(rec, "a"), step(rec, "b")
	par := workflow.NewParallel("çift", a, b).WithLogger(testLogger())

	son := step(rec, "son")
	son.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Internal("son", "son adım patladı")
	}

	eng := workflow.New(workflow.NewMemoryStore(), testLogger())
	_, err := eng.Run(t.Context(),
		workflow.Workflow{Name: "bileşik_telafi", Steps: []workflow.Step{par, son}}, nil)
	require.Error(t, err)

	calls := rec.snapshot()
	// Motorun ADIMLAR arası ters sıra kuralı bileşiğin İÇİNE uygulanmaz:
	// dallar eşzamanlı çalıştığı için aralarında tersine çevrilecek bir sıra
	// yoktur ve telafileri de eşzamanlı yürütülür. Aranan şey, HER dalın
	// telafi edilmiş olmasıdır.
	assert.Contains(t, calls, "compensate:a")
	assert.Contains(t, calls, "compensate:b")

	// Bileşiğin telafisi, kendisinden ÖNCEKİ adımlardan sonra gelmelidir;
	// asıl saga kuralı budur ve o korunmalıdır.
	assert.NotContains(t, calls, "compensate:son", "patlayan adım telafi edilmez")
}

// TestParallelCompensationFailureContinues bir dalın telafisi patlarken
// kalanların yine denendiğini doğrular.
func TestParallelCompensationFailureContinues(t *testing.T) {
	rec := &recorder{}

	compBoom := errors.New("b dalının telafisi patladı")

	a := step(rec, "a")
	b := step(rec, "b")
	b.onCompensate = func(context.Context, *workflow.StepContext) error {
		return errors.Wrap(compBoom, errors.KindInternal, "b_telafi", "b telafisi")
	}

	par := workflow.NewParallel("çift", a, b).WithLogger(testLogger())
	son := step(rec, "son")
	son.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Internal("son", "son adım patladı")
	}

	eng := workflow.New(workflow.NewMemoryStore(), testLogger())
	_, err := eng.Run(t.Context(),
		workflow.Workflow{Name: "dal_telafisi_patlar", Steps: []workflow.Step{par, son}}, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, compBoom)
	assert.Contains(t, rec.snapshot(), "compensate:a", "bir dalın telafi hatası kalanları durdurmamalı")
}

// TestParallelBranchPanicIsRecovered dal paniğinin motoru çökertmediğini doğrular.
func TestParallelBranchPanicIsRecovered(t *testing.T) {
	rec := &recorder{}

	a := step(rec, "a")
	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		panic("dal patladı")
	}

	par := workflow.NewParallel("çift", a, b).WithLogger(testLogger())
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "dal_panik", Steps: []workflow.Step{par}}, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, workflow.ErrPanic)
	assert.Contains(t, rec.snapshot(), "compensate:a", "panikleyen dalın kardeşi geri alınmalı")
}

// TestParallelNonRetryableBranchStopsRetry dallardan biri yeniden denenmeyen
// sınıfta hata verdiğinde bileşiğin tekrar denenmediğini doğrular.
func TestParallelNonRetryableBranchStopsRetry(t *testing.T) {
	rec := &recorder{}

	a := step(rec, "a")
	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Invalid("gecersiz", "dal girdisi geçersiz")
	}

	par := workflow.NewParallel("çift", a, b).WithLogger(testLogger())
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "denenmez_dal", Steps: []workflow.Step{par}}, nil,
		workflow.WithRetry(workflow.RetryPolicy{MaxAttempts: 4, Backoff: time.Millisecond}))

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "bileşiğin sınıfı denenmeyen dala göre seçilmeli")

	calls := rec.snapshot()
	invokes := 0
	for _, c := range calls {
		if c == "invoke:b" {
			invokes++
		}
	}
	assert.Equal(t, 1, invokes, "yeniden denenmeyen dal bileşiği de yeniden denenmez yapmalı")
}

// TestParallelRetriesWholeComposite geçici hatada bileşiğin bütün olarak
// yeniden denendiğini doğrular.
func TestParallelRetriesWholeComposite(t *testing.T) {
	rec := &recorder{}

	a := step(rec, "a")
	b := step(rec, "b")
	b.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		if sc.Attempt < 2 {
			return nil, errors.Unavailable("gecici", "dal geçici olarak kapalı")
		}
		return "b-out", nil
	}

	par := workflow.NewParallel("çift", a, b).WithLogger(testLogger())
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	out, err := eng.Run(t.Context(), workflow.Workflow{Name: "bileşik_deneme", Steps: []workflow.Step{par}}, nil,
		workflow.WithRetry(workflow.RetryPolicy{MaxAttempts: 3, Backoff: time.Millisecond}))

	require.NoError(t, err)
	assert.JSONEq(t, `["a-out","b-out"]`, rawOutput(t, out))

	calls := rec.snapshot()
	assert.Equal(t, 2, countCalls(calls, "invoke:a"), "bileşik bütün olarak yeniden denenir")
	assert.Equal(t, 1, countCalls(calls, "compensate:a"), "ilk denemede başaran dal geri alınmış olmalı")
}

// TestParallelValidation geçersiz bileşik tanımlarının reddedildiğini doğrular.
func TestParallelValidation(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	tests := []struct {
		name string
		par  *workflow.ParallelStep
	}{
		{"dalsız", workflow.NewParallel("boş")},
		{"nil dal", workflow.NewParallel("nil", nil)},
		{"adsız dal", workflow.NewParallel("adsız", step(rec, ""))},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := eng.Run(t.Context(),
				workflow.Workflow{Name: "geçersiz", Steps: []workflow.Step{tc.par}}, nil)
			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
		})
	}
}

// TestParallelNameIsUsedInRecords bileşiğin kayıtta TEK adım göründüğünü doğrular.
func TestParallelNameIsUsedInRecords(t *testing.T) {
	rec := &recorder{}

	var execID string
	a := step(rec, "a")
	a.onInvoke = captureExecutionID(&execID)
	b := step(rec, "b")

	par := workflow.NewParallel("çift", a, b).WithLogger(testLogger())
	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "tek_kayıt", Steps: []workflow.Step{par}}, nil)
	require.NoError(t, err)

	exec, err := store.Get(t.Context(), execID)
	require.NoError(t, err)
	require.Len(t, exec.Steps, 1, "bileşik kayıtta tek satır olmalı")
	assert.Equal(t, "çift", exec.Steps[0].Name)
	assert.Equal(t, workflow.StepInvoked, exec.Steps[0].Status)
}

// countCalls bir çağrının kaç kez kaydedildiğini sayar.
func countCalls(calls []string, want string) int {
	n := 0
	for _, c := range calls {
		if c == want {
			n++
		}
	}
	return n
}

// TestParallelCompensationDoesNotStarveSiblings yavaş bir dalın kardeşlerini
// ölü bağlamla bırakmadığını doğrular.
//
// Regresyon: dal telafileri SIRAYLA yürütülüyor ve her biri ortak ebeveyn
// bağlamından türetiliyordu. Sırası önce gelen yavaş bir dal motorun telafi
// bütçesini tüketince, sırası sonra gelen dallar context.DeadlineExceeded
// taşıyan bir bağlamla çağrılıyordu — yani en erken kaydedilen (ve tipik olarak
// en ağır kaynağı tutan) dalların telafisi sessizce başarısız oluyordu.
// Telafiler artık eşzamanlı yürüdüğü için her dal bütçesini AYNI ANDA alır.
func TestParallelCompensationDoesNotStarveSiblings(t *testing.T) {
	rec := &recorder{}

	var (
		mu      sync.Mutex
		ctxErrs = map[string]error{}
	)
	kaydet := func(ad string) func(context.Context, *workflow.StepContext) error {
		return func(ctx context.Context, _ *workflow.StepContext) error {
			mu.Lock()
			ctxErrs[ad] = ctx.Err()
			mu.Unlock()
			return nil
		}
	}

	erken := step(rec, "erken")
	erken.onCompensate = kaydet("erken")

	yavas := step(rec, "yavas")
	yavas.onCompensate = func(ctx context.Context, sc *workflow.StepContext) error {
		// Motorun telafi bütçesinden uzun sürer.
		select {
		case <-time.After(400 * time.Millisecond):
		case <-ctx.Done():
		}
		return kaydet("yavas")(ctx, sc)
	}

	par := workflow.NewParallel("çift", erken, yavas).WithLogger(testLogger())

	son := step(rec, "son")
	son.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Internal("son", "son adım patladı")
	}

	eng := workflow.New(workflow.NewMemoryStore(), testLogger())
	_, err := eng.Run(t.Context(),
		workflow.Workflow{Name: "aclik", Steps: []workflow.Step{par, son}}, nil,
		workflow.WithCompensationTimeout(150*time.Millisecond))
	require.Error(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Contains(t, ctxErrs, "erken", "erken dalın telafisi hiç çağrılmadı")
	assert.NoError(t, ctxErrs["erken"],
		"erken dal, yavaş kardeşi yüzünden ölü bağlamla çağrılmamalı")
}
