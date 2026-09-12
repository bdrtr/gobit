package storefront

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"html/template"
	"net/http"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// The shop's error codes. A client may branch on these; messages change, codes
// do not.
const (
	// codeNoHandle reports an address that names no product.
	codeNoHandle = "storefront_no_handle"
	// codePageFailed reports a page the shop could not draw.
	codePageFailed = "storefront_page_failed"
)

// files holds the shop's page and its script, EMBEDDED in the binary.
//
// The same requirement gobit's own panel carries: the delivery promise is "run
// the binary, it works", and a template read from disk is a second artifact that
// has to travel with it.
//
//go:embed templates/page.gohtml assets/storefront.js
var files embed.FS

// page is the one template all three pages render.
//
// One shell, three mounts. The pages differ in what the script does with them,
// not in their markup, and a second template would be a second place to change
// the header.
var page = template.Must(template.ParseFS(files, "templates/page.gohtml"))

// script is the shop's client and its content stamp.
//
// The stamp goes in the ADDRESS as well as the header, because the response says
// `immutable`: at a fixed address a browser that has the old copy would not ask
// again for a year. gobit's own panel learned this the same way (D94).
var (
	script      = mustRead("assets/storefront.js")
	scriptStamp = stampOf(script)
)

// contentSecurityPolicy is the shop's own policy.
//
// It is not the panel's, and it cannot be: a shop shows PRODUCT IMAGES, and the
// panel's policy starts at `default-src 'none'` with no img-src at all. Nothing
// published carries a policy for an embedder's own pages, so this is the
// example's, written where the pages are.
//
// `connect-src 'self'` is what lets the script reach /store/v1 — same origin,
// which is the whole reason the shop runs in gobit's process.
const contentSecurityPolicy = "default-src 'none'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; " +
	"connect-src 'self'; " +
	"form-action 'none'; " +
	"base-uri 'none'; " +
	"frame-ancestors 'none'"

// securityHeaders puts the shop's policy on every page it serves.
//
// Installed once on the group rather than per handler: a call per handler is a
// rule that holds until somebody adds a handler, and the next one is written by
// copying a neighbor.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("Content-Security-Policy", contentSecurityPolicy)
		header.Set("Referrer-Policy", "no-referrer")
		header.Set("X-Frame-Options", "DENY")

		next.ServeHTTP(w, r)
	})
}

// view is what the shell carries into the browser.
//
// The key and the channel are ATTRIBUTES rather than script text: the script is
// one immutable file shared by every installation, and a value baked into it
// would make the stamp installation-specific and the file uncacheable.
type view struct {
	Title   string
	Mount   string
	Handle  string
	Script  string
	Key     string
	Channel string
	Paths   struct{ List, Cart string }
}

// render writes one page.
func (m *Module) render(w http.ResponseWriter, r *http.Request, mount, title, handle string) {
	data := view{
		Title:   title,
		Mount:   mount,
		Handle:  handle,
		Script:  ScriptPath + "?v=" + scriptStamp,
		Key:     m.opts.PublishableKey,
		Channel: m.opts.SalesChannelID,
	}
	data.Paths.List = ListPath
	data.Paths.Cart = CartPath

	var body bytes.Buffer
	if err := page.Execute(&body, data); err != nil {
		// The template is embedded and parsed at startup, so a failure here is
		// this file's bug rather than a condition. It is still answered rather
		// than panicked: a shop that stops serving is worse than a page that
		// says it could not be drawn.
		//
		// The answer goes through the core's writer, which is the rule for every
		// HTTP surface in this repository and one an example has no license to
		// skip: the envelope carries the request id, and the message a shopper
		// sees is the one the core decides to show.
		corehttp.WriteError(r.Context(), w, coreerrors.Internal(codePageFailed,
			"the page could not be drawn"))

		return
	}

	corehttp.WriteHTML(r.Context(), w, http.StatusOK, body.Bytes())
}

// showList renders the catalog.
func (m *Module) showList(w http.ResponseWriter, r *http.Request) {
	m.render(w, r, "list", "Shop", "")
}

// showProduct renders one product, named by its handle.
func (m *Module) showProduct(w http.ResponseWriter, r *http.Request) {
	handle := chi.URLParam(r, "handle")
	if handle == "" {
		corehttp.WriteError(r.Context(), w, coreerrors.NotFound(codeNoHandle,
			"the address names no product"))

		return
	}

	m.render(w, r, "product", handle, handle)
}

// showCart renders the shopper's cart.
func (m *Module) showCart(w http.ResponseWriter, r *http.Request) {
	m.render(w, r, "cart", "Cart", "")
}

// serveScript writes the shop's client.
//
// It is served PUBLIC: the bytes are identical in every installation and behind
// no privilege, which is the distinction gobit's own asset writer draws (D95).
func (m *Module) serveScript(w http.ResponseWriter, r *http.Request) {
	corehttp.WriteAsset(r.Context(), w,
		"text/javascript; charset=utf-8", `"`+scriptStamp+`"`, script)
}

// mustRead reads an embedded file or panics; a failure means the embed directive
// and the file have drifted apart, which is a build-time mistake.
func mustRead(name string) []byte {
	body, err := files.ReadFile(name)
	if err != nil {
		panic(err)
	}

	return body
}

// stampOf derives a content stamp from the bytes.
func stampOf(body []byte) string {
	sum := sha256.Sum256(body)

	return hex.EncodeToString(sum[:16])
}
