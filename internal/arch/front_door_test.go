package arch_test

import (
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The README's first command is the front door (ADR 0183).
//
// `go run github.com/bdrtr/gobit/<dir>@latest new <name>` is what a stranger
// types before reading anything else, and nothing in the tree ran it: it names a
// package path and a verb, and either can move while every lane stays green. The
// package moving is a rename of the binary's directory; the verb moving is a
// change to the dispatch. The command itself cannot be run here — `@latest` is
// the module proxy's answer, not this checkout's — so its two halves are
// checked against the tree it will resolve to after the next release.

// frontDoorCommand is the README line, with the package directory and the verb
// captured.
var frontDoorCommand = regexp.MustCompile(
	`(?m)^go run github\.com/bdrtr/gobit/(\S+)@latest (\S+) \S+$`)

func TestTheReadmesFirstCommandNamesABinaryThatTakesIt(t *testing.T) {
	t.Parallel()

	readme, err := os.ReadFile(filepath.Join(repoRoot, "README.md"))
	require.NoError(t, err)

	matches := frontDoorCommand.FindAllStringSubmatch(string(readme), -1)
	require.Len(t, matches, 1,
		"README.md must start a project with exactly one `go run github.com/bdrtr/gobit/"+
			"<dir>@latest <verb> <name>` line; this gate found %d. If the command changed "+
			"shape, change the pattern with it, because a gate that finds no line checks nothing",
		len(matches))
	dir, verb := matches[0][1], matches[0][2]

	packageDir := filepath.Join(repoRoot, filepath.FromSlash(dir))
	files, err := filepath.Glob(filepath.Join(packageDir, "*.go"))
	require.NoError(t, err)
	require.NotEmpty(t, files,
		"README.md starts a project with github.com/bdrtr/gobit/%s, and that directory has "+
			"no Go files. The binary moved: `go run` of that path fails for everyone who "+
			"reads the README first", dir)
	isMain := false
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), file, nil, parser.PackageClauseOnly)
		require.NoError(t, parseErr)
		isMain = isMain || parsed.Name.Name == "main"
	}
	require.True(t, isMain,
		"github.com/bdrtr/gobit/%s is not a main package, so `go run` of it runs nothing", dir)

	cmd := exec.CommandContext(t.Context(), goToolPath(t), "run", ".", "help")
	cmd.Dir = packageDir
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "the binary the README names could not print its help:\n%s", output)

	// The usage LINE, not the word: "new" also appears in the text below the
	// verbs, so a Contains would stay green with the verb gone (ADR 0154).
	usageLine := regexp.MustCompile(`(?m)^\s+\S+ ` + regexp.QuoteMeta(verb) + ` `)
	require.Regexp(t, usageLine, string(output),
		"README.md starts a project with the verb %q, and the binary at %s lists no such "+
			"verb in its usage. The dispatch changed and the front door did not", verb, dir)
}
