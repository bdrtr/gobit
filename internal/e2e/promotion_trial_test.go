//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authsvc "github.com/bdrtr/gobit/internal/modules/auth/service"
	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	promotionmodels "github.com/bdrtr/gobit/internal/modules/promotion/models"
	promotionsvc "github.com/bdrtr/gobit/internal/modules/promotion/service"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// The amounts of the trial scenario, computed by hand.
//
//	12_340 × 2 = 24_680 subtotal per order
//	10% of it  =  2_468 the promotion would have added (no other promotion
//	              targets this variant, so the line had nothing discounted)
const (
	trialUnitPrice int64 = 12_340
	trialQuantity  int64 = 2
	trialRateBps   int64 = 1_000
	trialAdded     int64 = trialUnitPrice * trialQuantity * trialRateBps / 10_000
)

// TestAPromotionCanBeTriedOnTheOrdersItWouldHaveDiscounted is the gate ADR 0176
// rests on: a DRAFT coupon, priced against real orders through the production
// endpoint, over the order and promotion modules' real schemas.
//
// Two orders of the target variant were placed and a third was placed and
// canceled. The trial has to name the two with the discount they would have
// gained, leave the canceled one out, write nothing, and refuse an identity that
// may read promotions but not orders — the report is orders.
func TestAPromotionCanBeTriedOnTheOrdersItWouldHaveDiscounted(t *testing.T) {
	ctx := t.Context()
	from := time.Now().Add(-time.Second)

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Trial Product",
		map[string]int64{taxedCurrency: trialUnitPrice}, 10)

	place := func(outcome string) (checkoutwf.CompleteCartResult, error) {
		cartID, totals := prepareCart(ctx, t, customerID, variantID, trialQuantity)

		return orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
			CartID:            cartID,
			LocationID:        stockLocationID,
			PaymentProviderID: paymentmanual.ID,
			PaymentData:       paymentBehavior(t, outcome),
			Email:             email,
			ExpectedTotal:     totals.Total,
		})
	}
	var ordered []string
	for range 2 {
		placed, err := place(paymentmanual.OutcomeAuthorize)
		require.NoError(t, err)
		ordered = append(ordered, placed.OrderID)
	}
	first, second := ordered[0], ordered[1]
	// A declined payment is what cancels an order in the saga: the order is
	// placed in the second step and the compensation cancels it.
	_, err := place(paymentmanual.OutcomeDecline)
	require.Error(t, err, "the declined completion is the canceled order of this scenario")

	promotionID := newDraftTrialCoupon(ctx, t, variantID)
	to := time.Now()

	both := scopedAdminToken(t, "promotion:read", "order:read")
	rec := adminRequest(t, http.MethodGet, trialPath(promotionID, from, to), "Bearer "+both)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var envelope struct {
		Data struct {
			Assumptions    []string `json:"assumptions"`
			OrdersRead     int      `json:"orders_read"`
			OrdersCanceled int      `json:"orders_canceled"`
			Orders         []struct {
				OrderID       string `json:"order_id"`
				TrialDiscount int64  `json:"trial_discount"`
				Subtotal      int64  `json:"subtotal"`
			} `json:"orders"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), "body: %s", rec.Body.String())
	report := envelope.Data

	// Other scenarios place orders in the same window; the target rule names a
	// variant only this one sells, so the discounted orders are exactly ours.
	discounted := map[string]int64{}
	for _, order := range report.Orders {
		discounted[order.OrderID] = order.TrialDiscount
	}
	assert.Equal(t, map[string]int64{first: trialAdded, second: trialAdded}, discounted,
		"the trial has to name both orders of the target variant, each with 10 percent of its "+
			"subtotal, and no other order — and not the canceled one")
	assert.GreaterOrEqual(t, report.OrdersCanceled, 1, "the canceled order is counted apart")
	assert.GreaterOrEqual(t, report.OrdersRead, 3)
	assert.Subset(t, report.Assumptions, []string{"active", "automatic", "todays_catalog"},
		"what the trial set aside is published with it")

	promotion, err := promotionSvc.GetPromotion(ctx, promotionID)
	require.NoError(t, err)
	assert.Equal(t, promotionmodels.PromotionDraft, promotion.Status, "a trial publishes nothing")
	redemptions, err := promotionSvc.ListRedemptions(ctx, promotionID, 10, 0)
	require.NoError(t, err)
	assert.Zero(t, redemptions.Count, "a trial spends nothing")

	t.Run("an identity that may not read orders is refused", func(t *testing.T) {
		promotionsOnly := scopedAdminToken(t, "promotion:read")
		rec := adminRequest(t, http.MethodGet, trialPath(promotionID, from, to), "Bearer "+promotionsOnly)
		assert.Equal(t, http.StatusForbidden, rec.Code,
			"the report is orders: an identity allowed to read campaigns but not sales would read "+
				"sales here; body: %s", rec.Body.String())
	})

	t.Run("a period reaching into the future is refused", func(t *testing.T) {
		rec := adminRequest(t, http.MethodGet,
			trialPath(promotionID, from, time.Now().Add(time.Hour)), "Bearer "+both)
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
	})
}

// trialPath is the trial's address for a promotion and a period.
func trialPath(promotionID string, from, to time.Time) string {
	query := url.Values{}
	query.Set("from", from.UTC().Format(time.RFC3339Nano))
	query.Set("to", to.UTC().Format(time.RFC3339Nano))

	return "/admin/v1/promotions/" + promotionID + "/trial?" + query.Encode()
}

// newDraftTrialCoupon sets up an unpublished coupon taking 10 percent off the
// given variant and returns its id.
func newDraftTrialCoupon(ctx context.Context, t *testing.T, variantID string) string {
	t.Helper()

	promotion, err := promotionSvc.CreatePromotion(ctx, promotionsvc.PromotionInput{
		Code:        fmt.Sprintf("TRIAL%d", fixtureCounter.Add(1)),
		IsAutomatic: false,
		Status:      promotionmodels.PromotionDraft,
	})
	require.NoError(t, err, "the trial coupon could not be created")

	_, err = promotionSvc.SetApplicationMethod(ctx, promotion.ID, promotionsvc.ApplicationMethodInput{
		Type:       promotionmodels.MethodPercentage,
		TargetType: promotionmodels.TargetItems,
		Allocation: promotionmodels.AllocationEach,
		Value:      trialRateBps,
	})
	require.NoError(t, err, "the trial coupon's method could not be written")

	_, err = promotionSvc.AddPromotionRule(ctx, promotion.ID, promotionsvc.RuleInput{
		RuleType:  promotionmodels.RuleTarget,
		Attribute: attrVariantID,
		Operator:  promotionmodels.OpIn,
		Values:    []string{variantID},
	})
	require.NoError(t, err, "the trial coupon's target rule could not be written")

	return promotion.ID
}

// scopedAdminToken signs in a fresh admin user holding exactly the given scopes
// and returns the token.
func scopedAdminToken(t *testing.T, scopes ...string) string {
	t.Helper()

	email := fmt.Sprintf("trial-%d@gobit.test", fixtureCounter.Add(1))
	const password = "trial-scope-password-42"
	_, err := authSvc.CreateUser(t.Context(), authsvc.CreateUserInput{
		Email:     email,
		FirstName: "Trial",
		LastName:  "Operator",
		Scopes:    scopes,
	}, password)
	require.NoError(t, err, "the scoped admin user could not be created")

	return jetonAl(t, email, password)
}
