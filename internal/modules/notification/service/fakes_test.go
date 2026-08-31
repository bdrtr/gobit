package service_test

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/modules/notification/models"
	"github.com/bdrtr/gobit/internal/modules/notification/service"
)

// fakeStore service.Store'un bellek içi karşılığıdır.
//
// Gerçek depodan taklit edilen TEK davranış idempotency anahtarıdır: aynı
// (şablon, referans) çifti için ikinci bir kayıt AÇILMAZ. Servisin
// "mükerrer bildirim göndermez" iddiası tamamen buna dayanır ve gerçek
// veritabanında bunu sağlayan şey benzersiz indekstir; sahte depo aynı kuralı
// haritayla uygular, böylece iddia Docker olmadan da sınanabilir. Kısıtın
// gerçekten kurulu olduğu entegrasyon testinde ayrıca doğrulanır.
type fakeStore struct {
	mu sync.Mutex
	// kayitlar kimliğe göre günlük kayıtlarıdır.
	kayitlar map[string]models.Delivery
	// anahtarlar "<şablon>\x00<referans>" -> kayıt kimliği eşlemesidir;
	// benzersiz indeksin karşılığıdır.
	anahtarlar map[string]string
	// claimSayisi ClaimDelivery'nin kaç kez ÇAĞRILDIĞINI sayar.
	claimSayisi int

	// claimErr ayarlanırsa ClaimDelivery bu hatayı döner.
	claimErr error
	// finishErr ayarlanırsa FinishDelivery bu hatayı döner; sonucun
	// yazılamadığı yol bununla sınanır.
	finishErr error
	// listErr ayarlanırsa ListDeliveries bu hatayı döner.
	listErr error
}

// newFakeStore boş bir sahte depo üretir.
func newFakeStore() *fakeStore {
	return &fakeStore{
		kayitlar:   map[string]models.Delivery{},
		anahtarlar: map[string]string{},
	}
}

// anahtar idempotency anahtarını üretir.
//
// Ayırıcı olarak NUL kullanılır: "a.b"+"c" ile "a"+"b.c" gibi iki farklı çiftin
// aynı anahtara düşmesi, sahte deponun gerçek indeksten daha katı davranması
// demekti ve test, olmayan bir çakışmayı doğrulardı.
func anahtar(template, reference string) string { return template + "\x00" + reference }

func (s *fakeStore) ClaimDelivery(_ context.Context, d models.Delivery) (models.Delivery, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.claimSayisi++
	if s.claimErr != nil {
		return models.Delivery{}, false, s.claimErr
	}

	key := anahtar(d.Template, d.Reference)
	if _, varsa := s.anahtarlar[key]; varsa {
		return models.Delivery{}, false, nil
	}

	d.CreatedAt = time.Now().UTC()
	d.UpdatedAt = d.CreatedAt
	s.kayitlar[d.ID] = d
	s.anahtarlar[key] = d.ID

	return d, true, nil
}

func (s *fakeStore) FinishDelivery(
	_ context.Context,
	id string,
	status models.DeliveryStatus,
	failure string,
) (models.Delivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.finishErr != nil {
		return models.Delivery{}, s.finishErr
	}

	kayit, ok := s.kayitlar[id]
	if !ok {
		return models.Delivery{}, errors.NotFound("test_not_found", "kayıt yok: %s", id)
	}

	kayit.Status = status
	kayit.Error = failure
	kayit.UpdatedAt = time.Now().UTC()
	s.kayitlar[id] = kayit

	return kayit, nil
}

func (s *fakeStore) GetDelivery(_ context.Context, id string) (models.Delivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	kayit, ok := s.kayitlar[id]
	if !ok {
		return models.Delivery{}, errors.NotFound("test_not_found", "kayıt yok: %s", id)
	}
	return kayit, nil
}

func (s *fakeStore) ListDeliveries(
	_ context.Context,
	filter models.DeliveryFilter,
) ([]models.Delivery, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.listErr != nil {
		return nil, 0, s.listErr
	}

	out := make([]models.Delivery, 0, len(s.kayitlar))
	for id := range s.kayitlar {
		kayit := s.kayitlar[id]
		if filter.Reference != nil && kayit.Reference != *filter.Reference {
			continue
		}
		if filter.Status != nil && kayit.Status.String() != *filter.Status {
			continue
		}
		out = append(out, kayit)
	}
	return out, int64(len(out)), nil
}

// tumKayitlar deponun tüm kayıtlarını döner (test iddiaları için).
func (s *fakeStore) tumKayitlar() []models.Delivery {
	kayitlar, _, _ := s.ListDeliveries(context.Background(), models.DeliveryFilter{})
	return kayitlar
}

// fakeProvider coreprovider.NotificationProvider'ın senaryolanabilir
// karşılığıdır.
type fakeProvider struct {
	mu sync.Mutex
	id string
	// gonderilen sağlayıcıya ulaşan bildirimlerdir; SAYISI, "ikinci kez
	// gönderilmedi" iddiasının tek kanıtıdır.
	gonderilen []coreprovider.Notification
	// err ayarlanırsa Send bu hatayı döner.
	err error
}

// newFakeProvider verilen kimlikle bir sahte sağlayıcı üretir.
func newFakeProvider(id string) *fakeProvider { return &fakeProvider{id: id} }

func (p *fakeProvider) ID() string { return p.id }

func (p *fakeProvider) Send(_ context.Context, n coreprovider.Notification) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Çağrı, sonucu ne olursa olsun KAYDEDİLİR: sayaç "kaç bildirim başarıyla
	// gitti"yi değil "sağlayıcıya kaç kez gidildi"yi ölçmelidir. Başarısız
	// denemeyi saymayan bir sayaç, "başarısızdan sonra yeniden denenmiyor"
	// iddiasını sınayamazdı.
	p.gonderilen = append(p.gonderilen, n)

	return p.err
}

// cagriSayisi sağlayıcıya kaç kez gidildiğini döner.
func (p *fakeProvider) cagriSayisi() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.gonderilen)
}

// sonBildirim sağlayıcıya ulaşan son bildirimi döner.
func (p *fakeProvider) sonBildirim() coreprovider.Notification {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.gonderilen) == 0 {
		return coreprovider.Notification{}
	}
	return p.gonderilen[len(p.gonderilen)-1]
}

// fakeContacts service.OrderContactReader'ın senaryolanabilir karşılığıdır.
//
// Gerçek yüzeyin (order.interop) gövdesini DİZE olarak üretir: tipli bir
// yapıdan kodlamak, iki tarafın aynı Go tipini paylaştığı yanılsamasını
// verirdi — oysa paylaşılan tek şey JSON ŞEMASIDIR ve ayrışma tam da orada
// olur.
type fakeContacts struct {
	mu sync.Mutex
	// govde okunacak ham yanıttır.
	govde string
	// err ayarlanırsa okuma bu hatayı döner.
	err error
	// istenen son çağrının sipariş kimliğidir.
	istenen string
	// cagri okuma sayısıdır.
	cagri int
}

func (c *fakeContacts) OrderContactJSON(_ context.Context, orderID string) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cagri++
	c.istenen = orderID
	if c.err != nil {
		return nil, c.err
	}
	return json.RawMessage(c.govde), nil
}

// yeniServis sahte bağımlılıklarla bir servis kurar.
func yeniServis(store service.Store, providers *service.ProviderRegistry, id string, contacts service.OrderContactReader) (*service.Service, error) {
	return service.New(service.Options{
		Store:      store,
		Providers:  providers,
		ProviderID: id,
		Contacts:   contacts,
	})
}
