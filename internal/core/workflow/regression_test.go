package workflow_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
)

// --- yeniden denenen adımın telafisi ---------------------------------------

// TestRetriedStepSideEffectIsCompensated motorun KENDİ tetiklediği yeniden
// denemeden sonra patlayan adımın telafi edildiğini doğrular.
func TestRetriedStepSideEffectIsCompensated(t *testing.T) {
	rec := &recorder{}
	dunya := map[string]bool{}

	var execID string
	s := step(rec, "rezerve")
	s.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		execID = sc.ExecutionID
		if sc.Attempt == 1 {
			// 1. deneme yan etkiyi DÜNYAYA uyguladı, yanıt kayboldu.
			sc.Shared["rez"] = "rez_1"
			dunya["rez_1"] = true
			return nil, errors.Unavailable("gecici", "yanıt kayboldu")
		}
		return nil, errors.Unavailable("gecici", "servis kapalı")
	}
	s.onCompensate = func(_ context.Context, sc *workflow.StepContext) error {
		if id, ok := sc.Shared["rez"].(string); ok {
			delete(dunya, id)
		}
		return nil
	}

	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	_, err := eng.Run(t.Context(),
		workflow.Workflow{Name: "yeniden_denenen_telafi", Steps: steps(s)}, nil,
		workflow.WithRetry(workflow.RetryPolicy{MaxAttempts: 2}))

	require.Error(t, err)
	assert.Contains(t, rec.snapshot(), "compensate:rezerve",
		"motor adımı yeniden denediyse patlayan adım da telafi edilmeli")
	assert.Empty(t, dunya, "1. denemenin açtığı rezervasyon asılı kalmamalı")

	exec, gerr := store.Get(t.Context(), execID)
	require.NoError(t, gerr)
	assert.Equal(t, workflow.StatusFailed, exec.Status,
		"telafi başarılıysa failed doğru bir kayıttır")
}

// TestSingleAttemptFailureIsNotCompensated tek denemede patlayan adımın telafi
// EDİLMEDİĞİNİ doğrular: kural yalnızca motorun tetiklediği tekrar için delinir.
func TestSingleAttemptFailureIsNotCompensated(t *testing.T) {
	rec := &recorder{}

	a := step(rec, "a")
	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Invalid("gecersiz", "b patladı")
	}

	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "tek_deneme", Steps: steps(a, b)}, nil)

	require.Error(t, err)
	calls := rec.snapshot()
	assert.NotContains(t, calls, "compensate:b", "tek denemede patlayan adım telafi edilmemeli")
	assert.Contains(t, calls, "compensate:a")
}

// TestRetriedStepCompensationFailureMarksExecution yeniden denenen adımın
// telafisi de patlarsa yürütmenin compensation_failed yazıldığını doğrular.
func TestRetriedStepCompensationFailureMarksExecution(t *testing.T) {
	rec := &recorder{}

	var execID string
	s := step(rec, "rezerve")
	s.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		execID = sc.ExecutionID
		return nil, errors.Unavailable("gecici", "%d. deneme patladı", sc.Attempt)
	}
	s.onCompensate = func(context.Context, *workflow.StepContext) error {
		return errors.Unavailable("gecici", "telafi de patladı")
	}

	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	_, err := eng.Run(t.Context(),
		workflow.Workflow{Name: "telafi_de_patlar", Steps: steps(s)}, nil,
		workflow.WithRetry(workflow.RetryPolicy{MaxAttempts: 2}))

	require.Error(t, err)

	exec, gerr := store.Get(t.Context(), execID)
	require.NoError(t, gerr)
	assert.Equal(t, workflow.StatusCompensationFailed, exec.Status)
}

// --- telafi kaydı Invoke izini ezmemeli -------------------------------------

// TestCompensationRecordKeepsInvokeOutput telafi kaydının Invoke'un çıktısını
// ve deneme sayısını korduğunu doğrular; elle müdahale bu veriye dayanır.
func TestCompensationRecordKeepsInvokeOutput(t *testing.T) {
	rec := &recorder{}

	var execID string
	kere := 0
	a := step(rec, "rezerve")
	a.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		execID = sc.ExecutionID
		kere++
		if kere == 1 {
			return nil, errors.Unavailable("gecici", "ilk deneme patladı")
		}
		return map[string]string{"reservation_id": "rez_42"}, nil
	}

	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		// Yeniden denenmeyen sınıf: b tek denemede patlar.
		return nil, errors.Invalid("gecersiz", "b patladı")
	}

	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "iz_korunur", Steps: steps(a, b)}, nil,
		workflow.WithRetry(workflow.RetryPolicy{MaxAttempts: 3}))
	require.Error(t, err)

	exec, gerr := store.Get(t.Context(), execID)
	require.NoError(t, gerr)
	require.NotEmpty(t, exec.Steps)

	got := exec.Steps[0]
	assert.Equal(t, workflow.StepCompensated, got.Status)
	assert.JSONEq(t, `{"reservation_id":"rez_42"}`, string(got.Output),
		"telafi kaydı Invoke'un çıktısını silmemeli")
	assert.Equal(t, 2, got.Attempts, "Invoke'un deneme sayısı korunmalı")
	assert.False(t, got.StartedAt.IsZero())
}

// --- telafi bütçesi adım başınadır ------------------------------------------

// TestCompensationBudgetIsPerStep yavaş bir telafinin kendisinden ÖNCEKİ
// adımların telafisini ölü bağlamla bırakmadığını doğrular.
func TestCompensationBudgetIsPerStep(t *testing.T) {
	rec := &recorder{}

	var ilkTelafiHatasi error
	a := step(rec, "hizli")
	a.onCompensate = func(ctx context.Context, _ *workflow.StepContext) error {
		ilkTelafiHatasi = ctx.Err()
		return nil
	}

	b := step(rec, "yavas")
	b.onCompensate = func(context.Context, *workflow.StepContext) error {
		// Bağlama saygısız, yavaş bir telafi: bütçesini aşar.
		time.Sleep(150 * time.Millisecond)
		return nil
	}

	c := step(rec, "c")
	c.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Invalid("gecersiz", "c patladı")
	}

	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "butce", Steps: steps(a, b, c)}, nil,
		workflow.WithCompensationTimeout(50*time.Millisecond))

	require.Error(t, err)
	assert.NoError(t, ilkTelafiHatasi,
		"her telafi kendi bütçesini alır; önceki adım ölü bağlamla çağrılmamalı")
}

// --- özel Retryable yüklemi panik/iptal korumasını devralır -----------------

// TestCustomRetryableStillSkipsPanic özel yüklem her şeyi denenebilir dese bile
// paniğin yeniden DENENMEDİĞİNİ doğrular.
func TestCustomRetryableStillSkipsPanic(t *testing.T) {
	rec := &recorder{}
	cagri := 0

	s := step(rec, "panikleyen")
	s.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		cagri++
		panic("her denemede aynı çöküş")
	}

	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "ozel_yuklem_panik", Steps: steps(s)}, nil,
		workflow.WithRetry(workflow.RetryPolicy{
			MaxAttempts: 3,
			Retryable:   func(error) bool { return true },
		}))

	require.Error(t, err)
	assert.ErrorIs(t, err, workflow.ErrPanic)
	assert.Equal(t, 1, cagri, "panik özel yüklemle de yeniden denenmemeli")
}

// TestCustomRetryableStillSkipsCanceledContext özel yüklemin iptal korumasını
// da devraldığını doğrular.
func TestCustomRetryableStillSkipsCanceledContext(t *testing.T) {
	rec := &recorder{}
	cagri := 0

	s := step(rec, "iptal")
	s.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		cagri++
		// Bağımlılık iptal edilmiş bir alt bağlamla dönüyor; motorun kendi
		// bağlamı CANLI, dolayısıyla tekrarı durduran tek şey eleme kuralıdır.
		return nil, errors.Wrap(context.Canceled, errors.KindUnavailable, "iptal", "çağrı iptal edildi")
	}

	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "ozel_yuklem_iptal", Steps: steps(s)}, nil,
		workflow.WithRetry(workflow.RetryPolicy{
			MaxAttempts: 3,
			Retryable:   func(error) bool { return true },
		}))

	require.Error(t, err)
	assert.Equal(t, 1, cagri, "iptal edilmiş bağlamda yeniden denenmemeli")
}

// --- ölü bağlamla gelen çağrı anahtarı yakmamalı ----------------------------

// TestCanceledContextDoesNotBurnIdempotencyKey hiç adım çalışmadan iptal edilen
// çağrının aynı anahtarı kullanılamaz hâle GETİRMEDİĞİNİ doğrular.
func TestCanceledContextDoesNotBurnIdempotencyKey(t *testing.T) {
	rec := &recorder{}
	s := step(rec, "a")

	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())
	wf := workflow.Workflow{Name: "olu_baglam", Steps: steps(s)}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := eng.Run(ctx, wf, nil, workflow.WithIdempotencyKey("siparis-1"))
	require.Error(t, err)
	assert.Empty(t, rec.snapshot(), "hiçbir adım çalışmış olmamalı")

	out, err := eng.Run(t.Context(), wf, nil, workflow.WithIdempotencyKey("siparis-1"))
	require.NoError(t, err, "hiç iş yapılmamışken anahtar yeniden kullanılabilmeli")
	assert.NotNil(t, out)
}

// --- tipli nil adım ---------------------------------------------------------

// TestTypedNilStepIsRejected tipli-nil bir adımın motoru çökertmediğini doğrular.
func TestTypedNilStepIsRejected(t *testing.T) {
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	var bos *testStep // tipli nil: arayüz değeri nil DEĞİLDİR
	wf := workflow.Workflow{Name: "tipli_nil", Steps: []workflow.Step{bos}}

	_, err := eng.Run(t.Context(), wf, nil)

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "tipli nil adım geçersiz workflow'dur, panik değil")
}

// --- ad ve anahtar uzunluk sınırları ----------------------------------------

// TestWorkflowNameLengthIsValidated sınırı aşan adın motorda reddedildiğini
// doğrular: kalıcı Store'un sınırına orada çarpmak yerine burada bilinir.
func TestWorkflowNameLengthIsValidated(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	uzun := ""
	for range workflow.MaxNameLen + 1 {
		uzun += "a"
	}

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: uzun, Steps: steps(step(rec, "a"))}, nil)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))

	_, err = eng.Run(t.Context(),
		workflow.Workflow{Name: "kisa", Steps: steps(step(rec, uzun))}, nil)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "adım adı da sınırlıdır")
}

// --- eşzamanlı bileşiğin Shared hijyeni -------------------------------------

// TestParallelFailureDiscardsBranchWrites patlayan bir bileşikten sonra HİÇBİR
// dalın yazmasının üst bağlamda kalmadığını doğrular.
func TestParallelFailureDiscardsBranchWrites(t *testing.T) {
	rec := &recorder{}

	var gorulenRez any
	var hayaletVarMi bool

	once := step(rec, "siparis")
	once.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		sc.Shared["rez"] = "orijinal"
		return "siparis-out", nil
	}
	once.onCompensate = func(_ context.Context, sc *workflow.StepContext) error {
		gorulenRez = sc.Shared["rez"]
		_, hayaletVarMi = sc.Shared["hayalet"]
		return nil
	}

	a := step(rec, "stok")
	a.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		sc.Shared["rez"] = "dal_rezervasyonu"
		return "a-out", nil
	}

	b := step(rec, "kargo")
	b.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		sc.Shared["hayalet"] = "var"
		return nil, errors.Invalid("gecersiz", "kargo dalı patladı")
	}

	par := workflow.NewParallel("cift", a, b).WithLogger(testLogger())
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	_, err := eng.Run(t.Context(),
		workflow.Workflow{Name: "dal_kirlenmesi", Steps: []workflow.Step{once, par}}, nil)

	require.Error(t, err)
	assert.Equal(t, "orijinal", gorulenRez,
		"geri alınan dalın yazması önceki adımın telafisine sızmamalı")
	assert.False(t, hayaletVarMi, "patlayan dalın yazması üst bağlama işlenmemeli")
}

// TestParallelMergeKeepsWritingBranchValue yalnızca okuyan bir dalın bayat
// kopyasının, yazan kardeşinin değerini ezmediğini doğrular.
func TestParallelMergeKeepsWritingBranchValue(t *testing.T) {
	rec := &recorder{}

	once := step(rec, "once")
	once.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		sc.Shared["rez"] = "eski"
		return "once-out", nil
	}

	yazan := step(rec, "yazan")
	yazan.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		sc.Shared["rez"] = "yeni"
		return "yazan-out", nil
	}

	okuyan := step(rec, "okuyan")
	okuyan.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		_ = sc.Shared["rez"] // yalnızca okur, yazmaz
		return "okuyan-out", nil
	}

	var sonra any
	son := step(rec, "sonra")
	son.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		sonra = sc.Shared["rez"]
		return "sonra-out", nil
	}

	par := workflow.NewParallel("cift", yazan, okuyan).WithLogger(testLogger())
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	_, err := eng.Run(t.Context(),
		workflow.Workflow{Name: "bayat_kopya", Steps: []workflow.Step{once, par, son}}, nil)

	require.NoError(t, err)
	assert.Equal(t, "yeni", sonra,
		"dokunmayan dalın bayat kopyası yazan dalın değerini ezmemeli")
}

// TestParallelRollbackFailureMarksCompensationFailed bileşiğin İÇ geri alması
// patladığında yürütmenin failed değil compensation_failed yazıldığını doğrular.
func TestParallelRollbackFailureMarksCompensationFailed(t *testing.T) {
	rec := &recorder{}

	var execID string
	a := step(rec, "a")
	a.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		execID = sc.ExecutionID
		return "a-out", nil
	}
	a.onCompensate = func(context.Context, *workflow.StepContext) error {
		return errors.Unavailable("gecici", "a dalı geri alınamadı")
	}

	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Invalid("gecersiz", "b dalı patladı")
	}

	par := workflow.NewParallel("cift", a, b).WithLogger(testLogger())
	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	_, err := eng.Run(t.Context(),
		workflow.Workflow{Name: "ic_geri_alma", Steps: []workflow.Step{par}}, nil)
	require.Error(t, err)

	exec, gerr := store.Get(t.Context(), execID)
	require.NoError(t, gerr)
	assert.Equal(t, workflow.StatusCompensationFailed, exec.Status,
		"asılı kalan dal yan etkisi failed diye yazılamaz")
}

// --- Run'ın çıktı tipi ------------------------------------------------------

// TestRunOutputTypeIsStable mutlu yolun ve tekrarın AYNI Go tipini döndüğünü
// doğrular: çağıranın tip doğrulaması yarışa bağlı olamaz.
func TestRunOutputTypeIsStable(t *testing.T) {
	rec := &recorder{}
	wf := workflow.Workflow{Name: "tip_kararli", Steps: steps(step(rec, "a"))}

	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	ilk, err := eng.Run(t.Context(), wf, nil, workflow.WithIdempotencyKey("k"))
	require.NoError(t, err)
	tekrar, err := eng.Run(t.Context(), wf, nil, workflow.WithIdempotencyKey("k"))
	require.NoError(t, err)

	ilkRaw, ok := ilk.(json.RawMessage)
	require.True(t, ok, "mutlu yol da json.RawMessage dönmeli")
	tekrarRaw, ok := tekrar.(json.RawMessage)
	require.True(t, ok)

	assert.JSONEq(t, string(ilkRaw), string(tekrarRaw))
}

// TestParallelRetryStartsFromCleanShared yeniden denenen bir bileşiğin
// 2. denemesinin, geri alınmış 1. denemenin verisini GÖRMEDİĞİNİ doğrular.
func TestParallelRetryStartsFromCleanShared(t *testing.T) {
	rec := &recorder{}

	var gorulen []any
	a := step(rec, "a")
	a.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		gorulen = append(gorulen, sc.Shared["rez"])
		sc.Shared["rez"] = fmt.Sprintf("rez_%d", sc.Attempt)
		return "a-out", nil
	}

	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Unavailable("gecici", "b dalı patladı")
	}

	par := workflow.NewParallel("cift", a, b).WithLogger(testLogger())
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	_, err := eng.Run(t.Context(),
		workflow.Workflow{Name: "bilesik_tekrar", Steps: []workflow.Step{par}}, nil,
		workflow.WithRetry(workflow.RetryPolicy{MaxAttempts: 2}))

	require.Error(t, err)
	require.Len(t, gorulen, 2)
	assert.Equal(t, []any{nil, nil}, gorulen,
		"geri alınmış denemenin yazması sonraki denemeye sızmamalı")
}

// TestUncompensatedSentinelForcesCompensationFailed ErrUncompensated taşıyan bir
// adım hatasının, telafi zinciri temiz bitse bile durumu compensation_failed
// yaptığını doğrular.
func TestUncompensatedSentinelForcesCompensationFailed(t *testing.T) {
	rec := &recorder{}

	var execID string
	a := step(rec, "a")
	a.onInvoke = captureExecutionID(&execID)

	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Wrap(workflow.ErrUncompensated, errors.KindInternal,
			"asili", "b arkasında iş bıraktı")
	}

	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "asili_yan_etki", Steps: steps(a, b)}, nil)
	require.Error(t, err)

	assert.Contains(t, rec.snapshot(), "compensate:a", "önceki adım yine de telafi edilmeli")

	exec, gerr := store.Get(t.Context(), execID)
	require.NoError(t, gerr)
	assert.Equal(t, workflow.StatusCompensationFailed, exec.Status,
		"asılı yan etki bildiren yürütme failed diye yazılamaz")
}
