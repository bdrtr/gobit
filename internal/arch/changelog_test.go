package arch_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheChangelogNamesEveryUnreleasedDecision keeps the release notes from
// falling behind the records.
//
// # What went wrong without it
//
// CHANGELOG.md says of its own Unreleased section that every item "names its
// decision". Nothing asked the other direction — does every decision have an
// item — and by the time anybody counted, fifty-six records had been decided
// and never announced. Each of them was written down correctly in
// docs/adr/; what was missing was the one document a reader of a RELEASE
// opens.
//
// It is D49's shape one document over: a rule that lived in prose while its
// population kept growing.
//
// # How the population is derived
//
// From the two documents and nothing else. The newest released section carries
// a date, every record carries a date, and a record dated on or after it has
// not shipped — unless a released section names it, which is the case for a
// record decided on the morning of the release it went out in. No git command
// and no hand-written floor: a floor is a number that is right until the next
// release and silently wrong after it.
func TestTheChangelogNamesEveryUnreleasedDecision(t *testing.T) {
	t.Parallel()

	changelog := readADR(t, filepath.Join(repoRoot, "CHANGELOG.md"))
	unreleased, released := splitChangelog(t, changelog)

	records := make([]adrRecord, 0, 128)
	for _, path := range adrFiles(t) {
		records = append(records, adrRecord{
			number: filepath.Base(path)[:4],
			date:   adrDate(t, path),
		})
	}

	population, unnamed := unnamedDecisions(records,
		newestReleaseDate(t, released), released, unreleased)

	// A population that comes back empty would make every assertion above
	// vacuous, and the parse is the part most likely to break: one heading
	// rewritten and the section boundary moves. Twenty is far below the count
	// on the day this was written and far above anything a broken parse
	// produces.
	require.Greater(t, len(population), 20,
		"only %d records were found unreleased; the changelog parse has gone BLIND",
		len(population))

	assert.Empty(t, unnamed,
		"these decisions are not named in the Unreleased section of CHANGELOG.md: %s\n"+
			"A record nobody announced is a change that shipped without a line in the "+
			"one document a reader of a release opens. Add an entry naming the decision "+
			"— the reasoning stays in the ADR.", strings.Join(unnamed, ", "))
}

// adrRecord is the two things about a record this audit reads.
type adrRecord struct {
	number string
	date   string
}

// unnamedDecisions returns the records that belong to the unreleased section,
// and those of them it does not name.
//
// It is a pure function so that the RULE can be tried on inputs the tree does
// not happen to contain today — see [TestTheUnreleasedPopulationIsDerivedRight].
// Left inline, the exclusion below would be a branch no mutation of the real
// tree could reach, which is a claim with no proof behind it.
func unnamedDecisions(
	records []adrRecord, cut, released, unreleased string,
) (population, unnamed []string) {
	shipped := adrNumbersIn(released)

	for _, record := range records {
		// A record decided on the morning of the release it went out in is
		// dated on the cut and is NOT unreleased. What settles it is the
		// released section citing it.
		if _, out := shipped[record.number]; out {
			continue
		}
		if record.date < cut {
			continue
		}

		population = append(population, record.number)
		if !strings.Contains(unreleased, "ADR "+record.number) {
			unnamed = append(unnamed, record.number)
		}
	}

	return population, unnamed
}

// TestTheUnreleasedPopulationIsDerivedRight tries the rule on the four inputs
// the real tree cannot all supply at once.
func TestTheUnreleasedPopulationIsDerivedRight(t *testing.T) {
	t.Parallel()

	// The fixtures carry no headings: what the rule reads is the CITATIONS in
	// each part, and a heading here would only be this file repeating the
	// document's prose back at itself.
	const (
		cut        = "2026-09-04"
		released   = "- **A thing** (ADR 0011).\n"
		unreleased = "- **Another thing** (ADR 0022).\n"
	)

	population, unnamed := unnamedDecisions([]adrRecord{
		{number: "0009", date: "2026-09-01"},
		{number: "0011", date: "2026-09-04"},
		{number: "0022", date: "2026-09-05"},
		{number: "0033", date: "2026-09-06"},
	}, cut, released, unreleased)

	assert.Equal(t, []string{"0022", "0033"}, population,
		"a record dated before the cut has shipped, and one the RELEASED section "+
			"cites shipped in it however it is dated")
	assert.Equal(t, []string{"0033"}, unnamed,
		"only the record the unreleased section does not name is reported")
}

// splitChangelog cuts the document into its unreleased part and its released
// part.
//
// The boundary is the first heading naming a version -- "## " then a bracketed
// major.minor.patch -- and everything above it belongs to the release that has
// not happened.
func splitChangelog(t *testing.T, changelog string) (unreleased, released string) {
	t.Helper()

	at := regexp.MustCompile(`(?m)^## \[\d+\.\d+\.\d+\]`).FindStringIndex(changelog)
	require.NotNil(t, at, "CHANGELOG.md holds no released section; the parse has gone BLIND")

	return changelog[:at[0]], changelog[at[0]:]
}

// newestReleaseDate returns the date of the most recent released section.
func newestReleaseDate(t *testing.T, released string) string {
	t.Helper()

	found := regexp.MustCompile(`(?m)^## \[\d+\.\d+\.\d+\] — (\d{4}-\d{2}-\d{2})`).
		FindStringSubmatch(released)
	require.NotNil(t, found,
		"the newest released section carries no date; the parse has gone BLIND")

	return found[1]
}

// adrNumbersIn returns every record number the text cites.
func adrNumbersIn(text string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, m := range regexp.MustCompile(`ADR (\d{4})`).FindAllStringSubmatch(text, -1) {
		out[m[1]] = struct{}{}
	}

	return out
}

// adrDate returns the date a record carries.
func adrDate(t *testing.T, path string) string {
	t.Helper()

	body, err := os.ReadFile(path)
	require.NoError(t, err, "%s could not be read", path)

	found := regexp.MustCompile(`(?m)^- \*\*Date:\*\* (\d{4}-\d{2}-\d{2})`).
		FindStringSubmatch(string(body))
	require.NotNil(t, found, "%s carries no Date; the population cannot be derived", path)

	return found[1]
}
