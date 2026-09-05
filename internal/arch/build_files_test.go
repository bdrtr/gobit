package arch_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

// This file audits the BUILD FILES against the test source: a test selector
// written down outside the Go code must still name a test that exists.
//
// # The incident
//
// The Makefile's load-test target read
//
//	go test -tags=integration -count=1 -v -run TestTemelYukAltindaDogruKalir ./internal/e2e/
//
// The function behind that name had been renamed to
// TestStaysCorrectUnderBaselineLoad while internal/e2e/load_test.go was
// translated (ADR 0012). The Makefile was not part of that change and nothing
// said so, because the go command does not treat an unmatched -run pattern as
// an error: it prints "testing: warning: no tests to run", then
// "ok ... [no tests to run]", and exits 0. The target therefore started a
// Postgres container, measured NOTHING, and reported green — measured on this
// repository, not assumed.
//
// That is the failure class this repository keeps naming as its most
// expensive: a check that sees nothing is indistinguishable from a check that
// passes. Fixing the one target closes the instance; this gate closes the
// CLASS, because the next rename would be exactly as quiet as this one.
//
// # What is modeled, and what is deliberately not
//
// MODELED. The pattern is interpreted the way the go command documents -run:
// it is split on unbracketed slashes into a sequence of regular expressions,
// and element zero is matched UNANCHORED against the name of each top-level
// test, benchmark, fuzz target and example. Element zero is also the only
// element that decides whether anything runs at all — go runs the parents of a
// match, so a pattern whose first element matches something always executes at
// least that parent.
//
// NOT MODELED, on purpose:
//
//   - The elements after the first. Subtest names are produced at RUNTIME
//     (t.Run with a computed string, a table entry, a fixture name); no static
//     scan can enumerate them, and pretending to would mean rejecting correct
//     patterns. A pattern whose first element matches and whose second names
//     nothing still runs the parent, so it is not the silent-nothing class.
//   - The PACKAGE arguments of the command. A pattern is checked against the
//     whole repository's test names, not against the tests of the packages the
//     command actually lists. Narrowing to the listed packages would catch one
//     more case — a name that exists somewhere else than where it is being
//     looked for — but it would have to model `./...`, build tags and the
//     package list of a shell line, and every one of those is a place to be
//     wrong in the ACCEPTING direction. The narrow version can be added the day
//     it is worth that risk; until then the scope is written down here rather
//     than left to be guessed from the code.
//   - The -bench pattern itself. This gate is about -run. A -bench that names
//     no benchmark is the same class and is not covered; saying so is better
//     than implying it is.
const (
	// makefileName is the build file the repository's targets live in. It is
	// required to exist: if it is renamed, this gate must fail loudly rather
	// than audit an empty set and pass.
	makefileName = "Makefile"

	// workflowDirName holds the GitHub Actions workflows, the second place a
	// `go test` command is written down.
	workflowDirName = ".github/workflows"

	// scriptsDirName is scanned WHEN PRESENT. The repository has no scripts
	// directory today; the check is written now so that adding one does not
	// silently add an unaudited place to hide a dead selector.
	scriptsDirName = "scripts"

	// runFlag and benchFlag are the two flags this gate reads.
	//
	// benchFlag is compared for EQUALITY or with a trailing "=", never as a
	// prefix: -benchmem and -benchtime both start with "-bench" and neither
	// selects a benchmark. Reading them as -bench would turn every bare
	// "-run '^$' -benchmem" into an accepted command that runs nothing.
	runFlag   = "-run"
	benchFlag = "-bench"

	// emptyRunPattern is the idiomatic spelling of "select no tests".
	//
	// It is the ONE pattern allowed to match nothing, and only next to
	// benchFlag — see [TestEveryRunPatternInABuildFileNamesARealTest] for the
	// argument.
	emptyRunPattern = "^$"

	// testEntryPointFloor is the smallest number of test entry points the
	// repository is expected to hold.
	//
	// It is a FLOOR, not a count: pinning the exact number would turn every
	// added test into a failure here. What it defends against is the scan
	// going blind — a walk that finds a handful of names would make almost
	// every pattern look dead, and a walk that finds none would make this gate
	// fail on everything and get deleted. The repository held 3597 entry
	// points when the floor was written.
	testEntryPointFloor = 2000

	// unresolvableRunPatternBudget is how many -run patterns may carry a make
	// or shell expansion.
	//
	// A pattern like `-run $(SELECT)` cannot be checked without running make,
	// so the name check SKIPS it. The skip is the dangerous part: an unchecked
	// pattern that nobody is told about is the same silence this file exists to
	// end. So the skip is not silent in either direction — every one is printed
	// with t.Logf, and the COUNT is pinned here at zero, which is what the
	// repository has today.
	//
	// Refusing them outright was considered and rejected: `make test RUN=Foo`
	// is a legitimate convenience, and a gate that forbids it would be argued
	// with rather than obeyed. Raising this number is the deliberate,
	// reviewable way to add one — and raising it is an admission, written into
	// the source, that one more command is now unaudited.
	unresolvableRunPatternBudget = 0
)

// testEntryPrefixes are the name prefixes the go command treats as an entry
// point.
//
// Examples are included because -run selects them too: `go test -run ExampleX`
// runs the example and compares its output. Leaving them out would report a
// pattern that names a real example as dead.
var testEntryPrefixes = []string{"Test", "Benchmark", "Fuzz", "Example"}

// buildFile is one file that can carry a `go test` command.
type buildFile struct {
	// path is repo-relative; it is what a failure message points at.
	path string
	// content is the file as read from disk.
	content string
	// makeEscapes marks the make dialect. In a recipe make eats one level of
	// dollars, so "$$" is what reaches the shell as a single "$": the
	// Makefile's `-run '^$$'` and the workflow's `-run '^$'` are the SAME
	// command. Without this the make spelling would be read as a pattern
	// carrying a variable and skipped — the exact accepting-direction mistake
	// this gate is supposed to remove.
	makeEscapes bool
}

// commandLine is one logical command: a physical line plus everything a
// trailing backslash joined onto it.
type commandLine struct {
	// number is the 1-based line the command STARTS on. The start is used
	// rather than the line the flag appears on because that is where a reader
	// has to begin reading to see the whole command.
	number int
	text   string
}

// runSelector is one -run flag found in a build file.
type runSelector struct {
	file string
	line int
	// command is the whole logical line, so the failure message can be acted
	// on without opening the file.
	command string
	// raw is the pattern exactly as written, quotes removed.
	raw string
	// pattern is raw after the dialect's escaping is undone; this is what the
	// go command would receive.
	pattern string
	// hasBench reports whether the same command also selects benchmarks.
	hasBench bool
	// unresolved marks a pattern that still holds a make or shell expansion.
	unresolved bool
}

func (s runSelector) String() string {
	return fmt.Sprintf("%s:%d\n      pattern: %s\n      command: %s", s.file, s.line, s.raw, s.command)
}

// buildFilesUnderAudit reads every file that can carry a test selector.
//
// The file list is built from the disk, not from a hand-written list of
// commands, and each source is REQUIRED rather than optional: a missing
// Makefile or an empty workflow directory fails here instead of quietly
// shrinking the audit to nothing. The repository has been bitten by a scan
// whose root list went stale ([TestTheProductionTreeListCoversTheRepository]
// exists for the same reason on the Go side).
func buildFilesUnderAudit(t *testing.T) []buildFile {
	t.Helper()

	var files []buildFile

	read := func(rel string, makeEscapes bool) {
		raw, err := os.ReadFile(filepath.Join(repoRoot, rel))
		require.NoError(t, err, "%s could not be read; a build file this gate cannot read is a build file it does not audit", rel)
		files = append(files, buildFile{path: rel, content: string(raw), makeEscapes: makeEscapes})
	}

	read(makefileName, true)

	entries, err := os.ReadDir(filepath.Join(repoRoot, workflowDirName))
	require.NoError(t, err, "%s could not be read; if CI moved, this gate is auditing a place nothing lives in", workflowDirName)

	workflows := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if ext := filepath.Ext(entry.Name()); ext != ".yml" && ext != ".yaml" {
			continue
		}
		read(workflowDirName+"/"+entry.Name(), false)
		workflows++
	}
	require.Positive(t, workflows,
		"no workflow file was found under %s.\nCI is where a dead selector costs the most, "+
			"because nobody reads a green job. An empty directory here means this gate "+
			"stopped covering CI without anyone deciding that.", workflowDirName)

	scriptsRoot := filepath.Join(repoRoot, scriptsDirName)
	if info, statErr := os.Stat(scriptsRoot); statErr == nil && info.IsDir() {
		walkErr := filepath.WalkDir(scriptsRoot, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			rel, relErr := filepath.Rel(repoRoot, path)
			if relErr != nil {
				return relErr
			}
			read(filepath.ToSlash(rel), false)

			return nil
		})
		require.NoError(t, walkErr, "%s could not be walked", scriptsDirName)
	}

	return files
}

// commandLines splits a build file into logical commands.
//
// Two transformations, both of which change what the audit sees:
//
//   - A trailing backslash JOINS the next line. The load-test target sets two
//     environment variables on their own lines and the `go test` invocation is
//     the third; without joining, the -run and the command it belongs to would
//     be different lines and the -bench exception could not be decided at all.
//   - A line whose first non-blank character is "#" is DROPPED. Both dialects
//     comment that way, and this repository writes long comments that quote
//     commands — auditing a quoted command would produce accusations about
//     text that never runs. The cost is accepted openly: a -run inside a
//     comment is not checked, so a comment can rot. Comment rot is loud when
//     read; a green target that measures nothing is not.
func commandLines(content string) []commandLine {
	physical := strings.Split(content, "\n")

	var out []commandLine

	for i := 0; i < len(physical); i++ {
		text := physical[i]
		if isCommentLine(text) {
			continue
		}

		number := i + 1
		for i+1 < len(physical) {
			trimmed := strings.TrimRight(text, " \t")
			if !strings.HasSuffix(trimmed, `\`) {
				break
			}
			text = trimmed[:len(trimmed)-1] + " " + physical[i+1]
			i++
		}

		out = append(out, commandLine{number: number, text: text})
	}

	return out
}

// isCommentLine reports whether the line is a comment in either dialect.
//
// The leading "@" is trimmed as well: a make recipe silences a line with "@",
// and this repository writes "@#" comment lines inside recipes.
func isCommentLine(line string) bool {
	return strings.HasPrefix(strings.TrimLeft(line, " \t@"), "#")
}

// shellWords splits a command into words the way a shell would, for the
// quoting these build files actually use: single quotes, double quotes and
// whitespace.
//
// Backslash escapes and variable expansion are NOT modeled. Expansion is the
// point — an unexpanded "$(X)" is exactly what [carriesShellVariable] has to
// see — and a backslash inside a word does not occur in these files, while a
// backslash at the end of a line is already consumed by [commandLines].
func shellWords(line string) []string {
	var (
		words   []string
		current strings.Builder
		quote   rune
		inWord  bool
	)

	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0

				continue
			}
			current.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case unicode.IsSpace(r):
			if inWord {
				words = append(words, current.String())
				current.Reset()
				inWord = false
			}
		default:
			current.WriteRune(r)
			inWord = true
		}
	}

	if inWord {
		words = append(words, current.String())
	}

	return words
}

// runSelectorsIn extracts every -run flag in the file.
//
// Both spellings are read, "-run X" and "-run=X", because the go command
// accepts both and a gate that knew only one would go blind the day somebody
// wrote the other.
//
// A -run is read as a test selector WHEREVER it appears, without first proving
// the command is a `go test`. That is the fail-loud direction: if another tool
// with a -run flag ever enters these files, this gate accuses it and somebody
// has to teach it the difference. The alternative — matching only lines that
// contain "go test" — fails in the accepting direction, and a selector this
// gate does not see is a selector nobody checks.
func runSelectorsIn(file buildFile) []runSelector {
	var found []runSelector

	for _, line := range commandLines(file.content) {
		words := shellWords(line.text)

		hasBench := false
		for _, word := range words {
			if word == benchFlag || strings.HasPrefix(word, benchFlag+"=") {
				hasBench = true

				break
			}
		}

		for i, word := range words {
			var raw string
			switch {
			case word == runFlag && i+1 < len(words):
				raw = words[i+1]
			case word == runFlag:
				// A -run with nothing after it. The go command rejects this
				// loudly, but an empty pattern compiles as a regexp that
				// matches everything, so it must never reach the matcher.
				raw = ""
			case strings.HasPrefix(word, runFlag+"="):
				raw = strings.TrimPrefix(word, runFlag+"=")
			default:
				continue
			}

			pattern := raw
			if file.makeEscapes {
				pattern = strings.ReplaceAll(pattern, "$$", "$")
			}

			found = append(found, runSelector{
				file:       file.path,
				line:       line.number,
				command:    strings.TrimSpace(line.text),
				raw:        raw,
				pattern:    pattern,
				hasBench:   hasBench,
				unresolved: carriesShellVariable(pattern),
			})
		}
	}

	return found
}

// carriesShellVariable reports whether the pattern still holds an expansion
// only make or the shell could perform.
//
// The distinction that has to be exact is against the regexp anchor: "^$" ends
// in a dollar that means end-of-string, while "$(SELECT)", "${SELECT}" and
// "$SELECT" are expansions. A dollar followed by "(", "{" or an identifier
// rune is an expansion; a dollar followed by anything else — including the end
// of the string — is the anchor. Getting this backwards would either skip the
// bench targets as unresolvable or feed a literal "$(SELECT)" to the regexp
// engine as if it were a test name.
func carriesShellVariable(pattern string) bool {
	runes := []rune(pattern)
	for i, r := range runes {
		if r != '$' || i+1 >= len(runes) {
			continue
		}

		switch next := runes[i+1]; {
		case next == '(', next == '{', next == '_', unicode.IsLetter(next), unicode.IsDigit(next):
			return true
		}
	}

	return false
}

// firstRunElement returns the part of the pattern that selects TOP-LEVEL
// names.
//
// `go test -run` splits its pattern on unbracketed slashes: element zero is
// matched against the test's own name, element one against its subtest's name,
// and so on. Only element zero can be resolved statically and only element
// zero decides whether anything runs, so the rest is cut here rather than
// silently mismatched.
//
// The split tracks bracket and parenthesis depth and skips an escaped rune, so
// a slash inside "[a/b]" or "(a/b)" is part of the regexp and not a separator.
// The go command tracks brackets and parentheses separately; this does not,
// which only matters for a pattern that closes them out of order — such a
// pattern is not a valid regexp anyway and is reported by the compile step.
func firstRunElement(pattern string) string {
	runes := []rune(pattern)
	depth := 0

	for i := 0; i < len(runes); i++ {
		switch runes[i] {
		case '\\':
			i++
		case '[', '(':
			depth++
		case ']', ')':
			if depth > 0 {
				depth--
			}
		case '/':
			if depth == 0 {
				return string(runes[:i])
			}
		}
	}

	return pattern
}

// runPatternSelects reports whether the pattern selects at least one of the
// names.
//
// The match is UNANCHORED, which is what the go command does: -run StaysCorrect
// runs TestStaysCorrectUnderBaselineLoad. Anchoring here would reject correct
// patterns and, worse, teach everyone that this gate lies.
func runPatternSelects(pattern string, names []string) (bool, error) {
	expression, err := regexp.Compile(firstRunElement(pattern))
	if err != nil {
		return false, err
	}

	for _, name := range names {
		if expression.MatchString(name) {
			return true, nil
		}
	}

	return false, nil
}

// testFunctionNames collects every test entry point in the repository.
//
// The names are PARSED, never grepped and never obtained by running go test.
// A regexp over the source would miss a declaration spread over two lines and
// would find one inside a comment or a string; running `go test -list` would
// need every package to build and every build tag to be guessed, and this
// repository hides its heaviest tests behind tags. go/parser reads a file
// whatever its build tag says, which is the whole reason the integration-only
// name TestStaysCorrectUnderBaselineLoad is visible here at all.
func testFunctionNames(t *testing.T) []string {
	t.Helper()

	fset := token.NewFileSet()

	var names []string

	for _, tree := range productionTrees {
		for _, path := range treeFiles(t, tree) {
			if !strings.HasSuffix(path, "_test.go") {
				continue
			}

			parsed, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			require.NoError(t, err, "%s could not be parsed", path)

			for _, decl := range parsed.Decls {
				function, ok := decl.(*ast.FuncDecl)
				if !ok || function.Recv != nil {
					continue
				}
				if isGoTestEntryPoint(function.Name.Name) {
					names = append(names, function.Name.Name)
				}
			}
		}
	}

	return names
}

// isGoTestEntryPoint applies the go command's own naming rule.
//
// The rule is not "starts with Test": the character after the prefix must not
// be lower-case, which is why TestingTheHelper is not a test and Test_Order is.
// Copying the rule rather than approximating it matters in the accepting
// direction — an approximation that counted TestingTheHelper as a test would
// let a pattern naming it pass while `go test` runs nothing.
func isGoTestEntryPoint(name string) bool {
	for _, prefix := range testEntryPrefixes {
		rest, ok := strings.CutPrefix(name, prefix)
		if !ok {
			continue
		}
		if rest == "" {
			return true
		}
		first, _ := utf8.DecodeRuneInString(rest)

		return !unicode.IsLower(first)
	}

	return false
}

// TestEveryRunPatternInABuildFileNamesARealTest fails the build when a build
// file selects a test that does not exist.
//
// # The exception, and why it is narrow
//
// `-run '^$'` selects nothing ON PURPOSE: it is the idiomatic way to say "no
// tests, only benchmarks", and it appears in the Makefile's bench target and in
// the benchmark step of .github/workflows/ci.yml. Rejecting it would break two
// correct commands, so it is accepted — but ONLY when the same command also
// carries -bench. Alone, "-run '^$'" is a command that genuinely does nothing,
// which is the very thing this gate exists to name, and it is reported as such.
//
// The exception is spelled EXACTLY, not derived. "Any pattern that matches
// nothing is fine as long as -bench is there" was the obvious generalization
// and it is wrong: it would have accepted the load-test target if somebody had
// ever added -bench to it, and it accepts every future typo that happens to
// sit next to a benchmark run. "Selects nothing deliberately" and "selects
// nothing by accident" are the same string to a regexp engine and opposite
// things to a reader; the only honest separator is the idiom's exact spelling.
func TestEveryRunPatternInABuildFileNamesARealTest(t *testing.T) {
	t.Parallel()

	names := testFunctionNames(t)
	require.Greater(t, len(names), testEntryPointFloor,
		"only %d test entry points were found in the repository.\nThe name scan must have "+
			"gone blind, and a blind name scan makes this gate accuse every correct "+
			"pattern in the build files.", len(names))

	var selectors []runSelector
	for _, file := range buildFilesUnderAudit(t) {
		selectors = append(selectors, runSelectorsIn(file)...)
	}
	require.NotEmpty(t, selectors,
		"not one -run was found in the build files.\nThe repository writes at least three "+
			"of them; finding none means the scanner stopped seeing, and a scanner that "+
			"sees nothing passes everything.")

	var violations, unresolved []string

	for _, selector := range selectors {
		switch {
		case selector.unresolved:
			unresolved = append(unresolved, selector.String())
			t.Logf("NOT CHECKED, the pattern is expanded by make or the shell:\n    %s", selector)
		case selector.pattern == "":
			violations = append(violations, selector.String()+
				"\n      the flag carries no pattern at all")
		case selector.pattern == emptyRunPattern && selector.hasBench:
			continue
		case selector.pattern == emptyRunPattern:
			violations = append(violations, selector.String()+
				"\n      selects no tests and the command carries no "+benchFlag+
				", so it runs nothing at all")
		default:
			matched, err := runPatternSelects(selector.pattern, names)
			require.NoError(t, err,
				"the pattern at %s:%d is not a valid Go regexp: %v\n"+
					"go test would reject it; it is reported here rather than swallowed, "+
					"because a pattern that cannot compile is a pattern that was never run.",
				selector.file, selector.line, err)

			if !matched {
				violations = append(violations, selector.String()+
					"\n      names no test, benchmark, fuzz target or example in the repository")
			}
		}
	}

	require.Empty(t, violations,
		"a build file selects tests that do not exist:\n\n%s\n\n"+
			"go test exits 0 when its -run pattern matches nothing: it prints "+
			"\"no tests to run\" and the target goes green having measured nothing. "+
			"That is how the load-test target survived a rename for months. Either point "+
			"the pattern at the name the test has now, or delete the command — a command "+
			"that runs nothing is worse than no command, because it reports success.",
		strings.Join(violations, "\n\n"))

	require.LessOrEqual(t, len(unresolved), unresolvableRunPatternBudget,
		"%d -run pattern(s) cannot be resolved statically and the budget is %d:\n\n%s\n\n"+
			"Such a pattern is NOT checked by this gate. If the command really needs a "+
			"variable, raise unresolvableRunPatternBudget in this file and accept in "+
			"writing that one more command is unaudited; if it does not, spell the test "+
			"name out so that renaming it fails here.",
		len(unresolved), unresolvableRunPatternBudget, strings.Join(unresolved, "\n\n"))
}

// runScannerExample is the executable spelling of the extraction rules.
//
// It is written in the make dialect because that dialect has the two forms
// worth pinning: the "$$" escape and the backslash continuation. Every line of
// it stands for a rule, so a changed expectation in
// [TestTheRunPatternScannerIsNotBlind] says exactly which rule moved.
const runScannerExample = `# A comment mentioning -run TestInAComment must not be read as a command.
bench:
	go test -run '^$$' -bench . -benchmem ./...

nothing:
	go test -run '^$$' -benchmem ./...

load:
	GOBIT_LOAD_REQUESTS=1 \
	go test -tags=integration -count=1 -run TestRealName ./internal/e2e/

equals:
	go test -run=TestRealName/a_subtest ./...

variable:
	go test -run '$(SELECT)' ./...
`

// TestTheRunPatternScannerIsNotBlind pins the floor under the gate.
//
// [TestEveryRunPatternInABuildFileNamesARealTest] passes in three different
// ways that look identical from the outside: no build file selects a dead
// test, the scanner found no -run at all, or the name matcher approves
// everything it is given. Only the first is good news. The scanner and the
// matcher are therefore exercised on fixtures whose answers are known, and the
// rejection is proved with the very name the incident was about.
func TestTheRunPatternScannerIsNotBlind(t *testing.T) {
	t.Parallel()

	// 1. Extraction: what comes out of the example is pinned EXACTLY. A
	// missing line says the scanner has gone blind to a form; an extra line
	// says it started reading text that is not a command.
	var extracted []string
	for _, selector := range runSelectorsIn(buildFile{
		path:        "fixture/" + makefileName,
		content:     runScannerExample,
		makeEscapes: true,
	}) {
		extracted = append(extracted, fmt.Sprintf("%d|%s|bench=%t|variable=%t",
			selector.line, selector.pattern, selector.hasBench, selector.unresolved))
	}
	require.Equal(t, []string{
		"3|^$|bench=true|variable=false",
		"6|^$|bench=false|variable=false",
		"9|TestRealName|bench=false|variable=false",
		"13|TestRealName/a_subtest|bench=false|variable=false",
		"16|$(SELECT)|bench=false|variable=true",
	}, extracted,
		"the scanner read the example differently than expected.\nLine 3 is the make \"$$\" "+
			"escape next to a real -bench; line 6 is the same escape with only -benchmem, "+
			"which is NOT -bench; line 9 is the backslash continuation, whose command "+
			"starts on the line the number points at; line 13 is the \"-run=\" spelling "+
			"with a subtest element; line 16 is a pattern make would expand.")

	// 2. Matching: the names are in-memory, so what is being tested is the
	// matcher and not the repository.
	names := []string{"TestRealName", "BenchmarkRealThing", "FuzzRealInput", "ExampleRealUse"}
	for _, probe := range []struct {
		pattern string
		selects bool
		why     string
	}{
		{"TestRealName", true, "an exact name must be selected"},
		{"TestTemelYukAltindaDogruKalir", false,
			"a name that does not exist must be REJECTED; this is the incident itself, " +
				"and if it passes the gate approves everything"},
		{"RealName", true, "the match is unanchored, as go test documents it"},
		{"^TestRealName$", true, "an anchored pattern must still select its name"},
		{"TestRealName/a_subtest", true,
			"only the first slash element is matched; subtest names cannot be enumerated " +
				"statically and are not modeled"},
		{"NoSuchName/a_subtest", false,
			"a dead first element must be rejected even when a subtest element follows"},
		{emptyRunPattern, false, "the empty selector must match nothing, or the -bench exception is pointless"},
		{"Benchmark", true, "benchmarks are entry points too"},
	} {
		selects, err := runPatternSelects(probe.pattern, names)
		require.NoError(t, err, "%q did not compile", probe.pattern)
		require.Equal(t, probe.selects, selects, "%q: %s", probe.pattern, probe.why)
	}

	_, err := runPatternSelects("Test[", names)
	require.Error(t, err,
		"an invalid regexp was accepted; a pattern go test would refuse must not be "+
			"reported as a selector that works")

	// 3. The entry-point rule: the accepting direction is where a wrong answer
	// hides a dead pattern.
	require.True(t, isGoTestEntryPoint("Test"), "the bare name Test is an entry point")
	require.True(t, isGoTestEntryPoint("Test_Order"), "an underscore is not a lower-case rune")
	require.True(t, isGoTestEntryPoint("BenchmarkThing"), "benchmarks are entry points")
	require.False(t, isGoTestEntryPoint("TestingTheHelper"),
		"a lower-case rune after the prefix means go test does not run it; counting it "+
			"would let a pattern naming a helper pass while nothing runs")
	require.False(t, isGoTestEntryPoint("helperForTests"), "the prefix has to be at the front")

	// 4. The real repository: the two inputs of the gate must still find
	// something on disk.
	files := buildFilesUnderAudit(t)
	seen := map[string]int{}
	for _, file := range files {
		seen[file.path] += len(runSelectorsIn(file))
	}
	require.Positive(t, seen[makefileName],
		"no -run was found in the %s.\nThe bench and load-test targets both carry one; "+
			"finding none means the make dialect stopped being read.", makefileName)
	require.Positive(t, seen[workflowDirName+"/ci.yml"],
		"no -run was found in the CI workflow.\nIts benchmark step carries one; finding "+
			"none means the workflow dialect stopped being read and CI is no longer "+
			"covered by this gate.")

	names = testFunctionNames(t)
	require.Contains(t, names, "TestStaysCorrectUnderBaselineLoad",
		"the load test's own name was not collected.\nIt lives behind the integration "+
			"build tag in internal/e2e/load_test.go; if tagged files stopped being "+
			"parsed, this gate would call every integration selector dead.")
	require.Contains(t, names, "TestTheProductionTreeListCoversTheRepository",
		"a name from this very package was not collected; the walk is not reaching the "+
			"files it is standing in")
}
