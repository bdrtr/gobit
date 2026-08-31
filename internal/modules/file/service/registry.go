package service

import (
	"slices"
	"strings"
	"sync"

	"github.com/bdrtr/gobit/internal/core/errors"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
)

// ProviderRegistry dosya sağlayıcılarını kimlikleriyle tutar.
//
// Modül kendi varsayılan sağlayıcısını ([local.Provider]) Register sırasında
// buraya koyar ve kaydı container'a "file.providers" adıyla verir. Eklenti
// sistemi (coreplugin.Host.RegisterFileProvider), çekirdeğe ve bu modüle
// DOKUNMADAN kaydı çözüp kendi sağlayıcısını ekler; sözleşme
// internal/core/provider'daki FileProvider arayüzüdür.
//
// Eşzamanlı kullanıma güvenlidir: kayıt açılışta, okuma her yükleme ve her
// sunum isteğinde yapılır.
type ProviderRegistry struct {
	mu        sync.RWMutex
	providers map[string]coreprovider.FileProvider
}

// NewProviderRegistry boş bir sağlayıcı kaydı üretir.
func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{providers: make(map[string]coreprovider.FileProvider)}
}

// Register sağlayıcıyı kendi kimliğiyle kaydeder.
//
// Aynı kimlikle ikinci bir kayıt errors.Conflict döner ve mevcut sağlayıcı
// KORUNUR. Sessizce üzerine yazmak, iki eklentinin aynı kimliği kullandığı bir
// kurulumda hangi sağlayıcının çalıştığını yükleme sırasına bırakırdı — burada
// bunun bedeli somuttur: kimlik kayıtlara YAZILIR ve dosyayı okuyabilecek tek
// şey onu yazan sağlayıcıdır. Sıra değiştiği gün, dün yazılan dosyalar bugün
// okunamaz.
func (r *ProviderRegistry) Register(p coreprovider.FileProvider) error {
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
			"%q kimlikli bir dosya sağlayıcısı zaten kayıtlı", id)
	}
	r.providers[id] = p

	return nil
}

// Get sağlayıcıyı kimliğiyle döner; kayıtlı değilse errors.NotFound.
//
// Hata mesajı ARANAN kimliği ve KAYITLI kimlikleri birlikte yazar: bir
// sağlayıcının kaydedilmeyi unutulması (ya da FILE_PROVIDER'ın yanlış
// yazılması) çalışma zamanında ortaya çıkan bir kurulum hatasıdır ve teşhis
// edilebilir olmalıdır (bkz. ADR 0002).
func (r *ProviderRegistry) Get(id string) (coreprovider.FileProvider, error) {
	wanted := strings.TrimSpace(id)
	if wanted == "" {
		return nil, errors.Invalid(CodeInvalidInput, "sağlayıcı kimliği boş olamaz")
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.providers[wanted]
	if !ok {
		return nil, errors.NotFound(CodeProviderNotFound,
			"%q dosya sağlayıcısı kayıtlı değil; kayıtlı olanlar: %s",
			wanted, strings.Join(r.sortedIDs(), ", "))
	}

	return p, nil
}

// IDs kayıtlı sağlayıcı kimliklerini sıralı olarak döner.
func (r *ProviderRegistry) IDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.sortedIDs()
}

// sortedIDs kayıtlı kimlikleri sıralı döner; çağıran kilidi tutuyor olmalıdır.
//
// Sıra sabittir: hata mesajları map üzerinde dönerek üretilseydi her çağrıda
// başka bir sırada çıkar, teşhisi ve testi zorlaştırırdı.
func (r *ProviderRegistry) sortedIDs() []string {
	out := make([]string, 0, len(r.providers))
	for id := range r.providers {
		out = append(out, id)
	}
	slices.Sort(out)

	return out
}
