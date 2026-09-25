// Package benchbudget turns a benchmark into a gate.
//
// A benchmark measures. It does not defend. Five of them have run in this
// repository since the Go side was first measured, and not one of them could
// fail: a change that doubled the allocations of the promotion arithmetic would
// have printed a bigger number into a terminal nobody was watching and the build
// would have stayed green.
//
// A budget is the missing half. It names one benchmark and the number of
// allocations a single iteration may make, and it runs in the ORDINARY test
// lane rather than a lane of its own, because the number it reads does not
// change under the race detector and a lane nobody runs is the problem this
// package exists to fix.
//
// # Allocations and not time
//
// The ceiling is on allocations per operation and never on nanoseconds. A
// timing threshold measures the machine — a shared runner, a thermal throttle, a
// noisy neighbor — and would have to be set loose enough to pass on the worst
// of them, which is loose enough to miss the regression. Allocations per
// operation are a property of the code and of the Go release that compiled it:
// the same figure on a laptop and on a CI runner under one release, which is
// why this can be a gate at all. The race detector moves one of them (see
// [Budget.AllocsUnderRace]) and so does another release (D138): the HTTP
// surface's benchmark allocates 8,430 per request under go1.26.6 and 8,665
// under go1.27.1 from the same source. [Check] therefore judges a budget only
// under the release go.mod names, which is the one CI installs.
//
// What it therefore does NOT catch is a change that gets slower without
// allocating: a sort that becomes quadratic over the same buffers is invisible
// here. The benchmark still prints the time, and a person still has to read it.
package benchbudget

import (
	"errors"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// Budget is one benchmark and the allocations one iteration of it may make.
type Budget struct {
	// Name is the benchmark's own name, which is what a failure prints and what
	// the arch gate matches against the tree.
	Name string

	// Run is the benchmark function itself. It is the function VALUE rather than
	// its name, so a budget cannot outlive the benchmark it prices: deleting or
	// renaming the benchmark stops this package's callers from compiling.
	Run func(*testing.B)

	// Allocs is the ceiling, in allocations per iteration.
	Allocs int64

	// AllocsUnderRace is the ceiling when the race detector is on, and it is
	// stated only where it differs. Zero means "the same as [Budget.Allocs]".
	//
	// It exists because the assumption behind putting these budgets in the
	// ordinary lane was CHECKED and is true of four benchmarks out of five. The
	// fifth, which drives an HTTP surface, allocates 8,432 per request without
	// the detector and 8,702-8,705 with it, measured six times each. A single
	// ceiling would have had to be the looser of the two, and the tighter number
	// is the one that catches a regression.
	//
	// A race ceiling is never LOWER than the plain one, which is why zero can
	// mean "unstated" without ambiguity worth guarding.
	AllocsUnderRace int64

	// Why says what the number is, in a sentence. A ceiling with no sentence is
	// a number somebody will raise the day it fails, because nothing on the
	// screen says what raising it costs.
	Why string
}

// Check runs every budget's benchmark and reports the ones over their ceiling.
//
// It fails rather than skips in the three cases where the measurement would
// otherwise be missing and green: no budgets at all, a benchmark that did not
// run, and a `-benchtime` given as a fixed iteration count.
func Check(t *testing.T, budgets []Budget) {
	t.Helper()

	refuseIterationBenchtime(t)
	refuseAnotherRelease(t)

	if len(budgets) == 0 {
		t.Fatal("no allocation budgets were given: an empty table passes without measuring anything")
	}

	for _, budget := range budgets {
		if budget.Name == "" || budget.Run == nil || budget.Why == "" {
			t.Fatalf("budget %q is incomplete: a name, the benchmark and a sentence are all required", budget.Name)
		}

		if declared := benchmarkName(budget.Run); declared != budget.Name {
			t.Fatalf("budget %q prices %s: the name is what the tree is audited against, so the two cannot differ",
				budget.Name, declared)
		}

		t.Run(budget.Name, func(t *testing.T) {
			result := testing.Benchmark(budget.Run)

			// A benchmark that skipped or failed returns a zero result, whose
			// allocation count is under every ceiling there is. That is the one
			// way this gate could pass while measuring nothing.
			if result.N == 0 {
				t.Fatalf("%s did not run a single iteration, so its budget measured nothing", budget.Name)
			}

			ceiling, lane := budget.Allocs, "budget"
			if raceEnabled && budget.AllocsUnderRace > budget.Allocs {
				ceiling, lane = budget.AllocsUnderRace, "budget under the race detector"
			}

			if got := result.AllocsPerOp(); got > ceiling {
				t.Errorf("%s allocates %d per operation and its %s is %d.\n%s\n"+
					"Raising the budget is a decision about the cost of this path, not a repair of this test.",
					budget.Name, got, lane, ceiling, budget.Why)
			}
		})
	}
}

// benchmarkName is the declared name of the function a budget prices.
//
// The gate that walks the tree matches [Budget.Name] against the benchmarks it
// finds, so a budget naming one function and running another would report a
// benchmark as priced while pricing a different one. Asking the runtime removes
// the possibility instead of auditing for it.
func benchmarkName(run func(*testing.B)) string {
	full := runtime.FuncForPC(reflect.ValueOf(run).Pointer()).Name()
	if dot := strings.LastIndex(full, "."); dot >= 0 {
		return full[dot+1:]
	}
	return full
}

// refuseIterationBenchtime stops a run whose -benchtime is a fixed count.
//
// `-benchtime=1x` is the ordinary way to check that benchmarks still compile
// and run, and under it a per-operation figure stops meaning what this package
// asserts: one iteration divides whatever the loop allocated by one, so a body
// that allocates once for the whole run reads as one allocation per operation
// and a budget of zero fails for a reason that has nothing to do with the code.
// The message is the point — the alternative is a red build whose number nobody
// can explain.
func refuseIterationBenchtime(t *testing.T) {
	t.Helper()

	benchtime := flag.Lookup("test.benchtime")
	if benchtime == nil {
		return
	}

	if value := benchtime.Value.String(); strings.HasSuffix(value, "x") {
		t.Fatalf("-benchtime=%s fixes the iteration count, and an allocation budget is a per-operation figure. "+
			"Re-run these tests without it; `make bench` is the lane for a fixed count.", value)
	}
}

// refuseAnotherRelease stops a run compiled by another Go release than the one
// go.mod names (D138).
//
// The ceilings are counts taken under that release. Judged under another, a
// budget either fails for a reason that is not in the code or passes a
// regression the other release happens to absorb, and neither is an answer
// about the code. Every make lane sets GOTOOLCHAIN to go.mod's release.
func refuseAnotherRelease(t *testing.T) {
	t.Helper()

	want, err := moduleRelease()
	if err != nil {
		t.Fatalf("the Go release the budgets were measured under could not be read: %v", err)
	}

	if got := releaseOf(runtime.Version()); got != want {
		t.Fatalf("these allocation ceilings were measured under %s, the release go.mod names and CI "+
			"installs, and this test binary was built by %s. A count belongs to the code and the "+
			"release together; re-run with GOTOOLCHAIN=%s, which every make lane sets.",
			want, runtime.Version(), want)
	}
}

// releaseOf is the release in a runtime version: "go1.27.1" out of
// "go1.27.1 X:nodwarf5" or "go1.27.1-X:nodwarf5".
func releaseOf(version string) string {
	release, _, _ := strings.Cut(version, " ")
	release, _, _ = strings.Cut(release, "-")

	return release
}

// moduleRelease reads the go directive of the nearest go.mod at or above the
// working directory, which is the test's package directory, and returns it as
// a release name: "go1.26.6" for "go 1.26.6".
func moduleRelease() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		data, readErr := fs.ReadFile(os.DirFS(dir), "go.mod")
		if readErr == nil {
			return goDirective(string(data))
		}
		if !errors.Is(readErr, fs.ErrNotExist) {
			return "", readErr
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no go.mod at or above the working directory")
		}
		dir = parent
	}
}

// goDirective returns the release a go.mod's go directive names.
func goDirective(gomod string) (string, error) {
	for _, line := range strings.Split(gomod, "\n") {
		if version, found := strings.CutPrefix(strings.TrimSpace(line), "go "); found {
			return "go" + strings.TrimSpace(version), nil
		}
	}

	return "", errors.New("go.mod has no go directive")
}
