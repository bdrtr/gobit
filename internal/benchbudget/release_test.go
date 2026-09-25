package benchbudget

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheReleaseIsReadFromBothSides pins the two readings refuseAnotherRelease
// compares: a distribution's runtime version carries a suffix, and go.mod's
// directive a space.
func TestTheReleaseIsReadFromBothSides(t *testing.T) {
	for version, want := range map[string]string{
		"go1.26.6":            "go1.26.6",
		"go1.27.1 X:nodwarf5": "go1.27.1",
		"go1.27.1-X:nodwarf5": "go1.27.1",
	} {
		assert.Equal(t, want, releaseOf(version), version)
	}

	release, err := goDirective("module example.com/m\n\ngo 1.26.6\n\nrequire (\n\tgolang.org/x/mod v0.1.0\n)\n")
	require.NoError(t, err)
	assert.Equal(t, "go1.26.6", release)

	_, err = goDirective("module example.com/m\n")
	require.Error(t, err, "a go.mod without the directive names no release")
}

// TestTheRepositoryNamesTheReleaseThisBinaryRuns is the gate's own premise on
// the lane that runs it: the release this test binary was built by is the one
// go.mod names.
func TestTheRepositoryNamesTheReleaseThisBinaryRuns(t *testing.T) {
	refuseAnotherRelease(t)
}
