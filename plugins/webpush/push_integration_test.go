//go:build integration

package webpush

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAPushReachesOnlyTheNamedCustomersDevices is ADR 0446 on a real table:
// every device of each named customer is tried, a gone one is removed, and
// neither a device bound to nobody nor another customer's hears it.
func TestAPushReachesOnlyTheNamedCustomersDevices(t *testing.T) {
	st := freshStore(t)
	live := newPushServer(t, http.StatusCreated)
	gone := newPushServer(t, http.StatusGone)
	other := newPushServer(t, http.StatusCreated)
	m := newTestModule(t, st, live, gone, other)

	device(t, st, live.URL+"/p/1", "cust_1", m.opts.fingerprint)
	device(t, st, live.URL+"/p/2", "cust_1", m.opts.fingerprint)
	device(t, st, gone.URL+"/p/3", "cust_2", m.opts.fingerprint)
	device(t, st, other.URL+"/p/4", "", m.opts.fingerprint)
	device(t, st, other.URL+"/p/5", "cust_3", m.opts.fingerprint)

	pusher := &Pusher{m: m}
	result, err := pusher.PushToCustomers(t.Context(), []string{"cust_1", "cust_2", ""},
		Push{Title: "Sale", Body: "Today only", Tag: "campaign-1"})
	require.NoError(t, err)

	assert.Equal(t, PushResult{Devices: 3, Delivered: 2, Removed: 1}, result)
	assert.Len(t, live.received(), 2)
	assert.Empty(t, other.received(), "a device bound to nobody, or to a customer not named, hears nothing")
	assert.Equal(t, 4, countRows(t), "the gone device is removed and no other")
	for _, r := range live.received() {
		assert.NotEmpty(t, r.Header.Get("Topic"), "a tagged push names its topic")
	}

	_, err = pusher.PushToCustomer(t.Context(), "cust_1", Push{Title: "Sale", Body: "Again"})
	require.NoError(t, err)
	assert.Empty(t, live.received()[2].Header.Get("Topic"), "an untagged push replaces nothing")
}
