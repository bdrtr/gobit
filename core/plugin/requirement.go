package plugin

import (
	"context"
	"strings"

	"golang.org/x/mod/semver"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/version"
)

// CoreRequirement is implemented by a plugin that names the gobit releases it
// works with (ADR 0224). It is optional: a plugin that does not implement it is
// installed with any release, as every plugin was before.
//
// The range is what Go's module requirement cannot say. A plugin module's
// `require` sets the lowest release a build may use and nothing above it, while
// a release before 1.0.0 may break a plugin in a minor version (ADR 0036), and
// what breaks most quietly is looked up by name — a container name, an event,
// a setting — where no compiler looks.
type CoreRequirement interface {
	// RequiresCore returns the releases the plugin works with: comparisons of a
	// semantic version, all of which must hold, separated by spaces or commas,
	// such as ">=v0.9.0 <v0.11.0". The "v" may be left out.
	RequiresCore() string
}

// coreVersion is the release this binary was built from; a variable so a test
// can stand in for a build it cannot perform.
var coreVersion = version.Library

// coreBound is one comparison of a range.
type coreBound struct {
	op      string
	version string
}

// coreOperators are the comparisons a range takes, the two-character ones
// first so ">=" is not read as ">".
var coreOperators = []string{">=", "<=", ">", "<", "="}

// parseCoreRange reads a range; an empty one, an unknown comparison or a
// version that is not semantic is refused.
func parseCoreRange(text string) ([]coreBound, error) {
	fields := strings.FieldsFunc(text, func(r rune) bool { return r == ' ' || r == ',' })
	if len(fields) == 0 {
		return nil, coreerrors.Invalid(codeCoreRangeInvalid, "the range of core releases is empty")
	}
	bounds := make([]coreBound, 0, len(fields))
	for _, field := range fields {
		op := ""
		for _, candidate := range coreOperators {
			if strings.HasPrefix(field, candidate) {
				op = candidate
				break
			}
		}
		if op == "" {
			return nil, coreerrors.Invalid(codeCoreRangeInvalid,
				"%q compares nothing; a bound starts with >=, <=, >, < or =", field)
		}
		release := strings.TrimPrefix(field, op)
		if !strings.HasPrefix(release, "v") {
			release = "v" + release
		}
		if !semver.IsValid(release) {
			return nil, coreerrors.Invalid(codeCoreRangeInvalid, "%q is not a semantic version", field)
		}
		bounds = append(bounds, coreBound{op: op, version: release})
	}
	return bounds, nil
}

// admits reports whether a release holds every bound.
func admits(bounds []coreBound, release string) bool {
	for _, bound := range bounds {
		c := semver.Compare(release, bound.version)
		var holds bool
		switch bound.op {
		case ">=":
			holds = c >= 0
		case "<=":
			holds = c <= 0
		case ">":
			holds = c > 0
		case "<":
			holds = c < 0
		default:
			holds = c == 0
		}
		if !holds {
			return false
		}
	}
	return true
}

// checkCore refuses a plugin whose range does not admit the release this binary
// was built from, and one whose range cannot be read (ADR 0224).
//
// A build that does not say its release — `go run`, `go test`, a `replace`, a
// tree with uncommitted changes — cannot be checked; the ranges are still read,
// so a malformed one fails there too, and the log says the check was skipped.
func (r *Registry) checkCore(ctx context.Context) error {
	release := coreVersion()
	for _, p := range r.plugins {
		requirement, ok := p.(CoreRequirement)
		if !ok {
			continue
		}
		stated := requirement.RequiresCore()
		bounds, err := parseCoreRange(stated)
		if err != nil {
			return coreerrors.Wrap(err, coreerrors.KindInvalid, codeCoreRangeInvalid,
				"the %s plugin names the core releases it works with unreadably", p.Name())
		}
		if release == "" {
			r.log.InfoContext(ctx, "the core release is not stamped in this build; the plugin's range is not checked",
				"plugin", p.Name(), "requires_core", stated)
			continue
		}
		if !admits(bounds, release) {
			return coreerrors.Invalid(codeCoreUnsupported,
				"the %s plugin works with core releases %s and this binary is built with %s",
				p.Name(), stated, release)
		}
	}
	return nil
}
