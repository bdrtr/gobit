package arch_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gracefulStopChannel is the upstream field this repository must not send on.
const gracefulStopChannel = "GracefulStop"

// TestNothingSendsOnGracefulStop keeps ADR 0052's one-line decision.
//
// # What the line costs when it comes back
//
// golang-migrate v4.19.1 guards the migrator's isLocked field with a mutex and
// leaves isGracefulStop, declared on the next line, guarded by nothing. Its
// stop helper both reads and writes that field, and the Up entry point calls
// stop from BOTH goroutines it runs — the reader on a new one, the runner on
// the caller's — with no happens-before edge between them.
//
// The write only ever executes if something sends on the channel. Nothing does
// any more, so the race is dormant rather than fixed: the field is unexported
// and cannot be synchronized from outside, which is why NOT ARMING IT is the
// whole of the remedy and why one line restores the fault.
//
// The fault it restores is not a wrong answer. It is a DATA RACE, so under
// -race it fails the run rather than the assertion — which is how it arrived,
// as one red integration lane on 2026-09-08 that never reproduced (D31).
//
// # When to delete this gate
//
// When golang-migrate guards the field. At that point the send becomes a free
// belt-and-braces layer again and ADR 0003's original argument for it stands
// up on its own; ADR 0052 says so in its own Consequences.
func TestNothingSendsOnGracefulStop(t *testing.T) {
	t.Parallel()

	// The trees overlap: productionTrees carries the repository root as well as
	// the subtrees below it, so a file reached twice would be REPORTED twice
	// and the floor below would count it twice. Measured while writing this —
	// the mutation named one line in two messages.
	seen := map[string]bool{}
	scanned := 0
	for _, tree := range productionTrees {
		for _, file := range productionFiles(t, filepath.Join(repoRoot, tree)) {
			if !strings.HasSuffix(file, ".go") || seen[file] {
				continue
			}
			seen[file] = true
			scanned++
			raw, err := os.ReadFile(file)
			require.NoError(t, err, "%s could not be read", file)
			body := string(raw)
			for i, line := range strings.Split(body, "\n") {
				trimmed := strings.TrimSpace(line)
				// A comment naming the channel is how the decision is
				// EXPLAINED; only a send arms anything.
				if strings.HasPrefix(trimmed, "//") {
					continue
				}
				if !strings.Contains(line, gracefulStopChannel) || !strings.Contains(line, "<-") {
					continue
				}
				assert.Fail(t, "a send on the migrator's GracefulStop channel is back",
					"%s:%d: %s\n"+
						"That one line arms an unsynchronized write in golang-migrate and buys "+
						"nothing this repository can observe — measured both ways on 2026-09-08: "+
						"same error code, same version, same dirty flag, same regression test "+
						"(ADR 0052). Closing the connection is what stops the run.",
					strings.TrimPrefix(filepath.ToSlash(file), filepath.ToSlash(repoRoot)+"/"),
					i+1, trimmed)
			}
		}
	}

	require.Positive(t, scanned,
		"no Go file was scanned at all; the walk has gone BLIND and would approve the send "+
			"it exists to refuse")
}
