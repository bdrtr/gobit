package checkout

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
)

// testExternalID is the provider's own id of the opened session.
const testExternalID = "PAYTROID1"

// reportedHarness is a harness whose provider's customer pays on its page, with
// one opened session the provider reports by testExternalID.
func reportedHarness(t *testing.T) *harness {
	t.Helper()

	h := newHarness(t)
	h.payments.customerAuthorizes = map[string]bool{testProviderID: true}
	facts := openedSession()
	facts.IdempotencyKey = paymentSessionKeyPrefix + testOpenedCollectionID
	h.payments.sessions = map[string]paymentSessionFacts{testOpenedSessionID: facts}
	h.payments.reported = map[string]string{testOpenedSessionID: SessionCaptured}
	h.payments.byExternal = map[string][]string{testExternalID: {testOpenedSessionID}}
	// The completion a report makes takes no location, so the reservation
	// asks where the stock is, and the test location holds it.
	h.inventory.locationsFn = func(context.Context, string, int64) ([]string, error) {
		return []string{testLocationID}, nil
	}
	h.fulfillment.rankFn = rankByGreatestID

	return h
}

// withSessionStatus rescripts the opened session's status.
func withSessionStatus(h *harness, status string) {
	facts := h.payments.sessions[testOpenedSessionID]
	facts.Status = status
	h.payments.sessions[testOpenedSessionID] = facts
}

// TestAReportCompletesTheCart: the provider's report places the order with the
// opened session, on the cart's own channel and email, and settles completed.
func TestAReportCompletesTheCart(t *testing.T) {
	const (
		email   = "shopper@example.com"
		channel = "sc_web"
	)
	h := reportedHarness(t)
	h.carts.snapshotFn = func(ctx context.Context, cartID string) (json.RawMessage, error) {
		raw, err := defaultSnapshot(ctx, cartID)
		if err != nil {
			return nil, err
		}
		var snap map[string]any
		_ = json.Unmarshal(raw, &snap)
		snap["email"], snap["sales_channel_id"] = email, channel

		return json.Marshal(snap)
	}
	var narrowedBy [][]string
	h.links.listManyByToFn = func(_ context.Context, name string, toIDs []string) (map[string][]string, error) {
		if name != LinkLocationSalesChannel {
			return map[string][]string{}, nil
		}
		narrowedBy = append(narrowedBy, toIDs)

		return map[string][]string{channel: {testLocationID}}, nil
	}
	var placed map[string]any
	h.orders.placeFn = func(_ context.Context, snapshot json.RawMessage) (string, error) {
		require.NoError(t, json.Unmarshal(snapshot, &placed))

		return testOrderID, nil
	}

	require.NoError(t, h.wf.PaymentReported(context.Background(), testProviderID, testExternalID))

	assert.Equal(t, ReportCompleted, h.payments.report(testOpenedSessionID).outcome)
	assert.Equal(t, 1, h.rec.count("payment:authorize"))
	assert.Equal(t, 1, h.rec.count("payment:capture"))
	assert.Zero(t, h.rec.count("payment:session"), "the opened session is the one taken")
	assert.Equal(t, testCartID, placed["cart_id"])
	assert.Equal(t, email, placed["email"], "the order is placed with the cart's own address")
	require.NotEmpty(t, narrowedBy, "the warehouses are narrowed by the cart's channel")
	assert.Equal(t, []string{channel}, narrowedBy[0])
}

// TestAReportSettlesBySessionState: a session already captured is a completion
// that ran; one canceled is refunded when the provider says the money went
// back and closed, with the money held, when it says otherwise; one declined
// was closed by the provider; and one the checkout did not open is a
// person's.
func TestAReportSettlesBySessionState(t *testing.T) {
	for name, tc := range map[string]struct {
		change  func(h *harness)
		outcome string
	}{
		"captured": {func(h *harness) { withSessionStatus(h, SessionCaptured) }, ReportCompleted},
		"canceled and given back": {func(h *harness) {
			withSessionStatus(h, SessionCanceled)
			h.payments.reported[testOpenedSessionID] = SessionCanceled
		}, ReportRefunded},
		"canceled with the money held": {func(h *harness) { withSessionStatus(h, SessionCanceled) }, ReportClosed},
		"declined":                     {func(h *harness) { withSessionStatus(h, SessionFailed) }, ReportClosed},
		"not the checkout's": {func(h *harness) {
			facts := h.payments.sessions[testOpenedSessionID]
			facts.IdempotencyKey = "admin-key"
			h.payments.sessions[testOpenedSessionID] = facts
		}, ReportLeft},
		"opened under the opening's prefix for another collection": {func(h *harness) {
			facts := h.payments.sessions[testOpenedSessionID]
			facts.IdempotencyKey = paymentSessionKeyPrefix + "paycol_other"
			h.payments.sessions[testOpenedSessionID] = facts
		}, ReportLeft},
	} {
		t.Run(name, func(t *testing.T) {
			h := reportedHarness(t)
			tc.change(h)

			require.NoError(t, h.wf.PaymentReported(context.Background(), testProviderID, testExternalID))

			assert.Equal(t, tc.outcome, h.payments.report(testOpenedSessionID).outcome)
			assert.False(t, sagaRan(h.rec.snapshot()), "the saga ran: %v", h.rec.snapshot())
			assert.Zero(t, h.rec.count("totals:calculate"), "the state settles the report before any cart is read")
			assert.Zero(t, h.rec.count("payment:cancel"), "nothing is given back")
		})
	}
}

// TestAReportClosedWhileItRanSettlesByState: a session canceled, or reported
// declined, after the report was taken is refused by the completion for its
// state; the report settles by that state — refunded for the cancel, closed
// with the refusal kept for the decline — and this path gives nothing back.
func TestAReportClosedWhileItRanSettlesByState(t *testing.T) {
	for name, tc := range map[string]struct {
		change  func(h *harness)
		outcome string
	}{
		"canceled meanwhile": {func(h *harness) {
			h.carts.snapshotFn = func(ctx context.Context, cartID string) (json.RawMessage, error) {
				withSessionStatus(h, SessionCanceled)
				h.payments.reported[testOpenedSessionID] = SessionCanceled

				return defaultSnapshot(ctx, cartID)
			}
		}, ReportRefunded},
		"declined meanwhile": {func(h *harness) {
			h.payments.reported[testOpenedSessionID] = SessionFailed
		}, ReportClosed},
	} {
		t.Run(name, func(t *testing.T) {
			h := reportedHarness(t)
			tc.change(h)

			require.NoError(t, h.wf.PaymentReported(context.Background(), testProviderID, testExternalID))

			report := h.payments.report(testOpenedSessionID)
			assert.Equal(t, tc.outcome, report.outcome)
			assert.NotEmpty(t, report.note, "the report says why it settled so")
			assert.Zero(t, report.attempts, "a closed session is not tried again")
			assert.False(t, sagaRan(h.rec.snapshot()), "the saga ran: %v", h.rec.snapshot())
			assert.Zero(t, h.rec.count("payment:cancel"))
		})
	}
}

// TestAReportThatCanNeverPlaceItsCartIsRefunded: a cart that is gone, was
// completed with another payment, or no longer totals what was paid cannot be
// placed with this payment, so it goes back and the report says refunded.
func TestAReportThatCanNeverPlaceItsCartIsRefunded(t *testing.T) {
	for name, change := range map[string]func(h *harness){
		"the cart is gone": func(h *harness) {
			h.carts.snapshotFn = func(context.Context, string) (json.RawMessage, error) {
				return nil, coreerrors.NotFound("cart_not_found", "no cart")
			}
		},
		"the cart was completed otherwise": func(h *harness) {
			h.carts.snapshotFn = func(ctx context.Context, cartID string) (json.RawMessage, error) {
				raw, err := defaultSnapshot(ctx, cartID)
				if err != nil {
					return nil, err
				}
				var snap map[string]any
				_ = json.Unmarshal(raw, &snap)
				snap["completed"] = true

				return json.Marshal(snap)
			}
		},
		"the cart was completed while the report ran": func(h *harness) {
			reads := 0
			h.carts.snapshotFn = func(ctx context.Context, cartID string) (json.RawMessage, error) {
				raw, err := defaultSnapshot(ctx, cartID)
				if err != nil {
					return nil, err
				}
				reads++
				if reads == 1 {
					return raw, nil
				}
				var snap map[string]any
				_ = json.Unmarshal(raw, &snap)
				snap["completed"] = true

				return json.Marshal(snap)
			}
		},
		"the cart totals something else": func(h *harness) {
			facts := h.payments.sessions[testOpenedSessionID]
			facts.Amount, facts.Collection.Amount = testAmount-100, testAmount-100
			h.payments.sessions[testOpenedSessionID] = facts
		},
		"the cart was emptied": func(h *harness) {
			h.carts.snapshotFn = func(ctx context.Context, cartID string) (json.RawMessage, error) {
				raw, err := defaultSnapshot(ctx, cartID)
				if err != nil {
					return nil, err
				}
				var snap map[string]any
				_ = json.Unmarshal(raw, &snap)
				snap["items"] = []any{}

				return json.Marshal(snap)
			}
		},
		"the payment is in another currency": func(h *harness) {
			facts := h.payments.sessions[testOpenedSessionID]
			facts.CurrencyCode = "EUR"
			h.payments.sessions[testOpenedSessionID] = facts
		},
		"the payment was opened for another customer": func(h *harness) {
			facts := h.payments.sessions[testOpenedSessionID]
			facts.Collection.CustomerID = "cus_other"
			h.payments.sessions[testOpenedSessionID] = facts
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := reportedHarness(t)
			change(h)
			var canceled []string
			h.payments.cancelFn = func(_ context.Context, sessionID string) error {
				canceled = append(canceled, sessionID)

				return nil
			}

			require.NoError(t, h.wf.PaymentReported(context.Background(), testProviderID, testExternalID))

			assert.Equal(t, []string{testOpenedSessionID}, canceled)
			assert.Equal(t, ReportRefunded, h.payments.report(testOpenedSessionID).outcome)
			assert.NotEmpty(t, h.payments.report(testOpenedSessionID).note, "the reason is kept")
			assert.Zero(t, h.rec.count("order:place"))
		})
	}
}

// TestAReportIsNotRefundedWhenAnotherCompletionTookIt: the cart turned
// completed because the storefront's completion of the same session ran; the
// session is captured, so the report settles completed and nothing goes back.
func TestAReportIsNotRefundedWhenAnotherCompletionTookIt(t *testing.T) {
	h := reportedHarness(t)
	reads := 0
	h.carts.snapshotFn = func(ctx context.Context, cartID string) (json.RawMessage, error) {
		raw, err := defaultSnapshot(ctx, cartID)
		if err != nil {
			return nil, err
		}
		var snap map[string]any
		_ = json.Unmarshal(raw, &snap)
		snap["completed"] = true
		reads++
		// The storefront's completion captured the session meanwhile.
		withSessionStatus(h, SessionCaptured)

		return json.Marshal(snap)
	}

	require.NoError(t, h.wf.PaymentReported(context.Background(), testProviderID, testExternalID))

	assert.Equal(t, 1, reads)
	assert.Equal(t, ReportCompleted, h.payments.report(testOpenedSessionID).outcome)
	assert.Zero(t, h.rec.count("payment:cancel"))
}

// TestAFailedCompletionIsTriedAgainThenGivenUp: a completion that fails is
// counted and tried again when its next try is due, and the last attempt
// gives up, leaving the payment held for reconciliation rather than refunded.
func TestAFailedCompletionIsTriedAgainThenGivenUp(t *testing.T) {
	h := reportedHarness(t)
	h.inventory.reserveFn = func(context.Context, string, string, int64, string) (string, error) {
		return "", coreerrors.Conflict("inventory_insufficient", "no stock")
	}
	report := h.payments.report(testOpenedSessionID)

	for attempt := 1; attempt < MaxReportAttempts; attempt++ {
		pass, err := h.wf.CompleteReportedPayments(context.Background(), report.next)
		require.NoError(t, err)
		assert.Equal(t, ReportPass{Retrying: 1}, pass)
		assert.Equal(t, attempt, report.attempts)
		assert.Empty(t, report.outcome)
		assert.Contains(t, report.failure, "no stock", "the failure is kept for whoever reads the report")
	}

	pass, err := h.wf.CompleteReportedPayments(context.Background(), report.next)

	require.NoError(t, err)
	assert.Equal(t, ReportPass{GaveUp: 1}, pass)
	assert.Equal(t, ReportGaveUp, report.outcome)
	assert.Zero(t, h.rec.count("payment:cancel"), "a payment given up is held, not refunded")
}

// TestTheWaitsDouble: the n-th failed attempt waits the retry base times two
// to the n-1.
func TestTheWaitsDouble(t *testing.T) {
	h := reportedHarness(t)
	h.inventory.reserveFn = func(context.Context, string, string, int64, string) (string, error) {
		return "", coreerrors.Conflict("inventory_insufficient", "no stock")
	}
	report := h.payments.report(testOpenedSessionID)
	at := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

	var waits []time.Duration
	for range MaxReportAttempts - 1 {
		_, err := h.wf.CompleteReportedPayments(context.Background(), at)
		require.NoError(t, err)
		waits = append(waits, report.next.Sub(at))
		at = report.next
	}

	assert.Equal(t, []time.Duration{
		ReportRetryBase, 2 * ReportRetryBase, 4 * ReportRetryBase, 8 * ReportRetryBase, 16 * ReportRetryBase,
	}, waits)
}

// TestASweepAttemptsOnlyWhatIsDue: a report whose next try is later waits, and
// one the sweep just found is attempted at once.
func TestASweepAttemptsOnlyWhatIsDue(t *testing.T) {
	h := reportedHarness(t)
	at := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	h.payments.report(testOpenedSessionID).next = at.Add(time.Minute)

	pass, err := h.wf.CompleteReportedPayments(context.Background(), at)

	require.NoError(t, err)
	assert.Equal(t, ReportPass{}, pass)
	assert.False(t, sagaRan(h.rec.snapshot()))

	h.payments.discovered = []string{testOpenedSessionID}
	pass, err = h.wf.CompleteReportedPayments(context.Background(), at)

	require.NoError(t, err)
	assert.Equal(t, ReportPass{Discovered: 1, Completed: 1}, pass, "a report just found is attempted now")
}

// TestACompletionOfATakenSessionAnswersWithItsOrder: the storefront completing
// after the report placed the order is told that order, and no saga runs.
func TestACompletionOfATakenSessionAnswersWithItsOrder(t *testing.T) {
	h := newHarness(t)
	facts := openedSession()
	facts.Status = SessionCaptured
	facts.Collection.CapturedAmount = testAmount
	in := withOpenedSession(h, facts)
	h.links.listManyByToFn = func(_ context.Context, name string, toIDs []string) (map[string][]string, error) {
		require.Equal(t, LinkOrderPayment, name)
		require.Equal(t, []string{testOpenedCollectionID}, toIDs)

		return map[string][]string{testOpenedCollectionID: {"order_placed"}}, nil
	}

	out, err := h.wf.CompleteCart(context.Background(), in)

	require.NoError(t, err)
	assert.Equal(t, "order_placed", out.OrderID)
	assert.Equal(t, testAmount, out.Amount)
	assert.Zero(t, out.Outstanding)
	assert.Equal(t, testOpenedSessionID, out.PaymentSessionID)
	assert.False(t, sagaRan(h.rec.snapshot()), "the saga ran: %v", h.rec.snapshot())
	assert.Zero(t, h.rec.count("totals:calculate"), "nothing is prepared for an order already placed")
}

// TestATakenSessionAnswersOnlyItsOwnCompletion: a captured session answers
// with its order only the completion of its own cart at its own provider, and
// only while one order holds its collection; anything else goes to the
// preparation, which refuses it, and the answer's outstanding is what the
// collection did not capture.
func TestATakenSessionAnswersOnlyItsOwnCompletion(t *testing.T) {
	taken := func(h *harness, bound ...string) CompleteCartInput {
		facts := openedSession()
		facts.Status = SessionCaptured
		facts.Collection.CapturedAmount = testAmount
		in := withOpenedSession(h, facts)
		h.links.listManyByToFn = func(context.Context, string, []string) (map[string][]string, error) {
			return map[string][]string{testOpenedCollectionID: bound}, nil
		}

		return in
	}

	for name, tc := range map[string]struct {
		change func(in *CompleteCartInput)
		bound  []string
		code   string
	}{
		"another cart":     {func(in *CompleteCartInput) { in.CartID = "cart_other" }, []string{"order_placed"}, CodePaymentSessionForeign},
		"another provider": {func(in *CompleteCartInput) { in.PaymentProviderID = "card" }, []string{"order_placed"}, CodePaymentSessionForeign},
		"two orders":       {func(*CompleteCartInput) {}, []string{"order_a", "order_b"}, CodePaymentSessionClosed},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			in := taken(h, tc.bound...)
			tc.change(&in)

			_, err := h.wf.CompleteCart(context.Background(), in)

			require.Error(t, err)
			assert.True(t, hasCode(err, tc.code), "%v", err)
		})
	}

	t.Run("captured short", func(t *testing.T) {
		h := newHarness(t)
		in := taken(h, "order_placed")
		facts := h.payments.sessions[testOpenedSessionID]
		facts.Collection.CapturedAmount = testAmount - 100
		h.payments.sessions[testOpenedSessionID] = facts

		out, err := h.wf.CompleteCart(context.Background(), in)

		require.NoError(t, err)
		assert.Equal(t, int64(100), out.Outstanding)
	})
}

// TestACapturedSessionWithNoOrderIsLeftToThePreparation: a session captured
// with no order bound to its collection is not an order to answer with.
func TestACapturedSessionWithNoOrderIsLeftToThePreparation(t *testing.T) {
	h := newHarness(t)
	facts := openedSession()
	facts.Status = SessionCaptured
	in := withOpenedSession(h, facts)
	h.links.listManyByToFn = func(context.Context, string, []string) (map[string][]string, error) {
		return map[string][]string{}, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), in)

	require.Error(t, err)
	assert.True(t, hasCode(err, CodePaymentSessionClosed), "%v", err)
}

// TestACompletionOfASessionAlreadyRunningSaysNotYet: the same cart's completion
// running elsewhere is answered as the provider's silence is, so the storefront
// sends it again rather than reading a conflict it cannot act on.
func TestACompletionOfASessionAlreadyRunningSaysNotYet(t *testing.T) {
	h := newHarness(t)
	in := withOpenedSession(h, openedSession())
	entered, release := make(chan struct{}), make(chan struct{})
	h.inventory.reserveFn = func(_ context.Context, _, _ string, _ int64, lineItemID string) (string, error) {
		if lineItemID == testLineA {
			close(entered)
			<-release
		}

		return "res_" + lineItemID, nil
	}

	first := make(chan error, 1)
	go func() {
		_, err := h.wf.CompleteCart(context.Background(), in)
		first <- err
	}()
	<-entered

	_, err := h.wf.CompleteCart(context.Background(), in)

	close(release)
	require.Error(t, err)
	assert.True(t, hasCode(err, CodePaymentPending), "%v", err)
	assert.Equal(t, coreerrors.KindConflict, coreerrors.KindOf(err))
	require.NoError(t, <-first, "the first completion places the order")
}

// TestAReportNamingNoOpenSessionDoesNothing: an id that names no open session
// is a report taken with nothing to do.
func TestAReportNamingNoOpenSessionDoesNothing(t *testing.T) {
	h := reportedHarness(t)

	require.NoError(t, h.wf.PaymentReported(context.Background(), testProviderID, "UNKNOWN"))

	assert.False(t, sagaRan(h.rec.snapshot()))
	assert.Empty(t, h.payments.report(testOpenedSessionID).outcome)
}

// TestAReportThatCouldNotBeRecordedIsAnError: the provider is told the report
// was not taken, so it can send it again, and nothing is attempted.
func TestAReportThatCouldNotBeRecordedIsAnError(t *testing.T) {
	h := reportedHarness(t)
	h.payments.reportErr = errors.New("the report table is unreachable")

	err := h.wf.PaymentReported(context.Background(), testProviderID, testExternalID)

	require.Error(t, err)
	assert.False(t, sagaRan(h.rec.snapshot()))
}

// blockFirstCompletion starts a completion of the opened session that stops
// inside its first reservation, and returns a release that lets it finish and
// reports its error.
func blockFirstCompletion(t *testing.T, h *harness, in CompleteCartInput) (release func() error) {
	t.Helper()

	entered, unblock := make(chan struct{}), make(chan struct{})
	var once bool
	h.inventory.reserveFn = func(_ context.Context, _, _ string, _ int64, lineItemID string) (string, error) {
		if lineItemID == testLineA && !once {
			once = true
			close(entered)
			<-unblock
		}

		return "res_" + lineItemID, nil
	}
	done := make(chan error, 1)
	go func() {
		_, err := h.wf.CompleteCart(context.Background(), in)
		done <- err
	}()
	<-entered

	return func() error {
		close(unblock)

		return <-done
	}
}

// TestARefusalBesideARunningCompletionIsNotYet: while one completion of the
// cart runs, a second one refused for what the first has done — its totals no
// longer match — is told "not yet", a report meeting the same refusal is tried
// again rather than refunded, and the storefront's cancel of the payment is
// refused; nothing gives the first completion's money back (ADR 0438).
func TestARefusalBesideARunningCompletionIsNotYet(t *testing.T) {
	h := reportedHarness(t)
	in := h.input()
	in.LocationID = ""
	in.PaymentSessionID = testOpenedSessionID
	release := blockFirstCompletion(t, h, in)
	// The first completion spent the promotion's last use: the cart totals
	// more now.
	h.totals.calculateFn = func(ctx context.Context, cartID string) (cartwf.Totals, error) {
		totals, err := defaultTotals(ctx, cartID)
		totals.ShippingTotal += 100
		totals.Total += 100

		return totals, err
	}

	_, err := h.wf.CompleteCart(context.Background(), in)
	require.Error(t, err)
	assert.True(t, hasCode(err, CodePaymentPending), "%v", err)

	require.NoError(t, h.wf.PaymentReported(context.Background(), testProviderID, testExternalID))
	report := h.payments.report(testOpenedSessionID)
	assert.Empty(t, report.outcome, "the report waits")
	assert.Equal(t, 1, report.attempts)

	err = h.wf.CancelPayment(context.Background(), testCartID, testOpenedSessionID)
	require.Error(t, err)
	assert.True(t, hasCode(err, CodePaymentPending), "%v", err)

	assert.Zero(t, h.rec.count("payment:cancel"), "nothing was given back beside the running completion")
	require.NoError(t, release(), "the first completion places the order")
}

// TestAHeldSessionIsNotRefunded: a session a completion authorized and has
// not captured is that completion's, so a report meeting a cart completed
// otherwise tries again rather than refunding it.
func TestAHeldSessionIsNotRefunded(t *testing.T) {
	h := reportedHarness(t)
	withSessionStatus(h, SessionAuthorized)
	h.carts.snapshotFn = func(ctx context.Context, cartID string) (json.RawMessage, error) {
		raw, err := defaultSnapshot(ctx, cartID)
		if err != nil {
			return nil, err
		}
		var snap map[string]any
		_ = json.Unmarshal(raw, &snap)
		snap["completed"] = true

		return json.Marshal(snap)
	}

	require.NoError(t, h.wf.PaymentReported(context.Background(), testProviderID, testExternalID))

	report := h.payments.report(testOpenedSessionID)
	assert.Empty(t, report.outcome)
	assert.Equal(t, 1, report.attempts)
	assert.Zero(t, h.rec.count("payment:cancel"))
}

// TestASecondPaidSessionIsNotHandedTheFirstOrder: the cart was completed with
// one paid session and its stamp has not landed; the engine would answer the
// cart's key with that execution's output, so a second paid session is told
// the cart was completed with another payment, and its report gives it back.
func TestASecondPaidSessionIsNotHandedTheFirstOrder(t *testing.T) {
	const (
		secondSessionID  = "pses_second"
		secondExternalID = "PAYTROID2"
	)
	h := reportedHarness(t)
	second := openedSession()
	second.ID = secondSessionID
	second.Collection.ID = "paycol_second"
	second.IdempotencyKey = paymentSessionKeyPrefix + second.Collection.ID
	h.payments.sessions[secondSessionID] = second
	h.payments.reported[secondSessionID] = SessionCaptured
	h.payments.byExternal[secondExternalID] = []string{secondSessionID}
	var canceled []string
	h.payments.cancelFn = func(_ context.Context, sessionID string) error {
		canceled = append(canceled, sessionID)

		return nil
	}

	require.NoError(t, h.wf.PaymentReported(context.Background(), testProviderID, testExternalID))
	require.Equal(t, ReportCompleted, h.payments.report(testOpenedSessionID).outcome)

	in := h.input()
	in.LocationID = ""
	in.PaymentSessionID = secondSessionID
	_, err := h.wf.CompleteCart(context.Background(), in)

	require.Error(t, err)
	assert.True(t, hasCode(err, CodeCartCompleted), "%v", err)

	require.NoError(t, h.wf.PaymentReported(context.Background(), testProviderID, secondExternalID))

	assert.Equal(t, ReportRefunded, h.payments.report(secondSessionID).outcome)
	assert.Equal(t, []string{secondSessionID}, canceled, "the second payment goes back, the first stays")
}

// TestAPaymentForAClosedSessionIsTakenAndLeftToAPerson: the customer paid on
// a page whose session was canceled; the report is taken, nothing is
// completed, and the provider is not asked to send it again.
func TestAPaymentForAClosedSessionIsTakenAndLeftToAPerson(t *testing.T) {
	h := reportedHarness(t)
	h.payments.reportErr = coreerrors.Conflict(CodeReportedSessionClosed, "only closed sessions")

	require.NoError(t, h.wf.PaymentReported(context.Background(), testProviderID, testExternalID))

	assert.False(t, sagaRan(h.rec.snapshot()))
	assert.Zero(t, h.rec.count("payment:cancel"))
}

// TestASweepThatCannotDiscoverStillAttemptsWhatIsDue: a provider that cannot
// be asked does not hold back the reports already waiting.
func TestASweepThatCannotDiscoverStillAttemptsWhatIsDue(t *testing.T) {
	h := reportedHarness(t)
	h.payments.discoverErr = coreerrors.Unavailable("provider_down", "the provider did not answer")
	_ = h.payments.report(testOpenedSessionID)

	pass, err := h.wf.CompleteReportedPayments(context.Background(), time.Now())

	require.Error(t, err)
	assert.Equal(t, ReportPass{Completed: 1}, pass)
	assert.Equal(t, ReportCompleted, h.payments.report(testOpenedSessionID).outcome)
}

// TestACompletionThatMeetsTheOrderItRacedAnswersWithIt: the session was not
// yet captured when the completion looked, and by its preparation the other
// completion had captured it and stamped the cart; the refusal is the other
// completion's work, so the answer is its order (ADR 0438).
func TestACompletionThatMeetsTheOrderItRacedAnswersWithIt(t *testing.T) {
	h := newHarness(t)
	in := withOpenedSession(h, openedSession())
	h.carts.snapshotFn = func(ctx context.Context, cartID string) (json.RawMessage, error) {
		raw, err := defaultSnapshot(ctx, cartID)
		if err != nil {
			return nil, err
		}
		facts := h.payments.sessions[testOpenedSessionID]
		facts.Status, facts.Collection.CapturedAmount = SessionCaptured, testAmount
		h.payments.sessions[testOpenedSessionID] = facts
		var snap map[string]any
		_ = json.Unmarshal(raw, &snap)
		snap["completed"] = true

		return json.Marshal(snap)
	}
	h.links.listManyByToFn = func(context.Context, string, []string) (map[string][]string, error) {
		return map[string][]string{testOpenedCollectionID: {"order_raced"}}, nil
	}

	out, err := h.wf.CompleteCart(context.Background(), in)

	require.NoError(t, err)
	assert.Equal(t, "order_raced", out.OrderID)
	assert.False(t, sagaRan(h.rec.snapshot()), "the saga ran: %v", h.rec.snapshot())
}

// TestAReportForAGoneCartWaitsForARunningCompletion: the cart reads gone while
// a completion of it runs, and the report tries again rather than giving back
// a payment that completion may be about to authorize.
func TestAReportForAGoneCartWaitsForARunningCompletion(t *testing.T) {
	h := reportedHarness(t)
	in := h.input()
	in.LocationID = ""
	in.PaymentSessionID = testOpenedSessionID
	release := blockFirstCompletion(t, h, in)
	h.carts.snapshotFn = func(context.Context, string) (json.RawMessage, error) {
		return nil, coreerrors.NotFound("cart_not_found", "no cart")
	}

	require.NoError(t, h.wf.PaymentReported(context.Background(), testProviderID, testExternalID))

	report := h.payments.report(testOpenedSessionID)
	assert.Empty(t, report.outcome)
	assert.Equal(t, 1, report.attempts)
	assert.Zero(t, h.rec.count("payment:cancel"))
	h.carts.snapshotFn = defaultSnapshot
	require.NoError(t, release())
}

// TestAnAbandonedCompletionIsNotRunning: a completion whose process stopped
// leaves its record running past its lease; nothing will complete that
// session, so a refusal beside it stands and the report gives the payment
// back rather than waiting for good.
func TestAnAbandonedCompletionIsNotRunning(t *testing.T) {
	h := reportedHarness(t)
	stale := time.Now().UTC().Add(-3 * time.Hour)
	require.NoError(t, h.executions.Create(context.Background(), &workflow.Execution{
		ID: "wfx_abandoned", Workflow: WorkflowName, IdempotencyKey: IdempotencyKeyPrefix + testCartID,
		Status: workflow.StatusRunning, CreatedAt: stale, UpdatedAt: stale,
	}))
	h.totals.calculateFn = func(ctx context.Context, cartID string) (cartwf.Totals, error) {
		totals, err := defaultTotals(ctx, cartID)
		totals.ShippingTotal += 100
		totals.Total += 100

		return totals, err
	}
	var canceled []string
	h.payments.cancelFn = func(_ context.Context, sessionID string) error {
		canceled = append(canceled, sessionID)

		return nil
	}

	in := h.input()
	in.LocationID = ""
	in.PaymentSessionID = testOpenedSessionID
	_, err := h.wf.CompleteCart(context.Background(), in)
	require.Error(t, err)
	assert.False(t, hasCode(err, CodePaymentPending), "an abandoned record is not a completion running: %v", err)

	require.NoError(t, h.wf.PaymentReported(context.Background(), testProviderID, testExternalID))

	report := h.payments.report(testOpenedSessionID)
	assert.Equal(t, ReportRefunded, report.outcome, "failure: %s", report.failure)
	assert.Equal(t, []string{testOpenedSessionID}, canceled)
}

// TestATakenSessionIsAnsweredOnceWithItsToken: the storefront's completion of
// a session the provider's report turned into an order is told the order and
// given its access token when it presents the session's completion secret,
// once; without the secret, with another one, or a second time, it is refused
// as answered, which the storefront answers as any completed cart (ADR 0439).
// The secret is spent before the token is issued.
func TestATakenSessionIsAnsweredOnceWithItsToken(t *testing.T) {
	const secret = "cpsec_opened"
	taken := func(h *harness) CompleteCartInput {
		facts := openedSession()
		facts.Status = SessionCaptured
		facts.Collection.CapturedAmount = testAmount
		in := withOpenedSession(h, facts)
		in.IssueOrderToken = true
		h.payments.answers = map[string]*stubAnswer{testOpenedSessionID: {secret: secret}}
		h.links.listManyByToFn = func(context.Context, string, []string) (map[string][]string, error) {
			return map[string][]string{testOpenedCollectionID: {"order_placed"}}, nil
		}

		return in
	}

	t.Run("with its secret", func(t *testing.T) {
		h := newHarness(t)
		in := taken(h)
		in.CompletionSecret = secret

		out, err := h.wf.CompleteCart(context.Background(), in)

		require.NoError(t, err)
		assert.Equal(t, "order_placed", out.OrderID)
		assert.Equal(t, "ordtok_order_placed", out.OrderToken)
		assert.Equal(t, []string{"order_placed"}, h.orders.tokens)

		_, err = h.wf.CompleteCart(context.Background(), in)

		require.Error(t, err)
		assert.True(t, hasCode(err, CodePaymentAnswered), "%v", err)
		assert.Len(t, h.orders.tokens, 1, "the second asking gets no token")
	})

	for name, presented := range map[string]string{"without a secret": "", "with another secret": "cpsec_guess"} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			in := taken(h)
			in.CompletionSecret = presented

			_, err := h.wf.CompleteCart(context.Background(), in)

			require.Error(t, err)
			assert.True(t, hasCode(err, CodePaymentAnswered), "%v", err)
			assert.Empty(t, h.orders.tokens)
			assert.False(t, h.payments.answers[testOpenedSessionID].answered, "the secret is still its holder's")
		})
	}

	t.Run("not the storefront's", func(t *testing.T) {
		h := newHarness(t)
		in := taken(h)
		in.IssueOrderToken = false

		out, err := h.wf.CompleteCart(context.Background(), in)

		require.NoError(t, err)
		assert.Equal(t, "order_placed", out.OrderID)
		assert.Empty(t, out.OrderToken)
		assert.Zero(t, h.rec.count("payment:claim_answer"), "an answer without a token spends nothing")
	})
}

// TestTheStorefrontsOwnCompletionGetsTheTokenForItsSecret: a completion the
// saga answers, paid with an opened session, is given the order's token for
// that session's completion secret, which it spends; without the secret the
// order is placed and answered without a token, and the secret stays its
// holder's (ADR 0439).
func TestTheStorefrontsOwnCompletionGetsTheTokenForItsSecret(t *testing.T) {
	for name, tc := range map[string]struct {
		presented string
		token     bool
	}{
		"with the secret":    {"cpsec_opened", true},
		"without the secret": {"", false},
		"with another":       {"cpsec_guess", false},
	} {
		t.Run(name, func(t *testing.T) {
			h := reportedHarness(t)
			h.payments.answers = map[string]*stubAnswer{testOpenedSessionID: {secret: "cpsec_opened"}}
			in := h.input()
			in.LocationID = ""
			in.PaymentSessionID = testOpenedSessionID
			in.CompletionSecret = tc.presented
			in.IssueOrderToken = true

			out, err := h.wf.CompleteCart(context.Background(), in)

			require.NoError(t, err, "the order is placed either way")
			assert.Equal(t, testOrderID, out.OrderID)
			assert.Equal(t, tc.token, out.OrderToken != "", "token %q", out.OrderToken)
			assert.Equal(t, tc.token, h.payments.answers[testOpenedSessionID].answered)
		})
	}
}

// TestAHeldSessionIsNotAnsweredWithItsOrder: an order binds the opened
// session's collection once the session is authorized, before the capture;
// while that completion runs, a second completion of the session is told
// "not yet" rather than the order, which the first may still give back.
func TestAHeldSessionIsNotAnsweredWithItsOrder(t *testing.T) {
	h := newHarness(t)
	facts := openedSession()
	facts.Status = SessionAuthorized
	in := withOpenedSession(h, facts)
	in.LocationID = ""
	h.links.listManyByToFn = func(context.Context, string, []string) (map[string][]string, error) {
		return map[string][]string{testOpenedCollectionID: {"order_held"}}, nil
	}
	h.inventory.locationsFn = func(context.Context, string, int64) ([]string, error) {
		return []string{testLocationID}, nil
	}
	h.fulfillment.rankFn = rankByGreatestID
	release := blockFirstCompletion(t, h, in)

	_, err := h.wf.CompleteCart(context.Background(), in)

	require.Error(t, err, "an authorized session is not an order to answer with")
	assert.True(t, hasCode(err, CodePaymentPending), "%v", err)
	_ = release()
}

// TestARunningCompletionWithoutASessionIsTheEnginesConflict: "not yet" is the
// answer of a completion naming an opened session; a completion without one
// meeting another running is the engine's own conflict, as before.
func TestARunningCompletionWithoutASessionIsTheEnginesConflict(t *testing.T) {
	h := newHarness(t)
	in := h.input()
	release := blockFirstCompletion(t, h, in)

	_, err := h.wf.CompleteCart(context.Background(), in)

	require.Error(t, err)
	assert.True(t, hasCode(err, workflow.CodeExecutionRunning), "%v", err)
	assert.False(t, hasCode(err, CodePaymentPending), "%v", err)
	require.NoError(t, release())
}
