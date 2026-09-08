package arch_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file holds the ADR shape rule from CLAUDE.md: four sections, eighty
// lines, and no measurements.
//
// # Why a gate and not a habit
//
// The rule exists because the records grew until nobody read them. Fifty-one
// ADRs came to 11,934 lines, an average of 234 and a longest of 569, and the
// growth was never a decision — every individual paragraph was worth writing
// at the moment it was written. That is exactly the shape a habit cannot hold
// and a number can.
//
// # Why it starts at 0052
//
// The records below it are the historical account of how this repository was
// built, and rewriting them would destroy the thing they are for. They carry a
// two-line Summary instead. A ratchet that applies from the next record is the
// same instrument the language ledger uses (ADR 0012), for the same reason: the
// debt is bounded and the rule is real from today rather than after a cleanup
// nobody schedules.

// adrLineLimit is the most lines an ADR may hold.
//
// Eighty is not a measured optimum. It is a number small enough that a
// measurement cannot fit inside it, which is the actual rule — the size cap is
// what makes "measurements go to docs/measurements" enforceable, because a
// record that keeps its tables cannot stay under it.
const adrLineLimit = 80

// firstGovernedADR is the lowest number the shape rule applies to.
const firstGovernedADR = 52

// adrSections are the four headings a governed ADR may carry, in this order.
var adrSections = []string{"## Context", "## Decision", "## Consequences", "## Rejected"}

// adrDir is the directory the records live in, split so that filepath.Join
// receives one segment at a time.
var adrDir = []string{"docs", "adr"}

// adrNumber matches the number at the head of an ADR's filename.
var adrNumber = regexp.MustCompile(`^(\d{4})-`)

// TestAGovernedADRFitsInEightyLines is the size half of the rule.
func TestAGovernedADRFitsInEightyLines(t *testing.T) {
	t.Parallel()

	governed := governedADRs(t)
	for _, path := range governed {
		lines := strings.Count(readADR(t, path), "\n")
		assert.LessOrEqual(t, lines, adrLineLimit,
			"%s is %d lines and the limit is %d.\n"+
				"If the record does not fit, the usual reason is that it is carrying "+
				"evidence: numbers, tables, probe output and reproductions go to "+
				"docs/measurements/ and the ADR links to them in one line. The other "+
				"reason is that the decision has not been made yet, and what is missing "+
				"is the decision rather than the room.",
			short(path), lines, adrLineLimit)
	}
}

// TestAGovernedADRCarriesTheFourSections is the shape half.
//
// The order is checked as well as the membership. A record whose Consequences
// come before its Decision reads as an argument working towards a conclusion,
// which is the register this rule exists to stop.
func TestAGovernedADRCarriesTheFourSections(t *testing.T) {
	t.Parallel()

	for _, path := range governedADRs(t) {
		var found []string
		for _, line := range strings.Split(readADR(t, path), "\n") {
			if strings.HasPrefix(line, "## ") {
				found = append(found, strings.TrimRight(line, " "))
			}
		}

		assert.Equal(t, adrSections, found,
			"%s does not carry the four sections in order.\n"+
				"An ADR is Context, Decision, Consequences, Rejected — and nothing else. "+
				"A fifth heading is almost always one of the three things that moved out: "+
				"a measurement, the opposition narrative, or a later amendment that "+
				"belongs in its own record.",
			short(path))
	}
}

// TestTheADRIndexNamesEveryRecord keeps docs/adr/README.md honest.
//
// The index is the entry point, so a record missing from it is a record nobody
// finds. The check runs BOTH ways: an unlisted file and a listed file that does
// not exist are the same defect seen from two sides.
func TestTheADRIndexNamesEveryRecord(t *testing.T) {
	t.Parallel()

	index := readADR(t, filepath.Join(append([]string{repoRoot}, append(adrDir, "README.md")...)...))
	files := adrFiles(t)
	require.NotEmpty(t, files, "no ADR was found at all; the scan has gone BLIND")

	for _, path := range files {
		assert.Contains(t, index, "("+filepath.Base(path)+")",
			"%s is not in docs/adr/README.md. The index is where a reader starts, "+
				"so a record it does not name is a record nobody finds.", short(path))
	}

	for _, link := range regexp.MustCompile(`\((\d{4}-[a-z0-9-]+\.md)\)`).FindAllStringSubmatch(index, -1) {
		_, err := os.Stat(filepath.Join(append([]string{repoRoot}, append(adrDir, link[1])...)...))
		assert.NoError(t, err, "docs/adr/README.md names %s, which does not exist", link[1])
	}
}

// TestEveryADRCarriesASummary checks the two-line summary every record takes.
//
// It applies to ALL of them, governed or not: the summary is what makes an
// unbounded historical record usable without reading it, and it is the only
// edit 0001-0051 were given.
func TestEveryADRCarriesASummary(t *testing.T) {
	t.Parallel()

	for _, path := range adrFiles(t) {
		head := strings.SplitN(readADR(t, path), "\n", 8)
		assert.True(t, slicesContainsPrefix(head, "**Summary:**"),
			"%s has no **Summary:** in its first lines.\n"+
				"Two sentences: what was decided, and what it costs. It is what lets a "+
				"reader skip the record rather than skim it.", short(path))
	}
}

// governedADRs returns the ADRs the shape rule applies to.
func governedADRs(t *testing.T) []string {
	t.Helper()

	var out []string
	for _, path := range adrFiles(t) {
		match := adrNumber.FindStringSubmatch(filepath.Base(path))
		if match == nil {
			continue
		}
		if n, err := strconv.Atoi(match[1]); err == nil && n >= firstGovernedADR {
			out = append(out, path)
		}
	}

	return out
}

// adrFiles returns every ADR, excluding the index.
func adrFiles(t *testing.T) []string {
	t.Helper()

	found, err := filepath.Glob(filepath.Join(append([]string{repoRoot}, append(adrDir, "*.md")...)...))
	require.NoError(t, err, "the ADR directory could not be read")

	var out []string
	for _, path := range found {
		if filepath.Base(path) != "README.md" {
			out = append(out, path)
		}
	}

	return out
}

// readADR reads a document and fails the test if it cannot.
func readADR(t *testing.T, path string) string {
	t.Helper()

	body, err := os.ReadFile(path)
	require.NoError(t, err, "%s could not be read", path)

	return string(body)
}

// short is a path relative to the repository root, for a message.
func short(path string) string {
	return strings.TrimPrefix(filepath.ToSlash(path), filepath.ToSlash(repoRoot)+"/")
}

// slicesContainsPrefix reports whether any line starts with the prefix.
func slicesContainsPrefix(lines []string, prefix string) bool {
	for _, line := range lines {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}

	return false
}
