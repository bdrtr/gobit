package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/core/config"
)

// TestTheAdminPrefixTheCompositionRootPinsIsTheCoresDefault ties two constants
// that nothing compiles together.
//
// The panel's review shell hands its client [corehttp.DefaultAdminPrefix] as the
// API to call, and the guard stack scopes both the session ring and RequireAdmin
// to this package's own adminPrefix. Those are two spellings of one path. If they
// drift, the screen calls an address the guards do not cover: the request arrives
// unauthenticated, the operator sees an empty moderation queue, and nothing
// fails — the panel is a 200 with no rows, which is the one answer that feature
// must never give wrongly.
//
// It is asserted rather than assumed because the drift is FREE: both sides
// compile, both suites pass, and the two files are far apart.
func TestTheAdminPrefixTheCompositionRootPinsIsTheCoresDefault(t *testing.T) {
	t.Parallel()

	assert.Equal(t, corehttp.DefaultAdminPrefix, adminPrefix,
		"the composition root mounts the admin surface somewhere the panel's review "+
			"client does not call")

	// And the fallback really is that value, so a caller leaving AdminPrefix
	// empty gets the same path rather than a middleware scoped to "" — which
	// would match every request in the tree, storefront included.
	assert.Equal(t, corehttp.DefaultAdminPrefix, adminPrefixOf(corehttp.GuardOptions{}))
	assert.Equal(t, "/elsewhere",
		adminPrefixOf(corehttp.GuardOptions{AdminPrefix: "/elsewhere"}))
}

// TestTheOneAuditedReadIsTheAuditLogsOwnListing pins the composition root's list
// of recorded reads (ADR 0037, D259).
//
// The core records a read of every path it is handed, and its own tests hand it
// paths of their own, so nothing there reads this list. Deleting it leaves
// "reading the audit log is itself recorded" untrue with every lane green, and
// widening it fills the log with listings nobody asked about. Both are held
// here, on the options every backend starts from.
func TestTheOneAuditedReadIsTheAuditLogsOwnListing(t *testing.T) {
	t.Parallel()

	opts := guardOptions(config.Config{}, nil, nil, discardLogger())

	assert.Equal(t, []string{auditLogPath}, opts.AuditedReads,
		"the installation records a read of the audit log's own listing and of nothing else")
}

// countingIdentity proves its customer, or fails as an outage does, and counts
// how many times it was asked.
type countingIdentity struct {
	customer string
	err      error
	asked    int
}

func (c *countingIdentity) CustomerID(*http.Request) (string, error) {
	c.asked++

	return c.customer, c.err
}

// TestTheStoreProofAsksTheIdentityOnlyOffACart holds what the store ring keys a
// record on (ADR 0439): a cart's routes ask nothing, so their records are keyed
// as they always were and an outage does not reach them; any other keyed write
// names its token and its proven customer, and an identity that cannot answer
// refuses it.
func TestTheStoreProofAsksTheIdentityOnlyOffACart(t *testing.T) {
	down := &countingIdentity{err: errors.Unavailable("session_store_down", "down")}
	proof := storeIdempotencyProof(down)

	for _, path := range []string{"/store/v1/carts/cart_1/line-items", "/store/v1/carts/cart_1/complete"} {
		r := httptest.NewRequest(http.MethodPost, path, http.NoBody)
		r.Header.Set(corehttp.OrderTokenHeader, "ordtok_1")
		proven, err := proof(r)
		require.NoError(t, err, path)
		assert.Empty(t, proven, "%s is keyed on more than its path", path)
	}
	assert.Zero(t, down.asked, "a cart's route asked the identity")

	r := httptest.NewRequest(http.MethodPost, "/store/v1/orders/order_1/returns", http.NoBody)
	r.Header.Set(corehttp.OrderTokenHeader, "ordtok_1")
	_, err := proof(r)
	require.Error(t, err, "an outage was not refused")
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err))

	proving := storeIdempotencyProof(&countingIdentity{customer: "cus_1"})
	withToken, err := proving(r)
	require.NoError(t, err)
	assert.Contains(t, withToken, "ordtok_1")
	assert.Contains(t, withToken, "cus_1")
	nothing, err := storeIdempotencyProof(nil)(httptest.NewRequest(http.MethodPost, "/store/v1/customers", http.NoBody))
	require.NoError(t, err)
	assert.Empty(t, nothing, "a request proving nothing keeps the shared namespace")
}
