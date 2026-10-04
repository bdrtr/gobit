//go:build integration

package order_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// The two compares ADR 0388 adds under the order's lock, raced on the real
// schema in the shape of TestConcurrentCreditsCannotPassTheCeiling: several
// rounds, because a race lost once in three passes a single round (ADR 0091).
const (
	readRounds  = 5
	readWriters = 10
)

// race runs the writers at once, each with its index, and returns their
// errors.
func race(write func(idx int) error) []error {
	var start, finish sync.WaitGroup
	errs := make([]error, readWriters)
	start.Add(1)
	finish.Add(readWriters)
	for i := range readWriters {
		go func(idx int) {
			defer finish.Done()
			start.Wait()
			errs[idx] = write(idx)
		}(i)
	}
	start.Done()
	finish.Wait()

	return errs
}

// granted counts the writers that succeeded, and asserts every other one was
// refused with the code given.
func granted(t *testing.T, errs []error, refused string, round int) int {
	t.Helper()

	n := 0
	for _, err := range errs {
		if err == nil {
			n++
			continue
		}
		assert.Equal(t, refused, errors.CodeOf(err), "round %d: %v", round, err)
	}

	return n
}

// TestTenCreditsFromOnePageCreditOnce is ADR 0388: ten credits drawn from
// one page, each naming the total it read, credit once; a check made outside
// the order's lock, or before it, lets several through.
func TestTenCreditsFromOnePageCreditOnce(t *testing.T) {
	const each = 300

	ctx := context.Background()
	svc, _ := newService(t)

	for round := range readRounds {
		placed, err := svc.CreateOrder(ctx, validInput())
		require.NoError(t, err, "round %d", round)
		none := int64(0)

		errs := race(func(int) error {
			_, err := svc.CreateCreditLine(ctx, placed.ID, service.CreateCreditLineInput{
				Amount: each, Reason: "concurrent", ReadCredited: &none,
			})
			return err
		})

		assert.Equal(t, 1, granted(t, errs, service.CodeCreditMoved, round), "round %d: one form, one credit", round)
		detail, err := svc.GetOrder(ctx, placed.ID)
		require.NoError(t, err)
		assert.Equal(t, int64(each), detail.CreditedTotal, "round %d", round)
	}
}

// TestTenCorrectionsFromOnePageWriteOnce is ADR 0388: ten corrections drawn
// from one row, each saying something else, write once, and the order holds
// one closed row and one current; a check made outside the order's lock lets
// several through.
func TestTenCorrectionsFromOnePageWriteOnce(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	for round := range readRounds {
		placed := correctableOrder(ctx, t, svc)
		detail, err := svc.GetOrder(ctx, placed.ID)
		require.NoError(t, err)
		require.NotNil(t, detail.ShippingAddress)
		read := detail.ShippingAddress.ID

		errs := race(func(idx int) error {
			_, err := svc.CorrectShippingAddressFrom(ctx, placed.ID, models.OrderAddress{
				FirstName: "Ada", Address1: fmt.Sprintf("%d Race St", idx+1),
				City: "Springfield", PostalCode: "62701",
			}, read)
			return err
		})

		assert.Equal(t, 1, granted(t, errs, service.CodeAddressRevised, round), "round %d: one form, one correction", round)
		var closed, current int
		require.NoError(t, testPool.Pool().QueryRow(ctx, `
			SELECT count(*) FILTER (WHERE superseded_at IS NOT NULL),
			       count(*) FILTER (WHERE superseded_at IS NULL)
			FROM order_addresses WHERE order_id = $1 AND address_type = 'shipping'`,
			placed.ID).Scan(&closed, &current))
		assert.Equal(t, 1, closed, "round %d: the row read was closed once", round)
		assert.Equal(t, 1, current, "round %d", round)
	}
}

// TestThePanelCreditsAnOrderOnce is ADR 0388 through the panel's surface:
// the credit is written with its reason and note from the total read, the
// list carries the total the form names, and the same form again is refused.
func TestThePanelCreditsAnOrderOnce(t *testing.T) {
	ctx := context.Background()
	svc, surface := panelSurface(t)
	placed, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	require.NoError(t, surface.CreditOrder(ctx, placed.ID, 0, 250, "goodwill", "agreed on the phone"))
	err = surface.CreditOrder(ctx, placed.ID, 0, 250, "goodwill", "")
	assert.Equal(t, service.CodeCreditMoved, errors.CodeOf(err), "the same form sent twice: %v", err)

	raw, err := surface.CreditLinesJSON(ctx, placed.ID)
	require.NoError(t, err)
	var credits struct {
		CreditedTotal int64 `json:"credited_total"`
		Lines         []struct {
			ID        string    `json:"id"`
			Amount    int64     `json:"amount"`
			Reason    string    `json:"reason"`
			Note      string    `json:"note"`
			CreatedAt time.Time `json:"created_at"`
		} `json:"lines"`
	}
	require.NoError(t, json.Unmarshal(raw, &credits), string(raw))
	assert.Equal(t, int64(250), credits.CreditedTotal)
	require.Len(t, credits.Lines, 1, string(raw))
	assert.NotEmpty(t, credits.Lines[0].ID)
	assert.Equal(t, int64(250), credits.Lines[0].Amount)
	assert.Equal(t, "goodwill", credits.Lines[0].Reason)
	assert.Equal(t, "agreed on the phone", credits.Lines[0].Note)
	assert.False(t, credits.Lines[0].CreatedAt.IsZero(), "the credit's date, which the page prints: %s", raw)
}

// TestThePanelReadsWhatADeliveryCostsNow is ADR 0388: the deliveries the
// panel lists carry the price of their latest change, not the one sold.
func TestThePanelReadsWhatADeliveryCostsNow(t *testing.T) {
	ctx := context.Background()
	svc, surface := panelSurface(t)
	placed, methodID := soldExpressOn(ctx, t, svc)

	_, err := svc.ChangeDelivery(ctx, placed.ID, changeTo(methodID, "so_pickup", 1000))
	require.NoError(t, err)
	raw, err := surface.DeliveriesJSON(ctx, placed.ID)
	require.NoError(t, err)
	assert.JSONEq(t, `[{"id":"`+methodID+`","shipping_option_id":"so_pickup","name":"so_pickup","amount":1000}]`,
		string(raw))
}
