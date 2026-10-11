package checkout

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testOpenedSessionID and testOpenedCollectionID are a payment a storefront
// opened before the completion (ADR 0438); they differ from the ids the saga's
// own collection and session get, so a test can tell which was used.
const (
	testOpenedSessionID    = "pses_opened"
	testOpenedCollectionID = "pcol_opened"
)

// openedSession is the payment module's answer about a session opened for the
// harness's cart, at its provider, for its total.
func openedSession() paymentSessionFacts {
	facts := paymentSessionFacts{
		ID: testOpenedSessionID, ProviderID: testProviderID, Status: SessionPending,
		Amount: testAmount, CurrencyCode: testCurrency,
		Data: json.RawMessage(`{"iframe_token":"tok_1"}`),
	}
	facts.Collection.ID = testOpenedCollectionID
	facts.Collection.Reference = testCartID
	facts.Collection.Amount = testAmount
	facts.Collection.CurrencyCode = testCurrency

	return facts
}

// withOpenedSession scripts the harness with the opened session and returns
// the completion's input naming it.
func withOpenedSession(h *harness, facts paymentSessionFacts) CompleteCartInput {
	h.payments.sessions = map[string]paymentSessionFacts{facts.ID: facts}
	in := h.input()
	in.PaymentSessionID = facts.ID

	return in
}

// sagaRan reports whether any of the saga's steps touched a module.
func sagaRan(calls []string) bool {
	for _, call := range calls {
		for _, prefix := range []string{"inventory:", "order:", "payment:collection", "payment:session",
			"payment:authorize", "payment:capture", "promotion:", "cart:complete"} {
			if len(call) >= len(prefix) && call[:len(prefix)] == prefix {
				return true
			}
		}
	}

	return false
}

// TestOpenPaymentOpensACollectionForTheTotalAndASession is the opening: the
// cart is prepared as the completion prepares it, a collection is opened for
// its total and a session at the provider, and nothing of the saga runs.
func TestOpenPaymentOpensACollectionForTheTotalAndASession(t *testing.T) {
	h := newHarness(t)
	var opened struct {
		reference, customer, currency string
		amount                        int64
		collection, provider, key     string
	}
	h.payments.createCollectionFn = func(_ context.Context, reference, customerID, currency string, amount int64) (string, error) {
		opened.reference, opened.customer, opened.currency, opened.amount = reference, customerID, currency, amount

		return testOpenedCollectionID, nil
	}
	h.payments.openSessionFn = func(_ context.Context, collectionID, providerID, key string, _ json.RawMessage) (string, error) {
		opened.collection, opened.provider, opened.key = collectionID, providerID, key

		return testOpenedSessionID, nil
	}
	h.payments.sessions = map[string]paymentSessionFacts{testOpenedSessionID: openedSession()}

	result, err := h.wf.OpenPayment(context.Background(), OpenPaymentInput{
		CartID: testCartID, PaymentProviderID: testProviderID,
		PaymentData: json.RawMessage(`{"email":"a@b.c"}`), ExpectedTotal: testAmount,
	})

	require.NoError(t, err)
	assert.Equal(t, OpenPaymentResult{
		PaymentSessionID: testOpenedSessionID, PaymentCollectionID: testOpenedCollectionID,
		ProviderID: testProviderID, Status: SessionPending, Amount: testAmount, CurrencyCode: testCurrency,
		Data:             json.RawMessage(`{"iframe_token":"tok_1"}`),
		CompletionSecret: "cpsec_" + testOpenedSessionID,
	}, result)
	assert.Equal(t, "cpsec_"+testOpenedSessionID, h.payments.answers[testOpenedSessionID].secret,
		"the payment module keeps the secret handed back")
	assert.Equal(t, testCartID, opened.reference, "the collection is the cart's")
	assert.Equal(t, testCustomerID, opened.customer)
	assert.Equal(t, testCurrency, opened.currency)
	assert.Equal(t, testAmount, opened.amount, "the collection is opened for the recomputed total")
	assert.Equal(t, testOpenedCollectionID, opened.collection)
	assert.Equal(t, testProviderID, opened.provider)
	assert.Equal(t, "cart_payment:"+testOpenedCollectionID, opened.key)
	assert.Equal(t, []string{`{"email":"a@b.c"}`}, h.payments.sessionData, "the data is the provider's, as sent")
	assert.Equal(t, 1, h.rec.count("totals:calculate"), "the totals are recomputed")
	for _, call := range []string{"inventory:reserve:" + testLineA, "order:place", "payment:authorize", "payment:capture"} {
		assert.Zero(t, h.rec.count(call), "%s is the completion's, not the opening's", call)
	}
}

// TestOpenPaymentRefusesWhatTheCompletionRefuses: a total the customer did not
// confirm opens no payment.
func TestOpenPaymentRefusesWhatTheCompletionRefuses(t *testing.T) {
	h := newHarness(t)

	_, err := h.wf.OpenPayment(context.Background(), OpenPaymentInput{
		CartID: testCartID, PaymentProviderID: testProviderID, ExpectedTotal: testAmount + 1,
	})

	require.Error(t, err)
	assert.True(t, hasCode(err, CodeTotalMismatch), "%v", err)
	assert.Zero(t, h.rec.count("payment:collection"))
	assert.Zero(t, h.rec.count("payment:session"))
}

// TestACompletionNamingAnOpenedSessionAuthorizesIt: no collection and no
// session are opened; the opened session is authorized, captured, and only
// then is the order bound to its collection.
func TestACompletionNamingAnOpenedSessionAuthorizesIt(t *testing.T) {
	h := newHarness(t)
	in := withOpenedSession(h, openedSession())
	var authorized, captured []string
	h.payments.authorizeFn = func(_ context.Context, sessionID string) (string, int64, error) {
		authorized = append(authorized, sessionID)

		return SessionAuthorized, testAmount, nil
	}
	h.payments.captureFn = func(_ context.Context, sessionID string, _ int64) (string, error) {
		captured = append(captured, sessionID)

		return testPaymentID, nil
	}

	out, err := h.wf.CompleteCart(context.Background(), in)

	require.NoError(t, err)
	assert.Equal(t, testOpenedSessionID, out.PaymentSessionID)
	assert.Equal(t, testOpenedCollectionID, out.PaymentCollectionID)
	assert.Equal(t, []string{testOpenedSessionID}, authorized)
	assert.Equal(t, []string{testOpenedSessionID}, captured)
	assert.Equal(t, []int64{testAmount}, h.payments.captureAmounts)
	assert.Zero(t, h.rec.count("payment:collection"), "the storefront's collection is used")
	assert.Zero(t, h.rec.count("payment:session"), "the storefront's session is used")
	require.Len(t, h.links.created, 1)
	assert.Equal(t, linkPair{name: LinkOrderPayment, fromID: testOrderID, toID: testOpenedCollectionID},
		h.links.created[0])

	calls := h.rec.snapshot()
	authAt := slices.Index(calls, "payment:authorize")
	linkAt := slices.Index(calls, "link:create:"+LinkOrderPayment)
	require.NotEqual(t, -1, authAt)
	require.NotEqual(t, -1, linkAt)
	assert.Less(t, authAt, linkAt,
		"an opened collection outlives an attempt, so it is bound only once its money is held")
}

// TestAFailedAuthorizationLeavesTheOpenedSessionOpen: at a provider whose
// customer pays on its page the session is the customer's page, so a
// completion that could not authorize it does not cancel it and does not bind
// its collection to the order it rolls back.
func TestAFailedAuthorizationLeavesTheOpenedSessionOpen(t *testing.T) {
	h := newHarness(t)
	h.payments.customerAuthorizes = map[string]bool{testProviderID: true}
	in := withOpenedSession(h, openedSession())
	h.payments.reported = map[string]string{testOpenedSessionID: SessionCaptured}
	h.payments.authorizeFn = func(context.Context, string) (string, int64, error) {
		return "", 0, errors.New("the payment table is unreachable")
	}

	_, err := h.wf.CompleteCart(context.Background(), in)

	require.Error(t, err)
	assert.Zero(t, h.rec.count("payment:cancel"), "the opened session is not canceled")
	assert.Empty(t, h.links.created, "a collection bound to a rolled-back order could never be bound again")
	assert.Equal(t, []string{testOrderID}, h.orders.canceled, "the order the attempt opened is rolled back")
	assert.Positive(t, h.rec.count("inventory:release:res_"+testLineA))
}

// TestADrivenProvidersOpenedSessionIsReleasedWhenItsAuthorizationFails: a
// provider the module drives may have placed a hold on the way to a failure,
// so its opened session is released, as the saga releases its own.
func TestADrivenProvidersOpenedSessionIsReleasedWhenItsAuthorizationFails(t *testing.T) {
	h := newHarness(t)
	in := withOpenedSession(h, openedSession())
	h.payments.authorizeFn = func(context.Context, string) (string, int64, error) {
		return "", 0, errors.New("the gateway timed out")
	}
	var canceled []string
	h.payments.cancelFn = func(_ context.Context, sessionID string) error {
		canceled = append(canceled, sessionID)

		return nil
	}

	_, err := h.wf.CompleteCart(context.Background(), in)

	require.Error(t, err)
	assert.Equal(t, []string{testOpenedSessionID}, canceled)
	assert.Empty(t, h.links.created)
}

// TestAnOpenedSessionWhoseOrderCannotBeBoundIsReleased: the money is held for
// an order that cannot be reached from it, so the hold is released.
func TestAnOpenedSessionWhoseOrderCannotBeBoundIsReleased(t *testing.T) {
	h := newHarness(t)
	in := withOpenedSession(h, openedSession())
	h.links.createFn = func(context.Context, string, string, string) error {
		return errors.New("the link table is unreachable")
	}
	var canceled []string
	h.payments.cancelFn = func(_ context.Context, sessionID string) error {
		canceled = append(canceled, sessionID)

		return nil
	}

	_, err := h.wf.CompleteCart(context.Background(), in)

	require.Error(t, err)
	assert.True(t, hasCode(err, CodeLinkFailed), "%v", err)
	assert.Equal(t, []string{testOpenedSessionID}, canceled)
	assert.Zero(t, h.rec.count("payment:capture"))
}

// TestAnOpenedSessionHeldShortIsReleased: a hold below the total can never
// complete the cart, so it is released rather than left on the customer.
func TestAnOpenedSessionHeldShortIsReleased(t *testing.T) {
	h := newHarness(t)
	in := withOpenedSession(h, openedSession())
	h.payments.authorizeFn = func(context.Context, string) (string, int64, error) {
		return SessionAuthorized, testAmount - 1, nil
	}
	var canceled []string
	h.payments.cancelFn = func(_ context.Context, sessionID string) error {
		canceled = append(canceled, sessionID)

		return nil
	}

	_, err := h.wf.CompleteCart(context.Background(), in)

	require.Error(t, err)
	assert.True(t, hasCode(err, CodePaymentUnderauthorized), "%v", err)
	assert.Equal(t, []string{testOpenedSessionID}, canceled)
	assert.Empty(t, h.links.created)
	assert.Zero(t, h.rec.count("payment:capture"))
}

// TestALaterStepsFailureCancelsTheOpenedSession: once the opened session holds
// the money for this order, the compensation cancels it as it cancels the
// saga's own; at PayTR that is the refund.
func TestALaterStepsFailureCancelsTheOpenedSession(t *testing.T) {
	h := newHarness(t)
	in := withOpenedSession(h, openedSession())
	h.payments.captureFn = func(context.Context, string, int64) (string, error) {
		return "", errors.New("the provider refused the capture")
	}
	h.payments.collectionFn = func(context.Context, string) (string, int64, int64, int64, int64, error) {
		return SessionAuthorized, testAmount, testAmount, 0, 0, nil
	}
	var canceled []string
	h.payments.cancelFn = func(_ context.Context, sessionID string) error {
		canceled = append(canceled, sessionID)

		return nil
	}

	_, err := h.wf.CompleteCart(context.Background(), in)

	require.Error(t, err)
	assert.Equal(t, []string{testOpenedSessionID}, canceled)
	assert.Equal(t, []string{testOrderID}, h.orders.canceled)
}

// TestACompletionIsHeldToItsOpenedSession: each refusal comes before the
// saga's first step, so nothing is reserved, opened or authorized.
func TestACompletionIsHeldToItsOpenedSession(t *testing.T) {
	for name, tc := range map[string]struct {
		change func(*paymentSessionFacts)
		code   string
	}{
		"another cart's session": {
			change: func(f *paymentSessionFacts) { f.Collection.Reference = "cart_other" }, code: CodePaymentSessionForeign,
		},
		"another provider's session": {
			change: func(f *paymentSessionFacts) { f.ProviderID = "paytr" }, code: CodePaymentSessionForeign,
		},
		"another customer's session": {
			change: func(f *paymentSessionFacts) { f.Collection.CustomerID = "cus_other" }, code: CodePaymentSessionForeign,
		},
		"a captured session": {
			change: func(f *paymentSessionFacts) { f.Status = SessionCaptured }, code: CodePaymentSessionClosed,
		},
		"a canceled session": {
			change: func(f *paymentSessionFacts) { f.Status = SessionCanceled }, code: CodePaymentSessionClosed,
		},
		"a declined session": {
			change: func(f *paymentSessionFacts) { f.Status = SessionFailed }, code: CodePaymentSessionClosed,
		},
		"a session on a collection that captured": {
			change: func(f *paymentSessionFacts) { f.Collection.CapturedAmount = 1 }, code: CodePaymentSessionClosed,
		},
		"a session for an old total": {
			change: func(f *paymentSessionFacts) { f.Amount = testAmount - 100 }, code: CodePaymentSessionStale,
		},
		"a collection for an old total": {
			change: func(f *paymentSessionFacts) { f.Collection.Amount = testAmount - 100 }, code: CodePaymentSessionStale,
		},
		"a session in another currency": {
			change: func(f *paymentSessionFacts) { f.CurrencyCode = "EUR" }, code: CodePaymentSessionStale,
		},
		"a collection in another currency": {
			change: func(f *paymentSessionFacts) { f.Collection.CurrencyCode = "EUR" }, code: CodePaymentSessionStale,
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			facts := openedSession()
			tc.change(&facts)
			in := withOpenedSession(h, facts)

			_, err := h.wf.CompleteCart(context.Background(), in)

			require.Error(t, err)
			assert.True(t, hasCode(err, tc.code), "%v", err)
			assert.False(t, sagaRan(h.rec.snapshot()), "the saga ran: %v", h.rec.snapshot())
		})
	}
}

// TestAGuestsOpenedSessionCompletesTheSignedInCart: a payment opened before the
// shopper signed in names nobody, and it is not another person's money.
func TestAGuestsOpenedSessionCompletesTheSignedInCart(t *testing.T) {
	h := newHarness(t)
	facts := openedSession()
	facts.Collection.CustomerID = ""
	in := withOpenedSession(h, facts)

	_, err := h.wf.CompleteCart(context.Background(), in)

	require.NoError(t, err)
}

// TestTheOpenedSessionsTenderIsNotCheckedAgain: the session was checked when it
// was opened, with the data it was opened with, which the completion does not
// resend; asking again without it would refuse a gift card opened first.
func TestTheOpenedSessionsTenderIsNotCheckedAgain(t *testing.T) {
	h := newHarness(t)
	in := withOpenedSession(h, openedSession())

	_, err := h.wf.CompleteCart(context.Background(), in)

	require.NoError(t, err)
	assert.Empty(t, h.payments.checkedTenders)
}

// TestAForeignSessionsRefusalNamesNoOtherCart: a cart id is the capability to
// read that cart, so the refusal names only the completion's own.
func TestAForeignSessionsRefusalNamesNoOtherCart(t *testing.T) {
	h := newHarness(t)
	facts := openedSession()
	facts.Collection.Reference = "cart_somebody_else"
	in := withOpenedSession(h, facts)

	_, err := h.wf.CompleteCart(context.Background(), in)

	require.Error(t, err)
	assert.True(t, hasCode(err, CodePaymentSessionForeign))
	assert.NotContains(t, err.Error(), "cart_somebody_else")
}

// TestAnUnknownOpenedSessionIsRefused: the payment module's NotFound comes back
// as it is, before the saga.
func TestAnUnknownOpenedSessionIsRefused(t *testing.T) {
	h := newHarness(t)
	in := h.input()
	in.PaymentSessionID = "pses_nobody"

	_, err := h.wf.CompleteCart(context.Background(), in)

	require.Error(t, err)
	assert.True(t, hasCode(err, "payment_session_not_found"), "%v", err)
	assert.False(t, sagaRan(h.rec.snapshot()))
}

// TestACustomerAuthorizingProviderNeedsAnOpenedSession: its session cannot be
// opened inside the completion, so a completion naming none is refused before
// anything is opened (ADR 0438).
func TestACustomerAuthorizingProviderNeedsAnOpenedSession(t *testing.T) {
	h := newHarness(t)
	h.payments.customerAuthorizes = map[string]bool{testProviderID: true}

	_, err := h.wf.CompleteCart(context.Background(), h.input())

	require.Error(t, err)
	assert.True(t, hasCode(err, CodePaymentSessionRequired), "%v", err)
	assert.False(t, sagaRan(h.rec.snapshot()), "the saga ran: %v", h.rec.snapshot())
}

// TestACustomerAuthorizingProviderWaitsForItsReport: while the provider has
// not reported the customer's payment the completion does nothing and may be
// sent again; a reported refusal is final; a reported payment proceeds.
func TestACustomerAuthorizingProviderWaitsForItsReport(t *testing.T) {
	for reported, code := range map[string]string{
		SessionPending:  CodePaymentPending,
		SessionFailed:   CodePaymentReportedDeclined,
		SessionCanceled: CodePaymentReportedDeclined,
	} {
		t.Run(reported, func(t *testing.T) {
			h := newHarness(t)
			h.payments.customerAuthorizes = map[string]bool{testProviderID: true}
			in := withOpenedSession(h, openedSession())
			h.payments.reported = map[string]string{testOpenedSessionID: reported}

			_, err := h.wf.CompleteCart(context.Background(), in)

			require.Error(t, err)
			assert.True(t, hasCode(err, code), "%v", err)
			assert.False(t, sagaRan(h.rec.snapshot()), "the saga ran: %v", h.rec.snapshot())
		})
	}

	t.Run("captured", func(t *testing.T) {
		h := newHarness(t)
		h.payments.customerAuthorizes = map[string]bool{testProviderID: true}
		in := withOpenedSession(h, openedSession())
		h.payments.reported = map[string]string{testOpenedSessionID: SessionCaptured}

		out, err := h.wf.CompleteCart(context.Background(), in)

		require.NoError(t, err)
		assert.Equal(t, testOpenedSessionID, out.PaymentSessionID)
	})

	t.Run("an authorized session is not asked about", func(t *testing.T) {
		h := newHarness(t)
		h.payments.customerAuthorizes = map[string]bool{testProviderID: true}
		facts := openedSession()
		facts.Status = SessionAuthorized
		in := withOpenedSession(h, facts)

		_, err := h.wf.CompleteCart(context.Background(), in)

		require.NoError(t, err)
		assert.Zero(t, h.rec.count("payment:reported"),
			"a session this module already holds authorized needs no word from the provider")
	})
}

// TestADrivenProviderIsNotAskedForItsReport: a provider the module drives is
// authorized by the completion, so its pending session is not a reason to wait.
func TestADrivenProviderIsNotAskedForItsReport(t *testing.T) {
	h := newHarness(t)
	in := withOpenedSession(h, openedSession())

	_, err := h.wf.CompleteCart(context.Background(), in)

	require.NoError(t, err)
	assert.Zero(t, h.rec.count("payment:reported"))
}

// TestAnOpenedSessionPaysTheWholeTotal: nothing can pay before a session opened
// for the whole total.
func TestAnOpenedSessionPaysTheWholeTotal(t *testing.T) {
	for name, change := range map[string]func(*CompleteCartInput){
		"a gift card":     func(in *CompleteCartInput) { in.GiftCardCode = "GIFT-1" },
		"a balance first": func(in *CompleteCartInput) { in.PayFirstWith = []string{StoreCreditProviderID} },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			in := withOpenedSession(h, openedSession())
			change(&in)

			_, err := h.wf.CompleteCart(context.Background(), in)

			require.Error(t, err)
			assert.True(t, hasCode(err, CodeInvalidInput), "%v", err)
			assert.Empty(t, h.rec.snapshot(), "nothing is read for an input that cannot be served")
		})
	}
}

// TestAShopsOwnBalanceIsNotOpenedFirst: a gift card, store credit and loyalty
// points pay at the completion, where a card that expired or was closed is
// refused before the order; an opened session is not asked again, so none of
// them is opened first or completed as one (ADR 0438).
func TestAShopsOwnBalanceIsNotOpenedFirst(t *testing.T) {
	for _, balance := range []string{GiftCardProviderID, StoreCreditProviderID, LoyaltyPointsProviderID} {
		t.Run(balance, func(t *testing.T) {
			h := newHarness(t)

			_, err := h.wf.OpenPayment(context.Background(), OpenPaymentInput{
				CartID: testCartID, PaymentProviderID: balance, ExpectedTotal: testAmount,
			})
			require.Error(t, err)
			assert.True(t, hasCode(err, CodeInvalidInput), "%v", err)
			assert.Zero(t, h.rec.count("payment:collection"))

			facts := openedSession()
			facts.ProviderID = balance
			in := withOpenedSession(h, facts)
			in.PaymentProviderID = balance
			_, err = h.wf.CompleteCart(context.Background(), in)
			require.Error(t, err)
			assert.True(t, hasCode(err, CodeInvalidInput), "%v", err)
			assert.False(t, sagaRan(h.rec.snapshot()))
		})
	}
}

// TestTheOpenedSessionIsRecordedAndRecovered: the plan records the session and
// its collection, so a saga rebuilt from the record cancels the right one.
func TestTheOpenedSessionIsRecordedAndRecovered(t *testing.T) {
	recorded, err := json.Marshal(checkoutPlan{
		CartID: testCartID, PaymentSessionID: testOpenedSessionID, PaymentCollectionID: testOpenedCollectionID,
	})
	require.NoError(t, err)

	var back checkoutPlan
	require.NoError(t, json.Unmarshal(recorded, &back))
	assert.Equal(t, testOpenedSessionID, back.PaymentSessionID)
	assert.Equal(t, testOpenedCollectionID, back.PaymentCollectionID)

	_, err = newHarness(t).wf.RecoveryWorkflow(recorded)
	require.NoError(t, err)
}

// TestOpenPaymentJSONCrossesTheBoundary: the interop translates the request
// and the answer field for field.
func TestOpenPaymentJSONCrossesTheBoundary(t *testing.T) {
	h := newHarness(t)
	h.payments.createCollectionFn = func(context.Context, string, string, string, int64) (string, error) {
		return testOpenedCollectionID, nil
	}
	h.payments.openSessionFn = func(context.Context, string, string, string, json.RawMessage) (string, error) {
		return testOpenedSessionID, nil
	}
	h.payments.sessions = map[string]paymentSessionFacts{testOpenedSessionID: openedSession()}

	response, err := NewInterop(h.wf).OpenPaymentJSON(context.Background(), json.RawMessage(
		`{"cart_id":"`+testCartID+`","payment_provider_id":"`+testProviderID+
			`","payment_data":{"email":"a@b.c"},"sales_channel_ids":["sc_web"],"expected_total":3000}`))

	require.NoError(t, err)
	assert.JSONEq(t, `{"payment_session_id":"pses_opened","payment_collection_id":"pcol_opened",
		"provider_id":"manual","status":"pending","amount":3000,"currency_code":"TRY",
		"data":{"iframe_token":"tok_1"},"completion_secret":"cpsec_pses_opened"}`, string(response))

	_, err = NewInterop(h.wf).OpenPaymentJSON(context.Background(), nil)
	require.Error(t, err)
	assert.True(t, hasCode(err, CodeInteropRequestInvalid))
}

// TestCompleteCartJSONCarriesTheOpenedSession: the completion's interop field
// reaches the flow.
func TestCompleteCartJSONCarriesTheOpenedSession(t *testing.T) {
	h := newHarness(t)
	h.payments.sessions = map[string]paymentSessionFacts{testOpenedSessionID: openedSession()}
	// The interop takes no location, so the reservation asks where the stock
	// is, and the test location holds it.
	h.inventory.locationsFn = func(context.Context, string, int64) ([]string, error) {
		return []string{testLocationID}, nil
	}
	h.fulfillment.rankFn = rankByGreatestID

	_, err := NewInterop(h.wf).CompleteCartJSON(context.Background(), json.RawMessage(
		`{"cart_id":"`+testCartID+`","payment_provider_id":"`+testProviderID+
			`","payment_session_id":"`+testOpenedSessionID+`","expected_total":3000}`))

	require.NoError(t, err)
	assert.Zero(t, h.rec.count("payment:session"), "the named session was authorized, none was opened")
	assert.Equal(t, 2, h.rec.count("payment:read_session"),
		"once to find no order already took it, once to hold it to the plan")
}

// TestCancelPaymentCancelsTheCartsOwnSession: the session opened for the cart
// is canceled, and the cart is not written (ADR 0438).
func TestCancelPaymentCancelsTheCartsOwnSession(t *testing.T) {
	h := newHarness(t)
	h.payments.sessions = map[string]paymentSessionFacts{testOpenedSessionID: openedSession()}
	var canceled []string
	h.payments.cancelFn = func(_ context.Context, sessionID string) error {
		canceled = append(canceled, sessionID)

		return nil
	}

	require.NoError(t, h.wf.CancelPayment(context.Background(), testCartID, testOpenedSessionID))

	assert.Equal(t, []string{testOpenedSessionID}, canceled)
	assert.Zero(t, h.rec.count("totals:calculate"), "a cancel does not reprice the cart")
	assert.Zero(t, h.rec.count("cart:complete"))
}

// TestCancelPaymentRefusesAnotherCartsSession: the cart is the capability, so a
// session opened for another cart is not canceled through it.
func TestCancelPaymentRefusesAnotherCartsSession(t *testing.T) {
	h := newHarness(t)
	facts := openedSession()
	facts.Collection.Reference = "cart_somebody_else"
	h.payments.sessions = map[string]paymentSessionFacts{testOpenedSessionID: facts}

	err := h.wf.CancelPayment(context.Background(), testCartID, testOpenedSessionID)

	require.Error(t, err)
	assert.True(t, hasCode(err, CodePaymentSessionForeign), "%v", err)
	assert.NotContains(t, err.Error(), "cart_somebody_else")
	assert.Zero(t, h.rec.count("payment:cancel"))
}

// TestCancelPaymentRefusesACompletedCart: a completed cart's payment is its
// order's, not the storefront's.
func TestCancelPaymentRefusesACompletedCart(t *testing.T) {
	h := newHarness(t)
	h.payments.sessions = map[string]paymentSessionFacts{testOpenedSessionID: openedSession()}
	h.carts.snapshotFn = func(ctx context.Context, cartID string) (json.RawMessage, error) {
		raw, err := defaultSnapshot(ctx, cartID)
		require.NoError(t, err)
		var snap map[string]any
		require.NoError(t, json.Unmarshal(raw, &snap))
		snap["completed"] = true

		return json.Marshal(snap)
	}

	err := h.wf.CancelPayment(context.Background(), testCartID, testOpenedSessionID)

	require.Error(t, err)
	assert.True(t, hasCode(err, CodeCartCompleted), "%v", err)
	assert.Zero(t, h.rec.count("payment:cancel"))
}

// TestCancelPaymentJSONCrossesTheBoundary: the interop decodes the request and
// refuses one it cannot read.
func TestCancelPaymentJSONCrossesTheBoundary(t *testing.T) {
	h := newHarness(t)
	h.payments.sessions = map[string]paymentSessionFacts{testOpenedSessionID: openedSession()}

	require.NoError(t, NewInterop(h.wf).CancelPaymentJSON(context.Background(), json.RawMessage(
		`{"cart_id":"`+testCartID+`","payment_session_id":"`+testOpenedSessionID+`"}`)))
	assert.Equal(t, 1, h.rec.count("payment:cancel"))

	err := NewInterop(h.wf).CancelPaymentJSON(context.Background(), json.RawMessage(`{`))
	require.Error(t, err)
	assert.True(t, hasCode(err, CodeInteropRequestInvalid))
}
