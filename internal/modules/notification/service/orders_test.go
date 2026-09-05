package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/notification/service"
)

// TestOrderContactsResolutionIsLAZY verifies that the surface is resolved not
// at the moment of Register but ON FIRST USE.
//
// The Register order of the modules is not guaranteed: notification can be
// registered before order, and at that moment "order.interop" is not in the
// container. An early resolution would bring the startup down with an error
// where nothing is really missing.
func TestOrderContactsResolutionIsLAZY(t *testing.T) {
	c := container.New(nil)

	// It is constructed while the surface is NOT YET registered; the
	// construction must not return an error.
	reader := service.NewOrderContacts(c)
	require.NotNil(t, reader)

	require.NoError(t, c.Provide(service.OrderInteropName, &fakeContacts{body: testOrderBody}))

	raw, err := reader.OrderContactJSON(context.Background(), "order_01H")

	require.NoError(t, err)
	var body map[string]string
	require.NoError(t, json.Unmarshal(raw, &body))
	assert.Equal(t, "customer@example.com", body["email"])
}

// TestOrderContactsGivesADiagnosableErrorWhenTheSurfaceIsAbsent verifies that
// the error received while the order module is not installed says what it is.
func TestOrderContactsGivesADiagnosableErrorWhenTheSurfaceIsAbsent(t *testing.T) {
	reader := service.NewOrderContacts(container.New(nil))

	_, err := reader.OrderContactJSON(context.Background(), "order_01H")

	require.Error(t, err)
	assert.Equal(t, service.CodeContactUnavailable, errors.CodeOf(err))
	assert.Contains(t, err.Error(), service.OrderInteropName,
		"the error has to say which registration could not be found")
}

// TestOrderContactsAFailedResolutionIsNOTPERMANENT verifies that the first
// resolution falling over does not leave the notifications dead for the
// lifetime of the process.
//
// Had sync.Once been used, the RESULT of the first call would be permanent: a
// single event arriving while order was not yet registered would make the
// notification of all subsequent orders impossible as well.
func TestOrderContactsAFailedResolutionIsNOTPERMANENT(t *testing.T) {
	c := container.New(nil)
	reader := service.NewOrderContacts(c)
	ctx := context.Background()

	_, err := reader.OrderContactJSON(ctx, "order_01H")
	require.Error(t, err)

	require.NoError(t, c.Provide(service.OrderInteropName, &fakeContacts{body: testOrderBody}))

	_, err = reader.OrderContactJSON(ctx, "order_01H")
	assert.NoError(t, err, "when the registration arrives later the resolution has to be retried")
}

// TestOrderInteropNameIsTheSameAsTheContract verifies that the container name
// has not drifted from the value repeated by hand.
//
// If the name diverges the module can read no order at all; the compiler cannot
// catch this because the two packages cannot import each other (Principle 2.4).
func TestOrderInteropNameIsTheSameAsTheContract(t *testing.T) {
	assert.Equal(t, "order.interop", service.OrderInteropName)
}
