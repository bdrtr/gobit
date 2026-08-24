package service

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// LocalProviderID kutudan çıkan yerel hesaplama sağlayıcısının kimliğidir.
//
// Bölge zincirindeki HİÇBİR provider_id dolu değilse bu sağlayıcı kullanılır.
// Boş değer "yapılandırılmamış" DEĞİL, "ebeveynimin sağlayıcısı — o da yoksa
// yerel" demektir (çözüm sırası: [Service.providerFor]). Alanın doldurulmasını
// zorunlu kılmak, tek sağlayıcılı kurulumların her bölgeye aynı dizeyi
// yazmasını gerektirirdi.
//
// Ülkesi dış bir otoriteye bağlı bir eyalet yerel hesabı kullanmak istiyorsa bu
// kimliği AÇIKÇA yazar: "yerel" niyeti boş dizeyle değil ADIYLA ifade edilir.
// Ayrım şarttır — boş dize devralmayı, bu kimlik yereli anlatır ve ikisi aynı
// değere binseydi biri ifade edilemez olurdu.
const LocalProviderID = "local"

// TaxProvider bir vergi hesaplama sağlayıcısının bu modüle sunduğu
// sözleşmedir.
//
// # Neden çekirdekte değil
//
// Plan Bölüm 6 "TaxProvider" der, ama internal/core/provider yalnızca
// PaymentProvider ve FulfillmentProvider tanımlar ve bu modül çekirdeğe
// dokunamaz. Sözleşme bu yüzden BURADA yaşar. Karar geçicidir: ikinci bir
// gerçek sağlayıcı (Avalara/TaxJar gibi) yazıldığında arayüz
// internal/core/provider/tax.go'ya taşınmalı ve buradaki tipler takma ad
// hâline getirilmelidir. İmzalar bu taşımayı ucuzlatacak biçimde, çekirdekteki
// iki sağlayıcıyla aynı kalıpta yazılmıştır.
//
// # Yan etkisizlik
//
// Calculate YAN ETKİSİZDİR ve tekrar çağrılabilir: sepet toplamı her
// değişiklikte yeniden hesaplandığı için aynı girdiyle defalarca çağrılır ve
// aynı sonucu vermelidir. Bir sağlayıcı bu çağrıyı dış servise taşıyorsa
// önbelleklemeyi kendi üstlenmelidir; bu modül çağrıyı önbelleklemez.
//
// # Aritmetiği kim yapar
//
// Sağlayıcı hem uygulanan ORANI hem hesapladığı TUTARI döner. Yerel sağlayıcı
// tutarı [TaxOf] ile hesaplar, yani yuvarlama yönü modülün garantisidir. Dış
// bir sağlayıcı kendi tutarını döndürebilir — çoğu dış servis oranı değil
// tutarı yetkili sayar — ama sonuç DOĞRULANIR: tutar [0, taban] aralığında,
// oran [0, %100] aralığında olmalıdır (bkz. [Service.CalculateTax]).
// Doğrulama, bir sağlayıcı arızasının sepet toplamını sessizce bozmasını
// engeller.
type TaxProvider interface {
	// ID sağlayıcının benzersiz kimliğidir; bölge kaydındaki provider_id ile
	// eşleşen değer budur.
	ID() string

	// Calculate verilen bölge zinciri ve kalemler için vergiyi hesaplar.
	Calculate(ctx context.Context, in ProviderInput) (ProviderResult, error)
}

// ProviderInput bir vergi hesaplamasının sağlayıcıya giden girdisidir.
type ProviderInput struct {
	// RegionIDs çözülmüş bölge zinciridir: en ÖZELDEN genele (eyalet, sonra
	// ülke). Yerel sağlayıcı oranlarını bu kimliklerle okur; dış sağlayıcılar
	// alanı yok sayabilir.
	RegionIDs []string
	// CountryCode ISO 3166-1 alpha-2 kodudur (BÜYÜK harf).
	CountryCode string
	// ProvinceCode eyalet/il kodudur; verilmediyse boş.
	ProvinceCode string
	// Items vergilendirilecek kalemlerdir.
	Items []TaxableItem
	// Shipping kargo satırıdır; vergilendirilmeyecekse
	// [ShippingInput.Taxable] false'tur.
	Shipping ShippingInput
}

// ProviderResult sağlayıcının hesabıdır.
//
// Kalem sırası girdideki sırayla AYNI olmak zorunda değildir; eşleştirme
// [ProviderItemTax.ID] üzerinden yapılır. Girdide olmayan bir kimlik ya da
// eksik bir kalem, sözleşme ihlalidir ve hesap reddedilir.
type ProviderResult struct {
	// Items kalem başına hesaplanan vergidir.
	Items []ProviderItemTax
	// Shipping kargo satırının vergisidir; vergilendirilmediyse sıfır.
	Shipping ProviderItemTax
}

// ProviderItemTax tek bir kalemin sağlayıcı tarafından hesaplanan vergisidir.
type ProviderItemTax struct {
	// ID kalemin çağıran tarafındaki kimliğidir.
	ID string
	// RateID uygulanan oranın kimliğidir; dış sağlayıcılarda boş olabilir.
	RateID string
	// RateBps uygulanan orandır (baz puan).
	RateBps int32
	// TaxAmount hesaplanan vergidir (minor unit).
	TaxAmount int64
}

// ProviderRegistry vergi sağlayıcılarını kimlikleriyle tutar.
//
// Modül kendi varsayılan sağlayıcısını ([LocalProvider]) Register sırasında
// buraya koyar ve kaydı container'a "tax.providers" adıyla verir. Faz 9'daki
// plugin sistemi, çekirdeğe ve bu modüle DOKUNMADAN, container'dan kaydı çözüp
// kendi sağlayıcısını ekleyebilir.
//
// Eşzamanlı kullanıma güvenlidir: kayıt açılışta, okuma her istekte yapılır.
type ProviderRegistry struct {
	mu        sync.RWMutex
	providers map[string]TaxProvider
}

// NewProviderRegistry boş bir sağlayıcı kaydı üretir.
func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{providers: make(map[string]TaxProvider)}
}

// Register sağlayıcıyı kendi kimliğiyle kaydeder.
//
// Aynı kimlikle ikinci bir kayıt errors.Conflict döner ve mevcut sağlayıcı
// KORUNUR. Sessizce üzerine yazmak, iki eklentinin aynı kimliği kullandığı bir
// kurulumda hangi sağlayıcının çalıştığını yükleme sırasına bırakırdı — vergide
// bunun bedeli, yanlış oranla kesilmiş faturalardır.
func (r *ProviderRegistry) Register(p TaxProvider) error {
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
			"%q kimlikli bir vergi sağlayıcısı zaten kayıtlı", id)
	}
	r.providers[id] = p
	return nil
}

// Get sağlayıcıyı kimliğiyle döner; kayıtlı değilse errors.NotFound.
//
// Boş kimlik [LocalProviderID] anlamına gelir. Bölge zincirindeki devralma bu
// çağrıdan ÖNCE çözülür ([Service.providerFor]); buraya boş bir kimlik ancak
// zincirin tamamı boşsa gelir.
//
// Hata mesajı ARANAN kimliği ve KAYITLI kimlikleri birlikte yazar; bir
// sağlayıcının kaydedilmeyi unutulması çalışma zamanında ortaya çıkan bir
// kurulum hatasıdır ve teşhis edilebilir olmalıdır (bkz. ADR 0002).
func (r *ProviderRegistry) Get(id string) (TaxProvider, error) {
	wanted := strings.TrimSpace(id)
	if wanted == "" {
		wanted = LocalProviderID
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.providers[wanted]
	if !ok {
		return nil, errors.NotFound(CodeProviderNotFound,
			"%q vergi sağlayıcısı kayıtlı değil; kayıtlı olanlar: %s",
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

// RateSource yerel sağlayıcının oran kaynağıdır.
//
// Arayüz TÜKETEN tarafta tanımlıdır (ADR 0001'in modül içi karşılığı); somut
// uygulama repository paketidir. Yerel sağlayıcının veritabanı olmadan test
// edilebilmesini sağlayan sınır budur.
type RateSource interface {
	// ListTaxRatesByRegions bölge zincirindeki oranları TEK turda döner.
	ListTaxRatesByRegions(ctx context.Context, regionIDs []string) ([]models.TaxRate, error)
	// ListTaxRateRulesByRates verilen oranların kurallarını TEK turda döner.
	ListTaxRateRulesByRates(ctx context.Context, rateIDs []string) ([]models.TaxRateRule, error)
}
