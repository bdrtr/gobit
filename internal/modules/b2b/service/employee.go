package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/b2b/models"
)

// EmployeeInput bir çalışanın yazma girdisidir.
type EmployeeInput struct {
	// CompanyID çalışanın bağlanacağı şirkettir; zorunludur.
	CompanyID string
	// CustomerID çalışanın müşteri kaydıdır (customer modülü); zorunludur.
	//
	// Müşterinin GERÇEKTEN var olduğu doğrulanmaz: doğrulamak customer
	// modülüne bağımlılık demek olurdu ve bu bağ, link katmanının kaldırmak
	// için var olduğu bağın ta kendisidir (ADR 0001).
	CustomerID string
	// SpendingLimit çalışanın pencere başına harcayabileceği azami tutardır
	// (minor unit); nil SINIRSIZ demektir, 0 gerçek bir sıfır limittir.
	SpendingLimit *int64
	// IsCompanyAdmin çalışanın şirket yöneticisi olup olmadığıdır.
	IsCompanyAdmin bool
}

// CreateEmployee şirkete yeni bir çalışan ekler ve müşteri bağını kurar.
//
// Müşteri BAŞKA bir şirketin çalışanıysa errors.Conflict döner; kural link
// tablosundaki benzersizliktedir (bkz. [Definitions]) ve uygulama tarafında
// tekrarlanmaz — tekrarlansaydı iki eşzamanlı istek arasındaki yarışı yine
// indeks çözerdi.
//
// Bağ kurulumu çalışan satırıyla AYNI işlemde değildir (link servisi kendi
// bağlantısını kullanır); bu yüzden bağ kurulamazsa çalışan GERİ ALINIR.
// Alternatifi, müşterisi olmayan bir çalışan kaydının ayakta kalmasıydı: kayıt
// bir harcama limiti taşır ama vitrinde hiç kimseye çözülmez.
func (s *Service) CreateEmployee(ctx context.Context, in EmployeeInput) (models.CompanyEmployee, error) {
	if err := requireID(in.CompanyID, models.CompanyIDPrefix, "şirket kimliği"); err != nil {
		return models.CompanyEmployee{}, err
	}
	if err := requireID(in.CustomerID, models.CustomerIDPrefix, "müşteri kimliği"); err != nil {
		return models.CompanyEmployee{}, err
	}
	if err := validateSpendingLimit(in.SpendingLimit); err != nil {
		return models.CompanyEmployee{}, err
	}

	// Şirketin varlığı ÖNCE doğrulanır: foreign key ihlali de aynı sonucu
	// verirdi ama istemciye 422 olarak dönerdi ve eksik bir kaynak için doğru
	// sınıf errors.NotFound'dur.
	if _, err := s.repo.GetCompany(ctx, in.CompanyID); err != nil {
		return models.CompanyEmployee{}, err
	}

	now := s.clock()
	created, err := s.repo.CreateEmployee(ctx, models.CompanyEmployee{
		ID:             models.NewEmployeeID(now),
		CompanyID:      in.CompanyID,
		SpendingLimit:  in.SpendingLimit,
		IsCompanyAdmin: in.IsCompanyAdmin,
		CreatedAt:      now,
	})
	if err != nil {
		return models.CompanyEmployee{}, err
	}

	if err := s.linkCustomer(ctx, created.ID, in.CustomerID); err != nil {
		s.rollbackEmployee(ctx, created.ID)
		return models.CompanyEmployee{}, err
	}

	created.CustomerID = in.CustomerID
	s.log.InfoContext(ctx, "şirket çalışanı eklendi",
		slog.String("employee_id", created.ID),
		slog.String("company_id", created.CompanyID),
		slog.String("customer_id", in.CustomerID),
	)
	return created, nil
}

// rollbackEmployee bağı kurulamayan çalışanı geri alır.
//
// Hata DÖNMEZ: çağıran zaten asıl hatayı döndürecektir ve telafinin hatası onu
// gölgelerse istemci düzeltilebilir bir sebep yerine anlamsız bir sebep görür.
// Geri alınamayan kayıt uyarı olarak loglanır; hiçbir müşteriye bağlı olmadığı
// için vitrinde görünmez ama görünür kalmalıdır.
func (s *Service) rollbackEmployee(ctx context.Context, employeeID string) {
	if err := s.repo.DeleteEmployee(ctx, employeeID, s.clock()); err != nil {
		s.log.WarnContext(ctx, "bağı kurulamayan çalışan geri alınamadı",
			"employee_id", employeeID, "error", err)
	}
}

// GetEmployee kimliğe göre çalışan döner; yoksa errors.NotFound.
//
// Müşteri kimliği link'ten okunur ve kayda EKLENİR; sütunu yoktur.
func (s *Service) GetEmployee(ctx context.Context, id string) (models.CompanyEmployee, error) {
	if err := requireID(id, models.EmployeeIDPrefix, "çalışan kimliği"); err != nil {
		return models.CompanyEmployee{}, err
	}

	employee, err := s.repo.GetEmployee(ctx, id)
	if err != nil {
		return models.CompanyEmployee{}, err
	}

	tekil := []models.CompanyEmployee{employee}
	if err := s.attachCustomerIDs(ctx, tekil); err != nil {
		return models.CompanyEmployee{}, err
	}
	return tekil[0], nil
}

// ListEmployeesInput çalışan listelemesinin girdisidir.
type ListEmployeesInput struct {
	// CompanyID verilirse yalnızca bu şirketin çalışanları döner.
	CompanyID *string
	// IsCompanyAdmin verilirse yönetici ayrımına göre süzer.
	IsCompanyAdmin *bool
	// Limit sayfa boyudur; 0 ise [DefaultLimit] uygulanır.
	Limit int64
	// Offset atlanacak kayıt sayısıdır.
	Offset int64
}

// ListEmployees çalışanları süzerek ve sayfalayarak listeler.
//
// Müşteri kimlikleri TEK ek sorguyla doldurulur; kayıt başına ayrı sorgu N+1
// olurdu (bkz. [Service.attachCustomerIDs]).
func (s *Service) ListEmployees(
	ctx context.Context,
	in ListEmployeesInput,
) (Page[models.CompanyEmployee], error) {
	limit, offset, err := normalizePaging(in.Limit, in.Offset)
	if err != nil {
		return Page[models.CompanyEmployee]{}, err
	}
	if in.CompanyID != nil {
		if err := requireID(*in.CompanyID, models.CompanyIDPrefix, "şirket kimliği"); err != nil {
			return Page[models.CompanyEmployee]{}, err
		}
	}

	items, total, err := s.repo.ListEmployees(ctx, models.EmployeeFilter{
		CompanyID:      in.CompanyID,
		IsCompanyAdmin: in.IsCompanyAdmin,
	}, limit, offset)
	if err != nil {
		return Page[models.CompanyEmployee]{}, err
	}
	if err := s.attachCustomerIDs(ctx, items); err != nil {
		return Page[models.CompanyEmployee]{}, err
	}
	return Page[models.CompanyEmployee]{Items: items, Count: total, Limit: limit, Offset: offset}, nil
}

// UpdateEmployeeInput bir çalışanın kısmi güncelleme girdisidir.
//
// ŞİRKET ve MÜŞTERİ alanları BİLİNÇLİ OLARAK yoktur: ikisi de kaydın kimliğini
// oluşturur. Çalışan başka bir şirkete geçiyorsa doğru işlem kaydı taşımak
// değil, eskisini kapatıp yenisini açmaktır — harcama geçmişi eski şirkete
// aittir ve taşımak onu sessizce yeni şirkete devrederdi.
type UpdateEmployeeInput struct {
	// SpendingLimit yeni harcama limitidir (minor unit); nil "dokunma"dır.
	SpendingLimit *int64
	// ClearSpendingLimit doğruysa limit kaldırılır (çalışan sınırsız olur).
	//
	// Ayrı bir bayraktır çünkü alanın kendisi de nil olabilir: tek bir
	// işaretçi "dokunma" ile "sınırsız yap"ı ayıramaz ve ayıramadığı için
	// limit bir kez konduktan sonra asla kaldırılamazdı.
	ClearSpendingLimit bool
	// IsCompanyAdmin yönetici işaretinin yeni değeridir.
	IsCompanyAdmin *bool
}

// UpdateEmployee çalışanın verilen alanlarını günceller; yoksa errors.NotFound.
func (s *Service) UpdateEmployee(
	ctx context.Context,
	id string,
	in UpdateEmployeeInput,
) (models.CompanyEmployee, error) {
	if err := requireID(id, models.EmployeeIDPrefix, "çalışan kimliği"); err != nil {
		return models.CompanyEmployee{}, err
	}
	if in.ClearSpendingLimit && in.SpendingLimit != nil {
		return models.CompanyEmployee{}, errors.Invalid(CodeInvalidInput,
			"harcama limiti aynı anda hem verilip hem kaldırılamaz")
	}
	if err := validateSpendingLimit(in.SpendingLimit); err != nil {
		return models.CompanyEmployee{}, err
	}

	updated, err := s.repo.UpdateEmployee(ctx, id, models.EmployeePatch{
		SpendingLimit:      in.SpendingLimit,
		ClearSpendingLimit: in.ClearSpendingLimit,
		IsCompanyAdmin:     in.IsCompanyAdmin,
	}, s.clock())
	if err != nil {
		return models.CompanyEmployee{}, err
	}

	tekil := []models.CompanyEmployee{updated}
	if err := s.attachCustomerIDs(ctx, tekil); err != nil {
		return models.CompanyEmployee{}, err
	}
	return tekil[0], nil
}

// DeleteEmployee çalışanı yumuşak siler ve müşteri bağını kaldırır; kayıt yoksa
// errors.NotFound.
//
// Bağın kaldırılması silmenin ayrılmaz parçasıdır: bağ tekil olduğu için kalan
// bir satır, o müşterinin bir daha HİÇBİR şirkete çalışan olarak eklenememesi
// demektir. İşten çıkan bir çalışanın başka bir şirkette işe başlaması ise
// olağan durumdur.
func (s *Service) DeleteEmployee(ctx context.Context, id string) error {
	if err := requireID(id, models.EmployeeIDPrefix, "çalışan kimliği"); err != nil {
		return err
	}

	if err := s.repo.DeleteEmployee(ctx, id, s.clock()); err != nil {
		return err
	}
	s.unlinkCustomers(ctx, []string{id})

	s.log.InfoContext(ctx, "şirket çalışanı silindi", slog.String("employee_id", id))
	return nil
}

// Membership bir müşterinin şirketindeki üyeliğidir: kendi çalışan kaydı, bağlı
// olduğu şirket ve geçerli harcama penceresinin başlangıcı.
//
// Vitrinin okuduğu tek görünümdür. Üçü birlikte döner çünkü üçü tek bir soruyu
// cevaplar: "ben kimin adına, ne kadar ve hangi dönem içinde harcayabilirim?"
type Membership struct {
	// Employee müşterinin kendi çalışan kaydıdır.
	Employee models.CompanyEmployee
	// Company çalışanın bağlı olduğu şirkettir.
	Company models.Company
	// SpendingWindowStart geçerli harcama penceresinin başlangıcıdır; şirketin
	// sıfırlama periyodu [models.ResetNever] ise nil (pencere yoktur).
	//
	// KALAN HAK BURADA HESAPLANMAZ ve bu bilinçli bir eksiktir: kalanı bulmak
	// için pencere içindeki siparişlerin toplamı gerekir ve o veri order
	// modülünündür — limiti de o modül uygular (bkz. [Interop.SpendingLimitJSON]).
	// Uydurulmuş bir "kalan" alanı (örn. limitin kendisi) istemciye yanlış
	// bilgi verirdi; verilmeyen bir alan ise yalnızca eksiktir.
	SpendingWindowStart *time.Time
}

// MembershipOfCustomer müşterinin KENDİ üyeliğini döner; müşteri bir şirketin
// çalışanı değilse errors.NotFound.
//
// # Başkasının şirketi neden okunamaz
//
// Bu, vitrinin şirkete ulaşan TEK yoludur ve girdisi bir şirket kimliği değil,
// MÜŞTERİ kimliğidir. Şirket, müşterinin kendi çalışan kaydından türetilir;
// istemcinin bir şirketi adıyla isteyebileceği bir uç yoktur (bkz. api paketi).
// Böylece "başkasının şirketini okuma" isteği ifade EDİLEMEZ hâle gelir —
// yetki kontrolüyle reddedilen değil, kurulamayan bir istektir.
//
// Yumuşak silinmiş bir çalışan ya da şirket bulunmaz: her iki okuma da
// deleted_at IS NULL süzer. Bu yüzden geride kalmış (temizlenememiş) bir bağ
// bile silinmiş bir kaydı geri getiremez.
func (s *Service) MembershipOfCustomer(ctx context.Context, customerID string) (Membership, error) {
	if err := requireID(customerID, models.CustomerIDPrefix, "müşteri kimliği"); err != nil {
		return Membership{}, err
	}

	employeeID, err := s.employeeIDOfCustomer(ctx, customerID)
	if err != nil {
		return Membership{}, err
	}
	if employeeID == "" {
		return Membership{}, errors.NotFound(CodeEmployeeNotFound,
			"müşteri hiçbir şirketin çalışanı değil: %s", customerID)
	}

	employee, err := s.repo.GetEmployee(ctx, employeeID)
	if err != nil {
		return Membership{}, err
	}
	employee.CustomerID = customerID

	company, err := s.repo.GetCompany(ctx, employee.CompanyID)
	if err != nil {
		return Membership{}, err
	}

	return Membership{
		Employee:            employee,
		Company:             company,
		SpendingWindowStart: company.SpendingLimitResetPeriod.WindowStart(s.clock()),
	}, nil
}
