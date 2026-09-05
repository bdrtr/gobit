package adminui

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
	"slices"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
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

// titleKey is the data key the layout reads the page title from.
//
// It is a constant because the layout looks it up BY NAME: a typo in one page's
// data map would render that page with an empty <title> and nothing would fail.
const titleKey = "Title"

// errorKey is the template key carrying a message meant for the operator.
//
// It is a constant for the same reason titleKey is: three pages fill it and a
// typo in one of them would not fail, it would silently stop showing the
// operator why their edit was refused.
const errorKey = "Error"

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
	"products.gohtml",
	"product.gohtml",
	"product_edit.gohtml",
	"variant.gohtml",
	"orders.gohtml",
	"order.gohtml",
	"customers.gohtml",
	"customer.gohtml",
	"inventory.gohtml",
	"sales.gohtml",
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
func (t *templateSet) render(
	w http.ResponseWriter, r *http.Request, status int, page string, data map[string]any,
) {
	if data == nil {
		data = map[string]any{}
	}

	// The frame's own fields are filled in HERE rather than by each page. A page
	// that forgot one would render without a stylesheet or without its menu, and
	// nothing would fail — the template would simply see an empty value. Putting
	// them in one place makes forgetting impossible instead of unlikely.
	decorateFrame(r, data)

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

// The data keys the LAYOUT reads for its frame.
//
// They are constants for the reason titleKey is: the layout looks them up by
// name, so a typo would not fail — it would silently render a page with no
// stylesheet or no menu.
const (
	stylesheetKey = "StylesheetPath"
	logoutKey     = "LogoutPath"
	signedInKey   = "SignedIn"
	navKey        = "Nav"
)

// navItem is one entry of the panel's menu.
type navItem struct {
	// Label is what the operator reads.
	Label string
	// Path is where it goes.
	Path string
	// Current marks the section the request is in; the layout turns it into
	// aria-current, which is what the stylesheet keys on AND what a screen
	// reader announces — one fact rather than two that can drift.
	Current bool
}

// sections are the panel's menu, in the order they are shown.
//
// It is a list rather than markup in the template so that a section added to
// the panel enters the menu by being added HERE, next to the route that serves
// it, rather than in a file nobody edits when adding a handler.
func sections() []navItem {
	return []navItem{
		{Label: catalogLabel, Path: ProductsPath},
		{Label: ordersLabel, Path: OrdersPath},
		// The report sits next to the orders it is made of rather than at the
		// end of the menu: an operator who has just looked at one order and now
		// wants the period around it should not have to cross the whole menu to
		// get there.
		{Label: salesLabel, Path: SalesPath},
		{Label: customersLabel, Path: CustomersPath},
		{Label: inventoryLabel, Path: InventoryPath},
	}
}

// decorateFrame fills in the fields the layout draws around every page.
func decorateFrame(r *http.Request, data map[string]any) {
	data[stylesheetKey] = StylesheetPath
	data[logoutKey] = LogoutPath

	// The sign-out control appears only when there is a session to end. On the
	// login page there is none, and a button that logs nobody out would be an
	// invitation to a confusing click.
	_, signedIn := corehttp.PrincipalFromContext(r.Context())
	data[signedInKey] = signedIn

	if !signedIn {
		return
	}

	items := sections()
	for i := range items {
		// The section is current when the request is inside it, so a product's
		// own page keeps "Catalog" marked rather than leaving the menu blank on
		// every detail screen.
		items[i].Current = r.URL.Path == items[i].Path ||
			strings.HasPrefix(r.URL.Path, items[i].Path+"/")
	}

	data[navKey] = items
}

// The data keys the LIST templates read their paging from.
//
// They are constants for the reason [titleKey] is: the templates look them up
// by name, so a typo would not fail — the page would simply lose its "next"
// link and nobody would be told.
const (
	pageKey     = "Page"
	hasNextKey  = "HasNext"
	hasPrevKey  = "HasPrev"
	nextPageKey = "NextPage"
	prevPageKey = "PrevPage"
	pathKey     = "Path"
)

// addPaging writes the paging fields a list template reads.
//
// It exists because five screens page identically, and five copies of
// "page - 1" are five places for one of them to become "page + 1" without
// anything failing. The screens supply what only they know — which page they
// are on, whether the read returned one row more than the page holds, and their
// own path — and the arithmetic happens once.
//
// What it does NOT write is the rest of the query string. The sales report
// carries a date range in the address and appends it to its own paging links,
// because a "next page" that dropped the period would move the reader to a
// different report without saying so; a helper that guessed which parameters to
// carry would be guessing for every screen at once.
func addPaging(data map[string]any, page int, hasNext bool, path string) {
	data[pageKey] = page
	data[hasNextKey] = hasNext
	data[hasPrevKey] = page > 1
	data[nextPageKey] = page + 1
	data[prevPageKey] = page - 1
	data[pathKey] = path
}
