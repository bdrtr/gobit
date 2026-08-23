package service

import (
	"context"
	"log/slog"

	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// AddressInput bir adresin yazma girdisidir.
type AddressInput struct {
	// FirstName adresin üzerindeki addır; boş bırakılabilir.
	FirstName string
	// LastName adresin üzerindeki soyaddır; boş bırakılabilir.
	LastName string
	// Company şirket adıdır; boş bırakılabilir.
	Company string
	// Address1 adresin ilk satırıdır; zorunludur.
	Address1 string
	// Address2 adresin ikinci satırıdır; boş bırakılabilir.
	Address2 string
	// City şehirdir; zorunludur.
	City string
	// CountryCode ISO 3166-1 alpha-2 ülke kodudur; zorunludur, BÜYÜK harfe
	// normalize edilerek saklanır.
	CountryCode string
	// PostalCode posta kodudur; boş bırakılabilir.
	PostalCode string
	// Phone adresin iletişim telefonudur; boş bırakılabilir.
	Phone string
	// IsDefaultShipping adresi varsayılan kargo adresi yapar; müşterinin varsa
	// eski varsayılanı AYNI işlemde temizlenir.
	IsDefaultShipping bool
	// IsDefaultBilling adresi varsayılan fatura adresi yapar.
	IsDefaultBilling bool
}

// CreateAddress müşterinin yeni adresini ekler; müşteri yoksa errors.NotFound.
func (s *Service) CreateAddress(ctx context.Context, customerID string, in AddressInput) (models.CustomerAddress, error) {
	if err := s.ready(); err != nil {
		return models.CustomerAddress{}, err
	}
	if err := requireID(customerID, models.CustomerIDPrefix, "müşteri kimliği"); err != nil {
		return models.CustomerAddress{}, err
	}

	country, err := normalizeCountryCode(in.CountryCode)
	if err != nil {
		return models.CustomerAddress{}, err
	}
	if err := validateAddressText(in); err != nil {
		return models.CustomerAddress{}, err
	}

	now := s.clock()
	return s.repo.CreateAddress(ctx, models.CustomerAddress{
		ID:                models.NewAddressID(now),
		CustomerID:        customerID,
		FirstName:         in.FirstName,
		LastName:          in.LastName,
		Company:           in.Company,
		Address1:          in.Address1,
		Address2:          in.Address2,
		City:              in.City,
		CountryCode:       country,
		PostalCode:        in.PostalCode,
		Phone:             in.Phone,
		IsDefaultShipping: in.IsDefaultShipping,
		IsDefaultBilling:  in.IsDefaultBilling,
		CreatedAt:         now,
	})
}

// GetAddress müşterinin adresini döner; yoksa errors.NotFound.
//
// Sahiplik denetimi sorgunun WHERE koşulundadır: başka bir müşterinin adresinin
// kimliği verilse bile kayıt DÖNMEZ.
func (s *Service) GetAddress(ctx context.Context, customerID, addressID string) (models.CustomerAddress, error) {
	if err := s.ready(); err != nil {
		return models.CustomerAddress{}, err
	}
	if err := requireAddressIDs(customerID, addressID); err != nil {
		return models.CustomerAddress{}, err
	}
	return s.repo.GetAddress(ctx, customerID, addressID)
}

// ListAddresses müşterinin adreslerini döner.
//
// Müşterinin varlığı ÖNCE doğrulanır: olmayan bir müşteri için boş liste
// dönseydi istemci 404 yerine "hiç adresi yok" sanırdı.
func (s *Service) ListAddresses(ctx context.Context, customerID string) ([]models.CustomerAddress, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := requireID(customerID, models.CustomerIDPrefix, "müşteri kimliği"); err != nil {
		return nil, err
	}
	if _, err := s.repo.GetCustomer(ctx, customerID); err != nil {
		return nil, err
	}
	return s.repo.ListAddresses(ctx, customerID)
}

// UpdateAddressInput bir adresin kısmi güncelleme girdisidir.
//
// nil alan "dokunma", dolu alan "bu değeri yaz" demektir. Varsayılan işaretleri
// burada YOKTUR; onlar için [Service.SetDefaultShippingAddress] ve
// [Service.SetDefaultBillingAddress] kullanılır, çünkü işaret değiştirmek
// müşterinin diğer adreslerini de ilgilendirir.
type UpdateAddressInput struct {
	// FirstName yeni addır.
	FirstName *string
	// LastName yeni soyaddır.
	LastName *string
	// Company yeni şirket adıdır.
	Company *string
	// Address1 adresin yeni ilk satırıdır; verilirse boş olamaz.
	Address1 *string
	// Address2 adresin yeni ikinci satırıdır.
	Address2 *string
	// City yeni şehirdir; verilirse boş olamaz.
	City *string
	// CountryCode yeni ülke kodudur; verilirse doğrulanır ve BÜYÜK harfe çevrilir.
	CountryCode *string
	// PostalCode yeni posta kodudur.
	PostalCode *string
	// Phone yeni telefondur.
	Phone *string
}

// UpdateAddress adresin verilen alanlarını günceller; yoksa errors.NotFound.
func (s *Service) UpdateAddress(
	ctx context.Context,
	customerID, addressID string,
	in UpdateAddressInput,
) (models.CustomerAddress, error) {
	if err := s.ready(); err != nil {
		return models.CustomerAddress{}, err
	}
	if err := requireAddressIDs(customerID, addressID); err != nil {
		return models.CustomerAddress{}, err
	}

	patch := models.AddressPatch{
		FirstName:  in.FirstName,
		LastName:   in.LastName,
		Company:    in.Company,
		Address1:   in.Address1,
		Address2:   in.Address2,
		City:       in.City,
		PostalCode: in.PostalCode,
		Phone:      in.Phone,
	}
	if in.CountryCode != nil {
		country, err := normalizeCountryCode(*in.CountryCode)
		if err != nil {
			return models.CustomerAddress{}, err
		}
		patch.CountryCode = &country
	}
	if err := validateAddressPatch(patch); err != nil {
		return models.CustomerAddress{}, err
	}

	return s.repo.UpdateAddress(ctx, customerID, addressID, patch, s.clock())
}

// DeleteAddress adresi soft delete ile siler; yoksa errors.NotFound.
func (s *Service) DeleteAddress(ctx context.Context, customerID, addressID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireAddressIDs(customerID, addressID); err != nil {
		return err
	}
	return s.repo.DeleteAddress(ctx, customerID, addressID, s.clock())
}

// SetDefaultShippingAddress adresi müşterinin varsayılan kargo adresi yapar.
//
// Müşteri başına EN FAZLA BİR varsayılan kargo adresi olabilir: eski işaret
// aynı işlemde kaldırılır ve kısıt veritabanındaki kısmi benzersiz indeksle
// zorlanır (bkz. repository.Repo.SetDefaultAddress).
func (s *Service) SetDefaultShippingAddress(ctx context.Context, customerID, addressID string) (models.CustomerAddress, error) {
	return s.setDefault(ctx, customerID, addressID, models.DefaultShipping)
}

// SetDefaultBillingAddress adresi müşterinin varsayılan fatura adresi yapar.
//
// Kargo işaretinden BAĞIMSIZDIR: tek bir adresin ikisi birden olması da,
// iki işaretin farklı adreslere dağılması da geçerlidir.
func (s *Service) SetDefaultBillingAddress(ctx context.Context, customerID, addressID string) (models.CustomerAddress, error) {
	return s.setDefault(ctx, customerID, addressID, models.DefaultBilling)
}

// setDefault iki varsayılan atama yolunun ortak gövdesidir.
func (s *Service) setDefault(
	ctx context.Context,
	customerID, addressID string,
	kind models.DefaultKind,
) (models.CustomerAddress, error) {
	if err := s.ready(); err != nil {
		return models.CustomerAddress{}, err
	}
	if err := requireAddressIDs(customerID, addressID); err != nil {
		return models.CustomerAddress{}, err
	}

	address, err := s.repo.SetDefaultAddress(ctx, customerID, addressID, kind, s.clock())
	if err != nil {
		return models.CustomerAddress{}, err
	}

	s.log.DebugContext(ctx, "müşterinin varsayılan adresi güncellendi",
		slog.String("customer_id", customerID),
		slog.String("address_id", addressID),
		slog.String("tur", kind.String()),
	)
	return address, nil
}

// requireAddressIDs müşteri ve adresin kimliklerini birlikte doğrular.
func requireAddressIDs(customerID, addressID string) error {
	if err := requireID(customerID, models.CustomerIDPrefix, "müşteri kimliği"); err != nil {
		return err
	}
	return requireID(addressID, models.AddressIDPrefix, "adresin kimliği")
}

// validateAddressText adresin metin alanlarını doğrular.
func validateAddressText(in AddressInput) error {
	if err := requireText("adresin ilk satırı", in.Address1); err != nil {
		return err
	}
	if err := requireText("şehir", in.City); err != nil {
		return err
	}
	if err := validatePerson(in.FirstName, in.LastName, in.Phone); err != nil {
		return err
	}
	if err := checkLen("şirket", in.Company, models.MaxNameLen); err != nil {
		return err
	}
	if err := checkLen("adresin ilk satırı", in.Address1, models.MaxAddressLen); err != nil {
		return err
	}
	if err := checkLen("adresin ikinci satırı", in.Address2, models.MaxAddressLen); err != nil {
		return err
	}
	if err := checkLen("şehir", in.City, models.MaxNameLen); err != nil {
		return err
	}
	return checkLen("posta kodu", in.PostalCode, models.MaxPostalCodeLen)
}

// validateAddressPatch kısmi güncellemedeki alanları doğrular.
//
// Zorunlu alanlar (ilk satır, şehir) verilirse BOŞ OLAMAZ: kısmi güncelleme
// bir alanı atlayabilir ama var olan bir zorunluluğu kaldıramaz.
func validateAddressPatch(patch models.AddressPatch) error {
	if patch.Address1 != nil {
		if err := requireText("adresin ilk satırı", *patch.Address1); err != nil {
			return err
		}
		if err := checkLen("adresin ilk satırı", *patch.Address1, models.MaxAddressLen); err != nil {
			return err
		}
	}
	if patch.City != nil {
		if err := requireText("şehir", *patch.City); err != nil {
			return err
		}
		if err := checkLen("şehir", *patch.City, models.MaxNameLen); err != nil {
			return err
		}
	}
	if patch.Company != nil {
		if err := checkLen("şirket", *patch.Company, models.MaxNameLen); err != nil {
			return err
		}
	}
	if patch.Address2 != nil {
		if err := checkLen("adresin ikinci satırı", *patch.Address2, models.MaxAddressLen); err != nil {
			return err
		}
	}
	if patch.PostalCode != nil {
		if err := checkLen("posta kodu", *patch.PostalCode, models.MaxPostalCodeLen); err != nil {
			return err
		}
	}
	return validatePatchPerson(models.CustomerPatch{
		FirstName: patch.FirstName,
		LastName:  patch.LastName,
		Phone:     patch.Phone,
	})
}
