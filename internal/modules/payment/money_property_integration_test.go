//go:build integration

package payment_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// propertyKeys makes every session's idempotency key unique across runs of the
// property, since the manual provider's ledger holds its keys globally.
var propertyKeys atomic.Int64

// modelSession and modelPayment are the model's copies of the records.
type (
	modelSession struct {
		id         string
		amount     int64
		held       int64
		outcome    string
		status     models.SessionStatus
		paymentIdx int
	}
	modelPayment struct {
		id       string
		amount   int64
		refunded int64
	}
)

// TestACollectionNeverMovesMoreThanItWasFor is ADR 0249 on the payment
// module's money: any sequence of sessions opened for any part of a
// collection, authorized in full, in part, declined or failing, captured in
// full or in part, refunded by payment or by collection, and canceled, leaves
// the collection's held, captured and refunded amounts and its status where a
// model of the calls says. Nothing is captured past what was held or refunded
// past what was captured, a collection never takes more than it was for, and
// each call is refused exactly when the model says, by the code the contract
// names.
func TestACollectionNeverMovesMoreThanItWasFor(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	rapid.Check(t, func(rt *rapid.T) {
		amount := rapid.Int64Range(1, 10_000).Draw(rt, "collection amount")
		collection, err := svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
			Reference: "property_" + fmt.Sprint(propertyKeys.Add(1)), Amount: amount, CurrencyCode: testCurrency,
		})
		require.NoError(rt, err)

		var sessions []*modelSession
		var payments []*modelPayment
		var held, captured, refunded int64
		live := func() int64 {
			var sum int64
			for _, s := range sessions {
				switch s.status {
				case models.SessionPending:
					sum += s.amount
				case models.SessionAuthorized:
					sum += s.held
				}
			}
			return sum
		}
		refused := func(rt *rapid.T, err error, code string) {
			require.Error(rt, err)
			require.True(rt, errors.HasKind(err, errors.KindConflict), "%v", err)
			require.Equal(rt, code, errors.CodeOf(err), "%v", err)
		}
		pickSession := func(rt *rapid.T) *modelSession {
			if len(sessions) == 0 {
				rt.Skip("no session yet")
			}
			return sessions[rapid.IntRange(0, len(sessions)-1).Draw(rt, "session")]
		}

		rt.Repeat(map[string]func(*rapid.T){
			"open a session": func(rt *rapid.T) {
				asked := rapid.Int64Range(0, amount+2).Draw(rt, "session amount")
				outcome := rapid.SampledFrom([]string{"authorize", "authorize in part", manual.OutcomeDecline, manual.OutcomeError}).Draw(rt, "outcome")
				remaining := amount - captured - live()
				opened := asked
				if asked == 0 {
					opened = remaining
				}
				data := map[string]any{manual.DataKeyOutcome: outcome}
				partial := int64(0)
				if outcome == "authorize in part" {
					data[manual.DataKeyOutcome] = manual.OutcomeAuthorize
					if opened > 0 {
						partial = rapid.Int64Range(1, opened).Draw(rt, "held in part")
						data[manual.DataKeyAuthorizedAmount] = partial
					}
				}
				ses, err := svc.CreateSession(ctx, collection.ID, manual.ID, service.CreateSessionInput{
					Amount: asked, IdempotencyKey: fmt.Sprintf("property-%d", propertyKeys.Add(1)), Data: data,
				})
				switch {
				case remaining <= 0:
					refused(rt, err, service.CodeCollectionClosed)
					return
				case asked > remaining:
					refused(rt, err, service.CodeInvalidTransition)
					return
				}
				require.NoError(rt, err)
				require.Equal(rt, opened, ses.Amount)
				s := &modelSession{id: ses.ID, amount: opened, outcome: outcome, status: models.SessionPending, paymentIdx: -1}
				switch outcome {
				case "authorize":
					s.held = opened
				case "authorize in part":
					s.held = partial
				}
				sessions = append(sessions, s)
			},
			"authorize": func(rt *rapid.T) {
				s := pickSession(rt)
				_, err := svc.AuthorizePayment(ctx, s.id)
				switch s.status {
				case models.SessionAuthorized:
					require.NoError(rt, err, "a second authorization is a no-op")
					return
				case models.SessionCaptured, models.SessionCanceled, models.SessionFailed:
					refused(rt, err, service.CodeInvalidTransition)
					return
				}
				switch s.outcome {
				case manual.OutcomeDecline:
					refused(rt, err, service.CodeAuthorizationDeclined)
					s.status = models.SessionFailed
				case manual.OutcomeError:
					require.Error(rt, err, "the provider could not be reached; the session stays pending")
				default:
					require.NoError(rt, err)
					s.status = models.SessionAuthorized
					held += s.held
				}
			},
			"capture": func(rt *rapid.T) {
				s := pickSession(rt)
				asked := rapid.Int64Range(0, s.held+2).Draw(rt, "captured")
				_, err := svc.CapturePayment(ctx, s.id, asked)
				switch s.status {
				case models.SessionPending, models.SessionCanceled, models.SessionFailed:
					refused(rt, err, service.CodeInvalidTransition)
					return
				case models.SessionCaptured:
					if asked == 0 || asked == payments[s.paymentIdx].amount {
						require.NoError(rt, err, "a repeated capture is a no-op")
					} else {
						refused(rt, err, service.CodeInvalidTransition)
					}
					return
				}
				if asked > s.held {
					refused(rt, err, service.CodeInvalidTransition)
					return
				}
				require.NoError(rt, err)
				take := asked
				if take == 0 {
					take = s.held
				}
				held -= s.held
				captured += take
				s.held = take
				s.status = models.SessionCaptured
				payments = append(payments, &modelPayment{amount: take})
				s.paymentIdx = len(payments) - 1
			},
			"refund a payment": func(rt *rapid.T) {
				if len(payments) == 0 {
					rt.Skip("nothing captured yet")
				}
				i := rapid.IntRange(0, len(payments)-1).Draw(rt, "payment")
				stored, err := svc.ListPayments(ctx, collection.ID)
				require.NoError(rt, err)
				p := payments[i]
				p.id = paymentIDFor(stored, sessions, i)
				refundable := p.amount - p.refunded
				asked := rapid.Int64Range(0, refundable+2).Draw(rt, "refunded")
				_, err = svc.RefundPayment(ctx, p.id, asked, "property")
				switch {
				case refundable == 0:
					refused(rt, err, service.CodeNothingToRefund)
					return
				case asked > refundable:
					refused(rt, err, service.CodeInvalidTransition)
					return
				}
				require.NoError(rt, err)
				if asked == 0 {
					asked = refundable
				}
				p.refunded += asked
				refunded += asked
			},
			"refund the collection": func(rt *rapid.T) {
				ceiling := captured - refunded
				asked := rapid.Int64Range(0, ceiling+2).Draw(rt, "refunded from the collection")
				_, err := svc.RefundCollection(ctx, collection.ID, asked, "property", "")
				if ceiling <= 0 || asked > ceiling {
					refused(rt, err, service.CodeCollectionNothingToRefund)
					return
				}
				require.NoError(rt, err)
				if asked == 0 {
					asked = ceiling
				}
				refunded += asked
				// The plan takes the newest payment first; the model follows it.
				left := asked
				for i := len(payments) - 1; i >= 0 && left > 0; i-- {
					part := min(left, payments[i].amount-payments[i].refunded)
					payments[i].refunded += part
					left -= part
				}
			},
			"cancel": func(rt *rapid.T) {
				s := pickSession(rt)
				err := svc.CancelPayment(ctx, s.id)
				switch s.status {
				case models.SessionCaptured:
					refused(rt, err, service.CodeInvalidTransition)
					return
				case models.SessionCanceled:
					require.NoError(rt, err, "a second cancellation is a no-op")
					return
				}
				require.NoError(rt, err)
				if s.status == models.SessionAuthorized {
					held -= s.held
				}
				s.status = models.SessionCanceled
				s.held = 0
			},
			"": func(rt *rapid.T) {
				stored, err := svc.GetPaymentCollection(ctx, collection.ID)
				require.NoError(rt, err)
				require.Equal(rt, held, stored.AuthorizedAmount, "held")
				require.Equal(rt, captured, stored.CapturedAmount, "captured")
				require.Equal(rt, refunded, stored.RefundedAmount, "refunded")
				require.LessOrEqual(rt, captured+live(), amount, "a collection never takes more than it was for")
				require.LessOrEqual(rt, refunded, captured)

				var counts models.SessionCounts
				for _, s := range sessions {
					switch s.status {
					case models.SessionPending, models.SessionAuthorized:
						counts.Live++
					case models.SessionCanceled:
						counts.Canceled++
					case models.SessionFailed:
						counts.Failed++
					}
				}
				fromModel := stored
				fromModel.AuthorizedAmount, fromModel.CapturedAmount, fromModel.RefundedAmount = held, captured, refunded
				require.Equal(rt, models.CollectionStatusFor(fromModel, counts), stored.Status, "the status is the amounts'")

				rows, err := svc.ListPayments(ctx, collection.ID)
				require.NoError(rt, err)
				require.Len(rt, rows, len(payments))
				for i, s := range capturedSessions(sessions) {
					row := paymentRowFor(rt, rows, s.id)
					require.Equal(rt, payments[s.paymentIdx].amount, row.Amount, "payment %d", i)
					require.Equal(rt, payments[s.paymentIdx].refunded, row.RefundedAmount, "payment %d", i)
				}
			},
		})
	})
}

// capturedSessions are the model's sessions that produced a payment.
func capturedSessions(sessions []*modelSession) []*modelSession {
	var out []*modelSession
	for _, s := range sessions {
		if s.paymentIdx >= 0 {
			out = append(out, s)
		}
	}
	return out
}

// paymentRowFor finds the stored payment a session produced.
func paymentRowFor(rt *rapid.T, rows []models.Payment, sessionID string) models.Payment {
	for i := range rows {
		if rows[i].PaymentSessionID == sessionID {
			return rows[i]
		}
	}
	rt.Fatalf("no stored payment for session %s", sessionID)
	return models.Payment{}
}

// paymentIDFor is the stored id of the model's i-th payment.
func paymentIDFor(rows []models.Payment, sessions []*modelSession, i int) string {
	for _, s := range sessions {
		if s.paymentIdx == i {
			for j := range rows {
				if rows[j].PaymentSessionID == s.id {
					return rows[j].ID
				}
			}
		}
	}
	return ""
}
