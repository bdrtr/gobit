package app

import (
	"testing"

	"github.com/stretchr/testify/assert"

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
