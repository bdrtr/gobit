package version

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestTheLibraryReleaseIsReadFromTheBuild is ADR 0224: the release comes from
// the main module when the binary is gobit and from the dependency when it
// embeds it, and a build that cannot say — no build information, a development
// build, uncommitted changes, a replace — says nothing.
func TestTheLibraryReleaseIsReadFromTheBuild(t *testing.T) {
	previous := readBuildInfo
	t.Cleanup(func() { readBuildInfo = previous })

	embedded := &debug.BuildInfo{
		Main: debug.Module{Path: "example.com/shop", Version: "v1.2.3"},
		Deps: []*debug.Module{{Path: Module, Version: "v0.9.0"}},
	}
	for name, tc := range map[string]struct {
		info *debug.BuildInfo
		ok   bool
		want string
	}{
		"gobit itself":    {&debug.BuildInfo{Main: debug.Module{Path: Module, Version: "v0.10.0"}}, true, "v0.10.0"},
		"embedded":        {embedded, true, "v0.9.0"},
		"no build info":   {nil, false, ""},
		"a development":   {&debug.BuildInfo{Main: debug.Module{Path: Module, Version: "(devel)"}}, true, ""},
		"a dirty tree":    {&debug.BuildInfo{Main: debug.Module{Path: Module, Version: "v0.10.0+dirty"}}, true, ""},
		"a replace":       {&debug.BuildInfo{Deps: []*debug.Module{{Path: Module, Version: "v0.9.0", Replace: &debug.Module{Path: "../gobit"}}}}, true, ""},
		"not in the deps": {&debug.BuildInfo{Main: debug.Module{Path: "example.com/shop", Version: "v1.0.0"}}, true, ""},
	} {
		info, ok := tc.info, tc.ok
		readBuildInfo = func() (*debug.BuildInfo, bool) { return info, ok }
		assert.Equal(t, tc.want, Library(), name)
	}
}
