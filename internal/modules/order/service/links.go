package service

import (
	"context"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/link"
)

// Link uçlarındaki entity adları.
//
// [link.LinkSide.Entity], Query'nin SAĞLAYICI ARADIĞI ADDIR; modül adı değildir
// (bkz. core/query targetSide). order modülü sağlayıcısını "order.query" adıyla
// kaydeder, customer ve region modülleri de kendi entity adlarıyla kaydeder;
// buraya modül adı yazmak çalışma zamanında errors.NotFound demek olurdu.
const (
	// ModuleName bu modülün adıdır; link uçlarında sahibi bildirmek için
	// kullanılır.
	ModuleName = "order"
	// EntityCustomer customer modülünün müşteri entity adıdır.
	EntityCustomer = "customer"
	// EntityRegion region modülünün bölge entity adıdır.
	EntityRegion = "region"
)

// Link adları. Bu adlar MODÜLLER ARASI SÖZLEŞMEDİR: Query genişletmeleri tam
// olarak bu adları kullanır. Değişmeleri, kalıcı tanım defterindeki kayıtla
// çakışacağı için açılışta errors.Conflict üretir (bkz. link.LinkService.Define).
const (
	// LinkOrderCustomer siparişi müşteriye bağlar.
	LinkOrderCustomer = "order_customer"
	// LinkOrderRegion siparişi bölgeye bağlar.
	LinkOrderRegion = "order_region"
)

// Definitions order modülünün bildirdiği link tanımlarıdır.
//
// # Neden yalnızca iki tanım
//
// Plan Bölüm 6 "order↔payment" ve "order↔fulfillment" bağlarını da sayar. İkisi
// BURADA BİLDİRİLMEZ, çünkü bir link tanımı yalnızca BİR kez bildirilmelidir:
// aynı adı iki modül farklı uçlarla bildirirse kalıcı tanım defteri açılışta
// errors.Conflict üretir ve sunucu hiç açılmaz (ADR 0005). Bu bağların sahibi,
// bağın taşıdığı kaydı yazan taraftır — ödeme tahsilatını payment, sevkiyatı
// fulfillment modülü bilir — dolayısıyla tanımı onlar bildirir. Sipariş kendi
// sütununda taşımadığı bir ilişkinin sahibi değildir.
//
// # Kardinalite neden ManyToMany
//
// Bu iki bağın gerçek kardinalitesi "çoktan bire"dir: bir müşterinin birden çok
// siparişi olur, bir bölgede binlerce sipariş bulunur — ama bir sipariş tek bir
// müşteriye ve tek bir bölgeye aittir. core/link'in [link.Cardinality] kümesinde
// bu yön yoktur:
//
//   - [link.OneToOne] her İKİ uçta da benzersizlik ister. order_region için bu,
//     "bir bölgeye yalnızca bir sipariş bağlanabilir" demektir; aynı bölgede
//     ikinci bir sipariş açan istek benzersiz indekse çarpar ve Conflict alır.
//   - [link.OneToMany] (From = order) yalnızca To ucunda benzersizlik kurar;
//     yukarıdaki aynı hata kalır.
//
// Geriye iki seçenek kalır: uçları ters çevirmek (From = region, OneToMany) ya
// da [link.ManyToMany]. Uçları ters çevirmek kardinaliteyi veritabanı düzeyinde
// zorlardı, ama linkin okunma yönünü tersine çevirir ve bağın SAHİBİNİ (sipariş)
// From ucundan alırdı. Burada ManyToMany seçilmiştir, çünkü TEKİLLİK ZATEN
// BAŞKA BİR YERDE YAPISAL OLARAK GARANTİDİR: bölge ve müşteri kimliği siparişin
// KENDİ SÜTUNLARINDA durur (orders.region_id / orders.customer_id) ve bir
// satırın bir sütunu tek değer taşır. Link tablosu o sütunların Query katmanına
// açılan aynasıdır. Seçim cart modülündeki aynı kararla bilinçli olarak
// tutarlıdır.
func Definitions() []link.LinkDefinition {
	return []link.LinkDefinition{
		{
			Name:        LinkOrderCustomer,
			From:        link.LinkSide{Module: ModuleName, Entity: EntityName, Field: "order_id"},
			To:          link.LinkSide{Module: EntityCustomer, Entity: EntityCustomer, Field: "customer_id"},
			Cardinality: link.ManyToMany,
		},
		{
			Name:        LinkOrderRegion,
			From:        link.LinkSide{Module: ModuleName, Entity: EntityName, Field: "order_id"},
			To:          link.LinkSide{Module: EntityRegion, Entity: EntityRegion, Field: "region_id"},
			Cardinality: link.ManyToMany,
		},
	}
}

// linkOrder siparişin bölge ve (varsa) müşteri bağlarını kurar.
//
// Bağlar siparişin sütunlarıyla AYNI değerleri taşır; sütun kaynak, link ise
// Query katmanına açılan aynadır. İkisinin ayrışmaması için bu çağrı sipariş
// YAZILMADAN ÖNCE yapılır: bağ kurulamazsa sipariş hiç yazılmaz ve bağsız bir
// sipariş asla var olmaz (bkz. [Service.CreateOrder]).
//
// Sipariş kimliği bu yüzden çağrıya parametredir; bağın FROM ucu, henüz
// yazılmamış siparişin kimliğidir.
func (s *Service) linkOrder(ctx context.Context, orderID, regionID, customerID string) error {
	if err := s.links.Create(ctx, LinkOrderRegion, orderID, regionID); err != nil {
		return wrapLink(err, "%q bağı kurulamadı (sipariş: %s -> bölge: %s)",
			LinkOrderRegion, orderID, regionID)
	}
	if customerID == "" {
		return nil
	}
	if err := s.links.Create(ctx, LinkOrderCustomer, orderID, customerID); err != nil {
		return wrapLink(err, "%q bağı kurulamadı (sipariş: %s -> müşteri: %s)",
			LinkOrderCustomer, orderID, customerID)
	}
	return nil
}

// unlinkOrder siparişin tüm bağlarını kaldırır.
//
// Hata DÖNMEZ, uyarı olarak loglanır: bu çağrı yalnızca başarısız bir
// oluşturmanın telafisi olarak yapılır ve çağırana hata dönmek asıl hatayı
// gölgelerdi. Temizlenemeyen bağ zararsızdır: kimlikler yeniden kullanılmadığı
// için yetim satır başka bir kayda bağlanamaz — bu, bağın siparişten ÖNCE
// kurulmasını güvenli kılan özelliğin ta kendisidir.
func (s *Service) unlinkOrder(ctx context.Context, orderID string) {
	for _, name := range []string{LinkOrderRegion, LinkOrderCustomer} {
		targets, err := s.links.List(ctx, name, orderID)
		if err != nil {
			s.log.WarnContext(ctx, "siparişin bağı okunamadı",
				"link", name, "order_id", orderID, "error", err)
			continue
		}
		for _, target := range targets {
			if err := s.links.Delete(ctx, name, orderID, target); err != nil {
				s.log.WarnContext(ctx, "siparişin bağı kaldırılamadı",
					"link", name, "order_id", orderID, "to_id", target, "error", err)
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
