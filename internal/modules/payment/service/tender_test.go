package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/loyaltypoints"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
	"github.com/bdrtr/gobit/internal/modules/payment/storecredit"
)

// TestTheTenderCheckRefusesOnlyWhatIsKnownInAdvance verifies the question the
// checkout asks before it opens an order (ADR 0175).
//
// It refuses two things and nothing else: a provider nobody registered, and a
// person's balance for a payment that names nobody. A card provider for a guest
// passes, and so does a balance for a named customer — whether either will be
// authorized is only true when it is asked, which is the saga's step.
func TestTheTenderCheckRefusesOnlyWhatIsKnownInAdvance(t *testing.T) {
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(newFakeProvider("card")))
	// The tenders are the real ones: the rule under test is theirs. Neither
	// reads its store to answer, which is what makes the answer knowable early.
	require.NoError(t, registry.Register(storecredit.New(nil, nil)))
	require.NoError(t, registry.Register(loyaltypoints.New(nil, nil)))

	svc, err := service.New(service.Options{Store: newFakeStore(), Providers: registry, Events: newFakeBus()})
	require.NoError(t, err)

	for name, tc := range map[string]struct {
		provider, customer string
		code               string
	}{
		"a card for a guest":            {provider: "card"},
		"store credit for a customer":   {provider: storecredit.ID, customer: "cus_1"},
		"loyalty points for a customer": {provider: loyaltypoints.ID, customer: "cus_1"},
		"store credit for a guest":      {provider: storecredit.ID, code: storecredit.CodeNoCustomer},
		"loyalty points for a guest":    {provider: loyaltypoints.ID, code: loyaltypoints.CodeNoCustomer},
		"a blank customer is nobody":    {provider: storecredit.ID, customer: "   ", code: storecredit.CodeNoCustomer},
		"a provider nobody registered":  {provider: "adyen", customer: "cus_1", code: service.CodeProviderNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			err := svc.CheckTender(context.Background(), tc.provider, tc.customer)
			if tc.code == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, tc.code, errors.CodeOf(err))
		})
	}
}
