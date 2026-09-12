package scaffold

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The version a generated go.mod can require, and why it is usually not a tag.
//
// # The measured state of this module's own releases
//
// A generated project requires the library by VERSION, and the version has to be
// one the module proxy can serve with the published surface in it. Measured on
// 2026-09-12: the newest tag of this module is v0.8.0, `@latest` resolves to it,
// and it does NOT contain the root package — the facade landed after it, so
// `require github.com/bdrtr/gobit v0.8.0` plus `import "github.com/bdrtr/gobit"`
// fails at `go mod tidy` with "does not contain package".
//
// So the one thing a generated go.mod cannot write today is a semantic version.
// What the proxy does serve for any pushed commit is a PSEUDO-VERSION, and that
// is what this file composes.
//
// The fix is a release, not a generator: the day a tag contains the facade, the
// build injects that tag and [Version] returns it unchanged.

// Build is what the build injects about the binary that is generating.
//
// Every field is a STRING taken straight from git by the Makefile, with no
// arithmetic on that side: the arithmetic (the patch bump, the timestamp format,
// the hash length) is here, where it is tested.
type Build struct {
	// Release is the tag this binary was built from, empty when it was not
	// built from one.
	Release string
	// BaseTag is the newest tag reachable from the commit, empty when there is
	// none.
	BaseTag string
	// Commit is the full commit hash.
	Commit string
	// CommitTime is the commit's time.
	CommitTime time.Time
}

// pseudoHashLen is how many hex characters of the commit a pseudo-version
// carries. The number is Go's, not a choice of ours.
const pseudoHashLen = 12

// Version returns the version a generated go.mod should require, or "" when the
// build cannot name one.
//
// An empty answer is not a failure of this function — it is the honest report
// that this binary was built without the information, and the caller turns it
// into a refusal or into a `replace` (see [Input.Replace]).
func (b Build) Version() string {
	if b.Release != "" {
		return b.Release
	}
	if b.Commit == "" || b.CommitTime.IsZero() {
		return ""
	}

	return pseudoVersion(b.BaseTag, b.Commit, b.CommitTime)
}

// pseudoVersion composes the version the module proxy serves for one commit.
//
// The form is Go's own and the two shapes are not interchangeable:
//
//	v0.0.0-<time>-<hash>        when no tag is reachable
//	v<major>.<minor>.<patch+1>-0.<time>-<hash>   when one is
//
// A binary that wrote the first form while a tag existed would name a version
// the proxy does not serve, and `go mod tidy` in the generated project would
// rewrite it — silently, to something the generator never chose.
func pseudoVersion(baseTag, commit string, when time.Time) string {
	stamp := when.UTC().Format("20060102150405")
	hash := commit
	if len(hash) > pseudoHashLen {
		hash = hash[:pseudoHashLen]
	}

	major, minor, patch, ok := parseSemver(baseTag)
	if !ok {
		return fmt.Sprintf("v0.0.0-%s-%s", stamp, hash)
	}

	return fmt.Sprintf("v%d.%d.%d-0.%s-%s", major, minor, patch+1, stamp, hash)
}

// parseSemver reads "vX.Y.Z"; anything else is reported as absent.
//
// A prerelease or build suffix is refused rather than stripped: the pseudo-version
// of a commit after "v1.0.0-rc1" is not derived by bumping a patch, and guessing
// would produce a version the proxy does not serve.
func parseSemver(tag string) (major, minor, patch int, ok bool) {
	if !strings.HasPrefix(tag, "v") {
		return 0, 0, 0, false
	}
	parts := strings.Split(strings.TrimPrefix(tag, "v"), ".")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}

	numbers := make([]int, 3)
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return 0, 0, 0, false
		}
		numbers[i] = n
	}

	return numbers[0], numbers[1], numbers[2], true
}
