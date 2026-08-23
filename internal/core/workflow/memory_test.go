package workflow_test

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
)

// yeniYurutme testlerde kullanılan geçerli bir yürütme kaydı üretir.
func yeniYurutme(id, wf, key string) *workflow.Execution {
	return &workflow.Execution{
		ID:             id,
		Workflow:       wf,
		IdempotencyKey: key,
		Status:         workflow.StatusRunning,
		Input:          json.RawMessage(`{"n":1}`),
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
}

func TestMemoryStoreCreateAndGet(t *testing.T) {
	s := workflow.NewMemoryStore()
	require.NoError(t, s.Create(t.Context(), yeniYurutme("wfx_1", "wf", "k1")))

	got, err := s.Get(t.Context(), "wfx_1")
	require.NoError(t, err)
	assert.Equal(t, "wfx_1", got.ID)
	assert.Equal(t, "wf", got.Workflow)
	assert.Equal(t, workflow.StatusRunning, got.Status)
	assert.JSONEq(t, `{"n":1}`, string(got.Input))
}

func TestMemoryStoreGetNotFound(t *testing.T) {
	s := workflow.NewMemoryStore()
	_, err := s.Get(t.Context(), "yok")
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
	assert.Equal(t, workflow.CodeExecutionNotFound, errors.CodeOf(err))
}

func TestMemoryStoreCreateRejectsInvalid(t *testing.T) {
	s := workflow.NewMemoryStore()

	require.Error(t, s.Create(t.Context(), nil))
	assert.Equal(t, errors.KindInvalid, errors.KindOf(s.Create(t.Context(), &workflow.Execution{Workflow: "wf"})))
	assert.Equal(t, errors.KindInvalid, errors.KindOf(s.Create(t.Context(), &workflow.Execution{ID: "wfx_1"})))
}

func TestMemoryStoreDuplicateIdempotencyKeyConflicts(t *testing.T) {
	s := workflow.NewMemoryStore()
	require.NoError(t, s.Create(t.Context(), yeniYurutme("wfx_1", "wf", "k1")))

	err := s.Create(t.Context(), yeniYurutme("wfx_2", "wf", "k1"))
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))

	// Aynı anahtar FARKLI workflow için serbesttir.
	require.NoError(t, s.Create(t.Context(), yeniYurutme("wfx_3", "başka_wf", "k1")))
}

func TestMemoryStoreDuplicateIDConflicts(t *testing.T) {
	s := workflow.NewMemoryStore()
	require.NoError(t, s.Create(t.Context(), yeniYurutme("wfx_1", "wf", "")))

	err := s.Create(t.Context(), yeniYurutme("wfx_1", "wf", ""))
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
}

// TestMemoryStoreEmptyKeysDoNotConflict anahtarsız yürütmelerin birbiriyle
// çakışmadığını doğrular; tekillik yalnızca anahtar verilmişken zorlanır.
func TestMemoryStoreEmptyKeysDoNotConflict(t *testing.T) {
	s := workflow.NewMemoryStore()
	require.NoError(t, s.Create(t.Context(), yeniYurutme("wfx_1", "wf", "")))
	require.NoError(t, s.Create(t.Context(), yeniYurutme("wfx_2", "wf", "")))
	require.NoError(t, s.Create(t.Context(), yeniYurutme("wfx_3", "wf", "")))

	_, err := s.FindByIdempotencyKey(t.Context(), "wf", "")
	require.Error(t, err, "boş anahtar hiçbir yürütmeye eşleşmemeli")
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

func TestMemoryStoreFindByIdempotencyKey(t *testing.T) {
	s := workflow.NewMemoryStore()
	require.NoError(t, s.Create(t.Context(), yeniYurutme("wfx_1", "wf", "k1")))

	got, err := s.FindByIdempotencyKey(t.Context(), "wf", "k1")
	require.NoError(t, err)
	assert.Equal(t, "wfx_1", got.ID)

	_, err = s.FindByIdempotencyKey(t.Context(), "wf", "yok")
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))

	_, err = s.FindByIdempotencyKey(t.Context(), "başka", "k1")
	require.Error(t, err, "anahtar workflow adıyla BİRLİKTE tekildir")
}

func TestMemoryStoreAppendStepAddsThenUpdates(t *testing.T) {
	s := workflow.NewMemoryStore()
	require.NoError(t, s.Create(t.Context(), yeniYurutme("wfx_1", "wf", "")))

	require.NoError(t, s.AppendStep(t.Context(), "wfx_1", workflow.StepRecord{
		Name: "a", Index: 0, Status: workflow.StepInvoked, Attempts: 1,
	}))
	require.NoError(t, s.AppendStep(t.Context(), "wfx_1", workflow.StepRecord{
		Name: "b", Index: 1, Status: workflow.StepInvoked, Attempts: 1,
	}))

	got, err := s.Get(t.Context(), "wfx_1")
	require.NoError(t, err)
	require.Len(t, got.Steps, 2)

	// Aynı Index yeni satır AÇMAZ, var olanı günceller.
	require.NoError(t, s.AppendStep(t.Context(), "wfx_1", workflow.StepRecord{
		Name: "a", Index: 0, Status: workflow.StepCompensated, Attempts: 2,
	}))

	got, err = s.Get(t.Context(), "wfx_1")
	require.NoError(t, err)
	require.Len(t, got.Steps, 2, "aynı Index ikinci satır açmamış olmalı")
	assert.Equal(t, workflow.StepCompensated, got.Steps[0].Status)
	assert.Equal(t, 2, got.Steps[0].Attempts)
	assert.Equal(t, workflow.StepInvoked, got.Steps[1].Status)
}

func TestMemoryStoreAppendStepUnknownExecution(t *testing.T) {
	s := workflow.NewMemoryStore()
	err := s.AppendStep(t.Context(), "yok", workflow.StepRecord{Name: "a"})
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

func TestMemoryStoreUpdateStatus(t *testing.T) {
	s := workflow.NewMemoryStore()
	require.NoError(t, s.Create(t.Context(), yeniYurutme("wfx_1", "wf", "")))

	require.NoError(t, s.UpdateStatus(t.Context(), "wfx_1", workflow.StatusCompleted, json.RawMessage(`{"ok":true}`), ""))

	got, err := s.Get(t.Context(), "wfx_1")
	require.NoError(t, err)
	assert.Equal(t, workflow.StatusCompleted, got.Status)
	assert.JSONEq(t, `{"ok":true}`, string(got.Output))
	assert.True(t, got.UpdatedAt.After(got.CreatedAt) || got.UpdatedAt.Equal(got.CreatedAt))

	err = s.UpdateStatus(t.Context(), "yok", workflow.StatusFailed, nil, "hata")
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestMemoryStoreReturnsDeepCopies dışarı verilen değerin Store'un durumunu
// bozamayacağını doğrular; kalıcı bir Store'da da davranış budur.
func TestMemoryStoreReturnsDeepCopies(t *testing.T) {
	s := workflow.NewMemoryStore()

	original := yeniYurutme("wfx_1", "wf", "k1")
	require.NoError(t, s.Create(t.Context(), original))

	// Çağıranın elindeki değeri değiştirmek Store'u etkilememeli.
	original.Status = workflow.StatusCompleted
	original.Input[2] = 'X'

	require.NoError(t, s.AppendStep(t.Context(), "wfx_1", workflow.StepRecord{
		Name: "a", Index: 0, Status: workflow.StepInvoked, Output: json.RawMessage(`"ilk"`),
	}))

	got, err := s.Get(t.Context(), "wfx_1")
	require.NoError(t, err)
	assert.Equal(t, workflow.StatusRunning, got.Status, "Store kendi kopyasını tutmalı")
	assert.JSONEq(t, `{"n":1}`, string(got.Input))

	// Store'dan alınan değeri değiştirmek de Store'u etkilememeli.
	got.Status = workflow.StatusFailed
	got.Steps[0].Status = workflow.StepFailed
	got.Steps[0].Output[1] = 'X'

	again, err := s.Get(t.Context(), "wfx_1")
	require.NoError(t, err)
	assert.Equal(t, workflow.StatusRunning, again.Status)
	assert.Equal(t, workflow.StepInvoked, again.Steps[0].Status)
	assert.JSONEq(t, `"ilk"`, string(again.Steps[0].Output))
}

// TestMemoryStoreConcurrentUse eşzamanlı kullanımın güvenli olduğunu doğrular
// (yarış dedektörü ile anlamlıdır).
func TestMemoryStoreConcurrentUse(t *testing.T) {
	s := workflow.NewMemoryStore()

	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)

	for i := range n {
		go func() {
			defer wg.Done()

			id := "wfx_" + string(rune('A'+i%26)) + string(rune('a'+i/26))
			if err := s.Create(t.Context(), yeniYurutme(id, "wf", id)); err != nil {
				return
			}
			_ = s.AppendStep(t.Context(), id, workflow.StepRecord{Name: "a", Index: 0, Status: workflow.StepInvoked})
			_ = s.UpdateStatus(t.Context(), id, workflow.StatusCompleted, json.RawMessage(`1`), "")
			_, _ = s.Get(t.Context(), id)
			_, _ = s.FindByIdempotencyKey(t.Context(), "wf", id)
		}()
	}
	wg.Wait()
}
