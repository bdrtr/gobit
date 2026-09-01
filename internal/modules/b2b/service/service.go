// Package service b2b modülünün iş mantığını barındırır.
//
// # Modülün kırdığı varsayım
//
// Vitrin akışının B2C varsayımı "alıcı = birey"dir. B2B'de alıcı, HARCAMA
// YETKİSİ SINIRLI bir çalışandır: kimliği yine bir müşteri kaydıdır (customer
// modülü), ama ne kadar harcayabileceğini bağlı olduğu şirket belirler. Bu
// modül o iki bilgiyi — şirketi ve çalışanın yetkisini — tutar; kimliğin
// kendisini TUTMAZ.
//
// # Müşteri bağı neden link
//
// Çalışan ile müşteri arasındaki bağ core/link'tedir, bir sütunda değil
// (Prensip 2.2, ADR 0005). Bağın tekilliğini kardinalite zorlar: bir müşteri
// en fazla BİR çalışan kaydına sahip olabilir (bkz. [Definitions]). Vitrinin
// "kendi şirketim" sorusu ancak bu tekillik sayesinde tek bir cevaba çözülür.
//
// # Dışarıya açtığı yüzey
//
// Modül başka hiçbir modülü import etmez ve hiçbir modülün servisini çağırmaz.
// Müşterinin GERÇEKTEN var olduğu bu modülde doğrulanmaz: doğrulasaydı customer
// modülüne bağımlı olurdu ve bağ, tam da link'in kaldırmak için var olduğu
// bağımlılık olurdu. Var olmayan bir müşteriye bağlanmış çalışan kaydı, vitrinde
// hiçbir isteğe çözülmediği için zararsızdır.
package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/b2b/models"
)

// Hata kodları; çağıran taraf errors.CodeOf ile bunlara bakabilir.
const (
	// CodeInvalidInput girdinin doğrulamadan geçmediğini bildirir.
	CodeInvalidInput = "b2b_invalid_input"
	// CodeNotReady servisin kurulmadığını bildirir.
	CodeNotReady = "b2b_service_unconfigured"
	// CodeEmployeeNotFound istenen çalışanın bulunamadığını bildirir.
	CodeEmployeeNotFound = "b2b_employee_not_found"
	// CodeLinkFailed müşteri bağının kurulamadığını bildirir.
	CodeLinkFailed = "b2b_link_failed"
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
// internal/modules/b2b/repository paketindedir. Bu, ADR 0001'in örüntüsünün
// modül İÇİNDEKİ karşılığıdır ve servisin veritabanı olmadan test edilmesini
// sağlar.
type Repository interface {
	CreateCompany(ctx context.Context, c models.Company) (models.Company, error)
	GetCompany(ctx context.Context, id string) (models.Company, error)
	ListCompanies(ctx context.Context, filter models.CompanyFilter, limit, offset int64) ([]models.Company, int64, error)
	UpdateCompany(ctx context.Context, id string, patch models.CompanyPatch, now time.Time) (models.Company, error)
	// DeleteCompany şirketi ve çalışanlarını siler; dönen dilim silinen
	// çalışanların kimlikleridir (bağları servis kaldırır).
	DeleteCompany(ctx context.Context, id string, now time.Time) ([]string, error)

	CreateEmployee(ctx context.Context, e models.CompanyEmployee) (models.CompanyEmployee, error)
	GetEmployee(ctx context.Context, id string) (models.CompanyEmployee, error)
	ListEmployees(ctx context.Context, filter models.EmployeeFilter, limit, offset int64) ([]models.CompanyEmployee, int64, error)
	UpdateEmployee(ctx context.Context, id string, patch models.EmployeePatch, now time.Time) (models.CompanyEmployee, error)
	DeleteEmployee(ctx context.Context, id string, now time.Time) error
}

// Linker servisin modüller arası bağ katmanından ihtiyaç duyduğu DAR yüzeydir.
//
// core/link'in tam arayüzü tanım bildirimi ve ters yön okumaları da içerir;
// buradaki metotlar modülün GERÇEKTEN çağırdıklarıdır. Dar tutulması iki işe
// yarar: bağımlılık kullanılan yüzeyle sınırlanır ve birim testlerinde sahte
// bir bağ servisi birkaç satırda yazılabilir.
type Linker interface {
	// Create fromID ile toID arasında bağ kurar; aynı çift ikinci kez
	// bağlanırsa çağrı no-op'tur, kardinalite ihlali ise errors.Conflict.
	Create(ctx context.Context, name, fromID, toID string) error
	// Delete bağı kaldırır; bağ yoksa çağrı no-op'tur.
	Delete(ctx context.Context, name, fromID, toID string) error
	// ListMany birden çok çalışanın müşteri kimliklerini TEK sorguda döner.
	ListMany(ctx context.Context, name string, fromIDs []string) (map[string][]string, error)
	// ListManyByTo ters yönü çözer: verilen müşterilerin çalışan kimliklerini
	// döner. Vitrinin "kendi çalışan kaydım" sorusu bununla cevaplanır.
	ListManyByTo(ctx context.Context, name string, toIDs []string) (map[string][]string, error)
}

// Options servisin kurulum ayarlarıdır.
type Options struct {
	// Repo kalıcılık yüzeyidir; zorunludur.
	Repo Repository
	// Links modüller arası bağ servisidir; zorunludur.
	Links Linker
	// Logger yapısal log hedefidir; nil ise loglar atılır.
	Logger *slog.Logger
	// Now zaman kaynağıdır; nil ise time.Now kullanılır. Testler burayı sabit
	// bir saatle doldurarak zamana bağlı dalları belirlenimci hâle getirir.
	Now func() time.Time
}

// Service b2b modülünün public servisidir. Eşzamanlı kullanıma güvenlidir.
type Service struct {
	repo  Repository
	links Linker
	log   *slog.Logger
	now   func() time.Time
}

// New verilen bağımlılıklarla bir servis üretir.
//
// Eksik bir bağımlılık KURULUM anında hata döner. Link servisi olmadan modül
// "sessizce yarım" çalışırdı: çalışan satırları yazılır, ama hiçbiri bir
// müşteriye bağlanmaz ve eksiklik ancak vitrinde, hiçbir müşterinin şirketini
// bulamamasıyla görünürdü.
func New(opts Options) (*Service, error) {
	if opts.Repo == nil {
		return nil, errors.Internal(CodeNotReady, "b2b servisi depo olmadan kurulamaz")
	}
	if opts.Links == nil {
		return nil, errors.Internal(CodeNotReady, "b2b servisi link servisi olmadan kurulamaz")
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Service{repo: opts.Repo, links: opts.Links, log: log, now: now}, nil
}

// clock geçerli anı UTC olarak döner.
func (s *Service) clock() time.Time {
	return s.now().UTC()
}
