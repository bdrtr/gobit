package service

import "context"

// Bu dosya customer'ın MODÜLLER ARASI yüzeyidir (ADR 0001).
//
// Buradaki imzalar YALNIZCA ilkel ve stdlib tipleri kullanır. Sebep Go'nun
// yapısal uyumudur: tüketici modül (cart, order, ya da bir workflow) customer'ı
// import EDEMEZ, dolayısıyla models.Customer gibi bir tipi kendi arayüzünde
// adlandıramaz — adlandırdığı an kendi paketindeki farklı bir tip olur ve somut
// servis arayüzü karşılamaz. İlkel tiplerle yazılmış imzalar ise tüketicinin
// kendi paketinde birebir tekrarlanabilir:
//
//	// cart modülünde, customer import EDİLMEDEN:
//	type CustomerReader interface {
//	    CustomerGroupIDs(ctx context.Context, customerID string) ([]string, error)
//	}
//	customers, err := container.Resolve[CustomerReader](c, "customer.service")
//
// Yüzey bilinçli olarak DAR tutulur: buraya eklenen her metot, customer'ın bir
// daha değiştiremeyeceği bir sözleşmedir (uyumsuzluk derleme zamanında değil,
// container'dan çözüm anında yakalanır). Bir alanın tamamı gerekiyorsa doğru
// yol yeni bir ilkel metot değil, Query katmanıdır: "customer" sağlayıcısı
// kaydın tüm alanlarını ve grup kimliklerini tek çağrıda verir (ADR 0004).

// CustomerEmail müşterinin e-posta adresini döner; müşteri yoksa
// errors.NotFound.
//
// Sepet ve sipariş akışları misafir müşteride bile bir iletişim adresine
// ihtiyaç duyar; e-postayı tek başına veren bu yüzey, tüketicinin müşteri
// modelinin tamamına bağlanmasını gereksiz kılar.
func (s *Service) CustomerEmail(ctx context.Context, customerID string) (string, error) {
	customer, err := s.GetCustomer(ctx, customerID)
	if err != nil {
		return "", err
	}
	return customer.Email, nil
}

// CustomerGroupIDs müşterinin üye olduğu grupların kimliklerini döner;
// müşteri yoksa errors.NotFound.
//
// Bu yüzeyin asıl tüketicisi FİYAT HESABIDIR: pricing'in kural bağlamı
// "customer_group_id" özniteliğine bakar ve sepet toplamı hesaplanırken
// müşterinin segmentleri o bağlama konur. Grupların adları, üstverisi ya da
// oluşturma zamanı hesabın girdisi değildir; yalnızca kimlikler taşınır.
//
// Hiç grubu olmayan müşteri için boş (nil olmayan) dilim döner.
func (s *Service) CustomerGroupIDs(ctx context.Context, customerID string) ([]string, error) {
	groups, err := s.ListGroupsOf(ctx, customerID)
	if err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(groups))
	for _, g := range groups {
		ids = append(ids, g.ID)
	}
	return ids, nil
}

// RegisterGuestCustomer misafir bir müşteri kaydı açar ve KİMLİĞİNİ döner.
//
// Vitrinden gelen bir sepetin, hesabı olmayan bir müşteriye bağlanabilmesi
// içindir. [Service.RegisterGuest] ile aynı işi yapar; farkı, imzasının
// modüller arası kullanılabilecek kadar ilkel olmasıdır.
//
// Aynı e-postayla daha önce misafir kaydı bulunması engel DEĞİLDİR; gerekçe
// için bkz. internal/modules/customer/models, Customer.
func (s *Service) RegisterGuestCustomer(ctx context.Context, email, firstName, lastName, phone string) (string, error) {
	customer, err := s.RegisterGuest(ctx, CustomerInput{
		Email:     email,
		FirstName: firstName,
		LastName:  lastName,
		Phone:     phone,
	})
	if err != nil {
		return "", err
	}
	return customer.ID, nil
}
