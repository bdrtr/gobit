package graph_test

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// calibrationCopies are the published copies of the calibration table, each
// row named by its label there: the copies word the documents for their reader,
// so the label is mapped by hand rather than matched.
var calibrationCopies = []struct {
	file string
	// row reads a line of the file into its label and complexity column, and
	// reports whether the line is a row at all.
	row    func(line string) (label, complexity string, ok bool)
	labels map[string]string
}{
	{
		file: "limits.go",
		row:  godocRow,
		labels: map[string]string{
			"product page (PDP, everything included)":                   "product page (PDP, everything included)",
			"product page with its three related lists":                 "product page with its three related lists",
			"category list (24 products, card fields + price)":          "category list (24 products, card + price)",
			"ALL fields on the default page (20 products x whole tree)": "ALL fields on the default page (20 products)",
			"ALL fields with limit=100":                                 "ALL fields with limit=100",
			"related on every product of a page of 50":                  "related on every product of a page of 50",
			"a chain of three related lists":                            "a chain of three related lists",
			"products { count } with 400 aliases":                       "products { count } with 400 aliases",
			"description with 489 aliases (limit=100)":                  "description with 489 aliases (limit=100)",
			"description with 1500 aliases (default page)":              "description with 1500 aliases (20 products)",
		},
	},
	{
		file: "../../../../docs/api-surfaces.md",
		row:  markdownRow,
		labels: map[string]string{
			"product page (PDP, everything included)":                   "product page (PDP, everything included)",
			"product page with its three related lists":                 "product page with its three `related` lists (four cards each)",
			"category list (24 products, card fields + price)":          "category list (24 products, card + price)",
			"ALL fields on the default page (20 products x whole tree)": "ALL fields on the default page (20 products x whole tree)",
			"ALL fields with limit=100":                                 "ALL fields with `limit=100`",
			"related on every product of a page of 50":                  "`related` on every product of a page of 50",
			"a chain of three related lists":                            "a chain of three `related` lists",
			"products { count } with 400 aliases":                       "`products { count }` with 400 aliases",
			"description with 489 aliases (limit=100)":                  "`description` with 489 aliases, `limit=100`",
			"description with 1500 aliases (default page)":              "`description` with 1500 aliases, default page",
		},
	},
}

// godocColumns splits a row of the godoc's table: columns are two or more
// spaces apart.
var godocColumns = regexp.MustCompile(`\s{2,}`)

// godocRow reads a row of the table in the DefaultMaxComplexity godoc:
// "//<tab>label  request  complexity  response".
func godocRow(line string) (label, complexity string, ok bool) {
	body, found := strings.CutPrefix(strings.TrimSpace(line), "//\t")
	if !found {
		return "", "", false
	}
	columns := godocColumns.Split(strings.TrimSpace(body), -1)
	if len(columns) != 4 {
		return "", "", false
	}
	return columns[0], columns[2], true
}

// markdownRow reads a row of the table in docs/api-surfaces.md:
// "| label | request | complexity | response | outcome |", bold taken off.
func markdownRow(line string) (label, complexity string, ok bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "|") {
		return "", "", false
	}
	columns := strings.Split(strings.Trim(line, "|"), "|")
	if len(columns) != 5 {
		return "", "", false
	}
	clean := func(s string) string { return strings.Trim(strings.TrimSpace(s), "*") }
	return clean(columns[0]), clean(columns[2]), true
}

// TestTheComplexityTableCopiesMatchThePinnedOne is D158: the DefaultMaxComplexity
// godoc and docs/api-surfaces.md repeat the calibration table, and ADR 0219
// moved four of its rows while both copies kept the numbers before it. Every
// document of calibrationDocuments has to be a row of every copy, carrying the
// complexity the table pins.
func TestTheComplexityTableCopiesMatchThePinnedOne(t *testing.T) {
	t.Parallel()

	for _, c := range calibrationCopies {
		t.Run(c.file, func(t *testing.T) {
			t.Parallel()

			text, err := os.ReadFile(c.file)
			require.NoError(t, err)
			rows := map[string]string{}
			for line := range strings.SplitSeq(string(text), "\n") {
				if label, complexity, ok := c.row(line); ok {
					rows[label] = strings.ReplaceAll(complexity, ",", "")
				}
			}

			for name, entry := range calibrationDocuments {
				label, mapped := c.labels[name]
				if !assert.True(t, mapped, "%s: the pinned document %q has no row mapped", c.file, name) {
					continue
				}
				complexity, found := rows[label]
				if !assert.True(t, found, "%s: no row is labeled %q", c.file, label) {
					continue
				}
				assert.Equal(t, strconv.Itoa(entry.complexity), complexity,
					"%s: the row %q carries a complexity the pinned table does not", c.file, label)
			}
		})
	}
}
