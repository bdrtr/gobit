package service

import (
	"context"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// CapturePayment bloke edilmiş tutarı tahsil eder ve bir [models.Payment]
// üretir.
//
// amount SIFIR verilirse oturumun bloke edilmiş tutarının tamamı çekilir;
// sağlayıcı sözleşmesindeki kuralın aynısıdır. Bloke edilen tutardan fazlası
// çekilemez (errors.Conflict).
//
// # Tahsilat blokajı KAPATIR
//
// Tahsil edilen oturumun bloke tutarı koleksiyonun bloke toplamından TAMAMEN
// düşülür: çekilen kısım tahsilata dönüşmüştür, çekilmeyen kısım serbest
// bırakılır. Oturumun kendi bloke tutarı da fiilen çekilen tutara iner.
//
// Kısmi tahsilatta bunun ihmali onarılamaz bir hataydı: oturum artık "captured"
// olduğu için iptal edilemez ve çekilmeyen fark serbest bırakılamazdı —
// koleksiyon "müşterinin üzerinde ne kadar bloke var" sorusuna sonsuza kadar
// fazla cevap verirdi. Gerçek sağlayıcılar da tahsilatta kalan blokajı bırakır.
//
// Geçiş tablosu için bkz. [models.SessionStatus.CaptureAction]. Zaten tahsil
// edilmiş bir oturumda AYNI tutarla (ya da sıfırla) yapılan ikinci çağrı hata
// dönmez, var olan tahsilatı döner — bir oturumdan en fazla bir tahsilat çıkar
// ve idempotentlik oradan gelir. Farklı bir tutar istenirse errors.Conflict
// döner; o artık bir tekrar değil, yeni bir istektir.
//
// Kilit sırası: koleksiyon -> oturum.
func (s *Service) CapturePayment(ctx context.Context, sessionID string, amount int64) (models.Payment, error) {
	if err := requireText(fieldSessionID, sessionID); err != nil {
		return models.Payment{}, err
	}
	if err := requireOptionalAmount("amount", amount); err != nil {
		return models.Payment{}, err
	}

	prov, err := s.providerForSession(ctx, sessionID)
	if err != nil {
		return models.Payment{}, err
	}

	var out models.Payment
	err = s.store.WithTx(ctx, func(ctx context.Context) error {
		col, ses, err := s.lockCollectionAndSession(ctx, sessionID)
		if err != nil {
			return err
		}

		switch ses.Status.CaptureAction() {
		case models.ActionNoop:
			existing, err := s.store.PaymentBySession(ctx, ses.ID)
			if err != nil {
				return err
			}
			if amount != 0 && amount != existing.Amount {
				return errors.Conflict(CodeInvalidTransition,
					"oturum %d tutarıyla tahsil edilmiş; %d ile yeniden tahsil edilemez (%s)",
					existing.Amount, amount, ses.ID)
			}
			s.log.DebugContext(ctx, "oturum zaten tahsil edilmiş, işlem yapılmadı",
				"oturum", ses.ID, "tahsilat", existing.ID)
			out = existing
			return nil
		case models.ActionConflict:
			return conflictTransition("tahsil edilemez", ses)
		case models.ActionProceed:
			// Aşağıda ele alınır.
		}

		captured := amount
		if captured == 0 {
			captured = ses.AuthorizedAmount
		}
		if captured > ses.AuthorizedAmount {
			return errors.Conflict(CodeInvalidTransition,
				"tahsilat tutarı bloke tutarı aşamaz: istenen %d, bloke %d (%s)",
				captured, ses.AuthorizedAmount, ses.ID)
		}

		if err := prov.Capture(ctx, ses.ExternalID, captured); err != nil {
			return err
		}

		// Oturumun blokajı KAPANIR: çekilen kısım tahsilata dönüşür, çekilmeyen
		// kısım serbest bırakılır. Koleksiyonun bloke toplamı yalnızca HÂLÂ
		// bloke olan tutarı gösterir; oturum artık "captured" olduğu için
		// buradan düşülmeseydi aynı para hem bloke hem tahsil edilmiş sayılır
		// ve mutabakat bozulurdu. Kısmi tahsilatta durum daha da ağırdır:
		// oturum artık iptal edilemeyeceği için çekilmeyen fark bir daha
		// serbest bırakılamazdı.
		blocked := ses.AuthorizedAmount
		if blocked > col.AuthorizedAmount {
			return errors.Internal(CodeInconsistentState,
				"koleksiyonun bloke tutarı (%d) oturumunkinden (%d) küçük (%s)",
				col.AuthorizedAmount, blocked, ses.ID)
		}

		if _, err := s.store.UpdatePaymentSessionState(ctx, ses.ID,
			models.SessionCaptured, captured, ses.Data, ses.DeclineReason); err != nil {
			return err
		}

		payment, err := s.store.CreatePayment(ctx, models.Payment{
			ID:                  models.NewPaymentID(),
			PaymentSessionID:    ses.ID,
			PaymentCollectionID: col.ID,
			Amount:              captured,
			CurrencyCode:        ses.CurrencyCode,
			CapturedAt:          time.Now().UTC(),
		})
		if err != nil {
			return err
		}

		if _, err := s.writeCollectionTotals(ctx, col,
			col.AuthorizedAmount-blocked, col.CapturedAmount+captured, col.RefundedAmount); err != nil {
			return err
		}

		out = payment
		return nil
	})
	if err != nil {
		return models.Payment{}, err
	}
	return out, nil
}

// RefundPayment tahsil edilmiş tutarın tamamını ya da bir kısmını iade eder.
//
// amount SIFIR verilirse tahsilattan KALAN tutarın tamamı iade edilir. Kalan
// tutarı aşan bir istek errors.Conflict döner; iade edilecek bir şey kalmamışsa
// da errors.Conflict ([CodeNothingToRefund]) döner.
//
// # İdempotency
//
// Bu metot İDEMPOTENT DEĞİLDİR ve olması da doğru olmazdı: iki kez çağrılan
// 10 birimlik iade, 20 birimlik gerçek bir iadedir ve kayıt bunu iki satır
// olarak göstermelidir. Tekrar korumasının yeri burası değil, isteğin
// kendisidir; dışarıdan gelen çağrılar Faz 9'daki idempotency middleware'i ile
// korunur. Saga da bu adımı telafi olarak KULLANMAZ — telafi
// [Service.CancelPayment]'tır ve o idempotenttir.
//
// Kilit sırası: koleksiyon -> oturum -> tahsilat.
func (s *Service) RefundPayment(
	ctx context.Context,
	paymentID string,
	amount int64,
	reason string,
) (models.Refund, error) {
	if err := requireText("payment_id", paymentID); err != nil {
		return models.Refund{}, err
	}
	if err := requireOptionalAmount("amount", amount); err != nil {
		return models.Refund{}, err
	}
	if err := checkTextLen("reason", reason); err != nil {
		return models.Refund{}, err
	}

	preview, err := s.store.GetPayment(ctx, paymentID)
	if err != nil {
		return models.Refund{}, err
	}
	prov, err := s.providerForSession(ctx, preview.PaymentSessionID)
	if err != nil {
		return models.Refund{}, err
	}

	var out models.Refund
	err = s.store.WithTx(ctx, func(ctx context.Context) error {
		col, ses, err := s.lockCollectionAndSession(ctx, preview.PaymentSessionID)
		if err != nil {
			return err
		}
		payment, err := s.store.LockPayment(ctx, paymentID)
		if err != nil {
			return err
		}

		remaining := payment.RefundableAmount()
		if remaining == 0 {
			return errors.Conflict(CodeNothingToRefund,
				"tahsilatın tamamı zaten iade edilmiş: %s", payment.ID)
		}
		refund := amount
		if refund == 0 {
			refund = remaining
		}
		if refund > remaining {
			return errors.Conflict(CodeInvalidTransition,
				"iade tutarı kalan tutarı aşamaz: istenen %d, kalan %d (%s)",
				refund, remaining, payment.ID)
		}

		if err := prov.Refund(ctx, ses.ExternalID, refund); err != nil {
			return err
		}

		if _, err := s.store.UpdatePaymentRefundedAmount(ctx, payment.ID,
			payment.RefundedAmount+refund); err != nil {
			return err
		}

		created, err := s.store.CreateRefund(ctx, models.Refund{
			ID:        models.NewRefundID(),
			PaymentID: payment.ID,
			Amount:    refund,
			Reason:    strings.TrimSpace(reason),
		})
		if err != nil {
			return err
		}

		if _, err := s.writeCollectionTotals(ctx, col,
			col.AuthorizedAmount, col.CapturedAmount, col.RefundedAmount+refund); err != nil {
			return err
		}

		out = created
		return nil
	})
	if err != nil {
		return models.Refund{}, err
	}
	return out, nil
}
