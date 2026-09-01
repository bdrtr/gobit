package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
)

// AddLineItemInput sepete eklenecek satırın alanlarıdır.
type AddLineItemInput struct {
	// VariantID eklenen ürün varyantıdır; ZORUNLUDUR. product modülüne aittir,
	// burada varlığı doğrulanmaz (ADR 0001) ve foreign key verilmez.
	VariantID string
	// Title satırın görünen adıdır; ZORUNLUDUR. Varyanttan KOPYALANIR: katalog
	// sonradan değişse bile sepette görülen ad değişmez.
	Title string
	// Quantity eklenecek adettir; POZİTİF olmalıdır.
	Quantity int64
	// UnitPrice birim fiyattır (minor unit).
	//
	// Değeri İSTEMCİ VERMEZ ve veremez: vitrin gövdesinde fiyat alanı yoktur
	// (bkz. api paketindeki addLineItemRequest) ve satırı açan tek yol,
	// fiyatı pricing modülünden alan add_line_item akışıdır. Burada
	// opsiyonel görünmesi servisin bir eksikliği değil sınırıdır — modül
	// fiyatın DOĞRU olup olmadığını bilemez, yalnızca aralığını denetler; o
	// yüzden fiyat yetkisi çağıranın kim olduğuyla korunur ve tek çağıran
	// akıştır.
	//
	// Nihai değeri yine akış yazar: satır eklendikten hemen sonra koşan hesap
	// turu tüm satırları güncel adetle yeniden fiyatlar ve sonucu
	// [Service.SetTotals] ile yazar.
	UnitPrice int64
	// Metadata çağıranın serbest ek verisidir.
	Metadata map[string]any
}

// AddLineItem sepete satır ekler.
//
// # Aynı varyant ikinci kez eklenirse ne olur
//
// YENİ SATIR AÇILMAZ; var olan satırın ADEDİ ARTAR. Karar üç gerekçeye dayanır:
//
//  1. Fiyat kademeleri. pricing modülü fiyatı adet aralığına göre seçer
//     (min_quantity/max_quantity). Aynı varyant 3 + 2 olarak iki satıra
//     bölünürse iki satır da "1-4" kademesinden fiyatlanır ve müşteri hak
//     ettiği "5+" fiyatını ALAMAZ. Adet tek satırda toplanınca kademe doğru
//     seçilir.
//  2. Stok rezervasyonu. Faz 6'daki complete_cart satır başına rezervasyon
//     yapar; aynı varyantın iki satırı, aynı stok için iki ayrı rezervasyon
//     demektir ve kısmi başarı durumunda telafi karmaşıklaşır.
//  3. Müşteri beklentisi. Sepette aynı ürünün iki kez görünmesi, ürünlerin
//     farklı olduğu izlenimi verir.
//
// Karar veritabanı düzeyinde de zorlanır: cart_line_items_cart_variant_uniq
// kısmi benzersiz indeksi, sepet kilidini bir şekilde atlatan bir yazma yolunun
// bile ikinci satırı açmasını engeller.
//
// Birleştirmede yalnızca ADET taşınır; var olan satırın başlığı, birim fiyatı
// ve metadata'sı KORUNUR. Satır başına özelleştirme (örn. aynı varyanta farklı
// hediye notu) bu fazda desteklenmez; desteklenseydi birleştirme ölçütünün
// varyant değil "varyant + özelleştirme" olması gerekirdi.
func (s *Service) AddLineItem(ctx context.Context, cartID string, in AddLineItemInput) (models.LineItem, error) {
	if err := requireID("variant_id", in.VariantID); err != nil {
		return models.LineItem{}, err
	}
	title := strings.TrimSpace(in.Title)
	if err := requireText("title", title); err != nil {
		return models.LineItem{}, err
	}
	if err := checkQuantity(in.Quantity); err != nil {
		return models.LineItem{}, err
	}
	if err := checkAmount("unit_price", in.UnitPrice, models.MaxAmount); err != nil {
		return models.LineItem{}, err
	}

	var item models.LineItem
	_, err := s.mutate(ctx, cartID, func(ctx context.Context, cart models.Cart) error {
		existing, err := s.store.GetLineItemByVariant(ctx, cart.ID, in.VariantID)
		switch {
		case err == nil:
			// Toplam, taşma olmadan sınanır: iki adedin toplamı int64'e sığsa
			// bile modelin adet tavanının üstüne çıkamaz.
			if existing.Quantity > models.MaxQuantity-in.Quantity {
				return errors.Invalid(CodeInvalidInput,
					"satır adedi birleştirildiğinde sınırı aşıyor: %d + %d > %d",
					existing.Quantity, in.Quantity, models.MaxQuantity)
			}
			item, err = s.store.SetLineItemQuantity(ctx, cart.ID, existing.ID, existing.Quantity+in.Quantity)
			return err
		case errors.IsNotFound(err):
			item, err = s.store.CreateLineItem(ctx, models.LineItem{
				ID:        models.NewLineItemID(),
				CartID:    cart.ID,
				VariantID: in.VariantID,
				Title:     title,
				Quantity:  in.Quantity,
				UnitPrice: in.UnitPrice,
				Metadata:  in.Metadata,
			})
			return err
		default:
			return err
		}
	})
	if err != nil {
		return models.LineItem{}, err
	}
	return item, nil
}

// UpdateLineItemQuantity satırın adedini MUTLAK değerle yazar.
//
// quantity sıfır ya da negatifse errors.Invalid döner; satır SİLİNMEZ. "Adedi
// sıfır yap" ile "satırı kaldır" ayrı niyetlerdir ve ayrı metotları vardır
// ([Service.RemoveLineItem]); birini diğerine çevirmek, adet alanına sıfır
// gönderen bir hatanın sessizce veri silmesi demek olurdu.
func (s *Service) UpdateLineItemQuantity(ctx context.Context, cartID, lineID string, quantity int64) (models.LineItem, error) {
	if err := requireID("line_item_id", lineID); err != nil {
		return models.LineItem{}, err
	}
	if err := checkQuantity(quantity); err != nil {
		return models.LineItem{}, err
	}

	var item models.LineItem
	_, err := s.mutate(ctx, cartID, func(ctx context.Context, cart models.Cart) error {
		var err error
		item, err = s.store.SetLineItemQuantity(ctx, cart.ID, lineID, quantity)
		return err
	})
	if err != nil {
		return models.LineItem{}, err
	}
	return item, nil
}

// RemoveLineItem satırı sepetten kaldırır (yumuşak silme).
// Satır sepette yoksa errors.NotFound döner.
func (s *Service) RemoveLineItem(ctx context.Context, cartID, lineID string) error {
	if err := requireID("line_item_id", lineID); err != nil {
		return err
	}
	_, err := s.mutate(ctx, cartID, func(ctx context.Context, cart models.Cart) error {
		return s.store.SoftDeleteLineItem(ctx, cart.ID, lineID)
	})
	return err
}
