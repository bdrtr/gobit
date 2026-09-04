package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/notification/models"
	"github.com/bdrtr/gobit/internal/modules/notification/service"
)

// TestNewRefusesAConstructionWithAMissingDependency verifies that a missing
// dependency is said at construction time.
//
// A service constructed with a nil store would produce a panic on the first
// EVENT and the fault would surface long after the construction — at the moment
// the first order is placed.
func TestNewRefusesAConstructionWithAMissingDependency(t *testing.T) {
	complete := service.Options{
		Store:      newFakeStore(),
		Providers:  service.NewProviderRegistry(),
		ProviderID: "log",
		Contacts:   &fakeContacts{},
	}

	tests := map[string]func(o *service.Options){
		"without a store":    func(o *service.Options) { o.Store = nil },
		"without a registry": func(o *service.Options) { o.Providers = nil },
		"without a provider": func(o *service.Options) { o.ProviderID = "" },
		"without a reader":   func(o *service.Options) { o.Contacts = nil },
	}

	for name, breakIt := range tests {
		t.Run(name, func(t *testing.T) {
			opts := complete
			breakIt(&opts)

			_, err := service.New(opts)

			require.Error(t, err)
			assert.Equal(t, service.CodeNotReady, errors.CodeOf(err))
		})
	}

	svc, err := service.New(complete)
	require.NoError(t, err)
	assert.Equal(t, "log", svc.ProviderID())
}

// TestListDeliveriesRefusesAnUnrecognizedStatus verifies that a misspelled
// status filter does not silently return an empty list.
//
// A silent empty list would make "there are no failed notifications at all"
// indistinguishable from "I typed the status name wrong" — the first is
// reassuring, the second is misleading.
func TestListDeliveriesRefusesAnUnrecognizedStatus(t *testing.T) {
	svc, _, _ := setup(t)
	status := "delivered"

	_, _, err := svc.ListDeliveries(context.Background(),
		service.ListDeliveriesInput{Status: &status})

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "error: %v", err)
	assert.Contains(t, err.Error(), "delivered")
}

// TestListDeliveriesEnforcesThePaginationBounds verifies the limit/offset
// validation and the default.
func TestListDeliveriesEnforcesThePaginationBounds(t *testing.T) {
	svc, _, _ := setup(t)
	ctx := context.Background()

	_, _, err := svc.ListDeliveries(ctx, service.ListDeliveriesInput{
		Page: service.Page{Limit: service.MaxLimit + 1},
	})
	require.Error(t, err, "a limit above the ceiling has to be refused")

	_, _, err = svc.ListDeliveries(ctx, service.ListDeliveriesInput{
		Page: service.Page{Offset: -1},
	})
	require.Error(t, err, "a negative offset has to be refused")

	require.NoError(t, svc.Notify(ctx, testInput()))

	records, total, err := svc.ListDeliveries(ctx, service.ListDeliveriesInput{})
	require.NoError(t, err)
	assert.Len(t, records, 1)
	assert.Equal(t, int64(1), total)
}

// TestListDeliveriesFiltersByReference verifies the way of finding the
// notifications of an order; that is the log's most frequent question.
func TestListDeliveriesFiltersByReference(t *testing.T) {
	svc, _, _ := setup(t)
	ctx := context.Background()

	first := testInput()
	second := testInput()
	second.Reference = "order_OTHER"

	require.NoError(t, svc.Notify(ctx, first))
	require.NoError(t, svc.Notify(ctx, second))

	reference := "order_OTHER"
	records, total, err := svc.ListDeliveries(ctx,
		service.ListDeliveriesInput{Reference: &reference})

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "order_OTHER", records[0].Reference)
	assert.Equal(t, models.DeliverySent, records[0].Status)
}

// TestGetDeliveryRefusesAnEmptyIdentifier verifies that an empty identifier
// never reaches the store.
func TestGetDeliveryRefusesAnEmptyIdentifier(t *testing.T) {
	svc, _, _ := setup(t)

	_, err := svc.GetDelivery(context.Background(), "")

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "error: %v", err)
}
