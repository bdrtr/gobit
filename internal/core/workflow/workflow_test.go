package workflow_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
)

// --- test yardımcıları ------------------------------------------------------

// testLogger testleri kirletmeyen bir log üretir.
//
// Panik testleri yığın izi loglar; bunlar test çıktısında gürültüdür.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// recorder adım çağrılarını GELİŞ SIRASINDA kaydeder.
//
// Telafi sırasının ters olduğunu kanıtlayan şey budur: sıra bir kümede değil,
// dizide tutulur ve testler tam eşitlik arar.
type recorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *recorder) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, s)
}

func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	copy(out, r.calls)
	return out
}

// testStep yapılandırılabilir sahte adımdır.
type testStep struct {
	name         string
	rec          *recorder
	onInvoke     func(ctx context.Context, sc *workflow.StepContext) (any, error)
	onCompensate func(ctx context.Context, sc *workflow.StepContext) error
}

func (s *testStep) Name() string { return s.name }

func (s *testStep) Invoke(ctx context.Context, sc *workflow.StepContext) (any, error) {
	s.rec.add("invoke:" + s.name)
	if s.onInvoke != nil {
		return s.onInvoke(ctx, sc)
	}
	return s.name + "-out", nil
}

func (s *testStep) Compensate(ctx context.Context, sc *workflow.StepContext) error {
	s.rec.add("compensate:" + s.name)
	if s.onCompensate != nil {
		return s.onCompensate(ctx, sc)
	}
	return nil
}

// rawOutput Run'ın çıktısını ham JSON metni olarak döner.
//
// Çıktının tipi hem mutlu yolda hem tekrarda json.RawMessage'dır; yardımcı bu
// sözleşmeyi her çağrı yerinde yeniden doğrular.
func rawOutput(t *testing.T, out any) string {
	t.Helper()

	raw, ok := out.(json.RawMessage)
	require.True(t, ok, "Run çıktısı json.RawMessage olmalı, %T geldi", out)
	return string(raw)
}

// step varsayılan davranışlı (her zaman başarılı) bir adım üretir.
func step(rec *recorder, name string) *testStep {
	return &testStep{name: name, rec: rec}
}

// steps testStep dilimini workflow.Step dilimine çevirir.
func steps(list ...*testStep) []workflow.Step {
	out := make([]workflow.Step, len(list))
	for i, s := range list {
		out[i] = s
	}
	return out
}

// captureExecutionID adımın gördüğü yürütme kimliğini dışarı taşır.
func captureExecutionID(dst *string) func(context.Context, *workflow.StepContext) (any, error) {
	return func(_ context.Context, sc *workflow.StepContext) (any, error) {
		*dst = sc.ExecutionID
		return sc.StepName + "-out", nil
	}
}

// --- telafi (saga) çekirdeği ------------------------------------------------

// TestCompensationReverseOrderSkipsFailedStep Faz 3 DoD'sinin birebir karşılığıdır.
func TestCompensationReverseOrderSkipsFailedStep(t *testing.T) {
	rec := &recorder{}
	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	boom := errors.New("2. adım patladı")

	var execID string
	first := step(rec, "first")
	first.onInvoke = captureExecutionID(&execID)

	second := step(rec, "second")
	second.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Wrap(boom, errors.KindUnavailable, "second_failed", "ikinci adım")
	}

	third := step(rec, "third")

	wf := workflow.Workflow{Name: "üç_adım", Steps: steps(first, second, third)}

	out, err := eng.Run(t.Context(), wf, map[string]any{"x": 1})

	require.Error(t, err)
	assert.Nil(t, out)
	assert.ErrorIs(t, err, boom, "dönen hata patlayan adımın hatasını taşımalı")

	// DoD: telafi TERS SIRADA ve YALNIZCA başarılı adımlar için.
	assert.Equal(t, []string{
		"invoke:first",
		"invoke:second",
		"compensate:first",
	}, rec.snapshot())

	calls := rec.snapshot()
	assert.NotContains(t, calls, "invoke:third", "patlayan adımdan sonraki adım hiç çalışmamış olmalı")
	assert.NotContains(t, calls, "compensate:second", "patlayan adımın KENDİSİ telafi edilmemeli")

	// Telafi eksiksiz tamamlandığı için durum failed (compensation_failed değil).
	exec, gerr := store.Get(t.Context(), execID)
	require.NoError(t, gerr)
	assert.Equal(t, workflow.StatusFailed, exec.Status)
	assert.NotEmpty(t, exec.Failure)

	require.Len(t, exec.Steps, 2, "hiç çalışmayan adım kayda geçmemeli")
	assert.Equal(t, workflow.StepCompensated, exec.Steps[0].Status)
	assert.Equal(t, 0, exec.Steps[0].Index)
	assert.Equal(t, workflow.StepFailed, exec.Steps[1].Status)
	assert.Equal(t, 1, exec.Steps[1].Index)
	assert.NotEmpty(t, exec.Steps[1].Failure)
}

// TestCompensationOrderWithFiveSteps 4. adım patladığında sıranın 3,2,1 olduğunu doğrular.
func TestCompensationOrderWithFiveSteps(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	s1, s2, s3 := step(rec, "1"), step(rec, "2"), step(rec, "3")
	s4 := step(rec, "4")
	s4.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Invalid("dorduncu", "dördüncü adım patladı")
	}
	s5 := step(rec, "5")

	wf := workflow.Workflow{Name: "beş_adım", Steps: steps(s1, s2, s3, s4, s5)}

	_, err := eng.Run(t.Context(), wf, nil)
	require.Error(t, err)

	assert.Equal(t, []string{
		"invoke:1", "invoke:2", "invoke:3", "invoke:4",
		"compensate:3", "compensate:2", "compensate:1",
	}, rec.snapshot())
}

// TestSuccessfulRunPersistsCompletedState başarılı koşunun kalıcı izini doğrular.
func TestSuccessfulRunPersistsCompletedState(t *testing.T) {
	rec := &recorder{}
	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	var execID string
	a := step(rec, "a")
	a.onInvoke = captureExecutionID(&execID)
	b, c := step(rec, "b"), step(rec, "c")

	wf := workflow.Workflow{Name: "başarılı", Steps: steps(a, b, c)}

	out, err := eng.Run(t.Context(), wf, map[string]any{"girdi": "değer"})
	require.NoError(t, err)
	assert.JSONEq(t, `"c-out"`, rawOutput(t, out), "workflow çıktısı SON adımın çıktısıdır")

	assert.Equal(t, []string{"invoke:a", "invoke:b", "invoke:c"}, rec.snapshot())

	exec, err := store.Get(t.Context(), execID)
	require.NoError(t, err)

	assert.Equal(t, workflow.StatusCompleted, exec.Status)
	assert.Equal(t, "başarılı", exec.Workflow)
	assert.Empty(t, exec.Failure)
	assert.JSONEq(t, `{"girdi":"değer"}`, string(exec.Input))
	assert.JSONEq(t, `"c-out"`, string(exec.Output))

	require.Len(t, exec.Steps, 3)
	for i, want := range []string{"a", "b", "c"} {
		assert.Equal(t, i, exec.Steps[i].Index, "adım kayıtları SIRAYLA olmalı")
		assert.Equal(t, want, exec.Steps[i].Name)
		assert.Equal(t, workflow.StepInvoked, exec.Steps[i].Status)
		assert.Equal(t, 1, exec.Steps[i].Attempts)
		assert.JSONEq(t, `"`+want+`-out"`, string(exec.Steps[i].Output))
		assert.False(t, exec.Steps[i].StartedAt.IsZero())
		assert.False(t, exec.Steps[i].EndedAt.IsZero())
	}
}

// TestCompensationFailureContinuesChain telafi hatasının zinciri durdurmadığını doğrular.
func TestCompensationFailureContinuesChain(t *testing.T) {
	rec := &recorder{}
	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	invokeBoom := errors.New("üçüncü adım patladı")
	compBoom := errors.New("ikinci adımın telafisi patladı")

	var execID string
	a := step(rec, "a")
	a.onInvoke = captureExecutionID(&execID)

	b := step(rec, "b")
	b.onCompensate = func(context.Context, *workflow.StepContext) error {
		return errors.Wrap(compBoom, errors.KindInternal, "b_comp", "b telafisi")
	}

	c := step(rec, "c")
	c.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Wrap(invokeBoom, errors.KindInvalid, "c_invoke", "c adımı")
	}

	wf := workflow.Workflow{Name: "telafi_patlar", Steps: steps(a, b, c)}

	_, err := eng.Run(t.Context(), wf, nil)
	require.Error(t, err)

	// Zincir b'de patladı ama a'nın telafisi YİNE DE çalıştı.
	assert.Equal(t, []string{
		"invoke:a", "invoke:b", "invoke:c",
		"compensate:b", "compensate:a",
	}, rec.snapshot())

	assert.ErrorIs(t, err, invokeBoom, "hata adım hatasını içermeli")
	assert.ErrorIs(t, err, compBoom, "hata telafi hatasını da içermeli")
	assert.Equal(t, errors.KindInternal, errors.KindOf(err), "elle müdahale gereken durum internal olmalı")
	assert.Equal(t, workflow.CodeCompensationFailed, errors.CodeOf(err))

	exec, gerr := store.Get(t.Context(), execID)
	require.NoError(t, gerr)
	assert.Equal(t, workflow.StatusCompensationFailed, exec.Status)

	require.Len(t, exec.Steps, 3)
	assert.Equal(t, workflow.StepCompensated, exec.Steps[0].Status, "a telafi edilmiş olmalı")
	assert.Equal(t, workflow.StepCompensationFailed, exec.Steps[1].Status)
	assert.Equal(t, workflow.StepFailed, exec.Steps[2].Status)
}

// TestCompensationErrorDoesNotMaskStepError telafi hatasının adım hatasının
// yerine geçmediğini doğrular; ikisi de dönen hatada durur.
func TestCompensationErrorDoesNotMaskStepError(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	a := step(rec, "a")
	a.onCompensate = func(context.Context, *workflow.StepContext) error {
		return errors.Internal("a_comp", "a telafisi patladı")
	}
	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Conflict("b_invoke", "b adımı çakıştı")
	}

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "maskeleme", Steps: steps(a, b)}, nil)
	require.Error(t, err)

	msg := err.Error()
	assert.Contains(t, msg, "b adımı çakıştı")
	assert.Contains(t, msg, "a telafisi patladı")
}

// --- yeniden deneme ---------------------------------------------------------

// TestRetrySucceedsAfterTransientFailures geçici hatanın N denemede geçtiğini doğrular.
func TestRetrySucceedsAfterTransientFailures(t *testing.T) {
	rec := &recorder{}
	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	var attempts []int
	var execID string
	flaky := step(rec, "flaky")
	flaky.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		execID = sc.ExecutionID
		attempts = append(attempts, sc.Attempt)
		if sc.Attempt < 3 {
			return nil, errors.Unavailable("gecici", "%d. denemede alt sistem kapalı", sc.Attempt)
		}
		return "sonunda", nil
	}

	out, err := eng.Run(t.Context(), workflow.Workflow{Name: "yeniden_dene", Steps: steps(flaky)}, nil,
		workflow.WithRetry(workflow.RetryPolicy{MaxAttempts: 5, Backoff: time.Millisecond}))

	require.NoError(t, err)
	assert.JSONEq(t, `"sonunda"`, rawOutput(t, out))
	assert.Equal(t, []int{1, 2, 3}, attempts, "StepContext.Attempt 1'den başlayıp artmalı")
	assert.Len(t, rec.snapshot(), 3, "adım üç kez çağrılmalı")

	exec, gerr := store.Get(t.Context(), execID)
	require.NoError(t, gerr)
	require.Len(t, exec.Steps, 1)
	assert.Equal(t, 3, exec.Steps[0].Attempts, "deneme sayısı kayda geçmeli")
	assert.Equal(t, workflow.StepInvoked, exec.Steps[0].Status)
}

// TestRetrySkippedForNonRetryableKind Invalid sınıfının hemen düştüğünü doğrular.
func TestRetrySkippedForNonRetryableKind(t *testing.T) {
	rec := &recorder{}
	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	var execID string
	bad := step(rec, "bad")
	bad.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		execID = sc.ExecutionID
		return nil, errors.Invalid("gecersiz_girdi", "girdi geçersiz")
	}

	start := time.Now()
	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "denenmez", Steps: steps(bad)}, nil,
		workflow.WithRetry(workflow.RetryPolicy{MaxAttempts: 5, Backoff: 2 * time.Second}))
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Len(t, rec.snapshot(), 1, "Invalid yeniden DENENMEMELİ")
	assert.Less(t, elapsed, time.Second, "geri çekilme hiç beklenmemeli")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "hata sınıfı korunmalı")

	exec, gerr := store.Get(t.Context(), execID)
	require.NoError(t, gerr)
	require.Len(t, exec.Steps, 1)
	assert.Equal(t, 1, exec.Steps[0].Attempts)
}

// TestDefaultPolicyDoesNotRetry varsayılanın tek deneme olduğunu doğrular.
func TestDefaultPolicyDoesNotRetry(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	flaky := step(rec, "flaky")
	flaky.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		return nil, errors.Unavailable("gecici", "%d. deneme", sc.Attempt)
	}

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "varsayılan", Steps: steps(flaky)}, nil)
	require.Error(t, err)
	assert.Len(t, rec.snapshot(), 1, "WithRetry verilmedikçe yeniden deneme olmamalı")
}

// TestCompensationIsRetried telafinin de yeniden denendiğini doğrular.
func TestCompensationIsRetried(t *testing.T) {
	rec := &recorder{}
	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	var execID string
	a := step(rec, "a")
	a.onInvoke = captureExecutionID(&execID)
	compCalls := 0
	a.onCompensate = func(_ context.Context, sc *workflow.StepContext) error {
		compCalls++
		if sc.Attempt < 2 {
			return errors.Unavailable("gecici_telafi", "telafi geçici olarak başarısız")
		}
		return nil
	}

	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Internal("b_patladi", "b patladı")
	}

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "telafi_denemesi", Steps: steps(a, b)}, nil,
		workflow.WithCompensationRetry(workflow.RetryPolicy{MaxAttempts: 3, Backoff: time.Millisecond}))
	require.Error(t, err)

	assert.Equal(t, 2, compCalls, "telafi ikinci denemede başarılı olmalı")

	exec, gerr := store.Get(t.Context(), execID)
	require.NoError(t, gerr)
	assert.Equal(t, workflow.StatusFailed, exec.Status, "telafi sonunda başarılı olduğu için durum failed")
	require.Len(t, exec.Steps, 2)
	assert.Equal(t, workflow.StepCompensated, exec.Steps[0].Status)
	assert.Equal(t, 1, exec.Steps[0].Attempts,
		"Attempts INVOKE denemesidir; telafi kaydı onu ezmez, telafi denemeleri loglanır")
	assert.JSONEq(t, `"a-out"`, string(exec.Steps[0].Output),
		"telafi kaydı Invoke'un çıktısını da korumalı")
}

// TestDefaultRetryable sınıflandırma kararını doğrudan sınar.
func TestDefaultRetryable(t *testing.T) {
	assert.False(t, workflow.DefaultRetryable(nil))
	assert.True(t, workflow.DefaultRetryable(errors.Unavailable("x", "geçici")))
	assert.True(t, workflow.DefaultRetryable(errors.Internal("x", "beklenmeyen")))
	assert.True(t, workflow.DefaultRetryable(errors.New("sınıflandırılmamış")))
	assert.False(t, workflow.DefaultRetryable(errors.Invalid("x", "girdi")))
	assert.False(t, workflow.DefaultRetryable(errors.Conflict("x", "çakışma")))
	assert.False(t, workflow.DefaultRetryable(errors.NotFound("x", "yok")))
	assert.False(t, workflow.DefaultRetryable(errors.Unauthorized("x", "yetkisiz")))
	assert.False(t, workflow.DefaultRetryable(errors.Forbidden("x", "yasak")))
	assert.False(t, workflow.DefaultRetryable(context.Canceled))
	assert.False(t, workflow.DefaultRetryable(context.DeadlineExceeded))
	assert.False(t, workflow.DefaultRetryable(workflow.ErrPanic), "panik yeniden denenmemeli")
}

// TestCustomRetryablePredicate özel sınıflandırmanın kullanıldığını doğrular.
func TestCustomRetryablePredicate(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	s := step(rec, "s")
	s.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		if sc.Attempt < 2 {
			// Varsayılanın DENEMEYECEĞİ sınıf.
			return nil, errors.Invalid("gecersiz", "geçersiz")
		}
		return "tamam", nil
	}

	out, err := eng.Run(t.Context(), workflow.Workflow{Name: "özel", Steps: steps(s)}, nil,
		workflow.WithRetry(workflow.RetryPolicy{
			MaxAttempts: 3,
			Retryable:   func(error) bool { return true },
		}))

	require.NoError(t, err)
	assert.JSONEq(t, `"tamam"`, rawOutput(t, out))
	assert.Len(t, rec.snapshot(), 2)
}

// --- idempotency ------------------------------------------------------------

// TestIdempotencyReplayDoesNotRerunSteps aynı anahtarla ikinci çağrının
// adımları tekrar çalıştırmadığını doğrular.
func TestIdempotencyReplayDoesNotRerunSteps(t *testing.T) {
	rec := &recorder{}
	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	wf := workflow.Workflow{Name: "idem", Steps: steps(step(rec, "a"), step(rec, "b"))}

	first, err := eng.Run(t.Context(), wf, map[string]any{"n": 1}, workflow.WithIdempotencyKey("anahtar-1"))
	require.NoError(t, err)
	assert.JSONEq(t, `"b-out"`, rawOutput(t, first))
	assert.Equal(t, []string{"invoke:a", "invoke:b"}, rec.snapshot())

	second, err := eng.Run(t.Context(), wf, map[string]any{"n": 1}, workflow.WithIdempotencyKey("anahtar-1"))
	require.NoError(t, err)

	assert.Equal(t, []string{"invoke:a", "invoke:b"}, rec.snapshot(),
		"ikinci çağrı hiçbir adımı TEKRAR çalıştırmamalı")

	assert.JSONEq(t, `"b-out"`, rawOutput(t, second),
		"tekrar çağrısı da mutlu yolla AYNI tipi döner")
}

// TestIdempotencyDifferentKeyRunsAgain farklı anahtarın yeni yürütme açtığını doğrular.
func TestIdempotencyDifferentKeyRunsAgain(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())
	wf := workflow.Workflow{Name: "idem", Steps: steps(step(rec, "a"))}

	_, err := eng.Run(t.Context(), wf, nil, workflow.WithIdempotencyKey("k1"))
	require.NoError(t, err)
	_, err = eng.Run(t.Context(), wf, nil, workflow.WithIdempotencyKey("k2"))
	require.NoError(t, err)

	assert.Equal(t, []string{"invoke:a", "invoke:a"}, rec.snapshot())
}

// TestIdempotencyWithoutKeyAlwaysRuns anahtarsız çağrıların çakışmadığını doğrular.
func TestIdempotencyWithoutKeyAlwaysRuns(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())
	wf := workflow.Workflow{Name: "anahtarsız", Steps: steps(step(rec, "a"))}

	for range 3 {
		_, err := eng.Run(t.Context(), wf, nil)
		require.NoError(t, err)
	}
	assert.Len(t, rec.snapshot(), 3)
}

// TestIdempotencyNonCompletedStates tamamlanmamış yürütmelerin tekrar
// çağrısında ne döndüğünü doğrular.
func TestIdempotencyNonCompletedStates(t *testing.T) {
	tests := []struct {
		name     string
		status   workflow.Status
		wantCode string
	}{
		{"running", workflow.StatusRunning, workflow.CodeExecutionRunning},
		{"failed", workflow.StatusFailed, workflow.CodeExecutionFailed},
		{"compensation_failed", workflow.StatusCompensationFailed, workflow.CodeExecutionFailed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recorder{}
			store := workflow.NewMemoryStore()
			eng := workflow.New(store, testLogger())

			prev := &workflow.Execution{
				ID:             "wfx_ONCEKI",
				Workflow:       "durum",
				IdempotencyKey: "k",
				Status:         tc.status,
				Failure:        "önceki hata",
			}
			require.NoError(t, store.Create(t.Context(), prev))

			wf := workflow.Workflow{Name: "durum", Steps: steps(step(rec, "a"))}
			_, err := eng.Run(t.Context(), wf, nil, workflow.WithIdempotencyKey("k"))

			require.Error(t, err)
			assert.Equal(t, errors.KindConflict, errors.KindOf(err))
			assert.Equal(t, tc.wantCode, errors.CodeOf(err))
			assert.Empty(t, rec.snapshot(), "hiçbir adım çalıştırılmamalı")
		})
	}
}

// --- bağlam iptali ----------------------------------------------------------

// TestContextCancellationStillCompensates iptalden sonra telafinin çalıştığını doğrular.
func TestContextCancellationStillCompensates(t *testing.T) {
	rec := &recorder{}
	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var execID string
	var compCtxErr error
	var compHasDeadline bool

	a := step(rec, "a")
	a.onInvoke = captureExecutionID(&execID)
	a.onCompensate = func(cctx context.Context, _ *workflow.StepContext) error {
		compCtxErr = cctx.Err()
		_, compHasDeadline = cctx.Deadline()
		return nil
	}

	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		// Adım başarılı olur ama bağlamı öldürür: motor bir SONRAKİ adıma
		// geçmeden iptali görmeli.
		cancel()
		return "b-out", nil
	}

	c := step(rec, "c")

	wf := workflow.Workflow{Name: "iptal", Steps: steps(a, b, c)}
	_, err := eng.Run(ctx, wf, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, workflow.CodeCanceled, errors.CodeOf(err))

	assert.Equal(t, []string{
		"invoke:a", "invoke:b",
		"compensate:b", "compensate:a",
	}, rec.snapshot(), "iptal edilen yürütmede telafi YİNE DE ters sırada çalışmalı")

	assert.NoError(t, compCtxErr, "telafi CANLI bir bağlamla çalışmalı (context.WithoutCancel)")
	assert.True(t, compHasDeadline, "telafi bağlamının kendi süre bütçesi olmalı")

	exec, gerr := store.Get(t.Context(), execID)
	require.NoError(t, gerr)
	// Not: MemoryStore bağlamı yok saydığı için buradaki kalıcılaştırma
	// iddiası zayıftır; bağlamı gerçekten kullanan bir Store ile olan sınama
	// TestCancelledRunStillPersistsState'tedir.
	assert.Equal(t, workflow.StatusFailed, exec.Status)
	require.Len(t, exec.Steps, 2)
	assert.Equal(t, workflow.StepCompensated, exec.Steps[0].Status)
	assert.Equal(t, workflow.StepCompensated, exec.Steps[1].Status)
}

// ctxAwareStore bağlamı gerçekten kullanan bir Store'u taklit eder.
//
// MemoryStore süreç içi bir haritadır ve bağlamı yok sayar; bu yüzden onunla
// "iptal edilmiş bağlamda yazma" özelliği SINANAMAZ (iptal hiçbir şeyi
// bozmadığı için test mutasyona rağmen geçer). Gerçek bir veritabanı sürücüsü
// ölü bir bağlamla hata döner — bu sarmalayıcı o davranışı verir.
type ctxAwareStore struct {
	workflow.Store
}

func (s *ctxAwareStore) AppendStep(ctx context.Context, id string, rec workflow.StepRecord) error {
	if err := ctx.Err(); err != nil {
		return errors.Wrap(err, errors.KindUnavailable, "ctx_olu", "bağlam ölü")
	}
	return s.Store.AppendStep(ctx, id, rec)
}

func (s *ctxAwareStore) UpdateStatus(ctx context.Context, id string, st workflow.Status, out json.RawMessage, failure string) error {
	if err := ctx.Err(); err != nil {
		return errors.Wrap(err, errors.KindUnavailable, "ctx_olu", "bağlam ölü")
	}
	return s.Store.UpdateStatus(ctx, id, st, out, failure)
}

// TestCancelledRunStillPersistsState iptal edilmiş bir yürütmenin izinin YİNE DE
// yazıldığını doğrular.
//
// Store yazmaları çağıranın bağlamına bağlansaydı, iz tam da en çok gerektiği
// anda (iptal edilmiş, telafi edilmiş yürütme) yazılamazdı.
func TestCancelledRunStillPersistsState(t *testing.T) {
	rec := &recorder{}
	store := &ctxAwareStore{Store: workflow.NewMemoryStore()}
	eng := workflow.New(store, testLogger())

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var execID string
	a := step(rec, "a")
	a.onInvoke = captureExecutionID(&execID)

	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		cancel()
		return "b-out", nil
	}

	_, err := eng.Run(ctx, workflow.Workflow{Name: "iptal_iz", Steps: steps(a, b, step(rec, "c"))}, nil)
	require.Error(t, err)

	exec, gerr := store.Get(t.Context(), execID)
	require.NoError(t, gerr)
	assert.Equal(t, workflow.StatusFailed, exec.Status,
		"iptal edilmiş bağlamda bile uç durum yazılmalı (context.WithoutCancel)")
	require.Len(t, exec.Steps, 2)
	assert.Equal(t, workflow.StepCompensated, exec.Steps[0].Status,
		"telafi kayıtları da iptalden etkilenmemeli")
	assert.Equal(t, workflow.StepCompensated, exec.Steps[1].Status)
}

// TestContextCancelledBeforeFirstStep hiçbir adım çalışmadan iptali doğrular.
//
// Bağlam çağrı anında ölü olduğu için motor kaydı hiç açmaz; anahtarın
// yakılmadığını ayrıca TestCanceledContextDoesNotBurnIdempotencyKey doğrular.
func TestContextCancelledBeforeFirstStep(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	wf := workflow.Workflow{Name: "erken_iptal", Steps: steps(step(rec, "a"))}
	_, err := eng.Run(ctx, wf, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, rec.snapshot())
}

// --- panik ------------------------------------------------------------------

// TestStepPanicIsRecoveredAndCompensated adım paniğinin motoru çökertmediğini doğrular.
func TestStepPanicIsRecoveredAndCompensated(t *testing.T) {
	rec := &recorder{}
	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	var execID string
	a := step(rec, "a")
	a.onInvoke = captureExecutionID(&execID)

	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		panic("b adımı patladı")
	}

	c := step(rec, "c")

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "panik", Steps: steps(a, b, c)}, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, workflow.ErrPanic, "panik tipli hataya çevrilmeli")
	assert.Equal(t, errors.KindInternal, errors.KindOf(err))
	assert.Contains(t, err.Error(), "b adımı patladı", "panik değeri hata metnine taşınmalı")

	assert.Equal(t, []string{"invoke:a", "invoke:b", "compensate:a"}, rec.snapshot())

	exec, gerr := store.Get(t.Context(), execID)
	require.NoError(t, gerr)
	assert.Equal(t, workflow.StatusFailed, exec.Status)
}

// TestPanicIsNotRetried paniğin yeniden denenmediğini doğrular.
func TestPanicIsNotRetried(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	boom := step(rec, "boom")
	boom.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		panic(errors.New("hep aynı panik"))
	}

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "panik_denenmez", Steps: steps(boom)}, nil,
		workflow.WithRetry(workflow.RetryPolicy{MaxAttempts: 4, Backoff: time.Millisecond}))

	require.Error(t, err)
	assert.ErrorIs(t, err, workflow.ErrPanic)
	assert.Len(t, rec.snapshot(), 1, "panik yeniden DENENMEMELİ")
}

// TestCompensatePanicIsRecovered telafi paniğinin zinciri sürdürdüğünü doğrular.
func TestCompensatePanicIsRecovered(t *testing.T) {
	rec := &recorder{}
	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	var execID string
	a := step(rec, "a")
	a.onInvoke = captureExecutionID(&execID)

	b := step(rec, "b")
	b.onCompensate = func(context.Context, *workflow.StepContext) error {
		panic("telafi patladı")
	}

	c := step(rec, "c")
	c.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Internal("c_patladi", "c patladı")
	}

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "telafi_panik", Steps: steps(a, b, c)}, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, workflow.ErrPanic)
	assert.Equal(t, []string{
		"invoke:a", "invoke:b", "invoke:c",
		"compensate:b", "compensate:a",
	}, rec.snapshot(), "telafi paniği kalan telafileri durdurmamalı")

	exec, gerr := store.Get(t.Context(), execID)
	require.NoError(t, gerr)
	assert.Equal(t, workflow.StatusCompensationFailed, exec.Status)
}

// --- Shared verisi ----------------------------------------------------------

// TestSharedDataFlowsBetweenStepsAndCompensation Shared'ın hem adımlar arasında
// hem telafide erişilebilir olduğunu doğrular.
func TestSharedDataFlowsBetweenStepsAndCompensation(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	var seenByB any
	var seenInCompensateA any
	var seenInCompensateB any
	var inputInStep any

	a := step(rec, "a")
	a.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		inputInStep = sc.Input
		sc.Shared["rezervasyon"] = "rez_1"
		return nil, nil
	}
	a.onCompensate = func(_ context.Context, sc *workflow.StepContext) error {
		seenInCompensateA = sc.Shared["rezervasyon"]
		return nil
	}

	b := step(rec, "b")
	b.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		seenByB = sc.Shared["rezervasyon"]
		sc.Shared["ödeme"] = "pay_1"
		return nil, nil
	}
	b.onCompensate = func(_ context.Context, sc *workflow.StepContext) error {
		seenInCompensateB = sc.Shared["ödeme"]
		return nil
	}

	c := step(rec, "c")
	c.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Internal("c", "c patladı")
	}

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "paylaşım", Steps: steps(a, b, c)},
		map[string]any{"sepet": "cart_1"})
	require.Error(t, err)

	assert.Equal(t, "rez_1", seenByB, "sonraki adım önceki adımın yazdığını görmeli")
	assert.Equal(t, "rez_1", seenInCompensateA, "telafi kendi Invoke'unun yazdığını görmeli")
	assert.Equal(t, "pay_1", seenInCompensateB)
	assert.Equal(t, map[string]any{"sepet": "cart_1"}, inputInStep, "girdi adımlara geçmeli")
}

// --- doğrulama --------------------------------------------------------------

// TestRunValidatesWorkflow geçersiz workflow tanımlarının reddedildiğini doğrular.
func TestRunValidatesWorkflow(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	tests := []struct {
		name string
		wf   workflow.Workflow
	}{
		{"adsız", workflow.Workflow{Steps: steps(step(rec, "a"))}},
		{"adımsız", workflow.Workflow{Name: "boş"}},
		{"nil adım", workflow.Workflow{Name: "nil", Steps: []workflow.Step{nil}}},
		{"adsız adım", workflow.Workflow{Name: "adsız_adım", Steps: steps(step(rec, ""))}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := eng.Run(t.Context(), tc.wf, nil)
			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
			assert.Equal(t, workflow.CodeInvalidWorkflow, errors.CodeOf(err))
		})
	}
	assert.Empty(t, rec.snapshot())
}

// TestRunValidatesOptions geçersiz seçeneklerin reddedildiğini doğrular.
func TestRunValidatesOptions(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())
	wf := workflow.Workflow{Name: "seçenek", Steps: steps(step(rec, "a"))}

	tests := []struct {
		name string
		opt  workflow.RunOption
	}{
		{"boş anahtar", workflow.WithIdempotencyKey("")},
		{"sıfır deneme", workflow.WithRetry(workflow.RetryPolicy{MaxAttempts: 0})},
		{"negatif geri çekilme", workflow.WithRetry(workflow.RetryPolicy{MaxAttempts: 2, Backoff: -time.Second})},
		{"negatif üst sınır", workflow.WithRetry(workflow.RetryPolicy{MaxAttempts: 2, MaxBackoff: -time.Second})},
		{"negatif çarpan", workflow.WithRetry(workflow.RetryPolicy{MaxAttempts: 2, Multiplier: -1})},
		{"geçersiz telafi politikası", workflow.WithCompensationRetry(workflow.RetryPolicy{MaxAttempts: -1})},
		{"sıfır telafi süresi", workflow.WithCompensationTimeout(0)},
		{"negatif store süresi", workflow.WithStoreTimeout(-time.Second)},
		{"nil seçenek", nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := eng.Run(t.Context(), wf, nil, tc.opt)
			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
			assert.Equal(t, workflow.CodeInvalidOption, errors.CodeOf(err))
		})
	}
	assert.Empty(t, rec.snapshot(), "seçenek geçersizken hiçbir adım çalışmamış olmalı")
}

// TestRunRejectsUnserializableInput JSON'a çevrilemeyen girdinin yürütmeyi
// HİÇ başlatmadığını doğrular.
func TestRunRejectsUnserializableInput(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())
	wf := workflow.Workflow{Name: "serileşmez", Steps: steps(step(rec, "a"))}

	_, err := eng.Run(t.Context(), wf, make(chan int))

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Empty(t, rec.snapshot(), "girdi kalıcılaştırılamıyorsa hiçbir adım çalışmamış olmalı")
}

// TestUnserializableStepOutputDoesNotFailRun çıktısı serileşmeyen adımın
// yürütmeyi düşürmediğini doğrular.
func TestUnserializableStepOutputDoesNotFailRun(t *testing.T) {
	rec := &recorder{}
	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	var execID string
	a := step(rec, "a")
	a.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		execID = sc.ExecutionID
		return make(chan int), nil
	}

	out, err := eng.Run(t.Context(), workflow.Workflow{Name: "çıktı_serileşmez", Steps: steps(a)}, nil)
	require.NoError(t, err, "yan etki uygulandıktan sonra serileştirme hatası yürütmeyi düşürmemeli")
	assert.Empty(t, rawOutput(t, out), "çevrilemeyen çıktı boş RawMessage olarak döner")

	exec, gerr := store.Get(t.Context(), execID)
	require.NoError(t, gerr)
	assert.Equal(t, workflow.StatusCompleted, exec.Status)
	require.Len(t, exec.Steps, 1)
	assert.Equal(t, workflow.StepInvoked, exec.Steps[0].Status)
	assert.Empty(t, exec.Steps[0].Output)
	assert.Contains(t, exec.Steps[0].Failure, "JSON")
}

// --- Store hataları ---------------------------------------------------------

// brokenStore belirli çağrılarda hata dönen sarmalayıcı Store'dur.
type brokenStore struct {
	workflow.Store
	createErr error
	findErr   error
	appendErr error
	updateErr error

	mu          sync.Mutex
	appendCalls int
	updateCalls int
}

func (s *brokenStore) Create(ctx context.Context, exec *workflow.Execution) error {
	if s.createErr != nil {
		return s.createErr
	}
	return s.Store.Create(ctx, exec)
}

func (s *brokenStore) FindByIdempotencyKey(ctx context.Context, wf, key string) (*workflow.Execution, error) {
	if s.findErr != nil {
		return nil, s.findErr
	}
	return s.Store.FindByIdempotencyKey(ctx, wf, key)
}

func (s *brokenStore) AppendStep(ctx context.Context, id string, rec workflow.StepRecord) error {
	s.mu.Lock()
	s.appendCalls++
	s.mu.Unlock()
	if s.appendErr != nil {
		return s.appendErr
	}
	return s.Store.AppendStep(ctx, id, rec)
}

func (s *brokenStore) UpdateStatus(ctx context.Context, id string, st workflow.Status, out json.RawMessage, failure string) error {
	s.mu.Lock()
	s.updateCalls++
	s.mu.Unlock()
	if s.updateErr != nil {
		return s.updateErr
	}
	return s.Store.UpdateStatus(ctx, id, st, out, failure)
}

// TestCreateFailureAbortsRun kayıt açılamazsa hiçbir adımın çalışmadığını doğrular.
func TestCreateFailureAbortsRun(t *testing.T) {
	rec := &recorder{}
	store := &brokenStore{Store: workflow.NewMemoryStore(), createErr: errors.Unavailable("db", "veritabanı kapalı")}
	eng := workflow.New(store, testLogger())

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "create_patlar", Steps: steps(step(rec, "a"))}, nil)

	require.Error(t, err)
	assert.Equal(t, workflow.CodeStoreFailed, errors.CodeOf(err))
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err), "alttaki hatanın sınıfı korunmalı")
	assert.Empty(t, rec.snapshot(), "tekrar koruması kurulamadan adım çalıştırılmamalı")
}

// TestFindFailureAbortsRun tekrar koruması okunamazsa adımların çalışmadığını doğrular.
func TestFindFailureAbortsRun(t *testing.T) {
	rec := &recorder{}
	inner := workflow.NewMemoryStore()
	require.NoError(t, inner.Create(t.Context(), &workflow.Execution{
		ID: "wfx_VAR", Workflow: "find_patlar", IdempotencyKey: "k", Status: workflow.StatusCompleted,
	}))

	store := &brokenStore{Store: inner, findErr: errors.Unavailable("db", "veritabanı kapalı")}
	eng := workflow.New(store, testLogger())

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "find_patlar", Steps: steps(step(rec, "a"))}, nil,
		workflow.WithIdempotencyKey("k"))

	require.Error(t, err)
	assert.Equal(t, workflow.CodeStoreFailed, errors.CodeOf(err))
	assert.Empty(t, rec.snapshot(), "önceki yürütmenin sonucu okunamadan adım tekrar çalıştırılmamalı")
}

// nilFindStore sözleşmeyi ihlal edip (nil, nil) dönen bir Store'dur.
type nilFindStore struct {
	workflow.Store
}

func (s *nilFindStore) Create(context.Context, *workflow.Execution) error {
	return errors.Conflict("var", "zaten var")
}

func (s *nilFindStore) FindByIdempotencyKey(context.Context, string, string) (*workflow.Execution, error) {
	return nil, nil
}

// TestReplayHandlesNilExecution sözleşmeyi ihlal eden Store'un motoru
// çökertmediğini doğrular; Store ayrı bir pakette yazıldığı için bu savunma
// gereklidir.
func TestReplayHandlesNilExecution(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(&nilFindStore{Store: workflow.NewMemoryStore()}, testLogger())

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "nil_kayıt", Steps: steps(step(rec, "a"))}, nil,
		workflow.WithIdempotencyKey("k"))

	require.Error(t, err)
	assert.Equal(t, workflow.CodeStoreFailed, errors.CodeOf(err))
	assert.Empty(t, rec.snapshot())
}

// TestStoreWriteFailuresDoNotAbortRun adım/durum yazma hatalarının yürütmeyi
// düşürmediğini doğrular.
func TestStoreWriteFailuresDoNotAbortRun(t *testing.T) {
	rec := &recorder{}
	store := &brokenStore{
		Store:     workflow.NewMemoryStore(),
		appendErr: errors.Unavailable("db", "yazılamadı"),
		updateErr: errors.Unavailable("db", "yazılamadı"),
	}
	eng := workflow.New(store, testLogger())

	out, err := eng.Run(t.Context(),
		workflow.Workflow{Name: "yazma_patlar", Steps: steps(step(rec, "a"), step(rec, "b"))}, nil)

	require.NoError(t, err, "defter tutulamadı diye başarılı iş akışı düşürülmemeli")
	assert.JSONEq(t, `"b-out"`, rawOutput(t, out))
	assert.Equal(t, []string{"invoke:a", "invoke:b"}, rec.snapshot())

	store.mu.Lock()
	defer store.mu.Unlock()
	assert.Equal(t, 2, store.appendCalls)
	assert.Equal(t, 1, store.updateCalls)
}

// TestStoreWriteFailureStillCompensates yazma hatası varken telafinin
// bellekteki duruma göre doğru çalıştığını doğrular.
func TestStoreWriteFailureStillCompensates(t *testing.T) {
	rec := &recorder{}
	store := &brokenStore{
		Store:     workflow.NewMemoryStore(),
		appendErr: errors.Unavailable("db", "yazılamadı"),
	}
	eng := workflow.New(store, testLogger())

	a := step(rec, "a")
	b := step(rec, "b")
	c := step(rec, "c")
	c.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Internal("c", "c patladı")
	}

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "telafi_yazmasız", Steps: steps(a, b, c)}, nil)
	require.Error(t, err)

	assert.Equal(t, []string{
		"invoke:a", "invoke:b", "invoke:c",
		"compensate:b", "compensate:a",
	}, rec.snapshot(), "telafi Store'a değil motorun belleğine dayanmalı")
}

// --- yapılandırma -----------------------------------------------------------

// TestNilStoreEngineRefusesToRun Store verilmemiş bir motorun hiçbir adımı
// çalıştırmadığını doğrular: sessizce süreç içi depoya düşmek idempotency
// korumasını süreç sınırına indirirdi.
func TestNilStoreEngineRefusesToRun(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(nil, testLogger())

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "store_yok", Steps: steps(step(rec, "a"))}, nil)

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Empty(t, rec.snapshot(), "Store yokken hiçbir adım çalıştırılmamalı")
}

// TestNewInMemoryRunsWithoutStore süreç içi deponun AÇIK seçimle
// kullanılabildiğini ve nil log'un motoru çökertmediğini doğrular.
func TestNewInMemoryRunsWithoutStore(t *testing.T) {
	rec := &recorder{}
	eng := workflow.NewInMemory(testLogger())

	out, err := eng.Run(t.Context(), workflow.Workflow{Name: "nil_bağımlılık", Steps: steps(step(rec, "a"))}, nil)
	require.NoError(t, err)
	assert.JSONEq(t, `"a-out"`, rawOutput(t, out))
}

// TestStepContextCarriesEngineFields motorun StepContext alanlarını doldurduğunu doğrular.
func TestStepContextCarriesEngineFields(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	var seen []workflow.StepContext
	capture := func(_ context.Context, sc *workflow.StepContext) (any, error) {
		seen = append(seen, *sc)
		return nil, nil
	}

	a, b := step(rec, "a"), step(rec, "b")
	a.onInvoke, b.onInvoke = capture, capture

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "bağlam", Steps: steps(a, b)}, nil)
	require.NoError(t, err)

	require.Len(t, seen, 2)
	assert.True(t, strings.HasPrefix(seen[0].ExecutionID, "wfx_"), "yürütme kimliği ön ekli olmalı")
	assert.Equal(t, seen[0].ExecutionID, seen[1].ExecutionID)
	assert.Equal(t, "bağlam", seen[0].Workflow)
	assert.Equal(t, "a", seen[0].StepName)
	assert.Equal(t, 0, seen[0].StepIndex)
	assert.Equal(t, "b", seen[1].StepName)
	assert.Equal(t, 1, seen[1].StepIndex)
	assert.Equal(t, 1, seen[1].Attempt)
}

// TestStepFailureKeepsUnderlyingCode adım hatasının KODUNUN motorun
// sarmalamasında korunduğunu doğrular.
//
// # Neden bu iddia parayla ilgili
//
// Taşıma katmanı hata gövdesine tek bir makine okunur alan yazar: Code. Motor
// onu kendi sabitiyle ezdiğinde HER adım hatası istemci için tek bir değere —
// "workflow_step_failed" — düzleşiyordu. Somut bedeli B2B harcama limitiydi:
// limiti aşan alışveriş 409 alıyor ama vitrin, "limitiniz yetmedi" ile
// "geçici çakışma, tekrar deneyin"i AYIRT EDEMİYORDU. 409 tam olarak tekrarın
// çözmediği sınıftır; ayrımı yapacak veri (alt hatanın kodu) üretiliyor,
// yalnızca tüketiciye ulaşmıyordu.
//
// Sınıf (Kind) zaten devralınıyordu; test onun da korunduğunu birlikte
// sabitler — kod taşınırken sınıfın kayması, ikisini tek yerde okuyan taşıma
// katmanını bozardı.
func TestStepFailureKeepsUnderlyingCode(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	a := step(rec, "a")
	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Conflict("order_spending_limit_exceeded",
			"müşterinin dönem harcama limiti aşıldı")
	}

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "limit", Steps: steps(a, b)}, nil)
	require.Error(t, err)

	assert.Equal(t, "order_spending_limit_exceeded", errors.CodeOf(err),
		"reddin KODU dışa taşınmalı; motorun kendi kodu onu ezerse istemci "+
			"limit aşımını geçici bir arızadan ayıramaz")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err),
		"sınıf da alt hatadan gelmeli: 409, tekrarın çözmediği sınıftır")
	assert.Contains(t, err.Error(), "\"b\" adımı",
		"motorun kendi cümlesi (hangi workflow, hangi adım) KAYBOLMAMALI")

	// Telafi zinciri kod değişikliğinden etkilenmez: a geri alınmış olmalı.
	assert.Equal(t, []string{"invoke:a", "invoke:b", "compensate:a"}, rec.snapshot())
}

// TestStepFailureFallsBackToEngineCode KODSUZ bir adım hatasının motorun kendi
// kodunu aldığını doğrular.
//
// Tipsiz bir stdlib hatası için taşınacak kod yoktur ve kodsuz bir gövde
// istemciye hiçbir şey söylemez; yedek kod bu yüzden vardır.
func TestStepFailureFallsBackToEngineCode(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	a := step(rec, "a")
	a.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.New("kodsuz arıza")
	}

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "kodsuz", Steps: steps(a)}, nil)
	require.Error(t, err)

	assert.Equal(t, workflow.CodeStepFailed, errors.CodeOf(err),
		"kodsuz adım hatası motorun kendi kodunu almalı")
	assert.Equal(t, errors.KindInternal, errors.KindOf(err),
		"sınıflandırılmamış hata güvenli tarafa, internal'a düşmeli")
}

// TestStepPanicKeepsPanicCode panik yolunun da kendi kodunu koruduğunu
// doğrular.
//
// Panik bir PROGRAMLAMA hatasıdır ve operatörün onu geçici bir arızadan
// ayırabilmesi gerekir; kod dışa taşınmasaydı panik de sıradan bir adım
// hatasına benzerdi. Gözcü hata (ErrPanic) zincirde ayrıca durur.
func TestStepPanicKeepsPanicCode(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	a := step(rec, "a")
	a.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		panic("adım paniği")
	}

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "panik_kodu", Steps: steps(a)}, nil)
	require.Error(t, err)

	assert.Equal(t, workflow.CodeStepPanicked, errors.CodeOf(err))
	assert.ErrorIs(t, err, workflow.ErrPanic)
}

// TestCompensationFailureCodeUnchanged telafi patladığında dışa çıkan kodun
// DEĞİŞMEDİĞİNİ doğrular.
//
// Adım kodunun taşınması motorun tüm saga'larını etkiler; bu test o
// değişikliğin sınırını çizer. Telafi patladığında istemcinin/operatörün
// görmesi gereken şey adımın neden düştüğü değil, SİSTEMİN TUTARSIZ kaldığıdır
// — o yüzden dıştaki kod [workflow.CodeCompensationFailed] olarak kalmalıdır
// ve adım kodu yalnızca zincirin içinde durmalıdır.
func TestCompensationFailureCodeUnchanged(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	a := step(rec, "a")
	a.onCompensate = func(context.Context, *workflow.StepContext) error {
		return errors.Internal("a_comp", "a telafisi patladı")
	}
	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Conflict("order_spending_limit_exceeded", "limit aşıldı")
	}

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "telafi_kodu", Steps: steps(a, b)}, nil)
	require.Error(t, err)

	assert.Equal(t, workflow.CodeCompensationFailed, errors.CodeOf(err),
		"elle müdahale gerektiren durum adım koduyla maskelenmemeli")
	assert.Equal(t, errors.KindInternal, errors.KindOf(err))
	assert.Contains(t, err.Error(), "order_spending_limit_exceeded",
		"adım kodu zincirde yine okunabilmeli")
}
