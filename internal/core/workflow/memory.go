package workflow

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// memoryStore Store'un süreç içi, kalıcılığı olmayan uygulamasıdır.
type memoryStore struct {
	// mu tüm alanları korur. Yazma yolu (Create/AppendStep/UpdateStatus) okuma
	// yolu kadar sık olduğu için sade bir Mutex yeterlidir.
	mu sync.Mutex
	// byID yürütmeleri kimliğine göre tutar.
	byID map[string]*Execution
	// byKey (workflow, idempotency anahtarı) çiftini kimliğe eşler. Anahtar boş
	// olan yürütmeler buraya GİRMEZ; tekillik yalnızca anahtar verilmişken zorlanır.
	byKey map[idempotencyKey]string
}

// idempotencyKey byKey haritasının bileşik anahtarıdır.
//
// Dizgeleri birleştirmek yerine yapı kullanılır: birleştirme, ayırıcı karakteri
// içeren bir workflow adı ya da anahtar geldiğinde iki farklı çifti aynı
// anahtara düşürebilirdi.
type idempotencyKey struct {
	workflow string
	key      string
}

var _ Store = (*memoryStore)(nil)

// NewMemoryStore süreç içi, kalıcılığı olmayan bir Store üretir.
//
// Test ve geliştirme içindir: süreç ölürse tüm yürütme geçmişi kaybolur ve
// yarım kalmış bir yürütme kurtarılamaz. Üretimde kalıcı bir Store
// kullanılmalıdır. Eşzamanlı kullanıma güvenlidir ve dışarıya verdiği her
// Execution DERİN KOPYADIR: çağıranın elindeki değeri değiştirmek Store'un
// durumunu bozmaz.
//
// Bağlamı YOK SAYAR: süreç içi harita erişiminin iptalle kesilecek bir
// beklemesi yoktur. Bunun testler için sonucu şudur — motorun bağlam
// davranışını (örn. iptal edilmiş bağlamda yazma) sınayan testler bu Store'la
// yanıltıcı biçimde GEÇER; öyle bir sınama, bağlamı gerçekten kullanan bir
// sahte Store ister.
func NewMemoryStore() Store {
	return &memoryStore{
		byID:  make(map[string]*Execution),
		byKey: make(map[idempotencyKey]string),
	}
}

// Create yeni yürütme kaydı açar.
//
// Aynı (Workflow, IdempotencyKey) çifti zaten varsa errors.Conflict döner;
// anahtar boşsa tekillik denetlenmez. Aynı kimlik ikinci kez kaydedilirse de
// errors.Conflict döner.
func (s *memoryStore) Create(_ context.Context, exec *Execution) error {
	if exec == nil {
		return errors.Invalid(CodeInvalidWorkflow, "yürütme nil olamaz")
	}
	if exec.ID == "" {
		return errors.Invalid(CodeInvalidWorkflow, "yürütme kimliği boş olamaz")
	}
	if exec.Workflow == "" {
		return errors.Invalid(CodeInvalidWorkflow, "yürütmenin workflow adı boş olamaz")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.byID[exec.ID]; ok {
		return errors.Conflict(CodeExecutionExists, "%q kimlikli yürütme zaten var", exec.ID)
	}

	k := idempotencyKey{workflow: exec.Workflow, key: exec.IdempotencyKey}
	if exec.IdempotencyKey != "" {
		if existing, ok := s.byKey[k]; ok {
			return errors.Conflict(CodeExecutionExists,
				"%q workflow'unun %q anahtarlı yürütmesi zaten var: %s", exec.Workflow, exec.IdempotencyKey, existing)
		}
		s.byKey[k] = exec.ID
	}

	s.byID[exec.ID] = cloneExecution(exec)
	return nil
}

// FindByIdempotencyKey anahtara karşılık gelen yürütmeyi döner; yoksa errors.NotFound.
func (s *memoryStore) FindByIdempotencyKey(_ context.Context, workflow, key string) (*Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.byKey[idempotencyKey{workflow: workflow, key: key}]
	if !ok {
		return nil, errors.NotFound(CodeExecutionNotFound,
			"%q workflow'unun %q anahtarlı yürütmesi bulunamadı", workflow, key)
	}

	exec, ok := s.byID[id]
	if !ok {
		return nil, errors.Internal(CodeStoreFailed, "%q anahtarı %q kimliğine işaret ediyor ama kayıt yok", key, id)
	}
	return cloneExecution(exec), nil
}

// AppendStep bir adım kaydını ekler ya da aynı Index'li kaydı günceller.
func (s *memoryStore) AppendStep(_ context.Context, executionID string, rec StepRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	exec, ok := s.byID[executionID]
	if !ok {
		return errors.NotFound(CodeExecutionNotFound, "%q kimlikli yürütme bulunamadı", executionID)
	}

	stored := cloneStep(rec)
	if i := slices.IndexFunc(exec.Steps, func(r StepRecord) bool { return r.Index == rec.Index }); i >= 0 {
		exec.Steps[i] = stored
	} else {
		exec.Steps = append(exec.Steps, stored)
	}

	exec.UpdatedAt = time.Now().UTC()
	return nil
}

// UpdateStatus yürütmenin son durumunu yazar.
func (s *memoryStore) UpdateStatus(_ context.Context, executionID string, status Status, output json.RawMessage, failure string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	exec, ok := s.byID[executionID]
	if !ok {
		return errors.NotFound(CodeExecutionNotFound, "%q kimlikli yürütme bulunamadı", executionID)
	}

	exec.Status = status
	exec.Output = slices.Clone(output)
	exec.Failure = failure
	exec.UpdatedAt = time.Now().UTC()

	// Telafi eksiksiz tamamlandıysa anahtar BIRAKILIR; gerekçe
	// [StatusFailed] godoc'unda. Kayıt kalır, yalnızca anahtarı düşer —
	// pgstore'un aynı satırı NULL'a çekmesiyle aynı davranış.
	if status == StatusFailed && exec.IdempotencyKey != "" {
		delete(s.byKey, idempotencyKey{workflow: exec.Workflow, key: exec.IdempotencyKey})
		exec.IdempotencyKey = ""
	}

	return nil
}

// Get yürütmeyi adımlarıyla birlikte okur; yoksa errors.NotFound.
func (s *memoryStore) Get(_ context.Context, executionID string) (*Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	exec, ok := s.byID[executionID]
	if !ok {
		return nil, errors.NotFound(CodeExecutionNotFound, "%q kimlikli yürütme bulunamadı", executionID)
	}
	return cloneExecution(exec), nil
}

// cloneExecution yürütmenin derin kopyasını üretir.
//
// Kopya, Store'un durumunun dışarıdan (ya da dışarının Store tarafından)
// değiştirilmesini engeller; kalıcı bir Store'da da davranış budur, bellek içi
// uygulamanın onu taklit etmesi testlerin yanıltıcı biçimde geçmesini önler.
func cloneExecution(exec *Execution) *Execution {
	out := *exec
	out.Input = slices.Clone(exec.Input)
	out.Output = slices.Clone(exec.Output)

	if exec.Steps != nil {
		out.Steps = make([]StepRecord, len(exec.Steps))
		for i := range exec.Steps {
			out.Steps[i] = cloneStep(exec.Steps[i])
		}
	}
	return &out
}

// cloneStep adım kaydının derin kopyasını üretir.
func cloneStep(rec StepRecord) StepRecord {
	rec.Output = slices.Clone(rec.Output)
	return rec
}
