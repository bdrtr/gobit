package service

import (
	"context"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/link"
	"github.com/bdrtr/gobit/internal/modules/b2b/models"
)

// Link uçlarındaki modül ve entity adları.
//
// [link.LinkSide.Entity], Query'nin SAĞLAYICI ARADIĞI ADDIR; modül adı değildir
// (bkz. core/query targetSide). customer modülü sağlayıcısını "customer.query"
// adıyla kaydeder; hedef uca modül adı yerine entity adı yazmak, çalışma
// zamanında errors.NotFound demek olurdu. Burada ikisi aynıdır ("customer"),
// ama bu bir rastlantıdır ve ad açıkça yazılır.
const (
	// ModuleName bu modülün adıdır; link uçlarında sahibi bildirmek için
	// kullanılır.
	ModuleName = "b2b"
	// EntityEmployee çalışan kaydının entity adıdır.
	EntityEmployee = "b2b_employee"
	// EntityCustomer customer modülünün müşteri entity adıdır.
	EntityCustomer = "customer"
)

// LinkEmployeeCustomer çalışanı MÜŞTERİ kaydına bağlayan linkin adıdır.
//
// Ad MODÜLLER ARASI SÖZLEŞMEDİR: kalıcı tanım defterine yazılır ve değişmesi
// açılışta errors.Conflict üretir (bkz. link.LinkService.Define).
const LinkEmployeeCustomer = "b2b_employee_customer"

// Definitions b2b modülünün bildirdiği link tanımlarıdır.
//
// # Neden kardinalite OneToOne
//
// Her iki uçta da benzersizlik istenir ve ikisi de kasıtlıdır:
//
//   - From (çalışan) ucu: bir çalışan kaydı tek bir müşteriye aittir. Aksi,
//     tek bir harcama limitinin birden çok kişi tarafından paylaşılması
//     olurdu ve limitin kimin adına tükendiği söylenemezdi.
//   - To (müşteri) ucu: bir müşteri en fazla BİR şirketin çalışanıdır. Vitrin
//     "kendi şirketim" sorusunu tek cevaba çözebilmek zorundadır; iki cevabın
//     olduğu bir modelde hangi şirketin limitinin uygulanacağı belirsiz kalır
//     ve belirsizlik, harcama kuralının uygulanmaması demektir.
//
// Kısıt uygulamada değil VERİTABANINDA durur (link tablosunun benzersiz
// indeksleri): "önce oku sonra yaz" biçiminde bir kontrol, aynı müşteriyi iki
// şirkete ekleyen eşzamanlı iki istek arasında tutmazdı.
//
// Bunun bedeli, çalışan kaydı silinirken bağın da MUTLAKA kaldırılmasıdır;
// kalan bir bağ, müşterinin bir daha hiçbir şirkete eklenememesi demektir
// (bkz. [Service.DeleteEmployee]).
func Definitions() []link.LinkDefinition {
	return []link.LinkDefinition{
		{
			Name:        LinkEmployeeCustomer,
			From:        link.LinkSide{Module: ModuleName, Entity: EntityEmployee, Field: "employee_id"},
			To:          link.LinkSide{Module: EntityCustomer, Entity: EntityCustomer, Field: "customer_id"},
			Cardinality: link.OneToOne,
		},
	}
}

// linkCustomer çalışanı müşteriye bağlar.
func (s *Service) linkCustomer(ctx context.Context, employeeID, customerID string) error {
	if err := s.links.Create(ctx, LinkEmployeeCustomer, employeeID, customerID); err != nil {
		return wrapLink(err, "%q bağı kurulamadı (çalışan: %s -> müşteri: %s)",
			LinkEmployeeCustomer, employeeID, customerID)
	}
	return nil
}

// unlinkCustomers verilen çalışanların müşteri bağlarını kaldırır.
//
// Hata DÖNMEZ, uyarı olarak loglanır: bu çağrı ya silme tamamlandıktan sonra
// (temizlik) ya da başarısız bir oluşturmanın telafisi olarak yapılır. İki
// durumda da çağırana hata dönmek yanlış sebebi gösterirdi — birincide "çalışan
// silinmedi" izlenimi verir, ikincide asıl hatayı gölgeler.
//
// Temizlenemeyen bağ ZARARSIZ DEĞİLDİR ve bu, sepet modülündeki benzer
// durumdan farkıdır: bağ tekil olduğu için sarkan bir satır, o müşterinin bir
// daha hiçbir şirkete çalışan olarak eklenememesi demektir. Okuma yolu yine de
// güvendedir — çalışan kaydı yumuşak silinmiş olduğundan sarkan bağ hiçbir
// isteğe çözülmez (bkz. [Service.MembershipOfCustomer]) — ama durum log'da
// görünür kalmalıdır.
func (s *Service) unlinkCustomers(ctx context.Context, employeeIDs []string) {
	if len(employeeIDs) == 0 {
		return
	}

	bags, err := s.links.ListMany(ctx, LinkEmployeeCustomer, employeeIDs)
	if err != nil {
		s.log.WarnContext(ctx, "çalışanların müşteri bağları okunamadı",
			"link", LinkEmployeeCustomer, "employee_ids", employeeIDs, "error", err)
		return
	}

	for employeeID, customerIDs := range bags {
		for _, customerID := range customerIDs {
			if err := s.links.Delete(ctx, LinkEmployeeCustomer, employeeID, customerID); err != nil {
				s.log.WarnContext(ctx, "çalışanın müşteri bağı kaldırılamadı",
					"link", LinkEmployeeCustomer, "employee_id", employeeID,
					"customer_id", customerID, "error", err)
			}
		}
	}
}

// attachCustomerIDs çalışan kayıtlarının müşteri kimliklerini TEK sorguda
// doldurur.
//
// Kimlikler sütunda değil link tablosunda durur; kayıt başına ayrı bir sorgu
// N+1 olurdu (ADR 0004'ün yapısal olarak yasakladığı şey). Bağı olmayan bir
// çalışanın alanı BOŞ kalır ve bu görünür bir arızadır: bağ kurulamamış ya da
// elle bozulmuş bir kayıt, boş bir müşteri kimliğiyle kendini gösterir.
func (s *Service) attachCustomerIDs(ctx context.Context, employees []models.CompanyEmployee) error {
	if len(employees) == 0 {
		return nil
	}

	ids := make([]string, 0, len(employees))
	for _, e := range employees {
		ids = append(ids, e.ID)
	}

	bags, err := s.links.ListMany(ctx, LinkEmployeeCustomer, ids)
	if err != nil {
		return wrapLink(err, "çalışanların müşteri bağları okunamadı")
	}

	for i := range employees {
		if customerIDs := bags[employees[i].ID]; len(customerIDs) > 0 {
			// Bağ tekildir (OneToOne); ikinci bir kimlik veritabanı kısıtı
			// yüzünden oluşamaz, ilki alınır.
			employees[i].CustomerID = customerIDs[0]
		}
	}
	return nil
}

// employeeIDOfCustomer müşteriye bağlı çalışan kimliğini döner; bağ yoksa boş
// dize.
func (s *Service) employeeIDOfCustomer(ctx context.Context, customerID string) (string, error) {
	bags, err := s.links.ListManyByTo(ctx, LinkEmployeeCustomer, []string{customerID})
	if err != nil {
		return "", wrapLink(err, "müşterinin çalışan bağı okunamadı: %s", customerID)
	}

	employeeIDs := bags[customerID]
	if len(employeeIDs) == 0 {
		return "", nil
	}
	// Bağ tekildir (OneToOne); ikinci bir kimlik veritabanı kısıtı yüzünden
	// oluşamaz.
	return employeeIDs[0], nil
}

// wrapLink link servisinden gelen hatayı SINIFINI KORUYARAK sarar.
//
// Sınıfın korunması şart: kardinalite ihlali Conflict (409), tanımsız link adı
// NotFound (404) olarak kalmalıdır; hepsini Internal'a çevirmek istemciye
// düzeltilebilir bir hatayı sunucu hatası gibi gösterirdi. Müşteri zaten başka
// bir şirkete bağlıysa istemcinin gördüğü şey tam olarak budur: 409.
func wrapLink(err error, format string, a ...any) error {
	return errors.Wrap(err, errors.KindOf(err), CodeLinkFailed, format, a...)
}
