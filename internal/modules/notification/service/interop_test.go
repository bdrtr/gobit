package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/notification/models"
	"github.com/bdrtr/gobit/internal/modules/notification/service"
)

// TestTheSurfaceCarriesEACHFieldToItsOwnPlace is the translation, checked field by
// field.
//
// A surface that only forwards looks too simple to test, and this one is not: it
// maps five arguments onto five fields of an input whose meanings differ, and TWO of
// them — template and reference — are the idempotency key. Swapping a pair would
// compile, send something, and quietly make every invitation to one user look like a
// repeat of the first (ADR 0137).
//
// A mutation doing exactly that survived every other test in this package, which is
// why this one exists.
func TestTheSurfaceCarriesEACHFieldToItsOwnPlace(t *testing.T) {
	svc, store, prov := setup(t)
	interop := service.NewInterop(svc)

	require.NoError(t, interop.Send(context.Background(),
		"auth.user_invited", coreprovider.ChannelEmail, "invitation_01REFERENCE00",
		"colleague@example.test", map[string]string{"token": "a-secret"}))

	require.Equal(t, 1, prov.callCount())
	sent := prov.lastNotification()
	assert.Equal(t, "auth.user_invited", sent.Template)
	assert.Equal(t, coreprovider.ChannelEmail, sent.Channel)
	assert.Equal(t, "colleague@example.test", sent.To)
	assert.Equal(t, "a-secret", sent.Data["token"],
		"a caller's secret reaches the provider and nothing in between logs its value")

	records := store.allRecords()
	require.Len(t, records, 1)
	assert.Equal(t, "auth.user_invited", records[0].Template)
	assert.Equal(t, "invitation_01REFERENCE00", records[0].Reference,
		"the reference is the caller's OWN record and the second half of the "+
			"idempotency key; carrying the template here instead would make every "+
			"message of one template look like a repeat")
	assert.Equal(t, models.DeliverySent, records[0].Status)
}

// TestTheSurfaceIsIDEMPOTENTTheWayTheHandlersAre proves the key is the same key.
//
// Nothing about this surface re-implements the guarantee; the point of the test is
// that it does not ACCIDENTALLY defeat it — by generating a reference, say, or by
// passing the address where the reference belongs.
func TestTheSurfaceIsIDEMPOTENTTheWayTheHandlersAre(t *testing.T) {
	svc, store, prov := setup(t)
	interop := service.NewInterop(svc)

	for range 2 {
		require.NoError(t, interop.Send(context.Background(),
			"auth.user_invited", coreprovider.ChannelEmail, "invitation_01SAME00000000",
			"colleague@example.test", nil))
	}

	assert.Equal(t, 1, prov.callCount(), "one reference, one message")
	assert.Len(t, store.allRecords(), 1)
}
