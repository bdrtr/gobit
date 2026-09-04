package service_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// windowStart is the start of the spending window in the tests.
//
// A fixed moment is chosen: WHERE the window starts is the decision of the b2b
// module and what is exercised here is whether the given window IS APPLIED.
var windowStart = time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)

// limitRule produces a spending rule body with the given limit.
func limitRule(limit int64, currency string, window *time.Time) json.RawMessage {
	rule := map[string]any{
		"limited":        true,
		"spending_limit": limit,
		"currency_code":  currency,
		"window_start":   "",
	}
	if window != nil {
		rule["window_start"] = window.Format(time.RFC3339)
	}
	payload, err := json.Marshal(rule)
	if err != nil {
		panic(err)
	}
	return payload
}

// limitedEnv sets up a service with a spending rule wired in.
func limitedEnv(t *testing.T, payload json.RawMessage) (env, *fakeSpendingPolicy) {
	t.Helper()

	store := newFakeStore()
	bus := newFakeBus()
	policy := &fakeSpendingPolicy{payload: payload}

	svc, err := service.New(service.Options{
		Repo: store, Events: bus, Spending: policy,
	})
	require.NoError(t, err)

	return env{svc: svc, store: store, bus: bus}, policy
}

// pastOrder writes the customer's past spend into the store.
func pastOrder(t *testing.T, e env, id string, total int64, placedAt time.Time) {
	t.Helper()

	e.store.seedOrder(models.Order{
		ID:           id,
		Status:       models.OrderPending,
		RegionID:     testRegionID,
		CustomerID:   testCustomerID,
		CurrencyCode: testCurrency,
		Subtotal:     total,
		Total:        total,
		PlacedAt:     placedAt,
	})
}

// TestTheOrderIsNotOpenedWhenTheSpendingLimitIsExceeded pins down that the rule
// is REALLY applied.
//
// The order is worth 6100; 5000 was spent within the period and the limit is
// 10000. Because 5000 + 6100 > 10000 the order must not be opened and NO TRACE
// must be left in the store: the caller of this flow (the complete_cart saga)
// opens the order BEFORE the payment, that is, a rejection here means that the
// payment was never even attempted.
func TestTheOrderIsNotOpenedWhenTheSpendingLimitIsExceeded(t *testing.T) {
	e, policy := limitedEnv(t, limitRule(10_000, testCurrency, &windowStart))
	pastOrder(t, e, "order_PAST", 5000, windowStart.Add(time.Hour))

	_, err := e.svc.CreateOrder(context.Background(), validInput())

	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "exceeding the limit is a conflict, not a server fault")
	assert.Equal(t, service.CodeSpendingLimitExceeded, errors.CodeOf(err))
	assert.Equal(t, []string{testCustomerID}, policy.calls())

	// The write WAS ROLLED BACK: only the past order is left behind.
	records, _, listErr := e.svc.ListOrders(context.Background(), service.ListOrdersInput{})
	require.NoError(t, listErr)
	require.Len(t, records, 1)
	assert.Equal(t, "order_PAST", records[0].ID)

	// No event was published either: there is no announcement of an order that
	// was not opened.
	assert.Empty(t, e.bus.events())
}

// TestAnOrderBelowTheSpendingLimitPasses pins down that an order staying BELOW
// the bound is not blocked.
//
// The total is exactly EQUAL to the limit (3900 + 6100 = 10000): the limit is
// the CEILING that may be spent and as long as it is not exceeded the order
// passes. Exercising the equality separately is the only test that catches the
// one-character shift between "greater than" and "greater or equal".
func TestAnOrderBelowTheSpendingLimitPasses(t *testing.T) {
	e, _ := limitedEnv(t, limitRule(10_000, testCurrency, &windowStart))
	pastOrder(t, e, "order_PAST", 3900, windowStart.Add(time.Hour))

	created, err := e.svc.CreateOrder(context.Background(), validInput())

	require.NoError(t, err)
	assert.Equal(t, int64(6100), created.Total)
	assert.Equal(t, 1, e.store.spendingLockCount(), "the lock has to be taken while the rule is applied")
	assert.Equal(t, 1, e.store.spendingSumCount())
}

// TestTheSpendIsNeverReadWhenThereIsNoLimit pins down that there is no extra
// cost for an employee without a bound.
//
// The "limited": false body is the answer both for an employee whose limit is
// nil in b2b and for a customer who is not tied to any company. In neither case
// is a lock taken or a sum read; otherwise a B2C installation would pay two
// needless queries on every order.
func TestTheSpendIsNeverReadWhenThereIsNoLimit(t *testing.T) {
	e, policy := limitedEnv(t, json.RawMessage(`{"limited":false}`))

	_, err := e.svc.CreateOrder(context.Background(), validInput())

	require.NoError(t, err)
	assert.Equal(t, []string{testCustomerID}, policy.calls(), "the rule IS still asked for")
	assert.Equal(t, 0, e.store.spendingLockCount())
	assert.Equal(t, 0, e.store.spendingSumCount())
}

// TestTheBehaviorDoesNotChangeWhenTheSpendingRuleIsNotWired pins down the
// installation without the b2b module.
//
// When [service.Options.Spending] is left nil the order opening path has to
// behave as if the field had never been added: the rule is not even ASKED FOR.
// This is the only test that preserves today's behavior of a pure B2C
// installation.
func TestTheBehaviorDoesNotChangeWhenTheSpendingRuleIsNotWired(t *testing.T) {
	e := newEnv(t)
	// Enough past spend that the limit WOULD REJECT the order if it were
	// applied: when the rule is not wired these rows must have no effect at all.
	pastOrder(t, e, "order_PAST", 1_000_000, windowStart.Add(time.Hour))

	created, err := e.svc.CreateOrder(context.Background(), validInput())

	require.NoError(t, err)
	assert.Equal(t, int64(6100), created.Total)
	assert.Equal(t, 0, e.store.spendingLockCount())
	assert.Equal(t, 0, e.store.spendingSumCount())
}

// TestTheSpendOutsideTheWindowIsNotCounted pins down that the period is REALLY
// applied.
//
// An order of 50000 placed one second BEFORE the window must not block an
// employee whose limit is 10000: the spending limit is reset at the beginning of
// the period and the spend of the past period does not burn the budget of this
// one.
func TestTheSpendOutsideTheWindowIsNotCounted(t *testing.T) {
	e, _ := limitedEnv(t, limitRule(10_000, testCurrency, &windowStart))
	pastOrder(t, e, "order_PREVIOUS_PERIOD", 50_000, windowStart.Add(-time.Second))

	created, err := e.svc.CreateOrder(context.Background(), validInput())

	require.NoError(t, err)
	assert.Equal(t, int64(6100), created.Total)
}

// TestTheOrderAtTheStartOfTheWindowIsCounted pins down that the lower end of the
// window IS INCLUDED.
//
// An order placed exactly at the start of the window is INSIDE. This one-second
// difference decides which period the order falling on the first moment of the
// month is written to, and one of the two ends has to be chosen; the chosen end
// is the lower one.
func TestTheOrderAtTheStartOfTheWindowIsCounted(t *testing.T) {
	e, _ := limitedEnv(t, limitRule(10_000, testCurrency, &windowStart))
	pastOrder(t, e, "order_EXACT_BOUNDARY", 5000, windowStart)

	_, err := e.svc.CreateOrder(context.Background(), validInput())

	require.Error(t, err)
	assert.Equal(t, service.CodeSpendingLimitExceeded, errors.CodeOf(err))
}

// TestTheWholeHistoryIsCountedWhenThereIsNoWindow pins down the counterpart of
// the "never" period.
//
// When window_start arrives empty there is no window and the employee's WHOLE
// history is summed; even an order from years ago fills the limit. Counting an
// empty field as "the window starts now" would reset a limit that is meant never
// to be reset on every call.
func TestTheWholeHistoryIsCountedWhenThereIsNoWindow(t *testing.T) {
	e, _ := limitedEnv(t, limitRule(10_000, testCurrency, nil))
	pastOrder(t, e, "order_VERY_OLD", 5000, time.Date(2019, time.March, 3, 0, 0, 0, 0, time.UTC))

	_, err := e.svc.CreateOrder(context.Background(), validInput())

	require.Error(t, err)
	assert.Equal(t, service.CodeSpendingLimitExceeded, errors.CodeOf(err))
}

// TestACanceledOrderIsSubtractedFromTheSpend pins down that a cancellation gives
// the budget back.
//
// A cancellation means "this purchase did not happen"; a canceled order holding
// the budget until the end of the period would mean counting goods that were not
// sold as spend.
func TestACanceledOrderIsSubtractedFromTheSpend(t *testing.T) {
	e, _ := limitedEnv(t, limitRule(10_000, testCurrency, &windowStart))
	e.store.seedOrder(models.Order{
		ID:           "order_CANCELED",
		Status:       models.OrderCanceled,
		RegionID:     testRegionID,
		CustomerID:   testCustomerID,
		CurrencyCode: testCurrency,
		Subtotal:     9000,
		Total:        9000,
		PlacedAt:     windowStart.Add(time.Hour),
	})

	created, err := e.svc.CreateOrder(context.Background(), validInput())

	require.NoError(t, err)
	assert.Equal(t, int64(6100), created.Total)
}

// TestTheRefundedAmountIsSubtractedFromTheSpend pins down that a refund gives
// the budget back.
//
// If 8000 of an order of 9000 was refunded the spend is 1000; 1000 + 6100 is
// below the limit and the order has to pass. Had it not been subtracted, an
// order that was refunded in full would lock the employee's budget until the end
// of the period.
func TestTheRefundedAmountIsSubtractedFromTheSpend(t *testing.T) {
	e, _ := limitedEnv(t, limitRule(10_000, testCurrency, &windowStart))
	pastOrder(t, e, "order_REFUNDED", 9000, windowStart.Add(time.Hour))
	e.store.seedRefund("order_REFUNDED", 8000)

	created, err := e.svc.CreateOrder(context.Background(), validInput())

	require.NoError(t, err)
	assert.Equal(t, int64(6100), created.Total)
}

// TestTheCurrencyOfTheRuleIsNormalized pins down that a code arriving in lower
// case does not make the limit impossible to apply.
//
// On the order side the currency is folded to upper case anyway; had the rule
// side not been normalized, "try" and "TRY" would be taken for different
// currencies and every order would be rejected with a currency mismatch — that
// is, the limit would stop the shopping entirely instead of being applied.
func TestTheCurrencyOfTheRuleIsNormalized(t *testing.T) {
	e, _ := limitedEnv(t, limitRule(10_000, "try", &windowStart))

	created, err := e.svc.CreateOrder(context.Background(), validInput())

	require.NoError(t, err)
	assert.Equal(t, int64(6100), created.Total)
}

// TestAnOrderInADifferentCurrencyIsRejected pins down that no conversion is done
// and that the rule IS NOT SKIPPED.
//
// If the limit of the company is in TRY and the order is in USD the two amounts
// cannot be compared. Letting it through silently would open a door of unlimited
// shopping from a region with another currency to an employee whose limit is
// full; converting, on the other hand, would rest on an exchange rate that does
// not exist.
func TestAnOrderInADifferentCurrencyIsRejected(t *testing.T) {
	e, _ := limitedEnv(t, limitRule(10_000, "USD", &windowStart))

	_, err := e.svc.CreateOrder(context.Background(), validInput())

	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
	assert.Equal(t, service.CodeSpendingCurrencyMismatch, errors.CodeOf(err))
	// The rejection has to arrive without leaving any side effect: not even the
	// customer lock is taken.
	assert.Equal(t, 0, e.store.spendingLockCount())
}

// TestTheOrderIsNotOpenedWhenTheRuleCannotBeRead pins down the distinction
// between "there is no rule" and "we could not learn the rule".
//
// When the provider returns an error what the limit is IS UNKNOWN. Letting the
// order through would mean silently removing the limit on every fault of the
// provider.
func TestTheOrderIsNotOpenedWhenTheRuleCannotBeRead(t *testing.T) {
	e, policy := limitedEnv(t, nil)
	policy.err = errors.Internal("b2b_down", "the b2b service is not responding")

	_, err := e.svc.CreateOrder(context.Background(), validInput())

	require.Error(t, err)
	assert.Equal(t, service.CodeSpendingPolicyUnavailable, errors.CodeOf(err))
	assert.Empty(t, e.bus.events())
}

// TestAMalformedRuleBodyIsRejected pins down that a body breaking the contract
// does not silently fall back to "unlimited".
//
// A body that cannot be parsed or that is meaningless is an errors.Internal:
// there is nothing the caller can fix, the provider has broken the contract. A
// parser that cannot read the "limited" field falling back to the default false
// would remove the limit entirely in a broken installation.
func TestAMalformedRuleBodyIsRejected(t *testing.T) {
	for name, body := range map[string]json.RawMessage{
		"unparseable":                json.RawMessage(`{"limited":`),
		"a negative limit":           json.RawMessage(`{"limited":true,"spending_limit":-1,"currency_code":"TRY"}`),
		"a limit without a currency": json.RawMessage(`{"limited":true,"spending_limit":100,"currency_code":""}`),
		"a malformed window":         json.RawMessage(`{"limited":true,"spending_limit":100,"currency_code":"TRY","window_start":"yesterday"}`),
		"an invalid currency":        json.RawMessage(`{"limited":true,"spending_limit":100,"currency_code":"TRYY"}`),
		"a missing currency code":    json.RawMessage(`{"limited":true,"spending_limit":100}`),
	} {
		t.Run(name, func(t *testing.T) {
			e, _ := limitedEnv(t, body)

			_, err := e.svc.CreateOrder(context.Background(), validInput())

			require.Error(t, err)
			assert.Equal(t, service.CodeSpendingPolicyInvalid, errors.CodeOf(err),
				"a malformed rule must not silently fall back to unlimited")
		})
	}
}

// TestAnEmptyRuleBodyIsRejected pins down the provider that returns no body at
// all.
//
// An empty body IS NOT "there is no limit": the provider has not given an answer
// and counting an answer that was not given as unlimited would remove the rule
// in the quietest possible way.
func TestAnEmptyRuleBodyIsRejected(t *testing.T) {
	e, policy := limitedEnv(t, nil)
	policy.empty = true

	_, err := e.svc.CreateOrder(context.Background(), validInput())

	require.Error(t, err)
	assert.Equal(t, service.CodeSpendingPolicyInvalid, errors.CodeOf(err))
}

// TestAGuestOrderDoesNotAskForTheRule pins down that the provider is not called
// at all on an order without a customer.
//
// The spending limit is tied to THE EMPLOYEE and the identity of the employee is
// a customer record; on an order that has no customer there is no rule to ask
// for. Asking with an empty identifier would keep the provider busy for nothing
// on every guest order.
func TestAGuestOrderDoesNotAskForTheRule(t *testing.T) {
	e, policy := limitedEnv(t, limitRule(1, testCurrency, &windowStart))
	in := validInput()
	in.CustomerID = ""

	_, err := e.svc.CreateOrder(context.Background(), in)

	require.NoError(t, err)
	assert.Empty(t, policy.calls())
}

// TestAnIdempotentRepeatDoesNotCountTheSpendTwice pins down that a second call
// arriving with the same key does not burn the budget twice.
//
// A saga can retry a step. The repeated call finds the existing order on the
// cheap path and never enters the rule at all; had it entered, the order written
// by the first call would turn its own repeat into a request that trips the
// limit.
func TestAnIdempotentRepeatDoesNotCountTheSpendTwice(t *testing.T) {
	e, policy := limitedEnv(t, limitRule(10_000, testCurrency, &windowStart))
	in := validInput()
	in.IdempotencyKey = "wf_REPEAT"

	first, err := e.svc.CreateOrder(context.Background(), in)
	require.NoError(t, err)

	second, err := e.svc.CreateOrder(context.Background(), in)
	require.NoError(t, err)

	assert.Equal(t, first.ID, second.ID)
	assert.Len(t, policy.calls(), 1, "the repeated call never enters the rule")
	assert.Equal(t, 1, e.store.spendingSumCount())
}

// TestTheSpendingLockIsTakenInsideTheOrderTransaction pins down that the lock
// that is taken is INSIDE the write transaction.
//
// The fake store rejects with an error a service that tries to take the lock
// outside a transaction; the test passing is the proof that the lock is taken
// inside the transaction. Had the lock been taken outside the transaction it
// would be released immediately and would not close the "read first, then write"
// race at all.
func TestTheSpendingLockIsTakenInsideTheOrderTransaction(t *testing.T) {
	e, _ := limitedEnv(t, limitRule(1_000_000, testCurrency, &windowStart))

	_, err := e.svc.CreateOrder(context.Background(), validInput())

	require.NoError(t, err)
	assert.Equal(t, 1, e.store.spendingLockCount())
}

// TestAnEmployeeWithAZeroLimitCannotSpendAtAll pins down that the distinction
// between 0 and nil is preserved end to end.
//
// On the b2b side nil is "unlimited" while 0 is a real zero limit. The two have
// to behave differently here as well: no order of an employee with a zero limit
// passes.
func TestAnEmployeeWithAZeroLimitCannotSpendAtAll(t *testing.T) {
	e, _ := limitedEnv(t, limitRule(0, testCurrency, &windowStart))

	_, err := e.svc.CreateOrder(context.Background(), validInput())

	require.Error(t, err)
	assert.Equal(t, service.CodeSpendingLimitExceeded, errors.CodeOf(err))
	assert.Contains(t, fmt.Sprint(err), "limit 0")
}

// TestTrustBoundaryGuestOrderIsNeverAskedForTheSpendingRule pins down the TRUST
// BOUNDARY of the rule: no limit at all is applied to a purchase that does not
// declare its customer.
//
// The test protects not a CAPABILITY but a boundary that was deliberately left
// open. The reason it is pinned down is that both the accidental disappearance
// and the accidental widening of the boundary would be silent: this path, on
// which the rule is not applied today, is the first place that has to CHANGE the
// day a customer session arrives, and that day this test falling will be the
// sign that the decision was really made.
//
// The setup is the state in which the order would DEFINITELY be rejected had the
// rule been asked for: the limit is zero. The order still passes, because when
// [service.CreateOrderInput.CustomerID] is empty the provider is never reached —
// the absence of the claim is the absence of the rule too.
func TestTrustBoundaryGuestOrderIsNeverAskedForTheSpendingRule(t *testing.T) {
	e, policy := limitedEnv(t, limitRule(0, testCurrency, &windowStart))

	in := validInput()
	in.CustomerID = ""

	created, err := e.svc.CreateOrder(context.Background(), in)

	require.NoError(t, err,
		"a guest order is INDEPENDENT of the limit today; an error being returned means "+
			"that the boundary was closed and that decision cannot be made without updating ADR 0008")
	assert.Equal(t, int64(6100), created.Total)
	assert.Empty(t, policy.calls(),
		"the rule MUST NOT even be asked for: being asked and getting an 'unlimited' answer "+
			"is another behavior and would load one query onto b2b on every guest order")
	assert.Equal(t, 0, e.store.spendingLockCount())
	assert.Equal(t, 0, e.store.spendingSumCount())
}

// TestTheSpendingRuleIsAppliedToTheDeclaredCustomer pins down the second face of
// the boundary: the module DOES NOT VALIDATE the identifier it is given.
//
// The identifier in the input does not have to be the one of the person who
// really placed the order; this module holds no proof with which to test it (see
// the trust boundary in the spendingRuleFor godoc). The consequence runs in two
// directions and both are visible here: the spend falls out of the window of the
// DECLARED customer, and therefore a stranger's purchase can burn the allowance
// of an employee who has a limit.
//
// The claim looks at the ARGUMENT of the query, because that is exactly where
// the boundary lives: when a layer that authenticates the identity is added, the
// argument of this call has to come from the session and not from the body.
func TestTheSpendingRuleIsAppliedToTheDeclaredCustomer(t *testing.T) {
	const strangerID = "cus_SOMEONE_ELSES_ID"

	e, policy := limitedEnv(t, limitRule(0, testCurrency, &windowStart))

	in := validInput()
	in.CustomerID = strangerID

	_, err := e.svc.CreateOrder(context.Background(), in)

	require.Error(t, err)
	assert.Equal(t, service.CodeSpendingLimitExceeded, errors.CodeOf(err))
	assert.Equal(t, []string{strangerID}, policy.calls(),
		"the rule is asked for the CLAIMED customer; the module has no proof with which "+
			"to test the claim and that is why it asks for whichever identifier it is given")
}
