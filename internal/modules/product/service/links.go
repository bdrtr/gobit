package service

import (
	"context"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/link"
)

// Query katmanına sunulan entity adları.
//
// Sağlayıcılar container'a "<entity>.query" adıyla kaydedilir (ADR 0004) ve
// LİNK TANIMLARININ UÇLARI da bu adlarla yazılır — çünkü Query bir link'i
// çözerken karşı ucun modül alanını ENTITY ADI olarak okuyup sağlayıcıyı o adla
// arar (bkz. core/query targetSide + provider).
const (
	// EntityProduct ürün kayıtlarının entity adıdır.
	// ModuleName bu modülün adıdır; link uçlarında sahibi bildirmek için kullanılır.
	ModuleName    = "product"
	EntityProduct = "product"
	// EntityVariant varyant kayıtlarının entity adıdır.
	EntityVariant = "variant"
)

// Link adları. Bu adlar MODÜLLER ARASI SÖZLEŞMEDİR: pricing ve inventory
// modülleri bir varyanta bağ kurarken ya da ters yönde okuma yaparken tam
// olarak bu adları kullanır. Değişmeleri, kayıtlı tanımla çakışacağı için
// açılışta errors.Conflict üretir (bkz. link.Define).
const (
	// LinkVariantPriceSet varyantı pricing modülündeki fiyat kümesine bağlar.
	LinkVariantPriceSet = "product_variant_price_set"
	// LinkVariantInventory varyantı inventory modülündeki stok kalemine bağlar.
	LinkVariantInventory = "product_variant_inventory"
)

// Definitions product modülünün bildirdiği link tanımlarıdır.
//
// # Uçlardaki "modül" alanı neden entity adı
//
// Bağın product tarafındaki ucu ürünü değil VARYANTI gösterir: fiyat ve stok
// varyant başınadır. Query katmanı bir genişletmeyi çözerken link'in uçlarındaki
// Module alanını entity adı olarak okur ve sağlayıcıyı "<Module>.query" adıyla
// arar; kayıtları da o sağlayıcının "id" alanı üzerinden eşleştirir.
//
// Bu yüzden From ucu "product" değil "variant"tır: link tablosunda from_id
// olarak varyant kimlikleri durur ve genişletmenin kökü de varyant
// sağlayıcısıdır. "product" yazılsaydı Query, varyant kimlikleriyle dolu bir
// link tablosunu ÜRÜN sağlayıcısına sorar ve hiçbir kayıt eşleşmezdi — hata
// vermeden boş fiyat/stok dönerdi ki bu, bulunması en pahalı hata türüdür.
// Alan adı (variant_id) sahipliği ayrıca belgeler.
func Definitions() []link.LinkDefinition {
	return []link.LinkDefinition{
		{
			Name:        LinkVariantPriceSet,
			From:        link.LinkSide{Module: ModuleName, Entity: EntityVariant, Field: "variant_id"},
			To:          link.LinkSide{Module: "pricing", Entity: "price_set", Field: "price_set_id"},
			Cardinality: link.OneToOne,
		},
		{
			Name:        LinkVariantInventory,
			From:        link.LinkSide{Module: ModuleName, Entity: EntityVariant, Field: "variant_id"},
			To:          link.LinkSide{Module: "inventory", Entity: "inventory_item", Field: "inventory_item_id"},
			Cardinality: link.OneToOne,
		},
	}
}

// VariantLinks bir varyantın başka modüllerdeki karşılıklarıdır.
//
// Alanlar yalnızca KİMLİKTİR: fiyatın kendisi pricing'in, stok seviyesi
// inventory'nin verisidir ve bu modül onları yorumlamaz.
type VariantLinks struct {
	PriceSetID      *string `json:"price_set_id,omitempty"`
	InventoryItemID *string `json:"inventory_item_id,omitempty"`
}

// SetVariantPriceSet varyantı bir fiyat kümesine bağlar.
//
// Bağ TEKİLDİR (OneToOne): varyant başka bir kümeye bağlıysa eski bağ önce
// kaldırılır. Aksi hâlde link servisi kardinalite ihlali yüzünden Conflict
// dönerdi ve "fiyatı değiştir" isteği hata gibi görünürdü. İstek çakışmayla
// düşerse varyant ESKİ kümesine bağlı kalır (bkz. setVariantLink).
func (s *Service) SetVariantPriceSet(ctx context.Context, variantID, priceSetID string) error {
	return s.setVariantLink(ctx, LinkVariantPriceSet, variantID, priceSetID, "price_set_id")
}

// ClearVariantPriceSet varyantın fiyat kümesi bağını kaldırır.
func (s *Service) ClearVariantPriceSet(ctx context.Context, variantID string) error {
	return s.clearVariantLink(ctx, LinkVariantPriceSet, variantID)
}

// SetVariantInventoryItem varyantı bir stok kalemine bağlar.
func (s *Service) SetVariantInventoryItem(ctx context.Context, variantID, itemID string) error {
	return s.setVariantLink(ctx, LinkVariantInventory, variantID, itemID, "inventory_item_id")
}

// ClearVariantInventoryItem varyantın stok kalemi bağını kaldırır.
func (s *Service) ClearVariantInventoryItem(ctx context.Context, variantID string) error {
	return s.clearVariantLink(ctx, LinkVariantInventory, variantID)
}

// VariantLinkIDs varyantın bağlı olduğu fiyat kümesi ve stok kalemi
// kimliklerini döner.
func (s *Service) VariantLinkIDs(ctx context.Context, variantID string) (VariantLinks, error) {
	if _, err := requireID("variant_id", variantID); err != nil {
		return VariantLinks{}, err
	}
	if s.links == nil {
		return VariantLinks{}, s.linkerMissing()
	}
	if _, err := s.repo.GetVariant(ctx, variantID); err != nil {
		return VariantLinks{}, err
	}

	priceSetID, err := s.firstLink(ctx, LinkVariantPriceSet, variantID)
	if err != nil {
		return VariantLinks{}, err
	}
	itemID, err := s.firstLink(ctx, LinkVariantInventory, variantID)
	if err != nil {
		return VariantLinks{}, err
	}
	return VariantLinks{PriceSetID: priceSetID, InventoryItemID: itemID}, nil
}

// setVariantLink varyantı verilen link üzerinden hedef kayda bağlar.
//
// # Sıra ve telafi
//
// OneToOne kardinalitesinde İKİ uç da benzersizdir. Varyantın (FROM ucu) yeni
// bağı, eskisi kaldırılmadan kurulamaz; bu yüzden önce silinir. Ama hedefin
// (TO ucu) başka bir varyanta bağlı olması da ihlaldir ve Create o durumda
// Conflict döner — silme zaten yapılmış olduğu için varyant fiyatsız/stoksuz
// kalırdı. İstek 409 döndüğü için operatör "hiçbir şey değişmedi" okur, oysa
// vitrin o varyantı fiyatsız yayınlar: sessiz veri kaybı.
//
// Bu yüzden Create başarısız olursa silinen bağlar GERİ KURULUR ve hata
// dönülür; başarısız bir istek varyantın mevcut bağını bozmaz. Telafi de
// düşerse (arada başka bir istek hedefi kapmışsa) durum uyarı olarak loglanır —
// asıl hatayı gölgelemek çağırana yanlış sebebi gösterirdi.
func (s *Service) setVariantLink(ctx context.Context, name, variantID, toID, field string) error {
	if _, err := requireID("variant_id", variantID); err != nil {
		return err
	}
	if _, err := requireID(field, toID); err != nil {
		return err
	}
	if s.links == nil {
		return s.linkerMissing()
	}
	// Varyantın gerçekten var olduğu BURADA doğrulanır: link servisi kimlikleri
	// serbest dizge olarak görür ve hiçbir modülün şemasını tanımaz, yani
	// yazım hatası taşıyan bir kimlik sessizce bağlanırdı.
	if _, err := s.repo.GetVariant(ctx, variantID); err != nil {
		return err
	}

	existing, err := s.links.List(ctx, name, variantID)
	if err != nil {
		return wrapLink(err, "%q bağı okunamadı (varyant: %s)", name, variantID)
	}
	removed := make([]string, 0, len(existing))
	for _, current := range existing {
		if current == toID {
			continue
		}
		if err := s.links.Delete(ctx, name, variantID, current); err != nil {
			s.restoreVariantLinks(ctx, name, variantID, removed)
			return wrapLink(err, "%q bağının eskisi kaldırılamadı (varyant: %s)", name, variantID)
		}
		removed = append(removed, current)
	}

	if err := s.links.Create(ctx, name, variantID, toID); err != nil {
		s.restoreVariantLinks(ctx, name, variantID, removed)
		return wrapLink(err, "%q bağı kurulamadı (varyant: %s -> %s)", name, variantID, toID)
	}
	return nil
}

// restoreVariantLinks başarısız bir yeniden bağlamada kaldırılan bağları geri
// kurar.
//
// Hata DÖNMEZ: çağıran zaten asıl hatayı döndürecektir ve telafinin hatası onu
// gölgelerse istemci düzeltilebilir çakışma yerine anlamsız bir sebep görür.
// Geri kurulamayan bağ uyarı olarak loglanır.
func (s *Service) restoreVariantLinks(ctx context.Context, name, variantID string, removed []string) {
	for _, toID := range removed {
		if err := s.links.Create(ctx, name, variantID, toID); err != nil {
			s.log.WarnContext(ctx, "başarısız yeniden bağlamada eski bağ geri kurulamadı",
				"link", name, "variant_id", variantID, "to_id", toID, "error", err)
		}
	}
}

// clearVariantLink varyantın verilen linkteki tüm bağlarını kaldırır.
func (s *Service) clearVariantLink(ctx context.Context, name, variantID string) error {
	if _, err := requireID("variant_id", variantID); err != nil {
		return err
	}
	if s.links == nil {
		return s.linkerMissing()
	}

	existing, err := s.links.List(ctx, name, variantID)
	if err != nil {
		return wrapLink(err, "%q bağı okunamadı (varyant: %s)", name, variantID)
	}
	for _, current := range existing {
		if err := s.links.Delete(ctx, name, variantID, current); err != nil {
			return wrapLink(err, "%q bağı kaldırılamadı (varyant: %s)", name, variantID)
		}
	}
	return nil
}

// firstLink varyantın verilen linkteki ilk bağını döner; bağ yoksa nil.
func (s *Service) firstLink(ctx context.Context, name, variantID string) (*string, error) {
	ids, err := s.links.List(ctx, name, variantID)
	if err != nil {
		return nil, wrapLink(err, "%q bağı okunamadı (varyant: %s)", name, variantID)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return &ids[0], nil
}

// cleanupVariantLinks silinen varyantın fiyat/stok bağlarını temizler.
//
// Hata DÖNMEZ, uyarı olarak loglanır: silme işlemi zaten tamamlanmıştır ve
// çağırana hata dönmek "ürün silinmedi" izlenimi verirdi. Temizlenemeyen bir
// bağ zararsızdır — kimlikler yeniden kullanılmadığı için yetim satır başka
// bir kayda bağlanamaz; yine de görünür kalması için loglanır.
func (s *Service) cleanupVariantLinks(ctx context.Context, variantID string) {
	if s.links == nil {
		return
	}
	for _, name := range []string{LinkVariantPriceSet, LinkVariantInventory} {
		if err := s.clearVariantLink(ctx, name, variantID); err != nil {
			s.log.WarnContext(ctx, "silinen varyantın bağı temizlenemedi",
				"link", name, "variant_id", variantID, "error", err)
		}
	}
}

// linkerMissing link servisi olmadan kurulmuş bir servisin tipli hatasıdır.
func (s *Service) linkerMissing() error {
	return errors.Unavailable(codeNotReady,
		"link servisi kayıtlı değil; fiyat/stok bağları bu kurulumda yönetilemez")
}

// wrapLink link servisinden gelen hatayı SINIFINI KORUYARAK sarar.
//
// Sınıfın korunması şart: kardinalite ihlali Conflict (409), tanımsız link adı
// NotFound (404) olarak kalmalıdır; hepsini Internal'a çevirmek istemciye
// düzeltilebilir bir hatayı sunucu hatası gibi gösterirdi.
func wrapLink(err error, format string, a ...any) error {
	return errors.Wrap(err, errors.KindOf(err), codeLinkFailed, format, a...)
}
