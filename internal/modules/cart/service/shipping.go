package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/internal/modules/cart/models"
)

// AddShippingMethodInput sepete eklenecek kargo yönteminin alanlarıdır.
type AddShippingMethodInput struct {
	// Name yöntemin görünen adıdır; ZORUNLUDUR.
	Name string
	// ShippingOptionID fulfillment modülündeki seçeneğin kimliğidir;
	// OPSİYONELDİR (Faz 7'de seçenek kataloğu gelecek) ve foreign key değildir.
	ShippingOptionID string
	// Amount kargo tutarıdır (minor unit); negatif olamaz.
	Amount int64
	// Data sağlayıcıya özgü serbest veridir.
	Data map[string]any
}

// AddShippingMethod sepete bir kargo yöntemi ekler.
//
// Bir sepette birden çok yöntem bulunabilir (farklı kargo profilleri ayrı
// gönderi demektir), ama AYNI kargo seçeneği ikinci kez eklenemez: tekrar,
// aynı gönderinin iki kez ücretlendirilmesi olurdu ve
// cart_shipping_methods_cart_option_uniq indeksi bunu errors.Conflict'e
// çevirir. Seçeneksiz (Faz 5) yöntemler kısıtın dışındadır.
//
// Tutar sepetin shipping_total'ına BURADA toplanmaz; toplama calculate_totals
// workflow'unun işidir (bkz. [Service.SetTotals]). Ekleme yalnızca sepetin
// şekil sayacını artırır ve toplamları bayat işaretler.
func (s *Service) AddShippingMethod(ctx context.Context, cartID string, in AddShippingMethodInput) (models.ShippingMethod, error) {
	name := strings.TrimSpace(in.Name)
	if err := requireText("name", name); err != nil {
		return models.ShippingMethod{}, err
	}
	if in.ShippingOptionID != "" {
		if err := requireID("shipping_option_id", in.ShippingOptionID); err != nil {
			return models.ShippingMethod{}, err
		}
	}
	if err := checkAmount("amount", in.Amount, models.MaxAmount); err != nil {
		return models.ShippingMethod{}, err
	}

	var method models.ShippingMethod
	_, err := s.mutate(ctx, cartID, func(ctx context.Context, cart models.Cart) error {
		var err error
		method, err = s.store.CreateShippingMethod(ctx, models.ShippingMethod{
			ID:               models.NewShippingMethodID(),
			CartID:           cart.ID,
			Name:             name,
			ShippingOptionID: in.ShippingOptionID,
			Amount:           in.Amount,
			Data:             in.Data,
		})
		return err
	})
	if err != nil {
		return models.ShippingMethod{}, err
	}
	return method, nil
}

// RemoveShippingMethod kargo yöntemini sepetten kaldırır (yumuşak silme).
// Yöntem sepette yoksa errors.NotFound döner.
func (s *Service) RemoveShippingMethod(ctx context.Context, cartID, methodID string) error {
	if err := requireID("shipping_method_id", methodID); err != nil {
		return err
	}
	_, err := s.mutate(ctx, cartID, func(ctx context.Context, cart models.Cart) error {
		return s.store.SoftDeleteShippingMethod(ctx, cart.ID, methodID)
	})
	return err
}
