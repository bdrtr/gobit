package arch_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gapLedgerFile is the defect ledger.
const gapLedgerFile = "docs/gaps.md"

// gapLedgerRow matches a row of the ledger and captures its number.
var gapLedgerRow = regexp.MustCompile(`(?m)^\|\s*D(\d+)\s*\|`)

// gapLedgerFloor is the smallest ledger that could be the real one.
const gapLedgerFloor = 20

// TestTheDefectLedgerNumbersAreUniqueAndDense holds the ledger's own rule.
//
// # Why it exists, and it is a defect of this repository's own making
//
// `docs/gaps.md` says its numbers "never move and a closed row is never
// deleted", because code across the tree cites them as the argument for its own
// shape. Nothing checked it. On 2026-09-09 three rows were appended with the
// numbers D33, D34 and D35 — all three already taken — by someone who read the
// TAIL of the file and assumed it was ordered. It is not: the ledger groups
// rows by subject, so the last row is not the highest number.
//
// The rows were renumbered to D36-D38. The commit messages that introduced them
// still name the old numbers, and that is left standing: a commit message is
// history. This gate is what makes the class impossible rather than unlikely.
//
// # Why DENSE and not merely unique
//
// A skipped number reads exactly like a deleted row, and a deleted row is the
// thing the ledger's first paragraph forbids. Requiring 1..N with nothing
// missing means the two cannot be confused: if a number is absent, either a row
// was removed or one was misnumbered, and both are worth stopping for.
func TestTheDefectLedgerNumbersAreUniqueAndDense(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, gapLedgerFile))
	require.NoErrorf(t, err, "%s could not be read", gapLedgerFile)

	matches := gapLedgerRow.FindAllStringSubmatch(string(raw), -1)
	require.GreaterOrEqualf(t, len(matches), gapLedgerFloor,
		"only %d rows were read out of %s; the row pattern has gone blind, and an empty "+
			"ledger has no duplicates by definition", len(matches), gapLedgerFile)

	seen := map[int][]int{}
	var numbers []int
	for _, match := range matches {
		number, convErr := strconv.Atoi(match[1])
		require.NoErrorf(t, convErr, "D%s is not a number", match[1])
		seen[number] = append(seen[number], number)
		numbers = append(numbers, number)
	}

	var duplicates []string
	for number, rows := range seen {
		if len(rows) > 1 {
			duplicates = append(duplicates, "D"+strconv.Itoa(number))
		}
	}
	slices.Sort(duplicates)
	assert.Emptyf(t, duplicates,
		"the ledger uses %s more than once.\n"+
			"A number is an ADDRESS: files across the tree cite one as the argument for "+
			"their own shape, and two rows answering to it make every such citation "+
			"ambiguous. The ledger is grouped by subject rather than sorted, so the "+
			"highest number is not the last row — count, do not append.",
		strings.Join(duplicates, ", "))

	slices.Sort(numbers)
	var missing []string
	for wanted := 1; wanted <= numbers[len(numbers)-1]; wanted++ {
		if !slices.Contains(numbers, wanted) {
			missing = append(missing, "D"+strconv.Itoa(wanted))
		}
	}
	assert.Emptyf(t, missing,
		"the ledger skips %s.\n"+
			"A missing number reads exactly like a deleted row, and the ledger's own "+
			"first paragraph says a closed row is never deleted.",
		strings.Join(missing, ", "))
}
