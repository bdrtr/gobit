package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestAGiftCardLineCarriesNoTax is ADR 0247 at the order: a card is money its
// holder spends later and the goods it buys are taxed then, so a card line
// that carries tax is refused and one that carries none is placed.
func TestAGiftCardLineCarriesNoTax(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	taxed := validInput()
	taxed.Items[0].IsGiftcard = true
	_, err := e.svc.CreateOrder(ctx, taxed)
	require.Error(t, err)
	assert.Equal(t, service.CodeTotalsInconsistent, errors.CodeOf(err))
	assert.Contains(t, err.Error(), "gift card")

	untaxed := validInput()
	untaxed.Items[0].IsGiftcard = true
	untaxed.Items[0].TaxTotal, untaxed.Items[0].Total = 0, 3000
	untaxed.TaxTotal, untaxed.Total = 0, 5500
	order, err := e.svc.CreateOrder(ctx, untaxed)
	require.NoError(t, err)
	assert.Equal(t, int64(5500), order.Total)
}
