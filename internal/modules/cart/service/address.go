package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/internal/modules/cart/models"
)

// AddressInput sepete yazılacak adresin alanlarıdır.
//
// Alanların hepsi isteğe bağlıdır: teslimat bilgisi ödeme akışı boyunca parça
// parça toplanır ve eksik bir adresi kaydetmek geçerli bir ara durumdur.
// Adresin teslimat için YETERLİ olup olmadığına karar vermek fulfillment'ın
// (Faz 7) işidir; sepet burada yalnızca biçimi doğrular.
type AddressInput struct {
	// SourceAddressID adresin kopyalandığı customer adresinin kimliğidir;
	// yalnızca KÖKENİ belgeler, okuma için kullanılmaz.
	SourceAddressID string
	FirstName       string
	LastName        string
	Company         string
	Address1        string
	Address2        string
	City            string
	Province        string
	// PostalCode posta kodudur.
	PostalCode string
	// CountryCode ISO 3166-1 alpha-2 ülke kodudur; verilirse iki harf olmalıdır.
	CountryCode string
	Phone       string
	// Metadata çağıranın serbest ek verisidir.
	Metadata map[string]any
}

// SetShippingAddress sepetin kargo adresini yazar; varsa ÜZERİNE yazar.
//
// Sepetin adresi, customer modülündeki defterden KOPYALANIR (bkz.
// [models.CartAddress] belgesi): sepet kendi kopyasını tutar ki müşteri
// defterindeki kaydını sonradan değiştirdiğinde geçmiş sepet bozulmasın.
// Kopyalamayı yapan taraf çağırandır; sepet servisi customer modülünü çağırmaz
// (ADR 0001).
//
// Adresin değişmesi vergiyi etkiler, bu yüzden sepetin şekil sayacını artırır
// ve toplamlar bayat sayılır.
func (s *Service) SetShippingAddress(ctx context.Context, cartID string, in AddressInput) (models.CartAddress, error) {
	return s.setAddress(ctx, cartID, models.AddressShipping, in)
}

// SetBillingAddress sepetin fatura adresini yazar; varsa ÜZERİNE yazar.
//
// Kopyalama gerekçesi ve bayatlatma etkisi [Service.SetShippingAddress] ile
// aynıdır.
func (s *Service) SetBillingAddress(ctx context.Context, cartID string, in AddressInput) (models.CartAddress, error) {
	return s.setAddress(ctx, cartID, models.AddressBilling, in)
}

// setAddress verilen türdeki adresi yazar.
//
// Var olan kayıt GÜNCELLENİR, yenisi açılmaz: kimliğin sabit kalması, adrese
// verilen bir referansın (log kaydı, sipariş kopyası) düzeltmeden sonra da
// geçerli kalması demektir. Üretilen kimlik yalnızca ilk yazmada kullanılır.
func (s *Service) setAddress(ctx context.Context, cartID string, kind models.AddressType, in AddressInput) (models.CartAddress, error) {
	if in.SourceAddressID != "" {
		if err := requireID("source_address_id", in.SourceAddressID); err != nil {
			return models.CartAddress{}, err
		}
	}
	country, err := normalizeCountry(in.CountryCode)
	if err != nil {
		return models.CartAddress{}, err
	}
	// Sıra bilinçli olarak sabittir: harita üzerinde dönmek, birden çok alan
	// birden uzun olduğunda hangi hatanın döneceğini rastgele bırakırdı.
	for _, field := range []struct{ label, value string }{
		{"first_name", in.FirstName},
		{"last_name", in.LastName},
		{"company", in.Company},
		{"address_1", in.Address1},
		{"address_2", in.Address2},
		{"city", in.City},
		{"province", in.Province},
		{"postal_code", in.PostalCode},
		{"phone", in.Phone},
	} {
		if err := checkTextLen(field.label, field.value); err != nil {
			return models.CartAddress{}, err
		}
	}

	var addr models.CartAddress
	_, err = s.mutate(ctx, cartID, func(ctx context.Context, cart models.Cart) error {
		var err error
		addr, err = s.store.UpsertCartAddress(ctx, models.CartAddress{
			ID:              models.NewAddressID(),
			CartID:          cart.ID,
			Type:            kind,
			SourceAddressID: in.SourceAddressID,
			FirstName:       strings.TrimSpace(in.FirstName),
			LastName:        strings.TrimSpace(in.LastName),
			Company:         strings.TrimSpace(in.Company),
			Address1:        strings.TrimSpace(in.Address1),
			Address2:        strings.TrimSpace(in.Address2),
			City:            strings.TrimSpace(in.City),
			Province:        strings.TrimSpace(in.Province),
			PostalCode:      strings.TrimSpace(in.PostalCode),
			CountryCode:     country,
			Phone:           strings.TrimSpace(in.Phone),
			Metadata:        in.Metadata,
		})
		return err
	})
	if err != nil {
		return models.CartAddress{}, err
	}
	return addr, nil
}
