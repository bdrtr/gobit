package arch_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	// shellBlock matches a fenced bash block in a markdown document.
	shellBlock = regexp.MustCompile("(?s)```(?:bash|sh)\n(.*?)```")
	// shellAssignment matches a shell variable being given a value.
	shellAssignment = regexp.MustCompile(`(?m)^([A-Z_][A-Z0-9_]*)=`)
	// shellExpansion matches a shell variable being read.
	shellExpansion = regexp.MustCompile(`\$\{?([A-Z_][A-Z0-9_]*)\}?`)
)

// chainedFlowWitnesses names, for every document that walks a reader through a
// CHAIN of requests, the test that executes it.
//
// The keys are derived from the documents, so a second flow arrives here as a
// failure rather than as silence.
var chainedFlowWitnesses = map[string]string{
	"docs/security.md":  "TestTheDocumentedAdminToStorefrontFlowRuns",
	"docs/first-run.md": "TestTheDocumentedFirstRunReachesAnOrder",
}

// TestEveryChainedCurlFlowIsExecuted holds the one class of documented command
// that no other gate can see.
//
// # What is already covered
//
// [TestTheRouteAddressesInTheProseExist] checks every route address a document
// names, with its verb. A curl pointing at a path that moved is caught there,
// and that covers the six documents whose blocks hold STANDALONE commands.
//
// # What it cannot see, and why a chain is the place it hurts
//
// A command carries more than its address: the name of a header, the field name
// in a body, the jq path pulled out of a response. None of those is a route and
// none can be resolved against the router. In a chain they are load-bearing —
// one step's output is the next step's input — so a single renamed field turns
// every step after it into an unauthorized answer, and the document keeps
// looking right. ADR 0044 recorded the shape this takes: "its copy-pasteable
// curl 404ed".
//
// So the rule is drawn where the cost is: a block that feeds a variable from one
// request into another has to be EXECUTED by a test, not re-implemented beside
// it. Measured 2026-09-09, exactly one block in the tree did that; the second
// arrived on 2026-09-12 (ADR 0158) and arrived AS A FAILURE HERE, which is what
// the derived keys are for. The map still needs no exemptions.
func TestEveryChainedCurlFlowIsExecuted(t *testing.T) {
	chained := chainedFlowDocuments(t)

	require.NotEmptyf(t, chained,
		"no chained command block was found in any document; the block scanner has gone "+
			"blind, and an empty population is executed by definition")

	names := testFunctionNames(t)

	for _, doc := range chained {
		witness, named := chainedFlowWitnesses[doc]
		assert.Truef(t, named,
			"%s walks a reader through a chain of requests and no test executes it.\n"+
				"A chain is where a renamed header or body field stops being visible: the "+
				"route gate resolves the addresses and nothing resolves the rest.", doc)
		if !named {
			continue
		}

		assert.Containsf(t, names, witness,
			"the witness named for %s, %s, is not in the tree", doc, witness)
	}

	for doc := range chainedFlowWitnesses {
		assert.Containsf(t, chained, doc,
			"a witness is named for %s, which no longer holds a chained block; a witness "+
				"for a flow that is gone hides the next document that grows one", doc)
	}
}

// chainedFlowDocuments returns the documents holding a command block in which a
// variable set by one command is read by a later one.
func chainedFlowDocuments(t *testing.T) []string {
	t.Helper()

	var found []string

	for _, doc := range markdownFiles(t) {
		raw, err := os.ReadFile(filepath.Join(repoRoot, doc))
		require.NoErrorf(t, err, "%s could not be read", doc)

		for _, block := range shellBlock.FindAllStringSubmatch(string(raw), -1) {
			body := block[1]
			if !strings.Contains(body, "curl ") {
				continue
			}

			assigned := map[string]bool{}
			for _, match := range shellAssignment.FindAllStringSubmatch(body, -1) {
				assigned[match[1]] = true
			}
			for _, match := range shellExpansion.FindAllStringSubmatch(body, -1) {
				if assigned[match[1]] && !slices.Contains(found, doc) {
					found = append(found, doc)
				}
			}
		}
	}

	slices.Sort(found)

	return found
}

// markdownFiles returns every markdown document in the tree except the
// changelog.
//
// The changelog is out of scope for the reason the route gate gives for the
// dated records: it reports what a command looked like on the day it was
// written, and correcting it would be rewriting history rather than fixing a
// document somebody follows.
func markdownFiles(t *testing.T) []string {
	t.Helper()

	var docs []string
	err := filepath.WalkDir(repoRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && slices.Contains(skippedDirs, entry.Name()) {
			return filepath.SkipDir
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		relative := filepath.ToSlash(strings.TrimPrefix(filepath.ToSlash(path), repoRoot+"/"))
		if relative == "CHANGELOG.md" {
			return nil
		}
		docs = append(docs, relative)

		return nil
	})
	require.NoError(t, err, "the tree could not be walked for documents")
	slices.Sort(docs)

	return docs
}
