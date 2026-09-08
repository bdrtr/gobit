package arch_test

import (
	"maps"
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

// measurementsDir holds the evidence the records lean on.
const measurementsDir = "docs/measurements"

// measurementIndex is the file that says what is in there.
const measurementIndex = measurementsDir + "/README.md"

// measurementIndexRow reads one row of the index: the file it links to and the
// number in the column after it.
//
// The row is matched on the LINK rather than on the position of a pipe, because
// a table whose columns are re-ordered is still the same table and this audit
// should survive that. What it cannot survive is the link being written some
// other way, which is why the blindness control below plants one.
var measurementIndexRow = regexp.MustCompile(`\]\(([A-Za-z0-9][^)]*\.md)\)\s*\|\s*(\d+)\s*\|`)

// TestTheMeasurementIndexLineCountsAreTrue holds a number the prose states
// against the number the tree computes.
//
// # Why this one is gated when ADR 0070 declined to gate it
//
// That record's Rejected section names this column and hands it to the owner:
// it "prices a document's LENGTH rather than a population, so it goes stale on
// the same commit that corrects any report". Every word of that is true, and on
// re-reading it is an argument FOR a gate rather than against one. A number that
// goes stale on the next commit is exactly the number that needs something to
// notice; the reason it was left out of [TestTheCountsInTheProseAreTrue] is that
// the count vocabulary is built on POPULATIONS the tree sizes for itself, and a
// file's length is not one — which is a fact about that gate's shape, not about
// whether this number should be true.
//
// It was measured before it was decided: eighteen of the thirty-four rows were
// wrong, most by exactly four lines — the header block added when the reports
// moved out of `docs/gaps.md` and never carried back — and one by 189.
//
// # What it costs
//
// One line of the index has to move whenever a report grows, in the same commit.
// That is the whole cost and it is the point: the alternative measured out at
// eighteen wrong rows in a document whose entire job is to tell a reader what
// they are about to open.
func TestTheMeasurementIndexLineCountsAreTrue(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(filepath.Join(repoRoot, measurementIndex))
	require.NoError(t, err, "%s could not be read", measurementIndex)

	rows := measurementIndexRow.FindAllStringSubmatch(string(body), -1)
	require.NotEmpty(t, rows,
		"no row of %s was read at all; the reader has gone blind and an empty reading "+
			"agrees with every number in the file", measurementIndex)

	for _, row := range rows {
		file, claimed := row[1], row[2]

		lines, err := measurementLineCount(file)
		require.NoError(t, err,
			"%s links to %q and there is no such report; a reader following it lands "+
				"nowhere", measurementIndex, file)

		stated, err := strconv.Atoi(claimed)
		require.NoError(t, err)

		assert.Equal(t, lines, stated,
			"%s says %s is %d lines and it is %d.\n"+
				"The column exists to tell a reader what they are about to open, and a "+
				"report that grew without the row moving tells them the wrong thing. "+
				"Correct the row in the commit that changed the report.",
			measurementIndex, file, stated, lines)
	}
}

// TestTheMeasurementIndexNamesEveryReport is the other direction, and it is the
// one that matters more.
//
// A wrong number is a small lie. A report nobody indexed is evidence that exists
// and is not reachable: the ADRs link to their own measurement, so the file is
// not lost, but the index is what somebody reads when they are looking for
// evidence they cannot name yet.
func TestTheMeasurementIndexNamesEveryReport(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(filepath.Join(repoRoot, measurementIndex))
	require.NoError(t, err, "%s could not be read", measurementIndex)

	indexed := map[string]bool{}
	for _, row := range measurementIndexRow.FindAllStringSubmatch(string(body), -1) {
		indexed[row[1]] = true
	}

	entries, err := os.ReadDir(filepath.Join(repoRoot, measurementsDir))
	require.NoError(t, err, "%s could not be read", measurementsDir)

	reports := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".md") || name == "README.md" {
			continue
		}

		reports++
		assert.True(t, indexed[name],
			"%s is in %s and in no row of %s; the index is what somebody reads when "+
				"they are looking for evidence they cannot name yet",
			name, measurementsDir, measurementIndex)
	}

	require.Positive(t, reports,
		"there is no report under %s at all; both checks in this file are running on an "+
			"empty set and passing having found nothing", measurementsDir)
}

// TestTheMeasurementIndexReaderIsNotBlind is the positive control.
//
// Both checks above are only as good as one regular expression, and a reader
// that matches nothing reports a perfect index: no row is wrong, and every
// report is in a set that contains everything because it was never narrowed.
func TestTheMeasurementIndexReaderIsNotBlind(t *testing.T) {
	t.Parallel()

	const planted = "" +
		"| [A report](0001-a-report.md) | 42 |\n" +
		"| [Another](ai-subsystem.md) | 7 |\n" +
		"| [Re-aligned](0002-second.md)   |   115   |\n" +
		"| [Not a report](../adr/0001-something.md) | 9 |\n" +
		"| a row with no link at all | 3 |\n"

	got := map[string]string{}
	for _, row := range measurementIndexRow.FindAllStringSubmatch(planted, -1) {
		got[row[1]] = row[2]
	}

	for file, want := range map[string]string{
		"0001-a-report.md": "42",
		"ai-subsystem.md":  "7",
		"0002-second.md":   "115",
	} {
		assert.Equal(t, want, got[file],
			"the reader missed the %q row; every row of that shape is outside both "+
				"checks and its number is free to rot", file)
	}

	assert.NotContains(t, slices.Sorted(maps.Keys(got)), "../adr/0001-something.md",
		"the reader took a link OUT of the measurements directory for a report row")
}

// measurementLineCount counts the lines of one report.
func measurementLineCount(file string) (int, error) {
	body, err := os.ReadFile(filepath.Join(repoRoot, measurementsDir, file))
	if err != nil {
		return 0, err
	}

	// The count is what `wc -l` reports: the number of NEWLINES. A file whose
	// last line has no terminator therefore counts one fewer, which is the same
	// answer anybody checking this by hand would get.
	return strings.Count(string(body), "\n"), nil
}
