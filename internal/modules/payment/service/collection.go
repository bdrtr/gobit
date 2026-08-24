package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// CreateCollectionInput yeni bir ödeme koleksiyonunun alanlarıdır.
type CreateCollectionInput struct {
	// Reference çağıranın kendi kaydının kimliğidir (sepet ya da sipariş);
	// zorunludur. FOREIGN KEY DEĞİLDİR ve varlığı burada doğrulanmaz.
	Reference string
	// Amount toplanması gereken toplam tutardır (minor unit); pozitif olmalıdır.
	Amount int64
	// CurrencyCode ISO 4217 kodudur; zorunludur.
	CurrencyCode string
	// Metadata çağıranın serbest ek verisidir.
	Metadata map[string]any
}

// CreatePaymentCollection yeni bir ödeme koleksiyonu oluşturur.
//
// Koleksiyon "not_paid" durumunda doğar: henüz açılmış bir oturum ve tahsilat
// yoktur. Tutar SIFIR OLAMAZ (bkz. [models.MinAmount]); tutarı sıfır olan bir
// sipariş için ödeme toplanmaz ve böyle bir koleksiyon hiçbir zaman "captured"
// olamayacağı için sonsuza kadar ödeme bekleyen ölü bir kayıt olurdu.
func (s *Service) CreatePaymentCollection(
	ctx context.Context,
	in CreateCollectionInput,
) (models.PaymentCollection, error) {
	reference := strings.TrimSpace(in.Reference)
	if err := requireText("reference", reference); err != nil {
		return models.PaymentCollection{}, err
	}
	if err := requireAmount("amount", in.Amount); err != nil {
		return models.PaymentCollection{}, err
	}
	currency, err := normalizeCurrency(in.CurrencyCode)
	if err != nil {
		return models.PaymentCollection{}, err
	}

	return s.store.CreatePaymentCollection(ctx, models.PaymentCollection{
		ID:           models.NewPaymentCollectionID(),
		Reference:    reference,
		Amount:       in.Amount,
		CurrencyCode: currency,
		Status:       models.CollectionNotPaid,
		Metadata:     in.Metadata,
	})
}

// GetPaymentCollection koleksiyonu kimliğiyle döner; yoksa errors.NotFound.
func (s *Service) GetPaymentCollection(ctx context.Context, id string) (models.PaymentCollection, error) {
	if err := requireText("id", id); err != nil {
		return models.PaymentCollection{}, err
	}
	return s.store.GetPaymentCollection(ctx, id)
}

// ListCollectionsInput koleksiyon listelemesinin girdisidir.
type ListCollectionsInput struct {
	// Reference verilirse yalnızca o referansa bağlı koleksiyonlar döner.
	Reference *string
	// Status verilirse yalnızca o durumdaki koleksiyonlar döner.
	Status *string
	// Page sayfalama parametreleridir.
	Page Page
}

// ListPaymentCollections koleksiyonları sayfalayarak döner.
// İkinci dönüş değeri sayfaya değil, süzgece uyan TÜM satırlara ait sayıdır.
func (s *Service) ListPaymentCollections(
	ctx context.Context,
	in ListCollectionsInput,
) ([]models.PaymentCollection, int64, error) {
	page, err := in.Page.normalize()
	if err != nil {
		return nil, 0, err
	}

	filter := models.CollectionFilter{Limit: page.Limit, Offset: page.Offset}
	if in.Reference != nil {
		reference := strings.TrimSpace(*in.Reference)
		if err := requireText("reference", reference); err != nil {
			return nil, 0, err
		}
		filter.Reference = &reference
	}
	if in.Status != nil {
		status := models.CollectionStatus(strings.TrimSpace(*in.Status))
		if !status.Valid() {
			return nil, 0, invalidStatus(*in.Status)
		}
		value := status.String()
		filter.Status = &value
	}

	return s.store.ListPaymentCollections(ctx, filter)
}

// ListPaymentCollectionsByIDs verilen kimliklerin koleksiyonlarını TEK sorguda
// döner. Bulunamayan kimlik için kayıt dönmez; bu bir hata değildir.
func (s *Service) ListPaymentCollectionsByIDs(
	ctx context.Context,
	ids []string,
) ([]models.PaymentCollection, error) {
	if len(ids) == 0 {
		return []models.PaymentCollection{}, nil
	}
	return s.store.PaymentCollectionsByIDs(ctx, ids)
}

// ListPaymentSessions koleksiyonun oturumlarını döner.
//
// Koleksiyonun varlığı önce doğrulanır: olmayan bir koleksiyon için "oturum
// yok" yerine "koleksiyon yok" denmelidir; ikisi çağıran için farklı şeylerdir.
func (s *Service) ListPaymentSessions(ctx context.Context, collectionID string) ([]models.PaymentSession, error) {
	if err := requireText("payment_collection_id", collectionID); err != nil {
		return nil, err
	}
	if _, err := s.store.GetPaymentCollection(ctx, collectionID); err != nil {
		return nil, err
	}
	return s.store.ListPaymentSessionsByCollection(ctx, collectionID)
}

// ListPayments koleksiyonun tahsilatlarını döner.
func (s *Service) ListPayments(ctx context.Context, collectionID string) ([]models.Payment, error) {
	if err := requireText("payment_collection_id", collectionID); err != nil {
		return nil, err
	}
	if _, err := s.store.GetPaymentCollection(ctx, collectionID); err != nil {
		return nil, err
	}
	return s.store.ListPaymentsByCollection(ctx, collectionID)
}

// GetPaymentSession oturumu kimliğiyle döner; yoksa errors.NotFound.
func (s *Service) GetPaymentSession(ctx context.Context, id string) (models.PaymentSession, error) {
	if err := requireText("id", id); err != nil {
		return models.PaymentSession{}, err
	}
	return s.store.GetPaymentSession(ctx, id)
}

// GetPayment tahsilatı kimliğiyle döner; yoksa errors.NotFound.
func (s *Service) GetPayment(ctx context.Context, id string) (models.Payment, error) {
	if err := requireText("id", id); err != nil {
		return models.Payment{}, err
	}
	return s.store.GetPayment(ctx, id)
}

// ListRefunds tahsilatın iadelerini döner.
func (s *Service) ListRefunds(ctx context.Context, paymentID string) ([]models.Refund, error) {
	if err := requireText("payment_id", paymentID); err != nil {
		return nil, err
	}
	if _, err := s.store.GetPayment(ctx, paymentID); err != nil {
		return nil, err
	}
	return s.store.ListRefundsByPayment(ctx, paymentID)
}
