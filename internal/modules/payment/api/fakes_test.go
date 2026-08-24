package api_test

import (
	"context"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/api"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// fakePayments api.Payments'in senaryolanabilir karşılığıdır.
//
// HTTP davranışının gerçek bir veritabanı olmadan sınanabilmesi için vardır:
// handler'ların işi status kodu SEÇMEK değil, servisin tipli hatasını
// corehttp.WriteError'a vermektir ve bu ancak servis yerine bir sahte konarak
// tek tek doğrulanabilir.
type fakePayments struct {
	providerIDs []string

	collection models.PaymentCollection
	session    models.PaymentSession
	payment    models.Payment
	refund     models.Refund

	collections []models.PaymentCollection
	count       int64
	sessions    []models.PaymentSession
	payments    []models.Payment
	refunds     []models.Refund

	// err ayarlanırsa çağrılan her metot bu hatayı döner; hata sınıfının
	// status koduna doğru eşlendiği böyle sınanır.
	err error

	// sonCreateSession son CreateSession çağrısının girdisidir; gövdenin
	// servise BOZULMADAN ulaştığı bununla kanıtlanır.
	sonCreateSession service.CreateSessionInput
	// sonCollectionInput son CreatePaymentCollection çağrısının girdisidir.
	sonCollectionInput service.CreateCollectionInput
	// sonListInput son ListPaymentCollections çağrısının girdisidir.
	sonListInput service.ListCollectionsInput
	// sonCaptureAmount son CapturePayment çağrısının tutarıdır.
	sonCaptureAmount int64
	// sonRefundAmount ve sonRefundReason son RefundPayment çağrısının
	// argümanlarıdır.
	sonRefundAmount int64
	sonRefundReason string
	// cancelCagrisi CancelPayment'ın çağrılıp çağrılmadığını bildirir.
	cancelCagrisi bool
}

// Sahtenin handler'ın beklediği yüzeyi karşıladığı derleme zamanında
// doğrulanır.
var _ api.Payments = (*fakePayments)(nil)

// ProviderIDs kayıtlı sağlayıcı kimliklerini döner.
func (f *fakePayments) ProviderIDs(_ context.Context) []string { return f.providerIDs }

// CreatePaymentCollection senaryolanmış koleksiyonu döner.
func (f *fakePayments) CreatePaymentCollection(
	_ context.Context,
	in service.CreateCollectionInput,
) (models.PaymentCollection, error) {
	f.sonCollectionInput = in
	if f.err != nil {
		return models.PaymentCollection{}, f.err
	}
	return f.collection, nil
}

// GetPaymentCollection senaryolanmış koleksiyonu döner.
func (f *fakePayments) GetPaymentCollection(_ context.Context, _ string) (models.PaymentCollection, error) {
	if f.err != nil {
		return models.PaymentCollection{}, f.err
	}
	return f.collection, nil
}

// ListPaymentCollections senaryolanmış sayfayı döner.
func (f *fakePayments) ListPaymentCollections(
	_ context.Context,
	in service.ListCollectionsInput,
) ([]models.PaymentCollection, int64, error) {
	f.sonListInput = in
	if f.err != nil {
		return nil, 0, f.err
	}
	return f.collections, f.count, nil
}

// CreateSession senaryolanmış oturumu döner.
func (f *fakePayments) CreateSession(
	_ context.Context,
	_, _ string,
	in service.CreateSessionInput,
) (models.PaymentSession, error) {
	f.sonCreateSession = in
	if f.err != nil {
		return models.PaymentSession{}, f.err
	}
	return f.session, nil
}

// GetPaymentSession senaryolanmış oturumu döner.
func (f *fakePayments) GetPaymentSession(_ context.Context, _ string) (models.PaymentSession, error) {
	if f.err != nil {
		return models.PaymentSession{}, f.err
	}
	return f.session, nil
}

// ListPaymentSessions senaryolanmış oturumları döner.
func (f *fakePayments) ListPaymentSessions(_ context.Context, _ string) ([]models.PaymentSession, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.sessions, nil
}

// AuthorizePayment senaryolanmış oturumu döner.
func (f *fakePayments) AuthorizePayment(_ context.Context, _ string) (models.PaymentSession, error) {
	if f.err != nil {
		return models.PaymentSession{}, f.err
	}
	return f.session, nil
}

// CapturePayment senaryolanmış tahsilatı döner.
func (f *fakePayments) CapturePayment(_ context.Context, _ string, amount int64) (models.Payment, error) {
	f.sonCaptureAmount = amount
	if f.err != nil {
		return models.Payment{}, f.err
	}
	return f.payment, nil
}

// CancelPayment iptali kaydeder.
func (f *fakePayments) CancelPayment(_ context.Context, _ string) error {
	f.cancelCagrisi = true
	return f.err
}

// GetPayment senaryolanmış tahsilatı döner.
func (f *fakePayments) GetPayment(_ context.Context, _ string) (models.Payment, error) {
	if f.err != nil {
		return models.Payment{}, f.err
	}
	return f.payment, nil
}

// ListPayments senaryolanmış tahsilatları döner.
func (f *fakePayments) ListPayments(_ context.Context, _ string) ([]models.Payment, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.payments, nil
}

// RefundPayment senaryolanmış iadeyi döner.
func (f *fakePayments) RefundPayment(
	_ context.Context,
	_ string,
	amount int64,
	reason string,
) (models.Refund, error) {
	f.sonRefundAmount, f.sonRefundReason = amount, reason
	if f.err != nil {
		return models.Refund{}, f.err
	}
	return f.refund, nil
}

// ListRefunds senaryolanmış iadeleri döner.
func (f *fakePayments) ListRefunds(_ context.Context, _ string) ([]models.Refund, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.refunds, nil
}

// notFound testlerde kullanılan tipli hatadır.
func notFound() error {
	return errors.NotFound("payment_collection_not_found", "koleksiyon bulunamadı")
}
