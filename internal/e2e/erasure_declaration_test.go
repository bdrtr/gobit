//go:build integration

package e2e

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/workflows/datasubject"
)

// An erasure keeps only what its holder declares (ADR 0172).

// assertKeptIsDeclared holds every answer of a sweep to its holder's
// declaration: what it reports as kept is a place the declaration names, and
// what an anonymizing answer keeps is exactly what the declaration says an
// erasure keeps. The link holder once reported a column its declaration never
// named (D128); a controller reading the declaration would not have looked
// there.
func assertKeptIsDeclared(t *testing.T, co *datasubject.Coordinator, report personaldata.Report) {
	t.Helper()

	declared := map[string]personaldata.Declaration{}
	for _, d := range co.PersonalData() {
		declared[d.Holder] = d
	}

	for _, answer := range report.Results {
		declaration, ok := declared[answer.Holder]
		if !assert.True(t, ok, "%s answered an erasure and declares nothing", answer.Holder) {
			continue
		}
		paths := declaration.Paths()
		for _, kept := range answer.Kept {
			assert.True(t, slices.Contains(paths, kept),
				"%s reports %s as kept and its declaration does not name it", answer.Holder, kept)
		}
		// A holder that found nobody may keep its list empty — the order and the
		// cart do, and say why — so the equality is asked of an answer that
		// touched rows.
		if answer.Outcome == personaldata.Anonymized && answer.Rows > 0 {
			assert.Equal(t, declaration.KeptOnErasure(), answer.Kept,
				"%s anonymized and kept something other than what its declaration says an "+
					"erasure keeps", answer.Holder)
		}
	}
}

// TestAnErasureOfNobodyStillKeepsOnlyWhatIsDeclared asks the production sweep
// about a person no holder has. Several holders report what they keep whatever
// the subject — a refusal, a static holder — and those lists are held to the
// declarations too.
func TestAnErasureOfNobodyStillKeepsOnlyWhatIsDeclared(t *testing.T) {
	co := personalDataCoordinator(t)

	report, err := co.Erase(t.Context(), personaldata.Subject{
		CustomerID: "cus_NOBODY_AT_ALL", Email: "nobody.at.all@example.com",
	})
	require.NoError(t, err)
	require.NotEmpty(t, report.Results)

	assertKeptIsDeclared(t, co, report)
}
