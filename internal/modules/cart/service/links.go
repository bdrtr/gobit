package service

import (
	"context"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/link"
)

// Link uçlarındaki entity adları.
//
// [link.LinkSide.Entity], Query'nin SAĞLAYICI ARADIĞI ADDIR; modül adı değildir
// (bkz. core/query targetSide). cart modülü sağlayıcısını "cart.query" adıyla
// kaydeder, customer ve region modülleri de kendi entity adlarıyla kaydeder;
// buraya modül adı yazmak çalışma zamanında errors.NotFound demek olurdu.
const (
	// ModuleName bu modülün adıdır; link uçlarında sahibi bildirmek için
	// kullanılır.
	ModuleName = "cart"
	// EntityCustomer customer modülünün müşteri entity adıdır.
	EntityCustomer = "customer"
	// EntityRegion region modülünün bölge entity adıdır.
	EntityRegion = "region"
)

// Link adları. Bu adlar MODÜLLER ARASI SÖZLEŞMEDİR: Query genişletmeleri ve
// ileride order modülünün okumaları tam olarak bu adları kullanır. Değişmeleri,
// kalıcı tanım defterindeki kayıtla çakışacağı için açılışta errors.Conflict
// üretir (bkz. link.LinkService.Define).
const (
	// LinkCartCustomer sepeti müşteriye bağlar.
	LinkCartCustomer = "cart_customer"
	// LinkCartRegion sepeti bölgeye bağlar.
	LinkCartRegion = "cart_region"
)

// Definitions cart modülünün bildirdiği link tanımlarıdır.
//
// # Kardinalite neden ManyToMany
//
// Bu iki bağın gerçek kardinalitesi "çoktan bire"dir: bir müşterinin zaman
// içinde birden çok sepeti olur, bir bölgede ise binlerce sepet bulunur — ama
// bir sepet tek bir müşteriye ve tek bir bölgeye aittir. core/link'in
// [link.Cardinality] kümesinde bu yön yoktur:
//
//   - [link.OneToOne] her İKİ uçta da benzersizlik ister. cart_region için bu,
//     "bir bölgeye yalnızca bir sepet bağlanabilir" demektir; aynı bölgede
//     ikinci bir sepet açan istek link_cart_region_to_uniq indeksine çarpar ve
//     Conflict alır. cart_customer'da da müşteri ömrü boyunca tek sepetle
//     sınırlanırdı.
//   - [link.OneToMany] (From = cart) yalnızca To ucunda benzersizlik kurar;
//     yukarıdaki aynı hata kalır.
//
// Geriye iki seçenek kalır: uçları ters çevirmek (From = region, OneToMany) ya
// da [link.ManyToMany]. Uçları ters çevirmek kardinaliteyi veritabanı düzeyinde
// zorlardı, ama linkin okunma yönünü tersine çevirir ve bağın SAHİBİNİ (sepet)
// From ucundan alırdı. Burada ManyToMany seçilmiştir, çünkü TEKİLLİK ZATEN
// BAŞKA BİR YERDE YAPISAL OLARAK GARANTİDİR: bölge ve müşteri kimliği sepetin
// KENDİ SÜTUNLARINDA durur (carts.region_id / carts.customer_id) ve bir satırın
// bir sütunu tek değer taşır. Link tablosu o sütunların Query katmanına açılan
// aynasıdır; servis ikisini aynı işlemde birlikte yazar (bkz. [Service.CreateCart]).
//
// Bedeli: Query genişletmesi bu bağlarda TEK KAYIT değil DİLİM yazar. Sepetin
// kendi API'si bölge ve müşteri kimliğini zaten sütundan okuduğu için bu bedel
// yalnızca cross-module okumalarda görünür.
func Definitions() []link.LinkDefinition {
	return []link.LinkDefinition{
		{
			Name:        LinkCartCustomer,
			From:        link.LinkSide{Module: ModuleName, Entity: EntityName, Field: "cart_id"},
			To:          link.LinkSide{Module: EntityCustomer, Entity: EntityCustomer, Field: "customer_id"},
			Cardinality: link.ManyToMany,
		},
		{
			Name:        LinkCartRegion,
			From:        link.LinkSide{Module: ModuleName, Entity: EntityName, Field: "cart_id"},
			To:          link.LinkSide{Module: EntityRegion, Entity: EntityRegion, Field: "region_id"},
			Cardinality: link.ManyToMany,
		},
	}
}

// linkCart sepetin bölge ve (varsa) müşteri bağlarını kurar.
//
// Bağlar sepetin sütunlarıyla AYNI değerleri taşır; sütun kaynak, link ise
// Query katmanına açılan aynadır. İkisinin ayrışmaması için bu çağrı sepet
// yazıldıktan hemen sonra yapılır ve başarısız olursa sepet GERİ ALINIR
// (bkz. [Service.CreateCart]).
func (s *Service) linkCart(ctx context.Context, cartID, regionID, customerID string) error {
	if err := s.links.Create(ctx, LinkCartRegion, cartID, regionID); err != nil {
		return wrapLink(err, "%q bağı kurulamadı (sepet: %s -> bölge: %s)",
			LinkCartRegion, cartID, regionID)
	}
	if customerID == "" {
		return nil
	}
	if err := s.links.Create(ctx, LinkCartCustomer, cartID, customerID); err != nil {
		return wrapLink(err, "%q bağı kurulamadı (sepet: %s -> müşteri: %s)",
			LinkCartCustomer, cartID, customerID)
	}
	return nil
}

// unlinkCart sepetin tüm bağlarını kaldırır.
//
// Hata DÖNMEZ, uyarı olarak loglanır: bu çağrı ya silme tamamlandıktan sonra
// (temizlik) ya da başarısız bir oluşturmanın telafisi olarak yapılır. İki
// durumda da çağırana hata dönmek yanlış sebebi gösterirdi — birincide "sepet
// silinmedi" izlenimi verir, ikincide asıl hatayı gölgeler. Temizlenemeyen bağ
// zararsızdır: kimlikler yeniden kullanılmadığı için yetim satır başka bir
// kayda bağlanamaz.
func (s *Service) unlinkCart(ctx context.Context, cartID string) {
	for _, name := range []string{LinkCartRegion, LinkCartCustomer} {
		targets, err := s.links.List(ctx, name, cartID)
		if err != nil {
			s.log.WarnContext(ctx, "sepetin bağı okunamadı",
				"link", name, "cart_id", cartID, "error", err)
			continue
		}
		for _, target := range targets {
			if err := s.links.Delete(ctx, name, cartID, target); err != nil {
				s.log.WarnContext(ctx, "sepetin bağı kaldırılamadı",
					"link", name, "cart_id", cartID, "to_id", target, "error", err)
			}
		}
	}
}

// wrapLink link servisinden gelen hatayı SINIFINI KORUYARAK sarar.
//
// Sınıfın korunması şart: kardinalite ihlali Conflict (409), tanımsız link adı
// NotFound (404) olarak kalmalıdır; hepsini Internal'a çevirmek istemciye
// düzeltilebilir bir hatayı sunucu hatası gibi gösterirdi.
func wrapLink(err error, format string, a ...any) error {
	return errors.Wrap(err, errors.KindOf(err), CodeLinkFailed, format, a...)
}
