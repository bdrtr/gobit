package adminui

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
	"slices"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// templateFiles holds the panel's templates and is EMBEDDED IN THE BINARY.
//
// Embedding is not a convenience but a requirement of the repository's delivery
// promise: "run the binary, it works". Templates read from disk are a second
// artifact that has to travel next to the binary, and a failure that only
// appears on the first request when the working directory is wrong.
//
//go:embed templates/*.gohtml
var templateFiles embed.FS

// layoutFile is the outer frame every page is rendered into.
const layoutFile = "templates/layout.gohtml"

// pages lists the panel pages that are looked up BY NAME at runtime.
//
// The list is maintained by hand, deliberately. A page name is a STRING: a typo
// compiles, lints clean, and blows up only when that page is opened — in front
// of a user. [loadTemplates] verifies at startup that every listed page is
// really embedded, which pulls the failure back to startup.
//
// The list is also checked for STALENESS in the other direction: a file that is
// embedded but listed nowhere is dead weight, and that fails too. A one-way
// check would miss the change that forgot to delete a file.
var pages = []string{
	"login.gohtml",
	"error.gohtml",
}

// templateSet maps a page name to that page's parsed template set.
//
// # Why one set PER PAGE
//
// Each page is parsed TOGETHER with the layout and defines its own "body"
// block. Collected into a single set, only the last block of a given name would
// survive and every page would render the same body — silently.
//
// The alternative was passing the body into the layout as a VALUE, which
// requires converting it to template.HTML, and template.HTML DISABLES escaping.
// In a panel that runs inside an administrator's session, disabling escaping is
// the shortest path to handing an attacker admin privileges.
type templateSet struct {
	sets map[string]*template.Template
}

// loadTemplates parses the embedded templates and verifies their names.
//
// It is called AT STARTUP and returns an error; it never panics. The
// composition root turns the error into an exit code, so a broken template
// keeps the server from starting at all. The alternative — panicking via
// template.Must — reaches the same outcome but routes the failure through the
// runtime's panic path instead of the composition root's error path.
func loadTemplates() (*templateSet, error) {
	embedded, err := embeddedPages()
	if err != nil {
		return nil, err
	}

	var missing []string
	for _, name := range pages {
		if !slices.Contains(embedded, name) {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, errors.Internal(CodeTemplateInvalid,
			"page(s) the panel expects are not embedded: %s (embedded: %s)",
			strings.Join(missing, ", "), strings.Join(embedded, ", "))
	}

	var extra []string
	for _, name := range embedded {
		if !slices.Contains(pages, name) {
			extra = append(extra, name)
		}
	}
	if len(extra) > 0 {
		return nil, errors.Internal(CodeTemplateInvalid,
			"page(s) embedded but referenced nowhere: %s; the pages list must be stale",
			strings.Join(extra, ", "))
	}

	sets := make(map[string]*template.Template, len(pages))
	for _, name := range pages {
		set, parseErr := template.New(name).ParseFS(templateFiles, layoutFile, "templates/"+name)
		if parseErr != nil {
			return nil, errors.Wrap(parseErr, errors.KindInternal, CodeTemplateInvalid,
				"panel page could not be parsed: %s", name)
		}
		sets[name] = set
	}

	return &templateSet{sets: sets}, nil
}

// embeddedPages returns the embedded template files other than the layout.
func embeddedPages() ([]string, error) {
	entries, err := templateFiles.ReadDir("templates")
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeTemplateInvalid,
			"embedded template directory could not be read")
	}

	layout := strings.TrimPrefix(layoutFile, "templates/")
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == layout {
			continue
		}
		out = append(out, entry.Name())
	}
	slices.Sort(out)

	return out, nil
}

// render builds the page IN MEMORY first and only then writes the response.
//
// The order is mandatory and the reason is measured: streamed straight to the
// writer, an error arising midway through a template would leave a HALF-written
// page carrying a 200 status — once the header is out, neither the panic
// recoverer nor the error writer can do anything and the failure goes silent on
// the client. The buffer keeps the error somewhere it can still become a 500.
//
// The entry point is the LAYOUT, not the page: a page only defines its "body"
// block and the layout draws the frame.
func (t *templateSet) render(w http.ResponseWriter, r *http.Request, status int, page string, data any) {
	set, ok := t.sets[page]
	if !ok {
		corehttp.WriteError(r.Context(), w, errors.Internal(CodeTemplateInvalid,
			"the panel has no such page: %s", page))
		return
	}

	var buf bytes.Buffer
	if err := set.ExecuteTemplate(&buf, strings.TrimPrefix(layoutFile, "templates/"), data); err != nil {
		corehttp.WriteError(r.Context(), w, errors.Wrap(err, errors.KindInternal,
			CodeTemplateInvalid, "panel page could not be rendered: %s", page))
		return
	}
	corehttp.WriteHTML(r.Context(), w, status, buf.Bytes())
}
