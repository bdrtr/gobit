// Package service customer modülünün iş mantığını barındırır.
//
// # Modüller arası yüzey (ADR 0001)
//
// customer hiçbir modülü import ETMEZ ve hiçbir modülden veri OKUMAZ; bu yüzden
// bu pakette tüketici tarafı bir arayüz yoktur. Ters yön vardır: cart (Faz 5) ve
// order (Faz 6) müşteriye ihtiyaç duyar. O tarafın kendi paketinde dar bir
// arayüz tanımlayabilmesi için customer'ın yüzeyi İKİYE ayrılmıştır:
//
//   - Modül içi zengin yüzey — [models] tiplerini kullanır ([Service.CreateCustomer],
//     [Service.ListAddresses] …). Bu metotları yalnızca customer'ın kendi API
//     katmanı ve query sağlayıcısı çağırır.
//   - Modüller arası yüzey — YALNIZCA ilkel ve stdlib tipleri kullanır
//     (bkz. interop.go).
//
// Ayrım zorunludur: Go'da yapısal uyum imza EŞİTLİĞİ ister. Tüketici modül
// customer'ı import edemediği için [models.Customer] gibi bir tipi imzasında
// adlandıramaz; adlandırdığı an kendi paketindeki farklı bir tip olur ve somut
// servis arayüzü karşılamaz.
//
// # Misafir ve hesap
//
// Modülün en önemli kararı e-posta benzersizliğinin misafirlerde
// UYGULANMAMASIDIR; gerekçesi [models.Customer] godoc'unda yazılıdır. Servis
// bu kuralı tekrar etmez — kural veritabanındaki kısmi benzersiz indekstedir ve
// tekrarlansaydı iki eşzamanlı kayıt arasındaki yarışı yine indeks çözerdi.
// Servisin işi, indeksin ürettiği çakışmayı ANLAŞILIR bir hataya çevirmektir.
package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// Hata kodları; çağıran taraf errors.CodeOf ile bunlara bakabilir.
const (
	// CodeInvalidInput girdinin doğrulamadan geçmediğini bildirir.
	CodeInvalidInput = "customer_invalid_input"
	// CodeCustomerNotFound istenen müşterinin bulunamadığını bildirir.
	CodeCustomerNotFound = "customer_not_found"
)

// Sayfalama sınırları. Limit verilmezse varsayılan uygulanır; aşırı büyük bir
// limit reddedilir, böylece istemci tek istekle veritabanını tarayamaz.
const (
	// DefaultLimit limit verilmediğinde uygulanan sayfa boyutudur.
	DefaultLimit int64 = 50
	// MaxLimit tek istekte istenebilecek en büyük sayfa boyutudur.
	MaxLimit int64 = 100
)

// Page sayfalanmış bir liste sonucudur.
//
// Limit ve Offset, isteğin ham değerleri değil UYGULANAN değerlerdir; API zarfı
// bu alanları olduğu gibi yazar, böylece istemci varsayılana düşen bir limitten
// haberdar olur.
type Page[T any] struct {
	// Items geçerli sayfadaki kayıtlardır.
	Items []T
	// Count filtreye uyan TOPLAM kayıt sayısıdır (sayfa boyu değil).
	Count int64
	// Limit uygulanan sayfa boyudur.
	Limit int64
	// Offset uygulanan atlama sayısıdır.
	Offset int64
}

// Repository servisin ihtiyaç duyduğu veri erişim yüzeyidir.
//
// Arayüz TÜKETEN tarafta (burada) tanımlıdır; somut uygulama
// internal/modules/customer/repository paketindedir. Bu, ADR 0001'in
// örüntüsünün modül İÇİNDEKİ karşılığıdır ve servisin veritabanı olmadan test
// edilmesini sağlar.
type Repository interface {
	CreateCustomer(ctx context.Context, c models.Customer) (models.Customer, error)
	GetCustomer(ctx context.Context, id string) (models.Customer, error)
	GetAccountByEmail(ctx context.Context, email string) (models.Customer, error)
	ListCustomers(ctx context.Context, filter models.CustomerFilter, limit, offset int64) ([]models.Customer, int64, error)
	GetCustomersByIDs(ctx context.Context, ids []string) ([]models.Customer, error)
	UpdateCustomer(ctx context.Context, id string, patch models.CustomerPatch, now time.Time) (models.Customer, error)
	PromoteGuest(ctx context.Context, id string, now time.Time) (models.Customer, error)
	DeleteCustomer(ctx context.Context, id string, now time.Time) error

	CreateGroup(ctx context.Context, g models.CustomerGroup) (models.CustomerGroup, error)
	GetGroup(ctx context.Context, id string) (models.CustomerGroup, error)
	ListGroups(ctx context.Context, limit, offset int64) ([]models.CustomerGroup, int64, error)
	UpdateGroup(ctx context.Context, id string, patch models.CustomerGroupPatch, now time.Time) (models.CustomerGroup, error)
	DeleteGroup(ctx context.Context, id string, now time.Time) error
	AddToGroup(ctx context.Context, customerID, groupID string, now time.Time) error
	RemoveFromGroup(ctx context.Context, customerID, groupID string) error
	ListGroupsOf(ctx context.Context, customerID string) ([]models.CustomerGroup, error)
	GroupIDsOfCustomers(ctx context.Context, customerIDs []string) (map[string][]string, error)

	CreateAddress(ctx context.Context, a models.CustomerAddress) (models.CustomerAddress, error)
	GetAddress(ctx context.Context, customerID, addressID string) (models.CustomerAddress, error)
	ListAddresses(ctx context.Context, customerID string) ([]models.CustomerAddress, error)
	UpdateAddress(ctx context.Context, customerID, addressID string, patch models.AddressPatch, now time.Time) (models.CustomerAddress, error)
	DeleteAddress(ctx context.Context, customerID, addressID string, now time.Time) error
	SetDefaultAddress(ctx context.Context, customerID, addressID string, kind models.DefaultKind, now time.Time) (models.CustomerAddress, error)
}

// Options servisin kurulum ayarlarıdır.
type Options struct {
	// Logger yapısal log hedefidir; nil ise loglar atılır.
	Logger *slog.Logger
	// Now zaman kaynağıdır; nil ise time.Now kullanılır. Testler burayı sabit
	// bir saatle doldurarak zamana bağlı dalları belirlenimci hâle getirir.
	Now func() time.Time
}

// Service customer modülünün public servisidir. Eşzamanlı kullanıma güvenlidir.
type Service struct {
	repo Repository
	log  *slog.Logger
	now  func() time.Time
}

// New verilen depo üzerinde çalışan bir servis üretir.
//
// repo nil ise bu, kurulumda değil ilk çağrıda tipli bir hata olarak bildirilir;
// kurulum yolu panik üretmez.
func New(repo Repository, opts Options) *Service {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Service{repo: repo, log: log, now: now}
}

// ready deponun kurulu olduğunu doğrular.
func (s *Service) ready() error {
	if s == nil || s.repo == nil {
		return errors.Unavailable("customer_service_unconfigured", "customer servisi kurulmamış")
	}
	return nil
}

// clock geçerli anı UTC olarak döner.
func (s *Service) clock() time.Time {
	return s.now().UTC()
}

// CustomerInput bir müşterinin yazma girdisidir.
//
// Hem hesap oluşturmada ([Service.CreateCustomer]) hem misafir kaydında
// ([Service.RegisterGuest]) kullanılır; ikisini ayıran şey girdi değil ÇAĞRILAN
// METOTTUR. Ayrımın bir boolean alanla taşınmaması bilinçlidir: böyle bir alan,
// yönetim ucuna gelen bir isteğin sessizce misafir kaydı açmasına izin verirdi.
type CustomerInput struct {
	// Email müşterinin e-posta adresidir; zorunludur, küçük harfe normalize
	// edilerek saklanır.
	Email string
	// FirstName müşterinin adıdır; boş bırakılabilir.
	FirstName string
	// LastName müşterinin soyadıdır; boş bırakılabilir.
	LastName string
	// Phone müşterinin telefonudur; boş bırakılabilir.
	Phone string
	// Metadata serbest yapısal bağlamdır; boş bırakılabilir.
	Metadata map[string]any
}

// CreateCustomer KAYITLI bir müşteri hesabı oluşturur.
//
// E-posta zaten bir hesaba aitse errors.Conflict döner. Misafir kaydı için
// [Service.RegisterGuest] kullanılır; ikisi arasındaki fark
// [models.Customer.HasAccount] alanıdır ve benzersizlik kuralını da o belirler.
func (s *Service) CreateCustomer(ctx context.Context, in CustomerInput) (models.Customer, error) {
	return s.createCustomer(ctx, in, true)
}

// RegisterGuest MİSAFİR bir müşteri kaydı oluşturur.
//
// Aynı e-postayla daha önce misafir kaydı ya da kayıtlı bir hesap bulunması
// engel DEĞİLDİR: misafir kaydı bir kimlik değil, tek seferlik bir alışverişin
// iletişim bilgisidir (gerekçe için bkz. [models.Customer]). Vitrin bu yüzden
// müşteriyi "bu e-posta kullanılıyor" diye geri çeviremez.
func (s *Service) RegisterGuest(ctx context.Context, in CustomerInput) (models.Customer, error) {
	return s.createCustomer(ctx, in, false)
}

// createCustomer iki kayıt yolunun ortak gövdesidir.
func (s *Service) createCustomer(ctx context.Context, in CustomerInput, hasAccount bool) (models.Customer, error) {
	if err := s.ready(); err != nil {
		return models.Customer{}, err
	}

	email, err := normalizeEmail(in.Email)
	if err != nil {
		return models.Customer{}, err
	}
	if err := validatePerson(in.FirstName, in.LastName, in.Phone); err != nil {
		return models.Customer{}, err
	}

	now := s.clock()
	created, err := s.repo.CreateCustomer(ctx, models.Customer{
		ID:         models.NewCustomerID(now),
		Email:      email,
		FirstName:  in.FirstName,
		LastName:   in.LastName,
		Phone:      in.Phone,
		HasAccount: hasAccount,
		Metadata:   in.Metadata,
		CreatedAt:  now,
	})
	if err != nil {
		return models.Customer{}, err
	}

	// E-posta hassas veridir ve loglanmaz (plan Bölüm 8); kimlik ve kayıt türü
	// bir çağrının izini sürmeye yeter.
	s.log.DebugContext(ctx, "müşteri oluşturuldu",
		slog.String("customer_id", created.ID),
		slog.Bool("has_account", created.HasAccount),
	)
	return created, nil
}

// GetCustomer kimliğe göre müşteri döner; yoksa errors.NotFound.
func (s *Service) GetCustomer(ctx context.Context, id string) (models.Customer, error) {
	if err := s.ready(); err != nil {
		return models.Customer{}, err
	}
	if err := requireID(id, models.CustomerIDPrefix, "customer id"); err != nil {
		return models.Customer{}, err
	}
	return s.repo.GetCustomer(ctx, id)
}

// GetCustomerByEmail e-postaya göre KAYITLI hesabı döner; yoksa errors.NotFound.
//
// Misafir kayıtları bilinçli olarak dışarıda bırakılır: aynı e-postayla birden
// çok misafir olabildiği için sorunun misafirler arasında tek bir doğru yanıtı
// yoktur. Misafir kayıtlarını görmek isteyen çağıran
// [Service.ListCustomers]'ı e-posta süzgeciyle kullanır.
func (s *Service) GetCustomerByEmail(ctx context.Context, email string) (models.Customer, error) {
	if err := s.ready(); err != nil {
		return models.Customer{}, err
	}
	normalized, err := normalizeEmail(email)
	if err != nil {
		return models.Customer{}, err
	}
	return s.repo.GetAccountByEmail(ctx, normalized)
}

// ListCustomersInput müşteri listelemesinin girdisidir.
type ListCustomersInput struct {
	// Email verilirse yalnızca bu e-postaya sahip müşteriler döner
	// (misafirler dâhil). Değer normalize edilerek uygulanır.
	Email *string
	// HasAccount verilirse misafir/kayıtlı ayrımına göre süzer.
	HasAccount *bool
	// GroupID verilirse yalnızca bu grubun üyeleri döner.
	GroupID *string
	// Limit sayfa boyudur; 0 ise [DefaultLimit] uygulanır.
	Limit int64
	// Offset atlanacak kayıt sayısıdır.
	Offset int64
}

// ListCustomers süzgeçlenmiş ve sayfalanmış müşteri listesini döner.
func (s *Service) ListCustomers(ctx context.Context, in ListCustomersInput) (Page[models.Customer], error) {
	if err := s.ready(); err != nil {
		return Page[models.Customer]{}, err
	}

	limit, offset, err := normalizePaging(in.Limit, in.Offset)
	if err != nil {
		return Page[models.Customer]{}, err
	}

	filter := models.CustomerFilter{HasAccount: in.HasAccount}
	if in.Email != nil {
		normalized, emailErr := normalizeEmail(*in.Email)
		if emailErr != nil {
			return Page[models.Customer]{}, emailErr
		}
		filter.Email = &normalized
	}
	if in.GroupID != nil {
		if idErr := requireID(*in.GroupID, models.CustomerGroupIDPrefix, "group id"); idErr != nil {
			return Page[models.Customer]{}, idErr
		}
		filter.GroupID = in.GroupID
	}

	items, total, err := s.repo.ListCustomers(ctx, filter, limit, offset)
	if err != nil {
		return Page[models.Customer]{}, err
	}
	return Page[models.Customer]{Items: items, Count: total, Limit: limit, Offset: offset}, nil
}

// UpdateCustomerInput bir müşterinin kısmi güncelleme girdisidir.
//
// nil alan "dokunma", dolu alan "bu değeri yaz" demektir.
type UpdateCustomerInput struct {
	// Email yeni e-postadır; normalize edilerek yazılır.
	Email *string
	// FirstName yeni addır.
	FirstName *string
	// LastName yeni soyaddır.
	LastName *string
	// Phone yeni telefondur.
	Phone *string
	// Metadata yeni metadata haritasıdır; sütunun tamamını değiştirir.
	Metadata map[string]any
}

// UpdateCustomer müşterinin verilen alanlarını günceller.
//
// Kayıtlı bir hesabın e-postası başka bir hesap tarafından kullanılıyorsa
// errors.Conflict döner. [models.Customer.HasAccount] burada DEĞİŞTİRİLEMEZ;
// misafirden hesaba geçiş için [Service.ConvertGuestToAccount] kullanılır.
func (s *Service) UpdateCustomer(ctx context.Context, id string, in UpdateCustomerInput) (models.Customer, error) {
	if err := s.ready(); err != nil {
		return models.Customer{}, err
	}
	if err := requireID(id, models.CustomerIDPrefix, "customer id"); err != nil {
		return models.Customer{}, err
	}

	patch := models.CustomerPatch{
		FirstName: in.FirstName,
		LastName:  in.LastName,
		Phone:     in.Phone,
		Metadata:  in.Metadata,
	}
	if in.Email != nil {
		normalized, err := normalizeEmail(*in.Email)
		if err != nil {
			return models.Customer{}, err
		}
		patch.Email = &normalized
	}
	if err := validatePatchPerson(patch); err != nil {
		return models.Customer{}, err
	}

	return s.repo.UpdateCustomer(ctx, id, patch, s.clock())
}

// DeleteCustomer müşteriyi ve adreslerini soft delete ile siler.
func (s *Service) DeleteCustomer(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(id, models.CustomerIDPrefix, "customer id"); err != nil {
		return err
	}
	return s.repo.DeleteCustomer(ctx, id, s.clock())
}

// ConvertGuestToAccount misafir kaydını kayıtlı hesaba çevirir.
//
// Kaydın e-postası zaten kayıtlı bir hesaba aitse errors.Conflict döner: iki
// hesabın aynı e-postayı paylaşması, Faz 8'de gelecek "e-posta ile giriş"in
// hangi kaydı seçeceğini bilememesi demekti. Kayıt zaten hesapsa da
// errors.Conflict döner; sessiz bir no-op, çağırana dönüşümün gerçekleştiğini
// söylerdi.
//
// Karar müşteri satırı KİLİTLİYKEN verilir ve kısmi benzersiz indeks son kapı
// olarak kalır (bkz. repository.Repo.PromoteGuest).
func (s *Service) ConvertGuestToAccount(ctx context.Context, customerID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(customerID, models.CustomerIDPrefix, "customer id"); err != nil {
		return err
	}

	converted, err := s.repo.PromoteGuest(ctx, customerID, s.clock())
	if err != nil {
		return err
	}

	s.log.InfoContext(ctx, "misafir hesaba çevrildi",
		slog.String("customer_id", converted.ID),
	)
	return nil
}
