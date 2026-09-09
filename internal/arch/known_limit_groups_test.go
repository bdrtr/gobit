package arch_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// knownLimitGroupFloor is the smallest population that could be the real one.
const knownLimitGroupFloor = 2

// TestTheKnownLimitGroupsAreNamedCorrectly holds the half of the claim the
// count gate does not.
//
// # What was already held, and what was not
//
// [TestTheCountsInTheProseAreTrue] prices both READMEs' sentence about
// docs/known-limits.md: so many ITEMS in so many GROUPS, counted from the file.
// Both numbers are real and both are checked.
//
// The same sentence then NAMES the groups, and nothing read the names. The hole
// bit on 2026-09-09: a group was added, the count went from four to five, the
// gate turned green, and the sentence still listed four names. It was caught by
// a person reading the diff.
//
// # What this checks, and in which language
//
// The English README's names must be the file's headings, lower-cased and IN
// ORDER. Order is part of it: a group inserted in the middle and appended to
// the sentence would read as a different document than the one it prices.
//
// The Turkish README is a TRANSLATION, so its names cannot be compared against
// English headings. What is compared is how many it lists — which is the shape
// the defect took, since the count and the list are updated by different edits.
// A mistranslated group name is outside this gate and is written down as such.
//
// # What no gate here can hold
//
// Whether an entry sits under the right heading. The defect that started this
// was exactly that — a tax limit filed under the category tree — and it is a
// judgment about meaning, not a property of the text. The names being right
// does not make the filing right.
func TestTheKnownLimitGroupsAreNamedCorrectly(t *testing.T) {
	headings := knownLimitHeadings(t)
	require.GreaterOrEqualf(t, len(headings), knownLimitGroupFloor,
		"only %d group headings were read out of %s; the reader has gone blind, and an "+
			"empty population is named correctly by definition", len(headings), knownLimitsDoc)

	wanted := make([]string, 0, len(headings))
	for _, heading := range headings {
		wanted = append(wanted, strings.ToLower(heading))
	}

	named := knownLimitGroupsNamedIn(t, "README.en.md")
	assert.Equalf(t, wanted, named,
		"README.en.md names the groups of %s differently than the file has them.\n"+
			"The sentence prices a document, so it has to say what that document holds: the "+
			"same groups, in the same order. Adding a group means editing the count AND the "+
			"list, and the count alone going green is what let this drift once already.",
		knownLimitsDoc)

	// The Turkish README says the same thing in Turkish, so only the SIZE of its
	// list can be compared. Saying so is the point: an unchecked half that is
	// named is one somebody can decide to close.
	turkish := knownLimitGroupsNamedIn(t, "README.md")
	assert.Lenf(t, turkish, len(headings),
		"README.md lists %d group names and %s holds %d groups.\n"+
			"The names themselves are a translation and are not compared here; the COUNT is, "+
			"because the number and the list are updated by two different edits and only one "+
			"of them had a gate.", len(turkish), knownLimitsDoc, len(headings))
}

// knownLimitHeadings returns the group headings of the limits document, in the
// order the document has them.
func knownLimitHeadings(t *testing.T) []string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(repoRoot, knownLimitsDoc))
	require.NoErrorf(t, err, "%s could not be read", knownLimitsDoc)

	var headings []string
	for _, line := range strings.Split(string(raw), "\n") {
		if after, found := strings.CutPrefix(line, "## "); found {
			headings = append(headings, strings.TrimSpace(after))
		}
	}

	return headings
}

// knownLimitGroupsNamedIn returns the group names a README's row for the limits
// document lists after its dash.
//
// The row is found by the document's PATH rather than by a line number or by
// the sentence's wording: a row that moved is still the row, and a sentence
// that was rephrased still prices the same file.
func knownLimitGroupsNamedIn(t *testing.T, readme string) []string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(repoRoot, readme))
	require.NoErrorf(t, err, "%s could not be read", readme)

	var row string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "|") && strings.Contains(line, knownLimitsDoc) &&
			strings.Contains(line, "—") {
			require.Emptyf(t, row, "%s has two rows pricing %s", readme, knownLimitsDoc)
			row = line
		}
	}
	require.NotEmptyf(t, row,
		"%s has no row naming the groups of %s. The sentence is what this gate reads; "+
			"deleting it is a decision that has to be made here rather than by its absence.",
		readme, knownLimitsDoc)

	_, list, found := strings.Cut(row, "—")
	require.Truef(t, found, "the row in %s carries no dash to separate the names", readme)

	list = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(list), "|"))
	names := strings.Split(list, ",")
	for i := range names {
		names[i] = strings.TrimSpace(names[i])
	}
	names = slices.DeleteFunc(names, func(s string) bool { return s == "" })

	require.NotEmptyf(t, names, "%s names no groups at all", readme)

	return names
}
