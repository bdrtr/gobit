package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// TestInteropSurfaceUsesPrimitiveTypes proves the cross-module surface
// structurally satisfies the narrow interface a consumer can declare in its OWN
// package (ADR 0001, ADR 0006).
//
// The interface here deliberately names none of inventory's types; the saga
// writes it exactly this way and resolves the concrete value from the container
// under the name "inventory.interop". The signatures are a CONTRACT: since the
// consumer cannot import this module, a signature drift is INVISIBLE at compile
// time and only blows up at resolution time as "does not satisfy the
// interface". This assignment brings that moment forward.
func TestInteropSurfaceUsesPrimitiveTypes(t *testing.T) {
	// An exact copy of the interface the consumer package will write.
	type inventoryReserver interface {
		Reserve(
			ctx context.Context,
			inventoryItemID, locationID string,
			quantity int64,
			lineItemID string,
		) (reservationID string, err error)
		ReleaseReservation(ctx context.Context, reservationID string) error
		ConfirmReservation(ctx context.Context, reservationID, orderID string) error
	}
	// The interface of the flow that puts canceled units back is SEPARATE too:
	// that flow makes no reservation and picks no location; it only asks which
	// shelf the stock came off and puts it back (ADR 0134).
	type inventoryCancellations interface {
		SaleLocations(ctx context.Context, orderID string) (map[string]string, error)
		ReturnCanceled(
			ctx context.Context,
			inventoryItemID, locationID, lineItemID string,
			target int64,
			reference string,
		) (alreadyBack bool, err error)
	}
	// The surface that asks for location candidates is a SEPARATE interface:
	// the flow that uses it (the step that picks which warehouse ships) makes no
	// reservation, and a narrow interface should ask only for the method it
	// actually calls.
	type inventoryLocations interface {
		LocationsWithStock(ctx context.Context, inventoryItemID string, quantity int64) ([]string, error)
	}

	svc, _ := newService(t)
	interop := service.NewInterop(svc)

	var (
		_ inventoryReserver      = interop
		_ inventoryLocations     = interop
		_ inventoryCancellations = interop
	)
}

// TestInteropLocationsWithStockReturnsTheCandidates proves the surface only
// translates the signature, that is, it gives THE SAME answer as the service.
//
// Had the surface done its own filtering or sorting, the same question would
// get two different answers from two places; that is exactly what interop's
// promise to carry no rules means.
func TestInteropLocationsWithStockReturnsTheCandidates(t *testing.T) {
	ctx := context.Background()
	svc, store := newService(t)
	interop := service.NewInterop(svc)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 5)
	store.seedLevel(itemID, locB, 10, 6)

	locations, err := interop.LocationsWithStock(ctx, itemID, 5)

	require.NoError(t, err)
	assert.Equal(t, []string{locA}, locations)

	expected, err := svc.LocationsWithStock(ctx, itemID, 5)
	require.NoError(t, err)
	assert.Equal(t, expected, locations, "the surface must not change the service's answer")
}

// TestInteropLocationsWithStockWithNoCandidateIsAnEmptySlice proves that when
// there is not enough stock the surface returns an EMPTY slice, not an error.
//
// The distinction is critical for the saga: an error triggers the flow's
// compensation chain, while an empty list is a normal answer the flow turns into
// a Conflict in its own context (for example "this line cannot ship").
func TestInteropLocationsWithStockWithNoCandidateIsAnEmptySlice(t *testing.T) {
	svc, store := newService(t)
	interop := service.NewInterop(svc)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 9)

	locations, err := interop.LocationsWithStock(context.Background(), itemID, 5)

	require.NoError(t, err, "not having enough stock is not a fault")
	assert.Empty(t, locations)
	assert.NotNil(t, locations, "an empty slice has to come back, not nil")
}
