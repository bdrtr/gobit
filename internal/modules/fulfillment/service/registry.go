package service

import (
	"slices"
	"strings"
	"sync"

	"github.com/bdrtr/gobit/internal/core/errors"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
)

// ProviderRegistry kargo sağlayıcılarını kimlikleriyle tutar.
//
// Modül kendi varsayılan sağlayıcısını (manual.Provider) Register sırasında
// buraya koyar ve kaydı container'a "fulfillment.providers" adıyla verir. Faz
// 9'daki plugin sistemi, çekirdeğe ve bu modüle DOKUNMADAN, container'dan
// kaydı çözüp kendi sağlayıcısını ekleyebilir; sözleşme
// internal/core/provider'daki FulfillmentProvider arayüzüdür.
//
// Eşzamanlı kullanıma güvenlidir: kayıt açılışta, okuma her istekte yapılır.
type ProviderRegistry struct {
	mu        sync.RWMutex
	providers map[string]coreprovider.FulfillmentProvider
}

// NewProviderRegistry boş bir sağlayıcı kaydı üretir.
func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{providers: make(map[string]coreprovider.FulfillmentProvider)}
}

// Register sağlayıcıyı kendi kimliğiyle kaydeder.
//
// Aynı kimlikle ikinci bir kayıt errors.Conflict döner ve mevcut sağlayıcı
// KORUNUR. Sessizce üzerine yazmak, iki eklentinin aynı kimliği kullandığı bir
// kurulumda hangi sağlayıcının çalıştığını yükleme sırasına bırakırdı —
// kargoda bunun bedeli, paketin beklenmedik bir firmaya verilmesidir.
func (r *ProviderRegistry) Register(p coreprovider.FulfillmentProvider) error {
	if p == nil {
		return errors.Invalid(CodeInvalidInput, "sağlayıcı nil olamaz")
	}
	id := strings.TrimSpace(p.ID())
	if id == "" {
		return errors.Invalid(CodeInvalidInput, "sağlayıcı kimliği boş olamaz")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.providers[id]; exists {
		return errors.Conflict(CodeProviderExists,
			"%q kimlikli bir kargo sağlayıcısı zaten kayıtlı", id)
	}
	r.providers[id] = p
	return nil
}

// Get sağlayıcıyı kimliğiyle döner; kayıtlı değilse errors.NotFound.
//
// Hata mesajı ARANAN kimliği ve KAYITLI kimlikleri birlikte yazar: bir
// sağlayıcının kaydedilmeyi unutulması çalışma zamanında ortaya çıkan bir
// kurulum hatasıdır ve teşhis edilebilir olmalıdır (bkz. ADR 0002).
func (r *ProviderRegistry) Get(id string) (coreprovider.FulfillmentProvider, error) {
	wanted := strings.TrimSpace(id)
	if wanted == "" {
		return nil, errors.Invalid(CodeInvalidInput, "sağlayıcı kimliği boş olamaz")
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.providers[wanted]
	if !ok {
		return nil, errors.NotFound(CodeProviderNotFound,
			"%q kargo sağlayıcısı kayıtlı değil; kayıtlı olanlar: %s",
			wanted, strings.Join(r.sortedIDs(), ", "))
	}
	return p, nil
}

// Has sağlayıcının kayıtlı olup olmadığını bildirir.
//
// Seçenek oluştururken kullanılır: kaydedilmemiş bir sağlayıcıya bağlı seçenek,
// müşteriye gösterildiği anda ya da gönderi açılırken patlardı; hatanın
// yönetim yüzeyinde, seçenek yaratılırken görülmesi yeğdir.
func (r *ProviderRegistry) Has(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.providers[strings.TrimSpace(id)]
	return ok
}

// IDs kayıtlı sağlayıcı kimliklerini sıralı olarak döner.
func (r *ProviderRegistry) IDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sortedIDs()
}

// sortedIDs kayıtlı kimlikleri sıralı döner; çağıran kilidi tutuyor olmalıdır.
//
// Sıra sabittir: hata mesajları ve API yanıtları map üzerinde dönerek
// üretilseydi her çağrıda başka bir sırada çıkar, teşhisi ve testi
// zorlaştırırdı.
func (r *ProviderRegistry) sortedIDs() []string {
	out := make([]string, 0, len(r.providers))
	for id := range r.providers {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}
