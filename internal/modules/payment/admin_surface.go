package payment

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// AdminName is the container name of the payment module's panel surface
// (ADR 0287).
const AdminName = ModuleName + ".admin"

// ReceivingSurface is the payment module's panel surface: an operator records
// that the money an offline method promised arrived (ADR 0287). Only
// primitives cross it, as across every surface the panel resolves (ADR 0001).
type ReceivingSurface struct {
	svc *service.Service
}

// OfflineMethods lists the methods whose money comes after the order is
// placed, the ones a telephone order's completion takes (ADR 0306).
func (s *ReceivingSurface) OfflineMethods(context.Context) []string {
	return s.svc.OfflineMethods()
}

// RecordReceived captures the session's awaited money whole and returns the
// capture, its amount and its currency; a second call returns the first
// capture. A session whose provider moves its money at the checkout is refused.
func (s *ReceivingSurface) RecordReceived(
	ctx context.Context, sessionID string,
) (paymentID string, amount int64, currencyCode string, err error) {
	payment, err := s.svc.RecordReceived(ctx, sessionID)
	if err != nil {
		return "", 0, "", err
	}

	return payment.ID, payment.Amount, payment.CurrencyCode, nil
}
