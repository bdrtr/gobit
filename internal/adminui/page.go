package adminui

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// A screen a plugin put in the panel (ADR 0155).
//
// # Why the type is declared HERE
//
// The composition root hands these in; the published form a plugin author names
// is core/plugin's AdminPage. This package does not import that one — the
// consumer declares its own (ADR 0001) — so the panel stays a thing the plugin
// system feeds rather than a thing it depends on.
//
// # What a page may and may not be
//
// A shell and a script, and nothing else. The panel renders the shell from its
// own template with its own layout, and the script fills it from /admin/v1 with
// the operator's session — ADR 0030's shape, which the review screen was the
// first of. A plugin never ships a template, which is what keeps ADR 0030's
// rejected alternative rejected: a server-renderer extension point would put
// every plugin's markup in this binary.

// The template this package renders a registered screen with, and the key its
// script path is looked up under.
const (
	pluginPageTemplate = "plugin_page.gohtml"
	pageScriptKey      = "PageScript"
)

// scriptSuffix is appended to a page's path to address its script.
//
// Derived rather than registered: a second field would let the two drift, and a
// page whose script path pointed at another page's script is a screen that runs
// somebody else's code with the operator's session.
const scriptSuffix = ".js"

// Page is one registered screen.
type Page struct {
	// Label is what the navigation shows.
	Label string
	// Path is the panel path the shell answers on.
	Path string
	// Script is the screen's client, served from the panel's own origin.
	Script []byte
}

// pageScreen is a validated page plus what serving it needs.
type pageScreen struct {
	page Page
	etag string
}

// scriptPath is where this screen's script is served.
func (p pageScreen) scriptPath() string { return p.page.Path + scriptSuffix }

// validatePages turns the registrations into screens, or refuses.
//
// # Every refusal here is a STARTUP failure
//
// A malformed registration is a wiring mistake rather than a configuration, and
// the panel's own rule for that is already written down: a missing module is
// configuration and answers 503, a malformed registration stops the process.
// Two of these are worth naming for what they would otherwise be:
//
// A path outside the panel's prefix would be bound where the panel's session
// ring does NOT run — a screen carrying an operator's data with no operator
// check. A path colliding with a screen the panel ships would let a plugin take
// over the catalog or the orders, silently, depending on registration order.
func validatePages(pages []Page) ([]pageScreen, error) {
	builtIn := map[string]bool{}
	for _, item := range sections() {
		builtIn[item.Path] = true
	}

	seen := map[string]bool{}
	out := make([]pageScreen, 0, len(pages))

	for _, page := range pages {
		switch {
		case strings.TrimSpace(page.Label) == "":
			return nil, errors.Internal(CodeNotReady,
				"an admin page was registered with no label; nothing would name it in the menu")
		case len(page.Script) == 0:
			return nil, errors.Internal(CodeNotReady,
				"the %q admin page carries no script, so its shell would render an empty "+
					"box forever", page.Label)
		case !strings.HasPrefix(page.Path, URLPrefix+"/"):
			return nil, errors.Internal(CodeNotReady,
				"the %q admin page asks for %q, which is outside the panel's prefix (%s). "+
					"It would be bound where the panel's session ring does not run — an "+
					"operator's screen with no operator check",
				page.Label, page.Path, URLPrefix)
		case strings.HasSuffix(page.Path, scriptSuffix):
			return nil, errors.Internal(CodeNotReady,
				"the %q admin page asks for %q, and a path ending in %s collides with the "+
					"address its own script is served on", page.Label, page.Path, scriptSuffix)
		case builtIn[page.Path]:
			return nil, errors.Internal(CodeNotReady,
				"the %q admin page asks for %q, which is a screen the panel ships",
				page.Label, page.Path)
		case seen[page.Path]:
			return nil, errors.Internal(CodeNotReady,
				"two admin pages ask for %q; one of them would never be reachable", page.Path)
		}

		seen[page.Path] = true
		out = append(out, pageScreen{page: page, etag: assetETag(page.Script)})
	}

	return out, nil
}

// pageRoutes binds every registered screen: the shell AND its script.
//
// Both, from one loop, deliberately. The panel has already been bitten by the
// mirror image of the split — a screen only somebody who knew the URL could open
// — and the reverse is just as easy to write: a navigation entry whose link
// answers 404. Bound together they cannot disagree.
func (u *UI) pageRoutes(r chi.Router) {
	for i := range u.pages {
		screen := u.pages[i]
		r.Get(screen.page.Path, u.showPage(screen))
		r.Get(screen.scriptPath(), u.servePageScript(screen))
	}
}

// showPage renders a registered screen's shell.
func (u *UI) showPage(screen pageScreen) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u.templates.render(w, r, http.StatusOK, pluginPageTemplate, map[string]any{
			titleKey:      screen.page.Label,
			apiPrefixKey:  corehttp.DefaultAdminPrefix,
			pageScriptKey: screen.scriptPath(),
		})
	}
}

// servePageScript writes a registered screen's client.
//
// The type is the panel's own script type and the stamp is derived from the
// bytes, exactly as the review screen's is: a release that changes a plugin's
// script gets a new stamp automatically.
func (u *UI) servePageScript(screen pageScreen) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		corehttp.WriteAsset(r.Context(), w, reviewsScriptType, screen.etag, screen.page.Script)
	}
}

// navItemsOf turns the registered screens into menu entries.
//
// The two come from ONE list, which is the whole point: the route and the entry
// are bound from the same slice, in the same construction, so a screen cannot
// have one without the other.
func navItemsOf(screens []pageScreen) []navItem {
	out := make([]navItem, 0, len(screens))
	for i := range screens {
		out = append(out, navItem{Label: screens[i].page.Label, Path: screens[i].page.Path})
	}

	return out
}
