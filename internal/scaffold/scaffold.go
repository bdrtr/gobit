// Package scaffold writes a new project that embeds gobit (ADR 0154).
//
// # Why the templates are EMBEDDED
//
// Because the thing that generates a project is a released BINARY, and a binary
// that read its templates from disk would be a binary that only works inside a
// checkout of this repository. The same reasoning the admin panel's assets
// carry: run the binary, it works.
//
// # Why no template file is named "go.mod", and why none ends in ".go"
//
// Two traps, both measured rather than reasoned about.
//
// `//go:embed` silently EXCLUDES any directory that contains a file named
// "go.mod" — the whole directory, not the file — and an `all:` prefix does not
// lift it. A template tree with a literal go.mod in it therefore produces a
// binary that compiles, embeds part of the tree, walks it without error and
// generates an incomplete project. Nothing says so.
//
// And a template file ending in ".go" is parsed by two repository gates that
// walk every non-test Go file in the tree, plus by `go build ./...` itself;
// a main.go carrying `{{ .Module }}` breaks all three at once.
//
// So the embedded names are neutral ("gomod.tmpl", "main.go.tmpl") and the
// mapping from template name to written name is DATA, in [files].
//
// # Why the list is checked in both directions
//
// [files] is hand-written and the embedded set is produced by the compiler, so
// the two can disagree: a template added to the directory and forgotten here
// would never be written, and a name here whose template was deleted would fail
// at generation time for one user rather than at build time for everybody. The
// check runs at package init, the way the admin panel checks its own page list.
package scaffold

import (
	"embed"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"text/template"
)

//go:embed templates
var templateFiles embed.FS

// templateDir is the embedded directory's name.
const templateDir = "templates"

// files maps each embedded template to the path it is written to.
//
// The written names are what a Go project looks like; the template names are
// what `//go:embed` and this repository's own gates allow (see the package
// documentation). Nothing derives one from the other, because the mapping is not
// a rule — it is a decision per file.
var files = map[string]string{
	"gomod.tmpl":     "go.mod",
	"main.go.tmpl":   "main.go",
	"env.tmpl":       ".env",
	"gitignore.tmpl": ".gitignore",
	"readme.tmpl":    "README.md",
	"compose.tmpl":   "docker-compose.yml",
}

// init checks the hand-written list against the embedded set in BOTH
// directions.
//
// A panic is right here and nowhere else: both sides are fixed at compile time,
// so a disagreement means the binary was built wrongly rather than that an
// installation is misconfigured.
func init() {
	embedded, err := fs.ReadDir(templateFiles, templateDir)
	if err != nil {
		panic("scaffold: the embedded template directory could not be read: " + err.Error())
	}

	present := map[string]bool{}
	for _, entry := range embedded {
		present[entry.Name()] = true
	}

	for name := range files {
		if !present[name] {
			panic("scaffold: " + name + " is in the file list but is not embedded")
		}
	}
	for name := range present {
		if _, listed := files[name]; !listed {
			panic("scaffold: " + name + " is embedded but is in no file list entry")
		}
	}
}

// Input is what a generated project needs to be written.
type Input struct {
	// Dir is the directory to create; it must not exist.
	Dir string
	// Module is the generated project's own module path.
	Module string
	// GobitVersion is the version the generated go.mod REQUIRES.
	//
	// It is empty when the generating binary cannot name one (see
	// [Replace]); in that case Replace must be set instead.
	GobitVersion string
	// Replace, when set, makes the generated go.mod point at a gobit CHECKOUT
	// instead of a released version.
	//
	// It exists for two callers and no others: this repository's own test lane,
	// and a developer working against an unreleased tree. A generated project
	// carrying it is NOT portable — the path is absolute on one machine — and
	// the generated README says so.
	Replace string
	// GoVersion is the `go` directive written into the generated go.mod.
	GoVersion string
}

// values is what the templates are rendered with.
//
// It is a separate type from [Input] so a template cannot reach a field that is
// an instruction to the generator rather than a fact about the project.
type values struct {
	Module       string
	Binary       string
	GobitVersion string
	Replace      string
	GoVersion    string
	GobitModule  string
}

// GobitModule is the import path of the library a generated project embeds.
const GobitModule = "github.com/bdrtr/gobit"

// Write generates the project.
//
// The directory must NOT exist. Writing into an existing one was refused rather
// than merged: the files this generates are a project's own identity — its
// module path, its .env, its go.mod — and quietly overwriting somebody's go.mod
// is not a thing a scaffolding command gets to do.
func Write(in Input) error {
	if strings.TrimSpace(in.Dir) == "" {
		return fmt.Errorf("scaffold: the target directory is required")
	}
	if strings.TrimSpace(in.Module) == "" {
		return fmt.Errorf("scaffold: the module path is required")
	}
	if in.GobitVersion == "" && in.Replace == "" {
		return fmt.Errorf(
			"scaffold: neither a %s version nor a replacement checkout was given, so the "+
				"generated project would require nothing and could not build", GobitModule)
	}
	if _, err := os.Stat(in.Dir); err == nil {
		return fmt.Errorf("scaffold: %s already exists; a project is written into a NEW directory", in.Dir)
	}

	if err := os.MkdirAll(in.Dir, 0o750); err != nil {
		return fmt.Errorf("scaffold: %s could not be created: %w", in.Dir, err)
	}

	data := values{
		Module:       in.Module,
		Binary:       path.Base(in.Module),
		GobitVersion: in.GobitVersion,
		Replace:      in.Replace,
		GoVersion:    in.GoVersion,
		GobitModule:  GobitModule,
	}

	// The names are written in a FIXED order so a failure halfway leaves the
	// same partial tree every time, which is what makes the failure reportable.
	for _, name := range slices.Sorted(maps.Keys(files)) {
		if err := writeFile(in.Dir, name, files[name], data); err != nil {
			return err
		}
	}

	return nil
}

// writeFile renders one template into the project.
func writeFile(dir, templateName, target string, data values) error {
	raw, err := templateFiles.ReadFile(templateDir + "/" + templateName)
	if err != nil {
		return fmt.Errorf("scaffold: %s could not be read: %w", templateName, err)
	}

	// Option("missingkey=error") is the whole reason a template cannot ship a
	// half-rendered project: a field renamed on one side and not the other
	// fails HERE rather than writing "<no value>" into somebody's go.mod.
	tpl, err := template.New(templateName).Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return fmt.Errorf("scaffold: %s could not be parsed: %w", templateName, err)
	}

	// The name comes from [files], a package constant, joined onto the directory
	// the caller named — so the only variable part is the target the caller
	// already chose. O_EXCL is what keeps it from overwriting anything.
	target = filepath.Join(dir, target)
	handle, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304: the name is a package constant joined onto the caller's own directory
	if err != nil {
		return fmt.Errorf("scaffold: %s could not be created: %w", target, err)
	}
	defer func() { _ = handle.Close() }()

	if err := tpl.Execute(handle, data); err != nil {
		return fmt.Errorf("scaffold: %s could not be rendered: %w", templateName, err)
	}

	return handle.Close()
}

// Written returns the paths [Write] creates, relative to the project.
//
// It is published for the tests and for the command's own report: a generator
// that told the user nothing about what it wrote would leave them to find out
// with ls.
func Written() []string { return slices.Sorted(maps.Values(files)) }
