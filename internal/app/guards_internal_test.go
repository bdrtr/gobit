package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
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
	proof := storeIdempotencyProof(down, nil)

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

	proving := storeIdempotencyProof(&countingIdentity{customer: "cus_1"}, nil)
	withToken, err := proving(r)
	require.NoError(t, err)
	assert.Contains(t, withToken, "ordtok_1")
	assert.Contains(t, withToken, "cus_1")
	nothing, err := storeIdempotencyProof(nil, nil)(httptest.NewRequest(http.MethodPost, "/store/v1/customers", http.NoBody))
	require.NoError(t, err)
	assert.Empty(t, nothing, "a request proving nothing keeps the shared namespace")
}

// TestAPluginsProofHeaderKeysTheStoreRecord is ADR 0444's namespace: the value
// of a header a plugin declared enters the proof, and a request carrying none
// of the declared headers is keyed as it was before plugins could declare one.
func TestAPluginsProofHeaderKeysTheStoreRecord(t *testing.T) {
	const header = "X-Service-Request-Key"
	proof := storeIdempotencyProof(nil, []string{header})
	at := func(set map[string]string) string {
		r := httptest.NewRequest(http.MethodPost, "/store/v1/dtm/service-requests/srq_1/accept", http.NoBody)
		for k, v := range set {
			r.Header.Set(k, v)
		}
		proven, err := proof(r)
		require.NoError(t, err)

		return proven
	}

	assert.Empty(t, at(nil), "a request proving nothing keeps the shared namespace")
	holder, stranger := at(map[string]string{header: "key-a"}), at(map[string]string{header: "key-b"})
	assert.NotEmpty(t, holder, "the declared header is not in the proof")
	assert.NotEqual(t, holder, stranger, "two keys share a namespace")
	assert.Equal(t, "order-token:ordtok_1\x00customer:",
		at(map[string]string{corehttp.OrderTokenHeader: "ordtok_1"}),
		"an order token alone is keyed as before ADR 0444")
	two := storeIdempotencyProof(nil, []string{"X-A-Key", "X-B-Key"})
	under := func(name string) string {
		r := httptest.NewRequest(http.MethodPost, "/store/v1/acme/x", http.NoBody)
		r.Header.Set(name, "v")
		proven, err := two(r)
		require.NoError(t, err)

		return proven
	}
	assert.NotEqual(t, under("X-A-Key"), under("X-B-Key"), "one value under two declared headers shares a namespace")
	assert.Equal(t, "\x00X-A-Key:v", under("X-A-Key"))
	assert.NotEqual(t, at(map[string]string{corehttp.OrderTokenHeader: "ordtok_1"}),
		at(map[string]string{corehttp.OrderTokenHeader: "ordtok_1", header: "key-a"}),
		"the declared header is dropped beside an order token")
}

// TestAPluginsProofHeaderIsCheckedAtStartup holds the composition root's read
// of the declarations: canonical, once and sorted, so the namespace does not
// follow the order plugins were installed in, and a name that is not a header
// stops the installation naming the plugin.
func TestAPluginsProofHeaderIsCheckedAtStartup(t *testing.T) {
	installed := func(t *testing.T, plugins ...coreplugin.Plugin) *coreplugin.Host {
		t.Helper()
		host := coreplugin.NewHost(nil, nil, nil, discardLogger(), nil)
		reg := coreplugin.NewRegistry(discardLogger())
		for _, p := range plugins {
			reg.Add(p)
		}
		require.NoError(t, reg.Install(t.Context(), host))

		return host
	}

	headers, err := pluginProofHeaders(installed(t,
		proofPlugin{name: "b", headers: []string{"x-zeta-key", "X-Alpha-Key"}},
		proofPlugin{name: "a", headers: []string{"X-ZETA-KEY"}}))
	require.NoError(t, err)
	assert.Equal(t, []string{"X-Alpha-Key", "X-Zeta-Key"}, headers)

	for _, bad := range []string{"", "X Key", "X-Key:", "X-Ключ", "X-A,Authorization", "X/A",
		"x-request-id", corehttp.OrderTokenHeader, corehttp.PublishableKeyHeader, corehttp.IdempotencyKeyHeader, "traceparent"} {
		_, err := pluginProofHeaders(installed(t, proofPlugin{name: "acme", headers: []string{bad}}))
		require.Error(t, err, "%q was taken", bad)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
		assert.Contains(t, err.Error(), "acme", "the refusal does not name the plugin")
	}

	none, err := pluginProofHeaders(nil)
	require.NoError(t, err)
	assert.Empty(t, none)
}

// proofPlugin declares storefront proof headers and nothing else.
type proofPlugin struct {
	name    string
	headers []string
}

func (p proofPlugin) Name() string { return p.name }

func (p proofPlugin) Setup(_ context.Context, h *coreplugin.Host) error {
	for _, name := range p.headers {
		h.RegisterStoreProofHeader(name)
	}

	return nil
}
