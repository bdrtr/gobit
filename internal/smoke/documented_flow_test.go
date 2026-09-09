//go:build smoke

package smoke

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// documentedFlowDoc holds the shell block this test executes.
//
// `docs/security.md` is the only document in the tree that walks a reader from
// an empty database to a served storefront page, and every step of it is a
// command somebody pastes.
const documentedFlowDoc = "../../docs/security.md"

// documentedFlowCommands is the smallest number of commands the block can hold
// and still be the flow.
//
// It is a blindness guard rather than a count of the block: a parser that
// stopped seeing would return an empty script, and an empty script runs
// perfectly.
const documentedFlowCommands = 5

// TestTheDocumentedAdminToStorefrontFlowRuns executes the document instead of
// re-implementing it.
//
// # What is already covered, and what is not
//
// `TestTheRouteAddressesInTheProseExist` checks every route address a document
// names, WITH ITS VERB, so a curl pointing at a path that moved is already
// caught. What no gate could see is everything else the command carries: the
// name of a header, the field name in a body, and the jq path pulled out of a
// response. ADR 0044 records the shape this takes — "its copy-pasteable curl
// 404ed" — and the flow here is exactly where it hurts, because each step feeds
// the next: a renamed field makes $TOKEN the string "null" and every step after
// it answers unauthorized.
//
// # Why the document is executed rather than reproduced
//
// A Go re-implementation of these six commands would be a second copy of the
// flow, free to stay right while the document went wrong — which is the defect,
// not the fix. The block is read out of the file and handed to a shell, so what
// runs is what a reader pastes.
//
// # What is substituted, and why each one
//
// Three things, and nothing else. The address, because the document names the
// default port and the smoke harness picks a free one. The credentials, because
// the document writes a placeholder where a password belongs. And step 0's
// `make run` is dropped, because the harness has already started the very
// process that line asks the reader to start.
func TestTheDocumentedAdminToStorefrontFlowRuns(t *testing.T) {
	commands := documentedShellCommands(t, documentedFlowDoc)
	require.GreaterOrEqualf(t, len(commands), documentedFlowCommands,
		"only %d commands were read out of %s; the block parser has gone blind, and an "+
			"empty script passes every assertion below", len(commands), documentedFlowDoc)

	cfg := baseSettings(scenarioDatabase(t), freePort(t))
	cfg["ADMIN_BOOTSTRAP_EMAIL"] = seedEmail
	cfg["ADMIN_BOOTSTRAP_PASSWORD"] = seedPassword

	s := startServer(t, cfg)
	s.waitForReady(startupTimeout)

	script := strings.Join(commands, "\n")
	script = strings.ReplaceAll(script, "localhost:9000", strings.TrimPrefix(s.addr, "http://"))
	script = strings.ReplaceAll(script, "admin@example.com", seedEmail)
	script = strings.ReplaceAll(script, "'…'", "'"+seedPassword+"'")
	script = strings.ReplaceAll(script, "\"password\":\"…\"", "\"password\":\""+seedPassword+"\"")

	out := runDocumentedScript(t, script)

	// The block prints exactly three times: the three commands whose output is
	// not captured into a variable. Reading them BY POSITION is what makes each
	// link of the chain answerable on its own — a break anywhere turns the line
	// after it into an error envelope.
	printed := nonEmptyLines(out)
	require.Lenf(t, printed, 3,
		"the documented block printed %d lines and three were expected: the two captured "+
			"steps print nothing, so a different count means the block itself has changed "+
			"shape and this test is reading the wrong lines.\n--- output ---\n%s",
		len(printed), out)

	assert.Containsf(t, printed[0], `"kind":"user"`,
		"step 2 did not reach an authenticated answer.\n"+
			"Step 1 pulls the token out of the login response with a jq path and step 2 "+
			"sends it in a header; if either name has moved, $TOKEN is the string \"null\" "+
			"and this is what a reader pasting the document sees.\n--- line ---\n%s", printed[0])

	// The catalog is EMPTY and that is not the point: what the envelope proves is
	// that the publishable key was created bound to the channel and accepted on
	// the address the document builds out of $SC.
	assert.Containsf(t, printed[1], `"count"`,
		"step 4 did not reach the storefront.\n"+
			"Either the key header's name has moved, or the creation body's channel field "+
			"has, in which case the key came out unbound and the store surface answers "+
			"auth_no_sales_channel.\n--- line ---\n%s", printed[1])
	assert.NotContainsf(t, printed[1], "auth_no_sales_channel",
		"the publishable key came out unbound: the creation body's channel field has "+
			"moved and the document still names the old one.\n--- line ---\n%s", printed[1])

	assert.Containsf(t, printed[2], `"all_sessions"`,
		"step 5 did not log out; the token the document carries no longer opens the "+
			"logout endpoint.\n--- line ---\n%s", printed[2])
}

// documentedShellCommands returns the commands of the document's first bash
// block, with continuations joined and `make` invocations dropped.
func documentedShellCommands(t *testing.T, path string) []string {
	t.Helper()

	raw, err := os.ReadFile(path)
	require.NoErrorf(t, err, "%s could not be read", path)

	lines := strings.Split(string(raw), "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "```bash" {
			start = i + 1

			break
		}
	}
	require.GreaterOrEqualf(t, start, 0, "%s holds no bash block", path)

	var commands []string
	var current strings.Builder
	for _, line := range lines[start:] {
		if strings.TrimSpace(line) == "```" {
			break
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		current.WriteString(line)
		if strings.HasSuffix(trimmed, "\\") {
			current.WriteString("\n")

			continue
		}

		command := current.String()
		current.Reset()

		// Step 0 starts the server the harness has already started. It is the
		// one command that cannot run here, and dropping it is named in the
		// test's own godoc rather than left for a reader to notice.
		if strings.Contains(command, "make run") {
			continue
		}
		commands = append(commands, command)
	}

	return commands
}

// nonEmptyLines returns the script's output split into the lines it printed.
func nonEmptyLines(out string) []string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}

	return lines
}

// runDocumentedScript runs the script with sh and returns everything it printed.
//
// The exit status is not asserted on: curl answers 0 for an HTTP error unless it
// is asked otherwise, and the document does not ask. What the flow produces is
// therefore read out of the OUTPUT, which is also what a reader looks at.
func runDocumentedScript(t *testing.T, script string) string {
	t.Helper()

	for _, tool := range []string{"curl", "jq"} {
		_, err := exec.LookPath(tool)
		require.NoErrorf(t, err,
			"%s is not on PATH. The documents are written to be pasted into a shell and "+
				"this test runs them; both READMEs already list curl and jq among the "+
				"tools this repository expects.", tool)
	}

	cmd := exec.CommandContext(t.Context(), "sh", "-c", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("the documented script exited with %v", err)
	}

	return string(out)
}
